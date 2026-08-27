mod executor;
mod network_metering;
mod processor;
mod runtime;

#[cfg(test)]
#[path = "test/delete_vm.rs"]
mod delete_vm_tests;

use crate::config::Config;
use crate::infra::zone_kv::ZoneKvStore;
use std::sync::Arc;
use tokio::sync::{OwnedSemaphorePermit, Semaphore};

pub use executor::dispatch_hypervisor_job;
pub use network_metering::run_network_metering;

pub mod hypervisor_proto {
    include!(concat!(env!("OUT_DIR"), "/hypervisor.rs"));
}

mod network_metering_proto {
    include!(concat!(
        env!("OUT_DIR"),
        "/aurora.hypervisor.metering.v1.rs"
    ));
}

/// Shared per-pod runtime, equivalent to `MailRuntime`: HTTP pooling,
/// mutation backpressure and Zone-local provider identity have one owner.
pub struct HypervisorRuntime {
    pub(crate) proxmox: Arc<processor::ProxmoxClient>,
    pub(crate) provider_bindings: Arc<runtime::ProviderBindingRuntime>,
    pub(crate) zone_kv: Arc<ZoneKvStore>,
    pub(crate) image_store: Option<Arc<processor::ImageObjectStore>>,
    mutation_limit: Arc<Semaphore>,
}

impl HypervisorRuntime {
    pub fn new(config: &Config, zone_kv: Arc<ZoneKvStore>) -> Result<Arc<Self>, String> {
        Ok(Arc::new(Self {
            proxmox: Arc::new(processor::ProxmoxClient::new(config)?),
            provider_bindings: Arc::new(runtime::ProviderBindingRuntime::new(zone_kv.clone())),
            zone_kv,
            image_store: processor::ImageObjectStore::from_config(config)?.map(Arc::new),
            mutation_limit: Arc::new(Semaphore::new(config.proxmox_max_concurrent_jobs)),
        }))
    }

    pub(crate) async fn acquire_mutation_permit(&self) -> Result<OwnedSemaphorePermit, String> {
        self.mutation_limit
            .clone()
            .acquire_owned()
            .await
            .map_err(|_| "Hypervisor mutation limiter is closed".to_string())
    }
}
