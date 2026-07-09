// ======================================================================================================
// 📂 MODULE: acr/src/pkg/header.rs
//            Định nghĩa tập trung tên các HTTP Header dùng để inject qua biên Envoy sang các upstream microservices
// ======================================================================================================

pub const HEADER_X_USER_ID: &str = "x-user-id";
pub const HEADER_X_USER_NAME: &str = "x-user-name";
pub const HEADER_X_CLIENT_DEVICE_ID: &str = "x-client-device-id";
pub const HEADER_X_USER_ROLE_ID: &str = "x-user-role-id";
pub const HEADER_X_USER_LEVEL: &str = "x-user-level";
pub const HEADER_X_TENANT_ID: &str = "x-tenant-id";
pub const HEADER_X_ZONE_ID: &str = "x-zone-id";
pub const HEADER_X_WORKSPACE_ID: &str = "x-workspace-id";
