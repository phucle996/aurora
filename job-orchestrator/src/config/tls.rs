use super::environment::{normalized, Environment};
use std::path::PathBuf;
use std::str::FromStr;

#[derive(Clone, Copy, Eq, PartialEq)]
pub enum TlsTrustSource {
    System,
    File,
}

impl FromStr for TlsTrustSource {
    type Err = String;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        match normalized(value).as_str() {
            "system" => Ok(Self::System),
            "file" => Ok(Self::File),
            _ => Err("TLS trust source must be system or file".to_owned()),
        }
    }
}

#[derive(Clone, Copy, Eq, PartialEq)]
pub enum TlsClientAuth {
    None,
    Mutual,
}

impl FromStr for TlsClientAuth {
    type Err = String;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        match normalized(value).as_str() {
            "none" => Ok(Self::None),
            "mutual" => Ok(Self::Mutual),
            _ => Err("TLS client auth must be none or mutual".to_owned()),
        }
    }
}

/// Explicit trust and workload identity for one downstream. Deliberately no
/// Debug implementation: private-key paths must not leak into bootstrap logs.
#[derive(Clone)]
pub struct TlsClientConfig {
    pub trust_source: TlsTrustSource,
    pub ca_cert: Option<PathBuf>,
    pub client_cert: Option<PathBuf>,
    pub client_key: Option<PathBuf>,
}

impl TlsClientConfig {
    pub(crate) fn load(
        environment: &Environment,
        prefix: &str,
        ca_name: &str,
        client_cert_name: &str,
        client_key_name: &str,
    ) -> Result<Self, String> {
        let trust_name = format!("{prefix}_TRUST_SOURCE");
        let client_auth_name = format!("{prefix}_CLIENT_AUTH");
        let trust_source = environment.required_enum(&trust_name)?;
        let client_auth = environment.required_enum(&client_auth_name)?;

        let ca_cert = match trust_source {
            TlsTrustSource::System => {
                environment.reject_present(&[ca_name], &format!("{trust_name}=system"))?;
                None
            }
            TlsTrustSource::File => Some(environment.required_path(ca_name)?),
        };

        let (client_cert, client_key) = match client_auth {
            TlsClientAuth::None => {
                environment.reject_present(
                    &[client_cert_name, client_key_name],
                    &format!("{client_auth_name}=none"),
                )?;
                (None, None)
            }
            TlsClientAuth::Mutual => (
                Some(environment.required_path(client_cert_name)?),
                Some(environment.required_path(client_key_name)?),
            ),
        };

        Ok(Self {
            trust_source,
            ca_cert,
            client_cert,
            client_key,
        })
    }

    pub(crate) fn ensure_absent(
        environment: &Environment,
        prefix: &str,
        ca_name: &str,
        client_cert_name: &str,
        client_key_name: &str,
        context: &str,
    ) -> Result<(), String> {
        let trust_name = format!("{prefix}_TRUST_SOURCE");
        let client_auth_name = format!("{prefix}_CLIENT_AUTH");
        environment.reject_present(
            &[
                &trust_name,
                &client_auth_name,
                ca_name,
                client_cert_name,
                client_key_name,
            ],
            context,
        )
    }
}

#[cfg(test)]
mod tests {
    use super::{TlsClientConfig, TlsTrustSource};
    use crate::config::environment::Environment;

    #[test]
    fn file_trust_and_mutual_auth_require_complete_material() {
        let missing_key = Environment::from_pairs(&[
            ("TEST_TLS_TRUST_SOURCE", "file"),
            ("TEST_TLS_CLIENT_AUTH", "mutual"),
            ("TEST_TLS_CA_CERT", "/ca.pem"),
            ("TEST_TLS_CLIENT_CERT", "/client.pem"),
        ]);
        assert!(TlsClientConfig::load(
            &missing_key,
            "TEST_TLS",
            "TEST_TLS_CA_CERT",
            "TEST_TLS_CLIENT_CERT",
            "TEST_TLS_CLIENT_KEY",
        )
        .is_err());

        let complete = Environment::from_pairs(&[
            ("TEST_TLS_TRUST_SOURCE", "file"),
            ("TEST_TLS_CLIENT_AUTH", "mutual"),
            ("TEST_TLS_CA_CERT", "/ca.pem"),
            ("TEST_TLS_CLIENT_CERT", "/client.pem"),
            ("TEST_TLS_CLIENT_KEY", "/client.key"),
        ]);
        let config = TlsClientConfig::load(
            &complete,
            "TEST_TLS",
            "TEST_TLS_CA_CERT",
            "TEST_TLS_CLIENT_CERT",
            "TEST_TLS_CLIENT_KEY",
        )
        .unwrap();
        assert!(config.trust_source == TlsTrustSource::File);
        assert!(config.client_cert.is_some());
    }

    #[test]
    fn system_trust_rejects_silent_ca_override() {
        let environment = Environment::from_pairs(&[
            ("TEST_TLS_TRUST_SOURCE", "system"),
            ("TEST_TLS_CLIENT_AUTH", "none"),
            ("TEST_TLS_CA_CERT", "/unexpected.pem"),
        ]);
        assert!(TlsClientConfig::load(
            &environment,
            "TEST_TLS",
            "TEST_TLS_CA_CERT",
            "TEST_TLS_CLIENT_CERT",
            "TEST_TLS_CLIENT_KEY",
        )
        .is_err());
    }
}
