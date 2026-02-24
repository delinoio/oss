use std::{thread, time::Duration};

use reqwest::blocking::Client;
use semver::Version;
use serde::Deserialize;
use tracing::info;

use crate::{
    errors::{NodeupError, Result},
    types::NodeupChannel,
};

const DEFAULT_INDEX_URL: &str = "https://nodejs.org/download/release/index.json";
const DEFAULT_DOWNLOAD_BASE_URL: &str = "https://nodejs.org/download/release";
const MAX_RETRIES: usize = 3;

#[derive(Debug, Clone)]
pub struct ReleaseIndexClient {
    http: Client,
    index_url: String,
    download_base_url: String,
}

#[derive(Debug, Clone, Deserialize)]
pub struct ReleaseEntry {
    pub version: String,
    #[serde(default)]
    pub lts: serde_json::Value,
}

impl ReleaseEntry {
    pub fn is_lts(&self) -> bool {
        self.lts.as_bool().unwrap_or(false) || self.lts.as_str().is_some()
    }
}

impl ReleaseIndexClient {
    pub fn new() -> Result<Self> {
        let http = Client::builder()
            .timeout(Duration::from_secs(30))
            .build()
            .map_err(|error| {
                NodeupError::network(format!("Failed to build HTTP client: {error}"))
            })?;

        let index_url =
            std::env::var("NODEUP_INDEX_URL").unwrap_or_else(|_| DEFAULT_INDEX_URL.to_string());
        let download_base_url = std::env::var("NODEUP_DOWNLOAD_BASE_URL")
            .unwrap_or_else(|_| DEFAULT_DOWNLOAD_BASE_URL.to_string());

        Ok(Self {
            http,
            index_url,
            download_base_url,
        })
    }

    pub fn fetch_index(&self) -> Result<Vec<ReleaseEntry>> {
        for attempt in 1..=MAX_RETRIES {
            info!(
                command_path = "nodeup.release-index.fetch",
                attempt,
                url = %self.index_url,
                "Fetching Node.js release index"
            );

            match self.http.get(&self.index_url).send() {
                Ok(response) => {
                    if !response.status().is_success() {
                        if attempt == MAX_RETRIES {
                            return Err(NodeupError::network(format!(
                                "Release index request failed with status {}",
                                response.status()
                            )));
                        }
                    } else {
                        return response.json::<Vec<ReleaseEntry>>().map_err(|error| {
                            NodeupError::network(format!("Failed to decode release index: {error}"))
                        });
                    }
                }
                Err(error) => {
                    if attempt == MAX_RETRIES {
                        return Err(NodeupError::network(format!(
                            "Failed to fetch release index from {}: {error}",
                            self.index_url
                        )));
                    }
                }
            }

            thread::sleep(Duration::from_millis((attempt as u64) * 200));
        }

        Err(NodeupError::network("Exhausted release index retries"))
    }

    pub fn resolve_channel(&self, channel: NodeupChannel) -> Result<String> {
        let releases = self.fetch_index()?;
        let selected = match channel {
            NodeupChannel::Latest | NodeupChannel::Current => {
                releases.first().map(|entry| entry.version.clone())
            }
            NodeupChannel::Lts => releases
                .iter()
                .find(|entry| entry.is_lts())
                .map(|entry| entry.version.clone()),
        };

        selected.ok_or_else(|| {
            NodeupError::not_found(format!(
                "Could not resolve release for channel {channel}. Release index may be empty"
            ))
        })
    }

    pub fn ensure_version_available(&self, version: &str) -> Result<()> {
        let canonical_version = normalize_version(version);
        let releases = self.fetch_index()?;
        let found = releases
            .iter()
            .any(|entry| entry.version == canonical_version);
        let suggestion = if found {
            None
        } else {
            suggested_version_for_missing_release(&releases, &canonical_version)
        };

        info!(
            command_path = "nodeup.release-index.lookup",
            runtime = %canonical_version,
            found,
            suggestion = suggestion.as_deref().unwrap_or("none"),
            "Validated explicit runtime version against release index"
        );

        if found {
            return Ok(());
        }

        let message = match suggestion {
            Some(candidate) => format!(
                "Runtime {canonical_version} was not found in the Node.js release index. Did you \
                 mean {candidate}?"
            ),
            None => {
                format!("Runtime {canonical_version} was not found in the Node.js release index")
            }
        };

        Err(NodeupError::not_found(message))
    }

    pub fn archive_url(&self, version: &str, target_segment: &str) -> String {
        let version = normalize_version(version);
        format!(
            "{}/{}/node-{}-{}.tar.xz",
            self.download_base_url, version, version, target_segment
        )
    }

    pub fn shasums_url(&self, version: &str) -> String {
        let version = normalize_version(version);
        format!("{}/{}/SHASUMS256.txt", self.download_base_url, version)
    }

    pub fn download_base_url(&self) -> &str {
        &self.download_base_url
    }

    pub fn http(&self) -> &Client {
        &self.http
    }
}

pub fn normalize_version(version: &str) -> String {
    if version.starts_with('v') {
        version.to_string()
    } else {
        format!("v{version}")
    }
}

fn suggested_version_for_missing_release(
    releases: &[ReleaseEntry],
    requested_version: &str,
) -> Option<String> {
    let requested = Version::parse(requested_version.trim_start_matches('v')).ok()?;

    let swapped_minor_patch = format!(
        "v{}.{}.{}",
        requested.major, requested.patch, requested.minor
    );
    if releases
        .iter()
        .any(|entry| entry.version == swapped_minor_patch)
    {
        return Some(swapped_minor_patch);
    }

    releases.iter().find_map(|entry| {
        let parsed = Version::parse(entry.version.trim_start_matches('v')).ok()?;
        (parsed.major == requested.major).then(|| entry.version.clone())
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn latest_and_lts_resolution_from_stubbed_entries() {
        let entries = [
            ReleaseEntry {
                version: "v24.0.0".to_string(),
                lts: serde_json::Value::Bool(false),
            },
            ReleaseEntry {
                version: "v22.11.0".to_string(),
                lts: serde_json::Value::String("Jod".to_string()),
            },
        ];

        assert_eq!(entries[0].version, "v24.0.0");
        assert!(entries[1].is_lts());
    }

    #[test]
    fn normalize_version_prefixes_when_missing() {
        assert_eq!(normalize_version("22.1.0"), "v22.1.0");
        assert_eq!(normalize_version("v22.1.0"), "v22.1.0");
    }

    #[test]
    fn suggested_version_prefers_swapped_minor_patch_candidate() {
        let releases = vec![
            ReleaseEntry {
                version: "v20.4.0".to_string(),
                lts: serde_json::Value::String("Iron".to_string()),
            },
            ReleaseEntry {
                version: "v20.3.1".to_string(),
                lts: serde_json::Value::String("Iron".to_string()),
            },
        ];

        let suggested = suggested_version_for_missing_release(&releases, "v20.0.4");
        assert_eq!(suggested, Some("v20.4.0".to_string()));
    }

    #[test]
    fn suggested_version_falls_back_to_same_major_release() {
        let releases = vec![
            ReleaseEntry {
                version: "v22.11.0".to_string(),
                lts: serde_json::Value::String("Jod".to_string()),
            },
            ReleaseEntry {
                version: "v21.4.0".to_string(),
                lts: serde_json::Value::Bool(false),
            },
        ];

        let suggested = suggested_version_for_missing_release(&releases, "v22.0.4");
        assert_eq!(suggested, Some("v22.11.0".to_string()));
    }

    #[test]
    fn suggested_version_returns_none_when_major_is_missing() {
        let releases = vec![ReleaseEntry {
            version: "v22.11.0".to_string(),
            lts: serde_json::Value::String("Jod".to_string()),
        }];

        let suggested = suggested_version_for_missing_release(&releases, "v19.0.4");
        assert_eq!(suggested, None);
    }
}
