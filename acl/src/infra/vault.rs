use crate::config::VaultConfig;
use crate::error::AclError;
use crate::observability::logger::Logger;
use std::time::Duration;

/// ============================================================================
/// 📂 MODULE: infra/vault.rs - Vault Transit Engine Client (JWT HMAC Verify)
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Kết nối tới HashiCorp Vault qua REST API để xác thực chữ ký JWT HMAC-SHA256.
///   - ACL Service KHÔNG BAO GIỜ nắm giữ JWT signing secret — mọi phép ký/xác thực
///     đều được ủy thác hoàn toàn cho Vault Transit Engine.
///   - Hỗ trợ AppRole authentication (production) hoặc static token (dev).
///
/// 🔒 RANH GIỚI BẢO MẬT (SECURITY BOUNDARY):
///   - Secret material (HMAC key) KHÔNG BAO GIỜ rời khỏi Vault.
///   - ACL chỉ gửi dữ liệu cần verify dạng Base64 sang Vault, nhận kết quả true/false.
///   - Đồng bộ 100% logic với controlplane/internal/security/jwt.go.
///

pub struct VaultClient {
    // HTTP client tái sử dụng connection pool
    http_client: reqwest::Client,
    // Địa chỉ Vault Server (ví dụ: http://controlplane-vault:8200)
    addr: String,
    // Token xác thực REST API (được lấy từ AppRole login hoặc static token)
    token: String,
    // Tên khóa Transit dùng cho HMAC ký/xác thực JWT (mặc định: jwt-signer)
    transit_key_name: String,
}

impl VaultClient {
    /// Khởi tạo VaultClient với cơ chế retry và AppRole authentication.
    /// Đồng bộ logic với controlplane/infra/vault/vault.go::NewVaultClient().
    pub async fn new(cfg: &VaultConfig) -> Result<Self, AclError> {
        let http_client = reqwest::Client::builder()
            .timeout(cfg.timeout)
            .build()
            .map_err(|e| AclError::Internal(format!("Failed to build Vault HTTP client: {}", e)))?;

        let mut last_err = String::new();

        // Retry loop tương đương Go implementation
        for attempt in 1..=cfg.max_retries {
            // 1. Xác thực: AppRole (production) hoặc Static Token (dev)
            let token = if !cfg.role_id.is_empty() && !cfg.secret_id.is_empty() {
                // AppRole Login: POST auth/approle/login
                match Self::approle_login(&http_client, &cfg.addr, &cfg.role_id, &cfg.secret_id)
                    .await
                {
                    Ok(t) => t,
                    Err(e) => {
                        last_err = format!("AppRole login attempt {}: {}", attempt, e);
                        Logger::sys_warn(
                            "vault.init",
                            &format!("AppRole login attempt {} failed", attempt),
                            &last_err,
                        );
                        if attempt < cfg.max_retries {
                            tokio::time::sleep(Duration::from_secs(1)).await;
                        }
                        continue;
                    }
                }
            } else {
                // Static token fallback (dev/testing)
                cfg.token.clone()
            };

            // 2. Health check: Kiểm tra Vault đã initialized + unsealed
            match Self::health_check(&http_client, &cfg.addr).await {
                Ok(()) => {
                    Logger::sys_info(
                        "vault.init",
                        &format!(
                            "Vault client connected successfully to {} (attempt {})",
                            cfg.addr, attempt
                        ),
                    );
                    return Ok(Self {
                        http_client,
                        addr: cfg.addr.clone(),
                        token,
                        transit_key_name: cfg.transit_key_name.clone(),
                    });
                }
                Err(e) => {
                    last_err = format!("Health check attempt {}: {}", attempt, e);
                    Logger::sys_warn(
                        "vault.init",
                        &format!("Vault health check attempt {} failed", attempt),
                        &last_err,
                    );
                    if attempt < cfg.max_retries {
                        tokio::time::sleep(Duration::from_secs(1)).await;
                    }
                }
            }
        }

        Err(AclError::Internal(format!(
            "Vault: failed to establish healthy connection after {} attempts: {}",
            cfg.max_retries, last_err
        )))
    }

    /// Đăng nhập bằng AppRole và trả về Client Token.
    async fn approle_login(
        http: &reqwest::Client,
        addr: &str,
        role_id: &str,
        secret_id: &str,
    ) -> Result<String, AclError> {
        let url = format!("{}/v1/auth/approle/login", addr);
        let body = serde_json::json!({
            "role_id": role_id,
            "secret_id": secret_id,
        });

        let resp = http
            .post(&url)
            .json(&body)
            .send()
            .await
            .map_err(|e| AclError::Internal(format!("AppRole login request failed: {}", e)))?;

        let status = resp.status();
        let json: serde_json::Value = resp.json().await.map_err(|e| {
            AclError::Internal(format!("AppRole login response parse failed: {}", e))
        })?;

        if !status.is_success() {
            return Err(AclError::Internal(format!(
                "AppRole login HTTP {}: {:?}",
                status, json
            )));
        }

        // Trích xuất client_token từ auth object
        json["auth"]["client_token"]
            .as_str()
            .filter(|t| !t.is_empty())
            .map(|t| t.to_string())
            .ok_or_else(|| {
                AclError::Internal("AppRole login returned empty client_token".to_string())
            })
    }

    /// Kiểm tra Vault đã initialized và unsealed.
    async fn health_check(http: &reqwest::Client, addr: &str) -> Result<(), AclError> {
        let url = format!("{}/v1/sys/health", addr);
        let resp = http
            .get(&url)
            .send()
            .await
            .map_err(|e| AclError::Internal(format!("Vault health check failed: {}", e)))?;

        let json: serde_json::Value = resp.json().await.map_err(|e| {
            AclError::Internal(format!("Vault health response parse failed: {}", e))
        })?;

        let initialized = json["initialized"].as_bool().unwrap_or(false);
        let sealed = json["sealed"].as_bool().unwrap_or(true);

        if !initialized {
            return Err(AclError::Internal(
                "Vault is not initialized yet".to_string(),
            ));
        }
        if sealed {
            return Err(AclError::Internal("Vault is sealed".to_string()));
        }

        Ok(())
    }

    /// Xác thực chữ ký HMAC-SHA256 của JWT qua Vault Transit Engine.
    /// Đồng bộ 100% logic với controlplane/internal/security/jwt.go::Parse() vault path.
    ///
    /// Tham số:
    ///   - signing_input: "header.payload" (base64url encoded)
    ///   - vault_version: "v1" (phiên bản khóa Transit)
    ///   - signature_b64url: phần chữ ký raw URL base64
    ///
    /// Trả về: true nếu chữ ký hợp lệ, false nếu không.
    pub async fn verify_hmac(
        &self,
        signing_input: &str,
        vault_version: &str,
        signature_b64url: &str,
    ) -> Result<bool, AclError> {
        use base64::Engine;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;
        let std_engine = base64::engine::general_purpose::STANDARD;

        // 1. Decode chữ ký từ Base64 Raw URL sang bytes
        let sig_bytes = url_engine
            .decode(signature_b64url)
            .map_err(|e| AclError::TokenError(format!("Failed to decode signature: {}", e)))?;

        // 2. Encode lại sang Base64 Standard (Vault API yêu cầu Standard encoding)
        let sig_b64_std = std_engine.encode(&sig_bytes);

        // 3. Tái dựng chuỗi HMAC đúng chuẩn Vault: "vault:<version>:<base64_std_signature>"
        let vault_hmac = format!("vault:{}:{}", vault_version, sig_b64_std);

        // 4. Encode signing_input sang Base64 Standard cho Vault
        let input_b64 = std_engine.encode(signing_input.as_bytes());

        // 5. Gọi Vault Transit API verify
        let url = format!("{}/v1/transit/verify/{}", self.addr, self.transit_key_name);
        let body = serde_json::json!({
            "input": input_b64,
            "hmac": vault_hmac,
            "algorithm": "sha2-256",
        });

        let resp = self
            .http_client
            .post(&url)
            .header("X-Vault-Token", &self.token)
            .json(&body)
            .send()
            .await
            .map_err(|e| {
                AclError::Internal(format!("Vault Transit verify request failed: {}", e))
            })?;

        let status = resp.status();
        let json: serde_json::Value = resp.json().await.map_err(|e| {
            AclError::Internal(format!("Vault Transit verify response parse failed: {}", e))
        })?;

        if !status.is_success() {
            Logger::sys_error(
                "vault.verify_hmac",
                &format!("Vault Transit verify HTTP {}", status),
                &format!("{:?}", json),
            );
            return Err(AclError::Internal(format!("Vault verify HTTP {}", status)));
        }

        // 6. Trích xuất kết quả xác thực
        let valid = json["data"]["valid"].as_bool().unwrap_or(false);
        Ok(valid)
    }

    /// Ký chữ ký HMAC-SHA256 của JWT qua Vault Transit Engine.
    /// Đồng bộ 100% logic với controlplane/internal/security/jwt.go::SignWithSecret() vault path.
    ///
    /// Tham số:
    ///   - signing_input: "header.payload" (base64url encoded)
    ///
    /// Trả về: Chữ ký định dạng "version_signature_b64url" (ví dụ: "v1_abcd123...")
    pub async fn sign_hmac(&self, signing_input: &str) -> Result<String, AclError> {
        use base64::Engine;
        let std_engine = base64::engine::general_purpose::STANDARD;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;

        // 1. Encode signing_input sang Base64 Standard cho Vault Transit
        let input_b64 = std_engine.encode(signing_input.as_bytes());

        // 2. Gọi Vault Transit API hmac
        let url = format!("{}/v1/transit/hmac/{}", self.addr, self.transit_key_name);
        let body = serde_json::json!({
            "input": input_b64,
            "algorithm": "sha2-256",
        });

        let resp = self
            .http_client
            .post(&url)
            .header("X-Vault-Token", &self.token)
            .json(&body)
            .send()
            .await
            .map_err(|e| AclError::Internal(format!("Vault Transit hmac request failed: {}", e)))?;

        let status = resp.status();
        let json: serde_json::Value = resp.json().await.map_err(|e| {
            AclError::Internal(format!("Vault Transit hmac response parse failed: {}", e))
        })?;

        if !status.is_success() {
            Logger::sys_error(
                "vault.sign_hmac",
                &format!("Vault Transit hmac HTTP {}", status),
                &format!("{:?}", json),
            );
            return Err(AclError::Internal(format!("Vault hmac HTTP {}", status)));
        }

        // 3. Trích xuất hmac signature dạng "vault:v1:base64_std_signature"
        let hmac_val = json["data"]["hmac"]
            .as_str()
            .filter(|s| !s.is_empty())
            .ok_or_else(|| {
                AclError::Internal("Vault Transit hmac returned empty hmac value".to_string())
            })?;

        // 4. Split signature thành 3 phần: "vault", version, signature_b64_std
        let parts: Vec<&str> = hmac_val.split(':').collect();
        if parts.len() < 3 {
            return Err(AclError::Internal(
                "Malformed Vault HMAC signature format".to_string(),
            ));
        }

        let version = parts[1]; // Ví dụ: "v1"
        let sig_b64_std = parts[2];

        // 5. Decode signature từ Base64 Standard sang bytes
        let sig_bytes = std_engine.decode(sig_b64_std).map_err(|e| {
            AclError::Internal(format!("Failed to decode Vault hmac signature: {}", e))
        })?;

        // 6. Encode lại sang Base64 Raw URL để đúng đặc tả JWT
        let sig_b64_url = url_engine.encode(&sig_bytes);

        // 7. Trả về định dạng version_signature_b64url (ví dụ: "v1_abcd...")
        Ok(format!("{}_{}", version, sig_b64_url))
    }
}
