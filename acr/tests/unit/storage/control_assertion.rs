use super::{action_for_request, contains_xml_element, storage_body_is_allowed, Assertion};

#[test]
fn storage_control_route_is_classified_and_bound() {
    let list = "/zone-control/v1/storage/buckets/ws-bucket/objects?list-type=2";
    assert_eq!(action_for_request("GET", list), Some("ListBucket"));
    assert_eq!(
        action_for_request(
            "GET",
            "/zone-control/v1/storage/buckets/ws-bucket/unreviewed/objects?list-type=2"
        ),
        None
    );
    let object = "/zone-control/v1/storage/buckets/ws-bucket/objects/logs/2026/a.json";
    assert_eq!(action_for_request("HEAD", object), Some("GetObject"));
}

#[test]
fn bulk_delete_does_not_inherit_version_delete() {
    assert!(contains_xml_element(
        b"<Delete><Object><Key>a</Key><VersionId>v1</VersionId></Object></Delete>",
        "versionid"
    ));
    assert!(!contains_xml_element(
        b"<Delete><Object><Key>versionid.txt</Key></Object></Delete>",
        "versionid"
    ));
    assert!(!storage_body_is_allowed("ListBucket", b"unexpected"));
    assert!(!storage_body_is_allowed("PutObjectTagging", b""));
}

#[test]
fn schema_two_assertion_contains_request_facts_not_storage_policy() {
    let assertion = Assertion {
        schema_version: 2,
        jti: uuid::Uuid::new_v4().to_string(),
        operation_id: uuid::Uuid::new_v4().to_string(),
        access_session_id: "0198a808-508e-7000-8000-000000000001",
        actor_id: "0198a808-508e-7000-8000-000000000002",
        workspace_id: "0198a808-508e-7000-8000-000000000003",
        zone_id: "0198a808-508e-7000-8000-000000000004",
        capability: "storage.object",
        action: "GetObject",
        method: "HEAD",
        path_hash: "a".repeat(64),
        body_hash: "b".repeat(64),
        issued_at: 1,
        expires_at: 11,
        audience: "zone-control-edge-gateway",
        issuer: "aurora-acr",
        key_id: "zone-control-assertion:v2",
    };
    let value = serde_json::to_value(assertion).unwrap();
    assert_eq!(value["schema_version"], 2);
    for policy_field in [
        "binding_hash",
        "resource_id",
        "resource_name",
        "scope",
        "policy_revision",
    ] {
        assert!(value.get(policy_field).is_none());
    }
}
