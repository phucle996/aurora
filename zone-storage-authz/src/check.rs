use std::collections::HashMap;
use std::sync::Arc;

use envoy_types::ext_authz::v3::{CheckRequestExt, CheckResponseExt};
use envoy_types::pb::envoy::service::auth::v3::{
    authorization_server::Authorization, CheckRequest, CheckResponse,
};
use tonic::{Request, Response, Status};

use crate::access_store::{AccessRecord, AccessStore};
use crate::assertion::{AssertionVerifier, StorageAssertion};
use crate::canonical::path_targets_bucket;
use crate::error::AuthzError;
use crate::metrics::Metrics;

pub struct StorageAuthzService {
    verifier: AssertionVerifier,
    access: AccessStore,
    metrics: Arc<Metrics>,
}

impl StorageAuthzService {
    pub fn new(verifier: AssertionVerifier, access: AccessStore) -> Self {
        Self {
            verifier,
            access,
            metrics: Arc::new(Metrics::default()),
        }
    }

    async fn authorize(&self, request: &CheckRequest) -> Result<StorageAssertion, AuthzError> {
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
        let assertion = required(headers, "x-aurora-storage-assertion")?;
        let signature = required(headers, "x-aurora-storage-signature")?;
        let key_id = required(headers, "x-aurora-storage-key-id")?;
        let verified = self
            .verifier
            .verify(assertion, signature, key_id, method, path, body)?;
        let record = self
            .access
            .get(&verified.access_session_id)
            .await?
            .ok_or(AuthzError::Denied("ZONE_ACCESS_RECORD_MISSING"))?;
        match_record(&verified, &record, path)?;
        Ok(verified)
    }
}

#[tonic::async_trait]
impl Authorization for StorageAuthzService {
    async fn check(
        &self,
        request: Request<CheckRequest>,
    ) -> Result<Response<CheckResponse>, Status> {
        match self.authorize(request.get_ref()).await {
            Ok(assertion) => {
                self.metrics.allowed();
                let mut response = CheckResponse::with_status(Status::ok("authorized"));
                response.set_http_response(
                    envoy_types::pb::envoy::service::auth::v3::OkHttpResponse::default(),
                );
                if let Some(envoy_types::pb::envoy::service::auth::v3::check_response::HttpResponse::OkResponse(ok)) = response.http_response.as_mut() {
                    use envoy_types::pb::envoy::config::core::v3::{HeaderValue, HeaderValueOption};
                    for (key, value) in [
                        ("x-aurora-actor-id", assertion.actor_id),
                        ("x-aurora-resource-id", assertion.resource_id),
                        ("x-aurora-storage-action", assertion.action),
                        ("x-aurora-bucket-name", assertion.bucket_name),
                    ] {
                        let mut option = HeaderValueOption::default();
                        option.header = Some(HeaderValue { key: key.to_string(), value });
                        option.append_action = 2;
                        ok.headers.push(option);
                    }
                    ok.headers_to_remove.extend([
                        "x-aurora-storage-assertion".to_string(),
                        "x-aurora-storage-signature".to_string(),
                        "x-aurora-storage-key-id".to_string(),
                    ]);
                }
                Ok(Response::new(response))
            }
            Err(AuthzError::Dependency(code)) => {
                self.metrics.dependency_failure();
                tracing::error!(event_code = "STORAGE_AUTHZ_DEPENDENCY_FAILURE", error = %code);
                Ok(Response::new(CheckResponse::with_status(
                    Status::unavailable("storage authorization dependency unavailable"),
                )))
            }
            Err(error) => {
                self.metrics.denied();
                tracing::warn!(event_code = "STORAGE_AUTHZ_DENIED", reason = %error);
                Ok(Response::new(CheckResponse::with_status(
                    Status::permission_denied("storage request denied"),
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

fn match_record(
    assertion: &StorageAssertion,
    record: &AccessRecord,
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
        || record.bucket_name != assertion.bucket_name
        || record.workspace_id != assertion.workspace_id
        || record.zone_id != assertion.zone_id
        || record.policy_revision != assertion.policy_revision
        || record.expires_at_unix_seconds <= now
        || record.key_prefix != assertion.key_prefix
        || !record
            .actions
            .iter()
            .any(|action| action == &assertion.action)
        || !path_targets_bucket(path, &record.bucket_name, &record.key_prefix)
    {
        return Err(AuthzError::Denied("ZONE_ACCESS_RECORD_MISMATCH"));
    }
    Ok(())
}
