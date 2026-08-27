//! Durable terminal evidence owned by the job completion workflow, not a
//! product-state projection. A receipt is immutable within an operation epoch.
use crate::executor::ExecutionResult;
use crate::infra::zone_kv::ZoneKvStore;
use crate::job_runtime::completion::{
    job_proto::JobCompletionReceiptV1, CompletionStatus, JobExecutionResult,
};
use crate::job_runtime::model::ValidatedJob;
use bytes::Bytes;
use prost::Message;
use sha2::{Digest, Sha256};

#[cfg(test)]
#[path = "test/completion_receipt.rs"]
mod tests;

pub async fn load(
    store: &ZoneKvStore,
    job: &ValidatedJob,
) -> Result<Option<JobExecutionResult>, String> {
    let key = format!("job.completion.{}.{}", job.job_id, job.delivery_epoch);
    let primary = store
        .completion()
        .entry(&key)
        .await
        .map_err(|error| error.to_string())?;
    // Old receipts remain authoritative. A rollout must not turn a bucket move
    // into a cache miss and execute an already completed mutation again.
    let entry = match primary {
        Some(entry) => Some(entry),
        None => store.config_entry(&key).await?,
    };
    let Some(entry) = entry else {
        return Ok(None);
    };
    let receipt = JobCompletionReceiptV1::decode(entry.value)
        .map_err(|_| "JOB_COMPLETION_RECEIPT_CORRUPT".to_string())?;
    // Length-prefix every field; command boundaries must not hash ambiguously.
    let mut digest = Sha256::new();
    for value in [
        job.source_domain.as_bytes(),
        job.job_topic.as_bytes(),
        job.resource_id.as_bytes(),
        job.target_zone_id.as_bytes(),
        &job.job_version.to_be_bytes(),
        &job.payload_schema_version.to_be_bytes(),
        job.payload.as_ref(),
    ] {
        digest.update((value.len() as u64).to_be_bytes());
        digest.update(value);
    }
    if receipt.command_sha256 != digest.finalize().as_slice() {
        return Err("JOB_COMPLETION_RECEIPT_COMMAND_CONFLICT".to_string());
    }
    let status = match (receipt.schema_version, receipt.result_status.as_str()) {
        (1, "") | (2, "SUCCEEDED") if receipt.error_code.is_none() => CompletionStatus::Succeeded,
        (2, "FAILED")
            if receipt
                .error_code
                .as_ref()
                .is_some_and(|code| !code.is_empty()) =>
        {
            CompletionStatus::Failed
        }
        _ => return Err("JOB_COMPLETION_RECEIPT_STATUS_INVALID".into()),
    };
    let mut result = JobExecutionResult::from_executor(
        job,
        Ok(ExecutionResult {
            message: receipt.message,
            result_payload: receipt.result_payload,
            result_payload_schema_version: receipt.result_payload_schema_version,
        }),
    );
    result.attempt = receipt.attempt;
    result.status = status;
    result.error_code = receipt.error_code;
    Ok(Some(result))
}

pub async fn save(
    store: &ZoneKvStore,
    job: &ValidatedJob,
    result: &JobExecutionResult,
) -> Result<(), String> {
    if !result.status.is_terminal() {
        return Err("JOB_COMPLETION_RECEIPT_NONTERMINAL".into());
    }
    let mut digest = Sha256::new();
    for value in [
        job.source_domain.as_bytes(),
        job.job_topic.as_bytes(),
        job.resource_id.as_bytes(),
        job.target_zone_id.as_bytes(),
        &job.job_version.to_be_bytes(),
        &job.payload_schema_version.to_be_bytes(),
        job.payload.as_ref(),
    ] {
        digest.update((value.len() as u64).to_be_bytes());
        digest.update(value);
    }
    let receipt = JobCompletionReceiptV1 {
        schema_version: 2,
        command_sha256: digest.finalize().to_vec(),
        attempt: result.attempt,
        message: result.message.clone(),
        result_payload: result.result_payload.clone(),
        result_payload_schema_version: result.result_payload_schema_version,
        result_status: result.status.as_str().into(),
        error_code: result.error_code.clone(),
    };
    let key = format!("job.completion.{}.{}", job.job_id, job.delivery_epoch);
    let bytes = receipt.encode_to_vec();
    match store
        .completion()
        .create(&key, Bytes::from(bytes.clone()))
        .await
    {
        Ok(_) => Ok(()),
        Err(error) => {
            // Covers CAS contention AND an unknown create ACK: never overwrite
            // a winner, and never turn an unavailable store into a cache miss.
            match store
                .completion()
                .entry(key)
                .await
                .map_err(|error| error.to_string())?
            {
                Some(entry) if entry.value.as_ref() == bytes => Ok(()),
                Some(_) => Err("JOB_COMPLETION_RECEIPT_CONFLICT".to_string()),
                None => Err(error.to_string()),
            }
        }
    }
}
