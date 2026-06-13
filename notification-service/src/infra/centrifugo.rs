use reqwest::Client;
use serde::Serialize;
use crate::observability::logger::Logger;

// Định nghĩa cấu trúc máy khách kết nối đến Centrifugo API Gateway
#[derive(Clone)]
pub struct CentrifugoClient {
    client: Client,     // HTTP Client có sẵn connection pooling và timeout
    api_url: String,    // Địa chỉ base API URL của Centrifugo
    api_key: String,    // Mã khóa API Key dùng để ký/xác thực request
}

// Cấu trúc payload gửi tin đến Centrifugo theo đặc tả HTTP API
#[derive(Serialize)]
struct PublishRequest {
    channel: String,          // Kênh đích nhận thông tin (ví dụ: personal:user_id)
    data: serde_json::Value,  // Nội dung JSON thực tế hiển thị cho Client
}

impl CentrifugoClient {
    // Khởi tạo mới một CentrifugoClient
    pub fn new(api_url: String, api_key: String) -> Self {
        // [ignoring loop detection]
        // Xây dựng HTTP Client với timeout 5 giây để tránh treo request nếu network chập chờn
        let client = Client::builder()
            .timeout(std::time::Duration::from_secs(5))
            .build()
            .unwrap_or_else(|_| Client::new());
        
        Self {
            client,
            api_url,
            api_key,
        }
    }

    // Đẩy thông điệp JSON tới Centrifugo API để truyền xuống Websocket client
    pub async fn publish(&self, channel: &str, data: serde_json::Value) -> Result<(), reqwest::Error> {
        // [ignoring loop detection]
        Logger::sys_info("centrifugo.publish", &format!("Publishing event to channel: {}", channel));
        
        // Chuẩn hóa endpoint URL, tự động thêm hậu tố /publish nếu chưa có
        let url = if self.api_url.ends_with("/publish") {
            self.api_url.clone()
        } else {
            format!("{}/publish", self.api_url)
        };

        // Đóng gói thông điệp gửi đi
        let payload = PublishRequest {
            channel: channel.to_string(),
            data,
        };

        // Thực hiện cuộc gọi HTTP POST gửi tin nhắn sang Centrifugo
        let mut request = self.client.post(&url)
            .header("X-API-Key", &self.api_key) // Gửi API Key qua Header chuẩn của Centrifugo
            .header("Authorization", format!("apikey {}", self.api_key)) // Dự phòng Authorization header cho các phiên bản cũ hơn
            .json(&payload);

        // Đính kèm Trace Context (traceparent) nếu được truyền trong luồng thực thi async hiện tại
        if let Some(traceparent) = crate::observability::otel::OtelTracer::get_traceparent() {
            request = request.header("traceparent", traceparent);
        }

        let response = request.send().await?;

        // Ghi nhận cảnh báo nếu Centrifugo phản hồi mã lỗi không thành công (ví dụ: 401 Unauthorized, 404)
        if !response.status().is_success() {
            let status = response.status();
            Logger::sys_warn(
                "centrifugo.publish_failed",
                &format!("Failed to publish message to Centrifugo, status: {}", status),
                "http_error"
            );
        }

        Ok(())
    }
}
