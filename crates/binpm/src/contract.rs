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

    let (remainder, version) = split_version(raw, remainder)?;

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
    let segments = path_segments(raw, remainder)?;
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
    let segments = path_segments(raw, remainder)?;
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

fn split_version<'source>(
    raw: &str,
    remainder: &'source str,
) -> Result<(&'source str, Option<String>), BinpmError> {
    match remainder.split_once('@') {
        Some((source, version)) if !source.is_empty() && !version.is_empty() => {
            validate_version_selector(raw, version)?;
            Ok((source, Some(version.to_string())))
        }
        Some(("", _)) => Err(invalid_source(raw, "source path cannot be empty")),
        Some((_, "")) => Err(invalid_source(raw, "source version cannot be empty")),
        _ => Ok((remainder, None)),
    }
}

pub(crate) fn validate_version_selector(raw: &str, version: &str) -> Result<(), BinpmError> {
    if version == "latest" {
        return Err(unsupported_version_selector(
            raw,
            "`@latest` is not supported; omit `@version` to select the latest stable release",
        ));
    }

    if matches!(
        version,
        "stable" | "beta" | "alpha" | "nightly" | "canary" | "dev" | "edge" | "next"
    ) {
        return Err(unsupported_version_selector(
            raw,
            "channel selectors are not supported; use an exact release tag or omit `@version` for \
             the latest stable release",
        ));
    }

    if version.chars().all(|character| character.is_ascii_digit()) && version.len() <= 3 {
        return Err(unsupported_version_selector(
            raw,
            "major-version pins such as `@1` are not supported; use an exact release tag such as \
             `@v1` when the upstream release tag is literally `v1`",
        ));
    }

    if looks_like_semver_range(version) {
        return Err(unsupported_version_selector(
            raw,
            "semver ranges are not supported; use an exact release tag or omit `@version` for the \
             latest stable release",
        ));
    }

    Ok(())
}

fn looks_like_semver_range(version: &str) -> bool {
    version.starts_with(['^', '~', '<', '>', '=', '*'])
        || version.contains("||")
        || version.contains(" - ")
        || version
            .split(['.', '-'])
            .any(|segment| matches!(segment, "x" | "X" | "*"))
}

fn unsupported_version_selector(raw: &str, message: impl Into<String>) -> BinpmError {
    invalid_source(raw, message)
}

fn path_segments<'a>(raw: &str, path: &'a str) -> Result<Vec<&'a str>, BinpmError> {
    let segments: Vec<_> = path.split('/').collect();
    if segments.iter().any(|segment| segment.is_empty()) {
        return Err(invalid_source(
            raw,
            "source path segments must not be empty",
        ));
    }

    Ok(segments)
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
    pub fn current() -> Result<Self, BinpmError> {
        Ok(Self {
            os: TargetOs::current()?,
            arch: TargetArch::current()?,
            libc: TargetLibc::current(),
        })
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

    fn current() -> Result<Self, BinpmError> {
        Self::from_current(std::env::consts::OS)
    }

    fn from_current(raw: &str) -> Result<Self, BinpmError> {
        match raw {
            "linux" => Ok(Self::Linux),
            "macos" => Ok(Self::Darwin),
            "windows" => Ok(Self::Windows),
            "freebsd" => Ok(Self::FreeBsd),
            raw => Err(BinpmError::UnsupportedTargetComponent {
                component: "os",
                raw: raw.to_string(),
            }),
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

    fn current() -> Result<Self, BinpmError> {
        Self::from_current_cfg(std::env::consts::ARCH, option_env!("BINPM_TARGET_TRIPLE"))
    }

    fn from_current_cfg(raw: &str, target_triple: Option<&str>) -> Result<Self, BinpmError> {
        match raw {
            "x86_64" => Ok(Self::X86_64),
            "aarch64" => Ok(Self::Aarch64),
            "i686" => Ok(Self::I686),
            "x86" if target_triple.is_some_and(is_i686_target_triple) => Ok(Self::I686),
            "arm" if target_triple.is_some_and(is_armv7_target_triple) => Ok(Self::Armv7),
            raw => Err(BinpmError::UnsupportedTargetComponent {
                component: "architecture",
                raw: raw.to_string(),
            }),
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

fn is_i686_target_triple(target_triple: &str) -> bool {
    target_triple.starts_with("i686-")
}

fn is_armv7_target_triple(target_triple: &str) -> bool {
    target_triple.starts_with("armv7")
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
        Self::from_current_cfg(
            cfg!(target_env = "musl"),
            cfg!(target_env = "msvc"),
            cfg!(target_env = "gnu"),
            cfg!(target_os = "linux"),
        )
    }

    fn from_current_cfg(is_musl: bool, is_msvc: bool, is_gnu: bool, is_linux: bool) -> Self {
        match (is_musl, is_msvc, is_gnu, is_linux) {
            (true, _, _, _) => Self::Musl,
            (_, true, _, _) => Self::Msvc,
            (_, _, true, _) => Self::Gnu,
            _ => Self::Any,
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

impl ChecksumSource {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::GitHubDigest => "github-digest",
            Self::Sidecar => "sidecar",
            Self::Manifest => "manifest",
            Self::Signature => "signature",
            Self::Local => "local",
        }
    }

    pub fn is_upstream_verified(self) -> bool {
        matches!(self, Self::GitHubDigest)
    }
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

impl ArchiveFormat {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::TarGz => "tar.gz",
            Self::Tgz => "tgz",
            Self::TarXz => "tar.xz",
            Self::Txz => "txz",
            Self::TarZst => "tar.zst",
            Self::Zip => "zip",
            Self::BareExecutable => "bare-executable",
        }
    }
}

#[cfg(test)]
mod tests {
    use std::str::FromStr;

    use super::{
        ArchiveFormat, ChecksumSource, HostTarget, Scope, SourceProvider, SourceSpec, TargetArch,
        TargetLibc, TargetOs,
    };
    use crate::error::BinpmError;

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
    fn parses_source_version_with_at_sign() {
        let spec = SourceSpec::from_str("github:owner/repo@tool@v1.0.0").expect("source");

        assert_eq!(spec.provider, SourceProvider::GitHub);
        assert_eq!(spec.host, "github.com");
        assert_eq!(spec.path, "owner/repo");
        assert_eq!(spec.version.as_deref(), Some("tool@v1.0.0"));
    }

    #[test]
    fn rejects_latest_selector_with_omitted_version_hint() {
        let error = SourceSpec::from_str("github:owner/repo@latest").expect_err("latest");

        match error {
            BinpmError::InvalidSourceSpec { raw, message } => {
                assert_eq!(raw, "github:owner/repo@latest");
                assert!(message.contains("`@latest` is not supported"));
                assert!(message.contains("omit `@version`"));
            }
            other => panic!("expected InvalidSourceSpec, got {other:?}"),
        }
    }

    #[test]
    fn rejects_range_channel_and_major_pin_selectors() {
        for (raw, expected) in [
            ("github:owner/repo@^1", "semver ranges are not supported"),
            ("github:owner/repo@1.x", "semver ranges are not supported"),
            (
                "github:owner/repo@beta",
                "channel selectors are not supported",
            ),
            (
                "github:owner/repo@1",
                "major-version pins such as `@1` are not supported",
            ),
        ] {
            match SourceSpec::from_str(raw).expect_err("unsupported selector") {
                BinpmError::InvalidSourceSpec {
                    raw: error_raw,
                    message,
                } => {
                    assert_eq!(error_raw, raw);
                    assert!(message.contains(expected), "{message}");
                }
                other => panic!("expected InvalidSourceSpec, got {other:?}"),
            }
        }
    }

    #[test]
    fn preserves_exact_tag_forms_that_do_not_match_unsupported_selectors() {
        let v1 = SourceSpec::from_str("github:owner/repo@v1").expect("v1 tag");
        let release = SourceSpec::from_str("github:owner/repo@release-2026.06").expect("tag");
        let numeric_date = SourceSpec::from_str("github:owner/repo@20240621").expect("tag");

        assert_eq!(v1.version.as_deref(), Some("v1"));
        assert_eq!(release.version.as_deref(), Some("release-2026.06"));
        assert_eq!(numeric_date.version.as_deref(), Some("20240621"));
    }

    #[test]
    fn rejects_empty_source_path_segments() {
        for raw in [
            "github:/owner/repo",
            "github:owner//repo",
            "github:owner/repo/",
            "gitlab:/gitlab.example.com/platform/tool",
            "gitlab:gitlab.example.com/platform//tool",
            "gitlab:gitlab.example.com/platform/tool/",
        ] {
            assert_invalid_source(raw);
        }
    }

    #[test]
    fn rejects_empty_source_version() {
        let error = SourceSpec::from_str("github:owner/repo@").expect_err("empty version");

        match error {
            BinpmError::InvalidSourceSpec { raw, message } => {
                assert_eq!(raw, "github:owner/repo@");
                assert_eq!(message, "source version cannot be empty");
            }
            other => panic!("expected InvalidSourceSpec, got {other:?}"),
        }
    }

    #[test]
    fn rejects_version_delimiter_before_source_path() {
        for raw in [
            "github:@owner/repo",
            "gitlab:@gitlab.example.com/platform/tool",
        ] {
            assert_invalid_source(raw);
        }
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
    fn rejects_unsupported_current_os_without_linux_fallback() {
        let error = TargetOs::from_current("openbsd").expect_err("unsupported os");

        assert_unsupported_component(error, "os", "openbsd");
    }

    #[test]
    fn rejects_unsupported_current_arch_without_x86_64_fallback() {
        let error =
            TargetArch::from_current_cfg("riscv64", None).expect_err("unsupported architecture");

        assert_unsupported_component(error, "architecture", "riscv64");
    }

    #[test]
    fn rejects_ambiguous_current_arm_arch() {
        let error = TargetArch::from_current_cfg("arm", None).expect_err("ambiguous arm");

        assert_unsupported_component(error, "architecture", "arm");
    }

    #[test]
    fn rejects_arm_eabihf_without_armv7_target_triple() {
        let error = TargetArch::from_current_cfg("arm", Some("arm-unknown-linux-gnueabihf"))
            .expect_err("ambiguous arm eabihf");

        assert_unsupported_component(error, "architecture", "arm");
    }

    #[test]
    fn rejects_ambiguous_current_x86_arch() {
        let error = TargetArch::from_current_cfg("x86", None).expect_err("ambiguous x86");

        assert_unsupported_component(error, "architecture", "x86");
    }

    #[test]
    fn rejects_non_i686_current_x86_arch() {
        let error = TargetArch::from_current_cfg("x86", Some("i586-unknown-linux-gnu"))
            .expect_err("unsupported x86 target");

        assert_unsupported_component(error, "architecture", "x86");
    }

    #[test]
    fn preserves_current_i686_arch() {
        let arch = TargetArch::from_current_cfg("i686", None).expect("i686 architecture");

        assert_eq!(arch, TargetArch::I686);
    }

    #[test]
    fn maps_current_x86_i686_target_triple_to_i686_arch() {
        let arch = TargetArch::from_current_cfg("x86", Some("i686-unknown-linux-gnu"))
            .expect("i686 architecture");

        assert_eq!(arch, TargetArch::I686);
    }

    #[test]
    fn preserves_current_armv7_eabihf_arch() {
        let arch = TargetArch::from_current_cfg("arm", Some("armv7-unknown-linux-gnueabihf"))
            .expect("armv7 architecture");

        assert_eq!(arch, TargetArch::Armv7);
    }

    #[test]
    fn preserves_current_gnu_libc_without_linux_os() {
        assert_eq!(
            TargetLibc::from_current_cfg(false, false, true, false),
            TargetLibc::Gnu
        );
    }

    #[test]
    fn does_not_guess_gnu_for_unknown_current_linux_libc() {
        assert_eq!(
            TargetLibc::from_current_cfg(false, false, false, true),
            TargetLibc::Any
        );
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

    fn assert_invalid_source(raw: &str) {
        match SourceSpec::from_str(raw).expect_err("invalid source") {
            BinpmError::InvalidSourceSpec { raw: error_raw, .. } => {
                assert_eq!(error_raw, raw);
            }
            other => panic!("expected InvalidSourceSpec, got {other:?}"),
        }
    }

    fn assert_unsupported_component(
        error: BinpmError,
        expected_component: &str,
        expected_raw: &str,
    ) {
        match error {
            BinpmError::UnsupportedTargetComponent { component, raw } => {
                assert_eq!(component, expected_component);
                assert_eq!(raw, expected_raw);
            }
            other => panic!("expected UnsupportedTargetComponent, got {other:?}"),
        }
    }
}
