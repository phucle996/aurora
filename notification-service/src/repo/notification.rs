use crate::config::TimelineConfig;
use crate::service::ports::AppError;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use chrono::{DateTime, Duration, TimeZone, Utc};
use futures_util::future::BoxFuture;
use scylla::client::session::Session;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use uuid::Uuid;

// [COMMENT]: Entity bản ghi thông báo trong hộp thư lưu trữ trong ScyllaDB
#[derive(Clone, Debug)]
pub struct NotificationItem {
    pub notification_id: Uuid,
    pub activity_event_id: Uuid,
    pub user_id: Uuid,
    pub severity: String,
    pub title: String,
    pub message: String,
    pub operation: String,
    pub resource_id: Option<String>,
    pub created_at: DateTime<Utc>,
    /// Stable monotonic status version for replay idempotency
    pub projection_version: i64,
}

impl NotificationItem {
    pub fn validate(&self) -> Result<(), AppError> {
        if self.severity.len() > 32
            || self.title.len() > 256
            || self.message.len() > 4_096
            || self.operation.len() > 128
        {
            return Err(invalid("notification display fields are too large"));
        }
        Ok(())
    }
}

// [COMMENT]: View model trả về client cho một thông báo trong hộp thư
#[derive(Clone, Debug, Serialize)]
pub struct NotificationView {
    pub notification_id: Uuid,
    pub activity_event_id: Uuid,
    pub severity: String,
    pub title: String,
    pub message: String,
    pub operation: String,
    pub resource_id: Option<String>,
    pub created_at: DateTime<Utc>,
    pub read_at: Option<DateTime<Utc>>,
}

// [COMMENT]: Kết quả phân trang danh sách thông báo
#[derive(Clone, Debug, Serialize)]
pub struct NotificationPage {
    pub items: Vec<NotificationView>,
    pub next_cursor: Option<String>,
}

// [COMMENT]: Repository trait định nghĩa các thao tác lưu trữ, truy vấn và cập nhật trạng thái đã đọc của thông báo trong ScyllaDB
pub trait NotificationRepo: Send + Sync {
    fn persist_notification<'a>(
        &'a self,
        item: NotificationItem,
    ) -> BoxFuture<'a, Result<(), AppError>>;
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

const INSERT_NOTIFICATION: &str = "INSERT INTO inbox_by_user_month (
    user_id, month_bucket, created_at, notification_id, activity_event_id,
    severity, title, message, operation, resource_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TIMESTAMP ? AND TTL ?";

// [COMMENT]: ScyllaNotificationStore triển khai lưu trữ hộp thư thông báo (Inbox) và trạng thái đọc với ScyllaDB
#[derive(Clone)]
pub struct ScyllaNotificationStore {
    session: Arc<Session>,
    inbox_ttl: i32,
}

#[derive(Debug, Deserialize, Serialize)]
struct NotificationCursor {
    bucket: String,
    timestamp: DateTime<Utc>,
    id: Uuid,
}

impl ScyllaNotificationStore {
    pub fn new(session: Arc<Session>, config: &TimelineConfig) -> Result<Self, AppError> {
        Ok(Self {
            session,
            inbox_ttl: days_to_ttl(config.inbox_retention_days)?,
        })
    }

    // [COMMENT]: Lưu trữ một bản ghi thông báo mới vào bảng inbox_by_user_month với TTL và monotonic timestamp
    async fn write_notification(&self, item: NotificationItem) -> Result<(), AppError> {
        let bucket = month_bucket(item.created_at);
        self.session
            .query_unpaged(
                INSERT_NOTIFICATION,
                (
                    item.user_id,
                    bucket.as_str(),
                    item.created_at,
                    item.notification_id,
                    item.activity_event_id,
                    item.severity.as_str(),
                    item.title.as_str(),
                    item.message.as_str(),
                    item.operation.as_str(),
                    item.resource_id.as_deref(),
                    item.created_at
                        .timestamp_micros()
                        .saturating_add(item.projection_version),
                    self.inbox_ttl,
                ),
            )
            .await?;
        Ok(())
    }

    // [COMMENT]: Đọc danh sách thông báo phân trang, kết hợp với read_before watermark để xác định trạng thái đã đọc
    async fn read_notifications(
        &self,
        user_id: Uuid,
        cursor: Option<&str>,
        limit: usize,
        max_month_scan: usize,
    ) -> Result<NotificationPage, AppError> {
        let cursor = cursor.map(decode_cursor).transpose()?;
        let mut bucket = cursor
            .as_ref()
            .map(|value| value.bucket.clone())
            .unwrap_or_else(|| month_bucket(Utc::now()));
        let mut before = cursor
            .as_ref()
            .map(|value| value.timestamp)
            .unwrap_or_else(|| Utc::now() + Duration::seconds(1));
        let mut before_id = cursor
            .as_ref()
            .map(|value| value.id)
            .unwrap_or_else(max_uuid);
        let read_before = self.read_before(user_id).await?;
        let mut items = Vec::with_capacity(limit);

        for _ in 0..max_month_scan {
            let remaining = limit.saturating_sub(items.len());
            if remaining == 0 {
                break;
            }
            let result = self
                .session
                .query_unpaged(
                    "SELECT created_at, notification_id, activity_event_id, severity,
                            title, message, operation, resource_id, read_at
                     FROM inbox_by_user_month
                     WHERE user_id = ? AND month_bucket = ?
                       AND (created_at, notification_id) < (?, ?)
                     LIMIT ?",
                    (
                        user_id,
                        bucket.as_str(),
                        before,
                        before_id,
                        remaining as i32,
                    ),
                )
                .await?
                .into_rows_result()?;
            let mut fetched = 0;
            for row in result.rows::<(
                DateTime<Utc>,
                Uuid,
                Uuid,
                String,
                String,
                String,
                String,
                Option<String>,
                Option<DateTime<Utc>>,
            )>()? {
                let (
                    created_at,
                    notification_id,
                    activity_event_id,
                    severity,
                    title,
                    message,
                    operation,
                    resource_id,
                    explicit_read_at,
                ) = row?;
                fetched += 1;
                items.push(NotificationView {
                    notification_id,
                    activity_event_id,
                    severity,
                    title,
                    message,
                    operation,
                    resource_id,
                    created_at,
                    read_at: explicit_read_at
                        .or_else(|| read_before.filter(|cursor| created_at <= *cursor)),
                });
            }
            if fetched >= remaining {
                break;
            }
            bucket = previous_month(&bucket)?;
            before = month_end(&bucket)?;
            before_id = max_uuid();
        }
        let next_cursor = items.last().map(|item| {
            encode_cursor(&NotificationCursor {
                bucket: month_bucket(item.created_at),
                timestamp: item.created_at,
                id: item.notification_id,
            })
        });
        Ok(NotificationPage {
            items,
            next_cursor: next_cursor.transpose()?,
        })
    }

    // [COMMENT]: Lấy watermark mốc thời gian đánh dấu toàn bộ thông báo trước đó là đã đọc
    async fn read_before(&self, user_id: Uuid) -> Result<Option<DateTime<Utc>>, AppError> {
        let result = self
            .session
            .query_unpaged(
                "SELECT read_before FROM inbox_state_by_user WHERE user_id = ?",
                (user_id,),
            )
            .await?
            .into_rows_result()?;
        let mut rows = result.rows::<(Option<DateTime<Utc>>,)>()?;
        match rows.next() {
            Some(row) => Ok(row?.0),
            None => Ok(None),
        }
    }
}

impl NotificationRepo for ScyllaNotificationStore {
    fn persist_notification<'a>(
        &'a self,
        item: NotificationItem,
    ) -> BoxFuture<'a, Result<(), AppError>> {
        Box::pin(self.write_notification(item))
    }

    fn list_notifications<'a>(
        &'a self,
        user_id: Uuid,
        cursor: Option<&'a str>,
        limit: usize,
        max_month_scan: usize,
    ) -> BoxFuture<'a, Result<NotificationPage, AppError>> {
        Box::pin(self.read_notifications(user_id, cursor, limit, max_month_scan))
    }

    fn mark_notification_read<'a>(
        &'a self,
        user_id: Uuid,
        month_bucket: &'a str,
        created_at: DateTime<Utc>,
        notification_id: Uuid,
    ) -> BoxFuture<'a, Result<(), AppError>> {
        Box::pin(async move {
            self.session
                .query_unpaged(
                    "UPDATE inbox_by_user_month SET read_at = ?
                     WHERE user_id = ? AND month_bucket = ?
                       AND created_at = ? AND notification_id = ? IF EXISTS",
                    (
                        Utc::now(),
                        user_id,
                        month_bucket,
                        created_at,
                        notification_id,
                    ),
                )
                .await?;
            Ok(())
        })
    }

    fn mark_all_notifications_read<'a>(
        &'a self,
        user_id: Uuid,
    ) -> BoxFuture<'a, Result<(), AppError>> {
        Box::pin(async move {
            self.session
                .query_unpaged(
                    "INSERT INTO inbox_state_by_user (user_id, read_before) VALUES (?, ?)",
                    (user_id, Utc::now()),
                )
                .await?;
            Ok(())
        })
    }
}

fn days_to_ttl(days: u64) -> Result<i32, AppError> {
    let seconds = days
        .checked_mul(86_400)
        .filter(|value| *value <= i32::MAX as u64)
        .ok_or_else(|| invalid("inbox retention exceeds Scylla TTL range"))?;
    Ok(seconds as i32)
}

fn month_bucket(timestamp: DateTime<Utc>) -> String {
    timestamp.format("%Y-%m").to_string()
}

fn previous_month(bucket: &str) -> Result<String, AppError> {
    let (year, month) = bucket
        .split_once('-')
        .ok_or_else(|| invalid("notification cursor month bucket is invalid"))?;
    let mut year = year
        .parse::<i32>()
        .map_err(|_| invalid("notification cursor year is invalid"))?;
    let mut month = month
        .parse::<u32>()
        .map_err(|_| invalid("notification cursor month is invalid"))?;
    if !(1..=12).contains(&month) {
        return Err(invalid("notification cursor month is out of range"));
    }
    if month == 1 {
        year -= 1;
        month = 12;
    } else {
        month -= 1;
    }
    Ok(format!("{year:04}-{month:02}"))
}

fn month_end(bucket: &str) -> Result<DateTime<Utc>, AppError> {
    let (year, month) = bucket
        .split_once('-')
        .ok_or_else(|| invalid("notification month is invalid"))?;
    let year = year
        .parse::<i32>()
        .map_err(|_| invalid("notification year is invalid"))?;
    let month = month
        .parse::<u32>()
        .map_err(|_| invalid("notification month is invalid"))?;
    if !(1..=12).contains(&month) {
        return Err(invalid("notification month is out of range"));
    }
    let (next_year, next_month) = if month == 12 {
        (year + 1, 1)
    } else {
        (year, month + 1)
    };
    Utc.with_ymd_and_hms(next_year, next_month, 1, 0, 0, 0)
        .single()
        .ok_or_else(|| invalid("notification month boundary is invalid"))
}

fn max_uuid() -> Uuid {
    Uuid::from_u128(u128::MAX)
}

fn encode_cursor(cursor: &NotificationCursor) -> Result<String, AppError> {
    Ok(URL_SAFE_NO_PAD.encode(serde_json::to_vec(cursor)?))
}

fn decode_cursor(value: &str) -> Result<NotificationCursor, AppError> {
    let raw = URL_SAFE_NO_PAD
        .decode(value)
        .map_err(|_| invalid("notification cursor is not valid base64url"))?;
    let cursor: NotificationCursor =
        serde_json::from_slice(&raw).map_err(|_| invalid("notification cursor is invalid"))?;
    if month_bucket(cursor.timestamp) != cursor.bucket {
        return Err(invalid(
            "notification cursor bucket does not match timestamp",
        ));
    }
    Ok(cursor)
}

fn invalid(message: &str) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidInput, message.to_owned()).into()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn notification_cursor_round_trip() {
        let cursor = NotificationCursor {
            bucket: "2026-08".to_string(),
            timestamp: Utc.with_ymd_and_hms(2026, 8, 29, 12, 0, 0).unwrap(),
            id: Uuid::new_v4(),
        };
        let encoded = encode_cursor(&cursor).unwrap();
        let decoded = decode_cursor(&encoded).unwrap();
        assert_eq!(decoded.bucket, cursor.bucket);
        assert_eq!(decoded.timestamp, cursor.timestamp);
        assert_eq!(decoded.id, cursor.id);
    }

    #[test]
    fn malformed_notification_cursor_and_bucket_mismatch_are_rejected() {
        assert!(decode_cursor("not-base64url***").is_err());
        let cursor = NotificationCursor {
            bucket: "2026-07".to_string(),
            timestamp: Utc.with_ymd_and_hms(2026, 8, 29, 12, 0, 0).unwrap(),
            id: Uuid::new_v4(),
        };
        assert!(decode_cursor(&encode_cursor(&cursor).unwrap()).is_err());
        assert!(previous_month("2026-00").is_err());
    }

    #[test]
    fn notification_validation_rejects_oversized_display_fields() {
        let mut item = NotificationItem {
            notification_id: Uuid::new_v4(),
            activity_event_id: Uuid::new_v4(),
            user_id: Uuid::new_v4(),
            severity: "info".to_string(),
            title: "Done".to_string(),
            message: String::new(),
            operation: "storage.bucket.create".to_string(),
            resource_id: None,
            created_at: Utc::now(),
            projection_version: 0,
        };
        assert!(item.validate().is_ok());
        item.title = "x".repeat(257);
        assert!(item.validate().is_err());
    }
}
