//! Coordinates OS preferences with atomic settings publication.
//!
//! Failed registration or persistence retains the previous effective value.
//! If a compensating OS operation fails, correction stays paused until the
//! saved preferences have been explicitly reconciled.

use fixkey_core::session::{CorrectionSession, SessionConfig};
use serde::Serialize;

use crate::settings::{Settings, SettingsError, SettingsStore};

/// Stable OS integration failures, without user configuration or raw errors.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, thiserror::Error)]
#[serde(rename_all = "kebab-case")]
pub enum IntegrationError {
    /// Another application owns the requested shortcut.
    #[error("shortcut-conflict")]
    ShortcutConflict,
    /// The key combination cannot be registered on this platform.
    #[error("shortcut-unsupported")]
    ShortcutUnsupported,
    /// OS permissions prevent registration.
    #[error("shortcut-permission-missing")]
    ShortcutPermissionMissing,
    /// The OS rejected shortcut registration.
    #[error("shortcut-registration-failed")]
    ShortcutRegistrationFailed,
    /// The login startup setting could not be changed.
    #[error("login-startup-failed")]
    LoginStartupFailed,
    /// A native operation could not establish the final registration state.
    #[error("native-preferences-uncertain")]
    Uncertain,
}

/// The desktop's native preference operations.
pub trait SystemPreferences {
    /// Changes startup registration. An error must leave the previous
    /// registration unchanged. The operation is idempotent.
    ///
    /// # Errors
    ///
    /// Returns the stable reason for OS rejection.
    fn set_login_startup(&mut self, enabled: bool) -> Result<(), IntegrationError>;

    /// Atomically replaces the registered shortcut. Register and verify a
    /// new combination before releasing the previous one. An error must
    /// leave the previous registration intact. `None` unassigns the shortcut.
    ///
    /// # Errors
    ///
    /// Returns the stable reason for OS rejection.
    fn set_shortcut(&mut self, shortcut: Option<&str>) -> Result<(), IntegrationError>;
}

/// A failed preference transaction.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, thiserror::Error)]
#[serde(tag = "code", content = "reason", rename_all = "kebab-case")]
pub enum PreferenceError {
    /// Local settings could not be validated or persisted.
    #[error("{0}")]
    Settings(SettingsError),
    /// Native registration failed, leaving the prior value effective.
    #[error("{0}")]
    Integration(IntegrationError),
    /// Restoring native registration failed; correction remains paused.
    #[error("preferences-reconciliation-required")]
    ReconciliationRequired,
}

/// One current-user settings transaction coordinator. No input is stored in
/// settings; the engine retains its separate transient correction context.
pub struct Preferences<H> {
    store: SettingsStore,
    host: H,
    session: CorrectionSession,
    reconciliation_required: bool,
}

impl<H: SystemPreferences> Preferences<H> {
    /// Creates an inactive engine. The host grants permissions and starts
    /// input observation only after permissions and dictionaries are ready.
    #[must_use]
    pub fn new(store: SettingsStore, host: H) -> Self {
        let session = CorrectionSession::new(SessionConfig {
            languages: store.current().languages(),
            paused: true,
            permission_granted: false,
        });
        Self {
            store,
            host,
            session,
            reconciliation_required: true,
        }
    }

    /// Returns the effective, persisted settings.
    #[must_use]
    pub const fn current(&self) -> &Settings {
        self.store.current()
    }

    /// Returns the engine session for the Rust input observer only.
    pub const fn session(&mut self) -> &mut CorrectionSession {
        &mut self.session
    }

    /// Whether uncertain OS registration currently prevents correction.
    #[must_use]
    pub const fn needs_reconciliation(&self) -> bool {
        self.reconciliation_required
    }

    /// Returns the storage error without reading or exposing file contents.
    #[must_use]
    pub const fn storage_error(&self) -> Option<SettingsError> {
        self.store.error()
    }

    /// Restores the user-requested backup and reconciles OS registrations.
    ///
    /// # Errors
    ///
    /// Returns a recovery or native registration failure.
    pub fn restore_backup(&mut self) -> Result<(), PreferenceError> {
        self.session.set_paused(true);
        self.store
            .restore_backup()
            .map_err(PreferenceError::Settings)?;
        self.reconcile()
    }

    /// Reapplies the saved OS preferences without modifying the settings
    /// file. Startup and explicit recheck use this path.
    ///
    /// # Errors
    ///
    /// Preserves unreadable settings and remains inactive on native failure.
    pub fn reconcile(&mut self) -> Result<(), PreferenceError> {
        self.session.set_paused(true);
        self.reconciliation_required = true;
        if let Some(error) = self.store.error() {
            return Err(PreferenceError::Settings(error));
        }
        self.host
            .set_shortcut(self.store.current().shortcut.as_deref())
            .map_err(PreferenceError::Integration)?;
        self.host
            .set_login_startup(self.store.current().login_startup)
            .map_err(PreferenceError::Integration)?;
        self.reconciliation_required = false;
        self.session.set_paused(self.store.current().paused);
        Ok(())
    }

    /// Applies native changes, persists atomically and then updates the
    /// engine. A rejected transaction leaves the previous setting effective.
    ///
    /// # Errors
    ///
    /// Returns the registration, publication or compensation failure.
    pub fn apply(&mut self, next: Settings) -> Result<(), PreferenceError> {
        next.validate().map_err(PreferenceError::Settings)?;
        if let Some(error) = self.store.error() {
            return Err(PreferenceError::Settings(error));
        }
        if self.reconciliation_required {
            return Err(PreferenceError::ReconciliationRequired);
        }
        let previous = self.store.current().clone();
        let shortcut_changed = next.shortcut != previous.shortcut;
        let startup_changed = next.login_startup != previous.login_startup;
        if shortcut_changed && let Err(error) = self.host.set_shortcut(next.shortcut.as_deref()) {
            if error == IntegrationError::Uncertain {
                self.session.set_paused(true);
                self.session.set_permission_granted(false);
                self.reconciliation_required = true;
            }
            return Err(PreferenceError::Integration(error));
        }
        if startup_changed && let Err(error) = self.host.set_login_startup(next.login_startup) {
            self.rollback(
                &previous,
                shortcut_changed,
                error == IntegrationError::Uncertain,
            )?;
            return Err(PreferenceError::Integration(error));
        }
        if let Err(error) = self.store.save(next) {
            self.rollback(&previous, shortcut_changed, startup_changed)?;
            return Err(PreferenceError::Settings(error));
        }
        self.session.clear_transient();
        self.session.set_languages(self.store.current().languages());
        self.session.set_paused(self.store.current().paused);
        Ok(())
    }

    fn rollback(
        &mut self,
        previous: &Settings,
        shortcut: bool,
        startup: bool,
    ) -> Result<(), PreferenceError> {
        let startup_restored =
            !startup || self.host.set_login_startup(previous.login_startup).is_ok();
        let shortcut_restored =
            !shortcut || self.host.set_shortcut(previous.shortcut.as_deref()).is_ok();
        if !startup_restored || !shortcut_restored {
            self.reconciliation_required = true;
            self.session.set_paused(true);
            self.session.set_permission_granted(false);
            tracing::warn!(
                reason = "preferences-reconciliation-required",
                "fixkey native preference rollback failed"
            );
            return Err(PreferenceError::ReconciliationRequired);
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Default)]
    enum RejectedShortcut {
        #[default]
        Never,
        Unassigned,
        Combination(String),
    }

    #[derive(Default)]
    struct Host {
        startup: bool,
        shortcut: Option<String>,
        reject_shortcut: RejectedShortcut,
        reject_startup: Option<bool>,
    }
    impl SystemPreferences for Host {
        fn set_login_startup(&mut self, enabled: bool) -> Result<(), IntegrationError> {
            if self.reject_startup == Some(enabled) {
                return Err(IntegrationError::LoginStartupFailed);
            }
            self.startup = enabled;
            Ok(())
        }

        fn set_shortcut(&mut self, shortcut: Option<&str>) -> Result<(), IntegrationError> {
            let next = shortcut.map(str::to_owned);
            let rejected = match &self.reject_shortcut {
                RejectedShortcut::Never => false,
                RejectedShortcut::Unassigned => next.is_none(),
                RejectedShortcut::Combination(key) => next.as_ref() == Some(key),
            };
            if rejected {
                return Err(IntegrationError::ShortcutConflict);
            }
            self.shortcut = next;
            Ok(())
        }
    }
    fn fixture(path: std::path::PathBuf) -> Preferences<Host> {
        let mut preferences = Preferences::new(SettingsStore::load(path), Host::default());
        preferences.reconcile().unwrap();
        preferences
    }

    #[test]
    fn native_registration_failure_keeps_previous_setting_and_registration() {
        let temp = tempfile::tempdir().unwrap();
        let mut preferences = fixture(temp.path().join("settings.json"));
        let next = Settings {
            shortcut: Some("Ctrl+Alt+F".into()),
            ..Settings::default()
        };
        preferences.host.reject_shortcut =
            RejectedShortcut::Combination(next.shortcut.clone().unwrap());
        assert_eq!(
            preferences.apply(next),
            Err(PreferenceError::Integration(
                IntegrationError::ShortcutConflict
            ))
        );
        assert_eq!(preferences.current(), &Settings::default());
        assert_eq!(preferences.host.shortcut, None);
        assert!(!preferences.needs_reconciliation());
    }

    #[test]
    fn publication_failure_rolls_back_both_os_changes() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("settings.json");
        let mut preferences = fixture(path.clone());
        std::fs::create_dir(&path).unwrap();
        let next = Settings {
            login_startup: true,
            shortcut: Some("Ctrl+Alt+F".into()),
            ..Settings::default()
        };
        assert_eq!(
            preferences.apply(next),
            Err(PreferenceError::Settings(SettingsError::SaveFailed))
        );
        assert_eq!(preferences.current(), &Settings::default());
        assert!(!preferences.host.startup);
        assert_eq!(preferences.host.shortcut, None);
        assert!(!preferences.needs_reconciliation());
    }

    #[test]
    fn rollback_failure_blocks_correction_until_explicit_reconciliation() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("settings.json");
        let mut preferences = fixture(path.clone());
        std::fs::create_dir(&path).unwrap();
        preferences.host.reject_shortcut = RejectedShortcut::Unassigned;
        let next = Settings {
            shortcut: Some("Ctrl+Alt+F".into()),
            ..Settings::default()
        };
        assert_eq!(
            preferences.apply(next),
            Err(PreferenceError::ReconciliationRequired)
        );
        assert!(preferences.needs_reconciliation());
        assert_eq!(
            preferences.apply(Settings::default()),
            Err(PreferenceError::ReconciliationRequired)
        );
        preferences.host.reject_shortcut = RejectedShortcut::Never;
        preferences.reconcile().unwrap();
        assert!(!preferences.needs_reconciliation());
        assert_eq!(preferences.host.shortcut, None);
    }

    #[test]
    fn startup_failure_restores_shortcut_and_success_persists_both() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("settings.json");
        let mut preferences = fixture(path.clone());
        preferences.host.reject_startup = Some(true);
        let next = Settings {
            login_startup: true,
            shortcut: Some("Ctrl+Alt+F".into()),
            ..Settings::default()
        };
        assert_eq!(
            preferences.apply(next.clone()),
            Err(PreferenceError::Integration(
                IntegrationError::LoginStartupFailed
            ))
        );
        assert_eq!(preferences.host.shortcut, None);
        preferences.host.reject_startup = None;
        preferences.apply(next.clone()).unwrap();
        assert!(preferences.host.startup);
        assert_eq!(SettingsStore::load(path).current(), &next);
    }
}
