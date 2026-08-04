// ======================================================================================================
// 📂 sre/claims.rs — Claims JWT struct và TokenManager cho SRE Admin
// ======================================================================================================

use crate::error::AcrError;
use crate::infra::vault::VaultClient;
use serde::{Deserialize, Serialize};
use std::sync::Arc;

/// [COMMENT]: SreClaims — JWT payload cô lập dành riêng cho SRE Admin.
/// Không mang thông tin định danh cá nhân hay RBAC. Chỉ mang ngữ cảnh đăng nhập SRE.
#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct SreClaims {
    #[serde(rename = "sub")]
    pub sub: String,

    #[serde(rename = "zid", default)]
    pub zone_id: Option<String>,

    #[serde(rename = "access_key")]
    pub access_key: String,

    #[serde(rename = "iss", default)]
    pub iss: Option<String>,

    #[serde(rename = "exp")]
    pub exp: i64,

    #[serde(rename = "iat", default)]
    pub iat: i64,
}

pub struct SreTokenManager {
    vault_client: Arc<VaultClient>,
    admin_api_key_path: String,
    jwt_sig_cache: moka::future::Cache<String, SreClaims>,
}

impl SreTokenManager {
    pub fn new(vault_client: Arc<VaultClient>, admin_api_key_path: String) -> Self {
        let jwt_sig_cache = moka::future::Cache::builder().max_capacity(50000).build();
        Self {
            vault_client,
            admin_api_key_path,
            jwt_sig_cache,
        }
    }

    pub async fn verify_token(&self, token: &str) -> Result<SreClaims, AcrError> {
        let token = token.trim();
        if token.is_empty() {
            return Err(AcrError::TokenError("Empty SRE token".to_string()));
        }

        use sha2::{Digest, Sha256};
        let cache_key = {
            let mut hasher = Sha256::new();
            hasher.update(token.as_bytes());
            format!("{:x}", hasher.finalize())
        };

        if let Some(cached_claims) = self.jwt_sig_cache.get(&cache_key).await {
            let now = chrono::Utc::now().timestamp();
            if now <= cached_claims.exp {
                return Ok(cached_claims);
            }
            self.jwt_sig_cache.invalidate(&cache_key).await;
        }

        let parts: Vec<&str> = token.split('.').collect();
        if parts.len() != 3 {
            return Err(AcrError::TokenError(
                "Malformed SRE JWT structure".to_string(),
            ));
        }

        use base64::Engine;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;

        let payload_bytes = url_engine.decode(parts[1]).map_err(|e| {
            AcrError::TokenError(format!("Failed to decode SRE JWT payload: {}", e))
        })?;

        let claims: SreClaims = serde_json::from_slice(&payload_bytes)
            .map_err(|e| AcrError::TokenError(format!("Failed to parse SRE JWT claims: {}", e)))?;

        let now = chrono::Utc::now().timestamp();
        if now > claims.exp {
            return Err(AcrError::TokenError("SRE token has expired".to_string()));
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
                        "Invalid SRE token signature verified by Vault".to_string(),
                    ));
                }

                self.jwt_sig_cache.insert(cache_key, claims.clone()).await;
                return Ok(claims);
            }
        }

        Err(AcrError::TokenError(
            "SRE token lacks Vault signature prefix".to_string(),
        ))
    }

    pub async fn generate_token(&self, claims: &SreClaims) -> Result<String, AcrError> {
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
            .map_err(|e| AcrError::TokenError(format!("Failed to serialize SRE claims: {}", e)))?;

        let header_b64url = url_engine.encode(header_json.as_bytes());
        let payload_b64url = url_engine.encode(payload_json.as_bytes());
        let signing_input = format!("{}.{}", header_b64url, payload_b64url);
        let signature = self.vault_client.sign_hmac(&signing_input).await?;

        Ok(format!("{}.{}", signing_input, signature))
    }

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

    pub async fn verify_admin_totp(&self, code: &str) -> Result<bool, AcrError> {
        self.vault_client.verify_totp(code).await
    }
}
