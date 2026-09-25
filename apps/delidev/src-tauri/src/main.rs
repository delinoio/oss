#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::{
    collections::{BTreeMap, HashMap},
    path::PathBuf,
    sync::{Arc, Mutex},
};

use delidev_desktop::{
    Connection, Connector, LocalServerStatus, LocalWorkerAction, LocalWorkerProof,
    LocalWorkerStatus, NativeFailure, SavedConnection, SavedConnectionState, Supervision,
    bundled_sidecar, canonical_id, connection_origin, default_data_root,
};
use tauri::{
    AppHandle, Emitter, Manager, WebviewWindow, WebviewWindowBuilder, WindowEvent, Wry,
    utils::config::{Csp, CspDirectiveSources, WebviewUrl},
    webview::NewWindowResponse,
};

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
async fn connect_local(
    window: WebviewWindow<Wry>,
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
fn local_server_status(
    window: WebviewWindow<Wry>,
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
    window: WebviewWindow<Wry>,
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
    window: WebviewWindow<Wry>,
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
}
#[derive(Default)]
struct SavedWindows(Mutex<BTreeMap<String, SavedBinding>>);

fn trusted_main(window: &WebviewWindow<Wry>) -> Result<(), NativeFailure> {
    if window.label() != "main"
        || !trusted_url(&window.url().map_err(|_| NativeFailure::PermissionDenied)?)
    {
        return Err(NativeFailure::PermissionDenied);
    }
    Ok(())
}
fn saved_binding(
    window: &WebviewWindow<Wry>,
    windows: &SavedWindows,
) -> Result<SavedBinding, NativeFailure> {
    let url = window.url().map_err(|_| NativeFailure::PermissionDenied)?;
    // Saved credentials never reach an external development-server document.
    if !trusted_url(&url) || !(url.scheme() == "tauri" || url.host_str() == Some("tauri.localhost"))
    {
        return Err(NativeFailure::PermissionDenied);
    }
    windows
        .0
        .lock()
        .map_err(|_| NativeFailure::Busy)?
        .get(window.label())
        .cloned()
        .ok_or(NativeFailure::PermissionDenied)
}
#[tauri::command]
fn connection_context(
    window: WebviewWindow<Wry>,
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
    window: WebviewWindow<Wry>,
    connector: tauri::State<'_, Arc<Connector>>,
) -> Result<Vec<SavedConnection>, NativeFailure> {
    trusted_main(&window)?;
    let connector = Arc::clone(connector.inner());
    tauri::async_runtime::spawn_blocking(move || connector.saved_connections())
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?
}
#[tauri::command]
async fn pair_connection(
    window: WebviewWindow<Wry>,
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
    window: WebviewWindow<Wry>,
    connector: tauri::State<'_, Arc<Connector>>,
    id: String,
) -> Result<SavedConnection, NativeFailure> {
    trusted_main(&window)?;
    let connector = Arc::clone(connector.inner());
    tauri::async_runtime::spawn_blocking(move || connector.retry_saved(&id))
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?
}
#[tauri::command]
async fn rename_connection(
    window: WebviewWindow<Wry>,
    app: AppHandle<Wry>,
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
    app: &AppHandle<Wry>,
    windows: &SavedWindows,
    mut profile: SavedConnection,
) -> Result<SavedConnection, NativeFailure> {
    let label = format!("server-{}", profile.id);
    // Serialize presentation with the binding, including title publication.
    // Out-of-order accepted edits cannot roll back a newer window label.
    let mut values = windows.0.lock().map_err(|_| NativeFailure::SidecarFailed)?;
    if let Some(binding) = values.get_mut(&label) {
        if !binding.profile.same_authority(&profile) {
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
    window: WebviewWindow<Wry>,
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
    if current.instance != binding.instance || !current.profile.same_authority(&binding.profile) {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(result)
}
#[tauri::command]
async fn saved_worker_proof(
    window: WebviewWindow<Wry>,
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
    if current.instance != binding.instance || !current.profile.same_authority(&binding.profile) {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(proof)
}
#[tauri::command]
async fn saved_worker_control(
    window: WebviewWindow<Wry>,
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
    if current.instance != binding.instance || !current.profile.same_authority(&binding.profile) {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(result)
}
fn show(window: &WebviewWindow<Wry>) -> Result<(), NativeFailure> {
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
fn create_main(app: &AppHandle<Wry>) -> tauri::Result<WebviewWindow<Wry>> {
    let config = &app.config().app.windows[0];
    WebviewWindowBuilder::from_config(app, config)?
        .incognito(true)
        .on_navigation(|url| {
            let allowed = trusted_url(url);
            tracing::info!(operation = "main_navigation", allowed);
            allowed
        })
        .on_page_load(|_, payload| {
            tracing::info!(operation="main_page",phase=?payload.event());
        })
        .on_new_window(|_, _| NewWindowResponse::Deny)
        .build()
}
#[tauri::command]
fn show_connection_manager(
    window: WebviewWindow<Wry>,
    app: AppHandle<Wry>,
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
    window: WebviewWindow<Wry>,
    app: AppHandle<Wry>,
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
            },
        );
    }
    let created = WebviewWindowBuilder::new(&app, &label, WebviewUrl::App("index.html".into()))
        .title(title)
        .inner_size(1280.0, 820.0)
        .min_inner_size(960.0, 640.0)
        .incognito(true)
        .on_navigation(|url| {
            trusted_url(url)
                && (url.scheme() == "tauri" || url.host_str() == Some("tauri.localhost"))
        })
        .on_new_window(|_, _| NewWindowResponse::Deny)
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
                if matches!(event, WindowEvent::Destroyed) {
                    if let Ok(mut values) = windows.0.lock() {
                        if values
                            .get(&label)
                            .is_some_and(|binding| binding.instance == instance)
                        {
                            values.remove(&label);
                        }
                    }
                }
            });
            tracing::info!(operation = "saved_window", state = "opened");
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
    tauri::Builder::<Wry>::new()
        .manage(Arc::new(SavedWindows::default()))
        .manage(connector)
        .manage(Arc::clone(&supervision))
        .invoke_handler(tauri::generate_handler![
            connect_local,
            local_server_status,
            local_worker_proof,
            local_worker_control,
            connection_context,
            saved_connections,
            pair_connection,
            retry_connection,
            rename_connection,
            open_connection,
            connect_saved,
            saved_worker_proof,
            saved_worker_control,
            show_connection_manager
        ])
        .setup(|app| {
            let result = (|| -> tauri::Result<()> {
                create_main(app.handle())?;
                Ok(())
            })();
            if result.is_err() {
                tracing::error!(operation = "main_window", code = "window-unavailable");
                app.handle().exit(1);
            }
            Ok(())
        })
        .run(tauri::generate_context!())
        .map_err(|_| NativeFailure::SidecarFailed)
}

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
