//! Closed desktop updater protocol and trust boundary.
//!
//! The frontend may select an action, but it cannot provide a URL, header,
//! public key, package type, or target. Those values are all native enums or
//! constants in this module.

use std::{
    collections::HashSet,
    fs,
    future::Future,
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
    Client as AsyncClient, Response as AsyncResponse, StatusCode,
    blocking::{Client as BlockingClient, Response as BlockingResponse},
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
const CANCELLATION_POLL_INTERVAL: Duration = Duration::from_millis(100);
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
#[cfg(any(test, target_os = "windows"))]
const WINDOWS_INSTALLER_REBOOT_REQUIRED_EXIT_CODE: i32 = 3010;

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
    discovery_client: BlockingClient,
    download_client: AsyncClient,
}

impl UpdaterTransport {
    pub fn new() -> Result<Self, UpdaterError> {
        let discovery_client = BlockingClient::builder()
            .redirect(Policy::none())
            .connect_timeout(CONNECT_TIMEOUT)
            .timeout(DISCOVERY_TIMEOUT)
            .build()
            .map_err(|_| UpdaterError::new(DiagnosticCode::Offline, UpdatePhase::Discovery))?;
        let download_client = AsyncClient::builder()
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

    pub async fn download(
        &self,
        candidate: &VerifiedCandidate,
        package_kind: PackageKind,
        canceled: &AtomicBool,
    ) -> Result<VerifiedArtifact, UpdaterError> {
        cancelable_download(canceled, async {
            let mut url = validate_artifact_url(
                &candidate.payload.artifact.url,
                &candidate.payload.version,
                package_kind,
            )?;
            for redirect_count in 0..=MAX_REDIRECTS {
                let response = self
                    .download_client
                    .get(url.clone())
                    .header(ACCEPT, "application/octet-stream")
                    .header(USER_AGENT, "DevHud-Updater/1")
                    .send()
                    .await
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
                return read_artifact_response(response, candidate).await;
            }
            unreachable!("redirect loop is bounded")
        })
        .await
    }
}

async fn wait_for_cancellation(canceled: &AtomicBool) {
    while !canceled.load(Ordering::Acquire) {
        tokio::time::sleep(CANCELLATION_POLL_INTERVAL).await;
    }
}

async fn cancelable_download<T>(
    canceled: &AtomicBool,
    download: impl Future<Output = Result<T, UpdaterError>>,
) -> Result<T, UpdaterError> {
    tokio::select! {
        result = download => result,
        () = wait_for_cancellation(canceled) => Err(UpdaterError::new(
            DiagnosticCode::Canceled,
            UpdatePhase::Download,
        )),
    }
}

fn read_manifest_response(response: BlockingResponse) -> Result<Vec<u8>, UpdaterError> {
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

async fn read_artifact_response(
    mut response: AsyncResponse,
    candidate: &VerifiedCandidate,
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
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| UpdaterError::new(DiagnosticCode::DownloadFailed, UpdatePhase::Download))?
    {
        if bytes.len().saturating_add(chunk.len()) as u64 > candidate.payload.artifact.size {
            return Err(UpdaterError::new(
                DiagnosticCode::VerificationFailed,
                UpdatePhase::Verification,
            ));
        }
        verifier.update(&chunk);
        bytes.extend_from_slice(&chunk);
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

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum RestartDisposition {
    Relaunched,
    RestartRequired {
        executable: PathBuf,
        diagnostic: DiagnosticCode,
    },
}

pub trait Installer: Send + Sync {
    fn install_and_restart(
        &self,
        verified_artifact: &[u8],
    ) -> Result<RestartDisposition, DiagnosticCode>;
    fn retry_restart(&self, executable: &Path) -> Result<(), DiagnosticCode>;
}

pub trait RestartHandoff: Send + Sync {
    fn release(&self) -> Result<(), DiagnosticCode>;
    fn restore(&self) -> Result<(), DiagnosticCode>;
}

/// Native-only package handoff. The frontend cannot select an executable,
/// arguments, destination, or package type. Release publication remains
/// blocked until the production trust root is provisioned, but this path is
/// still fail-closed so development builds cannot turn arbitrary bytes into a
/// general-purpose process launcher.
pub struct PlatformInstaller<'a> {
    package_kind: PackageKind,
    handoff: &'a dyn RestartHandoff,
}

impl<'a> PlatformInstaller<'a> {
    pub const fn new(package_kind: PackageKind, handoff: &'a dyn RestartHandoff) -> Self {
        Self {
            package_kind,
            handoff,
        }
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

    fn restart(&self, executable: &Path) -> Result<(), DiagnosticCode> {
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
        self.handoff.release()?;
        let child = Command::new(executable)
            .arg(format!("{HEALTH_ARGUMENT}{file_name}"))
            .arg(format!("{HEALTH_TOKEN_ARGUMENT}{token}"))
            .spawn();
        let result = child
            .map_err(|_| DiagnosticCode::RestartFailed)
            .and_then(|mut child| {
                wait_for_health(&mut child, health_file.path(), token.as_bytes())
            });
        if result.is_err() && self.handoff.restore().is_err() {
            tracing::error!(event = "updater_single_instance_restore_failed");
        }
        result
    }

    #[cfg(target_os = "linux")]
    fn install_linux(&self, bytes: &[u8]) -> Result<RestartDisposition, DiagnosticCode> {
        match self.package_kind {
            PackageKind::LinuxAppimage => self.install_appimage(bytes),
            PackageKind::LinuxDeb => {
                if !bytes.starts_with(b"!<arch>\n") {
                    return Err(DiagnosticCode::InstallationFailed);
                }
                // Resolve the installed path before dpkg replaces the running
                // executable. Afterwards current_exe may point at a deleted
                // inode and cannot be used for restart-only recovery.
                let executable =
                    std::env::current_exe().map_err(|_| DiagnosticCode::InstallationFailed)?;
                let package = self.stage(bytes, ".deb")?;
                let status = Command::new("pkexec")
                    .args(["dpkg", "--install"])
                    .arg(package.path())
                    .status()
                    .map_err(|_| DiagnosticCode::InstallationFailed)?;
                if classify_debian_installer_exit(status.success())
                    == DebianInstallerExit::CommitUncertain
                {
                    // dpkg can return failure after unpacking replaced files.
                    // Keep only the pre-install executable path for an explicit
                    // restart recovery instead of claiming the old install was
                    // preserved or attempting the package installation again.
                    return Ok(RestartDisposition::RestartRequired {
                        executable,
                        diagnostic: DiagnosticCode::InstallationFailed,
                    });
                }
                Ok(match self.restart(&executable) {
                    Ok(()) => RestartDisposition::Relaunched,
                    Err(_) => RestartDisposition::RestartRequired {
                        executable,
                        diagnostic: DiagnosticCode::RestartFailed,
                    },
                })
            }
            _ => Err(DiagnosticCode::InstallationFailed),
        }
    }

    #[cfg(target_os = "linux")]
    fn install_appimage(&self, bytes: &[u8]) -> Result<RestartDisposition, DiagnosticCode> {
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
        replace_file_transactionally(&destination, replacement, |path| self.restart(path))
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
        let restart_executable =
            std::env::current_exe().map_err(|_| DiagnosticCode::InstallationFailed)?;
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
        match classify_windows_installer_exit(self.package_kind, status.success(), status.code())? {
            WindowsInstallerExit::Installed => {}
            WindowsInstallerExit::RestartRequired => {
                return Ok(RestartDisposition::RestartRequired {
                    executable: restart_executable,
                    diagnostic: DiagnosticCode::RestartFailed,
                });
            }
        }
        Ok(match self.restart(&restart_executable) {
            Ok(()) => RestartDisposition::Relaunched,
            Err(_) => RestartDisposition::RestartRequired {
                executable: restart_executable,
                diagnostic: DiagnosticCode::RestartFailed,
            },
        })
    }

    #[cfg(target_os = "macos")]
    fn install_macos(&self, bytes: &[u8]) -> Result<RestartDisposition, DiagnosticCode> {
        use std::path::Component;

        if self.package_kind != PackageKind::MacosApp || !bytes.starts_with(&[0x1f, 0x8b]) {
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
        // The staged and installed bundles must share a filesystem so macOS can
        // atomically exchange their directory entries without ever removing the
        // canonical application path.
        let extraction = tempfile::Builder::new()
            .prefix(".devhud-update-v1-")
            .tempdir_in(parent)
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
        let relaunch = destination.join("Contents/MacOS").join(
            executable
                .file_name()
                .ok_or(DiagnosticCode::RestartFailed)?,
        );
        replace_macos_bundle_transactionally(&destination, &apps[0], &relaunch, || {
            self.restart(&relaunch)
        })
    }
}

#[cfg(target_os = "macos")]
fn sync_tree(path: &Path) -> std::io::Result<()> {
    let metadata = fs::symlink_metadata(path)?;
    if metadata.file_type().is_symlink() {
        return Ok(());
    }
    if metadata.is_file() {
        return fs::File::open(path)?.sync_all();
    }
    for entry in fs::read_dir(path)? {
        sync_tree(&entry?.path())?;
    }
    fs::File::open(path)?.sync_all()
}

#[cfg(target_os = "macos")]
fn atomic_exchange(left: &Path, right: &Path) -> Result<(), DiagnosticCode> {
    use std::{ffi::CString, os::unix::ffi::OsStrExt};

    let left = CString::new(left.as_os_str().as_bytes())
        .map_err(|_| DiagnosticCode::InstallationFailed)?;
    let right = CString::new(right.as_os_str().as_bytes())
        .map_err(|_| DiagnosticCode::InstallationFailed)?;
    // SAFETY: both C strings are NUL-terminated, remain alive for the call, and
    // name sibling paths on the same filesystem. RENAME_SWAP changes both
    // directory entries atomically.
    let result = unsafe {
        libc::renameatx_np(
            libc::AT_FDCWD,
            left.as_ptr(),
            libc::AT_FDCWD,
            right.as_ptr(),
            libc::RENAME_SWAP,
        )
    };
    (result == 0)
        .then_some(())
        .ok_or(DiagnosticCode::InstallationFailed)
}

#[cfg(target_os = "macos")]
fn replace_macos_bundle_transactionally(
    destination: &Path,
    replacement: &Path,
    restart_executable: &Path,
    relaunch: impl FnOnce() -> Result<(), DiagnosticCode>,
) -> Result<RestartDisposition, DiagnosticCode> {
    sync_tree(replacement).map_err(|_| DiagnosticCode::InstallationFailed)?;
    replace_macos_bundle_transactionally_with_operations(
        destination,
        replacement,
        restart_executable,
        |parent| {
            fs::File::open(parent)
                .and_then(|directory| directory.sync_all())
                .map_err(|_| DiagnosticCode::InstallationFailed)
        },
        atomic_exchange,
        relaunch,
    )
}

#[cfg(any(test, target_os = "macos"))]
fn replace_macos_bundle_transactionally_with_operations(
    destination: &Path,
    replacement: &Path,
    restart_executable: &Path,
    mut sync_parent: impl FnMut(&Path) -> Result<(), DiagnosticCode>,
    mut exchange: impl FnMut(&Path, &Path) -> Result<(), DiagnosticCode>,
    relaunch: impl FnOnce() -> Result<(), DiagnosticCode>,
) -> Result<RestartDisposition, DiagnosticCode> {
    let parent = destination
        .parent()
        .ok_or(DiagnosticCode::InstallationFailed)?;
    sync_parent(parent)?;
    exchange(destination, replacement)?;
    if sync_parent(parent).is_err() {
        if exchange(destination, replacement).is_err() {
            tracing::error!(
                event = "updater_install_rollback_failed",
                package = "macos-app"
            );
            return Ok(RestartDisposition::RestartRequired {
                executable: restart_executable.to_path_buf(),
                diagnostic: DiagnosticCode::InstallationFailed,
            });
        }
        return Err(DiagnosticCode::InstallationFailed);
    }
    if let Err(error) = relaunch() {
        if exchange(destination, replacement).is_err() {
            tracing::error!(
                event = "updater_install_rollback_failed",
                package = "macos-app"
            );
            return Ok(RestartDisposition::RestartRequired {
                executable: restart_executable.to_path_buf(),
                diagnostic: DiagnosticCode::RestartFailed,
            });
        }
        if sync_parent(parent).is_err() {
            tracing::error!(
                event = "updater_install_rollback_failed",
                package = "macos-app"
            );
        }
        return Err(error);
    }
    Ok(RestartDisposition::Relaunched)
}

fn wait_for_health(
    child: &mut Child,
    health_file: &Path,
    expected: &[u8],
) -> Result<(), DiagnosticCode> {
    wait_for_health_with_limit(
        child,
        health_file,
        expected,
        300,
        Duration::from_millis(100),
    )
}

fn wait_for_health_with_limit(
    child: &mut Child,
    health_file: &Path,
    expected: &[u8],
    attempts: usize,
    poll_interval: Duration,
) -> Result<(), DiagnosticCode> {
    for _ in 0..attempts {
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
        std::thread::sleep(poll_interval);
    }
    let _ = child.kill();
    let _ = child.wait();
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

impl Installer for PlatformInstaller<'_> {
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
        match &result {
            Ok(RestartDisposition::RestartRequired { diagnostic, .. }) => tracing::warn!(
                event = "updater_restart_required",
                package = self.package_kind.header_value(),
                code = ?diagnostic
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

    fn retry_restart(&self, executable: &Path) -> Result<(), DiagnosticCode> {
        tracing::info!(
            event = "updater_restart_retry_started",
            package = self.package_kind.header_value()
        );
        let result = self.restart(executable);
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

#[cfg(any(test, target_os = "linux"))]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum DebianInstallerExit {
    Installed,
    CommitUncertain,
}

#[cfg(any(test, target_os = "linux"))]
fn classify_debian_installer_exit(succeeded: bool) -> DebianInstallerExit {
    if succeeded {
        DebianInstallerExit::Installed
    } else {
        DebianInstallerExit::CommitUncertain
    }
}

#[cfg(any(test, target_os = "windows"))]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum WindowsInstallerExit {
    Installed,
    RestartRequired,
}

#[cfg(any(test, target_os = "windows"))]
fn classify_windows_installer_exit(
    package_kind: PackageKind,
    succeeded: bool,
    exit_code: Option<i32>,
) -> Result<WindowsInstallerExit, DiagnosticCode> {
    if package_kind == PackageKind::WindowsMsi
        && exit_code == Some(WINDOWS_INSTALLER_REBOOT_REQUIRED_EXIT_CODE)
    {
        return Ok(WindowsInstallerExit::RestartRequired);
    }
    succeeded
        .then_some(WindowsInstallerExit::Installed)
        .ok_or(DiagnosticCode::InstallationFailed)
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
fn copy_appimage_backup(
    destination: &Path,
    backup: &Path,
    copy: impl FnOnce(&Path, &Path) -> std::io::Result<u64>,
) -> Result<(), DiagnosticCode> {
    if copy(destination, backup).is_ok() {
        return Ok(());
    }
    if let Err(error) = fs::remove_file(backup)
        && error.kind() != std::io::ErrorKind::NotFound
    {
        tracing::error!(
            event = "updater_install_backup_cleanup_failed",
            package = "linux-appimage"
        );
    }
    Err(DiagnosticCode::InstallationFailed)
}

#[cfg(target_os = "linux")]
fn replace_file_transactionally(
    destination: &Path,
    replacement: tempfile::NamedTempFile,
    relaunch: impl FnOnce(&Path) -> Result<(), DiagnosticCode>,
) -> Result<RestartDisposition, DiagnosticCode> {
    let backup = sibling_backup_path(destination)?;
    copy_appimage_backup(destination, &backup, |source, backup| {
        fs::copy(source, backup)
    })?;
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
            return Ok(RestartDisposition::RestartRequired {
                executable: destination.to_path_buf(),
                diagnostic: DiagnosticCode::RestartFailed,
            });
        }
        return Err(error);
    }
    let _ = fs::remove_file(backup);
    Ok(RestartDisposition::Relaunched)
}

pub struct UpdaterController {
    installed_version: Version,
    target: DesktopTarget,
    package_kind: PackageKind,
    snapshot: UpdaterSnapshot,
    candidate: Option<VerifiedCandidate>,
    artifact: Option<VerifiedArtifact>,
    restart_executable: Option<PathBuf>,
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
            restart_executable: None,
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
        // Rotate the worker generation before clearing the canceled state. A
        // canceled download may still be returning on another thread, and its
        // result must not be allowed to overwrite this newer check.
        self.canceled.store(true, Ordering::Release);
        self.canceled = Arc::new(AtomicBool::new(false));
        self.candidate = None;
        self.artifact = None;
        self.restart_executable = None;
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
            UpdaterStateKind::RestartRequired => self
                .restart_executable
                .as_deref()
                .ok_or(DiagnosticCode::RestartFailed)
                .and_then(|executable| installer.retry_restart(executable))
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
                self.restart_executable = None;
                self.snapshot.kind = UpdaterStateKind::Restarting;
                self.snapshot.diagnostic = None;
                Ok(RestartDisposition::Relaunched)
            }
            Ok(RestartDisposition::RestartRequired {
                executable,
                diagnostic,
            }) => {
                self.require_restart(executable.clone(), diagnostic);
                Ok(RestartDisposition::RestartRequired {
                    executable,
                    diagnostic,
                })
            }
            Err(code) => {
                let phase = if code == DiagnosticCode::RestartFailed {
                    UpdatePhase::Restart
                } else {
                    UpdatePhase::Installation
                };
                let error = UpdaterError::new(code, phase);
                if retrying {
                    self.mark_restart_required(DiagnosticCode::RestartFailed);
                } else {
                    self.fail(error.clone());
                }
                Err(error)
            }
        }
    }

    fn require_restart(&mut self, executable: PathBuf, diagnostic: DiagnosticCode) {
        self.restart_executable = Some(executable);
        self.mark_restart_required(diagnostic);
    }

    fn mark_restart_required(&mut self, diagnostic: DiagnosticCode) {
        self.artifact = None;
        self.snapshot.kind = UpdaterStateKind::RestartRequired;
        self.snapshot.diagnostic = Some(self.diagnostic(UpdaterError::new(
            diagnostic,
            if diagnostic == DiagnosticCode::RestartFailed {
                UpdatePhase::Restart
            } else {
                UpdatePhase::Installation
            },
        )));
    }

    fn fail(&mut self, error: UpdaterError) {
        self.artifact = None;
        self.restart_executable = None;
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
    use std::sync::{Mutex, atomic::AtomicUsize};

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

        fn retry_restart(&self, _executable: &Path) -> Result<(), DiagnosticCode> {
            Err(self.0)
        }
    }

    struct RestartRequiredInstaller {
        installs: AtomicUsize,
        retries: AtomicUsize,
        restart_executable: PathBuf,
        diagnostic: DiagnosticCode,
        retried_executables: Mutex<Vec<PathBuf>>,
    }

    struct TrackingHandoff {
        releases: AtomicUsize,
        restores: AtomicUsize,
    }

    impl RestartHandoff for TrackingHandoff {
        fn release(&self) -> Result<(), DiagnosticCode> {
            self.releases.fetch_add(1, Ordering::SeqCst);
            Ok(())
        }

        fn restore(&self) -> Result<(), DiagnosticCode> {
            self.restores.fetch_add(1, Ordering::SeqCst);
            Ok(())
        }
    }

    impl Installer for RestartRequiredInstaller {
        fn install_and_restart(
            &self,
            _artifact: &[u8],
        ) -> Result<RestartDisposition, DiagnosticCode> {
            self.installs.fetch_add(1, Ordering::SeqCst);
            Ok(RestartDisposition::RestartRequired {
                executable: self.restart_executable.clone(),
                diagnostic: self.diagnostic,
            })
        }

        fn retry_restart(&self, executable: &Path) -> Result<(), DiagnosticCode> {
            self.retried_executables
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner)
                .push(executable.to_path_buf());
            if self.retries.fetch_add(1, Ordering::SeqCst) == 0 {
                Err(DiagnosticCode::RestartFailed)
            } else {
                Ok(())
            }
        }
    }

    #[test]
    fn debian_installer_failures_preserve_potential_commit_recovery() {
        assert_eq!(
            classify_debian_installer_exit(true),
            DebianInstallerExit::Installed
        );
        assert_eq!(
            classify_debian_installer_exit(false),
            DebianInstallerExit::CommitUncertain
        );
    }

    #[test]
    fn windows_installer_exit_codes_distinguish_committed_msi_reboots() {
        assert_eq!(
            classify_windows_installer_exit(PackageKind::WindowsMsi, true, Some(0)),
            Ok(WindowsInstallerExit::Installed)
        );
        assert_eq!(
            classify_windows_installer_exit(
                PackageKind::WindowsMsi,
                false,
                Some(WINDOWS_INSTALLER_REBOOT_REQUIRED_EXIT_CODE),
            ),
            Ok(WindowsInstallerExit::RestartRequired)
        );
        assert_eq!(
            classify_windows_installer_exit(
                PackageKind::WindowsNsis,
                false,
                Some(WINDOWS_INSTALLER_REBOOT_REQUIRED_EXIT_CODE),
            ),
            Err(DiagnosticCode::InstallationFailed)
        );
        assert_eq!(
            classify_windows_installer_exit(PackageKind::WindowsMsi, false, Some(1603)),
            Err(DiagnosticCode::InstallationFailed)
        );
    }

    #[test]
    fn terminal_failures_and_cancellation_preserve_the_running_version() {
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
            assert!(controller.artifact.is_some());
            assert!(controller.approve_restart(&FailingInstaller(code)).is_err());
            assert!(controller.artifact.is_none());
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
    fn check_after_cancellation_invalidates_the_old_worker_generation() {
        let root = SigningKey::from_bytes(&RFC_ROOT_SEED);
        let mut controller =
            UpdaterController::new(Version::new(0, 1, 0), target(), package()).unwrap();
        controller.check_bytes(&fixture("0.2.0", &root, vec![], None), now());
        controller.begin_download().unwrap();
        let canceled_worker = controller.cancellation_token();
        controller.cancel();

        assert!(controller.begin_check());
        let check_worker = controller.cancellation_token();
        assert!(canceled_worker.load(Ordering::Acquire));
        assert!(!Arc::ptr_eq(&canceled_worker, &check_worker));
        assert!(!check_worker.load(Ordering::Acquire));
        assert_eq!(controller.snapshot().kind, UpdaterStateKind::Checking);
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
    fn cancellation_interrupts_a_stalled_download() {
        let canceled = Arc::new(AtomicBool::new(false));
        let worker_cancellation = canceled.clone();
        let canceler = std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(20));
            worker_cancellation.store(true, Ordering::Release);
        });
        let result = tauri::async_runtime::block_on(async {
            tokio::time::timeout(
                Duration::from_secs(1),
                cancelable_download(
                    &canceled,
                    std::future::pending::<Result<(), UpdaterError>>(),
                ),
            )
            .await
            .expect("cancellation must bound a stalled download")
        });
        canceler.join().unwrap();

        assert_eq!(result.unwrap_err().code, DiagnosticCode::Canceled);
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
        let restart_executable = PathBuf::from("cached-devhud-restart-target");
        let installer = RestartRequiredInstaller {
            installs: AtomicUsize::new(0),
            retries: AtomicUsize::new(0),
            restart_executable: restart_executable.clone(),
            diagnostic: DiagnosticCode::RestartFailed,
            retried_executables: Mutex::new(Vec::new()),
        };

        assert_eq!(
            controller.approve_restart(&installer).unwrap(),
            RestartDisposition::RestartRequired {
                executable: restart_executable.clone(),
                diagnostic: DiagnosticCode::RestartFailed,
            }
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
            controller.approve_restart(&installer).unwrap_err().code,
            DiagnosticCode::RestartFailed
        );
        assert_eq!(
            controller.snapshot().kind,
            UpdaterStateKind::RestartRequired
        );
        assert_eq!(
            controller.approve_restart(&installer).unwrap(),
            RestartDisposition::Relaunched
        );
        assert_eq!(controller.snapshot().kind, UpdaterStateKind::Restarting);
        assert_eq!(installer.installs.load(Ordering::SeqCst), 1);
        assert_eq!(installer.retries.load(Ordering::SeqCst), 2);
        assert_eq!(
            *installer
                .retried_executables
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner),
            vec![restart_executable.clone(), restart_executable]
        );
    }

    #[test]
    fn uncertain_debian_install_retries_restart_without_reinstalling() {
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
        let restart_executable = PathBuf::from("cached-devhud-restart-target");
        let installer = RestartRequiredInstaller {
            installs: AtomicUsize::new(0),
            retries: AtomicUsize::new(0),
            restart_executable: restart_executable.clone(),
            diagnostic: DiagnosticCode::InstallationFailed,
            retried_executables: Mutex::new(Vec::new()),
        };

        assert_eq!(
            controller.approve_restart(&installer).unwrap(),
            RestartDisposition::RestartRequired {
                executable: restart_executable.clone(),
                diagnostic: DiagnosticCode::InstallationFailed,
            }
        );
        let snapshot = controller.snapshot();
        assert_eq!(snapshot.kind, UpdaterStateKind::RestartRequired);
        assert_eq!(snapshot.installed_version, "0.1.0");
        assert_eq!(
            snapshot.diagnostic.unwrap(),
            controller.diagnostic(UpdaterError::new(
                DiagnosticCode::InstallationFailed,
                UpdatePhase::Installation,
            ))
        );
        assert!(controller.artifact.is_none());

        assert_eq!(
            controller.approve_restart(&installer).unwrap_err().code,
            DiagnosticCode::RestartFailed
        );
        assert_eq!(installer.installs.load(Ordering::SeqCst), 1);
        assert_eq!(installer.retries.load(Ordering::SeqCst), 1);
        assert_eq!(
            *installer
                .retried_executables
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner),
            vec![restart_executable]
        );
    }

    #[test]
    fn failed_relaunch_restores_single_instance_ownership() {
        let handoff = TrackingHandoff {
            releases: AtomicUsize::new(0),
            restores: AtomicUsize::new(0),
        };
        let installer = PlatformInstaller::new(package(), &handoff);
        let missing = std::env::temp_dir().join(format!(
            "devhud-missing-restart-target-{}",
            uuid::Uuid::now_v7()
        ));

        assert_eq!(
            installer.restart(&missing),
            Err(DiagnosticCode::RestartFailed)
        );
        assert_eq!(handoff.releases.load(Ordering::SeqCst), 1);
        assert_eq!(handoff.restores.load(Ordering::SeqCst), 1);
    }

    #[test]
    fn health_timeout_reaps_the_replacement_process() {
        let mut child = Command::new(std::env::current_exe().unwrap())
            .args(["--ignored", "restart_health_timeout_fixture"])
            .spawn()
            .unwrap();
        assert!(child.try_wait().unwrap().is_none());
        let health_file = tempfile::NamedTempFile::new().unwrap();

        assert_eq!(
            wait_for_health_with_limit(
                &mut child,
                health_file.path(),
                b"unacknowledged",
                1,
                Duration::ZERO,
            ),
            Err(DiagnosticCode::RestartFailed)
        );
        assert!(child.try_wait().unwrap().is_some());
    }

    #[test]
    #[ignore = "spawned only by health_timeout_reaps_the_replacement_process"]
    fn restart_health_timeout_fixture() {
        std::thread::sleep(Duration::from_secs(60));
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

    #[cfg(target_os = "linux")]
    #[test]
    fn failed_appimage_rollback_retains_restart_only_recovery() {
        let directory = tempfile::tempdir().unwrap();
        let destination = directory.path().join("DevHud.AppImage");
        let backup = sibling_backup_path(&destination).unwrap();
        fs::write(&destination, b"installed-version").unwrap();
        let mut replacement = tempfile::NamedTempFile::new_in(directory.path()).unwrap();
        replacement.write_all(b"candidate-version").unwrap();

        let result = replace_file_transactionally(&destination, replacement, |_| {
            fs::remove_file(&backup).unwrap();
            Err(DiagnosticCode::RestartFailed)
        });

        assert_eq!(
            result,
            Ok(RestartDisposition::RestartRequired {
                executable: destination.clone(),
                diagnostic: DiagnosticCode::RestartFailed,
            })
        );
        assert_eq!(fs::read(destination).unwrap(), b"candidate-version");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn failed_appimage_backup_copy_removes_partial_backup() {
        let directory = tempfile::tempdir().unwrap();
        let destination = directory.path().join("DevHud.AppImage");
        fs::write(&destination, b"installed-version").unwrap();
        let backup = sibling_backup_path(&destination).unwrap();

        let result = copy_appimage_backup(&destination, &backup, |_, backup| {
            fs::write(backup, b"partial-backup")?;
            Err(std::io::Error::other("simulated copy failure"))
        });

        assert_eq!(result, Err(DiagnosticCode::InstallationFailed));
        assert!(!backup.exists());
        assert_eq!(fs::read(destination).unwrap(), b"installed-version");
    }

    #[test]
    fn failed_post_swap_macos_rollback_retains_restart_only_recovery() {
        let directory = tempfile::tempdir().unwrap();
        let destination = directory.path().join("DevHUD.app");
        let replacement = directory.path().join("Candidate.app");
        let restart_executable = destination.join("Contents/MacOS/devhud");
        fs::create_dir(&destination).unwrap();
        fs::create_dir(&replacement).unwrap();
        fs::write(destination.join("version"), b"installed-version").unwrap();
        fs::write(replacement.join("version"), b"candidate-version").unwrap();
        let sync_attempts = AtomicUsize::new(0);
        let exchange_attempts = AtomicUsize::new(0);

        let result = replace_macos_bundle_transactionally_with_operations(
            &destination,
            &replacement,
            &restart_executable,
            |_| {
                if sync_attempts.fetch_add(1, Ordering::Relaxed) == 0 {
                    Ok(())
                } else {
                    Err(DiagnosticCode::InstallationFailed)
                }
            },
            |installed, candidate| {
                if exchange_attempts.fetch_add(1, Ordering::Relaxed) != 0 {
                    return Err(DiagnosticCode::InstallationFailed);
                }
                let installed_version = fs::read(installed.join("version")).unwrap();
                let candidate_version = fs::read(candidate.join("version")).unwrap();
                fs::write(installed.join("version"), candidate_version).unwrap();
                fs::write(candidate.join("version"), installed_version).unwrap();
                Ok(())
            },
            || panic!("relaunch must not run after the post-swap sync fails"),
        );

        assert_eq!(
            result,
            Ok(RestartDisposition::RestartRequired {
                executable: restart_executable,
                diagnostic: DiagnosticCode::InstallationFailed,
            })
        );
        assert_eq!(
            fs::read(destination.join("version")).unwrap(),
            b"candidate-version"
        );
        assert_eq!(
            fs::read(replacement.join("version")).unwrap(),
            b"installed-version"
        );
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn failed_relaunch_atomically_restores_the_installed_bundle() {
        let directory = tempfile::tempdir().unwrap();
        let destination = directory.path().join("DevHUD.app");
        let replacement = directory.path().join("Candidate.app");
        fs::create_dir(&destination).unwrap();
        fs::create_dir(&replacement).unwrap();
        fs::write(destination.join("version"), b"installed-version").unwrap();
        fs::write(replacement.join("version"), b"candidate-version").unwrap();

        let restart_executable = destination.join("Contents/MacOS/devhud");
        let result = replace_macos_bundle_transactionally(
            &destination,
            &replacement,
            &restart_executable,
            || {
                assert!(destination.is_dir());
                assert_eq!(
                    fs::read(destination.join("version")).unwrap(),
                    b"candidate-version"
                );
                Err(DiagnosticCode::RestartFailed)
            },
        );

        assert_eq!(result, Err(DiagnosticCode::RestartFailed));
        assert!(destination.is_dir());
        assert_eq!(
            fs::read(destination.join("version")).unwrap(),
            b"installed-version"
        );
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn failed_macos_rollback_retains_restart_only_recovery() {
        let directory = tempfile::tempdir().unwrap();
        let destination = directory.path().join("DevHUD.app");
        let replacement = directory.path().join("Candidate.app");
        let previous = directory.path().join("Previous.app");
        let restart_executable = destination.join("Contents/MacOS/devhud");
        fs::create_dir(&destination).unwrap();
        fs::create_dir(&replacement).unwrap();
        fs::write(destination.join("version"), b"installed-version").unwrap();
        fs::write(replacement.join("version"), b"candidate-version").unwrap();

        let result = replace_macos_bundle_transactionally(
            &destination,
            &replacement,
            &restart_executable,
            || {
                fs::rename(&replacement, &previous).unwrap();
                Err(DiagnosticCode::RestartFailed)
            },
        );

        assert_eq!(
            result,
            Ok(RestartDisposition::RestartRequired {
                executable: restart_executable,
                diagnostic: DiagnosticCode::RestartFailed,
            })
        );
        assert_eq!(
            fs::read(destination.join("version")).unwrap(),
            b"candidate-version"
        );
        assert_eq!(
            fs::read(previous.join("version")).unwrap(),
            b"installed-version"
        );
    }
}
