use super::ownership::ownership_proto;
use super::redis::SharedStreamPublisher;
use crate::config::Config;
use crate::observability::logger::{LogFields, Logger};
use chrono::{DateTime, Utc};
use prost::Message;
use std::sync::Arc;
use std::time::Duration;
use tokio_postgres::{Client, Row};
use uuid::Uuid;

pub mod allocation_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.hypervisor.billing.v1.rs"));
}

const OWNERSHIP_NAMESPACE: Uuid = Uuid::from_u128(0x62f24311_18e7_58d0_b035_27c2a0adcd91);
const ALLOCATION_NAMESPACE: Uuid = Uuid::from_u128(0xd5968c18_0f76_5ea5_8206_aca7f76cc319);

struct HypervisorAllocationExport {
    source_job_id: Uuid,
    ownership_event_id: Uuid,
    ownership_event_type: &'static str,
    ownership_payload: Vec<u8>,
    allocation_event_id: Uuid,
    allocation_event_type: String,
    allocation_payload: Vec<u8>,
}

struct HypervisorAllocationSource {
    source_job_id: Uuid,
    event_type: String,
    resource_id: Uuid,
    owner_id: Uuid,
    resource_name: String,
    zone_id: Uuid,
    effective_at: DateTime<Utc>,
    source_version: i64,
    cpu_cores: i64,
    memory_mib: i64,
    disk_gib: i64,
}

pub struct HypervisorAllocationRelay {
    config: Config,
    publisher: Arc<SharedStreamPublisher>,
    worker_id: String,
}

impl HypervisorAllocationRelay {
    pub fn new(config: Config, publisher: Arc<SharedStreamPublisher>) -> Self {
        let host = crate::config::get_node_hostname();
        Self {
            config,
            publisher,
            worker_id: format!("{host}-hypervisor-allocation-{}", Uuid::new_v4()),
        }
    }

    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        loop {
            match self.run_session().await {
                Ok(()) => return Err("Hypervisor allocation relay PostgreSQL session ended".into()),
                Err(error) => {
                    Logger::sys_error(
                        "hypervisor.allocation.relay",
                        "Hypervisor allocation relay session failed; reconnecting",
                        &error.to_string(),
                    );
                    tokio::time::sleep(Duration::from_secs(2 + self.worker_jitter_secs())).await;
                }
            }
        }
    }

    async fn run_session(&self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let client =
            crate::infra::postgres::connect(&self.config.postgres, "hypervisor.billing.postgres")
                .await?;
        loop {
            let claimed = claim_pending(
                &client,
                &self.worker_id,
                self.config.workflows.ownership.reconcile_batch_size,
                self.config.workflows.ownership.lease_secs,
            )
            .await?;
            for job_id in &claimed {
                if let Err(error) = publish_for_job(&client, &self.publisher, *job_id).await {
                    record_failure(&client, *job_id, &error.to_string()).await;
                }
            }
            let delay =
                if claimed.len() == self.config.workflows.ownership.reconcile_batch_size as usize {
                    Duration::from_millis(25)
                } else {
                    Duration::from_secs(
                        self.config.workflows.ownership.reconcile_interval_secs
                            + self.worker_jitter_secs(),
                    )
                };
            tokio::time::sleep(delay).await;
        }
    }

    fn worker_jitter_secs(&self) -> u64 {
        self.worker_id
            .bytes()
            .fold(0_u64, |sum, byte| sum.wrapping_add(u64::from(byte)))
            % 5
    }
}

async fn claim_pending(
    client: &Client,
    worker_id: &str,
    batch_size: i64,
    lease_secs: u64,
) -> Result<Vec<Uuid>, tokio_postgres::Error> {
    let lease_secs = i64::try_from(lease_secs).unwrap_or(30);
    let rows = client
        .query(
            "WITH candidates AS ( \
                 SELECT candidate.id FROM hypervisor.hypervisor_allocation_outbox candidate \
                 WHERE candidate.published_at IS NULL \
                   AND (candidate.locked_until IS NULL OR candidate.locked_until < NOW()) \
                   AND NOT EXISTS ( \
                       SELECT 1 FROM hypervisor.hypervisor_allocation_outbox predecessor \
                       WHERE predecessor.resource_id = candidate.resource_id \
                         AND predecessor.published_at IS NULL \
                         AND predecessor.source_version < candidate.source_version \
                   ) \
                 ORDER BY candidate.effective_at ASC, candidate.id ASC \
                 FOR UPDATE SKIP LOCKED LIMIT $1 \
             ) \
             UPDATE hypervisor.hypervisor_allocation_outbox o \
             SET locked_by = $2, locked_until = NOW() + ($3::bigint * INTERVAL '1 second'), \
                 attempt_count = attempt_count + 1, updated_at = NOW() \
             FROM candidates c WHERE o.id = c.id \
             RETURNING o.source_job_id",
            &[&batch_size, &worker_id, &lease_secs],
        )
        .await?;
    Ok(rows.into_iter().map(|row| row.get(0)).collect())
}

async fn publish_for_job(
    client: &Client,
    publisher: &SharedStreamPublisher,
    job_id: Uuid,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let row = client
        .query_opt(
            "SELECT source_job_id, event_type, resource_id, owner_id, resource_name, zone_id, \
                    effective_at, source_version, cpu_cores, memory_mib, disk_gib \
             FROM hypervisor.hypervisor_allocation_outbox \
             WHERE source_job_id=$1 AND published_at IS NULL",
            &[&job_id],
        )
        .await?;
    let Some(row) = row else {
        return Ok(());
    };
    let intent = build_intent(&row)?;
    publisher
        .publish_ownership(
            &intent.ownership_event_id.to_string(),
            intent.ownership_event_type,
            &intent.ownership_payload,
        )
        .await?;
    publisher
        .publish_hypervisor_allocation(
            &intent.allocation_event_id.to_string(),
            &intent.allocation_event_type,
            &intent.allocation_payload,
        )
        .await?;
    client
        .execute(
            "UPDATE hypervisor.hypervisor_allocation_outbox \
             SET published_at=COALESCE(published_at,NOW()), locked_by=NULL, locked_until=NULL, \
                 last_error=NULL, updated_at=NOW() \
             WHERE source_job_id=$1 AND published_at IS NULL",
            &[&intent.source_job_id],
        )
        .await?;
    Logger::sys_info_with_fields(
        "hypervisor.allocation.publish",
        "HYPERVISOR_ALLOCATION_EXPORTED",
        "Hypervisor ownership and allocation facts passed the Shared Redis durability fence",
        LogFields {
            operation_id: Some(&intent.source_job_id.to_string()),
            outcome: Some("enqueued"),
            ..LogFields::default()
        },
    );
    Ok(())
}

fn build_intent(
    row: &Row,
) -> Result<HypervisorAllocationExport, Box<dyn std::error::Error + Send + Sync>> {
    build_intent_from_source(HypervisorAllocationSource {
        source_job_id: row.get(0),
        event_type: row.get(1),
        resource_id: row.get(2),
        owner_id: row.get(3),
        resource_name: row.get(4),
        zone_id: row.get(5),
        effective_at: row.get(6),
        source_version: row.get(7),
        cpu_cores: row.get(8),
        memory_mib: row.get(9),
        disk_gib: row.get(10),
    })
}

fn build_intent_from_source(
    source: HypervisorAllocationSource,
) -> Result<HypervisorAllocationExport, Box<dyn std::error::Error + Send + Sync>> {
    let HypervisorAllocationSource {
        source_job_id,
        event_type,
        resource_id,
        owner_id,
        resource_name,
        zone_id,
        effective_at,
        source_version,
        cpu_cores,
        memory_mib,
        disk_gib,
    } = source;
    if source_version <= 0 {
        return Err("Hypervisor allocation export has invalid source_version".into());
    }
    let (ownership_event_type, cpu_cores, memory_mib, disk_gib) = match event_type.as_str() {
        "ACTIVATE" => (
            "RESOURCE_CREATED",
            u64::try_from(cpu_cores)?,
            u64::try_from(memory_mib)?,
            u64::try_from(disk_gib)?,
        ),
        "REVISE" => (
            "RESOURCE_UPDATED",
            u64::try_from(cpu_cores)?,
            u64::try_from(memory_mib)?,
            u64::try_from(disk_gib)?,
        ),
        "TERMINATE" => ("RESOURCE_DELETED", 0, 0, 0),
        _ => return Err("unsupported Hypervisor allocation event type".into()),
    };
    if (event_type == "ACTIVATE" && source_version != 1)
        || (event_type != "ACTIVATE" && source_version <= 1)
    {
        return Err("Hypervisor allocation source version does not match lifecycle event".into());
    }
    if resource_id.is_nil()
        || owner_id.is_nil()
        || zone_id.is_nil()
        || resource_name.trim().is_empty()
    {
        return Err("Hypervisor billing source identity is invalid".into());
    }

    let ownership_event_id = Uuid::new_v5(
        &OWNERSHIP_NAMESPACE,
        format!("{}:{ownership_event_type}", source_job_id).as_bytes(),
    );
    let ownership = ownership_proto::ResourceOwnershipChangedV1 {
        event_id: ownership_event_id.as_bytes().to_vec(),
        event_type: ownership_event_type.to_string(),
        schema_version: 1,
        resource_id: resource_id.as_bytes().to_vec(),
        resource_type: "HYPERVISOR_VM".to_string(),
        resource_name,
        owner_id: owner_id.as_bytes().to_vec(),
        owner_type: "PERSONAL".to_string(),
        zone_id: zone_id.as_bytes().to_vec(),
        source_version,
        effective_at: effective_at.to_rfc3339(),
        source_job_id: source_job_id.as_bytes().to_vec(),
        traceparent: String::new(),
    };
    let mut ownership_payload = Vec::with_capacity(ownership.encoded_len());
    ownership.encode(&mut ownership_payload)?;

    let allocation_event_id = Uuid::new_v5(
        &ALLOCATION_NAMESPACE,
        format!("{}:{event_type}:{source_version}", source_job_id).as_bytes(),
    );
    let allocation = allocation_proto::HypervisorAllocationChangedV1 {
        schema_version: 1,
        event_id: allocation_event_id.as_bytes().to_vec(),
        event_type: event_type.clone(),
        resource_id: resource_id.as_bytes().to_vec(),
        zone_id: zone_id.as_bytes().to_vec(),
        source_version: u64::try_from(source_version)?,
        effective_at_unix_ms: effective_at.timestamp_millis(),
        cpu_cores,
        memory_mib,
        disk_gib,
        gpu_sku: String::new(),
        gpu_count: 0,
        source_job_id: source_job_id.as_bytes().to_vec(),
    };
    let mut allocation_payload = Vec::with_capacity(allocation.encoded_len());
    allocation.encode(&mut allocation_payload)?;
    Ok(HypervisorAllocationExport {
        source_job_id,
        ownership_event_id,
        ownership_event_type,
        ownership_payload,
        allocation_event_id,
        allocation_event_type: event_type,
        allocation_payload,
    })
}

async fn record_failure(client: &Client, job_id: Uuid, error: &str) {
    let bounded = error.chars().take(512).collect::<String>();
    let _ = client
        .execute(
            "UPDATE hypervisor.hypervisor_allocation_outbox \
             SET last_error=$2, locked_by=NULL, locked_until=NULL, updated_at=NOW() \
             WHERE source_job_id=$1 AND published_at IS NULL",
            &[&job_id, &bounded],
        )
        .await;
}

#[cfg(test)]
mod tests {
    use super::{allocation_proto, build_intent_from_source, HypervisorAllocationSource};
    use chrono::Utc;
    use prost::Message;
    use uuid::Uuid;

    fn source(event_type: &str, source_version: i64) -> HypervisorAllocationSource {
        HypervisorAllocationSource {
            source_job_id: Uuid::new_v4(),
            event_type: event_type.to_string(),
            resource_id: Uuid::new_v4(),
            owner_id: Uuid::new_v4(),
            resource_name: "vm-a".to_string(),
            zone_id: Uuid::new_v4(),
            effective_at: Utc::now(),
            source_version,
            cpu_cores: 2,
            memory_mib: 4096,
            disk_gib: 64,
        }
    }

    #[test]
    fn create_uses_the_durable_first_lifecycle_version() {
        let intent = build_intent_from_source(source("ACTIVATE", 1)).unwrap();
        let allocation = allocation_proto::HypervisorAllocationChangedV1::decode(
            intent.allocation_payload.as_slice(),
        )
        .unwrap();
        assert_eq!(allocation.event_type, "ACTIVATE");
        assert_eq!(allocation.source_version, 1);
        assert_eq!(allocation.cpu_cores, 2);
    }

    #[test]
    fn delete_preserves_a_future_monotonic_lifecycle_version() {
        let intent = build_intent_from_source(source("TERMINATE", 7)).unwrap();
        let allocation = allocation_proto::HypervisorAllocationChangedV1::decode(
            intent.allocation_payload.as_slice(),
        )
        .unwrap();
        assert_eq!(allocation.event_type, "TERMINATE");
        assert_eq!(allocation.source_version, 7);
        assert_eq!(allocation.cpu_cores, 0);
    }

    #[test]
    fn lifecycle_topic_rejects_a_non_monotonic_source_version() {
        assert!(build_intent_from_source(source("ACTIVATE", 2)).is_err());
        assert!(build_intent_from_source(source("TERMINATE", 1)).is_err());
    }
}
