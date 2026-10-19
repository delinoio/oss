//! Current-session input worker and native shell integration.
//!
//! The observer gates events before buffering. The worker proves that the
//! complete observed key sequence accounts for the exact accessibility edit
//! before handing the completed word to the core's second write-time check.

use std::{
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
        mpsc,
    },
    time::{Duration, Instant},
};

use fixkey_core::{
    decision::{MissingValidator, Validators},
    observation::{await_completed_word, can_start, completed_word, pending_matches},
    platform::{Adapter, Application, Cursor, Identity, Key, KeyEvent},
    session::{Accessibility, CapturedContext, FieldSnapshot},
    tokenizer::{TokenOutcome, Tokenizer},
};
use tauri::{Emitter, Manager};
use tauri_plugin_global_shortcut::Shortcut;

use crate::{
    desktop::{
        AppHandle, CandidateAction, PICKER_KEYS, Shared, candidates, hide_candidates,
        show_candidates,
    },
    settings::{AppIdentity, ExcludedApp},
};

pub fn os_language() -> &'static str {
    if sys_locale::get_locale().is_some_and(|locale| locale.starts_with("ko")) {
        "ko"
    } else {
        "en"
    }
}

fn identity(identity: &Identity) -> AppIdentity {
    match identity {
        Identity::Macos(id) => AppIdentity::Macos {
            bundle_id: id.clone(),
        },
        Identity::WindowsApp(id) => AppIdentity::WindowsApp {
            app_user_model_id: id.clone(),
        },
        Identity::WindowsExecutable(path) => AppIdentity::WindowsExecutable { path: path.clone() },
        Identity::Linux {
            class,
            desktop_entry_id,
        } => AppIdentity::Linux {
            class: class.clone(),
            desktop_entry_id: desktop_entry_id.clone(),
        },
    }
}
fn allowed(application: &Application, settings: &crate::settings::Settings) -> bool {
    !matches!(&application.identity,Identity::Macos(id) if id=="io.delino.fixkey")
        && !settings
            .excluded_apps
            .iter()
            .any(|app| app.identity == identity(&application.identity))
}

fn discover_apps() -> Vec<ExcludedApp> {
    #[cfg(target_os = "macos")]
    {
        let mut apps = Vec::new();
        let mut roots = vec![
            std::path::PathBuf::from("/Applications"),
            std::path::PathBuf::from("/System/Applications"),
        ];
        if let Some(home) = dirs::home_dir() {
            roots.push(home.join("Applications"));
        }
        let mut pending = roots.into_iter().map(|path| (path, 0)).collect::<Vec<_>>();
        while let Some((directory, depth)) = pending.pop() {
            let Ok(entries) = std::fs::read_dir(directory) else {
                continue;
            };
            for entry in entries.flatten() {
                let path = entry.path();
                if path.extension().is_some_and(|ext| ext == "app") {
                    let Ok(info) = plist::Value::from_file(path.join("Contents/Info.plist")) else {
                        continue;
                    };
                    let Some(info) = info.as_dictionary() else {
                        continue;
                    };
                    let Some(id) = info
                        .get("CFBundleIdentifier")
                        .and_then(plist::Value::as_string)
                    else {
                        continue;
                    };
                    if id == "io.delino.fixkey" {
                        continue;
                    }
                    let name = info
                        .get("CFBundleDisplayName")
                        .or_else(|| info.get("CFBundleName"))
                        .and_then(plist::Value::as_string)
                        .unwrap_or(id);
                    if !apps.iter().any(|app: &ExcludedApp| {
                        app.identity
                            == AppIdentity::Macos {
                                bundle_id: id.into(),
                            }
                    }) {
                        apps.push(ExcludedApp {
                            identity: AppIdentity::Macos {
                                bundle_id: id.into(),
                            },
                            display_name: name.into(),
                        });
                    }
                } else if depth < 2
                    && entry
                        .file_type()
                        .is_ok_and(|kind| kind.is_dir() && !kind.is_symlink())
                {
                    pending.push((path, depth + 1));
                }
            }
        }
        apps.sort_by(|a, b| a.display_name.cmp(&b.display_name));
        apps
    }
    #[cfg(any(target_os = "windows", target_os = "linux"))]
    {
        let mut apps: Vec<_> = fixkey_core::platform::available_applications()
            .into_iter()
            .map(|app| ExcludedApp {
                identity: identity(&app.identity),
                display_name: app.name,
            })
            .collect();
        apps.sort_by(|a, b| a.display_name.cmp(&b.display_name));
        apps.dedup_by(|a, b| a.identity == b.identity);
        apps
    }
}
#[derive(Default)]
struct ApplicationInventory {
    fetched_at: Option<std::time::Instant>,
    apps: Vec<ExcludedApp>,
}
impl ApplicationInventory {
    fn get(
        &mut self,
        now: std::time::Instant,
        refresh: bool,
        discover: impl FnOnce() -> Vec<ExcludedApp>,
    ) -> &[ExcludedApp] {
        if refresh
            || self
                .fetched_at
                .is_none_or(|at| now.duration_since(at) >= Duration::from_secs(30))
        {
            self.apps = discover();
            self.fetched_at = Some(now);
        }
        &self.apps
    }
}
fn application_inventory(refresh: bool) -> Vec<ExcludedApp> {
    static INVENTORY: std::sync::OnceLock<std::sync::Mutex<ApplicationInventory>> =
        std::sync::OnceLock::new();
    INVENTORY
        .get_or_init(Default::default)
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
        .get(std::time::Instant::now(), refresh, discover_apps)
        .to_vec()
}
pub fn available_apps() -> Vec<ExcludedApp> {
    application_inventory(true)
}
pub fn unresolved_apps(excluded: &[ExcludedApp]) -> Vec<usize> {
    if excluded.is_empty() {
        return Vec::new();
    }
    let available = application_inventory(false);
    excluded
        .iter()
        .enumerate()
        .filter_map(|(index, app)| {
            (!available.iter().any(|known| known.identity == app.identity)).then_some(index)
        })
        .collect()
}
#[cfg(target_os = "macos")]
mod input_monitoring {
    #[repr(u32)]
    enum RequestType {
        ListenEvent = 1,
    }
    const ACCESS_GRANTED: u32 = 0;

    #[link(name = "IOKit", kind = "framework")]
    unsafe extern "C" {
        fn IOHIDCheckAccess(request_type: RequestType) -> u32;
        fn IOHIDRequestAccess(request_type: RequestType) -> bool;
    }

    pub fn granted() -> bool {
        // SAFETY: The SDK-defined listen-event request only queries permission.
        unsafe { IOHIDCheckAccess(RequestType::ListenEvent) == ACCESS_GRANTED }
    }

    pub fn request() -> bool {
        // SAFETY: This requests user consent without opening a device or
        // reading events. Unlike the CG request, it registers the app in the
        // Input Monitoring list even when accessibility is already granted.
        unsafe { IOHIDRequestAccess(RequestType::ListenEvent) }
    }
}

/// Requests Input Monitoring registration even when accessibility already
/// provides effective listen access. The result does not prove a list entry.
#[cfg(target_os = "macos")]
pub fn register_input_monitoring() {
    let granted = input_monitoring::request();
    tracing::info!(
        reason = "input-permission-registration-requested",
        input_monitoring_granted = granted,
        "fixkey input monitoring registration requested"
    );
}

/// Other platforms do not use the macOS permission registration API.
#[cfg(not(target_os = "macos"))]
pub const fn register_input_monitoring() {}

#[cfg(any(target_os = "macos", test))]
const fn macos_permission_reason(
    accessibility: bool,
    input_monitoring: bool,
) -> Option<&'static str> {
    match (accessibility, input_monitoring) {
        (false, _) => Some("permission-missing"),
        (true, false) => Some("input-permission-missing"),
        (true, true) => None,
    }
}

fn permission_reason() -> Option<&'static str> {
    #[cfg(target_os = "macos")]
    {
        #[link(name = "ApplicationServices", kind = "framework")]
        unsafe extern "C" {
            fn AXIsProcessTrusted() -> bool;
        }
        // SAFETY: These parameterless queries do not prompt or read input.
        unsafe { macos_permission_reason(AXIsProcessTrusted(), input_monitoring::granted()) }
    }
    #[cfg(not(target_os = "macos"))]
    (!Adapter::permission()).then_some("permission-missing")
}

/// Requests access from `FixKey` while its permission dialog is visible.
pub fn request_permission() -> Result<(), &'static str> {
    #[cfg(target_os = "macos")]
    {
        use std::{ffi::c_void, ptr};
        type Ref = *const c_void;
        #[link(name = "ApplicationServices", kind = "framework")]
        unsafe extern "C" {
            static kAXTrustedCheckOptionPrompt: Ref;
            fn AXIsProcessTrustedWithOptions(options: Ref) -> bool;
        }
        #[link(name = "CoreFoundation", kind = "framework")]
        unsafe extern "C" {
            static kCFBooleanTrue: Ref;
            fn CFDictionaryCreate(
                allocator: Ref,
                keys: *const Ref,
                values: *const Ref,
                count: isize,
                key_callbacks: Ref,
                value_callbacks: Ref,
            ) -> Ref;
            fn CFRelease(value: Ref);
        }
        // SAFETY: Both entries are immortal framework constants. Null callbacks
        // leave their ownership unchanged; the dictionary is released after
        // use.
        let needs_settings = unsafe {
            let key = kAXTrustedCheckOptionPrompt;
            let value = kCFBooleanTrue;
            let options = CFDictionaryCreate(
                ptr::null(),
                &raw const key,
                &raw const value,
                1,
                ptr::null(),
                ptr::null(),
            );
            if options.is_null() {
                return Err("permission-request-unavailable");
            }
            let trusted = AXIsProcessTrustedWithOptions(options);
            CFRelease(options);
            // Request explicit Input Monitoring registration through IOHID.
            // CGRequestListenEventAccess alone can leave the app unlisted.
            // Keep this independent of the asynchronous accessibility result.
            let input_monitoring_granted = input_monitoring::request();
            tracing::info!(
                reason = "input-permission-requested",
                accessibility_granted = trusted,
                input_monitoring_granted,
                "fixkey input monitoring permission requested"
            );
            !trusted || !input_monitoring_granted
        };
        tracing::info!(
            reason = "permission-requested",
            "fixkey permission requested"
        );
        // macOS may suppress an already-decided consent prompt. Always give
        // the user a visible recovery path while permission remains missing.
        if needs_settings {
            open_permission_settings()
        } else {
            Ok(())
        }
    }
    #[cfg(not(target_os = "macos"))]
    open_permission_settings()
}

pub fn open_permission_settings() -> Result<(), &'static str> {
    #[cfg(target_os = "macos")]
    let command = {
        #[link(name = "ApplicationServices", kind = "framework")]
        unsafe extern "C" {
            fn AXIsProcessTrusted() -> bool;
        }
        // SAFETY: This parameterless query neither prompts nor reads user
        // input.
        let pane = if unsafe { AXIsProcessTrusted() } {
            "x-apple.systempreferences:com.apple.preference.security?Privacy_ListenEvent"
        } else {
            "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
        };
        std::process::Command::new("open").arg(pane).spawn()
    };
    #[cfg(target_os = "windows")]
    let command = std::process::Command::new("explorer.exe")
        .arg("ms-settings:easeofaccess")
        .spawn();
    #[cfg(target_os = "linux")]
    let command = std::process::Command::new("xdg-open")
        .arg("help:gnome-help/a11y")
        .spawn();
    command
        .map(|_| ())
        .map_err(|_| "permission-settings-unavailable")
}

struct Context {
    before: FieldSnapshot,
    tokenizer: Tokenizer,
    keys: Vec<char>,
    pending_keys: Option<PendingKeys>,
}
#[derive(Clone, Copy)]
struct PendingKeys {
    snapshot_at: Instant,
    deadline: Instant,
}
impl PendingKeys {
    fn new(snapshot_at: Instant) -> Self {
        Self {
            snapshot_at,
            deadline: snapshot_at + Duration::from_millis(100),
        }
    }

    fn accepts(self, observed_at: Instant, now: Instant) -> bool {
        observed_at <= self.snapshot_at && now <= self.deadline
    }
}
fn insertion_ahead(before: &FieldSnapshot, current: &FieldSnapshot, keys: &[char]) -> bool {
    current.target == before.target
        && current.sensitivity == before.sensitivity
        && current.supports_write
        && current.selection_len == 0
        && current.caret > before.caret + keys.len()
        && before
            .text
            .chars()
            .take(before.caret)
            .eq(current.text.chars().take(before.caret))
        && before
            .text
            .chars()
            .skip(before.caret)
            .eq(current.text.chars().skip(current.caret))
}
#[derive(Default)]
struct InputState {
    baseline: Option<FieldSnapshot>,
    cursor: Option<Cursor>,
    context: Option<Context>,
    awaiting_ime: bool,
}
impl InputState {
    fn clear(&mut self) {
        *self = Self::default();
    }
}
pub fn start(app: AppHandle, shared: Shared) {
    let active = Arc::new(AtomicBool::new(false));
    let lost = Arc::new(AtomicBool::new(false));
    let (sender, receiver) = mpsc::sync_channel::<KeyEvent>(128);
    let policy = shared
        .0
        .lock()
        .expect("desktop state initialized")
        .input_policy
        .clone();
    let gate: fixkey_core::platform::Gate = Arc::new(move |application| {
        policy
            .read()
            .ok()
            .and_then(|policy| policy.clone())
            .is_some_and(|settings| allowed(application, &settings))
    });
    let handler_lost = lost.clone();
    let handler_active = active.clone();
    let handler: fixkey_core::platform::Handler = Arc::new(move |event| {
        if !handler_active.load(Ordering::Acquire) {
            return;
        }
        if sender.try_send(event).is_err() {
            handler_lost.store(true, Ordering::Release);
        }
    });
    let (shortcuts, shortcut_events) = mpsc::sync_channel::<Shortcut>(16);
    app.manage(ShortcutQueue {
        sender: shortcuts,
        lost: lost.clone(),
    });
    let shortcut_app = app.clone();
    let shortcut_lost = lost.clone();
    std::thread::spawn(move || {
        while let Ok(key) = shortcut_events.recv() {
            if !shortcut_lost.load(Ordering::Acquire) {
                shortcut(&shortcut_app, key);
            }
        }
    });
    let observer_ready = Arc::new(AtomicBool::new(false));
    let thread_ready = observer_ready.clone();
    let observer_active = active.clone();
    std::thread::spawn(move || {
        loop {
            if Adapter::permission() {
                let _ = Adapter::observe(&observer_active, &thread_ready, &gate, &handler);
            }
            std::thread::sleep(Duration::from_secs(1));
        }
    });
    let control = Arc::new(std::sync::Mutex::new(None));
    app.manage(WorkerControl(control.clone()));
    let handle = std::thread::spawn(move || {
        run_worker(&app, &shared, &active, &lost, &observer_ready, &receiver);
    });
    if let Ok(mut worker) = control.lock() {
        *worker = Some(handle);
    }
}

struct ShortcutQueue {
    sender: mpsc::SyncSender<Shortcut>,
    lost: Arc<AtomicBool>,
}
pub fn enqueue_shortcut(app: &AppHandle, key: Shortcut) {
    if let Some(queue) = app.try_state::<ShortcutQueue>()
        && queue.sender.try_send(key).is_err()
    {
        queue.lost.store(true, Ordering::Release);
    }
}

struct WorkerControl(Arc<std::sync::Mutex<Option<std::thread::JoinHandle<()>>>>);

pub fn stop_for_update(app: &AppHandle) -> Result<(), &'static str> {
    app.state::<Shared>()
        .0
        .lock()
        .map_err(|_| "desktop-state-unavailable")?
        .stop_for_update();
    hide_candidates(app);
    let control = app.state::<WorkerControl>();
    let handle = control
        .0
        .lock()
        .map_err(|_| "update-stop-failed")?
        .take()
        .ok_or("update-stop-failed")?;
    let deadline = std::time::Instant::now() + Duration::from_secs(2);
    while !handle.is_finished() {
        if std::time::Instant::now() >= deadline {
            *control.0.lock().map_err(|_| "update-stop-failed")? = Some(handle);
            return Err("update-stop-failed");
        }
        std::thread::sleep(Duration::from_millis(5));
    }
    handle.join().map_err(|_| "update-stop-failed")
}

fn run_worker(
    app: &AppHandle,
    shared: &Shared,
    active: &AtomicBool,
    lost: &AtomicBool,
    observer_ready: &AtomicBool,
    receiver: &mpsc::Receiver<KeyEvent>,
) {
    let mut adapter = Adapter::new();
    let mut input = InputState::default();
    let mut revision = 0;
    loop {
        let event = receiver.recv_timeout(Duration::from_millis(250)).ok();
        // Wait for the host to commit this physical event before acquiring
        // state. The observer remains independent during this bounded wait.
        if event.is_some() {
            std::thread::sleep(Duration::from_millis(8));
        }
        let Ok(mut runtime) = shared.0.lock() else {
            break;
        };
        if runtime.stopping {
            active.store(false, Ordering::Release);
            adapter.clear();
            break;
        }
        if revision != runtime.revision {
            revision = runtime.revision;
            // Rechecking permissions must rebuild transports that were absent
            // or disconnected when the long-lived worker first started.
            adapter = Adapter::new();
            input.clear();
            while receiver.try_recv().is_ok() {}
            runtime.preferences.session().clear_transient();
        }
        let previous_error = runtime.last_error.clone();
        let metadata = adapter.metadata();
        let status = status(&runtime, &metadata, observer_ready.load(Ordering::Acquire));
        let enabled = status == "active";
        let changed = runtime.status != status;
        runtime.status = status.into();
        set_active(&mut runtime, active, enabled);
        let mut hide = false;
        if !enabled || lost.swap(false, Ordering::AcqRel) {
            adapter.clear();
            input.clear();
            runtime.preferences.session().clear_transient();
            if runtime.manual.selected().is_some() {
                runtime.manual.close();
                runtime.generation += 1;
                hide = true;
            }
        }
        let current_cursor = if enabled {
            if let Ok((_, application)) = &metadata {
                adapter.set_application(&application.identity);
            }
            adapter.cursor().ok()
        } else {
            None
        };
        let needs_snapshot = snapshot_required(
            event.is_some(),
            input.awaiting_ime
                || input
                    .context
                    .as_ref()
                    .is_some_and(|pending| pending.pending_keys.is_some())
                || runtime.preferences.session().backspace_pending(),
            input.cursor,
            current_cursor,
        );
        if enabled && current_cursor.is_some() && needs_snapshot {
            let (snapshot, composing) = capture(&mut runtime, &mut adapter, &mut hide);
            input.awaiting_ime = composing;
            if let Some(event) = event {
                process_captured_key(
                    &mut runtime,
                    &mut adapter,
                    &mut input,
                    event,
                    snapshot.as_ref(),
                    metadata
                        .as_ref()
                        .is_ok_and(|(target, _)| *target == event.target),
                );
            } else if input.context.is_none() {
                runtime
                    .preferences
                    .session()
                    .observe_after_backspace(&mut adapter);
            }
            if !composing {
                update_baseline(&mut adapter, &mut input.baseline, &mut input.context);
                input.cursor = adapter.cursor().ok();
            }
        }
        if enabled && current_cursor.is_none() {
            input.clear();
            runtime.preferences.session().clear_transient();
        }
        let diagnostic = runtime
            .last_error
            .clone()
            .filter(|error| Some(error) != previous_error.as_ref());
        drop(runtime);
        if hide {
            hide_candidates(app);
        }
        publish_status(app, status, changed, diagnostic.as_deref());
    }
}

fn process_captured_key(
    runtime: &mut crate::desktop::Runtime,
    adapter: &mut Adapter,
    input: &mut InputState,
    event: KeyEvent,
    snapshot: Option<&FieldSnapshot>,
    target_matches: bool,
) {
    if !target_matches || (snapshot.is_none() && !input.awaiting_ime) {
        input.context = None;
        return;
    }
    if runtime.manual.selected().is_some() {
        return;
    }
    if input.context.as_ref().is_some_and(|pending| {
        pending
            .pending_keys
            .is_some_and(|waiting| !waiting.accepts(event.observed_at, Instant::now()))
    }) {
        input.context = None;
        tracing::info!(
            reason = "pending-key-delivery-invalidated",
            "fixkey input context discarded"
        );
    }
    process_key(
        runtime,
        adapter,
        &mut input.context,
        input.baseline.as_ref(),
        event,
        snapshot,
    );
}

fn set_active(runtime: &mut crate::desktop::Runtime, active: &AtomicBool, enabled: bool) {
    runtime.active = enabled;
    active.store(enabled, Ordering::Release);
    if let Ok(mut policy) = runtime.input_policy.write() {
        *policy = enabled.then(|| runtime.preferences.current().clone());
    }
    runtime
        .preferences
        .session()
        .set_permission_granted(enabled);
}

// Idle checks compare content-free native caret/selection markers. Text is
// refreshed on a cursor transition so the next word starts from that position.
fn snapshot_required(
    has_event: bool,
    awaiting_commit: bool,
    baseline: Option<Cursor>,
    focused: Option<Cursor>,
) -> bool {
    has_event || awaiting_commit || baseline != focused
}

fn publish_status(app: &AppHandle, status: &'static str, changed: bool, diagnostic: Option<&str>) {
    if let Some(reason) = diagnostic {
        crate::desktop::refresh_tray(app);
        crate::desktop::notify(app, reason);
    } else if changed {
        tracing::info!(reason = status, "fixkey correction status changed");
        crate::desktop::refresh_tray(app);
        crate::desktop::notify(app, status);
    }
}

fn capture(
    runtime: &mut crate::desktop::Runtime,
    adapter: &mut Adapter,
    hide: &mut bool,
) -> (Option<FieldSnapshot>, bool) {
    match adapter.snapshot() {
        Ok(snapshot) => {
            *hide |= observe_snapshot(runtime, &snapshot);
            (Some(snapshot), false)
        }
        Err(reason) => {
            if runtime.manual.selected().is_some() {
                runtime.manual.close();
                runtime.generation += 1;
                *hide = true;
            }
            // A read failure discards this observation but does not quarantine
            // the target. Only failed writes require an explicit recheck.
            if reason != fixkey_core::SkipReason::Composing {
                runtime.preferences.session().clear_transient();
                tracing::info!(reason = reason.as_str(), "fixkey snapshot unavailable");
            }
            (None, reason == fixkey_core::SkipReason::Composing)
        }
    }
}

fn update_baseline(
    adapter: &mut Adapter,
    baseline: &mut Option<FieldSnapshot>,
    context: &mut Option<Context>,
) {
    let next = adapter.snapshot().ok();
    let now = Instant::now();
    if context.as_ref().is_some_and(|pending| {
        pending
            .pending_keys
            .is_some_and(|waiting| now > waiting.deadline)
    }) {
        tracing::info!(
            reason = "pending-key-delivery-expired",
            "fixkey input context discarded"
        );
        *context = None;
    }
    if let Some(pending) = context.as_mut() {
        let forward = next.as_ref().is_some_and(|snapshot| {
            baseline.as_ref().is_none_or(|previous| {
                previous.target != snapshot.target || snapshot.caret >= previous.caret
            })
        });
        if forward
            && next
                .as_ref()
                .is_some_and(|snapshot| pending_matches(&pending.before, snapshot, &pending.keys))
        {
            pending.pending_keys = None;
        } else if forward
            && next
                .as_ref()
                .is_some_and(|snapshot| insertion_ahead(&pending.before, snapshot, &pending.keys))
        {
            // The host can publish text while its already-observed event is
            // still resolving native metadata. Retain only this insertion
            // shape and only events observed before this snapshot. Never
            // advance the baseline to text unexplained by delivered keys.
            let waiting = pending
                .pending_keys
                .get_or_insert_with(|| PendingKeys::new(now));
            if now <= waiting.deadline {
                return;
            }
            tracing::info!(
                reason = "pending-key-delivery-expired",
                "fixkey input context discarded"
            );
            *context = None;
        } else {
            tracing::info!(
                reason = "pending-context-mismatch",
                "fixkey input context discarded"
            );
            *context = None;
        }
    }
    *baseline = next;
}

fn observe_snapshot(runtime: &mut crate::desktop::Runtime, snapshot: &FieldSnapshot) -> bool {
    runtime.preferences.session().observe(snapshot);
    let was_open = runtime.manual.selected().is_some();
    runtime.manual.observe(snapshot);
    if was_open && runtime.manual.selected().is_none() {
        runtime.generation += 1;
        return true;
    }
    false
}

fn status(
    runtime: &crate::desktop::Runtime,
    metadata: &Result<(fixkey_core::session::TargetId, Application), fixkey_core::SkipReason>,
    observer_ready: bool,
) -> &'static str {
    if runtime.preferences.needs_reconciliation() {
        "preferences-reconciliation-required"
    } else if runtime.preferences.current().paused {
        "paused"
    } else if !runtime.preferences.current().korean_enabled
        && !runtime.preferences.current().english_enabled
    {
        "languages-disabled"
    } else if let Some(reason) = permission_reason() {
        reason
    } else if !runtime.dictionaries_ready() {
        "dictionary-unavailable"
    } else if !observer_ready {
        "input-observer-unavailable"
    } else {
        match metadata {
            Ok((_, application)) if !allowed(application, runtime.preferences.current()) => {
                "excluded-app"
            }
            Ok((target, _)) => runtime
                .refused_target
                .filter(|(refused, _)| refused == target)
                .map_or("active", |(_, reason)| reason),
            Err(reason) => reason.as_str(),
        }
    }
}

fn process_key(
    runtime: &mut crate::desktop::Runtime,
    adapter: &mut Adapter,
    context: &mut Option<Context>,
    baseline: Option<&FieldSnapshot>,
    event: KeyEvent,
    snapshot: Option<&FieldSnapshot>,
) {
    match event.key {
        Key::Backspace => {
            *context = None;
            if runtime.preferences.session().on_backspace(adapter)
                == fixkey_core::session::BackspaceAction::Failed
            {
                runtime.refused_target = Some((event.target, "undo-failed"));
                runtime.last_error = Some("undo-failed".into());
            }
        }
        Key::Reset | Key::Paste | Key::Untrusted => {
            *context = None;
            runtime.preferences.session().clear_transient();
        }
        Key::Character(ch) => {
            if context.is_none()
                && ch.is_ascii_alphabetic()
                && let Some(before) = baseline
                && before.target == event.target
                && can_start(before)
            {
                runtime.preferences.session().next_word_started();
                *context = Some(Context {
                    before: before.clone(),
                    tokenizer: Tokenizer::new(),
                    keys: Vec::new(),
                    pending_keys: None,
                });
                tracing::info!(
                    reason = "word-context-started",
                    "fixkey physical word started"
                );
            }
            if ch == ' ' && context.is_none() {
                tracing::info!(
                    reason = "word-context-missing",
                    baseline_available = baseline.is_some(),
                    target_matches = baseline.is_some_and(|before| before.target == event.target),
                    boundary_available = baseline.is_some_and(can_start),
                    "fixkey word boundary has no physical input context"
                );
            }
            if let Some(pending) = context.as_mut() {
                pending.keys.push(ch);
            }
            if context
                .as_ref()
                .is_some_and(|pending| pending.keys.len() > 256)
            {
                *context = None;
                tracing::debug!(
                    reason = "word-context-too-long",
                    "fixkey input context discarded"
                );
            }
            if let Some(pending) = context.as_mut()
                && let Some(outcome) = pending.tokenizer.feed_char(ch)
            {
                if let TokenOutcome::Evaluate { word, boundary } = outcome
                    && let Some(current) = await_completed_word(
                        &pending.before,
                        &word.chars().collect::<Vec<_>>(),
                        &boundary,
                        Duration::from_millis(100),
                        || adapter.snapshot(),
                    )
                {
                    evaluate(
                        runtime,
                        adapter,
                        &pending.before,
                        &current,
                        &word,
                        &boundary,
                    );
                }
                *context = None;
            }
        }
        Key::Modifier => {}
        Key::Enter | Key::Tab => {
            if let Some(pending) = context.as_mut()
                && let Some(TokenOutcome::Evaluate { word, boundary }) =
                    pending.tokenizer.feed_enter_or_tab()
                && let Some(current) = snapshot
            {
                // Enter/Tab itself is never part of the
                // replacement. A committed host edit makes
                // this proof fail and discards held
                // punctuation.
                evaluate(runtime, adapter, &pending.before, current, &word, &boundary);
            }
            *context = None;
        }
    }
}

fn evaluate(
    runtime: &mut crate::desktop::Runtime,
    adapter: &mut Adapter,
    before: &FieldSnapshot,
    current: &FieldSnapshot,
    word_keys: &str,
    boundary: &str,
) {
    let keys: Vec<char> = word_keys.chars().collect();
    if let Some(word) = completed_word(before, current, &keys, boundary) {
        let validators = Validators {
            korean: runtime
                .korean
                .as_ref()
                .map_or(&MissingValidator, |validator| {
                    validator as &dyn fixkey_core::decision::LanguageValidator
                }),
            english: runtime
                .english
                .as_ref()
                .map_or(&MissingValidator, |validator| {
                    validator as &dyn fixkey_core::decision::LanguageValidator
                }),
        };
        let evaluation = runtime.preferences.session().on_word_complete(
            &word,
            &keys,
            boundary,
            CapturedContext {
                target: current.target,
                caret: current.caret,
            },
            adapter,
            &validators,
        );
        if evaluation.skip_reason == Some(fixkey_core::SkipReason::UnsupportedWrite) {
            runtime.refused_target = Some((current.target, "unsupported-write"));
            runtime.last_error = Some("unsupported-write".into());
        }
    } else {
        tracing::info!(
            reason = "stale-provenance",
            "fixkey input context discarded"
        );
    }
}

fn picker_action(key: Shortcut) -> Option<CandidateAction> {
    PICKER_KEYS
        .iter()
        .position(|name| {
            name.parse::<Shortcut>()
                .is_ok_and(|candidate| candidate == key)
        })
        .map(|index| match index {
            0 => CandidateAction::Next,
            1 => CandidateAction::Previous,
            2 => CandidateAction::Apply,
            _ => CandidateAction::Close,
        })
}

pub fn shortcut(app: &AppHandle, key: Shortcut) {
    let shared = app.state::<Shared>();
    let picker = shared
        .0
        .lock()
        .ok()
        .and_then(|runtime| runtime.manual.selected().map(|_| runtime.generation));
    if let Some(generation) = picker
        && let Some(action) = picker_action(key)
    {
        let _ = candidate_action(app, generation, action, None);
        return;
    }
    let mut adapter = Adapter::new();
    let Ok((_, application)) = adapter.metadata() else {
        return;
    };
    let Ok(mut runtime) = shared.0.lock() else {
        return;
    };
    if !runtime.active
        || !allowed(&application, runtime.preferences.current())
        || runtime
            .preferences
            .current()
            .shortcut
            .as_ref()
            .is_none_or(|value| {
                value
                    .parse::<Shortcut>()
                    .map_or(true, |expected| expected != key)
            })
    {
        return;
    }
    adapter.set_application(&application.identity);
    let Ok((x, y)) = adapter.location() else {
        drop(runtime);
        crate::desktop::record_error(app, "candidate-location-unavailable");
        return;
    };
    let Ok(snapshot) = adapter.snapshot() else {
        return;
    };
    let chars: Vec<char> = snapshot.text.chars().collect();
    let mut start = snapshot.caret;
    while start > 0
        && (chars[start - 1].is_alphabetic() || matches!(chars[start - 1], '\'' | '-' | '\u{2019}'))
    {
        start -= 1;
    }
    runtime.last_error = None;
    let settings = runtime.preferences.current().clone();
    // Split field borrows: validators remain immutable while the manual
    // session captures the source and validates every candidate.
    let crate::desktop::Runtime {
        manual,
        generation,
        korean,
        english,
        ..
    } = &mut *runtime;
    let validators = Validators {
        korean: korean.as_ref().map_or(&MissingValidator, |validator| {
            validator as &dyn fixkey_core::decision::LanguageValidator
        }),
        english: english.as_ref().map_or(&MissingValidator, |validator| {
            validator as &dyn fixkey_core::decision::LanguageValidator
        }),
    };
    if manual
        .open(
            start,
            snapshot.caret,
            settings.languages(),
            &mut adapter,
            &validators,
        )
        .is_err()
    {
        return;
    }
    *generation += 1;
    let state = candidates(&runtime);
    drop(runtime);
    if let Err(reason) = show_candidates(app, state, x, y) {
        if let Ok(mut runtime) = shared.0.lock() {
            runtime.manual.close();
            runtime.generation += 1;
            runtime.status = reason.into();
        }
        hide_candidates(app);
        crate::desktop::record_error(app, reason);
    }
}
pub fn candidate_action(
    app: &AppHandle,
    generation: u64,
    action: CandidateAction,
    index: Option<usize>,
) -> Result<(), &'static str> {
    let shared = app.state::<Shared>();
    let mut runtime = shared.0.lock().map_err(|_| "desktop-state-unavailable")?;
    if runtime.generation != generation || runtime.manual.selected().is_none() {
        return Err("candidate-stale");
    }
    let mut close = false;
    match action {
        CandidateAction::Next => runtime
            .manual
            .navigate(fixkey_core::manual::Direction::Next),
        CandidateAction::Previous => runtime
            .manual
            .navigate(fixkey_core::manual::Direction::Previous),
        CandidateAction::Close => {
            runtime.manual.close();
            runtime.generation += 1;
            close = true;
        }
        CandidateAction::Apply => {
            if let Some(index) = index {
                if index >= runtime.manual.candidates().len() {
                    return Err("candidate-index-invalid");
                }
                while runtime.manual.selected() != Some(index) {
                    runtime
                        .manual
                        .navigate(fixkey_core::manual::Direction::Next);
                }
            }
            let mut adapter = Adapter::new();
            let metadata = adapter.metadata().ok().filter(|(_, application)| {
                runtime.active && allowed(application, runtime.preferences.current())
            });
            let Some((target, application)) = metadata else {
                runtime.manual.close();
                runtime.generation += 1;
                drop(runtime);
                hide_candidates(app);
                return Err("candidate-target-unavailable");
            };
            adapter.set_application(&application.identity);
            let result = runtime
                .manual
                .apply(&mut adapter)
                .map_err(|_| "candidate-apply-failed");
            if result.is_err() {
                runtime.refused_target = Some((target, "candidate-apply-failed"));
                runtime.last_error = Some("candidate-apply-failed".into());
            }
            runtime.generation += 1;
            drop(runtime);
            hide_candidates(app);
            return result;
        }
    }
    runtime.candidate_revision += 1;
    let state = candidates(&runtime);
    drop(runtime);
    if close {
        hide_candidates(app);
    } else {
        let _ = app.emit_to("candidates", "fixkey-candidates", state);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::{
        cell::Cell,
        time::{Duration, Instant},
    };

    use fixkey_core::session::TargetId;

    use super::{ApplicationInventory, PendingKeys, insertion_ahead, snapshot_required};

    #[test]
    fn only_already_observed_keys_can_explain_a_published_insertion() {
        use fixkey_core::{
            observation::pending_matches,
            session::{FieldSensitivity, FieldSnapshot},
        };
        let before = FieldSnapshot {
            target: TargetId(1),
            text: "prefix  suffix".into(),
            caret: 7,
            selection_len: 0,
            sensitivity: FieldSensitivity::Normal,
            supports_write: true,
        };
        let published = FieldSnapshot {
            text: "prefix zl suffix".into(),
            caret: 9,
            ..before
        };
        assert!(insertion_ahead(&before, &published, &['z']));
        let observed_at = Instant::now();
        let now = observed_at + Duration::from_millis(1);
        let waiting = PendingKeys::new(now);
        assert!(waiting.accepts(observed_at, now + Duration::from_millis(1)));
        assert!(!waiting.accepts(
            now + Duration::from_millis(1),
            now + Duration::from_millis(2)
        ));
        assert!(!waiting.accepts(now, now + Duration::from_millis(101)));
        assert!(pending_matches(&before, &published, &['z', 'l']));
        for unsafe_snapshot in [
            FieldSnapshot {
                target: TargetId(2),
                ..published.clone()
            },
            FieldSnapshot {
                selection_len: 1,
                ..published.clone()
            },
            FieldSnapshot {
                sensitivity: FieldSensitivity::Protected,
                ..published.clone()
            },
            FieldSnapshot {
                supports_write: false,
                ..published.clone()
            },
            FieldSnapshot {
                text: "prefix zl changed".into(),
                ..published
            },
        ] {
            assert!(!insertion_ahead(&before, &unsafe_snapshot, &['z']));
        }
    }

    #[test]
    fn settings_polls_reuse_inventory_until_expiry_or_explicit_refresh() {
        let calls = Cell::new(0);
        let discover = || {
            calls.set(calls.get() + 1);
            Vec::new()
        };
        let mut inventory = ApplicationInventory::default();
        let now = Instant::now();
        inventory.get(now, false, discover);
        for seconds in 1..30 {
            inventory.get(now + Duration::from_secs(seconds), false, discover);
        }
        assert_eq!(calls.get(), 1);
        inventory.get(now + Duration::from_secs(30), false, discover);
        assert_eq!(calls.get(), 2);
        inventory.get(now + Duration::from_secs(31), true, discover);
        assert_eq!(calls.get(), 3);
        assert_eq!(super::unresolved_apps(&[]), Vec::<usize>::new());
    }

    #[test]
    fn idle_targets_do_not_request_text_but_events_and_pending_commits_do() {
        let first = Some(super::Cursor {
            target: TargetId(1),
            marker: [0, 0, 0],
        });
        let second = Some(super::Cursor {
            target: TargetId(2),
            marker: [0, 0, 0],
        });
        let moved = Some(super::Cursor {
            target: TargetId(1),
            marker: [5, 5, 5],
        });
        let selected = Some(super::Cursor {
            target: TargetId(1),
            marker: [0, 0, 5],
        });
        assert!(snapshot_required(false, false, first, moved));
        assert!(snapshot_required(false, false, first, selected));
        assert!(!snapshot_required(false, false, moved, moved));
        for _ in 0..1000 {
            assert!(!snapshot_required(false, false, first, first));
        }
        assert!(snapshot_required(true, false, first, first));
        assert!(snapshot_required(false, true, first, first));
        assert!(snapshot_required(false, false, first, second));
        assert!(snapshot_required(false, false, None, first));
    }
}

#[cfg(test)]
mod permission_tests {
    use super::macos_permission_reason;

    #[test]
    fn reports_only_the_missing_macos_permission() {
        assert_eq!(
            macos_permission_reason(false, false),
            Some("permission-missing")
        );
        assert_eq!(
            macos_permission_reason(false, true),
            Some("permission-missing")
        );
        assert_eq!(
            macos_permission_reason(true, false),
            Some("input-permission-missing")
        );
        assert_eq!(macos_permission_reason(true, true), None);
    }
}
