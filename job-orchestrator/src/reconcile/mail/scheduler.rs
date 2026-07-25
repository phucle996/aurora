use super::consumer_tombstone::{
    reconcile_personal_consumer_tombstones, reconcile_tenant_consumer_tombstones,
};
use super::personal_consumer::reconcile_personal_consumers;
use super::personal_template::{
    reconcile_personal_template_tombstones, reconcile_personal_template_versions,
};
use super::tenant_consumer::reconcile_tenant_consumers;
use super::tenant_template::{
    reconcile_tenant_template_tombstones, reconcile_tenant_template_versions,
};
use super::{publish_mail_projection_command, RECONCILE_COMPLETION_NAMESPACE};
use crate::config::Config;
use crate::contracts::mail::{MailEventMetadataV1, MailProjectionReconcileCompletedV1};
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::Logger;
use chrono::Utc;
use prost::Message;
use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tokio::time::MissedTickBehavior;
use uuid::Uuid;

pub async fn run_periodic_mail_reconciliation(
    config: Config,
    redis_client: redis::Client,
    kafka: Arc<KafkaTransport>,
) {
    let mail_config = &config.workflows.mail;
    // [COMMENT]: Một jitter cấp instance làm các JO replica không thức và tranh toàn bộ Zone cùng thời điểm.
    let instance_id = crate::config::get_node_hostname();
    let mut initial_hasher = std::collections::hash_map::DefaultHasher::new();
    instance_id.hash(&mut initial_hasher);
    "mail-periodic-reconciliation".hash(&mut initial_hasher);
    let initial_jitter_ms = initial_hasher.finish() % mail_config.reconcile_jitter_max_ms;
    tokio::time::sleep(Duration::from_millis(initial_jitter_ms)).await;

    let pg_client =
        match crate::infra::postgres::connect(&config.postgres, "mail.reconcile.postgres").await {
            Ok(client) => client,
            Err(error) => {
                Logger::sys_error(
                    "mail.reconcile",
                    "Không thể mở read-only PostgreSQL connection cho mail reconciler",
                    &error.to_string(),
                );
                return;
            }
        };
    if let Err(error) = pg_client
        .batch_execute("SET default_transaction_read_only = on")
        .await
    {
        Logger::sys_error(
            "mail.reconcile.postgres",
            "Không thể khóa PostgreSQL reconciliation connection về read-only/time-bounded",
            &error.to_string(),
        );
        return;
    }

    let mut interval = tokio::time::interval(Duration::from_secs(
        mail_config.reconcile_scheduler_tick_secs,
    ));
    interval.set_missed_tick_behavior(MissedTickBehavior::Skip);

    loop {
        interval.tick().await;
        let zones = match pg_client
            .query(
                "SELECT id FROM hierarchy.zones WHERE status = 'active' ORDER BY id",
                &[],
            )
            .await
        {
            Ok(rows) => rows,
            Err(error) => {
                Logger::sys_error(
                    "mail.reconcile.zones",
                    "Không thể đọc danh sách Zone active",
                    &error.to_string(),
                );
                if pg_client.is_closed() {
                    return;
                }
                continue;
            }
        };

        for zone_row in zones {
            let zone_id: Uuid = zone_row.get(0);

            let mut redis_conn =
                match crate::infra::redis::multiplexed(&redis_client, &config.shared_redis).await {
                    Ok(connection) => connection,
                    Err(error) => {
                        Logger::sys_error(
                            "mail.reconcile.redis",
                            "Không thể kết nối Cache Redis",
                            &error.to_string(),
                        );
                        break;
                    }
                };
            // One zone is the atomic/fencing unit, so every Lua key must share
            // its Redis Cluster hash tag.
            let lock_key = format!("mail:reconcile:{{{zone_id}}}:lock");
            let generation_key = format!("mail:reconcile:{{{zone_id}}}:generation");
            let next_due_key = format!("mail:reconcile:{{{zone_id}}}:next-due");
            let now_ms = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap_or_default()
                .as_millis() as u64;
            let generation: i64 = match redis::Script::new(
                r#"
local due = tonumber(redis.call('GET', KEYS[3]) or '0')
if due > tonumber(ARGV[1]) then return 0 end
local token = redis.call('INCR', KEYS[2])
local acquired = redis.call('SET', KEYS[1], token, 'NX', 'PX', ARGV[3])
if not acquired then return 0 end
redis.call('SET', KEYS[3], tonumber(ARGV[1]) + tonumber(ARGV[2]))
return token
"#,
            )
            .key(&lock_key)
            .key(&generation_key)
            .key(&next_due_key)
            .arg(now_ms)
            .arg(mail_config.reconcile_interval_secs * 1_000)
            .arg(mail_config.reconcile_lock_ttl_secs * 1_000)
            .invoke_async(&mut redis_conn)
            .await
            {
                Ok(token) => token,
                Err(error) => {
                    Logger::sys_error(
                        "mail.reconcile.lock",
                        "Không thể thử fenced lock",
                        &error.to_string(),
                    );
                    continue;
                }
            };
            if generation == 0 {
                continue;
            }

            let checkpoint_key = format!("mail:reconcile:{{{zone_id}}}:checkpoint");
            let checkpoint: HashMap<String, String> = redis::cmd("HGETALL")
                .arg(&checkpoint_key)
                .query_async(&mut redis_conn)
                .await
                .unwrap_or_default();
            let mut phase = checkpoint
                .get("phase")
                .cloned()
                .unwrap_or_else(|| "personal_template_versions".to_string());
            let mut cursor_id = checkpoint.get("cursor_id").cloned().unwrap_or_default();
            let mut cursor_version = checkpoint
                .get("cursor_version")
                .and_then(|value| value.parse::<i64>().ok())
                .unwrap_or(0);
            let started = Instant::now();
            let mut completed_cycle = false;

            for _ in 0..mail_config.reconcile_max_pages_per_run {
                if started.elapsed() >= Duration::from_secs(mail_config.reconcile_work_budget_secs)
                {
                    break;
                }

                let page = match phase.as_str() {
                    "personal_template_versions" => {
                        reconcile_personal_template_versions(
                            &pg_client,
                            &mut redis_conn,
                            &kafka,
                            zone_id,
                            &cursor_id,
                            cursor_version,
                            mail_config.reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "tenant_template_versions" => {
                        reconcile_tenant_template_versions(
                            &pg_client,
                            &mut redis_conn,
                            &kafka,
                            zone_id,
                            &cursor_id,
                            cursor_version,
                            mail_config.reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "personal_consumers" => {
                        reconcile_personal_consumers(
                            &pg_client,
                            &mut redis_conn,
                            &kafka,
                            zone_id,
                            &cursor_id,
                            mail_config.reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "tenant_consumers" => {
                        reconcile_tenant_consumers(
                            &pg_client,
                            &mut redis_conn,
                            &kafka,
                            zone_id,
                            &cursor_id,
                            mail_config.reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "personal_consumer_tombstones" => {
                        reconcile_personal_consumer_tombstones(
                            &pg_client,
                            &mut redis_conn,
                            &kafka,
                            zone_id,
                            &cursor_id,
                            mail_config.reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "tenant_consumer_tombstones" => {
                        reconcile_tenant_consumer_tombstones(
                            &pg_client,
                            &mut redis_conn,
                            &kafka,
                            zone_id,
                            &cursor_id,
                            mail_config.reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "personal_template_tombstones" => {
                        reconcile_personal_template_tombstones(
                            &pg_client,
                            &mut redis_conn,
                            &kafka,
                            zone_id,
                            &cursor_id,
                            mail_config.reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "tenant_template_tombstones" => {
                        reconcile_tenant_template_tombstones(
                            &pg_client,
                            &mut redis_conn,
                            &kafka,
                            zone_id,
                            &cursor_id,
                            mail_config.reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    _ => Ok((0, String::new(), 0)),
                };

                let (count, last_id, last_version) = match page {
                    Ok(value) => value,
                    Err(error) => {
                        Logger::sys_error(
                            "mail.reconcile.page",
                            &format!("Reconcile Zone {zone_id}, phase {phase} thất bại"),
                            &error.to_string(),
                        );
                        break;
                    }
                };

                if count == mail_config.reconcile_page_size as usize {
                    cursor_id = last_id;
                    cursor_version = last_version;
                } else {
                    cursor_id.clear();
                    cursor_version = 0;
                    phase = match phase.as_str() {
                        "personal_template_versions" => "tenant_template_versions".to_string(),
                        "tenant_template_versions" => "personal_consumers".to_string(),
                        "personal_consumers" => "tenant_consumers".to_string(),
                        "tenant_consumers" => "personal_consumer_tombstones".to_string(),
                        "personal_consumer_tombstones" => "tenant_consumer_tombstones".to_string(),
                        "tenant_consumer_tombstones" => "personal_template_tombstones".to_string(),
                        "personal_template_tombstones" => "tenant_template_tombstones".to_string(),
                        "tenant_template_tombstones" => {
                            let completion_id = Uuid::new_v5(
                                &Uuid::parse_str(RECONCILE_COMPLETION_NAMESPACE).unwrap(),
                                format!("zone:{zone_id}:generation:{generation}").as_bytes(),
                            );
                            let completed_at = Utc::now().timestamp_millis();
                            let event = MailProjectionReconcileCompletedV1 {
                                metadata: Some(MailEventMetadataV1 {
                                    event_id: completion_id.as_bytes().to_vec(),
                                    schema_version: 1,
                                    occurred_at_unix_ms: completed_at,
                                    traceparent: String::new(),
                                    producer: "job-orchestrator-mail-reconciler".to_string(),
                                }),
                                reconcile_generation: generation as u64,
                                completed_at_unix_ms: completed_at,
                            };
                            if let Err(error) = publish_mail_projection_command(
                                &mut redis_conn,
                                &kafka,
                                zone_id,
                                completion_id,
                                "mail.projection.reconcile_completed",
                                &zone_id.to_string(),
                                &event.encode_to_vec(),
                                generation as u64,
                            )
                            .await
                            {
                                Logger::sys_error(
                                    "mail.reconcile.complete",
                                    "Không thể enqueue completion marker",
                                    &error.to_string(),
                                );
                                break;
                            }
                            completed_cycle = true;
                            "personal_template_versions".to_string()
                        }
                        _ => "personal_template_versions".to_string(),
                    };
                }

                let checkpoint_saved: i64 = redis::Script::new(
                    r#"
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('HSET', KEYS[2], 'phase', ARGV[2], 'cursor_id', ARGV[3],
  'cursor_version', ARGV[4], 'updated_at_unix_ms', ARGV[5])
return 1
"#,
                )
                .key(&lock_key)
                .key(&checkpoint_key)
                .arg(generation)
                .arg(&phase)
                .arg(&cursor_id)
                .arg(cursor_version)
                .arg(Utc::now().timestamp_millis())
                .invoke_async(&mut redis_conn)
                .await
                .unwrap_or(0);
                if checkpoint_saved == 0 {
                    break;
                }
            }

            // [COMMENT]: Incomplete cycle quay lại sau 5s; full cycle mới nghỉ theo periodic interval.
            let next_due_ms = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap_or_default()
                .as_millis() as u64
                + if completed_cycle {
                    mail_config.reconcile_interval_secs * 1_000
                } else {
                    5_000
                };
            let _: redis::RedisResult<i64> = redis::Script::new(
                "if redis.call('GET', KEYS[1]) == ARGV[1] then redis.call('SET', KEYS[2], ARGV[2]); return 1 else return 0 end",
            )
            .key(&lock_key)
            .key(&next_due_key)
            .arg(generation)
            .arg(next_due_ms)
            .invoke_async(&mut redis_conn)
            .await;

            // [COMMENT]: Compare-and-delete; owner cũ không thể xóa lock token mới sau pause/TTL.
            let _: redis::RedisResult<i64> = redis::Script::new(
                "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end",
            )
            .key(&lock_key)
            .arg(generation)
            .invoke_async(&mut redis_conn)
            .await;
        }
    }
}
