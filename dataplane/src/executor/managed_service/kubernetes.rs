use std::collections::BTreeMap;
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use reqwest::{Client, Method, StatusCode};
use serde::Deserialize;
use serde_json::{json, Map as JsonMap, Value as JsonValue};
use tokio::time::{sleep, Instant};
use uuid::Uuid;

use crate::config::Config;
use crate::executor::{ExecutionResult, ExecutorError};
use crate::job_runtime::model::ValidatedJob;

use super::admission::{admit_command, ManagedServiceOuterFence};
use super::apply::apply_graph;
use super::delete::delete_graph;
use super::entity::{
    KubernetesObservedObject, KubernetesResourceIdentity, ManagedServiceCommand,
    ManagedServiceFailure, ManagedServiceObservedState, ManagedServiceOperation,
};
use super::renderer::render_graph;
use super::result::terminal_result;

const FIELD_MANAGER: &str = "aurora-managed-service-dataplane";
const OWNER_ANNOTATION: &str = "platform.aurora.io/owner-id";
const WORKSPACE_ANNOTATION: &str = "platform.aurora.io/workspace-id";
const INSTANCE_ANNOTATION: &str = "platform.aurora.io/instance-id";
const COMPONENT_LABEL: &str = "platform.aurora.io/component";

#[derive(Clone, Debug)]
struct ResourceEndpoint {
    group: Option<String>,
    version: String,
    plural: String,
    namespaced: bool,
}

#[derive(Debug)]
pub(crate) enum KubernetesApiError {
    Retryable(&'static str),
    Terminal(&'static str),
    Conflict,
    NotFound,
}

impl KubernetesApiError {
    pub(crate) fn failure(&self) -> ManagedServiceFailure {
        match self {
            Self::Retryable(code) => {
                ManagedServiceFailure::retryable(code, "Kubernetes API is temporarily unavailable")
            }
            Self::Terminal(code) => ManagedServiceFailure::terminal(
                code,
                "Kubernetes rejected the managed service resource",
            ),
            Self::Conflict => ManagedServiceFailure::terminal(
                "K8S_NAMESPACE_CREATE_REJECTED",
                "Kubernetes namespace creation conflicted",
            ),
            Self::NotFound => ManagedServiceFailure::terminal(
                "K8S_RESOURCE_NOT_FOUND",
                "managed service resource was not found",
            ),
        }
    }
}

#[derive(Deserialize)]
struct DiscoveryResource {
    name: String,
    kind: String,
    namespaced: bool,
}

#[derive(Deserialize)]
struct ApiResourceList {
    resources: Vec<DiscoveryResource>,
}

/// Zone-local Kubernetes API client. The Dataplane receives only a mounted
/// service-account credential; it never asks Controlplane or Vault for a
/// customer secret. Discovery is cached per apiVersion/kind and the API URL is
/// validated once during bootstrap so a bad endpoint cannot accept work.
pub struct KubernetesRuntime {
    client: Client,
    api_url: String,
    token: String,
    discovery: Mutex<BTreeMap<(String, String), ResourceEndpoint>>,
    poll_interval: Duration,
    readiness_cap: Duration,
    max_job_attempts: u32,
}

impl KubernetesRuntime {
    pub async fn connect(config: &Config) -> Result<Arc<Self>, String> {
        let api_url = config.kubernetes_api_url.trim_end_matches('/').to_owned();
        let parsed = reqwest::Url::parse(&api_url)
            .map_err(|_| "KUBERNETES_API_URL must be a valid absolute URL".to_owned())?;
        if parsed.scheme() != "https" && parsed.scheme() != "http" {
            return Err("KUBERNETES_API_URL must use http or https".to_owned());
        }
        let token = read_non_empty_file(&config.kubernetes_token_path)?;
        let ca_path = Path::new(&config.kubernetes_ca_cert_path);
        if !ca_path.is_file() {
            return Err(format!(
                "KUBERNETES_CA_CERT_PATH does not point to a readable file: {}",
                ca_path.display()
            ));
        }
        let builder = Client::builder()
            .connect_timeout(Duration::from_secs(5))
            .timeout(Duration::from_secs(30));
        let ca_bytes = std::fs::read(ca_path)
            .map_err(|error| format!("read Kubernetes CA certificate failed: {error}"))?;
        let client = builder
            .add_root_certificate(
                reqwest::Certificate::from_pem(&ca_bytes)
                    .map_err(|error| format!("parse Kubernetes CA certificate failed: {error}"))?,
            )
            .build()
            .map_err(|error| format!("build Kubernetes API client failed: {error}"))?;
        let runtime = Arc::new(Self {
            client,
            api_url,
            token,
            discovery: Mutex::new(BTreeMap::new()),
            poll_interval: Duration::from_millis(config.kubernetes_poll_interval_ms),
            readiness_cap: Duration::from_secs(config.kubernetes_readiness_cap_seconds),
            max_job_attempts: config.kafka_max_job_attempts.max(1),
        });
        runtime
            .probe()
            .await
            .map_err(|error| format!("Kubernetes API bootstrap probe failed: {error:?}"))?;
        Ok(runtime)
    }

    pub async fn execute(&self, job: Arc<ValidatedJob>) -> Result<ExecutionResult, ExecutorError> {
        let zone_id = Uuid::parse_str(&job.target_zone_id).map_err(|_| {
            ExecutorError::ExecutionFailed("managed service target zone is not a UUID".to_owned())
        })?;
        let command = match admit_command(
            &job.payload,
            ManagedServiceOuterFence {
                job_id: Uuid::parse_str(&job.job_id).map_err(|_| {
                    ExecutorError::ExecutionFailed(
                        "managed service job id is not a UUID".to_owned(),
                    )
                })?,
                resource_id: &job.resource_id,
                zone_id,
                source_domain: &job.source_domain,
                job_topic: &job.job_topic,
                payload_schema_version: job.payload_schema_version,
            },
        ) {
            Ok(command) => command,
            Err(error) => {
                // There is no trusted inner identity on a malformed command;
                // quarantine it through the generic completion/DLQ boundary
                // instead of manufacturing a result that could not match an
                // authoritative operation row.
                return Err(ExecutorError::ExecutionFailed(format!(
                    "{}: {}",
                    error.code, error.message
                )));
            }
        };
        let graph = match render_graph(&command) {
            Ok(graph) => graph,
            Err(failure) => {
                return Err(self.domain_failure(&command, &job, failure));
            }
        };
        let operation_result: Result<ManagedServiceObservedState, ManagedServiceFailure> =
            match command.operation {
                ManagedServiceOperation::Delete => delete_graph(self, &command, &graph)
                    .await
                    .map(|observation| observation.state),
                ManagedServiceOperation::Create | ManagedServiceOperation::Resize => {
                    apply_graph(self, &command, &graph)
                        .await
                        .map(|observation| observation.state)
                }
            };
        match operation_result {
            Ok(observed) => Ok(ExecutionResult {
                message: "managed service desired state applied in the Zone".to_owned(),
                result_payload: terminal_result(
                    &command,
                    &job,
                    ManagedServiceFailure {
                        code: "",
                        message: "",
                        retryable: false,
                        observed_state: observed,
                    },
                ),
                result_payload_schema_version: 1,
            }),
            Err(failure) if failure.retryable => {
                // The generic retry scheduler owns transient delivery. Once
                // its bounded budget is exhausted, preserve the exact managed
                // service taxonomy and fences in a terminal inner result.
                if job.attempt.saturating_add(1) >= self.max_job_attempts {
                    Err(self.domain_failure(&command, &job, failure))
                } else {
                    Err(ExecutorError::Retryable(failure.message.to_owned()))
                }
            }
            Err(failure) => Err(self.domain_failure(&command, &job, failure)),
        }
    }

    fn domain_failure(
        &self,
        command: &ManagedServiceCommand,
        job: &ValidatedJob,
        failure: ManagedServiceFailure,
    ) -> ExecutorError {
        ExecutorError::DomainTerminal {
            error_code: failure.code.to_owned(),
            message: failure.message.to_owned(),
            result_payload: terminal_result(command, job, failure),
            result_payload_schema_version: 1,
        }
    }

    async fn probe(&self) -> Result<(), KubernetesApiError> {
        let response = self.request(Method::GET, "/version", None).await?;
        if !response.status().is_success() {
            return Err(map_status(response.status()));
        }
        Ok(())
    }

    pub(crate) async fn ensure_namespace(
        &self,
        namespace: &str,
        command: &ManagedServiceCommand,
    ) -> Result<(), ManagedServiceFailure> {
        let path = "/api/v1/namespaces";
        let get_path = format!("{path}/{namespace}");
        match self.get_json(&get_path).await {
            Ok(Some(existing)) => {
                if namespace_ownership(&existing, command).is_err() {
                    return Err(ManagedServiceFailure::terminal(
                        "K8S_OWNERSHIP_CONFLICT",
                        "workspace namespace belongs to another owner",
                    ));
                }
                Ok(())
            }
            Ok(None) => {
                let body = json!({
                    "apiVersion": "v1",
                    "kind": "Namespace",
                    "metadata": {
                        "name": namespace,
                        "annotations": {
                            OWNER_ANNOTATION: command.owner_id.to_string(),
                            WORKSPACE_ANNOTATION: command.workspace_id.to_string(),
                        }
                    }
                });
                match self
                    .request_json(Method::POST, path, &body, "application/json")
                    .await
                {
                    Ok(_) => Ok(()),
                    Err(KubernetesApiError::Conflict) => {
                        // Two instances in one workspace can create the
                        // deterministic namespace concurrently. A 409 is
                        // safe only after re-reading and checking ownership;
                        // never treat an arbitrary existing namespace as ours.
                        match self.get_json(&get_path).await {
                            Ok(Some(existing)) => {
                                namespace_ownership(&existing, command).map(|_| ())
                            }
                            Ok(None) => Err(ManagedServiceFailure::terminal(
                                "K8S_NAMESPACE_CREATE_REJECTED",
                                "Kubernetes rejected workspace namespace creation",
                            )),
                            Err(KubernetesApiError::Retryable(_)) => {
                                Err(ManagedServiceFailure::retryable(
                                    "K8S_API_UNAVAILABLE",
                                    "Kubernetes API is temporarily unavailable",
                                ))
                            }
                            Err(_) => Err(ManagedServiceFailure::terminal(
                                "K8S_NAMESPACE_CREATE_REJECTED",
                                "Kubernetes namespace ownership could not be confirmed",
                            )),
                        }
                    }
                    Err(KubernetesApiError::Terminal(_)) => Err(ManagedServiceFailure::terminal(
                        "K8S_NAMESPACE_CREATE_REJECTED",
                        "Kubernetes rejected workspace namespace creation",
                    )),
                    Err(KubernetesApiError::Retryable(_)) => Err(ManagedServiceFailure::retryable(
                        "K8S_API_UNAVAILABLE",
                        "Kubernetes API is temporarily unavailable",
                    )),
                    Err(KubernetesApiError::NotFound) => Err(ManagedServiceFailure::terminal(
                        "K8S_NAMESPACE_CREATE_REJECTED",
                        "Kubernetes namespace endpoint was not found",
                    )),
                }
            }
            Err(KubernetesApiError::Retryable(_)) => Err(ManagedServiceFailure::retryable(
                "K8S_API_UNAVAILABLE",
                "Kubernetes API is temporarily unavailable",
            )),
            Err(KubernetesApiError::Terminal(_)) => Err(ManagedServiceFailure::terminal(
                "K8S_NAMESPACE_READ_REJECTED",
                "Kubernetes rejected workspace namespace lookup",
            )),
            Err(KubernetesApiError::Conflict) => Err(ManagedServiceFailure::terminal(
                "K8S_NAMESPACE_READ_REJECTED",
                "Kubernetes namespace lookup conflicted",
            )),
            Err(KubernetesApiError::NotFound) => Err(ManagedServiceFailure::terminal(
                "K8S_NAMESPACE_READ_REJECTED",
                "Kubernetes namespace endpoint was not found",
            )),
        }
    }

    async fn endpoint(
        &self,
        api_version: &str,
        kind: &str,
    ) -> Result<ResourceEndpoint, ManagedServiceFailure> {
        let key = (api_version.to_owned(), kind.to_owned());
        if let Ok(discovery) = self.discovery.lock() {
            if let Some(endpoint) = discovery.get(&key) {
                return Ok(endpoint.clone());
            }
        }
        let (group, version, path) = match api_version.split_once('/') {
            Some((group, version)) if !group.is_empty() && !version.is_empty() => (
                Some(group.to_owned()),
                version.to_owned(),
                format!("/apis/{group}/{version}"),
            ),
            _ => (None, api_version.to_owned(), format!("/api/{api_version}")),
        };
        let body = self
            .get_json_required(&path)
            .await
            .map_err(|failure| failure.failure())?;
        let list: ApiResourceList = serde_json::from_value(body).map_err(|_| {
            ManagedServiceFailure::terminal(
                "K8S_DISCOVERY_INVALID",
                "Kubernetes discovery response is invalid",
            )
        })?;
        let resource = list
            .resources
            .into_iter()
            .find(|resource| resource.kind == kind)
            .ok_or_else(|| {
                ManagedServiceFailure::terminal(
                    "K8S_KIND_UNSUPPORTED",
                    "Kubernetes kind is not served by this API",
                )
            })?;
        if !resource.namespaced {
            return Err(ManagedServiceFailure::terminal(
                "K8S_CLUSTER_SCOPED_UNSUPPORTED",
                "managed service resources must be namespace-scoped",
            ));
        }
        let endpoint = ResourceEndpoint {
            group,
            version,
            plural: resource.name,
            namespaced: resource.namespaced,
        };
        if let Ok(mut discovery) = self.discovery.lock() {
            discovery.insert(key, endpoint.clone());
        }
        Ok(endpoint)
    }

    pub(crate) async fn get_resource(
        &self,
        identity: &KubernetesResourceIdentity,
    ) -> Result<Option<KubernetesObservedObject>, KubernetesApiError> {
        let endpoint = self
            .endpoint(&identity.api_version, &identity.kind)
            .await
            .map_err(|failure| {
                if failure.retryable {
                    KubernetesApiError::Retryable("K8S_API_UNAVAILABLE")
                } else {
                    KubernetesApiError::Terminal(failure.code)
                }
            })?;
        let path = self.resource_path(&endpoint, &identity.namespace, &identity.name);
        match self.get_json(&path).await? {
            Some(body) => Ok(Some(KubernetesObservedObject { body })),
            None => Ok(None),
        }
    }

    pub(crate) async fn apply_resource(
        &self,
        identity: &KubernetesResourceIdentity,
        manifest: &serde_yaml::Value,
        command: &ManagedServiceCommand,
    ) -> Result<KubernetesObservedObject, ManagedServiceFailure> {
        let endpoint = self.endpoint(&identity.api_version, &identity.kind).await?;
        if !endpoint.namespaced {
            return Err(ManagedServiceFailure::terminal(
                "K8S_CLUSTER_SCOPED_UNSUPPORTED",
                "managed service resources must be namespace-scoped",
            ));
        }
        let existing = self
            .get_resource(identity)
            .await
            .map_err(|error| error.failure())?;
        if let Some(object) = existing.as_ref() {
            namespace_ownership(&object.body, command)?;
            resource_ownership(&object.body, command, &identity.component_id)?;
        }
        let yaml = serde_yaml::to_string(manifest).map_err(|_| {
            ManagedServiceFailure::terminal(
                "K8S_RENDER_INVALID",
                "rendered Kubernetes manifest cannot be encoded",
            )
        })?;
        let path = self.resource_path(&endpoint, &identity.namespace, &identity.name);
        let dry_run = format!(
            "{path}?fieldManager={FIELD_MANAGER}&dryRun=All&force={}",
            existing.is_some()
        );
        self.patch_yaml(&dry_run, &yaml)
            .await
            .map_err(|error| error.failure())?;
        let apply_path = format!(
            "{path}?fieldManager={FIELD_MANAGER}&force={}",
            existing.is_some()
        );
        let body = self
            .patch_yaml(&apply_path, &yaml)
            .await
            .map_err(|error| error.failure())?;
        Ok(KubernetesObservedObject { body })
    }

    pub(crate) async fn delete_resource(
        &self,
        identity: &KubernetesResourceIdentity,
        command: &ManagedServiceCommand,
    ) -> Result<(), ManagedServiceFailure> {
        let endpoint = self.endpoint(&identity.api_version, &identity.kind).await?;
        let existing = self
            .get_resource(identity)
            .await
            .map_err(|error| error.failure())?;
        let Some(existing) = existing else {
            return Ok(());
        };
        resource_ownership(&existing.body, command, &identity.component_id)?;
        let path = format!(
            "{}?propagationPolicy=Foreground",
            self.resource_path(&endpoint, &identity.namespace, &identity.name)
        );
        self.request_json(Method::DELETE, &path, &json!({}), "application/json")
            .await
            .map_err(|error| error.failure())?;
        let deadline = Instant::now()
            + self.readiness_cap.min(Duration::from_secs(u64::from(
                identity.readiness_deadline_seconds,
            )));
        loop {
            if self
                .get_resource(identity)
                .await
                .map_err(|error| error.failure())?
                .is_none()
            {
                return Ok(());
            }
            if Instant::now() >= deadline {
                return Err(ManagedServiceFailure::retryable(
                    "K8S_DELETE_NOT_SETTLED",
                    "Kubernetes deletion has not settled before the bounded deadline",
                ));
            }
            sleep(self.poll_interval).await;
        }
    }

    pub(crate) async fn wait_ready(
        &self,
        identity: &KubernetesResourceIdentity,
    ) -> Result<ManagedServiceObservedState, ManagedServiceFailure> {
        if identity.readiness_rule == "exists" {
            return Ok(ManagedServiceObservedState::Ready);
        }
        let deadline = Instant::now()
            + self.readiness_cap.min(Duration::from_secs(u64::from(
                identity.readiness_deadline_seconds,
            )));
        loop {
            let Some(object) = self
                .get_resource(identity)
                .await
                .map_err(|error| error.failure())?
            else {
                if Instant::now() >= deadline {
                    return Err(ManagedServiceFailure::retryable(
                        "K8S_READINESS_TIMEOUT",
                        "managed service resource disappeared while waiting for readiness",
                    ));
                }
                sleep(self.poll_interval).await;
                continue;
            };
            if readiness_met(&identity.readiness_rule, &object.body) {
                return Ok(ManagedServiceObservedState::Ready);
            }
            if Instant::now() >= deadline {
                return Err(ManagedServiceFailure::retryable(
                    "K8S_READINESS_TIMEOUT",
                    "managed service resource did not reach its declared readiness rule",
                ));
            }
            sleep(self.poll_interval).await;
        }
    }

    fn resource_path(&self, endpoint: &ResourceEndpoint, namespace: &str, name: &str) -> String {
        let prefix = match &endpoint.group {
            Some(group) => format!("/apis/{group}/{}", endpoint.version),
            None => format!("/api/{}", endpoint.version),
        };
        format!(
            "{prefix}/namespaces/{namespace}/{}/{}",
            endpoint.plural, name
        )
    }

    async fn get_json(&self, path: &str) -> Result<Option<JsonValue>, KubernetesApiError> {
        let response = self.request(Method::GET, path, None).await?;
        if response.status() == StatusCode::NOT_FOUND {
            return Ok(None);
        }
        if !response.status().is_success() {
            return Err(map_status(response.status()));
        }
        response
            .json()
            .await
            .map(Some)
            .map_err(|_| KubernetesApiError::Terminal("K8S_RESPONSE_INVALID"))
    }

    async fn get_json_required(&self, path: &str) -> Result<JsonValue, KubernetesApiError> {
        self.get_json(path)
            .await?
            .ok_or(KubernetesApiError::NotFound)
    }

    async fn request_json(
        &self,
        method: Method,
        path: &str,
        body: &JsonValue,
        content_type: &str,
    ) -> Result<JsonValue, KubernetesApiError> {
        let response = self
            .request(
                method,
                path,
                Some((content_type, body.to_string().into_bytes())),
            )
            .await?;
        if !response.status().is_success() {
            return Err(map_status(response.status()));
        }
        response
            .json()
            .await
            .map_err(|_| KubernetesApiError::Terminal("K8S_RESPONSE_INVALID"))
    }

    async fn patch_yaml(&self, path: &str, yaml: &str) -> Result<JsonValue, KubernetesApiError> {
        let response = self
            .request(
                Method::PATCH,
                path,
                Some(("application/apply-patch+yaml", yaml.as_bytes().to_vec())),
            )
            .await?;
        if !response.status().is_success() {
            return Err(map_status(response.status()));
        }
        response
            .json()
            .await
            .map_err(|_| KubernetesApiError::Terminal("K8S_RESPONSE_INVALID"))
    }

    async fn request(
        &self,
        method: Method,
        path: &str,
        body: Option<(&str, Vec<u8>)>,
    ) -> Result<reqwest::Response, KubernetesApiError> {
        let method_name = method.as_str().to_owned();
        let mut request = self
            .client
            .request(method, format!("{}{}", self.api_url, path))
            .bearer_auth(&self.token);
        if let Some((content_type, body)) = body {
            request = request.header("content-type", content_type).body(body);
        }
        // Keep URL paths and bodies out of span attributes: both may contain
        // customer identifiers or Kubernetes diagnostics. The bearer token
        // remains opaque inside reqwest and is never attached to telemetry.
        crate::observability::otel::OtelTracer::trace_http_request(
            "kubernetes.managed_service",
            vec![
                opentelemetry::KeyValue::new("http.request.method", method_name),
                opentelemetry::KeyValue::new("server.address", "kubernetes"),
                opentelemetry::KeyValue::new("aurora.workflow", "managed_service"),
            ],
            request,
        )
        .await
        .map_err(|_| KubernetesApiError::Retryable("K8S_API_UNAVAILABLE"))
    }
}

fn read_non_empty_file(path: &str) -> Result<String, String> {
    let value =
        std::fs::read_to_string(path).map_err(|error| format!("read {path} failed: {error}"))?;
    let value = value.trim().to_owned();
    if value.is_empty() {
        return Err(format!("{path} must contain a non-empty value"));
    }
    Ok(value)
}

fn map_status(status: StatusCode) -> KubernetesApiError {
    if status == StatusCode::TOO_MANY_REQUESTS || status.is_server_error() {
        KubernetesApiError::Retryable("K8S_API_UNAVAILABLE")
    } else if status == StatusCode::CONFLICT {
        KubernetesApiError::Conflict
    } else if status == StatusCode::NOT_FOUND {
        KubernetesApiError::NotFound
    } else {
        KubernetesApiError::Terminal("K8S_APPLY_REJECTED")
    }
}

fn namespace_ownership(
    body: &JsonValue,
    command: &ManagedServiceCommand,
) -> Result<(), ManagedServiceFailure> {
    let annotations = annotations(body)?;
    let owner_id = command.owner_id.to_string();
    let workspace_id = command.workspace_id.to_string();
    if annotations
        .get(OWNER_ANNOTATION)
        .and_then(JsonValue::as_str)
        != Some(owner_id.as_str())
        || annotations
            .get(WORKSPACE_ANNOTATION)
            .and_then(JsonValue::as_str)
            != Some(workspace_id.as_str())
    {
        return Err(ManagedServiceFailure::terminal(
            "K8S_OWNERSHIP_CONFLICT",
            "Kubernetes namespace ownership marker does not match the command",
        ));
    }
    Ok(())
}

fn resource_ownership(
    body: &JsonValue,
    command: &ManagedServiceCommand,
    component_id: &str,
) -> Result<(), ManagedServiceFailure> {
    let annotations = annotations(body)?;
    let owner_id = command.owner_id.to_string();
    let workspace_id = command.workspace_id.to_string();
    let instance_id = command.instance_id.to_string();
    if annotations
        .get(OWNER_ANNOTATION)
        .and_then(JsonValue::as_str)
        != Some(owner_id.as_str())
        || annotations
            .get(WORKSPACE_ANNOTATION)
            .and_then(JsonValue::as_str)
            != Some(workspace_id.as_str())
        || annotations
            .get(INSTANCE_ANNOTATION)
            .and_then(JsonValue::as_str)
            != Some(instance_id.as_str())
    {
        return Err(ManagedServiceFailure::terminal(
            "K8S_OWNERSHIP_CONFLICT",
            "Kubernetes resource ownership marker does not match the command",
        ));
    }
    if let Some(label) = body
        .get("metadata")
        .and_then(|metadata| metadata.get("labels"))
        .and_then(|labels| labels.get(COMPONENT_LABEL))
        .and_then(JsonValue::as_str)
    {
        if label != component_id {
            return Err(ManagedServiceFailure::terminal(
                "K8S_OWNERSHIP_CONFLICT",
                "Kubernetes component marker does not match the command",
            ));
        }
    }
    Ok(())
}

fn annotations(body: &JsonValue) -> Result<&JsonMap<String, JsonValue>, ManagedServiceFailure> {
    body.get("metadata")
        .and_then(|metadata| metadata.get("annotations"))
        .and_then(JsonValue::as_object)
        .ok_or_else(|| {
            ManagedServiceFailure::terminal(
                "K8S_OWNERSHIP_CONFLICT",
                "Kubernetes resource has no Aurora ownership marker",
            )
        })
}

fn readiness_met(rule: &str, body: &JsonValue) -> bool {
    let status = body.get("status").and_then(JsonValue::as_object);
    match rule {
        "deployment_available" => {
            status
                .and_then(|value| value.get("availableReplicas"))
                .and_then(JsonValue::as_u64)
                .unwrap_or(0)
                >= body
                    .get("spec")
                    .and_then(|value| value.get("replicas"))
                    .and_then(JsonValue::as_u64)
                    .unwrap_or(1)
        }
        "statefulset_ready" => {
            status
                .and_then(|value| value.get("readyReplicas"))
                .and_then(JsonValue::as_u64)
                .unwrap_or(0)
                >= body
                    .get("spec")
                    .and_then(|value| value.get("replicas"))
                    .and_then(JsonValue::as_u64)
                    .unwrap_or(1)
        }
        "daemonset_ready" => {
            status
                .and_then(|value| value.get("numberReady"))
                .and_then(JsonValue::as_u64)
                .unwrap_or(0)
                >= status
                    .and_then(|value| value.get("desiredNumberScheduled"))
                    .and_then(JsonValue::as_u64)
                    .unwrap_or(1)
        }
        "job_complete" => status
            .and_then(|value| value.get("conditions"))
            .and_then(JsonValue::as_array)
            .is_some_and(|conditions| {
                conditions.iter().any(|condition| {
                    condition.get("type").and_then(JsonValue::as_str) == Some("Complete")
                        && condition.get("status").and_then(JsonValue::as_str) == Some("True")
                })
            }),
        "exists" => true,
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use reqwest::StatusCode;
    use serde_json::json;

    use super::{map_status, readiness_met, KubernetesApiError};

    #[test]
    fn readiness_rules_require_the_declared_controller_signal() {
        let deployment = json!({"spec": {"replicas": 3}, "status": {"availableReplicas": 2}});
        assert!(!readiness_met("deployment_available", &deployment));
        let deployment = json!({"spec": {"replicas": 3}, "status": {"availableReplicas": 3}});
        assert!(readiness_met("deployment_available", &deployment));

        let job = json!({"status": {"conditions": [{"type": "Complete", "status": "True"}]}});
        assert!(readiness_met("job_complete", &job));
        assert!(!readiness_met("unsupported", &job));
    }

    #[test]
    fn api_status_taxonomy_preserves_retry_and_conflict_semantics() {
        assert!(matches!(
            map_status(StatusCode::TOO_MANY_REQUESTS),
            KubernetesApiError::Retryable(_)
        ));
        assert!(matches!(
            map_status(StatusCode::SERVICE_UNAVAILABLE),
            KubernetesApiError::Retryable(_)
        ));
        assert!(matches!(
            map_status(StatusCode::CONFLICT),
            KubernetesApiError::Conflict
        ));
        assert!(matches!(
            map_status(StatusCode::FORBIDDEN),
            KubernetesApiError::Terminal(_)
        ));
    }
}
