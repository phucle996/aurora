fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Compile ACR-owned contracts and the canonical cross-service IAM contract
    // from the root proto registry so tonic emits one coherent descriptor set.
    // These contracts carry Redis messages, not gRPC endpoints. Envoy's
    // ext_authz server is supplied separately by envoy-types.
    tonic_build::configure()
        .build_client(false)
        .build_server(false)
        .compile(
            &[
                "../proto/iam/session/v1/session.proto",
                "../proto/iam/authentication/v1/session_recovery.proto",
                "../proto/iam/authentication/v1/login.proto",
                "../proto/iam/authentication/v1/social_login.proto",
                "../proto/iam/authentication/v1/social_link.proto",
                "../proto/iam/authentication/v1/runtime_read_authorization.proto",
                "../proto/iam/authentication/v1/role_projection.proto",
                "../proto/iam/authentication/v1/mfa_setup.proto",
                "../proto/hierarchy/zone_catalog/v1/zone_catalog.proto",
                "../proto/iam/trinity/v1/token_verification.proto",
                "../proto/iam/device_presence/v1/device_presence.proto",
                "../proto/notification/activity/v1/user_activity.proto",
            ],
            &["../proto"],
        )?;
    Ok(())
}
