use serde::Deserialize;

pub const REALTIME_CHANNEL: &str = "aurora:realtime:notifications";
pub const MAX_ENVELOPE_BYTES: usize = 256 * 1024;
pub const JOB_CHANNEL_PREFIX: &str = "notifications";
pub const RUNTIME_CHANNEL_PREFIX: &str = "runtime";

#[derive(Debug, Deserialize)]
pub struct RealtimeEnvelope {
    pub kind: String,
    pub user_id: String,
    pub payload: serde_json::Value,
}

#[derive(Clone, Copy, Debug)]
pub enum RealtimeKind {
    Storage,
    MailRuntime,
}

impl RealtimeEnvelope {
    pub fn parse(raw: &[u8]) -> Result<Self, &'static str> {
        if raw.len() > MAX_ENVELOPE_BYTES {
            return Err("REALTIME_ENVELOPE_TOO_LARGE");
        }

        let envelope: Self =
            serde_json::from_slice(raw).map_err(|_| "REALTIME_ENVELOPE_INVALID")?;
        if uuid::Uuid::parse_str(&envelope.user_id).is_err() {
            return Err("REALTIME_USER_ID_INVALID");
        }
        if envelope.payload.as_object().is_none() {
            return Err("REALTIME_PAYLOAD_OBJECT_REQUIRED");
        }
        envelope.kind().ok_or("REALTIME_KIND_INVALID")?;
        Ok(envelope)
    }

    pub fn kind(&self) -> Option<RealtimeKind> {
        match self.kind.as_str() {
            "storage" => Some(RealtimeKind::Storage),
            "mail_runtime" => Some(RealtimeKind::MailRuntime),
            _ => None,
        }
    }
}

pub fn notification_channel(user_id: &str) -> String {
    format!("{JOB_CHANNEL_PREFIX}:{user_id}")
}

pub fn runtime_channel(user_id: &str) -> String {
    format!("{RUNTIME_CHANNEL_PREFIX}:{user_id}")
}
