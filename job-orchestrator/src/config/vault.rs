use super::environment::Environment;
use std::path::PathBuf;
use std::time::Duration;

/// Vault bootstrap/auth configuration only. Connection credentials are read by
/// infra connectors from fixed capability paths and never hydrated into Config.
#[derive(Clone)]
pub struct VaultConfig {
    pub addr: String,
    pub token: Option<String>,
    pub role_id: Option<String>,
    pub secret_id: Option<String>,
    pub kubernetes_role: Option<String>,
    pub kubernetes_jwt_path: PathBuf,
    pub timeout: Duration,
    pub max_retries: usize,
}

impl VaultConfig {
    pub(crate) fn load(environment: &Environment) -> Result<Self, String> {
        Ok(Self {
            addr: environment
                .optional("VAULT_ADDR")
                .unwrap_or_else(|| "http://127.0.0.1:8200".to_owned()),
            token: environment.optional("VAULT_TOKEN"),
            role_id: environment.optional("VAULT_ROLE_ID"),
            secret_id: environment.optional("VAULT_SECRET_ID"),
            kubernetes_role: environment.optional("VAULT_KUBERNETES_ROLE"),
            kubernetes_jwt_path: environment
                .optional_path("VAULT_KUBERNETES_JWT_PATH")
                .unwrap_or_else(|| {
                    PathBuf::from("/var/run/secrets/kubernetes.io/serviceaccount/token")
                }),
            timeout: Duration::from_secs(environment.bounded(
                "VAULT_TIMEOUT_SECS",
                5_u64,
                1,
                60,
            )?),
            max_retries: environment.bounded("VAULT_MAX_RETRIES", 5_usize, 1, 20)?,
        })
    }
}
