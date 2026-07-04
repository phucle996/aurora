// ======================================================================================================
// 📂 MODULE: acl/src/service/session/release_session.rs
//            Triển khai nghiệp vụ chi tiết khởi tạo phiên làm việc (Release Session)
// ======================================================================================================

use crate::core::session::SessionManager;
use crate::core::token::{Claims, TokenManager};
use crate::observability::logger::Logger;
use std::sync::Arc;
use tonic::Status;
use uuid::Uuid;

// [COMMENT]: Import protobuf sinh ra từ proto/device.proto (đổi tên từ session.proto).
// Module này chỉ còn chứa DeviceService (RevokeUserSessionsByDevices).
#[allow(dead_code)]
#[allow(unused_imports)]
pub mod device_proto {
    tonic::include_proto!("iam.rpc");
}

// [COMMENT]: Cấu trúc chứa kết quả khởi tạo trinity session cho người dùng thường
pub struct ReleaseUserSessionResult {
    pub access_token: String,
    pub access_key: String,
    pub access_secret: String,
    pub client_device_id: String,
    pub tenant_id_val: String,
}

// [COMMENT]: Cấu trúc chứa kết quả khởi tạo trinity session cho quản trị viên SRE admin
pub struct ReleaseAdminSessionResult {
    pub access_token: String,
    pub access_key: String,
    pub access_secret: String,
}

/// [COMMENT]: Nghiệp vụ chi tiết khởi tạo Trinity Session cho User (dùng chung cho cả HTTP Login và gRPC Release)
pub async fn release_user_session(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    zone_mgr: &Arc<crate::core::zone::ZoneManager>,
    config: &crate::config::Config,
    user_id: &str,
    username: &str,
    role: &str,
    level: i32,
    tenant_id: &str,
    zone_id: &str,
    device_id: &str,
    client_device_id: &str,
    _trust_device: bool,
    _refresh_token: &str,
) -> Result<ReleaseUserSessionResult, Status> {
    Logger::sys_info(
        "session.release_user",
        &format!("Releasing new user trinity session for user_id={}", user_id),
    );

    // 1. Sinh Access Key (UUIDv7) và Access Secret (UUIDv4) để làm Trinity credentials
    let access_key = Uuid::now_v7().to_string();
    let access_secret = Uuid::new_v4().to_string();

    // 2. Tính hash của access_secret (SHA-256) lưu vào L2 Cache
    let ash = sha256_hash(&access_secret);

    // 3. Chuẩn bị Claims cho JWT Access Token
    let now_unix = chrono::Utc::now().timestamp();
    let exp_unix = now_unix + config.session_ttl_secs as i64;

    let claims = Claims {
        sub: username.to_string(),
        uid: user_id.to_string(),
        role_id: role.to_string(),
        lvl: level,
        tenant_id: if tenant_id.is_empty() {
            None
        } else {
            Some(tenant_id.to_string())
        },
        // [COMMENT]: Phân giải zone_id: Nếu là UUID hợp lệ thì giữ nguyên.
        // Nếu là zone code (ví dụ "vn", "sg"), gọi ZoneManager L1/CP để phân giải sang UUID.
        zone_id: if zone_id.is_empty() {
            None
        } else if Uuid::parse_str(zone_id).is_ok() {
            Some(zone_id.to_string())
        } else {
            zone_mgr.resolve_code_to_id(zone_id).await
        },
        access_key: access_key.clone(),
        jti: Uuid::new_v4().to_string(),
        iss: Some("aurora-acr".to_string()),
        exp: exp_unix,
        iat: now_unix,
    };

    // 4. Ký JWT thông qua Vault Transit Engine (Stateless Verification)
    let access_token = match token_mgr.generate_token(&claims).await {
        Ok(token) => token,
        Err(e) => {
            Logger::sys_error(
                "session.release_user",
                "Failed to sign access token via Vault",
                &e.to_string(),
            );
            return Err(Status::internal(format!(
                "Failed to sign access token: {}",
                e
            )));
        }
    };

    // 5. Ghi session lên Redis L2 qua SessionManager (Stateful Verification)
    if let Err(e) = session_mgr
        .register_session(
            claims.zone_id.as_deref().unwrap_or("global"),
            claims.tenant_id.as_deref().unwrap_or("global"),
            user_id,
            &access_key,
            &ash,
            device_id,
        )
        .await
    {
        Logger::sys_error(
            "session.release_user",
            "Failed to register session in Redis L2",
            &e.to_string(),
        );
        return Err(Status::internal(format!(
            "Failed to write session state: {}",
            e
        )));
    }

    // 6. Giải quyết client_device_id
    let resolved_client_device_id = if client_device_id.trim().is_empty() {
        Uuid::new_v4().to_string()
    } else {
        client_device_id.to_string()
    };

    let tenant_id_val = claims
        .tenant_id
        .clone()
        .unwrap_or_else(|| "global".to_string());

    Ok(ReleaseUserSessionResult {
        access_token,
        access_key,
        access_secret,
        client_device_id: resolved_client_device_id,
        tenant_id_val,
    })
}

/// [COMMENT]: Nghiệp vụ chi tiết khởi tạo SRE Admin Session (dùng cho HTTP Login SRE)
pub async fn release_admin_session(
    session_mgr: &Arc<SessionManager>,
    token_mgr: &Arc<TokenManager>,
    config: &crate::config::Config,
    device_public_key: &str,
) -> Result<ReleaseAdminSessionResult, Status> {
    Logger::sys_info("session.release_admin", "Releasing SRE admin session");

    // 1. Sinh Access Key (UUIDv4) và Access Secret (UUIDv4) để làm Trinity credentials
    let access_key = Uuid::new_v4().to_string();
    let access_secret = Uuid::new_v4().to_string();

    // 2. Tính hash của access_secret (SHA-256) lưu vào L2 Cache
    let ash = sha256_hash(&access_secret);

    // 3. Chuẩn bị Claims cho SRE: Không có role hay lvl phân quyền cụ thể
    // Đăng nhập trực tiếp vào virtual zone "global"
    let now_unix = chrono::Utc::now().timestamp();
    let exp_unix = now_unix + config.session_ttl_secs as i64;
    let claims = Claims {
        sub: "sre".to_string(),
        uid: "sre".to_string(),
        role_id: "".to_string(),
        lvl: 0,
        tenant_id: None,
        zone_id: Some("global".to_string()),
        access_key: access_key.clone(),
        jti: Uuid::new_v4().to_string(),
        iss: Some("aurora-acr".to_string()),
        exp: exp_unix,
        iat: now_unix,
    };

    // 4. Ký JWT qua Vault Transit Engine
    let access_token = match token_mgr.generate_token(&claims).await {
        Ok(t) => t,
        Err(e) => {
            Logger::sys_error(
                "session.release_admin",
                "Vault JWT signing failed for SRE",
                &e.to_string(),
            );
            return Err(Status::internal("Failed to issue session token"));
        }
    };

    // 5. Thực hiện giải mã thử và kiểm tra độ dài public key để phát hiện lỗi sớm trước khi ghi nhận login thành công
    if !device_public_key.is_empty() {
        use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
        let is_valid = match BASE64.decode(device_public_key) {
            Ok(bytes) => bytes.len() == 32,
            Err(_) => false,
        };
        if !is_valid {
            Logger::sys_warn(
                "session.release_admin",
                "SRE Admin login failed: Invalid device_public_key format or length",
                "",
            );
            return Err(Status::invalid_argument(
                "Invalid device_public_key format or length (must be a valid 32-byte Base64-encoded key)",
            ));
        }
    }

    // 6. Đăng ký Session Admin mới cho SRE vào Redis L2
    if let Err(e) = session_mgr
        .register_admin_session(&access_key, &ash, device_public_key)
        .await
    {
        Logger::sys_error(
            "session.release_admin",
            "Redis admin session registration failed",
            &e.to_string(),
        );
        return Err(Status::internal("Failed to save session state"));
    }

    Ok(ReleaseAdminSessionResult {
        access_token,
        access_key,
        access_secret,
    })
}

// [COMMENT]: Helper: Băm SHA-256 mã hóa access_secret
fn sha256_hash(secret: &str) -> String {
    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    format!("{:x}", hasher.finalize())
}
