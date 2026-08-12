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
use crate::user::context_switch::{handle_tenant_switch, resolve_and_verify_tenant};
use crate::user::revoke::handle_logout;
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
            || (method == "GET" && path == "/api/v1/iam/context/read"))
}

fn is_internal_render_context_path(path: &str) -> bool {
    matches!(
        path,
        "/api/v1/personal/iam/context/read" | "/api/v1/tenant/iam/context/read"
    )
}

fn is_internal_owner_path(path: &str) -> bool {
    path == "/api/v1/personal"
        || path.starts_with("/api/v1/personal/")
        || path == "/api/v1/tenant"
        || path.starts_with("/api/v1/tenant/")
}

fn is_acr_local_owner_control_path(method: &str, path: &str) -> bool {
    // This endpoint changes the verified session context inside ACR; it is not
    // an owner-selected Controlplane route. Every other owner prefix remains
    // an internal rewrite target.
    method == "POST"
        && matches!(
            path,
            "/api/v1/context/go-to-tenant" | "/api/v1/context/go-to-personal"
        )
}

fn rewrite_render_context_path(path: &str, tenant_id: Option<&str>) -> Option<String> {
    let (path_without_query, query) = path
        .split_once('?')
        .map_or((path, None), |(base, query)| (base, Some(query)));
    if path_without_query != "/api/v1/iam/context/read" {
        return None;
    }
    let owner = if tenant_id.is_some_and(|value| !value.is_empty() && value != "platform") {
        "tenant"
    } else {
        "personal"
    };
    let mut rewritten = format!("/api/v1/{owner}/iam/context/read");
    if let Some(query) = query {
        rewritten.push('?');
        rewritten.push_str(query);
    }
    Some(rewritten)
}

fn is_personal_only_neutral_path(method: &str, path: &str) -> bool {
    (path == "/api/v1/tenants" && (method == "GET" || method == "POST"))
}

fn rewrite_neutral_owner_path(path: &str, tenant_id: Option<&str>) -> Option<String> {
    let path_without_query = path.split('?').next().unwrap_or(path);
    if !path_without_query.starts_with("/api/v1/")
        || path_without_query.starts_with("/api/v1/me/")
        || path_without_query.starts_with("/api/v1/auth/")
        || path_without_query.starts_with("/api/v1/tenant/")
        || path_without_query.starts_with("/api/v1/personal/")
        || path_without_query.starts_with("/api/v1/billing/")
    {
        return None;
    }

    // Only the tenant binding already verified from the session may select
    // the internal owner route. No client path/header participates here.
    let owner = if tenant_id.is_some_and(|value| !value.is_empty() && value != "platform") {
        "tenant"
    } else {
        "personal"
    };
    Some(format!("/api/v1/{owner}{}", &path["/api/v1".len()..]))
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
            crate::user::login::LoginWorkflowContext {
                session_mgr: &self.session_mgr,
                token_mgr: &self.token_mgr,
                redis_client: redis_client.as_ref(),
                shared_redis: &self.shared_redis,
                config: &self.config,
            },
            crate::user::login::LoginWorkflowRequest {
                client_headers,
                request: &req,
                method,
                path,
            },
        )
        .await
        {
            return res;
        }

        if let Some(res) = self
            .oauth
            .handle(
                crate::user::oauth::OAuthWorkflowContext {
                    session_mgr: &self.session_mgr,
                    token_mgr: &self.token_mgr,
                    shared_redis_client: redis_client.as_ref(),
                    shared_redis: &self.shared_redis,
                    config: &self.config,
                },
                crate::user::oauth::OAuthEdgeRequest {
                    client_headers,
                    request: &req,
                    method,
                    path,
                },
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
        if is_internal_render_context_path(path_without_query) {
            // Internal owner routes exist only as rewrite targets. Letting the
            // browser choose one would move owner selection before auth.
            Logger::authz_log(
                "system",
                method,
                authz_log_path,
                "DENIED",
                "Direct internal IAM render context route",
            );
            return Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Internal IAM route"),
            )));
        }
        if is_internal_owner_path(path_without_query)
            && !is_acr_local_owner_control_path(method, path_without_query)
        {
            // All owner-prefixed business routes are upstream implementation
            // details. Browser/SDK callers must use the neutral route so ACR
            // remains the sole owner-branch selector.
            Logger::authz_log(
                "system",
                method,
                authz_log_path,
                "DENIED",
                "Direct internal owner route",
            );
            return Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Internal owner route"),
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
            crate::user::login::LoginWorkflowContext {
                session_mgr: &self.session_mgr,
                token_mgr: &self.token_mgr,
                redis_client: redis_client.as_ref(),
                shared_redis: &self.shared_redis,
                config: &self.config,
            },
            crate::user::login::LoginWorkflowRequest {
                client_headers,
                request: &req,
                method,
                path,
            },
        )
        .await
        {
            return res;
        }

        // 1b. User Session Check: GET /api/v1/me/session
        if let Some(res) = crate::user::verify::handle_user_session_check(
            crate::user::verify::SessionVerificationContext {
                session_mgr: &self.session_mgr,
                token_mgr: &self.token_mgr,
                shared_redis_client: redis_client.as_ref(),
                shared_redis: &self.shared_redis,
                config: &self.config,
            },
            crate::user::verify::SessionCheckRequest {
                client_headers,
                method,
                path,
            },
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
            crate::sre::login::AdminLoginWorkflowContext {
                session_mgr: &self.session_mgr,
                token_mgr: &self.sre_token_mgr,
                config: &self.config,
            },
            crate::sre::login::AdminLoginRequest {
                request: &req,
                method,
                path,
            },
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
            crate::user::zone_switcher::UserZoneSwitchWorkflowContext {
                session_mgr: &self.session_mgr,
                token_mgr: &self.token_mgr,
                shared_redis: &self.shared_redis,
                redis_client: redis_client.as_ref(),
                config: &self.config,
            },
            crate::user::zone_switcher::UserZoneSwitchRequest {
                client_headers,
                method,
                path,
            },
        )
        .await
        {
            return res;
        }

        // 6b. SRE Zone Switch: POST /admin/zone/go-to-zone
        if let Some(res) = crate::sre::zone_switcher::handle_sre_zone_switch(
            crate::sre::zone_switcher::SreZoneSwitchWorkflowContext {
                session_mgr: &self.session_mgr,
                token_mgr: &self.sre_token_mgr,
                shared_redis: &self.shared_redis,
                redis_client: redis_client.as_ref(),
                config: &self.config,
            },
            crate::sre::zone_switcher::SreZoneSwitchRequest {
                client_headers,
                method,
                path,
            },
        )
        .await
        {
            return res;
        }

        // 7. Context Switch: POST /api/v1/context/go-to-tenant
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

        if let Some(res) = crate::user::context_switch::handle_personal_switch(
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
                    crate::user::verify::SessionVerificationContext {
                        session_mgr: &self.session_mgr,
                        token_mgr: &self.token_mgr,
                        shared_redis_client: redis_client.as_ref(),
                        shared_redis: &self.shared_redis,
                        config: &self.config,
                    },
                    crate::user::verify::EdgeSessionVerificationRequest {
                        cookie_header: &cookie_header,
                        client_headers,
                        method,
                        path,
                    },
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
                    crate::billing::exchange::BillingHandoffWorkflowContext {
                        session_mgr: &self.session_mgr,
                        config: &self.config,
                    },
                    crate::billing::exchange::BillingHandoffIssueRequest {
                        claims: source_claims,
                        access_key: &access_key,
                        source_session: &source_session,
                        request: &req,
                        method,
                        path: path_without_query,
                    },
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
                        crate::user::oauth::SocialLinkStartWorkflowContext {
                            session_mgr: &self.session_mgr,
                        },
                        crate::user::oauth::SocialLinkStartRequest {
                            claims: user_claims,
                            access_key: &access_key,
                            request: &req,
                            method,
                            path,
                            cookies_to_set: &cookies_to_set,
                        },
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

        let zone_control_signed_headers = if path_without_query
            == "/zone-control/v1/transfer-tickets"
            || path_without_query.starts_with("/zone-control/v1/transfer-tickets/")
        {
            let Some(ref transfer_claims) = claims else {
                return Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied(
                        "Zone transfer tickets require a user Trinity session",
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
            match crate::storage::control_assertion::attest_transfer_ticket_request(
                crate::storage::control_assertion::GenericTransferTicketRequestContext {
                    claims: transfer_claims,
                    token_mgr: &self.token_mgr,
                    config: &self.config,
                    method,
                    path,
                    body: &raw_body,
                },
            )
            .await
            {
                Ok(headers) => Some(headers),
                Err(reason) => {
                    Logger::authz_log(
                        &transfer_claims.uid,
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
        } else if path_without_query.starts_with("/zone-control/v1/storage/") {
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
                crate::storage::control_assertion::StorageControlWorkflowContext {
                    session_mgr: &self.session_mgr,
                    token_mgr: &self.token_mgr,
                    config: &self.config,
                },
                crate::storage::control_assertion::StorageControlRequest {
                    claims: storage_claims,
                    headers: client_headers,
                    method,
                    path,
                    body: &raw_body,
                },
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
            if let Some(rewritten) = rewrite_render_context_path(path, verified_tenant_id) {
                Logger::sys_debug(
                    "ext_authz.render_context_rewrite",
                    &format!(
                        "Rewriting IAM Render Context path: {} -> {}",
                        path, rewritten
                    ),
                );
                rewritten_path_opt = Some(rewritten);
            } else if let Some(rewritten) = rewrite_owner_billing_path(path, verified_tenant_id) {
                Logger::sys_debug(
                    "ext_authz.billing_owner_rewrite",
                    &format!("Rewriting owner Billing path: {} -> {}", path, rewritten),
                );
                rewritten_path_opt = Some(rewritten);
            } else if !is_billing {
                if let Some(ref c) = claims {
                    let session_has_tenant = verified_tenant_id
                        .is_some_and(|value| !value.is_empty() && value != "platform");
                    if is_personal_only_neutral_path(method, path_without_query)
                        && session_has_tenant
                    {
                        Logger::authz_log(
                            &c.sub,
                            method,
                            authz_log_path,
                            "DENIED",
                            "Tenant context cannot create another tenant",
                        );
                        return Ok(Response::new(CheckResponse::with_status(
                            Status::permission_denied("Tenant creation is personal-only"),
                        )));
                    }

                    if let Some(new_path) = rewrite_neutral_owner_path(path, verified_tenant_id) {
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
                    // Workspace selection comes only from the workspace_id
                    // cookie below. Remove a direct browser header first so
                    // an absent cookie cannot leave caller input upstream.
                    HEADER_X_WORKSPACE_ID.to_string(),
                ]);

                if let Some(signed) = zone_control_signed_headers {
                    for (key, value) in [
                        ("x-aurora-access-session-id", signed.access_session_id),
                        ("x-aurora-control-assertion", signed.assertion),
                        ("x-aurora-control-signature", signed.signature),
                        ("x-aurora-control-key-id", signed.key_id),
                    ] {
                        let header = HeaderValueOption {
                            header: Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                                key: key.to_string(),
                                value,
                            }),
                            // OVERWRITE_IF_EXISTS prevents a client-injected copy
                            // from surviving the Central security boundary.
                            append_action: 2,
                            ..Default::default()
                        };
                        ok.headers.push(header);
                    }
                }

                // [COMMENT]: Luôn overwrite marker để client không thể tự giả mạo header đã được ACR xác minh.
                let proof_header = HeaderValueOption {
                    header: Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                        key: "x-session-proof-verified".to_string(),
                        value: if session_proof_challenge_id.is_some() {
                            "true".to_string()
                        } else {
                            "false".to_string()
                        },
                    }),
                    append_action: 2,
                    ..Default::default()
                };
                ok.headers.push(proof_header);

                if let Some(ref challenge_id) = session_proof_challenge_id {
                    let challenge_header = HeaderValueOption {
                        header: Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                            key: "x-session-proof-challenge-id".to_string(),
                            value: challenge_id.clone(),
                        }),
                        append_action: 2,
                        ..Default::default()
                    };
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
                            let h = HeaderValueOption {
                                header: Some(
                                    envoy_types::pb::envoy::config::core::v3::HeaderValue {
                                        key: key.to_string(),
                                        value: val,
                                    },
                                ),
                                // [COMMENT]: Identity ACR overwrite header client cũ; duplicate value không được phép tồn tại.
                                append_action: 2,
                                ..Default::default()
                            };
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
                            let h = HeaderValueOption {
                                header: Some(
                                    envoy_types::pb::envoy::config::core::v3::HeaderValue {
                                        key: key.to_string(),
                                        value: val,
                                    },
                                ),
                                append_action: 2,
                                ..Default::default()
                            };
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
                            let h = HeaderValueOption {
                                header: Some(
                                    envoy_types::pb::envoy::config::core::v3::HeaderValue {
                                        key: key.to_string(),
                                        value: val,
                                    },
                                ),
                                // [SECURITY]: These are ACR-authenticated context
                                // headers. Replace, never append to, any value the
                                // client supplied before the ext_authz check.
                                append_action: 2,
                                ..Default::default()
                            };
                            ok.headers.push(h);
                        }

                        if let Some(ws_id) = workspace_id {
                            let h = HeaderValueOption {
                                header: Some(
                                    envoy_types::pb::envoy::config::core::v3::HeaderValue {
                                        key: HEADER_X_WORKSPACE_ID.to_string(),
                                        value: ws_id,
                                    },
                                ),
                                // [SECURITY]: Workspace is part of the compiled
                                // five-level authorization key, so it must have the
                                // same overwrite boundary as identity headers.
                                append_action: 2,
                                ..Default::default()
                            };
                            ok.headers.push(h);
                        }
                    }
                }

                // [COMMENT]: Rewrite headers are emitted after both identity
                // branches so Trinity and Billing Alias follow the same
                // neutral public contract. OVERWRITE removes client copies.
                if let Some(ref rewritten_path) = rewritten_path_opt {
                    let original = HeaderValueOption {
                        header: Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                            key: "x-original-path".to_string(),
                            value: path.to_string(),
                        }),
                        append_action: 2,
                        ..Default::default()
                    };
                    ok.headers.push(original);

                    let rewritten = HeaderValueOption {
                        header: Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                            key: ":path".to_string(),
                            value: rewritten_path.clone(),
                        }),
                        append_action: 2,
                        ..Default::default()
                    };
                    ok.headers.push(rewritten);
                }

                for cookie_str in cookies_to_set {
                    let h = HeaderValueOption {
                        header: Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                            key: "set-cookie".to_string(),
                            value: cookie_str,
                        }),
                        ..Default::default()
                    };
                    ok.response_headers_to_add.push(h);
                }
            }
        }

        Ok(Response::new(ok_response))
    }
}

#[cfg(test)]
#[path = "../../tests/unit/gateway/ext_authz.rs"]
mod tests;
