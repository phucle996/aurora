fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("cargo:rerun-if-changed=../proto/job-orchestrator");
    // [COMMENT]: CI/containers must not silently depend on a host protoc version.
    // The vendored compiler makes the root contract generation reproducible.
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    println!("cargo:rerun-if-changed=../proto/security/payload.proto");
    println!("cargo:rerun-if-changed=../proto/job-orchestrator/command.proto");
    println!("cargo:rerun-if-changed=../proto/zone/zone_metadata.proto");
    println!("cargo:rerun-if-changed=../proto/storage/storage_sizes.proto");
    println!("cargo:rerun-if-changed=../proto/managed_service.proto");
    println!("cargo:rerun-if-changed=../proto/zone_report.proto");
    println!("cargo:rerun-if-changed=../proto/job-orchestrator/job_event.proto");
    println!("cargo:rerun-if-changed=../proto/cost-manager/engine/storage_usage_report.proto");
    println!(
        "cargo:rerun-if-changed=../proto/cost-manager/engine/hypervisor_allocation_event.proto"
    );
    println!(
        "cargo:rerun-if-changed=../proto/cost-manager/engine/hypervisor_network_usage_report.proto"
    );
    println!("cargo:rerun-if-changed=../proto/cost-manager/engine/mail_accepted_usage.proto");
    // [COMMENT]: Biên dịch toàn bộ proto files, gồm Billing resource ownership contract.
    prost_build::compile_protos(
        &[
            "../proto/job-orchestrator/job_event.proto",
            // [COMMENT]: Mail projection/result contract dùng chung giữa CP, JO và DP.
            "../proto/job-orchestrator/mail_runtime.proto",
            "../proto/security/payload.proto",
            "../proto/job-orchestrator/command.proto",
            "../proto/zone/zone_metadata.proto",
            "../proto/storage/storage_sizes.proto",
            "../proto/job-orchestrator/job_result.proto",
            "../proto/zone_report.proto",
            "../proto/job-orchestrator/storage_job.proto",
            "../proto/job-orchestrator/hypervisor_job.proto",
            // [COMMENT]: ResourceOwnershipChangedV1 — contract ownership phía Billing.
            "../proto/job-orchestrator/resource_ownership.proto",
            "../proto/cost-manager/engine/storage_usage_report.proto",
            "../proto/cost-manager/engine/hypervisor_allocation_event.proto",
            "../proto/cost-manager/engine/hypervisor_network_usage_report.proto",
            "../proto/cost-manager/engine/mail_accepted_usage.proto",
            // [COMMENT]: Canonical source lives at the monorepo root; local copies are forbidden.
            "../proto/managed_service.proto",
        ],
        &[
            "../proto/job-orchestrator",
            "../proto",
            "../proto/cost-manager/engine",
        ],
    )?;

    Ok(())
}
