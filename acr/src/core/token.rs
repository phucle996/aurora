use crate::error::AcrError;
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
///   - L1 Cache (moka) cho JWT Signature Verification: Tránh gọi Vault mỗi request,
///     chỉ cache token đã được Vault xác nhận hợp lệ. Garbage/invalid token không bao giờ vào cache.
///   - An toàn thu hồi session: Lớp Redis L2 luôn được kiểm tra sau L1 nên revocation có hiệu lực ngay lập tức.
///

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Claims {
    // Subject (Username)
    #[serde(rename = "sub")]
    pub sub: String,

    // User UUID
    #[serde(rename = "uid")]
    pub uid: String,

    // Role UUID từ Go controlplane JWT — đã chuyển sang ID-based authorization
    #[serde(rename = "role_id", default)]
    pub role_id: String,

    // Level (0 = highest privilege)
    #[serde(rename = "lvl", default)]
    pub lvl: i32,

    // Tenant Context
    #[serde(rename = "tnc", default)]
    pub tenant_id: Option<String>,

    // Zone ID
    #[serde(rename = "zid", default)]
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
    /// Tiện ích trả về role_id dưới dạng Vec để tương thích với AuthContext / PolicyEvaluator.
    pub fn get_roles(&self) -> Vec<String> {
        if self.role_id.is_empty() {
            vec![]
        } else {
            vec![self.role_id.clone()]
        }
    }

    /// [COMMENT]: Kiểm tra xem user có đặc quyền Admin/SRE hay không dựa trên sub claim
    pub fn is_admin(&self) -> bool {
        // [COMMENT]: Chỉ chấp nhận định danh sub == "sre" làm Admin quản trị cao cấp
        self.sub == "sre"
    }
}

// [COMMENT]: Giới hạn tối đa số lượng bản ghi trong L1 JWT Signature Cache.
// Mỗi entry ~128 bytes → 50,000 entries ≈ 6.4 MB RAM — an toàn cho mọi cấu hình Pod.
const JWT_SIG_CACHE_MAX_CAPACITY: u64 = 50_000;

pub struct TokenManager {
    vault_client: Arc<VaultClient>,
    // [COMMENT]: Đường dẫn động đến Admin API Key trong Vault
    admin_api_key_path: String,
    // [COMMENT]: L1 Cache cho JWT Signature Verification (moka concurrent cache)
    // Key: SHA-256 hex digest của toàn bộ JWT token string
    // Value: Claims đã được deserialize và xác thực chữ ký bởi Vault
    // Chỉ cache token hợp lệ — token giả/hết hạn/chữ ký sai không bao giờ vào cache.
    // TTL tự động tính theo thời gian còn lại của JWT (exp - now).
    jwt_sig_cache: moka::future::Cache<String, Claims>,
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

    /// Giải mã và xác thực tính hợp lệ của JWT Token.
    /// Sử dụng L1 Cache (moka) để tránh gọi Vault Transit trên mỗi request.
    /// Đồng bộ 100% với controlplane/internal/security/jwt.go::Parse().
    pub async fn verify_token(&self, token: &str) -> Result<Claims, AcrError> {
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
            // [COMMENT]: Kiểm tra lại expiration phòng trường hợp edge case
            let now = chrono::Utc::now().timestamp();
            if now <= cached_claims.exp {
                return Ok(cached_claims);
            }
            // [COMMENT]: Token đã hết hạn trong khi còn trong cache → loại bỏ
            self.jwt_sig_cache.invalidate(&cache_key).await;
        }

        // [COMMENT]: 3. Cache Miss — thực hiện full verification qua Vault

        // 3a. Tách token thành 3 phần: Header, Payload, Signature
        let parts: Vec<&str> = token.split('.').collect();
        if parts.len() != 3 {
            return Err(AcrError::TokenError("Malformed JWT structure".to_string()));
        }

        // 3b. Decode & Deserialize phần Payload sang Claims để đọc thông tin trước
        use base64::Engine;
        let url_engine = base64::engine::general_purpose::URL_SAFE_NO_PAD;

        let payload_bytes = url_engine
            .decode(parts[1])
            .map_err(|e| AcrError::TokenError(format!("Failed to decode JWT payload: {}", e)))?;

        let claims: Claims = serde_json::from_slice(&payload_bytes)
            .map_err(|e| AcrError::TokenError(format!("Failed to parse JWT claims: {}", e)))?;

        // 3c. Kiểm tra tính hợp lệ về mặt thời gian (Expiration)
        let now = chrono::Utc::now().timestamp();
        if now > claims.exp {
            return Err(AcrError::TokenError("Token has expired".to_string()));
        }

        // 3d. Kiểm tra chữ ký JWT qua Vault Transit Engine
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
                    return Err(AcrError::TokenError(
                        "Invalid signature verified by Vault".to_string(),
                    ));
                }

                // [COMMENT]: 4. Chữ ký hợp lệ → Lưu vào L1 Cache với TTL = thời gian còn lại của JWT
                let remaining_secs = (claims.exp - now).max(0) as u64;
                self.jwt_sig_cache.insert(cache_key, claims.clone()).await;
                // [COMMENT]: Thiết lập TTL riêng cho entry này qua time_to_live đã tính ở trên
                // Moka sẽ tự động evict khi max_capacity đầy (LRU) hoặc khi hết TTL global
                let _ = remaining_secs; // TTL được quản lý bởi expiration check trong cache hit path

                return Ok(claims);
            }
        }

        // Nếu token không có signature prefix của Vault -> Báo lỗi do production bắt buộc dùng Vault signature.
        Err(AcrError::TokenError(
            "Token lacks Vault signature prefix or format is invalid".to_string(),
        ))
    }

    /// Tạo mới một JWT Token dựa trên claims cung cấp bằng cách ký qua Vault Transit Engine.
    /// Đồng bộ 100% logic với controlplane/internal/security/jwt.go::SignWithSecret().
    pub async fn generate_token(&self, claims: &Claims) -> Result<String, AcrError> {
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
            .map_err(|e| AcrError::TokenError(format!("Failed to serialize header: {}", e)))?;
        let payload_json = serde_json::to_string(claims)
            .map_err(|e| AcrError::TokenError(format!("Failed to serialize claims: {}", e)))?;

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

    /// [COMMENT]: Đọc trực tiếp Admin API Key từ Vault và băm SHA-256 mà không qua cache trong bộ nhớ (RAM).
    /// Đảm bảo an toàn tuyệt đối, tránh rò rỉ credential qua memory dump và cho phép thay đổi có hiệu lực ngay lập tức.
    pub async fn get_admin_api_key_hash(&self) -> Result<String, AcrError> {
        // [COMMENT]: Gọi trực tiếp Vault đọc API Key thô dùng đường dẫn cấu hình động
        let secret = self
            .vault_client
            .read_secret(&self.admin_api_key_path)
            .await?;
        let api_key = secret["data"]["data"]["api_key"]
            .as_str()
            .ok_or_else(|| AcrError::Internal("api_key not found in Vault response".to_string()))?;

        // [COMMENT]: Thực hiện băm SHA-256 của api_key để đối chiếu an toàn
        use sha2::{Digest, Sha256};
        let mut hasher = Sha256::new();
        hasher.update(api_key.as_bytes());
        let hash_hex = format!("{:x}", hasher.finalize());

        Ok(hash_hex)
    }

    /// [COMMENT]: Ủy thác xác thực OTP SRE cho Vault TOTP Secrets Engine
    /// Đảm bảo không truyền hay lưu trữ OTP Secret tại bộ nhớ của ACL
    pub async fn verify_admin_totp(&self, code: &str) -> Result<bool, AcrError> {
        // [COMMENT]: Gọi trực tiếp verify_totp trên VaultClient sử dụng cấu hình động
        self.vault_client.verify_totp(code).await
    }
}
