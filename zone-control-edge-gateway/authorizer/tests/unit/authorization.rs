use super::*;

fn assertion_and_record() -> (ControlAssertion, AccessRecord, String) {
    let path = "/zone-control/v1/storage/buckets/ws-bucket/objects/a.txt".to_string();
    let access_session_id = uuid::Uuid::new_v4().to_string();
    let actor_id = uuid::Uuid::new_v4().to_string();
    let workspace_id = uuid::Uuid::new_v4().to_string();
    let zone_id = uuid::Uuid::new_v4().to_string();
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs();
    let assertion = ControlAssertion {
        schema_version: 2,
        jti: uuid::Uuid::new_v4().to_string(),
        operation_id: uuid::Uuid::new_v4().to_string(),
        access_session_id: access_session_id.clone(),
        actor_id: actor_id.clone(),
        workspace_id: workspace_id.clone(),
        zone_id: zone_id.clone(),
        capability: "storage.object".to_string(),
        action: "GetObject".to_string(),
        method: "HEAD".to_string(),
        path_hash: crate::request_binding::sha256_hex(path.as_bytes()),
        body_hash: crate::request_binding::sha256_hex(b""),
        issued_at: now as i64,
        expires_at: now as i64 + 10,
        audience: "zone-control-edge-gateway".to_string(),
        issuer: "aurora-acr".to_string(),
        key_id: "zone-control-assertion:v2".to_string(),
    };
    let record = AccessRecord {
        access_session_id,
        binding_hash: "ab".repeat(32),
        actor_id,
        resource_id: uuid::Uuid::new_v4().to_string(),
        bucket_name: "ws-bucket".to_string(),
        workspace_id,
        zone_id,
        actions: vec!["GetObject".to_string()],
        key_prefix: "".to_string(),
        expires_at_unix_seconds: now + 60,
        policy_revision: 1,
    };
    (assertion, record, path)
}

#[test]
fn zone_record_is_the_policy_authority_for_request_facts() {
    let (assertion, mut record, path) = assertion_and_record();
    assert!(match_storage_record(&assertion, &record, "HEAD", &path).is_ok());

    record.workspace_id = uuid::Uuid::new_v4().to_string();
    assert!(match_storage_record(&assertion, &record, "HEAD", &path).is_err());
    record.workspace_id = assertion.workspace_id.clone();
    record.actions.clear();
    assert!(match_storage_record(&assertion, &record, "HEAD", &path).is_err());
}
