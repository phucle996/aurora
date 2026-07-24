use crate::config::Config;
use crate::executor::mail::runtime_proto::{
    MailConsumerRuntimeReportBatchV1, MailConsumerRuntimeWatchRequestedV1,
};
use crate::observability::logger::Logger;
use futures_util::StreamExt;
use prost::Message;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};
use tokio::sync::RwLock;
use uuid::Uuid;

const WATCH_PRODUCER: &str = "job-orchestrator-mail-runtime-watch";

#[derive(Clone)]
struct RuntimeWatch {
    config_version: u64,
    runtime_epoch: String,
    occurred_at_unix_ms: i64,
    expires_at_unix_ms: i64,
}

/// [COMMENT]: NATS Core chỉ vận chuyển soft state Central↔Zone. Không dùng client này cho
/// durable job/projection và không bật JetStream fallback khi subscriber vắng mặt.
pub struct NatsCoreTransport {
    client: async_nats::Client,
    zone_id: Uuid,
    watches: RwLock<HashMap<Uuid, RuntimeWatch>>,
}

impl NatsCoreTransport {
    pub async fn connect(config: &Config) -> Result<Arc<Self>, String> {
        let zone_id = Uuid::parse_str(&config.zone_id)
            .map_err(|error| format!("ZONE_ID must be UUID for NATS subject isolation: {error}"))?;
        if config.nats_core_url.trim() == config.nats_zone_url.trim() {
            return Err(
                "NATS_URL must not equal NATS_ZONE_URL; Core transport and Zone KV are isolated"
                    .to_string(),
            );
        }
        if config.nats_core_client_cert.is_some() != config.nats_core_client_key.is_some() {
            return Err(
                "NATS_CLIENT_CERT and NATS_CLIENT_KEY must be configured together".to_string(),
            );
        }

        let mut options = async_nats::ConnectOptions::new()
            .name(format!("aurora-dataplane-runtime-{}", config.zone_id));
        if let Some(path) = &config.nats_core_ca_cert {
            options = options
                .add_root_certificates(path.clone().into())
                .require_tls(true);
        }
        if let (Some(cert), Some(key)) =
            (&config.nats_core_client_cert, &config.nats_core_client_key)
        {
            options = options
                .add_client_certificate(cert.clone().into(), key.clone().into())
                .require_tls(true);
        }
        let client = options
            .connect(&config.nats_core_url)
            .await
            .map_err(|error| format!("connect NATS Core failed: {error}"))?;

        Ok(Arc::new(Self {
            client,
            zone_id,
            watches: RwLock::new(HashMap::new()),
        }))
    }

    pub async fn start_watch_listener(self: &Arc<Self>) -> Result<(), String> {
        let subject = format!("aurora.runtime.watch.{}.mail.consumer.v1", self.zone_id);
        let subscription = self
            .client
            .subscribe(subject.clone())
            .await
            .map_err(|error| format!("subscribe NATS Core runtime watch failed: {error}"))?;

        // [COMMENT]: Subscription ACK is part of bootstrap readiness; a pod must not run healthy
        // with a permanently dead runtime-watch listener.
        let transport = self.clone();
        tokio::spawn(async move {
            let mut subscription = subscription;
            Logger::sys_info(
                "nats_core.runtime_watch",
                &format!("Listening for ephemeral runtime watches on {subject}"),
            );

            while let Some(message) = subscription.next().await {
                if message.payload.len() > 64 << 10 {
                    Logger::sys_warn(
                        "nats_core.runtime_watch",
                        "Dropped oversized runtime watch payload",
                        "RUNTIME_WATCH_PAYLOAD_TOO_LARGE",
                    );
                    continue;
                }
                let Ok(watch) =
                    MailConsumerRuntimeWatchRequestedV1::decode(message.payload.as_ref())
                else {
                    Logger::sys_warn(
                        "nats_core.runtime_watch",
                        "Dropped runtime watch that failed Protobuf decoding",
                        "RUNTIME_WATCH_PROTO_INVALID",
                    );
                    continue;
                };
                let Some(metadata) = watch.metadata.as_ref() else {
                    Logger::sys_warn(
                        "nats_core.runtime_watch",
                        "Dropped runtime watch without transport metadata",
                        "RUNTIME_WATCH_METADATA_MISSING",
                    );
                    continue;
                };
                let Ok(zone_id) = Uuid::from_slice(&watch.zone_id) else {
                    Logger::sys_warn(
                        "nats_core.runtime_watch",
                        "Dropped runtime watch with invalid Zone UUID",
                        "RUNTIME_WATCH_ZONE_ID_INVALID",
                    );
                    continue;
                };
                let Ok(consumer_id) = Uuid::from_slice(&watch.consumer_id) else {
                    Logger::sys_warn(
                        "nats_core.runtime_watch",
                        "Dropped runtime watch with invalid consumer UUID",
                        "RUNTIME_WATCH_CONSUMER_ID_INVALID",
                    );
                    continue;
                };
                let now_ms = SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .map(|duration| duration.as_millis().min(i64::MAX as u128) as i64)
                    .unwrap_or_default();
                let contract_valid = zone_id == transport.zone_id
                    && Uuid::from_slice(&metadata.event_id).is_ok()
                    && metadata.schema_version == 1
                    && metadata.producer == WATCH_PRODUCER
                    && metadata.traceparent.len() <= 128
                    && metadata.occurred_at_unix_ms <= now_ms.saturating_add(300_000)
                    && metadata.occurred_at_unix_ms >= now_ms.saturating_sub(300_000)
                    && watch.config_version > 0
                    && Uuid::parse_str(&watch.runtime_epoch).is_ok()
                    && watch.expires_at_unix_ms > now_ms
                    && watch.expires_at_unix_ms <= now_ms.saturating_add(60_000);
                if !contract_valid {
                    Logger::sys_warn(
                        "nats_core.runtime_watch",
                        "Dropped runtime watch that violated zone, version, producer, or expiry contract",
                        "RUNTIME_WATCH_CONTRACT_INVALID",
                    );
                    continue;
                }

                let candidate = RuntimeWatch {
                    config_version: watch.config_version,
                    runtime_epoch: watch.runtime_epoch,
                    occurred_at_unix_ms: metadata.occurred_at_unix_ms,
                    expires_at_unix_ms: watch.expires_at_unix_ms,
                };
                let mut watches = transport.watches.write().await;
                let replace = watches.get(&consumer_id).is_none_or(|current| {
                    candidate.occurred_at_unix_ms >= current.occurred_at_unix_ms
                });
                if replace {
                    watches.insert(consumer_id, candidate);
                }
                watches.retain(|_, value| value.expires_at_unix_ms > now_ms);
            }
            Logger::sys_error(
                "nats_core.runtime_watch",
                "NATS Core runtime watch subscription ended",
                "RUNTIME_WATCH_SUBSCRIPTION_ENDED",
            );
        });
        Ok(())
    }

    pub async fn active_watch(
        &self,
        consumer_id: &str,
        config_version: u64,
        now_ms: i64,
    ) -> Option<String> {
        let consumer_id = Uuid::parse_str(consumer_id).ok()?;
        let mut watches = self.watches.write().await;
        watches.retain(|_, value| value.expires_at_unix_ms > now_ms);
        watches.get(&consumer_id).and_then(|watch| {
            (watch.config_version == config_version).then(|| watch.runtime_epoch.clone())
        })
    }

    pub async fn publish_runtime_reports(
        &self,
        batch: &MailConsumerRuntimeReportBatchV1,
    ) -> Result<(), String> {
        let payload = batch.encode_to_vec();
        if payload.is_empty() || payload.len() > 512 << 10 {
            return Err("runtime report batch exceeded NATS contract".to_string());
        }
        self.client
            .publish(
                format!("aurora.runtime.reports.{}.mail.consumer.v1", self.zone_id),
                payload.into(),
            )
            .await
            .map_err(|error| format!("publish NATS runtime report failed: {error}"))
    }
}
