// ======================================================================================================
// 📂 sre/zone_catalog.rs — SRE Admin Zone Catalog Handler (GET /admin/hierarchy/zones/catalog)
// ======================================================================================================

use crate::infra::redis::{RedisRuntimeClient, SessionManager};
use crate::infra::shared_redis::SharedRedisBus;
use crate::infra::zone::get_all_zones;
use crate::observability::logger::Logger;
use crate::sre::claims::SreTokenManager;
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

/// [COMMENT]: Intercept GET /admin/hierarchy/zones/catalog dành cho SRE Admin.
pub async fn handle_admin_zone_catalog(
    _session_mgr: &Arc<SessionManager>,
    _token_mgr: &Arc<SreTokenManager>,
    shared_redis: &Arc<SharedRedisBus>,
    redis_client: &RedisRuntimeClient,
    _client_headers: &HashMap<String, String>,
    method: &str,
    path: &str,
) -> Option<Result<Response<CheckResponse>, Status>> {
    if !(method == "GET" && path.starts_with("/admin/hierarchy/zones/catalog")) {
        return None;
    }

    let all_zones = get_all_zones(shared_redis, redis_client).await;
    let mut catalog = Vec::new();

    for z in all_zones {
        catalog.push(ZoneCatalogEntry {
            code: z.code,
            name: z.name,
        });
    }
    catalog.push(ZoneCatalogEntry {
        code: "global".to_string(),
        name: "Global Zone".to_string(),
    });

    let json_body = match serde_json::to_string(&catalog) {
        Ok(body) => body,
        Err(e) => {
            Logger::sys_error(
                "sre.zone.catalog",
                "Failed to serialize admin zone catalog",
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
        "Admin zone catalog local intercept",
    ));
    response.set_http_response(denied_builder);

    Some(Ok(Response::new(response)))
}
