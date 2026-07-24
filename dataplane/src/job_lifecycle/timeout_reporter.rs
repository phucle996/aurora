use std::sync::Arc;

use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use crate::infra::kafka::{KafkaDelivery, KafkaTransport};
use crate::job_lifecycle::result::{JobExecutionResult, JobResultReporter};
use crate::observability::logger::Logger;

/// Durable-result boundary for executions aborted by the lease watchdog.
///
/// Keeping this outside `workerpool` prevents lease renewal from depending on
/// Kafka result latency. If reporting fails, the source offset is deliberately
/// left unsettled so at-least-once replay remains possible.
pub struct ExecutionTimeoutReport {
    pub result: JobExecutionResult,
    pub kafka_delivery: Option<KafkaDelivery>,
}

pub async fn run_execution_timeout_reporter(
    mut reports: mpsc::Receiver<ExecutionTimeoutReport>,
    kafka: Arc<KafkaTransport>,
    shutdown: CancellationToken,
) {
    loop {
        tokio::select! {
            report = reports.recv() => {
                let Some(report) = report else {
                    return;
                };
                publish_and_settle(&kafka, report).await;
            }
            _ = shutdown.cancelled() => {
                // Stop new queue admissions and drain reports already accepted
                // before the tracked task exits the shutdown barrier.
                reports.close();
                while let Some(report) = reports.recv().await {
                    publish_and_settle(&kafka, report).await;
                }
                return;
            }
        }
    }
}

async fn publish_and_settle(kafka: &KafkaTransport, report: ExecutionTimeoutReport) {
    match JobResultReporter::report_outcome(kafka, &report.result).await {
        Ok(()) => {
            if let Some(delivery) = report.kafka_delivery {
                if let Err(error) = delivery.settle().await {
                    Logger::sys_warn(
                        "job.execution_timeout_reporter",
                        &format!("Timeout result is durable but source settlement failed: {error}"),
                        "EXECUTION_TIMEOUT_SETTLEMENT_FAILED",
                    );
                }
            }
        }
        Err(error) => Logger::sys_error(
            "job.execution_timeout_reporter",
            &format!("Timeout result is not durable; source offset remains unsettled: {error}"),
            "EXECUTION_TIMEOUT_RESULT_NOT_DURABLE",
        ),
    }
}
