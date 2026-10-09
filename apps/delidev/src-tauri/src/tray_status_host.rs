// SPDX-License-Identifier: Apache-2.0
//! One auxiliary bundled document. Its ACL grants no product or preference API.
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicBool, Ordering},
};

use delidev_desktop::{
    NativeFailure,
    appearance::{AppearanceStore, PaletteColors, Theme},
    date_format::{DateFormatPreference, DateFormatStore},
    language::{LanguageStore, SupportedLanguage},
    tray_status::{Area, Target, place},
};
use tauri::{
    AppHandle, Manager, WebviewUrl, WebviewWindow, WebviewWindowBuilder, webview::NewWindowResponse,
};
use tauri_runtime_cef::CefRuntime;

pub const LABEL: &str = "tray-status";
#[derive(Default)]
pub struct PanelHost {
    instance: Mutex<Option<String>>,
    stopped: AtomicBool,
    failed: AtomicBool,
    focus_hidden: Mutex<Option<std::time::Instant>>,
    subscriber: Mutex<Option<(String, tauri::ipc::Channel<String>)>>,
}
#[derive(Clone, serde::Serialize)]
pub struct Snapshot {
    pub instance: String,
    pub windows: Vec<super::tray_host::PanelWindow>,
    pub more: bool,
    pub recent: Option<String>,
    pub theme: Theme,
    pub colors: PaletteColors,
    pub language: SupportedLanguage,
    pub date_format: DateFormatPreference,
}
#[derive(Clone, Copy, Debug, serde::Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Action {
    Show,
    Sessions,
    Inbox,
    Usage,
    Settings,
    Quit,
    Recovery,
}

fn document(url: &tauri::Url, instance: &str) -> bool {
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
        && url.path() == "/tray-status.html"
        && url.query() == Some(format!("instance={instance}").as_str())
        && url.fragment().is_none()
        && url.username().is_empty()
        && url.password().is_none()
}
fn admitted(window: &WebviewWindow<CefRuntime>, instance: &str) -> Result<(), NativeFailure> {
    let host = window.state::<Arc<PanelHost>>();
    if host.stopped.load(Ordering::Acquire) {
        return Err(NativeFailure::Stopped);
    }
    if window.label() != LABEL
        || host
            .instance
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .as_deref()
            != Some(instance)
    {
        return Err(NativeFailure::PermissionDenied);
    }
    // These commands are async. Native URL reads never run under tray locks or
    // on the CEF UI callback which they synchronize with.
    if !document(
        &window.url().map_err(|_| NativeFailure::PermissionDenied)?,
        instance,
    ) {
        return Err(NativeFailure::PermissionDenied);
    }
    if host.stopped.load(Ordering::Acquire)
        || host
            .instance
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .as_deref()
            != Some(instance)
    {
        return Err(NativeFailure::PermissionDenied);
    }
    Ok(())
}
#[tauri::command]
pub async fn watch_tray_status(
    window: WebviewWindow<CefRuntime>,
    instance: String,
    channel: tauri::ipc::Channel<String>,
) -> Result<(), NativeFailure> {
    admitted(&window, &instance)?;
    let host = window.state::<Arc<PanelHost>>();
    let previous = {
        let original = host.instance.lock().map_err(|_| NativeFailure::Busy)?;
        if original.as_deref() != Some(instance.as_str()) {
            return Err(NativeFailure::PermissionDenied);
        }
        let mut subscriber = host.subscriber.lock().map_err(|_| NativeFailure::Busy)?;
        if host.stopped.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        subscriber.replace((instance, channel))
    };
    drop(previous);
    Ok(())
}
#[tauri::command]
pub async fn read_tray_status(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    instance: String,
) -> Result<Snapshot, NativeFailure> {
    admitted(&window, &instance)?;
    let (windows, more, recent) = super::tray_host::panel_snapshot(&app)?;
    let appearance = app.state::<Arc<AppearanceStore>>().current();
    let result = Snapshot {
        instance: instance.clone(),
        windows,
        more,
        recent,
        theme: appearance.theme,
        colors: appearance
            .preferences
            .palette_colors()
            .map_err(|_| NativeFailure::StorageUnavailable)?,
        language: app
            .state::<Arc<LanguageStore>>()
            .current()
            .resolved_language,
        date_format: app.state::<Arc<DateFormatStore>>().current().date_format,
    };
    admitted(&window, &instance)?;
    Ok(result)
}
#[tauri::command]
pub async fn dismiss_tray_status(
    window: WebviewWindow<CefRuntime>,
    instance: String,
) -> Result<(), NativeFailure> {
    admitted(&window, &instance)?;
    window.hide().map_err(|_| NativeFailure::StorageUnavailable)
}
#[tauri::command]
pub async fn activate_tray_status(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    instance: String,
    action: Action,
    target: Option<Target>,
) -> Result<(), NativeFailure> {
    admitted(&window, &instance)?;
    match action {
        Action::Quit => {
            window
                .hide()
                .map_err(|_| NativeFailure::StorageUnavailable)?;
            admitted(&window, &instance)?;
            app.exit(0);
        }
        Action::Recovery => {
            if target.is_some()
                || app
                    .state::<Arc<super::ProductWindows>>()
                    .registry
                    .lock()
                    .map_err(|_| NativeFailure::Busy)?
                    .recent(None)
                    .is_some()
            {
                return Err(NativeFailure::InvalidEvidence);
            }
            window
                .hide()
                .map_err(|_| NativeFailure::StorageUnavailable)?;
            admitted(&window, &instance)?;
            super::window_host::restore_recent(&app);
        }
        _ => {
            let target = target.ok_or(NativeFailure::InvalidEvidence)?;
            super::tray_host::panel_activate(&app, &target, action, &window)?;
            admitted(&window, &instance)?;
            window
                .hide()
                .map_err(|_| NativeFailure::StorageUnavailable)?;
        }
    }
    tracing::info!(
        operation = "tray_panel",
        phase = "activation",
        ?action,
        state = "accepted"
    );
    Ok(())
}
impl PanelHost {
    /// Called only on the native event loop. Never authorizes a product URL.
    pub fn show_panel(&self, app: &AppHandle<CefRuntime>) {
        if self.stopped.load(Ordering::Acquire) {
            return;
        }
        if self.open(app, None).is_err() {
            self.failed.store(true, Ordering::Release);
            tracing::warn!(
                operation = "tray_panel",
                phase = "show",
                code = "presentation-unavailable"
            );
            super::tray_host::schedule(app);
        }
    }

    pub fn focus_lost(&self, window: &tauri::Window<CefRuntime>) {
        // Clicking the tray can deliver focus loss before its mouse-up
        // callback. Remember this bounded dismissal so that callback
        // closes the prior opening instead of immediately reopening it.
        // Keep the workaround until the pinned runtime supplies a
        // correlated tray-click event.
        *self.focus_hidden.lock().unwrap_or_else(|e| e.into_inner()) =
            Some(std::time::Instant::now());
        let _ = window.hide();
    }

    pub fn toggle(&self, app: &AppHandle<CefRuntime>, anchor: Option<Area>) {
        if self.stopped.load(Ordering::Acquire) {
            return;
        }
        if self
            .focus_hidden
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .take()
            .is_some_and(|at| at.elapsed() < std::time::Duration::from_millis(200))
        {
            return;
        }
        if let Some(window) = app.get_webview_window(LABEL)
            && window.is_visible().unwrap_or(false)
        {
            let _ = window.hide();
            return;
        }
        if self.open(app, anchor).is_err() {
            self.failed.store(true, Ordering::Release);
            tracing::warn!(
                operation = "tray_panel",
                phase = "show",
                code = "presentation-unavailable"
            );
            super::tray_host::schedule(app);
        }
    }

    fn open(&self, app: &AppHandle<CefRuntime>, anchor: Option<Area>) -> tauri::Result<()> {
        let window = if let Some(window) = app.get_webview_window(LABEL) {
            window
        } else {
            let instance = uuid::Uuid::now_v7().to_string();
            *self.instance.lock().unwrap_or_else(|e| e.into_inner()) = Some(instance.clone());
            let navigation_instance = instance.clone();
            let created = WebviewWindowBuilder::new(
                app,
                LABEL,
                WebviewUrl::App(format!("tray-status.html?instance={instance}").into()),
            )
            .title("DeliDev")
            .inner_size(380.0, 640.0)
            .decorations(false)
            .resizable(false)
            .visible(false)
            .skip_taskbar(true)
            .always_on_top(true)
            .shadow(true)
            .incognito(true)
            .on_navigation(move |url| document(url, &navigation_instance))
            .on_new_window(|_, _| NewWindowResponse::Deny)
            .on_page_load(|window, event| {
                if event.event() == tauri::webview::PageLoadEvent::Finished {
                    super::enable_document_accessibility(&window);
                }
            })
            .build();
            match created {
                Ok(window) => window,
                Err(error) => {
                    *self.instance.lock().unwrap_or_else(|e| e.into_inner()) = None;
                    return Err(error);
                }
            }
        };
        let point = anchor
            .map(|a| tauri::PhysicalPosition::new(a.x + a.width / 2.0, a.y + a.height / 2.0))
            .or_else(|| app.cursor_position().ok());
        let monitor = point
            .and_then(|p| app.monitor_from_point(p.x, p.y).ok().flatten())
            .or_else(|| app.primary_monitor().ok().flatten());
        if let Some(monitor) = monitor {
            let work = monitor.work_area();
            let placement = place(
                Area {
                    x: work.position.x as f64,
                    y: work.position.y as f64,
                    width: work.size.width as f64,
                    height: work.size.height as f64,
                },
                monitor.scale_factor(),
                anchor,
            );
            window.set_size(tauri::PhysicalSize::new(placement.width, placement.height))?;
            window.set_position(tauri::PhysicalPosition::new(placement.x, placement.y))?;
        } else {
            window.center()?;
        }
        if self.stopped.load(Ordering::Acquire) {
            return Ok(());
        }
        window.show()?;
        window.set_focus()?;
        self.failed.store(false, Ordering::Release);
        super::tray_host::schedule(app);
        notify(app);
        tracing::info!(operation = "tray_panel", phase = "show", state = "ready");
        Ok(())
    }

    pub fn failed(&self) -> bool {
        self.failed.load(Ordering::Acquire)
    }

    pub fn request_stop(&self, app: &AppHandle<CefRuntime>) {
        self.stopped.store(true, Ordering::Release);
        if let Some(window) = app.get_webview_window(LABEL) {
            let _ = window.hide();
        }
    }

    pub fn join(&self, app: &AppHandle<CefRuntime>) {
        self.request_stop(app);
        let subscriber = self
            .subscriber
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .take();
        drop(subscriber);
        if let Some(window) = app.get_webview_window(LABEL) {
            let _ = window.destroy();
        }
        *self.instance.lock().unwrap_or_else(|e| e.into_inner()) = None;
    }

    pub fn destroyed(&self) {
        let subscriber = self
            .subscriber
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .take();
        drop(subscriber);
        *self.instance.lock().unwrap_or_else(|e| e.into_inner()) = None;
    }
}
/// Notification carries only the original auxiliary instance. The document
/// obtains bounded retained data through its own verified read command.
pub fn notify(app: &AppHandle<CefRuntime>) {
    let host = app.state::<Arc<PanelHost>>();
    if host.stopped.load(Ordering::Acquire) {
        return;
    }
    let subscriber = host.subscriber.try_lock().ok().and_then(|v| v.clone());
    if let Some((instance, channel)) = subscriber
        && host
            .instance
            .try_lock()
            .is_ok_and(|value| value.as_deref() == Some(instance.as_str()))
    {
        let _ = channel.send(instance);
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn auxiliary_document_requires_exact_origin_path_and_original_instance() {
        let instance = "11111111-1111-4111-8111-111111111111";
        assert!(document(
            &format!("tauri://localhost/tray-status.html?instance={instance}")
                .parse()
                .unwrap(),
            instance
        ));
        for value in [
            "tauri://localhost/index.html",
            "tauri://localhost/tray-status.html?instance=replaced",
            "https://foreign.test/tray-status.html",
            "tauri://localhost/tray-status.html?instance=11111111-1111-4111-8111-111111111111&\
             extra=1",
        ] {
            assert!(!document(&value.parse().unwrap(), instance));
        }
    }
}
