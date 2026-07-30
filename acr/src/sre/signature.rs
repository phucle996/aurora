// ======================================================================================================
// SRE critical-request Ed25519 verifier and replay boundary.
// ======================================================================================================

use crate::infra::redis::SessionManager;
use crate::observability::logger::Logger;
use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use ed25519_dalek::{Signature, VerifyingKey};
use std::collections::HashMap;
use std::sync::Arc;

/// Verifies one SRE critical request and returns an opaque proof identifier for
/// downstream audit. Raw signature, timestamp and nonce never cross the ACR
/// security boundary.
pub async fn verify_sre_signature(
    session_mgr: &Arc<SessionManager>,
    device_public_key_b64: &str,
    headers: &HashMap<String, String>,
    method: &str,
    path: &str,
    raw_body: &[u8],
) -> Result<String, String> {
    let sig_b64 = headers
        .get("x-admin-signature")
        .or_else(|| headers.get("X-Admin-Signature"))
        .ok_or_else(|| "Missing admin signature".to_string())?;
    let timestamp_str = headers
        .get("x-admin-timestamp")
        .or_else(|| headers.get("X-Admin-Timestamp"))
        .ok_or_else(|| "Missing admin timestamp".to_string())?;
    let nonce = headers
        .get("x-admin-nonce")
        .or_else(|| headers.get("X-Admin-Nonce"))
        .ok_or_else(|| "Missing admin nonce".to_string())?;

    let timestamp: i64 = timestamp_str
        .parse()
        .map_err(|_| "Invalid admin timestamp".to_string())?;
    let now = chrono::Utc::now().timestamp();
    if (now - timestamp).abs() > 300 {
        return Err("Admin request expired".to_string());
    }
    if uuid::Uuid::parse_str(nonce).is_err() {
        return Err("Invalid admin nonce".to_string());
    }

    use sha2::{Digest, Sha256};
    let mut body_hasher = Sha256::new();
    body_hasher.update(raw_body);
    let body_hash_hex = format!("{:x}", body_hasher.finalize());

    // [COMMENT]: This byte format is shared with Admin UI. Method, exact path,
    // empty query line, body digest, timestamp and nonce are all signed.
    let message = format!(
        "{}\n{}\n\n{}\n{}\n{}",
        method, path, body_hash_hex, timestamp_str, nonce
    );

    let pubkey_bytes = BASE64
        .decode(device_public_key_b64)
        .map_err(|_| "Invalid admin device key".to_string())?;
    if pubkey_bytes.len() != 32 {
        return Err("Invalid admin device key".to_string());
    }
    let verifying_key = VerifyingKey::from_bytes(
        pubkey_bytes[..32]
            .try_into()
            .map_err(|_| "Invalid admin device key".to_string())?,
    )
    .map_err(|_| "Invalid admin device key".to_string())?;

    let sig_bytes = BASE64
        .decode(sig_b64)
        .map_err(|_| "Invalid admin signature".to_string())?;
    if sig_bytes.len() != 64 {
        return Err("Invalid admin signature".to_string());
    }
    let signature = Signature::from_bytes(
        sig_bytes[..64]
            .try_into()
            .map_err(|_| "Invalid admin signature".to_string())?,
    );
    verifying_key
        .verify_strict(message.as_bytes(), &signature)
        .map_err(|error| {
            Logger::sys_warn(
                "sre.signature",
                "Ed25519 verification failed",
                &error.to_string(),
            );
            "Invalid admin signature".to_string()
        })?;

    // [COMMENT]: Claim replay state only after cryptographic verification. Two
    // concurrent valid requests with the same nonce race on SET NX; exactly one
    // reaches the Controlplane.
    let mut conn = session_mgr
        .get_connection()
        .await
        .map_err(|_| "Admin replay protection unavailable".to_string())?;
    let replay_key = format!("iam:sre:nonce:{}", nonce);
    let set_nx: bool = redis::cmd("SET")
        .arg(&replay_key)
        .arg("1")
        .arg("EX")
        .arg(300)
        .arg("NX")
        .query_async(&mut conn)
        .await
        .map_err(|_| "Admin replay protection unavailable".to_string())?;
    if !set_nx {
        return Err("Admin request already consumed".to_string());
    }

    Ok(uuid::Uuid::new_v4().to_string())
}
