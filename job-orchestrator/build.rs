fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("cargo:rerun-if-changed=../proto/job-orchestrator");
    // [COMMENT]: CI/containers must not silently depend on a host protoc version.
    // The vendored compiler makes the root contract generation reproducible.
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    println!("cargo:rerun-if-changed=../proto/security/job_payload/v1/protected_payload.proto");
    println!("cargo:rerun-if-changed=../proto/transport/job/v1");
    println!("cargo:rerun-if-changed=../proto/zone/metadata/v1/zone_metadata.proto");
    println!("cargo:rerun-if-changed=../proto/storage/usage_projection/v1/bucket_sizes.proto");
    println!("cargo:rerun-if-changed=../proto/managedservice/lifecycle/v1/managed_service.proto");
    println!("cargo:rerun-if-changed=../proto/zone/report/v1/zone_report.proto");
    println!("cargo:rerun-if-changed=../proto/notification/job/v1/job_notification.proto");
    println!("cargo:rerun-if-changed=../proto/billing/storage/usage/v1/usage_report.proto");
    println!(
        "cargo:rerun-if-changed=../proto/billing/hypervisor/allocation/v1/allocation_event.proto"
    );
    println!(
        "cargo:rerun-if-changed=../proto/billing/hypervisor/network_usage/v1/usage_report.proto"
    );
    println!("cargo:rerun-if-changed=../proto/billing/mail/accepted_usage/v1/accepted_usage.proto");
    // [COMMENT]: Biên dịch toàn bộ proto files, gồm Billing resource ownership contract.
    prost_build::compile_protos(
        &[
            "../proto/notification/job/v1/job_notification.proto",
            // [COMMENT]: Mail projection/result contracts dùng chung giữa CP, JO và DP.
            "../proto/mail/consumer_lifecycle/v1/consumer_lifecycle.proto",
            "../proto/mail/template_projection/v1/template_projection.proto",
            "../proto/mail/runtime_projection/v1/reconcile.proto",
            "../proto/mail/dispatch/v1/dispatch.proto",
            "../proto/security/job_payload/v1/protected_payload.proto",
            "../proto/transport/job/v1/command.proto",
            "../proto/transport/job/v1/dead_letter.proto",
            "../proto/zone/metadata/v1/zone_metadata.proto",
            "../proto/storage/usage_projection/v1/bucket_sizes.proto",
            "../proto/transport/job/v1/result.proto",
            "../proto/zone/report/v1/zone_report.proto",
            "../proto/storage/bucket_lifecycle/v1/bucket_lifecycle.proto",
            "../proto/storage/credential_lifecycle/v1/credential_lifecycle.proto",
            "../proto/storage/access_session/v1/access_session.proto",
            "../proto/hypervisor/vm_lifecycle/v1/vm_lifecycle.proto",
            "../proto/hypervisor/image_lifecycle/v1/image_lifecycle.proto",
            // [COMMENT]: ResourceOwnershipChangedV1 — contract ownership phía Billing.
            "../proto/billing/ownership/v1/resource_ownership_event.proto",
            "../proto/billing/storage/usage/v1/usage_report.proto",
            "../proto/billing/hypervisor/allocation/v1/allocation_event.proto",
            "../proto/billing/hypervisor/network_usage/v1/usage_report.proto",
            "../proto/billing/mail/accepted_usage/v1/accepted_usage.proto",
            // [COMMENT]: Canonical source lives at the monorepo root; local copies are forbidden.
            "../proto/managedservice/lifecycle/v1/managed_service.proto",
        ],
        &["../proto"],
    )?;

    Ok(())
}
