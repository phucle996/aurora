fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: CI/containers must not silently depend on a host protoc version.
    // The vendored compiler makes the root contract generation reproducible.
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    println!("cargo:rerun-if-changed=../contracts/proto/platform_transport.proto");
    println!("cargo:rerun-if-changed=../contracts/proto/managed_service.proto");
    println!("cargo:rerun-if-changed=../contracts/proto/zone_report.proto");
    // [COMMENT]: Biên dịch toàn bộ proto files, gồm Billing resource ownership contract.
    prost_build::compile_protos(
        &[
            "proto/job_event.proto",
            // [COMMENT]: Mail projection/result contract dùng chung giữa CP, JO và DP.
            "proto/mail_runtime.proto",
            "../contracts/proto/platform_transport.proto",
            "proto/job_result.proto",
            "../contracts/proto/zone_report.proto",
            "proto/storage_job.proto",
            "proto/hypervisor_job.proto",
            // [COMMENT]: ResourceOwnershipChangedV1 — contract ownership phía Billing.
            "proto/resource_ownership.proto",
            // [COMMENT]: Canonical source lives at the monorepo root; local copies are forbidden.
            "../contracts/proto/managed_service.proto",
        ],
        &["proto/", "../contracts/proto"],
    )?;

    Ok(())
}
