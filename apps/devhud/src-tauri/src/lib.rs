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

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
use std::time::Duration;
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
use std::{
    fs::{self, File},
    io::{self, Write},
    path::{Path, PathBuf},
    sync::Mutex,
};

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
use tauri::{
    AppHandle, Manager, State, Webview, WebviewUrl,
    http::{HeaderName, HeaderValue},
    webview::{NewWindowResponse, WebviewWindowBuilder},
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
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
const SETTINGS_STORAGE_KEY: &str = "devhud.settings.v1";
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
const WIDGET_CONFIGURATION_STORAGE_KEY: &str = "devhud.widget-configuration.v1";
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
    update_policy: &'static str,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
enum RuntimeCommandError {
    NonBundledAsset,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
struct PersistenceState {
    directory: Option<PathBuf>,
    write_lock: Mutex<()>,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[derive(Debug, Serialize)]
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
        validate_current_record(key, record).ok_or(PersistenceCommandError::InvalidRecord)?;
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
        let Some(path) = self.path_for(key) else {
            log_persistence_unavailable("write", key);
            return Err(PersistenceCommandError::StorageUnavailable);
        };
        write_atomically(&path, record).map_err(|error| {
            log_persistence_io_failure("write", key, &error);
            PersistenceCommandError::WriteFailed
        })
    }

    fn reset(&self) -> Result<(), PersistenceCommandError> {
        let _guard = self.write_lock.lock().map_err(|_| {
            tracing::warn!(
                event = "devhud.persistence.reset_failure",
                classification = "reset-failed",
                "DevHud local data reset failed"
            );
            PersistenceCommandError::ResetFailed
        })?;
        for key in [SETTINGS_STORAGE_KEY, WIDGET_CONFIGURATION_STORAGE_KEY] {
            let Some(path) = self.path_for(key) else {
                log_persistence_unavailable("reset", key);
                return Err(PersistenceCommandError::StorageUnavailable);
            };
            if let Err(error) = fs::remove_file(path) {
                if error.kind() == io::ErrorKind::NotFound {
                    continue;
                }
                log_persistence_io_failure("reset", key, &error);
                return Err(PersistenceCommandError::ResetFailed);
            }
        }
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

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn persistence_record_name(key: &str) -> &'static str {
    match key {
        SETTINGS_STORAGE_KEY => "settings",
        WIDGET_CONFIGURATION_STORAGE_KEY => "widget-configuration",
        "persistence" => "persistence",
        _ => "unknown",
    }
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
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
    app: AppHandle<ActiveRuntime>,
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

    if std::env::var_os("DEVHUD_SMOKE").is_some() {
        std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(100));
            // The smoke run has no user-driven event to finish the CEF loop, and
            // AppHandle::exit can remain pending when requested from this command
            // thread. Remove this direct exit once the pinned runtime reliably
            // completes that request after the runtime-ready handshake.
            app.cleanup_before_exit();
            std::process::exit(0);
        });
    }

    Ok(RuntimeInfo {
        application_id: APPLICATION_ID,
        bundled_origin: bundled_origin(&url),
        operating_system: operating_system(),
        runtime: runtime_name(),
        sandbox_enabled: cfg!(not(any(target_os = "android", target_os = "ios"))),
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
    state.write(SETTINGS_STORAGE_KEY, &record)
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

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn reset_dev_hud(
    app: AppHandle<ActiveRuntime>,
    webview: Webview<ActiveRuntime>,
    state: State<'_, PersistenceState>,
) -> Result<(), PersistenceCommandError> {
    webview.clear_all_browsing_data().map_err(|_| {
        tracing::warn!(
            event = "devhud.persistence.reset_failure",
            classification = "reset-failed",
            "DevHud application browsing data reset failed"
        );
        PersistenceCommandError::ResetFailed
    })?;
    state.reset()?;
    #[cfg(all(
        feature = "mobile-system-webview",
        any(target_os = "android", target_os = "ios")
    ))]
    match app.devhud_widget_bridge().reset_configuration() {
        Ok(_) => {}
        Err(error) if error.code() == Some(WidgetBridgeErrorCode::RefreshFailed) => {
            widget_bridge_failure("reset-refresh", &error);
        }
        Err(error) => return Err(widget_bridge_failure("reset", &error)),
    }
    #[cfg(all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ))]
    let _ = app;
    tracing::info!(
        event = "devhud.persistence.reset",
        "DevHud local data was reset"
    );
    Ok(())
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
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
            WebviewWindowBuilder::new(app, MAIN_WINDOW_LABEL, WebviewUrl::App("index.html".into()))
                .title("DevHud")
                .inner_size(720.0, 520.0)
                .devtools(cfg!(feature = "desktop-cef") || cfg!(debug_assertions))
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

            tracing::info!(
                event = "devhud.window.created",
                runtime = runtime_name(),
                "DevHud window created"
            );
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
    let _ = tracing_subscriber::fmt()
        .json()
        .with_target(false)
        .without_time()
        .try_init();
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn run_app() -> tauri::Result<()> {
    let app = configure_builder(platform_builder()).build(tauri::generate_context!())?;
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

    if run_app().is_err() {
        tracing::error!(
            event = "devhud.runtime.initialization_failure",
            classification = if cfg!(feature = "desktop-cef") {
                "cef-initialization"
            } else {
                "system-webview-initialization"
            },
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
            update_policy: "Desktop updater unavailable",
        };
        let value = serde_json::to_value(runtime_info).unwrap();

        assert_eq!(value["applicationId"], APPLICATION_ID);
        assert_eq!(value["runtime"], "cef");
        assert_eq!(value["sandboxEnabled"], true);
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
}
