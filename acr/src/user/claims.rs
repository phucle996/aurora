// ======================================================================================================
// 📂 user/claims.rs — Claims JWT struct cho User thường & shared TokenManager
//
// 📌 VAI TRÒ:
//   - Định nghĩa Claims struct đồng bộ 100% với JSON payload sinh bởi Go controlplane.
//   - is_admin() phân biệt SRE (sub == "sre") khỏi user thường.
//   - TokenManager (Vault Transit + moka L1 cache) dùng chung toàn hệ thống.
// ======================================================================================================

use serde::{Deserialize, Serialize};

/// [COMMENT]: Claims — JWT payload chuẩn cho User và SRE Admin.
/// Chia sẻ cùng struct, phân biệt qua is_admin() (sub == "sre").
#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct Claims {
    // Subject (Username đăng nhập)
    #[serde(rename = "sub")]
    pub sub: String,

    // User UUID
    #[serde(rename = "uid")]
    pub uid: String,

    // Role UUID từ Go controlplane (ID-based authorization)
    #[serde(rename = "role_id", default)]
    pub role_id: String,

    // Level phân quyền (0 = cao nhất)
    #[serde(rename = "lvl", default)]
    pub lvl: i32,

    // Tenant Context (None = platform-level)
    #[serde(rename = "tnc", default)]
    pub tenant_id: Option<String>,

    // Zone ID
    #[serde(rename = "zid", default)]
    pub zone_id: Option<String>,

    // Access Key — binding JWT với Redis session
    #[serde(rename = "access_key")]
    pub access_key: String,

    // JWT ID (unique token identifier, chống replay)
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
    /// [COMMENT]: Trả về role_id dưới dạng Vec để tương thích với PolicyEvaluator / AuthContext.
    pub fn get_roles(&self) -> Vec<String> {
        if self.role_id.is_empty() {
            vec![]
        } else {
            vec![self.role_id.clone()]
        }
    }
}
