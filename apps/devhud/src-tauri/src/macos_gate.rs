use std::{
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread,
    time::{Duration, Instant},
};

use auto_launch::{AutoLaunch, AutoLaunchBuilder};
use global_hotkey::{
    GlobalHotKeyEvent, GlobalHotKeyManager, HotKeyState,
    hotkey::{Code, HotKey as Shortcut, Modifiers},
};
use tauri::{
    App, AppHandle, Manager, Theme, WebviewWindow, Window, menu::MenuBuilder, tray::TrayIconBuilder,
};

use super::{ActiveRuntime, PROBE_WINDOW_LABEL, ProbeCommandError, is_bundled_url};

const MENU_OPEN: &str = "open";
const MENU_SETTINGS: &str = "settings";
const MENU_CHECK_FOR_UPDATES: &str = "check-for-updates";
const MENU_OPEN_DEVTOOLS: &str = "open-devtools";
const MENU_QUIT: &str = "quit";
const SHORTCUT_EVENT_TIMEOUT: Duration = Duration::from_secs(20);
const SYSTEM_THEME_TIMEOUT: Duration = Duration::from_secs(20);
const STATE_SETTLE_DELAY: Duration = Duration::from_millis(250);

static DOCK_HIDDEN: AtomicBool = AtomicBool::new(false);
static SHORTCUT_EVENT_OBSERVED: AtomicBool = AtomicBool::new(false);
static SHORTCUT_REGISTERED: AtomicBool = AtomicBool::new(false);
static SYSTEM_THEME_CHANGED: AtomicBool = AtomicBool::new(false);
static TRAY_CREATED: AtomicBool = AtomicBool::new(false);

struct GateAutostart(Mutex<AutoLaunch>);

struct GateShortcutManager(Arc<GlobalHotKeyManager>);

// The upstream global-hotkey manager requires main-thread access on macOS.
// This wrapper follows that contract by creating/registering in setup and
// dispatching unregister through Tauri's main-thread runner.
unsafe impl Send for GateShortcutManager {}
unsafe impl Sync for GateShortcutManager {}

fn gate_shortcut() -> Shortcut {
    Shortcut::new(
        Some(Modifiers::CONTROL | Modifiers::ALT | Modifiers::SHIFT),
        Code::F18,
    )
}

fn toggle_probe_window(app: &AppHandle<ActiveRuntime>) -> Result<(), ProbeCommandError> {
    let window = app
        .get_webview_window(PROBE_WINDOW_LABEL)
        .ok_or(ProbeCommandError::GateWindowUnavailable)?;

    if window
        .is_visible()
        .map_err(|_| ProbeCommandError::GateWindowLifecycle)?
    {
        window
            .hide()
            .map_err(|_| ProbeCommandError::GateWindowLifecycle)?;
    } else {
        window
            .show()
            .and_then(|_| window.set_focus())
            .map_err(|_| ProbeCommandError::GateWindowLifecycle)?;
    }

    Ok(())
}

fn request_explicit_exit(app: &AppHandle<ActiveRuntime>) {
    tracing::info!(
        event = "devhud.probe.explicit_shutdown_requested",
        "explicit feasibility-probe shutdown requested"
    );
    app.exit(0);
}

pub(super) fn setup(app: &mut App<ActiveRuntime>) -> Result<(), Box<dyn std::error::Error>> {
    app.set_activation_policy(tauri::ActivationPolicy::Accessory);
    app.set_dock_visibility(false);
    DOCK_HIDDEN.store(true, Ordering::Release);

    let menu = MenuBuilder::new(app)
        .text(MENU_OPEN, "Open DevHud")
        .text(MENU_SETTINGS, "Settings")
        .text(MENU_CHECK_FOR_UPDATES, "Check for Updates")
        .text(MENU_OPEN_DEVTOOLS, "Open DevTools")
        .separator()
        .text(MENU_QUIT, "Quit")
        .build()?;

    let mut tray = TrayIconBuilder::with_id("devhud-feasibility-gate")
        .menu(&menu)
        .tooltip("DevHud feasibility gate")
        .icon_as_template(true)
        .on_menu_event(|app, event| match event.id().as_ref() {
            MENU_OPEN | MENU_SETTINGS => {
                if let Some(window) = app.get_webview_window(PROBE_WINDOW_LABEL) {
                    let _ = window.show();
                    let _ = window.set_focus();
                }
            }
            MENU_CHECK_FOR_UPDATES => {
                tracing::info!(
                    event = "devhud.probe.updater_menu_observed",
                    "updater menu action observed without a network request"
                );
            }
            MENU_OPEN_DEVTOOLS => {
                if let Some(window) = app.get_webview_window(PROBE_WINDOW_LABEL) {
                    window.open_devtools();
                }
            }
            MENU_QUIT => request_explicit_exit(app),
            _ => {}
        });

    if let Some(icon) = app.default_window_icon().cloned() {
        tray = tray.icon(icon);
    }
    tray.build(app)?;
    TRAY_CREATED.store(true, Ordering::Release);

    let shortcut = gate_shortcut();
    let shortcut_manager = Arc::new(GlobalHotKeyManager::new()?);
    shortcut_manager.register(shortcut)?;
    SHORTCUT_REGISTERED.store(true, Ordering::Release);
    let app_handle = app.handle().clone();
    GlobalHotKeyEvent::set_event_handler(Some(move |event: GlobalHotKeyEvent| {
        if event.id == shortcut.id() && event.state == HotKeyState::Pressed {
            if toggle_probe_window(&app_handle).is_ok() {
                SHORTCUT_EVENT_OBSERVED.store(true, Ordering::Release);
                tracing::info!(
                    event = "devhud.probe.global_shortcut_observed",
                    "registered shortcut toggled the probe window"
                );
            } else {
                tracing::error!(
                    event = "devhud.probe.global_shortcut_failure",
                    classification = "shortcut-registration",
                    "registered shortcut could not toggle the probe window"
                );
            }
        }
    }));
    app.manage(GateShortcutManager(shortcut_manager));

    let executable = std::env::current_exe()?;
    let executable = executable.to_string_lossy().into_owned();
    let mut autostart = AutoLaunchBuilder::new();
    autostart
        .set_app_name("DevHud Feasibility Probe")
        .set_app_path(&executable)
        .set_use_launch_agent(true);
    app.manage(GateAutostart(Mutex::new(autostart.build()?)));

    tracing::info!(
        event = "devhud.probe.macos_resident_shell_ready",
        "macOS menu-bar resident shell initialized"
    );
    Ok(())
}

pub(super) fn observe_window_event(event: &tauri::WindowEvent) {
    if matches!(event, tauri::WindowEvent::ThemeChanged(_)) {
        SYSTEM_THEME_CHANGED.store(true, Ordering::Release);
    }
}

pub(super) fn prevent_probe_window_close(
    window: &Window<ActiveRuntime>,
    event: &tauri::WindowEvent,
) {
    if window.label() != PROBE_WINDOW_LABEL {
        return;
    }

    if let tauri::WindowEvent::CloseRequested { api, .. } = event {
        api.prevent_close();
        let _ = window.hide();
        tracing::info!(
            event = "devhud.probe.window_close_hidden",
            "probe window close request preserved the resident process"
        );
    }
}

fn require(condition: bool, error: ProbeCommandError) -> Result<(), ProbeCommandError> {
    condition.then_some(()).ok_or(error)
}

fn exercise_registered_shortcut(
    window: &WebviewWindow<ActiveRuntime>,
    expected_visibility: bool,
) -> Result<(), ProbeCommandError> {
    SHORTCUT_EVENT_OBSERVED.store(false, Ordering::Release);
    tracing::info!(
        event = "devhud.probe.global_shortcut_ready",
        "probe is ready to observe the registered shortcut"
    );

    let deadline = Instant::now() + SHORTCUT_EVENT_TIMEOUT;
    while !SHORTCUT_EVENT_OBSERVED.load(Ordering::Acquire) && Instant::now() < deadline {
        thread::sleep(Duration::from_millis(100));
    }
    require(
        SHORTCUT_EVENT_OBSERVED.load(Ordering::Acquire),
        ProbeCommandError::GateShortcut,
    )?;
    require(
        window
            .is_visible()
            .map_err(|_| ProbeCommandError::GateShortcut)?
            == expected_visibility,
        ProbeCommandError::GateShortcut,
    )
}

fn run_window_lifecycle(window: &WebviewWindow<ActiveRuntime>) -> Result<(), ProbeCommandError> {
    window
        .close()
        .map_err(|_| ProbeCommandError::GateWindowLifecycle)?;
    thread::sleep(STATE_SETTLE_DELAY);
    require(
        !window
            .is_visible()
            .map_err(|_| ProbeCommandError::GateWindowLifecycle)?,
        ProbeCommandError::GateWindowLifecycle,
    )?;
    require(
        DOCK_HIDDEN.load(Ordering::Acquire),
        ProbeCommandError::GateDockPolicy,
    )?;

    window
        .show()
        .map_err(|_| ProbeCommandError::GateWindowLifecycle)?;
    exercise_registered_shortcut(window, false)?;
    exercise_registered_shortcut(window, true)
}

fn run_autostart_checks(autolaunch: &AutoLaunch) -> Result<(), ProbeCommandError> {
    require(
        !autolaunch
            .is_enabled()
            .map_err(|_| ProbeCommandError::GateAutostart)?,
        ProbeCommandError::GateAutostart,
    )?;
    autolaunch
        .enable()
        .map_err(|_| ProbeCommandError::GateAutostart)?;

    let enabled_result = autolaunch
        .is_enabled()
        .map_err(|_| ProbeCommandError::GateAutostart)
        .and_then(|enabled| require(enabled, ProbeCommandError::GateAutostart));
    let disable_result = autolaunch
        .disable()
        .map_err(|_| ProbeCommandError::GateAutostart);

    enabled_result?;
    disable_result?;
    require(
        !autolaunch
            .is_enabled()
            .map_err(|_| ProbeCommandError::GateAutostart)?,
        ProbeCommandError::GateAutostart,
    )
}

fn run_theme_checks(window: &WebviewWindow<ActiveRuntime>) -> Result<(), ProbeCommandError> {
    SYSTEM_THEME_CHANGED.store(false, Ordering::Release);
    window
        .set_theme(None)
        .map_err(|_| ProbeCommandError::GateTheme)?;
    tracing::info!(
        event = "devhud.probe.system_theme_change_ready",
        "probe is ready to observe a system appearance change"
    );

    let deadline = Instant::now() + SYSTEM_THEME_TIMEOUT;
    while !SYSTEM_THEME_CHANGED.load(Ordering::Acquire) && Instant::now() < deadline {
        thread::sleep(Duration::from_millis(100));
    }
    require(
        SYSTEM_THEME_CHANGED.load(Ordering::Acquire),
        ProbeCommandError::GateTheme,
    )?;

    window
        .set_theme(Some(Theme::Light))
        .map_err(|_| ProbeCommandError::GateTheme)?;
    thread::sleep(STATE_SETTLE_DELAY);
    require(
        window.theme().map_err(|_| ProbeCommandError::GateTheme)? == Theme::Light,
        ProbeCommandError::GateTheme,
    )?;

    window
        .set_theme(Some(Theme::Dark))
        .map_err(|_| ProbeCommandError::GateTheme)?;
    thread::sleep(STATE_SETTLE_DELAY);
    require(
        window.theme().map_err(|_| ProbeCommandError::GateTheme)? == Theme::Dark,
        ProbeCommandError::GateTheme,
    )?;
    window
        .set_theme(None)
        .map_err(|_| ProbeCommandError::GateTheme)
}

fn run_devtools_checks(window: &WebviewWindow<ActiveRuntime>) -> Result<(), ProbeCommandError> {
    window.open_devtools();
    thread::sleep(STATE_SETTLE_DELAY);
    require(window.is_devtools_open(), ProbeCommandError::GateDevTools)?;
    window
        .eval("window.location.href = 'https://example.invalid/devhud-gate'")
        .map_err(|_| ProbeCommandError::GateDevTools)?;
    thread::sleep(STATE_SETTLE_DELAY);
    let current_url = window.url().map_err(|_| ProbeCommandError::GateDevTools)?;
    require(
        is_bundled_url(&current_url),
        ProbeCommandError::GateDevTools,
    )?;
    window.close_devtools();
    Ok(())
}

pub(super) fn run(app: AppHandle<ActiveRuntime>) -> Result<(), ProbeCommandError> {
    let window = app
        .get_webview_window(PROBE_WINDOW_LABEL)
        .ok_or(ProbeCommandError::GateWindowUnavailable)?;

    require(
        TRAY_CREATED.load(Ordering::Acquire),
        ProbeCommandError::GateTray,
    )?;
    require(
        DOCK_HIDDEN.load(Ordering::Acquire),
        ProbeCommandError::GateDockPolicy,
    )?;

    require(
        SHORTCUT_REGISTERED.load(Ordering::Acquire),
        ProbeCommandError::GateShortcut,
    )?;

    let autolaunch = app.state::<GateAutostart>();
    let autolaunch = autolaunch
        .0
        .lock()
        .map_err(|_| ProbeCommandError::GateAutostart)?;
    run_autostart_checks(&autolaunch)?;

    run_window_lifecycle(&window)?;
    run_theme_checks(&window)?;
    run_devtools_checks(&window)?;

    tracing::info!(
        event = "devhud.probe.macos_gate_conditions_passed",
        "macOS runtime gate conditions passed"
    );
    Ok(())
}

fn cleanup(app: &AppHandle<ActiveRuntime>) -> Result<(), ProbeCommandError> {
    let shortcut_result = if SHORTCUT_REGISTERED.load(Ordering::Acquire) {
        let shortcut = gate_shortcut();
        let shortcut_manager = app.state::<GateShortcutManager>().0.clone();
        let (sender, receiver) = std::sync::mpsc::channel();
        app.run_on_main_thread(move || {
            let _ = sender.send(shortcut_manager.unregister(shortcut));
        })
        .map_err(|_| ProbeCommandError::GateShortcut)
        .and_then(|()| {
            receiver
                .recv()
                .map_err(|_| ProbeCommandError::GateShortcut)?
                .map_err(|_| ProbeCommandError::GateShortcut)
        })
        .inspect(|()| SHORTCUT_REGISTERED.store(false, Ordering::Release))
    } else {
        Ok(())
    };

    let autostart_result = app
        .state::<GateAutostart>()
        .0
        .lock()
        .map_err(|_| ProbeCommandError::GateAutostart)
        .and_then(|autolaunch| {
            autolaunch
                .disable()
                .map_err(|_| ProbeCommandError::GateAutostart)
        });

    shortcut_result?;
    autostart_result
}

pub(super) fn complete(app: AppHandle<ActiveRuntime>) -> Result<(), ProbeCommandError> {
    cleanup(&app)?;
    thread::spawn(move || {
        thread::sleep(Duration::from_millis(150));
        request_explicit_exit(&app);
    });
    Ok(())
}

pub(super) fn fail(app: AppHandle<ActiveRuntime>) {
    let _ = cleanup(&app);
    thread::spawn(move || {
        thread::sleep(Duration::from_millis(150));
        app.exit(72);
    });
}
