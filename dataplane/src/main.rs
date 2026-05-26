use std::error::Error;

mod app;
mod bootstrap;
mod config;
mod executor;
mod infra;
mod job_receiver;
mod observability;
mod policyengine;
mod rpc;
mod workerpool;

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    // 1. Run bootstrap actions to initialize infrastructure & resources
    let boot_result = bootstrap::run_actions()?;

    // 2. Build the Application Module Graph container
    let (app, worker_signal_rx) = app::AppContainer::new(boot_result);

    // 3. Start the application background services & workers
    app.start(worker_signal_rx).await;

    // 4. Block and listen for OS system shutdown signals (Ctrl+C / SIGINT)
    tokio::signal::ctrl_c().await?;

    // 5. Gracefully shutdown the container & release resources
    app.stop();
    println!("Shutdown process completed. Exiting Dataplane process safely.");

    Ok(())
}
