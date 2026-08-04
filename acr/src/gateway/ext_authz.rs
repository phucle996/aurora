// ======================================================================================================
// 📂 gateway/ext_authz.rs — Central Envoy Ext-Authz Dispatcher (Edge Ingress)
// ======================================================================================================

use std::sync::Arc;
use tonic::{Request, Response, Status};

use crate::config::Config;
use crate::gateway::csrf::verify_csrf_protection;
use crate::gateway::ratelimit::RateLimiter;
use crate::infra::redis::SessionManager;
use crate::infra::shared_redis::SharedRedisBus;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::pkg::header::*;
use crate::sre::claims::SreTokenManager;
use crate::token::TokenManager;
use crate::user::claims::Claims;
use crate::user::revoke::handle_logout;
use crate::user::tenant::{handle_tenant_switch, resolve_and_verify_tenant};
use crate::user::verify::verify_edge_session;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckRequestExt, CheckResponseExt};
use envoy_types::pb::envoy::service::auth::v3::{
    authorization_server::Authorization, CheckRequest, CheckResponse,
};

pub struct ExtAuthzService {
    session_mgr: Arc<SessionManager>,
    token_mgr: Arc<TokenManager>,
    sre_token_mgr: Arc<SreTokenManager>,
    config: Config,
    rate_limiter: Arc<RateLimiter>,
    shared_redis_client: Arc<redis::Client>,
    shared_redis: Arc<SharedRedisBus>,
    oauth: Arc<crate::user::oauth::OAuthProviderService>,
}

impl ExtAuthzService {
    pub fn new(
        session_mgr: Arc<SessionManager>,
        token_mgr: Arc<TokenManager>,
        sre_token_mgr: Arc<SreTokenManager>,
        config: Config,
        shared_redis_client: Arc<redis::Client>,
        shared_redis: Arc<SharedRedisBus>,
        oauth: Arc<crate::user::oauth::OAuthProviderService>,
    ) -> Self {
        let rate_limiter = Arc::new(RateLimiter::new(session_mgr.clone()));
        Self {
            session_mgr,
            token_mgr,
            sre_token_mgr,
            config,
            rate_limiter,
            shared_redis_client,
            shared_redis,
            oauth,
        }
    }
}

pub fn extract_cookie_value(cookie_header: &str, cookie_name: &str) -> Option<String> {
    for part in cookie_header.split(';') {
        let part = part.trim();
        if let Some(pos) = part.find('=') {
            let key = &part[..pos];
            let val = &part[pos + 1..];
            if key == cookie_name {
                return Some(val.to_string());
            }
        }
    }
    None
}

fn authority_matches_origin(authority: &str, configured_origin: &str) -> bool {
    let Ok(origin) = url::Url::parse(configured_origin) else {
        return false;
    };
    let Ok(request) = url::Url::parse(&format!("{}://{}", origin.scheme(), authority)) else {
        return false;
    };
    origin.host_str() == request.host_str()
        && origin.port_or_known_default() == request.port_or_known_default()
}

fn is_billing_alias_path(method: &str, path: &str, is_billing_console_authority: bool) -> bool {
    is_billing_console_authority
        && (path.starts_with("/api/v1/billing/")
            || (method == "GET" && path == "/api/v1/me/iam/context/read"))
}

fn is_neutral_owner_billing_path(path: &str) -> bool {
    path == "/api/v1/billing/wallet" || path.starts_with("/api/v1/billing/wallet/")
}

fn is_internal_owner_billing_path(path: &str) -> bool {
    path == "/api/v1/personal/billing"
        || path.starts_with("/api/v1/personal/billing/")
        || path == "/api/v1/tenant/billing"
        || path.starts_with("/api/v1/tenant/billing/")
}

fn is_payment_webhook_path(path: &str) -> bool {
    matches!(
        path,
        "/api/v1/billing/webhooks/personal/payment-settled"
            | "/api/v1/billing/webhooks/tenant/payment-settled"
    )
}

fn rewrite_owner_billing_path(path: &str, tenant_id: Option<&str>) -> Option<String> {
    let path_without_query = path.split('?').next().unwrap_or(path);
    if !is_neutral_owner_billing_path(path_without_query) {
        return None;
    }
    let suffix = &path["/api/v1/billing".len()..];
    let scope = if tenant_id.is_some_and(|value| !value.is_empty() && value != "platform") {
        "tenant"
    } else {
        "personal"
    };
    Some(format!("/api/v1/{scope}/billing{suffix}"))
}

pub fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}

#[tonic::async_trait]
impl Authorization for ExtAuthzService {
    async fn check(
        &self,
        request: Request<CheckRequest>,
    ) -> Result<Response<CheckResponse>, Status> {
        let req = request.into_inner();

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

        let http_req = req
            .attributes
            .as_ref()
            .and_then(|a| a.request.as_ref())
            .and_then(|r| r.http.as_ref());

        // [COMMENT]: Trích xuất path và method chính xác từ Envoy AttributeContext (http.path & http.method)
        // Tránh lỗi trượt đường dẫn do Envoy không truyền :path/:method dưới dạng header thông thường
        let path_str = http_req
            .map(|h| h.path.as_str())
            .or_else(|| client_headers.get(":path").map(|s| s.as_str()))
            .unwrap_or("/");
        let path = path_str;
        // Object names and query values are high-cardinality customer data.
        // Authorization logs retain the capability route without emitting them.
        let authz_log_path = if path
            .split('?')
            .next()
            .unwrap_or(path)
            .starts_with("/zone-control/v1/storage/")
        {
            "/zone-control/v1/storage/[redacted]"
        } else {
            path
        };

        let method_str = http_req
            .map(|h| h.method.as_str())
            .or_else(|| client_headers.get(":method").map(|s| s.as_str()))
            .unwrap_or("GET");
        let method = method_str;
        let authority = http_req
            .map(|h| h.host.as_str())
            .filter(|host| !host.is_empty())
            .or_else(|| client_headers.get(":authority").map(String::as_str))
            .or_else(|| client_headers.get("host").map(String::as_str))
            .unwrap_or("");
        // The same IAM self-context endpoint is consumed by both consoles. Host-aware selection
        // prevents a Cost alias from becoming a general-purpose Cloud IAM credential.
        let is_billing_console_authority =
            authority_matches_origin(authority, &self.config.billing_console_origin);

        let client_ip = client_headers
            .get("x-forwarded-for")
            .map(|s| s.as_str())
            .unwrap_or("unknown");

        // [COMMENT]: Zone/config resolution chỉ dùng rebuildable cache Redis; auth/session state vẫn qua SessionManager.
        let redis_client = self.shared_redis_client.clone();

        // 1. CORS Allowed Origin Check chạy cả với public route.
        if let Some(origin) = client_headers.get("origin") {
            if !self.config.allowed_origins.is_empty()
                && !self
                    .config
                    .allowed_origins
                    .iter()
                    .any(|o| o == origin || o == "*")
            {
                Logger::authz_log(
                    "system",
                    method,
                    authz_log_path,
                    "DENIED",
                    &format!("CORS origin '{}' not allowed", origin),
                );
                return Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("CORS origin not allowed"),
                )));
            }
        }

        // [COMMENT]: Trích xuất nhanh cookie_header và device_id để dùng chung cho cả Rate Limiter và các bộ xác thực phía sau.
        let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();
        let device_id = extract_cookie_value(&cookie_header, COOKIE_CLIENT_DEVICE_ID);

        // [COMMENT]: Tự động phân loại Route Group dựa trên đường dẫn path của request.
        let group = crate::gateway::ratelimit::detect_route_group(path);

        // [COMMENT]: Thực hiện Phase 1 Rate Limiting (Trước xác thực) dựa trên IP và Device ID.
        if !self
            .rate_limiter
            .check_pre_auth(client_ip, device_id.as_deref(), group)
            .await
        {
            return Ok(Response::new(CheckResponse::with_status(
                Status::resource_exhausted("Rate limit exceeded (Pre-Auth)"),
            )));
        }

        let path_without_query = path.split('?').next().unwrap_or(path);

        // ─── Local Interceptors ───────────────────────────────────────────────
        // [COMMENT]: Ưu tiên kiểm tra các Local Interceptors (Login Challenge, Login, Session Check...) tại tầng biên ACR trước.
        // Tránh trường hợp is_public_route bypass nhầm các endpoint local của ACR về Controlplane (gây ra 404).

        // [COMMENT]: Login challenge là endpoint local của ACR; request vẫn đã qua CORS và pre-auth rate limit.
        if let Some(res) =
            crate::user::login::handle_login_challenge(&self.session_mgr, method, path).await
        {
            return res;
        }

        if let Some(res) = crate::user::login::handle_mfa_verify(
            &self.session_mgr,
            &self.token_mgr,
            redis_client.as_ref(),
            &self.shared_redis,
            &self.config,
            client_headers,
            &req,
            method,
            path,
        )
        .await
        {
            return res;
        }

        if let Some(res) = self
            .oauth
            .handle(
                &self.session_mgr,
                &self.token_mgr,
                redis_client.as_ref(),
                &self.shared_redis,
                &self.config,
                client_headers,
                &req,
                method,
                path,
            )
            .await
        {
            return res;
        }

        // [COMMENT]: Public bypass chỉ exact method + path và chỉ sau CORS/rate limit; prefix không thể mở nhầm route con.
        let is_public_route = self.config.bypass_endpoints.iter().any(|entry| {
            if let Some((configured_method, configured_path)) = entry.split_once(' ') {
                configured_method.eq_ignore_ascii_case(method)
                    && configured_path == path_without_query
            } else {
                // Legacy env được giữ fail-closed theo các method công khai đã review.
                (entry == "/api/v1/health"
                    && method == "GET"
                    && matches!(
                        path_without_query,
                        "/api/v1/health/liveness"
                            | "/api/v1/health/readiness"
                            | "/api/v1/health/startup"
                    ))
                    || (entry == "/api/v1/auth/register"
                        && method == "POST"
                        && path_without_query == entry)
                    || (entry == "/api/v1/auth/verify"
                        && method == "POST"
                        && path_without_query == entry)
            }
        });
        if is_public_route {
            if is_payment_webhook_path(path_without_query) && !is_billing_console_authority {
                return Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Payment webhook authority mismatch"),
                )));
            }
            let mut response = CheckResponse::with_status(Status::ok("public route authorized"));
            response.set_http_response(
                envoy_types::pb::envoy::service::auth::v3::OkHttpResponse::default(),
            );
            return Ok(Response::new(response));
        }

        // [COMMENT]: Internal owner routes are reachable only through the
        // rewrite below. Rejecting a direct request prevents a caller from
        // choosing PERSONAL/TENANT by path before identity verification.
        if is_internal_owner_billing_path(path_without_query) {
            Logger::authz_log(
                "system",
                method,
                authz_log_path,
                "DENIED",
                "Direct internal Billing owner route",
            );
            return Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Internal Billing route"),
            )));
        }
        if path_without_query.starts_with("/api/v1/billing/")
            && !is_neutral_owner_billing_path(path_without_query)
            && !is_billing_console_authority
        {
            // [COMMENT]: Cloud may use only the neutral owner wallet surface.
            // Operator/auth Billing routes remain bound to the Cost authority.
            Logger::authz_log(
                "system",
                method,
                authz_log_path,
                "DENIED",
                "Billing route is not available on this authority",
            );
            return Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Billing authority mismatch"),
            )));
        }

        // 1. User Login: POST /api/v1/auth/login
        if let Some(res) = crate::user::login::handle_login(
            &self.session_mgr,
            &self.token_mgr,
            redis_client.as_ref(),
            &self.shared_redis,
            &self.config,
            client_headers,
            &req,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // 1b. User Session Check: GET /api/v1/me/session
        if let Some(res) = crate::user::verify::handle_user_session_check(
            &self.session_mgr,
            &self.token_mgr,
            redis_client.as_ref(),
            &self.shared_redis,
            &self.config,
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // [COMMENT]: Target exchange không dùng source cookie; one-time code atomic là credential duy nhất.
        if let Some(res) = crate::billing::exchange::handle_billing_handoff_exchange(
            &self.session_mgr,
            &self.config,
            client_headers,
            &req,
            method,
            path_without_query,
        )
        .await
        {
            return res;
        }

        // 2. Logout: POST /api/v1/auth/logout, POST /admin/auth/logout
        if let Some(res) = handle_logout(
            &self.session_mgr,
            &self.token_mgr,
            &self.shared_redis,
            &self.config,
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // 2b. Billing Logout: POST /api/v1/billing/auth/logout
        if let Some(res) = crate::billing::logout::handle_billing_logout(
            &self.session_mgr,
            &self.config,
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // 2d. Billing Session Check: GET /api/v1/billing/auth/session
        if let Some(res) = crate::billing::verify::handle_billing_session_check(
            &self.session_mgr,
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // [COMMENT]: 2c. SRE Logout: POST /admin/auth/logout
        if let Some(res) = crate::sre::logout::handle_sre_logout(
            &self.session_mgr,
            &self.sre_token_mgr,
            &self.config,
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // [COMMENT]: 2e. SRE Session Check: GET /admin/auth/session
        if let Some(res) = crate::sre::verify::handle_sre_session_check(
            &self.session_mgr,
            &self.sre_token_mgr,
            &self.config,
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // 3. SRE Admin Login: POST /admin/auth/login
        if let Some(res) = crate::sre::login::handle_admin_login(
            &self.session_mgr,
            &self.sre_token_mgr,
            redis_client.as_ref(),
            &self.config,
            client_headers,
            &req,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // 5. Zone Catalog Interceptors:
        // SRE Admin Zone Catalog
        if let Some(res) = crate::sre::zone_catalog::handle_admin_zone_catalog(
            &self.session_mgr,
            &self.sre_token_mgr,
            &self.shared_redis,
            redis_client.as_ref(),
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // User Zone Catalog
        if let Some(res) = crate::user::zone_catalog::handle_user_zone_catalog(
            &self.shared_redis,
            redis_client.as_ref(),
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // 6. User Zone Switch: POST /api/v1/zone/go-to-zone
        if let Some(res) = crate::user::zone_switcher::handle_user_zone_switch(
            &self.session_mgr,
            &self.token_mgr,
            &self.shared_redis,
            redis_client.as_ref(),
            &self.config,
            client_headers,
            &req,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // 6b. SRE Zone Switch: POST /admin/zone/go-to-zone
        if let Some(res) = crate::sre::zone_switcher::handle_sre_zone_switch(
            &self.session_mgr,
            &self.sre_token_mgr,
            &self.shared_redis,
            redis_client.as_ref(),
            &self.config,
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // 7. Tenant Switch: POST /api/v1/tenant/go-to-tenant
        if let Some(res) = handle_tenant_switch(
            &self.session_mgr,
            &self.token_mgr,
            &self.shared_redis,
            &self.config,
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // ─── Normal Auth Verification Flow ──────────────────────────────────

        // [COMMENT]: Đã trích xuất cookie_header ở đầu hàm check cho Phase 1 Rate Limiting nên không cần trích xuất lại tại đây.

        // Cost Console uses its host-bound Billing alias. Cloud Console keeps
        // Trinity even for the neutral Billing surface, so the same URL cannot
        // turn a Billing alias into a general Cloud credential.
        let is_billing =
            is_billing_alias_path(method, path_without_query, is_billing_console_authority);
        let is_sre = path.starts_with("/admin");

        let mut claims: Option<Claims> = None;
        let mut sre_claims: Option<crate::sre::claims::SreClaims> = None;
        let mut billing_alias: Option<crate::billing::session::BillingSessionAlias> = None;
        let access_key: String;
        let mut cookies_to_set = Vec::new();

        if is_billing {
            let verify_res = crate::billing::verify::verify_billing_alias(
                &self.session_mgr,
                &cookie_header,
                method,
                path,
            )
            .await;
            if let Some(denial) = verify_res.denial_response {
                return Ok(denial);
            }
            billing_alias = verify_res.alias;
            access_key = verify_res.alias_id;
        } else {
            if is_sre {
                let verify_res = crate::sre::verify::verify_sre_edge_session(
                    &self.session_mgr,
                    &self.sre_token_mgr,
                    &self.config,
                    &cookie_header,
                    client_headers,
                    method,
                    path,
                )
                .await;
                if let Some(denial) = verify_res.denial_response {
                    return Ok(denial);
                }
                sre_claims = verify_res.claims;
                access_key = verify_res.access_key;
                cookies_to_set.extend(verify_res.cookies_to_set);
            } else {
                let verify_res = verify_edge_session(
                    &self.session_mgr,
                    &self.token_mgr,
                    redis_client.as_ref(),
                    &self.shared_redis,
                    &self.config,
                    &cookie_header,
                    client_headers,
                    method,
                    path,
                )
                .await;
                if let Some(denial) = verify_res.denial_response {
                    return Ok(denial);
                }
                claims = verify_res.claims;
                access_key = verify_res.access_key;
                cookies_to_set.extend(verify_res.cookies_to_set);
            }
        }

        // [COMMENT]: Thực hiện Phase 2 Rate Limiting (Sau xác thực) dựa trên User ID và Device ID.
        let user_id = claims
            .as_ref()
            .map(|c| c.uid.as_str())
            .or_else(|| sre_claims.as_ref().map(|c| c.sub.as_str()))
            .or_else(|| billing_alias.as_ref().map(|alias| alias.user_id.as_str()))
            .unwrap_or("anonymous");

        if !self
            .rate_limiter
            .check_post_auth(user_id, device_id.as_deref(), group)
            .await
        {
            return Ok(Response::new(CheckResponse::with_status(
                Status::resource_exhausted("Rate limit exceeded (Post-Auth)"),
            )));
        }

        // CSRF Check
        if (claims.is_some() || sre_claims.is_some() || billing_alias.is_some())
            && !verify_csrf_protection(method, client_headers)
        {
            Logger::authz_log(
                "system",
                method,
                authz_log_path,
                "DENIED",
                "CSRF validation failed",
            );
            return Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("CSRF validation failed"),
            )));
        }

        // Zone Resolution & Boundaries
        let is_admin = sre_claims.is_some();

        let cookies_to_set_zone = if is_billing {
            // [COMMENT]: Billing zone đã bind khi exchange; không tin cookie/header zone do client gửi lại.
            Vec::new()
        } else if is_admin {
            match crate::sre::zone_resolution::resolve_and_verify_zone_admin(
                &self.shared_redis,
                redis_client.as_ref(),
                sre_claims.as_mut(),
                &cookie_header,
                client_headers,
                method,
                path,
            )
            .await
            {
                Ok(cookies) => cookies,
                Err(res) => return res,
            }
        } else {
            match crate::user::zone_resolution::resolve_and_verify_zone_user(
                &self.shared_redis,
                redis_client.as_ref(),
                claims.as_mut(),
                &cookie_header,
                client_headers,
                method,
                path,
            )
            .await
            {
                Ok(cookies) => cookies,
                Err(res) => return res,
            }
        };

        // Tenant Resolution
        if !is_billing {
            if let Err(res) = resolve_and_verify_tenant(
                claims.as_mut(),
                &cookie_header,
                client_headers,
                method,
                path,
            )
            .await
            {
                return res;
            }
        }

        // [COMMENT]: Source handoff chỉ được cấp sau full IAM session + CSRF + zone + tenant verification.
        if let Some(ref source_claims) = claims {
            if method == "POST" && path_without_query == "/api/v1/auth/domain-sessions/billing" {
                let source_session = match self
                    .session_mgr
                    .get_session(
                        source_claims.zone_id.as_deref().unwrap_or("global"),
                        source_claims.tenant_id.as_deref().unwrap_or("platform"),
                        &source_claims.uid,
                        &access_key,
                    )
                    .await
                {
                    Ok(Some(session)) => session,
                    _ => {
                        return Ok(Response::new(CheckResponse::with_status(
                            Status::unauthenticated("Source IAM session is unavailable"),
                        )))
                    }
                };
                if let Some(response) = crate::billing::exchange::handle_billing_handoff_issue(
                    &self.session_mgr,
                    &self.config,
                    source_claims,
                    &access_key,
                    &source_session,
                    &req,
                    method,
                    path_without_query,
                )
                .await
                {
                    return response;
                }
            }
        }

        // [COMMENT]: Challenge critical chỉ được cấp sau khi session, CSRF, zone và tenant đều đã được xác minh.
        if method == "POST"
            && ((claims.is_some() && path_without_query == "/api/v1/auth/session-proof/challenge")
                || (billing_alias.is_some()
                    && path_without_query == "/api/v1/billing/auth/session-proof/challenge"))
        {
            let challenge = match crate::user::session_proof::issue_critical_challenge(
                &self.session_mgr,
                &access_key,
            )
            .await
            {
                Ok(challenge) => challenge,
                Err(error) => {
                    Logger::sys_error(
                        "user.session_proof.challenge",
                        "Failed to issue critical challenge",
                        &error,
                    );
                    return Ok(Response::new(CheckResponse::with_status(
                        Status::unavailable("Session proof service unavailable"),
                    )));
                }
            };
            let body = serde_json::to_string(&challenge).unwrap_or_default();
            let mut builder = envoy_types::ext_authz::v3::DeniedHttpResponseBuilder::new();
            builder.set_http_status(HttpStatusCode::Ok);
            builder.add_header("content-type", "application/json", None, false);
            builder.set_body(body);
            let mut response = CheckResponse::new();
            // [COMMENT]: Local ACR endpoint phải trả non-OK gRPC status kèm denied response để Envoy không forward xuống upstream.
            response.set_status(Status::unauthenticated(
                "critical session proof challenge issued",
            ));
            response.set_http_response(builder);
            return Ok(Response::new(response));
        }

        // [COMMENT]: Mỗi mutation critical phải ký đúng method, original path và raw body bằng key đã bind vào session.
        let mut session_proof_challenge_id = None;
        if let Some(ref c) = claims {
            if path_without_query.starts_with("/api/v1/critical/")
                || path_without_query.starts_with("/api/v1/me/critical/")
            {
                let zone_id = c.zone_id.as_deref().unwrap_or("global");
                let tenant_id = c.tenant_id.as_deref().unwrap_or("platform");
                let session = match self
                    .session_mgr
                    .get_session(zone_id, tenant_id, &c.uid, &access_key)
                    .await
                {
                    Ok(Some(session)) => session,
                    _ => {
                        return Ok(Response::new(CheckResponse::with_status(
                            Status::permission_denied("Session proof key unavailable"),
                        )))
                    }
                };
                let raw_body = req
                    .attributes
                    .as_ref()
                    .and_then(|a| a.request.as_ref())
                    .and_then(|r| r.http.as_ref())
                    .map(|h| {
                        if !h.body.is_empty() {
                            h.body.as_bytes().to_vec()
                        } else {
                            h.raw_body.clone()
                        }
                    })
                    .unwrap_or_default();
                match crate::user::session_proof::verify_critical_proof(
                    &self.session_mgr,
                    &access_key,
                    &session.client_proof_public_key,
                    client_headers,
                    method,
                    path_without_query,
                    &raw_body,
                )
                .await
                {
                    Ok(challenge_id) => session_proof_challenge_id = Some(challenge_id),
                    Err(error) => {
                        Logger::authz_log(&c.uid, method, authz_log_path, "DENIED", &error);
                        return Ok(Response::new(CheckResponse::with_status(
                            Status::permission_denied("Invalid session proof"),
                        )));
                    }
                }
            }
        }

        // Social-link start is an ACR-local critical mutation because only ACR
        // owns provider redirects and OAuth state. It runs after proof consume,
        // session, CSRF, Zone and tenant verification.
        if session_proof_challenge_id.is_some() {
            if let Some(ref user_claims) = claims {
                if let Some(response) = self
                    .oauth
                    .handle_social_link_start(
                        &self.session_mgr,
                        user_claims,
                        &access_key,
                        &req,
                        method,
                        path,
                        &cookies_to_set,
                    )
                    .await
                {
                    return response;
                }
            }
        }

        if let Some(ref alias) = billing_alias {
            if path_without_query.starts_with("/api/v1/billing/critical/") {
                let raw_body = req
                    .attributes
                    .as_ref()
                    .and_then(|attributes| attributes.request.as_ref())
                    .and_then(|request| request.http.as_ref())
                    .map(|http| {
                        if http.body.is_empty() {
                            http.raw_body.clone()
                        } else {
                            http.body.as_bytes().to_vec()
                        }
                    })
                    .unwrap_or_default();
                match crate::user::session_proof::verify_critical_proof(
                    &self.session_mgr,
                    &access_key,
                    &alias.client_proof_public_key,
                    client_headers,
                    method,
                    path_without_query,
                    &raw_body,
                )
                .await
                {
                    Ok(challenge_id) => session_proof_challenge_id = Some(challenge_id),
                    Err(error) => {
                        Logger::authz_log(&alias.user_id, method, authz_log_path, "DENIED", &error);
                        return Ok(Response::new(CheckResponse::with_status(
                            Status::permission_denied("Invalid billing session proof"),
                        )));
                    }
                }
            }
        }

        // SRE Critical API Signature Verification
        if is_admin && path.starts_with("/admin/critical/") {
            let device_pubkey =
                if let Ok(Some(sess)) = self.session_mgr.get_sre_session(&access_key).await {
                    sess.device_public_key
                } else {
                    String::new()
                };

            let raw_body = req
                .attributes
                .as_ref()
                .and_then(|a| a.request.as_ref())
                .and_then(|r| r.http.as_ref())
                .map(|h| {
                    if !h.body.is_empty() {
                        h.body.as_bytes().to_vec()
                    } else {
                        h.raw_body.clone()
                    }
                })
                .unwrap_or_default();

            let step_up_code = client_headers
                .get("x-admin-stepup-code")
                .or_else(|| client_headers.get("X-Admin-StepUp-Code"))
                .map(String::as_str)
                .unwrap_or_default();
            let proof_id = match crate::sre::signature::verify_sre_signature(
                &self.session_mgr,
                &device_pubkey,
                client_headers,
                method,
                path,
                &raw_body,
            )
            .await
            {
                Ok(proof_id) => proof_id,
                Err(err_msg) => {
                    Logger::authz_log("sre", method, authz_log_path, "DENIED", &err_msg);
                    return Ok(Response::new(CheckResponse::with_status(
                        Status::permission_denied("Invalid SRE critical proof"),
                    )));
                }
            };

            // [COMMENT]: Only a device-signed, one-time request may reach
            // Vault TOTP verification. A failed OTP burns the nonce and forces
            // a freshly signed request, preventing replay across code windows.
            if step_up_code.len() != 6
                || !step_up_code.bytes().all(|value| value.is_ascii_digit())
                || !matches!(
                    self.sre_token_mgr.verify_admin_totp(step_up_code).await,
                    Ok(true)
                )
            {
                return Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Invalid SRE critical proof"),
                )));
            }
            session_proof_challenge_id = Some(proof_id);
        }

        let zone_control_signed_headers =
            if path_without_query.starts_with("/zone-control/v1/storage/") {
                let Some(ref storage_claims) = claims else {
                    return Ok(Response::new(CheckResponse::with_status(
                        Status::permission_denied(
                            "Zone Control Edge Gateway requires a user Trinity session",
                        ),
                    )));
                };
                let raw_body = req
                    .attributes
                    .as_ref()
                    .and_then(|attributes| attributes.request.as_ref())
                    .and_then(|request| request.http.as_ref())
                    .map(|http| {
                        if http.body.is_empty() {
                            http.raw_body.clone()
                        } else {
                            http.body.as_bytes().to_vec()
                        }
                    })
                    .unwrap_or_default();
                match crate::storage::control_assertion::authorize_storage_and_sign(
                    &self.session_mgr,
                    &self.token_mgr,
                    &self.config,
                    storage_claims,
                    client_headers,
                    method,
                    path,
                    &raw_body,
                )
                .await
                {
                    Ok(headers) => Some(headers),
                    Err(reason) => {
                        Logger::authz_log(
                            &storage_claims.uid,
                            method,
                            authz_log_path,
                            "DENIED",
                            reason,
                        );
                        return Ok(Response::new(CheckResponse::with_status(
                            Status::permission_denied(reason),
                        )));
                    }
                }
            } else if path_without_query.starts_with("/zone-control/v1/") {
                // The Central route is intentionally generic, but ACR must
                // fail closed until a capability has an explicit signer and
                // policy. Otherwise a new path could inherit client headers.
                return Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone control capability is not allowed"),
                )));
            } else {
                None
            };

        cookies_to_set.extend(cookies_to_set_zone);

        // [COMMENT]: Khởi tạo biến lưu trữ đường dẫn đã ghi đè (nếu có)
        let mut rewritten_path_opt = None;

        if !is_sre {
            let verified_tenant_id = billing_alias
                .as_ref()
                .map(|alias| alias.tenant_id.as_str())
                .or_else(|| claims.as_ref().and_then(|value| value.tenant_id.as_deref()));
            if let Some(rewritten) = rewrite_owner_billing_path(path, verified_tenant_id) {
                Logger::sys_debug(
                    "ext_authz.billing_owner_rewrite",
                    &format!("Rewriting owner Billing path: {} -> {}", path, rewritten),
                );
                rewritten_path_opt = Some(rewritten);
            } else if !is_billing {
                if let Some(ref c) = claims {
                    // [COMMENT]: Chỉ áp dụng rewrite đối với các endpoint nghiệp vụ chung /api/v1/...
                    // Loại trừ các endpoint thuộc cụm hệ thống, định danh hoặc billing
                    if path.starts_with("/api/v1/")
                        && !path.starts_with("/api/v1/me/")
                        && !path.starts_with("/api/v1/auth/")
                        && !path.starts_with("/api/v1/tenant/")
                        && !path.starts_with("/api/v1/personal/")
                        && !path.starts_with("/api/v1/billing/")
                    {
                        // [COMMENT]: Trích xuất tenant_id từ phía Client (cookie hoặc header)
                        let client_tenant_id =
                            extract_cookie_value(&cookie_header, COOKIE_TENANT_ID)
                                .or_else(|| client_headers.get("x-tenant-id").cloned())
                                .or_else(|| client_headers.get("X-Tenant-ID").cloned());

                        let client_has_tenant = client_tenant_id
                            .as_ref()
                            .map_or(false, |t| !t.is_empty() && t != "platform");
                        let session_has_tenant = c
                            .tenant_id
                            .as_ref()
                            .map_or(false, |t| !t.is_empty() && t != "platform");

                        // [COMMENT]: Cơ chế bảo mật CHẶN CHÉO - Ngăn cản sự sai lệch giữa ngữ cảnh Client yêu cầu và Session thực tế
                        if client_has_tenant != session_has_tenant {
                            Logger::authz_log(
                                &c.sub,
                                method,
                                authz_log_path,
                                "DENIED",
                                &format!(
                                    "Routing context mismatch: client_has_tenant={}, session_has_tenant={}. Platform fallback blocked.",
                                    client_has_tenant, session_has_tenant
                                ),
                            );
                            return Ok(Response::new(CheckResponse::with_status(
                                Status::permission_denied("Tenant context mismatch"),
                            )));
                        }

                        // [COMMENT]: So khớp chính xác Tenant ID của client và session để tránh giả mạo
                        if client_has_tenant {
                            let c_tenant = client_tenant_id.as_deref().unwrap();
                            let s_tenant = c.tenant_id.as_deref().unwrap();
                            if c_tenant != s_tenant {
                                Logger::authz_log(
                                    &c.sub,
                                    method,
                                    authz_log_path,
                                    "DENIED",
                                    &format!(
                                        "Tenant ID mismatch for routing: client='{}', session='{}'",
                                        c_tenant, s_tenant
                                    ),
                                );
                                return Ok(Response::new(CheckResponse::with_status(
                                    Status::permission_denied("Tenant mismatch"),
                                )));
                            }
                        }

                        // [COMMENT]: Xác định tiền tố route group tương ứng: /api/v1/tenant hoặc /api/v1/personal
                        let prefix = if client_has_tenant {
                            "/api/v1/tenant"
                        } else {
                            "/api/v1/personal"
                        };
                        let new_path = format!("{}{}", prefix, &path[7..]);
                        Logger::sys_debug(
                            "ext_authz.path_rewrite",
                            &format!("Rewriting path: {} -> {}", path, new_path),
                        );
                        rewritten_path_opt = Some(new_path);
                    }
                }
            }
        }

        // Build OkHttpResponse for Envoy
        let sub = if is_billing {
            billing_alias
                .as_ref()
                .map(|alias| alias.username.as_str())
                .unwrap_or("anonymous")
        } else {
            claims
                .as_ref()
                .map(|c| c.sub.as_str())
                .unwrap_or("anonymous")
        };
        Logger::authz_log(sub, method, authz_log_path, "ALLOWED", "Authorized");

        let mut ok_response = CheckResponse::with_status(Status::ok("authorized"));
        ok_response.set_http_response(
            envoy_types::pb::envoy::service::auth::v3::OkHttpResponse::default(),
        );

        if let Some(ref mut http_resp) = ok_response.http_response {
            use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;
            if let HttpResponse::OkResponse(ref mut ok) = http_resp {
                use envoy_types::pb::envoy::config::core::v3::HeaderValueOption;

                // [COMMENT]: Cryptographic material is consumed at ACR and
                // must never cross into a business backend or its access logs.
                // Verified opaque markers are re-added below after removal.
                ok.headers_to_remove.extend([
                    "x-admin-signature".to_string(),
                    "x-admin-timestamp".to_string(),
                    "x-admin-nonce".to_string(),
                    "x-admin-stepup-code".to_string(),
                    "x-session-proof-signature".to_string(),
                    "x-session-proof-timestamp".to_string(),
                    "x-session-proof-challenge-id".to_string(),
                    "x-session-proof-verified".to_string(),
                ]);

                if let Some(signed) = zone_control_signed_headers {
                    for (key, value) in [
                        ("x-aurora-access-session-id", signed.access_session_id),
                        ("x-aurora-control-assertion", signed.assertion),
                        ("x-aurora-control-signature", signed.signature),
                        ("x-aurora-control-key-id", signed.key_id),
                    ] {
                        let mut header = HeaderValueOption::default();
                        header.header =
                            Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                                key: key.to_string(),
                                value,
                            });
                        // OVERWRITE_IF_EXISTS prevents a client-injected copy
                        // from surviving the Central security boundary.
                        header.append_action = 2;
                        ok.headers.push(header);
                    }
                }

                // [COMMENT]: Luôn overwrite marker để client không thể tự giả mạo header đã được ACR xác minh.
                let mut proof_header = HeaderValueOption::default();
                proof_header.header = Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                    key: "x-session-proof-verified".to_string(),
                    value: if session_proof_challenge_id.is_some() {
                        "true".to_string()
                    } else {
                        "false".to_string()
                    },
                });
                proof_header.append_action = 2;
                ok.headers.push(proof_header);

                if let Some(ref challenge_id) = session_proof_challenge_id {
                    let mut challenge_header = HeaderValueOption::default();
                    challenge_header.header =
                        Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                            key: "x-session-proof-challenge-id".to_string(),
                            value: challenge_id.clone(),
                        });
                    challenge_header.append_action = 2;
                    ok.headers.push(challenge_header);
                }

                if is_billing {
                    if let Some(alias) = billing_alias {
                        let headers_to_set = vec![
                            (HEADER_X_USER_ID, alias.user_id),
                            (HEADER_X_USER_NAME, alias.username),
                            (HEADER_X_ZONE_ID, alias.zone_id),
                            (HEADER_X_TENANT_ID, alias.tenant_id),
                        ];

                        for (key, val) in headers_to_set {
                            let mut h = HeaderValueOption::default();
                            h.header =
                                Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                                    key: key.to_string(),
                                    value: val,
                                });
                            // [COMMENT]: Identity ACR overwrite header client cũ; duplicate value không được phép tồn tại.
                            h.append_action = 2;
                            ok.headers.push(h);
                        }
                    }
                } else if is_sre {
                    if let Some(c) = sre_claims {
                        let device_id =
                            extract_cookie_value(&cookie_header, COOKIE_CLIENT_DEVICE_ID)
                                .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());

                        let headers_to_set = vec![
                            (HEADER_X_USER_ID, "sre".to_string()),
                            (HEADER_X_USER_NAME, "sre".to_string()),
                            (HEADER_X_USER_LEVEL, "0".to_string()),
                            (HEADER_X_CLIENT_DEVICE_ID, device_id),
                            (
                                HEADER_X_ZONE_ID,
                                c.zone_id.unwrap_or_else(|| "global".to_string()),
                            ),
                        ];

                        for (key, val) in headers_to_set {
                            let mut h = HeaderValueOption::default();
                            h.header =
                                Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                                    key: key.to_string(),
                                    value: val,
                                });
                            h.append_action = 2;
                            ok.headers.push(h);
                        }
                    }
                } else {
                    if let Some(c) = claims {
                        let device_id =
                            extract_cookie_value(&cookie_header, COOKIE_CLIENT_DEVICE_ID)
                                .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());
                        let workspace_id =
                            extract_cookie_value(&cookie_header, COOKIE_WORKSPACE_ID);

                        let headers_to_set = vec![
                            (HEADER_X_USER_ID, c.uid.clone()),
                            (HEADER_X_USER_NAME, c.sub.clone()),
                            (HEADER_X_USER_LEVEL, c.lvl.to_string()),
                            (
                                HEADER_X_TENANT_ID,
                                c.tenant_id.unwrap_or_else(|| "platform".to_string()),
                            ),
                            (HEADER_X_CLIENT_DEVICE_ID, device_id),
                            (
                                HEADER_X_ZONE_ID,
                                c.zone_id.unwrap_or_else(|| "global".to_string()),
                            ),
                        ];

                        for (key, val) in headers_to_set {
                            let mut h = HeaderValueOption::default();
                            h.header =
                                Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                                    key: key.to_string(),
                                    value: val,
                                });
                            ok.headers.push(h);
                        }

                        if let Some(ws_id) = workspace_id {
                            let mut h = HeaderValueOption::default();
                            h.header =
                                Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                                    key: HEADER_X_WORKSPACE_ID.to_string(),
                                    value: ws_id,
                                });
                            ok.headers.push(h);
                        }
                    }
                }

                // [COMMENT]: Rewrite headers are emitted after both identity
                // branches so Trinity and Billing Alias follow the same
                // neutral public contract. OVERWRITE removes client copies.
                if let Some(ref rewritten_path) = rewritten_path_opt {
                    let mut original = HeaderValueOption::default();
                    original.header = Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                        key: "x-original-path".to_string(),
                        value: path.to_string(),
                    });
                    original.append_action = 2;
                    ok.headers.push(original);

                    let mut rewritten = HeaderValueOption::default();
                    rewritten.header =
                        Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                            key: ":path".to_string(),
                            value: rewritten_path.clone(),
                        });
                    rewritten.append_action = 2;
                    ok.headers.push(rewritten);
                }

                for cookie_str in cookies_to_set {
                    let mut h = HeaderValueOption::default();
                    h.header = Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                        key: "set-cookie".to_string(),
                        value: cookie_str,
                    });
                    ok.response_headers_to_add.push(h);
                }
            }
        }

        Ok(Response::new(ok_response))
    }
}

#[cfg(test)]
mod tests {
    use super::{
        authority_matches_origin, is_billing_alias_path, is_internal_owner_billing_path,
        rewrite_owner_billing_path,
    };

    #[test]
    fn billing_surface_selects_session_by_console_authority() {
        assert!(!is_billing_alias_path(
            "GET",
            "/api/v1/billing/wallet/summary",
            false
        ));
        assert!(is_billing_alias_path(
            "GET",
            "/api/v1/billing/wallet/onboarding",
            true
        ));
        assert!(is_billing_alias_path("GET", "/api/v1/billing/tiers", true));
        assert!(is_billing_alias_path(
            "POST",
            "/api/v1/billing/critical/tiers/STORAGE/CODE",
            true
        ));
    }

    #[test]
    fn owner_billing_rewrite_uses_only_verified_tenant_context() {
        assert_eq!(
            rewrite_owner_billing_path("/api/v1/billing/wallet/summary?fresh=1", None),
            Some("/api/v1/personal/billing/wallet/summary?fresh=1".to_string())
        );
        assert_eq!(
            rewrite_owner_billing_path(
                "/api/v1/billing/wallet/top-ups",
                Some("019f3d3e-997d-7894-9236-c5122634cb4f")
            ),
            Some("/api/v1/tenant/billing/wallet/top-ups".to_string())
        );
        assert_eq!(
            rewrite_owner_billing_path("/api/v1/billing/tiers", Some("tenant")),
            None
        );
    }

    #[test]
    fn internal_owner_billing_routes_are_not_public_inputs() {
        assert!(is_internal_owner_billing_path(
            "/api/v1/personal/billing/wallet/summary"
        ));
        assert!(is_internal_owner_billing_path(
            "/api/v1/tenant/billing/wallet/top-ups"
        ));
        assert!(!is_internal_owner_billing_path(
            "/api/v1/billing/wallet/summary"
        ));
    }

    #[test]
    fn iam_render_context_uses_alias_only_on_cost_console_authority() {
        assert!(is_billing_alias_path(
            "GET",
            "/api/v1/me/iam/context/read",
            true
        ));
        assert!(!is_billing_alias_path(
            "GET",
            "/api/v1/me/iam/context/read",
            false
        ));
        assert!(!is_billing_alias_path(
            "POST",
            "/api/v1/me/iam/context/read",
            true
        ));
        assert!(!is_billing_alias_path(
            "GET",
            "/api/v1/me/iam/profile/read",
            true
        ));
    }

    #[test]
    fn authority_comparison_is_scheme_and_port_aware() {
        assert!(authority_matches_origin(
            "cost-manager.aurora.local:443",
            "https://cost-manager.aurora.local"
        ));
        assert!(!authority_matches_origin(
            "cloud.aurora.local:443",
            "https://cost-manager.aurora.local"
        ));
        assert!(!authority_matches_origin(
            "cost-manager.aurora.local:8443",
            "https://cost-manager.aurora.local"
        ));
    }
}
