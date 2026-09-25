#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::{path::PathBuf, sync::Arc};

use delidev_desktop::{
    Connection, Connector, LocalServerStatus, NativeFailure, Supervision, bundled_sidecar,
    default_data_root,
};
use tauri::{WebviewWindow, WebviewWindowBuilder, Wry, webview::NewWindowResponse};

fn trusted_url(url: &tauri::Url) -> bool {
    let origin =
        (url.scheme() == "tauri" && url.host_str() == Some("localhost") && url.port().is_none())
            || (url.scheme() == "http"
                && url.host_str() == Some("tauri.localhost")
                && url.port().is_none())
            || (cfg!(debug_assertions)
                && url.scheme() == "http"
                && url.host_str() == Some("127.0.0.1")
                && url.port() == Some(46311));
    origin
        && ["", "/", "/index.html"].contains(&url.path())
        && url.query().is_none()
        && url.username().is_empty()
        && url.password().is_none()
}

#[tauri::command]
async fn connect_local(
    window: WebviewWindow<Wry>,
    connector: tauri::State<'_, Arc<Connector>>,
    supervision: tauri::State<'_, Arc<Supervision>>,
) -> Result<Connection, NativeFailure> {
    if window.label() != "main"
        || !trusted_url(&window.url().map_err(|_| NativeFailure::PermissionDenied)?)
    {
        return Err(NativeFailure::PermissionDenied);
    }
    let connector = Arc::clone(connector.inner());
    let result = tauri::async_runtime::spawn_blocking(move || connector.connect())
        .await
        .map_err(|_| NativeFailure::SidecarFailed)?;
    supervision.refresh();
    let connection = result?;
    if connection.endpoint != "http://127.0.0.1:46310" {
        return Err(NativeFailure::Incompatible);
    }
    Ok(connection)
}

#[tauri::command]
fn local_server_status(
    window: WebviewWindow<Wry>,
    supervision: tauri::State<'_, Arc<Supervision>>,
) -> Result<LocalServerStatus, NativeFailure> {
    if window.label() != "main"
        || !trusted_url(&window.url().map_err(|_| NativeFailure::PermissionDenied)?)
    {
        return Err(NativeFailure::PermissionDenied);
    }
    Ok(supervision.status())
}

fn run() -> Result<(), NativeFailure> {
    let mut args = std::env::args_os().skip(1);
    let root = match args.next() {
        None => default_data_root()?,
        Some(flag) if flag == "--data-dir" => {
            PathBuf::from(args.next().ok_or(NativeFailure::InvalidEvidence)?)
        }
        _ => return Err(NativeFailure::InvalidEvidence),
    };
    if args.next().is_some() || !root.is_absolute() {
        return Err(NativeFailure::InvalidEvidence);
    }
    let executable = std::env::current_exe().map_err(|_| NativeFailure::SidecarMissing)?;
    let connector = Arc::new(Connector::new(bundled_sidecar(&executable)?, root)?);
    let supervision = Arc::new(Supervision::new(Arc::clone(&connector)));
    tauri::Builder::<Wry>::new()
        .manage(connector)
        .manage(Arc::clone(&supervision))
        .invoke_handler(tauri::generate_handler![connect_local, local_server_status])
        .setup(|app| {
            let result = (|| -> tauri::Result<()> {
                let config = &app.config().app.windows[0];
                WebviewWindowBuilder::from_config(app, config)?
                    .incognito(true)
                    .on_navigation(|url| {
                        let allowed = trusted_url(url);
                        tracing::info!(operation = "main_navigation", allowed);
                        allowed
                    })
                    .on_page_load(|_, payload| {
                        tracing::info!(operation = "main_page", phase = ?payload.event());
                    })
                    .on_new_window(|_, _| NewWindowResponse::Deny)
                    .build()?;
                Ok(())
            })();
            if result.is_err() {
                tracing::error!(operation = "main_window", code = "window-unavailable");
                app.handle().exit(1);
            }
            Ok(())
        })
        .run(tauri::generate_context!())
        .map_err(|_| NativeFailure::SidecarFailed)
}

fn main() {
    tracing_subscriber::fmt()
        .json()
        .with_target(false)
        .with_writer(std::io::stderr)
        .init();
    if let Err(code) = run() {
        tracing::error!(operation = "desktop_host", ?code);
        std::process::exit(1);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn only_local_entry_documents_receive_native_capabilities() {
        for value in [
            "tauri://localhost",
            "tauri://localhost/",
            "tauri://localhost/index.html#main",
            "http://tauri.localhost/",
        ] {
            assert!(trusted_url(&value.parse().unwrap()), "{value}");
        }
        for value in [
            "https://example.com/",
            "http://tauri.localhost:81/",
            "tauri://other/",
            "tauri://localhost/private",
            "tauri://localhost/?token=bad",
            "tauri://user@localhost/",
        ] {
            assert!(!trusted_url(&value.parse().unwrap()), "{value}");
        }
    }
}
