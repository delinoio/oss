use serde::{Deserialize, Serialize};

use crate::{NativeFailure, canonical_id};

pub const APPLICATION_ID: &str = "io.delino.delidev";

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum NotificationKind {
    Request,
    Question,
    Approval,
    WorkerUnavailable,
    WorkerAvailable,
    QuotaExhausted,
    ScheduleStartFailed,
    ScheduleServerOffline,
    ScheduleWorkerOffline,
    ServerLost,
    ServerRestored,

    Succeeded,
    Failed,
    Stopped,
    SubscriptionRecovery,
}
impl NotificationKind {
    pub fn title(self) -> &'static str {
        match self {
            Self::Question => {
                crate::localization::text(crate::localization::Message::QuestionNotice)
            }
            Self::Approval => {
                crate::localization::text(crate::localization::Message::ApprovalNotice)
            }
            Self::WorkerUnavailable => {
                crate::localization::text(crate::localization::Message::WorkerUnavailableNotice)
            }
            Self::WorkerAvailable => {
                crate::localization::text(crate::localization::Message::WorkerAvailableNotice)
            }
            Self::QuotaExhausted => {
                crate::localization::text(crate::localization::Message::QuotaExhaustedNotice)
            }
            Self::ScheduleStartFailed => {
                crate::localization::text(crate::localization::Message::ScheduleStartFailedNotice)
            }
            Self::ScheduleServerOffline => {
                crate::localization::text(crate::localization::Message::ScheduleServerOfflineNotice)
            }
            Self::ScheduleWorkerOffline => {
                crate::localization::text(crate::localization::Message::ScheduleWorkerOfflineNotice)
            }
            Self::ServerLost => {
                crate::localization::text(crate::localization::Message::ServerLostNotice)
            }
            Self::ServerRestored => {
                crate::localization::text(crate::localization::Message::ServerRestoredNotice)
            }

            Self::Request => crate::localization::text(crate::localization::Message::RequestTitle),
            Self::Succeeded => {
                crate::localization::text(crate::localization::Message::SucceededTitle)
            }
            Self::Failed => crate::localization::text(crate::localization::Message::FailedTitle),
            Self::Stopped => crate::localization::text(crate::localization::Message::StoppedTitle),
            Self::SubscriptionRecovery => {
                crate::localization::text(crate::localization::Message::RecoveryTitle)
            }
        }
    }

    pub fn body(self) -> &'static str {
        crate::localization::text(crate::localization::Message::NoticeBody)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum Permission {
    NotDetermined,
    Granted,
    ServiceAvailable,
    Denied,
    Unavailable,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum PermissionProblem {
    None,
    BundleRequired,
    ActionsUnavailable,
    OsUnavailable,
    Capacity,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
pub struct Readiness {
    pub permission: Permission,
    pub problem: PermissionProblem,
}
impl Readiness {
    pub fn unavailable(problem: PermissionProblem) -> Self {
        Self {
            permission: Permission::Unavailable,
            problem,
        }
    }

    pub fn known(permission: Permission) -> Self {
        Self {
            permission,
            problem: PermissionProblem::None,
        }
    }
}
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum PresentationResult {
    Submitted,
    Denied,
    Failed,
    Uncertain,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Notice {
    pub claim_id: String,
    pub inbox_id: String,
    pub kind: NotificationKind,
}
impl Notice {
    pub fn validate(&self) -> Result<(), NativeFailure> {
        canonical_id(&self.claim_id)?;
        if matches!(
            self.kind,
            NotificationKind::ServerLost | NotificationKind::ServerRestored
        ) {
            if self.inbox_id.is_empty() {
                Ok(())
            } else {
                Err(NativeFailure::InvalidEvidence)
            }
        } else {
            canonical_id(&self.inbox_id)
        }
    }
}

#[cfg(all(feature = "native-notifications", target_os = "macos"))]
#[path = "notifications/macos.rs"]
mod platform;
#[cfg(all(feature = "native-notifications", target_os = "linux"))]
#[path = "notifications/linux.rs"]
mod platform;
#[cfg(all(feature = "native-notifications", windows))]
#[path = "notifications/windows.rs"]
mod platform;
#[cfg(feature = "native-notifications")]
pub use platform::{Presented, permission, present};

#[cfg(test)]
mod tests {
    use super::*;
    #[cfg(feature = "native-notifications")]
    #[test]
    fn platform_handle_can_live_in_an_owned_runtime_task() {
        fn sendable<T: Send>(_: T) {}
        sendable(async {
            let notice = Notice {
                claim_id: uuid::Uuid::now_v7().to_string(),
                inbox_id: uuid::Uuid::now_v7().to_string(),
                kind: NotificationKind::Request,
            };
            let _ = permission(false).await;
            if let Ok(mut handle) = present(&notice).await {
                let _ = handle.wait().await;
                handle.close().await;
            }
        });
    }
    #[test]
    fn presentation_accepts_only_original_opaque_references_and_closed_kinds() {
        let id = uuid::Uuid::now_v7().to_string();
        let raw = serde_json::json!({"claim_id":id,"inbox_id":id,"kind":"request"});
        let notice: Notice = serde_json::from_value(raw.clone()).unwrap();
        assert!(notice.validate().is_ok());
        let mut extra = raw.clone();
        extra["token"] = "not-native-presentation".into();
        assert!(serde_json::from_value::<Notice>(extra).is_err());
        let mut invalid = raw;
        invalid["inbox_id"] = "https://other.example".into();
        assert!(
            serde_json::from_value::<Notice>(invalid)
                .unwrap()
                .validate()
                .is_err()
        );
        assert_eq!(
            NotificationKind::Request.body(),
            NotificationKind::Failed.body()
        );
    }
}
