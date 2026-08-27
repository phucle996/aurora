use chrono::{DateTime, Utc};
use tokio_postgres::Client;

pub async fn update_personal_bucket_size(
    client: &Client,
    name: &str,
    used_bytes: i64,
    observed_at: DateTime<Utc>,
) -> Result<Option<String>, tokio_postgres::Error> {
    let row = client
        .query_opt(
            "WITH target AS ( \
                 SELECT b.id, b.used_bytes, w.owner_id \
                 FROM storage.personal_buckets b \
                 JOIN hierarchy.personal_workspaces w ON b.workspace_id=w.id \
                 WHERE b.name=$2 \
                   AND (b.used_bytes_observed_at IS NULL OR b.used_bytes_observed_at < $3) \
                 FOR UPDATE \
             ), updated AS ( \
                 UPDATE storage.personal_buckets b \
                 SET used_bytes=$1, used_bytes_observed_at=$3, updated_at=NOW() \
                 FROM target \
                 WHERE b.id=target.id \
                 RETURNING target.owner_id, target.used_bytes IS DISTINCT FROM $1 AS changed \
             ) \
             SELECT owner_id::text FROM updated WHERE changed",
            &[&used_bytes, &name, &observed_at],
        )
        .await?;
    Ok(row.map(|row| row.get(0)))
}

pub async fn update_tenant_bucket_size(
    client: &Client,
    name: &str,
    used_bytes: i64,
    observed_at: DateTime<Utc>,
) -> Result<Vec<String>, tokio_postgres::Error> {
    let rows = client
        .query(
            "WITH target AS ( \
                 SELECT id, tenant_id, used_bytes \
                 FROM storage.tenant_buckets \
                 WHERE name=$2 \
                   AND (used_bytes_observed_at IS NULL OR used_bytes_observed_at < $3) \
                 FOR UPDATE \
             ), updated AS ( \
                 UPDATE storage.tenant_buckets b \
                 SET used_bytes=$1, used_bytes_observed_at=$3, updated_at=NOW() \
                 FROM target \
                 WHERE b.id=target.id \
                 RETURNING target.tenant_id, target.used_bytes IS DISTINCT FROM $1 AS changed \
             ) \
             SELECT m.user_id::text \
             FROM updated u \
             JOIN hierarchy.tenant_memberships m ON m.tenant_id = u.tenant_id \
             WHERE u.changed AND m.status = 'active'",
            &[&used_bytes, &name, &observed_at],
        )
        .await?;
    Ok(rows.iter().map(|row| row.get(0)).collect())
}
