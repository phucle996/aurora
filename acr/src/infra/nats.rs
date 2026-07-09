use async_nats::HeaderMap;
use prost::Message;

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

use auth::{VerifyUserCredentialsRequest, VerifyUserCredentialsResponse};

// [COMMENT]: Client tương tác với Control Plane qua NATS Core Pub/Sub (Request-Reply)
#[derive(Clone)]
pub struct ControlPlaneClient {
    nats_client: async_nats::Client,
}

impl ControlPlaneClient {
    pub async fn new(
        nats_url: String,
        _ca_cert: Option<String>,
        _client_cert: Option<String>,
        _client_key: Option<String>,
    ) -> Self {
        // [COMMENT]: Khởi tạo kết nối đến NATS Core
        let nats_client = async_nats::connect(&nats_url)
            .await
            .unwrap_or_else(|e| panic!("Failed to connect to NATS at {}: {}", nats_url, e));

        Self { nats_client }
    }

    // [COMMENT]: Gọi VerifyUserCredentials sang Control Plane qua NATS Core Request-Reply
    pub async fn verify_user_credentials(
        &self,
        request: VerifyUserCredentialsRequest,
    ) -> Result<VerifyUserCredentialsResponse, tonic::Status> {
        let mut payload = Vec::new();
        request
            .encode(&mut payload)
            .map_err(|e| tonic::Status::internal(format!("Failed to encode request: {}", e)))?;

        // [COMMENT]: Bơm traceparent W3C vào NATS Headers để phân tích vết phân tán
        let mut headers = HeaderMap::new();
        if let Some(trace_id) = crate::observability::otel::OtelTracer::get_current_trace_id() {
            let span_id = uuid::Uuid::new_v4().simple().to_string()[..16].to_string();
            let traceparent = format!("00-{}-{}-01", trace_id, span_id);
            headers.insert("traceparent", traceparent.as_str());
        }

        let reply_subject = "iam.auth.verify_credentials";

        let response_msg = match self
            .nats_client
            .request_with_headers(reply_subject.to_string(), headers, payload.into())
            .await
        {
            Ok(msg) => msg,
            Err(e) => {
                return Err(tonic::Status::unavailable(format!(
                    "NATS request failed: {}",
                    e
                )));
            }
        };

        // [COMMENT]: Giải mã protobuf response
        let response = VerifyUserCredentialsResponse::decode(response_msg.payload.as_ref())
            .map_err(|e| tonic::Status::internal(format!("Failed to decode response: {}", e)))?;

        Ok(response)
    }

    // [COMMENT]: Mock các hàm gRPC khác để tránh lỗi compile các module khác của ACR
    pub async fn verify_opaque_refresh_token(
        &self,
        _refresh_token: String,
        _tenant_id: Option<String>,
        _user_id: String,
    ) -> Result<auth::VerifyOpaqueRefreshTokenResponse, tonic::Status> {
        Ok(auth::VerifyOpaqueRefreshTokenResponse {
            valid: false,
            user_id: String::new(),
            tenant_id: String::new(),
            role: String::new(),
            level: 0,
            error_message: "not implemented".to_string(),
            username: String::new(),
        })
    }

    pub async fn revoke_opaque_refresh_token(
        &self,
        _refresh_token: String,
    ) -> Result<(), tonic::Status> {
        Ok(())
    }

    pub async fn get_zone_list(&self) -> Result<Vec<zone_proto::ZoneEntry>, tonic::Status> {
        Ok(vec![])
    }
}
