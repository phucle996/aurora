use std::collections::BTreeMap;

use serde_json::Value as JsonValue;
use serde_yaml::Value as YamlValue;
use uuid::Uuid;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ManagedServiceOperation {
    Create,
    Resize,
    Delete,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ManagedServiceOwner {
    Personal,
    Tenant,
}

#[derive(Clone)]
pub struct ManagedServiceComponent {
    pub id: String,
    pub document_indexes: Vec<usize>,
    pub apply_order: u32,
    pub delete_order: u32,
    pub readiness_rule: String,
    pub readiness_deadline_seconds: u32,
}

/// Authenticated inner command. It intentionally has no `Debug`: template and
/// parameter values may contain credentials intended only for Kubernetes.
#[derive(Clone)]
pub struct ManagedServiceCommand {
    pub command_event_id: Uuid,
    pub operation_id: Uuid,
    pub instance_id: Uuid,
    pub owner_type: ManagedServiceOwner,
    pub owner_id: Uuid,
    pub workspace_id: Uuid,
    pub zone_id: Uuid,
    pub instance_code: String,
    pub operation: ManagedServiceOperation,
    pub generation: u64,
    pub instance_revision_id: Uuid,
    pub blueprint_revision_id: Uuid,
    pub template_yaml: String,
    pub components: Vec<ManagedServiceComponent>,
    pub bundle_hash: [u8; 32],
    pub component_contract_hash: [u8; 32],
    pub input_hash: [u8; 32],
    pub desired_spec_hash: [u8; 32],
    pub parameters: BTreeMap<String, JsonValue>,
    pub _issued_at_unix_ms: i64,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct KubernetesResourceIdentity {
    pub api_version: String,
    pub kind: String,
    pub namespace: String,
    pub name: String,
    pub component_id: String,
    pub document_index: usize,
    pub apply_order: u32,
    pub delete_order: u32,
    pub readiness_rule: String,
    pub readiness_deadline_seconds: u32,
}

/// Rendered manifests can contain Kubernetes Secret values and therefore must
/// never implement `Debug`, `Display`, serialization to logs or persistence.
pub struct RenderedResource {
    pub identity: KubernetesResourceIdentity,
    pub manifest: YamlValue,
}

pub struct RenderedGraph {
    pub namespace: String,
    pub resources: Vec<RenderedResource>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ManagedServiceObservedState {
    Unknown,
    Ready,
    Degraded,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ManagedServiceFailure {
    pub code: &'static str,
    pub message: &'static str,
    pub retryable: bool,
    pub observed_state: ManagedServiceObservedState,
}

impl ManagedServiceFailure {
    pub fn terminal(code: &'static str, message: &'static str) -> Self {
        Self {
            code,
            message,
            retryable: false,
            observed_state: ManagedServiceObservedState::Degraded,
        }
    }

    pub fn retryable(code: &'static str, message: &'static str) -> Self {
        Self {
            code,
            message,
            retryable: true,
            observed_state: ManagedServiceObservedState::Degraded,
        }
    }
}

pub struct KubernetesObservedObject {
    pub body: JsonValue,
}
