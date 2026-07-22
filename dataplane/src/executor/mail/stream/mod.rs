pub mod kafka;
pub mod nats_jetstream;
pub mod rabbitmq;
pub mod redis_stream;

use super::runtime_configuration::RuntimeConsumerConfiguration;
use super::runtime_proto::MailStreamType;
use super::stream_processor::MailStreamProcessor;
use crate::config::Config;
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
use aes_gcm::aead::{Aead, KeyInit, Payload};
use aes_gcm::{Aes256Gcm, Nonce};
use bytes::Bytes;
use serde::Serialize;
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

#[derive(Serialize)]
struct RuntimeHealthSnapshot<'a> {
    state: &'a str,
    consumer_id: &'a str,
    config_version: u64,
    runtime_generation: u64,
    slot: u32,
    fencing_token: u64,
    heartbeat_unix_ms: u64,
    error_code: &'a str,
}

/// [COMMENT]: Context này chỉ chứa Aurora scheduling/config/JMAP dependencies; broker connection và settlement vẫn nằm trong từng suite.
pub struct StreamRuntimeContext {
    pub zone_id: String,
    pub instance_id: String,
    pub zone_kv: Arc<ZoneKvStore>,
    pub processor: Arc<MailStreamProcessor>,
    pub lease_ttl: Duration,
    pub max_message_bytes: usize,
    pub max_slot_inflight: usize,
    envelope_key: Option<Arc<Zeroizing<[u8; 32]>>>,
}

impl StreamRuntimeContext {
    pub fn new(
        config: &Config,
        instance_id: String,
        zone_kv: Arc<ZoneKvStore>,
        processor: Arc<MailStreamProcessor>,
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
            instance_id,
            zone_kv,
            processor,
            lease_ttl: Duration::from_secs(config.mail_stream_slot_lease_ttl_seconds),
            max_message_bytes: config.mail_max_message_bytes,
            max_slot_inflight: config.mail_stream_max_inflight_per_slot,
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
            _ => false,
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
        let snapshot = RuntimeHealthSnapshot {
            state,
            consumer_id: &configuration.consumer_id,
            config_version: configuration.config_version,
            runtime_generation: generation,
            slot,
            fencing_token: lease.fencing_token,
            heartbeat_unix_ms: SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
                .unwrap_or_default(),
            error_code,
        };
        if let Ok(bytes) = serde_json::to_vec(&snapshot) {
            let key = format!("mail.runtime.{}.{}", configuration.consumer_id, slot);
            let _ = self
                .zone_kv
                .health_put_fenced(&key, Bytes::from(bytes), lease.fencing_token)
                .await;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn generation_fence_fails_closed_after_local_lease_deadline() {
        let fence = RuntimeGenerationFence::new(Duration::ZERO);
        assert!(!fence.is_accepting());

        fence.refresh_lease(Duration::from_secs(1));
        assert!(fence.is_accepting());
    }
}
