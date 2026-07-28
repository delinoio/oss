#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[allow(dead_code)]
mod auth;
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[allow(dead_code)]
mod auth_native;
#[cfg(any(feature = "desktop-cef", test))]
mod autostart;
#[cfg(any(
    feature = "desktop-cef",
    feature = "linux-capture-backend",
    feature = "mobile-system-webview",
    test
))]
#[cfg_attr(test, allow(dead_code))]
mod diagnostics;
#[cfg(any(
    feature = "desktop-cef",
    feature = "linux-capture-backend",
    feature = "mobile-system-webview",
    test
))]
mod local_log;
#[cfg(any(feature = "desktop-cef", feature = "linux-capture-backend", test))]
mod realqa_capture;
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
mod shortcut;
#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
mod single_instance;
#[cfg(any(feature = "desktop-cef", test))]
mod updater;

#[cfg(feature = "desktop-cef")]
use serde::Deserialize;
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
use serde::Serialize;
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
use url::Url;

#[cfg(all(feature = "desktop-cef", feature = "mobile-system-webview"))]
compile_error!("select exactly one DevHud runtime feature");
#[cfg(all(feature = "desktop-cef", any(target_os = "android", target_os = "ios")))]
compile_error!("desktop-cef cannot be used for iOS or Android");
#[cfg(all(
    feature = "mobile-system-webview",
    not(any(target_os = "android", target_os = "ios"))
))]
compile_error!("mobile-system-webview is reserved for iOS and Android targets");

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
use std::borrow::Cow;
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
use std::sync::Mutex;
#[cfg(feature = "desktop-cef")]
use std::sync::atomic::{AtomicBool, Ordering};
#[cfg(all(
    any(feature = "desktop-cef", feature = "mobile-system-webview"),
    debug_assertions
))]
use std::time::Duration;
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
use std::{
    fs, io,
    path::{Path, PathBuf},
};
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
use std::{fs::File, io::Write};

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
use http::{HeaderName, HeaderValue, Request, Response, StatusCode};
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
use tauri::{
    AppHandle, Manager, State, Webview, WebviewUrl,
    webview::{NewWindowResponse, WebviewWindowBuilder},
};
#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
use tauri::{
    PhysicalPosition, WindowEvent,
    image::Image,
    menu::{Menu, MenuItem},
    tray::TrayIconBuilder,
};
#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
use tauri_plugin_devhud_diagnostics::{
    DevHudDiagnosticsBridgeExt, DiagnosticsBridgeErrorCode,
    DiagnosticsExportOutcome as MobileDiagnosticsExportOutcome,
};
#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
use tauri_plugin_devhud_widget::{
    DevHudWidgetBridgeExt, Error as WidgetBridgeError, WidgetBridgeErrorCode,
};
#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
use uuid::Uuid;

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
type ActiveRuntime = tauri_runtime_cef::CefRuntime<tauri::EventLoopMessage>;
#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
type ActiveRuntime = tauri_runtime_wry::Wry<tauri::EventLoopMessage>;

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
const APPLICATION_ID: &str = "dev.deli.devhud";
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
const MAIN_WINDOW_LABEL: &str = "main";
#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
const SETTINGS_WINDOW_LABEL: &str = "settings";
#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
const TRAY_ACTIONS: [(&str, &str); 5] = [
    ("open-devhud", "Open DevHud"),
    ("settings", "Settings"),
    ("check-for-updates", "Check for Updates"),
    ("open-devtools", "Open DevTools"),
    ("quit", "Quit"),
];
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
const SETTINGS_STORAGE_KEY: &str = "devhud.settings.v1";
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
const SHORTCUT_EFFECTIVE_STATE_STORAGE_KEY: &str = "devhud.shortcut-effective-state.v2";
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
const WIDGET_CONFIGURATION_STORAGE_KEY: &str = "devhud.widget-configuration.v1";
#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
const CEF_PROFILE_DIRECTORY: &str = "cef";
#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
const CEF_PRIVATE_STORAGE_SWITCHES: [&str; 6] = [
    "--disable-application-cache",
    "--disable-databases",
    "--disable-local-storage",
    "--disable-session-storage",
    "--disable-sync",
    "--incognito",
];
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
const PERMISSIONS_POLICY: &str =
    "camera=(), display-capture=(), geolocation=(), microphone=(), usb=()";
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct RuntimeInfo {
    application_id: &'static str,
    bundled_origin: String,
    operating_system: &'static str,
    runtime: &'static str,
    sandbox_enabled: bool,
    surface: RuntimeSurface,
    first_run: bool,
    #[cfg(any(feature = "desktop-cef", test))]
    shortcut_startup_failure: Option<shortcut::ShortcutFailure>,
    #[cfg(any(feature = "desktop-cef", test))]
    autostart_startup_outcome: Option<autostart::AutostartOutcome>,
    update_policy: &'static str,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[derive(Default)]
struct StartupDiagnostics {
    #[cfg(feature = "desktop-cef")]
    shortcut_failure: Option<shortcut::ShortcutFailure>,
    #[cfg(feature = "desktop-cef")]
    autostart_outcome: Option<autostart::AutostartOutcome>,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[derive(Debug, Serialize)]
#[serde(rename_all = "kebab-case")]
enum RuntimeSurface {
    #[cfg(any(
        all(
            feature = "desktop-cef",
            not(any(target_os = "android", target_os = "ios"))
        ),
        test
    ))]
    Hud,
    #[cfg(any(
        all(
            feature = "desktop-cef",
            not(any(target_os = "android", target_os = "ios"))
        ),
        test
    ))]
    Settings,
    #[cfg(any(
        all(
            feature = "mobile-system-webview",
            any(target_os = "android", target_os = "ios")
        ),
        test
    ))]
    Mobile,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
enum RuntimeCommandError {
    NonBundledAsset,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(tag = "status", rename_all = "kebab-case")]
enum DiagnosticsExportOutcome {
    Exported,
    Cancelled,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[cfg_attr(test, allow(dead_code))]
#[serde(rename_all = "kebab-case")]
enum DiagnosticsExportError {
    Unavailable,
    #[cfg(any(
        all(
            feature = "mobile-system-webview",
            any(target_os = "android", target_os = "ios")
        ),
        test
    ))]
    PickerUnavailable,
    WriteFailed,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum RuntimeInitializationFailure {
    #[cfg(any(
        all(
            feature = "desktop-cef",
            not(any(target_os = "android", target_os = "ios"))
        ),
        test
    ))]
    InstanceGuardUnavailable,
    #[cfg(any(
        all(
            feature = "desktop-cef",
            not(any(target_os = "android", target_os = "ios"))
        ),
        test
    ))]
    CefInitialization,
    #[cfg(any(
        all(
            feature = "mobile-system-webview",
            any(target_os = "android", target_os = "ios")
        ),
        test
    ))]
    SystemWebviewInitialization,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
impl RuntimeInitializationFailure {
    const fn diagnostic_classification(self) -> diagnostics::DiagnosticClassification {
        match self {
            #[cfg(any(
                all(
                    feature = "desktop-cef",
                    not(any(target_os = "android", target_os = "ios"))
                ),
                test
            ))]
            Self::InstanceGuardUnavailable => {
                diagnostics::DiagnosticClassification::DesktopInstanceGuardUnavailable
            }
            #[cfg(any(
                all(
                    feature = "desktop-cef",
                    not(any(target_os = "android", target_os = "ios"))
                ),
                test
            ))]
            Self::CefInitialization => {
                diagnostics::DiagnosticClassification::DesktopCefInitializationFailed
            }
            #[cfg(any(
                all(
                    feature = "mobile-system-webview",
                    any(target_os = "android", target_os = "ios")
                ),
                test
            ))]
            Self::SystemWebviewInitialization => {
                diagnostics::DiagnosticClassification::MobileSystemWebviewInitializationFailed
            }
        }
    }
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
struct PersistenceState {
    directory: Option<PathBuf>,
    write_lock: Mutex<()>,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[cfg_attr(
    not(any(feature = "desktop-cef", feature = "mobile-system-webview")),
    allow(dead_code)
)]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
enum PersistenceCommandError {
    StorageUnavailable,
    InvalidRecord,
    ResetFailed,
    #[cfg(all(
        feature = "mobile-system-webview",
        any(target_os = "android", target_os = "ios")
    ))]
    WidgetBridgeFailed,
    #[cfg(all(
        feature = "mobile-system-webview",
        any(target_os = "android", target_os = "ios")
    ))]
    Corrupt,
    #[cfg(all(
        feature = "mobile-system-webview",
        any(target_os = "android", target_os = "ios")
    ))]
    FutureVersion,
    #[cfg(all(
        feature = "mobile-system-webview",
        any(target_os = "android", target_os = "ios")
    ))]
    Incompatible,
    WriteFailed,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum PersistenceResetFailure {
    BeforeRecordsRemoved(PersistenceCommandError),
    PartiallyRetained,
    CleanupFailed,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(tag = "status", rename_all = "kebab-case")]
enum PersistenceResetOutcome {
    Complete,
    PartiallyRetained,
    CleanupFailed,
    #[cfg(any(
        all(
            feature = "desktop-cef",
            not(any(target_os = "android", target_os = "ios"))
        ),
        test
    ))]
    IntegrationRollbackFailed {
        shortcut: Option<shortcut::StructuredShortcut>,
        #[serde(rename = "launchAtLogin")]
        launch_at_login: Option<bool>,
    },
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
impl PersistenceState {
    fn new(directory: PathBuf) -> io::Result<Self> {
        prepare_persistence_directory(&directory)?;
        Ok(Self {
            directory: Some(directory),
            write_lock: Mutex::new(()),
        })
    }

    fn unavailable() -> Self {
        Self {
            directory: None,
            write_lock: Mutex::new(()),
        }
    }

    fn path_for(&self, key: &str) -> Option<PathBuf> {
        self.directory.as_ref().map(|directory| directory.join(key))
    }

    fn read(&self, key: &str) -> Result<Option<String>, PersistenceCommandError> {
        let Some(path) = self.path_for(key) else {
            log_persistence_unavailable("read", key);
            return Err(PersistenceCommandError::StorageUnavailable);
        };
        match fs::read_to_string(path) {
            Ok(value) => Ok(Some(value)),
            Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
            Err(error) => {
                log_persistence_io_failure("read", key, &error);
                Err(PersistenceCommandError::StorageUnavailable)
            }
        }
    }

    fn write(&self, key: &str, record: &str) -> Result<(), PersistenceCommandError> {
        let _guard = self.write_lock.lock().map_err(|_| {
            diagnostics::emit_warning(
                diagnostics::DiagnosticEventId::PersistenceUnavailable,
                diagnostics::DiagnosticClassification::PersistenceStorageUnavailable,
            );
            PersistenceCommandError::StorageUnavailable
        })?;
        self.write_without_lock(key, record)
    }

    fn write_without_lock(&self, key: &str, record: &str) -> Result<(), PersistenceCommandError> {
        validate_current_record(key, record).ok_or(PersistenceCommandError::InvalidRecord)?;
        let Some(path) = self.path_for(key) else {
            log_persistence_unavailable("write", key);
            return Err(PersistenceCommandError::StorageUnavailable);
        };
        write_atomically(&path, record).map_err(|error| {
            log_persistence_io_failure("write", key, &error);
            PersistenceCommandError::WriteFailed
        })
    }

    #[cfg(feature = "desktop-cef")]
    fn write_frontend_settings(&self, record: &str) -> Result<(), PersistenceCommandError> {
        let _guard = self.write_lock.lock().map_err(|_| {
            log_persistence_unavailable("write", SETTINGS_STORAGE_KEY);
            PersistenceCommandError::StorageUnavailable
        })?;
        let current = self.read(SETTINGS_STORAGE_KEY)?;
        let merged = merge_frontend_settings_record(current.as_deref(), record)?;
        self.write_without_lock(SETTINGS_STORAGE_KEY, &merged)
    }

    #[cfg(feature = "desktop-cef")]
    fn update_settings_field(
        &self,
        field: &str,
        value: serde_json::Value,
    ) -> Result<(), PersistenceCommandError> {
        let _guard = self.write_lock.lock().map_err(|_| {
            log_persistence_unavailable("write", SETTINGS_STORAGE_KEY);
            PersistenceCommandError::StorageUnavailable
        })?;
        let mut record = match self.read(SETTINGS_STORAGE_KEY)? {
            Some(record) => {
                validate_current_record(SETTINGS_STORAGE_KEY, &record)
                    .ok_or(PersistenceCommandError::InvalidRecord)?;
                serde_json::from_str::<serde_json::Value>(&record)
                    .map_err(|_| PersistenceCommandError::InvalidRecord)?
            }
            None => default_settings_record(),
        };
        let settings = record
            .get_mut("settings")
            .and_then(serde_json::Value::as_object_mut)
            .ok_or(PersistenceCommandError::InvalidRecord)?;
        settings.insert(field.to_string(), value);
        let record =
            serde_json::to_string(&record).map_err(|_| PersistenceCommandError::InvalidRecord)?;
        self.write_without_lock(SETTINGS_STORAGE_KEY, &record)
    }

    fn reset(&self) -> Result<(), PersistenceResetFailure> {
        let _guard = self.write_lock.lock().map_err(|_| {
            diagnostics::emit_warning(
                diagnostics::DiagnosticEventId::PersistenceResetFailure,
                diagnostics::DiagnosticClassification::PersistenceResetFailed,
            );
            PersistenceResetFailure::BeforeRecordsRemoved(PersistenceCommandError::ResetFailed)
        })?;
        let (directory, paths) = self.reset_paths()?;
        let previously_staged = pending_reset_stage_paths(directory, &paths).map_err(|_| {
            diagnostics::emit_warning(
                diagnostics::DiagnosticEventId::PersistenceResetFailure,
                diagnostics::DiagnosticClassification::PersistenceResetFailed,
            );
            PersistenceResetFailure::BeforeRecordsRemoved(PersistenceCommandError::ResetFailed)
        })?;
        let transaction_id = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_nanos();
        // Stage both stable records before unlinking either one so a staging
        // failure can restore the complete pre-reset persistence state.
        let staged_paths = paths.map(|(key, path)| {
            let staged_path =
                path.with_extension(format!("reset-{}-{transaction_id}", std::process::id()));
            (key, path, staged_path)
        });
        reset_persisted_records(
            &staged_paths,
            &previously_staged,
            |source, destination| fs::rename(source, destination),
            |path| fs::remove_file(path),
        )
    }

    fn preflight_reset(&self) -> Result<(), PersistenceCommandError> {
        let _guard = self
            .write_lock
            .lock()
            .map_err(|_| PersistenceCommandError::ResetFailed)?;
        let (directory, paths) = self.reset_paths().map_err(|failure| match failure {
            PersistenceResetFailure::BeforeRecordsRemoved(error) => error,
            PersistenceResetFailure::PartiallyRetained | PersistenceResetFailure::CleanupFailed => {
                PersistenceCommandError::ResetFailed
            }
        })?;
        pending_reset_stage_paths(directory, &paths)
            .map(|_| ())
            .map_err(|_| PersistenceCommandError::ResetFailed)
    }

    fn reset_paths(&self) -> Result<PersistenceResetPaths<'_>, PersistenceResetFailure> {
        let paths = [
            (
                SETTINGS_STORAGE_KEY,
                self.path_for(SETTINGS_STORAGE_KEY).ok_or_else(|| {
                    log_persistence_unavailable("reset", SETTINGS_STORAGE_KEY);
                    PersistenceResetFailure::BeforeRecordsRemoved(
                        PersistenceCommandError::StorageUnavailable,
                    )
                })?,
            ),
            (
                WIDGET_CONFIGURATION_STORAGE_KEY,
                self.path_for(WIDGET_CONFIGURATION_STORAGE_KEY)
                    .ok_or_else(|| {
                        log_persistence_unavailable("reset", WIDGET_CONFIGURATION_STORAGE_KEY);
                        PersistenceResetFailure::BeforeRecordsRemoved(
                            PersistenceCommandError::StorageUnavailable,
                        )
                    })?,
            ),
            (
                SHORTCUT_EFFECTIVE_STATE_STORAGE_KEY,
                self.path_for(SHORTCUT_EFFECTIVE_STATE_STORAGE_KEY)
                    .ok_or_else(|| {
                        log_persistence_unavailable("reset", SHORTCUT_EFFECTIVE_STATE_STORAGE_KEY);
                        PersistenceResetFailure::BeforeRecordsRemoved(
                            PersistenceCommandError::StorageUnavailable,
                        )
                    })?,
            ),
        ];
        let directory =
            self.directory
                .as_deref()
                .ok_or(PersistenceResetFailure::BeforeRecordsRemoved(
                    PersistenceCommandError::StorageUnavailable,
                ))?;
        Ok((directory, paths))
    }
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
type PersistenceResetPaths<'a> = (&'a Path, [(&'static str, PathBuf); 3]);

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn pending_reset_stage_paths<'a>(
    directory: &Path,
    paths: &[(&'a str, PathBuf)],
) -> io::Result<Vec<(&'a str, PathBuf)>> {
    if fs::symlink_metadata(directory)?.file_type().is_symlink() {
        return Err(io::Error::other(
            "persistence reset target must not be a symbolic link",
        ));
    }
    for (_, path) in paths {
        match fs::symlink_metadata(path) {
            Ok(metadata) if metadata.file_type().is_file() => {}
            Ok(_) => {
                return Err(io::Error::other(
                    "persistence reset record has an invalid filesystem type",
                ));
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => {}
            Err(error) => return Err(error),
        }
    }
    let mut staged = Vec::new();
    for entry in fs::read_dir(directory)? {
        let entry = entry?;
        let candidate = entry.path();
        let Some(extension) = candidate.extension().and_then(|value| value.to_str()) else {
            continue;
        };
        let Some(transaction) = extension.strip_prefix("reset-") else {
            continue;
        };
        let mut identifiers = transaction.split('-');
        let managed = identifiers.next().is_some_and(|value| {
            !value.is_empty() && value.bytes().all(|byte| byte.is_ascii_digit())
        }) && identifiers.next().is_some_and(|value| {
            !value.is_empty() && value.bytes().all(|byte| byte.is_ascii_digit())
        }) && identifiers.next().is_none();
        if !managed {
            continue;
        }
        if let Some((key, _)) = paths
            .iter()
            .find(|(_, stable)| stable.file_stem() == candidate.file_stem())
        {
            if !entry.file_type()?.is_file() {
                return Err(io::Error::other(
                    "persistence reset stage has an invalid filesystem type",
                ));
            }
            staged.push((*key, candidate));
        }
    }
    Ok(staged)
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn reset_persisted_records<S, R>(
    paths: &[(&str, PathBuf, PathBuf)],
    previously_staged: &[(&str, PathBuf)],
    mut stage_record: S,
    mut remove_staged_record: R,
) -> Result<(), PersistenceResetFailure>
where
    S: FnMut(&Path, &Path) -> io::Result<()>,
    R: FnMut(&Path) -> io::Result<()>,
{
    let mut staged = Vec::with_capacity(paths.len());
    for (index, (key, path, staged_path)) in paths.iter().enumerate() {
        match stage_record(path, staged_path) {
            Ok(()) => staged.push(index),
            Err(error) if error.kind() == io::ErrorKind::NotFound => {}
            Err(error) => {
                log_persistence_io_failure("reset", key, &error);
                let mut rollback_failed = false;
                for staged_index in staged.into_iter().rev() {
                    let (staged_key, original_path, rollback_path) = &paths[staged_index];
                    if let Err(rollback_error) = stage_record(rollback_path, original_path) {
                        rollback_failed = true;
                        log_persistence_io_failure("reset-rollback", staged_key, &rollback_error);
                    }
                }
                return Err(if rollback_failed {
                    PersistenceResetFailure::PartiallyRetained
                } else {
                    PersistenceResetFailure::BeforeRecordsRemoved(
                        PersistenceCommandError::ResetFailed,
                    )
                });
            }
        }
    }

    // Once all records are staged, every stable key is absent. A partial unlink
    // can no longer be rolled back, but it must still report an incomplete reset.
    let mut cleanup_failed = false;
    for (_key, staged_path) in previously_staged {
        if let Err(error) = remove_staged_record(staged_path)
            && error.kind() != io::ErrorKind::NotFound
        {
            cleanup_failed = true;
            diagnostics::emit_warning(
                diagnostics::DiagnosticEventId::PersistenceResetCleanupFailure,
                diagnostics::DiagnosticClassification::PersistenceCleanupFailed,
            );
        }
    }
    for staged_index in staged {
        let (_key, _, staged_path) = &paths[staged_index];
        if let Err(error) = remove_staged_record(staged_path)
            && error.kind() != io::ErrorKind::NotFound
        {
            cleanup_failed = true;
            diagnostics::emit_warning(
                diagnostics::DiagnosticEventId::PersistenceResetCleanupFailure,
                diagnostics::DiagnosticClassification::PersistenceCleanupFailed,
            );
        }
    }
    if cleanup_failed {
        Err(PersistenceResetFailure::CleanupFailed)
    } else {
        Ok(())
    }
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
fn cef_profile_directory_from(cache_base: &Path) -> PathBuf {
    cache_base.join(APPLICATION_ID).join(CEF_PROFILE_DIRECTORY)
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn cef_profile_directory() -> io::Result<PathBuf> {
    dirs::cache_dir()
        .map(|cache_base| cef_profile_directory_from(&cache_base))
        .ok_or_else(|| io::Error::other("CEF profile directory is unavailable"))
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
fn is_cef_reset_stage(path: &Path) -> bool {
    let Some(name) = path.file_name().and_then(|name| name.to_str()) else {
        return false;
    };
    let Some(transaction) = name.strip_prefix("cef.reset-") else {
        return false;
    };
    let mut identifiers = transaction.split('-');
    identifiers
        .next()
        .is_some_and(|value| !value.is_empty() && value.bytes().all(|byte| byte.is_ascii_digit()))
        && identifiers.next().is_some_and(|value| {
            !value.is_empty() && value.bytes().all(|byte| byte.is_ascii_digit())
        })
        && identifiers.next().is_none()
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
fn validate_cef_profile_target(cache_base: &Path, profile: &Path) -> io::Result<()> {
    let expected = cef_profile_directory_from(cache_base);
    if profile != expected
        || profile.file_name().and_then(|name| name.to_str()) != Some(CEF_PROFILE_DIRECTORY)
        || profile
            .parent()
            .and_then(Path::file_name)
            .and_then(|name| name.to_str())
            != Some(APPLICATION_ID)
    {
        return Err(io::Error::other(
            "CEF profile reset target is outside the DevHud boundary",
        ));
    }
    match fs::symlink_metadata(profile) {
        Ok(metadata) if metadata.file_type().is_symlink() => {
            return Err(io::Error::other(
                "CEF profile reset target must not be a symbolic link",
            ));
        }
        Ok(metadata) if !metadata.file_type().is_dir() => {
            return Err(io::Error::other(
                "CEF profile reset target has an invalid filesystem type",
            ));
        }
        Ok(_) => {}
        Err(error) if error.kind() == io::ErrorKind::NotFound => {}
        Err(error) => return Err(error),
    }
    Ok(())
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
fn preflight_cef_profile_reset(cache_base: &Path, profile: &Path) -> io::Result<Vec<PathBuf>> {
    validate_cef_profile_target(cache_base, profile)?;
    let parent = profile
        .parent()
        .ok_or_else(|| io::Error::other("CEF profile parent is unavailable"))?;
    if fs::symlink_metadata(parent).is_ok_and(|metadata| metadata.file_type().is_symlink()) {
        return Err(io::Error::other(
            "CEF profile parent must not be a symbolic link",
        ));
    }
    let entries = match fs::read_dir(parent) {
        Ok(entries) => entries,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(Vec::new()),
        Err(error) => return Err(error),
    };
    let mut pending = Vec::new();
    for entry in entries {
        let entry = entry?;
        let candidate = entry.path();
        if is_cef_reset_stage(&candidate) {
            if !entry.file_type()?.is_dir() {
                return Err(io::Error::other(
                    "CEF reset stage has an invalid filesystem type",
                ));
            }
            pending.push(candidate);
        }
    }
    Ok(pending)
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
fn path_is_within_cef_reset_boundary(owned_root: &Path, path: &Path) -> bool {
    let Ok(relative) = path.strip_prefix(owned_root) else {
        return false;
    };
    let Some(std::path::Component::Normal(boundary)) = relative.components().next() else {
        return false;
    };
    let boundary = Path::new(boundary);
    boundary == Path::new(CEF_PROFILE_DIRECTORY) || is_cef_reset_stage(boundary)
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
fn destination_is_cef_reset_owned(cache_base: &Path, destination: &Path) -> io::Result<bool> {
    let owned_root = cache_base.join(APPLICATION_ID);
    if path_is_within_cef_reset_boundary(&owned_root, destination) {
        return Ok(true);
    }

    let parent = destination
        .parent()
        .ok_or_else(|| io::Error::other("diagnostics export parent is unavailable"))?;
    let file_name = destination
        .file_name()
        .ok_or_else(|| io::Error::other("diagnostics export file name is unavailable"))?;
    let canonical_parent = fs::canonicalize(parent)?;
    let canonical_owned_root = match fs::canonicalize(owned_root) {
        Ok(owned_root) => owned_root,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(false),
        Err(error) => return Err(error),
    };
    Ok(path_is_within_cef_reset_boundary(
        &canonical_owned_root,
        &canonical_parent.join(file_name),
    ))
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
fn reset_cef_profile_directory(
    cache_base: &Path,
    profile: &Path,
) -> Result<(), PersistenceResetFailure> {
    let pending = preflight_cef_profile_reset(cache_base, profile).map_err(|_| {
        PersistenceResetFailure::BeforeRecordsRemoved(PersistenceCommandError::ResetFailed)
    })?;
    let parent = profile
        .parent()
        .ok_or(PersistenceResetFailure::BeforeRecordsRemoved(
            PersistenceCommandError::ResetFailed,
        ))?;
    let transaction_id = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos();
    let staged = parent.join(format!("cef.reset-{}-{transaction_id}", std::process::id()));

    let staged_current = match fs::rename(profile, &staged) {
        Ok(()) => true,
        Err(error) if error.kind() == io::ErrorKind::NotFound => false,
        Err(_) => {
            return Err(PersistenceResetFailure::BeforeRecordsRemoved(
                PersistenceCommandError::ResetFailed,
            ));
        }
    };
    if let Err(error) = fs::create_dir_all(profile) {
        if staged_current && fs::rename(&staged, profile).is_err() {
            return Err(PersistenceResetFailure::PartiallyRetained);
        }
        let _ = error.kind();
        return Err(PersistenceResetFailure::BeforeRecordsRemoved(
            PersistenceCommandError::ResetFailed,
        ));
    }

    let mut cleanup_failed = false;
    for candidate in pending
        .into_iter()
        .chain(staged_current.then_some(staged).into_iter())
    {
        if let Err(error) = fs::remove_dir_all(candidate)
            && error.kind() != io::ErrorKind::NotFound
        {
            cleanup_failed = true;
        }
    }
    if cleanup_failed {
        Err(PersistenceResetFailure::CleanupFailed)
    } else {
        Ok(())
    }
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn prepare_persistence_directory(directory: &Path) -> io::Result<()> {
    fs::create_dir_all(directory)?;
    #[cfg(all(feature = "mobile-system-webview", target_os = "ios"))]
    exclude_ios_persistence_from_backup(directory)?;
    Ok(())
}

#[cfg(all(feature = "mobile-system-webview", target_os = "ios"))]
fn exclude_ios_persistence_from_backup(directory: &Path) -> io::Result<()> {
    use objc2_foundation::{NSNumber, NSString, NSURL, NSURLIsExcludedFromBackupKey};

    let path = directory
        .to_str()
        .ok_or_else(|| io::Error::other("DevHud persistence path is not valid UTF-8"))?;
    let path = NSString::from_str(path);
    let directory_url = NSURL::fileURLWithPath_isDirectory(&path, true);
    let excluded = NSNumber::new_bool(true);

    // SAFETY: NSURLIsExcludedFromBackupKey requires an NSNumber boolean value.
    unsafe {
        directory_url.setResourceValue_forKey_error(Some(&*excluded), NSURLIsExcludedFromBackupKey)
    }
    .map_err(|_| io::Error::other("failed to exclude DevHud persistence from iOS backup"))
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn log_persistence_io_failure(_operation: &'static str, _key: &str, _error: &io::Error) {
    diagnostics::emit_warning(
        diagnostics::DiagnosticEventId::PersistenceIoFailure,
        diagnostics::DiagnosticClassification::PersistenceStorageUnavailable,
    );
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn log_persistence_unavailable(_operation: &'static str, _key: &str) {
    diagnostics::emit_warning(
        diagnostics::DiagnosticEventId::PersistenceUnavailable,
        diagnostics::DiagnosticClassification::PersistenceStorageUnavailable,
    );
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
fn widget_bridge_failure(
    _operation: &'static str,
    error: &WidgetBridgeError,
) -> PersistenceCommandError {
    let failure = match error.code() {
        Some(WidgetBridgeErrorCode::Corrupt) => PersistenceCommandError::Corrupt,
        Some(WidgetBridgeErrorCode::FutureVersion) => PersistenceCommandError::FutureVersion,
        Some(WidgetBridgeErrorCode::Incompatible) => PersistenceCommandError::Incompatible,
        _ => PersistenceCommandError::WidgetBridgeFailed,
    };
    let classification = match error.code() {
        Some(WidgetBridgeErrorCode::Corrupt) => {
            diagnostics::DiagnosticClassification::WidgetCorrupt
        }
        Some(WidgetBridgeErrorCode::FutureVersion) => {
            diagnostics::DiagnosticClassification::WidgetFutureVersion
        }
        Some(WidgetBridgeErrorCode::Incompatible) => {
            diagnostics::DiagnosticClassification::WidgetIncompatible
        }
        Some(WidgetBridgeErrorCode::RefreshFailed) => {
            diagnostics::DiagnosticClassification::WidgetRefreshFailed
        }
        _ => diagnostics::DiagnosticClassification::WidgetBridgeUnavailable,
    };
    diagnostics::emit_warning(
        diagnostics::DiagnosticEventId::WidgetOutcome,
        classification,
    );
    failure
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn validate_current_record(key: &str, record: &str) -> Option<()> {
    let value: serde_json::Value = serde_json::from_str(record).ok()?;
    let object = value.as_object()?;
    match key {
        SETTINGS_STORAGE_KEY if object.get("version")?.as_u64() == Some(1) => {
            validate_settings_record(object)
        }
        WIDGET_CONFIGURATION_STORAGE_KEY if object.get("version")?.as_u64() == Some(1) => {
            validate_widget_configuration_record(object)
        }
        SHORTCUT_EFFECTIVE_STATE_STORAGE_KEY if object.get("version")?.as_u64() == Some(2) => {
            validate_shortcut_effective_state_record(object)
        }
        _ => None,
    }
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn validate_shortcut_effective_state_record(
    object: &serde_json::Map<String, serde_json::Value>,
) -> Option<()> {
    has_exact_keys(object, &["version", "state"]).then_some(())?;
    let state = object.get("state")?.as_object()?;
    has_exact_keys(state, &["version", "genericShortcut", "inactive"]).then_some(())?;
    (state.get("version")?.as_u64() == Some(2)).then_some(())?;
    match state.get("genericShortcut")? {
        serde_json::Value::Null => {}
        serde_json::Value::Object(shortcut) => {
            let shortcut: shortcut::StructuredShortcut =
                serde_json::from_value(serde_json::Value::Object(shortcut.clone())).ok()?;
            shortcut.validate().ok()?;
        }
        _ => return None,
    }
    for owner in state.get("inactive")?.as_array()? {
        let owner = owner.as_object()?;
        match owner.get("feature")?.as_str()? {
            "devhud" if has_exact_keys(owner, &["feature"]) => {}
            "deck"
                if has_exact_keys(owner, &["feature", "accountId", "definitionId"])
                    && owner
                        .get("accountId")?
                        .as_str()
                        .is_some_and(|value| !value.is_empty())
                    && owner
                        .get("definitionId")?
                        .as_str()
                        .is_some_and(|value| !value.is_empty()) => {}
            "realqa"
                if has_exact_keys(owner, &["feature", "definitionId"])
                    && owner
                        .get("definitionId")?
                        .as_str()
                        .is_some_and(|value| !value.is_empty()) => {}
            _ => return None,
        }
    }
    Some(())
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn has_exact_keys(object: &serde_json::Map<String, serde_json::Value>, keys: &[&str]) -> bool {
    object.len() == keys.len() && keys.iter().all(|key| object.contains_key(*key))
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn validate_settings_record(object: &serde_json::Map<String, serde_json::Value>) -> Option<()> {
    has_exact_keys(object, &["version", "settings"]).then_some(())?;
    let settings = object.get("settings")?.as_object()?;
    has_exact_keys(settings, &["theme", "launchAtLogin", "shortcut"]).then_some(())?;
    matches!(
        settings.get("theme")?.as_str()?,
        "system" | "light" | "dark"
    )
    .then_some(())?;
    settings.get("launchAtLogin")?.as_bool()?;
    match settings.get("shortcut")? {
        serde_json::Value::Null => Some(()),
        serde_json::Value::Object(shortcut) => {
            has_exact_keys(shortcut, &["modifiers", "key"]).then_some(())?;
            let modifiers = shortcut.get("modifiers")?.as_array()?;
            (!modifiers.is_empty()).then_some(())?;
            let mut unique_modifiers = std::collections::HashSet::new();
            for modifier in modifiers {
                let modifier = modifier.as_str()?;
                matches!(modifier, "control" | "alt" | "shift" | "meta").then_some(())?;
                unique_modifiers.insert(modifier).then_some(())?;
            }
            matches!(
                shortcut.get("key")?.as_str()?,
                "a" | "b"
                    | "c"
                    | "d"
                    | "e"
                    | "f"
                    | "g"
                    | "h"
                    | "i"
                    | "j"
                    | "k"
                    | "l"
                    | "m"
                    | "n"
                    | "o"
                    | "p"
                    | "q"
                    | "r"
                    | "s"
                    | "t"
                    | "u"
                    | "v"
                    | "w"
                    | "x"
                    | "y"
                    | "z"
                    | "0"
                    | "1"
                    | "2"
                    | "3"
                    | "4"
                    | "5"
                    | "6"
                    | "7"
                    | "8"
                    | "9"
                    | "f1"
                    | "f2"
                    | "f3"
                    | "f4"
                    | "f5"
                    | "f6"
                    | "f7"
                    | "f8"
                    | "f9"
                    | "f10"
                    | "f11"
                    | "f12"
                    | "space"
                    | "enter"
            )
            .then_some(())
        }
        _ => None,
    }
}

#[cfg(any(feature = "desktop-cef", test))]
#[cfg_attr(not(feature = "desktop-cef"), allow(dead_code))]
fn default_settings_record() -> serde_json::Value {
    serde_json::json!({
        "version": 1,
        "settings": {
            "theme": "system",
            "launchAtLogin": false,
            "shortcut": null
        }
    })
}

#[cfg(any(feature = "desktop-cef", test))]
fn merge_frontend_settings_record(
    current: Option<&str>,
    candidate: &str,
) -> Result<String, PersistenceCommandError> {
    validate_current_record(SETTINGS_STORAGE_KEY, candidate)
        .ok_or(PersistenceCommandError::InvalidRecord)?;
    let mut candidate = serde_json::from_str::<serde_json::Value>(candidate)
        .map_err(|_| PersistenceCommandError::InvalidRecord)?;
    let Some(current) = current else {
        return serde_json::to_string(&candidate)
            .map_err(|_| PersistenceCommandError::InvalidRecord);
    };
    validate_current_record(SETTINGS_STORAGE_KEY, current)
        .ok_or(PersistenceCommandError::InvalidRecord)?;
    let current = serde_json::from_str::<serde_json::Value>(current)
        .map_err(|_| PersistenceCommandError::InvalidRecord)?;
    let current_settings = current
        .get("settings")
        .and_then(serde_json::Value::as_object)
        .ok_or(PersistenceCommandError::InvalidRecord)?;
    let candidate_settings = candidate
        .get_mut("settings")
        .and_then(serde_json::Value::as_object_mut)
        .ok_or(PersistenceCommandError::InvalidRecord)?;
    for native_field in ["launchAtLogin", "shortcut"] {
        candidate_settings.insert(
            native_field.to_string(),
            current_settings
                .get(native_field)
                .cloned()
                .ok_or(PersistenceCommandError::InvalidRecord)?,
        );
    }
    serde_json::to_string(&candidate).map_err(|_| PersistenceCommandError::InvalidRecord)
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[derive(Deserialize)]
struct NativeSettingsRecord {
    version: u8,
    settings: NativeSettings,
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct NativeSettings {
    launch_at_login: bool,
    shortcut: Option<shortcut::StructuredShortcut>,
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn native_settings(record: Option<&str>) -> Option<NativeSettings> {
    let record = record?;
    validate_current_record(SETTINGS_STORAGE_KEY, record)?;
    let record = serde_json::from_str::<NativeSettingsRecord>(record).ok()?;
    (record.version == 1).then_some(record.settings)
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn validate_widget_configuration_record(
    object: &serde_json::Map<String, serde_json::Value>,
) -> Option<()> {
    has_exact_keys(object, &["version", "configuration"]).then_some(())?;
    let configuration = object.get("configuration")?.as_object()?;
    has_exact_keys(configuration, &["slots"]).then_some(())?;
    let slots = configuration.get("slots")?.as_array()?;
    let mut unique_slots = std::collections::HashSet::new();
    for reference in slots {
        let reference = reference.as_object()?;
        has_exact_keys(reference, &["slot", "toolId"]).then_some(())?;
        let slot = reference.get("slot")?.as_str()?;
        matches!(slot, "primary" | "secondary" | "tertiary").then_some(())?;
        unique_slots.insert(slot).then_some(())?;
        is_stable_tool_id(reference.get("toolId")?.as_str()?).then_some(())?;
    }
    Some(())
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn is_stable_tool_id(value: &str) -> bool {
    let mut segments = value.split('-');
    matches!(segments.next(), Some(first) if !first.is_empty() && first.bytes().all(|byte| byte.is_ascii_lowercase()))
        && segments.all(|segment| {
            !segment.is_empty()
                && segment
                    .bytes()
                    .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit())
        })
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn write_atomically(path: &std::path::Path, record: &str) -> io::Result<()> {
    let temporary_path = path.with_extension(format!("tmp-{}", std::process::id()));
    let mut temporary_file = File::create(&temporary_path)?;
    temporary_file.write_all(record.as_bytes())?;
    temporary_file.sync_all()?;
    drop(temporary_file);

    if let Err(error) = replace_file(&temporary_path, path) {
        let _ = fs::remove_file(&temporary_path);
        return Err(error);
    }
    Ok(())
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
fn write_export_atomically(path: &std::path::Path, contents: &[u8]) -> io::Result<()> {
    write_export_atomically_with(path, |temporary_file| {
        temporary_file.write_all(contents)?;
        temporary_file.sync_all()
    })
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
fn write_export_atomically_with(
    path: &std::path::Path,
    write: impl FnOnce(&mut File) -> io::Result<()>,
) -> io::Result<()> {
    let file_name = path
        .file_name()
        .ok_or_else(|| io::Error::other("atomic destination has no file name"))?;
    let mut temporary_name = file_name.to_os_string();
    temporary_name.push(format!(".{}.tmp", Uuid::now_v7()));
    let temporary_path = path.with_file_name(temporary_name);
    let mut temporary_file = fs::OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(&temporary_path)?;
    if let Err(error) = write(&mut temporary_file) {
        drop(temporary_file);
        let _ = fs::remove_file(&temporary_path);
        return Err(error);
    }
    drop(temporary_file);

    if let Err(error) = replace_file(&temporary_path, path) {
        let _ = fs::remove_file(&temporary_path);
        return Err(error);
    }
    Ok(())
}

#[cfg(all(
    any(feature = "desktop-cef", feature = "mobile-system-webview", test),
    not(target_os = "windows")
))]
fn replace_file(temporary_path: &std::path::Path, path: &std::path::Path) -> io::Result<()> {
    fs::rename(temporary_path, path)
}

#[cfg(all(
    any(feature = "desktop-cef", feature = "mobile-system-webview", test),
    target_os = "windows"
))]
fn replace_file(temporary_path: &std::path::Path, path: &std::path::Path) -> io::Result<()> {
    use std::os::windows::ffi::OsStrExt;

    const MOVEFILE_REPLACE_EXISTING: u32 = 0x1;
    const MOVEFILE_WRITE_THROUGH: u32 = 0x8;

    unsafe extern "system" {
        fn MoveFileExW(
            existing_file_name: *const u16,
            new_file_name: *const u16,
            flags: u32,
        ) -> i32;
    }

    let temporary_path: Vec<u16> = temporary_path
        .as_os_str()
        .encode_wide()
        .chain(std::iter::once(0))
        .collect();
    let path: Vec<u16> = path
        .as_os_str()
        .encode_wide()
        .chain(std::iter::once(0))
        .collect();
    // Both files are in the same directory, so MoveFileExW replaces the existing
    // destination without the non-atomic delete-and-rename fallback.
    if unsafe {
        MoveFileExW(
            temporary_path.as_ptr(),
            path.as_ptr(),
            MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    Ok(())
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn is_bundled_url(url: &Url) -> bool {
    let (scheme, host) = if cfg!(all(feature = "mobile-system-webview", target_os = "ios")) {
        ("tauri", "localhost")
    } else {
        ("http", "tauri.localhost")
    };

    url.port().is_none()
        && url.username().is_empty()
        && url.password().is_none()
        && url.scheme() == scheme
        && url.host_str() == Some(host)
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn is_local_ipc_request(url: &Url) -> bool {
    url.port().is_none()
        && url.username().is_empty()
        && url.password().is_none()
        && ((url.scheme() == "ipc" && url.host_str() == Some("localhost"))
            || (url.scheme() == "http" && url.host_str() == Some("ipc.localhost")))
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum WebResourceDecision {
    AllowBundledAsset,
    AllowIpc,
    Deny,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn web_resource_decision(uri: &str) -> WebResourceDecision {
    match uri.parse::<Url>() {
        Ok(url) if is_bundled_url(&url) => WebResourceDecision::AllowBundledAsset,
        Ok(url) if is_local_ipc_request(&url) => WebResourceDecision::AllowIpc,
        Ok(_) | Err(_) => WebResourceDecision::Deny,
    }
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn apply_web_resource_policy(
    request: Request<Vec<u8>>,
    response: &mut Response<Cow<'static, [u8]>>,
) {
    let decision = web_resource_decision(&request.uri().to_string());
    if decision == WebResourceDecision::Deny {
        *response.status_mut() = StatusCode::FORBIDDEN;
        *response.body_mut() = Cow::Borrowed(&[]);
        response.headers_mut().clear();
    }

    let headers = response.headers_mut();
    headers.insert(
        HeaderName::from_static("cache-control"),
        HeaderValue::from_static("no-store"),
    );
    if decision == WebResourceDecision::AllowIpc {
        return;
    }
    headers.insert(
        HeaderName::from_static("cross-origin-opener-policy"),
        HeaderValue::from_static("same-origin"),
    );
    headers.insert(
        HeaderName::from_static("cross-origin-resource-policy"),
        HeaderValue::from_static("same-origin"),
    );
    headers.insert(
        HeaderName::from_static("permissions-policy"),
        HeaderValue::from_static(PERMISSIONS_POLICY),
    );
    headers.insert(
        HeaderName::from_static("referrer-policy"),
        HeaderValue::from_static("no-referrer"),
    );
    headers.insert(
        HeaderName::from_static("x-content-type-options"),
        HeaderValue::from_static("nosniff"),
    );
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn bundled_origin(url: &Url) -> String {
    match url.host_str() {
        Some(host) => format!("{}://{host}", url.scheme()),
        None => url.scheme().to_string(),
    }
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
const fn runtime_name() -> &'static str {
    "cef"
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
const fn runtime_name() -> &'static str {
    "system-webview"
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
const fn operating_system() -> &'static str {
    if cfg!(target_os = "android") {
        "android"
    } else if cfg!(target_os = "ios") {
        "ios"
    } else if cfg!(target_os = "macos") {
        "macos"
    } else if cfg!(target_os = "windows") {
        "windows"
    } else {
        "linux"
    }
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
const fn update_policy() -> &'static str {
    if cfg!(any(target_os = "android", target_os = "ios")) {
        "Unsupported"
    } else {
        "Desktop updater unavailable"
    }
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn get_runtime_info(
    webview: Webview<ActiveRuntime>,
    _app: AppHandle<ActiveRuntime>,
    persistence: State<'_, PersistenceState>,
    startup_diagnostics: State<'_, Mutex<StartupDiagnostics>>,
) -> Result<RuntimeInfo, RuntimeCommandError> {
    let url = webview
        .url()
        .map_err(|_| RuntimeCommandError::NonBundledAsset)?;
    if !is_bundled_url(&url) {
        return Err(RuntimeCommandError::NonBundledAsset);
    }

    diagnostics::emit_runtime_ready(if cfg!(any(target_os = "android", target_os = "ios")) {
        diagnostics::DiagnosticClassification::MobileReady
    } else {
        diagnostics::DiagnosticClassification::DesktopReady
    });

    #[cfg(debug_assertions)]
    if std::env::var_os("DEVHUD_SMOKE").is_some_and(|value| value == "1") {
        let app = _app.clone();
        std::thread::spawn(move || {
            // CEF can still be initializing renderer frames when the frontend
            // invokes this command on Windows. Keep the hosted-runner delay
            // until a renderer-ready lifecycle signal is available.
            let shutdown_delay = if cfg!(target_os = "windows") {
                Duration::from_secs(10)
            } else {
                Duration::from_secs(1)
            };
            std::thread::sleep(shutdown_delay);
            if cfg!(target_os = "windows")
                && std::env::var_os("GITHUB_ACTIONS").is_some_and(|value| value == "true")
            {
                // GPU-less GitHub-hosted Windows runners can access-violate
                // inside CEF after the requested shutdown. This exact marker
                // lets the smoke distinguish that teardown from an app crash.
                eprintln!(r#"{{"eventId":"smoke-shutdown-requested"}}"#);
            }
            #[cfg(all(
                feature = "desktop-cef",
                not(any(target_os = "android", target_os = "ios"))
            ))]
            request_quit(&app);
            #[cfg(not(all(
                feature = "desktop-cef",
                not(any(target_os = "android", target_os = "ios"))
            )))]
            app.exit(0);
        });
    }

    #[cfg(all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ))]
    let surface = if webview.label() == SETTINGS_WINDOW_LABEL {
        RuntimeSurface::Settings
    } else {
        RuntimeSurface::Hud
    };
    #[cfg(all(
        feature = "mobile-system-webview",
        any(target_os = "android", target_os = "ios")
    ))]
    let surface = RuntimeSurface::Mobile;
    let first_run = matches!(persistence.read(SETTINGS_STORAGE_KEY), Ok(None));
    #[cfg(feature = "desktop-cef")]
    let (shortcut_startup_failure, autostart_startup_outcome) = startup_diagnostics
        .lock()
        .map(|diagnostics| (diagnostics.shortcut_failure, diagnostics.autostart_outcome))
        .unwrap_or((None, None));
    #[cfg(feature = "mobile-system-webview")]
    let _ = startup_diagnostics;

    Ok(RuntimeInfo {
        application_id: APPLICATION_ID,
        bundled_origin: bundled_origin(&url),
        operating_system: operating_system(),
        runtime: runtime_name(),
        sandbox_enabled: cfg!(not(any(target_os = "android", target_os = "ios"))),
        surface,
        first_run,
        #[cfg(feature = "desktop-cef")]
        shortcut_startup_failure,
        #[cfg(feature = "desktop-cef")]
        autostart_startup_outcome,
        update_policy: update_policy(),
    })
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn read_settings(
    state: State<'_, PersistenceState>,
) -> Result<Option<String>, PersistenceCommandError> {
    state.read(SETTINGS_STORAGE_KEY)
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn write_settings(
    record: String,
    state: State<'_, PersistenceState>,
) -> Result<(), PersistenceCommandError> {
    #[cfg(feature = "desktop-cef")]
    {
        state.write_frontend_settings(&record)
    }
    #[cfg(feature = "mobile-system-webview")]
    {
        state.write(SETTINGS_STORAGE_KEY, &record)
    }
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn read_shortcut_effective_state(
    state: State<'_, PersistenceState>,
) -> Result<Option<String>, PersistenceCommandError> {
    state.read(SHORTCUT_EFFECTIVE_STATE_STORAGE_KEY)
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn write_shortcut_effective_state(
    record: String,
    state: State<'_, PersistenceState>,
) -> Result<(), PersistenceCommandError> {
    state.write(SHORTCUT_EFFECTIVE_STATE_STORAGE_KEY, &record)
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn read_widget_configuration(
    state: State<'_, PersistenceState>,
) -> Result<Option<String>, PersistenceCommandError> {
    state.read(WIDGET_CONFIGURATION_STORAGE_KEY)
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
#[tauri::command]
fn read_widget_configuration(
    app: AppHandle<ActiveRuntime>,
) -> Result<Option<String>, PersistenceCommandError> {
    app.devhud_widget_bridge()
        .read_configuration()
        .map_err(|error| widget_bridge_failure("read", &error))
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn write_widget_configuration(
    record: String,
    state: State<'_, PersistenceState>,
) -> Result<(), PersistenceCommandError> {
    state.write(WIDGET_CONFIGURATION_STORAGE_KEY, &record)
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
#[tauri::command]
fn write_widget_configuration(
    record: String,
    app: AppHandle<ActiveRuntime>,
) -> Result<(), PersistenceCommandError> {
    validate_current_record(WIDGET_CONFIGURATION_STORAGE_KEY, &record)
        .ok_or(PersistenceCommandError::InvalidRecord)?;
    match app.devhud_widget_bridge().write_configuration(record) {
        Ok(_) => Ok(()),
        Err(error) if error.code() == Some(WidgetBridgeErrorCode::RefreshFailed) => {
            widget_bridge_failure("write-refresh", &error);
            Ok(())
        }
        Err(error) => Err(widget_bridge_failure("write", &error)),
    }
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
struct QuittingState(AtomicBool);

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
enum HudActionFailure {
    UnsupportedDisplay,
    WindowUnavailable,
    PositionFailed,
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(tag = "status", rename_all = "kebab-case")]
enum HudActionOutcome {
    Shown,
    Hidden,
    Unchanged { reason: HudActionFailure },
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct DisplayArea {
    bounds_x: i32,
    bounds_y: i32,
    bounds_width: u32,
    bounds_height: u32,
    work_x: i32,
    work_y: i32,
    work_width: u32,
    work_height: u32,
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
fn centered_hud_position(
    pointer: (f64, f64),
    displays: &[DisplayArea],
    hud_size: (u32, u32),
) -> Option<(i32, i32)> {
    let display = displays.iter().find(|display| {
        pointer.0 >= f64::from(display.bounds_x)
            && pointer.0 < f64::from(display.bounds_x) + f64::from(display.bounds_width)
            && pointer.1 >= f64::from(display.bounds_y)
            && pointer.1 < f64::from(display.bounds_y) + f64::from(display.bounds_height)
    })?;
    let available_x = display.work_width.saturating_sub(hud_size.0) / 2;
    let available_y = display.work_height.saturating_sub(hud_size.1) / 2;
    Some((
        display
            .work_x
            .saturating_add(i32::try_from(available_x).unwrap_or(i32::MAX)),
        display
            .work_y
            .saturating_add(i32::try_from(available_y).unwrap_or(i32::MAX)),
    ))
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn display_server_supported() -> bool {
    #[cfg(target_os = "linux")]
    {
        std::env::var_os("DISPLAY").is_some_and(|display| !display.is_empty())
    }
    #[cfg(not(target_os = "linux"))]
    {
        true
    }
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn show_hud_internal(app: &AppHandle<ActiveRuntime>, toggle: bool) -> HudActionOutcome {
    if !display_server_supported() {
        return HudActionOutcome::Unchanged {
            reason: HudActionFailure::UnsupportedDisplay,
        };
    }
    let Some(window) = app.get_webview_window(MAIN_WINDOW_LABEL) else {
        return HudActionOutcome::Unchanged {
            reason: HudActionFailure::WindowUnavailable,
        };
    };
    if toggle {
        match window.is_visible() {
            Ok(true) if window.hide().is_ok() => return HudActionOutcome::Hidden,
            Ok(true) | Err(_) => {
                return HudActionOutcome::Unchanged {
                    reason: HudActionFailure::WindowUnavailable,
                };
            }
            Ok(false) => {}
        }
    }

    let pointer = match app.cursor_position() {
        Ok(pointer) => pointer,
        Err(_) => {
            return HudActionOutcome::Unchanged {
                reason: HudActionFailure::UnsupportedDisplay,
            };
        }
    };
    let displays = match app.available_monitors() {
        Ok(monitors) => monitors
            .iter()
            .map(|monitor| {
                let work_area = monitor.work_area();
                let bounds = monitor.position();
                let size = monitor.size();
                DisplayArea {
                    bounds_x: bounds.x,
                    bounds_y: bounds.y,
                    bounds_width: size.width,
                    bounds_height: size.height,
                    work_x: work_area.position.x,
                    work_y: work_area.position.y,
                    work_width: work_area.size.width,
                    work_height: work_area.size.height,
                }
            })
            .collect::<Vec<_>>(),
        Err(_) => {
            return HudActionOutcome::Unchanged {
                reason: HudActionFailure::UnsupportedDisplay,
            };
        }
    };
    let size = match window.outer_size() {
        Ok(size) => size,
        Err(_) => {
            return HudActionOutcome::Unchanged {
                reason: HudActionFailure::PositionFailed,
            };
        }
    };
    let Some((x, y)) =
        centered_hud_position((pointer.x, pointer.y), &displays, (size.width, size.height))
    else {
        return HudActionOutcome::Unchanged {
            reason: HudActionFailure::UnsupportedDisplay,
        };
    };
    if window
        .set_position(PhysicalPosition::new(x, y))
        .and_then(|()| window.set_always_on_top(true))
        .is_err()
    {
        return HudActionOutcome::Unchanged {
            reason: HudActionFailure::PositionFailed,
        };
    }
    if window
        .unminimize()
        .and_then(|()| window.show())
        .and_then(|()| window.set_focus())
        .is_err()
    {
        return HudActionOutcome::Unchanged {
            reason: HudActionFailure::WindowUnavailable,
        };
    }
    let _ = window.eval("window.dispatchEvent(new Event('devhud:shown'))");
    HudActionOutcome::Shown
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn hide_hud_internal(app: &AppHandle<ActiveRuntime>) -> HudActionOutcome {
    match app.get_webview_window(MAIN_WINDOW_LABEL) {
        Some(window) if window.hide().is_ok() => HudActionOutcome::Hidden,
        _ => HudActionOutcome::Unchanged {
            reason: HudActionFailure::WindowUnavailable,
        },
    }
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn hide_hud(app: AppHandle<ActiveRuntime>) -> HudActionOutcome {
    let outcome = hide_hud_internal(&app);
    if let HudActionOutcome::Unchanged { reason } = outcome {
        log_window_action_failure("hud-hide", reason);
    }
    outcome
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn build_settings_window(
    app: &AppHandle<ActiveRuntime>,
) -> tauri::Result<tauri::WebviewWindow<ActiveRuntime>> {
    WebviewWindowBuilder::new(
        app,
        SETTINGS_WINDOW_LABEL,
        WebviewUrl::App("index.html".into()),
    )
    .title("DevHud Settings")
    .inner_size(560.0, 680.0)
    .min_inner_size(420.0, 540.0)
    .center()
    .skip_taskbar(true)
    .devtools(true)
    .incognito(true)
    .disable_drag_drop_handler()
    .on_navigation(is_bundled_url)
    .on_new_window(|_, _| NewWindowResponse::Deny)
    .on_download(|_, _| false)
    .on_web_resource_request(apply_web_resource_policy)
    .build()
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn show_settings_internal(app: &AppHandle<ActiveRuntime>) -> Result<(), HudActionFailure> {
    let window = match app.get_webview_window(SETTINGS_WINDOW_LABEL) {
        Some(window) => window,
        None => build_settings_window(app).map_err(|_| HudActionFailure::WindowUnavailable)?,
    };
    window
        .unminimize()
        .and_then(|()| window.show())
        .and_then(|()| window.set_focus())
        .map_err(|_| HudActionFailure::WindowUnavailable)
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn show_settings(app: AppHandle<ActiveRuntime>) -> Result<(), HudActionFailure> {
    let result = show_settings_internal(&app);
    if let Err(reason) = result {
        log_window_action_failure("hud-open-settings", reason);
    }
    result
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn hide_settings_internal(app: &AppHandle<ActiveRuntime>) -> Result<(), HudActionFailure> {
    app.get_webview_window(SETTINGS_WINDOW_LABEL)
        .ok_or(HudActionFailure::WindowUnavailable)?
        .hide()
        .map_err(|_| HudActionFailure::WindowUnavailable)
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn hide_settings(app: AppHandle<ActiveRuntime>) -> Result<(), HudActionFailure> {
    let result = hide_settings_internal(&app);
    if let Err(reason) = result {
        log_window_action_failure("hud-hide-settings", reason);
    }
    result
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn persist_settings_field(
    persistence: &PersistenceState,
    field: &str,
    value: serde_json::Value,
) -> Result<(), PersistenceCommandError> {
    persistence.update_settings_field(field, value)
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn log_shortcut_integration_failure(
    _operation: &'static str,
    reason: shortcut::ShortcutFailure,
    _effective_state: &'static str,
) {
    let classification = match reason {
        shortcut::ShortcutFailure::Malformed => {
            diagnostics::DiagnosticClassification::ShortcutMalformed
        }
        shortcut::ShortcutFailure::Conflict => {
            diagnostics::DiagnosticClassification::ShortcutConflict
        }
        shortcut::ShortcutFailure::PermissionDenied => {
            diagnostics::DiagnosticClassification::ShortcutPermissionDenied
        }
        shortcut::ShortcutFailure::RegistrationFailed => {
            diagnostics::DiagnosticClassification::ShortcutRegistrationFailed
        }
        shortcut::ShortcutFailure::UnsupportedDisplay => {
            diagnostics::DiagnosticClassification::DisplayUnsupported
        }
        shortcut::ShortcutFailure::StorageFailed => {
            diagnostics::DiagnosticClassification::ShortcutStorageFailed
        }
    };
    diagnostics::emit_warning(
        if reason == shortcut::ShortcutFailure::UnsupportedDisplay {
            diagnostics::DiagnosticEventId::DisplayOutcome
        } else {
            diagnostics::DiagnosticEventId::ShortcutOutcome
        },
        classification,
    );
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn log_autostart_integration_failure(
    _operation: &'static str,
    reason: autostart::AutostartFailure,
    _effective_enabled: Option<bool>,
) {
    let classification = match reason {
        autostart::AutostartFailure::PermissionDenied => {
            diagnostics::DiagnosticClassification::AutostartPermissionDenied
        }
        autostart::AutostartFailure::OperationFailed => {
            diagnostics::DiagnosticClassification::AutostartOperationFailed
        }
        autostart::AutostartFailure::StorageFailed => {
            diagnostics::DiagnosticClassification::AutostartStorageFailed
        }
    };
    diagnostics::emit_warning(
        diagnostics::DiagnosticEventId::AutostartOutcome,
        classification,
    );
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn log_window_action_failure(_operation: &'static str, reason: HudActionFailure) {
    let classification = match reason {
        HudActionFailure::UnsupportedDisplay => {
            diagnostics::DiagnosticClassification::DisplayUnsupported
        }
        HudActionFailure::WindowUnavailable => {
            diagnostics::DiagnosticClassification::DisplayWindowUnavailable
        }
        HudActionFailure::PositionFailed => {
            diagnostics::DiagnosticClassification::DisplayPositionFailed
        }
    };
    diagnostics::emit_warning(
        diagnostics::DiagnosticEventId::DisplayOutcome,
        classification,
    );
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn replace_global_shortcut(
    candidate: Option<serde_json::Value>,
    state: State<'_, Mutex<shortcut::ShortcutState>>,
    persistence: State<'_, PersistenceState>,
    startup_diagnostics: State<'_, Mutex<StartupDiagnostics>>,
) -> shortcut::ShortcutReplacementOutcome {
    match state.lock() {
        Ok(mut state) => {
            let previous = state.active_shortcut();
            let outcome = state.replace(candidate);
            if let shortcut::ShortcutReplacementOutcome::Replaced { shortcut } = &outcome {
                let value = serde_json::to_value(shortcut).unwrap_or(serde_json::Value::Null);
                if persist_settings_field(&persistence, "shortcut", value).is_err() {
                    let rollback_failed = if let Err(reason) = state.rollback(previous) {
                        log_shortcut_integration_failure(
                            "replace-rollback",
                            reason,
                            if state.active_shortcut().is_some() {
                                "configured"
                            } else {
                                "not-configured"
                            },
                        );
                        true
                    } else {
                        false
                    };
                    log_shortcut_integration_failure(
                        "replace-persist",
                        shortcut::ShortcutFailure::StorageFailed,
                        if state.active_shortcut().is_some() {
                            "configured"
                        } else {
                            "not-configured"
                        },
                    );
                    return shortcut::ShortcutReplacementOutcome::Unchanged {
                        reason: shortcut::ShortcutFailure::StorageFailed,
                        shortcut: if rollback_failed {
                            state.active_shortcut()
                        } else {
                            None
                        },
                    };
                }
                if let Ok(mut diagnostics) = startup_diagnostics.lock() {
                    diagnostics.shortcut_failure = None;
                }
            }
            if let shortcut::ShortcutReplacementOutcome::Unchanged { reason, .. } = &outcome {
                log_shortcut_integration_failure(
                    "replace",
                    *reason,
                    if state.active_shortcut().is_some() {
                        "configured"
                    } else {
                        "not-configured"
                    },
                );
                return shortcut::ShortcutReplacementOutcome::Unchanged {
                    reason: *reason,
                    shortcut: None,
                };
            }
            outcome
        }
        Err(_) => {
            log_shortcut_integration_failure(
                "replace",
                shortcut::ShortcutFailure::RegistrationFailed,
                "unknown",
            );
            shortcut::ShortcutReplacementOutcome::Unchanged {
                reason: shortcut::ShortcutFailure::RegistrationFailed,
                shortcut: None,
            }
        }
    }
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn set_launch_at_login(
    enabled: bool,
    state: State<'_, autostart::AutostartState>,
    persistence: State<'_, PersistenceState>,
    startup_diagnostics: State<'_, Mutex<StartupDiagnostics>>,
) -> autostart::AutostartOutcome {
    let (previous, outcome) = state.apply_with_previous(enabled);
    if matches!(outcome, autostart::AutostartOutcome::Applied { .. })
        && persist_settings_field(
            &persistence,
            "launchAtLogin",
            serde_json::Value::Bool(enabled),
        )
        .is_err()
    {
        let rollback = previous.map_or_else(
            || autostart::AutostartOutcome::Unknown {
                reason: autostart::AutostartFailure::OperationFailed,
            },
            |previous| state.apply(previous),
        );
        if let autostart::AutostartOutcome::Unchanged { enabled, reason } = rollback {
            log_autostart_integration_failure("change-rollback", reason, Some(enabled));
        } else if let autostart::AutostartOutcome::Unknown { reason } = rollback {
            log_autostart_integration_failure("change-rollback", reason, None);
        }
        log_autostart_integration_failure(
            "change-persist",
            autostart::AutostartFailure::StorageFailed,
            rollback.enabled(),
        );
        return match rollback.enabled() {
            Some(enabled) => autostart::AutostartOutcome::Unchanged {
                enabled,
                reason: autostart::AutostartFailure::StorageFailed,
            },
            None => autostart::AutostartOutcome::Unknown {
                reason: autostart::AutostartFailure::StorageFailed,
            },
        };
    }
    if matches!(outcome, autostart::AutostartOutcome::Applied { .. })
        && let Ok(mut diagnostics) = startup_diagnostics.lock()
    {
        diagnostics.autostart_outcome = None;
    }
    if let autostart::AutostartOutcome::Unchanged { enabled, reason } = outcome {
        log_autostart_integration_failure("change", reason, Some(enabled));
    } else if let autostart::AutostartOutcome::Unknown { reason } = outcome {
        log_autostart_integration_failure("change", reason, None);
    }
    outcome
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(tag = "status", rename_all = "kebab-case")]
enum FirstRunOutcome {
    Completed,
    Unchanged { reason: PersistenceCommandError },
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn complete_first_run(persistence: State<'_, PersistenceState>) -> FirstRunOutcome {
    match persistence.read(SETTINGS_STORAGE_KEY) {
        Ok(None) => {
            let record = r#"{"version":1,"settings":{"theme":"system","launchAtLogin":false,"shortcut":null}}"#;
            if let Err(reason) = persistence.write(SETTINGS_STORAGE_KEY, record) {
                return FirstRunOutcome::Unchanged { reason };
            }
        }
        Ok(Some(_)) => {}
        Err(reason) => return FirstRunOutcome::Unchanged { reason },
    }
    FirstRunOutcome::Completed
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn request_update_action(
    boundary: State<'_, updater::UpdateActionBoundary>,
) -> updater::UpdateActionOutcome {
    boundary.request()
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn request_quit(app: &AppHandle<ActiveRuntime>) {
    app.state::<QuittingState>()
        .0
        .store(true, Ordering::Release);
    app.exit(0);
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn create_tray(app: &AppHandle<ActiveRuntime>) -> tauri::Result<()> {
    let items = TRAY_ACTIONS
        .iter()
        .map(|(id, title)| MenuItem::with_id(app, *id, *title, true, None::<&str>))
        .collect::<tauri::Result<Vec<_>>>()?;
    let item_refs = items
        .iter()
        .map(|item| item as &dyn tauri::menu::IsMenuItem<ActiveRuntime>)
        .collect::<Vec<_>>();
    let menu = Menu::with_items(app, &item_refs)?;
    let mut tray = TrayIconBuilder::with_id("devhud")
        .tooltip("DevHud")
        .menu(&menu)
        .show_menu_on_left_click(true)
        .on_menu_event(|app, event| match event.id.as_ref() {
            "open-devhud" => {
                if let HudActionOutcome::Unchanged { reason } = show_hud_internal(app, false) {
                    log_window_action_failure("tray-open-devhud", reason);
                }
            }
            "settings" => {
                if let Err(reason) = show_settings_internal(app) {
                    log_window_action_failure("tray-open-settings", reason);
                }
            }
            "check-for-updates" => {
                let _outcome = app.state::<updater::UpdateActionBoundary>().request();
                diagnostics::emit(
                    diagnostics::DiagnosticEventId::UpdaterOutcome,
                    diagnostics::DiagnosticClassification::UpdaterUnavailable,
                );
            }
            "open-devtools" => {
                if let Some(window) = app.get_webview_window(MAIN_WINDOW_LABEL) {
                    window.open_devtools();
                }
            }
            "quit" => request_quit(app),
            _ => {}
        });
    #[cfg(target_os = "macos")]
    let tray_icon = Image::from_bytes(include_bytes!(
        "../../assets/tray/devhud-tray-template@2x.png"
    ))
    .ok();
    #[cfg(not(target_os = "macos"))]
    let tray_icon = Image::from_bytes(include_bytes!("../../assets/tray/devhud-tray@2x.png")).ok();
    if let Some(icon) = tray_icon {
        tray = tray.icon(icon);
    }
    #[cfg(target_os = "macos")]
    {
        tray = tray.icon_as_template(true);
    }
    tray.build(app)?;
    Ok(())
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn install_shortcut_handler(app: &AppHandle<ActiveRuntime>) {
    use global_hotkey::{GlobalHotKeyEvent, HotKeyState};

    let app = app.clone();
    GlobalHotKeyEvent::set_event_handler(Some(move |event: GlobalHotKeyEvent| {
        if event.state != HotKeyState::Pressed {
            return;
        }
        let is_active = app
            .state::<Mutex<shortcut::ShortcutState>>()
            .lock()
            .ok()
            .and_then(|state| state.active_id())
            == Some(event.id);
        if is_active {
            let dispatch = app.clone();
            let action = dispatch.clone();
            if dispatch
                .run_on_main_thread(move || {
                    if let HudActionOutcome::Unchanged { reason } = show_hud_internal(&action, true)
                    {
                        log_window_action_failure("shortcut-toggle", reason);
                    }
                })
                .is_err()
            {
                log_window_action_failure("shortcut-dispatch", HudActionFailure::WindowUnavailable);
            }
        }
    }));
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn browsing_data_reset_failure() -> PersistenceCommandError {
    diagnostics::emit_warning(
        diagnostics::DiagnosticEventId::PersistenceResetFailure,
        diagnostics::DiagnosticClassification::PersistenceResetFailed,
    );
    PersistenceCommandError::ResetFailed
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn clear_browsing_data_for_reset(
    app: &AppHandle<ActiveRuntime>,
) -> Result<(), PersistenceCommandError> {
    for label in [MAIN_WINDOW_LABEL, SETTINGS_WINDOW_LABEL] {
        if let Some(window) = app.get_webview_window(label) {
            window
                .clear_all_browsing_data()
                .map_err(|_| browsing_data_reset_failure())?;
        }
    }
    Ok(())
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
fn clear_browsing_data_for_reset(
    webview: Webview<ActiveRuntime>,
) -> Result<(), PersistenceCommandError> {
    webview
        .clear_all_browsing_data()
        .map_err(|_| browsing_data_reset_failure())
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[cfg_attr(test, allow(dead_code))]
fn clear_local_logs_for_reset(log_directory: &Path) -> Result<(), PersistenceCommandError> {
    let result = if let Some(diagnostics) = diagnostics::active() {
        diagnostics.clear(log_directory)
    } else {
        local_log::LocalLogWriter::clear_managed_in(log_directory)
    };
    result.map_err(|_| {
        diagnostics::emit_warning(
            diagnostics::DiagnosticEventId::PersistenceResetFailure,
            diagnostics::DiagnosticClassification::PersistenceResetFailed,
        );
        PersistenceCommandError::ResetFailed
    })
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[cfg_attr(test, allow(dead_code))]
fn preflight_local_logs_for_reset(log_directory: &Path) -> Result<(), PersistenceCommandError> {
    let result = if let Some(diagnostics) = diagnostics::active() {
        diagnostics.preflight_clear(log_directory)
    } else {
        local_log::LocalLogWriter::preflight_clear_managed_in(log_directory)
    };
    result.map_err(|_| PersistenceCommandError::ResetFailed)
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn reset_preflight_failure(error: PersistenceCommandError) -> PersistenceCommandError {
    diagnostics::emit_warning(
        diagnostics::DiagnosticEventId::PersistenceResetFailure,
        diagnostics::DiagnosticClassification::PersistenceResetPreflightFailed,
    );
    error
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
const DIAGNOSTICS_EXPORT_FILE_NAME: &str = "DevHud-diagnostics.jsonl";

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn diagnostics_bundle() -> Result<Vec<u8>, DiagnosticsExportError> {
    diagnostics::active()
        .ok_or(DiagnosticsExportError::Unavailable)?
        .sanitized_bundle()
        .map_err(|_| DiagnosticsExportError::Unavailable)
}

#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
fn export_selected_destination<T, S, W>(
    select: S,
    write: W,
) -> Result<DiagnosticsExportOutcome, DiagnosticsExportError>
where
    S: FnOnce() -> Option<T>,
    W: FnOnce(T) -> Result<(), DiagnosticsExportError>,
{
    let Some(destination) = select() else {
        return Ok(DiagnosticsExportOutcome::Cancelled);
    };
    write(destination)?;
    Ok(DiagnosticsExportOutcome::Exported)
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn export_diagnostics() -> Result<DiagnosticsExportOutcome, DiagnosticsExportError> {
    let outcome = export_selected_destination(
        || {
            rfd::FileDialog::new()
                .set_file_name(DIAGNOSTICS_EXPORT_FILE_NAME)
                .add_filter("DevHud diagnostics", &["jsonl"])
                .save_file()
        },
        |destination| {
            if diagnostics::active()
                .is_some_and(|diagnostics| diagnostics.destination_is_managed(&destination))
                || dirs::cache_dir()
                    .ok_or(DiagnosticsExportError::WriteFailed)
                    .and_then(|cache_base| {
                        destination_is_cef_reset_owned(&cache_base, &destination)
                            .map_err(|_| DiagnosticsExportError::WriteFailed)
                    })?
            {
                return Err(DiagnosticsExportError::WriteFailed);
            }
            let bundle = diagnostics_bundle()?;
            write_export_atomically(&destination, &bundle)
                .map_err(|_| DiagnosticsExportError::WriteFailed)
        },
    );
    match outcome {
        Ok(DiagnosticsExportOutcome::Exported) => diagnostics::emit(
            diagnostics::DiagnosticEventId::DiagnosticsExportOutcome,
            diagnostics::DiagnosticClassification::DiagnosticsExported,
        ),
        Err(_) => diagnostics::emit_warning(
            diagnostics::DiagnosticEventId::DiagnosticsExportOutcome,
            diagnostics::DiagnosticClassification::DiagnosticsExportFailed,
        ),
        Ok(DiagnosticsExportOutcome::Cancelled) => {}
    }
    outcome
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
#[tauri::command]
fn export_diagnostics(
    app: AppHandle<ActiveRuntime>,
) -> Result<DiagnosticsExportOutcome, DiagnosticsExportError> {
    let bundle = diagnostics_bundle()?;
    let bundle = String::from_utf8(bundle).map_err(|_| DiagnosticsExportError::Unavailable)?;
    let outcome = app
        .devhud_diagnostics_bridge()
        .export(DIAGNOSTICS_EXPORT_FILE_NAME.to_string(), bundle)
        .map_err(|error| match error.code() {
            Some(
                DiagnosticsBridgeErrorCode::Busy | DiagnosticsBridgeErrorCode::PickerUnavailable,
            ) => DiagnosticsExportError::PickerUnavailable,
            Some(DiagnosticsBridgeErrorCode::WriteFailed) => DiagnosticsExportError::WriteFailed,
            None => DiagnosticsExportError::Unavailable,
        })
        .map(|outcome| match outcome {
            MobileDiagnosticsExportOutcome::Exported => DiagnosticsExportOutcome::Exported,
            MobileDiagnosticsExportOutcome::Cancelled => DiagnosticsExportOutcome::Cancelled,
        });
    match outcome {
        Ok(DiagnosticsExportOutcome::Exported) => diagnostics::emit(
            diagnostics::DiagnosticEventId::DiagnosticsExportOutcome,
            diagnostics::DiagnosticClassification::DiagnosticsExported,
        ),
        Err(_) => diagnostics::emit_warning(
            diagnostics::DiagnosticEventId::DiagnosticsExportOutcome,
            diagnostics::DiagnosticClassification::DiagnosticsExportFailed,
        ),
        Ok(DiagnosticsExportOutcome::Cancelled) => {}
    }
    outcome
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn reset_dev_hud(
    app: AppHandle<ActiveRuntime>,
    persistence: State<'_, PersistenceState>,
    auth_state: State<'_, auth_native::NativeAuthState>,
    shortcut_state: State<'_, Mutex<shortcut::ShortcutState>>,
    autostart_state: State<'_, autostart::AutostartState>,
    startup_diagnostics: State<'_, Mutex<StartupDiagnostics>>,
) -> Result<PersistenceResetOutcome, PersistenceCommandError> {
    persistence
        .preflight_reset()
        .map_err(reset_preflight_failure)?;
    let cache_base = dirs::cache_dir()
        .ok_or_else(|| reset_preflight_failure(PersistenceCommandError::ResetFailed))?;
    let cef_profile = cef_profile_directory()
        .map_err(|_| reset_preflight_failure(PersistenceCommandError::ResetFailed))?;
    preflight_cef_profile_reset(&cache_base, &cef_profile)
        .map_err(|_| reset_preflight_failure(PersistenceCommandError::ResetFailed))?;
    let log_directory = local_log::managed_log_directory(APPLICATION_ID)
        .map_err(|_| reset_preflight_failure(PersistenceCommandError::ResetFailed))?;
    preflight_local_logs_for_reset(&log_directory).map_err(reset_preflight_failure)?;
    if auth_state.reset().is_err() {
        return Ok(PersistenceResetOutcome::PartiallyRetained);
    }
    if clear_browsing_data_for_reset(&app).is_err() {
        return Ok(PersistenceResetOutcome::PartiallyRetained);
    }
    let mut shortcuts = match shortcut_state.lock() {
        Ok(shortcuts) => shortcuts,
        Err(_) => {
            log_shortcut_integration_failure(
                "reset",
                shortcut::ShortcutFailure::RegistrationFailed,
                "unknown",
            );
            return Ok(PersistenceResetOutcome::PartiallyRetained);
        }
    };
    let previous_shortcut = shortcuts.active_shortcut();
    if let Err(reason) = shortcuts.clear() {
        log_shortcut_integration_failure("reset", reason, "configured");
        return Ok(PersistenceResetOutcome::PartiallyRetained);
    }

    let Some(previous_autostart) = autostart_state.current() else {
        log_autostart_integration_failure(
            "reset-snapshot",
            autostart::AutostartFailure::OperationFailed,
            None,
        );
        let shortcut_rollback = shortcuts.rollback(previous_shortcut);
        if let Err(reason) = shortcut_rollback {
            log_shortcut_integration_failure(
                "reset-rollback",
                reason,
                if shortcuts.active_shortcut().is_some() {
                    "configured"
                } else {
                    "not-configured"
                },
            );
        }
        if shortcut_rollback.is_err() {
            return Ok(PersistenceResetOutcome::IntegrationRollbackFailed {
                shortcut: shortcuts.active_shortcut(),
                launch_at_login: None,
            });
        }
        return Ok(PersistenceResetOutcome::PartiallyRetained);
    };
    let autostart_outcome = autostart_state.apply(false);
    if let autostart::AutostartOutcome::Unchanged { enabled, reason } = autostart_outcome {
        log_autostart_integration_failure("reset", reason, Some(enabled));
        let shortcut_rollback = shortcuts.rollback(previous_shortcut);
        if let Err(reason) = shortcut_rollback {
            log_shortcut_integration_failure(
                "reset-rollback",
                reason,
                if shortcuts.active_shortcut().is_some() {
                    "configured"
                } else {
                    "not-configured"
                },
            );
        }
        if shortcut_rollback.is_err() || enabled != previous_autostart {
            return Ok(PersistenceResetOutcome::IntegrationRollbackFailed {
                shortcut: shortcuts.active_shortcut(),
                launch_at_login: Some(enabled),
            });
        }
        return Ok(PersistenceResetOutcome::PartiallyRetained);
    } else if let autostart::AutostartOutcome::Unknown { reason } = autostart_outcome {
        log_autostart_integration_failure("reset", reason, None);
        let shortcut_rollback = shortcuts.rollback(previous_shortcut);
        if let Err(reason) = shortcut_rollback {
            log_shortcut_integration_failure(
                "reset-rollback",
                reason,
                if shortcuts.active_shortcut().is_some() {
                    "configured"
                } else {
                    "not-configured"
                },
            );
        }
        return Ok(PersistenceResetOutcome::IntegrationRollbackFailed {
            shortcut: shortcuts.active_shortcut(),
            launch_at_login: None,
        });
    }

    if clear_local_logs_for_reset(&log_directory).is_err() {
        let autostart_rollback = autostart_state.apply(previous_autostart);
        let autostart_rollback_failed = !matches!(
            autostart_rollback,
            autostart::AutostartOutcome::Applied { enabled }
                if enabled == previous_autostart
        );
        if let autostart::AutostartOutcome::Unchanged { enabled, reason } = autostart_rollback {
            log_autostart_integration_failure("reset-rollback", reason, Some(enabled));
        } else if let autostart::AutostartOutcome::Unknown { reason } = autostart_rollback {
            log_autostart_integration_failure("reset-rollback", reason, None);
        }
        let shortcut_rollback = shortcuts.rollback(previous_shortcut);
        if let Err(reason) = shortcut_rollback {
            log_shortcut_integration_failure(
                "reset-rollback",
                reason,
                if shortcuts.active_shortcut().is_some() {
                    "configured"
                } else {
                    "not-configured"
                },
            );
        }
        if autostart_rollback_failed || shortcut_rollback.is_err() {
            return Ok(PersistenceResetOutcome::IntegrationRollbackFailed {
                shortcut: shortcuts.active_shortcut(),
                launch_at_login: autostart_rollback.enabled(),
            });
        }
        return Ok(PersistenceResetOutcome::PartiallyRetained);
    }

    let mut reset_outcome = match persistence.reset() {
        Ok(()) => PersistenceResetOutcome::Complete,
        Err(PersistenceResetFailure::BeforeRecordsRemoved(_)) => {
            let autostart_rollback = autostart_state.apply(previous_autostart);
            let autostart_rollback_failed = !matches!(
                autostart_rollback,
                autostart::AutostartOutcome::Applied { enabled }
                    if enabled == previous_autostart
            );
            if let autostart::AutostartOutcome::Unchanged { enabled, reason } = autostart_rollback {
                log_autostart_integration_failure("reset-rollback", reason, Some(enabled));
            } else if let autostart::AutostartOutcome::Unknown { reason } = autostart_rollback {
                log_autostart_integration_failure("reset-rollback", reason, None);
            }
            let shortcut_rollback = shortcuts.rollback(previous_shortcut);
            if let Err(reason) = shortcut_rollback {
                log_shortcut_integration_failure(
                    "reset-rollback",
                    reason,
                    if shortcuts.active_shortcut().is_some() {
                        "configured"
                    } else {
                        "not-configured"
                    },
                );
            }
            if autostart_rollback_failed || shortcut_rollback.is_err() {
                return Ok(PersistenceResetOutcome::IntegrationRollbackFailed {
                    shortcut: shortcuts.active_shortcut(),
                    launch_at_login: autostart_rollback.enabled(),
                });
            }
            return Ok(PersistenceResetOutcome::PartiallyRetained);
        }
        Err(PersistenceResetFailure::PartiallyRetained) => {
            PersistenceResetOutcome::PartiallyRetained
        }
        Err(PersistenceResetFailure::CleanupFailed) => PersistenceResetOutcome::CleanupFailed,
    };
    if let Err(failure) = reset_cef_profile_directory(&cache_base, &cef_profile) {
        diagnostics::emit_warning(
            diagnostics::DiagnosticEventId::PersistenceResetCleanupFailure,
            diagnostics::DiagnosticClassification::CefProfileCleanupFailed,
        );
        match failure {
            PersistenceResetFailure::PartiallyRetained => {
                reset_outcome = PersistenceResetOutcome::PartiallyRetained;
            }
            PersistenceResetFailure::CleanupFailed
            | PersistenceResetFailure::BeforeRecordsRemoved(_) => {
                if reset_outcome != PersistenceResetOutcome::PartiallyRetained {
                    reset_outcome = PersistenceResetOutcome::CleanupFailed;
                }
            }
        }
    }

    if let Ok(mut diagnostics) = startup_diagnostics.lock() {
        diagnostics.shortcut_failure = None;
        diagnostics.autostart_outcome = None;
    }
    if reset_outcome == PersistenceResetOutcome::Complete {
        diagnostics::emit(
            diagnostics::DiagnosticEventId::PersistenceReset,
            diagnostics::DiagnosticClassification::PersistenceResetComplete,
        );
    }
    Ok(reset_outcome)
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
#[tauri::command]
fn reset_dev_hud(
    app: AppHandle<ActiveRuntime>,
    webview: Webview<ActiveRuntime>,
    state: State<'_, PersistenceState>,
    auth_state: State<'_, auth_native::NativeAuthState>,
) -> Result<PersistenceResetOutcome, PersistenceCommandError> {
    state.preflight_reset().map_err(reset_preflight_failure)?;
    app.devhud_widget_bridge()
        .prepare_reset()
        .map_err(|error| widget_bridge_failure("reset-preflight", &error))
        .map_err(reset_preflight_failure)?;
    let log_directory = app
        .path()
        .app_log_dir()
        .map_err(|_| reset_preflight_failure(PersistenceCommandError::ResetFailed))?;
    preflight_local_logs_for_reset(&log_directory).map_err(reset_preflight_failure)?;
    if auth_state.reset().is_err() {
        return Ok(PersistenceResetOutcome::PartiallyRetained);
    }
    if clear_browsing_data_for_reset(webview).is_err()
        || clear_local_logs_for_reset(&log_directory).is_err()
    {
        return Ok(PersistenceResetOutcome::PartiallyRetained);
    }
    let reset_outcome = match state.reset() {
        Ok(()) => PersistenceResetOutcome::Complete,
        Err(PersistenceResetFailure::BeforeRecordsRemoved(_)) => {
            return Ok(PersistenceResetOutcome::PartiallyRetained);
        }
        Err(PersistenceResetFailure::PartiallyRetained) => {
            PersistenceResetOutcome::PartiallyRetained
        }
        Err(PersistenceResetFailure::CleanupFailed) => PersistenceResetOutcome::CleanupFailed,
    };
    match app.devhud_widget_bridge().reset_configuration() {
        Ok(_) => {}
        Err(error) if error.code() == Some(WidgetBridgeErrorCode::RefreshFailed) => {
            widget_bridge_failure("reset-refresh", &error);
        }
        Err(error) => {
            widget_bridge_failure("reset", &error);
            return Ok(PersistenceResetOutcome::PartiallyRetained);
        }
    }
    if reset_outcome == PersistenceResetOutcome::Complete {
        diagnostics::emit(
            diagnostics::DiagnosticEventId::PersistenceReset,
            diagnostics::DiagnosticClassification::PersistenceResetComplete,
        );
    }
    Ok(reset_outcome)
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn realqa_inspect_capture_capabilities(
    state: State<'_, realqa_capture::CaptureCore>,
) -> Result<realqa_capture::CaptureCapabilities, realqa_capture::CaptureFailure> {
    let result = state.inspect_capabilities();
    realqa_capture::record_outcome(&result);
    result
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
async fn get_auth_session(
    app: AppHandle<ActiveRuntime>,
) -> Result<auth::SessionSnapshot, auth::AuthError> {
    tauri::async_runtime::spawn_blocking(move || {
        app.state::<auth_native::NativeAuthState>().snapshot()
    })
    .await
    .map_err(|_| auth::AuthError::TransportUnavailable)?
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
#[tauri::command]
async fn get_auth_session(
    app: AppHandle<ActiveRuntime>,
) -> Result<auth::SessionSnapshot, auth::AuthError> {
    tauri::async_runtime::spawn_blocking(move || {
        app.state::<auth_native::NativeAuthState>()
            .poll_mobile_callback(&app)
    })
    .await
    .map_err(|_| auth::AuthError::TransportUnavailable)?
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn realqa_list_capture_sources(
    state: State<'_, realqa_capture::CaptureCore>,
) -> Result<realqa_capture::CaptureSourceCatalog, realqa_capture::CaptureFailure> {
    let result = state.source_catalog();
    realqa_capture::record_outcome(&result);
    result
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
async fn start_authentication(
    feature: auth::AuthFeature,
    state: State<'_, auth_native::NativeAuthState>,
) -> Result<auth::SessionSnapshot, auth::AuthError> {
    auth_native::feature_supported(feature)?;
    let (authorization_url, callback) = state.begin_desktop(feature)?;
    if let Err(error) = auth_native::open_authorization_url(&authorization_url) {
        state.cancel_pending();
        return Err(error);
    }
    let callback = match tauri::async_runtime::spawn_blocking(move || callback.receive()).await {
        Ok(Ok(callback)) => callback,
        Ok(Err(error)) => {
            state.cancel_pending();
            return Err(error);
        }
        Err(_) => {
            state.cancel_pending();
            return Err(auth::AuthError::CallbackListenerUnavailable);
        }
    };
    state.finish_desktop(callback)
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
#[tauri::command]
fn start_authentication(
    feature: auth::AuthFeature,
    app: AppHandle<ActiveRuntime>,
    state: State<'_, auth_native::NativeAuthState>,
) -> Result<auth::SessionSnapshot, auth::AuthError> {
    auth_native::feature_supported(feature)?;
    let authorization_url = state.begin_mobile(&app, feature)?;
    if let Err(error) = auth_native::open_mobile_authorization(&app, &authorization_url) {
        state.cancel_pending();
        return Err(error);
    }
    Ok(auth::SessionSnapshot::Authenticating)
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn logout_authentication(
    state: State<'_, auth_native::NativeAuthState>,
) -> Result<auth::SessionSnapshot, auth::AuthError> {
    state.logout()
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn realqa_adjust_capture_selection(
    selection: realqa_capture::SelectionGeometry,
    adjustment: realqa_capture::SelectionAdjustment,
    state: State<'_, realqa_capture::CaptureCore>,
) -> Result<realqa_capture::SelectionGeometry, realqa_capture::CaptureFailure> {
    let result = state.adjust_selection(&selection, adjustment);
    realqa_capture::record_outcome(&result);
    result
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
async fn realqa_begin_capture(
    request: realqa_capture::CaptureRequest,
    state: State<'_, realqa_capture::CaptureCore>,
) -> Result<realqa_capture::CaptureResult, realqa_capture::CaptureFailure> {
    let capture_core = state.inner().clone();
    let result = tauri::async_runtime::spawn_blocking(move || capture_core.begin(request))
        .await
        .map_err(|_| realqa_capture::CaptureFailure::CaptureFailed)
        .and_then(|result| result);
    realqa_capture::record_outcome(&result);
    result
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn realqa_cancel_capture(
    session_id: realqa_capture::CaptureSessionId,
    state: State<'_, realqa_capture::CaptureCore>,
) -> Result<(), realqa_capture::CaptureFailure> {
    let result = state.cancel(&session_id);
    realqa_capture::record_outcome(&result);
    result
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn realqa_composer_accept_image(
    request: realqa_capture::ComposerImageRequest,
    state: State<'_, realqa_capture::ComposerCore>,
) -> Result<realqa_capture::ComposerImage, realqa_capture::CaptureFailure> {
    state.accept_image(request)
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn realqa_composer_remove_image(
    session_id: realqa_capture::ComposerSessionId,
    image_id: realqa_capture::ComposerImageId,
    state: State<'_, realqa_capture::ComposerCore>,
) -> Result<(), realqa_capture::CaptureFailure> {
    state.remove_image(&session_id, &image_id)
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn realqa_composer_reset_session(
    session_id: realqa_capture::ComposerSessionId,
    state: State<'_, realqa_capture::ComposerCore>,
) -> Result<(), realqa_capture::CaptureFailure> {
    state.reset_session(&session_id)
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn configure_builder(builder: tauri::Builder<ActiveRuntime>) -> tauri::Builder<ActiveRuntime> {
    builder
        .invoke_handler(tauri::generate_handler![
            get_runtime_info,
            read_settings,
            write_settings,
            read_shortcut_effective_state,
            write_shortcut_effective_state,
            read_widget_configuration,
            write_widget_configuration,
            export_diagnostics,
            reset_dev_hud,
            hide_hud,
            show_settings,
            hide_settings,
            replace_global_shortcut,
            set_launch_at_login,
            complete_first_run,
            request_update_action,
            get_auth_session,
            start_authentication,
            logout_authentication,
            realqa_inspect_capture_capabilities,
            realqa_list_capture_sources,
            realqa_adjust_capture_selection,
            realqa_begin_capture,
            realqa_cancel_capture,
            realqa_composer_accept_image,
            realqa_composer_remove_image,
            realqa_composer_reset_session
        ])
        .setup(|app| {
            let persistence = match app.path().app_local_data_dir() {
                Ok(directory) => match PersistenceState::new(directory) {
                    Ok(state) => state,
                    Err(error) => {
                        log_persistence_io_failure("initialize", "persistence", &error);
                        PersistenceState::unavailable()
                    }
                },
                Err(_) => {
                    diagnostics::emit_warning(
                        diagnostics::DiagnosticEventId::PersistenceUnavailable,
                        diagnostics::DiagnosticClassification::PersistenceStorageUnavailable,
                    );
                    PersistenceState::unavailable()
                }
            };
            let persisted_record = persistence.read(SETTINGS_STORAGE_KEY);
            let first_run = matches!(&persisted_record, Ok(None));
            let persisted_settings = persisted_record
                .as_ref()
                .ok()
                .and_then(|record| native_settings(record.as_deref()));
            app.manage(persistence);
            app.manage(auth_native::NativeAuthState::initialize());
            app.manage(QuittingState(AtomicBool::new(false)));
            app.manage(updater::UpdateActionBoundary);
            app.manage(realqa_capture::CaptureCore::new(std::sync::Arc::new(
                realqa_capture::PlatformCaptureBackend::current(),
            )));
            app.manage(realqa_capture::ComposerCore::default());

            let autostart = autostart::AutostartState::initialize();
            let launch_at_login = if first_run {
                Ok(false)
            } else {
                persisted_settings
                    .as_ref()
                    .map(|settings| settings.launch_at_login)
                    .ok_or(autostart::AutostartFailure::StorageFailed)
            };
            // The persisted opt-in is authoritative. In particular, a first
            // run actively clears any stale OS entry using the same app id.
            let autostart_outcome = autostart.restore(launch_at_login);
            if let autostart::AutostartOutcome::Unchanged { enabled, reason } = autostart_outcome {
                log_autostart_integration_failure("startup-restore", reason, Some(enabled));
            } else if let autostart::AutostartOutcome::Unknown { reason } = autostart_outcome {
                log_autostart_integration_failure("startup-restore", reason, None);
            }
            app.manage(autostart);

            let persisted_shortcut = persisted_settings.and_then(|settings| settings.shortcut);
            let shortcuts = shortcut::ShortcutState::initialize(persisted_shortcut);
            let shortcut_failure = shortcuts.restoration_failure();
            if let Some(reason) = shortcut_failure {
                log_shortcut_integration_failure(
                    "startup-restore",
                    reason,
                    if shortcuts.active_shortcut().is_some() {
                        "configured"
                    } else {
                        "not-configured"
                    },
                );
            }
            app.manage(Mutex::new(shortcuts));
            app.manage(Mutex::new(StartupDiagnostics {
                shortcut_failure,
                autostart_outcome: matches!(
                    autostart_outcome,
                    autostart::AutostartOutcome::Unchanged { .. }
                        | autostart::AutostartOutcome::Unknown { .. }
                )
                .then_some(autostart_outcome),
            }));

            #[cfg(target_os = "macos")]
            app.set_activation_policy(tauri::ActivationPolicy::Accessory);

            WebviewWindowBuilder::new(app, MAIN_WINDOW_LABEL, WebviewUrl::App("index.html".into()))
                .title("DevHud")
                .inner_size(720.0, 520.0)
                .resizable(false)
                .decorations(false)
                .always_on_top(true)
                .skip_taskbar(true)
                .visible(false)
                .focused(false)
                .devtools(true)
                .incognito(true)
                .disable_drag_drop_handler()
                .on_navigation(is_bundled_url)
                .on_new_window(|_, _| NewWindowResponse::Deny)
                .on_download(|_, _| false)
                .on_web_resource_request(apply_web_resource_policy)
                .build()?;
            create_tray(app.handle())?;
            install_shortcut_handler(app.handle());
            if first_run && build_settings_window(app.handle()).is_err() {
                diagnostics::emit_warning(
                    diagnostics::DiagnosticEventId::DisplayOutcome,
                    diagnostics::DiagnosticClassification::DisplayWindowUnavailable,
                );
            }

            diagnostics::emit(
                diagnostics::DiagnosticEventId::DisplayOutcome,
                diagnostics::DiagnosticClassification::DesktopReady,
            );
            Ok(())
        })
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
fn configure_builder(builder: tauri::Builder<ActiveRuntime>) -> tauri::Builder<ActiveRuntime> {
    builder
        .invoke_handler(tauri::generate_handler![
            get_runtime_info,
            read_settings,
            write_settings,
            read_shortcut_effective_state,
            write_shortcut_effective_state,
            read_widget_configuration,
            write_widget_configuration,
            export_diagnostics,
            reset_dev_hud,
            get_auth_session,
            start_authentication,
            logout_authentication
        ])
        .setup(|app| {
            if let Ok(directory) = app.path().app_log_dir() {
                #[cfg(target_os = "ios")]
                let writer = fs::create_dir_all(&directory)
                    .and_then(|()| exclude_ios_persistence_from_backup(&directory))
                    .and_then(|()| local_log::LocalLogWriter::new_in(directory));
                #[cfg(not(target_os = "ios"))]
                let writer = local_log::LocalLogWriter::new_in(directory);
                if let Ok(writer) = writer {
                    diagnostics::Diagnostics::install(writer);
                }
            }
            let persistence = match app.path().app_local_data_dir() {
                Ok(directory) => match PersistenceState::new(directory) {
                    Ok(state) => state,
                    Err(error) => {
                        log_persistence_io_failure("initialize", "persistence", &error);
                        PersistenceState::unavailable()
                    }
                },
                Err(_) => {
                    diagnostics::emit_warning(
                        diagnostics::DiagnosticEventId::PersistenceUnavailable,
                        diagnostics::DiagnosticClassification::PersistenceStorageUnavailable,
                    );
                    PersistenceState::unavailable()
                }
            };
            app.manage(persistence);
            app.manage(auth_native::NativeAuthState::initialize(app.handle()));
            app.manage(Mutex::new(StartupDiagnostics::default()));
            WebviewWindowBuilder::new(app, MAIN_WINDOW_LABEL, WebviewUrl::App("index.html".into()))
                .title("DevHud")
                .devtools(false)
                .disable_drag_drop_handler()
                .on_navigation(is_bundled_url)
                .on_new_window(|_, _| NewWindowResponse::Deny)
                .on_download(|_, _| false)
                .on_web_resource_request(apply_web_resource_policy)
                .build()?;
            Ok(())
        })
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn platform_builder() -> Result<tauri::Builder<ActiveRuntime>, RuntimeInitializationFailure> {
    let profile =
        cef_profile_directory().map_err(|_| RuntimeInitializationFailure::CefInitialization)?;
    let mut arguments = CEF_PRIVATE_STORAGE_SWITCHES
        .map(|switch| (switch, None::<&str>))
        .to_vec();
    arguments.extend([
        ("--disable-background-networking", None),
        ("--disable-component-update", None),
        ("--disable-domain-reliability", None),
        (
            "host-resolver-rules",
            Some("MAP * ~NOTFOUND, EXCLUDE tauri.localhost"),
        ),
    ]);
    if cfg!(target_os = "windows")
        && std::env::var_os("DEVHUD_SMOKE").is_some_and(|value| value == "1")
    {
        // GitHub-hosted Windows runners have no usable GPU; keep the smoke
        // focused on lifecycle behavior until it runs on GPU-backed hosts.
        arguments.push(("--disable-gpu", None));
    }
    Ok(tauri::Builder::<ActiveRuntime>::new()
        .root_cache_path(profile)
        .command_line_args(arguments))
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
fn platform_builder() -> Result<tauri::Builder<ActiveRuntime>, RuntimeInitializationFailure> {
    Ok(tauri::Builder::<ActiveRuntime>::new()
        .plugin(tauri_plugin_devhud_auth::init())
        .plugin(tauri_plugin_devhud_diagnostics::init())
        .plugin(tauri_plugin_devhud_widget::init()))
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn initialize_logging() {
    if let Ok(writer) = local_log::LocalLogWriter::new(APPLICATION_ID) {
        diagnostics::Diagnostics::install(writer);
    }
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
const fn initialize_logging() {}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn run_app() -> Result<(), RuntimeInitializationFailure> {
    let _instance_guard = match single_instance::InstanceGuard::acquire(APPLICATION_ID) {
        Ok(guard) => guard,
        Err(single_instance::InstanceGuardError::AlreadyRunning) => {
            diagnostics::emit(
                diagnostics::DiagnosticEventId::RuntimeDuplicateInstance,
                diagnostics::DiagnosticClassification::DesktopAlreadyRunning,
            );
            return Ok(());
        }
        Err(single_instance::InstanceGuardError::Unavailable(error)) => {
            // Preserve the source error for internal diagnosis without
            // disclosing filesystem details in the local diagnostics record.
            let _ = error.kind();
            return Err(RuntimeInitializationFailure::InstanceGuardUnavailable);
        }
    };
    let app = configure_builder(platform_builder()?)
        .build(tauri::generate_context!())
        .map_err(|_| RuntimeInitializationFailure::CefInitialization)?;
    app.run(|app, event| match event {
        tauri::RunEvent::WindowEvent {
            label,
            event: WindowEvent::CloseRequested { api, .. },
            ..
        } if label == MAIN_WINDOW_LABEL || label == SETTINGS_WINDOW_LABEL => {
            api.prevent_close();
            if label == MAIN_WINDOW_LABEL {
                if let HudActionOutcome::Unchanged { reason } = hide_hud_internal(app) {
                    log_window_action_failure("window-close-hud", reason);
                }
            } else if let Err(reason) = hide_settings_internal(app) {
                log_window_action_failure("window-close-settings", reason);
            }
        }
        tauri::RunEvent::WindowEvent {
            label,
            event: WindowEvent::Focused(false),
            ..
        } if label == MAIN_WINDOW_LABEL => {
            if let HudActionOutcome::Unchanged { reason } = hide_hud_internal(app) {
                log_window_action_failure("window-focus-loss-hud", reason);
            }
        }
        tauri::RunEvent::ExitRequested { api, .. }
            if !app.state::<QuittingState>().0.load(Ordering::Acquire) =>
        {
            api.prevent_exit();
        }
        _ => {}
    });
    Ok(())
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
fn run_app() -> Result<(), RuntimeInitializationFailure> {
    let app = configure_builder(platform_builder()?)
        .build(tauri::generate_context!())
        .map_err(|_| RuntimeInitializationFailure::SystemWebviewInitialization)?;
    app.run(|app, event| {
        #[cfg(target_os = "ios")]
        if let tauri::RunEvent::Opened { urls } = event {
            for callback in urls {
                if auth::is_mobile_callback_boundary(&callback) {
                    // The callback remains native-only and one-shot. The
                    // foreground provider observes completion through the
                    // narrow get_auth_session command.
                    let _ = app
                        .state::<auth_native::NativeAuthState>()
                        .accept_mobile_callback(callback);
                    break;
                }
            }
        }
    });
    Ok(())
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
pub fn run() {
    #[cfg(all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ))]
    if std::env::args().any(|argument| argument.starts_with("--type=")) {
        // The pinned entry-point macro reaches this same upstream helper through
        // tauri's `cef` feature. Calling the selected runtime directly prevents
        // that feature from being unified into mobile builds.
        tauri_runtime_cef::run_cef_helper_process();
        return;
    }

    initialize_logging();

    if let Err(error) = run_app() {
        diagnostics::emit_fatal(
            diagnostics::DiagnosticEventId::RuntimeInitializationFailure,
            error.diagnostic_classification(),
        );
        std::process::exit(70);
    }
}

#[cfg(all(feature = "mobile-system-webview", target_os = "ios"))]
#[unsafe(no_mangle)]
#[inline(never)]
pub extern "C" fn start_app() {
    run();
}

#[cfg(all(feature = "mobile-system-webview", target_os = "android"))]
mod android_entry {
    fn start_app_inner() {
        use tauri_runtime_wry::wry::{
            android_setup,
            prelude::{JClass, JNIEnv, JString},
        };

        // This is the pinned upstream mobile entry binding with its WRY path
        // made explicit. Remove it when Tauri's macro accepts an explicit
        // runtime without requiring the `tauri/wry` feature.
        tauri_runtime_wry::wry::android_binding!(dev_deli, devhud, tauri_runtime_wry::wry);
        tauri_runtime_wry::tao::android_binding!(
            dev_deli,
            devhud,
            Rust,
            android_setup,
            start_app_inner,
            tauri_runtime_wry::tao
        );
        tauri_runtime_wry::tao::platform::android::prelude::android_fn!(
            app_tauri,
            plugin,
            PluginManager,
            handlePluginResponse,
            [i32, JString, JString],
        );
        tauri_runtime_wry::tao::platform::android::prelude::android_fn!(
            app_tauri,
            plugin,
            PluginManager,
            sendChannelData,
            [i64, JString],
        );

        #[allow(non_snake_case)]
        fn handlePluginResponse(
            mut environment: JNIEnv,
            _: JClass,
            id: i32,
            success: JString,
            error: JString,
        ) {
            tauri::plugin::mobile::handle_android_plugin_response(
                &mut environment,
                id,
                success,
                error,
            );
        }

        #[allow(non_snake_case)]
        fn sendChannelData(mut environment: JNIEnv, _: JClass, id: i64, data: JString) {
            tauri::plugin::mobile::send_channel_data(&mut environment, id, data);
        }

        super::run();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn permits_only_bundled_application_origins() {
        let (allowed, inactive_runtime_origin) =
            if cfg!(all(feature = "mobile-system-webview", target_os = "ios")) {
                (
                    "tauri://localhost/index.html",
                    "http://tauri.localhost/index.html",
                )
            } else {
                (
                    "http://tauri.localhost/index.html",
                    "tauri://localhost/index.html",
                )
            };
        assert!(is_bundled_url(&allowed.parse().unwrap()), "{allowed}");

        for denied in [
            inactive_runtime_origin,
            "https://example.com/",
            "http://localhost:4173/",
            "https://tauri.localhost/index.html",
            "https://tauri.localhost:8080/index.html",
            "http://attacker@tauri.localhost/index.html",
            "http://tauri.localhost@attacker.example/index.html",
            "file:///tmp/index.html",
            "data:text/html,devhud",
            "about:blank",
        ] {
            assert!(!is_bundled_url(&denied.parse().unwrap()), "{denied}");
        }
    }

    #[test]
    fn normal_views_and_devtools_deny_malicious_navigation_and_remote_resources() {
        for attack_surface in [
            "hud-view",
            "settings-view",
            "hud-devtools",
            "settings-devtools",
        ] {
            for denied in [
                "https://attacker.example/remote.js",
                "http://localhost:4173/remote.js",
                "file:///private/user/file",
                "data:text/javascript,fetch('https://attacker.example')",
                "ftp://attacker.example/payload",
                "not a URI",
            ] {
                assert_eq!(
                    web_resource_decision(denied),
                    WebResourceDecision::Deny,
                    "{attack_surface} accepted {denied}"
                );
            }
            for denied in [
                "https://attacker.example/navigation",
                "http://localhost:4173/navigation",
                "file:///private/user/file",
                "data:text/html,malicious",
                "about:blank",
            ] {
                assert!(
                    !is_bundled_url(&denied.parse().unwrap()),
                    "{attack_surface} accepted navigation {denied}"
                );
            }
        }

        for allowed in [
            "http://tauri.localhost/index.html",
            "http://tauri.localhost/static/app.js",
        ] {
            assert_eq!(
                web_resource_decision(allowed),
                WebResourceDecision::AllowBundledAsset,
                "{allowed}"
            );
        }
        for allowed in ["ipc://localhost", "http://ipc.localhost"] {
            assert_eq!(
                web_resource_decision(allowed),
                WebResourceDecision::AllowIpc,
                "{allowed}"
            );
        }
    }

    #[test]
    fn malicious_remote_resource_responses_are_replaced_with_empty_denials() {
        let request = Request::builder()
            .uri("https://attacker.example/remote.js")
            .body(Vec::new())
            .unwrap();
        let mut response = Response::builder()
            .status(StatusCode::OK)
            .body(Cow::<[u8]>::Borrowed(b"malicious response"))
            .unwrap();

        apply_web_resource_policy(request, &mut response);

        assert_eq!(response.status(), StatusCode::FORBIDDEN);
        assert!(response.body().is_empty());
        assert_eq!(response.headers()["cache-control"], "no-store");
        assert_eq!(
            response.headers()["cross-origin-resource-policy"],
            "same-origin"
        );
        assert_eq!(response.headers()["referrer-policy"], "no-referrer");
        assert_eq!(response.headers()["x-content-type-options"], "nosniff");
    }

    #[test]
    fn local_ipc_responses_keep_internal_transport_headers_and_body() {
        let request = Request::builder()
            .uri("http://ipc.localhost")
            .body(Vec::new())
            .unwrap();
        let mut response = Response::builder()
            .status(StatusCode::OK)
            .header("access-control-allow-origin", "http://tauri.localhost")
            .body(Cow::<[u8]>::Borrowed(b"{\"ok\":true}"))
            .unwrap();

        apply_web_resource_policy(request, &mut response);

        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(response.body().as_ref(), b"{\"ok\":true}");
        assert_eq!(
            response.headers()["access-control-allow-origin"],
            "http://tauri.localhost"
        );
        assert_eq!(response.headers()["cache-control"], "no-store");
        assert!(
            !response
                .headers()
                .contains_key("cross-origin-resource-policy")
        );
    }

    #[test]
    fn runtime_info_uses_stable_application_id_and_desktop_contract() {
        let runtime_info = RuntimeInfo {
            application_id: APPLICATION_ID,
            bundled_origin: "http://tauri.localhost".to_string(),
            operating_system: "linux",
            runtime: "cef",
            sandbox_enabled: true,
            surface: RuntimeSurface::Hud,
            first_run: false,
            shortcut_startup_failure: Some(shortcut::ShortcutFailure::Conflict),
            autostart_startup_outcome: Some(autostart::AutostartOutcome::Unchanged {
                enabled: false,
                reason: autostart::AutostartFailure::PermissionDenied,
            }),
            update_policy: "Desktop updater unavailable",
        };
        let value = serde_json::to_value(runtime_info).unwrap();

        assert_eq!(value["applicationId"], APPLICATION_ID);
        assert_eq!(value["runtime"], "cef");
        assert_eq!(value["sandboxEnabled"], true);
        assert_eq!(value["surface"], "hud");
        assert_eq!(value["shortcutStartupFailure"], "conflict");
        assert_eq!(
            value["autostartStartupOutcome"],
            serde_json::json!({
                "status": "unchanged",
                "enabled": false,
                "reason": "permission-denied"
            })
        );
        assert_eq!(
            serde_json::to_value(RuntimeSurface::Settings).unwrap(),
            "settings"
        );
        assert_eq!(
            serde_json::to_value(RuntimeSurface::Mobile).unwrap(),
            "mobile"
        );
        assert_eq!(value["operatingSystem"], "linux");
        assert_eq!(value["updatePolicy"], "Desktop updater unavailable");
        assert_eq!(update_policy(), "Desktop updater unavailable");
    }

    #[test]
    fn media_permissions_are_explicitly_disabled() {
        for directive in ["camera=()", "microphone=()", "display-capture=()"] {
            assert!(PERMISSIONS_POLICY.contains(directive));
        }
    }

    #[test]
    fn stable_storage_keys_and_current_schema_validation_are_preserved() {
        assert_eq!(SETTINGS_STORAGE_KEY, "devhud.settings.v1");
        assert_eq!(
            WIDGET_CONFIGURATION_STORAGE_KEY,
            "devhud.widget-configuration.v1"
        );
        assert!(validate_current_record(
            SETTINGS_STORAGE_KEY,
            r#"{"version":1,"settings":{"theme":"system","launchAtLogin":false,"shortcut":null}}"#
        )
        .is_some());
        assert!(validate_current_record(SETTINGS_STORAGE_KEY, r#"{"version":2}"#).is_none());
        assert!(validate_current_record(SETTINGS_STORAGE_KEY, "not-json").is_none());
        assert!(validate_current_record(
            SETTINGS_STORAGE_KEY,
            r#"{"version":1,"settings":{"theme":"dark","launchAtLogin":false,"shortcut":{"modifiers":["control","control"],"key":"k"}}}"#
        )
        .is_none());
        assert!(validate_current_record(
            WIDGET_CONFIGURATION_STORAGE_KEY,
            r#"{"version":1,"configuration":{"slots":[{"slot":"primary","toolId":"fixture-diagnostics"}]}}"#
        )
        .is_some());
        assert!(validate_current_record(
            SETTINGS_STORAGE_KEY,
            r#"{"version":1,"settings":{"theme":"system","launchAtLogin":false,"shortcut":null,"extra":true}}"#
        )
        .is_none());
        assert!(
            validate_current_record(
                WIDGET_CONFIGURATION_STORAGE_KEY,
                r#"{"version":1,"configuration":{"slots":[]},"extra":true}"#
            )
            .is_none()
        );
    }

    #[test]
    fn cef_uses_an_exact_private_profile_with_persistent_web_storage_disabled() {
        let cache_base = PathBuf::from("/application-cache");
        assert_eq!(
            cef_profile_directory_from(&cache_base),
            cache_base.join(APPLICATION_ID).join("cef")
        );
        for required in [
            "--disable-application-cache",
            "--disable-databases",
            "--disable-local-storage",
            "--disable-session-storage",
            "--incognito",
        ] {
            assert!(CEF_PRIVATE_STORAGE_SWITCHES.contains(&required));
        }
    }

    #[test]
    fn cef_reset_removes_every_exact_profile_artifact_and_is_idempotent() {
        let directory = std::env::temp_dir().join(format!(
            "devhud-cef-reset-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        let cache_base = directory.join("cache");
        let profile = cef_profile_directory_from(&cache_base);
        let exported = directory.join("DevHud-diagnostics.jsonl");
        let unrelated = cache_base.join("another-application").join("Cookies");
        for artifact in [
            profile.join("Default").join("History"),
            profile.join("Default").join("Cookies"),
            profile.join("Default").join("Local Storage").join("state"),
            profile.join("runtime").join("GPUCache").join("entry"),
        ] {
            fs::create_dir_all(artifact.parent().unwrap()).unwrap();
            fs::write(artifact, b"retained-cef-artifact").unwrap();
        }
        fs::create_dir_all(unrelated.parent().unwrap()).unwrap();
        fs::write(&unrelated, b"unrelated").unwrap();
        fs::write(&exported, b"user-owned-export").unwrap();

        assert_eq!(reset_cef_profile_directory(&cache_base, &profile), Ok(()));
        assert_eq!(fs::read_dir(&profile).unwrap().count(), 0);
        assert_eq!(fs::read(&unrelated).unwrap(), b"unrelated");
        assert_eq!(fs::read(&exported).unwrap(), b"user-owned-export");

        assert_eq!(reset_cef_profile_directory(&cache_base, &profile), Ok(()));
        assert_eq!(fs::read_dir(&profile).unwrap().count(), 0);
        assert_eq!(fs::read(&unrelated).unwrap(), b"unrelated");
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn cef_reset_rejects_a_broad_symbolic_or_non_directory_target_before_deletion() {
        let directory = std::env::temp_dir().join(format!(
            "devhud-cef-boundary-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        let cache_base = directory.join("cache");
        let valid_data = cache_base.join("keep.txt");
        fs::create_dir_all(&cache_base).unwrap();
        fs::write(&valid_data, b"keep").unwrap();

        assert!(preflight_cef_profile_reset(&cache_base, &cache_base).is_err());
        assert_eq!(fs::read(&valid_data).unwrap(), b"keep");

        let profile = cef_profile_directory_from(&cache_base);
        fs::create_dir_all(profile.parent().unwrap()).unwrap();
        fs::write(&profile, b"invalid-profile-file").unwrap();
        assert!(preflight_cef_profile_reset(&cache_base, &profile).is_err());
        assert_eq!(fs::read(&profile).unwrap(), b"invalid-profile-file");
        fs::remove_file(&profile).unwrap();

        #[cfg(unix)]
        {
            use std::os::unix::fs::symlink;

            let external = directory.join("external");
            fs::create_dir_all(&external).unwrap();
            fs::write(external.join("keep.txt"), b"external").unwrap();
            symlink(&external, &profile).unwrap();

            assert!(preflight_cef_profile_reset(&cache_base, &profile).is_err());
            assert_eq!(fs::read(external.join("keep.txt")).unwrap(), b"external");
        }
        fs::remove_dir_all(directory).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn record_reset_preflight_rejects_a_symbolic_directory_without_deletion() {
        use std::os::unix::fs::symlink;

        let directory = std::env::temp_dir().join(format!(
            "devhud-record-boundary-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        let user_directory = directory.join("user-owned");
        let linked_directory = directory.join("devhud-records");
        fs::create_dir_all(&user_directory).unwrap();
        let user_record = user_directory.join(SETTINGS_STORAGE_KEY);
        fs::write(&user_record, b"user-owned").unwrap();
        symlink(&user_directory, &linked_directory).unwrap();
        let paths = [
            (
                SETTINGS_STORAGE_KEY,
                linked_directory.join(SETTINGS_STORAGE_KEY),
            ),
            (
                WIDGET_CONFIGURATION_STORAGE_KEY,
                linked_directory.join(WIDGET_CONFIGURATION_STORAGE_KEY),
            ),
        ];

        assert!(pending_reset_stage_paths(&linked_directory, &paths).is_err());
        assert_eq!(fs::read(&user_record).unwrap(), b"user-owned");
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn record_reset_preflight_rejects_non_file_records_and_transaction_stages() {
        let directory = std::env::temp_dir().join(format!(
            "devhud-record-type-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        fs::create_dir_all(&directory).unwrap();
        let settings_path = directory.join(SETTINGS_STORAGE_KEY);
        let widget_path = directory.join(WIDGET_CONFIGURATION_STORAGE_KEY);
        let paths = [
            (SETTINGS_STORAGE_KEY, settings_path.clone()),
            (WIDGET_CONFIGURATION_STORAGE_KEY, widget_path),
        ];

        fs::create_dir(&settings_path).unwrap();
        fs::write(settings_path.join("retained"), b"stable-directory").unwrap();
        assert!(pending_reset_stage_paths(&directory, &paths).is_err());
        assert_eq!(
            fs::read(settings_path.join("retained")).unwrap(),
            b"stable-directory"
        );

        fs::remove_dir_all(&settings_path).unwrap();
        let staged_path = settings_path.with_extension("reset-123-456");
        fs::create_dir(&staged_path).unwrap();
        fs::write(staged_path.join("retained"), b"staged-directory").unwrap();
        assert!(pending_reset_stage_paths(&directory, &paths).is_err());
        assert_eq!(
            fs::read(staged_path.join("retained")).unwrap(),
            b"staged-directory"
        );
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn frontend_settings_writes_preserve_native_integration_fields() {
        let current = r#"{"version":1,"settings":{"theme":"system","launchAtLogin":true,"shortcut":{"modifiers":["control"],"key":"k"}}}"#;
        let stale_frontend =
            r#"{"version":1,"settings":{"theme":"dark","launchAtLogin":false,"shortcut":null}}"#;
        let merged = merge_frontend_settings_record(Some(current), stale_frontend).unwrap();
        let merged: serde_json::Value = serde_json::from_str(&merged).unwrap();

        assert_eq!(merged["settings"]["theme"], "dark");
        assert_eq!(merged["settings"]["launchAtLogin"], true);
        assert_eq!(
            merged["settings"]["shortcut"],
            serde_json::json!({
                "modifiers": ["control"],
                "key": "k"
            })
        );
    }

    #[test]
    fn failed_record_reset_restores_previously_deleted_records() {
        let directory = std::env::temp_dir().join(format!(
            "devhud-reset-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        fs::create_dir_all(&directory).unwrap();
        let settings_path = directory.join(SETTINGS_STORAGE_KEY);
        let widget_path = directory.join(WIDGET_CONFIGURATION_STORAGE_KEY);
        let settings =
            br#"{"version":1,"settings":{"theme":"system","launchAtLogin":false,"shortcut":null}}"#;
        let widgets = br#"{"version":1,"configuration":{"slots":[]}}"#;
        fs::write(&settings_path, settings).unwrap();
        fs::write(&widget_path, widgets).unwrap();
        let paths = [
            (
                SETTINGS_STORAGE_KEY,
                settings_path.clone(),
                directory.join("settings-staged"),
            ),
            (
                WIDGET_CONFIGURATION_STORAGE_KEY,
                widget_path.clone(),
                directory.join("widgets-staged"),
            ),
        ];

        let result = reset_persisted_records(
            &paths,
            &[],
            |source, destination| {
                if source == widget_path {
                    Err(io::Error::new(
                        io::ErrorKind::PermissionDenied,
                        "injected reset failure",
                    ))
                } else {
                    fs::rename(source, destination)
                }
            },
            |path| fs::remove_file(path),
        );

        assert_eq!(
            result,
            Err(PersistenceResetFailure::BeforeRecordsRemoved(
                PersistenceCommandError::ResetFailed
            ))
        );
        assert_eq!(fs::read(settings_path).unwrap(), settings);
        assert_eq!(fs::read(widget_path).unwrap(), widgets);
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn failed_staging_rollback_reports_an_incomplete_reset() {
        let directory = std::env::temp_dir().join(format!(
            "devhud-reset-rollback-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        fs::create_dir_all(&directory).unwrap();
        let settings_path = directory.join(SETTINGS_STORAGE_KEY);
        let widget_path = directory.join(WIDGET_CONFIGURATION_STORAGE_KEY);
        let settings_staged = directory.join("settings-staged");
        fs::write(&settings_path, b"settings").unwrap();
        fs::write(&widget_path, b"widgets").unwrap();
        let paths = [
            (
                SETTINGS_STORAGE_KEY,
                settings_path.clone(),
                settings_staged.clone(),
            ),
            (
                WIDGET_CONFIGURATION_STORAGE_KEY,
                widget_path.clone(),
                directory.join("widgets-staged"),
            ),
        ];

        let result = reset_persisted_records(
            &paths,
            &[],
            |source, destination| {
                if source == widget_path || source == settings_staged {
                    Err(io::Error::new(
                        io::ErrorKind::PermissionDenied,
                        "injected staging or rollback failure",
                    ))
                } else {
                    fs::rename(source, destination)
                }
            },
            |path| fs::remove_file(path),
        );

        assert_eq!(result, Err(PersistenceResetFailure::PartiallyRetained));
        assert!(!settings_path.exists());
        assert!(settings_staged.exists());
        assert!(widget_path.exists());
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn failed_staged_record_cleanup_reports_an_incomplete_reset() {
        let directory = std::env::temp_dir().join(format!(
            "devhud-reset-cleanup-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        fs::create_dir_all(&directory).unwrap();
        let settings_path = directory.join(SETTINGS_STORAGE_KEY);
        let staged_path = directory.join("settings-staged");
        fs::write(&settings_path, b"settings").unwrap();
        let paths = [(SETTINGS_STORAGE_KEY, settings_path, staged_path.clone())];

        let result = reset_persisted_records(
            &paths,
            &[],
            |source, destination| fs::rename(source, destination),
            |_| {
                Err(io::Error::new(
                    io::ErrorKind::PermissionDenied,
                    "injected cleanup failure",
                ))
            },
        );

        assert_eq!(result, Err(PersistenceResetFailure::CleanupFailed));
        assert!(staged_path.exists());
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn reset_retry_removes_retained_transaction_stages() {
        let directory = std::env::temp_dir().join(format!(
            "devhud-reset-retry-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        fs::create_dir_all(&directory).unwrap();
        let settings_path = directory.join(SETTINGS_STORAGE_KEY);
        let widget_path = directory.join(WIDGET_CONFIGURATION_STORAGE_KEY);
        let paths = [
            (SETTINGS_STORAGE_KEY, settings_path.clone()),
            (WIDGET_CONFIGURATION_STORAGE_KEY, widget_path.clone()),
        ];
        let staged_path = settings_path.with_extension("reset-123-456");
        fs::write(&staged_path, b"settings").unwrap();
        let previously_staged = pending_reset_stage_paths(&directory, &paths).unwrap();
        let staged_paths = [
            (
                SETTINGS_STORAGE_KEY,
                settings_path,
                directory.join("settings-staged"),
            ),
            (
                WIDGET_CONFIGURATION_STORAGE_KEY,
                widget_path,
                directory.join("widgets-staged"),
            ),
        ];

        assert_eq!(
            reset_persisted_records(
                &staged_paths,
                &previously_staged,
                |source, destination| fs::rename(source, destination),
                |path| fs::remove_file(path),
            ),
            Ok(())
        );
        assert!(!staged_path.exists());
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn runtime_and_partial_reset_classifications_remain_distinct() {
        assert_eq!(
            RuntimeInitializationFailure::InstanceGuardUnavailable
                .diagnostic_classification()
                .as_str(),
            "desktop-instance-guard-unavailable"
        );
        assert_eq!(
            RuntimeInitializationFailure::CefInitialization
                .diagnostic_classification()
                .as_str(),
            "desktop-cef-initialization-failed"
        );
        assert_eq!(
            RuntimeInitializationFailure::SystemWebviewInitialization
                .diagnostic_classification()
                .as_str(),
            "mobile-system-webview-initialization-failed"
        );
        assert_eq!(
            diagnostics::DiagnosticClassification::PersistenceResetPreflightFailed.as_str(),
            "persistence-reset-preflight-failed"
        );
        assert_eq!(
            reset_preflight_failure(PersistenceCommandError::StorageUnavailable),
            PersistenceCommandError::StorageUnavailable
        );
        assert_eq!(
            serde_json::to_value(PersistenceResetOutcome::Complete).unwrap(),
            serde_json::json!({ "status": "complete" })
        );
        assert_eq!(
            serde_json::to_value(PersistenceResetOutcome::PartiallyRetained).unwrap(),
            serde_json::json!({ "status": "partially-retained" })
        );
        assert_eq!(
            serde_json::to_value(PersistenceResetOutcome::CleanupFailed).unwrap(),
            serde_json::json!({ "status": "cleanup-failed" })
        );
        assert_eq!(
            serde_json::to_value(PersistenceResetOutcome::IntegrationRollbackFailed {
                shortcut: None,
                launch_at_login: Some(true),
            })
            .unwrap(),
            serde_json::json!({
                "status": "integration-rollback-failed",
                "shortcut": null,
                "launchAtLogin": true,
            })
        );
    }

    #[test]
    fn reset_clears_managed_logs_without_an_active_sink() {
        let directory = std::env::temp_dir().join(format!(
            "devhud-reset-log-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        fs::create_dir_all(&directory).unwrap();
        let managed_log = directory.join("devhud-1-1-1.jsonl");
        let user_export = directory.join("DevHud-diagnostics.jsonl");
        fs::write(&managed_log, b"retained-log").unwrap();
        fs::write(&user_export, b"user-export").unwrap();

        preflight_local_logs_for_reset(&directory).unwrap();
        clear_local_logs_for_reset(&directory).unwrap();

        assert!(!managed_log.exists());
        assert_eq!(fs::read(&user_export).unwrap(), b"user-export");
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn cancelled_diagnostics_export_never_opens_or_mutates_a_destination() {
        let writes = std::cell::Cell::new(0_u8);
        let outcome = export_selected_destination(
            || None::<PathBuf>,
            |_| {
                writes.set(writes.get() + 1);
                Ok(())
            },
        );

        assert_eq!(outcome, Ok(DiagnosticsExportOutcome::Cancelled));
        assert_eq!(writes.get(), 0);
    }

    #[test]
    fn diagnostics_export_errors_are_stable_and_disclose_no_path() {
        let outcome = export_selected_destination(
            || Some(PathBuf::from("/private/adversarial/user-file")),
            |_| Err(DiagnosticsExportError::WriteFailed),
        );

        assert_eq!(outcome, Err(DiagnosticsExportError::WriteFailed));
        let serialized = serde_json::to_string(&outcome.unwrap_err()).unwrap();
        assert_eq!(serialized, "\"write-failed\"");
        assert!(!serialized.contains("private"));
        assert!(!serialized.contains('/'));
    }

    #[test]
    fn diagnostics_export_rejects_cef_profile_and_transaction_stage_destinations() {
        let directory = std::env::temp_dir().join(format!(
            "devhud-export-boundary-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        let cache_base = directory.join("cache");
        let owned_root = cache_base.join(APPLICATION_ID);
        let profile_directory = owned_root.join(CEF_PROFILE_DIRECTORY).join("exports");
        let staged_directory = owned_root.join("cef.reset-123-456").join("exports");
        let unrelated_directory = directory.join("user-owned");
        for path in [&profile_directory, &staged_directory, &unrelated_directory] {
            fs::create_dir_all(path).unwrap();
        }

        assert!(
            destination_is_cef_reset_owned(
                &cache_base,
                &profile_directory.join("DevHud-diagnostics.jsonl")
            )
            .unwrap()
        );
        assert!(
            destination_is_cef_reset_owned(
                &cache_base,
                &staged_directory.join("DevHud-diagnostics.jsonl")
            )
            .unwrap()
        );
        assert!(
            !destination_is_cef_reset_owned(
                &cache_base,
                &unrelated_directory.join("DevHud-diagnostics.jsonl")
            )
            .unwrap()
        );

        #[cfg(unix)]
        {
            use std::os::unix::fs::symlink;

            let alias = directory.join("profile-alias");
            symlink(&profile_directory, &alias).unwrap();
            assert!(
                destination_is_cef_reset_owned(
                    &cache_base,
                    &alias.join("DevHud-diagnostics.jsonl")
                )
                .unwrap()
            );
        }
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn failed_atomic_export_preserves_the_previous_destination() {
        let directory = std::env::temp_dir().join(format!(
            "devhud-export-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        fs::create_dir_all(&directory).unwrap();
        let destination = directory.join("DevHud-diagnostics.jsonl");
        fs::write(&destination, b"previous-export").unwrap();

        let result = write_export_atomically_with(&destination, |temporary_file| {
            temporary_file.write_all(b"incomplete-export")?;
            Err(io::Error::other("injected export failure"))
        });

        assert!(result.is_err());
        assert_eq!(fs::read(&destination).unwrap(), b"previous-export");
        assert_eq!(fs::read_dir(&directory).unwrap().count(), 1);

        write_export_atomically(&destination, b"complete-export").unwrap();
        assert_eq!(fs::read(&destination).unwrap(), b"complete-export");
        assert_eq!(fs::read_dir(&directory).unwrap().count(), 1);
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn tray_has_exactly_the_five_product_actions_in_order() {
        assert_eq!(
            TRAY_ACTIONS.map(|(_, title)| title),
            [
                "Open DevHud",
                "Settings",
                "Check for Updates",
                "Open DevTools",
                "Quit"
            ]
        );
    }

    #[test]
    fn hud_centers_on_the_display_containing_the_pointer() {
        let displays = [
            DisplayArea {
                bounds_x: -1920,
                bounds_y: 0,
                bounds_width: 1920,
                bounds_height: 1080,
                work_x: -1920,
                work_y: 0,
                work_width: 1920,
                work_height: 1080,
            },
            DisplayArea {
                bounds_x: 0,
                bounds_y: 0,
                bounds_width: 2560,
                bounds_height: 1440,
                work_x: 0,
                work_y: 0,
                work_width: 2560,
                work_height: 1440,
            },
        ];
        assert_eq!(
            centered_hud_position((-100.0, 400.0), &displays, (720, 520)),
            Some((-1320, 280))
        );
        assert_eq!(
            centered_hud_position((1200.0, 400.0), &displays, (720, 520)),
            Some((920, 460))
        );
        assert_eq!(
            centered_hud_position((5000.0, 400.0), &displays, (720, 520)),
            None
        );
    }
}
