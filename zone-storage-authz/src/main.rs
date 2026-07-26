mod access_store;
mod app;
mod assertion;
mod canonical;
mod check;
mod config;
mod error;
mod keys;
mod metrics;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();
    let config = config::Config::from_env()?;
    app::run(config).await?;
    Ok(())
}
