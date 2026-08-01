mod personal;
mod tenant;

use crate::results::contract::ValidatedManagedServiceResult;
use tokio_postgres::{Client, Row};

pub async fn apply_result(
    client: &mut Client,
    result: &ValidatedManagedServiceResult,
) -> Result<Option<Row>, Box<dyn std::error::Error>> {
    let instance_id = result.instance_id.to_string();
    let owner_type = client
        .query_opt(
            "SELECT owner_type FROM managed_service.managed_service_outbox_records \
             WHERE event_id = $1 \
               AND resource_id = $2 \
               AND job_topic = 'managed_service.instance.execute'",
            &[&result.source_command_event_id, &instance_id],
        )
        .await?;
    let Some(owner_type) = owner_type else {
        return Ok(None);
    };
    let owner_type: String = owner_type.get(0);
    match owner_type.as_str() {
        "PERSONAL" => personal::apply_result(client, result).await,
        "TENANT" => tenant::apply_result(client, result).await,
        _ => Err(format!("unsupported managed service owner type '{owner_type}'").into()),
    }
}
