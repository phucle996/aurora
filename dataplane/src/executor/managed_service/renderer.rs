use std::collections::{BTreeMap, BTreeSet};

use serde::Deserialize;
use serde_json::Value as JsonValue;
use serde_yaml::{Mapping, Value as YamlValue};
use sha2::{Digest, Sha256};

use super::entity::{
    KubernetesResourceIdentity, ManagedServiceCommand, ManagedServiceFailure,
    ManagedServiceOperation, ManagedServiceOwner, RenderedGraph, RenderedResource,
};

const MAX_DOCUMENTS: usize = 128;
const PROTECTED_PREFIX: &str = "platform.aurora.io/";

pub fn render_graph(
    command: &ManagedServiceCommand,
) -> Result<RenderedGraph, ManagedServiceFailure> {
    let namespace = workspace_namespace(command.owner_type, command.owner_id, command.workspace_id);
    let component_contract = command
        .components
        .iter()
        .map(|component| (component.id.as_str(), component))
        .collect::<BTreeMap<_, _>>();
    let mut seen_components = BTreeSet::new();
    let mut derived_indexes = BTreeMap::<String, Vec<usize>>::new();
    let mut resources = Vec::new();

    let deserializer = serde_yaml::Deserializer::from_str(&command.template_yaml);
    for (document_index, document) in deserializer.enumerate() {
        if document_index >= MAX_DOCUMENTS {
            return Err(template_error());
        }
        let mut manifest = YamlValue::deserialize(document).map_err(|_| template_error())?;
        let root = manifest.as_mapping_mut().ok_or_else(template_error)?;
        let api_version = static_root_string(root, "apiVersion")?;
        let kind = static_root_string(root, "kind")?;
        let component_id = take_component_tag(root)?;
        let component = component_contract
            .get(component_id.as_str())
            .ok_or_else(template_error)?;
        validate_secret_template(root, &kind)?;
        validate_unprotected_metadata(root)?;
        render_parameter_nodes(
            &mut manifest,
            &command.parameters,
            false,
            command.operation == ManagedServiceOperation::Delete,
        )?;
        let root = manifest.as_mapping_mut().ok_or_else(template_error)?;
        let name = if component_id == "primary" {
            command.instance_code.clone()
        } else {
            format!("{}-{component_id}", command.instance_code)
        };
        inject_metadata(root, &namespace, &name, &component_id, command, None)?;
        seen_components.insert(component_id.clone());
        derived_indexes
            .entry(component_id.clone())
            .or_default()
            .push(document_index);
        resources.push(RenderedResource {
            identity: KubernetesResourceIdentity {
                api_version,
                kind,
                namespace: namespace.clone(),
                name,
                component_id,
                document_index,
                apply_order: component.apply_order,
                delete_order: component.delete_order,
                readiness_rule: component.readiness_rule.clone(),
                readiness_deadline_seconds: component.readiness_deadline_seconds,
            },
            manifest,
        });
    }
    if resources.is_empty() || seen_components.len() != component_contract.len() {
        return Err(template_error());
    }
    for component in &command.components {
        let actual = derived_indexes
            .get(&component.id)
            .ok_or_else(template_error)?;
        if !component.document_indexes.is_empty() && &component.document_indexes != actual {
            return Err(template_error());
        }
    }

    resources.sort_by_key(|resource| {
        (
            resource.identity.apply_order,
            resource.identity.document_index,
        )
    });
    let mut hasher = Sha256::new();
    for resource in &resources {
        let encoded = serde_yaml::to_string(&resource.manifest).map_err(|_| template_error())?;
        hasher.update((encoded.len() as u64).to_be_bytes());
        hasher.update(encoded.as_bytes());
    }
    let render_hash: [u8; 32] = hasher.finalize().into();
    let render_hash_hex = hex(&render_hash);
    for resource in &mut resources {
        let root = resource
            .manifest
            .as_mapping_mut()
            .ok_or_else(template_error)?;
        inject_metadata(
            root,
            &resource.identity.namespace,
            &resource.identity.name,
            &resource.identity.component_id,
            command,
            Some(&render_hash_hex),
        )?;
    }

    Ok(RenderedGraph {
        namespace,
        resources,
    })
}

fn static_root_string(root: &Mapping, key: &str) -> Result<String, ManagedServiceFailure> {
    let value = root
        .get(YamlValue::String(key.to_string()))
        .and_then(YamlValue::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .ok_or_else(template_error)?;
    Ok(value.to_string())
}

fn take_component_tag(root: &mut Mapping) -> Result<String, ManagedServiceFailure> {
    let metadata = root
        .get_mut(YamlValue::String("metadata".to_string()))
        .and_then(YamlValue::as_mapping_mut)
        .ok_or_else(template_error)?;
    if metadata.contains_key(YamlValue::String("namespace".to_string()))
        || metadata.contains_key(YamlValue::String("generateName".to_string()))
    {
        return Err(template_error());
    }
    let name_key = YamlValue::String("name".to_string());
    let name = metadata.get(&name_key).ok_or_else(template_error)?;
    let YamlValue::Tagged(tagged) = name else {
        return Err(template_error());
    };
    if tagged.tag != "!aurora/component" {
        return Err(template_error());
    }
    let component = tagged
        .value
        .as_str()
        .map(str::trim)
        .ok_or_else(template_error)?;
    if component.is_empty() {
        return Err(template_error());
    }
    let component = component.to_string();
    // Consume the only legal component tag before the generic AST walk. Any
    // second component tag is then rejected as an unsupported tag instead of
    // leaking a platform-specific YAML tag to Kubernetes.
    metadata.insert(name_key, YamlValue::String(component.clone()));
    Ok(component)
}

fn render_parameter_nodes(
    node: &mut YamlValue,
    parameters: &BTreeMap<String, JsonValue>,
    inside_metadata: bool,
    allow_missing_for_delete: bool,
) -> Result<(), ManagedServiceFailure> {
    match node {
        YamlValue::Tagged(tagged) => {
            if tagged.tag == "!aurora/param" {
                if inside_metadata {
                    return Err(template_error());
                }
                let key = tagged
                    .value
                    .as_str()
                    .map(str::trim)
                    .ok_or_else(template_error)?;
                let replacement = match parameters.get(key) {
                    Some(value) => serde_yaml::to_value(value).map_err(|_| template_error())?,
                    None if allow_missing_for_delete => YamlValue::Null,
                    None => return Err(template_error()),
                };
                *node = replacement;
                return Ok(());
            }
            Err(template_error())
        }
        YamlValue::Mapping(mapping) => {
            for (key, value) in mapping.iter_mut() {
                if let YamlValue::String(text) = key {
                    if text.contains("{{") || text.contains("}}") {
                        return Err(template_error());
                    }
                }
                let child_inside_metadata =
                    inside_metadata || matches!(key, YamlValue::String(text) if text == "metadata");
                render_parameter_nodes(
                    value,
                    parameters,
                    child_inside_metadata,
                    allow_missing_for_delete,
                )?;
            }
            Ok(())
        }
        YamlValue::Sequence(values) => {
            for value in values {
                render_parameter_nodes(
                    value,
                    parameters,
                    inside_metadata,
                    allow_missing_for_delete,
                )?;
            }
            Ok(())
        }
        YamlValue::String(value) => {
            if value.contains("{{") || value.contains("}}") {
                return Err(template_error());
            }
            Ok(())
        }
        _ => Ok(()),
    }
}

fn validate_secret_template(root: &Mapping, kind: &str) -> Result<(), ManagedServiceFailure> {
    if kind != "Secret" {
        return Ok(());
    }
    for field in ["data", "stringData"] {
        let Some(values) = root.get(YamlValue::String(field.to_string())) else {
            continue;
        };
        let mapping = values.as_mapping().ok_or_else(template_error)?;
        for value in mapping.values() {
            if !matches!(value, YamlValue::Tagged(tagged) if tagged.tag == "!aurora/param") {
                return Err(template_error());
            }
        }
    }
    Ok(())
}

fn inject_metadata(
    root: &mut Mapping,
    namespace: &str,
    name: &str,
    component_id: &str,
    command: &ManagedServiceCommand,
    render_hash: Option<&str>,
) -> Result<(), ManagedServiceFailure> {
    let metadata = root
        .get_mut(YamlValue::String("metadata".to_string()))
        .and_then(YamlValue::as_mapping_mut)
        .ok_or_else(template_error)?;
    metadata.insert(
        YamlValue::String("name".to_string()),
        YamlValue::String(name.to_string()),
    );
    metadata.insert(
        YamlValue::String("namespace".to_string()),
        YamlValue::String(namespace.to_string()),
    );
    let annotations = metadata_map(metadata, "annotations")?;
    annotations.insert(
        yaml_string("platform.aurora.io/owner-id"),
        yaml_string(command.owner_id),
    );
    annotations.insert(
        yaml_string("platform.aurora.io/workspace-id"),
        yaml_string(command.workspace_id),
    );
    annotations.insert(
        yaml_string("platform.aurora.io/instance-id"),
        yaml_string(command.instance_id),
    );
    annotations.insert(
        yaml_string("platform.aurora.io/operation-id"),
        yaml_string(command.operation_id),
    );
    annotations.insert(
        yaml_string("platform.aurora.io/instance-revision-id"),
        yaml_string(command.instance_revision_id),
    );
    annotations.insert(
        yaml_string("platform.aurora.io/blueprint-revision-id"),
        yaml_string(command.blueprint_revision_id),
    );
    annotations.insert(
        yaml_string("platform.aurora.io/generation"),
        yaml_string(command.generation),
    );
    annotations.insert(
        yaml_string("platform.aurora.io/desired-spec-sha256"),
        YamlValue::String(hex(&command.desired_spec_hash)),
    );
    if let Some(render_hash) = render_hash {
        annotations.insert(
            yaml_string("platform.aurora.io/render-sha256"),
            YamlValue::String(render_hash.to_string()),
        );
    }
    let labels = metadata_map(metadata, "labels")?;
    labels.insert(
        yaml_string("platform.aurora.io/instance"),
        yaml_string(command.instance_id),
    );
    labels.insert(
        yaml_string("platform.aurora.io/component"),
        YamlValue::String(component_id.to_string()),
    );
    Ok(())
}

fn validate_unprotected_metadata(root: &Mapping) -> Result<(), ManagedServiceFailure> {
    let metadata = root
        .get(YamlValue::String("metadata".to_string()))
        .and_then(YamlValue::as_mapping)
        .ok_or_else(template_error)?;
    for key in ["annotations", "labels"] {
        if let Some(values) = metadata.get(YamlValue::String(key.to_string())) {
            let values = values.as_mapping().ok_or_else(template_error)?;
            reject_protected_keys(values)?;
        }
    }
    Ok(())
}

fn metadata_map<'a>(
    metadata: &'a mut Mapping,
    key: &str,
) -> Result<&'a mut Mapping, ManagedServiceFailure> {
    let key_value = YamlValue::String(key.to_string());
    if !metadata.contains_key(&key_value) {
        metadata.insert(key_value.clone(), YamlValue::Mapping(Mapping::new()));
    }
    metadata
        .get_mut(&key_value)
        .and_then(YamlValue::as_mapping_mut)
        .ok_or_else(template_error)
}

fn reject_protected_keys(mapping: &Mapping) -> Result<(), ManagedServiceFailure> {
    if mapping
        .keys()
        .any(|key| matches!(key, YamlValue::String(value) if value.starts_with(PROTECTED_PREFIX)))
    {
        return Err(template_error());
    }
    Ok(())
}

fn workspace_namespace(
    owner_type: ManagedServiceOwner,
    owner_id: uuid::Uuid,
    workspace_id: uuid::Uuid,
) -> String {
    let owner_prefix = match owner_type {
        ManagedServiceOwner::Personal => 'p',
        ManagedServiceOwner::Tenant => 't',
    };
    let mut input = [0_u8; 32];
    input[..16].copy_from_slice(owner_id.as_bytes());
    input[16..].copy_from_slice(workspace_id.as_bytes());
    format!("aur-ms-{owner_prefix}-{}", base32_lower(&input))
}

fn base32_lower(input: &[u8]) -> String {
    const ALPHABET: &[u8; 32] = b"abcdefghijklmnopqrstuvwxyz234567";
    let mut output = String::with_capacity((input.len() * 8).div_ceil(5));
    let mut buffer = 0_u32;
    let mut bits = 0_u8;
    for byte in input {
        buffer = (buffer << 8) | u32::from(*byte);
        bits += 8;
        while bits >= 5 {
            bits -= 5;
            output.push(char::from(ALPHABET[((buffer >> bits) & 31) as usize]));
        }
    }
    if bits > 0 {
        output.push(char::from(ALPHABET[((buffer << (5 - bits)) & 31) as usize]));
    }
    output
}

fn yaml_string(value: impl ToString) -> YamlValue {
    YamlValue::String(value.to_string())
}

fn hex(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut output = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        output.push(char::from(HEX[usize::from(byte >> 4)]));
        output.push(char::from(HEX[usize::from(byte & 0x0f)]));
    }
    output
}

fn template_error() -> ManagedServiceFailure {
    ManagedServiceFailure::terminal(
        "SRE_TEMPLATE_INPUT_MISMATCH",
        "published template and customer input cannot be rendered safely",
    )
}
