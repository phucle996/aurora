pub mod bucket;
pub mod core;
pub mod credential; // [COMMENT]: Khai báo module credential quản trị User key của MinIO
pub mod delete;
pub mod delivery;
pub mod object_sts;
pub mod resize;

pub mod storage_proto {
    include!(concat!(env!("OUT_DIR"), "/storage.rs"));
}

pub use credential::{CredentialCreateExecutor, CredentialDeleteExecutor}; // [COMMENT]: Export các bộ thực thi tương ứng
pub use delete::BucketDeleteExecutor;
pub use delivery::dispatch_storage_job;
pub use object_sts::ObjectStsExecutor;
pub use resize::BucketResizeExecutor;
