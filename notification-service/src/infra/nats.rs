// Sinh mã Rust từ protobuf definitions của trinity.proto
pub mod trinity {
    tonic::include_proto!("trinity.rpc");
}

use trinity::{
    VerifyAdminTrinityTokenRequest, VerifyAdminTrinityTokenResponse,
    VerifyUserTrinityTokenRequest, VerifyUserTrinityTokenResponse,
};
use crate::observability::logger::Logger;
use prost::Message;
use tonic::Status;

#[derive(Clone)]
pub struct NatsAuthClient {
    nats_client: async_nats::Client,
}

impl NatsAuthClient {
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

        Self { nats_client }
    }

    // Gửi yêu cầu xác thực Admin qua NATS Core đến ACR
    pub async fn verify_admin_trinity_token(
        &self,
        access_token: String,
        access_key: String,
        access_secret: String,
    ) -> Result<VerifyAdminTrinityTokenResponse, Status> {
        let start_time = std::time::Instant::now();
        Logger::sys_info(
            "nats.auth_call",
            "Verifying admin trinity token via NATS Request-Reply",
        );

        let req = VerifyAdminTrinityTokenRequest {
            access_token,
            access_key,
            access_secret,
        };

        let mut payload = Vec::new();
        req.encode(&mut payload)
            .map_err(|e| Status::internal(format!("Failed to encode request: {}", e)))?;

        let response_msg = match self
            .nats_client
            .request("iam.auth.verify_admin_trinity".to_string(), payload.into())
            .await
        {
            Ok(msg) => msg,
            Err(e) => return Err(Status::unavailable(format!("NATS request failed: {}", e))),
        };

        let res = VerifyAdminTrinityTokenResponse::decode(response_msg.payload.as_ref())
            .map_err(|e| Status::internal(format!("Failed to decode response: {}", e)))?;

        let latency = start_time.elapsed();
        crate::observability::metrics::MetricsManager::record_grpc_call("verify_admin_trinity_token", "ok", latency);

        Ok(res)
    }

    // Gửi yêu cầu xác thực User qua NATS Core đến ACR
    pub async fn verify_user_trinity_token(
        &self,
        access_token: String,
        access_key: String,
        access_secret: String,
    ) -> Result<VerifyUserTrinityTokenResponse, Status> {
        let start_time = std::time::Instant::now();
        Logger::sys_info(
            "nats.auth_call",
            "Verifying user trinity token via NATS Request-Reply",
        );

        let req = VerifyUserTrinityTokenRequest {
            access_token,
            access_key,
            access_secret,
        };

        let mut payload = Vec::new();
        req.encode(&mut payload)
            .map_err(|e| Status::internal(format!("Failed to encode request: {}", e)))?;

        let response_msg = match self
            .nats_client
            .request("iam.auth.verify_user_trinity".to_string(), payload.into())
            .await
        {
            Ok(msg) => msg,
            Err(e) => return Err(Status::unavailable(format!("NATS request failed: {}", e))),
        };

        let res = VerifyUserTrinityTokenResponse::decode(response_msg.payload.as_ref())
            .map_err(|e| Status::internal(format!("Failed to decode response: {}", e)))?;

        let latency = start_time.elapsed();
        crate::observability::metrics::MetricsManager::record_grpc_call("verify_user_trinity_token", "ok", latency);

        Ok(res)
    }
}
