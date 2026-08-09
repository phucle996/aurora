use crate::infra::redis::SessionManager;
use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use ed25519_dalek::{Signature, VerifyingKey};
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::collections::HashMap;

const LOGIN_CHALLENGE_TTL_SECONDS: u64 = 120;
const CRITICAL_CHALLENGE_TTL_SECONDS: u64 = 60;
const CRITICAL_CLOCK_SKEW_SECONDS: i64 = 60;

#[derive(Serialize)]
pub struct SessionProofChallenge {
    pub challenge_id: String,
    pub nonce: String,
    pub expires_in: u64,
}

fn random_nonce() -> String {
    // [COMMENT]: Hai UUIDv4 độc lập được hash thành đúng 32 byte nonce, không dùng timestamp/predictable state.
    let mut hasher = Sha256::new();
    hasher.update(uuid::Uuid::new_v4().as_bytes());
    hasher.update(uuid::Uuid::new_v4().as_bytes());
    BASE64.encode(hasher.finalize())
}

async fn store_challenge(
    session_mgr: &SessionManager,
    key: &str,
    ttl_seconds: u64,
) -> Result<SessionProofChallenge, String> {
    let challenge = SessionProofChallenge {
        challenge_id: uuid::Uuid::now_v7().to_string(),
        nonce: random_nonce(),
        expires_in: ttl_seconds,
    };
    let redis_key = format!("{}:{}", key, challenge.challenge_id);
    let mut conn = session_mgr
        .get_connection()
        .await
        .map_err(|error| format!("session proof Redis unavailable: {error}"))?;
    redis::cmd("SET")
        .arg(redis_key)
        .arg(&challenge.nonce)
        .arg("EX")
        .arg(ttl_seconds)
        .query_async::<_, ()>(&mut conn)
        .await
        .map_err(|error| format!("store session proof challenge failed: {error}"))?;
    Ok(challenge)
}

pub async fn issue_login_challenge(
    session_mgr: &SessionManager,
) -> Result<SessionProofChallenge, String> {
    store_challenge(
        session_mgr,
        "iam:session_proof:login",
        LOGIN_CHALLENGE_TTL_SECONDS,
    )
    .await
}

pub async fn issue_critical_challenge(
    session_mgr: &SessionManager,
    access_key: &str,
) -> Result<SessionProofChallenge, String> {
    store_challenge(
        session_mgr,
        &format!("iam:session_proof:critical:{access_key}"),
        CRITICAL_CHALLENGE_TTL_SECONDS,
    )
    .await
}

fn verifying_key(public_key_b64: &str) -> Result<VerifyingKey, String> {
    let bytes = BASE64
        .decode(public_key_b64)
        .map_err(|_| "invalid session proof public key encoding".to_string())?;
    let bytes: [u8; 32] = bytes
        .try_into()
        .map_err(|_| "session proof public key must be 32 bytes".to_string())?;
    VerifyingKey::from_bytes(&bytes).map_err(|_| "invalid Ed25519 public key".to_string())
}

pub fn canonicalize_public_key(public_key_b64: &str) -> Result<String, String> {
    let bytes = BASE64
        .decode(public_key_b64.trim())
        .map_err(|_| "invalid session proof public key encoding".to_string())?;
    if bytes.len() != 32 {
        return Err("session proof public key must be 32 bytes".to_string());
    }
    VerifyingKey::from_bytes(
        &bytes
            .clone()
            .try_into()
            .map_err(|_| "session proof public key must be 32 bytes".to_string())?,
    )
    .map_err(|_| "invalid Ed25519 public key".to_string())?;
    Ok(BASE64.encode(bytes))
}

fn verify(public_key_b64: &str, message: &str, signature_b64: &str) -> Result<(), String> {
    let signature = BASE64
        .decode(signature_b64)
        .map_err(|_| "invalid session proof signature encoding".to_string())?;
    let signature: [u8; 64] = signature
        .try_into()
        .map_err(|_| "session proof signature must be 64 bytes".to_string())?;
    verifying_key(public_key_b64)?
        .verify_strict(message.as_bytes(), &Signature::from_bytes(&signature))
        .map_err(|_| "session proof signature mismatch".to_string())
}

fn login_message(
    challenge_id: &str,
    nonce: &str,
    username: &str,
    tenant_domain: &str,
    zone_code: &str,
    remember_me: bool,
    timestamp: i64,
) -> String {
    format!(
        "aurora.login-proof.v1\n{challenge_id}\n{nonce}\n{username}\n{tenant_domain}\n{zone_code}\n{remember_me}\n{timestamp}"
    )
}

fn critical_message(
    challenge_id: &str,
    nonce: &str,
    method: &str,
    path: &str,
    body_hash: &str,
    timestamp: i64,
) -> String {
    format!(
        "aurora.session-proof.v1\n{challenge_id}\n{nonce}\n{}\n{path}\n{body_hash}\n{timestamp}",
        method.to_uppercase()
    )
}

async fn load_challenge(session_mgr: &SessionManager, redis_key: &str) -> Result<String, String> {
    let mut conn = session_mgr
        .get_connection()
        .await
        .map_err(|error| format!("session proof Redis unavailable: {error}"))?;
    redis::cmd("GET")
        .arg(redis_key)
        .query_async::<_, Option<String>>(&mut conn)
        .await
        .map_err(|error| format!("load session proof challenge failed: {error}"))?
        .ok_or_else(|| "session proof challenge missing or expired".to_string())
}

async fn consume_challenge(
    session_mgr: &SessionManager,
    redis_key: &str,
    expected_nonce: &str,
) -> Result<(), String> {
    const COMPARE_DELETE: &str = "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end";
    let mut conn = session_mgr
        .get_connection()
        .await
        .map_err(|error| format!("session proof Redis unavailable: {error}"))?;
    let consumed: i32 = redis::cmd("EVAL")
        .arg(COMPARE_DELETE)
        .arg(1)
        .arg(redis_key)
        .arg(expected_nonce)
        .query_async(&mut conn)
        .await
        .map_err(|error| format!("consume session proof challenge failed: {error}"))?;
    if consumed == 1 {
        Ok(())
    } else {
        Err("session proof replay detected".to_string())
    }
}

// Login proof canonicalization keeps each signed input explicit.
#[allow(clippy::too_many_arguments)]
pub async fn verify_login_proof(
    session_mgr: &SessionManager,
    challenge_id: &str,
    timestamp: i64,
    username: &str,
    tenant_domain: &str,
    zone_code: &str,
    remember_me: bool,
    public_key_b64: &str,
    signature_b64: &str,
) -> Result<(), String> {
    if challenge_id.is_empty() || signature_b64.is_empty() {
        return Err("login session proof is required".to_string());
    }
    if (chrono::Utc::now().timestamp() - timestamp).abs() > LOGIN_CHALLENGE_TTL_SECONDS as i64 {
        return Err("login session proof timestamp expired".to_string());
    }
    let redis_key = format!("iam:session_proof:login:{challenge_id}");
    let nonce = load_challenge(session_mgr, &redis_key).await?;
    let message = login_message(
        challenge_id,
        &nonce,
        username,
        tenant_domain,
        zone_code,
        remember_me,
        timestamp,
    );
    verify(public_key_b64, &message, signature_b64)?;
    consume_challenge(session_mgr, &redis_key, &nonce).await
}

fn header<'a>(headers: &'a HashMap<String, String>, name: &str) -> Option<&'a str> {
    headers
        .get(name)
        .or_else(|| {
            headers
                .iter()
                .find(|(key, _)| key.eq_ignore_ascii_case(name))
                .map(|(_, value)| value)
        })
        .map(String::as_str)
}

pub async fn verify_critical_proof(
    session_mgr: &SessionManager,
    access_key: &str,
    public_key_b64: &str,
    headers: &HashMap<String, String>,
    method: &str,
    path: &str,
    raw_body: &[u8],
) -> Result<String, String> {
    if public_key_b64.is_empty() {
        return Err("current session has no session proof key".to_string());
    }
    let challenge_id = header(headers, "x-session-proof-challenge-id")
        .ok_or_else(|| "missing session proof challenge id".to_string())?;
    let timestamp_raw = header(headers, "x-session-proof-timestamp")
        .ok_or_else(|| "missing session proof timestamp".to_string())?;
    let signature = header(headers, "x-session-proof-signature")
        .ok_or_else(|| "missing session proof signature".to_string())?;
    let timestamp = timestamp_raw
        .parse::<i64>()
        .map_err(|_| "invalid session proof timestamp".to_string())?;
    if (chrono::Utc::now().timestamp() - timestamp).abs() > CRITICAL_CLOCK_SKEW_SECONDS {
        return Err("session proof timestamp expired".to_string());
    }

    let redis_key = format!("iam:session_proof:critical:{access_key}:{challenge_id}");
    let nonce = load_challenge(session_mgr, &redis_key).await?;
    let body_hash = format!("{:x}", Sha256::digest(raw_body));
    let message = critical_message(challenge_id, &nonce, method, path, &body_hash, timestamp);
    verify(public_key_b64, &message, signature)?;
    consume_challenge(session_mgr, &redis_key, &nonce).await?;
    Ok(challenge_id.to_string())
}

#[cfg(test)]
mod tests {
    use super::{critical_message, login_message, verifying_key};

    #[test]
    fn rejects_empty_public_key() {
        assert!(verifying_key("").is_err());
    }

    #[test]
    fn canonical_messages_have_stable_field_order() {
        assert_eq!(
            login_message("c", "n", "alice", "acme", "vn", true, 7),
            "aurora.login-proof.v1\nc\nn\nalice\nacme\nvn\ntrue\n7"
        );
        assert_eq!(
            critical_message("c", "n", "post", "/api/v1/critical/x", "abc", 7),
            "aurora.session-proof.v1\nc\nn\nPOST\n/api/v1/critical/x\nabc\n7"
        );
    }
}
