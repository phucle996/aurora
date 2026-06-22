fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Biên dịch cả hai file proto trong cùng một phiên để tránh bị ghi đè dữ liệu sinh ra trong OUT_DIR
    tonic_build::configure().compile(
        &["proto/session.proto", "proto/auth.proto"],
        &["proto"],
    )?;
    Ok(())
}
