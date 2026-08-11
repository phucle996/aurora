mod app;
mod authorization;
mod config;
mod control_assertion;
mod error;
mod keys;
mod request_binding;
mod telemetry;
mod transfer_ticket;

pub mod transfer_proto {
    include!(concat!(env!("OUT_DIR"), "/aurora.zone.transfer.v1.rs"));
}
mod zone_access;

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
