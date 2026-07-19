fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Biên dịch các file proto mail_job.proto và job_result.proto
    prost_build::compile_protos(
        &[
            "proto/mail_job.proto",
            // [COMMENT]: Contract versioned cho CP desired state ↔ DP broker/runtime/result.
            "proto/mail_runtime.proto",
            "proto/job_result.proto",
            "proto/zone_report.proto",
            "proto/storage_job.proto",
        ],
        &["proto/"],
    )?;
    Ok(())
}
