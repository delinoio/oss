use std::sync::Once;

use serde::Serialize;
use tracing::{info, warn};
use url::Url;

const DEFAULT_LOCAL_REMOTE_URL: &str = "http://127.0.0.1:7878";
const LOCAL_REMOTE_OVERRIDE_URL_ENV: &str = "DEXDEX_LOCAL_REMOTE_OVERRIDE_URL";
const LEGACY_LOCAL_REMOTE_URL_ENV: &str = "DEXDEX_LOCAL_REMOTE_URL";
const LOCAL_REMOTE_TOKEN_ENV: &str = "DEXDEX_LOCAL_REMOTE_TOKEN";

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum WorkspaceEndpointSource {
    ManagedLoopback,
    LocalOverride,
}

impl WorkspaceEndpointSource {
    fn as_str(&self) -> &'static str {
        match self {
            Self::ManagedLoopback => "MANAGED_LOOPBACK",
            Self::LocalOverride => "LOCAL_OVERRIDE",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct LocalWorkspaceEndpoint {
    pub endpoint_url: String,
    pub token: Option<String>,
    pub endpoint_source: WorkspaceEndpointSource,
}

fn normalize_optional_token(token: Option<String>) -> Option<String> {
    token.and_then(|raw| {
        let trimmed = raw.trim().to_owned();
        if trimmed.is_empty() {
            None
        } else {
            Some(trimmed)
        }
    })
}

fn parse_and_normalize_url(raw_url: &str) -> Result<String, String> {
    let parsed = Url::parse(raw_url).map_err(|_| {
        "DEXDEX_LOCAL_REMOTE_OVERRIDE_URL (or legacy DEXDEX_LOCAL_REMOTE_URL) must be a valid absolute URL."
            .to_owned()
    })?;

    match parsed.scheme() {
        "http" | "https" => Ok(parsed.to_string()),
        _ => Err(
            "DEXDEX_LOCAL_REMOTE_OVERRIDE_URL (or legacy DEXDEX_LOCAL_REMOTE_URL) must use http or https scheme."
                .to_owned(),
        ),
    }
}

fn normalize_optional_env_value(raw: Option<String>) -> Option<String> {
    raw.map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
}

fn resolve_override_url_from<F>(get_env: &mut F) -> Option<String>
where
    F: FnMut(&str) -> Option<String>,
{
    normalize_optional_env_value(get_env(LOCAL_REMOTE_OVERRIDE_URL_ENV))
        .or_else(|| normalize_optional_env_value(get_env(LEGACY_LOCAL_REMOTE_URL_ENV)))
}

fn redact_endpoint_url_for_logs(endpoint_url: &str) -> String {
    let mut parsed = match Url::parse(endpoint_url) {
        Ok(url) => url,
        Err(_) => return "[invalid-endpoint-url]".to_owned(),
    };

    let _ = parsed.set_username("");
    let _ = parsed.set_password(None);
    parsed.set_query(None);
    parsed.set_fragment(None);
    parsed.to_string()
}

fn resolve_local_workspace_endpoint_from<F>(
    mut get_env: F,
) -> Result<LocalWorkspaceEndpoint, String>
where
    F: FnMut(&str) -> Option<String>,
{
    let override_url = resolve_override_url_from(&mut get_env);
    let endpoint_source = if override_url.is_some() {
        WorkspaceEndpointSource::LocalOverride
    } else {
        WorkspaceEndpointSource::ManagedLoopback
    };
    let endpoint_url = override_url.unwrap_or_else(|| DEFAULT_LOCAL_REMOTE_URL.to_owned());

    let token = normalize_optional_token(get_env(LOCAL_REMOTE_TOKEN_ENV));
    let endpoint_url = parse_and_normalize_url(&endpoint_url)?;

    Ok(LocalWorkspaceEndpoint {
        endpoint_url,
        token,
        endpoint_source,
    })
}

fn resolve_local_workspace_endpoint_command() -> Result<LocalWorkspaceEndpoint, String> {
    info!(
        workspace_mode = "LOCAL",
        command = "resolve_local_workspace_endpoint",
        "resolving local workspace endpoint"
    );

    let result = resolve_local_workspace_endpoint_from(|key| std::env::var(key).ok());

    match result {
        Ok(endpoint) => {
            let redacted_endpoint_url = redact_endpoint_url_for_logs(&endpoint.endpoint_url);
            info!(
                workspace_mode = "LOCAL",
                endpoint_source = endpoint.endpoint_source.as_str(),
                endpoint_url = redacted_endpoint_url,
                result = "success",
                "resolved local workspace endpoint"
            );
            Ok(endpoint)
        }
        Err(error) => {
            warn!(
                workspace_mode = "LOCAL",
                command = "resolve_local_workspace_endpoint",
                result = "failure",
                error,
                "failed to resolve local workspace endpoint"
            );
            Err(error)
        }
    }
}

mod commands {
    use super::{resolve_local_workspace_endpoint_command, LocalWorkspaceEndpoint};

    #[tauri::command]
    pub fn resolve_local_workspace_endpoint() -> Result<LocalWorkspaceEndpoint, String> {
        resolve_local_workspace_endpoint_command()
    }

    #[derive(Clone, Debug, serde::Serialize, serde::Deserialize)]
    pub struct CredentialEntry {
        pub name: String,
        pub credential_type: String,
        pub value: String,
    }

    #[derive(Clone, Debug, serde::Serialize)]
    pub struct CredentialListEntry {
        pub name: String,
        pub credential_type: String,
    }

    fn credentials_file_path() -> Result<std::path::PathBuf, String> {
        let home = dirs::home_dir().ok_or("unable to determine home directory")?;
        let dir = home.join(".dexdex");
        std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
        Ok(dir.join("credentials.json"))
    }

    fn load_credentials() -> Result<Vec<CredentialEntry>, String> {
        let path = credentials_file_path()?;
        if !path.exists() {
            return Ok(vec![]);
        }
        let data = std::fs::read_to_string(&path).map_err(|e| e.to_string())?;
        serde_json::from_str(&data).map_err(|e| e.to_string())
    }

    fn save_credentials(creds: &[CredentialEntry]) -> Result<(), String> {
        let path = credentials_file_path()?;
        let data = serde_json::to_string_pretty(creds).map_err(|e| e.to_string())?;
        std::fs::write(&path, data).map_err(|e| e.to_string())
    }

    #[tauri::command]
    pub fn list_credentials() -> Result<Vec<CredentialListEntry>, String> {
        let creds = load_credentials()?;
        Ok(creds
            .into_iter()
            .map(|c| CredentialListEntry {
                name: c.name,
                credential_type: c.credential_type,
            })
            .collect())
    }

    #[tauri::command]
    pub fn store_credential(name: String, credential_type: String, value: String) -> Result<(), String> {
        let mut creds = load_credentials()?;
        creds.retain(|c| c.name != name);
        creds.push(CredentialEntry {
            name,
            credential_type,
            value,
        });
        save_credentials(&creds)
    }

    #[tauri::command]
    pub fn delete_credential(name: String) -> Result<(), String> {
        let mut creds = load_credentials()?;
        creds.retain(|c| c.name != name);
        save_credentials(&creds)
    }

    #[tauri::command]
    pub fn update_tray_status(
        app: tauri::AppHandle,
        status: String,
    ) -> Result<(), String> {
        let (tooltip, _icon_label) = match status.as_str() {
            "FAILED" => ("DexDex: Failed", "!"),
            "ACTION_REQUIRED" => ("DexDex: Action Required", "!"),
            "WAITING_FOR_INPUT" => ("DexDex: Waiting for Input", "?"),
            "RUNNING" => ("DexDex: Running", ">"),
            "IDLE" => ("DexDex: Idle", "-"),
            "DISCONNECTED" => ("DexDex: Disconnected", "x"),
            _ => ("DexDex", "-"),
        };

        if let Some(tray) = app.tray_by_id("dexdex-tray") {
            tray.set_tooltip(Some(tooltip)).map_err(|e| e.to_string())?;
            tracing::info!(status = %status, tooltip = %tooltip, "tray status updated");
        }

        Ok(())
    }
}

fn init_tracing() {
    static INIT: Once = Once::new();

    INIT.call_once(|| {
        let env_filter = tracing_subscriber::EnvFilter::try_from_default_env()
            .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("dexdex_desktop=info"));

        tracing_subscriber::fmt()
            .with_ansi(true)
            .with_target(false)
            .with_env_filter(env_filter)
            .init();
    });
}

pub fn run() {
    init_tracing();

    tauri::Builder::default()
        .plugin(
            tauri_plugin_global_shortcut::Builder::new()
                .with_handler(|app, shortcut, event| {
                    use tauri::Emitter;
                    if event.state == tauri_plugin_global_shortcut::ShortcutState::Pressed {
                        let shortcut_str = shortcut.to_string();
                        tracing::info!(shortcut = %shortcut_str, "global shortcut triggered");
                        let _ = app.emit("dexdex://global-shortcut-input", ());
                    }
                })
                .build(),
        )
        .setup(|app| {
            use tauri::Manager;

            // Set up menu bar tray (status-only)
            let _tray = tauri::tray::TrayIconBuilder::with_id("dexdex-tray")
                .icon(app.default_window_icon().cloned().unwrap())
                .icon_as_template(true)
                .tooltip("DexDex: Idle")
                .show_menu_on_left_click(false)
                .on_tray_icon_event(
                    |tray: &tauri::tray::TrayIcon<tauri::Wry>, event| {
                        if let tauri::tray::TrayIconEvent::Click { .. } = event {
                            let app = tray.app_handle();
                            if let Some(window) = app.get_webview_window("main") {
                                let _ = window.show();
                                let _ = window.set_focus();
                            }
                        }
                    },
                )
                .build(app)?;

            // Register global shortcut Cmd/Ctrl+Shift+I
            use tauri_plugin_global_shortcut::GlobalShortcutExt;
            app.global_shortcut().on_shortcut("CmdOrCtrl+Shift+I", |_app, _shortcut, _event| {
                // Handled by the plugin-level handler above
            }).map_err(|e| {
                tracing::warn!(error = %e, "failed to register global shortcut CmdOrCtrl+Shift+I");
                e
            })?;

            tracing::info!("DexDex tray and global shortcut initialized");
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            commands::resolve_local_workspace_endpoint,
            commands::update_tray_status,
            commands::list_credentials,
            commands::store_credential,
            commands::delete_credential
        ])
        .run(tauri::generate_context!())
        .expect("failed to run dexdex desktop app");
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use super::{
        redact_endpoint_url_for_logs, resolve_local_workspace_endpoint_from,
        WorkspaceEndpointSource, DEFAULT_LOCAL_REMOTE_URL, LEGACY_LOCAL_REMOTE_URL_ENV,
        LOCAL_REMOTE_OVERRIDE_URL_ENV, LOCAL_REMOTE_TOKEN_ENV,
    };

    fn env_map(entries: &[(&str, &str)]) -> HashMap<String, String> {
        entries
            .iter()
            .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
            .collect()
    }

    #[test]
    fn uses_default_url_when_env_is_missing() {
        let values = env_map(&[]);
        let resolved =
            resolve_local_workspace_endpoint_from(|key| values.get(key).cloned()).unwrap();

        assert_eq!(
            resolved.endpoint_url,
            format!("{DEFAULT_LOCAL_REMOTE_URL}/")
        );
        assert_eq!(resolved.token, None);
        assert_eq!(
            resolved.endpoint_source,
            WorkspaceEndpointSource::ManagedLoopback
        );
    }

    #[test]
    fn uses_override_url_and_trims_token() {
        let values = env_map(&[
            (LOCAL_REMOTE_OVERRIDE_URL_ENV, "https://dexdex.example/rpc"),
            (LOCAL_REMOTE_TOKEN_ENV, "  local-token  "),
        ]);

        let resolved =
            resolve_local_workspace_endpoint_from(|key| values.get(key).cloned()).unwrap();

        assert_eq!(resolved.endpoint_url, "https://dexdex.example/rpc");
        assert_eq!(resolved.token.as_deref(), Some("local-token"));
        assert_eq!(
            resolved.endpoint_source,
            WorkspaceEndpointSource::LocalOverride
        );
    }

    #[test]
    fn uses_legacy_override_url_env_for_compatibility() {
        let values = env_map(&[(LEGACY_LOCAL_REMOTE_URL_ENV, "https://dexdex.example/rpc")]);

        let resolved =
            resolve_local_workspace_endpoint_from(|key| values.get(key).cloned()).unwrap();

        assert_eq!(resolved.endpoint_url, "https://dexdex.example/rpc");
        assert_eq!(
            resolved.endpoint_source,
            WorkspaceEndpointSource::LocalOverride
        );
    }

    #[test]
    fn prefers_new_override_env_over_legacy_value() {
        let values = env_map(&[
            (LOCAL_REMOTE_OVERRIDE_URL_ENV, "https://primary.dexdex.example/rpc"),
            (LEGACY_LOCAL_REMOTE_URL_ENV, "https://legacy.dexdex.example/rpc"),
        ]);

        let resolved =
            resolve_local_workspace_endpoint_from(|key| values.get(key).cloned()).unwrap();

        assert_eq!(resolved.endpoint_url, "https://primary.dexdex.example/rpc");
        assert_eq!(
            resolved.endpoint_source,
            WorkspaceEndpointSource::LocalOverride
        );
    }

    #[test]
    fn rejects_invalid_url() {
        let values = env_map(&[(LOCAL_REMOTE_OVERRIDE_URL_ENV, "not-a-url")]);

        let error =
            resolve_local_workspace_endpoint_from(|key| values.get(key).cloned()).unwrap_err();

        assert_eq!(
            error,
            "DEXDEX_LOCAL_REMOTE_OVERRIDE_URL (or legacy DEXDEX_LOCAL_REMOTE_URL) must be a valid absolute URL."
        );
    }

    #[test]
    fn rejects_non_http_scheme() {
        let values = env_map(&[(LOCAL_REMOTE_OVERRIDE_URL_ENV, "ftp://localhost:7878")]);

        let error =
            resolve_local_workspace_endpoint_from(|key| values.get(key).cloned()).unwrap_err();

        assert_eq!(
            error,
            "DEXDEX_LOCAL_REMOTE_OVERRIDE_URL (or legacy DEXDEX_LOCAL_REMOTE_URL) must use http or https scheme."
        );
    }

    #[test]
    fn redacts_endpoint_credentials_and_query_for_logging() {
        let redacted =
            redact_endpoint_url_for_logs("https://user:pass@dexdex.example/rpc?token=abc#frag");
        assert_eq!(redacted, "https://dexdex.example/rpc");
    }

    #[test]
    fn redaction_marks_invalid_urls() {
        let redacted = redact_endpoint_url_for_logs("not-a-url");
        assert_eq!(redacted, "[invalid-endpoint-url]");
    }
}
