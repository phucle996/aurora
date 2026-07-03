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
use crate::error::AcrError;
use crate::infra::controlplane::ControlPlaneClient;
use crate::observability::logger::Logger;
use crate::observability::otel::OtelTracer;
use crate::service::tenant::manager::TenantManager;

pub struct ExtAuthzService {
    session_mgr: Arc<SessionManager>,
    token_mgr: Arc<TokenManager>,
    evaluator: Arc<PolicyEvaluator>,
    config: Config,
    // [COMMENT]: Client gRPC để gọi không đồng bộ sang Control Plane
    control_plane_client: Arc<ControlPlaneClient>,
    zone_mgr: Arc<ZoneManager>,
    tenant_mgr: Arc<TenantManager>,
    // [COMMENT]: Bộ giới hạn tần suất tích hợp tại biên
    rate_limiter: Arc<crate::service::ratelimit::RateLimiter>,
}

impl ExtAuthzService {
    pub fn new(
        session_mgr: Arc<SessionManager>,
        token_mgr: Arc<TokenManager>,
        evaluator: Arc<PolicyEvaluator>,
        config: Config,
        control_plane_client: Arc<ControlPlaneClient>,
        zone_mgr: Arc<ZoneManager>,
        tenant_mgr: Arc<TenantManager>,
    ) -> Self {
        let rate_limiter = Arc::new(crate::service::ratelimit::RateLimiter::new(
            session_mgr.clone(),
        ));
        Self {
            session_mgr,
            token_mgr,
            evaluator,
            config,
            control_plane_client,
            zone_mgr,
            tenant_mgr,
            rate_limiter,
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

                // [COMMENT]: Gọi handle_admin_login để xử lý chặn bắt và đăng nhập SRE Admin tại biên
                if let Some(admin_login_res) =
                    crate::service::login::admin_login_handler::handle_admin_login(
                        &self.session_mgr,
                        &self.token_mgr,
                        &self.zone_mgr,
                        &self.config,
                        client_headers,
                        &req,
                        &method,
                        &path,
                    )
                    .await
                {
                    return admin_login_res;
                }

                // [COMMENT]: Chặn bắt và xác thực session SRE Admin tại biên
                if let Some(admin_session_res) =
                    crate::service::session::verifier::handle_admin_session_check(
                        &self.session_mgr,
                        &self.token_mgr,
                        client_headers,
                        &method,
                        &path,
                    )
                    .await
                {
                    return admin_session_res;
                }

                // [COMMENT]: Chặn bắt và xác thực session User tại biên
                if let Some(user_session_res) =
                    crate::service::session::verifier::handle_user_session_check(
                        &self.session_mgr,
                        &self.token_mgr,
                        &self.zone_mgr,
                        &self.control_plane_client,
                        &self.config,
                        client_headers,
                        &method,
                        &path,
                    )
                    .await
                {
                    return user_session_res;
                }

                // [COMMENT]: Gọi handle_login để xử lý chặn bắt và xử lý đăng nhập trực tiếp tại biên
                if let Some(login_res) = crate::service::login::login_handler::handle_login(
                    &self.session_mgr,
                    &self.token_mgr,
                    &self.control_plane_client,
                    &self.zone_mgr,
                    &self.config,
                    client_headers,
                    &req,
                    &method,
                    &path,
                )
                .await
                {
                    return login_res;
                }

                // [COMMENT]: Gọi handle_zone_catalog từ module zone_catalog để xử lý API danh mục tại biên
                if let Some(catalog_res) = crate::service::zone::zone_catalog::handle_zone_catalog(
                    &self.session_mgr,
                    &self.token_mgr,
                    &self.zone_mgr,
                    client_headers,
                    &method,
                    &path,
                )
                .await
                {
                    return catalog_res;
                }

                // [COMMENT]: Chặn bắt và xử lý API chuyển Active Zone cho SRE Admin tại biên (Edge Termination)
                if let Some(zone_switch_res) =
                    crate::service::zone::zone_switch::handle_zone_switch(
                        &self.session_mgr,
                        &self.token_mgr,
                        &self.zone_mgr,
                        &self.config,
                        client_headers,
                        &req,
                        &method,
                        &path,
                    )
                    .await
                {
                    return zone_switch_res;
                }

                // [COMMENT]: Chặn bắt và xử lý API chuyển Active Tenant tại biên
                if let Some(tenant_switch_res) =
                    crate::service::tenant::tenant_switch::handle_tenant_switch(
                        &self.session_mgr,
                        &self.token_mgr,
                        &self.tenant_mgr,
                        &self.config,
                        client_headers,
                        &method,
                        &path,
                    )
                    .await
                {
                    return tenant_switch_res;
                }

                // [COMMENT]: Chặn bắt và xử lý API chuyển Active Zone cho SRE Admin tại biên (Edge Termination)
                if let Some(admin_zone_switch_res) =
                    crate::service::zone::zone_switch::handle_admin_zone_switch(
                        &self.session_mgr,
                        &self.token_mgr,
                        &self.zone_mgr,
                        &self.config,
                        client_headers,
                        &req,
                        &method,
                        &path,
                    )
                    .await
                {
                    return admin_zone_switch_res;
                }

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
                if let Some(revoke_res) = crate::service::session::revoke_session::handle_logout(
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

                // [COMMENT]: Gọi handle_admin_logout để xử lý đăng xuất cho SRE Admin tại biên
                if let Some(admin_revoke_res) =
                    crate::service::session::revoke_session::handle_admin_logout(
                        &self.session_mgr,
                        &self.token_mgr,
                        client_headers,
                        &method,
                        &path,
                    )
                    .await
                {
                    return admin_revoke_res;
                }

                // Trích xuất cookie từ HTTP header để phục vụ cho xác thực và khôi phục
                let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

                // [COMMENT]: Chặn nhanh (Fast-Bypass) nếu token nằm trong L1 Block Cache của Rate Limiter
                if let Some(jwt_token) = extract_cookie_value(&cookie_header, "access_token") {
                    let cache_key = sha256_hash(&jwt_token);
                    if self.rate_limiter.is_blocked(&cache_key).await {
                        Logger::sys_warn(
                            "ext_authz.check",
                            "Fast-bypass rate limit block triggered",
                            &cache_key,
                        );
                        return Ok(Response::new(CheckResponse::with_status(
                            Status::resource_exhausted("Rate limit exceeded"),
                        )));
                    }
                }

                // [COMMENT]: Thực hiện xác thực an toàn CSRF chống tấn công cross-site forgery trên session dùng cookie
                if let Err(err_msg) = crate::service::csrf::verify_csrf(
                    &method,
                    &path,
                    &cookie_header,
                    client_headers,
                    &self.config,
                ) {
                    return Ok(Response::new(CheckResponse::with_status(
                        Status::permission_denied(err_msg),
                    )));
                }

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

                    let mut dev_pubkey = "".to_string();

                    // [COMMENT]: Kiểm tra session trạng thái trong Redis L2 (Stateful Verification)
                    let session = if claims.is_admin() {
                        let admin_sess = match self.session_mgr.get_admin_session(&access_key).await
                        {
                            Ok(Some(s)) => s,
                            Ok(None) => return Err("Session Expired or Revoked"),
                            Err(e) => {
                                Logger::sys_error(
                                    "ext_authz.check",
                                    "Redis error while validating admin session",
                                    &e.to_string(),
                                );
                                return Err("Authentication service unavailable");
                            }
                        };
                        // [COMMENT]: Lưu khóa công khai của thiết bị phục vụ kiểm tra chữ ký ở bước sau
                        dev_pubkey = admin_sess.device_public_key.clone();
                        crate::core::session::UserAccessSession {
                            ash: admin_sess.access_secret_hash,
                            tdid: "global".to_string(),
                            lsa: chrono::Utc::now().timestamp(),
                        }
                    } else {
                        match self.session_mgr.get_session(
                            claims.zone_id.as_deref().unwrap_or("global"),
                            claims.tenant_id.as_deref().unwrap_or("global"),
                            &claims.uid,
                            &access_key,
                        ).await {
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

                    Ok((claims, session, access_key, dev_pubkey))
                };

                let is_critical_admin = path.contains("/critical/");

                let auth_result = if is_critical_admin {
                    // [COMMENT]: Trích xuất mã OTP từ header x-admin-stepup-code
                    let otp_code = client_headers
                        .get("x-admin-stepup-code")
                        .map(|s| s.as_str().trim().to_string())
                        .unwrap_or_default();

                    if otp_code.is_empty()
                        || otp_code.len() != 6
                        || !otp_code.chars().all(|c| c.is_ascii_digit())
                    {
                        Logger::sys_warn(
                            "ext_authz.check",
                            "Missing or invalid SRE OTP code for critical endpoint",
                            &path,
                        );
                        return Ok(Response::new(CheckResponse::with_status(
                            Status::unauthenticated("Missing or invalid SRE OTP code"),
                        )));
                    }

                    // [COMMENT]: Chạy song song: Xác thực session (trinity) và gọi Vault verify OTP
                    let trinity_verify = auth_result;
                    let otp_verify = self.token_mgr.verify_admin_totp(&otp_code);

                    match tokio::join!(trinity_verify, otp_verify) {
                        (Ok((claims, session, access_key, dev_pubkey)), Ok(true)) => {
                            if !claims.is_admin() {
                                Logger::sys_warn(
                                    "ext_authz.check",
                                    "Non-admin attempted to access critical endpoint",
                                    &claims.sub,
                                );
                                return Ok(Response::new(CheckResponse::with_status(
                                    Status::permission_denied(
                                        "Only SRE Admin can access critical endpoints",
                                    ),
                                )));
                            }
                            Ok((claims, session, access_key, dev_pubkey))
                        }
                        (Err(err_msg), _) => Err(err_msg),
                        (_, Ok(false)) => Err("Invalid OTP code"),
                        (_, Err(e)) => {
                            Logger::sys_error(
                                "ext_authz.check",
                                "Vault TOTP verification failed",
                                &e.to_string(),
                            );
                            Err("Authentication service temporarily unavailable")
                        }
                    }
                } else {
                    auth_result.await
                };

                let (mut claims, session, access_key, device_public_key) = match auth_result {
                    Ok(val) => val,
                    Err(err_msg) => {
                        // [COMMENT]: SRE Admin không sử dụng Refresh Token để khôi phục session.
                        // Trả về trực tiếp 401 Unauthorized và ghi log đúng ngữ cảnh.
                        if is_critical_admin {
                            Logger::authz_log(
                                "unknown",
                                &method,
                                &path,
                                "DENIED",
                                &format!("SRE Admin critical auth failed: {}. Session recovery bypassed.", err_msg),
                            );
                            let mut denied_builder = envoy_types::ext_authz::v3::DeniedHttpResponseBuilder::new();
                            denied_builder.set_http_status(envoy_types::ext_authz::v3::pb::HttpStatusCode::Unauthorized);
                            let mut response = CheckResponse::new();
                            response.set_status(Status::unauthenticated(err_msg));
                            response.set_http_response(denied_builder);
                            return Ok(Response::new(response));
                        }

                        // [COMMENT]: Nếu không mang theo Refresh Token -> Không thể phục hồi session, trả về 401 trực tiếp.
                        // Điều này cũng đúng với SRE Admin khi gọi các API thường (như /api/v1/auth/session) vì Admin không có Refresh Token.
                        let refresh_token = extract_cookie_value(&cookie_header, "refresh_token");
                        if refresh_token.is_none() {
                            Logger::authz_log(
                                "unknown",
                                &method,
                                &path,
                                "DENIED",
                                &format!("Authentication failed: {}. No refresh token for recovery.", err_msg),
                            );
                            let mut denied_builder = envoy_types::ext_authz::v3::DeniedHttpResponseBuilder::new();
                            denied_builder.set_http_status(envoy_types::ext_authz::v3::pb::HttpStatusCode::Unauthorized);
                            // Xóa bộ ba Trinity Access Cookies để đồng bộ trạng thái client
                            let cookies_to_clear = vec![
                                "access_token=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                                "access_key=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                                "access_secret=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                            ];
                            for cookie in cookies_to_clear {
                                denied_builder.add_header("set-cookie", cookie, None, false);
                            }
                            let mut response = CheckResponse::new();
                            response.set_status(Status::unauthenticated(err_msg));
                            response.set_http_response(denied_builder);
                            return Ok(Response::new(response));
                        }

                        // [COMMENT]: Khi xác thực Trinity Cookie thất bại, chuyển hướng sang luồng khôi phục session bằng Refresh Token
                        return crate::service::session::recovery_session::handle_session_recovery(
                            &self.session_mgr,
                            &self.token_mgr,
                            &self.zone_mgr,
                            &self.control_plane_client,
                            &self.config,
                            &cookie_header,
                            client_headers,
                            err_msg,
                            &method,
                            &path,
                        )
                        .await;
                    }
                };

                // [COMMENT]: 4. Thực hiện Rate Limit (Post-Auth) tích hợp L1 Block Cache
                if let Some(jwt_token) = extract_cookie_value(&cookie_header, "access_token") {
                    let cache_key = sha256_hash(&jwt_token);
                    if let Err(status) = self
                        .rate_limiter
                        .check_rate_limit(&claims, &cache_key, is_critical_admin, &path)
                        .await
                    {
                        return Ok(Response::new(CheckResponse::with_status(status)));
                    }
                }

                // [COMMENT]: Nếu là endpoint critical của SRE, kiểm tra chữ ký số thiết bị và chống Replay
                if is_critical_admin {
                    if let Err(status) = crate::service::signature::verify_admin_signature(
                        &self.session_mgr,
                        client_headers,
                        &req,
                        &method,
                        &path,
                        &device_public_key,
                        &access_key,
                    )
                    .await
                    {
                        return Ok(Response::new(CheckResponse::with_status(status)));
                    }
                }

                // [COMMENT]: 3.5. Phân giải và xác thực thông tin Zone thông qua dịch vụ zone_resolution tùy theo vai trò
                let cookies_to_set_zone = if claims.is_admin() {
                    match crate::service::zone::zone_resolution::resolve_and_verify_zone_admin(
                        &self.zone_mgr,
                        Some(&mut claims),
                        &cookie_header,
                        client_headers,
                        &method,
                        &path,
                    )
                    .await
                    {
                        Ok(cookies) => cookies,
                        Err(err_res) => return err_res,
                    }
                } else {
                    match crate::service::zone::zone_resolution::resolve_and_verify_zone_user(
                        &self.zone_mgr,
                        Some(&mut claims),
                        &cookie_header,
                        client_headers,
                        &method,
                        &path,
                    )
                    .await
                    {
                        Ok(cookies) => cookies,
                        Err(err_res) => return err_res,
                    }
                };

                // [COMMENT]: 3.6. Phân giải và xác thực thông tin Tenant thông qua dịch vụ tenant_resolution tùy theo vai trò
                let cookies_to_set_tenant = if claims.is_admin() {
                    match crate::service::tenant::tenant_resolution::resolve_and_verify_tenant_admin(
                        &self.tenant_mgr,
                        Some(&mut claims),
                        &cookie_header,
                        client_headers,
                        &method,
                        &path,
                    )
                    .await
                    {
                        Ok(cookies) => cookies,
                        Err(err_res) => return err_res,
                    }
                } else {
                    match crate::service::tenant::tenant_resolution::resolve_and_verify_tenant_user(
                        &self.tenant_mgr,
                        Some(&mut claims),
                        &cookie_header,
                        client_headers,
                        &method,
                        &path,
                    )
                    .await
                    {
                        Ok(cookies) => cookies,
                        Err(err_res) => return err_res,
                    }
                };

                // [COMMENT]: 6. Xử lý cập nhật Last Seen At (Throttled ghi) - Chỉ áp dụng cho user thường, bỏ qua cho SRE Admin
                if !claims.is_admin() {
                    let _ = self
                        .session_mgr
                        .update_last_seen(
                            claims.zone_id.as_deref().unwrap_or("global"),
                            claims.tenant_id.as_deref().unwrap_or("global"),
                            &claims.uid,
                            &access_key,
                            session.lsa,
                        )
                        .await;
                }

                // 7. Tạo ngữ cảnh danh tính AuthContext phục vụ đánh giá quyền hạn
                let auth_ctx = AuthContext {
                    user_id: claims.uid.clone(),
                    device_id: session.tdid.clone(),
                    tenant_id: claims.tenant_id.clone(),
                    roles: claims.get_roles(),
                };

                // 8. Chạy bộ đánh giá quyền (PolicyEvaluator) - Hỗ trợ scale RBAC/ABAC/IP
                if let Err(e) = self.evaluator.evaluate(&auth_ctx, &req_ctx).await {
                    return match e {
                        AcrError::Forbidden(msg) => {
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

                // [COMMENT]: 9. Thực hiện xoay vòng session (Sliding Session) tương ứng cho Admin hoặc User thường
                let mut cookies_to_set = if claims.is_admin() {
                    crate::service::session::rotate_session::handle_admin_session_rotation(
                        &self.session_mgr,
                        &self.token_mgr,
                        &self.config,
                        &claims,
                        &access_key,
                    )
                    .await
                } else {
                    crate::service::session::rotate_session::handle_session_rotation(
                        &self.session_mgr,
                        &self.token_mgr,
                        &self.config,
                        &claims,
                        &session,
                        &access_key,
                    )
                    .await
                };

                // Hợp nhất các cookie cập nhật zone & tenant
                cookies_to_set.extend(cookies_to_set_zone);
                cookies_to_set.extend(cookies_to_set_tenant);

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
                                value: claims.uid.clone(),
                                ..Default::default()
                            }),
                            ..Default::default()
                        });
                        ok.headers.push(HeaderValueOption {
                            header: Some(HeaderValue {
                                key: "x-user-name".to_string(),
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
                        if !claims.role.is_empty() {
                            ok.headers.push(HeaderValueOption {
                                header: Some(HeaderValue {
                                    key: "x-user-role".to_string(),
                                    value: claims.role.clone(),
                                    ..Default::default()
                                }),
                                ..Default::default()
                            });
                        }
                        ok.headers.push(HeaderValueOption {
                            header: Some(HeaderValue {
                                key: "x-user-level".to_string(),
                                value: claims.lvl.to_string(),
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

                        // [COMMENT]: Chỉ inject header x-zone-id cho microservices upstream, không trả x-zone-code
                        if let Some(ref z_id) = claims.zone_id {
                            ok.headers.push(HeaderValueOption {
                                header: Some(HeaderValue {
                                    key: "x-zone-id".to_string(),
                                    value: z_id.clone(),
                                    ..Default::default()
                                }),
                                ..Default::default()
                            });
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
