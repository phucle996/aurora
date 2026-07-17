// ======================================================================================================
// 📂 gateway/ext_authz.rs — Central Envoy Ext-Authz Dispatcher (Edge Ingress)
// ======================================================================================================

use std::sync::Arc;
use tonic::{Request, Response, Status};

use crate::billing::claims::TokenManager;
use crate::config::Config;
use crate::gateway::csrf::verify_csrf_protection;
use crate::gateway::ratelimit::RateLimiter;
use crate::infra::nats::Nats;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::pkg::header::*;
use crate::user::claims::Claims;
use crate::user::revoke::handle_logout;
use crate::user::tenant::{handle_tenant_switch, resolve_and_verify_tenant};
use crate::user::verify::verify_edge_session;
use envoy_types::ext_authz::v3::{CheckRequestExt, CheckResponseExt};
use envoy_types::pb::envoy::service::auth::v3::{
    authorization_server::Authorization, CheckRequest, CheckResponse,
};

pub struct ExtAuthzService {
    session_mgr: Arc<SessionManager>,
    token_mgr: Arc<TokenManager>,
    config: Config,
    nats: Arc<Nats>,
    rate_limiter: Arc<RateLimiter>,
}

impl ExtAuthzService {
    pub fn new(
        session_mgr: Arc<SessionManager>,
        token_mgr: Arc<TokenManager>,
        config: Config,
        nats: Arc<Nats>,
    ) -> Self {
        let rate_limiter = Arc::new(RateLimiter::new(session_mgr.clone()));
        Self {
            session_mgr,
            token_mgr,
            config,
            nats,
            rate_limiter,
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

pub fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}

pub struct TrinityCookies<'a> {
    pub access_token: Option<&'a str>,
    pub access_key: Option<&'a str>,
    pub access_secret: Option<&'a str>,
    pub _refresh_token: Option<&'a str>,
    pub _tenant_id: Option<&'a str>,
    pub _workspace_id: Option<&'a str>,
}

pub fn parse_trinity_cookies(cookie_str: &str) -> TrinityCookies<'_> {
    let mut cookies = TrinityCookies {
        access_token: None,
        access_key: None,
        access_secret: None,
        _refresh_token: None,
        _tenant_id: None,
        _workspace_id: None,
    };
    for part in cookie_str.split(';') {
        let part = part.trim();
        if let Some(pos) = part.find('=') {
            let key = &part[..pos];
            let val = &part[pos + 1..];
            match key {
                COOKIE_ACCESS_TOKEN => cookies.access_token = Some(val),
                COOKIE_ACCESS_KEY => cookies.access_key = Some(val),
                COOKIE_ACCESS_SECRET => cookies.access_secret = Some(val),
                COOKIE_REFRESH_TOKEN => cookies._refresh_token = Some(val),
                COOKIE_TENANT_ID => cookies._tenant_id = Some(val),
                COOKIE_WORKSPACE_ID => cookies._workspace_id = Some(val),
                _ => {}
            }
        }
    }
    cookies
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

        let path = client_headers
            .get(":path")
            .map(|s| s.as_str())
            .unwrap_or("/");
        let method = client_headers
            .get(":method")
            .map(|s| s.as_str())
            .unwrap_or("GET");
        let client_ip = client_headers
            .get("x-forwarded-for")
            .map(|s| s.as_str())
            .unwrap_or("unknown");

        let redis_client = self.session_mgr.redis_client_arc();

        // 1. Bypass Endpoint Check (e.g. /api/v1/health)
        if self
            .config
            .bypass_endpoints
            .iter()
            .any(|ep| path.starts_with(ep))
        {
            let mut response = CheckResponse::with_status(Status::ok("bypassed"));
            response.set_http_response(
                envoy_types::pb::envoy::service::auth::v3::OkHttpResponse::default(),
            );
            return Ok(Response::new(response));
        }

        // 2. CORS Allowed Origin Check
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
                    path,
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

        // ─── Local Interceptors ───────────────────────────────────────────────

        // 1. User Login: POST /api/v1/auth/login
        if let Some(res) = crate::user::login::handle_login(
            &self.session_mgr,
            &self.token_mgr,
            &self.nats,
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

        // 2. Logout: POST /api/v1/auth/logout, POST /admin/auth/logout
        if let Some(res) = handle_logout(
            &self.session_mgr,
            &self.token_mgr,
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
            &self.token_mgr,
            &self.config,
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // 2c. SRE Logout: POST /admin/auth/logout
        if let Some(res) = crate::sre::logout::handle_sre_logout(
            &self.session_mgr,
            &self.token_mgr,
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
            &self.token_mgr,
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

        // 4. Billing Auditor Login: POST /api/v1/billing/auth/login
        if let Some(res) = crate::billing::login::handle_billing_login(
            &self.session_mgr,
            &self.token_mgr,
            &self.nats,
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
            &self.token_mgr,
            &self.nats,
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
            &self.nats,
            redis_client.as_ref(),
            client_headers,
            method,
            path,
        )
        .await
        {
            return res;
        }

        // Billing Zone Catalog
        if let Some(res) = crate::billing::zone_catalog::handle_billing_zone_catalog(
            &self.session_mgr,
            &self.token_mgr,
            &self.nats,
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
            &self.nats,
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
            &self.token_mgr,
            &self.nats,
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

        let is_billing = path.starts_with("/api/v1/billing");
        let is_sre = path.starts_with("/admin");

        let mut claims: Option<Claims> = None;
        let mut billing_claims: Option<crate::billing::claims::BillingClaims> = None;
        let mut access_key = String::new();
        let mut cookies_to_set = Vec::new();

        if is_billing {
            let verify_res = crate::billing::verify::verify_billing_edge_session(
                &self.session_mgr,
                &self.token_mgr,
                &cookie_header,
                client_headers,
                method,
                path,
            )
            .await;
            if let Some(denial) = verify_res.denial_response {
                return Ok(denial);
            }
            billing_claims = verify_res.claims;
            cookies_to_set.extend(verify_res.cookies_to_set);
        } else {
            if is_sre {
                let verify_res = crate::sre::verify::verify_sre_edge_session(
                    &self.session_mgr,
                    &self.token_mgr,
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
            } else {
                let verify_res = verify_edge_session(
                    &self.session_mgr,
                    &self.token_mgr,
                    &self.nats,
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
            .or_else(|| billing_claims.as_ref().map(|c| c.uid.as_str()))
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
        if (claims.is_some() || billing_claims.is_some())
            && !verify_csrf_protection(method, client_headers)
        {
            Logger::authz_log("system", method, path, "DENIED", "CSRF validation failed");
            return Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("CSRF validation failed"),
            )));
        }

        // Zone Resolution & Boundaries
        let is_admin = claims.as_ref().map(|c| c.is_admin()).unwrap_or(false);

        let cookies_to_set_zone = if is_billing {
            if let Err(res) = crate::billing::zone_resolution::resolve_and_verify_zone_billing(
                &self.nats,
                redis_client.as_ref(),
                billing_claims.as_ref().and_then(|c| c.zone_id.as_deref()),
                &cookie_header,
                client_headers,
                method,
                path,
            )
            .await
            {
                return res;
            }
            Vec::new()
        } else if is_admin {
            match crate::sre::zone_resolution::resolve_and_verify_zone_admin(
                &self.nats,
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
        } else {
            match crate::user::zone_resolution::resolve_and_verify_zone_user(
                &self.nats,
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

            if let Err(err_msg) = crate::sre::signature::verify_sre_signature(
                &self.session_mgr,
                &device_pubkey,
                client_headers,
                method,
                path,
                &raw_body,
            )
            .await
            {
                Logger::authz_log(
                    "sre",
                    method,
                    path,
                    "DENIED",
                    &format!("SRE Signature failed: {}", err_msg),
                );
                return Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied(&format!("SRE signature invalid: {}", err_msg)),
                )));
            }
        }

        cookies_to_set.extend(cookies_to_set_zone);

        // Build OkHttpResponse for Envoy
        let sub = if is_billing {
            billing_claims
                .as_ref()
                .map(|c| c.sub.as_str())
                .unwrap_or("anonymous")
        } else {
            claims
                .as_ref()
                .map(|c| c.sub.as_str())
                .unwrap_or("anonymous")
        };
        Logger::authz_log(sub, method, path, "ALLOWED", "Authorized");

        let mut ok_response = CheckResponse::with_status(Status::ok("authorized"));
        ok_response.set_http_response(
            envoy_types::pb::envoy::service::auth::v3::OkHttpResponse::default(),
        );

        if let Some(ref mut http_resp) = ok_response.http_response {
            use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;
            if let HttpResponse::OkResponse(ref mut ok) = http_resp {
                use envoy_types::pb::envoy::config::core::v3::HeaderValueOption;

                if is_billing {
                    if let Some(bc) = billing_claims {
                        let headers_to_set = vec![
                            (HEADER_X_USER_ID, bc.uid.clone()),
                            (HEADER_X_USER_ROLE_ID, bc.role_id.clone()),
                            (HEADER_X_USER_LEVEL, bc.lvl.to_string()),
                            (
                                HEADER_X_ZONE_ID,
                                bc.zone_id.clone().unwrap_or_else(|| "global".to_string()),
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
                    }
                } else {
                    if let Some(c) = claims {
                        let roles_str = c.get_roles().join(",");

                        let device_id =
                            extract_cookie_value(&cookie_header, COOKIE_CLIENT_DEVICE_ID)
                                .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());
                        let workspace_id =
                            extract_cookie_value(&cookie_header, COOKIE_WORKSPACE_ID);

                        let headers_to_set = vec![
                            (HEADER_X_USER_ID, c.uid.clone()),
                            (HEADER_X_USER_ROLE_ID, roles_str),
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

                for cookie_str in cookies_to_set {
                    let mut h = HeaderValueOption::default();
                    h.header = Some(envoy_types::pb::envoy::config::core::v3::HeaderValue {
                        key: "set-cookie".to_string(),
                        value: cookie_str,
                    });
                    ok.headers.push(h);
                }
            }
        }

        Ok(Response::new(ok_response))
    }
}
