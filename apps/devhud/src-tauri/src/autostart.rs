use serde::Serialize;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum AutostartFailure {
    PermissionDenied,
    OperationFailed,
    StorageFailed,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(tag = "status", rename_all = "kebab-case")]
pub(crate) enum AutostartOutcome {
    Applied {
        enabled: bool,
    },
    Unchanged {
        enabled: bool,
        reason: AutostartFailure,
    },
    Unknown {
        reason: AutostartFailure,
    },
}

impl AutostartOutcome {
    pub(crate) const fn enabled(self) -> Option<bool> {
        match self {
            Self::Applied { enabled } | Self::Unchanged { enabled, .. } => Some(enabled),
            Self::Unknown { .. } => None,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum BackendError {
    PermissionDenied,
    Failed,
}

trait AutostartBackend {
    fn is_enabled(&self) -> Result<bool, BackendError>;
    fn set_enabled(&self, enabled: bool) -> Result<(), BackendError>;
}

struct AutostartCoordinator<B: AutostartBackend> {
    backend: B,
}

impl<B: AutostartBackend> AutostartCoordinator<B> {
    fn restore(&self, enabled: Result<bool, AutostartFailure>) -> AutostartOutcome {
        match enabled {
            Ok(enabled) => self.apply(enabled),
            Err(reason) => match self.backend.is_enabled() {
                Ok(enabled) => AutostartOutcome::Unchanged { enabled, reason },
                Err(_) => AutostartOutcome::Unknown { reason },
            },
        }
    }

    fn apply(&self, enabled: bool) -> AutostartOutcome {
        self.apply_with_previous(enabled).1
    }

    fn apply_with_previous(&self, enabled: bool) -> (Option<bool>, AutostartOutcome) {
        let previous = match self.backend.is_enabled() {
            Ok(previous) => previous,
            Err(error) => {
                return (
                    None,
                    AutostartOutcome::Unknown {
                        reason: map_error(error),
                    },
                );
            }
        };
        if previous == enabled {
            return (Some(previous), AutostartOutcome::Applied { enabled });
        }
        if let Err(error) = self.backend.set_enabled(enabled) {
            return (
                Some(previous),
                AutostartOutcome::Unchanged {
                    enabled: previous,
                    reason: map_error(error),
                },
            );
        }
        let outcome = match self.backend.is_enabled() {
            Ok(actual) if actual == enabled => AutostartOutcome::Applied { enabled },
            _ => {
                let rollback_error = self.backend.set_enabled(previous).err();
                let rollback_reason = rollback_error.map(map_error);
                match self.backend.is_enabled() {
                    Ok(effective) => AutostartOutcome::Unchanged {
                        enabled: effective,
                        reason: rollback_reason.unwrap_or(AutostartFailure::OperationFailed),
                    },
                    Err(error) => AutostartOutcome::Unknown {
                        reason: rollback_reason.unwrap_or_else(|| map_error(error)),
                    },
                }
            }
        };
        (Some(previous), outcome)
    }
}

const fn map_error(error: BackendError) -> AutostartFailure {
    match error {
        BackendError::PermissionDenied => AutostartFailure::PermissionDenied,
        BackendError::Failed => AutostartFailure::OperationFailed,
    }
}

#[cfg(feature = "desktop-cef")]
pub(crate) struct NativeAutostart {
    inner: auto_launch::AutoLaunch,
}

#[cfg(feature = "desktop-cef")]
impl NativeAutostart {
    pub(crate) fn for_current_executable() -> Result<Self, AutostartFailure> {
        let executable = std::env::current_exe().map_err(|_| AutostartFailure::OperationFailed)?;
        let executable = select_autostart_executable(executable, std::env::var_os("APPIMAGE"));
        let Some(executable) = executable.to_str() else {
            return Err(AutostartFailure::OperationFailed);
        };
        let inner = auto_launch::AutoLaunchBuilder::new()
            .set_app_name("DevHud")
            .set_app_path(executable)
            .set_args(&["--autostart"])
            .set_macos_launch_mode(auto_launch::MacOSLaunchMode::LaunchAgent)
            .set_windows_enable_mode(auto_launch::WindowsEnableMode::CurrentUser)
            .set_linux_launch_mode(auto_launch::LinuxLaunchMode::XdgAutostart)
            .build()
            .map_err(classify_native_error)?;
        Ok(Self { inner })
    }
}

#[cfg(any(feature = "desktop-cef", test))]
fn select_autostart_executable(
    current_executable: std::path::PathBuf,
    appimage: Option<std::ffi::OsString>,
) -> std::path::PathBuf {
    #[cfg(target_os = "linux")]
    if let Some(appimage) = appimage.filter(|path| !path.is_empty()) {
        return appimage.into();
    }

    #[cfg(not(target_os = "linux"))]
    let _ = appimage;

    current_executable
}

#[cfg(feature = "desktop-cef")]
impl AutostartBackend for NativeAutostart {
    fn is_enabled(&self) -> Result<bool, BackendError> {
        self.inner.is_enabled().map_err(classify_backend_error)
    }

    fn set_enabled(&self, enabled: bool) -> Result<(), BackendError> {
        if enabled {
            self.inner.enable()
        } else {
            self.inner.disable()
        }
        .map_err(classify_backend_error)
    }
}

#[cfg(feature = "desktop-cef")]
fn classify_native_error(error: auto_launch::Error) -> AutostartFailure {
    match classify_backend_error(error) {
        BackendError::PermissionDenied => AutostartFailure::PermissionDenied,
        BackendError::Failed => AutostartFailure::OperationFailed,
    }
}

#[cfg(feature = "desktop-cef")]
fn classify_backend_error(error: auto_launch::Error) -> BackendError {
    match error {
        auto_launch::Error::Io(error) if error.kind() == std::io::ErrorKind::PermissionDenied => {
            BackendError::PermissionDenied
        }
        _ => BackendError::Failed,
    }
}

#[cfg(feature = "desktop-cef")]
pub(crate) struct AutostartState {
    coordinator: Option<AutostartCoordinator<NativeAutostart>>,
    unavailable: Option<AutostartFailure>,
}

#[cfg(feature = "desktop-cef")]
impl AutostartState {
    pub(crate) fn initialize() -> Self {
        match NativeAutostart::for_current_executable() {
            Ok(backend) => Self {
                coordinator: Some(AutostartCoordinator { backend }),
                unavailable: None,
            },
            Err(error) => Self {
                coordinator: None,
                unavailable: Some(error),
            },
        }
    }

    pub(crate) fn apply(&self, enabled: bool) -> AutostartOutcome {
        self.apply_with_previous(enabled).1
    }

    pub(crate) fn apply_with_previous(&self, enabled: bool) -> (Option<bool>, AutostartOutcome) {
        match &self.coordinator {
            Some(coordinator) => coordinator.apply_with_previous(enabled),
            None => (
                None,
                AutostartOutcome::Unknown {
                    reason: self
                        .unavailable
                        .unwrap_or(AutostartFailure::OperationFailed),
                },
            ),
        }
    }

    pub(crate) fn restore(&self, enabled: Result<bool, AutostartFailure>) -> AutostartOutcome {
        match (&self.coordinator, enabled) {
            (Some(coordinator), enabled) => coordinator.restore(enabled),
            (None, Err(reason)) => AutostartOutcome::Unknown { reason },
            (None, Ok(_)) => AutostartOutcome::Unknown {
                reason: self
                    .unavailable
                    .unwrap_or(AutostartFailure::OperationFailed),
            },
        }
    }

    pub(crate) fn current(&self) -> Option<bool> {
        self.coordinator
            .as_ref()
            .and_then(|coordinator| coordinator.backend.is_enabled().ok())
    }
}

#[cfg(test)]
mod tests {
    #[cfg(target_os = "linux")]
    use std::ffi::OsString;
    use std::{cell::Cell, collections::VecDeque, path::PathBuf};

    use super::*;

    struct FakeBackend {
        enabled: Cell<bool>,
        read_results: std::cell::RefCell<VecDeque<Result<bool, BackendError>>>,
        set_results: std::cell::RefCell<VecDeque<Result<(), BackendError>>>,
        lie_after_write: Cell<bool>,
    }

    impl AutostartBackend for FakeBackend {
        fn is_enabled(&self) -> Result<bool, BackendError> {
            self.read_results
                .borrow_mut()
                .pop_front()
                .unwrap_or_else(|| Ok(self.enabled.get()))
        }

        fn set_enabled(&self, enabled: bool) -> Result<(), BackendError> {
            let result = self.set_results.borrow_mut().pop_front().unwrap_or(Ok(()));
            if result.is_ok() && !self.lie_after_write.get() {
                self.enabled.set(enabled);
            }
            result
        }
    }

    fn coordinator(enabled: bool) -> AutostartCoordinator<FakeBackend> {
        AutostartCoordinator {
            backend: FakeBackend {
                enabled: Cell::new(enabled),
                read_results: std::cell::RefCell::new(VecDeque::new()),
                set_results: std::cell::RefCell::new(VecDeque::new()),
                lie_after_write: Cell::new(false),
            },
        }
    }

    #[test]
    fn launch_at_login_is_disabled_until_explicitly_enabled() {
        let coordinator = coordinator(false);
        assert_eq!(
            coordinator.apply(true),
            AutostartOutcome::Applied { enabled: true }
        );
        assert!(coordinator.backend.enabled.get());
    }

    #[test]
    fn failed_changes_report_the_previous_working_state() {
        for (backend_error, expected) in [
            (
                BackendError::PermissionDenied,
                AutostartFailure::PermissionDenied,
            ),
            (BackendError::Failed, AutostartFailure::OperationFailed),
        ] {
            let coordinator = coordinator(false);
            coordinator
                .backend
                .set_results
                .borrow_mut()
                .push_back(Err(backend_error));
            assert_eq!(
                coordinator.apply(true),
                AutostartOutcome::Unchanged {
                    enabled: false,
                    reason: expected
                }
            );
            assert!(!coordinator.backend.enabled.get());
        }
    }

    #[test]
    fn failed_initial_snapshot_reports_an_unknown_effective_state() {
        let coordinator = coordinator(true);
        coordinator
            .backend
            .read_results
            .borrow_mut()
            .push_back(Err(BackendError::PermissionDenied));

        assert_eq!(
            coordinator.apply(false),
            AutostartOutcome::Unknown {
                reason: AutostartFailure::PermissionDenied
            }
        );
        assert!(coordinator.backend.enabled.get());
    }

    #[test]
    fn unreadable_settings_preserve_the_existing_autostart_state() {
        let coordinator = coordinator(true);
        coordinator
            .backend
            .set_results
            .borrow_mut()
            .push_back(Err(BackendError::Failed));

        assert_eq!(
            coordinator.restore(Err(AutostartFailure::StorageFailed)),
            AutostartOutcome::Unchanged {
                enabled: true,
                reason: AutostartFailure::StorageFailed
            }
        );
        assert_eq!(coordinator.backend.set_results.borrow().len(), 1);
    }

    #[test]
    fn unverifiable_changes_are_rolled_back() {
        let coordinator = coordinator(false);
        coordinator.backend.lie_after_write.set(true);
        assert_eq!(
            coordinator.apply(true),
            AutostartOutcome::Unchanged {
                enabled: false,
                reason: AutostartFailure::OperationFailed
            }
        );
        assert!(!coordinator.backend.enabled.get());
    }

    #[test]
    fn failed_rollbacks_report_the_effective_requested_state() {
        let coordinator = coordinator(false);
        assert_eq!(
            coordinator.apply(true),
            AutostartOutcome::Applied { enabled: true }
        );
        coordinator
            .backend
            .set_results
            .borrow_mut()
            .push_back(Err(BackendError::PermissionDenied));

        let rollback = coordinator.apply(false);
        assert_eq!(
            rollback,
            AutostartOutcome::Unchanged {
                enabled: true,
                reason: AutostartFailure::PermissionDenied
            }
        );
        assert_eq!(rollback.enabled(), Some(true));
    }

    #[test]
    fn failed_verification_rollbacks_report_the_verified_effective_state() {
        let coordinator = coordinator(false);
        coordinator.backend.read_results.borrow_mut().extend([
            Ok(false),
            Err(BackendError::Failed),
            Ok(true),
        ]);
        coordinator
            .backend
            .set_results
            .borrow_mut()
            .extend([Ok(()), Err(BackendError::PermissionDenied)]);

        assert_eq!(
            coordinator.apply(true),
            AutostartOutcome::Unchanged {
                enabled: true,
                reason: AutostartFailure::PermissionDenied
            }
        );
    }

    #[test]
    fn failed_verification_reads_report_an_unknown_effective_state() {
        let coordinator = coordinator(false);
        coordinator.backend.read_results.borrow_mut().extend([
            Ok(false),
            Err(BackendError::Failed),
            Err(BackendError::Failed),
        ]);

        assert_eq!(
            coordinator.apply(true),
            AutostartOutcome::Unknown {
                reason: AutostartFailure::OperationFailed
            }
        );
    }

    #[test]
    fn applying_returns_the_verified_pre_change_state() {
        let coordinator = coordinator(true);

        assert_eq!(
            coordinator.apply_with_previous(false),
            (Some(true), AutostartOutcome::Applied { enabled: false })
        );
    }

    #[test]
    fn current_executable_is_used_without_an_appimage_path() {
        let current = PathBuf::from("/tmp/.mount_devhud/devhud");

        assert_eq!(select_autostart_executable(current.clone(), None), current);
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn persistent_appimage_path_is_used_for_linux_autostart() {
        assert_eq!(
            select_autostart_executable(
                PathBuf::from("/tmp/.mount_devhud/devhud"),
                Some(OsString::from("/opt/DevHud.AppImage")),
            ),
            PathBuf::from("/opt/DevHud.AppImage")
        );
    }
}
