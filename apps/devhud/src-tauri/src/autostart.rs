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
}

impl AutostartOutcome {
    pub(crate) const fn enabled(self) -> bool {
        match self {
            Self::Applied { enabled } | Self::Unchanged { enabled, .. } => enabled,
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
            Err(reason) => AutostartOutcome::Unchanged {
                enabled: self.backend.is_enabled().unwrap_or(false),
                reason,
            },
        }
    }

    fn apply(&self, enabled: bool) -> AutostartOutcome {
        let previous = match self.backend.is_enabled() {
            Ok(previous) => previous,
            Err(error) => {
                return AutostartOutcome::Unchanged {
                    enabled: false,
                    reason: map_error(error),
                };
            }
        };
        if previous == enabled {
            return AutostartOutcome::Applied { enabled };
        }
        if let Err(error) = self.backend.set_enabled(enabled) {
            return AutostartOutcome::Unchanged {
                enabled: previous,
                reason: map_error(error),
            };
        }
        match self.backend.is_enabled() {
            Ok(actual) if actual == enabled => AutostartOutcome::Applied { enabled },
            _ => {
                let _ = self.backend.set_enabled(previous);
                AutostartOutcome::Unchanged {
                    enabled: previous,
                    reason: AutostartFailure::OperationFailed,
                }
            }
        }
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
        match &self.coordinator {
            Some(coordinator) => coordinator.apply(enabled),
            None => AutostartOutcome::Unchanged {
                enabled: false,
                reason: self
                    .unavailable
                    .unwrap_or(AutostartFailure::OperationFailed),
            },
        }
    }

    pub(crate) fn restore(&self, enabled: Result<bool, AutostartFailure>) -> AutostartOutcome {
        match (&self.coordinator, enabled) {
            (Some(coordinator), enabled) => coordinator.restore(enabled),
            (None, Err(reason)) => AutostartOutcome::Unchanged {
                enabled: false,
                reason,
            },
            (None, Ok(_)) => AutostartOutcome::Unchanged {
                enabled: false,
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
    use std::{cell::Cell, collections::VecDeque};

    use super::*;

    struct FakeBackend {
        enabled: Cell<bool>,
        set_results: std::cell::RefCell<VecDeque<Result<(), BackendError>>>,
        lie_after_write: Cell<bool>,
    }

    impl AutostartBackend for FakeBackend {
        fn is_enabled(&self) -> Result<bool, BackendError> {
            Ok(self.enabled.get())
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
        assert!(rollback.enabled());
    }
}
