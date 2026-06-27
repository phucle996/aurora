use crate::core::session::SessionManager;
use crate::core::token::{Claims, TokenManager};
use crate::observability::logger::Logger;
use std::sync::Arc;
use tonic::{Request, Response, Status};
use uuid::Uuid;

// Import protobuf sinh ra từ proto/session.proto
pub mod session_proto {
    tonic::include_proto!("iam.rpc");
}

use session_proto::{
    session_service_server::SessionService, ReleaseTrinitySessionRequest,
    ReleaseTrinitySessionResponse,
};

pub struct SessionServiceImpl {
    session_mgr: Arc<SessionManager>,
    token_mgr: Arc<TokenManager>,
    zone_mgr: Arc<crate::core::zone::ZoneManager>,
    // [COMMENT]: Lưu trữ cấu hình đầy đủ thay vì chỉ lưu session_ttl_secs để sử dụng cho domain & cookies
    config: crate::config::Config,
}

impl SessionServiceImpl {
    pub fn new(
        session_mgr: Arc<SessionManager>,
        token_mgr: Arc<TokenManager>,
        zone_mgr: Arc<crate::core::zone::ZoneManager>,
        config: crate::config::Config,
    ) -> Self {
        Self {
            session_mgr,
            token_mgr,
            zone_mgr,
            config,
        }
    }
}

#[tonic::async_trait]
impl SessionService for SessionServiceImpl {
    // Triển khai RPC cấp mới Trinity Session & lưu Redis L2
    async fn release_trinity_session(
        &self,
        request: Request<ReleaseTrinitySessionRequest>,
    ) -> Result<Response<ReleaseTrinitySessionResponse>, Status> {
        let req = request.into_inner();

        // [COMMENT]: Đọc refresh token trực tiếp từ proto request field (không qua metadata header nữa)
        let refresh_token = req.refresh_token.clone();

        Logger::sys_info(
            "session.release",
            &format!("Releasing new trinity session for user_id={}", req.user_id),
        );

        // 1. Sinh Access Key (UUIDv7) và Access Secret (UUIDv4) để làm Trinity credentials
        let access_key = Uuid::now_v7().to_string();
        let access_secret = Uuid::new_v4().to_string();

        // 2. Tính hash của access_secret (SHA-256) lưu vào L2 Cache
        let ash = sha256_hash(&access_secret);

        // 3. Chuẩn bị Claims cho JWT Access Token
        let now_unix = chrono::Utc::now().timestamp();
        let exp_unix = now_unix + self.config.session_ttl_secs as i64;

        let claims = Claims {
            sub: req.user_id.clone(),
            role: req.role.clone(),
            lvl: req.level,
            tenant_id: if req.tenant_id.is_empty() {
                None
            } else {
                Some(req.tenant_id.clone())
            },
            // [COMMENT]: Phân giải zone_id: Nếu là UUID hợp lệ thì giữ nguyên.
            // Nếu là zone code (ví dụ "vn", "sg"), gọi ZoneManager L1/CP để phân giải sang UUID.
            zone_id: if req.zone_id.is_empty() {
                None
            } else if Uuid::parse_str(&req.zone_id).is_ok() {
                Some(req.zone_id.clone())
            } else {
                self.zone_mgr.resolve_code_to_id(&req.zone_id).await
            },
            access_key: access_key.clone(),
            jti: Uuid::new_v4().to_string(),
            iss: Some("aurora-acl".to_string()),
            exp: exp_unix,
            iat: now_unix,
        };

        // 4. Ký JWT thông qua Vault Transit Engine (Stateless Verification)
        let access_token = match self.token_mgr.generate_token(&claims).await {
            Ok(token) => token,
            Err(e) => {
                Logger::sys_error(
                    "session.release",
                    "Failed to sign access token via Vault",
                    &e.to_string(),
                );
                return Err(Status::internal(format!(
                    "Failed to sign access token: {}",
                    e
                )));
            }
        };

        // 5. Ghi session lên Redis L2 qua SessionManager (Stateful Verification)
        if let Err(e) = self
            .session_mgr
            .register_session(&req.user_id, &access_key, &ash, &req.device_id)
            .await
        {
            Logger::sys_error(
                "session.release",
                "Failed to register session in Redis",
                &e.to_string(),
            );
            return Err(Status::internal(format!(
                "Failed to write session state: {}",
                e
            )));
        }

        // 7. Giải quyết client_device_id
        let client_device_id = if req.client_device_id.trim().is_empty() {
            Uuid::new_v4().to_string()
        } else {
            req.client_device_id
        };

        Logger::sys_info(
            "session.release",
            &format!(
                "Session released successfully for user_id={} with access_key={}",
                req.user_id, access_key
            ),
        );

        // [COMMENT]: Khởi tạo gRPC response với released = true
        // Toàn bộ cookie và header x-client-device-id sẽ được truyền ngược lại qua gRPC response metadata
        let mut response = Response::new(ReleaseTrinitySessionResponse {
            released: true,
        });

        // Cấu hình cookie domain
        let domain_str = if self.config.app_public_domain.trim().is_empty() {
            "".to_string()
        } else {
            format!("; Domain={}", self.config.app_public_domain.trim())
        };

        // 1. Cấu hình Cookie cho trinity_access_token (JWT)
        let access_cookie = format!(
            "access_token={}; Path=/{}; HttpOnly; Secure; SameSite=Lax; Max-Age={}",
            access_token, domain_str, self.config.session_ttl_secs
        );
        if let Ok(val) = tonic::metadata::MetadataValue::try_from(&access_cookie) {
            response.metadata_mut().append("set-cookie", val);
        }

        // 2. Cấu hình Cookie cho trinity_access_key
        let key_cookie = format!(
            "access_key={}; Path=/{}; HttpOnly; Secure; SameSite=Lax; Max-Age={}",
            access_key, domain_str, self.config.session_ttl_secs
        );
        if let Ok(val) = tonic::metadata::MetadataValue::try_from(&key_cookie) {
            response.metadata_mut().append("set-cookie", val);
        }

        // 3. Cấu hình Cookie cho trinity_access_secret
        let secret_cookie = format!(
            "access_secret={}; Path=/{}; HttpOnly; Secure; SameSite=Lax; Max-Age={}",
            access_secret, domain_str, self.config.session_ttl_secs
        );
        if let Ok(val) = tonic::metadata::MetadataValue::try_from(&secret_cookie) {
            response.metadata_mut().append("set-cookie", val);
        }

        // 4. Cấu hình Cookie cho client_device_id (365 ngày, không HttpOnly để UI đọc được nếu cần)
        let cdid_cookie = format!(
            "client_device_id={}; Path=/{}; Secure; SameSite=Lax; Max-Age=31536000",
            client_device_id, domain_str
        );
        if let Ok(val) = tonic::metadata::MetadataValue::try_from(&cdid_cookie) {
            response.metadata_mut().append("set-cookie", val);
        }

        // 5. Cấu hình Cookie cho trinity_refresh_token (nếu có, 30 ngày)
        if !refresh_token.is_empty() {
            let refresh_cookie = format!(
                "refresh_token={}; Path=/{}; HttpOnly; Secure; SameSite=Lax; Max-Age=2592000",
                refresh_token, domain_str
            );
            if let Ok(val) = tonic::metadata::MetadataValue::try_from(&refresh_cookie) {
                response.metadata_mut().append("set-cookie", val);
            }
        }

        // 6. Trả thêm header X-Client-Device-Id cho Envoy
        if let Ok(val) = tonic::metadata::MetadataValue::try_from(&client_device_id) {
            response.metadata_mut().insert("x-client-device-id", val);
        }

        Ok(response)
    }
}

// Helper: Băm SHA-256 mã hóa access_secret
fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}
