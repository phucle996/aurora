// [COMMENT]: Sinh mã Rust từ gRPC protobuf definitions dựa trên package name 'iam.rpc' tương thích Go
#[allow(dead_code)]
#[allow(unused_imports)]
pub mod auth {
    tonic::include_proto!("iam.rpc");
}

// [COMMENT]: Sinh mã Rust từ gRPC protobuf definitions dựa trên package name 'core.rpc' tương thích Go
pub mod zone_proto {
    tonic::include_proto!("core.rpc");
}

// [COMMENT]: Sinh mã Rust từ protobuf definitions của trinity.proto
#[allow(dead_code)]
#[allow(unused_imports)]
pub mod trinity {
    tonic::include_proto!("trinity.rpc");
}

// [COMMENT]: Quản lý kết nối đến NATS Core
#[derive(Clone)]
pub struct Nats {
    nats_client: async_nats::Client,
}

impl Nats {
    pub async fn new(
        nats_url: String,
        ca_cert: Option<String>,
        client_cert: Option<String>,
        client_key: Option<String>,
    ) -> Self {
        let mut options = async_nats::ConnectOptions::new();

        // [COMMENT]: Cấu hình TLS CA Certificate
        if let Some(ref ca_path) = ca_cert {
            options = options.add_root_certificates(ca_path.clone().into());
            options = options.require_tls(true);
        }

        // [COMMENT]: Cấu hình mTLS Client Certificate & Private Key
        if let (Some(ref cert_path), Some(ref key_path)) = (client_cert, client_key) {
            options =
                options.add_client_certificate(cert_path.clone().into(), key_path.clone().into());
            options = options.require_tls(true);
        }

        // [COMMENT]: Khởi tạo kết nối đến NATS Core
        let nats_client = options
            .connect(&nats_url)
            .await
            .unwrap_or_else(|e| panic!("Failed to connect to NATS at {}: {}", nats_url, e));

        Self { nats_client }
    }

    // [COMMENT]: Expose NATS Client để các call site trực tiếp gọi
    pub fn client(&self) -> &async_nats::Client {
        &self.nats_client
    }

    pub async fn get_zone_list(&self) -> Result<Vec<zone_proto::ZoneEntry>, tonic::Status> {
        use prost::Message;
        let req = zone_proto::GetZoneListRequest {};
        let mut buf = Vec::new();
        req.encode(&mut buf)
            .map_err(|e| tonic::Status::internal(format!("Failed to encode request: {}", e)))?;

        match self.nats_client.request("core.zone.get_zone_list".to_string(), buf.into()).await {
            Ok(msg) => {
                let resp = zone_proto::GetZoneListResponse::decode(msg.payload)
                    .map_err(|e| tonic::Status::internal(format!("Failed to decode GetZoneListResponse: {}", e)))?;
                Ok(resp.zones)
            }
            Err(e) => {
                Err(tonic::Status::internal(format!("NATS request failed: {}", e)))
            }
        }
    }
}
