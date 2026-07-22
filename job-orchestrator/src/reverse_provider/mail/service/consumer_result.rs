use super::super::runtime_proto::{MailConsumerDeleteV1, MailConsumerUpsertV1};
use prost::Message;
use uuid::Uuid;

const CONSUMER_EVENT_NAMESPACE: &str = "43de31a4-0c86-54e9-8384-47b33f541c28";

/// [COMMENT]: Upsert result dùng payload đã khóa trong PostgreSQL, không tin resource identity
/// từ Dataplane result. FAILED của create V1 hard-delete đúng V1 và để lại projection tombstone.
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
            "SELECT status,result_attempt,resource_id,zone_id,payload \
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
    // [COMMENT]: SUCCEEDED là bằng chứng projection đã áp dụng nên luôn heal FAILED cũ.
    // PROCESSING/FAILED phải tôn trọng attempt fence để result đến trễ không rollback retry mới.
    let transition_allowed = current_status != "SUCCEEDED"
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
        transaction.commit().await?;
        return Ok(None);
    }
    let resource_id = Uuid::parse_str(locked.get::<_, Option<String>>(2).as_deref().unwrap_or(""))?;
    let zone_id: Uuid = locked.get(3);
    let payload: Vec<u8> = locked.get(4);
    let command = MailConsumerUpsertV1::decode(payload.as_slice())?;
    let command_consumer_id = Uuid::from_slice(&command.consumer_id)?;
    if command_consumer_id != resource_id || command.config_version == 0 {
        return Err("Mail consumer upsert outbox identity/version mismatch".into());
    }

    if status == "FAILED" && command.config_version == 1 {
        let delete_version = 2_i64;
        let namespace = Uuid::parse_str(CONSUMER_EVENT_NAMESPACE)?;
        let delete_event_id = Uuid::new_v5(
            &namespace,
            format!("consumer:{resource_id}:{delete_version}:delete:{zone_id}").as_bytes(),
        );
        // [COMMENT]: Tombstone và hard delete phụ thuộc cùng exact config_version; result V1 đến
        // muộn sau update V2 không thể xóa connection mới hơn.
        transaction
            .execute(
                "WITH target AS MATERIALIZED ( \
                     SELECT id FROM mail.mail_consumers \
                     WHERE id=$1 AND config_version=1 \
                     FOR UPDATE \
                 ), tombstone AS ( \
                     INSERT INTO mail.mail_consumer_projection_tombstones( \
                         consumer_id,zone_id,config_version,delete_event_id,tombstoned_at \
                     ) \
                     SELECT id,$2,$3,$4,NOW() FROM target \
                     ON CONFLICT (consumer_id) DO UPDATE SET \
                         zone_id=EXCLUDED.zone_id,config_version=EXCLUDED.config_version, \
                         delete_event_id=EXCLUDED.delete_event_id,tombstoned_at=EXCLUDED.tombstoned_at \
                     WHERE EXCLUDED.config_version > mail_consumer_projection_tombstones.config_version \
                     RETURNING consumer_id \
                 ) \
                 DELETE FROM mail.mail_consumers AS consumer \
                 WHERE consumer.id=$1 AND consumer.config_version=1 \
                   AND (EXISTS(SELECT 1 FROM tombstone) OR EXISTS( \
                       SELECT 1 FROM mail.mail_consumer_projection_tombstones AS saved \
                       WHERE saved.consumer_id=$1 AND saved.config_version >= $3 \
                   ))",
                &[&resource_id, &zone_id, &delete_version, &delete_event_id],
            )
            .await?;
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
/// FAILED chỉ đóng outbox, giữ row `deleting` để reconciler tiếp tục phát delete command.
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
            "SELECT status,result_attempt,resource_id,zone_id,payload \
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
    // [COMMENT]: Viết riêng transition của delete để transaction boundary hiện rõ; result
    // attempt cũ không được hard-delete sau khi một retry mới hơn đã bắt đầu.
    let transition_allowed = current_status != "SUCCEEDED"
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
        transaction.commit().await?;
        return Ok(None);
    }
    let resource_id = Uuid::parse_str(locked.get::<_, Option<String>>(2).as_deref().unwrap_or(""))?;
    let zone_id: Uuid = locked.get(3);
    let payload: Vec<u8> = locked.get(4);
    let command = MailConsumerDeleteV1::decode(payload.as_slice())?;
    let command_consumer_id = Uuid::from_slice(&command.consumer_id)?;
    if command_consumer_id != resource_id || command.config_version == 0 {
        return Err("Mail consumer delete outbox identity/version mismatch".into());
    }

    if status == "SUCCEEDED" {
        let config_version = i64::try_from(command.config_version)
            .map_err(|_| "Mail consumer delete config_version exceeds BIGINT")?;
        // [COMMENT]: Tombstone được ghi cả khi row đã mất do retry; hard delete chỉ chạm exact
        // deleting generation nên result cũ không thể xóa consumer generation mới hơn.
        transaction
            .execute(
                "INSERT INTO mail.mail_consumer_projection_tombstones( \
                     consumer_id,zone_id,config_version,delete_event_id,tombstoned_at \
                 ) VALUES ($1,$2,$3,$4,NOW()) \
                 ON CONFLICT (consumer_id) DO UPDATE SET \
                     zone_id=EXCLUDED.zone_id,config_version=EXCLUDED.config_version, \
                     delete_event_id=EXCLUDED.delete_event_id,tombstoned_at=EXCLUDED.tombstoned_at \
                 WHERE EXCLUDED.config_version > mail_consumer_projection_tombstones.config_version",
                &[&resource_id, &zone_id, &config_version, &event_id],
            )
            .await?;
        transaction
            .execute(
                "DELETE FROM mail.mail_consumers \
                 WHERE id=$1 AND config_version=$2 AND desired_state='deleting'",
                &[&resource_id, &config_version],
            )
            .await?;
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
