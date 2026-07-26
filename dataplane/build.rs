fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: Mail delivery dùng fixed JSON envelope từ customer broker; protobuf chỉ còn control-plane contracts.
    prost_build::compile_protos(
        &[
            // [COMMENT]: Contract versioned cho CP desired state ↔ DP broker/runtime/result.
            "proto/mail_runtime.proto",
            "proto/platform_transport.proto",
            "proto/job_result.proto",
            "proto/zone_report.proto",
            "proto/storage_job.proto",
            "proto/hypervisor_job.proto",
        ],
        &["proto/"],
    )?;
    Ok(())
}
