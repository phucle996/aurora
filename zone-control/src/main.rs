use std::error::Error;

mod admission;
mod metering;
mod orchestrator;
mod storage_report_relay;
mod transfer_ticket;
mod zone_control_kafka;
mod zone_control_state;
mod zone_health;
mod zone_metadata;
mod zone_scaling;
mod zone_storage;

pub mod storage_usage_report_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.storage.metering.v1.rs"));
}

pub mod transport_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.transport.v1.rs"));
}

pub mod zone_report_proto {
    include!(concat!(env!("OUT_DIR"), "/zone.rs"));
}

pub mod storage_admission_proto {
    include!(concat!(env!("OUT_DIR"), "/controlplane.storage.v1.rs"));
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error + Send + Sync>> {
    let _ = rustls::crypto::ring::default_provider().install_default();

    tracing_subscriber::fmt()
        .json()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();
    let config = transfer_ticket::config::Config::from_env()?;
    let store = transfer_ticket::store::TicketStore::connect(&config).await?;
    let shutdown = tokio_util::sync::CancellationToken::new();
    if !config.orchestrator_enabled {
        return Err(
            "ZONE_CONTROL_ORCHESTRATOR_ENABLED must be true after the Gate B cutover; legacy Zone-wide control is removed".into(),
        );
    }
    orchestrator::start(config.clone(), shutdown.clone());
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
