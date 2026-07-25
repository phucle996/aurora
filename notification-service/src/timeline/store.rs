use super::event::{
    ActivityCategory, ActivityEvent, ActivityPage, NotificationItem, NotificationPage,
};
use crate::application::ports::AppError;
use chrono::{DateTime, Utc};
use futures_util::future::BoxFuture;
use uuid::Uuid;

pub trait TimelineStore: Send + Sync {
    fn persist_activity<'a>(&'a self, event: ActivityEvent) -> BoxFuture<'a, Result<(), AppError>>;
    fn persist_notification<'a>(
        &'a self,
        item: NotificationItem,
    ) -> BoxFuture<'a, Result<(), AppError>>;
    fn list_activity<'a>(
        &'a self,
        user_id: Uuid,
        cursor: Option<&'a str>,
        category: Option<ActivityCategory>,
        limit: usize,
        max_month_scan: usize,
    ) -> BoxFuture<'a, Result<ActivityPage, AppError>>;
    fn list_notifications<'a>(
        &'a self,
        user_id: Uuid,
        cursor: Option<&'a str>,
        limit: usize,
        max_month_scan: usize,
    ) -> BoxFuture<'a, Result<NotificationPage, AppError>>;
    fn mark_notification_read<'a>(
        &'a self,
        user_id: Uuid,
        month_bucket: &'a str,
        created_at: DateTime<Utc>,
        notification_id: Uuid,
    ) -> BoxFuture<'a, Result<(), AppError>>;
    fn mark_all_notifications_read<'a>(
        &'a self,
        user_id: Uuid,
    ) -> BoxFuture<'a, Result<(), AppError>>;
}
