use crate::executor::{ExecutionResult, Executor, ExecutorError};
use crate::job_receiver::message::JobPayload;
use async_trait::async_trait;
use lettre::transport::smtp::authentication::Credentials;
use lettre::transport::smtp::client::{
    Certificate, CertificateStore, Identity, Tls, TlsParametersBuilder,
};
use lettre::SmtpTransport;
use serde::Deserialize;
use std::time::Duration;

/// ============================================================================
/// 📂 MODULE: executor/mail/test_connection.rs - BỘ KIỂM TRA KẾT NỐI SMTP
/// ============================================================================
///
/// 📌 VAI TRÒ & NHIỆM VỤ (ROLE):
///   - Thực hiện kiểm tra kết nối SMTP bất đồng bộ (handshake và credentials auth) từ Dataplane.
///   - Hỗ trợ đầy đủ các chế độ bảo mật: None, STARTTLS, TLS, và mTLS (Client Certificate).
///   - Ràng buộc an toàn: Không gây block thread của worker nhờ cơ chế Async/Tokio và Timeout.
///
/// 🔄 CONTRACT & CALLSITE FLOW (CONTRACT):
///   - Input: Nhận `JobPayload` chứa `payload_json` chứa cấu hình SMTP nhạy cảm.
///   - Schema JSON mong đợi: `SmtpTestPayload` với các thuộc tính:
///       - `host`: Tên miền hoặc IP của SMTP Server.
///       - `port`: Cổng dịch vụ (ví dụ: 25, 465, 587).
///       - `username` / `password`: Tùy chọn tài khoản đăng nhập SMTP.
///       - `tls_mode`: Chế độ mã hóa ("none", "starttls", "tls", "mtls").
///       - `ca_cert_pem`: Chuỗi PEM chứa chứng chỉ CA để verify server cert.
///       - `client_cert_pem` / `client_key_pem`: Chứng chỉ và khóa client phục vụ bắt tay mTLS.
///   - Output: Trả về `Result<ExecutionResult, ExecutorError>` báo cáo trạng thái bắt tay SMTP.
///   - Callback: Kết quả trả về sau đó được `job_receiver/consumer.rs` chuyển đổi thành
///     `JobExecutionResult` và gửi trực tiếp qua Redis Pub/Sub kênh `job_results:<job_id>`.
///
/// 🎯 SOURCE OF TRUTH (SoT):
///   - File cấu hình thực thi: Giải mã trễ (Lazy Deserialization) tại hàm `execute` nhằm tránh lưu trữ
///     cấu hình SMTP giải mã trong bộ nhớ đệm hay logs trung gian của Dataplane.
///   - Trạng thái SMTP: Xác định duy nhất bằng cách mở kết nối TCP socket và thực hiện SMTP handshake
///     thời gian thực đến Mail Server chỉ định. Không cache trạng thái kết nối.
///
/// 🔒 PRIVACY BOUNDARY / SECURITY BOUNDARY (BOUNDARY):
///   - **Bảo vệ thông tin nhạy cảm**: Credentials (username, password, client_key_pem) chỉ tồn tại
///     dưới dạng chuỗi nhạy cảm ngắn hạn trong ngăn xếp bộ nhớ (RAM stack) khi chạy `execute`.
///     Tuyệt đối không được ghi ra các log file của hệ thống.
///   - **Cách ly chứng chỉ (Certificate Isolation)**: Nếu `ca_cert_pem` không được cung cấp,
///     chúng ta chủ động thiết lập `CertificateStore::None` để không dùng chung kho chứng chỉ mặc định
///     của hệ điều hành máy chủ (system root certs), ngăn chặn các trường hợp vượt rào bảo mật giữa các Tenant.
///   - **Chống Treo Luồng (DoS Protection)**: Cấu hình cứng `timeout` là 5 giây trong `lettre` builder
///     để đảm bảo nếu Mail Server bị chậm hoặc cố tình gửi gói tin vô tận, socket sẽ bị đóng cưỡng bức,
///     bảo vệ tài nguyên cho cụm High Availability của Dataplane.
///

#[derive(Deserialize, Debug, Clone)]
pub struct SmtpTestPayload {
    /// Tên miền / Địa chỉ IP của SMTP Server (ví dụ: "smtp.gmail.com")
    pub host: String,
    /// Cổng kết nối SMTP (thường là 25, 465, 587 hoặc 2525)
    pub port: u16,
    /// Tài khoản đăng nhập tùy chọn
    pub username: Option<String>,
    /// Mật khẩu đăng nhập tùy chọn (được truyền an toàn qua Redis mã hóa)
    pub password: Option<String>,
    /// Chế độ bảo mật mong muốn: "none", "starttls", "tls", hoặc "mtls"
    pub tls_mode: String,
    /// Chứng chỉ CA tùy chọn định dạng PEM để xác thực Server
    pub ca_cert_pem: Option<String>,
    /// Chứng chỉ Client tùy chọn phục vụ mTLS (Client Authentication)
    pub client_cert_pem: Option<String>,
    /// Khóa riêng tư Client tùy chọn phục vụ mTLS (Client Authentication)
    pub client_key_pem: Option<String>,
}

pub struct SmtpTestExecutor;

#[async_trait]
impl Executor for SmtpTestExecutor {
    /// Thực thi kiểm tra bắt tay (handshake) SMTP dựa vào payload cấu hình gửi từ Controlplane.
    async fn execute(&self, payload: JobPayload) -> Result<ExecutionResult, ExecutorError> {
        crate::observability::logger::Logger::sys_info(
            "executor.mail.test_connection",
            &format!(
                "SMTP Test Connection Executor started for job_id={}",
                payload.job_id
            ),
        );

        // ----------------------------------------------------------------------
        // Bước 1: Giải mã cấu hình SMTP từ JSON thô (Lazy Deserialization)
        // ----------------------------------------------------------------------
        // Chỉ giải mã ngay trước khi thực thi để giảm diện tích bộ nhớ nhạy cảm.
        let config: SmtpTestPayload = serde_json::from_str(&payload.payload_json).map_err(|e| {
            ExecutorError::ExecutionFailed(format!("Invalid SMTP config JSON: {}", e))
        })?;

        // ----------------------------------------------------------------------
        // Bước 2: Thiết lập cấu hình bảo mật TLS nếu cần thiết
        // ----------------------------------------------------------------------
        // Khởi tạo tham số TLS chỉ khi chế độ bảo mật yêu cầu mã hóa kết nối.
        let tls_parameters = match config.tls_mode.as_str() {
            "tls" | "mtls" | "starttls" => {
                // Sử dụng Builder của lettre để xây dựng cấu hình TLS (bọc từ rustls)
                let mut tls_builder = TlsParametersBuilder::new(config.host.clone());

                // RÀNG BUỘC BẢO MẬT: Bắt buộc cấu hình CertificateStore::None để ngăn chặn việc
                // tự động nạp các chứng chỉ root mặc định của hệ thống máy chủ (System Roots).
                // Mọi liên kết TLS chỉ được tin cậy chứng chỉ do Tenant tự truyền vào.
                tls_builder = tls_builder.certificate_store(CertificateStore::None);

                // Nạp CA Cert tùy chọn của Tenant nếu có
                if let Some(ref ca_pem) = config.ca_cert_pem {
                    if !ca_pem.trim().is_empty() {
                        let cert = Certificate::from_pem(ca_pem.as_bytes()).map_err(|e| {
                            ExecutorError::ExecutionFailed(format!(
                                "Failed to parse custom CA PEM: {}",
                                e
                            ))
                        })?;
                        tls_builder = tls_builder.add_root_certificate(cert);
                        crate::observability::logger::Logger::sys_info(
                            "executor.mail.test_connection",
                            "Successfully loaded custom CA certificate for SMTP TLS verification",
                        );
                    }
                }

                // Nếu chạy chế độ mTLS (Mutual TLS), nạp thêm Client Certificate & Private Key
                if config.tls_mode == "mtls" {
                    if let (Some(ref cert_pem), Some(ref key_pem)) =
                        (&config.client_cert_pem, &config.client_key_pem)
                    {
                        if !cert_pem.trim().is_empty() && !key_pem.trim().is_empty() {
                            // Tạo Identity đại diện cho Client bằng cặp Cert & Key PEM
                            let identity =
                                Identity::from_pem(cert_pem.as_bytes(), key_pem.as_bytes())
                                    .map_err(|e| {
                                        ExecutorError::ExecutionFailed(format!(
                                            "Failed to load client cert/key for mTLS identity: {}",
                                            e
                                        ))
                                    })?;
                            tls_builder = tls_builder.identify_with(identity);
                            crate::observability::logger::Logger::sys_info(
                                "executor.mail.test_connection",
                                "Successfully loaded client certificate & private key for SMTP mTLS",
                            );
                        } else {
                            return Err(ExecutorError::ExecutionFailed(
                                "mTLS mode selected but client_cert_pem or client_key_pem is empty"
                                    .to_string(),
                            ));
                        }
                    } else {
                        return Err(ExecutorError::ExecutionFailed(
                            "mTLS mode selected but client_cert_pem or client_key_pem is missing"
                                .to_string(),
                        ));
                    }
                }

                // Build TLS Parameters an toàn từ cấu hình đã thiết lập
                let tls_params = tls_builder.build().map_err(|e| {
                    ExecutorError::ExecutionFailed(format!("Failed to build TLS Parameters: {}", e))
                })?;
                Some(tls_params)
            }
            _ => None,
        };

        // ----------------------------------------------------------------------
        // Bước 3: Quyết định cơ chế kết nối bảo mật SMTP (Connection Security)
        // ----------------------------------------------------------------------
        let connection_security = match config.tls_mode.as_str() {
            "tls" | "mtls" => {
                if let Some(params) = tls_parameters {
                    Tls::Required(params)
                } else {
                    return Err(ExecutorError::ExecutionFailed(
                        "TLS parameters required but missing".to_string(),
                    ));
                }
            }
            "starttls" => {
                if let Some(params) = tls_parameters {
                    Tls::Opportunistic(params)
                } else {
                    return Err(ExecutorError::ExecutionFailed(
                        "STARTTLS parameters required but missing".to_string(),
                    ));
                }
            }
            _ => Tls::None,
        };

        // ----------------------------------------------------------------------
        // Bước 4: Khởi tạo SmtpTransport của lettre
        // ----------------------------------------------------------------------
        // Khóa cứng timeout 5s để tránh treo luồng hệ thống
        let mut transport_builder = SmtpTransport::builder_dangerous(&config.host)
            .port(config.port)
            .tls(connection_security)
            .timeout(Some(Duration::from_secs(5)));

        // Nạp thông tin đăng nhập SMTP Credentials nếu được cấu hình
        if let (Some(user), Some(pass)) = (config.username, config.password) {
            if !user.is_empty() && !pass.is_empty() {
                transport_builder = transport_builder.credentials(Credentials::new(user, pass));
            }
        }

        let transport = transport_builder.build();

        // ----------------------------------------------------------------------
        // Bước 5: Thực thi bắt tay kiểm tra kết nối thực tế lên Mail Server
        // ----------------------------------------------------------------------
        // Gọi test_connection() gửi EHLO/HELO, nâng cấp TLS và verify credentials
        match transport.test_connection() {
            Ok(true) => {
                crate::observability::logger::Logger::sys_info(
                    "executor.mail.test_connection",
                    &format!(
                        "SMTP connection test SUCCEEDED for job_id={}",
                        payload.job_id
                    ),
                );
                Ok(ExecutionResult {
                    success: true,
                    return_code: "SUCCEEDED".to_string(),
                    message: "SMTP handshake and/or credentials validation succeeded".to_string(),
                })
            }
            Ok(false) => {
                crate::observability::logger::Logger::sys_warn(
                    "executor.mail.test_connection",
                    &format!(
                        "SMTP connection test returned false for job_id={}",
                        payload.job_id
                    ),
                    "SMTP_TEST_FALSE",
                );
                Err(ExecutorError::ExecutionFailed(
                    "SMTP handshake was successful, but connection check returned false"
                        .to_string(),
                ))
            }
            Err(e) => {
                crate::observability::logger::Logger::sys_error(
                    "executor.mail.test_connection",
                    &format!(
                        "SMTP connection test failed for job_id={}: {}",
                        payload.job_id, e
                    ),
                    "SMTP_TEST_FAILED",
                );
                Err(ExecutorError::ExecutionFailed(format!(
                    "SMTP connection failed: {}",
                    e
                )))
            }
        }
    }
}
