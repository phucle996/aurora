// ======================================================================================================
// 📂 MODULE: acr/src/service/tenant/tenant_resolution.rs
//            DỊCH VỤ PHÂN GIẢI & XÁC THỰC RÀNG BUỘC BẢO MẬT CỦA TENANT TẠI BIÊN (EDGE)
// ======================================================================================================
// [COMMENT]: Chịu trách nhiệm trích xuất ngữ cảnh tenant (qua domain), giải phân ra tenant_id,
// so khớp với context claims và kiểm tra tính hợp lệ của Membership (user ∈ tenant).

use envoy_types::ext_authz::v3::CheckResponseExt;
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use std::sync::Arc;
use tonic::{Response, Status};

use crate::core::token::Claims;
use crate::observability::logger::Logger;
use crate::service::ext_authz::extract_cookie_value;
use crate::service::tenant::manager::TenantManager;

#[derive(Debug)]
pub enum TenantResolutionError {
    Missing,
    InvalidDomain(String),
}

/// [COMMENT]: Trích xuất ngữ cảnh tenant_domain từ cookies hoặc headers
pub async fn resolve_tenant_context(
    tenant_mgr: &TenantManager,
    cookie_header: &str,
    client_headers: &std::collections::HashMap<String, String>,
) -> Result<(String, String), TenantResolutionError> {
    // [COMMENT]: Thử trích xuất từ cookie tenant_domain
    let mut requested_domain = extract_cookie_value(cookie_header, "tenant_domain");
    if requested_domain.is_none() {
        // [COMMENT]: Thử lấy từ header x-tenant-domain
        requested_domain = client_headers
            .get("x-tenant-domain")
            .map(|s| s.clone())
            .or_else(|| client_headers.get("X-Tenant-Domain").map(|s| s.clone()));
    }

    if let Some(ref domain) = requested_domain {
        if domain == "global" {
            Ok(("global".to_string(), "global".to_string()))
        } else if let Some(id) = tenant_mgr.resolve_tenant_id(domain).await {
            Ok((id, domain.clone()))
        } else {
            Err(TenantResolutionError::InvalidDomain(domain.clone()))
        }
    } else {
        Err(TenantResolutionError::Missing)
    }
}

/// [COMMENT]: Phân giải và xác thực Tenant dành cho user thường.
/// Phải đảm bảo user_id nằm trong tenant_id.
pub async fn resolve_and_verify_tenant_user(
    tenant_mgr: &Arc<TenantManager>,
    claims: Option<&mut Claims>,
    cookie_header: &str,
    client_headers: &std::collections::HashMap<String, String>,
    method: &str,
    path: &str,
) -> Result<Vec<String>, Result<Response<CheckResponse>, Status>> {
    let mut cookies_to_set = Vec::new();

    // 1. Phân giải tenant từ context
    let tenant_res = resolve_tenant_context(tenant_mgr, cookie_header, client_headers).await;

    let (resolved_tenant_id, resolved_domain) = match tenant_res {
        Ok(res) => (Some(res.0), Some(res.1)),
        Err(TenantResolutionError::InvalidDomain(dom)) => {
            let sub = claims.as_ref().map(|c| c.sub.as_str()).unwrap_or("unknown");
            Logger::authz_log(
                sub,
                method,
                path,
                "DENIED",
                &format!("User requested tenant domain not found: {}", dom),
            );
            return Err(Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Tenant unavailable"),
            ))));
        }
        Err(TenantResolutionError::Missing) => {
            // Fallback về tenant_id trong claims của user
            if let Some(ref c) = claims {
                if let Some(ref claims_tenant_id) = c.tenant_id {
                    // Đối với user, nếu không truyền tenant_domain cụ thể, ta coi như dùng claims_tenant_id
                    // Ta không cần resolve domain ngược vì auth context chính đã có claims_tenant_id
                    (Some(claims_tenant_id.clone()), None)
                } else {
                    (None, None)
                }
            } else {
                (None, None)
            }
        }
    };

    // 2. Xác thực
    if let Some(ref c) = claims {
        if let Some(tenant_id) = resolved_tenant_id {
            // Chặn user thường vào tenant global
            if tenant_id == "global" {
                Logger::authz_log(
                    &c.sub,
                    method,
                    path,
                    "DENIED",
                    "Forbidden global tenant access for non-admin user",
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Tenant unavailable"),
                ))));
            }

            // [COMMENT]: KIỂM TRA MEMBERSHIP (user ∈ tenant)
            let membership = tenant_mgr.check_membership(&tenant_id, &c.uid).await;
            if !membership.is_member {
                Logger::authz_log(
                    &c.sub,
                    method,
                    path,
                    "DENIED",
                    &format!("User not a member of tenant {}", tenant_id),
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Tenant access denied"),
                ))));
            }

            // Đảm bảo claims.tenant_id khớp với resolved_tenant_id
            let claims_mismatch = c.tenant_id.as_ref() != Some(&tenant_id);
            if claims_mismatch {
                Logger::authz_log(
                    &c.sub,
                    method,
                    path,
                    "DENIED",
                    &format!(
                        "User tenant mismatch: JWT={:?}, Req={}",
                        c.tenant_id, tenant_id
                    ),
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Tenant unavailable"),
                ))));
            }

            // Sync domain cookie nếu lệch
            if let Some(ref dom) = resolved_domain {
                let cookie_mismatch =
                    extract_cookie_value(cookie_header, "tenant_domain").as_ref() != Some(dom);
                if cookie_mismatch {
                    cookies_to_set.push(format!(
                        "tenant_domain={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000",
                        dom
                    ));
                }
            }
        }
    }

    Ok(cookies_to_set)
}

/// [COMMENT]: Phân giải và xác thực Tenant dành cho Admin.
pub async fn resolve_and_verify_tenant_admin(
    tenant_mgr: &Arc<TenantManager>,
    claims: Option<&mut Claims>,
    cookie_header: &str,
    client_headers: &std::collections::HashMap<String, String>,
    method: &str,
    path: &str,
) -> Result<Vec<String>, Result<Response<CheckResponse>, Status>> {
    let mut cookies_to_set = Vec::new();

    // 1. Phân giải tenant từ context
    let tenant_res = resolve_tenant_context(tenant_mgr, cookie_header, client_headers).await;

    let (resolved_tenant_id, resolved_domain) = match tenant_res {
        Ok(res) => (Some(res.0), Some(res.1)),
        Err(TenantResolutionError::InvalidDomain(dom)) => {
            Logger::authz_log(
                "admin",
                method,
                path,
                "DENIED",
                &format!("Admin requested tenant domain not found: {}", dom),
            );
            return Err(Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Tenant unavailable"),
            ))));
        }
        Err(TenantResolutionError::Missing) => {
            if let Some(ref c) = claims {
                if let Some(ref claims_tenant_id) = c.tenant_id {
                    (Some(claims_tenant_id.clone()), None)
                } else {
                    (None, None)
                }
            } else {
                (None, None)
            }
        }
    };

    // Admin có quyền truy cập rộng, nhưng vẫn kiểm tra khớp access token
    if let Some(ref c) = claims {
        if let Some(tenant_id) = resolved_tenant_id {
            let claims_mismatch = c.tenant_id.as_ref() != Some(&tenant_id);
            if claims_mismatch {
                Logger::authz_log(
                    &c.sub,
                    method,
                    path,
                    "DENIED",
                    &format!(
                        "Admin tenant mismatch: JWT={:?}, Req={}",
                        c.tenant_id, tenant_id
                    ),
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Tenant unavailable"),
                ))));
            }

            if let Some(ref dom) = resolved_domain {
                let cookie_mismatch =
                    extract_cookie_value(cookie_header, "tenant_domain").as_ref() != Some(dom);
                if cookie_mismatch {
                    cookies_to_set.push(format!(
                        "tenant_domain={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000",
                        dom
                    ));
                }
            }
        }
    }

    Ok(cookies_to_set)
}
