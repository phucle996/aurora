// ======================================================================================================
// 📂 MODULE: acl/src/service/zone_resolution.rs
//            DỊCH VỤ PHÂN GIẢI & XÁC THỰC RÀNG BUỘC BẢO MẬT CỦA ZONE TẠI BIÊN (EDGE)
// ======================================================================================================
// [COMMENT]: File này chịu trách nhiệm đóng gói toàn bộ logic phân giải zone_code/zone_id,
// đối chiếu trạng thái hoạt động của Zone và kiểm tra quyền truy cập vùng dữ liệu (Zone Boundary).
// Đảm bảo nguyên tắc Separation of Concerns (SoC) và giúp file ext_authz.rs gọn gàng hơn.

use envoy_types::ext_authz::v3::CheckResponseExt;
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use std::sync::Arc;
use tonic::{Response, Status};

use crate::core::token::Claims;
use crate::core::zone::ZoneManager;
use crate::observability::logger::Logger;
use crate::service::ext_authz::extract_cookie_value;

#[derive(Debug)]
pub enum ZoneResolutionError {
    Missing,
    InvalidCode(String),
}

/// [COMMENT]: Hàm phụ trợ trích xuất và phân giải ngữ cảnh zone từ cookies hoặc headers
pub async fn resolve_zone_context(
    zone_mgr: &ZoneManager,
    cookie_header: &str,
    client_headers: &std::collections::HashMap<String, String>,
) -> Result<(String, String, String), ZoneResolutionError> {
    // [COMMENT]: Thử trích xuất từ cookie zone_code
    let mut requested_zone_code = extract_cookie_value(cookie_header, "zone_code");
    if requested_zone_code.is_none() {
        // [COMMENT]: Nếu không có cookie, thử lấy từ header x-zone-code
        requested_zone_code = client_headers
            .get("x-zone-code")
            .map(|s| s.clone())
            .or_else(|| client_headers.get("X-Zone-Code").map(|s| s.clone()));
    }

    // [COMMENT]: Phân giải theo mức độ ưu tiên (chỉ phân giải zone_code ra zone_id, không phân giải ngược từ headers)
    if let Some(ref code) = requested_zone_code {
        // [COMMENT]: Nếu có zone code, truy vấn trong L1 cache/gRPC xem có hợp lệ không
        if code == "global" {
            // [COMMENT]: Hỗ trợ phân giải cho virtual zone global của admin
            Ok((
                "global".to_string(),
                "global".to_string(),
                "active".to_string(),
            ))
        } else if let Some((id, status)) = zone_mgr.resolve_code_to_id_and_status(code).await {
            Ok((id, code.clone(), status))
        } else {
            Err(ZoneResolutionError::InvalidCode(code.clone()))
        }
    } else {
        // [COMMENT]: Không chỉ định thông tin zone nào
        Err(ZoneResolutionError::Missing)
    }
}

/// [COMMENT]: Hàm phân giải và xác thực Zone tại biên.
/// - Đầu vào: các thông tin định danh (claims), headers, cookies và context request.
/// - Đầu ra:
///   + Ok(cookies): danh sách cookie cần set (nếu có chuyển đổi zone thành công).
///   + Err(Response): phản hồi từ chối (DENIED) trực tiếp từ gateway gửi về client.
/// [COMMENT]: Hàm phân giải và xác thực Zone dành riêng cho Admin (SRE Sách lược)
pub async fn resolve_and_verify_zone_admin(
    zone_mgr: &Arc<ZoneManager>,
    claims: Option<&mut Claims>,
    cookie_header: &str,
    client_headers: &std::collections::HashMap<String, String>,
    method: &str,
    path: &str,
) -> Result<Vec<String>, Result<Response<CheckResponse>, Status>> {
    let mut cookies_to_set_zone = Vec::new();

    // [COMMENT]: 1. Phân giải ngữ cảnh zone từ cookie hoặc HTTP header
    let zone_res = resolve_zone_context(zone_mgr, cookie_header, client_headers).await;

    let (resolved_zone_id, resolved_zone_code, resolved_zone_status) = match zone_res {
        Ok(res) => (Some(res.0), Some(res.1), Some(res.2)),
        Err(ZoneResolutionError::InvalidCode(code)) => {
            Logger::authz_log(
                "admin",
                method,
                path,
                "DENIED",
                &format!("Admin requested zone code not found: {}", code),
            );
            return Err(Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Zone unavailable"),
            ))));
        }
        Err(ZoneResolutionError::Missing) => {
            // [COMMENT]: Fallback về zone của claims của admin (nếu đã đăng nhập)
            if let Some(ref c) = claims {
                if let Some(ref claims_zone_id) = c.zone_id {
                    if claims_zone_id == "global" {
                        (
                            Some("global".to_string()),
                            Some("global".to_string()),
                            Some("active".to_string()),
                        )
                    } else if let Some((code, status)) =
                        zone_mgr.resolve_id_to_code_and_status(claims_zone_id).await
                    {
                        (Some(claims_zone_id.clone()), Some(code), Some(status))
                    } else {
                        (None, None, None)
                    }
                } else {
                    (None, None, None)
                }
            } else {
                (None, None, None)
            }
        }
    };

    // [COMMENT]: 2. Xác thực theo trạng thái đăng nhập dựa trên Claims của Admin
    if let Some(ref c) = claims {
        // [COMMENT] --- ĐÃ ĐĂNG NHẬP ---
        // Đối với Admin đã đăng nhập: Ma trận chấp nhận toàn bộ zone kèm global (không lọc status)
        if let (Some(zone_id), Some(zone_code)) = (resolved_zone_id, resolved_zone_code) {
            let claims_mismatch = c.zone_id.as_ref() != Some(&zone_id);
            let cookie_mismatch =
                extract_cookie_value(cookie_header, "zone_code").as_ref() != Some(&zone_code);

            // [COMMENT]: Chặn đổi zone ngầm không thông qua API go-to-zone
            if claims_mismatch {
                Logger::authz_log(
                    &c.sub,
                    method,
                    path,
                    "DENIED",
                    &format!("Admin zone mismatch: JWT={:?}, Req={}", c.zone_id, zone_id),
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone unavailable"),
                ))));
            } else if cookie_mismatch {
                // [COMMENT]: Set lại cookie đồng bộ với JWT nếu bị lệch
                cookies_to_set_zone.push(format!(
                    "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000",
                    zone_code
                ));
            }
        }
    } else {
        // [COMMENT] --- CHƯA ĐĂNG NHẬP ---
        // Đối với Admin chưa đăng nhập: Ma trận chấp nhận các zone active, draining kèm global
        if let (Some(zone_id), Some(zone_code), Some(zone_status)) =
            (resolved_zone_id, resolved_zone_code, resolved_zone_status)
        {
            if zone_code != "global"
                && zone_id != "global"
                && zone_status != "active"
                && zone_status != "draining"
            {
                Logger::authz_log(
                    "anonymous_admin",
                    method,
                    path,
                    "DENIED",
                    &format!(
                        "Admin requested zone {} is inactive ({})",
                        zone_code, zone_status
                    ),
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone unavailable"),
                ))));
            }
        }
    }

    Ok(cookies_to_set_zone)
}

/// [COMMENT]: Hàm phân giải và xác thực Zone dành riêng cho End-User thông thường
pub async fn resolve_and_verify_zone_user(
    zone_mgr: &Arc<ZoneManager>,
    claims: Option<&mut Claims>,
    cookie_header: &str,
    client_headers: &std::collections::HashMap<String, String>,
    method: &str,
    path: &str,
) -> Result<Vec<String>, Result<Response<CheckResponse>, Status>> {
    let mut cookies_to_set_zone = Vec::new();

    // [COMMENT]: 1. Phân giải ngữ cảnh zone từ cookie hoặc HTTP header
    let zone_res = resolve_zone_context(zone_mgr, cookie_header, client_headers).await;

    let (resolved_zone_id, resolved_zone_code, resolved_zone_status) = match zone_res {
        Ok(res) => (Some(res.0), Some(res.1), Some(res.2)),
        Err(ZoneResolutionError::InvalidCode(code)) => {
            let sub = claims.as_ref().map(|c| c.sub.as_str()).unwrap_or("unknown");
            Logger::authz_log(
                sub,
                method,
                path,
                "DENIED",
                &format!("User requested zone code not found: {}", code),
            );
            return Err(Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Zone unavailable"),
            ))));
        }
        Err(ZoneResolutionError::Missing) => {
            // [COMMENT]: Fallback về zone của claims của user (nếu đã đăng nhập)
            if let Some(ref c) = claims {
                if let Some(ref claims_zone_id) = c.zone_id {
                    if let Some((code, status)) =
                        zone_mgr.resolve_id_to_code_and_status(claims_zone_id).await
                    {
                        (Some(claims_zone_id.clone()), Some(code), Some(status))
                    } else {
                        (None, None, None)
                    }
                } else {
                    (None, None, None)
                }
            } else {
                (None, None, None)
            }
        }
    };

    // [COMMENT]: 2. Xác thực theo trạng thái đăng nhập dựa trên Claims của User thường
    if let Some(ref c) = claims {
        // [COMMENT] --- ĐÃ ĐĂNG NHẬP ---
        // Đối với User đã đăng nhập: Ma trận chỉ cho phép các zone active và draining, không cho phép global
        if let (Some(zone_id), Some(zone_code), Some(zone_status)) = (
            &resolved_zone_id,
            &resolved_zone_code,
            &resolved_zone_status,
        ) {
            // [COMMENT]: Chặn tuyệt đối user thường truy cập vào Zone Global
            if zone_code == "global" || zone_id == "00000000-0000-0000-0000-000000000000" {
                Logger::authz_log(
                    &c.sub,
                    method,
                    path,
                    "DENIED",
                    "Forbidden global zone access for non-admin",
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone unavailable"),
                ))));
            }

            // [COMMENT]: Chặn các zone không ở trạng thái active/draining
            if zone_status != "active" && zone_status != "draining" {
                Logger::authz_log(
                    &c.sub,
                    method,
                    path,
                    "DENIED",
                    &format!(
                        "Forbidden access to inactive zone ({} is {})",
                        zone_code, zone_status
                    ),
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone unavailable"),
                ))));
            }

            let claims_mismatch = c.zone_id.as_ref() != Some(zone_id);
            let cookie_mismatch =
                extract_cookie_value(cookie_header, "zone_code").as_ref() != Some(zone_code);

            // [COMMENT]: Chặn đổi zone ngầm không thông qua API go-to-zone
            if claims_mismatch {
                Logger::authz_log(
                    &c.sub,
                    method,
                    path,
                    "DENIED",
                    &format!("User zone mismatch: JWT={:?}, Req={}", c.zone_id, zone_id),
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone unavailable"),
                ))));
            } else if cookie_mismatch {
                // [COMMENT]: Set lại cookie đồng bộ với JWT nếu bị lệch
                cookies_to_set_zone.push(format!(
                    "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000",
                    zone_code
                ));
            }
        }
    } else {
        // [COMMENT] --- CHƯA ĐĂNG NHẬP ---
        // Đối với User chưa đăng nhập: Ma trận chỉ cho phép các zone active và draining (không global)
        if let (Some(zone_id), Some(zone_code), Some(zone_status)) =
            (resolved_zone_id, resolved_zone_code, resolved_zone_status)
        {
            if zone_code == "global" || zone_id == "00000000-0000-0000-0000-000000000000" {
                Logger::authz_log(
                    "anonymous_user",
                    method,
                    path,
                    "DENIED",
                    "Anonymous user tried to access global zone",
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone unavailable"),
                ))));
            }
            if zone_status != "active" && zone_status != "draining" {
                Logger::authz_log(
                    "anonymous_user",
                    method,
                    path,
                    "DENIED",
                    &format!(
                        "Anonymous user tried to access inactive zone: {}",
                        zone_code
                    ),
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone unavailable"),
                ))));
            }
        }
    }

    Ok(cookies_to_set_zone)
}
