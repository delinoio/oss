use serde::Serialize;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(tag = "status", rename_all = "kebab-case")]
pub(crate) enum UpdateActionOutcome {
    Unavailable { reason: UpdateActionFailure },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[allow(dead_code)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum UpdateActionFailure {
    ScopedUpdaterUnavailable,
    Offline,
    RateLimited,
    InvalidSignature,
    InstallationFailed,
}

/// The production boundary intentionally has no network implementation in
/// 0.1.0. A future updater must be injected here after its GitHub-only scope,
/// signature checks, and capabilities are reviewed.
#[derive(Default)]
pub(crate) struct UpdateActionBoundary;

impl UpdateActionBoundary {
    pub(crate) const fn request(&self) -> UpdateActionOutcome {
        UpdateActionOutcome::Unavailable {
            reason: UpdateActionFailure::ScopedUpdaterUnavailable,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn preview_update_action_is_typed_and_has_no_network_implementation() {
        assert_eq!(
            UpdateActionBoundary.request(),
            UpdateActionOutcome::Unavailable {
                reason: UpdateActionFailure::ScopedUpdaterUnavailable
            }
        );
    }

    #[test]
    fn future_updater_failures_remain_closed_and_value_free() {
        let failures = [
            UpdateActionFailure::ScopedUpdaterUnavailable,
            UpdateActionFailure::Offline,
            UpdateActionFailure::RateLimited,
            UpdateActionFailure::InvalidSignature,
            UpdateActionFailure::InstallationFailed,
        ];

        assert_eq!(
            failures.map(|failure| serde_json::to_string(&failure).unwrap()),
            [
                "\"scoped-updater-unavailable\"",
                "\"offline\"",
                "\"rate-limited\"",
                "\"invalid-signature\"",
                "\"installation-failed\"",
            ]
        );
    }
}
