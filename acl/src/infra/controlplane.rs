// [COMMENT]: Sinh mã Rust từ gRPC protobuf definitions dựa trên package name 'iam.rpc' tương thích Go
pub mod auth {
    tonic::include_proto!("iam.rpc");
}

// [COMMENT]: Sinh mã Rust từ gRPC protobuf definitions dựa trên package name 'core.rpc' tương thích Go
pub mod zone_proto {
    tonic::include_proto!("core.rpc");
}

use auth::auth_service_client::AuthServiceClient;
use auth::RevokeOpaqueRefreshTokenRequest;
use zone_proto::zone_service_client::ZoneServiceClient;
use tonic::transport::{Certificate, Channel, ClientTlsConfig, Endpoint, Identity};

// [COMMENT]: Client tương tác với Control Plane gRPC server để thực hiện các cuộc gọi nghiệp vụ nội bộ
#[derive(Clone)]
pub struct ControlPlaneClient {
    client: AuthServiceClient<Channel>,
    zone_client: ZoneServiceClient<Channel>,
}

impl ControlPlaneClient {
    pub fn new(
        endpoint: String,
        ca_cert: Option<String>,
        client_cert: Option<String>,
        client_key: Option<String>,
    ) -> Self {
        // [COMMENT]: Kiểm tra cấu hình có yêu cầu TLS/mTLS hay không
        let has_tls = ca_cert.is_some() || (client_cert.is_some() && client_key.is_some());

        let url = if has_tls {
            format!("https://{}", endpoint)
        } else {
            format!("http://{}", endpoint)
        };

        // [COMMENT]: Thiết lập endpoint với lazy connection, keep-alive và timeout để chống treo kết nối khi hệ thống tải cao
        let mut endpoint_configured = Endpoint::from_shared(url)
            .expect("Invalid Controlplane gRPC endpoint URI")
            .connect_timeout(std::time::Duration::from_secs(5))
            .timeout(std::time::Duration::from_secs(5))
            .tcp_keepalive(Some(std::time::Duration::from_secs(15)));

        if has_tls {
            let mut tls_config = ClientTlsConfig::new();

            if let Some(ref ca_path) = ca_cert {
                let ca_pem = std::fs::read(ca_path).unwrap_or_else(|e| {
                    panic!("Failed to read gRPC CA cert from {}: {}", ca_path, e)
                });
                let cert = Certificate::from_pem(ca_pem);
                tls_config = tls_config.ca_certificate(cert);
            }

            if let (Some(ref cert_path), Some(ref key_path)) =
                (client_cert.clone(), client_key.clone())
            {
                let cert_pem = std::fs::read(&cert_path).unwrap_or_else(|e| {
                    panic!("Failed to read gRPC client cert from {}: {}", cert_path, e)
                });
                let key_pem = std::fs::read(&key_path).unwrap_or_else(|e| {
                    panic!("Failed to read gRPC client key from {}: {}", key_path, e)
                });
                let identity = Identity::from_pem(cert_pem, key_pem);
                tls_config = tls_config.identity(identity);
            }

            // [COMMENT]: Cấu hình domain_name khớp với Common Name (CN) hoặc Subject Alternative Name (SAN) trong cert tự ký
            let domain_name = std::env::var("CONTROLPLANE_GRPC_DOMAIN")
                .unwrap_or_else(|_| "localhost".to_string());
            tls_config = tls_config.domain_name(domain_name);

            endpoint_configured = endpoint_configured
                .tls_config(tls_config)
                .expect("Failed to configure gRPC client TLS");
        }

        // [COMMENT]: Khởi tạo kênh kết nối lazy (không block startup của ACL nếu Controlplane chưa sẵn sàng)
        let channel = endpoint_configured.connect_lazy();
        let client = AuthServiceClient::new(channel.clone());
        let zone_client = ZoneServiceClient::new(channel);

        Self { client, zone_client }
    }

    // [COMMENT]: Thu hồi Opaque Refresh Token bất đồng bộ (gọi qua gRPC đến CP)
    pub async fn revoke_opaque_refresh_token(
        &self,
        refresh_token: String,
    ) -> Result<(), tonic::Status> {
        let mut client = self.client.clone();
        let mut request = tonic::Request::new(RevokeOpaqueRefreshTokenRequest { refresh_token });

        // [COMMENT]: Bơm traceparent W3C vào gRPC Metadata để tiếp tục distributed tracing ở Controlplane (Go)
        if let Some(trace_id) = crate::observability::otel::OtelTracer::get_current_trace_id() {
            // [COMMENT]: Sinh span_id ngẫu nhiên 16 ký tự hex để đóng gói traceparent chuẩn
            let span_id = uuid::Uuid::new_v4().simple().to_string()[..16].to_string();
            let traceparent = format!("00-{}-{}-01", trace_id, span_id);
            if let Ok(meta_val) = tonic::metadata::MetadataValue::try_from(&traceparent) {
                request.metadata_mut().insert("traceparent", meta_val);
            }
        }

        client.revoke_opaque_refresh_token(request).await?;
        Ok(())
    }

    // [COMMENT]: Xác thực Opaque Refresh Token đồng bộ qua gRPC đến Controlplane
    pub async fn verify_opaque_refresh_token(
        &self,
        refresh_token: String,
        scope: String,
    ) -> Result<auth::VerifyOpaqueRefreshTokenResponse, tonic::Status> {
        let mut client = self.client.clone();
        let mut request = tonic::Request::new(auth::VerifyOpaqueRefreshTokenRequest {
            refresh_token,
            scope,
        });

        // [COMMENT]: Bơm traceparent W3C vào gRPC Metadata để tiếp tục distributed tracing ở Controlplane (Go)
        if let Some(trace_id) = crate::observability::otel::OtelTracer::get_current_trace_id() {
            // [COMMENT]: Sinh span_id ngẫu nhiên 16 ký tự hex để đóng gói traceparent chuẩn
            let span_id = uuid::Uuid::new_v4().simple().to_string()[..16].to_string();
            let traceparent = format!("00-{}-{}-01", trace_id, span_id);
            if let Ok(meta_val) = tonic::metadata::MetadataValue::try_from(&traceparent) {
                request.metadata_mut().insert("traceparent", meta_val);
            }
        }

        let response = client.verify_opaque_refresh_token(request).await?;
        Ok(response.into_inner())
    }

    // [COMMENT]: Lấy danh sách Zone từ Controlplane qua gRPC phục vụ đồng bộ L1 cache ở ACL
    pub async fn get_zone_list(&self) -> Result<Vec<zone_proto::ZoneEntry>, tonic::Status> {
        let mut client = self.zone_client.clone();
        let mut request = tonic::Request::new(zone_proto::GetZoneListRequest {});

        // [COMMENT]: Bơm traceparent W3C vào gRPC Metadata để phục vụ distributed tracing
        if let Some(trace_id) = crate::observability::otel::OtelTracer::get_current_trace_id() {
            let span_id = uuid::Uuid::new_v4().simple().to_string()[..16].to_string();
            let traceparent = format!("00-{}-{}-01", trace_id, span_id);
            if let Ok(meta_val) = tonic::metadata::MetadataValue::try_from(&traceparent) {
                request.metadata_mut().insert("traceparent", meta_val);
            }
        }

        let response = client.get_zone_list(request).await?;
        Ok(response.into_inner().zones)
    }
}
