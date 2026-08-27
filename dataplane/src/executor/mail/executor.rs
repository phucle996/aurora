use crate::executor::{ExecutionResult, ExecutorError};
use crate::job_runtime::model::ValidatedJob;
use std::sync::Arc;

pub async fn dispatch_mail_job(
    action: &str,
    payload: Arc<ValidatedJob>,
    mail_runtime: Arc<super::MailRuntime>,
    _zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    // [COMMENT]: Projection là control path riêng; delivery payload chỉ được broker runtime xử lý.
    if action == "consumer.drain" {
        return super::consumer::apply_mail_consumer_drain(
            payload,
            mail_runtime
                .configuration
                .zone_kv()
                .ok_or_else(|| failed("MAIL_ZONE_KV_UNAVAILABLE"))?,
        )
        .await;
    }
    if action == "consumer.upsert" {
        return super::consumer::apply_mail_consumer_upsert(
            payload,
            mail_runtime
                .configuration
                .zone_kv()
                .ok_or_else(|| failed("MAIL_ZONE_KV_UNAVAILABLE"))?,
            _zone_id,
        )
        .await;
    }
    if action == "consumer.delete" {
        return super::consumer::apply_mail_consumer_delete(
            payload,
            mail_runtime
                .configuration
                .zone_kv()
                .ok_or_else(|| failed("MAIL_ZONE_KV_UNAVAILABLE"))?,
            _zone_id,
        )
        .await;
    }
    if action == "template.version_published" {
        return super::projection::apply_mail_template_version_published(
            payload,
            mail_runtime
                .configuration
                .zone_kv()
                .ok_or_else(|| failed("MAIL_ZONE_KV_UNAVAILABLE"))?,
            _zone_id,
        )
        .await;
    }
    if action == "template.deleted" {
        return super::projection::apply_mail_template_deleted(
            payload,
            mail_runtime
                .configuration
                .zone_kv()
                .ok_or_else(|| failed("MAIL_ZONE_KV_UNAVAILABLE"))?,
            _zone_id,
        )
        .await;
    }
    if action == "projection.reconcile_completed" {
        return super::projection::apply_mail_reconcile_completed(
            payload,
            mail_runtime
                .configuration
                .zone_kv()
                .ok_or_else(|| failed("MAIL_ZONE_KV_UNAVAILABLE"))?,
            _zone_id,
        )
        .await;
    }

    // [COMMENT]: Delivery mail chỉ chạy qua ordinary broker consumer runtime; Kafka internal không direct-send system mail.
    Err(failed(format!("unsupported mail action: {action}")))
}

fn failed(message: impl Into<String>) -> ExecutorError {
    ExecutorError::ExecutionFailed(message.into())
}
