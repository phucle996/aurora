// ======================================================================================================
// 📂 MODULE: acl/src/service/zone_catalog.rs
//            Xử Lý Trực Tiếp API Zone Catalog Tại Biên (Edge) Theo Ma Trận Quyền Hạn
// ======================================================================================================

use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use serde::Serialize;
use std::sync::Arc;
use tonic::{Response, Status};

use crate::core::session::SessionManager;
use crate::core::token::TokenManager;
use crate::core::zone::ZoneManager;
use crate::observability::logger::Logger;
use crate::service::ext_authz::extract_cookie_value;

// [COMMENT]: Cấu trúc JSON trả về cho Client, khớp với ZoneCatalog cũ của Go
#[derive(Serialize)]
pub struct ZoneCatalogEntry {
    pub code: String,
    pub name: String,
}

// [COMMENT]: Hàm xử lý chính intercept request lấy catalog của Zone
pub async fn handle_zone_catalog(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    zone_mgr: &Arc<ZoneManager>,
    client_headers: &std::collections::HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    // [COMMENT]: Xác định route đang truy cập thuộc Admin hay User
    let is_admin_catalog = path.starts_with("/admin/core/zones/catalog");
    let is_user_catalog = path.starts_with("/api/v1/zones/catalog");

    // [COMMENT]: Chỉ intercept các request GET tương ứng
    if !(method == "GET" && (is_admin_catalog || is_user_catalog)) {
        return None;
    }

    // [COMMENT]: Đánh giá trạng thái đăng nhập và vai trò người dùng từ Cookies
    let cookie_header = client_headers.get("cookie").cloned().unwrap_or_default();
    let is_logged_in_admin = async {
        let jwt_token = extract_cookie_value(&cookie_header, "access_token")?;
        let access_key = extract_cookie_value(&cookie_header, "access_key")?;

        // [COMMENT]: Giải mã token stateless
        let claims = token_mgr.verify_token(&jwt_token).await.ok()?;
        if claims.access_key != access_key {
            return None;
        }

        // [COMMENT]: Kiểm tra session hoạt động trong Redis L2 tương ứng với vai trò
        if claims.is_admin() {
            let _admin_session = session_mgr
                .get_admin_session(&access_key)
                .await
                .ok()??;
        } else {
            let _user_session = session_mgr
                .get_session(
                    claims.zone_id.as_deref().unwrap_or("global"),
                    claims.tenant_id.as_deref().unwrap_or("global"),
                    &claims.uid,
                    &access_key,
                )
                .await
                .ok()??;
        }

        // [COMMENT]: Trả về kết quả kiểm tra quyền admin
        Some(claims.is_admin())
    }
    .await;

    // [COMMENT]: Lấy toàn bộ zone hiện tại từ L1 cache của ACL
    let all_zones = zone_mgr.get_all_zones().await;
    let mut catalog = Vec::new();

    // [COMMENT]: Khai báo thông tin zone global ảo
    let global_zone = ZoneCatalogEntry {
        code: "global".to_string(),
        name: "Global Zone".to_string(),
    };

    // [COMMENT]: Áp dụng bộ lọc ma trận quyền hạn hiển thị
    match is_logged_in_admin {
        Some(true) => {
            // [COMMENT]: ĐÃ ĐĂNG NHẬP + ADMIN -> Trả về toàn bộ zone kèm global
            for z in all_zones {
                catalog.push(ZoneCatalogEntry {
                    code: z.code,
                    name: z.name,
                });
            }
            catalog.push(global_zone);
        }
        Some(false) => {
            // [COMMENT]: ĐÃ ĐĂNG NHẬP + USER THƯỜNG -> Chỉ các zone active và draining, không kèm global
            for z in all_zones {
                if z.status == "active" || z.status == "draining" {
                    catalog.push(ZoneCatalogEntry {
                        code: z.code,
                        name: z.name,
                    });
                }
            }
        }
        None => {
            // [COMMENT]: CHƯA ĐĂNG NHẬP (ANONYMOUS)
            if is_admin_catalog {
                // [COMMENT]: Trang đăng nhập Admin -> Trả về các zone active, draining kèm global
                for z in all_zones {
                    if z.status == "active" || z.status == "draining" {
                        catalog.push(ZoneCatalogEntry {
                            code: z.code,
                            name: z.name,
                        });
                    }
                }
                catalog.push(global_zone);
            } else {
                // [COMMENT]: Endpoint public của User -> Chỉ trả về các zone active, draining (không global)
                for z in all_zones {
                    if z.status == "active" || z.status == "draining" {
                        catalog.push(ZoneCatalogEntry {
                            code: z.code,
                            name: z.name,
                        });
                    }
                }
            }
        }
    }

    // [COMMENT]: Chuyển dữ liệu catalog sang JSON
    let json_body = match serde_json::to_string(&catalog) {
        Ok(body) => body,
        Err(e) => {
            Logger::sys_error(
                "zone_catalog",
                "Failed to serialize catalog",
                &e.to_string(),
            );
            let mut denied_builder = DeniedHttpResponseBuilder::new();
            denied_builder.set_http_status(HttpStatusCode::InternalServerError);
            denied_builder.set_body("Internal Server Error");

            let mut response = CheckResponse::new();
            response.set_status(tonic::Status::internal("Failed to serialize catalog"));
            response.set_http_response(denied_builder);
            return Some(Ok(Response::new(response)));
        }
    };

    // [COMMENT]: Xây dựng phản hồi HTTP 200 OK trực tiếp tại biên (Local Intercept) kèm tiền tố XSSI
    let xssi_json_body = format!(")]}}',\n{}", json_body);

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::Ok);
    denied_builder.add_header("content-type", "application/json", None, false);
    denied_builder.set_body(xssi_json_body);

    let mut response = CheckResponse::new();
    // [COMMENT]: Envoy yêu cầu gRPC status không phải OK để kích hoạt DeniedResponse cục bộ
    response.set_status(tonic::Status::unauthenticated(
        "Zone catalog local intercept",
    ));
    response.set_http_response(denied_builder);

    Some(Ok(Response::new(response)))
}
