// SPDX-License-Identifier: Apache-2.0
use std::sync::Arc;

use delidev_desktop::{
    NativeFailure,
    session_creation_preferences::{CreationKind, Pair, Scope, Snapshot, Store},
};
use tauri::WebviewWindow;
use tauri_runtime_cef::CefRuntime;

use super::{ProductWindows, WindowAuthority};

fn scope(
    window: &WebviewWindow<CefRuntime>,
    windows: &ProductWindows,
    authority: &WindowAuthority,
) -> Result<Scope, NativeFailure> {
    let scope = if let Some(saved) = &authority.saved {
        Scope {
            server_id: saved.server_id.clone(),
            device_id: saved.device_id.clone(),
        }
    } else {
        let value = windows
            .local_preference_scope
            .lock()
            .map_err(|_| NativeFailure::Busy)?;
        let (revision, scope) = value.as_ref().ok_or(NativeFailure::InvalidEvidence)?;
        if *revision != authority.local_revision {
            return Err(NativeFailure::InvalidEvidence);
        }
        scope.clone()
    };
    super::recheck_authority(window, authority)?;
    if !scope.valid() {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(scope)
}
#[tauri::command]
pub async fn read_session_creation_preferences(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    store: tauri::State<'_, Arc<Store>>,
    kind: CreationKind,
) -> Result<Snapshot, NativeFailure> {
    let original = super::capture_authority(&window)?;
    let selected = scope(&window, &windows, &original)?;
    let store = Arc::clone(store.inner());
    let snapshot = tauri::async_runtime::spawn_blocking(move || store.read(selected, kind))
        .await
        .map_err(|_| NativeFailure::StorageUnavailable)?;
    super::recheck_authority(&window, &original)?;
    Ok(snapshot)
}
#[tauri::command]
pub async fn update_session_creation_preferences(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    store: tauri::State<'_, Arc<Store>>,
    kind: CreationKind,
    agent_id: String,
    machine_id: String,
    expected_revision: u32,
) -> Result<Snapshot, NativeFailure> {
    let original = super::capture_authority(&window)?;
    let selected = scope(&window, &windows, &original)?;
    let store = Arc::clone(store.inner());
    let response_window = window.clone();
    let authority = original.clone();
    let snapshot = tauri::async_runtime::spawn_blocking(move || {
        super::recheck_authority(&response_window, &authority)?;
        Ok(store.update(
            selected,
            kind,
            Pair {
                agent_id,
                machine_id,
            },
            expected_revision,
        ))
    })
    .await
    .map_err(|_| NativeFailure::StorageUnavailable)??;
    super::recheck_authority(&window, &original)?;
    Ok(snapshot)
}
