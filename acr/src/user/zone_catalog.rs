// ======================================================================================================
// 📂 user/zone_catalog.rs — User Zone Catalog Handler (GET /api/v1/zones/catalog)
// ======================================================================================================

use crate::infra::nats::Nats;
use crate::infra::zone::get_all_zones;
use crate::observability::logger::Logger;
use envoy_types::ext_authz::v3::pb::HttpStatusCode;
use envoy_types::ext_authz::v3::{CheckResponseExt, DeniedHttpResponseBuilder};
use envoy_types::pb::envoy::service::auth::v3::CheckResponse;
use serde::Serialize;
use std::collections::HashMap;
use tonic::{Response, Status};

#[derive(Serialize)]
pub struct ZoneCatalogEntry {
    pub code: String,
    pub name: String,
}

/// [COMMENT]: Intercept GET /api/v1/zones/catalog dành cho User domain.
pub async fn handle_user_zone_catalog(
    nats: &Nats,
    redis_client: &redis::Client,
    _client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    if !(method == "GET" && path.starts_with("/api/v1/zones/catalog")) {
        return None;
    }

    let all_zones = get_all_zones(nats, redis_client).await;
    let mut catalog = Vec::new();

    for z in all_zones {
        if z.status == "active" || z.status == "draining" {
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
