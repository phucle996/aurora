use tokio_postgres::Client;

pub async fn update_personal_bucket_size(
    client: &Client,
    name: &str,
    used_bytes: i64,
) -> Result<Option<String>, tokio_postgres::Error> {
    let row = client
        .query_opt(
            "UPDATE storage.personal_buckets b \
             SET used_bytes = $1, updated_at = NOW() \
             FROM hierarchy.personal_workspaces w \
             WHERE b.workspace_id = w.id AND b.name = $2 \
               AND b.used_bytes IS DISTINCT FROM $1 \
             RETURNING w.owner_id::text",
            &[&used_bytes, &name],
        )
        .await?;
    Ok(row.map(|row| row.get(0)))
}

pub async fn update_tenant_bucket_size(
    client: &Client,
    name: &str,
    used_bytes: i64,
) -> Result<Vec<String>, tokio_postgres::Error> {
    let rows = client
        .query(
            "WITH updated AS ( \
                 UPDATE storage.tenant_buckets \
                 SET used_bytes = $1, updated_at = NOW() \
                 WHERE name = $2 AND used_bytes IS DISTINCT FROM $1 \
                 RETURNING tenant_id \
             ) \
             SELECT m.user_id::text \
             FROM updated u \
             JOIN hierarchy.tenant_memberships m ON m.tenant_id = u.tenant_id \
             WHERE m.status = 'active'",
            &[&used_bytes, &name],
        )
        .await?;
    Ok(rows.iter().map(|row| row.get(0)).collect())
}
