use super::*;

#[test]
fn social_login_return_path_allowlist_keeps_only_self_continuations() {
    assert!(social_login_return_to("/personal"));
    assert!(social_login_return_to("/billing/authorize?request_id=abc"));
    assert!(social_login_return_to(
        "/personal/settings/tenant-invitations/join?token=0123456789abcdefghijklmnopqrstuvwxyzABCDE-_"
    ));
    assert!(!social_login_return_to("/"));
    assert!(!social_login_return_to("/personal/settings/social-links"));
    assert!(!social_login_return_to("/tenant/settings/social-links"));
    assert!(!social_login_return_to("//attacker.example"));
    assert!(!social_login_return_to("https://attacker.example"));
    assert!(!social_login_return_to("/billing/authorize/other"));
}

#[test]
fn social_link_state_omits_zone_and_tenant_context() {
    let state = OAuthState {
        flow: "link".to_string(),
        provider: "github".to_string(),
        operation_id: "operation".to_string(),
        code_verifier: "verifier".to_string(),
        nonce: "nonce".to_string(),
        device_public_key: "proof".to_string(),
        client_device_id: "device".to_string(),
        device_name: String::new(),
        device_type: String::new(),
        trust_device: false,
        zone_id: String::new(),
        zone_code: String::new(),
        user_id: "user".to_string(),
        return_to: "/tenant/settings/social-links".to_string(),
    };

    let serialized = serde_json::to_string(&state).expect("state must serialize");
    assert!(!serialized.contains("zone_id"));
    assert!(!serialized.contains("zone_code"));
    assert!(!serialized.contains("tenant_id"));
}

#[test]
fn canonical_identity_normalizes_only_provider_verified_fields() {
    let identity = canonical_identity(
        "google",
        " provider-subject ".to_string(),
        " USER@Example.COM ".to_string(),
        " Aurora User ".to_string(),
        Some("http://example.com/avatar".to_string()),
    )
    .expect("identity must be canonical");

    assert_eq!(identity.subject, "provider-subject");
    assert_eq!(identity.email, "user@example.com");
    assert_eq!(identity.display_name, "Aurora User");
    assert!(identity.avatar_url.is_none());
}

#[test]
fn provider_avatar_accepts_https_query_but_rejects_embedded_credentials() {
    let github = canonical_identity(
        "github",
        "42".to_string(),
        "user@example.com".to_string(),
        "Aurora User".to_string(),
        Some("https://avatars.githubusercontent.com/u/42?v=4".to_string()),
    )
    .expect("GitHub identity must be canonical");
    assert_eq!(
        github.avatar_url.as_deref(),
        Some("https://avatars.githubusercontent.com/u/42?v=4")
    );

    let credentialed = canonical_identity(
        "github",
        "42".to_string(),
        "user@example.com".to_string(),
        "Aurora User".to_string(),
        Some("https://user:secret@example.com/avatar".to_string()),
    )
    .expect("invalid optional avatar must not reject the identity");
    assert!(credentialed.avatar_url.is_none());
}

#[test]
fn social_link_state_and_index_share_the_cluster_hash_slot() {
    let user_id = "019f9f2d-25a7-7bb3-8bf1-78c6e65bcf94";
    let slot = social_link_slot(user_id);
    let state_token = format!("{slot}.opaque-state");
    assert!(state_key("github", &state_token).contains(&format!("{{{slot}}}")));
    assert!(link_index_key(user_id, "github").contains(&format!("{{{slot}}}")));
}

#[test]
fn callback_failure_exposes_only_the_generic_error_and_returns_to_signin() {
    use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;

    let response = oauth_failure_redirect("/billing/authorize?request_id=opaque");
    let denied = match response.http_response {
        Some(HttpResponse::DeniedResponse(denied)) => denied,
        _ => panic!("OAuth failure must be an Envoy denied response"),
    };
    let location = denied
        .headers
        .into_iter()
        .filter_map(|option| option.header)
        .find(|header| header.key.eq_ignore_ascii_case("location"))
        .map(|header| header.value)
        .expect("OAuth failure response must redirect");

    assert!(location.starts_with("/signin?"));
    assert!(location.contains("oauth_error=OAUTH_SIGN_IN_FAILED"));
    assert!(location.contains("return_to=%2Fbilling%2Fauthorize%3Frequest_id%3Dopaque"));
    assert!(!location.contains("INVALID_CREDENTIALS"));
    assert!(!location.contains("PROVIDER_"));
}
