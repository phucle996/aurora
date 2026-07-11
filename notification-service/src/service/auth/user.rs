// Sinh mã Rust từ protobuf definitions của trinity.proto
pub mod trinity {
    tonic::include_proto!("trinity.rpc");
}

use trinity::{VerifyUserTrinityTokenRequest, VerifyUserTrinityTokenResponse};
use crate::observability::logger::Logger;
use prost::Message;
use tonic::Status;

// [COMMENT]: Thực hiện cuộc gọi gRPC/NATS xác thực Trinity Token của User
pub async fn verify_user_token(
    nats_client: &async_nats::Client,
    access_token: String,
    access_key: String,
    access_secret: String,
) -> Result<VerifyUserTrinityTokenResponse, Status> {
    let start_time = std::time::Instant::now();
    Logger::sys_info(
        "auth_service.user",
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

    let response_msg = match nats_client
        .request("iam.auth.verify_user_trinity".to_string(), payload.into())
        .await
    {
        Ok(msg) => msg,
        Err(e) => return Err(Status::unavailable(format!("NATS request failed: {}", e))),
    };

    let res = VerifyUserTrinityTokenResponse::decode(response_msg.payload.as_ref())
        .map_err(|e| Status::internal(format!("Failed to decode response: {}", e)))?;

    let latency = start_time.elapsed();
    crate::observability::metrics::MetricsManager::record_nats_call("verify_user_trinity_token", "ok", latency);

    Ok(res)
}
