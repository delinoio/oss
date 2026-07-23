#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
use serde::Serialize;
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
use url::Url;

#[cfg(all(feature = "desktop-cef", feature = "mobile-system-webview"))]
compile_error!("select exactly one DevHud runtime feature");
#[cfg(all(feature = "desktop-cef", any(target_os = "android", target_os = "ios")))]
compile_error!("desktop-cef cannot be used for iOS or Android");
#[cfg(all(
    feature = "mobile-system-webview",
    not(any(target_os = "android", target_os = "ios"))
))]
compile_error!("mobile-system-webview is reserved for iOS and Android targets");

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
use std::time::Duration;

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
use tauri::{
    AppHandle, Webview, WebviewUrl,
    http::{HeaderName, HeaderValue},
    webview::{NewWindowResponse, WebviewWindowBuilder},
};

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
type ActiveRuntime = tauri_runtime_cef::CefRuntime<tauri::EventLoopMessage>;
#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
type ActiveRuntime = tauri::Wry;

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
const APPLICATION_ID: &str = "dev.deli.devhud";
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
const PROBE_WINDOW_LABEL: &str = "probe";
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
const PERMISSIONS_POLICY: &str =
    "camera=(), display-capture=(), geolocation=(), microphone=(), usb=()";

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct StartupReceipt {
    application_id: &'static str,
    bundled_origin: String,
    runtime: &'static str,
    sandbox_enabled: bool,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
enum ProbeCommandError {
    NonBundledAsset,
    ForbiddenCommandReached,
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview", test))]
fn is_bundled_url(url: &Url) -> bool {
    url.port().is_none()
        && matches!(
            (url.scheme(), url.host_str()),
            ("tauri", Some("localhost"))
                | ("http", Some("tauri.localhost"))
                | ("https", Some("tauri.localhost"))
        )
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn bundled_origin(url: &Url) -> String {
    match url.host_str() {
        Some(host) => format!("{}://{host}", url.scheme()),
        None => url.scheme().to_string(),
    }
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
const fn runtime_name() -> &'static str {
    "cef"
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
const fn runtime_name() -> &'static str {
    "system-webview"
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn probe_bundled_asset_ready(
    webview: Webview<ActiveRuntime>,
) -> Result<StartupReceipt, ProbeCommandError> {
    let url = webview
        .url()
        .map_err(|_| ProbeCommandError::NonBundledAsset)?;
    if !is_bundled_url(&url) {
        return Err(ProbeCommandError::NonBundledAsset);
    }

    tracing::info!(
        event = "devhud.probe.bundled_asset_ready",
        runtime = runtime_name(),
        "bundled asset startup observed"
    );

    Ok(StartupReceipt {
        application_id: APPLICATION_ID,
        bundled_origin: bundled_origin(&url),
        runtime: runtime_name(),
        sandbox_enabled: cfg!(not(any(target_os = "android", target_os = "ios"))),
    })
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn probe_denial_observed(app: AppHandle<ActiveRuntime>) {
    tracing::info!(
        event = "devhud.probe.capability_denial_observed",
        "undeclared command was denied"
    );

    if std::env::var_os("DEVHUD_PROBE_SMOKE").is_some() {
        std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(100));
            app.exit(0);
        });
    }
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
#[tauri::command]
fn probe_forbidden() -> Result<(), ProbeCommandError> {
    tracing::error!(
        event = "devhud.probe.capability_boundary_failed",
        "forbidden command reached its handler"
    );
    Err(ProbeCommandError::ForbiddenCommandReached)
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn configure_builder(builder: tauri::Builder<ActiveRuntime>) -> tauri::Builder<ActiveRuntime> {
    builder
        .invoke_handler(tauri::generate_handler![
            probe_bundled_asset_ready,
            probe_denial_observed,
            probe_forbidden,
        ])
        .setup(|app| {
            WebviewWindowBuilder::new(
                app,
                PROBE_WINDOW_LABEL,
                WebviewUrl::App("index.html".into()),
            )
            .title("DevHud feasibility probe")
            .inner_size(720.0, 520.0)
            .devtools(true)
            .disable_drag_drop_handler()
            .on_navigation(is_bundled_url)
            .on_new_window(|_, _| NewWindowResponse::Deny)
            .on_download(|_, _| false)
            .on_web_resource_request(|_, response| {
                response.headers_mut().insert(
                    HeaderName::from_static("permissions-policy"),
                    HeaderValue::from_static(PERMISSIONS_POLICY),
                );
            })
            .build()?;

            tracing::info!(
                event = "devhud.probe.window_created",
                runtime = runtime_name(),
                "feasibility probe window created"
            );
            Ok(())
        })
}

#[cfg(all(
    feature = "desktop-cef",
    not(any(target_os = "android", target_os = "ios"))
))]
fn platform_builder() -> tauri::Builder<ActiveRuntime> {
    tauri::Builder::<ActiveRuntime>::new().command_line_args([
        ("--disable-background-networking", None::<&str>),
        ("--disable-component-update", None),
        ("--disable-domain-reliability", None),
        ("--disable-sync", None),
        (
            "host-resolver-rules",
            Some("MAP * ~NOTFOUND, EXCLUDE tauri.localhost"),
        ),
    ])
}

#[cfg(all(
    feature = "mobile-system-webview",
    any(target_os = "android", target_os = "ios")
))]
fn platform_builder() -> tauri::Builder<ActiveRuntime> {
    tauri::Builder::<ActiveRuntime>::new()
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn initialize_logging() {
    let _ = tracing_subscriber::fmt()
        .json()
        .with_target(false)
        .without_time()
        .try_init();
}

#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
fn run_app() -> tauri::Result<()> {
    let app = configure_builder(platform_builder()).build(tauri::generate_context!())?;
    app.run(|_, _| {});
    Ok(())
}

#[cfg_attr(
    all(
        feature = "desktop-cef",
        not(any(target_os = "android", target_os = "ios"))
    ),
    tauri::cef_entry_point
)]
#[cfg_attr(
    all(
        feature = "mobile-system-webview",
        any(target_os = "android", target_os = "ios")
    ),
    tauri::mobile_entry_point
)]
#[cfg(any(feature = "desktop-cef", feature = "mobile-system-webview"))]
pub fn run() {
    initialize_logging();

    if run_app().is_err() {
        tracing::error!(
            event = "devhud.probe.cef_initialization_failure",
            classification = "cef-initialization",
            "runtime initialization failed"
        );
        std::process::exit(70);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn permits_only_bundled_application_origins() {
        for allowed in [
            "tauri://localhost/index.html",
            "http://tauri.localhost/index.html",
            "https://tauri.localhost/index.html",
        ] {
            assert!(is_bundled_url(&allowed.parse().unwrap()), "{allowed}");
        }

        for denied in [
            "https://example.com/",
            "http://localhost:4173/",
            "https://tauri.localhost:8080/index.html",
            "file:///tmp/index.html",
            "data:text/html,probe",
            "about:blank",
        ] {
            assert!(!is_bundled_url(&denied.parse().unwrap()), "{denied}");
        }
    }

    #[test]
    fn receipt_uses_stable_application_id_and_desktop_contract() {
        let receipt = StartupReceipt {
            application_id: APPLICATION_ID,
            bundled_origin: "http://tauri.localhost".to_string(),
            runtime: "cef",
            sandbox_enabled: true,
        };
        let value = serde_json::to_value(receipt).unwrap();

        assert_eq!(value["applicationId"], APPLICATION_ID);
        assert_eq!(value["runtime"], "cef");
        assert_eq!(value["sandboxEnabled"], true);
    }

    #[test]
    fn media_permissions_are_explicitly_disabled() {
        for directive in ["camera=()", "microphone=()", "display-capture=()"] {
            assert!(PERMISSIONS_POLICY.contains(directive));
        }
    }
}
