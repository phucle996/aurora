// ======================================================================================================
// 📂 MODULE: acl/src/rpc/session.rs
//            Triển khai gRPC SessionService Handler (Transport/Presentation Layer)
// ======================================================================================================

use crate::core::session::SessionManager;
use crate::core::token::TokenManager;
use crate::service::session::release_session::session_proto::{
    session_service_server::SessionService, ReleaseTrinitySessionRequest,
    ReleaseTrinitySessionResponse, RevokeUserSessionsByDevicesRequest,
    RevokeUserSessionsByDevicesResponse,
};
use std::sync::Arc;
use tonic::{Request, Response, Status};

pub struct SessionRpcHandler {
    session_mgr: Arc<SessionManager>,
    token_mgr: Arc<TokenManager>,
    zone_mgr: Arc<crate::core::zone::ZoneManager>,
    config: crate::config::Config,
}

impl SessionRpcHandler {
    pub fn new(
        session_mgr: Arc<SessionManager>,
        token_mgr: Arc<TokenManager>,
        zone_mgr: Arc<crate::core::zone::ZoneManager>,
        config: crate::config::Config,
    ) -> Self {
        Self {
            session_mgr,
            token_mgr,
            zone_mgr,
            config,
        }
    }
}

#[tonic::async_trait]
impl SessionService for SessionRpcHandler {
    // [COMMENT]: Nhận RPC cấp mới Trinity Session, dispatch sang service/session/release_session.rs
    async fn release_trinity_session(
        &self,
        request: Request<ReleaseTrinitySessionRequest>,
    ) -> Result<Response<ReleaseTrinitySessionResponse>, Status> {
        crate::service::session::release_session::release_trinity_session(
            &self.session_mgr,
            &self.token_mgr,
            &self.zone_mgr,
            &self.config,
            request,
        )
        .await
    }

    // [COMMENT]: Nhận RPC thu hồi các session thuộc danh sách các thiết bị, dispatch sang service/device/revoke_device.rs
    async fn revoke_user_sessions_by_devices(
        &self,
        request: Request<RevokeUserSessionsByDevicesRequest>,
    ) -> Result<Response<RevokeUserSessionsByDevicesResponse>, Status> {
        crate::service::device::revoke_user_sessions_by_devices(&self.session_mgr, request).await
    }
}
