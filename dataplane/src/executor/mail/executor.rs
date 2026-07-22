use crate::executor::{ExecutionResult, ExecutorError};
use crate::infra::redis::RedisClientManager;
use crate::job_lifecycle::message::JobPayload;
use crate::workerpool::lifecycle::WorkerLifecycleManager;
use std::sync::Arc;

pub async fn dispatch_mail_job(
    action: &str,
    payload: JobPayload,
    worker_pool: Arc<WorkerLifecycleManager>,
    _redis_job: Arc<RedisClientManager>,
    _zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    // [COMMENT]: Projection là control path riêng; delivery payload chỉ được broker runtime xử lý.
    if action == "consumer.upsert" {
        return super::projection::apply_mail_consumer_upsert(
            payload,
            worker_pool
                .mail_runtime
                .configuration
                .zone_kv()
                .ok_or_else(|| failed("MAIL_ZONE_KV_UNAVAILABLE"))?,
            _zone_id,
        )
        .await;
    }
    if action == "consumer.delete" {
        return super::projection::apply_mail_consumer_delete(
            payload,
            worker_pool
                .mail_runtime
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
            worker_pool
                .mail_runtime
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
            worker_pool
                .mail_runtime
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
            worker_pool
                .mail_runtime
                .configuration
                .zone_kv()
                .ok_or_else(|| failed("MAIL_ZONE_KV_UNAVAILABLE"))?,
            _zone_id,
        )
        .await;
    }

    // [COMMENT]: Delivery mail chỉ chạy qua ordinary broker consumer runtime; Redis Job không còn direct/system mail action.
    Err(failed(format!("unsupported mail action: {action}")))
}

fn failed(message: impl Into<String>) -> ExecutorError {
    ExecutorError::ExecutionFailed(message.into())
}
