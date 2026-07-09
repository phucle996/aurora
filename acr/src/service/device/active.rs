use crate::core::session::SessionManager;
use crate::infra::nats::auth::{
    ActiveDeviceEntry, GetActiveDevicesRequest, GetActiveDevicesResponse,
};
use crate::observability::logger::Logger;
use prost::Message;
use std::sync::Arc;

/// [COMMENT]: Giải mã yêu cầu lấy danh sách session thiết bị đang online từ NATS bytes,
/// quét Redis L2 cache, và trả về dữ liệu kết quả được mã hóa nhị phân Protobuf.
pub async fn get_active_devices_bytes(
    session_mgr: &Arc<SessionManager>,
    payload: &[u8],
) -> Vec<u8> {
    let req = match GetActiveDevicesRequest::decode(payload) {
        Ok(r) => r,
        Err(e) => {
            Logger::sys_error(
                "device.active",
                "Failed to decode GetActiveDevicesRequest",
                &e.to_string(),
            );
            return vec![];
        }
    };

    let mut active_devices = Vec::new();
    match session_mgr.get_active_sessions(&req.user_id).await {
        Ok(sessions) => {
            for (tdid, lsa) in sessions {
                active_devices.push(ActiveDeviceEntry {
                    client_device_id: tdid,
                    last_seen_at: lsa,
                });
            }
        }
        Err(e) => {
            Logger::sys_error(
                "device.active",
                &format!(
                    "Failed to retrieve active sessions from Redis for user_id={}",
                    req.user_id
                ),
                &e.to_string(),
            );
        }
    }

    let res = GetActiveDevicesResponse { active_devices };
    let mut reply_payload = Vec::new();
    if res.encode(&mut reply_payload).is_ok() {
        reply_payload
    } else {
        Logger::sys_error(
            "device.active",
            "Failed to encode GetActiveDevicesResponse",
            "",
        );
        vec![]
    }
}
