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
    ) -> Self {
        // [COMMENT]: Khởi tạo kết nối đến NATS Core
        let nats_client = async_nats::connect(&nats_url)
            .await
            .unwrap_or_else(|e| panic!("Failed to connect to NATS at {}: {}", nats_url, e));

        Self { nats_client }
    }

    // [COMMENT]: Expose NATS Client để các call site trực tiếp gọi
    pub fn client(&self) -> &async_nats::Client {
        &self.nats_client
    }

    pub async fn get_zone_list(&self) -> Result<Vec<zone_proto::ZoneEntry>, tonic::Status> {
        Ok(vec![])
    }
}
