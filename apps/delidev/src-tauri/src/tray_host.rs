use std::{
    collections::BTreeMap,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread::{self, JoinHandle},
    time::{Duration, Instant},
};

use delidev_desktop::{
    NativeFailure,
    presentation::{TrayDestination, TraySummary, menu_alias},
    widget_writer::{Publication, WidgetWriter},
};
use tauri::{
    AppHandle, Emitter, Manager, WebviewWindow,
    menu::{Menu, MenuItem, Submenu},
    tray::TrayIconBuilder,
};
use tauri_runtime_cef::CefRuntime;

use super::{ProductWindows, saved_binding, show, trusted_local, trusted_url};

const TRAY_ID: &str = "delidev-status";
const STALE_AFTER: Duration = Duration::from_secs(45);
struct Presentation {
    scope: String,
    revision: u32,
    summary: Option<TraySummary>,
    received: Instant,
    stale: bool,
}
#[derive(Clone)]
struct Activation {
    label: String,
    instance: Option<String>,
    destination: TrayDestination,
}
#[derive(Clone, serde::Serialize)]
pub struct TrayAction {
    id: String,
    destination: TrayDestination,
    #[serde(skip_serializing_if = "Option::is_none")]
    inbox_id: Option<String>,
    #[serde(skip)]
    notification_scope: Option<String>,
}
#[derive(Default)]
struct State {
    windows: BTreeMap<String, Presentation>,
    actions: BTreeMap<String, Activation>,
    pending: BTreeMap<String, TrayAction>,
}
impl State {
    fn publish(
        &mut self,
        label: &str,
        scope: &str,
        revision: u32,
        summary: TraySummary,
    ) -> Result<(), NativeFailure> {
        summary.validate()?;
        let current = self
            .windows
            .get_mut(label)
            .filter(|v| v.scope == scope && revision > v.revision)
            .ok_or(NativeFailure::InvalidEvidence)?;
        current.revision = revision;
        current.summary = Some(summary);
        current.received = Instant::now();
        current.stale = false;
        Ok(())
    }

    fn acknowledge(&mut self, label: &str, id: &str) {
        if self.pending.get(label).is_some_and(|value| value.id == id) {
            self.pending.remove(label);
        }
    }
}
pub struct TrayHost {
    state: Arc<Mutex<State>>,
    widgets: WidgetWriter,
    pub available: AtomicBool,
    stop: Arc<AtomicBool>,
    task: Mutex<Option<JoinHandle<()>>>,
}
impl Default for TrayHost {
    fn default() -> Self {
        Self {
            state: Arc::new(Mutex::new(State::default())),
            widgets: WidgetWriter::new(super::widget_host::apply),
            available: AtomicBool::new(false),
            stop: Arc::new(AtomicBool::new(false)),
            task: Mutex::new(None),
        }
    }
}
fn authorized(
    window: &WebviewWindow<CefRuntime>,
    windows: &ProductWindows,
) -> Result<(), NativeFailure> {
    if super::is_local(window) {
        trusted_local(window)
    } else {
        saved_binding(window, windows).map(|_| ())
    }
}
#[tauri::command]
pub async fn begin_tray(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    host: tauri::State<'_, Arc<TrayHost>>,
) -> Result<String, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        authorized(&window, &windows)?;
        let scope = uuid::Uuid::now_v7().to_string();
        let mut state = host.state.lock().map_err(|_| NativeFailure::Busy)?;
        if host.stop.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        state.windows.insert(
            window.label().into(),
            Presentation {
                scope: scope.clone(),
                revision: 0,
                summary: None,
                received: Instant::now(),
                stale: false,
            },
        );
        drop(state);
        schedule(&app);
        Ok(scope)
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
pub async fn publish_tray(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    host: tauri::State<'_, Arc<TrayHost>>,
    scope: String,
    revision: u32,
    summary: TraySummary,
) -> Result<(), NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        authorized(&window, &windows)?;
        let host = Arc::clone(host.inner());
        let windows = Arc::clone(windows.inner());
        let window = window.clone();
        // Capture URL/binding authority off the UI loop. The bounded tray lock
        // orders admission only; the joined widget worker owns persistence.
        tauri::async_runtime::spawn_blocking(move || {
            // CEF URL reads wait on the native UI loop. No widget worker or
            // shutdown join may depend on these URL reads.
            authorized(&window, &windows)?;
            let binding = if super::is_local(&window) {
                None
            } else {
                Some(saved_binding(&window, &windows)?)
            };
            let mut state = host.state.lock().map_err(|_| NativeFailure::Busy)?;
            if host.stop.load(Ordering::Acquire) {
                return Err(NativeFailure::Stopped);
            }
            if let Some(binding) = &binding {
                let current = windows
                    .bindings
                    .try_lock()
                    .map_err(|_| NativeFailure::Busy)?;
                if !current
                    .get(window.label())
                    .is_some_and(|value| widget_binding(value, binding))
                {
                    return Err(NativeFailure::PermissionDenied);
                }
            }
            state.publish(window.label(), &scope, revision, summary.clone())?;
            if let Some(binding) = binding {
                let registry = windows
                    .registry
                    .try_lock()
                    .map_err(|_| NativeFailure::Busy)?;
                let writer = registry.oldest(&delidev_desktop::window_registry::Role::Saved(
                    binding.profile.id.clone(),
                ));
                drop(registry);
                if writer
                    .as_ref()
                    .is_none_or(|entry| entry.instance != binding.instance)
                {
                    return Ok(());
                }
                let state = Arc::clone(&host.state);
                let label = window.label().to_owned();
                let publication = Publication::Publish {
                    id: binding.profile.id.clone(),
                    name: binding.profile.name.clone(),
                    summary: Box::new(summary),
                };
                // A failed widget admission/storage outcome does not undo the
                // accepted tray projection. The old snapshot expires normally.
                if let Err(code) = host.widgets.submit(publication, move |publication| {
                    widget_current(
                        &state,
                        &windows,
                        &label,
                        &scope,
                        revision,
                        &binding,
                        publication,
                    )
                }) {
                    tracing::warn!(
                        operation = "widget_snapshot",
                        phase = "admission-failed",
                        ?code
                    );
                }
            }
            Ok::<(), NativeFailure>(())
        })
        .await
        .map_err(|_| NativeFailure::StorageUnavailable)??;
        schedule(&app);
        Ok(())
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}

// No CEF calls or persistence here. An in-flight write may finish after a
// replacement, but the FIFO worker puts every successor after that write.
fn widget_binding(current: &super::SavedBinding, original: &super::SavedBinding) -> bool {
    !current.closing
        && current.instance == original.instance
        && current.profile.id == original.profile.id
        && current.profile.state == delidev_desktop::SavedConnectionState::Paired
        && current.profile.endpoint == original.profile.endpoint
        && current.profile.server_id == original.profile.server_id
        && current.profile.device_id == original.profile.device_id
        && current.profile.pairing_id == original.profile.pairing_id
}

fn widget_current(
    state: &Mutex<State>,
    windows: &ProductWindows,
    label: &str,
    scope: &str,
    revision: u32,
    binding: &super::SavedBinding,
    publication: &mut Publication,
) -> bool {
    let state = match state.lock() {
        Ok(value) => value,
        Err(_) => return false,
    };
    if !state
        .windows
        .get(label)
        .is_some_and(|value| value.scope == scope && value.revision == revision)
    {
        return false;
    }
    let bindings = match windows.bindings.try_lock() {
        Ok(values) => values,
        Err(_) => return false,
    };
    let Some(current) = bindings
        .get(label)
        .filter(|value| widget_binding(value, binding))
    else {
        return false;
    };
    let registry = match windows.registry.try_lock() {
        Ok(value) => value,
        Err(_) => return false,
    };
    if registry
        .oldest(&delidev_desktop::window_registry::Role::Saved(
            binding.profile.id.clone(),
        ))
        .is_none_or(|entry| entry.instance != binding.instance)
    {
        return false;
    }
    // A queued write uses the newest committed native name.
    if let Publication::Publish { name, .. } = publication {
        *name = current.profile.name.clone();
    }
    true
}

pub fn remove_widget(app: &AppHandle<CefRuntime>, id: &str) {
    let host = app.state::<Arc<TrayHost>>();
    if let Ok(_guard) = host.state.lock()
        && let Err(code) = host
            .widgets
            .submit(Publication::Remove { id: id.into() }, |_| true)
    {
        tracing::warn!(
            operation = "widget_snapshot",
            phase = "removal-admission-failed",
            ?code
        );
    }
}
#[tauri::command]
pub async fn read_tray_action(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    host: tauri::State<'_, Arc<TrayHost>>,
) -> Result<Option<TrayAction>, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        authorized(&window, &windows)?;
        let action = host
            .state
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .pending
            .get(window.label())
            .cloned();
        Ok(action.filter(|action| {
            action.notification_scope.as_ref().is_none_or(|scope| {
                window
                    .state::<Arc<super::NotificationHost>>()
                    .current(window.label(), scope)
            })
        }))
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
pub async fn acknowledge_tray_action(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    host: tauri::State<'_, Arc<TrayHost>>,
    id: String,
) -> Result<(), NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        authorized(&window, &windows)?;
        let mut state = host.state.lock().map_err(|_| NativeFailure::Busy)?;
        state.acknowledge(window.label(), &id);
        Ok(())
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}

pub fn schedule(app: &AppHandle<CefRuntime>) {
    let target = app.clone();
    if app
        .run_on_main_thread(move || {
            if render(&target).is_err() {
                tracing::warn!(
                    operation = "tray_refresh",
                    code = "presentation-unavailable"
                );
            }
        })
        .is_err()
    {
        tracing::warn!(
            operation = "tray_schedule",
            code = "presentation-unavailable"
        );
    }
}
pub fn remove(app: &AppHandle<CefRuntime>, label: &str) {
    let host = app.state::<Arc<TrayHost>>();
    if let Ok(mut state) = host.state.lock() {
        state.windows.remove(label);
        state.pending.remove(label);
    }
    schedule(app);
}
fn append(
    app: &AppHandle<CefRuntime>,
    menu: &Submenu<CefRuntime>,
    text: &str,
    activation: Option<Activation>,
    state: &mut State,
) -> tauri::Result<()> {
    let id = uuid::Uuid::now_v7().to_string();
    let item = MenuItem::with_id(app, id.clone(), text, activation.is_some(), None::<&str>)?;
    if let Some(action) = activation {
        state.actions.insert(id, action);
    }
    menu.append(&item)
}
fn render(app: &AppHandle<CefRuntime>) -> tauri::Result<()> {
    let Some(tray) = app.tray_by_id(TRAY_ID) else {
        return Ok(());
    };
    let host = app.state::<Arc<TrayHost>>();
    let saved = app.state::<Arc<ProductWindows>>();
    // Both state maps are touched only long enough to build bounded native UI.
    // No controller, network operation, credential read or product call occurs.
    // Skip contention rather than delaying the native event loop. The joined
    // presentation timer schedules another repaint with current labels.
    let saved = match saved.bindings.try_lock() {
        Ok(values) => values.clone(),
        Err(_) => return Ok(()),
    };
    let mut state = match host.state.try_lock() {
        Ok(state) => state,
        Err(_) => return Ok(()),
    };
    state.actions.clear();
    let menu = Menu::new(app)?;
    menu.append(&MenuItem::with_id(
        app,
        "tray-show",
        "Show DeliDev",
        true,
        None::<&str>,
    )?)?;
    let registry = match app.state::<Arc<ProductWindows>>().registry.try_lock() {
        Ok(registry) => registry.entries(),
        Err(_) => return Ok(()),
    };
    for entry in registry
        .into_iter()
        .filter(|entry| entry.phase == delidev_desktop::window_registry::Phase::Ready)
    {
        let label = entry.label;
        let name = match entry.role {
            delidev_desktop::window_registry::Role::Local => {
                format!("This computer · Window {}", entry.number)
            }
            delidev_desktop::window_registry::Role::Saved(_) => {
                let Some(binding) = saved.get(&label).filter(|v| !v.closing) else {
                    continue;
                };
                format!(
                    "{} · Window {}",
                    menu_alias(&binding.profile.name),
                    entry.number
                )
            }
        };
        let instance = Some(entry.instance);
        let action = |destination| {
            Some(Activation {
                label: label.clone(),
                instance: instance.clone(),
                destination,
            })
        };
        let submenu = Submenu::new(app, name, true)?;
        let current = state.windows.get(&label);
        let stale = current.is_some_and(|v| {
            v.stale
                || v.received.elapsed() > STALE_AFTER
                || v.summary
                    .as_ref()
                    .and_then(|v| v.overview.as_ref())
                    .is_some_and(|v| v.stale)
        });
        let summary = current.and_then(|v| v.summary.clone());
        if let Some(overview) = summary.as_ref().and_then(|v| v.overview.as_ref()) {
            append(
                app,
                &submenu,
                if stale {
                    "Connection status stale"
                } else {
                    "Connected"
                },
                None,
                &mut state,
            )?;
            append(
                app,
                &submenu,
                &format!("Updated {}", overview.observed_at),
                None,
                &mut state,
            )?;
            append(
                app,
                &submenu,
                &format!(
                    "{} active sessions{}",
                    overview.active_sessions,
                    if stale { " · stale" } else { "" }
                ),
                action(TrayDestination::Sessions),
                &mut state,
            )?;
            append(
                app,
                &submenu,
                &format!(
                    "{} pending requests{}",
                    overview.pending_interactions,
                    if stale { " · stale" } else { "" }
                ),
                action(TrayDestination::Inbox),
                &mut state,
            )?;
            append(
                app,
                &submenu,
                &format!(
                    "Workers: {} / {} connected{}",
                    overview.connected_workers,
                    overview.registered_workers,
                    if stale { " · stale" } else { "" }
                ),
                action(TrayDestination::Settings),
                &mut state,
            )?;
        } else {
            append(
                app,
                &submenu,
                "Connection status unavailable",
                None,
                &mut state,
            )?;
            append(
                app,
                &submenu,
                "Sessions",
                action(TrayDestination::Sessions),
                &mut state,
            )?;
            append(
                app,
                &submenu,
                "Inbox",
                action(TrayDestination::Inbox),
                &mut state,
            )?;
        }
        let usage = summary.as_ref().and_then(|v| v.usage.as_ref());
        let usage_text = match usage.and_then(|v| v.known_tokens.as_deref()) {
            Some(tokens) => format!(
                "Today (UTC): {tokens} known tokens{}{}",
                if usage.is_some_and(|v| v.incomplete) {
                    " · incomplete"
                } else {
                    ""
                },
                if stale { " · stale" } else { "" }
            ),
            None => "Today (UTC): usage unavailable".into(),
        };
        append(
            app,
            &submenu,
            &usage_text,
            action(TrayDestination::Usage),
            &mut state,
        )?;
        let accounts = Submenu::new(
            app,
            if stale {
                "Account quotas · stale"
            } else {
                "Account quotas"
            },
            true,
        )?;
        if let Some(values) = summary.as_ref().and_then(|v| v.accounts.as_ref()) {
            if values.entries.is_empty() {
                append(app, &accounts, "No configured accounts", None, &mut state)?;
            }
            for account in &values.entries {
                let quota = Submenu::new(app, menu_alias(&account.alias), true)?;
                if account.windows.is_empty() {
                    append(app, &quota, "Quota unavailable", None, &mut state)?;
                }
                for (index, window) in account.windows.iter().enumerate() {
                    append(
                        app,
                        &quota,
                        &format!("Window {}: {}", index + 1, window.label()),
                        None,
                        &mut state,
                    )?;
                    if let Some(at) = &window.observed_at {
                        append(app, &quota, &format!("Observed {at}"), None, &mut state)?;
                    }
                    if let Some(at) = &window.reset_at {
                        append(app, &quota, &format!("Reset {at}"), None, &mut state)?;
                    }
                }
                if account.more {
                    append(
                        app,
                        &quota,
                        "More windows in account settings",
                        action(TrayDestination::Settings),
                        &mut state,
                    )?;
                }
                accounts.append(&quota)?;
            }
            if values.more {
                append(
                    app,
                    &accounts,
                    "More accounts in Settings",
                    action(TrayDestination::Settings),
                    &mut state,
                )?;
            }
        } else {
            append(
                app,
                &accounts,
                "Account quotas unavailable",
                None,
                &mut state,
            )?;
        }
        submenu.append(&accounts)?;
        append(
            app,
            &submenu,
            "Worker and account settings",
            action(TrayDestination::Settings),
            &mut state,
        )?;
        menu.append(&submenu)?;
    }
    menu.append(&MenuItem::with_id(
        app,
        "tray-quit",
        "Quit DeliDev",
        true,
        None::<&str>,
    )?)?;
    tray.set_menu(Some(menu))
}
fn activate(app: &AppHandle<CefRuntime>, id: &str) {
    if id == "tray-quit" {
        app.exit(0);
        return;
    }
    if id == "tray-show" {
        super::window_host::restore_recent(app);
        return;
    }
    let host = app.state::<Arc<TrayHost>>();
    let action = host
        .state
        .try_lock()
        .ok()
        .and_then(|v| v.actions.get(id).cloned());
    let Some(action) = action else { return };
    navigate(app, action, None, None);
}
pub fn activate_inbox(
    app: &AppHandle<CefRuntime>,
    label: String,
    instance: Option<String>,
    scope: String,
    inbox_id: String,
) {
    if delidev_desktop::canonical_id(&inbox_id).is_err() {
        return;
    }
    navigate(
        app,
        Activation {
            label,
            instance,
            destination: TrayDestination::Inbox,
        },
        Some(inbox_id),
        Some(scope),
    );
}
fn navigate(
    app: &AppHandle<CefRuntime>,
    action: Activation,
    inbox_id: Option<String>,
    notification_scope: Option<String>,
) {
    // Tray and notification callbacks originate on the native event loop.
    // CEF URL authorization waits for that loop, so perform it on a worker.
    let app = app.clone();
    tauri::async_runtime::spawn_blocking(move || {
        navigate_off_loop(&app, action, inbox_id, notification_scope);
    });
}
fn navigate_off_loop(
    app: &AppHandle<CefRuntime>,
    action: Activation,
    inbox_id: Option<String>,
    notification_scope: Option<String>,
) {
    let host = app.state::<Arc<TrayHost>>();
    if host.stop.load(Ordering::Acquire) {
        return;
    }
    let Some(window) = app.get_webview_window(&action.label) else {
        return;
    };
    if !window.url().is_ok_and(|url| trusted_url(&url)) {
        return;
    }
    let original = match super::capture_authority(&window) {
        Ok(value) => value,
        Err(_) => return,
    };
    if action
        .instance
        .as_ref()
        .is_some_and(|instance| &original.entry.instance != instance)
    {
        return;
    }
    if host.stop.load(Ordering::Acquire)
        || notification_scope.as_ref().is_some_and(|scope| {
            !app.state::<Arc<super::NotificationHost>>()
                .current(&action.label, scope)
        })
    {
        return;
    }
    if super::recheck_authority(&window, &original).is_err() {
        return;
    }
    if let Ok(mut state) = host.state.lock() {
        state.pending.insert(
            action.label,
            TrayAction {
                id: uuid::Uuid::now_v7().to_string(),
                destination: action.destination,
                inbox_id,
                notification_scope,
            },
        );
    }
    if show(&window).is_err() || window.emit("tray-activate", ()).is_err() {
        tracing::warn!(operation = "tray_activate", code = "window-unavailable");
    }
}
impl TrayHost {
    pub fn install(self: &Arc<Self>, app: &AppHandle<CefRuntime>) -> tauri::Result<()> {
        let menu = Menu::new(app)?;
        menu.append(&MenuItem::with_id(
            app,
            "tray-show",
            "Show DeliDev",
            true,
            None::<&str>,
        )?)?;
        let mut builder = TrayIconBuilder::with_id(TRAY_ID)
            .menu(&menu)
            .tooltip("DeliDev")
            .show_menu_on_left_click(true)
            .on_menu_event(|app, event| activate(app, event.id.as_ref()));
        if let Some(icon) = app.default_window_icon() {
            builder = builder.icon(icon.clone());
        }
        builder.build(app)?;
        render(app)?;
        self.available.store(true, Ordering::Release);
        let app = app.clone();
        let stop = Arc::clone(&self.stop);
        *self.task.lock().unwrap_or_else(|e| e.into_inner()) = Some(thread::spawn(move || {
            while !stop.load(Ordering::Acquire) {
                thread::park_timeout(Duration::from_secs(15));
                if stop.load(Ordering::Acquire) {
                    break;
                }
                let host = app.state::<Arc<TrayHost>>();
                let changed = if let Ok(mut state) = host.state.lock() {
                    let mut changed = false;
                    for value in state.windows.values_mut() {
                        if !value.stale && value.received.elapsed() > STALE_AFTER {
                            value.stale = true;
                            changed = true;
                        }
                    }
                    changed
                } else {
                    false
                };
                if changed {
                    schedule(&app);
                }
            }
        }));
        tracing::info!(operation = "tray_install", state = "ready");
        Ok(())
    }

    pub fn request_stop(&self) {
        self.stop.store(true, Ordering::Release);
        self.widgets.request_stop();
    }

    // Only the tracked Quit worker or post-runtime return cleanup calls this.
    pub fn stop(&self) {
        self.request_stop();
        if let Some(task) = self.task.lock().unwrap_or_else(|e| e.into_inner()).take() {
            task.thread().unpark();
            let _ = task.join();
        }
        if let Err(code) = self.widgets.stop() {
            tracing::warn!(operation = "widget_snapshot", phase = "join-failed", ?code);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn unavailable() -> TraySummary {
        TraySummary {
            overview: None,
            usage: None,
            accounts: None,
        }
    }
    fn presentation(scope: &str) -> Presentation {
        Presentation {
            scope: scope.into(),
            revision: 0,
            summary: None,
            received: Instant::now(),
            stale: false,
        }
    }
    fn saved(windows: &ProductWindows) -> (String, super::super::SavedBinding) {
        use delidev_desktop::{SavedConnection, SavedConnectionState, window_registry::Role};
        let id = "0199ab40-8280-7000-8000-000000000001";
        let mut registry = windows.registry.lock().unwrap();
        let entry = registry.reserve(Role::Saved(id.into()), false).unwrap();
        registry.ready(&entry).unwrap();
        let binding = super::super::SavedBinding {
            profile: SavedConnection {
                version: 1,
                revision: 1,
                id: id.into(),
                name: "Fixture".into(),
                endpoint: "https://fixture.test".into(),
                server_id: "server".into(),
                pairing_id: "pairing".into(),
                device_id: "device".into(),
                state: SavedConnectionState::Paired,
                created_at: "fixture".into(),
                removal: None,
            },
            instance: entry.instance,
            closing: false,
        };
        windows
            .bindings
            .lock()
            .unwrap()
            .insert(entry.label.clone(), binding.clone());
        (entry.label, binding)
    }
    fn snapshot(binding: &super::super::SavedBinding) -> Publication {
        Publication::Publish {
            id: binding.profile.id.clone(),
            name: binding.profile.name.clone(),
            summary: Box::new(unavailable()),
        }
    }
    #[test]
    fn queued_widget_rechecks_scope_revision_original_window_and_oldest_ready_owner() {
        let windows = ProductWindows::default();
        let (first, original) = saved(&windows);
        let (second, successor) = saved(&windows);
        let state = Mutex::new(State::default());
        for label in [&first, &second] {
            state
                .lock()
                .unwrap()
                .windows
                .insert(label.clone(), presentation("scope"));
            state
                .lock()
                .unwrap()
                .publish(label, "scope", 1, unavailable())
                .unwrap();
        }
        let mut publication = snapshot(&original);
        assert!(widget_current(
            &state,
            &windows,
            &first,
            "scope",
            1,
            &original,
            &mut publication
        ));
        assert!(!widget_current(
            &state,
            &windows,
            &second,
            "scope",
            1,
            &successor,
            &mut publication
        ));
        assert!(!widget_current(
            &state,
            &windows,
            &first,
            "old-scope",
            1,
            &original,
            &mut publication
        ));
        assert!(!widget_current(
            &state,
            &windows,
            &first,
            "scope",
            0,
            &original,
            &mut publication
        ));

        windows
            .bindings
            .lock()
            .unwrap()
            .get_mut(&first)
            .unwrap()
            .profile
            .name = "Renamed".into();
        assert!(widget_current(
            &state,
            &windows,
            &first,
            "scope",
            1,
            &original,
            &mut publication
        ));
        assert_eq!(
            serde_json::to_value(&publication).unwrap()["name"],
            "Renamed"
        );
        windows
            .bindings
            .lock()
            .unwrap()
            .get_mut(&first)
            .unwrap()
            .profile
            .device_id = "replacement-device".into();
        assert!(!widget_current(
            &state,
            &windows,
            &first,
            "scope",
            1,
            &original,
            &mut publication
        ));
        windows
            .bindings
            .lock()
            .unwrap()
            .get_mut(&first)
            .unwrap()
            .profile
            .device_id = original.profile.device_id.clone();
        windows
            .bindings
            .lock()
            .unwrap()
            .get_mut(&first)
            .unwrap()
            .closing = true;
        assert!(!widget_current(
            &state,
            &windows,
            &first,
            "scope",
            1,
            &original,
            &mut publication
        ));
        windows
            .registry
            .lock()
            .unwrap()
            .remove(&first, &original.instance);
        assert!(widget_current(
            &state,
            &windows,
            &second,
            "scope",
            1,
            &successor,
            &mut snapshot(&successor)
        ));
        windows
            .bindings
            .lock()
            .unwrap()
            .get_mut(&second)
            .unwrap()
            .instance = "replacement".into();
        assert!(!widget_current(
            &state,
            &windows,
            &second,
            "scope",
            1,
            &successor,
            &mut snapshot(&successor)
        ));
    }

    #[test]
    fn blocked_widget_storage_releases_tray_and_window_state_before_quit_join() {
        use std::sync::mpsc;
        let wait = Duration::from_secs(5);
        let state = Arc::new(Mutex::new(State::default()));
        let windows = Arc::new(ProductWindows::default());
        let (label, binding) = saved(&windows);
        let (next_label, successor) = saved(&windows);
        windows
            .bindings
            .lock()
            .unwrap()
            .get_mut(&next_label)
            .unwrap()
            .profile
            .name = "Successor".into();
        state
            .lock()
            .unwrap()
            .windows
            .insert(label.clone(), presentation("scope"));
        state
            .lock()
            .unwrap()
            .publish(&label, "scope", 1, unavailable())
            .unwrap();
        let (entered, entering) = mpsc::channel();
        let (release, releasing) = mpsc::channel();
        let (written, writes) = mpsc::channel();
        let mut blocked = false;
        let writer = Arc::new(WidgetWriter::new(move |publication| {
            if !blocked && matches!(publication, Publication::Publish { .. }) {
                blocked = true;
                entered.send(()).unwrap();
                releasing.recv_timeout(wait).unwrap();
            }
            written
                .send(serde_json::to_value(publication).unwrap())
                .unwrap();
            Ok(())
        }));
        let queued_state = Arc::clone(&state);
        let queued_windows = Arc::clone(&windows);
        let queued_label = label.clone();
        let original_instance = binding.instance.clone();
        let queued_binding = binding.clone();
        writer
            .submit(snapshot(&binding), move |publication| {
                widget_current(
                    &queued_state,
                    &queued_windows,
                    &queued_label,
                    "scope",
                    1,
                    &queued_binding,
                    publication,
                )
            })
            .unwrap();
        entering.recv_timeout(wait).unwrap();
        // These are the production locks read by native render and window
        // event callbacks, after the real publication-authority check.
        assert!(state.try_lock().is_ok());
        assert!(windows.bindings.try_lock().is_ok());
        assert!(windows.registry.try_lock().is_ok());

        let queued_state = Arc::clone(&state);
        let queued_windows = Arc::clone(&windows);
        let queued_label = label.clone();
        writer
            .submit(snapshot(&binding), move |publication| {
                widget_current(
                    &queued_state,
                    &queued_windows,
                    &queued_label,
                    "scope",
                    1,
                    &binding,
                    publication,
                )
            })
            .unwrap();
        state
            .lock()
            .unwrap()
            .windows
            .insert(label.clone(), presentation("replacement"));
        windows
            .registry
            .lock()
            .unwrap()
            .remove(&label, &original_instance);
        {
            let mut state = state.lock().unwrap();
            state
                .windows
                .insert(next_label.clone(), presentation("successor"));
            state
                .publish(&next_label, "successor", 1, unavailable())
                .unwrap();
        }
        let queued_state = Arc::clone(&state);
        let queued_windows = Arc::clone(&windows);
        writer
            .submit(snapshot(&successor), move |publication| {
                widget_current(
                    &queued_state,
                    &queued_windows,
                    &next_label,
                    "successor",
                    1,
                    &successor,
                    publication,
                )
            })
            .unwrap();
        writer.request_stop();
        let quitting = Arc::clone(&writer);
        let (joined, joining) = mpsc::channel();
        let quit = thread::spawn(move || {
            joined.send(quitting.stop()).unwrap();
        });
        assert!(joining.recv_timeout(Duration::from_millis(50)).is_err());
        release.send(()).unwrap();
        joining.recv_timeout(wait).unwrap().unwrap();
        quit.join().unwrap();
        assert_eq!(writes.recv_timeout(wait).unwrap()["name"], "Fixture");
        assert_eq!(writes.recv_timeout(wait).unwrap()["name"], "Successor");
        assert_eq!(writes.recv_timeout(wait).unwrap()["action"], "stop");
        assert!(writes.try_recv().is_err());
    }
    #[test]
    fn old_scopes_cannot_replace_another_window_or_newer_publication() {
        let mut state = State::default();
        state.windows.insert("main".into(), presentation("first"));
        state.windows.insert("saved".into(), presentation("other"));
        state.publish("main", "first", 2, unavailable()).unwrap();
        assert!(state.publish("main", "first", 1, unavailable()).is_err());
        assert!(state.publish("main", "first", 2, unavailable()).is_err());
        assert!(state.publish("saved", "first", 3, unavailable()).is_err());
        state
            .windows
            .insert("main".into(), presentation("replacement"));
        assert!(state.publish("main", "first", 4, unavailable()).is_err());
        state
            .publish("main", "replacement", 1, unavailable())
            .unwrap();
        state.windows.remove("main");
        assert!(
            state
                .publish("main", "replacement", 2, unavailable())
                .is_err()
        );
    }
    #[test]
    fn reading_and_stale_acknowledgment_preserve_pending_navigation() {
        let mut state = State::default();
        state.pending.insert(
            "main".into(),
            TrayAction {
                id: "original".into(),
                destination: TrayDestination::Inbox,
                inbox_id: Some(uuid::Uuid::now_v7().to_string()),
                notification_scope: None,
            },
        );
        assert_eq!(
            state.pending.get("main").unwrap().destination,
            TrayDestination::Inbox
        );
        state.acknowledge("saved", "original");
        assert!(state.pending.contains_key("main"));
        state.pending.insert(
            "main".into(),
            TrayAction {
                id: "newer".into(),
                destination: TrayDestination::Usage,
                inbox_id: None,
                notification_scope: None,
            },
        );
        state.acknowledge("main", "original");
        assert_eq!(
            state.pending.get("main").unwrap().destination,
            TrayDestination::Usage
        );
        state.acknowledge("main", "newer");
        assert!(!state.pending.contains_key("main"));
    }
}
