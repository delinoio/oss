use chrono::{DateTime, Utc};
use reqwest::{blocking::Client, header};
use serde::Deserialize;
use tracing::{debug, info};

use crate::{
    contract::{SourceProvider, SourceSpec},
    error::{BinpmError, Result},
};

const USER_AGENT: &str = concat!("binpm/", env!("CARGO_PKG_VERSION"));

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
    pub source_archive: bool,
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

        let releases = self
            .http
            .get(&url)
            .header(header::ACCEPT, "application/vnd.github+json")
            .send()
            .and_then(|response| response.error_for_status())
            .map_err(BinpmError::ReleaseLookup)?
            .json::<Vec<GitHubRelease>>()
            .map_err(BinpmError::ReleaseLookup)?;

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
                            source_archive: false,
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

        let mut releases = self
            .http
            .get(&url)
            .send()
            .and_then(|response| response.error_for_status())
            .map_err(BinpmError::ReleaseLookup)?
            .json::<Vec<GitLabRelease>>()
            .map_err(BinpmError::ReleaseLookup)?
            .into_iter()
            .map(|release| release.into_release(self.now))
            .collect::<Vec<_>>();

        sort_gitlab_releases(&mut releases);
        Ok(releases)
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
    url.split(['?', '#']).next().unwrap_or(url).to_string()
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
                    name: link.name,
                    url: link.url,
                    provider_url: link.direct_asset_url,
                    source_archive: false,
                })
                .chain(self.assets.sources.into_iter().map(|source| ReleaseAsset {
                    name: source.format,
                    url: source.url,
                    provider_url: None,
                    source_archive: true,
                }))
                .collect(),
        }
    }
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
    let normalized = tag.trim_start_matches('v');
    normalized
        .split_once('-')
        .is_some_and(|(_, suffix)| !suffix.is_empty())
}

#[cfg(test)]
mod tests {
    use chrono::{TimeZone, Utc};

    use super::{
        matching_tag_candidates, select_release, sort_gitlab_releases, GitHubReleaseClient,
        GitLabRelease, GitLabReleaseClient, Release,
    };
    use crate::contract::SourceSpec;

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
}
