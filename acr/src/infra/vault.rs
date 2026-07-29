use crate::config::VaultConfig;
use crate::error::AcrError;
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

#[derive(Clone)]
pub struct VaultClient {
    // HTTP client tái sử dụng connection pool
    http_client: reqwest::Client,
    // Địa chỉ Vault Server (ví dụ: http://controlplane-vault:8200)
    addr: String,
    // Token xác thực REST API (được lấy từ AppRole login hoặc static token)
    token: String,
    // Mount path của Transit Engine trong Vault (mặc định: transit)
    transit_mount_path: String,
    // Tên khóa Transit dùng cho HMAC ký/xác thực JWT (mặc định: jwt-signer)
    transit_key_name: String,
    // Mount path của TOTP Engine (mặc định: totp)
    totp_mount_path: String,
    // Tên khóa TOTP dùng cho OTP (mặc định: admin)
    totp_key_name: String,
    // Bounded retry budget shared by startup-only KV reads.
    max_retries: usize,
}

impl VaultClient {
    /// Khởi tạo VaultClient với cơ chế retry và AppRole authentication.
    /// Đồng bộ logic với controlplane/infra/vault/vault.go::NewVaultClient().
    pub async fn new(cfg: &VaultConfig) -> Result<Self, AcrError> {
        let http_client = reqwest::Client::builder()
            .timeout(cfg.timeout)
            .build()
            .map_err(|e| AcrError::Internal(format!("Failed to build Vault HTTP client: {}", e)))?;

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
            } else if !cfg.kubernetes_role.is_empty() {
                match Self::kubernetes_login(
                    &http_client,
                    &cfg.addr,
                    &cfg.kubernetes_role,
                    &cfg.kubernetes_jwt_path,
                )
                .await
                {
                    Ok(t) => t,
                    Err(e) => {
                        last_err = format!("Kubernetes auth attempt {} failed", attempt);
                        Logger::sys_warn(
                            "vault.init",
                            &format!("Kubernetes auth attempt {} failed", attempt),
                            &e.to_string(),
                        );
                        if attempt < cfg.max_retries {
                            tokio::time::sleep(Duration::from_secs(1)).await;
                        }
                        continue;
                    }
                }
            } else {
                // Static token fallback (dev/testing)
                if cfg.token.trim().is_empty() {
                    last_err =
                        "no Vault token, AppRole credentials, or Kubernetes auth role configured"
                            .to_string();
                    continue;
                }
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
                    // [COMMENT]: Phân tách mount path và key name từ đường dẫn khóa transit (ví dụ: transit/keys/jwt-signer)
                    let transit_parts = cfg
                        .transit_key_path
                        .trim_matches('/')
                        .split('/')
                        .filter(|part| !part.is_empty())
                        .collect::<Vec<_>>();
                    let (transit_mount, transit_key) = match transit_parts.as_slice() {
                        [mount, "keys", key] => ((*mount).to_owned(), (*key).to_owned()),
                        _ => {
                            return Err(AcrError::ConfigError(
                                "VAULT_TRANSIT_KEY_PATH must use mount/keys/key".to_owned(),
                            ))
                        }
                    };

                    // [COMMENT]: Phân tách mount path và key name từ đường dẫn khóa TOTP (ví dụ: totp/keys/admin)
                    let totp_parts = cfg
                        .totp_key_path
                        .trim_matches('/')
                        .split('/')
                        .filter(|part| !part.is_empty())
                        .collect::<Vec<_>>();
                    let (totp_mount, totp_key) = match totp_parts.as_slice() {
                        [mount, "keys", key] => ((*mount).to_owned(), (*key).to_owned()),
                        _ => {
                            return Err(AcrError::ConfigError(
                                "VAULT_TOTP_KEY_PATH must use mount/keys/key".to_owned(),
                            ))
                        }
                    };

                    return Ok(Self {
                        http_client,
                        addr: cfg.addr.clone(),
                        token,
                        transit_mount_path: transit_mount,
                        transit_key_name: transit_key,
                        totp_mount_path: totp_mount,
                        totp_key_name: totp_key,
                        max_retries: cfg.max_retries.max(1),
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

        Err(AcrError::Internal(format!(
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
    ) -> Result<String, AcrError> {
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
            .map_err(|e| AcrError::Internal(format!("AppRole login request failed: {}", e)))?;

        let status = resp.status();
        let json: serde_json::Value = resp.json().await.map_err(|e| {
            AcrError::Internal(format!("AppRole login response parse failed: {}", e))
        })?;

        if !status.is_success() {
            return Err(AcrError::Internal(format!(
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
                AcrError::Internal("AppRole login returned empty client_token".to_string())
            })
    }

    async fn kubernetes_login(
        http: &reqwest::Client,
        addr: &str,
        role: &str,
        jwt_path: &str,
    ) -> Result<String, AcrError> {
        let jwt = tokio::fs::read_to_string(jwt_path)
            .await
            .map_err(|e| AcrError::Internal(format!("Kubernetes Vault JWT read failed: {e}")))?;
        let body = serde_json::json!({
            "role": role,
            "jwt": jwt.trim(),
        });
        let resp = http
            .post(format!(
                "{}/v1/auth/kubernetes/login",
                addr.trim_end_matches('/')
            ))
            .json(&body)
            .send()
            .await
            .map_err(|e| AcrError::Internal(format!("Kubernetes Vault login failed: {e}")))?;
        let status = resp.status();
        let json: serde_json::Value = resp
            .json()
            .await
            .map_err(|e| AcrError::Internal(format!("Kubernetes Vault login parse failed: {e}")))?;
        if !status.is_success() {
            return Err(AcrError::Internal(format!(
                "Kubernetes Vault login HTTP {}",
                status
            )));
        }
        json["auth"]["client_token"]
            .as_str()
            .filter(|value| !value.trim().is_empty())
            .map(str::to_string)
            .ok_or_else(|| {
                AcrError::Internal("Kubernetes Vault login returned empty client token".to_string())
            })
    }

    /// Kiểm tra Vault đã initialized và unsealed.
    async fn health_check(http: &reqwest::Client, addr: &str) -> Result<(), AcrError> {
        let url = format!("{}/v1/sys/health", addr);
        let resp = http
            .get(&url)
            .send()
            .await
            .map_err(|e| AcrError::Internal(format!("Vault health check failed: {}", e)))?;

        let json: serde_json::Value = resp.json().await.map_err(|e| {
            AcrError::Internal(format!("Vault health response parse failed: {}", e))
        })?;

        let initialized = json["initialized"].as_bool().unwrap_or(false);
        let sealed = json["sealed"].as_bool().unwrap_or(true);

        if !initialized {
            return Err(AcrError::Internal(
                "Vault is not initialized yet".to_string(),
            ));
        }
        if sealed {
            return Err(AcrError::Internal("Vault is sealed".to_string()));
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
    ) -> Result<bool, AcrError> {
        use base64::Engine;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;
        let std_engine = base64::engine::general_purpose::STANDARD;

        // 1. Decode chữ ký từ Base64 Raw URL sang bytes
        let sig_bytes = url_engine
            .decode(signature_b64url)
            .map_err(|e| AcrError::TokenError(format!("Failed to decode signature: {}", e)))?;

        // 2. Encode lại sang Base64 Standard (Vault API yêu cầu Standard encoding)
        let sig_b64_std = std_engine.encode(&sig_bytes);

        // 3. Tái dựng chuỗi HMAC đúng chuẩn Vault: "vault:<version>:<base64_std_signature>"
        let vault_hmac = format!("vault:{}:{}", vault_version, sig_b64_std);

        // 4. Encode signing_input sang Base64 Standard cho Vault
        let input_b64 = std_engine.encode(signing_input.as_bytes());

        // 5. Gọi Vault Transit API verify dùng đường dẫn linh hoạt
        let url = format!(
            "{}/v1/{}/verify/{}",
            self.addr, self.transit_mount_path, self.transit_key_name
        );
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
                AcrError::Internal(format!("Vault Transit verify request failed: {}", e))
            })?;

        let status = resp.status();
        let json: serde_json::Value = resp.json().await.map_err(|e| {
            AcrError::Internal(format!("Vault Transit verify response parse failed: {}", e))
        })?;

        if !status.is_success() {
            Logger::sys_error(
                "vault.verify_hmac",
                &format!("Vault Transit verify HTTP {}", status),
                &format!("{:?}", json),
            );
            return Err(AcrError::Internal(format!("Vault verify HTTP {}", status)));
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
    pub async fn sign_hmac(&self, signing_input: &str) -> Result<String, AcrError> {
        use base64::Engine;
        let std_engine = base64::engine::general_purpose::STANDARD;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;

        // 1. Encode signing_input sang Base64 Standard cho Vault Transit
        let input_b64 = std_engine.encode(signing_input.as_bytes());

        // 2. Gọi Vault Transit API hmac dùng đường dẫn linh hoạt
        let url = format!(
            "{}/v1/{}/hmac/{}",
            self.addr, self.transit_mount_path, self.transit_key_name
        );
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
            .map_err(|e| AcrError::Internal(format!("Vault Transit hmac request failed: {}", e)))?;

        let status = resp.status();
        let json: serde_json::Value = resp.json().await.map_err(|e| {
            AcrError::Internal(format!("Vault Transit hmac response parse failed: {}", e))
        })?;

        if !status.is_success() {
            Logger::sys_error(
                "vault.sign_hmac",
                &format!("Vault Transit hmac HTTP {}", status),
                &format!("{:?}", json),
            );
            return Err(AcrError::Internal(format!("Vault hmac HTTP {}", status)));
        }

        // 3. Trích xuất hmac signature dạng "vault:v1:base64_std_signature"
        let hmac_val = json["data"]["hmac"]
            .as_str()
            .filter(|s| !s.is_empty())
            .ok_or_else(|| {
                AcrError::Internal("Vault Transit hmac returned empty hmac value".to_string())
            })?;

        // 4. Split signature thành 3 phần: "vault", version, signature_b64_std
        let parts: Vec<&str> = hmac_val.split(':').collect();
        if parts.len() < 3 {
            return Err(AcrError::Internal(
                "Malformed Vault HMAC signature format".to_string(),
            ));
        }

        let version = parts[1]; // Ví dụ: "v1"
        let sig_b64_std = parts[2];

        // 5. Decode signature từ Base64 Standard sang bytes
        let sig_bytes = std_engine.decode(sig_b64_std).map_err(|e| {
            AcrError::Internal(format!("Failed to decode Vault hmac signature: {}", e))
        })?;

        // 6. Encode lại sang Base64 Raw URL để đúng đặc tả JWT
        let sig_b64_url = url_engine.encode(&sig_bytes);

        // 7. Trả về định dạng version_signature_b64url (ví dụ: "v1_abcd...")
        Ok(format!("{}_{}", version, sig_b64_url))
    }

    /// Signs a short-lived Zone assertion with a dedicated asymmetric Transit
    /// key. Only the public key is distributed to Zones; this method never
    /// exposes private key material to ACR or Dataplane.
    pub async fn sign_asymmetric(
        &self,
        key_path: &str,
        input: &[u8],
    ) -> Result<(String, String), AcrError> {
        use base64::Engine;

        let normalized = key_path.trim_matches('/');
        let parts: Vec<&str> = normalized
            .split('/')
            .filter(|part| !part.is_empty())
            .collect();
        let (mount, key) = match parts.as_slice() {
            [mount, "keys", key] => (*mount, *key),
            [mount, key] => (*mount, *key),
            [key] => ("transit", *key),
            _ => {
                return Err(AcrError::ConfigError(
                    "VAULT_ZONE_CONTROL_ASSERTION_KEY_PATH must identify one Transit key"
                        .to_string(),
                ))
            }
        };
        let response = self
            .http_client
            .post(format!("{}/v1/{}/sign/{}", self.addr, mount, key))
            .header("X-Vault-Token", &self.token)
            .json(&serde_json::json!({
                "input": base64::engine::general_purpose::STANDARD.encode(input),
            }))
            .send()
            .await
            .map_err(|error| {
                AcrError::Internal(format!("Vault Zone control assertion sign failed: {error}"))
            })?;
        let status = response.status();
        let body: serde_json::Value = response.json().await.map_err(|error| {
            AcrError::Internal(format!(
                "Vault Zone control assertion response invalid: {error}"
            ))
        })?;
        if !status.is_success() {
            return Err(AcrError::Internal(format!(
                "Vault Zone control assertion sign HTTP {status}"
            )));
        }
        let signature = body["data"]["signature"].as_str().ok_or_else(|| {
            AcrError::Internal("Vault Zone control assertion signature missing".to_string())
        })?;
        let mut signature_parts = signature.splitn(3, ':');
        if signature_parts.next() != Some("vault") {
            return Err(AcrError::Internal(
                "Vault Zone control assertion signature format invalid".to_string(),
            ));
        }
        let version = signature_parts.next().ok_or_else(|| {
            AcrError::Internal("Vault Zone control assertion version missing".to_string())
        })?;
        let encoded = signature_parts
            .next()
            .filter(|value| !value.is_empty())
            .ok_or_else(|| {
                AcrError::Internal("Vault Zone control assertion bytes missing".to_string())
            })?;
        Ok((format!("{key}:{version}"), encoded.to_string()))
    }

    /// [COMMENT]: Đọc dữ liệu secret từ một đường dẫn bất kỳ trong Vault (KV v2)
    /// Hỗ trợ đọc API Key và 2FA Secret thô phục vụ xác thực tại biên (ACL).
    pub async fn read_secret(&self, path: &str) -> Result<serde_json::Value, AcrError> {
        let url = format!("{}/v1/{}", self.addr, path);
        let mut last_error = "no Vault read attempt".to_string();
        for attempt in 1..=self.max_retries {
            match self
                .http_client
                .get(&url)
                .header("X-Vault-Token", &self.token)
                .send()
                .await
            {
                Ok(response) if response.status().is_success() => {
                    return response.json().await.map_err(|error| {
                        AcrError::Internal(format!("Vault read response parse failed: {}", error))
                    });
                }
                Ok(response) => {
                    let status = response.status();
                    // Never read or log the response body: an engine error may
                    // itself contain secret-bearing fields.
                    if status != reqwest::StatusCode::TOO_MANY_REQUESTS && !status.is_server_error()
                    {
                        return Err(AcrError::Internal(format!("Vault read HTTP {}", status)));
                    }
                    last_error = format!("Vault read HTTP {}", status);
                }
                Err(error) => last_error = format!("Vault read request failed: {}", error),
            }
            if attempt < self.max_retries {
                tokio::time::sleep(Duration::from_millis(attempt as u64 * 250)).await;
            }
        }
        Logger::sys_error("vault.read_secret", &last_error, "redacted");
        Err(AcrError::Internal(format!(
            "Vault read failed after {} attempts",
            self.max_retries
        )))
    }

    pub async fn read_redis_url(&self, path: &str) -> Result<String, AcrError> {
        let secret = self.read_secret(path).await?;
        let data = secret
            .get("data")
            .and_then(|value| value.get("data"))
            .ok_or_else(|| {
                AcrError::Internal("Vault connection record is missing KV data".to_string())
            })?;
        if data
            .get("schema_version")
            .and_then(serde_json::Value::as_u64)
            != Some(1)
        {
            return Err(AcrError::Internal(
                "Unsupported Vault connection schema_version".to_string(),
            ));
        }
        let redis_url = data
            .get("url")
            .and_then(serde_json::Value::as_str)
            .map(str::trim)
            .filter(|value| !value.is_empty())
            .ok_or_else(|| AcrError::Internal("Vault Redis URL is missing".to_string()))?;
        if !redis_url.starts_with("redis://") && !redis_url.starts_with("rediss://") {
            return Err(AcrError::Internal(
                "Vault Redis URL must use redis:// or rediss://".to_string(),
            ));
        }
        Ok(redis_url.to_string())
    }

    /// [COMMENT]: Thực hiện gửi mã OTP đến Vault TOTP Secrets Engine để kiểm tra trực tiếp
    /// Giúp bảo vệ an toàn tối đa cho 2FA Secret Key (không bao giờ rời khỏi Vault)
    pub async fn verify_totp(&self, code: &str) -> Result<bool, AcrError> {
        // [COMMENT]: 1. Xây dựng URL tới Vault TOTP verify endpoint sử dụng cấu hình động
        let url = format!(
            "{}/v1/{}/code/{}",
            self.addr, self.totp_mount_path, self.totp_key_name
        );

        // [COMMENT]: 2. Chuẩn bị request body chứa OTP code từ client
        let body = serde_json::json!({
            "code": code,
        });

        // [COMMENT]: 3. Thực thi POST request sang Vault kèm theo token xác thực hợp lệ
        let resp = self
            .http_client
            .post(&url)
            .header("X-Vault-Token", &self.token)
            .json(&body)
            .send()
            .await
            .map_err(|e| AcrError::Internal(format!("Vault TOTP verify request failed: {}", e)))?;

        let status = resp.status();
        let json: serde_json::Value = resp.json().await.map_err(|e| {
            AcrError::Internal(format!("Vault TOTP verify response parse failed: {}", e))
        })?;

        // [COMMENT]: 4. Xử lý kết quả trả về từ Vault
        if !status.is_success() {
            Logger::sys_warn(
                "vault.verify_totp",
                &format!("Vault TOTP verify HTTP {}: {:?}", status, json),
                "",
            );
            return Ok(false);
        }

        let valid = json["data"]["valid"].as_bool().unwrap_or(false);
        Ok(valid)
    }
}
