use crate::config::Config;
use crate::executor::mail::runtime::RuntimeHealthSnapshot;
use crate::executor::mail::runtime_proto::{
    MailConsumerRuntimeReportedV1, MailConsumerRuntimeState, MailEventMetadataV1,
};
use crate::infra::redis::RedisClientManager;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;
use prost::Message;
use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use uuid::Uuid;

/// [COMMENT]: Mail workload tự relay runtime detail; Zone Gateway không cần hiểu consumer generation hay protobuf mail.
pub(super) fn start(
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
    redis_job: Arc<RedisClientManager>,
) {
    tokio::spawn(async move {
        let instance_id = std::env::var("HOSTNAME")
            .unwrap_or_else(|_| format!("dataplane-{}", std::process::id()));
        let runtime_event_namespace = Uuid::parse_str("e7b1a4a4-9150-4494-88d4-5994d312d219")
            .expect("mail runtime report namespace must be valid");
        // [COMMENT]: Cache local chỉ giảm duplicate transport. PostgreSQL vẫn guard event_id,
        // generation và sequence nên leader rotation/restart không thể làm read model lùi trạng thái.
        let mut last_runtime_reported = HashMap::<String, (String, u64, u64, u64)>::new();

        loop {
            let lease = match zone_kv
                .acquire_rotating_lease(
                    "lease.mail.runtime.report",
                    &instance_id,
                    Duration::from_secs(15),
                    Duration::from_secs(6),
                )
                .await
            {
                Ok(Some(lease)) => lease,
                Ok(None) => {
                    tokio::time::sleep(Duration::from_secs(5)).await;
                    continue;
                }
                Err(error) => {
                    Logger::sys_warn(
                        "mail.supervisor.runtime_reporter",
                        "Failed to acquire mail runtime report lease",
                        &error,
                    );
                    tokio::time::sleep(Duration::from_secs(2)).await;
                    continue;
                }
            };

            let now_ms = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
                .unwrap_or_default();
            let mut seen_runtime_keys = HashSet::new();
            let mut pending_runtime_reports = Vec::new();

            for key in zone_kv.health_keys().await.unwrap_or_default() {
                if !key.starts_with("mail.runtime.") {
                    continue;
                }
                seen_runtime_keys.insert(key.clone());
                let Some(bytes) = zone_kv.health_get(key.clone()).await.ok().flatten() else {
                    continue;
                };
                let Ok(snapshot) = serde_json::from_slice::<RuntimeHealthSnapshot>(&bytes) else {
                    continue;
                };
                // [COMMENT]: Health KV giữ lâu để debug; reverse path chỉ relay heartbeat còn sống
                // trong hai slot lease TTL để không hồi sinh runtime cũ trên Consumer Detail.
                if snapshot.report_sequence == 0
                    || snapshot.runtime_generation == 0
                    || snapshot.runtime_generation != snapshot.fencing_token
                    || snapshot.config_version == 0
                    || snapshot.heartbeat_unix_ms > i64::MAX as u64
                    || snapshot.instance_id.is_empty()
                    || snapshot.instance_id.len() > 255
                    || snapshot.instance_id != format!("slot:{}", snapshot.slot)
                    || now_ms.saturating_sub(snapshot.heartbeat_unix_ms)
                        > config
                            .mail_stream_slot_lease_ttl_seconds
                            .saturating_mul(2_000)
                {
                    continue;
                }
                let consumer_id = match Uuid::parse_str(&snapshot.consumer_id) {
                    Ok(value) => value,
                    Err(_) => continue,
                };
                let runtime_state = match snapshot.state.as_str() {
                    "STOPPED" => MailConsumerRuntimeState::Stopped,
                    "STARTING" => MailConsumerRuntimeState::Starting,
                    "RUNNING" => MailConsumerRuntimeState::Running,
                    "PAUSED" => MailConsumerRuntimeState::Paused,
                    "DRAINING" => MailConsumerRuntimeState::Draining,
                    "ERROR" => MailConsumerRuntimeState::Error,
                    "DEGRADED" => MailConsumerRuntimeState::Degraded,
                    _ => continue,
                };
                let marker = (
                    snapshot.instance_id.clone(),
                    snapshot.config_version,
                    snapshot.runtime_generation,
                    snapshot.report_sequence,
                );
                if last_runtime_reported.get(&key) == Some(&marker) {
                    continue;
                }

                let relay_gate_key = format!(
                    "mail:runtime:report:gate:{}:{}:{}",
                    config.zone_id, consumer_id, snapshot.instance_id
                );
                // [COMMENT]: Heartbeat/sequence không nằm trong signature để slot ổn định chỉ refresh
                // Controlplane tối đa mỗi 60 giây; state/config/generation/error đổi vẫn bypass throttle.
                let relay_signature = format!(
                    "{}:{}:{}:{}:{}",
                    snapshot.instance_id,
                    snapshot.config_version,
                    snapshot.runtime_generation,
                    runtime_state as i32,
                    snapshot.error_code
                );
                let event_name = format!(
                    "{}:{}:{}:{}:{}",
                    config.zone_id,
                    consumer_id,
                    snapshot.instance_id,
                    snapshot.runtime_generation,
                    snapshot.report_sequence
                );
                let event_id = Uuid::new_v5(&runtime_event_namespace, event_name.as_bytes());
                let error_code = if snapshot.error_code.len() <= 100
                    && snapshot.error_code.bytes().all(|byte| {
                        byte.is_ascii_uppercase() || byte.is_ascii_digit() || byte == b'_'
                    }) {
                    snapshot.error_code
                } else {
                    "MAIL_RUNTIME_ERROR_INVALID".to_string()
                };
                let runtime_report = MailConsumerRuntimeReportedV1 {
                    metadata: Some(MailEventMetadataV1 {
                        event_id: event_id.as_bytes().to_vec(),
                        schema_version: 1,
                        occurred_at_unix_ms: snapshot.heartbeat_unix_ms as i64,
                        traceparent: String::new(),
                        producer: "dataplane-mail-runtime".to_string(),
                    }),
                    consumer_id: consumer_id.as_bytes().to_vec(),
                    config_version: snapshot.config_version,
                    runtime_state: runtime_state as i32,
                    instance_id: snapshot.instance_id,
                    runtime_generation: snapshot.runtime_generation,
                    consumer_lag: snapshot.consumer_lag,
                    error_code,
                    // [COMMENT]: Không chuyển exception thô của customer broker qua trust boundary.
                    error_message: String::new(),
                    report_sequence: snapshot.report_sequence,
                };
                let mut payload = Vec::new();
                if runtime_report.encode(&mut payload).is_ok() {
                    pending_runtime_reports.push((
                        key,
                        marker,
                        relay_gate_key,
                        relay_signature,
                        payload,
                    ));
                }
            }
            last_runtime_reported.retain(|key, _| seen_runtime_keys.contains(key));

            if !pending_runtime_reports.is_empty() {
                if let Ok(mut connection) =
                    redis_job.client().get_multiplexed_tokio_connection().await
                {
                    // [COMMENT]: Recovery relay chia chunk cố định để không tạo Redis pipeline hoặc
                    // event-loop burst tỷ lệ không giới hạn với số consumer trong Zone.
                    for chunk in pending_runtime_reports.chunks(500) {
                        if !zone_kv
                            .renew_lease(&lease, Duration::from_secs(15))
                            .await
                            .unwrap_or(false)
                        {
                            break;
                        }
                        let mut pipeline = redis::pipe();
                        for (_, _, relay_gate_key, relay_signature, runtime_payload) in chunk {
                            // [COMMENT]: Shared Redis gate giữ throttle qua leader rotation; Redis TIME
                            // loại clock skew giữa các Dataplane replica trong cùng Zone.
                            pipeline
                                .cmd("EVAL")
                                .arg(
                                    "local t=redis.call('TIME'); \
                             local now=(tonumber(t[1])*1000)+math.floor(tonumber(t[2])/1000); \
                             local previous=redis.call('GET',KEYS[1]); \
                             if previous then \
                               local separator=string.find(previous,'|',1,true); \
                               if separator then \
                                 local previous_signature=string.sub(previous,1,separator-1); \
                                 local previous_at=tonumber(string.sub(previous,separator+1)) or 0; \
                                 if previous_signature==ARGV[1] and now-previous_at < tonumber(ARGV[2]) then return '' end; \
                               end; \
                             end; \
                             local min_id=string.format('%.0f-0',now-tonumber(ARGV[4])); \
                             local id=redis.call('XADD',KEYS[2],'MINID','~',min_id,'*','zone_id',ARGV[5],'payload',ARGV[6]); \
                             redis.call('SET',KEYS[1],ARGV[1]..'|'..now,'PX',ARGV[3]); \
                             return id",
                                )
                                .arg(2)
                                .arg(relay_gate_key)
                                .arg("mail:runtime:reports")
                                .arg(relay_signature)
                                .arg(60_000)
                                .arg(300_000)
                                .arg(3_600_000)
                                .arg(&config.zone_id)
                                .arg(runtime_payload)
                                .ignore();
                        }
                        let published: redis::RedisResult<()> =
                            pipeline.query_async(&mut connection).await;
                        if published.is_err() {
                            break;
                        }
                        for (key, marker, _, _, _) in chunk {
                            last_runtime_reported.insert(key.clone(), marker.clone());
                        }
                    }
                }
            }

            let _ = zone_kv.release_lease(&lease).await;
            tokio::time::sleep(Duration::from_millis(4_500 + rand::random::<u64>() % 1_000)).await;
        }
    });
}
