use super::redis::SharedStreamPublisher;
use crate::config::Config;
use crate::observability::logger::{LogFields, Logger};
use chrono::{DateTime, Utc};
use prost::Message;
use std::sync::Arc;
use std::time::Duration;
use tokio_postgres::{Client, Row};
use uuid::Uuid;

pub mod ownership_proto {
    include!(concat!(env!("OUT_DIR"), "/billing_ownership.rs"));
}

const OWNERSHIP_NAMESPACE: Uuid = Uuid::from_bytes([
    0x6b, 0x18, 0x4e, 0x2a, 0x9f, 0x3c, 0x5d, 0x71, 0x8a, 0x2b, 0x1c, 0x4f, 0x6e, 0x7d, 0x0e, 0x3f,
]);
const OWNERSHIP_TOPICS: [&str; 2] = ["storage.bucket.create", "storage.bucket.delete"];

struct OwnershipIntent {
    event_id: Uuid,
    event_type: &'static str,
    payload: Vec<u8>,
}

pub struct OwnershipRelay {
    config: Config,
    publisher: Arc<SharedStreamPublisher>,
    worker_id: String,
}

impl OwnershipRelay {
    pub fn new(config: Config, publisher: Arc<SharedStreamPublisher>) -> Self {
        let host = crate::config::get_node_hostname();
        Self {
            config,
            publisher,
            worker_id: format!("{host}-{}", Uuid::new_v4()),
        }
    }

    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        // The durable row survives a local wake loss. Startup drain plus a
        // jittered periodic scan provides failover without hot polling.
        loop {
            match self.run_session().await {
                Ok(()) => return Err("ownership relay PostgreSQL session ended".into()),
                Err(error) => {
                    Logger::sys_error(
                        "ownership.relay",
                        "Ownership relay session failed; reconnecting",
                        &error.to_string(),
                    );
                    tokio::time::sleep(Duration::from_secs(2 + self.worker_jitter_secs())).await;
                }
            }
        }
    }

    async fn run_session(&self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let client =
            crate::infra::postgres::connect(&self.config.postgres, "ownership.postgres").await?;

        loop {
            let claimed = claim_pending(
                &client,
                &self.worker_id,
                self.config.workflows.ownership.reconcile_batch_size,
                self.config.workflows.ownership.lease_secs,
            )
            .await?;
            for (job_id, job_topic) in &claimed {
                if let Err(error) =
                    publish_for_job(&client, &self.publisher, *job_id, job_topic).await
                {
                    record_failure(&client, *job_id, job_topic, &error.to_string()).await;
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
        let seed = self
            .worker_id
            .bytes()
            .fold(0_u64, |acc, value| acc.wrapping_add(u64::from(value)));
        seed % 5
    }
}

/// Fast path called after the result transaction commits. Failure is persisted
/// on the same storage outbox row and recovered by OwnershipRelay, so callers
/// may settle the Kafka result without coupling it to a Redis outage.
pub async fn publish_for_job(
    client: &Client,
    publisher: &SharedStreamPublisher,
    job_id: Uuid,
    job_topic: &str,
) -> Result<bool, Box<dyn std::error::Error + Send + Sync>> {
    if !OWNERSHIP_TOPICS.contains(&job_topic) {
        return Ok(false);
    }
    let row = client
        .query_opt(
            "SELECT event_id, job_topic, resource_id, owner_id, owner_type, \
                    resource_name, zone_id, completed_at \
             FROM storage.storage_outbox_records \
             WHERE event_id = $1 AND job_topic = $2 AND status = 'SUCCEEDED' \
               AND ownership_published_at IS NULL",
            &[&job_id, &job_topic],
        )
        .await?;
    let Some(row) = row else {
        return Ok(false);
    };
    let intent = build_intent(&row)?;
    let redis_context = crate::observability::otel::OtelTracer::start_current_span(
        format!("send {}", super::redis::OWNERSHIP_STREAM),
        opentelemetry::trace::SpanKind::Producer,
        vec![
            opentelemetry::KeyValue::new("messaging.system", "redis"),
            opentelemetry::KeyValue::new("messaging.operation.type", "send"),
            opentelemetry::KeyValue::new(
                "messaging.destination.name",
                super::redis::OWNERSHIP_STREAM,
            ),
            opentelemetry::KeyValue::new("messaging.message.id", intent.event_id.to_string()),
        ],
    );
    use opentelemetry::trace::FutureExt;
    let publish_result = publisher
        .publish_ownership(
            &intent.event_id.to_string(),
            intent.event_type,
            &intent.payload,
        )
        .with_context(redis_context.clone())
        .await;
    crate::observability::otel::OtelTracer::finish_span(
        &redis_context,
        publish_result
            .as_ref()
            .err()
            .map(|_| "OWNERSHIP_REDIS_PUBLISH_FAILED"),
    );
    let stream_id = publish_result?;

    client
        .execute(
            "UPDATE storage.storage_outbox_records \
             SET ownership_published_at = COALESCE(ownership_published_at, NOW()), \
                 ownership_last_error = NULL, ownership_locked_by = NULL, ownership_locked_until = NULL \
             WHERE event_id = $1 AND job_topic = $2",
            &[&job_id, &job_topic],
        )
        .await?;
    crate::observability::metrics::MetricsManager::record_ownership_enqueued();
    Logger::sys_info_with_fields(
        "ownership.publish",
        "RESOURCE_OWNERSHIP_ENQUEUED",
        &format!("Ownership event accepted by Shared Redis at entry {stream_id}"),
        LogFields {
            event_id: Some(&intent.event_id.to_string()),
            operation_id: Some(&job_id.to_string()),
            outcome: Some("enqueued"),
            ..LogFields::default()
        },
    );
    Ok(true)
}

async fn claim_pending(
    client: &Client,
    worker_id: &str,
    batch_size: i64,
    lease_secs: u64,
) -> Result<Vec<(Uuid, String)>, tokio_postgres::Error> {
    let lease_secs = i64::try_from(lease_secs).unwrap_or(30);
    // [COMMENT]: Ép kiểu $3::bigint để tokio-postgres xác định đúng OID khi nhân với INTERVAL '1 second'
    let rows = client
        .query(
            "WITH candidates AS ( \
                 SELECT id FROM storage.storage_outbox_records \
                 WHERE status = 'SUCCEEDED' \
                   AND job_topic IN ('storage.bucket.create', 'storage.bucket.delete') \
                   AND ownership_published_at IS NULL \
                   AND (ownership_locked_until IS NULL OR ownership_locked_until < NOW()) \
                 ORDER BY completed_at ASC, id ASC \
                 FOR UPDATE SKIP LOCKED LIMIT $1 \
             ) \
             UPDATE storage.storage_outbox_records o \
             SET ownership_locked_by = $2, \
                 ownership_locked_until = NOW() + ($3::bigint * INTERVAL '1 second'), \
                 ownership_attempt_count = ownership_attempt_count + 1 \
             FROM candidates c WHERE o.id = c.id \
             RETURNING o.event_id, o.job_topic",
            &[&batch_size, &worker_id, &lease_secs],
        )
        .await?;
    Ok(rows
        .into_iter()
        .map(|row| (row.get::<_, Uuid>(0), row.get::<_, String>(1)))
        .collect())
}

fn build_intent(row: &Row) -> Result<OwnershipIntent, Box<dyn std::error::Error + Send + Sync>> {
    let job_id: Uuid = row.get(0);
    let job_topic: String = row.get(1);
    let resource_id = Uuid::parse_str(
        row.get::<_, Option<String>>(2)
            .as_deref()
            .ok_or("ownership source has no resource_id")?,
    )?;
    let owner_id: Uuid = row.get(3);
    let owner_type: String = row.get(4);
    if !matches!(owner_type.as_str(), "PERSONAL" | "TENANT") {
        return Err(format!("unsupported owner_type {owner_type}").into());
    }
    let resource_name: String = row.get(5);
    let zone_id: Uuid = row.get(6);
    if zone_id.is_nil() {
        return Err("ownership source has a nil zone_id".into());
    }
    let completed_at: DateTime<Utc> = row
        .get::<_, Option<DateTime<Utc>>>(7)
        .ok_or("terminal ownership source has no completed_at")?;

    let (event_type, source_version) = match job_topic.as_str() {
        "storage.bucket.create" => ("RESOURCE_CREATED", 1_i64),
        "storage.bucket.delete" => ("RESOURCE_DELETED", 2_i64),
        _ => return Err(format!("unsupported ownership job topic {job_topic}").into()),
    };
    if resource_name.trim().is_empty() {
        return Err("ownership resource_name is empty".into());
    }

    let event_id = ownership_event_id(job_id, event_type);
    let wire = ownership_proto::ResourceOwnershipChangedV1 {
        event_id: event_id.as_bytes().to_vec(),
        event_type: event_type.to_string(),
        schema_version: 1,
        resource_id: resource_id.as_bytes().to_vec(),
        resource_type: "STORAGE_BUCKET".to_string(),
        resource_name,
        owner_id: owner_id.as_bytes().to_vec(),
        owner_type,
        zone_id: zone_id.as_bytes().to_vec(),
        source_version,
        effective_at: completed_at.to_rfc3339(),
        source_job_id: job_id.as_bytes().to_vec(),
        traceparent: String::new(),
    };
    let mut payload = Vec::with_capacity(wire.encoded_len());
    wire.encode(&mut payload)?;
    Ok(OwnershipIntent {
        event_id,
        event_type,
        payload,
    })
}

async fn record_failure(client: &Client, job_id: Uuid, job_topic: &str, error: &str) {
    let bounded_error = bounded_utf8(error, 512);
    let _ = client
        .execute(
            "UPDATE storage.storage_outbox_records \
             SET ownership_last_error = $3, ownership_locked_by = NULL, ownership_locked_until = NULL \
             WHERE event_id = $1 AND job_topic = $2 AND ownership_published_at IS NULL",
            &[&job_id, &job_topic, &bounded_error],
        )
        .await;
    crate::observability::metrics::MetricsManager::record_ownership_pending();
    Logger::sys_error_with_fields(
        "ownership.publish",
        "RESOURCE_OWNERSHIP_ENQUEUE_FAILED",
        "Ownership delivery remains pending in the storage outbox",
        &bounded_error,
        LogFields {
            operation_id: Some(&job_id.to_string()),
            retryable: Some(true),
            outcome: Some("pending"),
            ..LogFields::default()
        },
    );
}

fn ownership_event_id(job_id: Uuid, event_type: &str) -> Uuid {
    let mut seed = job_id.as_bytes().to_vec();
    seed.extend_from_slice(event_type.as_bytes());
    Uuid::new_v5(&OWNERSHIP_NAMESPACE, &seed)
}

fn bounded_utf8(value: &str, max_bytes: usize) -> String {
    if value.len() <= max_bytes {
        return value.to_string();
    }
    let mut end = max_bytes;
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    value[..end].to_string()
}

#[cfg(test)]
mod tests {
    use super::ownership_event_id;
    use uuid::Uuid;

    #[test]
    fn ownership_event_identity_is_stable_and_event_specific() {
        let job = Uuid::new_v4();
        assert_eq!(
            ownership_event_id(job, "RESOURCE_CREATED"),
            ownership_event_id(job, "RESOURCE_CREATED")
        );
        assert_ne!(
            ownership_event_id(job, "RESOURCE_CREATED"),
            ownership_event_id(job, "RESOURCE_DELETED")
        );
    }
}
