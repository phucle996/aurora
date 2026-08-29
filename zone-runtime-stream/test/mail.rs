use super::*;
use uuid::Uuid;

#[test]
fn mail_query_is_fixed_to_registered_consumer_scope() {
    let scope = RuntimeScope {
        module: "mail".into(),
        resource_type: "consumer".into(),
        resource_id: Uuid::new_v4(),
        resource_name: None,
        owner_id: Uuid::new_v4(),
        workspace_id: Uuid::new_v4(),
        zone_id: Uuid::new_v4(),
        component_id: Some("slot-1".into()),
        panel_id: "health".into(),
        snapshot_seconds: 60,
    };
    let query = fixed_query(&scope).expect("Mail scope must have a fixed query");
    assert!(query.starts_with("aurora_runtime_health{"));
    assert!(query.contains(&scope.resource_id.to_string()));
    assert!(query.contains("aurora_module=\"mail\""));
    assert!(query.contains(&format!("aurora_zone_id=\"{}\"", scope.zone_id)));
}

#[test]
fn mail_adapter_rejects_other_resource_types() {
    let scope = RuntimeScope {
        module: "mail".into(),
        resource_type: "template".into(),
        resource_id: Uuid::new_v4(),
        resource_name: None,
        owner_id: Uuid::new_v4(),
        workspace_id: Uuid::new_v4(),
        zone_id: Uuid::new_v4(),
        component_id: None,
        panel_id: "health".into(),
        snapshot_seconds: 60,
    };
    assert!(!validate_scope(&scope));
    assert!(fixed_query(&scope).is_none());
}
