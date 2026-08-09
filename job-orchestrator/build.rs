fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: CI/containers must not silently depend on a host protoc version.
    // The vendored compiler makes the root contract generation reproducible.
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    println!("cargo:rerun-if-changed=../proto/platform_transport.proto");
    println!("cargo:rerun-if-changed=../proto/managed_service.proto");
    println!("cargo:rerun-if-changed=../proto/zone_report.proto");
    println!("cargo:rerun-if-changed=../proto/job-orchestrator/job_event.proto");
    // [COMMENT]: Biên dịch toàn bộ proto files, gồm Billing resource ownership contract.
    prost_build::compile_protos(
        &[
            "../proto/job-orchestrator/job_event.proto",
            // [COMMENT]: Mail projection/result contract dùng chung giữa CP, JO và DP.
            "../proto/job-orchestrator/mail_runtime.proto",
            "../proto/platform_transport.proto",
            "../proto/job-orchestrator/job_result.proto",
            "../proto/zone_report.proto",
            "../proto/job-orchestrator/storage_job.proto",
            "../proto/job-orchestrator/hypervisor_job.proto",
            // [COMMENT]: ResourceOwnershipChangedV1 — contract ownership phía Billing.
            "../proto/job-orchestrator/resource_ownership.proto",
            // [COMMENT]: Canonical source lives at the monorepo root; local copies are forbidden.
            "../proto/managed_service.proto",
        ],
        &["../proto/job-orchestrator", "../proto"],
    )?;

    Ok(())
}
