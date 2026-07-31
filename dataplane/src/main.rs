use std::error::Error;

mod app;
mod bootstrap;
mod config;
mod executor;
mod infra;
mod job_runtime;
mod leader;
mod observability;
mod security;
mod workerpool;

use crate::observability::logger::Logger;

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    // Load deployment logging controls before installing the only global tracing subscriber.
    dotenvy::dotenv().ok();

    // [COMMENT]: Thiết lập mặc định múi giờ TZ là Asia/Ho_Chi_Minh nếu chưa được định nghĩa
    if std::env::var("TZ").is_err() {
        std::env::set_var("TZ", "Asia/Ho_Chi_Minh");
    }
    let _logger_guard = Logger::init();

    // 1. Run bootstrap actions to initialize infrastructure & resources
    let boot_result = match bootstrap::run_actions().await {
        Ok(result) => result,
        Err(error) => {
            Logger::sys_error_with_fields(
                "system.bootstrap",
                "DATAPLANE_BOOTSTRAP_FAILED",
                "Dataplane bootstrap failed; process will exit before accepting workload",
                &error.to_string(),
                Default::default(),
            );
            return Err(error);
        }
    };

    // 2. Build the Application Module Graph container
    let app = app::AppContainer::new(boot_result);

    // 3. Start the application background services & workers
    app.start().await;

    // 4. Block on OS shutdown signals (SIGINT hoặc SIGTERM).
    //    - SIGINT  (Ctrl+C) → developer dừng thủ công khi dev local.
    //    - SIGTERM → Docker/K8s gửi khi thực hiện `docker stop` hoặc rolling restart.
    //    Sử dụng tokio::signal::unix để handle đúng cả 2 signal, tránh trường hợp
    //    ctrl_c() resolve sớm do cargo-watch forward SIGINT xuống child trong container.
    use tokio::signal::unix::{signal, SignalKind};
    let mut sigint = signal(SignalKind::interrupt())?;
    let mut sigterm = signal(SignalKind::terminate())?;
    let fatal_shutdown = app.fatal_shutdown_token();

    let fatal_exit = tokio::select! {
        _ = sigint.recv()  => {
            Logger::sys_info("system.signal", "Received SIGINT. Initiating graceful shutdown...");
            false
        }
        _ = sigterm.recv() => {
            Logger::sys_info("system.signal", "Received SIGTERM. Initiating graceful shutdown...");
            false
        }
        _ = fatal_shutdown.cancelled() => {
            Logger::sys_error(
                "system.signal",
                "A critical runtime task exited; initiating fail-safe shutdown",
                "DATAPLANE_CRITICAL_TASK_EXITED",
            );
            true
        }
    };

    // 5. Gracefully shutdown the container & release resources
    if fatal_exit {
        app.fence_jobs_for_process_restart();
    }
    app.stop().await;
    Logger::sys_info(
        "system.shutdown",
        "Shutdown process completed. Exiting Dataplane process safely.",
    );

    if fatal_exit {
        return Err(std::io::Error::other("critical Dataplane task exited").into());
    }

    Ok(())
}
