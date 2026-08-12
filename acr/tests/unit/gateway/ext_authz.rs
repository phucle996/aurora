use super::{
    authority_matches_origin, is_acr_local_owner_control_path, is_billing_alias_path,
    is_internal_owner_billing_path, is_internal_owner_path, is_internal_render_context_path,
    is_personal_only_neutral_path, rewrite_neutral_owner_path, rewrite_owner_billing_path,
    rewrite_render_context_path,
};

#[test]
fn billing_surface_selects_session_by_console_authority() {
    assert!(!is_billing_alias_path(
        "GET",
        "/api/v1/billing/wallet/summary",
        false
    ));
    assert!(is_billing_alias_path(
        "GET",
        "/api/v1/billing/wallet/onboarding",
        true
    ));
    assert!(is_billing_alias_path("GET", "/api/v1/billing/tiers", true));
    assert!(is_billing_alias_path(
        "POST",
        "/api/v1/billing/critical/tiers/STORAGE/CODE",
        true
    ));
}

#[test]
fn owner_billing_rewrite_uses_only_verified_tenant_context() {
    assert_eq!(
        rewrite_owner_billing_path("/api/v1/billing/wallet/summary?fresh=1", None),
        Some("/api/v1/personal/billing/wallet/summary?fresh=1".to_string())
    );
    assert_eq!(
        rewrite_owner_billing_path(
            "/api/v1/billing/wallet/top-ups",
            Some("019f3d3e-997d-7894-9236-c5122634cb4f")
        ),
        Some("/api/v1/tenant/billing/wallet/top-ups".to_string())
    );
    assert_eq!(
        rewrite_owner_billing_path("/api/v1/billing/tiers", Some("tenant")),
        None
    );
}

#[test]
fn internal_owner_billing_routes_are_not_public_inputs() {
    assert!(is_internal_owner_billing_path(
        "/api/v1/personal/billing/wallet/summary"
    ));
    assert!(is_internal_owner_billing_path(
        "/api/v1/tenant/billing/wallet/top-ups"
    ));
    assert!(!is_internal_owner_billing_path(
        "/api/v1/billing/wallet/summary"
    ));
}

#[test]
fn iam_render_context_uses_alias_only_on_cost_console_authority() {
    assert!(is_billing_alias_path(
        "GET",
        "/api/v1/iam/context/read",
        true
    ));
    assert!(!is_billing_alias_path(
        "GET",
        "/api/v1/iam/context/read",
        false
    ));
    assert!(!is_billing_alias_path(
        "POST",
        "/api/v1/iam/context/read",
        true
    ));
    assert!(!is_billing_alias_path(
        "GET",
        "/api/v1/me/iam/profile/read",
        true
    ));
}

#[test]
fn render_context_rewrite_uses_only_verified_owner_context() {
    assert_eq!(
        rewrite_render_context_path("/api/v1/iam/context/read?fresh=1", None),
        Some("/api/v1/personal/iam/context/read?fresh=1".to_string())
    );
    assert_eq!(
        rewrite_render_context_path(
            "/api/v1/iam/context/read",
            Some("019f3d3e-997d-7894-9236-c5122634cb4f")
        ),
        Some("/api/v1/tenant/iam/context/read".to_string())
    );
    assert_eq!(
        rewrite_render_context_path("/api/v1/me/iam/context/read", None),
        None
    );
}

#[test]
fn internal_render_context_routes_are_not_public_inputs() {
    assert!(is_internal_render_context_path(
        "/api/v1/personal/iam/context/read"
    ));
    assert!(is_internal_render_context_path(
        "/api/v1/tenant/iam/context/read"
    ));
    assert!(!is_internal_render_context_path("/api/v1/iam/context/read"));
}

#[test]
fn every_owner_prefixed_business_route_is_internal() {
    assert!(is_internal_owner_path("/api/v1/personal/storage/buckets"));
    assert!(is_internal_owner_path(
        "/api/v1/tenant/managed-services/instances"
    ));
    assert!(!is_internal_owner_path("/api/v1/storage/buckets"));
    assert!(!is_internal_owner_path("/api/v1/iam/context/read"));
}

#[test]
fn tenant_switch_is_an_acr_local_control_route_not_a_downstream_owner_route() {
    assert!(is_acr_local_owner_control_path(
        "POST",
        "/api/v1/context/go-to-tenant"
    ));
    assert!(is_acr_local_owner_control_path(
        "POST",
        "/api/v1/context/go-to-personal"
    ));
    assert!(!is_acr_local_owner_control_path(
        "GET",
        "/api/v1/context/go-to-tenant"
    ));
    assert!(!is_acr_local_owner_control_path(
        "POST",
        "/api/v1/tenant/managed-services/instances"
    ));
}

#[test]
fn tenant_creation_is_a_personal_only_neutral_workflow() {
    assert!(is_personal_only_neutral_path("POST", "/api/v1/tenants"));
    assert!(is_personal_only_neutral_path("GET", "/api/v1/tenants"));
    assert!(!is_personal_only_neutral_path(
        "POST",
        "/api/v1/tenants/other"
    ));
}

#[test]
fn generic_business_path_is_rewritten_only_from_verified_owner_context() {
    assert_eq!(
        rewrite_neutral_owner_path("/api/v1/hierarchy/workspaces?limit=20", None),
        Some("/api/v1/personal/hierarchy/workspaces?limit=20".to_string())
    );
    assert_eq!(
        rewrite_neutral_owner_path(
            "/api/v1/managed-services/instances",
            Some("019f3d3e-997d-7894-9236-c5122634cb4f")
        ),
        Some("/api/v1/tenant/managed-services/instances".to_string())
    );
    assert_eq!(
        rewrite_neutral_owner_path("/api/v1/personal/storage/buckets", None),
        None
    );
    assert_eq!(
        rewrite_neutral_owner_path("/api/v1/me/iam/profile/read", None),
        None
    );
    assert_eq!(
        rewrite_neutral_owner_path(
            "/api/v1/me/critical/iam/social-link/github",
            Some("019f3d3e-997d-7894-9236-c5122634cb4f")
        ),
        None
    );
}

#[test]
fn authority_comparison_is_scheme_and_port_aware() {
    assert!(authority_matches_origin(
        "cost-manager.aurora.local:443",
        "https://cost-manager.aurora.local"
    ));
    assert!(!authority_matches_origin(
        "cloud.aurora.local:443",
        "https://cost-manager.aurora.local"
    ));
    assert!(!authority_matches_origin(
        "cost-manager.aurora.local:8443",
        "https://cost-manager.aurora.local"
    ));
}
