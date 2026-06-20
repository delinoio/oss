use std::cmp::Ordering;

use serde::{Deserialize, Serialize};
use tracing::debug;

use crate::{
    contract::{ArchiveFormat, HostTarget, SourceProvider, TargetArch, TargetLibc, TargetOs},
    release::ReleaseAsset,
};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum ArtifactKind {
    #[serde(rename = "archive")]
    Archive(ArchiveFormat),
    #[serde(rename = "bare-executable")]
    BareExecutable,
    #[serde(rename = "source-archive")]
    SourceArchive,
    #[serde(rename = "sidecar")]
    Sidecar,
    #[serde(rename = "desktop-package")]
    DesktopPackage,
    #[serde(rename = "package-metadata")]
    PackageMetadata,
    #[serde(rename = "unknown")]
    Unknown,
}

impl ArtifactKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Archive(_) => "archive",
            Self::BareExecutable => "bare-executable",
            Self::SourceArchive => "source-archive",
            Self::Sidecar => "sidecar",
            Self::DesktopPackage => "desktop-package",
            Self::PackageMetadata => "package-metadata",
            Self::Unknown => "unknown",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CandidateDecision {
    pub asset_name: String,
    pub canonical_url: String,
    pub download_url: String,
    pub kind: ArtifactKind,
    pub detected_os: Option<TargetOs>,
    pub detected_arch: Option<TargetArch>,
    pub detected_libc: Option<TargetLibc>,
    pub score: Option<i32>,
    pub eligible: bool,
    pub recognized_pattern: bool,
    pub rejection_reason: Option<String>,
}

impl CandidateDecision {
    pub fn explain_line(&self) -> String {
        if self.eligible {
            format!(
                "candidate {} kind={} score={} target={}/{}/{}",
                self.asset_name,
                self.kind.as_str(),
                self.score.unwrap_or_default(),
                self.detected_os.map(TargetOs::as_str).unwrap_or("unknown"),
                self.detected_arch
                    .map(TargetArch::as_str)
                    .unwrap_or("unknown"),
                self.detected_libc
                    .map(TargetLibc::as_str)
                    .unwrap_or("unknown")
            )
        } else {
            format!(
                "rejected {} kind={} reason={}",
                self.asset_name,
                self.kind.as_str(),
                self.rejection_reason.as_deref().unwrap_or("not eligible")
            )
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AssetSelection {
    pub selected: CandidateDecision,
    pub decisions: Vec<CandidateDecision>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ArchiveMember {
    pub path: String,
    pub executable: bool,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum BinaryDiscovery {
    Selected(String),
    Ambiguous(Vec<String>),
    NotFound,
}

pub fn classify_artifact(name: &str, source_archive: bool) -> ArtifactKind {
    let lower = name.to_ascii_lowercase();

    if source_archive || is_source_archive_name(&lower) {
        return ArtifactKind::SourceArchive;
    }

    if is_sidecar_name(&lower) {
        return ArtifactKind::Sidecar;
    }

    if is_package_metadata_name(&lower) {
        return ArtifactKind::PackageMetadata;
    }

    if is_desktop_package_name(&lower) {
        return ArtifactKind::DesktopPackage;
    }

    if lower.ends_with(".tar.gz") {
        return ArtifactKind::Archive(ArchiveFormat::TarGz);
    }
    if lower.ends_with(".tgz") {
        return ArtifactKind::Archive(ArchiveFormat::Tgz);
    }
    if lower.ends_with(".tar.xz") {
        return ArtifactKind::Archive(ArchiveFormat::TarXz);
    }
    if lower.ends_with(".txz") {
        return ArtifactKind::Archive(ArchiveFormat::Txz);
    }
    if lower.ends_with(".tar.zst") {
        return ArtifactKind::Archive(ArchiveFormat::TarZst);
    }
    if lower.ends_with(".zip") {
        return ArtifactKind::Archive(ArchiveFormat::Zip);
    }
    if lower.ends_with(".exe") || !lower.rsplit('/').next().unwrap_or(&lower).contains('.') {
        return ArtifactKind::BareExecutable;
    }

    ArtifactKind::Unknown
}

pub fn score_assets(
    provider: SourceProvider,
    target: &HostTarget,
    assets: &[ReleaseAsset],
) -> Vec<CandidateDecision> {
    let mut decisions = assets
        .iter()
        .map(|asset| score_asset(provider, target, asset))
        .collect::<Vec<_>>();
    decisions.sort_by(compare_candidates);
    decisions
}

pub fn select_asset(
    provider: SourceProvider,
    target: &HostTarget,
    assets: &[ReleaseAsset],
) -> Option<AssetSelection> {
    let decisions = score_assets(provider, target, assets);
    let selected = decisions
        .iter()
        .find(|decision| decision.eligible)
        .cloned()?;
    Some(AssetSelection {
        selected,
        decisions,
    })
}

pub fn discover_archive_binary(repo_name: &str, members: &[ArchiveMember]) -> BinaryDiscovery {
    let mut candidates = members
        .iter()
        .filter(|member| member.executable || member.path.to_ascii_lowercase().ends_with(".exe"))
        .map(|member| member.path.clone())
        .collect::<Vec<_>>();
    candidates.sort();

    if candidates.is_empty() {
        return BinaryDiscovery::NotFound;
    }

    let normalized_repo = normalized_binary_name(repo_name);
    let matching_repo = candidates
        .iter()
        .filter(|candidate| normalized_binary_name(basename(candidate)) == normalized_repo)
        .cloned()
        .collect::<Vec<_>>();

    match matching_repo.len() {
        1 => BinaryDiscovery::Selected(matching_repo[0].clone()),
        len if len > 1 => BinaryDiscovery::Ambiguous(matching_repo),
        _ if candidates.len() == 1 => BinaryDiscovery::Selected(candidates[0].clone()),
        _ => BinaryDiscovery::Ambiguous(candidates),
    }
}

fn score_asset(
    provider: SourceProvider,
    target: &HostTarget,
    asset: &ReleaseAsset,
) -> CandidateDecision {
    let download_url = asset
        .provider_url
        .as_deref()
        .unwrap_or(&asset.url)
        .to_string();
    let canonical_url = download_url
        .split(['?', '#'])
        .next()
        .unwrap_or(&asset.url)
        .to_string();
    let kind = classify_artifact(&asset.name, asset.source_archive);
    let target_signal = detect_target(&asset.name);
    let mut decision = CandidateDecision {
        asset_name: asset.name.clone(),
        canonical_url,
        download_url,
        kind,
        detected_os: target_signal.os,
        detected_arch: target_signal.arch,
        detected_libc: target_signal.libc,
        score: None,
        eligible: false,
        recognized_pattern: target_signal.recognized_pattern,
        rejection_reason: None,
    };

    if provider == SourceProvider::GitLab && !gitlab_https_eligible(asset) {
        decision.rejection_reason = Some("gitlab asset link is not HTTPS eligible".to_string());
        log_candidate(target, &decision);
        return decision;
    }

    match kind {
        ArtifactKind::Archive(_) | ArtifactKind::BareExecutable => {}
        ArtifactKind::DesktopPackage => {
            decision.rejection_reason =
                Some("desktop or system package formats are not installed by default".to_string());
            log_candidate(target, &decision);
            return decision;
        }
        ArtifactKind::SourceArchive => {
            decision.rejection_reason = Some("source archive is not installable".to_string());
            log_candidate(target, &decision);
            return decision;
        }
        ArtifactKind::Sidecar => {
            decision.rejection_reason = Some("sidecar metadata is not installable".to_string());
            log_candidate(target, &decision);
            return decision;
        }
        ArtifactKind::PackageMetadata => {
            decision.rejection_reason = Some("package metadata is not installable".to_string());
            log_candidate(target, &decision);
            return decision;
        }
        ArtifactKind::Unknown => {
            decision.rejection_reason = Some("artifact kind is unknown".to_string());
            log_candidate(target, &decision);
            return decision;
        }
    }

    let Some(score) = target_score(target, &target_signal) else {
        decision.rejection_reason = Some("asset target does not match host target".to_string());
        log_candidate(target, &decision);
        return decision;
    };

    decision.score = Some(score);
    decision.eligible = true;
    log_candidate(target, &decision);
    decision
}

pub(crate) fn gitlab_https_eligible(asset: &ReleaseAsset) -> bool {
    is_https_url(&asset.url)
        && asset
            .provider_url
            .as_deref()
            .map(is_https_url)
            .unwrap_or(true)
        && asset.final_url_https.unwrap_or(true)
}

fn is_https_url(url: &str) -> bool {
    url.to_ascii_lowercase().starts_with("https://")
}

fn target_score(target: &HostTarget, signal: &TargetSignal) -> Option<i32> {
    let os = signal.os?;
    let arch = signal.arch;
    let libc = signal.libc;

    if os != target.os {
        return None;
    }

    let mut score = 100;

    match (arch, target.arch, os) {
        (Some(asset_arch), target_arch, _) if asset_arch == target_arch => score += 80,
        (Some(TargetArch::X86_64), TargetArch::Aarch64, TargetOs::Darwin)
            if signal.universal_macos =>
        {
            score += 20;
        }
        (None, _, _) => score -= 60,
        _ => return None,
    }

    match (target.os, target.libc, libc) {
        (TargetOs::Linux, TargetLibc::Gnu, Some(TargetLibc::Gnu)) => score += 50,
        (TargetOs::Linux, TargetLibc::Gnu, Some(TargetLibc::Any)) => score += 45,
        (TargetOs::Linux, TargetLibc::Gnu, None) => score += 20,
        (TargetOs::Linux, TargetLibc::Musl, Some(TargetLibc::Musl)) => score += 50,
        (TargetOs::Linux, TargetLibc::Musl, Some(TargetLibc::Any)) => score += 45,
        (TargetOs::Linux, TargetLibc::Musl, None) => return None,
        (TargetOs::Linux, _, Some(asset_libc)) if asset_libc == target.libc => score += 50,
        (TargetOs::Linux, _, Some(TargetLibc::Any)) => score += 45,
        (TargetOs::Linux, _, None) => score += 10,
        (_, _, Some(TargetLibc::Any)) => score += 10,
        (_, _, Some(asset_libc)) if asset_libc == target.libc => score += 10,
        (_, _, None) => {}
        _ => return None,
    }

    if signal.recognized_pattern {
        score += 5;
    }

    Some(score)
}

fn compare_candidates(left: &CandidateDecision, right: &CandidateDecision) -> Ordering {
    right
        .eligible
        .cmp(&left.eligible)
        .then_with(|| right.score.cmp(&left.score))
        .then_with(|| right.recognized_pattern.cmp(&left.recognized_pattern))
        .then_with(|| left.asset_name.len().cmp(&right.asset_name.len()))
        .then_with(|| left.asset_name.cmp(&right.asset_name))
}

#[derive(Debug, Clone, Copy, Default)]
struct TargetSignal {
    os: Option<TargetOs>,
    arch: Option<TargetArch>,
    libc: Option<TargetLibc>,
    recognized_pattern: bool,
    universal_macos: bool,
}

fn detect_target(name: &str) -> TargetSignal {
    let lower_name = name.to_ascii_lowercase().replace("x86_64", "x64");
    let is_windows_executable = lower_name.ends_with(".exe");
    let lower = strip_known_suffixes(&lower_name);
    let tokens = lower
        .split(|character: char| !character.is_ascii_alphanumeric())
        .filter(|token| !token.is_empty())
        .collect::<Vec<_>>();

    let mut signal = TargetSignal::default();
    if is_windows_executable {
        signal.os = Some(TargetOs::Windows);
    }

    for token in &tokens {
        if signal.os.is_none() {
            signal.os = os_alias(token);
        }
        if signal.arch.is_none() {
            signal.arch = arch_alias(token);
        }
        if signal.libc.is_none() {
            signal.libc = libc_alias(token);
        }
    }

    let joined = tokens.join("-");
    if joined.contains("apple-darwin") {
        signal.os = Some(TargetOs::Darwin);
    }
    if joined.contains("pc-windows-msvc") {
        signal.os = Some(TargetOs::Windows);
        signal.libc = Some(TargetLibc::Msvc);
    }
    if joined.contains("unknown-linux-gnu") {
        signal.os = Some(TargetOs::Linux);
        signal.libc = Some(TargetLibc::Gnu);
    }
    if joined.contains("unknown-linux-musl") {
        signal.os = Some(TargetOs::Linux);
        signal.libc = Some(TargetLibc::Musl);
    }
    if joined.contains("universal-apple-darwin") || joined.contains("darwin-universal") {
        signal.os = Some(TargetOs::Darwin);
        signal.libc = Some(TargetLibc::Any);
        signal.universal_macos = true;
    }

    signal.recognized_pattern = signal.os.is_some() && signal.arch.is_some();
    signal
}

fn strip_known_suffixes(name: &str) -> &str {
    for suffix in [
        ".tar.gz",
        ".tar.xz",
        ".tar.zst",
        ".tgz",
        ".txz",
        ".zip",
        ".exe",
        ".sha256",
        ".sha512",
        ".minisig",
        ".sigstore.json",
        ".sbom.json",
        ".asc",
        ".sig",
    ] {
        if let Some(stripped) = name.strip_suffix(suffix) {
            return stripped;
        }
    }
    name
}

fn os_alias(token: &str) -> Option<TargetOs> {
    match token {
        "linux" => Some(TargetOs::Linux),
        "darwin" | "macos" | "mac" | "osx" => Some(TargetOs::Darwin),
        "windows" | "win" | "win32" => Some(TargetOs::Windows),
        "freebsd" => Some(TargetOs::FreeBsd),
        _ => None,
    }
}

fn arch_alias(token: &str) -> Option<TargetArch> {
    match token {
        "x86_64" | "amd64" | "x64" => Some(TargetArch::X86_64),
        "aarch64" | "arm64" => Some(TargetArch::Aarch64),
        "i686" | "i386" | "x86" | "ia32" | "386" => Some(TargetArch::I686),
        "armv7" => Some(TargetArch::Armv7),
        _ => None,
    }
}

fn libc_alias(token: &str) -> Option<TargetLibc> {
    match token {
        "gnu" | "glibc" => Some(TargetLibc::Gnu),
        "musl" | "alpine" => Some(TargetLibc::Musl),
        "msvc" => Some(TargetLibc::Msvc),
        "static" | "portable" | "universal" | "any" => Some(TargetLibc::Any),
        _ => None,
    }
}

fn is_source_archive_name(lower: &str) -> bool {
    matches!(lower, "source.tar.gz" | "source.zip")
        || lower.contains("source-code")
        || (has_installable_archive_suffix(lower)
            && (lower.contains("-source") || lower.contains("_source") || lower.contains("-src")))
}

fn is_sidecar_name(lower: &str) -> bool {
    lower.ends_with(".sha256")
        || lower.ends_with(".sha512")
        || lower.ends_with(".sig")
        || lower.ends_with(".asc")
        || lower.ends_with(".minisig")
        || lower.ends_with(".sigstore.json")
        || lower.ends_with(".sbom.json")
        || matches!(
            lower,
            "sha256sums"
                | "sha256sums.txt"
                | "checksums.txt"
                | "dist-manifest.json"
                | "latest.json"
        )
}

fn is_package_metadata_name(lower: &str) -> bool {
    lower.ends_with(".rb") || lower.ends_with(".json") || is_npm_package_tarball_name(lower)
}

fn is_npm_package_tarball_name(lower: &str) -> bool {
    let basename = basename(lower);
    let Some(stem) = basename.strip_suffix(".tgz") else {
        return false;
    };

    if detect_target(stem).recognized_pattern {
        return false;
    }

    stem == "package"
        || stem.match_indices('-').any(|(index, _)| {
            let (name, version) = stem.split_at(index);
            !name.is_empty() && is_semver_like(&version[1..])
        })
}

fn is_semver_like(version: &str) -> bool {
    let version = version.strip_prefix('v').unwrap_or(version);
    let (version, prerelease) = version
        .split_once('-')
        .map(|(version, prerelease)| (version, Some(prerelease)))
        .unwrap_or((version, None));
    let mut parts = version.split('.');

    let (Some(major), Some(minor), Some(patch)) = (parts.next(), parts.next(), parts.next()) else {
        return false;
    };

    parts.next().is_none()
        && is_version_number(major)
        && is_version_number(minor)
        && is_version_number(patch)
        && prerelease
            .map(|prerelease| {
                prerelease.split('.').all(|part| {
                    !part.is_empty() && part.chars().all(|c| c.is_ascii_alphanumeric() || c == '-')
                })
            })
            .unwrap_or(true)
}

fn is_version_number(part: &str) -> bool {
    !part.is_empty() && part.chars().all(|character| character.is_ascii_digit())
}

fn is_desktop_package_name(lower: &str) -> bool {
    lower.ends_with(".deb")
        || lower.ends_with(".rpm")
        || lower.ends_with(".apk")
        || lower.ends_with(".pkg.tar.zst")
        || lower.ends_with(".dmg")
        || lower.ends_with(".msi")
        || lower.ends_with(".pkg")
        || lower.ends_with(".appimage")
        || lower.ends_with(".flatpak")
        || lower.ends_with(".snap")
}

fn has_installable_archive_suffix(lower: &str) -> bool {
    lower.ends_with(".tar.gz")
        || lower.ends_with(".tgz")
        || lower.ends_with(".tar.xz")
        || lower.ends_with(".txz")
        || lower.ends_with(".tar.zst")
        || lower.ends_with(".zip")
}

fn log_candidate(target: &HostTarget, decision: &CandidateDecision) {
    debug!(
        target_os = target.os.as_str(),
        target_arch = target.arch.as_str(),
        target_libc = target.libc.as_str(),
        asset_name = decision.asset_name,
        detected_os = decision.detected_os.map(TargetOs::as_str).unwrap_or(""),
        detected_arch = decision.detected_arch.map(TargetArch::as_str).unwrap_or(""),
        detected_libc = decision.detected_libc.map(TargetLibc::as_str).unwrap_or(""),
        artifact_kind = decision.kind.as_str(),
        score = decision.score.unwrap_or_default(),
        rejection_reason = decision.rejection_reason.as_deref().unwrap_or(""),
        "Scored release asset candidate"
    );
}

fn basename(path: &str) -> &str {
    path.rsplit(['/', '\\']).next().unwrap_or(path)
}

fn normalized_binary_name(name: &str) -> String {
    name.strip_suffix(".exe")
        .unwrap_or(name)
        .to_ascii_lowercase()
}

#[cfg(test)]
mod tests {
    use super::{
        classify_artifact, discover_archive_binary, score_assets, select_asset, ArchiveMember,
        ArtifactKind, BinaryDiscovery,
    };
    use crate::{
        contract::{ArchiveFormat, HostTarget, SourceProvider, TargetArch, TargetLibc, TargetOs},
        release::ReleaseAsset,
    };

    fn asset(name: &str) -> ReleaseAsset {
        ReleaseAsset {
            name: name.to_string(),
            url: format!("https://example.com/{name}"),
            provider_url: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
        }
    }

    fn target(os: TargetOs, arch: TargetArch, libc: TargetLibc) -> HostTarget {
        HostTarget { os, arch, libc }
    }

    fn member(path: &str, executable: bool) -> ArchiveMember {
        ArchiveMember {
            path: path.to_string(),
            executable,
        }
    }

    #[test]
    fn classifies_installable_archive_and_bare_executable_kinds() {
        assert_eq!(
            classify_artifact("tool-x86_64-unknown-linux-gnu.tar.gz", false),
            ArtifactKind::Archive(ArchiveFormat::TarGz)
        );
        assert_eq!(
            classify_artifact("tool.exe", false),
            ArtifactKind::BareExecutable
        );
    }

    #[test]
    fn preserves_native_tgz_archives_with_node_like_names() {
        assert_eq!(
            classify_artifact("nodeup-linux-amd64.tgz", false),
            ArtifactKind::Archive(ArchiveFormat::Tgz)
        );
        assert_eq!(
            classify_artifact("package-linux-amd64.tgz", false),
            ArtifactKind::Archive(ArchiveFormat::Tgz)
        );
        assert_eq!(
            classify_artifact("tool-x86_64-unknown-linux-gnu-1.2.3.tgz", false),
            ArtifactKind::Archive(ArchiveFormat::Tgz)
        );
        assert_eq!(
            classify_artifact("tool-1.2.3-x86_64-unknown-linux-gnu.tgz", false),
            ArtifactKind::Archive(ArchiveFormat::Tgz)
        );
        assert_eq!(
            classify_artifact("rollup-linux-x64-gnu-4.9.5-rc-1.tgz", false),
            ArtifactKind::Archive(ArchiveFormat::Tgz)
        );
        assert_eq!(
            classify_artifact("rollup-linux-x64-gnu-4.9.5.tgz", false),
            ArtifactKind::Archive(ArchiveFormat::Tgz)
        );
        assert_eq!(
            classify_artifact("nodeup-1.2.3.tgz", false),
            ArtifactKind::PackageMetadata
        );
        assert_eq!(
            classify_artifact("nodeup-1.2.3-beta.1.tgz", false),
            ArtifactKind::PackageMetadata
        );
    }

    #[test]
    fn rejects_source_archives_and_generated_gitlab_sources() {
        assert_eq!(
            classify_artifact("source.tar.gz", false),
            ArtifactKind::SourceArchive
        );
        assert_eq!(
            classify_artifact("tar.gz", true),
            ArtifactKind::SourceArchive
        );
    }

    #[test]
    fn rejects_sidecars_and_desktop_installers() {
        assert_eq!(
            classify_artifact("tool.tar.gz.sha256", false),
            ArtifactKind::Sidecar
        );
        assert_eq!(
            classify_artifact("tool.dmg", false),
            ArtifactKind::DesktopPackage
        );
    }

    #[test]
    fn exact_libc_match_beats_any_and_missing_libc() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        let selected = select_asset(
            SourceProvider::GitHub,
            &host,
            &[
                asset("tool-linux-x64.tar.gz"),
                asset("tool-linux-x64-portable.tar.gz"),
                asset("tool-x86_64-unknown-linux-gnu.tar.gz"),
            ],
        )
        .expect("selected");

        assert_eq!(
            selected.selected.asset_name,
            "tool-x86_64-unknown-linux-gnu.tar.gz"
        );
    }

    #[test]
    fn linux_gnu_accepts_missing_libc_fallback() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        let selected = select_asset(
            SourceProvider::GitHub,
            &host,
            &[asset("tool-linux-amd64.tar.gz")],
        )
        .expect("selected");

        assert_eq!(selected.selected.asset_name, "tool-linux-amd64.tar.gz");
    }

    #[test]
    fn bare_exe_assets_are_windows_candidates() {
        let windows = target(TargetOs::Windows, TargetArch::X86_64, TargetLibc::Msvc);
        let selected =
            select_asset(SourceProvider::GitHub, &windows, &[asset("tool.exe")]).expect("selected");

        assert_eq!(selected.selected.asset_name, "tool.exe");
        assert_eq!(selected.selected.detected_os, Some(TargetOs::Windows));
    }

    #[test]
    fn keeps_runtime_download_url_separate_from_persisted_url() {
        let linux = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        let mut release_asset = asset("tool-x86_64-unknown-linux-gnu");
        release_asset.url = "https://example.com/tool?token=secret#fragment".to_string();
        let selected =
            select_asset(SourceProvider::GitHub, &linux, &[release_asset]).expect("selected");

        assert_eq!(
            selected.selected.download_url,
            "https://example.com/tool?token=secret#fragment"
        );
        assert_eq!(selected.selected.canonical_url, "https://example.com/tool");
    }

    #[test]
    fn linux_musl_rejects_missing_libc_without_portable_signal() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Musl);
        let decisions = score_assets(
            SourceProvider::GitHub,
            &host,
            &[asset("tool-linux-amd64.tar.gz")],
        );

        assert!(!decisions[0].eligible);
        assert_eq!(
            decisions[0].rejection_reason.as_deref(),
            Some("asset target does not match host target")
        );
    }

    #[test]
    fn linux_musl_accepts_portable_any_signal() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Musl);
        let selected = select_asset(
            SourceProvider::GitHub,
            &host,
            &[asset("tool-linux-amd64-static.tar.gz")],
        )
        .expect("selected");

        assert_eq!(selected.selected.detected_libc, Some(TargetLibc::Any));
    }

    #[test]
    fn universal_macos_is_lower_score_than_exact_arch() {
        let host = target(TargetOs::Darwin, TargetArch::Aarch64, TargetLibc::Any);
        let selected = select_asset(
            SourceProvider::GitHub,
            &host,
            &[
                asset("tool-universal-apple-darwin.tar.gz"),
                asset("tool-aarch64-apple-darwin.tar.gz"),
            ],
        )
        .expect("selected");

        assert_eq!(
            selected.selected.asset_name,
            "tool-aarch64-apple-darwin.tar.gz"
        );
    }

    #[test]
    fn recognizes_cargo_dist_and_goreleaser_and_bun_deno_patterns() {
        let linux = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        let darwin = target(TargetOs::Darwin, TargetArch::Aarch64, TargetLibc::Any);

        assert!(select_asset(
            SourceProvider::GitHub,
            &linux,
            &[asset("ripgrep-x86_64-unknown-linux-gnu.tar.xz")]
        )
        .is_some());
        assert!(select_asset(
            SourceProvider::GitHub,
            &linux,
            &[asset("tool_1.2.3_Linux_amd64.tar.gz")]
        )
        .is_some());
        assert!(select_asset(
            SourceProvider::GitHub,
            &darwin,
            &[asset("bun-darwin-aarch64.zip")]
        )
        .is_some());
        assert!(select_asset(
            SourceProvider::GitHub,
            &linux,
            &[asset("deno-x86_64-unknown-linux-gnu.zip")]
        )
        .is_some());
    }

    #[test]
    fn tie_breaks_by_recognized_pattern_then_shorter_then_lexicographic_name() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        let selected = select_asset(
            SourceProvider::GitHub,
            &host,
            &[
                asset("zzzz-tool-linux-amd64.tar.gz"),
                asset("tool-linux-amd64.tar.gz"),
            ],
        )
        .expect("selected");

        assert_eq!(selected.selected.asset_name, "tool-linux-amd64.tar.gz");
    }

    #[test]
    fn gitlab_rejects_non_https_link_or_direct_asset_url_before_scoring() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        let mut link = asset("tool-x86_64-unknown-linux-gnu.tar.gz");
        link.url = "http://example.com/tool.tar.gz".to_string();
        let mut direct = asset("tool-x86_64-unknown-linux-gnu.zip");
        direct.provider_url = Some("http://gitlab.example.com/direct.zip".to_string());
        let mut redirected = asset("tool-x86_64-unknown-linux-gnu.tgz");
        redirected.final_url_https = Some(false);
        let decisions = score_assets(SourceProvider::GitLab, &host, &[link, direct]);

        assert!(decisions.iter().all(|decision| !decision.eligible));
        assert!(decisions
            .iter()
            .all(|decision| decision.rejection_reason.as_deref()
                == Some("gitlab asset link is not HTTPS eligible")));

        let redirected_decisions = score_assets(SourceProvider::GitLab, &host, &[redirected]);
        assert!(!redirected_decisions[0].eligible);
        assert_eq!(
            redirected_decisions[0].rejection_reason.as_deref(),
            Some("gitlab asset link is not HTTPS eligible")
        );
    }

    #[test]
    fn archive_binary_discovery_prefers_repo_name_and_reports_ambiguity() {
        assert_eq!(
            discover_archive_binary(
                "tool",
                &[
                    member("pkg/bin/helper", true),
                    member("pkg/bin/tool", true),
                    member("pkg/README.md", false),
                ],
            ),
            BinaryDiscovery::Selected("pkg/bin/tool".to_string())
        );
        assert_eq!(
            discover_archive_binary(
                "tool",
                &[member("pkg/bin/alpha", true), member("pkg/bin/beta", true)],
            ),
            BinaryDiscovery::Ambiguous(vec![
                "pkg/bin/alpha".to_string(),
                "pkg/bin/beta".to_string()
            ])
        );
    }
}
