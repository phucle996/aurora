use crate::config::Config;
use crate::observability::logger::{LogFields, Logger};
use crate::observability::metrics::MetricsManager;
use std::hash::{Hash, Hasher};
use std::time::Duration;

/// Redelivers the exact current Managed Service command when the durable
/// outbox marker has been left pending/processing past the bounded stale
/// window. It never rewrites protected payload, event identity or fences.
///
/// PostgreSQL row locks provide the HA coordination boundary: each replica
/// claims a disjoint batch with SKIP LOCKED, then the status update is emitted
/// through the existing WAL/CDC dispatcher. This deliberately does not create
/// a second outbox record or call Kafka directly.
pub async fn run_periodic_managed_service_reconciliation(config: Config) {
    let workflow = &config.workflows.managed_service;
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    crate::config::get_node_hostname().hash(&mut hasher);
    "managed-service-reconciliation".hash(&mut hasher);
    let jitter_window_ms = workflow.reconcile_interval_secs.min(60) * 1_000;
    tokio::time::sleep(Duration::from_millis(hasher.finish() % jitter_window_ms)).await;
    let mut interval = tokio::time::interval(Duration::from_secs(workflow.reconcile_interval_secs));
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);

    loop {
        interval.tick().await;
        let mut dispatch_postgres = config.postgres.clone();
        dispatch_postgres.database_url = config.postgres.dispatch_database_url.clone();
        let mut client = match crate::infra::postgres::connect(
            &dispatch_postgres,
            "managed_service.reconcile.postgres",
        )
        .await
        {
            Ok(client) => client,
            Err(error) => {
                Logger::sys_error(
                    "managed_service.reconcile.connect",
                    "Managed Service reconciler could not connect to PostgreSQL; retrying on next tick",
                    &error.to_string(),
                );
                continue;
            }
        };

        let transaction = match client.transaction().await {
            Ok(transaction) => transaction,
            Err(error) => {
                Logger::sys_error(
                    "managed_service.reconcile.transaction",
                    "Managed Service reconciler could not start transaction",
                    &error.to_string(),
                );
                continue;
            }
        };

        let rows = transaction
            .query(
                "WITH due AS ( \
                     SELECT event_id \
                     FROM managed_service.managed_service_outbox_records \
                     WHERE status IN ('PENDING', 'PROCESSING') \
                       AND available_at <= NOW() \
                       AND updated_at < NOW() - ($1::bigint * INTERVAL '1 second') \
                     ORDER BY updated_at, event_id \
                     FOR UPDATE SKIP LOCKED \
                     LIMIT $2 \
                 ) \
                 UPDATE managed_service.managed_service_outbox_records outbox \
                 SET status = 'PENDING', available_at = NOW(), updated_at = NOW() \
                 FROM due \
                 WHERE outbox.event_id = due.event_id \
                 RETURNING outbox.event_id",
                &[
                    &(workflow.reconcile_stale_secs as i64),
                    &workflow.reconcile_batch_size,
                ],
            )
            .await;
        let rows = match rows {
            Ok(rows) => rows,
            Err(error) => {
                let _ = transaction.rollback().await;
                Logger::sys_error(
                    "managed_service.reconcile.scan",
                    "Managed Service reconciler scan failed; retrying on next tick",
                    &error.to_string(),
                );
                continue;
            }
        };

        if let Err(error) = transaction.commit().await {
            Logger::sys_error(
                "managed_service.reconcile.commit",
                "Managed Service reconciler status reset did not commit",
                &error.to_string(),
            );
            continue;
        }

        if !rows.is_empty() {
            for _ in &rows {
                MetricsManager::record_managed_service_redelivery();
            }
            Logger::sys_info_with_fields(
                "managed_service.reconcile",
                "MANAGED_SERVICE_OUTBOX_REDELIVERY_SCHEDULED",
                "Stale Managed Service delivery markers were reset for WAL/CDC redispatch",
                LogFields {
                    source_domain: Some("MANAGED_SERVICE"),
                    retryable: Some(true),
                    outcome: Some("pending"),
                    ..LogFields::default()
                },
            );
        }
    }
}
