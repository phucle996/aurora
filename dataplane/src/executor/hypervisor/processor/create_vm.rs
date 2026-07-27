use crate::config::Config;
use crate::executor::hypervisor::hypervisor_proto::{VmCreateResultV1, VmCreateV1};
use crate::executor::hypervisor::HypervisorRuntime;
use crate::executor::{ExecutionResult, ExecutorError};
use crate::job_runtime::model::ValidatedJob;
use prost::Message;
use sha2::{Digest, Sha256};
use std::collections::HashSet;
use std::sync::Arc;
use std::time::Duration;

use super::proxmox::CloneTemplateRequest;

pub(super) fn canonical_vm_config_hash(command: &VmCreateV1) -> [u8; 32] {
    let mut digest = Sha256::new();
    digest.update(command.image_id.as_slice());
    digest.update([0]);
    digest.update(command.image_revision.to_be_bytes());
    digest.update(command.image_sha256.as_slice());
    digest.update(u64::from(command.cpu_cores).to_be_bytes());
    digest.update(command.memory_mb.to_be_bytes());
    digest.update(command.disk_gb.to_be_bytes());
    digest.update(command.ssh_public_key.as_bytes());
    digest.finalize().into()
}

pub(crate) async fn execute_vm_create(
    job: Arc<ValidatedJob>,
    runtime: Arc<HypervisorRuntime>,
) -> Result<ExecutionResult, ExecutorError> {
    if job.payload_schema_version != 1 {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_VM_CREATE_SCHEMA_UNSUPPORTED".to_string(),
        ));
    }
    let command = VmCreateV1::decode(job.payload.as_ref()).map_err(|_| {
        ExecutorError::ExecutionFailed("HYPERVISOR_VM_CREATE_PROTO_INVALID".to_string())
    })?;
    let resource_id = uuid::Uuid::parse_str(&job.resource_id).map_err(|_| {
        ExecutorError::ExecutionFailed("HYPERVISOR_VM_RESOURCE_ID_INVALID".to_string())
    })?;
    if command.schema_version != 1
        || command.vm_id.as_slice() != resource_id.as_bytes()
        || command.provider_name != format!("aurora-{resource_id}")
        || command.provider_name.len() > 80
        || command.cpu_cores == 0
        || command.cpu_cores > 64
        || command.memory_mb < 512
        || command.memory_mb > 262_144
        || command.disk_gb < 8
        || command.disk_gb > 4_096
        || command.config_hash.len() != 32
        || command.image_id.len() != 16
        || command.image_revision == 0
        || command.image_sha256.len() != 32
        || command.provider_template_vmid == 0
        || command.ssh_public_key.len() > 16_384
        || (!command.ssh_public_key.starts_with("ssh-ed25519 ")
            && !command.ssh_public_key.starts_with("ssh-rsa ")
            && !command.ssh_public_key.starts_with("ecdsa-sha2-"))
    {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_VM_CREATE_CONTRACT_INVALID".to_string(),
        ));
    }
    let expected_hash = canonical_vm_config_hash(&command);
    if command.config_hash.as_slice() != expected_hash {
        // The config hash is the immutable execution identity. Recomputing
        // it after the Kafka trust boundary prevents payload/hash drift.
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_VM_CONFIG_HASH_MISMATCH".to_string(),
        ));
    }

    let config = Config::get_global();
    if config.proxmox_api_url.is_empty() || config.proxmox_api_token.is_empty() {
        return Err(ExecutorError::Retryable(
            "Proxmox endpoint or credential is unavailable in this Zone".to_string(),
        ));
    }
    let template_vmid = command.provider_template_vmid;

    let inventory = runtime
        .proxmox
        .list_vms()
        .await
        .map_err(ExecutorError::Retryable)?;

    let discovered = inventory.iter().find_map(|vm| {
        if vm.name == command.provider_name && !vm.is_template {
            return Some((vm.vmid, vm.node.clone()));
        }
        None
    });
    let occupied_vmids = inventory.iter().map(|vm| vm.vmid).collect::<HashSet<_>>();
    let provider_vmid = runtime
        .provider_bindings
        .resolve_provider_vmid(
            resource_id,
            &command.provider_name,
            discovered.as_ref().map(|(vmid, _)| *vmid),
            &occupied_vmids,
        )
        .await?;
    let mut provider_node = discovered
        .filter(|(vmid, _)| *vmid == provider_vmid)
        .map(|(_, node)| node)
        .unwrap_or_default();

    if let Some(vm) = inventory.iter().find(|vm| vm.vmid == provider_vmid) {
        if vm.name != command.provider_name || vm.is_template {
            return Err(ExecutorError::ExecutionFailed(
                "HYPERVISOR_PROVIDER_VMID_COLLISION".to_string(),
            ));
        }
        provider_node = vm.node.clone();
    }

    let mutation_permit;
    if provider_node.is_empty() {
        let template = inventory
            .iter()
            .find(|vm| vm.vmid == template_vmid && vm.is_template)
            .ok_or_else(|| {
                ExecutorError::Retryable(format!(
                    "Proxmox template VMID {template_vmid} is unavailable"
                ))
            })?;
        let nodes = runtime
            .proxmox
            .fetch_nodes()
            .await
            .map_err(ExecutorError::Retryable)?;
        let target = nodes
            .iter()
            .filter(|node| {
                node.status == "online"
                    && node.maxcpu >= u64::from(command.cpu_cores)
                    && node.maxmem.saturating_sub(node.mem)
                        >= command.memory_mb.saturating_mul(1_048_576)
            })
            .min_by(|left, right| {
                let left_score = left.cpu
                    + if left.maxmem == 0 {
                        1.0
                    } else {
                        left.mem as f64 / left.maxmem as f64
                    };
                let right_score = right.cpu
                    + if right.maxmem == 0 {
                        1.0
                    } else {
                        right.mem as f64 / right.maxmem as f64
                    };
                left_score.total_cmp(&right_score)
            })
            .ok_or_else(|| {
                ExecutorError::Retryable(
                    "No online Proxmox node has enough CPU and memory".to_string(),
                )
            })?;

        provider_node = target.node.clone();
        // The permit starts immediately before the first heavy mutation.
        // Inventory, placement and Zone KV CAS do not consume clone slots.
        mutation_permit = runtime
            .acquire_vm_mutation_permit()
            .await
            .map_err(ExecutorError::Retryable)?;
        let task = runtime
            .proxmox
            .clone_template(CloneTemplateRequest {
                template_node: &template.node,
                template_vmid,
                target_node: &provider_node,
                target_vmid: provider_vmid,
                provider_name: &command.provider_name,
                storage: &config.proxmox_storage,
                pool: &config.proxmox_pool,
            })
            .await
            .map_err(ExecutorError::Retryable)?;
        runtime
            .proxmox
            .wait_task(&template.node, &task)
            .await
            .map_err(ExecutorError::Retryable)?;
    } else {
        mutation_permit = runtime
            .acquire_vm_mutation_permit()
            .await
            .map_err(ExecutorError::Retryable)?;
    }

    let current_config = runtime
        .proxmox
        .vm_config(&provider_node, provider_vmid)
        .await
        .map_err(ExecutorError::Retryable)?;
    let config_hash_hex = command
        .config_hash
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    if let Some(description) = current_config.description.as_deref() {
        if let Some(existing_hash) = description
            .lines()
            .find_map(|line| line.strip_prefix("aurora-config-sha256="))
        {
            if existing_hash != config_hash_hex {
                // A stable provider identity pointing at another spec is a
                // permanent collision; retrying could mutate the wrong VM.
                return Err(ExecutorError::ExecutionFailed(
                    "HYPERVISOR_PROVIDER_IDENTITY_COLLISION".to_string(),
                ));
            }
        }
    }

    let (boot_disk, boot_disk_config) = match current_config.bootdisk.as_deref() {
        Some("scsi0") => ("scsi0", current_config.scsi0.as_deref()),
        Some("virtio0") => ("virtio0", current_config.virtio0.as_deref()),
        Some("sata0") => ("sata0", current_config.sata0.as_deref()),
        _ if current_config.scsi0.is_some() => ("scsi0", current_config.scsi0.as_deref()),
        _ if current_config.virtio0.is_some() => ("virtio0", current_config.virtio0.as_deref()),
        _ if current_config.sata0.is_some() => ("sata0", current_config.sata0.as_deref()),
        _ => {
            return Err(ExecutorError::ExecutionFailed(
                "HYPERVISOR_TEMPLATE_BOOT_DISK_UNREADABLE".to_string(),
            ))
        }
    };
    let current_disk_gb = boot_disk_config
        .and_then(|value| {
            value.split(',').find_map(|part| {
                let size = part.strip_prefix("size=")?;
                if let Some(gb) = size.strip_suffix('G') {
                    gb.parse::<u64>().ok()
                } else if let Some(tb) = size.strip_suffix('T') {
                    tb.parse::<u64>()
                        .ok()
                        .map(|value| value.saturating_mul(1024))
                } else if let Some(mb) = size.strip_suffix('M') {
                    mb.parse::<u64>().ok().map(|value| value.div_ceil(1024))
                } else {
                    None
                }
            })
        })
        .ok_or_else(|| {
            ExecutorError::ExecutionFailed("HYPERVISOR_TEMPLATE_BOOT_DISK_UNREADABLE".to_string())
        })?;
    if current_disk_gb > command.disk_gb {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_TEMPLATE_DISK_EXCEEDS_REQUEST".to_string(),
        ));
    }

    runtime
        .proxmox
        .configure_vm(
            &provider_node,
            provider_vmid,
            command.cpu_cores,
            command.memory_mb,
            &command.ssh_public_key,
            &config_hash_hex,
        )
        .await
        .map_err(ExecutorError::Retryable)?;
    if current_disk_gb < command.disk_gb {
        runtime
            .proxmox
            .resize_boot_disk(
                &provider_node,
                provider_vmid,
                boot_disk,
                command.disk_gb - current_disk_gb,
            )
            .await
            .map_err(ExecutorError::Retryable)?;
    }

    let current_status = runtime
        .proxmox
        .vm_status(&provider_node, provider_vmid)
        .await
        .map_err(ExecutorError::Retryable)?;
    if current_status != "running" {
        let task = runtime
            .proxmox
            .start_vm(&provider_node, provider_vmid)
            .await
            .map_err(ExecutorError::Retryable)?;
        runtime
            .proxmox
            .wait_task(&provider_node, &task)
            .await
            .map_err(ExecutorError::Retryable)?;
    }

    // Guest-agent warm-up is read-only and can be slow. Release the scarce
    // mutation slot before polling so another VM can clone/configure.
    drop(mutation_permit);
    let mut ipv4_address = String::new();
    for _ in 0..10 {
        ipv4_address = runtime
            .proxmox
            .guest_ipv4(&provider_node, provider_vmid)
            .await
            .unwrap_or_default();
        if !ipv4_address.is_empty() {
            break;
        }
        tokio::time::sleep(Duration::from_secs(3)).await;
    }

    let result = VmCreateResultV1 {
        schema_version: 1,
        vm_id: resource_id.as_bytes().to_vec(),
        provider_name: command.provider_name,
        provider_node: provider_node.clone(),
        provider_vmid,
        ipv4_address,
        config_hash: command.config_hash,
    };
    let result_payload = result.encode_to_vec();
    if result_payload.len() > 64 * 1024 {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_VM_RESULT_TOO_LARGE".to_string(),
        ));
    }
    Ok(ExecutionResult {
        message: format!(
            "Virtual machine {} is ready on Proxmox node {}",
            resource_id, provider_node
        ),
        result_payload,
        result_payload_schema_version: 1,
    })
}
