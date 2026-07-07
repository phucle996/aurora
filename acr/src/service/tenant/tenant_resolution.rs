// ======================================================================================================
// 📂 MODULE: acr/src/service/tenant/tenant_resolution.rs
//            DỊCH VỤ PHÂN GIẢI & XÁC THỰC RÀNG BUỘC BẢO MẬT CỦA TENANT TẠI BIÊN (EDGE)
// ======================================================================================================
// [COMMENT]: Thực hiện lấy cookie tenant_id trên client so sánh với tenant_id trong JWT payload,
// từ chối request nếu không khớp hoặc định dạng UUID không hợp lệ.

use envoy_types::ext_authz::v3::CheckResponseExt;
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use tonic::{Response, Status};

use crate::core::token::Claims;
use crate::observability::logger::Logger;
use crate::service::ext_authz::extract_cookie_value;

/// [COMMENT]: Xác thực Tenant đối chiếu giữa cookie/header gửi lên và Claims trong JWT
pub async fn resolve_and_verify_tenant(
    claims: Option<&mut Claims>,
    cookie_header: &str,
    client_headers: &std::collections::HashMap<String, String>,
    method: &str,
    path: &str,
) -> Result<(), Result<Response<CheckResponse>, Status>> {
    // 1. Lấy cookie/header tenant_id từ Client
    let cookie_tenant_id = extract_cookie_value(cookie_header, "tenant_id")
        .or_else(|| client_headers.get("x-tenant-id").cloned())
        .or_else(|| client_headers.get("X-Tenant-ID").cloned());

    // 2. Xác thực so khớp với JWT claims
    if let Some(ref c) = claims {
        // [COMMENT]: Sử dụng mặc định "platform" thay vì "global" để phân biệt rõ ràng với phạm vi "global" của Zone
        let claims_tenant_id = c.tenant_id.as_deref().unwrap_or("platform");
        let req_tenant_id = cookie_tenant_id.as_deref().unwrap_or("platform");

        if req_tenant_id != claims_tenant_id {
            Logger::authz_log(
                &c.sub,
                method,
                path,
                "DENIED",
                &format!(
                    "Tenant mismatch: client cookie='{}', jwt claims='{}'",
                    req_tenant_id, claims_tenant_id
                ),
            );
            return Err(Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Tenant unavailable"),
            ))));
        }

        // 3. Nếu có giá trị tenant_id, xác thực định dạng UUID hợp lệ
        // [COMMENT]: Loại trừ "platform" thay vì "global" khỏi kiểm thử định dạng UUID
        if !req_tenant_id.is_empty() && req_tenant_id != "platform" {
            if uuid::Uuid::parse_str(req_tenant_id).is_err() {
                Logger::authz_log(
                    &c.sub,
                    method,
                    path,
                    "DENIED",
                    &format!("Invalid UUID format for requested tenant: {}", req_tenant_id),
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Tenant unavailable"),
                ))));
            }
        }
    }

    Ok(())
}
