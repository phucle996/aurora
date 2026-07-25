pub const JOB_NOTIFICATION_STREAM: &str = "stream:{job_notifications}";
pub const CONSUMER_GROUP: &str = "notification-service-v1";

const NOTIFICATION_NAMESPACE: uuid::Uuid = uuid::Uuid::from_bytes([
    0x43, 0xa7, 0xde, 0x2c, 0x59, 0x85, 0x50, 0x67, 0xa0, 0x16, 0x0b, 0x63, 0x8d, 0xd8, 0xe9, 0x71,
]);

pub mod proto {
    tonic::include_proto!("job");
}

pub fn valid_event(event: &proto::JobNotificationEvent) -> bool {
    (event.notification_id.is_empty() || uuid::Uuid::parse_str(&event.notification_id).is_ok())
        && uuid::Uuid::parse_str(&event.job_id).is_ok()
        && uuid::Uuid::parse_str(&event.user_id).is_ok()
        && matches!(event.status.as_str(), "PROCESSING" | "SUCCESS" | "FAILED")
        && !event.event_type.is_empty()
        && event.event_type.len() <= 128
        && event.title.len() <= 256
        && event.message.len() <= 4_096
        && event.resource_id.len() <= 256
        && crate::observability::tracing::OtelTracer::is_valid_propagation_context(
            &event.trace_parent,
            &event.trace_state,
        )
}

pub fn effective_notification_id(
    event: &proto::JobNotificationEvent,
) -> Result<String, uuid::Error> {
    if !event.notification_id.is_empty() {
        return uuid::Uuid::parse_str(&event.notification_id).map(|value| value.to_string());
    }

    // Old JO producers may omit field 13. Deriving the identity from the
    // immutable status tuple preserves idempotency during rolling upgrades.
    let job_id = uuid::Uuid::parse_str(&event.job_id)?;
    let identity = format!(
        "{}:{}:{}:{}",
        job_id, event.job_version, event.attempt, event.status
    );
    Ok(uuid::Uuid::new_v5(&NOTIFICATION_NAMESPACE, identity.as_bytes()).to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn event() -> proto::JobNotificationEvent {
        proto::JobNotificationEvent {
            job_id: uuid::Uuid::new_v4().to_string(),
            user_id: uuid::Uuid::new_v4().to_string(),
            status: "SUCCESS".to_string(),
            event_type: "storage.bucket.create".to_string(),
            title: "Bucket Created".to_string(),
            message: String::new(),
            created_at: chrono::Utc::now().timestamp(),
            trace_parent: String::new(),
            trace_state: String::new(),
            resource_id: "bucket-1".to_string(),
            job_version: 1,
            attempt: 0,
            notification_id: uuid::Uuid::new_v4().to_string(),
        }
    }

    #[test]
    fn rolling_event_without_trace_context_is_valid() {
        assert!(valid_event(&event()));
    }

    #[test]
    fn missing_notification_id_gets_a_stable_fallback() {
        let mut candidate = event();
        candidate.notification_id.clear();
        let first = effective_notification_id(&candidate).unwrap();
        let replay = effective_notification_id(&candidate).unwrap();
        assert_eq!(first, replay);
        assert!(valid_event(&candidate));
    }

    #[test]
    fn partial_trace_context_and_oversized_message_are_rejected() {
        let mut candidate = event();
        candidate.trace_state = "vendor=value".to_string();
        assert!(!valid_event(&candidate));

        candidate.trace_state.clear();
        candidate.message = "x".repeat(4_097);
        assert!(!valid_event(&candidate));
    }
}
