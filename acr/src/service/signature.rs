// ======================================================================================================
// 📂 MODULE: acl/src/service/signature.rs
//            Bộ xác thực chữ ký Ed25519 tại biên (Edge Signature Verification) cho SRE Critical API
// ======================================================================================================

use std::sync::Arc;
use tonic::Status;
use envoy_types::pb::envoy::service::auth::v3::CheckRequest;
use crate::core::session::SessionManager;
use crate::observability::logger::Logger;

// [COMMENT]: Hàm chính xử lý xác thực chữ ký Ed25519, kiểm tra Clock Skew và replay prevention
pub async fn verify_admin_signature(
    session_mgr: &Arc<SessionManager>,
    client_headers: &std::collections::HashMap<String, String>,
    req: &CheckRequest,
    method: &str,
    path: &str,
    device_public_key: &str,
    access_key: &str,
) -> Result<(), Status> {
    // Trích xuất các header cần thiết cho xác minh chữ ký từ Envoy client headers
    let sig = client_headers
        .get("x-admin-signature")
        .map(|s| s.as_str().trim())
        .unwrap_or("");
    let ts_raw = client_headers
        .get("x-admin-timestamp")
        .map(|s| s.as_str().trim())
        .unwrap_or("");
    let nonce = client_headers
        .get("x-admin-nonce")
        .map(|s| s.as_str().trim())
        .unwrap_or("");

    // Trả về lỗi Unauthenticated nếu thiếu bất kỳ header bảo mật bắt buộc nào
    if sig.is_empty() || ts_raw.is_empty() || nonce.is_empty() {
        Logger::sys_warn(
            "ext_authz.signature",
            "Missing critical signature headers for SRE request",
            path,
        );
        return Err(Status::unauthenticated("Missing critical signature headers"));
    }

    // [COMMENT]: Chống tràn số (overflow/underflow) khi tính toán Clock Skew từ timestamp người dùng gửi lên
    let ts_unix = ts_raw.parse::<i64>().map_err(|_| {
        Status::unauthenticated("Invalid signature timestamp format")
    })?;
    
    let now_unix = chrono::Utc::now().timestamp();
    
    // Sử dụng checked_sub và checked_abs để đảm bảo không bị crash hệ thống khi kẻ tấn công gửi timestamp độc hại
    let diff = ts_unix
        .checked_sub(now_unix)
        .and_then(|d| d.checked_abs())
        .unwrap_or(i64::MAX);

    // Chặn bắt các request quá cũ hoặc quá mới vượt quá skew window (120 giây)
    if diff > 120 {
        Logger::sys_warn(
            "ext_authz.signature",
            &format!("Timestamp skew too large: {}s", diff),
            path,
        );
        return Err(Status::unauthenticated(
            "Timestamp skew too large (exceeds 120 seconds)",
        ));
    }

    // [COMMENT]: Kiểm tra và khóa Nonce bằng Redis SETNX (TTL 2 phút) chống Replay Attack tại Edge
    let mut conn = session_mgr.get_connection().await.map_err(|e| {
        Logger::sys_error(
            "ext_authz.signature",
            "Failed to connect to Redis for Nonce check",
            &e.to_string(),
        );
        Status::internal("Authentication service temporarily unavailable")
    })?;

    let nonce_key = format!("iam:nonce:{}", nonce);
    
    // Sử dụng lệnh SET key value EX 120 NX để ghi khóa nonce một cách nguyên tử (atomic)
    let success: bool = redis::cmd("SET")
        .arg(&nonce_key)
        .arg("1")
        .arg("EX")
        .arg(120)
        .arg("NX")
        .query_async(&mut conn)
        .await
        .map_err(|e| {
            Logger::sys_error(
                "ext_authz.signature",
                "Redis connection error during nonce verification",
                &e.to_string(),
            );
            Status::internal("Authentication service temporarily unavailable")
        })?;

    // Nếu khóa đã tồn tại (SET thất bại), đây là một cuộc tấn công Replay Attack -> Từ chối ngay lập tức
    if !success {
        Logger::sys_warn(
            "ext_authz.signature",
            "Replay attack detected: nonce already used",
            nonce,
        );
        return Err(Status::unauthenticated(
            "Replay attack detected: nonce already used",
        ));
    }

    // [COMMENT]: Lấy Raw Body của request để tính SHA-256 hash làm canonical payload
    let raw_body = req
        .attributes
        .as_ref()
        .and_then(|a| a.request.as_ref())
        .and_then(|r| r.http.as_ref())
        .map(|h| &h.body)
        .cloned()
        .unwrap_or_default();

    use sha2::{Digest, Sha256};
    let mut hasher = Sha256::new();
    hasher.update(raw_body.as_bytes());
    let body_hash_hex = format!("{:x}", hasher.finalize());

    // [COMMENT]: Phân tách path và query string độc lập để xây dựng canonical request
    let mut parts = path.splitn(2, '?');
    let clean_path = parts.next().unwrap_or("");
    let raw_query = parts.next().unwrap_or("");

    // Xây dựng canonical payload theo định dạng đã thống nhất với Admin UI/CLI:
    // METHOD\nPATH\nQUERY\nSHA256(BODY)\nTS\nNONCE
    let canonical_payload = format!(
        "{}\n{}\n{}\n{}\n{}\n{}",
        method.to_uppercase(),
        clean_path,
        raw_query,
        body_hash_hex,
        ts_raw,
        nonce
    );

    // [COMMENT]: Kiểm tra xem thiết bị hiện tại đã lưu public key trong Redis Session chưa
    if device_public_key.is_empty() {
        Logger::sys_warn(
            "ext_authz.signature",
            "Device public key not found in admin session",
            access_key,
        );
        return Err(Status::unauthenticated(
            "Device registration required (public key missing)",
        ));
    }

    // [COMMENT]: Thực hiện verify chữ ký Ed25519
    if !verify_ed25519_signature(device_public_key, &canonical_payload, sig) {
        Logger::sys_warn(
            "ext_authz.signature",
            "Invalid Ed25519 signature for SRE critical request",
            path,
        );
        return Err(Status::unauthenticated("Invalid request signature"));
    }

    Ok(())
}

// [COMMENT]: Helper xác thực chữ ký Ed25519 bằng base64-encoded public key và base64-encoded signature
pub fn verify_ed25519_signature(
    pubkey_b64: &str,
    payload: &str,
    signature_b64: &str,
) -> bool {
    use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
    use ed25519_dalek::{Signature, Verifier, VerifyingKey};

    // Decode public key từ Base64
    let pubkey_bytes = match BASE64.decode(pubkey_b64) {
        Ok(b) => b,
        Err(_) => return false,
    };
    if pubkey_bytes.len() != 32 {
        return false;
    }
    let pubkey_arr: [u8; 32] = match pubkey_bytes.try_into() {
        Ok(arr) => arr,
        Err(_) => return false,
    };
    let verifying_key = match VerifyingKey::from_bytes(&pubkey_arr) {
        Ok(key) => key,
        Err(_) => return false,
    };

    // Decode signature từ Base64
    let sig_bytes = match BASE64.decode(signature_b64) {
        Ok(b) => b,
        Err(_) => return false,
    };
    if sig_bytes.len() != 64 {
        return false;
    }
    let sig_arr: [u8; 64] = match sig_bytes.try_into() {
        Ok(arr) => arr,
        Err(_) => return false,
    };
    let signature = Signature::from_bytes(&sig_arr);

    // Xác thực chữ ký số trên payload
    verifying_key.verify(payload.as_bytes(), &signature).is_ok()
}
