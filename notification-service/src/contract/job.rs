pub const JOB_NOTIFICATION_STREAM: &str = "stream:{job_notifications}";
pub const JOB_NOTIFICATION_DLQ: &str = "stream:{job_notifications_quarantine}";
pub const JOB_NOTIFICATION_CONSUMER_GROUP: &str = "notification-job-v1";

pub mod proto {
    tonic::include_proto!("job");
}

pub fn valid_event(event: &proto::JobNotificationEvent) -> bool {
    (uuid::Uuid::parse_str(&event.notification_id).is_ok())
        && uuid::Uuid::parse_str(&event.job_id).is_ok()
        && uuid::Uuid::parse_str(&event.user_id).is_ok()
        && chrono::DateTime::from_timestamp(event.created_at, 0)
            .is_some_and(|value| value <= chrono::Utc::now() + chrono::Duration::minutes(5))
        && matches!(event.status.as_str(), "PROCESSING" | "SUCCESS" | "FAILED")
        && !event.event_type.is_empty()
        && event.event_type.len() <= 128
        && event.title.len() <= 256
        && event.message.len() <= 4_096
        && event.resource_id.len() <= 256
        && (event.event_type != "managed_service.instance.execute"
            || (event.status_version > 0
                && ((event.status == "PROCESSING"
                    && !event.status_version.is_multiple_of(2))
                    || (event.status != "PROCESSING"
                        && event.status_version.is_multiple_of(2)))))
        && crate::observability::tracing::OtelTracer::is_valid_propagation_context(
            &event.trace_parent,
            &event.trace_state,
        )
}

pub fn parse_notification_id(event: &proto::JobNotificationEvent) -> Result<String, uuid::Error> {
    uuid::Uuid::parse_str(&event.notification_id).map(|value| value.to_string())
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
            status_version: 0,
        }
    }

    #[test]
    fn rolling_event_without_trace_context_is_valid() {
        assert!(valid_event(&event()));
    }

    #[test]
    fn missing_notification_id_is_rejected() {
        let mut candidate = event();
        candidate.notification_id.clear();
        assert!(!valid_event(&candidate));
        assert!(parse_notification_id(&candidate).is_err());
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
