use crate::application::auth::AuthCredentials;
use futures_util::future::BoxFuture;
use serde_json::Value;
use std::error::Error;

pub type AppError = Box<dyn Error + Send + Sync>;

#[derive(Debug)]
pub enum AuthError {
    Invalid,
    Unavailable(String),
    Protocol(String),
}

impl std::fmt::Display for AuthError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Invalid => formatter.write_str("credentials were rejected"),
            Self::Unavailable(message) => write!(
                formatter,
                "authentication dependency unavailable: {message}"
            ),
            Self::Protocol(message) => {
                write!(formatter, "authentication response invalid: {message}")
            }
        }
    }
}

impl Error for AuthError {}

#[derive(Debug)]
pub struct AuthenticatedPrincipal {
    pub id: String,
}

pub trait AuthVerifier: Send + Sync {
    fn verify<'a>(
        &'a self,
        credentials: AuthCredentials,
    ) -> BoxFuture<'a, Result<AuthenticatedPrincipal, AuthError>>;
}

pub trait RealtimePublisher: Send + Sync {
    fn publish<'a>(&'a self, channel: &'a str, data: Value) -> BoxFuture<'a, Result<(), AppError>>;
}
