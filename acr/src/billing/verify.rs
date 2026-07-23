// ======================================================================================================
// 📂 billing/verify.rs — Fail-closed Cost alias verifier
// ======================================================================================================

use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;

use crate::billing::session::BillingSessionAlias;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use crate::pkg::cookie::{COOKIE_BILLING_SESSION_ID, COOKIE_BILLING_SESSION_SECRET};

fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    format!("{:x}", Sha256::digest(secret.as_bytes()))
}

fn build_denied_json(status: HttpStatusCode, message: &str) -> CheckResponse {
    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(status);
    builder.add_header("content-type", "application/json", None, false);
    builder.add_header("cache-control", "no-store", None, false);
    builder.set_body(serde_json::json!({"error_message": message}).to_string());
    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(message));
    response.set_http_response(builder);
    response
}

pub struct VerifyBillingAliasResult {
    pub alias: Option<BillingSessionAlias>,
    pub alias_id: String,
    pub denial_response: Option<Response<CheckResponse>>,
}

/// [COMMENT]: Alias hợp lệ khi secret đúng và IAM source session vẫn tồn tại với cùng proof key.
pub async fn verify_billing_alias(
    session_mgr: &Arc<SessionManager>,
    cookie_header: &str,
    method: &str,
    path: &str,
) -> VerifyBillingAliasResult {
    use crate::gateway::ext_authz::extract_cookie_value;

    let reject = |message: &str| VerifyBillingAliasResult {
        alias: None,
        alias_id: String::new(),
        denial_response: Some(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            message,
        ))),
    };
    let alias_id = match extract_cookie_value(cookie_header, COOKIE_BILLING_SESSION_ID) {
        Some(value) if uuid::Uuid::parse_str(&value).is_ok() => value,
        _ => return reject("Cost session alias is required"),
    };
    let alias_secret = match extract_cookie_value(cookie_header, COOKIE_BILLING_SESSION_SECRET) {
        Some(value) if !value.is_empty() => value,
        _ => return reject("Cost session alias secret is required"),
    };
    let alias = match session_mgr.get_billing_alias(&alias_id).await {
        Ok(Some(alias)) => alias,
        Ok(None) => return reject("Cost session alias expired or was revoked"),
        Err(error) => {
            Logger::sys_error(
                "billing.verify",
                "Redis Security-State alias lookup failed",
                &error.to_string(),
            );
            return VerifyBillingAliasResult {
                alias: None,
                alias_id,
                denial_response: Some(Response::new(build_denied_json(
                    HttpStatusCode::InternalServerError,
                    "Authentication service unavailable",
                ))),
            };
        }
    };
    if alias.access_secret_hash != sha256_hash(&alias_secret) {
        Logger::authz_log(
            &alias.user_id,
            method,
            path,
            "DENIED",
            "Cost alias secret mismatch",
        );
        return reject("Cost session alias binding is invalid");
    }

    // [COMMENT]: Recheck source mỗi request làm alias chết cùng IAM session, kể cả invalidation index bị gián đoạn.
    match session_mgr
        .get_session(
            &alias.zone_id,
            &alias.tenant_id,
            &alias.user_id,
            &alias.source_access_key,
        )
        .await
    {
        Ok(Some(source)) if source.client_proof_public_key == alias.source_proof_public_key => {
            VerifyBillingAliasResult {
                alias: Some(alias),
                alias_id,
                denial_response: None,
            }
        }
        Ok(_) => reject("Source IAM session expired or was revoked"),
        Err(error) => {
            Logger::sys_error(
                "billing.verify",
                "Redis Security-State source lookup failed",
                &error.to_string(),
            );
            VerifyBillingAliasResult {
                alias: None,
                alias_id,
                denial_response: Some(Response::new(build_denied_json(
                    HttpStatusCode::InternalServerError,
                    "Authentication service unavailable",
                ))),
            }
        }
    }
}

/// [COMMENT]: Session endpoint chỉ trả identity của source session; permission được Cost resolve server-side.
pub async fn handle_billing_session_check(
    session_mgr: &Arc<SessionManager>,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    if !(method == "GET" && path == "/api/v1/billing/auth/session") {
        return None;
    }
    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();
    let verified = verify_billing_alias(session_mgr, &cookie_header, method, path).await;
    let alias = match verified.alias {
        Some(alias) => alias,
        None => {
            return Some(Ok(verified.denial_response.unwrap_or_else(|| {
                Response::new(build_denied_json(
                    HttpStatusCode::Unauthorized,
                    "Cost session alias is required",
                ))
            })))
        }
    };

    let mut builder = DeniedHttpResponseBuilder::new();
    builder.set_http_status(HttpStatusCode::Ok);
    builder.add_header("content-type", "application/json", None, false);
    builder.add_header("cache-control", "no-store", None, false);
    builder.set_body(
        serde_json::json!({
            "data": {
                "authenticated": true,
                "user": {"id": alias.user_id, "username": alias.username},
                "zone_id": alias.zone_id
            }
        })
        .to_string(),
    );
    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("Cost session status returned"));
    response.set_http_response(builder);
    Some(Ok(Response::new(response)))
}
