use serde::{Deserialize, Serialize};
use std::collections::HashMap;

pub const MAX_CLIENT_ID_BYTES: usize = 128;

#[derive(Debug, Deserialize)]
pub struct ConnectRequest {
    pub client: String,
    pub request: Option<RequestInfo>,
}

impl ConnectRequest {
    pub fn is_valid(&self) -> bool {
        !self.client.is_empty()
            && self.client.len() <= MAX_CLIENT_ID_BYTES
            && !self.client.chars().any(char::is_control)
    }

    pub fn forwarded_header(&self, name: &str) -> Option<&str> {
        self.request
            .as_ref()
            .and_then(|request| request.headers.as_ref())
            .and_then(|headers| {
                headers
                    .iter()
                    .find(|(key, _)| key.eq_ignore_ascii_case(name))
                    .map(|(_, value)| value.as_str())
            })
    }
}

#[derive(Debug, Deserialize)]
pub struct RequestInfo {
    pub headers: Option<HashMap<String, String>>,
}

#[derive(Debug, Serialize)]
pub struct ConnectResponse {
    pub result: ConnectResult,
}

#[derive(Debug, Serialize)]
pub struct ConnectResult {
    pub user: String,
    pub channels: Vec<String>,
}
