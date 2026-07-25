use crate::observability::logger::{LogFields, Logger};
use prost::Message;

const JOB_NOTIFICATION_STREAM: &str = "stream:{job_notifications}";
const JOB_NOTIFICATION_STREAM_CAPACITY: usize = 100_000;
const NOTIFICATION_NAMESPACE: uuid::Uuid = uuid::Uuid::from_bytes([
    0x43, 0xa7, 0xde, 0x2c, 0x59, 0x85, 0x50, 0x67, 0xa0, 0x16, 0x0b, 0x63, 0x8d, 0xd8, 0xe9, 0x71,
]);

pub mod notification_proto {
    include!(concat!(env!("OUT_DIR"), "/job.rs"));
}

pub struct NotificationIntent<'a> {
    pub job_id: uuid::Uuid,
    pub user_id: &'a str,
    pub job_version: u32,
    pub attempt: u32,
    pub status: &'a str,
    pub job_topic: &'a str,
    pub resource_id: &'a str,
    pub message: &'a str,
    pub traceparent: &'a str,
    pub tracestate: &'a str,
}

pub struct JobNotifier;

impl JobNotifier {
    pub async fn notify_realtime(
        intent: NotificationIntent<'_>,
        redis_connection: &mut redis::aio::ConnectionManager,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let notification_status = match intent.status {
            "SUCCEEDED" => "SUCCESS",
            "PROCESSING" => "PROCESSING",
            _ => "FAILED",
        };
        let notification_id = notification_id(
            intent.job_id,
            intent.job_version,
            intent.attempt,
            notification_status,
        );
        let event = notification_proto::JobNotificationEvent {
            job_id: intent.job_id.to_string(),
            user_id: intent.user_id.to_string(),
            status: notification_status.to_string(),
            event_type: intent.job_topic.to_string(),
            title: notification_title(intent.job_topic, intent.status).to_string(),
            message: bounded_utf8(intent.message, 4_096),
            created_at: chrono::Utc::now().timestamp(),
            trace_parent: intent.traceparent.to_string(),
            trace_state: intent.tracestate.to_string(),
            resource_id: intent.resource_id.to_string(),
            job_version: intent.job_version,
            attempt: intent.attempt,
            notification_id: notification_id.to_string(),
        };
        let mut payload = Vec::with_capacity(event.encoded_len());
        event.encode(&mut payload)?;

        // Never trim a Stream containing pending entries. Capacity rejection is
        // best-effort notification backpressure; it must not roll back business state.
        let mut stream_id = None;
        for attempt in 0..3_u64 {
            let result: Result<String, redis::RedisError> = redis::Script::new(
                r#"
                local length = redis.call('XLEN', KEYS[1])
                if length >= tonumber(ARGV[1]) then
                    return redis.error_reply('JOB_NOTIFICATION_STREAM_CAPACITY_REACHED')
                end
                return redis.call('XADD', KEYS[1], '*', 'payload', ARGV[2])
                "#,
            )
            .key(JOB_NOTIFICATION_STREAM)
            .arg(JOB_NOTIFICATION_STREAM_CAPACITY)
            .arg(&payload)
            .invoke_async(redis_connection)
            .await;
            match result {
                Ok(value) => {
                    stream_id = Some(value);
                    break;
                }
                Err(error)
                    if attempt == 2
                        || error
                            .to_string()
                            .contains("JOB_NOTIFICATION_STREAM_CAPACITY_REACHED") =>
                {
                    return Err(error.into());
                }
                Err(_) => {
                    // Best-effort retry budget is deliberately sub-second so
                    // realtime degradation cannot stall the durable result lane.
                    tokio::time::sleep(std::time::Duration::from_millis(50 * (attempt + 1))).await;
                }
            }
        }
        let stream_id = stream_id.ok_or("notification enqueue retry budget exhausted")?;

        crate::observability::metrics::MetricsManager::inc_notifications_enqueued();
        let notification_id_text = notification_id.to_string();
        let job_id_text = intent.job_id.to_string();
        Logger::job_log_with_fields(
            &job_id_text,
            intent.job_topic,
            intent.attempt,
            "results.notify",
            "JOB_NOTIFICATION_ENQUEUED",
            &format!("Queued job notification at Redis entry {stream_id}"),
            LogFields {
                event_id: Some(&notification_id_text),
                operation_id: Some(&job_id_text),
                job_version: Some(u64::from(intent.job_version)),
                outcome: Some("enqueued"),
                ..LogFields::default()
            },
        );
        Ok(())
    }
}

fn notification_id(job_id: uuid::Uuid, job_version: u32, attempt: u32, status: &str) -> uuid::Uuid {
    let identity = format!("{job_id}:{job_version}:{attempt}:{status}");
    uuid::Uuid::new_v5(&NOTIFICATION_NAMESPACE, identity.as_bytes())
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
        ("storage.bucket.create", "SUCCEEDED") => "Bucket Created",
        ("storage.bucket.create", "FAILED") => "Bucket Creation Failed",
        ("storage.bucket.create", _) => "Creating Bucket",
        ("storage.bucket.delete", "SUCCEEDED") => "Bucket Deleted",
        ("storage.bucket.delete", "FAILED") => "Bucket Deletion Failed",
        ("storage.bucket.delete", _) => "Deleting Bucket",
        ("storage.bucket.resize", "SUCCEEDED") => "Bucket Resized",
        ("storage.bucket.resize", "FAILED") => "Bucket Resize Failed",
        ("storage.bucket.resize", _) => "Resizing Bucket",
        ("storage.credential.create", "SUCCEEDED") => "Storage Credential Created",
        ("storage.credential.create", "FAILED") => "Storage Credential Creation Failed",
        ("storage.credential.create", _) => "Creating Storage Credential",
        ("storage.credential.delete", "SUCCEEDED") => "Storage Credential Deleted",
        ("storage.credential.delete", "FAILED") => "Storage Credential Deletion Failed",
        ("storage.credential.delete", _) => "Deleting Storage Credential",
        ("storage.object.sts", "SUCCEEDED") => "Temporary Storage Access Ready",
        ("storage.object.sts", "FAILED") => "Temporary Storage Access Failed",
        ("storage.object.sts", _) => "Preparing Temporary Storage Access",
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
    fn notification_identity_is_stable_for_replay() {
        let job = uuid::Uuid::new_v4();
        assert_eq!(
            notification_id(job, 1, 2, "SUCCEEDED"),
            notification_id(job, 1, 2, "SUCCEEDED")
        );
    }

    #[test]
    fn bounded_message_preserves_utf8() {
        let bounded = bounded_utf8(&"á".repeat(4_096), 4_096);
        assert!(bounded.len() <= 4_096);
        assert!(std::str::from_utf8(bounded.as_bytes()).is_ok());
    }

    #[test]
    fn failed_storage_job_never_uses_a_success_title() {
        assert_eq!(
            notification_title("storage.bucket.create", "FAILED"),
            "Bucket Creation Failed"
        );
        assert_eq!(
            notification_title("storage.credential.delete", "FAILED"),
            "Storage Credential Deletion Failed"
        );
    }
}
