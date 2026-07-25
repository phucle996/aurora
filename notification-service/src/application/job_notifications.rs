use crate::application::ports::{AppError, RealtimePublisher};
use crate::contract::job::{effective_notification_id, proto::JobNotificationEvent};
use crate::contract::realtime::job_channel;
use crate::observability::{logger::Logger, metrics::MetricsManager};
use chrono::DateTime;
use std::sync::Arc;

/// Durable job notifications have an isolated service and channel so runtime
/// telemetry cannot consume the delivery budget for user-visible job state.
pub struct JobNotificationService {
    publisher: Arc<dyn RealtimePublisher>,
}

impl JobNotificationService {
    pub fn new(publisher: Arc<dyn RealtimePublisher>) -> Self {
        Self { publisher }
    }

    pub async fn dispatch(&self, event: JobNotificationEvent) -> Result<(), AppError> {
        let created_at = DateTime::from_timestamp(event.created_at, 0)
            .ok_or_else(|| boxed_error("invalid notification timestamp"))?
            .to_rfc3339();
        let notification_id = effective_notification_id(&event)
            .map_err(|error| boxed_error(&format!("invalid notification identity: {error}")))?;
        let client_payload = serde_json::json!({
            "status": event.status,
            "title": event.title,
            "message": event.message,
            "created_at": created_at,
            "transaction_id": event.job_id,
            "operation": event.event_type,
            "resource_id": event.resource_id,
            "job_version": event.job_version,
            "attempt": event.attempt,
            "notification_id": notification_id,
            "event_type": "job.notification",
            "stream": "job",
        });
        validate_user_id(&event.user_id, "job notification")?;
        self.publisher
            .publish(&job_channel(&event.user_id), client_payload)
            .await?;

        let age = chrono::Utc::now()
            .signed_duration_since(
                DateTime::parse_from_rfc3339(&created_at)
                    .map_err(|error| boxed_error(&format!("invalid notification time: {error}")))?
                    .with_timezone(&chrono::Utc),
            )
            .max(chrono::Duration::zero());
        MetricsManager::record_delivered_lag("success", age.to_std().unwrap_or_default());
        Logger::sys_info(
            "notification.job_published",
            "Published job notification to the authenticated user's job channel",
        );
        Ok(())
    }
}

fn validate_user_id(user_id: &str, context: &str) -> Result<(), AppError> {
    if uuid::Uuid::parse_str(user_id).is_err() {
        return Err(boxed_error(&format!("{context} user id is not a UUID")));
    }
    Ok(())
}

fn boxed_error(message: &str) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidData, message.to_owned()).into()
}
