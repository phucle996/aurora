use super::*;

fn dummy_assertion() -> ControlAssertion {
    ControlAssertion {
        schema_version: 2,
        jti: "0198a808-508e-7000-8000-000000000001".to_string(),
        operation_id: "0198a808-508e-7000-8000-000000000002".to_string(),
        access_session_id: "0198a808-508e-7000-8000-000000000003".to_string(),
        actor_id: "0198a808-508e-7000-8000-000000000004".to_string(),
        workspace_id: "0198a808-508e-7000-8000-000000000005".to_string(),
        zone_id: "0198a808-508e-7000-8000-000000000006".to_string(),
        capability: "zone.transfer.ticket".to_string(),
        action: "multipart_initiate".to_string(),
        method: "POST".to_string(),
        path_hash: "a".repeat(64),
        body_hash: "b".repeat(64),
        issued_at: 1,
        expires_at: 11,
        audience: "zone-control-edge-gateway".to_string(),
        issuer: "aurora-acr".to_string(),
        key_id: "test".to_string(),
    }
}

fn dummy_record() -> AccessRecord {
    AccessRecord {
        access_session_id: "0198a808-508e-7000-8000-000000000003".to_string(),
        binding_hash: "a".repeat(64),
        actor_id: "0198a808-508e-7000-8000-000000000004".to_string(),
        resource_id: "0198a808-508e-7000-8000-000000000007".to_string(),
        bucket_name: "ws-test-bucket".to_string(),
        workspace_id: "0198a808-508e-7000-8000-000000000005".to_string(),
        zone_id: "0198a808-508e-7000-8000-000000000006".to_string(),
        actions: vec!["PutObject".to_string(), "GetObject".to_string()],
        key_prefix: String::new(),
        expires_at_unix_seconds: 9999999999,
        policy_revision: 1,
    }
}

#[test]
fn multipart_initiate_generates_correct_grant() {
    let assertion = dummy_assertion();
    let record = dummy_record();
    let body = serde_json::json!({
        "capability": "storage.object",
        "operation": "multipart_initiate",
        "access_session_id": "0198a808-508e-7000-8000-000000000003",
        "resource": {
            "bucket_name": "ws-test-bucket",
            "object_key": "photos/image.png"
        },
        "constraints": {
            "content_type": "image/png"
        }
    });
    let raw_grant = storage_object_grant(
        &assertion,
        &record,
        "POST",
        "/zone-control/v1/transfer-tickets",
        &serde_json::to_vec(&body).unwrap(),
    )
    .unwrap();
    let bytes = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .decode(raw_grant)
        .unwrap();
    let grant = TransferGrantV1::decode(bytes.as_slice()).unwrap();
    assert_eq!(grant.method, "POST");
    assert_eq!(
        grant.public_path,
        "/ws-test-bucket/photos/image.png?uploads"
    );
    assert_eq!(grant.content_type.as_deref(), Some("image/png"));
    assert!(grant.one_time);
}

#[test]
fn multipart_upload_part_validates_part_number_and_length() {
    let assertion = dummy_assertion();
    let record = dummy_record();
    let valid_body = serde_json::json!({
        "capability": "storage.object",
        "operation": "multipart_upload_part",
        "access_session_id": "0198a808-508e-7000-8000-000000000003",
        "resource": {
            "bucket_name": "ws-test-bucket",
            "object_key": "large.iso"
        },
        "constraints": {
            "upload_id": "up-123_abc",
            "part_number": 5,
            "content_length": 10485760
        }
    });
    let raw_grant = storage_object_grant(
        &assertion,
        &record,
        "POST",
        "/zone-control/v1/transfer-tickets",
        &serde_json::to_vec(&valid_body).unwrap(),
    )
    .unwrap();
    let bytes = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .decode(raw_grant)
        .unwrap();
    let grant = TransferGrantV1::decode(bytes.as_slice()).unwrap();
    assert_eq!(grant.method, "PUT");
    assert_eq!(
        grant.public_path,
        "/ws-test-bucket/large.iso?partNumber=5&uploadId=up-123_abc"
    );
    assert_eq!(grant.content_length, Some(10485760));

    // Part number 0 is invalid
    let invalid_part = serde_json::json!({
        "capability": "storage.object",
        "operation": "multipart_upload_part",
        "access_session_id": "0198a808-508e-7000-8000-000000000003",
        "resource": { "bucket_name": "ws-test-bucket", "object_key": "large.iso" },
        "constraints": { "upload_id": "up-123", "part_number": 0, "content_length": 1024 }
    });
    assert!(storage_object_grant(
        &assertion,
        &record,
        "POST",
        "/zone-control/v1/transfer-tickets",
        &serde_json::to_vec(&invalid_part).unwrap()
    )
    .is_err());
}

#[test]
fn multipart_complete_and_abort_routes() {
    let assertion = dummy_assertion();
    let record = dummy_record();
    let complete_body = serde_json::json!({
        "capability": "storage.object",
        "operation": "multipart_complete",
        "access_session_id": "0198a808-508e-7000-8000-000000000003",
        "resource": { "bucket_name": "ws-test-bucket", "object_key": "file.bin" },
        "constraints": { "upload_id": "up-xyz" }
    });
    let raw_grant = storage_object_grant(
        &assertion,
        &record,
        "POST",
        "/zone-control/v1/transfer-tickets",
        &serde_json::to_vec(&complete_body).unwrap(),
    )
    .unwrap();
    let bytes = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .decode(raw_grant)
        .unwrap();
    let grant = TransferGrantV1::decode(bytes.as_slice()).unwrap();
    assert_eq!(grant.method, "POST");
    assert_eq!(
        grant.public_path,
        "/ws-test-bucket/file.bin?uploadId=up-xyz"
    );

    let abort_body = serde_json::json!({
        "capability": "storage.object",
        "operation": "multipart_abort",
        "access_session_id": "0198a808-508e-7000-8000-000000000003",
        "resource": { "bucket_name": "ws-test-bucket", "object_key": "file.bin" },
        "constraints": { "upload_id": "up-xyz" }
    });
    let raw_grant = storage_object_grant(
        &assertion,
        &record,
        "POST",
        "/zone-control/v1/transfer-tickets",
        &serde_json::to_vec(&abort_body).unwrap(),
    )
    .unwrap();
    let bytes = base64::engine::general_purpose::URL_SAFE_NO_PAD
        .decode(raw_grant)
        .unwrap();
    let grant = TransferGrantV1::decode(bytes.as_slice()).unwrap();
    assert_eq!(grant.method, "DELETE");
    assert_eq!(
        grant.public_path,
        "/ws-test-bucket/file.bin?uploadId=up-xyz"
    );
}
