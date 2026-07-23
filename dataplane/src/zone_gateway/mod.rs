pub mod listener;
pub mod reconciler;
pub mod reporter;

use crate::config::Config;
use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::ZoneKvStore;
use std::sync::Arc;

/// [COMMENT]: Protobuf auto-generated struct import
pub mod zone_proto {
    include!(concat!(env!("OUT_DIR"), "/zone.rs"));
}

/// [COMMENT]: Cổng thông tin và Đồng bộ hóa Trạng thái Zone (ZoneStatusGateway)
/// Facade wrapper để giữ nguyên interface cũ cho app.rs gọi.
pub struct ZoneStatusGateway;

impl ZoneStatusGateway {
    /// [COMMENT]: Khởi chạy task tổng hợp tài nguyên của cả cụm Dataplane và đẩy lên Kafka.
    pub fn start_zone_gateway(
        zone_kv: Arc<ZoneKvStore>,
        kafka: Arc<KafkaTransport>,
        config: Arc<Config>,
    ) {
        reporter::start_zone_gateway(zone_kv, kafka, config);
    }

    /// [COMMENT]: Lắng nghe các sự kiện cập nhật cấu hình thời gian thực (CDC events) từ Platform L1
    pub fn start_metadata_event_listener(
        zone_kv: Arc<ZoneKvStore>,
        kafka: Arc<KafkaTransport>,
        config: Arc<Config>,
    ) {
        listener::start_metadata_event_listener(zone_kv, kafka, config);
    }
}
