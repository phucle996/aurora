use super::model::PreparedMail;
use super::template::{get_template, render_html, render_subject};
use crate::executor::{ExecutionResult, ExecutorError};
use crate::infra::redis::RedisClientManager;
use crate::job_lifecycle::message::JobPayload;
use crate::workerpool::lifecycle::WorkerLifecycleManager;
use prost::Message;
use std::sync::Arc;

pub mod mail_proto {
    include!(concat!(env!("OUT_DIR"), "/mail.rs"));
}

pub async fn dispatch_mail_job(
    action: &str,
    payload: JobPayload,
    worker_pool: Arc<WorkerLifecycleManager>,
    _redis_job: Arc<RedisClientManager>,
    _zone_id: &str,
) -> Result<ExecutionResult, ExecutorError> {
    // [COMMENT]: Projection là control path riêng; không decode nhầm desired-state thành SendMailConfig.
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
    if action != "send" && action != "verify_account" && !action.starts_with("system.") {
        return Err(failed(format!("unsupported mail action: {action}")));
    }
    let config = mail_proto::SendMailConfig::decode(payload.payload.as_slice())
        .map_err(|error| failed(format!("decode SendMailConfig failed: {error}")))?;
    uuid::Uuid::parse_str(&payload.job_id).map_err(|_| failed("MAIL_JOB_ID_INVALID"))?;
    let sender = &worker_pool.mail_runtime.sender;
    if config.sender_profile_id != sender.id {
        return Err(failed("MAIL_SENDER_NOT_FOUND"));
    }
    if config.sender_version != sender.version {
        return Err(failed("MAIL_SENDER_VERSION_MISMATCH"));
    }

    let recipient = config
        .recipient
        .trim()
        .parse::<lettre::message::Mailbox>()
        .map_err(|_| failed("MAIL_RECIPIENT_INVALID"))?
        .email
        .to_string();
    let (subject, text_body, html_body) = if !config.template_id.trim().is_empty() {
        let zone_kv = worker_pool
            .mail_runtime
            .configuration
            .zone_kv()
            .ok_or_else(|| failed("MAIL_ZONE_KV_UNAVAILABLE"))?;
        let template = get_template(&zone_kv, config.template_id.trim())
            .await
            .map_err(|error| failed(format!("MAIL_TEMPLATE_UNAVAILABLE: {error}")))?;
        (
            render_subject(&template.subject, &config.template_variables),
            None,
            Some(render_html(&template.body, &config.template_variables)),
        )
    } else {
        (
            config.subject.trim().to_string(),
            non_empty(config.text_body),
            non_empty(config.html_body),
        )
    };
    if subject.is_empty()
        || subject.contains(['\r', '\n'])
        || (text_body.is_none() && html_body.is_none())
    {
        return Err(failed("MAIL_CONTENT_REQUIRED"));
    }
    if subject.len() > 998 {
        return Err(failed("MAIL_SUBJECT_TOO_LARGE"));
    }
    let estimated_bytes = subject.len()
        + recipient.len()
        + text_body.as_ref().map_or(0, String::len)
        + html_body.as_ref().map_or(0, String::len)
        + 1024;
    let max_message_bytes = crate::config::Config::get_global().mail_max_message_bytes;
    if estimated_bytes > max_message_bytes {
        return Err(failed("MAIL_MESSAGE_TOO_LARGE"));
    }

    let result = worker_pool
        .mail_runtime
        .batcher
        .submit(PreparedMail {
            job_id: payload.job_id,
            recipient,
            subject,
            text_body,
            html_body,
            estimated_bytes,
        })
        .await;
    match result {
        Ok(accepted) => Ok(ExecutionResult {
            message: format!("JMAP submission accepted: {}", accepted.submission_id),
        }),
        Err(error) => Err(failed(format!(
            "{} (retryable={}): {}",
            error.code, error.retryable, error.message
        ))),
    }
}

fn non_empty(value: String) -> Option<String> {
    if value.trim().is_empty() {
        None
    } else {
        Some(value)
    }
}

fn failed(message: impl Into<String>) -> ExecutorError {
    ExecutorError::ExecutionFailed(message.into())
}
