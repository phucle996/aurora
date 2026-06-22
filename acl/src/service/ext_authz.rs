use std::sync::Arc;
use tonic::{Request, Response, Status};

// Import các struct từ crate envoy-types (đúng path cho envoy-types 0.2)
use envoy_types::ext_authz::v3::{CheckRequestExt, CheckResponseExt};
use envoy_types::pb::envoy::service::auth::v3::{
    authorization_server::Authorization, CheckRequest, CheckResponse,
};

use crate::authz::evaluator::PolicyEvaluator;
use crate::authz::{AuthContext, RequestContext};
use crate::config::Config;
use crate::core::session::SessionManager;
use crate::core::token::TokenManager;
use crate::core::zone::ZoneManager;
use crate::error::AclError;
use crate::infra::controlplane::ControlPlaneClient;
use crate::observability::logger::Logger;
use crate::observability::otel::OtelTracer;

pub struct ExtAuthzService {
    session_mgr: Arc<SessionManager>,
    token_mgr: Arc<TokenManager>,
    evaluator: Arc<PolicyEvaluator>,
    config: Config,
    // [COMMENT]: Client gRPC để gọi không đồng bộ sang Control Plane
    control_plane_client: Arc<ControlPlaneClient>,
    zone_mgr: Arc<ZoneManager>,
}

impl ExtAuthzService {
    pub fn new(
        session_mgr: Arc<SessionManager>,
        token_mgr: Arc<TokenManager>,
        evaluator: Arc<PolicyEvaluator>,
        config: Config,
        control_plane_client: Arc<ControlPlaneClient>,
        zone_mgr: Arc<ZoneManager>,
    ) -> Self {
        Self {
            session_mgr,
            token_mgr,
            evaluator,
            config,
            control_plane_client,
            zone_mgr,
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

        // 1. Trích xuất Trace ID từ HTTP headers của Envoy để binding vào scope của async task
        let trace_id = if let Some(headers) = req.get_client_headers() {
            let traceparent = headers.get("traceparent").map(|s| s.as_str()).unwrap_or("");
            if let Some(span_ctx) = OtelTracer::parse_traceparent(traceparent) {
                span_ctx.trace_id().to_string()
            } else {
                let x_req_id = headers
                    .get("x-request-id")
                    .map(|s| s.as_str())
                    .unwrap_or("");
                if !x_req_id.is_empty() {
                    x_req_id.to_string()
                } else {
                    uuid::Uuid::new_v4().simple().to_string()
                }
            }
        } else {
            uuid::Uuid::new_v4().simple().to_string()
        };

        crate::observability::otel::CURRENT_TRACE_ID
            .scope(trace_id, async move {
                // 2. Trích xuất HTTP attributes từ request của Envoy
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
                if self
                    .config
                    .bypass_endpoints
                    .iter()
                    .any(|endpoint| path.starts_with(endpoint))
                {
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

                // [COMMENT]: Gọi handle_revoke_session từ module revoke_session đã được tách biệt
                if let Some(revoke_res) = crate::service::revoke_session::handle_logout(
                    &self.session_mgr,
                    &self.token_mgr,
                    &self.control_plane_client,
                    client_headers,
                    &method,
                    &path,
                )
                .await
                {
                    return revoke_res;
                }

                // Trích xuất cookie từ HTTP header để phục vụ cho xác thực và khôi phục
                let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

                // 3. Thực hiện xác thực Trinity Credentials thô qua cookie và Redis L2
                let auth_result = async {
                    // Lấy access_token JWT từ cookie
                    let jwt_token = extract_cookie_value(&cookie_header, "access_token")
                        .ok_or("Missing access_token cookie")?;

                    // Lấy access_key từ cookie (Key dùng để lookup session trong Redis L2)
                    let access_key = extract_cookie_value(&cookie_header, "access_key")
                        .ok_or("Missing access_key cookie")?;

                    // Giải mã và verify JWT Token (Stateless Verification)
                    let claims = self.token_mgr.verify_token(&jwt_token).await.map_err(|e| {
                        Logger::sys_debug(
                            "ext_authz.check",
                            &format!("JWT verification failed: {}", e),
                        );
                        "Invalid access_token"
                    })?;

                    // Kiểm tra xem access_key trong token có khớp với cookie không (chống replay attack)
                    if claims.access_key != access_key {
                        return Err("Access Key Mismatch");
                    }

                    // Kiểm tra session trạng thái trong Redis L2 (Stateful Verification)
                    let session = match self.session_mgr.get_session(&claims.sub, &access_key).await
                    {
                        Ok(Some(s)) => s,
                        Ok(None) => return Err("Session Expired or Revoked"),
                        Err(e) => {
                            Logger::sys_error(
                                "ext_authz.check",
                                "Redis error while validating session",
                                &e.to_string(),
                            );
                            return Err("Authentication service unavailable");
                        }
                    };

                    // Trích xuất access_secret từ cookie HttpOnly để làm mảnh thứ 3 bảo mật chống XSS
                    let access_secret = extract_cookie_value(&cookie_header, "access_secret")
                        .ok_or("Missing access_secret cookie")?;

                    // Đối chiếu hash SHA-256 của access_secret nhận được với session.ash trong Redis L2
                    let incoming_hash = sha256_hash(&access_secret);
                    if session.ash != incoming_hash {
                        return Err("Access Secret Mismatch");
                    }

                    Ok((claims, session, access_key))
                }
                .await;

                let (mut claims, session, access_key) = match auth_result {
                    Ok(val) => val,
                    Err(err_msg) => {
                        return crate::service::recovery_session::handle_session_recovery(
                            &self.session_mgr,
                            &self.token_mgr,
                            &self.control_plane_client,
                            &self.config,
                            &cookie_header,
                            err_msg,
                            &method,
                            &path,
                        )
                        .await;
                    }
                };

                // [COMMENT]: 3.5. Xác định, phân giải và kiểm tra tính hợp lệ của Zone (L1 Cache + gRPC Controlplane)
                let mut cookies_to_set_zone = Vec::new();
                
                // Trích xuất zone code / zone id được chỉ định từ request cookies hoặc custom headers
                let mut requested_zone_code = extract_cookie_value(&cookie_header, "zone_code");
                if requested_zone_code.is_none() {
                    requested_zone_code = client_headers.get("x-zone-code").cloned()
                        .or_else(|| client_headers.get("X-Zone-Code").cloned());
                }
                let requested_zone_id = client_headers.get("x-zone-id").cloned()
                    .or_else(|| client_headers.get("X-Zone-Id").cloned());

                let mut resolved_zone_id = None;
                let mut resolved_zone_code = None;

                if let Some(ref code) = requested_zone_code {
                    // Phân giải zone code thành zone id
                    if let Some(id) = self.zone_mgr.resolve_code_to_id(code).await {
                        resolved_zone_id = Some(id);
                        resolved_zone_code = Some(code.clone());
                    } else {
                        // Gửi zone_code không hợp lệ -> chặn request ngay
                        Logger::authz_log(&claims.sub, &method, &path, "DENIED", &format!("Requested zone code not found: {}", code));
                        return Ok(Response::new(CheckResponse::with_status(
                            Status::invalid_argument("Requested zone code not found"),
                        )));
                    }
                } else if let Some(ref id) = requested_zone_id {
                    // Phân giải zone id thành zone code
                    if let Some(code) = self.zone_mgr.resolve_id_to_code(id).await {
                        resolved_zone_id = Some(id.clone());
                        resolved_zone_code = Some(code);
                    } else {
                        // Gửi zone_id không hợp lệ -> chặn request
                        Logger::authz_log(&claims.sub, &method, &path, "DENIED", &format!("Requested zone ID not found: {}", id));
                        return Ok(Response::new(CheckResponse::with_status(
                            Status::invalid_argument("Requested zone ID not found"),
                        )));
                    }
                } else if let Some(ref claims_zone_id) = claims.zone_id {
                    // Fallback về zone trong JWT claims nếu không có tham số request cụ thể
                    if let Some(code) = self.zone_mgr.resolve_id_to_code(claims_zone_id).await {
                        resolved_zone_id = Some(claims_zone_id.clone());
                        resolved_zone_code = Some(code);
                    }
                }

                // Nếu tìm thấy thông tin Zone hợp lệ
                if let (Some(zone_id), Some(zone_code)) = (resolved_zone_id, resolved_zone_code) {
                    // So sánh sự không khớp giữa zone trong claims hiện tại và zone phân giải được
                    let claims_mismatch = claims.zone_id.as_ref() != Some(&zone_id);
                    let cookie_mismatch = extract_cookie_value(&cookie_header, "zone_code").as_ref() != Some(&zone_code);

                    if claims_mismatch {
                        // Ký lại token mới với claims.zone_id được cập nhật để downstream sử dụng
                        let mut updated_claims = claims.clone();
                        updated_claims.zone_id = Some(zone_id.clone());
                        match self.token_mgr.generate_token(&updated_claims).await {
                            Ok(new_jwt) => {
                                Logger::sys_info(
                                    "ext_authz.zone",
                                    &format!("Switching active zone of user_id={} to zone_id={}, zone_code={}", claims.sub, zone_id, zone_code)
                                );
                                cookies_to_set_zone.push(format!(
                                    "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}",
                                    new_jwt, self.config.session_ttl_secs
                                ));
                                cookies_to_set_zone.push(format!(
                                    "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000",
                                    zone_code
                                ));
                                // Cập nhật claims trong bộ nhớ hiện tại
                                claims.zone_id = Some(zone_id);
                            }
                            Err(e) => {
                                Logger::sys_error(
                                    "ext_authz.zone",
                                    "Failed to generate access token with updated zone_id",
                                    &e.to_string(),
                                );
                            }
                        }
                    } else if cookie_mismatch {
                        // Nếu JWT đã khớp zone_id nhưng thiếu/sai cookie zone_code, set lại cookie cho đồng bộ
                        cookies_to_set_zone.push(format!(
                            "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000",
                            zone_code
                        ));
                    }
                }

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
                let mut cookies_to_set = crate::service::rotate_session::handle_session_rotation(
                    &self.session_mgr,
                    &self.token_mgr,
                    &self.config,
                    &claims,
                    &session,
                    &access_key,
                )
                .await;
                
                // Hợp nhất các cookie cập nhật zone
                cookies_to_set.extend(cookies_to_set_zone);

                // 10. Xây dựng response OK cho Envoy
                Logger::authz_log(&claims.sub, &method, &path, "ALLOWED", "Passed all checks");

                // Dựng response OK với headers inject vào upstream
                let mut ok_response = CheckResponse::with_status(Status::ok("authorized"));
                ok_response
                    .set_http_response(envoy_types::ext_authz::v3::pb::OkHttpResponse::default());

                // Chèn thông tin danh tính cho microservice Upstream qua HTTP Headers
                // Sử dụng trực tiếp mutable access vào ok_response
                if let Some(ref mut http_resp) = ok_response.http_response {
                    use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;
                    if let HttpResponse::OkResponse(ref mut ok) = http_resp {
                        use envoy_types::pb::envoy::config::core::v3::{
                            HeaderValue, HeaderValueOption,
                        };
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
                        
                        // Inject zone headers nếu có
                        if let Some(ref z_id) = claims.zone_id {
                            ok.headers.push(HeaderValueOption {
                                header: Some(HeaderValue {
                                    key: "x-zone-id".to_string(),
                                    value: z_id.clone(),
                                    ..Default::default()
                                }),
                                ..Default::default()
                            });
                            if let Some(z_code) = self.zone_mgr.resolve_id_to_code(z_id).await {
                                ok.headers.push(HeaderValueOption {
                                    header: Some(HeaderValue {
                                        key: "x-zone-code".to_string(),
                                        value: z_code,
                                        ..Default::default()
                                    }),
                                    ..Default::default()
                                });
                            }
                        }

                        // Inject Set-Cookie headers cho sliding session refresh & zone updates
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
            })
            .await
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
