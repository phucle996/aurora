use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;

use prost::Message;
use serde_json::Value as JsonValue;
use sha2::{Digest, Sha256};
use uuid::Uuid;

use crate::infra::kafka::managed_service_proto::ManagedServiceCommandV1;

use super::entity::{
    ManagedServiceCommand, ManagedServiceComponent, ManagedServiceOperation, ManagedServiceOwner,
};

const MAX_TEMPLATE_BYTES: usize = 1_048_576;
const MAX_PARAMETER_BYTES: usize = 65_536;
const MAX_COMPONENTS: usize = 128;
const MAX_DOCUMENTS: usize = 128;
const MAX_READINESS_DEADLINE_SECONDS: u32 = 3_600;

pub struct ManagedServiceOuterFence<'a> {
    pub job_id: Uuid,
    pub resource_id: &'a str,
    pub zone_id: Uuid,
    pub source_domain: &'a str,
    pub job_topic: &'a str,
    pub payload_schema_version: u32,
}

#[derive(Debug, Eq, PartialEq)]
pub struct ManagedServiceAdmissionError {
    pub code: &'static str,
    pub message: &'static str,
}

fn reject(
    code: &'static str,
    message: &'static str,
) -> Result<Arc<ManagedServiceCommand>, ManagedServiceAdmissionError> {
    Err(ManagedServiceAdmissionError { code, message })
}

pub fn admit_command(
    payload: &[u8],
    outer: ManagedServiceOuterFence<'_>,
) -> Result<Arc<ManagedServiceCommand>, ManagedServiceAdmissionError> {
    if outer.source_domain != "MANAGED_SERVICE"
        || outer.job_topic != "managed_service.instance.execute"
        || outer.payload_schema_version != 1
    {
        return reject(
            "MANAGED_SERVICE_ROUTE_INVALID",
            "managed service route contract is invalid",
        );
    }
    if payload.is_empty() || payload.len() > 1_000_000 {
        return reject(
            "MANAGED_SERVICE_COMMAND_SIZE_INVALID",
            "managed service command size is invalid",
        );
    }
    let wire =
        ManagedServiceCommandV1::decode(payload).map_err(|_| ManagedServiceAdmissionError {
            code: "MANAGED_SERVICE_COMMAND_PROTO_INVALID",
            message: "managed service command protobuf is invalid",
        })?;
    if wire.schema_version != 1 {
        return reject(
            "MANAGED_SERVICE_COMMAND_SCHEMA_INVALID",
            "managed service command schema is unsupported",
        );
    }

    let command_event_id = uuid_field(&wire.command_event_id, "MANAGED_SERVICE_EVENT_INVALID")?;
    let operation_id = uuid_field(&wire.operation_id, "MANAGED_SERVICE_OPERATION_INVALID")?;
    let instance_id = uuid_field(&wire.instance_id, "MANAGED_SERVICE_INSTANCE_INVALID")?;
    let owner_id = uuid_field(&wire.owner_id, "MANAGED_SERVICE_OWNER_INVALID")?;
    let workspace_id = uuid_field(&wire.workspace_id, "MANAGED_SERVICE_WORKSPACE_INVALID")?;
    let zone_id = uuid_field(&wire.zone_id, "MANAGED_SERVICE_ZONE_INVALID")?;
    let instance_revision_id = uuid_field(
        &wire.instance_revision_id,
        "MANAGED_SERVICE_INSTANCE_REVISION_INVALID",
    )?;
    let blueprint_revision_id = uuid_field(
        &wire.blueprint_revision_id,
        "MANAGED_SERVICE_BLUEPRINT_REVISION_INVALID",
    )?;
    if command_event_id != outer.job_id
        || instance_id.to_string() != outer.resource_id
        || zone_id != outer.zone_id
    {
        return reject(
            "MANAGED_SERVICE_OUTER_FENCE_MISMATCH",
            "managed service inner and outer fences do not match",
        );
    }
    let owner_type = match wire.owner_type {
        1 => ManagedServiceOwner::Personal,
        2 => ManagedServiceOwner::Tenant,
        _ => {
            return reject(
                "MANAGED_SERVICE_OWNER_INVALID",
                "managed service owner type is invalid",
            )
        }
    };
    let operation = match wire.operation_kind {
        1 => ManagedServiceOperation::Create,
        3 => ManagedServiceOperation::Delete,
        4 => ManagedServiceOperation::Resize,
        _ => {
            return reject(
                "MANAGED_SERVICE_OPERATION_INVALID",
                "managed service operation kind is invalid",
            )
        }
    };
    if wire.generation == 0 || !valid_instance_code(&wire.instance_code) {
        return reject(
            "MANAGED_SERVICE_EXECUTION_FENCE_INVALID",
            "managed service generation or instance code is invalid",
        );
    }
    if wire.template_yaml.is_empty() || wire.template_yaml.len() > MAX_TEMPLATE_BYTES {
        return reject(
            "MANAGED_SERVICE_TEMPLATE_INVALID",
            "managed service template size is invalid",
        );
    }
    let bundle_hash = hash_field(&wire.bundle_hash, "MANAGED_SERVICE_BUNDLE_HASH_INVALID")?;
    let component_contract_hash = hash_field(
        &wire.component_contract_hash,
        "MANAGED_SERVICE_COMPONENT_HASH_INVALID",
    )?;
    let input_hash = hash_field(&wire.input_hash, "MANAGED_SERVICE_INPUT_HASH_INVALID")?;
    let desired_spec_hash = hash_field(
        &wire.desired_spec_hash,
        "MANAGED_SERVICE_DESIRED_HASH_INVALID",
    )?;
    let parameter_values_hash = hash_field(
        &wire.parameter_values_sha256,
        "MANAGED_SERVICE_PARAMETER_HASH_INVALID",
    )?;
    if Sha256::digest(wire.template_yaml.as_bytes()).as_slice() != bundle_hash {
        return reject(
            "MANAGED_SERVICE_BUNDLE_HASH_MISMATCH",
            "managed service template bundle hash does not match",
        );
    }

    let parameters = if operation == ManagedServiceOperation::Delete {
        if !wire.parameter_values.is_empty() {
            return reject(
                "MANAGED_SERVICE_DELETE_INPUT_INVALID",
                "managed service delete command must not contain parameter values",
            );
        }
        BTreeMap::new()
    } else {
        if wire.parameter_values.is_empty() || wire.parameter_values.len() > MAX_PARAMETER_BYTES {
            return reject(
                "MANAGED_SERVICE_PARAMETER_VALUES_INVALID",
                "managed service parameter values size is invalid",
            );
        }
        if Sha256::digest(&wire.parameter_values).as_slice() != parameter_values_hash
            || parameter_values_hash != input_hash
        {
            return reject(
                "MANAGED_SERVICE_INPUT_HASH_MISMATCH",
                "managed service parameter digest does not match",
            );
        }
        let parsed: BTreeMap<String, JsonValue> = serde_json::from_slice(&wire.parameter_values)
            .map_err(|_| ManagedServiceAdmissionError {
                code: "MANAGED_SERVICE_PARAMETER_VALUES_INVALID",
                message: "managed service parameter values are invalid",
            })?;
        if parsed.len() > 64
            || parsed
                .iter()
                .any(|(key, value)| !valid_parameter_key(key) || !valid_parameter_value(value))
        {
            return reject(
                "MANAGED_SERVICE_PARAMETER_VALUES_INVALID",
                "managed service parameter values violate bounded scalar contract",
            );
        }
        parsed
    };

    let expected_desired_hash = match operation {
        ManagedServiceOperation::Create => {
            let mut source = format!(
                "{}:{}:{}:{}:{}:",
                workspace_id, zone_id, wire.instance_code, blueprint_revision_id, wire.generation
            )
            .into_bytes();
            source.extend_from_slice(&wire.parameter_values);
            Sha256::digest(source)
        }
        ManagedServiceOperation::Resize => {
            let mut source = format!(
                "{}:{}:{}:",
                workspace_id, wire.instance_code, wire.generation
            )
            .into_bytes();
            source.extend_from_slice(&wire.parameter_values);
            Sha256::digest(source)
        }
        ManagedServiceOperation::Delete => Sha256::digest([]),
    };
    if operation != ManagedServiceOperation::Delete
        && expected_desired_hash.as_slice() != desired_spec_hash
    {
        return reject(
            "MANAGED_SERVICE_DESIRED_HASH_MISMATCH",
            "managed service desired hash does not match",
        );
    }

    if wire.components.is_empty() || wire.components.len() > MAX_COMPONENTS {
        return reject(
            "MANAGED_SERVICE_COMPONENT_CONTRACT_INVALID",
            "managed service component contract size is invalid",
        );
    }
    let mut ids = BTreeSet::new();
    let mut apply_orders = BTreeSet::new();
    let mut delete_orders = BTreeSet::new();
    let mut components = Vec::with_capacity(wire.components.len());
    for component in wire.components {
        if !valid_component_id(&component.component_id)
            || !ids.insert(component.component_id.clone())
            || component.apply_order == 0
            || !apply_orders.insert(component.apply_order)
            || component.delete_order == 0
            || !delete_orders.insert(component.delete_order)
            || !valid_readiness_rule(&component.readiness_rule)
            || !(1..=MAX_READINESS_DEADLINE_SECONDS).contains(&component.readiness_deadline_seconds)
        {
            return reject(
                "MANAGED_SERVICE_COMPONENT_CONTRACT_INVALID",
                "managed service component contract is invalid",
            );
        }
        let mut document_indexes = component
            .document_indexes
            .into_iter()
            .map(|value| value as usize)
            .collect::<Vec<_>>();
        document_indexes.sort_unstable();
        document_indexes.dedup();
        if document_indexes.iter().any(|index| *index >= MAX_DOCUMENTS) {
            return reject(
                "MANAGED_SERVICE_COMPONENT_CONTRACT_INVALID",
                "managed service component document index is invalid",
            );
        }
        components.push(ManagedServiceComponent {
            id: component.component_id,
            document_indexes,
            apply_order: component.apply_order,
            delete_order: component.delete_order,
            readiness_rule: component.readiness_rule,
            readiness_deadline_seconds: component.readiness_deadline_seconds,
        });
    }
    if canonical_component_contract_hash(&components) != component_contract_hash {
        return reject(
            "MANAGED_SERVICE_COMPONENT_HASH_MISMATCH",
            "managed service component contract hash does not match",
        );
    }

    let now = chrono::Utc::now().timestamp_millis();
    if wire.issued_at_unix_ms <= 0 || wire.issued_at_unix_ms > now.saturating_add(300_000) {
        return reject(
            "MANAGED_SERVICE_ISSUED_AT_INVALID",
            "managed service issued timestamp is invalid",
        );
    }

    Ok(Arc::new(ManagedServiceCommand {
        command_event_id,
        operation_id,
        instance_id,
        owner_type,
        owner_id,
        workspace_id,
        zone_id,
        instance_code: wire.instance_code,
        operation,
        generation: wire.generation,
        instance_revision_id,
        blueprint_revision_id,
        template_yaml: wire.template_yaml,
        components,
        bundle_hash,
        component_contract_hash,
        input_hash,
        desired_spec_hash,
        parameters,
        _issued_at_unix_ms: wire.issued_at_unix_ms,
    }))
}

fn uuid_field(bytes: &[u8], code: &'static str) -> Result<Uuid, ManagedServiceAdmissionError> {
    let value = Uuid::from_slice(bytes).map_err(|_| ManagedServiceAdmissionError {
        code,
        message: "managed service UUID field is invalid",
    })?;
    if value.is_nil() {
        return Err(ManagedServiceAdmissionError {
            code,
            message: "managed service UUID field must be non-nil",
        });
    }
    Ok(value)
}

fn hash_field(bytes: &[u8], code: &'static str) -> Result<[u8; 32], ManagedServiceAdmissionError> {
    bytes.try_into().map_err(|_| ManagedServiceAdmissionError {
        code,
        message: "managed service hash field must contain 32 bytes",
    })
}

fn valid_instance_code(value: &str) -> bool {
    let bytes = value.as_bytes();
    (1..=35).contains(&bytes.len())
        && bytes[0].is_ascii_lowercase()
        && bytes
            .iter()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || *byte == b'-')
        && bytes
            .last()
            .is_some_and(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit())
}

fn valid_component_id(value: &str) -> bool {
    let bytes = value.as_bytes();
    (1..=27).contains(&bytes.len())
        && bytes[0].is_ascii_lowercase()
        && bytes
            .iter()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || *byte == b'-')
        && bytes
            .last()
            .is_some_and(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit())
}

fn valid_parameter_key(value: &str) -> bool {
    let bytes = value.as_bytes();
    (1..=64).contains(&bytes.len())
        && bytes[0].is_ascii_lowercase()
        && bytes.iter().all(|byte| {
            byte.is_ascii_lowercase()
                || byte.is_ascii_digit()
                || matches!(*byte, b'_' | b'.' | b'-')
        })
}

fn valid_parameter_value(value: &JsonValue) -> bool {
    match value {
        JsonValue::Bool(_) | JsonValue::Number(_) => true,
        JsonValue::String(value) => value.len() <= 4_096,
        JsonValue::Array(values) => {
            values.len() <= 64
                && values.iter().all(|value| {
                    matches!(value, JsonValue::Bool(_) | JsonValue::Number(_))
                        || matches!(value, JsonValue::String(text) if text.len() <= 4_096)
                })
        }
        JsonValue::Null | JsonValue::Object(_) => false,
    }
}

fn valid_readiness_rule(value: &str) -> bool {
    matches!(
        value,
        "exists"
            | "deployment_available"
            | "statefulset_ready"
            | "daemonset_ready"
            | "job_complete"
    )
}

fn canonical_component_contract_hash(components: &[ManagedServiceComponent]) -> [u8; 32] {
    let rows = components
        .iter()
        .map(|component| {
            let mut readiness = BTreeMap::new();
            readiness.insert(
                "deadline_seconds".to_string(),
                JsonValue::from(component.readiness_deadline_seconds),
            );
            readiness.insert(
                "type".to_string(),
                JsonValue::from(component.readiness_rule.clone()),
            );
            let mut row = BTreeMap::new();
            row.insert(
                "apply_order".to_string(),
                JsonValue::from(component.apply_order),
            );
            row.insert(
                "delete_order".to_string(),
                JsonValue::from(component.delete_order),
            );
            if !component.document_indexes.is_empty() {
                row.insert(
                    "document_indexes".to_string(),
                    JsonValue::from(
                        component
                            .document_indexes
                            .iter()
                            .map(|index| JsonValue::from(*index as u64))
                            .collect::<Vec<_>>(),
                    ),
                );
            }
            row.insert("id".to_string(), JsonValue::from(component.id.clone()));
            row.insert(
                "readiness".to_string(),
                serde_json::to_value(readiness).expect("bounded readiness contract"),
            );
            row
        })
        .collect::<Vec<_>>();
    let encoded = serde_json::to_vec(&rows).expect("bounded component contract");
    Sha256::digest(encoded).into()
}

#[cfg(test)]
pub(super) fn component_contract_hash_for_test(components: &[ManagedServiceComponent]) -> [u8; 32] {
    canonical_component_contract_hash(components)
}
