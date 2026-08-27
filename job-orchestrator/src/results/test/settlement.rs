use prost::Message;
use tokio_postgres::NoTls;
use uuid::Uuid;

#[tokio::test]
#[ignore = "requires an empty dedicated AURORA_TEST_POSTGRES database"]
async fn vm_delete_failed_result_retains_deleting_resource_and_settles_job() {
    let (mut client, connection) = tokio_postgres::connect(
        &std::env::var("AURORA_TEST_POSTGRES").expect("dedicated test database DSN"),
        NoTls,
    )
    .await
    .unwrap();
    tokio::spawn(async move {
        connection.await.unwrap();
    });
    assert_eq!(
        client
            .query_one(
                "SELECT count(*) FROM pg_namespace WHERE nspname='hypervisor'",
                &[]
            )
            .await
            .unwrap()
            .get::<_, i64>(0),
        0,
        "refusing to modify existing application schema"
    );
    client
        .batch_execute(
            r#"
        CREATE SCHEMA hypervisor;
        CREATE TABLE hypervisor.personal_vms (
            id uuid PRIMARY KEY, status text, provider_name text, provider_vmid bigint,
            owner_user_id uuid, name text, zone_id uuid
        );
        CREATE TABLE hypervisor.hypervisor_outbox_records (
            event_id uuid PRIMARY KEY, job_topic text, resource_id text, status text,
            actor_user_id uuid, trace_id uuid, completed_at timestamptz, updated_at timestamptz,
            error_code text, error_message text
        );
    "#,
        )
        .await
        .unwrap();
    let vm = Uuid::new_v4();
    let job = Uuid::new_v4();
    let actor = Uuid::new_v4();
    client
        .execute(
            "INSERT INTO hypervisor.personal_vms VALUES($1,'DELETING',$2,100,$3,'test',$4)",
            &[&vm, &format!("aurora-{vm}"), &actor, &Uuid::new_v4()],
        )
        .await
        .unwrap();
    client.execute("INSERT INTO hypervisor.hypervisor_outbox_records(event_id,job_topic,resource_id,status,actor_user_id) VALUES($1,'hypervisor.vm.delete',$2,'PROCESSING',$3)",
        &[&job, &vm.to_string(), &actor]).await.unwrap();
    let result = crate::results::hypervisor::apply_vm_delete_result(
        &mut client,
        crate::results::hypervisor::VmResultRequest {
            job_id: job,
            job_topic: "hypervisor.vm.delete",
            status: "FAILED",
            error_code: Some("PROVIDER_FAILURE"),
            error_message: Some("provider failure requires attention"),
            result_payload: &[],
            result_payload_schema_version: 0,
        },
    )
    .await
    .unwrap();
    assert!(result.is_some());
    assert_eq!(
        client
            .query_one(
                "SELECT status FROM hypervisor.personal_vms WHERE id=$1",
                &[&vm]
            )
            .await
            .unwrap()
            .get::<_, String>(0),
        "DELETING"
    );
    assert_eq!(
        client
            .query_one(
                "SELECT status FROM hypervisor.hypervisor_outbox_records WHERE event_id=$1",
                &[&job]
            )
            .await
            .unwrap()
            .get::<_, String>(0),
        "FAILED"
    );
    let replay = crate::results::hypervisor::apply_vm_delete_result(
        &mut client,
        crate::results::hypervisor::VmResultRequest {
            job_id: job,
            job_topic: "hypervisor.vm.delete",
            status: "FAILED",
            error_code: Some("PROVIDER_FAILURE"),
            error_message: None,
            result_payload: &[],
            result_payload_schema_version: 0,
        },
    )
    .await
    .unwrap();
    assert!(replay.is_none());
    client
        .batch_execute("DROP SCHEMA hypervisor CASCADE")
        .await
        .unwrap();
}

#[tokio::test]
#[ignore = "requires an empty dedicated AURORA_TEST_POSTGRES database"]
async fn storage_lifecycle_and_managed_retry_settle_in_postgres() {
    let (mut client, connection) = tokio_postgres::connect(
        &std::env::var("AURORA_TEST_POSTGRES").expect("dedicated test database DSN"),
        NoTls,
    )
    .await
    .unwrap();
    tokio::spawn(async move {
        connection.await.unwrap();
    });
    assert_eq!(
        client
            .query_one(
                "SELECT count(*) FROM pg_namespace WHERE nspname IN ('storage','managed_service')",
                &[]
            )
            .await
            .unwrap()
            .get::<_, i64>(0),
        0,
        "refusing to modify an existing application schema"
    );
    client.batch_execute(r#"
        CREATE SCHEMA storage;
        CREATE TABLE storage.storage_outbox_records (
            event_id uuid PRIMARY KEY, job_topic text, owner_type text, status text,
            resource_id text, actor_user_id uuid, trace_id uuid,
            completed_at timestamptz, updated_at timestamptz, error_code text, error_message text
        );
        CREATE TABLE storage.personal_buckets (
            id uuid PRIMARY KEY, lifecycle_rules jsonb DEFAULT '[]', status text, updated_at timestamptz,
            capacity_quota_bytes bigint DEFAULT 1024, versioning_enabled boolean DEFAULT false
        );
        CREATE TABLE storage.tenant_buckets (LIKE storage.personal_buckets INCLUDING ALL);
        CREATE SCHEMA managed_service;
        CREATE TABLE managed_service.managed_service_outbox_records (
            event_id uuid PRIMARY KEY, job_topic text, status text, owner_type text,
            zone_id uuid, delivery_epoch bigint, resource_id text, actor_user_id uuid,
            completed_at timestamptz, updated_at timestamptz, error_code text, error_message text
        );
    "#).await.unwrap();

    for (branch, owner) in [("personal", "PERSONAL"), ("tenant", "TENANT")] {
        let event = Uuid::new_v4();
        let bucket = Uuid::new_v4();
        client.execute("INSERT INTO storage.storage_outbox_records(event_id, job_topic, owner_type, status, resource_id) VALUES($1,'storage.bucket.lifecycle',$2,'PROCESSING',$3)", &[&event, &owner, &bucket.to_string()]).await.unwrap();
        client
            .execute(
                &format!("INSERT INTO storage.{branch}_buckets(id,status) VALUES($1,'UPDATING')"),
                &[&bucket],
            )
            .await
            .unwrap();
        let payload = crate::contracts::storage::BucketLifecycleAppliedV1 {
            schema_version: 1,
            bucket_id: bucket.to_string(),
            actual_rules: Vec::new(),
        }
        .encode_to_vec();
        // A lifecycle job cannot be settled through either of the other topic owners.
        assert!(crate::results::storage::bucket::resolve_bucket_resize(
            &client,
            event,
            "SUCCEEDED",
            None,
            None,
            &payload,
            1,
        )
        .await
        .unwrap()
        .is_none());
        assert!(crate::results::storage::bucket::resolve_bucket_versioning(
            &client,
            event,
            "SUCCEEDED",
            None,
            None,
            &payload,
            1,
        )
        .await
        .unwrap()
        .is_none());
        let outcome = crate::results::storage::bucket::resolve_bucket_lifecycle(
            &client,
            event,
            "SUCCEEDED",
            None,
            None,
            &payload,
            1,
        )
        .await;
        assert!(
            outcome.unwrap().is_some(),
            "valid lifecycle result did not settle"
        );
        let state: String = client
            .query_one(
                &format!("SELECT status FROM storage.{branch}_buckets WHERE id=$1"),
                &[&bucket],
            )
            .await
            .unwrap()
            .get(0);
        assert_eq!(state, "READY");

        let quota_payload = crate::contracts::storage::BucketQuotaAppliedV1 {
            schema_version: 1,
            bucket_id: bucket.to_string(),
            actual_quota_bytes: 2048,
        }
        .encode_to_vec();
        let versioning_payload = crate::contracts::storage::BucketVersioningAppliedV1 {
            schema_version: 1,
            bucket_id: bucket.to_string(),
            actual_versioning_enabled: true,
        }
        .encode_to_vec();
        for (topic, payload) in [
            ("storage.bucket.resize", &quota_payload),
            ("storage.bucket.versioning", &versioning_payload),
        ] {
            let update_event = Uuid::new_v4();
            client.execute(
                "INSERT INTO storage.storage_outbox_records(event_id,job_topic,owner_type,status,resource_id) VALUES($1,$2,$3,'PROCESSING',$4)",
                &[&update_event, &topic, &owner, &bucket.to_string()],
            ).await.unwrap();
            client
                .execute(
                    &format!("UPDATE storage.{branch}_buckets SET status='UPDATING' WHERE id=$1"),
                    &[&bucket],
                )
                .await
                .unwrap();
            assert!(crate::results::storage::bucket::resolve_bucket_lifecycle(
                &client,
                update_event,
                "SUCCEEDED",
                None,
                None,
                payload,
                1,
            )
            .await
            .unwrap()
            .is_none());
            for replay in [false, true] {
                let outcome = crate::results::storage::apply::apply_storage_result(
                    &client,
                    crate::results::storage::apply::StorageResultRequest {
                        job_id: update_event,
                        job_topic: topic,
                        status: "SUCCEEDED",
                        error_code: None,
                        error_message: None,
                        result_payload: payload,
                        result_payload_schema_version: 1,
                    },
                )
                .await
                .unwrap();
                assert_eq!(outcome.is_none(), replay, "{owner} {topic} replay={replay}");
            }
            let settled = client.query_one(
                &format!("SELECT b.status, b.capacity_quota_bytes, b.versioning_enabled, o.status FROM storage.{branch}_buckets b JOIN storage.storage_outbox_records o ON o.resource_id=b.id::text WHERE o.event_id=$1"),
                &[&update_event],
            ).await.unwrap();
            assert_eq!(settled.get::<_, String>(0), "READY");
            assert_eq!(settled.get::<_, i64>(1), 2048);
            assert_eq!(
                settled.get::<_, bool>(2),
                topic == "storage.bucket.versioning"
            );
            assert_eq!(settled.get::<_, String>(3), "SUCCEEDED");
        }
        let text_control = "[]".to_owned();
        client
            .query_one("SELECT $1::text::jsonb", &[&text_control])
            .await
            .unwrap();

        client
            .batch_execute(&format!(
                r#"
            CREATE TABLE managed_service.{branch}_managed_service_instances (
                id uuid PRIMARY KEY, generation bigint, zone_id uuid, pending_revision_id uuid,
                active_revision_id uuid, state text, updated_at timestamptz
            );
            CREATE TABLE managed_service.{branch}_managed_service_operations (
                id uuid PRIMARY KEY, instance_id uuid, kind text, state text, generation bigint,
                attempt smallint, delivery_epoch bigint, current_command_event_id uuid,
                target_revision_id uuid, blueprint_revision_id uuid, zone_id uuid,
                template_bundle_sha256 bytea, component_contract_sha256 bytea,
                input_sha256 bytea, desired_spec_sha256 bytea, completed_at timestamptz,
                last_error_code text, last_sanitized_error text, status_version bigint DEFAULT 1,
                updated_at timestamptz
            );
        "#
            ))
            .await
            .unwrap();
        let instance = Uuid::new_v4();
        let zone = Uuid::new_v4();
        let operation = Uuid::new_v4();
        let event = Uuid::new_v4();
        let revision = Uuid::new_v4();
        let blueprint = Uuid::new_v4();
        let hash = vec![1_u8; 32];
        client.execute("INSERT INTO managed_service.managed_service_outbox_records(event_id,job_topic,status,owner_type,zone_id,delivery_epoch,resource_id) VALUES($1,'managed_service.instance.execute','PROCESSING',$2,$3,0,$4)", &[&event,&owner,&zone,&instance.to_string()]).await.unwrap();
        client.execute(&format!("INSERT INTO managed_service.{branch}_managed_service_instances(id,generation,zone_id,pending_revision_id,state) VALUES($1,1,$2,$3,'provisioning')"), &[&instance,&zone,&revision]).await.unwrap();
        client.execute(&format!("INSERT INTO managed_service.{branch}_managed_service_operations(id,instance_id,kind,state,generation,attempt,delivery_epoch,current_command_event_id,target_revision_id,blueprint_revision_id,zone_id,template_bundle_sha256,component_contract_sha256,input_sha256,desired_spec_sha256) VALUES($1,$2,'create','accepted',1,0,0,$3,$4,$5,$6,$7,$7,$7,$7)"), &[&operation,&instance,&event,&revision,&blueprint,&zone,&hash]).await.unwrap();
        let mut result = crate::results::contract::ValidatedManagedServiceResult {
            source_command_event_id: event,
            operation_id: operation,
            instance_id: instance,
            zone_id: zone,
            generation: 1,
            attempt: 1,
            instance_revision_id: revision,
            blueprint_revision_id: blueprint,
            bundle_hash: hash.clone(),
            component_contract_hash: hash.clone(),
            input_hash: hash.clone(),
            desired_spec_hash: hash,
            status: "SUCCEEDED",
            error_code: None,
            sanitized_message: String::new(),
            delivery_epoch: 0,
        };
        let outcome = crate::results::managed_service::apply_result(&mut client, &result)
            .await
            .unwrap();
        assert!(outcome.is_some(), "same-fence retry must settle");

        result.attempt = 0;
        let outcome = crate::results::managed_service::apply_result(&mut client, &result)
            .await
            .unwrap();
        assert!(
            outcome.is_none(),
            "older attempt must not overwrite terminal state"
        );
        let state: String = client.query_one(&format!("SELECT state FROM managed_service.{branch}_managed_service_instances WHERE id=$1"), &[&instance]).await.unwrap().get(0);
        assert_eq!(state, "active");
    }
}

#[tokio::test]
#[ignore = "requires an empty dedicated AURORA_TEST_POSTGRES database"]
async fn mail_drain_settlement_replay_and_failed_create_keep_resource() {
    use crate::contracts::mail::MailConsumerDrainedV1;
    use crate::results::contract::{job_proto::JobExecutionResultProto, ValidatedResult};
    let (mut client, connection) = tokio_postgres::connect(
        &std::env::var("AURORA_TEST_POSTGRES").expect("dedicated test database DSN"),
        NoTls,
    )
    .await
    .unwrap();
    tokio::spawn(async move {
        connection.await.unwrap();
    });
    assert_eq!(
        client
            .query_one(
                "SELECT count(*) FROM pg_namespace WHERE nspname IN ('mail','hierarchy')",
                &[]
            )
            .await
            .unwrap()
            .get::<_, i64>(0),
        0,
        "refusing existing application schemas"
    );
    client.batch_execute(r#"
        CREATE SCHEMA mail;
        CREATE SCHEMA hierarchy;
        CREATE TABLE hierarchy.personal_workspaces(id uuid PRIMARY KEY, zone_id uuid);
        CREATE TABLE hierarchy.tenant_workspaces(LIKE hierarchy.personal_workspaces INCLUDING ALL);
        CREATE TABLE mail.personal_mail_consumers(id uuid PRIMARY KEY, workspace_id uuid, config_version bigint, parallelism int, desired_state text, updated_at timestamptz);
        CREATE TABLE mail.tenant_mail_consumers(LIKE mail.personal_mail_consumers INCLUDING ALL);
        CREATE TABLE mail.personal_mail_consumer_update_versions(consumer_id uuid,event_id uuid,config_version bigint);
        CREATE TABLE mail.tenant_mail_consumer_update_versions(LIKE mail.personal_mail_consumer_update_versions);
        CREATE TABLE mail.mail_outbox_records(event_id uuid PRIMARY KEY,zone_id uuid,resource_id text,job_topic text,status text,result_attempt int DEFAULT 0,actor_user_id uuid,trace_id bytea,completed_at timestamptz,updated_at timestamptz,error_code text,error_message text);
    "#).await.unwrap();
    let migration = include_str!(
        "../../../../controlplane/internal/mail/migrations/000004_mail_triggers.up.sql"
    );
    let consumer_triggers = migration
        .split("CREATE OR REPLACE FUNCTION reject_mail_template_version_mutation()")
        .next()
        .unwrap();
    client
        .batch_execute(&format!("SET search_path TO mail; {consumer_triggers}"))
        .await
        .unwrap();
    for branch in ["personal", "tenant"] {
        let event = Uuid::new_v4();
        let id = Uuid::new_v4();
        let zone = Uuid::new_v4();
        let workspace = Uuid::new_v4();
        client
            .execute(
                &format!("INSERT INTO hierarchy.{branch}_workspaces VALUES($1,$2)"),
                &[&workspace, &zone],
            )
            .await
            .unwrap();
        client
            .execute(
                &format!(
                    "INSERT INTO mail.{branch}_mail_consumers VALUES($1,$2,7,4,'draining',NOW())"
                ),
                &[&id, &workspace],
            )
            .await
            .unwrap();
        client.execute("INSERT INTO mail.mail_outbox_records(event_id,zone_id,resource_id,job_topic,status,result_attempt) VALUES($1,$2,$3,'mail.consumer.drain','PROCESSING',1)", &[&event,&zone,&id.to_string()]).await.unwrap();
        let mut result = ValidatedResult {
            job_id: event,
            managed_service: None,
            wire: JobExecutionResultProto {
                result_status: "SUCCEEDED".into(),
                attempt: 1,
                result_payload_schema_version: 1,
                result_payload: MailConsumerDrainedV1 {
                    schema_version: 1,
                    consumer_id: id.as_bytes().to_vec(),
                    config_version: 6,
                    settled_slots: 4,
                }
                .encode_to_vec(),
                ..Default::default()
            },
        };
        assert!(
            crate::results::mail::consumer::apply_drain_result(&mut client, &result)
                .await
                .is_err(),
            "stale version must not mark drained"
        );
        let state: String = client
            .query_one(
                &format!("SELECT desired_state FROM mail.{branch}_mail_consumers WHERE id=$1"),
                &[&id],
            )
            .await
            .unwrap()
            .get(0);
        assert_eq!(state, "draining");
        result.wire.result_payload = MailConsumerDrainedV1 {
            schema_version: 1,
            consumer_id: id.as_bytes().to_vec(),
            config_version: 7,
            settled_slots: 4,
        }
        .encode_to_vec();
        assert!(
            crate::results::mail::consumer::apply_drain_result(&mut client, &result)
                .await
                .unwrap()
                .is_some()
        );
        let state: String = client
            .query_one(
                &format!("SELECT desired_state FROM mail.{branch}_mail_consumers WHERE id=$1"),
                &[&id],
            )
            .await
            .unwrap()
            .get(0);
        assert_eq!(state, "drained");
        assert!(
            crate::results::mail::consumer::apply_drain_result(&mut client, &result)
                .await
                .unwrap()
                .is_some(),
            "terminal replay must retry notification"
        );
        result.wire.result_status = "PROCESSING".into();
        result.wire.result_payload.clear();
        result.wire.result_payload_schema_version = 0;
        assert!(
            crate::results::mail::consumer::apply_drain_result(&mut client, &result)
                .await
                .unwrap()
                .is_none(),
            "late processing must not downgrade success"
        );

        let failed_event = Uuid::new_v4();
        let failed_id = Uuid::new_v4();
        client
            .execute(
                &format!(
                    "INSERT INTO mail.{branch}_mail_consumers VALUES($1,$2,1,4,'paused',NOW())"
                ),
                &[&failed_id, &workspace],
            )
            .await
            .unwrap();
        client.execute("INSERT INTO mail.mail_outbox_records(event_id,zone_id,resource_id,job_topic,status) VALUES($1,$2,$3,'mail.consumer.upsert','PENDING')", &[&failed_event,&zone,&failed_id.to_string()]).await.unwrap();
        assert!(crate::results::mail::consumer::apply_upsert_result(
            &mut client,
            failed_event,
            "FAILED",
            0,
            Some("EXECUTION_FAILED"),
            Some("test")
        )
        .await
        .unwrap()
        .is_some());
        let count: i64 = client
            .query_one(
                &format!("SELECT count(*) FROM mail.{branch}_mail_consumers WHERE id=$1"),
                &[&failed_id],
            )
            .await
            .unwrap()
            .get(0);
        assert_eq!(count, 1, "create failure must preserve the resource record");
    }
}
