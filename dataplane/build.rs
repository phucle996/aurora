fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: CI/containers must not silently depend on a host protoc version.
    // The vendored compiler makes the root contract generation reproducible.
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    println!("cargo:rerun-if-changed=../proto/security/payload.proto");
    println!("cargo:rerun-if-changed=../proto/job-orchestrator/command.proto");
    println!("cargo:rerun-if-changed=../proto/managed_service.proto");
    println!("cargo:rerun-if-changed=../proto/controlplane/storage/v1/storage_admission.proto");
    // [COMMENT]: Mail delivery dùng fixed JSON envelope từ customer broker; protobuf chỉ còn control-plane contracts.
    prost_build::compile_protos(
        &[
            // [COMMENT]: Contract versioned cho CP desired state ↔ DP broker/runtime/result.
            "../proto/dataplane/mail_runtime.proto",
            "../proto/security/payload.proto",
            "../proto/job-orchestrator/command.proto",
            "../proto/dataplane/job_result.proto",
            "../proto/dataplane/storage_job.proto",
            "../proto/dataplane/hypervisor_job.proto",
            "../proto/cost-manager/engine/hypervisor_network_usage_report.proto",
            "../proto/cost-manager/engine/mail_accepted_usage.proto",
            "../proto/controlplane/storage/v1/storage_admission.proto",
            // [COMMENT]: Canonical source lives at the monorepo root; local copies are forbidden.
            "../proto/managed_service.proto",
        ],
        &[
            "../proto/dataplane",
            "../proto",
            "../proto/cost-manager/engine",
        ],
    )?;
    Ok(())
}
