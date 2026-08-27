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
    upload_id: Option<String>,
    part_number: Option<u32>,
    version_id: Option<String>,
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
            || record.workspace_id != assertion.workspace_id
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
        || record.workspace_id != assertion.workspace_id
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
    let (action, transfer_method, public_path, content_length, content_type) =
        match request.operation.as_str() {
            "upload" => {
                let Some(content_length) = request.constraints.content_length else {
                    return Err(AuthzError::Denied("TRANSFER_TICKET_LENGTH_REQUIRED"));
                };
                if content_length == 0 || content_length > 5 * 1024 * 1024 * 1024_u64 {
                    return Err(AuthzError::Denied("TRANSFER_TICKET_LENGTH_INVALID"));
                }
                let content_type = parse_content_type(request.constraints.content_type.as_deref())?;
                (
                    "PutObject",
                    "PUT",
                    format!(
                        "/{}/{}",
                        request.resource.bucket_name,
                        encode_object_key(&request.resource.object_key)
                    ),
                    Some(content_length),
                    content_type,
                )
            }
            "download" if request.constraints.content_length.is_none() => {
                let path = if let Some(version_id) = request.constraints.version_id.as_deref() {
                    validate_version_id(version_id)?;
                    format!(
                        "/{}/{}?versionId={}",
                        request.resource.bucket_name,
                        encode_object_key(&request.resource.object_key),
                        encode_query_value(version_id)
                    )
                } else {
                    format!(
                        "/{}/{}",
                        request.resource.bucket_name,
                        encode_object_key(&request.resource.object_key)
                    )
                };
                ("GetObject", "GET", path, None, None)
            }
            "multipart_initiate" => {
                let content_type = parse_content_type(request.constraints.content_type.as_deref())?;
                (
                    "PutObject",
                    "POST",
                    format!(
                        "/{}/{}?uploads",
                        request.resource.bucket_name,
                        encode_object_key(&request.resource.object_key)
                    ),
                    None,
                    content_type,
                )
            }
            "multipart_upload_part" => {
                let Some(upload_id) = request.constraints.upload_id.as_deref() else {
                    return Err(AuthzError::Denied("TRANSFER_TICKET_UPLOAD_ID_REQUIRED"));
                };
                validate_upload_id(upload_id)?;
                let Some(part_number) = request.constraints.part_number else {
                    return Err(AuthzError::Denied("TRANSFER_TICKET_PART_NUMBER_REQUIRED"));
                };
                if !(1..=10_000).contains(&part_number) {
                    return Err(AuthzError::Denied("TRANSFER_TICKET_PART_NUMBER_INVALID"));
                }
                let Some(content_length) = request.constraints.content_length else {
                    return Err(AuthzError::Denied("TRANSFER_TICKET_LENGTH_REQUIRED"));
                };
                if content_length == 0 || content_length > 5 * 1024 * 1024 * 1024_u64 {
                    return Err(AuthzError::Denied("TRANSFER_TICKET_LENGTH_INVALID"));
                }
                (
                    "PutObject",
                    "PUT",
                    format!(
                        "/{}/{}?partNumber={}&uploadId={}",
                        request.resource.bucket_name,
                        encode_object_key(&request.resource.object_key),
                        part_number,
                        encode_query_value(upload_id)
                    ),
                    Some(content_length),
                    None,
                )
            }
            "multipart_complete" => {
                let Some(upload_id) = request.constraints.upload_id.as_deref() else {
                    return Err(AuthzError::Denied("TRANSFER_TICKET_UPLOAD_ID_REQUIRED"));
                };
                validate_upload_id(upload_id)?;
                let content_type = parse_content_type(request.constraints.content_type.as_deref())?;
                (
                    "PutObject",
                    "POST",
                    format!(
                        "/{}/{}?uploadId={}",
                        request.resource.bucket_name,
                        encode_object_key(&request.resource.object_key),
                        encode_query_value(upload_id)
                    ),
                    request.constraints.content_length,
                    content_type,
                )
            }
            "multipart_abort" => {
                let Some(upload_id) = request.constraints.upload_id.as_deref() else {
                    return Err(AuthzError::Denied("TRANSFER_TICKET_UPLOAD_ID_REQUIRED"));
                };
                validate_upload_id(upload_id)?;
                (
                    "PutObject",
                    "DELETE",
                    format!(
                        "/{}/{}?uploadId={}",
                        request.resource.bucket_name,
                        encode_object_key(&request.resource.object_key),
                        encode_query_value(upload_id)
                    ),
                    None,
                    None,
                )
            }
            _ => return Err(AuthzError::Denied("TRANSFER_TICKET_OPERATION_INVALID")),
        };
    if !record.actions.iter().any(|allowed| allowed == action)
        || (!record.key_prefix.is_empty()
            && !request.resource.object_key.starts_with(&record.key_prefix))
    {
        return Err(AuthzError::Denied("TRANSFER_TICKET_SCOPE_INVALID"));
    }
    let grant = TransferGrantV1 {
        schema_version: TRANSFER_TICKET_SCHEMA_VERSION,
        capability: request.capability,
        actor_id: record.actor_id.clone(),
        zone_id: record.zone_id.clone(),
        resource_id: record.resource_id.clone(),
        workspace_id: record.workspace_id.clone(),
        operation_id: assertion.operation_id.clone(),
        method: transfer_method.to_string(),
        public_path,
        content_length,
        content_type,
        one_time: true,
    };
    Ok(base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(grant.encode_to_vec()))
}

fn validate_upload_id(upload_id: &str) -> Result<(), AuthzError> {
    if upload_id.is_empty()
        || upload_id.len() > 256
        || !upload_id.bytes().all(|b| {
            b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_' | b'.' | b'~' | b'+' | b'/' | b'=')
        })
    {
        return Err(AuthzError::Denied("TRANSFER_TICKET_UPLOAD_ID_INVALID"));
    }
    Ok(())
}

fn validate_version_id(version_id: &str) -> Result<(), AuthzError> {
    if version_id.is_empty()
        || version_id.len() > 1024
        || !version_id.bytes().all(|b| {
            b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_' | b'.' | b'~' | b'+' | b'/' | b'=')
        })
    {
        return Err(AuthzError::Denied("TRANSFER_TICKET_VERSION_ID_INVALID"));
    }
    Ok(())
}

fn parse_content_type(content_type: Option<&str>) -> Result<Option<String>, AuthzError> {
    match content_type {
        Some(value)
            if !value.is_empty()
                && value.len() <= 128
                && value
                    .bytes()
                    .all(|byte| byte.is_ascii_graphic() || byte == b' ') =>
        {
            Ok(Some(value.to_string()))
        }
        Some(_) => Err(AuthzError::Denied("TRANSFER_TICKET_CONTENT_TYPE_INVALID")),
        None => Ok(None),
    }
}

fn encode_query_value(value: &str) -> String {
    let mut output = String::with_capacity(value.len());
    for byte in value.bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b'~') {
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

#[cfg(test)]
#[path = "../tests/unit/transfer_ticket.rs"]
mod tests;
