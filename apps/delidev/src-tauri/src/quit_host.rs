// SPDX-License-Identifier: Apache-2.0
//! One original Quit attempt precedes every normal shutdown fence.
use std::{
    collections::BTreeMap,
    sync::{
        Arc, Condvar, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread::{self, JoinHandle},
    time::{Duration, Instant},
};

use delidev_desktop::{
    NativeFailure,
    localization::{Message, text},
    window_registry::{Entry, Phase, Role},
};
use tauri::{AppHandle, Emitter, Manager, WebviewWindow};
use tauri_runtime_cef::CefRuntime;

use super::{ProductWindows, WindowAuthority, capture_authority, recheck_registered};
#[derive(Clone, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct View {
    id: String,
    checking: bool,
    unknown: bool,
    count: String,
    present: bool,
    observe: bool,
}
struct Attempt {
    id: String,
    code: i32,
    started: Instant,
    entries: Vec<Entry>,
    local_revision: u64,
    authorities: BTreeMap<String, WindowAuthority>,
    observers: BTreeMap<String, String>,
    counts: BTreeMap<String, Option<u64>>,
    target: Option<String>,
    presented: bool,
    presentation_seen: Instant,
    checking: bool,
    unknown: bool,
    count: u128,
}
#[derive(Default)]
pub struct QuitHost {
    attempt: Mutex<Option<Attempt>>,
    changed: Condvar,
    worker: Mutex<Option<JoinHandle<()>>>,
    admitted: AtomicBool,
}
impl QuitHost {
    pub fn admitted(&self) -> bool {
        self.admitted.load(Ordering::Acquire)
    }

    pub fn join(&self) {
        if let Some(task) = self.worker.lock().unwrap_or_else(|e| e.into_inner()).take() {
            let _ = task.join();
        }
    }

    pub fn request(self: &Arc<Self>, app: &AppHandle<CefRuntime>, code: i32) {
        let mut state = self.attempt.lock().unwrap_or_else(|e| e.into_inner());
        if state.is_some() || self.admitted() {
            return;
        }
        // Read only the native registered scopes, never unopened saved
        // profiles.
        let windows = app.state::<Arc<ProductWindows>>();
        let registry = windows.registry.lock().unwrap_or_else(|e| e.into_inner());
        let entries = registry.entries();
        let local_revision = registry.local_revision;
        let local = registry.local_preference_scope_at(local_revision).ok();
        let target = registry.recent(None).map(|v| v.label);
        let bindings = windows.bindings.lock().unwrap_or_else(|e| e.into_inner());
        let mut authorities = BTreeMap::new();
        let mut scopes = Vec::new();
        let mut unknown = entries.is_empty();
        for entry in &entries {
            if entry.phase != Phase::Ready {
                scopes.push((entry.label.clone(), None, false));
                continue;
            }
            let saved = match &entry.role {
                Role::Local => None,
                Role::Saved(_) => bindings
                    .get(&entry.label)
                    .filter(|b| !b.closing && b.instance == entry.instance)
                    .map(|b| b.profile.clone()),
            };
            let server = match &entry.role {
                Role::Local => local.as_ref().map(|s| s.server_id.clone()),
                Role::Saved(_) => saved.as_ref().map(|s| s.server_id.clone()),
            };
            scopes.push((entry.label.clone(), server, true));
            authorities.insert(
                entry.label.clone(),
                WindowAuthority {
                    entry: entry.clone(),
                    local_revision,
                    saved,
                },
            );
        }
        let (observers, incomplete) = delidev_desktop::quit_confirmation::observers(scopes);
        unknown |= incomplete;
        drop(bindings);
        drop(registry);
        let id = uuid::Uuid::now_v7().to_string();
        *state = Some(Attempt {
            id: id.clone(),
            code,
            started: Instant::now(),
            entries,
            local_revision,
            authorities,
            observers,
            counts: BTreeMap::new(),
            target: target.clone(),
            presented: false,
            presentation_seen: Instant::now(),
            checking: true,
            unknown,
            count: 0,
        });
        drop(state);
        tracing::info!(operation = "desktop_quit_confirmation", phase = "checking");
        if let Some(label) = target {
            let handle = app.clone();
            let _ = app.run_on_main_thread(move || {
                if let Some(window) = handle.get_webview_window(&label) {
                    let _ = super::show(&window);
                }
            });
        }
        let _ = app.emit("quit-attempt", ());
        // Join a retired attempt before installing its successor's one worker.
        let previous = self.worker.lock().unwrap_or_else(|e| e.into_inner()).take();
        let host = Arc::clone(self);
        let app = app.clone();
        *self.worker.lock().unwrap_or_else(|e| e.into_inner()) = Some(thread::spawn(move || {
            if let Some(previous) = previous {
                let _ = previous.join();
            }
            host.check(app, id);
        }));
    }

    fn same_set(attempt: &Attempt, app: &AppHandle<CefRuntime>) -> bool {
        let windows = app.state::<Arc<ProductWindows>>();
        let Ok(registry) = windows.registry.lock() else {
            return false;
        };
        let current = registry.entries();
        if current.len() != attempt.entries.len()
            || attempt.entries.iter().any(|old| {
                !current.iter().any(|new| {
                    new.label == old.label
                        && new.instance == old.instance
                        && new.role == old.role
                        && new.phase == old.phase
                })
            })
            || (attempt.entries.iter().any(|e| e.role == Role::Local)
                && registry.local_revision != attempt.local_revision)
        {
            return false;
        }
        drop(registry);
        attempt
            .authorities
            .values()
            .all(|a| recheck_registered(app, a).is_ok())
    }

    fn check(self: Arc<Self>, app: AppHandle<CefRuntime>, id: String) {
        let mut state = self.attempt.lock().unwrap_or_else(|e| e.into_inner());
        loop {
            let Some(attempt) = state.as_mut().filter(|a| a.id == id) else {
                return;
            };
            let remaining = Duration::from_secs(5).saturating_sub(attempt.started.elapsed());
            if remaining.is_zero()
                || (attempt
                    .observers
                    .values()
                    .all(|label| attempt.counts.contains_key(label))
                    && (attempt.presented || attempt.target.is_none()))
            {
                break;
            }
            state = self
                .changed
                .wait_timeout(state, remaining)
                .unwrap_or_else(|e| e.into_inner())
                .0;
        }
        let attempt = state.as_mut().unwrap();
        attempt.checking = false;
        attempt.unknown |= !Self::same_set(attempt, &app)
            || attempt
                .observers
                .values()
                .any(|label| !matches!(attempt.counts.get(label), Some(Some(_))));
        let summary = delidev_desktop::quit_confirmation::summarize(
            attempt
                .observers
                .values()
                .map(|label| attempt.counts.get(label).copied().flatten()),
            !attempt.unknown,
        );
        attempt.count = summary.count;
        attempt.unknown = summary.unknown;
        let fallback = !attempt.presented
            || attempt
                .target
                .as_ref()
                .is_none_or(|label| app.get_webview_window(label).is_none());
        if fallback {
            attempt.target = None;
            attempt.unknown = true;
        }
        let zero = !attempt.unknown && attempt.count == 0;
        let code = attempt.code;
        let count = attempt.count.to_string();
        drop(state);
        if zero && self.admit(&app, &id, code, true) {
            return;
        }
        tracing::info!(operation = "desktop_quit_confirmation", phase = "warning");
        let _ = app.emit("quit-attempt", ());
        let fallback = if fallback {
            true
        } else {
            // Keep one joined owner until the original decision. A destroyed or
            // unresponsive presenter cannot strand a pending process-level
            // Quit.
            let mut state = self.attempt.lock().unwrap_or_else(|e| e.into_inner());
            loop {
                let Some(attempt) = state.as_mut().filter(|a| a.id == id) else {
                    return;
                };
                if attempt
                    .target
                    .as_ref()
                    .is_none_or(|label| app.get_webview_window(label).is_none())
                    || attempt.presentation_seen.elapsed() > Duration::from_secs(5)
                {
                    attempt.target = None;
                    attempt.unknown = true;
                    drop(state);
                    let _ = app.emit("quit-attempt", ());
                    break true;
                }
                state = self
                    .changed
                    .wait_timeout(state, Duration::from_millis(250))
                    .unwrap_or_else(|e| e.into_inner())
                    .0;
            }
        };
        if fallback {
            let mut body = text(Message::QuitUnknown).to_owned();
            if count != "0" {
                body.push('\n');
                body.push_str(&delidev_desktop::localization::format(
                    Message::QuitCount,
                    &[("count", &count)],
                ));
            }
            body.push_str("\n\n");
            body.push_str(text(Message::QuitExplanation));
            let approved = super::quit_dialog::confirm(
                &app,
                text(Message::QuitTitle).into(),
                body,
                text(Message::QuitCancel).into(),
                text(Message::QuitConfirm).into(),
            );
            if approved {
                self.admit(&app, &id, code, false);
            } else {
                self.cancel(&app, &id);
            }
        }
    }

    fn admit(&self, app: &AppHandle<CefRuntime>, id: &str, code: i32, silent: bool) -> bool {
        let mut state = self.attempt.lock().unwrap_or_else(|e| e.into_inner());
        let Some(attempt) = state.as_mut().filter(|a| a.id == id) else {
            return false;
        };
        if self.admitted() {
            return false;
        }
        let windows = app.state::<Arc<ProductWindows>>();
        let mut registry = windows.registry.lock().unwrap_or_else(|e| e.into_inner());
        if silent && !registry.stop_if_current(&attempt.entries, attempt.local_revision) {
            attempt.unknown = true;
            return false;
        }
        // Explicit confirmation also fences queued window creation before the
        // exit event. Silent admission checks and closes this gate atomically.
        registry.stop();
        self.admitted.store(true, Ordering::Release);
        drop(registry);
        *state = None;
        self.changed.notify_all();
        drop(state);
        tracing::info!(operation = "desktop_quit_confirmation", phase = "confirmed");
        app.exit(code);
        true
    }

    fn cancel(&self, app: &AppHandle<CefRuntime>, id: &str) {
        let mut state = self.attempt.lock().unwrap_or_else(|e| e.into_inner());
        if !state.as_ref().is_some_and(|a| a.id == id) {
            return;
        }
        *state = None;
        self.changed.notify_all();
        drop(state);
        tracing::info!(operation = "desktop_quit_confirmation", phase = "canceled");
        let _ = app.emit("quit-attempt", ());
    }
}
#[tauri::command]
pub async fn read_quit_attempt(
    window: WebviewWindow<CefRuntime>,
    host: tauri::State<'_, Arc<QuitHost>>,
) -> Result<Option<View>, NativeFailure> {
    let authority = capture_authority(&window)?;
    let mut state = host.attempt.lock().map_err(|_| NativeFailure::Busy)?;
    let Some(attempt) = state.as_mut() else {
        return Ok(None);
    };
    let original = attempt
        .authorities
        .get(window.label())
        .ok_or(NativeFailure::InvalidEvidence)?;
    if original.entry.instance != authority.entry.instance
        || original.local_revision != authority.local_revision
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    recheck_registered(window.app_handle(), original)?;
    let present = attempt.target.as_deref() == Some(window.label());
    if present && attempt.presented {
        attempt.presentation_seen = Instant::now();
    }
    Ok(Some(View {
        id: attempt.id.clone(),
        checking: attempt.checking,
        unknown: attempt.unknown,
        count: attempt.count.to_string(),
        present,
        observe: attempt
            .observers
            .values()
            .any(|label| label == window.label())
            && !attempt.counts.contains_key(window.label()),
    }))
}
#[tauri::command]
pub async fn observe_quit_attempt(
    window: WebviewWindow<CefRuntime>,
    host: tauri::State<'_, Arc<QuitHost>>,
    id: String,
    count: Option<String>,
) -> Result<(), NativeFailure> {
    let original = capture_authority(&window)?;
    let parsed = count
        .map(|v| {
            delidev_desktop::quit_confirmation::parse_count(&v)
                .map_err(|_| NativeFailure::InvalidEvidence)
        })
        .transpose()?;
    let mut state = host.attempt.lock().map_err(|_| NativeFailure::Busy)?;
    let attempt = state
        .as_mut()
        .filter(|a| a.id == id && a.checking && a.started.elapsed() < Duration::from_secs(5))
        .ok_or(NativeFailure::InvalidEvidence)?;
    let expected = attempt
        .authorities
        .get(window.label())
        .ok_or(NativeFailure::InvalidEvidence)?;
    if expected.entry.instance != original.entry.instance
        || expected.local_revision != original.local_revision
        || !attempt
            .observers
            .values()
            .any(|label| label == window.label())
        || attempt.counts.contains_key(window.label())
    {
        return Err(NativeFailure::InvalidEvidence);
    };
    recheck_registered(window.app_handle(), expected)?;
    attempt.counts.insert(window.label().into(), parsed);
    host.changed.notify_all();
    Ok(())
}
#[derive(serde::Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Decision {
    Cancel,
    Confirm,
}
#[tauri::command]
pub async fn decide_quit_attempt(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    host: tauri::State<'_, Arc<QuitHost>>,
    id: String,
    decision: Decision,
) -> Result<(), NativeFailure> {
    let original = capture_authority(&window)?;
    let mut state = host.attempt.lock().map_err(|_| NativeFailure::Busy)?;
    let attempt = state
        .as_mut()
        .filter(|a| a.id == id && a.target.as_deref() == Some(window.label()))
        .ok_or(NativeFailure::InvalidEvidence)?;
    let expected = attempt
        .authorities
        .get(window.label())
        .ok_or(NativeFailure::InvalidEvidence)?;
    if expected.entry.instance != original.entry.instance {
        return Err(NativeFailure::InvalidEvidence);
    };
    recheck_registered(&app, expected)?;
    if matches!(decision, Decision::Confirm)
        && !attempt.unknown
        && !QuitHost::same_set(attempt, &app)
    {
        attempt.unknown = true;
        drop(state);
        let _ = app.emit("quit-attempt", ());
        return Err(NativeFailure::InvalidEvidence);
    }
    let code = attempt.code;
    if matches!(decision, Decision::Confirm) && attempt.checking {
        return Err(NativeFailure::InvalidEvidence);
    }
    drop(state);
    match decision {
        Decision::Cancel => host.cancel(&app, &id),
        Decision::Confirm => {
            host.admit(&app, &id, code, false);
        }
    };
    Ok(())
}

#[tauri::command]
pub async fn present_quit_attempt(
    window: WebviewWindow<CefRuntime>,
    host: tauri::State<'_, Arc<QuitHost>>,
    id: String,
) -> Result<(), NativeFailure> {
    let original = capture_authority(&window)?;
    let mut state = host.attempt.lock().map_err(|_| NativeFailure::Busy)?;
    let attempt = state
        .as_mut()
        .filter(|a| a.id == id && a.target.as_deref() == Some(window.label()))
        .ok_or(NativeFailure::InvalidEvidence)?;
    let expected = attempt
        .authorities
        .get(window.label())
        .ok_or(NativeFailure::InvalidEvidence)?;
    if expected.entry.instance != original.entry.instance
        || expected.local_revision != original.local_revision
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    recheck_registered(window.app_handle(), expected)?;
    attempt.presented = true;
    attempt.presentation_seen = Instant::now();
    host.changed.notify_all();
    Ok(())
}
