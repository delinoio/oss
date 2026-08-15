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
const FRONTEND_READY_TITLE: &str = "DevHUD";
const FRONTEND_READY_TIMEOUT: Duration = Duration::from_secs(5);
const DEVHUD_ERROR_FILTER_DIRECTIVE: &str = "devhud=error";

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

fn diagnostic_filter(rust_log: Option<&str>) -> tracing_subscriber::EnvFilter {
    let filter = rust_log
        .and_then(|value| tracing_subscriber::EnvFilter::try_new(value).ok())
        .unwrap_or_else(|| tracing_subscriber::EnvFilter::new("info"));

    // Operator filters may tune dependency verbosity, but fatal application
    // diagnostics must remain available when a packaged GUI cannot start.
    filter.add_directive(
        DEVHUD_ERROR_FILTER_DIRECTIVE
            .parse()
            .expect("the DevHUD error filter directive must remain valid"),
    )
}

fn init_logging(smoke_mode: Option<SmokeMode>, subprocess: bool) {
    let rust_log = std::env::var("RUST_LOG").ok();
    let filter = diagnostic_filter(rust_log.as_deref());

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

fn is_frontend_ready_title(title: &str) -> bool {
    title == FRONTEND_READY_TITLE
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
            let app_handle_for_frontend = app.handle().clone();
            let renderer_crashed_for_frontend = renderer_crashed.clone();
            let frontend_readiness_for_title = frontend_readiness_complete.clone();
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
            .on_document_title_changed(move |webview, title| {
                if !is_frontend_ready_title(&title) {
                    return;
                }

                let origin = if tauri::is_dev() {
                    DEVELOPMENT_ORIGIN
                } else {
                    PRODUCTION_ORIGIN
                };
                if frontend_readiness_for_title.swap(true, Ordering::SeqCst) {
                    return;
                }
                handle_frontend_ready(
                    &webview,
                    &app_handle_for_frontend,
                    smoke_mode,
                    origin,
                    renderer_crashed_for_frontend.clone(),
                );
            })
            .build()?;

            start_frontend_readiness_watchdog(
                app.handle().clone(),
                frontend_readiness_for_watchdog,
            );

            #[cfg(not(target_os = "macos"))]
            if smoke_mode == Some(SmokeMode::RendererCrash) {
                let renderer_crashed_for_protocol = renderer_crashed.clone();
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
mod tests {
    use std::{
        sync::atomic::{AtomicBool, Ordering},
        time::Duration,
    };

    use super::{
        DEVHUD_ERROR_FILTER_DIRECTIVE, SmokeMode, diagnostic_filter, inject_smoke_missing_resource,
        is_frontend_ready_title, wait_for_frontend_readiness_timeout,
    };

    #[test]
    fn frontend_readiness_requires_the_mounted_title() {
        assert!(!is_frontend_ready_title("DevHUD Loading"));
        assert!(is_frontend_ready_title("DevHUD"));
    }

    #[test]
    fn diagnostic_filter_keeps_devhud_errors_enabled() {
        for rust_log in [Some("off"), Some("devhud=off"), Some("warn,devhud=trace")] {
            let configured = diagnostic_filter(rust_log).to_string();

            assert!(
                configured
                    .split(',')
                    .any(|directive| directive == DEVHUD_ERROR_FILTER_DIRECTIVE),
                "missing error floor in {configured}"
            );
        }
    }

    #[test]
    fn diagnostic_filter_uses_info_for_missing_or_invalid_configuration() {
        for rust_log in [None, Some("devhud=invalid-level")] {
            let configured = diagnostic_filter(rust_log).to_string();

            assert!(
                configured.split(',').any(|directive| directive == "info"),
                "missing info fallback in {configured}"
            );
        }
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
}
