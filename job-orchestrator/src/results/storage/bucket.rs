use crate::contracts::storage as storage_proto;
use crate::observability::logger::Logger;
use prost::Message;

/// [COMMENT]: Xử lý khép lại vòng đời của job `storage.bucket.create` với CTE cách ly theo Owner Type.
/// SUCCEEDED: UPDATE outbox thành SUCCEEDED và chuyển bucket status thành READY (chỉ cập nhật đúng bảng của owner_type).
/// PROCESSING: UPDATE outbox thành PROCESSING.
/// FAILED: UPDATE outbox thành FAILED và chuyển bucket status thành FAILED.
pub async fn resolve_bucket_creation(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.resolve_bucket_creation",
        &format!(
            "Khép lại vòng đời Outbox cho Bucket Job: {} -> {}",
            job_uuid, status
        ),
    );

    let outbox_meta = pg_client
        .query_opt(
            "SELECT owner_type FROM storage.storage_outbox_records \
             WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING')",
            &[&job_uuid, &job_topic],
        )
        .await?;

    let Some(outbox_meta) = outbox_meta else {
        return Ok(None);
    };
    let owner_type: String = outbox_meta.get(0);

    let row_opt = if status == "SUCCEEDED" {
        match owner_type.as_str() {
            "PERSONAL" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $1::uuid AND job_topic = $2 AND owner_type = 'PERSONAL' \
                               AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                          ), \
                         ready_personal AS ( \
                             UPDATE storage.personal_buckets bucket \
                             SET status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'PROVISIONING' \
                             RETURNING id \
                         ), ready_personal_credentials AS ( \
                             UPDATE storage.personal_credentials credential \
                             SET state = 'READY', updated_at = NOW() \
                             WHERE credential.bucket_id IN (SELECT id FROM ready_personal) AND credential.state = 'CREATING' \
                             RETURNING credential.id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW(), error_code = NULL, error_message = NULL \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM ready_personal) \
                           AND EXISTS (SELECT 1 FROM ready_personal_credentials) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&job_uuid, &job_topic],
                    )
                    .await?
            }
            "TENANT" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $1::uuid AND job_topic = $2 AND owner_type = 'TENANT' \
                               AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                          ), \
                         ready_tenant AS ( \
                             UPDATE storage.tenant_buckets bucket \
                             SET status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'PROVISIONING' \
                             RETURNING id \
                         ), ready_tenant_credentials AS ( \
                             UPDATE storage.tenant_credentials credential \
                             SET state = 'READY', updated_at = NOW() \
                             WHERE credential.bucket_id IN (SELECT id FROM ready_tenant) AND credential.state = 'CREATING' \
                             RETURNING credential.id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW(), error_code = NULL, error_message = NULL \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM ready_tenant) \
                           AND EXISTS (SELECT 1 FROM ready_tenant_credentials) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&job_uuid, &job_topic],
                    )
                    .await?
            }
            _ => None,
        }
    } else if status == "PROCESSING" {
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        match owner_type.as_str() {
            "PERSONAL" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'PERSONAL' \
                               AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                         ), \
                         failed_personal AS ( \
                             UPDATE storage.personal_buckets bucket \
                             SET status = 'FAILED', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'PROVISIONING' \
                             RETURNING bucket.id \
                         ), failed_personal_credentials AS ( \
                             UPDATE storage.personal_credentials credential \
                             SET state = 'ERROR', updated_at = NOW() \
                             WHERE credential.bucket_id IN (SELECT id FROM failed_personal) \
                               AND credential.state = 'CREATING' \
                             RETURNING credential.id \
                         ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = $1, completed_at = NOW(), updated_at = NOW(), \
                             error_code = $2, error_message = $3 \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id \
                           AND EXISTS (SELECT 1 FROM failed_personal) \
                           AND EXISTS (SELECT 1 FROM failed_personal_credentials) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&status, &error_code, &error_message, &job_uuid, &job_topic],
                    )
                    .await?
            }
            "TENANT" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'TENANT' \
                               AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                         ), \
                         failed_tenant AS ( \
                             UPDATE storage.tenant_buckets bucket \
                             SET status = 'FAILED', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'PROVISIONING' \
                             RETURNING bucket.id \
                         ), failed_tenant_credentials AS ( \
                             UPDATE storage.tenant_credentials credential \
                             SET state = 'ERROR', updated_at = NOW() \
                             WHERE credential.bucket_id IN (SELECT id FROM failed_tenant) \
                               AND credential.state = 'CREATING' \
                             RETURNING credential.id \
                         ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = $1, completed_at = NOW(), updated_at = NOW(), \
                             error_code = $2, error_message = $3 \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id \
                           AND EXISTS (SELECT 1 FROM failed_tenant) \
                           AND EXISTS (SELECT 1 FROM failed_tenant_credentials) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&status, &error_code, &error_message, &job_uuid, &job_topic],
                    )
                    .await?
            }
            _ => None,
        }
    };

    Ok(row_opt)
}

/// [COMMENT]: Xử lý khép lại vòng đời của job `storage.bucket.resize` với CTE cách ly theo Owner Type.
pub async fn resolve_bucket_resize(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
    result_payload: &[u8],
    result_payload_schema_version: u32,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
    // This resolver can settle only its own workflow's outbox rows.
    let job_topic = "storage.bucket.resize";
    Logger::sys_info(
        "storage_db.resolve_bucket_resize",
        &format!(
            "Khép lại vòng đời Outbox cho Bucket Resize Job: {} -> {}",
            job_uuid, status
        ),
    );

    let outbox_meta = pg_client
        .query_opt(
            "SELECT owner_type FROM storage.storage_outbox_records \
             WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING')",
            &[&job_uuid, &job_topic],
        )
        .await?;

    let Some(outbox_meta) = outbox_meta else {
        return Ok(None);
    };
    let owner_type: String = outbox_meta.get(0);

    let row_opt = if status == "SUCCEEDED" {
        if result_payload_schema_version != 1 {
            return Err("storage bucket quota result schema version is unsupported".into());
        }
        let result = storage_proto::BucketQuotaAppliedV1::decode(result_payload)?;
        let result_bucket_id = uuid::Uuid::parse_str(&result.bucket_id)?;
        if result.schema_version != 1 || result.actual_quota_bytes <= 0 {
            return Err("storage bucket quota result is invalid".into());
        }
        match owner_type.as_str() {
            "PERSONAL" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $1::uuid AND job_topic = $2 AND owner_type = 'PERSONAL' \
                               AND status IN ('PENDING', 'PROCESSING') AND resource_id::uuid = $3 \
                             FOR UPDATE \
                          ), \
                         updated_personal AS ( \
                             UPDATE storage.personal_buckets bucket \
                             SET capacity_quota_bytes = $4, status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW(), \
                             error_code = NULL, error_message = NULL \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM updated_personal) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&job_uuid, &job_topic, &result_bucket_id, &result.actual_quota_bytes],
                    )
                    .await?
            }
            "TENANT" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $1::uuid AND job_topic = $2 AND owner_type = 'TENANT' \
                               AND status IN ('PENDING', 'PROCESSING') AND resource_id::uuid = $3 \
                             FOR UPDATE \
                          ), \
                         updated_tenant AS ( \
                             UPDATE storage.tenant_buckets bucket \
                             SET capacity_quota_bytes = $4, status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW(), \
                             error_code = NULL, error_message = NULL \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM updated_tenant) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&job_uuid, &job_topic, &result_bucket_id, &result.actual_quota_bytes],
                    )
                    .await?
            }
            _ => None,
        }
    } else if status == "PROCESSING" {
        if !result_payload.is_empty() || result_payload_schema_version != 0 {
            return Err("PROCESSING storage bucket quota result must not carry a payload".into());
        }
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        if !result_payload.is_empty() || result_payload_schema_version != 0 {
            return Err("FAILED storage bucket quota result must not carry a payload".into());
        }
        match owner_type.as_str() {
            "PERSONAL" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'PERSONAL' \
                               AND status IN ('PENDING', 'PROCESSING') \
                             FOR UPDATE \
                         ), restored_personal AS ( \
                             UPDATE storage.personal_buckets bucket \
                             SET status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING bucket.id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = $1, completed_at = NOW(), updated_at = NOW(), \
                             error_code = $2, error_message = $3 \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = $4::uuid AND outbox.job_topic = $5 \
                           AND EXISTS (SELECT 1 FROM restored_personal) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&status, &error_code, &error_message, &job_uuid, &job_topic],
                    )
                    .await?
            }
            "TENANT" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'TENANT' \
                               AND status IN ('PENDING', 'PROCESSING') \
                             FOR UPDATE \
                         ), restored_tenant AS ( \
                             UPDATE storage.tenant_buckets bucket \
                             SET status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING bucket.id \
                         ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = $1, completed_at = NOW(), updated_at = NOW(), \
                             error_code = $2, error_message = $3 \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = $4::uuid AND outbox.job_topic = $5 \
                           AND EXISTS (SELECT 1 FROM restored_tenant) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&status, &error_code, &error_message, &job_uuid, &job_topic],
                    )
                    .await?
            }
            _ => None,
        }
    };

    Ok(row_opt)
}

/// [COMMENT]: Xử lý khép lại vòng đời của job `storage.bucket.versioning` với CTE cách ly theo Owner Type.
pub async fn resolve_bucket_versioning(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
    result_payload: &[u8],
    result_payload_schema_version: u32,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
    // This resolver can settle only its own workflow's outbox rows.
    let job_topic = "storage.bucket.versioning";
    Logger::sys_info(
        "storage_db.resolve_bucket_versioning",
        &format!(
            "Khép lại vòng đời Outbox cho Bucket Versioning Job: {} -> {}",
            job_uuid, status
        ),
    );

    let outbox_meta = pg_client
        .query_opt(
            "SELECT owner_type FROM storage.storage_outbox_records \
             WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING')",
            &[&job_uuid, &job_topic],
        )
        .await?;

    let Some(outbox_meta) = outbox_meta else {
        return Ok(None);
    };
    let owner_type: String = outbox_meta.get(0);

    let row_opt = if status == "SUCCEEDED" {
        if result_payload_schema_version != 1 {
            return Err("storage bucket versioning result schema version is unsupported".into());
        }
        let result = storage_proto::BucketVersioningAppliedV1::decode(result_payload)?;
        let result_bucket_id = uuid::Uuid::parse_str(&result.bucket_id)?;
        if result.schema_version != 1 {
            return Err("storage bucket versioning result is invalid".into());
        }
        match owner_type.as_str() {
            "PERSONAL" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $1::uuid AND job_topic = $2 AND owner_type = 'PERSONAL' \
                               AND status IN ('PENDING', 'PROCESSING') AND resource_id::uuid = $3 \
                             FOR UPDATE \
                          ), \
                         updated_personal AS ( \
                             UPDATE storage.personal_buckets bucket \
                             SET versioning_enabled = $4, status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW(), \
                             error_code = NULL, error_message = NULL \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM updated_personal) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&job_uuid, &job_topic, &result_bucket_id, &result.actual_versioning_enabled],
                    )
                    .await?
            }
            "TENANT" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $1::uuid AND job_topic = $2 AND owner_type = 'TENANT' \
                               AND status IN ('PENDING', 'PROCESSING') AND resource_id::uuid = $3 \
                             FOR UPDATE \
                          ), \
                         updated_tenant AS ( \
                             UPDATE storage.tenant_buckets bucket \
                             SET versioning_enabled = $4, status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW(), \
                             error_code = NULL, error_message = NULL \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM updated_tenant) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&job_uuid, &job_topic, &result_bucket_id, &result.actual_versioning_enabled],
                    )
                    .await?
            }
            _ => None,
        }
    } else if status == "PROCESSING" {
        if !result_payload.is_empty() || result_payload_schema_version != 0 {
            return Err(
                "PROCESSING storage bucket versioning result must not carry a payload".into(),
            );
        }
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        if !result_payload.is_empty() || result_payload_schema_version != 0 {
            return Err("FAILED storage bucket versioning result must not carry a payload".into());
        }
        match owner_type.as_str() {
            "PERSONAL" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'PERSONAL' \
                               AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                          ), \
                         restored_personal AS ( \
                             UPDATE storage.personal_buckets bucket \
                             SET status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = $1, completed_at = NOW(), updated_at = NOW(), error_code = $2, error_message = $3 \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM restored_personal) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&status, &error_code, &error_message, &job_uuid, &job_topic],
                    )
                    .await?
            }
            "TENANT" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'TENANT' \
                               AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                          ), \
                         restored_tenant AS ( \
                             UPDATE storage.tenant_buckets bucket \
                             SET status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = $1, completed_at = NOW(), updated_at = NOW(), error_code = $2, error_message = $3 \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM restored_tenant) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&status, &error_code, &error_message, &job_uuid, &job_topic],
                    )
                    .await?
            }
            _ => None,
        }
    };

    Ok(row_opt)
}

/// [COMMENT]: Xử lý khép lại vòng đời của job `storage.bucket.lifecycle` với CTE cách ly theo Owner Type.
pub async fn resolve_bucket_lifecycle(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
    result_payload: &[u8],
    result_payload_schema_version: u32,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
    // This resolver can settle only its own workflow's outbox rows.
    let job_topic = "storage.bucket.lifecycle";
    Logger::sys_info(
        "storage_db.resolve_bucket_lifecycle",
        &format!(
            "Khép lại vòng đời Outbox cho Bucket Lifecycle Job: {} -> {}",
            job_uuid, status
        ),
    );

    let outbox_meta = pg_client
        .query_opt(
            "SELECT owner_type FROM storage.storage_outbox_records \
             WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING')",
            &[&job_uuid, &job_topic],
        )
        .await?;

    let Some(outbox_meta) = outbox_meta else {
        return Ok(None);
    };
    let owner_type: String = outbox_meta.get(0);

    let row_opt = if status == "SUCCEEDED" {
        if result_payload_schema_version != 1 {
            return Err("storage bucket lifecycle result schema version is unsupported".into());
        }
        let result = storage_proto::BucketLifecycleAppliedV1::decode(result_payload)?;
        let result_bucket_id = uuid::Uuid::parse_str(&result.bucket_id)?;
        if result.schema_version != 1
            || result.actual_rules.len() > 100
            || result.actual_rules.iter().any(|rule| {
                rule.id.trim().is_empty()
                    || rule.id.len() > 255
                    || rule.expiration_days < 0
                    || rule.noncurrent_version_expiration_days < 0
                    || rule.abort_incomplete_multipart_upload_days < 0
            })
        {
            return Err("storage bucket lifecycle result is invalid".into());
        }
        let actual_rules_json = serde_json::to_string(
            &result
                .actual_rules
                .iter()
                .map(|rule| {
                    serde_json::json!({
                        "id": rule.id,
                        "enabled": rule.enabled,
                        "prefix": rule.prefix,
                        "expiration_days": rule.expiration_days,
                        "noncurrent_version_expiration_days": rule.noncurrent_version_expiration_days,
                        "abort_incomplete_multipart_upload_days": rule.abort_incomplete_multipart_upload_days
                    })
                })
                .collect::<Vec<_>>(),
        )?;
        match owner_type.as_str() {
            "PERSONAL" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $1::uuid AND job_topic = $2 AND owner_type = 'PERSONAL' \
                               AND status IN ('PENDING', 'PROCESSING') AND resource_id::uuid = $3 \
                             FOR UPDATE \
                          ), \
                         updated_personal AS ( \
                             UPDATE storage.personal_buckets bucket \
                             SET lifecycle_rules = $4::text::jsonb, status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW(), \
                             error_code = NULL, error_message = NULL \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM updated_personal) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&job_uuid, &job_topic, &result_bucket_id, &actual_rules_json],
                    )
                    .await?
            }
            "TENANT" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $1::uuid AND job_topic = $2 AND owner_type = 'TENANT' \
                               AND status IN ('PENDING', 'PROCESSING') AND resource_id::uuid = $3 \
                             FOR UPDATE \
                          ), \
                         updated_tenant AS ( \
                             UPDATE storage.tenant_buckets bucket \
                             SET lifecycle_rules = $4::text::jsonb, status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = 'SUCCEEDED', completed_at = NOW(), updated_at = NOW(), \
                             error_code = NULL, error_message = NULL \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM updated_tenant) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&job_uuid, &job_topic, &result_bucket_id, &actual_rules_json],
                    )
                    .await?
            }
            _ => None,
        }
    } else if status == "PROCESSING" {
        if !result_payload.is_empty() || result_payload_schema_version != 0 {
            return Err(
                "PROCESSING storage bucket lifecycle result must not carry a payload".into(),
            );
        }
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        if !result_payload.is_empty() || result_payload_schema_version != 0 {
            return Err("FAILED storage bucket lifecycle result must not carry a payload".into());
        }
        match owner_type.as_str() {
            "PERSONAL" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'PERSONAL' \
                               AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                          ), \
                         restored_personal AS ( \
                             UPDATE storage.personal_buckets bucket \
                             SET status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = $1, completed_at = NOW(), updated_at = NOW(), error_code = $2, error_message = $3 \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM restored_personal) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&status, &error_code, &error_message, &job_uuid, &job_topic],
                    )
                    .await?
            }
            "TENANT" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'TENANT' \
                               AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                          ), \
                         restored_tenant AS ( \
                             UPDATE storage.tenant_buckets bucket \
                             SET status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'UPDATING' \
                             RETURNING id \
                          ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = $1, completed_at = NOW(), updated_at = NOW(), error_code = $2, error_message = $3 \
                         FROM locked_outbox locked \
                         WHERE outbox.event_id = locked.event_id AND EXISTS (SELECT 1 FROM restored_tenant) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&status, &error_code, &error_message, &job_uuid, &job_topic],
                    )
                    .await?
            }
            _ => None,
        }
    };

    Ok(row_opt)
}

/// [COMMENT]: Xử lý xóa Bucket với CTE cách ly hoàn toàn theo Owner Type.
///
/// PERSONAL: Chỉ thực thi CTE `deleted_personal` trên bảng `personal_buckets`.
/// TENANT:   Chỉ thực thi CTE `deleted_tenant` trên bảng `tenant_buckets`.
pub async fn resolve_bucket_deletion(
    pg_client: &tokio_postgres::Client,
    job_uuid: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, tokio_postgres::Error> {
    Logger::sys_info(
        "storage_db.resolve_bucket_deletion",
        &format!(
            "Khép lại vòng đời Outbox cho Bucket Delete Job: {} -> {}",
            job_uuid, status
        ),
    );

    let outbox_meta = pg_client
        .query_opt(
            "SELECT owner_type FROM storage.storage_outbox_records \
             WHERE event_id = $1::uuid AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING')",
            &[&job_uuid, &job_topic],
        )
        .await?;

    let Some(outbox_meta) = outbox_meta else {
        return Ok(None);
    };
    let owner_type: String = outbox_meta.get(0);

    let row_opt = if status == "SUCCEEDED" {
        match owner_type.as_str() {
            "PERSONAL" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT resource_id::uuid AS resource_id, resource_name, zone_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $1::uuid AND job_topic = $2 \
                               AND owner_type = 'PERSONAL' \
                               AND status IN ('PENDING', 'PROCESSING') \
                               AND length(btrim(resource_name)) BETWEEN 1 AND 255 \
                               AND zone_id <> '00000000-0000-0000-0000-000000000000'::uuid \
                             FOR UPDATE \
                         ), \
                         deleted_personal AS ( \
                             DELETE FROM storage.personal_buckets bucket \
                             USING locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'DELETING' \
                             RETURNING bucket.id \
                         ), \
                         deleted_admission AS ( \
                             DELETE FROM storage.resource_admission_projection admission \
                             USING locked_outbox locked \
                             WHERE admission.resource_id = locked.resource_id \
                               AND admission.zone_id = locked.zone_id \
                               AND EXISTS (SELECT 1 FROM deleted_personal) \
                             RETURNING admission.resource_id \
                         ), \
                         settled_outbox AS ( \
                             UPDATE storage.storage_outbox_records outbox \
                             SET status = 'SUCCEEDED', \
                                 completed_at = NOW(), \
                                 updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE outbox.event_id = $1::uuid AND outbox.job_topic = $2 \
                               AND EXISTS (SELECT 1 FROM deleted_personal) \
                             RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, \
                                       outbox.resource_id, outbox.owner_id, outbox.owner_type, outbox.payload, outbox.zone_id \
                         ) \
                         SELECT * FROM settled_outbox",
                        &[&job_uuid, &job_topic],
                    )
                    .await?
            }
            "TENANT" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT resource_id::uuid AS resource_id, resource_name, zone_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $1::uuid AND job_topic = $2 \
                               AND owner_type = 'TENANT' \
                               AND status IN ('PENDING', 'PROCESSING') \
                               AND length(btrim(resource_name)) BETWEEN 1 AND 255 \
                               AND zone_id <> '00000000-0000-0000-0000-000000000000'::uuid \
                             FOR UPDATE \
                         ), \
                         deleted_tenant AS ( \
                             DELETE FROM storage.tenant_buckets bucket \
                             USING locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'DELETING' \
                             RETURNING bucket.id \
                         ), \
                         deleted_admission AS ( \
                             DELETE FROM storage.resource_admission_projection admission \
                             USING locked_outbox locked \
                             WHERE admission.resource_id = locked.resource_id \
                               AND admission.zone_id = locked.zone_id \
                               AND EXISTS (SELECT 1 FROM deleted_tenant) \
                             RETURNING admission.resource_id \
                         ), \
                         settled_outbox AS ( \
                             UPDATE storage.storage_outbox_records outbox \
                             SET status = 'SUCCEEDED', \
                                 completed_at = NOW(), \
                                 updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE outbox.event_id = $1::uuid AND outbox.job_topic = $2 \
                               AND EXISTS (SELECT 1 FROM deleted_tenant) \
                             RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, \
                                       outbox.resource_id, outbox.owner_id, outbox.owner_type, outbox.payload, outbox.zone_id \
                         ) \
                         SELECT * FROM settled_outbox",
                        &[&job_uuid, &job_topic],
                    )
                    .await?
            }
            _ => None,
        }
    } else if status == "PROCESSING" {
        pg_client
            .query_opt(
                "UPDATE storage.storage_outbox_records \
                 SET status = $1, \
                     error_code = NULL, \
                     error_message = NULL \
                 WHERE event_id = $2::uuid AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&status, &job_uuid, &job_topic],
            )
            .await?
    } else {
        match owner_type.as_str() {
            "PERSONAL" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'PERSONAL' \
                               AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                         ), \
                         ready_personal AS ( \
                             UPDATE storage.personal_buckets bucket \
                             SET status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'DELETING' \
                             RETURNING bucket.id \
                         ), ready_personal_credentials AS ( \
                             UPDATE storage.personal_credentials credential \
                             SET state = 'READY', updated_at = NOW() \
                             WHERE credential.bucket_id IN (SELECT id FROM ready_personal) \
                               AND credential.state = 'DELETING' \
                             RETURNING credential.id \
                         ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = $1, completed_at = NOW(), updated_at = NOW(), \
                             error_code = $2, error_message = $3 \
                         FROM locked_outbox locked \
                         CROSS JOIN (SELECT count(*) FROM ready_personal_credentials) restored_credentials \
                         WHERE outbox.event_id = locked.event_id \
                           AND EXISTS (SELECT 1 FROM ready_personal) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&status, &error_code, &error_message, &job_uuid, &job_topic],
                    )
                    .await?
            }
            "TENANT" => {
                pg_client
                    .query_opt(
                        "WITH locked_outbox AS MATERIALIZED ( \
                             SELECT event_id, resource_id::uuid AS resource_id \
                             FROM storage.storage_outbox_records \
                             WHERE event_id = $4::uuid AND job_topic = $5 AND owner_type = 'TENANT' \
                               AND status IN ('PENDING', 'PROCESSING') FOR UPDATE \
                         ), \
                         ready_tenant AS ( \
                             UPDATE storage.tenant_buckets bucket \
                             SET status = 'READY', updated_at = NOW() \
                             FROM locked_outbox locked \
                             WHERE bucket.id = locked.resource_id AND bucket.status = 'DELETING' \
                             RETURNING bucket.id \
                         ), ready_tenant_credentials AS ( \
                             UPDATE storage.tenant_credentials credential \
                             SET state = 'READY', updated_at = NOW() \
                             WHERE credential.bucket_id IN (SELECT id FROM ready_tenant) \
                               AND credential.state = 'DELETING' \
                             RETURNING credential.id \
                         ) \
                         UPDATE storage.storage_outbox_records outbox \
                         SET status = $1, completed_at = NOW(), updated_at = NOW(), \
                             error_code = $2, error_message = $3 \
                         FROM locked_outbox locked \
                         CROSS JOIN (SELECT count(*) FROM ready_tenant_credentials) restored_credentials \
                         WHERE outbox.event_id = locked.event_id \
                           AND EXISTS (SELECT 1 FROM ready_tenant) \
                         RETURNING outbox.actor_user_id::text, outbox.job_topic, outbox.trace_id, outbox.resource_id",
                        &[&status, &error_code, &error_message, &job_uuid, &job_topic],
                    )
                    .await?
            }
            _ => None,
        }
    };

    Ok(row_opt)
}
