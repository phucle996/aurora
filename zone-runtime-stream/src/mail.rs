use crate::contract::RuntimeScope;

pub fn validate_scope(scope: &RuntimeScope) -> bool {
    scope.module == "mail" && scope.resource_type == "consumer"
}

pub fn fixed_query(scope: &RuntimeScope) -> Option<String> {
    if !validate_scope(scope) {
        return None;
    }
    let resource_id = scope.resource_id;
    let owner_id = scope.owner_id;
    let workspace_id = scope.workspace_id;
    let zone_id = scope.zone_id;
    let component = scope
        .component_id
        .as_deref()
        .map(regex_escape)
        .unwrap_or_else(|| ".*".to_string());

    match scope.panel_id.as_str() {
        "health" => Some(format!(
            "aurora_runtime_health{{aurora_module=\"mail\",aurora_resource_type=\"consumer\",aurora_resource_id=\"{resource_id}\",aurora_owner_id=\"{owner_id}\",aurora_workspace_id=\"{workspace_id}\",aurora_zone_id=\"{zone_id}\",aurora_component_id=~\"{component}\"}}"
        )),
        "metrics" => Some(format!(
            "aurora_runtime_metric{{aurora_module=\"mail\",aurora_resource_type=\"consumer\",aurora_resource_id=\"{resource_id}\",aurora_owner_id=\"{owner_id}\",aurora_workspace_id=\"{workspace_id}\",aurora_zone_id=\"{zone_id}\",aurora_component_id=~\"{component}\"}}"
        )),
        "logs" | "events" => Some(format!(
            "_stream:{{service_name=\"aurora-dataplane\"}} AND aurora_module:=\"mail\" AND aurora_resource_type:=\"consumer\" AND aurora_resource_id:=\"{resource_id}\" AND aurora_owner_id:=\"{owner_id}\" AND aurora_workspace_id:=\"{workspace_id}\" AND aurora_zone_id:=\"{zone_id}\" AND aurora_component_id:~\"{component}\""
        )),
        _ => None,
    }
}

fn regex_escape(value: &str) -> String {
    let mut escaped = String::with_capacity(value.len());
    for character in value.chars() {
        if matches!(
            character,
            '\\' | '.' | '^' | '$' | '*' | '+' | '?' | '(' | ')' | '[' | ']' | '{' | '}' | '|'
        ) {
            escaped.push('\\');
        }
        escaped.push(character);
    }
    escaped
}

#[cfg(test)]
#[path = "../test/mail.rs"]
mod tests;
