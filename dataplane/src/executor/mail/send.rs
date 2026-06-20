use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::infra::redis::RedisClientManager;
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;
use std::sync::Arc;

pub mod mail_proto {
    include!(concat!(env!("OUT_DIR"), "/mail.rs"));
}

/// ============================================================================
/// 📂 MODULE: executor/mail/send.rs - BỘ THỰC THI GỬI EMAIL TRỰC TIẾP QUA STALWART
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Đắp thông tin và gửi email trực tiếp đến Stalwart Cluster qua giao thức LMTP (cổng 24).
///   - Tích hợp cơ chế tự động thử lại (retry) tối đa 3 lần nếu gặp sự cố mạng tạm thời.
///   - Loại bỏ hoàn toàn sự phụ thuộc vào SMTP Gateway và Endpoint Registry cũ.
///

// Cấu trúc bộ thực thi nhiệm vụ gửi mail với các dependency cần thiết
pub struct MailSendExecutor {
    _redis_mgr: Arc<RedisClientManager>,
    _zone_id: String,
}

impl MailSendExecutor {
    // Khởi tạo một đối tượng MailSendExecutor mới
    pub fn new(
        redis_mgr: Arc<RedisClientManager>,
        zone_id: String,
    ) -> Self {
        Self {
            _redis_mgr: redis_mgr,
            _zone_id: zone_id,
        }
    }
}

#[async_trait]
impl Executor for MailSendExecutor {
    // Thực thi nghiệp vụ gửi mail transactional
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        // 1. Giải mã email config từ protobuf payload
        let mail_config = match mail_proto::SendMailConfig::decode(payload.payload.as_slice()) {
            Ok(c) => c,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to decode SendMailConfig: {}",
                    e
                )));
            }
        };

        // Trích xuất sender từ template_variables hoặc mặc định
        let sender_addr = mail_config.template_variables.get("from")
            .or_else(|| mail_config.template_variables.get("sender"))
            .map(|s| s.as_str())
            .unwrap_or("noreply@aurora.system");
        let sender_mailbox: lettre::message::Mailbox = sender_addr.parse().unwrap_or_else(|_| "noreply@aurora.system".parse().unwrap());

        // 2. Dựng email message bằng lettre để lấy định dạng MIME chuẩn
        let email = match lettre::Message::builder()
            .from(sender_mailbox.clone())
            .to(mail_config.to.parse().map_err(|e| ExecutorError::ExecutionFailed(format!("Invalid recipient address: {}", e)))?)
            .subject(mail_config.subject.clone())
            .header(lettre::message::header::ContentType::TEXT_HTML)
            .body(mail_config.body_html.clone())
        {
            Ok(m) => m,
            Err(e) => {
                return Err(ExecutorError::ExecutionFailed(format!(
                    "Failed to construct MIME Message for LMTP stream: {}",
                    e
                )));
            }
        };

        let email_bytes = email.formatted();
        let stuffed_bytes = dot_stuffing(&email_bytes);

        // 3. Đọc thông tin kết nối Stalwart từ biến môi trường (Cloud Native Defaults)
        let host = std::env::var("STALWART_LMTP_HOST").unwrap_or_else(|_| "stalwart-mail".to_string());
        let port = std::env::var("STALWART_LMTP_PORT")
            .ok()
            .and_then(|p| p.parse::<u16>().ok())
            .unwrap_or(24);

        Logger::sys_info(
            "executor.mail.send",
            &format!("Routing via Stalwart LMTP to {}:{}...", host, port),
        );

        // 4. Thử kết nối và gửi trực tiếp qua LMTP (retry tối đa 3 lần để đảm bảo HA)
        let mut attempts = 0;
        let mut last_error = String::new();
        while attempts < 3 {
            match send_via_lmtp(&host, port, sender_addr, &mail_config.to, &stuffed_bytes).await {
                Ok(success_msg) => {
                    return Ok(ExecutionResult {
                        message: success_msg,
                    });
                }
                Err(e) => {
                    Logger::sys_warn(
                        "executor.mail.send",
                        &format!("LMTP delivery attempt {} failed: {}", attempts + 1, e),
                        "LMTP_ATTEMPT_FAILED",
                    );
                    last_error = e;
                    attempts += 1;
                    if attempts < 3 {
                        tokio::time::sleep(tokio::time::Duration::from_millis(500)).await;
                    }
                }
            }
        }

        Err(ExecutorError::ExecutionFailed(format!(
            "Failed to send email to Stalwart after 3 attempts. Last error: {}",
            last_error
        )))
    }
}

/// Thực hiện cơ chế dot-stuffing cho giao thức LMTP/SMTP (thêm dấu chấm nếu dòng bắt đầu bằng dấu chấm)
fn dot_stuffing(content: &[u8]) -> Vec<u8> {
    let mut stuffed = Vec::new();
    let mut at_line_start = true;
    for &b in content {
        if at_line_start && b == b'.' {
            stuffed.push(b'.');
        }
        stuffed.push(b);
        at_line_start = b == b'\n';
    }
    stuffed
}

/// Gửi email qua kết nối LMTP (cổng 24) tới Stalwart Mail Server
async fn send_via_lmtp(
    host: &str,
    port: u16,
    sender: &str,
    recipient: &str,
    email_bytes: &[u8],
) -> Result<String, String> {
    use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
    use tokio::net::TcpStream;

    // 1. Kết nối TCP tới LMTP Server
    let stream = TcpStream::connect(format!("{}:{}", host, port))
        .await
        .map_err(|e| format!("Failed to connect to LMTP server {}:{}: {}", host, port, e))?;

    let mut reader = BufReader::new(stream);

    // Đọc dòng chào mừng (e.g. "220 ...")
    let mut line = String::new();
    reader
        .read_line(&mut line)
        .await
        .map_err(|e| e.to_string())?;
    if !line.starts_with("220") {
        return Err(format!("Invalid LMTP welcome response: {}", line.trim()));
    }

    // 2. Gửi lệnh LHLO (LMTP HELO)
    line.clear();
    let hostname = crate::config::get_node_hostname();
    reader
        .get_mut()
        .write_all(format!("LHLO {}\r\n", hostname).as_bytes())
        .await
        .map_err(|e| e.to_string())?;
    reader.get_mut().flush().await.map_err(|e| e.to_string())?;

    // Đọc phản hồi LHLO (250 OK, có thể nhiều dòng)
    loop {
        line.clear();
        reader
            .read_line(&mut line)
            .await
            .map_err(|e| e.to_string())?;
        if line.starts_with("250 ") {
            break;
        } else if !line.starts_with("250-") {
            return Err(format!("LHLO handshake failed: {}", line.trim()));
        }
    }

    // 3. Gửi lệnh MAIL FROM
    line.clear();
    reader
        .get_mut()
        .write_all(format!("MAIL FROM:<{}>\r\n", sender).as_bytes())
        .await
        .map_err(|e| e.to_string())?;
    reader.get_mut().flush().await.map_err(|e| e.to_string())?;
    reader
        .read_line(&mut line)
        .await
        .map_err(|e| e.to_string())?;
    if !line.starts_with("250") {
        return Err(format!("MAIL FROM rejected: {}", line.trim()));
    }

    // 4. Gửi lệnh RCPT TO
    line.clear();
    reader
        .get_mut()
        .write_all(format!("RCPT TO:<{}>\r\n", recipient).as_bytes())
        .await
        .map_err(|e| e.to_string())?;
    reader.get_mut().flush().await.map_err(|e| e.to_string())?;
    reader
        .read_line(&mut line)
        .await
        .map_err(|e| e.to_string())?;
    if !line.starts_with("250") {
        return Err(format!("RCPT TO rejected: {}", line.trim()));
    }

    // 5. Gửi lệnh DATA
    line.clear();
    reader
        .get_mut()
        .write_all(b"DATA\r\n")
        .await
        .map_err(|e| e.to_string())?;
    reader.get_mut().flush().await.map_err(|e| e.to_string())?;
    reader
        .read_line(&mut line)
        .await
        .map_err(|e| e.to_string())?;
    if !line.starts_with("354") {
        return Err(format!("DATA command rejected: {}", line.trim()));
    }

    // 6. Gửi dữ liệu email (MIME) thô
    reader
        .get_mut()
        .write_all(email_bytes)
        .await
        .map_err(|e| e.to_string())?;
    // Đảm bảo kết thúc DATA block bằng CRLF.CRLF
    if !email_bytes.ends_with(b"\r\n") {
        reader
            .get_mut()
            .write_all(b"\r\n")
            .await
            .map_err(|e| e.to_string())?;
    }
    reader
        .get_mut()
        .write_all(b".\r\n")
        .await
        .map_err(|e| e.to_string())?;
    reader.get_mut().flush().await.map_err(|e| e.to_string())?;

    // Đọc phản hồi sau DATA (250 OK cho mỗi recipient)
    line.clear();
    reader
        .read_line(&mut line)
        .await
        .map_err(|e| e.to_string())?;
    if !line.starts_with("250") {
        return Err(format!("Delivery failed after DATA input: {}", line.trim()));
    }

    // 7. Gửi lệnh QUIT
    let _ = reader.get_mut().write_all(b"QUIT\r\n").await;
    let _ = reader.get_mut().flush().await;

    Ok(format!("LMTP delivery succeeded: {}", line.trim()))
}
