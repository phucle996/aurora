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
        Ok(access_key) => {
            denied_builder.set_body(format!(
                r#"{{"data":{{"authenticated":true,"access_key":"{}"}}}}"#,
                access_key
            ));
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
