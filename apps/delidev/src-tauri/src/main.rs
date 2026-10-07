#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use tauri_runtime_cef::{CefRuntime, WebviewCefExt};

mod appearance_host;
mod browser_host;
mod notification_host;
mod oauth_host;
mod session_creation_preferences_host;
mod tray_host;
mod updater_host;
mod widget_host;
mod window_host;
use std::{
    collections::{BTreeMap, HashMap},
    path::PathBuf,
    sync::{Arc, Mutex},
};

// Normal setup/return failures join the owned session. Process crashes also
// retire it through the independent parent/pipe and platform containment path.
struct DesktopLifetime {
    connector: Arc<Connector>,
    supervision: Arc<Supervision>,
    worker_supervision: Arc<WorkerSupervision>,
    quit_started: Arc<std::sync::atomic::AtomicBool>,
}
impl Drop for DesktopLifetime {
    fn drop(&mut self) {
        if std::thread::panicking() || self.quit_started.load(std::sync::atomic::Ordering::Acquire)
        {
            return;
        }
        self.supervision.stop();
        self.worker_supervision.stop();
        if let Err(code) = self.connector.shutdown_owned() {
            tracing::error!(
                operation = "desktop_sidecar_shutdown",
                phase = "return-cleanup-failed",
                ?code
            );
        }
    }
}

use appearance_host::{read_appearance, update_appearance};
use session_creation_preferences_host::{
    read_session_creation_preferences, update_session_creation_preferences,
};
mod language_host;
use cef::{ImplBrowser, ImplBrowserHost};
use delidev_desktop::{
    Connection, Connector, DesktopRegistration, LocalServerStatus, LocalWorkerAction,
    LocalWorkerProof, LocalWorkerStatus, NativeFailure, RemovedConnections, SavedConnection,
    SavedConnectionState, Supervision, WorkerNetworkAction, WorkerSupervision,
    browser_storage::BrowserStorageMode, bundled_sidecar, canonical_id, connection_origin,
    default_data_root,
};
use language_host::{read_language, update_language};
use notification_host::{
    NotificationHost, begin_notifications, end_notifications, notification_permission,
    present_notification, request_notification_permission,
};
use oauth_host::account_oauth_native;

#[tauri::command]
async fn desktop_credential_access(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    action: delidev_desktop::CredentialAccessAction,
    server: String,
    generation: String,
    expected_attempt_id: Option<String>,
) -> Result<delidev_desktop::CredentialAccessResult, NativeFailure> {
    let original = capture_authority(&window)?;
    trusted_local(&window)?;
    let connector = Arc::clone(connector.inner());
    let dispatch_window = window.clone();
    let dispatch_authority = original.clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        recheck_authority(&dispatch_window, &dispatch_authority)?;
        connector.credential_access(action, &server, &generation, expected_attempt_id.as_deref())
    })
    .await
    .map_err(|_| NativeFailure::SidecarFailed)?;
    recheck_authority(&window, &original)?;
    result
}
use tauri::{
    AppHandle, Emitter, Manager, WebviewWindow, WindowEvent,
    utils::config::{Csp, CspDirectiveSources},
};
use tray_host::{TrayHost, acknowledge_tray_action, begin_tray, publish_tray, read_tray_action};
use updater_host::{UpdateHost, desktop_update_context, desktop_update_native};

fn trusted_url(url: &tauri::Url) -> bool {
    let origin =
        (url.scheme() == "tauri" && url.host_str() == Some("localhost") && url.port().is_none())
            || (url.scheme() == "http"
                && url.host_str() == Some("tauri.localhost")
                && url.port().is_none())
            || (cfg!(debug_assertions)
                && url.scheme() == "http"
                && url.host_str() == Some("127.0.0.1")
                && url.port() == Some(46311));
    origin
        && ["", "/", "/index.html"].contains(&url.path())
        && url.query().is_none()
        && url.username().is_empty()
        && url.password().is_none()
}

// One native folder dialog per process. This command accepts no renderer path,
// grants no content/Git access and rechecks the original webview on completion.
static FOLDER_PICKER_BUSY: std::sync::atomic::AtomicBool =
    std::sync::atomic::AtomicBool::new(false);
struct FolderPickerGuard;
impl Drop for FolderPickerGuard {
    fn drop(&mut self) {
        FOLDER_PICKER_BUSY.store(false, std::sync::atomic::Ordering::Release);
    }
}

#[tauri::command]
async fn choose_repository_folder(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
) -> Result<Option<String>, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        let binding = if is_local(&window) {
            trusted_local(&window)?;
            None
        } else {
            Some(saved_binding(&window, &windows)?)
        };
        if FOLDER_PICKER_BUSY
            .compare_exchange(
                false,
                true,
                std::sync::atomic::Ordering::AcqRel,
                std::sync::atomic::Ordering::Acquire,
            )
            .is_err()
        {
            return Err(NativeFailure::Busy);
        }
        let _guard = FolderPickerGuard;
        tracing::info!(operation = "repository_folder", phase = "choosing");
        let selected = rfd::AsyncFileDialog::new()
            .set_title(delidev_desktop::localization::text(
                delidev_desktop::localization::Message::ChooseRepository,
            ))
            .set_parent(&window)
            .pick_folder()
            .await;
        if let Some(original) = binding {
            let current = saved_binding(&window, &windows)?;
            if current.instance != original.instance || current.profile != original.profile {
                return Err(NativeFailure::PermissionDenied);
            }
        } else {
            trusted_local(&window)?;
        }
        let result = selected
            .map(|handle| delidev_desktop::repository_folder_path(handle.path()))
            .transpose();
        match &result {
            Ok(Some(_)) => tracing::info!(operation = "repository_folder", phase = "selected"),
            Ok(None) => tracing::info!(operation = "repository_folder", phase = "canceled"),
            Err(code) => tracing::warn!(operation = "repository_folder", phase = "failed", ?code),
        }
        result
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}

#[tauri::command]
async fn open_github(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    connector: tauri::State<'_, Arc<Connector>>,
    url: String,
) -> Result<(), NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        if is_local(&window) {
            trusted_local(&window)?;
        } else {
            saved_binding(&window, &windows)?;
        }
        let connector = Arc::clone(connector.inner());
        let result = tauri::async_runtime::spawn_blocking(move || connector.open_github(&url))
            .await
            .map_err(|_| NativeFailure::SidecarFailed)?;
        if let Err(code) = result {
            tracing::warn!(operation = "github_open", ?code);
        }
        result
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}

// This is a closed presentation selector, never a renderer-supplied URL.
#[tauri::command]
async fn open_provider_guidance(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    connector: tauri::State<'_, Arc<Connector>>,
    preset: String,
    action: delidev_desktop::provider_guidance::GuidanceAction,
) -> Result<(), NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        let original = if is_local(&window) {
            trusted_local(&window)?;
            None
        } else {
            Some(saved_binding(&window, &windows)?)
        };
        // Capture authority on the native loop; dispatch runs on a bounded
        // worker.
        let connector = Arc::clone(connector.inner());
        let result = tauri::async_runtime::spawn_blocking(move || {
            connector.open_provider_guidance(&preset, action)
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?;
        if let Some(original) = original {
            let current = saved_binding(&window, &windows)?;
            if current.instance != original.instance {
                return Err(NativeFailure::InvalidEvidence);
            }
        } else {
            trusted_local(&window)?;
        }
        result
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}

// Observation joins the native-owned attempt; it never bootstraps or pairs.
#[tauri::command]
async fn launch_local(
    window: WebviewWindow<CefRuntime>,
    supervision: tauri::State<'_, Arc<Supervision>>,
) -> Result<Option<Connection>, NativeFailure> {
    let response_window = window.clone();
    let mut original_authority = capture_authority(&response_window)?;
    let result = async {
        trusted_local(&window)?;
        let supervision = Arc::clone(supervision.inner());
        tauri::async_runtime::spawn_blocking(move || supervision.launch_connection())
            .await
            .map_err(|_| NativeFailure::SidecarFailed)?
    }
    .await;
    if let Ok(Some(connection)) = &result {
        original_authority.local_revision =
            adopt_local(&response_window, &original_authority, connection)?;
    }
    recheck_authority(&response_window, &original_authority)?;
    result
}

#[tauri::command]
async fn retry_local(
    window: WebviewWindow<CefRuntime>,
    supervision: tauri::State<'_, Arc<Supervision>>,
) -> Result<Connection, NativeFailure> {
    let response_window = window.clone();
    let mut original_authority = capture_authority(&response_window)?;
    let result = async {
        trusted_local(&window)?;
        let supervision = Arc::clone(supervision.inner());
        tauri::async_runtime::spawn_blocking(move || supervision.retry_launch())
            .await
            .map_err(|_| NativeFailure::SidecarFailed)?
    }
    .await;
    if let Ok(connection) = &result {
        original_authority.local_revision =
            adopt_local(&response_window, &original_authority, connection)?;
    }
    recheck_authority(&response_window, &original_authority)?;
    result
}

#[tauri::command]
async fn connect_local(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    supervision: tauri::State<'_, Arc<Supervision>>,
) -> Result<Connection, NativeFailure> {
    let response_window = window.clone();
    let mut original_authority = capture_authority(&response_window)?;
    let result = async {
        if !is_local(&window)
            || !trusted_url(&window.url().map_err(|_| NativeFailure::PermissionDenied)?)
        {
            return Err(NativeFailure::PermissionDenied);
        }
        let connector = Arc::clone(connector.inner());
        let result = tauri::async_runtime::spawn_blocking(move || connector.connect())
            .await
            .map_err(|_| NativeFailure::SidecarFailed)?;
        supervision.refresh();
        let connection = result?;
        if !valid_local_runtime(&connection.endpoint) {
            return Err(NativeFailure::Incompatible);
        }
        supervision.adopt(&connection);
        Ok(connection)
    }
    .await;
    if let Ok(connection) = &result {
        original_authority.local_revision =
            adopt_local(&response_window, &original_authority, connection)?;
    }
    recheck_authority(&response_window, &original_authority)?;
    result
}

#[tauri::command]
async fn inspect_local_registration(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
) -> Result<DesktopRegistration, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        trusted_local(&window)?;
        let connector = Arc::clone(connector.inner());
        tauri::async_runtime::spawn_blocking(move || connector.inspect_desktop_registration())
            .await
            .map_err(|_| NativeFailure::SidecarFailed)?
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}

#[tauri::command]
async fn recover_local_registration(
    window: WebviewWindow<CefRuntime>,
    supervision: tauri::State<'_, Arc<Supervision>>,
    connector: tauri::State<'_, Arc<Connector>>,
    device_id: String,
    revision: String,
    request_id: String,
) -> Result<Connection, NativeFailure> {
    let response_window = window.clone();
    let mut original_authority = capture_authority(&response_window)?;
    let result = async {
        trusted_local(&window)?;
        let connector = Arc::clone(connector.inner());
        let connection = tauri::async_runtime::spawn_blocking(move || {
            connector.recover_desktop_registration(&device_id, &revision, &request_id)
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
        if !valid_local_runtime(&connection.endpoint) {
            return Err(NativeFailure::Incompatible);
        }
        supervision.adopt(&connection);
        Ok(connection)
    }
    .await;
    if let Ok(connection) = &result {
        original_authority.local_revision =
            adopt_local(&response_window, &original_authority, connection)?;
    }
    recheck_authority(&response_window, &original_authority)?;
    result
}

#[tauri::command]
async fn local_server_status(
    window: WebviewWindow<CefRuntime>,
    supervision: tauri::State<'_, Arc<Supervision>>,
) -> Result<LocalServerStatus, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        if !is_local(&window)
            || !trusted_url(&window.url().map_err(|_| NativeFailure::PermissionDenied)?)
        {
            return Err(NativeFailure::PermissionDenied);
        }
        Ok(supervision.status())
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}

#[tauri::command]
async fn local_worker_proof(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
) -> Result<LocalWorkerProof, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        if !is_local(&window)
            || !trusted_url(&window.url().map_err(|_| NativeFailure::PermissionDenied)?)
        {
            return Err(NativeFailure::PermissionDenied);
        }
        let connector = Arc::clone(connector.inner());
        let proof = tauri::async_runtime::spawn_blocking(move || connector.local_worker_proof())
            .await
            .map_err(|_| NativeFailure::SidecarFailed)??;
        if !valid_local_runtime(&proof.endpoint) {
            return Err(NativeFailure::Incompatible);
        }
        Ok(proof)
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}

#[tauri::command]
async fn local_worker_control(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    action: LocalWorkerAction,
    generation: Option<String>,
) -> Result<LocalWorkerStatus, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        if !is_local(&window)
            || !trusted_url(&window.url().map_err(|_| NativeFailure::PermissionDenied)?)
        {
            return Err(NativeFailure::PermissionDenied);
        }
        let connector = Arc::clone(connector.inner());
        tauri::async_runtime::spawn_blocking(move || {
            connector.local_worker(action, generation.as_deref())
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}

#[derive(Clone)]
struct SavedBinding {
    profile: SavedConnection,
    instance: String,
    closing: bool,
}
#[derive(Default)]
struct ProductWindows {
    bindings: Mutex<BTreeMap<String, SavedBinding>>,
    registry: Mutex<delidev_desktop::window_registry::Registry>,
    removals: Mutex<BTreeMap<String, SavedBinding>>,
}

// CEF URL getters enqueue work on its UI loop and wait for a response. Every
// command reaching this check must execute asynchronously off that loop; a
// synchronous IPC handler can deadlock both the window and application quit.
// Keep this boundary while the pinned runtime uses blocking URL getters.
fn is_local(window: &WebviewWindow<CefRuntime>) -> bool {
    window
        .state::<Arc<ProductWindows>>()
        .registry
        .lock()
        .is_ok_and(|registry| {
            registry
                .admitted(window.label())
                .is_ok_and(|entry| entry.role == delidev_desktop::window_registry::Role::Local)
        })
}
fn trusted_local(window: &WebviewWindow<CefRuntime>) -> Result<(), NativeFailure> {
    if !is_local(window)
        || !trusted_url(&window.url().map_err(|_| NativeFailure::PermissionDenied)?)
    {
        return Err(NativeFailure::PermissionDenied);
    }
    Ok(())
}
#[derive(Clone)]
struct WindowAuthority {
    entry: delidev_desktop::window_registry::Entry,
    local_revision: u64,
    saved: Option<SavedConnection>,
}
fn capture_authority(window: &WebviewWindow<CefRuntime>) -> Result<WindowAuthority, NativeFailure> {
    let windows = window.state::<Arc<ProductWindows>>();
    let saved = if is_local(window) {
        trusted_local(window)?;
        None
    } else {
        Some(saved_binding(window, &windows)?.profile)
    };
    let registry = windows.registry.lock().map_err(|_| NativeFailure::Busy)?;
    Ok(WindowAuthority {
        entry: registry.admitted(window.label())?,
        local_revision: registry.local_revision,
        saved,
    })
}
fn recheck_authority(
    window: &WebviewWindow<CefRuntime>,
    original: &WindowAuthority,
) -> Result<(), NativeFailure> {
    let current = capture_authority(window)?;
    if current.entry.instance != original.entry.instance
        || current.entry.role != original.entry.role
        || (original.saved.is_none() && current.local_revision != original.local_revision)
        || !match (&current.saved, &original.saved) {
            (None, None) => true,
            (Some(current), Some(original)) => current.same_authority(original),
            _ => false,
        }
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(())
}
// The source document was authenticated off the CEF loop. Recheck its
// process-local lifetime immediately before native creation without calling
// CEF's blocking URL getter from the UI thread.
fn recheck_registered(
    app: &AppHandle<CefRuntime>,
    original: &WindowAuthority,
) -> Result<(), NativeFailure> {
    recheck_record(&app.state::<Arc<ProductWindows>>(), original)
}
fn recheck_record(
    windows: &ProductWindows,
    original: &WindowAuthority,
) -> Result<(), NativeFailure> {
    let registry = windows.registry.lock().map_err(|_| NativeFailure::Busy)?;
    let current = registry.admitted(&original.entry.label)?;
    if current.instance != original.entry.instance
        || current.role != original.entry.role
        || (original.saved.is_none() && registry.local_revision != original.local_revision)
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    drop(registry);
    if let Some(expected) = &original.saved {
        let barriers = windows.removals.lock().map_err(|_| NativeFailure::Busy)?;
        if barriers.contains_key(&expected.id) {
            return Err(NativeFailure::InvalidEvidence);
        }
        let bindings = windows.bindings.lock().map_err(|_| NativeFailure::Busy)?;
        if !bindings.get(&current.label).is_some_and(|binding| {
            !binding.closing
                && binding.instance == current.instance
                && binding.profile.same_authority(expected)
        }) {
            return Err(NativeFailure::InvalidEvidence);
        }
    }
    Ok(())
}
fn adopt_local(
    window: &WebviewWindow<CefRuntime>,
    original: &WindowAuthority,
    connection: &Connection,
) -> Result<u64, NativeFailure> {
    use sha2::{Digest, Sha256};
    let mut digest = Sha256::new();
    for value in [
        &connection.endpoint,
        &connection.server_id,
        &connection.device_id,
        &connection.token,
    ] {
        digest.update(value.len().to_le_bytes());
        digest.update(value.as_bytes());
    }
    recheck_authority(window, original)?;
    let windows = window.state::<Arc<ProductWindows>>();
    let (changed, revision) = {
        let mut registry = windows.registry.lock().map_err(|_| NativeFailure::Busy)?;
        let current = registry.admitted(window.label())?;
        if current.instance != original.entry.instance || current.role != original.entry.role {
            return Err(NativeFailure::InvalidEvidence);
        }
        registry.adopt_local_scope_at(
            digest.finalize().into(),
            original.local_revision,
            delidev_desktop::session_creation_preferences::Scope {
                server_id: connection.server_id.clone(),
                device_id: connection.device_id.clone(),
            },
        )?
    };
    if changed {
        // The event carries no identity or credential. Each sibling re-reads
        // the native-owned observation and authenticates its own transport.
        for sibling in window.app_handle().webview_windows().into_values() {
            if is_local(&sibling) {
                sibling
                    .state::<Arc<delidev_desktop::oauth::OAuthHost>>()
                    .close_window(sibling.label());
                sibling
                    .state::<Arc<NotificationHost>>()
                    .remove(sibling.label());
                tray_host::remove(sibling.app_handle(), sibling.label());
                let app = sibling.app_handle().clone();
                let label = sibling.label().to_owned();
                let browser = Arc::clone(app.state::<Arc<browser_host::BrowserHost>>().inner());
                app.run_on_main_thread(move || {
                    if let Err(code) = browser.close_window(&label) {
                        tracing::warn!(operation = "connection_browser_cleanup", ?code);
                    }
                })
                .map_err(|_| NativeFailure::SidecarFailed)?;
                if sibling.label() != window.label() {
                    let _ = sibling.emit("local-connection-changed", ());
                }
            }
        }
    }
    Ok(revision)
}
fn saved_binding(
    window: &WebviewWindow<CefRuntime>,
    windows: &ProductWindows,
) -> Result<SavedBinding, NativeFailure> {
    let url = window.url().map_err(|_| NativeFailure::PermissionDenied)?;
    // Saved credentials never reach an external development-server document.
    if !trusted_url(&url) || !(url.scheme() == "tauri" || url.host_str() == Some("tauri.localhost"))
    {
        return Err(NativeFailure::PermissionDenied);
    }
    let entry = windows
        .registry
        .lock()
        .map_err(|_| NativeFailure::Busy)?
        .admitted(window.label())?;
    let delidev_desktop::window_registry::Role::Saved(id) = entry.role else {
        return Err(NativeFailure::PermissionDenied);
    };
    if windows
        .removals
        .try_lock()
        .map_err(|_| NativeFailure::Busy)?
        .contains_key(&id)
    {
        return Err(NativeFailure::PermissionDenied);
    }
    windows
        .bindings
        .try_lock()
        .map_err(|_| NativeFailure::Busy)?
        .get(window.label())
        .filter(|binding| {
            !binding.closing && binding.instance == entry.instance && binding.profile.id == id
        })
        .cloned()
        .ok_or(NativeFailure::PermissionDenied)
}
#[tauri::command]
async fn connection_context(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
) -> Result<Option<SavedConnection>, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        if is_local(&window) {
            trusted_local(&window)?;
            return Ok(None);
        }
        Ok(Some(saved_binding(&window, &windows)?.profile))
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
async fn saved_connections(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
) -> Result<Vec<SavedConnection>, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        trusted_local(&window)?;
        let connector = Arc::clone(connector.inner());
        tauri::async_runtime::spawn_blocking(move || connector.saved_connections())
            .await
            .map_err(|_| NativeFailure::SidecarFailed)?
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
async fn removed_connections(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    after: String,
) -> Result<RemovedConnections, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        trusted_local(&window)?;
        let connector = Arc::clone(connector.inner());
        tauri::async_runtime::spawn_blocking(move || connector.removed_connections(&after))
            .await
            .map_err(|_| NativeFailure::SidecarFailed)?
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
async fn retained_worker_control(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    id: String,
    action: LocalWorkerAction,
    generation: Option<String>,
) -> Result<LocalWorkerStatus, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        trusted_local(&window)?;
        let connector = Arc::clone(connector.inner());
        tauri::async_runtime::spawn_blocking(move || {
            connector.retained_worker(&id, action, generation.as_deref())
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
// Tauri injects trusted framework state separately from the closed IPC fields.
// Keep this exception on the command; remove it if those injected states are
// consolidated.
#[allow(
    clippy::too_many_arguments,
    reason = "Tauri-injected state is separate from typed IPC input"
)]
#[tauri::command]
async fn remove_connection(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    browser: tauri::State<'_, Arc<browser_host::BrowserHost>>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    id: String,
    request_id: String,
    revision: u64,
) -> Result<SavedConnection, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        trusted_local(&window)?;
        canonical_id(&id)?;
        canonical_id(&request_id)?;
        let connector = Arc::clone(connector.inner());
        let lookup = Arc::clone(&connector);
        let profile_id = id.clone();
        let profile =
            tauri::async_runtime::spawn_blocking(move || lookup.inspect_saved(&profile_id))
                .await
                .map_err(|_| NativeFailure::SidecarFailed)??;
        // Avoid closing a window from a stale confirmation. Go independently
        // validates and claims the exact mutation at its durable commit
        // boundary.
        if let Some(removal) = &profile.removal {
            if removal.request_id != request_id || removal.expected_revision != revision {
                return Err(NativeFailure::InvalidEvidence);
            }
        } else if profile.revision != revision {
            return Err(NativeFailure::InvalidEvidence);
        }
        let instance = uuid::Uuid::now_v7().to_string();
        let original_profile = profile.clone();
        let original = {
            // One profile-level barrier covers every current and pending
            // window. Creation takes this gate through native
            // binding reservation.
            let mut barriers = windows.removals.lock().map_err(|_| NativeFailure::Busy)?;
            if let Some(binding) = barriers.get(&id) {
                let retry = profile.state == SavedConnectionState::Removing
                    && binding.profile.state == SavedConnectionState::Removing
                    && binding.profile.removal == profile.removal;
                if profile.state != SavedConnectionState::Removed && !retry {
                    return Err(NativeFailure::Busy);
                }
            }
            let original = barriers.insert(
                id.clone(),
                SavedBinding {
                    profile,
                    instance: instance.clone(),
                    closing: true,
                },
            );
            let mut registry = windows.registry.lock().map_err(|_| NativeFailure::Busy)?;
            for entry in registry.entries().into_iter().filter(|entry| {
                entry.role == delidev_desktop::window_registry::Role::Saved(id.clone())
            }) {
                registry.begin_close(&entry.label, false)?;
            }
            original
        };
        let affected = windows
            .bindings
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .iter()
            .filter(|(_, binding)| binding.profile.id == id)
            .map(|(label, _)| label.clone())
            .collect::<Vec<_>>();
        let host = Arc::clone(browser.inner());
        let close_app = app.clone();
        let (send, receive) = tokio::sync::oneshot::channel();
        let destroyed = match app.run_on_main_thread(move || {
            let mut result = Ok(());
            for label in affected {
                if let Some(saved) = close_app.get_webview_window(&label) {
                    let closed = host
                        .close_window(&label)
                        .and_then(|()| saved.destroy().map_err(|_| NativeFailure::SidecarFailed));
                    if closed.is_err() {
                        result = closed;
                    }
                }
            }
            let _ = send.send(result);
        }) {
            Ok(()) => receive
                .await
                .map_err(|_| NativeFailure::SidecarFailed)
                .and_then(|v| v),
            Err(_) => Err(NativeFailure::SidecarFailed),
        };
        if destroyed.is_err() {
            let mut barriers = windows
                .removals
                .lock()
                .map_err(|_| NativeFailure::SidecarFailed)?;
            if let Some(original) = original {
                barriers.insert(id.clone(), original);
            } else {
                barriers.remove(&id);
            }
            let mut registry = windows.registry.lock().map_err(|_| NativeFailure::Busy)?;
            for entry in registry.entries().into_iter().filter(|entry| {
                entry.role == delidev_desktop::window_registry::Role::Saved(id.clone())
            }) {
                registry.cancel_close(&entry.label);
            }
            return Err(NativeFailure::SidecarFailed);
        }
        // All fallible window setup precedes durable browser staging. A staged
        // marker alone cannot authorize purge: Go's exact retained removal
        // receipt is independently checked after CEF shutdown.
        let mut planned = original_profile.clone();
        if planned.removal.is_none() {
            planned.state = SavedConnectionState::Removing;
            planned.revision = revision
                .checked_add(1)
                .ok_or(NativeFailure::InvalidEvidence)?;
            planned.removal = Some(delidev_desktop::RemovalMetadata {
                request_id: request_id.clone(),
                expected_revision: revision,
            });
        }
        let retry_lookup = Arc::clone(&connector);
        let retry_id = id.clone();
        let retry_request = request_id.clone();
        let host = Arc::clone(browser.inner());
        let staged = planned.clone();
        let close_app = app.clone();
        let result = tauri::async_runtime::spawn_blocking(move || {
            host.prepare_forget(&staged)?;
            let close_host = Arc::clone(&host);
            if close_app
                .run_on_main_thread(move || close_host.close_scope(&staged))
                .is_err()
            {
                tracing::warn!(
                    operation = "browser_scope_close",
                    code = "window-unavailable"
                );
            }
            connector.remove_saved(&id, &request_id, revision)
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)
        .and_then(|value| value);
        // Keep the barrier even when cleanup fails: Go may already have
        // committed the removal marker, and an in-flight open can hold
        // older paired metadata. Re-inspect an uncertain failure so its
        // exact durable cleanup retry can replace this barrier without
        // admitting a stale open.
        if result.is_err()
            && let Ok(Ok(observed)) =
                tauri::async_runtime::spawn_blocking(move || retry_lookup.inspect_saved(&retry_id))
                    .await
        {
            if observed.state == SavedConnectionState::Paired
                && observed.same_authority(&original_profile)
            {
                let host = Arc::clone(browser.inner());
                tauri::async_runtime::spawn_blocking(move || {
                    host.cancel_unaccepted_forget(&planned)
                })
                .await
                .map_err(|_| NativeFailure::SidecarFailed)??;
                let mut values = windows
                    .removals
                    .lock()
                    .map_err(|_| NativeFailure::SidecarFailed)?;
                if values
                    .get(&original_profile.id)
                    .is_some_and(|binding| binding.instance == instance)
                {
                    values.remove(&original_profile.id);
                }
            } else if observed.state == SavedConnectionState::Removing
                && observed.removal.as_ref().is_some_and(|removal| {
                    removal.request_id == retry_request && removal.expected_revision == revision
                })
            {
                let mut values = windows
                    .removals
                    .lock()
                    .map_err(|_| NativeFailure::SidecarFailed)?;
                if let Some(binding) = values.get_mut(&original_profile.id)
                    && binding.instance == instance
                {
                    binding.profile = observed;
                }
            }
        }
        if let Ok(observed) = &result {
            let mut barriers = windows.removals.lock().map_err(|_| NativeFailure::Busy)?;
            if let Some(binding) = barriers.get_mut(&observed.id)
                && binding.instance == instance
            {
                binding.profile = observed.clone();
            }
        }
        if result
            .as_ref()
            .is_ok_and(|profile| profile.state == SavedConnectionState::Removed)
        {
            // Credential removal remains authoritative even if presentation
            // cleanup fails. The stale metadata grants no
            // connection or execution authority.
            let id = result.as_ref().unwrap().id.clone();
            let cleanup = tauri::async_runtime::spawn_blocking(move || {
                tray_host::remove_widget(&app, &id);
            })
            .await;
            if cleanup.is_err() {
                tracing::warn!(operation = "widget_snapshot", code = "storage-unavailable");
            }
        }
        result
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
async fn pair_connection(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    id: String,
    name: String,
    grant: String,
) -> Result<SavedConnection, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        let grant = zeroize::Zeroizing::new(grant.into_bytes());
        trusted_local(&window)?;
        let connector = Arc::clone(connector.inner());
        tauri::async_runtime::spawn_blocking(move || connector.pair_saved(&id, &name, grant))
            .await
            .map_err(|_| NativeFailure::SidecarFailed)?
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
async fn retry_connection(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    id: String,
) -> Result<SavedConnection, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        trusted_local(&window)?;
        let connector = Arc::clone(connector.inner());
        tauri::async_runtime::spawn_blocking(move || connector.retry_saved(&id))
            .await
            .map_err(|_| NativeFailure::SidecarFailed)?
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
// Tauri maps these separate argument names into the renderer IPC contract.
// Remove this exception when the command and callers use a validated request
// object.
#[expect(clippy::too_many_arguments)]
#[tauri::command]
async fn rename_connection(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    id: String,
    request_id: String,
    revision: u64,
    name: String,
) -> Result<SavedConnection, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        trusted_local(&window)?;
        let connector = Arc::clone(connector.inner());
        let profile = tauri::async_runtime::spawn_blocking(move || {
            connector.rename_saved(&id, &request_id, revision, &name)
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
        update_saved_label(&app, &windows, profile)
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}

fn reconcile_saved_label(
    values: &BTreeMap<String, SavedBinding>,
    mut profile: SavedConnection,
) -> Result<SavedConnection, NativeFailure> {
    let id = profile.id.clone();
    for binding in values.values().filter(|v| v.profile.id == id) {
        if binding.closing || !binding.profile.same_authority(&profile) {
            return Err(NativeFailure::InvalidEvidence);
        }
        if binding.profile.revision > profile.revision {
            profile = binding.profile.clone();
        }
    }
    Ok(profile)
}

fn apply_saved_label(
    windows: &ProductWindows,
    profile: SavedConnection,
) -> Result<SavedConnection, NativeFailure> {
    let mut values = windows
        .bindings
        .lock()
        .map_err(|_| NativeFailure::SidecarFailed)?;
    let profile = reconcile_saved_label(&values, profile)?;
    for binding in values.values_mut().filter(|v| v.profile.id == profile.id) {
        binding.profile = profile.clone();
    }
    Ok(profile)
}
fn update_saved_label(
    app: &AppHandle<CefRuntime>,
    windows: &ProductWindows,
    mut profile: SavedConnection,
) -> Result<SavedConnection, NativeFailure> {
    profile = apply_saved_label(windows, profile)?;
    // Never hold a binding mutex while waiting for native title publication.
    // The UI callback reads the newest committed name, so reordered callbacks
    // cannot roll a later accepted rename backward.
    let target = app.clone();
    let id = profile.id.clone();
    app.run_on_main_thread(move || {
        let windows = target.state::<Arc<ProductWindows>>();
        let entries = match windows.registry.lock() {
            Ok(r) => r.entries(),
            Err(_) => return,
        };
        let bindings = match windows.bindings.lock() {
            Ok(v) => v.clone(),
            Err(_) => return,
        };
        for entry in entries {
            if let Some(binding) = bindings
                .get(&entry.label)
                .filter(|v| !v.closing && v.profile.id == id && v.instance == entry.instance)
                && let Some(window) = target.get_webview_window(&entry.label)
                && (window
                    .set_title(&window_host::title(&entry, Some(&binding.profile.name)))
                    .is_err()
                    || window.emit("saved-connection-label", ()).is_err())
            {
                tracing::warn!(
                    operation = "saved_window_label",
                    code = "window-unavailable"
                );
            }
        }
        tray_host::schedule(&target);
    })
    .map_err(|_| NativeFailure::SidecarFailed)?;
    Ok(profile)
}

#[tauri::command]
async fn connect_saved(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
) -> Result<Connection, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        let binding = saved_binding(&window, &windows)?;
        let connector = Arc::clone(connector.inner());
        let expected = binding.profile.clone();
        let result =
            tauri::async_runtime::spawn_blocking(move || connector.connect_saved(&expected))
                .await
                .map_err(|_| NativeFailure::SidecarFailed)??;
        let current = saved_binding(&window, &windows)?;
        if current.closing
            || current.instance != binding.instance
            || !current.profile.same_authority(&binding.profile)
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(result)
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
async fn saved_worker_proof(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
) -> Result<LocalWorkerProof, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        let binding = saved_binding(&window, &windows)?;
        let connector = Arc::clone(connector.inner());
        let expected = binding.profile.clone();
        let proof =
            tauri::async_runtime::spawn_blocking(move || connector.saved_worker_proof(&expected))
                .await
                .map_err(|_| NativeFailure::SidecarFailed)??;
        let current = saved_binding(&window, &windows)?;
        if current.closing
            || current.instance != binding.instance
            || !current.profile.same_authority(&binding.profile)
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(proof)
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
async fn saved_worker_control(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    action: LocalWorkerAction,
    generation: Option<String>,
) -> Result<LocalWorkerStatus, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        let binding = saved_binding(&window, &windows)?;
        let connector = Arc::clone(connector.inner());
        let expected = binding.profile.clone();
        let result = tauri::async_runtime::spawn_blocking(move || {
            connector.saved_worker(&expected, action, generation.as_deref())
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
        let current = saved_binding(&window, &windows)?;
        if current.closing
            || current.instance != binding.instance
            || !current.profile.same_authority(&binding.profile)
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(result)
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
async fn worker_network_control(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    machine: String,
    action: WorkerNetworkAction,
    ciphertext: Vec<u8>,
    digest: String,
) -> Result<serde_json::Value, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        let binding = if is_local(&window) {
            trusted_local(&window)?;
            None
        } else {
            Some(saved_binding(&window, &windows)?)
        };
        let expected = binding.as_ref().map(|value| value.profile.clone());
        let connector = Arc::clone(connector.inner());
        let result = tauri::async_runtime::spawn_blocking(move || {
            connector.worker_network(expected.as_ref(), &machine, action, ciphertext, &digest)
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
        if let Some(binding) = binding {
            let current = saved_binding(&window, &windows)?;
            if current.closing
                || current.instance != binding.instance
                || !current.profile.same_authority(&binding.profile)
            {
                return Err(NativeFailure::InvalidEvidence);
            }
        } else {
            trusted_local(&window)?;
        }
        Ok(result)
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}

fn show(window: &WebviewWindow<CefRuntime>) -> Result<(), NativeFailure> {
    window
        .unminimize()
        .and_then(|_| window.show())
        .and_then(|_| window.set_focus())
        .map_err(|_| NativeFailure::SidecarFailed)
}
fn saved_csp(policy: &str, origin: &str) -> Result<String, NativeFailure> {
    if connection_origin(origin)? != origin {
        return Err(NativeFailure::InvalidEvidence);
    }
    let mut directives: HashMap<String, CspDirectiveSources> =
        Csp::Policy(policy.to_owned()).into();
    directives.insert(
        "connect-src".into(),
        CspDirectiveSources::List(vec![
            "ipc:".to_owned(),
            "http://ipc.localhost".to_owned(),
            origin.to_owned(),
        ]),
    );
    Ok(Csp::from(directives).to_string())
}
fn local_csp(policy: Csp, endpoint: &str, development: bool) -> Result<Csp, NativeFailure> {
    if !valid_local_runtime(endpoint) {
        return Err(NativeFailure::InvalidEvidence);
    }
    let mut directives: HashMap<String, CspDirectiveSources> = policy.into();
    let mut sources = vec![
        "ipc:".to_owned(),
        "http://ipc.localhost".to_owned(),
        endpoint.to_owned(),
    ];
    if development {
        sources.push("ws://127.0.0.1:46311".to_owned());
    }
    directives.insert("connect-src".into(), CspDirectiveSources::List(sources));
    Ok(Csp::from(directives))
}
// The pinned CEF runtime applies macOS accessibility notifications only to
// browsers that already exist. Enable each trusted document explicitly so a
// saved-server window created afterward also exposes its semantic content.
// Remove this workaround when the runtime carries accessibility state forward
// to every newly created browser; no content or accessibility tree is logged.
fn enable_document_accessibility(window: &WebviewWindow<CefRuntime>) {
    if window
        .with_cef_webview(|view| {
            if let Some(host) = view.browser().host() {
                host.set_accessibility_state(cef::State::ENABLED);
            } else {
                tracing::warn!(
                    operation = "document_accessibility",
                    code = "browser-unavailable"
                );
            }
        })
        .is_err()
    {
        tracing::warn!(
            operation = "document_accessibility",
            code = "window-unavailable"
        );
    }
}
#[tauri::command]
async fn show_connection_manager(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
) -> Result<(), NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        let original = capture_authority(&window)?;
        recheck_authority(&window, &original)?;
        window_host::show_local(&app, original).await
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
async fn open_connection(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    id: String,
) -> Result<(), NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        trusted_local(&window)?;
        let original = capture_authority(&window)?;
        canonical_id(&id)?;
        if !cfg!(feature = "custom-protocol") {
            return Err(NativeFailure::Incompatible);
        }
        let connector = Arc::clone(connector.inner());
        let profile = tauri::async_runtime::spawn_blocking(move || connector.inspect_saved(&id))
            .await
            .map_err(|_| NativeFailure::SidecarFailed)??;
        recheck_authority(&window, &original)?;
        if profile.state != SavedConnectionState::Paired {
            return Err(NativeFailure::InvalidEvidence);
        }
        let entry = windows
            .registry
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .recent(Some(&delidev_desktop::window_registry::Role::Saved(
                profile.id.clone(),
            )));
        if let Some(entry) = entry
            && let Some(existing) = app.get_webview_window(&entry.label)
        {
            let binding = saved_binding(&existing, &windows)?;
            if !binding.profile.same_authority(&profile) {
                return Err(NativeFailure::InvalidEvidence);
            }
            update_saved_label(&app, &windows, profile)?;
            return show(&existing);
        }
        window_host::create_on_loop(&app, Some(profile), Some(original))
            .await
            .map(|_| ())
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}

// Tauri injects trusted framework state separately from the closed IPC fields.
// Keep this exception on the command; remove it if those injected states are
// consolidated.
#[allow(
    clippy::too_many_arguments,
    reason = "Tauri-injected state is separate from typed IPC input"
)]
#[tauri::command]
async fn open_browser(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    connector: tauri::State<'_, Arc<Connector>>,
    host: tauri::State<'_, Arc<browser_host::BrowserHost>>,
    profile_id: String,
    view_id: String,
    url: String,
    bounds: browser_host::Bounds,
) -> Result<browser_host::BrowserState, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        let binding = if is_local(&window) {
            trusted_local(&window)?;
            None
        } else {
            Some(saved_binding(&window, &windows)?)
        };
        let scope = binding.as_ref().map(|b| b.profile.clone());
        let reservation_host = Arc::clone(host.inner());
        let label = window.label().to_string();
        let reservation_view = view_id.clone();
        let reservation_app = app.clone();
        tauri::async_runtime::spawn_blocking(move || {
            reservation_host.reserve_on_worker(reservation_app, label, reservation_view)
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
        let connector = Arc::clone(connector.inner());
        let scope_copy = scope.clone();
        let record = tauri::async_runtime::spawn_blocking(move || {
            connector.browser_profile(scope_copy.as_ref(), &profile_id)
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
        if let Some(original) = &binding {
            let current = saved_binding(&window, &windows)?;
            if current.instance != original.instance
                || !current.profile.same_authority(&original.profile)
            {
                return Err(NativeFailure::InvalidEvidence);
            }
        } else {
            trusted_local(&window)?;
        }
        let storage_host = Arc::clone(host.inner());
        let storage_scope = scope.clone();
        let storage_record = record.clone();
        let label = window.label().to_string();
        let storage_view = view_id.clone();
        tauri::async_runtime::spawn_blocking(move || {
            storage_host.prepare_open(&label, &storage_view, storage_scope, storage_record, &url)
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
        if let Some(original) = &binding {
            let current = saved_binding(&window, &windows)?;
            if current.instance != original.instance
                || !current.profile.same_authority(&original.profile)
            {
                return Err(NativeFailure::InvalidEvidence);
            }
        } else {
            trusted_local(&window)?;
        }
        let host = Arc::clone(host.inner());
        let (send, receive) = tokio::sync::oneshot::channel();
        let copy = app.clone();
        let source = original_authority.clone();
        app.run_on_main_thread(move || {
            let result = recheck_registered(&copy, &source)
                .and_then(|()| host.open(&copy, &window, scope, record, bounds, view_id));
            let _ = send.send(result);
        })
        .map_err(|_| NativeFailure::SidecarFailed)?;
        receive.await.map_err(|_| NativeFailure::SidecarFailed)?
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
// Tauri injects trusted framework state separately from the closed IPC fields.
// Keep this exception on the command; remove it if those injected states are
// consolidated.
#[allow(
    clippy::too_many_arguments,
    reason = "Tauri-injected state is separate from typed IPC input"
)]
#[tauri::command]
async fn control_browser(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    connector: tauri::State<'_, Arc<Connector>>,
    host: tauri::State<'_, Arc<browser_host::BrowserHost>>,
    profile_id: String,
    view_id: String,
    action: browser_host::Action,
    url: Option<String>,
    tab_id: Option<String>,
    bounds: Option<browser_host::Bounds>,
) -> Result<browser_host::BrowserState, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        let binding = if is_local(&window) {
            trusted_local(&window)?;
            None
        } else {
            Some(saved_binding(&window, &windows)?)
        };
        let scope = binding.as_ref().map(|b| b.profile.clone());
        // Hide closes an existing owned view even while its server is offline.
        if !matches!(
            action,
            browser_host::Action::Hide | browser_host::Action::Resize
        ) {
            let c = Arc::clone(connector.inner());
            let id = profile_id.clone();
            let record = tauri::async_runtime::spawn_blocking(move || {
                c.browser_profile(scope.as_ref(), &id)
            })
            .await
            .map_err(|_| NativeFailure::SidecarFailed)??;
            if record.data.state != delidev_desktop::browser::ProfileState::Active {
                return Err(NativeFailure::Stopped);
            }
        }
        if let Some(original) = &binding {
            let current = saved_binding(&window, &windows)?;
            if current.instance != original.instance
                || !current.profile.same_authority(&original.profile)
            {
                return Err(NativeFailure::InvalidEvidence);
            }
        } else {
            trusted_local(&window)?;
        }
        let prepared_revision = if matches!(
            action,
            browser_host::Action::Hide | browser_host::Action::Resize
        ) {
            None
        } else {
            let storage_host = Arc::clone(host.inner());
            let label = window.label().to_string();
            let storage_profile = profile_id.clone();
            let storage_view = view_id.clone();
            let storage_url = url.clone();
            Some(
                tauri::async_runtime::spawn_blocking(move || {
                    storage_host.prepare_control(
                        &label,
                        &storage_profile,
                        &storage_view,
                        action,
                        storage_url.as_deref(),
                        tab_id.as_deref(),
                    )
                })
                .await
                .map_err(|_| NativeFailure::SidecarFailed)??,
            )
        };
        if let Some(original) = &binding {
            let current = saved_binding(&window, &windows)?;
            if current.instance != original.instance
                || !current.profile.same_authority(&original.profile)
            {
                return Err(NativeFailure::InvalidEvidence);
            }
        } else {
            trusted_local(&window)?;
        }
        let host = Arc::clone(host.inner());
        let (send, receive) = tokio::sync::oneshot::channel();
        let copy = app.clone();
        let source = original_authority.clone();
        app.run_on_main_thread(move || {
            let result = recheck_registered(&copy, &source).and_then(|()| {
                host.control(
                    &copy,
                    &window,
                    browser_host::Control {
                        profile: profile_id,
                        view_id,
                        action,
                        url,
                        prepared_revision,
                        bounds,
                    },
                )
            });
            let _ = send.send(result);
        })
        .map_err(|_| NativeFailure::SidecarFailed)?;
        receive.await.map_err(|_| NativeFailure::SidecarFailed)?
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
async fn browser_state(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    host: tauri::State<'_, Arc<browser_host::BrowserHost>>,
    profile_id: String,
    view_id: String,
) -> Result<browser_host::BrowserState, NativeFailure> {
    let response_window = window.clone();
    let original_authority = capture_authority(&response_window)?;
    let result = async {
        if is_local(&window) {
            trusted_local(&window)?
        } else {
            saved_binding(&window, &windows)?;
        };
        host.status(window.label(), &profile_id, &view_id)
    }
    .await;
    recheck_authority(&response_window, &original_authority)?;
    result
}

fn valid_local_runtime(endpoint: &str) -> bool {
    url::Url::parse(endpoint).is_ok_and(|u| {
        u.scheme() == "http"
            && u.host_str() == Some("127.0.0.1")
            && u.port().is_some_and(|p| p > 0)
            && u.origin().ascii_serialization() == endpoint
    })
}

fn run() -> Result<(), NativeFailure> {
    let mut args = std::env::args_os().skip(1);
    let root = match args.next() {
        None => default_data_root()?,
        Some(flag) if flag == "--data-dir" => {
            PathBuf::from(args.next().ok_or(NativeFailure::InvalidEvidence)?)
        }
        _ => return Err(NativeFailure::InvalidEvidence),
    };
    if args.next().is_some() || !root.is_absolute() {
        return Err(NativeFailure::InvalidEvidence);
    }
    let executable = std::env::current_exe().map_err(|_| NativeFailure::SidecarMissing)?;
    let connector = Arc::new(Connector::new(bundled_sidecar(&executable)?, root)?);
    let endpoint = connector.runtime_endpoint().inspect_err(|code| {
        use delidev_desktop::{
            language::{LanguagePreference, resolve},
            localization::{Message, text_in},
        };
        let locale = resolve(LanguagePreference::System, sys_locale::get_locale());
        let message = match *code {
            NativeFailure::Busy | NativeFailure::ServiceManaged => Message::StartupConflict,
            NativeFailure::Incompatible => Message::StartupIncompatible,
            _ => Message::StartupUnavailable,
        };
        rfd::MessageDialog::new()
            .set_title("DeliDev")
            .set_description(text_in(message, locale))
            .set_level(rfd::MessageLevel::Error)
            .show();
    })?;
    let mut context = tauri::generate_context!();
    let security = &mut context.config_mut().app.security;
    for (policy, development) in [(&mut security.csp, false), (&mut security.dev_csp, true)] {
        if let Some(source) = policy.take() {
            // Use exclusively the retained child's private hello, before any
            // Local window can acquire networking authority. Replace the
            // complete directive, including directive-map configurations.
            *policy = Some(local_csp(source, &endpoint, development)?);
        }
    }
    let supervision = Arc::new(Supervision::new(Arc::clone(&connector)));
    let worker_supervision = Arc::new(WorkerSupervision::new(
        Arc::clone(&connector),
        Arc::clone(&supervision),
    ));
    let quit_started = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let _lifetime = DesktopLifetime {
        connector: Arc::clone(&connector),
        supervision: Arc::clone(&supervision),
        worker_supervision: Arc::clone(&worker_supervision),
        quit_started: Arc::clone(&quit_started),
    };
    let tray = Arc::new(TrayHost::default());
    let notifications = Arc::new(NotificationHost::default());
    let oauth = Arc::new(delidev_desktop::oauth::OAuthHost::default());
    let browser_cache = connector.prepare_browser_storage()?;
    let storage_mode = BrowserStorageMode::current();
    let browser = Arc::new(browser_host::BrowserHost::new(
        browser_cache,
        Arc::clone(&connector),
        storage_mode,
    )?);
    tracing::info!(operation = "browser_storage", mode = ?storage_mode);
    if storage_mode == BrowserStorageMode::DevelopmentMock {
        tracing::warn!(
            operation = "browser_storage",
            code = "development-public-cookie-key",
            "Development browser cookies use a public test key and have no meaningful encryption \
             protection at rest."
        );
    }
    let app = tauri::Builder::<CefRuntime>::new()
        // Windows is an approved unsandboxed exception until upstream supports
        // Chromium's broker for executable hosts. Auto warns about that limit;
        // macOS/Linux require sandboxing. Cookie encryption follows the
        // compiled storage policy, independently of Chromium's sandbox.
        .runtime(
            tauri_runtime_cef::Cef::default()
                .sandbox(if cfg!(windows) {
                    tauri_runtime_cef::SandboxPolicy::Auto
                } else {
                    tauri_runtime_cef::SandboxPolicy::Required
                })
                .secret_storage(match storage_mode {
                    BrowserStorageMode::System => tauri_runtime_cef::SecretStorage::System,
                    BrowserStorageMode::DevelopmentMock => tauri_runtime_cef::SecretStorage::Mock,
                })
                .root_cache_path(browser.cache_root()),
        )
        .manage(Arc::new(UpdateHost::default()))
        .manage(Arc::clone(&browser))
        .manage(Arc::new(ProductWindows::default()))
        .manage(Arc::new(window_host::WindowActions::default()))
        .manage(Arc::clone(&tray))
        .manage(Arc::clone(&notifications))
        .manage(Arc::clone(&oauth))
        .manage(Arc::clone(&connector))
        .manage(Arc::clone(&supervision))
        .invoke_handler(tauri::generate_handler![
            account_oauth_native,
            desktop_credential_access,
            choose_repository_folder,
            read_appearance,
            read_session_creation_preferences,
            update_session_creation_preferences,
            update_appearance,
            read_language,
            update_language,
            open_browser,
            control_browser,
            browser_state,
            open_github,
            open_provider_guidance,
            connect_local,
            launch_local,
            retry_local,
            inspect_local_registration,
            recover_local_registration,
            local_server_status,
            local_worker_proof,
            local_worker_control,
            worker_network_control,
            desktop_update_context,
            desktop_update_native,
            connection_context,
            saved_connections,
            removed_connections,
            remove_connection,
            retained_worker_control,
            pair_connection,
            retry_connection,
            rename_connection,
            open_connection,
            connect_saved,
            saved_worker_proof,
            saved_worker_control,
            show_connection_manager,
            begin_tray,
            publish_tray,
            read_tray_action,
            acknowledge_tray_action,
            begin_notifications,
            end_notifications,
            notification_permission,
            request_notification_permission,
            present_notification
        ])
        .on_window_event(|window, event| {
            let windows = window.state::<Arc<ProductWindows>>();
            if matches!(event, WindowEvent::Focused(true))
                && let Ok(mut registry) = windows.registry.lock()
            {
                registry.focus(window.label());
            }
            if let WindowEvent::CloseRequested { api, .. } = event {
                let tray = window
                    .state::<Arc<TrayHost>>()
                    .available
                    .load(std::sync::atomic::Ordering::Acquire);
                let decision = windows
                    .registry
                    .lock()
                    .map_err(|_| NativeFailure::Busy)
                    .and_then(|mut registry| registry.begin_close(window.label(), tray));
                if matches!(decision, Ok(delidev_desktop::window_registry::Close::Hide))
                    && window.hide().is_ok()
                {
                    api.prevent_close();
                    return;
                }
                if decision.is_err() {
                    api.prevent_close();
                    return;
                }
                if matches!(decision, Ok(delidev_desktop::window_registry::Close::Hide)) {
                    tracing::warn!(operation = "window_hide", code = "window-unavailable");
                    if let Ok(mut registry) = windows.registry.lock() {
                        let _ = registry.begin_close(window.label(), false);
                    }
                }
                window
                    .state::<Arc<delidev_desktop::oauth::OAuthHost>>()
                    .close_window(window.label());
                if let Err(code) = window
                    .state::<Arc<browser_host::BrowserHost>>()
                    .close_window(window.label())
                {
                    api.prevent_close();
                    if let Ok(mut registry) = windows.registry.lock() {
                        registry.cancel_close(window.label());
                    }
                    tracing::warn!(operation = "browser_window_close", ?code);
                }
            } else if matches!(event, WindowEvent::Destroyed) {
                window
                    .state::<Arc<delidev_desktop::oauth::OAuthHost>>()
                    .close_window(window.label());
                if let Err(code) = window
                    .state::<Arc<browser_host::BrowserHost>>()
                    .close_window(window.label())
                {
                    tracing::warn!(operation = "browser_window_close", ?code);
                }
                window
                    .state::<Arc<NotificationHost>>()
                    .remove(window.label());
                tray_host::remove(window.app_handle(), window.label());
            }
        })
        .setup(|app| {
            let config_dir = app.path().app_config_dir().ok();
            if config_dir.is_none() {
                tracing::warn!(
                    operation = "appearance_initialize",
                    code = "storage-unavailable"
                );
            }
            app.manage(Arc::new(
                delidev_desktop::session_creation_preferences::Store::new(
                    app.path().app_config_dir().ok(),
                ),
            ));
            app.manage(Arc::new(delidev_desktop::appearance::AppearanceStore::new(
                config_dir.clone(),
            )));
            let language = Arc::new(delidev_desktop::language::LanguageStore::new(config_dir));
            let initial = language.read();
            delidev_desktop::language::activate(initial.resolved_language);
            if widget_host::set_language(initial.language).is_err() {
                tracing::warn!(
                    operation = "language_initialize",
                    code = "widget-unavailable"
                );
            }
            app.manage(language);
            let result = (|| -> Result<(), NativeFailure> {
                window_host::install_menu(app.handle())
                    .map_err(|_| NativeFailure::SidecarFailed)?;
                window_host::create(app.handle(), None, true)?;
                if app.state::<Arc<TrayHost>>().install(app.handle()).is_err() {
                    tracing::warn!(
                        operation = "tray_install",
                        code = "presentation-unavailable"
                    );
                }
                Ok(())
            })();
            if result.is_err() {
                tracing::error!(operation = "main_window", code = "window-unavailable");
                app.handle().exit(1);
            }
            Ok(())
        })
        .build(context)
        .map_err(|_| NativeFailure::SidecarFailed)?;
    let exiting_supervision = Arc::clone(&supervision);
    let exiting = Arc::clone(&tray);
    let exiting_notifications = Arc::clone(&notifications);
    browser.start(app.handle().clone());
    let exiting_browser = Arc::clone(&browser);
    let window_actions = Arc::clone(app.state::<Arc<window_host::WindowActions>>().inner());
    let quit_done = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let quit_task = Arc::new(Mutex::new(None));
    let joining_quit = Arc::clone(&quit_task);
    let exiting_window_actions = Arc::clone(&window_actions);
    let returning_oauth = Arc::clone(&oauth);
    app.run(move |_app, event| {
        if let tauri::RunEvent::ExitRequested { api, code, .. } = &event {
            _app.state::<Arc<window_host::WindowActions>>().stop();
            if let Ok(mut registry) = _app.state::<Arc<ProductWindows>>().registry.lock() {
                registry.stop();
            }
            let exit_code = code.unwrap_or(0);
            let browser_pending = exiting_browser.begin_exit(exit_code);
            if !quit_started.swap(true, std::sync::atomic::Ordering::AcqRel) {
                // Fence fresh starts synchronously. Browser discovery keeps its
                // separate observer until its final bounded read pass joins.
                exiting_supervision.request_stop();
                worker_supervision.request_stop();
                exiting.request_stop();
                let host = Arc::clone(&exiting_supervision);
                let workers = Arc::clone(&worker_supervision);
                let sidecar = Arc::clone(&connector);
                let browser = Arc::clone(&exiting_browser);
                let tray = Arc::clone(&exiting);
                let notifications = Arc::clone(&exiting_notifications);
                let oauth = Arc::clone(&oauth);
                let windows = Arc::clone(&exiting_window_actions);
                let complete = Arc::clone(&quit_done);
                let app = _app.clone();
                *quit_task.lock().unwrap_or_else(|e| e.into_inner()) =
                    Some(std::thread::spawn(move || {
                        oauth.stop();
                        windows.join();
                        host.stop();
                        workers.stop();
                        browser.stop();
                        if let Err(code) = sidecar.shutdown_owned() {
                            tracing::error!(
                                operation = "desktop_sidecar_shutdown",
                                phase = "quit-cleanup-failed",
                                ?code
                            );
                        }
                        notifications.stop();
                        tracing::info!(operation = "desktop_exit", state = "notifications-joined");
                        tray.stop();
                        tracing::info!(operation = "desktop_exit", state = "tray-joined");
                        complete.store(true, std::sync::atomic::Ordering::Release);
                        app.exit(exit_code);
                    }));
            }
            if browser_pending || !quit_done.load(std::sync::atomic::Ordering::Acquire) {
                api.prevent_exit();
            }
            tracing::info!(operation = "desktop_exit", state = "runtime-requested");
        }
        #[cfg(target_os = "macos")]
        if matches!(event, tauri::RunEvent::Reopen { .. }) {
            window_host::restore_recent(_app);
        }
        if matches!(event, tauri::RunEvent::Exit) {
            // This event precedes CEF shutdown. Keep host task joins and the
            // return from app.run separate so an exit event cannot imply that
            // the native runtime has actually finished.
            exiting_browser.close_all();
            tracing::info!(operation = "desktop_exit", state = "runtime-exit-event");
        }
    });
    tracing::info!(operation = "desktop_exit", state = "runtime-returned");
    window_actions.stop();
    window_actions.join();
    if let Some(task) = joining_quit
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .take()
    {
        let _ = task.join();
    }
    supervision.stop();
    returning_oauth.stop();
    browser.stop();
    notifications.stop();
    tray.stop();
    browser.finish_removals()?;
    Ok(())
}

#[tauri_runtime_cef::cef_entry_point]
fn main() {
    tracing_subscriber::fmt()
        .json()
        .with_target(false)
        .with_writer(std::io::stderr)
        .init();
    if let Err(code) = run() {
        tracing::error!(operation = "desktop_host", ?code);
        std::process::exit(1);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn local_csp_has_only_the_selected_runtime_origin() {
        for endpoint in ["http://127.0.0.1:51234", "http://127.0.0.1:62345"] {
            let original = Csp::Policy(
                "default-src 'none'; script-src 'self'; connect-src * http://127.0.0.1:46310"
                    .into(),
            );
            let map: HashMap<String, CspDirectiveSources> = original.into();
            let policy = local_csp(Csp::from(map), endpoint, false).unwrap();
            let directives: HashMap<String, CspDirectiveSources> = policy.into();
            let sources: Vec<String> = directives["connect-src"].clone().into();
            assert_eq!(sources, vec!["ipc:", "http://ipc.localhost", endpoint]);
            let scripts: Vec<String> = directives["script-src"].clone().into();
            assert_eq!(scripts, vec!["'self'"]);
        }
        assert!(
            local_csp(
                Csp::Policy("default-src 'none'".into()),
                "http://localhost:51234",
                false
            )
            .is_err()
        );
    }

    #[test]
    fn generated_oauth_acl_preserves_trusted_webview_boundaries() {
        use tauri::{
            ipc::{Origin, RuntimeAuthority},
            utils::{
                acl::{
                    APP_ACL_KEY, capability::Capability, manifest::Manifest, resolved::Resolved,
                },
                platform::Target,
            },
        };

        // Use the build's actual permission resolution. Handler-only tests
        // cannot detect commands removed or denied by Tauri's generated ACL.
        let manifests: BTreeMap<String, Manifest> = serde_json::from_str(include_str!(concat!(
            env!("OUT_DIR"),
            "/acl-manifests.json"
        )))
        .unwrap();
        let app = manifests.get(APP_ACL_KEY).unwrap();
        assert!(
            app.permissions.contains_key("allow-account-oauth-native")
                || app
                    .command_permission("allow-account-oauth-native", false)
                    .is_some()
        );
        assert_eq!(
            app.permissions["account-oauth"].commands.allow,
            ["account_oauth_native"]
        );
        let capabilities: BTreeMap<String, Capability> =
            serde_json::from_str(include_str!(concat!(env!("OUT_DIR"), "/capabilities.json")))
                .unwrap();
        let resolved = Resolved::resolve(&manifests, capabilities, Target::current()).unwrap();
        assert!(resolved.has_app_acl);
        assert!(
            resolved
                .allowed_commands
                .contains_key("account_oauth_native")
        );
        let authority = RuntimeAuthority::new(
            #[cfg(debug_assertions)]
            manifests,
            resolved,
        );
        for label in ["main", "server-fixture"] {
            assert!(
                authority
                    .resolve_access("account_oauth_native", label, label, &Origin::Local)
                    .is_some()
            );
            // A raw child in a trusted containing window inherits no access.
            assert!(
                authority
                    .resolve_access(
                        "account_oauth_native",
                        label,
                        "external-fixture",
                        &Origin::Local
                    )
                    .is_none()
            );
            assert!(
                authority
                    .resolve_access(
                        "account_oauth_native",
                        label,
                        label,
                        &Origin::Remote {
                            url: "https://openrouter.ai/".parse().unwrap()
                        }
                    )
                    .is_none()
            );
        }
        assert!(
            authority
                .resolve_access(
                    "account_oauth_native",
                    "external-fixture",
                    "external-fixture",
                    &Origin::Local
                )
                .is_none()
        );
    }

    #[test]
    fn generated_credential_access_acl_is_local_only() {
        use tauri::{
            ipc::{Origin, RuntimeAuthority},
            utils::{
                acl::{capability::Capability, manifest::Manifest, resolved::Resolved},
                platform::Target,
            },
        };
        let manifests: BTreeMap<String, Manifest> = serde_json::from_str(include_str!(concat!(
            env!("OUT_DIR"),
            "/acl-manifests.json"
        )))
        .unwrap();
        let capabilities: BTreeMap<String, Capability> =
            serde_json::from_str(include_str!(concat!(env!("OUT_DIR"), "/capabilities.json")))
                .unwrap();
        let resolved = Resolved::resolve(&manifests, capabilities, Target::current()).unwrap();
        let authority = RuntimeAuthority::new(
            #[cfg(debug_assertions)]
            manifests,
            resolved,
        );
        for label in ["main", "local-fixture"] {
            assert!(
                authority
                    .resolve_access("desktop_credential_access", label, label, &Origin::Local)
                    .is_some()
            );
            assert!(
                authority
                    .resolve_access(
                        "desktop_credential_access",
                        label,
                        "external-child",
                        &Origin::Local
                    )
                    .is_none()
            );
        }
        assert!(
            authority
                .resolve_access(
                    "desktop_credential_access",
                    "server-fixture",
                    "server-fixture",
                    &Origin::Local
                )
                .is_none()
        );
        assert!(
            authority
                .resolve_access(
                    "desktop_credential_access",
                    "main",
                    "main",
                    &Origin::Remote {
                        url: "https://example.invalid/".parse().unwrap()
                    }
                )
                .is_none()
        );
    }

    #[test]
    fn saved_policy_replaces_only_the_exact_connection_source() {
        let original = "default-src 'none'; script-src 'self' 'sha256-fixed'; connect-src ipc: http://ipc.localhost http://127.0.0.1:46310; frame-src 'none'";
        let result = saved_csp(original, "https://selected.example.test").unwrap();
        let directives: HashMap<String, CspDirectiveSources> = Csp::Policy(result.clone()).into();
        let sources: Vec<String> = directives.get("connect-src").unwrap().clone().into();
        assert_eq!(
            sources,
            vec![
                "ipc:",
                "http://ipc.localhost",
                "https://selected.example.test"
            ]
        );
        assert!(result.contains("'sha256-fixed'"));
        assert!(result.contains("frame-src 'none'"));
        assert!(!result.contains("46310"));
        assert!(saved_csp(original, "https://selected.example.test; connect-src *").is_err());
        assert!(saved_csp(original, "https://user:secret@selected.example.test").is_err());
    }
    #[test]
    fn only_local_entry_documents_receive_native_capabilities() {
        for value in [
            "tauri://localhost",
            "tauri://localhost/",
            "tauri://localhost/index.html#main",
            "http://tauri.localhost/",
        ] {
            assert!(trusted_url(&value.parse().unwrap()), "{value}");
        }
        for value in [
            "https://example.com/",
            "http://tauri.localhost:81/",
            "tauri://other/",
            "tauri://localhost/private",
            "tauri://localhost/?token=bad",
            "tauri://user@localhost/",
        ] {
            assert!(!trusted_url(&value.parse().unwrap()), "{value}");
        }
    }
}

#[cfg(test)]
mod product_window_tests {
    use delidev_desktop::window_registry::Role;

    use super::*;

    fn saved(windows: &ProductWindows, id: &str) -> WindowAuthority {
        let mut registry = windows.registry.lock().unwrap();
        let entry = registry.reserve(Role::Saved(id.into()), false).unwrap();
        registry.ready(&entry).unwrap();
        let profile = SavedConnection {
            version: 1,
            revision: 1,
            id: id.into(),
            name: "Original".into(),
            endpoint: "https://fixture.test".into(),
            server_id: "server".into(),
            pairing_id: "pairing".into(),
            device_id: "device".into(),
            state: SavedConnectionState::Paired,
            created_at: "fixture".into(),
            removal: None,
        };
        windows.bindings.lock().unwrap().insert(
            entry.label.clone(),
            SavedBinding {
                profile: profile.clone(),
                instance: entry.instance.clone(),
                closing: false,
            },
        );
        WindowAuthority {
            entry,
            local_revision: registry.local_revision,
            saved: Some(profile),
        }
    }

    #[test]
    fn profile_barrier_denies_all_siblings_but_preserves_other_connections() {
        let windows = ProductWindows::default();
        let first = saved(&windows, "profile");
        let second = saved(&windows, "profile");
        let other = saved(&windows, "other");
        assert!(recheck_record(&windows, &first).is_ok());
        windows.removals.lock().unwrap().insert(
            "profile".into(),
            SavedBinding {
                profile: first.saved.clone().unwrap(),
                instance: "removal".into(),
                closing: true,
            },
        );
        assert!(recheck_record(&windows, &first).is_err());
        assert!(recheck_record(&windows, &second).is_err());
        assert!(recheck_record(&windows, &other).is_ok());
    }

    #[test]
    fn response_fences_reject_closing_instances_and_changed_authority() {
        let windows = ProductWindows::default();
        let first = saved(&windows, "profile");
        let second = saved(&windows, "profile");
        windows
            .registry
            .lock()
            .unwrap()
            .begin_close(&first.entry.label, true)
            .unwrap();
        assert!(recheck_record(&windows, &first).is_err());
        assert!(recheck_record(&windows, &second).is_ok());
        windows
            .bindings
            .lock()
            .unwrap()
            .get_mut(&second.entry.label)
            .unwrap()
            .profile
            .device_id = "replacement".into();
        assert!(recheck_record(&windows, &second).is_err());
        let mut forged = second;
        forged.entry.role = Role::Local;
        forged.saved = None;
        assert!(recheck_record(&windows, &forged).is_err());
    }

    #[test]
    fn rename_updates_every_sibling_without_rolling_back_newer_names() {
        let windows = ProductWindows::default();
        let first = saved(&windows, "profile");
        let second = saved(&windows, "profile");
        let other = saved(&windows, "other");
        let mut renamed = first.saved.clone().unwrap();
        renamed.revision = 3;
        renamed.name = "Renamed".into();
        apply_saved_label(&windows, renamed).unwrap();
        let original = first.saved.unwrap();
        let stale = apply_saved_label(&windows, original.clone()).unwrap();
        assert_eq!(stale.revision, 3);
        let values = windows.bindings.lock().unwrap();
        let queued = reconcile_saved_label(&values, original).unwrap();
        assert_eq!(queued.name, "Renamed");
        assert_eq!(queued.revision, 3);
        assert_eq!(values[&second.entry.label].profile.name, "Renamed");
        assert_eq!(values[&other.entry.label].profile.name, "Original");
    }

    #[test]
    fn saved_csp_retains_script_limits_and_only_exact_connection_origin() {
        let policy = saved_csp(
            "default-src 'self'; script-src 'self'; connect-src http://127.0.0.1:46310",
            "https://fixture.test",
        )
        .unwrap();
        assert!(policy.contains("script-src 'self'"));
        assert!(policy.contains("https://fixture.test"));
        assert!(!policy.contains("46310"));
        assert!(saved_csp("default-src 'self'", "https://fixture.test/path").is_err());
    }
}
