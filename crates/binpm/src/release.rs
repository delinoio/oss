use std::{collections::BTreeSet, env, fmt};

use chrono::{DateTime, Utc};
use reqwest::{
    blocking::{Client, RequestBuilder},
    header, StatusCode, Url,
};
use serde::{de::DeserializeOwned, Deserialize};
use tracing::{debug, info};

use crate::{
    assets::{classify_artifact, ArtifactKind},
    contract::{SourceProvider, SourceSpec},
    error::{BinpmError, ReleaseLookupDiagnosticKind, Result},
};

const USER_AGENT: &str = concat!("binpm/", env!("CARGO_PKG_VERSION"));
const RELEASES_PER_PAGE: u16 = 100;
const MAX_GITLAB_ASSET_REDIRECTS: usize = 10;
const GITHUB_TOKEN_ENV: &str = "BINPM_GITHUB_TOKEN";
const GITHUB_TOKEN_ENV_LEGACY: &str = "GITHUB_TOKEN";
const GITLAB_TOKEN_ENV: &str = "BINPM_GITLAB_TOKEN";
const GITLAB_TOKEN_ENV_LEGACY: &str = "GITLAB_TOKEN";
pub(crate) const GITHUB_ASSET_DOWNLOAD_ACCEPT: &str = "application/octet-stream";

#[derive(Clone, PartialEq, Eq)]
pub struct ProviderAuth {
    pub(crate) header_name: &'static str,
    pub(crate) header_value: String,
    pub(crate) env_var: String,
}

impl fmt::Debug for ProviderAuth {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ProviderAuth")
            .field("header_name", &self.header_name)
            .field("header_value", &"<redacted>")
            .field("env_var", &self.env_var)
            .finish()
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Release {
    pub tag: String,
    pub assets: Vec<ReleaseAsset>,
    pub stable: bool,
    pub released_at: Option<DateTime<Utc>>,
    pub stability_reason: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ReleaseAsset {
    pub name: String,
    pub url: String,
    pub provider_url: Option<String>,
    pub download_url: Option<String>,
    pub download_auth: Option<ProviderAuth>,
    pub download_accept: Option<&'static str>,
    pub digest: Option<String>,
    pub source_archive: bool,
    pub final_url_https: Option<bool>,
    pub final_url: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ReleaseSelection {
    pub release: Release,
    pub decision: String,
    pub skipped: Vec<SkippedRelease>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SkippedRelease {
    pub tag: String,
    pub reason: String,
}

pub trait ReleaseClient {
    fn list_releases(&self, source: &SourceSpec) -> Result<Vec<Release>>;

    fn resolve_release(&self, source: &SourceSpec) -> Result<ReleaseSelection> {
        let releases = self.list_releases(source)?;
        select_release(source, releases)
    }
}

#[derive(Debug, Clone)]
pub struct GitHubReleaseClient {
    http: Client,
}

impl GitHubReleaseClient {
    pub fn new() -> Result<Self> {
        Ok(Self {
            http: Client::builder()
                .user_agent(USER_AGENT)
                .build()
                .map_err(BinpmError::ReleaseHttpClient)?,
        })
    }

    pub fn releases_api_url(source: &SourceSpec) -> String {
        let (owner, repo) = source
            .path
            .split_once('/')
            .expect("GitHub source parsing guarantees owner/repo");

        if source.host == "github.com" {
            format!("https://api.github.com/repos/{owner}/{repo}/releases")
        } else {
            format!(
                "https://{}/api/v3/repos/{owner}/{repo}/releases",
                source.host
            )
        }
    }
}

impl ReleaseClient for GitHubReleaseClient {
    fn list_releases(&self, source: &SourceSpec) -> Result<Vec<Release>> {
        let url = Self::releases_api_url(source);
        info!(
            source_provider = source.provider.as_str(),
            source_host = source.host,
            source_path = source.path,
            api_url = sanitize_url(&url),
            "Looking up GitHub releases"
        );

        let auth = provider_auth_for_source(source);
        let releases = fetch_paginated_json::<GitHubRelease>(
            &self.http,
            source,
            &url,
            Some("application/vnd.github+json"),
            auth.as_ref(),
        )?;

        Ok(releases
            .into_iter()
            .map(|release| {
                let stable = !release.draft && !release.prerelease;
                let stability_reason = if stable {
                    None
                } else if release.draft {
                    Some("github draft release".to_string())
                } else {
                    Some("github prerelease release".to_string())
                };

                Release {
                    tag: release.tag_name,
                    stable,
                    released_at: None,
                    stability_reason,
                    assets: release
                        .assets
                        .into_iter()
                        .map(|asset| ReleaseAsset {
                            name: asset.name,
                            url: asset.browser_download_url,
                            provider_url: None,
                            download_url: auth.as_ref().map(|_| asset.url.clone()),
                            download_auth: auth.clone(),
                            download_accept: auth.as_ref().map(|_| GITHUB_ASSET_DOWNLOAD_ACCEPT),
                            digest: asset.digest,
                            source_archive: false,
                            final_url_https: None,
                            final_url: None,
                        })
                        .collect(),
                }
            })
            .collect())
    }
}

#[derive(Debug, Clone)]
pub struct GitLabReleaseClient {
    http: Client,
    now: DateTime<Utc>,
}

impl GitLabReleaseClient {
    pub fn new() -> Result<Self> {
        Self::with_now(Utc::now())
    }

    pub fn with_now(now: DateTime<Utc>) -> Result<Self> {
        Ok(Self {
            http: Client::builder()
                .user_agent(USER_AGENT)
                .redirect(reqwest::redirect::Policy::none())
                .build()
                .map_err(BinpmError::ReleaseHttpClient)?,
            now,
        })
    }

    pub fn releases_api_url(source: &SourceSpec) -> String {
        format!(
            "https://{}/api/v4/projects/{}/releases",
            source.host,
            percent_encode_path(&source.path)
        )
    }
}

impl ReleaseClient for GitLabReleaseClient {
    fn list_releases(&self, source: &SourceSpec) -> Result<Vec<Release>> {
        let url = Self::releases_api_url(source);
        info!(
            source_provider = source.provider.as_str(),
            source_host = source.host,
            source_path = source.path,
            api_url = sanitize_url(&url),
            "Looking up GitLab releases"
        );

        let auth = provider_auth_for_source(source);
        let mut releases =
            fetch_paginated_json::<GitLabRelease>(&self.http, source, &url, None, auth.as_ref())?
                .into_iter()
                .map(|release| release.into_release_with_auth(self.now, source, auth.as_ref()))
                .collect::<Vec<_>>();

        sort_gitlab_releases(&mut releases);
        Ok(releases)
    }

    fn resolve_release(&self, source: &SourceSpec) -> Result<ReleaseSelection> {
        let releases = self.list_releases(source)?;
        let mut selection = select_release(source, releases)?;
        verify_gitlab_asset_redirects(std::slice::from_mut(&mut selection.release))?;
        Ok(selection)
    }
}

fn sort_gitlab_releases(releases: &mut [Release]) {
    releases.sort_by(|left, right| {
        right
            .released_at
            .cmp(&left.released_at)
            .then_with(|| right.tag.cmp(&left.tag))
    });
}

pub fn select_release(source: &SourceSpec, releases: Vec<Release>) -> Result<ReleaseSelection> {
    if let Some(version) = &source.version {
        if let Some(release) = releases.iter().find(|release| &release.tag == version) {
            debug!(
                source_provider = source.provider.as_str(),
                source_host = source.host,
                source_path = source.path,
                requested_version = version,
                release_tag = release.tag,
                "Matched explicit release version"
            );
            return Ok(ReleaseSelection {
                release: release.clone(),
                decision: format!(
                    "explicit version `{version}` matched release tag `{}`",
                    release.tag
                ),
                skipped: Vec::new(),
            });
        }

        let prefix_hint = exact_tag_prefix_hint(version, &releases)
            .map(|hint| format!(" {hint}"))
            .unwrap_or_default();
        return Err(BinpmError::ReleaseNotFound {
            package: source.to_string(),
            message: format!("no release tag matched `{version}`.{prefix_hint}"),
        });
    }

    let mut skipped = Vec::new();
    for release in releases {
        if release.stable {
            return Ok(ReleaseSelection {
                decision: format!("selected latest stable release `{}`", release.tag),
                release,
                skipped,
            });
        }

        let reason = release
            .stability_reason
            .clone()
            .unwrap_or_else(|| "unstable release".to_string());
        debug!(
            source_provider = source.provider.as_str(),
            source_host = source.host,
            source_path = source.path,
            release_tag = release.tag,
            rejection_reason = reason,
            "Rejected unstable release"
        );
        skipped.push(SkippedRelease {
            tag: release.tag,
            reason,
        });
    }

    Err(BinpmError::ReleaseNotFound {
        package: source.to_string(),
        message: "no stable release found".to_string(),
    })
}

fn exact_tag_prefix_hint(version: &str, releases: &[Release]) -> Option<String> {
    if let Some(stripped) = version.strip_prefix('v') {
        if !stripped.is_empty() && releases.iter().any(|release| release.tag == stripped) {
            return Some(format!(
                "Exact tag matching is unchanged; upstream has `{stripped}`, so use `@{stripped}` \
                 or omit `@version` for latest stable selection."
            ));
        }
    }

    let prefixed = format!("v{version}");
    releases
        .iter()
        .any(|release| release.tag == prefixed)
        .then(|| {
            format!(
                "Exact tag matching is unchanged; upstream has `{prefixed}`, so use `@{prefixed}` \
                 or omit `@version` for latest stable selection."
            )
        })
}

pub fn client_for_source(source: &SourceSpec) -> Result<Box<dyn ReleaseClient>> {
    match source.provider {
        SourceProvider::GitHub => Ok(Box::new(GitHubReleaseClient::new()?)),
        SourceProvider::GitLab => Ok(Box::new(GitLabReleaseClient::new()?)),
    }
}

fn percent_encode_path(path: &str) -> String {
    path.bytes()
        .flat_map(|byte| match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                vec![byte as char]
            }
            other => format!("%{other:02X}").chars().collect(),
        })
        .collect()
}

fn sanitize_url(url: &str) -> String {
    let without_query = url.split(['?', '#']).next().unwrap_or(url);
    let Ok(mut parsed) = reqwest::Url::parse(without_query) else {
        return without_query.to_string();
    };

    if !parsed.username().is_empty() || parsed.password().is_some() {
        let _ = parsed.set_username("");
        let _ = parsed.set_password(None);
    }
    parsed.to_string()
}

pub(crate) fn provider_auth_for_source(source: &SourceSpec) -> Option<ProviderAuth> {
    provider_auth_for_source_with(source, |name| env::var(name).ok())
}

fn provider_auth_for_source_with(
    source: &SourceSpec,
    get_env: impl Fn(&str) -> Option<String>,
) -> Option<ProviderAuth> {
    for env_var in provider_token_env_candidates(source.provider, &source.host) {
        let Some(token) = get_env(&env_var).map(|token| token.trim().to_string()) else {
            continue;
        };
        if token.is_empty() {
            continue;
        }

        return Some(match source.provider {
            SourceProvider::GitHub => ProviderAuth {
                header_name: header::AUTHORIZATION.as_str(),
                header_value: format!("Bearer {token}"),
                env_var,
            },
            SourceProvider::GitLab => ProviderAuth {
                header_name: "PRIVATE-TOKEN",
                header_value: token,
                env_var,
            },
        });
    }

    None
}

fn provider_token_env_candidates(provider: SourceProvider, host: &str) -> Vec<String> {
    let normalized_host = match (provider, host) {
        (SourceProvider::GitHub, "github.com") => "GITHUB_COM".to_string(),
        (SourceProvider::GitLab, "gitlab.com") => "GITLAB_COM".to_string(),
        _ => normalized_host_env_suffix(host),
    };
    match provider {
        SourceProvider::GitHub => {
            let mut candidates = vec![format!("{GITHUB_TOKEN_ENV}_{normalized_host}")];
            if host == "github.com" {
                candidates.push(GITHUB_TOKEN_ENV.to_string());
                candidates.push(GITHUB_TOKEN_ENV_LEGACY.to_string());
            }
            candidates
        }
        SourceProvider::GitLab => {
            let mut candidates = vec![format!("{GITLAB_TOKEN_ENV}_{normalized_host}")];
            if host == "gitlab.com" {
                candidates.push(GITLAB_TOKEN_ENV.to_string());
                candidates.push(GITLAB_TOKEN_ENV_LEGACY.to_string());
            }
            candidates
        }
    }
}

fn normalized_host_env_suffix(host: &str) -> String {
    host.bytes().fold(String::new(), |mut suffix, byte| {
        if byte.is_ascii_alphanumeric() {
            suffix.push(byte.to_ascii_uppercase() as char);
        } else {
            suffix.push_str(&format!("_{byte:02X}_"));
        }
        suffix
    })
}

fn releases_page_url(url: &str) -> String {
    let separator = if url.contains('?') { '&' } else { '?' };
    format!("{url}{separator}per_page={RELEASES_PER_PAGE}")
}

fn fetch_paginated_json<T>(
    http: &Client,
    source: &SourceSpec,
    first_url: &str,
    accept: Option<&'static str>,
    auth: Option<&ProviderAuth>,
) -> Result<Vec<T>>
where
    T: DeserializeOwned,
{
    let first_page_url = releases_page_url(first_url);
    let pagination_origin = Url::parse(&first_page_url).map_err(|error| BinpmError::UnsafeUrl {
        url: sanitize_url(&first_page_url),
        message: format!("invalid release pagination URL: {error}"),
    })?;
    let mut next_url = Some(first_page_url);
    let mut visited_urls = BTreeSet::new();
    let mut items = Vec::new();

    while let Some(url) = next_url {
        if !visited_urls.insert(url.clone()) {
            return Err(BinpmError::ReleasePaginationLoop {
                url: sanitize_url(&url),
            });
        }
        debug!(
            api_url = sanitize_url(&url),
            "Fetching release metadata page"
        );

        let response = release_request(http, &url, accept, auth)
            .send()
            .map_err(|error| BinpmError::ReleaseLookup(error.without_url()))?;
        if let Some(error) =
            release_lookup_diagnostic(source, auth, response.status(), response.headers())
        {
            return Err(error);
        }
        let response = response
            .error_for_status()
            .map_err(|error| BinpmError::ReleaseLookup(error.without_url()))?;
        next_url = match next_page_url(response.headers().get(header::LINK)) {
            Some(url) => Some(validate_pagination_url(&pagination_origin, &url)?),
            None => None,
        };

        let mut page = response
            .json::<Vec<T>>()
            .map_err(BinpmError::ReleaseLookup)?;
        debug!(
            page_release_count = page.len(),
            has_next_page = next_url.is_some(),
            "Fetched release metadata page"
        );
        items.append(&mut page);
    }

    Ok(items)
}

fn release_request(
    http: &Client,
    url: &str,
    accept: Option<&'static str>,
    auth: Option<&ProviderAuth>,
) -> RequestBuilder {
    let mut request = http.get(url);
    if let Some(accept) = accept {
        request = request.header(header::ACCEPT, accept);
    }
    if let Some(auth) = auth {
        request = request.header(auth.header_name, auth.header_value.as_str());
    }
    request
}

fn release_lookup_diagnostic(
    source: &SourceSpec,
    auth: Option<&ProviderAuth>,
    status: StatusCode,
    headers: &header::HeaderMap,
) -> Option<BinpmError> {
    let kind = classify_release_lookup_status(status, auth.is_some(), headers)?;
    let message = match kind {
        ReleaseLookupDiagnosticKind::MissingAuth => "The provider did not return release metadata \
                                                     for an unauthenticated request."
            .to_string(),
        ReleaseLookupDiagnosticKind::InsufficientPermissions => {
            "The configured provider token was rejected or does not have access to this repository."
                .to_string()
        }
        ReleaseLookupDiagnosticKind::RateLimited => rate_limit_message(headers),
    };
    let hint = release_lookup_hint(source, auth, kind);

    Some(BinpmError::ReleaseLookupDiagnostic {
        package: source.to_string(),
        provider: source.provider.as_str(),
        host: source.host.clone(),
        status: status.as_u16(),
        kind,
        message,
        hint,
    })
}

fn classify_release_lookup_status(
    status: StatusCode,
    authenticated: bool,
    headers: &header::HeaderMap,
) -> Option<ReleaseLookupDiagnosticKind> {
    if status == StatusCode::TOO_MANY_REQUESTS
        || (status.is_client_error() || status.is_server_error()) && rate_limit_indicated(headers)
    {
        return Some(ReleaseLookupDiagnosticKind::RateLimited);
    }

    if status.is_redirection() {
        return Some(if authenticated {
            ReleaseLookupDiagnosticKind::InsufficientPermissions
        } else {
            ReleaseLookupDiagnosticKind::MissingAuth
        });
    }

    match status {
        StatusCode::UNAUTHORIZED => Some(if authenticated {
            ReleaseLookupDiagnosticKind::InsufficientPermissions
        } else {
            ReleaseLookupDiagnosticKind::MissingAuth
        }),
        StatusCode::FORBIDDEN | StatusCode::NOT_FOUND => Some(if authenticated {
            ReleaseLookupDiagnosticKind::InsufficientPermissions
        } else {
            ReleaseLookupDiagnosticKind::MissingAuth
        }),
        _ => None,
    }
}

fn rate_limit_indicated(headers: &header::HeaderMap) -> bool {
    header_is_zero(headers, "x-ratelimit-remaining")
        || header_is_zero(headers, "ratelimit-remaining")
        || headers.contains_key(header::RETRY_AFTER)
}

fn header_is_zero(headers: &header::HeaderMap, name: &'static str) -> bool {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.trim() == "0")
}

fn rate_limit_message(headers: &header::HeaderMap) -> String {
    let retry_after = headers
        .get(header::RETRY_AFTER)
        .and_then(|value| value.to_str().ok())
        .map(str::trim)
        .filter(|value| !value.is_empty());
    match retry_after {
        Some(retry_after) => format!(
            "The provider rate limit is exhausted. Retry after `{retry_after}` seconds or when \
             the provider quota resets."
        ),
        None => "The provider rate limit is exhausted.".to_string(),
    }
}

fn release_lookup_hint(
    source: &SourceSpec,
    auth: Option<&ProviderAuth>,
    kind: ReleaseLookupDiagnosticKind,
) -> String {
    match kind {
        ReleaseLookupDiagnosticKind::MissingAuth => format!(
            "Set one of {} for this host.",
            provider_token_env_candidates(source.provider, &source.host)
                .into_iter()
                .map(|name| format!("`{name}`"))
                .collect::<Vec<_>>()
                .join(", ")
        ),
        ReleaseLookupDiagnosticKind::InsufficientPermissions => match auth {
            Some(auth) => format!(
                "Check that `{}` is valid for `{}` and has permission to read releases for `{}`.",
                auth.env_var, source.host, source.path
            ),
            None => format!(
                "Set one of {} with permission to read releases for `{}`.",
                provider_token_env_candidates(source.provider, &source.host)
                    .into_iter()
                    .map(|name| format!("`{name}`"))
                    .collect::<Vec<_>>()
                    .join(", "),
                source.path
            ),
        },
        ReleaseLookupDiagnosticKind::RateLimited => match auth {
            Some(auth) => format!(
                "Wait for the provider quota to reset, or use a token with more quota via `{}`.",
                auth.env_var
            ),
            None => format!(
                "Set one of {} to use an authenticated provider quota, or wait for the anonymous \
                 quota to reset.",
                provider_token_env_candidates(source.provider, &source.host)
                    .into_iter()
                    .map(|name| format!("`{name}`"))
                    .collect::<Vec<_>>()
                    .join(", ")
            ),
        },
    }
}

fn next_page_url(link: Option<&header::HeaderValue>) -> Option<String> {
    let link = link?.to_str().ok()?;

    link.split(',').find_map(|part| {
        let (raw_url, raw_params) = part.trim().split_once(';')?;
        let is_next = raw_params
            .split(';')
            .any(|param| param.trim() == r#"rel="next""#);

        if is_next {
            raw_url
                .trim()
                .strip_prefix('<')?
                .strip_suffix('>')
                .map(str::to_string)
        } else {
            None
        }
    })
}

fn validate_pagination_url(origin: &Url, next_url: &str) -> Result<String> {
    let parsed = origin
        .join(next_url)
        .map_err(|error| BinpmError::UnsafeUrl {
            url: sanitize_url(next_url),
            message: format!("invalid release pagination URL: {error}"),
        })?;

    if parsed.scheme() != "https"
        || parsed.scheme() != origin.scheme()
        || parsed.host_str() != origin.host_str()
        || parsed.port_or_known_default() != origin.port_or_known_default()
    {
        return Err(BinpmError::UnsafeUrl {
            url: sanitize_url(parsed.as_str()),
            message: "release pagination URL must stay on the original HTTPS API origin"
                .to_string(),
        });
    }

    Ok(parsed.to_string())
}

#[derive(Debug, Deserialize)]
struct GitHubRelease {
    tag_name: String,
    draft: bool,
    prerelease: bool,
    #[serde(default)]
    assets: Vec<GitHubAsset>,
}

#[derive(Debug, Deserialize)]
struct GitHubAsset {
    url: String,
    name: String,
    browser_download_url: String,
    digest: Option<String>,
}

#[derive(Debug, Deserialize)]
struct GitLabRelease {
    tag_name: String,
    released_at: Option<String>,
    #[serde(default)]
    upcoming_release: bool,
    #[serde(default)]
    assets: GitLabAssets,
}

impl GitLabRelease {
    #[cfg(test)]
    fn into_release(self, now: DateTime<Utc>) -> Release {
        let source: SourceSpec = "gitlab:gitlab.example.com/group/tool"
            .parse()
            .expect("source");
        self.into_release_with_auth(now, &source, None)
    }

    fn into_release_with_auth(
        self,
        now: DateTime<Utc>,
        source: &SourceSpec,
        auth: Option<&ProviderAuth>,
    ) -> Release {
        let mut stable = true;
        let mut reason = None;

        if self.upcoming_release {
            stable = false;
            reason = Some("gitlab upcoming release".to_string());
        }

        let released_at = parse_released_at(self.released_at.as_deref());

        if stable
            && released_at
                .as_ref()
                .is_some_and(|released_at| *released_at > now)
        {
            stable = false;
            reason = Some("gitlab future released_at".to_string());
        }

        if stable && has_prerelease_tag(&self.tag_name) {
            stable = false;
            reason = Some("gitlab prerelease tag".to_string());
        }

        Release {
            tag: self.tag_name,
            stable,
            released_at,
            stability_reason: reason,
            assets: self
                .assets
                .links
                .into_iter()
                .map(|link| ReleaseAsset {
                    name: gitlab_link_asset_name(&link),
                    download_auth: gitlab_asset_download_auth(source, &link, auth),
                    url: link.url,
                    provider_url: link.direct_asset_url,
                    download_url: None,
                    download_accept: None,
                    digest: None,
                    source_archive: false,
                    final_url_https: None,
                    final_url: None,
                })
                .chain(self.assets.sources.into_iter().map(|source| ReleaseAsset {
                    name: source.format,
                    url: source.url,
                    provider_url: None,
                    download_url: None,
                    download_auth: None,
                    download_accept: None,
                    digest: None,
                    source_archive: true,
                    final_url_https: None,
                    final_url: None,
                }))
                .collect(),
        }
    }
}

fn gitlab_asset_download_auth(
    source: &SourceSpec,
    link: &GitLabLink,
    auth: Option<&ProviderAuth>,
) -> Option<ProviderAuth> {
    let auth = auth?;
    let source_origin = Url::parse(&format!("https://{}/", source.host)).ok()?;
    let request_url = link.direct_asset_url.as_deref().unwrap_or(&link.url);
    let request_origin = Url::parse(request_url).ok()?;

    same_origin(&source_origin, &request_origin).then(|| auth.clone())
}

fn gitlab_link_asset_name(link: &GitLabLink) -> String {
    link.direct_asset_url
        .as_deref()
        .and_then(url_filename)
        .or_else(|| url_filename(&link.url))
        .unwrap_or_else(|| link.name.clone())
}

fn url_filename(raw: &str) -> Option<String> {
    let parsed = Url::parse(raw).ok()?;
    parsed
        .path_segments()?
        .next_back()
        .filter(|segment| !segment.is_empty())
        .map(str::to_string)
}

#[derive(Debug, Default, Deserialize)]
struct GitLabAssets {
    #[serde(default)]
    links: Vec<GitLabLink>,
    #[serde(default)]
    sources: Vec<GitLabSource>,
}

#[derive(Debug, Deserialize)]
struct GitLabLink {
    name: String,
    url: String,
    direct_asset_url: Option<String>,
}

#[derive(Debug, Deserialize)]
struct GitLabSource {
    format: String,
    url: String,
}

fn parse_released_at(released_at: Option<&str>) -> Option<DateTime<Utc>> {
    released_at
        .and_then(|raw| DateTime::parse_from_rfc3339(raw).ok())
        .map(|released_at| released_at.with_timezone(&Utc))
}

fn has_prerelease_tag(tag: &str) -> bool {
    tag.split(|character: char| {
        !(character.is_ascii_alphanumeric() || character == '.' || character == '-')
    })
    .any(is_semver_prerelease)
}

fn is_semver_prerelease(candidate: &str) -> bool {
    let parts = candidate.split('-').collect::<Vec<_>>();

    parts.windows(2).any(|window| {
        let version_core = window[0].trim_start_matches('v');
        let suffix = window[1];

        is_semver_core(version_core) && is_known_prerelease_identifier(suffix)
    })
}

fn is_known_prerelease_identifier(candidate: &str) -> bool {
    let identifier = candidate
        .split(['.', '+'])
        .next()
        .unwrap_or(candidate)
        .to_ascii_lowercase();
    matches!(
        identifier.as_str(),
        "alpha" | "a" | "beta" | "b" | "pre" | "preview" | "rc"
    )
}

fn is_semver_core(candidate: &str) -> bool {
    let mut parts = candidate.split('.');

    let Some(major) = parts.next() else {
        return false;
    };
    let Some(minor) = parts.next() else {
        return false;
    };
    let Some(patch) = parts.next() else {
        return false;
    };

    parts.next().is_none()
        && is_numeric_identifier(major)
        && is_numeric_identifier(minor)
        && is_numeric_identifier(patch)
}

fn is_numeric_identifier(candidate: &str) -> bool {
    !candidate.is_empty()
        && candidate
            .chars()
            .all(|character| character.is_ascii_digit())
}

fn verify_gitlab_asset_redirects(releases: &mut [Release]) -> Result<()> {
    let http = Client::builder()
        .user_agent(USER_AGENT)
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(BinpmError::ReleaseHttpClient)?;
    for release in releases {
        for asset in &mut release.assets {
            if asset.source_archive || !is_https_url(&asset.url) {
                continue;
            }

            if !matches!(
                classify_artifact(&asset.name, asset.source_archive),
                ArtifactKind::Archive(_) | ArtifactKind::BareExecutable
            ) {
                continue;
            }

            let url = asset.provider_url.as_deref().unwrap_or(&asset.url);
            if !is_https_url(url) {
                continue;
            }
            crate::storage::validate_download_url(url)?;

            let final_url =
                match resolve_gitlab_asset_redirect_url(&http, url, asset.download_auth.as_ref()) {
                    Ok(final_url) => final_url,
                    Err(BinpmError::ReleaseLookup(_)) => {
                        asset.final_url_https = Some(false);
                        continue;
                    }
                    Err(error) => return Err(error),
                };
            let final_url_https = is_https_url(&final_url);
            debug!(
                release_tag = release.tag,
                asset_name = asset.name,
                asset_url = sanitize_url(url),
                final_url = sanitize_url(&final_url),
                final_url_https,
                "Resolved GitLab asset redirect target"
            );
            asset.final_url_https = Some(final_url_https);
            asset.final_url = Some(final_url);
        }
    }

    Ok(())
}

fn resolve_gitlab_asset_redirect_url(
    http: &Client,
    url: &str,
    auth: Option<&ProviderAuth>,
) -> Result<String> {
    let origin = Url::parse(url).expect("GitLab asset URL was already validated");
    let mut current_url = url.to_string();
    let mut visited_urls = BTreeSet::new();

    for _ in 0..=MAX_GITLAB_ASSET_REDIRECTS {
        if !visited_urls.insert(current_url.clone()) {
            return Err(BinpmError::ReleasePaginationLoop {
                url: sanitize_url(&current_url),
            });
        }
        if !is_https_url(&current_url) {
            return Ok(current_url);
        }
        crate::storage::validate_download_url(&current_url)?;

        let current = Url::parse(&current_url).map_err(|error| BinpmError::UnsafeUrl {
            url: sanitize_url(&current_url),
            message: format!("invalid release asset redirect URL: {error}"),
        })?;
        let mut request = http.head(current.as_str());
        if let Some(auth) = auth.filter(|_| same_origin(&origin, &current)) {
            request = request.header(auth.header_name, auth.header_value.as_str());
        }

        let response = request
            .send()
            .map_err(|error| BinpmError::ReleaseLookup(error.without_url()))?
            .error_for_status()
            .map_err(|error| BinpmError::ReleaseLookup(error.without_url()))?;
        let Some(next_url) = response
            .headers()
            .get(header::LOCATION)
            .and_then(|location| location.to_str().ok())
            .and_then(|location| response.url().join(location).ok())
            .map(|location| location.to_string())
        else {
            return Ok(response.url().as_str().to_string());
        };
        current_url = next_url;
    }

    Err(BinpmError::UnsafeUrl {
        url: sanitize_url(&current_url),
        message: "release asset redirect chain exceeded limit".to_string(),
    })
}

fn same_origin(left: &Url, right: &Url) -> bool {
    left.scheme() == right.scheme()
        && left.host_str() == right.host_str()
        && left.port_or_known_default() == right.port_or_known_default()
}

fn is_https_url(url: &str) -> bool {
    url.to_ascii_lowercase().starts_with("https://")
}

#[cfg(test)]
mod tests {
    use chrono::{TimeZone, Utc};
    use reqwest::{header, StatusCode};

    use super::{
        classify_release_lookup_status, exact_tag_prefix_hint, has_prerelease_tag, next_page_url,
        provider_auth_for_source_with, provider_token_env_candidates, release_lookup_diagnostic,
        release_request, releases_page_url, sanitize_url, select_release, sort_gitlab_releases,
        validate_pagination_url, verify_gitlab_asset_redirects, GitHubReleaseClient, GitLabRelease,
        GitLabReleaseClient, Release,
    };
    use crate::{
        contract::{SourceProvider, SourceSpec},
        error::{BinpmError, ReleaseLookupDiagnosticKind},
        release::ReleaseAsset,
    };

    #[test]
    fn github_client_uses_dot_com_api_for_implicit_host() {
        let source: SourceSpec = "github:owner/repo".parse().expect("source");

        assert_eq!(
            GitHubReleaseClient::releases_api_url(&source),
            "https://api.github.com/repos/owner/repo/releases"
        );
    }

    #[test]
    fn github_client_uses_enterprise_api_for_explicit_host() {
        let source: SourceSpec = "github:ghe.example.com/owner/repo".parse().expect("source");

        assert_eq!(
            GitHubReleaseClient::releases_api_url(&source),
            "https://ghe.example.com/api/v3/repos/owner/repo/releases"
        );
    }

    #[test]
    fn gitlab_client_percent_encodes_project_path() {
        let source: SourceSpec = "gitlab:gitlab.example.com/group/sub/tool"
            .parse()
            .expect("source");

        assert_eq!(
            GitLabReleaseClient::releases_api_url(&source),
            "https://gitlab.example.com/api/v4/projects/group%2Fsub%2Ftool/releases"
        );
    }

    #[test]
    fn release_lookup_requests_maximum_page_size() {
        assert_eq!(
            releases_page_url("https://api.example.com/releases"),
            "https://api.example.com/releases?per_page=100"
        );
        assert_eq!(
            releases_page_url("https://api.example.com/releases?order_by=released_at"),
            "https://api.example.com/releases?order_by=released_at&per_page=100"
        );
    }

    #[test]
    fn release_lookup_reads_next_link_header() {
        let link = header::HeaderValue::from_static(
            r#"<https://api.example.com/releases?page=3>; rel="next", <https://api.example.com/releases?page=9>; rel="last""#,
        );

        assert_eq!(
            next_page_url(Some(&link)).as_deref(),
            Some("https://api.example.com/releases?page=3")
        );
        assert_eq!(next_page_url(None), None);
    }

    #[test]
    fn release_pagination_accepts_same_https_origin() {
        let origin = reqwest::Url::parse("https://api.example.com/releases?per_page=100")
            .expect("origin URL");

        assert_eq!(
            validate_pagination_url(&origin, "https://api.example.com/releases?page=2")
                .expect("same origin"),
            "https://api.example.com/releases?page=2"
        );
        assert_eq!(
            validate_pagination_url(&origin, "/releases?page=2").expect("relative URL"),
            "https://api.example.com/releases?page=2"
        );
    }

    #[test]
    fn release_pagination_rejects_unsafe_next_url() {
        let origin = reqwest::Url::parse("https://api.example.com/releases?per_page=100")
            .expect("origin URL");

        let downgrade = validate_pagination_url(&origin, "http://api.example.com/releases?page=2")
            .expect_err("http URL");
        assert!(downgrade.to_string().contains("original HTTPS API origin"));

        let other_host =
            validate_pagination_url(&origin, "https://evil.example.com/releases?page=2")
                .expect_err("different host");
        assert!(other_host.to_string().contains("original HTTPS API origin"));
    }

    #[test]
    fn sanitized_urls_redact_userinfo() {
        assert_eq!(
            sanitize_url("https://token@example.com/asset?download=1#fragment"),
            "https://example.com/asset"
        );
    }

    #[test]
    fn provider_token_env_candidates_are_host_scoped() {
        assert_eq!(
            provider_token_env_candidates(SourceProvider::GitHub, "github.com"),
            [
                "BINPM_GITHUB_TOKEN_GITHUB_COM",
                "BINPM_GITHUB_TOKEN",
                "GITHUB_TOKEN"
            ]
        );
        assert_eq!(
            provider_token_env_candidates(SourceProvider::GitHub, "ghe.example.com"),
            ["BINPM_GITHUB_TOKEN_GHE_2E_EXAMPLE_2E_COM"]
        );
        assert_eq!(
            provider_token_env_candidates(SourceProvider::GitLab, "gitlab.com"),
            [
                "BINPM_GITLAB_TOKEN_GITLAB_COM",
                "BINPM_GITLAB_TOKEN",
                "GITLAB_TOKEN"
            ]
        );
        assert_eq!(
            provider_token_env_candidates(SourceProvider::GitLab, "gitlab.example.com"),
            ["BINPM_GITLAB_TOKEN_GITLAB_2E_EXAMPLE_2E_COM"]
        );
        assert_ne!(
            provider_token_env_candidates(SourceProvider::GitHub, "ghe.example.com"),
            provider_token_env_candidates(SourceProvider::GitHub, "ghe-example.com")
        );
    }

    #[test]
    fn provider_auth_uses_host_specific_precedence() {
        let source: SourceSpec = "github:owner/repo".parse().expect("source");
        let auth = provider_auth_for_source_with(&source, |name| match name {
            "BINPM_GITHUB_TOKEN_GITHUB_COM" => Some("host-token".to_string()),
            "BINPM_GITHUB_TOKEN" => Some("generic-token".to_string()),
            "GITHUB_TOKEN" => Some("legacy-token".to_string()),
            _ => None,
        })
        .expect("auth");

        assert_eq!(auth.env_var, "BINPM_GITHUB_TOKEN_GITHUB_COM");
        assert_eq!(auth.header_name, "authorization");
        assert_eq!(auth.header_value, "Bearer host-token");
    }

    #[test]
    fn provider_auth_does_not_send_dot_com_tokens_to_enterprise_hosts() {
        let source: SourceSpec = "github:ghe.example.com/owner/repo".parse().expect("source");
        let auth = provider_auth_for_source_with(&source, |name| match name {
            "BINPM_GITHUB_TOKEN" | "GITHUB_TOKEN" => Some("wrong-host-token".to_string()),
            _ => None,
        });

        assert_eq!(auth, None);
    }

    #[test]
    fn provider_auth_does_not_reuse_colliding_host_tokens() {
        let source: SourceSpec = "github:ghe-example.com/owner/repo".parse().expect("source");
        let auth = provider_auth_for_source_with(&source, |name| match name {
            "BINPM_GITHUB_TOKEN_GHE_2E_EXAMPLE_2E_COM" => Some("wrong-host-token".to_string()),
            "BINPM_GITHUB_TOKEN_GHE_2D_EXAMPLE_2E_COM" => Some("right-host-token".to_string()),
            _ => None,
        })
        .expect("auth");

        assert_eq!(auth.env_var, "BINPM_GITHUB_TOKEN_GHE_2D_EXAMPLE_2E_COM");
        assert_eq!(auth.header_value, "Bearer right-host-token");
    }

    #[test]
    fn gitlab_provider_auth_uses_private_token_header() {
        let source: SourceSpec = "gitlab:gitlab.example.com/group/tool"
            .parse()
            .expect("source");
        let auth = provider_auth_for_source_with(&source, |name| match name {
            "BINPM_GITLAB_TOKEN_GITLAB_2E_EXAMPLE_2E_COM" => Some("self-managed-token".to_string()),
            _ => None,
        })
        .expect("auth");
        let request = release_request(
            &reqwest::blocking::Client::new(),
            "https://gitlab.example.com/api/v4/projects/group%2Ftool/releases?per_page=100",
            None,
            Some(&auth),
        )
        .build()
        .expect("request");

        assert_eq!(auth.env_var, "BINPM_GITLAB_TOKEN_GITLAB_2E_EXAMPLE_2E_COM");
        assert_eq!(
            request
                .headers()
                .get("PRIVATE-TOKEN")
                .and_then(|value| value.to_str().ok()),
            Some("self-managed-token")
        );
        assert_eq!(
            request
                .headers()
                .get(header::AUTHORIZATION)
                .and_then(|value| value.to_str().ok()),
            None
        );
    }

    #[test]
    fn gitlab_release_assets_keep_provider_auth_for_downloads_and_probes() {
        let source: SourceSpec = "gitlab:gitlab.example.com/group/tool"
            .parse()
            .expect("source");
        let auth = provider_auth_for_source_with(&source, |name| match name {
            "BINPM_GITLAB_TOKEN_GITLAB_2E_EXAMPLE_2E_COM" => Some("self-managed-token".to_string()),
            _ => None,
        })
        .expect("auth");
        let release = GitLabRelease {
            tag_name: "v1.0.0".to_string(),
            released_at: None,
            upcoming_release: false,
            assets: super::GitLabAssets {
                links: vec![super::GitLabLink {
                    name: "linux amd64".to_string(),
                    url: "https://gitlab.example.com/group/tool/-/releases/v1/downloads/tool-linux-amd64.tar.gz".to_string(),
                    direct_asset_url: Some(
                        "https://gitlab.example.com/group/tool/-/releases/v1/downloads/tool-linux-amd64.tar.gz".to_string(),
                    ),
                }],
                sources: Vec::new(),
            },
        }
        .into_release_with_auth(
            Utc.with_ymd_and_hms(2026, 6, 19, 0, 0, 0).unwrap(),
            &source,
            Some(&auth),
        );

        assert_eq!(
            release.assets[0]
                .download_auth
                .as_ref()
                .map(|auth| (auth.header_name, auth.header_value.as_str())),
            Some(("PRIVATE-TOKEN", "self-managed-token"))
        );
    }

    #[test]
    fn gitlab_release_assets_drop_provider_auth_for_external_downloads_and_probes() {
        let source: SourceSpec = "gitlab:gitlab.example.com/group/tool"
            .parse()
            .expect("source");
        let auth = provider_auth_for_source_with(&source, |name| match name {
            "BINPM_GITLAB_TOKEN_GITLAB_2E_EXAMPLE_2E_COM" => Some("self-managed-token".to_string()),
            _ => None,
        })
        .expect("auth");
        let release = GitLabRelease {
            tag_name: "v1.0.0".to_string(),
            released_at: None,
            upcoming_release: false,
            assets: super::GitLabAssets {
                links: vec![super::GitLabLink {
                    name: "linux amd64".to_string(),
                    url: "https://downloads.example.net/tool-linux-amd64.tar.gz".to_string(),
                    direct_asset_url: None,
                }],
                sources: Vec::new(),
            },
        }
        .into_release_with_auth(
            Utc.with_ymd_and_hms(2026, 6, 19, 0, 0, 0).unwrap(),
            &source,
            Some(&auth),
        );

        assert_eq!(release.assets[0].download_auth, None);
    }

    #[test]
    fn release_lookup_diagnostic_distinguishes_missing_auth_and_permissions() {
        let source: SourceSpec = "github:owner/private".parse().expect("source");
        let headers = header::HeaderMap::new();
        let missing = release_lookup_diagnostic(&source, None, StatusCode::NOT_FOUND, &headers)
            .expect("missing auth diagnostic");

        match missing {
            BinpmError::ReleaseLookupDiagnostic { kind, hint, .. } => {
                assert_eq!(kind, ReleaseLookupDiagnosticKind::MissingAuth);
                assert!(hint.contains("BINPM_GITHUB_TOKEN_GITHUB_COM"));
            }
            other => panic!("unexpected diagnostic: {other}"),
        }

        let auth = provider_auth_for_source_with(&source, |name| match name {
            "BINPM_GITHUB_TOKEN_GITHUB_COM" => Some("secret-token".to_string()),
            _ => None,
        })
        .expect("auth");
        let insufficient =
            release_lookup_diagnostic(&source, Some(&auth), StatusCode::FORBIDDEN, &headers)
                .expect("permission diagnostic");
        assert!(insufficient
            .to_string()
            .contains("insufficient permissions"));
        assert!(insufficient
            .to_string()
            .contains("BINPM_GITHUB_TOKEN_GITHUB_COM"));
        assert!(!insufficient.to_string().contains("secret-token"));

        assert_eq!(
            classify_release_lookup_status(StatusCode::FOUND, false, &headers),
            Some(ReleaseLookupDiagnosticKind::MissingAuth)
        );
        assert_eq!(
            classify_release_lookup_status(StatusCode::SEE_OTHER, true, &headers),
            Some(ReleaseLookupDiagnosticKind::InsufficientPermissions)
        );
    }

    #[test]
    fn release_lookup_diagnostic_distinguishes_rate_limits() {
        let mut headers = header::HeaderMap::new();
        headers.insert(
            "x-ratelimit-remaining",
            header::HeaderValue::from_static("0"),
        );

        assert_eq!(
            classify_release_lookup_status(StatusCode::FORBIDDEN, false, &headers),
            Some(ReleaseLookupDiagnosticKind::RateLimited)
        );
        assert_eq!(
            classify_release_lookup_status(
                StatusCode::TOO_MANY_REQUESTS,
                true,
                &header::HeaderMap::new()
            ),
            Some(ReleaseLookupDiagnosticKind::RateLimited)
        );
        headers.insert(
            "x-ratelimit-remaining",
            header::HeaderValue::from_static("10"),
        );
        headers.insert(header::RETRY_AFTER, header::HeaderValue::from_static("30"));
        assert_eq!(
            classify_release_lookup_status(StatusCode::FORBIDDEN, true, &headers),
            Some(ReleaseLookupDiagnosticKind::RateLimited)
        );
        assert_eq!(
            classify_release_lookup_status(StatusCode::OK, false, &headers),
            None
        );
    }

    #[test]
    fn explicit_release_matching_requires_exact_tag() {
        let source: SourceSpec = "github:owner/repo@1.2.3".parse().expect("source");
        let error = select_release(
            &source,
            vec![Release {
                tag: "v1.2.3".to_string(),
                assets: vec![],
                stable: true,
                released_at: None,
                stability_reason: None,
            }],
        )
        .expect_err("opposite v prefix should not match");

        assert!(error.to_string().contains("no release tag matched `1.2.3`"));
        assert!(error
            .to_string()
            .contains("upstream has `v1.2.3`, so use `@v1.2.3`"));
    }

    #[test]
    fn versionless_release_selection_skips_unstable_candidates() {
        let source: SourceSpec = "github:owner/repo".parse().expect("source");
        let selected = select_release(
            &source,
            vec![
                Release {
                    tag: "v2.0.0-rc.1".to_string(),
                    assets: vec![],
                    stable: false,
                    released_at: None,
                    stability_reason: Some("github prerelease release".to_string()),
                },
                Release {
                    tag: "v1.9.0".to_string(),
                    assets: vec![],
                    stable: true,
                    released_at: None,
                    stability_reason: None,
                },
            ],
        )
        .expect("selected");

        assert_eq!(selected.release.tag, "v1.9.0");
        assert_eq!(selected.skipped.len(), 1);
        assert_eq!(selected.skipped[0].tag, "v2.0.0-rc.1");
        assert_eq!(selected.skipped[0].reason, "github prerelease release");
    }

    #[test]
    fn explicit_release_prefix_hint_preserves_exact_match_semantics() {
        let releases = [Release {
            tag: "1.2.3".to_string(),
            assets: vec![],
            stable: true,
            released_at: None,
            stability_reason: None,
        }];

        assert_eq!(
            exact_tag_prefix_hint("v1.2.3", &releases).as_deref(),
            Some(
                "Exact tag matching is unchanged; upstream has `1.2.3`, so use `@1.2.3` or omit \
                 `@version` for latest stable selection."
            )
        );
    }

    #[test]
    fn gitlab_release_stability_rejects_future_upcoming_and_prerelease_tags() {
        let now = Utc.with_ymd_and_hms(2026, 6, 19, 0, 0, 0).unwrap();
        let future = GitLabRelease {
            tag_name: "v1.0.0".to_string(),
            released_at: Some("2027-01-01T00:00:00Z".to_string()),
            upcoming_release: false,
            assets: Default::default(),
        }
        .into_release(now);
        let upcoming = GitLabRelease {
            tag_name: "v1.0.0".to_string(),
            released_at: None,
            upcoming_release: true,
            assets: Default::default(),
        }
        .into_release(now);
        let prerelease = GitLabRelease {
            tag_name: "v1.0.0-rc.1".to_string(),
            released_at: None,
            upcoming_release: false,
            assets: Default::default(),
        }
        .into_release(now);

        assert!(!future.stable);
        assert!(!upcoming.stable);
        assert!(!prerelease.stable);
    }

    #[test]
    fn gitlab_release_links_use_download_filename_for_asset_name() {
        let release = GitLabRelease {
            tag_name: "v1.0.0".to_string(),
            released_at: None,
            upcoming_release: false,
            assets: super::GitLabAssets {
                links: vec![super::GitLabLink {
                    name: "linux amd64".to_string(),
                    url: "https://gitlab.example.com/group/tool/-/releases/v1/downloads/tool-linux-amd64.tar.gz".to_string(),
                    direct_asset_url: Some(
                        "https://cdn.example.com/tool-linux-amd64.tar.gz".to_string(),
                    ),
                }],
                sources: Vec::new(),
            },
        }
        .into_release(Utc.with_ymd_and_hms(2026, 6, 19, 0, 0, 0).unwrap());

        assert_eq!(release.assets[0].name, "tool-linux-amd64.tar.gz");
    }

    #[test]
    fn gitlab_release_stability_keeps_stable_hyphenated_non_semver_tags() {
        assert!(!has_prerelease_tag("v1.2.3"));
        assert!(!has_prerelease_tag("tool-v1.2.3"));
        assert!(!has_prerelease_tag("release-2026-06-19"));
        assert!(!has_prerelease_tag("v1.2.3-linux-x64"));
        assert!(has_prerelease_tag("v1.2.3-rc.1"));
        assert!(has_prerelease_tag("tool-v1.2.3-beta.1"));
    }

    #[test]
    fn gitlab_release_ordering_uses_released_at_descending_then_tag() {
        let mut releases = vec![
            Release {
                tag: "v9.0.0".to_string(),
                assets: vec![],
                stable: true,
                released_at: Some(Utc.with_ymd_and_hms(2025, 1, 1, 0, 0, 0).unwrap()),
                stability_reason: None,
            },
            Release {
                tag: "v1.0.0".to_string(),
                assets: vec![],
                stable: true,
                released_at: Some(Utc.with_ymd_and_hms(2026, 1, 1, 0, 0, 0).unwrap()),
                stability_reason: None,
            },
        ];

        sort_gitlab_releases(&mut releases);

        assert_eq!(releases[0].tag, "v1.0.0");
        assert_eq!(releases[1].tag, "v9.0.0");
    }

    #[test]
    fn gitlab_redirect_verification_can_be_limited_to_selected_release() {
        let mut release = Release {
            tag: "v1.0.0".to_string(),
            assets: vec![ReleaseAsset {
                name: "source".to_string(),
                url: "https://example.com/source.tar.gz".to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: true,
                final_url_https: None,
                final_url: None,
            }],
            stable: true,
            released_at: None,
            stability_reason: None,
        };

        verify_gitlab_asset_redirects(std::slice::from_mut(&mut release))
            .expect("source archives are skipped without network access");

        assert_eq!(release.assets[0].final_url_https, None);
    }

    #[test]
    fn gitlab_redirect_verification_skips_non_candidate_links() {
        let mut release = Release {
            tag: "v1.0.0".to_string(),
            assets: vec![
                ReleaseAsset {
                    name: "tool-x86_64-unknown-linux-gnu.tar.gz.sha256".to_string(),
                    url: "https://127.0.0.1:9/tool.tar.gz.sha256".to_string(),
                    provider_url: None,
                    download_url: None,
                    download_auth: None,
                    download_accept: None,
                    digest: None,
                    source_archive: false,
                    final_url_https: None,
                    final_url: None,
                },
                ReleaseAsset {
                    name: "tool.dmg".to_string(),
                    url: "https://127.0.0.1:9/tool.dmg".to_string(),
                    provider_url: None,
                    download_url: None,
                    download_auth: None,
                    download_accept: None,
                    digest: None,
                    source_archive: false,
                    final_url_https: None,
                    final_url: None,
                },
                ReleaseAsset {
                    name: "latest.json".to_string(),
                    url: "https://127.0.0.1:9/latest.json".to_string(),
                    provider_url: None,
                    download_url: None,
                    download_auth: None,
                    download_accept: None,
                    digest: None,
                    source_archive: false,
                    final_url_https: None,
                    final_url: None,
                },
            ],
            stable: true,
            released_at: None,
            stability_reason: None,
        };

        verify_gitlab_asset_redirects(std::slice::from_mut(&mut release))
            .expect("non-candidate links are skipped without network access");

        assert!(release
            .assets
            .iter()
            .all(|asset| asset.final_url_https.is_none()));
    }

    #[test]
    fn gitlab_redirect_verification_rejects_credential_bearing_candidate_urls() {
        let mut release = Release {
            tag: "v1.0.0".to_string(),
            assets: vec![ReleaseAsset {
                name: "tool-x86_64-unknown-linux-gnu".to_string(),
                url: "https://token@127.0.0.1:9/tool".to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            }],
            stable: true,
            released_at: None,
            stability_reason: None,
        };

        let error = verify_gitlab_asset_redirects(std::slice::from_mut(&mut release))
            .expect_err("credential URL is rejected before probing");

        assert!(error.to_string().contains("must not include credentials"));
    }

    #[test]
    fn gitlab_redirect_verification_marks_failed_candidate_probe_ineligible() {
        let mut release = Release {
            tag: "v1.0.0".to_string(),
            assets: vec![ReleaseAsset {
                name: "tool-x86_64-unknown-linux-gnu".to_string(),
                url: "https://127.0.0.1:9/tool".to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            }],
            stable: true,
            released_at: None,
            stability_reason: None,
        };

        verify_gitlab_asset_redirects(std::slice::from_mut(&mut release))
            .expect("failed candidate probe is scoped to the asset");

        assert_eq!(release.assets[0].final_url_https, Some(false));
    }
}
