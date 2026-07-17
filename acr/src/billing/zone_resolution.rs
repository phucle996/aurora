// ======================================================================================================
// 📂 billing/zone_resolution.rs — Billing Auditor Zone Context Resolution & Verification
// ======================================================================================================

use crate::infra::nats::Nats;
use crate::infra::zone::resolve_code_to_id_and_status;
use crate::observability::logger::Logger;
use envoy_types::ext_authz::v3::CheckResponseExt;
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use std::collections::HashMap;
use tonic::{Response, Status};

/// [COMMENT]: Phân giải và xác thực Zone dành riêng cho Billing Auditor.
pub async fn resolve_and_verify_zone_billing(
    nats: &Nats,
    redis_client: &redis::Client,
    zone_id_claim: Option<&str>,
    cookie_header: &str,
    client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Result<(), Result<Response<CheckResponse>, Status>> {
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
        if code != "global" {
            if let Some((id, status)) =
                resolve_code_to_id_and_status(nats, redis_client, code).await
            {
                if status != "active" && status != "draining" {
                    Logger::authz_log(
                        "billing_auditor",
                        method,
                        path,
                        "DENIED",
                        &format!("Billing requested inactive zone: {}", code),
                    );
                    return Err(Ok(Response::new(CheckResponse::with_status(
                        Status::permission_denied("Zone unavailable"),
                    ))));
                }

                if let Some(claim_id) = zone_id_claim {
                    if claim_id != "global" && claim_id != id {
                        Logger::authz_log(
                            "billing_auditor",
                            method,
                            path,
                            "DENIED",
                            &format!("Billing zone mismatch: JWT={}, Req={}", claim_id, id),
                        );
                        return Err(Ok(Response::new(CheckResponse::with_status(
                            Status::permission_denied("Zone unavailable"),
                        ))));
                    }
                }
            } else {
                Logger::authz_log(
                    "billing_auditor",
                    method,
                    path,
                    "DENIED",
                    &format!("Billing requested zone code not found: {}", code),
                );
                return Err(Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone unavailable"),
                ))));
            }
        }
    }

    Ok(())
}
