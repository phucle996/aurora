fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [ignoring loop detection]
    // Biên dịch file proto/trinity.proto thành code Rust
    tonic_build::compile_protos("proto/trinity.proto")?;

    // Biên dịch file proto/job_event.proto để giải mã dữ liệu nhị phân từ Redis Stream
    tonic_build::compile_protos("proto/job_event.proto")?;
    Ok(())
}
