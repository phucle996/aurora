// ======================================================================================================
// 📂 user/tenant.rs — Xác thực Tenant context tại Edge (Tenant Resolution + Tenant Switch)
//
// Đây là merge của service/tenant/tenant_resolution.rs và service/tenant/tenant_switch.rs.
// ======================================================================================================

use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::infra::shared_redis::SharedRedisBus;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::token::TokenManager;
use crate::user::claims::Claims;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use serde::Serialize;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;
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
    let query = path.split_once('?')?.1;
    url::form_urlencoded::parse(query.as_bytes())
        .find(|(name, _)| name == "tenant_domain")
        .map(|(_, value)| value.into_owned())
}

fn parse_tenant_id(path: &str) -> Option<String> {
    let query = path.split_once('?')?.1;
    url::form_urlencoded::parse(query.as_bytes())
        .find(|(name, _)| name == "tenant_id")
        .map(|(_, value)| value.into_owned())
}

/// [COMMENT]: Intercept POST /api/v1/tenant/go-to-tenant — xác thực Trinity và re-issue JWT với tenant_id mới.
pub async fn handle_tenant_switch(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    shared_redis: &Arc<SharedRedisBus>,
    config: &Config,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    use crate::gateway::ext_authz::extract_cookie_value;

    // [COMMENT]: Match the path component exactly. A suffix must never enter
    // the security-sensitive tenant-switch workflow just because it shares a prefix.
    if method != "POST"
        || path.split_once('?').map(|(value, _)| value).unwrap_or(path)
            != "/api/v1/tenant/go-to-tenant"
    {
        return None;
    }

    Logger::sys_info(
        "user.tenant.switch",
        &format!("Intercepted tenant switch request: {}", path),
    );

    let tenant_domain = match parse_tenant_domain(path) {
        Some(value) => {
            let normalized = value.trim().to_lowercase();
            let valid_boundary = normalized
                .as_bytes()
                .first()
                .is_some_and(u8::is_ascii_alphanumeric)
                && normalized
                    .as_bytes()
                    .last()
                    .is_some_and(u8::is_ascii_alphanumeric);
            if normalized.len() < 3
                || normalized.len() > 255
                || normalized.contains("..")
                || normalized.contains(".-")
                || normalized.contains("-.")
                || !valid_boundary
                || !normalized.bytes().all(|byte| {
                    byte.is_ascii_lowercase()
                        || byte.is_ascii_digit()
                        || byte == b'.'
                        || byte == b'-'
                })
            {
                return Some(Ok(Response::new(build_denied_json(
                    HttpStatusCode::BadRequest,
                    "Tenant domain is required",
                ))));
            }
            normalized
        }
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

    let tenant_uuid = match parse_tenant_id(path)
        .as_deref()
        .map(str::trim)
        .and_then(|value| uuid::Uuid::parse_str(value).ok())
    {
        Some(id) if !id.is_nil() => id,
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

        Ok((claims, access_key, session))
    }
    .await;

    let (mut claims, access_key, source_session) = match auth_result {
        Ok(val) => val,
        Err(msg) => {
            Logger::sys_warn("user.tenant.switch", &format!("Auth failed: {}", msg), "");
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Unauthorized",
            ))));
        }
    };

    let user_uuid = match uuid::Uuid::parse_str(&claims.uid) {
        Ok(value) if !value.is_nil() => value,
        _ => {
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Unauthorized",
            ))));
        }
    };
    let mut access_request = Vec::with_capacity(32 + tenant_domain.len());
    access_request.extend_from_slice(user_uuid.as_bytes());
    access_request.extend_from_slice(tenant_uuid.as_bytes());
    access_request.extend_from_slice(tenant_domain.as_bytes());
    let access_response = match shared_redis
        .request(
            "iam.tenant.access.resolve",
            "iam.tenant.access.reply.",
            access_request,
            Duration::from_millis(900),
        )
        .await
    {
        Ok(response) => response,
        Err(error) => {
            Logger::sys_error(
                "user.tenant.switch",
                "Tenant membership verification unavailable",
                &error,
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::ServiceUnavailable,
                "Tenant unavailable",
            ))));
        }
    };
    if access_response.first() != Some(&1) {
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Forbidden,
            "Tenant unavailable",
        ))));
    }

    // [COMMENT]: Accept the former 21-byte reply during an ACR-first rolling
    // deployment, but ignore its retired metadata. Once every Controlplane
    // replica emits the canonical 5-byte shape both paths resolve only level.
    let level_bytes = match access_response.len() {
        5 => &access_response[1..5],
        21 => &access_response[17..21],
        _ => {
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::ServiceUnavailable,
                "Tenant unavailable",
            ))));
        }
    };
    let role_level = i32::from_be_bytes(
        level_bytes
            .try_into()
            .expect("tenant access level wire has fixed width"),
    );

    // [COMMENT]: ACR accepts tenant identity only from the Controlplane membership
    // decision. Client query values never become role claims on their own.
    let tenant_id = tenant_uuid.to_string();
    claims.tenant_id = Some(tenant_id.clone());
    claims.lvl = role_level;

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

    if let Err(error) = session_mgr
        .register_session(
            claims.zone_id.as_deref().unwrap_or("global"),
            &tenant_id,
            &claims.uid,
            &access_key,
            &source_session.ash,
            &source_session.tdid,
            &source_session.client_proof_public_key,
        )
        .await
    {
        Logger::sys_error(
            "user.tenant.switch",
            "Failed to bind tenant session scope",
            &error.to_string(),
        );
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::ServiceUnavailable,
            "Tenant unavailable",
        ))));
    }

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

#[cfg(test)]
mod tests {
    use super::{parse_tenant_domain, parse_tenant_id};

    #[test]
    fn tenant_switch_query_is_percent_decoded_by_name() {
        let path = "/api/v1/tenant/go-to-tenant?ignored=1&tenant_domain=acme.example&tenant_id=10000000-0000-4000-8000-000000000001";
        assert_eq!(parse_tenant_domain(path).as_deref(), Some("acme.example"));
        assert_eq!(
            parse_tenant_id(path).as_deref(),
            Some("10000000-0000-4000-8000-000000000001")
        );
    }

    #[test]
    fn tenant_switch_query_does_not_accept_prefix_collisions() {
        let path = "/api/v1/tenant/go-to-tenant?tenant_domain_suffix=evil&tenant_id_suffix=bad";
        assert_eq!(parse_tenant_domain(path), None);
        assert_eq!(parse_tenant_id(path), None);
    }
}
