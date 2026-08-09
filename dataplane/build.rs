fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: CI/containers must not silently depend on a host protoc version.
    // The vendored compiler makes the root contract generation reproducible.
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    println!("cargo:rerun-if-changed=../proto/platform_transport.proto");
    println!("cargo:rerun-if-changed=../proto/managed_service.proto");
    println!("cargo:rerun-if-changed=../proto/zone_report.proto");
    // [COMMENT]: Mail delivery dùng fixed JSON envelope từ customer broker; protobuf chỉ còn control-plane contracts.
    prost_build::compile_protos(
        &[
            // [COMMENT]: Contract versioned cho CP desired state ↔ DP broker/runtime/result.
            "../proto/dataplane/mail_runtime.proto",
            "../proto/platform_transport.proto",
            "../proto/dataplane/job_result.proto",
            "../proto/zone_report.proto",
            "../proto/dataplane/storage_job.proto",
            "../proto/dataplane/hypervisor_job.proto",
            // [COMMENT]: Canonical source lives at the monorepo root; local copies are forbidden.
            "../proto/managed_service.proto",
        ],
        &["../proto/dataplane", "../proto"],
    )?;
    Ok(())
}
