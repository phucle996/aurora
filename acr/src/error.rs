use std::fmt;

// Định nghĩa mã lỗi của hệ thống ACR
#[derive(Debug)]
pub enum AcrError {
    // Input violates a workflow invariant before any state is issued.
    InvalidArgument(String),
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

impl std::error::Error for AcrError {}

// Triển khai fmt::Display để in lỗi ra log dạng chuỗi
impl fmt::Display for AcrError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            AcrError::InvalidArgument(msg) => write!(f, "Invalid argument: {}", msg),
            AcrError::Unauthorized(msg) => write!(f, "Unauthorized: {}", msg),
            AcrError::Forbidden(msg) => write!(f, "Forbidden: {}", msg),
            AcrError::RedisError(msg) => write!(f, "Redis error: {}", msg),
            AcrError::TokenError(msg) => write!(f, "Token error: {}", msg),
            AcrError::ConfigError(msg) => write!(f, "Configuration error: {}", msg),
            AcrError::Internal(msg) => write!(f, "Internal error: {}", msg),
        }
    }
}

// Chuyển đổi từ AcrError sang tonic::Status để trả về cho Envoy hoặc IAM
impl From<AcrError> for tonic::Status {
    fn from(err: AcrError) -> Self {
        match err {
            AcrError::InvalidArgument(msg) => tonic::Status::invalid_argument(msg),
            AcrError::Unauthorized(msg) => tonic::Status::unauthenticated(msg),
            AcrError::Forbidden(msg) => tonic::Status::permission_denied(msg),
            AcrError::RedisError(msg) => {
                // Log lỗi Redis chi tiết ra console nội bộ
                tracing::error!("Redis infrastructure error: {}", msg);
                tonic::Status::unavailable("Session storage is temporarily unavailable")
            }
            AcrError::TokenError(msg) => {
                tonic::Status::unauthenticated(format!("Invalid token: {}", msg))
            }
            AcrError::ConfigError(msg) => {
                tracing::error!("Configuration error: {}", msg);
                tonic::Status::internal("Internal system configuration error")
            }
            AcrError::Internal(msg) => {
                tracing::error!("Internal error: {}", msg);
                tonic::Status::internal("Internal server error")
            }
        }
    }
}
