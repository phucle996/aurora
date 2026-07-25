use prost::Message;
use std::fmt::{Display, Formatter};
use uuid::Uuid;

const MAX_RESULT_PAYLOAD_BYTES: usize = 64 * 1024;
const MAX_RESULT_MESSAGE_BYTES: usize = 16 * 1024;
const MAX_TOPIC_BYTES: usize = 128;
const MAX_ERROR_CODE_BYTES: usize = 128;
const MAX_ATTEMPT: u32 = 10_000;

pub mod job_proto {
    include!(concat!(env!("OUT_DIR"), "/job_lifecycle.rs"));
}

#[derive(Debug)]
pub struct ValidatedResult {
    pub wire: job_proto::JobExecutionResultProto,
    pub job_id: Uuid,
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
        || !crate::job_topics::is_registered(&wire.source_domain, &wire.job_topic)
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
    if wire.message.len() > MAX_RESULT_MESSAGE_BYTES
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
    if !crate::observability::otel::OtelTracer::is_valid_propagation_context(
        &wire.traceparent,
        &wire.tracestate,
    ) {
        return Err(ContractError::new(
            "JOB_RESULT_TRACE_INVALID",
            "result trace propagation context is malformed",
        ));
    }
    Ok(ValidatedResult { wire, job_id })
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
}
