// ======================================================================================================
// 📂 user/zone_switcher.rs — User Zone Switch Handler (POST /api/v1/zone/go-to-zone)
// ======================================================================================================

use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::infra::shared_redis::SharedRedisBus;
use crate::infra::zone::resolve_code_to_id_and_status;
use crate::observability::logger::Logger;
use crate::token::TokenManager;
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
    let json_body = format!(
        ")]}}',\n{}",
        serde_json::to_string(&err_resp).unwrap_or_default()
    );

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

pub struct UserZoneSwitchWorkflowContext<'a> {
    pub session_mgr: &'a Arc<SessionManager>,
    pub token_mgr: &'a Arc<TokenManager>,
    pub shared_redis: &'a Arc<SharedRedisBus>,
    pub redis_client: &'a redis::Client,
    pub config: &'a Config,
}

pub struct UserZoneSwitchRequest<'a> {
    pub client_headers: &'a HashMap<String, String>,
    pub method: &'a str,
    pub path: &'a str,
}

/// [COMMENT]: Intercept POST /api/v1/zone/go-to-zone — xác thực Trinity và re-issue JWT với zone mới.
pub async fn handle_user_zone_switch(
    workflow: UserZoneSwitchWorkflowContext<'_>,
    request: UserZoneSwitchRequest<'_>,
) -> Option<Result<Response<CheckResponse>, Status>> {
    let UserZoneSwitchWorkflowContext {
        session_mgr,
        token_mgr,
        shared_redis,
        redis_client,
        config,
    } = workflow;
    let UserZoneSwitchRequest {
        client_headers,
        method,
        path,
    } = request;
    use crate::gateway::ext_authz::extract_cookie_value;
    use crate::pkg::cookie::{
        COOKIE_ACCESS_KEY, COOKIE_ACCESS_SECRET, COOKIE_ACCESS_TOKEN, COOKIE_ZONE_CODE,
    };

    if !(method == "POST" && path.starts_with("/api/v1/zone/go-to-zone")) {
        return None;
    }

    Logger::sys_info(
        "user.zone.switch",
        &format!("Intercepted user zone switch request at edge: {}", path),
    );

    let zone_code = match parse_zone_code(path) {
        Some(code) if !code.trim().is_empty() => code.trim().to_lowercase(),
        _ => {
            Logger::sys_warn("user.zone.switch", "Missing zone_code in query params", "");
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "zone_code is required",
            ))));
        }
    };

    let resolved = if zone_code == "global" {
        Some(("global".to_string(), "active".to_string()))
    } else {
        resolve_code_to_id_and_status(shared_redis, redis_client, &zone_code).await
    };

    let (zone_id, zone_status) = match resolved {
        Some(r) => r,
        None => {
            Logger::sys_warn(
                "user.zone.switch",
                &format!("Zone code not found: {}", zone_code),
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
            "user.zone.switch",
            &format!("Zone {} is inactive: {}", zone_code, zone_status),
            "",
        );
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::BadRequest,
            "Zone unavailable",
        ))));
    }

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

    let auth_result = async {
        let jwt_token = extract_cookie_value(&cookie_header, COOKIE_ACCESS_TOKEN)
            .ok_or("Missing access_token cookie")?;
        let access_key = extract_cookie_value(&cookie_header, COOKIE_ACCESS_KEY)
            .ok_or("Missing access_key cookie")?;

        let claims = token_mgr.verify_token(&jwt_token).await.map_err(|e| {
            Logger::sys_debug("user.zone.switch", &format!("JWT verify failed: {}", e));
            "Invalid access_token"
        })?;

        if claims.access_key != access_key {
            return Err("Access Key Mismatch");
        }

        let session = match session_mgr
            .get_session(
                claims.zone_id.as_deref().unwrap_or("global"),
                claims.tenant_id.as_deref().unwrap_or("platform"),
                &claims.uid,
                &access_key,
            )
            .await
        {
            Ok(Some(s)) => s,
            Ok(None) => return Err("Session Expired or Revoked"),
            Err(e) => {
                Logger::sys_error("user.zone.switch", "Redis session error", &e.to_string());
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
            Logger::sys_warn("user.zone.switch", &format!("Auth failed: {}", msg), "");
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Unauthorized",
            ))));
        }
    };

    claims.zone_id = if zone_code == "global" {
        Some("global".to_string())
    } else {
        Some(zone_id)
    };

    let new_jwt = match token_mgr.generate_token(&claims).await {
        Ok(jwt) => jwt,
        Err(e) => {
            Logger::sys_error("user.zone.switch", "Failed to re-issue JWT", &e.to_string());
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
        "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        new_jwt, config.session_ttl_secs, domain_str
    );
    builder.add_header("set-cookie", &access_cookie, None, false);

    let zone_cookie = format!(
        "{}={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        COOKIE_ZONE_CODE, zone_code, domain_str
    );
    builder.add_header("set-cookie", &zone_cookie, None, false);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(
        "Zone switch completed successfully",
    ));
    response.set_http_response(builder);

    Logger::authz_log(
        &claims.uid,
        method,
        path,
        "ALLOWED",
        &format!("User zone switch to '{}'", zone_code),
    );

    Some(Ok(Response::new(response)))
}
