use prost::Message;
use uuid::Uuid;

use crate::infra::kafka::managed_service_proto::{
    ManagedServiceObservedStateV1, ManagedServiceOutcomeV1, ManagedServiceResultV1,
};
use crate::job_runtime::model::ValidatedJob;

use super::entity::{ManagedServiceCommand, ManagedServiceFailure, ManagedServiceObservedState};

pub(crate) fn terminal_result(
    command: &ManagedServiceCommand,
    job: &ValidatedJob,
    failure: ManagedServiceFailure,
) -> Vec<u8> {
    let outcome = if failure.code.is_empty() {
        ManagedServiceOutcomeV1::ManagedServiceOutcomeSucceeded
    } else {
        ManagedServiceOutcomeV1::ManagedServiceOutcomeTerminalFailure
    };
    let observed_state = match failure.observed_state {
        ManagedServiceObservedState::Ready => {
            ManagedServiceObservedStateV1::ManagedServiceObservedStateReady
        }
        ManagedServiceObservedState::Degraded => {
            ManagedServiceObservedStateV1::ManagedServiceObservedStateDegraded
        }
        ManagedServiceObservedState::Unknown => {
            ManagedServiceObservedStateV1::ManagedServiceObservedStateUnknown
        }
    };
    let result_event_id = Uuid::new_v5(
        &Uuid::NAMESPACE_URL,
        format!(
            "aurora:managed-service:{}:{}:{}:{}:{}",
            command.command_event_id,
            command.operation_id,
            job.delivery_epoch,
            job.attempt,
            failure.code
        )
        .as_bytes(),
    );
    // The V1 command intentionally carries no safe-observed-output schema.
    // Returning guessed Kubernetes fields would create a second undeclared
    // contract surface, so P06 emits an empty snapshot. Namespace, service
    // names and protected selectors are deterministic CP detail projections.
    ManagedServiceResultV1 {
        result_event_id: result_event_id.as_bytes().to_vec(),
        source_command_event_id: command.command_event_id.as_bytes().to_vec(),
        operation_id: command.operation_id.as_bytes().to_vec(),
        instance_id: command.instance_id.as_bytes().to_vec(),
        zone_id: command.zone_id.as_bytes().to_vec(),
        generation: command.generation,
        attempt: job.attempt,
        instance_revision_id: command.instance_revision_id.as_bytes().to_vec(),
        blueprint_revision_id: command.blueprint_revision_id.as_bytes().to_vec(),
        bundle_hash: command.bundle_hash.to_vec(),
        component_contract_hash: command.component_contract_hash.to_vec(),
        input_hash: command.input_hash.to_vec(),
        desired_spec_hash: command.desired_spec_hash.to_vec(),
        outcome: outcome as i32,
        error_code: failure.code.to_owned(),
        sanitized_message: failure.message.to_owned(),
        observed_state: observed_state as i32,
        safe_observed_output: Vec::new(),
        observed_state_version: command.generation,
        schema_version: 1,
        completed_at_unix_ms: chrono::Utc::now().timestamp_millis(),
        traceparent: job.traceparent.clone(),
        tracestate: job.tracestate.clone(),
        delivery_epoch: job.delivery_epoch,
    }
    .encode_to_vec()
}
