#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod browser_host;
mod notification_host;
mod tray_host;
mod widget_host;
use std::{
    collections::{BTreeMap, HashMap},
    path::PathBuf,
    sync::{Arc, Mutex},
};

use cef::{ImplBrowser, ImplBrowserHost};
use delidev_desktop::{
    Connection, Connector, DesktopRegistration, LocalServerStatus, LocalWorkerAction,
    LocalWorkerProof, LocalWorkerStatus, NativeFailure, RemovedConnections, SavedConnection,
    SavedConnectionState, Supervision, bundled_sidecar, canonical_id, connection_origin,
    default_data_root,
};
use notification_host::{
    NotificationHost, begin_notifications, end_notifications, notification_permission,
    present_notification, request_notification_permission,
};
use tauri::{
    AppHandle, Cef, Emitter, Manager, WebviewWindow, WebviewWindowBuilder, WindowEvent,
    utils::config::{Csp, CspDirectiveSources, WebviewUrl},
    webview::NewWindowResponse,
};
use tray_host::{TrayHost, acknowledge_tray_action, begin_tray, publish_tray, read_tray_action};

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

#[tauri::command]
async fn open_github(
    window: WebviewWindow<Cef>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
    connector: tauri::State<'_, Arc<Connector>>,
    url: String,
) -> Result<(), NativeFailure> {
    if window.label() == "main" {
        trusted_main(&window)?;
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

#[tauri::command]
async fn connect_local(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    supervision: tauri::State<'_, Arc<Supervision>>,
) -> Result<Connection, NativeFailure> {
    if window.label() != "main"
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
    if connection.endpoint != "http://127.0.0.1:46310" {
        return Err(NativeFailure::Incompatible);
    }
    Ok(connection)
}

#[tauri::command]
async fn inspect_local_registration(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
) -> Result<DesktopRegistration, NativeFailure> {
    trusted_main(&window)?;
    let connector = Arc::clone(connector.inner());
    tauri::async_runtime::spawn_blocking(move || connector.inspect_desktop_registration())
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?
}

#[tauri::command]
async fn recover_local_registration(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    device_id: String,
    revision: String,
    request_id: String,
) -> Result<Connection, NativeFailure> {
    trusted_main(&window)?;
    let connector = Arc::clone(connector.inner());
    let connection = tauri::async_runtime::spawn_blocking(move || {
        connector.recover_desktop_registration(&device_id, &revision, &request_id)
    })
    .await
    .map_err(|_| NativeFailure::SidecarFailed)??;
    if connection.endpoint != "http://127.0.0.1:46310" {
        return Err(NativeFailure::Incompatible);
    }
    Ok(connection)
}

#[tauri::command]
async fn local_server_status(
    window: WebviewWindow<Cef>,
    supervision: tauri::State<'_, Arc<Supervision>>,
) -> Result<LocalServerStatus, NativeFailure> {
    if window.label() != "main"
        || !trusted_url(&window.url().map_err(|_| NativeFailure::PermissionDenied)?)
    {
        return Err(NativeFailure::PermissionDenied);
    }
    Ok(supervision.status())
}

#[tauri::command]
async fn local_worker_proof(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
) -> Result<LocalWorkerProof, NativeFailure> {
    if window.label() != "main"
        || !trusted_url(&window.url().map_err(|_| NativeFailure::PermissionDenied)?)
    {
        return Err(NativeFailure::PermissionDenied);
    }
    let connector = Arc::clone(connector.inner());
    let proof = tauri::async_runtime::spawn_blocking(move || connector.local_worker_proof())
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
    if proof.endpoint != "http://127.0.0.1:46310" {
        return Err(NativeFailure::Incompatible);
    }
    Ok(proof)
}

#[tauri::command]
async fn local_worker_control(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    action: LocalWorkerAction,
    generation: Option<String>,
) -> Result<LocalWorkerStatus, NativeFailure> {
    if window.label() != "main"
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

#[derive(Clone)]
struct SavedBinding {
    profile: SavedConnection,
    instance: String,
    closing: bool,
}
#[derive(Default)]
struct SavedWindows(Mutex<BTreeMap<String, SavedBinding>>);

// CEF URL getters enqueue work on its UI loop and wait for a response. Every
// command reaching this check must execute asynchronously off that loop; a
// synchronous IPC handler can deadlock both the window and application quit.
// Keep this boundary while the pinned runtime uses blocking URL getters.
fn trusted_main(window: &WebviewWindow<Cef>) -> Result<(), NativeFailure> {
    if window.label() != "main"
        || !trusted_url(&window.url().map_err(|_| NativeFailure::PermissionDenied)?)
    {
        return Err(NativeFailure::PermissionDenied);
    }
    Ok(())
}
fn saved_binding(
    window: &WebviewWindow<Cef>,
    windows: &SavedWindows,
) -> Result<SavedBinding, NativeFailure> {
    let url = window.url().map_err(|_| NativeFailure::PermissionDenied)?;
    // Saved credentials never reach an external development-server document.
    if !trusted_url(&url) || !(url.scheme() == "tauri" || url.host_str() == Some("tauri.localhost"))
    {
        return Err(NativeFailure::PermissionDenied);
    }
    // Native commands may run on the event loop while label publication owns
    // this map and waits for that loop. Return Busy instead of deadlocking it.
    windows
        .0
        .try_lock()
        .map_err(|_| NativeFailure::Busy)?
        .get(window.label())
        .filter(|binding| !binding.closing)
        .cloned()
        .ok_or(NativeFailure::PermissionDenied)
}
#[tauri::command]
async fn connection_context(
    window: WebviewWindow<Cef>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
) -> Result<Option<SavedConnection>, NativeFailure> {
    if window.label() == "main" {
        trusted_main(&window)?;
        return Ok(None);
    }
    Ok(Some(saved_binding(&window, &windows)?.profile))
}
#[tauri::command]
async fn saved_connections(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
) -> Result<Vec<SavedConnection>, NativeFailure> {
    trusted_main(&window)?;
    let connector = Arc::clone(connector.inner());
    tauri::async_runtime::spawn_blocking(move || connector.saved_connections())
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?
}
#[tauri::command]
async fn removed_connections(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    after: String,
) -> Result<RemovedConnections, NativeFailure> {
    trusted_main(&window)?;
    let connector = Arc::clone(connector.inner());
    tauri::async_runtime::spawn_blocking(move || connector.removed_connections(&after))
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?
}
#[tauri::command]
async fn retained_worker_control(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    id: String,
    action: LocalWorkerAction,
    generation: Option<String>,
) -> Result<LocalWorkerStatus, NativeFailure> {
    trusted_main(&window)?;
    let connector = Arc::clone(connector.inner());
    tauri::async_runtime::spawn_blocking(move || {
        connector.retained_worker(&id, action, generation.as_deref())
    })
    .await
    .map_err(|_| NativeFailure::SidecarFailed)?
}
#[tauri::command]
async fn remove_connection(
    window: WebviewWindow<Cef>,
    app: AppHandle<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    browser: tauri::State<'_, Arc<browser_host::BrowserHost>>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
    id: String,
    request_id: String,
    revision: u64,
) -> Result<SavedConnection, NativeFailure> {
    trusted_main(&window)?;
    canonical_id(&id)?;
    canonical_id(&request_id)?;
    let connector = Arc::clone(connector.inner());
    let lookup = Arc::clone(&connector);
    let profile_id = id.clone();
    let profile = tauri::async_runtime::spawn_blocking(move || lookup.inspect_saved(&profile_id))
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
    // Avoid closing a window from a stale confirmation. Go independently
    // validates and claims the exact mutation at its durable commit boundary.
    if let Some(removal) = &profile.removal {
        if removal.request_id != request_id || removal.expected_revision != revision {
            return Err(NativeFailure::InvalidEvidence);
        }
    } else if profile.revision != revision {
        return Err(NativeFailure::InvalidEvidence);
    }
    // This durable local scope survives credential deletion and denies older
    // in-flight browser opens. Purging waits for independently completed CEF
    // shutdown.
    let host = Arc::clone(browser.inner());
    let scope = profile.clone();
    tauri::async_runtime::spawn_blocking(move || host.prepare_forget(&scope))
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
    let host = Arc::clone(browser.inner());
    let scope = profile.clone();
    app.run_on_main_thread(move || host.close_scope(&scope))
        .map_err(|_| NativeFailure::SidecarFailed)?;
    let label = format!("server-{id}");
    let instance = uuid::Uuid::now_v7().to_string();
    let original = {
        let mut values = windows.0.lock().map_err(|_| NativeFailure::Busy)?;
        if let Some(binding) = values.get(&label).filter(|binding| binding.closing) {
            let retry = profile.state == SavedConnectionState::Removing
                && binding.profile.state == SavedConnectionState::Removing
                && binding.profile.removal == profile.removal;
            if profile.state != SavedConnectionState::Removed && !retry {
                return Err(NativeFailure::Busy);
            }
        }
        values.insert(
            label.clone(),
            SavedBinding {
                profile,
                instance: instance.clone(),
                closing: true,
            },
        )
    };
    // The explicit UI confirmation includes losing this window's unsent drafts.
    // Block reopen and late token delivery before beginning private cleanup.
    if let Some(saved) = app.get_webview_window(&label)
        && saved.destroy().is_err()
    {
        let mut values = windows.0.lock().map_err(|_| NativeFailure::SidecarFailed)?;
        if let Some(original) = original {
            values.insert(label, original);
        } else {
            values.remove(&label);
        }
        return Err(NativeFailure::SidecarFailed);
    }
    let retry_lookup = Arc::clone(&connector);
    let retry_id = id.clone();
    let retry_request = request_id.clone();
    let result = tauri::async_runtime::spawn_blocking(move || {
        connector.remove_saved(&id, &request_id, revision)
    })
    .await
    .map_err(|_| NativeFailure::SidecarFailed)
    .and_then(|value| value);
    // Keep the barrier even when cleanup fails: Go may already have committed
    // the removal marker, and an in-flight open can hold older paired metadata.
    // Re-inspect an uncertain failure so its exact durable cleanup retry can
    // replace this barrier without admitting a stale open.
    if result.is_err()
        && let Ok(Ok(observed)) =
            tauri::async_runtime::spawn_blocking(move || retry_lookup.inspect_saved(&retry_id))
                .await
        && observed.state == SavedConnectionState::Removing
        && observed.removal.as_ref().is_some_and(|removal| {
            removal.request_id == retry_request && removal.expected_revision == revision
        })
    {
        let mut values = windows.0.lock().map_err(|_| NativeFailure::SidecarFailed)?;
        if let Some(binding) = values.get_mut(&label)
            && binding.instance == instance
        {
            binding.profile = observed;
        }
    }
    if result
        .as_ref()
        .is_ok_and(|profile| profile.state == SavedConnectionState::Removed)
    {
        // Credential removal remains authoritative even if presentation cleanup
        // fails. The stale metadata grants no connection or execution authority.
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
#[tauri::command]
async fn pair_connection(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    id: String,
    name: String,
    grant: String,
) -> Result<SavedConnection, NativeFailure> {
    let grant = zeroize::Zeroizing::new(grant.into_bytes());
    trusted_main(&window)?;
    let connector = Arc::clone(connector.inner());
    tauri::async_runtime::spawn_blocking(move || connector.pair_saved(&id, &name, grant))
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?
}
#[tauri::command]
async fn retry_connection(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    id: String,
) -> Result<SavedConnection, NativeFailure> {
    trusted_main(&window)?;
    let connector = Arc::clone(connector.inner());
    tauri::async_runtime::spawn_blocking(move || connector.retry_saved(&id))
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?
}
// Tauri maps these separate argument names into the renderer IPC contract.
// Remove this exception when the command and callers use a validated request
// object.
#[expect(clippy::too_many_arguments)]
#[tauri::command]
async fn rename_connection(
    window: WebviewWindow<Cef>,
    app: AppHandle<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
    id: String,
    request_id: String,
    revision: u64,
    name: String,
) -> Result<SavedConnection, NativeFailure> {
    trusted_main(&window)?;
    let connector = Arc::clone(connector.inner());
    let profile = tauri::async_runtime::spawn_blocking(move || {
        connector.rename_saved(&id, &request_id, revision, &name)
    })
    .await
    .map_err(|_| NativeFailure::SidecarFailed)??;
    update_saved_label(&app, &windows, profile)
}

fn update_saved_label(
    app: &AppHandle<Cef>,
    windows: &SavedWindows,
    mut profile: SavedConnection,
) -> Result<SavedConnection, NativeFailure> {
    let label = format!("server-{}", profile.id);
    // Serialize presentation with the binding, including title publication.
    // Out-of-order accepted edits cannot roll back a newer window label.
    let mut values = windows.0.lock().map_err(|_| NativeFailure::SidecarFailed)?;
    if let Some(binding) = values.get_mut(&label) {
        if binding.closing || !binding.profile.same_authority(&profile) {
            return Err(NativeFailure::InvalidEvidence);
        }
        if profile.revision >= binding.profile.revision {
            binding.profile = profile.clone();
        } else {
            profile = binding.profile.clone();
        }
        if let Some(saved) = app.get_webview_window(&label) {
            saved
                .set_title(&format!("DeliDev · {}", profile.name))
                .map_err(|_| NativeFailure::SidecarFailed)?;
            // No selectable identity or credential crosses this notification.
            saved
                .emit("saved-connection-label", ())
                .map_err(|_| NativeFailure::SidecarFailed)?;
        }
    }
    Ok(profile)
}

#[tauri::command]
async fn connect_saved(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
) -> Result<Connection, NativeFailure> {
    let binding = saved_binding(&window, &windows)?;
    let connector = Arc::clone(connector.inner());
    let expected = binding.profile.clone();
    let result = tauri::async_runtime::spawn_blocking(move || connector.connect_saved(&expected))
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
#[tauri::command]
async fn saved_worker_proof(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
) -> Result<LocalWorkerProof, NativeFailure> {
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
#[tauri::command]
async fn saved_worker_control(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
    action: LocalWorkerAction,
    generation: Option<String>,
) -> Result<LocalWorkerStatus, NativeFailure> {
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
fn show(window: &WebviewWindow<Cef>) -> Result<(), NativeFailure> {
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
// The pinned CEF runtime applies macOS accessibility notifications only to
// browsers that already exist. Enable each trusted document explicitly so a
// saved-server window created afterward also exposes its semantic content.
// Remove this workaround when the runtime carries accessibility state forward
// to every newly created browser; no content or accessibility tree is logged.
fn enable_document_accessibility(window: &WebviewWindow<Cef>) {
    if window
        .with_webview(|view| {
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
fn create_main(app: &AppHandle<Cef>) -> tauri::Result<WebviewWindow<Cef>> {
    let config = &app.config().app.windows[0];
    WebviewWindowBuilder::from_config(app, config)?
        .incognito(true)
        .on_navigation(|url| {
            let allowed = trusted_url(url);
            tracing::info!(operation = "main_navigation", allowed);
            allowed
        })
        .on_page_load(|window, payload| {
            tracing::info!(operation="main_page",phase=?payload.event());
            if payload.event() == tauri::webview::PageLoadEvent::Finished {
                enable_document_accessibility(&window);
            }
        })
        .on_new_window(|_, _| NewWindowResponse::Deny)
        .build()
}
#[tauri::command]
async fn show_connection_manager(
    window: WebviewWindow<Cef>,
    app: AppHandle<Cef>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
) -> Result<(), NativeFailure> {
    if window.label() == "main" {
        trusted_main(&window)?;
    } else {
        saved_binding(&window, &windows)?;
    }
    if let Some(main) = app.get_webview_window("main") {
        show(&main)
    } else {
        create_main(&app)
            .map(|_| ())
            .map_err(|_| NativeFailure::SidecarFailed)
    }
}
#[tauri::command]
async fn open_connection(
    window: WebviewWindow<Cef>,
    app: AppHandle<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
    id: String,
) -> Result<(), NativeFailure> {
    trusted_main(&window)?;
    canonical_id(&id)?;
    // Tauri's resource callback does not run for an external development URL.
    // Require bundled assets before creating a credential-bearing saved window.
    if !cfg!(feature = "custom-protocol") {
        return Err(NativeFailure::Incompatible);
    }
    let connector = Arc::clone(connector.inner());
    let lookup = id.clone();
    let profile = tauri::async_runtime::spawn_blocking(move || connector.inspect_saved(&lookup))
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
    if profile.state != SavedConnectionState::Paired {
        return Err(NativeFailure::InvalidEvidence);
    }
    let label = format!("server-{id}");
    if let Some(existing) = app.get_webview_window(&label) {
        let binding = saved_binding(&existing, &windows)?;
        if !binding.profile.same_authority(&profile) {
            return Err(NativeFailure::InvalidEvidence);
        }
        update_saved_label(&app, &windows, profile)?;
        return show(&existing);
    }
    let title = format!("DeliDev · {}", profile.name);
    let origin = connection_origin(&profile.endpoint)?;
    let instance = uuid::Uuid::now_v7().to_string();
    {
        let mut values = windows.0.lock().map_err(|_| NativeFailure::Busy)?;
        if values.contains_key(&label) {
            return Err(NativeFailure::Busy);
        }
        values.insert(
            label.clone(),
            SavedBinding {
                profile,
                instance: instance.clone(),
                closing: false,
            },
        );
    }
    let created = WebviewWindowBuilder::new(&app, &label, WebviewUrl::App("index.html".into()))
        .title(title)
        .inner_size(1280.0, 820.0)
        .min_inner_size(960.0, 640.0)
        .decorations(true)
        .maximized(true)
        .incognito(true)
        .on_navigation(|url| {
            trusted_url(url)
                && (url.scheme() == "tauri" || url.host_str() == Some("tauri.localhost"))
        })
        .on_new_window(|_, _| NewWindowResponse::Deny)
        .on_page_load(|window, payload| {
            if payload.event() == tauri::webview::PageLoadEvent::Finished {
                enable_document_accessibility(&window);
            }
        })
        .on_web_resource_request(move |_, response| {
            if let Some(header) = response.headers_mut().get_mut("Content-Security-Policy") {
                let policy = header
                    .to_str()
                    .ok()
                    .and_then(|value| saved_csp(value, &origin).ok());
                let value = policy.as_deref().unwrap_or("default-src 'none'");
                *header = tauri::http::HeaderValue::from_str(value).unwrap_or_else(|_| {
                    tauri::http::HeaderValue::from_static("default-src 'none'")
                });
            }
        })
        .build();
    match created {
        Ok(window) => {
            let windows = Arc::clone(windows.inner());
            window.on_window_event(move |event| {
                if matches!(event, WindowEvent::Destroyed)
                    && let Ok(mut values) = windows.0.lock()
                    && values
                        .get(&label)
                        .is_some_and(|binding| binding.instance == instance && !binding.closing)
                {
                    values.remove(&label);
                }
            });
            tracing::info!(operation = "saved_window", state = "opened");
            tray_host::schedule(&app);
            Ok(())
        }
        Err(_) => {
            if let Ok(mut values) = windows.0.lock() {
                values.remove(&label);
            }
            Err(NativeFailure::SidecarFailed)
        }
    }
}

#[tauri::command]
async fn open_browser(
    window: WebviewWindow<Cef>,
    app: AppHandle<Cef>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
    connector: tauri::State<'_, Arc<Connector>>,
    host: tauri::State<'_, Arc<browser_host::BrowserHost>>,
    profile_id: String,
    view_id: String,
    url: String,
    bounds: browser_host::Bounds,
) -> Result<browser_host::BrowserState, NativeFailure> {
    let binding = if window.label() == "main" {
        trusted_main(&window)?;
        None
    } else {
        Some(saved_binding(&window, &windows)?)
    };
    let scope = binding.as_ref().map(|b| b.profile.clone());
    host.reserve(window.label(), &view_id)?;
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
        trusted_main(&window)?;
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
        trusted_main(&window)?;
    }
    let host = Arc::clone(host.inner());
    let (send, receive) = tokio::sync::oneshot::channel();
    let copy = app.clone();
    app.run_on_main_thread(move || {
        let _ = send.send(host.open(&copy, &window, scope, record, bounds, view_id));
    })
    .map_err(|_| NativeFailure::SidecarFailed)?;
    receive.await.map_err(|_| NativeFailure::SidecarFailed)?
}
#[tauri::command]
async fn control_browser(
    window: WebviewWindow<Cef>,
    app: AppHandle<Cef>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
    connector: tauri::State<'_, Arc<Connector>>,
    host: tauri::State<'_, Arc<browser_host::BrowserHost>>,
    profile_id: String,
    view_id: String,
    action: browser_host::Action,
    url: Option<String>,
    tab_id: Option<String>,
    bounds: Option<browser_host::Bounds>,
) -> Result<browser_host::BrowserState, NativeFailure> {
    let binding = if window.label() == "main" {
        trusted_main(&window)?;
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
        let record =
            tauri::async_runtime::spawn_blocking(move || c.browser_profile(scope.as_ref(), &id))
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
        trusted_main(&window)?;
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
        trusted_main(&window)?;
    }
    let host = Arc::clone(host.inner());
    let (send, receive) = tokio::sync::oneshot::channel();
    let copy = app.clone();
    app.run_on_main_thread(move || {
        let _ = send.send(host.control(
            &copy,
            &window,
            &profile_id,
            &view_id,
            action,
            url,
            prepared_revision,
            bounds,
        ));
    })
    .map_err(|_| NativeFailure::SidecarFailed)?;
    receive.await.map_err(|_| NativeFailure::SidecarFailed)?
}
#[tauri::command]
async fn browser_state(
    window: WebviewWindow<Cef>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
    host: tauri::State<'_, Arc<browser_host::BrowserHost>>,
    profile_id: String,
    view_id: String,
) -> Result<browser_host::BrowserState, NativeFailure> {
    if window.label() == "main" {
        trusted_main(&window)?
    } else {
        saved_binding(&window, &windows)?;
    };
    host.status(window.label(), &profile_id, &view_id)
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
    let supervision = Arc::new(Supervision::new(Arc::clone(&connector)));
    let tray = Arc::new(TrayHost::default());
    let notifications = Arc::new(NotificationHost::default());
    let browser_cache = connector.prepare_browser_storage()?;
    let browser = Arc::new(browser_host::BrowserHost::new(
        browser_cache.clone(),
        Arc::clone(&connector),
    )?);
    let app = tauri::Builder::<Cef>::new()
        .root_cache_path(&browser_cache)
        .manage(Arc::clone(&browser))
        .manage(Arc::new(SavedWindows::default()))
        .manage(Arc::clone(&tray))
        .manage(Arc::clone(&notifications))
        .manage(connector)
        .manage(Arc::clone(&supervision))
        .invoke_handler(tauri::generate_handler![
            open_browser,
            control_browser,
            browser_state,
            open_github,
            connect_local,
            inspect_local_registration,
            recover_local_registration,
            local_server_status,
            local_worker_proof,
            local_worker_control,
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
            if let WindowEvent::CloseRequested { api, .. } = event {
                if window
                    .state::<Arc<TrayHost>>()
                    .available
                    .load(std::sync::atomic::Ordering::Acquire)
                {
                    if window.hide().is_ok() {
                        api.prevent_close();
                    } else {
                        tracing::warn!(operation = "window_hide", code = "window-unavailable");
                    }
                }
            } else if matches!(event, WindowEvent::Destroyed) {
                window
                    .state::<Arc<NotificationHost>>()
                    .remove(window.label());
                tray_host::remove(window.app_handle(), window.label());
            }
        })
        .setup(|app| {
            let result = (|| -> tauri::Result<()> {
                create_main(app.handle())?;
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
        .build(tauri::generate_context!())
        .map_err(|_| NativeFailure::SidecarFailed)?;
    let exiting = Arc::clone(&tray);
    let exiting_notifications = Arc::clone(&notifications);
    browser.start(app.handle().clone());
    let exiting_browser = Arc::clone(&browser);
    app.run(move |_app, event| {
        if let tauri::RunEvent::ExitRequested { api, code, .. } = &event {
            if exiting_browser.begin_exit(code.unwrap_or(0)) {
                api.prevent_exit();
            }
            tracing::info!(operation = "desktop_exit", state = "runtime-requested");
        }
        #[cfg(target_os = "macos")]
        if matches!(event, tauri::RunEvent::Reopen { .. }) {
            // macOS app/Dock reopening must reveal the retained main window
            // after close-to-tray. Reuse the same restoration as tray actions;
            // creating a replacement would discard the renderer's drafts.
            if _app
                .get_webview_window("main")
                .is_none_or(|window| show(&window).is_err())
            {
                tracing::warn!(operation = "window_reopen", code = "window-unavailable");
            }
        }
        if matches!(event, tauri::RunEvent::Exit) {
            // This event precedes CEF shutdown. Keep host task joins and the
            // return from app.run separate so an exit event cannot imply that
            // the native runtime has actually finished.
            exiting_browser.stop();
            exiting_browser.close_all();
            tracing::info!(operation = "desktop_exit", state = "runtime-exit-event");
            exiting_notifications.stop();
            tracing::info!(operation = "desktop_exit", state = "notifications-joined");
            exiting.stop();
            tracing::info!(operation = "desktop_exit", state = "tray-joined");
        }
    });
    tracing::info!(operation = "desktop_exit", state = "runtime-returned");
    notifications.stop();
    tray.stop();
    browser.finish_removals()?;
    Ok(())
}

#[tauri::cef_entry_point]
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
