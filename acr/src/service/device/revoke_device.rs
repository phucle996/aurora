// ======================================================================================================
// 📂 MODULE: acl/src/service/device/revoke_device.rs
//            Triển khai nghiệp vụ chi tiết thu hồi phiên thiết bị (Device Session Revocation) từ A - Z
// ======================================================================================================

use crate::core::session::SessionManager;
use crate::observability::logger::Logger;
// [COMMENT]: Import device_proto (đổi tên từ session_proto) để lấy message types của DeviceService
use crate::service::session::release_session::device_proto;
use std::sync::Arc;
use tonic::{Request, Response, Status};

/// [COMMENT]: Xử lý gRPC request thu hồi session của các thiết bị cho một user từ A - Z trong một hàm duy nhất.
/// Nhận diện yêu cầu, thực thi truy vấn Redis L2 (SMEMBERS & pipeline EXPIRE/SREM) và trả kết quả/lỗi gRPC Status.
pub async fn revoke_user_sessions_by_devices(
    session_mgr: &Arc<SessionManager>,
    request: Request<device_proto::RevokeUserSessionsByDevicesRequest>,
) -> Result<Response<device_proto::RevokeUserSessionsByDevicesResponse>, Status> {
    let req = request.into_inner();
    Logger::sys_info(
        "session.revoke_by_devices",
        &format!(
            "Received request to revoke sessions for user_id={} and device_ids={:?}",
            req.user_id, req.device_ids
        ),
    );

    // [COMMENT]: Lấy kết nối Redis L2 trực tiếp từ session_mgr
    let mut conn = match session_mgr.get_connection().await {
        Ok(c) => c,
        Err(e) => {
            Logger::sys_error(
                "session.revoke_by_devices",
                "Failed to get Redis connection",
                &e.to_string(),
            );
            return Err(Status::internal(format!(
                "Failed to get Redis connection: {}",
                e
            )));
        }
    };

    let mut revoked_count = 0;

    for device_id in &req.device_ids {
        let dev_index_key = format!("iam:device_access_index:{}", device_id);

        // 1. Lấy tất cả access_key liên quan đến thiết bị này
        let access_keys: Vec<String> = match redis::cmd("SMEMBERS")
            .arg(&dev_index_key)
            .query_async(&mut conn)
            .await
        {
            Ok(keys) => keys,
            Err(e) => {
                Logger::sys_error(
                    "session.revoke_by_devices",
                    &format!("SMEMBERS failed for device {}", device_id),
                    &e.to_string(),
                );
                return Err(Status::internal(format!(
                    "SMEMBERS failed for device {}: {}",
                    device_id, e
                )));
            }
        };

        if access_keys.is_empty() {
            continue;
        }

        // 2. Chuyển đổi TTL từng session về 5s và xóa khỏi user_index
        let user_index_key = format!("iam:user_access_index:{}", req.user_id);
        let mut pipe = redis::pipe();
        pipe.atomic();

        for access_key in &access_keys {
            let session_key = format!("iam:user_access_session:{}:{}", req.user_id, access_key);
            pipe.cmd("EXPIRE").arg(&session_key).arg(5);
            pipe.cmd("SREM").arg(&user_index_key).arg(access_key);
            revoked_count += 1;
        }

        // 3. Xóa index của thiết bị này
        pipe.cmd("DEL").arg(&dev_index_key);

        if let Err(e) = pipe.query_async::<_, ()>(&mut conn).await {
            Logger::sys_error(
                "session.revoke_by_devices",
                &format!(
                    "Revoke device session pipeline failed for device {}",
                    device_id
                ),
                &e.to_string(),
            );
            return Err(Status::internal(format!(
                "Revoke device session pipeline failed for device {}: {}",
                device_id, e
            )));
        }
    }

    Logger::sys_info(
        "session.revoke_by_devices",
        &format!(
            "Successfully revoked {} sessions for user_id={}",
            revoked_count, req.user_id
        ),
    );

    Ok(Response::new(
        device_proto::RevokeUserSessionsByDevicesResponse { revoked_count },
    ))
}
