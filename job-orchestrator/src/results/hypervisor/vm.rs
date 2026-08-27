use super::VmResultRequest;
use crate::contracts::hypervisor as hypervisor_proto;
use prost::Message;

pub async fn apply_vm_create_result(
    pg_client: &mut tokio_postgres::Client,
    result: VmResultRequest<'_>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
    let VmResultRequest {
        job_id,
        job_topic,
        status,
        error_code,
        error_message,
        result_payload,
        result_payload_schema_version,
    } = result;
    let tx = pg_client.transaction().await?;
    // Custom PostgreSQL enums cross this driver boundary as text so every JO
    // connection does not need mutable per-schema type registration.
    let authority = tx
        .query_opt(
            "SELECT outbox.resource_id, outbox.status::text, vm.spec_hash, vm.provider_name, vm.status::text \
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
    let current_vm_status: String = authority.get(4);
    if !matches!(current_status.as_str(), "PENDING" | "PROCESSING") {
        tx.rollback().await?;
        return Ok(None);
    }
    if current_vm_status != "PROVISIONING" {
        return Err("Hypervisor create result found a VM outside PROVISIONING state".into());
    }
    let vm_id = uuid::Uuid::parse_str(&resource_id)?;

    let row = match status {
        "PROCESSING" => {
            if !result_payload.is_empty() || result_payload_schema_version != 0 {
                return Err("PROCESSING hypervisor result must not carry a result payload".into());
            }
            tx.query_opt(
                "UPDATE hypervisor.hypervisor_outbox_records \
                 SET status = 'PROCESSING', error_code = NULL, error_message = NULL, updated_at = NOW() \
                 WHERE event_id = $1 AND job_topic = $2 AND status IN ('PENDING', 'PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&job_id, &job_topic],
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
                || result.provider_completed_at_unix_ms <= 0
                || result.provider_completed_at_unix_ms
                    > chrono::Utc::now().timestamp_millis() + 30_000
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
                         provisioned_at = COALESCE(provisioned_at, TIMESTAMPTZ 'epoch' + $6::bigint * INTERVAL '1 millisecond'), updated_at = NOW() \
                     WHERE id = $3 AND status = 'PROVISIONING' \
                     RETURNING id, owner_user_id, name, zone_id, cpu_cores, memory_mb, disk_gb, provisioned_at \
                 ), settled_job AS ( \
                     UPDATE hypervisor.hypervisor_outbox_records \
                     SET status = 'SUCCEEDED', completed_at = NOW(), error_code = NULL, \
                         error_message = NULL, updated_at = NOW() \
                     WHERE event_id = $4 AND job_topic = $5 AND status IN ('PENDING', 'PROCESSING') \
                       AND EXISTS (SELECT 1 FROM updated_vm) \
                     RETURNING event_id, actor_user_id::text, job_topic, trace_id, resource_id \
                 ), allocation_appended AS ( \
                     INSERT INTO hypervisor.hypervisor_allocation_outbox ( \
                         source_job_id, event_type, resource_id, owner_id, owner_type, resource_name, \
                         zone_id, source_version, effective_at, cpu_cores, memory_mib, disk_gib \
                     ) \
                     SELECT settled_job.event_id, 'ACTIVATE', updated_vm.id, updated_vm.owner_user_id, \
                            'PERSONAL', updated_vm.name, updated_vm.zone_id, 1, updated_vm.provisioned_at, \
                            updated_vm.cpu_cores, updated_vm.memory_mb, updated_vm.disk_gb \
                     FROM updated_vm CROSS JOIN settled_job \
                     ON CONFLICT (source_job_id) DO NOTHING \
                     RETURNING source_job_id \
                 ) \
                 SELECT settled_job.actor_user_id, settled_job.job_topic, settled_job.trace_id, settled_job.resource_id \
                 FROM settled_job JOIN allocation_appended ON allocation_appended.source_job_id = settled_job.event_id",
                &[&provider_vmid, &ipv4_address, &vm_id, &job_id, &job_topic, &result.provider_completed_at_unix_ms],
            )
            .await?
        }
        "FAILED" => {
            if !result_payload.is_empty() || result_payload_schema_version != 0 {
                return Err("FAILED hypervisor result must not carry a result payload".into());
            }
            tx.query_opt(
                "WITH failed_vm AS ( \
                     UPDATE hypervisor.personal_vms \
                     SET status = 'FAILED', updated_at = NOW() \
                     WHERE id = $1 AND status = 'PROVISIONING' \
                     RETURNING id \
                 ) \
                 UPDATE hypervisor.hypervisor_outbox_records \
                 SET status = 'FAILED', completed_at = NOW(), error_code = $2, \
                     error_message = $3, updated_at = NOW() \
                 WHERE event_id = $4 AND job_topic = $5 \
                   AND status IN ('PENDING', 'PROCESSING') \
                   AND EXISTS (SELECT 1 FROM failed_vm) \
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

pub async fn apply_vm_delete_result(
    pg_client: &mut tokio_postgres::Client,
    result: VmResultRequest<'_>,
) -> Result<Option<tokio_postgres::Row>, Box<dyn std::error::Error + Send + Sync>> {
    let VmResultRequest {
        job_id,
        job_topic,
        status,
        error_code,
        error_message,
        result_payload,
        result_payload_schema_version,
    } = result;
    let tx = pg_client.transaction().await?;
    let authority = tx
        .query_opt(
            "SELECT outbox.resource_id, outbox.status::text, vm.status::text, \
                    vm.provider_name, vm.provider_vmid, vm.owner_user_id, vm.name, vm.zone_id \
             FROM hypervisor.hypervisor_outbox_records outbox \
             JOIN hypervisor.personal_vms vm ON vm.id::text = outbox.resource_id \
             WHERE outbox.event_id = $1 AND outbox.job_topic = 'hypervisor.vm.delete' \
               AND outbox.job_topic = $2 FOR UPDATE OF outbox, vm",
            &[&job_id, &job_topic],
        )
        .await?;
    let Some(authority) = authority else {
        tx.rollback().await?;
        return Ok(None);
    };
    let resource_id: String = authority.get(0);
    let current_outbox_status: String = authority.get(1);
    let current_vm_status: String = authority.get(2);
    let provider_name: String = authority.get(3);
    let provider_vmid: Option<i64> = authority.get(4);
    let owner_id: uuid::Uuid = authority.get(5);
    let resource_name: String = authority.get(6);
    let zone_id: uuid::Uuid = authority.get(7);
    if !matches!(current_outbox_status.as_str(), "PENDING" | "PROCESSING") {
        tx.rollback().await?;
        return Ok(None);
    }
    if current_vm_status != "DELETING" {
        return Err("Hypervisor delete result found a VM outside DELETING state".into());
    }
    let vm_id = uuid::Uuid::parse_str(&resource_id)?;
    let provider_vmid = provider_vmid.ok_or("Hypervisor delete authority has no provider VMID")?;

    let row = match status {
        "PROCESSING" => {
            if !result_payload.is_empty() || result_payload_schema_version != 0 {
                return Err("PROCESSING Hypervisor delete result must not carry a payload".into());
            }
            tx.query_opt(
                "UPDATE hypervisor.hypervisor_outbox_records \
                 SET status='PROCESSING', error_code=NULL, error_message=NULL, updated_at=NOW() \
                 WHERE event_id=$1 AND job_topic=$2 AND status IN ('PENDING','PROCESSING') \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&job_id, &job_topic],
            )
            .await?
        }
        "SUCCEEDED" => {
            if result_payload_schema_version != 1 {
                return Err(
                    "Hypervisor delete result payload schema version is unsupported".into(),
                );
            }
            let decoded = hypervisor_proto::VmDeleteResultV1::decode(result_payload)?;
            if decoded.schema_version != 1
                || decoded.vm_id.as_slice() != vm_id.as_bytes()
                || decoded.provider_name != provider_name
                || i64::try_from(decoded.provider_vmid).ok() != Some(provider_vmid)
                || decoded.provider_completed_at_unix_ms <= 0
                || decoded.provider_completed_at_unix_ms
                    > chrono::Utc::now().timestamp_millis() + 30_000
            {
                return Err("Hypervisor delete result does not match durable authority".into());
            }
            tx.query_opt(
                "WITH deleted_vm AS ( \
                     DELETE FROM hypervisor.personal_vms \
                     WHERE id=$1 AND status='DELETING' AND provider_name=$2 AND provider_vmid=$3 \
                     RETURNING id \
                 ), settled_job AS ( \
                     UPDATE hypervisor.hypervisor_outbox_records \
                     SET status='SUCCEEDED', completed_at=NOW(), error_code=NULL, error_message=NULL, updated_at=NOW() \
                     WHERE event_id=$4 AND job_topic=$5 AND status IN ('PENDING','PROCESSING') \
                       AND EXISTS (SELECT 1 FROM deleted_vm) \
                     RETURNING event_id, actor_user_id::text, job_topic, trace_id, resource_id, completed_at \
                 ), next_version AS ( \
                     SELECT COALESCE(MAX(source_version), 0) + 1 AS source_version \
                     FROM hypervisor.hypervisor_allocation_outbox WHERE resource_id=$1 \
                 ), allocation_appended AS ( \
                     INSERT INTO hypervisor.hypervisor_allocation_outbox ( \
                         source_job_id, event_type, resource_id, owner_id, owner_type, resource_name, \
                         zone_id, source_version, effective_at, cpu_cores, memory_mib, disk_gib \
                     ) \
                     SELECT settled_job.event_id, 'TERMINATE', $1, $6, 'PERSONAL', $7, $8, \
                            next_version.source_version, TIMESTAMPTZ 'epoch' + $9::bigint * INTERVAL '1 millisecond', 0, 0, 0 \
                     FROM settled_job CROSS JOIN next_version \
                     WHERE next_version.source_version > 1 \
                     ON CONFLICT (source_job_id) DO NOTHING \
                     RETURNING source_job_id \
                 ) \
                 SELECT settled_job.actor_user_id, settled_job.job_topic, settled_job.trace_id, settled_job.resource_id \
                 FROM settled_job JOIN allocation_appended ON allocation_appended.source_job_id = settled_job.event_id",
                &[
                    &vm_id,
                    &provider_name,
                    &provider_vmid,
                    &job_id,
                    &job_topic,
                    &owner_id,
                    &resource_name,
                    &zone_id,
                    &decoded.provider_completed_at_unix_ms,
                ],
            )
            .await?
        }
        "FAILED" => {
            if !result_payload.is_empty() || result_payload_schema_version != 0 {
                return Err("FAILED Hypervisor delete result must not carry a payload".into());
            }
            tx.query_opt(
                "WITH retained_vm AS ( \
                     SELECT id FROM hypervisor.personal_vms \
                     WHERE id=$1 AND status='DELETING' \
                 ) \
                 UPDATE hypervisor.hypervisor_outbox_records \
                 SET status='FAILED', completed_at=NOW(), error_code=$2, error_message=$3, updated_at=NOW() \
                 WHERE event_id=$4 AND job_topic=$5 AND status IN ('PENDING','PROCESSING') \
                   AND EXISTS (SELECT 1 FROM retained_vm) \
                 RETURNING actor_user_id::text, job_topic, trace_id, resource_id",
                &[&vm_id, &error_code, &error_message, &job_id, &job_topic],
            )
            .await?
        }
        _ => return Err(format!("unsupported Hypervisor delete result status '{status}'").into()),
    };
    // Returning a row authorizes the caller to enqueue Centrifugo/timeline
    // notification, so the VM deletion and outbox fence must commit first.
    tx.commit().await?;
    Ok(row)
}
