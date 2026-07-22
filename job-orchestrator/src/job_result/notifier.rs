use crate::observability::logger::Logger;

// [COMMENT]: Bộ phát sự kiện thông báo real-time chuyên biệt phục vụ kiến trúc hướng sự kiện (EDA)
pub struct JobNotifier;

impl JobNotifier {
    // [COMMENT]: Phát sự kiện thông báo real-time tới NATS Core thay thế cho Redis Stream
    pub async fn notify_realtime(
        job_id: &str,
        user_id: &str,
        attempt: u32,
        status: &str,
        job_topic: &str,
        resource_id: &str,
        message: &str,
        trace_id: &str,
        nats_client: &async_nats::Client,
    ) -> Result<(), Box<dyn std::error::Error>> {
        Logger::job_log(
            job_id,
            user_id,
            attempt,
            "job_result.notify_start",
            &format!("Bắt đầu tạo sự kiện realtime NATS cho user {}", user_id),
        );

        let notification_status = if status == "SUCCEEDED" {
            "SUCCESS".to_string()
        } else if status == "PROCESSING" {
            "PROCESSING".to_string()
        } else {
            "FAILED".to_string()
        };

        // [COMMENT]: Đóng gói thông báo trực tiếp dạng JSON gửi qua NATS
        let nats_payload = serde_json::json!({
            "status": notification_status,
            // [COMMENT]: Title phản ánh đúng terminal outcome; FAILED không được mang nhãn đã apply/delete thành công.
            "title": match (job_topic, status) {
                ("mail.consumer.upsert", "SUCCEEDED") => "Mail Consumer Applied".to_string(),
                ("mail.consumer.upsert", "FAILED") => "Mail Consumer Apply Failed".to_string(),
                ("mail.consumer.upsert", _) => "Applying Mail Consumer".to_string(),
                ("mail.consumer.delete", "SUCCEEDED") => "Mail Consumer Deleted".to_string(),
                ("mail.consumer.delete", "FAILED") => "Mail Consumer Delete Failed".to_string(),
                ("mail.consumer.delete", _) => "Deleting Mail Consumer".to_string(),
                ("mail.template.version_published", "SUCCEEDED") => "Mail Template Published".to_string(),
                ("mail.template.version_published", "FAILED") => "Mail Template Publish Failed".to_string(),
                ("mail.template.version_published", _) => "Publishing Mail Template".to_string(),
                ("mail.template.deleted", "SUCCEEDED") => "Mail Template Deleted".to_string(),
                ("mail.template.deleted", "FAILED") => "Mail Template Delete Failed".to_string(),
                ("mail.template.deleted", _) => "Deleting Mail Template".to_string(),
                (job_topic, _) => match job_topic {
                "mail.test_connection" => "SMTP Connection Test".to_string(),
                "storage.bucket.create" => "Bucket Created".to_string(),
                "storage.bucket.delete" => "Bucket Deleted".to_string(),
                "storage.credential.create" => "Storage Credential Created".to_string(),
                "storage.credential.delete" => "Storage Credential Deleted".to_string(),
                _ => "Job Notification".to_string(),
                },
            },
            // [COMMENT]: operation = job_topic dùng làm signal cho client quyết định realtime behavior
            "operation": job_topic,
            "resource_id": resource_id,
            "message": message,
            "created_at": chrono::Utc::now().to_rfc3339(),
            "job_id": job_id,
            "event_type": job_topic,
            "trace_parent": trace_id,
        });

        // [COMMENT]: Serialize JSON payload
        let payload_bin = serde_json::to_vec(&nats_payload)?;

        // [COMMENT]: Bắn sự kiện lên NATS Core theo chủ đề định hướng người dùng cụ thể
        let subject = format!("jobs.notifications.{}", user_id);
        nats_client
            .publish(subject.clone(), payload_bin.into())
            .await?;

        // Tăng chỉ số metrics số thông báo realtime đã gửi thành công
        crate::observability::metrics::MetricsManager::inc_notifications_sent();

        Logger::job_log(
            job_id,
            user_id,
            attempt,
            "job_result.notify_sent",
            &format!(
                "Đã đẩy thành công sự kiện thông báo vào NATS Core chủ đề: {}",
                subject
            ),
        );

        Ok(())
    }
}
