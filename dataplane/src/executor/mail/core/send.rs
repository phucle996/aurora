use tokio::net::TcpStream;
use tokio::io::BufReader;
use tokio::sync::Mutex as TokioMutex;
use crate::observability::logger::Logger;

/// ============================================================================
/// 📂 KẾT NỐI LMTP DUY TRÌ TRẠNG THÁI GỬI MAIL ĐẾN STALWART
/// ============================================================================
pub struct LmtpConnection {
    pub reader: BufReader<TcpStream>,
}

impl LmtpConnection {
    /// Thực hiện kết nối TCP và bắt tay LHLO ban đầu đến Stalwart
    pub async fn connect(host: &str, port: u16) -> Result<Self, String> {
        use tokio::io::{AsyncBufReadExt, AsyncWriteExt};

        // Thiết lập kết nối TCP Socket đến Stalwart LMTP Service
        let stream = TcpStream::connect(format!("{}:{}", host, port))
            .await
            .map_err(|e| format!("Kết nối đến Stalwart LMTP {}:{}: {} thất bại", host, port, e))?;

        let mut reader = BufReader::new(stream);

        // Đọc mã phản hồi chào mừng từ Stalwart (kỳ vọng 220)
        let mut line = String::new();
        reader
            .read_line(&mut line)
            .await
            .map_err(|e| e.to_string())?;
        if !line.starts_with("220") {
            return Err(format!("Chào mừng từ LMTP không hợp lệ: {}", line.trim()));
        }

        // Gửi lệnh LHLO kèm hostname để định danh phiên làm việc LMTP
        line.clear();
        let hostname = crate::config::get_node_hostname();
        reader
            .get_mut()
            .write_all(format!("LHLO {}\r\n", hostname).as_bytes())
            .await
            .map_err(|e| e.to_string())?;
        reader.get_mut().flush().await.map_err(|e| e.to_string())?;

        // Đọc phản hồi từ LHLO (phản hồi nhiều dòng, kết thúc bằng dòng chứa mã 250 kèm dấu cách)
        loop {
            line.clear();
            reader
                .read_line(&mut line)
                .await
                .map_err(|e| e.to_string())?;
            if line.starts_with("250 ") {
                break;
            } else if !line.starts_with("250-") {
                return Err(format!("Bắt tay LHLO thất bại: {}", line.trim()));
            }
        }

        Ok(Self { reader })
    }

    /// Gửi một email qua kết nối LMTP hiện có
    pub async fn send_mail(
        &mut self,
        sender: &str,
        recipient: &str,
        email_bytes: &[u8],
    ) -> Result<String, String> {
        use tokio::io::{AsyncBufReadExt, AsyncWriteExt};

        let mut line = String::new();

        // 1. MAIL FROM giao thức LMTP
        self.reader
            .get_mut()
            .write_all(format!("MAIL FROM:<{}>\r\n", sender).as_bytes())
            .await
            .map_err(|e| e.to_string())?;
        self.reader.get_mut().flush().await.map_err(|e| e.to_string())?;
        self.reader
            .read_line(&mut line)
            .await
            .map_err(|e| e.to_string())?;
        if !line.starts_with("250") {
            return Err(format!("MAIL FROM bị từ chối: {}", line.trim()));
        }

        // 2. RCPT TO giao thức LMTP
        line.clear();
        self.reader
            .get_mut()
            .write_all(format!("RCPT TO:<{}>\r\n", recipient).as_bytes())
            .await
            .map_err(|e| e.to_string())?;
        self.reader.get_mut().flush().await.map_err(|e| e.to_string())?;
        self.reader
            .read_line(&mut line)
            .await
            .map_err(|e| e.to_string())?;
        if !line.starts_with("250") {
            let _ = self.reset().await;
            return Err(format!("RCPT TO bị từ chối: {}", line.trim()));
        }

        // 3. Khởi tạo lệnh DATA
        line.clear();
        self.reader
            .get_mut()
            .write_all(b"DATA\r\n")
            .await
            .map_err(|e| e.to_string())?;
        self.reader.get_mut().flush().await.map_err(|e| e.to_string())?;
        self.reader
            .read_line(&mut line)
            .await
            .map_err(|e| e.to_string())?;
        if !line.starts_with("354") {
            let _ = self.reset().await;
            return Err(format!("DATA bị từ chối: {}", line.trim()));
        }

        // 4. Stream nội dung MIME của email
        self.reader
            .get_mut()
            .write_all(email_bytes)
            .await
            .map_err(|e| e.to_string())?;
        if !email_bytes.ends_with(b"\r\n") {
            self.reader
                .get_mut()
                .write_all(b"\r\n")
                .await
                .map_err(|e| e.to_string())?;
        }
        self.reader
            .get_mut()
            .write_all(b".\r\n")
            .await
            .map_err(|e| e.to_string())?;
        self.reader.get_mut().flush().await.map_err(|e| e.to_string())?;

        // 5. Kiểm chứng phản hồi xử lý email từ Stalwart (kỳ vọng 250)
        line.clear();
        self.reader
            .read_line(&mut line)
            .await
            .map_err(|e| e.to_string())?;
        if !line.starts_with("250") {
            return Err(format!("Không thể gửi email thành công sau lệnh DATA: {}", line.trim()));
        }

        Ok(line.trim().to_string())
    }

    /// Lệnh RSET để khôi phục trạng thái kết nối khi giao dịch email thất bại nửa chừng
    async fn reset(&mut self) -> Result<(), String> {
        use tokio::io::{AsyncBufReadExt, AsyncWriteExt};
        let mut line = String::new();
        self.reader.get_mut().write_all(b"RSET\r\n").await.map_err(|e| e.to_string())?;
        self.reader.get_mut().flush().await.map_err(|e| e.to_string())?;
        self.reader.read_line(&mut line).await.map_err(|e| e.to_string())?;
        Ok(())
    }
}

/// ============================================================================
/// 📂 POOL QUẢN LÝ CÁC KẾT NỐI LMTP ĐẾN STALWART CLUSTER
/// ============================================================================
pub struct LmtpConnectionPool {
    host: String,
    port: u16,
    connections: TokioMutex<Vec<LmtpConnection>>,
}

impl LmtpConnectionPool {
    /// Tạo mới một connection pool cho Stalwart
    pub fn new(host: String, port: u16) -> Self {
        Self {
            host,
            port,
            connections: TokioMutex::new(Vec::new()),
        }
    }

    /// Lấy ra kết nối LMTP rảnh rỗi từ pool hoặc mở kết nối mới
    pub async fn get(&self) -> Result<LmtpConnection, String> {
        let mut conns = self.connections.lock().await;
        if let Some(conn) = conns.pop() {
            Ok(conn)
        } else {
            LmtpConnection::connect(&self.host, self.port).await
        }
    }

    /// Trả kết nối về lại pool để tái sử dụng
    pub async fn put(&self, conn: LmtpConnection) {
        let mut conns = self.connections.lock().await;
        conns.push(conn);
    }
}

/// ============================================================================
/// 📂 HÀM TIỆN ÍCH DÙNG CHUNG GỬI EMAIL THÔ
/// ============================================================================
pub async fn send_raw_email(
    pool: &LmtpConnectionPool,
    sender_addr: &str,
    recipient: &str,
    subject: &str,
    html_body: &str,
) -> Result<String, String> {
    let sender_mailbox: lettre::message::Mailbox = sender_addr
        .parse()
        .map_err(|e| format!("Địa chỉ người gửi không hợp lệ: {}", e))?;

    let email = lettre::Message::builder()
        .from(sender_mailbox.clone())
        .to(recipient.parse().map_err(|e| format!("Địa chỉ người nhận không hợp lệ: {}", e))?)
        .subject(subject.to_string())
        .header(lettre::message::header::ContentType::TEXT_HTML)
        .body(html_body.to_string())
        .map_err(|e| format!("Dựng MIME Message cho LMTP stream thất bại: {}", e))?;

    let email_bytes = email.formatted();
    let stuffed_bytes = dot_stuffing(&email_bytes);

    let mut attempts = 0;
    let mut last_error = String::new();
    while attempts < 3 {
        let mut conn = match pool.get().await {
            Ok(c) => c,
            Err(e) => {
                last_error = e;
                attempts += 1;
                continue;
            }
        };

        match conn.send_mail(sender_addr, recipient, &stuffed_bytes).await {
            Ok(success_msg) => {
                pool.put(conn).await;
                return Ok(success_msg);
            }
            Err(e) => {
                Logger::sys_warn(
                    "core.mail.send",
                    &format!("Thử gửi LMTP lần {} thất bại: {}. Đang đóng kết nối lỗi.", attempts + 1, e),
                    "LMTP_CONNECTION_ERROR",
                );
                last_error = e;
                attempts += 1;
                if attempts < 3 {
                    tokio::time::sleep(tokio::time::Duration::from_millis(500)).await;
                }
            }
        }
    }

    Err(format!(
        "Gửi email đến Stalwart thất bại sau 3 lần thử. Lỗi cuối cùng: {}",
        last_error
    ))
}

/// Thực hiện dot-stuffing cho giao thức LMTP/SMTP (thêm dấu chấm nếu dòng bắt đầu bằng dấu chấm)
pub fn dot_stuffing(content: &[u8]) -> Vec<u8> {
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
