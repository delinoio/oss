// SPDX-License-Identifier: Apache-2.0
use std::sync::Arc;

use delidev_desktop::{
    Connector, NativeFailure,
    oauth::{OAuthAction, OAuthHost, OAuthResult, OAuthScope},
};
use tauri::{Cef, WebviewWindow};

use super::{SavedWindows, saved_binding, trusted_main};

// Tauri injects the independent native owners alongside the fixed renderer
// schema. Limit this exception to the IPC boundary; internal operations use
// cohesive requests. Remove it if native injection can be grouped without
// changing the existing command schema or weakening owner/lifetime checks.
#[tauri::command]
#[allow(clippy::too_many_arguments)]
pub async fn account_oauth_native(
    window: WebviewWindow<Cef>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<SavedWindows>>,
    host: tauri::State<'_, Arc<OAuthHost>>,
    server: String,
    opening: String,
    action: OAuthAction,
    generation: String,
    attempt: String,
    authorization: String,
) -> Result<OAuthResult, NativeFailure> {
    let binding = if window.label() == "main" {
        trusted_main(&window)?;
        None
    } else {
        Some(saved_binding(&window, &windows)?)
    };
    // Capture before any server-sidecar wait. Window closure invalidates even
    // an admitted bridge invocation whose blocking Begin has not run yet.
    let window_epoch = host.window_epoch(window.label())?;
    // Closing a Settings visit needs no live server/native sidecar read. The
    // original opaque generation can dispose only its own matching listener.
    if action == OAuthAction::Dispose {
        let scope = OAuthScope {
            window: window.label().into(),
            instance: binding
                .as_ref()
                .map(|v| v.instance.clone())
                .unwrap_or_else(|| "main".into()),
            server,
            opening,
            window_epoch,
        };
        let native = Arc::clone(host.inner());
        return tauri::async_runtime::spawn_blocking(move || {
            native.control(scope, action, &generation, &attempt, &authorization)
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?;
    }
    let expected = if let Some(binding) = &binding {
        binding.profile.server_id.clone()
    } else {
        let connector = Arc::clone(connector.inner());
        tauri::async_runtime::spawn_blocking(move || connector.oauth_server_identity())
            .await
            .map_err(|_| NativeFailure::SidecarFailed)??
    };
    if server != expected {
        return Err(NativeFailure::InvalidEvidence);
    }
    let scope = OAuthScope {
        window: window.label().into(),
        instance: binding
            .as_ref()
            .map(|v| v.instance.clone())
            .unwrap_or_else(|| "main".into()),
        server,
        opening,
        window_epoch,
    };
    let local = binding
        .as_ref()
        .map(|v| {
            url::Url::parse(&v.profile.endpoint)
                .ok()
                .is_some_and(|u| match u.host() {
                    Some(url::Host::Domain("localhost")) => true,
                    Some(url::Host::Ipv4(ip)) => ip.is_loopback(),
                    Some(url::Host::Ipv6(ip)) => ip.is_loopback(),
                    _ => false,
                })
        })
        .unwrap_or(true);
    let original_scope = scope.clone();
    let native = Arc::clone(host.inner());
    let result = tauri::async_runtime::spawn_blocking(move || {
        native.control_subscription(scope, action, &generation, &attempt, &authorization, local)
    })
    .await
    .map_err(|_| NativeFailure::SidecarFailed)??;
    let authority = if let Some(binding) = binding {
        saved_binding(&window, &windows).map(|current| {
            current.instance == binding.instance && current.profile.same_authority(&binding.profile)
        })
    } else if trusted_main(&window).is_err() {
        Err(NativeFailure::InvalidEvidence)
    } else {
        let connector = Arc::clone(connector.inner());
        match tauri::async_runtime::spawn_blocking(move || connector.oauth_server_identity()).await
        {
            Ok(result) => result.map(|current| current == expected),
            Err(_) => Err(NativeFailure::SidecarFailed),
        }
    };
    if !authority.unwrap_or(false) || host.window_epoch(window.label())? != window_epoch {
        let _ = host.control(
            original_scope,
            OAuthAction::Dispose,
            &result.generation,
            "",
            "",
        );
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(result)
}
