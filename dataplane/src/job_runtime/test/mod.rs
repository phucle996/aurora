use std::sync::Arc;
use std::time::Duration;

use krafka::consumer::Consumer;
use prost::Message;

use crate::infra::kafka::transport_proto::{JobCommandV1, PayloadEncodingV1};
use crate::infra::kafka::{KafkaDelivery, KafkaRebalanceFence, KafkaSettlement};
use crate::job_runtime::model::{QueuedJob, ValidatedJob};
use crate::security::jobpayload::PayloadKeyring;

// Fixtures still cross the real HPKE/transport validation boundary.
pub(crate) fn validated_job(
    domain: &str,
    topic: &str,
    resource: &str,
    payload: &[u8],
) -> Arc<ValidatedJob> {
    let keyring = PayloadKeyring::for_test();
    let zone = uuid::Uuid::new_v4();
    let command = JobCommandV1 {
        job_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
        job_version: 1,
        attempt: 0,
        source_domain: domain.into(),
        job_topic: topic.into(),
        resource_id: resource.into(),
        payload_schema_version: 1,
        payload: keyring.protect_for_test(zone, domain, topic, resource, 1, 1, payload),
        target_zone_id: zone.to_string(),
        transport_schema_version: 1,
        payload_encoding: PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm as i32,
        delivery_epoch: 7,
        idle_seconds: Some(60),
        ..Default::default()
    };
    ValidatedJob::decode(&command.encode_to_vec(), &zone.to_string(), 5, &keyring).unwrap()
}

// Real Kafka metadata connection; no test-only bypass of delivery/HPKE fences.
// These fixtures deliberately never subscribe or settle the registered source.
pub(crate) async fn queued_job(attempt: u32) -> (QueuedJob, Arc<KafkaSettlement>) {
    let broker =
        std::env::var("AURORA_TEST_KAFKA").expect("set AURORA_TEST_KAFKA for integration tests");
    let consumer = Consumer::builder()
        .bootstrap_servers(broker)
        .enable_auto_commit(false)
        .request_timeout(Duration::from_secs(15))
        .build()
        .await
        .expect("Kafka test broker");
    let settlement =
        KafkaSettlement::new(Arc::new(consumer), Arc::new(KafkaRebalanceFence::default()));
    settlement
        .register(0, "watchdog-test", 0, 10)
        .await
        .unwrap();

    let keyring = PayloadKeyring::for_test();
    let zone = uuid::Uuid::new_v4();
    let resource = uuid::Uuid::new_v4().to_string();
    let command = JobCommandV1 {
        job_id: uuid::Uuid::new_v4().as_bytes().to_vec(),
        job_version: 1,
        attempt,
        job_topic: "hypervisor.vm.create".to_owned(),
        source_domain: "HYPERVISOR".to_owned(),
        resource_id: resource.clone(),
        payload_schema_version: 1,
        payload: keyring.protect_for_test(
            zone,
            "HYPERVISOR",
            "hypervisor.vm.create",
            &resource,
            1,
            1,
            &[1, 2, 3],
        ),
        trace_id: Vec::new(),
        idle_seconds: Some(60),
        reconcile_generation: None,
        target_zone_id: zone.to_string(),
        transport_schema_version: 1,
        traceparent: String::new(),
        tracestate: String::new(),
        payload_encoding: PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm as i32,
        delivery_epoch: 7,
    };
    let job =
        ValidatedJob::decode(&command.encode_to_vec(), &zone.to_string(), 5, &keyring).unwrap();
    let delivery = KafkaDelivery::new("watchdog-test".into(), 0, 10, 0, settlement.clone());
    (QueuedJob { job, delivery }, settlement)
}
