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
fn only_storage_scan_work_is_sharded() {
    let units = work_units(16);
    assert_eq!(units.len(), 24);
    for class in WorkClass::ALL {
        let class_units = units
            .iter()
            .filter(|unit| unit.class == class)
            .collect::<Vec<_>>();
        if class == WorkClass::StorageScan {
            assert_eq!(class_units.len(), 16);
            assert_eq!(class_units.first().unwrap().shard, 0);
            assert_eq!(class_units.last().unwrap().shard, 15);
        } else {
            assert_eq!(class_units.len(), 1);
            assert_eq!(class_units[0].shard, 0);
        }
    }
}

#[test]
fn same_owner_renewal_preserves_fencing_epoch_and_assignment_time() {
    let assignment = AssignmentRecord {
        unit_key: "assignment.storage_report.0".to_string(),
        member_id: "member-a".to_string(),
        assignment_epoch: 7,
        assigned_at_unix_ms: 10,
        expires_at_unix_ms: 20,
    };

    let renewed = assignment.renew(18);

    assert_eq!(renewed.member_id, assignment.member_id);
    assert_eq!(renewed.assignment_epoch, assignment.assignment_epoch);
    assert_eq!(renewed.assigned_at_unix_ms, assignment.assigned_at_unix_ms);
    assert_eq!(
        renewed.expires_at_unix_ms,
        18 + ASSIGNMENT_TTL.as_millis() as i64
    );
}

#[test]
fn assignment_renews_before_a_single_reconcile_tick_can_miss_expiry() {
    let assignment = AssignmentRecord {
        unit_key: "assignment.zone_report.0".to_string(),
        member_id: "member-a".to_string(),
        assignment_epoch: 7,
        assigned_at_unix_ms: 10,
        expires_at_unix_ms: 20_000,
    };

    assert!(!assignment.needs_renewal(9_999));
    assert!(assignment.needs_renewal(10_000));
    assert!(ASSIGNMENT_RENEWAL_MARGIN >= RECONCILE_INTERVAL.saturating_add(RECONCILE_INTERVAL));
}

#[tokio::test]
async fn workflow_restarts_when_fencing_epoch_changes() {
    let mut task = None;
    sync_workflow(&mut task, true, "test", 7, |shutdown, _| {
        tokio::spawn(async move {
            shutdown.cancelled().await;
            Ok(())
        })
    })
    .await;
    assert_eq!(task.as_ref().unwrap().assignment_epoch, 7);

    sync_workflow(&mut task, true, "test", 8, |shutdown, _| {
        tokio::spawn(async move {
            shutdown.cancelled().await;
            Ok(())
        })
    })
    .await;
    assert_eq!(task.as_ref().unwrap().assignment_epoch, 8);

    stop_workflow(&mut task).await;
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
