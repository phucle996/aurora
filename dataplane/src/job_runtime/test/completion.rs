use super::*;
use prost::Message;

#[test]
fn unknown_outcome_cannot_be_exhausted_into_a_false_failure() {
    let mut result = JobExecutionResult {
        job_id: uuid::Uuid::new_v4().to_string(),
        resource_id: uuid::Uuid::new_v4().to_string(),
        job_version: 1,
        attempt: 4,
        status: CompletionStatus::Retryable,
        error_code: Some("JOB_OUTCOME_UNKNOWN".into()),
        message: "provider ACK unavailable".into(),
        job_topic: "hypervisor.vm.delete".into(),
        source_domain: "HYPERVISOR".into(),
        trace_id: String::new(),
        result_payload: Vec::new(),
        result_payload_schema_version: 0,
    };
    result.mark_retry_exhausted();
    assert_eq!(result.status, CompletionStatus::Retryable);
    assert_eq!(result.attempt, 4);
    assert_eq!(result.error_code.as_deref(), Some("JOB_OUTCOME_UNKNOWN"));
    result.error_code = Some("TRANSIENT_INFRASTRUCTURE".into());
    result.mark_retry_exhausted();
    assert_eq!(result.status, CompletionStatus::Failed);
}

#[test]
fn stable_dlq_identity_is_replay_safe() {
    assert_eq!(
        stable_dlq_event_id("topic", 1, 2, "INVALID"),
        stable_dlq_event_id("topic", 1, 2, "INVALID")
    );
    assert_ne!(
        stable_dlq_event_id("topic", 1, 2, "INVALID"),
        stable_dlq_event_id("topic", 1, 3, "INVALID")
    );
}

#[test]
fn trace_decoder_fails_closed() {
    assert_eq!(decode_hex("0011ff").unwrap(), vec![0x00, 0x11, 0xff]);
    assert!(decode_hex("00zz").is_err());
    assert!(decode_hex("0").is_err());
}

#[test]
fn dlq_omits_untrusted_raw_payload_and_redacts_diagnostic() {
    let record = build_dead_letter_record(
        uuid::Uuid::nil().as_bytes().to_vec(),
        "commands".to_string(),
        1,
        2,
        "INVALID",
        "upstream password=do-not-publish",
        b"plaintext-customer-secret",
    );
    assert!(record.original_payload.is_empty());
    assert!(!record.error_message.contains("do-not-publish"));
    assert!(!record.error_message.contains("plaintext-customer-secret"));
    assert!(record.error_message.contains("original_payload_sha256="));
}

#[tokio::test]
#[ignore = "requires AURORA_TEST_KAFKA"]
async fn timeout_replay_preserves_last_attempt_and_protected_command_without_ack() {
    let (queued, settlement) = crate::job_runtime::test::queued_job(4).await;
    let original = queued
        .job
        .command_for_attempt(4, String::new(), String::new());
    let request = build_retry_request(
        &queued,
        queued.job.attempt,
        Duration::from_secs(30),
        "JOB_EXECUTION_OUTCOME_UNKNOWN",
    );
    assert_eq!(request.next_attempt, 4);
    assert_eq!(request.reason, "JOB_EXECUTION_OUTCOME_UNKNOWN");
    assert_eq!(request.delay, Duration::from_secs(30));
    assert_eq!(request.topic, queued.delivery.topic);
    assert_eq!(
        request.delivery.assignment_epoch,
        queued.delivery.assignment_epoch
    );
    let replay =
        request
            .job
            .command_for_attempt(request.next_attempt, String::new(), String::new());
    assert_eq!(replay, original);
    assert_eq!(replay.delivery_epoch, 7);
    let decoded = ValidatedJob::decode(
        &replay.encode_to_vec(),
        &queued.job.target_zone_id,
        5,
        &crate::security::jobpayload::PayloadKeyring::for_test(),
    )
    .expect("last-attempt timeout replay must remain admissible and decryptable");
    assert_eq!(decoded.payload.as_ref(), queued.job.payload.as_ref());
    assert_eq!(settlement.pending_records().await, 1);
}

#[tokio::test]
#[ignore = "requires AURORA_TEST_KAFKA"]
async fn closed_retry_queue_does_not_ack_the_source() {
    let (queued, settlement) = crate::job_runtime::test::queued_job(4).await;
    let (tx, rx) = mpsc::channel(1);
    drop(rx);
    let request = build_retry_request(
        &queued,
        queued.job.attempt,
        Duration::from_secs(30),
        "JOB_EXECUTION_OUTCOME_UNKNOWN",
    );
    assert!(enqueue_retry(&tx, request).await.is_err());
    assert_eq!(settlement.pending_records().await, 1);
}
