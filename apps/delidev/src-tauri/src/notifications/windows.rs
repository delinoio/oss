use std::sync::Mutex;

use tokio::sync::oneshot;
use windows::{
    Data::Xml::Dom::XmlDocument,
    Foundation::TypedEventHandler,
    UI::Notifications::{
        NotificationSetting, ToastDismissalReason, ToastNotification, ToastNotificationManager,
        ToastNotifier,
    },
    core::{HSTRING, IInspectable},
};

use super::{APPLICATION_ID, Notice, Permission, PermissionProblem, PresentationResult, Readiness};

pub struct Presented {
    notifier: ToastNotifier,
    toast: ToastNotification,
    activated: i64,
    dismissed: i64,
    receiver: oneshot::Receiver<bool>,
}
fn notifier() -> windows::core::Result<ToastNotifier> {
    // Use only the installed DeliDev AppUserModelID. Never borrow PowerShell
    // or another application's identity to make development delivery succeed.
    ToastNotificationManager::CreateToastNotifierWithId(&HSTRING::from(APPLICATION_ID))
}
pub async fn permission(_request: bool) -> Readiness {
    let Ok(setting) = notifier().and_then(|value| value.Setting()) else {
        return Readiness::unavailable(PermissionProblem::OsUnavailable);
    };
    if setting == NotificationSetting::Enabled {
        Readiness::known(Permission::Granted)
    } else if [
        NotificationSetting::DisabledForApplication,
        NotificationSetting::DisabledForUser,
        NotificationSetting::DisabledByGroupPolicy,
        NotificationSetting::DisabledByManifest,
    ]
    .contains(&setting)
    {
        Readiness::known(Permission::Denied)
    } else {
        Readiness::unavailable(PermissionProblem::OsUnavailable)
    }
}
pub async fn present(notice: &Notice) -> Result<Presented, PresentationResult> {
    let create = || -> windows::core::Result<Presented> {
        let xml = XmlDocument::new()?;
        // Both strings are closed native literals, never renderer content/XML.
        xml.LoadXml(&HSTRING::from(format!(
            "<toast><visual><binding \
             template=\"ToastGeneric\"><text>{}</text><text>{}</text></binding></visual><audio \
             silent=\"true\"/></toast>",
            notice.kind.title(),
            notice.kind.body()
        )))?;
        let toast = ToastNotification::CreateToastNotification(&xml)?;
        let (sender, receiver) = oneshot::channel();
        let sender = std::sync::Arc::new(Mutex::new(Some(sender)));
        let action = sender.clone();
        let activated = toast.Activated(
            &TypedEventHandler::<ToastNotification, IInspectable>::new(move |_, _| {
                if let Ok(mut value) = action.lock() {
                    if let Some(value) = value.take() {
                        let _ = value.send(true);
                    }
                }
                Ok(())
            }),
        )?;
        let dismissed =
            match toast.Dismissed(&TypedEventHandler::new(
                move |_,
                      args: windows::core::Ref<
                    windows::UI::Notifications::ToastDismissedEventArgs,
                >| {
                    // Banner timeout can leave the toast in Action Center; retain its
                    // activation handler until actual dismissal or our bounded expiry.
                    if args.as_ref().and_then(|v| v.Reason().ok())
                        == Some(ToastDismissalReason::UserCanceled)
                    {
                        if let Ok(mut value) = sender.lock() {
                            if let Some(value) = value.take() {
                                let _ = value.send(false);
                            }
                        }
                    }
                    Ok(())
                },
            )) {
                Ok(token) => token,
                Err(error) => {
                    let _ = toast.RemoveActivated(activated);
                    return Err(error);
                }
            };
        let notifier = match notifier() {
            Ok(v) => v,
            Err(error) => {
                let _ = toast.RemoveActivated(activated);
                let _ = toast.RemoveDismissed(dismissed);
                return Err(error);
            }
        };
        let value = Presented {
            notifier,
            toast,
            activated,
            dismissed,
            receiver,
        };
        value.notifier.Show(&value.toast)?;
        Ok(value)
    };
    create().map_err(|_| PresentationResult::Failed)
}
impl Presented {
    pub async fn wait(&mut self) -> bool {
        (&mut self.receiver).await.unwrap_or(false)
    }

    pub async fn close(&self) {
        if self.notifier.Hide(&self.toast).is_err() {
            tracing::warn!(operation = "notification_close", code = "os-unavailable");
        }
    }
}
impl Drop for Presented {
    fn drop(&mut self) {
        let _ = self.toast.RemoveActivated(self.activated);
        let _ = self.toast.RemoveDismissed(self.dismissed);
    }
}
