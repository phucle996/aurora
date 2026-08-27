use super::*;

fn consumer_event(version: u64, hash_seed: u8) -> MailConsumerUpsertV1 {
    let mut event = MailConsumerUpsertV1 {
        metadata: None,
        consumer_id: [1_u8; 16].to_vec(),
        config_version: version,
        stream: Some(MailStreamSourceV1 {
            stream_type: MailStreamType::Kafka as i32,
            payload_schema_version: 1,
            broker_resource_id: [2_u8; 16].to_vec(),
            payload: KafkaStreamPayloadV1 {
                source_config_envelope: vec![3_u8; 64],
                topic: "orders.created".to_string(),
                consumer_group: "mailer".to_string(),
            }
            .encode_to_vec(),
        }),
        template_id: "template-a".to_string(),
        template_version: 3,
        sender_profile_id: "sender-a".to_string(),
        sender_version: 2,
        desired_state: 2,
        parallelism: 4,
        config_sha256: vec![hash_seed; 32],
        owner_id: [4_u8; 16].to_vec(),
        owner_type: "PERSONAL".to_string(),
        workspace_id: [5_u8; 16].to_vec(),
        zone_id: [6_u8; 16].to_vec(),
    };
    event.config_sha256 = canonical_consumer_sha256(&event).to_vec();
    event
}

fn test_runtime(max_entries: usize) -> MailConfigurationRuntime {
    MailConfigurationRuntime {
        zone_id: uuid::Uuid::from_bytes([6_u8; 16]).to_string(),
        instance_id: "pod-a".to_string(),
        scan_interval: Duration::from_secs(60),
        scan_page_size: 16,
        scan_max_pages_per_tick: 1,
        max_consumer_entries: max_entries,
        max_template_bytes: 1_048_576,
        consumers: ArcSwap::from_pointee(HashMap::new()),
        templates: Cache::builder().max_capacity(1_048_576).build(),
        apply_lock: tokio::sync::Mutex::new(()),
        scan_cursor: AtomicU64::new(0),
        cancel: CancellationToken::new(),
        task: Mutex::new(None),
        zone_kv: None,
    }
}

fn configuration(id: &str, version: u64, hash: u8) -> Arc<RuntimeConsumerConfiguration> {
    Arc::new(RuntimeConsumerConfiguration {
        consumer_id: id.to_string(),
        owner_id: uuid::Uuid::from_bytes([4_u8; 16]).to_string(),
        owner_type: "PERSONAL".to_string(),
        workspace_id: uuid::Uuid::from_bytes([5_u8; 16]).to_string(),
        zone_id: uuid::Uuid::from_bytes([6_u8; 16]).to_string(),
        config_version: version,
        config_sha256: [hash; 32],
        stream: RuntimeStreamSource {
            stream_type: MailStreamType::Kafka,
            payload_schema_version: 1,
            broker_resource_id: [1; 16],
            payload: KafkaStreamPayloadV1 {
                source_config_envelope: vec![3; 64],
                topic: "topic".to_string(),
                consumer_group: "group".to_string(),
            }
            .encode_to_vec(),
        },
        template_id: uuid::Uuid::nil().to_string(),
        template_version: 1,
        sender_profile_id: "sender-a".to_string(),
        sender_version: 1,
        desired_state: RuntimeDesiredState::Enabled,
        parallelism: 1,
    })
}

#[test]
fn canonical_consumer_hash_ignores_event_metadata_and_hash_field() {
    let mut first = consumer_event(8, 1);
    let expected = canonical_consumer_sha256(&first);
    first.config_sha256 = vec![9; 32];
    first.metadata = Some(crate::executor::mail::runtime_proto::MailEventMetadataV1 {
        event_id: [7; 16].to_vec(),
        schema_version: 1,
        occurred_at_unix_ms: 123,
        traceparent: "trace".to_string(),
        producer: "test".to_string(),
    });
    assert_eq!(canonical_consumer_sha256(&first), expected);
}

#[test]
fn enabled_consumer_requires_bounded_encrypted_source_envelope() {
    let runtime = test_runtime(10);
    let mut event = consumer_event(1, 1);
    event.template_id = uuid::Uuid::new_v4().to_string();
    event.metadata = Some(crate::executor::mail::runtime_proto::MailEventMetadataV1 {
        event_id: [7; 16].to_vec(),
        schema_version: 1,
        occurred_at_unix_ms: 123,
        traceparent: String::new(),
        producer: "test".to_string(),
    });
    let stream = event.stream.as_mut().expect("stream fixture");
    let mut kafka =
        KafkaStreamPayloadV1::decode(stream.payload.as_slice()).expect("Kafka fixture payload");
    kafka.source_config_envelope.clear();
    stream.payload = kafka.encode_to_vec();
    let stream = event.stream.as_ref().expect("stream fixture");
    assert!(runtime
        .validate_consumer_contract(1, &event, stream)
        .is_err());

    // [COMMENT]: PAUSED config được phép chưa có credential để người dùng cấu hình rồi mới resume.
    event.desired_state = 1;
    let stream = event.stream.as_ref().expect("stream fixture");
    assert!(runtime
        .validate_consumer_contract(1, &event, stream)
        .is_ok());
}

#[test]
fn stream_discriminator_requires_the_matching_suite_payload() {
    let runtime = test_runtime(10);
    let mut event = consumer_event(1, 1);
    event.template_id = uuid::Uuid::new_v4().to_string();
    event.metadata = Some(crate::executor::mail::runtime_proto::MailEventMetadataV1 {
        event_id: [7; 16].to_vec(),
        schema_version: 1,
        occurred_at_unix_ms: 123,
        traceparent: String::new(),
        producer: "test".to_string(),
    });
    let envelope = vec![3_u8; 64];
    let fixtures = [
        (
            MailStreamType::Kafka,
            KafkaStreamPayloadV1 {
                source_config_envelope: envelope.clone(),
                topic: "orders.created".to_string(),
                consumer_group: "mailer".to_string(),
            }
            .encode_to_vec(),
        ),
        (
            MailStreamType::RedisStream,
            RedisStreamPayloadV1 {
                source_config_envelope: envelope.clone(),
                stream_key: "orders".to_string(),
                consumer_group: "mailer".to_string(),
            }
            .encode_to_vec(),
        ),
        (
            MailStreamType::NatsJetstream,
            NatsJetStreamPayloadV1 {
                source_config_envelope: envelope.clone(),
                stream_name: "ORDERS".to_string(),
                durable_name: "mailer".to_string(),
            }
            .encode_to_vec(),
        ),
        (
            MailStreamType::Rabbitmq,
            RabbitMqPayloadV1 {
                source_config_envelope: envelope,
                queue_name: "orders.mail".to_string(),
                consumer_tag_prefix: "aurora-mailer".to_string(),
            }
            .encode_to_vec(),
        ),
    ];

    for (stream_type, payload) in fixtures {
        let stream = event.stream.as_mut().expect("stream fixture");
        stream.stream_type = stream_type as i32;
        stream.payload = payload;
        assert!(runtime
            .validate_consumer_contract(1, &event, event.stream.as_ref().expect("stream"))
            .is_ok());
    }

    // [COMMENT]: Outer discriminator không được decode nhầm bytes Kafka thành config Redis mặc định.
    let stream = event.stream.as_mut().expect("stream fixture");
    stream.stream_type = MailStreamType::RedisStream as i32;
    stream.payload = KafkaStreamPayloadV1 {
        source_config_envelope: vec![3; 64],
        topic: "orders.created".to_string(),
        consumer_group: "mailer".to_string(),
    }
    .encode_to_vec();
    assert!(runtime
        .validate_consumer_contract(1, &event, event.stream.as_ref().expect("stream"))
        .is_err());
}

#[test]
fn canonical_template_hash_matches_go_html_escaping_contract() {
    let mut hasher = Sha256::new();
    hasher.update("A < B & C".as_bytes());
    hasher.update([0x00]);
    hasher.update("<p>Hi</p>".as_bytes());
    let expected: [u8; 32] = hasher.finalize().into();
    assert_eq!(
        canonical_template_sha256("A < B & C", "<p>Hi</p>".as_bytes()),
        expected
    );
}

#[test]
fn registry_parser_rejects_tombstone_with_hash() {
    assert!(parse_registry_record("7|DELETED|deadbeef").is_err());
    let record = parse_registry_record("7|DELETED|").expect("valid tombstone");
    assert_eq!(record.config_version, 7);
    assert!(record.config_sha256.is_none());
}

#[tokio::test]
async fn cow_keeps_old_generation_readable_and_rejects_version_rollback() {
    let runtime = test_runtime(10);
    let consumer_id = uuid::Uuid::new_v4().to_string();
    runtime
        .apply_observation(LoadedConsumerObservation::Active(configuration(
            &consumer_id,
            7,
            7,
        )))
        .await
        .expect("apply v7");
    let old_generation = runtime.snapshot();
    runtime
        .apply_observation(LoadedConsumerObservation::Active(configuration(
            &consumer_id,
            8,
            8,
        )))
        .await
        .expect("apply v8");
    assert_eq!(old_generation[&consumer_id].config_version(), 7);
    assert_eq!(runtime.snapshot()[&consumer_id].config_version(), 8);
    assert_eq!(
        runtime
            .apply_observation(LoadedConsumerObservation::Active(configuration(
                &consumer_id,
                7,
                7,
            )))
            .await
            .expect("stale is a no-op"),
        CowApplyOutcome::Stale
    );
    assert_eq!(runtime.snapshot()[&consumer_id].config_version(), 8);
}

#[tokio::test]
async fn tombstone_fences_same_or_older_upsert() {
    let runtime = test_runtime(10);
    let consumer_id = uuid::Uuid::new_v4().to_string();
    runtime
        .apply_observation(LoadedConsumerObservation::Active(configuration(
            &consumer_id,
            6,
            6,
        )))
        .await
        .expect("apply v6");
    runtime
        .apply_observation(LoadedConsumerObservation::Tombstone {
            consumer_id: consumer_id.clone(),
            config_version: 7,
        })
        .await
        .expect("apply tombstone");
    assert!(matches!(
        runtime.snapshot().get(&consumer_id),
        Some(RuntimeConsumerEntry::Tombstone { config_version: 7 })
    ));
    assert_eq!(
        runtime
            .apply_observation(LoadedConsumerObservation::Active(configuration(
                &consumer_id,
                6,
                9,
            )))
            .await
            .expect("older upsert is stale"),
        CowApplyOutcome::Stale
    );
}

#[tokio::test]
async fn same_version_different_hash_fails_closed() {
    let runtime = test_runtime(10);
    let consumer_id = uuid::Uuid::new_v4().to_string();
    runtime
        .apply_observation(LoadedConsumerObservation::Active(configuration(
            &consumer_id,
            8,
            1,
        )))
        .await
        .expect("apply first hash");
    let error = runtime
        .apply_observation(LoadedConsumerObservation::Active(configuration(
            &consumer_id,
            8,
            2,
        )))
        .await
        .expect_err("same version different hash must fail");
    assert_eq!(error.code, "MAIL_CONFIG_L1_VERSION_CONFLICT");
    let RuntimeConsumerEntry::Active(current) = &runtime.snapshot()[&consumer_id] else {
        panic!("consumer must remain active")
    };
    assert_eq!(current.config_sha256, [1; 32]);
}
