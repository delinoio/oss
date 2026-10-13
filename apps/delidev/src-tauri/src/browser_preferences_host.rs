// SPDX-License-Identifier: Apache-2.0
use std::sync::Arc;

use delidev_desktop::{
    Connector, NativeFailure, browser_discovery,
    browser_preferences::{BrowserChoices, BrowserSnapshot, BrowserStore},
};
use serde::Serialize;
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

#[derive(Serialize)]
pub struct InventoryResponse {
    browsers: Vec<browser_discovery::BrowserEntry>,
}

#[tauri::command]
pub async fn read_browser_preferences(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    store: tauri::State<'_, Arc<BrowserStore>>,
) -> Result<BrowserSnapshot, NativeFailure> {
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
            tracing::info!(operation = "device_browser_preferences_read", revision = snapshot.revision, problem = ?snapshot.problem);
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
pub async fn update_browser_preferences(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    store: tauri::State<'_, Arc<BrowserStore>>,
    choices: BrowserChoices,
    expected_revision: u32,
) -> Result<BrowserSnapshot, NativeFailure> {
    mutate(
        window,
        app,
        windows,
        store,
        expected_revision,
        Some(choices),
    )
    .await
}

/// Explicit recovery from corrupt or unsupported storage. Healthy storage is
/// never reset through this command.
#[tauri::command]
pub async fn reset_browser_preferences(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    store: tauri::State<'_, Arc<BrowserStore>>,
    expected_revision: u32,
) -> Result<BrowserSnapshot, NativeFailure> {
    mutate(window, app, windows, store, expected_revision, None).await
}

async fn mutate(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    store: tauri::State<'_, Arc<BrowserStore>>,
    expected_revision: u32,
    choices: Option<BrowserChoices>,
) -> Result<BrowserSnapshot, NativeFailure> {
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
            let snapshot = match choices {
                Some(choices) => {
                    // A newly selected identity must be registered right now.
                    // An unchanged saved identity may be unavailable and stays.
                    browser_discovery::require_installed_changes(
                        &store.current().choices,
                        &choices,
                    )?;
                    store.update(choices, expected_revision)
                }
                None => store.reset(expected_revision),
            };
            tracing::info!(operation = "device_browser_preferences_update", revision = snapshot.revision, problem = ?snapshot.problem);
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

/// Refreshes the native HTTP(S) handler inventory. A discovery failure rejects
/// with `browser-discovery-failed`, which is distinct from an empty list.
#[tauri::command]
pub async fn read_browser_inventory(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
) -> Result<InventoryResponse, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        authorized(&window, &windows)?;
        tauri::async_runtime::spawn_blocking(|| {
            let browsers = browser_discovery::entries();
            match &browsers {
                Ok(list) => tracing::info!(
                    operation = "browser_inventory",
                    phase = "read",
                    count = list.len()
                ),
                Err(code) => {
                    tracing::warn!(operation = "browser_inventory", phase = "failed", ?code)
                }
            }
            browsers.map(|browsers| InventoryResponse { browsers })
        })
        .await
        .map_err(|_| NativeFailure::BrowserDiscoveryFailed)?
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}

/// User-activated conversation or terminal HTTP(S) link. The renderer supplies
/// only a candidate; native code validates, classifies and resolves the choice.
#[tauri::command]
pub async fn open_external_url(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    connector: tauri::State<'_, Arc<Connector>>,
    store: tauri::State<'_, Arc<BrowserStore>>,
    url: String,
) -> Result<(), NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        authorized(&window, &windows)?;
        let connector = Arc::clone(connector.inner());
        let store = Arc::clone(store.inner());
        let result =
            tauri::async_runtime::spawn_blocking(move || connector.open_external_url(&store, &url))
                .await
                .map_err(|_| NativeFailure::SidecarFailed)?;
        authorized(&window, &windows)?;
        result
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}

fn publish(app: &AppHandle<CefRuntime>, windows: &ProductWindows, snapshot: &BrowserSnapshot) {
    // The store presentation lock orders commits and publication across
    // windows. External child views receive no preference events.
    for window in app.webview_windows().into_values() {
        let mut admission = authorized(&window, windows);
        for _ in 0..5 {
            if !matches!(admission, Err(NativeFailure::Busy)) {
                break;
            }
            std::thread::sleep(std::time::Duration::from_millis(20));
            admission = authorized(&window, windows);
        }
        if admission.is_ok()
            && window
                .emit("browser-preferences-changed", snapshot)
                .is_err()
        {
            tracing::warn!(
                operation = "browser_preferences_event",
                code = "delivery-unavailable"
            );
        } else if matches!(admission, Err(NativeFailure::Busy)) {
            tracing::warn!(
                operation = "browser_preferences_event",
                code = "binding-busy"
            );
        }
    }
}
