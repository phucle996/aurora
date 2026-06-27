// Định nghĩa cấu trúc rule phục vụ kiểm tra quyền
#[derive(Debug, Clone)]
pub struct PolicyRule {
    // Tiền tố của path (ví dụ: "/api/v1/admin")
    pub path_prefix: &'static str,
    // Các phương thức được phép (ví dụ: ["GET", "POST"])
    pub methods: Vec<&'static str>,
    // Các vai trò được phép truy cập (ví dụ: ["admin", "super-admin"])
    pub allowed_roles: Vec<&'static str>,
}

pub struct PolicyStore {
    rules: Vec<PolicyRule>,
}

impl PolicyStore {
    // Khởi tạo các policy mặc định cho hệ thống
    pub fn new() -> Self {
        let rules = vec![
            // Quy tắc cho tài nguyên Admin
            PolicyRule {
                path_prefix: "/api/v1/admin",
                methods: vec!["GET", "POST", "PUT", "DELETE"],
                allowed_roles: vec!["admin", "super_admin"],
            },
            // Quy tắc cho tài nguyên Tenant-specific
            PolicyRule {
                path_prefix: "/api/v1/tenant",
                methods: vec!["GET", "POST"],
                allowed_roles: vec!["tenant_member", "admin"],
            },
            // Quy tắc chung cho toàn bộ end-users đã đăng nhập
            PolicyRule {
                path_prefix: "/api/v1/users",
                methods: vec!["GET"],
                allowed_roles: vec!["user", "tenant_member", "admin"],
            },
        ];
        
        Self { rules }
    }

    // Tìm kiếm quy tắc phù hợp nhất với request hiện tại
    pub fn find_rule(&self, path: &str) -> Option<&PolicyRule> {
        // Tìm quy tắc có tiền tố dài nhất khớp với path của request (Longest prefix match)
        self.rules.iter()
            .filter(|rule| path.starts_with(rule.path_prefix))
            .max_by_key(|rule| rule.path_prefix.len())
    }
}
