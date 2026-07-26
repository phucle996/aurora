use crate::executor::ExecutorError;
use crate::infra::zone_kv::ZoneKvStore;
use bytes::Bytes;
use serde::{Deserialize, Serialize};
use std::collections::HashSet;
use std::sync::Arc;
use std::time::Duration;

const PROVIDER_BINDING_IO_TIMEOUT: Duration = Duration::from_secs(5);
const VMID_COLLISION_PROBE_BUDGET: u64 = 32;
const VMID_RANGE: u64 = 999_999_900;
const VMID_MINIMUM: u64 = 100;

pub(super) fn provider_vmid_candidate(resource_id: uuid::Uuid, offset: u64) -> u64 {
    let mut seed = [0_u8; 8];
    seed.copy_from_slice(&resource_id.as_bytes()[..8]);
    let first_candidate = u64::from_be_bytes(seed) % VMID_RANGE + VMID_MINIMUM;
    (first_candidate - VMID_MINIMUM + offset) % VMID_RANGE + VMID_MINIMUM
}

#[derive(Deserialize, Serialize)]
struct ProviderBinding {
    schema_version: u32,
    resource_id: String,
    provider_name: String,
    provider_vmid: u64,
}

pub(crate) struct VmProviderBindingRuntime {
    zone_kv: Arc<ZoneKvStore>,
}

impl VmProviderBindingRuntime {
    pub(crate) fn new(zone_kv: Arc<ZoneKvStore>) -> Self {
        Self { zone_kv }
    }

    pub(crate) async fn resolve_provider_vmid(
        &self,
        resource_id: uuid::Uuid,
        provider_name: &str,
        discovered_vmid: Option<u64>,
        occupied_vmids: &HashSet<u64>,
    ) -> Result<u64, ExecutorError> {
        let binding_key = format!("hypervisor.vm.provider.{resource_id}");
        let stored_binding = self
            .load_resource_binding(&binding_key, resource_id, provider_name)
            .await?;

        let provider_vmid = if let Some(discovered_vmid) = discovered_vmid {
            if !matches!(
                stored_binding.as_ref(),
                Some(binding) if binding.provider_vmid == discovered_vmid
            ) {
                // Provider names are observable data, not authority. Only the
                // immutable binding written before clone permits adoption.
                return Err(ExecutorError::ExecutionFailed(
                    "HYPERVISOR_PROVIDER_BINDING_MISSING_OR_MISMATCHED".to_string(),
                ));
            }
            discovered_vmid
        } else if let Some(binding) = stored_binding.as_ref() {
            binding.provider_vmid
        } else {
            let mut reserved_vmid = 0_u64;

            for offset in 0..VMID_COLLISION_PROBE_BUDGET {
                let candidate = provider_vmid_candidate(resource_id, offset);
                if occupied_vmids.contains(&candidate) {
                    continue;
                }
                let reverse_key = format!("hypervisor.provider.vmid.{candidate}");
                match self.load_vmid_owner(&reverse_key).await? {
                    Some(owner) if owner.as_ref() == resource_id.as_bytes() => {
                        reserved_vmid = candidate;
                        break;
                    }
                    Some(_) => continue,
                    None => {
                        if self
                            .create_vmid_owner(&reverse_key, resource_id)
                            .await
                            .is_ok()
                        {
                            reserved_vmid = candidate;
                            break;
                        }
                        if self
                            .load_vmid_owner(&reverse_key)
                            .await?
                            .is_some_and(|owner| owner.as_ref() == resource_id.as_bytes())
                        {
                            reserved_vmid = candidate;
                            break;
                        }
                    }
                }
            }

            if reserved_vmid == 0 {
                return Err(ExecutorError::Retryable(
                    "HYPERVISOR_PROVIDER_VMID_RESERVATION_EXHAUSTED".to_string(),
                ));
            }
            reserved_vmid
        };

        self.verify_or_create_vmid_owner(provider_vmid, resource_id)
            .await?;
        self.verify_or_create_resource_binding(
            &binding_key,
            resource_id,
            provider_name,
            provider_vmid,
        )
        .await?;
        Ok(provider_vmid)
    }

    async fn load_resource_binding(
        &self,
        binding_key: &str,
        resource_id: uuid::Uuid,
        provider_name: &str,
    ) -> Result<Option<ProviderBinding>, ExecutorError> {
        let value = tokio::time::timeout(
            PROVIDER_BINDING_IO_TIMEOUT,
            self.zone_kv.config_get(binding_key),
        )
        .await
        .map_err(|_| {
            ExecutorError::Retryable("HYPERVISOR_PROVIDER_BINDING_READ_TIMEOUT".to_string())
        })?
        .map_err(|_| {
            ExecutorError::Retryable("HYPERVISOR_PROVIDER_BINDING_READ_FAILED".to_string())
        })?;
        let Some(value) = value else {
            return Ok(None);
        };
        let binding: ProviderBinding = serde_json::from_slice(&value).map_err(|_| {
            ExecutorError::ExecutionFailed("HYPERVISOR_PROVIDER_BINDING_CORRUPT".to_string())
        })?;
        if binding.schema_version != 1
            || binding.resource_id != resource_id.to_string()
            || binding.provider_name != provider_name
            || binding.provider_vmid < VMID_MINIMUM
        {
            return Err(ExecutorError::ExecutionFailed(
                "HYPERVISOR_PROVIDER_BINDING_MISMATCH".to_string(),
            ));
        }
        Ok(Some(binding))
    }

    async fn load_vmid_owner(&self, reverse_key: &str) -> Result<Option<Bytes>, ExecutorError> {
        tokio::time::timeout(
            PROVIDER_BINDING_IO_TIMEOUT,
            self.zone_kv.config_get(reverse_key),
        )
        .await
        .map_err(|_| ExecutorError::Retryable("HYPERVISOR_PROVIDER_VMID_READ_TIMEOUT".to_string()))?
        .map_err(|_| ExecutorError::Retryable("HYPERVISOR_PROVIDER_VMID_READ_FAILED".to_string()))
    }

    async fn create_vmid_owner(
        &self,
        reverse_key: &str,
        resource_id: uuid::Uuid,
    ) -> Result<(), ExecutorError> {
        tokio::time::timeout(
            PROVIDER_BINDING_IO_TIMEOUT,
            self.zone_kv
                .config_create(reverse_key, Bytes::copy_from_slice(resource_id.as_bytes())),
        )
        .await
        .map_err(|_| {
            ExecutorError::Retryable("HYPERVISOR_PROVIDER_VMID_CREATE_TIMEOUT".to_string())
        })?
        .map(|_| ())
        .map_err(|_| ExecutorError::Retryable("HYPERVISOR_PROVIDER_VMID_CREATE_FAILED".to_string()))
    }

    async fn verify_or_create_vmid_owner(
        &self,
        provider_vmid: u64,
        resource_id: uuid::Uuid,
    ) -> Result<(), ExecutorError> {
        let reverse_key = format!("hypervisor.provider.vmid.{provider_vmid}");
        match self.load_vmid_owner(&reverse_key).await? {
            Some(owner) if owner.as_ref() == resource_id.as_bytes() => Ok(()),
            Some(_) => Err(ExecutorError::ExecutionFailed(
                "HYPERVISOR_PROVIDER_VMID_BINDING_COLLISION".to_string(),
            )),
            None => {
                if self
                    .create_vmid_owner(&reverse_key, resource_id)
                    .await
                    .is_ok()
                {
                    return Ok(());
                }
                if self
                    .load_vmid_owner(&reverse_key)
                    .await?
                    .is_some_and(|owner| owner.as_ref() == resource_id.as_bytes())
                {
                    return Ok(());
                }
                Err(ExecutorError::ExecutionFailed(
                    "HYPERVISOR_PROVIDER_VMID_BINDING_COLLISION".to_string(),
                ))
            }
        }
    }

    async fn verify_or_create_resource_binding(
        &self,
        binding_key: &str,
        resource_id: uuid::Uuid,
        provider_name: &str,
        provider_vmid: u64,
    ) -> Result<(), ExecutorError> {
        if let Some(binding) = self
            .load_resource_binding(binding_key, resource_id, provider_name)
            .await?
        {
            if binding.provider_vmid == provider_vmid {
                return Ok(());
            }
            return Err(ExecutorError::ExecutionFailed(
                "HYPERVISOR_PROVIDER_BINDING_MISMATCH".to_string(),
            ));
        }

        let value = serde_json::to_vec(&ProviderBinding {
            schema_version: 1,
            resource_id: resource_id.to_string(),
            provider_name: provider_name.to_string(),
            provider_vmid,
        })
        .map_err(|_| {
            ExecutorError::ExecutionFailed(
                "HYPERVISOR_PROVIDER_BINDING_SERIALIZE_FAILED".to_string(),
            )
        })?;
        let create = tokio::time::timeout(
            PROVIDER_BINDING_IO_TIMEOUT,
            self.zone_kv.config_create(binding_key, Bytes::from(value)),
        )
        .await;
        if matches!(create, Ok(Ok(_))) {
            return Ok(());
        }
        let winner = self
            .load_resource_binding(binding_key, resource_id, provider_name)
            .await?
            .ok_or_else(|| {
                ExecutorError::Retryable("HYPERVISOR_PROVIDER_BINDING_CAS_NOT_VISIBLE".to_string())
            })?;
        if winner.provider_vmid != provider_vmid {
            return Err(ExecutorError::ExecutionFailed(
                "HYPERVISOR_PROVIDER_BINDING_MISMATCH".to_string(),
            ));
        }
        Ok(())
    }
}
