fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: Biên dịch toàn bộ proto files, gồm Billing resource ownership contract.
    prost_build::compile_protos(
        &[
            "proto/job_event.proto",
            // [COMMENT]: Mail projection/result contract dùng chung giữa CP, JO và DP.
            "proto/mail_runtime.proto",
            "proto/platform_transport.proto",
            "proto/job_result.proto",
            "proto/zone_report.proto",
            "proto/storage_job.proto",
            "proto/hypervisor_job.proto",
            // [COMMENT]: ResourceOwnershipChangedV1 — contract ownership phía Billing.
            "proto/resource_ownership.proto",
        ],
        &["proto/"],
    )?;

    Ok(())
}
