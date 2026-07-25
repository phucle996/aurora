use crate::application::ports::{AppError, RealtimePublisher};
use crate::contract::realtime::{runtime_channel, RealtimeEnvelope, RealtimeKind};
use crate::observability::logger::Logger;
use chrono::DateTime;
use std::sync::Arc;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DispatchOutcome {
    Published,
    Dropped,
}

/// Runtime updates are soft state. They have a separate path so malformed or
/// high-rate telemetry can never block durable job notification delivery.
pub struct RuntimeUpdateService {
    publisher: Arc<dyn RealtimePublisher>,
}

impl RuntimeUpdateService {
    pub fn new(publisher: Arc<dyn RealtimePublisher>) -> Self {
        Self { publisher }
    }

    pub async fn dispatch(&self, envelope: RealtimeEnvelope) -> Result<DispatchOutcome, AppError> {
        let kind = envelope
            .kind()
            .ok_or_else(|| boxed_error("invalid realtime kind"))?;
        let payload = match kind {
            RealtimeKind::Storage => {
                let mut payload = envelope.payload;
                let object = payload
                    .as_object_mut()
                    .ok_or_else(|| boxed_error("storage payload must be an object"))?;
                object.insert(
                    "event_type".to_string(),
                    serde_json::Value::String("storage.bucket.sizes.sync".to_string()),
                );
                object.insert(
                    "stream".to_string(),
                    serde_json::Value::String("runtime".to_string()),
                );
                payload
            }
            RealtimeKind::MailRuntime => {
                let Some(payload) = validate_mail_runtime(&envelope.payload) else {
                    Logger::sys_warn(
                        "notification.mail_runtime_dropped",
                        "Dropped malformed Mail runtime notification",
                        "MAIL_RUNTIME_NOTIFICATION_INVALID",
                    );
                    return Ok(DispatchOutcome::Dropped);
                };
                payload
            }
        };

        validate_user_id(&envelope.user_id)?;
        self.publisher
            .publish(&runtime_channel(&envelope.user_id), payload)
            .await?;
        Ok(DispatchOutcome::Published)
    }
}

fn validate_user_id(user_id: &str) -> Result<(), AppError> {
    if uuid::Uuid::parse_str(user_id).is_err() {
        return Err(boxed_error("runtime update user id is not a UUID"));
    }
    Ok(())
}

fn validate_mail_runtime(payload: &serde_json::Value) -> Option<serde_json::Value> {
    let scope = payload.get("scope").and_then(serde_json::Value::as_str);
    let consumer_id = payload
        .get("consumer_id")
        .and_then(serde_json::Value::as_str);
    let config_version = payload
        .get("config_version")
        .and_then(serde_json::Value::as_u64);
    let runtime_epoch = payload
        .get("runtime_epoch")
        .and_then(serde_json::Value::as_str);
    let runtime_revision = payload
        .get("runtime_revision")
        .and_then(serde_json::Value::as_u64);
    let state = payload.get("state").and_then(serde_json::Value::as_str);
    let active_instances = payload
        .get("active_instances")
        .and_then(serde_json::Value::as_u64);
    let consumer_lag = payload
        .get("consumer_lag")
        .and_then(serde_json::Value::as_u64);
    let error_code = payload
        .get("error_code")
        .and_then(serde_json::Value::as_str);
    let error_message = payload
        .get("error_message")
        .and_then(serde_json::Value::as_str);
    let observed_at = payload
        .get("observed_at")
        .and_then(serde_json::Value::as_str);
    let expires_at = payload
        .get("expires_at")
        .and_then(serde_json::Value::as_str);

    let valid = matches!(scope, Some("personal" | "tenant"))
        && consumer_id.is_some_and(|value| uuid::Uuid::parse_str(value).is_ok())
        && config_version.is_some_and(|value| value > 0)
        && runtime_epoch.is_some_and(|value| uuid::Uuid::parse_str(value).is_ok())
        && runtime_revision.is_some_and(|value| value > 0)
        && matches!(
            state,
            Some("stopped" | "starting" | "running" | "paused" | "draining" | "error" | "degraded")
        )
        && active_instances.is_some_and(|value| value <= 256)
        && consumer_lag.is_some()
        && error_code.is_some_and(|value| {
            value.len() <= 100
                && value
                    .bytes()
                    .all(|byte| byte.is_ascii_uppercase() || byte.is_ascii_digit() || byte == b'_')
        })
        && error_message
            .is_some_and(|value| value.len() <= 1_024 && !value.chars().any(char::is_control))
        && observed_at.is_some_and(|value| DateTime::parse_from_rfc3339(value).is_ok())
        && expires_at.is_some_and(|value| DateTime::parse_from_rfc3339(value).is_ok());
    if !valid {
        return None;
    }

    Some(serde_json::json!({
        "event_type": "mail.consumer.runtime.changed",
        "stream": "runtime",
        "scope": scope,
        "consumer_id": consumer_id,
        "config_version": config_version,
        "runtime_epoch": runtime_epoch,
        "runtime_revision": runtime_revision,
        "state": state,
        "active_instances": active_instances,
        "consumer_lag": consumer_lag,
        "error_code": error_code,
        "error_message": error_message,
        "observed_at": observed_at,
        "expires_at": expires_at,
    }))
}

fn boxed_error(message: &str) -> AppError {
    std::io::Error::new(std::io::ErrorKind::InvalidData, message.to_owned()).into()
}
