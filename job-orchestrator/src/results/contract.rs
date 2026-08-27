use prost::Message;
use std::fmt::{Display, Formatter};
use uuid::Uuid;

use crate::infra::kafka::managed_service_proto::{
    ManagedServiceObservedStateV1, ManagedServiceOutcomeV1, ManagedServiceResultV1,
};

const MAX_RESULT_PAYLOAD_BYTES: usize = 64 * 1024;
const MAX_RESULT_MESSAGE_BYTES: usize = 16 * 1024;
const MAX_TOPIC_BYTES: usize = 128;
const MAX_ERROR_CODE_BYTES: usize = 128;
const MAX_ATTEMPT: u32 = 10_000;
const MAX_MANAGED_SERVICE_ATTEMPT: u32 = 4;
const MANAGED_SERVICE_RESULT_SCHEMA_VERSION: u32 = 1;

pub mod job_proto {
    include!(concat!(env!("OUT_DIR"), "/job_lifecycle.rs"));
}

#[derive(Debug)]
pub struct ValidatedResult {
    pub wire: job_proto::JobExecutionResultProto,
    pub job_id: Uuid,
    pub managed_service: Option<ValidatedManagedServiceResult>,
}

#[derive(Debug)]
pub struct ValidatedManagedServiceResult {
    pub source_command_event_id: Uuid,
    pub operation_id: Uuid,
    pub instance_id: Uuid,
    pub zone_id: Uuid,
    pub generation: i64,
    pub attempt: i16,
    pub instance_revision_id: Uuid,
    pub blueprint_revision_id: Uuid,
    pub bundle_hash: Vec<u8>,
    pub component_contract_hash: Vec<u8>,
    pub input_hash: Vec<u8>,
    pub desired_spec_hash: Vec<u8>,
    pub status: &'static str,
    pub error_code: Option<String>,
    pub sanitized_message: String,
    pub delivery_epoch: i64,
}

#[derive(Debug)]
pub struct ContractError {
    pub code: &'static str,
    detail: &'static str,
}

impl ContractError {
    fn new(code: &'static str, detail: &'static str) -> Self {
        Self { code, detail }
    }
}

impl Display for ContractError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(self.detail)
    }
}

impl std::error::Error for ContractError {}

pub fn decode(payload: &[u8]) -> Result<ValidatedResult, ContractError> {
    if payload.is_empty() || payload.len() > MAX_RESULT_PAYLOAD_BYTES {
        return Err(ContractError::new(
            "JOB_RESULT_SIZE_INVALID",
            "result payload is empty or exceeds the bounded contract",
        ));
    }
    let wire = job_proto::JobExecutionResultProto::decode(payload).map_err(|_| {
        ContractError::new(
            "JOB_RESULT_PROTO_INVALID",
            "result payload is not valid JobExecutionResultProto",
        )
    })?;
    let job_id = Uuid::from_slice(&wire.job_id).map_err(|_| {
        ContractError::new(
            "JOB_RESULT_ID_INVALID",
            "result job_id must be a 16-byte UUID",
        )
    })?;
    if job_id.is_nil() || wire.job_version == 0 || wire.attempt > MAX_ATTEMPT {
        return Err(ContractError::new(
            "JOB_RESULT_FENCE_INVALID",
            "result job_version or attempt fence is invalid",
        ));
    }
    if wire.job_topic.is_empty()
        || wire.job_topic.len() > MAX_TOPIC_BYTES
        || !crate::job_topics::is_result_registered(&wire.source_domain, &wire.job_topic)
    {
        return Err(ContractError::new(
            "JOB_RESULT_ROUTE_INVALID",
            "result source_domain and job_topic route is not registered",
        ));
    }
    if !matches!(
        wire.result_status.as_str(),
        "PROCESSING" | "SUCCEEDED" | "FAILED"
    ) {
        return Err(ContractError::new(
            "JOB_RESULT_STATUS_INVALID",
            "result status is not supported",
        ));
    }
    if wire.source_domain == "MANAGED_SERVICE" && wire.result_status == "PROCESSING" {
        return Err(ContractError::new(
            "MANAGED_SERVICE_RESULT_STATUS_INVALID",
            "managed service result must be terminal; retry remains owned by dataplane",
        ));
    }
    if wire.message.len() > MAX_RESULT_MESSAGE_BYTES
        || wire.result_payload.len() > MAX_RESULT_PAYLOAD_BYTES
        || wire
            .error_code
            .as_ref()
            .is_some_and(|value| value.len() > MAX_ERROR_CODE_BYTES)
        || (!wire.trace_id.is_empty() && wire.trace_id.len() != 16)
    {
        return Err(ContractError::new(
            "JOB_RESULT_FIELD_INVALID",
            "result contains an oversized or malformed bounded field",
        ));
    }
    if (wire.result_payload.is_empty() && wire.result_payload_schema_version != 0)
        || (!wire.result_payload.is_empty() && wire.result_payload_schema_version == 0)
    {
        return Err(ContractError::new(
            "JOB_RESULT_PAYLOAD_FENCE_INVALID",
            "result payload and schema version must either both be present or both be absent",
        ));
    }
    if !crate::observability::otel::OtelTracer::is_valid_propagation_context(
        &wire.traceparent,
        &wire.tracestate,
    ) {
        return Err(ContractError::new(
            "JOB_RESULT_TRACE_INVALID",
            "result trace propagation context is malformed",
        ));
    }
    let managed_service = if wire.source_domain == "MANAGED_SERVICE" {
        Some(decode_managed_service_result(&wire, job_id)?)
    } else {
        None
    };
    Ok(ValidatedResult {
        wire,
        job_id,
        managed_service,
    })
}

fn decode_managed_service_result(
    wire: &job_proto::JobExecutionResultProto,
    job_id: Uuid,
) -> Result<ValidatedManagedServiceResult, ContractError> {
    if wire.result_payload_schema_version != MANAGED_SERVICE_RESULT_SCHEMA_VERSION {
        return Err(ContractError::new(
            "MANAGED_SERVICE_RESULT_SCHEMA_INVALID",
            "managed service result schema version is not supported",
        ));
    }
    let inner = ManagedServiceResultV1::decode(wire.result_payload.as_slice()).map_err(|_| {
        ContractError::new(
            "MANAGED_SERVICE_RESULT_PROTO_INVALID",
            "managed service result payload is not valid ManagedServiceResultV1",
        )
    })?;
    let result_event_id = required_uuid(&inner.result_event_id)?;
    let source_event_id = required_uuid(&inner.source_command_event_id)?;
    let operation_id = required_uuid(&inner.operation_id)?;
    let instance_id = required_uuid(&inner.instance_id)?;
    let zone_id = required_uuid(&inner.zone_id)?;
    let instance_revision_id = required_uuid(&inner.instance_revision_id)?;
    let blueprint_revision_id = required_uuid(&inner.blueprint_revision_id)?;
    if result_event_id == source_event_id || source_event_id != job_id {
        return Err(ContractError::new(
            "MANAGED_SERVICE_RESULT_SOURCE_INVALID",
            "managed service result does not match its source command event",
        ));
    }
    if inner.schema_version != MANAGED_SERVICE_RESULT_SCHEMA_VERSION
        || inner.attempt != wire.attempt
        || inner.attempt > MAX_MANAGED_SERVICE_ATTEMPT
        || inner.generation == 0
        || inner.observed_state_version != inner.generation
        || inner.completed_at_unix_ms <= 0
        || inner.delivery_epoch > i64::MAX as u64
        || inner.generation > i64::MAX as u64
    {
        return Err(ContractError::new(
            "MANAGED_SERVICE_RESULT_FENCE_INVALID",
            "managed service result contains an invalid generation, attempt, epoch, or version fence",
        ));
    }
    if [
        inner.bundle_hash.as_slice(),
        inner.component_contract_hash.as_slice(),
        inner.input_hash.as_slice(),
        inner.desired_spec_hash.as_slice(),
    ]
    .iter()
    .any(|hash| hash.len() != 32)
    {
        return Err(ContractError::new(
            "MANAGED_SERVICE_RESULT_HASH_INVALID",
            "managed service result hash fence must contain 32 bytes",
        ));
    }
    // The command does not yet carry a safe-output schema. Accepting fields
    // before that schema is pinned would let Dataplane invent a new public
    // data contract and potentially return Secret-derived material.
    if !inner.safe_observed_output.is_empty() {
        return Err(ContractError::new(
            "MANAGED_SERVICE_RESULT_OUTPUT_INVALID",
            "managed service V1 safe observed output must be empty",
        ));
    }
    if inner.error_code.len() > MAX_ERROR_CODE_BYTES
        || inner.sanitized_message.len() > 1_024
        || inner.traceparent != wire.traceparent
        || inner.tracestate != wire.tracestate
        || !crate::observability::otel::OtelTracer::is_valid_propagation_context(
            &inner.traceparent,
            &inner.tracestate,
        )
    {
        return Err(ContractError::new(
            "MANAGED_SERVICE_RESULT_FIELD_INVALID",
            "managed service result contains malformed bounded or trace fields",
        ));
    }

    let outcome = ManagedServiceOutcomeV1::try_from(inner.outcome).map_err(|_| {
        ContractError::new(
            "MANAGED_SERVICE_RESULT_OUTCOME_INVALID",
            "managed service result outcome is unknown",
        )
    })?;
    let observed = ManagedServiceObservedStateV1::try_from(inner.observed_state).map_err(|_| {
        ContractError::new(
            "MANAGED_SERVICE_RESULT_OBSERVED_STATE_INVALID",
            "managed service observed state is unknown",
        )
    })?;
    let (status, error_code) = match (outcome, observed) {
        (
            ManagedServiceOutcomeV1::ManagedServiceOutcomeSucceeded,
            ManagedServiceObservedStateV1::ManagedServiceObservedStateReady,
        ) if wire.result_status == "SUCCEEDED"
            && inner.error_code.is_empty()
            && wire.error_code.is_none() =>
        {
            ("SUCCEEDED", None)
        }
        (
            ManagedServiceOutcomeV1::ManagedServiceOutcomeTerminalFailure,
            ManagedServiceObservedStateV1::ManagedServiceObservedStateDegraded,
        ) if wire.result_status == "FAILED"
            && !inner.error_code.is_empty()
            && wire.error_code.as_deref() == Some(inner.error_code.as_str()) =>
        {
            ("FAILED", Some(inner.error_code.clone()))
        }
        (
            ManagedServiceOutcomeV1::ManagedServiceOutcomeTerminalFailure,
            ManagedServiceObservedStateV1::ManagedServiceObservedStateUnknown,
        ) if wire.result_status == "FAILED"
            && !inner.error_code.is_empty()
            && wire.error_code.as_deref() == Some(inner.error_code.as_str()) =>
        {
            ("FAILED", Some(inner.error_code.clone()))
        }
        _ => {
            return Err(ContractError::new(
                "MANAGED_SERVICE_RESULT_OUTCOME_INVALID",
                "managed service inner outcome does not match the outer terminal result",
            ))
        }
    };
    Ok(ValidatedManagedServiceResult {
        source_command_event_id: source_event_id,
        operation_id,
        instance_id,
        zone_id,
        generation: inner.generation as i64,
        attempt: inner.attempt as i16,
        instance_revision_id,
        blueprint_revision_id,
        bundle_hash: inner.bundle_hash,
        component_contract_hash: inner.component_contract_hash,
        input_hash: inner.input_hash,
        desired_spec_hash: inner.desired_spec_hash,
        status,
        error_code,
        sanitized_message: inner.sanitized_message,
        delivery_epoch: inner.delivery_epoch as i64,
    })
}

fn required_uuid(bytes: &[u8]) -> Result<Uuid, ContractError> {
    let value = Uuid::from_slice(bytes).map_err(|_| {
        ContractError::new(
            "MANAGED_SERVICE_RESULT_ID_INVALID",
            "managed service result UUID field must contain 16 bytes",
        )
    })?;
    if value.is_nil() {
        return Err(ContractError::new(
            "MANAGED_SERVICE_RESULT_ID_INVALID",
            "managed service result UUID field cannot be nil",
        ));
    }
    Ok(value)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn valid_wire() -> job_proto::JobExecutionResultProto {
        job_proto::JobExecutionResultProto {
            job_id: Uuid::new_v4().as_bytes().to_vec(),
            job_version: 1,
            attempt: 1,
            result_status: "SUCCEEDED".to_string(),
            job_topic: "storage.bucket.create".to_string(),
            trace_id: Vec::new(),
            error_code: None,
            message: String::new(),
            source_domain: "STORAGE".to_string(),
            traceparent: String::new(),
            tracestate: String::new(),
            result_payload: Vec::new(),
            result_payload_schema_version: 0,
        }
    }

    #[test]
    fn rejects_unregistered_source_topic_pair() {
        let mut wire = valid_wire();
        wire.source_domain = "MAIL".to_string();
        let payload = wire.encode_to_vec();
        assert_eq!(
            decode(&payload).unwrap_err().code,
            "JOB_RESULT_ROUTE_INVALID"
        );
    }

    #[test]
    fn decodes_valid_result_once() {
        let payload = valid_wire().encode_to_vec();
        assert_eq!(decode(&payload).unwrap().wire.job_version, 1);
    }

    #[test]
    fn managed_service_result_route_is_admitted_for_settlement_phase() {
        let mut wire = valid_wire();
        wire.source_domain = "MANAGED_SERVICE".to_string();
        wire.job_topic = "managed_service.instance.execute".to_string();
        wire.result_payload_schema_version = 1;
        wire.result_payload = ManagedServiceResultV1 {
            result_event_id: Uuid::new_v4().as_bytes().to_vec(),
            source_command_event_id: wire.job_id.clone(),
            operation_id: Uuid::new_v4().as_bytes().to_vec(),
            instance_id: Uuid::new_v4().as_bytes().to_vec(),
            zone_id: Uuid::new_v4().as_bytes().to_vec(),
            generation: 1,
            attempt: wire.attempt,
            instance_revision_id: Uuid::new_v4().as_bytes().to_vec(),
            blueprint_revision_id: Uuid::new_v4().as_bytes().to_vec(),
            bundle_hash: vec![1; 32],
            component_contract_hash: vec![2; 32],
            input_hash: vec![3; 32],
            desired_spec_hash: vec![4; 32],
            outcome: ManagedServiceOutcomeV1::ManagedServiceOutcomeSucceeded as i32,
            error_code: String::new(),
            sanitized_message: String::new(),
            observed_state: ManagedServiceObservedStateV1::ManagedServiceObservedStateReady as i32,
            safe_observed_output: Vec::new(),
            observed_state_version: 1,
            schema_version: 1,
            completed_at_unix_ms: 1,
            traceparent: String::new(),
            tracestate: String::new(),
            delivery_epoch: 0,
        }
        .encode_to_vec();
        let payload = wire.encode_to_vec();
        let validated = decode(&payload).unwrap();
        assert_eq!(validated.wire.source_domain, "MANAGED_SERVICE");
        assert_eq!(validated.managed_service.unwrap().generation, 1);
    }
}
