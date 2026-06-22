use std::sync::Arc;
use tonic::{Request, Response, Status};
use uuid::Uuid;
use crate::core::session::SessionManager;
use crate::core::token::{TokenManager, Claims};
use crate::observability::logger::Logger;

// Import protobuf sinh ra từ proto/session.proto
pub mod session_proto {
    tonic::include_proto!("iam.rpc");
}

use session_proto::{
    session_service_server::SessionService,
    IssueTrinitySessionRequest,
    IssueTrinitySessionResponse,
};

pub struct SessionServiceImpl {
    session_mgr: Arc<SessionManager>,
    token_mgr: Arc<TokenManager>,
    session_ttl_secs: u64,
}

impl SessionServiceImpl {
    pub fn new(
        session_mgr: Arc<SessionManager>,
        token_mgr: Arc<TokenManager>,
        session_ttl_secs: u64,
    ) -> Self {
        Self {
            session_mgr,
            token_mgr,
            session_ttl_secs,
        }
    }
}

#[tonic::async_trait]
impl SessionService for SessionServiceImpl {
    // Triển khai RPC cấp mới Trinity Session & lưu Redis L2
    async fn issue_trinity_session(
        &self,
        request: Request<IssueTrinitySessionRequest>,
    ) -> Result<Response<IssueTrinitySessionResponse>, Status> {
        let req = request.into_inner();

        Logger::sys_info(
            "session.issue",
            &format!("Issuing new trinity session for user_id={}", req.user_id),
        );

        // 1. Sinh Access Key (UUIDv7) và Access Secret (UUIDv4) để làm Trinity credentials
        let access_key = Uuid::now_v7().to_string();
        let access_secret = Uuid::new_v4().to_string();

        // 2. Tính hash của access_secret (SHA-256) lưu vào L2 Cache
        let ash = sha256_hash(&access_secret);

        // 3. Chuẩn bị Claims cho JWT Access Token
        let now_unix = chrono::Utc::now().timestamp();
        let exp_unix = now_unix + self.session_ttl_secs as i64;

        let claims = Claims {
            sub: req.user_id.clone(),
            role: req.role.clone(),
            lvl: req.level,
            tenant_id: if req.tenant_id.is_empty() { None } else { Some(req.tenant_id.clone()) },
            zone_id: if req.zone_id.is_empty() { None } else { Some(req.zone_id.clone()) },
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
                    "session.issue",
                    "Failed to sign access token via Vault",
                    &e.to_string(),
                );
                return Err(Status::internal(format!("Failed to sign access token: {}", e)));
            }
        };

        // 5. Ghi session lên Redis L2 qua SessionManager (Stateful Verification)
        if let Err(e) = self.session_mgr.register_session(
            &req.user_id,
            &access_key,
            &ash,
            &req.device_id,
        ).await {
            Logger::sys_error(
                "session.issue",
                "Failed to register session in Redis",
                &e.to_string(),
            );
            return Err(Status::internal(format!("Failed to write session state: {}", e)));
        }

        // 6. Cấp Opaque Refresh Token nếu trust_device = true
        // Token này sẽ được chèn vào PostgreSQL phía Go (IAM) để quản lý vòng đời lưu trữ
        let refresh_token = if req.trust_device {
            Uuid::new_v4().to_string().replace("-", "")
        } else {
            String::new()
        };

        // 7. Giải quyết client_device_id
        let client_device_id = if req.client_device_id.trim().is_empty() {
            Uuid::new_v4().to_string()
        } else {
            req.client_device_id
        };

        Logger::sys_info(
            "session.issue",
            &format!("Session issued successfully for user_id={} with access_key={}", req.user_id, access_key),
        );

        Ok(Response::new(IssueTrinitySessionResponse {
            access_token,
            refresh_token,
            access_key,
            access_secret,
            client_device_id,
            expires_in_secs: self.session_ttl_secs as i64,
        }))
    }
}

// Helper: Băm SHA-256 mã hóa access_secret
fn sha256_hash(secret: &str) -> String {
    use sha2::{Sha256, Digest};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}
