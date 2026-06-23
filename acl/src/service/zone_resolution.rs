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

use super::ext_authz::extract_cookie_value;
use crate::config::Config;
use crate::core::token::{Claims, TokenManager};
use crate::core::zone::ZoneManager;
use crate::observability::logger::Logger;

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
            .cloned()
            .or_else(|| client_headers.get("X-Zone-Code").cloned());
    }

    // [COMMENT]: Phân giải theo mức độ ưu tiên (chỉ phân giải zone_code ra zone_id, không phân giải ngược từ headers)
    if let Some(ref code) = requested_zone_code {
        // [COMMENT]: Nếu có zone code, truy vấn trong L1 cache/gRPC xem có hợp lệ không
        if let Some((id, status)) = zone_mgr.resolve_code_to_id_and_status(code).await {
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
pub async fn resolve_and_verify_zone(
    zone_mgr: &Arc<ZoneManager>,
    _token_mgr: &Arc<TokenManager>,
    _config: &Config,
    claims: &mut Claims,
    cookie_header: &str,
    client_headers: &std::collections::HashMap<String, String>,
    method: &str,
    path: &str,
) -> Result<Vec<String>, Result<Response<CheckResponse>, Status>> {
    let mut cookies_to_set_zone = Vec::new();

    // [COMMENT]: Gọi hàm helper để phân giải ngữ cảnh zone được truyền lên từ request
    let zone_res = resolve_zone_context(zone_mgr, cookie_header, client_headers).await;

    let (resolved_zone_id, resolved_zone_code, resolved_zone_status) = match zone_res {
        Ok(res) => (Some(res.0), Some(res.1), Some(res.2)),
        Err(ZoneResolutionError::InvalidCode(code)) => {
            // [COMMENT]: Chặn truy cập ngay nếu zone code không tồn tại
            Logger::authz_log(
                &claims.sub,
                method,
                path,
                "DENIED",
                &format!("Requested zone code not found: {}", code),
            );
            // [COMMENT]: Trả lỗi "Zone unavailable" về client để ẩn giấu thông tin chi tiết hệ thống
            return Err(Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Zone unavailable"),
            ))));
        }
        Err(ZoneResolutionError::Missing) => {
            // [COMMENT]: Nếu request không có zone, fallback về zone_id lưu trong JWT Claims hiện tại
            if let Some(ref claims_zone_id) = claims.zone_id {
                if let Some((code, status)) = zone_mgr.resolve_id_to_code_and_status(claims_zone_id).await {
                    (Some(claims_zone_id.clone()), Some(code), Some(status))
                } else {
                    (None, None, None)
                }
            } else {
                (None, None, None)
            }
        }
    };

    // [COMMENT]: 3. Thực hiện xác thực các ràng buộc bảo mật của Zone đối với End-User (không áp dụng cho admin)
    if let (Some(ref zone_id), Some(ref zone_code), Some(ref zone_status)) = (
        &resolved_zone_id,
        &resolved_zone_code,
        &resolved_zone_status,
    ) {
        if !claims.is_admin() {
            // [COMMENT]: Ràng buộc 1: Chặn tuyệt đối user thường truy cập vào Zone Global (UUID rỗng)
            if zone_code == "global" || zone_id == "00000000-0000-0000-0000-000000000000" {
                Logger::authz_log(
                    &claims.sub,
                    method,
                    path,
                    "DENIED",
                    &format!(
                        "Forbidden global zone access for non-admin: user_id={}",
                        claims.sub
                    ),
                );
                // [COMMENT]: Trả lỗi "Zone unavailable" về client để ẩn giấu thông tin
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone unavailable"),
                ))));
            }

            // [COMMENT]: Ràng buộc 2: Chặn user thường vào zone không hoạt động (status != "active")
            if zone_status != "active" {
                Logger::authz_log(
                    &claims.sub,
                    method,
                    path,
                    "DENIED",
                    &format!(
                        "Forbidden access to inactive zone ({} is {}): user_id={}",
                        zone_code, zone_status, claims.sub
                    ),
                );
                // [COMMENT]: Trả lỗi "Zone unavailable" về client để ẩn giấu thông tin
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone unavailable"),
                ))));
            }
        }
    }

    // [COMMENT]: 4. Thực hiện kiểm tra trùng khớp Active Zone của phiên làm việc hiện tại
    if let (Some(zone_id), Some(zone_code)) = (resolved_zone_id, resolved_zone_code) {
        let claims_mismatch = claims.zone_id.as_ref() != Some(&zone_id);
        let cookie_mismatch =
            extract_cookie_value(cookie_header, "zone_code").as_ref() != Some(&zone_code);

        // [COMMENT]: Cấm tự động đổi/ký lại zone ngầm trên các request API tự động của máy (machine-triggered).
        // Chuyển zone phải do người dùng thao tác tường minh qua API go-to-zone.
        if claims_mismatch {
            Logger::authz_log(
                &claims.sub,
                method,
                path,
                "DENIED",
                &format!(
                    "Zone mismatch: JWT active zone_id={:?}, Request resolved_zone_id={:?}. Active zone transition is forbidden for automated requests.",
                    claims.zone_id, zone_id
                ),
            );
            return Err(Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Zone unavailable"),
            ))));
        } else if cookie_mismatch {
            // [COMMENT]: Nếu JWT đã khớp zone_id nhưng thiếu/sai cookie zone_code, set lại cookie cho đồng bộ với JWT
            cookies_to_set_zone.push(format!(
                "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000",
                zone_code
            ));
        }
    }

    Ok(cookies_to_set_zone)
}
