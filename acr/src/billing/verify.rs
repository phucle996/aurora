// ======================================================================================================
// 📂 billing/verify.rs — Edge session verification for Billing Auditor
// ======================================================================================================

use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;

use crate::billing::claims::{BillingClaims, TokenManager};
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;

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

pub struct VerifyBillingEdgeSessionResult {
    pub claims: Option<BillingClaims>,
    #[allow(dead_code)]
    pub access_key: String,
    pub denial_response: Option<Response<CheckResponse>>,
    pub cookies_to_set: Vec<String>,
}

/// [COMMENT]: Xác thực session cho Billing Auditor từ cookies / headers.
/// Tự động xử lý Cookie Extraction nội bộ.
pub async fn verify_billing_edge_session(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    cookie_header: &str,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> VerifyBillingEdgeSessionResult {
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
            return VerifyBillingEdgeSessionResult {
                claims: None,
                access_key: String::new(),
                denial_response: None,
                cookies_to_set: Vec::new(),
            };
        }
    };

    let claims = match token_mgr.verify_billing_token(&jwt_token).await {
        Ok(c) => c,
        Err(e) => {
            Logger::sys_debug(
                "billing.verify",
                &format!("Billing token verification failed for path {}: {}", path, e),
            );
            return VerifyBillingEdgeSessionResult {
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
                "Missing access_key cookie for billing",
            );
            return VerifyBillingEdgeSessionResult {
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
                "Billing access key mismatch: claim={}, cookie={}",
                claims.access_key, access_key
            ),
        );
        return VerifyBillingEdgeSessionResult {
            claims: None,
            access_key: String::new(),
            denial_response: Some(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Access Key Mismatch",
            ))),
            cookies_to_set: Vec::new(),
        };
    }

    let billing_session = match session_mgr.get_billing_session(&access_key).await {
        Ok(Some(s)) => s,
        Ok(None) => {
            Logger::authz_log(
                &claims.sub,
                method,
                path,
                "DENIED",
                "Billing session expired or revoked in Redis L2",
            );
            return VerifyBillingEdgeSessionResult {
                claims: None,
                access_key: access_key.clone(),
                denial_response: Some(Response::new(build_denied_json(
                    HttpStatusCode::Unauthorized,
                    "Billing Session Expired or Revoked",
                ))),
                cookies_to_set: Vec::new(),
            };
        }
        Err(e) => {
            Logger::sys_error(
                "billing.verify",
                "Redis error fetching billing session",
                &e.to_string(),
            );
            return VerifyBillingEdgeSessionResult {
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
                "Missing access_secret cookie for billing",
            );
            return VerifyBillingEdgeSessionResult {
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
    if billing_session.access_secret_hash != incoming_hash {
        Logger::authz_log(
            &claims.sub,
            method,
            path,
            "DENIED",
            "Billing access secret mismatch",
        );
        return VerifyBillingEdgeSessionResult {
            claims: None,
            access_key: access_key.clone(),
            denial_response: Some(Response::new(build_denied_json(
                HttpStatusCode::Unauthorized,
                "Access Secret Mismatch",
            ))),
            cookies_to_set: Vec::new(),
        };
    }

    VerifyBillingEdgeSessionResult {
        claims: Some(claims),
        access_key,
        denial_response: None,
        cookies_to_set: Vec::new(),
    }
}

/// [COMMENT]: Intercept GET /api/v1/billing/auth/session — trả về 200 OK với body {"data":{"authenticated":true/false}}
pub async fn handle_billing_session_check(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chỉ bắt GET request đến đúng /api/v1/billing/auth/session
    if !(method == "GET" && path == "/api/v1/billing/auth/session") {
        return None;
    }

    Logger::sys_info(
        "billing.session.check",
        "Intercepted billing session status check request at edge",
    );

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

    // [COMMENT]: Gọi verify_billing_edge_session dùng chung để tái sử dụng toàn bộ logic kiểm tra JWT, Redis Session, v.v.
    let verify_res = verify_billing_edge_session(
        session_mgr,
        token_mgr,
        &cookie_header,
        client_headers,
        method,
        path,
    )
    .await;

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::Ok);
    denied_builder.add_header("content-type", "application/json", None, false);

    // [COMMENT]: Session endpoint chỉ công bố trạng thái xác thực, không làm lộ thông tin người dùng.
    if verify_res.claims.is_some() {
        denied_builder.set_body(r#"{"data":{"authenticated":true}}"#);
    } else {
        denied_builder.set_body(r#"{"data":{"authenticated":false}}"#);
    }

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("Billing session check status"));
    response.set_http_response(denied_builder);

    Some(Ok(Response::new(response)))
}
