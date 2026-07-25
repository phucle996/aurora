pub mod reconciler;
pub mod reporter;
pub mod result_apply;
pub mod service;

// [COMMENT]: JO encode snapshot command lên Kafka; Cache Redis chỉ giữ reconciler/runtime soft state.
#[allow(dead_code)]
pub mod runtime_proto {
    include!(concat!(env!("OUT_DIR"), "/mail.runtime.v1.rs"));
}

#[cfg(test)]
#[path = "test/mod.rs"]
mod tests;
