use crate::application::ports::AppError;
use crate::config::TimelineConfig;
use crate::timeline::event::{
    ActivityCategory, ActivityEvent, ActivityPage, ActivityView, NotificationItem,
    NotificationPage, NotificationView,
};
use crate::timeline::store::TimelineStore;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use chrono::{DateTime, Duration, TimeZone, Utc};
use futures_util::future::BoxFuture;
use scylla::client::session::Session;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use uuid::Uuid;

const INSERT_ACTIVITY: &str = "INSERT INTO activity_by_user_month (
    user_id, month_bucket, occurred_at, event_id, category, action, actor_type,
    actor_id, outcome, source_service, resource_type, resource_id, operation_id,
    title, summary, metadata_json, schema_version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TIMESTAMP ? AND TTL ?";

const INSERT_ACTIVITY_CATEGORY: &str = "INSERT INTO activity_by_user_category_month (
    user_id, category, month_bucket, occurred_at, event_id, action, actor_type,
    actor_id, outcome, source_service, resource_type, resource_id, operation_id,
    title, summary, metadata_json, schema_version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TIMESTAMP ? AND TTL ?";

const INSERT_NOTIFICATION: &str = "INSERT INTO inbox_by_user_month (
    user_id, month_bucket, created_at, notification_id, activity_event_id,
    severity, title, message, operation, resource_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TIMESTAMP ? AND TTL ?";

#[derive(scylla::SerializeRow)]
#[scylla(flavor = "enforce_order", skip_name_checks)]
struct ActivityInsert<'a> {
    user_id: Uuid,
    month_bucket: &'a str,
    occurred_at: DateTime<Utc>,
    event_id: Uuid,
    category: &'a str,
    action: &'a str,
    actor_type: &'a str,
    actor_id: Option<&'a str>,
    outcome: &'a str,
    source_service: &'a str,
    resource_type: Option<&'a str>,
    resource_id: Option<&'a str>,
    operation_id: Option<&'a str>,
    title: &'a str,
    summary: &'a str,
    metadata_json: &'a str,
    schema_version: i32,
    timestamp: i64,
    ttl: i32,
}

#[derive(scylla::SerializeRow)]
#[scylla(flavor = "enforce_order", skip_name_checks)]
struct CategoryInsert<'a> {
    user_id: Uuid,
    category: &'a str,
    month_bucket: &'a str,
    occurred_at: DateTime<Utc>,
    event_id: Uuid,
    action: &'a str,
    actor_type: &'a str,
    actor_id: Option<&'a str>,
    outcome: &'a str,
    source_service: &'a str,
    resource_type: Option<&'a str>,
    resource_id: Option<&'a str>,
    operation_id: Option<&'a str>,
    title: &'a str,
    summary: &'a str,
    metadata_json: &'a str,
    schema_version: i32,
    timestamp: i64,
    ttl: i32,
}

#[derive(Clone)]
pub struct ScyllaTimelineStore {
    session: Arc<Session>,
    activity_ttl: i32,
    inbox_ttl: i32,
}

#[derive(Debug, Deserialize, Serialize)]
struct TimelineCursor {
    bucket: String,
    timestamp: DateTime<Utc>,
    id: Uuid,
    category: Option<String>,
}

impl ScyllaTimelineStore {
    pub fn new(session: Arc<Session>, config: &TimelineConfig) -> Result<Self, AppError> {
        Ok(Self {
            session,
            activity_ttl: days_to_ttl(config.activity_retention_days)?,
            inbox_ttl: days_to_ttl(config.inbox_retention_days)?,
        })
    }

    async fn write_activity(&self, event: ActivityEvent) -> Result<(), AppError> {
        let bucket = month_bucket(event.occurred_at);
        let actor_id = event.actor_id.map(|value| value.to_string());
        let resource_type = event.resource_type.as_deref();
        let resource_id = event.resource_id.as_deref();
        let operation_id = event.operation_id.as_deref();
        self.session
            .query_unpaged(
                INSERT_ACTIVITY,
                ActivityInsert {
                    user_id: event.user_id,
                    month_bucket: bucket.as_str(),
                    occurred_at: event.occurred_at,
                    event_id: event.event_id,
                    category: event.category.as_str(),
                    action: event.action.as_str(),
                    actor_type: event.actor_type.as_str(),
                    actor_id: actor_id.as_deref(),
                    outcome: event.outcome.as_str(),
                    source_service: event.source_service.as_str(),
                    resource_type,
                    resource_id,
                    operation_id,
                    title: event.title.as_str(),
                    summary: event.summary.as_str(),
                    metadata_json: event.metadata_json.as_str(),
                    schema_version: event.schema_version as i32,
                    timestamp: event
                        .occurred_at
                        .timestamp_micros()
                        .saturating_add(event.projection_version),
                    ttl: self.activity_ttl,
                },
            )
            .await?;
        // This projection is intentionally a second idempotent write. A crash
        // between projections leaves the Redis entry pending; retry repairs it.
        self.session
            .query_unpaged(
                INSERT_ACTIVITY_CATEGORY,
                CategoryInsert {
                    user_id: event.user_id,
                    category: event.category.as_str(),
                    month_bucket: bucket.as_str(),
                    occurred_at: event.occurred_at,
                    event_id: event.event_id,
                    action: event.action.as_str(),
                    actor_type: event.actor_type.as_str(),
                    actor_id: actor_id.as_deref(),
                    outcome: event.outcome.as_str(),
                    source_service: event.source_service.as_str(),
                    resource_type,
                    resource_id,
                    operation_id,
                    title: event.title.as_str(),
                    summary: event.summary.as_str(),
                    metadata_json: event.metadata_json.as_str(),
                    schema_version: event.schema_version as i32,
                    timestamp: event
                        .occurred_at
                        .timestamp_micros()
                        .saturating_add(event.projection_version),
                    ttl: self.activity_ttl,
                },
            )
            .await?;
        Ok(())
    }

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

    async fn read_activity(
        &self,
        user_id: Uuid,
        cursor: Option<&str>,
        category: Option<ActivityCategory>,
        limit: usize,
        max_month_scan: usize,
    ) -> Result<ActivityPage, AppError> {
        let cursor = cursor.map(decode_cursor).transpose()?;
        if let Some(cursor_category) = cursor.as_ref().and_then(|value| value.category.as_deref()) {
            if category.as_ref().map(ActivityCategory::as_str) != Some(cursor_category) {
                return Err(invalid("timeline cursor category does not match request"));
            }
        }
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
        let mut items = Vec::with_capacity(limit);

        for _ in 0..max_month_scan {
            let remaining = limit.saturating_sub(items.len());
            if remaining == 0 {
                break;
            }
            let rows = if let Some(category) = category.as_ref() {
                self.query_activity_category(
                    user_id,
                    category.as_str(),
                    &bucket,
                    before,
                    before_id,
                    remaining,
                )
                .await?
            } else {
                self.query_activity(user_id, &bucket, before, before_id, remaining)
                    .await?
            };
            let fetched = rows.len();
            items.extend(rows);
            if fetched >= remaining {
                break;
            }
            bucket = previous_month(&bucket)?;
            before = month_end(&bucket)?;
            before_id = max_uuid();
        }

        let next_cursor = items.last().map(|item| {
            encode_cursor(&TimelineCursor {
                bucket: month_bucket(item.occurred_at),
                timestamp: item.occurred_at,
                id: item.event_id,
                category: category.as_ref().map(|value| value.as_str().to_owned()),
            })
        });
        Ok(ActivityPage {
            items,
            next_cursor: next_cursor.transpose()?,
        })
    }

    async fn query_activity(
        &self,
        user_id: Uuid,
        bucket: &str,
        before: DateTime<Utc>,
        before_id: Uuid,
        limit: usize,
    ) -> Result<Vec<ActivityView>, AppError> {
        let result = self
            .session
            .query_unpaged(
                "SELECT occurred_at, event_id, category, action, actor_type, outcome,
                        source_service, resource_type, resource_id, operation_id,
                        title, summary, metadata_json
                 FROM activity_by_user_month
                 WHERE user_id = ? AND month_bucket = ?
                   AND (occurred_at, event_id) < (?, ?)
                 LIMIT ?",
                (user_id, bucket, before, before_id, limit as i32),
            )
            .await?
            .into_rows_result()?;
        parse_activity_rows(result)
    }

    async fn query_activity_category(
        &self,
        user_id: Uuid,
        category: &str,
        bucket: &str,
        before: DateTime<Utc>,
        before_id: Uuid,
        limit: usize,
    ) -> Result<Vec<ActivityView>, AppError> {
        let result = self
            .session
            .query_unpaged(
                "SELECT occurred_at, event_id, action, actor_type, outcome,
                        source_service, resource_type, resource_id, operation_id,
                        title, summary, metadata_json
                 FROM activity_by_user_category_month
                 WHERE user_id = ? AND category = ? AND month_bucket = ?
                   AND (occurred_at, event_id) < (?, ?) LIMIT ?",
                (user_id, category, bucket, before, before_id, limit as i32),
            )
            .await?
            .into_rows_result()?;
        let mut items = Vec::new();
        for row in result.rows::<(
            DateTime<Utc>,
            Uuid,
            String,
            String,
            String,
            String,
            Option<String>,
            Option<String>,
            Option<String>,
            String,
            String,
            String,
        )>()? {
            let (
                occurred_at,
                event_id,
                action,
                actor_type,
                outcome,
                source_service,
                resource_type,
                resource_id,
                operation_id,
                title,
                summary,
                metadata_json,
            ) = row?;
            items.push(ActivityView {
                event_id,
                category: category.to_owned(),
                action,
                actor_type,
                outcome,
                source_service,
                resource_type,
                resource_id,
                operation_id,
                title,
                summary,
                occurred_at,
                metadata: serde_json::from_str(&metadata_json).unwrap_or_default(),
            });
        }
        Ok(items)
    }

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
            encode_cursor(&TimelineCursor {
                bucket: month_bucket(item.created_at),
                timestamp: item.created_at,
                id: item.notification_id,
                category: None,
            })
        });
        Ok(NotificationPage {
            items,
            next_cursor: next_cursor.transpose()?,
        })
    }

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

impl TimelineStore for ScyllaTimelineStore {
    fn persist_activity<'a>(&'a self, event: ActivityEvent) -> BoxFuture<'a, Result<(), AppError>> {
        Box::pin(self.write_activity(event))
    }

    fn persist_notification<'a>(
        &'a self,
        item: NotificationItem,
    ) -> BoxFuture<'a, Result<(), AppError>> {
        Box::pin(self.write_notification(item))
    }

    fn list_activity<'a>(
        &'a self,
        user_id: Uuid,
        cursor: Option<&'a str>,
        category: Option<ActivityCategory>,
        limit: usize,
        max_month_scan: usize,
    ) -> BoxFuture<'a, Result<ActivityPage, AppError>> {
        Box::pin(self.read_activity(user_id, cursor, category, limit, max_month_scan))
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

type ActivityRow = (
    DateTime<Utc>,
    Uuid,
    String,
    String,
    String,
    String,
    String,
    Option<String>,
    Option<String>,
    Option<String>,
    String,
    String,
    String,
);

fn parse_activity_rows(
    result: scylla::response::query_result::QueryRowsResult,
) -> Result<Vec<ActivityView>, AppError> {
    let mut items = Vec::new();
    for row in result.rows::<ActivityRow>()? {
        let (
            occurred_at,
            event_id,
            category,
            action,
            actor_type,
            outcome,
            source_service,
            resource_type,
            resource_id,
            operation_id,
            title,
            summary,
            metadata_json,
        ) = row?;
        items.push(ActivityView {
            event_id,
            category,
            action,
            actor_type,
            outcome,
            source_service,
            resource_type,
            resource_id,
            operation_id,
            title,
            summary,
            occurred_at,
            metadata: serde_json::from_str(&metadata_json).unwrap_or_default(),
        });
    }
    Ok(items)
}

fn days_to_ttl(days: u64) -> Result<i32, AppError> {
    let seconds = days
        .checked_mul(86_400)
        .filter(|value| *value <= i32::MAX as u64)
        .ok_or_else(|| invalid("timeline retention exceeds Scylla TTL range"))?;
    Ok(seconds as i32)
}

fn month_bucket(timestamp: DateTime<Utc>) -> String {
    timestamp.format("%Y-%m").to_string()
}

fn previous_month(bucket: &str) -> Result<String, AppError> {
    let (year, month) = bucket
        .split_once('-')
        .ok_or_else(|| invalid("timeline cursor month bucket is invalid"))?;
    let mut year = year
        .parse::<i32>()
        .map_err(|_| invalid("timeline cursor year is invalid"))?;
    let mut month = month
        .parse::<u32>()
        .map_err(|_| invalid("timeline cursor month is invalid"))?;
    if !(1..=12).contains(&month) {
        return Err(invalid("timeline cursor month is out of range"));
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
        .ok_or_else(|| invalid("timeline month is invalid"))?;
    let year = year
        .parse::<i32>()
        .map_err(|_| invalid("timeline year is invalid"))?;
    let month = month
        .parse::<u32>()
        .map_err(|_| invalid("timeline month is invalid"))?;
    if !(1..=12).contains(&month) {
        return Err(invalid("timeline month is out of range"));
    }
    let (next_year, next_month) = if month == 12 {
        (year + 1, 1)
    } else {
        (year, month + 1)
    };
    Utc.with_ymd_and_hms(next_year, next_month, 1, 0, 0, 0)
        .single()
        .ok_or_else(|| invalid("timeline month boundary is invalid"))
}

fn max_uuid() -> Uuid {
    Uuid::from_u128(u128::MAX)
}

fn encode_cursor(cursor: &TimelineCursor) -> Result<String, AppError> {
    Ok(URL_SAFE_NO_PAD.encode(serde_json::to_vec(cursor)?))
}

fn decode_cursor(value: &str) -> Result<TimelineCursor, AppError> {
    let raw = URL_SAFE_NO_PAD
        .decode(value)
        .map_err(|_| invalid("timeline cursor is not valid base64url"))?;
    let cursor: TimelineCursor =
        serde_json::from_slice(&raw).map_err(|_| invalid("timeline cursor is invalid"))?;
    if month_bucket(cursor.timestamp) != cursor.bucket {
        return Err(invalid("timeline cursor bucket does not match timestamp"));
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
    fn cursor_round_trip_is_stable_and_validated() {
        let cursor = TimelineCursor {
            bucket: "2026-07".to_string(),
            timestamp: Utc.with_ymd_and_hms(2026, 7, 25, 10, 0, 0).unwrap(),
            id: Uuid::new_v4(),
            category: None,
        };
        let encoded = encode_cursor(&cursor).unwrap();
        let decoded = decode_cursor(&encoded).unwrap();
        assert_eq!(decoded.bucket, cursor.bucket);
        assert_eq!(decoded.timestamp, cursor.timestamp);
        assert_eq!(decoded.id, cursor.id);
    }

    #[test]
    fn month_navigation_crosses_year_boundary() {
        assert_eq!(previous_month("2026-01").unwrap(), "2025-12");
        assert_eq!(previous_month("2026-07").unwrap(), "2026-06");
        assert_eq!(
            month_end("2026-12").unwrap(),
            Utc.with_ymd_and_hms(2027, 1, 1, 0, 0, 0).unwrap()
        );
    }
}
