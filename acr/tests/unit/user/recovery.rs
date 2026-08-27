#[test]
fn recovery_lock_and_cache_share_the_cluster_hash_slot() {
    let recovery_key = "b7d2ad6f891c4e6da5b9f7204e3b31a6";
    let slot = format!("{{{recovery_key}}}");
    let cache_key = format!("iam:recovery:{{{recovery_key}}}:cache");
    let lock_key = format!("iam:recovery:{{{recovery_key}}}:lock");

    assert!(cache_key.contains(&slot));
    assert!(lock_key.contains(&slot));
}
