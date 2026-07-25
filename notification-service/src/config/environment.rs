use std::collections::HashMap;

/// Captures the process environment once so all configuration domains observe
/// one coherent startup snapshot instead of racing with later env mutations.
#[derive(Clone, Debug)]
pub struct Environment {
    values: HashMap<String, String>,
}

impl Environment {
    pub fn capture() -> Self {
        Self {
            values: std::env::vars().collect(),
        }
    }

    pub fn required(&self, key: &'static str) -> Result<String, ConfigError> {
        self.values
            .get(key)
            .filter(|value| !value.trim().is_empty())
            .cloned()
            .ok_or(ConfigError::Missing(key))
    }

    pub fn optional(&self, key: &'static str) -> Option<&str> {
        self.values
            .get(key)
            .map(String::as_str)
            .filter(|value| !value.trim().is_empty())
    }

    pub fn bounded_u64(
        &self,
        key: &'static str,
        default: u64,
        min: u64,
        max: u64,
    ) -> Result<u64, ConfigError> {
        let value = self
            .optional(key)
            .map(|raw| {
                raw.parse::<u64>()
                    .map_err(|_| ConfigError::InvalidNumber(key))
            })
            .transpose()?
            .unwrap_or(default);
        if value < min || value > max {
            return Err(ConfigError::OutOfRange { key, min, max });
        }
        Ok(value)
    }

    pub fn bounded_usize(
        &self,
        key: &'static str,
        default: usize,
        min: usize,
        max: usize,
    ) -> Result<usize, ConfigError> {
        let value = self
            .optional(key)
            .map(|raw| {
                raw.parse::<usize>()
                    .map_err(|_| ConfigError::InvalidNumber(key))
            })
            .transpose()?
            .unwrap_or(default);
        if value < min || value > max {
            return Err(ConfigError::OutOfRange {
                key,
                min: min as u64,
                max: max as u64,
            });
        }
        Ok(value)
    }

    pub fn required_bool(&self, key: &'static str) -> Result<bool, ConfigError> {
        match self.required(key)?.as_str() {
            "true" | "1" => Ok(true),
            "false" | "0" => Ok(false),
            _ => Err(ConfigError::InvalidValue(key)),
        }
    }
}

#[derive(Debug)]
pub enum ConfigError {
    Missing(&'static str),
    InvalidNumber(&'static str),
    InvalidValue(&'static str),
    OutOfRange {
        key: &'static str,
        min: u64,
        max: u64,
    },
}

impl std::fmt::Display for ConfigError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Missing(key) => {
                write!(formatter, "required environment variable {key} is missing")
            }
            Self::InvalidNumber(key) => {
                write!(formatter, "environment variable {key} is not a number")
            }
            Self::InvalidValue(key) => {
                write!(formatter, "environment variable {key} has an invalid value")
            }
            Self::OutOfRange { key, min, max } => {
                write!(
                    formatter,
                    "environment variable {key} must be in range {min}..={max}"
                )
            }
        }
    }
}

impl std::error::Error for ConfigError {}
