use super::*;
use crate::timeline::event::{ActivityPage, ActivityView, NotificationPage, NotificationView};
use crate::timeline::store::TimelineStore;
use futures_util::future::BoxFuture;
use serde_json::Value;
use std::sync::Mutex;

#[derive(Debug)]
enum RecordedWrite {
    Activity(Uuid, String),
    Inbox(Uuid, String),
    Realtime(String, Value),
}

struct RecordingStore {
    writes: Arc<Mutex<Vec<RecordedWrite>>>,
}

impl TimelineStore for RecordingStore {
    fn persist_activity<'a>(&'a self, event: ActivityEvent) -> BoxFuture<'a, Result<(), AppError>> {
        Box::pin(async move {
            self.writes
                .lock()
                .expect("recording store lock")
                .push(RecordedWrite::Activity(event.user_id, event.action));
            Ok(())
        })
    }

    fn persist_notification<'a>(
        &'a self,
        item: NotificationItem,
    ) -> BoxFuture<'a, Result<(), AppError>> {
        Box::pin(async move {
            self.writes
                .lock()
                .expect("recording store lock")
                .push(RecordedWrite::Inbox(item.user_id, item.operation));
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
}

impl RealtimePublisher for RecordingPublisher {
    fn publish<'a>(&'a self, channel: &'a str, data: Value) -> BoxFuture<'a, Result<(), AppError>> {
        Box::pin(async move {
            self.writes
                .lock()
                .expect("recording publisher lock")
                .push(RecordedWrite::Realtime(channel.to_string(), data));
            Ok(())
        })
    }
}

#[tokio::test]
async fn vm_delete_persists_user_timeline_before_realtime() {
    let writes = Arc::new(Mutex::new(Vec::new()));
    let store: Arc<dyn TimelineStore> = Arc::new(RecordingStore {
        writes: writes.clone(),
    });
    let publisher: Arc<dyn RealtimePublisher> = Arc::new(RecordingPublisher {
        writes: writes.clone(),
    });
    let service = JobNotificationService::new(
        publisher,
        Arc::new(ActivityService::new(store.clone(), 50, 12)),
        Arc::new(InboxService::new(store, 50, 12)),
    );
    let user_id = Uuid::new_v4();
    let vm_id = Uuid::new_v4();
    service
        .dispatch(JobNotificationEvent {
            job_id: Uuid::new_v4().to_string(),
            user_id: user_id.to_string(),
            status: "SUCCESS".to_string(),
            event_type: "hypervisor.vm.delete".to_string(),
            title: "Virtual Machine Deleted".to_string(),
            message: "Hypervisor VM deletion completed".to_string(),
            created_at: Utc::now().timestamp(),
            trace_parent: String::new(),
            trace_state: String::new(),
            resource_id: vm_id.to_string(),
            job_version: 1,
            attempt: 0,
            notification_id: Uuid::new_v4().to_string(),
            status_version: 0,
        })
        .await
        .expect("dispatch VM delete notification");

    let recorded = writes.lock().expect("recorded writes lock");
    assert!(matches!(
        recorded.as_slice(),
        [
            RecordedWrite::Activity(activity_user, activity_action),
            RecordedWrite::Inbox(inbox_user, inbox_operation),
            RecordedWrite::Realtime(channel, payload),
        ] if *activity_user == user_id
            && activity_action == "hypervisor.vm.delete"
            && *inbox_user == user_id
            && inbox_operation == "hypervisor.vm.delete"
            && channel == &format!("notifications:{user_id}")
            && payload["resource_id"] == vm_id.to_string()
    ));
}
