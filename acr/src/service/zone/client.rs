// ======================================================================================================
// 📂 MODULE: acr/src/service/zone/client.rs
//            NATS request-reply client gọi Controlplane để lấy Zone list
//            Transport: NATS request-reply
//            Encoding: Protobuf binary (prost) — không dùng gRPC/tonic
// ======================================================================================================

// [COMMENT]: Nhúng các proto struct được sinh từ core.rpc.proto bằng prost_build
// Không phụ thuộc tonic — chỉ dùng prost để encode/decode binary protobuf thuần
pub mod zone_proto {
    tonic::include_proto!("core.rpc");
}

use prost::Message;

/// Gọi NATS request-reply đến Controlplane để lấy toàn bộ danh sách Zone.
/// Transport: NATS | Encoding: protobuf binary
/// Trả về lỗi dạng String thuần thay vì tonic::Status để không lồng gRPC semantics vào NATS call.
pub async fn get_zone_list(nats_client: &async_nats::Client) -> Result<Vec<zone_proto::ZoneEntry>, String> {
    // [COMMENT]: Encode request protobuf thành binary — empty request struct
    let req = zone_proto::GetZoneListRequest {};
    let mut buf = Vec::new();
    req.encode(&mut buf)
        .map_err(|e| format!("zone.nats: failed to encode GetZoneListRequest: {}", e))?;

    // [COMMENT]: NATS request-reply, không phải gRPC — transport là NATS thuần
    let msg = nats_client
        .request("hierarchy.zone.get_zone_list".to_string(), buf.into())
        .await
        .map_err(|e| format!("zone.nats: NATS request failed: {}", e))?;

    // [COMMENT]: Decode response protobuf binary
    let resp = zone_proto::GetZoneListResponse::decode(msg.payload)
        .map_err(|e| format!("zone.nats: failed to decode GetZoneListResponse: {}", e))?;

    Ok(resp.zones)
}
