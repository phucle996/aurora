use super::*;

#[test]
fn generation_fence_fails_closed_after_local_lease_deadline() {
    let fence = RuntimeGenerationFence::new(Duration::ZERO);
    assert!(!fence.is_accepting());

    fence.refresh_lease(Duration::from_secs(1));
    assert!(fence.is_accepting());
}
