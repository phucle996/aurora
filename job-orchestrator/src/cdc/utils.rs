/// Helper phân tích URL kết nối PostgreSQL chuẩn sử dụng thư viện tokio-postgres Config parser.
/// Điều này giúp trích xuất các trường thông tin kết nối đúng đắn kể cả khi mật khẩu có chứa các ký tự đặc biệt.
pub fn parse_pg_config(url: &str) -> Result<(String, u16, String, String, String), String> {
    let pg_config = url
        .parse::<tokio_postgres::Config>()
        .map_err(|e| e.to_string())?;

    // Lấy host đầu tiên trong danh sách hosts
    let host = match pg_config
        .get_hosts()
        .first()
        .ok_or_else(|| "No host found in database URL".to_string())?
    {
        tokio_postgres::config::Host::Tcp(h) => h.clone(),
        tokio_postgres::config::Host::Unix(p) => p.to_string_lossy().into_owned(),
    };

    // Lấy port (mặc định 5432)
    let port = pg_config.get_ports().first().copied().unwrap_or(5432);

    // Lấy thông tin user
    let user = pg_config.get_user().unwrap_or("").to_string();

    // Lấy password (convert từ nhị phân sang string UTF-8)
    let password = pg_config
        .get_password()
        .map(|p| String::from_utf8_lossy(p).into_owned())
        .unwrap_or_default();

    // Lấy tên database
    let db = pg_config.get_dbname().unwrap_or("").to_string();

    Ok((host, port, user, password, db))
}
