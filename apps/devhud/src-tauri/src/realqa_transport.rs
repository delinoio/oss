//! Closed native transport for the authenticated RealQA composer.
//!
//! The frontend selects a generated protobuf procedure, never a URL, method,
//! header, bearer, redirect policy, or arbitrary request target.

use std::time::Duration;

use base64::{Engine as _, engine::general_purpose::STANDARD};
use reqwest::{StatusCode, blocking::Client, redirect::Policy};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use url::Url;

use crate::{auth::AuthError, auth_native::NativeAuthState};

const REALQA_ORIGIN: &str = "https://realqa.deli.dev";
const REALQA_ASSET_HOST: &str = "assets.realqa.deli.dev";
const REALQA_GITHUB_CLIENT_ID_VARIABLE: &str = "DEVHUD_REALQA_GITHUB_APP_CLIENT_ID";
const REALQA_GITHUB_APP_SLUG_VARIABLE: &str = "DEVHUD_REALQA_GITHUB_APP_SLUG";
const REALQA_GITHUB_CALLBACK: &str = "https://realqa.deli.dev/github/oauth/callback";
const FORWARDED_USER_TOKEN_HEADER: &str = "X-Delibase-Forwarded-User-Token";
const MAX_PROTO_REQUEST_BYTES: usize = 1024 * 1024;
const MAX_PROTO_RESPONSE_BYTES: usize = 8 * 1024 * 1024;
const MAX_ERROR_DETAIL_BYTES: usize = 4 * 1024;
const MAX_IMAGE_BYTES: usize = 25 * 1024 * 1024;
const REALQA_ERROR_DETAIL_TYPE: &str = "devhud.realqa.v1.ErrorDetail";

#[derive(Debug, Clone, Copy, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum RealQaProcedure {
    ListPresets,
    GetPreset,
    CreatePreset,
    UpdatePreset,
    DeletePreset,
    DeleteFeatureData,
    GetGithubConnection,
    StartGithubConnection,
    ListGithubInstallations,
    DisconnectGithubConnection,
    ListRepositories,
    GetRepositoryIssueSchema,
    ListSubmissions,
    CreateSubmission,
    CreateImageUpload,
    FinalizeImageUpload,
    SubmitIssue,
    GetSubmission,
    RebindSubmissionStorageAuthorization,
    DeleteImage,
    DeleteSubmissionAssets,
}

impl RealQaProcedure {
    fn path(self) -> &'static str {
        match self {
            Self::ListPresets => "/devhud.realqa.v1.RealQAPresetService/ListPresets",
            Self::GetPreset => "/devhud.realqa.v1.RealQAPresetService/GetPreset",
            Self::CreatePreset => "/devhud.realqa.v1.RealQAPresetService/CreatePreset",
            Self::UpdatePreset => "/devhud.realqa.v1.RealQAPresetService/UpdatePreset",
            Self::DeletePreset => "/devhud.realqa.v1.RealQAPresetService/DeletePreset",
            Self::DeleteFeatureData => "/devhud.realqa.v1.RealQAPresetService/DeleteFeatureData",
            Self::GetGithubConnection => {
                "/devhud.realqa.v1.RealQATrackerService/GetGitHubConnection"
            }
            Self::StartGithubConnection => {
                "/devhud.realqa.v1.RealQATrackerService/StartGitHubConnection"
            }
            Self::ListGithubInstallations => {
                "/devhud.realqa.v1.RealQATrackerService/ListGitHubInstallations"
            }
            Self::DisconnectGithubConnection => {
                "/devhud.realqa.v1.RealQATrackerService/DisconnectGitHubConnection"
            }
            Self::ListRepositories => "/devhud.realqa.v1.RealQATrackerService/ListRepositories",
            Self::GetRepositoryIssueSchema => {
                "/devhud.realqa.v1.RealQATrackerService/GetRepositoryIssueSchema"
            }
            Self::ListSubmissions => "/devhud.realqa.v1.RealQASubmissionService/ListSubmissions",
            Self::CreateSubmission => "/devhud.realqa.v1.RealQASubmissionService/CreateSubmission",
            Self::CreateImageUpload => {
                "/devhud.realqa.v1.RealQASubmissionService/CreateImageUpload"
            }
            Self::FinalizeImageUpload => {
                "/devhud.realqa.v1.RealQASubmissionService/FinalizeImageUpload"
            }
            Self::SubmitIssue => "/devhud.realqa.v1.RealQASubmissionService/SubmitIssue",
            Self::GetSubmission => "/devhud.realqa.v1.RealQASubmissionService/GetSubmission",
            Self::RebindSubmissionStorageAuthorization => {
                "/devhud.realqa.v1.RealQASubmissionService/RebindSubmissionStorageAuthorization"
            }
            Self::DeleteImage => "/devhud.realqa.v1.RealQASubmissionService/DeleteImage",
            Self::DeleteSubmissionAssets => {
                "/devhud.realqa.v1.RealQASubmissionService/DeleteSubmissionAssets"
            }
        }
    }
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct RealQaConnectRequest {
    procedure: RealQaProcedure,
    body_base64: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct RealQaConnectResponse {
    body_base64: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct RealQaSignedPutRequest {
    signed_put_url: String,
    content_type: String,
    sha256: String,
    body_base64: String,
}

#[derive(Debug)]
pub(crate) struct RealQaGithubBrowserState {
    configuration: Option<RealQaGithubBrowserConfiguration>,
}

#[derive(Debug)]
struct RealQaGithubBrowserConfiguration {
    client_id: String,
    app_slug: String,
}

impl RealQaGithubBrowserState {
    pub(crate) fn initialize() -> Self {
        let configuration = std::env::var(REALQA_GITHUB_CLIENT_ID_VARIABLE)
            .ok()
            .zip(std::env::var(REALQA_GITHUB_APP_SLUG_VARIABLE).ok())
            .and_then(|(client_id, app_slug)| {
                RealQaGithubBrowserConfiguration::new(client_id, app_slug)
            });
        Self { configuration }
    }
}

impl RealQaGithubBrowserConfiguration {
    fn new(client_id: String, app_slug: String) -> Option<Self> {
        let client_id_valid = !client_id.is_empty()
            && client_id.len() <= 100
            && client_id
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || byte == b'.');
        let app_slug_valid = !app_slug.is_empty()
            && app_slug.len() <= 100
            && !app_slug.starts_with('-')
            && !app_slug.ends_with('-')
            && app_slug
                .bytes()
                .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-');
        (client_id_valid && app_slug_valid).then_some(Self {
            client_id,
            app_slug,
        })
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum RealQaTransportFailure {
    AuthenticationRequired,
    ReauthenticationRequired,
    InvalidRequest,
    RequestTooLarge,
    ResponseTooLarge,
    RateLimited,
    Conflict,
    BillingRequired,
    StaleRevision,
    ImageTooLarge,
    SessionTooLarge,
    DecodedImageTooLarge,
    FinalBodyTooLarge,
    UploadConcurrencyLimited,
    StorageBillingGrace,
    SubmissionAmbiguous,
    PublicImageConfirmationRequired,
    ServiceUnavailable,
    R2Unavailable,
    GithubUnavailable,
    UploadRejected,
    ConfigurationUnavailable,
    InvalidAuthorizationTarget,
    BrowserUnavailable,
}

fn map_auth_failure(error: AuthError) -> RealQaTransportFailure {
    match error {
        AuthError::ReauthenticationRequired
        | AuthError::TokenExpired
        | AuthError::TokenInvalid
        | AuthError::AudienceMismatch
        | AuthError::ScopeMismatch
        | AuthError::SubjectMismatch => RealQaTransportFailure::ReauthenticationRequired,
        _ => RealQaTransportFailure::AuthenticationRequired,
    }
}

fn client() -> Result<Client, RealQaTransportFailure> {
    Client::builder()
        .https_only(true)
        .redirect(Policy::none())
        .timeout(Duration::from_secs(30))
        .user_agent("DevHud/0.1 RealQA")
        .build()
        .map_err(|_| RealQaTransportFailure::ServiceUnavailable)
}

fn map_connect_status(status: StatusCode) -> RealQaTransportFailure {
    match status {
        StatusCode::UNAUTHORIZED => RealQaTransportFailure::ReauthenticationRequired,
        StatusCode::CONFLICT | StatusCode::PRECONDITION_FAILED => RealQaTransportFailure::Conflict,
        StatusCode::PAYMENT_REQUIRED => RealQaTransportFailure::BillingRequired,
        StatusCode::FAILED_DEPENDENCY => RealQaTransportFailure::GithubUnavailable,
        StatusCode::TOO_MANY_REQUESTS => RealQaTransportFailure::RateLimited,
        status if status.is_server_error() => RealQaTransportFailure::ServiceUnavailable,
        _ => RealQaTransportFailure::InvalidRequest,
    }
}

#[derive(Deserialize)]
struct ConnectErrorEnvelope {
    #[serde(default)]
    details: Vec<ConnectErrorDetail>,
}

#[derive(Deserialize)]
struct ConnectErrorDetail {
    #[serde(rename = "type")]
    detail_type: String,
    value: String,
}

fn decode_varint(bytes: &[u8], offset: &mut usize) -> Option<u64> {
    let mut value = 0_u64;
    for shift in (0..70).step_by(7) {
        let byte = *bytes.get(*offset)?;
        *offset += 1;
        if shift == 63 && byte > 1 {
            return None;
        }
        value |= u64::from(byte & 0x7f) << shift;
        if byte & 0x80 == 0 {
            return Some(value);
        }
    }
    None
}

// The native crate deliberately does not link a generated RealQA Rust client.
// Decode only ErrorDetail.reason (field 1) here; remove this bounded decoder if
// the closed native transport gains a generated Rust message boundary.
fn decode_error_reason(bytes: &[u8]) -> Option<u64> {
    let mut offset = 0;
    while offset < bytes.len() {
        let tag = decode_varint(bytes, &mut offset)?;
        let field = tag >> 3;
        let wire = tag & 0x07;
        if field == 1 && wire == 0 {
            return decode_varint(bytes, &mut offset);
        }
        match wire {
            0 => {
                decode_varint(bytes, &mut offset)?;
            }
            1 => offset = offset.checked_add(8)?,
            2 => {
                let length = usize::try_from(decode_varint(bytes, &mut offset)?).ok()?;
                offset = offset.checked_add(length)?;
            }
            5 => offset = offset.checked_add(4)?,
            _ => return None,
        }
        if offset > bytes.len() {
            return None;
        }
    }
    None
}

// Keep these closed numeric values synchronized with ErrorReason in
// protos/devhud-realqa/v1/common.proto.
fn map_error_reason(reason: u64) -> Option<RealQaTransportFailure> {
    match reason {
        1 => Some(RealQaTransportFailure::AuthenticationRequired),
        2 => Some(RealQaTransportFailure::ReauthenticationRequired),
        5 | 6 => Some(RealQaTransportFailure::StaleRevision),
        7 | 35 | 37 => Some(RealQaTransportFailure::Conflict),
        10 | 12..=14 => Some(RealQaTransportFailure::GithubUnavailable),
        15 => Some(RealQaTransportFailure::FinalBodyTooLarge),
        16 => Some(RealQaTransportFailure::ImageTooLarge),
        17 => Some(RealQaTransportFailure::SessionTooLarge),
        18 => Some(RealQaTransportFailure::DecodedImageTooLarge),
        22..=24 => Some(RealQaTransportFailure::UploadRejected),
        25 => Some(RealQaTransportFailure::SubmissionAmbiguous),
        26 => Some(RealQaTransportFailure::RateLimited),
        27 => Some(RealQaTransportFailure::UploadConcurrencyLimited),
        28..=32 => Some(RealQaTransportFailure::BillingRequired),
        33 => Some(RealQaTransportFailure::StorageBillingGrace),
        36 => Some(RealQaTransportFailure::PublicImageConfirmationRequired),
        _ => None,
    }
}

fn map_connect_error(status: StatusCode, body: &[u8]) -> RealQaTransportFailure {
    let typed = serde_json::from_slice::<ConnectErrorEnvelope>(body)
        .ok()
        .and_then(|error| {
            error.details.into_iter().find_map(|detail| {
                if detail.detail_type != REALQA_ERROR_DETAIL_TYPE {
                    return None;
                }
                let bytes = STANDARD.decode(detail.value).ok()?;
                if bytes.len() > MAX_ERROR_DETAIL_BYTES {
                    return None;
                }
                map_error_reason(decode_error_reason(&bytes)?)
            })
        });
    typed.unwrap_or_else(|| map_connect_status(status))
}

fn validate_github_authorization_target(
    target: &str,
    configuration: &RealQaGithubBrowserConfiguration,
) -> Result<Url, RealQaTransportFailure> {
    if target.len() > 2_048 || !target.starts_with("https://github.com/") {
        return Err(RealQaTransportFailure::InvalidAuthorizationTarget);
    }
    let raw_path = target
        .split(['?', '#'])
        .next()
        .unwrap_or_default()
        .strip_prefix("https://github.com")
        .unwrap_or_default()
        .to_ascii_lowercase();
    if raw_path.contains("%2f")
        || raw_path.contains("%5c")
        || raw_path.contains("%2e")
        || raw_path
            .split('/')
            .any(|segment| matches!(segment, "." | ".."))
    {
        return Err(RealQaTransportFailure::InvalidAuthorizationTarget);
    }
    let url = Url::parse(target).map_err(|_| RealQaTransportFailure::InvalidAuthorizationTarget)?;
    if url.scheme() != "https"
        || url.host_str() != Some("github.com")
        || url.port().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.fragment().is_some()
    {
        return Err(RealQaTransportFailure::InvalidAuthorizationTarget);
    }
    let query = url.query_pairs().collect::<Vec<_>>();
    let values = |name: &str| {
        query
            .iter()
            .filter_map(|(key, value)| (key == name).then_some(value.as_ref()))
            .collect::<Vec<_>>()
    };
    let states = values("state");
    if states.len() != 1
        || !(32..=2_048).contains(&states[0].len())
        || !states[0]
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'~' | b'-'))
    {
        return Err(RealQaTransportFailure::InvalidAuthorizationTarget);
    }
    let valid = if url.path() == "/login/oauth/authorize" {
        let client_ids = values("client_id");
        let callbacks = values("redirect_uri");
        query.len() == 3
            && client_ids.as_slice() == [configuration.client_id.as_str()]
            && callbacks.as_slice() == [REALQA_GITHUB_CALLBACK]
    } else {
        query.len() == 1
            && url.path() == format!("/apps/{}/installations/new", configuration.app_slug)
    };
    valid
        .then_some(url)
        .ok_or(RealQaTransportFailure::InvalidAuthorizationTarget)
}

pub(crate) fn open_github_authorization(
    target: &str,
    state: &RealQaGithubBrowserState,
    auth: &NativeAuthState,
) -> Result<(), RealQaTransportFailure> {
    auth.with_realqa_bearers(|_feature, _delibase, _subject| ())
        .map_err(map_auth_failure)?;
    let configuration = state
        .configuration
        .as_ref()
        .ok_or(RealQaTransportFailure::ConfigurationUnavailable)?;
    let target = validate_github_authorization_target(target, configuration)?;
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
        .map_err(|_| RealQaTransportFailure::BrowserUnavailable)
        .and_then(|status| {
            status
                .success()
                .then_some(())
                .ok_or(RealQaTransportFailure::BrowserUnavailable)
        })
}

pub(crate) fn connect(
    request: RealQaConnectRequest,
    auth: &NativeAuthState,
) -> Result<RealQaConnectResponse, RealQaTransportFailure> {
    let body = STANDARD
        .decode(request.body_base64)
        .map_err(|_| RealQaTransportFailure::InvalidRequest)?;
    if body.len() > MAX_PROTO_REQUEST_BYTES {
        return Err(RealQaTransportFailure::RequestTooLarge);
    }
    let endpoint = format!("{REALQA_ORIGIN}{}", request.procedure.path());
    auth.with_realqa_bearers(|feature, delibase, _subject| {
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
            .map_err(|_| RealQaTransportFailure::ServiceUnavailable)
    })
    .map_err(map_auth_failure)?
    .and_then(|response| {
        let status = response.status();
        let bytes = response
            .bytes()
            .map_err(|_| RealQaTransportFailure::ServiceUnavailable)?;
        if bytes.len() > MAX_PROTO_RESPONSE_BYTES {
            return Err(RealQaTransportFailure::ResponseTooLarge);
        }
        if !status.is_success() {
            return Err(map_connect_error(status, &bytes));
        }
        Ok(RealQaConnectResponse {
            body_base64: STANDARD.encode(bytes),
        })
    })
}

fn validate_signed_put(
    request: &RealQaSignedPutRequest,
) -> Result<(Url, Vec<u8>), RealQaTransportFailure> {
    let url =
        Url::parse(&request.signed_put_url).map_err(|_| RealQaTransportFailure::UploadRejected)?;
    if url.scheme() != "https"
        || url.host_str() != Some(REALQA_ASSET_HOST)
        || url.port().is_some()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.fragment().is_some()
        || !url.path().starts_with("/uploads/")
        || !matches!(request.content_type.as_str(), "image/png" | "image/webp")
        || request.sha256.len() != 64
        || !request
            .sha256
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
    {
        return Err(RealQaTransportFailure::UploadRejected);
    }
    let body = STANDARD
        .decode(&request.body_base64)
        .map_err(|_| RealQaTransportFailure::UploadRejected)?;
    if body.is_empty() || body.len() > MAX_IMAGE_BYTES {
        return Err(RealQaTransportFailure::RequestTooLarge);
    }
    let actual = format!("{:x}", Sha256::digest(&body));
    if actual != request.sha256 {
        return Err(RealQaTransportFailure::UploadRejected);
    }
    Ok((url, body))
}

pub(crate) fn signed_put(
    request: RealQaSignedPutRequest,
    auth: &NativeAuthState,
) -> Result<(), RealQaTransportFailure> {
    // A previously issued signed URL is not upload authority after logout or
    // while the retained session needs online reauthentication.
    auth.with_realqa_bearers(|_feature, _delibase, _subject| ())
        .map_err(map_auth_failure)?;
    let (url, body) = validate_signed_put(&request)?;
    let response = client()?
        .put(url)
        .header("Content-Type", request.content_type)
        .header("X-Realqa-Content-Sha256", request.sha256)
        .body(body)
        .send()
        .map_err(|_| RealQaTransportFailure::R2Unavailable)?;
    if response.status().is_success() {
        Ok(())
    } else if response.status() == StatusCode::TOO_MANY_REQUESTS {
        Err(RealQaTransportFailure::RateLimited)
    } else if response.status().is_server_error() {
        Err(RealQaTransportFailure::R2Unavailable)
    } else {
        Err(RealQaTransportFailure::UploadRejected)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn put_request(url: &str, body: &[u8]) -> RealQaSignedPutRequest {
        RealQaSignedPutRequest {
            signed_put_url: url.to_owned(),
            content_type: "image/png".to_owned(),
            sha256: format!("{:x}", Sha256::digest(body)),
            body_base64: STANDARD.encode(body),
        }
    }

    #[test]
    fn procedure_table_is_exact_and_complete() {
        let paths = [
            RealQaProcedure::ListPresets,
            RealQaProcedure::GetPreset,
            RealQaProcedure::CreatePreset,
            RealQaProcedure::UpdatePreset,
            RealQaProcedure::DeletePreset,
            RealQaProcedure::DeleteFeatureData,
            RealQaProcedure::GetGithubConnection,
            RealQaProcedure::StartGithubConnection,
            RealQaProcedure::ListGithubInstallations,
            RealQaProcedure::DisconnectGithubConnection,
            RealQaProcedure::ListRepositories,
            RealQaProcedure::GetRepositoryIssueSchema,
            RealQaProcedure::ListSubmissions,
            RealQaProcedure::CreateSubmission,
            RealQaProcedure::CreateImageUpload,
            RealQaProcedure::FinalizeImageUpload,
            RealQaProcedure::SubmitIssue,
            RealQaProcedure::GetSubmission,
            RealQaProcedure::RebindSubmissionStorageAuthorization,
            RealQaProcedure::DeleteImage,
            RealQaProcedure::DeleteSubmissionAssets,
        ]
        .map(RealQaProcedure::path);
        assert!(
            paths
                .iter()
                .all(|path| path.starts_with("/devhud.realqa.v1."))
        );
        assert_eq!(
            21,
            paths
                .iter()
                .collect::<std::collections::BTreeSet<_>>()
                .len()
        );
    }

    #[test]
    fn connect_errors_prefer_closed_realqa_error_details() {
        let error_body = |reason: u8| {
            serde_json::to_vec(&serde_json::json!({
                "code": "invalid_argument",
                "message": "invalid request",
                "details": [{
                    "type": REALQA_ERROR_DETAIL_TYPE,
                    "value": STANDARD.encode([0x08, reason]),
                }],
            }))
            .unwrap()
        };
        for (reason, expected) in [
            (5, RealQaTransportFailure::StaleRevision),
            (15, RealQaTransportFailure::FinalBodyTooLarge),
            (16, RealQaTransportFailure::ImageTooLarge),
            (17, RealQaTransportFailure::SessionTooLarge),
            (18, RealQaTransportFailure::DecodedImageTooLarge),
            (25, RealQaTransportFailure::SubmissionAmbiguous),
            (27, RealQaTransportFailure::UploadConcurrencyLimited),
            (33, RealQaTransportFailure::StorageBillingGrace),
            (36, RealQaTransportFailure::PublicImageConfirmationRequired),
        ] {
            assert_eq!(
                map_connect_error(StatusCode::BAD_REQUEST, &error_body(reason)),
                expected,
            );
        }
    }

    #[test]
    fn connect_errors_fall_back_to_status_for_untrusted_details() {
        for body in [
            br#"{"details": [{"type": "other.ErrorDetail", "value": "CA8="}]}"#.as_slice(),
            br#"{"details": [{"type": "devhud.realqa.v1.ErrorDetail", "value": "not-base64"}]}"#
                .as_slice(),
            br#"not-json"#.as_slice(),
        ] {
            assert_eq!(
                map_connect_error(StatusCode::TOO_MANY_REQUESTS, body),
                RealQaTransportFailure::RateLimited,
            );
        }
    }

    #[test]
    fn signed_put_accepts_only_exact_assets_origin_and_matching_bytes() {
        let body = b"fixture-png";
        assert!(
            validate_signed_put(&put_request(
                "https://assets.realqa.deli.dev/uploads/opaque?X-Amz-Signature=fixture",
                body,
            ))
            .is_ok()
        );
        for invalid in [
            "http://assets.realqa.deli.dev/uploads/opaque",
            "https://r2.example.com/uploads/opaque",
            "https://assets.realqa.deli.dev/public/opaque",
            "https://user@assets.realqa.deli.dev/uploads/opaque",
            "https://assets.realqa.deli.dev/uploads/opaque#fragment",
        ] {
            assert_eq!(
                validate_signed_put(&put_request(invalid, body)).unwrap_err(),
                RealQaTransportFailure::UploadRejected,
            );
        }
        let mut mismatch = put_request("https://assets.realqa.deli.dev/uploads/opaque", body);
        mismatch.sha256 = "0".repeat(64);
        assert_eq!(
            validate_signed_put(&mismatch).unwrap_err(),
            RealQaTransportFailure::UploadRejected,
        );
    }

    #[test]
    fn github_browser_handoff_accepts_only_the_configured_exact_shapes() {
        let configuration = RealQaGithubBrowserConfiguration::new(
            "Iv1.fixtureclient123".to_owned(),
            "fixture-realqa".to_owned(),
        )
        .unwrap();
        let state = "a".repeat(512);
        let callback = "https%3A%2F%2Frealqa.deli.dev%2Fgithub%2Foauth%2Fcallback";
        for valid in [
            format!(
                "https://github.com/login/oauth/authorize?client_id=Iv1.fixtureclient123&redirect_uri={}&state={state}",
                callback,
            ),
            format!(
                "https://github.com/apps/fixture-realqa/installations/new?state={state}"
            ),
        ] {
            assert!(validate_github_authorization_target(&valid, &configuration).is_ok());
        }
        for invalid in [
            format!("http://github.com/apps/fixture-realqa/installations/new?state={state}"),
            format!("https://user@github.com/apps/fixture-realqa/installations/new?state={state}"),
            format!("https://github.com/apps/other/installations/new?state={state}"),
            format!("https://github.com/apps/fixture-realqa/%2e%2e/installations/new?state={state}"),
            format!("https://github.com/apps/fixture-realqa/installations%2fnew?state={state}"),
            format!("https://github.com/apps/fixture-realqa/installations/new?state={state}#fragment"),
            "https://github.com/apps/fixture-realqa/installations/new?state=contains%20spaces-and-is-long-enough-123456".to_owned(),
            format!("https://github.com/login/oauth/authorize?client_id=other&redirect_uri={callback}&state={state}"),
            format!("https://github.com/login/oauth/authorize?client_id=Iv1.fixtureclient123&redirect_uri=https%3A%2F%2Fexample.com%2Fcallback&state={state}"),
        ] {
            assert_eq!(
                validate_github_authorization_target(&invalid, &configuration).unwrap_err(),
                RealQaTransportFailure::InvalidAuthorizationTarget,
            );
        }
    }
}
