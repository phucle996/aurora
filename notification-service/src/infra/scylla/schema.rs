use crate::application::ports::AppError;
use crate::config::ScyllaConfig;
use scylla::client::session::Session;

pub async fn ensure(session: &Session, config: &ScyllaConfig) -> Result<(), AppError> {
    let keyspace = &config.keyspace;
    let keyspace_query = format!(
        "CREATE KEYSPACE IF NOT EXISTS {keyspace} \
         WITH replication = {{'class': 'NetworkTopologyStrategy', \
         '{}': {}}}",
        config.local_dc, config.replication_factor
    );
    session.query_unpaged(keyspace_query, &[]).await?;
    session.use_keyspace(keyspace, false).await?;

    session
        .query_unpaged(
            "CREATE TABLE IF NOT EXISTS activity_by_user_month (
                user_id uuid,
                month_bucket text,
                occurred_at timestamp,
                event_id uuid,
                category text,
                action text,
                actor_type text,
                actor_id text,
                outcome text,
                source_service text,
                resource_type text,
                resource_id text,
                operation_id text,
                title text,
                summary text,
                metadata_json text,
                schema_version int,
                PRIMARY KEY ((user_id, month_bucket), occurred_at, event_id)
             ) WITH CLUSTERING ORDER BY (occurred_at DESC, event_id DESC)
               AND compaction = {
                 'class': 'TimeWindowCompactionStrategy',
                 'compaction_window_unit': 'DAYS',
                 'compaction_window_size': '1'
               }",
            &[],
        )
        .await?;
    session
        .query_unpaged(
            "CREATE TABLE IF NOT EXISTS activity_by_user_category_month (
                user_id uuid,
                category text,
                month_bucket text,
                occurred_at timestamp,
                event_id uuid,
                action text,
                actor_type text,
                actor_id text,
                outcome text,
                source_service text,
                resource_type text,
                resource_id text,
                operation_id text,
                title text,
                summary text,
                metadata_json text,
                schema_version int,
                PRIMARY KEY ((user_id, category, month_bucket), occurred_at, event_id)
             ) WITH CLUSTERING ORDER BY (occurred_at DESC, event_id DESC)
               AND compaction = {
                 'class': 'TimeWindowCompactionStrategy',
                 'compaction_window_unit': 'DAYS',
                 'compaction_window_size': '1'
               }",
            &[],
        )
        .await?;
    session
        .query_unpaged(
            "CREATE TABLE IF NOT EXISTS inbox_by_user_month (
                user_id uuid,
                month_bucket text,
                created_at timestamp,
                notification_id uuid,
                activity_event_id uuid,
                severity text,
                title text,
                message text,
                operation text,
                resource_id text,
                read_at timestamp,
                PRIMARY KEY ((user_id, month_bucket), created_at, notification_id)
             ) WITH CLUSTERING ORDER BY (created_at DESC, notification_id DESC)
               AND compaction = {
                 'class': 'TimeWindowCompactionStrategy',
                 'compaction_window_unit': 'DAYS',
                 'compaction_window_size': '1'
               }",
            &[],
        )
        .await?;
    session
        .query_unpaged(
            "CREATE TABLE IF NOT EXISTS inbox_state_by_user (
                user_id uuid PRIMARY KEY,
                read_before timestamp
             )",
            &[],
        )
        .await?;
    Ok(())
}

pub async fn verify(session: &Session) -> Result<(), AppError> {
    // A real query validates both schema visibility and the configured
    // consistency profile. Startup must fail before consuming a Redis entry.
    session
        .query_unpaged("SELECT user_id FROM inbox_state_by_user LIMIT 1", &[])
        .await?;
    Ok(())
}
