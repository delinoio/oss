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
    localization::{Message, date, format as translated, number, text},
    presentation::{TrayDestination, TraySummary, menu_alias},
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
#[derive(Default)]
pub struct TrayHost {
    state: Mutex<State>,
    pub available: AtomicBool,
    stop: Arc<AtomicBool>,
    task: Mutex<Option<JoinHandle<()>>>,
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
        host.state
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .windows
            .insert(
                window.label().into(),
                Presentation {
                    scope: scope.clone(),
                    revision: 0,
                    summary: None,
                    received: Instant::now(),
                    stale: false,
                },
            );
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
        // File synchronization runs off the native UI and async executor
        // threads. Preserve order under the tray scope/revision lock,
        // and recheck the saved binding there so a closed/replaced
        // window cannot publish another profile.
        tauri::async_runtime::spawn_blocking(move || {
            // CEF URL reads wait on the native UI loop. Do them before
            // acquiring the publication lock so UI-thread shutdown
            // can join that lock safely.
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
                if !current.get(window.label()).is_some_and(|value| {
                    !value.closing
                        && value.instance == binding.instance
                        && value.profile.id == binding.profile.id
                }) {
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
                // Widget storage is a separate presentation outcome. Its closed
                // diagnostic and the widget's expiry remain truthful without
                // disabling an already accepted in-memory tray publication.
                let _ = super::widget_host::publish(
                    &binding.profile.id,
                    &binding.profile.name,
                    &summary,
                );
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

pub fn remove_widget(app: &AppHandle<CefRuntime>, id: &str) {
    let host = app.state::<Arc<TrayHost>>();
    if let Ok(_guard) = host.state.lock() {
        let _ = super::widget_host::remove(id);
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
    let mut state = host.state.lock().unwrap_or_else(|e| e.into_inner());
    state.actions.clear();
    let menu = Menu::new(app)?;
    menu.append(&MenuItem::with_id(
        app,
        "tray-show",
        text(Message::Show),
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
                format!("{} · Window {}", text(Message::Computer), entry.number)
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
                    text(Message::ConnectionStale)
                } else {
                    text(Message::Connected)
                },
                None,
                &mut state,
            )?;
            append(
                app,
                &submenu,
                &translated(Message::Updated, &[("at", &date(&overview.observed_at))]),
                None,
                &mut state,
            )?;
            append(
                app,
                &submenu,
                &translated(
                    Message::ActiveSessions,
                    &[
                        ("count", &number(&overview.active_sessions)),
                        (
                            "stale",
                            if stale {
                                text(Message::StaleSuffix)
                            } else {
                                ""
                            },
                        ),
                    ],
                ),
                action(TrayDestination::Sessions),
                &mut state,
            )?;
            append(
                app,
                &submenu,
                &translated(
                    Message::PendingRequests,
                    &[
                        ("count", &number(&overview.pending_interactions)),
                        (
                            "stale",
                            if stale {
                                text(Message::StaleSuffix)
                            } else {
                                ""
                            },
                        ),
                    ],
                ),
                action(TrayDestination::Inbox),
                &mut state,
            )?;
            append(
                app,
                &submenu,
                &translated(
                    Message::Workers,
                    &[
                        ("connected", &number(&overview.connected_workers)),
                        ("registered", &number(&overview.registered_workers)),
                        (
                            "stale",
                            if stale {
                                text(Message::StaleSuffix)
                            } else {
                                ""
                            },
                        ),
                    ],
                ),
                action(TrayDestination::Settings),
                &mut state,
            )?;
        } else {
            append(
                app,
                &submenu,
                text(Message::ConnectionUnavailable),
                None,
                &mut state,
            )?;
            append(
                app,
                &submenu,
                text(Message::Sessions),
                action(TrayDestination::Sessions),
                &mut state,
            )?;
            append(
                app,
                &submenu,
                text(Message::Inbox),
                action(TrayDestination::Inbox),
                &mut state,
            )?;
        }
        let usage = summary.as_ref().and_then(|v| v.usage.as_ref());
        let usage_text = match usage.and_then(|v| v.known_tokens.as_deref()) {
            Some(tokens) => translated(
                Message::TodayTokens,
                &[
                    ("tokens", &number(tokens)),
                    (
                        "incomplete",
                        if usage.is_some_and(|v| v.incomplete) {
                            text(Message::IncompleteSuffix)
                        } else {
                            ""
                        },
                    ),
                    (
                        "stale",
                        if stale {
                            text(Message::StaleSuffix)
                        } else {
                            ""
                        },
                    ),
                ],
            ),
            None => text(Message::TodayUnavailable).into(),
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
                text(Message::AccountQuotasStale)
            } else {
                text(Message::AccountQuotas)
            },
            true,
        )?;
        if let Some(values) = summary.as_ref().and_then(|v| v.accounts.as_ref()) {
            if values.entries.is_empty() {
                append(app, &accounts, text(Message::NoAccounts), None, &mut state)?;
            }
            for account in &values.entries {
                let quota = Submenu::new(
                    app,
                    if account.alias_hidden {
                        text(Message::AliasHidden).to_owned()
                    } else {
                        menu_alias(&account.alias)
                    },
                    true,
                )?;
                if account.windows.is_empty() {
                    append(
                        app,
                        &quota,
                        text(Message::QuotaUnavailable),
                        None,
                        &mut state,
                    )?;
                }
                for (index, window) in account.windows.iter().enumerate() {
                    append(
                        app,
                        &quota,
                        &translated(
                            Message::QuotaWindow,
                            &[
                                ("number", &(index + 1).to_string()),
                                ("quota", &window.label()),
                            ],
                        ),
                        None,
                        &mut state,
                    )?;
                    if let Some(at) = &window.observed_at {
                        append(
                            app,
                            &quota,
                            &translated(Message::ObservedAt, &[("at", &date(at))]),
                            None,
                            &mut state,
                        )?;
                    }
                    if let Some(at) = &window.reset_at {
                        append(
                            app,
                            &quota,
                            &translated(Message::ResetAt, &[("at", &date(at))]),
                            None,
                            &mut state,
                        )?;
                    }
                }
                if account.more {
                    append(
                        app,
                        &quota,
                        text(Message::MoreWindows),
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
                    text(Message::MoreAccounts),
                    action(TrayDestination::Settings),
                    &mut state,
                )?;
            }
        } else {
            append(
                app,
                &accounts,
                text(Message::QuotasUnavailable),
                None,
                &mut state,
            )?;
        }
        submenu.append(&accounts)?;
        append(
            app,
            &submenu,
            text(Message::WorkerSettings),
            action(TrayDestination::Settings),
            &mut state,
        )?;
        menu.append(&submenu)?;
    }
    menu.append(&MenuItem::with_id(
        app,
        "tray-quit",
        text(Message::Quit),
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
        .lock()
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
            text(Message::Show),
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

    pub fn stop(&self) {
        self.stop.store(true, Ordering::Release);
        // Join any admitted snapshot publication before the exit marker; queued
        // blocking publications observe stop under this same lock and cannot
        // make the persisted metadata fresh again after process shutdown.
        if let Ok(_guard) = self.state.lock() {
            super::widget_host::stop();
        }
        if let Some(task) = self.task.lock().unwrap_or_else(|e| e.into_inner()).take() {
            task.thread().unpark();
            let _ = task.join();
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

// A language change repaints retained observations without another RPC or
// scope.
pub fn refresh(app: &AppHandle<CefRuntime>) {
    schedule(app);
}
