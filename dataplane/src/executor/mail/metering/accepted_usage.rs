use std::sync::Arc;

use prost::Message;
use sha2::{Digest, Sha256};
use uuid::Uuid;

use crate::executor::mail::accepted_usage_proto::MailAcceptedUsageV1;
use crate::infra::kafka::KafkaTransport;

pub struct AcceptedUsagePublisher {
    kafka: Arc<KafkaTransport>,
}

impl AcceptedUsagePublisher {
    pub fn new(kafka: Arc<KafkaTransport>) -> Arc<Self> {
        Arc::new(Self { kafka })
    }

    pub async fn publish(
        &self,
        evidence_id: Uuid,
        zone_id: Uuid,
        resource_id: Uuid,
        accepted_at_unix_ms: i64,
    ) -> Result<(), String> {
        let mut evidence = MailAcceptedUsageV1 {
            schema_version: 1,
            evidence_id: evidence_id.to_string(),
            zone_id: zone_id.to_string(),
            resource_id: resource_id.to_string(),
            accepted_at_unix_ms,
            recipient_quantity: 1,
            evidence_sha256: Vec::new(),
        };
        evidence.evidence_sha256 = Sha256::digest(evidence.encode_to_vec()).to_vec();
        self.kafka
            .publish_message(
                &self.kafka.mail_accepted_usage_topic(),
                evidence_id.as_bytes(),
                &evidence,
            )
            .await
    }
}
