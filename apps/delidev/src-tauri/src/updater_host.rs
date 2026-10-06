// SPDX-License-Identifier: Apache-2.0
use std::{
    collections::BTreeMap,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
};

use delidev_desktop::{
    Connector, NativeFailure,
    oauth::OAuthHost,
    updater::{Action, DesktopUpdateRequest, Phase, Prepared, PublicResult},
};
use tauri::WebviewWindow;
use tauri_runtime_cef::CefRuntime;

use super::{ProductWindows, saved_binding, trusted_local};

#[derive(Clone, PartialEq, Eq)]
struct Authority {
    window: String,
    instance: String,
    server: String,
    epoch: u64,
}
#[derive(Clone)]
struct Attempt {
    authority: Authority,
    prepared: Prepared,
    revision: u64,
}
#[derive(Default)]
pub struct UpdateHost {
    attempts: Mutex<BTreeMap<String, Attempt>>,
    busy: AtomicBool,
}
struct Busy(Arc<UpdateHost>);
impl Drop for Busy {
    fn drop(&mut self) {
        self.0.busy.store(false, Ordering::Release);
    }
}

#[tauri::command]
pub async fn desktop_update_context(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
) -> Result<serde_json::Value, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        if super::is_local(&window) {
            trusted_local(&window)?;
        } else {
            saved_binding(&window, &windows)?;
        }
        let target = format!(
            "{}-{}",
            std::env::consts::OS,
            match std::env::consts::ARCH {
                "x86_64" => "amd64",
                "aarch64" => "arm64",
                _ => return Err(NativeFailure::Incompatible),
            }
        )
        .replace("macos-", "darwin-");
        Ok(serde_json::json!({"current_version":env!("CARGO_PKG_VERSION"),"target":target}))
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}
// Tauri injects the independent native owners alongside the fixed renderer
// schema. Limit this exception to the IPC boundary; internal operations use
// cohesive requests. Remove it if native injection can be grouped without
// changing the existing command schema or weakening owner/lifetime checks.
#[allow(clippy::too_many_arguments)]
#[tauri::command]
pub async fn desktop_update_native(
    window: WebviewWindow<CefRuntime>,
    connector: tauri::State<'_, Arc<Connector>>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    oauth: tauri::State<'_, Arc<OAuthHost>>,
    host: tauri::State<'_, Arc<UpdateHost>>,
    server: String,
    id: String,
    revision: String,
    action: Action,
) -> Result<PublicResult, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        delidev_desktop::canonical_id(&id)?;
        delidev_desktop::canonical_id(&server)?;
        let revision: u64 = revision.parse().map_err(|_| NativeFailure::InvalidInput)?;
        if revision == 0 || revision >= 1 << 63 {
            return Err(NativeFailure::InvalidInput);
        }
        let binding = if super::is_local(&window) {
            trusted_local(&window)?;
            None
        } else {
            Some(saved_binding(&window, &windows)?)
        };
        let epoch = oauth.window_epoch(window.label())?;
        let expected = binding.as_ref().map(|v| v.profile.clone());
        let observed = if let Some(profile) = &expected {
            profile.server_id.clone()
        } else {
            let c = Arc::clone(connector.inner());
            tauri::async_runtime::spawn_blocking(move || c.oauth_server_identity())
                .await
                .map_err(|_| NativeFailure::SidecarFailed)??
        };
        if observed != server {
            return Err(NativeFailure::InvalidEvidence);
        }
        let authority = Authority {
            window: window.label().into(),
            instance: binding
                .as_ref()
                .map(|v| v.instance.clone())
                .unwrap_or_else(|| window.label().into()),
            server: server.clone(),
            epoch,
        };
        let host = Arc::clone(host.inner());
        if host.busy.swap(true, Ordering::AcqRel) {
            return Err(NativeFailure::Busy);
        }
        let _busy = Busy(Arc::clone(&host));
        let (generation, command) = if action == Action::Prepare || action == Action::Inspect {
            (
                uuid::Uuid::now_v7().to_string(),
                if action == Action::Inspect {
                    "native-inspect"
                } else {
                    "native-prepare"
                },
            )
        } else {
            let attempt = host
                .attempts
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .get(window.label())
                .filter(|v| {
                    v.authority == authority
                        && v.prepared.operation_id == id
                        && v.revision == revision
                })
                .cloned()
                .ok_or(NativeFailure::InvalidEvidence)?;
            (
                attempt.prepared.generation,
                if action == Action::Inspect {
                    "native-inspect"
                } else {
                    "native-verify"
                },
            )
        };
        let original = authority.clone();
        let c = Arc::clone(connector.inner());
        let saved = expected.clone();
        let sid = server.clone();
        let oid = id.clone();
        let native_generation = generation.clone();
        let prepared = tauri::async_runtime::spawn_blocking(move || {
            c.desktop_update(
                saved.as_ref(),
                DesktopUpdateRequest {
                    server: &sid,
                    id: &oid,
                    revision,
                    generation: &native_generation,
                    action: command,
                    outcome: None,
                },
            )
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
        let valid = || -> Result<(), NativeFailure> {
            if oauth.window_epoch(window.label())? != original.epoch {
                return Err(NativeFailure::InvalidEvidence);
            }
            if let Some(old) = &binding {
                let current = saved_binding(&window, &windows)?;
                if current.instance != old.instance || !current.profile.same_authority(&old.profile)
                {
                    return Err(NativeFailure::InvalidEvidence);
                }
            } else {
                trusted_local(&window)?;
            }
            Ok(())
        };
        valid()?;
        if action != Action::Install {
            host.attempts
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .insert(
                    window.label().into(),
                    Attempt {
                        authority,
                        prepared: prepared.clone(),
                        revision,
                    },
                );
            return Ok(prepared.public());
        }
        if prepared.phase != Phase::Prepared {
            return Err(NativeFailure::InvalidEvidence);
        }
        let approved = rfd::AsyncMessageDialog::new()
            .set_title("Install DeliDev update")
            .set_description(format!(
                "Install DeliDev {} on this computer? Running servers and sessions keep their own \
                 processes. Restart the desktop after installation.",
                prepared.release_version
            ))
            .set_buttons(rfd::MessageButtons::YesNo)
            .show()
            .await;
        valid()?;
        if approved != rfd::MessageDialogResult::Yes {
            return Ok(prepared.public());
        }
        // Reverify signed bytes and atomically claim once-only installation
        // after the native confirmation. Native owns this outcome even
        // if the renderer leaves.
        let c = Arc::clone(connector.inner());
        let saved = expected.clone();
        let sid = server.clone();
        let oid = id.clone();
        let native_generation = generation.clone();
        let begun = tauri::async_runtime::spawn_blocking(move || {
            c.desktop_update(
                saved.as_ref(),
                DesktopUpdateRequest {
                    server: &sid,
                    id: &oid,
                    revision,
                    generation: &native_generation,
                    action: "native-begin",
                    outcome: None,
                },
            )
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
        valid()?;
        let c = Arc::clone(connector.inner());
        let saved = expected;
        let sid = server;
        let oid = id;
        let native_generation = generation;
        let result = tauri::async_runtime::spawn_blocking(move || {
            let outcome = c.install_desktop(&begun);
            c.desktop_update(
                saved.as_ref(),
                DesktopUpdateRequest {
                    server: &sid,
                    id: &oid,
                    revision,
                    generation: &native_generation,
                    action: "native-outcome",
                    outcome: Some(outcome),
                },
            )
        })
        .await
        .map_err(|_| NativeFailure::SidecarFailed)??;
        host.attempts
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .insert(
                window.label().into(),
                Attempt {
                    authority,
                    prepared: result.clone(),
                    revision,
                },
            );
        // A vanished original window cannot receive a late outcome. The durable
        // original native journal remains authoritative for subsequent
        // inspection.
        valid()?;
        Ok(result.public())
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}
