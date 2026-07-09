use crate::core::session::SessionManager;
use crate::observability::logger::Logger;
use crate::service::session::release_session::device_proto::{
    RevokeUserSessionsByDevicesRequest, RevokeUserSessionsByDevicesResponse,
};
use prost::Message;
use std::sync::Arc;
use tonic::{Request, Response, Status};

/// [COMMENT]: Xử lý gRPC request thu hồi session của các thiết bị cho một user từ A - Z trong một hàm duy nhất.
/// Nhận diện yêu cầu, thực thi truy vấn Redis L2 (SMEMBERS & pipeline EXPIRE/SREM) và trả kết quả/lỗi gRPC Status.
pub async fn revoke_user_sessions_by_devices(
    session_mgr: &Arc<SessionManager>,
    request: Request<RevokeUserSessionsByDevicesRequest>,
) -> Result<Response<RevokeUserSessionsByDevicesResponse>, Status> {
    let req = request.into_inner();
    Logger::sys_info(
        "session.revoke_by_devices",
        &format!(
            "Received request to revoke sessions for user_id={} and device_ids={:?}",
            req.user_id, req.device_ids
        ),
    );

    // Lấy kết nối Redis L2 trực tiếp từ session_mgr
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
            // [COMMENT]: Khôi phục lại định dạng key phân cấp để xóa chính xác
            // iam:user_session:{zone_id}:{tenant_id}:{user_id}:{access_key}
            // Vì access_key chứa full key (theo thiết kế register_session), ta expire trực tiếp key đó.
            pipe.cmd("EXPIRE").arg(access_key).arg(5);
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
        RevokeUserSessionsByDevicesResponse { revoked_count },
    ))
}

/// [COMMENT]: Xử lý yêu cầu thu hồi session thiết bị truyền vào dưới dạng bytes từ NATS,
/// thực thi nghiệp vụ và trả về kết quả mã hóa nhị phân.
pub async fn revoke_user_sessions_by_devices_bytes(
    session_mgr: &Arc<SessionManager>,
    payload: &[u8],
) -> Vec<u8> {
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

    let res = match revoke_user_sessions_by_devices(session_mgr, Request::new(req)).await {
        Ok(resp) => resp.into_inner(),
        Err(_) => RevokeUserSessionsByDevicesResponse { revoked_count: 0 },
    };

    let mut reply_payload = Vec::new();
    if res.encode(&mut reply_payload).is_ok() {
        reply_payload
    } else {
        Logger::sys_error(
            "device.revoke",
            "Failed to encode RevokeUserSessionsByDevicesResponse",
            "",
        );
        vec![]
    }
}
