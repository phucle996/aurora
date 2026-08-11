use std::{
    path::PathBuf,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use async_nats::jetstream::{self, kv, stream::StorageType};
use bytes::Bytes;
use serde::{Deserialize, Serialize};
use tokio_util::sync::CancellationToken;

use crate::transfer_ticket::config::Config;

/// Zone Control owns Zone-wide coordination. Ticket issuance is a separate
/// workflow context and does not receive this lease or any orchestrator state.
pub fn start(config: Config, shutdown: CancellationToken) {
    tokio::spawn(async move {
        let owner_id = format!("zone-control:{}", uuid::Uuid::new_v4());
        let coordinator = match ZoneCoordinator::connect(&config).await {
            Ok(coordinator) => coordinator,
            Err(error) => {
                tracing::error!(event_code = "ZONE_CONTROL_COORDINATION_UNAVAILABLE", zone_id = %config.zone_id, error = %error);
                return;
            }
        };
        tracing::info!(
            event_code = "ZONE_CONTROL_ORCHESTRATOR_STARTED",
            zone_id = %config.zone_id,
            owner_id = %owner_id,
            responsibilities = "zone_coordination,metadata_projection,health_aggregation,report_publish"
        );
        while !shutdown.is_cancelled() {
            let Some(lease) = coordinator.acquire(&owner_id).await else {
                tokio::time::sleep(Duration::from_secs(2)).await;
                continue;
            };
            tracing::info!(event_code = "ZONE_CONTROL_LEADER_ELECTED", zone_id = %config.zone_id, owner_id = %owner_id, fencing_token = lease.fencing_token);
            let mut renew = tokio::time::interval(Duration::from_secs(5));
            renew.tick().await;
            loop {
                tokio::select! {
                    _ = shutdown.cancelled() => break,
                    _ = renew.tick() => {
                        if !coordinator.renew(&lease).await { break; }
                        tracing::debug!(event_code = "ZONE_CONTROL_ORCHESTRATOR_TICK", zone_id = %config.zone_id, owner_id = %owner_id, fencing_token = lease.fencing_token);
                    }
                }
            }
            let _ = coordinator.release(&lease).await;
        }
        tracing::info!(event_code = "ZONE_CONTROL_ORCHESTRATOR_STOPPED", zone_id = %config.zone_id, owner_id = %owner_id);
    });
}

const LEASE_KEY: &str = "lease.zone.leader";
const LEASE_TTL_SECONDS: u64 = 15;

#[derive(Clone, Debug, Deserialize, Serialize)]
struct LeaseValue {
    owner_id: String,
    fencing_token: u64,
    expires_at_unix_seconds: u64,
}

#[derive(Clone, Debug)]
struct Lease {
    owner_id: String,
    fencing_token: u64,
}

struct ZoneCoordinator {
    store: kv::Store,
    timeout: Duration,
}

impl ZoneCoordinator {
    async fn connect(config: &Config) -> Result<Self, String> {
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
        let store = match js.get_key_value("AURORA_ZONE_COORDINATION").await {
            Ok(store) => store,
            Err(_) => js
                .create_key_value(kv::Config {
                    bucket: "AURORA_ZONE_COORDINATION".to_string(),
                    description: "Fenced Zone Control coordination leases".to_string(),
                    history: 1,
                    max_age: Duration::from_secs(86_400),
                    max_value_size: 64 * 1024,
                    storage: StorageType::File,
                    num_replicas: config.required_replicas,
                    ..Default::default()
                })
                .await
                .map_err(|error| format!("create Zone coordination KV: {error}"))?,
        };
        Ok(Self {
            store,
            timeout: config.nats_timeout,
        })
    }

    async fn acquire(&self, owner_id: &str) -> Option<Lease> {
        let now = SystemTime::now().duration_since(UNIX_EPOCH).ok()?.as_secs();
        let entry = tokio::time::timeout(self.timeout, self.store.entry(LEASE_KEY.to_string()))
            .await
            .ok()?
            .ok()?;
        let (expected_revision, fencing_token) = match entry {
            Some(entry) => {
                let value: LeaseValue = serde_json::from_slice(&entry.value).ok()?;
                if value.expires_at_unix_seconds > now && value.owner_id != owner_id {
                    return None;
                }
                (entry.revision, value.fencing_token.saturating_add(1))
            }
            None => (0, 1),
        };
        let value = Bytes::from(
            serde_json::to_vec(&LeaseValue {
                owner_id: owner_id.to_string(),
                fencing_token,
                expires_at_unix_seconds: now.saturating_add(LEASE_TTL_SECONDS),
            })
            .ok()?,
        );
        if expected_revision == 0 {
            self.store.create(LEASE_KEY, value).await.ok()?
        } else {
            self.store
                .update(LEASE_KEY, value, expected_revision)
                .await
                .ok()?
        };
        Some(Lease {
            owner_id: owner_id.to_string(),
            fencing_token,
        })
    }

    async fn renew(&self, lease: &Lease) -> bool {
        let now = match SystemTime::now().duration_since(UNIX_EPOCH) {
            Ok(value) => value.as_secs(),
            Err(_) => return false,
        };
        let entry =
            match tokio::time::timeout(self.timeout, self.store.entry(LEASE_KEY.to_string())).await
            {
                Ok(Ok(Some(entry))) => entry,
                _ => return false,
            };
        let current: LeaseValue = match serde_json::from_slice(&entry.value) {
            Ok(value) => value,
            Err(_) => return false,
        };
        if current.owner_id != lease.owner_id || current.fencing_token != lease.fencing_token {
            return false;
        }
        self.store
            .update(
                LEASE_KEY,
                Bytes::from(
                    serde_json::to_vec(&LeaseValue {
                        owner_id: lease.owner_id.clone(),
                        fencing_token: lease.fencing_token,
                        expires_at_unix_seconds: now.saturating_add(LEASE_TTL_SECONDS),
                    })
                    .unwrap_or_default(),
                ),
                entry.revision,
            )
            .await
            .is_ok()
    }

    async fn release(&self, lease: &Lease) -> bool {
        let entry = match self.store.entry(LEASE_KEY.to_string()).await {
            Ok(Some(entry)) => entry,
            _ => return false,
        };
        let current: LeaseValue = match serde_json::from_slice(&entry.value) {
            Ok(value) => value,
            Err(_) => return false,
        };
        if current.owner_id != lease.owner_id || current.fencing_token != lease.fencing_token {
            return false;
        }
        self.store.delete(LEASE_KEY).await.is_ok()
    }
}
