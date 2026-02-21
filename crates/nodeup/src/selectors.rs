use std::fmt;

use semver::Version;
use serde::{Deserialize, Serialize};

use crate::{
    errors::{NodeupError, Result},
    types::NodeupChannel,
};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "kind", content = "value", rename_all = "kebab-case")]
pub enum RuntimeSelector {
    Version(Version),
    Channel(NodeupChannel),
    LinkedName(String),
}

impl RuntimeSelector {
    pub fn parse(input: &str) -> Result<Self> {
        let normalized = input.trim();
        if normalized.is_empty() {
            return Err(NodeupError::invalid_input(
                "Runtime selector cannot be empty",
            ));
        }

        match normalized {
            "lts" => return Ok(Self::Channel(NodeupChannel::Lts)),
            "current" => return Ok(Self::Channel(NodeupChannel::Current)),
            "latest" => return Ok(Self::Channel(NodeupChannel::Latest)),
            _ => {}
        }

        if let Some(stripped) = normalized.strip_prefix('v') {
            if let Ok(version) = Version::parse(stripped) {
                return Ok(Self::Version(version));
            }
        }

        if let Ok(version) = Version::parse(normalized) {
            return Ok(Self::Version(version));
        }

        if !is_valid_linked_name(normalized) {
            return Err(NodeupError::invalid_input(format!(
                "Invalid selector '{normalized}'. Expected semantic version, channel, or linked \
                 runtime name"
            )));
        }

        Ok(Self::LinkedName(normalized.to_string()))
    }

    pub fn stable_id(&self) -> String {
        match self {
            Self::Version(version) => format!("v{version}"),
            Self::Channel(channel) => channel.to_string(),
            Self::LinkedName(name) => name.clone(),
        }
    }

    pub fn is_version(&self) -> bool {
        matches!(self, Self::Version(_))
    }
}

impl fmt::Display for RuntimeSelector {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Version(version) => write!(f, "v{version}"),
            Self::Channel(channel) => write!(f, "{channel}"),
            Self::LinkedName(name) => write!(f, "{name}"),
        }
    }
}

pub fn is_valid_linked_name(input: &str) -> bool {
    let mut chars = input.chars();
    match chars.next() {
        Some(c) if c.is_ascii_alphanumeric() => {}
        _ => return false,
    }

    chars.all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_')
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::NodeupChannel;

    #[test]
    fn parse_channels() {
        assert_eq!(
            RuntimeSelector::parse("lts").unwrap(),
            RuntimeSelector::Channel(NodeupChannel::Lts)
        );
        assert_eq!(
            RuntimeSelector::parse("latest").unwrap(),
            RuntimeSelector::Channel(NodeupChannel::Latest)
        );
    }

    #[test]
    fn parse_versions_with_or_without_prefix() {
        let selector = RuntimeSelector::parse("22.1.0").unwrap();
        assert!(matches!(selector, RuntimeSelector::Version(_)));

        let selector = RuntimeSelector::parse("v22.1.0").unwrap();
        assert!(matches!(selector, RuntimeSelector::Version(_)));
    }

    #[test]
    fn parse_linked_name() {
        assert_eq!(
            RuntimeSelector::parse("my-node").unwrap(),
            RuntimeSelector::LinkedName("my-node".to_string())
        );
    }
}
