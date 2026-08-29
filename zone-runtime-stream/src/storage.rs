use crate::contract::RuntimeScope;

pub fn validate_scope(scope: &RuntimeScope) -> bool {
    scope.module == "storage"
        && scope.resource_type == "bucket"
        && scope.panel_id == "metrics"
        && scope.component_id.is_none()
        && scope.resource_name.as_deref().is_some_and(|name| {
            !name.is_empty()
                && name.len() <= 63
                && (name.starts_with("ws-") || name.starts_with("tn-"))
                && name
                    .bytes()
                    .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
        })
}

pub fn fixed_query(scope: &RuntimeScope) -> Option<String> {
    if !validate_scope(scope) {
        return None;
    }
    let bucket_name = scope.resource_name.as_deref()?;
    Some(format!(
        "minio_bucket_usage_total_bytes{{bucket=\"{bucket_name}\"}}"
    ))
}

#[cfg(test)]
#[path = "../test/storage.rs"]
mod tests;
