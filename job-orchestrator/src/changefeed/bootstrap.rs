use crate::config::Config;
use crate::observability::logger::Logger;
use std::time::Duration;

#[derive(Debug)]
pub struct BootstrapError(pub String);

impl std::fmt::Display for BootstrapError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(formatter, "changefeed bootstrap failed: {}", self.0)
    }
}

impl std::error::Error for BootstrapError {}

/// Runtime owns no DDL credential. Publication, membership and replication slot
/// are provisioned by migrations/operators; every replica only verifies them.
pub async fn verify(config: &Config) -> Result<(), Box<dyn std::error::Error>> {
    let max_attempts = config.workflows.changefeed.setup_attempts.max(1);
    for attempt in 1..=max_attempts {
        match verify_once(config).await {
            Ok(()) => {
                Logger::sys_info(
                    "changefeed.bootstrap",
                    "Logical replication infrastructure verified",
                );
                return Ok(());
            }
            Err(error) if attempt == max_attempts || is_permanent(error.as_ref()) => {
                return Err(error);
            }
            Err(error) => {
                let backoff = (2_u64.pow(attempt.min(5))).min(30);
                // Stable per-process jitter prevents standby replicas from
                // reconnecting to PostgreSQL on the same second.
                let jitter = crate::config::get_node_hostname()
                    .bytes()
                    .fold(0_u64, |sum, value| sum.wrapping_add(u64::from(value)))
                    % 3;
                Logger::sys_warn(
                    "changefeed.bootstrap",
                    &format!(
                        "Verification attempt {attempt}/{max_attempts} failed; retrying in {}s",
                        backoff + jitter
                    ),
                    &error.to_string(),
                );
                tokio::time::sleep(Duration::from_secs(backoff + jitter)).await;
            }
        }
    }
    Err(Box::new(BootstrapError(
        "verification attempts exhausted".to_string(),
    )))
}

async fn verify_once(config: &Config) -> Result<(), Box<dyn std::error::Error>> {
    let client = crate::infra::postgres::connect(&config.postgres, "changefeed.postgres").await?;

    let publication_exists: bool = client
        .query_one(
            "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)",
            &[&config.workflows.changefeed.publication_name],
        )
        .await?
        .get(0);
    if !publication_exists {
        return Err(Box::new(BootstrapError(format!(
            "publication '{}' is not provisioned",
            config.workflows.changefeed.publication_name
        ))));
    }

    for source in &config.workflows.changefeed.sources {
        let (schema, table) = split_source(source)?;
        let exists: bool = client
            .query_one(
                "SELECT EXISTS ( \
                     SELECT 1 FROM information_schema.tables \
                     WHERE table_schema = $1 AND table_name = $2 \
                 )",
                &[&schema, &table],
            )
            .await?
            .get(0);
        if !exists {
            return Err(Box::new(BootstrapError(format!(
                "configured source {schema}.{table} does not exist"
            ))));
        }
        let published: bool = client
            .query_one(
                "SELECT EXISTS ( \
                     SELECT 1 FROM pg_publication_tables \
                     WHERE pubname = $1 AND schemaname = $2 AND tablename = $3 \
                 )",
                &[
                    &config.workflows.changefeed.publication_name,
                    &schema,
                    &table,
                ],
            )
            .await?
            .get(0);
        if !published {
            return Err(Box::new(BootstrapError(format!(
                "source {schema}.{table} is not a member of publication '{}'",
                config.workflows.changefeed.publication_name
            ))));
        }
    }

    let slot = client
        .query_opt(
            "SELECT plugin, slot_type, database \
             FROM pg_replication_slots WHERE slot_name = $1",
            &[&config.workflows.changefeed.slot_name],
        )
        .await?;
    let Some(slot) = slot else {
        return Err(Box::new(BootstrapError(format!(
            "replication slot '{}' is not provisioned",
            config.workflows.changefeed.slot_name
        ))));
    };
    let plugin: String = slot.get(0);
    let slot_type: String = slot.get(1);
    let database: Option<String> = slot.get(2);
    let current_database: String = client
        .query_one("SELECT current_database()", &[])
        .await?
        .get(0);
    if plugin != "pgoutput"
        || slot_type != "logical"
        || database.as_deref() != Some(current_database.as_str())
    {
        return Err(Box::new(BootstrapError(format!(
            "replication slot '{}' has an incompatible plugin/type/database",
            config.workflows.changefeed.slot_name
        ))));
    }
    Ok(())
}

fn split_source(source: &str) -> Result<(&str, &str), Box<dyn std::error::Error>> {
    let mut parts = source.split('.');
    let schema = parts.next().unwrap_or_default();
    let table = parts.next().unwrap_or_default();
    if schema.is_empty() || table.is_empty() || parts.next().is_some() {
        return Err(Box::new(BootstrapError(format!(
            "changefeed source '{source}' must use schema.table"
        ))));
    }
    if !schema.bytes().all(valid_identifier_byte) || !table.bytes().all(valid_identifier_byte) {
        return Err(Box::new(BootstrapError(format!(
            "changefeed source '{source}' contains an invalid identifier"
        ))));
    }
    Ok((schema, table))
}

fn valid_identifier_byte(value: u8) -> bool {
    value == b'_' || value.is_ascii_alphanumeric()
}

fn is_permanent(error: &(dyn std::error::Error + 'static)) -> bool {
    if error.is::<BootstrapError>() {
        return true;
    }
    error
        .downcast_ref::<tokio_postgres::Error>()
        .and_then(tokio_postgres::Error::as_db_error)
        .is_some_and(|db_error| {
            matches!(
                db_error.code().code(),
                "42P01" | "42501" | "42704" | "3D000"
            )
        })
}

#[cfg(test)]
mod tests {
    use super::split_source;

    #[test]
    fn source_requires_an_explicit_safe_schema_and_table() {
        assert_eq!(
            split_source("storage.storage_outbox_records").unwrap(),
            ("storage", "storage_outbox_records")
        );
        assert!(split_source("storage").is_err());
        assert!(split_source("storage.jobs;DROP").is_err());
    }
}
