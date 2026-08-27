use super::proxmox::DeleteTaskOutcome;
use crate::executor::hypervisor::hypervisor_proto::{
    VmDeleteJournalV1, VmDeleteResultV1, VmDeleteV1,
};
use crate::executor::hypervisor::HypervisorRuntime;
use crate::executor::{ExecutionResult, ExecutorError};
use crate::job_runtime::model::ValidatedJob;
use prost::Message;
use std::sync::Arc;

pub(crate) async fn execute_vm_delete(
    job: Arc<ValidatedJob>,
    runtime: Arc<HypervisorRuntime>,
) -> Result<ExecutionResult, ExecutorError> {
    if job.payload_schema_version != 1 {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_VM_DELETE_SCHEMA_UNSUPPORTED".to_string(),
        ));
    }
    let command = VmDeleteV1::decode(job.payload.as_ref()).map_err(|_| {
        ExecutorError::ExecutionFailed("HYPERVISOR_VM_DELETE_PROTO_INVALID".to_string())
    })?;
    let resource_id = uuid::Uuid::parse_str(&job.resource_id).map_err(|_| {
        ExecutorError::ExecutionFailed("HYPERVISOR_VM_RESOURCE_ID_INVALID".to_string())
    })?;
    if command.schema_version != 1
        || command.vm_id.as_slice() != resource_id.as_bytes()
        || command.provider_name != format!("aurora-{resource_id}")
        || command.provider_name.len() > 80
        || command.provider_vmid == 0
    {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_VM_DELETE_CONTRACT_INVALID".to_string(),
        ));
    }

    let completion_key = format!("hypervisor.vm.deletion.{}", job.job_id);
    let completion = runtime
        .zone_kv
        .config_get(&completion_key)
        .await
        .map_err(ExecutorError::OutcomeUnknown)?;
    let mut provider_completed_at_unix_ms =
        match completion {
            Some(bytes) => i64::from_be_bytes(bytes.as_ref().try_into().map_err(|_| {
                ExecutorError::ExecutionFailed("VM_DELETE_EVIDENCE_CORRUPT".into())
            })?),
            None => 0,
        };
    let journal_key = format!("hypervisor.vm.deletion.journal.{}", job.job_id);
    let entry = runtime
        .zone_kv
        .config_entry(&journal_key)
        .await
        .map_err(ExecutorError::OutcomeUnknown)?;
    let mut revision = entry.as_ref().map_or(0, |entry| entry.revision);
    let mut journal = match entry {
        Some(entry) => VmDeleteJournalV1::decode(entry.value)
            .map_err(|_| ExecutorError::OutcomeUnknown("VM_DELETE_JOURNAL_CORRUPT".into()))?,
        None => VmDeleteJournalV1 {
            schema_version: 1,
            vm_id: command.vm_id.clone(),
            provider_name: command.provider_name.clone(),
            provider_vmid: command.provider_vmid,
            ..Default::default()
        },
    };
    if journal.schema_version != 1
        || journal.vm_id != command.vm_id
        || journal.provider_name != command.provider_name
        || journal.provider_vmid != command.provider_vmid
    {
        return Err(ExecutorError::OutcomeUnknown(
            "VM_DELETE_JOURNAL_COMMAND_CONFLICT".into(),
        ));
    }
    // Preserve already-written provider evidence from jobs admitted before this
    // journal contract. Absence never manufactures a provider completion time.
    if revision == 0 {
        if let Some(task) = runtime
            .zone_kv
            .config_get(format!("hypervisor.vm.deletion.task.{}", job.job_id))
            .await
            .map_err(ExecutorError::OutcomeUnknown)?
        {
            let task = std::str::from_utf8(&task).map_err(|_| {
                ExecutorError::OutcomeUnknown("VM_DELETE_TASK_EVIDENCE_CORRUPT".into())
            })?;
            let (node, upid) = task.split_once('\n').ok_or_else(|| {
                ExecutorError::OutcomeUnknown("VM_DELETE_TASK_EVIDENCE_CORRUPT".into())
            })?;
            journal.provider_node = node.into();
            journal.task_upid = upid.into();
            revision = runtime
                .zone_kv
                .config_create(&journal_key, journal.encode_to_vec().into())
                .await
                .map_err(ExecutorError::OutcomeUnknown)?;
        }
    }
    if journal.provider_completed_at_unix_ms > 0 {
        provider_completed_at_unix_ms = journal.provider_completed_at_unix_ms;
    }
    if revision > 0 && provider_completed_at_unix_ms == 0 {
        if journal.task_upid.is_empty() {
            let tasks = runtime
                .proxmox
                .delete_tasks(&journal.provider_node, command.provider_vmid)
                .await
                .map_err(ExecutorError::OutcomeUnknown)?;
            let mut candidates = tasks
                .into_iter()
                .filter(|upid| !journal.previous_task_upids.contains(upid));
            if let Some(upid) = candidates.next() {
                if candidates.next().is_some() {
                    return Err(ExecutorError::OutcomeUnknown(
                        "VM_DELETE_TASK_RECOVERY_AMBIGUOUS".into(),
                    ));
                }
                journal.task_upid = upid;
                revision = runtime
                    .zone_kv
                    .config_update(&journal_key, journal.encode_to_vec().into(), revision)
                    .await
                    .map_err(ExecutorError::OutcomeUnknown)?;
            }
        }
        if !journal.task_upid.is_empty() {
            match runtime
                .proxmox
                .delete_task_outcome(&journal.provider_node, &journal.task_upid)
                .await
                .map_err(ExecutorError::OutcomeUnknown)?
            {
                DeleteTaskOutcome::Running => {
                    return Err(ExecutorError::OutcomeUnknown(
                        "VM_DELETE_PROVIDER_TASK_RUNNING".into(),
                    ))
                }
                DeleteTaskOutcome::Failed => {
                    journal
                        .previous_task_upids
                        .push(std::mem::take(&mut journal.task_upid));
                    journal.failed_tasks = journal.failed_tasks.saturating_add(1);
                    runtime
                        .zone_kv
                        .config_update(&journal_key, journal.encode_to_vec().into(), revision)
                        .await
                        .map_err(ExecutorError::OutcomeUnknown)?;
                    // A known failed task must not trap every retry on its UPID.
                    // The next attempt rechecks the immutable resource identity.
                    return Err(ExecutorError::Retryable(
                        "VM_DELETE_PROVIDER_TASK_FAILED".into(),
                    ));
                }
                DeleteTaskOutcome::Succeeded(seconds) => {
                    provider_completed_at_unix_ms = seconds.checked_mul(1000).ok_or_else(|| {
                        ExecutorError::OutcomeUnknown("VM_DELETE_COMPLETION_TIME_OVERFLOW".into())
                    })?;
                    journal.provider_completed_at_unix_ms = provider_completed_at_unix_ms;
                    revision = runtime
                        .zone_kv
                        .config_update(&journal_key, journal.encode_to_vec().into(), revision)
                        .await
                        .map_err(ExecutorError::OutcomeUnknown)?;
                }
            }
        }
    }
    let inventory = runtime
        .proxmox
        .list_vms()
        .await
        .map_err(ExecutorError::OutcomeUnknown)?;
    let by_vmid = inventory.iter().find(|vm| vm.vmid == command.provider_vmid);
    let by_name = inventory.iter().find(|vm| vm.name == command.provider_name);
    let provider_node = match (by_vmid, by_name) {
        (None, None) => String::new(),
        (Some(vmid), Some(named))
            if vmid.vmid == named.vmid
                && vmid.name == command.provider_name
                && !vmid.is_template =>
        {
            vmid.node.clone()
        }
        _ => {
            return Err(ExecutorError::ExecutionFailed(
                "HYPERVISOR_PROVIDER_IDENTITY_COLLISION".to_string(),
            ))
        }
    };

    if !provider_node.is_empty() {
        if provider_completed_at_unix_ms > 0 {
            return Err(ExecutorError::OutcomeUnknown(
                "VM_DELETE_SUCCEEDED_BUT_RESOURCE_PRESENT".into(),
            ));
        }
        if journal.failed_tasks >= 8 {
            return Err(ExecutorError::ExecutionFailed(
                "VM_DELETE_PROVIDER_FAILURE_REQUIRES_ATTENTION".into(),
            ));
        }
        let _permit = runtime
            .acquire_mutation_permit()
            .await
            .map_err(ExecutorError::OutcomeUnknown)?;
        let status = runtime
            .proxmox
            .vm_status(&provider_node, command.provider_vmid)
            .await
            .map_err(ExecutorError::OutcomeUnknown)?;
        if status != "stopped" {
            let task = runtime
                .proxmox
                .stop_vm(&provider_node, command.provider_vmid)
                .await
                .map_err(ExecutorError::OutcomeUnknown)?;
            runtime
                .proxmox
                .wait_task(&provider_node, &task)
                .await
                .map_err(ExecutorError::OutcomeUnknown)?;
        }
        let final_inventory = runtime
            .proxmox
            .list_vms()
            .await
            .map_err(ExecutorError::OutcomeUnknown)?;
        let final_vm = final_inventory
            .iter()
            .find(|vm| {
                vm.vmid == command.provider_vmid
                    && vm.name == command.provider_name
                    && !vm.is_template
            })
            .ok_or_else(|| {
                ExecutorError::OutcomeUnknown(
                    "HYPERVISOR_FINAL_NETWORK_COUNTER_UNAVAILABLE".to_string(),
                )
            })?;
        crate::executor::hypervisor::network_metering::record_terminal_network_observation(
            &runtime.zone_kv,
            resource_id,
            chrono::Utc::now().timestamp_millis(),
            final_vm.network_in_bytes,
            final_vm.network_out_bytes,
        )
        .await
        .map_err(ExecutorError::OutcomeUnknown)?;
        if revision == 0 {
            journal.provider_node = provider_node.clone();
            journal.previous_task_upids = runtime
                .proxmox
                .delete_tasks(&provider_node, command.provider_vmid)
                .await
                .map_err(ExecutorError::OutcomeUnknown)?;
            revision = runtime
                .zone_kv
                .config_create(&journal_key, journal.encode_to_vec().into())
                .await
                .map_err(ExecutorError::OutcomeUnknown)?;
        } else if journal.provider_node != provider_node {
            return Err(ExecutorError::OutcomeUnknown(
                "VM_DELETE_PROVIDER_NODE_CHANGED".into(),
            ));
        }
        let task = runtime
            .proxmox
            .delete_vm(&provider_node, command.provider_vmid)
            .await
            .map_err(ExecutorError::OutcomeUnknown)?;
        if !task.is_empty() {
            journal.task_upid = task;
            runtime
                .zone_kv
                .config_update(&journal_key, journal.encode_to_vec().into(), revision)
                .await
                .map_err(ExecutorError::OutcomeUnknown)?;
        }
        // Provider polling belongs to the same exact-command retry, including
        // the lost HTTP ACK case. Do not submit a second task while one is live.
        return Err(ExecutorError::OutcomeUnknown(
            "VM_DELETE_AWAITING_PROVIDER_RESULT".into(),
        ));
    }

    // Absence alone cannot reconstruct a lost provider completion timestamp.
    // Never substitute replay time and silently charge for JO/provider downtime.
    if provider_completed_at_unix_ms <= 0 {
        return Err(ExecutorError::OutcomeUnknown(
            "VM_DELETE_COMPLETION_EVIDENCE_UNAVAILABLE".into(),
        ));
    }
    let result = VmDeleteResultV1 {
        schema_version: 1,
        vm_id: resource_id.as_bytes().to_vec(),
        provider_name: command.provider_name,
        provider_vmid: command.provider_vmid,
        provider_completed_at_unix_ms,
    };
    let mut result_payload = Vec::with_capacity(result.encoded_len());
    result.encode(&mut result_payload).map_err(|_| {
        ExecutorError::ExecutionFailed("HYPERVISOR_VM_DELETE_RESULT_ENCODE_FAILED".to_string())
    })?;
    Ok(ExecutionResult {
        message: "Hypervisor VM deletion completed".to_string(),
        result_payload,
        result_payload_schema_version: 1,
    })
}
