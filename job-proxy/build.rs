fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Biên dịch file proto job_event.proto và mail_job.proto cục bộ
    prost_build::compile_protos(
        &[
            "proto/job_event.proto",
            "proto/mail_job.proto"
        ],
        &["proto/"],
    )?;
    Ok(())
}
