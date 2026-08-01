use crate::application::ports::AppError;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ActivityCategory {
    Security,
    Identity,
    Resource,
    Billing,
    Access,
}

impl ActivityCategory {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "security" => Some(Self::Security),
            "identity" => Some(Self::Identity),
            "resource" => Some(Self::Resource),
            "billing" => Some(Self::Billing),
            "access" => Some(Self::Access),
            _ => None,
        }
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Security => "security",
            Self::Identity => "identity",
            Self::Resource => "resource",
            Self::Billing => "billing",
            Self::Access => "access",
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ActivityOutcome {
    Succeeded,
    Failed,
    Started,
}

impl ActivityOutcome {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "succeeded" => Some(Self::Succeeded),
            "failed" => Some(Self::Failed),
            "started" => Some(Self::Started),
            _ => None,
        }
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Succeeded => "succeeded",
            Self::Failed => "failed",
            Self::Started => "started",
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ActorType {
    SelfUser,
    Admin,
    System,
}

impl ActorType {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "self" => Some(Self::SelfUser),
            "admin" => Some(Self::Admin),
            "system" => Some(Self::System),
            _ => None,
        }
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            Self::SelfUser => "self",
            Self::Admin => "admin",
            Self::System => "system",
        }
    }
}

#[derive(Clone, Debug)]
pub struct ActivityEvent {
    pub event_id: Uuid,
    pub user_id: Uuid,
    pub category: ActivityCategory,
    pub action: String,
    pub actor_type: ActorType,
    pub actor_id: Option<Uuid>,
    pub outcome: ActivityOutcome,
    pub source_service: String,
    pub resource_type: Option<String>,
    pub resource_id: Option<String>,
    pub operation_id: Option<String>,
    pub title: String,
    pub summary: String,
    pub occurred_at: DateTime<Utc>,
    pub metadata_json: String,
    pub schema_version: u32,
    /// Stable replay ordering for one event identity. A larger version must
    /// win even when Redis pending-entry recovery delivers it out of order.
    pub projection_version: i64,
}

impl ActivityEvent {
    pub fn validate(&self) -> Result<(), AppError> {
        if self.action.is_empty() || self.action.len() > 128 {
            return Err(invalid("activity action length is invalid"));
        }
        if self.source_service.is_empty() || self.source_service.len() > 64 {
            return Err(invalid("activity source service length is invalid"));
        }
        if self.title.len() > 256 || self.summary.len() > 4_096 {
            return Err(invalid("activity display fields are too large"));
        }
        if self.metadata_json.len() > 16 * 1024
            || serde_json::from_str::<serde_json::Value>(&self.metadata_json).is_err()
        {
            return Err(invalid("activity metadata must be bounded JSON"));
        }
        if self.schema_version == 0 {
            return Err(invalid("activity schema version is invalid"));
        }
        if self.occurred_at > Utc::now() + chrono::Duration::minutes(5) {
            return Err(invalid("activity timestamp is too far in the future"));
        }
        Ok(())
    }
}

#[derive(Clone, Debug)]
pub struct NotificationItem {
    pub notification_id: Uuid,
    pub activity_event_id: Uuid,
    pub user_id: Uuid,
    pub severity: String,
    pub title: String,
    pub message: String,
    pub operation: String,
    pub resource_id: Option<String>,
    pub created_at: DateTime<Utc>,
    pub projection_version: i64,
}

impl NotificationItem {
    pub fn validate(&self) -> Result<(), AppError> {
        if self.severity.len() > 32
            || self.title.len() > 256
            || self.message.len() > 4_096
            || self.operation.len() > 128
        {
            return Err(invalid("notification display fields are too large"));
        }
        Ok(())
    }
}

#[derive(Clone, Debug, Deserialize)]
pub struct PageRequest {
    pub cursor: Option<String>,
    pub limit: Option<usize>,
    pub category: Option<String>,
}

#[derive(Clone, Debug, Serialize)]
pub struct ActivityView {
    pub event_id: Uuid,
    pub category: String,
    pub action: String,
    pub actor_type: String,
    pub outcome: String,
    pub source_service: String,
    pub resource_type: Option<String>,
    pub resource_id: Option<String>,
    pub operation_id: Option<String>,
    pub title: String,
    pub summary: String,
    pub occurred_at: DateTime<Utc>,
    pub metadata: serde_json::Value,
}

#[derive(Clone, Debug, Serialize)]
pub struct NotificationView {
    pub notification_id: Uuid,
    pub activity_event_id: Uuid,
    pub severity: String,
    pub title: String,
    pub message: String,
    pub operation: String,
    pub resource_id: Option<String>,
    pub created_at: DateTime<Utc>,
    pub read_at: Option<DateTime<Utc>>,
}

#[derive(Clone, Debug, Serialize)]
pub struct ActivityPage {
    pub items: Vec<ActivityView>,
    pub next_cursor: Option<String>,
}

#[derive(Clone, Debug, Serialize)]
pub struct NotificationPage {
    pub items: Vec<NotificationView>,
    pub next_cursor: Option<String>,
}

fn invalid(message: &str) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidData, message.to_owned()).into()
}
