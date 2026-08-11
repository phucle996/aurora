use base64::Engine;
use prost::Message;
use serde::Deserialize;

use crate::{
    control_assertion::ControlAssertion, error::AuthzError, transfer_proto::TransferGrantV1,
    zone_access::AccessRecord,
};

const TRANSFER_TICKET_SCHEMA_VERSION: u32 = 1;

#[derive(Deserialize)]
struct TicketRequest {
    capability: String,
    operation: String,
    access_session_id: String,
    resource: StorageObject,
    #[serde(default)]
    constraints: TicketConstraints,
}

#[derive(Deserialize)]
struct StorageObject {
    bucket_name: String,
    object_key: String,
}

#[derive(Default, Deserialize)]
struct TicketConstraints {
    content_length: Option<u64>,
    content_type: Option<String>,
}

pub fn storage_object_grant(
    assertion: &ControlAssertion,
    record: &AccessRecord,
    method: &str,
    path: &str,
    body: &[u8],
) -> Result<String, AuthzError> {
    let is_issue = method == "POST" && path == "/zone-control/v1/transfer-tickets";
    let is_revoke = method == "DELETE"
        && path
            .strip_prefix("/zone-control/v1/transfer-tickets/")
            .is_some_and(|ticket_id| {
                !ticket_id.is_empty()
                    && !ticket_id.contains('/')
                    && uuid::Uuid::parse_str(ticket_id).is_ok()
            });
    if !is_issue && !is_revoke {
        return Err(AuthzError::Denied("TRANSFER_TICKET_ROUTE_INVALID"));
    }
    let request: TicketRequest = serde_json::from_slice(body)
        .map_err(|_| AuthzError::Denied("TRANSFER_TICKET_BODY_INVALID"))?;
    if request.operation == "revoke" {
        if request.capability != "storage.object"
            || request.access_session_id != assertion.access_session_id
            || record.access_session_id != assertion.access_session_id
            || record.actor_id != assertion.actor_id
            || record.zone_id != assertion.zone_id
            || !path
                .strip_prefix("/zone-control/v1/transfer-tickets/")
                .is_some_and(|ticket_id| {
                    !ticket_id.is_empty()
                        && !ticket_id.contains('/')
                        && uuid::Uuid::parse_str(ticket_id).is_ok()
                })
        {
            return Err(AuthzError::Denied("TRANSFER_TICKET_REVOKE_INVALID"));
        }
        let grant = TransferGrantV1 {
            schema_version: TRANSFER_TICKET_SCHEMA_VERSION,
            capability: request.capability,
            actor_id: record.actor_id.clone(),
            zone_id: record.zone_id.clone(),
            resource_id: record.resource_id.clone(),
            workspace_id: record.workspace_id.clone(),
            operation_id: assertion.operation_id.clone(),
            method: "GET".to_string(),
            public_path: "/".to_string(),
            content_length: None,
            content_type: None,
            one_time: true,
        };
        return Ok(base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(grant.encode_to_vec()));
    }
    if request.capability != "storage.object"
        || request.access_session_id != assertion.access_session_id
        || record.access_session_id != assertion.access_session_id
        || record.actor_id != assertion.actor_id
        || record.zone_id != assertion.zone_id
        || record.bucket_name != request.resource.bucket_name
        || request.resource.object_key.is_empty()
        || request.resource.object_key.len() > 1024
        || request.resource.object_key.contains('\0')
        || request
            .resource
            .object_key
            .split('/')
            .any(|segment| segment.is_empty() || matches!(segment, "." | ".."))
    {
        return Err(AuthzError::Denied("TRANSFER_TICKET_SCOPE_INVALID"));
    }
    let (action, transfer_method, content_length) = match request.operation.as_str() {
        "upload" => {
            let Some(content_length) = request.constraints.content_length else {
                return Err(AuthzError::Denied("TRANSFER_TICKET_LENGTH_REQUIRED"));
            };
            if content_length == 0 || content_length > 5 * 1024 * 1024 * 1024_u64 {
                return Err(AuthzError::Denied("TRANSFER_TICKET_LENGTH_INVALID"));
            }
            ("PutObject", "PUT", Some(content_length))
        }
        "download" if request.constraints.content_length.is_none() => ("GetObject", "GET", None),
        _ => return Err(AuthzError::Denied("TRANSFER_TICKET_OPERATION_INVALID")),
    };
    if !record.actions.iter().any(|allowed| allowed == action)
        || (!record.key_prefix.is_empty()
            && !request.resource.object_key.starts_with(&record.key_prefix))
    {
        return Err(AuthzError::Denied("TRANSFER_TICKET_SCOPE_INVALID"));
    }
    let content_type = match request.constraints.content_type {
        Some(value)
            if !value.is_empty()
                && value.len() <= 128
                && value
                    .bytes()
                    .all(|byte| byte.is_ascii_graphic() || byte == b' ') =>
        {
            Some(value)
        }
        Some(_) => return Err(AuthzError::Denied("TRANSFER_TICKET_CONTENT_TYPE_INVALID")),
        None => None,
    };
    let grant = TransferGrantV1 {
        schema_version: TRANSFER_TICKET_SCHEMA_VERSION,
        capability: request.capability,
        actor_id: record.actor_id.clone(),
        zone_id: record.zone_id.clone(),
        resource_id: record.resource_id.clone(),
        workspace_id: record.workspace_id.clone(),
        operation_id: assertion.operation_id.clone(),
        method: transfer_method.to_string(),
        public_path: format!(
            "/{}/{}",
            request.resource.bucket_name,
            encode_object_key(&request.resource.object_key)
        ),
        content_length,
        content_type,
        one_time: true,
    };
    Ok(base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(grant.encode_to_vec()))
}

fn encode_object_key(value: &str) -> String {
    let mut output = String::with_capacity(value.len());
    for byte in value.bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b'~' | b'/') {
            output.push(char::from(byte));
        } else {
            const HEX: &[u8; 16] = b"0123456789ABCDEF";
            output.push('%');
            output.push(char::from(HEX[usize::from(byte >> 4)]));
            output.push(char::from(HEX[usize::from(byte & 15)]));
        }
    }
    output
}
