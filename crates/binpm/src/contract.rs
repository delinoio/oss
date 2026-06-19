use std::{fmt, str::FromStr};

use serde::{Deserialize, Serialize};

use crate::error::BinpmError;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum Scope {
    #[serde(rename = "local")]
    Local,
    #[serde(rename = "global")]
    Global,
    #[serde(rename = "auto")]
    Auto,
}

impl Scope {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Local => "local",
            Self::Global => "global",
            Self::Auto => "auto",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SourceSpec {
    pub provider: SourceProvider,
    pub host: String,
    pub path: String,
    pub version: Option<String>,
}

impl SourceSpec {
    pub fn source_without_version(&self) -> String {
        match self.provider {
            SourceProvider::GitHub if self.host == "github.com" => {
                format!("github:{}", self.path)
            }
            SourceProvider::GitHub => format!("github:{}/{}", self.host, self.path),
            SourceProvider::GitLab => format!("gitlab:{}/{}", self.host, self.path),
        }
    }
}

impl fmt::Display for SourceSpec {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{}", self.source_without_version())?;
        if let Some(version) = &self.version {
            write!(formatter, "@{version}")?;
        }
        Ok(())
    }
}

impl FromStr for SourceSpec {
    type Err = BinpmError;

    fn from_str(raw: &str) -> Result<Self, Self::Err> {
        parse_source_spec(raw)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum SourceProvider {
    #[serde(rename = "github")]
    GitHub,
    #[serde(rename = "gitlab")]
    GitLab,
}

impl SourceProvider {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::GitHub => "github",
            Self::GitLab => "gitlab",
        }
    }
}

fn parse_source_spec(raw: &str) -> Result<SourceSpec, BinpmError> {
    let trimmed = raw.trim();
    let (provider_raw, remainder) = trimmed
        .split_once(':')
        .ok_or_else(|| invalid_source(raw, "missing provider prefix"))?;

    let (remainder, version) = split_version(remainder);

    match provider_raw {
        "github" => parse_github_source(raw, remainder, version),
        "gitlab" => parse_gitlab_source(raw, remainder, version),
        _ => Err(invalid_source(raw, "provider must be `github` or `gitlab`")),
    }
}

fn parse_github_source(
    raw: &str,
    remainder: &str,
    version: Option<String>,
) -> Result<SourceSpec, BinpmError> {
    let segments = non_empty_segments(remainder);
    match segments.as_slice() {
        [owner, repo] => Ok(SourceSpec {
            provider: SourceProvider::GitHub,
            host: "github.com".to_string(),
            path: format!("{owner}/{repo}"),
            version,
        }),
        [host, owner, repo] => Ok(SourceSpec {
            provider: SourceProvider::GitHub,
            host: (*host).to_string(),
            path: format!("{owner}/{repo}"),
            version,
        }),
        _ => Err(invalid_source(
            raw,
            "github sources must be `github:owner/repo[@version]` or \
             `github:<host>/owner/repo[@version]`",
        )),
    }
}

fn parse_gitlab_source(
    raw: &str,
    remainder: &str,
    version: Option<String>,
) -> Result<SourceSpec, BinpmError> {
    let segments = non_empty_segments(remainder);
    if segments.len() < 3 {
        return Err(invalid_source(
            raw,
            "gitlab sources must be `gitlab:<host>/<namespace...>/<project>[@version]`",
        ));
    }

    let host = segments[0].to_string();
    let path = segments[1..].join("/");

    Ok(SourceSpec {
        provider: SourceProvider::GitLab,
        host,
        path,
        version,
    })
}

fn split_version(remainder: &str) -> (&str, Option<String>) {
    match remainder.rsplit_once('@') {
        Some((source, version)) if !source.is_empty() && !version.is_empty() => {
            (source, Some(version.to_string()))
        }
        _ => (remainder, None),
    }
}

fn non_empty_segments(raw: &str) -> Vec<&str> {
    raw.split('/')
        .filter(|segment| !segment.is_empty())
        .collect()
}

fn invalid_source(raw: &str, message: impl Into<String>) -> BinpmError {
    BinpmError::InvalidSourceSpec {
        raw: raw.to_string(),
        message: message.into(),
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct HostTarget {
    pub os: TargetOs,
    pub arch: TargetArch,
    pub libc: TargetLibc,
}

impl HostTarget {
    pub fn current() -> Self {
        Self {
            os: TargetOs::current(),
            arch: TargetArch::current(),
            libc: TargetLibc::current(),
        }
    }

    pub fn key(&self) -> String {
        format!(
            "{}-{}-{}",
            self.os.as_str(),
            self.arch.as_str(),
            self.libc.as_str()
        )
    }
}

impl FromStr for HostTarget {
    type Err = BinpmError;

    fn from_str(raw: &str) -> Result<Self, Self::Err> {
        let segments: Vec<_> = raw.split('-').collect();
        if segments.len() != 3 {
            return Err(BinpmError::InvalidTargetKey {
                raw: raw.to_string(),
            });
        }

        Ok(Self {
            os: TargetOs::from_alias(segments[0])?,
            arch: TargetArch::from_alias(segments[1])?,
            libc: TargetLibc::from_alias(segments[2])?,
        })
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum TargetOs {
    #[serde(rename = "linux")]
    Linux,
    #[serde(rename = "darwin")]
    Darwin,
    #[serde(rename = "windows")]
    Windows,
    #[serde(rename = "freebsd")]
    FreeBsd,
}

impl TargetOs {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Linux => "linux",
            Self::Darwin => "darwin",
            Self::Windows => "windows",
            Self::FreeBsd => "freebsd",
        }
    }

    fn current() -> Self {
        match std::env::consts::OS {
            "macos" => Self::Darwin,
            "windows" => Self::Windows,
            "freebsd" => Self::FreeBsd,
            _ => Self::Linux,
        }
    }

    fn from_alias(raw: &str) -> Result<Self, BinpmError> {
        match raw.to_ascii_lowercase().as_str() {
            "linux" => Ok(Self::Linux),
            "darwin" | "macos" | "mac" | "osx" => Ok(Self::Darwin),
            "windows" | "win" | "win32" => Ok(Self::Windows),
            "freebsd" => Ok(Self::FreeBsd),
            _ => Err(BinpmError::UnsupportedTargetComponent {
                component: "os",
                raw: raw.to_string(),
            }),
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum TargetArch {
    #[serde(rename = "x86_64")]
    X86_64,
    #[serde(rename = "aarch64")]
    Aarch64,
    #[serde(rename = "i686")]
    I686,
    #[serde(rename = "armv7")]
    Armv7,
}

impl TargetArch {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::X86_64 => "x86_64",
            Self::Aarch64 => "aarch64",
            Self::I686 => "i686",
            Self::Armv7 => "armv7",
        }
    }

    fn current() -> Self {
        match std::env::consts::ARCH {
            "aarch64" => Self::Aarch64,
            "x86" | "i686" => Self::I686,
            "arm" => Self::Armv7,
            _ => Self::X86_64,
        }
    }

    fn from_alias(raw: &str) -> Result<Self, BinpmError> {
        match raw.to_ascii_lowercase().as_str() {
            "x86_64" | "amd64" | "x64" => Ok(Self::X86_64),
            "aarch64" | "arm64" => Ok(Self::Aarch64),
            "i686" | "i386" | "x86" | "ia32" => Ok(Self::I686),
            "armv7" => Ok(Self::Armv7),
            _ => Err(BinpmError::UnsupportedTargetComponent {
                component: "architecture",
                raw: raw.to_string(),
            }),
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum TargetLibc {
    #[serde(rename = "gnu")]
    Gnu,
    #[serde(rename = "musl")]
    Musl,
    #[serde(rename = "msvc")]
    Msvc,
    #[serde(rename = "any")]
    Any,
}

impl TargetLibc {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Gnu => "gnu",
            Self::Musl => "musl",
            Self::Msvc => "msvc",
            Self::Any => "any",
        }
    }

    fn current() -> Self {
        if cfg!(target_env = "musl") {
            Self::Musl
        } else if cfg!(target_env = "msvc") {
            Self::Msvc
        } else if cfg!(target_os = "linux") {
            Self::Gnu
        } else {
            Self::Any
        }
    }

    fn from_alias(raw: &str) -> Result<Self, BinpmError> {
        match raw.to_ascii_lowercase().as_str() {
            "gnu" | "glibc" => Ok(Self::Gnu),
            "musl" | "alpine" => Ok(Self::Musl),
            "msvc" => Ok(Self::Msvc),
            "static" | "portable" | "universal" | "any" => Ok(Self::Any),
            _ => Err(BinpmError::UnsupportedTargetComponent {
                component: "libc",
                raw: raw.to_string(),
            }),
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum ChecksumSource {
    #[serde(rename = "github-digest")]
    GitHubDigest,
    #[serde(rename = "sidecar")]
    Sidecar,
    #[serde(rename = "manifest")]
    Manifest,
    #[serde(rename = "signature")]
    Signature,
    #[serde(rename = "local")]
    Local,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum ArchiveFormat {
    #[serde(rename = "tar.gz")]
    TarGz,
    #[serde(rename = "tgz")]
    Tgz,
    #[serde(rename = "tar.xz")]
    TarXz,
    #[serde(rename = "txz")]
    Txz,
    #[serde(rename = "tar.zst")]
    TarZst,
    #[serde(rename = "zip")]
    Zip,
    #[serde(rename = "bare-executable")]
    BareExecutable,
}

#[cfg(test)]
mod tests {
    use std::str::FromStr;

    use super::{
        ArchiveFormat, ChecksumSource, HostTarget, Scope, SourceProvider, SourceSpec, TargetArch,
        TargetLibc, TargetOs,
    };

    #[test]
    fn parses_github_dot_com_source_without_host() {
        let spec = SourceSpec::from_str("github:BurntSushi/ripgrep@14.1.1").expect("source");

        assert_eq!(spec.provider, SourceProvider::GitHub);
        assert_eq!(spec.host, "github.com");
        assert_eq!(spec.path, "BurntSushi/ripgrep");
        assert_eq!(spec.version.as_deref(), Some("14.1.1"));
        assert_eq!(spec.source_without_version(), "github:BurntSushi/ripgrep");
    }

    #[test]
    fn parses_github_enterprise_source() {
        let spec = SourceSpec::from_str("github:github.example.com/acme/tool").expect("source");

        assert_eq!(spec.provider, SourceProvider::GitHub);
        assert_eq!(spec.host, "github.example.com");
        assert_eq!(spec.path, "acme/tool");
        assert_eq!(spec.version, None);
    }

    #[test]
    fn parses_gitlab_nested_source() {
        let spec = SourceSpec::from_str("gitlab:gitlab.example.com/platform/tools/thing@v1.0.0")
            .expect("source");

        assert_eq!(spec.provider, SourceProvider::GitLab);
        assert_eq!(spec.host, "gitlab.example.com");
        assert_eq!(spec.path, "platform/tools/thing");
        assert_eq!(spec.version.as_deref(), Some("v1.0.0"));
    }

    #[test]
    fn normalizes_target_aliases() {
        let target = HostTarget::from_str("macos-arm64-universal").expect("target");

        assert_eq!(target.os, TargetOs::Darwin);
        assert_eq!(target.arch, TargetArch::Aarch64);
        assert_eq!(target.libc, TargetLibc::Any);
        assert_eq!(target.key(), "darwin-aarch64-any");
    }

    #[test]
    fn serializes_documented_contract_values() {
        assert_json_string(Scope::Local, "local");
        assert_json_string(SourceProvider::GitHub, "github");
        assert_json_string(TargetOs::FreeBsd, "freebsd");
        assert_json_string(TargetArch::X86_64, "x86_64");
        assert_json_string(TargetLibc::Any, "any");
        assert_json_string(ChecksumSource::GitHubDigest, "github-digest");
        assert_json_string(ArchiveFormat::TarGz, "tar.gz");
        assert_json_string(ArchiveFormat::BareExecutable, "bare-executable");
    }

    fn assert_json_string(value: impl serde::Serialize, expected: &str) {
        let serialized = serde_json::to_string(&value).expect("serialize enum");

        assert_eq!(serialized, format!("\"{expected}\""));
    }
}
