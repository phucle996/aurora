use crate::config::Config;
use crate::infra::kafka::KafkaTransport;
use crate::observability::logger::Logger;
use pgwire_replication::{Lsn, ReplicationClient, ReplicationConfig, ReplicationEvent};
use std::collections::HashMap;
use std::sync::Arc;
use tokio_util::sync::CancellationToken;

use super::connection::parse_pg_config;
use super::pgoutput::{
    parse_insert_message, parse_relation_message, parse_update_message, read_u32, PgOutputRelation,
};

#[derive(Debug)]
pub(super) struct PermanentChangeError(pub(super) String);

impl std::fmt::Display for PermanentChangeError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl std::error::Error for PermanentChangeError {}

/// ChangefeedWorker duy trì logical replication và chỉ advance LSN sau durable
/// Kafka publication hoặc một terminal outcome đã được phân loại rõ.
pub struct ChangefeedWorker {
    pub(super) config: Config,
    pub(super) kafka: Arc<KafkaTransport>,
    pub(super) metadata_client: Arc<tokio_postgres::Client>,
    /// Managed Service dispatch is the only changefeed path that writes back to
    /// Controlplane PostgreSQL. It uses a separate writable session from the
    /// read-only metadata client; deployment SQL grants must restrict this path
    /// to the outbox transition and the worker never uses it for result settlement.
    pub(super) managed_service_outbox_writer: Option<Arc<tokio_postgres::Client>>,
    /// [COMMENT]: Cache desired_state của từng (zone_id, service_type) — dùng để phát hiện thay đổi thực sự.
    /// Persist qua các lần reconnect (không reset khi replication stream ngắt/reconnect).
    /// Key: (zone_id, service_type), Value: desired_state hiện tại (true = enabled).
    pub(super) desired_state_cache: std::sync::Mutex<HashMap<(String, String), bool>>,
}

impl ChangefeedWorker {
    /// Khởi tạo worker và bootstrap desired_state_cache từ DB.
    /// Tránh publish spurious Zone snapshots khi replay changefeed sau restart.
    pub async fn new(
        config: Config,
        kafka: Arc<KafkaTransport>,
    ) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        // [COMMENT]: Bootstrap snapshot từ DB để khởi tạo cache trước khi nhận WAL events.
        // Tránh publish false-positive khi JO restart và WAL replay các event cũ.
        let metadata_client =
            crate::infra::postgres::connect(&config.postgres, "changefeed.metadata_postgres")
                .await?;
        metadata_client
            .batch_execute("SET default_transaction_read_only = on")
            .await?;
        let metadata_client = Arc::new(metadata_client);
        let snapshot =
            crate::zone_state::store::query_all_zone_services_enabled(&metadata_client).await?;

        let managed_service_outbox_writer = if config
            .workflows
            .changefeed
            .sources
            .iter()
            .any(|source| source == "managed_service.managed_service_outbox_records")
        {
            if config.postgres.dispatch_database_url.trim().is_empty() {
                return Err(
                    "managed service CDC source requires a Vault-resolved dispatch PostgreSQL URL"
                        .into(),
                );
            }
            let mut dispatch_config = config.postgres.clone();
            dispatch_config.database_url = config.postgres.dispatch_database_url.clone();
            // This session owns only the post-ACK outbox transition. It must not
            // be reused by the result worker, which owns terminal settlement.
            let writer = crate::infra::postgres::connect(
                &dispatch_config,
                "changefeed.managed_service_outbox_writer",
            )
            .await?;
            let transaction_read_only: String = writer
                .query_one("SHOW transaction_read_only", &[])
                .await?
                .get(0);
            if transaction_read_only.eq_ignore_ascii_case("on") {
                return Err(
                    "managed service outbox writer is attached to a read-only PostgreSQL role"
                        .into(),
                );
            }
            Some(Arc::new(writer))
        } else {
            None
        };

        // [COMMENT]: Flatten từ HashMap<zone_id, HashMap<svc_type, bool>>
        // sang HashMap<(zone_id, svc_type), bool> để lookup O(1).
        let mut cache: HashMap<(String, String), bool> = HashMap::new();
        for (zone_id, services) in snapshot {
            for (svc_type, enabled) in services {
                cache.insert((zone_id.clone(), svc_type), enabled);
            }
        }

        Logger::sys_info(
            "changefeed.cache_bootstrap",
            &format!(
                "ChangefeedWorker: Bootstrap desired_state_cache thành công — {} entries.",
                cache.len()
            ),
        );

        Ok(Self {
            config,
            kafka,
            metadata_client,
            managed_service_outbox_writer,
            desired_state_cache: std::sync::Mutex::new(cache),
        })
    }

    /// Khởi chạy luồng stream nhận và phân phối sự kiện từ WAL theo giao thức push-based.
    pub async fn run(&self, shutdown: CancellationToken) -> Result<(), Box<dyn std::error::Error>> {
        Logger::sys_info(
            "changefeed.run",
            "ChangefeedWorker: Khởi chạy logical changefeed với cơ chế tự động reconnect...",
        );

        let mut retry_delay = 1_u64;
        loop {
            let session = tokio::select! {
                biased;
                _ = shutdown.cancelled() => {
                    Logger::sys_info(
                        "changefeed.shutdown",
                        "Changefeed cancellation observed; current WAL record remains unsettled unless its full durable boundary already completed",
                    );
                    return Ok(());
                }
                result = self.run_replication_stream() => result,
            };
            let delay = if let Err(error) = session {
                let jitter = crate::config::get_node_hostname()
                    .bytes()
                    .fold(0_u64, |sum, value| sum.wrapping_add(u64::from(value)))
                    % 3;
                Logger::sys_error(
                    "changefeed.run",
                    "Changefeed session failed; reconnecting with bounded jitter",
                    &error.to_string(),
                );
                let delay = std::time::Duration::from_secs(retry_delay + jitter);
                retry_delay = (retry_delay * 2).min(30);
                delay
            } else {
                Logger::sys_info("changefeed.run", "Changefeed session ended; reconnecting");
                retry_delay = 1;
                std::time::Duration::from_secs(1)
            };
            tokio::select! {
                biased;
                _ = shutdown.cancelled() => return Ok(()),
                _ = tokio::time::sleep(delay) => {}
            }
        }
    }

    /// Kết nối và chạy stream logical replication cho một phiên kết nối cụ thể.
    async fn run_replication_stream(&self) -> Result<(), Box<dyn std::error::Error>> {
        // Session-scoped advisory lease. Only the holder consumes the logical
        // replication slot; a crashed pod releases it when PostgreSQL closes
        // this connection, and a retry creates a fresh lease session after a
        // transient network failure.
        let leadership_client = crate::infra::postgres::connect(
            &self.config.postgres,
            "changefeed.leadership_postgres",
        )
        .await?;
        let leader = leadership_client
            .query_one(
                "SELECT pg_try_advisory_lock(hashtextextended($1, 0))",
                &[&self.config.workflows.changefeed.slot_name],
            )
            .await?
            .get::<_, bool>(0);
        if !leader {
            // Standbys must not open competing replication sessions. Returning
            // after a bounded wait lets the outer reconnect loop re-check the
            // lease while keeping failover latency below the retry budget.
            Logger::sys_info(
                "changefeed.leader",
                "Another Job Orchestrator replica owns the logical replication lease",
            );
            tokio::time::sleep(std::time::Duration::from_secs(5)).await;
            return Ok(());
        }
        self.observe_managed_service_backlog().await;

        let (pg_host, pg_port, pg_user, pg_password, pg_db) =
            parse_pg_config(&self.config.postgres.database_url)
                .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidInput, e))?;

        Logger::sys_info(
            "changefeed.run",
            "Connecting PostgreSQL logical replication stream",
        );

        let config = ReplicationConfig {
            host: pg_host,
            port: pg_port,
            user: pg_user,
            password: pg_password,
            database: pg_db,
            slot: self.config.workflows.changefeed.slot_name.clone(),
            publication: self.config.workflows.changefeed.publication_name.clone(),
            start_lsn: Lsn::ZERO,
            tls: self.config.postgres.replication_tls(),
            status_interval: std::time::Duration::from_millis(
                self.config.workflows.changefeed.status_interval_ms,
            ),
            idle_wakeup_interval: std::time::Duration::from_secs(
                self.config.workflows.changefeed.idle_wakeup_secs,
            ),
            buffer_events: self.config.workflows.changefeed.buffer_events,
            ..Default::default()
        };

        let mut client = tokio::time::timeout(
            std::time::Duration::from_secs(self.config.postgres.connect_timeout_secs),
            ReplicationClient::connect(config),
        )
        .await
        .map_err(|_| {
            std::io::Error::new(
                std::io::ErrorKind::TimedOut,
                "PostgreSQL logical replication connect timed out",
            )
        })??;
        let mut relation_map: HashMap<u32, PgOutputRelation> = HashMap::new();
        let mut next_backlog_sample =
            tokio::time::Instant::now() + std::time::Duration::from_secs(10);

        Logger::sys_info(
            "changefeed.run",
            &format!(
                "Listening on logical replication slot {}",
                self.config.workflows.changefeed.slot_name
            ),
        );

        while let Some(event) = client.recv().await? {
            match event {
                ReplicationEvent::XLogData { wal_end, data, .. } => {
                    if data.is_empty() {
                        client.update_applied_lsn(wal_end);
                        continue;
                    }

                    let tag = data[0];
                    let outcome: Result<(), Box<dyn std::error::Error>> =
                        async {
                            match tag {
                                b'R' => {
                                    let rel = parse_relation_message(&data)
                                        .map_err(PermanentChangeError)?;
                                    Logger::sys_info(
                                        "changefeed.relation",
                                        &format!(
                                            "Schema table {}.{} (ID: {}) được cập nhật: {} columns",
                                            rel.schema_name,
                                            rel.relation_name,
                                            rel.relation_id,
                                            rel.columns.len()
                                        ),
                                    );
                                    relation_map.insert(rel.relation_id, rel);
                                    Ok(())
                                }
                                b'I' | b'U' => {
                                    let mut offset = 1;
                                    let relation_id = read_u32(&data, &mut offset)
                                        .map_err(PermanentChangeError)?;
                                    let rel = relation_map.get(&relation_id).ok_or_else(|| {
                                        std::io::Error::other(format!(
                                        "relation {relation_id} is unknown; reconnect before ACK"
                                    ))
                                    })?;
                                    // [COMMENT]: Match đủ schema.table; không nhận nhầm outbox cùng tên ở domain khác.
                                    let is_monitored =
                                        self.config.workflows.changefeed.sources.iter().any(
                                            |source| {
                                                if let Some((schema_name, table_name)) =
                                                    source.split_once('.')
                                                {
                                                    rel.schema_name == schema_name
                                                        && rel.relation_name == table_name
                                                } else {
                                                    rel.relation_name == source.as_str()
                                                }
                                            },
                                        );

                                    if is_monitored {
                                        let fields_res = if tag == b'I' {
                                            parse_insert_message(&data, &rel.columns)
                                        } else {
                                            parse_update_message(&data, &rel.columns)
                                        };

                                        match fields_res {
                                            Ok(fields) => {
                                                if rel.relation_name == "zones"
                                                    || rel.relation_name == "zone_services"
                                                {
                                                    // [COMMENT]: Bỏ tham số tag — so sánh bằng cache thay vì heuristic.
                                                    self.process_zone_config_change(
                                                        &fields,
                                                        &rel.relation_name,
                                                    )
                                                    .await?;
                                                } else if tag == b'I' {
                                                    // Source schema is authoritative routing metadata;
                                                    // never infer the owner domain from job_topic.
                                                    self.process_outbox_record(
                                                        &fields,
                                                        &rel.schema_name,
                                                        wal_end,
                                                    )
                                                    .await?;
                                                } else if tag == b'U'
                                                    && rel.schema_name == "managed_service"
                                                    && rel.relation_name
                                                        == "managed_service_outbox_records"
                                                    && fields.text("status") == Some("PENDING")
                                                {
                                                    // A retry/manual replay changes only the
                                                    // durable outbox delivery fields. The same
                                                    // encoder is used so ciphertext/event identity
                                                    // cannot drift between INSERT and UPDATE WAL.
                                                    self.process_outbox_record(
                                                        &fields,
                                                        &rel.schema_name,
                                                        wal_end,
                                                    )
                                                    .await?;
                                                }
                                            }
                                            Err(err) => {
                                                return Err(PermanentChangeError(format!(
                                                    "pgoutput {} parse failed for {}.{}: {err}",
                                                    tag as char, rel.schema_name, rel.relation_name
                                                ))
                                                .into());
                                            }
                                        }
                                    }
                                    Ok(())
                                }
                                _ => Ok(()),
                            }
                        }
                        .await;

                    match outcome {
                        Ok(()) => {}
                        Err(error) if error.is::<PermanentChangeError>() => {
                            self.quarantine_change(wal_end, tag, &data, &error.to_string())
                                .await?;
                        }
                        // Transient PostgreSQL/Kafka errors retain the current LSN.
                        Err(error) => return Err(error),
                    }
                    client.update_applied_lsn(wal_end);
                }
                ReplicationEvent::KeepAlive {
                    wal_end,
                    reply_requested: true,
                    ..
                } => client.update_applied_lsn(wal_end),
                ReplicationEvent::StoppedAt { reached } => {
                    Logger::sys_warn(
                        "changefeed.run",
                        "Logical replication stopped at LSN",
                        &reached.to_string(),
                    );
                    break;
                }
                _ => {}
            }
            if tokio::time::Instant::now() >= next_backlog_sample {
                self.observe_managed_service_backlog().await;
                next_backlog_sample =
                    tokio::time::Instant::now() + std::time::Duration::from_secs(10);
            }
        }

        Ok(())
    }
}
