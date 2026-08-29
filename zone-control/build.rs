fn main() -> Result<(), Box<dyn std::error::Error>> {
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    println!("cargo:rerun-if-changed=../proto/zone/transfer/v1/transfer_ticket.proto");
    println!("cargo:rerun-if-changed=../proto/billing/storage/usage/v1/usage_report.proto");
    println!("cargo:rerun-if-changed=../proto/security/job_payload/v1/protected_payload.proto");
    println!("cargo:rerun-if-changed=../proto/transport/job/v1");
    println!("cargo:rerun-if-changed=../proto/zone/metadata/v1/zone_metadata.proto");
    println!("cargo:rerun-if-changed=../proto/storage/usage_projection/v1/bucket_sizes.proto");
    println!("cargo:rerun-if-changed=../proto/zone/report/v1/zone_report.proto");
    prost_build::compile_protos(
        &[
            "../proto/zone/transfer/v1/transfer_ticket.proto",
            "../proto/billing/storage/usage/v1/usage_report.proto",
            "../proto/security/job_payload/v1/protected_payload.proto",
            "../proto/transport/job/v1/command.proto",
            "../proto/transport/job/v1/dead_letter.proto",
            "../proto/zone/metadata/v1/zone_metadata.proto",
            "../proto/storage/usage_projection/v1/bucket_sizes.proto",
            "../proto/zone/report/v1/zone_report.proto",
        ],
        &["../proto"],
    )?;
    Ok(())
}
