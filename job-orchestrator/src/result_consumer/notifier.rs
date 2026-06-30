use crate::observability::logger::Logger;
use prost::Message;

pub mod job_proto {
    // Nạp struct sinh tự động từ protobuf (job_event.proto)
    include!(concat!(env!("OUT_DIR"), "/job.rs"));
}

/// Bộ phát sự kiện thông báo real-time chuyên biệt phục vụ kiến trúc hướng sự kiện (EDA)
pub struct JobNotifier;

impl JobNotifier {
    /// Phát sự kiện thông báo real-time tới Redis stream:job_notifications
    pub async fn notify_realtime(
        job_id: &str,
        user_id: &str,
        attempt: u32,
        status: &str,
        job_topic: &str,
        message: &str,
        trace_id: &str,
        redis_conn: &mut redis::aio::MultiplexedConnection,
    ) -> Result<(), Box<dyn std::error::Error>> {
        Logger::job_log(
            job_id,
            user_id,
            attempt,
            "result_consumer.notify_start",
            &format!("Bắt đầu tạo sự kiện realtime cho user {}", user_id),
        );

        let notification_status = if status == "SUCCEEDED" {
            "SUCCESS".to_string()
        } else if status == "PROCESSING" {
            "PROCESSING".to_string()
        } else {
            "FAILED".to_string()
        };

        // Đóng gói sự kiện JobNotificationEvent theo cấu trúc Protobuf
        let event = job_proto::JobNotificationEvent {
            job_id: job_id.to_string(),
            user_id: user_id.to_string(),
            status: notification_status,
            event_type: job_topic.to_string(),
            title: match job_topic {
                "mail.test_connection" => "SMTP Connection Test".to_string(),
                _ => "Job Execution Result".to_string(),
            },
            message: message.to_string(),
            created_at: chrono::Utc::now().timestamp(),
            trace_parent: trace_id.to_string(),
        };

        // Mã hóa sự kiện sang dạng nhị phân Protobuf bytes
        let mut binary_buf = Vec::new();
        event.encode(&mut binary_buf)?;

        // Đẩy dữ liệu nhị phân vào Redis Stream bằng câu lệnh XADD
        let _: String = redis::cmd("XADD")
            .arg("stream:job_notifications")
            .arg("*")
            .arg("data")
            .arg(&binary_buf)
            .query_async(redis_conn)
            .await?;

        // Tăng chỉ số metrics số thông báo realtime đã gửi thành công
        crate::observability::metrics::MetricsManager::inc_notifications_sent();

        Logger::job_log(
            job_id,
            user_id,
            attempt,
            "result_consumer.notify_sent",
            "Đã đẩy thành công sự kiện thông báo vào stream:job_notifications",
        );

        Ok(())
    }
}
