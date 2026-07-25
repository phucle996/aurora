use super::environment::{normalized, validate_identifier, Environment};
use super::tls::TlsClientConfig;
use std::path::PathBuf;
use std::str::FromStr;

#[derive(Clone, Copy, Eq, PartialEq)]
pub enum NatsAuthMode {
    None,
    Token,
    UserPassword,
    CredentialsFile,
}

impl FromStr for NatsAuthMode {
    type Err = String;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        match normalized(value).as_str() {
            "none" => Ok(Self::None),
            "token" => Ok(Self::Token),
            "user_password" => Ok(Self::UserPassword),
            "credentials_file" => Ok(Self::CredentialsFile),
            _ => Err(
                "NATS_AUTH_MODE must be explicitly set to none, token, user_password or credentials_file"
                    .to_owned(),
            ),
        }
    }
}

/// NATS Core is the realtime Central-to-Zone soft-state transport. This type
/// must never be reused for a Zone NATS JetStream KV endpoint.
#[derive(Clone)]
pub struct NatsCoreConfig {
    pub urls: Vec<String>,
    pub client_name: String,
    pub auth_mode: NatsAuthMode,
    pub token: Option<String>,
    pub username: Option<String>,
    pub password: Option<String>,
    pub credentials_file: Option<PathBuf>,
    pub tls: Option<TlsClientConfig>,
    pub tls_first: bool,
    pub connect_timeout_secs: u64,
    pub request_timeout_secs: u64,
    pub ping_interval_secs: u64,
    pub subscription_capacity: usize,
    pub client_capacity: usize,
    pub retry_initial_connect: bool,
    pub reconnect_base_delay_ms: u64,
    pub reconnect_max_delay_ms: u64,
}

impl NatsCoreConfig {
    pub(crate) fn load(environment: &Environment) -> Result<Self, String> {
        let urls = environment
            .required("NATS_URL")?
            .split(',')
            .map(str::trim)
            .filter(|url| !url.is_empty())
            .map(str::to_owned)
            .collect::<Vec<_>>();
        if urls.is_empty()
            || urls
                .iter()
                .any(|url| !url.starts_with("nats://") && !url.starts_with("tls://"))
        {
            return Err(
                "NATS_URL must contain one or more comma-separated nats:// or tls:// endpoints"
                    .to_owned(),
            );
        }
        let uses_tls = urls.iter().all(|url| url.starts_with("tls://"));
        if !uses_tls && urls.iter().any(|url| url.starts_with("tls://")) {
            return Err(
                "NATS_URL cannot mix nats:// and tls:// endpoints in one server pool".to_owned(),
            );
        }

        let auth_mode: NatsAuthMode = environment.required_enum("NATS_AUTH_MODE")?;
        let token = environment.optional("NATS_TOKEN");
        let username = environment.optional("NATS_USERNAME");
        let password = environment.optional("NATS_PASSWORD");
        let credentials_file = environment.optional_path("NATS_CREDENTIALS_FILE");
        validate_auth(auth_mode, &token, &username, &password, &credentials_file)?;
        if auth_mode != NatsAuthMode::None && !uses_tls {
            return Err(
                "NATS authentication requires tls:// endpoints to protect credentials and server identity"
                    .to_owned(),
            );
        }

        let tls = if uses_tls {
            Some(TlsClientConfig::load(
                environment,
                "NATS_TLS",
                "NATS_TLS_CA_CERT",
                "NATS_TLS_CLIENT_CERT",
                "NATS_TLS_CLIENT_KEY",
            )?)
        } else {
            TlsClientConfig::ensure_absent(
                environment,
                "NATS_TLS",
                "NATS_TLS_CA_CERT",
                "NATS_TLS_CLIENT_CERT",
                "NATS_TLS_CLIENT_KEY",
                "NATS_URL uses nats://",
            )?;
            None
        };

        let tls_first = environment.optional_bool("NATS_TLS_FIRST", false)?;
        if tls_first && !uses_tls {
            return Err("NATS_TLS_FIRST=true requires tls:// NATS_URL endpoints".to_owned());
        }
        let reconnect_base_delay_ms =
            environment.bounded("NATS_RECONNECT_BASE_DELAY_MS", 250_u64, 10, 30_000)?;
        let reconnect_max_delay_ms =
            environment.bounded("NATS_RECONNECT_MAX_DELAY_MS", 10_000_u64, 100, 120_000)?;
        if reconnect_base_delay_ms > reconnect_max_delay_ms {
            return Err(
                "NATS_RECONNECT_BASE_DELAY_MS cannot exceed NATS_RECONNECT_MAX_DELAY_MS".to_owned(),
            );
        }

        let client_name = environment
            .optional("NATS_CLIENT_NAME")
            .unwrap_or_else(|| "aurora-job-orchestrator".to_owned());
        validate_identifier("NATS_CLIENT_NAME", &client_name)?;

        Ok(Self {
            urls,
            client_name,
            auth_mode,
            token,
            username,
            password,
            credentials_file,
            tls,
            tls_first,
            connect_timeout_secs: environment.bounded("NATS_CONNECT_TIMEOUT_SECS", 5_u64, 1, 60)?,
            request_timeout_secs: environment.bounded(
                "NATS_REQUEST_TIMEOUT_SECS",
                5_u64,
                1,
                120,
            )?,
            ping_interval_secs: environment.bounded("NATS_PING_INTERVAL_SECS", 30_u64, 5, 300)?,
            subscription_capacity: environment.bounded(
                "NATS_SUBSCRIPTION_CAPACITY",
                4_096_usize,
                128,
                1_048_576,
            )?,
            client_capacity: environment.bounded(
                "NATS_CLIENT_CAPACITY",
                4_096_usize,
                128,
                1_048_576,
            )?,
            // Initial retry remains opt-in so Kubernetes/Compose owns the
            // bounded process-level restart policy during bootstrap.
            retry_initial_connect: environment
                .optional_bool("NATS_RETRY_INITIAL_CONNECT", false)?,
            reconnect_base_delay_ms,
            reconnect_max_delay_ms,
        })
    }
}

fn validate_auth(
    mode: NatsAuthMode,
    token: &Option<String>,
    username: &Option<String>,
    password: &Option<String>,
    credentials_file: &Option<PathBuf>,
) -> Result<(), String> {
    let configured_methods = usize::from(token.is_some())
        + usize::from(username.is_some() || password.is_some())
        + usize::from(credentials_file.is_some());
    if configured_methods > 1 {
        return Err("NATS authentication methods are mutually exclusive".to_owned());
    }
    match mode {
        NatsAuthMode::None if configured_methods == 0 => Ok(()),
        NatsAuthMode::Token if token.is_some() => Ok(()),
        NatsAuthMode::UserPassword if username.is_some() && password.is_some() => Ok(()),
        NatsAuthMode::CredentialsFile if credentials_file.is_some() => Ok(()),
        NatsAuthMode::None => {
            Err("NATS_AUTH_MODE=none cannot be combined with NATS credentials".to_owned())
        }
        NatsAuthMode::Token => Err("NATS_AUTH_MODE=token requires NATS_TOKEN".to_owned()),
        NatsAuthMode::UserPassword => {
            Err("NATS_AUTH_MODE=user_password requires NATS_USERNAME and NATS_PASSWORD".to_owned())
        }
        NatsAuthMode::CredentialsFile => {
            Err("NATS_AUTH_MODE=credentials_file requires NATS_CREDENTIALS_FILE".to_owned())
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{validate_auth, NatsAuthMode};

    #[test]
    fn authentication_methods_are_exclusive() {
        assert!(validate_auth(
            NatsAuthMode::Token,
            &Some("token".to_owned()),
            &None,
            &None,
            &None,
        )
        .is_ok());
        assert!(validate_auth(
            NatsAuthMode::Token,
            &Some("token".to_owned()),
            &Some("user".to_owned()),
            &Some("password".to_owned()),
            &None,
        )
        .is_err());
    }
}
