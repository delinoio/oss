use std::{
    fs,
    io::Write,
    path::PathBuf,
    thread,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use tempfile::NamedTempFile;
use tracing::{info, warn};

use crate::{
    errors::{NodeupError, Result},
    types::NodeupChannel,
};

const DEFAULT_INDEX_URL: &str = "https://nodejs.org/download/release/index.json";
const DEFAULT_DOWNLOAD_BASE_URL: &str = "https://nodejs.org/download/release";
const RELEASE_INDEX_TTL_ENV: &str = "NODEUP_RELEASE_INDEX_TTL_SECONDS";
const DEFAULT_RELEASE_INDEX_TTL_SECONDS: u64 = 600;
const MAX_RETRIES: usize = 3;
const RELEASE_INDEX_CACHE_SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone)]
pub struct ReleaseIndexClient {
    http: Client,
    index_url: String,
    download_base_url: String,
    cache_file: PathBuf,
    cache_ttl: Duration,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ReleaseEntry {
    pub version: String,
    #[serde(default)]
    pub lts: serde_json::Value,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct ReleaseIndexCachePayload {
    schema_version: u32,
    fetched_at_epoch_seconds: u64,
    entries: Vec<ReleaseEntry>,
}

#[derive(Debug, Clone)]
struct CachedReleaseIndex {
    entries: Vec<ReleaseEntry>,
    age_seconds: u64,
}

impl ReleaseEntry {
    pub fn is_lts(&self) -> bool {
        self.lts.as_bool().unwrap_or(false) || self.lts.as_str().is_some()
    }
}

impl ReleaseIndexClient {
    pub fn new(cache_file: PathBuf, cache_ttl: Duration) -> Result<Self> {
        let http = Self::build_http_client()?;
        let index_url =
            std::env::var("NODEUP_INDEX_URL").unwrap_or_else(|_| DEFAULT_INDEX_URL.to_string());
        let download_base_url = std::env::var("NODEUP_DOWNLOAD_BASE_URL")
            .unwrap_or_else(|_| DEFAULT_DOWNLOAD_BASE_URL.to_string());

        Ok(Self {
            http,
            index_url,
            download_base_url,
            cache_file,
            cache_ttl,
        })
    }

    pub fn cache_ttl_from_env() -> Duration {
        match std::env::var(RELEASE_INDEX_TTL_ENV) {
            Ok(raw) => match raw.parse::<i64>() {
                Ok(seconds) if seconds > 0 => Duration::from_secs(seconds as u64),
                _ => {
                    warn!(
                        command_path = "nodeup.release-index.cache",
                        env_var = RELEASE_INDEX_TTL_ENV,
                        env_value = %raw,
                        fallback_seconds = DEFAULT_RELEASE_INDEX_TTL_SECONDS,
                        "Invalid release index TTL value; using default"
                    );
                    Duration::from_secs(DEFAULT_RELEASE_INDEX_TTL_SECONDS)
                }
            },
            Err(_) => Duration::from_secs(DEFAULT_RELEASE_INDEX_TTL_SECONDS),
        }
    }

    fn build_http_client() -> Result<Client> {
        let http = Client::builder()
            .timeout(Duration::from_secs(30))
            .build()
            .map_err(|error| {
                NodeupError::network(format!("Failed to build HTTP client: {error}"))
            })?;
        Ok(http)
    }

    #[cfg(test)]
    fn with_urls(
        cache_file: PathBuf,
        cache_ttl: Duration,
        index_url: String,
        download_base_url: String,
    ) -> Result<Self> {
        let http = Self::build_http_client()?;
        Ok(Self {
            http,
            index_url,
            download_base_url,
            cache_file,
            cache_ttl,
        })
    }

    pub fn fetch_index(&self) -> Result<Vec<ReleaseEntry>> {
        let now_epoch_seconds = unix_epoch_seconds();
        let ttl_seconds = self.cache_ttl.as_secs();
        let cached = self.read_cached_index(now_epoch_seconds);

        match cached.as_ref() {
            Some(cached_index) if cached_index.age_seconds <= ttl_seconds => {
                info!(
                    command_path = "nodeup.release-index.cache",
                    cache_path = %self.cache_file.display(),
                    outcome = "hit",
                    age_seconds = cached_index.age_seconds,
                    ttl_seconds,
                    "Using cached Node.js release index"
                );
                return Ok(cached_index.entries.clone());
            }
            Some(cached_index) => {
                info!(
                    command_path = "nodeup.release-index.cache",
                    cache_path = %self.cache_file.display(),
                    outcome = "expired",
                    age_seconds = cached_index.age_seconds,
                    ttl_seconds,
                    "Release index cache expired; refreshing from network"
                );
            }
            None => {
                info!(
                    command_path = "nodeup.release-index.cache",
                    cache_path = %self.cache_file.display(),
                    outcome = "miss",
                    ttl_seconds,
                    "Release index cache miss; fetching from network"
                );
            }
        }

        match self.fetch_index_from_network() {
            Ok(entries) => {
                info!(
                    command_path = "nodeup.release-index.cache",
                    cache_path = %self.cache_file.display(),
                    outcome = "refresh",
                    ttl_seconds,
                    entries_len = entries.len(),
                    "Fetched release index from network"
                );
                if let Err(error) = self.write_cache(&entries, now_epoch_seconds) {
                    warn!(
                        command_path = "nodeup.release-index.cache",
                        cache_path = %self.cache_file.display(),
                        outcome = "write-failure",
                        ttl_seconds,
                        error = %error.message,
                        "Failed to persist release index cache"
                    );
                }
                Ok(entries)
            }
            Err(error) => {
                if let Some(stale_cache) = cached {
                    warn!(
                        command_path = "nodeup.release-index.cache",
                        cache_path = %self.cache_file.display(),
                        outcome = "stale-fallback",
                        age_seconds = stale_cache.age_seconds,
                        ttl_seconds,
                        error = %error.message,
                        "Using stale release index cache after refresh failure"
                    );
                    return Ok(stale_cache.entries);
                }
                Err(error)
            }
        }
    }

    fn fetch_index_from_network(&self) -> Result<Vec<ReleaseEntry>> {
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

    fn read_cached_index(&self, now_epoch_seconds: u64) -> Option<CachedReleaseIndex> {
        if !self.cache_file.exists() {
            return None;
        }

        let content = match fs::read_to_string(&self.cache_file) {
            Ok(content) => content,
            Err(error) => {
                warn!(
                    command_path = "nodeup.release-index.cache",
                    cache_path = %self.cache_file.display(),
                    outcome = "miss",
                    reason = "read-failed",
                    error = %error,
                    "Failed to read release index cache; treating as cache miss"
                );
                return None;
            }
        };

        let payload = match serde_json::from_str::<ReleaseIndexCachePayload>(&content) {
            Ok(payload) => payload,
            Err(error) => {
                warn!(
                    command_path = "nodeup.release-index.cache",
                    cache_path = %self.cache_file.display(),
                    outcome = "miss",
                    reason = "decode-failed",
                    error = %error,
                    "Failed to decode release index cache; treating as cache miss"
                );
                return None;
            }
        };

        if payload.schema_version != RELEASE_INDEX_CACHE_SCHEMA_VERSION {
            warn!(
                command_path = "nodeup.release-index.cache",
                cache_path = %self.cache_file.display(),
                outcome = "miss",
                reason = "schema-mismatch",
                schema_version = payload.schema_version,
                expected_schema_version = RELEASE_INDEX_CACHE_SCHEMA_VERSION,
                "Release index cache schema mismatch; treating as cache miss"
            );
            return None;
        }

        if payload.fetched_at_epoch_seconds > now_epoch_seconds {
            warn!(
                command_path = "nodeup.release-index.cache",
                cache_path = %self.cache_file.display(),
                outcome = "miss",
                reason = "invalid-timestamp",
                fetched_at_epoch_seconds = payload.fetched_at_epoch_seconds,
                now_epoch_seconds,
                "Release index cache timestamp is invalid; treating as cache miss"
            );
            return None;
        }

        Some(CachedReleaseIndex {
            entries: payload.entries,
            age_seconds: now_epoch_seconds - payload.fetched_at_epoch_seconds,
        })
    }

    fn write_cache(&self, entries: &[ReleaseEntry], fetched_at_epoch_seconds: u64) -> Result<()> {
        let parent = self.cache_file.parent().ok_or_else(|| {
            NodeupError::internal(format!(
                "Cannot determine release index cache parent for {}",
                self.cache_file.display()
            ))
        })?;
        fs::create_dir_all(parent)?;

        let payload = ReleaseIndexCachePayload {
            schema_version: RELEASE_INDEX_CACHE_SCHEMA_VERSION,
            fetched_at_epoch_seconds,
            entries: entries.to_vec(),
        };
        let serialized = serde_json::to_vec(&payload)?;
        let mut temp_file = NamedTempFile::new_in(parent)?;
        temp_file.write_all(&serialized)?;
        temp_file.flush()?;
        temp_file.persist(&self.cache_file).map_err(|error| {
            NodeupError::internal(format!(
                "Failed to persist release index cache {}: {error}",
                self.cache_file.display()
            ))
        })?;
        Ok(())
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

fn unix_epoch_seconds() -> u64 {
    match SystemTime::now().duration_since(UNIX_EPOCH) {
        Ok(duration) => duration.as_secs(),
        Err(_) => 0,
    }
}

pub fn normalize_version(version: &str) -> String {
    if version.starts_with('v') {
        version.to_string()
    } else {
        format!("v{version}")
    }
}

#[cfg(test)]
mod tests {
    use std::{fs, time::Duration};

    use httpmock::{Method::GET, MockServer};
    use tempfile::tempdir;

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
    fn fresh_cache_hit_skips_network() {
        let dir = tempdir().unwrap();
        let cache_file = dir.path().join("release-index.json");
        let now = unix_epoch_seconds();
        let cached_entries = vec![ReleaseEntry {
            version: "v24.14.0".to_string(),
            lts: serde_json::Value::String("Krypton".to_string()),
        }];
        let payload = ReleaseIndexCachePayload {
            schema_version: RELEASE_INDEX_CACHE_SCHEMA_VERSION,
            fetched_at_epoch_seconds: now,
            entries: cached_entries.clone(),
        };
        fs::write(&cache_file, serde_json::to_vec(&payload).unwrap()).unwrap();

        let server = MockServer::start();
        let index_mock = server.mock(|when, then| {
            when.method(GET).path("/index.json");
            then.status(200)
                .header("content-type", "application/json")
                .body("[]");
        });

        let client = ReleaseIndexClient::with_urls(
            cache_file,
            Duration::from_secs(600),
            server.url("/index.json"),
            server.url("/release"),
        )
        .unwrap();

        let fetched = client.fetch_index().unwrap();
        assert_eq!(fetched.len(), 1);
        assert_eq!(fetched[0].version, "v24.14.0");
        index_mock.assert_calls(0);
    }

    #[test]
    fn expired_cache_refreshes_from_network_and_rewrites_cache() {
        let dir = tempdir().unwrap();
        let cache_file = dir.path().join("release-index.json");
        let now = unix_epoch_seconds();
        let stale_payload = ReleaseIndexCachePayload {
            schema_version: RELEASE_INDEX_CACHE_SCHEMA_VERSION,
            fetched_at_epoch_seconds: now.saturating_sub(3600),
            entries: vec![ReleaseEntry {
                version: "v20.0.0".to_string(),
                lts: serde_json::Value::Bool(false),
            }],
        };
        fs::write(&cache_file, serde_json::to_vec(&stale_payload).unwrap()).unwrap();

        let server = MockServer::start();
        let index_mock = server.mock(|when, then| {
            when.method(GET).path("/index.json");
            then.status(200)
                .header("content-type", "application/json")
                .body(
                    r#"[{"version":"v24.14.0","lts":"Krypton"},{"version":"v23.0.0","lts":false}]"#,
                );
        });

        let client = ReleaseIndexClient::with_urls(
            cache_file.clone(),
            Duration::from_secs(600),
            server.url("/index.json"),
            server.url("/release"),
        )
        .unwrap();

        let fetched = client.fetch_index().unwrap();
        assert_eq!(fetched[0].version, "v24.14.0");
        index_mock.assert_calls(1);

        let rewritten = fs::read_to_string(&cache_file).unwrap();
        let payload: ReleaseIndexCachePayload = serde_json::from_str(&rewritten).unwrap();
        assert_eq!(payload.schema_version, RELEASE_INDEX_CACHE_SCHEMA_VERSION);
        assert_eq!(payload.entries[0].version, "v24.14.0");
        assert!(payload.fetched_at_epoch_seconds >= stale_payload.fetched_at_epoch_seconds);
    }

    #[test]
    fn stale_cache_fallback_is_used_when_refresh_fails() {
        let dir = tempdir().unwrap();
        let cache_file = dir.path().join("release-index.json");
        let now = unix_epoch_seconds();
        let stale_payload = ReleaseIndexCachePayload {
            schema_version: RELEASE_INDEX_CACHE_SCHEMA_VERSION,
            fetched_at_epoch_seconds: now.saturating_sub(3600),
            entries: vec![ReleaseEntry {
                version: "v22.11.0".to_string(),
                lts: serde_json::Value::String("Jod".to_string()),
            }],
        };
        fs::write(&cache_file, serde_json::to_vec(&stale_payload).unwrap()).unwrap();

        let server = MockServer::start();
        let index_mock = server.mock(|when, then| {
            when.method(GET).path("/index.json");
            then.status(500);
        });

        let client = ReleaseIndexClient::with_urls(
            cache_file,
            Duration::from_secs(60),
            server.url("/index.json"),
            server.url("/release"),
        )
        .unwrap();

        let fetched = client.fetch_index().unwrap();
        assert_eq!(fetched[0].version, "v22.11.0");
        index_mock.assert_calls(MAX_RETRIES);
    }

    #[test]
    fn cache_decode_failure_becomes_miss_and_recovers_with_network_refresh() {
        let dir = tempdir().unwrap();
        let cache_file = dir.path().join("release-index.json");
        fs::write(&cache_file, "{invalid-json").unwrap();

        let server = MockServer::start();
        let index_mock = server.mock(|when, then| {
            when.method(GET).path("/index.json");
            then.status(200)
                .header("content-type", "application/json")
                .body(r#"[{"version":"v24.14.0","lts":"Krypton"}]"#);
        });

        let client = ReleaseIndexClient::with_urls(
            cache_file,
            Duration::from_secs(600),
            server.url("/index.json"),
            server.url("/release"),
        )
        .unwrap();

        let fetched = client.fetch_index().unwrap();
        assert_eq!(fetched[0].version, "v24.14.0");
        index_mock.assert_calls(1);
    }
}
