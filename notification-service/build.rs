fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [ignoring loop detection]
    // Biên dịch file proto/auth.proto thành code Rust gRPC
    tonic_build::compile_protos("proto/auth.proto")?;
    
    // Biên dịch file proto/job_event.proto để giải mã dữ liệu nhị phân từ Redis Stream
    tonic_build::compile_protos("proto/job_event.proto")?;
    Ok(())
}
