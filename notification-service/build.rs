fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [ignoring loop detection]
    // Compile Notification-owned sources from the root proto registry.
    tonic_build::compile_protos("../proto/notification-service/trinity.proto")?;

    // Compile job_event.proto to decode Redis Stream binary payloads.
    tonic_build::compile_protos("../proto/notification-service/job_event.proto")?;

    // User activity is a durable self-history contract. It is intentionally
    // independent from job notification and runtime soft-state contracts.
    tonic_build::compile_protos("../proto/notification-service/user_activity.proto")?;
    Ok(())
}
