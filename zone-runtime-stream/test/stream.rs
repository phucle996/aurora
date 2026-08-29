use super::*;

fn test_runtime() -> RuntimeStream {
    let config = Config {
        listen_addr: "127.0.0.1:0".parse().unwrap(),
        zone_id: Uuid::new_v4(),
        victoria_metrics_url: "http://metrics".into(),
        victoria_logs_url: "http://logs".into(),
        max_connections: 4,
        max_fanout_groups: 4,
        max_buffered_events: 4,
        max_lifetime: Duration::from_secs(5),
        heartbeat: Duration::from_secs(1),
        query_interval: Duration::from_secs(1),
        max_snapshot: Duration::from_secs(5),
        max_event_bytes: 1024,
        max_log_lines: 10,
    };
    let source = VictoriaSource::new(&config).unwrap();
    RuntimeStream::new(config, source, CancellationToken::new())
}

fn test_scope(zone_id: Uuid) -> RuntimeScope {
    RuntimeScope {
        module: "mail".into(),
        resource_type: "consumer".into(),
        resource_id: Uuid::new_v4(),
        resource_name: None,
        owner_id: Uuid::new_v4(),
        workspace_id: Uuid::new_v4(),
        zone_id,
        component_id: None,
        panel_id: "health".into(),
        snapshot_seconds: 5,
    }
}

#[test]
fn victoria_failure_taxonomy_is_sanitized() {
    assert_eq!(
        source_error_code(&SourceError::ResponseTooLarge),
        "VICTORIA_RESPONSE_TOO_LARGE"
    );
    assert_eq!(
        source_error_code(&SourceError::Decode),
        "VICTORIA_RESPONSE_INVALID"
    );
    assert_eq!(
        source_error_code(&SourceError::Scope),
        "RUNTIME_SCOPE_INVALID"
    );
}

#[tokio::test]
async fn cleanup_rechecks_client_count_while_holding_subscription_map_lock() {
    let runtime = test_runtime();
    let scope = test_scope(runtime.zone_id());
    let key = SubscriptionKey {
        scope: scope.clone(),
    };
    let subscription = Subscription::new(4);
    runtime
        .subscriptions
        .lock()
        .await
        .insert(key.clone(), subscription.clone());

    subscription
        .clients
        .store(1, std::sync::atomic::Ordering::Release);
    runtime.remove_if_unused(&scope, &subscription).await;

    assert!(runtime.subscriptions.lock().await.contains_key(&key));
    assert!(!subscription.stop.is_cancelled());
}
