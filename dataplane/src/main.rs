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

use crate::observability::logger::Logger;

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    // 1. Run bootstrap actions to initialize infrastructure & resources
    let boot_result = bootstrap::run_actions()?;

    // 2. Build the Application Module Graph container
    let (app, worker_signal_rx) = app::AppContainer::new(boot_result);

    // 3. Start the application background services & workers
    app.start(worker_signal_rx).await;

    // 4. Block on OS shutdown signals (SIGINT hoặc SIGTERM).
    //    - SIGINT  (Ctrl+C) → developer dừng thủ công khi dev local.
    //    - SIGTERM → Docker/K8s gửi khi thực hiện `docker stop` hoặc rolling restart.
    //    Sử dụng tokio::signal::unix để handle đúng cả 2 signal, tránh trường hợp
    //    ctrl_c() resolve sớm do cargo-watch forward SIGINT xuống child trong container.
    use tokio::signal::unix::{signal, SignalKind};
    let mut sigint = signal(SignalKind::interrupt())?;
    let mut sigterm = signal(SignalKind::terminate())?;

    tokio::select! {
        _ = sigint.recv()  => { Logger::sys_info("system.signal", "Received SIGINT. Initiating graceful shutdown..."); }
        _ = sigterm.recv() => { Logger::sys_info("system.signal", "Received SIGTERM. Initiating graceful shutdown..."); }
    }

    // 5. Gracefully shutdown the container & release resources
    app.stop().await;
    Logger::sys_info("system.shutdown", "Shutdown process completed. Exiting Dataplane process safely.");

    Ok(())
}
