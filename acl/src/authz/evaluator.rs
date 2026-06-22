use std::sync::Arc;
use crate::error::AclError;
use crate::authz::{Authorizer, AuthContext, RequestContext};
use crate::authz::policy::PolicyStore;

// Triển khai Engine kiểm tra quyền dựa trên Vai trò (RBAC Engine)
pub struct RbacAuthorizer {
    policy_store: PolicyStore,
}

impl RbacAuthorizer {
    pub fn new() -> Self {
        Self {
            policy_store: PolicyStore::new(),
        }
    }
}

#[tonic::async_trait]
impl Authorizer for RbacAuthorizer {
    fn name(&self) -> &'static str {
        "RBAC_Authorizer"
    }

    async fn authorize(&self, auth_ctx: &AuthContext, req_ctx: &RequestContext) -> Result<bool, AclError> {
        // Tìm quy tắc bảo vệ của đường dẫn hiện tại
        let rule = match self.policy_store.find_rule(&req_ctx.path) {
            Some(r) => r,
            None => {
                // Nếu không có quy tắc nào khớp, cho phép truy cập (Bypass hoặc Default-Allow)
                return Ok(true);
            }
        };

        // Kiểm tra xem HTTP Method có được quy tắc cho phép không
        if !rule.methods.is_empty() && !rule.methods.contains(&req_ctx.method.as_str()) {
            return Ok(false);
        }

        // Kiểm tra đối khớp giữa vai trò của User và vai trò được phép truy cập
        for user_role in &auth_ctx.roles {
            if rule.allowed_roles.contains(&user_role.as_str()) {
                return Ok(true);
            }
        }

        Ok(false)
    }
}

// Minh họa thêm 1 Engine kiểm tra IP để chứng minh khả năng scale của khung
pub struct IPBlacklistAuthorizer {
    blacklist: Vec<&'static str>,
}

impl IPBlacklistAuthorizer {
    pub fn new() -> Self {
        Self {
            // Danh sách các IP bị chặn truy cập
            blacklist: vec!["192.168.1.99"],
        }
    }
}

#[tonic::async_trait]
impl Authorizer for IPBlacklistAuthorizer {
    fn name(&self) -> &'static str {
        "IP_Blacklist_Authorizer"
    }

    async fn authorize(&self, _auth_ctx: &AuthContext, req_ctx: &RequestContext) -> Result<bool, AclError> {
        if self.blacklist.contains(&req_ctx.client_ip.as_str()) {
            tracing::warn!("Blocked request from blacklisted IP: {}", req_ctx.client_ip);
            return Ok(false);
        }
        Ok(true)
    }
}

// Bộ điều phối trung tâm (Evaluator Engine)
pub struct PolicyEvaluator {
    // Chứa danh sách các Engine kiểm tra độc lập
    authorizers: Vec<Arc<dyn Authorizer>>,
}

impl PolicyEvaluator {
    pub fn new() -> Self {
        let rbac = Arc::new(RbacAuthorizer::new());
        let ip_check = Arc::new(IPBlacklistAuthorizer::new());
        
        Self {
            authorizers: vec![ip_check, rbac],
        }
    }

    // Đánh giá tuần tự qua tất cả các Authorizers
    pub async fn evaluate(&self, auth_ctx: &AuthContext, req_ctx: &RequestContext) -> Result<(), AclError> {
        for authz in &self.authorizers {
            let name = authz.name();
            match authz.authorize(auth_ctx, req_ctx).await {
                Ok(true) => {
                    tracing::debug!("Authorizer '{}' passed", name);
                }
                Ok(false) => {
                    tracing::warn!("Forbidden by authorizer '{}' for user '{}'", name, auth_ctx.user_id);
                    return Err(AclError::Forbidden(format!("Access denied by {}", name)));
                }
                Err(e) => {
                    return Err(e);
                }
            }
        }
        Ok(())
    }
}
