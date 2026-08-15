#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod platform;
mod resources;

use std::{
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

use platform::DesktopTarget;
#[cfg(target_os = "linux")]
use platform::LinuxDisplayMode;
use resources::ResourceLayout;
use tauri::WebviewUrl;
use tracing::{error, info, warn};

const DEVELOPMENT_ORIGIN: &str = "http://127.0.0.1:46305";
const PRODUCTION_ORIGIN: &str = "http://tauri.localhost";
const FRONTEND_READY_PROBE: &str =
    r#"document.querySelector('[data-devhud-ready="true"]') !== null"#;
const FRONTEND_READY_ATTEMPTS: u8 = 100;
const FRONTEND_READY_RETRY_DELAY: Duration = Duration::from_millis(50);
const FRONTEND_READY_TIMEOUT: Duration = Duration::from_secs(5);

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum SmokeMode {
    Normal,
    RendererCrash,
    MissingResource,
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
    use std::{fs::OpenOptions, sync::Mutex};

    use tracing_subscriber::fmt::writer::MakeWriterExt;

    if cfg!(debug_assertions) || subprocess {
        return tracing_subscriber::fmt::writer::BoxMakeWriter::new(std::io::stderr);
    }

    let result = (|| {
        let directory = diagnostic_log_directory(smoke_mode).ok_or(std::io::ErrorKind::NotFound)?;
        std::fs::create_dir_all(&directory).map_err(|error| error.kind())?;
        OpenOptions::new()
            .create(true)
            .append(true)
            .open(directory.join("devhud.jsonl"))
            .map_err(|error| error.kind())
    })();

    match result {
        Ok(file) => tracing_subscriber::fmt::writer::BoxMakeWriter::new(
            std::io::stderr.and(Mutex::new(file)),
        ),
        Err(kind) => {
            eprintln!(
                "{{\"level\":\"WARN\",\"event\":\"file_logging_unavailable\",\"kind\":\"{kind:?}\"\
                 }}"
            );
            tracing_subscriber::fmt::writer::BoxMakeWriter::new(std::io::stderr)
        }
    }
}

fn init_logging(smoke_mode: Option<SmokeMode>, subprocess: bool) {
    let filter = tracing_subscriber::EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info"));

    let writer = diagnostic_writer(smoke_mode, subprocess);

    let _ = tracing_subscriber::fmt()
        .json()
        .with_env_filter(filter)
        .with_writer(writer)
        .with_target(false)
        .try_init();
}

fn is_allowed_navigation(url: &tauri::Url) -> bool {
    let origin = url.origin().ascii_serialization();
    origin == PRODUCTION_ORIGIN || (tauri::is_dev() && origin == DEVELOPMENT_ORIGIN)
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

fn probe_frontend_ready(
    webview: tauri::WebviewWindow<tauri::Cef>,
    app_handle: tauri::AppHandle<tauri::Cef>,
    smoke_mode: Option<SmokeMode>,
    origin: String,
    frontend_readiness_complete: Arc<AtomicBool>,
    renderer_crashed: Arc<AtomicBool>,
    attempts_remaining: u8,
) {
    let retry_webview = webview.clone();
    let retry_app_handle = app_handle.clone();
    let retry_origin = origin.clone();
    let retry_frontend_readiness_complete = frontend_readiness_complete.clone();
    let retry_renderer_crashed = renderer_crashed.clone();
    let failure_app_handle = app_handle.clone();
    let failure_frontend_readiness_complete = frontend_readiness_complete.clone();

    if let Err(error) =
        webview.eval_with_callback(
            FRONTEND_READY_PROBE,
            move |result| match serde_json::from_str::<bool>(&result) {
                Ok(true) => {
                    if !retry_frontend_readiness_complete.swap(true, Ordering::SeqCst) {
                        handle_frontend_ready(
                            &retry_webview,
                            &retry_app_handle,
                            smoke_mode,
                            &retry_origin,
                            retry_renderer_crashed.clone(),
                        );
                    }
                }
                Ok(false) if attempts_remaining > 1 => {
                    let webview = retry_webview.clone();
                    let app_handle = retry_app_handle.clone();
                    let origin = retry_origin.clone();
                    let frontend_readiness_complete = retry_frontend_readiness_complete.clone();
                    let renderer_crashed = retry_renderer_crashed.clone();
                    std::thread::spawn(move || {
                        std::thread::sleep(FRONTEND_READY_RETRY_DELAY);
                        probe_frontend_ready(
                            webview,
                            app_handle,
                            smoke_mode,
                            origin,
                            frontend_readiness_complete,
                            renderer_crashed,
                            attempts_remaining - 1,
                        );
                    });
                }
                Ok(false) => {}
                Err(error) => {
                    error!(event = "frontend_readiness_probe_failed", reason = %error, result);
                    if smoke_mode.is_some() {
                        retry_app_handle.exit(1);
                    }
                }
            },
        )
    {
        error!(event = "frontend_readiness_probe_failed", reason = %error);
        if smoke_mode.is_some() && !failure_frontend_readiness_complete.load(Ordering::SeqCst) {
            failure_app_handle.exit(1);
        }
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

    let renderer_crashed = Arc::new(AtomicBool::new(false));
    let frontend_readiness_complete = Arc::new(AtomicBool::new(false));

    let mut builder = tauri::Builder::<tauri::Cef>::default();
    if let Some(cache_path) = std::env::var_os("DEVHUD_SMOKE_CACHE_DIR") {
        builder = builder.root_cache_path(cache_path);
    }

    #[cfg(target_os = "macos")]
    {
        let renderer_crashed = renderer_crashed.clone();
        builder = builder.on_web_content_process_terminate(move |_| {
            renderer_crashed.store(true, Ordering::SeqCst);
            error!(event = "renderer_terminated", source = "cef_callback");
        });
    }

    let result = builder
        .setup(move |app| {
            let app_handle = app.handle().clone();
            let renderer_crashed_for_protocol = renderer_crashed.clone();
            let renderer_crashed_for_page_load = renderer_crashed.clone();
            let frontend_readiness_for_page_load = frontend_readiness_complete.clone();
            let frontend_readiness_for_watchdog = frontend_readiness_complete.clone();
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
            .on_page_load(move |webview, payload| {
                if payload.event() != tauri::webview::PageLoadEvent::Finished {
                    return;
                }
                probe_frontend_ready(
                    webview.clone(),
                    app_handle.clone(),
                    smoke_mode,
                    payload.url().origin().ascii_serialization(),
                    frontend_readiness_for_page_load.clone(),
                    renderer_crashed_for_page_load.clone(),
                    FRONTEND_READY_ATTEMPTS,
                );
            })
            .build()?;

            start_frontend_readiness_watchdog(
                app.handle().clone(),
                frontend_readiness_for_watchdog,
            );

            webview.on_dev_tools_protocol(move |message| {
                if let tauri::CefDevToolsProtocol::Event { method, .. } = message
                    && method.contains("targetCrashed")
                {
                    renderer_crashed_for_protocol.store(true, Ordering::SeqCst);
                    error!(event = "renderer_terminated", source = "cdp");
                }
            })?;
            if let Err(error) = webview
                .send_dev_tools_message(br#"{"id":9000,"method":"Inspector.enable","params":{}}"#)
            {
                error!(event = "renderer_diagnostic_enable_failed", reason = %error);
            }

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
mod tests {
    use std::{
        sync::atomic::{AtomicBool, Ordering},
        time::Duration,
    };

    use super::{SmokeMode, inject_smoke_missing_resource, wait_for_frontend_readiness_timeout};

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
}
