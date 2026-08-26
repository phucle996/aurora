use super::configuration::RuntimeConsumerConfiguration;
use crate::config::Config;
use crate::executor::mail::processor::MailMessageProcessor;
use crate::executor::mail::runtime_proto::MailStreamType;
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
use crate::observability::logger::{LogFields, Logger};
use aes_gcm::aead::{Aead, KeyInit, Payload};
use aes_gcm::{Aes256Gcm, Nonce};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, RwLock as StdRwLock};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tokio::sync::{OwnedRwLockReadGuard, RwLock};
use zeroize::{Zeroize, Zeroizing};

const ENVELOPE_MAGIC: &[u8; 4] = b"AMS1";
const ENVELOPE_NONCE_BYTES: usize = 12;

/// [COMMENT]: Fence thuộc Aurora runtime generation, không giả lập ACK semantics của bất kỳ broker nào.
#[derive(Debug)]
pub struct RuntimeGenerationFence {
    accepting: AtomicBool,
    gate: Arc<RwLock<()>>,
    // [COMMENT]: Monotonic local deadline chặn stale settlement nếu runtime bị stall quá Zone lease TTL trước renew tick kế tiếp.
    lease_deadline: StdRwLock<Instant>,
}

impl RuntimeGenerationFence {
    pub fn new(lease_ttl: Duration) -> Arc<Self> {
        Arc::new(Self {
            accepting: AtomicBool::new(true),
            gate: Arc::new(RwLock::new(())),
            lease_deadline: StdRwLock::new(Instant::now() + lease_ttl),
        })
    }

    pub async fn enter_submit(&self) -> Option<OwnedRwLockReadGuard<()>> {
        if !self.is_accepting() {
            return None;
        }
        let permit = self.gate.clone().read_owned().await;
        self.is_accepting().then_some(permit)
    }

    pub fn is_accepting(&self) -> bool {
        self.accepting.load(Ordering::Acquire) && self.lease_is_live()
    }

    pub fn lease_is_live(&self) -> bool {
        Instant::now()
            < *self
                .lease_deadline
                .read()
                .expect("mail generation lease deadline lock poisoned")
    }

    pub fn refresh_lease(&self, lease_ttl: Duration) {
        *self
            .lease_deadline
            .write()
            .expect("mail generation lease deadline lock poisoned") = Instant::now() + lease_ttl;
    }

    pub async fn fence(&self) {
        self.accepting.store(false, Ordering::Release);
        // [COMMENT]: COW/lease loss chỉ hoàn tất sau khi mọi JMAP request cũ rời critical section.
        let _drained = self.gate.write().await;
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(crate) struct RuntimeHealthSnapshot {
    pub state: String,
    pub consumer_id: String,
    pub owner_id: String,
    pub owner_type: String,
    pub workspace_id: String,
    pub zone_id: String,
    pub config_version: u64,
    pub runtime_generation: u64,
    pub slot: u32,
    pub fencing_token: u64,
    pub heartbeat_unix_ms: u64,
    pub instance_id: String,
    pub consumer_lag: u64,
    pub error_code: String,
}

/// [COMMENT]: Context này chỉ chứa Aurora scheduling/config/JMAP dependencies; broker connection và settlement vẫn nằm trong từng suite.
pub struct StreamRuntimeContext {
    pub zone_id: String,
    // Lease ownership must identify one process incarnation. Reusing only the
    // hostname after a restart could let a fresh pod appear to be the stale
    // owner while the old Zone lease is still live.
    pub lease_owner_id: String,
    pub zone_kv: Arc<ZoneKvStore>,
    pub processor: Arc<MailMessageProcessor>,
    pub lease_ttl: Duration,
    pub max_message_bytes: usize,
    pub max_slot_inflight: usize,
    // Lag/state/heartbeat là pod-local soft state và chỉ được export sang Zone
    // OTel/Victoria. Zone KV vẫn chỉ giữ desired projection/lease authority.
    runtime_snapshots: StdRwLock<HashMap<(String, u32), RuntimeHealthSnapshot>>,
    envelope_key: Option<Arc<Zeroizing<[u8; 32]>>>,
}

impl StreamRuntimeContext {
    pub fn new(
        config: &Config,
        lease_owner_id: String,
        zone_kv: Arc<ZoneKvStore>,
        processor: Arc<MailMessageProcessor>,
    ) -> Arc<Self> {
        let envelope_key_hex = &config.mail_stream_envelope_key_hex;
        let envelope_key = if envelope_key_hex.len() == 64 {
            let mut key = [0_u8; 32];
            let mut valid = true;
            let encoded = envelope_key_hex.as_bytes();
            for (index, byte) in key.iter_mut().enumerate() {
                match std::str::from_utf8(&encoded[index * 2..index * 2 + 2])
                    .ok()
                    .and_then(|pair| u8::from_str_radix(pair, 16).ok())
                {
                    Some(value) => *byte = value,
                    None => {
                        valid = false;
                        break;
                    }
                }
            }
            if valid {
                Some(Arc::new(Zeroizing::new(key)))
            } else {
                key.zeroize();
                None
            }
        } else {
            None
        };
        Arc::new(Self {
            zone_id: config.zone_id.clone(),
            lease_owner_id,
            zone_kv,
            processor,
            lease_ttl: Duration::from_secs(config.mail_stream_slot_lease_ttl_seconds),
            max_message_bytes: config.mail_max_message_bytes,
            max_slot_inflight: config.mail_stream_max_inflight_per_slot,
            runtime_snapshots: StdRwLock::new(HashMap::new()),
            envelope_key,
        })
    }

    pub fn decrypt_connection(
        &self,
        stream_type: MailStreamType,
        broker_resource_id: [u8; 16],
        envelope: &[u8],
    ) -> Result<Zeroizing<Vec<u8>>, &'static str> {
        let Some(key) = &self.envelope_key else {
            return Err("MAIL_STREAM_ENVELOPE_KEY_UNAVAILABLE");
        };
        if envelope.len() < ENVELOPE_MAGIC.len() + 1 + ENVELOPE_NONCE_BYTES + 16
            || &envelope[..4] != ENVELOPE_MAGIC
            || envelope[4] != 1
        {
            return Err("MAIL_STREAM_ENVELOPE_FORMAT_INVALID");
        }
        let aad = format!(
            "aurora-mail-stream-envelope-v1\0{}\0{}\0{}",
            self.zone_id,
            uuid::Uuid::from_bytes(broker_resource_id),
            stream_type as i32,
        );
        let nonce = Nonce::from_slice(&envelope[5..5 + ENVELOPE_NONCE_BYTES]);
        let cipher = Aes256Gcm::new_from_slice(key.as_ref().as_slice())
            .map_err(|_| "MAIL_STREAM_ENVELOPE_KEY_INVALID")?;
        let plaintext = Zeroizing::new(
            cipher
                .decrypt(
                    nonce,
                    Payload {
                        msg: &envelope[5 + ENVELOPE_NONCE_BYTES..],
                        aad: aad.as_bytes(),
                    },
                )
                .map_err(|_| "MAIL_STREAM_ENVELOPE_AUTH_FAILED")?,
        );
        if plaintext.len() > 16 << 10 {
            return Err("MAIL_STREAM_ENVELOPE_PLAINTEXT_TOO_LARGE");
        }
        Ok(plaintext)
    }

    pub async fn renew_and_report(
        &self,
        configuration: &RuntimeConsumerConfiguration,
        slot: u32,
        generation: u64,
        lease: &ZoneLease,
        generation_fence: &RuntimeGenerationFence,
    ) -> bool {
        match self.zone_kv.renew_lease(lease, self.lease_ttl).await {
            Ok(true) => {
                // [COMMENT]: Chỉ server-acknowledged renew mới đẩy local deadline; lỗi mạng không được tự gia hạn quyền settlement.
                generation_fence.refresh_lease(self.lease_ttl);
                self.write_health("RUNNING", configuration, slot, generation, lease, "")
                    .await;
                true
            }
            Ok(false) => {
                Logger::sys_warn_with_fields(
                    "mail.stream.lease",
                    "MAIL_STREAM_SLOT_LEASE_LOST",
                    "Mail runtime slot lease is no longer current; fencing generation",
                    "",
                    LogFields {
                        operation_id: Some(&configuration.consumer_id),
                        job_version: Some(configuration.config_version),
                        fencing_token: Some(lease.fencing_token),
                        runtime_generation: Some(generation),
                        slot: Some(slot),
                        outcome: Some("fenced"),
                        ..LogFields::default()
                    },
                );
                false
            }
            Err(error) => {
                Logger::sys_warn_with_fields(
                    "mail.stream.lease",
                    "MAIL_STREAM_SLOT_LEASE_RENEW_FAILED",
                    "Mail runtime slot lease renew failed; generation will fail closed",
                    &error,
                    LogFields {
                        operation_id: Some(&configuration.consumer_id),
                        job_version: Some(configuration.config_version),
                        fencing_token: Some(lease.fencing_token),
                        runtime_generation: Some(generation),
                        slot: Some(slot),
                        retryable: Some(true),
                        outcome: Some("fenced"),
                        ..LogFields::default()
                    },
                );
                false
            }
        }
    }

    pub async fn write_health(
        &self,
        state: &str,
        configuration: &RuntimeConsumerConfiguration,
        slot: u32,
        generation: u64,
        lease: &ZoneLease,
        error_code: &str,
    ) {
        // Instance của runtime snapshot là logical slot, không phải hostname.
        // Zone lease fencing remains the only slot ownership authority.
        let runtime_instance_id = format!("slot:{slot}");
        let snapshot = RuntimeHealthSnapshot {
            state: state.to_string(),
            consumer_id: configuration.consumer_id.clone(),
            owner_id: configuration.owner_id.clone(),
            owner_type: configuration.owner_type.clone(),
            workspace_id: configuration.workspace_id.clone(),
            zone_id: configuration.zone_id.clone(),
            config_version: configuration.config_version,
            runtime_generation: generation,
            slot,
            fencing_token: lease.fencing_token,
            heartbeat_unix_ms: SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
                .unwrap_or_default(),
            instance_id: runtime_instance_id,
            // [COMMENT]: Suite-specific lag sampler chưa được triển khai; 0 hiện là protobuf V1 default,
            // không được dùng một giá trị ước lượng hoặc Kafka platform lag thay cho customer broker.
            consumer_lag: 0,
            error_code: error_code.to_string(),
        };
        let key = (configuration.consumer_id.clone(), slot);
        let previous = self
            .runtime_snapshots
            .write()
            .expect("mail runtime snapshot lock poisoned")
            .insert(key, snapshot);
        let changed = previous.as_ref().is_none_or(|previous| {
            previous.state != state
                || previous.error_code != error_code
                || previous.runtime_generation != generation
                || previous.fencing_token != lease.fencing_token
        });
        if changed {
            // Runtime diagnostics are a Zone-owned read model. These flat,
            // bounded labels let OTel route only this consumer's records to
            // Victoria without a Central report/watch workflow.
            tracing::event!(
                target: "aurora-dataplane",
                tracing::Level::INFO,
                level = "info",
                service_name = "aurora-dataplane",
                log_type = "runtime",
                aurora_module = "mail",
                aurora_resource_type = "consumer",
                aurora_resource_id = configuration.consumer_id.as_str(),
                aurora_owner_id = configuration.owner_id.as_str(),
                aurora_owner_type = configuration.owner_type.as_str(),
                aurora_workspace_id = configuration.workspace_id.as_str(),
                aurora_zone_id = configuration.zone_id.as_str(),
                aurora_component_id = format!("slot-{slot}"),
                event_code = "MAIL_STREAM_RUNTIME_STATE_CHANGED",
                runtime_state = state,
                error_code = error_code,
                message = "Mail consumer runtime state changed",
            );
            let fields = LogFields {
                operation_id: Some(&configuration.consumer_id),
                job_version: Some(configuration.config_version),
                fencing_token: Some(lease.fencing_token),
                runtime_generation: Some(generation),
                slot: Some(slot),
                outcome: Some(state),
                ..LogFields::default()
            };
            if state == "ERROR" {
                Logger::sys_error_with_fields(
                    "mail.stream.runtime_state",
                    if error_code.is_empty() {
                        "MAIL_STREAM_RUNTIME_ERROR"
                    } else {
                        error_code
                    },
                    "Mail consumer runtime entered ERROR state",
                    "",
                    fields,
                );
            } else {
                Logger::sys_info_with_fields(
                    "mail.stream.runtime_state",
                    "MAIL_STREAM_RUNTIME_STATE_CHANGED",
                    &format!("Mail consumer runtime state changed to {state}"),
                    fields,
                );
            }
        }
    }

    pub(crate) fn runtime_snapshots(&self) -> Vec<RuntimeHealthSnapshot> {
        let now_ms = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
            .unwrap_or_default();
        let freshness_ms =
            (self.lease_ttl.as_millis().min(u64::MAX as u128) as u64).saturating_mul(2);
        let mut snapshots = self
            .runtime_snapshots
            .write()
            .expect("mail runtime snapshot lock poisoned");
        // Slot đã mất lease/pod-local task đã dừng tự rời memory; không cần
        // tombstone hoặc cleanup record trong Zone KV/Victoria.
        snapshots.retain(|_, snapshot| {
            now_ms.saturating_sub(snapshot.heartbeat_unix_ms) <= freshness_ms
        });
        snapshots.values().cloned().collect()
    }
}

#[cfg(test)]
#[path = "../test/runtime_context.rs"]
mod tests;
