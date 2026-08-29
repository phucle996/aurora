use crate::observability::{logger::Logger, metrics::MetricsManager};
use crate::repo::{ActivityCategory, ActivityEvent, ActivityOutcome, ActorType, NotificationItem};
use crate::service::ports::notification_channel;
use crate::service::ports::{AppError, RealtimePublisher};
use crate::service::{ActivityService, NotificationService};
use crate::transport::stream::job_stream::{parse_notification_id, JobNotificationEvent};
use chrono::DateTime;
use std::sync::Arc;
use uuid::Uuid;

/// Durable job notifications have an isolated service and channel so runtime
/// telemetry cannot consume the delivery budget for user-visible job state.
pub struct JobNotificationService {
    publisher: Arc<dyn RealtimePublisher>,
    activities: Arc<ActivityService>,
    inbox: Arc<NotificationService>,
}

impl JobNotificationService {
    pub fn new(
        publisher: Arc<dyn RealtimePublisher>,
        activities: Arc<ActivityService>,
        inbox: Arc<NotificationService>,
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
            .map_err(|_| boxed_error("notification identity must be a valid UUID"))?;
        let activity_event_id = Uuid::parse_str(&event.job_id)
            .map_err(|_| boxed_error("job_id must be a valid UUID"))?;
        let user_id = Uuid::parse_str(&event.user_id)
            .map_err(|_| boxed_error("user_id must be a valid UUID"))?;

        let projection_version = if event.event_type == "managed_service.instance.execute" {
            i64::try_from(event.status_version)
                .map_err(|_| boxed_error("status_version exceeds projection range"))?
        } else {
            i64::from(event.job_version)
        };

        let outcome = match event.status.to_ascii_uppercase().as_str() {
            "SUCCESS" => ActivityOutcome::Succeeded,
            "FAILED" => ActivityOutcome::Failed,
            "PROCESSING" | "RUNNING" => ActivityOutcome::Started,
            _ => ActivityOutcome::Succeeded,
        };

        let activity = ActivityEvent {
            event_id: activity_event_id,
            user_id,
            category: ActivityCategory::Resource,
            action: event.event_type.clone(),
            actor_type: ActorType::SelfUser,
            actor_id: Some(user_id),
            outcome,
            source_service: "controlplane".to_string(),
            resource_type: Some("job".to_string()),
            resource_id: if event.resource_id.is_empty() {
                None
            } else {
                Some(event.resource_id.clone())
            },
            operation_id: Some(event.job_id.clone()),
            title: event.title.clone(),
            summary: event.message.clone(),
            occurred_at: created_at,
            metadata_json: "{}".to_string(),
            schema_version: 1,
            projection_version,
        };

        let notification = NotificationItem {
            notification_id,
            activity_event_id,
            user_id,
            severity: "info".to_string(),
            title: event.title.clone(),
            message: event.message.clone(),
            operation: event.event_type.clone(),
            resource_id: if event.resource_id.is_empty() {
                None
            } else {
                Some(event.resource_id.clone())
            },
            created_at,
            projection_version,
        };

        self.activities.persist(activity).await?;
        self.inbox.persist(notification).await?;

        let channel = notification_channel(&user_id.to_string());
        let payload = serde_json::json!({
            "notification_id": notification_id.to_string(),
            "activity_event_id": activity_event_id.to_string(),
            "job_id": event.job_id,
            "operation_id": event.job_id,
            "user_id": event.user_id,
            "event_type": event.event_type,
            "status": event.status,
            "title": event.title,
            "message": event.message,
            "resource_id": event.resource_id,
            "created_at": created_at.to_rfc3339(),
            "job_version": event.job_version,
            "status_version": event.status_version,
        });

        match self.publisher.publish(&channel, payload).await {
            Ok(()) => {
                MetricsManager::record_centrifugo_publish("success");
            }
            Err(error) => {
                MetricsManager::record_centrifugo_publish("failed");
                Logger::sys_error(
                    "centrifugo.publish_failed",
                    "Failed to publish durable job notification to Centrifugo; persisted state remains source of truth",
                    &error.to_string(),
                );
            }
        }
        Ok(())
    }
}

fn boxed_error(message: &str) -> AppError {
    Box::new(std::io::Error::new(
        std::io::ErrorKind::InvalidData,
        message.to_owned(),
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::repo::notification::NotificationView;
    use crate::repo::timeline::ActivityView;
    use crate::repo::{
        ActivityCategory, ActivityEvent, ActivityPage, NotificationItem, NotificationPage,
        NotificationRepo, TimelineRepo,
    };
    use chrono::Utc;
    use futures_util::future::BoxFuture;
    use serde_json::Value;
    use std::sync::Mutex;

    #[derive(Clone, Debug)]
    enum RecordedWrite {
        Activity(ActivityEvent),
        Inbox(NotificationItem),
        Realtime(String, Value),
    }

    #[derive(Clone, Copy, Debug, Eq, PartialEq)]
    enum FailAt {
        Never,
        Activity,
        Inbox,
        Realtime,
    }

    struct RecordingStore {
        writes: Arc<Mutex<Vec<RecordedWrite>>>,
        fail_at: FailAt,
    }

    impl TimelineRepo for RecordingStore {
        fn persist_activity<'a>(
            &'a self,
            event: ActivityEvent,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async move {
                if self.fail_at == FailAt::Activity {
                    return Err(test_error("activity unavailable"));
                }
                self.writes
                    .lock()
                    .expect("recording store lock")
                    .push(RecordedWrite::Activity(event));
                Ok(())
            })
        }

        fn list_activity<'a>(
            &'a self,
            _user_id: Uuid,
            _cursor: Option<&'a str>,
            _category: Option<ActivityCategory>,
            _limit: usize,
            _max_month_scan: usize,
        ) -> BoxFuture<'a, Result<ActivityPage, AppError>> {
            Box::pin(async {
                Ok(ActivityPage {
                    items: Vec::<ActivityView>::new(),
                    next_cursor: None,
                })
            })
        }
    }

    impl NotificationRepo for RecordingStore {
        fn persist_notification<'a>(
            &'a self,
            item: NotificationItem,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async move {
                if self.fail_at == FailAt::Inbox {
                    return Err(test_error("inbox unavailable"));
                }
                self.writes
                    .lock()
                    .expect("recording store lock")
                    .push(RecordedWrite::Inbox(item));
                Ok(())
            })
        }

        fn list_notifications<'a>(
            &'a self,
            _user_id: Uuid,
            _cursor: Option<&'a str>,
            _limit: usize,
            _max_month_scan: usize,
        ) -> BoxFuture<'a, Result<NotificationPage, AppError>> {
            Box::pin(async {
                Ok(NotificationPage {
                    items: Vec::<NotificationView>::new(),
                    next_cursor: None,
                })
            })
        }

        fn mark_notification_read<'a>(
            &'a self,
            _user_id: Uuid,
            _month_bucket: &'a str,
            _created_at: DateTime<Utc>,
            _notification_id: Uuid,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async { Ok(()) })
        }

        fn mark_all_notifications_read<'a>(
            &'a self,
            _user_id: Uuid,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async { Ok(()) })
        }
    }

    struct RecordingPublisher {
        writes: Arc<Mutex<Vec<RecordedWrite>>>,
        fail: bool,
    }

    impl RealtimePublisher for RecordingPublisher {
        fn publish<'a>(
            &'a self,
            channel: &'a str,
            data: Value,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async move {
                if self.fail {
                    return Err(test_error("centrifugo unavailable"));
                }
                self.writes
                    .lock()
                    .expect("recording publisher lock")
                    .push(RecordedWrite::Realtime(channel.to_string(), data));
                Ok(())
            })
        }
    }

    fn test_error(message: &str) -> AppError {
        std::io::Error::other(message.to_owned()).into()
    }

    fn event(status: &str, event_type: &str) -> JobNotificationEvent {
        JobNotificationEvent {
            job_id: Uuid::new_v4().to_string(),
            user_id: Uuid::new_v4().to_string(),
            status: status.to_string(),
            event_type: event_type.to_string(),
            title: "Operation update".to_string(),
            message: "Operation state changed".to_string(),
            created_at: Utc::now().timestamp(),
            trace_parent: String::new(),
            trace_state: String::new(),
            resource_id: "resource-1".to_string(),
            job_version: 7,
            attempt: 2,
            notification_id: Uuid::new_v4().to_string(),
            status_version: 12,
        }
    }

    fn service(writes: Arc<Mutex<Vec<RecordedWrite>>>, fail_at: FailAt) -> JobNotificationService {
        let store = Arc::new(RecordingStore {
            writes: writes.clone(),
            fail_at,
        });
        let publisher: Arc<dyn RealtimePublisher> = Arc::new(RecordingPublisher {
            writes,
            fail: fail_at == FailAt::Realtime,
        });
        JobNotificationService::new(
            publisher,
            Arc::new(ActivityService::new(store.clone(), 50, 12)),
            Arc::new(NotificationService::new(store, 50, 12)),
        )
    }

    #[tokio::test]
    async fn durable_projections_are_written_before_realtime() {
        let writes = Arc::new(Mutex::new(Vec::new()));
        let service = service(writes.clone(), FailAt::Never);
        let event = event("SUCCESS", "compute.instance.delete");
        let user_id = Uuid::parse_str(&event.user_id).expect("user UUID");
        let notification_id = event.notification_id.clone();

        service.dispatch(event).await.expect("dispatch job");

        let records = writes.lock().expect("recording lock").clone();
        assert_eq!(records.len(), 3);
        assert!(matches!(
            records[0],
            RecordedWrite::Activity(ref activity)
                if activity.user_id == user_id && activity.action == "compute.instance.delete"
        ));
        assert!(matches!(
            records[1],
            RecordedWrite::Inbox(ref item)
                if item.user_id == user_id && item.notification_id.to_string() == notification_id
        ));
        assert!(matches!(
            records[2],
            RecordedWrite::Realtime(ref channel, ref data)
                if channel == &format!("notifications:{user_id}")
                    && data["event_type"] == "compute.instance.delete"
        ));
    }

    #[tokio::test]
    async fn activity_failure_prevents_inbox_and_realtime_delivery() {
        let writes = Arc::new(Mutex::new(Vec::new()));
        let result = service(writes.clone(), FailAt::Activity)
            .dispatch(event("SUCCESS", "storage.bucket.create"))
            .await;

        assert!(result.is_err());
        assert!(writes.lock().expect("recording lock").is_empty());
    }

    #[tokio::test]
    async fn inbox_failure_leaves_activity_for_idempotent_retry_and_skips_realtime() {
        let writes = Arc::new(Mutex::new(Vec::new()));
        let result = service(writes.clone(), FailAt::Inbox)
            .dispatch(event("SUCCESS", "storage.bucket.create"))
            .await;

        assert!(result.is_err());
        let records = writes.lock().expect("recording lock").clone();
        assert_eq!(records.len(), 1);
        assert!(matches!(records[0], RecordedWrite::Activity(_)));
    }

    #[tokio::test]
    async fn realtime_failure_does_not_undo_durable_delivery() {
        let writes = Arc::new(Mutex::new(Vec::new()));
        let result = service(writes.clone(), FailAt::Realtime)
            .dispatch(event("SUCCESS", "storage.bucket.create"))
            .await;

        assert!(result.is_ok());
        let records = writes.lock().expect("recording lock").clone();
        assert_eq!(records.len(), 2);
        assert!(matches!(records[0], RecordedWrite::Activity(_)));
        assert!(matches!(records[1], RecordedWrite::Inbox(_)));
    }

    #[tokio::test]
    async fn managed_processing_projects_started_outcome() {
        let writes = Arc::new(Mutex::new(Vec::new()));
        let service = service(writes.clone(), FailAt::Never);
        let event = event("PROCESSING", "managed_service.instance.execute");
        service.dispatch(event).await.expect("dispatch processing");

        let records = writes.lock().expect("recording lock").clone();
        let RecordedWrite::Activity(activity) = &records[0] else {
            panic!("first write must be activity");
        };
        assert_eq!(activity.outcome, ActivityOutcome::Started);
    }

    #[tokio::test]
    async fn managed_projections_use_status_version_for_lww_fence() {
        let writes = Arc::new(Mutex::new(Vec::new()));
        let service = service(writes.clone(), FailAt::Never);
        let event = event("SUCCESS", "managed_service.instance.execute");
        let expected_version = i64::try_from(event.status_version).expect("bounded status version");

        service.dispatch(event).await.expect("dispatch terminal");

        let records = writes.lock().expect("recording lock").clone();
        let RecordedWrite::Activity(activity) = &records[0] else {
            panic!("first write must be activity");
        };
        assert_eq!(activity.projection_version, expected_version);
        let RecordedWrite::Inbox(item) = &records[1] else {
            panic!("second write must be inbox");
        };
        assert_eq!(item.projection_version, expected_version);
    }

    #[tokio::test]
    async fn managed_realtime_payload_carries_status_version() {
        let writes = Arc::new(Mutex::new(Vec::new()));
        let service = service(writes.clone(), FailAt::Never);
        let event = event("FAILED", "managed_service.instance.execute");
        let expected_status_version = event.status_version;
        service.dispatch(event).await.expect("dispatch terminal");

        let records = writes.lock().expect("recording lock").clone();
        let RecordedWrite::Realtime(_, payload) = &records[2] else {
            panic!("third write must be realtime");
        };
        assert_eq!(payload["status_version"], expected_status_version);
    }

    #[tokio::test]
    async fn managed_realtime_payload_carries_operation_id() {
        let writes = Arc::new(Mutex::new(Vec::new()));
        let service = service(writes.clone(), FailAt::Never);
        let event = event("FAILED", "managed_service.instance.execute");
        let expected_operation_id = event.job_id.clone();

        service.dispatch(event).await.expect("dispatch terminal");

        let records = writes.lock().expect("recording lock").clone();
        let RecordedWrite::Realtime(_, payload) = &records[2] else {
            panic!("third write must be realtime");
        };
        assert_eq!(payload["operation_id"], expected_operation_id);
    }
}
