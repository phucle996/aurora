pub mod db;
pub mod reconciler;
pub mod runtime_report;

// [COMMENT]: JO chỉ encode snapshot command rồi XADD Redis Job; module này không có Zone Redis client.
pub mod runtime_proto {
    include!(concat!(env!("OUT_DIR"), "/mail.runtime.v1.rs"));
}
