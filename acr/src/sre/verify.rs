// ======================================================================================================
// 📂 sre/verify.rs — Edge session verification for SRE
// ======================================================================================================

use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;

use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::sre::claims::{SreClaims, SreTokenManager};

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

pub struct VerifySreEdgeSessionResult {
    pub claims: Option<SreClaims>,
    pub access_key: String,
    pub denial_response: Option<Response<CheckResponse>>,
    pub cookies_to_set: Vec<String>,
}

/// [COMMENT]: Edge session verifier cho SRE — kiểm tra credentials (JWT + access_key + access_secret)
/// Tự động xử lý Cookie Extraction và Session Rotation (Sliding Session) nội bộ.
pub async fn verify_sre_edge_session(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<SreTokenManager>,
    config: &Config,
    cookie_header: &str,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> VerifySreEdgeSessionResult {
    use crate::gateway::ext_authz::extract_cookie_value;

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
            return VerifySreEdgeSessionResult {
                claims: None,
                access_key: String::new(),
                denial_response: None,
                cookies_to_set: Vec::new(),
            };
        }
    };

    let mut claims = match token_mgr.verify_token(&jwt_token).await {
        Ok(c) => c,
        Err(e) => {
            Logger::sys_debug(
                "sre.verify",
                &format!("SRE token verification failed for path {}: {}", path, e),
            );
            return VerifySreEdgeSessionResult {
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
                "Missing access_key cookie for SRE",
            );
            return VerifySreEdgeSessionResult {
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
                "SRE access key mismatch: claim={}, cookie={}",
                claims.access_key, access_key
            ),
        );
        return VerifySreEdgeSessionResult {
            claims: None,
            access_key: String::new(),
            denial_response: Some(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Access Key Mismatch",
            ))),
            cookies_to_set: Vec::new(),
        };
    }

    let sre_session = match session_mgr.get_sre_session(&access_key).await {
        Ok(Some(s)) => s,
        Ok(None) => {
            Logger::authz_log(
                &claims.sub,
                method,
                path,
                "DENIED",
                "SRE session expired or revoked in Redis L2",
            );
            return VerifySreEdgeSessionResult {
                claims: None,
                access_key: access_key.clone(),
                denial_response: Some(Response::new(build_denied_json(
                    HttpStatusCode::Unauthorized,
                    "SRE Session Expired or Revoked",
                ))),
                cookies_to_set: Vec::new(),
            };
        }
        Err(e) => {
            Logger::sys_error(
                "sre.verify",
                "Redis error fetching SRE session",
                &e.to_string(),
            );
            return VerifySreEdgeSessionResult {
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
                "Missing access_secret cookie for SRE",
            );
            return VerifySreEdgeSessionResult {
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
    if sre_session.access_secret_hash != incoming_hash {
        Logger::authz_log(
            &claims.sub,
            method,
            path,
            "DENIED",
            "SRE access secret mismatch",
        );
        return VerifySreEdgeSessionResult {
            claims: None,
            access_key: access_key.clone(),
            denial_response: Some(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Access Secret Mismatch",
            ))),
            cookies_to_set: Vec::new(),
        };
    }

    if claims.zone_id.is_none() {
        claims.zone_id = Some("global".to_string());
    }

    // [COMMENT]: Thực hiện Session Rotation (Sliding Session) nội bộ cho SRE
    let cookies_to_set = crate::sre::rotate::handle_sre_session_rotation(
        session_mgr,
        token_mgr,
        config,
        &claims,
        &access_key,
    )
    .await;

    VerifySreEdgeSessionResult {
        claims: Some(claims),
        access_key,
        denial_response: None,
        cookies_to_set,
    }
}
