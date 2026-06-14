use crate::config::Config;
use crate::observability::logger::Logger;
use tonic::transport::{Certificate, Channel, ClientTlsConfig, Identity};

// Nạp gRPC code generated từ backpressure.proto
pub mod core_rpc {
    tonic::include_proto!("core.rpc");
}

use core_rpc::backpressure_service_client::BackpressureServiceClient;

/// Thiết lập kênh kết nối gRPC an toàn hỗ trợ mTLS.
/// Tự động dò và nạp CA, Certificate, Key nếu có cấu hình TLS.
pub async fn create_grpc_channel(config: &Config) -> Result<Channel, Box<dyn std::error::Error>> {
    let endpoint = &config.controlplane_grpc_endpoint;

    let has_tls = config.controlplane_grpc_ca_cert.is_some()
        && config.controlplane_grpc_client_cert.is_some()
        && config.controlplane_grpc_client_key.is_some();

    // Quyết định URL Scheme: Dùng https nếu có TLS, http nếu chạy dev không mã hóa
    let uri = if endpoint.starts_with("http://") || endpoint.starts_with("https://") {
        endpoint.to_string()
    } else if has_tls {
        format!("https://{}", endpoint)
    } else {
        format!("http://{}", endpoint)
    };

    Logger::sys_info(
        "grpc_transport.connect",
        &format!("Đang khởi tạo kênh gRPC tới: {} (has_tls={})", uri, has_tls),
    );

    let mut endpoint_config = Channel::from_shared(uri)?;

    // Áp dụng mTLS khi cấu hình đường dẫn CA và chứng chỉ client được truyền đầy đủ
    if let (Some(ca_path), Some(cert_path), Some(key_path)) = (
        &config.controlplane_grpc_ca_cert,
        &config.controlplane_grpc_client_cert,
        &config.controlplane_grpc_client_key,
    ) {
        let ca_pem = tokio::fs::read(ca_path).await?;
        let cert_pem = tokio::fs::read(cert_path).await?;
        let key_pem = tokio::fs::read(key_path).await?;

        let server_ca = Certificate::from_pem(ca_pem);
        let client_identity = Identity::from_pem(cert_pem, key_pem);

        let mut tls_config = ClientTlsConfig::new()
            .ca_certificate(server_ca)
            .identity(client_identity);

        if let Some(dom) = &config.controlplane_grpc_domain {
            tls_config = tls_config.domain_name(dom);
        }

        endpoint_config = endpoint_config.tls_config(tls_config)?;
    }

    let channel = endpoint_config.connect().await?;
    Ok(channel)
}

/// Thực hiện cuộc gọi gRPC báo cáo backpressure của zone xuống controlplane.
/// Sử dụng cơ chế Lazy initialization để tái sử dụng connection channel.
pub async fn send_backpressure_report(
    client_opt: &mut Option<BackpressureServiceClient<Channel>>,
    config: &Config,
    zone_id: &str,
    queue_len: i64,
    pending_len: i64,
    congested: bool,
    epoch: i64,
    congestion_rate: f64,
) -> Result<(), Box<dyn std::error::Error>> {
    let client = match client_opt {
        Some(c) => c,
        None => {
            let channel = create_grpc_channel(config).await?;
            let client = BackpressureServiceClient::new(channel);
            *client_opt = Some(client);
            client_opt.as_mut().unwrap()
        }
    };

    let request = tonic::Request::new(core_rpc::ReportBackpressureRequest {
        zone_id: zone_id.to_string(),
        queue_len,
        pending_len,
        congested,
        epoch,
        congestion_rate,
    });

    client.report_backpressure(request).await?;
    Ok(())
}
