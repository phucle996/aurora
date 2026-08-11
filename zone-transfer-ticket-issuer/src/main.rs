mod app;
mod config;
mod store;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();
    let config = config::Config::from_env()?;
    let store = store::TicketStore::connect(&config).await?;
    app::run(config, store).await
}
