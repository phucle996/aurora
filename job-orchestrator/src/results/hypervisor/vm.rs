use crate::contracts::hypervisor as hypervisor_proto;
use prost::Message;

pub async fn apply_vm_result(
    pg_client: &mut tokio_postgres::Client,
    job_id: uuid::Uuid,
    job_topic: &str,
    status: &str,
    error_code: Option<&str>,
    error_message: Option<&str>,
    result_payload: &[u8],
    result_payload_schema_version: u32,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
    let tx = pg_client.transaction().await?;
    // Custom PostgreSQL enums cross this driver boundary as text so every JO
    // connection does not need mutable per-schema type registration.
    let authority = tx
        .query_opt(
            "SELECT outbox.resource_id, outbox.status::text, vm.spec_hash, vm.provider_name \
             FROM hypervisor.hypervisor_outbox_records outbox \
             JOIN hypervisor.personal_vms vm ON vm.id::text = outbox.resource_id \
             WHERE outbox.event_id = $1 AND outbox.job_topic = $2 \
             FOR UPDATE OF outbox, vm",
            &[&job_id, &job_topic],
        )
        .await?;
    let Some(authority) = authority else {
        tx.rollback().await?;
        return Ok(None);
    };

    let resource_id: String = authority.get(0);
    let current_status: String = authority.get(1);
    let config_hash: Vec<u8> = authority.get(2);
    let provider_name: String = authority.get(3);
    if !matches!(current_status.as_str(), "PENDING" | "PROCESSING") {
        tx.rollback().await?;
        return Ok(None);
    }
    let vm_id = uuid::Uuid::parse_str(&resource_id)?;

    let row = match status {
        "PROCESSING" => {
            if !result_payload.is_empty() || result_payload_schema_version != 0 {
                return Err("PROCESSING hypervisor result must not carry a result payload".into());
            }
            tx.query_opt(
                "WITH updated_vm AS ( \
                     UPDATE hypervisor.personal_vms \
                     SET status = 'PROVISIONING', updated_at = NOW() \
                     WHERE id = $1 \
                 ) \
                 UPDATE hypervisor.hypervisor_outbox_records \
                 SET status = 'PROCESSING', error_code = NULL, error_message = NULL, updated_at = NOW() \
                 WHERE event_id = $2 AND job_topic = $3 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&vm_id, &job_id, &job_topic],
            )
            .await?
        }
        "SUCCEEDED" => {
            if result_payload_schema_version != 1 {
                return Err("hypervisor result payload schema version is unsupported".into());
            }
            let result = hypervisor_proto::VmCreateResultV1::decode(result_payload)?;
            if result.schema_version != 1
                || result.vm_id.as_slice() != vm_id.as_bytes()
                || result.provider_name != provider_name
                || result.provider_node.trim().is_empty()
                || result.provider_vmid == 0
                || result.config_hash != config_hash
            {
                // Result fields cross a Zone trust boundary and cannot redefine
                // the authoritative resource or immutable VM specification.
                return Err("hypervisor result payload does not match the authoritative VM".into());
            }
            let provider_vmid = i64::try_from(result.provider_vmid)
                .map_err(|_| "provider VMID exceeds PostgreSQL bigint")?;
            let ipv4_address = result
                .ipv4_address
                .trim()
                .parse::<std::net::Ipv4Addr>()
                .ok()
                .map(|address| address.to_string());
            tx.query_opt(
                "WITH updated_vm AS ( \
                     UPDATE hypervisor.personal_vms \
                     SET status = 'READY', provider_vmid = $1, \
                         ipv4_address = $2::inet, \
                         provisioned_at = COALESCE(provisioned_at, NOW()), updated_at = NOW() \
                     WHERE id = $3 \
                 ) \
                 UPDATE hypervisor.hypervisor_outbox_records \
                 SET status = 'SUCCEEDED', completed_at = NOW(), error_code = NULL, \
                     error_message = NULL, updated_at = NOW() \
                 WHERE event_id = $4 AND job_topic = $5 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&provider_vmid, &ipv4_address, &vm_id, &job_id, &job_topic],
            )
            .await?
        }
        "FAILED" => {
            if !result_payload.is_empty() || result_payload_schema_version != 0 {
                return Err("FAILED hypervisor result must not carry a result payload".into());
            }
            // Resource deletion and terminal fencing are one transaction:
            // retries can reuse the natural name, while duplicate results
            // still resolve through the retained outbox operation.
            tx.query_opt(
                "WITH deleted_vm AS ( \
                     DELETE FROM hypervisor.personal_vms \
                     WHERE id = $1 AND status = 'PROVISIONING' \
                     RETURNING id \
                 ) \
                 UPDATE hypervisor.hypervisor_outbox_records \
                 SET status = 'FAILED', completed_at = NOW(), error_code = $2, \
                     error_message = $3, updated_at = NOW() \
                 WHERE event_id = $4 AND job_topic = $5 \
                   AND status IN ('PENDING', 'PROCESSING') \
                   AND EXISTS (SELECT 1 FROM deleted_vm) \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&vm_id, &error_code, &error_message, &job_id, &job_topic],
            )
            .await?
        }
        _ => return Err(format!("unsupported hypervisor result status '{status}'").into()),
    };

    tx.commit().await?;
    Ok(row)
}
