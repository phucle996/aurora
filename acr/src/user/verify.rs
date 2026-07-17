// ======================================================================================================
// 📂 user/verify.rs — Edge session verification for User
// ======================================================================================================

use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;

use crate::billing::claims::TokenManager;
use crate::config::Config;
use crate::infra::nats::Nats;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::user::claims::Claims;
use crate::user::recovery::try_handle_recovery_session;

fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}

fn build_denied_json(status: HttpStatusCode, message: &str) -> CheckResponse {
    let json_body = format!(")]}}',\n{{\"error_message\":\"{}\"}}", message);
    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(status);
    denied_builder.add_header("content-type", "application/json", None, false);
    denied_builder.set_body(json_body);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(message));
    response.set_http_response(denied_builder);
    response
}

pub struct VerifyEdgeSessionResult {
    pub claims: Option<Claims>,
    pub access_key: String,
    pub denial_response: Option<Response<CheckResponse>>,
    pub cookies_to_set: Vec<String>,
}

/// [COMMENT]: Edge session verifier — kiểm tra Trinity credentials (JWT + access_key + access_secret)
/// Tự động xử lý Cookie Extraction, Token Recovery (sliding window khi JWT hết hạn), và Session Rotation nội bộ.
pub async fn verify_edge_session(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    nats: &Arc<Nats>,
    config: &Config,
    cookie_header: &str,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> VerifyEdgeSessionResult {
    use crate::gateway::ext_authz::{extract_cookie_value, parse_trinity_cookies};

    let jwt_token = extract_cookie_value(cookie_header, COOKIE_ACCESS_TOKEN).or_else(|| {
        client_headers.get("authorization").and_then(|h| {
            if h.to_lowercase().starts_with("bearer ") {
                Some(h[7..].trim().to_string())
            } else {
                None
            }
        })
    });

    let jwt_token = match jwt_token {
        Some(t) => t,
        None => {
            return VerifyEdgeSessionResult {
                claims: None,
                access_key: String::new(),
                denial_response: None,
                cookies_to_set: Vec::new(),
            };
        }
    };

    let claims = match token_mgr.verify_token(&jwt_token).await {
        Ok(c) => Some(c),
        Err(e) => {
            Logger::sys_debug(
                "user.verify",
                &format!("Token verification failed for path {}: {}", path, e),
            );
            None
        }
    };

    // [COMMENT]: Recovery Session (Sliding Window khi JWT hết hạn nhưng access_secret còn hợp lệ)
    if claims.is_none() {
        let tc = parse_trinity_cookies(cookie_header);
        if let (Some(jwt_str), Some(key_str), Some(_secret_str)) =
            (tc.access_token, tc.access_key, tc.access_secret)
        {
            let redis_client = session_mgr.redis_client_arc();
            if let Some(recovery_res) = try_handle_recovery_session(
                session_mgr,
                token_mgr,
                nats,
                redis_client.as_ref(),
                config,
                cookie_header,
                jwt_str,
                key_str,
                client_headers,
            )
            .await
            {
                match recovery_res {
                    Ok(resp) => {
                        return VerifyEdgeSessionResult {
                            claims: None,
                            access_key: String::new(),
                            denial_response: Some(resp),
                            cookies_to_set: Vec::new(),
                        };
                    }
                    Err(status) => {
                        return VerifyEdgeSessionResult {
                            claims: None,
                            access_key: String::new(),
                            denial_response: Some(Response::new(build_denied_json(
                                HttpStatusCode::InternalServerError,
                                &status.message(),
                            ))),
                            cookies_to_set: Vec::new(),
                        };
                    }
                }
            }
        }
    }

    let mut claims = match claims {
        Some(c) => c,
        None => {
            return VerifyEdgeSessionResult {
                claims: None,
                access_key: String::new(),
                denial_response: None,
                cookies_to_set: Vec::new(),
            };
        }
    };

    let access_key = match extract_cookie_value(cookie_header, COOKIE_ACCESS_KEY) {
        Some(k) => k,
        None => {
            Logger::authz_log(
                &claims.sub,
                method,
                path,
                "DENIED",
                "Missing access_key cookie",
            );
            return VerifyEdgeSessionResult {
                claims: None,
                access_key: String::new(),
                denial_response: Some(Response::new(build_denied_json(
                    HttpStatusCode::Unauthorized,
                    "Missing access_key cookie",
                ))),
                cookies_to_set: Vec::new(),
            };
        }
    };

    if claims.access_key != access_key {
        Logger::authz_log(
            &claims.sub,
            method,
            path,
            "DENIED",
            &format!(
                "Access key mismatch: claim={}, cookie={}",
                claims.access_key, access_key
            ),
        );
        return VerifyEdgeSessionResult {
            claims: None,
            access_key: String::new(),
            denial_response: Some(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Access Key Mismatch",
            ))),
            cookies_to_set: Vec::new(),
        };
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
        Ok(None) => {
            Logger::authz_log(
                &claims.sub,
                method,
                path,
                "DENIED",
                "Session expired or revoked in Redis L2",
            );
            return VerifyEdgeSessionResult {
                claims: None,
                access_key: access_key.clone(),
                denial_response: Some(Response::new(build_denied_json(
                    HttpStatusCode::Unauthorized,
                    "Session Expired or Revoked",
                ))),
                cookies_to_set: Vec::new(),
            };
        }
        Err(e) => {
            Logger::sys_error(
                "user.verify",
                "Redis error fetching user session",
                &e.to_string(),
            );
            return VerifyEdgeSessionResult {
                claims: None,
                access_key: access_key.clone(),
                denial_response: Some(Response::new(build_denied_json(
                    HttpStatusCode::InternalServerError,
                    "Authentication service unavailable",
                ))),
                cookies_to_set: Vec::new(),
            };
        }
    };

    let access_secret = match extract_cookie_value(cookie_header, COOKIE_ACCESS_SECRET) {
        Some(s) => s,
        None => {
            Logger::authz_log(
                &claims.sub,
                method,
                path,
                "DENIED",
                "Missing access_secret cookie",
            );
            return VerifyEdgeSessionResult {
                claims: None,
                access_key: access_key.clone(),
                denial_response: Some(Response::new(build_denied_json(
                    HttpStatusCode::Unauthorized,
                    "Missing access_secret cookie",
                ))),
                cookies_to_set: Vec::new(),
            };
        }
    };

    let incoming_hash = sha256_hash(&access_secret);
    if session.ash != incoming_hash {
        Logger::authz_log(
            &claims.sub,
            method,
            path,
            "DENIED",
            "Access secret mismatch",
        );
        return VerifyEdgeSessionResult {
            claims: None,
            access_key: access_key.clone(),
            denial_response: Some(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Access Secret Mismatch",
            ))),
            cookies_to_set: Vec::new(),
        };
    }

    let _ = session_mgr
        .update_last_seen(
            claims.zone_id.as_deref().unwrap_or("global"),
            claims.tenant_id.as_deref().unwrap_or("platform"),
            &claims.uid,
            &access_key,
            session.lsa,
        )
        .await;

    if claims.zone_id.is_none() {
        claims.zone_id = Some("global".to_string());
    }

    // [COMMENT]: Thực hiện Session Rotation (Sliding Session) nội bộ cho User
    let cookies_to_set = crate::user::rotate::handle_user_session_rotation(
        session_mgr,
        token_mgr,
        config,
        &claims,
        &access_key,
        cookie_header,
    )
    .await;

    VerifyEdgeSessionResult {
        claims: Some(claims),
        access_key,
        denial_response: None,
        cookies_to_set,
    }
}
