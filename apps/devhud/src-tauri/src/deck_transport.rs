//! Closed native Connect transport and GitHub pull-request handoff for Deck.
//!
//! Frontend callers select an exact generated procedure and protobuf body. A
//! URL, method, header, bearer, or arbitrary browser target never crosses IPC.

#![cfg_attr(test, allow(dead_code))]

#[cfg(not(any(target_os = "android", target_os = "ios")))]
use std::sync::{Mutex, MutexGuard};
use std::time::Duration;

use base64::{
    Engine as _,
    engine::general_purpose::{STANDARD, STANDARD_NO_PAD},
};
use prost::Message;
use reqwest::{StatusCode, blocking::Client, redirect::Policy};
use serde::{Deserialize, Serialize};
#[cfg(any(target_os = "android", target_os = "ios"))]
use tauri_plugin_devhud_auth::DevHudAuthBridgeExt;
use url::Url;
use uuid::Uuid;
use zeroize::{Zeroize, ZeroizeOnDrop, Zeroizing};

use crate::{auth::AuthError, auth_native::NativeAuthState};

const DECK_ORIGIN: &str = "https://deck.deli.dev";
const FORWARDED_USER_TOKEN_HEADER: &str = "X-Devhud-Deck-Forwarded-Delibase-Token";
const DEVICE_REVOCATION_GRANT_HEADER: &str = "x-devhud-deck-device-revocation-grant";
#[cfg(not(any(target_os = "android", target_os = "ios")))]
const DEVICE_VAULT_SERVICE: &str = "dev.deli.devhud.deck-device";
#[cfg(not(any(target_os = "android", target_os = "ios")))]
const DEVICE_VAULT_ACCOUNT: &str = "active-registration";
const MAX_DEVICE_REVOCATION_GRANT_BYTES: usize = 256;
const MAX_PROTO_REQUEST_BYTES: usize = 1024 * 1024;
const MAX_PROTO_RESPONSE_BYTES: usize = 8 * 1024 * 1024;
const MAX_CONNECT_ERROR_BYTES: usize = 64 * 1024;
const MAX_ERROR_DETAIL_BYTES: usize = 4 * 1024;
const DECK_ERROR_DETAIL_TYPE: &str = "devhud.deck.v1.ErrorDetail";
#[cfg(not(any(target_os = "android", target_os = "ios")))]
static DEVICE_REGISTRATION_LIFECYCLE: Mutex<()> = Mutex::new(());

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
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

#[derive(Clone, PartialEq, Message)]
struct RegisterDeviceResponseMetadata {
    #[prost(message, optional, tag = "1")]
    registration: Option<DeviceRegistrationMetadata>,
}

#[derive(Clone, PartialEq, Message)]
struct DeviceRegistrationMetadata {
    #[prost(message, optional, tag = "1")]
    registration_id: Option<UuidV7Metadata>,
    #[prost(message, optional, tag = "3")]
    lease_expires_at: Option<TimestampMetadata>,
}

#[derive(Clone, PartialEq, Message)]
struct UuidV7Metadata {
    #[prost(string, tag = "1")]
    value: String,
}

#[derive(Clone, Copy, PartialEq, Message)]
struct TimestampMetadata {
    #[prost(int64, tag = "1")]
    seconds: i64,
    #[prost(int32, tag = "2")]
    nanos: i32,
}

#[derive(Deserialize, Serialize, Zeroize, ZeroizeOnDrop)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct RetainedDeckDeviceRegistration {
    registration_id: String,
    lease_expires_at_unix_seconds: i64,
    revocation_grant: String,
    cleanup_pending: bool,
}

fn retained_device_registration(
    body: &[u8],
    revocation_grant: &str,
) -> Result<RetainedDeckDeviceRegistration, DeckTransportFailure> {
    if revocation_grant.is_empty()
        || revocation_grant.len() > MAX_DEVICE_REVOCATION_GRANT_BYTES
        || !revocation_grant
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
    {
        return Err(DeckTransportFailure::ServiceUnavailable);
    }
    let response = RegisterDeviceResponseMetadata::decode(body)
        .map_err(|_| DeckTransportFailure::ServiceUnavailable)?;
    let registration = response
        .registration
        .ok_or(DeckTransportFailure::ServiceUnavailable)?;
    let registration_id = registration
        .registration_id
        .map(|id| id.value)
        .filter(|id| Uuid::parse_str(id).is_ok_and(|parsed| parsed.get_version_num() == 7))
        .ok_or(DeckTransportFailure::ServiceUnavailable)?;
    let lease_expires_at_unix_seconds = registration
        .lease_expires_at
        .filter(|lease| lease.seconds > 0 && (0..1_000_000_000).contains(&lease.nanos))
        .map(|lease| lease.seconds)
        .ok_or(DeckTransportFailure::ServiceUnavailable)?;
    Ok(RetainedDeckDeviceRegistration {
        registration_id,
        lease_expires_at_unix_seconds,
        revocation_grant: revocation_grant.to_owned(),
        cleanup_pending: false,
    })
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
fn replace_retained_device_registration(
    retained: &RetainedDeckDeviceRegistration,
) -> Result<(), DeckTransportFailure> {
    let encoded = Zeroizing::new(
        serde_json::to_string(&retained).map_err(|_| DeckTransportFailure::ServiceUnavailable)?,
    );
    keyring::Entry::new(DEVICE_VAULT_SERVICE, DEVICE_VAULT_ACCOUNT)
        .map_err(|_| DeckTransportFailure::ServiceUnavailable)?
        .set_password(&encoded)
        .map_err(|_| DeckTransportFailure::ServiceUnavailable)
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
fn load_retained_device_registration()
-> Result<Option<RetainedDeckDeviceRegistration>, DeckTransportFailure> {
    let entry = keyring::Entry::new(DEVICE_VAULT_SERVICE, DEVICE_VAULT_ACCOUNT)
        .map_err(|_| DeckTransportFailure::ServiceUnavailable)?;
    let encoded = match entry.get_password() {
        Ok(encoded) => Zeroizing::new(encoded),
        Err(keyring::Error::NoEntry) => return Ok(None),
        Err(_) => return Err(DeckTransportFailure::ServiceUnavailable),
    };
    serde_json::from_str(&encoded)
        .map(Some)
        .map_err(|_| DeckTransportFailure::ServiceUnavailable)
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
fn clear_retained_device_registration() -> Result<(), DeckTransportFailure> {
    let entry = keyring::Entry::new(DEVICE_VAULT_SERVICE, DEVICE_VAULT_ACCOUNT)
        .map_err(|_| DeckTransportFailure::ServiceUnavailable)?;
    match entry.delete_credential() {
        Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
        Err(_) => Err(DeckTransportFailure::ServiceUnavailable),
    }
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
fn retain_device_registration(
    body: &[u8],
    revocation_grant: &str,
) -> Result<(), DeckTransportFailure> {
    replace_retained_device_registration(&retained_device_registration(body, revocation_grant)?)
}

#[cfg(any(target_os = "android", target_os = "ios"))]
fn retain_device_registration(
    _body: &[u8],
    _revocation_grant: &str,
) -> Result<(), DeckTransportFailure> {
    Err(DeckTransportFailure::ServiceUnavailable)
}

#[derive(Clone, PartialEq, Message)]
struct UnregisterDeviceRequestMetadata {
    #[prost(message, optional, tag = "1")]
    registration_id: Option<UuidV7Metadata>,
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
fn unregister_retained_device(
    retained: &RetainedDeckDeviceRegistration,
) -> Result<(), DeckTransportFailure> {
    let body = UnregisterDeviceRequestMetadata {
        registration_id: Some(UuidV7Metadata {
            value: retained.registration_id.clone(),
        }),
    }
    .encode_to_vec();
    let response = client()?
        .post(format!(
            "{DECK_ORIGIN}{}",
            DeckProcedure::UnregisterDevice.path()
        ))
        .header(DEVICE_REVOCATION_GRANT_HEADER, &retained.revocation_grant)
        .header("Content-Type", "application/proto")
        .header("Accept", "application/proto")
        .header("Connect-Protocol-Version", "1")
        .header("Cache-Control", "no-store")
        .body(body)
        .send()
        .map_err(|_| DeckTransportFailure::ServiceUnavailable)?;
    if response.status().is_success() {
        Ok(())
    } else {
        Err(map_status(response.status()))
    }
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
fn cleanup_pending_device_registration() -> Result<(), DeckTransportFailure> {
    let Some(retained) = load_retained_device_registration()? else {
        return Ok(());
    };
    if !retained.cleanup_pending {
        return Ok(());
    }
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_err(|_| DeckTransportFailure::ServiceUnavailable)?
        .as_secs();
    if u64::try_from(retained.lease_expires_at_unix_seconds).is_ok_and(|expiry| expiry <= now) {
        return clear_retained_device_registration();
    }
    unregister_retained_device(&retained)?;
    clear_retained_device_registration()
}

#[cfg(any(target_os = "android", target_os = "ios"))]
fn cleanup_pending_device_registration() -> Result<(), DeckTransportFailure> {
    Ok(())
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
pub(crate) struct DeviceAuthClearGuard {
    _registration: MutexGuard<'static, ()>,
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
fn lock_device_registration_lifecycle() -> Result<MutexGuard<'static, ()>, DeckTransportFailure> {
    DEVICE_REGISTRATION_LIFECYCLE
        .lock()
        .map_err(|_| DeckTransportFailure::ServiceUnavailable)
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
pub(crate) fn prepare_device_auth_clear() -> Result<DeviceAuthClearGuard, DeckTransportFailure> {
    let registration = lock_device_registration_lifecycle()?;
    if let Some(mut retained) = load_retained_device_registration()? {
        if unregister_retained_device(&retained).is_ok() {
            clear_retained_device_registration()?;
        } else {
            tracing::warn!("Deck device cleanup remains pending after authentication clears");
            retained.cleanup_pending = true;
            replace_retained_device_registration(&retained)?;
        }
    }
    Ok(DeviceAuthClearGuard {
        _registration: registration,
    })
}

#[cfg(any(target_os = "android", target_os = "ios"))]
pub(crate) struct DeviceAuthClearGuard;

#[cfg(any(target_os = "android", target_os = "ios"))]
pub(crate) fn prepare_device_auth_clear() -> Result<DeviceAuthClearGuard, DeckTransportFailure> {
    Ok(DeviceAuthClearGuard)
}

fn map_auth_failure(error: AuthError) -> DeckTransportFailure {
    match error {
        AuthError::TransportUnavailable => DeckTransportFailure::ServiceUnavailable,
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
                let bytes = STANDARD
                    .decode(&detail.value)
                    .or_else(|_| STANDARD_NO_PAD.decode(&detail.value))
                    .ok()?;
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
    #[cfg(not(any(target_os = "android", target_os = "ios")))]
    let _device_registration = (request.procedure == DeckProcedure::RegisterDevice)
        .then(lock_device_registration_lifecycle)
        .transpose()
        .map_err(DeckConnectFailure::from)?;
    if request.procedure == DeckProcedure::RegisterDevice {
        cleanup_pending_device_registration().map_err(DeckConnectFailure::from)?;
    }
    let endpoint = format!("{DECK_ORIGIN}{}", request.procedure.path());
    let procedure = request.procedure;
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
        let revocation_grant = (procedure == DeckProcedure::RegisterDevice && status.is_success())
            .then(|| {
                response
                    .headers()
                    .get(DEVICE_REVOCATION_GRANT_HEADER)
                    .ok_or(DeckTransportFailure::ServiceUnavailable)?
                    .to_str()
                    .map(|grant| Zeroizing::new(grant.to_owned()))
                    .map_err(|_| DeckTransportFailure::ServiceUnavailable)
            })
            .transpose()
            .map_err(DeckConnectFailure::from)?;
        let bytes = response
            .bytes()
            .map_err(|_| DeckConnectFailure::from(DeckTransportFailure::ServiceUnavailable))?;
        if !status.is_success() {
            return Err(connect_failure(status, &bytes));
        }
        if bytes.len() > MAX_PROTO_RESPONSE_BYTES {
            return Err(DeckTransportFailure::ResponseTooLarge.into());
        }
        if let Some(revocation_grant) = revocation_grant {
            retain_device_registration(&bytes, &revocation_grant)
                .map_err(DeckConnectFailure::from)?;
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
    fn retryable_auth_transport_failures_remain_retryable() {
        assert_eq!(
            map_auth_failure(AuthError::TransportUnavailable),
            DeckTransportFailure::ServiceUnavailable
        );
    }

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
        let bytes = [0x08, 0x15];
        for detail in [STANDARD.encode(bytes), STANDARD_NO_PAD.encode(bytes)] {
            let body = format!(
                r#"{{"code":"resource_exhausted","details":[{{"type":"{DECK_ERROR_DETAIL_TYPE}","value":"{detail}"}}]}}"#,
            );

            let failure = connect_failure(StatusCode::TOO_MANY_REQUESTS, body.as_bytes());

            assert_eq!(failure.code, DeckTransportFailure::RateLimited);
            assert_eq!(failure.detail_body_base64, Some(STANDARD.encode(bytes)));
        }
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

    #[test]
    fn register_device_metadata_is_bounded_before_vaulting() {
        let body = RegisterDeviceResponseMetadata {
            registration: Some(DeviceRegistrationMetadata {
                registration_id: Some(UuidV7Metadata {
                    value: "018f0000-0000-7000-8000-000000000001".into(),
                }),
                lease_expires_at: Some(TimestampMetadata {
                    seconds: 1_800_000_000,
                    nanos: 0,
                }),
            }),
        }
        .encode_to_vec();

        let retained =
            retained_device_registration(&body, "abcdEFGHijklMNOPqrstUVWXyz0123456789_-abcd")
                .unwrap();

        assert_eq!(
            retained.registration_id,
            "018f0000-0000-7000-8000-000000000001"
        );
        assert_eq!(retained.lease_expires_at_unix_seconds, 1_800_000_000);
    }

    #[test]
    fn register_device_metadata_rejects_missing_or_unbounded_values() {
        assert!(matches!(
            retained_device_registration(&[], "grant"),
            Err(DeckTransportFailure::ServiceUnavailable)
        ));
        let body = RegisterDeviceResponseMetadata {
            registration: Some(DeviceRegistrationMetadata {
                registration_id: Some(UuidV7Metadata {
                    value: "018f0000-0000-7000-8000-000000000001".into(),
                }),
                lease_expires_at: Some(TimestampMetadata {
                    seconds: 1_800_000_000,
                    nanos: 0,
                }),
            }),
        }
        .encode_to_vec();
        assert!(matches!(
            retained_device_registration(&body, &"a".repeat(MAX_DEVICE_REVOCATION_GRANT_BYTES + 1)),
            Err(DeckTransportFailure::ServiceUnavailable)
        ));
    }
}
