use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use uuid::Uuid;

const MAX_MODULE_LENGTH: usize = 64;
const MAX_RESOURCE_TYPE_LENGTH: usize = 64;
const MAX_COMPONENT_LENGTH: usize = 128;
const MAX_PANEL_LENGTH: usize = 64;
const SUPPORTED_PANELS: [&str; 4] = ["health", "metrics", "logs", "events"];

#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct RuntimeScope {
    pub module: String,
    pub resource_type: String,
    pub resource_id: Uuid,
    pub resource_name: Option<String>,
    pub owner_id: Uuid,
    pub workspace_id: Uuid,
    pub zone_id: Uuid,
    pub component_id: Option<String>,
    pub panel_id: String,
    pub snapshot_seconds: u64,
}

#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct SubscriptionKey {
    pub scope: RuntimeScope,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StreamQuery {
    pub panel_id: Option<String>,
    pub component_id: Option<String>,
    pub from_seconds: Option<u64>,
}

#[derive(Clone, Debug, Serialize)]
pub struct RuntimeEvent {
    pub schema_version: u16,
    pub module: String,
    pub resource_type: String,
    pub resource_id: Uuid,
    pub component_id: Option<String>,
    pub event_type: String,
    pub event_id: String,
    pub observed_at: String,
    pub payload: BTreeMap<String, serde_json::Value>,
}

#[derive(Clone, Debug)]
pub enum RuntimeFrame {
    Event(RuntimeEvent),
    Error { event_id: String, code: String },
}

impl RuntimeScope {
    pub fn validate(&self, expected_zone: Uuid) -> Result<(), ContractError> {
        if self.zone_id != expected_zone {
            return Err(ContractError::ZoneMismatch);
        }
        validate_token(&self.module, MAX_MODULE_LENGTH, "module")?;
        validate_token(
            &self.resource_type,
            MAX_RESOURCE_TYPE_LENGTH,
            "resource_type",
        )?;
        validate_token(&self.panel_id, MAX_PANEL_LENGTH, "panel_id")?;
        if !SUPPORTED_PANELS.contains(&self.panel_id.as_str()) {
            return Err(ContractError::UnsupportedPanel);
        }
        if let Some(component_id) = &self.component_id {
            validate_token(component_id, MAX_COMPONENT_LENGTH, "component_id")?;
        }
        if self.resource_id.is_nil() || self.owner_id.is_nil() || self.workspace_id.is_nil() {
            return Err(ContractError::NilIdentity);
        }
        if self.snapshot_seconds == 0 {
            return Err(ContractError::SnapshotWindowInvalid);
        }
        let adapter_exists = match self.module.as_str() {
            "mail" => crate::mail::validate_scope(self),
            "storage" => crate::storage::validate_scope(self),
            "managed_service" => self.resource_type == "instance",
            "hypervisor" => self.resource_type == "vm",
            _ => false,
        };
        if !adapter_exists {
            return Err(ContractError::UnsupportedResource);
        }
        Ok(())
    }
}

impl RuntimeFrame {
    pub fn event_type(&self) -> &'static str {
        match self {
            Self::Event(event) => match event.event_type.as_str() {
                "runtime.snapshot" => "runtime.snapshot",
                "runtime.metric" => "runtime.metric",
                "runtime.log" => "runtime.log",
                "runtime.state" => "runtime.state",
                "runtime.event" => "runtime.event",
                _ => "runtime.event",
            },
            Self::Error { .. } => "stream.error",
        }
    }

    pub fn event_id(&self) -> &str {
        match self {
            Self::Event(event) => &event.event_id,
            Self::Error { event_id, .. } => event_id,
        }
    }

    pub fn data(&self) -> Result<String, serde_json::Error> {
        match self {
            Self::Event(event) => serde_json::to_string(event),
            Self::Error { code, .. } => serde_json::to_string(&serde_json::json!({
                "code": code
            })),
        }
    }
}

#[derive(Debug, thiserror::Error)]
pub enum ContractError {
    #[error("zone scope mismatch")]
    ZoneMismatch,
    #[error("identity must be non-nil")]
    NilIdentity,
    #[error("{0} is invalid")]
    InvalidToken(&'static str),
    #[error("panel is not enabled")]
    UnsupportedPanel,
    #[error("snapshot window is invalid")]
    SnapshotWindowInvalid,
    #[error("runtime resource adapter is not enabled")]
    UnsupportedResource,
}

fn validate_token(
    value: &str,
    max_length: usize,
    field: &'static str,
) -> Result<(), ContractError> {
    if value.is_empty()
        || value.len() > max_length
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
    {
        return Err(ContractError::InvalidToken(field));
    }
    Ok(())
}

#[cfg(test)]
#[path = "../test/contract.rs"]
mod tests;
