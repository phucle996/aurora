use crate::application::ports::AppError;
use crate::observability::tracing::OtelTracer;
use crate::timeline::event::{ActivityCategory, ActivityEvent, ActivityOutcome, ActorType};
use chrono::DateTime;
use uuid::Uuid;

pub const USER_ACTIVITY_STREAM: &str = "stream:{user_activity}";
pub const USER_ACTIVITY_DLQ: &str = "stream:{user_activity_quarantine}";
pub const USER_ACTIVITY_CONSUMER_GROUP: &str = "notification-activity-v1";

pub mod proto {
    tonic::include_proto!("activity");
}

pub fn decode(event: proto::UserActivityEvent) -> Result<ActivityEvent, AppError> {
    let actor_id = optional_uuid(&event.actor_id, "activity actor id")?;
    let occurred_at = DateTime::from_timestamp(event.occurred_at, 0)
        .ok_or_else(|| invalid("activity timestamp is invalid"))?;
    if occurred_at > chrono::Utc::now() + chrono::Duration::minutes(5) {
        return Err(invalid("activity timestamp is too far in the future"));
    }
    if event.resource_type.len() > 128
        || event.resource_id.len() > 256
        || event.operation_id.len() > 256
    {
        return Err(invalid("activity routing metadata is too large"));
    }
    if !OtelTracer::is_valid_propagation_context(&event.trace_parent, &event.trace_state) {
        return Err(invalid("activity trace context is invalid"));
    }

    let activity = ActivityEvent {
        event_id: Uuid::parse_str(&event.event_id)
            .map_err(|_| invalid("activity event id is not a UUID"))?,
        user_id: Uuid::parse_str(&event.user_id)
            .map_err(|_| invalid("activity user id is not a UUID"))?,
        category: ActivityCategory::parse(&event.category)
            .ok_or_else(|| invalid("activity category is invalid"))?,
        action: event.action,
        actor_type: ActorType::parse(&event.actor_type)
            .ok_or_else(|| invalid("activity actor type is invalid"))?,
        actor_id,
        outcome: ActivityOutcome::parse(&event.outcome)
            .ok_or_else(|| invalid("activity outcome is invalid"))?,
        source_service: event.source_service,
        resource_type: optional_text(event.resource_type),
        resource_id: optional_text(event.resource_id),
        operation_id: optional_text(event.operation_id),
        title: event.title,
        summary: event.summary,
        occurred_at,
        metadata_json: event.metadata_json,
        schema_version: event.schema_version,
    };
    activity.validate()?;
    Ok(activity)
}

fn optional_uuid(value: &str, field: &str) -> Result<Option<Uuid>, AppError> {
    if value.is_empty() {
        return Ok(None);
    }
    Uuid::parse_str(value)
        .map(Some)
        .map_err(|_| invalid(&format!("{field} is not a UUID")))
}

fn optional_text(value: String) -> Option<String> {
    (!value.is_empty()).then_some(value)
}

fn invalid(message: &str) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidData, message.to_owned()).into()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_contract_maps_to_timeline_event() {
        let user_id = Uuid::new_v4();
        let event = proto::UserActivityEvent {
            event_id: Uuid::new_v4().to_string(),
            user_id: user_id.to_string(),
            category: "security".to_string(),
            action: "session.login".to_string(),
            actor_type: "self".to_string(),
            actor_id: user_id.to_string(),
            outcome: "succeeded".to_string(),
            source_service: "acr".to_string(),
            resource_type: String::new(),
            resource_id: String::new(),
            operation_id: Uuid::new_v4().to_string(),
            title: "Signed in".to_string(),
            summary: "A new session was created".to_string(),
            occurred_at: chrono::Utc::now().timestamp(),
            metadata_json: "{}".to_string(),
            schema_version: 1,
            trace_parent: String::new(),
            trace_state: String::new(),
        };

        let decoded = decode(event).unwrap();
        assert_eq!(decoded.user_id, user_id);
        assert_eq!(decoded.category, ActivityCategory::Security);
    }
}
