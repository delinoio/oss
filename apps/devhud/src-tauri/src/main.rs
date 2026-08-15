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

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum SmokeMode {
    Normal,
    RendererCrash,
}

impl SmokeMode {
    fn from_environment() -> Option<Self> {
        match std::env::var("DEVHUD_PLATFORM_SMOKE").ok().as_deref() {
            Some("normal") => Some(Self::Normal),
            Some("renderer-crash") => Some(Self::RendererCrash),
            _ => None,
        }
    }
}

fn is_cef_subprocess() -> bool {
    std::env::args().any(|argument| argument.starts_with("--type="))
}

fn init_logging() {
    let filter = tracing_subscriber::EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info"));
    let _ = tracing_subscriber::fmt()
        .json()
        .with_env_filter(filter)
        .with_target(false)
        .without_time()
        .try_init();
}

fn is_allowed_navigation(url: &tauri::Url) -> bool {
    let origin = url.origin().ascii_serialization();
    origin == PRODUCTION_ORIGIN || (tauri::is_dev() && origin == DEVELOPMENT_ORIGIN)
}

fn validate_host() -> Result<(), String> {
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

    let executable = std::env::current_exe()
        .map_err(|error| format!("failed to resolve the current executable: {error}"))?;
    let layout = ResourceLayout::for_executable(&executable)?;
    let missing = layout.missing();
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

fn main() {
    init_logging();
    let subprocess = is_cef_subprocess();
    if !subprocess && let Err(message) = validate_host() {
        error!(event = "cef_fatal_initialization", reason = %message);
        std::process::exit(78);
    }

    let smoke_mode = SmokeMode::from_environment();
    let renderer_crashed = Arc::new(AtomicBool::new(false));

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
                info!(event = "frontend_ready", origin = %payload.url().origin().ascii_serialization());
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
                        info!(event = "renderer_crash_requested");
                        if let Err(error) = webview.send_dev_tools_message(
                            br#"{"id":9001,"method":"Page.crash","params":{}}"#,
                        ) {
                            error!(event = "renderer_crash_request_failed", reason = %error);
                        }
                    }
                    None => {}
                }
            })
            .build()?;

            if smoke_mode == Some(SmokeMode::RendererCrash) {
                webview.on_dev_tools_protocol(move |message| {
                    if let tauri::CefDevToolsProtocol::Event { method, .. } = message
                        && method.contains("targetCrashed")
                    {
                        renderer_crashed_for_protocol.store(true, Ordering::SeqCst);
                        error!(event = "renderer_terminated", source = "cdp");
                    }
                })?;

                let app_handle = app.handle().clone();
                let renderer_crashed = renderer_crashed.clone();
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
