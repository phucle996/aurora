// ======================================================================================================
// 📂 MODULE: acl/src/service/session/verifier.rs
//            Xác thực trạng thái đăng nhập cho SRE Admin (API /admin/auth/session) tại Edge
// ======================================================================================================

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use std::sync::Arc;
use tonic::{Response, Status};

use crate::core::session::SessionManager;
use crate::core::token::TokenManager;
use crate::observability::logger::Logger;
use crate::service::ext_authz::{extract_cookie_value, sha256_hash};

// [COMMENT]: Xử lý chặn bắt endpoint GET /admin/auth/session tại biên để kiểm tra trạng thái đăng nhập SRE
pub async fn handle_admin_session_check(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    client_headers: &std::collections::HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chỉ intercept HTTP GET /admin/auth/session
    if !(method == "GET" && path == "/admin/auth/session") {
        return None;
    }

    Logger::sys_info(
        "SRE-Session",
        "Intercepted SRE Admin session check request at edge",
    );

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

    // 1. Thực hiện các bước kiểm tra xác thực tuần tự
    let is_authenticated = async {
        let jwt_token = extract_cookie_value(&cookie_header, "access_token")
            .ok_or("Missing access_token cookie")?;
        let access_key = extract_cookie_value(&cookie_header, "access_key")
            .ok_or("Missing access_key cookie")?;

        let claims = token_mgr.verify_token(&jwt_token).await.map_err(|e| {
            Logger::sys_debug("SRE-Session", &format!("JWT verification failed: {}", e));
            "Invalid access_token"
        })?;

        if claims.access_key != access_key {
            return Err("Access Key Mismatch");
        }

        if !claims.is_admin() {
            return Err("Not an admin");
        }

        let admin_sess = session_mgr
            .get_admin_session(&access_key)
            .await
            .map_err(|_| "Redis query error")?
            .ok_or("Session Expired or Revoked")?;

        let access_secret = extract_cookie_value(&cookie_header, "access_secret")
            .ok_or("Missing access_secret cookie")?;

        let incoming_hash = sha256_hash(&access_secret);
        if admin_sess.access_secret_hash != incoming_hash {
            return Err("Access Secret Mismatch");
        }

        Ok(access_key)
    }
    .await;

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::Ok);
    denied_builder.add_header("content-type", "application/json", None, false);

    match is_authenticated {
        Ok(_) => {
            denied_builder.set_body(r#"{"data":{"authenticated":true}}"#);
        }
        Err(_) => {
            denied_builder.set_body(r#"{"data":{"authenticated":false}}"#);
            // [COMMENT]: Xóa các cookie nếu xác thực session thất bại
            let cookies_to_clear = vec![
                "access_token=; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                "access_key=; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                "access_secret=; Path=/admin; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                "zone_code=; Path=/admin; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
            ];
            for cookie in cookies_to_clear {
                denied_builder.add_header("set-cookie", cookie, None, false);
            }
        }
    }

    let mut response = CheckResponse::new();
    response.set_status(Status::unauthenticated("SRE session status"));
    response.set_http_response(denied_builder);

    Some(Ok(Response::new(response)))
}

// [COMMENT]: Xử lý chặn bắt endpoint GET /api/v1/auth/session tại biên để kiểm tra trạng thái đăng nhập của User
pub async fn handle_user_session_check(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    zone_mgr: &Arc<crate::core::zone::ZoneManager>,
    control_plane_client: &Arc<crate::infra::controlplane::ControlPlaneClient>,
    config: &crate::config::Config,
    client_headers: &std::collections::HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chỉ intercept HTTP GET /api/v1/me/session
    if !(method == "GET" && path == "/api/v1/me/session") {
        return None;
    }

    Logger::sys_info(
        "User-Session",
        "Intercepted User session check request at edge",
    );

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();

    // 1. Thực hiện kiểm tra bộ ba Trinity Credentials của user
    let verify_trinity = async {
        let jwt_token = extract_cookie_value(&cookie_header, "access_token")
            .ok_or("Missing access_token cookie")?;
        let access_key = extract_cookie_value(&cookie_header, "access_key")
            .ok_or("Missing access_key cookie")?;
        let access_secret = extract_cookie_value(&cookie_header, "access_secret")
            .ok_or("Missing access_secret cookie")?;

        let claims = token_mgr.verify_token(&jwt_token).await.map_err(|e| {
            Logger::sys_debug("User-Session", &format!("JWT verification failed: {}", e));
            "Invalid access_token"
        })?;

        if claims.access_key != access_key {
            return Err("Access Key Mismatch");
        }

        let sess = session_mgr
            .get_session(
                claims.zone_id.as_deref().unwrap_or("global"),
                claims.tenant_id.as_deref().unwrap_or("global"),
                &claims.uid,
                &access_key,
            )
            .await
            .map_err(|_| "Redis query error")?
            .ok_or("Session Expired or Revoked")?;

        let incoming_hash = sha256_hash(&access_secret);
        if sess.ash != incoming_hash {
            return Err("Access Secret Mismatch");
        }

        Ok((claims, sess))
    };

    match verify_trinity.await {
        Ok((claims, sess)) => {
            // [COMMENT]: Cập nhật Last Seen At (throttle 30s)
            let _ = session_mgr
                .update_last_seen(
                    claims.zone_id.as_deref().unwrap_or("global"),
                    claims.tenant_id.as_deref().unwrap_or("global"),
                    &claims.uid,
                    &claims.access_key,
                    sess.lsa,
                )
                .await;

            // [COMMENT]: Kiểm tra xem có cần tự động Sliding Session (Trinity Rotation) không
            let rotation_cookies =
                crate::service::session::rotate_session::handle_session_rotation(
                    session_mgr,
                    token_mgr,
                    config,
                    &claims,
                    &sess,
                    &claims.access_key,
                )
                .await;

            let mut denied_builder = DeniedHttpResponseBuilder::new();
            denied_builder.set_http_status(HttpStatusCode::Ok);
            denied_builder.add_header("content-type", "application/json", None, false);
            denied_builder.set_body(r#"{"data":{"authenticated":true}}"#);

            for cookie in rotation_cookies {
                denied_builder.add_header("set-cookie", &cookie, None, false);
            }

            let mut response = CheckResponse::new();
            response.set_status(Status::unauthenticated("User session status"));
            response.set_http_response(denied_builder);

            Some(Ok(Response::new(response)))
        }
        Err(err_msg) => {
            // [COMMENT]: Trinity Cookies không hợp lệ. Kiểm tra xem có refresh_token cookie không để tự động phục hồi (Recovery)
            let refresh_token = extract_cookie_value(&cookie_header, "refresh_token");

            if refresh_token.is_some() {
                Logger::sys_info(
                    "User-Session",
                    &format!(
                        "Trinity auth failed: {}. Attempting session recovery at Edge.",
                        err_msg
                    ),
                );

                // [COMMENT]: Gọi hàm handle_session_recovery dùng chung
                let recovery_res =
                    crate::service::session::recovery_session::handle_session_recovery(
                        session_mgr,
                        token_mgr,
                        zone_mgr,
                        control_plane_client,
                        config,
                        &cookie_header,
                        client_headers,
                        err_msg,
                        method,
                        path,
                    )
                    .await;

                match recovery_res {
                    Ok(resp) => {
                        let inner = resp.into_inner();
                        let mut denied_builder = DeniedHttpResponseBuilder::new();
                        denied_builder.set_http_status(HttpStatusCode::Ok);
                        denied_builder.add_header("content-type", "application/json", None, false);

                        // [COMMENT]: Phân tích response từ recovery_session để xây dựng body phù hợp
                        if inner.status.is_some() && inner.status.as_ref().unwrap().code == 0 {
                            // [COMMENT]: Phục hồi thành công! Trích xuất và đẩy cookies sang client
                            denied_builder.set_body(r#"{"data":{"authenticated":true}}"#);

                            if let Some(http_resp) = inner.http_response {
                                use envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse;
                                if let HttpResponse::OkResponse(ok) = http_resp {
                                    for header_opt in ok.response_headers_to_add {
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
                            // [COMMENT]: Phục hồi thất bại! Xoá toàn bộ cookie cũ
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

                        let mut response = CheckResponse::new();
                        response.set_status(Status::unauthenticated("User session status"));
                        response.set_http_response(denied_builder);
                        Some(Ok(Response::new(response)))
                    }
                    Err(status) => {
                        // [COMMENT]: Lỗi hệ thống khi gọi gRPC/Vault... Trả về 200 OK với authenticated=false kèm xoá cookies
                        Logger::sys_error(
                            "User-Session",
                            "Recovery failed with system error status",
                            &status.to_string(),
                        );

                        let mut denied_builder = DeniedHttpResponseBuilder::new();
                        denied_builder.set_http_status(HttpStatusCode::Ok);
                        denied_builder.add_header("content-type", "application/json", None, false);
                        denied_builder.set_body(r#"{"data":{"authenticated":false}}"#);

                        let cookies_to_clear = vec![
                            "access_token=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                            "access_key=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                            "access_secret=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                            "refresh_token=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                        ];
                        for cookie in cookies_to_clear {
                            denied_builder.add_header("set-cookie", cookie, None, false);
                        }

                        let mut response = CheckResponse::new();
                        response.set_status(Status::unauthenticated("User session status"));
                        response.set_http_response(denied_builder);
                        Some(Ok(Response::new(response)))
                    }
                }
            } else {
                // [COMMENT]: Trinity hỏng và không mang refresh_token -> Trả về authenticated=false kèm dọn dẹp cookies
                let mut denied_builder = DeniedHttpResponseBuilder::new();
                denied_builder.set_http_status(HttpStatusCode::Ok);
                denied_builder.add_header("content-type", "application/json", None, false);
                denied_builder.set_body(r#"{"data":{"authenticated":false}}"#);

                let cookies_to_clear = vec![
                    "access_token=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                    "access_key=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                    "access_secret=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
                ];
                for cookie in cookies_to_clear {
                    denied_builder.add_header("set-cookie", cookie, None, false);
                }

                let mut response = CheckResponse::new();
                response.set_status(Status::unauthenticated("User session status"));
                response.set_http_response(denied_builder);

                Some(Ok(Response::new(response)))
            }
        }
    }
}
