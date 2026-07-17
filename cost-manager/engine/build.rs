fn main() -> Result<(), Box<dyn std::error::Error>> {
    prost_build::compile_protos(
        &["proto/storage_usage.proto"],
        &["proto/"],
    )?;
    Ok(())
}
