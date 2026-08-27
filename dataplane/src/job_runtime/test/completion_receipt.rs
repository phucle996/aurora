use super::*;
use crate::job_runtime::test::validated_job;

#[tokio::test]
#[ignore = "requires dedicated AURORA_TEST_NATS"]
async fn failed_receipt_replays_failure_and_unknown_outcomes_are_never_cached() {
    let store = ZoneKvStore::for_test().await;
    let job = validated_job(
        "HYPERVISOR",
        "hypervisor.vm.delete",
        &uuid::Uuid::new_v4().to_string(),
        &[3],
    );
    let unknown = JobExecutionResult::from_executor(
        &job,
        Err(crate::executor::ExecutorError::OutcomeUnknown(
            "lost ACK".into(),
        )),
    );
    assert!(save(&store, &job, &unknown).await.is_err());
    assert!(load(&store, &job).await.unwrap().is_none());
    let failed = JobExecutionResult::from_executor(
        &job,
        Err(crate::executor::ExecutorError::ExecutionFailed(
            "provider failed".into(),
        )),
    );
    save(&store, &job, &failed).await.unwrap();
    let replay = load(&store, &job).await.unwrap().unwrap();
    assert_eq!(replay.status, CompletionStatus::Failed);
    assert_eq!(replay.error_code, failed.error_code);
}

#[tokio::test]
#[ignore = "requires dedicated AURORA_TEST_NATS"]
async fn receipt_is_isolated_durable_and_conflicting_result_cannot_replace_it() {
    let store = ZoneKvStore::for_test().await;
    let job = validated_job(
        "HYPERVISOR",
        "hypervisor.vm.delete",
        &uuid::Uuid::new_v4().to_string(),
        &[1, 2, 3],
    );
    let result = JobExecutionResult::from_executor(
        &job,
        Ok(ExecutionResult {
            message: "deleted".into(),
            result_payload: vec![7, 8],
            result_payload_schema_version: 1,
        }),
    );
    save(&store, &job, &result).await.unwrap();
    save(&store, &job, &result).await.unwrap();
    let key = format!("job.completion.{}.{}", job.job_id, job.delivery_epoch);
    assert!(store.config_get(&key).await.unwrap().is_none());
    assert!(store.completion().get(&key).await.unwrap().is_some());
    let loaded = load(&store, &job).await.unwrap().unwrap();
    assert_eq!(loaded.result_payload, vec![7, 8]);
    let mut conflict = result;
    conflict.result_payload = vec![9];
    assert!(save(&store, &job, &conflict).await.is_err());
    assert_eq!(
        load(&store, &job).await.unwrap().unwrap().result_payload,
        vec![7, 8]
    );
}

#[tokio::test]
#[ignore = "requires dedicated AURORA_TEST_NATS"]
async fn old_config_receipt_remains_authoritative_after_bucket_change() {
    let source = ZoneKvStore::for_test().await;
    let upgraded = ZoneKvStore::for_test().await;
    let job = validated_job(
        "MAIL",
        "mail.consumer.drain",
        &uuid::Uuid::new_v4().to_string(),
        &[1],
    );
    let result = JobExecutionResult::from_executor(
        &job,
        Ok(ExecutionResult {
            message: "drained".into(),
            result_payload: vec![4],
            result_payload_schema_version: 1,
        }),
    );
    save(&source, &job, &result).await.unwrap();
    let key = format!("job.completion.{}.{}", job.job_id, job.delivery_epoch);
    upgraded
        .config_create(&key, source.completion().get(&key).await.unwrap().unwrap())
        .await
        .unwrap();
    assert_eq!(
        load(&upgraded, &job).await.unwrap().unwrap().result_payload,
        vec![4]
    );
    // Deleting a new-bucket receipt is not a cache miss: the tombstone fails
    // closed rather than silently permitting an external mutation to replay.
    save(&upgraded, &job, &result).await.unwrap();
    upgraded.completion().delete(&key).await.unwrap();
    assert!(load(&upgraded, &job).await.is_err());
}
