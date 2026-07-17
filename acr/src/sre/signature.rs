// ======================================================================================================
// 📂 sre/signature.rs — Ed25519 Signature Verifier & Replay Prevention cho SRE Critical APIs
// ======================================================================================================

use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use ed25519_dalek::{Signature, VerifyingKey};
use std::collections::HashMap;
use std::sync::Arc;

/// [COMMENT]: Phân tích và xác thực chữ ký Ed25519 cho SRE Critical APIs.
/// Headers yêu cầu: x-sre-signature, x-sre-timestamp, x-sre-nonce.
pub async fn verify_sre_signature(
    session_mgr: &Arc<SessionManager>,
    device_public_key_b64: &str,
    headers: &HashMap<String, String>,
    method: &str,
    path: &str,
    raw_body: &[u8],
) -> Result<(), String> {
    let sig_b64 = headers
        .get("x-sre-signature")
        .or_else(|| headers.get("X-Sre-Signature"))
        .ok_or_else(|| "Missing x-sre-signature header".to_string())?;

    let timestamp_str = headers
        .get("x-sre-timestamp")
        .or_else(|| headers.get("X-Sre-Timestamp"))
        .ok_or_else(|| "Missing x-sre-timestamp header".to_string())?;

    let nonce = headers
        .get("x-sre-nonce")
        .or_else(|| headers.get("X-Sre-Nonce"))
        .ok_or_else(|| "Missing x-sre-nonce header".to_string())?;

    // 1. Clock Skew Check (chênh lệch tối đa 5 phút)
    let timestamp: i64 = timestamp_str
        .parse()
        .map_err(|_| "Invalid timestamp format".to_string())?;
    let now = chrono::Utc::now().timestamp();
    if (now - timestamp).abs() > 300 {
        return Err("Clock skew exceeded (request expired or timestamp in future)".to_string());
    }

    // 2. Anti-Replay Check via Redis SETNX
    let mut conn = session_mgr
        .get_connection()
        .await
        .map_err(|e| format!("Redis connection error for replay check: {}", e))?;

    let replay_key = format!("iam:sre:nonce:{}", nonce);
    let set_nx: bool = redis::cmd("SET")
        .arg(&replay_key)
        .arg("1")
        .arg("EX")
        .arg(300)
        .arg("NX")
        .query_async(&mut conn)
        .await
        .map_err(|e| format!("Replay check SETNX failed: {}", e))?;

    if !set_nx {
        return Err("Replay attack detected (nonce already processed)".to_string());
    }

    // 3. Construct Payload: method|path|timestamp|nonce|body_hash
    use sha2::{Digest, Sha256};
    let mut body_hasher = Sha256::new();
    body_hasher.update(raw_body);
    let body_hash_hex = format!("{:x}", body_hasher.finalize());

    let message = format!("{}|{}|{}|{}|{}", method, path, timestamp_str, nonce, body_hash_hex);

    // 4. Ed25519 Signature Verification
    let pubkey_bytes = BASE64
        .decode(device_public_key_b64)
        .map_err(|e| format!("Invalid Base64 device_public_key: {}", e))?;

    if pubkey_bytes.len() != 32 {
        return Err("device_public_key must be 32 bytes".to_string());
    }

    let verifying_key = VerifyingKey::from_bytes(
        pubkey_bytes[..32]
            .try_into()
            .map_err(|_| "Failed to parse public key bytes".to_string())?,
    )
    .map_err(|e| format!("Invalid Ed25519 public key: {}", e))?;

    let sig_bytes = BASE64
        .decode(sig_b64)
        .map_err(|e| format!("Invalid Base64 signature: {}", e))?;

    if sig_bytes.len() != 64 {
        return Err("Signature must be 64 bytes".to_string());
    }

    let signature = Signature::from_bytes(
        sig_bytes[..64]
            .try_into()
            .map_err(|_| "Failed to parse signature bytes".to_string())?,
    );

    verifying_key
        .verify_strict(message.as_bytes(), &signature)
        .map_err(|e| {
            Logger::sys_warn("sre.signature", &format!("Ed25519 verification failed: {}", e), "");
            "Ed25519 signature verification failed".to_string()
        })?;

    Ok(())
}
