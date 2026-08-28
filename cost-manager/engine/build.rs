fn main() -> Result<(), Box<dyn std::error::Error>> {
    // The build script owns the process environment, so setting the vendored
    // compiler path is safe and keeps local/CI generation reproducible.
    unsafe {
        std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    }
    prost_build::compile_protos(
        &[
            "../../proto/billing/storage/usage/v1/usage.proto",
            "../../proto/billing/pricing/v1/pricing_event.proto",
            "../../proto/billing/storage/usage/v1/usage_report.proto",
            "../../proto/billing/hypervisor/allocation/v1/allocation_event.proto",
            "../../proto/billing/hypervisor/network_usage/v1/usage_report.proto",
            "../../proto/billing/mail/accepted_usage/v1/accepted_usage.proto",
        ],
        &["../../proto"],
    )?;
    Ok(())
}
