use super::runtime_proto::{
    KafkaStreamPayloadV1, MailConsumerDeleteV1, MailConsumerDesiredState, MailConsumerUpsertV1,
    MailEventMetadataV1, MailMessageMappingV1, MailProjectionReconcileCompletedV1,
    MailStreamSourceV1, MailStreamType, MailTemplateDeletedV1, MailTemplateVersionPublishedV1,
};
use crate::config::Config;
use crate::observability::logger::Logger;
use chrono::{DateTime, Utc};
use prost::Message;
use serde::Deserialize;
use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tokio::time::MissedTickBehavior;
use tokio_postgres::NoTls;
use uuid::Uuid;

const CONSUMER_EVENT_NAMESPACE: &str = "43de31a4-0c86-54e9-8384-47b33f541c28";
const PERSONAL_TEMPLATE_EVENT_NAMESPACE: &str = "9314352a-19ba-5808-b8e2-14e06df7b791";
const TENANT_TEMPLATE_EVENT_NAMESPACE: &str = "92712973-d86b-5e59-9a86-9bf5726c9981";
const RECONCILE_COMPLETION_NAMESPACE: &str = "e295a8c6-c04f-56f3-9577-f53521006bb9";

#[derive(Deserialize)]
struct StoredMessageMapping {
    #[serde(default)]
    external_message_id_json_path: String,
    recipient_json_path: String,
    #[serde(default)]
    variable_json_paths: HashMap<String, String>,
}

// [COMMENT]: Đây là transport primitive duy nhất được dùng chung; business query/encode vẫn tách theo từng flow.
async fn xadd_mail_projection_command(
    redis_conn: &mut redis::aio::MultiplexedConnection,
    zone_id: Uuid,
    event_id: Uuid,
    job_topic: &str,
    resource_id: &str,
    payload: &[u8],
    generation: u64,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    // [COMMENT]: Fencing check và XADD cùng Lua; owner cũ không enqueue sau khi lock đã sang generation mới.
    let stream_id: String = redis::Script::new(
        r#"
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return '' end
return redis.call('XADD', KEYS[2], '*',
  'job_id', ARGV[2], 'job_version', '1', 'attempt', '0',
  'job_topic', ARGV[3], 'source_domain', 'MAIL', 'resource_id', ARGV[4],
  'payload_schema_version', '1', 'payload', ARGV[5], 'trace_id', '',
  'idle', '60', 'reconcile_generation', ARGV[1])
"#,
    )
    .key(format!("mail:reconcile:lock:{zone_id}"))
    .key(format!("jobs:{zone_id}"))
    .arg(generation)
    .arg(event_id.to_string())
    .arg(job_topic)
    .arg(resource_id)
    .arg(payload)
    .invoke_async(redis_conn)
    .await?;
    if stream_id.is_empty() {
        return Err("MAIL_RECONCILE_FENCED".into());
    }
    Ok(())
}

pub async fn run_periodic_mail_reconciliation(config: Config, redis_client: redis::Client) {
    // [COMMENT]: Một jitter cấp instance làm các JO replica không thức và tranh toàn bộ Zone cùng thời điểm.
    let instance_id = crate::config::get_node_hostname();
    let mut initial_hasher = std::collections::hash_map::DefaultHasher::new();
    instance_id.hash(&mut initial_hasher);
    "mail-periodic-reconciliation".hash(&mut initial_hasher);
    let initial_jitter_ms = initial_hasher.finish() % config.mail_reconcile_jitter_max_ms;
    tokio::time::sleep(Duration::from_millis(initial_jitter_ms)).await;

    let (pg_client, pg_connection) =
        match tokio_postgres::connect(&config.database_url, NoTls).await {
            Ok(connection) => connection,
            Err(error) => {
                Logger::sys_error(
                    "mail.reconcile",
                    "Không thể mở read-only PostgreSQL connection cho mail reconciler",
                    &error.to_string(),
                );
                return;
            }
        };
    tokio::spawn(async move {
        if let Err(error) = pg_connection.await {
            Logger::sys_error(
                "mail.reconcile.postgres",
                "Mail reconciliation PostgreSQL connection stopped",
                &error.to_string(),
            );
        }
    });
    if let Err(error) = pg_client
        .batch_execute(
            "SET default_transaction_read_only = on; \
             SET statement_timeout = '5s'; \
             SET lock_timeout = '1s'; \
             SET idle_in_transaction_session_timeout = '5s'",
        )
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
        config.mail_reconcile_scheduler_tick_secs,
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

            let mut redis_conn = match redis_client.get_multiplexed_tokio_connection().await {
                Ok(connection) => connection,
                Err(error) => {
                    Logger::sys_error(
                        "mail.reconcile.redis",
                        "Không thể kết nối Redis Job",
                        &error.to_string(),
                    );
                    break;
                }
            };
            let lock_key = format!("mail:reconcile:lock:{zone_id}");
            let generation_key = format!("mail:reconcile:generation:{zone_id}");
            let next_due_key = format!("mail:reconcile:next_due:{zone_id}");
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
            .arg(config.mail_reconcile_interval_secs * 1_000)
            .arg(config.mail_reconcile_lock_ttl_secs * 1_000)
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

            let checkpoint_key = format!("mail:reconcile:checkpoint:{zone_id}");
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

            for _ in 0..config.mail_reconcile_max_pages_per_run {
                if started.elapsed() >= Duration::from_secs(config.mail_reconcile_work_budget_secs)
                {
                    break;
                }

                let page = match phase.as_str() {
                    "personal_template_versions" => {
                        reconcile_personal_template_versions(
                            &pg_client,
                            &mut redis_conn,
                            zone_id,
                            &cursor_id,
                            cursor_version,
                            config.mail_reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "tenant_template_versions" => {
                        reconcile_tenant_template_versions(
                            &pg_client,
                            &mut redis_conn,
                            zone_id,
                            &cursor_id,
                            cursor_version,
                            config.mail_reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "personal_consumers" => {
                        reconcile_personal_consumers(
                            &pg_client,
                            &mut redis_conn,
                            zone_id,
                            &cursor_id,
                            config.mail_reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "tenant_consumers" => {
                        reconcile_tenant_consumers(
                            &pg_client,
                            &mut redis_conn,
                            zone_id,
                            &cursor_id,
                            config.mail_reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "personal_template_tombstones" => {
                        reconcile_personal_template_tombstones(
                            &pg_client,
                            &mut redis_conn,
                            zone_id,
                            &cursor_id,
                            config.mail_reconcile_page_size,
                            generation as u64,
                        )
                        .await
                    }
                    "tenant_template_tombstones" => {
                        reconcile_tenant_template_tombstones(
                            &pg_client,
                            &mut redis_conn,
                            zone_id,
                            &cursor_id,
                            config.mail_reconcile_page_size,
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

                if count == config.mail_reconcile_page_size as usize {
                    cursor_id = last_id;
                    cursor_version = last_version;
                } else {
                    cursor_id.clear();
                    cursor_version = 0;
                    phase = match phase.as_str() {
                        "personal_template_versions" => "tenant_template_versions".to_string(),
                        "tenant_template_versions" => "personal_consumers".to_string(),
                        "personal_consumers" => "tenant_consumers".to_string(),
                        "tenant_consumers" => "personal_template_tombstones".to_string(),
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
                            if let Err(error) = xadd_mail_projection_command(
                                &mut redis_conn,
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
                    config.mail_reconcile_interval_secs * 1_000
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

async fn reconcile_personal_template_versions(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    zone_id: Uuid,
    cursor_id: &str,
    cursor_version: i64,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    let rows = pg.query(
        "SELECT t.id, v.version, v.subject_template, v.html_template, v.content_sha256, v.created_at \
         FROM mail.personal_mail_templates t \
         JOIN mail.personal_mail_template_versions v ON v.template_id=t.id \
         JOIN hierarchy.personal_workspaces w ON w.id=t.workspace_id \
         WHERE w.zone_id=$1 AND (t.id, v.version) > ($2, $3) \
         ORDER BY t.id, v.version LIMIT $4",
        &[&zone_id, &cursor_id, &cursor_version, &limit],
    ).await?;
    let namespace = Uuid::parse_str(PERSONAL_TEMPLATE_EVENT_NAMESPACE)?;
    let mut last_id = String::new();
    let mut last_version = 0;
    for row in &rows {
        let template_id: String = row.get(0);
        let version: i64 = row.get(1);
        let created_at: DateTime<Utc> = row.get(5);
        let event_id = Uuid::new_v5(
            &namespace,
            format!("template:{template_id}:{version}:publish:{zone_id}").as_bytes(),
        );
        let event = MailTemplateVersionPublishedV1 {
            metadata: Some(MailEventMetadataV1 {
                event_id: event_id.as_bytes().to_vec(),
                schema_version: 1,
                occurred_at_unix_ms: created_at.timestamp_millis(),
                traceparent: String::new(),
                producer: "job-orchestrator-mail-reconciler".to_string(),
            }),
            template_id: template_id.clone(),
            template_revision: version as u64,
            template_version: version as u64,
            subject_template: row.get(2),
            html_template: row.get(3),
            content_sha256: row.get(4),
        };
        xadd_mail_projection_command(
            redis_conn,
            zone_id,
            event_id,
            "mail.template.version_published",
            &template_id,
            &event.encode_to_vec(),
            generation,
        )
        .await?;
        last_id = template_id;
        last_version = version;
    }
    Ok((rows.len(), last_id, last_version))
}

async fn reconcile_tenant_template_versions(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    zone_id: Uuid,
    cursor_id: &str,
    cursor_version: i64,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    let rows = pg.query(
        "SELECT t.id, v.version, v.subject_template, v.html_template, v.content_sha256, v.created_at \
         FROM mail.tenant_mail_templates t \
         JOIN mail.tenant_mail_template_versions v ON v.template_id=t.id \
         JOIN hierarchy.tenant_workspaces w ON w.id=t.workspace_id \
         WHERE w.zone_id=$1 AND (t.id, v.version) > ($2, $3) \
         ORDER BY t.id, v.version LIMIT $4",
        &[&zone_id, &cursor_id, &cursor_version, &limit],
    ).await?;
    let namespace = Uuid::parse_str(TENANT_TEMPLATE_EVENT_NAMESPACE)?;
    let mut last_id = String::new();
    let mut last_version = 0;
    for row in &rows {
        let template_id: String = row.get(0);
        let version: i64 = row.get(1);
        let created_at: DateTime<Utc> = row.get(5);
        let event_id = Uuid::new_v5(
            &namespace,
            format!("template:{template_id}:{version}:publish:{zone_id}").as_bytes(),
        );
        let event = MailTemplateVersionPublishedV1 {
            metadata: Some(MailEventMetadataV1 {
                event_id: event_id.as_bytes().to_vec(),
                schema_version: 1,
                occurred_at_unix_ms: created_at.timestamp_millis(),
                traceparent: String::new(),
                producer: "job-orchestrator-mail-reconciler".to_string(),
            }),
            template_id: template_id.clone(),
            template_revision: version as u64,
            template_version: version as u64,
            subject_template: row.get(2),
            html_template: row.get(3),
            content_sha256: row.get(4),
        };
        xadd_mail_projection_command(
            redis_conn,
            zone_id,
            event_id,
            "mail.template.version_published",
            &template_id,
            &event.encode_to_vec(),
            generation,
        )
        .await?;
        last_id = template_id;
        last_version = version;
    }
    Ok((rows.len(), last_id, last_version))
}

async fn reconcile_personal_consumers(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    zone_id: Uuid,
    cursor_id: &str,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    let rows = pg.query(
        "SELECT c.id,c.broker_resource_id,c.source_config_envelope,c.topic,c.consumer_group,c.mapping_json::text,\
                c.template_id,c.template_version,c.sender_profile_id,c.sender_version,c.desired_state::text,\
                c.parallelism,c.config_version,c.config_sha256,c.updated_at \
         FROM mail.mail_consumers c JOIN hierarchy.personal_workspaces w ON w.id=c.workspace_id \
         WHERE w.zone_id=$1 AND c.id::text > $2 ORDER BY c.id LIMIT $3",
        &[&zone_id, &cursor_id, &limit],
    ).await?;
    let namespace = Uuid::parse_str(CONSUMER_EVENT_NAMESPACE)?;
    let mut last_id = String::new();
    for row in &rows {
        let consumer_id: Uuid = row.get(0);
        let config_version: i64 = row.get(12);
        let desired_state: String = row.get(10);
        let updated_at: DateTime<Utc> = row.get(14);
        let (event_id, topic, payload) =
            if desired_state == "deleted" || desired_state == "deleting" {
                let event_id = Uuid::new_v5(
                    &namespace,
                    format!("consumer:{consumer_id}:{config_version}:delete:{zone_id}").as_bytes(),
                );
                let event = MailConsumerDeleteV1 {
                    metadata: Some(MailEventMetadataV1 {
                        event_id: event_id.as_bytes().to_vec(),
                        schema_version: 1,
                        occurred_at_unix_ms: updated_at.timestamp_millis(),
                        traceparent: String::new(),
                        producer: "job-orchestrator-mail-reconciler".to_string(),
                    }),
                    consumer_id: consumer_id.as_bytes().to_vec(),
                    config_version: config_version as u64,
                    drain_timeout_seconds: 0,
                    reason: "periodic-reconciliation".to_string(),
                };
                (event_id, "mail.consumer.delete", event.encode_to_vec())
            } else {
                let mapping: StoredMessageMapping = serde_json::from_str(&row.get::<_, String>(5))?;
                let event_id = Uuid::new_v5(
                    &namespace,
                    format!("consumer:{consumer_id}:{config_version}:upsert:{zone_id}").as_bytes(),
                );
                let broker_resource_id: Uuid = row.get(1);
                // [COMMENT]: Reconciler tái tạo adapter protobuf rồi đặt vào generic stream bytes; không giải mã envelope.
                let stream_payload = KafkaStreamPayloadV1 {
                    source_config_envelope: row.get::<_, Vec<u8>>(2),
                    topic: row.get(3),
                    consumer_group: row.get(4),
                }
                .encode_to_vec();
                let event = MailConsumerUpsertV1 {
                    metadata: Some(MailEventMetadataV1 {
                        event_id: event_id.as_bytes().to_vec(),
                        schema_version: 1,
                        occurred_at_unix_ms: updated_at.timestamp_millis(),
                        traceparent: String::new(),
                        producer: "job-orchestrator-mail-reconciler".to_string(),
                    }),
                    consumer_id: consumer_id.as_bytes().to_vec(),
                    config_version: config_version as u64,
                    stream: Some(MailStreamSourceV1 {
                        stream_type: MailStreamType::Kafka as i32,
                        payload_schema_version: 1,
                        broker_resource_id: broker_resource_id.as_bytes().to_vec(),
                        payload: stream_payload,
                    }),
                    mapping: Some(MailMessageMappingV1 {
                        external_message_id_json_path: mapping.external_message_id_json_path,
                        recipient_json_path: mapping.recipient_json_path,
                        variable_json_paths: mapping.variable_json_paths,
                    }),
                    template_id: row.get(6),
                    template_version: row.get::<_, i64>(7) as u64,
                    sender_profile_id: row.get(8),
                    sender_version: row.get::<_, i64>(9) as u64,
                    desired_state: if desired_state == "enabled" {
                        MailConsumerDesiredState::Enabled as i32
                    } else {
                        MailConsumerDesiredState::Paused as i32
                    },
                    parallelism: row.get::<_, i32>(11) as u32,
                    config_sha256: row.get(13),
                };
                (event_id, "mail.consumer.upsert", event.encode_to_vec())
            };
        xadd_mail_projection_command(
            redis_conn,
            zone_id,
            event_id,
            topic,
            &consumer_id.to_string(),
            &payload,
            generation,
        )
        .await?;
        last_id = consumer_id.to_string();
    }
    Ok((rows.len(), last_id, 0))
}

async fn reconcile_tenant_consumers(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    zone_id: Uuid,
    cursor_id: &str,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    let rows = pg.query(
        "SELECT c.id,c.broker_resource_id,c.source_config_envelope,c.topic,c.consumer_group,c.mapping_json::text,\
                c.template_id,c.template_version,c.sender_profile_id,c.sender_version,c.desired_state::text,\
                c.parallelism,c.config_version,c.config_sha256,c.updated_at \
         FROM mail.mail_consumers c JOIN hierarchy.tenant_workspaces w ON w.id=c.workspace_id \
         WHERE w.zone_id=$1 AND c.id::text > $2 ORDER BY c.id LIMIT $3",
        &[&zone_id, &cursor_id, &limit],
    ).await?;
    let namespace = Uuid::parse_str(CONSUMER_EVENT_NAMESPACE)?;
    let mut last_id = String::new();
    for row in &rows {
        let consumer_id: Uuid = row.get(0);
        let config_version: i64 = row.get(12);
        let desired_state: String = row.get(10);
        let updated_at: DateTime<Utc> = row.get(14);
        let (event_id, topic, payload) =
            if desired_state == "deleted" || desired_state == "deleting" {
                let event_id = Uuid::new_v5(
                    &namespace,
                    format!("consumer:{consumer_id}:{config_version}:delete:{zone_id}").as_bytes(),
                );
                let event = MailConsumerDeleteV1 {
                    metadata: Some(MailEventMetadataV1 {
                        event_id: event_id.as_bytes().to_vec(),
                        schema_version: 1,
                        occurred_at_unix_ms: updated_at.timestamp_millis(),
                        traceparent: String::new(),
                        producer: "job-orchestrator-mail-reconciler".to_string(),
                    }),
                    consumer_id: consumer_id.as_bytes().to_vec(),
                    config_version: config_version as u64,
                    drain_timeout_seconds: 0,
                    reason: "periodic-reconciliation".to_string(),
                };
                (event_id, "mail.consumer.delete", event.encode_to_vec())
            } else {
                let mapping: StoredMessageMapping = serde_json::from_str(&row.get::<_, String>(5))?;
                let event_id = Uuid::new_v5(
                    &namespace,
                    format!("consumer:{consumer_id}:{config_version}:upsert:{zone_id}").as_bytes(),
                );
                let broker_resource_id: Uuid = row.get(1);
                // [COMMENT]: Tenant giữ cùng outer discriminator; adapter bytes vẫn opaque với routing layer.
                let stream_payload = KafkaStreamPayloadV1 {
                    source_config_envelope: row.get::<_, Vec<u8>>(2),
                    topic: row.get(3),
                    consumer_group: row.get(4),
                }
                .encode_to_vec();
                let event = MailConsumerUpsertV1 {
                    metadata: Some(MailEventMetadataV1 {
                        event_id: event_id.as_bytes().to_vec(),
                        schema_version: 1,
                        occurred_at_unix_ms: updated_at.timestamp_millis(),
                        traceparent: String::new(),
                        producer: "job-orchestrator-mail-reconciler".to_string(),
                    }),
                    consumer_id: consumer_id.as_bytes().to_vec(),
                    config_version: config_version as u64,
                    stream: Some(MailStreamSourceV1 {
                        stream_type: MailStreamType::Kafka as i32,
                        payload_schema_version: 1,
                        broker_resource_id: broker_resource_id.as_bytes().to_vec(),
                        payload: stream_payload,
                    }),
                    mapping: Some(MailMessageMappingV1 {
                        external_message_id_json_path: mapping.external_message_id_json_path,
                        recipient_json_path: mapping.recipient_json_path,
                        variable_json_paths: mapping.variable_json_paths,
                    }),
                    template_id: row.get(6),
                    template_version: row.get::<_, i64>(7) as u64,
                    sender_profile_id: row.get(8),
                    sender_version: row.get::<_, i64>(9) as u64,
                    desired_state: if desired_state == "enabled" {
                        MailConsumerDesiredState::Enabled as i32
                    } else {
                        MailConsumerDesiredState::Paused as i32
                    },
                    parallelism: row.get::<_, i32>(11) as u32,
                    config_sha256: row.get(13),
                };
                (event_id, "mail.consumer.upsert", event.encode_to_vec())
            };
        xadd_mail_projection_command(
            redis_conn,
            zone_id,
            event_id,
            topic,
            &consumer_id.to_string(),
            &payload,
            generation,
        )
        .await?;
        last_id = consumer_id.to_string();
    }
    Ok((rows.len(), last_id, 0))
}

async fn reconcile_personal_template_tombstones(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    zone_id: Uuid,
    cursor_id: &str,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    let rows = pg.query(
        "SELECT t.template_id,t.template_revision,t.last_published_version,t.event_id,t.deleted_at \
         FROM mail.personal_mail_template_projection_tombstones t \
         JOIN hierarchy.personal_workspaces w ON w.id=t.workspace_id \
         WHERE w.zone_id=$1 AND t.template_id>$2 ORDER BY t.template_id LIMIT $3",
        &[&zone_id, &cursor_id, &limit],
    ).await?;
    let mut last_id = String::new();
    for row in &rows {
        let template_id: String = row.get(0);
        let revision: i64 = row.get(1);
        let event_id: Uuid = row.get(3);
        let deleted_at: DateTime<Utc> = row.get(4);
        let event = MailTemplateDeletedV1 {
            metadata: Some(MailEventMetadataV1 {
                event_id: event_id.as_bytes().to_vec(),
                schema_version: 1,
                occurred_at_unix_ms: deleted_at.timestamp_millis(),
                traceparent: String::new(),
                producer: "job-orchestrator-mail-reconciler".to_string(),
            }),
            template_id: template_id.clone(),
            template_revision: revision as u64,
            last_published_version: row.get::<_, i64>(2) as u64,
        };
        xadd_mail_projection_command(
            redis_conn,
            zone_id,
            event_id,
            "mail.template.deleted",
            &template_id,
            &event.encode_to_vec(),
            generation,
        )
        .await?;
        last_id = template_id;
    }
    Ok((rows.len(), last_id, 0))
}

async fn reconcile_tenant_template_tombstones(
    pg: &tokio_postgres::Client,
    redis_conn: &mut redis::aio::MultiplexedConnection,
    zone_id: Uuid,
    cursor_id: &str,
    limit: i64,
    generation: u64,
) -> Result<(usize, String, i64), Box<dyn std::error::Error + Send + Sync>> {
    let rows = pg.query(
        "SELECT t.template_id,t.template_revision,t.last_published_version,t.event_id,t.deleted_at \
         FROM mail.tenant_mail_template_projection_tombstones t \
         JOIN hierarchy.tenant_workspaces w ON w.id=t.workspace_id \
         WHERE w.zone_id=$1 AND t.template_id>$2 ORDER BY t.template_id LIMIT $3",
        &[&zone_id, &cursor_id, &limit],
    ).await?;
    let mut last_id = String::new();
    for row in &rows {
        let template_id: String = row.get(0);
        let revision: i64 = row.get(1);
        let event_id: Uuid = row.get(3);
        let deleted_at: DateTime<Utc> = row.get(4);
        let event = MailTemplateDeletedV1 {
            metadata: Some(MailEventMetadataV1 {
                event_id: event_id.as_bytes().to_vec(),
                schema_version: 1,
                occurred_at_unix_ms: deleted_at.timestamp_millis(),
                traceparent: String::new(),
                producer: "job-orchestrator-mail-reconciler".to_string(),
            }),
            template_id: template_id.clone(),
            template_revision: revision as u64,
            last_published_version: row.get::<_, i64>(2) as u64,
        };
        xadd_mail_projection_command(
            redis_conn,
            zone_id,
            event_id,
            "mail.template.deleted",
            &template_id,
            &event.encode_to_vec(),
            generation,
        )
        .await?;
        last_id = template_id;
    }
    Ok((rows.len(), last_id, 0))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn consumer_event_identity_matches_controlplane_uuid_v5_contract() {
        // [COMMENT]: Reconciler replay phải tạo đúng identity mà Go uuid.NewSHA1 tạo cho live event.
        let namespace = Uuid::parse_str(CONSUMER_EVENT_NAMESPACE).unwrap();
        let upsert = Uuid::new_v5(
            &namespace,
            b"consumer:00000000-0000-0000-0000-000000000001:7:upsert:00000000-0000-0000-0000-000000000002",
        );
        let delete = Uuid::new_v5(
            &namespace,
            b"consumer:00000000-0000-0000-0000-000000000001:8:delete:00000000-0000-0000-0000-000000000002",
        );
        assert_eq!(
            upsert,
            Uuid::parse_str("d9017c19-7a01-5ed8-a33b-5925217f2b6c").unwrap()
        );
        assert_eq!(
            delete,
            Uuid::parse_str("4c58e5e0-6031-5c02-b583-df423fc2a311").unwrap()
        );
    }
}
