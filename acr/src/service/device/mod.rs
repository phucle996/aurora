// ======================================================================================================
// 📂 MODULE: acl/src/service/device/mod.rs
//            Khai báo các module con của ngữ cảnh Thiết Bị (Device Context)
// ======================================================================================================

pub mod revoke_device;

// Re-export để bên ngoài (như release_session) sử dụng trực tiếp
pub use revoke_device::revoke_user_sessions_by_devices;
