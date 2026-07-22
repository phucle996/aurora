pub(crate) mod configuration;
mod consumer_supervisor;
pub(crate) mod context;
mod dispatcher;
mod kafka;
mod nats_jetstream;
mod rabbitmq;
mod redis_stream;

pub use configuration::MailConfigurationRuntime;
pub use consumer_supervisor::MailConsumerSupervisor;
pub(crate) use context::RuntimeHealthSnapshot;
pub(super) use context::{RuntimeGenerationFence, StreamRuntimeContext};
