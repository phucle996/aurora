use super::super::runtime_proto::{MailConsumerRuntimeReportBatchV1, MailConsumerRuntimeState};
use crate::config::Config;
use crate::observability::logger::Logger;
use chrono::{Duration as ChronoDuration, Utc};
use prost::Message;
use serde::{Deserialize, Serialize};
use tokio_postgres::NoTls;
use uuid::Uuid;

const CONSUMER_REPORT_STREAM: &str = "mail:consumer:reports";
const CONSUMER_REPORT_GROUP: &str = "job-orchestrator-mail-consumer-v2";

#[derive(Clone, Deserialize, Serialize)]
struct SlotRuntimeSnapshot {
    config_version: u64,
    runtime_epoch: String,
    runtime_generation: u64,
    report_sequence: u64,
    state: String,
    consumer_lag: u64,
    error_code: String,
    error_message: String,
    observed_at_unix_ms: i64,
    expires_at_unix_ms: i64,
}

#[derive(Serialize)]
struct ConsumerRuntimeSnapshot<'a> {
    config_version: u64,
    runtime_epoch: &'a str,
    runtime_revision: u64,
    state: &'a str,
    active_instances: u32,
    consumer_lag: u64,
    error_code: &'a str,
    error_message: &'a str,
    observed_at: chrono::DateTime<Utc>,
    expires_at: chrono::DateTime<Utc>,
}

/// [COMMENT]: Reverse path chỉ xác minh business scope/config trong PostgreSQL. Dynamic runtime
/// được fence + aggregate trong Redis Job khi watch lease còn sống; không ghi business DB/NATS KV.
pub async fn run_consumer_report_listener(
    config: &Config,
    redis_client: &redis::Client,
    nats_client: &async_nats::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    let (pg_client, pg_connection) = tokio_postgres::connect(&config.database_url, NoTls).await?;
    tokio::spawn(async move {
        if let Err(error) = pg_connection.await {
            Logger::sys_error(
                "mail.consumer_report.postgres",
                "Mail consumer report PostgreSQL connection stopped",
                &error.to_string(),
            );
        }
    });
    pg_client
        .batch_execute(
            "SET statement_timeout = '5s'; \
             SET lock_timeout = '1s'; \
             SET idle_in_transaction_session_timeout = '5s'",
        )
        .await?;
    let resolve_consumer_scope = pg_client
        .prepare(
            "WITH targets AS ( \
                 SELECT 'personal'::text AS scope, c.config_version, c.parallelism, c.desired_state::text \
                 FROM mail.personal_mail_consumers AS c \
                 WHERE c.id=$1 \
                   AND EXISTS ( \
                     SELECT 1 FROM hierarchy.personal_workspaces AS w \
                     WHERE w.id=c.workspace_id AND w.zone_id=$2 \
                   ) \
                 UNION ALL \
                 SELECT 'tenant'::text AS scope, c.config_version, c.parallelism, c.desired_state::text \
                 FROM mail.tenant_mail_consumers AS c \
                 WHERE c.id=$1 \
                   AND EXISTS ( \
                     SELECT 1 FROM hierarchy.tenant_workspaces AS w \
                     WHERE w.id=c.workspace_id AND w.zone_id=$2 \
                   ) \
             ) \
             SELECT scope, config_version, parallelism, desired_state FROM targets",
        )
        .await?;

    let mut redis_conn = redis_client.get_multiplexed_tokio_connection().await?;
    let _: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(CONSUMER_REPORT_STREAM)
        .arg(CONSUMER_REPORT_GROUP)
        // [COMMENT]: V2 group đọc backlog để rollout không bỏ report đã publish trước pod restart.
        .arg("0")
        .arg("MKSTREAM")
        .query_async(&mut redis_conn)
        .await;
    let listener_id = format!(
        "mail-consumer-{}-{}",
        crate::config::get_node_hostname(),
        std::process::id()
    );

    Logger::sys_info(
        "mail.consumer_report.run",
        &format!(
            "Listening on Redis Stream {} as {}",
            CONSUMER_REPORT_STREAM, listener_id
        ),
    );

    loop {
        let claimed: redis::Value = redis::cmd("XAUTOCLAIM")
            .arg(CONSUMER_REPORT_STREAM)
            .arg(CONSUMER_REPORT_GROUP)
            .arg(&listener_id)
            .arg(config.mail_runtime_report_claim_idle_ms)
            .arg("0-0")
            .arg("COUNT")
            .arg(50)
            .query_async(&mut redis_conn)
            .await?;
        let reply = if let redis::Value::Bulk(parts) = claimed {
            if let Some(redis::Value::Bulk(entries)) = parts.get(1) {
                if entries.is_empty() {
                    redis::cmd("XREADGROUP")
                        .arg("GROUP")
                        .arg(CONSUMER_REPORT_GROUP)
                        .arg(&listener_id)
                        .arg("BLOCK")
                        .arg(2_000)
                        .arg("COUNT")
                        .arg(50)
                        .arg("STREAMS")
                        .arg(CONSUMER_REPORT_STREAM)
                        .arg(">")
                        .query_async(&mut redis_conn)
                        .await?
                } else {
                    redis::Value::Bulk(vec![redis::Value::Bulk(vec![
                        redis::Value::Data(CONSUMER_REPORT_STREAM.as_bytes().to_vec()),
                        redis::Value::Bulk(entries.clone()),
                    ])])
                }
            } else {
                redis::Value::Nil
            }
        } else {
            redis::Value::Nil
        };

        let redis::Value::Bulk(streams) = reply else {
            continue;
        };
        let Some(redis::Value::Bulk(stream_data)) = streams.first() else {
            continue;
        };
        let Some(redis::Value::Bulk(entries)) = stream_data.get(1) else {
            continue;
        };

        for entry in entries {
            let redis::Value::Bulk(entry_parts) = entry else {
                continue;
            };
            let Some(redis::Value::Data(entry_id_bytes)) = entry_parts.first() else {
                continue;
            };
            let entry_id = String::from_utf8_lossy(entry_id_bytes).into_owned();
            let fields = match entry_parts.get(1) {
                Some(redis::Value::Bulk(fields)) => fields.as_slice(),
                _ => &[],
            };
            let mut zone_id_bytes = None;
            let mut payload_bytes = None;
            for field in fields.chunks(2) {
                if field.len() != 2 {
                    continue;
                }
                let redis::Value::Data(name) = &field[0] else {
                    continue;
                };
                if name.as_slice() == b"zone_id" {
                    if let redis::Value::Data(value) = &field[1] {
                        zone_id_bytes = Some(value.as_slice());
                    }
                } else if name.as_slice() == b"payload" {
                    if let redis::Value::Data(value) = &field[1] {
                        payload_bytes = Some(value.as_slice());
                    }
                }
            }

            let mut terminal = false;
            let mut validation_error = String::new();
            let zone_id = zone_id_bytes
                .and_then(|value| std::str::from_utf8(value).ok())
                .and_then(|value| Uuid::parse_str(value).ok());
            let batch = payload_bytes
                .filter(|payload| payload.len() <= 512 << 10)
                .and_then(|payload| MailConsumerRuntimeReportBatchV1::decode(payload).ok())
                .filter(|batch| !batch.reports.is_empty() && batch.reports.len() <= 250);
            if zone_id.is_none() || batch.is_none() {
                terminal = true;
                validation_error = "MAIL_CONSUMER_REPORT_ENVELOPE_INVALID".to_string();
            }

            if let (Some(zone_id), Some(batch)) = (zone_id, batch) {
                terminal = true;
                for report in batch.reports {
                    let metadata = report.metadata.as_ref();
                    let event_id =
                        metadata.and_then(|value| Uuid::from_slice(&value.event_id).ok());
                    let consumer_id = Uuid::from_slice(&report.consumer_id).ok();
                    let runtime_state =
                        MailConsumerRuntimeState::try_from(report.runtime_state).ok();
                    let occurred_at = metadata.and_then(|value| {
                        chrono::DateTime::<Utc>::from_timestamp_millis(value.occurred_at_unix_ms)
                    });
                    let now = Utc::now();
                    let runtime_slot = report
                        .instance_id
                        .strip_prefix("slot:")
                        .and_then(|slot| slot.parse::<i32>().ok());
                    let contract_valid = event_id.is_some()
                        && consumer_id.is_some()
                        && runtime_state
                            .is_some_and(|value| value != MailConsumerRuntimeState::Unspecified)
                        && metadata.is_some_and(|value| {
                            value.schema_version == 1
                                && value.producer == "dataplane-mail-consumer"
                                && value.traceparent.len() <= 128
                        })
                        && occurred_at.is_some_and(|value| {
                            value <= now + ChronoDuration::minutes(5)
                                && value >= now - ChronoDuration::hours(24)
                        })
                        && runtime_slot.is_some_and(|slot| (0..256).contains(&slot))
                        && report.error_code.len() <= 100
                        && report.error_code.bytes().all(|byte| {
                            byte.is_ascii_uppercase() || byte.is_ascii_digit() || byte == b'_'
                        })
                        && report.error_message.len() <= 1_024
                        && !report.error_message.chars().any(char::is_control)
                        && report.config_version > 0
                        && report.runtime_generation > 0
                        && report.report_sequence > 0
                        && Uuid::parse_str(&report.runtime_epoch).is_ok()
                        && report.config_version <= i64::MAX as u64
                        && report.runtime_generation <= i64::MAX as u64
                        && report.report_sequence <= i64::MAX as u64
                        && report.consumer_lag <= i64::MAX as u64;
                    if !contract_valid {
                        validation_error = "MAIL_CONSUMER_REPORT_CONTRACT_INVALID".to_string();
                        continue;
                    }

                    let state = match runtime_state {
                        Some(MailConsumerRuntimeState::Stopped) => "stopped",
                        Some(MailConsumerRuntimeState::Starting) => "starting",
                        Some(MailConsumerRuntimeState::Running) => "running",
                        Some(MailConsumerRuntimeState::Paused) => "paused",
                        Some(MailConsumerRuntimeState::Draining) => "draining",
                        Some(MailConsumerRuntimeState::Error) => "error",
                        Some(MailConsumerRuntimeState::Degraded) => "degraded",
                        Some(MailConsumerRuntimeState::Unspecified) | None => unreachable!(),
                    };
                    let consumer_id = consumer_id.expect("validated consumer id");
                    let occurred_at = occurred_at.expect("validated occurred at");
                    let runtime_slot = runtime_slot.expect("validated runtime slot");
                    let target = pg_client
                        .query(&resolve_consumer_scope, &[&consumer_id, &zone_id])
                        .await;
                    let target = match target {
                        Ok(rows) if rows.len() == 1 => {
                            let row = &rows[0];
                            (
                                row.get::<_, String>(0),
                                row.get::<_, i64>(1),
                                row.get::<_, i32>(2),
                                row.get::<_, String>(3),
                            )
                        }
                        Ok(_) => {
                            Logger::sys_warn(
                                "mail.consumer_report.scope",
                                "Consumer report did not match exactly one consumer in its Zone",
                                "MAIL_CONSUMER_REPORT_SCOPE_MISMATCH",
                            );
                            continue;
                        }
                        Err(error) => {
                            terminal = false;
                            Logger::sys_error(
                                "mail.consumer_report.scope",
                                "Could not validate consumer scope; leaving batch pending",
                                &error.to_string(),
                            );
                            if pg_client.is_closed() {
                                return Err(error.into());
                            }
                            break;
                        }
                    };
                    let (scope, current_config_version, parallelism, desired_state) = target;
                    if current_config_version <= 0
                        || report.config_version != current_config_version as u64
                        || runtime_slot < 0
                        || runtime_slot >= parallelism
                    {
                        // [COMMENT]: Report của config cũ là terminal stale data, không phải lỗi
                        // transport để retry; watch mới chỉ nhận đúng active version.
                        validation_error = "MAIL_CONSUMER_REPORT_CONFIG_STALE".to_string();
                        continue;
                    }

                    let ttl_ms = config
                        .mail_runtime_report_ttl_secs
                        .saturating_mul(1_000)
                        .min(i64::MAX as u64);
                    let expires_at = occurred_at
                        + ChronoDuration::milliseconds(ttl_ms.min(i64::MAX as u64) as i64);
                    let slot_snapshot = SlotRuntimeSnapshot {
                        config_version: report.config_version,
                        runtime_epoch: report.runtime_epoch.clone(),
                        runtime_generation: report.runtime_generation,
                        report_sequence: report.report_sequence,
                        state: state.to_string(),
                        consumer_lag: report.consumer_lag,
                        error_code: report.error_code,
                        error_message: report.error_message,
                        observed_at_unix_ms: occurred_at.timestamp_millis(),
                        expires_at_unix_ms: expires_at.timestamp_millis(),
                    };
                    let slot_json = match serde_json::to_string(&slot_snapshot) {
                        Ok(value) => value,
                        Err(error) => {
                            validation_error =
                                "MAIL_CONSUMER_REPORT_SERIALIZATION_FAILED".to_string();
                            Logger::sys_error(
                                "mail.consumer_report.serialize",
                                "Could not serialize bounded runtime slot",
                                &error.to_string(),
                            );
                            continue;
                        }
                    };
                    let active_watch_key =
                        format!("mail:runtime:watch-active:{zone_id}:{consumer_id}");
                    let slot_key = format!(
                        "mail:runtime:slot:{scope}:{consumer_id}:{}",
                        report.instance_id
                    );
                    let slot_index_key = format!("mail:runtime:slot-index:{scope}:{consumer_id}");
                    let revision_key = format!("mail:runtime:revision:{scope}:{consumer_id}");

                    // [COMMENT]: Lease check + generation/sequence fence + slot update là một
                    // Redis transaction. Report ngoài watch window không tạo bất kỳ runtime key.
                    let applied: redis::RedisResult<Vec<String>> = redis::Script::new(
                        "local lease=redis.call('GET',KEYS[1]); \
                         if not lease then return {} end; \
                         if lease ~= ARGV[1]..':'..ARGV[4] then return {} end; \
                         local existing=redis.call('GET',KEYS[2]); \
                         if existing then \
                           local ok,current=pcall(cjson.decode,existing); \
                           if ok and current.runtime_epoch == ARGV[4] \
                             and (tonumber(current.config_version)>tonumber(ARGV[1]) \
                               or (tonumber(current.config_version)==tonumber(ARGV[1]) \
                                 and tonumber(current.runtime_generation)>tonumber(ARGV[2])) \
                               or (tonumber(current.config_version)==tonumber(ARGV[1]) \
                                 and tonumber(current.runtime_generation)==tonumber(ARGV[2]) \
                                 and tonumber(current.report_sequence)>=tonumber(ARGV[3]))) then \
                             local revision=redis.call('GET',KEYS[4]) or '0'; \
                             return {lease,revision}; \
                           end; \
                         end; \
                         redis.call('SET',KEYS[2],ARGV[5],'PX',ARGV[6]); \
                         redis.call('SADD',KEYS[3],KEYS[2]); \
                         redis.call('PEXPIRE',KEYS[3],tonumber(ARGV[6])*2); \
                         local revision=redis.call('INCR',KEYS[4]); \
                         redis.call('PEXPIRE',KEYS[4],tonumber(ARGV[6])*2); \
                         return {lease,tostring(revision)}",
                    )
                    .key(&active_watch_key)
                    .key(&slot_key)
                    .key(&slot_index_key)
                    .key(&revision_key)
                    .arg(report.config_version)
                    .arg(report.runtime_generation)
                    .arg(report.report_sequence)
                    .arg(&report.runtime_epoch)
                    .arg(slot_json)
                    .arg(ttl_ms)
                    .invoke_async(&mut redis_conn)
                    .await;
                    let applied = match applied {
                        Ok(values) if values.len() == 2 => values,
                        Ok(_) => continue,
                        Err(error) => {
                            terminal = false;
                            Logger::sys_error(
                                "mail.consumer_report.redis",
                                "Could not fence runtime slot; leaving batch pending",
                                &error.to_string(),
                            );
                            break;
                        }
                    };
                    let active_lease = &applied[0];
                    let runtime_revision = applied[1].parse::<u64>().unwrap_or_default();
                    let Some((_, runtime_epoch)) = active_lease.split_once(':') else {
                        validation_error = "MAIL_CONSUMER_REPORT_LEASE_INVALID".to_string();
                        continue;
                    };

                    let mut slot_keys: Vec<String> = match redis::cmd("SMEMBERS")
                        .arg(&slot_index_key)
                        .query_async(&mut redis_conn)
                        .await
                    {
                        Ok(values) => values,
                        Err(error) => {
                            terminal = false;
                            Logger::sys_error(
                                "mail.consumer_report.aggregate",
                                "Could not read runtime slot index; leaving batch pending",
                                &error.to_string(),
                            );
                            break;
                        }
                    };
                    slot_keys.sort();
                    slot_keys.truncate(256);
                    let slot_values: Vec<Option<String>> = if slot_keys.is_empty() {
                        Vec::new()
                    } else {
                        match redis::cmd("MGET")
                            .arg(&slot_keys)
                            .query_async(&mut redis_conn)
                            .await
                        {
                            Ok(values) => values,
                            Err(error) => {
                                terminal = false;
                                Logger::sys_error(
                                    "mail.consumer_report.aggregate",
                                    "Could not read runtime slots; leaving batch pending",
                                    &error.to_string(),
                                );
                                break;
                            }
                        }
                    };

                    let aggregate_now = Utc::now();
                    let mut live_slots = Vec::new();
                    for raw in slot_values.into_iter().flatten() {
                        let Ok(slot) = serde_json::from_str::<SlotRuntimeSnapshot>(&raw) else {
                            continue;
                        };
                        if slot.config_version == report.config_version
                            && slot.runtime_epoch == report.runtime_epoch
                            && slot.expires_at_unix_ms > aggregate_now.timestamp_millis()
                        {
                            live_slots.push(slot);
                        }
                    }
                    if live_slots.is_empty() {
                        continue;
                    }

                    let mut active_instances = 0_u32;
                    let mut total_lag = 0_u64;
                    let mut observed_at_unix_ms = 0_i64;
                    let mut expires_at_unix_ms = i64::MAX;
                    let mut has_error = false;
                    let mut has_degraded = false;
                    let mut has_draining = false;
                    let mut has_starting = false;
                    let mut has_running = false;
                    let mut has_paused = false;
                    let mut aggregate_error_code = String::new();
                    let mut aggregate_error_message = String::new();
                    for slot in &live_slots {
                        if slot.state != "stopped" {
                            active_instances = active_instances.saturating_add(1);
                        }
                        total_lag = total_lag.saturating_add(slot.consumer_lag);
                        observed_at_unix_ms = observed_at_unix_ms.max(slot.observed_at_unix_ms);
                        expires_at_unix_ms = expires_at_unix_ms.min(slot.expires_at_unix_ms);
                        match slot.state.as_str() {
                            "error" => has_error = true,
                            "degraded" => has_degraded = true,
                            "draining" => has_draining = true,
                            "starting" => has_starting = true,
                            "running" => has_running = true,
                            "paused" => has_paused = true,
                            _ => {}
                        }
                        if aggregate_error_code.is_empty() && !slot.error_code.is_empty() {
                            aggregate_error_code.clone_from(&slot.error_code);
                            aggregate_error_message.clone_from(&slot.error_message);
                        }
                    }
                    let incomplete_parallelism = live_slots.len() < parallelism as usize;
                    let aggregate_state = if has_error {
                        "error"
                    } else if has_degraded || (desired_state == "enabled" && incomplete_parallelism)
                    {
                        "degraded"
                    } else if has_draining {
                        "draining"
                    } else if has_starting {
                        "starting"
                    } else if has_running {
                        "running"
                    } else if has_paused || desired_state == "paused" {
                        "paused"
                    } else {
                        "stopped"
                    };
                    let Some(observed_at) =
                        chrono::DateTime::<Utc>::from_timestamp_millis(observed_at_unix_ms)
                    else {
                        validation_error = "MAIL_CONSUMER_REPORT_OBSERVED_AT_INVALID".to_string();
                        continue;
                    };
                    let Some(snapshot_expires_at) =
                        chrono::DateTime::<Utc>::from_timestamp_millis(expires_at_unix_ms)
                    else {
                        validation_error = "MAIL_CONSUMER_REPORT_EXPIRES_AT_INVALID".to_string();
                        continue;
                    };
                    let snapshot = ConsumerRuntimeSnapshot {
                        config_version: report.config_version,
                        runtime_epoch,
                        runtime_revision,
                        state: aggregate_state,
                        active_instances,
                        consumer_lag: total_lag,
                        error_code: &aggregate_error_code,
                        error_message: &aggregate_error_message,
                        observed_at,
                        expires_at: snapshot_expires_at,
                    };
                    let snapshot_json = match serde_json::to_string(&snapshot) {
                        Ok(value) => value,
                        Err(error) => {
                            validation_error =
                                "MAIL_CONSUMER_REPORT_SERIALIZATION_FAILED".to_string();
                            Logger::sys_error(
                                "mail.consumer_report.serialize",
                                "Could not serialize aggregate runtime snapshot",
                                &error.to_string(),
                            );
                            continue;
                        }
                    };
                    let snapshot_key = format!("mail:runtime:snapshot:{scope}:{consumer_id}");
                    let snapshot_ttl_ms = expires_at_unix_ms
                        .saturating_sub(aggregate_now.timestamp_millis())
                        .max(1);
                    let committed: redis::RedisResult<i64> = redis::Script::new(
                        "if redis.call('GET',KEYS[1]) ~= ARGV[1] then return 0 end; \
                         local existing=redis.call('GET',KEYS[2]); \
                         if existing then \
                           local ok,current=pcall(cjson.decode,existing); \
                           if ok and tonumber(current.runtime_revision)>=tonumber(ARGV[2]) then return 0 end; \
                         end; \
                         redis.call('SET',KEYS[2],ARGV[3],'PX',ARGV[4]); \
                         return 1",
                    )
                    .key(&active_watch_key)
                    .key(&snapshot_key)
                    .arg(active_lease)
                    .arg(runtime_revision)
                    .arg(&snapshot_json)
                    .arg(snapshot_ttl_ms)
                    .invoke_async(&mut redis_conn)
                    .await;
                    match committed {
                        Ok(1) => {}
                        Ok(_) => continue,
                        Err(error) => {
                            terminal = false;
                            Logger::sys_error(
                                "mail.consumer_report.snapshot",
                                "Could not commit aggregate runtime snapshot; leaving batch pending",
                                &error.to_string(),
                            );
                            break;
                        }
                    }

                    // [COMMENT]: Watcher list cũng dùng Redis server time. NATS Core/Centrifugo là
                    // best-effort wake-up; snapshot Redis cho phép UI recover khi notification mất.
                    let watchers: redis::RedisResult<Vec<String>> = redis::Script::new(
                        "local t=redis.call('TIME'); \
                         local now=(tonumber(t[1])*1000)+math.floor(tonumber(t[2])/1000); \
                         redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now); \
                         return redis.call('ZRANGEBYSCORE',KEYS[1],now,'+inf','LIMIT',0,256)",
                    )
                    .key(format!("mail:runtime:watchers:{zone_id}:{consumer_id}"))
                    .invoke_async(&mut redis_conn)
                    .await;
                    if let Ok(watchers) = watchers {
                        let notification = serde_json::json!({
                            "event_type": "mail.consumer.runtime.changed",
                            "scope": scope,
                            "consumer_id": consumer_id,
                            "config_version": report.config_version,
                            "runtime_epoch": runtime_epoch,
                            "runtime_revision": runtime_revision,
                            "state": aggregate_state,
                            "active_instances": active_instances,
                            "consumer_lag": total_lag,
                            "error_code": aggregate_error_code,
                            "error_message": aggregate_error_message,
                            "observed_at": observed_at,
                            "expires_at": snapshot_expires_at,
                        });
                        if let Ok(payload) = serde_json::to_vec(&notification) {
                            for watcher in watchers {
                                if Uuid::parse_str(&watcher).is_err() {
                                    continue;
                                }
                                if let Err(error) = nats_client
                                    .publish(
                                        format!("mail.runtime.notifications.{watcher}"),
                                        payload.clone().into(),
                                    )
                                    .await
                                {
                                    Logger::sys_warn(
                                        "mail.consumer_report.notify",
                                        "Runtime notification publish failed; Redis snapshot remains recoverable",
                                        &error.to_string(),
                                    );
                                }
                            }
                        }
                    }
                }
            }

            if !validation_error.is_empty() {
                Logger::sys_warn(
                    "mail.consumer_report.reject",
                    "Dropping invalid or stale consumer runtime item without retry",
                    &validation_error,
                );
            }
            if terminal {
                // [COMMENT]: Runtime là soft state nên settle phụ thuộc Redis snapshot, không phụ
                // thuộc best-effort NATS notification.
                let settled: redis::RedisResult<i64> = redis::Script::new(
                    "local acked=redis.call('XACK',KEYS[1],ARGV[1],ARGV[2]); \
                     if acked == 1 then return redis.call('XDEL',KEYS[1],ARGV[2]) end; \
                     return 0",
                )
                .key(CONSUMER_REPORT_STREAM)
                .arg(CONSUMER_REPORT_GROUP)
                .arg(&entry_id)
                .invoke_async(&mut redis_conn)
                .await;
                if let Err(error) = settled {
                    Logger::sys_error(
                        "mail.consumer_report.settle",
                        "Could not atomically ACK/XDEL consumer report batch",
                        &error.to_string(),
                    );
                }
            }
        }
    }
}
