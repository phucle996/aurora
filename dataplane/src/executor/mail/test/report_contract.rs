use crate::executor::mail::runtime_proto::{
    MailConsumerRuntimeReportBatchV1, MailConsumerRuntimeReportedV1, MailConsumerRuntimeState,
    MailEventMetadataV1,
};
use prost::Message;
use uuid::Uuid;

#[test]
fn consumer_report_batch_contains_only_customer_safe_runtime_state() {
    let event_id = Uuid::new_v4();
    let consumer_id = Uuid::new_v4();
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
            runtime_epoch: Uuid::new_v4().to_string(),
        }],
    };
    let decoded = MailConsumerRuntimeReportBatchV1::decode(batch.encode_to_vec().as_slice())
        .expect("consumer report batch must decode");
    assert_eq!(decoded.reports.len(), 1);
    assert_eq!(decoded.reports[0].consumer_id, consumer_id.as_bytes());
    assert_eq!(decoded.reports[0].runtime_generation, 3);
}
