use super::*;

impl ZoneKvStore {
    pub(crate) async fn for_test() -> Arc<Self> {
        let client = async_nats::connect(
            std::env::var("AURORA_TEST_NATS").expect("dedicated NATS test server"),
        )
        .await
        .unwrap();
        let js = jetstream::new(client);
        let prefix = uuid::Uuid::new_v4().simple().to_string();
        let mut stores = Vec::new();
        for suffix in [
            "config",
            "completion",
            "health",
            "access",
            "admission",
            "coordination",
        ] {
            stores.push(
                js.create_key_value(kv::Config {
                    bucket: format!("test_{prefix}_{suffix}"),
                    history: 1,
                    max_age: if suffix == "coordination" {
                        Duration::from_millis(100)
                    } else {
                        Duration::ZERO
                    },
                    storage: StorageType::File,
                    num_replicas: 1,
                    ..Default::default()
                })
                .await
                .unwrap(),
            );
        }
        let mut stores = stores.into_iter();
        Arc::new(Self {
            config: stores.next().unwrap(),
            completion: stores.next().unwrap(),
            health: stores.next().unwrap(),
            access: stores.next().unwrap(),
            admission: stores.next().unwrap(),
            coordination: stores.next().unwrap(),
        })
    }
}
