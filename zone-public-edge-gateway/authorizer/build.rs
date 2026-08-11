fn main() -> Result<(), Box<dyn std::error::Error>> {
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    println!("cargo:rerun-if-changed=../../proto/zone/transfer_ticket.proto");
    prost_build::compile_protos(
        &["../../proto/zone/transfer_ticket.proto"],
        &["../../proto"],
    )?;
    Ok(())
}
