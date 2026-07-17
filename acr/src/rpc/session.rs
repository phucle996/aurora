// ======================================================================================================
// 📂 rpc/session.rs — Device RPC Handler (gRPC DeviceService implementation)
// ======================================================================================================

use crate::infra::redis::SessionManager;
use crate::user::device::device_proto::{
    device_service_server::DeviceService, RevokeUserSessionsByDevicesRequest,
    RevokeUserSessionsByDevicesResponse,
};
use std::sync::Arc;
use tonic::{Request, Response, Status};

pub struct DeviceRpcHandler {
    session_mgr: Arc<SessionManager>,
}

impl DeviceRpcHandler {
    pub fn new(session_mgr: Arc<SessionManager>) -> Self {
        Self { session_mgr }
    }
}

#[tonic::async_trait]
impl DeviceService for DeviceRpcHandler {
    async fn revoke_user_sessions_by_devices(
        &self,
        request: Request<RevokeUserSessionsByDevicesRequest>,
    ) -> Result<Response<RevokeUserSessionsByDevicesResponse>, Status> {
        use prost::Message;
        let req = request.into_inner();
        let mut payload = Vec::new();
        if req.encode(&mut payload).is_err() {
            return Err(Status::internal("Failed to encode request"));
        }
        let reply_payload = crate::user::revoke::revoke_sessions_bytes(&self.session_mgr, &payload).await;
        let resp = RevokeUserSessionsByDevicesResponse::decode(reply_payload.as_slice())
            .map_err(|e| Status::internal(e.to_string()))?;
        Ok(Response::new(resp))
    }
}
