use crate::infra::centrifugo::CentrifugoClient;
use crate::observability::logger::Logger;

// [COMMENT]: Xử lý sự kiện thông báo kết quả công việc và đẩy tin qua Centrifugo WebSocket
pub async fn handle_job_notification(
    centrifugo_client: &CentrifugoClient,
    user_id: &str,
    payload: serde_json::Value,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel_name = format!("personal:{}", user_id);

    // [COMMENT]: Lọc dữ liệu thông báo tránh rò rỉ thông tin hạ tầng nội bộ.
    // operation = job_topic được giữ lại để client dùng như signal quyết định realtime behavior.
    let filtered_data = serde_json::json!({
        "status": payload.get("status").cloned().unwrap_or(serde_json::Value::Null),
        "title": payload.get("title").cloned().unwrap_or(serde_json::Value::Null),
        "message": payload.get("message").cloned().unwrap_or(serde_json::Value::Null),
        "created_at": payload.get("created_at").cloned().unwrap_or(serde_json::Value::Null),
        "transaction_id": payload.get("job_id").cloned().unwrap_or(serde_json::Value::Null),
        // [COMMENT]: operation cho phép client phân biệt loại job để quyết định silent hay hiển thị notification
        "operation": payload.get("operation").cloned().unwrap_or(serde_json::Value::Null),
        "resource_id": payload.get("resource_id").cloned().unwrap_or(serde_json::Value::Null),
    });

    let mut client_event = filtered_data;
    if let Some(obj) = client_event.as_object_mut() {
        obj.insert(
            "event_type".to_string(),
            serde_json::Value::String("job.notification".to_string()),
        );
    }

    match centrifugo_client.publish(&channel_name, client_event).await {
        Ok(_) => {
            crate::observability::metrics::MetricsManager::record_centrifugo_publish("success");

            // Đo lường độ trễ từ khi phát sinh CDC / Job cho đến lúc Centrifugo publish WebSocket thành công
            if let Some(created_at_str) = payload.get("created_at").and_then(|v| v.as_str()) {
                if let Ok(created_at_dt) = chrono::DateTime::parse_from_rfc3339(created_at_str) {
                    let current_time = chrono::Utc::now();
                    let lag = current_time
                        .signed_duration_since(created_at_dt.with_timezone(&chrono::Utc));
                    let lag_duration =
                        std::time::Duration::from_secs_f64(lag.num_milliseconds() as f64 / 1000.0);
                    crate::observability::metrics::MetricsManager::record_delivered_lag(
                        "success",
                        lag_duration,
                    );
                }
            }

            Logger::sys_info(
                "job_service.notification_success",
                &format!(
                    "Đã chuyển tiếp thông báo kết quả công việc tới Centrifugo channel: {}",
                    channel_name
                ),
            );
            Ok(())
        }
        Err(e) => {
            crate::observability::metrics::MetricsManager::record_centrifugo_publish("failed");
            Logger::sys_error(
                "job_service.notification_fail",
                &format!("Lỗi chuyển tiếp tới Centrifugo channel: {}", channel_name),
                &e.to_string(),
            );
            Err(Box::new(e))
        }
    }
}
