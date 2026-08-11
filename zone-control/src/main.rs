use std::error::Error;

mod metering;
mod orchestrator;
mod transfer_ticket;

pub mod storage_usage_report_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.storage.metering.v1.rs"));
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error + Send + Sync>> {
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();
    let config = transfer_ticket::config::Config::from_env()?;
    let store = transfer_ticket::store::TicketStore::connect(&config).await?;
    let shutdown = tokio_util::sync::CancellationToken::new();
    if config.orchestrator_enabled {
        orchestrator::start(config.clone(), shutdown.clone());
    } else {
        tracing::info!(
            event_code = "ZONE_CONTROL_ORCHESTRATOR_DISABLED",
            reason = "distributed control scheduler is disabled during controlled extraction"
        );
    }
    if !config.metering_enabled {
        tracing::info!(
            event_code = "ZONE_STORAGE_METERING_DISABLED",
            reason =
                "report publisher remains opt-in until Zone reconciliation and Cost cutover gates"
        );
    } else if !config.orchestrator_enabled {
        return Err("ZONE_CONTROL_METERING_ENABLED requires ZONE_CONTROL_ORCHESTRATOR_ENABLED; the report publisher must run inside an assigned and fenced work unit".into());
    }
    transfer_ticket::app::run(config, store, shutdown).await?;
    Ok(())
}
