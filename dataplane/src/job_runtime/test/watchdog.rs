use super::*;
use crate::infra::zone_kv::ZoneLease;

#[test]
fn preparation_and_completion_are_not_execution_timeouts() {
    let deadline = ExecutionDeadline::preparing(Duration::from_secs(1));
    assert!(!deadline.timed_out(Instant::now() + Duration::from_secs(10)));
    assert!(deadline.begin_execution());
    assert!(deadline.timed_out(Instant::now() + Duration::from_secs(2)));
    deadline.begin_completion();
    assert!(!deadline.timed_out(Instant::now() + Duration::from_secs(10)));
}

#[test]
fn execution_deadline_is_fixed_not_sliding() {
    let deadline = ExecutionDeadline::preparing(Duration::from_secs(10));
    assert!(deadline.begin_execution());
    let now = Instant::now();
    assert!(!deadline.timed_out(now + Duration::from_secs(5)));
    assert!(deadline.timed_out(now + Duration::from_secs(11)));
    assert!(deadline.timed_out(now + Duration::from_secs(12)));
}

#[test]
fn closed_empty_retry_queue_has_nothing_to_flush() {
    let (tx, rx) = tokio::sync::mpsc::channel(1);
    drop(rx);
    assert!(flush_timeout_retries(&tx, &mut VecDeque::new()));
}

#[tokio::test]
#[ignore = "requires AURORA_TEST_KAFKA"]
async fn timeout_is_generation_fenced_and_cannot_remove_a_new_execution() {
    let (queued, _) = crate::job_runtime::test::queued_job(4).await;
    let registry = JobExecutionLeaseRegistry::new();
    let lease = ZoneLease {
        key: "test-lease".into(),
        owner_id: "test-owner".into(),
        fencing_token: 1,
    };
    let first = registry
        .register_job_execution(
            lease.key.clone(),
            TrackedJobExecution::preparing(
                Duration::from_secs(1),
                CancellationToken::new(),
                queued.job.clone(),
                lease.clone(),
                queued.delivery.clone(),
            ),
        )
        .unwrap();
    assert!(registry.mark_execution_phase(&lease.key, first));
    assert!(registry.snapshot()[0]
        .2
        .execution_timed_out(Instant::now() + Duration::from_secs(2)));
    assert!(registry.remove_timed_out_if_current(&lease.key, first));
    assert!(!registry.remove_timed_out_if_current(&lease.key, first));
    let second = registry
        .register_job_execution(
            lease.key.clone(),
            TrackedJobExecution::preparing(
                Duration::from_secs(1),
                CancellationToken::new(),
                queued.job,
                lease.clone(),
                queued.delivery,
            ),
        )
        .unwrap();
    assert!(registry.mark_execution_phase(&lease.key, second));
    assert!(!registry.remove_timed_out_if_current(&lease.key, first));
    assert!(!registry.remove_if_current(&lease.key, first));
    assert_eq!(registry.snapshot()[0].1, second);
}

#[tokio::test]
#[ignore = "requires AURORA_TEST_KAFKA"]
async fn provider_completion_wins_over_a_late_watchdog_snapshot() {
    let (queued, _) = crate::job_runtime::test::queued_job(4).await;
    let registry = JobExecutionLeaseRegistry::new();
    let lease = ZoneLease {
        key: "test-lease".into(),
        owner_id: "test-owner".into(),
        fencing_token: 1,
    };
    let id = registry
        .register_job_execution(
            lease.key.clone(),
            TrackedJobExecution::preparing(
                Duration::from_secs(1),
                CancellationToken::new(),
                queued.job,
                lease.clone(),
                queued.delivery,
            ),
        )
        .unwrap();
    assert!(registry.mark_execution_phase(&lease.key, id));
    assert!(registry.snapshot()[0]
        .2
        .execution_timed_out(Instant::now() + Duration::from_secs(2)));
    assert!(registry.mark_completion_phase(&lease.key, id));
    assert!(!registry.remove_timed_out_if_current(&lease.key, id));
    assert_eq!(registry.snapshot().len(), 1);
}

#[tokio::test]
#[ignore = "requires AURORA_TEST_KAFKA"]
async fn full_recovery_queue_is_retained_then_flushed_without_ack() {
    let (queued, settlement) = crate::job_runtime::test::queued_job(4).await;
    let (tx, mut rx) = tokio::sync::mpsc::channel(1);
    let mut pending = VecDeque::new();
    for _ in 0..2 {
        pending.push_back(build_retry_request(
            &queued,
            queued.job.attempt,
            Duration::from_secs(30),
            "JOB_EXECUTION_OUTCOME_UNKNOWN",
        ));
    }
    assert!(flush_timeout_retries(&tx, &mut pending));
    assert_eq!(pending.len(), 1);
    assert!(rx.try_recv().is_ok());
    assert!(flush_timeout_retries(&tx, &mut pending));
    assert!(pending.is_empty());
    assert!(rx.try_recv().is_ok());
    assert_eq!(settlement.pending_records().await, 1);
}

#[tokio::test]
#[ignore = "requires AURORA_TEST_KAFKA"]
async fn closed_recovery_queue_keeps_pending_command_without_ack() {
    let (queued, settlement) = crate::job_runtime::test::queued_job(4).await;
    let (tx, rx) = tokio::sync::mpsc::channel(1);
    drop(rx);
    let mut pending = VecDeque::from([build_retry_request(
        &queued,
        queued.job.attempt,
        Duration::from_secs(30),
        "JOB_EXECUTION_OUTCOME_UNKNOWN",
    )]);
    assert!(!flush_timeout_retries(&tx, &mut pending));
    assert_eq!(pending.len(), 1);
    assert_eq!(settlement.pending_records().await, 1);
}
