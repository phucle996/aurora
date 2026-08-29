use super::*;
use uuid::Uuid;

fn scope() -> RuntimeScope {
    RuntimeScope {
        module: "storage".into(),
        resource_type: "bucket".into(),
        resource_id: Uuid::new_v4(),
        resource_name: Some("ws-12345678-backups".into()),
        owner_id: Uuid::new_v4(),
        workspace_id: Uuid::new_v4(),
        zone_id: Uuid::new_v4(),
        component_id: None,
        panel_id: "metrics".into(),
        snapshot_seconds: 60,
    }
}

#[test]
fn storage_query_uses_only_the_trusted_physical_name() {
    let scope = scope();
    assert!(validate_scope(&scope));
    assert!(scope.validate(scope.zone_id).is_ok());
    assert_eq!(
        fixed_query(&scope).as_deref(),
        Some("minio_bucket_usage_total_bytes{bucket=\"ws-12345678-backups\"}")
    );
    assert!(!fixed_query(&RuntimeScope {
        resource_name: Some("../../other".into()),
        ..scope
    })
    .is_some());
}
