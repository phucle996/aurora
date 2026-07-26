use super::contract::ValidatedResult;
use super::notify::{JobNotifier, NotificationIntent};
use crate::observability::logger::{LogFields, Logger};
use crate::outbox::{ownership, SharedStreamPublisher};
use crate::results::{mail, storage};
use opentelemetry::trace::FutureExt;
use std::sync::Arc;
use tokio_postgres::{Client, Row};

pub struct ResolvedResult {
    actor_user_id: Option<String>,
    job_topic: String,
    resource_id: String,
}

pub enum ApplyOutcome {
    Applied,
    AlreadyApplied,
}

pub enum AuthorityCheck {
    Verified,
    Reject(&'static str),
}

/// Resolve the command fence from Controlplane state. Kafka/Dataplane may
/// report correlation fields, but they cannot redefine the authoritative
/// source domain, topic or job version used to mutate business state.
pub async fn verify_authority(
    result: &ValidatedResult,
    client: &Client,
) -> Result<AuthorityCheck, tokio_postgres::Error> {
    let query = match result.wire.source_domain.as_str() {
        "MAIL" => {
            "SELECT job_version FROM mail.mail_outbox_records \
             WHERE event_id = $1 AND job_topic = $2"
        }
        "STORAGE" => {
            "SELECT job_version FROM storage.storage_outbox_records \
             WHERE event_id = $1 AND job_topic = $2"
        }
        _ => return Ok(AuthorityCheck::Reject("JOB_RESULT_SOURCE_INVALID")),
    };
    let row = client
        .query_opt(query, &[&result.job_id, &result.wire.job_topic])
        .await?;
    let Some(row) = row else {
        return Ok(AuthorityCheck::Reject("JOB_RESULT_SOURCE_NOT_FOUND"));
    };
    let authoritative_version: i32 = row.get(0);
    if authoritative_version <= 0
        || u32::try_from(authoritative_version).ok() != Some(result.wire.job_version)
    {
        return Ok(AuthorityCheck::Reject("JOB_RESULT_VERSION_MISMATCH"));
    }
    Ok(AuthorityCheck::Verified)
}

pub async fn apply_result(
    result: &ValidatedResult,
    pg_client: &mut Client,
    notification_redis: &mut redis::aio::ConnectionManager,
    ownership_publisher: &Arc<SharedStreamPublisher>,
) -> Result<ApplyOutcome, Box<dyn std::error::Error>> {
    let wire = &result.wire;
    let error_code = wire.error_code.as_deref();
    let error_message = if matches!(wire.result_status.as_str(), "SUCCEEDED" | "PROCESSING") {
        None
    } else {
        Some(wire.message.as_str())
    };

    let db_context = crate::observability::otel::OtelTracer::start_current_span(
        "apply controlplane job result",
        opentelemetry::trace::SpanKind::Client,
        vec![
            opentelemetry::KeyValue::new("db.system", "postgresql"),
            opentelemetry::KeyValue::new("db.operation.name", "update"),
            opentelemetry::KeyValue::new("aurora.source.domain", wire.source_domain.clone()),
        ],
    );
    let db_result: Result<Option<Row>, Box<dyn std::error::Error>> = async {
        match wire.source_domain.as_str() {
            "MAIL" => {
                mail::apply::apply_mail_result(
                    pg_client,
                    result.job_id,
                    &wire.job_topic,
                    &wire.result_status,
                    wire.attempt,
                    error_code,
                    error_message,
                )
                .await
            }
            "STORAGE" => storage::apply::apply_storage_result(
                pg_client,
                result.job_id,
                &wire.job_topic,
                &wire.result_status,
                error_code,
                error_message,
            )
            .await
            .map_err(|error| Box::<dyn std::error::Error>::from(error.to_string())),
            _ => Err(format!("unsupported source_domain '{}'", wire.source_domain).into()),
        }
    }
    .with_context(db_context.clone())
    .await;
    crate::observability::otel::OtelTracer::finish_span(
        &db_context,
        db_result
            .as_ref()
            .err()
            .map(|_| "POSTGRES_JOB_RESULT_UPDATE_FAILED"),
    );

    let applied_row = db_result?;
    let applied = applied_row.is_some();
    let resolved = if let Some(row) = applied_row {
        Some(resolve_row(row))
    } else if wire.source_domain == "STORAGE" {
        // A replay after DB commit must still reconstruct the same ownership
        // intent. Dataplane fields are never used as the ownership authority.
        load_existing_storage_result(
            pg_client,
            result.job_id,
            &wire.job_topic,
            &wire.result_status,
        )
        .await?
    } else {
        None
    };

    if wire.source_domain == "STORAGE"
        && wire.result_status == "SUCCEEDED"
        && matches!(
            wire.job_topic.as_str(),
            "storage.bucket.create" | "storage.bucket.delete"
        )
    {
        if let Err(error) = ownership::publish_for_job(
            pg_client,
            ownership_publisher,
            result.job_id,
            &wire.job_topic,
        )
        .await
        {
            crate::observability::metrics::MetricsManager::record_ownership_pending();
            // PostgreSQL ownership_published_at remains NULL. The recovery relay
            // owns retry, so Shared Redis degradation does not stall result offsets.
            Logger::sys_error_with_fields(
                "results.ownership",
                "RESOURCE_OWNERSHIP_FAST_PATH_FAILED",
                "Ownership remains pending on the durable storage outbox row",
                &error.to_string(),
                LogFields {
                    operation_id: Some(&result.job_id.to_string()),
                    retryable: Some(true),
                    outcome: Some("pending"),
                    ..LogFields::default()
                },
            );
        }
    }

    if let Some(ref resolved) = resolved {
        if let Some(user_id) = resolved
            .actor_user_id
            .as_deref()
            .filter(|value| !value.is_empty())
            // Access preparation is an internal authorization projection. It
            // must not create a user notification or expose payload details.
            .filter(|_| wire.job_topic != "storage.access.prepare")
        {
            let current_context = opentelemetry::Context::current();
            let propagation =
                crate::observability::otel::OtelTracer::inject_context(&current_context);
            let notification = NotificationIntent {
                job_id: result.job_id,
                user_id,
                job_version: wire.job_version,
                attempt: wire.attempt,
                status: &wire.result_status,
                job_topic: &resolved.job_topic,
                resource_id: &resolved.resource_id,
                message: &wire.message,
                traceparent: &propagation.traceparent,
                tracestate: &propagation.tracestate,
            };
            let notification_context = crate::observability::otel::OtelTracer::start_current_span(
                "send stream:{job_notifications}",
                opentelemetry::trace::SpanKind::Producer,
                vec![
                    opentelemetry::KeyValue::new("messaging.system", "redis"),
                    opentelemetry::KeyValue::new("messaging.operation.type", "send"),
                    opentelemetry::KeyValue::new(
                        "messaging.destination.name",
                        "stream:{job_notifications}",
                    ),
                ],
            );
            let notification_result =
                JobNotifier::notify_realtime(notification, notification_redis)
                    .with_context(notification_context.clone())
                    .await;
            crate::observability::otel::OtelTracer::finish_span(
                &notification_context,
                notification_result
                    .as_ref()
                    .err()
                    .map(|_| "JOB_NOTIFICATION_ENQUEUE_FAILED"),
            );
            if let Err(error) = notification_result {
                crate::observability::metrics::MetricsManager::record_notification_failed();
                Logger::sys_error_with_fields(
                    "results.notify",
                    "JOB_NOTIFICATION_BEST_EFFORT_DROPPED",
                    "Business result is durable; UI must recover through the authoritative API",
                    &error.to_string(),
                    LogFields {
                        operation_id: Some(&result.job_id.to_string()),
                        retryable: Some(false),
                        outcome: Some("dropped"),
                        ..LogFields::default()
                    },
                );
            }
        }
    }

    Ok(if applied {
        ApplyOutcome::Applied
    } else {
        ApplyOutcome::AlreadyApplied
    })
}

fn resolve_row(row: Row) -> ResolvedResult {
    ResolvedResult {
        actor_user_id: row.get(0),
        job_topic: row.get(1),
        resource_id: row.get::<_, Option<String>>(3).unwrap_or_default(),
    }
}

async fn load_existing_storage_result(
    client: &Client,
    job_id: uuid::Uuid,
    job_topic: &str,
    status: &str,
) -> Result<Option<ResolvedResult>, tokio_postgres::Error> {
    let row = client
        .query_opt(
            "SELECT actor_user_id::text, job_topic, trace_id, resource_id \
             FROM storage.storage_outbox_records \
             WHERE event_id = $1 AND job_topic = $2 AND status = $3",
            &[&job_id, &job_topic, &status],
        )
        .await?;
    Ok(row.map(resolve_row))
}
