use std::{collections::BTreeMap, time::Duration};

use reqwest::Client;
use serde_json::Value;
use sha2::Digest;
use thiserror::Error;
use tokio::time::timeout;
use url::Url;

use crate::{
    config::Config,
    contract::{RuntimeEvent, RuntimeScope},
};

#[derive(Debug, Error)]
pub enum SourceError {
    #[error("victoria request failed")]
    Request(#[from] reqwest::Error),
    #[error("victoria response was not successful")]
    Status,
    #[error("runtime scope cannot be rendered into a query")]
    Scope,
}

#[derive(Clone)]
pub struct VictoriaSource {
    client: Client,
    metrics_url: Url,
    logs_url: Url,
    timeout: Duration,
    live_window: Duration,
    max_event_bytes: usize,
    max_log_lines: usize,
}

impl VictoriaSource {
    pub fn new(config: &Config) -> Result<Self, SourceError> {
        let client = Client::builder()
            .connect_timeout(Duration::from_secs(2))
            .timeout(Duration::from_secs(5))
            .build()?;
        let metrics_url =
            Url::parse(&config.victoria_metrics_url).map_err(|_| SourceError::Scope)?;
        let logs_url = Url::parse(&config.victoria_logs_url).map_err(|_| SourceError::Scope)?;
        Ok(Self {
            client,
            metrics_url,
            logs_url,
            timeout: Duration::from_secs(5),
            live_window: config.query_interval,
            max_event_bytes: config.max_event_bytes,
            max_log_lines: config.max_log_lines,
        })
    }

    pub async fn read(
        &self,
        scope: &RuntimeScope,
        event_id: String,
        initial_snapshot: bool,
    ) -> Result<RuntimeEvent, SourceError> {
        let query = fixed_query(scope)?;
        let endpoint = match scope.panel_id.as_str() {
            "logs" => self
                .logs_url
                .join("select/logsql/query")
                .map_err(|_| SourceError::Scope)?,
            _ => self
                .metrics_url
                .join("api/v1/query_range")
                .map_err(|_| SourceError::Scope)?,
        };
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs();
        // Only the first read uses the requested snapshot window. Live reads use
        // one bounded polling interval so fan-out does not repeatedly download
        // the full historical range for every SSE tick.
        let window_seconds = if initial_snapshot {
            scope.snapshot_seconds.max(1)
        } else {
            self.live_window.as_secs().max(1)
        };
        let start = now.saturating_sub(window_seconds);
        let mut request = self.client.get(endpoint).query(&[("query", query)]);
        if scope.panel_id == "logs" {
            request = request.query(&[
                ("start", start.to_string()),
                ("end", now.to_string()),
                ("limit", self.max_log_lines.to_string()),
            ]);
        } else {
            request = request.query(&[
                ("start", start.to_string()),
                ("end", now.to_string()),
                ("step", "1".to_string()),
            ]);
        }
        let response = timeout(self.timeout, request.send())
            .await
            .map_err(|_| SourceError::Status)??;
        if !response.status().is_success() {
            return Err(SourceError::Status);
        }
        let value: Value = response.json().await?;
        let mut payload = BTreeMap::new();
        payload.insert("source".into(), Value::String("victoria".into()));
        payload.insert("panel_id".into(), Value::String(scope.panel_id.clone()));
        payload.insert("data".into(), bounded_value(value, self.max_event_bytes));
        Ok(RuntimeEvent {
            schema_version: 1,
            module: scope.module.clone(),
            resource_type: scope.resource_type.clone(),
            resource_id: scope.resource_id,
            component_id: scope.component_id.clone(),
            event_type: if initial_snapshot {
                "runtime.snapshot".into()
            } else if scope.panel_id == "logs" {
                "runtime.log".into()
            } else {
                "runtime.metric".into()
            },
            event_id,
            observed_at: chrono_like_now(),
            payload,
        })
    }
}

fn fixed_query(scope: &RuntimeScope) -> Result<String, SourceError> {
    let module = query_label(&scope.module)?;
    let resource_type = query_label(&scope.resource_type)?;
    let resource_id = scope.resource_id.to_string();
    let owner_id = scope.owner_id.to_string();
    let workspace_id = scope.workspace_id.to_string();
    let component = scope
        .component_id
        .as_deref()
        .map(query_regex_label)
        .transpose()?
        .unwrap_or_else(|| ".*".to_string());
    let metric = match scope.panel_id.as_str() {
        "health" => "aurora_runtime_health",
        "metrics" => "aurora_runtime_metric",
        "logs" => "{_stream=~\"aurora_runtime_logs\"}",
        "events" => "aurora_runtime_event",
        _ => return Err(SourceError::Scope),
    };
    if scope.panel_id == "logs" {
        return Ok(format!(
            "{metric} aurora_module=\"{module}\", aurora_resource_type=\"{resource_type}\", aurora_resource_id=\"{resource_id}\", aurora_owner_id=\"{owner_id}\", aurora_workspace_id=\"{workspace_id}\", aurora_component_id=~\"{component}\""
        ));
    }
    Ok(format!(
        "{metric}{{aurora_module=\"{module}\",aurora_resource_type=\"{resource_type}\",aurora_resource_id=\"{resource_id}\",aurora_owner_id=\"{owner_id}\",aurora_workspace_id=\"{workspace_id}\",aurora_component_id=~\"{component}\"}}"
    ))
}

fn query_label(value: &str) -> Result<String, SourceError> {
    if value.is_empty()
        || value.len() > 128
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
    {
        return Err(SourceError::Scope);
    }
    Ok(value.to_string())
}

fn query_regex_label(value: &str) -> Result<String, SourceError> {
    let value = query_label(value)?;
    // Component IDs are inserted into a regex matcher. Escape regex syntax so
    // a trusted-but-unusual component name cannot widen the Victoria query.
    Ok(regex_escape(&value))
}

fn regex_escape(value: &str) -> String {
    let mut escaped = String::with_capacity(value.len());
    for character in value.chars() {
        if matches!(
            character,
            '\\' | '.' | '^' | '$' | '*' | '+' | '?' | '(' | ')' | '[' | ']' | '{' | '}' | '|'
        ) {
            escaped.push('\\');
        }
        escaped.push(character);
    }
    escaped
}

fn bounded_value(value: Value, max_bytes: usize) -> Value {
    let encoded = serde_json::to_vec(&value).unwrap_or_default();
    if encoded.len() <= max_bytes {
        return value;
    }
    serde_json::json!({
        "truncated": true,
        "sha256": format!("{:x}", sha2::Sha256::digest(&encoded)),
        "bytes": encoded.len()
    })
}

fn chrono_like_now() -> String {
    chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true)
}

#[cfg(test)]
mod tests {
    use super::*;
    use uuid::Uuid;

    fn scope(module: &str) -> RuntimeScope {
        RuntimeScope {
            module: module.into(),
            resource_type: "instance".into(),
            resource_id: Uuid::new_v4(),
            owner_id: Uuid::new_v4(),
            workspace_id: Uuid::new_v4(),
            zone_id: Uuid::new_v4(),
            component_id: Some("broker".into()),
            panel_id: "metrics".into(),
            snapshot_seconds: 60,
        }
    }

    #[test]
    fn query_builder_rejects_untrusted_label_syntax() {
        assert!(fixed_query(&scope("managed_service")).is_ok());
        assert!(fixed_query(&scope("managed\"_service")).is_err());
    }

    #[test]
    fn component_regex_is_exactly_escaped() {
        let mut scoped = scope("managed_service");
        scoped.component_id = Some("broker.v1".into());
        let rendered = fixed_query(&scoped).unwrap();
        assert!(rendered.contains("aurora_component_id=~\"broker\\.v1\""));
    }

    #[test]
    fn oversized_payload_is_reduced_to_digest() {
        let value = Value::String("x".repeat(32));
        let bounded = bounded_value(value, 8);
        assert_eq!(bounded["truncated"], true);
        assert!(bounded["sha256"].is_string());
    }
}
