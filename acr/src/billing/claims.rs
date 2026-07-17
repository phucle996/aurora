// ======================================================================================================
// 📂 billing/claims.rs — BillingClaims JWT struct & TokenManager cho Billing Auditor
//
// 📌 VAI TRÒ:
//   - Định nghĩa BillingClaims cô lập hoàn toàn, không có tenant_id hay device info.
//   - Cung cấp TokenManager dùng chung (Vault Transit) cho toàn hệ thống.
//   - Phát sinh và xác thực JWT Billing thông qua Vault HMAC-SHA256.
// ======================================================================================================

use crate::error::AcrError;
use crate::infra::vault::VaultClient;
use serde::{Deserialize, Serialize};
use std::sync::Arc;

/// [COMMENT]: BillingClaims — JWT payload cô lập cho Billing Auditor.
/// Không có tenant_id, không có client_device_id.
/// Issuer phân biệt: "aurora-billing-acr"
#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct BillingClaims {
    // Mã nhân viên kiểm toán (employee_code)
    #[serde(rename = "sub")]
    pub sub: String,

    // UUID nhân viên kiểm toán
    #[serde(rename = "uid")]
    pub uid: String,

    // Role ID (billing_admin / billing_auditor)
    #[serde(rename = "role_id", default)]
    pub role_id: String,

    // Level kiểm toán
    #[serde(rename = "lvl", default)]
    pub lvl: i32,

    // Zone ID (Optional — không bắt buộc)
    #[serde(rename = "zid", default)]
    pub zone_id: Option<String>,

    // Access Key binding JWT to Redis billing session
    #[serde(rename = "access_key")]
    pub access_key: String,

    // JWT ID (unique token identifier)
    #[serde(rename = "jti", default)]
    pub jti: String,

    // Issuer — luôn là "aurora-billing-acr"
    #[serde(rename = "iss", default)]
    pub iss: Option<String>,

    // Expiration timestamp (Unix epoch)
    #[serde(rename = "exp")]
    pub exp: i64,

    // Issued At
    #[serde(rename = "iat", default)]
    pub iat: i64,
}

// [COMMENT]: Giới hạn tối đa bản ghi trong L1 JWT Signature Cache.
// Mỗi entry ~128 bytes → 50,000 entries ≈ 6.4 MB RAM.
const JWT_SIG_CACHE_MAX_CAPACITY: u64 = 50_000;

/// [COMMENT]: TokenManager — ký và xác thực JWT qua Vault Transit Engine.
/// Dùng chung cho cả User, SRE Admin, và Billing Auditor.
/// L1 Cache (moka) cho JWT Signature Verification — chỉ cache token hợp lệ.
pub struct TokenManager {
    pub(crate) vault_client: Arc<VaultClient>,
    // [COMMENT]: Đường dẫn động đến Admin API Key trong Vault
    pub(crate) admin_api_key_path: String,
    // [COMMENT]: L1 Cache cho JWT Signature Verification (moka concurrent cache).
    // Key: SHA-256 hex của toàn bộ JWT string.
    // Value: Claims đã được xác thực hợp lệ bởi Vault.
    jwt_sig_cache: moka::future::Cache<String, crate::user::claims::Claims>,
}

impl TokenManager {
    pub fn new(vault_client: Arc<VaultClient>, admin_api_key_path: String) -> Self {
        // [COMMENT]: Khởi tạo moka cache với max capacity và auto-eviction
        let jwt_sig_cache = moka::future::Cache::builder()
            .max_capacity(JWT_SIG_CACHE_MAX_CAPACITY)
            .build();

        Self {
            vault_client,
            admin_api_key_path,
            jwt_sig_cache,
        }
    }

    /// [COMMENT]: Giải mã và xác thực tính hợp lệ của JWT User/SRE Token.
    /// Sử dụng L1 Cache (moka) để tránh gọi Vault Transit trên mỗi request.
    pub async fn verify_token(&self, token: &str) -> Result<crate::user::claims::Claims, AcrError> {
        let token = token.trim();
        if token.is_empty() {
            return Err(AcrError::TokenError("Empty token".to_string()));
        }

        // [COMMENT]: 1. Tính SHA-256 của toàn bộ JWT string làm cache key
        use sha2::{Digest, Sha256};
        let cache_key = {
            let mut hasher = Sha256::new();
            hasher.update(token.as_bytes());
            format!("{:x}", hasher.finalize())
        };

        // [COMMENT]: 2. Kiểm tra L1 Cache trước — nếu Hit thì bỏ qua Vault hoàn toàn
        if let Some(cached_claims) = self.jwt_sig_cache.get(&cache_key).await {
            let now = chrono::Utc::now().timestamp();
            if now <= cached_claims.exp {
                return Ok(cached_claims);
            }
            // [COMMENT]: Token đã hết hạn trong khi còn trong cache → loại bỏ
            self.jwt_sig_cache.invalidate(&cache_key).await;
        }

        // [COMMENT]: 3. Cache Miss — thực hiện full verification qua Vault
        let parts: Vec<&str> = token.split('.').collect();
        if parts.len() != 3 {
            return Err(AcrError::TokenError("Malformed JWT structure".to_string()));
        }

        use base64::Engine;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;

        let payload_bytes = url_engine
            .decode(parts[1])
            .map_err(|e| AcrError::TokenError(format!("Failed to decode JWT payload: {}", e)))?;

        let claims: crate::user::claims::Claims = serde_json::from_slice(&payload_bytes)
            .map_err(|e| AcrError::TokenError(format!("Failed to parse JWT claims: {}", e)))?;

        let now = chrono::Utc::now().timestamp();
        if now > claims.exp {
            return Err(AcrError::TokenError("Token has expired".to_string()));
        }

        // [COMMENT]: 3d. Kiểm tra chữ ký JWT qua Vault Transit Engine
        let sig_part = parts[2];
        if sig_part.starts_with('v') {
            if let Some(idx) = sig_part.find('_') {
                let vault_version = &sig_part[..idx];
                let signature_b64url = &sig_part[idx + 1..];
                let signing_input = format!("{}.{}", parts[0], parts[1]);

                let is_valid = self
                    .vault_client
                    .verify_hmac(&signing_input, vault_version, signature_b64url)
                    .await?;
                if !is_valid {
                    return Err(AcrError::TokenError(
                        "Invalid signature verified by Vault".to_string(),
                    ));
                }

                // [COMMENT]: 4. Chữ ký hợp lệ → Lưu vào L1 Cache
                let remaining_secs = (claims.exp - now).max(0) as u64;
                self.jwt_sig_cache.insert(cache_key, claims.clone()).await;
                let _ = remaining_secs;

                return Ok(claims);
            }
        }

        Err(AcrError::TokenError(
            "Token lacks Vault signature prefix or format is invalid".to_string(),
        ))
    }

    /// [COMMENT]: Tạo JWT User/SRE Token, ký qua Vault Transit Engine.
    pub async fn generate_token(
        &self,
        claims: &crate::user::claims::Claims,
    ) -> Result<String, AcrError> {
        use base64::Engine;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;

        #[derive(Serialize)]
        struct Header {
            alg: &'static str,
            typ: &'static str,
        }
        let header = Header {
            alg: "HS256",
            typ: "JWT",
        };

        let header_json = serde_json::to_string(&header)
            .map_err(|e| AcrError::TokenError(format!("Failed to serialize header: {}", e)))?;
        let payload_json = serde_json::to_string(claims)
            .map_err(|e| AcrError::TokenError(format!("Failed to serialize claims: {}", e)))?;

        let header_b64url = url_engine.encode(header_json.as_bytes());
        let payload_b64url = url_engine.encode(payload_json.as_bytes());
        let signing_input = format!("{}.{}", header_b64url, payload_b64url);
        let signature = self.vault_client.sign_hmac(&signing_input).await?;

        Ok(format!("{}.{}", signing_input, signature))
    }

    /// [COMMENT]: Tạo Billing JWT Token, ký qua Vault Transit Engine (không có tenant).
    pub async fn generate_billing_token(&self, claims: &BillingClaims) -> Result<String, AcrError> {
        use base64::Engine;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;

        #[derive(Serialize)]
        struct Header {
            alg: &'static str,
            typ: &'static str,
        }
        let header = Header {
            alg: "HS256",
            typ: "JWT",
        };

        let header_json = serde_json::to_string(&header)
            .map_err(|e| AcrError::TokenError(format!("Failed to serialize header: {}", e)))?;
        let payload_json = serde_json::to_string(claims).map_err(|e| {
            AcrError::TokenError(format!("Failed to serialize billing claims: {}", e))
        })?;

        let header_b64url = url_engine.encode(header_json.as_bytes());
        let payload_b64url = url_engine.encode(payload_json.as_bytes());
        let signing_input = format!("{}.{}", header_b64url, payload_b64url);
        let signature = self.vault_client.sign_hmac(&signing_input).await?;

        Ok(format!("{}.{}", signing_input, signature))
    }

    /// [COMMENT]: Giải mã và xác thực tính hợp lệ của Billing JWT Token.
    #[allow(dead_code)]
    pub async fn verify_billing_token(&self, token: &str) -> Result<BillingClaims, AcrError> {
        let token = token.trim();
        if token.is_empty() {
            return Err(AcrError::TokenError("Empty billing token".to_string()));
        }

        let parts: Vec<&str> = token.split('.').collect();
        if parts.len() != 3 {
            return Err(AcrError::TokenError(
                "Malformed billing JWT structure".to_string(),
            ));
        }

        use base64::Engine;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;

        let payload_bytes = url_engine.decode(parts[1]).map_err(|e| {
            AcrError::TokenError(format!("Failed to decode billing JWT payload: {}", e))
        })?;

        let claims: BillingClaims = serde_json::from_slice(&payload_bytes).map_err(|e| {
            AcrError::TokenError(format!("Failed to parse billing JWT claims: {}", e))
        })?;

        let now = chrono::Utc::now().timestamp();
        if now > claims.exp {
            return Err(AcrError::TokenError(
                "Billing token has expired".to_string(),
            ));
        }

        let sig_part = parts[2];
        if sig_part.starts_with('v') {
            if let Some(idx) = sig_part.find('_') {
                let vault_version = &sig_part[..idx];
                let signature_b64url = &sig_part[idx + 1..];
                let signing_input = format!("{}.{}", parts[0], parts[1]);

                let is_valid = self
                    .vault_client
                    .verify_hmac(&signing_input, vault_version, signature_b64url)
                    .await?;
                if !is_valid {
                    return Err(AcrError::TokenError(
                        "Invalid billing token signature verified by Vault".to_string(),
                    ));
                }
                return Ok(claims);
            }
        }

        Err(AcrError::TokenError(
            "Billing token lacks Vault signature prefix".to_string(),
        ))
    }

    /// [COMMENT]: Đọc trực tiếp Admin API Key từ Vault và băm SHA-256 mà không qua cache RAM.
    pub async fn get_admin_api_key_hash(&self) -> Result<String, AcrError> {
        let secret = self
            .vault_client
            .read_secret(&self.admin_api_key_path)
            .await?;
        let api_key = secret["data"]["data"]["api_key"]
            .as_str()
            .ok_or_else(|| AcrError::Internal("api_key not found in Vault response".to_string()))?;

        use sha2::{Digest, Sha256};
        let mut hasher = Sha256::new();
        hasher.update(api_key.as_bytes());
        Ok(format!("{:x}", hasher.finalize()))
    }

    /// [COMMENT]: Ủy thác xác thực OTP SRE cho Vault TOTP Secrets Engine.
    pub async fn verify_admin_totp(&self, code: &str) -> Result<bool, AcrError> {
        self.vault_client.verify_totp(code).await
    }
}
