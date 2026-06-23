use crate::error::AclError;
use crate::infra::vault::VaultClient;
use serde::{Deserialize, Serialize};
use std::sync::Arc;

/// ============================================================================
/// 📂 MODULE: core/token.rs - JWT Identity Claims & Verification via Vault
/// ============================================================================
///
/// 📌 VAI TRÒ (ROLE):
///   - Định nghĩa cấu trúc Claims đồng bộ 100% với JSON payload sinh bởi Go controlplane.
///   - Giải mã token dạng Stateless, sau đó ủy quyền kiểm tra chữ ký (HMAC-SHA256)
///     cho Vault Transit Engine thông qua VaultClient.
///   - Tuân thủ nguyên tắc "Zero Trust": Không lưu trữ hay kiểm tra chữ ký cục bộ bằng env secret.
///

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Claims {
    // Subject (User UUID)
    #[serde(rename = "sub")]
    pub sub: String,

    // Single role from Go controlplane JWT
    #[serde(rename = "role", default)]
    pub role: String,

    // Level (0 = highest privilege)
    #[serde(rename = "lvl", default)]
    pub lvl: i32,

    // Tenant ID
    #[serde(rename = "tenant_id", default)]
    pub tenant_id: Option<String>,

    // Zone ID
    #[serde(rename = "zone_id", default)]
    pub zone_id: Option<String>,

    // Access key binding the JWT to a Redis session
    #[serde(rename = "access_key")]
    pub access_key: String,

    // JWT ID (Token unique identifier)
    #[serde(rename = "jti", default)]
    pub jti: String,

    // Issuer
    #[serde(rename = "iss", default)]
    pub iss: Option<String>,

    // Expiration timestamp (Unix epoch in seconds)
    #[serde(rename = "exp")]
    pub exp: i64,

    // Issued At timestamp
    #[serde(rename = "iat", default)]
    pub iat: i64,
}

impl Claims {
    /// Tiện ích chuyển đổi role đơn thành danh sách roles Vec<String>
    /// để tương thích với hệ thống AuthContext / PolicyEvaluator của Rust ACL.
    pub fn get_roles(&self) -> Vec<String> {
        if self.role.is_empty() {
            vec![]
        } else {
            vec![self.role.clone()]
        }
    }

    /// [COMMENT]: Kiểm tra xem user có đặc quyền Admin/SRE hay không dựa trên sub claim
    pub fn is_admin(&self) -> bool {
        // [COMMENT]: Chỉ chấp nhận định danh sub == "sre" làm Admin quản trị cao cấp
        self.sub == "sre"
    }
}

pub struct TokenManager {
    vault_client: Arc<VaultClient>,
}

impl TokenManager {
    pub fn new(vault_client: Arc<VaultClient>) -> Self {
        Self { vault_client }
    }

    /// Giải mã và xác thực tính hợp lệ của JWT Token sử dụng Vault Transit Engine
    /// Đồng bộ 100% với controlplane/internal/security/jwt.go::Parse().
    pub async fn verify_token(&self, token: &str) -> Result<Claims, AclError> {
        let token = token.trim();
        if token.is_empty() {
            return Err(AclError::TokenError("Empty token".to_string()));
        }

        // 1. Tách token thành 3 phần: Header, Payload, Signature
        let parts: Vec<&str> = token.split('.').collect();
        if parts.len() != 3 {
            return Err(AclError::TokenError("Malformed JWT structure".to_string()));
        }

        // 2. Decode & Deserialize phần Payload sang Claims để đọc thông tin trước
        use base64::Engine;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;

        let payload_bytes = url_engine
            .decode(parts[1])
            .map_err(|e| AclError::TokenError(format!("Failed to decode JWT payload: {}", e)))?;

        let claims: Claims = serde_json::from_slice(&payload_bytes)
            .map_err(|e| AclError::TokenError(format!("Failed to parse JWT claims: {}", e)))?;

        // 3. Kiểm tra tính hợp lệ về mặt thời gian (Expiration)
        let now = chrono::Utc::now().timestamp();
        if now > claims.exp {
            return Err(AclError::TokenError("Token has expired".to_string()));
        }

        // 4. Kiểm tra chữ ký JWT qua Vault Transit Engine
        let sig_part = parts[2];

        // Nhận diện định dạng chữ ký lai có chứa version của Vault (vd: "v1_signature_hash")
        if sig_part.starts_with('v') {
            if let Some(idx) = sig_part.find('_') {
                let vault_version = &sig_part[..idx]; // Ví dụ: "v1"
                let signature_b64url = &sig_part[idx + 1..];

                let signing_input = format!("{}.{}", parts[0], parts[1]);

                let is_valid = self
                    .vault_client
                    .verify_hmac(&signing_input, vault_version, signature_b64url)
                    .await?;
                if !is_valid {
                    return Err(AclError::TokenError(
                        "Invalid signature verified by Vault".to_string(),
                    ));
                }

                return Ok(claims);
            }
        }

        // Nếu token không có signature prefix của Vault -> Báo lỗi do production bắt buộc dùng Vault signature.
        Err(AclError::TokenError(
            "Token lacks Vault signature prefix or format is invalid".to_string(),
        ))
    }

    /// Tạo mới một JWT Token dựa trên claims cung cấp bằng cách ký qua Vault Transit Engine.
    /// Đồng bộ 100% logic với controlplane/internal/security/jwt.go::SignWithSecret().
    pub async fn generate_token(&self, claims: &Claims) -> Result<String, AclError> {
        use base64::Engine;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;

        // 1. Tạo JWT Header mặc định
        #[derive(Serialize)]
        struct Header {
            alg: &'static str,
            typ: &'static str,
        }
        let header = Header {
            alg: "HS256",
            typ: "JWT",
        };

        // 2. Serialize Header & Payload (Claims)
        let header_json = serde_json::to_string(&header)
            .map_err(|e| AclError::TokenError(format!("Failed to serialize header: {}", e)))?;
        let payload_json = serde_json::to_string(claims)
            .map_err(|e| AclError::TokenError(format!("Failed to serialize claims: {}", e)))?;

        // 3. Base64url encode các phần
        let header_b64url = url_engine.encode(header_json.as_bytes());
        let payload_b64url = url_engine.encode(payload_json.as_bytes());

        // 4. Tạo input ký: "header.payload"
        let signing_input = format!("{}.{}", header_b64url, payload_b64url);

        // 5. Ký qua Vault Transit
        let signature = self.vault_client.sign_hmac(&signing_input).await?;

        // 6. Ghép thành JWT đầy đủ: "header.payload.signature"
        Ok(format!("{}.{}", signing_input, signature))
    }
}
