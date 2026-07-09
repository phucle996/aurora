fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Biên dịch cả hai file proto trong cùng một phiên để tránh bị ghi đè dữ liệu sinh ra trong OUT_DIR
    // [COMMENT]: Biên dịch các file proto: device.proto (DeviceService), auth.proto, zone.proto
    // Lưu ý: session.proto đã được đổi tên thành device.proto
    tonic_build::configure().compile(
        &["proto/device.proto", "proto/auth.proto", "proto/zone.proto", "proto/trinity.proto"],
        &["proto"],
    )?;
    Ok(())
}
