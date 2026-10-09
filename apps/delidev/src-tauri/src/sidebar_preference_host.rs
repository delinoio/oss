// SPDX-License-Identifier: Apache-2.0
use std::sync::Arc;

use delidev_desktop::{
    NativeFailure,
    sidebar_preference::{SidebarPreference, SidebarSnapshot, SidebarStore},
};
use tauri::{AppHandle, Emitter, Manager, WebviewWindow};
use tauri_runtime_cef::CefRuntime;

use super::{ProductWindows, saved_binding, trusted_local};

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
pub async fn read_sidebar_preference(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    store: tauri::State<'_, Arc<SidebarStore>>,
) -> Result<SidebarSnapshot, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let storage_authority = original_authority.clone();
    let result = async {
        authorized(&window, &windows)?;
        let store = Arc::clone(store.inner());
        let windows = Arc::clone(windows.inner());
        tauri::async_runtime::spawn_blocking(move || {
            let _publication = store.presentation();
            super::recheck_authority(&window, &storage_authority)?;
            let snapshot = store.read();
            tracing::info!(operation = "device_sidebar_preference_read", revision = snapshot.revision, problem = ?snapshot.problem);
            if snapshot.problem.is_none() {
                publish(&app, &windows, &snapshot);
            }
            Ok(snapshot)
        })
            .await
            .map_err(|_| NativeFailure::StorageUnavailable)?
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}

#[tauri::command]
pub async fn update_sidebar_preference(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    store: tauri::State<'_, Arc<SidebarStore>>,
    sidebar_preference: SidebarPreference,
    expected_revision: u32,
) -> Result<SidebarSnapshot, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let storage_authority = original_authority.clone();
    let result = async {
        authorized(&window, &windows)?;
        let store = Arc::clone(store.inner());
        let windows = Arc::clone(windows.inner());
        tauri::async_runtime::spawn_blocking(move || {
            let _publication = store.presentation();
            super::recheck_authority(&window, &storage_authority)?;
            let snapshot = store.update(sidebar_preference, expected_revision);
            tracing::info!(operation = "device_sidebar_preference_update", revision = snapshot.revision, problem = ?snapshot.problem);
            if snapshot.problem.is_none() {
                publish(&app, &windows, &snapshot);
            }
            Ok(snapshot)
        })
        .await
        .map_err(|_| NativeFailure::StorageUnavailable)?
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}

fn publish(app: &AppHandle<CefRuntime>, windows: &ProductWindows, snapshot: &SidebarSnapshot) {
    // The store presentation lock orders commits and publication across
    // windows. External child views receive no preference events or storage
    // authority.
    for window in app.webview_windows().into_values() {
        let mut admission = authorized(&window, windows);
        for _ in 0..5 {
            if !matches!(admission, Err(NativeFailure::Busy)) {
                break;
            }
            std::thread::sleep(std::time::Duration::from_millis(20));
            admission = authorized(&window, windows);
        }
        if admission.is_ok() && window.emit("sidebar-preference-changed", snapshot).is_err() {
            tracing::warn!(
                operation = "sidebar_preference_event",
                code = "delivery-unavailable"
            );
        } else if matches!(admission, Err(NativeFailure::Busy)) {
            tracing::warn!(
                operation = "sidebar_preference_event",
                code = "binding-busy"
            );
        }
    }
}
