#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod bridge;
mod native_plugin;
mod platform;
mod resources;

#[cfg(any(target_os = "macos", target_os = "windows"))]
use std::process::{Child, Command};
#[cfg(target_os = "linux")]
use std::sync::mpsc;
use std::{
    net::IpAddr,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

#[cfg(target_os = "linux")]
use gtk::prelude::CancellableExt;
#[cfg(target_os = "linux")]
use gtk::{gio, glib};
use platform::DesktopTarget;
#[cfg(target_os = "linux")]
use platform::LinuxDisplayMode;
use resources::ResourceLayout;
use tauri::{
    Manager, WebviewUrl,
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
};
use tracing::{error, info, warn};
use tracing_subscriber::{
    Layer,
    filter::{FilterExt, filter_fn},
    layer::SubscriberExt,
    util::SubscriberInitExt,
};
#[cfg(any(target_os = "macos", target_os = "windows"))]
use wait_timeout::ChildExt;

const DEVELOPMENT_ORIGIN: &str = "http://127.0.0.1:46305";
const PRODUCTION_ORIGIN: &str = "http://tauri.localhost";
const FRONTEND_READY_TIMEOUT: Duration = Duration::from_secs(5);
const EXTERNAL_OPENER_TIMEOUT: Duration = Duration::from_secs(5);
const RENDERER_CRASH_LISTENER_READY_ATTEMPTS: usize = 100;
const RENDERER_CRASH_LISTENER_READY_DELAY: Duration = Duration::from_millis(50);
const DIAGNOSTIC_LOG_FILE_LIMIT: usize = 7;

#[derive(Clone)]
struct TrayMenuItems {
    show: MenuItem<tauri::Cef>,
    quit: MenuItem<tauri::Cef>,
}

fn tray_labels(language: &str) -> (&'static str, &'static str) {
    match language {
        "ko" => ("DevHUD 표시", "DevHUD 종료"),
        _ => ("Show DevHUD", "Quit DevHUD"),
    }
}

#[tauri::command]
fn set_tray_language(
    language: String,
    tray: tauri::State<'_, TrayMenuItems>,
) -> Result<(), String> {
    let (show, quit) = tray_labels(&language);
    tray.show.set_text(show).map_err(|error| {
        error!(event = "tray_language_update_failed", menu_item = "show", error = %error);
        "unable to update tray menu".to_string()
    })?;
    tray.quit.set_text(quit).map_err(|error| {
        error!(event = "tray_language_update_failed", menu_item = "quit", error = %error);
        "unable to update tray menu".to_string()
    })
}

fn validated_api_origin(origin: &str) -> Option<String> {
    let url = tauri::Url::parse(origin).ok()?;
    let loopback = url.host_str().is_some_and(|host| {
        host == "localhost"
            || host
                .trim_matches(['[', ']'])
                .parse::<IpAddr>()
                .is_ok_and(|ip| ip.is_loopback())
    });
    if url.username().is_empty()
        && url.password().is_none()
        && url.query().is_none()
        && url.fragment().is_none()
        && url.path() == "/"
        && (url.scheme() == "https" || (url.scheme() == "http" && loopback))
    {
        Some(url.to_string())
    } else {
        None
    }
}

fn external_destination(target: &str, api_origin: &str) -> Option<String> {
    match target {
        "authentication" => validated_api_origin(api_origin),
        "pat" => Some("https://github.com/settings/personal-access-tokens/new".to_string()),
        "issue" => Some("https://github.com/delinoio/oss/issues/new".to_string()),
        _ => None,
    }
}

#[cfg(any(target_os = "macos", target_os = "windows"))]
fn confirm_external_opener_status(status: std::process::ExitStatus) -> Result<(), String> {
    if status.success() {
        Ok(())
    } else {
        error!(event = "external_opener_failed", code = ?status.code());
        Err("system browser opener failed".to_string())
    }
}

#[cfg(any(target_os = "macos", target_os = "windows"))]
fn external_opener_command(destination: &str) -> Command {
    #[cfg(target_os = "macos")]
    let mut command = Command::new("open");
    #[cfg(target_os = "windows")]
    let mut command = Command::new("explorer.exe");
    command.arg(destination);
    command
}

#[cfg(target_os = "linux")]
fn confirm_linux_external_dispatch(
    result: Result<(), glib::Error>,
    cancellable: &gio::Cancellable,
) -> Result<(), String> {
    if cancellable.is_cancelled() {
        return Err("system browser opener timed out".to_string());
    }
    result.map_err(|reason| {
        error!(event = "external_opener_failed", %reason);
        "system browser opener failed".to_string()
    })
}

#[cfg(target_os = "linux")]
fn wait_for_linux_external_dispatch(destination: String) -> Result<(), String> {
    let (result_sender, result_receiver) = mpsc::sync_channel(1);
    let (loop_sender, loop_receiver) = mpsc::sync_channel(1);
    let cancellable = gio::Cancellable::new();
    let worker_cancellable = cancellable.clone();
    std::thread::spawn(move || {
        let context = glib::MainContext::new();
        let main_loop = glib::MainLoop::new(Some(&context), false);
        let _ = loop_sender.send(main_loop.clone());
        if context
            .with_thread_default(|| {
                let completion_loop = main_loop.clone();
                let completion_cancellable = worker_cancellable.clone();
                gio::AppInfo::launch_default_for_uri_async(
                    &destination,
                    None::<&gio::AppLaunchContext>,
                    Some(&worker_cancellable),
                    move |result| {
                        let _ = result_sender.send(confirm_linux_external_dispatch(
                            result,
                            &completion_cancellable,
                        ));
                        completion_loop.quit();
                    },
                );
                main_loop.run();
            })
            .is_err()
        {
            error!(event = "external_opener_worker_failed");
        }
    });
    let main_loop = loop_receiver.recv().map_err(|_| {
        error!(event = "external_opener_worker_failed");
        "unable to confirm system browser opener".to_string()
    })?;
    await_linux_external_dispatch(result_receiver, &cancellable, &main_loop)
}

#[cfg(target_os = "linux")]
fn cancel_linux_external_dispatch(cancellable: &gio::Cancellable, main_loop: &glib::MainLoop) {
    cancellable.cancel();
    main_loop.quit();
}

#[cfg(target_os = "linux")]
fn await_linux_external_dispatch(
    receiver: mpsc::Receiver<Result<(), String>>,
    cancellable: &gio::Cancellable,
    main_loop: &glib::MainLoop,
) -> Result<(), String> {
    match receiver.recv_timeout(EXTERNAL_OPENER_TIMEOUT) {
        Ok(result) => result,
        Err(mpsc::RecvTimeoutError::Timeout) => {
            error!(event = "external_opener_timeout");
            cancel_linux_external_dispatch(cancellable, main_loop);
            Err("system browser opener timed out".to_string())
        }
        Err(mpsc::RecvTimeoutError::Disconnected) => {
            error!(event = "external_opener_worker_failed");
            Err("unable to confirm system browser opener".to_string())
        }
    }
}

#[cfg(any(target_os = "macos", target_os = "windows"))]
fn wait_for_external_opener(mut child: Child) -> Result<(), String> {
    match child.wait_timeout(EXTERNAL_OPENER_TIMEOUT) {
        Ok(Some(status)) => confirm_external_opener_status(status),
        Ok(None) => {
            error!(event = "external_opener_timeout");
            if let Err(reason) = child.kill() {
                error!(event = "external_opener_kill_failed", %reason);
            }
            if let Err(reason) = child.wait() {
                error!(event = "external_opener_reap_failed", %reason);
            }
            Err("system browser opener timed out".to_string())
        }
        Err(reason) => {
            error!(event = "external_opener_wait_failed", %reason);
            Err("unable to confirm system browser opener".to_string())
        }
    }
}

#[tauri::command]
async fn open_external(target: String, api_origin: String) -> Result<(), String> {
    let destination = external_destination(&target, &api_origin).ok_or_else(|| {
        error!(event = "external_destination_rejected");
        "external destination is not allowlisted".to_string()
    })?;
    #[cfg(target_os = "linux")]
    return tauri::async_runtime::spawn_blocking(move || {
        wait_for_linux_external_dispatch(destination)
    })
    .await
    .map_err(|reason| {
        error!(event = "external_opener_worker_failed", %reason);
        "unable to confirm system browser opener".to_string()
    })?;

    #[cfg(any(target_os = "macos", target_os = "windows"))]
    let child = external_opener_command(&destination)
        .spawn()
        .map_err(|reason| {
            error!(event = "external_opener_spawn_failed", %reason);
            "unable to open system browser".to_string()
        })?;
    #[cfg(any(target_os = "macos", target_os = "windows"))]
    tauri::async_runtime::spawn_blocking(move || wait_for_external_opener(child))
        .await
        .map_err(|reason| {
            error!(event = "external_opener_worker_failed", %reason);
            "unable to confirm system browser opener".to_string()
        })?
}

fn restore_main_window(app: &tauri::AppHandle<tauri::Cef>) {
    let Some(window) = app.get_webview_window("main") else {
        error!(event = "tray_window_restore_missing");
        return;
    };
    if let Err(reason) = window.unminimize() {
        error!(event = "tray_window_restore_unminimize_failed", %reason);
    }
    if let Err(reason) = window.show() {
        error!(event = "tray_window_restore_show_failed", %reason);
    }
    if let Err(reason) = window.set_focus() {
        error!(event = "tray_window_restore_focus_failed", %reason);
    }
}

fn create_tray(app: &tauri::AppHandle<tauri::Cef>) -> tauri::Result<()> {
    let show = MenuItem::with_id(app, "show", "Show DevHUD", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "Quit DevHUD", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&show, &quit])?;
    app.manage(TrayMenuItems {
        show: show.clone(),
        quit: quit.clone(),
    });
    TrayIconBuilder::with_id("devhud")
        .tooltip("DevHUD")
        .icon(
            app.default_window_icon()
                .expect("DevHUD bundle icon")
                .clone(),
        )
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, event| match event.id.as_ref() {
            "show" => restore_main_window(app),
            "quit" => app.exit(0),
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                restore_main_window(tray.app_handle());
            }
        })
        .build(app)?;
    Ok(())
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum SmokeMode {
    Normal,
    RendererCrash,
    MissingResource,
}

#[derive(Clone)]
struct FrontendReadiness {
    complete: Arc<AtomicBool>,
    smoke_mode: Option<SmokeMode>,
    renderer_crashed: Arc<AtomicBool>,
    renderer_crash_listener_ready: Arc<AtomicBool>,
}

impl SmokeMode {
    fn from_environment() -> Option<Self> {
        match std::env::var("DEVHUD_PLATFORM_SMOKE").ok().as_deref() {
            Some("normal") => Some(Self::Normal),
            Some("renderer-crash") => Some(Self::RendererCrash),
            Some("missing-resource") => Some(Self::MissingResource),
            _ => None,
        }
    }
}

fn inject_smoke_missing_resource(missing: &mut Vec<String>, smoke_mode: Option<SmokeMode>) {
    if smoke_mode == Some(SmokeMode::MissingResource)
        && !missing
            .iter()
            .any(|resource| resource.ends_with("resources.pak"))
    {
        missing.push("resources.pak".to_string());
    }
}

fn is_cef_subprocess() -> bool {
    std::env::args().any(|argument| argument.starts_with("--type="))
}

#[cfg(target_os = "windows")]
fn platform_log_directory() -> Option<std::path::PathBuf> {
    dirs::data_local_dir().map(|directory| directory.join("io.delino.devhud").join("logs"))
}

#[cfg(target_os = "macos")]
fn platform_log_directory() -> Option<std::path::PathBuf> {
    dirs::home_dir().map(|directory| directory.join("Library/Logs/io.delino.devhud"))
}

#[cfg(target_os = "linux")]
fn platform_log_directory() -> Option<std::path::PathBuf> {
    dirs::state_dir().map(|directory| directory.join("io.delino.devhud").join("logs"))
}

#[cfg(not(any(target_os = "windows", target_os = "macos", target_os = "linux")))]
fn platform_log_directory() -> Option<std::path::PathBuf> {
    None
}

fn diagnostic_log_directory(smoke_mode: Option<SmokeMode>) -> Option<std::path::PathBuf> {
    if smoke_mode.is_some()
        && let Some(directory) = std::env::var_os("DEVHUD_SMOKE_LOG_DIR")
    {
        return Some(directory.into());
    }

    platform_log_directory()
}

fn diagnostic_writer(
    smoke_mode: Option<SmokeMode>,
    subprocess: bool,
) -> tracing_subscriber::fmt::writer::BoxMakeWriter {
    use tracing_appender::rolling::{RollingFileAppender, Rotation};
    use tracing_subscriber::fmt::writer::MakeWriterExt;

    if cfg!(debug_assertions) || subprocess {
        return tracing_subscriber::fmt::writer::BoxMakeWriter::new(std::io::stderr);
    }

    let result = (|| {
        let directory = diagnostic_log_directory(smoke_mode).ok_or(())?;
        std::fs::create_dir_all(&directory).map_err(|_| ())?;
        RollingFileAppender::builder()
            .rotation(Rotation::DAILY)
            .filename_prefix("devhud")
            .filename_suffix("jsonl")
            .max_log_files(DIAGNOSTIC_LOG_FILE_LIMIT)
            .build(directory)
            .map_err(|_| ())
    })();

    match result {
        Ok(appender) => {
            tracing_subscriber::fmt::writer::BoxMakeWriter::new(std::io::stderr.and(appender))
        }
        Err(()) => {
            eprintln!("{{\"level\":\"WARN\",\"event\":\"file_logging_unavailable\"}}");
            tracing_subscriber::fmt::writer::BoxMakeWriter::new(std::io::stderr)
        }
    }
}

fn diagnostic_filter(
    rust_log: Option<&str>,
) -> impl tracing_subscriber::layer::Filter<tracing_subscriber::Registry> + use<> {
    let filter = rust_log
        .and_then(|value| tracing_subscriber::EnvFilter::try_new(value).ok())
        .unwrap_or_else(|| tracing_subscriber::EnvFilter::new("info"));

    // Operator filters may tune dependency verbosity, but fatal application
    // diagnostics must remain available when a packaged GUI cannot start.
    filter.or(filter_fn(|metadata| {
        metadata.level() == &tracing::Level::ERROR
            && (metadata.target() == "devhud" || metadata.target().starts_with("devhud::"))
    }))
}

fn init_logging(smoke_mode: Option<SmokeMode>, subprocess: bool) {
    let rust_log = std::env::var("RUST_LOG").ok();
    let filter = diagnostic_filter(rust_log.as_deref());

    let writer = diagnostic_writer(smoke_mode, subprocess);

    let layer = tracing_subscriber::fmt::layer()
        .json()
        .with_writer(writer)
        .with_target(false)
        .with_filter(filter);
    let _ = tracing_subscriber::registry().with(layer).try_init();
}

fn is_allowed_navigation(url: &tauri::Url) -> bool {
    let origin = url.origin().ascii_serialization();
    origin == PRODUCTION_ORIGIN || (tauri::is_dev() && origin == DEVELOPMENT_ORIGIN)
}

#[tauri::command]
fn frontend_ready(
    webview: tauri::WebviewWindow<tauri::Cef>,
    readiness: tauri::State<'_, FrontendReadiness>,
) {
    if readiness.complete.swap(true, Ordering::SeqCst) {
        return;
    }

    let origin = if tauri::is_dev() {
        DEVELOPMENT_ORIGIN
    } else {
        PRODUCTION_ORIGIN
    };
    let app_handle = webview.app_handle();
    handle_frontend_ready(
        &webview,
        app_handle,
        readiness.smoke_mode,
        origin,
        readiness.renderer_crashed.clone(),
        readiness.renderer_crash_listener_ready.clone(),
    );
}

#[cfg(not(target_os = "macos"))]
fn should_observe_renderer_crashes(development: bool) -> bool {
    !development
}

fn validate_host(smoke_mode: Option<SmokeMode>) -> Result<(), String> {
    let target = DesktopTarget::current();
    info!(event = "platform_detected", target = %target);

    #[cfg(target_os = "linux")]
    match platform::validate_current_environment().map_err(|error| error.to_string())? {
        LinuxDisplayMode::X11 => info!(event = "linux_display_ready", mode = "x11"),
        LinuxDisplayMode::XWayland => warn!(
            event = "linux_display_best_effort",
            mode = "xwayland",
            "XWayland operation is best effort"
        ),
    }

    if tauri::is_dev() {
        return Ok(());
    }

    let executable = std::env::current_exe()
        .map_err(|error| format!("failed to resolve the current executable: {error}"))?;
    let layout = ResourceLayout::for_executable(&executable)?;
    let mut missing = layout.missing();
    inject_smoke_missing_resource(&mut missing, smoke_mode);
    if !missing.is_empty() {
        return Err(format!(
            "CEF initialization cannot continue; missing bundled resources: {}",
            missing.join(", ")
        ));
    }
    info!(
        event = "cef_resources_verified",
        count = layout.required_relative_paths().count(),
        sandbox = true
    );
    Ok(())
}

fn start_renderer_crash_watchdog(
    app_handle: tauri::AppHandle<tauri::Cef>,
    renderer_crashed: Arc<AtomicBool>,
) {
    std::thread::spawn(move || {
        for _ in 0..100 {
            if renderer_crashed.load(Ordering::SeqCst) {
                std::thread::sleep(Duration::from_millis(100));
                app_handle.exit(0);
                return;
            }
            std::thread::sleep(Duration::from_millis(50));
        }
        error!(event = "renderer_diagnostic_timeout");
        app_handle.exit(70);
    });
}

fn wait_for_renderer_crash_listener(
    listener_ready: &AtomicBool,
    attempts: usize,
    retry_delay: Duration,
) -> bool {
    for _ in 0..attempts {
        if listener_ready.load(Ordering::SeqCst) {
            return true;
        }
        std::thread::sleep(retry_delay);
    }
    false
}

fn wait_for_frontend_readiness_timeout(
    frontend_readiness_complete: &AtomicBool,
    timeout: Duration,
) -> bool {
    std::thread::sleep(timeout);
    !frontend_readiness_complete.swap(true, Ordering::SeqCst)
}

fn start_frontend_readiness_watchdog(
    app_handle: tauri::AppHandle<tauri::Cef>,
    frontend_readiness_complete: Arc<AtomicBool>,
) {
    std::thread::spawn(move || {
        if wait_for_frontend_readiness_timeout(&frontend_readiness_complete, FRONTEND_READY_TIMEOUT)
        {
            error!(event = "frontend_readiness_timeout");
            app_handle.exit(1);
        }
    });
}

fn handle_frontend_ready(
    webview: &tauri::WebviewWindow<tauri::Cef>,
    app_handle: &tauri::AppHandle<tauri::Cef>,
    smoke_mode: Option<SmokeMode>,
    origin: &str,
    renderer_crashed: Arc<AtomicBool>,
    renderer_crash_listener_ready: Arc<AtomicBool>,
) {
    info!(event = "frontend_ready", origin);
    match smoke_mode {
        Some(SmokeMode::Normal) => {
            let app_handle = app_handle.clone();
            std::thread::spawn(move || {
                std::thread::sleep(Duration::from_millis(250));
                info!(event = "smoke_shutdown_requested");
                app_handle.exit(0);
            });
        }
        Some(SmokeMode::RendererCrash) => {
            let webview = webview.clone();
            let app_handle = app_handle.clone();
            std::thread::spawn(move || {
                if !wait_for_renderer_crash_listener(
                    &renderer_crash_listener_ready,
                    RENDERER_CRASH_LISTENER_READY_ATTEMPTS,
                    RENDERER_CRASH_LISTENER_READY_DELAY,
                ) {
                    error!(event = "renderer_crash_listener_timeout");
                    app_handle.exit(70);
                    return;
                }
                match webview
                    .send_dev_tools_message(br#"{"id":9001,"method":"Page.crash","params":{}}"#)
                {
                    Ok(()) => {
                        info!(event = "renderer_crash_requested");
                        start_renderer_crash_watchdog(app_handle, renderer_crashed);
                    }
                    Err(error) => {
                        error!(event = "renderer_crash_request_failed", reason = %error);
                        app_handle.exit(70);
                    }
                }
            });
        }
        Some(SmokeMode::MissingResource) | None => {}
    }
}

#[tauri::cef_entry_point]
fn main() {
    let smoke_mode = SmokeMode::from_environment();
    let subprocess = is_cef_subprocess();
    init_logging(smoke_mode, subprocess);
    if !subprocess && let Err(message) = validate_host(smoke_mode) {
        error!(event = "cef_fatal_initialization", reason = %message);
        std::process::exit(78);
    }

    #[cfg(target_os = "linux")]
    if !subprocess && let Err(error) = gtk::init() {
        error!(event = "gtk_initialization_failed", reason = %error);
        std::process::exit(78);
    }

    let frontend_readiness = FrontendReadiness {
        complete: Arc::new(AtomicBool::new(false)),
        smoke_mode,
        renderer_crashed: Arc::new(AtomicBool::new(false)),
        renderer_crash_listener_ready: Arc::new(AtomicBool::new(cfg!(target_os = "macos"))),
    };

    let mut builder = tauri::Builder::<tauri::Cef>::default()
        .plugin(native_plugin::init())
        .manage(bridge::NativeBridgeState::default())
        .manage(frontend_readiness.clone())
        .invoke_handler(tauri::generate_handler![
            bridge::native_bridge_v1,
            frontend_ready,
            open_external,
            set_tray_language
        ])
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                if let Err(reason) = window.hide() {
                    error!(event = "tray_window_hide_failed", %reason);
                    if let Err(reason) = window.show() {
                        error!(event = "tray_window_hide_recovery_show_failed", %reason);
                    }
                    if let Err(reason) = window.set_focus() {
                        error!(event = "tray_window_hide_recovery_focus_failed", %reason);
                    }
                }
            }
        });
    if let Some(cache_path) = std::env::var_os("DEVHUD_SMOKE_CACHE_DIR") {
        builder = builder.root_cache_path(cache_path);
    }

    #[cfg(target_os = "macos")]
    {
        let renderer_crashed = frontend_readiness.renderer_crashed.clone();
        builder = builder.on_web_content_process_terminate(move |_| {
            renderer_crashed.store(true, Ordering::SeqCst);
            error!(event = "renderer_terminated", source = "cef_callback");
        });
    }

    let result = builder
        .setup(move |app| {
            let readiness = frontend_readiness.clone();
            create_tray(&app.handle().clone())?;
            let webview = tauri::WebviewWindowBuilder::<tauri::Cef, _>::new(
                app,
                "main",
                WebviewUrl::App("index.html".into()),
            )
            .title("DevHUD")
            .inner_size(960.0, 640.0)
            .min_inner_size(640.0, 480.0)
            .on_navigation(|url| {
                let allowed = is_allowed_navigation(url);
                if !allowed {
                    warn!(event = "navigation_denied", scheme = url.scheme());
                }
                allowed
            })
            .on_new_window(|url, _| {
                warn!(event = "popup_denied", scheme = url.scheme());
                tauri::webview::NewWindowResponse::Deny
            })
            .on_download(|_, event| {
                if let tauri::webview::DownloadEvent::Requested { url, .. } = event {
                    warn!(event = "download_denied", scheme = url.scheme());
                }
                false
            })
            .build()?;

            start_frontend_readiness_watchdog(app.handle().clone(), readiness.complete.clone());

            #[cfg(not(target_os = "macos"))]
            if should_observe_renderer_crashes(tauri::is_dev()) {
                let renderer_crashed_for_protocol = readiness.renderer_crashed.clone();
                webview.on_dev_tools_protocol(move |message| {
                    if let tauri::CefDevToolsProtocol::Event { method, .. } = message
                        && method.contains("targetCrashed")
                    {
                        renderer_crashed_for_protocol.store(true, Ordering::SeqCst);
                        error!(event = "renderer_terminated", source = "cdp");
                    }
                })?;
                if let Err(error) = webview.send_dev_tools_message(
                    br#"{"id":9000,"method":"Inspector.enable","params":{}}"#,
                ) {
                    error!(event = "renderer_diagnostic_enable_failed", reason = %error);
                    if smoke_mode == Some(SmokeMode::RendererCrash) {
                        app.handle().exit(70);
                    }
                } else {
                    readiness
                        .renderer_crash_listener_ready
                        .store(true, Ordering::SeqCst);
                }
            }

            #[cfg(target_os = "macos")]
            drop(webview);

            Ok(())
        })
        .run(tauri::generate_context!());

    match result {
        Ok(()) => info!(event = "host_shutdown_complete"),
        Err(error) => {
            error!(event = "cef_fatal_initialization", reason = %error);
            std::process::exit(1);
        }
    }
}

#[cfg(test)]
mod review_tests {
    #[cfg(target_os = "macos")]
    use std::os::unix::process::ExitStatusExt;
    #[cfg(target_os = "linux")]
    use std::sync::mpsc;

    #[cfg(target_os = "linux")]
    use gtk::prelude::CancellableExt;
    #[cfg(target_os = "linux")]
    use gtk::{gio, glib};

    #[cfg(target_os = "macos")]
    use super::confirm_external_opener_status;
    #[cfg(target_os = "linux")]
    use super::{await_linux_external_dispatch, cancel_linux_external_dispatch};
    use super::{external_destination, tray_labels};

    #[test]
    fn external_destinations_are_closed_and_validate_the_authentication_origin() {
        assert_eq!(
            external_destination("pat", "https://example.test"),
            Some("https://github.com/settings/personal-access-tokens/new".to_string())
        );
        assert_eq!(
            external_destination("authentication", "https://example.test"),
            Some("https://example.test/".to_string())
        );
        assert_eq!(
            external_destination("authentication", "http://example.test"),
            None
        );
        assert_eq!(
            external_destination("authentication", "http://[::1]:46307"),
            Some("http://[::1]:46307/".to_string())
        );
        assert_eq!(
            external_destination("authentication", "http://127.0.0.2:46307"),
            Some("http://127.0.0.2:46307/".to_string())
        );
        assert_eq!(
            external_destination("documentation", "https://example.test"),
            None
        );
        assert_eq!(
            external_destination("unknown", "https://example.test"),
            None
        );
    }

    #[test]
    fn tray_labels_are_localized() {
        assert_eq!(tray_labels("en"), ("Show DevHUD", "Quit DevHUD"));
        assert_eq!(tray_labels("ko"), ("DevHUD 표시", "DevHUD 종료"));
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn external_opener_requires_a_successful_exit_status() {
        assert!(confirm_external_opener_status(std::process::ExitStatus::from_raw(0)).is_ok());
        assert!(confirm_external_opener_status(std::process::ExitStatus::from_raw(256)).is_err());
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn linux_external_opener_uses_bounded_gio_dispatch() {
        let (sender, receiver) = mpsc::sync_channel(1);
        sender.send(Ok(())).expect("dispatch result channel");
        let cancellable = gio::Cancellable::new();
        let main_loop = glib::MainLoop::new(None, false);
        assert!(await_linux_external_dispatch(receiver, &cancellable, &main_loop).is_ok());
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn linux_external_opener_cancels_timed_out_gio_dispatch() {
        let cancellable = gio::Cancellable::new();
        let main_loop = glib::MainLoop::new(None, false);

        cancel_linux_external_dispatch(&cancellable, &main_loop);

        assert!(cancellable.is_cancelled());
    }
}

#[cfg(test)]
mod tests {
    use std::{
        io::{self, Write},
        sync::{
            Arc, Mutex,
            atomic::{AtomicBool, Ordering},
        },
        time::Duration,
    };

    use tracing::{debug, error, info, warn};
    use tracing_subscriber::{Layer, layer::SubscriberExt};

    #[cfg(not(target_os = "macos"))]
    use super::should_observe_renderer_crashes;
    use super::{
        SmokeMode, diagnostic_filter, inject_smoke_missing_resource,
        wait_for_frontend_readiness_timeout, wait_for_renderer_crash_listener,
    };

    #[test]
    fn frontend_readiness_claim_is_one_shot() {
        let frontend_readiness_complete = AtomicBool::new(false);

        assert!(!frontend_readiness_complete.swap(true, Ordering::SeqCst));
        assert!(frontend_readiness_complete.swap(true, Ordering::SeqCst));
    }

    #[test]
    fn renderer_crash_dispatch_waits_for_the_listener() {
        let listener_ready = Arc::new(AtomicBool::new(false));
        let listener_ready_for_wait = listener_ready.clone();
        let waiter = std::thread::spawn(move || {
            wait_for_renderer_crash_listener(
                &listener_ready_for_wait,
                100,
                Duration::from_millis(1),
            )
        });

        std::thread::sleep(Duration::from_millis(5));
        assert!(!waiter.is_finished());
        listener_ready.store(true, Ordering::SeqCst);

        assert!(waiter.join().expect("listener readiness waiter panicked"));
    }

    #[cfg(not(target_os = "macos"))]
    #[test]
    fn renderer_crash_observation_is_enabled_only_for_packaged_launches() {
        assert!(should_observe_renderer_crashes(false));
        assert!(!should_observe_renderer_crashes(true));
    }

    #[test]
    fn diagnostic_filter_preserves_configured_devhud_info() {
        let output = capture_diagnostics(Some("info"), || {
            info!(target: "devhud", "configured info");
            debug!(target: "devhud", "unconfigured debug");
        });

        assert!(output.contains("configured info"));
        assert!(!output.contains("unconfigured debug"));
    }

    #[test]
    fn diagnostic_filter_keeps_only_devhud_errors_enabled_when_off() {
        let output = capture_diagnostics(Some("off"), || {
            error!(target: "devhud", "fatal DevHUD diagnostic");
            warn!(target: "devhud", "disabled DevHUD warning");
            error!(target: "dependency", "disabled dependency error");
        });

        assert!(output.contains("fatal DevHUD diagnostic"));
        assert!(!output.contains("disabled DevHUD warning"));
        assert!(!output.contains("disabled dependency error"));
    }

    #[test]
    fn diagnostic_filter_uses_info_for_missing_or_invalid_configuration() {
        for rust_log in [None, Some("devhud=invalid-level")] {
            let output = capture_diagnostics(rust_log, || {
                info!(target: "devhud", "fallback info");
                debug!(target: "devhud", "fallback debug");
            });

            assert!(output.contains("fallback info"));
            assert!(!output.contains("fallback debug"));
        }
    }

    fn capture_diagnostics(rust_log: Option<&str>, callback: impl FnOnce()) -> String {
        let buffer = Arc::new(Mutex::new(Vec::new()));
        let writer = SharedWriter(buffer.clone());
        let layer = tracing_subscriber::fmt::layer()
            .with_ansi(false)
            .with_target(false)
            .with_level(false)
            .without_time()
            .with_writer(move || writer.clone())
            .with_filter(diagnostic_filter(rust_log));
        let subscriber = tracing_subscriber::registry().with(layer);

        tracing::subscriber::with_default(subscriber, callback);

        let output = buffer.lock().expect("lock log buffer").clone();
        String::from_utf8(output).expect("utf8 log output")
    }

    #[test]
    fn frontend_readiness_timeout_claims_pending_state() {
        let frontend_readiness_complete = AtomicBool::new(false);

        assert!(wait_for_frontend_readiness_timeout(
            &frontend_readiness_complete,
            Duration::ZERO,
        ));
        assert!(frontend_readiness_complete.load(Ordering::SeqCst));
    }

    #[test]
    fn frontend_readiness_timeout_ignores_completed_state() {
        let frontend_readiness_complete = AtomicBool::new(true);

        assert!(!wait_for_frontend_readiness_timeout(
            &frontend_readiness_complete,
            Duration::ZERO,
        ));
    }

    #[test]
    fn missing_resource_smoke_injects_resources_pak_once() {
        let mut missing = Vec::new();

        inject_smoke_missing_resource(&mut missing, Some(SmokeMode::MissingResource));
        inject_smoke_missing_resource(&mut missing, Some(SmokeMode::MissingResource));

        assert_eq!(missing, ["resources.pak"]);
    }

    #[test]
    fn other_smoke_modes_do_not_inject_resources() {
        for smoke_mode in [
            None,
            Some(SmokeMode::Normal),
            Some(SmokeMode::RendererCrash),
        ] {
            let mut missing = Vec::new();
            inject_smoke_missing_resource(&mut missing, smoke_mode);
            assert!(missing.is_empty());
        }
    }

    #[derive(Clone)]
    struct SharedWriter(Arc<Mutex<Vec<u8>>>);

    impl Write for SharedWriter {
        fn write(&mut self, buffer: &[u8]) -> io::Result<usize> {
            self.0
                .lock()
                .expect("lock log buffer")
                .extend_from_slice(buffer);
            Ok(buffer.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }
}
