fn main() -> Result<(), Box<dyn std::error::Error>> {
    // The build script owns the process environment, so setting the vendored
    // compiler path is safe and keeps local/CI generation reproducible.
    unsafe {
        std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    }
    prost_build::compile_protos(
        &[
            "../../proto/cost-manager/engine/storage_usage.proto",
            "../../proto/cost-manager/engine/pricing_event.proto",
            "../../proto/cost-manager/engine/storage_usage_report.proto",
        ],
        &["../../proto/cost-manager/engine"],
    )?;
    Ok(())
}
