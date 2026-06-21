use std::{collections::BTreeMap, ffi::OsStr, fmt};

use serde::{Deserialize, Serialize};
use serde_json::json;

use crate::errors::{ErrorDiagnostics, NodeupError};

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
pub enum ArchiveFormat {
    TarXz,
    Zip,
}

impl ArchiveFormat {
    pub fn extension(self) -> &'static str {
        match self {
            Self::TarXz => "tar.xz",
            Self::Zip => "zip",
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
    WindowsX64,
    WindowsArm64,
}

pub const SUPPORTED_PLATFORM_PAIRS: &[&str] = &[
    "macos/x64",
    "macos/arm64",
    "linux/x64",
    "linux/arm64",
    "windows/x64",
    "windows/arm64",
];

pub const SUPPORTED_PLATFORM_GUIDANCE: &str = "Nodeup supports macOS x64, macOS arm64, Linux x64, \
                                               Linux arm64, Windows x64, and Windows arm64 hosts. \
                                               x86 hosts are unsupported.";

pub const UNSUPPORTED_PLATFORM_HINT: &str = "Use an x64/arm64 host or a supported CI image: macOS \
                                             x64/arm64, Linux x64/arm64, or Windows x64/arm64.";

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct PlatformDiagnostic {
    pub os: String,
    pub architecture: String,
    pub platform_source: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub forced_platform: Option<String>,
    pub supported_platforms: Vec<String>,
}

impl PlatformDiagnostic {
    pub fn into_error_diagnostics(self) -> ErrorDiagnostics {
        let mut diagnostics = BTreeMap::new();
        diagnostics.insert("os".to_string(), json!(self.os));
        diagnostics.insert("architecture".to_string(), json!(self.architecture));
        diagnostics.insert("platform_source".to_string(), json!(self.platform_source));
        if let Some(forced_platform) = self.forced_platform {
            diagnostics.insert("forced_platform".to_string(), json!(forced_platform));
        }
        diagnostics.insert(
            "supported_platforms".to_string(),
            json!(SUPPORTED_PLATFORM_PAIRS),
        );
        diagnostics
    }
}

impl PlatformTarget {
    pub fn archive_segment(self) -> &'static str {
        match self {
            Self::DarwinX64 => "darwin-x64",
            Self::DarwinArm64 => "darwin-arm64",
            Self::LinuxX64 => "linux-x64",
            Self::LinuxArm64 => "linux-arm64",
            Self::WindowsX64 => "win-x64",
            Self::WindowsArm64 => "win-arm64",
        }
    }

    pub fn archive_format(self) -> ArchiveFormat {
        match self {
            Self::DarwinX64 | Self::DarwinArm64 | Self::LinuxX64 | Self::LinuxArm64 => {
                ArchiveFormat::TarXz
            }
            Self::WindowsX64 | Self::WindowsArm64 => ArchiveFormat::Zip,
        }
    }

    pub fn from_host() -> Option<Self> {
        Self::from_host_result().ok()
    }

    pub fn from_host_result() -> std::result::Result<Self, PlatformDiagnostic> {
        if let Ok(value) = std::env::var("NODEUP_FORCE_PLATFORM") {
            if let Some(target) = Self::from_forced(&value) {
                return Ok(target);
            }
            let (os, architecture) = parse_forced_platform_diagnostic(&value);
            return Err(PlatformDiagnostic {
                os,
                architecture,
                platform_source: "NODEUP_FORCE_PLATFORM".to_string(),
                forced_platform: Some(value),
                supported_platforms: SUPPORTED_PLATFORM_PAIRS
                    .iter()
                    .map(|value| (*value).to_string())
                    .collect(),
            });
        }

        match (std::env::consts::OS, std::env::consts::ARCH) {
            ("macos", "x86_64") => Ok(Self::DarwinX64),
            ("macos", "aarch64") => Ok(Self::DarwinArm64),
            ("linux", "x86_64") => Ok(Self::LinuxX64),
            ("linux", "aarch64") => Ok(Self::LinuxArm64),
            ("windows", "x86_64") => Ok(Self::WindowsX64),
            ("windows", "aarch64") => Ok(Self::WindowsArm64),
            (os, architecture) => Err(PlatformDiagnostic {
                os: os.to_string(),
                architecture: architecture.to_string(),
                platform_source: "host".to_string(),
                forced_platform: None,
                supported_platforms: SUPPORTED_PLATFORM_PAIRS
                    .iter()
                    .map(|value| (*value).to_string())
                    .collect(),
            }),
        }
    }

    pub fn from_forced(value: &str) -> Option<Self> {
        match value {
            "darwin-x64" => Some(Self::DarwinX64),
            "darwin-arm64" => Some(Self::DarwinArm64),
            "linux-x64" => Some(Self::LinuxX64),
            "linux-arm64" => Some(Self::LinuxArm64),
            "windows-x64" => Some(Self::WindowsX64),
            "windows-arm64" => Some(Self::WindowsArm64),
            _ => None,
        }
    }

    pub fn ensure_supported_host(action: &str) -> std::result::Result<Self, NodeupError> {
        Self::from_host_result()
            .map_err(|diagnostic| unsupported_platform_error(action, diagnostic))
    }
}

fn parse_forced_platform_diagnostic(value: &str) -> (String, String) {
    if let Some((os, architecture)) = value.split_once('-') {
        (os.to_string(), architecture.to_string())
    } else {
        ("unknown".to_string(), value.to_string())
    }
}

fn unsupported_platform_error(action: &str, diagnostic: PlatformDiagnostic) -> NodeupError {
    let host = format!("{}/{}", diagnostic.os, diagnostic.architecture);
    let forced = diagnostic
        .forced_platform
        .as_ref()
        .map(|value| format!(", forced_platform={value}"))
        .unwrap_or_default();
    let x86_context = if is_x86_architecture(&diagnostic.architecture) {
        " x86 hosts are unsupported."
    } else {
        ""
    };
    NodeupError::unsupported_platform_with_diagnostics(
        format!(
            "Unsupported host platform for {action}. host={host}, \
             platform_source={}{}.{x86_context} {SUPPORTED_PLATFORM_GUIDANCE}",
            diagnostic.platform_source, forced
        ),
        UNSUPPORTED_PLATFORM_HINT,
        diagnostic.into_error_diagnostics(),
    )
}

fn is_x86_architecture(architecture: &str) -> bool {
    matches!(
        architecture.to_ascii_lowercase().as_str(),
        "x86" | "i386" | "i486" | "i586" | "i686" | "ia32" | "386"
    )
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ManagedAlias {
    Node,
    Npm,
    Npx,
    Yarn,
    Pnpm,
}

impl ManagedAlias {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Node => "node",
            Self::Npm => "npm",
            Self::Npx => "npx",
            Self::Yarn => "yarn",
            Self::Pnpm => "pnpm",
        }
    }

    pub fn from_argv0(argv0: &OsStr) -> Option<Self> {
        let path = std::path::Path::new(argv0);
        let basename = path.file_name().and_then(|part| part.to_str())?;
        let alias_name = if path
            .extension()
            .and_then(|part| part.to_str())
            .is_some_and(|extension| extension.eq_ignore_ascii_case("exe"))
        {
            path.file_stem().and_then(|part| part.to_str())?
        } else {
            basename
        };

        match alias_name {
            "node" => Some(Self::Node),
            "npm" => Some(Self::Npm),
            "npx" => Some(Self::Npx),
            "yarn" => Some(Self::Yarn),
            "pnpm" => Some(Self::Pnpm),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{ManagedAlias, PlatformTarget};

    #[test]
    fn managed_alias_from_argv0_accepts_extensionless_and_windows_exe_aliases() {
        assert_eq!(
            ManagedAlias::from_argv0("node".as_ref()),
            Some(ManagedAlias::Node)
        );
        assert_eq!(
            ManagedAlias::from_argv0("node.exe".as_ref()),
            Some(ManagedAlias::Node)
        );
        assert_eq!(
            ManagedAlias::from_argv0("/Users/alice/bin/npm.exe".as_ref()),
            Some(ManagedAlias::Npm)
        );
        assert_eq!(ManagedAlias::from_argv0("nodeup.exe".as_ref()), None);
    }

    #[test]
    fn platform_target_maps_supported_forced_platforms() {
        assert_eq!(
            PlatformTarget::from_forced("darwin-x64"),
            Some(PlatformTarget::DarwinX64)
        );
        assert_eq!(
            PlatformTarget::from_forced("darwin-arm64"),
            Some(PlatformTarget::DarwinArm64)
        );
        assert_eq!(
            PlatformTarget::from_forced("linux-x64"),
            Some(PlatformTarget::LinuxX64)
        );
        assert_eq!(
            PlatformTarget::from_forced("linux-arm64"),
            Some(PlatformTarget::LinuxArm64)
        );
        assert_eq!(
            PlatformTarget::from_forced("windows-x64"),
            Some(PlatformTarget::WindowsX64)
        );
        assert_eq!(
            PlatformTarget::from_forced("windows-arm64"),
            Some(PlatformTarget::WindowsArm64)
        );
    }

    #[test]
    fn platform_target_rejects_forced_x86_platforms() {
        assert_eq!(PlatformTarget::from_forced("linux-x86"), None);
        assert_eq!(PlatformTarget::from_forced("windows-x86"), None);
    }
}
