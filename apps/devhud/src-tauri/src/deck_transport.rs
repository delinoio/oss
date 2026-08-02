//! Closed native Connect transport and GitHub pull-request handoff for Deck.
//!
//! Frontend callers select an exact generated procedure and protobuf body. A
//! URL, method, header, bearer, or arbitrary browser target never crosses IPC.

#![cfg_attr(test, allow(dead_code))]

use std::time::Duration;

use base64::{
    Engine as _,
    engine::general_purpose::{STANDARD, STANDARD_NO_PAD},
};
use reqwest::{StatusCode, blocking::Client, redirect::Policy};
use serde::{Deserialize, Serialize};
#[cfg(any(target_os = "android", target_os = "ios"))]
use tauri_plugin_devhud_auth::DevHudAuthBridgeExt;
use url::Url;

use crate::{auth::AuthError, auth_native::NativeAuthState};

const DECK_ORIGIN: &str = "https://deck.deli.dev";
const FORWARDED_USER_TOKEN_HEADER: &str = "X-Devhud-Deck-Forwarded-Delibase-Token";
const MAX_PROTO_REQUEST_BYTES: usize = 1024 * 1024;
const MAX_PROTO_RESPONSE_BYTES: usize = 8 * 1024 * 1024;
const MAX_CONNECT_ERROR_BYTES: usize = 64 * 1024;
const MAX_ERROR_DETAIL_BYTES: usize = 4 * 1024;
const DECK_ERROR_DETAIL_TYPE: &str = "devhud.deck.v1.ErrorDetail";

#[derive(Debug, Clone, Copy, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum DeckProcedure {
    ListOwners,
    ListViews,
    GetView,
    CreateView,
    UpdateView,
    DeleteView,
    ListPullRequests,
    ListPullRequestMutationCandidates,
    GetRefreshPreflight,
    RefreshView,
    MutatePullRequest,
    GetDevice,
    RegisterDevice,
    UpdateDevice,
    UnregisterDevice,
    ResolveNotificationEvent,
}

impl DeckProcedure {
    fn path(self) -> &'static str {
        match self {
            Self::ListOwners => "/devhud.deck.v1.DeckViewService/ListOwners",
            Self::ListViews => "/devhud.deck.v1.DeckViewService/ListViews",
            Self::GetView => "/devhud.deck.v1.DeckViewService/GetView",
            Self::CreateView => "/devhud.deck.v1.DeckViewService/CreateView",
            Self::UpdateView => "/devhud.deck.v1.DeckViewService/UpdateView",
            Self::DeleteView => "/devhud.deck.v1.DeckViewService/DeleteView",
            Self::ListPullRequests => "/devhud.deck.v1.DeckViewService/ListPullRequests",
            Self::ListPullRequestMutationCandidates => {
                "/devhud.deck.v1.DeckViewService/ListPullRequestMutationCandidates"
            }
            Self::GetRefreshPreflight => "/devhud.deck.v1.DeckViewService/GetRefreshPreflight",
            Self::RefreshView => "/devhud.deck.v1.DeckViewService/RefreshView",
            Self::MutatePullRequest => "/devhud.deck.v1.DeckViewService/MutatePullRequest",
            Self::GetDevice => "/devhud.deck.v1.DeckDeviceService/GetDevice",
            Self::RegisterDevice => "/devhud.deck.v1.DeckDeviceService/RegisterDevice",
            Self::UpdateDevice => "/devhud.deck.v1.DeckDeviceService/UpdateDevice",
            Self::UnregisterDevice => "/devhud.deck.v1.DeckDeviceService/UnregisterDevice",
            Self::ResolveNotificationEvent => {
                "/devhud.deck.v1.DeckDeviceService/ResolveNotificationEvent"
            }
        }
    }
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct DeckConnectRequest {
    procedure: DeckProcedure,
    body_base64: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct DeckConnectResponse {
    body_base64: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum DeckTransportFailure {
    AuthenticationRequired,
    ReauthenticationRequired,
    InvalidRequest,
    RequestTooLarge,
    ResponseTooLarge,
    PermissionDenied,
    RateLimited,
    Conflict,
    BillingUnavailable,
    ProviderUnavailable,
    ServiceUnavailable,
    InvalidPullRequest,
    BrowserUnavailable,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct DeckConnectFailure {
    code: DeckTransportFailure,
    #[serde(skip_serializing_if = "Option::is_none")]
    detail_body_base64: Option<String>,
}

impl From<DeckTransportFailure> for DeckConnectFailure {
    fn from(code: DeckTransportFailure) -> Self {
        Self {
            code,
            detail_body_base64: None,
        }
    }
}

#[derive(Debug, Deserialize)]
struct ConnectErrorBody {
    #[serde(default)]
    details: Vec<ConnectErrorDetail>,
}

#[derive(Debug, Deserialize)]
struct ConnectErrorDetail {
    #[serde(rename = "type")]
    type_name: String,
    value: String,
}

fn map_auth_failure(error: AuthError) -> DeckTransportFailure {
    match error {
        AuthError::ReauthenticationRequired
        | AuthError::TokenExpired
        | AuthError::TokenInvalid
        | AuthError::AudienceMismatch
        | AuthError::ScopeMismatch
        | AuthError::SubjectMismatch => DeckTransportFailure::ReauthenticationRequired,
        _ => DeckTransportFailure::AuthenticationRequired,
    }
}

fn client() -> Result<Client, DeckTransportFailure> {
    Client::builder()
        .https_only(true)
        .redirect(Policy::none())
        .timeout(Duration::from_secs(30))
        .user_agent("DevHud/0.1 Deck")
        .build()
        .map_err(|_| DeckTransportFailure::ServiceUnavailable)
}

fn map_status(status: StatusCode) -> DeckTransportFailure {
    match status {
        StatusCode::UNAUTHORIZED => DeckTransportFailure::ReauthenticationRequired,
        StatusCode::FORBIDDEN => DeckTransportFailure::PermissionDenied,
        StatusCode::CONFLICT | StatusCode::PRECONDITION_FAILED => DeckTransportFailure::Conflict,
        StatusCode::PAYMENT_REQUIRED => DeckTransportFailure::BillingUnavailable,
        StatusCode::FAILED_DEPENDENCY => DeckTransportFailure::ProviderUnavailable,
        StatusCode::TOO_MANY_REQUESTS => DeckTransportFailure::RateLimited,
        status if status.is_server_error() => DeckTransportFailure::ServiceUnavailable,
        _ => DeckTransportFailure::InvalidRequest,
    }
}

fn connect_failure(status: StatusCode, body: &[u8]) -> DeckConnectFailure {
    let detail_body_base64 = (body.len() <= MAX_CONNECT_ERROR_BYTES)
        .then(|| serde_json::from_slice::<ConnectErrorBody>(body).ok())
        .flatten()
        .and_then(|error| {
            error.details.into_iter().find_map(|detail| {
                if detail.type_name != DECK_ERROR_DETAIL_TYPE {
                    return None;
                }
                let bytes = STANDARD_NO_PAD.decode(detail.value).ok()?;
                (bytes.len() <= MAX_ERROR_DETAIL_BYTES).then(|| STANDARD.encode(bytes))
            })
        });
    DeckConnectFailure {
        code: map_status(status),
        detail_body_base64,
    }
}

pub(crate) fn connect(
    request: DeckConnectRequest,
    auth: &NativeAuthState,
) -> Result<DeckConnectResponse, DeckConnectFailure> {
    let body = STANDARD
        .decode(request.body_base64)
        .map_err(|_| DeckConnectFailure::from(DeckTransportFailure::InvalidRequest))?;
    if body.len() > MAX_PROTO_REQUEST_BYTES {
        return Err(DeckTransportFailure::RequestTooLarge.into());
    }
    let endpoint = format!("{DECK_ORIGIN}{}", request.procedure.path());
    auth.with_deck_bearers(|feature, delibase, _subject| {
        client()?
            .post(endpoint)
            .header("Authorization", format!("Bearer {feature}"))
            .header(FORWARDED_USER_TOKEN_HEADER, delibase)
            .header("Content-Type", "application/proto")
            .header("Accept", "application/proto")
            .header("Connect-Protocol-Version", "1")
            .header("Cache-Control", "no-store")
            .body(body)
            .send()
            .map_err(|_| DeckTransportFailure::ServiceUnavailable)
    })
    .map_err(map_auth_failure)
    .map_err(DeckConnectFailure::from)?
    .map_err(DeckConnectFailure::from)
    .and_then(|response| {
        let status = response.status();
        let bytes = response
            .bytes()
            .map_err(|_| DeckConnectFailure::from(DeckTransportFailure::ServiceUnavailable))?;
        if !status.is_success() {
            return Err(connect_failure(status, &bytes));
        }
        if bytes.len() > MAX_PROTO_RESPONSE_BYTES {
            return Err(DeckTransportFailure::ResponseTooLarge.into());
        }
        Ok(DeckConnectResponse {
            body_base64: STANDARD.encode(bytes),
        })
    })
}

fn pull_request_url(
    owner: &str,
    repository: &str,
    number: u64,
) -> Result<Url, DeckTransportFailure> {
    let segment_valid = |value: &str| {
        !value.is_empty()
            && value.len() <= 100
            && !matches!(value, "." | "..")
            && value
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.'))
    };
    if !segment_valid(owner) || !segment_valid(repository) || number == 0 {
        return Err(DeckTransportFailure::InvalidPullRequest);
    }
    Url::parse(&format!(
        "https://github.com/{owner}/{repository}/pull/{number}"
    ))
    .map_err(|_| DeckTransportFailure::InvalidPullRequest)
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
pub(crate) fn open_pull_request(
    owner: &str,
    repository: &str,
    number: u64,
    auth: &NativeAuthState,
) -> Result<(), DeckTransportFailure> {
    auth.with_deck_bearers(|_feature, _delibase, _subject| ())
        .map_err(map_auth_failure)?;
    let target = pull_request_url(owner, repository, number)?;
    #[cfg(target_os = "macos")]
    let status = std::process::Command::new("/usr/bin/open")
        .arg(target.as_str())
        .status();
    #[cfg(target_os = "linux")]
    let status = std::process::Command::new("xdg-open")
        .arg(target.as_str())
        .status();
    #[cfg(target_os = "windows")]
    let status = std::process::Command::new("explorer.exe")
        .arg(target.as_str())
        .status();
    status
        .map_err(|_| DeckTransportFailure::BrowserUnavailable)
        .and_then(|status| {
            status
                .success()
                .then_some(())
                .ok_or(DeckTransportFailure::BrowserUnavailable)
        })
}

#[cfg(any(target_os = "android", target_os = "ios"))]
pub(crate) fn open_pull_request(
    app: &tauri::AppHandle<crate::ActiveRuntime>,
    owner: &str,
    repository: &str,
    number: u64,
    auth: &NativeAuthState,
) -> Result<(), DeckTransportFailure> {
    auth.with_deck_bearers(|_feature, _delibase, _subject| ())
        .map_err(map_auth_failure)?;
    let target = pull_request_url(owner, repository, number)?;
    app.devhud_auth_bridge()
        .open_pull_request(target.as_str().to_owned())
        .map_err(|_| DeckTransportFailure::BrowserUnavailable)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn procedure_table_is_exact_and_complete() {
        let paths = [
            DeckProcedure::ListOwners,
            DeckProcedure::ListViews,
            DeckProcedure::GetView,
            DeckProcedure::CreateView,
            DeckProcedure::UpdateView,
            DeckProcedure::DeleteView,
            DeckProcedure::ListPullRequests,
            DeckProcedure::ListPullRequestMutationCandidates,
            DeckProcedure::GetRefreshPreflight,
            DeckProcedure::RefreshView,
            DeckProcedure::MutatePullRequest,
            DeckProcedure::GetDevice,
            DeckProcedure::RegisterDevice,
            DeckProcedure::UpdateDevice,
            DeckProcedure::UnregisterDevice,
            DeckProcedure::ResolveNotificationEvent,
        ]
        .map(DeckProcedure::path);
        assert!(
            paths
                .iter()
                .all(|path| path.starts_with("/devhud.deck.v1."))
        );
        assert_eq!(
            16,
            paths
                .iter()
                .collect::<std::collections::BTreeSet<_>>()
                .len()
        );
    }

    #[test]
    fn pull_request_handoff_builds_only_the_exact_github_shape() {
        assert_eq!(
            pull_request_url("delinoio", "oss", 755).unwrap().as_str(),
            "https://github.com/delinoio/oss/pull/755"
        );
        for (owner, repository, number) in [
            ("", "oss", 1),
            ("delinoio", "../oss", 1),
            ("delinoio", "oss/path", 1),
            ("delinoio", "oss", 0),
        ] {
            assert_eq!(
                pull_request_url(owner, repository, number).unwrap_err(),
                DeckTransportFailure::InvalidPullRequest
            );
        }
    }

    #[test]
    fn connect_failure_preserves_only_the_typed_bounded_deck_detail() {
        let detail = STANDARD_NO_PAD.encode([0x08, 0x15, 0x2a, 0x02, 0x08, 0x1e]);
        let body = format!(
            r#"{{"code":"resource_exhausted","details":[{{"type":"{DECK_ERROR_DETAIL_TYPE}","value":"{detail}"}}]}}"#,
        );

        let failure = connect_failure(StatusCode::TOO_MANY_REQUESTS, body.as_bytes());

        assert_eq!(failure.code, DeckTransportFailure::RateLimited);
        assert_eq!(
            failure.detail_body_base64,
            Some(STANDARD.encode([0x08, 0x15, 0x2a, 0x02, 0x08, 0x1e]))
        );
    }

    #[test]
    fn connect_failure_drops_unknown_and_malformed_details() {
        for body in [
            br#"{"details":[{"type":"other.ErrorDetail","value":"CBU"}]}"#.as_slice(),
            br#"{"details":[{"type":"devhud.deck.v1.ErrorDetail","value":"!"}]}"#.as_slice(),
            br#"not-json"#.as_slice(),
        ] {
            assert_eq!(
                connect_failure(StatusCode::FORBIDDEN, body).detail_body_base64,
                None
            );
        }
    }
}
