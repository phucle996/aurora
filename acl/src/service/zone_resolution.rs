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
    token_mgr: &Arc<TokenManager>,
    config: &Config,
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
            return Err(Ok(Response::new(CheckResponse::with_status(
                Status::invalid_argument("Requested zone code not found"),
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
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Forbidden access to global zone"),
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
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Forbidden access to inactive zone"),
                ))));
            }
        }
    }

    // [COMMENT]: 4. Thực hiện cơ chế sliding/chuyển đổi Active Zone tự động (Zone Transitioning)
    if let (Some(zone_id), Some(zone_code)) = (resolved_zone_id, resolved_zone_code) {
        let claims_mismatch = claims.zone_id.as_ref() != Some(&zone_id);
        let cookie_mismatch =
            extract_cookie_value(cookie_header, "zone_code").as_ref() != Some(&zone_code);

        if claims_mismatch {
            // [COMMENT]: Khi phát hiện có sự thay đổi Zone, ký lại JWT mới chứa zone_id cập nhật
            let mut updated_claims = claims.clone();
            updated_claims.zone_id = Some(zone_id.clone());
            match token_mgr.generate_token(&updated_claims).await {
                Ok(new_jwt) => {
                    Logger::sys_info(
                        "ext_authz.zone",
                        &format!(
                            "Switching active zone of user_id={} to zone_id={}, zone_code={}",
                            claims.sub, zone_id, zone_code
                        ),
                    );
                    // [COMMENT]: Cấp cookie JWT chứa claims zone mới để trình duyệt ghi đè lại
                    cookies_to_set_zone.push(format!(
                        "access_token={}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age={}",
                        new_jwt, config.session_ttl_secs
                    ));
                    // [COMMENT]: Lưu song song zone_code để client frontend đọc trực tiếp dễ dàng
                    cookies_to_set_zone.push(format!(
                        "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000",
                        zone_code
                    ));
                    // [COMMENT]: Cập nhật claims trong bộ nhớ hiện tại để downstream context nhận đúng zone_id mới
                    claims.zone_id = Some(zone_id);
                }
                Err(e) => {
                    Logger::sys_error(
                        "ext_authz.zone",
                        "Failed to generate access token with updated zone_id",
                        &e.to_string(),
                    );
                }
            }
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
