use std::sync::Arc;
use tonic::{Request, Response, Status};

// Import các struct từ crate envoy-types (đúng path cho envoy-types 0.2)
use envoy_types::pb::envoy::service::auth::v3::{
    authorization_server::Authorization, CheckRequest, CheckResponse,
};
// Import extension traits tiện lợi cho CheckRequest/CheckResponse
use envoy_types::ext_authz::v3::{CheckRequestExt, CheckResponseExt};

use crate::authz::evaluator::PolicyEvaluator;
use crate::authz::{AuthContext, RequestContext};
use crate::config::Config;
use crate::core::session::SessionManager;
use crate::core::token::TokenManager;
use crate::error::AclError;
use crate::infra::controlplane::ControlPlaneClient;
use crate::observability::logger::Logger;

pub struct ExtAuthzService {
    session_mgr: Arc<SessionManager>,
    token_mgr: Arc<TokenManager>,
    evaluator: Arc<PolicyEvaluator>,
    config: Config,
    // [COMMENT]: Client gRPC để gọi không đồng bộ sang Control Plane
    control_plane_client: Arc<ControlPlaneClient>,
}

impl ExtAuthzService {
    pub fn new(
        session_mgr: Arc<SessionManager>,
        token_mgr: Arc<TokenManager>,
        evaluator: Arc<PolicyEvaluator>,
        config: Config,
        control_plane_client: Arc<ControlPlaneClient>,
    ) -> Self {
        Self {
            session_mgr,
            token_mgr,
            evaluator,
            config,
            control_plane_client,
        }
    }
}

#[tonic::async_trait]
impl Authorization for ExtAuthzService {
    // Hàm xử lý chính kiểm tra quyền của mỗi request từ Envoy
    async fn check(
        &self,
        request: Request<CheckRequest>,
    ) -> Result<Response<CheckResponse>, Status> {
        let req = request.into_inner();

        // 1. Trích xuất HTTP attributes từ request của Envoy
        let client_headers = match req.get_client_headers() {
            Some(h) => h,
            None => {
                Logger::authz_log(
                    "unknown",
                    "?",
                    "?",
                    "DENIED",
                    "Missing HTTP context from Envoy",
                );
                return Ok(Response::new(CheckResponse::with_status(
                    Status::invalid_argument("Missing HTTP context"),
                )));
            }
        };

        // Lấy path và method từ pseudo-headers
        let path = client_headers.get(":path").cloned().unwrap_or_default();
        let method = client_headers
            .get(":method")
            .cloned()
            .unwrap_or_else(|| "GET".to_string());
        let client_ip = client_headers
            .get("x-forwarded-for")
            .cloned()
            .unwrap_or_else(|| "unknown".to_string());

        let req_ctx = RequestContext {
            path: path.clone(),
            method: method.clone(),
            client_ip,
        };

        Logger::sys_debug(
            "ext_authz.check",
            &format!("Intercepting: {} {}", method, path),
        );

        // [COMMENT]: Cho phép Bypass (Không kiểm tra) đối với các endpoint Public cấu hình từ môi trường
        // Sử dụng self.config.bypass_endpoints để kiểm tra tiền tố đường dẫn (starts_with)
        if self.config.bypass_endpoints.iter().any(|endpoint| path.starts_with(endpoint)) {
            Logger::authz_log(
                "anonymous",
                &method,
                &path,
                "ALLOWED",
                "Public endpoint bypass",
            );
            return Ok(Response::new(CheckResponse::with_status(Status::ok(
                "public endpoint",
            ))));
        }

        // [COMMENT]: Gọi handle_logout từ module logout đã được tách biệt
        if let Some(logout_res) = crate::service::logout::handle_logout(
            &self.session_mgr,
            &self.token_mgr,
            &self.control_plane_client,
            client_headers,
            &method,
            &path,
        )
        .await
        {
            return logout_res;
        }

        // 3. Trích xuất cookie từ HTTP header
        let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

        // Lấy access_token JWT từ cookie
        let jwt_token = match extract_cookie_value(&cookie_header, "access_token") {
            Some(t) => t,
            None => {
                Logger::authz_log(
                    "unknown",
                    &method,
                    &path,
                    "DENIED",
                    "Missing access_token cookie",
                );
                return Ok(Response::new(CheckResponse::with_status(
                    Status::unauthenticated("Missing access_token cookie"),
                )));
            }
        };

        // Lấy access_key từ cookie (Key dùng để lookup session trong Redis L2)
        let access_key = match extract_cookie_value(&cookie_header, "access_key") {
            Some(k) => k,
            None => {
                Logger::authz_log(
                    "unknown",
                    &method,
                    &path,
                    "DENIED",
                    "Missing access_key cookie",
                );
                return Ok(Response::new(CheckResponse::with_status(
                    Status::unauthenticated("Missing access_key cookie"),
                )));
            }
        };

        // 4. Giải mã và verify JWT Token (Stateless Verification)
        let claims = match self.token_mgr.verify_token(&jwt_token).await {
            Ok(c) => c,
            Err(e) => {
                Logger::authz_log(
                    "unknown",
                    &method,
                    &path,
                    "DENIED",
                    &format!("JWT verification failed: {}", e),
                );
                return Ok(Response::new(CheckResponse::with_status(
                    Status::unauthenticated("Invalid access_token"),
                )));
            }
        };

        // Kiểm tra xem access_key trong token có khớp với cookie không (chống replay attack)
        if claims.access_key != access_key {
            Logger::authz_log(&claims.sub, &method, &path, "DENIED", "Access key mismatch");
            return Ok(Response::new(CheckResponse::with_status(
                Status::unauthenticated("Access Key Mismatch"),
            )));
        }

        // 5. Kiểm tra session trạng thái trong Redis L2 (Stateful Verification)
        let session = match self.session_mgr.get_session(&claims.sub, &access_key).await {
            Ok(Some(s)) => s,
            Ok(None) => {
                Logger::authz_log(
                    &claims.sub,
                    &method,
                    &path,
                    "DENIED",
                    "Session expired or revoked",
                );
                return Ok(Response::new(CheckResponse::with_status(
                    Status::unauthenticated("Session Expired or Revoked"),
                )));
            }
            Err(e) => {
                Logger::sys_error(
                    "ext_authz.check",
                    "Redis error while validating session",
                    &e.to_string(),
                );
                // Fail-Closed: từ chối nếu hạ tầng session gặp sự cố bảo mật
                return Ok(Response::new(CheckResponse::with_status(
                    Status::unavailable("Authentication service unavailable"),
                )));
            }
        };

        // 6. Xử lý cập nhật Last Seen At (Throttled ghi)
        let _ = self
            .session_mgr
            .update_last_seen(&claims.sub, &access_key, session.lsa)
            .await;

        // 7. Tạo ngữ cảnh danh tính AuthContext phục vụ đánh giá quyền hạn
        let auth_ctx = AuthContext {
            user_id: claims.sub.clone(),
            device_id: session.tdid.clone(),
            tenant_id: claims.tenant_id.clone(),
            roles: claims.get_roles(),
        };

        // 8. Chạy bộ đánh giá quyền (PolicyEvaluator) - Hỗ trợ scale RBAC/ABAC/IP
        if let Err(e) = self.evaluator.evaluate(&auth_ctx, &req_ctx).await {
            return match e {
                AclError::Forbidden(msg) => {
                    Logger::authz_log(&claims.sub, &method, &path, "FORBIDDEN", &msg);
                    Ok(Response::new(CheckResponse::with_status(
                        Status::permission_denied(msg),
                    )))
                }
                _ => {
                    Logger::sys_error(
                        "ext_authz.check",
                        "Authorization processing error",
                        &e.to_string(),
                    );
                    Ok(Response::new(CheckResponse::with_status(Status::internal(
                        "Authorization processing error",
                    ))))
                }
            };
        }

        // [COMMENT]: Gọi handle_session_rotation từ module rotate đã được tách biệt
        let cookies_to_set = crate::service::rotate::handle_session_rotation(
            &self.session_mgr,
            &self.token_mgr,
            &self.config,
            &claims,
            &session,
            &access_key,
        )
        .await;

        // 10. Xây dựng response OK cho Envoy
        Logger::authz_log(&claims.sub, &method, &path, "ALLOWED", "Passed all checks");

        // Dựng response OK với headers inject vào upstream
        let mut ok_response = CheckResponse::with_status(Status::ok("authorized"));
        ok_response.set_http_response(envoy_types::ext_authz::v3::pb::OkHttpResponse::default());

        // Chèn thông tin danh tính cho microservice Upstream qua HTTP Headers
        // Sử dụng trực tiếp mutable access vào ok_response
        if let Some(ref mut http_resp) = ok_response.http_response {
            use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;
            if let HttpResponse::OkResponse(ref mut ok) = http_resp {
                use envoy_types::pb::envoy::config::core::v3::{HeaderValue, HeaderValueOption};
                // Inject identity headers
                ok.headers.push(HeaderValueOption {
                    header: Some(HeaderValue {
                        key: "x-user-id".to_string(),
                        value: claims.sub.clone(),
                        ..Default::default()
                    }),
                    ..Default::default()
                });
                ok.headers.push(HeaderValueOption {
                    header: Some(HeaderValue {
                        key: "x-device-id".to_string(),
                        value: session.tdid,
                        ..Default::default()
                    }),
                    ..Default::default()
                });
                if let Some(t_id) = claims.tenant_id {
                    ok.headers.push(HeaderValueOption {
                        header: Some(HeaderValue {
                            key: "x-tenant-id".to_string(),
                            value: t_id,
                            ..Default::default()
                        }),
                        ..Default::default()
                    });
                }

                // Inject Set-Cookie headers cho sliding session refresh
                for cookie in cookies_to_set {
                    ok.response_headers_to_add.push(HeaderValueOption {
                        header: Some(HeaderValue {
                            key: "set-cookie".to_string(),
                            value: cookie,
                            ..Default::default()
                        }),
                        ..Default::default()
                    });
                }
            }
        }

        Ok(Response::new(ok_response))
    }
}

// Helper: Phân tích cookie string để trích xuất value của một key cụ thể
pub(crate) fn extract_cookie_value(cookie_str: &str, key: &str) -> Option<String> {
    for part in cookie_str.split(';') {
        let part = part.trim();
        // Đảm bảo key khớp chính xác (tránh trường hợp "new_access_key" khớp với "access_key")
        if let Some(value) = part.strip_prefix(key) {
            if let Some(value) = value.strip_prefix('=') {
                return Some(value.to_string());
            }
        }
    }
    None
}

// Helper: Băm SHA-256 mã hóa access_secret
pub(crate) fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}
