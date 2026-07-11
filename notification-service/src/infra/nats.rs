
#[derive(Clone)]
pub struct NatsClient {
    client: async_nats::Client,
}

impl NatsClient {
    // [COMMENT]: Khởi tạo NATS client kết nối hỗ trợ TLS/mTLS
    pub async fn new(
        nats_url: String,
        ca_cert: Option<String>,
        client_cert: Option<String>,
        client_key: Option<String>,
    ) -> Self {
        let mut options = async_nats::ConnectOptions::new();

        // Cấu hình TLS CA Certificate
        if let Some(ref ca_path) = ca_cert {
            options = options.add_root_certificates(ca_path.clone().into());
            options = options.require_tls(true);
        }

        // Cấu hình mTLS Client Certificate & Private Key
        if let (Some(ref cert_path), Some(ref key_path)) = (client_cert, client_key) {
            options = options.add_client_certificate(cert_path.clone().into(), key_path.clone().into());
            options = options.require_tls(true);
        }

        let nats_client = options.connect(&nats_url)
            .await
            .unwrap_or_else(|e| panic!("Failed to connect to NATS at {}: {}", nats_url, e));

        Self { client: nats_client }
    }

    /// Lấy bản sao client NATS phục vụ các dịch vụ
    pub fn client(&self) -> async_nats::Client {
        self.client.clone()
    }
}
