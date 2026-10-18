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
    Language,
    decision::{MissingValidator, Validators},
    layout::text_from_keys,
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

pub fn available_apps() -> Result<Vec<ExcludedApp>, &'static str> {
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
        Ok(apps)
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
        Ok(apps)
    }
}
pub fn unresolved_apps(excluded: &[ExcludedApp]) -> Vec<usize> {
    let available = available_apps().unwrap_or_default();
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
    let gate: fixkey_core::platform::Gate = Arc::new(move || {
        let settings = policy.read().ok().and_then(|policy| policy.clone());
        let Some(settings) = settings else {
            return false;
        };
        Adapter::new()
            .metadata()
            .is_ok_and(|(_, app)| allowed(&app, &settings))
    });
    let handler_lost = lost.clone();
    let handler: fixkey_core::platform::Handler = Arc::new(move |event| {
        if sender.try_send(event).is_err() {
            handler_lost.store(true, Ordering::Release);
        }
    });
    let observer_ready = Arc::new(AtomicBool::new(false));
    let thread_ready = observer_ready.clone();
    let observer_active = active.clone();
    std::thread::spawn(move || {
        loop {
            if Adapter::permission() {
                let _ = Adapter::observe(
                    observer_active.clone(),
                    thread_ready.clone(),
                    gate.clone(),
                    handler.clone(),
                );
            }
            std::thread::sleep(Duration::from_secs(1));
        }
    });
    std::thread::spawn(move || {
        let mut adapter = Adapter::new();
        let mut last: Option<FieldSnapshot> = None;
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
                break;
            }
            if revision != runtime.revision {
                revision = runtime.revision;
                context = None;
                last = None;
                while receiver.try_recv().is_ok() {}
                runtime.preferences.session().clear_transient();
            }
            let metadata = adapter.metadata();
            let status = if runtime.preferences.needs_reconciliation() {
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
            } else if !observer_ready.load(Ordering::Acquire) {
                "input-observer-unavailable"
            } else {
                match &metadata {
                    Ok((_, application))
                        if !allowed(application, runtime.preferences.current()) =>
                    {
                        "excluded-app"
                    }
                    Ok(_) => "active",
                    Err(reason) => reason.as_str(),
                }
            };
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
                context = None;
                last = None;
                runtime.preferences.session().clear_transient();
                if runtime.manual.selected().is_some() {
                    runtime.manual.close();
                    runtime.generation += 1;
                    hide = true;
                }
            }
            if enabled {
                let observed = adapter.snapshot();
                let composing = matches!(observed, Err(fixkey_core::SkipReason::Composing));
                let snapshot = observed.ok();
                if let Some(snapshot) = &snapshot {
                    runtime.preferences.session().observe(snapshot);
                    let was_open = runtime.manual.selected().is_some();
                    runtime.manual.observe(snapshot);
                    if was_open && runtime.manual.selected().is_none() {
                        runtime.generation += 1;
                        hide = true;
                    }
                }
                if let Some(event) = event {
                    if metadata
                        .as_ref()
                        .map_or(true, |(target, _)| *target != event.target)
                        || (snapshot.is_none() && !composing)
                    {
                        context = None;
                    } else if runtime.manual.selected().is_none() {
                        match event.key {
                            Key::Backspace => {
                                context = None;
                                runtime.preferences.session().on_backspace(&mut adapter);
                            }
                            Key::Reset | Key::Paste | Key::Untrusted => {
                                context = None;
                                runtime.preferences.session().clear_transient();
                            }
                            Key::Character(ch) => {
                                if context.is_none()
                                    && let Some(before) = &last
                                    && before.target == event.target
                                    && before.selection_len == 0
                                {
                                    runtime.preferences.session().next_word_started();
                                    context = Some(Context {
                                        before: before.clone(),
                                        tokenizer: Tokenizer::new(),
                                    });
                                }
                                if let Some(pending) = &mut context
                                    && let Some(outcome) = pending.tokenizer.feed_char(ch)
                                {
                                    if let TokenOutcome::Evaluate { word, boundary } = outcome {
                                        if let Ok(current) = adapter.snapshot() {
                                            evaluate(
                                                &mut runtime,
                                                &mut adapter,
                                                &pending.before,
                                                &current,
                                                &word,
                                                &boundary,
                                            );
                                        }
                                    }
                                    context = None;
                                }
                            }
                            Key::Enter | Key::Tab => {
                                if let Some(pending) = &mut context
                                    && let Some(TokenOutcome::Evaluate { word, boundary }) =
                                        pending.tokenizer.feed_enter_or_tab()
                                    && let Some(current) = &snapshot
                                {
                                    // Enter/Tab itself is never part of the
                                    // replacement. A committed host edit makes
                                    // this proof fail and discards held
                                    // punctuation.
                                    evaluate(
                                        &mut runtime,
                                        &mut adapter,
                                        &pending.before,
                                        current,
                                        &word,
                                        &boundary,
                                    );
                                }
                                context = None;
                            }
                        }
                    }
                } else if context.is_none() {
                    runtime
                        .preferences
                        .session()
                        .observe_after_backspace(&mut adapter);
                }
                if !composing {
                    last = adapter.snapshot().ok();
                }
            }
            drop(runtime);
            if hide {
                hide_candidates(&app);
            }
            if changed {
                if let Some(tray) = app.tray_by_id("fixkey") {
                    let _ = tray.set_tooltip(Some(format!("FixKey · {status}")));
                }
                crate::desktop::notify(&app, status);
            }
        }
    });
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
    for language in Language::ALL {
        let Some(word) = text_from_keys(&keys, language) else {
            continue;
        };
        let mut expected: Vec<char> = before.text.chars().collect();
        expected.splice(
            before.caret..before.caret,
            format!("{word}{boundary}").chars(),
        );
        let expected: String = expected.into_iter().collect();
        if current.target != before.target
            || current.selection_len != 0
            || current.text != expected
            || current.caret != before.caret + word.chars().count() + boundary.chars().count()
        {
            continue;
        }
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
        runtime.preferences.session().on_word_complete(
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
        break;
    }
}

pub fn shortcut(app: &AppHandle, key: Shortcut) {
    let shared = app.state::<Shared>();
    let picker = shared
        .0
        .lock()
        .ok()
        .and_then(|runtime| runtime.manual.selected().map(|_| runtime.generation));
    if let Some(generation) = picker {
        for (index, name) in PICKER_KEYS.iter().enumerate() {
            if name
                .parse::<Shortcut>()
                .is_ok_and(|candidate| candidate == key)
            {
                let action = match index {
                    0 => CandidateAction::Next,
                    1 => CandidateAction::Previous,
                    2 => CandidateAction::Apply,
                    _ => CandidateAction::Close,
                };
                let _ = candidate_action(app, generation, action, None);
                return;
            }
        }
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
    let Ok((x, y)) = adapter.location() else {
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
            if !runtime.active
                || !adapter
                    .metadata()
                    .is_ok_and(|(_, app)| allowed(&app, runtime.preferences.current()))
            {
                runtime.manual.close();
                runtime.generation += 1;
                drop(runtime);
                hide_candidates(app);
                return Err("candidate-target-unavailable");
            }
            let result = runtime
                .manual
                .apply(&mut adapter)
                .map_err(|_| "candidate-apply-failed");
            runtime.generation += 1;
            drop(runtime);
            hide_candidates(app);
            return result;
        }
    }
    let state = candidates(&runtime);
    drop(runtime);
    if close {
        hide_candidates(app);
    } else {
        let _ = app.emit_to("candidates", "fixkey-candidates", state);
    }
    Ok(())
}
