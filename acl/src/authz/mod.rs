pub mod evaluator;
pub mod policy;

use crate::error::AclError;
use serde::{Deserialize, Serialize};

// Ngữ cảnh nhận diện danh tính người dùng (đã xác thực thành công)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AuthContext {
    pub user_id: String,
    pub device_id: String,
    pub tenant_id: Option<String>,
    pub roles: Vec<String>,
}

// Ngữ cảnh thông tin chi tiết của Request đang đi qua Gateway
#[derive(Debug, Clone)]
pub struct RequestContext {
    // URL Path (ví dụ: "/api/v1/projects")
    pub path: String,
    // HTTP Method (GET, POST, PUT, DELETE, ...)
    pub method: String,
    // IP của client gửi request
    pub client_ip: String,
}

// Trait cốt lõi định nghĩa một Engine phê duyệt quyền truy cập.
// Giúp dễ dàng mở rộng thêm các loại kiểm tra quyền khác nhau sau này.
#[tonic::async_trait]
pub trait Authorizer: Send + Sync {
    // Tên của Engine (phục vụ logging và phân loại)
    fn name(&self) -> &'static str;

    // Trả về true nếu được phép đi tiếp, false nếu bị chặn
    async fn authorize(&self, auth_ctx: &AuthContext, req_ctx: &RequestContext) -> Result<bool, AclError>;
}
