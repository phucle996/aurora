// ======================================================================================================
// 📂 sre/zone_switcher.rs — SRE Zone Switch Handler (POST /admin/zone/go-to-zone)
// ======================================================================================================

use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::infra::shared_redis::SharedRedisBus;
use crate::infra::zone::resolve_code_to_id_and_status;
use crate::observability::logger::Logger;
use crate::sre::claims::SreTokenManager;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use serde::Serialize;
use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};

#[derive(Serialize)]
pub struct ZoneSwitchSuccessResponse {
    pub zone_code: String,
}

#[derive(Serialize)]
pub struct ErrorResponse {
    pub error_message: String,
}

fn parse_zone_code(path: &str) -> Option<String> {
    path.find('?').and_then(|pos| {
        let query_str = &path[pos + 1..];
        query_str
            .split('&')
            .find(|pair| pair.starts_with("zone_code="))
            .map(|pair| pair["zone_code=".len()..].to_string())
    })
}

fn build_denied_json(status: HttpStatusCode, message: &str) -> CheckResponse {
    let err_resp = ErrorResponse {
        error_message: message.to_string(),
    };
    let json_body = serde_json::to_string(&err_resp).unwrap_or_default();

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(status);
    denied_builder.add_header("content-type", "application/json", None, false);
    denied_builder.set_body(json_body);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(message));
    response.set_http_response(denied_builder);
    response
}

fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}

/// [COMMENT]: Intercept POST /admin/zone/go-to-zone — dành riêng cho SRE Admin để chuyển vùng zone hoạt động.
// Keep the zone-switch workflow's authority and storage capabilities visible.
#[allow(clippy::too_many_arguments)]
pub async fn handle_sre_zone_switch(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<SreTokenManager>,
    shared_redis: &Arc<SharedRedisBus>,
    redis_client: &redis::Client,
    config: &Config,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    use crate::gateway::ext_authz::extract_cookie_value;
    use crate::pkg::cookie::{
        COOKIE_ACCESS_KEY, COOKIE_ACCESS_SECRET, COOKIE_ACCESS_TOKEN, COOKIE_ZONE_CODE,
    };

    // [COMMENT]: Chỉ bắt đúng POST request tới endpoint chuyển zone của admin
    if !(method == "POST" && path.starts_with("/admin/zone/go-to-zone")) {
        return None;
    }

    Logger::sys_info(
        "sre.zone.switch",
        &format!("Intercepted SRE admin zone switch request: {}", path),
    );

    // [COMMENT]: Trích xuất mã zone_code từ query parameters
    let zone_code = match parse_zone_code(path) {
        Some(code) if !code.trim().is_empty() => code.trim().to_lowercase(),
        _ => {
            Logger::sys_warn("sre.zone.switch", "Missing zone_code query param", "");
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "zone_code is required",
            ))));
        }
    };

    // [COMMENT]: Nếu zone_code là global thì chuyển trực tiếp, ngược lại thực hiện phân giải và kiểm tra trạng thái hoạt động của zone
    let resolved = if zone_code == "global" {
        Some(("global".to_string(), "active".to_string()))
    } else {
        resolve_code_to_id_and_status(shared_redis, redis_client, &zone_code).await
    };

    let (zone_id, zone_status) = match resolved {
        Some(r) => r,
        None => {
            Logger::sys_warn(
                "sre.zone.switch",
                &format!("SRE zone switch target code not found: {}", zone_code),
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Zone unavailable",
            ))));
        }
    };

    if zone_code != "global" && zone_status != "active" && zone_status != "draining" {
        Logger::sys_warn(
            "sre.zone.switch",
            &format!("SRE target zone {} is inactive: {}", zone_code, zone_status),
            "",
        );
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::BadRequest,
            "Zone unavailable",
        ))));
    }

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

    // [COMMENT]: Thực hiện kiểm tra tính hợp lệ của Credentials (JWT + SRE Redis Session)
    let auth_result = async {
        let jwt_token = extract_cookie_value(&cookie_header, COOKIE_ACCESS_TOKEN)
            .ok_or("Missing access_token cookie")?;
        let access_key = extract_cookie_value(&cookie_header, COOKIE_ACCESS_KEY)
            .ok_or("Missing access_key cookie")?;

        let claims = token_mgr.verify_token(&jwt_token).await.map_err(|e| {
            Logger::sys_debug(
                "sre.zone.switch",
                &format!("JWT verification failed for SRE: {}", e),
            );
            "Invalid access_token"
        })?;

        if claims.access_key != access_key {
            return Err("Access Key Mismatch");
        }

        // [COMMENT]: Query SRE Session trực tiếp bằng get_sre_session từ namespace của SRE (HA compliant)
        let session = match session_mgr.get_sre_session(&access_key).await {
            Ok(Some(s)) => s,
            Ok(None) => return Err("SRE Session Expired or Revoked"),
            Err(e) => {
                Logger::sys_error(
                    "sre.zone.switch",
                    "Redis query error for SRE session",
                    &e.to_string(),
                );
                return Err("Authentication service unavailable");
            }
        };

        let access_secret = extract_cookie_value(&cookie_header, COOKIE_ACCESS_SECRET)
            .ok_or("Missing access_secret cookie")?;
        let incoming_hash = sha256_hash(&access_secret);
        if session.access_secret_hash != incoming_hash {
            return Err("Access Secret Mismatch");
        }

        Ok((claims, access_key))
    }
    .await;

    let (mut claims, _access_key) = match auth_result {
        Ok(val) => val,
        Err(msg) => {
            Logger::sys_warn(
                "sre.zone.switch",
                &format!("SRE Auth verification failed: {}", msg),
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Unauthorized",
            ))));
        }
    };

    // [COMMENT]: Cập nhật zone_id mới vào Claims và cấp lại Token mới
    claims.zone_id = if zone_code == "global" {
        Some("global".to_string())
    } else {
        Some(zone_id)
    };

    let new_jwt = match token_mgr.generate_token(&claims).await {
        Ok(jwt) => jwt,
        Err(e) => {
            Logger::sys_error(
                "sre.zone.switch",
                "Failed to generate new JWT token for SRE",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Zone unavailable",
            ))));
        }
    };

    let domain_str = if config.app_public_domain.trim().is_empty() {
        String::new()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };

    let success_body = format!(
        ")]}}'\\n{}",
        serde_json::to_string(&ZoneSwitchSuccessResponse {
            zone_code: zone_code.clone()
        })
        .unwrap_or_default()
    );

    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(HttpStatusCode::Ok);
    builder.add_header("content-type", "application/json", None, false);
    builder.set_body(success_body);

    let access_cookie = format!(
        "access_token={}; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        new_jwt, config.session_ttl_secs, domain_str
    );
    builder.add_header("set-cookie", &access_cookie, None, false);

    let zone_cookie = format!(
        "{}={}; Path=/admin; Secure; SameSite=Lax; Max-Age=31536000{}",
        COOKIE_ZONE_CODE, zone_code, domain_str
    );
    builder.add_header("set-cookie", &zone_cookie, None, false);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(
        "SRE Zone switch completed successfully",
    ));
    response.set_http_response(builder);

    Logger::authz_log(
        &claims.sub,
        method,
        path,
        "ALLOWED",
        &format!("SRE zone switch to '{}'", zone_code),
    );

    Some(Ok(Response::new(response)))
}
