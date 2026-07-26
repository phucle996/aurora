// ======================================================================================================
// 📂 token.rs — Shared Vault-signed JWT manager cho IAM và domain Trinity
// ======================================================================================================

use crate::error::AcrError;
use crate::infra::vault::VaultClient;
use serde::Serialize;
use std::sync::Arc;

const JWT_SIG_CACHE_MAX_CAPACITY: u64 = 50_000;

pub struct TokenManager {
    pub(crate) vault_client: Arc<VaultClient>,
    jwt_sig_cache: moka::future::Cache<String, crate::user::claims::Claims>,
}

impl TokenManager {
    pub fn new(vault_client: Arc<VaultClient>) -> Self {
        Self {
            vault_client,
            jwt_sig_cache: moka::future::Cache::builder()
                .max_capacity(JWT_SIG_CACHE_MAX_CAPACITY)
                .build(),
        }
    }

    pub async fn verify_token(&self, token: &str) -> Result<crate::user::claims::Claims, AcrError> {
        let token = token.trim();
        if token.is_empty() {
            return Err(AcrError::TokenError("Empty token".to_string()));
        }
        use sha2::{Digest, Sha256};
        let cache_key = format!("{:x}", Sha256::digest(token.as_bytes()));
        if let Some(cached_claims) = self.jwt_sig_cache.get(&cache_key).await {
            if chrono::Utc::now().timestamp() <= cached_claims.exp {
                return Ok(cached_claims);
            }
            self.jwt_sig_cache.invalidate(&cache_key).await;
        }

        let parts: Vec<&str> = token.split('.').collect();
        if parts.len() != 3 {
            return Err(AcrError::TokenError("Malformed JWT structure".to_string()));
        }
        use base64::Engine;
        let engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;
        let payload = engine.decode(parts[1]).map_err(|error| {
            AcrError::TokenError(format!("Failed to decode JWT payload: {error}"))
        })?;
        let claims: crate::user::claims::Claims =
            serde_json::from_slice(&payload).map_err(|error| {
                AcrError::TokenError(format!("Failed to parse JWT claims: {error}"))
            })?;
        let now = chrono::Utc::now().timestamp();
        if now > claims.exp {
            return Err(AcrError::TokenError("Token has expired".to_string()));
        }
        self.verify_vault_signature(parts[0], parts[1], parts[2], "token")
            .await?;
        self.jwt_sig_cache.insert(cache_key, claims.clone()).await;
        Ok(claims)
    }

    pub async fn generate_token(
        &self,
        claims: &crate::user::claims::Claims,
    ) -> Result<String, AcrError> {
        self.generate_vault_token(claims, "user").await
    }

    pub async fn sign_zone_control_assertion(
        &self,
        key_path: &str,
        payload: &[u8],
    ) -> Result<(String, String), AcrError> {
        self.vault_client.sign_asymmetric(key_path, payload).await
    }

    async fn generate_vault_token<T: Serialize>(
        &self,
        claims: &T,
        kind: &str,
    ) -> Result<String, AcrError> {
        use base64::Engine;
        #[derive(Serialize)]
        struct Header {
            alg: &'static str,
            typ: &'static str,
        }
        let header = serde_json::to_vec(&Header {
            alg: "HS256",
            typ: "JWT",
        })
        .map_err(|error| {
            AcrError::TokenError(format!("Failed to serialize JWT header: {error}"))
        })?;
        let payload = serde_json::to_vec(claims).map_err(|error| {
            AcrError::TokenError(format!("Failed to serialize {kind} claims: {error}"))
        })?;
        let engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;
        let signing_input = format!("{}.{}", engine.encode(header), engine.encode(payload));
        let signature = self.vault_client.sign_hmac(&signing_input).await?;
        Ok(format!("{signing_input}.{signature}"))
    }

    async fn verify_vault_signature(
        &self,
        header: &str,
        payload: &str,
        signature: &str,
        kind: &str,
    ) -> Result<(), AcrError> {
        let separator = signature.find('_').filter(|_| signature.starts_with('v'));
        let Some(separator) = separator else {
            return Err(AcrError::TokenError(format!(
                "{kind} lacks Vault signature prefix"
            )));
        };
        let valid = self
            .vault_client
            .verify_hmac(
                &format!("{header}.{payload}"),
                &signature[..separator],
                &signature[separator + 1..],
            )
            .await?;
        if !valid {
            return Err(AcrError::TokenError(format!(
                "Invalid {kind} signature verified by Vault"
            )));
        }
        Ok(())
    }
}
