fn main() -> Result<(), Box<dyn std::error::Error>> {
    // [COMMENT]: CI/containers must not silently depend on a host protoc version.
    // The vendored compiler makes the root contract generation reproducible.
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    println!("cargo:rerun-if-changed=../proto/security/job_payload/v1/protected_payload.proto");
    println!("cargo:rerun-if-changed=../proto/mail");
    println!("cargo:rerun-if-changed=../proto/hypervisor");
    println!("cargo:rerun-if-changed=../proto/storage");
    println!("cargo:rerun-if-changed=../proto/billing");
    println!("cargo:rerun-if-changed=../proto/transport/job/v1");
    println!("cargo:rerun-if-changed=../proto/managedservice/lifecycle/v1/managed_service.proto");
    println!(
        "cargo:rerun-if-changed=../proto/storage/commercial_admission/v1/zone_projection.proto"
    );
    // [COMMENT]: Mail delivery dùng fixed JSON envelope từ customer broker; protobuf chỉ còn control-plane contracts.
    prost_build::compile_protos(
        &[
            // [COMMENT]: Contract versioned cho CP desired state ↔ DP broker/runtime/result.
            "../proto/mail/consumer_lifecycle/v1/consumer_lifecycle.proto",
            "../proto/mail/template_projection/v1/template_projection.proto",
            "../proto/mail/runtime_projection/v1/reconcile.proto",
            "../proto/mail/dispatch/v1/dispatch.proto",
            "../proto/mail/consumer_drain/v1/zone_runtime_generation.proto",
            "../proto/security/job_payload/v1/protected_payload.proto",
            "../proto/transport/job/v1/command.proto",
            "../proto/transport/job/v1/dead_letter.proto",
            "../proto/transport/job/v1/result.proto",
            "../proto/transport/job/v1/zone_completion_receipt.proto",
            "../proto/storage/bucket_lifecycle/v1/bucket_lifecycle.proto",
            "../proto/storage/credential_lifecycle/v1/credential_lifecycle.proto",
            "../proto/storage/access_session/v1/access_session.proto",
            "../proto/hypervisor/vm_lifecycle/v1/vm_lifecycle.proto",
            "../proto/hypervisor/image_lifecycle/v1/image_lifecycle.proto",
            "../proto/hypervisor/vm_delete/v1/zone_journal.proto",
            "../proto/billing/hypervisor/network_usage/v1/usage_report.proto",
            "../proto/billing/mail/accepted_usage/v1/accepted_usage.proto",
            "../proto/storage/commercial_admission/v1/zone_projection.proto",
            // [COMMENT]: Canonical source lives at the monorepo root; local copies are forbidden.
            "../proto/managedservice/lifecycle/v1/managed_service.proto",
        ],
        &["../proto"],
    )?;
    Ok(())
}
