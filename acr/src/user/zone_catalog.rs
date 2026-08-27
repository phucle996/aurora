// ======================================================================================================
// 📂 user/zone_catalog.rs — User Zone Catalog Handler (GET /api/v1/zones/catalog)
// ======================================================================================================

use crate::infra::redis::RedisRuntimeClient;
use crate::infra::shared_redis::SharedRedisBus;
use crate::infra::zone::get_all_zones;
use crate::observability::logger::Logger;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use serde::Serialize;
use std::collections::HashMap;
use std::sync::Arc;
use tonic::{Response, Status};

#[derive(Serialize)]
pub struct ZoneCatalogEntry {
    pub code: String,
    pub name: String,
}

fn is_user_visible_zone_status(status: &str) -> bool {
    matches!(status, "active" | "draining")
}

/// [COMMENT]: Intercept GET /api/v1/zones/catalog dành cho User domain.
pub async fn handle_user_zone_catalog(
    shared_redis: &Arc<SharedRedisBus>,
    redis_client: &RedisRuntimeClient,
    _client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    if !(method == "GET" && path.starts_with("/api/v1/zones/catalog")) {
        return None;
    }

    let all_zones = get_all_zones(shared_redis, redis_client).await;
    let mut catalog = Vec::new();

    for z in all_zones {
        if is_user_visible_zone_status(&z.status) {
            catalog.push(ZoneCatalogEntry {
                code: z.code,
                name: z.name,
            });
        }
    }

    let json_body = match serde_json::to_string(&catalog) {
        Ok(body) => body,
        Err(e) => {
            Logger::sys_error(
                "user.zone.catalog",
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

    let xssi_json_body = format!(")]}}',\n{}", json_body);

    let mut denied_builder = DeniedHttpResponseBuilder::new();
    denied_builder.set_http_status(HttpStatusCode::Ok);
    denied_builder.add_header("content-type", "application/json", None, false);
    denied_builder.set_body(xssi_json_body);

    let mut response = CheckResponse::new();
    response.set_status(tonic::Status::unauthenticated(
        "User zone catalog local intercept",
    ));
    response.set_http_response(denied_builder);

    Some(Ok(Response::new(response)))
}

#[cfg(test)]
#[path = "../../tests/unit/user/zone_catalog.rs"]
mod tests;
