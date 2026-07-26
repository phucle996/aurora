use std::sync::Arc;
use std::time::Duration;

use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;

use crate::config::Config;
use crate::executor::hypervisor::HypervisorRuntime;
use crate::executor::mail::MailRuntime;
use crate::infra::kafka::KafkaTransport;
use crate::infra::zone_kv::{ZoneKvStore, ZoneLease};
use crate::observability::logger::{LogFields, Logger};

const LEADER_LEASE_KEY: &str = "lease.zone.leader";
const LEADER_LEASE_TTL: Duration = Duration::from_secs(15);
const LEADER_RENEW_INTERVAL: Duration = Duration::from_secs(5);

/// Fenced session shared by every leader-only duty.
#[derive(Clone)]
pub(crate) struct ZoneLeaderSession {
    zone_kv: Arc<ZoneKvStore>,
    lease: ZoneLease,
    cancelled: CancellationToken,
}

impl ZoneLeaderSession {
    pub(crate) fn new(
        zone_kv: Arc<ZoneKvStore>,
        lease: ZoneLease,
        cancelled: CancellationToken,
    ) -> Self {
        Self {
            zone_kv,
            lease,
            cancelled,
        }
    }

    pub(crate) fn owner_id(&self) -> &str {
        &self.lease.owner_id
    }

    pub(crate) fn fencing_token(&self) -> u64 {
        self.lease.fencing_token
    }

    pub(crate) fn cancellation_token(&self) -> CancellationToken {
        self.cancelled.clone()
    }

    pub(crate) fn is_cancelled(&self) -> bool {
        self.cancelled.is_cancelled()
    }

    /// [COMMENT]: Đây là security/failure boundary trước mọi probe/publish của leader.
    /// KV read lỗi cũng fail-closed để pod bị partition không tiếp tục làm stale leader.
    pub(crate) async fn permits_external_side_effect(&self) -> bool {
        if self.cancelled.is_cancelled() {
            return false;
        }
        match self.zone_kv.lease_is_current(&self.lease).await {
            Ok(true) => true,
            Ok(false) => {
                Logger::sys_warn_with_fields(
                    "leader.side_effect_guard",
                    "ZONE_LEADER_FENCED",
                    "Leader side effect was denied because the lease is no longer current",
                    "",
                    LogFields {
                        operation_id: Some(&self.lease.owner_id),
                        leader_fencing_token: Some(self.lease.fencing_token),
                        outcome: Some("denied"),
                        ..LogFields::default()
                    },
                );
                false
            }
            Err(error) => {
                Logger::sys_warn_with_fields(
                    "leader.side_effect_guard",
                    "ZONE_LEADER_LEASE_READ_FAILED",
                    "Leader side effect was denied because current lease could not be verified",
                    &error,
                    LogFields {
                        operation_id: Some(&self.lease.owner_id),
                        leader_fencing_token: Some(self.lease.fencing_token),
                        retryable: Some(true),
                        outcome: Some("denied"),
                        ..LogFields::default()
                    },
                );
                false
            }
        }
    }

    pub(crate) async fn wait(&self, duration: Duration) -> bool {
        tokio::select! {
            _ = self.cancelled.cancelled() => false,
            _ = tokio::time::sleep(duration) => true,
        }
    }
}

pub(crate) async fn run_zone_leader_supervisor(
    config: Arc<Config>,
    zone_kv: Arc<ZoneKvStore>,
    kafka: Arc<KafkaTransport>,
    mail_runtime: Arc<MailRuntime>,
    hypervisor_runtime: Arc<HypervisorRuntime>,
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
            hypervisor_runtime.clone(),
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
    hypervisor_runtime: Arc<HypervisorRuntime>,
) -> JoinSet<()> {
    let mut duties = JoinSet::new();

    duties.spawn(super::zone_metadata::run_zone_metadata_kafka_listener(
        session.clone(),
        zone_kv.clone(),
        kafka.clone(),
        config.clone(),
    ));
    duties.spawn(super::zone_metadata::run_zone_metadata_repair_publisher(
        session.clone(),
        kafka.clone(),
        config.clone(),
    ));
    duties.spawn(super::zone_report::run_zone_report_publisher(
        session.clone(),
        zone_kv.clone(),
        kafka.clone(),
        config.clone(),
    ));
    duties.spawn(super::infra::hypervisor::run_hypervisor_health_probe(
        session.clone(),
        config.clone(),
        zone_kv.clone(),
        hypervisor_runtime,
    ));
    duties.spawn(super::infra::storage::run_storage_health_probe(
        session.clone(),
        config.clone(),
        zone_kv.clone(),
    ));
    duties.spawn(super::infra::storage::run_storage_bucket_size_scanner(
        session.clone(),
        config.clone(),
        zone_kv.clone(),
        kafka.clone(),
    ));
    duties.spawn(super::infra::mail::run_mail_infrastructure_health_probe(
        session.clone(),
        config.clone(),
        zone_kv.clone(),
        mail_runtime,
    ));
    duties.spawn(super::worker_scaling::run_zone_worker_scale_controller(
        session, config, zone_kv,
    ));
    duties
}

async fn wait_for_retry(shutdown: &CancellationToken) {
    let jitter_ms = rand::random::<u64>() % 1_000;
    tokio::select! {
        _ = shutdown.cancelled() => {}
        _ = tokio::time::sleep(Duration::from_millis(1_000 + jitter_ms)) => {}
    }
}
