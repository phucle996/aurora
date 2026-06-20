use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
use lettre::transport::smtp::authentication::Credentials;
use lettre::transport::smtp::client::{
    Certificate, CertificateStore, Identity, Tls, TlsParametersBuilder,
};
use lettre::SmtpTransport;
use lettre::Transport;
use prost::Message;
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::{mpsc, oneshot, RwLock};
use tokio::time::{timeout, Duration};

// Import protobuffer đã được generate tự động từ build.rs
pub mod mail_proto {
    include!(concat!(env!("OUT_DIR"), "/mail.rs"));
}

/// ============================================================================
/// 📂 MODULE: executor/mail/registry.rs - BỘ QUẢN LÝ MAIL SERVER POOL & ACTORS
/// ============================================================================
///
/// 📌 VAI TRÒ & NHIỆM VỤ:
///   - Quản lý metadata định tuyến nhẹ của Mail Server Pool (`server_metadata`).
///   - Quản lý danh sách MPSC Senders kết nối trực tiếp đến các Actor đang hoạt động.
///   - Cung cấp giải thuật định tuyến ngẫu nhiên theo trọng số (Weighted Random Selection).
///   - Tự động phát hiện lỗi SMTP và cô lập/cách ly Endpoint bị lỗi (Failover & Quarantine).
///

/// Trạng thái hoạt động của SMTP Endpoint
#[derive(Clone, Copy, Debug, PartialEq)]
pub enum EndpointStatus {
    Healthy,
    Unhealthy, // Bị cách ly do lỗi kết nối liên tiếp vượt ngưỡng
}

/// Metadata định tuyến nhẹ của một Endpoint
#[derive(Clone, Debug, serde::Deserialize, serde::Serialize)]
pub struct EndpointMetadata {
    pub weight: u32,
    pub priority: u32,
    pub max_connections: u32,
    pub config_version: u32,
}

/// Chỉ số giám sát tải và lỗi nội bộ
#[derive(Clone, Debug)]
pub struct EndpointMetrics {
    pub active_conns: usize,
    pub consecutive_failures: u32,
    pub status: EndpointStatus,
}

/// Thông điệp gửi tới Endpoint Mail Actor qua MPSC channel
pub enum MailActorMessage {
    SendEmail {
        payload: Vec<u8>,
        response_tx: oneshot::Sender<Result<String, String>>,
    },
    TestConnection {
        response_tx: oneshot::Sender<Result<(), String>>,
    },
}

/// Mail Server Pool tổng thể quản lý kết nối & định tuyến
pub struct MailServerPool {
    // Bảng thông số định tuyến nhẹ: endpoint_id -> metadata
    pub server_metadata: Arc<RwLock<HashMap<String, EndpointMetadata>>>,
    // Các cổng truyền tin (Senders) dẫn tới các Actors đang thức
    pub active_actors: Arc<RwLock<HashMap<String, mpsc::Sender<MailActorMessage>>>>,
    // Chỉ số giám sát chất lượng và kết nối thời gian thực
    pub metrics: Arc<RwLock<HashMap<String, EndpointMetrics>>>,
}

impl MailServerPool {
    /// Khởi tạo một Pool trống
    pub fn new() -> Self {
        Self {
            server_metadata: Arc::new(RwLock::new(HashMap::new())),
            active_actors: Arc::new(RwLock::new(HashMap::new())),
            metrics: Arc::new(RwLock::new(HashMap::new())),
        }
    }

    /// Cập nhật hoặc thêm mới thông số định tuyến của Endpoint vào RAM L1
    pub async fn update_metadata(&self, endpoint_id: String, metadata: EndpointMetadata) {
        let mut meta = self.server_metadata.write().await;
        meta.insert(endpoint_id.clone(), metadata);

        let mut met = self.metrics.write().await;
        if !met.contains_key(&endpoint_id) {
            met.insert(
                endpoint_id,
                EndpointMetrics {
                    active_conns: 0,
                    consecutive_failures: 0,
                    status: EndpointStatus::Healthy,
                },
            );
        }
    }

    /// Xóa hoàn toàn Endpoint khỏi Pool (ví dụ: khi admin xóa hoặc vô hiệu hóa)
    pub async fn remove_endpoint(&self, endpoint_id: &str) {
        // 1. Xóa khỏi bảng định tuyến
        {
            let mut meta = self.server_metadata.write().await;
            meta.remove(endpoint_id);
        }
        // 2. Xóa khỏi bảng chỉ số metrics
        {
            let mut met = self.metrics.write().await;
            met.remove(endpoint_id);
        }
        // 3. Xóa Actor Sender. Khi không còn tham chiếu Sender nào, Actor nhận tin sẽ tự động tắt.
        {
            let mut actors = self.active_actors.write().await;
            actors.remove(endpoint_id);
        }
        Logger::sys_info(
            "mail.pool",
            &format!(
                "Endpoint {} has been removed from L1 RAM MailServerPool",
                endpoint_id
            ),
        );
    }

    /// Ghi nhận kết nối hoặc gửi email thành công để giải phóng trạng thái cách ly
    pub async fn record_success(&self, endpoint_id: &str) {
        let mut met = self.metrics.write().await;
        if let Some(m) = met.get_mut(endpoint_id) {
            m.consecutive_failures = 0;
            m.status = EndpointStatus::Healthy;
        }
    }

    /// Ghi nhận lỗi kết nối và thực hiện Quarantine (cách ly) nếu lỗi liên tục vượt ngưỡng
    pub async fn record_failure(
        &self,
        endpoint_id: &str,
        redis_mgr: Option<Arc<RedisClientManager>>,
        zone_id: &str,
    ) {
        let mut met = self.metrics.write().await;
        if let Some(m) = met.get_mut(endpoint_id) {
            m.consecutive_failures += 1;
            if m.consecutive_failures >= 5 && m.status == EndpointStatus::Healthy {
                m.status = EndpointStatus::Unhealthy;
                Logger::sys_warn(
                    "mail.pool",
                    &format!("CRITICAL: Endpoint {} failed 5 consecutive times. Quarantined (marked UNHEALTHY)!", endpoint_id),
                    "ENDPOINT_QUARANTINED"
                );

                // Đồng bộ trạng thái UNHEALTHY lên Redis L2 Cache để các Pod khác lập tức bỏ qua Endpoint này
                if let Some(mgr) = redis_mgr {
                    let zone_clone = zone_id.to_string();
                    let id_clone = endpoint_id.to_string();
                    tokio::spawn(async move {
                        let _ = mark_l2_endpoint_unhealthy(&mgr, &zone_clone, &id_clone).await;
                    });
                }
            }
        }
    }

    /// Buộc đánh dấu Unhealthy lập tức (ví dụ: khi khởi động bắt tay lỗi nghiêm trọng)
    pub async fn mark_unhealthy(&self, endpoint_id: &str) {
        let mut met = self.metrics.write().await;
        if let Some(m) = met.get_mut(endpoint_id) {
            m.status = EndpointStatus::Unhealthy;
        }
    }

    /// Định tuyến ngẫu nhiên theo trọng số (Weighted Random Selection) qua các Server khỏe mạnh
    pub async fn select_endpoint_weighted_random(&self) -> Result<String, String> {
        let meta = self.server_metadata.read().await;
        let met = self.metrics.read().await;

        // Lọc ra các Endpoint khỏe mạnh và chưa bị nghẽn
        let mut candidates = Vec::new();
        let mut total_weight = 0;

        for (id, info) in meta.iter() {
            let is_healthy = met
                .get(id)
                .map(|m| m.status == EndpointStatus::Healthy)
                .unwrap_or(true);
            let is_under_limit = met
                .get(id)
                .map(|m| m.active_conns < info.max_connections as usize)
                .unwrap_or(true);

            if is_healthy && is_under_limit {
                candidates.push((id.clone(), info.weight));
                total_weight += info.weight;
            }
        }

        if candidates.is_empty() {
            return Err(
                "No healthy or available SMTP Endpoints found in the MailServerPool".to_string(),
            );
        }

        if total_weight == 0 {
            // Trường hợp tất cả đều có weight = 0, fallback lấy cái đầu tiên
            return Ok(candidates[0].0.clone());
        }

        // Chọn ngẫu nhiên theo công thức lũy kế trọng số
        use rand::Rng;
        let mut rng = rand::thread_rng();
        let random_roll = rng.gen_range(1..=total_weight);

        let mut cumulative = 0;
        for (id, w) in candidates {
            cumulative += w;
            if random_roll <= cumulative {
                return Ok(id);
            }
        }

        Err("Failed to execute weighted random selection due to logic drift".to_string())
    }

    /// Lấy hoặc kích hoạt Cold Start một Actor xử lý kết nối SMTP cho Endpoint cụ thể
    pub async fn get_or_create_actor(
        self: &Arc<Self>,
        endpoint_id: &str,
        zone_id: &str,
        redis_mgr: &Arc<RedisClientManager>,
    ) -> Result<mpsc::Sender<MailActorMessage>, String> {
        // ----------------------------------------------------------------------
        // Bước 1: Kiểm tra nhanh L1 Cache trong RAM (Read Lock)
        // ----------------------------------------------------------------------
        {
            let actors = self.active_actors.read().await;
            if let Some(sender) = actors.get(endpoint_id) {
                return Ok(sender.clone());
            }
        }

        // ----------------------------------------------------------------------
        // Bước 2: Lock ghi để chống việc Spawn trùng lặp Actor (Double-checked locking)
        // ----------------------------------------------------------------------
        let mut actors = self.active_actors.write().await;
        if let Some(sender) = actors.get(endpoint_id) {
            return Ok(sender.clone());
        }

        // ----------------------------------------------------------------------
        // Bước 3: Cold Start thực sự - Kéo cấu hình nhạy cảm từ Redis L2 Cache
        // ----------------------------------------------------------------------
        Logger::sys_info(
            "mail.pool",
            &format!(
                "Cold Start triggered: Fetching SMTP config from Redis L2 for endpoint {}",
                endpoint_id
            ),
        );
        let config = fetch_l2_endpoint_config(redis_mgr, zone_id, endpoint_id).await?;

        // Khởi tạo kênh MPSC chuyên dụng để truyền tin đến Actor
        let (tx, rx) = mpsc::channel::<MailActorMessage>(100);

        // Kích hoạt Actor chạy ngầm bọc kết nối SMTP
        spawn_endpoint_actor(
            endpoint_id.to_string(),
            config,
            self.clone(),
            redis_mgr.clone(),
            zone_id.to_string(),
            rx,
        );

        // Đăng ký cổng truyền tin vào Registry
        actors.insert(endpoint_id.to_string(), tx.clone());

        Ok(tx)
    }
}

/// Ghi đè cập nhật trạng thái của Endpoint bị lỗi lên Redis L2 Cache để đồng bộ toàn bộ cluster
async fn mark_l2_endpoint_unhealthy(
    redis_mgr: &RedisClientManager,
    zone_id: &str,
    endpoint_id: &str,
) -> Result<(), String> {
    let mut conn = redis_mgr
        .client()
        .get_multiplexed_async_connection()
        .await
        .map_err(|e| e.to_string())?;

    // Đọc Hash của Routing Table
    let key = format!("mail:zone:{}:server_pool", zone_id);
    let raw_val: Option<String> = redis::cmd("HGET")
        .arg(&key)
        .arg(endpoint_id)
        .query_async(&mut conn)
        .await
        .map_err(|e| e.to_string())?;

    if let Some(raw) = raw_val {
        // Parse metadata JSON, đổi status thành unhealthy
        if let Ok(mut json_val) = serde_json::from_str::<serde_json::Value>(&raw) {
            json_val["status"] = serde_json::Value::String("unhealthy".to_string());
            if let Ok(updated_raw) = serde_json::to_string(&json_val) {
                let _: () = redis::cmd("HSET")
                    .arg(&key)
                    .arg(endpoint_id)
                    .arg(updated_raw)
                    .query_async(&mut conn)
                    .await
                    .map_err(|e| e.to_string())?;
            }
        }
    }
    Ok(())
}

/// Đọc cấu hình kết nối SMTP được Controlplane đồng bộ vào Redis L2
pub(crate) async fn fetch_l2_endpoint_config(
    redis_mgr: &RedisClientManager,
    zone_id: &str,
    endpoint_id: &str,
) -> Result<mail_proto::SmtpEndpointSync, String> {
    let mut conn = redis_mgr
        .client()
        .get_multiplexed_async_connection()
        .await
        .map_err(|e| e.to_string())?;
    let key = format!("mail:zone:{}:endpoints:{}", zone_id, endpoint_id);

    let raw_bytes: Option<Vec<u8>> = redis::cmd("GET")
        .arg(&key)
        .query_async(&mut conn)
        .await
        .map_err(|e| e.to_string())?;

    let bytes = match raw_bytes {
        Some(b) => b,
        None => {
            return Err(format!(
                "SMTP config not found in Redis L2 for endpoint {}",
                endpoint_id
            ))
        }
    };

    // Giải mã nhị phân Protobuf cấu hình của SMTP Server
    let config = mail_proto::SmtpEndpointSync::decode(bytes.as_slice())
        .map_err(|e| format!("Failed to decode SMTP config Protobuf from Redis L2: {}", e))?;

    Ok(config)
}

/// Bắt chạy Actor quản lý connection pool biệt lập dưới dạng Tokio Task
pub fn spawn_endpoint_actor(
    endpoint_id: String,
    config: mail_proto::SmtpEndpointSync,
    pool: Arc<MailServerPool>,
    redis_mgr: Arc<RedisClientManager>,
    zone_id: String,
    mut rx: mpsc::Receiver<MailActorMessage>,
) {
    tokio::spawn(async move {
        Logger::sys_info(
            "mail.actor",
            &format!(
                "Initializing connection pool Actor for SMTP Endpoint {}",
                endpoint_id
            ),
        );

        // Xây dựng connection pool thông qua lettre SmtpTransport
        let transport = match build_smtp_transport(&config) {
            Ok(t) => t,
            Err(e) => {
                Logger::sys_error(
                    "mail.actor",
                    &format!(
                        "Failed to initialize SmtpTransport for Endpoint {}: {}",
                        endpoint_id, e
                    ),
                    "SMTP_INIT_FAILED",
                );
                pool.mark_unhealthy(&endpoint_id).await;
                return;
            }
        };

        loop {
            // [COMMENT]: Sử dụng timeout 15 phút của Tokio. Nếu không có Job nào gửi tới, Actor tự giải nghệ.
            match timeout(Duration::from_secs(900), rx.recv()).await {
                Ok(Some(msg)) => {
                    match msg {
                        MailActorMessage::SendEmail {
                            payload,
                            response_tx,
                        } => {
                            // Tăng số lượng kết nối đang bận
                            increment_active_connections(&pool, &endpoint_id).await;

                            let transport_clone = transport.clone();
                            // [COMMENT]: lettre send là hàm chặn đồng bộ, bắt buộc bọc trong spawn_blocking của Tokio
                            let res = tokio::task::spawn_blocking(move || {
                                execute_send_email(&transport_clone, payload)
                            })
                            .await
                            .unwrap_or_else(|_| Err("Thread pool execution panicked".to_string()));

                            // Ghi nhận chỉ số thành công hay thất bại để tự cách ly
                            match &res {
                                Ok(_) => pool.record_success(&endpoint_id).await,
                                Err(_) => {
                                    pool.record_failure(
                                        &endpoint_id,
                                        Some(redis_mgr.clone()),
                                        &zone_id,
                                    )
                                    .await
                                }
                            }

                            // Giảm số lượng kết nối bận
                            decrement_active_connections(&pool, &endpoint_id).await;
                            let _ = response_tx.send(res);
                        }
                        MailActorMessage::TestConnection { response_tx } => {
                            let transport_clone = transport.clone();
                            // [COMMENT]: test_connection là hàm chặn đồng bộ, bắt buộc chạy trong spawn_blocking
                            let res = tokio::task::spawn_blocking(move || {
                                match transport_clone.test_connection() {
                                    Ok(true) => Ok(()),
                                    Ok(false) => Err("SMTP handshake succeeded but test connection returned false".to_string()),
                                    Err(e) => Err(e.to_string()),
                                }
                            })
                            .await
                            .unwrap_or_else(|_| Err("Thread pool execution panicked".to_string()));

                            let _ = response_tx.send(res);
                        }
                    }
                }
                Ok(None) => {
                    // Kênh truyền tin bị đóng hoàn toàn
                    break;
                }
                Err(_) => {
                    // [COMMENT]: ĐÃ HẾT 15 PHÚT NHÀN RỖI -> ACTOR TỰ KẾT LIỄU
                    Logger::sys_info(
                        "mail.actor",
                        &format!(
                            "Actor for SMTP Endpoint {} idle for 15 minutes. Self-terminating...",
                            endpoint_id
                        ),
                    );
                    // Gỡ Sender ra khỏi active_actors Registry
                    {
                        let mut actors = pool.active_actors.write().await;
                        actors.remove(&endpoint_id);
                    }
                    // Thoát vòng lặp. SmtpTransport khi bị drop sẽ tự động đóng toàn bộ TCP connections đang mở.
                    break;
                }
            }
        }
    });
}

/// Tăng số lượng kết nối đang bận
async fn increment_active_connections(pool: &MailServerPool, endpoint_id: &str) {
    let mut met = pool.metrics.write().await;
    if let Some(m) = met.get_mut(endpoint_id) {
        m.active_conns += 1;
    }
}

/// Giảm số lượng kết nối đang bận
async fn decrement_active_connections(pool: &MailServerPool, endpoint_id: &str) {
    let mut met = pool.metrics.write().await;
    if let Some(m) = met.get_mut(endpoint_id) {
        if m.active_conns > 0 {
            m.active_conns -= 1;
        }
    }
}

/// Xây dựng đối tượng SmtpTransport từ SmtpEndpointSync Protobuf config
fn build_smtp_transport(config: &mail_proto::SmtpEndpointSync) -> Result<SmtpTransport, String> {
    let tls_parameters = match config.tls_mode.as_str() {
        "tls" | "mtls" | "starttls" => {
            let mut tls_builder = TlsParametersBuilder::new(config.host.clone());
            tls_builder = tls_builder.certificate_store(CertificateStore::None);

            if let Some(ref ca_pem) = config.ca_cert_pem {
                if !ca_pem.is_empty() {
                    let cert = Certificate::from_pem(ca_pem.as_bytes())
                        .map_err(|e| format!("Failed to parse CA Pem: {}", e))?;
                    tls_builder = tls_builder.add_root_certificate(cert);
                }
            }

            if config.tls_mode == "mtls" {
                if let (Some(ref client_cert), Some(ref client_key)) =
                    (&config.client_cert_pem, &config.client_key_pem)
                {
                    if !client_cert.is_empty() && !client_key.is_empty() {
                        let identity =
                            Identity::from_pem(client_cert.as_bytes(), client_key.as_bytes())
                                .map_err(|e| format!("Failed to build mTLS Identity: {}", e))?;
                        tls_builder = tls_builder.identify_with(identity);
                    } else {
                        return Err("mTLS enabled but client cert or key is empty".to_string());
                    }
                } else {
                    return Err("mTLS enabled but client cert or key is missing".to_string());
                }
            }

            let params = tls_builder
                .build()
                .map_err(|e| format!("Failed to build TLS Parameters: {}", e))?;
            Some(params)
        }
        _ => None,
    };

    let connection_security = match config.tls_mode.as_str() {
        "tls" | "mtls" => {
            if let Some(params) = tls_parameters {
                Tls::Required(params)
            } else {
                return Err("TLS parameters missing for SSL connection".to_string());
            }
        }
        "starttls" => {
            if let Some(params) = tls_parameters {
                Tls::Opportunistic(params)
            } else {
                return Err("STARTTLS parameters missing for opportunistic connection".to_string());
            }
        }
        _ => Tls::None,
    };

    let mut transport_builder = SmtpTransport::builder_dangerous(&config.host)
        .port(config.port as u16)
        .tls(connection_security)
        .timeout(Some(Duration::from_secs(5)));

    if !config.username.is_empty() && !config.password.is_empty() {
        transport_builder = transport_builder.credentials(Credentials::new(
            config.username.clone(),
            config.password.clone(),
        ));
    }

    Ok(transport_builder.build())
}

/// Thực thi nghiệp vụ gửi thư điện tử giao dịch bằng lettre
fn execute_send_email(transport: &SmtpTransport, raw_payload: Vec<u8>) -> Result<String, String> {
    // Giải mã email cấu hình từ SendMailConfig
    let mail_config = match mail_proto::SendMailConfig::decode(raw_payload.as_slice()) {
        Ok(c) => c,
        Err(e) => return Err(format!("Failed to decode SendMailConfig: {}", e)),
    };

    // Tạo email message để chuyển đi
    let email = match lettre::Message::builder()
        .from("noreply@aurora.system".parse().unwrap()) // Default system sender
        .to(mail_config
            .to
            .parse()
            .map_err(|e| format!("Invalid recipient address: {}", e))?)
        .subject(mail_config.subject)
        .header(lettre::message::header::ContentType::TEXT_HTML)
        .body(mail_config.body_html)
    {
        Ok(m) => m,
        Err(e) => return Err(format!("Failed to construct lettre Message: {}", e)),
    };

    // Gửi thư thông qua SmtpTransport (chặn đồng bộ ở thread spawn_blocking)
    match transport.send(&email) {
        Ok(response) => Ok(format!(
            "Email delivered successfully. SMTP Response: {:?}",
            response
        )),
        Err(e) => Err(format!("SMTP delivery error: {}", e)),
    }
}
