use super::event::{NotificationItem, NotificationPage, PageRequest};
use super::store::TimelineStore;
use crate::application::ports::AppError;
use chrono::DateTime;
use std::sync::Arc;
use uuid::Uuid;

#[derive(Clone)]
pub struct InboxService {
    store: Arc<dyn TimelineStore>,
    max_page_size: usize,
    max_month_scan: usize,
}

impl InboxService {
    pub fn new(store: Arc<dyn TimelineStore>, max_page_size: usize, max_month_scan: usize) -> Self {
        Self {
            store,
            max_page_size,
            max_month_scan,
        }
    }

    pub async fn persist(&self, item: NotificationItem) -> Result<(), AppError> {
        item.validate()?;
        self.store.persist_notification(item).await
    }

    pub async fn list(
        &self,
        user_id: Uuid,
        request: PageRequest,
    ) -> Result<NotificationPage, AppError> {
        let limit = request
            .limit
            .unwrap_or(self.max_page_size)
            .min(self.max_page_size);
        if limit == 0 {
            return Err(invalid("notification page size must be positive"));
        }
        self.store
            .list_notifications(
                user_id,
                request.cursor.as_deref(),
                limit,
                self.max_month_scan,
            )
            .await
    }

    pub async fn mark_read(
        &self,
        user_id: Uuid,
        created_at: DateTime<chrono::Utc>,
        notification_id: Uuid,
    ) -> Result<(), AppError> {
        let month_bucket = created_at.format("%Y-%m").to_string();
        self.store
            .mark_notification_read(user_id, &month_bucket, created_at, notification_id)
            .await
    }

    pub async fn mark_all_read(&self, user_id: Uuid) -> Result<(), AppError> {
        self.store.mark_all_notifications_read(user_id).await
    }
}

fn invalid(message: &str) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidInput, message.to_owned()).into()
}
