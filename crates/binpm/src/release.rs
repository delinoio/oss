use std::collections::BTreeSet;

use chrono::{DateTime, Utc};
use reqwest::{blocking::Client, header, Url};
use serde::{de::DeserializeOwned, Deserialize};
use tracing::{debug, info};

use crate::{
    assets::{classify_artifact, ArtifactKind},
    contract::{SourceProvider, SourceSpec},
    error::{BinpmError, Result},
};

const USER_AGENT: &str = concat!("binpm/", env!("CARGO_PKG_VERSION"));
const RELEASES_PER_PAGE: u16 = 100;
const MAX_GITLAB_ASSET_REDIRECTS: usize = 10;

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
    pub digest: Option<String>,
    pub source_archive: bool,
    pub final_url_https: Option<bool>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ReleaseSelection {
    pub release: Release,
    pub decision: String,
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

        let releases = fetch_paginated_json::<GitHubRelease>(
            &self.http,
            &url,
            Some("application/vnd.github+json"),
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
                            digest: asset.digest,
                            source_archive: false,
                            final_url_https: None,
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

        let mut releases = fetch_paginated_json::<GitLabRelease>(&self.http, &url, None)?
            .into_iter()
            .map(|release| release.into_release(self.now))
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
        let tried = matching_tag_candidates(version);
        for expected in &tried {
            if let Some(release) = releases.iter().find(|release| &release.tag == expected) {
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
                });
            }
        }

        return Err(BinpmError::ReleaseNotFound {
            package: source.to_string(),
            message: format!("no release tag matched {}", tried.join(" or ")),
        });
    }

    for release in releases {
        if release.stable {
            return Ok(ReleaseSelection {
                decision: format!("selected latest stable release `{}`", release.tag),
                release,
            });
        }

        debug!(
            source_provider = source.provider.as_str(),
            source_host = source.host,
            source_path = source.path,
            release_tag = release.tag,
            rejection_reason = release
                .stability_reason
                .as_deref()
                .unwrap_or("unstable release"),
            "Rejected unstable release"
        );
    }

    Err(BinpmError::ReleaseNotFound {
        package: source.to_string(),
        message: "no stable release found".to_string(),
    })
}

pub fn matching_tag_candidates(version: &str) -> Vec<String> {
    let mut tags = vec![version.to_string()];
    if let Some(without_v) = version.strip_prefix('v') {
        tags.push(without_v.to_string());
    } else {
        tags.push(format!("v{version}"));
    }
    tags
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

fn releases_page_url(url: &str) -> String {
    let separator = if url.contains('?') { '&' } else { '?' };
    format!("{url}{separator}per_page={RELEASES_PER_PAGE}")
}

fn fetch_paginated_json<T>(
    http: &Client,
    first_url: &str,
    accept: Option<&'static str>,
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

        let mut request = http.get(&url);
        if let Some(accept) = accept {
            request = request.header(header::ACCEPT, accept);
        }

        let response = request
            .send()
            .and_then(|response| response.error_for_status())
            .map_err(BinpmError::ReleaseLookup)?;
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
    fn into_release(self, now: DateTime<Utc>) -> Release {
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
                    url: link.url,
                    provider_url: link.direct_asset_url,
                    digest: None,
                    source_archive: false,
                    final_url_https: None,
                })
                .chain(self.assets.sources.into_iter().map(|source| ReleaseAsset {
                    name: source.format,
                    url: source.url,
                    provider_url: None,
                    digest: None,
                    source_archive: true,
                    final_url_https: None,
                }))
                .collect(),
        }
    }
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

        !suffix.is_empty() && is_semver_core(version_core)
    })
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

            let final_url = match resolve_gitlab_asset_redirect_url(&http, url) {
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
        }
    }

    Ok(())
}

fn resolve_gitlab_asset_redirect_url(http: &Client, url: &str) -> Result<String> {
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

        let response = http
            .head(&current_url)
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

fn is_https_url(url: &str) -> bool {
    url.to_ascii_lowercase().starts_with("https://")
}

#[cfg(test)]
mod tests {
    use chrono::{TimeZone, Utc};
    use reqwest::header;

    use super::{
        has_prerelease_tag, matching_tag_candidates, next_page_url, releases_page_url,
        sanitize_url, select_release, sort_gitlab_releases, validate_pagination_url,
        verify_gitlab_asset_redirects, GitHubReleaseClient, GitLabRelease, GitLabReleaseClient,
        Release,
    };
    use crate::{contract::SourceSpec, release::ReleaseAsset};

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
    fn explicit_release_matching_tries_exact_then_opposite_v_prefix() {
        assert_eq!(matching_tag_candidates("1.2.3"), ["1.2.3", "v1.2.3"]);
        assert_eq!(matching_tag_candidates("v1.2.3"), ["v1.2.3", "1.2.3"]);
    }

    #[test]
    fn explicit_release_matching_selects_opposite_v_prefix() {
        let source: SourceSpec = "github:owner/repo@1.2.3".parse().expect("source");
        let selected = select_release(
            &source,
            vec![Release {
                tag: "v1.2.3".to_string(),
                assets: vec![],
                stable: true,
                released_at: None,
                stability_reason: None,
            }],
        )
        .expect("selected");

        assert_eq!(selected.release.tag, "v1.2.3");
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
                digest: None,
                source_archive: true,
                final_url_https: None,
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
                    digest: None,
                    source_archive: false,
                    final_url_https: None,
                },
                ReleaseAsset {
                    name: "tool.dmg".to_string(),
                    url: "https://127.0.0.1:9/tool.dmg".to_string(),
                    provider_url: None,
                    digest: None,
                    source_archive: false,
                    final_url_https: None,
                },
                ReleaseAsset {
                    name: "latest.json".to_string(),
                    url: "https://127.0.0.1:9/latest.json".to_string(),
                    provider_url: None,
                    digest: None,
                    source_archive: false,
                    final_url_https: None,
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
                digest: None,
                source_archive: false,
                final_url_https: None,
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
                digest: None,
                source_archive: false,
                final_url_https: None,
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
