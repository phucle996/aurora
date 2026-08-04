// ======================================================================================================
// 📂 user/claims.rs — Claims JWT struct cho User thường
//
// 📌 VAI TRÒ:
//   - Định nghĩa Claims struct đồng bộ 100% với JSON payload sinh bởi Go controlplane.
//   - is_admin() phân biệt SRE (sub == "sre") khỏi user thường.
//   - TokenManager dùng chung toàn hệ thống được đặt riêng tại crate::token.
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
