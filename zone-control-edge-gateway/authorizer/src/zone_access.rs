use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use async_nats::jetstream;
use futures_util::StreamExt;
use moka::future::Cache;
use serde::Deserialize;
use tokio::sync::watch;
use tokio::task::JoinHandle;

use crate::config::Config;
use crate::error::AuthzError;
use crate::telemetry::Telemetry;

#[derive(Clone, Debug, Deserialize)]
pub struct AccessRecord {
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

#[derive(Clone, Debug, Deserialize)]
pub struct AdmissionRecord {
    pub policy_version: i64,
    pub decision: String,
    pub effective_at_unix_seconds: i64,
    pub valid_until_unix_seconds: Option<i64>,
}

#[derive(Clone)]
pub struct AccessStore {
    store: jetstream::kv::Store,
    admission: jetstream::kv::Store,
    cache: Cache<String, Arc<AccessRecord>>,
    read_timeout: Duration,
    telemetry: Arc<Telemetry>,
}

impl AccessStore {
    pub async fn connect(config: &Config, telemetry: Arc<Telemetry>) -> Result<Self, AuthzError> {
        let options = async_nats::ConnectOptions::new()
            .add_root_certificates(config.nats_ca.clone())
            .require_tls(true)
            .add_client_certificate(config.nats_cert.clone(), config.nats_key.clone())
            .credentials_file(PathBuf::from(&config.nats_creds))
            .await
            .map_err(|error| {
                AuthzError::Configuration(format!("read NATS credentials failed: {error}"))
            })?;
        let client = tokio::time::timeout(
            config.nats_connect_timeout,
            options.connect(&config.nats_zone_url),
        )
        .await
        .map_err(|_| AuthzError::Dependency("connect Zone NATS timed out".into()))?
        .map_err(|error| AuthzError::Dependency(format!("connect Zone NATS failed: {error}")))?;
        let js = jetstream::new(client);
        let store = tokio::time::timeout(
            config.nats_request_timeout,
            js.get_key_value("AURORA_ZONE_ACCESS"),
        )
        .await
        .map_err(|_| AuthzError::Dependency("open Zone access KV timed out".into()))?
        .map_err(|error| AuthzError::Dependency(format!("open Zone access KV failed: {error}")))?;
        let status = tokio::time::timeout(config.nats_request_timeout, store.status())
            .await
            .map_err(|_| AuthzError::Dependency("read Zone access KV status timed out".into()))?
            .map_err(|error| {
                AuthzError::Dependency(format!("read Zone access KV status failed: {error}"))
            })?;
        if status.history() != 1
            || status.info.config.storage != jetstream::stream::StorageType::File
            || status.info.config.num_replicas < config.access_required_replicas
        {
            return Err(AuthzError::Dependency(
                "Zone access KV durability contract mismatch".into(),
            ));
        }
        let admission = tokio::time::timeout(
            config.nats_request_timeout,
            js.get_key_value("AURORA_ZONE_ADMISSION"),
        )
        .await
        .map_err(|_| AuthzError::Dependency("open Zone admission KV timed out".into()))?
        .map_err(|error| {
            AuthzError::Dependency(format!("open Zone admission KV failed: {error}"))
        })?;
        let result = Self {
            store,
            admission,
            cache: Cache::builder()
                .max_capacity(config.access_cache_capacity)
                .time_to_live(config.access_cache_ttl)
                .build(),
            read_timeout: config.access_read_timeout,
            telemetry,
        };
        Ok(result)
    }

    pub async fn require_billable_admission(&self, resource_id: &str) -> Result<(), AuthzError> {
        let entry = tokio::time::timeout(
            self.read_timeout,
            self.admission.entry(resource_id.to_string()),
        )
        .await
        .map_err(|_| AuthzError::Dependency("Zone admission KV read timed out".into()))?
        .map_err(|_| AuthzError::Dependency("Zone admission KV read failed".into()))?;
        let Some(entry) = entry else {
            return Err(AuthzError::Denied("STORAGE_COMMERCIAL_ADMISSION_MISSING"));
        };
        let record: AdmissionRecord = serde_json::from_slice(&entry.value)
            .map_err(|_| AuthzError::Dependency("Zone admission record is corrupt".into()))?;
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map_err(|_| AuthzError::Dependency("system clock invalid".into()))?
            .as_secs() as i64;
        if record.policy_version <= 0
            || record.decision != "ALLOW"
            || record.effective_at_unix_seconds > now
            || record
                .valid_until_unix_seconds
                .is_some_and(|until| until <= now)
        {
            return Err(AuthzError::Denied("STORAGE_COMMERCIAL_ADMISSION_SUSPENDED"));
        }
        Ok(())
    }

    pub async fn get(&self, id: &str) -> Result<Option<Arc<AccessRecord>>, AuthzError> {
        if let Some(cached) = self.cache.get(id).await {
            self.telemetry.cache_hit();
            return Ok(Some(cached));
        }
        self.telemetry.cache_miss();
        let entry = tokio::time::timeout(self.read_timeout, self.store.entry(id.to_string()))
            .await
            .map_err(|_| {
                self.telemetry.kv_read_failure();
                AuthzError::Dependency("Zone access KV read timed out".into())
            })?
            .map_err(|_| {
                self.telemetry.kv_read_failure();
                AuthzError::Dependency("Zone access KV read failed".into())
            })?;
        let Some(entry) = entry else {
            return Ok(None);
        };
        let record: Arc<AccessRecord> =
            Arc::new(serde_json::from_slice(&entry.value).map_err(|_| {
                self.telemetry.kv_read_failure();
                AuthzError::Dependency("Zone access record is corrupt".into())
            })?);
        // Only the ordered watch stream hydrates L1. A direct read can race
        // with a newer watch event and must never resurrect an older record.
        Ok(Some(record))
    }

    pub fn start_watch(&self, mut shutdown: watch::Receiver<bool>) -> JoinHandle<()> {
        let store = self.store.clone();
        let cache = self.cache.clone();
        let telemetry = self.telemetry.clone();
        tokio::spawn(async move {
            let mut retry = 0_u32;
            // Per-process entropy prevents all replicas reconnecting to the KV
            // leader on the same millisecond after a Zone-wide interruption.
            let jitter_seed = uuid::Uuid::new_v4().as_u128() as u64;
            loop {
                if *shutdown.borrow() {
                    telemetry.set_ready(false);
                    return;
                }
                if let Ok(mut watch) = store.watch_all().await {
                    retry = 0;
                    telemetry.set_ready(true);
                    loop {
                        tokio::select! {
                            changed = shutdown.changed() => {
                                if changed.is_err() || *shutdown.borrow() {
                                    telemetry.set_ready(false);
                                    return;
                                }
                            }
                            entry = watch.next() => {
                                match entry {
                                    Some(Ok(entry)) => {
                                        if let Ok(record) =
                                            serde_json::from_slice::<AccessRecord>(&entry.value)
                                        {
                                            cache.insert(entry.key, Arc::new(record)).await;
                                        } else {
                                            telemetry.kv_read_failure();
                                            tracing::warn!(
                                                event_code = "ZONE_ACCESS_WATCH_RECORD_CORRUPT"
                                            );
                                            cache.invalidate(&entry.key).await;
                                        }
                                    }
                                    Some(Err(error)) => {
                                        tracing::warn!(
                                            event_code = "ZONE_ACCESS_WATCH_STREAM_FAILED",
                                            error = %error
                                        );
                                        break;
                                    }
                                    None => break,
                                }
                            }
                        }
                    }
                } else {
                    tracing::warn!(event_code = "ZONE_ACCESS_WATCH_OPEN_FAILED");
                }
                telemetry.set_ready(false);
                telemetry.watch_restart();
                retry = retry.saturating_add(1);
                let exponential_ms = 100_u64.saturating_mul(1_u64 << retry.min(6));
                let jitter_ms =
                    (jitter_seed.wrapping_add(u64::from(retry).wrapping_mul(137))) % 251;
                let delay = Duration::from_millis((exponential_ms + jitter_ms).min(5_000));
                tokio::select! {
                    _ = tokio::time::sleep(delay) => {}
                    changed = shutdown.changed() => {
                        if changed.is_err() || *shutdown.borrow() {
                            return;
                        }
                    }
                }
            }
        })
    }
}

#[cfg(test)]
mod tests {
    use super::AdmissionRecord;

    #[test]
    fn decodes_the_storage_owned_admission_schema() {
        let value = serde_json::json!({
            "resource_id": "01990c25-b030-7b50-826a-33bb9553e34b",
            "resource_name": "personal-bucket",
            "policy_version": 7,
            "decision": "ALLOW",
            "restriction_reason": null,
            "effective_at_unix_seconds": 1_700_000_000,
            "valid_until_unix_seconds": null,
            "source_event_id": "01990c25-b030-7b50-826a-33bb9553e34c"
        });

        let record: AdmissionRecord =
            serde_json::from_value(value).expect("canonical admission must decode");
        assert_eq!(record.policy_version, 7);
        assert_eq!(record.decision, "ALLOW");
    }

    #[test]
    fn rejects_the_retired_wallet_field_names() {
        let value = serde_json::json!({
            "wallet_version": 7,
            "admission_mode": "ALLOW",
            "effective_at_unix_seconds": 1_700_000_000,
            "valid_until_unix_seconds": null
        });

        assert!(serde_json::from_value::<AdmissionRecord>(value).is_err());
    }
}
