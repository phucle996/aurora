use std::sync::Arc;
use std::time::Duration;

use chrono::{DateTime, Utc};
use prost::Message;
use tokio_postgres::Client;
use uuid::Uuid;

use super::ownership::ownership_proto;
use super::redis::SharedStreamPublisher;
use crate::config::Config;
use crate::observability::logger::{LogFields, Logger};

const OWNERSHIP_NAMESPACE: Uuid = Uuid::from_u128(0xe5c3_6d87_6dcc_58e1_978d_2819_40ac_c520);

pub struct MailBillingRelay {
    config: Config,
    publisher: Arc<SharedStreamPublisher>,
    worker_id: String,
}

impl MailBillingRelay {
    pub fn new(config: Config, publisher: Arc<SharedStreamPublisher>) -> Self {
        Self {
            config,
            publisher,
            worker_id: format!(
                "{}-mail-billing-{}",
                crate::config::get_node_hostname(),
                Uuid::new_v4()
            ),
        }
    }

    pub async fn run(&self) -> Result<(), Box<dyn std::error::Error>> {
        loop {
            match self.run_session().await {
                Ok(()) => return Err("Mail billing relay PostgreSQL session ended".into()),
                Err(error) => {
                    Logger::sys_error(
                        "mail.billing.relay",
                        "Mail billing relay session failed; reconnecting",
                        &error.to_string(),
                    );
                    tokio::time::sleep(Duration::from_secs(2 + self.worker_jitter_secs())).await;
                }
            }
        }
    }

    async fn run_session(&self) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let client =
            crate::infra::postgres::connect(&self.config.postgres, "mail.billing.postgres").await?;
        loop {
            let claimed = claim_pending(
                &client,
                &self.worker_id,
                self.config.workflows.ownership.reconcile_batch_size,
                self.config.workflows.ownership.lease_secs,
            )
            .await?;
            for source_event_id in &claimed {
                if let Err(error) =
                    publish_for_event(&client, &self.publisher, *source_event_id).await
                {
                    record_failure(&client, *source_event_id, &error.to_string()).await;
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
            "WITH candidates AS (
                 SELECT source_event_id FROM mail.mail_consumer_billing_outbox
                 WHERE published_at IS NULL
                   AND (locked_until IS NULL OR locked_until < NOW())
                 ORDER BY effective_at ASC, source_event_id ASC
                 FOR UPDATE SKIP LOCKED LIMIT $1
             )
             UPDATE mail.mail_consumer_billing_outbox outbox
             SET locked_by=$2, locked_until=NOW()+($3::bigint*INTERVAL '1 second'),
                 attempt_count=attempt_count+1
             FROM candidates
             WHERE outbox.source_event_id=candidates.source_event_id
             RETURNING outbox.source_event_id",
            &[&batch_size, &worker_id, &lease_secs],
        )
        .await?;
    Ok(rows.into_iter().map(|row| row.get(0)).collect())
}

async fn publish_for_event(
    client: &Client,
    publisher: &SharedStreamPublisher,
    source_event_id: Uuid,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let row = client
        .query_opt(
            "SELECT event_type,resource_id,resource_name,owner_id,owner_type,zone_id,
                    source_version,effective_at
             FROM mail.mail_consumer_billing_outbox
             WHERE source_event_id=$1 AND published_at IS NULL",
            &[&source_event_id],
        )
        .await?;
    let Some(row) = row else {
        return Ok(());
    };
    let event_type: String = row.get(0);
    let resource_id: Uuid = row.get(1);
    let resource_name: String = row.get(2);
    let owner_id: Uuid = row.get(3);
    let owner_type: String = row.get(4);
    let zone_id: Uuid = row.get(5);
    let source_version: i64 = row.get(6);
    let effective_at: DateTime<Utc> = row.get(7);
    let event_id = Uuid::new_v5(
        &OWNERSHIP_NAMESPACE,
        format!("{source_event_id}:{event_type}:{source_version}").as_bytes(),
    );
    let wire = ownership_proto::ResourceOwnershipChangedV1 {
        event_id: event_id.as_bytes().to_vec(),
        event_type: event_type.clone(),
        schema_version: 1,
        resource_id: resource_id.as_bytes().to_vec(),
        resource_type: "MAIL_CONSUMER".to_string(),
        resource_name,
        owner_id: owner_id.as_bytes().to_vec(),
        owner_type,
        zone_id: zone_id.as_bytes().to_vec(),
        source_version,
        effective_at: effective_at.to_rfc3339(),
        source_job_id: source_event_id.as_bytes().to_vec(),
        traceparent: String::new(),
    };
    let mut payload = Vec::with_capacity(wire.encoded_len());
    wire.encode(&mut payload)?;
    publisher
        .publish_ownership(&event_id.to_string(), &event_type, &payload)
        .await?;
    client
        .execute(
            "UPDATE mail.mail_consumer_billing_outbox
             SET published_at=COALESCE(published_at,NOW()),locked_by=NULL,locked_until=NULL,last_error=NULL
             WHERE source_event_id=$1 AND published_at IS NULL",
            &[&source_event_id],
        )
        .await?;
    Logger::sys_info_with_fields(
        "mail.billing.publish",
        "MAIL_BILLING_OWNERSHIP_ENQUEUED",
        "Mail consumer ownership passed the Shared Redis durability fence",
        LogFields {
            operation_id: Some(&source_event_id.to_string()),
            outcome: Some("enqueued"),
            ..LogFields::default()
        },
    );
    Ok(())
}

async fn record_failure(client: &Client, source_event_id: Uuid, error: &str) {
    let bounded = error.chars().take(512).collect::<String>();
    let _ = client
        .execute(
            "UPDATE mail.mail_consumer_billing_outbox
             SET last_error=$2,locked_by=NULL,locked_until=NULL
             WHERE source_event_id=$1 AND published_at IS NULL",
            &[&source_event_id, &bounded],
        )
        .await;
}
