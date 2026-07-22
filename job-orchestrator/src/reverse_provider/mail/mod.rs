pub mod l2_dispatcher;
pub mod reconciler;
pub mod reporter;
pub mod service;

// [COMMENT]: JO chỉ encode snapshot command rồi XADD Redis Job; module này không có Zone Redis client.
pub mod runtime_proto {
    include!(concat!(env!("OUT_DIR"), "/mail.runtime.v1.rs"));
}

#[cfg(test)]
#[path = "test/mod.rs"]
mod tests;
