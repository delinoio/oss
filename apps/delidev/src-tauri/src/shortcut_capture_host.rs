// SPDX-License-Identifier: Apache-2.0
use std::{
    collections::BTreeMap,
    sync::{Arc, Mutex},
    time::Instant,
};

use delidev_desktop::{
    NativeFailure, shortcut_capture::CaptureGate, shortcut_preferences::ShortcutStore,
};
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Manager, WebviewWindow, menu::Menu};
use tauri_runtime_cef::CefRuntime;

use super::{
    ProductWindows, WindowAuthority, capture_authority, recheck_authority, recheck_registered,
};

#[derive(Default)]
pub struct CaptureHost {
    state: Mutex<State>,
    pending_close: Mutex<BTreeMap<String, u64>>,
}
#[derive(Default)]
struct State {
    gate: CaptureGate,
    original: Option<WindowAuthority>,
    menu: Option<Menu<CefRuntime>>,
    inert: Option<Menu<CefRuntime>>,
}
#[derive(Clone, Copy, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Operation {
    Begin,
    Inspect,
    End,
}
#[derive(Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum Status {
    Active,
    Expired,
    Released,
}
#[derive(Serialize)]
pub struct Receipt {
    token: String,
    status: Status,
    remaining_ms: u64,
}
impl CaptureHost {
    pub fn epoch(&self) -> u64 {
        self.state
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .gate
            .epoch
    }

    pub fn allowed(&self, epoch: u64) -> bool {
        self.state
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .gate
            .queued_allowed(epoch)
    }

    pub fn fenced(&self) -> bool {
        self.state
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .gate
            .fenced()
    }

    pub fn menu_close(&self, label: &str) -> bool {
        let state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        if state.gate.fenced() {
            return false;
        }
        self.pending_close
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .insert(label.into(), state.gate.epoch);
        true
    }

    pub fn close_requested(&self, label: &str) -> bool {
        let pending = self
            .pending_close
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .remove(label);
        self.close_fenced(label) || pending.is_some_and(|epoch| !self.allowed(epoch))
    }

    pub fn close_fenced(&self, label: &str) -> bool {
        self.state
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .original
            .as_ref()
            .is_some_and(|o| o.entry.label == label)
    }

    // Native-loop only. Keep exact handles on partial failure and retain a
    // paused token on blur: a suspended renderer can focus this window again
    // before its own blur/end callback runs.
    fn restore(state: &State, app: &AppHandle<CefRuntime>) -> Result<(), NativeFailure> {
        if app.menu().as_ref().is_some_and(|m| {
            state.inert.as_ref().is_none_or(|i| i.id() != m.id())
                && state.menu.as_ref().is_none_or(|o| o.id() != m.id())
        }) {
            tracing::warn!(
                operation = "shortcut_capture_restore",
                code = "menu-generation-changed"
            );
            return Err(NativeFailure::InvalidEvidence);
        }
        app.set_menu(
            state
                .menu
                .as_ref()
                .ok_or(NativeFailure::InvalidEvidence)?
                .clone(),
        )
        .map_err(|_| NativeFailure::SidecarFailed)?;
        Ok(())
    }

    pub fn owned(&self) -> bool {
        self.state
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .gate
            .owned()
    }

    pub fn release(&self, app: &AppHandle<CefRuntime>) -> Result<(), NativeFailure> {
        let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        if state.gate.fenced() {
            Self::restore(&state, app)?;
        }
        state.gate.end();
        state.original = None;
        state.menu = None;
        state.inert = None;
        tracing::info!(operation = "shortcut_capture", state = "retired");
        Ok(())
    }

    fn pause(&self, app: &AppHandle<CefRuntime>) -> Result<(), NativeFailure> {
        let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        if state.gate.fenced() {
            Self::restore(&state, app)?;
            state.gate.pause();
        }
        Ok(())
    }

    fn resume(&self, app: &AppHandle<CefRuntime>) -> Result<(), NativeFailure> {
        let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        if state.gate.owned() && !state.gate.fenced() {
            let original = state
                .original
                .as_ref()
                .ok_or(NativeFailure::InvalidEvidence)?;
            recheck_registered(app, original)?;
            if app.menu().as_ref().map(|m| m.id()) != state.menu.as_ref().map(|m| m.id()) {
                return Err(NativeFailure::InvalidEvidence);
            }
            state.gate.resume();
            app.set_menu(
                state
                    .inert
                    .as_ref()
                    .ok_or(NativeFailure::InvalidEvidence)?
                    .clone(),
            )
            .map_err(|_| NativeFailure::SidecarFailed)?;
        }
        Ok(())
    }

    pub fn departure(&self, app: &AppHandle<CefRuntime>, label: &str, focused: bool) {
        let own = self.close_fenced(label);
        if own && focused {
            if self.resume(app).is_err() {
                if let Some(window) = app.get_webview_window(label) {
                    let _ = window.hide();
                }
                tracing::warn!(
                    operation = "shortcut_capture_resume",
                    code = "menu-restoration-uncertain"
                );
            }
        } else if own || focused {
            let _ = self.pause(app);
        }
    }

    pub fn destroyed(&self, app: &AppHandle<CefRuntime>, label: &str) {
        if self.close_fenced(label) {
            let _ = self.release(app);
        }
    }

    pub fn watchdog(&self, app: &AppHandle<CefRuntime>) {
        let owner = self
            .state
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .original
            .clone();
        if let Some(owner) = owner {
            let target = app.get_webview_window(&owner.entry.label);
            if target.is_none() || recheck_registered(app, &owner).is_err() {
                let _ = self.release(app);
                return;
            }
            let window = target.unwrap();
            if window.is_visible().unwrap_or(false)
                && window.is_focused().unwrap_or(false)
                && !window.is_minimized().unwrap_or(true)
            {
                let _ = self.resume(app);
            } else {
                let _ = self.pause(app);
            }
        }
    }
}

#[tauri::command]
pub async fn shortcut_capture_native(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    host: tauri::State<'_, Arc<CaptureHost>>,
    store: tauri::State<'_, Arc<ShortcutStore>>,
    operation: Operation,
    token: String,
    expected_revision: u32,
) -> Result<Receipt, NativeFailure> {
    if token.len() != 36 || uuid::Uuid::parse_str(&token).is_err() {
        return Err(NativeFailure::InvalidEvidence);
    }
    let original = capture_authority(&window)?;
    if super::is_local(&window) {
        super::trusted_local(&window)?;
    } else {
        super::saved_binding(&window, &windows)?;
    }
    if matches!(operation, Operation::Begin) {
        let snapshot = store.read();
        if snapshot.problem.is_some() || snapshot.revision != expected_revision {
            return Err(NativeFailure::InvalidEvidence);
        }
    }
    recheck_authority(&window, &original)?;
    let host = Arc::clone(host.inner());
    let app_loop = app.clone();
    let owned = original.clone();
    let (send, receive) = tokio::sync::oneshot::channel();
    app.run_on_main_thread(move || {
        let result = (|| {
            recheck_registered(&app_loop, &owned)?;
            match operation {
                Operation::Begin => {
                    let target = app_loop
                        .get_webview_window(&owned.entry.label)
                        .ok_or(NativeFailure::PermissionDenied)?;
                    if !target.is_focused().unwrap_or(false)
                        || !target.is_visible().unwrap_or(false)
                        || target.is_minimized().unwrap_or(true)
                    {
                        return Err(NativeFailure::PermissionDenied);
                    }
                    let mut state = host.state.lock().unwrap_or_else(|e| e.into_inner());
                    if state.gate.owned() {
                        return Err(NativeFailure::Busy);
                    }
                    let menu = app_loop.menu().ok_or(NativeFailure::InvalidEvidence)?;
                    // Product windows have app-wide menus only. Fail closed if
                    // a later feature gives any native
                    // window a separate menu.
                    if app_loop
                        .windows()
                        .values()
                        .any(|w| w.menu().is_some_and(|m| m.id() != menu.id()))
                    {
                        return Err(NativeFailure::InvalidEvidence);
                    }
                    let inert = Menu::new(&app_loop).map_err(|_| NativeFailure::SidecarFailed)?;
                    if !state.gate.begin(
                        owned.entry.instance.clone(),
                        token.clone(),
                        Instant::now(),
                    ) {
                        return Err(NativeFailure::InvalidEvidence);
                    }
                    state.original = Some(owned.clone());
                    state.menu = Some(menu);
                    state.inert = Some(inert.clone());
                    // set_menu is physically acknowledged here: nested CEF UI
                    // operations execute inline on this loop. Retain all
                    // handles if removal/replacement
                    // partially fails.
                    app_loop
                        .set_menu(inert)
                        .map_err(|_| NativeFailure::SidecarFailed)?;
                    tracing::info!(operation = "shortcut_capture", state = "admitted");
                }
                Operation::Inspect | Operation::End => {
                    let state = host.state.lock().unwrap_or_else(|e| e.into_inner());
                    if state.gate.owned() && !state.gate.matches(&owned.entry.instance, &token) {
                        return Err(NativeFailure::PermissionDenied);
                    }
                    drop(state);
                    if matches!(operation, Operation::End) {
                        host.release(&app_loop)?;
                        if !host
                            .state
                            .lock()
                            .unwrap_or_else(|e| e.into_inner())
                            .gate
                            .retire(owned.entry.instance.clone(), token.clone())
                        {
                            return Err(NativeFailure::Busy);
                        }
                    }
                }
            }
            let state = host.state.lock().unwrap_or_else(|e| e.into_inner());
            let remaining = state.gate.remaining(Instant::now());
            Ok(Receipt {
                token,
                status: if !state.gate.owned() {
                    Status::Released
                } else if remaining == 0 {
                    Status::Expired
                } else {
                    Status::Active
                },
                remaining_ms: remaining,
            })
        })();
        let _ = send.send(result);
    })
    .map_err(|_| NativeFailure::SidecarFailed)?;
    let receipt = receive.await.map_err(|_| NativeFailure::SidecarFailed)??;
    recheck_authority(&window, &original)?;
    Ok(receipt)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn original() -> WindowAuthority {
        let mut registry = delidev_desktop::window_registry::Registry::default();
        WindowAuthority {
            entry: registry
                .reserve(delidev_desktop::window_registry::Role::Local, true)
                .unwrap(),
            local_revision: 0,
            saved: None,
        }
    }
    #[test]
    fn shortcut_capture_native_queued_close_cannot_cross_capture_epoch() {
        let host = CaptureHost::default();
        let original = original();
        assert!(host.menu_close(&original.entry.label));
        {
            let mut state = host.state.lock().unwrap();
            state.gate.begin(
                original.entry.instance.clone(),
                "original-token".into(),
                Instant::now(),
            );
            state.original = Some(original.clone());
        }
        assert!(!host.menu_close(&original.entry.label));
        assert!(host.close_requested(&original.entry.label));
        {
            let mut state = host.state.lock().unwrap();
            state.gate.end();
            state.original = None;
        }
        assert!(host.menu_close(&original.entry.label));
        {
            let mut state = host.state.lock().unwrap();
            state.gate.begin(
                original.entry.instance.clone(),
                "next-token".into(),
                Instant::now(),
            );
            state.gate.end();
        }
        assert!(host.close_requested(&original.entry.label));
        assert!(!host.close_requested(&original.entry.label));
    }
    #[test]
    fn shortcut_capture_native_paused_context_retains_exact_owner_and_rejects_adoption() {
        let host = CaptureHost::default();
        let owner = original();
        {
            let mut state = host.state.lock().unwrap();
            state
                .gate
                .begin(owner.entry.instance.clone(), "token".into(), Instant::now());
            state.original = Some(owner.clone());
            state.gate.pause();
        }
        assert!(host.owned());
        assert!(!host.fenced());
        assert!(host.close_fenced(&owner.entry.label));
        let state = host.state.lock().unwrap();
        assert!(state.gate.matches(&owner.entry.instance, "token"));
        assert!(!state.gate.matches("replacement-instance", "token"));
        assert!(
            !state
                .gate
                .matches(&owner.entry.instance, "replacement-token")
        );
    }
    #[test]
    fn shortcut_capture_native_operation_is_closed() {
        for operation in ["begin", "inspect", "end"] {
            assert!(serde_json::from_str::<Operation>(&format!("\"{operation}\"")).is_ok());
        }
        for operation in ["renew", "quit", "close", "capture-arbitrary-window"] {
            assert!(serde_json::from_str::<Operation>(&format!("\"{operation}\"")).is_err());
        }
    }
}
