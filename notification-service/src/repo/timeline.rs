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

// [COMMENT]: Phân loại danh mục của sự kiện hoạt động người dùng (Security, Identity, Resource, Billing, Access)
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ActivityCategory {
    Security,
    Identity,
    Resource,
    Billing,
    Access,
}

impl ActivityCategory {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "security" => Some(Self::Security),
            "identity" => Some(Self::Identity),
            "resource" => Some(Self::Resource),
            "billing" => Some(Self::Billing),
            "access" => Some(Self::Access),
            _ => None,
        }
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Security => "security",
            Self::Identity => "identity",
            Self::Resource => "resource",
            Self::Billing => "billing",
            Self::Access => "access",
        }
    }
}

// [COMMENT]: Kết quả thực thi của sự kiện hoạt động (Succeeded, Failed, Started)
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ActivityOutcome {
    Succeeded,
    Failed,
    Started,
}

impl ActivityOutcome {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "succeeded" => Some(Self::Succeeded),
            "failed" => Some(Self::Failed),
            "started" => Some(Self::Started),
            _ => None,
        }
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Succeeded => "succeeded",
            Self::Failed => "failed",
            Self::Started => "started",
        }
    }
}

// [COMMENT]: Loại tác tử thực hiện hành động (SelfUser, Admin, System)
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ActorType {
    SelfUser,
    Admin,
    System,
}

impl ActorType {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "self" => Some(Self::SelfUser),
            "admin" => Some(Self::Admin),
            "system" => Some(Self::System),
            _ => None,
        }
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            Self::SelfUser => "self",
            Self::Admin => "admin",
            Self::System => "system",
        }
    }
}

// [COMMENT]: Entity bản ghi sự kiện hoạt động lưu trữ trong ScyllaDB
#[derive(Clone, Debug)]
pub struct ActivityEvent {
    pub event_id: Uuid,
    pub user_id: Uuid,
    pub category: ActivityCategory,
    pub action: String,
    pub actor_type: ActorType,
    pub actor_id: Option<Uuid>,
    pub outcome: ActivityOutcome,
    pub source_service: String,
    pub resource_type: Option<String>,
    pub resource_id: Option<String>,
    pub operation_id: Option<String>,
    pub title: String,
    pub summary: String,
    pub occurred_at: DateTime<Utc>,
    pub metadata_json: String,
    pub schema_version: u32,
    /// Stable replay ordering for one event identity. A larger version must
    /// win even when Redis pending-entry recovery delivers it out of order.
    pub projection_version: i64,
}

impl ActivityEvent {
    pub fn validate(&self) -> Result<(), AppError> {
        if self.action.is_empty() || self.action.len() > 128 {
            return Err(invalid("activity action length is invalid"));
        }
        if self.source_service.is_empty() || self.source_service.len() > 64 {
            return Err(invalid("activity source service length is invalid"));
        }
        if self.title.len() > 256 || self.summary.len() > 4_096 {
            return Err(invalid("activity display fields are too large"));
        }
        if self.metadata_json.len() > 16 * 1024
            || serde_json::from_str::<serde_json::Value>(&self.metadata_json).is_err()
        {
            return Err(invalid("activity metadata must be bounded JSON"));
        }
        if self.schema_version == 0 {
            return Err(invalid("activity schema version is invalid"));
        }
        if self.occurred_at > Utc::now() + Duration::minutes(5) {
            return Err(invalid("activity timestamp is too far in the future"));
        }
        Ok(())
    }
}

// [COMMENT]: DTO Query phân trang cho Activity Timeline APIs
#[derive(Clone, Debug, Deserialize)]
pub struct PageRequest {
    pub cursor: Option<String>,
    pub limit: Option<usize>,
    pub category: Option<String>,
}

// [COMMENT]: View model trả về client cho một sự kiện hoạt động
#[derive(Clone, Debug, Serialize)]
pub struct ActivityView {
    pub event_id: Uuid,
    pub category: String,
    pub action: String,
    pub actor_type: String,
    pub outcome: String,
    pub source_service: String,
    pub resource_type: Option<String>,
    pub resource_id: Option<String>,
    pub operation_id: Option<String>,
    pub title: String,
    pub summary: String,
    pub occurred_at: DateTime<Utc>,
    pub metadata: serde_json::Value,
}

// [COMMENT]: Kết quả phân trang danh sách hoạt động
#[derive(Clone, Debug, Serialize)]
pub struct ActivityPage {
    pub items: Vec<ActivityView>,
    pub next_cursor: Option<String>,
}

// [COMMENT]: Repository trait định nghĩa các thao tác lưu trữ và truy vấn dòng hoạt động ScyllaDB
pub trait TimelineRepo: Send + Sync {
    fn persist_activity<'a>(&'a self, event: ActivityEvent) -> BoxFuture<'a, Result<(), AppError>>;
    fn list_activity<'a>(
        &'a self,
        user_id: Uuid,
        cursor: Option<&'a str>,
        category: Option<ActivityCategory>,
        limit: usize,
        max_month_scan: usize,
    ) -> BoxFuture<'a, Result<ActivityPage, AppError>>;
}

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

// [COMMENT]: ScyllaTimelineStore triển khai lưu trữ và truy vấn sự kiện hoạt động với ScyllaDB
#[derive(Clone)]
pub struct ScyllaTimelineStore {
    session: Arc<Session>,
    activity_ttl: i32,
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
        })
    }

    // [COMMENT]: Ghi đồng thời 2 projection (theo user_id và theo category) để tối ưu truy vấn
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

    // [COMMENT]: Đọc danh sách hoạt động với phân trang cursor duyệt ngược qua các bucket tháng
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
}

impl TimelineRepo for ScyllaTimelineStore {
    fn persist_activity<'a>(&'a self, event: ActivityEvent) -> BoxFuture<'a, Result<(), AppError>> {
        Box::pin(self.write_activity(event))
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

    #[test]
    fn malformed_cursor_and_bucket_mismatch_are_rejected() {
        assert!(decode_cursor("not-base64url***").is_err());
        let cursor = TimelineCursor {
            bucket: "2026-06".to_string(),
            timestamp: Utc.with_ymd_and_hms(2026, 7, 25, 10, 0, 0).unwrap(),
            id: Uuid::new_v4(),
            category: None,
        };
        assert!(decode_cursor(&encode_cursor(&cursor).unwrap()).is_err());
        assert!(previous_month("2026-13").is_err());
    }

    #[test]
    fn activity_validation_rejects_unbounded_or_invalid_projection_input() {
        let mut event = ActivityEvent {
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
            summary: String::new(),
            occurred_at: Utc::now(),
            metadata_json: "{}".to_string(),
            schema_version: 1,
            projection_version: 0,
        };
        assert!(event.validate().is_ok());
        event.action.clear();
        assert!(event.validate().is_err());
        event.action = "session.login".to_string();
        event.metadata_json = "[] trailing".to_string();
        assert!(event.validate().is_err());
        event.metadata_json = "{}".to_string();
        event.schema_version = 0;
        assert!(event.validate().is_err());
    }
}
