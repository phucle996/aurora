// ======================================================================================================
// 📂 user/verify.rs — Edge session verification for User
// ======================================================================================================

use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;

use crate::config::Config;
use crate::infra::redis::SessionManager;
use crate::infra::shared_redis::SharedRedisBus;
use crate::observability::logger::Logger;
use crate::pkg::cookie::*;
use crate::token::TokenManager;
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
    shared_redis_client: &redis::Client,
    shared_redis: &Arc<SharedRedisBus>,
    config: &Config,
    cookie_header: &str,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> VerifyEdgeSessionResult {
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

    let claims = match jwt_token {
        Some(token) => match token_mgr.verify_token(&token).await {
            Ok(claims) => Some(claims),
            Err(error) => {
                Logger::sys_debug(
                    "user.verify",
                    &format!("Token verification failed for path {}: {}", path, error),
                );
                None
            }
        },
        None => None,
    };

    // The opaque user/device credential is sufficient for recovery. Expired or
    // missing Trinity material is never decoded to recover an identity.
    if claims.is_none() {
        if let Some(recovery_res) = try_handle_recovery_session(
            session_mgr,
            token_mgr,
            shared_redis_client,
            shared_redis,
            config,
            cookie_header,
            client_headers,
        )
        .await
        {
            match recovery_res {
                Ok(response) => {
                    return VerifyEdgeSessionResult {
                        claims: None,
                        access_key: String::new(),
                        denial_response: Some(response),
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

/// [COMMENT]: Intercept GET /api/v1/me/session — trả về 200 OK với body {"data":{"authenticated":true/false}}
pub async fn handle_user_session_check(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    shared_redis_client: &redis::Client,
    shared_redis: &Arc<SharedRedisBus>,
    config: &Config,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chỉ bắt GET request đến đúng /api/v1/me/session
    if !(method == "GET" && path == "/api/v1/me/session") {
        return None;
    }

    Logger::sys_info(
        "user.session.check",
        "Intercepted user session status check request at edge",
    );

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

    // [COMMENT]: Gọi verify_edge_session dùng chung để tái sử dụng toàn bộ logic kiểm tra JWT, Redis Session, Recovery Sliding Window và Session Rotation
    let verify_res = verify_edge_session(
        session_mgr,
        token_mgr,
        shared_redis_client,
        shared_redis,
        config,
        &cookie_header,
        client_headers,
        method,
        path,
    )
    .await;

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::Ok);
    denied_builder.add_header("content-type", "application/json", None, false);

    // [COMMENT]: Nếu có claims, chứng tỏ session hợp lệ (hoặc vừa được rotate thành công)
    if verify_res.claims.is_some() {
        denied_builder.set_body(r#"{"data":{"authenticated":true}}"#);
        // [COMMENT]: Set bất kỳ cookies nào được trả về (ví dụ như rotated access_token)
        for cookie in verify_res.cookies_to_set {
            denied_builder.add_header("set-cookie", &cookie, None, false);
        }
    } else {
        // [COMMENT]: Nếu không có claims, kiểm tra xem có denial_response từ recovery trả về hay không
        if let Some(resp) = verify_res.denial_response {
            let inner = resp.into_inner();
            // [COMMENT]: Phục hồi thành công nếu status của recovery response là OK/0
            if inner.status.is_some() && inner.status.as_ref().unwrap().code == 0 {
                denied_builder.set_body(r#"{"data":{"authenticated":true}}"#);

                if let Some(http_resp) = inner.http_response {
                    use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;
                    if let HttpResponse::DeniedResponse(denied) = http_resp {
                        for header_opt in denied.headers {
                            if let Some(header) = header_opt.header {
                                if header.key.to_lowercase() == "set-cookie" {
                                    denied_builder.add_header(
                                        "set-cookie",
                                        &header.value,
                                        None,
                                        false,
                                    );
                                }
                            }
                        }
                    }
                }
            } else {
                // [COMMENT]: Phục hồi thất bại, trả về authenticated: false kèm xóa cookies cũ (nếu có)
                denied_builder.set_body(r#"{"data":{"authenticated":false}}"#);
                if let Some(http_resp) = inner.http_response {
                    use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;
                    if let HttpResponse::DeniedResponse(denied) = http_resp {
                        for header_opt in denied.headers {
                            if let Some(header) = header_opt.header {
                                if header.key.to_lowercase() == "set-cookie" {
                                    denied_builder.add_header(
                                        "set-cookie",
                                        &header.value,
                                        None,
                                        false,
                                    );
                                }
                            }
                        }
                    }
                }
            }
        } else {
            // [COMMENT]: Không có credentials và không thể recovery -> Trả về authenticated: false kèm dọn dẹp các cookie access_token cũ
            denied_builder.set_body(r#"{"data":{"authenticated":false}}"#);
            let cookies_to_clear = vec![
                "access_token=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                "access_key=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                "access_secret=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
            ];
            for cookie in cookies_to_clear {
                denied_builder.add_header("set-cookie", cookie, None, false);
            }
        }
    }

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("User session check status"));
    response.set_http_response(denied_builder);

    Some(Ok(Response::new(response)))
}
