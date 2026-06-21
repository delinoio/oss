use std::cmp::Ordering;

use reqwest::Url;
use serde::{Deserialize, Serialize};
use tracing::debug;

use crate::{
    contract::{ArchiveFormat, HostTarget, SourceProvider, TargetArch, TargetLibc, TargetOs},
    release::{ProviderAuth, ReleaseAsset},
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

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum CpuFeatureVariant {
    #[serde(rename = "baseline")]
    Baseline,
    #[serde(rename = "modern")]
    Modern,
}

impl CpuFeatureVariant {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Baseline => "baseline",
            Self::Modern => "modern",
        }
    }

    fn score_adjustment(self) -> i32 {
        match self {
            Self::Baseline => 4,
            Self::Modern => -4,
        }
    }
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

    pub fn is_installable(self) -> bool {
        matches!(self, Self::Archive(_) | Self::BareExecutable)
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CandidateDecision {
    pub asset_name: String,
    pub canonical_url: String,
    pub download_url: String,
    pub download_auth: Option<ProviderAuth>,
    pub download_accept: Option<&'static str>,
    pub kind: ArtifactKind,
    pub detected_os: Option<TargetOs>,
    pub detected_arch: Option<TargetArch>,
    pub detected_libc: Option<TargetLibc>,
    pub cpu_feature: Option<CpuFeatureVariant>,
    pub score: Option<i32>,
    pub eligible: bool,
    pub recognized_pattern: bool,
    pub rejection_reason: Option<String>,
}

impl CandidateDecision {
    pub fn explain_line(&self) -> String {
        if self.eligible {
            let cpu_feature = self
                .cpu_feature
                .map(|variant| format!(" cpu_feature={}", variant.as_str()))
                .unwrap_or_default();
            format!(
                "candidate {} kind={} score={} target={}/{}/{}{}",
                self.asset_name,
                self.kind.as_str(),
                self.score.unwrap_or_default(),
                self.detected_os.map(TargetOs::as_str).unwrap_or("unknown"),
                self.detected_arch
                    .map(TargetArch::as_str)
                    .unwrap_or("unknown"),
                self.detected_libc
                    .map(TargetLibc::as_str)
                    .unwrap_or("unknown"),
                cpu_feature
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
    pub missing_executable_metadata: bool,
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
    if is_bare_executable_name(&lower) {
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

pub fn discover_archive_binary(
    repo_name: &str,
    target: &HostTarget,
    members: &[ArchiveMember],
) -> BinaryDiscovery {
    let executable_candidates = members
        .iter()
        .filter(|member| member.executable)
        .map(|member| member.path.clone())
        .collect::<Vec<_>>();
    let executable_repo_discovery = discover_archive_binary_from_candidates(
        repo_name,
        target,
        executable_candidates.clone(),
        true,
    );
    if !matches!(executable_repo_discovery, BinaryDiscovery::NotFound) {
        return executable_repo_discovery;
    }

    let executable_discovery =
        discover_archive_binary_from_candidates(repo_name, target, executable_candidates, false);

    let recoverable_candidates = members
        .iter()
        .filter(|member| {
            member.missing_executable_metadata
                && recoverable_archive_binary_name(repo_name, target, &member.path)
        })
        .map(|member| member.path.clone())
        .collect::<Vec<_>>();
    let recoverable_discovery =
        discover_archive_binary_from_candidates(repo_name, target, recoverable_candidates, true);
    if !matches!(recoverable_discovery, BinaryDiscovery::NotFound) {
        return recoverable_discovery;
    }

    executable_discovery
}

fn discover_archive_binary_from_candidates(
    repo_name: &str,
    target: &HostTarget,
    mut candidates: Vec<String>,
    require_repo_name_match: bool,
) -> BinaryDiscovery {
    candidates.sort();

    if candidates.is_empty() {
        return BinaryDiscovery::NotFound;
    }

    let normalized_repo = normalized_binary_name(repo_name);
    let raw_matching_repo = candidates
        .iter()
        .filter(|candidate| normalized_binary_name(basename(candidate)) == normalized_repo)
        .cloned()
        .collect::<Vec<_>>();
    let matching_repo = target_archive_candidates(target, raw_matching_repo.clone());

    match matching_repo.len() {
        1 => return BinaryDiscovery::Selected(matching_repo[0].clone()),
        len if len > 1 => return BinaryDiscovery::Ambiguous(matching_repo),
        _ if !raw_matching_repo.is_empty() => return BinaryDiscovery::NotFound,
        _ => {}
    }

    if require_repo_name_match {
        return BinaryDiscovery::NotFound;
    }

    let candidates = target_archive_candidates(target, candidates);
    if candidates.is_empty() {
        return BinaryDiscovery::NotFound;
    }

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

fn recoverable_archive_binary_name(repo_name: &str, target: &HostTarget, path: &str) -> bool {
    let basename = basename(path);
    if target.os != TargetOs::Windows && basename.to_ascii_lowercase().ends_with(".exe") {
        return false;
    }
    normalized_binary_name(basename) == normalized_binary_name(repo_name)
}

pub(crate) fn target_archive_candidates(
    target: &HostTarget,
    candidates: Vec<String>,
) -> Vec<String> {
    let mut scored = candidates
        .into_iter()
        .filter_map(|candidate| {
            archive_member_target_score(target, &candidate).map(|score| (score, candidate))
        })
        .collect::<Vec<_>>();
    scored.sort_by(|(left_score, left_path), (right_score, right_path)| {
        right_score
            .cmp(left_score)
            .then_with(|| left_path.cmp(right_path))
    });
    match scored.first().map(|(score, _)| *score) {
        Some(best_score) if best_score > 0 => scored
            .into_iter()
            .filter_map(|(score, path)| (score == best_score).then_some(path))
            .collect(),
        Some(_) => scored.into_iter().map(|(_, path)| path).collect(),
        None => Vec::new(),
    }
}

fn archive_member_target_score(target: &HostTarget, path: &str) -> Option<i32> {
    let signal = detect_target(path);
    if signal.cpu_feature == Some(CpuFeatureVariant::Modern) {
        return None;
    }
    if signal.os.is_none() && signal.arch.is_none() && signal.libc.is_none() {
        return Some(0);
    }
    if signal.os.is_some() {
        return target_score(target, &signal);
    }

    let mut score = 0;
    if let Some(arch) = signal.arch {
        if arch != target.arch {
            return None;
        }
        score += 80;
    }
    match (target.os, target.libc, signal.libc) {
        (TargetOs::Linux, TargetLibc::Gnu, Some(TargetLibc::Gnu)) => score += 50,
        (TargetOs::Linux, TargetLibc::Gnu, Some(TargetLibc::Any)) => score += 45,
        (TargetOs::Linux, TargetLibc::Musl, Some(TargetLibc::Musl)) => score += 50,
        (TargetOs::Linux, TargetLibc::Musl, Some(TargetLibc::Any)) => score += 45,
        (TargetOs::Linux, _, Some(asset_libc)) if asset_libc == target.libc => score += 50,
        (TargetOs::Linux, _, Some(TargetLibc::Any)) => score += 45,
        (_, _, Some(TargetLibc::Any)) => score += 10,
        (_, _, Some(asset_libc)) if asset_libc == target.libc => score += 10,
        (_, _, None) => {}
        _ => return None,
    }
    if signal.recognized_pattern {
        score += 5;
    }
    if let Some(cpu_feature) = signal.cpu_feature {
        score += cpu_feature.score_adjustment();
    }
    Some(score)
}

fn score_asset(
    provider: SourceProvider,
    target: &HostTarget,
    asset: &ReleaseAsset,
) -> CandidateDecision {
    let download_url = asset
        .download_url
        .as_deref()
        .or(asset.provider_url.as_deref())
        .unwrap_or(&asset.url)
        .to_string();
    let canonical_url = asset
        .provider_url
        .as_deref()
        .unwrap_or(&asset.url)
        .to_string();
    let canonical_url = canonical_url
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
        download_auth: asset.download_auth.clone(),
        download_accept: asset.download_accept,
        kind,
        detected_os: target_signal.os,
        detected_arch: target_signal.arch,
        detected_libc: target_signal.libc,
        cpu_feature: target_signal.cpu_feature,
        score: None,
        eligible: false,
        recognized_pattern: target_signal.recognized_pattern,
        rejection_reason: None,
    };

    if provider == SourceProvider::GitLab {
        if let Some(reason) = gitlab_https_rejection_reason(asset) {
            decision.rejection_reason = Some(reason);
            log_candidate(target, &decision);
            return decision;
        }
    }

    if target_signal.cpu_feature == Some(CpuFeatureVariant::Modern) {
        decision.rejection_reason = Some(
            "CPU feature variant `modern` requires explicit host capability selection; baseline \
             or unspecified assets are preferred by default"
                .to_string(),
        );
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
        decision.rejection_reason = Some(target_rejection_reason(target, &target_signal));
        log_candidate(target, &decision);
        return decision;
    };

    decision.score = Some(score);
    decision.eligible = true;
    log_candidate(target, &decision);
    decision
}

pub(crate) fn gitlab_https_eligible(asset: &ReleaseAsset) -> bool {
    gitlab_https_rejection_reason(asset).is_none()
}

pub(crate) fn gitlab_https_rejection_reason(asset: &ReleaseAsset) -> Option<String> {
    if !is_https_url(&asset.url) {
        return Some(format!(
            "gitlab asset link URL is not HTTPS: {}; configure the GitLab release link to use \
             HTTPS or a secure direct asset URL",
            sanitized_origin(&asset.url)
        ));
    }

    if let Some(provider_url) = &asset.provider_url {
        if !is_https_url(provider_url) {
            return Some(format!(
                "gitlab direct asset URL is not HTTPS: {}; configure the GitLab release link to \
                 use HTTPS or omit the insecure direct asset URL",
                sanitized_origin(provider_url)
            ));
        }
    }

    if asset.final_url_https == Some(false) {
        let target = asset
            .final_url
            .as_deref()
            .map(sanitized_origin)
            .unwrap_or_else(|| "unknown redirect target".to_string());
        return Some(format!(
            "gitlab asset redirect target is not HTTPS: {target}; configure the GitLab release \
             link to redirect to HTTPS"
        ));
    }

    None
}

pub(crate) fn gitlab_https_diagnostic_url(asset: &ReleaseAsset) -> String {
    if asset.final_url_https == Some(false) {
        if let Some(final_url) = &asset.final_url {
            return sanitized_origin(final_url);
        }
    }
    let url = asset.provider_url.as_deref().unwrap_or(&asset.url);
    sanitized_origin(url)
}

fn sanitized_origin(raw: &str) -> String {
    let Ok(parsed) = Url::parse(raw) else {
        return sanitize_unparsed_url_like_input(raw);
    };
    let Some(host) = parsed.host_str() else {
        return format!("{}:", parsed.scheme());
    };
    match parsed.port() {
        Some(port) => format!("{}://{}:{}", parsed.scheme(), host, port),
        None => format!("{}://{}", parsed.scheme(), host),
    }
}

fn sanitize_unparsed_url_like_input(raw: &str) -> String {
    let without_query = raw.split(['?', '#']).next().unwrap_or(raw);
    let Some((scheme, rest)) = without_query.split_once("://") else {
        return without_query.to_string();
    };
    let authority_end = rest.find('/').unwrap_or(rest.len());
    let authority = &rest[..authority_end];
    let Some((_, hostish)) = authority.rsplit_once('@') else {
        return without_query.to_string();
    };

    format!("{scheme}://<redacted>@{hostish}{}", &rest[authority_end..])
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
    if let Some(cpu_feature) = signal.cpu_feature {
        score += cpu_feature.score_adjustment();
    }

    Some(score)
}

fn target_rejection_reason(target: &HostTarget, signal: &TargetSignal) -> String {
    if target.os == TargetOs::Linux
        && target.libc == TargetLibc::Musl
        && signal.os == Some(TargetOs::Linux)
        && signal.arch == Some(target.arch)
        && signal.libc.is_none()
    {
        return "linux musl target requires an explicit libc signal; rename the asset with musl, \
                static, portable, universal, or any, or add a target override if this binary is \
                known to be compatible"
            .to_string();
    }

    if target.arch == TargetArch::Armv7 && signal.os == Some(target.os) && signal.arch.is_none() {
        return "armv7 target requires an explicit architecture token such as armv7, armv7l, or \
                armhf"
            .to_string();
    }

    "asset target does not match host target".to_string()
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
    cpu_feature: Option<CpuFeatureVariant>,
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

    for (index, token) in tokens.iter().enumerate() {
        if signal.os.is_none() {
            signal.os = os_alias(token);
        }
        if signal.arch.is_none() {
            signal.arch = arch_alias(token);
        }
        if signal.libc.is_none() {
            signal.libc = libc_alias(token);
        }
        if signal.cpu_feature.is_none() && cpu_feature_token_has_target_context(&tokens, index) {
            signal.cpu_feature = cpu_feature_alias(token);
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
        "armv7" | "armv7l" | "armhf" => Some(TargetArch::Armv7),
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

fn cpu_feature_alias(token: &str) -> Option<CpuFeatureVariant> {
    match token {
        "baseline" => Some(CpuFeatureVariant::Baseline),
        "modern" => Some(CpuFeatureVariant::Modern),
        _ => None,
    }
}

fn cpu_feature_token_has_target_context(tokens: &[&str], index: usize) -> bool {
    if cpu_feature_alias(tokens[index]).is_none() {
        return false;
    }
    if index == 0 {
        return false;
    }
    let has_target_context = |token: &str| {
        os_alias(token).is_some() || arch_alias(token).is_some() || libc_alias(token).is_some()
    };
    index
        .checked_sub(1)
        .and_then(|previous| tokens.get(previous))
        .is_some_and(|token| has_target_context(token))
        || tokens
            .get(index + 1)
            .is_some_and(|token| has_target_context(token))
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

fn is_bare_executable_name(lower: &str) -> bool {
    let basename = basename(lower);
    if lower.ends_with(".exe") || !basename.contains('.') {
        return true;
    }

    basename
        .rsplit_once('.')
        .map(|(_, extension)| {
            extension.is_empty()
                || !extension
                    .chars()
                    .all(|character| character.is_ascii_alphanumeric())
        })
        .unwrap_or(false)
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
        classify_artifact, discover_archive_binary, score_assets, select_asset,
        target_archive_candidates, ArchiveMember, ArtifactKind, BinaryDiscovery, CpuFeatureVariant,
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
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
            final_url: None,
        }
    }

    fn target(os: TargetOs, arch: TargetArch, libc: TargetLibc) -> HostTarget {
        HostTarget { os, arch, libc }
    }

    fn member(path: &str, executable: bool) -> ArchiveMember {
        ArchiveMember {
            path: path.to_string(),
            executable,
            missing_executable_metadata: !executable,
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
        assert_eq!(
            classify_artifact("tool_1.2.3_linux_amd64", false),
            ArtifactKind::BareExecutable
        );
        assert_eq!(
            classify_artifact("tool-linux-amd64.txt", false),
            ArtifactKind::Unknown
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

        let mut release_asset = asset("tool-x86_64-unknown-linux-gnu");
        release_asset.url = "https://github.com/owner/tool/releases/download/v1/tool".to_string();
        release_asset.download_url =
            Some("https://api.github.com/repos/owner/tool/releases/assets/1".to_string());
        let selected =
            select_asset(SourceProvider::GitHub, &linux, &[release_asset]).expect("selected");

        assert_eq!(
            selected.selected.download_url,
            "https://api.github.com/repos/owner/tool/releases/assets/1"
        );
        assert_eq!(
            selected.selected.canonical_url,
            "https://github.com/owner/tool/releases/download/v1/tool"
        );
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
            Some(
                "linux musl target requires an explicit libc signal; rename the asset with musl, \
                 static, portable, universal, or any, or add a target override if this binary is \
                 known to be compatible"
            )
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
        let armv7 = target(TargetOs::Linux, TargetArch::Armv7, TargetLibc::Gnu);

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
        assert!(select_asset(
            SourceProvider::GitHub,
            &armv7,
            &[asset("tool_1.2.3_Linux_armv7.tar.gz")]
        )
        .is_some());
        assert!(select_asset(
            SourceProvider::GitHub,
            &armv7,
            &[asset("tool-linux-armv7l.tar.gz")]
        )
        .is_some());
        assert!(select_asset(
            SourceProvider::GitHub,
            &armv7,
            &[asset("tool-linux-armhf.tar.gz")]
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
        let mut malformed_link = asset("tool-x86_64-unknown-linux-gnu.tar.xz");
        malformed_link.url = "http://user:secret@[::1/tool.tar.xz?token=secret".to_string();
        let mut malformed_direct = asset("tool-x86_64-unknown-linux-gnu");
        malformed_direct.provider_url =
            Some("http://user:secret@[::1/tool?token=secret".to_string());
        let mut redirected = asset("tool-x86_64-unknown-linux-gnu.tgz");
        redirected.final_url_https = Some(false);
        redirected.final_url = Some("http://cdn.example.com/tool.tgz?token=secret".to_string());
        let decisions = score_assets(
            SourceProvider::GitLab,
            &host,
            &[link, direct, malformed_link, malformed_direct],
        );

        assert!(decisions.iter().all(|decision| !decision.eligible));
        assert!(decisions.iter().any(|decision| {
            decision
                .rejection_reason
                .as_deref()
                .is_some_and(|reason| reason.contains("gitlab asset link URL is not HTTPS"))
        }));
        assert!(decisions.iter().any(|decision| {
            decision
                .rejection_reason
                .as_deref()
                .is_some_and(|reason| reason.contains("gitlab direct asset URL is not HTTPS"))
        }));
        assert!(decisions.iter().any(|decision| {
            decision
                .rejection_reason
                .as_deref()
                .is_some_and(|reason| reason.contains("http://<redacted>@[::1/tool"))
        }));
        assert!(decisions.iter().all(|decision| decision
            .rejection_reason
            .as_deref()
            .is_none_or(|reason| !reason.contains("secret"))));

        let redirected_decisions = score_assets(SourceProvider::GitLab, &host, &[redirected]);
        assert!(!redirected_decisions[0].eligible);
        let reason = redirected_decisions[0]
            .rejection_reason
            .as_deref()
            .expect("redirect rejection reason");
        assert!(reason.contains("gitlab asset redirect target is not HTTPS"));
        assert!(reason.contains("http://cdn.example.com"));
        assert!(!reason.contains("secret"));
    }

    #[test]
    fn cpu_feature_variants_prefer_baseline_and_reject_modern_by_default() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        let decisions = score_assets(
            SourceProvider::GitHub,
            &host,
            &[
                asset("bun-linux-x64-modern.zip"),
                asset("bun-linux-x64-baseline.zip"),
            ],
        );

        assert_eq!(decisions[0].asset_name, "bun-linux-x64-baseline.zip");
        assert_eq!(decisions[0].cpu_feature, Some(CpuFeatureVariant::Baseline));
        let modern = decisions
            .iter()
            .find(|decision| decision.asset_name == "bun-linux-x64-modern.zip")
            .expect("modern decision");
        assert_eq!(modern.cpu_feature, Some(CpuFeatureVariant::Modern));
        assert!(!modern.eligible);
        assert!(modern
            .rejection_reason
            .as_deref()
            .is_some_and(|reason| reason.contains("CPU feature variant `modern`")));
    }

    #[test]
    fn cpu_feature_variants_are_detected_before_adjacent_target_tokens() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        let decisions = score_assets(
            SourceProvider::GitHub,
            &host,
            &[
                asset("bun-modern-linux-x64.zip"),
                asset("bun-baseline-linux-x64.zip"),
            ],
        );

        assert_eq!(decisions[0].asset_name, "bun-baseline-linux-x64.zip");
        assert_eq!(decisions[0].cpu_feature, Some(CpuFeatureVariant::Baseline));
        let modern = decisions
            .iter()
            .find(|decision| decision.asset_name == "bun-modern-linux-x64.zip")
            .expect("modern decision");
        assert_eq!(modern.cpu_feature, Some(CpuFeatureVariant::Modern));
        assert!(!modern.eligible);
    }

    #[test]
    fn modern_product_name_token_is_not_rejected_as_cpu_feature() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        let decisions = score_assets(
            SourceProvider::GitHub,
            &host,
            &[asset("modern-tool-linux-x64.tar.gz")],
        );

        assert_eq!(decisions[0].cpu_feature, None);
        assert!(decisions[0].eligible);
    }

    #[test]
    fn modern_product_name_before_target_suffix_is_not_rejected_as_cpu_feature() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        let decisions = score_assets(
            SourceProvider::GitHub,
            &host,
            &[asset("modern-linux-x64.tar.gz")],
        );

        assert_eq!(decisions[0].cpu_feature, None);
        assert!(decisions[0].eligible);
    }

    #[test]
    fn archive_member_discovery_rejects_modern_cpu_variant_by_default() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        let candidates = target_archive_candidates(
            &host,
            vec![
                "pkg/bin/linux-x64-modern/tool".to_string(),
                "pkg/bin/linux-x64-baseline/tool".to_string(),
            ],
        );

        assert_eq!(candidates, vec!["pkg/bin/linux-x64-baseline/tool"]);

        let modern_only =
            target_archive_candidates(&host, vec!["pkg/bin/linux-x64-modern/tool".to_string()]);
        assert!(modern_only.is_empty());
    }

    #[test]
    fn archive_binary_discovery_prefers_repo_name_and_reports_ambiguity() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        assert_eq!(
            discover_archive_binary(
                "tool",
                &host,
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
                &host,
                &[member("pkg/bin/alpha", true), member("pkg/bin/beta", true)],
            ),
            BinaryDiscovery::Ambiguous(vec![
                "pkg/bin/alpha".to_string(),
                "pkg/bin/beta".to_string()
            ])
        );
    }

    #[test]
    fn archive_binary_discovery_prefers_target_matching_members() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        assert_eq!(
            discover_archive_binary(
                "tool",
                &host,
                &[
                    member("bin/darwin/tool", true),
                    member("bin/linux/tool", true),
                ],
            ),
            BinaryDiscovery::Selected("bin/linux/tool".to_string())
        );
        assert_eq!(
            discover_archive_binary(
                "tool",
                &host,
                &[
                    member("bin/linux-arm64/tool", true),
                    member("bin/linux-x64/tool", true),
                ],
            ),
            BinaryDiscovery::Selected("bin/linux-x64/tool".to_string())
        );
        assert_eq!(
            discover_archive_binary(
                "tool",
                &host,
                &[member("bin/linux/helper", true), member("pkg/tool", true),],
            ),
            BinaryDiscovery::Selected("pkg/tool".to_string())
        );
    }

    #[test]
    fn archive_binary_discovery_recovers_missing_executable_metadata_for_repo_binary() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        assert_eq!(
            discover_archive_binary(
                "tool",
                &host,
                &[member("pkg/README.md", false), member("pkg/tool", false),],
            ),
            BinaryDiscovery::Selected("pkg/tool".to_string())
        );
        assert_eq!(
            discover_archive_binary(
                "tool",
                &host,
                &[
                    member("bin/darwin/tool", false),
                    member("bin/linux-x64/tool", false),
                ],
            ),
            BinaryDiscovery::Selected("bin/linux-x64/tool".to_string())
        );
        assert_eq!(
            discover_archive_binary(
                "tool",
                &host,
                &[member("pkg/install.sh", true), member("pkg/tool", false),],
            ),
            BinaryDiscovery::Selected("pkg/tool".to_string())
        );
    }

    #[test]
    fn archive_binary_discovery_does_not_guess_non_executable_non_repo_files() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        assert_eq!(
            discover_archive_binary(
                "tool",
                &host,
                &[member("pkg/alpha", false), member("pkg/beta", false)],
            ),
            BinaryDiscovery::NotFound
        );
        assert_eq!(
            discover_archive_binary(
                "tool",
                &host,
                &[
                    member("linux-x64/README", false),
                    member("linux-x64/LICENSE", false)
                ],
            ),
            BinaryDiscovery::NotFound
        );
    }

    #[test]
    fn archive_binary_discovery_does_not_recover_windows_exe_on_posix_target() {
        let host = target(TargetOs::Linux, TargetArch::X86_64, TargetLibc::Gnu);
        assert_eq!(
            discover_archive_binary("tool", &host, &[member("pkg/tool.exe", false)]),
            BinaryDiscovery::NotFound
        );
    }
}
