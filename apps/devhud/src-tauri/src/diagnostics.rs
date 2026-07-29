use std::{
    io::{self, Write},
    sync::OnceLock,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::local_log::LocalLogWriter;

pub(crate) const TAURI_UPSTREAM_VERSION: &str = "2.11.5+f49ebda2fdba5755456b0f049e32593ca0ea331a";
pub(crate) const CEF_UPSTREAM_VERSION: &str = "150.0.0+150.0.10";
const APPLICATION_VERSION: &str = env!("CARGO_PKG_VERSION");
const BUILD_VERSION: &str = "1";

static DIAGNOSTICS: OnceLock<Diagnostics> = OnceLock::new();

#[derive(Clone)]
pub(crate) struct Diagnostics {
    writer: LocalLogWriter,
    session_id: Uuid,
    started_at: Instant,
}

impl Diagnostics {
    pub(crate) fn new(writer: LocalLogWriter) -> Self {
        Self {
            writer,
            session_id: Uuid::now_v7(),
            started_at: Instant::now(),
        }
    }

    pub(crate) fn install(writer: LocalLogWriter) -> bool {
        DIAGNOSTICS.set(Self::new(writer)).is_ok()
    }

    fn emit(
        &self,
        event_id: DiagnosticEventId,
        classification: DiagnosticClassification,
        severity: DiagnosticSeverity,
        duration: Option<Duration>,
    ) -> io::Result<()> {
        let record = DiagnosticRecord::new(
            self.session_id,
            event_id,
            classification,
            severity,
            duration,
        );
        let mut bytes = serde_json::to_vec(&record).map_err(io::Error::other)?;
        bytes.push(b'\n');
        let mut writer = self.writer.clone();
        writer.write_all(&bytes)?;
        writer.flush()
    }

    pub(crate) fn preflight_clear(&self, directory: &std::path::Path) -> io::Result<()> {
        self.writer.preflight_clear_in(directory)
    }

    pub(crate) fn clear(&self, directory: &std::path::Path) -> io::Result<()> {
        self.writer.clear_in(directory)
    }

    pub(crate) fn sanitized_bundle(&self) -> io::Result<Vec<u8>> {
        sanitize_chunks(self.writer.snapshot()?)
    }

    pub(crate) fn destination_is_managed(&self, destination: &std::path::Path) -> bool {
        self.writer.destination_is_managed(destination)
    }

    #[cfg(test)]
    fn records(&self) -> Vec<DiagnosticRecord> {
        self.writer
            .snapshot()
            .unwrap()
            .into_iter()
            .flat_map(|chunk| {
                chunk
                    .split(|byte| *byte == b'\n')
                    .filter_map(DiagnosticRecord::parse_valid)
                    .collect::<Vec<_>>()
            })
            .collect()
    }
}

pub(crate) fn emit(event_id: DiagnosticEventId, classification: DiagnosticClassification) {
    emit_with(event_id, classification, DiagnosticSeverity::Info, None);
}

pub(crate) fn emit_warning(event_id: DiagnosticEventId, classification: DiagnosticClassification) {
    emit_with(event_id, classification, DiagnosticSeverity::Warning, None);
}

pub(crate) fn emit_fatal(event_id: DiagnosticEventId, classification: DiagnosticClassification) {
    emit_with(event_id, classification, DiagnosticSeverity::Fatal, None);
}

pub(crate) fn emit_duration(
    event_id: DiagnosticEventId,
    classification: DiagnosticClassification,
    duration: Duration,
) {
    emit_with(
        event_id,
        classification,
        DiagnosticSeverity::Info,
        Some(duration),
    );
}

pub(crate) fn emit_runtime_ready(classification: DiagnosticClassification) {
    let duration = DIAGNOSTICS
        .get()
        .map(|diagnostics| diagnostics.started_at.elapsed())
        .unwrap_or_default();
    emit_duration(DiagnosticEventId::RuntimeReady, classification, duration);
}

fn emit_with(
    event_id: DiagnosticEventId,
    classification: DiagnosticClassification,
    severity: DiagnosticSeverity,
    duration: Option<Duration>,
) {
    tracing::event!(
        target: "devhud::diagnostics",
        tracing::Level::INFO,
        event_id = event_id.as_str(),
        classification = classification.as_str(),
        severity = severity.as_str(),
    );
    let sink_succeeded = DIAGNOSTICS.get().is_some_and(|diagnostics| {
        diagnostics
            .emit(event_id, classification, severity, duration)
            .is_ok()
    });
    if needs_fatal_fallback(severity, sink_succeeded) {
        // This fallback is deliberately static and contains no exception or
        // environment material. It preserves the fatal event if the local sink
        // could not be initialized or becomes unwritable.
        eprintln!(
            "{{\"eventId\":\"{}\",\"classification\":\"{}\",\"severity\":\"fatal\"}}",
            event_id.as_str(),
            classification.as_str()
        );
    }
}

fn needs_fatal_fallback(severity: DiagnosticSeverity, sink_succeeded: bool) -> bool {
    severity == DiagnosticSeverity::Fatal && !sink_succeeded
}

pub(crate) fn active() -> Option<&'static Diagnostics> {
    DIAGNOSTICS.get()
}

fn sanitize_chunks(chunks: Vec<Vec<u8>>) -> io::Result<Vec<u8>> {
    let mut output = Vec::new();
    for chunk in chunks {
        for line in chunk.split(|byte| *byte == b'\n') {
            let Some(record) = DiagnosticRecord::parse_valid(line) else {
                continue;
            };
            serde_json::to_writer(&mut output, &record).map_err(io::Error::other)?;
            output.push(b'\n');
        }
    }
    Ok(output)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum DiagnosticEventId {
    RuntimeReady,
    RuntimeDuplicateInstance,
    RuntimeInitializationFailure,
    PersistenceUnavailable,
    PersistenceIoFailure,
    PersistenceReset,
    PersistenceResetFailure,
    PersistenceResetCleanupFailure,
    WidgetOutcome,
    DisplayOutcome,
    ShortcutOutcome,
    AutostartOutcome,
    UpdaterOutcome,
    SignatureOutcome,
    InstallationOutcome,
    DiagnosticsExportOutcome,
    RealqaCaptureOutcome,
}

impl DiagnosticEventId {
    const fn as_str(self) -> &'static str {
        match self {
            Self::RuntimeReady => "runtime-ready",
            Self::RuntimeDuplicateInstance => "runtime-duplicate-instance",
            Self::RuntimeInitializationFailure => "runtime-initialization-failure",
            Self::PersistenceUnavailable => "persistence-unavailable",
            Self::PersistenceIoFailure => "persistence-io-failure",
            Self::PersistenceReset => "persistence-reset",
            Self::PersistenceResetFailure => "persistence-reset-failure",
            Self::PersistenceResetCleanupFailure => "persistence-reset-cleanup-failure",
            Self::WidgetOutcome => "widget-outcome",
            Self::DisplayOutcome => "display-outcome",
            Self::ShortcutOutcome => "shortcut-outcome",
            Self::AutostartOutcome => "autostart-outcome",
            Self::UpdaterOutcome => "updater-outcome",
            Self::SignatureOutcome => "signature-outcome",
            Self::InstallationOutcome => "installation-outcome",
            Self::DiagnosticsExportOutcome => "diagnostics-export-outcome",
            Self::RealqaCaptureOutcome => "realqa-capture-outcome",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum DiagnosticClassification {
    DesktopReady,
    DesktopAlreadyRunning,
    DesktopInstanceGuardUnavailable,
    DesktopCefInitializationFailed,
    MobileReady,
    MobileSystemWebviewInitializationFailed,
    WidgetBridgeUnavailable,
    WidgetCorrupt,
    WidgetFutureVersion,
    WidgetIncompatible,
    WidgetRefreshFailed,
    PersistenceStorageUnavailable,
    PersistenceInvalidRecord,
    PersistenceResetComplete,
    PersistenceResetPreflightFailed,
    PersistenceResetFailed,
    PersistencePartiallyRetained,
    PersistenceCleanupFailed,
    CefProfileCleanupFailed,
    DisplayWindowUnavailable,
    DisplayUnsupported,
    DisplayPositionFailed,
    ShortcutMalformed,
    ShortcutConflict,
    ShortcutPermissionDenied,
    ShortcutRegistrationFailed,
    ShortcutStorageFailed,
    AutostartPermissionDenied,
    AutostartOperationFailed,
    AutostartStorageFailed,
    UpdaterUnavailable,
    UpdaterOffline,
    UpdaterRateLimited,
    SignatureValid,
    SignatureInvalid,
    InstallationSucceeded,
    InstallationFailed,
    DiagnosticsExported,
    DiagnosticsExportFailed,
    RealqaCaptureCompleted,
    RealqaCaptureUnsupportedPlatform,
    RealqaCaptureBackendUnavailable,
    RealqaCapturePermissionRequired,
    RealqaCapturePermissionDenied,
    RealqaCapturePermissionLost,
    RealqaCaptureCancelled,
    RealqaCapturePortalCancelled,
    RealqaCaptureProtectedContent,
    RealqaCaptureWindowMinimized,
    RealqaCaptureWindowClosed,
    RealqaCaptureWindowLost,
    RealqaCaptureDisplayRemoved,
    RealqaCaptureModeUnavailable,
    RealqaCaptureDisplayChanged,
    RealqaCaptureInvalidRequest,
    RealqaCaptureImageRejected,
    RealqaCaptureFailed,
}

impl DiagnosticClassification {
    pub(crate) const fn as_str(self) -> &'static str {
        match self {
            Self::DesktopReady => "desktop-ready",
            Self::DesktopAlreadyRunning => "desktop-already-running",
            Self::DesktopInstanceGuardUnavailable => "desktop-instance-guard-unavailable",
            Self::DesktopCefInitializationFailed => "desktop-cef-initialization-failed",
            Self::MobileReady => "mobile-ready",
            Self::MobileSystemWebviewInitializationFailed => {
                "mobile-system-webview-initialization-failed"
            }
            Self::WidgetBridgeUnavailable => "widget-bridge-unavailable",
            Self::WidgetCorrupt => "widget-corrupt",
            Self::WidgetFutureVersion => "widget-future-version",
            Self::WidgetIncompatible => "widget-incompatible",
            Self::WidgetRefreshFailed => "widget-refresh-failed",
            Self::PersistenceStorageUnavailable => "persistence-storage-unavailable",
            Self::PersistenceInvalidRecord => "persistence-invalid-record",
            Self::PersistenceResetComplete => "persistence-reset-complete",
            Self::PersistenceResetPreflightFailed => "persistence-reset-preflight-failed",
            Self::PersistenceResetFailed => "persistence-reset-failed",
            Self::PersistencePartiallyRetained => "persistence-partially-retained",
            Self::PersistenceCleanupFailed => "persistence-cleanup-failed",
            Self::CefProfileCleanupFailed => "cef-profile-cleanup-failed",
            Self::DisplayWindowUnavailable => "display-window-unavailable",
            Self::DisplayUnsupported => "display-unsupported",
            Self::DisplayPositionFailed => "display-position-failed",
            Self::ShortcutMalformed => "shortcut-malformed",
            Self::ShortcutConflict => "shortcut-conflict",
            Self::ShortcutPermissionDenied => "shortcut-permission-denied",
            Self::ShortcutRegistrationFailed => "shortcut-registration-failed",
            Self::ShortcutStorageFailed => "shortcut-storage-failed",
            Self::AutostartPermissionDenied => "autostart-permission-denied",
            Self::AutostartOperationFailed => "autostart-operation-failed",
            Self::AutostartStorageFailed => "autostart-storage-failed",
            Self::UpdaterUnavailable => "updater-unavailable",
            Self::UpdaterOffline => "updater-offline",
            Self::UpdaterRateLimited => "updater-rate-limited",
            Self::SignatureValid => "signature-valid",
            Self::SignatureInvalid => "signature-invalid",
            Self::InstallationSucceeded => "installation-succeeded",
            Self::InstallationFailed => "installation-failed",
            Self::DiagnosticsExported => "diagnostics-exported",
            Self::DiagnosticsExportFailed => "diagnostics-export-failed",
            Self::RealqaCaptureCompleted => "realqa-capture-completed",
            Self::RealqaCaptureUnsupportedPlatform => "realqa-capture-unsupported-platform",
            Self::RealqaCaptureBackendUnavailable => "realqa-capture-backend-unavailable",
            Self::RealqaCapturePermissionRequired => "realqa-capture-permission-required",
            Self::RealqaCapturePermissionDenied => "realqa-capture-permission-denied",
            Self::RealqaCapturePermissionLost => "realqa-capture-permission-lost",
            Self::RealqaCaptureCancelled => "realqa-capture-cancelled",
            Self::RealqaCapturePortalCancelled => "realqa-capture-portal-cancelled",
            Self::RealqaCaptureProtectedContent => "realqa-capture-protected-content",
            Self::RealqaCaptureWindowMinimized => "realqa-capture-window-minimized",
            Self::RealqaCaptureWindowClosed => "realqa-capture-window-closed",
            Self::RealqaCaptureWindowLost => "realqa-capture-window-lost",
            Self::RealqaCaptureDisplayRemoved => "realqa-capture-display-removed",
            Self::RealqaCaptureModeUnavailable => "realqa-capture-mode-unavailable",
            Self::RealqaCaptureDisplayChanged => "realqa-capture-display-changed",
            Self::RealqaCaptureInvalidRequest => "realqa-capture-invalid-request",
            Self::RealqaCaptureImageRejected => "realqa-capture-image-rejected",
            Self::RealqaCaptureFailed => "realqa-capture-failed",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
enum DiagnosticSeverity {
    Info,
    Warning,
    Fatal,
}

impl DiagnosticSeverity {
    const fn as_str(self) -> &'static str {
        match self {
            Self::Info => "info",
            Self::Warning => "warning",
            Self::Fatal => "fatal",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
enum DiagnosticOperatingSystem {
    Android,
    Ios,
    Linux,
    Macos,
    Windows,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
enum DiagnosticArchitecture {
    Aarch64,
    Arm,
    X86,
    X86_64,
    Unknown,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct DiagnosticRecord {
    application_version: String,
    build_version: String,
    operating_system: DiagnosticOperatingSystem,
    architecture: DiagnosticArchitecture,
    tauri_version: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    cef_version: Option<String>,
    session_id: Uuid,
    timestamp_ms: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    duration_ms: Option<u64>,
    event_id: DiagnosticEventId,
    classification: DiagnosticClassification,
    severity: DiagnosticSeverity,
}

impl DiagnosticRecord {
    fn new(
        session_id: Uuid,
        event_id: DiagnosticEventId,
        classification: DiagnosticClassification,
        severity: DiagnosticSeverity,
        duration: Option<Duration>,
    ) -> Self {
        Self {
            application_version: APPLICATION_VERSION.to_string(),
            build_version: BUILD_VERSION.to_string(),
            operating_system: current_operating_system(),
            architecture: current_architecture(),
            tauri_version: TAURI_UPSTREAM_VERSION.to_string(),
            cef_version: current_cef_version().map(str::to_string),
            session_id,
            timestamp_ms: current_timestamp_ms(),
            duration_ms: duration.map(|value| u64::try_from(value.as_millis()).unwrap_or(u64::MAX)),
            event_id,
            classification,
            severity,
        }
    }

    fn parse_valid(line: &[u8]) -> Option<Self> {
        if line.is_empty() {
            return None;
        }
        let record = serde_json::from_slice::<Self>(line).ok()?;
        record.is_valid().then_some(record)
    }

    fn is_valid(&self) -> bool {
        is_trusted_application_version(&self.application_version)
            && is_numeric_version(&self.build_version)
            && is_tauri_version(&self.tauri_version)
            && self.cef_version.as_deref().is_none_or(is_cef_version)
            && self.session_id.get_version_num() == 7
    }
}

fn is_trusted_application_version(value: &str) -> bool {
    let Some(version) = numeric_version_triplet(value) else {
        return false;
    };
    let Some(current_version) = numeric_version_triplet(APPLICATION_VERSION) else {
        return false;
    };
    version <= current_version
}

fn numeric_version_triplet(value: &str) -> Option<[u64; 3]> {
    is_numeric_version_with_exact_parts(value, 3).then_some(())?;
    let mut parts = value.split('.').map(str::parse::<u64>);
    Some([
        parts.next()?.ok()?,
        parts.next()?.ok()?,
        parts.next()?.ok()?,
    ])
}

fn is_numeric_version(value: &str) -> bool {
    if value.is_empty() || value.len() > 32 {
        return false;
    }
    let count = value.split('.').count();
    (1..=4).contains(&count) && has_valid_numeric_version_parts(value)
}

fn is_numeric_version_with_exact_parts(value: &str, expected_parts: usize) -> bool {
    value.len() <= 32
        && value.split('.').count() == expected_parts
        && has_valid_numeric_version_parts(value)
}

fn has_valid_numeric_version_parts(value: &str) -> bool {
    value.split('.').all(|part| {
        !part.is_empty()
            && part.bytes().all(|byte| byte.is_ascii_digit())
            && (part.len() == 1 || !part.starts_with('0'))
    })
}

fn is_tauri_version(value: &str) -> bool {
    let Some((version, revision)) = value.split_once('+') else {
        return false;
    };
    is_numeric_version_with_exact_parts(version, 3)
        && revision.len() == 40
        && revision
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn is_cef_version(value: &str) -> bool {
    if value.len() > 65 {
        return false;
    }
    let Some((cef, chromium)) = value.split_once('+') else {
        return false;
    };
    is_numeric_version_with_exact_parts(cef, 3) && is_numeric_version_with_exact_parts(chromium, 3)
}

fn current_timestamp_ms() -> u64 {
    let milliseconds = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis();
    u64::try_from(milliseconds).unwrap_or(u64::MAX)
}

const fn current_cef_version() -> Option<&'static str> {
    if cfg!(all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    )) {
        Some(CEF_UPSTREAM_VERSION)
    } else {
        None
    }
}

const fn current_operating_system() -> DiagnosticOperatingSystem {
    if cfg!(target_os = "android") {
        DiagnosticOperatingSystem::Android
    } else if cfg!(target_os = "ios") {
        DiagnosticOperatingSystem::Ios
    } else if cfg!(target_os = "macos") {
        DiagnosticOperatingSystem::Macos
    } else if cfg!(target_os = "windows") {
        DiagnosticOperatingSystem::Windows
    } else {
        DiagnosticOperatingSystem::Linux
    }
}

const fn current_architecture() -> DiagnosticArchitecture {
    if cfg!(target_arch = "aarch64") {
        DiagnosticArchitecture::Aarch64
    } else if cfg!(target_arch = "arm") {
        DiagnosticArchitecture::Arm
    } else if cfg!(target_arch = "x86") {
        DiagnosticArchitecture::X86
    } else if cfg!(target_arch = "x86_64") {
        DiagnosticArchitecture::X86_64
    } else {
        DiagnosticArchitecture::Unknown
    }
}

#[cfg(test)]
mod tests {
    use std::{fs, path::PathBuf};

    use serde_json::json;

    use super::*;

    fn temporary_directory(name: &str) -> PathBuf {
        std::env::temp_dir().join(format!(
            "devhud-diagnostics-{name}-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ))
    }

    fn diagnostics(name: &str) -> (Diagnostics, PathBuf) {
        let directory = temporary_directory(name);
        let writer = LocalLogWriter::new_in(directory.clone()).unwrap();
        (Diagnostics::new(writer), directory)
    }

    #[test]
    fn diagnostic_sessions_are_ephemeral_uuid_v7_values() {
        let first =
            Diagnostics::new(LocalLogWriter::new_in(temporary_directory("uuid-a")).unwrap());
        let second =
            Diagnostics::new(LocalLogWriter::new_in(temporary_directory("uuid-b")).unwrap());

        assert_eq!(first.session_id.get_version_num(), 7);
        assert_eq!(second.session_id.get_version_num(), 7);
        assert_ne!(first.session_id, second.session_id);

        fs::remove_dir_all(temporary_directory("uuid-a")).unwrap();
        fs::remove_dir_all(temporary_directory("uuid-b")).unwrap();
    }

    #[test]
    fn fatal_initialization_is_one_safe_record_without_an_exception() {
        let (diagnostics, directory) = diagnostics("fatal");
        diagnostics
            .emit(
                DiagnosticEventId::RuntimeInitializationFailure,
                DiagnosticClassification::DesktopCefInitializationFailed,
                DiagnosticSeverity::Fatal,
                None,
            )
            .unwrap();

        let records = diagnostics.records();
        assert_eq!(records.len(), 1);
        assert_eq!(records[0].severity, DiagnosticSeverity::Fatal);
        assert_eq!(
            records[0].classification,
            DiagnosticClassification::DesktopCefInitializationFailed
        );
        let encoded = serde_json::to_string(&records[0]).unwrap();
        assert!(!encoded.contains("error"));
        assert!(!encoded.contains("exception"));
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn fatal_events_fall_back_when_an_installed_sink_fails() {
        assert!(needs_fatal_fallback(DiagnosticSeverity::Fatal, false));
        assert!(!needs_fatal_fallback(DiagnosticSeverity::Fatal, true));
        assert!(!needs_fatal_fallback(DiagnosticSeverity::Warning, false));
    }

    #[test]
    fn export_recursively_rejects_unknown_and_adversarial_values() {
        let valid = DiagnosticRecord::new(
            Uuid::now_v7(),
            DiagnosticEventId::RuntimeReady,
            DiagnosticClassification::DesktopReady,
            DiagnosticSeverity::Info,
            Some(Duration::from_millis(12)),
        );
        let valid = serde_json::to_vec(&valid).unwrap();
        let adversarial_values = [
            json!({
                "searchText": "private query",
                "nested": {
                    "account": {"invitation": "invite-secret"},
                    "authorization": "Bearer hidden",
                    "credentials": ["password-secret"],
                    "environment": {"rawValue": "HOME=/private/home"},
                    "signingData": "certificate-secret",
                    "token": "token-secret",
                },
                "shortcutKeys": ["control", "k"],
                "userFiles": [{"path": "/private/user/file"}],
            }),
            json!({
                "applicationVersion": "token-secret",
                "buildVersion": BUILD_VERSION,
                "operatingSystem": current_operating_system(),
                "architecture": current_architecture(),
                "tauriVersion": TAURI_UPSTREAM_VERSION,
                "sessionId": Uuid::now_v7(),
                "timestampMs": 1,
                "eventId": "runtime-ready",
                "classification": "desktop-ready",
                "severity": "info",
            }),
            json!({
                "applicationVersion": APPLICATION_VERSION,
                "buildVersion": BUILD_VERSION,
                "operatingSystem": current_operating_system(),
                "architecture": current_architecture(),
                "tauriVersion": TAURI_UPSTREAM_VERSION,
                "sessionId": Uuid::nil(),
                "timestampMs": 1,
                "eventId": "runtime-ready",
                "classification": "desktop-ready",
                "severity": "info",
                "clipboard": ["hidden", {"path": "/private/user/file"}],
            }),
        ];
        let mut chunks = vec![valid];
        for value in adversarial_values {
            chunks.push(serde_json::to_vec(&value).unwrap());
        }

        let bundle = sanitize_chunks(chunks).unwrap();
        let text = String::from_utf8(bundle).unwrap();

        assert_eq!(text.lines().count(), 1);
        for excluded in [
            "private query",
            "Bearer hidden",
            "certificate-secret",
            "invite-secret",
            "password-secret",
            "token-secret",
            "HOME=/private/home",
            "/private/user/file",
            "account",
            "searchText",
            "clipboard",
            "authorization",
            "credentials",
            "environment",
            "invitation",
            "shortcutKeys",
            "signingData",
            "token",
            "path",
            "userFiles",
        ] {
            assert!(!text.contains(excluded));
        }
    }

    #[test]
    fn export_preserves_closed_records_from_previous_builds() {
        let mut record = DiagnosticRecord::new(
            Uuid::now_v7(),
            DiagnosticEventId::RuntimeReady,
            DiagnosticClassification::DesktopReady,
            DiagnosticSeverity::Info,
            None,
        );
        record.application_version = "0.0.9".to_string();
        record.build_version = "42.1".to_string();
        record.tauri_version = "2.11.4+0123456789abcdef0123456789abcdef01234567".to_string();
        record.cef_version = Some("149.0.0+149.0.7".to_string());

        let bundle = sanitize_chunks(vec![serde_json::to_vec(&record).unwrap()]).unwrap();

        assert_eq!(
            serde_json::from_slice::<DiagnosticRecord>(bundle.strip_suffix(b"\n").unwrap())
                .unwrap(),
            record
        );
    }

    #[test]
    fn export_rejects_unbounded_version_metadata() {
        let mut record = DiagnosticRecord::new(
            Uuid::now_v7(),
            DiagnosticEventId::RuntimeReady,
            DiagnosticClassification::DesktopReady,
            DiagnosticSeverity::Info,
            None,
        );
        record.tauri_version = "2.11.4+token-secret".to_string();

        assert!(
            sanitize_chunks(vec![serde_json::to_vec(&record).unwrap()])
                .unwrap()
                .is_empty()
        );

        record.tauri_version =
            "2.11.4-token-secret+0123456789abcdef0123456789abcdef01234567".to_string();
        assert!(
            sanitize_chunks(vec![serde_json::to_vec(&record).unwrap()])
                .unwrap()
                .is_empty()
        );

        record.tauri_version = TAURI_UPSTREAM_VERSION.to_string();
        record.application_version = format!("{APPLICATION_VERSION}+token-secret");
        assert!(
            sanitize_chunks(vec![serde_json::to_vec(&record).unwrap()])
                .unwrap()
                .is_empty()
        );

        record.application_version = "999.0.0".to_string();
        assert!(
            sanitize_chunks(vec![serde_json::to_vec(&record).unwrap()])
                .unwrap()
                .is_empty()
        );
    }

    #[test]
    fn classifications_cover_every_required_native_outcome_domain() {
        let classifications = [
            DiagnosticClassification::DesktopReady,
            DiagnosticClassification::MobileReady,
            DiagnosticClassification::WidgetBridgeUnavailable,
            DiagnosticClassification::PersistenceStorageUnavailable,
            DiagnosticClassification::DisplayWindowUnavailable,
            DiagnosticClassification::ShortcutRegistrationFailed,
            DiagnosticClassification::UpdaterUnavailable,
            DiagnosticClassification::SignatureInvalid,
            DiagnosticClassification::InstallationFailed,
            DiagnosticClassification::RealqaCapturePortalCancelled,
        ];
        assert_eq!(classifications.len(), 10);
        assert!(
            classifications
                .iter()
                .all(|value| !value.as_str().is_empty())
        );
    }
}
