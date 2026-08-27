use super::is_user_visible_zone_status;

#[test]
fn zone_catalog_redis_payload_stays_wire_compatible_without_grpc_stubs() {
    use crate::infra::zone::zone_proto::GetZoneListResponse;
    use prost::Message;

    // One ZoneEntry: field 1=id, 2=code, 3=status, 4=name. Only the unused
    // client/server stubs are removed; Redis still carries these exact bytes.
    let payload = b"\x0a\x13\x0a\x01z\x12\x02vn\x1a\x06active\x22\x02VN";
    let decoded = GetZoneListResponse::decode(payload.as_slice()).expect("zone list protobuf");
    assert_eq!(decoded.zones.len(), 1);
    assert_eq!(decoded.zones[0].zone_id, "z");
    assert_eq!(decoded.zones[0].zone_code, "vn");
    assert_eq!(decoded.zones[0].status, "active");
    assert_eq!(decoded.zones[0].name, "VN");
    assert_eq!(decoded.encode_to_vec(), payload);
}

#[test]
fn user_catalog_only_exposes_active_or_draining_zones() {
    assert!(is_user_visible_zone_status("active"));
    assert!(is_user_visible_zone_status("draining"));

    for hidden_status in ["planned", "maintenance", "disabled", "inactive", ""] {
        assert!(
            !is_user_visible_zone_status(hidden_status),
            "{hidden_status} must not be exposed in the user catalog"
        );
    }
}
