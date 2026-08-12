use async_nats::jetstream::{self, kv, stream::StorageType};
use bytes::Bytes;
use futures_util::TryStreamExt;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use crate::transfer_ticket::config::Config;

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(crate) struct ZoneMetadata {
    pub status: String,
    #[serde(default)]
    pub services: HashMap<String, bool>,
    pub updated_at: u64,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(crate) struct StorageAdmission {
    pub resource_id: String,
    pub resource_name: String,
    pub wallet_version: i64,
    pub admission_mode: String,
    pub restriction_reason: Option<String>,
    pub effective_at_unix_seconds: i64,
    pub valid_until_unix_seconds: Option<i64>,
    pub source_event_id: String,
}

impl Default for ZoneMetadata {
    fn default() -> Self {
        Self {
            status: "inactive".to_string(),
            services: HashMap::new(),
            updated_at: 0,
        }
    }
}

#[derive(Clone)]
pub(crate) struct ZoneControlState {
    config: kv::Store,
    health: kv::Store,
    coordination: kv::Store,
    assignments: kv::Store,
    admission: kv::Store,
}

impl ZoneControlState {
    pub(crate) async fn connect(config: &Config) -> Result<Arc<Self>, String> {
        let options = async_nats::ConnectOptions::new()
            .add_root_certificates(config.nats_ca.clone())
            .require_tls(true)
            .add_client_certificate(config.nats_cert.clone(), config.nats_key.clone())
            .credentials_file(PathBuf::from(&config.nats_creds))
            .await
            .map_err(|error| format!("read Zone NATS credentials: {error}"))?;
        let client =
            tokio::time::timeout(config.nats_timeout, options.connect(&config.nats_zone_url))
                .await
                .map_err(|_| "connect Zone NATS timed out".to_string())?
                .map_err(|error| format!("connect Zone NATS: {error}"))?;
        let js = jetstream::new(client);
        let config_store = get_or_create_store(
            &js,
            config,
            "AURORA_ZONE_CONFIG",
            "Aurora Zone projected configuration",
            Duration::ZERO,
            4 * 1024 * 1024,
        )
        .await?;
        let health_store = get_or_create_store(
            &js,
            config,
            "AURORA_ZONE_HEALTH",
            "Aurora Zone current health snapshots",
            Duration::from_secs(86_400),
            1024 * 1024,
        )
        .await?;
        let coordination_store = get_or_create_store(
            &js,
            config,
            "AURORA_ZONE_COORDINATION",
            "Aurora Zone work directives and fencing state",
            Duration::from_secs(86_400),
            64 * 1024,
        )
        .await?;
        let assignments_store = get_or_create_store(
            &js,
            config,
            "AURORA_ZONE_CONTROL_ASSIGNMENTS",
            "Aurora Zone Control work-unit assignments",
            Duration::from_secs(30),
            64 * 1024,
        )
        .await?;
        let admission_store = get_or_create_store(
            &js,
            config,
            "AURORA_ZONE_ADMISSION",
            "Aurora Storage wallet admission projection",
            Duration::ZERO,
            64 * 1024,
        )
        .await?;
        Ok(Arc::new(Self {
            config: config_store,
            health: health_store,
            coordination: coordination_store,
            assignments: assignments_store,
            admission: admission_store,
        }))
    }

    pub(crate) async fn update_storage_admission(
        &self,
        resource_id: &str,
        next: StorageAdmission,
    ) -> Result<(), String> {
        for _ in 0..5 {
            let current = self
                .admission
                .entry(resource_id.to_string())
                .await
                .map_err(|error| format!("read Storage admission revision: {error}"))?;
            if let Some(entry) = current.as_ref() {
                if let Ok(existing) = serde_json::from_slice::<StorageAdmission>(&entry.value) {
                    if existing.wallet_version >= next.wallet_version {
                        return Ok(());
                    }
                }
            }
            let value = Bytes::from(
                serde_json::to_vec(&next)
                    .map_err(|error| format!("encode Storage admission: {error}"))?,
            );
            let applied = match current {
                Some(entry) => self
                    .admission
                    .update(resource_id, value, entry.revision)
                    .await
                    .is_ok(),
                None => self
                    .admission
                    .create(resource_id.to_string(), value)
                    .await
                    .is_ok(),
            };
            if applied {
                return Ok(());
            }
        }
        Err(format!(
            "Storage admission CAS contention for {resource_id}"
        ))
    }

    pub(crate) async fn update_storage_admission_name_index(
        &self,
        resource_name: &str,
        next: StorageAdmission,
    ) -> Result<(), String> {
        let key = format!("name/{resource_name}");
        for _ in 0..5 {
            let current = self
                .admission
                .entry(key.clone())
                .await
                .map_err(|error| format!("read Storage admission name revision: {error}"))?;
            if let Some(entry) = current.as_ref() {
                if let Ok(existing) = serde_json::from_slice::<StorageAdmission>(&entry.value) {
                    if existing.wallet_version >= next.wallet_version {
                        return Ok(());
                    }
                }
            }
            let value = Bytes::from(
                serde_json::to_vec(&next)
                    .map_err(|error| format!("encode Storage admission name: {error}"))?,
            );
            let applied = match current {
                Some(entry) => self
                    .admission
                    .update(&key, value, entry.revision)
                    .await
                    .is_ok(),
                None => self.admission.create(key.clone(), value).await.is_ok(),
            };
            if applied {
                return Ok(());
            }
        }
        Err(format!(
            "Storage admission name CAS contention for {resource_name}"
        ))
    }

    pub(crate) async fn read_metadata(&self) -> Result<ZoneMetadata, String> {
        let Some(bytes) = self
            .config
            .get("zone.metadata")
            .await
            .map_err(|error| format!("read Zone metadata: {error}"))?
        else {
            return Ok(ZoneMetadata::default());
        };
        serde_json::from_slice(&bytes).map_err(|error| format!("decode Zone metadata: {error}"))
    }

    pub(crate) async fn update_metadata(
        &self,
        status: Option<&str>,
        service: Option<(&str, bool)>,
    ) -> Result<(), String> {
        for _ in 0..5 {
            let current = self
                .config
                .entry("zone.metadata".to_string())
                .await
                .map_err(|error| format!("read Zone metadata revision: {error}"))?;
            let mut metadata = current
                .as_ref()
                .and_then(|entry| serde_json::from_slice::<ZoneMetadata>(&entry.value).ok())
                .unwrap_or_default();
            if let Some(status) = status {
                metadata.status = status.to_string();
            }
            if let Some((service_type, enabled)) = service {
                metadata.services.insert(service_type.to_string(), enabled);
            }
            metadata.updated_at = unix_time_seconds();
            let value = Bytes::from(
                serde_json::to_vec(&metadata)
                    .map_err(|error| format!("encode Zone metadata: {error}"))?,
            );
            let applied = match current {
                Some(entry) => self
                    .config
                    .update("zone.metadata", value, entry.revision)
                    .await
                    .is_ok(),
                None => self.config.create("zone.metadata", value).await.is_ok(),
            };
            if applied {
                return Ok(());
            }
        }
        Err("Zone metadata CAS contention".to_string())
    }

    pub(crate) async fn health_keys(&self) -> Result<Vec<String>, String> {
        let mut stream = self
            .health
            .keys()
            .await
            .map_err(|error| format!("list Zone health keys: {error}"))?;
        let mut keys = Vec::new();
        while let Some(key) = stream
            .try_next()
            .await
            .map_err(|error| format!("read Zone health key: {error}"))?
        {
            keys.push(key);
        }
        Ok(keys)
    }

    pub(crate) async fn health_get(&self, key: &str) -> Result<Option<Bytes>, String> {
        self.health
            .get(key)
            .await
            .map_err(|error| format!("read Zone health {key}: {error}"))
    }

    pub(crate) async fn health_put_fenced(
        &self,
        key: &str,
        value: Bytes,
        assignment_epoch: u64,
    ) -> Result<bool, String> {
        for _ in 0..5 {
            let current = self
                .health
                .entry(key.to_string())
                .await
                .map_err(|error| format!("read Zone health revision {key}: {error}"))?;
            let current_epoch = current
                .as_ref()
                .and_then(|entry| serde_json::from_slice::<serde_json::Value>(&entry.value).ok())
                .and_then(|snapshot| {
                    snapshot
                        .get("fencing_token")
                        .and_then(|token| token.as_u64())
                })
                .unwrap_or_default();
            if current_epoch > assignment_epoch {
                return Ok(false);
            }
            let revision = current.as_ref().map_or(0, |entry| entry.revision);
            let applied = if revision == 0 {
                self.health.create(key, value.clone()).await.is_ok()
            } else {
                self.health
                    .update(key, value.clone(), revision)
                    .await
                    .is_ok()
            };
            if applied {
                return Ok(true);
            }
        }
        Err(format!("Zone health CAS contention for {key}"))
    }

    pub(crate) async fn coordination_get(&self, key: &str) -> Result<Option<Bytes>, String> {
        self.coordination
            .get(key)
            .await
            .map_err(|error| format!("read Zone coordination {key}: {error}"))
    }

    pub(crate) async fn assignment_is_current(
        &self,
        unit_key: &str,
        assignment_epoch: u64,
    ) -> Result<bool, String> {
        let Some(value) = self
            .assignments
            .get(unit_key)
            .await
            .map_err(|error| format!("read Zone Control assignment {unit_key}: {error}"))?
        else {
            return Ok(false);
        };
        let assignment: AssignmentRecord = serde_json::from_slice(&value)
            .map_err(|error| format!("decode Zone Control assignment {unit_key}: {error}"))?;
        Ok(assignment.assignment_epoch == assignment_epoch
            && assignment.expires_at_unix_ms > unix_time_millis())
    }

    pub(crate) async fn coordination_put_fenced(
        &self,
        key: &str,
        value: Bytes,
        assignment_epoch: u64,
    ) -> Result<bool, String> {
        for _ in 0..5 {
            let current = self
                .coordination
                .entry(key.to_string())
                .await
                .map_err(|error| format!("read Zone coordination revision {key}: {error}"))?;
            let current_epoch = current
                .as_ref()
                .and_then(|entry| serde_json::from_slice::<serde_json::Value>(&entry.value).ok())
                .and_then(|snapshot| {
                    snapshot
                        .get("assignment_epoch")
                        .and_then(|token| token.as_u64())
                })
                .unwrap_or_default();
            if current_epoch > assignment_epoch {
                return Ok(false);
            }
            let applied = match current {
                Some(entry) => self
                    .coordination
                    .update(key, value.clone(), entry.revision)
                    .await
                    .is_ok(),
                None => self.coordination.create(key, value.clone()).await.is_ok(),
            };
            if applied {
                return Ok(true);
            }
        }
        Err(format!("Zone coordination CAS contention for {key}"))
    }
}

#[derive(Deserialize)]
struct AssignmentRecord {
    assignment_epoch: u64,
    expires_at_unix_ms: i64,
}

async fn get_or_create_store(
    js: &jetstream::Context,
    config: &Config,
    bucket: &str,
    description: &str,
    max_age: Duration,
    max_value_size: usize,
) -> Result<kv::Store, String> {
    match js.get_key_value(bucket).await {
        Ok(store) => Ok(store),
        Err(_) => match js
            .create_key_value(kv::Config {
                bucket: bucket.to_string(),
                description: description.to_string(),
                history: 1,
                max_age,
                max_value_size: i32::try_from(max_value_size)
                    .map_err(|_| format!("{bucket} max value size is too large"))?,
                storage: StorageType::File,
                num_replicas: config.required_replicas,
                ..Default::default()
            })
            .await
        {
            Ok(store) => Ok(store),
            Err(create_error) => js.get_key_value(bucket).await.map_err(|get_error| {
                format!("create {bucket} KV: {create_error}; reopen failed: {get_error}")
            }),
        },
    }
}

fn unix_time_seconds() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs())
        .unwrap_or_default()
}

fn unix_time_millis() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_millis().min(i64::MAX as u128) as i64)
        .unwrap_or_default()
}
