use std::path::Path;
use std::str::FromStr;
use sqlx::postgres::{PgPoolOptions, PgConnectOptions, PgSslMode, PgPool};
use crate::config::Config;

// [COMMENT]: Khởi tạo Postgres Connection Pool với đầy đủ cấu hình bảo mật TLS/mTLS
pub async fn init_pg_pool(config: &Config) -> Result<PgPool, sqlx::Error> {
    let mut pg_conn_options = PgConnectOptions::from_str(&config.database_url)?;

    // [COMMENT]: Cấu hình SSL Mode tương ứng với môi trường chạy (disable, verify-ca, verify-full,...)
    let pg_ssl_mode = match config.pg_ssl_mode.as_str() {
        "disable" => PgSslMode::Disable,
        "allow" => PgSslMode::Allow,
        "prefer" => PgSslMode::Prefer,
        "require" => PgSslMode::Require,
        "verify-ca" => PgSslMode::VerifyCa,
        "verify-full" => PgSslMode::VerifyFull,
        _ => PgSslMode::Prefer,
    };
    pg_conn_options = pg_conn_options.ssl_mode(pg_ssl_mode);

    // [COMMENT]: Nạp Certificate Authority (CA) root cert để xác thực chứng chỉ từ Server Postgres
    if let Some(ca_path) = &config.pg_ssl_root_cert {
        pg_conn_options = pg_conn_options.ssl_root_cert(Path::new(ca_path));
    }
    
    // [COMMENT]: Nạp Client Certificate và Private Key dùng cho xác thực hai chiều mTLS
    if let (Some(cert_path), Some(key_path)) = (&config.pg_ssl_client_cert, &config.pg_ssl_client_key) {
        pg_conn_options = pg_conn_options.ssl_client_cert(Path::new(cert_path));
        pg_conn_options = pg_conn_options.ssl_client_key(Path::new(key_path));
    }

    // [COMMENT]: Khởi tạo pool kết nối Postgres với các tham số tối đa/tối thiểu kết nối và thời gian chờ
    PgPoolOptions::new()
        .max_connections(config.pg_max_connections)
        .min_connections(config.pg_min_connections)
        .acquire_timeout(config.pg_acquire_timeout)
        .connect_with(pg_conn_options)
        .await
}
