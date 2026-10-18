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

    /// Claims one scheduled check and advances by 24 hours before I/O.
    pub fn claim(&mut self, now: Instant) -> bool {
        if now < self.next {
            return false;
        }
        self.next = now + Duration::from_secs(24 * 60 * 60);
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;
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
    use tauri::{AppHandle, Manager};
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
    struct Release {
        tag_name: String,
        draft: bool,
        prerelease: bool,
        assets: Vec<Asset>,
    }
    #[derive(Deserialize)]
    struct Asset {
        name: String,
        browser_download_url: String,
    }

    pub fn start(app: AppHandle, shared: SharedUpdater) {
        tauri::async_runtime::spawn(async move {
            let mut schedule = Schedule::new(std::time::Instant::now());
            loop {
                if schedule.claim(std::time::Instant::now()) {
                    let _ = check(&app, &shared).await;
                }
                tokio::time::sleep(Duration::from_secs(24 * 60 * 60)).await;
            }
        });
    }

    pub async fn check(app: &AppHandle, shared: &SharedUpdater) -> Result<(), &'static str> {
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
                Ok(())
            }
            Ok(None) => {
                updater.state = UpdateState::default();
                Ok(())
            }
            Err(reason) => {
                updater.state.fail(reason);
                tracing::warn!(reason, "fixkey update check failed");
                Err(reason)
            }
        }
    }

    async fn discover(app: &AppHandle) -> Result<Option<Update>, &'static str> {
        let key = option_env!("FIXKEY_UPDATER_PUBLIC_KEY")
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
        let mut selected: Option<(semver::Version, String)> = None;
        for page in 1..=10 {
            let releases: Vec<Release> = client
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
                .map_err(|_| "update-metadata-invalid")?;
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
                    || version <= current
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
        let Some((version, endpoint)) = selected else {
            return Ok(None);
        };
        let update = app
            .updater_builder()
            .pubkey(key)
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
        if update.version != version.to_string()
            || update.download_url.scheme() != "https"
            || update.download_url.host_str() != Some("github.com")
        {
            return Err("update-metadata-invalid");
        }
        Ok(Some(update))
    }

    pub async fn install(app: &AppHandle, shared: &SharedUpdater) -> Result<(), &'static str> {
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
        let bytes = if let Ok(bytes) = bytes {
            bytes
        } else {
            shared
                .lock()
                .map_err(|_| "update-state-unavailable")?
                .state
                .fail("update-signature-or-download-failed");
            return Err("update-signature-or-download-failed");
        };
        // The updater verifies the detached signature before returning bytes.
        // Stop input observation and erase transient state before the first
        // installation write; never touch the user's settings during recovery.
        app.state::<super::super::desktop::Shared>()
            .0
            .lock()
            .map_err(|_| "desktop-state-unavailable")?
            .stop_for_update();
        super::super::desktop::hide_candidates(app);
        shared
            .lock()
            .map_err(|_| "update-state-unavailable")?
            .state
            .phase = Phase::Installing;
        if update.install(&bytes).is_err() {
            shared
                .lock()
                .map_err(|_| "update-state-unavailable")?
                .state
                .fail("update-install-failed");
            return Err("update-install-failed");
        }
        app.restart();
    }
}
