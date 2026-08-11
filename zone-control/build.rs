fn main() -> Result<(), Box<dyn std::error::Error>> {
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    println!("cargo:rerun-if-changed=../proto/zone/transfer_ticket.proto");
    println!("cargo:rerun-if-changed=../proto/cost-manager/engine/storage_usage_report.proto");
    println!("cargo:rerun-if-changed=../proto/platform_transport.proto");
    println!("cargo:rerun-if-changed=../proto/zone_report.proto");
    prost_build::compile_protos(
        &[
            "../proto/zone/transfer_ticket.proto",
            "../proto/cost-manager/engine/storage_usage_report.proto",
            "../proto/platform_transport.proto",
            "../proto/zone_report.proto",
        ],
        &["../proto", "../proto/cost-manager/engine"],
    )?;
    Ok(())
}
