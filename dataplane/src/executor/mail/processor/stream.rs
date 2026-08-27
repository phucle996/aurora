use super::batcher::MailBatcherHandle;
use super::model::{PreparedMail, SenderProfile};
use crate::config::Config;
use crate::executor::mail::metering::AcceptedUsagePublisher;
use crate::executor::mail::runtime::configuration::{
    MailConfigurationRuntime, RuntimeConsumerConfiguration, RuntimeDesiredState,
    RuntimeTemplateSnapshot, RuntimeTemplateToken,
};
use crate::executor::mail::runtime::context::RuntimeGenerationFence;
use crate::executor::mail::runtime_proto::MailDispatchEnvelopeV1;
use crate::infra::zone_kv::ZoneKvStore;
use bytes::Bytes;
use opentelemetry::metrics::Counter;
use opentelemetry::{global, KeyValue};
use prost::Message;
use serde::de::{self, MapAccess, Visitor};
use serde::{Deserialize, Deserializer};
use std::collections::{HashMap, HashSet};
use std::fmt;
use std::sync::{Arc, OnceLock};
use tokio::sync::Semaphore;

const MAX_RECIPIENT_BYTES: usize = 320;
const MAX_PARAMETER_COUNT: usize = 256;
const MAX_PARAMETER_KEY_BYTES: usize = 128;
const MAX_PARAMETER_VALUE_BYTES: usize = 8 * 1024;
const MAX_PARAMETER_TOTAL_BYTES: usize = 64 * 1024;
static PROCESSING_OUTCOME: OnceLock<Counter<u64>> = OnceLock::new();

fn processing_outcome_metric() -> &'static Counter<u64> {
    PROCESSING_OUTCOME.get_or_init(|| {
        global::meter("aurora-dataplane")
            .u64_counter("mail_stream_processing_outcome_total")
            .with_description("Phase-7 fixed-envelope/render/JMAP outcomes")
            .init()
    })
}

#[derive(Clone, Debug)]
pub enum MailProcessingStatus {
    Accepted,
    PermanentRejected { code: &'static str },
    Retryable { code: &'static str },
    Ambiguous { code: &'static str },
}

#[derive(Debug)]
struct FixedMailEnvelope {
    event_id: Option<[u8; 16]>,
    to: String,
    parameter: HashMap<String, String>,
    // [COMMENT]: Optional broker-level expiry prevents delayed verification/reset mail from being sent after token TTL.
    not_after_unix_ms: Option<u64>,
}

struct FlatParameters(HashMap<String, String>);

struct ScalarParameter(String);

impl<'de> Deserialize<'de> for ScalarParameter {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        struct ScalarVisitor;

        impl<'de> Visitor<'de> for ScalarVisitor {
            type Value = ScalarParameter;

            fn expecting(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str("a string, JSON number, or boolean")
            }

            fn visit_str<E>(self, value: &str) -> Result<Self::Value, E>
            where
                E: de::Error,
            {
                Ok(ScalarParameter(value.to_string()))
            }

            fn visit_string<E>(self, value: String) -> Result<Self::Value, E>
            where
                E: de::Error,
            {
                Ok(ScalarParameter(value))
            }

            fn visit_bool<E>(self, value: bool) -> Result<Self::Value, E>
            where
                E: de::Error,
            {
                Ok(ScalarParameter(value.to_string()))
            }

            fn visit_i64<E>(self, value: i64) -> Result<Self::Value, E>
            where
                E: de::Error,
            {
                Ok(ScalarParameter(value.to_string()))
            }

            fn visit_u64<E>(self, value: u64) -> Result<Self::Value, E>
            where
                E: de::Error,
            {
                Ok(ScalarParameter(value.to_string()))
            }

            fn visit_f64<E>(self, value: f64) -> Result<Self::Value, E>
            where
                E: de::Error,
            {
                if value.is_finite() {
                    Ok(ScalarParameter(value.to_string()))
                } else {
                    Err(E::custom("non-finite number is not supported"))
                }
            }
        }

        deserializer.deserialize_any(ScalarVisitor)
    }
}

impl<'de> Deserialize<'de> for FlatParameters {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        struct ParameterVisitor;

        impl<'de> Visitor<'de> for ParameterVisitor {
            type Value = FlatParameters;

            fn expecting(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str("a flat parameter object")
            }

            fn visit_map<A>(self, mut map: A) -> Result<Self::Value, A::Error>
            where
                A: MapAccess<'de>,
            {
                let mut parameters = HashMap::new();
                let mut total_bytes = 0_usize;
                while let Some(key) = map.next_key::<String>()? {
                    if parameters.len() >= MAX_PARAMETER_COUNT {
                        return Err(de::Error::custom("too many parameters"));
                    }
                    if !valid_parameter_key(&key) {
                        return Err(de::Error::custom("invalid parameter key"));
                    }
                    if parameters.contains_key(&key) {
                        return Err(de::Error::custom("duplicate parameter key"));
                    }
                    let ScalarParameter(value) = map.next_value::<ScalarParameter>()?;
                    if value.len() > MAX_PARAMETER_VALUE_BYTES {
                        return Err(de::Error::custom("parameter value is too large"));
                    }
                    total_bytes = total_bytes
                        .saturating_add(key.len())
                        .saturating_add(value.len());
                    if total_bytes > MAX_PARAMETER_TOTAL_BYTES {
                        return Err(de::Error::custom("parameter object is too large"));
                    }
                    parameters.insert(key, value);
                }
                Ok(FlatParameters(parameters))
            }
        }

        deserializer.deserialize_map(ParameterVisitor)
    }
}

impl<'de> Deserialize<'de> for FixedMailEnvelope {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        struct EnvelopeVisitor;

        impl<'de> Visitor<'de> for EnvelopeVisitor {
            type Value = FixedMailEnvelope;

            fn expecting(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter
                    .write_str("an object containing to, parameter, and optional not_after_unix_ms")
            }

            fn visit_map<A>(self, mut map: A) -> Result<Self::Value, A::Error>
            where
                A: MapAccess<'de>,
            {
                let mut to = None;
                let mut parameter = None;
                let mut not_after_unix_ms = None;
                while let Some(field) = map.next_key::<String>()? {
                    match field.as_str() {
                        "to" if to.is_none() => to = Some(map.next_value::<String>()?),
                        "parameter" if parameter.is_none() => {
                            parameter = Some(map.next_value::<FlatParameters>()?.0)
                        }
                        "not_after_unix_ms" if not_after_unix_ms.is_none() => {
                            not_after_unix_ms = Some(map.next_value::<u64>()?)
                        }
                        "to" | "parameter" | "not_after_unix_ms" => {
                            return Err(de::Error::custom("duplicate top-level field"));
                        }
                        _ => {
                            return Err(de::Error::unknown_field(
                                &field,
                                &["to", "parameter", "not_after_unix_ms"],
                            ))
                        }
                    }
                }
                Ok(FixedMailEnvelope {
                    event_id: None,
                    to: to.ok_or_else(|| de::Error::missing_field("to"))?,
                    parameter: parameter.ok_or_else(|| de::Error::missing_field("parameter"))?,
                    not_after_unix_ms,
                })
            }
        }

        deserializer.deserialize_map(EnvelopeVisitor)
    }
}

/// [COMMENT]: Processor chung chỉ giới hạn CPU/JMAP inflight; source suite tự sở hữu receive/retry/ACK state.
pub struct MailMessageProcessor {
    zone_id: String,
    max_message_bytes: usize,
    configuration: Arc<MailConfigurationRuntime>,
    zone_kv: Arc<ZoneKvStore>,
    batcher: Arc<MailBatcherHandle>,
    sender: Arc<SenderProfile>,
    accepted_usage: Arc<AcceptedUsagePublisher>,
    concurrency: Arc<Semaphore>,
}

impl MailMessageProcessor {
    pub fn new(
        config: &Config,
        configuration: Arc<MailConfigurationRuntime>,
        zone_kv: Arc<ZoneKvStore>,
        batcher: Arc<MailBatcherHandle>,
        sender: Arc<SenderProfile>,
        accepted_usage: Arc<AcceptedUsagePublisher>,
    ) -> Arc<Self> {
        Arc::new(Self {
            zone_id: config.zone_id.clone(),
            max_message_bytes: config.mail_max_message_bytes,
            configuration,
            zone_kv,
            batcher,
            sender,
            accepted_usage,
            concurrency: Arc::new(Semaphore::new(config.mail_stream_processor_concurrency)),
        })
    }

    pub async fn process(
        &self,
        configuration: Arc<RuntimeConsumerConfiguration>,
        generation_fence: Arc<RuntimeGenerationFence>,
        payload: Bytes,
        trusted_evidence_id: String,
    ) -> MailProcessingStatus {
        let stream_type = configuration
            .stream
            .stream_type
            .as_str_name()
            .to_ascii_lowercase();
        let process_context = crate::observability::otel::OtelTracer::start_span_with_parent(
            format!("process customer-mail {stream_type}"),
            opentelemetry::trace::SpanKind::Consumer,
            vec![
                KeyValue::new("messaging.system", stream_type),
                KeyValue::new("messaging.operation.type", "process"),
                KeyValue::new("aurora.zone.id", self.zone_id.clone()),
                KeyValue::new("aurora.runtime.accepting", generation_fence.is_accepting()),
            ],
            // [COMMENT]: Customer broker context is untrusted. Start a fresh trace
            // instead of letting customer sampled flags control platform telemetry cost.
            &opentelemetry::Context::new(),
        );
        use opentelemetry::trace::{FutureExt, TraceContextExt};
        let status = self
            .process_inner(
                configuration,
                generation_fence,
                payload,
                trusted_evidence_id,
            )
            .with_context(process_context.clone())
            .await;
        let (outcome, code) = processing_status_fields(&status);
        process_context
            .span()
            .set_attribute(KeyValue::new("aurora.mail.outcome", outcome));
        crate::observability::otel::OtelTracer::finish_span(
            &process_context,
            (outcome != "accepted").then_some(code),
        );
        status
    }

    async fn process_inner(
        &self,
        configuration: Arc<RuntimeConsumerConfiguration>,
        generation_fence: Arc<RuntimeGenerationFence>,
        payload: Bytes,
        trusted_evidence_id: String,
    ) -> MailProcessingStatus {
        // [COMMENT]: Global permit chặn tổng inflight của mọi suite nhưng không can thiệp cách broker ACK/retry.
        let _permit = match self.concurrency.acquire().await {
            Ok(permit) => permit,
            Err(_) => {
                return MailProcessingStatus::Retryable {
                    code: "MAIL_PROCESSOR_UNAVAILABLE",
                }
            }
        };
        // [COMMENT]: Mọi early outcome cũng phải vào metric; nếu chỉ record sau JMAP thì decode/expiry/config failures bị mù.
        let retryable = |code| {
            let status = MailProcessingStatus::Retryable { code };
            self.record_outcome(&status);
            status
        };
        let rejected = |code| {
            let status = MailProcessingStatus::PermanentRejected { code };
            self.record_outcome(&status);
            status
        };

        // [COMMENT]: Current pointer check loại queued work cũ; suite vẫn giữ nguyên broker coordinate để quyết định redelivery.
        let current = self
            .configuration
            .active_consumer(&configuration.consumer_id);
        if generation_fence.is_accepting()
            && current.as_ref().is_some_and(|current| {
                current.config_version > configuration.config_version
                    || (current.config_version == configuration.config_version
                        && current.desired_state == RuntimeDesiredState::Paused)
            })
        {
            generation_fence.request_drain();
        }
        if !generation_fence.is_draining()
            && current.as_ref().is_none_or(|current| {
                current.config_version != configuration.config_version
                    || current.config_sha256 != configuration.config_sha256
                    || current.desired_state != RuntimeDesiredState::Enabled
            })
        {
            return retryable("MAIL_RUNTIME_GENERATION_STALE");
        }
        if configuration.sender_profile_id != self.sender.id
            || configuration.sender_version != u64::from(self.sender.version)
        {
            return retryable("MAIL_SENDER_CONFIGURATION_UNAVAILABLE");
        }
        if payload.is_empty() || payload.len() > self.max_message_bytes {
            return rejected("MAIL_MESSAGE_ENVELOPE_SIZE_INVALID");
        }

        let envelope = match decode_fixed_envelope(&payload) {
            Ok(envelope) => envelope,
            Err(code) => return rejected(code),
        };
        // [COMMENT]: Expired mail is terminally ACKed by source suites; retrying can never make its token valid again.
        if envelope.not_after_unix_ms.is_some_and(|deadline| {
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .is_ok_and(|elapsed| elapsed.as_millis() >= u128::from(deadline))
        }) {
            return rejected("MAIL_MESSAGE_EXPIRED");
        }
        if envelope.to.len() > MAX_RECIPIENT_BYTES || envelope.to.contains(['\r', '\n']) {
            return rejected("MAIL_RECIPIENT_INVALID");
        }
        let recipient = match envelope
            .to
            .trim()
            .parse::<lettre::message::Mailbox>()
            .map(|mailbox| mailbox.email.to_string())
        {
            Ok(recipient) => recipient,
            Err(_) => return rejected("MAIL_RECIPIENT_INVALID"),
        };
        // [COMMENT]: Internal Protobuf event_id thắng broker coordinate để JMAP submission retry giữ cùng idempotency key.
        let jmap_submission_id = envelope
            .event_id
            .map(uuid::Uuid::from_bytes)
            .map(|event_id| event_id.to_string())
            .unwrap_or_else(|| trusted_evidence_id.clone());

        let template = match self
            .configuration
            .load_template_for_consumer(&self.zone_kv, &configuration)
            .await
        {
            Ok(template) => template,
            Err(_) => return retryable("MAIL_TEMPLATE_CONFIGURATION_UNAVAILABLE"),
        };
        let (subject, html_body) =
            match render_template(&template, &envelope.parameter, self.max_message_bytes) {
                Ok(rendered) => rendered,
                Err(code)
                    if code.starts_with("MAIL_PARAMETER_")
                        || matches!(
                            code,
                            "MAIL_MESSAGE_TOO_LARGE" | "MAIL_TEMPLATE_SUBJECT_INVALID"
                        ) =>
                {
                    return rejected(code)
                }
                Err(code) => return retryable(code),
            };
        let estimated_bytes = recipient
            .len()
            .saturating_add(subject.len())
            .saturating_add(html_body.len())
            .saturating_add(1024);
        if estimated_bytes > self.max_message_bytes {
            return rejected("MAIL_MESSAGE_TOO_LARGE");
        }

        // [COMMENT]: Permit được giữ xuyên JMAP await; COW/mất lease phải đợi request này có typed result rồi mới fence xong.
        let Some(submit_permit) = generation_fence.enter_submit().await else {
            return retryable("MAIL_RUNTIME_GENERATION_STALE");
        };
        let current = self
            .configuration
            .active_consumer(&configuration.consumer_id);
        if generation_fence.is_accepting()
            && current.as_ref().is_some_and(|current| {
                current.config_version > configuration.config_version
                    || (current.config_version == configuration.config_version
                        && current.desired_state == RuntimeDesiredState::Paused)
            })
        {
            generation_fence.request_drain();
        }
        if !generation_fence.is_draining()
            && current.as_ref().is_none_or(|current| {
                current.config_version != configuration.config_version
                    || current.config_sha256 != configuration.config_sha256
                    || current.desired_state != RuntimeDesiredState::Enabled
            })
        {
            drop(submit_permit);
            return retryable("MAIL_RUNTIME_GENERATION_STALE");
        }
        let result = self
            .batcher
            .submit(PreparedMail {
                job_id: jmap_submission_id,
                recipient,
                subject,
                text_body: None,
                html_body: Some(html_body),
                estimated_bytes,
            })
            .await;
        drop(submit_permit);

        let status = match result {
            Ok(_) => {
                let evidence_id = match uuid::Uuid::parse_str(&trusted_evidence_id) {
                    Ok(value) if !value.is_nil() => value,
                    _ => return retryable("MAIL_ACCEPTED_EVIDENCE_ID_INVALID"),
                };
                let zone_id = match uuid::Uuid::parse_str(&self.zone_id) {
                    Ok(value) if !value.is_nil() => value,
                    _ => return retryable("MAIL_ACCEPTED_EVIDENCE_ZONE_INVALID"),
                };
                let resource_id = match uuid::Uuid::parse_str(&configuration.consumer_id) {
                    Ok(value) if !value.is_nil() => value,
                    _ => return retryable("MAIL_ACCEPTED_EVIDENCE_RESOURCE_INVALID"),
                };
                let accepted_at_unix_ms = chrono::Utc::now().timestamp_millis();
                let mut retry_delay = std::time::Duration::from_millis(100);
                loop {
                    if !generation_fence.is_accepting() {
                        return retryable("MAIL_ACCEPTED_EVIDENCE_PENDING");
                    }
                    match self
                        .accepted_usage
                        .publish(evidence_id, zone_id, resource_id, accepted_at_unix_ms)
                        .await
                    {
                        Ok(()) => break MailProcessingStatus::Accepted,
                        Err(_) => {
                            tokio::time::sleep(retry_delay).await;
                            retry_delay = retry_delay
                                .saturating_mul(2)
                                .min(std::time::Duration::from_secs(5));
                        }
                    }
                }
            }
            Err(error)
                if matches!(
                    error.code.as_str(),
                    "MAIL_JMAP_TRANSPORT"
                        | "MAIL_JMAP_INVALID_RESPONSE"
                        | "MAIL_JMAP_RESULT_MISSING"
                        | "MAIL_BATCHER_RESULT_DROPPED"
                ) =>
            {
                MailProcessingStatus::Ambiguous {
                    code: "MAIL_JMAP_SUBMISSION_AMBIGUOUS",
                }
            }
            Err(error) if error.retryable => MailProcessingStatus::Retryable {
                code: "MAIL_JMAP_SUBMISSION_RETRYABLE",
            },
            Err(_) => MailProcessingStatus::PermanentRejected {
                code: "MAIL_JMAP_SUBMISSION_REJECTED",
            },
        };
        self.record_outcome(&status);
        status
    }

    fn record_outcome(&self, outcome: &MailProcessingStatus) {
        let (status, code) = processing_status_fields(outcome);
        // [COMMENT]: Chỉ taxonomy low-cardinality; không dùng consumer/topic/recipient/template làm metric label.
        processing_outcome_metric().add(
            1,
            &[
                KeyValue::new("zone_id", self.zone_id.clone()),
                KeyValue::new("status", status),
                KeyValue::new("code", code),
            ],
        );
    }
}

fn processing_status_fields(outcome: &MailProcessingStatus) -> (&'static str, &'static str) {
    match outcome {
        MailProcessingStatus::Accepted => ("accepted", "MAIL_ACCEPTED"),
        MailProcessingStatus::PermanentRejected { code } => ("rejected", *code),
        MailProcessingStatus::Retryable { code } => ("retryable", *code),
        MailProcessingStatus::Ambiguous { code } => ("ambiguous", *code),
    }
}

fn decode_fixed_envelope(payload: &[u8]) -> Result<FixedMailEnvelope, &'static str> {
    if payload.first() == Some(&b'{') {
        let mut deserializer = serde_json::Deserializer::from_slice(payload);
        let envelope = FixedMailEnvelope::deserialize(&mut deserializer)
            .map_err(|_| "MAIL_MESSAGE_ENVELOPE_INVALID")?;
        deserializer
            .end()
            .map_err(|_| "MAIL_MESSAGE_ENVELOPE_INVALID")?;
        return Ok(envelope);
    }

    let envelope =
        MailDispatchEnvelopeV1::decode(payload).map_err(|_| "MAIL_MESSAGE_ENVELOPE_INVALID")?;
    if envelope.schema_version != 1
        || envelope.event_id.len() != 16
        || envelope.not_after_unix_ms <= 0
        || envelope.parameter.len() > MAX_PARAMETER_COUNT
    {
        return Err("MAIL_MESSAGE_ENVELOPE_INVALID");
    }
    let mut total_bytes = 0_usize;
    for (key, value) in &envelope.parameter {
        if !valid_parameter_key(key) || value.len() > MAX_PARAMETER_VALUE_BYTES {
            return Err("MAIL_MESSAGE_ENVELOPE_INVALID");
        }
        total_bytes = total_bytes
            .saturating_add(key.len())
            .saturating_add(value.len());
        if total_bytes > MAX_PARAMETER_TOTAL_BYTES {
            return Err("MAIL_MESSAGE_ENVELOPE_INVALID");
        }
    }
    let event_id: [u8; 16] = envelope
        .event_id
        .try_into()
        .map_err(|_| "MAIL_MESSAGE_ENVELOPE_INVALID")?;
    Ok(FixedMailEnvelope {
        event_id: Some(event_id),
        to: envelope.to,
        parameter: envelope.parameter,
        not_after_unix_ms: Some(envelope.not_after_unix_ms as u64),
    })
}

fn valid_parameter_key(key: &str) -> bool {
    if key.is_empty() || key.len() > MAX_PARAMETER_KEY_BYTES {
        return false;
    }
    let mut bytes = key.bytes();
    bytes
        .next()
        .is_some_and(|byte| byte.is_ascii_alphabetic() || byte == b'_')
        && bytes.all(|byte| byte.is_ascii_alphanumeric() || byte == b'_')
}

fn render_template(
    template: &RuntimeTemplateSnapshot,
    parameters: &HashMap<String, String>,
    max_message_bytes: usize,
) -> Result<(String, String), &'static str> {
    let mut used = HashSet::new();
    let subject = render_piece(
        &template.subject_template,
        &template.subject_tokens,
        parameters,
        false,
        998,
        &mut used,
    )?;
    if subject.trim().is_empty() || subject.chars().any(char::is_control) {
        return Err("MAIL_TEMPLATE_SUBJECT_INVALID");
    }
    let html = render_piece(
        &template.html_template,
        &template.html_tokens,
        parameters,
        true,
        max_message_bytes,
        &mut used,
    )?;
    if html.trim().is_empty() {
        return Err("MAIL_TEMPLATE_BODY_INVALID");
    }
    if parameters.keys().any(|key| !used.contains(key)) {
        return Err("MAIL_PARAMETER_UNKNOWN");
    }
    Ok((subject, html))
}

fn render_piece(
    template: &str,
    tokens: &[RuntimeTemplateToken],
    parameters: &HashMap<String, String>,
    escape_html: bool,
    max_output_bytes: usize,
    used: &mut HashSet<String>,
) -> Result<String, &'static str> {
    let mut rendered = String::with_capacity(template.len());
    let mut cursor = 0_usize;
    for token in tokens {
        rendered.push_str(&template[cursor..token.start]);
        let value = parameters
            .get(&token.key)
            .ok_or("MAIL_PARAMETER_REQUIRED")?;
        used.insert(token.key.clone());
        if escape_html {
            for character in value.chars() {
                match character {
                    '&' => rendered.push_str("&amp;"),
                    '<' => rendered.push_str("&lt;"),
                    '>' => rendered.push_str("&gt;"),
                    '"' => rendered.push_str("&quot;"),
                    '\'' => rendered.push_str("&#39;"),
                    _ => rendered.push(character),
                }
            }
        } else {
            rendered.push_str(value);
        }
        if rendered.len() > max_output_bytes {
            return Err("MAIL_MESSAGE_TOO_LARGE");
        }
        cursor = token.end;
    }
    rendered.push_str(&template[cursor..]);
    if rendered.len() > max_output_bytes {
        return Err("MAIL_MESSAGE_TOO_LARGE");
    }
    Ok(rendered)
}

#[cfg(test)]
#[path = "../test/stream_processor.rs"]
mod tests;
