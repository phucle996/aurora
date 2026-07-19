use crate::infra::redis::RedisClientManager;
use crate::observability::logger::Logger;
use futures_util::StreamExt;
use moka::future::Cache;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::OnceLock;
use std::time::Duration;

#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct MailTemplate {
    pub subject: String,
    pub body: String,
}

fn l1_cache() -> &'static Cache<String, MailTemplate> {
    static CACHE: OnceLock<Cache<String, MailTemplate>> = OnceLock::new();
    CACHE.get_or_init(|| {
        Cache::builder()
            .max_capacity(10_000)
            .time_to_live(Duration::from_secs(3600))
            .build()
    })
}

/// [COMMENT]: Moka try_get_with coalesce concurrent cache miss cùng template_id thành một lần đọc L2/PubSub.
pub async fn get_template(
    redis_mgr: &RedisClientManager,
    template_id: &str,
) -> Result<MailTemplate, String> {
    l1_cache()
        .try_get_with(
            template_id.to_string(),
            load_template(redis_mgr, template_id),
        )
        .await
        .map_err(|error: std::sync::Arc<String>| (*error).clone())
}

#[allow(deprecated)]
async fn load_template(
    redis_mgr: &RedisClientManager,
    template_id: &str,
) -> Result<MailTemplate, String> {
    let client = redis_mgr.client();
    let redis_key = format!("cache:mail_template:v2:{template_id}");
    let mut conn = client
        .get_multiplexed_async_connection()
        .await
        .map_err(|error| format!("mail template Redis unavailable: {error}"))?;
    if let Ok(Some(cached_json)) = redis::cmd("GET")
        .arg(&redis_key)
        .query_async::<_, Option<String>>(&mut conn)
        .await
    {
        if let Ok(template) = serde_json::from_str::<MailTemplate>(&cached_json) {
            return Ok(template);
        }
    }

    let request_id = uuid::Uuid::new_v4().to_string();
    let response_channel = format!("mail.template.response:{request_id}");
    let conn_pubsub = client
        .get_async_connection()
        .await
        .map_err(|error| format!("mail template PubSub unavailable: {error}"))?;
    let mut pubsub = conn_pubsub.into_pubsub();
    pubsub
        .subscribe(&response_channel)
        .await
        .map_err(|error| format!("subscribe mail template response failed: {error}"))?;
    let trace_id =
        crate::observability::otel::OtelTracer::get_current_trace_id().unwrap_or_default();
    let request = serde_json::json!({
        "request_id": request_id,
        "template_id": template_id,
        "reply_to": response_channel,
        "trace_id": trace_id
    });
    redis::cmd("PUBLISH")
        .arg("mail.template.request")
        .arg(request.to_string())
        .query_async::<_, ()>(&mut conn)
        .await
        .map_err(|error| format!("publish mail template request failed: {error}"))?;

    let mut stream = pubsub.on_message();
    let template = tokio::time::timeout(Duration::from_secs(5), async move {
        let message = stream
            .next()
            .await
            .ok_or_else(|| "mail template response channel closed".to_string())?;
        let payload: String = message
            .get_payload()
            .map_err(|error| format!("decode mail template response failed: {error}"))?;
        let value: serde_json::Value = serde_json::from_str(&payload)
            .map_err(|error| format!("parse mail template response failed: {error}"))?;
        Ok::<MailTemplate, String>(MailTemplate {
            subject: value
                .get("subject")
                .and_then(serde_json::Value::as_str)
                .unwrap_or("No Subject")
                .to_string(),
            body: value
                .get("content")
                .or_else(|| value.get("body"))
                .and_then(serde_json::Value::as_str)
                .unwrap_or_default()
                .to_string(),
        })
    })
    .await
    .map_err(|_| "timeout waiting for mail template".to_string())??;

    if let Ok(serialized) = serde_json::to_string(&template) {
        let _: redis::RedisResult<()> = redis::cmd("SETEX")
            .arg(&redis_key)
            .arg(3600)
            .arg(serialized)
            .query_async(&mut conn)
            .await;
    }
    Logger::sys_info(
        "executor.mail.template",
        &format!("Loaded template_id={template_id} into bounded L1 cache"),
    );
    Ok(template)
}

pub fn render_subject(template: &str, variables: &HashMap<String, String>) -> String {
    render(template, variables, false)
}

pub fn render_html(template: &str, variables: &HashMap<String, String>) -> String {
    render(template, variables, true)
}

fn render(template: &str, variables: &HashMap<String, String>, escape_html: bool) -> String {
    let mut rendered = template.to_string();
    for (key, value) in variables {
        let placeholder = format!("{{{{{key}}}}}");
        let value = if escape_html {
            html_escape(value)
        } else {
            value.clone()
        };
        rendered = rendered.replace(&placeholder, &value);
    }
    rendered
}

fn html_escape(value: &str) -> String {
    value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&#39;")
}

#[cfg(test)]
mod tests {
    use super::{render_html, render_subject};
    use std::collections::HashMap;

    #[test]
    fn html_variables_are_escaped_but_subject_is_plain_text() {
        let variables = HashMap::from([("name".to_string(), "<Alice & Bob>".to_string())]);
        assert_eq!(
            render_html("<p>{{name}}</p>", &variables),
            "<p>&lt;Alice &amp; Bob&gt;</p>"
        );
        assert_eq!(
            render_subject("Hello {{name}}", &variables),
            "Hello <Alice & Bob>"
        );
    }
}
