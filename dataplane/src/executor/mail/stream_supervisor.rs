use super::runtime_configuration::{
    MailConfigurationRuntime, RuntimeConsumerConfiguration, RuntimeConsumerEntry,
    RuntimeDesiredState,
};
use super::runtime_proto::{KafkaStreamPayloadV1, MailStreamType};
use crate::config::Config;
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
use crate::observability::logger::Logger;
use aes_gcm::aead::{Aead, KeyInit, Payload};
use aes_gcm::{Aes256Gcm, Nonce};
use bytes::Bytes;
use krafka::auth::{AuthConfig, TlsConfig};
use krafka::consumer::{AutoOffsetReset, Consumer};
use prost::Message;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tokio::sync::{mpsc, Mutex as AsyncMutex};
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;
use zeroize::{Zeroize, Zeroizing};

const ENVELOPE_MAGIC: &[u8; 4] = b"AMS1";
const ENVELOPE_NONCE_BYTES: usize = 12;

#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
struct RuntimeSlotKey {
    consumer_id: String,
    slot: u32,
}

struct RuntimeSlotHandle {
    config_version: u64,
    config_sha256: [u8; 32],
    cancel: CancellationToken,
    task: JoinHandle<()>,
}

/// [COMMENT]: Coordinate giữ đủ thông tin để Phase 8 commit đúng broker, nhưng Phase 6 tuyệt đối không auto-ACK.
#[allow(dead_code)]
#[derive(Clone, Debug)]
pub enum MailStreamCoordinate {
    Kafka {
        topic: String,
        partition: i32,
        offset: i64,
    },
}

/// [COMMENT]: Record qua bounded channel mang captured generation + fencing token để callback cũ bị loại sau COW.
#[allow(dead_code)]
#[derive(Clone, Debug)]
pub struct MailStreamRecord {
    pub consumer_id: String,
    pub config_version: u64,
    pub runtime_generation: u64,
    pub fencing_token: u64,
    pub coordinate: MailStreamCoordinate,
    pub payload: Bytes,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct KafkaConnectionConfigV1 {
    bootstrap_servers: String,
    security_protocol: String,
    #[serde(default)]
    username: Option<String>,
    #[serde(default)]
    password: Option<String>,
}

impl Drop for KafkaConnectionConfigV1 {
    fn drop(&mut self) {
        // [COMMENT]: Plaintext connection material chỉ tồn tại trong stack/runtime memory và được zeroize khi rời scope.
        self.bootstrap_servers.zeroize();
        self.security_protocol.zeroize();
        self.username.zeroize();
        self.password.zeroize();
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

/// [COMMENT]: Một supervisor chung cho toàn pod; không tạo ticker riêng cho mỗi consumer.
pub struct MailStreamSupervisor {
    zone_id: String,
    instance_id: String,
    configuration: Arc<MailConfigurationRuntime>,
    zone_kv: Arc<ZoneKvStore>,
    reconcile_interval: Duration,
    lease_ttl: Duration,
    max_slots_per_pod: usize,
    claim_cursor: AtomicU64,
    envelope_key: Option<Arc<Zeroizing<[u8; 32]>>>,
    ingress_tx: mpsc::Sender<MailStreamRecord>,
    ingress_rx: AsyncMutex<mpsc::Receiver<MailStreamRecord>>,
    cancel: CancellationToken,
    task: Mutex<Option<JoinHandle<()>>>,
}

impl MailStreamSupervisor {
    pub fn new(
        config: &Config,
        configuration: Arc<MailConfigurationRuntime>,
        zone_kv: Arc<ZoneKvStore>,
    ) -> Arc<Self> {
        let envelope_key = if config.mail_stream_envelope_key_hex.len() == 64 {
            let mut key = [0_u8; 32];
            let mut valid = true;
            let encoded = config.mail_stream_envelope_key_hex.as_bytes();
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
        let (ingress_tx, ingress_rx) = mpsc::channel(config.mail_stream_ingress_capacity);
        Arc::new(Self {
            zone_id: config.zone_id.clone(),
            instance_id: std::env::var("HOSTNAME")
                .unwrap_or_else(|_| format!("dataplane-{}", std::process::id())),
            configuration,
            zone_kv,
            reconcile_interval: Duration::from_millis(config.mail_stream_supervisor_interval_ms),
            lease_ttl: Duration::from_secs(config.mail_stream_slot_lease_ttl_seconds),
            max_slots_per_pod: config.mail_stream_max_slots_per_pod,
            claim_cursor: AtomicU64::new(0),
            envelope_key,
            ingress_tx,
            ingress_rx: AsyncMutex::new(ingress_rx),
            cancel: CancellationToken::new(),
            task: Mutex::new(None),
        })
    }

    pub fn start(self: &Arc<Self>) {
        let mut task = self
            .task
            .lock()
            .expect("mail supervisor task mutex poisoned");
        if task.is_some() {
            return;
        }
        let supervisor = self.clone();
        *task = Some(tokio::spawn(async move {
            supervisor.run().await;
        }));
    }

    /// [COMMENT]: Phase 7 lấy record qua API này; receiver duy nhất bảo toàn ownership và bounded backpressure.
    #[allow(dead_code)]
    pub async fn next_record(&self) -> Option<MailStreamRecord> {
        self.ingress_rx.lock().await.recv().await
    }

    pub async fn shutdown(&self) {
        self.cancel.cancel();
        let task = self
            .task
            .lock()
            .expect("mail supervisor task mutex poisoned")
            .take();
        if let Some(task) = task {
            let _ = task.await;
        }
    }

    async fn run(self: Arc<Self>) {
        let mut hasher = std::collections::hash_map::DefaultHasher::new();
        self.zone_id.hash(&mut hasher);
        self.instance_id.hash(&mut hasher);
        "mail-phase-6-supervisor".hash(&mut hasher);
        let jitter = Duration::from_millis(hasher.finish() % 2_000);
        tokio::select! {
            _ = self.cancel.cancelled() => return,
            _ = tokio::time::sleep(jitter) => {}
        }

        let mut slots = HashMap::<RuntimeSlotKey, RuntimeSlotHandle>::new();
        let mut last_snapshot = None;
        let mut last_claim_attempt = Instant::now() - Duration::from_secs(30);
        loop {
            let snapshot = self.configuration.snapshot();
            let config_changed = last_snapshot
                .as_ref()
                .is_none_or(|previous| !Arc::ptr_eq(previous, &snapshot));
            let has_finished = slots.values().any(|slot| slot.task.is_finished());
            if config_changed
                || has_finished
                || last_claim_attempt.elapsed() >= Duration::from_secs(5)
            {
                self.reconcile(snapshot.clone(), &mut slots).await;
                last_snapshot = Some(snapshot);
                last_claim_attempt = Instant::now();
            }
            tokio::select! {
                _ = self.cancel.cancelled() => break,
                _ = tokio::time::sleep(self.reconcile_interval) => {}
            }
        }

        // [COMMENT]: Shutdown fence intake trước, chờ adapter leave group, rồi mới kết thúc supervisor.
        for slot in slots.values() {
            slot.cancel.cancel();
        }
        for (_, slot) in slots {
            let _ = slot.task.await;
        }
    }

    async fn reconcile(
        self: &Arc<Self>,
        snapshot: Arc<HashMap<String, RuntimeConsumerEntry>>,
        slots: &mut HashMap<RuntimeSlotKey, RuntimeSlotHandle>,
    ) {
        let mut remove = Vec::new();
        for (key, slot) in slots.iter() {
            let still_desired = snapshot.get(&key.consumer_id).is_some_and(|entry| {
                matches!(entry, RuntimeConsumerEntry::Active(configuration)
                    if configuration.desired_state == RuntimeDesiredState::Enabled
                        && key.slot < configuration.parallelism
                        && slot.config_version == configuration.config_version
                        && slot.config_sha256 == configuration.config_sha256)
            });
            if !still_desired {
                slot.cancel.cancel();
            }
            if slot.task.is_finished() {
                remove.push(key.clone());
            }
        }
        for key in remove {
            if let Some(slot) = slots.remove(&key) {
                let _ = slot.task.await;
            }
        }

        let mut configurations = snapshot
            .values()
            .filter_map(|entry| match entry {
                RuntimeConsumerEntry::Active(configuration)
                    if configuration.desired_state == RuntimeDesiredState::Enabled =>
                {
                    Some(configuration.clone())
                }
                _ => None,
            })
            .collect::<Vec<_>>();
        configurations.sort_by(|left, right| left.consumer_id.cmp(&right.consumer_id));
        if !configurations.is_empty() {
            // [COMMENT]: Rotate deterministic scan head để các slot sau không starvation khi pod luôn chạm hard cap.
            let cursor = self
                .claim_cursor
                .fetch_add(self.max_slots_per_pod.max(1) as u64, Ordering::Relaxed)
                as usize
                % configurations.len();
            configurations.rotate_left(cursor);
        }

        for configuration in configurations {
            if configuration.stream.stream_type != MailStreamType::Kafka {
                // [COMMENT]: Outer contract nhận diện mọi type; adapter chưa ship chỉ cô lập consumer, không panic supervisor.
                let snapshot = RuntimeHealthSnapshot {
                    state: "ERROR",
                    consumer_id: &configuration.consumer_id,
                    config_version: configuration.config_version,
                    runtime_generation: 0,
                    slot: 0,
                    fencing_token: 0,
                    heartbeat_unix_ms: now_unix_ms(),
                    error_code: "MAIL_STREAM_ADAPTER_UNSUPPORTED",
                };
                if let Ok(bytes) = serde_json::to_vec(&snapshot) {
                    let _ = self
                        .zone_kv
                        .health_put_fenced(
                            &format!("mail.runtime.{}.0", configuration.consumer_id),
                            Bytes::from(bytes),
                            0,
                        )
                        .await;
                }
                continue;
            }
            for slot_number in 0..configuration.parallelism {
                if slots.len() >= self.max_slots_per_pod {
                    return;
                }
                let key = RuntimeSlotKey {
                    consumer_id: configuration.consumer_id.clone(),
                    slot: slot_number,
                };
                if slots.contains_key(&key) {
                    continue;
                }
                let cancel = self.cancel.child_token();
                let slot_cancel = cancel.clone();
                let supervisor = self.clone();
                let slot_configuration = configuration.clone();
                let task = tokio::spawn(async move {
                    supervisor
                        .run_kafka_slot(slot_configuration, slot_number, slot_cancel)
                        .await;
                });
                slots.insert(
                    key,
                    RuntimeSlotHandle {
                        config_version: configuration.config_version,
                        config_sha256: configuration.config_sha256,
                        cancel,
                        task,
                    },
                );
            }
        }
    }

    async fn run_kafka_slot(
        &self,
        configuration: Arc<RuntimeConsumerConfiguration>,
        slot: u32,
        cancel: CancellationToken,
    ) {
        let kafka = match KafkaStreamPayloadV1::decode(configuration.stream.payload.as_slice()) {
            Ok(payload) if configuration.stream.payload_schema_version == 1 => payload,
            _ => {
                Logger::sys_warn(
                    "mail.stream.runtime",
                    "Kafka adapter payload is invalid",
                    "MAIL_KAFKA_PAYLOAD_INVALID",
                );
                return;
            }
        };

        let lease_key = format!("mail.consumer.slot.{}.{}", configuration.consumer_id, slot);
        let owner_id = format!(
            "{}:{}:{}",
            self.instance_id, configuration.consumer_id, slot
        );
        let mut hasher = std::collections::hash_map::DefaultHasher::new();
        owner_id.hash(&mut hasher);
        lease_key.hash(&mut hasher);
        tokio::select! {
            _ = cancel.cancelled() => return,
            _ = tokio::time::sleep(Duration::from_millis(hasher.finish() % 750)) => {}
        }
        let lease = match self
            .zone_kv
            .acquire_lease(&lease_key, &owner_id, self.lease_ttl)
            .await
        {
            Ok(Some(lease)) => lease,
            Ok(None) => return,
            Err(_) => {
                Logger::sys_debug("mail.stream.runtime", "MAIL_STREAM_SLOT_LEASE_UNAVAILABLE");
                return;
            }
        };
        let generation = lease.fencing_token;
        self.write_health("STARTING", &configuration, slot, generation, &lease, "")
            .await;

        let Some(envelope_key) = &self.envelope_key else {
            self.write_health(
                "ERROR",
                &configuration,
                slot,
                generation,
                &lease,
                "MAIL_STREAM_ENVELOPE_KEY_UNAVAILABLE",
            )
            .await;
            tokio::select! {
                _ = cancel.cancelled() => {}
                _ = tokio::time::sleep(self.lease_ttl) => {}
            }
            let _ = self.zone_kv.release_lease(&lease).await;
            return;
        };
        let aad = format!(
            "aurora-mail-stream-envelope-v1\0{}\0{}\0{}",
            self.zone_id,
            uuid::Uuid::from_bytes(configuration.stream.broker_resource_id),
            MailStreamType::Kafka as i32,
        );
        let connection = match decrypt_kafka_connection(
            envelope_key.as_ref(),
            &kafka.source_config_envelope,
            aad.as_bytes(),
        ) {
            Ok(connection) => connection,
            Err(code) => {
                self.write_health("ERROR", &configuration, slot, generation, &lease, code)
                    .await;
                tokio::select! {
                    _ = cancel.cancelled() => {}
                    _ = tokio::time::sleep(self.lease_ttl) => {}
                }
                let _ = self.zone_kv.release_lease(&lease).await;
                return;
            }
        };

        let static_member = format!("aurora-{}-{}", configuration.consumer_id, slot);
        let max_message_bytes = Config::get_global()
            .mail_max_message_bytes
            .min(i32::MAX as usize);
        let mut builder = Consumer::builder()
            .bootstrap_servers(connection.bootstrap_servers.clone())
            .group_id(kafka.consumer_group.clone())
            .client_id(format!("aurora-mail-{}-{slot}", configuration.consumer_id))
            .group_instance_id(static_member)
            .auto_offset_reset(AutoOffsetReset::Earliest)
            // [COMMENT]: Phase 6 không được commit trước terminal boundary Phase 8.
            .enable_auto_commit(false)
            .max_poll_records(64)
            .max_partition_fetch_bytes(max_message_bytes as i32)
            .fetch_max_bytes(max_message_bytes.saturating_mul(64).min(i32::MAX as usize) as i32)
            .request_timeout(Duration::from_secs(10));
        let mut tls = TlsConfig::new().with_native_roots();
        if let Some(ca_path) = &Config::get_global().mail_stream_ca_cert_path {
            // [COMMENT]: CA path chỉ đến từ pod deployment; ciphertext khách hàng không thể ép DP đọc file tùy ý.
            tls = tls.with_ca_cert(ca_path);
        }
        builder = match connection.security_protocol.as_str() {
            "ssl" => builder.auth(AuthConfig::ssl(tls)),
            "sasl_plain_ssl" => {
                let (Some(username), Some(password)) =
                    (connection.username.as_ref(), connection.password.as_ref())
                else {
                    self.write_health(
                        "ERROR",
                        &configuration,
                        slot,
                        generation,
                        &lease,
                        "MAIL_KAFKA_CREDENTIAL_REQUIRED",
                    )
                    .await;
                    tokio::select! {
                        _ = cancel.cancelled() => {}
                        _ = tokio::time::sleep(self.lease_ttl) => {}
                    }
                    let _ = self.zone_kv.release_lease(&lease).await;
                    return;
                };
                match AuthConfig::sasl_plain_ssl(username, password, tls) {
                    Ok(auth) => builder.auth(auth),
                    Err(_) => {
                        self.write_health(
                            "ERROR",
                            &configuration,
                            slot,
                            generation,
                            &lease,
                            "MAIL_KAFKA_CREDENTIAL_INVALID",
                        )
                        .await;
                        tokio::select! {
                            _ = cancel.cancelled() => {}
                            _ = tokio::time::sleep(self.lease_ttl) => {}
                        }
                        let _ = self.zone_kv.release_lease(&lease).await;
                        return;
                    }
                }
            }
            _ => {
                self.write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_KAFKA_SECURITY_PROTOCOL_UNSUPPORTED",
                )
                .await;
                tokio::select! {
                    _ = cancel.cancelled() => {}
                    _ = tokio::time::sleep(self.lease_ttl) => {}
                }
                let _ = self.zone_kv.release_lease(&lease).await;
                return;
            }
        };

        let consumer = match builder.build().await {
            Ok(consumer) => consumer,
            Err(_) => {
                self.write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_KAFKA_CONNECTION_FAILED",
                )
                .await;
                let _ = self.zone_kv.release_lease(&lease).await;
                return;
            }
        };
        if consumer.subscribe(&[kafka.topic.as_str()]).await.is_err() {
            self.write_health(
                "ERROR",
                &configuration,
                slot,
                generation,
                &lease,
                "MAIL_KAFKA_SUBSCRIBE_FAILED",
            )
            .await;
            let _ = consumer.close().await;
            let _ = self.zone_kv.release_lease(&lease).await;
            return;
        }
        self.write_health("RUNNING", &configuration, slot, generation, &lease, "")
            .await;

        let renew_every = (self.lease_ttl / 3).max(Duration::from_secs(1));
        let mut renew = tokio::time::interval(renew_every);
        renew.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
        let mut consecutive_poll_errors = 0_u8;
        'runtime: loop {
            tokio::select! {
                _ = cancel.cancelled() => break,
                _ = renew.tick() => {
                    if !self.renew_and_report(&configuration, slot, generation, &lease).await {
                        break;
                    }
                }
                result = consumer.poll(Duration::from_secs(1)) => {
                    let records = match result {
                        Ok(records) => {
                            consecutive_poll_errors = 0;
                            records
                        }
                        Err(_) => {
                            consecutive_poll_errors = consecutive_poll_errors.saturating_add(1);
                            if consecutive_poll_errors >= 5 {
                                self.write_health(
                                    "ERROR",
                                    &configuration,
                                    slot,
                                    generation,
                                    &lease,
                                    "MAIL_KAFKA_POLL_FAILED",
                                ).await;
                                break;
                            }
                            tokio::time::sleep(Duration::from_millis(100_u64 << consecutive_poll_errors)).await;
                            continue;
                        }
                    };
                    for record in records {
                        let message = MailStreamRecord {
                            consumer_id: configuration.consumer_id.clone(),
                            config_version: configuration.config_version,
                            runtime_generation: generation,
                            fencing_token: lease.fencing_token,
                            coordinate: MailStreamCoordinate::Kafka {
                                topic: record.topic,
                                partition: record.partition,
                                offset: record.offset,
                            },
                            payload: record.value.unwrap_or_default(),
                        };
                        let send = self.ingress_tx.send(message);
                        tokio::pin!(send);
                        loop {
                            tokio::select! {
                                _ = cancel.cancelled() => break 'runtime,
                                _ = renew.tick() => {
                                    if !self.renew_and_report(&configuration, slot, generation, &lease).await {
                                        break 'runtime;
                                    }
                                }
                                result = &mut send => {
                                    if result.is_err() {
                                        break 'runtime;
                                    }
                                    break;
                                }
                            }
                        }
                    }
                }
            }
        }

        // [COMMENT]: Không commit khi drain Phase 6; record chưa terminal sẽ được Kafka redeliver sau leave group.
        let _ = consumer.close().await;
        self.write_health("STOPPED", &configuration, slot, generation, &lease, "")
            .await;
        let _ = self.zone_kv.release_lease(&lease).await;
    }

    async fn renew_and_report(
        &self,
        configuration: &RuntimeConsumerConfiguration,
        slot: u32,
        generation: u64,
        lease: &ZoneLease,
    ) -> bool {
        match self.zone_kv.renew_lease(lease, self.lease_ttl).await {
            Ok(true) => {
                self.write_health("RUNNING", configuration, slot, generation, lease, "")
                    .await;
                true
            }
            _ => false,
        }
    }

    async fn write_health(
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
            heartbeat_unix_ms: now_unix_ms(),
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

fn decrypt_kafka_connection(
    key: &[u8; 32],
    envelope: &[u8],
    aad: &[u8],
) -> Result<KafkaConnectionConfigV1, &'static str> {
    if envelope.len() < ENVELOPE_MAGIC.len() + 1 + ENVELOPE_NONCE_BYTES + 16
        || &envelope[..4] != ENVELOPE_MAGIC
        || envelope[4] != 1
    {
        return Err("MAIL_STREAM_ENVELOPE_FORMAT_INVALID");
    }
    let nonce = Nonce::from_slice(&envelope[5..5 + ENVELOPE_NONCE_BYTES]);
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|_| "MAIL_STREAM_ENVELOPE_KEY_INVALID")?;
    let plaintext = Zeroizing::new(
        cipher
            .decrypt(
                nonce,
                Payload {
                    msg: &envelope[5 + ENVELOPE_NONCE_BYTES..],
                    aad,
                },
            )
            .map_err(|_| "MAIL_STREAM_ENVELOPE_AUTH_FAILED")?,
    );
    if plaintext.len() > 16 << 10 {
        return Err("MAIL_STREAM_ENVELOPE_PLAINTEXT_TOO_LARGE");
    }
    let connection = serde_json::from_slice::<KafkaConnectionConfigV1>(&plaintext)
        .map_err(|_| "MAIL_KAFKA_CONNECTION_CONFIG_INVALID")?;
    if connection.bootstrap_servers.trim().is_empty()
        || connection.bootstrap_servers.len() > 4_096
        || connection.bootstrap_servers.split(',').count() > 32
        || connection.bootstrap_servers.chars().any(char::is_control)
    {
        return Err("MAIL_KAFKA_BOOTSTRAP_SERVERS_INVALID");
    }
    Ok(connection)
}

fn now_unix_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;
    use aes_gcm::aead::Aead;

    #[test]
    fn encrypted_connection_requires_matching_aad() {
        let key = [7_u8; 32];
        let nonce = [3_u8; ENVELOPE_NONCE_BYTES];
        let aad = b"aurora-mail-stream-envelope-v1\0zone-a\0broker-a\x001";
        let plaintext = br#"{"bootstrap_servers":"kafka.internal:9093","security_protocol":"ssl"}"#;
        let cipher = Aes256Gcm::new_from_slice(&key).expect("test key");
        let ciphertext = cipher
            .encrypt(
                Nonce::from_slice(&nonce),
                Payload {
                    msg: plaintext,
                    aad,
                },
            )
            .expect("test encrypt");
        let mut envelope = ENVELOPE_MAGIC.to_vec();
        envelope.push(1);
        envelope.extend_from_slice(&nonce);
        envelope.extend_from_slice(&ciphertext);

        let connection = decrypt_kafka_connection(&key, &envelope, aad).expect("decrypt");
        assert_eq!(connection.security_protocol, "ssl");
        assert!(decrypt_kafka_connection(&key, &envelope, b"wrong-aad").is_err());
    }
}
