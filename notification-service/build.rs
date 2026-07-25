fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [ignoring loop detection]
    // Biên dịch file proto/trinity.proto thành code Rust
    tonic_build::compile_protos("proto/trinity.proto")?;

    // Biên dịch file proto/job_event.proto để giải mã dữ liệu nhị phân từ Redis Stream
    tonic_build::compile_protos("proto/job_event.proto")?;

    // User activity is a durable self-history contract. It is intentionally
    // independent from job notification and runtime soft-state contracts.
    tonic_build::compile_protos("proto/user_activity.proto")?;
    Ok(())
}
