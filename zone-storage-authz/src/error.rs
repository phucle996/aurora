#[derive(Debug)]
pub enum AuthzError {
    Configuration(String),
    Dependency(String),
    Denied(&'static str),
}

impl std::fmt::Display for AuthzError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Configuration(message) | Self::Dependency(message) => {
                formatter.write_str(message)
            }
            Self::Denied(code) => formatter.write_str(code),
        }
    }
}

impl std::error::Error for AuthzError {}
