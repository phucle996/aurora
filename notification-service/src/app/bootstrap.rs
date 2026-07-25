use crate::app::state::AppState;
use crate::application::auth::ConnectAuthorizer;
use crate::application::job_notifications::JobNotificationService;
use crate::application::ports::{AppError, AuthVerifier, RealtimePublisher};
use crate::application::runtime_updates::RuntimeUpdateService;
use crate::config::Config;
use crate::inbound::job_stream::JobStreamConsumer;
use crate::inbound::realtime_pubsub::RealtimePubSubConsumer;
use crate::infra::centrifugo::CentrifugoPublisher;
use crate::infra::redis::RedisAuthBus;
use crate::observability::logger::Logger;
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
    pub async fn build(config: &Config) -> Result<Self, AppError> {
        Logger::sys_info("app.bootstrap", "Initializing Notification Service runtime");
        let (shutdown, shutdown_rx) = watch::channel(false);

        let auth_bus = RedisAuthBus::connect(&config.redis).await?;
        let publisher: Arc<dyn RealtimePublisher> =
            Arc::new(CentrifugoPublisher::new(&config.centrifugo)?);
        let reply_router = auth_bus.spawn_reply_router(shutdown_rx.clone());
        let job_notifications = Arc::new(JobNotificationService::new(publisher.clone()));
        let runtime_updates = Arc::new(RuntimeUpdateService::new(publisher));
        let verifier: Arc<dyn AuthVerifier> = auth_bus.clone();
        let authorizer = Arc::new(ConnectAuthorizer::new(verifier));
        let redis_client = auth_bus.client();

        let job_consumer = JobStreamConsumer::new(
            redis_client.clone(),
            job_notifications,
            config.runtime.clone(),
            config.redis.connect_timeout,
            shutdown_rx.clone(),
        );
        let realtime_consumer = RealtimePubSubConsumer::new(
            redis_client,
            runtime_updates,
            config.runtime.clone(),
            config.redis.connect_timeout,
            shutdown_rx,
        );

        let job_task = tokio::spawn(job_consumer.run());
        let realtime_task = tokio::spawn(realtime_consumer.run());
        let state = Arc::new(AppState { authorizer });

        Ok(Self {
            state,
            shutdown,
            tasks: vec![reply_router, job_task, realtime_task],
            shutdown_timeout: config.runtime.shutdown_timeout,
        })
    }

    pub fn state(&self) -> Arc<AppState> {
        self.state.clone()
    }

    pub async fn shutdown(mut self) {
        Logger::sys_info(
            "app.shutdown",
            "Stopping Notification Service inbound workers",
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
                        "Shutdown deadline reached; aborting remaining inbound workers",
                        "NOTIFICATION_SHUTDOWN_TIMEOUT",
                    );
                    break;
                }
            }
        }
        for task in &self.tasks {
            if !task.is_finished() {
                task.abort();
            }
        }
    }
}
