// SPDX-License-Identifier: Apache-2.0
//! One process-local selected-window badge owner. No credentials, RPC or Inbox
//! content.
use std::{
    collections::BTreeMap,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

use delidev_desktop::NativeFailure;
use tauri::{AppHandle, Emitter, Manager, WebviewWindow};
use tauri_runtime_cef::CefRuntime;

use super::{ProductWindows, WindowAuthority, capture_authority, recheck_registered};
#[path = "badge_pixels.rs"]
mod pixels;
struct Publication {
    authority: WindowAuthority,
    scope: String,
    revision: u32,
}
#[derive(Default)]
struct State {
    records: BTreeMap<String, Publication>,
    selected: Option<(String, String)>,
    generation: u32,
    count: u64,
    received: Option<Instant>,
    paint: u64,
}
#[derive(Default)]
pub struct BadgeHost {
    state: Mutex<State>,
    stopped: AtomicBool,
    task: Mutex<Option<std::thread::JoinHandle<()>>>,
}
#[derive(serde::Serialize)]
pub struct Selection {
    scope: String,
    generation: u32,
}
impl BadgeHost {
    pub fn start(self: &Arc<Self>, app: &AppHandle<CefRuntime>) {
        let host = Arc::clone(self);
        let app = app.clone();
        let task = std::thread::spawn(move || {
            while !host.stopped.load(Ordering::Acquire) {
                host.reconcile(&app);
                std::thread::sleep(Duration::from_millis(250));
            }
        });
        *self.task.lock().unwrap_or_else(|e| e.into_inner()) = Some(task);
    }

    pub fn stop(&self, app: &AppHandle<CefRuntime>) {
        if self.stopped.swap(true, Ordering::AcqRel) {
            return;
        }
        tracing::info!(operation = "inbox_badge_lifecycle", state = "quit-fenced");
        if let Ok(mut state) = self.state.lock() {
            state.records.clear();
            state.selected = None;
            state.count = 0;
            state.received = None;
            state.paint = state.paint.saturating_add(1);
        }
        self.queue_paint(app);
    }

    pub fn join(&self) {
        if let Some(task) = self.task.lock().unwrap_or_else(|e| e.into_inner()).take() {
            let _ = task.join();
        }
    }

    pub fn retire(&self, app: &AppHandle<CefRuntime>, label: &str) {
        if let Ok(mut state) = self.state.lock() {
            state.records.remove(label);
            if state
                .selected
                .as_ref()
                .is_some_and(|(selected, _)| selected == label)
            {
                state.selected = None;
            }
        }
        self.reconcile(app);
    }

    pub fn reconcile(&self, app: &AppHandle<CefRuntime>) {
        if self.stopped.load(Ordering::Acquire) {
            return;
        }
        let windows = app.state::<Arc<ProductWindows>>();
        let recent = windows
            .registry
            .lock()
            .ok()
            .and_then(|registry| registry.recent(None));
        let mut changed = false;
        let mut selection_changed = false;
        if let Ok(mut state) = self.state.lock() {
            state
                .records
                .retain(|_, record| recheck_registered(app, &record.authority).is_ok());
            let selected = recent.map(|entry| (entry.label, entry.instance));
            if state.selected != selected {
                state.selected = selected;
                state.generation = match state.generation.checked_add(1) {
                    Some(value) => value,
                    None => {
                        self.stopped.store(true, Ordering::Release);
                        state.records.clear();
                        state.selected = None;
                        u32::MAX
                    }
                };
                state.count = 0;
                state.received = None;
                changed = true;
                selection_changed = true;
            }
            let missing = state
                .selected
                .as_ref()
                .is_none_or(|(label, _)| !state.records.contains_key(label));
            if (missing
                || state
                    .received
                    .is_some_and(|at| !pixels::fresh(at.elapsed())))
                && (state.count != 0 || state.received.is_some())
            {
                state.count = 0;
                state.received = None;
                changed = true;
            }
            if changed {
                state.paint = state.paint.saturating_add(1);
            }
        }
        if selection_changed {
            let _ = app.emit("inbox-badge-selection", ());
        }
        if changed {
            self.queue_paint(app);
        }
    }

    fn queue_paint(&self, app: &AppHandle<CefRuntime>) {
        let app = app.clone();
        let native = app.clone();
        let revision = self.state.lock().map(|state| state.paint).unwrap_or(0);
        let _ = app.run_on_main_thread(move || {
            let host = native.state::<Arc<BadgeHost>>();
            let Ok(state) = host.state.lock() else {
                return;
            };
            if state.paint != revision {
                return;
            }
            let current = native
                .state::<Arc<ProductWindows>>()
                .registry
                .lock()
                .ok()
                .and_then(|registry| registry.recent(None))
                .map(|entry| (entry.label, entry.instance));
            if current != state.selected && !host.stopped.load(Ordering::Acquire) {
                return;
            }
            let valid = state
                .selected
                .as_ref()
                .and_then(|(label, _)| state.records.get(label))
                .is_some_and(|record| recheck_registered(&native, &record.authority).is_ok());
            if let Err(code) = paint(
                &native,
                if valid && state.received.is_some_and(|at| pixels::fresh(at.elapsed())) {
                    state.count
                } else {
                    0
                },
            ) {
                tracing::warn!(operation = "inbox_badge_presentation", ?code);
            }
        });
    }
}
#[tauri::command]
pub async fn begin_inbox_badge(
    window: WebviewWindow<CefRuntime>,
    host: tauri::State<'_, Arc<BadgeHost>>,
) -> Result<String, NativeFailure> {
    if host.stopped.load(Ordering::Acquire) {
        return Err(NativeFailure::Stopped);
    }
    let authority = capture_authority(&window)?;
    let scope = uuid::Uuid::now_v7().to_string();
    {
        let mut state = host.state.lock().map_err(|_| NativeFailure::Busy)?;
        state.records.insert(
            window.label().into(),
            Publication {
                authority,
                scope: scope.clone(),
                revision: 0,
            },
        );
        if state
            .selected
            .as_ref()
            .is_some_and(|(label, _)| label == window.label())
        {
            state.generation = state.generation.checked_add(1).ok_or(NativeFailure::Busy)?;
            state.count = 0;
            state.received = None;
            state.paint = state.paint.saturating_add(1);
        }
    }
    host.reconcile(window.app_handle());
    host.queue_paint(window.app_handle());
    let _ = window.app_handle().emit("inbox-badge-selection", ());
    Ok(scope)
}
#[tauri::command]
pub async fn read_inbox_badge_selection(
    window: WebviewWindow<CefRuntime>,
    host: tauri::State<'_, Arc<BadgeHost>>,
    scope: String,
) -> Result<Option<Selection>, NativeFailure> {
    if scope.len() != 36 {
        return Err(NativeFailure::InvalidEvidence);
    }
    let authority = capture_authority(&window)?;
    host.reconcile(window.app_handle());
    let state = host.state.lock().map_err(|_| NativeFailure::Busy)?;
    let record = state
        .records
        .get(window.label())
        .filter(|record| record.scope == scope)
        .ok_or(NativeFailure::InvalidEvidence)?;
    recheck_registered(window.app_handle(), &record.authority)?;
    Ok(state
        .selected
        .as_ref()
        .filter(|(label, instance)| {
            label == window.label() && instance == &authority.entry.instance
        })
        .map(|_| Selection {
            scope,
            generation: state.generation,
        }))
}
#[tauri::command]
pub async fn publish_inbox_badge(
    window: WebviewWindow<CefRuntime>,
    host: tauri::State<'_, Arc<BadgeHost>>,
    scope: String,
    generation: u32,
    revision: u32,
    count: Option<String>,
) -> Result<(), NativeFailure> {
    if scope.len() != 36 {
        return Err(NativeFailure::InvalidEvidence);
    }
    let authority = capture_authority(&window)?;
    let value = match count {
        None => None,
        Some(value) => {
            if value.len() > 20 {
                return Err(NativeFailure::InvalidEvidence);
            }
            let parsed = value
                .parse::<u64>()
                .map_err(|_| NativeFailure::InvalidEvidence)?;
            if parsed.to_string() != value {
                return Err(NativeFailure::InvalidEvidence);
            }
            Some(parsed)
        }
    };
    host.reconcile(window.app_handle());
    {
        let mut state = host.state.lock().map_err(|_| NativeFailure::Busy)?;
        if host.stopped.load(Ordering::Acquire)
            || state.generation != generation
            || !state.selected.as_ref().is_some_and(|(label, instance)| {
                label == window.label() && instance == &authority.entry.instance
            })
        {
            tracing::debug!(
                operation = "inbox_badge_publication",
                code = "obsolete-selection"
            );
            return Err(NativeFailure::InvalidEvidence);
        }
        let record = state
            .records
            .get_mut(window.label())
            .filter(|record| record.scope == scope && revision > record.revision)
            .ok_or(NativeFailure::InvalidEvidence)?;
        recheck_registered(window.app_handle(), &record.authority)?;
        record.revision = revision;
        state.count = value.unwrap_or(0);
        state.received = value.map(|_| Instant::now());
        state.paint = state.paint.saturating_add(1);
    }
    host.queue_paint(window.app_handle());
    Ok(())
}
fn paint(app: &AppHandle<CefRuntime>, count: u64) -> Result<(), NativeFailure> {
    #[cfg(any(target_os = "macos", windows))]
    let label = pixels::label(count);
    #[cfg(target_os = "macos")]
    {
        let marker = objc2::MainThreadMarker::new().ok_or(NativeFailure::InvalidEvidence)?;
        let app = objc2_app_kit::NSApplication::sharedApplication(marker);
        let value = (count > 0).then(|| objc2_foundation::NSString::from_str(&label));
        app.dockTile().setBadgeLabel(value.as_deref());
        return Ok(());
    }
    #[cfg(not(target_os = "macos"))]
    {
        let base = app
            .default_window_icon()
            .ok_or(NativeFailure::InvalidEvidence)?;
        let rgba = pixels::render(base.rgba(), base.width(), base.height(), count)
            .ok_or(NativeFailure::InvalidEvidence)?;
        let mut unavailable = false;
        for window in app.webview_windows().values() {
            if app
                .state::<Arc<ProductWindows>>()
                .registry
                .lock()
                .ok()
                .and_then(|registry| registry.admitted(window.label()).ok())
                .is_none()
            {
                continue;
            }
            if window
                .set_icon(tauri::image::Image::new_owned(
                    rgba.clone(),
                    base.width(),
                    base.height(),
                ))
                .is_err()
            {
                unavailable = true;
                tracing::warn!(
                    operation = "inbox_badge_presentation",
                    code = "window-icon-unavailable"
                );
            }
            #[cfg(windows)]
            if overlay(window, count, &label).is_err() {
                unavailable = true;
                tracing::warn!(
                    operation = "inbox_badge_presentation",
                    code = "taskbar-overlay-unavailable"
                );
            }
        }
        #[cfg(target_os = "linux")]
        if let Some(tray) = app.tray_by_id("delidev-status") {
            if tray
                .set_icon(Some(tauri::image::Image::new_owned(
                    rgba,
                    base.width(),
                    base.height(),
                )))
                .is_err()
            {
                unavailable = true;
                tracing::warn!(
                    operation = "inbox_badge_presentation",
                    code = "tray-icon-unavailable"
                );
            }
            if tray.set_tooltip(Some(description(count))).is_err() {
                unavailable = true;
                tracing::warn!(
                    operation = "inbox_badge_presentation",
                    code = "tray-description-unavailable"
                );
            }
        }
        if unavailable {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(())
    }
}
#[cfg(not(target_os = "macos"))]
fn description(count: u64) -> String {
    use delidev_desktop::localization::{Message, text};
    if count == 0 {
        "DeliDev".into()
    } else {
        text(Message::InboxBadgeUnread).replace("{{count}}", &count.to_string())
    }
}
#[cfg(windows)]
fn overlay(
    window: &WebviewWindow<CefRuntime>,
    count: u64,
    _label: &str,
) -> Result<(), NativeFailure> {
    use windows::{
        Win32::{
            System::Com::{CLSCTX_INPROC_SERVER, CoCreateInstance},
            UI::{
                Shell::{ITaskbarList3, TaskbarList},
                WindowsAndMessaging::{CreateIcon, DestroyIcon, HICON},
            },
        },
        core::PCWSTR,
    };
    // The native UI thread already owns COM. Keep the original window handle
    // and destroy the temporary icon after the shell copies it, including
    // failures.
    unsafe {
        let hwnd = window.hwnd().map_err(|_| NativeFailure::InvalidEvidence)?;
        let shell: ITaskbarList3 = CoCreateInstance(&TaskbarList, None, CLSCTX_INPROC_SERVER)
            .map_err(|_| NativeFailure::InvalidEvidence)?;
        // Every fresh taskbar interface must initialize before setting or
        // clearing an overlay. Fail before allocating an icon when
        // initialization fails.
        shell.HrInit().map_err(|_| NativeFailure::InvalidEvidence)?;
        let icon = if count == 0 {
            HICON::default()
        } else {
            let mut rgba = pixels::render(&vec![0; 32 * 32 * 4], 32, 32, count)
                .ok_or(NativeFailure::InvalidEvidence)?;
            let mut mask = vec![0; 32 * 4];
            for (index, pixel) in rgba.chunks_exact_mut(4).enumerate() {
                pixel.swap(0, 2);
                if pixel[3] == 0 {
                    mask[index / 8] |= 1 << (7 - index % 8);
                }
            }
            CreateIcon(None, 32, 32, 1, 32, mask.as_ptr(), rgba.as_ptr())
                .map_err(|_| NativeFailure::InvalidEvidence)?
        };
        let text: Vec<u16> = description(count).encode_utf16().chain(Some(0)).collect();
        let result = shell
            .SetOverlayIcon(
                windows::Win32::Foundation::HWND(hwnd.0),
                icon,
                PCWSTR(text.as_ptr()),
            )
            .map_err(|_| NativeFailure::InvalidEvidence);
        if count > 0 {
            let _ = DestroyIcon(icon);
        }
        result
    }
}
