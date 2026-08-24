use std::time::Duration;

use base64::{Engine as _, engine::general_purpose::STANDARD};
use hmac::{Hmac, Mac};
use reqwest::header::{
    AUTHORIZATION, CONTENT_LENGTH, CONTENT_TYPE, ETAG, HOST, HeaderMap, HeaderName, HeaderValue,
};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use url::{Host, Url};
use uuid::Uuid;
use zeroize::Zeroizing;

use crate::{capture::CaptureService, secure_store};

type HmacSha256 = Hmac<Sha256>;

const CONNECT_TIMEOUT: Duration = Duration::from_secs(10);
const REQUEST_TIMEOUT: Duration = Duration::from_secs(5 * 60);

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct OfficialUploadRequest {
    pub(crate) draft_id: Uuid,
    pub(crate) expected_revision: u64,
    pub(crate) image_id: Uuid,
    pub(crate) expected_bytes: usize,
    pub(crate) expected_sha256: String,
    pub(crate) official_upload_origin: String,
    pub(crate) upload: OfficialUpload,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct OfficialUpload {
    upload_id: Uuid,
    submission_id: Uuid,
    upload_group_id: Uuid,
    reservation_id: Uuid,
    staging_generation: String,
    signed_put_url: String,
    required_headers: OfficialHeaders,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct OfficialHeaders {
    content_type: String,
    checksum_sha256_base64: String,
    content_length: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct R2UploadRequest {
    pub(crate) draft_id: Uuid,
    pub(crate) expected_revision: u64,
    pub(crate) image_id: Uuid,
    pub(crate) expected_bytes: usize,
    pub(crate) expected_sha256: String,
    pub(crate) profile: R2Profile,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct R2Profile {
    profile_ref: String,
    account_id: String,
    bucket: String,
    public_base_url: String,
    prefix: String,
}

pub(crate) struct UploadResult {
    pub(crate) observed_etag: String,
    pub(crate) public_url: Option<String>,
}

pub(crate) async fn put_official(
    capture: &CaptureService,
    request: OfficialUploadRequest,
) -> Result<UploadResult, String> {
    let bytes = checked_asset(
        capture,
        request.draft_id,
        request.image_id,
        request.expected_revision,
        request.expected_bytes,
        &request.expected_sha256,
    )?;
    let upload = request.upload;
    let _immutable_binding = (
        upload.upload_id,
        upload.submission_id,
        upload.upload_group_id,
        upload.reservation_id,
    );
    if upload.staging_generation.parse::<u64>().is_err()
        || upload.required_headers.content_type != "image/png"
        || upload.required_headers.content_length != bytes.len().to_string()
        || upload.required_headers.checksum_sha256_base64 != STANDARD.encode(Sha256::digest(&bytes))
    {
        return Err("invalid-argument".to_string());
    }
    let url = signed_upload_url(&upload.signed_put_url, &request.official_upload_origin)?;
    let response = direct_client("official")?
        .put(url)
        .header(CONTENT_TYPE, upload.required_headers.content_type)
        .header(CONTENT_LENGTH, upload.required_headers.content_length)
        .header(
            HeaderName::from_static("x-amz-checksum-sha256"),
            upload.required_headers.checksum_sha256_base64,
        )
        .body(bytes)
        .send()
        .await
        .map_err(|error| transport_failure("official", "put", &error))?;
    if !response.status().is_success() {
        return Err(upload_failure(
            "official",
            "put",
            "http-status",
            Some(response.status().as_u16()),
        ));
    }
    let response_status = response.status().as_u16();
    let observed_etag = response
        .headers()
        .get(ETAG)
        .and_then(|value| value.to_str().ok())
        .filter(|value| !value.is_empty())
        .ok_or_else(|| upload_failure("official", "put", "missing-etag", Some(response_status)))?
        .to_string();
    Ok(UploadResult {
        observed_etag,
        public_url: None,
    })
}

pub(crate) async fn put_r2(
    capture: &CaptureService,
    request: R2UploadRequest,
) -> Result<UploadResult, String> {
    validate_profile(&request.profile)?;
    let bytes = checked_asset(
        capture,
        request.draft_id,
        request.image_id,
        request.expected_revision,
        request.expected_bytes,
        &request.expected_sha256,
    )?;
    let credentials = secure_store::r2_credentials(&request.profile.profile_ref)?;
    let key = r2_object_key(
        &request.profile.prefix,
        request.draft_id,
        request.expected_revision,
        request.image_id,
    );
    let mut upload_url = Url::parse(&format!(
        "https://{}.r2.cloudflarestorage.com",
        request.profile.account_id
    ))
    .map_err(|_| "invalid-argument".to_string())?;
    {
        let mut segments = upload_url
            .path_segments_mut()
            .map_err(|_| "invalid-argument".to_string())?;
        segments.pop_if_empty().push(&request.profile.bucket);
        for segment in key.split('/') {
            segments.push(segment);
        }
    }
    let now = aws_timestamp()?;
    let payload_hash = hex(&Sha256::digest(&bytes));
    let headers = signed_headers(
        &upload_url,
        bytes.len(),
        &payload_hash,
        &now,
        &credentials.access_key_id,
        &credentials.secret_access_key,
    )?;
    let client = direct_client("r2")?;
    let response = client
        .put(upload_url)
        .headers(headers)
        .body(bytes.clone())
        .send()
        .await
        .map_err(|error| transport_failure("r2", "put", &error))?;
    if !response.status().is_success() {
        return Err(upload_failure(
            "r2",
            "put",
            "http-status",
            Some(response.status().as_u16()),
        ));
    }
    let observed_etag = response
        .headers()
        .get(ETAG)
        .and_then(|value| value.to_str().ok())
        .unwrap_or("")
        .to_string();
    let mut public_url = https_url(&request.profile.public_base_url, false)?;
    {
        let mut segments = public_url
            .path_segments_mut()
            .map_err(|_| "invalid-argument".to_string())?;
        segments.pop_if_empty();
        for segment in key.split('/') {
            segments.push(segment);
        }
    }
    let mut verification = client
        .get(public_url.clone())
        .send()
        .await
        .map_err(|error| transport_failure("r2", "verify", &error))?;
    let verification_status = verification.status().as_u16();
    if !verification.status().is_success() {
        return Err(upload_failure(
            "r2",
            "verify",
            "http-status",
            Some(verification_status),
        ));
    }
    if verification
        .content_length()
        .is_some_and(|length| length > request.expected_bytes as u64)
    {
        return Err(upload_failure(
            "r2",
            "verify",
            "content-length-too-large",
            Some(verification_status),
        ));
    }
    let mut verified = VerificationBody::new(request.expected_bytes);
    while let Some(chunk) = verification
        .chunk()
        .await
        .map_err(|error| transport_failure("r2", "verify-body", &error))?
    {
        if let Err(error_code) = verified.push(&chunk) {
            return Err(upload_failure(
                "r2",
                "verify",
                error_code,
                Some(verification_status),
            ));
        }
    }
    if let Err(error_code) = verified.finish(&request.expected_sha256) {
        return Err(upload_failure(
            "r2",
            "verify",
            error_code,
            Some(verification_status),
        ));
    }
    Ok(UploadResult {
        observed_etag,
        public_url: Some(public_url.to_string()),
    })
}

fn checked_asset(
    capture: &CaptureService,
    draft_id: Uuid,
    image_id: Uuid,
    revision: u64,
    expected_bytes: usize,
    expected_sha256: &str,
) -> Result<Vec<u8>, String> {
    if expected_sha256.len() != 64
        || !expected_sha256
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
    {
        return Err("invalid-argument".to_string());
    }
    let bytes = capture
        .with_draft_store(|store| store.asset(draft_id, image_id, true, revision))
        .map_err(|error| error.code().to_string())?;
    if bytes.len() != expected_bytes || hex(&Sha256::digest(&bytes)) != expected_sha256 {
        return Err("revision-conflict".to_string());
    }
    Ok(bytes)
}

fn direct_client(provider: &'static str) -> Result<reqwest::Client, String> {
    reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(CONNECT_TIMEOUT)
        .timeout(REQUEST_TIMEOUT)
        .build()
        .map_err(|_| upload_failure(provider, "client", "client-build", None))
}

fn transport_failure(
    provider: &'static str,
    stage: &'static str,
    error: &reqwest::Error,
) -> String {
    let error_code = if error.is_timeout() {
        "timeout"
    } else if error.is_connect() {
        "connect"
    } else if error.is_body() {
        "body"
    } else {
        "transport"
    };
    upload_failure(provider, stage, error_code, None)
}

fn upload_failure(
    provider: &'static str,
    stage: &'static str,
    error_code: &'static str,
    status: Option<u16>,
) -> String {
    tracing::error!(
        event = "realqa_upload_failed",
        action = "capture-upload",
        provider,
        stage,
        status = status.unwrap_or(0),
        error_code
    );
    "platform-failure".to_string()
}

fn r2_object_key(prefix: &str, draft_id: Uuid, revision: u64, image_id: Uuid) -> String {
    [
        prefix,
        &draft_id.to_string(),
        &revision.to_string(),
        &format!("{image_id}.png"),
    ]
    .into_iter()
    .filter(|part| !part.is_empty())
    .collect::<Vec<_>>()
    .join("/")
}

struct VerificationBody {
    expected_bytes: usize,
    received_bytes: usize,
    digest: Sha256,
}

impl VerificationBody {
    fn new(expected_bytes: usize) -> Self {
        Self {
            expected_bytes,
            received_bytes: 0,
            digest: Sha256::new(),
        }
    }

    fn push(&mut self, bytes: &[u8]) -> Result<(), &'static str> {
        self.received_bytes = self
            .received_bytes
            .checked_add(bytes.len())
            .ok_or("response-too-large")?;
        if self.received_bytes > self.expected_bytes {
            return Err("response-too-large");
        }
        self.digest.update(bytes);
        Ok(())
    }

    fn finish(self, expected_sha256: &str) -> Result<(), &'static str> {
        if self.received_bytes != self.expected_bytes {
            return Err("size-mismatch");
        }
        if hex(&self.digest.finalize()) != expected_sha256 {
            return Err("checksum-mismatch");
        }
        Ok(())
    }
}

fn validate_profile(profile: &R2Profile) -> Result<(), String> {
    let identifier = |value: &str| {
        !value.is_empty()
            && value.len() <= 128
            && value
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
    };
    if !identifier(&profile.profile_ref)
        || profile.account_id.len() != 32
        || !profile
            .account_id
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
        || !identifier(&profile.bucket)
        || profile.prefix.len() > 512
        || profile.prefix.starts_with('/')
        || profile.prefix.ends_with('/')
        || profile.prefix.contains('\\')
        || profile
            .prefix
            .split('/')
            .any(|part| part.is_empty() || matches!(part, "." | ".."))
            && !profile.prefix.is_empty()
    {
        return Err("invalid-argument".to_string());
    }
    https_url(&profile.public_base_url, false)?;
    Ok(())
}

fn https_url(value: &str, allow_query: bool) -> Result<Url, String> {
    validated_url(value, allow_query, false)
}

fn signed_upload_url(value: &str, expected_origin: &str) -> Result<Url, String> {
    let url = validated_url(value, true, true)?;
    let origin = validated_url(expected_origin, false, true)?;
    if origin.path() != "/" || url.origin() != origin.origin() {
        return Err("invalid-argument".to_string());
    }
    Ok(url)
}

fn validated_url(value: &str, allow_query: bool, allow_loopback_http: bool) -> Result<Url, String> {
    let url = Url::parse(value).map_err(|_| "invalid-argument".to_string())?;
    let loopback = url.host().is_some_and(|host| match host {
        Host::Domain(host) => host.trim_end_matches('.').eq_ignore_ascii_case("localhost"),
        Host::Ipv4(address) => address.is_loopback(),
        Host::Ipv6(address) => address.is_loopback(),
    });
    if value.trim() != value
        || (url.scheme() != "https" && !(allow_loopback_http && url.scheme() == "http" && loopback))
        || !url.username().is_empty()
        || url.password().is_some()
        || url.fragment().is_some()
        || (!allow_query && url.query().is_some())
    {
        return Err("invalid-argument".to_string());
    }
    Ok(url)
}

fn signed_headers(
    url: &Url,
    length: usize,
    payload_hash: &str,
    timestamp: &str,
    access_key: &str,
    secret: &str,
) -> Result<HeaderMap, String> {
    let host = match url.port() {
        Some(port) if port != 443 => {
            format!("{}:{port}", url.host_str().ok_or("invalid-argument")?)
        }
        _ => url.host_str().ok_or("invalid-argument")?.to_string(),
    };
    let date = timestamp.get(..8).ok_or("platform-failure")?;
    let canonical_headers = format!(
        "content-length:{length}\ncontent-type:image/png\nhost:{host}\nx-amz-content-sha256:\
         {payload_hash}\nx-amz-date:{timestamp}\n"
    );
    let signed = "content-length;content-type;host;x-amz-content-sha256;x-amz-date";
    let canonical_request = format!(
        "PUT\n{}\n\n{canonical_headers}\n{signed}\n{payload_hash}",
        url.path()
    );
    let scope = format!("{date}/auto/s3/aws4_request");
    let string_to_sign = format!(
        "AWS4-HMAC-SHA256\n{timestamp}\n{scope}\n{}",
        hex(&Sha256::digest(canonical_request.as_bytes()))
    );
    let mut initial_key = Zeroizing::new(Vec::with_capacity(4 + secret.len()));
    initial_key.extend_from_slice(b"AWS4");
    initial_key.extend_from_slice(secret.as_bytes());
    let date_key = Zeroizing::new(hmac(&initial_key, date.as_bytes())?);
    let region_key = Zeroizing::new(hmac(&date_key, b"auto")?);
    let service_key = Zeroizing::new(hmac(&region_key, b"s3")?);
    let signing_key = Zeroizing::new(hmac(&service_key, b"aws4_request")?);
    let signature = hex(&hmac(&signing_key, string_to_sign.as_bytes())?);
    let authorization = format!(
        "AWS4-HMAC-SHA256 Credential={access_key}/{scope}, SignedHeaders={signed}, \
         Signature={signature}"
    );
    let mut headers = HeaderMap::new();
    for (name, value) in [
        (CONTENT_LENGTH, length.to_string()),
        (CONTENT_TYPE, "image/png".to_string()),
        (HOST, host),
        (
            HeaderName::from_static("x-amz-content-sha256"),
            payload_hash.to_string(),
        ),
        (HeaderName::from_static("x-amz-date"), timestamp.to_string()),
        (AUTHORIZATION, authorization),
    ] {
        headers.insert(
            name,
            HeaderValue::from_str(&value).map_err(|_| "invalid-argument".to_string())?,
        );
    }
    Ok(headers)
}

fn hmac(key: &[u8], value: &[u8]) -> Result<Vec<u8>, String> {
    let mut mac = HmacSha256::new_from_slice(key).map_err(|_| "platform-failure".to_string())?;
    mac.update(value);
    Ok(mac.finalize().into_bytes().to_vec())
}

fn aws_timestamp() -> Result<String, String> {
    let seconds = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_err(|_| "platform-failure".to_string())?
        .as_secs();
    timestamp_from_unix(seconds)
}

fn timestamp_from_unix(seconds: u64) -> Result<String, String> {
    let days = i64::try_from(seconds / 86_400).map_err(|_| "platform-failure".to_string())?;
    let seconds_in_day = seconds % 86_400;
    let shifted = days + 719_468;
    let era = shifted / 146_097;
    let day_of_era = shifted - era * 146_097;
    let year_of_era =
        (day_of_era - day_of_era / 1_460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let mut year = year_of_era + era * 400;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let month_prime = (5 * day_of_year + 2) / 153;
    let day = day_of_year - (153 * month_prime + 2) / 5 + 1;
    let month = month_prime + if month_prime < 10 { 3 } else { -9 };
    if month <= 2 {
        year += 1;
    }
    if !(0..=9_999).contains(&year) {
        return Err("platform-failure".to_string());
    }
    let hour = seconds_in_day / 3_600;
    let minute = seconds_in_day % 3_600 / 60;
    let second = seconds_in_day % 60;
    Ok(format!(
        "{year:04}{month:02}{day:02}T{hour:02}{minute:02}{second:02}Z"
    ))
}
fn hex(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut result = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        result.push(HEX[(byte >> 4) as usize] as char);
        result.push(HEX[(byte & 15) as usize] as char);
    }
    result
}

#[cfg(test)]
mod tests {
    use reqwest::header::{AUTHORIZATION, CONTENT_LENGTH, CONTENT_TYPE};
    use sha2::{Digest, Sha256};
    use url::Url;
    use uuid::Uuid;

    use super::{
        R2Profile, VerificationBody, hex, https_url, r2_object_key, signed_headers,
        signed_upload_url, timestamp_from_unix, validate_profile,
    };

    fn profile() -> R2Profile {
        R2Profile {
            profile_ref: "018f47a2-7b3c-7def-8abc-1234567890ab".to_string(),
            account_id: "0123456789abcdef0123456789abcdef".to_string(),
            bucket: "screenshots".to_string(),
            public_base_url: "https://images.example/devhud".to_string(),
            prefix: "team/realqa".to_string(),
        }
    }

    #[test]
    fn byo_profile_requires_https_and_normalized_metadata() {
        assert_eq!(validate_profile(&profile()), Ok(()));
        let mut invalid = profile();
        invalid.account_id = "account.example".to_string();
        assert_eq!(
            validate_profile(&invalid),
            Err("invalid-argument".to_string())
        );
        invalid = profile();
        invalid.prefix = "../secret".to_string();
        assert_eq!(
            validate_profile(&invalid),
            Err("invalid-argument".to_string())
        );
    }

    #[test]
    fn official_signed_urls_allow_queries_but_public_urls_do_not() {
        assert!(
            signed_upload_url(
                "https://r2.example/object?signature=value",
                "https://r2.example"
            )
            .is_ok()
        );
        assert!(
            signed_upload_url(
                "http://127.0.0.1:9000/object?signature=value",
                "http://127.0.0.1:9000"
            )
            .is_ok()
        );
        assert!(
            signed_upload_url(
                "http://[::1]:9000/object?signature=value",
                "http://[::1]:9000"
            )
            .is_ok()
        );
        assert!(
            signed_upload_url(
                "https://attacker.example/object?signature=value",
                "https://r2.example"
            )
            .is_err()
        );
        assert!(
            signed_upload_url(
                "http://r2.example/object?signature=value",
                "http://r2.example"
            )
            .is_err()
        );
        assert!(https_url("https://images.example/object?secret=value", false).is_err());
        assert!(https_url("https://user@example.com/object", true).is_err());
    }

    #[test]
    fn sigv4_headers_bind_bytes_without_exposing_the_secret() {
        let headers = signed_headers(
            &Url::parse("https://r2.example/bucket/object.png").expect("URL"),
            123,
            &"a".repeat(64),
            "20260821T010203Z",
            "access-id",
            "do-not-expose-secret",
        )
        .expect("signed headers");
        assert_eq!(headers.get(CONTENT_LENGTH).unwrap(), "123");
        assert_eq!(headers.get(CONTENT_TYPE).unwrap(), "image/png");
        let authorization = headers.get(AUTHORIZATION).unwrap().to_str().unwrap();
        assert!(authorization.contains("Credential=access-id/20260821/auto/s3/aws4_request"));
        assert!(!authorization.contains("do-not-expose-secret"));
    }

    #[test]
    fn aws_timestamp_uses_utc_calendar_fields() {
        assert_eq!(timestamp_from_unix(0), Ok("19700101T000000Z".to_string()));
        assert_eq!(
            timestamp_from_unix(86_400),
            Ok("19700102T000000Z".to_string())
        );
    }

    #[test]
    fn byo_object_key_is_immutable_per_flattened_revision() {
        let draft_id = Uuid::parse_str("018f47a2-7b3c-7def-8abc-1234567890ac").unwrap();
        let image_id = Uuid::parse_str("018f47a2-7b3c-7def-8abc-1234567890ad").unwrap();
        assert_eq!(
            r2_object_key("team/realqa", draft_id, 7, image_id),
            format!("team/realqa/{draft_id}/7/{image_id}.png")
        );
        assert_ne!(
            r2_object_key("team/realqa", draft_id, 7, image_id),
            r2_object_key("team/realqa", draft_id, 8, image_id)
        );
    }

    #[test]
    fn public_verification_hashes_incrementally_and_rejects_oversize_bodies() {
        let mut exact = VerificationBody::new(6);
        exact.push(b"abc").unwrap();
        exact.push(b"def").unwrap();
        assert_eq!(exact.finish(&hex(&Sha256::digest(b"abcdef"))), Ok(()));

        let mut oversized = VerificationBody::new(5);
        assert_eq!(oversized.push(b"abcdef"), Err("response-too-large"));
    }
}
