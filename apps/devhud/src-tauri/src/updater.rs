//! Closed desktop updater protocol and trust boundary.
//!
//! The frontend may select an action, but it cannot provide a URL, header,
//! public key, package type, or target. Those values are all native enums or
//! constants in this module.

use std::{
    collections::HashSet,
    fs,
    io::{Read, Write},
    path::{Path, PathBuf},
    process::{Child, Command},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

use base64::{Engine as _, engine::general_purpose::STANDARD as BASE64};
use ed25519_dalek::{Signature, StreamVerifier, Verifier, VerifyingKey};
use reqwest::{
    StatusCode,
    blocking::{Client, Response},
    header::{ACCEPT, CONTENT_LENGTH, LOCATION, RETRY_AFTER, USER_AGENT},
    redirect::Policy,
};
use semver::Version;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};
use url::Url;

use crate::platform::DesktopTarget;

pub const UPDATE_CHANNEL: &str = "stable";
pub const UPDATE_ORIGIN: &str = "https://devhud.api.delino.io";
pub const FIRST_CHECK_DELAY: Duration = Duration::from_secs(30);
pub const CHECK_INTERVAL: Duration = Duration::from_secs(24 * 60 * 60);
pub const MAX_MANIFEST_BYTES: usize = 256 * 1024;
pub const MAX_ARTIFACT_BYTES: u64 = 512 * 1024 * 1024;
pub const MAX_REDIRECTS: usize = 3;
pub const ROOT_KEY_ID: &str = "devhud-release-root-v1";
const CONNECT_TIMEOUT: Duration = Duration::from_secs(10);
const DISCOVERY_TIMEOUT: Duration = Duration::from_secs(30);
const DOWNLOAD_TIMEOUT: Duration = Duration::from_secs(2 * 60 * 60);
const ARTIFACT_SIGNATURE_DOMAIN: &[u8] = b"devhud-update-artifact-v1\0";

// This is a syntactically valid, public RFC 8032 test-vector key. Publication
// remains fail-closed until release engineering replaces it and flips the
// readiness gate. No private production key is present in the repository.
pub const ROOT_PUBLIC_KEY_BASE64: &str = "11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=";
pub const ROOT_FINGERPRINT: &str =
    "21fe31dfa154a261626bf854046fd2271b7bed4b6abe45aa58877ef47f9721b9";
pub const ROOT_PRODUCTION_READY: bool = false;
const HEALTH_FILE_PREFIX: &str = "devhud-update-health-v1-";
const HEALTH_ARGUMENT: &str = "--devhud-update-health-v1=";
const HEALTH_TOKEN_ARGUMENT: &str = "--devhud-update-health-token-v1=";

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum PackageKind {
    MacosApp,
    WindowsNsis,
    WindowsMsi,
    LinuxAppimage,
    LinuxDeb,
}

impl PackageKind {
    pub const fn header_value(self) -> &'static str {
        match self {
            Self::MacosApp => "macos-app",
            Self::WindowsNsis => "windows-nsis",
            Self::WindowsMsi => "windows-msi",
            Self::LinuxAppimage => "linux-appimage",
            Self::LinuxDeb => "linux-deb",
        }
    }

    pub const fn supported_by(self, target: DesktopTarget) -> bool {
        matches!(
            (target, self),
            (
                DesktopTarget::MacOsX64 | DesktopTarget::MacOsArm64,
                Self::MacosApp
            ) | (
                DesktopTarget::WindowsX64 | DesktopTarget::WindowsArm64,
                Self::WindowsNsis | Self::WindowsMsi
            ) | (
                DesktopTarget::LinuxX64 | DesktopTarget::LinuxArm64,
                Self::LinuxAppimage | Self::LinuxDeb
            )
        )
    }

    pub fn current(target: DesktopTarget) -> Result<Self, UpdaterError> {
        let configured = option_env!("DEVHUD_PACKAGE_KIND");
        let package = match configured {
            Some("macos-app") => Self::MacosApp,
            Some("windows-nsis") => Self::WindowsNsis,
            Some("windows-msi") => Self::WindowsMsi,
            Some("linux-appimage") => Self::LinuxAppimage,
            Some("linux-deb") => Self::LinuxDeb,
            Some(_) => {
                return Err(UpdaterError::new(
                    DiagnosticCode::Unsupported,
                    UpdatePhase::Target,
                ));
            }
            None => match target {
                DesktopTarget::MacOsX64 | DesktopTarget::MacOsArm64 => Self::MacosApp,
                DesktopTarget::WindowsX64 | DesktopTarget::WindowsArm64 => Self::WindowsNsis,
                DesktopTarget::LinuxX64 | DesktopTarget::LinuxArm64 => {
                    if std::env::var_os("APPIMAGE").is_some() {
                        Self::LinuxAppimage
                    } else {
                        Self::LinuxDeb
                    }
                }
            },
        };
        package
            .supported_by(target)
            .then_some(package)
            .ok_or_else(|| UpdaterError::new(DiagnosticCode::Unsupported, UpdatePhase::Target))
    }
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct ReleaseNotes {
    pub en: String,
    pub ko: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct Artifact {
    pub url: String,
    pub size: u64,
    pub sha256: String,
    pub signature: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct ManifestPayload {
    pub schema_version: u32,
    pub channel: String,
    pub platform: String,
    pub architecture: String,
    pub package_kind: PackageKind,
    pub version: String,
    pub published_at: String,
    pub release_notes: ReleaseNotes,
    pub artifact: Artifact,
    pub signer_fingerprint: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct KeySuccessor {
    pub predecessor_fingerprint: String,
    pub successor_fingerprint: String,
    pub public_key: String,
    pub valid_from: String,
    pub valid_until: String,
    pub signature: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct RollbackAuthorization {
    pub installed_version: String,
    pub candidate_version: String,
    pub channel: String,
    pub platform: String,
    pub architecture: String,
    pub package_kind: PackageKind,
    pub manifest_sha256: String,
    pub expires_at: String,
    pub signature: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct ManifestEnvelope {
    pub schema_version: u32,
    pub signed_payload: String,
    pub manifest_signature: String,
    #[serde(default)]
    pub key_chain: Vec<KeySuccessor>,
    pub rollback_authorization: Option<RollbackAuthorization>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct VerifiedCandidate {
    pub payload: ManifestPayload,
    pub version: Version,
    pub payload_bytes: Vec<u8>,
    pub terminal_key: VerifyingKey,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum DiagnosticCode {
    Offline,
    Malformed,
    RateLimited,
    Missing,
    Unsupported,
    Canceled,
    InvalidSignature,
    RollbackDenied,
    DownloadFailed,
    VerificationFailed,
    InstallationFailed,
    RestartFailed,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum UpdatePhase {
    Discovery,
    Target,
    Download,
    Verification,
    Installation,
    Restart,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateDiagnostic {
    pub code: DiagnosticCode,
    pub phase: UpdatePhase,
    pub target: String,
    pub package_kind: PackageKind,
    pub installed_version: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub candidate_version: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub http_status_class: Option<u16>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub retry_after_seconds: Option<u64>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct UpdaterError {
    pub code: DiagnosticCode,
    pub phase: UpdatePhase,
    pub status_class: Option<u16>,
    pub retry_after_seconds: Option<u64>,
}

impl UpdaterError {
    fn new(code: DiagnosticCode, phase: UpdatePhase) -> Self {
        Self {
            code,
            phase,
            status_class: None,
            retry_after_seconds: None,
        }
    }
}

pub fn endpoint(target: DesktopTarget) -> String {
    format!(
        "{UPDATE_ORIGIN}/updates/{UPDATE_CHANNEL}/{}/{}.json",
        target.update_platform(),
        target.update_architecture()
    )
}

pub fn root_ready_for_publication() -> Result<(), &'static str> {
    debug_assert_eq!(ROOT_KEY_ID, "devhud-release-root-v1");
    ROOT_PRODUCTION_READY
        .then_some(())
        .ok_or("devhud-release-root-v1-placeholder")
}

fn decode_key(encoded: &str) -> Result<VerifyingKey, UpdaterError> {
    let bytes = BASE64
        .decode(encoded)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Verification))?;
    let bytes: [u8; 32] = bytes
        .try_into()
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Verification))?;
    VerifyingKey::from_bytes(&bytes)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Verification))
}

fn decode_signature(encoded: &str) -> Result<Signature, UpdaterError> {
    let bytes = BASE64
        .decode(encoded)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Verification))?;
    Signature::from_slice(&bytes)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Verification))
}

fn fingerprint(key: &VerifyingKey) -> String {
    Sha256::digest(key.as_bytes())
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

fn parse_time(value: &str) -> Result<OffsetDateTime, UpdaterError> {
    OffsetDateTime::parse(value, &Rfc3339)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Verification))
}

fn successor_message(successor: &KeySuccessor) -> Vec<u8> {
    format!(
        "devhud-key-successor-v1\0{}\0{}\0{}\0{}\0{}",
        successor.predecessor_fingerprint,
        successor.successor_fingerprint,
        successor.public_key,
        successor.valid_from,
        successor.valid_until
    )
    .into_bytes()
}

fn rollback_message(authorization: &RollbackAuthorization) -> Vec<u8> {
    format!(
        "devhud-rollback-authorization-v1\0{}\0{}\0{}\0{}\0{}\0{}\0{}\0{}",
        authorization.installed_version,
        authorization.candidate_version,
        authorization.channel,
        authorization.platform,
        authorization.architecture,
        authorization.package_kind.header_value(),
        authorization.manifest_sha256,
        authorization.expires_at
    )
    .into_bytes()
}

pub fn verify_manifest(
    bytes: &[u8],
    installed_version: &Version,
    target: DesktopTarget,
    package_kind: PackageKind,
    now: OffsetDateTime,
) -> Result<VerifiedCandidate, UpdaterError> {
    if root_ready_for_publication().is_err() && !cfg!(test) {
        return Err(UpdaterError::new(
            DiagnosticCode::InvalidSignature,
            UpdatePhase::Verification,
        ));
    }
    if bytes.len() > MAX_MANIFEST_BYTES {
        return Err(UpdaterError::new(
            DiagnosticCode::Malformed,
            UpdatePhase::Discovery,
        ));
    }
    let envelope: ManifestEnvelope = serde_json::from_slice(bytes)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Discovery))?;
    if envelope.schema_version != 1 || envelope.key_chain.len() > 4 {
        return Err(UpdaterError::new(
            DiagnosticCode::Malformed,
            UpdatePhase::Verification,
        ));
    }

    let root = decode_key(ROOT_PUBLIC_KEY_BASE64)?;
    if fingerprint(&root) != ROOT_FINGERPRINT {
        return Err(UpdaterError::new(
            DiagnosticCode::InvalidSignature,
            UpdatePhase::Verification,
        ));
    }
    let mut trusted = root;
    let mut trusted_fingerprint = ROOT_FINGERPRINT.to_string();
    let mut seen = HashSet::from([trusted_fingerprint.clone()]);
    for successor in &envelope.key_chain {
        if successor.predecessor_fingerprint != trusted_fingerprint {
            return Err(UpdaterError::new(
                DiagnosticCode::InvalidSignature,
                UpdatePhase::Verification,
            ));
        }
        let next = decode_key(&successor.public_key)?;
        let next_fingerprint = fingerprint(&next);
        if successor.successor_fingerprint != next_fingerprint
            || !seen.insert(next_fingerprint.clone())
            || parse_time(&successor.valid_from)? > now
            || parse_time(&successor.valid_until)? < now
            || trusted
                .verify(
                    &successor_message(successor),
                    &decode_signature(&successor.signature)?,
                )
                .is_err()
        {
            return Err(UpdaterError::new(
                DiagnosticCode::InvalidSignature,
                UpdatePhase::Verification,
            ));
        }
        trusted = next;
        trusted_fingerprint = next_fingerprint;
    }

    let payload_bytes = BASE64
        .decode(&envelope.signed_payload)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Verification))?;
    let mut signed_message = b"devhud-update-manifest-v1\0".to_vec();
    signed_message.extend_from_slice(&payload_bytes);
    trusted
        .verify(
            &signed_message,
            &decode_signature(&envelope.manifest_signature)?,
        )
        .map_err(|_| {
            UpdaterError::new(DiagnosticCode::InvalidSignature, UpdatePhase::Verification)
        })?;
    let payload: ManifestPayload = serde_json::from_slice(&payload_bytes)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Verification))?;
    validate_payload(&payload, target, package_kind, &trusted_fingerprint)?;
    let version = Version::parse(&payload.version)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Verification))?;
    if !version.pre.is_empty() || !version.build.is_empty() {
        return Err(UpdaterError::new(
            DiagnosticCode::Unsupported,
            UpdatePhase::Verification,
        ));
    }
    if version < *installed_version {
        let authorization = envelope.rollback_authorization.as_ref().ok_or_else(|| {
            UpdaterError::new(DiagnosticCode::RollbackDenied, UpdatePhase::Verification)
        })?;
        verify_rollback(
            authorization,
            installed_version,
            &version,
            target,
            package_kind,
            &payload_bytes,
            now,
            &root,
        )?;
    }
    Ok(VerifiedCandidate {
        payload,
        version,
        payload_bytes,
        terminal_key: trusted,
    })
}

fn validate_payload(
    payload: &ManifestPayload,
    target: DesktopTarget,
    package_kind: PackageKind,
    signer_fingerprint: &str,
) -> Result<(), UpdaterError> {
    let malformed = || UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Verification);
    if payload.schema_version != 1
        || payload.channel != UPDATE_CHANNEL
        || payload.platform != target.update_platform()
        || payload.architecture != target.update_architecture()
        || payload.package_kind != package_kind
        || payload.signer_fingerprint != signer_fingerprint
        || payload.release_notes.en.is_empty()
        || payload.release_notes.ko.is_empty()
        || payload.release_notes.en.len() > 32 * 1024
        || payload.release_notes.ko.len() > 32 * 1024
        || payload.artifact.size == 0
        || payload.artifact.size > MAX_ARTIFACT_BYTES
        || payload.artifact.sha256.len() != 64
        || parse_time(&payload.published_at).is_err()
        || !package_kind.supported_by(target)
        || validate_artifact_url(&payload.artifact.url, &payload.version, package_kind).is_err()
    {
        return Err(malformed());
    }
    Ok(())
}

#[allow(clippy::too_many_arguments)]
fn verify_rollback(
    authorization: &RollbackAuthorization,
    installed: &Version,
    candidate: &Version,
    target: DesktopTarget,
    package_kind: PackageKind,
    payload: &[u8],
    now: OffsetDateTime,
    root: &VerifyingKey,
) -> Result<(), UpdaterError> {
    let manifest_sha256 = fingerprint_bytes(payload);
    if authorization.installed_version != installed.to_string()
        || authorization.candidate_version != candidate.to_string()
        || authorization.channel != UPDATE_CHANNEL
        || authorization.platform != target.update_platform()
        || authorization.architecture != target.update_architecture()
        || authorization.package_kind != package_kind
        || authorization.manifest_sha256 != manifest_sha256
        || parse_time(&authorization.expires_at)? < now
        || root
            .verify(
                &rollback_message(authorization),
                &decode_signature(&authorization.signature)?,
            )
            .is_err()
    {
        return Err(UpdaterError::new(
            DiagnosticCode::RollbackDenied,
            UpdatePhase::Verification,
        ));
    }
    Ok(())
}

fn fingerprint_bytes(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

pub fn validate_artifact_url(
    value: &str,
    version: &str,
    package_kind: PackageKind,
) -> Result<Url, UpdaterError> {
    let url = Url::parse(value)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Download))?;
    let prefix = format!("/delinoio/oss/releases/download/devhud%40v{version}/");
    let path_prefix = format!("/delinoio/oss/releases/download/devhud@v{version}/");
    if url.scheme() != "https"
        || url.host_str() != Some("github.com")
        || url.port_or_known_default() != Some(443)
        || (!url.path().starts_with(&prefix) && !url.path().starts_with(&path_prefix))
        || url.query().is_some()
        || url.fragment().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || !url.path().contains(package_kind.header_value())
    {
        return Err(UpdaterError::new(
            DiagnosticCode::Unsupported,
            UpdatePhase::Download,
        ));
    }
    Ok(url)
}

fn validate_redirect(value: &str) -> Result<Url, UpdaterError> {
    let url = Url::parse(value)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Download))?;
    if url.scheme() != "https"
        || url.host_str() != Some("release-assets.githubusercontent.com")
        || url.port_or_known_default() != Some(443)
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return Err(UpdaterError::new(
            DiagnosticCode::Unsupported,
            UpdatePhase::Download,
        ));
    }
    Ok(url)
}

pub struct UpdaterTransport {
    discovery_client: Client,
    download_client: Client,
}

impl UpdaterTransport {
    pub fn new() -> Result<Self, UpdaterError> {
        let discovery_client = Client::builder()
            .redirect(Policy::none())
            .connect_timeout(CONNECT_TIMEOUT)
            .timeout(DISCOVERY_TIMEOUT)
            .build()
            .map_err(|_| UpdaterError::new(DiagnosticCode::Offline, UpdatePhase::Discovery))?;
        let download_client = Client::builder()
            .redirect(Policy::none())
            .connect_timeout(CONNECT_TIMEOUT)
            .timeout(DOWNLOAD_TIMEOUT)
            .build()
            .map_err(|_| UpdaterError::new(DiagnosticCode::Offline, UpdatePhase::Discovery))?;
        Ok(Self {
            discovery_client,
            download_client,
        })
    }

    pub fn discover(
        &self,
        target: DesktopTarget,
        package_kind: PackageKind,
    ) -> Result<Vec<u8>, UpdaterError> {
        let response = self
            .discovery_client
            .get(endpoint(target))
            .header(
                ACCEPT,
                "application/vnd.devhud.update-manifest+json;version=1",
            )
            .header(USER_AGENT, "DevHud-Updater/1")
            .header("X-DevHud-Package", package_kind.header_value())
            .send()
            .map_err(|_| UpdaterError::new(DiagnosticCode::Offline, UpdatePhase::Discovery))?;
        read_manifest_response(response)
    }

    pub fn download(
        &self,
        candidate: &VerifiedCandidate,
        package_kind: PackageKind,
        canceled: &AtomicBool,
    ) -> Result<VerifiedArtifact, UpdaterError> {
        let mut url = validate_artifact_url(
            &candidate.payload.artifact.url,
            &candidate.payload.version,
            package_kind,
        )?;
        for redirect_count in 0..=MAX_REDIRECTS {
            if canceled.load(Ordering::Acquire) {
                return Err(UpdaterError::new(
                    DiagnosticCode::Canceled,
                    UpdatePhase::Download,
                ));
            }
            let response = self
                .download_client
                .get(url.clone())
                .header(ACCEPT, "application/octet-stream")
                .header(USER_AGENT, "DevHud-Updater/1")
                .send()
                .map_err(|_| {
                    UpdaterError::new(DiagnosticCode::DownloadFailed, UpdatePhase::Download)
                })?;
            if response.status().is_redirection() {
                if redirect_count == MAX_REDIRECTS {
                    return Err(UpdaterError::new(
                        DiagnosticCode::Unsupported,
                        UpdatePhase::Download,
                    ));
                }
                let location = response
                    .headers()
                    .get(LOCATION)
                    .and_then(|value| value.to_str().ok())
                    .ok_or_else(|| {
                        UpdaterError::new(DiagnosticCode::Malformed, UpdatePhase::Download)
                    })?;
                url = validate_redirect(location)?;
                continue;
            }
            return read_artifact_response(response, candidate, canceled);
        }
        unreachable!("redirect loop is bounded")
    }
}

fn read_manifest_response(response: Response) -> Result<Vec<u8>, UpdaterError> {
    let status = response.status();
    if status == StatusCode::TOO_MANY_REQUESTS {
        let retry_after_seconds = response
            .headers()
            .get(RETRY_AFTER)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.parse::<u64>().ok())
            .map(|value| value.min(24 * 60 * 60));
        return Err(UpdaterError {
            code: DiagnosticCode::RateLimited,
            phase: UpdatePhase::Discovery,
            status_class: Some(4),
            retry_after_seconds,
        });
    }
    if status == StatusCode::NOT_FOUND {
        return Err(UpdaterError::new(
            DiagnosticCode::Missing,
            UpdatePhase::Discovery,
        ));
    }
    if status.is_client_error() {
        let mut error = UpdaterError::new(DiagnosticCode::Unsupported, UpdatePhase::Discovery);
        error.status_class = Some(4);
        return Err(error);
    }
    if !status.is_success() {
        let mut error = UpdaterError::new(DiagnosticCode::Offline, UpdatePhase::Discovery);
        error.status_class = Some(status.as_u16() / 100);
        return Err(error);
    }
    if response
        .content_length()
        .is_some_and(|length| length > MAX_MANIFEST_BYTES as u64)
    {
        return Err(UpdaterError::new(
            DiagnosticCode::Malformed,
            UpdatePhase::Discovery,
        ));
    }
    let mut bytes = Vec::new();
    response
        .take((MAX_MANIFEST_BYTES + 1) as u64)
        .read_to_end(&mut bytes)
        .map_err(|_| UpdaterError::new(DiagnosticCode::Offline, UpdatePhase::Discovery))?;
    if bytes.len() > MAX_MANIFEST_BYTES {
        return Err(UpdaterError::new(
            DiagnosticCode::Malformed,
            UpdatePhase::Discovery,
        ));
    }
    Ok(bytes)
}

fn read_artifact_response(
    mut response: Response,
    candidate: &VerifiedCandidate,
    canceled: &AtomicBool,
) -> Result<VerifiedArtifact, UpdaterError> {
    if !response.status().is_success()
        || response
            .headers()
            .get(CONTENT_LENGTH)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.parse::<u64>().ok())
            .is_some_and(|length| length != candidate.payload.artifact.size)
    {
        return Err(UpdaterError::new(
            DiagnosticCode::DownloadFailed,
            UpdatePhase::Download,
        ));
    }
    let mut bytes =
        Vec::with_capacity(candidate.payload.artifact.size.min(16 * 1024 * 1024) as usize);
    let mut verifier = ArtifactVerifier::new(candidate)?;
    let mut buffer = [0_u8; 64 * 1024];
    loop {
        if canceled.load(Ordering::Acquire) {
            return Err(UpdaterError::new(
                DiagnosticCode::Canceled,
                UpdatePhase::Download,
            ));
        }
        let read = response.read(&mut buffer).map_err(|_| {
            UpdaterError::new(DiagnosticCode::DownloadFailed, UpdatePhase::Download)
        })?;
        if read == 0 {
            break;
        }
        if bytes.len().saturating_add(read) as u64 > candidate.payload.artifact.size {
            return Err(UpdaterError::new(
                DiagnosticCode::VerificationFailed,
                UpdatePhase::Verification,
            ));
        }
        verifier.update(&buffer[..read]);
        bytes.extend_from_slice(&buffer[..read]);
    }
    if canceled.load(Ordering::Acquire) {
        return Err(UpdaterError::new(
            DiagnosticCode::Canceled,
            UpdatePhase::Download,
        ));
    }
    verifier.finish(bytes)
}

#[derive(Debug)]
pub struct VerifiedArtifact(Vec<u8>);

struct ArtifactVerifier<'a> {
    candidate: &'a VerifiedCandidate,
    digest: Sha256,
    signature: StreamVerifier,
    received: u64,
}

impl<'a> ArtifactVerifier<'a> {
    fn new(candidate: &'a VerifiedCandidate) -> Result<Self, UpdaterError> {
        let signature = decode_signature(&candidate.payload.artifact.signature)?;
        let mut verifier = candidate
            .terminal_key
            .verify_stream(&signature)
            .map_err(|_| {
                UpdaterError::new(DiagnosticCode::InvalidSignature, UpdatePhase::Verification)
            })?;
        verifier.update(ARTIFACT_SIGNATURE_DOMAIN);
        Ok(Self {
            candidate,
            digest: Sha256::new(),
            signature: verifier,
            received: 0,
        })
    }

    fn update(&mut self, bytes: &[u8]) {
        self.received = self.received.saturating_add(bytes.len() as u64);
        self.digest.update(bytes);
        self.signature.update(bytes);
    }

    fn finish(self, bytes: Vec<u8>) -> Result<VerifiedArtifact, UpdaterError> {
        let digest = self
            .digest
            .finalize()
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>();
        if self.received != self.candidate.payload.artifact.size
            || digest != self.candidate.payload.artifact.sha256
        {
            return Err(UpdaterError::new(
                DiagnosticCode::VerificationFailed,
                UpdatePhase::Verification,
            ));
        }
        self.signature.finalize_and_verify().map_err(|_| {
            UpdaterError::new(DiagnosticCode::InvalidSignature, UpdatePhase::Verification)
        })?;
        Ok(VerifiedArtifact(bytes))
    }
}

#[cfg(test)]
fn verify_artifact(
    candidate: &VerifiedCandidate,
    artifact: Vec<u8>,
) -> Result<VerifiedArtifact, UpdaterError> {
    let mut verifier = ArtifactVerifier::new(candidate)?;
    verifier.update(&artifact);
    verifier.finish(artifact)
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum UpdaterStateKind {
    Idle,
    Checking,
    UpToDate,
    Available,
    Downloading,
    Downloaded,
    InstallationApproved,
    RestartRequired,
    Restarting,
    Failed,
    Canceled,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CandidateSummary {
    pub version: String,
    pub release_notes: ReleaseNotes,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdaterSnapshot {
    pub kind: UpdaterStateKind,
    pub installed_version: String,
    pub target: String,
    pub package_kind: PackageKind,
    pub candidate: Option<CandidateSummary>,
    pub diagnostic: Option<UpdateDiagnostic>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RestartDisposition {
    Relaunched,
    RestartRequired,
}

pub trait Installer: Send + Sync {
    fn install_and_restart(
        &self,
        verified_artifact: &[u8],
    ) -> Result<RestartDisposition, DiagnosticCode>;
    fn retry_restart(&self) -> Result<(), DiagnosticCode>;
}

/// Native-only package handoff. The frontend cannot select an executable,
/// arguments, destination, or package type. Release publication remains
/// blocked until the production trust root is provisioned, but this path is
/// still fail-closed so development builds cannot turn arbitrary bytes into a
/// general-purpose process launcher.
pub struct PlatformInstaller {
    package_kind: PackageKind,
}

impl PlatformInstaller {
    pub const fn new(package_kind: PackageKind) -> Self {
        Self { package_kind }
    }

    fn stage(&self, bytes: &[u8], suffix: &str) -> Result<tempfile::NamedTempFile, DiagnosticCode> {
        let mut file = tempfile::Builder::new()
            .prefix("devhud-update-v1-")
            .suffix(suffix)
            .tempfile()
            .map_err(|_| DiagnosticCode::InstallationFailed)?;
        file.write_all(bytes)
            .and_then(|_| file.as_file().sync_all())
            .map_err(|_| DiagnosticCode::InstallationFailed)?;
        Ok(file)
    }

    fn restart(executable: &Path) -> Result<(), DiagnosticCode> {
        let health_file = tempfile::Builder::new()
            .prefix(HEALTH_FILE_PREFIX)
            .tempfile()
            .map_err(|_| DiagnosticCode::RestartFailed)?;
        let file_name = health_file
            .path()
            .file_name()
            .and_then(|value| value.to_str())
            .ok_or(DiagnosticCode::RestartFailed)?;
        let token = uuid::Uuid::now_v7().to_string();
        let mut child = Command::new(executable)
            .arg(format!("{HEALTH_ARGUMENT}{file_name}"))
            .arg(format!("{HEALTH_TOKEN_ARGUMENT}{token}"))
            .spawn()
            .map_err(|_| DiagnosticCode::RestartFailed)?;
        wait_for_health(&mut child, health_file.path(), token.as_bytes())
    }

    fn restart_current() -> Result<(), DiagnosticCode> {
        let executable = std::env::current_exe().map_err(|_| DiagnosticCode::RestartFailed)?;
        Self::restart(&executable)
    }

    #[cfg(target_os = "linux")]
    fn install_linux(&self, bytes: &[u8]) -> Result<RestartDisposition, DiagnosticCode> {
        match self.package_kind {
            PackageKind::LinuxAppimage => self
                .install_appimage(bytes)
                .map(|()| RestartDisposition::Relaunched),
            PackageKind::LinuxDeb => {
                if !bytes.starts_with(b"!<arch>\n") {
                    return Err(DiagnosticCode::InstallationFailed);
                }
                let package = self.stage(bytes, ".deb")?;
                let status = Command::new("pkexec")
                    .args(["dpkg", "--install"])
                    .arg(package.path())
                    .status()
                    .map_err(|_| DiagnosticCode::InstallationFailed)?;
                if !status.success() {
                    return Err(DiagnosticCode::InstallationFailed);
                }
                Ok(match Self::restart_current() {
                    Ok(()) => RestartDisposition::Relaunched,
                    Err(_) => RestartDisposition::RestartRequired,
                })
            }
            _ => Err(DiagnosticCode::InstallationFailed),
        }
    }

    #[cfg(target_os = "linux")]
    fn install_appimage(&self, bytes: &[u8]) -> Result<(), DiagnosticCode> {
        use std::os::unix::fs::PermissionsExt;

        if !bytes.starts_with(b"\x7fELF") {
            return Err(DiagnosticCode::InstallationFailed);
        }
        let destination = std::env::var_os("APPIMAGE")
            .map(PathBuf::from)
            .ok_or(DiagnosticCode::InstallationFailed)?;
        let parent = destination
            .parent()
            .filter(|parent| !parent.as_os_str().is_empty())
            .ok_or(DiagnosticCode::InstallationFailed)?;
        let metadata =
            fs::metadata(&destination).map_err(|_| DiagnosticCode::InstallationFailed)?;
        let mut replacement = tempfile::Builder::new()
            .prefix(".devhud-update-v1-")
            .tempfile_in(parent)
            .map_err(|_| DiagnosticCode::InstallationFailed)?;
        replacement
            .write_all(bytes)
            .and_then(|_| replacement.as_file().sync_all())
            .and_then(|_| {
                fs::set_permissions(
                    replacement.path(),
                    fs::Permissions::from_mode(metadata.permissions().mode()),
                )
            })
            .map_err(|_| DiagnosticCode::InstallationFailed)?;
        replace_file_transactionally(&destination, replacement, Self::restart)
    }

    #[cfg(target_os = "windows")]
    fn install_windows(&self, bytes: &[u8]) -> Result<RestartDisposition, DiagnosticCode> {
        let (suffix, executable, arguments): (&str, &str, &[&str]) = match self.package_kind {
            PackageKind::WindowsNsis if bytes.starts_with(b"MZ") => {
                (".exe", "", &["/S", "/UPDATE"])
            }
            PackageKind::WindowsMsi if bytes.starts_with(&[0xd0, 0xcf, 0x11, 0xe0]) => {
                (".msi", "msiexec.exe", &["/i", "/passive", "/norestart"])
            }
            _ => return Err(DiagnosticCode::InstallationFailed),
        };
        let package = self.stage(bytes, suffix)?;
        let status = if executable.is_empty() {
            Command::new(package.path()).args(arguments).status()
        } else {
            let mut command = Command::new(executable);
            command
                .arg(arguments[0])
                .arg(package.path())
                .args(&arguments[1..]);
            command.status()
        }
        .map_err(|_| DiagnosticCode::InstallationFailed)?;
        if !status.success() {
            return Err(DiagnosticCode::InstallationFailed);
        }
        Ok(match Self::restart_current() {
            Ok(()) => RestartDisposition::Relaunched,
            Err(_) => RestartDisposition::RestartRequired,
        })
    }

    #[cfg(target_os = "macos")]
    fn install_macos(&self, bytes: &[u8]) -> Result<RestartDisposition, DiagnosticCode> {
        use std::path::Component;

        if self.package_kind != PackageKind::MacosApp || !bytes.starts_with(&[0x1f, 0x8b]) {
            return Err(DiagnosticCode::InstallationFailed);
        }
        let extraction = tempfile::Builder::new()
            .prefix("devhud-update-v1-")
            .tempdir()
            .map_err(|_| DiagnosticCode::InstallationFailed)?;
        let mut archive = tar::Archive::new(flate2::read::GzDecoder::new(bytes));
        for entry in archive
            .entries()
            .map_err(|_| DiagnosticCode::InstallationFailed)?
        {
            let mut entry = entry.map_err(|_| DiagnosticCode::InstallationFailed)?;
            let relative = entry
                .path()
                .map_err(|_| DiagnosticCode::InstallationFailed)?
                .into_owned();
            let entry_type = entry.header().entry_type();
            let safe_link = if entry_type.is_symlink() {
                entry
                    .link_name()
                    .map_err(|_| DiagnosticCode::InstallationFailed)?
                    .flatten()
                    .is_some_and(|target| {
                        !target.is_absolute()
                            && target
                                .components()
                                .all(|component| matches!(component, Component::Normal(_)))
                    })
            } else {
                true
            };
            if relative.is_absolute()
                || relative
                    .components()
                    .any(|component| !matches!(component, Component::Normal(_)))
                || !(entry_type.is_file() || entry_type.is_dir() || entry_type.is_symlink())
                || !safe_link
                || !entry
                    .unpack_in(extraction.path())
                    .map_err(|_| DiagnosticCode::InstallationFailed)?
            {
                return Err(DiagnosticCode::InstallationFailed);
            }
        }
        let apps = fs::read_dir(extraction.path())
            .map_err(|_| DiagnosticCode::InstallationFailed)?
            .filter_map(Result::ok)
            .map(|entry| entry.path())
            .filter(|path| path.extension().is_some_and(|extension| extension == "app"))
            .collect::<Vec<_>>();
        if apps.len() != 1 {
            return Err(DiagnosticCode::InstallationFailed);
        }
        let executable = std::env::current_exe().map_err(|_| DiagnosticCode::InstallationFailed)?;
        let destination = executable
            .ancestors()
            .find(|path| path.extension().is_some_and(|extension| extension == "app"))
            .map(Path::to_path_buf)
            .ok_or(DiagnosticCode::InstallationFailed)?;
        let parent = destination
            .parent()
            .ok_or(DiagnosticCode::InstallationFailed)?;
        let name = destination
            .file_name()
            .ok_or(DiagnosticCode::InstallationFailed)?;
        let backup = parent.join(format!(".{}.devhud-backup-v1", name.to_string_lossy()));
        if backup.exists() {
            return Err(DiagnosticCode::InstallationFailed);
        }
        fs::rename(&destination, &backup).map_err(|_| DiagnosticCode::InstallationFailed)?;
        if fs::rename(&apps[0], &destination).is_err() {
            let _ = fs::rename(&backup, &destination);
            return Err(DiagnosticCode::InstallationFailed);
        }
        let relaunch = destination.join("Contents/MacOS").join(
            executable
                .file_name()
                .ok_or(DiagnosticCode::RestartFailed)?,
        );
        if Self::restart(&relaunch).is_err() {
            let _ = fs::remove_dir_all(&destination);
            if fs::rename(&backup, &destination).is_err() {
                tracing::error!(
                    event = "updater_install_rollback_failed",
                    package = "macos-app"
                );
            }
            return Err(DiagnosticCode::RestartFailed);
        }
        let _ = fs::remove_dir_all(backup);
        Ok(RestartDisposition::Relaunched)
    }
}

fn wait_for_health(
    child: &mut Child,
    health_file: &Path,
    expected: &[u8],
) -> Result<(), DiagnosticCode> {
    for _ in 0..300 {
        if fs::read(health_file).is_ok_and(|value| value == expected) {
            return Ok(());
        }
        if child
            .try_wait()
            .map_err(|_| DiagnosticCode::RestartFailed)?
            .is_some()
        {
            return Err(DiagnosticCode::RestartFailed);
        }
        std::thread::sleep(Duration::from_millis(100));
    }
    let _ = child.kill();
    Err(DiagnosticCode::RestartFailed)
}

#[derive(Clone, Debug)]
pub struct UpdateHealthProbe {
    file_name: String,
    token: String,
}

impl UpdateHealthProbe {
    pub fn acknowledge(&self) -> bool {
        let path = std::env::temp_dir().join(&self.file_name);
        path.is_file() && fs::write(path, self.token.as_bytes()).is_ok()
    }
}

pub fn health_probe_from_args() -> Option<UpdateHealthProbe> {
    parse_health_probe(std::env::args_os().filter_map(|argument| argument.into_string().ok()))
}

fn parse_health_probe(arguments: impl IntoIterator<Item = String>) -> Option<UpdateHealthProbe> {
    let mut file_name = None;
    let mut token = None;
    for argument in arguments {
        if let Some(value) = argument.strip_prefix(HEALTH_ARGUMENT) {
            file_name = Some(value.to_string());
        } else if let Some(value) = argument.strip_prefix(HEALTH_TOKEN_ARGUMENT) {
            token = Some(value.to_string());
        }
    }
    let file_name = file_name?;
    let token = token?;
    if !file_name.starts_with(HEALTH_FILE_PREFIX)
        || file_name.len() > 128
        || !file_name
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.'))
        || uuid::Uuid::parse_str(&token).is_err()
    {
        return None;
    }
    Some(UpdateHealthProbe { file_name, token })
}

impl Installer for PlatformInstaller {
    fn install_and_restart(
        &self,
        verified_artifact: &[u8],
    ) -> Result<RestartDisposition, DiagnosticCode> {
        tracing::info!(
            event = "updater_install_started",
            package = self.package_kind.header_value()
        );
        #[cfg(target_os = "linux")]
        let result = self.install_linux(verified_artifact);
        #[cfg(target_os = "windows")]
        let result = self.install_windows(verified_artifact);
        #[cfg(target_os = "macos")]
        let result = self.install_macos(verified_artifact);
        match result {
            Ok(RestartDisposition::RestartRequired) => tracing::warn!(
                event = "updater_restart_required",
                package = self.package_kind.header_value()
            ),
            Err(code) => tracing::warn!(
                event = "updater_install_failed",
                package = self.package_kind.header_value(),
                code = ?code
            ),
            Ok(RestartDisposition::Relaunched) => {}
        }
        result
    }

    fn retry_restart(&self) -> Result<(), DiagnosticCode> {
        tracing::info!(
            event = "updater_restart_retry_started",
            package = self.package_kind.header_value()
        );
        let result = Self::restart_current();
        if let Err(code) = result {
            tracing::warn!(
                event = "updater_restart_retry_failed",
                package = self.package_kind.header_value(),
                code = ?code
            );
        }
        result
    }
}

#[cfg(target_os = "linux")]
fn sibling_backup_path(destination: &Path) -> Result<PathBuf, DiagnosticCode> {
    let file_name = destination
        .file_name()
        .and_then(|value| value.to_str())
        .ok_or(DiagnosticCode::InstallationFailed)?;
    let backup = destination.with_file_name(format!(".{file_name}.devhud-backup-v1"));
    if backup.exists() {
        return Err(DiagnosticCode::InstallationFailed);
    }
    Ok(backup)
}

#[cfg(target_os = "linux")]
fn replace_file_transactionally(
    destination: &Path,
    replacement: tempfile::NamedTempFile,
    relaunch: impl FnOnce(&Path) -> Result<(), DiagnosticCode>,
) -> Result<(), DiagnosticCode> {
    let backup = sibling_backup_path(destination)?;
    fs::copy(destination, &backup).map_err(|_| DiagnosticCode::InstallationFailed)?;
    if replacement.persist(destination).is_err() {
        let _ = fs::remove_file(&backup);
        return Err(DiagnosticCode::InstallationFailed);
    }
    if let Err(error) = relaunch(destination) {
        if fs::rename(&backup, destination).is_err() {
            tracing::error!(
                event = "updater_install_rollback_failed",
                package = "linux-appimage"
            );
        }
        return Err(error);
    }
    let _ = fs::remove_file(backup);
    Ok(())
}

pub struct UpdaterController {
    installed_version: Version,
    target: DesktopTarget,
    package_kind: PackageKind,
    snapshot: UpdaterSnapshot,
    candidate: Option<VerifiedCandidate>,
    artifact: Option<VerifiedArtifact>,
    canceled: Arc<AtomicBool>,
}

impl UpdaterController {
    pub fn new(
        installed_version: Version,
        target: DesktopTarget,
        package_kind: PackageKind,
    ) -> Result<Self, UpdaterError> {
        if !package_kind.supported_by(target) {
            return Err(UpdaterError::new(
                DiagnosticCode::Unsupported,
                UpdatePhase::Target,
            ));
        }
        Ok(Self {
            snapshot: UpdaterSnapshot {
                kind: UpdaterStateKind::Idle,
                installed_version: installed_version.to_string(),
                target: target.update_target_id().to_string(),
                package_kind,
                candidate: None,
                diagnostic: None,
            },
            installed_version,
            target,
            package_kind,
            candidate: None,
            artifact: None,
            canceled: Arc::new(AtomicBool::new(false)),
        })
    }

    pub fn snapshot(&self) -> UpdaterSnapshot {
        self.snapshot.clone()
    }

    pub fn begin_check(&mut self) -> bool {
        if !matches!(
            self.snapshot.kind,
            UpdaterStateKind::Idle
                | UpdaterStateKind::UpToDate
                | UpdaterStateKind::Available
                | UpdaterStateKind::Failed
                | UpdaterStateKind::Canceled
        ) {
            return false;
        }
        self.candidate = None;
        self.artifact = None;
        self.snapshot.candidate = None;
        self.snapshot.kind = UpdaterStateKind::Checking;
        self.snapshot.diagnostic = None;
        true
    }

    pub fn check_bytes(&mut self, manifest: &[u8], now: OffsetDateTime) {
        self.snapshot.kind = UpdaterStateKind::Checking;
        self.snapshot.diagnostic = None;
        match verify_manifest(
            manifest,
            &self.installed_version,
            self.target,
            self.package_kind,
            now,
        ) {
            Ok(candidate) if candidate.version == self.installed_version => {
                self.candidate = None;
                self.snapshot.candidate = None;
                self.snapshot.kind = UpdaterStateKind::UpToDate;
            }
            Ok(candidate) => {
                self.snapshot.candidate = Some(CandidateSummary {
                    version: candidate.version.to_string(),
                    release_notes: candidate.payload.release_notes.clone(),
                });
                self.candidate = Some(candidate);
                self.snapshot.kind = UpdaterStateKind::Available;
            }
            Err(error) => self.fail(error),
        }
    }

    pub fn record_error(&mut self, error: UpdaterError) {
        self.fail(error);
    }

    pub fn begin_download(&mut self) -> Result<(), UpdaterError> {
        if self.snapshot.kind != UpdaterStateKind::Available {
            return Err(UpdaterError::new(
                DiagnosticCode::Unsupported,
                UpdatePhase::Download,
            ));
        }
        // A new download receives a new token so an older canceled worker can
        // never be revived by a later retry.
        self.canceled = Arc::new(AtomicBool::new(false));
        self.snapshot.kind = UpdaterStateKind::Downloading;
        Ok(())
    }

    pub fn candidate(&self) -> Option<&VerifiedCandidate> {
        self.candidate.as_ref()
    }

    pub fn cancellation_token(&self) -> Arc<AtomicBool> {
        self.canceled.clone()
    }

    pub fn complete_download(&mut self, artifact: VerifiedArtifact) -> Result<(), UpdaterError> {
        if self.snapshot.kind != UpdaterStateKind::Downloading
            || self.canceled.load(Ordering::Acquire)
        {
            return Err(UpdaterError::new(
                DiagnosticCode::Canceled,
                UpdatePhase::Download,
            ));
        }
        self.artifact = Some(artifact);
        self.snapshot.kind = UpdaterStateKind::Downloaded;
        Ok(())
    }

    pub fn cancel(&mut self) {
        self.canceled.store(true, Ordering::Release);
        self.artifact = None;
        self.snapshot.kind = UpdaterStateKind::Canceled;
        self.snapshot.diagnostic = Some(self.diagnostic(UpdaterError::new(
            DiagnosticCode::Canceled,
            UpdatePhase::Download,
        )));
    }

    pub fn approve_installation(&mut self) -> Result<(), UpdaterError> {
        if self.snapshot.kind != UpdaterStateKind::Downloaded || self.artifact.is_none() {
            return Err(UpdaterError::new(
                DiagnosticCode::Unsupported,
                UpdatePhase::Installation,
            ));
        }
        self.snapshot.kind = UpdaterStateKind::InstallationApproved;
        Ok(())
    }

    pub fn approve_restart(
        &mut self,
        installer: &dyn Installer,
    ) -> Result<RestartDisposition, UpdaterError> {
        let retrying = self.snapshot.kind == UpdaterStateKind::RestartRequired;
        let result = match self.snapshot.kind {
            UpdaterStateKind::InstallationApproved => {
                let artifact = self.artifact.as_ref().ok_or_else(|| {
                    UpdaterError::new(
                        DiagnosticCode::InstallationFailed,
                        UpdatePhase::Installation,
                    )
                })?;
                installer.install_and_restart(&artifact.0)
            }
            UpdaterStateKind::RestartRequired => installer
                .retry_restart()
                .map(|()| RestartDisposition::Relaunched),
            _ => {
                return Err(UpdaterError::new(
                    DiagnosticCode::Unsupported,
                    UpdatePhase::Restart,
                ));
            }
        };
        match result {
            Ok(RestartDisposition::Relaunched) => {
                // The old process continues to report its running version until
                // the health-checked replacement has started successfully.
                self.artifact = None;
                self.snapshot.kind = UpdaterStateKind::Restarting;
                self.snapshot.diagnostic = None;
                Ok(RestartDisposition::Relaunched)
            }
            Ok(RestartDisposition::RestartRequired) => {
                self.require_restart();
                Ok(RestartDisposition::RestartRequired)
            }
            Err(code) => {
                let phase = if code == DiagnosticCode::RestartFailed {
                    UpdatePhase::Restart
                } else {
                    UpdatePhase::Installation
                };
                let error = UpdaterError::new(code, phase);
                if retrying {
                    self.require_restart();
                } else {
                    self.fail(error.clone());
                }
                Err(error)
            }
        }
    }

    fn require_restart(&mut self) {
        self.artifact = None;
        self.snapshot.kind = UpdaterStateKind::RestartRequired;
        self.snapshot.diagnostic = Some(self.diagnostic(UpdaterError::new(
            DiagnosticCode::RestartFailed,
            UpdatePhase::Restart,
        )));
    }

    fn fail(&mut self, error: UpdaterError) {
        self.snapshot.kind = if error.code == DiagnosticCode::Canceled {
            UpdaterStateKind::Canceled
        } else {
            UpdaterStateKind::Failed
        };
        self.snapshot.diagnostic = Some(self.diagnostic(error));
    }

    fn diagnostic(&self, error: UpdaterError) -> UpdateDiagnostic {
        UpdateDiagnostic {
            code: error.code,
            phase: error.phase,
            target: self.target.update_target_id().to_string(),
            package_kind: self.package_kind,
            installed_version: self.installed_version.to_string(),
            candidate_version: self
                .candidate
                .as_ref()
                .map(|candidate| candidate.version.to_string()),
            http_status_class: error.status_class,
            retry_after_seconds: error.retry_after_seconds,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct CheckSchedule {
    next_due_seconds: u64,
}

impl CheckSchedule {
    pub const fn after_frontend_ready() -> Self {
        Self {
            next_due_seconds: FIRST_CHECK_DELAY.as_secs(),
        }
    }

    pub const fn next_due_seconds(self) -> u64 {
        self.next_due_seconds
    }

    pub fn mark_checked(&mut self, active_runtime_seconds: u64) {
        self.next_due_seconds = active_runtime_seconds.saturating_add(CHECK_INTERVAL.as_secs());
    }

    pub const fn is_due(self, active_runtime_seconds: u64) -> bool {
        active_runtime_seconds >= self.next_due_seconds
    }
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::AtomicUsize;

    use ed25519_dalek::{Signer, SigningKey};
    use serde_json::json;

    use super::*;

    const RFC_ROOT_SEED: [u8; 32] = [
        0x9d, 0x61, 0xb1, 0x9d, 0xef, 0xfd, 0x5a, 0x60, 0xba, 0x84, 0x4a, 0xf4, 0x92, 0xec, 0x2c,
        0xc4, 0x44, 0x49, 0xc5, 0x69, 0x7b, 0x32, 0x69, 0x19, 0x70, 0x3b, 0xac, 0x03, 0x1c, 0xae,
        0x7f, 0x60,
    ];

    fn target() -> DesktopTarget {
        DesktopTarget::LinuxX64
    }

    fn package() -> PackageKind {
        PackageKind::LinuxAppimage
    }

    fn fixture(
        version: &str,
        signer: &SigningKey,
        chain: Vec<KeySuccessor>,
        rollback: Option<RollbackAuthorization>,
    ) -> Vec<u8> {
        let artifact = b"deterministic updater artifact";
        let mut artifact_message = b"devhud-update-artifact-v1\0".to_vec();
        artifact_message.extend_from_slice(artifact);
        let payload = json!({
            "schemaVersion": 1,
            "channel": "stable",
            "platform": "linux",
            "architecture": "x86_64",
            "packageKind": "linux-appimage",
            "version": version,
            "publishedAt": "2026-08-20T00:00:00Z",
            "releaseNotes": { "en": "Verified update", "ko": "검증된 업데이트" },
            "artifact": {
                "url": format!("https://github.com/delinoio/oss/releases/download/devhud@v{version}/devhud-linux-appimage"),
                "size": artifact.len(),
                "sha256": fingerprint_bytes(artifact),
                "signature": BASE64.encode(signer.sign(&artifact_message).to_bytes()),
            },
            "signerFingerprint": fingerprint(&signer.verifying_key()),
        });
        let payload_bytes = serde_json::to_vec(&payload).unwrap();
        let mut message = b"devhud-update-manifest-v1\0".to_vec();
        message.extend_from_slice(&payload_bytes);
        serde_json::to_vec(&ManifestEnvelope {
            schema_version: 1,
            signed_payload: BASE64.encode(payload_bytes),
            manifest_signature: BASE64.encode(signer.sign(&message).to_bytes()),
            key_chain: chain,
            rollback_authorization: rollback,
        })
        .unwrap()
    }

    fn now() -> OffsetDateTime {
        OffsetDateTime::parse("2026-08-20T12:00:00Z", &Rfc3339).unwrap()
    }

    #[test]
    fn committed_fixtures_cover_signature_rotation_and_rollback() {
        let signed = include_bytes!("../fixtures/updater/signed.json");
        let invalid = include_bytes!("../fixtures/updater/invalid-signature.json");
        let rotation = include_bytes!("../fixtures/updater/rotation.json");
        let rollback = include_bytes!("../fixtures/updater/rollback-authorized.json");
        assert!(
            verify_manifest(signed, &Version::new(0, 1, 0), target(), package(), now()).is_ok()
        );
        assert_eq!(
            verify_manifest(invalid, &Version::new(0, 1, 0), target(), package(), now())
                .unwrap_err()
                .code,
            DiagnosticCode::InvalidSignature
        );
        assert!(
            verify_manifest(rotation, &Version::new(0, 1, 0), target(), package(), now()).is_ok()
        );
        assert!(
            verify_manifest(rollback, &Version::new(0, 2, 0), target(), package(), now()).is_ok()
        );
    }

    fn successor(root: &SigningKey, next: &SigningKey) -> KeySuccessor {
        let mut certificate = KeySuccessor {
            predecessor_fingerprint: fingerprint(&root.verifying_key()),
            successor_fingerprint: fingerprint(&next.verifying_key()),
            public_key: BASE64.encode(next.verifying_key().as_bytes()),
            valid_from: "2026-01-01T00:00:00Z".into(),
            valid_until: "2027-01-01T00:00:00Z".into(),
            signature: String::new(),
        };
        certificate.signature =
            BASE64.encode(root.sign(&successor_message(&certificate)).to_bytes());
        certificate
    }

    #[test]
    fn maps_every_target_to_the_fixed_endpoint() {
        let cases = [
            (DesktopTarget::MacOsX64, "darwin/x86_64"),
            (DesktopTarget::MacOsArm64, "darwin/aarch64"),
            (DesktopTarget::WindowsX64, "windows/x86_64"),
            (DesktopTarget::WindowsArm64, "windows/aarch64"),
            (DesktopTarget::LinuxX64, "linux/x86_64"),
            (DesktopTarget::LinuxArm64, "linux/aarch64"),
        ];
        for (target, suffix) in cases {
            assert_eq!(
                endpoint(target),
                format!("{UPDATE_ORIGIN}/updates/stable/{suffix}.json")
            );
        }
    }

    #[test]
    fn accepts_a_signed_manifest_and_rejects_tampering() {
        let root = SigningKey::from_bytes(&RFC_ROOT_SEED);
        let signed = fixture("0.2.0", &root, vec![], None);
        assert!(
            verify_manifest(&signed, &Version::new(0, 1, 0), target(), package(), now()).is_ok()
        );
        let mut envelope: ManifestEnvelope = serde_json::from_slice(&signed).unwrap();
        let mut payload = BASE64.decode(&envelope.signed_payload).unwrap();
        payload[0] ^= 1;
        envelope.signed_payload = BASE64.encode(payload);
        let tampered = serde_json::to_vec(&envelope).unwrap();
        assert_eq!(
            verify_manifest(
                &tampered,
                &Version::new(0, 1, 0),
                target(),
                package(),
                now()
            )
            .unwrap_err()
            .code,
            DiagnosticCode::InvalidSignature
        );
    }

    #[test]
    fn selects_only_the_exact_target_package_and_newer_version() {
        let root = SigningKey::from_bytes(&RFC_ROOT_SEED);
        let signed = fixture("0.2.0", &root, vec![], None);
        assert_eq!(
            verify_manifest(
                &signed,
                &Version::new(0, 1, 0),
                DesktopTarget::LinuxArm64,
                package(),
                now()
            )
            .unwrap_err()
            .code,
            DiagnosticCode::Malformed
        );
        let same = fixture("0.1.0", &root, vec![], None);
        let mut controller =
            UpdaterController::new(Version::new(0, 1, 0), target(), package()).unwrap();
        assert!(controller.begin_check());
        assert!(!controller.begin_check());
        assert!(controller.begin_download().is_err());
        controller.check_bytes(&same, now());
        assert_eq!(controller.snapshot().kind, UpdaterStateKind::UpToDate);
        controller.check_bytes(&signed, now());
        assert_eq!(controller.snapshot().kind, UpdaterStateKind::Available);
        assert_eq!(controller.snapshot().candidate.unwrap().version, "0.2.0");
        for unstable_version in ["0.3.0-rc.1", "0.3.0+rebuilt"] {
            assert_eq!(
                verify_manifest(
                    &fixture(unstable_version, &root, vec![], None),
                    &Version::new(0, 1, 0),
                    target(),
                    package(),
                    now()
                )
                .unwrap_err()
                .code,
                DiagnosticCode::Unsupported
            );
        }
    }

    #[test]
    fn accepts_only_a_valid_successor_chain() {
        let root = SigningKey::from_bytes(&RFC_ROOT_SEED);
        let next = SigningKey::from_bytes(&[19; 32]);
        let certificate = successor(&root, &next);
        let signed = fixture("0.2.0", &next, vec![certificate.clone()], None);
        assert!(
            verify_manifest(&signed, &Version::new(0, 1, 0), target(), package(), now()).is_ok()
        );
        let mut invalid = certificate;
        invalid.successor_fingerprint = ROOT_FINGERPRINT.into();
        assert_eq!(
            verify_manifest(
                &fixture("0.2.0", &next, vec![invalid], None),
                &Version::new(0, 1, 0),
                target(),
                package(),
                now()
            )
            .unwrap_err()
            .code,
            DiagnosticCode::InvalidSignature
        );
    }

    #[test]
    fn rollback_requires_an_exact_root_authorization() {
        let root = SigningKey::from_bytes(&RFC_ROOT_SEED);
        let unsigned = fixture("0.1.0", &root, vec![], None);
        assert_eq!(
            verify_manifest(
                &unsigned,
                &Version::new(0, 2, 0),
                target(),
                package(),
                now()
            )
            .unwrap_err()
            .code,
            DiagnosticCode::RollbackDenied
        );
        let envelope: ManifestEnvelope = serde_json::from_slice(&unsigned).unwrap();
        let payload = BASE64.decode(&envelope.signed_payload).unwrap();
        let mut authorization = RollbackAuthorization {
            installed_version: "0.2.0".into(),
            candidate_version: "0.1.0".into(),
            channel: "stable".into(),
            platform: "linux".into(),
            architecture: "x86_64".into(),
            package_kind: package(),
            manifest_sha256: fingerprint_bytes(&payload),
            expires_at: "2026-09-01T00:00:00Z".into(),
            signature: String::new(),
        };
        authorization.signature =
            BASE64.encode(root.sign(&rollback_message(&authorization)).to_bytes());
        let authorized = fixture("0.1.0", &root, vec![], Some(authorization));
        assert!(
            verify_manifest(
                &authorized,
                &Version::new(0, 2, 0),
                target(),
                package(),
                now()
            )
            .is_ok()
        );
    }

    #[test]
    fn redirect_and_header_policy_is_closed() {
        assert!(validate_artifact_url("https://github.com/delinoio/oss/releases/download/devhud@v0.2.0/devhud-linux-appimage", "0.2.0", package()).is_ok());
        assert!(validate_artifact_url("https://example.com/file", "0.2.0", package()).is_err());
        assert!(
            validate_redirect(
                "https://release-assets.githubusercontent.com/github-production-release-asset/file"
            )
            .is_ok()
        );
        assert!(validate_redirect("http://release-assets.githubusercontent.com/file").is_err());
        assert!(validate_redirect("https://github.com/file").is_err());
    }

    struct FailingInstaller(DiagnosticCode);
    impl Installer for FailingInstaller {
        fn install_and_restart(
            &self,
            _artifact: &[u8],
        ) -> Result<RestartDisposition, DiagnosticCode> {
            Err(self.0)
        }

        fn retry_restart(&self) -> Result<(), DiagnosticCode> {
            Err(self.0)
        }
    }

    struct RestartRequiredInstaller {
        installs: AtomicUsize,
        retries: AtomicUsize,
    }

    impl Installer for RestartRequiredInstaller {
        fn install_and_restart(
            &self,
            _artifact: &[u8],
        ) -> Result<RestartDisposition, DiagnosticCode> {
            self.installs.fetch_add(1, Ordering::SeqCst);
            Ok(RestartDisposition::RestartRequired)
        }

        fn retry_restart(&self) -> Result<(), DiagnosticCode> {
            self.retries.fetch_add(1, Ordering::SeqCst);
            Ok(())
        }
    }

    #[test]
    fn every_failure_and_cancellation_preserves_the_installed_version() {
        let root = SigningKey::from_bytes(&RFC_ROOT_SEED);
        let signed = fixture("0.2.0", &root, vec![], None);
        let candidate =
            verify_manifest(&signed, &Version::new(0, 1, 0), target(), package(), now()).unwrap();
        let artifact = b"deterministic updater artifact".to_vec();
        for code in [
            DiagnosticCode::InstallationFailed,
            DiagnosticCode::RestartFailed,
        ] {
            let mut controller =
                UpdaterController::new(Version::new(0, 1, 0), target(), package()).unwrap();
            controller.check_bytes(&signed, now());
            controller.begin_download().unwrap();
            controller
                .complete_download(verify_artifact(&candidate, artifact.clone()).unwrap())
                .unwrap();
            controller.approve_installation().unwrap();
            assert!(controller.approve_restart(&FailingInstaller(code)).is_err());
            assert_eq!(controller.snapshot().installed_version, "0.1.0");
            assert_eq!(controller.candidate().unwrap().version, candidate.version);
        }
        let mut controller =
            UpdaterController::new(Version::new(0, 1, 0), target(), package()).unwrap();
        controller.check_bytes(&signed, now());
        controller.begin_download().unwrap();
        let canceled_worker = controller.cancellation_token();
        let verified_artifact = verify_artifact(&candidate, artifact).unwrap();
        controller.cancel();
        assert_eq!(
            controller
                .complete_download(verified_artifact)
                .unwrap_err()
                .code,
            DiagnosticCode::Canceled
        );
        assert_eq!(controller.snapshot().installed_version, "0.1.0");
        assert_eq!(controller.snapshot().kind, UpdaterStateKind::Canceled);
        controller.check_bytes(&signed, now());
        controller.begin_download().unwrap();
        assert!(canceled_worker.load(Ordering::Acquire));
        assert!(!controller.cancellation_token().load(Ordering::Acquire));
    }

    #[test]
    fn artifact_verification_streams_and_hands_off_verified_bytes_once() {
        let root = SigningKey::from_bytes(&RFC_ROOT_SEED);
        let signed = fixture("0.2.0", &root, vec![], None);
        let candidate =
            verify_manifest(&signed, &Version::new(0, 1, 0), target(), package(), now()).unwrap();
        let artifact = b"deterministic updater artifact".to_vec();
        let split = artifact.len() / 2;
        let mut verifier = ArtifactVerifier::new(&candidate).unwrap();
        verifier.update(&artifact[..split]);
        verifier.update(&artifact[split..]);
        let verified = verifier.finish(artifact.clone()).unwrap();
        assert_eq!(verified.0, artifact);

        let mut tampered = b"deterministic updater artifact".to_vec();
        tampered[0] ^= 1;
        assert_eq!(
            verify_artifact(&candidate, tampered).unwrap_err().code,
            DiagnosticCode::VerificationFailed
        );
    }

    #[test]
    fn package_install_restart_failure_is_retried_without_reinstalling() {
        let root = SigningKey::from_bytes(&RFC_ROOT_SEED);
        let signed = fixture("0.2.0", &root, vec![], None);
        let candidate =
            verify_manifest(&signed, &Version::new(0, 1, 0), target(), package(), now()).unwrap();
        let mut controller =
            UpdaterController::new(Version::new(0, 1, 0), target(), package()).unwrap();
        controller.check_bytes(&signed, now());
        controller.begin_download().unwrap();
        controller
            .complete_download(
                verify_artifact(&candidate, b"deterministic updater artifact".to_vec()).unwrap(),
            )
            .unwrap();
        controller.approve_installation().unwrap();
        let installer = RestartRequiredInstaller {
            installs: AtomicUsize::new(0),
            retries: AtomicUsize::new(0),
        };

        assert_eq!(
            controller.approve_restart(&installer).unwrap(),
            RestartDisposition::RestartRequired
        );
        assert_eq!(
            controller.snapshot().kind,
            UpdaterStateKind::RestartRequired
        );
        assert_eq!(
            controller.snapshot().diagnostic.unwrap().code,
            DiagnosticCode::RestartFailed
        );
        assert_eq!(
            controller.approve_restart(&installer).unwrap(),
            RestartDisposition::Relaunched
        );
        assert_eq!(controller.snapshot().kind, UpdaterStateKind::Restarting);
        assert_eq!(installer.installs.load(Ordering::SeqCst), 1);
        assert_eq!(installer.retries.load(Ordering::SeqCst), 1);
    }

    #[test]
    fn schedule_is_stable_and_resume_checks_overdue_work() {
        let mut schedule = CheckSchedule::after_frontend_ready();
        assert!(!schedule.is_due(29));
        assert!(schedule.is_due(30));
        schedule.mark_checked(30);
        assert_eq!(schedule.next_due_seconds(), 30 + 86_400);
        assert!(schedule.is_due(30 + 86_400 + 20));
    }

    #[test]
    fn release_gate_rejects_the_placeholder_root() {
        assert_eq!(
            root_ready_for_publication(),
            Err("devhud-release-root-v1-placeholder")
        );
    }

    #[test]
    fn restart_health_capability_rejects_path_and_token_injection() {
        let token = uuid::Uuid::now_v7().to_string();
        assert!(
            parse_health_probe([
                format!("{HEALTH_ARGUMENT}{HEALTH_FILE_PREFIX}fixture"),
                format!("{HEALTH_TOKEN_ARGUMENT}{token}"),
            ])
            .is_some()
        );
        assert!(
            parse_health_probe([
                format!("{HEALTH_ARGUMENT}../escape"),
                format!("{HEALTH_TOKEN_ARGUMENT}{token}"),
            ])
            .is_none()
        );
        assert!(
            parse_health_probe([
                format!("{HEALTH_ARGUMENT}{HEALTH_FILE_PREFIX}fixture"),
                format!("{HEALTH_TOKEN_ARGUMENT}not-a-token"),
            ])
            .is_none()
        );
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn failed_relaunch_rolls_back_an_appimage_replacement() {
        let directory = tempfile::tempdir().unwrap();
        let destination = directory.path().join("DevHud.AppImage");
        fs::write(&destination, b"installed-version").unwrap();
        let mut replacement = tempfile::NamedTempFile::new_in(directory.path()).unwrap();
        replacement.write_all(b"candidate-version").unwrap();
        let result = replace_file_transactionally(&destination, replacement, |_| {
            Err(DiagnosticCode::RestartFailed)
        });
        assert_eq!(result, Err(DiagnosticCode::RestartFailed));
        assert_eq!(fs::read(destination).unwrap(), b"installed-version");
    }
}
