use uuid::Uuid;

#[test]
fn consumer_upsert_identity_matches_controlplane_uuid_v5_contract() {
    // [COMMENT]: Upsert replay tạo deterministic identity; delete retry dùng event ID mới và tombstone persist ID đó.
    let namespace = Uuid::parse_str("43de31a4-0c86-54e9-8384-47b33f541c28").unwrap();
    let upsert = Uuid::new_v5(
        &namespace,
        b"consumer:00000000-0000-0000-0000-000000000001:7:upsert:00000000-0000-0000-0000-000000000002",
    );
    assert_eq!(
        upsert,
        Uuid::parse_str("d9017c19-7a01-5ed8-a33b-5925217f2b6c").unwrap()
    );
}
