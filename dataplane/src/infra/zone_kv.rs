use async_nats::jetstream::{self, kv, stream::StorageType};
use bytes::Bytes;
use futures_util::TryStreamExt;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ConsumerConfigHead {
    pub version: u64,
    pub event_id: String,
    pub config_sha256: String,
    pub desired_state: String,
    pub tombstoned: bool,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct TemplateConfigHead {
    pub revision: u64,
    pub event_id: String,
    pub current_version: u64,
    pub content_sha256: String,
    pub tombstoned: bool,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ZoneMetadata {
    pub status: String,
    #[serde(default)]
    pub services: HashMap<String, bool>,
    pub updated_at: u64,
}

impl Default for ZoneMetadata {
    fn default() -> Self {
        Self {
            // [COMMENT]: Chưa hydrate metadata thì fail-closed; reconciler phải xác nhận Zone active trước khi nhận job.
            status: "inactive".to_string(),
            services: HashMap::new(),
            updated_at: 0,
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
struct LeaseValue {
    owner_id: String,
    fencing_token: u64,
    expires_at_unix_ms: u64,
    #[serde(default)]
    last_owner_id: String,
    #[serde(default)]
    released_at_unix_ms: u64,
}

#[derive(Clone, Debug)]
pub struct ZoneLease {
    pub key: String,
    pub owner_id: String,
    pub fencing_token: u64,
}

#[derive(Clone, Debug, Deserialize, Serialize, Eq, PartialEq)]
pub struct StorageAccessRecord {
    pub access_session_id: String,
    pub binding_hash: String,
    pub actor_id: String,
    pub resource_id: String,
    pub bucket_name: String,
    pub workspace_id: String,
    pub zone_id: String,
    pub actions: Vec<String>,
    pub key_prefix: String,
    pub expires_at_unix_seconds: u64,
    pub policy_revision: u64,
}

/// [COMMENT]: Các bucket tách config, health, access và coordination để tải
/// authorization ngắn hạn không ép retention hoặc quyền ACL của loại state khác.
#[derive(Clone)]
pub struct ZoneKvStore {
    config: kv::Store,
    health: kv::Store,
    access: kv::Store,
    coordination: kv::Store,
}

/// Deployment-owned capabilities used only while opening the Zone KV session.
/// Keeping these inputs named prevents the Zone KV workflow from growing an
/// argument list whose fields can be confused with business data.
pub struct ZoneKvConnectionConfig<'a> {
    pub url: &'a str,
    pub ca_cert: &'a str,
    pub creds: &'a str,
    pub client_cert: Option<&'a str>,
    pub client_key: Option<&'a str>,
}

impl ZoneKvStore {
    pub async fn connect(
        connection: ZoneKvConnectionConfig<'_>,
        replicas: usize,
    ) -> Result<Arc<Self>, String> {
        if !matches!(replicas, 1 | 3 | 5) {
            return Err("NATS_ZONE_KV_REPLICAS must be 1, 3 or 5".to_string());
        }
        if connection.client_cert.is_some() != connection.client_key.is_some() {
            return Err(
                "NATS_ZONE_TLS_CERT and NATS_ZONE_TLS_KEY must be configured together".to_string(),
            );
        }
        let mut options = async_nats::ConnectOptions::new()
            .add_root_certificates(PathBuf::from(connection.ca_cert))
            .require_tls(true);
        if let (Some(cert), Some(key)) = (connection.client_cert, connection.client_key) {
            options = options.add_client_certificate(PathBuf::from(cert), PathBuf::from(key));
        }
        let options = options
            .credentials_file(PathBuf::from(connection.creds))
            .await
            .map_err(|error| format!("read Zone NATS credentials: {error}"))?;
        let client = options
            .connect(connection.url)
            .await
            .map_err(|error| format!("connect Zone NATS: {error}"))?;
        let js = jetstream::new(client);

        let config = match js.get_key_value("AURORA_ZONE_CONFIG").await {
            Ok(store) => store,
            Err(_) => match js
                .create_key_value(kv::Config {
                    bucket: "AURORA_ZONE_CONFIG".to_string(),
                    description: "Aurora Zone projected configuration".to_string(),
                    history: 1,
                    max_value_size: 4 * 1024 * 1024,
                    storage: StorageType::File,
                    num_replicas: replicas,
                    ..Default::default()
                })
                .await
            {
                Ok(store) => store,
                Err(create_error) => {
                    js.get_key_value("AURORA_ZONE_CONFIG")
                        .await
                        .map_err(|get_error| {
                            format!(
                                "create Zone config KV: {create_error}; reopen failed: {get_error}"
                            )
                        })?
                }
            },
        };
        let health = match js.get_key_value("AURORA_ZONE_HEALTH").await {
            Ok(store) => store,
            Err(_) => match js
                .create_key_value(kv::Config {
                    bucket: "AURORA_ZONE_HEALTH".to_string(),
                    description: "Aurora Zone current health snapshots".to_string(),
                    history: 1,
                    max_age: Duration::from_secs(86_400),
                    max_value_size: 1024 * 1024,
                    storage: StorageType::File,
                    num_replicas: replicas,
                    ..Default::default()
                })
                .await
            {
                Ok(store) => store,
                Err(create_error) => {
                    js.get_key_value("AURORA_ZONE_HEALTH")
                        .await
                        .map_err(|get_error| {
                            format!(
                                "create Zone health KV: {create_error}; reopen failed: {get_error}"
                            )
                        })?
                }
            },
        };
        let coordination = match js.get_key_value("AURORA_ZONE_COORDINATION").await {
            Ok(store) => store,
            Err(_) => match js
                .create_key_value(kv::Config {
                    bucket: "AURORA_ZONE_COORDINATION".to_string(),
                    description: "Aurora Zone short-lived fenced leases".to_string(),
                    history: 1,
                    max_age: Duration::from_secs(86_400),
                    max_value_size: 64 * 1024,
                    storage: StorageType::File,
                    num_replicas: replicas,
                    ..Default::default()
                })
                .await
            {
                Ok(store) => store,
                Err(create_error) => js
                    .get_key_value("AURORA_ZONE_COORDINATION")
                    .await
                    .map_err(|get_error| format!("create Zone coordination KV: {create_error}; reopen failed: {get_error}"))?,
            },
        };
        let access = match js.get_key_value("AURORA_ZONE_ACCESS").await {
            Ok(store) => store,
            Err(_) => match js
                .create_key_value(kv::Config {
                    bucket: "AURORA_ZONE_ACCESS".to_string(),
                    description: "Aurora Zone ephemeral storage authorization projections"
                        .to_string(),
                    history: 1,
                    max_age: Duration::from_secs(86_400),
                    max_value_size: 32 * 1024,
                    max_bytes: 256 * 1024 * 1024,
                    storage: StorageType::File,
                    num_replicas: replicas,
                    ..Default::default()
                })
                .await
            {
                Ok(store) => store,
                Err(create_error) => {
                    js.get_key_value("AURORA_ZONE_ACCESS")
                        .await
                        .map_err(|get_error| {
                            format!(
                                "create Zone access KV: {create_error}; reopen failed: {get_error}"
                            )
                        })?
                }
            },
        };

        // [COMMENT]: Bucket tồn tại nhưng sai durability contract phải fail-fast thay vì âm thầm hạ HA.
        for (name, store) in [
            ("config", &config),
            ("health", &health),
            ("access", &access),
            ("coordination", &coordination),
        ] {
            let status = store
                .status()
                .await
                .map_err(|error| format!("read Zone {name} KV status: {error}"))?;
            if status.history() != 1
                || status.info.config.storage != StorageType::File
                || status.info.config.num_replicas < replicas
                || (name == "config" && !status.info.config.max_age.is_zero())
                || (name != "config" && status.info.config.max_age != Duration::from_secs(86_400))
            {
                return Err(format!(
                    "Zone {name} KV violates history, storage, replica or retention contract"
                ));
            }
        }
        Ok(Arc::new(Self {
            config,
            health,
            coordination,
            access,
        }))
    }

    /// Idempotent CAS projection. A replay with identical content succeeds;
    /// a conflicting command for the same session id is rejected rather than
    /// widening privileges after a producer bug or malicious replay.
    pub async fn access_put(&self, record: &StorageAccessRecord) -> Result<(), String> {
        let value = Bytes::from(serde_json::to_vec(record).map_err(|error| error.to_string())?);
        for _ in 0..5 {
            let current = self
                .access
                .entry(record.access_session_id.clone())
                .await
                .map_err(|error| error.to_string())?;
            if let Some(entry) = &current {
                let existing: StorageAccessRecord = serde_json::from_slice(&entry.value)
                    .map_err(|error| format!("storage access record corrupt: {error}"))?;
                if existing == *record {
                    return Ok(());
                }
                return Err("conflicting storage access session replay".to_string());
            }
            if self
                .access
                .update(&record.access_session_id, value.clone(), 0)
                .await
                .is_ok()
            {
                return Ok(());
            }
        }
        Err("storage access session CAS contention".to_string())
    }

    pub async fn config_keys_page(
        &self,
        skip: usize,
        limit: usize,
    ) -> Result<(Vec<String>, bool), String> {
        let mut stream = self
            .config
            .keys()
            .await
            .map_err(|error| error.to_string())?;
        let mut seen = 0_usize;
        let mut keys = Vec::with_capacity(limit);
        while let Some(key) = stream.try_next().await.map_err(|error| error.to_string())? {
            if seen < skip {
                seen += 1;
                continue;
            }
            if keys.len() == limit {
                return Ok((keys, true));
            }
            keys.push(key);
        }
        Ok((keys, false))
    }

    pub async fn config_entry(&self, key: impl Into<String>) -> Result<Option<kv::Entry>, String> {
        self.config
            .entry(key)
            .await
            .map_err(|error| error.to_string())
    }

    pub async fn config_get(&self, key: impl Into<String>) -> Result<Option<Bytes>, String> {
        self.config
            .get(key)
            .await
            .map_err(|error| error.to_string())
    }

    pub async fn config_create(&self, key: impl AsRef<str>, value: Bytes) -> Result<u64, String> {
        self.config
            .update(key, value, 0)
            .await
            .map_err(|error| error.to_string())
    }

    pub async fn config_update(
        &self,
        key: impl AsRef<str>,
        value: Bytes,
        revision: u64,
    ) -> Result<u64, String> {
        self.config
            .update(key, value, revision)
            .await
            .map_err(|error| error.to_string())
    }

    pub async fn config_delete(&self, key: impl AsRef<str>) -> Result<(), String> {
        self.config
            .delete(key)
            .await
            .map_err(|error| error.to_string())
    }

    pub async fn health_put(&self, key: impl AsRef<str>, value: Bytes) -> Result<u64, String> {
        self.health
            .put(key, value)
            .await
            .map_err(|error| error.to_string())
    }

    pub async fn coordination_get(&self, key: impl Into<String>) -> Result<Option<Bytes>, String> {
        self.coordination
            .get(key)
            .await
            .map_err(|error| error.to_string())
    }

    pub async fn read_zone_metadata(&self) -> Result<ZoneMetadata, String> {
        let snapshot = crate::observability::otel::OtelTracer::trace_result(
            "KV GetZoneMetadata",
            opentelemetry::trace::SpanKind::Client,
            vec![
                opentelemetry::KeyValue::new("db.system", "nats_jetstream_kv"),
                opentelemetry::KeyValue::new("db.namespace", "AURORA_ZONE_CONFIG"),
                opentelemetry::KeyValue::new("db.operation.name", "get"),
            ],
            self.config_get("zone.metadata"),
        )
        .await?;
        match snapshot {
            Some(bytes) => serde_json::from_slice(&bytes).map_err(|error| error.to_string()),
            None => Ok(ZoneMetadata::default()),
        }
    }

    pub async fn acquire_lease(
        &self,
        key: &str,
        owner_id: &str,
        ttl: Duration,
    ) -> Result<Option<ZoneLease>, String> {
        crate::observability::otel::OtelTracer::trace_result(
            "KV AcquireLease",
            opentelemetry::trace::SpanKind::Client,
            vec![
                opentelemetry::KeyValue::new("db.system", "nats_jetstream_kv"),
                opentelemetry::KeyValue::new("db.namespace", "AURORA_ZONE_COORDINATION"),
                opentelemetry::KeyValue::new("db.operation.name", "cas"),
            ],
            self.acquire_lease_internal(key, owner_id, ttl),
        )
        .await
    }

    async fn acquire_lease_internal(
        &self,
        key: &str,
        owner_id: &str,
        ttl: Duration,
    ) -> Result<Option<ZoneLease>, String> {
        for _ in 0..5 {
            let current = self
                .coordination
                .entry(key.to_string())
                .await
                .map_err(|error| error.to_string())?;
            let previous = current
                .as_ref()
                .map(|entry| serde_json::from_slice::<LeaseValue>(&entry.value))
                .transpose()
                .map_err(|error| format!("lease value corrupt for {key}: {error}"))?;
            let now = now_unix_ms();
            if previous
                .as_ref()
                .is_some_and(|lease| !lease.owner_id.is_empty() && lease.expires_at_unix_ms > now)
            {
                return Ok(None);
            }
            let previous_owner = previous.as_ref().map_or("", |lease| {
                if lease.owner_id.is_empty() {
                    &lease.last_owner_id
                } else {
                    &lease.owner_id
                }
            });
            let previous_release = previous.as_ref().map_or(0, |lease| {
                if lease.owner_id.is_empty() {
                    lease.released_at_unix_ms
                } else {
                    lease.expires_at_unix_ms
                }
            });
            let next = LeaseValue {
                owner_id: owner_id.to_string(),
                fencing_token: previous
                    .as_ref()
                    .map_or(1, |lease| lease.fencing_token.saturating_add(1)),
                expires_at_unix_ms: now
                    .saturating_add(ttl.as_millis().min(u64::MAX as u128) as u64),
                last_owner_id: previous_owner.to_string(),
                released_at_unix_ms: previous_release,
            };
            let value = Bytes::from(serde_json::to_vec(&next).map_err(|error| error.to_string())?);
            let result = match current {
                Some(entry) => self.coordination.update(key, value, entry.revision).await,
                None => self.coordination.update(key, value, 0).await,
            };
            if result.is_ok() {
                return Ok(Some(ZoneLease {
                    key: key.to_string(),
                    owner_id: owner_id.to_string(),
                    fencing_token: next.fencing_token,
                }));
            }
        }
        Ok(None)
    }

    pub async fn renew_lease(&self, lease: &ZoneLease, ttl: Duration) -> Result<bool, String> {
        for _ in 0..3 {
            let Some(entry) = self
                .coordination
                .entry(lease.key.clone())
                .await
                .map_err(|error| error.to_string())?
            else {
                return Ok(false);
            };
            let current: LeaseValue =
                serde_json::from_slice(&entry.value).map_err(|error| error.to_string())?;
            if current.owner_id != lease.owner_id
                || current.fencing_token != lease.fencing_token
                || current.expires_at_unix_ms <= now_unix_ms()
            {
                return Ok(false);
            }
            let next = LeaseValue {
                owner_id: current.owner_id,
                fencing_token: current.fencing_token,
                expires_at_unix_ms: now_unix_ms()
                    .saturating_add(ttl.as_millis().min(u64::MAX as u128) as u64),
                last_owner_id: current.last_owner_id,
                released_at_unix_ms: current.released_at_unix_ms,
            };
            let value = Bytes::from(serde_json::to_vec(&next).map_err(|error| error.to_string())?);
            if self
                .coordination
                .update(&lease.key, value, entry.revision)
                .await
                .is_ok()
            {
                return Ok(true);
            }
        }
        Ok(false)
    }

    pub async fn release_lease(&self, lease: &ZoneLease) -> Result<bool, String> {
        let context = crate::observability::otel::OtelTracer::start_current_span(
            "KV ReleaseLease",
            opentelemetry::trace::SpanKind::Client,
            vec![
                opentelemetry::KeyValue::new("db.system", "nats_jetstream_kv"),
                opentelemetry::KeyValue::new("db.namespace", "AURORA_ZONE_COORDINATION"),
                opentelemetry::KeyValue::new("db.operation.name", "cas"),
            ],
        );
        use opentelemetry::trace::FutureExt;
        let result = self
            .release_lease_internal(lease)
            .with_context(context.clone())
            .await;
        crate::observability::otel::OtelTracer::finish_span(
            &context,
            result.as_ref().err().map(|_| "ZONE_KV_RELEASE_FAILED"),
        );
        result
    }

    async fn release_lease_internal(&self, lease: &ZoneLease) -> Result<bool, String> {
        for _ in 0..3 {
            let Some(entry) = self
                .coordination
                .entry(lease.key.clone())
                .await
                .map_err(|error| error.to_string())?
            else {
                return Ok(false);
            };
            let current: LeaseValue =
                serde_json::from_slice(&entry.value).map_err(|error| error.to_string())?;
            if current.owner_id != lease.owner_id || current.fencing_token != lease.fencing_token {
                return Ok(false);
            }
            let released = LeaseValue {
                owner_id: String::new(),
                fencing_token: current.fencing_token,
                expires_at_unix_ms: now_unix_ms(),
                last_owner_id: current.owner_id,
                released_at_unix_ms: now_unix_ms(),
            };
            let value =
                Bytes::from(serde_json::to_vec(&released).map_err(|error| error.to_string())?);
            if self
                .coordination
                .update(&lease.key, value, entry.revision)
                .await
                .is_ok()
            {
                return Ok(true);
            }
        }
        Ok(false)
    }
}

fn now_unix_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_millis().min(u64::MAX as u128) as u64)
        .unwrap_or_default()
}
