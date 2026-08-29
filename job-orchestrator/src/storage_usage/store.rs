use chrono::{DateTime, Utc};
use tokio_postgres::Client;

pub async fn update_personal_bucket_size(
    client: &Client,
    name: &str,
    used_bytes: i64,
    observed_at: DateTime<Utc>,
) -> Result<(), tokio_postgres::Error> {
    client
        .execute(
            "WITH target AS ( \
                 SELECT b.id \
                 FROM storage.personal_buckets b \
                 WHERE b.name=$2 \
                   AND (b.used_bytes_observed_at IS NULL OR b.used_bytes_observed_at < $3) \
                 FOR UPDATE \
             ) \
             UPDATE storage.personal_buckets b \
             SET used_bytes=$1, used_bytes_observed_at=$3, updated_at=NOW() \
             FROM target \
             WHERE b.id=target.id",
            &[&used_bytes, &name, &observed_at],
        )
        .await?;
    Ok(())
}

pub async fn update_tenant_bucket_size(
    client: &Client,
    name: &str,
    used_bytes: i64,
    observed_at: DateTime<Utc>,
) -> Result<(), tokio_postgres::Error> {
    client
        .execute(
            "WITH target AS ( \
                 SELECT id \
                 FROM storage.tenant_buckets \
                 WHERE name=$2 \
                   AND (used_bytes_observed_at IS NULL OR used_bytes_observed_at < $3) \
                 FOR UPDATE \
             ) \
             UPDATE storage.tenant_buckets b \
             SET used_bytes=$1, used_bytes_observed_at=$3, updated_at=NOW() \
             FROM target \
             WHERE b.id=target.id",
            &[&used_bytes, &name, &observed_at],
        )
        .await?;
    Ok(())
}
