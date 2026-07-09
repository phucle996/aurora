use crate::core::session::SessionManager;
use crate::observability::logger::Logger;
use crate::service::session::release_session::device_proto::{
    RevokeUserSessionsByDevicesRequest, RevokeUserSessionsByDevicesResponse,
};
use prost::Message;
use std::sync::Arc;

/// [COMMENT]: Giải mã yêu cầu từ bytes, thực thi quét Redis L2 để chuyển đổi TTL
/// của toàn bộ session thuộc thiết bị được yêu cầu về 5s, và trả về phản hồi dạng bytes.
pub async fn revoke_sessions_bytes(session_mgr: &Arc<SessionManager>, payload: &[u8]) -> Vec<u8> {
    let req = match RevokeUserSessionsByDevicesRequest::decode(payload) {
        Ok(r) => r,
        Err(e) => {
            Logger::sys_error(
                "device.revoke",
                "Failed to decode RevokeUserSessionsByDevicesRequest",
                &e.to_string(),
            );
            return vec![];
        }
    };

    Logger::sys_info(
        "device.revoke",
        &format!(
            "Revoking sessions for user_id={} and device_ids={:?}",
            req.user_id, req.device_ids
        ),
    );

    // Lấy kết nối Redis L2
    let mut conn = match session_mgr.get_connection().await {
        Ok(c) => c,
        Err(e) => {
            Logger::sys_error(
                "device.revoke",
                "Failed to get Redis connection",
                &e.to_string(),
            );
            return vec![];
        }
    };

    let mut revoked_count = 0;

    for device_id in &req.device_ids {
        let dev_index_key = format!("iam:device_access_index:{}", device_id);

        // 1. Lấy tất cả access_key của thiết bị này
        let access_keys: Vec<String> = match redis::cmd("SMEMBERS")
            .arg(&dev_index_key)
            .query_async(&mut conn)
            .await
        {
            Ok(keys) => keys,
            Err(e) => {
                Logger::sys_error(
                    "device.revoke",
                    &format!("SMEMBERS failed for device {}", device_id),
                    &e.to_string(),
                );
                continue;
            }
        };

        if access_keys.is_empty() {
            continue;
        }

        // 2. Chuyển đổi TTL session về 5s và loại bỏ khỏi user index
        let user_index_key = format!("iam:user_access_index:{}", req.user_id);
        let mut pipe = redis::pipe();
        pipe.atomic();

        for access_key in &access_keys {
            pipe.cmd("EXPIRE").arg(access_key).arg(5);
            pipe.cmd("SREM").arg(&user_index_key).arg(access_key);
            revoked_count += 1;
        }

        // 3. Xóa index của thiết bị này
        pipe.cmd("DEL").arg(&dev_index_key);

        if let Err(e) = pipe.query_async::<_, ()>(&mut conn).await {
            Logger::sys_error(
                "device.revoke",
                &format!(
                    "Revoke device session pipeline failed for device {}",
                    device_id
                ),
                &e.to_string(),
            );
        }
    }

    Logger::sys_info(
        "device.revoke",
        &format!(
            "Successfully revoked {} sessions for user_id={}",
            revoked_count, req.user_id
        ),
    );

    let res = RevokeUserSessionsByDevicesResponse { revoked_count };
    let mut reply_payload = Vec::new();
    if res.encode(&mut reply_payload).is_ok() {
        reply_payload
    } else {
        vec![]
    }
}
