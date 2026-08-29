use crate::app::state::AppState;
use crate::config::Config;
use crate::infra::centrifugo::CentrifugoPublisher;
use crate::infra::redis::RedisAuthBus;
use crate::infra::scylla::{connect as connect_scylla, resolve_from_vault};
use crate::infra::vault::VaultClient;
use crate::observability::logger::Logger;
use crate::repo::{NotificationRepo, ScyllaNotificationStore, ScyllaTimelineStore, TimelineRepo};
use crate::service::{
    ActivityService, AppError, AuthVerifier, ConnectAuthorizer, JobNotificationService,
    NotificationService, RealtimePublisher,
};
use crate::transport::stream::{ActivityStreamConsumer, JobStreamConsumer};
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::watch;
use tokio::task::JoinHandle;

pub struct Runtime {
    state: Arc<AppState>,
    shutdown: watch::Sender<bool>,
    tasks: Vec<JoinHandle<()>>,
    shutdown_timeout: Duration,
}

impl Runtime {
    pub async fn build(config: &Config, vault: &VaultClient) -> Result<Self, AppError> {
        Logger::sys_info("app.bootstrap", "Initializing Notification Service runtime");
        let (shutdown, shutdown_rx) = watch::channel(false);

        let auth_bus = RedisAuthBus::connect(&config.redis, vault).await?;
        let publisher: Arc<dyn RealtimePublisher> =
            Arc::new(CentrifugoPublisher::new(&config.centrifugo)?);
        let mut scylla_config = config.scylla.clone();
        resolve_from_vault(vault, &mut scylla_config).await?;
        let scylla = connect_scylla(&scylla_config).await?;
        let timeline_store: Arc<dyn TimelineRepo> =
            Arc::new(ScyllaTimelineStore::new(scylla.clone(), &config.timeline)?);
        let notification_store: Arc<dyn NotificationRepo> =
            Arc::new(ScyllaNotificationStore::new(scylla, &config.timeline)?);
        let activities = Arc::new(ActivityService::new(
            timeline_store,
            config.timeline.max_page_size,
            config.timeline.max_month_scan,
        ));
        let inbox = Arc::new(NotificationService::new(
            notification_store,
            config.timeline.max_page_size,
            config.timeline.max_month_scan,
        ));
        let reply_router = auth_bus.spawn_reply_router(shutdown_rx.clone());
        let job_notifications = Arc::new(JobNotificationService::new(
            publisher,
            activities.clone(),
            inbox.clone(),
        ));
        let verifier: Arc<dyn AuthVerifier> = auth_bus.clone();
        let authorizer = Arc::new(ConnectAuthorizer::new(verifier));
        let redis_client = auth_bus.client();

        let job_consumer = JobStreamConsumer::new(
            redis_client.clone(),
            job_notifications,
            config.runtime.clone(),
            config.redis.connect_timeout,
            shutdown_rx,
        );
        let activity_consumer = ActivityStreamConsumer::new(
            auth_bus.client(),
            activities.clone(),
            config.runtime.clone(),
            config.redis.connect_timeout,
            shutdown.subscribe(),
        );

        let job_task = tokio::spawn(job_consumer.run());
        let activity_task = tokio::spawn(activity_consumer.run());
        let state = Arc::new(AppState {
            authorizer,
            activities,
            inbox,
        });

        Ok(Self {
            state,
            shutdown,
            tasks: vec![reply_router, job_task, activity_task],
            shutdown_timeout: config.runtime.shutdown_timeout,
        })
    }

    pub fn state(&self) -> Arc<AppState> {
        self.state.clone()
    }

    pub async fn shutdown(mut self) {
        Logger::sys_info(
            "app.shutdown",
            "Stopping Notification Service stream workers",
        );
        let _ = self.shutdown.send(true);
        let deadline = tokio::time::sleep(self.shutdown_timeout);
        tokio::pin!(deadline);

        for task in &mut self.tasks {
            tokio::select! {
                result = task => {
                    if let Err(error) = result {
                        Logger::sys_error(
                            "app.shutdown",
                            "A supervised Notification Service task exited with an error",
                            &error.to_string(),
                        );
                    }
                }
                _ = &mut deadline => {
                    Logger::sys_warn(
                        "app.shutdown",
                        "Shutdown deadline reached; aborting remaining stream workers",
                        "NOTIFICATION_SHUTDOWN_TIMEOUT",
                    );
                    break;
                }
            }
        }
        for task in &self.tasks {
            task.abort();
        }
    }
}
