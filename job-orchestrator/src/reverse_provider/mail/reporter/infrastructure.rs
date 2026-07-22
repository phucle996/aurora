use super::super::runtime_proto::{
    MailInfrastructureSnapshotReportedV1, MailInfrastructureState, MailStalwartNodeState,
};
use crate::config::Config;
use crate::observability::logger::Logger;
use chrono::{Duration as ChronoDuration, Utc};
use prost::Message;
use serde_json::json;
use tokio_postgres::NoTls;
use uuid::Uuid;

const INFRA_REPORT_STREAM: &str = "mail:infra:reports";
const INFRA_REPORT_GROUP: &str = "job-orchestrator-mail-infra-v1";

/// [COMMENT]: Low-cardinality infra listener owns the only Mail actual_state write path.
/// Guarded CTE applies snapshot and hierarchy state atomically, so stale Redis delivery cannot roll back either view.
pub async fn run_infra_report_listener(
    config: &Config,
    redis_client: &redis::Client,
) -> Result<(), Box<dyn std::error::Error>> {
    let (pg_client, pg_connection) = tokio_postgres::connect(&config.database_url, NoTls).await?;
    tokio::spawn(async move {
        if let Err(error) = pg_connection.await {
            Logger::sys_error(
                "mail.infra_report.postgres",
                "Mail infrastructure report PostgreSQL connection stopped",
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
    let apply_infra_report = pg_client
        .prepare(
            "WITH target AS MATERIALIZED ( \
                 SELECT service.zone_id \
                 FROM hierarchy.zones AS zone \
                 JOIN hierarchy.zone_services AS service \
                   ON service.zone_id=zone.id AND service.service_type='mail' \
                 WHERE zone.id=$1 \
                 FOR KEY SHARE OF service \
             ), applied AS ( \
                 INSERT INTO mail.mail_infrastructure_reports AS current( \
                     zone_id,event_id,report_generation,report_sequence,service_state,capacity, \
                     pending_items,in_flight_batches,probe_node_id,dataplane_nodes,stalwart_nodes, \
                     inventory_truncated,error_code,reported_at,expires_at \
                 ) \
                 SELECT target.zone_id,$2,$3,$4,$5,$6,$7,$8,$9,$10::text::jsonb,$11::text::jsonb,$12,$13,$14,$15 \
                 FROM target \
                 ON CONFLICT (zone_id) DO UPDATE SET \
                     event_id=EXCLUDED.event_id, report_generation=EXCLUDED.report_generation, \
                     report_sequence=EXCLUDED.report_sequence, service_state=EXCLUDED.service_state, \
                     capacity=EXCLUDED.capacity, pending_items=EXCLUDED.pending_items, \
                     in_flight_batches=EXCLUDED.in_flight_batches, probe_node_id=EXCLUDED.probe_node_id, \
                     dataplane_nodes=EXCLUDED.dataplane_nodes, stalwart_nodes=EXCLUDED.stalwart_nodes, \
                     inventory_truncated=EXCLUDED.inventory_truncated, error_code=EXCLUDED.error_code, \
                     reported_at=EXCLUDED.reported_at, expires_at=EXCLUDED.expires_at \
                 WHERE EXCLUDED.report_generation > current.report_generation \
                    OR (EXCLUDED.report_generation = current.report_generation \
                        AND EXCLUDED.report_sequence > current.report_sequence) \
                 RETURNING zone_id,service_state \
             ), actual AS ( \
                 UPDATE hierarchy.zone_services AS service \
                 SET actual_state=applied.service_state::text::hierarchy.zone_service_status, updated_at=NOW() \
                 FROM applied \
                 WHERE service.zone_id=applied.zone_id AND service.service_type='mail' \
                   AND service.actual_state IS DISTINCT FROM applied.service_state::text::hierarchy.zone_service_status \
                 RETURNING 1 \
             ), actual_consistent AS ( \
                 SELECT 1 FROM actual \
                 UNION ALL \
                 SELECT 1 \
                 FROM applied \
                 JOIN hierarchy.zone_services AS service \
                   ON service.zone_id=applied.zone_id AND service.service_type='mail' \
                 WHERE service.actual_state=applied.service_state::text::hierarchy.zone_service_status \
             ) \
             SELECT EXISTS(SELECT 1 FROM target), EXISTS(SELECT 1 FROM applied), \
                    EXISTS(SELECT 1 FROM actual_consistent)",
        )
        .await?;

    let mut redis_conn = redis_client.get_multiplexed_tokio_connection().await?;
    let _: redis::RedisResult<()> = redis::cmd("XGROUP")
        .arg("CREATE")
        .arg(INFRA_REPORT_STREAM)
        .arg(INFRA_REPORT_GROUP)
        .arg("0")
        .arg("MKSTREAM")
        .query_async(&mut redis_conn)
        .await;
    let consumer_id = format!(
        "mail-infra-{}-{}",
        crate::config::get_node_hostname(),
        std::process::id()
    );
    Logger::sys_info(
        "mail.infra_report.run",
        &format!(
            "Listening on Redis Stream {} as {}",
            INFRA_REPORT_STREAM, consumer_id
        ),
    );

    loop {
        let claimed: redis::Value = redis::cmd("XAUTOCLAIM")
            .arg(INFRA_REPORT_STREAM)
            .arg(INFRA_REPORT_GROUP)
            .arg(&consumer_id)
            .arg(config.mail_runtime_report_claim_idle_ms)
            .arg("0-0")
            .arg("COUNT")
            .arg(20)
            .query_async(&mut redis_conn)
            .await?;
        let reply = if let redis::Value::Bulk(parts) = claimed {
            if let Some(redis::Value::Bulk(entries)) = parts.get(1) {
                if entries.is_empty() {
                    redis::cmd("XREADGROUP")
                        .arg("GROUP")
                        .arg(INFRA_REPORT_GROUP)
                        .arg(&consumer_id)
                        .arg("BLOCK")
                        .arg(2_000)
                        .arg("COUNT")
                        .arg(20)
                        .arg("STREAMS")
                        .arg(INFRA_REPORT_STREAM)
                        .arg(">")
                        .query_async(&mut redis_conn)
                        .await?
                } else {
                    redis::Value::Bulk(vec![redis::Value::Bulk(vec![
                        redis::Value::Data(INFRA_REPORT_STREAM.as_bytes().to_vec()),
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

            let zone_id = zone_id_bytes
                .and_then(|value| std::str::from_utf8(value).ok())
                .and_then(|value| Uuid::parse_str(value).ok());
            let report = payload_bytes
                .filter(|payload| payload.len() <= 1 << 20)
                .and_then(|payload| MailInfrastructureSnapshotReportedV1::decode(payload).ok());
            let mut terminal = false;
            let mut validation_error = String::new();
            if zone_id.is_none() || report.is_none() {
                terminal = true;
                validation_error = "MAIL_INFRA_REPORT_ENVELOPE_INVALID".to_string();
            }

            if let (Some(zone_id), Some(report)) = (zone_id, report) {
                let metadata = report.metadata.as_ref();
                let event_id = metadata.and_then(|value| Uuid::from_slice(&value.event_id).ok());
                let occurred_at = metadata.and_then(|value| {
                    chrono::DateTime::<Utc>::from_timestamp_millis(value.occurred_at_unix_ms)
                });
                let service_state = MailInfrastructureState::try_from(report.service_state).ok();
                let now = Utc::now();
                let mut contract_valid = event_id.is_some()
                    && occurred_at.is_some_and(|value| {
                        value <= now + ChronoDuration::minutes(5)
                            && value >= now - ChronoDuration::hours(24)
                    })
                    && metadata.is_some_and(|value| {
                        value.schema_version == 1
                            && value.producer == "dataplane-mail-infra"
                            && value.traceparent.len() <= 128
                    })
                    && service_state
                        .is_some_and(|value| value != MailInfrastructureState::Unspecified)
                    && report.report_generation > 0
                    && report.report_sequence > 0
                    && report.report_generation <= i64::MAX as u64
                    && report.report_sequence <= i64::MAX as u64
                    && report.capacity <= 100
                    && report.pending_items <= i64::MAX as u64
                    && report.in_flight_batches <= i64::MAX as u64
                    && !report.probe_node_id.is_empty()
                    && report.probe_node_id.len() <= 255
                    && !report.probe_node_id.chars().any(char::is_control)
                    && report.dataplane_nodes.len() <= 512
                    && report.stalwart_nodes.len() <= 512
                    && report.error_code.len() <= 100
                    && report.error_code.bytes().all(|byte| {
                        byte.is_ascii_uppercase() || byte.is_ascii_digit() || byte == b'_'
                    });
                let mut dataplane_json = Vec::with_capacity(report.dataplane_nodes.len());
                for node in &report.dataplane_nodes {
                    let boot_id = Uuid::from_slice(&node.boot_id).ok();
                    let state = MailInfrastructureState::try_from(node.state).ok();
                    let observed_at =
                        chrono::DateTime::<Utc>::from_timestamp_millis(node.observed_at_unix_ms);
                    let node_valid = boot_id.is_some()
                        && state.is_some_and(|value| value != MailInfrastructureState::Unspecified)
                        && !node.node_id.is_empty()
                        && node.node_id.len() <= 255
                        && !node.node_id.chars().any(char::is_control)
                        && node.capacity <= 100
                        && node.pending_items <= i64::MAX as u64
                        && node.in_flight_batches <= i64::MAX as u64
                        && node.error_code.len() <= 100
                        && node.error_code.bytes().all(|byte| {
                            byte.is_ascii_uppercase() || byte.is_ascii_digit() || byte == b'_'
                        })
                        && observed_at.is_some_and(|value| {
                            value <= now + ChronoDuration::minutes(5)
                                && value >= now - ChronoDuration::hours(24)
                        });
                    if !node_valid {
                        contract_valid = false;
                        break;
                    }
                    let state_name = match state {
                        Some(MailInfrastructureState::Healthy) => "healthy",
                        Some(MailInfrastructureState::Degraded) => "degraded",
                        Some(MailInfrastructureState::Unhealthy) => "unhealthy",
                        Some(MailInfrastructureState::Down) => "down",
                        Some(MailInfrastructureState::Unspecified) | None => unreachable!(),
                    };
                    dataplane_json.push(json!({
                        "node_id": node.node_id,
                        "boot_id": boot_id.expect("validated boot id"),
                        "state": state_name,
                        "capacity": node.capacity,
                        "pending_items": node.pending_items,
                        "in_flight_batches": node.in_flight_batches,
                        "active_consumer_slots": node.active_consumer_slots,
                        "jmap_reachable": node.jmap_reachable,
                        "last_probe_at_unix_ms": node.last_probe_at_unix_ms,
                        "observed_at_unix_ms": node.observed_at_unix_ms,
                        "error_code": node.error_code,
                    }));
                }
                let mut stalwart_json = Vec::with_capacity(report.stalwart_nodes.len());
                for node in &report.stalwart_nodes {
                    let state = MailStalwartNodeState::try_from(node.state).ok();
                    let node_valid = node.node_id > 0
                        && !node.hostname.is_empty()
                        && node.hostname.len() <= 255
                        && !node.hostname.chars().any(char::is_control)
                        && state.is_some_and(|value| value != MailStalwartNodeState::Unspecified)
                        && node.last_renewal_unix_ms >= 0;
                    if !node_valid {
                        contract_valid = false;
                        break;
                    }
                    let state_name = match state {
                        Some(MailStalwartNodeState::Active) => "active",
                        Some(MailStalwartNodeState::Stale) => "stale",
                        Some(MailStalwartNodeState::Inactive) => "inactive",
                        Some(MailStalwartNodeState::Unspecified) | None => unreachable!(),
                    };
                    stalwart_json.push(json!({
                        "node_id": node.node_id,
                        "hostname": node.hostname,
                        "state": state_name,
                        "last_renewal_unix_ms": node.last_renewal_unix_ms,
                    }));
                }

                if !contract_valid {
                    terminal = true;
                    validation_error = "MAIL_INFRA_REPORT_CONTRACT_INVALID".to_string();
                } else {
                    let state_name = match service_state {
                        Some(MailInfrastructureState::Healthy) => "healthy",
                        Some(MailInfrastructureState::Degraded) => "degraded",
                        Some(MailInfrastructureState::Unhealthy) => "unhealthy",
                        Some(MailInfrastructureState::Down) => "down",
                        Some(MailInfrastructureState::Unspecified) | None => unreachable!(),
                    };
                    let event_id = event_id.expect("validated event id");
                    let occurred_at = occurred_at.expect("validated occurred at");
                    let expires_at = occurred_at
                        + ChronoDuration::seconds(config.mail_infra_report_ttl_secs as i64);
                    let dataplane_json = serde_json::to_string(&dataplane_json)?;
                    let stalwart_json = serde_json::to_string(&stalwart_json)?;
                    let error_code =
                        (!report.error_code.is_empty()).then_some(report.error_code.as_str());
                    match pg_client
                        .query_one(
                            &apply_infra_report,
                            &[
                                &zone_id,
                                &event_id,
                                &(report.report_generation as i64),
                                &(report.report_sequence as i64),
                                &state_name,
                                &(report.capacity as i32),
                                &(report.pending_items as i64),
                                &(report.in_flight_batches as i64),
                                &report.probe_node_id,
                                &dataplane_json,
                                &stalwart_json,
                                &report.inventory_truncated,
                                &error_code,
                                &occurred_at,
                                &expires_at,
                            ],
                        )
                        .await
                    {
                        Ok(row) => {
                            let target_exists: bool = row.get(0);
                            let applied: bool = row.get(1);
                            let actual_consistent: bool = row.get(2);
                            if !target_exists {
                                Logger::sys_warn(
                                    "mail.infra_report.scope",
                                    "Infrastructure report did not match a Mail service in its Zone",
                                    "MAIL_INFRA_REPORT_SCOPE_MISMATCH",
                                );
                            } else if applied && !actual_consistent {
                                Logger::sys_warn(
                                    "mail.infra_report.actual_state",
                                    "Infrastructure snapshot applied without a consistent Mail actual state",
                                    "MAIL_INFRA_ACTUAL_STATE_INCONSISTENT",
                                );
                            }
                            terminal = true;
                        }
                        Err(error) => {
                            Logger::sys_error(
                                "mail.infra_report.apply",
                                "Guarded infrastructure report UPSERT failed; leaving entry pending",
                                &error.to_string(),
                            );
                            if pg_client.is_closed() {
                                return Err(error.into());
                            }
                        }
                    }
                }
            }

            if !validation_error.is_empty() {
                Logger::sys_warn(
                    "mail.infra_report.reject",
                    "Dropping invalid infrastructure report without retry",
                    &validation_error,
                );
            }
            if terminal {
                let settled: redis::RedisResult<i64> = redis::Script::new(
                    "local acked=redis.call('XACK',KEYS[1],ARGV[1],ARGV[2]); \
                     if acked == 1 then return redis.call('XDEL',KEYS[1],ARGV[2]) end; \
                     return 0",
                )
                .key(INFRA_REPORT_STREAM)
                .arg(INFRA_REPORT_GROUP)
                .arg(&entry_id)
                .invoke_async(&mut redis_conn)
                .await;
                if let Err(error) = settled {
                    Logger::sys_error(
                        "mail.infra_report.settle",
                        "Could not atomically ACK/XDEL infrastructure report",
                        &error.to_string(),
                    );
                }
            }
        }
    }
}
