use std::sync::Arc;
use std::time::Duration;

use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;

use super::zone_leader_session::ZoneLeaderSession;
use crate::config::Config;
use crate::executor::mail::MailRuntime;
use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::{LogFields, Logger};

const LEADER_LEASE_KEY: &str = "lease.zone.leader";
const LEADER_LEASE_TTL: Duration = Duration::from_secs(15);
const LEADER_RENEW_INTERVAL: Duration = Duration::from_secs(5);

pub(crate) async fn run_zone_leader_supervisor(
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
    kafka: Arc<KafkaTransport>,
    mail_runtime: Arc<MailRuntime>,
    shutdown: CancellationToken,
) {
    // [COMMENT]: Boot UUID phân biệt incarnation; pod restart không được renew lease của process cũ.
    // Reuse logger identity so every election record and lease holder can be joined exactly.
    let owner_id = format!("{}:{}", Logger::node_id(), Logger::boot_id());

    while !shutdown.is_cancelled() {
        let lease_result = tokio::select! {
            _ = shutdown.cancelled() => return,
            result = tokio::time::timeout(
                Duration::from_secs(5),
                zone_kv.acquire_lease(LEADER_LEASE_KEY, &owner_id, LEADER_LEASE_TTL),
            ) => result,
        };
        let lease = match lease_result {
            Ok(Ok(Some(lease))) => lease,
            Ok(Ok(None)) => {
                wait_for_retry(&shutdown).await;
                continue;
            }
            Ok(Err(error)) => {
                Logger::sys_warn(
                    "leader.election",
                    "Không thể tham gia bầu leader trên Zone KV",
                    &error,
                );
                wait_for_retry(&shutdown).await;
                continue;
            }
            Err(_) => {
                Logger::sys_warn(
                    "leader.election",
                    "Timeout khi acquire leader lease",
                    "LEADER_ACQUIRE_TIMEOUT",
                );
                wait_for_retry(&shutdown).await;
                continue;
            }
        };

        Logger::sys_info_with_fields(
            "leader.elected",
            "ZONE_LEADER_ELECTED",
            &format!(
                "Node trở thành Zone leader owner={} fencing_token={}",
                owner_id, lease.fencing_token
            ),
            LogFields {
                operation_id: Some(&owner_id),
                leader_fencing_token: Some(lease.fencing_token),
                outcome: Some("elected"),
                ..LogFields::default()
            },
        );

        let session_cancel = shutdown.child_token();
        let session =
            ZoneLeaderSession::new(zone_kv.clone(), lease.clone(), session_cancel.clone());
        let mut duties = spawn_zone_leader_duties(
            session.clone(),
            config.clone(),
            zone_kv.clone(),
            kafka.clone(),
            mail_runtime.clone(),
        );
        let mut renew = tokio::time::interval(LEADER_RENEW_INTERVAL);
        renew.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
        renew.tick().await;

        loop {
            tokio::select! {
                _ = shutdown.cancelled() => break,
                duty = duties.join_next(), if !duties.is_empty() => {
                    let detail = match duty {
                        Some(Ok(())) => "leader duty exited unexpectedly".to_string(),
                        Some(Err(error)) => format!("leader duty failed: {error}"),
                        None => "leader duty set became empty".to_string(),
                    };
                    Logger::sys_warn(
                        "leader.duty",
                        "Một leader duty dừng; resign để khởi tạo lại toàn bộ session",
                        &detail,
                    );
                    break;
                }
                _ = renew.tick() => {
                    match zone_kv.renew_lease(&lease, LEADER_LEASE_TTL).await {
                        Ok(true) => {}
                        Ok(false) => {
                            Logger::sys_warn(
                                "leader.fenced",
                                "Leader lease đã mất; dừng toàn bộ leader duties",
                                "LEADER_LEASE_LOST",
                            );
                            break;
                        }
                        Err(error) => {
                            Logger::sys_warn(
                                "leader.fenced",
                                "Không renew được leader lease; fail-closed leader session",
                                &error,
                            );
                            break;
                        }
                    }
                }
            }
        }

        session_cancel.cancel();
        // [COMMENT]: Probe đều có bounded timeout/cancellation. Grace period ngắn hơn lease TTL,
        // sau đó abort để election mới không bị process cũ giữ task treo vô hạn.
        let drain = async { while duties.join_next().await.is_some() {} };
        if tokio::time::timeout(Duration::from_secs(8), drain)
            .await
            .is_err()
        {
            duties.abort_all();
            while duties.join_next().await.is_some() {}
        }
        match zone_kv.release_lease(&lease).await {
            Ok(true) => {}
            Ok(false) => Logger::sys_warn_with_fields(
                "leader.release",
                "ZONE_LEADER_RELEASE_NOT_CURRENT",
                "Leader lease was not released because ownership had already changed",
                "",
                LogFields {
                    operation_id: Some(&owner_id),
                    leader_fencing_token: Some(lease.fencing_token),
                    outcome: Some("already_fenced"),
                    ..LogFields::default()
                },
            ),
            Err(error) => Logger::sys_warn_with_fields(
                "leader.release",
                "ZONE_LEADER_RELEASE_FAILED",
                "Could not release leader lease; TTL and fencing still prevent stale ownership",
                &error,
                LogFields {
                    operation_id: Some(&owner_id),
                    leader_fencing_token: Some(lease.fencing_token),
                    retryable: Some(false),
                    outcome: Some("ttl_expiry_required"),
                    ..LogFields::default()
                },
            ),
        }
        Logger::sys_info_with_fields(
            "leader.resigned",
            "ZONE_LEADER_RESIGNED",
            &format!(
                "Node rời vai trò Zone leader owner={} fencing_token={}",
                owner_id, lease.fencing_token
            ),
            LogFields {
                operation_id: Some(&owner_id),
                leader_fencing_token: Some(lease.fencing_token),
                outcome: Some("resigned"),
                ..LogFields::default()
            },
        );
    }
}

fn spawn_zone_leader_duties(
    session: ZoneLeaderSession,
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
    kafka: Arc<KafkaTransport>,
    mail_runtime: Arc<MailRuntime>,
) -> JoinSet<()> {
    let mut duties = JoinSet::new();

    duties.spawn(
        super::zone_metadata_kafka_listener::run_zone_metadata_kafka_listener(
            session.clone(),
            zone_kv.clone(),
            kafka.clone(),
            config.clone(),
        ),
    );
    duties.spawn(
        super::zone_metadata_repair_publisher::run_zone_metadata_repair_publisher(
            session.clone(),
            kafka.clone(),
            config.clone(),
        ),
    );
    duties.spawn(super::zone_report_publisher::run_zone_report_publisher(
        session.clone(),
        zone_kv.clone(),
        kafka.clone(),
        config.clone(),
    ));
    duties.spawn(super::hypervisor_health_probe::run_hypervisor_health_probe(
        session.clone(),
        config.clone(),
        zone_kv.clone(),
    ));
    duties.spawn(super::storage_health_probe::run_storage_health_probe(
        session.clone(),
        config.clone(),
        zone_kv.clone(),
    ));
    duties.spawn(
        super::storage_bucket_size_scanner::run_storage_bucket_size_scanner(
            session.clone(),
            config.clone(),
            zone_kv.clone(),
            kafka.clone(),
        ),
    );
    duties.spawn(
        super::mail_infrastructure_health_probe::run_mail_infrastructure_health_probe(
            session.clone(),
            config.clone(),
            zone_kv.clone(),
            mail_runtime,
        ),
    );
    duties.spawn(
        super::zone_worker_scale_controller::run_zone_worker_scale_controller(
            session, config, zone_kv,
        ),
    );
    duties
}

async fn wait_for_retry(shutdown: &CancellationToken) {
    let jitter_ms = rand::random::<u64>() % 1_000;
    tokio::select! {
        _ = shutdown.cancelled() => {}
        _ = tokio::time::sleep(Duration::from_millis(1_000 + jitter_ms)) => {}
    }
}
