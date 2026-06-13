fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Biên dịch file proto mail_job.proto
    prost_build::compile_protos(
        &["proto/mail_job.proto"],
        &["proto/"],
    )?;
    Ok(())
}
