// ======================================================================================================
// 📂 user/zone_resolution.rs — User Zone Context Resolution & Verification
// ======================================================================================================

use crate::infra::nats::Nats;
use crate::infra::zone::{resolve_code_to_id_and_status, resolve_id_to_code_and_status};
use crate::observability::logger::Logger;
use crate::user::claims::Claims;
use envoy_types::ext_authz::v3::CheckResponseExt;
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use std::collections::HashMap;

use tonic::{Response, Status};

#[derive(Debug)]
pub enum ZoneResolutionError {
    Missing,
    InvalidCode(String),
}

/// [COMMENT]: Trích xuất và phân giải zone context từ cookies hoặc headers dành cho User domain
pub async fn resolve_zone_context(
    nats: &Nats,
    redis_client: &redis::Client,
    cookie_header: &str,
    client_headers: &HashMap<String, String>,
) -> Result<(String, String, String), ZoneResolutionError> {
    use crate::gateway::ext_authz::extract_cookie_value;
    use crate::pkg::cookie::COOKIE_ZONE_CODE;

    let mut requested_zone_code = extract_cookie_value(cookie_header, COOKIE_ZONE_CODE);
    if requested_zone_code.is_none() {
        requested_zone_code = client_headers
            .get("x-zone-code")
            .cloned()
            .or_else(|| client_headers.get("X-Zone-Code").cloned());
    }

    if let Some(ref code) = requested_zone_code {
        if code == "global" {
            Ok((
                "global".to_string(),
                "global".to_string(),
                "active".to_string(),
            ))
        } else if let Some((id, status)) = resolve_code_to_id_and_status(nats, redis_client, code).await {
            Ok((id, code.clone(), status))
        } else {
            Err(ZoneResolutionError::InvalidCode(code.clone()))
        }
    } else {
        Err(ZoneResolutionError::Missing)
    }
}

/// [COMMENT]: Phân giải và xác thực Zone dành riêng cho User thường.
pub async fn resolve_and_verify_zone_user(
    nats: &Nats,
    redis_client: &redis::Client,
    claims: Option<&mut Claims>,
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
            if let Some(ref c) = claims {
                if let Some(ref claims_zone_id) = c.zone_id {
                    if let Some((code, status)) =
                        resolve_id_to_code_and_status(nats, redis_client, claims_zone_id).await
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

    if let Some(ref c) = claims {
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
                extract_cookie_value(cookie_header, COOKIE_ZONE_CODE).as_ref() != Some(zone_code);

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
                cookies_to_set_zone.push(format!(
                    "zone_code={}; Path=/; Secure; SameSite=Lax; Max-Age=31536000",
                    zone_code
                ));
            }
        }
    } else {
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
