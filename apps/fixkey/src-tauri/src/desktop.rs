//! Tauri shell commands. Input and candidate state remain in Rust; only the
//! non-activating candidate window receives ephemeral candidate text.

use std::{
    sync::{Arc, Mutex, RwLock},
    time::Instant,
};

use fixkey_core::{
    english::HunspellValidator, korean::KoreanValidator, manual::ManualSession,
    mecab::MecabAnalyzer,
};
use serde::{Deserialize, Serialize};
pub use tauri::AppHandle;
use tauri::{
    Emitter, Manager, WebviewUrl, WebviewWindowBuilder,
    menu::{Menu, MenuItem},
    tray::TrayIconBuilder,
};
use tauri_plugin_autostart::ManagerExt as _;
use tauri_plugin_global_shortcut::{GlobalShortcutExt, Shortcut, ShortcutState};

use crate::{
    diagnostics::{Logs, platform},
    preferences::{IntegrationError, Preferences, SystemPreferences},
    settings::{ExcludedApp, Settings, SettingsStore, Theme, UiLanguage},
    updates::{
        UpdateState,
        desktop::{SharedUpdater, Updater},
    },
};

pub struct NativePreferences {
    app: AppHandle,
    shortcut: Option<String>,
    uncertain: bool,
}
impl SystemPreferences for NativePreferences {
    fn set_login_startup(&mut self, enabled: bool) -> Result<(), IntegrationError> {
        let manager = self.app.autolaunch();
        let previous = manager
            .is_enabled()
            .map_err(|_| IntegrationError::Uncertain)?;
        if previous == enabled {
            return Ok(());
        }
        let result = if enabled {
            manager.enable()
        } else {
            manager.disable()
        };
        let actual = manager.is_enabled().ok();
        if result.is_ok() && actual == Some(enabled) {
            return Ok(());
        }
        if actual != Some(previous) {
            let _ = if previous {
                manager.enable()
            } else {
                manager.disable()
            };
            if manager.is_enabled().ok() != Some(previous) {
                return Err(IntegrationError::Uncertain);
            }
        }
        Err(IntegrationError::LoginStartupFailed)
    }

    fn set_shortcut(&mut self, next: Option<&str>) -> Result<(), IntegrationError> {
        let manager = self.app.global_shortcut();
        if self.uncertain {
            manager
                .unregister_all()
                .map_err(|_| IntegrationError::Uncertain)?;
            self.shortcut = None;
            self.uncertain = false;
        }
        if self.shortcut.as_deref() == next {
            return Ok(());
        }
        if let Some(key) = next {
            let shortcut: Shortcut = key
                .parse()
                .map_err(|_| IntegrationError::ShortcutUnsupported)?;
            manager
                .register(shortcut)
                .map_err(|_| IntegrationError::ShortcutConflict)?;
        }
        if let Some(previous) = &self.shortcut
            && manager.unregister(previous.as_str()).is_err()
        {
            if let Some(key) = next {
                let _ = manager.unregister(key);
            }
            self.uncertain = true;
            return Err(IntegrationError::Uncertain);
        }
        self.shortcut = next.map(str::to_owned);
        Ok(())
    }
}

#[derive(Clone)]
pub struct Shared(pub Arc<Mutex<Runtime>>);
pub struct Runtime {
    pub(crate) preferences: Preferences<NativePreferences>,
    pub(crate) manual: ManualSession,
    pub(crate) generation: u64,
    pub(crate) candidate_revision: u64,
    pub(crate) korean: Option<KoreanValidator<MecabAnalyzer>>,
    pub(crate) english: Option<HunspellValidator>,
    pub(crate) status: String,
    pub(crate) active: bool,
    pub(crate) stopping: bool,
    pub(crate) last_error: Option<String>,
    pub(crate) refused_target: Option<(fixkey_core::session::TargetId, &'static str)>,
    pub(crate) revision: u64,
    pub(crate) input_policy: Arc<RwLock<Option<Settings>>>,
    logs: Logs,
    os_language: &'static str,
}
impl Runtime {
    pub fn stop_for_update(&mut self) {
        if let Ok(mut policy) = self.input_policy.write() {
            *policy = None;
        }
        self.stopping = true;
        self.active = false;
        self.preferences.session().set_permission_granted(false);
        self.manual.close();
        self.generation += 1;
        self.status = "update-installing".into();
    }

    pub(crate) const fn dictionaries_ready(&self) -> bool {
        let settings = self.preferences.current();
        (!settings.korean_enabled || self.korean.is_some())
            && (!settings.english_enabled || self.english.is_some())
    }
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct DesktopState {
    revision: u64,
    settings: Settings,
    version: &'static str,
    platform: &'static str,
    os_language: &'static str,
    status: String,
    active: bool,
    settings_error: Option<String>,
    logs_healthy: bool,
    diagnostic_error: Option<String>,
    unresolved_apps: Vec<usize>,
    update: UpdateState,
}

#[derive(Clone, Serialize, Default)]
#[serde(rename_all = "camelCase")]
pub(crate) struct CandidateState {
    pub generation: u64,
    pub revision: u64,
    pub selected: usize,
    pub items: Vec<Candidate>,
    pub language: Option<&'static str>,
    pub theme: Option<Theme>,
}
#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct Candidate {
    text: String,
    language: &'static str,
    is_original: bool,
    validation: &'static str,
}
pub(crate) fn candidates(runtime: &Runtime) -> CandidateState {
    CandidateState {
        generation: runtime.generation,
        revision: runtime.candidate_revision,
        language: Some(interface_language(runtime)),
        theme: Some(runtime.preferences.current().theme),
        selected: runtime.manual.selected().unwrap_or(0),
        items: runtime
            .manual
            .candidates()
            .iter()
            .map(|item| Candidate {
                text: item.text.clone(),
                language: item.language.as_str(),
                is_original: item.is_original,
                validation: item.validation.as_str(),
            })
            .collect(),
    }
}

#[tauri::command]
async fn desktop_state(
    shared: tauri::State<'_, Shared>,
    updater: tauri::State<'_, SharedUpdater>,
) -> Result<DesktopState, String> {
    let runtime = shared.0.lock().map_err(|_| "desktop-state-unavailable")?;
    let settings = runtime.preferences.current().clone();
    let state = DesktopState {
        revision: runtime.revision,
        settings,
        version: env!("CARGO_PKG_VERSION"),
        platform: platform(),
        os_language: runtime.os_language,
        status: runtime.status.clone(),
        active: runtime.active,
        settings_error: runtime
            .preferences
            .storage_error()
            .map(|error| error.to_string()),
        logs_healthy: runtime.logs.is_healthy(),
        diagnostic_error: runtime.last_error.clone(),
        unresolved_apps: Vec::new(),
        update: UpdateState::default(),
    };
    drop(runtime);
    let mut state = state;
    state.update = updater
        .lock()
        .map_err(|_| "update-state-unavailable")?
        .state
        .clone();
    state.unresolved_apps = crate::native::unresolved_apps(&state.settings.excluded_apps);
    Ok(state)
}

#[tauri::command]
async fn save_settings(
    settings: Settings,
    revision: u64,
    app: AppHandle,
    shared: tauri::State<'_, Shared>,
    updater: tauri::State<'_, SharedUpdater>,
) -> Result<DesktopState, String> {
    {
        let mut runtime = shared.0.lock().map_err(|_| "desktop-state-unavailable")?;
        if revision != runtime.revision {
            return Err("settings-stale".into());
        }
        if let Ok(mut policy) = runtime.input_policy.write() {
            *policy = None;
        }
        runtime
            .preferences
            .apply(settings)
            .map_err(|error| error.to_string())?;
        runtime.manual.close();
        runtime.generation += 1;
        runtime.revision += 1;
        runtime.refused_target = None;
        runtime.last_error = None;
        runtime.active = false;
        runtime.status = "rechecking".into();
    }
    hide_candidates(&app);
    refresh_tray(&app);
    desktop_state(shared, updater).await
}

#[derive(Deserialize)]
#[serde(rename_all = "kebab-case")]
enum Action {
    Recheck,
    PermissionSettings,
    RequestPermission,
    OpenLogs,
    DeleteLogs,
    Support,
    RestoreBackup,
    CheckUpdate,
    InstallUpdate,
}

#[tauri::command]
async fn desktop_action(
    action: Action,
    app: AppHandle,
    shared: tauri::State<'_, Shared>,
    updater: tauri::State<'_, SharedUpdater>,
) -> Result<(), String> {
    match action {
        Action::CheckUpdate => crate::updates::desktop::check(&app, &updater)
            .await
            .map_err(str::to_owned),
        Action::InstallUpdate => crate::updates::desktop::install(&app, &updater)
            .await
            .map_err(str::to_owned),
        Action::RequestPermission => crate::native::request_permission().map_err(str::to_owned),
        Action::PermissionSettings => {
            crate::native::open_permission_settings().map_err(str::to_owned)
        }
        Action::Support => open_resource("https://github.com/delinoio/oss/issues"),
        Action::OpenLogs => open_resource(
            app.path()
                .app_log_dir()
                .map_err(|_| "log-directory-unavailable")?
                .to_str()
                .ok_or("log-directory-unavailable")?,
        ),
        Action::DeleteLogs => shared
            .0
            .lock()
            .map_err(|_| "desktop-state-unavailable")?
            .logs
            .delete()
            .map_err(|_| "log-delete-failed".into()),
        Action::Recheck | Action::RestoreBackup => {
            let mut runtime = shared.0.lock().map_err(|_| "desktop-state-unavailable")?;
            runtime.manual.close();
            runtime.generation += 1;
            let result = if matches!(action, Action::RestoreBackup) {
                runtime.preferences.restore_backup()
            } else {
                runtime.preferences.reconcile()
            };
            runtime.revision += 1;
            runtime.refused_target = None;
            runtime.last_error = None;
            drop(runtime);
            hide_candidates(&app);
            refresh_tray(&app);
            result.map_err(|error| error.to_string())
        }
    }
}

#[tauri::command]
async fn available_apps() -> Vec<ExcludedApp> {
    crate::native::available_apps()
}

#[tauri::command]
async fn candidate_state(
    window: tauri::WebviewWindow,
    shared: tauri::State<'_, Shared>,
) -> Result<CandidateState, String> {
    if window.label() != "candidates" {
        return Err("candidate-window-required".into());
    }
    let runtime = shared.0.lock().map_err(|_| "desktop-state-unavailable")?;
    Ok(candidates(&runtime))
}

#[derive(Clone, Copy, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum CandidateAction {
    Next,
    Previous,
    Apply,
    Close,
}
#[tauri::command]
async fn candidate_action(
    generation: u64,
    action: CandidateAction,
    index: Option<usize>,
    window: tauri::WebviewWindow,
    app: AppHandle,
) -> Result<(), String> {
    if window.label() != "candidates" {
        return Err("candidate-window-required".into());
    }
    crate::native::candidate_action(&app, generation, action, index).map_err(str::to_owned)
}

pub(crate) const PICKER_KEYS: [&str; 4] = ["ArrowDown", "ArrowUp", "Enter", "Escape"];
pub(crate) fn hide_candidates(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("candidates") {
        let _ = window.hide();
    }
    for key in PICKER_KEYS {
        let _ = app.global_shortcut().unregister(key);
    }
    let generation = app
        .state::<Shared>()
        .0
        .lock()
        .map_or(0, |runtime| runtime.generation);
    let _ = app.emit_to(
        "candidates",
        "fixkey-candidates",
        CandidateState {
            generation,
            ..CandidateState::default()
        },
    );
}
pub(crate) fn show_candidates(
    app: &AppHandle,
    state: CandidateState,
    x: f64,
    y: f64,
) -> Result<(), &'static str> {
    let mut registered = Vec::new();
    for key in PICKER_KEYS {
        if app.global_shortcut().register(key).is_err() {
            for key in registered {
                let _ = app.global_shortcut().unregister(key);
            }
            return Err("candidate-key-registration-failed");
        }
        registered.push(key);
    }
    let window = app
        .get_webview_window("candidates")
        .ok_or("candidate-window-unavailable")?;
    // AX reports macOS screen points; Windows UIA and X11 use screen pixels.
    #[cfg(target_os = "macos")]
    let (x, y) = window
        .available_monitors()
        .map_err(|_| "candidate-location-unavailable")?
        .into_iter()
        .find_map(|monitor| {
            let scale = monitor.scale_factor();
            let position = monitor.position().to_logical::<f64>(scale);
            let size = monitor.size().to_logical::<f64>(scale);
            (x >= position.x
                && y >= position.y
                && x < position.x + size.width
                && y < position.y + size.height)
                .then_some((x * scale, y * scale))
        })
        .ok_or("candidate-location-unavailable")?;
    let monitor = window
        .monitor_from_point(x, y)
        .map_err(|_| "candidate-location-unavailable")?
        .ok_or("candidate-location-unavailable")?;
    let scale = monitor.scale_factor();
    let origin = monitor.work_area().position;
    let size = monitor.work_area().size;
    let width = (360.0 * scale).min(f64::from(size.width));
    let height = (240.0 * scale).min(f64::from(size.height));
    window
        .set_size(tauri::PhysicalSize::new(width, height))
        .map_err(|_| "candidate-location-unavailable")?;
    let left = x
        .max(f64::from(origin.x))
        .min(f64::from(origin.x) + f64::from(size.width) - width);
    let top = y
        .max(f64::from(origin.y))
        .min(f64::from(origin.y) + f64::from(size.height) - height);
    window
        .set_position(tauri::PhysicalPosition::new(left, top))
        .map_err(|_| "candidate-location-unavailable")?;
    window.show().map_err(|_| "candidate-window-unavailable")?;
    app.emit_to("candidates", "fixkey-candidates", state)
        .map_err(|_| "candidate-window-unavailable")
}

const fn interface_language(runtime: &Runtime) -> &'static str {
    match runtime.preferences.current().ui_language {
        UiLanguage::System => runtime.os_language,
        UiLanguage::Ko => "ko",
        UiLanguage::En => "en",
    }
}
struct TrayMenus {
    pause: MenuItem<tauri::DynRuntime>,
    settings: MenuItem<tauri::DynRuntime>,
    exit: MenuItem<tauri::DynRuntime>,
}
pub(crate) fn refresh_tray(app: &AppHandle) {
    let shared = app.state::<Shared>();
    let Ok(runtime) = shared.0.lock() else {
        return;
    };
    let korean = interface_language(&runtime) == "ko";
    let paused = runtime.preferences.current().paused;
    let status = runtime
        .last_error
        .clone()
        .unwrap_or_else(|| runtime.status.clone());
    drop(runtime);
    if let Some(menu) = app.try_state::<TrayMenus>() {
        let _ = menu.pause.set_text(match (korean, paused) {
            (true, true) => "교정 재개",
            (true, false) => "교정 일시 정지",
            (false, true) => "Resume correction",
            (false, false) => "Pause correction",
        });
        let _ = menu
            .settings
            .set_text(if korean { "설정" } else { "Settings" });
        let _ = menu.exit.set_text(if korean { "종료" } else { "Exit" });
    }
    if let Some(tray) = app.tray_by_id("fixkey") {
        let _ = tray.set_tooltip(Some(format!(
            "FixKey · {} · {status}",
            if korean {
                "교정 상태"
            } else {
                "Correction status"
            }
        )));
    }
}
pub(crate) fn record_error(app: &AppHandle, reason: &'static str) {
    let changed = app.state::<Shared>().0.lock().is_ok_and(|mut runtime| {
        let changed = runtime.last_error.as_deref() != Some(reason);
        runtime.last_error = Some(reason.into());
        changed
    });
    tracing::warn!(reason, "fixkey operation failed");
    refresh_tray(app);
    if changed {
        notify(app, reason);
    }
}
pub(crate) fn notify(app: &AppHandle, code: &str) {
    let korean = app
        .try_state::<Shared>()
        .and_then(|shared| {
            shared
                .0
                .lock()
                .ok()
                .map(|runtime| interface_language(&runtime) == "ko")
        })
        .unwrap_or(false);
    let message = match (korean, code) {
        (true, "active") => "자동 교정이 활성화되었습니다.",
        (false, "active") => "Automatic correction is active.",
        (true, "paused") => "자동 교정을 일시 정지했습니다.",
        (false, "paused") => "Automatic correction is paused.",
        (true, "update-available") => {
            "새 업데이트가 있습니다. 설정에서 확인하고 설치할 수 있습니다."
        }
        (false, "update-available") => "An update is available. Review and install it in Settings.",
        (true, _) => "FixKey 상태를 확인해 주세요. 설정에서 원인 코드를 확인할 수 있어요.",
        (false, _) => "FixKey needs attention. Check the reason code in Settings.",
    };
    if notify_rust::Notification::new()
        .summary("FixKey")
        .body(&format!("{message}\n{code}"))
        .appname("FixKey")
        .show()
        .is_err()
    {
        tracing::warn!(
            reason = "notification-failed",
            "fixkey status notification failed"
        );
    }
}
fn open_resource(value: &str) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    let result = std::process::Command::new("open").arg(value).spawn();
    #[cfg(target_os = "windows")]
    let result = std::process::Command::new("explorer.exe")
        .arg(value)
        .spawn();
    #[cfg(target_os = "linux")]
    let result = std::process::Command::new("xdg-open").arg(value).spawn();
    result
        .map(|_| ())
        .map_err(|_| "open-resource-failed".into())
}

fn setup(app: &mut tauri::App) -> Result<(), Box<dyn std::error::Error>> {
    let started = Instant::now();
    let logs = Logs::open(app.path().app_log_dir()?)?;
    logs.install()?;
    let dictionary = app.path().resource_dir()?.join("dictionaries/ko");
    let korean = MecabAnalyzer::load(&dictionary)
        .ok()
        .map(KoreanValidator::new);
    let english = HunspellValidator::bundled().ok();
    let store = SettingsStore::load(app.path().app_config_dir()?.join("settings.json"));
    let mut preferences = Preferences::new(
        store,
        NativePreferences {
            app: app.handle().clone(),
            shortcut: None,
            uncertain: false,
        },
    );
    let _ = preferences.reconcile();
    let os_language = crate::native::os_language();
    let language = match preferences.current().ui_language {
        crate::settings::UiLanguage::Ko => "ko",
        crate::settings::UiLanguage::En => "en",
        crate::settings::UiLanguage::System => os_language,
    };
    WebviewWindowBuilder::new(
        app,
        "candidates",
        WebviewUrl::App(format!("index.html?window=candidates&language={language}").into()),
    )
    .title("FixKey")
    .inner_size(360.0, 240.0)
    .visible(false)
    .focused(false)
    .focusable(false)
    .skip_taskbar(true)
    .always_on_top(true)
    .decorations(false)
    .build()?;
    let shared = Shared(Arc::new(Mutex::new(Runtime {
        preferences,
        manual: ManualSession::default(),
        generation: 1,
        candidate_revision: 0,
        korean,
        english,
        status: "initializing".into(),
        active: false,
        stopping: false,
        refused_target: None,
        last_error: None,
        revision: 1,
        input_policy: Arc::new(RwLock::new(None)),
        logs: logs.clone(),
        os_language,
    })));
    app.manage(shared.clone());
    let updater: SharedUpdater = Arc::new(Mutex::new(Updater::default()));
    app.manage(updater.clone());
    create_tray(app)?;
    refresh_tray(app.handle());
    crate::native::start(app.handle().clone(), shared);
    crate::updates::desktop::start(app.handle().clone(), updater);
    std::thread::spawn(move || {
        loop {
            std::thread::sleep(std::time::Duration::from_secs(60));
            let _ = logs.prune(std::time::SystemTime::now());
        }
    });
    tracing::info!(
        reason = "desktop-started",
        duration_us = u64::try_from(started.elapsed().as_micros()).unwrap_or(u64::MAX),
        "fixkey desktop initialized"
    );
    Ok(())
}

fn create_tray(app: &tauri::App) -> tauri::Result<()> {
    let pause = MenuItem::with_id(app, "pause", "Pause / Resume", true, None::<&str>)?;
    let settings = MenuItem::with_id(app, "settings", "Settings", true, None::<&str>)?;
    let exit = MenuItem::with_id(app, "exit", "Exit", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&pause, &settings, &exit])?;
    TrayIconBuilder::with_id("fixkey")
        .icon(tauri::include_image!("icons/32x32.png"))
        .menu(&menu)
        .tooltip("FixKey · initializing")
        .on_menu_event(|app, event| {
            let app = app.clone();
            let id = event.id().as_ref().to_owned();
            tauri::async_runtime::spawn(async move {
                match id.as_str() {
                    "settings" => {
                        if let Some(window) = app.get_webview_window("settings") {
                            let _ = window.show();
                            let _ = window.set_focus();
                        }
                    }
                    "pause" => {
                        if let Ok(mut runtime) = app.state::<Shared>().0.lock() {
                            if let Ok(mut policy) = runtime.input_policy.write() {
                                *policy = None;
                            }
                            let mut next = runtime.preferences.current().clone();
                            next.paused = !next.paused;
                            match runtime.preferences.apply(next) {
                                Ok(()) => {
                                    runtime.manual.close();
                                    runtime.generation += 1;
                                    runtime.revision += 1;
                                    runtime.last_error = None;
                                }
                                Err(error) => {
                                    runtime.last_error = Some(error.to_string());
                                    tracing::warn!(
                                        reason = "tray-preference-failed",
                                        "fixkey tray preference failed"
                                    );
                                }
                            }
                        }
                        hide_candidates(&app);
                        refresh_tray(&app);
                    }
                    "exit" => {
                        if let Ok(mut runtime) = app.state::<Shared>().0.lock() {
                            runtime.stop_for_update();
                        }
                        app.exit(0);
                    }
                    _ => {}
                }
            });
        })
        .build(app)?;
    app.manage(TrayMenus {
        pause,
        settings,
        exit,
    });
    Ok(())
}

// The pinned Tauri tracing wrapper emits an import after its Invoke binding.
// Limit the allowance to generated dispatch code; remove when the macro orders
// imports first. An expectation cannot cover both tracing feature states.
#[expect(
    clippy::allow_attributes,
    reason = "Tracing is unified only in some workspace builds"
)]
#[allow(
    clippy::items_after_statements,
    reason = "Pinned Tauri tracing command wrapper emits late imports"
)]
fn commands() -> impl Fn(tauri::ipc::Invoke) -> bool + Send + Sync + 'static {
    tauri::generate_handler![
        desktop_state,
        save_settings,
        desktop_action,
        available_apps,
        candidate_state,
        candidate_action
    ]
}

/// Starts the desktop event loop.
///
/// # Panics
/// Panics if native shell initialization fails before the event loop starts.
pub fn run() {
    let autostart = tauri_plugin_autostart::Builder::new();
    #[cfg(target_os = "macos")]
    let autostart = autostart.macos_launcher(tauri_plugin_autostart::MacosLauncher::LaunchAgent);

    tauri::Builder::default()
        .runtime(tauri_runtime_wry::Wry::default())
        .plugin(tauri_plugin_single_instance::init(|app, _, _| {
            if let Some(window) = app.get_webview_window("settings") {
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        .plugin(autostart.build())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(
            tauri_plugin_global_shortcut::Builder::new()
                .with_handler(|app, shortcut, event| {
                    if event.state() != ShortcutState::Pressed {
                        return;
                    }
                    crate::native::enqueue_shortcut(app, *shortcut);
                })
                .build(),
        )
        .setup(setup)
        .on_window_event(|window, event| {
            if window.label() == "settings"
                && let tauri::WindowEvent::CloseRequested { api, .. } = event
            {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .invoke_handler(commands())
        .build(tauri::generate_context!())
        .expect("fixkey desktop initialization failed")
        .run(|_, event| {
            if let tauri::RunEvent::ExitRequested { api, code, .. } = event
                && code.is_none()
            {
                api.prevent_exit();
            }
        });
}
