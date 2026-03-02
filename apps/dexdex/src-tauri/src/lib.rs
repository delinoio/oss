use std::sync::Once;

use serde::Serialize;
use tracing::{info, warn};
use url::Url;

const DEFAULT_LOCAL_REMOTE_URL: &str = "http://127.0.0.1:7878";
const LOCAL_REMOTE_URL_ENV: &str = "DEXDEX_LOCAL_REMOTE_URL";
const LOCAL_REMOTE_TOKEN_ENV: &str = "DEXDEX_LOCAL_REMOTE_TOKEN";

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum WorkspaceEndpointSource {
    ManagedLoopback,
}

impl WorkspaceEndpointSource {
    fn as_str(&self) -> &'static str {
        match self {
            Self::ManagedLoopback => "MANAGED_LOOPBACK",
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
    let parsed = Url::parse(raw_url)
        .map_err(|_| "DEXDEX_LOCAL_REMOTE_URL must be a valid absolute URL.".to_owned())?;

    match parsed.scheme() {
        "http" | "https" => Ok(parsed.to_string()),
        _ => Err("DEXDEX_LOCAL_REMOTE_URL must use http or https scheme.".to_owned()),
    }
}

fn resolve_local_workspace_endpoint_from<F>(
    mut get_env: F,
) -> Result<LocalWorkspaceEndpoint, String>
where
    F: FnMut(&str) -> Option<String>,
{
    let endpoint_url = get_env(LOCAL_REMOTE_URL_ENV)
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| DEFAULT_LOCAL_REMOTE_URL.to_owned());

    let token = normalize_optional_token(get_env(LOCAL_REMOTE_TOKEN_ENV));
    let endpoint_url = parse_and_normalize_url(&endpoint_url)?;

    Ok(LocalWorkspaceEndpoint {
        endpoint_url,
        token,
        endpoint_source: WorkspaceEndpointSource::ManagedLoopback,
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
            info!(
                workspace_mode = "LOCAL",
                endpoint_source = endpoint.endpoint_source.as_str(),
                endpoint_url = endpoint.endpoint_url,
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
        .invoke_handler(tauri::generate_handler![
            commands::resolve_local_workspace_endpoint
        ])
        .run(tauri::generate_context!())
        .expect("failed to run dexdex desktop app");
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use super::{
        resolve_local_workspace_endpoint_from, WorkspaceEndpointSource, DEFAULT_LOCAL_REMOTE_URL,
        LOCAL_REMOTE_TOKEN_ENV, LOCAL_REMOTE_URL_ENV,
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
    fn uses_env_url_and_trims_token() {
        let values = env_map(&[
            (LOCAL_REMOTE_URL_ENV, "https://dexdex.example/rpc"),
            (LOCAL_REMOTE_TOKEN_ENV, "  local-token  "),
        ]);

        let resolved =
            resolve_local_workspace_endpoint_from(|key| values.get(key).cloned()).unwrap();

        assert_eq!(resolved.endpoint_url, "https://dexdex.example/rpc");
        assert_eq!(resolved.token.as_deref(), Some("local-token"));
    }

    #[test]
    fn rejects_invalid_url() {
        let values = env_map(&[(LOCAL_REMOTE_URL_ENV, "not-a-url")]);

        let error =
            resolve_local_workspace_endpoint_from(|key| values.get(key).cloned()).unwrap_err();

        assert_eq!(
            error,
            "DEXDEX_LOCAL_REMOTE_URL must be a valid absolute URL."
        );
    }

    #[test]
    fn rejects_non_http_scheme() {
        let values = env_map(&[(LOCAL_REMOTE_URL_ENV, "ftp://localhost:7878")]);

        let error =
            resolve_local_workspace_endpoint_from(|key| values.get(key).cloned()).unwrap_err();

        assert_eq!(
            error,
            "DEXDEX_LOCAL_REMOTE_URL must use http or https scheme."
        );
    }
}
