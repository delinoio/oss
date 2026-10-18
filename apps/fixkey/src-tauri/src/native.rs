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
    time::Duration,
};

use fixkey_core::{
    decision::{MissingValidator, Validators},
    observation::{can_start, completed_word, pending_matches},
    platform::{Adapter, Application, Identity, Key, KeyEvent},
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

pub fn available_apps() -> Vec<ExcludedApp> {
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
pub fn unresolved_apps(excluded: &[ExcludedApp]) -> Vec<usize> {
    let available = available_apps();
    excluded
        .iter()
        .enumerate()
        .filter_map(|(index, app)| {
            (!available.iter().any(|known| known.identity == app.identity)).then_some(index)
        })
        .collect()
}
pub fn open_permission_settings() -> Result<(), &'static str> {
    #[cfg(target_os = "macos")]
    let command = std::process::Command::new("open")
        .arg("x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility")
        .spawn();
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
    let mut baseline: Option<FieldSnapshot> = None;
    let mut context: Option<Context> = None;
    let mut revision = 0;
    loop {
        let event = receiver.recv_timeout(Duration::from_millis(12)).ok();
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
            context = None;
            baseline = None;
            while receiver.try_recv().is_ok() {}
            runtime.preferences.session().clear_transient();
        }
        let previous_error = runtime.last_error.clone();
        let metadata = adapter.metadata();
        let status = status(&runtime, &metadata, observer_ready.load(Ordering::Acquire));
        let enabled = status == "active";
        let changed = runtime.status != status;
        runtime.status = status.into();
        runtime.active = enabled;
        active.store(enabled, Ordering::Release);
        if let Ok(mut policy) = runtime.input_policy.write() {
            *policy = enabled.then(|| runtime.preferences.current().clone());
        }
        runtime
            .preferences
            .session()
            .set_permission_granted(enabled);
        let mut hide = false;
        if !enabled || lost.swap(false, Ordering::AcqRel) {
            adapter.clear();
            context = None;
            baseline = None;
            runtime.preferences.session().clear_transient();
            if runtime.manual.selected().is_some() {
                runtime.manual.close();
                runtime.generation += 1;
                hide = true;
            }
        }
        if enabled {
            if let Ok((_, application)) = &metadata {
                adapter.set_application(&application.identity);
            }
            let (snapshot, composing) = capture(
                &mut runtime,
                &mut adapter,
                metadata.as_ref().ok().map(|(target, _)| *target),
                &mut hide,
            );
            if let Some(event) = event {
                if metadata
                    .as_ref()
                    .map_or(true, |(target, _)| *target != event.target)
                    || (snapshot.is_none() && !composing)
                {
                    context = None;
                } else if runtime.manual.selected().is_none() {
                    process_key(
                        &mut runtime,
                        &mut adapter,
                        &mut context,
                        baseline.as_ref(),
                        event,
                        snapshot.as_ref(),
                    );
                }
            } else if context.is_none() {
                runtime
                    .preferences
                    .session()
                    .observe_after_backspace(&mut adapter);
            }
            if !composing {
                update_baseline(&mut adapter, &mut baseline, &mut context);
            }
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
    target: Option<fixkey_core::session::TargetId>,
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
            if reason != fixkey_core::SkipReason::Composing {
                runtime.refused_target = target.map(|target| (target, reason.as_str()));
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
    if let Some(pending) = context.as_ref()
        && next.as_ref().is_none_or(|snapshot| {
            baseline.as_ref().is_some_and(|previous| {
                previous.target == snapshot.target && snapshot.caret < previous.caret
            }) || !pending_matches(&pending.before, snapshot, &pending.keys)
        })
    {
        *context = None;
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
    } else if !runtime.dictionaries_ready() {
        "dictionary-unavailable"
    } else if !Adapter::permission() {
        "permission-missing"
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
                });
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
                    && let Ok(current) = adapter.snapshot()
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
        tracing::debug!(
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
