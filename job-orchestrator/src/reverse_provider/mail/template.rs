use crate::config::Config;
use crate::observability::logger::Logger;

/// [COMMENT]: Legacy platform-mail bridge đọc đúng immutable current version trong schema Phase 1.
/// Broker consumer runtime mới sẽ dùng Zone L2 projection, không gọi query này trên hot path.
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

    // [COMMENT]: Join identity -> immutable version để seed verify-account tiếp tục tương thích
    // trong lúc broker mail projection được triển khai ở các phase sau.
    let row = pg_client
        .query_one(
            "SELECT v.subject_template, \
                    CASE WHEN v.html_template <> '' THEN v.html_template ELSE v.text_template END \
             FROM mail.mail_templates AS t \
             JOIN mail.mail_template_versions AS v \
               ON v.template_id = t.id AND v.version = t.current_version \
             WHERE t.id = $1 AND t.status = 'active'",
            &[&template_id],
        )
        .await?;

    let subject: String = row.get(0);
    let body: String = row.get(1);
    Ok((subject, body))
}
