use std::{ffi::OsStr, fmt};

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum NodeupCommand {
    Toolchain,
    Default,
    Show,
    Update,
    Check,
    Override,
    Which,
    Run,
    SelfCmd,
    Completions,
}

impl NodeupCommand {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Toolchain => "toolchain",
            Self::Default => "default",
            Self::Show => "show",
            Self::Update => "update",
            Self::Check => "check",
            Self::Override => "override",
            Self::Which => "which",
            Self::Run => "run",
            Self::SelfCmd => "self",
            Self::Completions => "completions",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum NodeupToolchainCommand {
    List,
    Install,
    Uninstall,
    Link,
}

impl NodeupToolchainCommand {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Install => "install",
            Self::Uninstall => "uninstall",
            Self::Link => "link",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum NodeupShowCommand {
    ActiveRuntime,
    Home,
}

impl NodeupShowCommand {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::ActiveRuntime => "active-runtime",
            Self::Home => "home",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum NodeupOverrideCommand {
    List,
    Set,
    Unset,
}

impl NodeupOverrideCommand {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Set => "set",
            Self::Unset => "unset",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum NodeupSelfCommand {
    Update,
    Uninstall,
    UpgradeData,
}

impl NodeupSelfCommand {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Update => "update",
            Self::Uninstall => "uninstall",
            Self::UpgradeData => "upgrade-data",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum NodeupChannel {
    Lts,
    Current,
    Latest,
}

impl fmt::Display for NodeupChannel {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let value = match self {
            Self::Lts => "lts",
            Self::Current => "current",
            Self::Latest => "latest",
        };
        write!(f, "{value}")
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum RuntimeSelectorSource {
    Explicit,
    Override,
    Default,
}

impl RuntimeSelectorSource {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Explicit => "explicit",
            Self::Override => "override",
            Self::Default => "default",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum OverrideLookupFallbackReason {
    OverrideMatched,
    FallbackToDefault,
    NoDefaultSelector,
}

impl OverrideLookupFallbackReason {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::OverrideMatched => "override-matched",
            Self::FallbackToDefault => "fallback-to-default",
            Self::NoDefaultSelector => "no-default-selector",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum PlatformTarget {
    DarwinX64,
    DarwinArm64,
    LinuxX64,
    LinuxArm64,
}

impl PlatformTarget {
    pub fn archive_segment(self) -> &'static str {
        match self {
            Self::DarwinX64 => "darwin-x64",
            Self::DarwinArm64 => "darwin-arm64",
            Self::LinuxX64 => "linux-x64",
            Self::LinuxArm64 => "linux-arm64",
        }
    }

    pub fn from_host() -> Option<Self> {
        if let Ok(value) = std::env::var("NODEUP_FORCE_PLATFORM") {
            return Self::from_forced(&value);
        }

        match (std::env::consts::OS, std::env::consts::ARCH) {
            ("macos", "x86_64") => Some(Self::DarwinX64),
            ("macos", "aarch64") => Some(Self::DarwinArm64),
            ("linux", "x86_64") => Some(Self::LinuxX64),
            ("linux", "aarch64") => Some(Self::LinuxArm64),
            _ => None,
        }
    }

    pub fn from_forced(value: &str) -> Option<Self> {
        match value {
            "darwin-x64" => Some(Self::DarwinX64),
            "darwin-arm64" => Some(Self::DarwinArm64),
            "linux-x64" => Some(Self::LinuxX64),
            "linux-arm64" => Some(Self::LinuxArm64),
            _ => None,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ManagedAlias {
    Node,
    Npm,
    Npx,
}

impl ManagedAlias {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Node => "node",
            Self::Npm => "npm",
            Self::Npx => "npx",
        }
    }

    pub fn from_argv0(argv0: &OsStr) -> Option<Self> {
        let basename = std::path::Path::new(argv0)
            .file_name()
            .and_then(|part| part.to_str())?;

        match basename {
            "node" => Some(Self::Node),
            "npm" => Some(Self::Npm),
            "npx" => Some(Self::Npx),
            _ => None,
        }
    }
}
