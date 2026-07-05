pub mod backpressure;
pub mod deadman;
pub mod processor;
pub mod query;

/// [COMMENT]: Protobuf auto-generated struct, chia sẻ giữa tất cả sub-modules của listener
pub mod zone_proto {
    include!(concat!(env!("OUT_DIR"), "/zone.rs"));
}

// [COMMENT]: Re-export các hàm public để giữ nguyên interface cũ cho zone/mod.rs gọi
pub use backpressure::run_backpressure_listener;
pub use query::run_metadata_query_listener;
