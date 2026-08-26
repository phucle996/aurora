use super::*;
use uuid::Uuid;

fn scope(module: &str) -> RuntimeScope {
    RuntimeScope {
        module: module.into(),
        resource_type: "instance".into(),
        resource_id: Uuid::new_v4(),
        owner_id: Uuid::new_v4(),
        workspace_id: Uuid::new_v4(),
        zone_id: Uuid::new_v4(),
        component_id: Some("broker".into()),
        panel_id: "metrics".into(),
        snapshot_seconds: 60,
    }
}

#[test]
fn query_builder_rejects_untrusted_label_syntax() {
    assert!(fixed_query(&scope("managed_service")).is_ok());
    assert!(fixed_query(&scope("managed\"_service")).is_err());
}

#[test]
fn component_regex_is_exactly_escaped() {
    let mut scoped = scope("managed_service");
    scoped.component_id = Some("broker.v1".into());
    let rendered = fixed_query(&scoped).unwrap();
    assert!(rendered.contains("aurora_component_id=~\"broker\\.v1\""));
}

#[test]
fn oversized_payload_is_reduced_to_digest() {
    let value = Value::String("x".repeat(32));
    let bounded = bounded_value(value, 8);
    assert_eq!(bounded["truncated"], true);
    assert!(bounded["sha256"].is_string());
}

#[test]
fn victoria_logs_ndjson_is_decoded_as_a_bounded_record_array() {
    let response = b"{\"_msg\":\"one\"}\n{\"_msg\":\"two\"}\n";
    let records = decode_victoria_payload("logs", response).unwrap();
    assert_eq!(records.as_array().unwrap().len(), 2);
    assert_eq!(records[1]["_msg"], "two");
}
