// SPDX-License-Identifier: Apache-2.0
use std::sync::Arc;

use delidev_desktop::{
    NativeFailure,
    language::{LanguagePreference, LanguageSnapshot, LanguageStore},
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
pub async fn read_language(
    app: AppHandle<CefRuntime>,
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    store: tauri::State<'_, Arc<LanguageStore>>,
) -> Result<PublishedLanguage, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        authorized(&window, &windows)?;
        let store = Arc::clone(store.inner());
        let windows = Arc::clone(windows.inner());
        tauri::async_runtime::spawn_blocking(move || {
            let _publication = store.presentation();
            let inspected = store.read();
            tracing::info!(operation = "device_language_read", revision = inspected.revision, problem = ?inspected.problem);
            publish(&app, &windows, &store)
        })
        .await
        .map_err(|_| NativeFailure::StorageUnavailable)?
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}

#[tauri::command]
pub async fn update_language(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    store: tauri::State<'_, Arc<LanguageStore>>,
    language: LanguagePreference,
    expected_revision: u32,
) -> Result<PublishedLanguage, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        authorized(&window, &windows)?;
        let store = Arc::clone(store.inner());
        let windows = Arc::clone(windows.inner());
        tauri::async_runtime::spawn_blocking(move || {
            let _publication = store.presentation();
            let snapshot = store.update(language, expected_revision);
            tracing::info!(operation = "device_language_update", revision = snapshot.revision, problem = ?snapshot.problem);
            if snapshot.problem.is_none() {
                return publish(&app, &windows, &store);
            }
            Ok(PublishedLanguage {
                snapshot,
                widget_problem: None,
            })
        })
        .await
        .map_err(|_| NativeFailure::StorageUnavailable)?
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}

#[derive(serde::Serialize)]
pub struct PublishedLanguage {
    #[serde(flatten)]
    snapshot: LanguageSnapshot,
    widget_problem: Option<&'static str>,
}

fn publish(
    app: &AppHandle<CefRuntime>,
    windows: &ProductWindows,
    store: &LanguageStore,
) -> Result<PublishedLanguage, NativeFailure> {
    // Read the latest commit, not the triggering reply. Concurrent inspection
    // cannot restore older native labels or a stale widget preference.
    let snapshot = store.current();
    delidev_desktop::language::activate(snapshot.resolved_language);
    let widget_problem = super::widget_host::set_language(snapshot.language)
        .err()
        .map(|_| "storage-unavailable");
    if widget_problem.is_some() {
        tracing::warn!(
            operation = "widget_language_publish",
            code = "storage-unavailable"
        );
    }
    let published = PublishedLanguage {
        snapshot,
        widget_problem,
    };
    super::tray_host::refresh(app);
    for window in app.webview_windows().into_values() {
        let mut admission = authorized(&window, windows);
        for _ in 0..5 {
            if !matches!(admission, Err(NativeFailure::Busy)) {
                break;
            }
            std::thread::sleep(std::time::Duration::from_millis(20));
            admission = authorized(&window, windows);
        }
        if admission.is_ok() && window.emit("language-changed", &published).is_err() {
            tracing::warn!(operation = "language_event", code = "delivery-unavailable");
        } else if matches!(admission, Err(NativeFailure::Busy)) {
            tracing::warn!(operation = "language_event", code = "binding-busy");
        }
    }
    Ok(published)
}
