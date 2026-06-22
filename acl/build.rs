fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Biên dịch file proto session.proto cho SessionService gRPC
    tonic_build::compile_protos("proto/session.proto")?;
    Ok(())
}
