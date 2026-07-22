use super::configuration::{
    MailConfigurationRuntime, RuntimeConsumerConfiguration, RuntimeConsumerEntry,
    RuntimeDesiredState,
};
use super::context::{RuntimeGenerationFence, StreamRuntimeContext};
use super::dispatcher::dispatch_stream_runtime;
use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use crate::observability::logger::Logger;
use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
struct RuntimeSlotKey {
    consumer_id: String,
    slot: u32,
}

struct RuntimeSlotHandle {
    config_version: u64,
    config_sha256: [u8; 32],
    cancel: CancellationToken,
    task: JoinHandle<()>,
    retry_at: Instant,
}

/// [COMMENT]: Supervisor chỉ reconcile desired slots và Aurora lease; broker lifecycle được dispatch nguyên bộ theo stream_type.
pub struct MailConsumerSupervisor {
    enabled: bool,
    configuration: Arc<MailConfigurationRuntime>,
    context: Arc<StreamRuntimeContext>,
    reconcile_interval: Duration,
    max_slots_per_pod: usize,
    claim_cursor: AtomicU64,
    cancel: CancellationToken,
    task: Mutex<Option<JoinHandle<()>>>,
}

impl MailConsumerSupervisor {
    pub fn new(
        config: &Config,
        configuration: Arc<MailConfigurationRuntime>,
        zone_kv: Arc<ZoneKvStore>,
        processor: Arc<crate::executor::mail::processor::MailMessageProcessor>,
        runtime_node_id: String,
        runtime_boot_id: uuid::Uuid,
    ) -> Arc<Self> {
        let context =
            StreamRuntimeContext::new(config, runtime_node_id, runtime_boot_id, zone_kv, processor);
        Arc::new(Self {
            enabled: config.mail_stream_delivery_enabled,
            configuration,
            context,
            reconcile_interval: Duration::from_millis(config.mail_stream_supervisor_interval_ms),
            max_slots_per_pod: config.mail_stream_max_slots_per_pod,
            claim_cursor: AtomicU64::new(0),
            cancel: CancellationToken::new(),
            task: Mutex::new(None),
        })
    }

    pub fn start(self: &Arc<Self>) {
        if !self.enabled {
            Logger::sys_info(
                "mail.stream.supervisor",
                "Stream suites are installed but MAIL_STREAM_DELIVERY_ENABLED is false",
            );
            return;
        }
        let mut task = self
            .task
            .lock()
            .expect("mail supervisor task mutex poisoned");
        if task.is_some() {
            return;
        }
        let supervisor = self.clone();
        *task = Some(tokio::spawn(async move {
            supervisor.run().await;
        }));
    }

    pub async fn shutdown(&self) {
        self.cancel.cancel();
        let task = self
            .task
            .lock()
            .expect("mail supervisor task mutex poisoned")
            .take();
        if let Some(task) = task {
            let _ = task.await;
        }
    }

    async fn run(self: Arc<Self>) {
        let mut hasher = std::collections::hash_map::DefaultHasher::new();
        self.context.zone_id.hash(&mut hasher);
        self.context.instance_id.hash(&mut hasher);
        "mail-stream-supervisor".hash(&mut hasher);
        tokio::select! {
            _ = self.cancel.cancelled() => return,
            _ = tokio::time::sleep(Duration::from_millis(hasher.finish() % 2_000)) => {}
        }

        let mut slots = HashMap::<RuntimeSlotKey, RuntimeSlotHandle>::new();
        let mut last_snapshot = None;
        let mut last_claim_attempt = Instant::now() - Duration::from_secs(30);
        loop {
            let snapshot = self.configuration.snapshot();
            let config_changed = last_snapshot
                .as_ref()
                .is_none_or(|previous| !Arc::ptr_eq(previous, &snapshot));
            let has_retry_due = slots
                .values()
                .any(|slot| slot.task.is_finished() && Instant::now() >= slot.retry_at);
            if config_changed
                || has_retry_due
                || last_claim_attempt.elapsed() >= Duration::from_secs(5)
            {
                self.reconcile(snapshot.clone(), &mut slots).await;
                last_snapshot = Some(snapshot);
                last_claim_attempt = Instant::now();
            }
            tokio::select! {
                _ = self.cancel.cancelled() => break,
                _ = tokio::time::sleep(self.reconcile_interval) => {}
            }
        }

        // [COMMENT]: Mỗi suite nhận cancel, tự fence processing rồi đóng connection theo semantics của broker đó.
        for slot in slots.values() {
            slot.cancel.cancel();
        }
        for (_, slot) in slots {
            let _ = slot.task.await;
        }
    }

    async fn reconcile(
        self: &Arc<Self>,
        snapshot: Arc<HashMap<String, RuntimeConsumerEntry>>,
        slots: &mut HashMap<RuntimeSlotKey, RuntimeSlotHandle>,
    ) {
        let mut remove = Vec::new();
        for (key, slot) in slots.iter() {
            let still_desired = snapshot.get(&key.consumer_id).is_some_and(|entry| {
                matches!(entry, RuntimeConsumerEntry::Active(configuration)
                    if configuration.desired_state == RuntimeDesiredState::Enabled
                        && key.slot < configuration.parallelism
                        && slot.config_version == configuration.config_version
                        && slot.config_sha256 == configuration.config_sha256)
            });
            if !still_desired {
                slot.cancel.cancel();
            }
            if slot.task.is_finished() && (!still_desired || Instant::now() >= slot.retry_at) {
                remove.push(key.clone());
            }
        }
        for key in remove {
            if let Some(slot) = slots.remove(&key) {
                let _ = slot.task.await;
            }
        }

        let mut configurations = snapshot
            .values()
            .filter_map(|entry| match entry {
                RuntimeConsumerEntry::Active(configuration)
                    if configuration.desired_state == RuntimeDesiredState::Enabled =>
                {
                    Some(configuration.clone())
                }
                _ => None,
            })
            .collect::<Vec<_>>();
        configurations.sort_by(|left, right| left.consumer_id.cmp(&right.consumer_id));
        if !configurations.is_empty() {
            let cursor = self
                .claim_cursor
                .fetch_add(self.max_slots_per_pod.max(1) as u64, Ordering::Relaxed)
                as usize
                % configurations.len();
            configurations.rotate_left(cursor);
        }

        for configuration in configurations {
            for slot_number in 0..configuration.parallelism {
                if slots.len() >= self.max_slots_per_pod {
                    return;
                }
                let key = RuntimeSlotKey {
                    consumer_id: configuration.consumer_id.clone(),
                    slot: slot_number,
                };
                if slots.contains_key(&key) {
                    continue;
                }
                let cancel = self.cancel.child_token();
                let slot_cancel = cancel.clone();
                let supervisor = self.clone();
                let slot_configuration = configuration.clone();
                let task = tokio::spawn(async move {
                    supervisor
                        .run_slot(slot_configuration, slot_number, slot_cancel)
                        .await;
                });
                let mut retry_hasher = std::collections::hash_map::DefaultHasher::new();
                key.hash(&mut retry_hasher);
                self.context.instance_id.hash(&mut retry_hasher);
                slots.insert(
                    key,
                    RuntimeSlotHandle {
                        config_version: configuration.config_version,
                        config_sha256: configuration.config_sha256,
                        cancel,
                        task,
                        // [COMMENT]: Failed connect/lease claim không được biến central supervisor tick thành CAS loop 500 ms.
                        retry_at: Instant::now()
                            + Duration::from_secs(5)
                            + Duration::from_millis(retry_hasher.finish() % 2_000),
                    },
                );
            }
        }
    }

    async fn run_slot(
        &self,
        configuration: Arc<RuntimeConsumerConfiguration>,
        slot: u32,
        cancel: CancellationToken,
    ) {
        let lease_key = format!("mail.consumer.slot.{}.{}", configuration.consumer_id, slot);
        let owner_id = format!(
            "{}:{}:{}",
            self.context.instance_id, configuration.consumer_id, slot
        );
        let mut hasher = std::collections::hash_map::DefaultHasher::new();
        owner_id.hash(&mut hasher);
        lease_key.hash(&mut hasher);
        tokio::select! {
            _ = cancel.cancelled() => return,
            _ = tokio::time::sleep(Duration::from_millis(hasher.finish() % 750)) => {}
        }
        let lease = match self
            .context
            .zone_kv
            .acquire_lease(&lease_key, &owner_id, self.context.lease_ttl)
            .await
        {
            Ok(Some(lease)) => lease,
            Ok(None) => return,
            Err(_) => {
                Logger::sys_debug("mail.stream.runtime", "MAIL_STREAM_SLOT_LEASE_UNAVAILABLE");
                return;
            }
        };
        let generation = lease.fencing_token;
        let generation_fence = RuntimeGenerationFence::new(self.context.lease_ttl);
        self.context
            .write_health("STARTING", &configuration, slot, generation, &lease, "")
            .await;

        dispatch_stream_runtime(
            self.context.clone(),
            configuration.clone(),
            slot,
            generation,
            lease.clone(),
            generation_fence.clone(),
            cancel.clone(),
        )
        .await;
        generation_fence.fence().await;

        if cancel.is_cancelled() {
            self.context
                .write_health("STOPPED", &configuration, slot, generation, &lease, "")
                .await;
        }
        let _ = self.context.zone_kv.release_lease(&lease).await;
    }
}
