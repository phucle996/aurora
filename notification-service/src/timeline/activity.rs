use super::event::{ActivityEvent, ActivityPage, PageRequest};
use super::store::TimelineStore;
use crate::application::ports::AppError;
use std::sync::Arc;
use uuid::Uuid;

#[derive(Clone)]
pub struct ActivityService {
    store: Arc<dyn TimelineStore>,
    max_page_size: usize,
    max_month_scan: usize,
}

impl ActivityService {
    pub fn new(store: Arc<dyn TimelineStore>, max_page_size: usize, max_month_scan: usize) -> Self {
        Self {
            store,
            max_page_size,
            max_month_scan,
        }
    }

    pub async fn persist(&self, event: ActivityEvent) -> Result<(), AppError> {
        event.validate()?;
        self.store.persist_activity(event).await
    }

    pub async fn list(
        &self,
        user_id: Uuid,
        request: PageRequest,
    ) -> Result<ActivityPage, AppError> {
        let limit = request
            .limit
            .unwrap_or(self.max_page_size)
            .min(self.max_page_size);
        if limit == 0 {
            return Err(invalid("activity page size must be positive"));
        }
        let category = request
            .category
            .as_deref()
            .map(|value| {
                super::event::ActivityCategory::parse(value)
                    .ok_or_else(|| invalid("activity category is invalid"))
            })
            .transpose()?;
        self.store
            .list_activity(
                user_id,
                request.cursor.as_deref(),
                category,
                limit,
                self.max_month_scan,
            )
            .await
    }
}

fn invalid(message: &str) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidInput, message.to_owned()).into()
}
