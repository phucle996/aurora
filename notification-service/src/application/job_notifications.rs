use crate::application::ports::{AppError, RealtimePublisher};
use crate::contract::job::{parse_notification_id, proto::JobNotificationEvent};
use crate::contract::realtime::notification_channel;
use crate::observability::{logger::Logger, metrics::MetricsManager};
use crate::timeline::activity::ActivityService;
use crate::timeline::event::{
    ActivityCategory, ActivityEvent, ActivityOutcome, ActorType, NotificationItem,
};
use crate::timeline::inbox::InboxService;
use chrono::{DateTime, Utc};
use std::sync::Arc;
use uuid::Uuid;

/// Durable job notifications have an isolated service and channel so runtime
/// telemetry cannot consume the delivery budget for user-visible job state.
pub struct JobNotificationService {
    publisher: Arc<dyn RealtimePublisher>,
    activities: Arc<ActivityService>,
    inbox: Arc<InboxService>,
}

impl JobNotificationService {
    pub fn new(
        publisher: Arc<dyn RealtimePublisher>,
        activities: Arc<ActivityService>,
        inbox: Arc<InboxService>,
    ) -> Self {
        Self {
            publisher,
            activities,
            inbox,
        }
    }

    pub async fn dispatch(&self, event: JobNotificationEvent) -> Result<(), AppError> {
        let created_at = DateTime::from_timestamp(event.created_at, 0)
            .ok_or_else(|| boxed_error("invalid notification timestamp"))?;
        let notification_identity = parse_notification_id(&event)
            .map_err(|error| boxed_error(&format!("invalid notification identity: {error}")))?;
        let notification_id = Uuid::parse_str(&notification_identity)
            .map_err(|error| boxed_error(&format!("invalid notification identity: {error}")))?;
        let user_id = Uuid::parse_str(&event.user_id)
            .map_err(|_| boxed_error("job notification user id is not a UUID"))?;
        let (outcome, severity) = match event.status.as_str() {
            "SUCCESS" => (ActivityOutcome::Succeeded, "success"),
            "FAILED" => (ActivityOutcome::Failed, "error"),
            "PROCESSING" => (ActivityOutcome::Started, "info"),
            _ => return Err(boxed_error("job notification status is invalid")),
        };

        let activity = ActivityEvent {
            event_id: notification_id,
            user_id,
            category: ActivityCategory::Resource,
            action: event.event_type.clone(),
            actor_type: ActorType::System,
            actor_id: None,
            outcome,
            source_service: "job-orchestrator".to_string(),
            resource_type: None,
            resource_id: (!event.resource_id.is_empty()).then(|| event.resource_id.clone()),
            operation_id: Some(event.job_id.clone()),
            title: event.title.clone(),
            summary: event.message.clone(),
            occurred_at: created_at,
            metadata_json: serde_json::json!({
                "job_version": event.job_version,
                "attempt": event.attempt,
            })
            .to_string(),
            schema_version: 1,
        };
        let notification = NotificationItem {
            notification_id,
            activity_event_id: notification_id,
            user_id,
            severity: severity.to_string(),
            title: event.title.clone(),
            message: event.message.clone(),
            operation: event.event_type.clone(),
            resource_id: (!event.resource_id.is_empty()).then(|| event.resource_id.clone()),
            created_at,
        };

        // Scylla is the durable user-history boundary. Realtime publication is
        // attempted only after both idempotent projections are visible.
        self.activities.persist(activity).await?;
        self.inbox.persist(notification).await?;
        let client_payload = serde_json::json!({
            "status": event.status,
            "title": event.title,
            "message": event.message,
            "created_at": created_at.to_rfc3339(),
            "transaction_id": event.job_id,
            "operation": event.event_type,
            "resource_id": event.resource_id,
            "job_version": event.job_version,
            "attempt": event.attempt,
            "notification_id": notification_id,
            "event_type": "job.notification",
            "stream": "notification",
        });
        self.publisher
            .publish(&notification_channel(&event.user_id), client_payload)
            .await?;

        let age = Utc::now()
            .signed_duration_since(created_at)
            .max(chrono::Duration::zero());
        MetricsManager::record_delivered_lag("success", age.to_std().unwrap_or_default());
        Logger::sys_info(
            "notification.job_published",
            "Persisted and published job notification to the authenticated user",
        );
        Ok(())
    }
}

fn boxed_error(message: &str) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidData, message.to_owned()).into()
}
