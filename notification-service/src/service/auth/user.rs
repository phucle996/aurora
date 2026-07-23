use crate::infra::shared_redis::SharedRedisRequestBus;
use crate::observability::logger::Logger;
use crate::service::auth::trinity::{
    VerifyUserTrinityTokenRequest, VerifyUserTrinityTokenResponse,
};
use prost::Message;
use std::sync::Arc;
use tonic::Status;

// [COMMENT]: Thực hiện Shared Redis request/reply xác thực Trinity Token của User.
pub async fn verify_user_token(
    shared_redis: &Arc<SharedRedisRequestBus>,
    access_token: String,
    access_key: String,
    access_secret: String,
) -> Result<VerifyUserTrinityTokenResponse, Status> {
    let start_time = std::time::Instant::now();
    Logger::sys_info(
        "auth_service.user",
        "Verifying user trinity token via Shared Redis",
    );

    let req = VerifyUserTrinityTokenRequest {
        access_token,
        access_key,
        access_secret,
    };

    let mut payload = Vec::new();
    req.encode(&mut payload)
        .map_err(|e| Status::internal(format!("Failed to encode request: {}", e)))?;

    let response_payload = match shared_redis
        .request(
            "iam.auth.verify_user_trinity",
            "iam.auth.verify_user_trinity.reply.",
            payload,
        )
        .await
    {
        Ok(value) => value,
        Err(e) => {
            return Err(Status::unavailable(format!(
                "Shared Redis request failed: {}",
                e
            )))
        }
    };

    let res = VerifyUserTrinityTokenResponse::decode(response_payload.as_slice())
        .map_err(|e| Status::internal(format!("Failed to decode response: {}", e)))?;

    let latency = start_time.elapsed();
    crate::observability::metrics::MetricsManager::record_redis_call(
        "verify_user_trinity_token",
        "ok",
        latency,
    );

    Ok(res)
}
