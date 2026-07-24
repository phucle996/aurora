use crate::config::Config;
use crate::observability::logger::Logger;
use std::time::Duration;

/// Cấu trúc node trả về từ Proxmox API `GET /api2/json/nodes`
#[derive(serde::Deserialize, Debug, Clone)]
pub struct ProxmoxNodeRaw {
    /// Định danh node vật lý trong Proxmox (ví dụ: "pve-node-01")
    pub node: String,
    /// Trạng thái của node theo Proxmox: "online" | "offline" | "unknown"
    pub status: String,
    /// Mức độ sử dụng CPU hiện tại (float 0.0–1.0, tỉ lệ trên maxcpu)
    #[serde(default)]
    pub cpu: f64,
    /// Tổng số lõi CPU vật lý
    #[serde(default)]
    pub maxcpu: u64,
    /// Bộ nhớ RAM đang sử dụng (bytes)
    #[serde(default)]
    pub mem: u64,
    /// Tổng dung lượng RAM vật lý (bytes)
    #[serde(default)]
    pub maxmem: u64,
    /// Dung lượng disk OS node đang dùng (bytes)
    #[serde(default)]
    pub disk: u64,
    /// Tổng dung lượng disk OS node (bytes)
    #[serde(default)]
    pub maxdisk: u64,
}

/// Wrapper lớp ngoài của Proxmox API response envelope
#[derive(serde::Deserialize)]
struct ProxmoxApiResponse {
    data: Vec<ProxmoxNodeRaw>,
}

/// Client kết nối chuyên biệt đến Proxmox REST API
pub struct ProxmoxClient {
    client: reqwest::Client,
    api_url: String,
    api_token: String,
}

impl ProxmoxClient {
    /// Khởi tạo client kết nối với cấu hình TLS thích hợp
    pub fn new(config: &Config) -> Self {
        let mut builder = reqwest::Client::builder().timeout(Duration::from_secs(5)); // Timeout cứng 5 giây để tránh treo vòng lặp

        // Chỉ cho phép skip TLS verify trên dev/test — production bắt buộc tắt
        if config.proxmox_tls_insecure {
            builder = builder.danger_accept_invalid_certs(true);
        }

        let client = builder.build().unwrap_or_else(|e| {
            Logger::sys_error(
                "proxmox_client.init",
                "Không thể khởi tạo HTTP client cho Proxmox. Sử dụng default client.",
                &e.to_string(),
            );
            reqwest::Client::new()
        });

        Self {
            client,
            api_url: config.proxmox_api_url.clone(),
            api_token: config.proxmox_api_token.clone(),
        }
    }

    /// Poll Proxmox REST API `/api2/json/nodes` để lấy danh sách node và metrics
    pub async fn fetch_nodes(&self) -> Result<Vec<ProxmoxNodeRaw>, String> {
        let url = format!("{}/api2/json/nodes", self.api_url.trim_end_matches('/'));

        let request = self
            .client
            .get(&url)
            // [COMMENT]: Proxmox yêu cầu Authorization header dạng: PVEAPIToken=user@realm!id=secret
            .header("Authorization", &self.api_token);
        let response = crate::observability::otel::OtelTracer::trace_http_request(
            "GET proxmox.nodes",
            vec![
                opentelemetry::KeyValue::new("http.request.method", "GET"),
                opentelemetry::KeyValue::new("server.address", "proxmox"),
                opentelemetry::KeyValue::new("url.template", "/api2/json/nodes"),
            ],
            request,
        )
        .await
        .map_err(|e| e.to_string())?;

        if !response.status().is_success() {
            return Err(format!(
                "Proxmox API trả về HTTP {}: {}",
                response.status(),
                response.text().await.unwrap_or_default()
            ));
        }

        let api_resp: ProxmoxApiResponse = response
            .json()
            .await
            .map_err(|e| format!("Không thể parse Proxmox API response: {}", e))?;

        Ok(api_resp.data)
    }
}
