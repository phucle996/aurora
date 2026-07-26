pub mod access;
pub mod bucket;
pub mod core;
pub mod credential; // [COMMENT]: Khai báo module credential quản trị User key của MinIO
pub mod delete;
pub mod delivery;
pub mod resize;

// Dataplane compiles the shared storage schema for command compatibility, but
// must never read the Central Auth-State Redis projection. The access-record
// message is therefore intentionally unused in this process.
#[allow(dead_code)]
pub mod storage_proto {
    include!(concat!(env!("OUT_DIR"), "/storage.rs"));
}

pub use access::StorageAccessPrepareExecutor;
pub use credential::{CredentialCreateExecutor, CredentialDeleteExecutor}; // [COMMENT]: Export các bộ thực thi tương ứng
pub use delete::BucketDeleteExecutor;
pub use delivery::dispatch_storage_job;
pub use resize::BucketResizeExecutor;
