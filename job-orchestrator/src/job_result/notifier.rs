use crate::observability::logger::{LogFields, Logger};
use prost::Message;

const JOB_NOTIFICATION_STREAM: &str = "stream:{job_notifications}";
const JOB_NOTIFICATION_STREAM_CAPACITY: usize = 100_000;

pub mod notification_proto {
    include!(concat!(env!("OUT_DIR"), "/job.rs"));
}

pub struct JobNotifier;

impl JobNotifier {
    #[allow(clippy::too_many_arguments)]
    pub async fn notify_realtime(
        job_id: &str,
        user_id: &str,
        job_version: u32,
        attempt: u32,
        status: &str,
        job_topic: &str,
        resource_id: &str,
        message: &str,
        traceparent: &str,
        tracestate: &str,
        redis_connection: &mut redis::aio::ConnectionManager,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let notification_status = match status {
            "SUCCEEDED" => "SUCCESS",
            "PROCESSING" => "PROCESSING",
            _ => "FAILED",
        };
        let event = notification_proto::JobNotificationEvent {
            job_id: job_id.to_string(),
            user_id: user_id.to_string(),
            status: notification_status.to_string(),
            event_type: job_topic.to_string(),
            title: notification_title(job_topic, status).to_string(),
            message: bounded_utf8(message, 4_096),
            created_at: chrono::Utc::now().timestamp(),
            trace_parent: traceparent.to_string(),
            trace_state: tracestate.to_string(),
            resource_id: resource_id.to_string(),
            job_version,
            attempt,
        };
        let mut payload = Vec::with_capacity(event.encoded_len());
        event.encode(&mut payload)?;

        // The stream itself is never trimmed because MAXLEN may delete pending
        // entries. At capacity, fail the Kafka result so replay applies backpressure.
        let stream_result: Result<String, redis::RedisError> = redis::Script::new(
            r#"
            local length = redis.call('XLEN', KEYS[1])
            if length >= tonumber(ARGV[1]) then
                return redis.error_reply('JOB_NOTIFICATION_STREAM_CAPACITY_REACHED')
            end
            return redis.call('XADD', KEYS[1], '*', 'data', ARGV[2])
            "#,
        )
        .key(JOB_NOTIFICATION_STREAM)
        .arg(JOB_NOTIFICATION_STREAM_CAPACITY)
        .arg(payload)
        .invoke_async(redis_connection)
        .await;
        let stream_id = match stream_result {
            Ok(stream_id) => stream_id,
            Err(error) => {
                crate::observability::metrics::MetricsManager::record_notification_failed();
                return Err(error.into());
            }
        };

        crate::observability::metrics::MetricsManager::inc_notifications_enqueued();
        Logger::job_log_with_fields(
            job_id,
            job_topic,
            attempt,
            "job_result.notify_sent",
            "JOB_NOTIFICATION_ENQUEUED",
            &format!(
                "Queued job notification in Shared Redis stream at entry {}",
                stream_id
            ),
            LogFields {
                event_id: Some(job_id),
                job_version: Some(u64::from(job_version)),
                outcome: Some("enqueued"),
                ..LogFields::default()
            },
        );
        Ok(())
    }
}

fn notification_title(job_topic: &str, status: &str) -> &'static str {
    match (job_topic, status) {
        ("mail.consumer.upsert", "SUCCEEDED") => "Mail Consumer Applied",
        ("mail.consumer.upsert", "FAILED") => "Mail Consumer Apply Failed",
        ("mail.consumer.upsert", _) => "Applying Mail Consumer",
        ("mail.consumer.delete", "SUCCEEDED") => "Mail Consumer Deleted",
        ("mail.consumer.delete", "FAILED") => "Mail Consumer Delete Failed",
        ("mail.consumer.delete", _) => "Deleting Mail Consumer",
        ("mail.template.version_published", "SUCCEEDED") => "Mail Template Published",
        ("mail.template.version_published", "FAILED") => "Mail Template Publish Failed",
        ("mail.template.version_published", _) => "Publishing Mail Template",
        ("mail.template.deleted", "SUCCEEDED") => "Mail Template Deleted",
        ("mail.template.deleted", "FAILED") => "Mail Template Delete Failed",
        ("mail.template.deleted", _) => "Deleting Mail Template",
        ("mail.test_connection", _) => "SMTP Connection Test",
        ("storage.bucket.create", _) => "Bucket Created",
        ("storage.bucket.delete", _) => "Bucket Deleted",
        ("storage.credential.create", _) => "Storage Credential Created",
        ("storage.credential.delete", _) => "Storage Credential Deleted",
        _ => "Job Notification",
    }
}

fn bounded_utf8(value: &str, max_bytes: usize) -> String {
    if value.len() <= max_bytes {
        return value.to_string();
    }
    let mut end = max_bytes;
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    value[..end].to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn notification_message_bound_preserves_utf8() {
        let bounded = bounded_utf8(&"á".repeat(4_096), 4_096);
        assert!(bounded.len() <= 4_096);
        assert!(std::str::from_utf8(bounded.as_bytes()).is_ok());
    }

    #[test]
    fn failed_mail_title_never_claims_success() {
        assert_eq!(
            notification_title("mail.consumer.delete", "FAILED"),
            "Mail Consumer Delete Failed"
        );
    }
}
