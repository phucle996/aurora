use crate::repo::{ActivityCategory, ActivityEvent, ActivityPage, PageRequest, TimelineRepo};
use crate::service::ports::AppError;
use std::sync::Arc;
use uuid::Uuid;

// [COMMENT]: Service xử lý nghiệp vụ dòng sự kiện hoạt động (Activity Timeline)
#[derive(Clone)]
pub struct ActivityService {
    repo: Arc<dyn TimelineRepo>,
    max_page_size: usize,
    max_month_scan: usize,
}

impl ActivityService {
    pub fn new(repo: Arc<dyn TimelineRepo>, max_page_size: usize, max_month_scan: usize) -> Self {
        Self {
            repo,
            max_page_size,
            max_month_scan,
        }
    }

    // [COMMENT]: Validate và lưu trữ một activity event mới vào ScyllaDB
    pub async fn persist(&self, event: ActivityEvent) -> Result<(), AppError> {
        event.validate()?;
        self.repo.persist_activity(event).await
    }

    // [COMMENT]: Truy vấn danh sách hoạt động phân trang theo user_id và category
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
                ActivityCategory::parse(value)
                    .ok_or_else(|| invalid("activity category is invalid"))
            })
            .transpose()?;
        self.repo
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::repo::timeline::ActivityView;
    use crate::repo::{ActivityOutcome, ActorType};
    use chrono::Utc;
    use futures_util::future::BoxFuture;
    use std::sync::Mutex;

    type ListCall = (Uuid, Option<String>, usize, usize);

    #[derive(Default)]
    struct Repo {
        persisted: Mutex<Vec<ActivityEvent>>,
        listed: Mutex<Vec<ListCall>>,
    }

    impl TimelineRepo for Repo {
        fn persist_activity<'a>(
            &'a self,
            event: ActivityEvent,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async move {
                self.persisted.lock().expect("persisted").push(event);
                Ok(())
            })
        }

        fn list_activity<'a>(
            &'a self,
            user_id: Uuid,
            _cursor: Option<&'a str>,
            category: Option<ActivityCategory>,
            limit: usize,
            max_month_scan: usize,
        ) -> BoxFuture<'a, Result<ActivityPage, AppError>> {
            Box::pin(async move {
                self.listed.lock().expect("listed").push((
                    user_id,
                    category.map(|value| value.as_str().to_string()),
                    limit,
                    max_month_scan,
                ));
                Ok(ActivityPage {
                    items: Vec::<ActivityView>::new(),
                    next_cursor: None,
                })
            })
        }
    }

    fn event() -> ActivityEvent {
        ActivityEvent {
            event_id: Uuid::new_v4(),
            user_id: Uuid::new_v4(),
            category: ActivityCategory::Security,
            action: "session.login".to_string(),
            actor_type: ActorType::SelfUser,
            actor_id: None,
            outcome: ActivityOutcome::Succeeded,
            source_service: "acr".to_string(),
            resource_type: None,
            resource_id: None,
            operation_id: None,
            title: "Signed in".to_string(),
            summary: "Session established".to_string(),
            occurred_at: Utc::now(),
            metadata_json: "{}".to_string(),
            schema_version: 1,
            projection_version: 0,
        }
    }

    #[tokio::test]
    async fn persist_validates_before_repository_write() {
        let repo = Arc::new(Repo::default());
        let service = ActivityService::new(repo.clone(), 50, 24);
        let mut invalid = event();
        invalid.metadata_json = "not-json".to_string();

        assert!(service.persist(invalid).await.is_err());
        assert!(repo.persisted.lock().expect("persisted").is_empty());

        service.persist(event()).await.expect("valid activity");
        assert_eq!(repo.persisted.lock().expect("persisted").len(), 1);
    }

    #[tokio::test]
    async fn list_applies_owner_category_and_bounded_pagination() {
        let repo = Arc::new(Repo::default());
        let service = ActivityService::new(repo.clone(), 50, 24);
        let user_id = Uuid::new_v4();

        service
            .list(
                user_id,
                PageRequest {
                    cursor: None,
                    limit: Some(999),
                    category: Some("billing".to_string()),
                },
            )
            .await
            .expect("list");

        assert_eq!(
            repo.listed.lock().expect("listed").as_slice(),
            &[(user_id, Some("billing".to_string()), 50, 24)]
        );
    }

    #[tokio::test]
    async fn list_rejects_zero_limit_and_unknown_category_without_repository_call() {
        let repo = Arc::new(Repo::default());
        let service = ActivityService::new(repo.clone(), 50, 24);
        for request in [
            PageRequest {
                cursor: None,
                limit: Some(0),
                category: None,
            },
            PageRequest {
                cursor: None,
                limit: Some(10),
                category: Some("root".to_string()),
            },
        ] {
            assert!(service.list(Uuid::new_v4(), request).await.is_err());
        }
        assert!(repo.listed.lock().expect("listed").is_empty());
    }
}
