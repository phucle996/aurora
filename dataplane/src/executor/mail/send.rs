use crate::executor::mail::registry::{fetch_l2_endpoint_config, mail_proto, MailServerPool};
use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::infra::redis::RedisClientManager;
use crate::job_lifecycle::message::JobPayload;
use crate::observability::logger::Logger;
use async_trait::async_trait;
use prost::Message;
use std::sync::Arc;

/// ============================================================================
/// 📂 MODULE: executor/mail/send.rs - BỘ THỰC THI GỬI EMAIL NGHIỆP VỤ
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Thực hiện gửi email thông qua việc định tuyến động qua các SMTP Endpoint khỏe mạnh.
///   - Hỗ trợ cơ chế tự động chuyển vùng dự phòng (Failover) tối đa 3 lần nếu xảy ra lỗi.
///   - Hỗ trợ định tuyến lai (Hybrid Routing): Tự động phát hiện Stalwart Mail Server
///     và stream trực tiếp email qua cổng LMTP nội bộ (cổng 24) không qua SMTP Actor.
///

// Cấu trúc bộ thực thi nhiệm vụ gửi mail với các dependency cần thiết
pub struct MailSendExecutor {
    mail_server_pool: Arc<MailServerPool>,
    redis_mgr: Arc<RedisClientManager>,
    zone_id: String,
}

impl MailSendExecutor {
    // Khởi tạo một đối tượng MailSendExecutor mới
    pub fn new(
        mail_server_pool: Arc<MailServerPool>,
        redis_mgr: Arc<RedisClientManager>,
        zone_id: String,
    ) -> Self {
        Self {
            mail_server_pool,
            redis_mgr,
            zone_id,
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

        // 2. Xác định endpoint ID đích
        let mut target_endpoint_id = if !payload.resource_id.is_empty() && payload.resource_id != "verify_account" {
            Some(payload.resource_id.clone())
        } else {
            None
        };

        // Nếu là email hệ thống hoặc resource_id rỗng -> Tìm kiếm một Stalwart endpoint trong L1/L2 Pool
        if target_endpoint_id.is_none() {
            let endpoint_ids: Vec<String> = {
                let meta = self.mail_server_pool.server_metadata.read().await;
                meta.keys().cloned().collect()
            };
            for id in endpoint_ids {
                if let Ok(cfg) = fetch_l2_endpoint_config(&self.redis_mgr, &self.zone_id, &id).await {
                    let is_stalwart = cfg.port == 24 
                        || cfg.host.contains("stalwart") 
                        || cfg.name.to_lowercase().contains("stalwart");
                    if is_stalwart {
                        Logger::sys_info(
                            "executor.mail.send",
                            &format!("System Mail: Automatically matched Stalwart Endpoint ID: {}", id),
                        );
                        target_endpoint_id = Some(id);
                        break;
                    }
                }
            }
        }

        // Nếu vẫn không tìm thấy Stalwart Endpoint và đây là email hệ thống/fallback -> Thử lấy endpoint ngẫu nhiên có trọng số
        let endpoint_id = match target_endpoint_id {
            Some(id) => id,
            None => {
                match self.mail_server_pool.select_endpoint_weighted_random().await {
                    Ok(id) => {
                        Logger::sys_info(
                            "executor.mail.send",
                            &format!("Stalwart endpoint not explicitly found. Falling back to weighted random endpoint: {}", id),
                        );
                        id
                    }
                    Err(e) => {
                        return Err(ExecutorError::ExecutionFailed(format!(
                            "No mail endpoints available in pool to route mail job: {}",
                            e
                        )));
                    }
                }
            }
        };

        // 3. Thực hiện vòng lặp gửi qua Endpoint (Failover tối đa 3 lần cho dynamic pool)
        let mut attempts = 0;
        let mut current_endpoint_id = endpoint_id;
        let mut last_error = "Unknown error".to_string();

        while attempts < 3 {
            // Lấy cấu hình chi tiết từ Redis L2 Cache
            let config = match fetch_l2_endpoint_config(&self.redis_mgr, &self.zone_id, &current_endpoint_id).await {
                Ok(cfg) => cfg,
                Err(e) => {
                    Logger::sys_error(
                        "executor.mail.send",
                        &format!("Failed to fetch L2 config for endpoint {}: {}", current_endpoint_id, e),
                        "L2_CONFIG_FETCH_FAILED",
                    );
                    attempts += 1;
                    // Lấy endpoint khác từ pool để retry
                    if let Ok(id) = self.mail_server_pool.select_endpoint_weighted_random().await {
                        current_endpoint_id = id;
                    }
                    continue;
                }
            };

            // Kiểm tra xem có phải là Stalwart (cổng 24 hoặc chứa chữ stalwart)
            let is_stalwart = config.port == 24 
                || config.host.contains("stalwart") 
                || config.name.to_lowercase().contains("stalwart");

            if is_stalwart {
                Logger::sys_info(
                    "executor.mail.send",
                    &format!("Routing via Stalwart LMTP to {}:{}...", config.host, config.port),
                );

                // Dựng email message bằng lettre để lấy định dạng MIME chuẩn
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

                match send_via_lmtp(&config.host, config.port as u16, sender_addr, &mail_config.to, &stuffed_bytes).await {
                    Ok(success_msg) => {
                        self.mail_server_pool.record_success(&current_endpoint_id).await;
                        return Ok(ExecutionResult {
                            message: format!("LMTP Relay through Endpoint {}: {}", current_endpoint_id, success_msg),
                        });
                    }
                    Err(e) => {
                        Logger::sys_error(
                            "executor.mail.send",
                            &format!("LMTP delivery failed on Stalwart endpoint {}: {}", current_endpoint_id, e),
                            "LMTP_DELIVERY_ERROR",
                        );
                        self.mail_server_pool.record_failure(&current_endpoint_id, Some(self.redis_mgr.clone()), &self.zone_id).await;
                        last_error = e;
                        attempts += 1;
                        // Thử lấy endpoint ngẫu nhiên tiếp theo nếu có thể
                        if let Ok(id) = self.mail_server_pool.select_endpoint_weighted_random().await {
                            current_endpoint_id = id;
                        }
                        continue;
                    }
                }
            } else {
                // Định tuyến qua SMTP Actor thông thường
                Logger::sys_info(
                    "executor.mail.send",
                    &format!("Routing via SMTP Actor for Endpoint {} ({}:{})...", current_endpoint_id, config.host, config.port),
                );

                let tx = match self
                    .mail_server_pool
                    .get_or_create_actor(&current_endpoint_id, &self.zone_id, &self.redis_mgr)
                    .await
                {
                    Ok(sender) => sender,
                    Err(e) => {
                        Logger::sys_error(
                            "executor.mail.send",
                            &format!("Failed to retrieve/create SMTP Actor for endpoint {}: {}", current_endpoint_id, e),
                            "SMTP_ACTOR_CREATION_FAILED",
                        );
                        self.mail_server_pool.record_failure(&current_endpoint_id, Some(self.redis_mgr.clone()), &self.zone_id).await;
                        last_error = e;
                        attempts += 1;
                        if let Ok(id) = self.mail_server_pool.select_endpoint_weighted_random().await {
                            current_endpoint_id = id;
                        }
                        continue;
                    }
                };

                let (response_tx, response_rx) = tokio::sync::oneshot::channel();
                let msg = crate::executor::mail::registry::MailActorMessage::SendEmail {
                    payload: payload.payload.clone(),
                    response_tx,
                };

                if let Err(e) = tx.send(msg).await {
                    Logger::sys_error(
                        "executor.mail.send",
                        &format!("Failed to dispatch email task to Actor {}: {}", current_endpoint_id, e),
                        "ACTOR_SEND_ERROR",
                    );
                    self.mail_server_pool.record_failure(&current_endpoint_id, Some(self.redis_mgr.clone()), &self.zone_id).await;
                    last_error = e.to_string();
                    attempts += 1;
                    if let Ok(id) = self.mail_server_pool.select_endpoint_weighted_random().await {
                        current_endpoint_id = id;
                    }
                    continue;
                }

                // Đợi kết quả phản hồi từ Actor SMTP với thời gian chờ Timeout 15s để chống treo thread
                match tokio::time::timeout(tokio::time::Duration::from_secs(15), response_rx).await {
                    Ok(Ok(Ok(success_msg))) => {
                        self.mail_server_pool.record_success(&current_endpoint_id).await;
                        return Ok(ExecutionResult {
                            message: format!("SMTP Delivery: {}", success_msg),
                        });
                    }
                    Ok(Ok(Err(delivery_err))) => {
                        Logger::sys_error(
                            "executor.mail.send",
                            &format!("SMTP Actor delivery error for endpoint {}: {}", current_endpoint_id, delivery_err),
                            "SMTP_DELIVERY_FAILED",
                        );
                        self.mail_server_pool.record_failure(&current_endpoint_id, Some(self.redis_mgr.clone()), &self.zone_id).await;
                        last_error = delivery_err;
                        attempts += 1;
                        if let Ok(id) = self.mail_server_pool.select_endpoint_weighted_random().await {
                            current_endpoint_id = id;
                        }
                        continue;
                    }
                    Ok(Err(_)) => {
                        Logger::sys_error(
                            "executor.mail.send",
                            &format!("Oneshot channel closed while waiting for SMTP Actor response on endpoint {}", current_endpoint_id),
                            "ONESHOT_CLOSED",
                        );
                        self.mail_server_pool.record_failure(&current_endpoint_id, Some(self.redis_mgr.clone()), &self.zone_id).await;
                        last_error = "Oneshot channel closed".to_string();
                        attempts += 1;
                        if let Ok(id) = self.mail_server_pool.select_endpoint_weighted_random().await {
                            current_endpoint_id = id;
                        }
                        continue;
                    }
                    Err(_) => {
                        Logger::sys_error(
                            "executor.mail.send",
                            &format!("SMTP Actor response timed out after 15s on endpoint {}", current_endpoint_id),
                            "SMTP_TIMEOUT",
                        );
                        self.mail_server_pool.record_failure(&current_endpoint_id, Some(self.redis_mgr.clone()), &self.zone_id).await;
                        last_error = "Timeout waiting for Actor response".to_string();
                        attempts += 1;
                        if let Ok(id) = self.mail_server_pool.select_endpoint_weighted_random().await {
                            current_endpoint_id = id;
                        }
                        continue;
                    }
                }
            }
        }

        // Nếu vượt quá 3 lần thử mà vẫn thất bại, trả về lỗi chi tiết
        Err(ExecutorError::ExecutionFailed(format!(
            "Failed to send email after 3 failover attempts. Last error: {}",
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
