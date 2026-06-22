use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use std::sync::Arc;
use tonic::{Response, Status};

use super::ext_authz::extract_cookie_value;
use crate::core::session::SessionManager;
use crate::core::token::TokenManager;
use crate::infra::controlplane::ControlPlaneClient;
use crate::observability::logger::Logger;

// [COMMENT]: Xử lý luồng logout độc lập để làm sạch mã nguồn của ext_authz.rs
pub async fn handle_logout(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    control_plane_client: &Arc<ControlPlaneClient>,
    client_headers: &std::collections::HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Chỉ áp dụng đối với HTTP POST /api/v1/auth/logout
    if !(path.starts_with("/api/v1/auth/logout") && method == "POST") {
        return None;
    }

    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();
    let jwt_token = extract_cookie_value(&cookie_header, "access_token");
    let access_key = extract_cookie_value(&cookie_header, "access_key");

    // [COMMENT]: Nếu không có thông tin cookie cần thiết, xem như đã logout thành công từ trước, trả về 204 ngay lập tức
    if jwt_token.is_none() || access_key.is_none() {
        Logger::authz_log(
            "unknown",
            method,
            path,
            "ALLOWED_BYPASS_LOGOUT",
            "No cookies present, bypass to 204",
        );
        let mut denied_builder = DeniedHttpResponseBuilder::new();
        denied_builder.set_http_status(HttpStatusCode::NoContent);
        add_clear_cookie_headers(&mut denied_builder);

        let mut response = CheckResponse::new();
        response.set_status(tonic::Status::unauthenticated("Logged out"));
        response.set_http_response(denied_builder);
        return Some(Ok(Response::new(response)));
    }

    let jwt_token = jwt_token.unwrap();
    let access_key = access_key.unwrap();

    // [COMMENT]: Thực hiện verify token JWT stateless
    let claims = match token_mgr.verify_token(&jwt_token).await {
        Ok(c) => c,
        Err(_) => {
            // [COMMENT]: Nếu token không hợp lệ hoặc hết hạn, trả về 204 kèm lệnh xóa cookie
            let mut denied_builder = DeniedHttpResponseBuilder::new();
            denied_builder.set_http_status(HttpStatusCode::NoContent);
            add_clear_cookie_headers(&mut denied_builder);

            let mut response = CheckResponse::new();
            response.set_status(tonic::Status::unauthenticated(
                "Invalid token during logout",
            ));
            response.set_http_response(denied_builder);
            return Some(Ok(Response::new(response)));
        }
    };

    // [COMMENT]: Ràng buộc khóa kiểm tra để tránh tấn công giả mạo (replay attack)
    if claims.access_key != access_key {
        let mut denied_builder = DeniedHttpResponseBuilder::new();
        denied_builder.set_http_status(HttpStatusCode::NoContent);
        add_clear_cookie_headers(&mut denied_builder);

        let mut response = CheckResponse::new();
        response.set_status(tonic::Status::unauthenticated(
            "Access key mismatch during logout",
        ));
        response.set_http_response(denied_builder);
        return Some(Ok(Response::new(response)));
    }

    // [COMMENT]: Xóa session tại Redis L2. Trả về HTTP 500 nếu gặp sự cố hạ tầng Redis
    if let Err(e) = session_mgr.delete_session(&claims.sub, &access_key).await {
        Logger::sys_error(
            "ext_authz.logout",
            "Failed to delete L2 session in Redis during logout",
            &e.to_string(),
        );

        let mut denied_builder = DeniedHttpResponseBuilder::new();
        denied_builder.set_http_status(HttpStatusCode::InternalServerError);
        denied_builder.set_body("Failed to process logout session revocation");

        let mut response = CheckResponse::new();
        response.set_status(tonic::Status::internal("Redis delete session failed"));
        response.set_http_response(denied_builder);
        return Some(Ok(Response::new(response)));
    }

    Logger::sys_info(
        "ext_authz.logout",
        &format!("Successfully deleted L2 session for user={}", claims.sub),
    );

    // [COMMENT]: Hủy refresh token lưu tại database Control Plane (Go) qua gRPC bất đồng bộ
    let refresh_token_opt = extract_cookie_value(&cookie_header, "refresh_token");
    if let Some(refresh_token) = refresh_token_opt {
        let cp_client = control_plane_client.clone();
        tokio::spawn(async move {
            Logger::sys_info(
                "ext_authz.logout",
                "Asynchronously revoking refresh token on Control Plane...",
            );
            match cp_client.revoke_opaque_refresh_token(refresh_token).await {
                Ok(_) => {
                    Logger::sys_info(
                        "ext_authz.logout",
                        "Successfully revoked refresh token on Control Plane via async gRPC.",
                    );
                }
                Err(e) => {
                    Logger::sys_error(
                        "ext_authz.logout",
                        "Failed to revoke refresh token on Control Plane via async gRPC (ignoring)",
                        &e.to_string(),
                    );
                }
            }
        });
    } else {
        Logger::sys_info(
            "ext_authz.logout",
            &format!(
                "No refresh token found for user={}, skipping CP revocation",
                claims.sub
            ),
        );
    }

    // [COMMENT]: Trả về HTTP 204 NoContent, xóa sạch mọi session cookie ngoại trừ client_device_id
    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::NoContent);
    add_clear_cookie_headers(&mut denied_builder);

    let mut response = CheckResponse::new();
    response.set_status(tonic::Status::unauthenticated("Logout success"));
    response.set_http_response(denied_builder);
    Some(Ok(Response::new(response)))
}

// [COMMENT]: Helper function để thêm các HTTP Header "set-cookie" cấu hình xóa cookie khỏi trình duyệt.
// Cấu hình cookie bao gồm Path, HttpOnly, Secure, SameSite, và thời gian hết hạn để xóa sạch trên Client.
pub(crate) fn add_clear_cookie_headers(
    builder: &mut envoy_types::ext_authz::v3::DeniedHttpResponseBuilder,
) {
    let cookies = vec![
        "access_token=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
        "access_key=; Path=/; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
        "access_secret=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
        "refresh_token=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=-1; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
    ];
    for cookie in cookies {
        builder.add_header("set-cookie", cookie, None, false);
    }
}
