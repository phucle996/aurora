use super::pgoutput::DecodedRow;
use super::worker::{ChangefeedWorker, PermanentChangeError};
use crate::infra::kafka::transport_proto::{ZoneMetadataSnapshotV1, ZoneServiceDesiredStateV1};

impl ChangefeedWorker {
    pub(super) async fn process_zone_config_change(
        &self,
        fields: &DecodedRow,
        table_name: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let zone_id = if table_name == "zones" {
            fields.text("id").unwrap_or_default().to_string()
        } else {
            fields.text("zone_id").unwrap_or_default().to_string()
        };
        if uuid::Uuid::parse_str(&zone_id).is_err() {
            return Err(Box::new(PermanentChangeError(
                "monitored Zone change has an invalid zone UUID".to_string(),
            )));
        }

        let mut cache_update = None;
        if table_name == "zone_services" {
            let service_type = fields.text("service_type").unwrap_or_default().to_string();
            let enabled = fields
                .text("desired_state")
                .is_some_and(|value| value == "t" || value == "true");
            let key = (zone_id.clone(), service_type.clone());
            if self
                .desired_state_cache
                .lock()
                .map_err(|_| "desired-state cache lock poisoned")?
                .get(&key)
                .is_some_and(|cached| *cached == enabled)
            {
                return Ok(());
            }
            cache_update = Some((key, enabled));
        }

        // [COMMENT]: Luôn publish full snapshot; compacted topic không phụ thuộc thứ tự delta khi pod cold-start.
        let (status, services) =
            crate::zone_state::store::query_zone_metadata(&self.metadata_client, &zone_id).await?;
        let event_id = uuid::Uuid::new_v4();
        let snapshot = ZoneMetadataSnapshotV1 {
            event_id: event_id.as_bytes().to_vec(),
            zone_id: uuid::Uuid::parse_str(&zone_id)?.as_bytes().to_vec(),
            status,
            services: services
                .into_iter()
                .map(|(service_type, enabled)| ZoneServiceDesiredStateV1 {
                    service_type,
                    enabled,
                })
                .collect(),
            observed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
            schema_version: 1,
        };
        self.kafka
            .publish_message(
                &self.kafka.metadata_topic(&zone_id),
                zone_id.as_bytes(),
                &snapshot,
            )
            .await
            .map_err(std::io::Error::other)?;

        // [COMMENT]: Cache chỉ tiến sau acks=all; publish lỗi sẽ để WAL replay thử lại.
        if let Some((key, enabled)) = cache_update {
            self.desired_state_cache
                .lock()
                .map_err(|_| "desired-state cache lock poisoned")?
                .insert(key, enabled);
        }

        Ok(())
    }
}
