use super::super::runtime_proto::{MailConsumerRuntimeReportBatchV1, MailConsumerRuntimeState};
use crate::config::Config;
use crate::observability::logger::Logger;
use chrono::{Duration as ChronoDuration, Utc};
use prost::Message;
use tokio_postgres::NoTls;
use uuid::Uuid;

const CONSUMER_REPORT_STREAM: &str = "mail:consumer:reports";
const CONSUMER_REPORT_GROUP: &str = "job-orchestrator-mail-consumer-v1";

/// [COMMENT]: Consumer này là event-driven reverse path. XREADGROUP BLOCK không poll DB;
/// PostgreSQL chỉ nhận guarded CTE khi Zone reporter đã phát một runtime delta.
pub async fn run_consumer_report_listener(
    config: &Config,
    redis_client: &redis::Client,
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
    // [COMMENT]: Parse/plan guarded CTE đúng một lần cho connection; heartbeat batch không ép
    // PostgreSQL parse lại cùng statement cho từng logical slot.
    let apply_consumer_report = pg_client
        .prepare(
            "WITH target AS MATERIALIZED ( \
                 SELECT c.config_version \
                 FROM mail.mail_consumers AS c \
                 WHERE c.id=$1 AND $5 <= c.config_version \
                   AND $16 >= 0 AND $16 < c.parallelism \
                   AND ( \
                     EXISTS (SELECT 1 FROM hierarchy.personal_workspaces AS w WHERE w.id=c.workspace_id AND w.zone_id=$2) \
                     OR EXISTS (SELECT 1 FROM hierarchy.tenant_workspaces AS w WHERE w.id=c.workspace_id AND w.zone_id=$2) \
                   ) \
             ), applied AS ( \
                 INSERT INTO mail.mail_consumer_runtime_reports AS current( \
                     consumer_id,event_id,instance_id,config_version,runtime_state, \
                     runtime_generation,report_sequence,consumer_lag,error_code,error_message,reported_at,expires_at, \
                     runtime_node_id,runtime_boot_id \
                 ) \
                 SELECT $1,$3,$4,$5,$6::text::mail.mail_consumer_runtime_state,$7,$8,$9,$10,$11,$12,$13,$14,$15 \
                 FROM target \
                 ON CONFLICT (consumer_id,instance_id) DO UPDATE SET \
                     event_id=EXCLUDED.event_id, config_version=EXCLUDED.config_version, \
                     runtime_state=EXCLUDED.runtime_state, runtime_generation=EXCLUDED.runtime_generation, \
                     report_sequence=EXCLUDED.report_sequence, consumer_lag=EXCLUDED.consumer_lag, \
                     error_code=EXCLUDED.error_code, error_message=EXCLUDED.error_message, \
                     reported_at=EXCLUDED.reported_at, expires_at=EXCLUDED.expires_at, \
                     runtime_node_id=EXCLUDED.runtime_node_id, runtime_boot_id=EXCLUDED.runtime_boot_id \
                 WHERE EXCLUDED.config_version > current.config_version \
                    OR (EXCLUDED.config_version = current.config_version \
                        AND EXCLUDED.runtime_generation > current.runtime_generation) \
                    OR (EXCLUDED.config_version = current.config_version \
                        AND EXCLUDED.runtime_generation = current.runtime_generation \
                        AND EXCLUDED.report_sequence > current.report_sequence) \
                 RETURNING 1 \
             ) \
             SELECT EXISTS(SELECT 1 FROM target), EXISTS(SELECT 1 FROM applied)",
        )
        .await?;

    let mut redis_conn = redis_client.get_multiplexed_tokio_connection().await?;
    let _: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(CONSUMER_REPORT_STREAM)
        .arg(CONSUMER_REPORT_GROUP)
        // [COMMENT]: First deploy phải consume backlog durable đã tồn tại trước khi group được tạo.
        .arg("0")
        .arg("MKSTREAM")
        .query_async(&mut redis_conn)
        .await;
    let consumer_id = format!(
        "mail-consumer-{}-{}",
        crate::config::get_node_hostname(),
        std::process::id()
    );

    Logger::sys_info(
        "mail.consumer_report.run",
        &format!(
            "Listening on Redis Stream {} as {}",
            CONSUMER_REPORT_STREAM, consumer_id
        ),
    );

    loop {
        // [COMMENT]: Reclaim batch nhỏ từ pod chết trước; entry lỗi DB tạm thời vẫn nằm trong PEL
        // và không chặn consumer này đọc các report mới trong thời gian chờ idle timeout.
        let claimed: redis::Value = redis::cmd("XAUTOCLAIM")
            .arg(CONSUMER_REPORT_STREAM)
            .arg(CONSUMER_REPORT_GROUP)
            .arg(&consumer_id)
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
                        .arg(&consumer_id)
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
                    let runtime_boot_id = Uuid::from_slice(&report.runtime_boot_id).ok();
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
                        && runtime_boot_id.is_some()
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
                        && !report.runtime_node_id.is_empty()
                        && report.runtime_node_id.len() <= 255
                        && !report.runtime_node_id.chars().any(char::is_control)
                        && report.error_code.len() <= 100
                        && report.error_code.bytes().all(|byte| {
                            byte.is_ascii_uppercase() || byte.is_ascii_digit() || byte == b'_'
                        })
                        && report.error_message.len() <= 1_024
                        && !report.error_message.chars().any(char::is_control)
                        && report.config_version > 0
                        && report.runtime_generation > 0
                        && report.report_sequence > 0
                        && report.config_version <= i64::MAX as u64
                        && report.runtime_generation <= i64::MAX as u64
                        && report.report_sequence <= i64::MAX as u64
                        && report.consumer_lag <= i64::MAX as u64;
                    if !contract_valid {
                        validation_error = "MAIL_CONSUMER_REPORT_CONTRACT_INVALID".to_string();
                        // [COMMENT]: Một malformed item không poison toàn bộ bounded batch; valid item
                        // vẫn apply và entry được settle sau khi mọi database operation thành công.
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
                    let event_id = event_id.expect("validated event id");
                    let consumer_id = consumer_id.expect("validated consumer id");
                    let runtime_boot_id = runtime_boot_id.expect("validated boot id");
                    let occurred_at = occurred_at.expect("validated occurred at");
                    let runtime_slot = runtime_slot.expect("validated runtime slot");
                    let expires_at = occurred_at
                        + ChronoDuration::seconds(config.mail_runtime_report_ttl_secs as i64);
                    let error_code =
                        (!report.error_code.is_empty()).then_some(report.error_code.as_str());
                    let error_message =
                        (!report.error_message.is_empty()).then_some(report.error_message.as_str());
                    let applied = pg_client
                        .query_one(
                            &apply_consumer_report,
                            &[
                                &consumer_id,
                                &zone_id,
                                &event_id,
                                &report.instance_id,
                                &(report.config_version as i64),
                                &state,
                                &(report.runtime_generation as i64),
                                &(report.report_sequence as i64),
                                &(report.consumer_lag as i64),
                                &error_code,
                                &error_message,
                                &occurred_at,
                                &expires_at,
                                &report.runtime_node_id,
                                &runtime_boot_id,
                                &runtime_slot,
                            ],
                        )
                        .await;
                    match applied {
                        Ok(row) => {
                            let target_exists: bool = row.get(0);
                            if !target_exists {
                                Logger::sys_warn(
                                    "mail.consumer_report.scope",
                                    "Consumer report did not match an active consumer in its Zone",
                                    "MAIL_CONSUMER_REPORT_SCOPE_MISMATCH",
                                );
                            }
                        }
                        Err(error) => {
                            terminal = false;
                            Logger::sys_error(
                                "mail.consumer_report.apply",
                                "Guarded consumer report UPSERT failed; leaving batch pending",
                                &error.to_string(),
                            );
                            if pg_client.is_closed() {
                                return Err(error.into());
                            }
                            break;
                        }
                    }
                }
            }

            if !validation_error.is_empty() {
                Logger::sys_warn(
                    "mail.consumer_report.reject",
                    "Dropping invalid consumer report item without retry",
                    &validation_error,
                );
            }
            if terminal {
                // [COMMENT]: Lua chỉ XDEL khi XACK trên đúng group thành công; lỗi group không được
                // xóa một entry chưa settle. PostgreSQL apply và report event vẫn idempotent nếu reply mất.
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
