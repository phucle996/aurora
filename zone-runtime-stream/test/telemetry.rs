use super::*;

#[test]
fn prometheus_contract_has_no_runtime_scope_labels() {
    let telemetry = Telemetry::default();
    telemetry.connection_opened();
    telemetry.source_query();
    let output = telemetry.prometheus();
    assert!(output.contains("connections_active 1"));
    assert!(!output.contains("resource_id"));
    assert!(!output.contains("owner_id"));
}

#[test]
fn close_counters_never_underflow() {
    let telemetry = Telemetry::default();
    telemetry.connection_closed();
    telemetry.fanout_group_closed();
    let output = telemetry.prometheus();
    assert!(output.contains("connections_active 0"));
    assert!(output.contains("fanout_groups_active 0"));
}
