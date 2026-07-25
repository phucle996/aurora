use super::environment::{ConfigError, Environment};

#[derive(Clone, Debug)]
pub struct TimelineConfig {
    pub activity_retention_days: u64,
    pub inbox_retention_days: u64,
    pub max_page_size: usize,
    pub max_month_scan: usize,
}

impl TimelineConfig {
    pub fn from_env(environment: &Environment) -> Result<Self, ConfigError> {
        Ok(Self {
            activity_retention_days: environment.bounded_u64(
                "TIMELINE_ACTIVITY_RETENTION_DAYS",
                365,
                30,
                2_555,
            )?,
            inbox_retention_days: environment.bounded_u64(
                "TIMELINE_INBOX_RETENTION_DAYS",
                180,
                7,
                730,
            )?,
            max_page_size: environment.bounded_usize("TIMELINE_MAX_PAGE_SIZE", 50, 10, 100)?,
            max_month_scan: environment.bounded_usize("TIMELINE_MAX_MONTH_SCAN", 24, 1, 84)?,
        })
    }
}
