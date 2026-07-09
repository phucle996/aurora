// ======================================================================================================
// 📂 MODULE: acl/src/service/auth.rs
//            Triển Khai gRPC Server AuthService cho Rust ACL
// ======================================================================================================
//
// 📜 THIẾT KẾ & TÁCH BIỆT TRÁCH NHIỆM:
//   - Tự động hóa xác minh Trinity Token cho User và Admin/SRE tại tầng ACL mà không cần
//     gọi gRPC sang Go Controlplane (Data Plane / Auth Gate).
//   - Delegating (Proxying) các cuộc gọi xác thực Opaque Refresh Token sang Go Controlplane
//     vì chúng yêu cầu truy cập trực tiếp Database PostgreSQL.
//
// ======================================================================================================

use prost::Message;
use std::sync::Arc;
use tonic::{Request, Response, Status};

use crate::core::session::{AdminAccessSession, SessionManager};
use crate::core::token::TokenManager;
use crate::infra::nats::Nats;
use crate::observability::logger::Logger;

// [COMMENT]: Nạp các cấu trúc tự động sinh từ proto/auth.proto & proto/session.proto
#[allow(dead_code)]
#[allow(unused_imports)]
pub mod auth_proto {
    tonic::include_proto!("iam.rpc");
}

use auth_proto::{
    auth_service_server::AuthService, RevokeOpaqueRefreshTokenRequest,
    RevokeOpaqueRefreshTokenResponse, VerifyOpaqueRefreshTokenRequest,
    VerifyOpaqueRefreshTokenResponse, VerifyUserCredentialsRequest, VerifyUserCredentialsResponse,
};

#[derive(Clone)]
pub struct AuthServiceImpl {
    session_mgr: Arc<SessionManager>,
    token_mgr: Arc<TokenManager>,
    redis_client: Arc<redis::Client>,
    nats: Arc<Nats>,
}

impl AuthServiceImpl {
    pub fn new(
        session_mgr: Arc<SessionManager>,
        token_mgr: Arc<TokenManager>,
        redis_client: Arc<redis::Client>,
        nats: Arc<Nats>,
    ) -> Self {
        Self {
            session_mgr,
            token_mgr,
            redis_client,
            nats,
        }
    }

    // [COMMENT]: Hỗ trợ kết nối bất đồng bộ sang Redis để lấy thông tin phiên admin
    async fn get_redis_connection(&self) -> Result<redis::aio::Connection, Status> {
        self.redis_client
            .get_tokio_connection()
            .await
            .map_err(|e| Status::internal(format!("Failed to get Redis connection: {}", e)))
    }

    // [COMMENT]: Xác thực Trinity credentials cho Admin/SRE quản trị hệ thống (gọi qua NATS)
    pub async fn verify_admin_trinity_token(
        &self,
        req: crate::infra::nats::trinity::VerifyAdminTrinityTokenRequest,
    ) -> Result<crate::infra::nats::trinity::VerifyAdminTrinityTokenResponse, Status> {
        // 1. Kiểm tra nhanh các trường rỗng đầu vào
        if req.access_token.is_empty() || req.access_key.is_empty() || req.access_secret.is_empty()
        {
            return Ok(
                crate::infra::nats::trinity::VerifyAdminTrinityTokenResponse {
                    valid: false,
                    admin_id: String::new(),
                },
            );
        }

        // 2. Giải mã và xác thực token JWT stateless qua TokenManager (Vault)
        let claims = match self.token_mgr.verify_token(&req.access_token).await {
            Ok(c) => c,
            Err(e) => {
                Logger::sys_warn(
                    "auth.verify_admin",
                    &format!("Admin token verification failed: {}", e),
                    "invalid_token",
                );
                return Ok(
                    crate::infra::nats::trinity::VerifyAdminTrinityTokenResponse {
                        valid: false,
                        ..Default::default()
                    },
                );
            }
        };

        // 3. Đối chiếu access_key trong token với access_key client cung cấp
        if claims.access_key.is_empty() || claims.access_key != req.access_key {
            return Ok(
                crate::infra::nats::trinity::VerifyAdminTrinityTokenResponse {
                    valid: false,
                    ..Default::default()
                },
            );
        }

        // 4. Kiểm tra tính hoạt động của session từ Redis L2 cache (Bỏ zone_id khỏi key theo kiến trúc tĩnh HA)
        let redis_key = format!("iam:admin_access_session:{}", req.access_key);
        let mut conn = self.get_redis_connection().await?;

        let data: Option<Vec<u8>> = redis::cmd("GET")
            .arg(&redis_key)
            .query_async(&mut conn)
            .await
            .map_err(|e| {
                Logger::sys_error(
                    "auth.verify_admin",
                    "GET admin session failed",
                    &e.to_string(),
                );
                Status::internal("GET admin session failed")
            })?;

        let session_data = match data {
            Some(bytes) => bytes,
            None => {
                return Ok(
                    crate::infra::nats::trinity::VerifyAdminTrinityTokenResponse {
                        valid: false,
                        ..Default::default()
                    },
                );
            }
        };

        let session = match AdminAccessSession::decode(session_data.as_slice()) {
            Ok(s) => s,
            Err(e) => {
                Logger::sys_error(
                    "auth.verify_admin",
                    "Protobuf decode admin session failed",
                    &e.to_string(),
                );
                return Err(Status::internal("Protobuf decode admin session failed"));
            }
        };

        // 5. So sánh SHA256 hash của access_secret nhận được
        let incoming_hash = sha256_hash(&req.access_secret);
        if session.access_secret_hash != incoming_hash {
            return Ok(
                crate::infra::nats::trinity::VerifyAdminTrinityTokenResponse {
                    valid: false,
                    ..Default::default()
                },
            );
        }

        Ok(
            crate::infra::nats::trinity::VerifyAdminTrinityTokenResponse {
                valid: true,
                admin_id: claims.sub.clone(),
            },
        )
    }

    // [COMMENT]: Xác thực Trinity credentials cho người dùng (End-User) thông thường (gọi qua NATS)
    pub async fn verify_user_trinity_token(
        &self,
        req: crate::infra::nats::trinity::VerifyUserTrinityTokenRequest,
    ) -> Result<crate::infra::nats::trinity::VerifyUserTrinityTokenResponse, Status> {
        // 1. Kiểm tra nhanh các trường rỗng đầu vào
        if req.access_token.is_empty() || req.access_key.is_empty() || req.access_secret.is_empty()
        {
            return Ok(
                crate::infra::nats::trinity::VerifyUserTrinityTokenResponse {
                    valid: false,
                    user_id: String::new(),
                },
            );
        }

        // 2. Giải mã và xác thực token JWT stateless qua TokenManager (Vault)
        let claims = match self.token_mgr.verify_token(&req.access_token).await {
            Ok(c) => c,
            Err(e) => {
                Logger::sys_warn(
                    "auth.verify_user",
                    &format!("User token verification failed: {}", e),
                    "invalid_token",
                );
                return Ok(
                    crate::infra::nats::trinity::VerifyUserTrinityTokenResponse {
                        valid: false,
                        ..Default::default()
                    },
                );
            }
        };

        // 3. Đối chiếu access_key trong token với access_key client cung cấp
        if claims.access_key.is_empty() || claims.access_key != req.access_key {
            return Ok(
                crate::infra::nats::trinity::VerifyUserTrinityTokenResponse {
                    valid: false,
                    ..Default::default()
                },
            );
        }

        let session = match self
            .session_mgr
            .get_session(
                claims.zone_id.as_deref().unwrap_or("global"),
                // [COMMENT]: Sử dụng "platform" thay vì "global" làm fallback cho tenant_id
                claims.tenant_id.as_deref().unwrap_or("platform"),
                &claims.uid,
                &req.access_key,
            )
            .await
        {
            Ok(Some(s)) => s,
            Ok(None) => {
                return Ok(
                    crate::infra::nats::trinity::VerifyUserTrinityTokenResponse {
                        valid: false,
                        ..Default::default()
                    },
                );
            }
            Err(e) => {
                Logger::sys_error(
                    "auth.verify_user",
                    "Failed to retrieve session from Redis",
                    &e.to_string(),
                );
                return Err(Status::internal("Failed to retrieve session"));
            }
        };

        // 5. So sánh SHA256 hash của access_secret nhận được
        let incoming_hash = sha256_hash(&req.access_secret);
        if session.ash != incoming_hash {
            return Ok(
                crate::infra::nats::trinity::VerifyUserTrinityTokenResponse {
                    valid: false,
                    ..Default::default()
                },
            );
        }

        Ok(
            crate::infra::nats::trinity::VerifyUserTrinityTokenResponse {
                valid: true,
                user_id: claims.uid.clone(),
            },
        )
    }

    // [COMMENT]: Giải mã VerifyUserTrinityTokenRequest từ bytes, xử lý xác thực, và trả về bytes
    pub async fn verify_user_trinity_token_bytes(&self, payload: &[u8]) -> Vec<u8> {
        use prost::Message;
        let req = match crate::infra::nats::trinity::VerifyUserTrinityTokenRequest::decode(payload)
        {
            Ok(r) => r,
            Err(e) => {
                Logger::sys_error(
                    "auth.verify_user",
                    "Failed to decode VerifyUserTrinityTokenRequest",
                    &e.to_string(),
                );
                return vec![];
            }
        };

        let res = match self.verify_user_trinity_token(req).await {
            Ok(r) => r,
            Err(_) => crate::infra::nats::trinity::VerifyUserTrinityTokenResponse {
                valid: false,
                user_id: String::new(),
            },
        };

        let mut reply_payload = Vec::new();
        if res.encode(&mut reply_payload).is_ok() {
            reply_payload
        } else {
            vec![]
        }
    }

    // [COMMENT]: Giải mã VerifyAdminTrinityTokenRequest từ bytes, xử lý xác thực, và trả về bytes
    pub async fn verify_admin_trinity_token_bytes(&self, payload: &[u8]) -> Vec<u8> {
        use prost::Message;
        let req = match crate::infra::nats::trinity::VerifyAdminTrinityTokenRequest::decode(payload)
        {
            Ok(r) => r,
            Err(e) => {
                Logger::sys_error(
                    "auth.verify_admin",
                    "Failed to decode VerifyAdminTrinityTokenRequest",
                    &e.to_string(),
                );
                return vec![];
            }
        };

        let res = match self.verify_admin_trinity_token(req).await {
            Ok(r) => r,
            Err(_) => crate::infra::nats::trinity::VerifyAdminTrinityTokenResponse {
                valid: false,
                admin_id: String::new(),
            },
        };

        let mut reply_payload = Vec::new();
        if res.encode(&mut reply_payload).is_ok() {
            reply_payload
        } else {
            vec![]
        }
    }
}

#[tonic::async_trait]
impl AuthService for AuthServiceImpl {
    // [COMMENT]: Xác thực Opaque Refresh Token (Ủy thác sang Go Controlplane qua NATS)
    async fn verify_opaque_refresh_token(
        &self,
        request: Request<VerifyOpaqueRefreshTokenRequest>,
    ) -> Result<Response<VerifyOpaqueRefreshTokenResponse>, Status> {
        let req = request.into_inner();

        let mut payload = Vec::new();
        req.encode(&mut payload)
            .map_err(|e| Status::internal(format!("Failed to encode request: {}", e)))?;

        let mut headers = async_nats::HeaderMap::new();
        if let Some(trace_id) = crate::observability::otel::OtelTracer::get_current_trace_id() {
            let span_id = uuid::Uuid::new_v4().simple().to_string()[..16].to_string();
            let traceparent = format!("00-{}-{}-01", trace_id, span_id);
            headers.insert("traceparent", traceparent.as_str());
        }

        let response_msg = match self
            .nats
            .client()
            .request_with_headers(
                "iam.auth.verify_opaque_token".to_string(),
                headers,
                payload.into(),
            )
            .await
        {
            Ok(msg) => msg,
            Err(e) => return Err(Status::unavailable(format!("NATS request failed: {}", e))),
        };

        let res = VerifyOpaqueRefreshTokenResponse::decode(response_msg.payload.as_ref())
            .map_err(|e| Status::internal(format!("Failed to decode response: {}", e)))?;

        Ok(Response::new(res))
    }

    // [COMMENT]: Thu hồi Opaque Refresh Token bất đồng bộ (Ủy thác sang Go Controlplane qua NATS)
    async fn revoke_opaque_refresh_token(
        &self,
        request: Request<RevokeOpaqueRefreshTokenRequest>,
    ) -> Result<Response<RevokeOpaqueRefreshTokenResponse>, Status> {
        let req = request.into_inner();

        let mut payload = Vec::new();
        req.encode(&mut payload)
            .map_err(|e| Status::internal(format!("Failed to encode request: {}", e)))?;

        let mut headers = async_nats::HeaderMap::new();
        if let Some(trace_id) = crate::observability::otel::OtelTracer::get_current_trace_id() {
            let span_id = uuid::Uuid::new_v4().simple().to_string()[..16].to_string();
            let traceparent = format!("00-{}-{}-01", trace_id, span_id);
            headers.insert("traceparent", traceparent.as_str());
        }

        match self
            .nats
            .client()
            .request_with_headers(
                "iam.auth.revoke_opaque_token".to_string(),
                headers,
                payload.into(),
            )
            .await
        {
            Ok(_) => {}
            Err(e) => return Err(Status::unavailable(format!("NATS request failed: {}", e))),
        };

        Ok(Response::new(RevokeOpaqueRefreshTokenResponse {}))
    }

    async fn verify_user_credentials(
        &self,
        _request: Request<VerifyUserCredentialsRequest>,
    ) -> Result<Response<VerifyUserCredentialsResponse>, Status> {
        Err(Status::unimplemented(
            "VerifyUserCredentials is not implemented on the ACL server side",
        ))
    }
}

// [COMMENT]: Helper: Băm SHA-256 mã hóa access_secret
fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}
