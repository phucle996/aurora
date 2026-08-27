use super::{parse_tenant_domain, parse_tenant_id};

#[tokio::test]
async fn tenant_resolution_preserves_verified_context_and_denial_reasons() {
    use super::{resolve_and_verify_tenant, Claims, HashMap};

    let tenant = "10000000-0000-4000-8000-000000000001";
    let other = "20000000-0000-4000-8000-000000000002";
    let cases = [
        (None, "", None, Ok(())),
        (Some("platform"), "tenant_id=platform", None, Ok(())),
    ];
    for (claim_tenant, cookie, header, expected) in cases {
        let mut claims = Claims {
            sub: "test-user".into(),
            uid: "test-user-id".into(),
            lvl: 3,
            tenant_id: claim_tenant.map(str::to_string),
            zone_id: None,
            access_key: "test-access-key".into(),
            iss: None,
            exp: i64::MAX,
            iat: 0,
        };
        let headers = header
            .map(|value: &str| HashMap::from([("x-tenant-id".into(), value.into())]))
            .unwrap_or_default();
        assert_eq!(
            resolve_and_verify_tenant(
                Some(&mut claims),
                cookie,
                &headers,
                "GET",
                "/api/v1/hierarchy/workspaces"
            )
            .await,
            expected
        );
        assert_eq!(claims.tenant_id.as_deref(), claim_tenant);
    }
    for (cookie, header, expected) in [
        (format!("tenant_id={tenant}"), None, Ok(())),
        (String::new(), Some(tenant), Ok(())),
        (format!("tenant_id={tenant}"), Some(other), Ok(())),
        (
            format!("tenant_id={other}"),
            Some(tenant),
            Err("Tenant unavailable"),
        ),
        (String::new(), Some(other), Err("Tenant unavailable")),
        (String::new(), None, Err("Tenant unavailable")),
    ] {
        let mut claims = Claims {
            sub: "test-user".into(),
            uid: "test-user-id".into(),
            lvl: 3,
            tenant_id: Some(tenant.into()),
            zone_id: None,
            access_key: "test-access-key".into(),
            iss: None,
            exp: i64::MAX,
            iat: 0,
        };
        let headers = header
            .map(|value| HashMap::from([("x-tenant-id".into(), value.into())]))
            .unwrap_or_default();
        let result = resolve_and_verify_tenant(
            Some(&mut claims),
            &cookie,
            &headers,
            "GET",
            "/api/v1/hierarchy/workspaces",
        )
        .await;
        assert_eq!(result, expected);
        assert_eq!(claims.tenant_id.as_deref(), Some(tenant));
        if let Err(reason) = result {
            use envoy_types::ext_authz::v3::CheckResponseExt;
            let response = envoy_types::pb::envoy::service::auth::v3::CheckResponse::with_status(
                tonic::Status::permission_denied(reason),
            );
            let status = response.status.expect("denial status");
            assert_eq!(status.code, tonic::Code::PermissionDenied as i32);
            assert_eq!(status.message, "Tenant unavailable");
            assert!(response.http_response.is_none());
        }
    }
}

#[tokio::test]
async fn tenant_resolution_still_rejects_matching_malformed_tenant_ids() {
    use super::{resolve_and_verify_tenant, Claims, HashMap};
    let mut claims = Claims {
        sub: "test-user".into(),
        uid: "test-user-id".into(),
        lvl: 3,
        tenant_id: Some("not-a-uuid".into()),
        zone_id: None,
        access_key: "test-access-key".into(),
        iss: None,
        exp: i64::MAX,
        iat: 0,
    };
    assert_eq!(
        resolve_and_verify_tenant(
            Some(&mut claims),
            "tenant_id=not-a-uuid",
            &HashMap::new(),
            "GET",
            "/api/v1/hierarchy/workspaces"
        )
        .await,
        Err("Tenant unavailable")
    );
}

#[test]
fn tenant_switch_query_is_percent_decoded_by_name() {
    let path = "/api/v1/context/go-to-tenant?ignored=1&tenant_domain=acme.example&tenant_id=10000000-0000-4000-8000-000000000001";
    assert_eq!(parse_tenant_domain(path).as_deref(), Some("acme.example"));
    assert_eq!(
        parse_tenant_id(path).as_deref(),
        Some("10000000-0000-4000-8000-000000000001")
    );
}

#[test]
fn tenant_switch_query_does_not_accept_prefix_collisions() {
    let path = "/api/v1/context/go-to-tenant?tenant_domain_suffix=evil&tenant_id_suffix=bad";
    assert_eq!(parse_tenant_domain(path), None);
    assert_eq!(parse_tenant_id(path), None);
}
