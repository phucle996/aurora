// ======================================================================================================
// 📂 MODULE: acr/src/service/tenant/tenant_switch.rs
//            Xử Lý Tenant Context Switch tại Edge
//
// 🔄 LUỒNG:
//   POST /api/v1/tenant/go-to-tenant?tenant_domain=acme.platform.io
//   1. Xác thực Trinity Credentials (JWT + access_key + access_secret)
//   2. Phân giải tenant_domain → tenant_id (L1 → L2 → gRPC)
//   3. CheckMembership(tenant_id, user_id) - xác nhận user thuộc tenant đó
//   4. Re-issue JWT mới chứa tenant_id cập nhật
//   5. Set cookie tenant_domain (không HttpOnly để JS đọc được)
// ======================================================================================================

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use serde::Serialize;
use std::sync::Arc;
use tonic::{Response, Status};

use crate::config::Config;
use crate::core::session::SessionManager;
use crate::core::token::TokenManager;
use crate::observability::logger::Logger;
use crate::service::ext_authz::extract_cookie_value;
use crate::service::tenant::manager::TenantManager;

// [COMMENT]: Response JSON trả về client khi switch tenant thành công
#[derive(Serialize)]
pub struct TenantSwitchSuccessResponse {
    pub tenant_domain: String,
}

// [COMMENT]: Response JSON lỗi chung
#[derive(Serialize)]
pub struct ErrorResponse {
    pub error_message: String,
}

// ─── Helper builders ────────────────────────────────────────────────────────

fn build_denied_json(status: HttpStatusCode, message: &str) -> CheckResponse {
    let body = serde_json::to_string(&ErrorResponse {
        error_message: message.to_string(),
    })
    .unwrap_or_default();
    // [COMMENT]: XSSI prefix ")]}'" cộng dồn thay vì dùng format! để tránh lỗi escape brace
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

/// [COMMENT]: Hàm băm SHA-256 access_secret để so sánh với session.ash
fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}

/// [COMMENT]: Parse tenant_domain từ query param: /api/v1/tenant/go-to-tenant?tenant_domain=acme.io
fn parse_tenant_domain(path: &str) -> Option<String> {
    path.find('?').and_then(|pos| {
        let query_str = &path[pos + 1..];
        query_str
            .split('&')
            .find(|pair| pair.starts_with("tenant_domain="))
            .map(|pair| pair["tenant_domain=".len()..].to_string())
    })
}

// ─── Main Handler ────────────────────────────────────────────────────────────

/// [COMMENT]: Xử lý POST /api/v1/tenant/go-to-tenant
/// Intercepted ở Envoy ext_authz trước khi request đến backend
pub async fn handle_tenant_switch(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    tenant_mgr: &Arc<TenantManager>,
    config: &Config,
    client_headers: &std::collections::HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chỉ intercept POST /api/v1/tenant/go-to-tenant
    if !(method == "POST" && path.starts_with("/api/v1/tenant/go-to-tenant")) {
        return None;
    }

    Logger::sys_info(
        "tenant_switch",
        &format!("Intercepted tenant switch request: {}", path),
    );

    // [COMMENT]: Parse tenant_domain từ query param
    let tenant_domain = match parse_tenant_domain(path) {
        Some(d) if !d.trim().is_empty() => d.trim().to_lowercase(),
        _ => {
            Logger::sys_warn("tenant_switch", "Missing tenant_domain in query params", "");
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Tenant unavailable",
            ))));
        }
    };

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

    // ─── Bước 1: Xác thực Trinity Credentials ───────────────────────────────
    let auth_result = async {
        let jwt_token = extract_cookie_value(&cookie_header, "access_token")
            .ok_or("Missing access_token cookie")?;
        let access_key = extract_cookie_value(&cookie_header, "access_key")
            .ok_or("Missing access_key cookie")?;

        let claims = token_mgr.verify_token(&jwt_token).await.map_err(|e| {
            Logger::sys_debug("tenant_switch", &format!("JWT verify failed: {}", e));
            "Invalid access_token"
        })?;

        if claims.access_key != access_key {
            return Err("Access Key Mismatch");
        }

        // [COMMENT]: Lấy session hiện tại từ Redis để validate session còn sống
        let session = match session_mgr
            .get_session(
                claims.zone_id.as_deref().unwrap_or("global"),
                claims.tenant_id.as_deref().unwrap_or("global"),
                &claims.uid,
                &access_key,
            )
            .await
        {
            Ok(Some(s)) => s,
            Ok(None) => return Err("Session Expired or Revoked"),
            Err(e) => {
                Logger::sys_error("tenant_switch", "Redis session error", &e.to_string());
                return Err("Authentication service unavailable");
            }
        };

        let access_secret = extract_cookie_value(&cookie_header, "access_secret")
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
            Logger::sys_warn("tenant_switch", &format!("Auth failed: {}", msg), "");
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Unauthorized",
            ))));
        }
    };

    // ─── Bước 2: Resolve tenant_domain → tenant_id ──────────────────────────
    // [COMMENT]: L1 → Redis L2 → gRPC CP (single flight trong node)
    let tenant_id = match tenant_mgr.resolve_tenant_id(&tenant_domain).await {
        Some(id) => id,
        None => {
            Logger::sys_warn(
                "tenant_switch",
                &format!("Tenant domain '{}' not found", tenant_domain),
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Tenant unavailable",
            ))));
        }
    };

    // ─── Bước 3: CheckMembership(tenant_id, user_id) ────────────────────────
    // [COMMENT]: L1 (5 min TTL) → gRPC CheckMembership
    // Fail-closed: nếu gRPC lỗi, mặc định từ chối
    let membership = tenant_mgr.check_membership(&tenant_id, &claims.uid).await;

    if !membership.is_member {
        Logger::sys_warn(
            "tenant_switch",
            &format!(
                "User '{}' is not a member of tenant '{}'",
                claims.uid, tenant_domain
            ),
            "membership_denied",
        );
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Forbidden,
            "Tenant access denied",
        ))));
    }

    // ─── Bước 4: Re-issue JWT với tenant_id mới ─────────────────────────────
    // [COMMENT]: Cập nhật tenant_id trong claims, giữ nguyên các field khác
    claims.tenant_id = Some(tenant_id.clone());

    let new_jwt = match token_mgr.generate_token(&claims).await {
        Ok(jwt) => jwt,
        Err(e) => {
            Logger::sys_error("tenant_switch", "Failed to re-issue JWT", &e.to_string());
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Tenant unavailable",
            ))));
        }
    };

    // ─── Bước 5: Set cookies và trả response ────────────────────────────────
    let domain_str = if config.app_public_domain.trim().is_empty() {
        String::new()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };

    let tenant_body = serde_json::to_string(&TenantSwitchSuccessResponse {
        tenant_domain: tenant_domain.clone(),
    })
    .unwrap_or_default();
    // [COMMENT]: XSSI prefix cộng dồn để tránh lỗi escape brace trong format!
    let success_body = ")]}',\n".to_string() + &tenant_body;

    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(HttpStatusCode::Ok);
    builder.add_header("content-type", "application/json", None, false);
    builder.set_body(success_body);

    // [COMMENT]: Set access_token cookie với JWT mới (HttpOnly - JS không đọc được)
    let access_cookie = format!(
        "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        new_jwt, config.session_ttl_secs, domain_str
    );
    builder.add_header("set-cookie", &access_cookie, None, false);

    // [COMMENT]: Set tenant_domain cookie (không HttpOnly để JS đọc được cho UI)
    // tenant_code đã bỏ hoàn toàn - client dùng domain làm tenant identifier
    let tenant_cookie = format!(
        "tenant_domain={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        tenant_domain, domain_str
    );
    builder.add_header("set-cookie", &tenant_cookie, None, false);

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
        &format!(
            "Tenant switch to '{}' (role: {})",
            tenant_domain, membership.role
        ),
    );

    Some(Ok(Response::new(response)))
}
