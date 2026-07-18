fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: Biên dịch toàn bộ proto files — storage_job, resource_lifecycle và job lifecycle
    prost_build::compile_protos(
        &[
            "proto/job_event.proto",
            "proto/mail_job.proto",
            "proto/job_result.proto",
            "proto/zone_report.proto",
            "proto/storage_job.proto",
            // [COMMENT]: ResourceLifecycleEventV1 — contract duy nhất cho billing domain events
            "proto/resource_lifecycle.proto",
        ],
        &["proto/"],
    )?;

    Ok(())
}
