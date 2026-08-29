use super::*;

#[test]
fn subscription_key_never_uses_client_query() {
    let zone = Uuid::new_v4();
    let scope = RuntimeScope {
        module: "managed_service".into(),
        resource_type: "instance".into(),
        resource_id: Uuid::new_v4(),
        resource_name: None,
        owner_id: Uuid::new_v4(),
        workspace_id: Uuid::new_v4(),
        zone_id: zone,
        component_id: None,
        panel_id: "health".into(),
        snapshot_seconds: 60,
    };
    assert!(scope.validate(zone).is_ok());
    assert!(scope.validate(Uuid::new_v4()).is_err());
}

#[test]
fn scope_rejects_unknown_panel_and_zero_snapshot() {
    let zone = Uuid::new_v4();
    let mut scope = RuntimeScope {
        module: "managed_service".into(),
        resource_type: "instance".into(),
        resource_id: Uuid::new_v4(),
        resource_name: None,
        owner_id: Uuid::new_v4(),
        workspace_id: Uuid::new_v4(),
        zone_id: zone,
        component_id: None,
        panel_id: "shell".into(),
        snapshot_seconds: 60,
    };
    assert!(matches!(
        scope.validate(zone),
        Err(ContractError::UnsupportedPanel)
    ));
    scope.panel_id = "health".into();
    scope.snapshot_seconds = 0;
    assert!(matches!(
        scope.validate(zone),
        Err(ContractError::SnapshotWindowInvalid)
    ));
}
