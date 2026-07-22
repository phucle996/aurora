use crate::executor::mail::runtime_proto::MailTemplateVersionPublishedV1;
use crate::infra::zone_kv::{TemplateConfigHead, ZoneKvStore};
use moka::future::Cache;
use prost::Message;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::{Arc, OnceLock};
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

/// [COMMENT]: Mail send path chỉ đọc immutable snapshot trong NATS KV; không còn request/response qua Redis hay history DB.
pub async fn get_template(
    zone_kv: &Arc<ZoneKvStore>,
    template_id: &str,
) -> Result<MailTemplate, String> {
    let head_key = format!("mail.template.head.{template_id}");
    let head_bytes = zone_kv
        .config_get(head_key)
        .await?
        .ok_or_else(|| "mail template head missing".to_string())?;
    let head: TemplateConfigHead = serde_json::from_slice(&head_bytes)
        .map_err(|error| format!("mail template head invalid: {error}"))?;
    if head.tombstoned || head.current_version == 0 {
        return Err("mail template is deleted or unpublished".to_string());
    }
    // [COMMENT]: Cache key pin version+hash; hard-delete/new publish luôn đọc head trước nên không dùng content cũ chỉ vì TTL L1.
    let key = format!(
        "{template_id}:v{}:{}",
        head.current_version, head.content_sha256
    );
    let zone_kv = zone_kv.clone();
    let template_id = template_id.to_string();
    l1_cache()
        .try_get_with(key, async move {
            let snapshot_key = format!(
                "mail.template.snapshot.{template_id}.v{}",
                head.current_version
            );
            let snapshot = zone_kv
                .config_get(snapshot_key)
                .await?
                .ok_or_else(|| "mail template snapshot missing".to_string())?;
            let event = MailTemplateVersionPublishedV1::decode(snapshot.as_ref())
                .map_err(|error| format!("mail template snapshot invalid: {error}"))?;
            if event.template_id != template_id
                || event.template_version != head.current_version
                || event.subject_template.contains(['\r', '\n'])
                || event.html_template.is_empty()
                || event.content_sha256.len() != 32
                || event
                    .content_sha256
                    .iter()
                    .map(|byte| format!("{byte:02x}"))
                    .collect::<String>()
                    != head.content_sha256
                || crate::executor::mail::runtime::configuration::canonical_template_sha256(
                    &event.subject_template,
                    &event.html_template,
                )
                .as_slice()
                    != event.content_sha256.as_slice()
            {
                return Err("mail template snapshot integrity mismatch".to_string());
            }
            Ok(MailTemplate {
                subject: event.subject_template,
                body: event.html_template,
            })
        })
        .await
        .map_err(|error: Arc<String>| (*error).clone())
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
#[path = "../test/template.rs"]
mod tests;
