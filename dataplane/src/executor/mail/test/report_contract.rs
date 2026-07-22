use crate::executor::mail::runtime_proto::{
    MailConsumerRuntimeReportBatchV1, MailConsumerRuntimeReportedV1, MailConsumerRuntimeState,
    MailDataplaneNodeSnapshotV1, MailEventMetadataV1, MailInfrastructureSnapshotReportedV1,
    MailInfrastructureState,
};
use prost::Message;
use uuid::Uuid;

#[test]
fn consumer_report_batch_preserves_physical_process_identity() {
    let event_id = Uuid::new_v4();
    let consumer_id = Uuid::new_v4();
    let boot_id = Uuid::new_v4();
    let batch = MailConsumerRuntimeReportBatchV1 {
        reports: vec![MailConsumerRuntimeReportedV1 {
            metadata: Some(MailEventMetadataV1 {
                event_id: event_id.as_bytes().to_vec(),
                schema_version: 1,
                occurred_at_unix_ms: 1,
                traceparent: String::new(),
                producer: "dataplane-mail-consumer".to_string(),
            }),
            consumer_id: consumer_id.as_bytes().to_vec(),
            config_version: 2,
            runtime_state: MailConsumerRuntimeState::Running as i32,
            instance_id: "slot:0".to_string(),
            runtime_generation: 3,
            consumer_lag: 0,
            error_code: String::new(),
            error_message: String::new(),
            report_sequence: 4,
            runtime_node_id: "mail-dp-0".to_string(),
            runtime_boot_id: boot_id.as_bytes().to_vec(),
        }],
    };
    let decoded = MailConsumerRuntimeReportBatchV1::decode(batch.encode_to_vec().as_slice())
        .expect("consumer report batch must decode");
    assert_eq!(decoded.reports.len(), 1);
    assert_eq!(decoded.reports[0].runtime_node_id, "mail-dp-0");
    assert_eq!(decoded.reports[0].runtime_boot_id, boot_id.as_bytes());
}

#[test]
fn infrastructure_snapshot_contains_only_bounded_current_state_contract() {
    let event_id = Uuid::new_v4();
    let boot_id = Uuid::new_v4();
    let report = MailInfrastructureSnapshotReportedV1 {
        metadata: Some(MailEventMetadataV1 {
            event_id: event_id.as_bytes().to_vec(),
            schema_version: 1,
            occurred_at_unix_ms: 1,
            traceparent: String::new(),
            producer: "dataplane-mail-infra".to_string(),
        }),
        report_generation: 7,
        report_sequence: 1,
        service_state: MailInfrastructureState::Healthy as i32,
        capacity: 95,
        pending_items: 5,
        in_flight_batches: 1,
        probe_node_id: "mail-dp-0".to_string(),
        dataplane_nodes: vec![MailDataplaneNodeSnapshotV1 {
            node_id: "mail-dp-0".to_string(),
            boot_id: boot_id.as_bytes().to_vec(),
            state: MailInfrastructureState::Healthy as i32,
            capacity: 95,
            pending_items: 5,
            in_flight_batches: 1,
            active_consumer_slots: 2,
            jmap_reachable: true,
            last_probe_at_unix_ms: 1,
            observed_at_unix_ms: 1,
            error_code: String::new(),
        }],
        stalwart_nodes: Vec::new(),
        inventory_truncated: false,
        error_code: "MAIL_STALWART_INVENTORY_UNCONFIGURED".to_string(),
    };
    let encoded = report.encode_to_vec();
    assert!(encoded.len() < 1 << 20);
    let decoded = MailInfrastructureSnapshotReportedV1::decode(encoded.as_slice())
        .expect("infrastructure snapshot must decode");
    assert_eq!(decoded.dataplane_nodes.len(), 1);
    assert_eq!(decoded.report_generation, 7);
}
