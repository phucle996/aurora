use super::*;
use crate::executor::mail::runtime_proto::MailConsumerRuntimeGenerationV1;
use crate::job_runtime::test::validated_job;

async fn fixture(phase: u32) -> (Arc<ZoneKvStore>, Arc<ValidatedJob>, String, String, u64) {
    let store = ZoneKvStore::for_test().await;
    let id = uuid::Uuid::new_v4();
    let command = MailConsumerDrainV1 {
        schema_version: 1,
        consumer_id: id.as_bytes().to_vec(),
        config_version: 7,
        parallelism: 1,
        timeout_seconds: 1,
    };
    let job = validated_job(
        "MAIL",
        "mail.consumer.drain",
        &id.to_string(),
        &command.encode_to_vec(),
    );
    let generation = uuid::Uuid::new_v4().to_string();
    let head_key = format!("mail.consumer.head.{id}");
    let journal_key = format!("mail.consumer.runtime.{id}.{generation}");
    // Current PAUSED v7 still owes settlement of runtime v6.
    let head = ConsumerConfigHead {
        schema_version: 1,
        runtime_read_enabled: true,
        module: "mail".into(),
        resource_type: "consumer".into(),
        resource_id: id.to_string(),
        version: 7,
        event_id: job.job_id.clone(),
        config_sha256: "00".repeat(32),
        desired_state: "PAUSED".into(),
        tombstoned: false,
        owner_id: uuid::Uuid::new_v4().to_string(),
        owner_type: "PERSONAL".into(),
        workspace_id: uuid::Uuid::new_v4().to_string(),
        zone_id: job.target_zone_id.clone(),
        runtime_protocol: 1,
        runtime_generations: vec![generation.clone()],
    };
    store
        .config_create(&head_key, serde_json::to_vec(&head).unwrap().into())
        .await
        .unwrap();
    let journal = MailConsumerRuntimeGenerationV1 {
        schema_version: 1,
        consumer_id: id.to_string(),
        generation_id: generation,
        config_version: 6,
        slot: 0,
        fencing_token: 3,
        lease_owner_id: "old-pod".into(),
        phase,
    };
    let revision = store
        .config_create(&journal_key, journal.encode_to_vec().into())
        .await
        .unwrap();
    (store, job, head_key, journal_key, revision)
}

#[tokio::test]
#[ignore = "requires dedicated AURORA_TEST_NATS"]
async fn paused_new_version_drains_settled_old_generation() {
    let (store, job, head_key, _, _) = fixture(3).await;
    let result = apply_mail_consumer_drain(job, store.clone()).await.unwrap();
    let receipt = MailConsumerDrainedV1::decode(result.result_payload.as_slice()).unwrap();
    assert_eq!(receipt.config_version, 7);
    let head: ConsumerConfigHead =
        serde_json::from_slice(&store.config_get(head_key).await.unwrap().unwrap()).unwrap();
    assert!(head.runtime_generations.is_empty());
    assert_eq!(head.desired_state, "DRAINING");
}

#[tokio::test]
#[ignore = "requires dedicated AURORA_TEST_NATS"]
async fn prepared_generation_is_fenced_before_late_broker_start() {
    let (store, job, _, key, old_revision) = fixture(1).await;
    apply_mail_consumer_drain(job, store.clone()).await.unwrap();
    let mut late =
        MailConsumerRuntimeGenerationV1::decode(store.config_get(&key).await.unwrap().unwrap())
            .unwrap();
    assert_eq!(late.phase, 3);
    late.phase = 2;
    assert!(store
        .config_update(&key, late.encode_to_vec().into(), old_revision)
        .await
        .is_err());
}

#[tokio::test]
#[ignore = "requires dedicated AURORA_TEST_NATS"]
async fn expired_lease_and_empty_replacement_do_not_discharge_crashed_owner() {
    let (store, job, head_key, _, _) = fixture(2).await;
    let lease_key = format!("mail.consumer.slot.{}.0", job.resource_id);
    store
        .acquire_lease(&lease_key, "old-pod", Duration::from_millis(20))
        .await
        .unwrap()
        .unwrap();
    tokio::time::sleep(Duration::from_millis(250)).await;
    let replacement = store
        .acquire_lease(&lease_key, "replacement", Duration::from_secs(1))
        .await
        .unwrap()
        .unwrap();
    store.release_lease(&replacement).await.unwrap();
    // A receipt for the empty replacement previously incorrectly discharged A.
    store
        .config_create(
            format!("mail.consumer.drained.{}.7.0", job.resource_id),
            Bytes::copy_from_slice(&replacement.fencing_token.to_be_bytes()),
        )
        .await
        .unwrap();
    assert!(matches!(
        apply_mail_consumer_drain(job.clone(), store.clone()).await,
        Err(ExecutorError::OutcomeUnknown(_))
    ));
    let head: ConsumerConfigHead =
        serde_json::from_slice(&store.config_get(head_key).await.unwrap().unwrap()).unwrap();
    assert_eq!(head.runtime_generations.len(), 1);
    assert!(store
        .config_get(format!("mail.consumer.drain.result.{}.7", job.resource_id))
        .await
        .unwrap()
        .is_none());
}

#[tokio::test]
#[ignore = "requires dedicated AURORA_TEST_NATS"]
async fn legacy_head_without_runtime_evidence_never_becomes_drained() {
    let (store, job, head_key, _, _) = fixture(3).await;
    let entry = store.config_entry(&head_key).await.unwrap().unwrap();
    let mut head: ConsumerConfigHead = serde_json::from_slice(&entry.value).unwrap();
    head.runtime_protocol = 0;
    store
        .config_update(
            &head_key,
            serde_json::to_vec(&head).unwrap().into(),
            entry.revision,
        )
        .await
        .unwrap();
    assert!(matches!(
        apply_mail_consumer_drain(job, store).await,
        Err(ExecutorError::OutcomeUnknown(_))
    ));
}
