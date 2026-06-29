use crate::config::Config;

/// Nhận yêu cầu tài nguyên, định tuyến và trả về kết quả JSON tương ứng
pub async fn dispatch_request(
    config: &Config,
    template_id: &str,
) -> Result<serde_json::Value, Box<dyn std::error::Error>> {
    // Định tuyến nghiệp vụ lấy email template
    let (subject, body) = super::mail::template::fetch_template(config, template_id).await?;

    Ok(serde_json::json!({
        "subject": subject,
        "content": body
    }))
}
