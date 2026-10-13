// SPDX-License-Identifier: Apache-2.0
use std::{
    collections::BTreeMap,
    sync::{
        Arc, Condvar, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};

use delidev_desktop::{
    Connector, NativeFailure,
    oauth::OAuthHost,
    updater::{AcceptedInstallation, Action, DesktopUpdateRequest, Phase, Prepared, PublicResult},
};
use tauri::WebviewWindow;
use tauri_runtime_cef::CefRuntime;

use super::{ProductWindows, WindowAuthority, recheck_record, saved_binding, trusted_local};

/// Upper bound for retrying an unverifiable post-begin authority check. It is
/// shared by the original task and a concurrent Quit, which stops retrying at
/// once, so it never extends shutdown beyond one more check.
const AUTHORITY_RETRY_BUDGET: Duration = Duration::from_secs(5);
const AUTHORITY_RETRY_INTERVAL: Duration = Duration::from_millis(25);

/// Result of the retained worker's post-begin authority check.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum AuthorityVerdict {
    /// The exact original authority is still present.
    Authorized,
    /// The original authority definitively departed or changed.
    Departed,
    /// Authority could not be verified (for example contention); not a
    /// statement that it changed.
    Unverifiable,
}

fn classify_authority(result: Result<(), NativeFailure>) -> AuthorityVerdict {
    match result {
        Ok(()) => AuthorityVerdict::Authorized,
        Err(
            NativeFailure::InvalidEvidence
            | NativeFailure::PermissionDenied
            | NativeFailure::Stopped,
        ) => AuthorityVerdict::Departed,
        Err(_) => AuthorityVerdict::Unverifiable,
    }
}

/// Rechecks the exact original authority from retained native state only. It
/// never calls a CEF URL getter, `trusted_local` or `saved_binding`: those
/// enqueue work on the UI loop, which may already be stopped while the
/// post-runtime join waits for this worker. Initial trusted-document admission
/// happened before begin and the command boundary still rechecks the window.
pub(super) fn retained_authority(
    oauth: &OAuthHost,
    windows: &ProductWindows,
    label: &str,
    original: &WindowAuthority,
    epoch: u64,
) -> Result<(), NativeFailure> {
    if oauth.window_epoch(label)? != epoch {
        return Err(NativeFailure::InvalidEvidence);
    }
    recheck_record(windows, original)
}

/// Chooses the phase to settle after the durable begin. Only a definitive
/// departure records a pre-effect Failed without invoking the installer. An
/// unverifiable check is retried until `budget` elapses or Quit begins; then
/// the original Uncertain phase is retained and the installer is never called.
fn select_phase(
    mut check: impl FnMut() -> Result<(), NativeFailure>,
    stopping: &AtomicBool,
    budget: Duration,
    interval: Duration,
    install: impl FnOnce() -> Phase,
) -> Phase {
    let started = Instant::now();
    let mut attempts: u32 = 0;
    loop {
        attempts += 1;
        let result = check();
        let failure = result.err();
        match classify_authority(result) {
            AuthorityVerdict::Authorized => return install(),
            AuthorityVerdict::Departed => {
                tracing::warn!(
                    operation = "desktop_update_native",
                    phase = "authority-departed",
                    ?failure,
                    attempts
                );
                return Phase::Failed;
            }
            AuthorityVerdict::Unverifiable => {
                let cancelled = stopping.load(Ordering::Acquire);
                if cancelled || started.elapsed() >= budget {
                    tracing::error!(
                        operation = "desktop_update_native",
                        phase = "authority-unverifiable",
                        state = "original-uncertain",
                        ?failure,
                        attempts,
                        cancelled
                    );
                    return Phase::Uncertain;
                }
                std::thread::sleep(interval);
            }
        }
    }
}

/// Runs the retained worker after the durable begin. The original owner is
/// published before any authority check or native effect. A definitive
/// pre-effect departure records Failed; an unverifiable check retains
/// Uncertain without granting installation replay; an in-effect panic also
/// retains uncertainty.
fn run_retained(
    task: &Installation,
    connector: &Connector,
    begun: AcceptedInstallation,
    check: impl FnMut() -> Result<(), NativeFailure>,
    stopping: &AtomicBool,
    budget: Duration,
    interval: Duration,
) -> Result<Prepared, NativeFailure> {
    let mut pending = task.pending.lock().map_err(|_| NativeFailure::Busy)?;
    *pending = Some((begun, Phase::Uncertain));
    let (accepted, phase) = pending.as_mut().unwrap();
    *phase = select_phase(check, stopping, budget, interval, || {
        accepted.install(connector)
    });
    drop(pending);
    task.settle(connector)
}

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
            if let Some(join) = task.join.lock().unwrap_or_else(|e| e.into_inner()).take()
                && join.join().is_err()
            {
                tracing::error!(operation = "desktop_update_join", code = "native-uncertain");
            }
            let pending = task
                .pending
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .is_some();
            if pending && let Err(code) = task.settle(connector) {
                tracing::error!(
                    operation = "desktop_update_join",
                    state = "settlement-uncertain",
                    ?code
                );
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
        // The retained worker owns retained native state only: the original
        // window label, authority record, registry and OAuth epoch. It never
        // holds the window handle, so it cannot reach a CEF URL getter.
        let native_label = window.label().to_string();
        let native_oauth = Arc::clone(oauth.inner());
        let native_windows = Arc::clone(windows.inner());
        let native_authority = original_authority.clone();
        let native_original = original.clone();
        let native_host = Arc::clone(&host);
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
            run_retained(
                &task,
                &c,
                begun,
                || {
                    retained_authority(
                        &native_oauth,
                        &native_windows,
                        &native_label,
                        &native_authority,
                        native_original.epoch,
                    )
                },
                &native_host.stopping,
                AUTHORITY_RETRY_BUDGET,
                AUTHORITY_RETRY_INTERVAL,
            )
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
    struct NativeFixture {
        _temp: tempfile::TempDir,
        root: std::path::PathBuf,
        connector: Arc<Connector>,
        server: String,
        id: String,
    }

    /// Builds a fake sidecar whose begin and outcome calls append to a `calls`
    /// file, plus the original accepted installation token from its begin.
    #[cfg(unix)]
    fn native_fixture() -> (NativeFixture, AcceptedInstallation) {
        use std::os::unix::fs::PermissionsExt;
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().canonicalize().unwrap();
        let sidecar = root.join("sidecar");
        std::fs::write(
            &sidecar,
            r#"#!/usr/bin/python3
import sys,json,os
root=sys.argv[2]
if sys.argv[3]=='server':
 print(json.dumps({'version':2,'result':{'endpoint':'http://127.0.0.1:51234','generation':'019c2381-9300-7000-8000-000000000001','key':'CQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQk'}}),flush=True)
 for line in sys.stdin:
  request=json.loads(line)
  op=request['operation']
  if op=='runtime.shutdown': break
  if op in ('runtime.cancel','runtime.fence'): continue
  assert op=='update.native-begin'
  open(os.path.join(root,'calls'),'a').write('x')
  result=json.load(open(os.path.join(root,'begin.json')))['result']
  print(json.dumps({'version':2,'id':request['id'],'result':result}),flush=True)
else:
 assert sys.argv[3:5]==['update','native-outcome']
 open(os.path.join(root,'calls'),'a').write('x')
 print(open(os.path.join(root,'outcome.json')).read())
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
        (
            NativeFixture {
                _temp: temp,
                root,
                connector,
                server,
                id,
            },
            accepted,
        )
    }

    fn failing_then_ok(
        failures: usize,
        failure: NativeFailure,
        calls: &std::sync::atomic::AtomicUsize,
    ) -> impl FnMut() -> Result<(), NativeFailure> + '_ {
        move || {
            if calls.fetch_add(1, Ordering::AcqRel) < failures {
                Err(failure)
            } else {
                Ok(())
            }
        }
    }

    #[test]
    fn classification_separates_definitive_departure_from_unverifiable_checks() {
        assert_eq!(classify_authority(Ok(())), AuthorityVerdict::Authorized);
        for failure in [
            NativeFailure::InvalidEvidence,
            NativeFailure::PermissionDenied,
            NativeFailure::Stopped,
        ] {
            assert_eq!(classify_authority(Err(failure)), AuthorityVerdict::Departed);
        }
        for failure in [
            NativeFailure::Busy,
            NativeFailure::TimedOut,
            NativeFailure::SidecarFailed,
            NativeFailure::StorageUnavailable,
        ] {
            assert_eq!(
                classify_authority(Err(failure)),
                AuthorityVerdict::Unverifiable
            );
        }
    }

    #[test]
    fn transient_contention_is_retried_and_installs_exactly_once() {
        let checks = std::sync::atomic::AtomicUsize::new(0);
        let installs = std::sync::atomic::AtomicUsize::new(0);
        let stopping = AtomicBool::new(false);
        let phase = select_phase(
            failing_then_ok(3, NativeFailure::Busy, &checks),
            &stopping,
            Duration::from_secs(30),
            Duration::from_millis(1),
            || {
                installs.fetch_add(1, Ordering::AcqRel);
                Phase::Installed
            },
        );
        assert_eq!(phase, Phase::Installed);
        assert_eq!(checks.load(Ordering::Acquire), 4);
        assert_eq!(installs.load(Ordering::Acquire), 1);
    }

    #[test]
    fn definitive_departure_fails_before_any_installer_call_without_retry() {
        for failure in [
            NativeFailure::InvalidEvidence,
            NativeFailure::PermissionDenied,
            NativeFailure::Stopped,
        ] {
            let checks = std::sync::atomic::AtomicUsize::new(0);
            let installs = std::sync::atomic::AtomicUsize::new(0);
            let stopping = AtomicBool::new(false);
            let phase = select_phase(
                failing_then_ok(usize::MAX, failure, &checks),
                &stopping,
                Duration::from_secs(30),
                Duration::from_millis(1),
                || {
                    installs.fetch_add(1, Ordering::AcqRel);
                    Phase::Installed
                },
            );
            assert_eq!(phase, Phase::Failed);
            assert_eq!(checks.load(Ordering::Acquire), 1);
            assert_eq!(installs.load(Ordering::Acquire), 0);
        }
    }

    #[test]
    fn persistent_contention_retains_uncertainty_without_installing() {
        let checks = std::sync::atomic::AtomicUsize::new(0);
        let installs = std::sync::atomic::AtomicUsize::new(0);
        let stopping = AtomicBool::new(false);
        let phase = select_phase(
            failing_then_ok(usize::MAX, NativeFailure::Busy, &checks),
            &stopping,
            Duration::from_millis(40),
            Duration::from_millis(5),
            || {
                installs.fetch_add(1, Ordering::AcqRel);
                Phase::Installed
            },
        );
        assert_eq!(phase, Phase::Uncertain);
        assert!(checks.load(Ordering::Acquire) > 1);
        assert_eq!(installs.load(Ordering::Acquire), 0);
    }

    #[test]
    fn quit_stops_retrying_and_retains_uncertainty_without_installing() {
        let checks = std::sync::atomic::AtomicUsize::new(0);
        let installs = std::sync::atomic::AtomicUsize::new(0);
        let stopping = AtomicBool::new(true);
        let started = Instant::now();
        let phase = select_phase(
            failing_then_ok(usize::MAX, NativeFailure::Busy, &checks),
            &stopping,
            Duration::from_secs(60),
            Duration::from_millis(1),
            || {
                installs.fetch_add(1, Ordering::AcqRel);
                Phase::Installed
            },
        );
        assert_eq!(phase, Phase::Uncertain);
        assert_eq!(checks.load(Ordering::Acquire), 1);
        assert_eq!(installs.load(Ordering::Acquire), 0);
        assert!(started.elapsed() < Duration::from_secs(5));
    }

    #[cfg(unix)]
    fn write_outcome(fixture: &NativeFixture, phase: &str) {
        let path = fixture.root.join("outcome.json");
        let mut value: serde_json::Value =
            serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        value["result"]["phase"] = serde_json::json!(phase);
        std::fs::write(path, serde_json::to_vec(&value).unwrap()).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn worker_records_pre_effect_failed_or_retained_uncertainty_without_installer_replay() {
        let cases = [
            (NativeFailure::InvalidEvidence, Phase::Failed, "failed"),
            (NativeFailure::PermissionDenied, Phase::Failed, "failed"),
            (NativeFailure::Busy, Phase::Uncertain, "uncertain"),
        ];
        for (failure, expected, name) in cases {
            let (fixture, accepted) = native_fixture();
            write_outcome(&fixture, name);
            let task = Installation::default();
            let checks = std::sync::atomic::AtomicUsize::new(0);
            let stopping = AtomicBool::new(false);
            let result = run_retained(
                &task,
                &fixture.connector,
                accepted,
                failing_then_ok(usize::MAX, failure, &checks),
                &stopping,
                Duration::from_millis(30),
                Duration::from_millis(5),
            )
            .unwrap();
            assert_eq!(result.phase, expected);
            assert!(task.pending.lock().unwrap().is_none());
            assert_eq!(
                std::fs::read(fixture.root.join("calls")).unwrap(),
                b"xx",
                "original begin and one same-phase outcome only; no installer"
            );
            fixture.connector.shutdown_owned().unwrap();
        }
    }

    #[cfg(unix)]
    #[test]
    fn delayed_begin_completes_retained_task_and_quit_join_with_state_only_checks() {
        let (fixture, accepted) = native_fixture();
        write_outcome(&fixture, "failed");
        let host = Arc::new(UpdateHost::default());
        let (release, gate) = std::sync::mpsc::channel::<()>();
        let checks = Arc::new(std::sync::atomic::AtomicUsize::new(0));
        let observed = Arc::clone(&checks);
        let connector = Arc::clone(&fixture.connector);
        let worker_host = Arc::clone(&host);
        let task = host
            .start(move |task| {
                // The native loop is torn down while begin is still in flight.
                gate.recv().unwrap();
                run_retained(
                    &task,
                    &connector,
                    accepted,
                    move || {
                        observed.fetch_add(1, Ordering::AcqRel);
                        Err(NativeFailure::Stopped)
                    },
                    &worker_host.stopping,
                    AUTHORITY_RETRY_BUDGET,
                    AUTHORITY_RETRY_INTERVAL,
                )
            })
            .unwrap();
        host.request_stop();
        let owner = Arc::clone(&host);
        let connector = Arc::clone(&fixture.connector);
        let joining = std::thread::spawn(move || owner.join(&connector));
        release.send(()).unwrap();
        joining.join().unwrap();
        assert_eq!(task.wait().unwrap().phase, Phase::Failed);
        assert_eq!(checks.load(Ordering::Acquire), 1);
        assert_eq!(std::fs::read(fixture.root.join("calls")).unwrap(), b"xx");
        fixture.connector.shutdown_owned().unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn failed_settlement_after_unverifiable_check_retains_the_token_for_inspection() {
        let (fixture, accepted) = native_fixture();
        // A sidecar outcome that contradicts the retained phase cannot settle.
        write_outcome(&fixture, "installed");
        let task = Installation::default();
        let stopping = AtomicBool::new(true);
        let checks = std::sync::atomic::AtomicUsize::new(0);
        let result = run_retained(
            &task,
            &fixture.connector,
            accepted,
            failing_then_ok(usize::MAX, NativeFailure::Busy, &checks),
            &stopping,
            Duration::from_secs(60),
            Duration::from_millis(1),
        );
        assert!(result.is_err());
        let pending = task.pending.lock().unwrap();
        let (retained, phase) = pending.as_ref().expect("token stays retained");
        assert_eq!(*phase, Phase::Uncertain);
        assert!(retained.matches(&fixture.server, &fixture.id, 7));
        drop(pending);
        fixture.connector.shutdown_owned().unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn retained_original_uncertainty_settles_through_inspect_and_quit() {
        for quit in [false, true] {
            let (fixture, accepted) = native_fixture();
            let NativeFixture {
                root,
                connector,
                server,
                id,
                ..
            } = &fixture;
            let (root, connector, server, id) = (
                root.clone(),
                Arc::clone(connector),
                server.clone(),
                id.clone(),
            );
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
            connector.shutdown_owned().unwrap();
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
