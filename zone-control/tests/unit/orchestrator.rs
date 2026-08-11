use std::collections::BTreeMap;

use super::*;

fn member(id: &str, weight: u32) -> MemberRecord {
    MemberRecord {
        member_id: id.to_string(),
        zone_id: "zone".to_string(),
        weight,
        max_concurrency: 16,
        heartbeat_seq: 1,
        expires_at_unix_ms: i64::MAX,
    }
}

#[test]
fn rendezvous_assignment_is_deterministic() {
    let members = vec![member("a", 1), member("b", 2), member("c", 1)];
    let first = (0..256)
        .map(|shard| {
            rendezvous_owner(&format!("storage-report.{shard}"), &members)
                .unwrap()
                .member_id
                .clone()
        })
        .collect::<Vec<_>>();
    let second = (0..256)
        .map(|shard| {
            rendezvous_owner(&format!("storage-report.{shard}"), &members)
                .unwrap()
                .member_id
                .clone()
        })
        .collect::<Vec<_>>();
    assert_eq!(first, second);
}

#[test]
fn weighted_assignment_does_not_collapse_to_one_replica() {
    let members = vec![member("a", 1), member("b", 1), member("c", 1)];
    let mut counts = BTreeMap::new();
    for shard in 0..512 {
        let owner = rendezvous_owner(&format!("metadata.{shard}"), &members).unwrap();
        *counts.entry(owner.member_id.clone()).or_insert(0) += 1;
    }
    assert_eq!(counts.len(), 3);
    assert!(counts.values().all(|count| *count > 20), "{counts:?}");
}

#[test]
fn assignment_follows_capacity_weight() {
    let mut small = member("small", 1);
    small.max_concurrency = 1;
    let mut large = member("large", 1);
    large.max_concurrency = 4;
    let members = vec![small, large];
    let mut counts = BTreeMap::new();
    for shard in 0..1_024 {
        let owner = rendezvous_owner(&format!("capacity.{shard}"), &members).unwrap();
        *counts.entry(owner.member_id.clone()).or_insert(0) += 1;
    }
    assert!(counts["large"] > counts["small"] * 2);
}

#[test]
fn removing_a_member_reassigns_work_without_a_global_owner() {
    let members = vec![member("a", 1), member("b", 1), member("c", 1)];
    let remaining = vec![member("a", 1), member("b", 1)];
    let mut reassigned = 0;
    for shard in 0..1_024 {
        let unit = format!("rebalance.{shard}");
        let before = rendezvous_owner(&unit, &members).unwrap();
        let after = rendezvous_owner(&unit, &remaining).unwrap();
        if before.member_id == "c" {
            assert_ne!(after.member_id, "c");
            reassigned += 1;
        }
    }
    assert!(reassigned > 0);
}

#[test]
fn expired_assignment_is_not_authorized() {
    let assignment = AssignmentRecord {
        unit_key: "assignment.storage_report.0".to_string(),
        member_id: "member-a".to_string(),
        assignment_epoch: 7,
        assigned_at_unix_ms: 10,
        expires_at_unix_ms: 20,
    };
    assert!(assignment.is_current_for("member-a", 19));
    assert!(!assignment.is_current_for("member-a", 20));
    assert!(!assignment.is_current_for("member-b", 19));
}
