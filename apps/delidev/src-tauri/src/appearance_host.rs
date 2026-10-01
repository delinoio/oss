// SPDX-License-Identifier: Apache-2.0
use std::sync::Arc;

use delidev_desktop::{
    NativeFailure,
    appearance::{AppearanceSnapshot, AppearanceStore, Theme},
};
use tauri::{AppHandle, Cef, Emitter, Manager, WebviewWindow};

use super::{SavedWindows, saved_binding, trusted_main};

fn authorized(window: &WebviewWindow<Cef>, windows: &SavedWindows) -> Result<(), NativeFailure> {
    if window.label() == "main" {
        trusted_main(window)
    } else {
        saved_binding(window, windows).map(|_| ())
    }
}

#[tauri::command]
pub async fn read_appearance(
    window: WebviewWindow<Cef>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
    store: tauri::State<'_, Arc<AppearanceStore>>,
) -> Result<AppearanceSnapshot, NativeFailure> {
    authorized(&window, &windows)?;
    let store = Arc::clone(store.inner());
    tauri::async_runtime::spawn_blocking(move || store.read())
        .await
        .map_err(|_| NativeFailure::StorageUnavailable)
}

#[tauri::command]
pub async fn update_appearance(
    window: WebviewWindow<Cef>,
    app: AppHandle<Cef>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
    store: tauri::State<'_, Arc<AppearanceStore>>,
    theme: Theme,
    expected_revision: u32,
) -> Result<AppearanceSnapshot, NativeFailure> {
    authorized(&window, &windows)?;
    let store = Arc::clone(store.inner());
    let windows = Arc::clone(windows.inner());
    tauri::async_runtime::spawn_blocking(move || {
        let snapshot = store.update(theme, expected_revision);
        if snapshot.problem.is_none() {
            // Only committed changes are broadcast. Native URL authorization
            // stays off the CEF UI loop and external child views receive none.
            for window in app.webview_windows().into_values() {
                let mut admission = authorized(&window, &windows);
                // Retry brief saved-window label contention without holding
                // its binding lock or blocking the CEF UI loop.
                for _ in 0..5 {
                    if !matches!(admission, Err(NativeFailure::Busy)) {
                        break;
                    }
                    std::thread::sleep(std::time::Duration::from_millis(20));
                    admission = authorized(&window, &windows);
                }
                if admission.is_ok() && window.emit("appearance-changed", &snapshot).is_err() {
                    tracing::warn!(
                        operation = "appearance_event",
                        code = "delivery-unavailable"
                    );
                } else if matches!(admission, Err(NativeFailure::Busy)) {
                    tracing::warn!(operation = "appearance_event", code = "binding-busy");
                }
            }
        }
        Ok(snapshot)
    })
    .await
    .map_err(|_| NativeFailure::StorageUnavailable)?
}
