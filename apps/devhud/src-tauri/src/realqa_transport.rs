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
const FORWARDED_USER_TOKEN_HEADER: &str = "X-Delibase-Forwarded-User-Token";
const MAX_PROTO_REQUEST_BYTES: usize = 1024 * 1024;
const MAX_PROTO_RESPONSE_BYTES: usize = 8 * 1024 * 1024;
const MAX_IMAGE_BYTES: usize = 25 * 1024 * 1024;

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
    ServiceUnavailable,
    R2Unavailable,
    GithubUnavailable,
    UploadRejected,
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
        if !response.status().is_success() {
            return Err(map_connect_status(response.status()));
        }
        let bytes = response
            .bytes()
            .map_err(|_| RealQaTransportFailure::ServiceUnavailable)?;
        if bytes.len() > MAX_PROTO_RESPONSE_BYTES {
            return Err(RealQaTransportFailure::ResponseTooLarge);
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
}
