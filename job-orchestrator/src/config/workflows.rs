use super::environment::{validate_identifier, Environment};

#[derive(Clone)]
pub struct ChangefeedConfig {
    pub slot_name: String,
    pub publication_name: String,
    pub sources: Vec<String>,
    pub setup_attempts: u32,
    pub status_interval_ms: u64,
    pub idle_wakeup_secs: u64,
    pub buffer_events: usize,
    pub shutdown_grace_ms: u64,
}

#[derive(Clone)]
pub struct MailWorkflowConfig {
    pub reconcile_interval_secs: u64,
    pub reconcile_scheduler_tick_secs: u64,
    pub reconcile_jitter_max_ms: u64,
    pub reconcile_lock_ttl_secs: u64,
    pub reconcile_page_size: i64,
    pub reconcile_max_pages_per_run: usize,
    pub reconcile_work_budget_secs: u64,
    pub runtime_report_ttl_secs: u64,
    pub runtime_report_claim_idle_ms: u64,
}

#[derive(Clone)]
pub struct OwnershipWorkflowConfig {
    pub reconcile_interval_secs: u64,
    pub reconcile_batch_size: i64,
    pub lease_secs: u64,
    pub stream_capacity: usize,
}

#[derive(Clone)]
pub struct WorkflowConfig {
    pub changefeed: ChangefeedConfig,
    pub mail: MailWorkflowConfig,
    pub ownership: OwnershipWorkflowConfig,
}

impl WorkflowConfig {
    pub(crate) fn load(environment: &Environment) -> Result<Self, String> {
        let slot_name = environment.required("REPLICATION_SLOT_NAME")?;
        let publication_name = environment.required("PUBLICATION_NAME")?;
        validate_identifier("REPLICATION_SLOT_NAME", &slot_name)?;
        validate_identifier("PUBLICATION_NAME", &publication_name)?;

        let sources = environment
            .required("CDC_SOURCES")?
            .split(',')
            .map(str::trim)
            .filter(|source| !source.is_empty())
            .map(str::to_owned)
            .collect::<Vec<_>>();
        if sources.is_empty() || sources.iter().any(|source| !valid_source(source)) {
            return Err(
                "CDC_SOURCES must contain one or more comma-separated schema.table values"
                    .to_owned(),
            );
        }

        let reconcile_lock_ttl_secs =
            environment.bounded("MAIL_RECONCILE_LOCK_TTL_SECS", 60_u64, 30, 600)?;
        let reconcile_work_budget_secs =
            environment.bounded("MAIL_RECONCILE_WORK_BUDGET_SECS", 20_u64, 5, 595)?;
        if reconcile_work_budget_secs >= reconcile_lock_ttl_secs {
            return Err(
                "MAIL_RECONCILE_WORK_BUDGET_SECS must be lower than MAIL_RECONCILE_LOCK_TTL_SECS"
                    .to_owned(),
            );
        }

        Ok(Self {
            changefeed: ChangefeedConfig {
                slot_name,
                publication_name,
                sources,
                setup_attempts: environment.bounded("CHANGEFEED_SETUP_ATTEMPTS", 10_u32, 1, 100)?,
                status_interval_ms: environment.bounded(
                    "CHANGEFEED_STATUS_INTERVAL_MS",
                    1_000_u64,
                    100,
                    60_000,
                )?,
                idle_wakeup_secs: environment.bounded(
                    "CHANGEFEED_IDLE_WAKEUP_SECS",
                    10_u64,
                    1,
                    300,
                )?,
                buffer_events: environment.bounded(
                    "CHANGEFEED_BUFFER_EVENTS",
                    128_usize,
                    8,
                    256,
                )?,
                shutdown_grace_ms: environment.bounded(
                    "CHANGEFEED_SHUTDOWN_GRACE_MS",
                    5_000_u64,
                    100,
                    30_000,
                )?,
            },
            mail: MailWorkflowConfig {
                reconcile_interval_secs: environment.bounded(
                    "MAIL_RECONCILE_INTERVAL_SECS",
                    600_u64,
                    60,
                    86_400,
                )?,
                reconcile_scheduler_tick_secs: environment.bounded(
                    "MAIL_RECONCILE_SCHEDULER_TICK_SECS",
                    5_u64,
                    2,
                    60,
                )?,
                reconcile_jitter_max_ms: environment.bounded(
                    "MAIL_RECONCILE_JITTER_MAX_MS",
                    30_000_u64,
                    1_000,
                    300_000,
                )?,
                reconcile_lock_ttl_secs,
                reconcile_page_size: environment.bounded(
                    "MAIL_RECONCILE_PAGE_SIZE",
                    100_i64,
                    10,
                    500,
                )?,
                reconcile_max_pages_per_run: environment.bounded(
                    "MAIL_RECONCILE_MAX_PAGES_PER_RUN",
                    4_usize,
                    1,
                    32,
                )?,
                reconcile_work_budget_secs,
                runtime_report_ttl_secs: environment.bounded(
                    "MAIL_RUNTIME_REPORT_TTL_SECS",
                    45_u64,
                    30,
                    300,
                )?,
                runtime_report_claim_idle_ms: environment.bounded(
                    "MAIL_RUNTIME_REPORT_CLAIM_IDLE_MS",
                    30_000_u64,
                    5_000,
                    300_000,
                )?,
            },
            ownership: OwnershipWorkflowConfig {
                reconcile_interval_secs: environment.bounded(
                    "OWNERSHIP_RECONCILE_INTERVAL_SECS",
                    30_u64,
                    5,
                    300,
                )?,
                reconcile_batch_size: environment.bounded(
                    "OWNERSHIP_RECONCILE_BATCH_SIZE",
                    50_i64,
                    1,
                    500,
                )?,
                lease_secs: environment.bounded("OWNERSHIP_LEASE_SECS", 30_u64, 10, 300)?,
                stream_capacity: environment.bounded(
                    "OWNERSHIP_STREAM_CAPACITY",
                    100_000_usize,
                    1_000,
                    10_000_000,
                )?,
            },
        })
    }
}

fn valid_source(source: &str) -> bool {
    let Some((schema, table)) = source.split_once('.') else {
        return false;
    };
    !schema.is_empty()
        && !table.is_empty()
        && !table.contains('.')
        && source.len() <= 128
        && source
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'.'))
}
