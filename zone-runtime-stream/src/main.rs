mod config;
mod contract;
mod http;
mod mail;
mod source;
mod stream;
mod telemetry;

use tokio_util::sync::CancellationToken;
use tracing::info;

use crate::config::Config;
use crate::source::VictoriaSource;
use crate::stream::RuntimeStream;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();

    let config = Config::from_env()?;
    let shutdown = CancellationToken::new();
    let source = VictoriaSource::new(&config)?;
    let runtime = RuntimeStream::new(config.clone(), source, shutdown.clone());
    let app = http::router(runtime.clone());

    let listener = tokio::net::TcpListener::bind(config.listen_addr).await?;
    info!(
        event_code = "ZONE_RUNTIME_STREAM_STARTED",
        address = %config.listen_addr,
        zone_id = %config.zone_id,
        "zone runtime stream listening"
    );

    let signal_shutdown = shutdown.clone();
    let serve_result = axum::serve(listener, app)
        .with_graceful_shutdown(async move {
            shutdown_signal().await;
            signal_shutdown.cancel();
        })
        .await;

    runtime.shutdown().await;
    info!(
        event_code = "ZONE_RUNTIME_STREAM_STOPPED",
        "zone runtime stream stopped"
    );
    serve_result?;
    Ok(())
}

async fn shutdown_signal() {
    let ctrl_c = async {
        if let Err(error) = tokio::signal::ctrl_c().await {
            tracing::error!(event_code = "ZONE_RUNTIME_STREAM_SIGNAL_ERROR", error = %error);
        }
    };

    #[cfg(unix)]
    let terminate = async {
        match tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
            Ok(mut signal) => {
                signal.recv().await;
            }
            Err(error) => {
                tracing::error!(event_code = "ZONE_RUNTIME_STREAM_SIGNAL_ERROR", error = %error);
            }
        }
    };

    #[cfg(not(unix))]
    let terminate = std::future::pending::<()>();

    tokio::select! {
        () = ctrl_c => {}
        () = terminate => {}
    }
}
