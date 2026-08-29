use crate::service::auth::AuthCredentials;
use futures_util::future::BoxFuture;
use serde_json::Value;
use std::error::Error;

// [COMMENT]: Type alias AppError chuẩn đại diện cho lỗi trả về trong toàn bộ Notification Service
pub type AppError = Box<dyn Error + Send + Sync>;

// [COMMENT]: Định nghĩa các trạng thái lỗi trong quá trình xác thực Trinity Token với ACR
#[derive(Debug)]
pub enum AuthError {
    Invalid,
    Unavailable(String),
    Protocol(String),
}

impl std::fmt::Display for AuthError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Invalid => formatter.write_str("credentials were rejected"),
            Self::Unavailable(message) => write!(
                formatter,
                "authentication dependency unavailable: {message}"
            ),
            Self::Protocol(message) => {
                write!(formatter, "authentication response invalid: {message}")
            }
        }
    }
}

impl Error for AuthError {}

// [COMMENT]: Định danh người dùng sau khi xác thực thành công qua ACR
#[derive(Debug)]
pub struct AuthenticatedPrincipal {
    pub id: String,
}

// [COMMENT]: Port giao tiếp xác thực token (được triển khai bởi RedisAuthBus kết nối tới ACR)
pub trait AuthVerifier: Send + Sync {
    fn verify<'a>(
        &'a self,
        credentials: AuthCredentials,
    ) -> BoxFuture<'a, Result<AuthenticatedPrincipal, AuthError>>;
}

// [COMMENT]: Port phát dữ liệu Realtime tới Centrifugo Server
pub trait RealtimePublisher: Send + Sync {
    fn publish<'a>(&'a self, channel: &'a str, data: Value) -> BoxFuture<'a, Result<(), AppError>>;
}

// [COMMENT]: Định nghĩa các tiền tố và helper tạo kênh kết nối Realtime Centrifugo
pub const JOB_CHANNEL_PREFIX: &str = "notifications";

// [COMMENT]: Sinh tên kênh notifications cá nhân cho user_id ("notifications:{user_id}")
pub fn notification_channel(user_id: &str) -> String {
    format!("{JOB_CHANNEL_PREFIX}:{user_id}")
}
