fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Biên dịch các file proto mail_job.proto và job_result.proto
    prost_build::compile_protos(
        &["proto/mail_job.proto", "proto/job_result.proto"],
        &["proto/"],
    )?;
    Ok(())
}
