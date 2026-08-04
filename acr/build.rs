fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Compile service-local contracts and the canonical cross-service IAM
    // contract together so tonic emits one coherent descriptor set.
    tonic_build::configure().compile(
        &[
            "proto/device.proto",
            "../contracts/proto/iam_auth.proto",
            "proto/zone.proto",
            "proto/trinity.proto",
            "proto/device_presence.proto",
            "proto/user_activity.proto",
            "proto/storage_access.proto",
        ],
        &["proto", "../contracts/proto"],
    )?;
    Ok(())
}
