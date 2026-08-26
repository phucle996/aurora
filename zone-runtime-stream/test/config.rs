use super::*;

#[test]
fn duration_budget_is_not_zero() {
    let config = Config {
        listen_addr: "127.0.0.1:8080".parse().unwrap(),
        zone_id: Uuid::new_v4(),
        victoria_metrics_url: "http://metrics".into(),
        victoria_logs_url: "http://logs".into(),
        max_connections: 1,
        max_fanout_groups: 1,
        max_buffered_events: 1,
        max_lifetime: Duration::from_secs(5),
        heartbeat: Duration::from_secs(1),
        query_interval: Duration::from_millis(100),
        max_snapshot: Duration::from_secs(1),
        max_event_bytes: 256 * 1024,
        max_log_lines: 100,
    };
    assert!(config.max_lifetime > config.heartbeat);
}
