// ======================================================================================================
// 📂 user/tenant.rs — Xác thực Tenant context tại Edge (Tenant Resolution + Tenant Switch)
//
// Đây là merge của service/tenant/tenant_resolution.rs và service/tenant/tenant_switch.rs.
// ======================================================================================================

use crate::billing::claims::TokenManager;
use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::user::claims::Claims;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use serde::Serialize;
use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};

// ─── Tenant Resolution ────────────────────────────────────────────────────────

/// [COMMENT]: Xác thực Tenant đối chiếu giữa cookie/header gửi lên và Claims trong JWT
pub async fn resolve_and_verify_tenant(
    claims: Option<&mut Claims>,
    cookie_header: &str,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Result<(), Result<Response<CheckResponse>, Status>> {
    use crate::gateway::ext_authz::extract_cookie_value;

    // [COMMENT]: 1. Lấy cookie/header tenant_id từ Client
    let cookie_tenant_id = extract_cookie_value(cookie_header, COOKIE_TENANT_ID)
        .or_else(|| client_headers.get("x-tenant-id").cloned())
        .or_else(|| client_headers.get("X-Tenant-ID").cloned());

    // [COMMENT]: 2. So khớp với JWT claims
    if let Some(ref c) = claims {
        // [COMMENT]: "platform" làm fallback thay vì "global" để phân biệt rõ ràng với Zone
        let claims_tenant_id = c.tenant_id.as_deref().unwrap_or("platform");
        let req_tenant_id = cookie_tenant_id.as_deref().unwrap_or("platform");

        if req_tenant_id != claims_tenant_id {
            Logger::authz_log(
                &c.sub,
                method,
                path,
                "DENIED",
                &format!(
                    "Tenant mismatch: client cookie='{}', jwt claims='{}'",
                    req_tenant_id, claims_tenant_id
                ),
            );
            return Err(Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Tenant unavailable"),
            ))));
        }

        // [COMMENT]: 3. Nếu có tenant_id không phải "platform", validate UUID format
        if !req_tenant_id.is_empty() && req_tenant_id != "platform" {
            if uuid::Uuid::parse_str(req_tenant_id).is_err() {
                Logger::authz_log(
                    &c.sub,
                    method,
                    path,
                    "DENIED",
                    &format!(
                        "Invalid UUID format for requested tenant: {}",
                        req_tenant_id
                    ),
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Tenant unavailable"),
                ))));
            }
        }
    }

    Ok(())
}

// ─── Tenant Switch Handler ────────────────────────────────────────────────────

#[derive(Serialize)]
pub struct TenantSwitchSuccessResponse {
    pub tenant_domain: String,
    pub tenant_id: String,
}

#[derive(Serialize)]
struct ErrorResponse {
    error_message: String,
}

fn build_denied_json(status: HttpStatusCode, message: &str) -> CheckResponse {
    let body = serde_json::to_string(&ErrorResponse {
        error_message: message.to_string(),
    })
    .unwrap_or_default();
    // [COMMENT]: XSSI prefix cộng dồn để tránh lỗi escape brace
    let json_body = ")]}',\n".to_string() + &body;

    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(status);
    builder.add_header("content-type", "application/json", None, false);
    builder.set_body(json_body);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(message));
    response.set_http_response(builder);
    response
}

fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}

fn parse_tenant_domain(path: &str) -> Option<String> {
    path.find('?').and_then(|pos| {
        let query_str = &path[pos + 1..];
        query_str
            .split('&')
            .find(|pair| pair.starts_with("tenant_domain="))
            .map(|pair| pair["tenant_domain=".len()..].to_string())
    })
}

fn parse_tenant_id(path: &str) -> Option<String> {
    path.find('?').and_then(|pos| {
        let query_str = &path[pos + 1..];
        query_str
            .split('&')
            .find(|pair| pair.starts_with("tenant_id="))
            .map(|pair| pair["tenant_id=".len()..].to_string())
    })
}

/// [COMMENT]: Intercept POST /api/v1/tenant/go-to-tenant — xác thực Trinity và re-issue JWT với tenant_id mới.
pub async fn handle_tenant_switch(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    config: &Config,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    use crate::gateway::ext_authz::extract_cookie_value;

    // [COMMENT]: Chỉ intercept POST /api/v1/tenant/go-to-tenant
    if !(method == "POST" && path.starts_with("/api/v1/tenant/go-to-tenant")) {
        return None;
    }

    Logger::sys_info(
        "user.tenant.switch",
        &format!("Intercepted tenant switch request: {}", path),
    );

    let tenant_domain = match parse_tenant_domain(path) {
        Some(d) if !d.trim().is_empty() => d.trim().to_lowercase(),
        _ => {
            Logger::sys_warn(
                "user.tenant.switch",
                "Missing tenant_domain in query params",
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Tenant domain is required",
            ))));
        }
    };

    let tenant_id = match parse_tenant_id(path) {
        Some(id) if !id.trim().is_empty() => id.trim().to_string(),
        _ => {
            Logger::sys_warn(
                "user.tenant.switch",
                "Missing tenant_id in query params",
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Tenant ID is required",
            ))));
        }
    };

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

    // ─── Xác thực Trinity Credentials ──────────────────────────────────────
    let auth_result = async {
        let jwt_token = extract_cookie_value(&cookie_header, COOKIE_ACCESS_TOKEN)
            .ok_or("Missing access_token cookie")?;
        let access_key = extract_cookie_value(&cookie_header, COOKIE_ACCESS_KEY)
            .ok_or("Missing access_key cookie")?;

        let claims = token_mgr.verify_token(&jwt_token).await.map_err(|e| {
            Logger::sys_debug("user.tenant.switch", &format!("JWT verify failed: {}", e));
            "Invalid access_token"
        })?;

        if claims.access_key != access_key {
            return Err("Access Key Mismatch");
        }

        let session = match session_mgr
            .get_session(
                claims.zone_id.as_deref().unwrap_or("global"),
                // [COMMENT]: "platform" làm fallback cho tenant_id
                claims.tenant_id.as_deref().unwrap_or("platform"),
                &claims.uid,
                &access_key,
            )
            .await
        {
            Ok(Some(s)) => s,
            Ok(None) => return Err("Session Expired or Revoked"),
            Err(e) => {
                Logger::sys_error("user.tenant.switch", "Redis session error", &e.to_string());
                return Err("Authentication service unavailable");
            }
        };

        let access_secret = extract_cookie_value(&cookie_header, COOKIE_ACCESS_SECRET)
            .ok_or("Missing access_secret cookie")?;
        let incoming_hash = sha256_hash(&access_secret);

        if session.ash != incoming_hash {
            return Err("Access Secret Mismatch");
        }

        Ok((claims, access_key))
    }
    .await;

    let (mut claims, _access_key) = match auth_result {
        Ok(val) => val,
        Err(msg) => {
            Logger::sys_warn("user.tenant.switch", &format!("Auth failed: {}", msg), "");
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Unauthorized",
            ))));
        }
    };

    // ─── Re-issue JWT với tenant_id mới ────────────────────────────────────
    claims.tenant_id = Some(tenant_id.clone());

    let new_jwt = match token_mgr.generate_token(&claims).await {
        Ok(jwt) => jwt,
        Err(e) => {
            Logger::sys_error(
                "user.tenant.switch",
                "Failed to re-issue JWT",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Tenant unavailable",
            ))));
        }
    };

    // ─── Set cookies và trả response ───────────────────────────────────────
    let domain_str = if config.app_public_domain.trim().is_empty() {
        String::new()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };

    let tenant_body = serde_json::to_string(&TenantSwitchSuccessResponse {
        tenant_domain: tenant_domain.clone(),
        tenant_id: tenant_id.clone(),
    })
    .unwrap_or_default();
    let success_body = ")]}',\n".to_string() + &tenant_body;

    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(HttpStatusCode::Ok);
    builder.add_header("content-type", "application/json", None, false);
    builder.set_body(success_body);

    let access_cookie = format!(
        "{}={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        COOKIE_ACCESS_TOKEN, new_jwt, config.session_ttl_secs, domain_str
    );
    builder.add_header("set-cookie", &access_cookie, None, false);

    let tenant_cookie = format!(
        "{}={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        COOKIE_TENANT_DOMAIN, tenant_domain, domain_str
    );
    builder.add_header("set-cookie", &tenant_cookie, None, false);

    let tenant_id_cookie = format!(
        "{}={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        COOKIE_TENANT_ID, tenant_id, domain_str
    );
    builder.add_header("set-cookie", &tenant_id_cookie, None, false);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(
        "Tenant switch completed successfully",
    ));
    response.set_http_response(builder);

    Logger::authz_log(
        &claims.uid,
        method,
        path,
        "ALLOWED",
        &format!("Tenant switch to '{}' (ID: {})", tenant_domain, tenant_id),
    );

    Some(Ok(Response::new(response)))
}
