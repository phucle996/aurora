// Sinh mã Rust từ gRPC protobuf definitions dựa trên package name 'iam.rpc' tương thích Go
pub mod auth {
    // Nạp mã nguồn tự động sinh từ file proto có package name là 'iam.rpc'
    tonic::include_proto!("iam.rpc");
}

use tonic::transport::{Channel, Endpoint, ClientTlsConfig, Certificate, Identity};
use auth::auth_service_client::AuthServiceClient;
use auth::{
    VerifyAdminTrinityTokenRequest, VerifyAdminTrinityTokenResponse,
    VerifyUserTrinityTokenRequest, VerifyUserTrinityTokenResponse,
};
use crate::observability::logger::Logger;

#[derive(Clone)]
pub struct GrpcAuthClient {
    client: AuthServiceClient<Channel>,
}

impl GrpcAuthClient {
    pub fn new(
        endpoint: String,
        ca_cert: Option<String>,
        client_cert: Option<String>,
        client_key: Option<String>,
    ) -> Self {
        // [ignoring loop detection]
        let has_tls = ca_cert.is_some() || (client_cert.is_some() && client_key.is_some());
        
        let url = if has_tls {
            format!("https://{}", endpoint)
        } else {
            format!("http://{}", endpoint)
        };
        
        // Thiết lập endpoint với lazy connection, keep-alive và timeout để chống treo kết nối
        let mut endpoint_configured = Endpoint::from_shared(url)
            .expect("Invalid Controlplane gRPC endpoint URI")
            .connect_timeout(std::time::Duration::from_secs(5))
            .timeout(std::time::Duration::from_secs(5))
            .tcp_keepalive(Some(std::time::Duration::from_secs(15)));

        if has_tls {
            let mut tls_config = ClientTlsConfig::new();
            
            if let Some(ref ca_path) = ca_cert {
                let ca_pem = std::fs::read(ca_path)
                    .unwrap_or_else(|e| panic!("Failed to read gRPC CA cert from {}: {}", ca_path, e));
                let cert = Certificate::from_pem(ca_pem);
                tls_config = tls_config.ca_certificate(cert);
            }
            
            if let (Some(ref cert_path), Some(ref key_path)) = (client_cert.clone(), client_key.clone()) {
                let cert_pem = std::fs::read(&cert_path)
                    .unwrap_or_else(|e| panic!("Failed to read gRPC client cert from {}: {}", cert_path, e));
                let key_pem = std::fs::read(&key_path)
                    .unwrap_or_else(|e| panic!("Failed to read gRPC client key from {}: {}", key_path, e));
                let identity = Identity::from_pem(cert_pem, key_pem);
                tls_config = tls_config.identity(identity);
            }

            // Cấu hình domain_name khớp với Common Name (CN) hoặc Subject Alternative Name (SAN) trong cert tự ký
            let domain_name = std::env::var("CONTROLPLANE_GRPC_DOMAIN")
                .unwrap_or_else(|_| "localhost".to_string());
            tls_config = tls_config.domain_name(domain_name);

            endpoint_configured = endpoint_configured
                .tls_config(tls_config)
                .expect("Failed to configure gRPC client TLS");
        }

        // Khởi tạo kênh kết nối lazy (không block startup nếu Controlplane chưa sẵn sàng)
        let channel = endpoint_configured.connect_lazy();
        let client = AuthServiceClient::new(channel);

        Self { client }
    }

    // Gửi yêu cầu xác thực Admin qua gRPC đến Controlplane
    pub async fn verify_admin_trinity_token(
        &self,
        admin_api_token: String,
        access_key: String,
        access_secret: String,
    ) -> Result<VerifyAdminTrinityTokenResponse, tonic::Status> {
        let start_time = std::time::Instant::now();
        Logger::sys_info(
            "grpc.auth_call",
            "Verifying admin trinity token via lazy-connected gRPC Channel pool",
        );

        // Client của tonic rẻ để clone (chỉ clone underlying channel reference)
        let mut client = self.client.clone();

        let mut request = tonic::Request::new(VerifyAdminTrinityTokenRequest {
            admin_api_token,
            access_key,
            access_secret,
        });

        // Bơm traceparent W3C vào gRPC Metadata để tiếp tục distributed tracing ở Controlplane (Go)
        if let Some(traceparent) = crate::observability::otel::OtelTracer::get_traceparent() {
            if let Ok(meta_val) = tonic::metadata::MetadataValue::try_from(&traceparent) {
                request.metadata_mut().insert("traceparent", meta_val);
            }
        }

        let response = client.verify_admin_trinity_token(request).await;

        // Lưu trữ các chỉ số đo đạc cuộc gọi gRPC
        let duration = start_time.elapsed().as_secs_f64();
        let status = if response.is_ok() { "ok" } else { "error" };
        crate::observability::prometheus::GRPC_CALLS_TOTAL.with_label_values(&["verify_admin_trinity_token", status]).inc();
        crate::observability::prometheus::GRPC_CALL_DURATION_SECONDS.with_label_values(&["verify_admin_trinity_token", status]).observe(duration);

        match response {
            Ok(res) => Ok(res.into_inner()),
            Err(status) => Err(status),
        }
    }

    // Gửi yêu cầu xác thực User qua gRPC đến Controlplane
    pub async fn verify_user_trinity_token(
        &self,
        access_token: String,
        access_key: String,
        access_secret: String,
    ) -> Result<VerifyUserTrinityTokenResponse, tonic::Status> {
        let start_time = std::time::Instant::now();
        Logger::sys_info(
            "grpc.auth_call",
            "Verifying user trinity token via lazy-connected gRPC Channel pool",
        );

        // Client của tonic rẻ để clone (chỉ clone underlying channel reference)
        let mut client = self.client.clone();

        let mut request = tonic::Request::new(VerifyUserTrinityTokenRequest {
            access_token,
            access_key,
            access_secret,
        });

        // Bơm traceparent W3C vào gRPC Metadata để tiếp tục distributed tracing ở Controlplane (Go)
        if let Some(traceparent) = crate::observability::otel::OtelTracer::get_traceparent() {
            if let Ok(meta_val) = tonic::metadata::MetadataValue::try_from(&traceparent) {
                request.metadata_mut().insert("traceparent", meta_val);
            }
        }

        let response = client.verify_user_trinity_token(request).await;

        // Lưu trữ các chỉ số đo đạc cuộc gọi gRPC
        let duration = start_time.elapsed().as_secs_f64();
        let status = if response.is_ok() { "ok" } else { "error" };
        crate::observability::prometheus::GRPC_CALLS_TOTAL.with_label_values(&["verify_user_trinity_token", status]).inc();
        crate::observability::prometheus::GRPC_CALL_DURATION_SECONDS.with_label_values(&["verify_user_trinity_token", status]).observe(duration);

        match response {
            Ok(res) => Ok(res.into_inner()),
            Err(status) => Err(status),
        }
    }
}
