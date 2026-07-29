use super::environment::Environment;
use std::path::PathBuf;
use std::time::Duration;

#[derive(Clone, Debug)]
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
    pub(crate) fn load(environment: &Environment) -> Result<Self, super::ConfigError> {
        Ok(Self {
            // Vault is the connection-identity authority for this process;
            // missing deployment routing is a startup error.
            addr: environment.required("VAULT_ADDR")?,
            token: environment.optional("VAULT_TOKEN").map(str::to_owned),
            role_id: environment.optional("VAULT_ROLE_ID").map(str::to_owned),
            secret_id: environment.optional("VAULT_SECRET_ID").map(str::to_owned),
            kubernetes_role: environment
                .optional("VAULT_KUBERNETES_ROLE")
                .map(str::to_owned),
            kubernetes_jwt_path: environment
                .optional("VAULT_KUBERNETES_JWT_PATH")
                .map(PathBuf::from)
                .unwrap_or_else(|| {
                    PathBuf::from("/var/run/secrets/kubernetes.io/serviceaccount/token")
                }),
            timeout: Duration::from_millis(environment.bounded_u64(
                "VAULT_TIMEOUT_MS",
                5_000,
                100,
                60_000,
            )?),
            max_retries: environment.bounded_usize("VAULT_MAX_RETRIES", 5, 1, 20)?,
        })
    }
}
