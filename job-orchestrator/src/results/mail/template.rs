use crate::contracts::mail::{MailTemplateDeletedV1, MailTemplateVersionPublishedV1};
use prost::Message;
use uuid::Uuid;

/// [COMMENT]: Template result là ranh giới infrastructure-first: publish promote candidate sau
/// Zone ACK; delete chỉ hard-delete CP aggregate sau khi Zone đã xóa projection.
pub async fn apply_result(
    pg_client: &mut tokio_postgres::Client,
    event_id: Uuid,
    job_topic: &str,
    status: &str,
    attempt: u32,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error>> {
    let transaction = pg_client.transaction().await?;
    let locked = transaction
        .query_opt(
            "SELECT status,result_attempt,resource_id,payload \
             FROM mail.mail_outbox_records WHERE event_id=$1 AND job_topic=$2 FOR UPDATE",
            &[&event_id, &job_topic],
        )
        .await?;
    let Some(locked) = locked else {
        transaction.commit().await?;
        return Ok(None);
    };
    let current_status: String = locked.get(0);
    let current_attempt: i32 = locked.get(1);
    let attempt = attempt as i32;
    // [COMMENT]: FAILED là terminal cho operation ID; delete retry tạo event mới nhưng giữ cùng monotonic fence.
    let transition_allowed = current_status != "SUCCEEDED"
        && current_status != "FAILED"
        && match status {
            // [COMMENT]: FAILED đã cleanup create/publish candidate nên là terminal cho publish event.
            "SUCCEEDED" => true,
            "PROCESSING" => current_status == "PENDING" || attempt > current_attempt,
            "FAILED" => {
                attempt > current_attempt
                    || (attempt == current_attempt && current_status != "FAILED")
            }
            _ => false,
        };
    if !transition_allowed {
        // [COMMENT]: Nếu terminal DB commit nhưng NATS notify lỗi, Redis redelivery chỉ retry
        // notification cùng transaction ID; publish/delete mutation không chạy lại.
        let notification = if current_status == status && matches!(status, "SUCCEEDED" | "FAILED") {
            transaction
                .query_opt(
                    "SELECT actor_user_id::text,job_topic,trace_id,resource_id \
                     FROM mail.mail_outbox_records WHERE event_id=$1 AND job_topic=$2",
                    &[&event_id, &job_topic],
                )
                .await?
        } else {
            None
        };
        transaction.commit().await?;
        return Ok(notification);
    }

    let resource_id = locked.get::<_, Option<String>>(2).unwrap_or_default();
    let payload: Vec<u8> = locked.get(3);

    if job_topic == "mail.template.version_published" && status != "PROCESSING" {
        let command = MailTemplateVersionPublishedV1::decode(payload.as_slice())?;
        if command.template_id != resource_id
            || command.template_version == 0
            || command.template_revision == 0
        {
            return Err("Mail template publish outbox identity/version mismatch".into());
        }
        let version = i64::try_from(command.template_version)
            .map_err(|_| "Mail template version exceeds BIGINT")?;
        let revision = i64::try_from(command.template_revision)
            .map_err(|_| "Mail template revision exceeds BIGINT")?;

        // [COMMENT]: UUID identity là globally unique; khóa đúng aggregate trước khi chọn scope.
        let personal = transaction
            .query_opt(
                "SELECT current_version,template_revision FROM mail.personal_mail_templates \
                 WHERE id=$1 FOR UPDATE",
                &[&resource_id],
            )
            .await?;
        let tenant = if personal.is_none() {
            transaction
                .query_opt(
                    "SELECT current_version,template_revision FROM mail.tenant_mail_templates \
                     WHERE id=$1 FOR UPDATE",
                    &[&resource_id],
                )
                .await?
        } else {
            None
        };

        if status == "FAILED" {
            // [COMMENT]: Create V1 thất bại xóa toàn aggregate; publish thất bại chỉ xóa exact candidate.
            transaction
                .query_one(
                    "SELECT set_config('mail.allow_template_version_mutation','on',true)",
                    &[],
                )
                .await?;
            let cleaned = if command.template_version == 1 {
                if personal.is_some() {
                    transaction
                        .execute(
                            "DELETE FROM mail.personal_mail_templates \
                             WHERE id=$1 AND current_version=1 AND template_revision=1",
                            &[&resource_id],
                        )
                        .await?
                } else if tenant.is_some() {
                    transaction
                        .execute(
                            "DELETE FROM mail.tenant_mail_templates \
                             WHERE id=$1 AND current_version=1 AND template_revision=1",
                            &[&resource_id],
                        )
                        .await?
                } else {
                    0
                }
            } else if personal.is_some() {
                transaction
                    .execute(
                        "DELETE FROM mail.personal_mail_template_versions \
                         WHERE template_id=$1 AND version=$2 AND template_revision=$3 AND event_id=$4 \
                           AND version > (SELECT current_version FROM mail.personal_mail_templates WHERE id=$1)",
                        &[&resource_id, &version, &revision, &event_id],
                    )
                    .await?
            } else if tenant.is_some() {
                transaction
                    .execute(
                        "DELETE FROM mail.tenant_mail_template_versions \
                         WHERE template_id=$1 AND version=$2 AND template_revision=$3 AND event_id=$4 \
                           AND version > (SELECT current_version FROM mail.tenant_mail_templates WHERE id=$1)",
                        &[&resource_id, &version, &revision, &event_id],
                    )
                    .await?
            } else {
                0
            };
            if cleaned != 1 {
                return Err("Mail template FAILED result has no exact generation to clean".into());
            }
        } else if command.template_version > 1 {
            // [COMMENT]: Chỉ candidate đã ACK mới trở thành current head; immutable version row được giữ làm history.
            let promoted = if personal.is_some() {
                transaction
					.execute(
                        "UPDATE mail.personal_mail_templates AS head SET \
                             current_version=candidate.version,template_revision=candidate.template_revision, \
                             updated_at=candidate.created_at \
                         FROM mail.personal_mail_template_versions AS candidate \
                         WHERE head.id=$1 AND candidate.template_id=head.id AND candidate.version=$2 \
                           AND candidate.template_revision=$3 AND candidate.event_id=$4 \
                           AND head.template_revision < candidate.template_revision",
                        &[&resource_id, &version, &revision, &event_id],
                    )
					.await?
            } else if tenant.is_some() {
                transaction
                    .execute(
                        "UPDATE mail.tenant_mail_templates AS head SET \
                             current_version=candidate.version,template_revision=candidate.template_revision, \
                             updated_by=candidate.created_by,updated_at=candidate.created_at \
                         FROM mail.tenant_mail_template_versions AS candidate \
                         WHERE head.id=$1 AND candidate.template_id=head.id AND candidate.version=$2 \
                           AND candidate.template_revision=$3 AND candidate.event_id=$4 \
                           AND head.template_revision < candidate.template_revision",
                        &[&resource_id, &version, &revision, &event_id],
                    )
					.await?
            } else {
                0
            };
            if promoted != 1 {
                return Err("Mail template publish ACK has no exact candidate to promote".into());
            }
        } else if personal.is_none() && tenant.is_none() {
            return Err("Mail template create ACK has no V1 aggregate".into());
        }
    } else if job_topic == "mail.template.deleted" && status == "SUCCEEDED" {
        let command = MailTemplateDeletedV1::decode(payload.as_slice())?;
        if command.template_id != resource_id
            || command.template_revision == 0
            || command.last_published_version == 0
        {
            return Err("Mail template delete outbox identity/version mismatch".into());
        }
        let revision = i64::try_from(command.template_revision)
            .map_err(|_| "Mail template delete revision exceeds BIGINT")?;
        let last_version = i64::try_from(command.last_published_version)
            .map_err(|_| "Mail template delete version exceeds BIGINT")?;

        let personal = transaction
            .query_opt(
                "SELECT workspace_id,current_version,template_revision \
                 FROM mail.personal_mail_templates WHERE id=$1 FOR UPDATE",
                &[&resource_id],
            )
            .await?;
        let tenant = if personal.is_none() {
            transaction
                .query_opt(
                    "SELECT workspace_id,current_version,template_revision \
                     FROM mail.tenant_mail_templates WHERE id=$1 FOR UPDATE",
                    &[&resource_id],
                )
                .await?
        } else {
            None
        };
        if personal.is_none() && tenant.is_none() {
            return Err("Mail template delete ACK has no aggregate".into());
        }

        transaction
            .query_one(
                "SELECT set_config('mail.allow_template_version_mutation','on',true)",
                &[],
            )
            .await?;
        if let Some(target) = personal {
            let workspace_id: Uuid = target.get(0);
            let current_version: i64 = target.get(1);
            let current_revision: i64 = target.get(2);
            if current_version != last_version || revision <= current_revision {
                return Err("Mail personal template delete fence/head mismatch".into());
            }
            {
                let tombstoned = transaction.execute(
                    "INSERT INTO mail.personal_mail_template_projection_tombstones( \
                         template_id,workspace_id,template_revision,last_published_version,event_id,deleted_at \
                     ) VALUES ($1,$2,$3,$4,$5,NOW()) ON CONFLICT (template_id) DO UPDATE SET \
                         workspace_id=EXCLUDED.workspace_id,template_revision=EXCLUDED.template_revision, \
                         last_published_version=EXCLUDED.last_published_version,event_id=EXCLUDED.event_id, \
                         deleted_at=EXCLUDED.deleted_at \
                     WHERE EXCLUDED.template_revision > personal_mail_template_projection_tombstones.template_revision",
                    &[&resource_id,&workspace_id,&revision,&last_version,&event_id],
                ).await?;
                if tombstoned != 1 {
                    return Err(
                        "Mail personal template delete ACK did not advance tombstone".into(),
                    );
                }
                let deleted = transaction
                    .execute(
                        "DELETE FROM mail.personal_mail_templates WHERE id=$1",
                        &[&resource_id],
                    )
                    .await?;
                if deleted != 1 {
                    return Err("Mail personal template delete ACK did not remove aggregate".into());
                }
            }
        } else if let Some(target) = tenant {
            let workspace_id: Uuid = target.get(0);
            let current_version: i64 = target.get(1);
            let current_revision: i64 = target.get(2);
            if current_version != last_version || revision <= current_revision {
                return Err("Mail tenant template delete fence/head mismatch".into());
            }
            {
                let tombstoned = transaction.execute(
                    "INSERT INTO mail.tenant_mail_template_projection_tombstones( \
                         template_id,workspace_id,template_revision,last_published_version,event_id,deleted_at \
                     ) VALUES ($1,$2,$3,$4,$5,NOW()) ON CONFLICT (template_id) DO UPDATE SET \
                         workspace_id=EXCLUDED.workspace_id,template_revision=EXCLUDED.template_revision, \
                         last_published_version=EXCLUDED.last_published_version,event_id=EXCLUDED.event_id, \
                         deleted_at=EXCLUDED.deleted_at \
                     WHERE EXCLUDED.template_revision > tenant_mail_template_projection_tombstones.template_revision",
                    &[&resource_id,&workspace_id,&revision,&last_version,&event_id],
                ).await?;
                if tombstoned != 1 {
                    return Err("Mail tenant template delete ACK did not advance tombstone".into());
                }
                let deleted = transaction
                    .execute(
                        "DELETE FROM mail.tenant_mail_templates WHERE id=$1",
                        &[&resource_id],
                    )
                    .await?;
                if deleted != 1 {
                    return Err("Mail tenant template delete ACK did not remove aggregate".into());
                }
            }
        }
    }

    let row = transaction
        .query_opt(
            "UPDATE mail.mail_outbox_records SET status=$1, \
             completed_at=CASE WHEN $1='PROCESSING' THEN NULL ELSE NOW() END,updated_at=NOW(), \
             error_code=CASE WHEN $1='FAILED' THEN $2 ELSE NULL END, \
             error_message=CASE WHEN $1='FAILED' THEN $3 ELSE NULL END, \
             result_attempt=GREATEST(result_attempt,$4) \
         WHERE event_id=$5 AND job_topic=$6 AND status<>'SUCCEEDED' \
         RETURNING actor_user_id::text,job_topic,trace_id,resource_id",
            &[
                &status,
                &error_code,
                &error_message,
                &attempt,
                &event_id,
                &job_topic,
            ],
        )
        .await?;
    transaction.commit().await?;
    Ok(row)
}
