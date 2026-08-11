use std::error::Error;

mod orchestrator;
mod transfer_ticket;

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error + Send + Sync>> {
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();
    let config = transfer_ticket::config::Config::from_env()?;
    let store = transfer_ticket::store::TicketStore::connect(&config).await?;
    let shutdown = tokio_util::sync::CancellationToken::new();
    orchestrator::start(config.clone(), shutdown.clone());
    transfer_ticket::app::run(config, store, shutdown).await?;
    Ok(())
}
