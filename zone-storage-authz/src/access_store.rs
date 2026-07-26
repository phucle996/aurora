use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use async_nats::jetstream;
use futures_util::StreamExt;
use moka::future::Cache;
use serde::Deserialize;

use crate::config::Config;
use crate::error::AuthzError;

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

#[derive(Clone)]
pub struct AccessStore {
    store: jetstream::kv::Store,
    cache: Cache<String, Arc<AccessRecord>>,
}

impl AccessStore {
    pub async fn connect(config: &Config) -> Result<Self, AuthzError> {
        let mut options = async_nats::ConnectOptions::new();
        if let Some(ca) = &config.nats_ca {
            options = options.add_root_certificates(ca.clone()).require_tls(true);
        }
        match (&config.nats_cert, &config.nats_key) {
            (Some(cert), Some(key)) => {
                options = options.add_client_certificate(cert.clone(), key.clone())
            }
            (None, None) => {}
            _ => {
                return Err(AuthzError::Configuration(
                    "NATS Zone client cert and key must be configured together".into(),
                ))
            }
        }
        if let Some(creds) = &config.nats_creds {
            options = options
                .credentials_file(PathBuf::from(creds))
                .await
                .map_err(|error| {
                    AuthzError::Configuration(format!("read NATS credentials failed: {error}"))
                })?;
        }
        let client = options
            .connect(&config.nats_zone_url)
            .await
            .map_err(|error| {
                AuthzError::Dependency(format!("connect Zone NATS failed: {error}"))
            })?;
        let store = jetstream::new(client)
            .get_key_value("AURORA_ZONE_ACCESS")
            .await
            .map_err(|error| {
                AuthzError::Dependency(format!("open Zone access KV failed: {error}"))
            })?;
        let status = store.status().await.map_err(|error| {
            AuthzError::Dependency(format!("read Zone access KV status failed: {error}"))
        })?;
        if status.history() != 1
            || status.info.config.storage != jetstream::stream::StorageType::File
        {
            return Err(AuthzError::Dependency(
                "Zone access KV durability contract mismatch".into(),
            ));
        }
        let result = Self {
            store,
            cache: Cache::builder()
                .max_capacity(config.access_cache_capacity)
                .time_to_live(config.access_cache_ttl)
                .build(),
        };
        result.start_watch();
        Ok(result)
    }

    pub async fn get(&self, id: &str) -> Result<Option<Arc<AccessRecord>>, AuthzError> {
        if let Some(record) = self.cache.get(id).await {
            return Ok(Some(record));
        }
        let Some(value) = self
            .store
            .get(id.to_string())
            .await
            .map_err(|_| AuthzError::Dependency("Zone access KV read failed".into()))?
        else {
            return Ok(None);
        };
        let record: Arc<AccessRecord> = Arc::new(
            serde_json::from_slice(&value)
                .map_err(|_| AuthzError::Dependency("Zone access record is corrupt".into()))?,
        );
        self.cache.insert(id.to_string(), record.clone()).await;
        Ok(Some(record))
    }

    fn start_watch(&self) {
        let store = self.store.clone();
        let cache = self.cache.clone();
        tokio::spawn(async move {
            loop {
                match store.watch_all().await {
                    Ok(mut watch) => {
                        while let Some(entry) = watch.next().await {
                            match entry {
                                Ok(entry) => {
                                    if let Ok(record) =
                                        serde_json::from_slice::<AccessRecord>(&entry.value)
                                    {
                                        cache.insert(entry.key, Arc::new(record)).await;
                                    } else {
                                        cache.invalidate(&entry.key).await;
                                    }
                                }
                                Err(_) => break,
                            }
                        }
                    }
                    Err(_) => {}
                }
                tokio::time::sleep(Duration::from_secs(1)).await;
            }
        });
    }
}
