use crate::infra::centrifugo::CentrifugoClient;
use crate::observability::logger::Logger;

/// [COMMENT]: Runtime notification là customer-safe wake-up signal. Notification Service không
/// forward field lạ để physical node identity hay broker secret lọt ra WebSocket.
pub async fn handle_consumer_runtime(
    centrifugo_client: &CentrifugoClient,
    user_id: &str,
    payload: serde_json::Value,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    if uuid::Uuid::parse_str(user_id).is_err() {
        return Ok(());
    }
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
        && observed_at.is_some_and(|value| chrono::DateTime::parse_from_rfc3339(value).is_ok())
        && expires_at.is_some_and(|value| chrono::DateTime::parse_from_rfc3339(value).is_ok());
    if !valid {
        Logger::sys_warn(
            "mail_service.runtime_rejected",
            "Rejected malformed Mail runtime notification",
            "MAIL_RUNTIME_NOTIFICATION_INVALID",
        );
        return Ok(());
    }

    // [COMMENT]: Sau validation vẫn dựng object mới từ allowlist; payload gốc không được forward.
    let client_event = serde_json::json!({
        "event_type": "mail.consumer.runtime.changed",
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
    });
    let channel_name = format!("personal:{user_id}");
    match centrifugo_client.publish(&channel_name, client_event).await {
        Ok(()) => {
            crate::observability::metrics::MetricsManager::record_centrifugo_publish("success");
            Ok(())
        }
        Err(error) => {
            crate::observability::metrics::MetricsManager::record_centrifugo_publish("failed");
            Logger::sys_error(
                "mail_service.runtime_fail",
                "Could not publish Mail runtime snapshot to Centrifugo",
                &error.to_string(),
            );
            Err(Box::new(error))
        }
    }
}
