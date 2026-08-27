use crate::observability::logger::Logger;

/// Settles `storage.credential.delete` without allowing the durable outbox to lead
/// the real resource. Controlplane has already moved the credential to DELETING;
/// JO hard-deletes only that owner branch after Dataplane confirms success.
pub async fn resolve_credential_deletion(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.resolve_credential_deletion",
        &format!(
            "Khép lại vòng đời Outbox cho Credential Job: {} -> {}",
            job_uuid, status
        ),
    );

    let owner = pg_client
        .query_opt(
            "SELECT owner_type FROM storage.storage_outbox_records \
             WHERE event_id = $1::uuid AND job_topic = $2 \
               AND status IN ('PENDING', 'PROCESSING')",
            &[&job_uuid, &job_topic],
        )
        .await?;
    let Some(owner) = owner else {
        return Ok(None);
    };
    let owner_type: String = owner.get(0);

    if status == "PROCESSING" {
        return pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = 'PROCESSING', updated_at = NOW(), error_code = NULL, error_message = NULL \
                 WHERE event_id = $1::uuid AND job_topic = $2 \
                   AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&job_uuid, &job_topic],
            )
            .await;
    }

    if status == "SUCCEEDED" {
        return match owner_type.as_str() {
            "PERSONAL" => pg_client
                .query_opt(
                    "WITH locked_outbox AS MATERIALIZED ( \
                         SELECT event_id, resource_id::uuid AS resource_id \
                         FROM storage.storage_outbox_records \
                         WHERE event_id = $1::uuid AND job_topic = $2 AND owner_type = 'PERSONAL' \
                           AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                     ), deleted_credential AS ( \
                         DELETE FROM storage.personal_credentials credential \
                         USING locked_outbox locked \
                         WHERE credential.id = locked.resource_id AND credential.state = 'DELETING' \
                         RETURNING credential.id \
                     ) \
                     UPDATE storage.storage_outbox_records outbox \
                     SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW(), \
                         error_code = NULL, error_message = NULL \
                     FROM locked_outbox locked \
                     WHERE outbox.event_id = locked.event_id \
                       AND EXISTS (SELECT 1 FROM deleted_credential) \
                     RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                    &[&job_uuid, &job_topic],
                )
                .await,
            "TENANT" => pg_client
                .query_opt(
                    "WITH locked_outbox AS MATERIALIZED ( \
                         SELECT event_id, resource_id::uuid AS resource_id \
                         FROM storage.storage_outbox_records \
                         WHERE event_id = $1::uuid AND job_topic = $2 AND owner_type = 'TENANT' \
                           AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                     ), deleted_credential AS ( \
                         DELETE FROM storage.tenant_credentials credential \
                         USING locked_outbox locked \
                         WHERE credential.id = locked.resource_id AND credential.state = 'DELETING' \
                         RETURNING credential.id \
                     ) \
                     UPDATE storage.storage_outbox_records outbox \
                     SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW(), \
                         error_code = NULL, error_message = NULL \
                     FROM locked_outbox locked \
                     WHERE outbox.event_id = locked.event_id \
                       AND EXISTS (SELECT 1 FROM deleted_credential) \
                     RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                    &[&job_uuid, &job_topic],
                )
                .await,
            _ => Ok(None),
        };
    }

    match owner_type.as_str() {
        "PERSONAL" => pg_client
            .query_opt(
                "WITH locked_outbox AS MATERIALIZED ( \
                     SELECT event_id, resource_id::uuid AS resource_id \
                     FROM storage.storage_outbox_records \
                     WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'PERSONAL' \
                       AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                 ), restored_credential AS ( \
                     UPDATE storage.personal_credentials credential \
                     SET state = 'READY', updated_at = NOW() \
                     FROM locked_outbox locked \
                     WHERE credential.id = locked.resource_id AND credential.state = 'DELETING' \
                     RETURNING credential.id \
                 ) \
                 UPDATE storage.storage_outbox_records outbox \
                 SET status = $1, completed_at = NOW(), updated_at = NOW(), \
                     error_code = $2, error_message = $3 \
                 FROM locked_outbox locked \
                 WHERE outbox.event_id = locked.event_id \
                   AND EXISTS (SELECT 1 FROM restored_credential) \
                 RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                &[&status, &error_code, &error_message, &job_uuid, &job_topic],
            )
            .await,
        "TENANT" => pg_client
            .query_opt(
                "WITH locked_outbox AS MATERIALIZED ( \
                     SELECT event_id, resource_id::uuid AS resource_id \
                     FROM storage.storage_outbox_records \
                     WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'TENANT' \
                       AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                 ), restored_credential AS ( \
                     UPDATE storage.tenant_credentials credential \
                     SET state = 'READY', updated_at = NOW() \
                     FROM locked_outbox locked \
                     WHERE credential.id = locked.resource_id AND credential.state = 'DELETING' \
                     RETURNING credential.id \
                 ) \
                 UPDATE storage.storage_outbox_records outbox \
                 SET status = $1, completed_at = NOW(), updated_at = NOW(), \
                     error_code = $2, error_message = $3 \
                 FROM locked_outbox locked \
                 WHERE outbox.event_id = locked.event_id \
                   AND EXISTS (SELECT 1 FROM restored_credential) \
                 RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                &[&status, &error_code, &error_message, &job_uuid, &job_topic],
            )
            .await,
        _ => Ok(None),
    }
}

/// Settles `storage.credential.create`. The credential row is retained as the
/// single source of truth: CREATING becomes READY on success or ERROR on a
/// terminal Dataplane failure. The outbox is settled only after that transition.
pub async fn resolve_credential_creation(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.resolve_credential_creation",
        &format!(
            "Khép lại vòng đời Outbox cho Credential Create Job: {} -> {}",
            job_uuid, status
        ),
    );

    let owner = pg_client
        .query_opt(
            "SELECT owner_type FROM storage.storage_outbox_records \
             WHERE event_id = $1::uuid AND job_topic = $2 \
               AND status IN ('PENDING', 'PROCESSING')",
            &[&job_uuid, &job_topic],
        )
        .await?;
    let Some(owner) = owner else {
        return Ok(None);
    };
    let owner_type: String = owner.get(0);

    if status == "PROCESSING" {
        return pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = 'PROCESSING', updated_at = NOW(), error_code = NULL, error_message = NULL \
                 WHERE event_id = $1::uuid AND job_topic = $2 \
                   AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&job_uuid, &job_topic],
            )
            .await;
    }

    let target_state = if status == "SUCCEEDED" {
        "READY"
    } else {
        "ERROR"
    };

    match owner_type.as_str() {
        "PERSONAL" => pg_client
            .query_opt(
                "WITH locked_outbox AS MATERIALIZED ( \
                     SELECT event_id, resource_id::uuid AS resource_id \
                     FROM storage.storage_outbox_records \
                     WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'PERSONAL' \
                       AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                 ), transitioned_credential AS ( \
                     UPDATE storage.personal_credentials credential \
                     SET state = $6, updated_at = NOW() \
                     FROM locked_outbox locked \
                     WHERE credential.id = locked.resource_id AND credential.state = 'CREATING' \
                     RETURNING credential.id \
                 ) \
                 UPDATE storage.storage_outbox_records outbox \
                 SET status = $1, completed_at = NOW(), updated_at = NOW(), \
                     error_code = $2, error_message = $3 \
                 FROM locked_outbox locked \
                 WHERE outbox.event_id = locked.event_id \
                   AND EXISTS (SELECT 1 FROM transitioned_credential) \
                 RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                &[
                    &status,
                    &error_code,
                    &error_message,
                    &job_uuid,
                    &job_topic,
                    &target_state,
                ],
            )
            .await,
        "TENANT" => pg_client
            .query_opt(
                "WITH locked_outbox AS MATERIALIZED ( \
                     SELECT event_id, resource_id::uuid AS resource_id \
                     FROM storage.storage_outbox_records \
                     WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'TENANT' \
                       AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                 ), transitioned_credential AS ( \
                     UPDATE storage.tenant_credentials credential \
                     SET state = $6, updated_at = NOW() \
                     FROM locked_outbox locked \
                     WHERE credential.id = locked.resource_id AND credential.state = 'CREATING' \
                     RETURNING credential.id \
                 ) \
                 UPDATE storage.storage_outbox_records outbox \
                 SET status = $1, completed_at = NOW(), updated_at = NOW(), \
                     error_code = $2, error_message = $3 \
                 FROM locked_outbox locked \
                 WHERE outbox.event_id = locked.event_id \
                   AND EXISTS (SELECT 1 FROM transitioned_credential) \
                 RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                &[
                    &status,
                    &error_code,
                    &error_message,
                    &job_uuid,
                    &job_topic,
                    &target_state,
                ],
            )
            .await,
        _ => Ok(None),
    }
}
