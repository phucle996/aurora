use std::collections::HashMap;
use std::sync::Arc;

use envoy_types::ext_authz::v3::{CheckRequestExt, CheckResponseExt};
use envoy_types::pb::envoy::service::auth::v3::{
    authorization_server::Authorization, CheckRequest, CheckResponse,
};
use tokio::sync::Semaphore;
use tonic::{Request, Response, Status};

use crate::control_assertion::{AssertionVerifier, ControlAssertion};
use crate::error::AuthzError;
use crate::request_binding::{
    path_targets_storage_resource, storage_action, storage_body_is_allowed,
};
use crate::telemetry::Telemetry;
use crate::transfer_ticket::storage_object_grant;
use crate::zone_access::{AccessRecord, AccessStore};

const MAX_CONTROL_BODY_BYTES: usize = 64 * 1024;

pub struct ZoneControlAuthorizer {
    verifier: AssertionVerifier,
    access: AccessStore,
    telemetry: Arc<Telemetry>,
    inflight: Arc<Semaphore>,
}

impl ZoneControlAuthorizer {
    pub fn new(
        verifier: AssertionVerifier,
        access: AccessStore,
        telemetry: Arc<Telemetry>,
        max_inflight_checks: usize,
    ) -> Self {
        Self {
            verifier,
            access,
            telemetry,
            inflight: Arc::new(Semaphore::new(max_inflight_checks)),
        }
    }

    async fn authorize(
        &self,
        request: &CheckRequest,
    ) -> Result<(ControlAssertion, Option<String>), AuthzError> {
        let headers = request
            .get_client_headers()
            .ok_or(AuthzError::Denied("HTTP_CONTEXT_MISSING"))?;
        let http = request
            .attributes
            .as_ref()
            .and_then(|attributes| attributes.request.as_ref())
            .and_then(|request| request.http.as_ref())
            .ok_or(AuthzError::Denied("HTTP_CONTEXT_MISSING"))?;
        let method = http.method.as_str();
        let path = http.path.as_str();
        let body = if http.body.is_empty() {
            http.raw_body.as_slice()
        } else {
            http.body.as_bytes()
        };
        if body.len() > MAX_CONTROL_BODY_BYTES {
            return Err(AuthzError::Denied("CONTROL_BODY_TOO_LARGE"));
        }
        let assertion = required(headers, "x-aurora-control-assertion")?;
        let signature = required(headers, "x-aurora-control-signature")?;
        let key_id = required(headers, "x-aurora-control-key-id")?;
        let verified = self
            .verifier
            .verify(assertion, signature, key_id, method, path, body)?;
        if verified.capability == "zone.transfer.ticket" {
            let record = self
                .access
                .get(&verified.access_session_id)
                .await?
                .ok_or(AuthzError::NotReady("ZONE_ACCESS_RECORD_MISSING"))?;
            let grant = storage_object_grant(&verified, &record, method, path, body)?;
            return Ok((verified, Some(grant)));
        }
        if !storage_body_is_allowed(&verified.action, body) {
            return Err(AuthzError::Denied("CONTROL_BODY_SEMANTICS_FORBIDDEN"));
        }
        let record = self
            .access
            .get(&verified.access_session_id)
            .await?
            .ok_or(AuthzError::NotReady("ZONE_ACCESS_RECORD_MISSING"))?;
        match_storage_record(&verified, &record, method, path)?;
        Ok((verified, None))
    }
}

#[tonic::async_trait]
impl Authorization for ZoneControlAuthorizer {
    async fn check(
        &self,
        request: Request<CheckRequest>,
    ) -> Result<Response<CheckResponse>, Status> {
        // Backpressure is enforced before signature or KV work so overload
        // cannot turn into an unbounded task or memory queue.
        let Ok(_permit) = self.inflight.clone().try_acquire_owned() else {
            self.telemetry.overloaded();
            return Err(Status::resource_exhausted(
                "Zone Control Authorizer is overloaded",
            ));
        };
        let _observation = self.telemetry.observe_check();
        match self.authorize(request.get_ref()).await {
            Ok((assertion, transfer_grant)) => {
                self.telemetry.allowed();
                let mut response = CheckResponse::with_status(Status::ok("authorized"));
                response.set_http_response(
                    envoy_types::pb::envoy::service::auth::v3::OkHttpResponse::default(),
                );
                if let Some(envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse::OkResponse(ok)) = response.http_response.as_mut() {
                    use envoy_types::pb::envoy::config::core::v3::{HeaderValue, HeaderValueOption};
                    for (key, value) in [
                        ("x-aurora-actor-id", assertion.actor_id),
                        ("x-aurora-resource-id", assertion.resource_id),
                        ("x-aurora-control-capability", assertion.capability),
                        ("x-aurora-control-action", assertion.action),
                        ("x-aurora-operation-id", assertion.operation_id),
                        ("x-aurora-bucket-name", assertion.resource_name),
                    ] {
                        let option = HeaderValueOption {
                            header: Some(HeaderValue {
                                key: key.to_string(),
                                value,
                                // ExtAuthz receives the textual representation; the binary form is empty.
                                ..Default::default()
                            }),
                            append_action: 2,
                            ..Default::default()
                        };
                        ok.headers.push(option);
                    }
                    if let Some(grant) = transfer_grant {
                        ok.headers.push(HeaderValueOption {
                            header: Some(HeaderValue { key: "x-aurora-transfer-grant".to_string(), value: grant, ..Default::default() }),
                            append_action: 2,
                            ..Default::default()
                        });
                    }
                    ok.headers_to_remove.extend([
                        "x-aurora-access-session-id".to_string(),
                        "x-aurora-control-assertion".to_string(),
                        "x-aurora-control-signature".to_string(),
                        "x-aurora-control-key-id".to_string(),
                    ]);
                }
                Ok(Response::new(response))
            }
            Err(AuthzError::Dependency(code)) => {
                self.telemetry.dependency_failure();
                tracing::error!(event_code = "ZONE_CONTROL_AUTHZ_DEPENDENCY_FAILURE", error = %code);
                Err(Status::unavailable(
                    "Zone control authorization dependency unavailable",
                ))
            }
            Err(AuthzError::NotReady(code)) => {
                self.telemetry.not_ready();
                tracing::info!(event_code = "ZONE_CONTROL_AUTHZ_NOT_READY", reason = code);
                Err(Status::unavailable("Zone access projection is not ready"))
            }
            Err(error) => {
                self.telemetry.denied();
                tracing::warn!(event_code = "ZONE_CONTROL_AUTHZ_DENIED", reason = %error);
                Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("Zone control request denied"),
                )))
            }
        }
    }
}

fn required<'a>(
    headers: &'a HashMap<String, String>,
    name: &'static str,
) -> Result<&'a str, AuthzError> {
    headers
        .get(name)
        .map(String::as_str)
        .filter(|value| !value.is_empty())
        .ok_or(AuthzError::Denied(name))
}

fn match_storage_record(
    assertion: &ControlAssertion,
    record: &AccessRecord,
    method: &str,
    path: &str,
) -> Result<(), AuthzError> {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_err(|_| AuthzError::Dependency("system clock invalid".into()))?
        .as_secs();
    if record.access_session_id != assertion.access_session_id
        || record.binding_hash != assertion.binding_hash
        || record.actor_id != assertion.actor_id
        || record.resource_id != assertion.resource_id
        || record.bucket_name != assertion.resource_name
        || record.workspace_id != assertion.workspace_id
        || record.zone_id != assertion.zone_id
        || record.policy_revision != assertion.policy_revision
        || record.expires_at_unix_seconds <= now
        || record.key_prefix != assertion.scope
        || !record
            .actions
            .iter()
            .any(|action| action == &assertion.action)
        || storage_action(method, path) != Some(assertion.action.as_str())
        || !path_targets_storage_resource(path, &record.bucket_name, &record.key_prefix)
    {
        return Err(AuthzError::Denied("ZONE_ACCESS_RECORD_MISMATCH"));
    }
    Ok(())
}
