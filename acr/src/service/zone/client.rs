// ======================================================================================================
// 📂 MODULE: acr/src/service/zone/client.rs
//            NATS client calls for Zone synchronization and resolution to Go Control Plane
// ======================================================================================================

// [COMMENT]: Sinh mã Rust từ gRPC protobuf definitions dựa trên package name 'core.rpc' tương thích Go
pub mod zone_proto {
    tonic::include_proto!("core.rpc");
}

use prost::Message;

// [COMMENT]: Gọi NATS request-reply đến Control Plane để lấy danh sách các Zone
pub async fn get_zone_list(nats_client: &async_nats::Client) -> Result<Vec<zone_proto::ZoneEntry>, tonic::Status> {
    let req = zone_proto::GetZoneListRequest {};
    let mut buf = Vec::new();
    req.encode(&mut buf)
        .map_err(|e| tonic::Status::internal(format!("Failed to encode request: {}", e)))?;

    match nats_client.request("core.zone.get_zone_list".to_string(), buf.into()).await {
        Ok(msg) => {
            let resp = zone_proto::GetZoneListResponse::decode(msg.payload)
                .map_err(|e| tonic::Status::internal(format!("Failed to decode GetZoneListResponse: {}", e)))?;
            Ok(resp.zones)
        }
        Err(e) => {
            Err(tonic::Status::internal(format!("NATS request failed: {}", e)))
        }
    }
}
