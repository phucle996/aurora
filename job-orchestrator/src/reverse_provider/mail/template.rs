use crate::config::Config;
use crate::observability::logger::Logger;

/// Truy vấn trực tiếp từ bảng mail.mail_templates các thông tin subject và body
pub async fn fetch_template(
    config: &Config,
    template_id: &str,
) -> Result<(String, String), Box<dyn std::error::Error>> {
    use tokio_postgres::NoTls;

    // Thiết lập kết nối không TLS đến PostgreSQL
    let (pg_client, connection) = tokio_postgres::connect(&config.database_url, NoTls).await?;

    tokio::spawn(async move {
        if let Err(e) = connection.await {
            Logger::sys_error(
                "reverse_provider.mail_template",
                "ReverseProvider: Lỗi kết nối Postgres khi fetch template",
                &e.to_string(),
            );
        }
    });

    // Query thông tin subject và body của template từ schema mail
    let row = pg_client
        .query_one(
            "SELECT subject, body FROM mail.mail_templates WHERE id = $1",
            &[&template_id],
        )
        .await?;

    let subject: String = row.get(0);
    let body: String = row.get(1);
    Ok((subject, body))
}
