use std::fmt;

// Định nghĩa mã lỗi của hệ thống ACL
#[derive(Debug)]
pub enum AclError {
    // Lỗi không tìm thấy Token hoặc Token hết hạn
    Unauthorized(String),
    // Lỗi từ chối truy cập (Bị chặn bởi Policy RBAC/ABAC)
    Forbidden(String),
    // Lỗi kết nối hoặc thực thi với Redis L2
    RedisError(String),
    // Lỗi liên quan đến mã hóa/giải mã JWT
    TokenError(String),
    // Lỗi cấu hình hệ thống
    ConfigError(String),
    // Lỗi nội bộ không xác định
    Internal(String),
}

impl std::error::Error for AclError {}

// Triển khai fmt::Display để in lỗi ra log dạng chuỗi
impl fmt::Display for AclError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            AclError::Unauthorized(msg) => write!(f, "Unauthorized: {}", msg),
            AclError::Forbidden(msg) => write!(f, "Forbidden: {}", msg),
            AclError::RedisError(msg) => write!(f, "Redis error: {}", msg),
            AclError::TokenError(msg) => write!(f, "Token error: {}", msg),
            AclError::ConfigError(msg) => write!(f, "Configuration error: {}", msg),
            AclError::Internal(msg) => write!(f, "Internal error: {}", msg),
        }
    }
}

// Chuyển đổi từ AclError sang tonic::Status để trả về cho Envoy hoặc IAM
impl From<AclError> for tonic::Status {
    fn from(err: AclError) -> Self {
        match err {
            AclError::Unauthorized(msg) => tonic::Status::unauthenticated(msg),
            AclError::Forbidden(msg) => tonic::Status::permission_denied(msg),
            AclError::RedisError(msg) => {
                // Log lỗi Redis chi tiết ra console nội bộ
                tracing::error!("Redis infrastructure error: {}", msg);
                tonic::Status::unavailable("Session storage is temporarily unavailable")
            }
            AclError::TokenError(msg) => {
                tonic::Status::unauthenticated(format!("Invalid token: {}", msg))
            }
            AclError::ConfigError(msg) => {
                tracing::error!("Configuration error: {}", msg);
                tonic::Status::internal("Internal system configuration error")
            }
            AclError::Internal(msg) => {
                tracing::error!("Internal error: {}", msg);
                tonic::Status::internal("Internal server error")
            }
        }
    }
}
