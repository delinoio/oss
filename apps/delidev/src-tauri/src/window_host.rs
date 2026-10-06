// SPDX-License-Identifier: Apache-2.0
//! Native menus and product-window creation, independent of server startup.
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicBool, Ordering},
};

use delidev_desktop::{
    NativeFailure, SavedConnection, SavedConnectionState,
    window_registry::{Entry, Role},
};
use tauri::{
    AppHandle, Manager, WebviewWindow, WebviewWindowBuilder,
    menu::{Menu, MenuItem, Submenu},
    utils::config::WebviewUrl,
    webview::NewWindowResponse,
};
use tauri_runtime_cef::CefRuntime;

use super::{
    ProductWindows, SavedBinding, WindowAuthority, capture_authority, recheck_authority,
    recheck_registered, reconcile_saved_label, saved_csp, show, trusted_url,
};

const NEW_WINDOW: &str = "delidev-new-window";
const CLOSE_WINDOW: &str = "delidev-close-window";

#[derive(Default)]
pub struct WindowActions {
    stopping: AtomicBool,
    tasks: Mutex<Vec<tauri::async_runtime::JoinHandle<()>>>,
}

pub fn title(entry: &Entry, name: Option<&str>) -> String {
    match name {
        Some(name) => format!("DeliDev · {name} · Window {}", entry.number),
        None if entry.number == 1 => "DeliDev".into(),
        None => format!("DeliDev · Window {}", entry.number),
    }
}

pub fn create(
    app: &AppHandle<CefRuntime>,
    profile: Option<SavedConnection>,
    initial: bool,
) -> Result<WebviewWindow<CefRuntime>, NativeFailure> {
    let windows = app.state::<Arc<ProductWindows>>();
    let role = profile
        .as_ref()
        .map_or(Role::Local, |v| Role::Saved(v.id.clone()));
    let barriers = windows
        .removals
        .try_lock()
        .map_err(|_| NativeFailure::Busy)?;
    if let Some(profile) = &profile {
        if !cfg!(feature = "custom-protocol") {
            return Err(NativeFailure::Incompatible);
        }
        if profile.state != SavedConnectionState::Paired {
            return Err(NativeFailure::InvalidEvidence);
        }
        if barriers.contains_key(&profile.id) {
            return Err(NativeFailure::Busy);
        }
    }
    let origin = profile
        .as_ref()
        .map(|v| delidev_desktop::connection_origin(&v.endpoint))
        .transpose()?;
    // Reconcile and register under the same binding lock so an accepted rename
    // cannot miss a queued creation and leave its title at an older revision.
    let mut bindings = windows.bindings.lock().map_err(|_| NativeFailure::Busy)?;
    let profile = profile
        .map(|value| reconcile_saved_label(&bindings, value))
        .transpose()?;
    let entry = windows
        .registry
        .lock()
        .map_err(|_| NativeFailure::Busy)?
        .reserve(role, initial)?;
    if let Some(profile) = &profile {
        bindings.insert(
            entry.label.clone(),
            SavedBinding {
                profile: profile.clone(),
                instance: entry.instance.clone(),
                closing: false,
            },
        );
    }
    drop(bindings);
    drop(barriers);
    // No registry mutex is held while Tauri synchronizes native creation.
    let result = (|| -> tauri::Result<WebviewWindow<CefRuntime>> {
        let builder = if profile.is_none() {
            let mut config = app.config().app.windows[0].clone();
            config.label = entry.label.clone();
            config.title = title(&entry, None);
            WebviewWindowBuilder::from_config(app, &config)?
        } else {
            WebviewWindowBuilder::new(app, &entry.label, WebviewUrl::App("index.html".into()))
                .title(title(&entry, profile.as_ref().map(|v| v.name.as_str())))
                .inner_size(1280.0, 820.0)
                .min_inner_size(960.0, 640.0)
                .decorations(true)
                .maximized(true)
        };
        let saved = profile.is_some();
        builder
            .incognito(true)
            .on_navigation(move |url| {
                trusted_url(url)
                    && (!saved
                        || url.scheme() == "tauri"
                        || url.host_str() == Some("tauri.localhost"))
            })
            .on_new_window(|_, _| NewWindowResponse::Deny)
            .on_page_load(|window, payload| {
                if payload.event() == tauri::webview::PageLoadEvent::Finished {
                    super::enable_document_accessibility(&window);
                }
            })
            .on_web_resource_request(move |_, response| {
                if let Some(origin) = &origin
                    && let Some(header) = response.headers_mut().get_mut("Content-Security-Policy")
                {
                    let policy = header.to_str().ok().and_then(|v| saved_csp(v, origin).ok());
                    *header = tauri::http::HeaderValue::from_str(
                        policy.as_deref().unwrap_or("default-src 'none'"),
                    )
                    .unwrap_or_else(|_| {
                        tauri::http::HeaderValue::from_static("default-src 'none'")
                    });
                }
            })
            .build()
    })();
    match result {
        Ok(window) => {
            if windows
                .registry
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .ready(&entry)
                .is_err()
            {
                let _ = window.destroy();
                cleanup(&windows, &entry);
                return Err(NativeFailure::Stopped);
            }
            // Labels are never reused in this process. A late Destroyed event
            // still checks the exact original instance before releasing state.
            // Retain only registry state. AppHandle would form a cycle with
            // the manager's window listeners and keep CEF request contexts
            // alive during shutdown after a window was destroyed.
            let owned_windows = Arc::clone(windows.inner());
            window.on_window_event(move |event| {
                if matches!(event, tauri::WindowEvent::Destroyed) {
                    cleanup(&owned_windows, &entry);
                }
            });
            super::tray_host::schedule(window.app_handle());
            tracing::info!(
                operation = "product_window",
                state = "opened",
                saved = profile.is_some()
            );
            Ok(window)
        }
        Err(_) => {
            cleanup(&windows, &entry);
            Err(NativeFailure::SidecarFailed)
        }
    }
}

fn cleanup(windows: &ProductWindows, entry: &Entry) {
    if let Ok(mut values) = windows.bindings.lock()
        && values
            .get(&entry.label)
            .is_some_and(|v| v.instance == entry.instance)
    {
        values.remove(&entry.label);
    }
    if let Ok(mut registry) = windows.registry.lock() {
        registry.remove(&entry.label, &entry.instance);
    }
}

enum CreationState {
    Pending,
    Created(Box<WebviewWindow<CefRuntime>>),
    Complete,
    Canceled,
}
struct PendingCreation {
    app: AppHandle<CefRuntime>,
    state: Arc<Mutex<CreationState>>,
}
impl Drop for PendingCreation {
    fn drop(&mut self) {
        let previous = {
            let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
            if matches!(*state, CreationState::Complete) {
                return;
            }
            std::mem::replace(&mut *state, CreationState::Canceled)
        };
        if let CreationState::Created(window) = previous
            && self
                .app
                .run_on_main_thread(move || {
                    if window.destroy().is_err() {
                        tracing::warn!(operation = "new_window", code = "cleanup-uncertain");
                    }
                })
                .is_err()
        {
            tracing::warn!(operation = "new_window", code = "cleanup-uncertain");
        }
    }
}

pub async fn create_on_loop(
    app: &AppHandle<CefRuntime>,
    profile: Option<SavedConnection>,
    source: Option<WindowAuthority>,
) -> Result<WebviewWindow<CefRuntime>, NativeFailure> {
    let (send, receive) = tokio::sync::oneshot::channel();
    let pending = PendingCreation {
        app: app.clone(),
        state: Arc::new(Mutex::new(CreationState::Pending)),
    };
    let state = Arc::clone(&pending.state);
    let target = app.clone();
    app.run_on_main_thread(move || {
        if !matches!(
            *state.lock().unwrap_or_else(|e| e.into_inner()),
            CreationState::Pending
        ) {
            return;
        }
        let result = source
            .as_ref()
            .map_or(Ok(()), |original| recheck_registered(&target, original))
            .and_then(|()| create(&target, profile, false));
        if let Ok(window) = &result {
            let retained = {
                let mut state = state.lock().unwrap_or_else(|e| e.into_inner());
                if matches!(*state, CreationState::Pending) {
                    *state = CreationState::Created(Box::new(window.clone()));
                    true
                } else {
                    false
                }
            };
            if !retained && window.destroy().is_err() {
                tracing::warn!(operation = "new_window", code = "cleanup-uncertain");
            }
        }
        let _ = send.send(result);
    })
    .map_err(|_| NativeFailure::SidecarFailed)?;
    let result = tokio::time::timeout(std::time::Duration::from_secs(5), receive)
        .await
        .map_err(|_| NativeFailure::TimedOut)?
        .map_err(|_| NativeFailure::SidecarFailed)??;
    // The guard owns even an already-sent result until its waiter adopts it.
    // Timeout or task cancellation destroys that result on the native loop;
    // a callback that has not started cannot create a late orphan window.
    *pending.state.lock().unwrap_or_else(|e| e.into_inner()) = CreationState::Complete;
    Ok(result)
}

pub async fn show_local(
    app: &AppHandle<CefRuntime>,
    original: WindowAuthority,
) -> Result<(), NativeFailure> {
    let entry = app
        .state::<Arc<ProductWindows>>()
        .registry
        .lock()
        .map_err(|_| NativeFailure::Busy)?
        .recent(Some(&Role::Local));
    if let Some(entry) = entry
        && let Some(window) = app.get_webview_window(&entry.label)
    {
        return show(&window);
    }
    create_on_loop(app, None, Some(original)).await.map(|_| ())
}

pub fn restore_recent(app: &AppHandle<CefRuntime>) {
    let entry = app
        .state::<Arc<ProductWindows>>()
        .registry
        .lock()
        .ok()
        .and_then(|r| r.recent(None));
    if let Some(entry) = entry
        && let Some(window) = app.get_webview_window(&entry.label)
        && show(&window).is_err()
    {
        tracing::warn!(operation = "window_restore", code = "window-unavailable");
    }
}

pub fn install_menu(app: &AppHandle<CefRuntime>) -> tauri::Result<()> {
    let menu = Menu::default(app)?;
    let item = MenuItem::with_id(app, NEW_WINDOW, "New Window", true, Some("CmdOrCtrl+N"))?;
    let close = MenuItem::with_id(app, CLOSE_WINDOW, "Close Window", true, Some("CmdOrCtrl+W"))?;
    let file = menu.items()?.into_iter().enumerate().find(|(_, item)| {
        item.as_submenu()
            .is_some_and(|sub| sub.text().is_ok_and(|v| v == "File"))
    });
    let position = if let Some((position, _)) = file {
        menu.remove_at(position)?;
        position
    } else {
        0
    };
    // Custom Close dispatch preserves the approved two-row macOS File menu.
    // AppKit's predefined performClose selector can add a Close All row.
    let file = Submenu::with_items(app, "File", true, &[&item, &close])?;
    #[cfg(not(target_os = "macos"))]
    file.append(&tauri::menu::PredefinedMenuItem::quit(app, None)?)?;
    menu.insert(&file, position)?;
    app.set_menu(menu)?;
    // One app-level handler: per-window handlers would all receive the same
    // global menu event and multiply a single key press into many windows.
    app.on_menu_event(|app, event| match event.id.as_ref() {
        NEW_WINDOW => enqueue_new(app),
        CLOSE_WINDOW => {
            let entry = app
                .state::<Arc<ProductWindows>>()
                .registry
                .lock()
                .ok()
                .and_then(|registry| registry.recent(None));
            if let Some(entry) = entry
                && let Some(window) = app.get_webview_window(&entry.label)
                && window.close().is_err()
            {
                tracing::warn!(operation = "window_close", code = "window-unavailable");
            }
        }
        _ => {}
    });
    Ok(())
}

fn enqueue_new(app: &AppHandle<CefRuntime>) {
    let actions = app.state::<Arc<WindowActions>>();
    let mut tasks = actions.tasks.lock().unwrap_or_else(|e| e.into_inner());
    if actions.stopping.load(Ordering::Acquire) {
        return;
    }
    tasks.retain(|task| !task.inner().is_finished());
    let entry = app
        .state::<Arc<ProductWindows>>()
        .registry
        .lock()
        .ok()
        .and_then(|r| r.recent(None));
    let app = app.clone();
    let stopping = Arc::clone(actions.inner());
    tasks.push(tauri::async_runtime::spawn(async move {
        let result = async {
            let (profile, source) = if let Some(entry) = entry {
                let window = app
                    .get_webview_window(&entry.label)
                    .ok_or(NativeFailure::PermissionDenied)?;
                let target = window.clone();
                let original =
                    tauri::async_runtime::spawn_blocking(move || capture_authority(&target))
                        .await
                        .map_err(|_| NativeFailure::SidecarFailed)??;
                let profile = if let Some(expected) = &original.saved {
                    let connector =
                        Arc::clone(app.state::<Arc<delidev_desktop::Connector>>().inner());
                    let id = expected.id.clone();
                    let current =
                        tauri::async_runtime::spawn_blocking(move || connector.inspect_saved(&id))
                            .await
                            .map_err(|_| NativeFailure::SidecarFailed)??;
                    if !current.same_authority(expected) {
                        return Err(NativeFailure::InvalidEvidence);
                    }
                    Some(current)
                } else {
                    None
                };
                let checked = original.clone();
                tauri::async_runtime::spawn_blocking(move || recheck_authority(&window, &checked))
                    .await
                    .map_err(|_| NativeFailure::SidecarFailed)??;
                (profile, Some(original))
            } else {
                (None, None)
            };
            if stopping.stopping.load(Ordering::Acquire) {
                return Err(NativeFailure::Stopped);
            }
            create_on_loop(&app, profile, source).await.map(|_| ())
        }
        .await;
        if let Err(code) = result {
            tracing::warn!(operation = "new_window", ?code);
            if !stopping.stopping.load(Ordering::Acquire) {
                rfd::AsyncMessageDialog::new()
                    .set_title("DeliDev")
                    .set_description("DeliDev could not open a new window. Try again.")
                    .set_level(rfd::MessageLevel::Error)
                    .show()
                    .await;
            }
        }
    }));
}

impl WindowActions {
    pub fn stop(&self) {
        self.stopping.store(true, Ordering::Release);
    }

    pub fn join(&self) {
        let tasks = std::mem::take(&mut *self.tasks.lock().unwrap_or_else(|e| e.into_inner()));
        tauri::async_runtime::block_on(async {
            let deadline = tokio::time::Instant::now() + std::time::Duration::from_secs(5);
            for mut task in tasks {
                if tokio::time::timeout_at(deadline, &mut task).await.is_err() {
                    task.abort();
                    let _ = task.await;
                    tracing::warn!(operation = "window_tasks", code = "cleanup-uncertain");
                }
            }
        });
    }
}
