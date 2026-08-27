use crate::results::contract::ValidatedManagedServiceResult;
use tokio_postgres::{Client, Row};

const JOB_TOPIC: &str = "managed_service.instance.execute";

pub async fn apply_result(
    client: &mut Client,
    result: &ValidatedManagedServiceResult,
) -> Result<Option<Row>, Box<dyn std::error::Error>> {
    let tx = client.transaction().await?;
    let authority = tx
        .query_opt(
            "SELECT outbox.status, outbox.owner_type, outbox.zone_id, outbox.delivery_epoch, \
                    operation.id, operation.kind::text, operation.state::text, operation.generation, \
                    operation.attempt, operation.delivery_epoch, operation.current_command_event_id, \
                    operation.target_revision_id, operation.blueprint_revision_id, operation.zone_id, \
                    operation.template_bundle_sha256, operation.component_contract_sha256, \
                    operation.input_sha256, operation.desired_spec_sha256, \
                    instance.generation, instance.zone_id, instance.pending_revision_id, instance.state::text \
             FROM managed_service.managed_service_outbox_records outbox \
             JOIN managed_service.personal_managed_service_operations operation \
               ON operation.current_command_event_id = outbox.event_id \
              AND operation.instance_id::text = outbox.resource_id \
             JOIN managed_service.personal_managed_service_instances instance \
               ON instance.id = operation.instance_id \
             WHERE outbox.event_id = $1 AND outbox.job_topic = $2 \
             FOR UPDATE OF outbox, operation, instance",
            &[&result.source_command_event_id, &JOB_TOPIC],
        )
        .await?;
    let Some(authority) = authority else {
        tx.rollback().await?;
        return Ok(None);
    };
    let outbox_status: String = authority.get(0);
    let owner_type: String = authority.get(1);
    let outbox_zone_id: uuid::Uuid = authority.get(2);
    let outbox_epoch: i64 = authority.get(3);
    let operation_id: uuid::Uuid = authority.get(4);
    let operation_kind: String = authority.get(5);
    let operation_state: String = authority.get(6);
    let operation_generation: i64 = authority.get(7);
    let operation_attempt: i16 = authority.get(8);
    let operation_epoch: i64 = authority.get(9);
    let current_event: uuid::Uuid = authority.get(10);
    let target_revision_id: uuid::Uuid = authority.get(11);
    let blueprint_revision_id: uuid::Uuid = authority.get(12);
    let operation_zone_id: uuid::Uuid = authority.get(13);
    let bundle_hash: Vec<u8> = authority.get(14);
    let component_contract_hash: Vec<u8> = authority.get(15);
    let input_hash: Vec<u8> = authority.get(16);
    let desired_spec_hash: Vec<u8> = authority.get(17);
    let instance_generation: i64 = authority.get(18);
    let instance_zone_id: uuid::Uuid = authority.get(19);
    let pending_revision_id: Option<uuid::Uuid> = authority.get(20);
    let instance_state: String = authority.get(21);
    if owner_type != "PERSONAL"
        || !matches!(outbox_status.as_str(), "PENDING" | "PROCESSING")
        || !matches!(
            operation_state.as_str(),
            "accepted" | "dispatching" | "running" | "retrying"
        )
        || current_event != result.source_command_event_id
        || operation_id != result.operation_id
        || operation_generation != instance_generation
        || operation_generation != result.generation
        || operation_epoch != outbox_epoch
        || operation_epoch != result.delivery_epoch
        // DP owns automatic retries within this delivery epoch. The database
        // records the last accepted result, not every intermediate execution.
        || operation_attempt > result.attempt
        || outbox_zone_id != result.zone_id
        || operation_zone_id != result.zone_id
        || instance_zone_id != result.zone_id
        || target_revision_id != result.instance_revision_id
        || blueprint_revision_id != result.blueprint_revision_id
        || bundle_hash != result.bundle_hash
        || component_contract_hash != result.component_contract_hash
        || input_hash != result.input_hash
        || desired_spec_hash != result.desired_spec_hash
        || (matches!(operation_kind.as_str(), "create" | "resize")
            && pending_revision_id != Some(result.instance_revision_id))
        || match operation_kind.as_str() {
            "create" => instance_state != "provisioning",
            "resize" => instance_state != "updating",
            "delete" => instance_state != "deleting",
            _ => true,
        }
    {
        tx.rollback().await?;
        return Ok(None);
    }

    let instance_id = result.instance_id;
    let job_id = result.source_command_event_id;
    let attempt = result.attempt;
    let job_topic = JOB_TOPIC;
    let row = if result.status == "SUCCEEDED" {
        match operation_kind.as_str() {
            "create" | "resize" => {
                tx.query_opt(
                    "WITH promoted AS ( \
                         UPDATE managed_service.personal_managed_service_instances \
                         SET state = 'active', active_revision_id = pending_revision_id, \
                             pending_revision_id = NULL, updated_at = NOW() \
                         WHERE id = $1 AND generation = $2 AND pending_revision_id IS NOT NULL \
                           AND (($7 = 'create' AND state = 'provisioning') OR ($7 = 'resize' AND state = 'updating')) \
                         RETURNING id \
                     ), updated_operation AS ( \
                         UPDATE managed_service.personal_managed_service_operations \
                         SET state = 'succeeded', attempt = $3, completed_at = NOW(), \
                             last_error_code = NULL, last_sanitized_error = NULL, \
                             status_version = status_version + 1, updated_at = NOW() \
                         WHERE id = $4 AND current_command_event_id = $5 \
                           AND state IN ('accepted','dispatching','running','retrying') \
                           AND EXISTS (SELECT 1 FROM promoted) \
                         RETURNING id \
                     ) \
                     UPDATE managed_service.managed_service_outbox_records outbox \
                     SET status = 'SUCCEEDED', completed_at = NOW(), error_code = NULL, \
                         error_message = NULL, updated_at = NOW() \
                     WHERE outbox.event_id = $5 AND outbox.job_topic = $6 \
                       AND outbox.status IN ('PENDING','PROCESSING') \
                       AND EXISTS (SELECT 1 FROM updated_operation) \
                     RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.resource_id, $5::uuid",
                    &[&instance_id, &instance_generation, &attempt, &operation_id, &job_id, &job_topic, &operation_kind],
                )
                .await?
            }
            "delete" => {
                tx.query_opt(
                    "WITH fenced AS ( \
                         INSERT INTO managed_service.personal_managed_service_deletion_fences \
                           (instance_id, operation_id, generation, zone_id, retained_until) \
                         SELECT $1, $2, $3, zone_id, GREATEST(retained_until, NOW() + interval '30 days') \
                         FROM managed_service.personal_managed_service_operations \
                         WHERE id = $2 ON CONFLICT (instance_id, operation_id, generation) DO NOTHING \
                         RETURNING instance_id \
                     ), deleted_revisions AS ( \
                         DELETE FROM managed_service.personal_managed_service_instance_revisions \
                         WHERE instance_id = $1 AND EXISTS (SELECT 1 FROM fenced) RETURNING id \
                     ), deleted_instance AS ( \
                         DELETE FROM managed_service.personal_managed_service_instances \
                         WHERE id = $1 AND state = 'deleting' AND EXISTS (SELECT 1 FROM fenced) \
                         RETURNING id \
                     ), updated_operation AS ( \
                         UPDATE managed_service.personal_managed_service_operations \
                         SET state = 'succeeded', attempt = $4, completed_at = NOW(), \
                             last_error_code = NULL, last_sanitized_error = NULL, \
                             status_version = status_version + 1, updated_at = NOW() \
                         WHERE id = $2 AND current_command_event_id = $5 \
                           AND state IN ('accepted','dispatching','running','retrying') \
                           AND EXISTS (SELECT 1 FROM deleted_instance) \
                         RETURNING id \
                     ) \
                     UPDATE managed_service.managed_service_outbox_records outbox \
                     SET status = 'SUCCEEDED', completed_at = NOW(), error_code = NULL, \
                         error_message = NULL, updated_at = NOW() \
                     WHERE outbox.event_id = $5 AND outbox.job_topic = $6 \
                       AND outbox.status IN ('PENDING','PROCESSING') \
                       AND EXISTS (SELECT 1 FROM updated_operation) \
                     RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.resource_id, $5::uuid",
                    &[&instance_id, &operation_id, &operation_generation, &attempt, &job_id, &job_topic],
                )
                .await?
            }
            _ => return Err(format!("unsupported personal operation kind '{operation_kind}'").into()),
        }
    } else {
        tx.query_opt(
            "WITH updated_instance AS ( \
                 UPDATE managed_service.personal_managed_service_instances \
                 SET state = CASE WHEN $3 = 'resize' THEN 'active'::managed_service.managed_service_instance_state ELSE state END, \
                     pending_revision_id = CASE WHEN $3 = 'resize' THEN NULL ELSE pending_revision_id END, \
                     updated_at = NOW() \
                 WHERE id = $1 AND generation = $2 \
                   AND (($3 = 'create' AND state = 'provisioning') \
                     OR ($3 = 'resize' AND state = 'updating') \
                     OR ($3 = 'delete' AND state = 'deleting')) \
                 RETURNING id \
             ), updated_operation AS ( \
                 UPDATE managed_service.personal_managed_service_operations \
                 SET state = 'terminal_failed', attempt = $5, completed_at = NOW(), \
                     last_error_code = $6, last_sanitized_error = LEFT($7, 1024), \
                     status_version = status_version + 1, updated_at = NOW() \
                 WHERE id = $4 AND current_command_event_id = $8 \
                   AND state IN ('accepted','dispatching','running','retrying') \
                   AND EXISTS (SELECT 1 FROM updated_instance) RETURNING id \
             ) \
             UPDATE managed_service.managed_service_outbox_records outbox \
             SET status = 'FAILED', completed_at = NOW(), error_code = $6, \
                 error_message = LEFT($7, 1024), updated_at = NOW() \
             WHERE outbox.event_id = $8 AND outbox.job_topic = $9 \
               AND outbox.status IN ('PENDING','PROCESSING') \
               AND EXISTS (SELECT 1 FROM updated_operation) \
             RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.resource_id, $8::uuid",
            &[
                &instance_id,
                &instance_generation,
                &operation_kind,
                &operation_id,
                &attempt,
                &result.error_code,
                &result.sanitized_message,
                &job_id,
                &job_topic,
            ],
        )
        .await?
    };
    tx.commit().await?;
    Ok(row)
}
