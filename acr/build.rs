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
                "../proto/acr/device.proto",
                "../proto/iam_auth.proto",
                "../proto/acr/zone.proto",
                "../proto/acr/trinity.proto",
                "../proto/acr/device_presence.proto",
                "../proto/acr/user_activity.proto",
            ],
            &["../proto/acr", "../proto"],
        )?;
    Ok(())
}
