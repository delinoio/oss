use std::{
    collections::{BTreeMap, HashSet},
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

use delidev_desktop::{
    NativeFailure, canonical_id,
    notifications::{self, Notice, Permission, PermissionProblem, PresentationResult, Readiness},
};
use tauri::{AppHandle, Manager, WebviewWindow};
use tauri_runtime_cef::CefRuntime;
use tokio::sync::oneshot;

use super::{ProductWindows, saved_binding, tray_host, trusted_local};

const MAX_ACTIVE: usize = 256;
const MAX_SEEN: usize = 10000;
const LIFETIME: Duration = Duration::from_secs(24 * 60 * 60);
#[derive(Clone, PartialEq, Eq)]
struct Target {
    connection: Option<String>,
    label: String,
    scope: String,
    instance: Option<String>,
}
struct Active {
    target: Target,
    cancel: Option<oneshot::Sender<()>>,
    task: tauri::async_runtime::JoinHandle<()>,
}
struct Observer {
    cancel: Option<oneshot::Sender<()>>,
    task: tauri::async_runtime::JoinHandle<()>,
}
struct Prompt {
    cancel: oneshot::Sender<()>,
    task: tauri::async_runtime::JoinHandle<()>,
}
#[derive(Default)]
struct State {
    scopes: BTreeMap<String, Target>,
    active: BTreeMap<String, Active>,
    seen: HashSet<String>,
    prompt: Option<Prompt>,
    observers: BTreeMap<String, Observer>,
    authorities: BTreeMap<String, super::WindowAuthority>,
}
#[derive(Default)]
pub struct NotificationHost {
    state: Mutex<State>,
    stopping: AtomicBool,
    prompting: AtomicBool,
}

fn instance(
    window: &WebviewWindow<CefRuntime>,
    windows: &ProductWindows,
) -> Result<Option<String>, NativeFailure> {
    if super::is_local(window) {
        trusted_local(window)?;
        Ok(None)
    } else {
        Ok(Some(saved_binding(window, windows)?.instance))
    }
}
#[tauri::command]
pub async fn begin_notifications(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    host: tauri::State<'_, Arc<NotificationHost>>,
    app: AppHandle<CefRuntime>,
    connector: tauri::State<'_, Arc<delidev_desktop::Connector>>,
    supervision: tauri::State<'_, Arc<delidev_desktop::Supervision>>,
) -> Result<String, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        let instance = instance(&window, &windows)?;
        if host.stopping.load(Ordering::Acquire) {
            return Err(NativeFailure::Busy);
        }
        let profile = if super::is_local(&window) {
            None
        } else {
            Some(saved_binding(&window, &windows)?.profile)
        };
        let connector = Arc::clone(connector.inner());
        let original = if let Some(profile) = profile.clone() {
            let native = Arc::clone(&connector);
            tauri::async_runtime::spawn_blocking(move || native.connect_saved(&profile))
                .await
                .map_err(|_| NativeFailure::SidecarFailed)??
        } else {
            let native = Arc::clone(supervision.inner());
            tauri::async_runtime::spawn_blocking(move || native.launch_connection())
                .await
                .map_err(|_| NativeFailure::SidecarFailed)??
                .ok_or(NativeFailure::CredentialUnavailable)?
        };
        super::recheck_authority(&response_window, &original_authority)?;
        let ledger = connector
            .notification_connection_ledger(&original, profile.as_ref().map(|v| v.id.as_str()))?;
        let key = ledger.key.clone();
        let scope = uuid::Uuid::now_v7().to_string();
        {
            let mut state = host.state.lock().map_err(|_| NativeFailure::Busy)?;
            state.begin(Target {
                label: window.label().into(),
                scope: scope.clone(),
                instance,
                connection: Some(key.clone()),
            })?;
            state
                .authorities
                .insert(window.label().into(), original_authority.clone());
        }
        host.observe_connection(app, connector, original, ledger)?;
        Ok(scope)
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
pub async fn end_notifications(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    host: tauri::State<'_, Arc<NotificationHost>>,
    scope: String,
) -> Result<(), NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        let original = instance(&window, &windows)?;
        canonical_id(&scope)?;
        let target = Target {
            label: window.label().into(),
            scope,
            instance: original,
            connection: host
                .state
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .scopes
                .get(window.label())
                .and_then(|v| v.connection.clone()),
        };
        let mut state = host.state.lock().map_err(|_| NativeFailure::Busy)?;
        state.end(&target);
        Ok(())
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
pub async fn notification_permission(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    host: tauri::State<'_, Arc<NotificationHost>>,
) -> Result<Readiness, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        instance(&window, &windows)?;
        if host.at_capacity() {
            return Ok(Readiness::unavailable(PermissionProblem::Capacity));
        }
        Ok(
            tokio::time::timeout(Duration::from_secs(5), notifications::permission(false))
                .await
                .unwrap_or(Readiness::unavailable(PermissionProblem::OsUnavailable)),
        )
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
pub async fn request_notification_permission(
    window: WebviewWindow<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    host: tauri::State<'_, Arc<NotificationHost>>,
) -> Result<Readiness, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        instance(&window, &windows)?;
        let receiver = host.request_permission()?;
        receiver.await.map_err(|_| NativeFailure::Busy)
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}
#[tauri::command]
pub async fn present_notification(
    window: WebviewWindow<CefRuntime>,
    app: AppHandle<CefRuntime>,
    windows: tauri::State<'_, Arc<ProductWindows>>,
    host: tauri::State<'_, Arc<NotificationHost>>,
    scope: String,
    notice: Notice,
) -> Result<PresentationResult, NativeFailure> {
    let response_window = window.clone();
    let original_authority = super::capture_authority(&response_window)?;
    let result = async {
        let original = instance(&window, &windows)?;
        canonical_id(&scope)?;
        notice.validate()?;
        if matches!(
            notice.kind,
            notifications::NotificationKind::ServerLost
                | notifications::NotificationKind::ServerRestored
        ) {
            return Err(NativeFailure::PermissionDenied);
        }
        let target = Target {
            label: window.label().into(),
            scope,
            instance: original,
            connection: host
                .state
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .scopes
                .get(window.label())
                .and_then(|v| v.connection.clone()),
        };
        let receiver = host.start(app, target, notice)?;
        Ok(receiver.await.unwrap_or(PresentationResult::Uncertain))
    }
    .await;
    super::recheck_authority(&response_window, &original_authority)?;
    result
}

impl State {
    fn end(&mut self, target: &Target) {
        if self.scopes.get(&target.label) == Some(target) {
            self.scopes.remove(&target.label);
            self.authorities.remove(&target.label);
            self.cancel_label(&target.label);
            self.cancel_unused_observers();
        }
    }

    fn cancel_unused_observers(&mut self) {
        for (key, observer) in &mut self.observers {
            if !self
                .scopes
                .values()
                .any(|v| v.connection.as_ref() == Some(key))
            {
                if let Some(cancel) = observer.cancel.take() {
                    let _ = cancel.send(());
                }
            }
        }
    }

    fn cancel_label(&mut self, label: &str) {
        for active in self.active.values_mut().filter(|v| v.target.label == label) {
            if let Some(cancel) = active.cancel.take() {
                let _ = cancel.send(());
            }
        }
    }

    fn begin(&mut self, target: Target) -> Result<(), NativeFailure> {
        if self.scopes.len() >= MAX_ACTIVE && !self.scopes.contains_key(&target.label) {
            return Err(NativeFailure::Busy);
        }
        self.cancel_label(&target.label);
        self.scopes.insert(target.label.clone(), target);
        self.cancel_unused_observers();
        Ok(())
    }

    fn reserve(&mut self, target: &Target, claim: &str) -> Result<(), NativeFailure> {
        if self.scopes.get(&target.label) != Some(target)
            || self.active.len() >= MAX_ACTIVE
            || self.seen.len() >= MAX_SEEN
            || !self.seen.insert(claim.into())
        {
            return Err(NativeFailure::Busy);
        }
        Ok(())
    }
}
impl NotificationHost {
    fn observe_connection(
        self: &Arc<Self>,
        app: AppHandle<CefRuntime>,
        connector: Arc<delidev_desktop::Connector>,
        connection: delidev_desktop::Connection,
        ledger: delidev_desktop::notification_connection::Ledger,
    ) -> Result<(), NativeFailure> {
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if self.stopping.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        if let Some(observer) = state.observers.get(&ledger.key) {
            return if observer.cancel.is_some() {
                Ok(())
            } else {
                Err(NativeFailure::Busy)
            };
        }
        if state.observers.len() >= MAX_ACTIVE {
            return Err(NativeFailure::Busy);
        }
        let (cancel, mut canceled) = oneshot::channel();
        let host = Arc::clone(self);
        let key = ledger.key.clone();
        let task = tauri::async_runtime::spawn(async move {
            let mut transitions = delidev_desktop::notification_connection::Transition::default();
            let mut cadence = tokio::time::interval(Duration::from_secs(30));
            cadence.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
            loop {
                tokio::select! {biased;_=&mut canceled=>break,_=cadence.tick()=>{}}
                if host.stopping.load(Ordering::Acquire) {
                    break;
                }
                let current = host
                    .state
                    .lock()
                    .ok()
                    .map(|state| {
                        state
                            .scopes
                            .values()
                            .filter(|target| target.connection.as_ref() == Some(&ledger.key))
                            .filter_map(|target| {
                                state
                                    .authorities
                                    .get(&target.label)
                                    .cloned()
                                    .map(|authority| (target.clone(), authority))
                            })
                            .collect::<Vec<_>>()
                    })
                    .unwrap_or_default();
                let current = current.into_iter().find_map(|(target, authority)| {
                    app.get_webview_window(&target.label)
                        .filter(|window| super::recheck_authority(window, &authority).is_ok())
                        .map(|window| (target, window, authority))
                });
                let Some((validated_target, window, authority)) = current else {
                    break;
                };
                let native = Arc::clone(&connector);
                let original = connection.clone();
                // Await the bounded native child even on scope disposal. Quit
                // joins this original read; canceling a future
                // cannot abandon its process.
                let observed = tauri::async_runtime::spawn_blocking(move || {
                    native.observe_notification_connection(&original)
                })
                .await;
                if host.stopping.load(Ordering::Acquire) {
                    break;
                }
                if super::recheck_authority(&window, &authority).is_err() {
                    continue;
                }
                let Ok(Ok(observed)) = observed else {
                    tracing::warn!(
                        operation = "notification_connection_observation",
                        code = "unconfirmed"
                    );
                    break;
                };
                if matches!(
                    observed,
                    delidev_desktop::notification_connection::Observation::AuthenticatedSuccess { .. }
                ) {
                    match ledger.acknowledged(&observed) {
                        Ok(true) => transitions = Default::default(),
                        Ok(false) => {}
                        Err(_) => break,
                    }
                }
                let edge = match transitions.observe(&observed, &connection.server_id) {
                    Ok(v) => v,
                    Err(_) => break,
                };
                if let Some(kind) = edge {
                    if host.current(&validated_target.label, &validated_target.scope) {
                        let target = validated_target;
                        match ledger.reserve(kind) {
                            Ok(Some(notice)) => {
                                let _ = host.start(app.clone(), target, notice);
                            }
                            Ok(None) => {}
                            Err(_) => tracing::warn!(
                                operation = "notification_connection_reservation",
                                code = "unconfirmed"
                            ),
                        }
                    }
                }
            }
            tracing::info!(operation = "notification_connection_observation_joined");
            if let Ok(mut state) = host.state.lock() {
                state.observers.remove(&ledger.key);
            }
        });
        state.observers.insert(
            key,
            Observer {
                cancel: Some(cancel),
                task,
            },
        );
        Ok(())
    }

    pub fn current(&self, label: &str, scope: &str) -> bool {
        !self.stopping.load(Ordering::Acquire)
            && self.state.lock().is_ok_and(|state| {
                state
                    .scopes
                    .get(label)
                    .is_some_and(|target| target.scope == scope)
            })
    }

    fn request_permission(self: &Arc<Self>) -> Result<oneshot::Receiver<Readiness>, NativeFailure> {
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if self.stopping.load(Ordering::Acquire) || self.prompting.swap(true, Ordering::AcqRel) {
            return Err(NativeFailure::Busy);
        }
        let (cancel, mut canceled) = oneshot::channel();
        let (sender, receiver) = oneshot::channel();
        let host = Arc::clone(self);
        let task = tauri::async_runtime::spawn(async move {
            // Polling never requests permission. If a native prompt outlives
            // the deadline, do not launch another in this process;
            // only a completed platform response can release this
            // admission gate.
            let (readiness, completed) = tokio::select! {biased;
                _=&mut canceled=>(Readiness::unavailable(PermissionProblem::OsUnavailable),false),
                result=tokio::time::timeout(Duration::from_secs(120),notifications::permission(true))=>match result{
                    Ok(value)=>(value,true),
                    Err(_)=>(Readiness::unavailable(PermissionProblem::OsUnavailable),false),
                },
            };
            tracing::info!(operation="notification_permission",state=?readiness.permission);
            let _ = sender.send(readiness);
            if let Ok(mut state) = host.state.lock() {
                state.prompt.take();
                if completed {
                    host.prompting.store(false, Ordering::Release);
                }
            }
        });
        state.prompt = Some(Prompt { cancel, task });
        Ok(receiver)
    }

    fn at_capacity(&self) -> bool {
        self.stopping.load(Ordering::Acquire)
            || self
                .state
                .lock()
                .map(|v| v.active.len() >= MAX_ACTIVE || v.seen.len() >= MAX_SEEN)
                .unwrap_or(true)
    }

    fn start(
        self: &Arc<Self>,
        app: AppHandle<CefRuntime>,
        target: Target,
        notice: Notice,
    ) -> Result<oneshot::Receiver<PresentationResult>, NativeFailure> {
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if self.stopping.load(Ordering::Acquire) {
            return Err(NativeFailure::Busy);
        }
        state.reserve(&target, &notice.claim_id)?;
        // A process-lifetime bound also covers callbacks whose platform release
        // is uncertain. mac-usernotifications 0.3.1 consumes its response guard
        // before waiting, so cancellation cannot prove delegate-map release.
        // Keep this bound until every backend offers verified owned
        // cancellation; never recycle an old claim into another display
        // attempt.
        let (cancel, mut canceled) = oneshot::channel();
        let (sender, receiver) = oneshot::channel();
        let host = Arc::clone(self);
        let original = target.clone();
        let id = notice.claim_id.clone();
        let task = tauri::async_runtime::spawn(async move {
            let operation = async {
                let readiness = notifications::permission(false).await;
                match readiness.permission {
                    Permission::Granted | Permission::ServiceAvailable => {
                        notifications::present(&notice).await
                    }
                    Permission::Denied | Permission::NotDetermined => {
                        Err(PresentationResult::Denied)
                    }
                    Permission::Unavailable => Err(PresentationResult::Failed),
                }
            };
            let submitted = tokio::select! {biased;
                _=&mut canceled=>Err(PresentationResult::Uncertain),
                result=tokio::time::timeout(Duration::from_secs(10),operation)=>result.unwrap_or(Err(PresentationResult::Uncertain)),
            };
            match submitted {
                Ok(mut handle) => {
                    let _ = sender.send(PresentationResult::Submitted);
                    tracing::info!(operation = "notification_submit", state = "submitted");
                    let activate = tokio::select! {biased;
                        _=&mut canceled=>false,
                        _=tokio::time::sleep(LIFETIME)=>false,
                        result=handle.wait()=>result,
                    };
                    if activate {
                        let app = app.clone();
                        let target = original.clone();
                        let inbox = notice.inbox_id.clone();
                        let host = Arc::clone(&host);
                        let queued = app.clone();
                        if app
                            .run_on_main_thread(move || {
                                if !host.stopping.load(Ordering::Acquire)
                                    && host.state.lock().is_ok_and(|state| {
                                        state.scopes.get(&target.label) == Some(&target)
                                    })
                                {
                                    if matches!(
                                        notice.kind,
                                        notifications::NotificationKind::ServerLost
                                            | notifications::NotificationKind::ServerRestored
                                    ) {
                                        tray_host::activate_connection_diagnostics(
                                            &queued,
                                            target.label,
                                            target.instance,
                                            target.scope,
                                        );
                                    } else {
                                        tray_host::activate_inbox(
                                            &queued,
                                            target.label,
                                            target.instance,
                                            target.scope,
                                            inbox,
                                        );
                                    }
                                }
                            })
                            .is_err()
                        {
                            tracing::warn!(
                                operation = "notification_activate",
                                code = "window-unavailable"
                            );
                        }
                    }
                    if tokio::time::timeout(Duration::from_secs(3), handle.close())
                        .await
                        .is_err()
                    {
                        tracing::warn!(operation = "notification_close", code = "uncertain");
                    }
                }
                Err(result) => {
                    let _ = sender.send(result);
                    tracing::warn!(operation="notification_submit",state=?result);
                }
            }
            if let Ok(mut state) = host.state.lock() {
                state.active.remove(&notice.claim_id);
            }
        });
        state.active.insert(
            id,
            Active {
                target,
                cancel: Some(cancel),
                task,
            },
        );
        Ok(receiver)
    }

    pub fn remove(&self, label: &str) {
        if let Ok(mut state) = self.state.lock() {
            state.scopes.remove(label);
            state.authorities.remove(label);
            state.cancel_label(label);
            state.cancel_unused_observers();
        }
    }

    pub fn stop(&self) {
        if self.stopping.swap(true, Ordering::AcqRel) {
            return;
        }
        let (active, prompt, observers) = if let Ok(mut state) = self.state.lock() {
            state.scopes.clear();
            state.authorities.clear();
            (
                std::mem::take(&mut state.active),
                state.prompt.take(),
                std::mem::take(&mut state.observers),
            )
        } else {
            (BTreeMap::new(), None, BTreeMap::new())
        };
        let mut tasks = std::collections::VecDeque::with_capacity(active.len() + 1);
        for (_, active) in active {
            if let Some(cancel) = active.cancel {
                let _ = cancel.send(());
            }
            tasks.push_back(active.task);
        }
        for (_, observer) in observers {
            if let Some(cancel) = observer.cancel {
                let _ = cancel.send(());
            }
            tasks.push_back(observer.task);
        }
        if let Some(prompt) = prompt {
            let _ = prompt.cancel.send(());
            tasks.push_back(prompt.task);
        }
        tauri::async_runtime::block_on(async {
            if tokio::time::timeout(Duration::from_secs(15), async {
                while let Some(task) = tasks.front_mut() {
                    let _ = task.await;
                    tasks.pop_front();
                }
            })
            .await
            .is_err()
            {
                for task in &tasks {
                    task.abort();
                }
                for task in tasks {
                    let _ = task.await;
                }
                tracing::warn!(
                    operation = "notification_shutdown",
                    code = "cleanup-uncertain"
                );
            }
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn target(label: &str, instance: Option<&str>) -> Target {
        Target {
            label: label.into(),
            scope: uuid::Uuid::now_v7().to_string(),
            instance: instance.map(str::to_owned),
            connection: None,
        }
    }
    #[test]
    fn claims_cannot_move_between_windows_instances_or_replaced_scopes() {
        let mut state = State::default();
        let main = target("main", None);
        let saved = target("server-fixture", Some("original"));
        state.begin(main.clone()).unwrap();
        state.begin(saved.clone()).unwrap();
        state.reserve(&main, "claimed").unwrap();
        assert!(state.reserve(&saved, "claimed").is_err());
        let mut mixed = saved.clone();
        mixed.scope = main.scope.clone();
        assert!(state.reserve(&mixed, "unclaimed").is_err());
        mixed = saved.clone();
        mixed.instance = Some("replacement".into());
        assert!(state.reserve(&mixed, "unclaimed").is_err());
        state
            .begin(target("server-fixture", Some("replacement")))
            .unwrap();
        assert!(state.reserve(&saved, "unclaimed").is_err());
        assert!(!state.seen.contains("unclaimed"));
        let newer = target("main", None);
        state.begin(newer.clone()).unwrap();
        state.end(&main);
        assert!(state.reserve(&newer, "fresh").is_ok());
        state.end(&newer);
        assert!(state.reserve(&newer, "ended").is_err());
        state.begin(main.clone()).unwrap();
        assert!(state.reserve(&main, "claimed").is_err());
    }
    #[test]
    fn capacity_never_recycles_a_claim_or_evicts_another_window() {
        let mut state = State::default();
        let main = target("main", None);
        state.begin(main.clone()).unwrap();
        for index in 0..MAX_SEEN {
            state.reserve(&main, &index.to_string()).unwrap();
        }
        assert!(state.reserve(&main, "overflow").is_err());
        assert_eq!(state.seen.len(), MAX_SEEN);
        for index in 1..MAX_ACTIVE {
            state
                .begin(target(&format!("saved-{index}"), Some("instance")))
                .unwrap();
        }
        assert!(state.begin(target("overflow", None)).is_err());
        state.begin(target("main", None)).unwrap();
        assert_eq!(state.scopes.len(), MAX_ACTIVE);
    }
    #[test]
    fn removal_and_exit_cancel_and_join_owned_work_without_releasing_claims() {
        let host = NotificationHost::default();
        let original = target("main", None);
        let finished = Arc::new(AtomicBool::new(false));
        let done = Arc::clone(&finished);
        let (cancel, canceled) = oneshot::channel();
        let task = tauri::async_runtime::spawn(async move {
            let _ = canceled.await;
            done.store(true, Ordering::Release);
        });
        {
            let mut state = host.state.lock().unwrap();
            state.begin(original.clone()).unwrap();
            state.reserve(&original, "claimed").unwrap();
            state.active.insert(
                "claimed".into(),
                Active {
                    target: original.clone(),
                    cancel: Some(cancel),
                    task,
                },
            );
        }
        host.remove("main");
        {
            let mut state = host.state.lock().unwrap();
            assert!(state.scopes.is_empty());
            assert!(state.active["claimed"].cancel.is_none());
            assert!(state.reserve(&original, "another").is_err());
            assert!(state.seen.contains("claimed"));
        }
        host.stop();
        host.stop();
        assert!(finished.load(Ordering::Acquire));
        assert!(host.at_capacity());
        assert!(host.state.lock().unwrap().active.is_empty());
    }
}
