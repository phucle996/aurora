use super::configuration::RuntimeConsumerConfiguration;
use super::context::{RuntimeGenerationFence, StreamRuntimeContext};
use super::{kafka, nats_jetstream, rabbitmq, redis_stream};
use crate::executor::mail::runtime_proto::MailStreamType;
use crate::infra::zone_kv::ZoneLease;
use std::sync::Arc;
use tokio_util::sync::CancellationToken;

/// [COMMENT]: Dispatcher chỉ match đúng một lần; sau branch này connect/consume/retry/settlement thuộc trọn bộ broker tương ứng.
pub async fn dispatch_stream_runtime(
    context: Arc<StreamRuntimeContext>,
    configuration: Arc<RuntimeConsumerConfiguration>,
    slot: u32,
    generation: u64,
    lease: ZoneLease,
    generation_fence: Arc<RuntimeGenerationFence>,
    cancel: CancellationToken,
) {
    match configuration.stream.stream_type {
        MailStreamType::Kafka => {
            kafka::run(
                context,
                configuration,
                slot,
                generation,
                lease,
                generation_fence,
                cancel,
            )
            .await;
        }
        MailStreamType::RedisStream => {
            redis_stream::run(
                context,
                configuration,
                slot,
                generation,
                lease,
                generation_fence,
                cancel,
            )
            .await;
        }
        MailStreamType::NatsJetstream => {
            nats_jetstream::run(
                context,
                configuration,
                slot,
                generation,
                lease,
                generation_fence,
                cancel,
            )
            .await;
        }
        MailStreamType::Rabbitmq => {
            rabbitmq::run(
                context,
                configuration,
                slot,
                generation,
                lease,
                generation_fence,
                cancel,
            )
            .await;
        }
        MailStreamType::Unspecified => {
            context
                .write_health(
                    "ERROR",
                    &configuration,
                    slot,
                    generation,
                    &lease,
                    "MAIL_STREAM_TYPE_UNSPECIFIED",
                )
                .await;
        }
    }
}
