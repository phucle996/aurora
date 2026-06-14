use crate::config::Config;
use crate::observability::logger::Logger;
use tokio_postgres::NoTls;
use std::time::Duration;

/// Khởi tạo hạ tầng Logical Replication (Publication và Replication Slot) cho PostgreSQL.
/// Hàm này được thiết kế để chỉ chạy một lần duy nhất lúc khởi chạy ứng dụng (main.rs).
/// Có cơ chế tự động thử lại kết nối (reconnect) và bỏ qua tranh chấp tài nguyên trên môi trường High-Availability (HA).
pub async fn setup_replication_infrastructure(config: &Config) -> Result<(), Box<dyn std::error::Error>> {
    Logger::sys_info(
        "cdc.setup",
        "CdcSetup: Bắt đầu kiểm tra và khởi tạo hạ tầng Logical Replication...",
    );

    loop {
        // Thực thi kiểm tra kết nối và cấu hình publication/slot
        match try_setup(config).await {
            Ok(_) => {
                Logger::sys_info(
                    "cdc.setup",
                    "CdcSetup: Toàn bộ hạ tầng Logical Replication đã được thiết lập thành công.",
                );
                return Ok(());
            }
            Err(e) => {
                Logger::sys_warn(
                    "cdc.setup",
                    "CdcSetup: Không thể khởi tạo hạ tầng replication, sẽ thử lại kết nối PostgreSQL sau 5 giây...",
                    &e.to_string(),
                );
                // Đợi 5 giây trước khi thực hiện thử lại kết nối tiếp theo
                tokio::time::sleep(Duration::from_secs(5)).await;
            }
        }
    }
}

/// Thực hiện kết nối PostgreSQL và thiết lập Publication + Replication Slot
async fn try_setup(config: &Config) -> Result<(), Box<dyn std::error::Error>> {
    // 1. Thực hiện kết nối PostgreSQL thông thường để cấu hình hạ tầng
    let (client, connection) = tokio_postgres::connect(&config.database_url, NoTls).await?;
    
    // Spawn kết nối chạy ngầm để trao đổi các thông điệp giao thức của tokio-postgres
    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "cdc.setup.postgres",
                "CdcSetup: Gặp lỗi trong luồng kết nối chạy ngầm của PostgreSQL",
                &e.to_string(),
            );
        }
    });

    // Đảm bảo search path nằm ở mail schema
    client.execute("SET search_path TO mail, public", &[]).await?;

    // 2. Đảm bảo Publication tồn tại cho bảng mail_outbox_records
    let pub_check = client.query(
        "SELECT 1 FROM pg_publication WHERE pubname = $1",
        &[&config.publication_name],
    ).await?;

    if pub_check.is_empty() {
        Logger::sys_info(
            "cdc.setup",
            &format!("CdcSetup: Tạo mới publication '{}' cho bảng mail_outbox_records...", config.publication_name),
        );
        let create_pub_sql = format!(
            "CREATE PUBLICATION {} FOR TABLE mail_outbox_records",
            config.publication_name
        );
        if let Err(err) = client.execute(&create_pub_sql, &[]).await {
            let err_str = err.to_string();
            // Bắt lỗi trùng lặp đối tượng (duplicate_object / 42710) phòng trường hợp tranh chấp HA khi nhiều instance chạy song song
            if err_str.contains("already exists") || err_str.contains("42710") {
                Logger::sys_warn(
                    "cdc.setup",
                    "CdcSetup: Publication đã tồn tại (bỏ qua do tranh chấp HA)",
                    &err_str,
                );
            } else {
                return Err(err.into());
            }
        }
    } else {
        // Kiểm tra xem bảng mail_outbox_records đã nằm trong publication chưa
        let table_check = client.query(
            "SELECT 1 FROM pg_publication_tables WHERE pubname = $1 AND tablename = 'mail_outbox_records'",
            &[&config.publication_name],
        ).await?;
        if table_check.is_empty() {
            Logger::sys_info(
                "cdc.setup",
                &format!("CdcSetup: Thêm bảng mail_outbox_records vào publication '{}'...", config.publication_name),
            );
            let alter_pub_sql = format!(
                "ALTER PUBLICATION {} ADD TABLE mail_outbox_records",
                config.publication_name
            );
            if let Err(err) = client.execute(&alter_pub_sql, &[]).await {
                let err_str = err.to_string();
                if err_str.contains("already is member") || err_str.contains("duplicate") {
                    Logger::sys_warn(
                        "cdc.setup",
                        "CdcSetup: Bảng đã thuộc publication (bỏ qua do tranh chấp)",
                        &err_str,
                    );
                } else {
                    return Err(err.into());
                }
            }
        }
    }

    // 3. Đảm bảo Replication Slot tồn tại với plugin pgoutput
    let slot_check = client.query(
        "SELECT 1 FROM pg_replication_slots WHERE slot_name = $1 AND plugin = 'pgoutput'",
        &[&config.slot_name],
    ).await?;

    if slot_check.is_empty() {
        Logger::sys_info(
            "cdc.setup",
            &format!("CdcSetup: Tạo mới logical replication slot '{}' với plugin pgoutput...", config.slot_name),
        );
        let create_slot_sql = format!(
            "SELECT lsn FROM pg_create_logical_replication_slot('{}', 'pgoutput')",
            config.slot_name
        );
        if let Err(err) = client.query(&create_slot_sql, &[]).await {
            let err_str = err.to_string();
            // Bắt lỗi trùng lặp đối tượng (duplicate_object / 42710) phòng trường hợp tranh chấp HA khi nhiều instance chạy song song
            if err_str.contains("already exists") || err_str.contains("42710") {
                Logger::sys_warn(
                    "cdc.setup",
                    "CdcSetup: Replication slot đã tồn tại (bỏ qua do tranh chấp HA)",
                    &err_str,
                );
            } else {
                return Err(err.into());
            }
        }
    }

    Ok(())
}
