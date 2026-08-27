use super::*;

#[tokio::test]
async fn draining_stops_intake_without_fencing_already_owned_work() {
    let fence = RuntimeGenerationFence::new(Duration::from_secs(60));
    assert!(fence.drain_is_complete());
    fence.mark_running();
    fence.drain_requested.store(true, Ordering::Release);
    assert!(fence.is_draining());
    assert!(!fence.drain_is_complete());
    let permit = fence.enter_submit().await.expect("drain must allow owned work to settle");
    drop(permit);
    fence.mark_drained();
    fence.fence().await;
    assert!(fence.drain_is_complete());
    assert!(fence.enter_submit().await.is_none());
}

#[test]
fn generation_fence_fails_closed_after_local_lease_deadline() {
    let fence = RuntimeGenerationFence::new(Duration::ZERO);
    assert!(!fence.is_accepting());

    fence.refresh_lease(Duration::from_secs(1));
    assert!(fence.is_accepting());
}
