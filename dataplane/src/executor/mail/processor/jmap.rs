use super::model::{MailAccepted, MailSubmitError, MailSubmitResult, PreparedMail, SenderProfile};
use crate::config::Config;
use reqwest::{Client, RequestBuilder, StatusCode};
use serde_json::{json, Map, Value};
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

#[derive(Clone)]
enum JmapAuth {
    Bearer(String),
    Basic { username: String, password: String },
}

pub struct JmapClient {
    http: Client,
    endpoint: String,
    auth: JmapAuth,
    sender: Arc<SenderProfile>,
    max_retries: usize,
}

impl JmapClient {
    pub fn new(config: &Config, sender: Arc<SenderProfile>) -> Result<Self, String> {
        let endpoint = config
            .stalwart_jmap_url
            .trim()
            .trim_end_matches('/')
            .to_string();
        if endpoint.is_empty() {
            return Err("STALWART_JMAP_URL is required".to_string());
        }
        let auth = if !config.stalwart_jmap_bearer_token.trim().is_empty() {
            JmapAuth::Bearer(config.stalwart_jmap_bearer_token.clone())
        } else if !config.stalwart_jmap_username.trim().is_empty()
            && !config.stalwart_jmap_password.is_empty()
        {
            JmapAuth::Basic {
                username: config.stalwart_jmap_username.clone(),
                password: config.stalwart_jmap_password.clone(),
            }
        } else {
            return Err(
                "JMAP requires STALWART_JMAP_BEARER_TOKEN or username/password".to_string(),
            );
        };
        let http = Client::builder()
            .connect_timeout(Duration::from_secs(3))
            .timeout(Duration::from_millis(
                config.mail_jmap_request_timeout_ms.max(1),
            ))
            .pool_idle_timeout(Duration::from_secs(90))
            .tcp_keepalive(Duration::from_secs(30))
            .build()
            .map_err(|error| format!("build shared JMAP HTTP client failed: {error}"))?;
        Ok(Self {
            http,
            endpoint,
            auth,
            sender,
            max_retries: config.mail_jmap_max_retries,
        })
    }

    fn request(&self) -> RequestBuilder {
        let request = self.http.post(&self.endpoint);
        match &self.auth {
            JmapAuth::Bearer(token) => request.bearer_auth(token),
            JmapAuth::Basic { username, password } => request.basic_auth(username, Some(password)),
        }
    }

    pub async fn healthcheck(&self) -> Result<(), String> {
        let payload = json!({
            "using": ["urn:ietf:params:jmap:core"],
            "methodCalls": [["Core/echo", {"dataplane": "mail"}, "health"]]
        });
        let response = self
            .request()
            .json(&payload)
            .send()
            .await
            .map_err(|error| format!("JMAP health request failed: {error}"))?;
        if response.status().is_success() {
            Ok(())
        } else {
            Err(format!("JMAP health returned HTTP {}", response.status()))
        }
    }

    pub async fn submit_batch(&self, mails: &[PreparedMail]) -> Vec<MailSubmitResult> {
        if mails.is_empty() {
            return Vec::new();
        }
        let payload = self.build_batch_request(mails);
        let mut last_error = MailSubmitError::new("MAIL_JMAP_UNAVAILABLE", true);

        for attempt in 0..=self.max_retries {
            match self.request().json(&payload).send().await {
                Ok(response) if response.status().is_success() => {
                    return match response.json::<Value>().await {
                        Ok(value) => self.parse_batch_response(mails, &value),
                        Err(_error) => {
                            vec![
                                Err(MailSubmitError::new("MAIL_JMAP_INVALID_RESPONSE", true,));
                                mails.len()
                            ]
                        }
                    };
                }
                Ok(response) => {
                    let status = response.status();
                    let retryable =
                        status == StatusCode::TOO_MANY_REQUESTS || status.is_server_error();
                    last_error = MailSubmitError::new(
                        format!("MAIL_JMAP_HTTP_{}", status.as_u16()),
                        retryable,
                    );
                    if !retryable {
                        break;
                    }
                }
                Err(_error) => {
                    last_error = MailSubmitError::new("MAIL_JMAP_TRANSPORT", true);
                }
            }
            if attempt < self.max_retries {
                // [COMMENT]: Jitter theo attempt tránh các Dataplane replica retry đồng nhịp khi Stalwart hồi phục.
                let jitter = rand::random::<u64>() % 100;
                tokio::time::sleep(Duration::from_millis(
                    100 * (1_u64 << attempt.min(5)) + jitter,
                ))
                .await;
            }
        }
        vec![Err(last_error); mails.len()]
    }

    fn build_batch_request(&self, mails: &[PreparedMail]) -> Value {
        let mut email_creates = Map::new();
        let mut submission_creates = Map::new();
        let mut destroy_after_submit = Vec::with_capacity(mails.len());

        for mail in mails {
            let key = creation_key(&mail.job_id);
            let email_key = format!("mail-{key}");
            let submission_key = format!("submit-{key}");
            let mut body_values = Map::new();
            let mut text_body = Vec::new();
            let mut html_body = Vec::new();
            if let Some(text) = &mail.text_body {
                body_values.insert(
                    "text".to_string(),
                    json!({"value": text, "isTruncated": false}),
                );
                text_body.push(json!({"partId": "text", "type": "text/plain"}));
            }
            if let Some(html) = &mail.html_body {
                body_values.insert(
                    "html".to_string(),
                    json!({"value": html, "isTruncated": false}),
                );
                html_body.push(json!({"partId": "html", "type": "text/html"}));
            }
            email_creates.insert(
                email_key.clone(),
                json!({
                    "mailboxIds": {self.sender.mailbox_id.clone(): true},
                    "keywords": {"$draft": true},
                    "from": [{"email": self.sender.from_address}],
                    "to": [{"email": mail.recipient}],
                    "subject": mail.subject,
                    "bodyValues": body_values,
                    "textBody": text_body,
                    "htmlBody": html_body
                }),
            );
            submission_creates.insert(
                submission_key.clone(),
                json!({
                    "emailId": format!("#{email_key}"),
                    "identityId": self.sender.identity_id,
                    "envelope": {
                        "mailFrom": {"email": self.sender.from_address},
                        "rcptTo": [{"email": mail.recipient}]
                    }
                }),
            );
            // [COMMENT]: Bulk sender không giữ bản sao mailbox; Stalwart destroy Email sau khi queue submission thành công.
            destroy_after_submit.push(Value::String(format!("#{submission_key}")));
        }

        json!({
            "using": [
                "urn:ietf:params:jmap:core",
                "urn:ietf:params:jmap:mail",
                "urn:ietf:params:jmap:submission"
            ],
            "methodCalls": [
                ["Email/set", {
                    "accountId": self.sender.account_id,
                    "create": email_creates
                }, "create-mails"],
                ["EmailSubmission/set", {
                    "accountId": self.sender.account_id,
                    "create": submission_creates,
                    "onSuccessDestroyEmail": destroy_after_submit
                }, "submit-mails"]
            ]
        })
    }

    fn parse_batch_response(
        &self,
        mails: &[PreparedMail],
        response: &Value,
    ) -> Vec<MailSubmitResult> {
        let mut by_key: HashMap<String, MailSubmitResult> = HashMap::new();
        let method_responses = match response.get("methodResponses").and_then(Value::as_array) {
            Some(value) => value,
            None => {
                return vec![
                    Err(MailSubmitError::new("MAIL_JMAP_INVALID_RESPONSE", true,));
                    mails.len()
                ]
            }
        };

        for method in method_responses {
            let parts = match method.as_array() {
                Some(parts) if parts.len() >= 3 => parts,
                _ => continue,
            };
            let name = parts[0].as_str().unwrap_or_default();
            let call_id = parts[2].as_str().unwrap_or_default();
            if call_id != "submit-mails" {
                continue;
            }
            if name == "error" {
                let error_type = parts[1]
                    .get("type")
                    .and_then(Value::as_str)
                    .unwrap_or("methodError");
                for mail in mails {
                    by_key.insert(
                        creation_key(&mail.job_id),
                        Err(MailSubmitError::new(
                            "MAIL_JMAP_METHOD_ERROR",
                            is_retryable_jmap_error(error_type),
                        )),
                    );
                }
                break;
            }
            let args = &parts[1];
            if let Some(created) = args.get("created").and_then(Value::as_object) {
                for (submission_key, _) in created {
                    if let Some(key) = submission_key.strip_prefix("submit-") {
                        // [COMMENT]: Current phase không lưu delivery history; created key là đủ để settle item.
                        by_key.insert(key.to_string(), Ok(MailAccepted));
                    }
                }
            }
            if let Some(not_created) = args.get("notCreated").and_then(Value::as_object) {
                for (submission_key, value) in not_created {
                    if let Some(key) = submission_key.strip_prefix("submit-") {
                        let error_type = value
                            .get("type")
                            .and_then(Value::as_str)
                            .unwrap_or("notCreated");
                        by_key.insert(
                            key.to_string(),
                            Err(MailSubmitError::new(
                                "MAIL_JMAP_SUBMISSION_REJECTED",
                                is_retryable_jmap_error(error_type),
                            )),
                        );
                    }
                }
            }
        }

        mails
            .iter()
            .map(|mail| {
                by_key
                    .remove(&creation_key(&mail.job_id))
                    .unwrap_or_else(|| Err(MailSubmitError::new("MAIL_JMAP_RESULT_MISSING", true)))
            })
            .collect()
    }
}

fn creation_key(job_id: &str) -> String {
    job_id
        .chars()
        .filter(|character| character.is_ascii_alphanumeric())
        .collect()
}

fn is_retryable_jmap_error(error_type: &str) -> bool {
    matches!(
        error_type,
        "serverFail" | "serverPartialFail" | "rateLimit" | "tooManyRequests"
    )
}

#[cfg(test)]
#[path = "../test/jmap.rs"]
mod tests;
