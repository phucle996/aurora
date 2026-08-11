use std::{path::PathBuf, time::Duration};

use async_nats::jetstream::{self, kv, stream::StorageType};
use bytes::Bytes;
use zone_transfer_contract::{TransferTicketState, TransferTicketV1};

use crate::config::Config;

#[derive(Clone)]
pub struct TicketStore {
    store: jetstream::kv::Store,
    timeout: Duration,
}

impl TicketStore {
    pub async fn connect(config: &Config) -> Result<Self, String> {
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
        let store = match tokio::time::timeout(
            config.nats_timeout,
            js.get_key_value("AURORA_ZONE_TRANSFER"),
        )
        .await
        {
            Ok(Ok(store)) => store,
            Ok(Err(_)) => js
                .create_key_value(kv::Config {
                    bucket: "AURORA_ZONE_TRANSFER".to_string(),
                    description: "Aurora one-time Zone transfer tickets".to_string(),
                    history: 1,
                    max_age: Duration::from_secs(300),
                    max_value_size: 32 * 1024,
                    storage: StorageType::File,
                    num_replicas: config.required_replicas,
                    ..Default::default()
                })
                .await
                .map_err(|error| format!("create Zone transfer KV: {error}"))?,
            Err(_) => return Err("open Zone transfer KV timed out".to_string()),
        };
        let status = store
            .status()
            .await
            .map_err(|error| format!("read Zone transfer KV status: {error}"))?;
        if status.history() != 1
            || status.info.config.storage != StorageType::File
            || status.info.config.num_replicas < config.required_replicas
        {
            return Err("Zone transfer KV durability contract mismatch".to_string());
        }
        Ok(Self {
            store,
            timeout: config.nats_timeout,
        })
    }

    pub async fn create(&self, ticket: &TransferTicketV1) -> Result<(), String> {
        let value = Bytes::from(
            serde_json::to_vec(ticket).map_err(|error| format!("encode ticket: {error}"))?,
        );
        tokio::time::timeout(self.timeout, self.store.update(&ticket.ticket_id, value, 0))
            .await
            .map_err(|_| "create transfer ticket timed out".to_string())?
            .map_err(|error| format!("create transfer ticket: {error}"))?;
        Ok(())
    }

    pub async fn revoke(
        &self,
        ticket_id: &str,
        grant: &zone_transfer_contract::TransferGrantV1,
    ) -> Result<bool, String> {
        for _ in 0..5 {
            let entry = tokio::time::timeout(self.timeout, self.store.entry(ticket_id.to_string()))
                .await
                .map_err(|_| "read transfer ticket timed out".to_string())?
                .map_err(|error| format!("read transfer ticket: {error}"))?;
            let Some(entry) = entry else { return Ok(false) };
            let mut ticket: TransferTicketV1 = serde_json::from_slice(&entry.value)
                .map_err(|_| "transfer ticket is corrupt".to_string())?;
            if ticket.actor_id != grant.actor_id
                || ticket.zone_id != grant.zone_id
                || ticket.resource_id != grant.resource_id
                || ticket.workspace_id != grant.workspace_id
                || ticket.capability != grant.capability
            {
                return Ok(false);
            }
            if ticket.state == TransferTicketState::Revoked {
                return Ok(true);
            }
            if ticket.state != TransferTicketState::Issued {
                return Ok(false);
            }
            ticket.state = TransferTicketState::Revoked;
            let value = Bytes::from(
                serde_json::to_vec(&ticket)
                    .map_err(|error| format!("encode revoked ticket: {error}"))?,
            );
            if self
                .store
                .update(ticket_id, value, entry.revision)
                .await
                .is_ok()
            {
                return Ok(true);
            }
        }
        Err("transfer ticket revoke CAS contention".to_string())
    }
}
