use crate::infra::centrifugo::CentrifugoClient;
use crate::observability::logger::Logger;

// [COMMENT]: Xử lý sự kiện đồng bộ dung lượng bucket và đẩy tin qua Centrifugo WebSocket
pub async fn handle_bucket_size_sync(
    centrifugo_client: &CentrifugoClient,
    user_id: &str,
    payload: serde_json::Value,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel_name = format!("personal:{}", user_id);

    let mut client_event = payload;
    if let Some(obj) = client_event.as_object_mut() {
        obj.insert(
            "event_type".to_string(),
            serde_json::Value::String("storage.bucket.sizes.sync".to_string()),
        );
    }

    match centrifugo_client.publish(&channel_name, client_event).await {
        Ok(_) => {
            crate::observability::metrics::MetricsManager::record_centrifugo_publish("success");
            Logger::sys_info(
                "storage_service.sync_success",
                &format!(
                    "Đã chuyển tiếp thông tin dung lượng tới Centrifugo channel: {}",
                    channel_name
                ),
            );
            Ok(())
        }
        Err(e) => {
            crate::observability::metrics::MetricsManager::record_centrifugo_publish("failed");
            Logger::sys_error(
                "storage_service.sync_fail",
                &format!("Lỗi chuyển tiếp tới Centrifugo channel: {}", channel_name),
                &e.to_string(),
            );
            Err(Box::new(e))
        }
    }
}
