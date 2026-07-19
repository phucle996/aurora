use crate::config::Config;

#[derive(Clone, Debug)]
pub struct SenderProfile {
    pub id: String,
    pub version: u32,
    pub from_address: String,
    pub account_id: String,
    pub identity_id: String,
    pub mailbox_id: String,
}

impl SenderProfile {
    pub fn from_config(config: &Config) -> Result<Self, String> {
        let required = [
            (
                "MAIL_SENDER_PROFILE_ID",
                config.mail_sender_profile_id.trim(),
            ),
            ("MAIL_SENDER_ADDRESS", config.mail_sender_address.trim()),
            (
                "STALWART_JMAP_ACCOUNT_ID",
                config.stalwart_jmap_account_id.trim(),
            ),
            (
                "STALWART_JMAP_IDENTITY_ID",
                config.stalwart_jmap_identity_id.trim(),
            ),
            (
                "STALWART_JMAP_MAILBOX_ID",
                config.stalwart_jmap_mailbox_id.trim(),
            ),
        ];
        if let Some((name, _)) = required.iter().find(|(_, value)| value.is_empty()) {
            return Err(format!("{name} is required for JMAP mail runtime"));
        }
        if config.mail_sender_version == 0 {
            // [COMMENT]: Version 0 là protobuf default/missing; cấm cấu hình 0 để payload cũ không bypass binding.
            return Err("MAIL_SENDER_VERSION must be greater than zero".to_string());
        }
        let from_mailbox = config
            .mail_sender_address
            .parse::<lettre::message::Mailbox>()
            .map_err(|error| format!("MAIL_SENDER_ADDRESS is invalid: {error}"))?;
        Ok(Self {
            id: config.mail_sender_profile_id.clone(),
            version: config.mail_sender_version,
            from_address: from_mailbox.email.to_string(),
            account_id: config.stalwart_jmap_account_id.clone(),
            identity_id: config.stalwart_jmap_identity_id.clone(),
            mailbox_id: config.stalwart_jmap_mailbox_id.clone(),
        })
    }
}

#[derive(Debug)]
pub struct PreparedMail {
    pub job_id: String,
    pub recipient: String,
    pub subject: String,
    pub text_body: Option<String>,
    pub html_body: Option<String>,
    pub estimated_bytes: usize,
}

#[derive(Clone, Debug)]
pub struct MailAccepted {
    pub submission_id: String,
}

#[derive(Clone, Debug)]
pub struct MailSubmitError {
    pub code: String,
    pub message: String,
    pub retryable: bool,
}

impl MailSubmitError {
    pub fn new(code: impl Into<String>, message: impl Into<String>, retryable: bool) -> Self {
        Self {
            code: code.into(),
            message: message.into(),
            retryable,
        }
    }
}

pub type MailSubmitResult = Result<MailAccepted, MailSubmitError>;
