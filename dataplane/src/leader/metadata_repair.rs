use std::sync::Arc;
use std::time::Duration;

use super::session::ZoneLeaderSession;
use crate::config::Config;
use crate::infra::kafka::transport_proto::ZoneMetadataQueryV1;
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::Logger;

/// [COMMENT]: Cold-start và repair query thuộc leader session; không cần lease con cho từng duty.
pub(crate) async fn run_zone_metadata_repair_publisher(
    session: ZoneLeaderSession,
    kafka: Arc<KafkaTransport>,
    config: Arc<Config>,
) {
    loop {
        if session.permits_external_side_effect().await {
            let request_id = uuid::Uuid::new_v4();
            let query = ZoneMetadataQueryV1 {
                request_id: request_id.as_bytes().to_vec(),
                zone_id: uuid::Uuid::parse_str(&config.zone_id)
                    .map(|value| value.as_bytes().to_vec())
                    .unwrap_or_default(),
                requested_at_unix_ms: chrono::Utc::now().timestamp_millis(),
                schema_version: 1,
            };
            if let Err(error) = kafka
                .publish_message(
                    &kafka.metadata_query_topic(),
                    config.zone_id.as_bytes(),
                    &query,
                )
                .await
            {
                Logger::sys_warn(
                    "leader.zone_metadata_repair_publisher",
                    "Không publish được Zone metadata repair query",
                    &error,
                );
            }
        }

        // [COMMENT]: Shared compacted snapshot tự repair realtime; full query chỉ cold-start/hourly.
        if !session
            .wait(Duration::from_secs(60 * 60 + rand::random::<u64>() % 30))
            .await
        {
            return;
        }
    }
}
