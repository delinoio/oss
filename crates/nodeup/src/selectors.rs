use std::fmt;

use semver::Version;
use serde::{Deserialize, Serialize};

use crate::{
    errors::{NodeupError, Result},
    types::NodeupChannel,
};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum RuntimeSelectorKind {
    ExactVersion,
    Channel,
    LinkedRuntime,
}

impl RuntimeSelectorKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::ExactVersion => "exact-version",
            Self::Channel => "channel",
            Self::LinkedRuntime => "linked-runtime",
        }
    }
}

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
            return Err(NodeupError::invalid_input_with_hint(
                format!(
                    "Runtime selector cannot be empty (raw_input_len={}, trimmed_len={})",
                    input.len(),
                    normalized.len()
                ),
                "Provide a selector like `22.1.0`, `v22.1.0`, `lts`, `current`, `latest`, or a \
                 linked runtime name.",
            ));
        }

        if let Some(channel) = reserved_channel_selector(normalized) {
            return Ok(Self::Channel(channel));
        }

        if is_case_variant_of_reserved_channel_selector(normalized) {
            return Err(NodeupError::invalid_input_with_hint(
                format!("Invalid runtime selector '{normalized}'"),
                "Reserved channel selectors are case-sensitive; use `lts`, `current`, or \
                 `latest`, or choose a linked runtime name that does not differ from a reserved \
                 channel selector only by case.",
            ));
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
            return Err(NodeupError::invalid_input_with_hint(
                format!("Invalid runtime selector '{normalized}'"),
                "Use a semantic version (`22.1.0`), channel (`lts|current|latest`), or a linked \
                 runtime name ([A-Za-z0-9][A-Za-z0-9_-]*).",
            ));
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

    pub fn canonical_id(&self) -> String {
        match self {
            Self::Version(version) => format!("v{version}"),
            Self::Channel(channel) => channel.canonical_selector().to_string(),
            Self::LinkedName(name) => name.clone(),
        }
    }

    pub fn kind(&self) -> RuntimeSelectorKind {
        match self {
            Self::Version(_) => RuntimeSelectorKind::ExactVersion,
            Self::Channel(_) => RuntimeSelectorKind::Channel,
            Self::LinkedName(_) => RuntimeSelectorKind::LinkedRuntime,
        }
    }

    pub fn alias_of(&self) -> Option<String> {
        match self {
            Self::Channel(channel) => channel.alias_of().map(str::to_string),
            _ => None,
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

pub fn reserved_channel_selector(input: &str) -> Option<NodeupChannel> {
    match input {
        "lts" => Some(NodeupChannel::Lts),
        "current" => Some(NodeupChannel::Current),
        "latest" => Some(NodeupChannel::Latest),
        _ => None,
    }
}

pub fn is_reserved_channel_selector_token(input: &str) -> bool {
    reserved_channel_selector(input).is_some()
}

pub fn is_case_variant_of_reserved_channel_selector(input: &str) -> bool {
    reserved_channel_selector(input).is_none()
        && matches!(
            input.to_ascii_lowercase().as_str(),
            "lts" | "current" | "latest"
        )
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

    #[test]
    fn reserved_channel_selector_token_detection_is_exact_and_case_sensitive() {
        assert!(is_reserved_channel_selector_token("lts"));
        assert!(is_reserved_channel_selector_token("current"));
        assert!(is_reserved_channel_selector_token("latest"));

        assert!(!is_reserved_channel_selector_token("LTS"));
        assert!(!is_reserved_channel_selector_token("Latest"));
        assert!(!is_reserved_channel_selector_token("node-lts"));
    }

    #[test]
    fn selector_metadata_identifies_canonical_aliases() {
        let latest = RuntimeSelector::parse("latest").unwrap();
        assert_eq!(latest.kind(), RuntimeSelectorKind::Channel);
        assert_eq!(latest.stable_id(), "latest");
        assert_eq!(latest.canonical_id(), "current");
        assert_eq!(latest.alias_of().as_deref(), Some("current"));

        let current = RuntimeSelector::parse("current").unwrap();
        assert_eq!(current.canonical_id(), "current");
        assert_eq!(current.alias_of(), None);

        let version = RuntimeSelector::parse("22.1.0").unwrap();
        assert_eq!(version.kind(), RuntimeSelectorKind::ExactVersion);
        assert_eq!(version.canonical_id(), "v22.1.0");
    }

    #[test]
    fn detects_case_variants_of_reserved_channel_selectors() {
        assert!(is_case_variant_of_reserved_channel_selector("LTS"));
        assert!(is_case_variant_of_reserved_channel_selector("Current"));
        assert!(is_case_variant_of_reserved_channel_selector("LATEST"));

        assert!(!is_case_variant_of_reserved_channel_selector("lts"));
        assert!(!is_case_variant_of_reserved_channel_selector("node-lts"));
    }

    #[test]
    fn parse_rejects_case_variants_of_reserved_channel_selectors() {
        for selector in ["LTS", "Current", "LATEST"] {
            let error = RuntimeSelector::parse(selector).unwrap_err();
            assert_eq!(error.kind, crate::errors::ErrorKind::InvalidInput);
            assert!(error.message.contains("Reserved channel selectors"));
        }
    }
}
