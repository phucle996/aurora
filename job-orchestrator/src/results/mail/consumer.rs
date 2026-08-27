use crate::contracts::mail::MailConsumerDrainedV1;
use crate::results::contract::ValidatedResult;
use prost::Message;
use tokio_postgres::{Client, Row};
use uuid::Uuid;

/// [COMMENT]: Upsert result dùng payload đã khóa trong PostgreSQL, không tin resource identity
/// từ Dataplane result. Create FAILED giữ record; update dùng candidate COW rồi promote sau ACK.
pub async fn apply_upsert_result(
    pg_client: &mut tokio_postgres::Client,
    event_id: Uuid,
    status: &str,
    attempt: u32,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error>> {
    let transaction = pg_client.transaction().await?;
    let locked = transaction
        .query_opt(
            "SELECT status,result_attempt,resource_id,zone_id \
             FROM mail.mail_outbox_records \
             WHERE event_id=$1 AND job_topic='mail.consumer.upsert' \
             FOR UPDATE",
            &[&event_id],
        )
        .await?;
    let Some(locked) = locked else {
        transaction.commit().await?;
        return Ok(None);
    };
    let current_status: String = locked.get(0);
    let current_attempt: i32 = locked.get(1);
    let attempt = attempt as i32;
    // [COMMENT]: Upsert FAILED đã cleanup candidate nên terminal; PROCESSING/FAILED vẫn dùng attempt fence.
    let transition_allowed = current_status != "SUCCEEDED"
        && current_status != "FAILED"
        && match status {
            // [COMMENT]: FAILED đã cleanup create/update candidate nên là terminal cho upsert event này.
            "SUCCEEDED" => true,
            "PROCESSING" => current_status == "PENDING" || attempt > current_attempt,
            "FAILED" => {
                attempt > current_attempt
                    || (attempt == current_attempt && current_status != "FAILED")
            }
            _ => false,
        };
    if !transition_allowed {
        // [COMMENT]: DB terminal có thể đã commit nhưng NATS notify lỗi. Redis result redelivery
        // được phép phát lại cùng notification ID mà không chạy lại cleanup/promote.
        let notification = if current_status == status && matches!(status, "SUCCEEDED" | "FAILED") {
            transaction
                .query_opt(
                    "SELECT actor_user_id::text,job_topic,trace_id,resource_id \
                     FROM mail.mail_outbox_records WHERE event_id=$1 AND job_topic='mail.consumer.upsert'",
                    &[&event_id],
                )
                .await?
        } else {
            None
        };
        transaction.commit().await?;
        return Ok(notification);
    }
    let resource_id = Uuid::parse_str(locked.get::<_, Option<String>>(2).as_deref().unwrap_or(""))?;
    let _zone_id: Uuid = locked.get(3);
    // [COMMENT]: Consumer UUID không mang scope trên wire. JO khóa và xác định đúng một namespace;
    // zero hoặc hai match đều fail-close thay vì ghi nhầm Personal/Tenant.
    let scope = transaction
        .query_one(
            "SELECT \
                 EXISTS(SELECT 1 FROM mail.personal_mail_consumers WHERE id=$1 FOR UPDATE), \
                 EXISTS(SELECT 1 FROM mail.tenant_mail_consumers WHERE id=$1 FOR UPDATE)",
            &[&resource_id],
        )
        .await?;
    let personal_exists: bool = scope.get(0);
    let tenant_exists: bool = scope.get(1);
    if personal_exists == tenant_exists {
        return Err("Mail consumer upsert result cannot resolve exactly one scope".into());
    }

    // The outbox payload is ciphertext and JO deliberately has no private key.
    // Resolve the immutable candidate fence from CP business tables instead.
    let version_row = if personal_exists {
        transaction
            .query_opt(
                "SELECT config_version FROM mail.personal_mail_consumer_update_versions \
                 WHERE consumer_id=$1 AND event_id=$2 \
                 UNION ALL \
                 SELECT config_version FROM mail.personal_mail_consumers \
                 WHERE id=$1 AND config_version=1 \
                   AND NOT EXISTS (SELECT 1 FROM mail.personal_mail_consumer_update_versions \
                                   WHERE consumer_id=$1 AND event_id=$2) \
                 LIMIT 1",
                &[&resource_id, &event_id],
            )
            .await?
    } else {
        transaction
            .query_opt(
                "SELECT config_version FROM mail.tenant_mail_consumer_update_versions \
                 WHERE consumer_id=$1 AND event_id=$2 \
                 UNION ALL \
                 SELECT config_version FROM mail.tenant_mail_consumers \
                 WHERE id=$1 AND config_version=1 \
                   AND NOT EXISTS (SELECT 1 FROM mail.tenant_mail_consumer_update_versions \
                                   WHERE consumer_id=$1 AND event_id=$2) \
                 LIMIT 1",
                &[&resource_id, &event_id],
            )
            .await?
    }
    .ok_or("Mail consumer upsert result has no immutable candidate fence")?;
    let config_version: i64 = version_row.get(0);

    if status == "FAILED" {
        let cleaned = if config_version == 1 {
            // Failed provisioning is not deletion evidence. Keep the V1 record
            // and last configuration; only the delete workflow may remove it.
            if personal_exists {
                transaction
                    .execute(
                        "UPDATE mail.personal_mail_consumers SET updated_at=NOW() WHERE id=$1 AND config_version=1",
                        &[&resource_id],
                    )
                    .await?
            } else {
                transaction
                    .execute(
                        "UPDATE mail.tenant_mail_consumers SET updated_at=NOW() WHERE id=$1 AND config_version=1",
                        &[&resource_id],
                    )
                    .await?
            }
        } else {
            // [COMMENT]: Candidate FAILED bị xóa chính xác theo event/version; sequence trên head không lùi.
            if personal_exists {
                transaction
                    .execute(
                        "DELETE FROM mail.personal_mail_consumer_update_versions \
                         WHERE consumer_id=$1 AND config_version=$2 AND event_id=$3",
                        &[&resource_id, &config_version, &event_id],
                    )
                    .await?
            } else {
                transaction
                    .execute(
                        "DELETE FROM mail.tenant_mail_consumer_update_versions \
                         WHERE consumer_id=$1 AND config_version=$2 AND event_id=$3",
                        &[&resource_id, &config_version, &event_id],
                    )
                    .await?
            }
        };
        if cleaned != 1 {
            return Err("Mail consumer FAILED result has no exact generation to clean".into());
        }
    } else if status == "SUCCEEDED" && config_version > 1 {
        // [COMMENT]: Zone đã apply thành công mới được promote candidate thành active business row.
        let promoted = if personal_exists {
            // [COMMENT]: Personal promotion không có actor audit column.
            transaction
                .execute(
                    "UPDATE mail.personal_mail_consumers AS active SET \
                         name=candidate.name,source_type=candidate.source_type, \
                         broker_resource_id=candidate.broker_resource_id, \
                         source_config_envelope=candidate.source_config_envelope,topic=candidate.topic, \
                         consumer_group=candidate.consumer_group,template_id=candidate.template_id, \
                         template_version=candidate.template_version,sender_profile_id=candidate.sender_profile_id, \
                         sender_version=candidate.sender_version,desired_state=candidate.desired_state, \
                         parallelism=candidate.parallelism,config_version=candidate.config_version, \
                         config_sha256=candidate.config_sha256,updated_at=candidate.created_at \
                     FROM mail.personal_mail_consumer_update_versions AS candidate \
                     WHERE active.id=$1 AND candidate.consumer_id=active.id \
                       AND candidate.config_version=$2 AND candidate.event_id=$3 \
                       AND active.config_version < candidate.config_version",
                    &[&resource_id, &config_version, &event_id],
                )
                .await?
        } else {
            transaction
                .execute(
                    "UPDATE mail.tenant_mail_consumers AS active SET \
                         name=candidate.name,source_type=candidate.source_type, \
                         broker_resource_id=candidate.broker_resource_id, \
                         source_config_envelope=candidate.source_config_envelope,topic=candidate.topic, \
                         consumer_group=candidate.consumer_group,template_id=candidate.template_id, \
                         template_version=candidate.template_version,sender_profile_id=candidate.sender_profile_id, \
                         sender_version=candidate.sender_version,desired_state=candidate.desired_state, \
                         parallelism=candidate.parallelism,config_version=candidate.config_version, \
                         config_sha256=candidate.config_sha256,updated_by=candidate.updated_by, \
                         updated_at=candidate.created_at \
                     FROM mail.tenant_mail_consumer_update_versions AS candidate \
                     WHERE active.id=$1 AND candidate.consumer_id=active.id \
                       AND candidate.config_version=$2 AND candidate.event_id=$3 \
                       AND active.config_version < candidate.config_version",
                    &[&resource_id, &config_version, &event_id],
                )
                .await?
        };
        if promoted != 1 {
            return Err("Mail consumer update ACK has no exact candidate to promote".into());
        }
    } else if status == "SUCCEEDED" {
        let exists = if personal_exists {
            transaction
                .query_opt(
                    "SELECT 1 FROM mail.personal_mail_consumers WHERE id=$1 AND config_version=1 FOR UPDATE",
                    &[&resource_id],
                )
                .await?
        } else {
            transaction
                .query_opt(
                    "SELECT 1 FROM mail.tenant_mail_consumers WHERE id=$1 AND config_version=1 FOR UPDATE",
                    &[&resource_id],
                )
                .await?
        };
        if exists.is_none() {
            return Err("Mail consumer create ACK has no V1 aggregate".into());
        }
    }
    // The first successful apply can follow a failed V1 create. Stage ownership
    // once per resource, including when a later COW revision is the first success.
    if status == "SUCCEEDED" {
        let ownership_staged = if personal_exists {
            transaction
                .execute(
                    "INSERT INTO mail.mail_consumer_billing_outbox
                     (source_event_id,event_type,resource_id,resource_name,owner_id,owner_type,
                      zone_id,source_version,effective_at)
                     SELECT $1,'RESOURCE_CREATED',c.id,c.code,w.owner_id,'PERSONAL',w.zone_id,1,NOW()
                     FROM mail.personal_mail_consumers c
                     JOIN hierarchy.personal_workspaces w ON w.id=c.workspace_id
                     WHERE c.id=$2 AND w.zone_id=$3
                     ON CONFLICT (resource_id,source_version) DO NOTHING",
                    &[&event_id, &resource_id, &_zone_id],
                )
                .await?
        } else {
            transaction
                .execute(
                    "INSERT INTO mail.mail_consumer_billing_outbox
                     (source_event_id,event_type,resource_id,resource_name,owner_id,owner_type,
                      zone_id,source_version,effective_at)
                     SELECT $1,'RESOURCE_CREATED',c.id,c.code,w.tenant_id,'TENANT',w.zone_id,1,NOW()
                     FROM mail.tenant_mail_consumers c
                     JOIN hierarchy.tenant_workspaces w ON w.id=c.workspace_id
                     WHERE c.id=$2 AND w.zone_id=$3
                     ON CONFLICT (resource_id,source_version) DO NOTHING",
                    &[&event_id, &resource_id, &_zone_id],
                )
                .await?
        };
        if ownership_staged != 1 {
            let exists: bool = transaction
                .query_one(
                    "SELECT EXISTS(SELECT 1 FROM mail.mail_consumer_billing_outbox
                     WHERE resource_id=$1 AND source_version=1)",
                    &[&resource_id],
                )
                .await?
                .get(0);
            if !exists {
                return Err("Mail consumer create ACK did not stage billing ownership".into());
            }
        }
    }

    let row = transaction
        .query_opt(
            "UPDATE mail.mail_outbox_records \
             SET status=$1, completed_at=CASE WHEN $1='PROCESSING' THEN NULL ELSE NOW() END, \
                 updated_at=NOW(), \
                 error_code=CASE WHEN $1='FAILED' THEN $2 ELSE NULL END, \
                 error_message=CASE WHEN $1='FAILED' THEN $3 ELSE NULL END, \
                 result_attempt=GREATEST(result_attempt,$4) \
             WHERE event_id=$5 AND job_topic='mail.consumer.upsert' AND status<>'SUCCEEDED' \
             RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
            &[&status, &error_code, &error_message, &attempt, &event_id],
        )
        .await?;
    transaction.commit().await?;
    Ok(row)
}

/// [COMMENT]: Delete success hard-delete connection row và persist tombstone trong cùng transaction.
/// FAILED chỉ đóng outbox và giữ nguyên business row để người dùng có thể retry.
pub async fn apply_delete_result(
    pg_client: &mut tokio_postgres::Client,
    event_id: Uuid,
    status: &str,
    attempt: u32,
    error_code: Option<&str>,
    error_message: Option<&str>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error>> {
    let transaction = pg_client.transaction().await?;
    let locked = transaction
        .query_opt(
            "SELECT status,result_attempt,resource_id,zone_id \
             FROM mail.mail_outbox_records \
             WHERE event_id=$1 AND job_topic='mail.consumer.delete' \
             FOR UPDATE",
            &[&event_id],
        )
        .await?;
    let Some(locked) = locked else {
        transaction.commit().await?;
        return Ok(None);
    };
    let current_status: String = locked.get(0);
    let current_attempt: i32 = locked.get(1);
    let attempt = attempt as i32;
    // [COMMENT]: FAILED là terminal cho operation ID; delete retry tạo event mới với cùng version fence.
    let transition_allowed = current_status != "SUCCEEDED"
        && current_status != "FAILED"
        && match status {
            "SUCCEEDED" => true,
            "PROCESSING" => current_status == "PENDING" || attempt > current_attempt,
            "FAILED" => {
                attempt > current_attempt
                    || (attempt == current_attempt && current_status != "FAILED")
            }
            _ => false,
        };
    if !transition_allowed {
        // [COMMENT]: Cùng terminal result được redeliver chỉ để retry NATS notification;
        // resource mutation vẫn tuyệt đối không chạy lần hai.
        let notification = if current_status == status && matches!(status, "SUCCEEDED" | "FAILED") {
            transaction
                .query_opt(
                    "SELECT actor_user_id::text,job_topic,trace_id,resource_id \
                     FROM mail.mail_outbox_records WHERE event_id=$1 AND job_topic='mail.consumer.delete'",
                    &[&event_id],
                )
                .await?
        } else {
            None
        };
        transaction.commit().await?;
        return Ok(notification);
    }
    let resource_id = Uuid::parse_str(locked.get::<_, Option<String>>(2).as_deref().unwrap_or(""))?;
    let zone_id: Uuid = locked.get(3);
    if status == "SUCCEEDED" {
        // [COMMENT]: Delete ACK cũng phải resolve đúng một physical namespace trước khi tạo tombstone.
        let scope = transaction
            .query_one(
                "SELECT \
                     EXISTS(SELECT 1 FROM mail.personal_mail_consumers WHERE id=$1 FOR UPDATE), \
                     EXISTS(SELECT 1 FROM mail.tenant_mail_consumers WHERE id=$1 FOR UPDATE)",
                &[&resource_id],
            )
            .await?;
        let personal_exists: bool = scope.get(0);
        let tenant_exists: bool = scope.get(1);
        if personal_exists == tenant_exists {
            return Err("Mail consumer delete result cannot resolve exactly one scope".into());
        }
        let target = if personal_exists {
            transaction
                .query_opt(
                    "SELECT config_version,next_config_version FROM mail.personal_mail_consumers WHERE id=$1 FOR UPDATE",
                    &[&resource_id],
                )
                .await?
        } else {
            transaction
                .query_opt(
                    "SELECT config_version,next_config_version FROM mail.tenant_mail_consumers WHERE id=$1 FOR UPDATE",
                    &[&resource_id],
                )
                .await?
        }
        .ok_or("Mail consumer delete ACK has no aggregate")?;
        let active_version: i64 = target.get(0);
        let config_version: i64 = target.get(1);
        if active_version >= config_version {
            return Err("Mail consumer delete fence is not newer than active version".into());
        }
        let ownership_staged = if personal_exists {
            transaction
                .execute(
                    "INSERT INTO mail.mail_consumer_billing_outbox
                     (source_event_id,event_type,resource_id,resource_name,owner_id,owner_type,
                      zone_id,source_version,effective_at)
                     SELECT $1,'RESOURCE_DELETED',c.id,c.code,w.owner_id,'PERSONAL',w.zone_id,2,NOW()
                     FROM mail.personal_mail_consumers c
                     JOIN hierarchy.personal_workspaces w ON w.id=c.workspace_id
                     WHERE c.id=$2 AND w.zone_id=$3
                     ON CONFLICT (source_event_id) DO NOTHING",
                    &[&event_id, &resource_id, &zone_id],
                )
                .await?
        } else {
            transaction
                .execute(
                    "INSERT INTO mail.mail_consumer_billing_outbox
                     (source_event_id,event_type,resource_id,resource_name,owner_id,owner_type,
                      zone_id,source_version,effective_at)
                     SELECT $1,'RESOURCE_DELETED',c.id,c.code,w.tenant_id,'TENANT',w.zone_id,2,NOW()
                     FROM mail.tenant_mail_consumers c
                     JOIN hierarchy.tenant_workspaces w ON w.id=c.workspace_id
                     WHERE c.id=$2 AND w.zone_id=$3
                     ON CONFLICT (source_event_id) DO NOTHING",
                    &[&event_id, &resource_id, &zone_id],
                )
                .await?
        };
        if ownership_staged != 1 {
            let exists: bool = transaction
                .query_one(
                    "SELECT EXISTS(SELECT 1 FROM mail.mail_consumer_billing_outbox
                     WHERE source_event_id=$1 AND resource_id=$2 AND source_version=2)",
                    &[&event_id, &resource_id],
                )
                .await?
                .get(0);
            if !exists {
                return Err("Mail consumer delete ACK did not stage billing ownership".into());
            }
        }
        // [COMMENT]: Tombstone là rebuild authority; command fence phải lớn hơn active version.
        let tombstoned = if personal_exists {
            transaction
                .execute(
                    "INSERT INTO mail.personal_mail_consumer_projection_tombstones( \
                         consumer_id,zone_id,config_version,delete_event_id,tombstoned_at \
                     ) VALUES ($1,$2,$3,$4,NOW()) \
                     ON CONFLICT (consumer_id) DO UPDATE SET \
                         zone_id=EXCLUDED.zone_id,config_version=EXCLUDED.config_version, \
                         delete_event_id=EXCLUDED.delete_event_id,tombstoned_at=EXCLUDED.tombstoned_at \
                     WHERE EXCLUDED.config_version > personal_mail_consumer_projection_tombstones.config_version",
                    &[&resource_id, &zone_id, &config_version, &event_id],
                )
                .await?
        } else {
            transaction
                .execute(
                    "INSERT INTO mail.tenant_mail_consumer_projection_tombstones( \
                         consumer_id,zone_id,config_version,delete_event_id,tombstoned_at \
                     ) VALUES ($1,$2,$3,$4,NOW()) \
                     ON CONFLICT (consumer_id) DO UPDATE SET \
                         zone_id=EXCLUDED.zone_id,config_version=EXCLUDED.config_version, \
                         delete_event_id=EXCLUDED.delete_event_id,tombstoned_at=EXCLUDED.tombstoned_at \
                     WHERE EXCLUDED.config_version > tenant_mail_consumer_projection_tombstones.config_version",
                    &[&resource_id, &zone_id, &config_version, &event_id],
                )
                .await?
        };
        if tombstoned != 1 {
            return Err("Mail consumer delete ACK did not advance projection tombstone".into());
        }
        let deleted = if personal_exists {
            transaction
                .execute(
                    "DELETE FROM mail.personal_mail_consumers WHERE id=$1 AND config_version < $2 AND desired_state='deleting'",
                    &[&resource_id, &config_version],
                )
                .await?
        } else {
            transaction
                .execute(
                    "DELETE FROM mail.tenant_mail_consumers WHERE id=$1 AND config_version < $2 AND desired_state='deleting'",
                    &[&resource_id, &config_version],
                )
                .await?
        };
        if deleted != 1 {
            return Err("Mail consumer delete ACK did not remove aggregate".into());
        }
    }

    let row = transaction
        .query_opt(
            "UPDATE mail.mail_outbox_records \
             SET status=$1, completed_at=CASE WHEN $1='PROCESSING' THEN NULL ELSE NOW() END, \
                 updated_at=NOW(), \
                 error_code=CASE WHEN $1='FAILED' THEN $2 ELSE NULL END, \
                 error_message=CASE WHEN $1='FAILED' THEN $3 ELSE NULL END, \
                 result_attempt=GREATEST(result_attempt,$4) \
             WHERE event_id=$5 AND job_topic='mail.consumer.delete' AND status<>'SUCCEEDED' \
             RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
            &[&status, &error_code, &error_message, &attempt, &event_id],
        )
        .await?;
    transaction.commit().await?;
    Ok(row)
}

pub async fn apply_drain_result(
    client: &mut Client,
    result: &ValidatedResult,
) -> Result<Option<Row>, Box<dyn std::error::Error>> {
    let wire = &result.wire;
    let tx = client.transaction().await?;
    let authority = tx
        .query_opt(
            "SELECT resource_id,zone_id,status,result_attempt FROM mail.mail_outbox_records
         WHERE event_id=$1 AND job_topic='mail.consumer.drain' FOR UPDATE",
            &[&result.job_id],
        )
        .await?;
    let Some(authority) = authority else {
        return Ok(None);
    };
    let state: String = authority.get(2);
    if !matches!(state.as_str(), "PENDING" | "PROCESSING") {
        // Business mutation is terminal; replay still retries the durable notification.
        let notification = if state == wire.result_status
            && matches!(state.as_str(), "SUCCEEDED" | "FAILED")
        {
            tx.query_opt("SELECT actor_user_id::text,job_topic,trace_id,resource_id FROM mail.mail_outbox_records WHERE event_id=$1", &[&result.job_id]).await?
        } else {
            None
        };
        tx.commit().await?;
        return Ok(notification);
    }
    let current_attempt: i32 = authority.get(3);
    let attempt = i32::try_from(wire.attempt)?;
    if wire.result_status != "SUCCEEDED" && attempt < current_attempt {
        return Ok(None);
    }
    let resource_id = uuid::Uuid::parse_str(authority.get::<_, &str>(0))?;
    let zone_id: uuid::Uuid = authority.get(1);
    match wire.result_status.as_str() {
        "SUCCEEDED" => {
            if wire.result_payload_schema_version != 1 {
                return Err("MAIL_DRAIN_RESULT_SCHEMA_INVALID".into());
            }
            let drained = MailConsumerDrainedV1::decode(wire.result_payload.as_slice())?;
            if drained.schema_version != 1
                || drained.consumer_id.as_slice() != resource_id.as_bytes()
                || drained.config_version == 0
                || drained.config_version > i64::MAX as u64
                || drained.settled_slots == 0
                || drained.settled_slots > 1024
            {
                return Err("MAIL_DRAIN_RESULT_INVALID".into());
            }
            let version = drained.config_version as i64;
            let slots = drained.settled_slots as i32;
            let personal = tx.execute(
                "UPDATE mail.personal_mail_consumers c SET desired_state='drained',updated_at=NOW()
                 FROM hierarchy.personal_workspaces w WHERE c.id=$1 AND c.workspace_id=w.id AND w.zone_id=$2
                 AND c.config_version=$3 AND c.parallelism=$4 AND c.desired_state='draining'",
                &[&resource_id,&zone_id,&version,&slots],
            ).await?;
            let tenant = tx.execute(
                "UPDATE mail.tenant_mail_consumers c SET desired_state='drained',updated_at=NOW()
                 FROM hierarchy.tenant_workspaces w WHERE c.id=$1 AND c.workspace_id=w.id AND w.zone_id=$2
                 AND c.config_version=$3 AND c.parallelism=$4 AND c.desired_state='draining'",
                &[&resource_id,&zone_id,&version,&slots],
            ).await?;
            if personal + tenant != 1 {
                return Err("MAIL_DRAIN_RESULT_FENCE_MISMATCH".into());
            }
        }
        "PROCESSING" | "FAILED" => {
            if !wire.result_payload.is_empty() || wire.result_payload_schema_version != 0 {
                return Err("MAIL_DRAIN_NON_SUCCESS_PAYLOAD".into());
            }
        }
        _ => return Err("MAIL_DRAIN_STATUS_INVALID".into()),
    }
    let row = tx
        .query_opt(
            "UPDATE mail.mail_outbox_records
         SET status=$2,completed_at=CASE WHEN $2='PROCESSING' THEN NULL ELSE NOW() END,
             updated_at=NOW(),result_attempt=GREATEST(result_attempt,$3),error_code=$4,
             error_message=CASE WHEN $2='FAILED' THEN $5 ELSE NULL END
         WHERE event_id=$1 AND status IN ('PENDING','PROCESSING')
         RETURNING actor_user_id::text,job_topic,trace_id,resource_id",
            &[
                &result.job_id,
                &wire.result_status,
                &attempt,
                &wire.error_code,
                &wire.message,
            ],
        )
        .await?;
    tx.commit().await?;
    Ok(row)
}
