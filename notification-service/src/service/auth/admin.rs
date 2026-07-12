use crate::observability::logger::Logger;
use crate::service::auth::trinity::{
    VerifyAdminTrinityTokenRequest, VerifyAdminTrinityTokenResponse,
};
use prost::Message;
use tonic::Status;

// [COMMENT]: Thực hiện cuộc gọi gRPC/NATS xác thực Trinity Token của Admin
pub async fn verify_admin_token(
    nats_client: &async_nats::Client,
    access_token: String,
    access_key: String,
    access_secret: String,
) -> Result<VerifyAdminTrinityTokenResponse, Status> {
    let start_time = std::time::Instant::now();
    Logger::sys_info(
        "auth_service.admin",
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

    let response_msg = match nats_client
        .request("iam.auth.verify_admin_trinity".to_string(), payload.into())
        .await
    {
        Ok(msg) => msg,
        Err(e) => return Err(Status::unavailable(format!("NATS request failed: {}", e))),
    };

    let res = VerifyAdminTrinityTokenResponse::decode(response_msg.payload.as_ref())
        .map_err(|e| Status::internal(format!("Failed to decode response: {}", e)))?;

    let latency = start_time.elapsed();
    crate::observability::metrics::MetricsManager::record_nats_call(
        "verify_admin_trinity_token",
        "ok",
        latency,
    );

    Ok(res)
}
