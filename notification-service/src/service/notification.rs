use crate::repo::{NotificationItem, NotificationPage, NotificationRepo, PageRequest};
use crate::service::ports::AppError;
use chrono::DateTime;
use std::sync::Arc;
use uuid::Uuid;

// [COMMENT]: Service xử lý nghiệp vụ thông báo (Notification Inbox)
#[derive(Clone)]
pub struct NotificationService {
    repo: Arc<dyn NotificationRepo>,
    max_page_size: usize,
    max_month_scan: usize,
}

impl NotificationService {
    pub fn new(
        repo: Arc<dyn NotificationRepo>,
        max_page_size: usize,
        max_month_scan: usize,
    ) -> Self {
        Self {
            repo,
            max_page_size,
            max_month_scan,
        }
    }

    // [COMMENT]: Validate và lưu trữ một thông báo mới vào ScyllaDB
    pub async fn persist(&self, item: NotificationItem) -> Result<(), AppError> {
        item.validate()?;
        self.repo.persist_notification(item).await
    }

    // [COMMENT]: Truy vấn danh sách thông báo phân trang theo user_id
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
        self.repo
            .list_notifications(
                user_id,
                request.cursor.as_deref(),
                limit,
                self.max_month_scan,
            )
            .await
    }

    // [COMMENT]: Đánh dấu 1 thông báo cụ thể là đã đọc
    pub async fn mark_read(
        &self,
        user_id: Uuid,
        created_at: DateTime<chrono::Utc>,
        notification_id: Uuid,
    ) -> Result<(), AppError> {
        let month_bucket = created_at.format("%Y-%m").to_string();
        self.repo
            .mark_notification_read(user_id, &month_bucket, created_at, notification_id)
            .await
    }

    // [COMMENT]: Đánh dấu toàn bộ thông báo của người dùng là đã đọc
    pub async fn mark_all_read(&self, user_id: Uuid) -> Result<(), AppError> {
        self.repo.mark_all_notifications_read(user_id).await
    }
}

fn invalid(message: &str) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidInput, message.to_owned()).into()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::repo::notification::NotificationView;
    use chrono::{TimeZone, Utc};
    use futures_util::future::BoxFuture;
    use std::sync::Mutex;

    type ReadCall = (Uuid, String, DateTime<Utc>, Uuid);

    #[derive(Default)]
    struct Repo {
        persisted: Mutex<Vec<NotificationItem>>,
        listed: Mutex<Vec<(Uuid, usize, usize)>>,
        reads: Mutex<Vec<ReadCall>>,
        read_all: Mutex<Vec<Uuid>>,
    }

    impl NotificationRepo for Repo {
        fn persist_notification<'a>(
            &'a self,
            item: NotificationItem,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async move {
                self.persisted.lock().expect("persisted").push(item);
                Ok(())
            })
        }

        fn list_notifications<'a>(
            &'a self,
            user_id: Uuid,
            _cursor: Option<&'a str>,
            limit: usize,
            max_month_scan: usize,
        ) -> BoxFuture<'a, Result<NotificationPage, AppError>> {
            Box::pin(async move {
                self.listed
                    .lock()
                    .expect("listed")
                    .push((user_id, limit, max_month_scan));
                Ok(NotificationPage {
                    items: Vec::<NotificationView>::new(),
                    next_cursor: None,
                })
            })
        }

        fn mark_notification_read<'a>(
            &'a self,
            user_id: Uuid,
            month_bucket: &'a str,
            created_at: DateTime<Utc>,
            notification_id: Uuid,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async move {
                self.reads.lock().expect("reads").push((
                    user_id,
                    month_bucket.to_string(),
                    created_at,
                    notification_id,
                ));
                Ok(())
            })
        }

        fn mark_all_notifications_read<'a>(
            &'a self,
            user_id: Uuid,
        ) -> BoxFuture<'a, Result<(), AppError>> {
            Box::pin(async move {
                self.read_all.lock().expect("read all").push(user_id);
                Ok(())
            })
        }
    }

    fn item() -> NotificationItem {
        NotificationItem {
            notification_id: Uuid::new_v4(),
            activity_event_id: Uuid::new_v4(),
            user_id: Uuid::new_v4(),
            severity: "info".to_string(),
            title: "Done".to_string(),
            message: "Operation completed".to_string(),
            operation: "storage.bucket.create".to_string(),
            resource_id: None,
            created_at: Utc::now(),
            projection_version: 0,
        }
    }

    #[tokio::test]
    async fn persist_rejects_oversized_item_before_repository_write() {
        let repo = Arc::new(Repo::default());
        let service = NotificationService::new(repo.clone(), 50, 12);
        let mut invalid = item();
        invalid.message = "x".repeat(4_097);

        assert!(service.persist(invalid).await.is_err());
        assert!(repo.persisted.lock().expect("persisted").is_empty());
    }

    #[tokio::test]
    async fn list_clamps_limit_and_rejects_zero() {
        let repo = Arc::new(Repo::default());
        let service = NotificationService::new(repo.clone(), 50, 12);
        let user_id = Uuid::new_v4();
        service
            .list(
                user_id,
                PageRequest {
                    cursor: None,
                    limit: Some(1000),
                    category: None,
                },
            )
            .await
            .expect("list");
        assert_eq!(
            repo.listed.lock().expect("listed").as_slice(),
            &[(user_id, 50, 12)]
        );

        assert!(service
            .list(
                user_id,
                PageRequest {
                    cursor: None,
                    limit: Some(0),
                    category: None,
                },
            )
            .await
            .is_err());
        assert_eq!(repo.listed.lock().expect("listed").len(), 1);
    }

    #[tokio::test]
    async fn mark_read_uses_utc_month_bucket_and_owner() {
        let repo = Arc::new(Repo::default());
        let service = NotificationService::new(repo.clone(), 50, 12);
        let user_id = Uuid::new_v4();
        let notification_id = Uuid::new_v4();
        let created_at = Utc.with_ymd_and_hms(2025, 12, 31, 23, 59, 59).unwrap();

        service
            .mark_read(user_id, created_at, notification_id)
            .await
            .expect("mark read");
        service.mark_all_read(user_id).await.expect("mark all");

        let reads = repo.reads.lock().expect("reads");
        assert_eq!(reads[0].0, user_id);
        assert_eq!(reads[0].1, "2025-12");
        assert_eq!(reads[0].3, notification_id);
        assert_eq!(
            repo.read_all.lock().expect("read all").as_slice(),
            &[user_id]
        );
    }
}
