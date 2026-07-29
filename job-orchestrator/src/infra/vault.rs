use crate::config::VaultConfig;
use reqwest::Client as HttpClient;
use serde::de::DeserializeOwned;
use std::fs;
use std::time::Duration;

pub struct VaultClient {
    http: HttpClient,
    addr: String,
    token: String,
    max_retries: usize,
}

#[derive(serde::Deserialize)]
struct KvResponse {
    data: KvData,
}

#[derive(serde::Deserialize)]
struct KvData {
    data: serde_json::Value,
}

impl VaultClient {
    pub async fn new(config: &VaultConfig) -> Result<Self, String> {
        let http = HttpClient::builder()
            .timeout(config.timeout)
            .build()
            .map_err(|error| format!("build Vault client: {error}"))?;
        let mut last_error = "no Vault authentication attempt".to_owned();
        for attempt in 1..=config.max_retries.max(1) {
            match Self::authenticate(&http, config).await {
                Ok(token) => {
                    return Ok(Self {
                        http,
                        addr: config.addr.trim_end_matches('/').to_owned(),
                        token,
                        max_retries: config.max_retries.max(1),
                    })
                }
                Err(error) => last_error = error,
            }
            if attempt < config.max_retries {
                tokio::time::sleep(Duration::from_millis(attempt as u64 * 250)).await;
            }
        }
        Err(format!(
            "Vault authentication failed after {} attempts: {}",
            config.max_retries, last_error
        ))
    }

    pub async fn read<T: DeserializeOwned>(&self, path: &str) -> Result<T, String> {
        let mut last_error = "no Vault read attempt".to_owned();
        for attempt in 1..=self.max_retries {
            match self
                .http
                .get(format!("{}/v1/{}", self.addr, path.trim_start_matches('/')))
                .header("X-Vault-Token", &self.token)
                .send()
                .await
            {
                Ok(response) if response.status().is_success() => {
                    let response: KvResponse = response
                        .json()
                        .await
                        .map_err(|error| format!("decode Vault KV response: {error}"))?;
                    return serde_json::from_value(response.data.data)
                        .map_err(|error| format!("decode Vault connection record: {error}"));
                }
                Ok(response) => {
                    let status = response.status();
                    if status != reqwest::StatusCode::TOO_MANY_REQUESTS && !status.is_server_error()
                    {
                        return Err(format!("Vault read returned HTTP {status}"));
                    }
                    last_error = format!("Vault read returned HTTP {status}");
                }
                Err(error) => last_error = format!("Vault read request: {error}"),
            }
            if attempt < self.max_retries {
                tokio::time::sleep(Duration::from_millis(attempt as u64 * 250)).await;
            }
        }
        Err(format!(
            "Vault read failed after {} attempts: {}",
            self.max_retries, last_error
        ))
    }

    async fn authenticate(http: &HttpClient, config: &VaultConfig) -> Result<String, String> {
        if let Some(token) = config
            .token
            .as_deref()
            .filter(|token| !token.trim().is_empty())
        {
            return Ok(token.to_owned());
        }
        let (path, body) = match (
            config.role_id.as_deref(),
            config.secret_id.as_deref(),
            config.kubernetes_role.as_deref(),
        ) {
            (Some(role_id), Some(secret_id), _) => (
                "auth/approle/login",
                serde_json::json!({"role_id": role_id, "secret_id": secret_id}),
            ),
            (_, _, Some(role)) => {
                let jwt = fs::read_to_string(&config.kubernetes_jwt_path)
                    .map_err(|error| format!("read Kubernetes Vault JWT: {error}"))?;
                (
                    "auth/kubernetes/login",
                    serde_json::json!({"role": role, "jwt": jwt.trim()}),
                )
            }
            _ => return Err("no Vault token, AppRole, or Kubernetes auth configured".to_owned()),
        };
        let response = http
            .post(format!("{}/v1/{path}", config.addr.trim_end_matches('/')))
            .json(&body)
            .send()
            .await
            .map_err(|error| format!("Vault authentication request: {error}"))?;
        if !response.status().is_success() {
            return Err(format!(
                "Vault authentication returned HTTP {}",
                response.status()
            ));
        }
        let value: serde_json::Value = response
            .json()
            .await
            .map_err(|error| format!("decode Vault authentication response: {error}"))?;
        value["auth"]["client_token"]
            .as_str()
            .filter(|token| !token.trim().is_empty())
            .map(str::to_owned)
            .ok_or_else(|| "Vault authentication returned an empty client token".to_owned())
    }
}
