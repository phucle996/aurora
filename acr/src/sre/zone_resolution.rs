// ======================================================================================================
// 📂 sre/zone_resolution.rs — Admin/SRE Zone Context Resolution & Verification
// ======================================================================================================

use crate::infra::nats::Nats;
use crate::observability::logger::Logger;
use crate::sre::claims::SreClaims;
use crate::user::zone_resolution::{resolve_zone_context, ZoneResolutionError};
use envoy_types::ext_authz::v3::CheckResponseExt;
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use std::collections::HashMap;
use tonic::{Response, Status};

/// [COMMENT]: Phân giải và xác thực Zone dành riêng cho Admin (SRE).
pub async fn resolve_and_verify_zone_admin(
    nats: &Nats,
    redis_client: &redis::Client,
    claims: Option<&mut SreClaims>,
    cookie_header: &str,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Result<Vec<String>, Result<Response<CheckResponse>, Status>> {
    use crate::gateway::ext_authz::extract_cookie_value;
    use crate::pkg::cookie::COOKIE_ZONE_CODE;

    let mut cookies_to_set_zone = Vec::new();

    let zone_res = resolve_zone_context(nats, redis_client, cookie_header, client_headers).await;

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
            let sub = claims.as_ref().map(|c| c.sub.as_str()).unwrap_or("admin");
            Logger::authz_log(
                sub,
                method,
                path,
                "DENIED",
                "Missing zone_code context (no cookie/header)",
            );
            return Err(Ok(Response::new(CheckResponse::with_status(
                Status::permission_denied("Zone unavailable"),
            ))));
        }
    };

    if let Some(ref c) = claims {
        if let (Some(zone_id), Some(zone_code)) = (resolved_zone_id, resolved_zone_code) {
            let claims_mismatch = c.zone_id.as_ref() != Some(&zone_id);
            let cookie_mismatch =
                extract_cookie_value(cookie_header, COOKIE_ZONE_CODE).as_ref() != Some(&zone_code);

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
                cookies_to_set_zone.push(format!(
                    "zone_code={}; Path=/admin; Secure; SameSite=Lax; Max-Age=31536000",
                    zone_code
                ));
            }
        }
    } else {
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
