fn main() -> Result<(), Box<dyn std::error::Error>> {
    prost_build::compile_protos(
        &[
            "../../proto/cost-manager/engine/storage_usage.proto",
            "../../proto/cost-manager/engine/pricing_event.proto",
        ],
        &["../../proto/cost-manager/engine"],
    )?;
    Ok(())
}
