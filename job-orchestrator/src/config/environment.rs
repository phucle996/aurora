use std::collections::HashMap;
use std::ffi::OsString;
use std::fmt;
use std::path::PathBuf;
use std::str::FromStr;

/// Immutable startup snapshot. Parsing every domain from the same snapshot
/// prevents a test/operator mutation from producing a mixed configuration.
pub(crate) struct Environment {
    values: HashMap<String, String>,
}

impl Environment {
    pub(crate) fn capture() -> Self {
        let values = std::env::vars_os()
            .filter_map(|(name, value)| Some((utf8(name)?, utf8(value)?)))
            .collect();
        Self { values }
    }

    #[cfg(test)]
    pub(crate) fn from_pairs(pairs: &[(&str, &str)]) -> Self {
        Self {
            values: pairs
                .iter()
                .map(|(name, value)| ((*name).to_owned(), (*value).to_owned()))
                .collect(),
        }
    }

    pub(crate) fn required(&self, name: &str) -> Result<String, String> {
        self.optional(name)
            .ok_or_else(|| format!("{name} must be set and non-empty"))
    }

    pub(crate) fn optional(&self, name: &str) -> Option<String> {
        self.values
            .get(name)
            .map(|value| value.trim().to_owned())
            .filter(|value| !value.is_empty())
    }

    pub(crate) fn optional_path(&self, name: &str) -> Option<PathBuf> {
        self.optional(name).map(PathBuf::from)
    }

    pub(crate) fn required_path(&self, name: &str) -> Result<PathBuf, String> {
        self.required(name).map(PathBuf::from)
    }

    pub(crate) fn required_enum<T>(&self, name: &str) -> Result<T, String>
    where
        T: FromStr<Err = String>,
    {
        self.required(name)?.parse()
    }

    pub(crate) fn required_bool(&self, name: &str) -> Result<bool, String> {
        parse_bool(name, &self.required(name)?)
    }

    pub(crate) fn optional_bool(&self, name: &str, default: bool) -> Result<bool, String> {
        self.optional(name)
            .map(|value| parse_bool(name, &value))
            .unwrap_or(Ok(default))
    }

    pub(crate) fn bounded<T>(&self, name: &str, default: T, min: T, max: T) -> Result<T, String>
    where
        T: FromStr + PartialOrd + Copy + fmt::Display,
    {
        let value = self
            .optional(name)
            .map(|value| {
                value
                    .parse::<T>()
                    .map_err(|_| format!("{name} must be a valid number"))
            })
            .transpose()?
            .unwrap_or(default);
        validate_range(name, value, min, max)
    }

    pub(crate) fn required_bounded<T>(&self, name: &str, min: T, max: T) -> Result<T, String>
    where
        T: FromStr + PartialOrd + Copy + fmt::Display,
    {
        let value = self
            .required(name)?
            .parse::<T>()
            .map_err(|_| format!("{name} must be a valid number"))?;
        validate_range(name, value, min, max)
    }

    pub(crate) fn bounded_f64(
        &self,
        name: &str,
        default: f64,
        min: f64,
        max: f64,
    ) -> Result<f64, String> {
        let value = self.bounded(name, default, min, max)?;
        if !value.is_finite() {
            return Err(format!("{name} must be finite"));
        }
        Ok(value)
    }

    pub(crate) fn reject_present(&self, names: &[&str], context: &str) -> Result<(), String> {
        if let Some(name) = names.iter().find(|name| self.optional(name).is_some()) {
            return Err(format!("{name} cannot be set when {context}"));
        }
        Ok(())
    }
}

pub(crate) fn normalized(value: &str) -> String {
    value.trim().to_ascii_lowercase()
}

pub(crate) fn validate_identifier(name: &str, value: &str) -> Result<(), String> {
    if value.trim().is_empty() || value.len() > 128 || value.chars().any(char::is_control) {
        return Err(format!(
            "{name} must be non-empty, at most 128 characters and contain no control characters"
        ));
    }
    Ok(())
}

fn parse_bool(name: &str, value: &str) -> Result<bool, String> {
    match normalized(value).as_str() {
        "1" | "true" | "yes" | "on" => Ok(true),
        "0" | "false" | "no" | "off" => Ok(false),
        _ => Err(format!("{name} must be a boolean")),
    }
}

fn validate_range<T>(name: &str, value: T, min: T, max: T) -> Result<T, String>
where
    T: PartialOrd + Copy + fmt::Display,
{
    if value < min || value > max {
        return Err(format!("{name} must be between {min} and {max}"));
    }
    Ok(value)
}

fn utf8(value: OsString) -> Option<String> {
    value.into_string().ok()
}

#[cfg(test)]
mod tests {
    use super::Environment;

    #[test]
    fn required_values_do_not_fallback_to_defaults() {
        let environment = Environment::from_pairs(&[("PRESENT", "value")]);
        assert_eq!(environment.required("PRESENT").unwrap(), "value");
        assert!(environment.required("MISSING").is_err());
    }

    #[test]
    fn bounded_defaults_are_reserved_for_tuning_values() {
        let environment = Environment::from_pairs(&[]);
        assert_eq!(
            environment.bounded("TIMEOUT_MS", 500_u64, 100, 1_000),
            Ok(500)
        );
        assert!(environment
            .required_bounded::<u64>("REPLICA_ACKS", 0, 5)
            .is_err());
    }
}
