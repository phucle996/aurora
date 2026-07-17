// ======================================================================================================
// 📂 billing/login.rs — handle_billing_login: Điều hướng đăng nhập Billing Auditor tại Edge
//
// 📌 LUỒNG:
//   POST /api/v1/billing/auth/login
//   1. Parse JSON: employee_code + secret_key
//   2. Gọi NATS billing.auth.verify_credentials sang cost-manager để xác thực Ed25519
//   3. Cấp phát Trinity Session Billing (billing:session: namespace cô lập)
//   4. Trả về Set-Cookie HTTP response
// ======================================================================================================

use crate::billing::claims::TokenManager;
use crate::billing::session::release_billing_session;
use crate::config::Config;
use crate::infra::nats::billing_auth::{
    VerifyBillingCredentialsRequest, VerifyBillingCredentialsResponse,
};
use crate::infra::nats::Nats;
use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use async_nats::HeaderMap;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use prost::Message;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use tonic::{Response, Status};

/// [COMMENT]: JSON payload nhận từ client khi đăng nhập Billing Auditor.
/// Chỉ dùng employee_code và secret_key — không có username/password hay tenant.
#[derive(Deserialize)]
pub struct BillingLoginPayload {
    // Mã nhân viên kiểm toán
    pub employee_code: Option<String>,
    // Khóa bí mật (private key Ed25519 dạng Base64 để ký challenge)
    pub secret_key: Option<String>,
    // Cho phép backward-compat với field username/password cũ (deprecated)
    #[allow(dead_code)]
    pub username: Option<String>,
    #[allow(dead_code)]
    pub password: Option<String>,
    #[allow(dead_code)]
    pub trust_device: Option<bool>,
    // Zone context (optional)
    pub zone_code: Option<String>,
}

/// [COMMENT]: JSON response lỗi chung
#[derive(Serialize)]
pub struct ErrorResponse {
    pub error_message: String,
}

/// [COMMENT]: Hàm xử lý đăng nhập Billing Auditor cục bộ tại biên.
/// Intercept: POST /api/v1/billing/auth/login
pub async fn handle_billing_login(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    nats: &Arc<Nats>,
    redis_client: &redis::Client,
    config: &Config,
    _client_headers: &std::collections::HashMap<String, String>,
    req: &envoy_types::pb::envoy::service::auth::v3::CheckRequest,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chỉ intercept HTTP POST /api/v1/billing/auth/login
    if !(method == "POST" && path == "/api/v1/billing/auth/login") {
        return None;
    }

    Logger::sys_info("billing.login", "Intercepted billing login request at edge");

    // [COMMENT]: 1. Trích xuất Request Body thô dạng byte nhị phân từ Envoy
    let raw_body_bytes = req
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

    if raw_body_bytes.is_empty() {
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::BadRequest,
            "Request body is empty",
        ))));
    }

    // [COMMENT]: 2. Giải mã JSON payload — Zero-copy từ byte slice
    let payload: BillingLoginPayload = match serde_json::from_slice(&raw_body_bytes) {
        Ok(p) => p,
        Err(e) => {
            Logger::sys_warn(
                "billing.login",
                &format!("Failed to parse JSON body: {}", e),
                "",
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Invalid JSON payload format",
            ))));
        }
    };

    // [COMMENT]: 3. Validate bắt buộc employee_code (fallback sang username nếu cũ)
    let employee_code = match payload.employee_code.as_ref().or(payload.username.as_ref()) {
        Some(u) if !u.trim().is_empty() => u.clone(),
        _ => {
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Employee code is required",
            ))));
        }
    };

    // [COMMENT]: 4. Validate bắt buộc secret_key (fallback sang password nếu cũ)
    let secret_key = match payload.secret_key.as_ref().or(payload.password.as_ref()) {
        Some(p) if !p.is_empty() => p.clone(),
        _ => {
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::BadRequest,
                "Secret key is required",
            ))));
        }
    };

    // [COMMENT]: 5. Encode Protobuf và gửi NATS request sang cost-manager
    let cp_req = VerifyBillingCredentialsRequest {
        employee_code: employee_code.clone(),
        secret_key,
    };

    Logger::sys_info(
        "billing.login",
        &format!(
            "Forwarding billing verify via NATS billing.auth.verify_credentials for employee_code={}",
            cp_req.employee_code
        ),
    );

    let mut payload_bytes = Vec::new();
    if let Err(e) = cp_req.encode(&mut payload_bytes) {
        Logger::sys_error(
            "billing.login",
            "Failed to serialize NATS verify request payload",
            &e.to_string(),
        );
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::InternalServerError,
            "Internal server error",
        ))));
    }

    // [COMMENT]: Inject traceparent header cho distributed tracing
    let mut headers = HeaderMap::new();
    if let Some(trace_id) = crate::observability::otel::OtelTracer::get_current_trace_id() {
        let span_id = uuid::Uuid::new_v4().simple().to_string()[..16].to_string();
        let traceparent = format!("00-{}-{}-01", trace_id, span_id);
        headers.insert("traceparent", traceparent.as_str());
    }

    let response_msg = match nats
        .client()
        .request_with_headers(
            "billing.auth.verify_credentials".to_string(),
            headers,
            payload_bytes.into(),
        )
        .await
    {
        Ok(msg) => msg,
        Err(e) => {
            Logger::sys_error(
                "billing.login",
                "NATS billing.auth.verify_credentials request failed",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Authentication service is temporarily unavailable",
            ))));
        }
    };

    // [COMMENT]: 6. Giải mã phản hồi Protobuf từ cost-manager
    let cp_res = match VerifyBillingCredentialsResponse::decode(response_msg.payload.as_ref()) {
        Ok(res) => res,
        Err(e) => {
            Logger::sys_error(
                "billing.login",
                "NATS billing verify response decode failed",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Authentication service response was invalid",
            ))));
        }
    };

    // [COMMENT]: 7. Reject nếu xác thực thất bại
    if !cp_res.valid {
        let err_msg = if cp_res.error_message.is_empty() {
            "Invalid employee code or secret key".to_string()
        } else {
            cp_res.error_message.clone()
        };
        Logger::sys_warn(
            "billing.login",
            &format!("Authentication rejected by billing backend: {}", err_msg),
            "",
        );
        return Some(Ok(Response::new(build_denied_json(
            HttpStatusCode::Unauthorized,
            &err_msg,
        ))));
    }

    // [COMMENT]: 8. Cấp phát Trinity Session Billing (cô lập, không có tenant/device)
    let zone_code = payload.zone_code.as_deref().unwrap_or("global");
    let res_val = match release_billing_session(
        session_mgr,
        token_mgr,
        nats,
        redis_client,
        config,
        &cp_res.user_id,
        &cp_res.employee_code,
        &cp_res.role_id,
        cp_res.level,
        zone_code,
    )
    .await
    {
        Ok(r) => r,
        Err(e) => {
            Logger::sys_error(
                "billing.login",
                "Release billing session failed",
                &e.to_string(),
            );
            return Some(Ok(Response::new(build_denied_json(
                HttpStatusCode::InternalServerError,
                "Failed to issue session state",
            ))));
        }
    };

    // [COMMENT]: 9. Thiết lập Set-Cookie headers
    let domain_str = if config.app_public_domain.trim().is_empty() {
        "".to_string()
    } else {
        format!("; Domain={}", config.app_public_domain.trim())
    };

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    // [COMMENT]: Trả về HTTP 204 No Content (không body) khi đăng nhập thành công
    denied_builder.set_http_status(HttpStatusCode::NoContent);

    // [COMMENT]: Set-Cookie access_token (HttpOnly — JS không đọc được)
    let access_cookie = format!(
        "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_token, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &access_cookie, None, false);

    // [COMMENT]: Set-Cookie access_key (HttpOnly)
    let key_cookie = format!(
        "access_key={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_key, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &key_cookie, None, false);

    // [COMMENT]: Set-Cookie access_secret (HttpOnly)
    let secret_cookie = format!(
        "access_secret={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}{}",
        res_val.access_secret, config.session_ttl_secs, domain_str
    );
    denied_builder.add_header("set-cookie", &secret_cookie, None, false);

    // [COMMENT]: Set-Cookie zone_code (không HttpOnly — cho UI đọc được)
    let zone_cookie = format!(
        "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000{}",
        zone_code, domain_str
    );
    denied_builder.add_header("set-cookie", &zone_cookie, None, false);

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated(
        "Local intercept billing login success",
    ));
    response.set_http_response(denied_builder);

    Logger::sys_info(
        "billing.login",
        &format!(
            "Billing login session released successfully for user_id={}",
            cp_res.user_id
        ),
    );

    Some(Ok(Response::new(response)))
}

// ─── Helper ────────────────────────────────────────────────────────────────────

/// [COMMENT]: Helper xây dựng denied response chứa body JSON lỗi
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
