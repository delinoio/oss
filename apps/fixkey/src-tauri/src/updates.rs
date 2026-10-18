//! Consent and scheduling rules shared by the desktop update integration.

use std::time::{Duration, Instant};

use serde::Serialize;

/// Stable updater presentation states.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum Phase {
    /// No update selected.
    #[default]
    Idle,
    /// A metadata request is in flight.
    Checking,
    /// A newer stable version awaits consent.
    Available,
    /// A consented payload is downloading and being verified.
    Downloading,
    /// Verified bytes are being installed with correction stopped.
    Installing,
    /// A check, download, verification or installation failed.
    Failed,
}

/// Safe update state sent to settings. No input or settings file is included.
#[derive(Debug, Clone, Default, Serialize)]
pub struct UpdateState {
    /// Current phase.
    pub phase: Phase,
    /// Available version.
    pub version: Option<String>,
    /// Download progress in percent, when known.
    pub progress: Option<u8>,
    /// Stable failure code.
    pub error: Option<&'static str>,
}

impl UpdateState {
    /// Starts a check only when another operation is not running.
    pub fn begin_check(&mut self) -> bool {
        if matches!(
            self.phase,
            Phase::Checking | Phase::Downloading | Phase::Installing
        ) {
            return false;
        }
        *self = Self {
            phase: Phase::Checking,
            ..Self::default()
        };
        true
    }

    /// Starts a download only after the user explicitly consents to the
    /// currently displayed available version.
    pub fn consent(&mut self) -> bool {
        if self.phase != Phase::Available || self.version.is_none() {
            return false;
        }
        self.phase = Phase::Downloading;
        true
    }

    /// Installs only a consented, verified payload after clearing correction.
    ///
    /// # Errors
    /// Returns a stable consent, signature, stop or installation failure code.
    pub fn install_payload(
        &mut self,
        bytes: &[u8],
        signature: &str,
        key: &str,
        stop: impl FnOnce() -> Result<(), &'static str>,
        install: impl FnOnce(&[u8]) -> Result<(), &'static str>,
    ) -> Result<(), &'static str> {
        if self.phase != Phase::Downloading {
            return Err("update-consent-required");
        }
        if let Err(reason) = verify_payload(bytes, signature, key) {
            self.fail(reason);
            return Err(reason);
        }
        if let Err(reason) = stop() {
            self.fail(reason);
            return Err(reason);
        }
        self.phase = Phase::Installing;
        if let Err(reason) = install(bytes) {
            self.fail(reason);
            return Err(reason);
        }
        Ok(())
    }

    /// Records a failed operation. A failed payload is never installable.
    pub const fn fail(&mut self, reason: &'static str) {
        self.phase = Phase::Failed;
        self.error = Some(reason);
        self.progress = None;
    }
}

/// Monotonic check schedule. Failures and manual checks never cause a retry
/// burst or postpone a scheduled check.
pub struct Schedule {
    next: Instant,
}
impl Schedule {
    /// Makes the startup check immediately due.
    #[must_use]
    pub const fn new(now: Instant) -> Self {
        Self { next: now }
    }

    /// Remaining delay until the next scheduled check.
    #[must_use]
    pub fn delay(&self, now: Instant) -> Duration {
        self.next.saturating_duration_since(now)
    }

    /// Claims one scheduled check and advances by 24 hours before I/O.
    pub fn claim(&mut self, now: Instant) -> bool {
        if now < self.next {
            return false;
        }
        self.next = now + Duration::from_secs(24 * 60 * 60);
        true
    }
}

/// Verifies the Tauri base64-encoded Minisign envelope against the release key.
///
/// # Errors
/// Returns one fixed code for malformed keys, signatures or modified payloads.
pub fn verify_payload(
    bytes: &[u8],
    encoded_signature: &str,
    encoded_key: &str,
) -> Result<(), &'static str> {
    use base64::Engine as _;
    let decode = |encoded: &str| {
        base64::engine::general_purpose::STANDARD
            .decode(encoded.trim())
            .ok()
            .and_then(|bytes| String::from_utf8(bytes).ok())
    };
    let key = decode(encoded_key).ok_or("update-signature-invalid")?;
    let signature = decode(encoded_signature).ok_or("update-signature-invalid")?;
    let key = minisign_verify::PublicKey::decode(&key).map_err(|_| "update-signature-invalid")?;
    let signature =
        minisign_verify::Signature::decode(&signature).map_err(|_| "update-signature-invalid")?;
    key.verify(bytes, &signature, true)
        .map_err(|_| "update-signature-invalid")
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn authentic_payload_is_accepted_and_corruption_or_missing_keys_are_rejected() {
        use base64::Engine as _;
        let encode = |bytes: &[u8]| base64::engine::general_purpose::STANDARD.encode(bytes);
        let key = encode(include_bytes!("../testdata/update-public-key.txt"));
        let signature = encode(include_bytes!("../testdata/update-signature.txt"));
        assert_eq!(verify_payload(b"test", &signature, &key), Ok(()));
        assert_eq!(
            verify_payload(b"tampered", &signature, &key),
            Err("update-signature-invalid")
        );
        assert!(verify_payload(b"test", "malformed", &key).is_err());
        assert!(verify_payload(b"test", &signature, "").is_err());
    }
    #[test]
    fn mocked_installation_preserves_settings_and_never_runs_before_consent_or_verification() {
        use base64::Engine as _;
        let encode = |bytes: &[u8]| base64::engine::general_purpose::STANDARD.encode(bytes);
        let key = encode(include_bytes!("../testdata/update-public-key.txt"));
        let signature = encode(include_bytes!("../testdata/update-signature.txt"));
        let directory = tempfile::tempdir().unwrap();
        let file = directory.path().join("settings.json");
        let mut store = crate::settings::SettingsStore::load(file.clone());
        store.save(crate::settings::Settings::default()).unwrap();
        let saved = std::fs::read(&file).unwrap();
        let stopped = std::cell::Cell::new(0);
        let installed = std::cell::Cell::new(0);
        for (consented, payload, expected_stops) in [
            (false, b"test".as_slice(), 0),
            (true, b"corrupted".as_slice(), 0),
            (true, b"test".as_slice(), 1),
        ] {
            let mut state = UpdateState {
                phase: Phase::Available,
                version: Some("1.0.0".into()),
                ..UpdateState::default()
            };
            if consented {
                assert!(state.consent());
            }
            let result = state.install_payload(
                payload,
                &signature,
                &key,
                || {
                    stopped.set(stopped.get() + 1);
                    Ok(())
                },
                |_| {
                    installed.set(installed.get() + 1);
                    Err("update-install-failed")
                },
            );
            assert!(result.is_err());
            assert_eq!(stopped.get(), expected_stops);
            assert_eq!(installed.get(), expected_stops);
            assert_eq!(std::fs::read(&file).unwrap(), saved);
            if expected_stops == 1 {
                assert_eq!(state.phase, Phase::Failed);
                assert_eq!(state.error, Some("update-install-failed"));
            }
        }
    }
    #[test]
    fn offline_failure_waits_for_the_next_day_and_never_downloads() {
        let now = Instant::now();
        let mut schedule = Schedule::new(now);
        let mut state = UpdateState::default();
        assert!(schedule.claim(now));
        assert!(state.begin_check());
        state.fail("update-check-failed");
        assert!(!state.consent());
        assert!(!schedule.claim(now + Duration::from_secs(60)));
        assert!(schedule.claim(now + Duration::from_secs(24 * 60 * 60)));
    }
    #[test]
    fn consent_is_required_and_failed_payloads_cannot_resume_installation() {
        let mut state = UpdateState::default();
        assert!(!state.consent());
        state.phase = Phase::Available;
        state.version = Some("1.0.0".into());
        assert!(state.consent());
        assert!(!state.consent());
        state.fail("update-signature-or-download-failed");
        assert!(!state.consent());
        assert!(state.begin_check());
        assert!(!state.begin_check());
    }
}

#[cfg(feature = "desktop")]
pub mod desktop {
    use std::{
        sync::{Arc, Mutex},
        time::Duration,
    };

    use serde::Deserialize;
    use tauri::AppHandle;
    use tauri_plugin_updater::{Update, UpdaterExt};

    use super::{Phase, Schedule, UpdateState};

    #[derive(Default)]
    pub struct Updater {
        pub state: UpdateState,
        candidate: Option<Update>,
        announced: Option<String>,
    }
    pub type SharedUpdater = Arc<Mutex<Updater>>;

    #[derive(Deserialize)]
    pub(super) struct Release {
        tag_name: String,
        draft: bool,
        prerelease: bool,
        pub(super) assets: Vec<Asset>,
    }
    #[derive(Deserialize)]
    pub(super) struct Asset {
        name: String,
        pub(super) browser_download_url: String,
    }

    pub fn start(app: AppHandle, shared: SharedUpdater) {
        tauri::async_runtime::spawn(async move {
            let mut schedule = Schedule::new(std::time::Instant::now());
            loop {
                if schedule.claim(std::time::Instant::now()) {
                    let _ = check(&app, &shared).await;
                }
                tokio::time::sleep(schedule.delay(std::time::Instant::now())).await;
            }
        });
    }

    pub(crate) async fn check(app: &AppHandle, shared: &SharedUpdater) -> Result<(), &'static str> {
        {
            let mut updater = shared.lock().map_err(|_| "update-state-unavailable")?;
            if !updater.state.begin_check() {
                return Err("update-busy");
            }
            updater.candidate = None;
        }
        let result = discover(app).await;
        let mut updater = shared.lock().map_err(|_| "update-state-unavailable")?;
        match result {
            Ok(Some(update)) => {
                let version = update.version.clone();
                updater.state = UpdateState {
                    phase: Phase::Available,
                    version: Some(version.clone()),
                    ..UpdateState::default()
                };
                updater.candidate = Some(update);
                if updater.announced.as_ref() != Some(&version) {
                    updater.announced = Some(version);
                    super::super::desktop::notify(app, "update-available");
                }
                drop(updater);
                Ok(())
            }
            Ok(None) => {
                updater.state = UpdateState::default();
                drop(updater);
                Ok(())
            }
            Err(reason) => {
                updater.state.fail(reason);
                drop(updater);
                tracing::warn!(reason, "fixkey update check failed");
                Err(reason)
            }
        }
    }

    pub(super) async fn latest_release<F, Fut>(
        current: &semver::Version,
        mut fetch: F,
    ) -> Result<Option<(semver::Version, String)>, &'static str>
    where
        F: FnMut(u32) -> Fut,
        Fut: std::future::Future<Output = Result<Vec<Release>, &'static str>>,
    {
        let mut selected: Option<(semver::Version, String)> = None;
        for page in 1..=10 {
            let releases = fetch(page).await?;
            let complete = releases.len() < 100;
            for release in releases {
                if release.draft || release.prerelease {
                    continue;
                }
                let Some(tag) = release.tag_name.strip_prefix("fixkey-v") else {
                    continue;
                };
                let Ok(version) = semver::Version::parse(tag) else {
                    continue;
                };
                if !version.pre.is_empty()
                    || version <= *current
                    || selected.as_ref().is_some_and(|(old, _)| old >= &version)
                {
                    continue;
                }
                let asset = release
                    .assets
                    .into_iter()
                    .find(|asset| asset.name == "fixkey-latest.json")
                    .ok_or("update-metadata-missing")?;
                let expected = format!(
                    "https://github.com/delinoio/oss/releases/download/{}/fixkey-latest.json",
                    release.tag_name
                );
                if asset.browser_download_url != expected {
                    return Err("update-metadata-invalid");
                }
                selected = Some((version, expected));
            }
            if complete {
                break;
            }
            if page == 10 {
                return Err("update-release-list-incomplete");
            }
        }
        Ok(selected)
    }

    async fn discover(app: &AppHandle) -> Result<Option<Update>, &'static str> {
        let key = option_env!("FIXKEY_UPDATER_PUBLIC_KEY")
            .map(str::trim)
            .filter(|key| !key.trim().is_empty())
            .ok_or("update-key-unavailable")?;
        let current = semver::Version::parse(env!("CARGO_PKG_VERSION"))
            .map_err(|_| "update-version-invalid")?;
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(30))
            .https_only(true)
            .user_agent(concat!("FixKey/", env!("CARGO_PKG_VERSION")))
            .build()
            .map_err(|_| "update-check-failed")?;
        let selected = latest_release(&current, |page| {
            let client = &client;
            async move {
                client
                    .get(format!(
                    "https://api.github.com/repos/delinoio/oss/releases?per_page=100&page={page}"
                ))
                    .send()
                    .await
                    .map_err(|_| "update-check-failed")?
                    .error_for_status()
                    .map_err(|_| "update-check-failed")?
                    .json()
                    .await
                    .map_err(|_| "update-metadata-invalid")
            }
        })
        .await?;
        let Some((version, endpoint)) = selected else {
            return Ok(None);
        };
        let update = app
            .updater_builder()
            .pubkey(key)
            .configure_client(|client| client.https_only(true))
            .timeout(Duration::from_secs(30))
            .endpoints(vec![
                endpoint.parse().map_err(|_| "update-metadata-invalid")?,
            ])
            .map_err(|_| "update-metadata-invalid")?
            .build()
            .map_err(|_| "update-check-failed")?
            .check()
            .await
            .map_err(|_| "update-check-failed")?;
        let Some(update) = update else {
            return Ok(None);
        };
        let payload = match crate::diagnostics::platform() {
            "macos-arm64" => "FixKey-macos-arm64.app.tar.gz",
            "windows-x64" => "FixKey-windows-x64.exe",
            "linux-x11-x64" => "FixKey-linux-x11-x64.AppImage",
            _ => return Err("update-platform-unsupported"),
        };
        let expected = format!(
            "https://github.com/delinoio/oss/releases/download/fixkey-v{version}/{payload}"
        );
        if update.version != version.to_string() || update.download_url.as_str() != expected {
            return Err("update-metadata-invalid");
        }
        Ok(Some(update))
    }

    pub(crate) async fn install(
        app: &AppHandle,
        shared: &SharedUpdater,
    ) -> Result<(), &'static str> {
        let update = {
            let mut updater = shared.lock().map_err(|_| "update-state-unavailable")?;
            if !updater.state.consent() {
                return Err("update-consent-required");
            }
            updater
                .candidate
                .take()
                .ok_or("update-candidate-unavailable")?
        };
        let mut downloaded = 0u64;
        let bytes = update
            .download(
                |chunk, total| {
                    downloaded =
                        downloaded.saturating_add(u64::try_from(chunk).unwrap_or(u64::MAX));
                    if let Ok(mut updater) = shared.lock() {
                        updater.state.progress =
                            total.filter(|total| *total > 0).and_then(|total| {
                                u8::try_from((downloaded.saturating_mul(100) / total).min(100)).ok()
                            });
                    }
                },
                || {},
            )
            .await;
        let Ok(bytes) = bytes else {
            shared
                .lock()
                .map_err(|_| "update-state-unavailable")?
                .state
                .fail("update-signature-or-download-failed");
            return Err("update-signature-or-download-failed");
        };
        let result = shared
            .lock()
            .map_err(|_| "update-state-unavailable")?
            .state
            .install_payload(
                &bytes,
                &update.signature,
                option_env!("FIXKEY_UPDATER_PUBLIC_KEY").unwrap_or(""),
                || crate::native::stop_for_update(app),
                |verified| {
                    update
                        .install(verified)
                        .map_err(|_| "update-install-failed")
                },
            );
        if let Err(reason) = result {
            crate::desktop::record_error(app, reason);
            return Err(reason);
        }
        app.restart();
    }
}

#[cfg(all(test, feature = "desktop"))]
mod discovery_tests {
    use super::desktop::{Release, latest_release};
    fn releases() -> Vec<Release> {
        serde_json::from_value(serde_json::json!([
            {"tag_name":"fixkey-v9.0.0","draft":true,"prerelease":false,"assets":[]},
            {"tag_name":"fixkey-v4.0.0-beta.1","draft":false,"prerelease":true,"assets":[]},
            {"tag_name":"other-v9.0.0","draft":false,"prerelease":false,"assets":[]},
            {"tag_name":"fixkey-v0.0.1","draft":false,"prerelease":false,"assets":[]},
            {"tag_name":"fixkey-v2.0.0","draft":false,"prerelease":false,"assets":[{"name":"fixkey-latest.json","browser_download_url":"https://github.com/delinoio/oss/releases/download/fixkey-v2.0.0/fixkey-latest.json"}]}
        ])).unwrap()
    }
    #[test]
    fn mocked_release_check_selects_only_newer_stable_project_releases() {
        let current = semver::Version::parse("1.0.0").unwrap();
        let selected =
            tauri::async_runtime::block_on(latest_release(&current, |_| async { Ok(releases()) }))
                .unwrap()
                .unwrap();
        assert_eq!(selected.0.to_string(), "2.0.0");
        assert!(selected.1.ends_with("/fixkey-v2.0.0/fixkey-latest.json"));
        let current = semver::Version::parse("2.0.0").unwrap();
        assert!(
            tauri::async_runtime::block_on(latest_release(&current, |_| async { Ok(releases()) }))
                .unwrap()
                .is_none()
        );
    }
    #[test]
    fn mocked_offline_and_unverifiable_metadata_fail_without_a_payload() {
        let current = semver::Version::parse("1.0.0").unwrap();
        assert_eq!(
            tauri::async_runtime::block_on(latest_release(&current, |_| async {
                Err("update-check-failed")
            })),
            Err("update-check-failed")
        );
        let result = tauri::async_runtime::block_on(latest_release(&current, |_| async {
            let mut releases = releases();
            releases.last_mut().unwrap().assets[0].browser_download_url =
                "https://example.invalid/update.json".into();
            Ok(releases)
        }));
        assert_eq!(result, Err("update-metadata-invalid"));
    }
}
