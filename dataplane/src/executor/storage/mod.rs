pub mod bucket;
pub mod core;
pub mod credential; // [COMMENT]: Khai báo module credential quản trị User key của MinIO
pub mod delete;
pub mod delivery;
pub mod object_signer;
pub mod resize;
pub mod sizes_syncer;

pub mod storage_proto {
    include!(concat!(env!("OUT_DIR"), "/storage.rs"));
}

pub use core::StorageWorkloadMonitor;
pub use credential::{CredentialCreateExecutor, CredentialDeleteExecutor}; // [COMMENT]: Export các bộ thực thi tương ứng
pub use delete::BucketDeleteExecutor;
pub use delivery::dispatch_storage_job;
pub use object_signer::ObjectPresignExecutor;
pub use resize::BucketResizeExecutor;
pub use sizes_syncer::StorageSizesSyncer;
