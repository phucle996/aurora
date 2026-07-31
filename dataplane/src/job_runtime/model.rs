use std::sync::Arc;
use std::time::Duration;

use prost::Message;

use crate::infra::kafka::transport_proto::{JobCommandV1, PayloadEncodingV1};
use crate::infra::kafka::KafkaDelivery;
use crate::infra::zone_kv::ZoneLease;
use crate::security::jobpayload::PayloadKeyring;

pub const MAX_JOB_COMMAND_BYTES: usize = 1_048_576;
pub const MAX_JOB_PAYLOAD_BYTES: usize = 1_000_256;
const DEFAULT_EXECUTION_TIMEOUT_SECONDS: u64 = 600;
const MAX_EXECUTION_TIMEOUT_SECONDS: u64 = 3_600;
const MAX_JOB_TOPIC_BYTES: usize = 160;
const MAX_SOURCE_DOMAIN_BYTES: usize = 32;
const MAX_RESOURCE_ID_BYTES: usize = 512;

/// Immutable command data that has crossed the Kafka trust boundary.
///
/// Transport settlement and the Zone lease deliberately live in phase-specific
/// wrappers below, so an executor cannot observe or mutate either handle.
#[derive(Clone, Debug)]
pub struct ValidatedJob {
    pub job_id: String,
    pub job_version: u32,
    pub attempt: u32,
    pub delivery_epoch: u64,
    pub job_topic: String,
    pub source_domain: String,
    pub resource_id: String,
    pub payload_schema_version: u32,
    pub payload: Arc<[u8]>,
    protected_payload: Arc<[u8]>,
    pub trace_id: String,
    pub traceparent: String,
    pub tracestate: String,
    pub execution_timeout_seconds: Option<u32>,
    pub reconcile_generation: Option<u64>,
    pub target_zone_id: String,
    job_id_bytes: [u8; 16],
    trace_id_bytes: Vec<u8>,
}

#[derive(Clone, Debug)]
pub struct QueuedJob {
    pub job: Arc<ValidatedJob>,
    pub delivery: KafkaDelivery,
}

#[derive(Clone, Debug)]
pub struct LeasedJob {
    pub queued: QueuedJob,
    pub lease: ZoneLease,
}

#[derive(Debug, Eq, PartialEq)]
pub struct JobValidationError {
    pub code: &'static str,
    pub message: String,
    pub retryable: bool,
}

impl JobValidationError {
    fn new(code: &'static str, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            retryable: false,
        }
    }
}

impl ValidatedJob {
    pub fn decode(
        raw: &[u8],
        expected_zone_id: &str,
        max_attempts: u32,
        payload_keyring: &PayloadKeyring,
    ) -> Result<Arc<Self>, JobValidationError> {
        if raw.is_empty() || raw.len() > MAX_JOB_COMMAND_BYTES {
            return Err(JobValidationError::new(
                "JOB_COMMAND_SIZE_INVALID",
                format!("JobCommandV1 size must be in 1..={MAX_JOB_COMMAND_BYTES} bytes"),
            ));
        }

        let command = JobCommandV1::decode(raw).map_err(|error| {
            JobValidationError::new(
                "JOB_COMMAND_PROTO_INVALID",
                format!("JobCommandV1 decode failed: {error}"),
            )
        })?;
        Self::from_command(command, expected_zone_id, max_attempts, payload_keyring).map(Arc::new)
    }

    fn from_command(
        command: JobCommandV1,
        expected_zone_id: &str,
        max_attempts: u32,
        payload_keyring: &PayloadKeyring,
    ) -> Result<Self, JobValidationError> {
        if command.transport_schema_version != 1 {
            return Err(JobValidationError::new(
                "JOB_TRANSPORT_SCHEMA_UNSUPPORTED",
                "transport_schema_version must equal 1",
            ));
        }
        if command.payload_schema_version == 0 {
            return Err(JobValidationError::new(
                "JOB_PAYLOAD_SCHEMA_INVALID",
                "payload_schema_version must be greater than zero",
            ));
        }
        if command.job_version == 0 {
            return Err(JobValidationError::new(
                "JOB_VERSION_INVALID",
                "job_version must be greater than zero",
            ));
        }
        if command.payload.len() > MAX_JOB_PAYLOAD_BYTES {
            return Err(JobValidationError::new(
                "JOB_PAYLOAD_SIZE_EXCEEDED",
                format!("job payload exceeds {MAX_JOB_PAYLOAD_BYTES} bytes"),
            ));
        }
        if command.payload_encoding
            != PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm as i32
        {
            return Err(JobValidationError::new(
                "JOB_PAYLOAD_ENCODING_UNSUPPORTED",
                "every Zone-bound V1 command must contain an HPKE protected payload",
            ));
        }
        if command.attempt >= max_attempts.max(1) {
            return Err(JobValidationError::new(
                "JOB_ATTEMPT_OUT_OF_RANGE",
                format!(
                    "attempt {} exceeds configured retry budget {}",
                    command.attempt,
                    max_attempts.max(1)
                ),
            ));
        }
        if command.delivery_epoch > i64::MAX as u64 {
            return Err(JobValidationError::new(
                "JOB_DELIVERY_EPOCH_INVALID",
                "delivery_epoch exceeds the durable platform range",
            ));
        }
        validate_text(
            "job_topic",
            &command.job_topic,
            MAX_JOB_TOPIC_BYTES,
            "JOB_TOPIC_INVALID",
        )?;
        if !command.job_topic.contains('.') {
            return Err(JobValidationError::new(
                "JOB_TOPIC_INVALID",
                "job_topic must use workload.action form",
            ));
        }
        validate_text(
            "source_domain",
            &command.source_domain,
            MAX_SOURCE_DOMAIN_BYTES,
            "JOB_SOURCE_DOMAIN_INVALID",
        )?;
        let workload = command
            .job_topic
            .split_once('.')
            .map(|(workload, _)| workload)
            .unwrap_or_default();
        match (workload, command.source_domain.as_str()) {
            ("mail", "MAIL") | ("storage", "STORAGE") | ("hypervisor", "HYPERVISOR") => {}
            ("managed_service", "MANAGED_SERVICE") => {
                // [COMMENT]: Inner proto may exist in P01, but no P01 executor is
                // allowed to consume it. This explicit rejection prevents a future
                // accidental topic/ACL change from creating a partial runtime path.
                return Err(JobValidationError::new(
                    "JOB_WORKLOAD_DISABLED",
                    "managed_service workload is not enabled on this Dataplane",
                ));
            }
            ("managed_service", _) => {
                return Err(JobValidationError::new(
                    "JOB_SOURCE_DOMAIN_MISMATCH",
                    "source_domain does not own the declared workload",
                ));
            }
            ("mail" | "storage" | "hypervisor", _) => {
                return Err(JobValidationError::new(
                    "JOB_SOURCE_DOMAIN_MISMATCH",
                    "source_domain does not own the declared workload",
                ));
            }
            _ => {
                return Err(JobValidationError::new(
                    "JOB_WORKLOAD_UNSUPPORTED",
                    "job_topic workload is not enabled on this Dataplane",
                ));
            }
        }
        validate_text(
            "resource_id",
            &command.resource_id,
            MAX_RESOURCE_ID_BYTES,
            "JOB_RESOURCE_ID_INVALID",
        )?;
        if command.target_zone_id != expected_zone_id {
            return Err(JobValidationError::new(
                "JOB_TARGET_ZONE_MISMATCH",
                format!(
                    "envelope target {:?} does not match consumer zone {expected_zone_id}",
                    command.target_zone_id
                ),
            ));
        }
        let expected_zone_uuid = uuid::Uuid::parse_str(expected_zone_id).map_err(|_| {
            JobValidationError::new(
                "JOB_CONSUMER_ZONE_INVALID",
                "configured Dataplane Zone must be a UUID",
            )
        })?;
        if !matches!(command.trace_id.len(), 0 | 16) {
            return Err(JobValidationError::new(
                "JOB_TRACE_ID_INVALID",
                "legacy trace_id must be empty or exactly 16 bytes",
            ));
        }
        if !crate::observability::otel::OtelTracer::is_valid_propagation_context(
            &command.traceparent,
            &command.tracestate,
        ) {
            return Err(JobValidationError::new(
                "JOB_TRACE_CONTEXT_INVALID",
                "traceparent/tracestate failed W3C validation",
            ));
        }
        if command.idle_seconds.is_some_and(|seconds| {
            seconds == 0 || u64::from(seconds) > MAX_EXECUTION_TIMEOUT_SECONDS
        }) {
            return Err(JobValidationError::new(
                "JOB_EXECUTION_TIMEOUT_INVALID",
                format!(
                    "idle_seconds compatibility field must be in 1..={MAX_EXECUTION_TIMEOUT_SECONDS}"
                ),
            ));
        }

        let job_uuid = uuid::Uuid::from_slice(&command.job_id).map_err(|error| {
            JobValidationError::new(
                "JOB_ID_UUID_INVALID",
                format!("job_id must be a 16-byte UUID: {error}"),
            )
        })?;
        let mut job_id_bytes = [0_u8; 16];
        job_id_bytes.copy_from_slice(job_uuid.as_bytes());
        let trace_id = encode_hex(&command.trace_id);
        let protected_payload: Arc<[u8]> = Arc::from(command.payload.clone());
        let payload = payload_keyring
            .open(
                &command.payload,
                expected_zone_uuid,
                &command.source_domain,
                &command.job_topic,
                &command.resource_id,
                command.job_version,
                command.payload_schema_version,
            )
            .map_err(|error| JobValidationError {
                code: error.code,
                message: error.message,
                retryable: error.retryable,
            })?;

        Ok(Self {
            job_id: job_uuid.to_string(),
            job_version: command.job_version,
            attempt: command.attempt,
            delivery_epoch: command.delivery_epoch,
            job_topic: command.job_topic,
            source_domain: command.source_domain,
            resource_id: command.resource_id,
            payload_schema_version: command.payload_schema_version,
            payload: Arc::from(payload),
            protected_payload,
            trace_id,
            traceparent: command.traceparent,
            tracestate: command.tracestate,
            execution_timeout_seconds: command.idle_seconds,
            reconcile_generation: command.reconcile_generation,
            target_zone_id: command.target_zone_id,
            job_id_bytes,
            trace_id_bytes: command.trace_id,
        })
    }

    pub fn execution_timeout(&self) -> Duration {
        Duration::from_secs(
            self.execution_timeout_seconds
                .map(u64::from)
                .unwrap_or(DEFAULT_EXECUTION_TIMEOUT_SECONDS),
        )
    }

    pub fn command_for_attempt(
        &self,
        attempt: u32,
        traceparent: String,
        tracestate: String,
    ) -> JobCommandV1 {
        JobCommandV1 {
            job_id: self.job_id_bytes.to_vec(),
            job_version: self.job_version,
            attempt,
            job_topic: self.job_topic.clone(),
            source_domain: self.source_domain.clone(),
            resource_id: self.resource_id.clone(),
            payload_schema_version: self.payload_schema_version,
            // [COMMENT]: Retry changes only delivery metadata. Re-sealing here
            // would require a public key and could change the security fence;
            // preserve the exact ciphertext committed by Controlplane.
            payload: self.protected_payload.to_vec(),
            trace_id: self.trace_id_bytes.clone(),
            idle_seconds: self.execution_timeout_seconds,
            reconcile_generation: self.reconcile_generation,
            target_zone_id: self.target_zone_id.clone(),
            transport_schema_version: 1,
            traceparent,
            tracestate,
            payload_encoding: PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm
                as i32,
            delivery_epoch: self.delivery_epoch,
        }
    }
}

fn validate_text(
    field: &str,
    value: &str,
    max_bytes: usize,
    code: &'static str,
) -> Result<(), JobValidationError> {
    if value.trim().is_empty() || value.len() > max_bytes || value.chars().any(char::is_control) {
        return Err(JobValidationError::new(
            code,
            format!("{field} must be non-empty, bounded and contain no control characters"),
        ));
    }
    Ok(())
}

fn encode_hex(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut encoded = String::with_capacity(bytes.len().saturating_mul(2));
    for byte in bytes {
        encoded.push(char::from(HEX[usize::from(byte >> 4)]));
        encoded.push(char::from(HEX[usize::from(byte & 0x0f)]));
    }
    encoded
}

#[cfg(test)]
mod tests {
    use super::*;

    const ZONE_A: &str = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
    const ZONE_B: &str = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";

    fn command(keyring: &PayloadKeyring) -> JobCommandV1 {
        let mut command = JobCommandV1 {
            job_id: uuid::Uuid::nil().as_bytes().to_vec(),
            job_version: 1,
            attempt: 0,
            job_topic: "storage.bucket.create".to_string(),
            source_domain: "STORAGE".to_string(),
            resource_id: "bucket-1".to_string(),
            payload_schema_version: 1,
            payload: Vec::new(),
            trace_id: Vec::new(),
            idle_seconds: Some(60),
            reconcile_generation: None,
            target_zone_id: ZONE_A.to_string(),
            transport_schema_version: 1,
            traceparent: String::new(),
            tracestate: String::new(),
            payload_encoding: PayloadEncodingV1::PayloadEncodingHpkeX25519HkdfSha256Aes256Gcm
                as i32,
            delivery_epoch: 0,
        };
        protect(&mut command, keyring);
        command
    }

    fn protect(command: &mut JobCommandV1, keyring: &PayloadKeyring) {
        command.payload = keyring.protect_for_test(
            uuid::Uuid::parse_str(&command.target_zone_id).expect("test Zone UUID"),
            &command.source_domain,
            &command.job_topic,
            &command.resource_id,
            command.job_version,
            command.payload_schema_version,
            &[1, 2, 3],
        );
    }

    #[test]
    fn validation_rejects_cross_zone_commands() {
        let keyring = PayloadKeyring::for_test();
        let error = ValidatedJob::from_command(command(&keyring), ZONE_B, 5, &keyring).unwrap_err();
        assert_eq!(error.code, "JOB_TARGET_ZONE_MISMATCH");
    }

    #[test]
    fn retry_command_preserves_validated_envelope() {
        let keyring = PayloadKeyring::for_test();
        let mut source = command(&keyring);
        source.delivery_epoch = 7;
        let job = ValidatedJob::from_command(source, ZONE_A, 5, &keyring).expect("valid command");
        let retry = job.command_for_attempt(1, "parent".to_string(), "state".to_string());
        assert_eq!(retry.attempt, 1);
        assert_eq!(retry.delivery_epoch, 7);
        assert_eq!(retry.job_id, uuid::Uuid::nil().as_bytes());
        assert_eq!(retry.target_zone_id, ZONE_A);
        assert_eq!(retry.traceparent, "parent");
    }

    #[test]
    fn validation_rejects_cross_domain_routing() {
        let keyring = PayloadKeyring::for_test();
        let mut command = command(&keyring);
        command.source_domain = "MAIL".to_string();
        let error = ValidatedJob::from_command(command, ZONE_A, 5, &keyring).unwrap_err();
        assert_eq!(error.code, "JOB_SOURCE_DOMAIN_MISMATCH");
    }

    #[test]
    fn validation_accepts_hypervisor_commands_from_the_hypervisor_domain() {
        let keyring = PayloadKeyring::for_test();
        let mut command = command(&keyring);
        command.job_topic = "hypervisor.vm.create".to_string();
        command.source_domain = "HYPERVISOR".to_string();
        protect(&mut command, &keyring);
        let job = ValidatedJob::from_command(command, ZONE_A, 5, &keyring).expect("valid command");
        assert_eq!(job.job_topic, "hypervisor.vm.create");
    }

    #[test]
    fn validation_keeps_managed_service_route_disabled() {
        let keyring = PayloadKeyring::for_test();
        let mut command = command(&keyring);
        command.job_topic = "managed_service.instance.execute".to_string();
        command.source_domain = "MANAGED_SERVICE".to_string();
        let error = ValidatedJob::from_command(command, ZONE_A, 5, &keyring).unwrap_err();
        assert_eq!(error.code, "JOB_WORKLOAD_DISABLED");
    }

    #[test]
    fn validation_requires_positive_job_version() {
        let keyring = PayloadKeyring::for_test();
        let mut command = command(&keyring);
        command.job_version = 0;
        let error = ValidatedJob::from_command(command, ZONE_A, 5, &keyring).unwrap_err();
        assert_eq!(error.code, "JOB_VERSION_INVALID");
    }

    #[test]
    fn validation_rejects_delivery_epoch_outside_postgres_range() {
        let keyring = PayloadKeyring::for_test();
        let mut command = command(&keyring);
        command.delivery_epoch = u64::MAX;
        let error = ValidatedJob::from_command(command, ZONE_A, 5, &keyring).unwrap_err();
        assert_eq!(error.code, "JOB_DELIVERY_EPOCH_INVALID");
    }
}
