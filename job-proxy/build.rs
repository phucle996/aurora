fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Biên dịch file proto job_event.proto từ notification-service và mail_job.proto
    prost_build::compile_protos(
        &[
            "../notification-service/proto/job_event.proto",
            "proto/mail_job.proto"
        ],
        &["../notification-service/proto/", "proto/"],
    )?;
    Ok(())
}
