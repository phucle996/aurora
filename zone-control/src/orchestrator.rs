use std::{
    collections::HashMap,
    path::PathBuf,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use async_nats::jetstream::{self, kv, stream::StorageType};
use bytes::Bytes;
use futures_util::TryStreamExt;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::{
    metering, transfer_ticket::config::Config, zone_control_kafka::ControlKafka,
    zone_control_state::ZoneControlState, zone_metadata,
};

const MEMBERS_BUCKET: &str = "AURORA_ZONE_CONTROL_MEMBERS";
const ASSIGNMENTS_BUCKET: &str = "AURORA_ZONE_CONTROL_ASSIGNMENTS";
const MEMBERSHIP_TTL: Duration = Duration::from_secs(15);
const ASSIGNMENT_TTL: Duration = Duration::from_secs(20);
const RECONCILE_INTERVAL: Duration = Duration::from_secs(5);
const ASSIGNMENT_RENEWAL_MARGIN: Duration = Duration::from_secs(10);

/// A control workflow is scheduled by work unit, not by a process-wide leader.
/// A unit may remain ordered when its state transition cannot be parallelized;
/// other units are free to move to another Zone Control replica.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Hash, PartialEq, Serialize)]
enum WorkClass {
    MetadataProjection,
    MetadataRepair,
    StorageProbe,
    MailProbe,
    HypervisorProbe,
    ZoneReport,
    StorageScan,
    StorageReport,
    WorkerScale,
}

impl WorkClass {
    const ALL: [Self; 9] = [
        Self::MetadataProjection,
        Self::MetadataRepair,
        Self::StorageProbe,
        Self::MailProbe,
        Self::HypervisorProbe,
        Self::ZoneReport,
        Self::StorageScan,
        Self::StorageReport,
        Self::WorkerScale,
    ];

    const fn as_str(self) -> &'static str {
        match self {
            Self::MetadataProjection => "metadata_projection",
            Self::MetadataRepair => "metadata_repair",
            Self::StorageProbe => "storage_probe",
            Self::MailProbe => "mail_probe",
            Self::HypervisorProbe => "hypervisor_probe",
            Self::ZoneReport => "zone_report",
            Self::StorageScan => "storage_scan",
            Self::StorageReport => "storage_report",
            Self::WorkerScale => "worker_scale",
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
struct WorkUnit {
    class: WorkClass,
    shard: u16,
}

impl WorkUnit {
    fn key(self) -> String {
        format!("assignment.{}.{}", self.class.as_str(), self.shard)
    }
}

fn work_units(shards: u16) -> Vec<WorkUnit> {
    let mut units = WorkClass::ALL
        .into_iter()
        .filter(|class| *class != WorkClass::StorageScan)
        .map(|class| WorkUnit { class, shard: 0 })
        .collect::<Vec<_>>();
    units.extend((0..shards).map(|shard| WorkUnit {
        class: WorkClass::StorageScan,
        shard,
    }));
    units
}

#[derive(Clone, Debug, Deserialize, Serialize)]
struct MemberRecord {
    member_id: String,
    zone_id: String,
    weight: u32,
    max_concurrency: u32,
    heartbeat_seq: u64,
    expires_at_unix_ms: i64,
}

impl MemberRecord {
    fn is_alive(&self, now_ms: i64) -> bool {
        !self.member_id.is_empty()
            && self.weight > 0
            && self.max_concurrency > 0
            && self.expires_at_unix_ms > now_ms
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
struct AssignmentRecord {
    unit_key: String,
    member_id: String,
    assignment_epoch: u64,
    assigned_at_unix_ms: i64,
    expires_at_unix_ms: i64,
}

impl AssignmentRecord {
    fn is_current_for(&self, member_id: &str, now_ms: i64) -> bool {
        self.member_id == member_id && self.expires_at_unix_ms > now_ms
    }

    fn renew(&self, now_ms: i64) -> Self {
        Self {
            unit_key: self.unit_key.clone(),
            member_id: self.member_id.clone(),
            assignment_epoch: self.assignment_epoch,
            assigned_at_unix_ms: self.assigned_at_unix_ms,
            expires_at_unix_ms: now_ms.saturating_add(ASSIGNMENT_TTL.as_millis() as i64),
        }
    }

    fn needs_renewal(&self, now_ms: i64) -> bool {
        self.expires_at_unix_ms
            <= now_ms.saturating_add(ASSIGNMENT_RENEWAL_MARGIN.as_millis() as i64)
    }
}

struct WorkAssignmentCoordinator {
    members: kv::Store,
    assignments: kv::Store,
    timeout: Duration,
    zone_id: String,
}

impl WorkAssignmentCoordinator {
    async fn connect(config: &Config) -> Result<Self, String> {
        let options = async_nats::ConnectOptions::new()
            .add_root_certificates(config.nats_ca.clone())
            .require_tls(true)
            .add_client_certificate(config.nats_cert.clone(), config.nats_key.clone())
            .credentials_file(PathBuf::from(&config.nats_creds))
            .await
            .map_err(|error| format!("read Zone NATS credentials: {error}"))?;
        let client =
            tokio::time::timeout(config.nats_timeout, options.connect(&config.nats_zone_url))
                .await
                .map_err(|_| "connect Zone NATS timed out".to_string())?
                .map_err(|error| format!("connect Zone NATS: {error}"))?;
        let js = jetstream::new(client);
        let members = get_or_create_store(
            &js,
            config,
            MEMBERS_BUCKET,
            "Zone Control replica membership and capacity heartbeats",
        )
        .await?;
        let assignments = get_or_create_store(
            &js,
            config,
            ASSIGNMENTS_BUCKET,
            "Zone Control work-unit assignments and fencing epochs",
        )
        .await?;
        Ok(Self {
            members,
            assignments,
            timeout: config.nats_timeout,
            zone_id: config.zone_id.clone(),
        })
    }

    async fn heartbeat(&self, member_id: &str, weight: u32, max_concurrency: u32) -> bool {
        let now_ms = unix_time_ms();
        let key = format!("member.{member_id}");
        let current =
            match tokio::time::timeout(self.timeout, self.members.entry(key.clone())).await {
                Ok(Ok(value)) => value,
                _ => return false,
            };
        let (revision, sequence) = current
            .as_ref()
            .and_then(|entry| serde_json::from_slice::<MemberRecord>(&entry.value).ok())
            .map(|value| {
                (
                    current.as_ref().map_or(0, |entry| entry.revision),
                    value.heartbeat_seq.saturating_add(1),
                )
            })
            .unwrap_or((0, 1));
        let value = match serde_json::to_vec(&MemberRecord {
            member_id: member_id.to_string(),
            zone_id: self.zone_id.clone(),
            weight,
            max_concurrency,
            heartbeat_seq: sequence,
            expires_at_unix_ms: now_ms.saturating_add(MEMBERSHIP_TTL.as_millis() as i64),
        }) {
            Ok(value) => Bytes::from(value),
            Err(_) => return false,
        };
        if revision == 0 {
            self.members.create(key, value).await.is_ok()
        } else {
            self.members.update(key, value, revision).await.is_ok()
        }
    }

    async fn reconcile(&self, units: &[WorkUnit]) -> Result<usize, String> {
        let now_ms = unix_time_ms();
        let active_members = self.active_members(now_ms).await?;
        if active_members.is_empty() {
            return Ok(0);
        }
        let mut changed = 0;
        for unit in units {
            let unit_key = unit.key();
            let winner = rendezvous_owner(&unit_key, &active_members)
                .ok_or_else(|| "active membership unexpectedly empty".to_string())?;
            if self.reconcile_unit(&unit_key, winner, now_ms).await? {
                changed += 1;
            }
        }
        Ok(changed)
    }

    async fn active_members(&self, now_ms: i64) -> Result<Vec<MemberRecord>, String> {
        let mut keys = self
            .members
            .keys()
            .await
            .map_err(|error| format!("list Zone Control membership: {error}"))?;
        let mut members = Vec::new();
        while let Some(key) = keys
            .try_next()
            .await
            .map_err(|error| format!("read Zone Control membership key: {error}"))?
        {
            let Some(entry) = self
                .members
                .entry(key)
                .await
                .map_err(|error| format!("read Zone Control membership entry: {error}"))?
            else {
                continue;
            };
            let Ok(member) = serde_json::from_slice::<MemberRecord>(&entry.value) else {
                continue;
            };
            if member.zone_id == self.zone_id && member.is_alive(now_ms) {
                members.push(member);
            }
        }
        members.sort_by(|left, right| left.member_id.cmp(&right.member_id));
        Ok(members)
    }

    async fn reconcile_unit(
        &self,
        unit_key: &str,
        winner: &MemberRecord,
        now_ms: i64,
    ) -> Result<bool, String> {
        let current = self
            .assignments
            .entry(unit_key.to_string())
            .await
            .map_err(|error| format!("read work-unit assignment {unit_key}: {error}"))?;
        let current_record = current
            .as_ref()
            .and_then(|entry| serde_json::from_slice::<AssignmentRecord>(&entry.value).ok());
        let current_owner_is_renewing = current_record
            .as_ref()
            .is_some_and(|record| record.is_current_for(&winner.member_id, now_ms));
        if current_owner_is_renewing
            && current_record
                .as_ref()
                .is_some_and(|record| !record.needs_renewal(now_ms))
        {
            return Ok(false);
        }
        let next_record = match current_record.as_ref() {
            Some(record) if current_owner_is_renewing => record.renew(now_ms),
            _ => AssignmentRecord {
                unit_key: unit_key.to_string(),
                member_id: winner.member_id.clone(),
                assignment_epoch: current_record
                    .as_ref()
                    .map_or(1, |record| record.assignment_epoch.saturating_add(1)),
                assigned_at_unix_ms: now_ms,
                expires_at_unix_ms: now_ms.saturating_add(ASSIGNMENT_TTL.as_millis() as i64),
            },
        };
        let value = Bytes::from(
            serde_json::to_vec(&next_record)
                .map_err(|error| format!("encode work-unit assignment {unit_key}: {error}"))?,
        );
        let current_entry_exists = current.is_some();
        let result = match current {
            Some(entry) => self
                .assignments
                .update(unit_key, value, entry.revision)
                .await
                .map_err(|error| error.to_string()),
            None => self
                .assignments
                .create(unit_key, value)
                .await
                .map_err(|error| error.to_string()),
        };
        match result {
            Ok(_) if current_owner_is_renewing => Ok(false),
            Ok(_) => {
                let reason = match current_record.as_ref() {
                    None if current_entry_exists => "invalid_record",
                    None => "initial",
                    Some(record) if record.expires_at_unix_ms <= now_ms => "expired",
                    Some(_) => "rebalance",
                };
                tracing::info!(
                    event_code = "ZONE_CONTROL_WORK_UNIT_ASSIGNED",
                    zone_id = %self.zone_id,
                    unit_key,
                    member_id = %winner.member_id,
                    assignment_epoch = next_record.assignment_epoch,
                    reason
                );
                Ok(true)
            }
            Err(error) if is_cas_conflict(&error) => Ok(false),
            Err(error) => Err(format!("write work-unit assignment {unit_key}: {error}")),
        }
    }

    async fn assignment_for(&self, unit: WorkUnit) -> Result<Option<AssignmentRecord>, String> {
        let entry = self
            .assignments
            .entry(unit.key())
            .await
            .map_err(|error| format!("read assignment {}: {error}", unit.key()))?;
        Ok(entry.and_then(|entry| serde_json::from_slice(&entry.value).ok()))
    }
}

async fn get_or_create_store(
    js: &jetstream::Context,
    config: &Config,
    bucket: &str,
    description: &str,
) -> Result<kv::Store, String> {
    match js.get_key_value(bucket).await {
        Ok(store) => Ok(store),
        Err(_) => match js
            .create_key_value(kv::Config {
                bucket: bucket.to_string(),
                description: description.to_string(),
                history: 1,
                max_age: Duration::from_secs(86_400),
                max_value_size: 64 * 1024,
                storage: StorageType::File,
                num_replicas: config.required_replicas,
                ..Default::default()
            })
            .await
        {
            Ok(store) => Ok(store),
            Err(create_error) => js.get_key_value(bucket).await.map_err(|get_error| {
                format!("create {bucket} KV: {create_error}; reopen failed: {get_error}")
            }),
        },
    }
}

fn rendezvous_owner<'a>(unit_key: &str, members: &'a [MemberRecord]) -> Option<&'a MemberRecord> {
    members.iter().max_by_key(|member| {
        let mut digest = Sha256::new();
        digest.update(unit_key.as_bytes());
        digest.update([0]);
        digest.update(member.member_id.as_bytes());
        let hash = digest.finalize();
        // Keep the hash score in 64 bits before applying capacity. Using the
        // full 128-bit prefix and saturating multiplication would collapse
        // most scores to `u128::MAX` as soon as capacity is greater than one.
        let raw = u64::from_be_bytes(hash[..8].try_into().expect("SHA-256 prefix length"));
        // Capacity participates in the deterministic score, so a replica with
        // more reserved control slots receives proportionally more work. This
        // keeps balancing stable without introducing a singleton scheduler.
        let capacity_weight =
            u128::from(member.weight).saturating_mul(u128::from(member.max_concurrency));
        u128::from(raw) * capacity_weight
    })
}

fn unix_time_ms() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .ok()
        .and_then(|duration| i64::try_from(duration.as_millis()).ok())
        .unwrap_or_default()
}

fn is_cas_conflict(error: &impl std::fmt::Display) -> bool {
    let message = error.to_string().to_ascii_lowercase();
    message.contains("wrong last sequence")
        || message.contains("wrong expected")
        || message.contains("conflict")
}

struct WorkflowTask {
    name: &'static str,
    assignment_epoch: u64,
    shutdown: CancellationToken,
    handle: JoinHandle<Result<(), String>>,
}

/// Starts Zone Control's distributed scheduler. This deliberately has no
/// process-wide leader lease: every replica heartbeats its capacity, computes
/// the same weighted assignment, and CAS-writes only the work units it owns.
pub fn start(config: Config, shutdown: CancellationToken) {
    tokio::spawn(async move {
        // NATS KV keys reject `:`; keep the member identifier key-safe while
        // retaining a per-process UUID for assignment fencing.
        let owner_id = format!("zone-control-{}", uuid::Uuid::new_v4());
        let coordinator = match WorkAssignmentCoordinator::connect(&config).await {
            Ok(coordinator) => coordinator,
            Err(error) => {
                tracing::error!(
                    event_code = "ZONE_CONTROL_COORDINATION_UNAVAILABLE",
                    zone_id = %config.zone_id,
                    error = %error
                );
                return;
            }
        };
        let state = match ZoneControlState::connect(&config).await {
            Ok(state) => state,
            Err(error) => {
                tracing::error!(
                    event_code = "ZONE_CONTROL_STATE_UNAVAILABLE",
                    zone_id = %config.zone_id,
                    error = %error
                );
                return;
            }
        };
        let kafka = match ControlKafka::connect(&config).await {
            Ok(kafka) => kafka,
            Err(error) => {
                tracing::error!(
                    event_code = "ZONE_CONTROL_KAFKA_UNAVAILABLE",
                    zone_id = %config.zone_id,
                    error = %error
                );
                return;
            }
        };
        let units = work_units(config.control_assignment_shards as u16);
        tracing::info!(
            event_code = "ZONE_CONTROL_DISTRIBUTED_SCHEDULER_STARTED",
            zone_id = %config.zone_id,
            member_id = %owner_id,
            work_units = units.len(),
            assignment_shards = config.control_assignment_shards,
            capacity_weight = config.control_capacity_weight
        );
        let mut interval = tokio::time::interval(RECONCILE_INTERVAL);
        interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
        let mut metering_task: Option<WorkflowTask> = None;
        let mut storage_report_relay_task: Option<WorkflowTask> = None;
        let mut metadata_task: Option<WorkflowTask> = None;
        let mut metadata_repair_task: Option<WorkflowTask> = None;
        let mut storage_probe_task: Option<WorkflowTask> = None;
        let mut mail_probe_task: Option<WorkflowTask> = None;
        let mut hypervisor_probe_task: Option<WorkflowTask> = None;
        let mut zone_report_task: Option<WorkflowTask> = None;
        let mut storage_scan_tasks: HashMap<u16, WorkflowTask> = HashMap::new();
        let mut worker_scale_task: Option<WorkflowTask> = None;
        loop {
            tokio::select! {
                _ = shutdown.cancelled() => break,
                _ = interval.tick() => {
                    if !coordinator
                        .heartbeat(&owner_id, config.control_capacity_weight, config.control_max_concurrency)
                        .await
                    {
                        tracing::warn!(
                            event_code = "ZONE_CONTROL_MEMBERSHIP_HEARTBEAT_FAILED",
                            zone_id = %config.zone_id,
                            member_id = %owner_id,
                            retryable = true
                        );
                        stop_workflow(&mut metering_task).await;
                        stop_workflow(&mut storage_report_relay_task).await;
                        stop_workflow(&mut metadata_task).await;
                        stop_workflow(&mut metadata_repair_task).await;
                        stop_workflow(&mut storage_probe_task).await;
                        stop_workflow(&mut mail_probe_task).await;
                        stop_workflow(&mut hypervisor_probe_task).await;
                        stop_workflow(&mut zone_report_task).await;
                        stop_workflows(&mut storage_scan_tasks).await;
                        stop_workflow(&mut worker_scale_task).await;
                        continue;
                    }
                    match coordinator.reconcile(&units).await {
                        Ok(changed) if changed > 0 => tracing::info!(
                            event_code = "ZONE_CONTROL_ASSIGNMENTS_RECONCILED",
                            zone_id = %config.zone_id,
                            member_id = %owner_id,
                            changed
                        ),
                        Ok(_) => {}
                        Err(error) => tracing::warn!(
                            event_code = "ZONE_CONTROL_ASSIGNMENT_RECONCILE_FAILED",
                            zone_id = %config.zone_id,
                            member_id = %owner_id,
                            error = %error,
                            retryable = true
                        ),
                    }
                    let report_unit = WorkUnit { class: WorkClass::StorageReport, shard: 0 };
                    let metadata_unit = WorkUnit { class: WorkClass::MetadataProjection, shard: 0 };
                    let repair_unit = WorkUnit { class: WorkClass::MetadataRepair, shard: 0 };
                    let storage_probe_unit = WorkUnit { class: WorkClass::StorageProbe, shard: 0 };
                    let mail_probe_unit = WorkUnit { class: WorkClass::MailProbe, shard: 0 };
                    let hypervisor_probe_unit = WorkUnit { class: WorkClass::HypervisorProbe, shard: 0 };
                    let zone_report_unit = WorkUnit { class: WorkClass::ZoneReport, shard: 0 };
                    let worker_scale_unit = WorkUnit { class: WorkClass::WorkerScale, shard: 0 };
                    let now_ms = unix_time_ms();
                    let owns = |assignment: Option<AssignmentRecord>| {
                        assignment.is_some_and(|value| value.is_current_for(&owner_id, now_ms))
                    };
                    let report_assignment = coordinator.assignment_for(report_unit).await.ok().flatten();
                    let metadata_assignment = coordinator.assignment_for(metadata_unit).await.ok().flatten();
                    let repair_assignment = coordinator.assignment_for(repair_unit).await.ok().flatten();
                    let storage_probe_assignment = coordinator.assignment_for(storage_probe_unit).await.ok().flatten();
                    let mail_probe_assignment = coordinator.assignment_for(mail_probe_unit).await.ok().flatten();
                    let hypervisor_probe_assignment = coordinator.assignment_for(hypervisor_probe_unit).await.ok().flatten();
                    let zone_report_assignment = coordinator.assignment_for(zone_report_unit).await.ok().flatten();
                    let worker_scale_assignment = coordinator.assignment_for(worker_scale_unit).await.ok().flatten();
                    let owns_report_unit = owns(report_assignment.clone());
                    let owns_metadata_unit = owns(metadata_assignment.clone());
                    let owns_repair_unit = owns(repair_assignment.clone());
                    let owns_storage_probe = owns(storage_probe_assignment.clone());
                    let owns_mail_probe = owns(mail_probe_assignment.clone());
                    let owns_hypervisor_probe = owns(hypervisor_probe_assignment.clone());
                    let owns_zone_report = owns(zone_report_assignment.clone());
                    let owns_worker_scale = owns(worker_scale_assignment.clone());

                    sync_workflow(&mut metadata_task, owns_metadata_unit, "metadata_projection", metadata_assignment.map_or(0, |value| value.assignment_epoch), {
                        let config = config.clone();
                        let state = state.clone();
                        let kafka = kafka.clone();
                        move |task_shutdown, assignment_epoch| {
                            tokio::spawn(async move {
                                zone_metadata::run_projection(config, state, kafka, task_shutdown, assignment_epoch).await
                            })
                        }
                    }).await;
                    sync_workflow(&mut metadata_repair_task, owns_repair_unit, "metadata_repair", repair_assignment.map_or(0, |value| value.assignment_epoch), {
                        let config = config.clone();
                        let state = state.clone();
                        let kafka = kafka.clone();
                        move |task_shutdown, assignment_epoch| {
                            tokio::spawn(async move {
                                zone_metadata::run_repair_publisher(config, state, kafka, task_shutdown, assignment_epoch).await
                            })
                        }
                    }).await;
                    sync_workflow(&mut metering_task, config.metering_enabled && owns_report_unit, "storage_report_publisher", report_assignment.as_ref().map_or(0, |value| value.assignment_epoch), {
                        let config = config.clone();
                        let state = state.clone();
                        move |task_shutdown, assignment_epoch| {
                            tokio::spawn(async move { metering::run(config, state, task_shutdown, assignment_epoch).await })
                        }
                    }).await;
                    sync_workflow(&mut storage_report_relay_task, config.metering_enabled && owns_report_unit, "storage_report_relay", report_assignment.as_ref().map_or(0, |value| value.assignment_epoch), {
                        let config = config.clone();
                        let state = state.clone();
                        move |task_shutdown, assignment_epoch| {
                            tokio::spawn(async move {
                                crate::storage_report_relay::run(config, state, task_shutdown, assignment_epoch).await
                            })
                        }
                    }).await;
                    sync_workflow(&mut storage_probe_task, owns_storage_probe, "storage_probe", storage_probe_assignment.as_ref().map_or(0, |value| value.assignment_epoch), {
                        let config = config.clone();
                        let state = state.clone();
                        move |task_shutdown, assignment_epoch| {
                            tokio::spawn(async move {
                                crate::zone_health::run_storage_probe(config, state, task_shutdown, assignment_epoch).await
                            })
                        }
                    }).await;
                    sync_workflow(&mut mail_probe_task, owns_mail_probe, "mail_probe", mail_probe_assignment.as_ref().map_or(0, |value| value.assignment_epoch), {
                        let config = config.clone();
                        let state = state.clone();
                        move |task_shutdown, assignment_epoch| {
                            tokio::spawn(async move {
                                crate::zone_health::run_mail_probe(config, state, task_shutdown, assignment_epoch).await
                            })
                        }
                    }).await;
                    sync_workflow(&mut hypervisor_probe_task, owns_hypervisor_probe, "hypervisor_probe", hypervisor_probe_assignment.as_ref().map_or(0, |value| value.assignment_epoch), {
                        let config = config.clone();
                        let state = state.clone();
                        move |task_shutdown, assignment_epoch| {
                            tokio::spawn(async move {
                                crate::zone_health::run_hypervisor_probe(config, state, task_shutdown, assignment_epoch).await
                            })
                        }
                    }).await;
                    sync_workflow(&mut zone_report_task, owns_zone_report, "zone_report", zone_report_assignment.as_ref().map_or(0, |value| value.assignment_epoch), {
                        let config = config.clone();
                        let state = state.clone();
                        let kafka = kafka.clone();
                        move |task_shutdown, assignment_epoch| {
                            tokio::spawn(async move {
                                crate::zone_health::run_zone_report(config, state, kafka, task_shutdown, assignment_epoch).await
                            })
                        }
                    }).await;
                    for shard in 0..config.control_assignment_shards as u16 {
                        let storage_scan_unit = WorkUnit { class: WorkClass::StorageScan, shard };
                        let storage_scan_assignment = coordinator.assignment_for(storage_scan_unit).await.ok().flatten();
                        let should_run = owns(storage_scan_assignment.clone());
                        let assignment_epoch = storage_scan_assignment.as_ref().map_or(0, |value| value.assignment_epoch);
                        sync_sharded_workflow(
                            &mut storage_scan_tasks,
                            shard,
                            should_run,
                            "storage_bucket_scan",
                            assignment_epoch,
                            {
                                let config = config.clone();
                                let state = state.clone();
                                let kafka = kafka.clone();
                                move |task_shutdown, assignment_epoch| {
                                    tokio::spawn(async move {
                                        crate::zone_storage::run_bucket_scanner(
                                            config,
                                            state,
                                            kafka,
                                            task_shutdown,
                                            assignment_epoch,
                                            shard,
                                        )
                                        .await
                                    })
                                }
                            },
                        )
                        .await;
                    }
                    sync_workflow(&mut worker_scale_task, owns_worker_scale, "worker_scale", worker_scale_assignment.as_ref().map_or(0, |value| value.assignment_epoch), {
                        let config = config.clone();
                        let state = state.clone();
                        move |task_shutdown, assignment_epoch| {
                            tokio::spawn(async move {
                                crate::zone_scaling::run_worker_scale_controller(config, state, task_shutdown, assignment_epoch).await
                            })
                        }
                    }).await;
                }
            }
        }
        stop_workflow(&mut metering_task).await;
        stop_workflow(&mut storage_report_relay_task).await;
        stop_workflow(&mut metadata_task).await;
        stop_workflow(&mut metadata_repair_task).await;
        stop_workflow(&mut storage_probe_task).await;
        stop_workflow(&mut mail_probe_task).await;
        stop_workflow(&mut hypervisor_probe_task).await;
        stop_workflow(&mut zone_report_task).await;
        stop_workflows(&mut storage_scan_tasks).await;
        stop_workflow(&mut worker_scale_task).await;
        tracing::info!(
            event_code = "ZONE_CONTROL_DISTRIBUTED_SCHEDULER_STOPPED",
            zone_id = %config.zone_id,
            member_id = %owner_id
        );
    });
}

async fn sync_workflow<F>(
    task: &mut Option<WorkflowTask>,
    should_run: bool,
    name: &'static str,
    assignment_epoch: u64,
    start: F,
) where
    F: FnOnce(CancellationToken, u64) -> JoinHandle<Result<(), String>>,
{
    if task
        .as_ref()
        .is_some_and(|value| value.handle.is_finished())
    {
        if let Some(task) = task.take() {
            match task.handle.await {
                Ok(Ok(())) => {}
                Ok(Err(error)) => tracing::warn!(
                    event_code = "ZONE_CONTROL_WORKFLOW_STOPPED",
                    workflow = task.name,
                    error = %error,
                    retryable = true
                ),
                Err(error) => tracing::warn!(
                    event_code = "ZONE_CONTROL_WORKFLOW_JOIN_FAILED",
                    workflow = task.name,
                    error = %error,
                    retryable = true
                ),
            }
        }
    }
    if should_run
        && task
            .as_ref()
            .is_some_and(|value| value.assignment_epoch != assignment_epoch)
    {
        stop_workflow(task).await;
    }
    if should_run && task.is_none() {
        let workflow_shutdown = CancellationToken::new();
        *task = Some(WorkflowTask {
            name,
            assignment_epoch,
            handle: start(workflow_shutdown.clone(), assignment_epoch),
            shutdown: workflow_shutdown,
        });
        tracing::info!(
            event_code = "ZONE_CONTROL_WORKFLOW_STARTED",
            workflow = name
        );
    } else if !should_run {
        stop_workflow(task).await;
    }
}

async fn stop_workflow(task: &mut Option<WorkflowTask>) {
    let Some(task) = task.take() else {
        return;
    };
    task.shutdown.cancel();
    let mut handle = task.handle;
    if tokio::time::timeout(Duration::from_secs(5), &mut handle)
        .await
        .is_err()
    {
        handle.abort();
        let _ = handle.await;
    }
}

async fn sync_sharded_workflow<F>(
    tasks: &mut HashMap<u16, WorkflowTask>,
    shard: u16,
    should_run: bool,
    name: &'static str,
    assignment_epoch: u64,
    start: F,
) where
    F: FnOnce(CancellationToken, u64) -> JoinHandle<Result<(), String>>,
{
    let mut task = tasks.remove(&shard);
    sync_workflow(&mut task, should_run, name, assignment_epoch, start).await;
    if let Some(task) = task {
        tasks.insert(shard, task);
    }
}

async fn stop_workflows(tasks: &mut HashMap<u16, WorkflowTask>) {
    let mut pending = std::mem::take(tasks);
    for task in pending.values_mut() {
        task.shutdown.cancel();
    }
    for (_, mut task) in pending {
        if tokio::time::timeout(Duration::from_secs(5), &mut task.handle)
            .await
            .is_err()
        {
            task.handle.abort();
            let _ = task.handle.await;
        }
    }
}

#[cfg(test)]
#[path = "../tests/unit/orchestrator.rs"]
mod tests;
