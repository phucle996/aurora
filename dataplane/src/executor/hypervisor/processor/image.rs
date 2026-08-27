use crate::config::Config;
use crate::executor::hypervisor::hypervisor_proto::{
    ImageDeleteResultV1, ImageDeleteV1, ImageImportResultV1, ImageImportV1,
};
use crate::executor::hypervisor::HypervisorRuntime;
use crate::executor::{ExecutionResult, ExecutorError};
use crate::job_runtime::model::ValidatedJob;
use aws_credential_types::Credentials;
use aws_sdk_s3::config::{BehaviorVersion, Builder, Region};
use aws_sdk_s3::presigning::PresigningConfig;
use aws_sdk_s3::Client as S3Client;
use prost::Message;
use sha2::{Digest, Sha256};
use std::collections::HashSet;
use std::sync::Arc;
use std::time::Duration;
use uuid::Uuid;

const IMAGE_S3_TIMEOUT: Duration = Duration::from_secs(30);
const IMAGE_PRESIGN_TTL: Duration = Duration::from_secs(3_600);
const MAX_IMAGE_BYTES: u64 = 1 << 40;

/// One pooled, least-privilege S3 client per Dataplane pod.  The Proxmox
/// download URL is generated only after the object has passed size/hash checks.
pub(crate) struct ImageObjectStore {
    client: S3Client,
    bucket: String,
}

impl ImageObjectStore {
    pub(crate) fn from_config(config: &Config) -> Result<Option<Self>, String> {
        let values = [
            config.hypervisor_image_s3_endpoint.as_deref(),
            config.hypervisor_image_s3_bucket.as_deref(),
            config.hypervisor_image_s3_access_key.as_deref(),
            config.hypervisor_image_s3_secret_key.as_deref(),
        ];
        if values.iter().all(Option::is_none) {
            return Ok(None);
        }
        let [Some(endpoint), Some(bucket), Some(access_key), Some(secret_key)] = values else {
            return Err(
                "HYPERVISOR_IMAGE_S3_ENDPOINT, _BUCKET, _ACCESS_KEY and _SECRET_KEY must be configured together"
                    .to_string(),
            );
        };
        let endpoint = endpoint.trim();
        if !(endpoint.starts_with("http://") || endpoint.starts_with("https://"))
            || endpoint.len() > 512
            || endpoint.chars().any(char::is_control)
        {
            return Err("HYPERVISOR_IMAGE_S3_ENDPOINT is invalid".to_string());
        }
        if bucket.is_empty()
            || bucket.len() > 63
            || bucket
                .chars()
                .any(|character| character.is_control() || character == '/')
        {
            return Err("HYPERVISOR_IMAGE_S3_BUCKET is invalid".to_string());
        }
        let credentials = Credentials::new(
            access_key.to_string(),
            secret_key.to_string(),
            None,
            None,
            "hypervisor-image-registry",
        );
        let s3_config = Builder::new()
            .behavior_version(BehaviorVersion::latest())
            .credentials_provider(credentials)
            .endpoint_url(endpoint)
            .region(Region::new("us-east-1"))
            .force_path_style(true)
            .build();
        Ok(Some(Self {
            client: S3Client::from_conf(s3_config),
            bucket: bucket.to_string(),
        }))
    }

    async fn verify_object(
        &self,
        object_key: &str,
        expected_size: u64,
        expected_sha256: &[u8],
    ) -> Result<(), ExecutorError> {
        let head = tokio::time::timeout(
            IMAGE_S3_TIMEOUT,
            self.client
                .head_object()
                .bucket(&self.bucket)
                .key(object_key)
                .send(),
        )
        .await
        .map_err(|_| ExecutorError::Retryable("HYPERVISOR_IMAGE_OBJECT_HEAD_TIMEOUT".to_string()))?
        .map_err(|_| ExecutorError::Retryable("HYPERVISOR_IMAGE_OBJECT_HEAD_FAILED".to_string()))?;
        let content_length = head.content_length().unwrap_or(-1);
        if content_length < 0 || content_length as u64 != expected_size {
            return Err(ExecutorError::ExecutionFailed(
                "HYPERVISOR_IMAGE_OBJECT_SIZE_MISMATCH".to_string(),
            ));
        }

        let output = tokio::time::timeout(
            IMAGE_S3_TIMEOUT,
            self.client
                .get_object()
                .bucket(&self.bucket)
                .key(object_key)
                .send(),
        )
        .await
        .map_err(|_| ExecutorError::Retryable("HYPERVISOR_IMAGE_OBJECT_GET_TIMEOUT".to_string()))?
        .map_err(|_| ExecutorError::Retryable("HYPERVISOR_IMAGE_OBJECT_GET_FAILED".to_string()))?;
        let mut body = output.body;
        let mut digest = Sha256::new();
        let mut bytes_read = 0_u64;
        loop {
            let chunk = tokio::time::timeout(IMAGE_S3_TIMEOUT, body.try_next())
                .await
                .map_err(|_| {
                    ExecutorError::Retryable("HYPERVISOR_IMAGE_OBJECT_STREAM_TIMEOUT".to_string())
                })?
                .map_err(|_| {
                    ExecutorError::Retryable("HYPERVISOR_IMAGE_OBJECT_STREAM_FAILED".to_string())
                })?;
            let Some(chunk) = chunk else {
                break;
            };
            bytes_read = bytes_read.checked_add(chunk.len() as u64).ok_or_else(|| {
                ExecutorError::ExecutionFailed("HYPERVISOR_IMAGE_OBJECT_SIZE_OVERFLOW".to_string())
            })?;
            if bytes_read > expected_size {
                return Err(ExecutorError::ExecutionFailed(
                    "HYPERVISOR_IMAGE_OBJECT_SIZE_MISMATCH".to_string(),
                ));
            }
            digest.update(&chunk);
        }
        if bytes_read != expected_size || digest.finalize().as_slice() != expected_sha256 {
            return Err(ExecutorError::ExecutionFailed(
                "HYPERVISOR_IMAGE_OBJECT_SHA256_MISMATCH".to_string(),
            ));
        }
        Ok(())
    }

    async fn presigned_get(&self, object_key: &str) -> Result<String, ExecutorError> {
        let config = PresigningConfig::expires_in(IMAGE_PRESIGN_TTL).map_err(|_| {
            ExecutorError::ExecutionFailed("HYPERVISOR_IMAGE_PRESIGN_CONFIG_INVALID".to_string())
        })?;
        let request = tokio::time::timeout(
            IMAGE_S3_TIMEOUT,
            self.client
                .get_object()
                .bucket(&self.bucket)
                .key(object_key)
                .presigned(config),
        )
        .await
        .map_err(|_| ExecutorError::Retryable("HYPERVISOR_IMAGE_PRESIGN_TIMEOUT".to_string()))?
        .map_err(|_| ExecutorError::Retryable("HYPERVISOR_IMAGE_PRESIGN_FAILED".to_string()))?;
        Ok(request.uri().to_string())
    }

    async fn delete_object(&self, object_key: &str) -> Result<(), ExecutorError> {
        tokio::time::timeout(
            IMAGE_S3_TIMEOUT,
            self.client
                .delete_object()
                .bucket(&self.bucket)
                .key(object_key)
                .send(),
        )
        .await
        .map_err(|_| {
            ExecutorError::Retryable("HYPERVISOR_IMAGE_OBJECT_DELETE_TIMEOUT".to_string())
        })?
        .map_err(|_| {
            ExecutorError::Retryable("HYPERVISOR_IMAGE_OBJECT_DELETE_FAILED".to_string())
        })?;
        Ok(())
    }
}

fn image_provider_name(image_id: Uuid) -> String {
    format!("aurora-image-{image_id}")
}

fn image_filename(image_id: Uuid, format: &str) -> String {
    format!("{image_id}.{format}")
}

fn sha256_hex(sha256: &[u8]) -> String {
    sha256.iter().map(|byte| format!("{byte:02x}")).collect()
}

fn validate_import(
    job: &ValidatedJob,
    command: &ImageImportV1,
) -> Result<(Uuid, String, String), ExecutorError> {
    let image_id = Uuid::from_slice(&command.image_id)
        .map_err(|_| ExecutorError::ExecutionFailed("HYPERVISOR_IMAGE_ID_INVALID".to_string()))?;
    let zone_id = Uuid::from_slice(&command.zone_id).map_err(|_| {
        ExecutorError::ExecutionFailed("HYPERVISOR_IMAGE_ZONE_ID_INVALID".to_string())
    })?;
    if job.resource_id != image_id.to_string()
        || job.target_zone_id != zone_id.to_string()
        || command.schema_version != 1
        || command.revision == 0
        || command.revision > i64::MAX as u64
        || command.sha256.len() != 32
        || command.size_bytes == 0
        || command.size_bytes > MAX_IMAGE_BYTES
        || !matches!(command.format.as_str(), "qcow2" | "raw")
        || !matches!(command.architecture.as_str(), "x86_64" | "aarch64")
    {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_IMAGE_IMPORT_CONTRACT_INVALID".to_string(),
        ));
    }
    let expected_key = format!(
        "images/{image_id}/revisions/{}/{}.{}",
        command.revision,
        sha256_hex(command.sha256.as_slice()),
        command.format
    );
    if command.object_key != expected_key {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_IMAGE_OBJECT_KEY_MISMATCH".to_string(),
        ));
    }
    Ok((
        image_id,
        expected_key,
        sha256_hex(command.sha256.as_slice()),
    ))
}

fn validate_delete(
    job: &ValidatedJob,
    command: &ImageDeleteV1,
) -> Result<(Uuid, String, String), ExecutorError> {
    let image_id = Uuid::from_slice(&command.image_id)
        .map_err(|_| ExecutorError::ExecutionFailed("HYPERVISOR_IMAGE_ID_INVALID".to_string()))?;
    let zone_id = Uuid::from_slice(&command.zone_id).map_err(|_| {
        ExecutorError::ExecutionFailed("HYPERVISOR_IMAGE_ZONE_ID_INVALID".to_string())
    })?;
    if job.resource_id != image_id.to_string()
        || job.target_zone_id != zone_id.to_string()
        || command.schema_version != 1
        || command.revision == 0
        || command.revision > i64::MAX as u64
        || command.sha256.len() != 32
    {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_IMAGE_DELETE_CONTRACT_INVALID".to_string(),
        ));
    }
    let format = command.object_key.rsplit('.').next().unwrap_or_default();
    if !matches!(format, "qcow2" | "raw") {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_IMAGE_FORMAT_INVALID".to_string(),
        ));
    }
    let expected_key = format!(
        "images/{image_id}/revisions/{}/{}.{}",
        command.revision,
        sha256_hex(command.sha256.as_slice()),
        format
    );
    if command.object_key != expected_key {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_IMAGE_OBJECT_KEY_MISMATCH".to_string(),
        ));
    }
    Ok((
        image_id,
        expected_key,
        sha256_hex(command.sha256.as_slice()),
    ))
}

async fn cleanup_staging(
    runtime: &HypervisorRuntime,
    node: &str,
    source_storage: &str,
    filename: &str,
) -> Result<(), ExecutorError> {
    if source_storage.is_empty() {
        return Ok(());
    }
    let task = runtime
        .proxmox
        .delete_storage_content(node, source_storage, filename)
        .await
        .map_err(ExecutorError::Retryable)?;
    if task.is_empty() {
        return Ok(());
    }
    runtime
        .proxmox
        .wait_task(node, &task)
        .await
        .map(|_| ())
        .map_err(ExecutorError::Retryable)
}

pub async fn execute_image_import(
    job: Arc<ValidatedJob>,
    runtime: Arc<HypervisorRuntime>,
) -> Result<ExecutionResult, ExecutorError> {
    if job.payload_schema_version != 1 {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_IMAGE_IMPORT_SCHEMA_UNSUPPORTED".to_string(),
        ));
    }
    let command = ImageImportV1::decode(job.payload.as_ref()).map_err(|_| {
        ExecutorError::ExecutionFailed("HYPERVISOR_IMAGE_IMPORT_PROTO_INVALID".to_string())
    })?;
    let (image_id, object_key, checksum_hex) = validate_import(&job, &command)?;
    let config = Config::get_global();
    let Some(store) = runtime.image_store.as_ref() else {
        return Err(ExecutorError::Retryable(
            "HYPERVISOR_IMAGE_REGISTRY_UNAVAILABLE".to_string(),
        ));
    };
    if config.proxmox_image_source_storage.is_empty() || config.proxmox_storage.is_empty() {
        return Err(ExecutorError::Retryable(
            "HYPERVISOR_IMAGE_PROXMOX_STORAGE_UNAVAILABLE".to_string(),
        ));
    }

    let provider_name = image_provider_name(image_id);
    let filename = image_filename(image_id, &command.format);
    let inventory = runtime
        .proxmox
        .list_vms()
        .await
        .map_err(ExecutorError::Retryable)?;
    let matches = inventory
        .iter()
        .filter(|vm| vm.name == provider_name)
        .collect::<Vec<_>>();
    if matches.len() > 1 {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_IMAGE_PROVIDER_NAME_COLLISION".to_string(),
        ));
    }
    let discovered = matches.first().copied();
    let occupied_vmids = inventory.iter().map(|vm| vm.vmid).collect::<HashSet<_>>();
    let provider_vmid = runtime
        .provider_bindings
        .resolve_image_template_vmid(
            image_id,
            &provider_name,
            discovered.map(|vm| vm.vmid),
            &occupied_vmids,
        )
        .await?;

    if let Some(vm) = discovered {
        if vm.vmid != provider_vmid {
            return Err(ExecutorError::ExecutionFailed(
                "HYPERVISOR_IMAGE_PROVIDER_VMID_COLLISION".to_string(),
            ));
        }
        if !vm.is_template {
            let current = runtime
                .proxmox
                .vm_config(&vm.node, vm.vmid)
                .await
                .map_err(ExecutorError::Retryable)?;
            if !current.description.as_deref().is_some_and(|description| {
                description.contains(&format!("aurora-image-sha256={checksum_hex}"))
            }) {
                return Err(ExecutorError::ExecutionFailed(
                    "HYPERVISOR_IMAGE_PROVIDER_IDENTITY_COLLISION".to_string(),
                ));
            }
            let permit = runtime
                .acquire_mutation_permit()
                .await
                .map_err(ExecutorError::Retryable)?;
            let task = runtime
                .proxmox
                .convert_to_template(&vm.node, vm.vmid)
                .await
                .map_err(ExecutorError::Retryable)?;
            runtime
                .proxmox
                .wait_task(&vm.node, &task)
                .await
                .map_err(ExecutorError::Retryable)?;
            drop(permit);
        }
        cleanup_staging(
            &runtime,
            &vm.node,
            &config.proxmox_image_source_storage,
            &filename,
        )
        .await?;
        return Ok(image_import_result(
            image_id,
            command.revision,
            &command.sha256,
            provider_vmid,
        ));
    }

    store
        .verify_object(&object_key, command.size_bytes, &command.sha256)
        .await?;
    let url = store.presigned_get(&object_key).await?;
    let nodes = runtime
        .proxmox
        .fetch_nodes()
        .await
        .map_err(ExecutorError::Retryable)?;
    let node = nodes
        .iter()
        .filter(|node| node.status == "online")
        .min_by(|left, right| left.cpu.total_cmp(&right.cpu))
        .ok_or_else(|| ExecutorError::Retryable("HYPERVISOR_IMAGE_NO_ONLINE_NODE".to_string()))?;
    let permit = runtime
        .acquire_mutation_permit()
        .await
        .map_err(ExecutorError::Retryable)?;
    let download_task = runtime
        .proxmox
        .download_url_to_storage(
            &node.node,
            &config.proxmox_image_source_storage,
            &filename,
            &url,
            &checksum_hex,
        )
        .await
        .map_err(ExecutorError::Retryable)?;
    runtime
        .proxmox
        .wait_task(&node.node, &download_task)
        .await
        .map_err(ExecutorError::Retryable)?;
    let import_task = runtime
        .proxmox
        .create_vm_from_import(super::proxmox::CreateVmFromImportRequest {
            node: &node.node,
            vmid: provider_vmid,
            provider_name: &provider_name,
            source_storage: &config.proxmox_image_source_storage,
            filename: &filename,
            target_storage: &config.proxmox_storage,
            checksum_hex: &checksum_hex,
        })
        .await
        .map_err(ExecutorError::Retryable)?;
    runtime
        .proxmox
        .wait_task(&node.node, &import_task)
        .await
        .map_err(ExecutorError::Retryable)?;
    let template_task = runtime
        .proxmox
        .convert_to_template(&node.node, provider_vmid)
        .await
        .map_err(ExecutorError::Retryable)?;
    runtime
        .proxmox
        .wait_task(&node.node, &template_task)
        .await
        .map_err(ExecutorError::Retryable)?;
    drop(permit);
    cleanup_staging(
        &runtime,
        &node.node,
        &config.proxmox_image_source_storage,
        &filename,
    )
    .await?;
    Ok(image_import_result(
        image_id,
        command.revision,
        &command.sha256,
        provider_vmid,
    ))
}

pub async fn execute_image_delete(
    job: Arc<ValidatedJob>,
    runtime: Arc<HypervisorRuntime>,
) -> Result<ExecutionResult, ExecutorError> {
    if job.payload_schema_version != 1 {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_IMAGE_DELETE_SCHEMA_UNSUPPORTED".to_string(),
        ));
    }
    let command = ImageDeleteV1::decode(job.payload.as_ref()).map_err(|_| {
        ExecutorError::ExecutionFailed("HYPERVISOR_IMAGE_DELETE_PROTO_INVALID".to_string())
    })?;
    let (image_id, object_key, _) = validate_delete(&job, &command)?;
    let config = Config::get_global();
    let Some(store) = runtime.image_store.as_ref() else {
        return Err(ExecutorError::Retryable(
            "HYPERVISOR_IMAGE_REGISTRY_UNAVAILABLE".to_string(),
        ));
    };
    let provider_name = image_provider_name(image_id);
    let format = command.object_key.rsplit('.').next().unwrap_or("qcow2");
    let filename = image_filename(image_id, format);
    let inventory = runtime
        .proxmox
        .list_vms()
        .await
        .map_err(ExecutorError::Retryable)?;
    let named = inventory
        .iter()
        .filter(|vm| vm.name == provider_name)
        .collect::<Vec<_>>();
    if named.len() > 1 {
        return Err(ExecutorError::ExecutionFailed(
            "HYPERVISOR_IMAGE_PROVIDER_NAME_COLLISION".to_string(),
        ));
    }
    if let Some(vm) = named.first().copied() {
        if command.provider_template_vmid != 0 && vm.vmid != command.provider_template_vmid {
            return Err(ExecutorError::ExecutionFailed(
                "HYPERVISOR_IMAGE_PROVIDER_VMID_COLLISION".to_string(),
            ));
        }
        let permit = runtime
            .acquire_mutation_permit()
            .await
            .map_err(ExecutorError::Retryable)?;
        let task = runtime
            .proxmox
            .delete_vm(&vm.node, vm.vmid)
            .await
            .map_err(ExecutorError::Retryable)?;
        if !task.is_empty() {
            runtime
                .proxmox
                .wait_task(&vm.node, &task)
                .await
                .map_err(ExecutorError::Retryable)?;
        }
        drop(permit);
        cleanup_staging(
            &runtime,
            &vm.node,
            &config.proxmox_image_source_storage,
            &filename,
        )
        .await?;
    }
    if command.provider_template_vmid != 0 {
        runtime
            .provider_bindings
            .remove_image_template_binding(image_id, command.provider_template_vmid)
            .await?;
    }
    store.delete_object(&object_key).await?;
    Ok(ExecutionResult {
        message: "hypervisor image deleted".to_string(),
        result_payload: ImageDeleteResultV1 {
            schema_version: 1,
            image_id: image_id.as_bytes().to_vec(),
            revision: command.revision,
            sha256: command.sha256,
        }
        .encode_to_vec(),
        result_payload_schema_version: 1,
    })
}

fn image_import_result(
    image_id: Uuid,
    revision: u64,
    sha256: &[u8],
    provider_vmid: u64,
) -> ExecutionResult {
    ExecutionResult {
        message: "hypervisor image imported".to_string(),
        result_payload: ImageImportResultV1 {
            schema_version: 1,
            image_id: image_id.as_bytes().to_vec(),
            revision,
            sha256: sha256.to_vec(),
            provider_template_vmid: provider_vmid,
        }
        .encode_to_vec(),
        result_payload_schema_version: 1,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn image_key_is_derived_from_immutable_identity() {
        let id = Uuid::nil();
        let sha = vec![0xab; 32];
        assert_eq!(image_filename(id, "qcow2"), format!("{id}.qcow2"));
        assert_eq!(sha256_hex(&sha), "ab".repeat(32));
    }
}
