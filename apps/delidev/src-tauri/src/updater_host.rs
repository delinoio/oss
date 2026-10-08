// SPDX-License-Identifier: Apache-2.0
use std::{
    collections::BTreeMap,
    sync::{
        Arc, Condvar, Mutex,
        atomic::{AtomicBool, Ordering},
    },
};

use delidev_desktop::{
    Connector, NativeFailure,
    oauth::OAuthHost,
    updater::{AcceptedInstallation, Action, DesktopUpdateRequest, Phase, Prepared, PublicResult},
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
    stopping: AtomicBool,
    installation: Mutex<Option<Arc<Installation>>>,
}
#[derive(Default)]
struct Installation {
    result: Mutex<Option<Result<Prepared, NativeFailure>>>,
    complete: Condvar,
    join: Mutex<Option<std::thread::JoinHandle<()>>>,
    pending: Mutex<Option<(AcceptedInstallation, Phase)>>,
}
impl Installation {
    fn settle(&self, connector: &Connector) -> Result<Prepared, NativeFailure> {
        let mut pending = self.pending.lock().map_err(|_| NativeFailure::Busy)?;
        let (accepted, phase) = pending.as_ref().ok_or(NativeFailure::InvalidEvidence)?;
        let result = accepted.settle(connector, *phase);
        if result.is_ok() {
            *pending = None;
        }
        result
    }

    fn wait(&self) -> Result<Prepared, NativeFailure> {
        let mut result = self.result.lock().map_err(|_| NativeFailure::Busy)?;
        while result.is_none() {
            result = self
                .complete
                .wait(result)
                .map_err(|_| NativeFailure::Busy)?;
        }
        result.as_ref().unwrap().clone()
    }
}
impl UpdateHost {
    fn settle_retained(
        &self,
        connector: &Connector,
        server: &str,
        id: &str,
        revision: u64,
    ) -> Result<(), NativeFailure> {
        let owner = self.installation.lock().map_err(|_| NativeFailure::Busy)?;
        if let Some(task) = owner.as_ref() {
            if task
                .result
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .is_none()
            {
                return Ok(());
            }
            let matches = task
                .pending
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .as_ref()
                .is_some_and(|(accepted, _)| accepted.matches(server, id, revision));
            if matches {
                task.settle(connector)?;
            }
        }
        Ok(())
    }

    pub fn request_stop(&self) {
        self.stopping.store(true, Ordering::Release);
    }

    fn start(
        self: &Arc<Self>,
        run: impl FnOnce(Arc<Installation>) -> Result<Prepared, NativeFailure> + Send + 'static,
    ) -> Result<Arc<Installation>, NativeFailure> {
        let mut owner = self.installation.lock().map_err(|_| NativeFailure::Busy)?;
        if self.stopping.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        if let Some(previous) = owner.as_ref() {
            if previous
                .result
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .is_none()
                || previous
                    .pending
                    .lock()
                    .map_err(|_| NativeFailure::Busy)?
                    .is_some()
            {
                return Err(NativeFailure::Busy);
            }
            if let Some(join) = previous
                .join
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .take()
            {
                join.join().map_err(|_| NativeFailure::SidecarFailed)?;
            }
        }
        let task = Arc::new(Installation::default());
        let worker = Arc::clone(&task);
        *task.join.lock().map_err(|_| NativeFailure::Busy)? = Some(std::thread::spawn(move || {
            let result =
                std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| run(Arc::clone(&worker))))
                    .unwrap_or(Err(NativeFailure::SidecarFailed));
            *worker.result.lock().unwrap_or_else(|e| e.into_inner()) = Some(result);
            worker.complete.notify_all();
        }));
        *owner = Some(Arc::clone(&task));
        Ok(task)
    }

    pub fn join(&self, connector: &Connector) {
        self.request_stop();
        // Called only by the tracked Quit worker or after native runtime
        // return. Joining cannot depend on a renderer future or
        // presentation epoch.
        let owner = self.installation.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(task) = owner.as_ref() {
            if let Some(join) = task.join.lock().unwrap_or_else(|e| e.into_inner()).take() {
                if join.join().is_err() {
                    tracing::error!(operation = "desktop_update_join", code = "native-uncertain");
                }
            }
            if task
                .pending
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .is_some()
            {
                if let Err(code) = task.settle(connector) {
                    tracing::error!(
                        operation = "desktop_update_join",
                        state = "settlement-uncertain",
                        ?code
                    );
                }
            }
        }
    }
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
        if host.stopping.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        if host.busy.swap(true, Ordering::AcqRel) {
            return Err(NativeFailure::Busy);
        }
        let _busy = Busy(Arc::clone(&host));
        if action == Action::Inspect {
            let retained = Arc::clone(&host);
            let c = Arc::clone(connector.inner());
            let sid = server.clone();
            let oid = id.clone();
            tauri::async_runtime::spawn_blocking(move || {
                retained.settle_retained(&c, &sid, &oid, revision)
            })
            .await
            .map_err(|_| NativeFailure::SidecarFailed)??;
        }
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
            .set_title(delidev_desktop::localization::text(
                delidev_desktop::localization::Message::InstallTitle,
            ))
            .set_description(delidev_desktop::localization::format(
                delidev_desktop::localization::Message::InstallBody,
                &[("version", &prepared.release_version)],
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
        let native_window = window.clone();
        let native_oauth = Arc::clone(oauth.inner());
        let native_windows = Arc::clone(windows.inner());
        let native_binding = binding.clone();
        let native_original = original.clone();
        let task = host.start(move |task| {
            let begun = c.begin_desktop_installation(
                saved.as_ref(),
                DesktopUpdateRequest {
                    server: &sid,
                    id: &oid,
                    revision,
                    generation: &native_generation,
                    action: "native-begin",
                    outcome: None,
                },
            )?;
            // Publish the original owner before any authority check or native
            // effect. A pre-effect departure records Failed; an in-effect
            // panic retains uncertainty without granting installation replay.
            let mut pending = task.pending.lock().map_err(|_| NativeFailure::Busy)?;
            *pending = Some((begun, Phase::Uncertain));
            let valid = || -> Result<(), NativeFailure> {
                if native_oauth.window_epoch(native_window.label())? != native_original.epoch {
                    return Err(NativeFailure::InvalidEvidence);
                }
                if let Some(old) = &native_binding {
                    let current = saved_binding(&native_window, &native_windows)?;
                    if current.instance != old.instance
                        || !current.profile.same_authority(&old.profile)
                    {
                        return Err(NativeFailure::InvalidEvidence);
                    }
                } else {
                    trusted_local(&native_window)?;
                }
                Ok(())
            };
            let (accepted, phase) = pending.as_mut().unwrap();
            *phase = if valid().is_ok() {
                accepted.install(&c)
            } else {
                Phase::Failed
            };
            drop(pending);
            task.settle(&c)
        })?;
        let result = tauri::async_runtime::spawn_blocking(move || task.wait())
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

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn quit_joins_original_task_after_caller_departure_and_fences_new_admission() {
        let temp = tempfile::tempdir().unwrap();
        let connector = Arc::new(
            Connector::new(
                temp.path().join("unused-sidecar"),
                temp.path().to_path_buf(),
            )
            .unwrap(),
        );
        let host = Arc::new(UpdateHost::default());
        let (entered, started) = std::sync::mpsc::channel();
        let (release, finish) = std::sync::mpsc::channel();
        let effects = Arc::new(std::sync::atomic::AtomicUsize::new(0));
        let observed = Arc::clone(&effects);
        let task = host
            .start(move |_| {
                entered.send(()).unwrap();
                finish.recv().unwrap();
                observed.fetch_add(1, Ordering::AcqRel);
                Err(NativeFailure::InvalidEvidence)
            })
            .unwrap();
        started.recv().unwrap();
        drop(task); // Renderer departure cannot own cancellation or the join.
        host.request_stop();
        assert!(matches!(
            host.start(|_| Err(NativeFailure::SidecarFailed)),
            Err(NativeFailure::Stopped)
        ));
        let owner = Arc::clone(&host);
        let joining = std::thread::spawn(move || owner.join(&connector));
        std::thread::sleep(std::time::Duration::from_millis(25));
        assert!(!joining.is_finished());
        release.send(()).unwrap();
        joining.join().unwrap();
        assert_eq!(effects.load(Ordering::Acquire), 1);
        assert!(matches!(
            host.installation.lock().unwrap().as_ref().unwrap().wait(),
            Err(NativeFailure::InvalidEvidence)
        ));
    }

    #[cfg(unix)]
    #[test]
    fn retained_original_uncertainty_settles_through_inspect_and_quit() {
        use std::os::unix::fs::PermissionsExt;
        for quit in [false, true] {
            let temp = tempfile::tempdir().unwrap();
            let root = temp.path().canonicalize().unwrap();
            let sidecar = root.join("sidecar");
            std::fs::write(
                &sidecar,
                r#"#!/bin/sh
printf x >> "$2/calls"
case "$4" in
native-begin) exec /bin/cat "$2/begin.json" ;;
native-outcome) exec /bin/cat "$2/outcome.json" ;;
esac
exit 1
"#,
            )
            .unwrap();
            std::fs::set_permissions(&sidecar, std::fs::Permissions::from_mode(0o700)).unwrap();
            let connector = Arc::new(Connector::new(sidecar, root.clone()).unwrap());
            let id = uuid::Uuid::now_v7().to_string();
            let server = uuid::Uuid::now_v7().to_string();
            let generation = uuid::Uuid::now_v7().to_string();
            let target = format!(
                "{}-{}",
                std::env::consts::OS,
                if cfg!(target_arch = "aarch64") {
                    "arm64"
                } else {
                    "amd64"
                }
            )
            .replace("macos-", "darwin-");
            let extension = if cfg!(target_os = "macos") {
                ".dmg"
            } else {
                ".AppImage"
            };
            let mut value = serde_json::json!({"version":1,"operation_id":id,"server_id":server,"generation":generation,"release_version":"0.2.0","target":target,"phase":"installing","artifact_path":root.join("desktop-updates/downloads").join(format!("{}{}","a".repeat(64),extension)),"artifact_sha256":"a".repeat(64),"artifact_size":8,"manifest_sha256":"b".repeat(64)});
            std::fs::write(
                root.join("begin.json"),
                serde_json::to_vec(&serde_json::json!({"version":1,"result":value})).unwrap(),
            )
            .unwrap();
            value["phase"] = serde_json::json!("uncertain");
            std::fs::write(
                root.join("outcome.json"),
                serde_json::to_vec(&serde_json::json!({"version":1,"result":value})).unwrap(),
            )
            .unwrap();
            let accepted = connector
                .begin_desktop_installation(
                    None,
                    DesktopUpdateRequest {
                        server: &server,
                        id: &id,
                        revision: 7,
                        generation: &generation,
                        action: "native-begin",
                        outcome: None,
                    },
                )
                .unwrap();
            let host = Arc::new(UpdateHost::default());
            let task = host
                .start(move |task| {
                    *task.pending.lock().unwrap() = Some((accepted, Phase::Uncertain));
                    Err(NativeFailure::Busy)
                })
                .unwrap();
            assert!(matches!(task.wait(), Err(NativeFailure::Busy)));
            assert!(task.pending.lock().unwrap().is_some());
            if quit {
                host.join(&connector);
            } else {
                host.settle_retained(&connector, &server, &id, 7).unwrap();
            }
            assert!(task.pending.lock().unwrap().is_none());
            host.join(&connector);
            assert_eq!(
                std::fs::read(root.join("calls")).unwrap(),
                b"xx",
                "only original begin and same-phase outcome; no installer replay"
            );
        }
    }

    #[test]
    fn native_task_panic_releases_waiter_without_fabricating_success() {
        let host = Arc::new(UpdateHost::default());
        let task = host
            .start(|_| panic!("controlled native fixture failure"))
            .unwrap();
        assert!(matches!(task.wait(), Err(NativeFailure::SidecarFailed)));
    }
}
