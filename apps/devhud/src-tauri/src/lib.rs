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
    path::PathBuf,
    sync::Mutex,
};

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
type ActiveRuntime = tauri_runtime_cef::CefRuntime<tauri::EventLoopMessage>;
#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
type ActiveRuntime = tauri::Wry;

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
    runtime: &'static str,
    sandbox_enabled: bool,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
enum RuntimeCommandError {
    NonBundledAsset,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
struct PersistenceState {
    directory: PathBuf,
    write_lock: Mutex<()>,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[derive(Debug, Serialize)]
#[serde(rename_all = "kebab-case")]
enum PersistenceCommandError {
    StorageUnavailable,
    InvalidRecord,
    WriteFailed,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
impl PersistenceState {
    fn new(directory: PathBuf) -> io::Result<Self> {
        fs::create_dir_all(&directory)?;
        Ok(Self {
            directory,
            write_lock: Mutex::new(()),
        })
    }

    fn path_for(&self, key: &str) -> PathBuf {
        self.directory.join(key)
    }

    fn read(&self, key: &str) -> Result<Option<String>, PersistenceCommandError> {
        match fs::read_to_string(self.path_for(key)) {
            Ok(value) => Ok(Some(value)),
            Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
            Err(_) => Err(PersistenceCommandError::StorageUnavailable),
        }
    }

    fn write(&self, key: &str, record: &str) -> Result<(), PersistenceCommandError> {
        validate_current_record(key, record).ok_or(PersistenceCommandError::InvalidRecord)?;
        let _guard = self
            .write_lock
            .lock()
            .map_err(|_| PersistenceCommandError::StorageUnavailable)?;
        write_atomically(&self.path_for(key), record)
            .map_err(|_| PersistenceCommandError::WriteFailed)
    }
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
fn validate_settings_record(object: &serde_json::Map<String, serde_json::Value>) -> Option<()> {
    let settings = object.get("settings")?.as_object()?;
    matches!(
        settings.get("theme")?.as_str()?,
        "system" | "light" | "dark"
    )
    .then_some(())?;
    settings.get("launchAtLogin")?.as_bool()?;
    match settings.get("shortcut")? {
        serde_json::Value::Null => Some(()),
        serde_json::Value::Object(shortcut) => {
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
    let slots = object.get("configuration")?.get("slots")?.as_array()?;
    let mut unique_slots = std::collections::HashSet::new();
    for reference in slots {
        let reference = reference.as_object()?;
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

    if let Err(error) = fs::rename(&temporary_path, path) {
        let _ = fs::remove_file(&temporary_path);
        return Err(error);
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
        runtime: runtime_name(),
        sandbox_enabled: cfg!(not(any(target_os = "android", target_os = "ios"))),
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

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn read_widget_configuration(
    state: State<'_, PersistenceState>,
) -> Result<Option<String>, PersistenceCommandError> {
    state.read(WIDGET_CONFIGURATION_STORAGE_KEY)
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn write_widget_configuration(
    record: String,
    state: State<'_, PersistenceState>,
) -> Result<(), PersistenceCommandError> {
    state.write(WIDGET_CONFIGURATION_STORAGE_KEY, &record)
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn configure_builder(builder: tauri::Builder<ActiveRuntime>) -> tauri::Builder<ActiveRuntime> {
    builder
        .invoke_handler(tauri::generate_handler![
            get_runtime_info,
            read_settings,
            write_settings,
            read_widget_configuration,
            write_widget_configuration
        ])
        .setup(|app| {
            let persistence_directory = app
                .path()
                .app_local_data_dir()
                .map_err(|_| io::Error::other("DevHud persistence directory is unavailable"))?;
            app.manage(PersistenceState::new(persistence_directory)?);
            WebviewWindowBuilder::new(app, MAIN_WINDOW_LABEL, WebviewUrl::App("index.html".into()))
                .title("DevHud")
                .inner_size(720.0, 520.0)
                .devtools(true)
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
    tauri::Builder::<ActiveRuntime>::new()
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

#[cfg_attr(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    tauri::cef_entry_point
)]
#[cfg_attr(
    all(
        feature = "mobile-system-webview",
        any(target_os = "android", target_os = "ios")
    ),
    tauri::mobile_entry_point
)]
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
pub fn run() {
    initialize_logging();

    if run_app().is_err() {
        tracing::error!(
            event = "devhud.runtime.cef_initialization_failure",
            classification = "cef-initialization",
            "runtime initialization failed"
        );
        std::process::exit(70);
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
            runtime: "cef",
            sandbox_enabled: true,
        };
        let value = serde_json::to_value(runtime_info).unwrap();

        assert_eq!(value["applicationId"], APPLICATION_ID);
        assert_eq!(value["runtime"], "cef");
        assert_eq!(value["sandboxEnabled"], true);
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
    }
}
