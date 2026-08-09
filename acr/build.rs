fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Compile ACR-owned contracts and the canonical cross-service IAM contract
    // from the root proto registry so tonic emits one coherent descriptor set.
    tonic_build::configure().compile(
        &[
            "../proto/acr/device.proto",
            "../proto/iam_auth.proto",
            "../proto/acr/zone.proto",
            "../proto/acr/trinity.proto",
            "../proto/acr/device_presence.proto",
            "../proto/acr/user_activity.proto",
            "../proto/acr/storage_access.proto",
        ],
        &["../proto/acr", "../proto"],
    )?;
    Ok(())
}
