fn main() -> Result<(), Box<dyn std::error::Error>> {
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    println!("cargo:rerun-if-changed=../proto/zone/transfer_ticket.proto");
    println!("cargo:rerun-if-changed=../proto/cost-manager/engine/storage_usage_report.proto");
    println!("cargo:rerun-if-changed=../proto/security/payload.proto");
    println!("cargo:rerun-if-changed=../proto/job-orchestrator/command.proto");
    println!("cargo:rerun-if-changed=../proto/zone/zone_metadata.proto");
    println!("cargo:rerun-if-changed=../proto/storage/storage_sizes.proto");
    println!("cargo:rerun-if-changed=../proto/zone_report.proto");
    println!("cargo:rerun-if-changed=../proto/controlplane/storage/v1/storage_admission.proto");
    prost_build::compile_protos(
        &[
            "../proto/zone/transfer_ticket.proto",
            "../proto/cost-manager/engine/storage_usage_report.proto",
            "../proto/security/payload.proto",
            "../proto/job-orchestrator/command.proto",
            "../proto/zone/zone_metadata.proto",
            "../proto/storage/storage_sizes.proto",
            "../proto/zone_report.proto",
            "../proto/controlplane/storage/v1/storage_admission.proto",
        ],
        &["../proto", "../proto/cost-manager/engine"],
    )?;
    Ok(())
}
