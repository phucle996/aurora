// ======================================================================================================
// 📂 MODULE: acl/src/rpc/session.rs
//            Triển khai gRPC DeviceService Handler (Transport/Presentation Layer)
//            Lưu ý: Đổi tên từ SessionService sang DeviceService sau khi bỏ ReleaseTrinitySession RPC.
// ======================================================================================================

use crate::core::session::SessionManager;
use crate::service::session::release_session::device_proto::{
    // [COMMENT]: Import DeviceService server trait và các message của RevokeUserSessionsByDevices
    device_service_server::DeviceService,
    RevokeUserSessionsByDevicesRequest,
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
    // [COMMENT]: Nhận RPC thu hồi session thuộc danh sách thiết bị, dispatch sang service/device/revoke_device.rs
    async fn revoke_user_sessions_by_devices(
        &self,
        request: Request<RevokeUserSessionsByDevicesRequest>,
    ) -> Result<Response<RevokeUserSessionsByDevicesResponse>, Status> {
        crate::service::device::revoke_user_sessions_by_devices(&self.session_mgr, request).await
    }
}
