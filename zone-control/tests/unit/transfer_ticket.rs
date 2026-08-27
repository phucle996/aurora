use super::*;

#[test]
fn grant_accepts_multipart_and_version_queries_without_changing_binding() {
    for (method, path) in [
        ("POST", "/bucket/object?uploads"),
        ("PUT", "/bucket/object?partNumber=1&uploadId=a%2Bb%2F"),
        ("POST", "/bucket/object?uploadId=a%2Bb%2F"),
        ("DELETE", "/bucket/object?uploadId=a%2Bb%2F"),
        ("GET", "/bucket/object?versionId=a%2Bb%2F"),
    ] {
        let grant = TransferGrantV1 {
            schema_version: TRANSFER_TICKET_SCHEMA_VERSION,
            zone_id: "zone-test".into(),
            operation_id: uuid::Uuid::new_v4().to_string(),
            method: method.into(),
            public_path: path.into(),
            ..Default::default()
        };
        let encoded =
            base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(grant.encode_to_vec());
        let mut headers = HeaderMap::new();
        headers.insert(GRANT_HEADER, encoded.parse().unwrap());
        let decoded = decode_grant(&headers, "zone-test").unwrap();
        assert_eq!(decoded.method, method);
        assert_eq!(decoded.public_path, path);
        assert_eq!(
            decode_grant(&headers, "other-zone"),
            Err(StatusCode::FORBIDDEN)
        );
    }
}

#[test]
fn grant_rejects_unsafe_or_oversized_path_and_unsupported_method() {
    for (method, path) in [
        ("PATCH", "/bucket/object".to_string()),
        ("GET", "bucket/object".to_string()),
        ("GET", "/bucket/object\r\ninjected".to_string()),
        ("GET", "/bucket/object with space".to_string()),
        ("GET", format!("/{}", "a".repeat(2048))),
    ] {
        let grant = TransferGrantV1 {
            schema_version: TRANSFER_TICKET_SCHEMA_VERSION,
            zone_id: "zone-test".into(),
            operation_id: uuid::Uuid::new_v4().to_string(),
            method: method.into(),
            public_path: path,
            ..Default::default()
        };
        let encoded =
            base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(grant.encode_to_vec());
        let mut headers = HeaderMap::new();
        headers.insert(GRANT_HEADER, encoded.parse().unwrap());
        assert_eq!(
            decode_grant(&headers, "zone-test"),
            Err(StatusCode::FORBIDDEN)
        );
    }
}
