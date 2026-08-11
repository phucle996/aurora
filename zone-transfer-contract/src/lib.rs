use serde::{Deserialize, Serialize};

pub const TRANSFER_TICKET_SCHEMA_VERSION: u32 = 1;

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct TransferGrantV1 {
    pub schema_version: u32,
    pub capability: String,
    pub actor_id: String,
    pub zone_id: String,
    pub resource_id: String,
    pub workspace_id: String,
    pub operation_id: String,
    pub method: String,
    pub public_path: String,
    pub content_length: Option<u64>,
    pub content_type: Option<String>,
    pub one_time: bool,
}

#[derive(Clone, Debug, Deserialize, Serialize, Eq, PartialEq)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum TransferTicketState {
    Issued,
    Consuming,
    Revoked,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct TransferTicketV1 {
    pub schema_version: u32,
    pub ticket_id: String,
    pub secret_sha256: String,
    pub capability: String,
    pub actor_id: String,
    pub zone_id: String,
    pub resource_id: String,
    pub workspace_id: String,
    pub operation_id: String,
    pub method: String,
    pub public_path: String,
    pub content_length: Option<u64>,
    pub content_type: Option<String>,
    pub issued_at_unix_seconds: u64,
    pub expires_at_unix_seconds: u64,
    pub one_time: bool,
    pub state: TransferTicketState,
}
