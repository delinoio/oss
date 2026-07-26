#[cfg(any(feature = "desktop-cef", test))]
mod autostart;
#[cfg(any(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    test
))]
mod local_log;
#[cfg(any(feature = "desktop-cef", test))]
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

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
use std::sync::OnceLock;
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
use std::sync::{
    Mutex,
    atomic::{AtomicBool, Ordering},
};
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
use std::time::Duration;
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
use std::{
    fs, io,
    path::{Path, PathBuf},
};
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
use std::{fs::File, io::Write};

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
use tauri::{
    AppHandle, Manager, State, Webview, WebviewUrl,
    http::{HeaderName, HeaderValue},
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
use tauri_plugin_devhud_widget::{
    DevHudWidgetBridgeExt, Error as WidgetBridgeError, WidgetBridgeErrorCode,
};

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
const WIDGET_CONFIGURATION_STORAGE_KEY: &str = "devhud.widget-configuration.v1";
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
const PERMISSIONS_POLICY: &str =
    "camera=(), display-capture=(), geolocation=(), microphone=(), usb=()";
#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
static LOCAL_LOG_WRITER: OnceLock<local_log::LocalLogWriter> = OnceLock::new();

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
    const fn classification(self) -> &'static str {
        match self {
            #[cfg(any(
                all(
                    feature = "desktop-cef",
                    not(any(target_os = "android", target_os = "ios"))
                ),
                test
            ))]
            Self::InstanceGuardUnavailable => "instance-guard-unavailable",
            #[cfg(any(
                all(
                    feature = "desktop-cef",
                    not(any(target_os = "android", target_os = "ios"))
                ),
                test
            ))]
            Self::CefInitialization => "cef-initialization",
            #[cfg(any(
                all(
                    feature = "mobile-system-webview",
                    any(target_os = "android", target_os = "ios")
                ),
                test
            ))]
            Self::SystemWebviewInitialization => "system-webview-initialization",
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
            tracing::warn!(
                event = "devhud.persistence.unavailable",
                operation = "write",
                record = persistence_record_name(key),
                classification = "storage-unavailable",
                "DevHud persistence is unavailable"
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
            tracing::warn!(
                event = "devhud.persistence.reset_failure",
                classification = "reset-failed",
                "DevHud local data reset failed"
            );
            PersistenceResetFailure::BeforeRecordsRemoved(PersistenceCommandError::ResetFailed)
        })?;
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
        ];
        let directory =
            self.directory
                .as_deref()
                .ok_or(PersistenceResetFailure::BeforeRecordsRemoved(
                    PersistenceCommandError::StorageUnavailable,
                ))?;
        let previously_staged = pending_reset_stage_paths(directory, &paths).map_err(|error| {
            tracing::warn!(
                event = "devhud.persistence.reset_failure",
                operation = "reset-scan",
                error_kind = ?error.kind(),
                classification = "reset-failed",
                "DevHud could not inspect retained reset staging records"
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
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn pending_reset_stage_paths<'a>(
    directory: &Path,
    paths: &[(&'a str, PathBuf)],
) -> io::Result<Vec<(&'a str, PathBuf)>> {
    let mut staged = Vec::new();
    for entry in fs::read_dir(directory)? {
        let candidate = entry?.path();
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
    for (key, staged_path) in previously_staged {
        if let Err(error) = remove_staged_record(staged_path)
            && error.kind() != io::ErrorKind::NotFound
        {
            cleanup_failed = true;
            tracing::warn!(
                event = "devhud.persistence.reset_cleanup_failure",
                operation = "reset-cleanup",
                record = persistence_record_name(key),
                error_kind = ?error.kind(),
                classification = "cleanup-failed",
                "DevHud retained reset staging cleanup failed"
            );
        }
    }
    for staged_index in staged {
        let (key, _, staged_path) = &paths[staged_index];
        if let Err(error) = remove_staged_record(staged_path)
            && error.kind() != io::ErrorKind::NotFound
        {
            cleanup_failed = true;
            tracing::warn!(
                event = "devhud.persistence.reset_cleanup_failure",
                operation = "reset-cleanup",
                record = persistence_record_name(key),
                error_kind = ?error.kind(),
                classification = "cleanup-failed",
                "DevHud reset staging cleanup failed"
            );
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
fn persistence_record_name(key: &str) -> &'static str {
    match key {
        SETTINGS_STORAGE_KEY => "settings",
        WIDGET_CONFIGURATION_STORAGE_KEY => "widget-configuration",
        "persistence" => "persistence",
        _ => "unknown",
    }
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn log_persistence_io_failure(operation: &'static str, key: &str, error: &io::Error) {
    tracing::warn!(
        event = "devhud.persistence.io_failure",
        operation,
        record = persistence_record_name(key),
        error_kind = ?error.kind(),
        classification = "storage-unavailable",
        "DevHud persistence I/O failed"
    );
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn log_persistence_unavailable(operation: &'static str, key: &str) {
    tracing::warn!(
        event = "devhud.persistence.unavailable",
        operation,
        record = persistence_record_name(key),
        classification = "storage-unavailable",
        "DevHud persistence is unavailable"
    );
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
fn widget_bridge_failure(
    operation: &'static str,
    error: &WidgetBridgeError,
) -> PersistenceCommandError {
    let failure = match error.code() {
        Some(WidgetBridgeErrorCode::Corrupt) => PersistenceCommandError::Corrupt,
        Some(WidgetBridgeErrorCode::FutureVersion) => PersistenceCommandError::FutureVersion,
        Some(WidgetBridgeErrorCode::Incompatible) => PersistenceCommandError::Incompatible,
        _ => PersistenceCommandError::WidgetBridgeFailed,
    };
    tracing::warn!(
        event = "devhud.widget_bridge.failure",
        operation,
        classification = ?error.code(),
        "DevHud native widget bridge operation failed"
    );
    failure
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn validate_current_record(key: &str, record: &str) -> Option<()> {
    let value: serde_json::Value = serde_json::from_str(record).ok()?;
    let object = value.as_object()?;
    (object.get("version")?.as_u64() == Some(1)).then_some(())?;
    match key {
        SETTINGS_STORAGE_KEY => validate_settings_record(object),
        WIDGET_CONFIGURATION_STORAGE_KEY => validate_widget_configuration_record(object),
        _ => None,
    }
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

#[cfg(all(
    any(feature = "desktop-cef", feature = "mobile-system-webview"),
    not(target_os = "windows")
))]
fn replace_file(temporary_path: &std::path::Path, path: &std::path::Path) -> io::Result<()> {
    fs::rename(temporary_path, path)
}

#[cfg(all(
    any(feature = "desktop-cef", feature = "mobile-system-webview"),
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

    url.port().is_none() && url.scheme() == scheme && url.host_str() == Some(host)
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

    tracing::info!(
        event = "devhud.runtime.ready",
        runtime = runtime_name(),
        "DevHud runtime is ready"
    );

    #[cfg(debug_assertions)]
    if std::env::var_os("DEVHUD_SMOKE").is_some_and(|value| value == "1") {
        let app = _app.clone();
        std::thread::spawn(move || {
            std::thread::sleep(Duration::from_secs(1));
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
impl HudActionFailure {
    const fn classification(self) -> &'static str {
        match self {
            Self::UnsupportedDisplay => "unsupported-display",
            Self::WindowUnavailable => "window-unavailable",
            Self::PositionFailed => "position-failed",
        }
    }
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
fn show_hud(app: AppHandle<ActiveRuntime>) -> HudActionOutcome {
    show_hud_internal(&app, false)
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
    .on_web_resource_request(|_, response| {
        response.headers_mut().insert(
            HeaderName::from_static("permissions-policy"),
            HeaderValue::from_static(PERMISSIONS_POLICY),
        );
    })
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
    operation: &'static str,
    reason: shortcut::ShortcutFailure,
    effective_state: &'static str,
) {
    tracing::warn!(
        event = "devhud.shortcut.integration_failure",
        operation,
        classification = reason.classification(),
        effective_state,
        "DevHud shortcut integration failed"
    );
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn log_autostart_integration_failure(
    operation: &'static str,
    reason: autostart::AutostartFailure,
    effective_enabled: Option<bool>,
) {
    let effective_state = match effective_enabled {
        Some(true) => "enabled",
        Some(false) => "disabled",
        None => "unknown",
    };
    tracing::warn!(
        event = "devhud.autostart.integration_failure",
        operation,
        classification = reason.classification(),
        effective_state,
        "DevHud launch-at-login integration failed"
    );
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn log_window_action_failure(operation: &'static str, reason: HudActionFailure) {
    tracing::warn!(
        event = "devhud.window.action_failure",
        operation,
        classification = reason.classification(),
        "DevHud window action failed"
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
                let outcome = app.state::<updater::UpdateActionBoundary>().request();
                tracing::info!(
                    event = "devhud.update.action",
                    classification = ?outcome,
                    "DevHud update action completed"
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
    tracing::warn!(
        event = "devhud.persistence.reset_failure",
        classification = "reset-failed",
        "DevHud application browsing data reset failed"
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

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn clear_local_logs_for_reset() -> Result<(), PersistenceCommandError> {
    let Some(writer) = LOCAL_LOG_WRITER.get() else {
        return Ok(());
    };
    writer.clear().map_err(|error| {
        tracing::warn!(
            event = "devhud.logging.reset_failure",
            operation = "reset",
            error_kind = ?error.kind(),
            classification = "reset-failed",
            "DevHud local log reset failed"
        );
        PersistenceCommandError::ResetFailed
    })
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
#[tauri::command]
fn reset_dev_hud(
    app: AppHandle<ActiveRuntime>,
    persistence: State<'_, PersistenceState>,
    shortcut_state: State<'_, Mutex<shortcut::ShortcutState>>,
    autostart_state: State<'_, autostart::AutostartState>,
    startup_diagnostics: State<'_, Mutex<StartupDiagnostics>>,
) -> Result<PersistenceResetOutcome, PersistenceCommandError> {
    clear_browsing_data_for_reset(&app)?;
    let mut shortcuts = shortcut_state.lock().map_err(|_| {
        log_shortcut_integration_failure(
            "reset",
            shortcut::ShortcutFailure::RegistrationFailed,
            "unknown",
        );
        PersistenceCommandError::ResetFailed
    })?;
    let previous_shortcut = shortcuts.active_shortcut();
    if let Err(reason) = shortcuts.clear() {
        log_shortcut_integration_failure("reset", reason, "configured");
        return Err(PersistenceCommandError::ResetFailed);
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
        return Err(PersistenceCommandError::ResetFailed);
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
        return Err(PersistenceCommandError::ResetFailed);
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

    if let Err(reason) = clear_local_logs_for_reset() {
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
        return Err(reason);
    }

    let reset_outcome = match persistence.reset() {
        Ok(()) => PersistenceResetOutcome::Complete,
        Err(PersistenceResetFailure::BeforeRecordsRemoved(error)) => {
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
            return Err(error);
        }
        Err(PersistenceResetFailure::PartiallyRetained) => {
            PersistenceResetOutcome::PartiallyRetained
        }
        Err(PersistenceResetFailure::CleanupFailed) => PersistenceResetOutcome::CleanupFailed,
    };

    if let Ok(mut diagnostics) = startup_diagnostics.lock() {
        diagnostics.shortcut_failure = None;
        diagnostics.autostart_outcome = None;
    }
    if reset_outcome == PersistenceResetOutcome::Complete {
        tracing::info!(
            event = "devhud.persistence.reset",
            native_shortcut_configured = false,
            native_autostart_enabled = false,
            "DevHud local data was reset"
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
) -> Result<PersistenceResetOutcome, PersistenceCommandError> {
    clear_browsing_data_for_reset(webview)?;
    let reset_outcome = match state.reset() {
        Ok(()) => PersistenceResetOutcome::Complete,
        Err(PersistenceResetFailure::BeforeRecordsRemoved(error)) => return Err(error),
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
        Err(error) => return Err(widget_bridge_failure("reset", &error)),
    }
    if reset_outcome == PersistenceResetOutcome::Complete {
        tracing::info!(
            event = "devhud.persistence.reset",
            "DevHud local data was reset"
        );
    }
    Ok(reset_outcome)
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
            read_widget_configuration,
            write_widget_configuration,
            reset_dev_hud,
            show_hud,
            hide_hud,
            show_settings,
            hide_settings,
            replace_global_shortcut,
            set_launch_at_login,
            complete_first_run,
            request_update_action
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
                    tracing::warn!(
                        event = "devhud.persistence.unavailable",
                        operation = "initialize",
                        record = "persistence",
                        classification = "storage-unavailable",
                        "DevHud persistence is unavailable"
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
            app.manage(QuittingState(AtomicBool::new(false)));
            app.manage(updater::UpdateActionBoundary);

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
                .on_web_resource_request(|_, response| {
                    response.headers_mut().insert(
                        HeaderName::from_static("permissions-policy"),
                        HeaderValue::from_static(PERMISSIONS_POLICY),
                    );
                })
                .build()?;
            create_tray(app.handle())?;
            install_shortcut_handler(app.handle());
            if first_run && build_settings_window(app.handle()).is_err() {
                tracing::warn!(
                    event = "devhud.settings.window_failure",
                    operation = "first-run",
                    classification = "window-unavailable",
                    "DevHud first-run settings window could not be opened"
                );
            }

            tracing::info!(
                event = "devhud.window.created",
                runtime = runtime_name(),
                "DevHud window created"
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
            read_widget_configuration,
            write_widget_configuration,
            reset_dev_hud
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
                    tracing::warn!(
                        event = "devhud.persistence.unavailable",
                        operation = "initialize",
                        record = "persistence",
                        classification = "storage-unavailable",
                        "DevHud persistence is unavailable"
                    );
                    PersistenceState::unavailable()
                }
            };
            app.manage(persistence);
            app.manage(Mutex::new(StartupDiagnostics::default()));
            WebviewWindowBuilder::new(app, MAIN_WINDOW_LABEL, WebviewUrl::App("index.html".into()))
                .title("DevHud")
                .devtools(cfg!(debug_assertions))
                .disable_drag_drop_handler()
                .on_navigation(is_bundled_url)
                .on_new_window(|_, _| NewWindowResponse::Deny)
                .on_download(|_, _| false)
                .on_web_resource_request(|_, response| {
                    response.headers_mut().insert(
                        HeaderName::from_static("permissions-policy"),
                        HeaderValue::from_static(PERMISSIONS_POLICY),
                    );
                })
                .build()?;
            Ok(())
        })
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn platform_builder() -> tauri::Builder<ActiveRuntime> {
    tauri::Builder::<ActiveRuntime>::new().command_line_args([
        ("--disable-background-networking", None::<&str>),
        ("--disable-component-update", None),
        ("--disable-domain-reliability", None),
        ("--disable-sync", None),
        (
            "host-resolver-rules",
            Some("MAP * ~NOTFOUND, EXCLUDE tauri.localhost"),
        ),
    ])
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
fn platform_builder() -> tauri::Builder<ActiveRuntime> {
    tauri::Builder::<ActiveRuntime>::new().plugin(tauri_plugin_devhud_widget::init())
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn initialize_logging() {
    #[cfg(all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ))]
    if let Ok(writer) = local_log::LocalLogWriter::new(APPLICATION_ID) {
        let sink = writer.clone();
        let _ = LOCAL_LOG_WRITER.set(writer);
        let _ = tracing_subscriber::fmt()
            .json()
            .with_target(false)
            .with_writer(move || sink.clone())
            .try_init();
        return;
    }

    let _ = tracing_subscriber::fmt()
        .json()
        .with_target(false)
        .without_time()
        .try_init();
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn run_app() -> Result<(), RuntimeInitializationFailure> {
    let _instance_guard = match single_instance::InstanceGuard::acquire(APPLICATION_ID) {
        Ok(guard) => guard,
        Err(single_instance::InstanceGuardError::AlreadyRunning) => {
            tracing::info!(
                event = "devhud.runtime.duplicate_instance",
                classification = "already-running",
                "A resident DevHud instance is already running"
            );
            return Ok(());
        }
        Err(single_instance::InstanceGuardError::Unavailable(error)) => {
            tracing::error!(
                event = "devhud.runtime.instance_guard_failure",
                error_kind = ?error.kind(),
                classification = "instance-guard-unavailable",
                fatal = true,
                "DevHud could not acquire its desktop instance guard"
            );
            return Err(RuntimeInitializationFailure::InstanceGuardUnavailable);
        }
    };
    let app = configure_builder(platform_builder())
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
    let app = configure_builder(platform_builder())
        .build(tauri::generate_context!())
        .map_err(|_| RuntimeInitializationFailure::SystemWebviewInitialization)?;
    app.run(|_, _| {});
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
        tracing::error!(
            event = "devhud.runtime.initialization_failure",
            classification = error.classification(),
            fatal = true,
            "runtime initialization failed"
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
            "file:///tmp/index.html",
            "data:text/html,devhud",
            "about:blank",
        ] {
            assert!(!is_bundled_url(&denied.parse().unwrap()), "{denied}");
        }
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
            RuntimeInitializationFailure::InstanceGuardUnavailable.classification(),
            "instance-guard-unavailable"
        );
        assert_eq!(
            RuntimeInitializationFailure::CefInitialization.classification(),
            "cef-initialization"
        );
        assert_eq!(
            RuntimeInitializationFailure::SystemWebviewInitialization.classification(),
            "system-webview-initialization"
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
