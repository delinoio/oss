use mac_usernotifications::{AuthorizationStatus, NotificationSettingStatus};

use super::{Notice, Permission, PermissionProblem, PresentationResult, Readiness};

pub struct Presented {
    id: String,
    handle: Option<mac_usernotifications::NotificationHandle>,
}
pub async fn permission(request: bool) -> Readiness {
    if mac_usernotifications::check_bundle().is_err() {
        return Readiness::unavailable(PermissionProblem::BundleRequired);
    }
    if request && mac_usernotifications::request_auth().await.is_err() {
        return Readiness::unavailable(PermissionProblem::OsUnavailable);
    }
    let Ok(settings) = mac_usernotifications::get_notification_settings().await else {
        return Readiness::unavailable(PermissionProblem::OsUnavailable);
    };
    let permission = match settings.authorization_status {
        AuthorizationStatus::NotDetermined => Permission::NotDetermined,
        AuthorizationStatus::Denied => Permission::Denied,
        AuthorizationStatus::Authorized | AuthorizationStatus::Provisional
            if settings.alert_enabled == NotificationSettingStatus::Enabled
                || settings.notification_center_enabled == NotificationSettingStatus::Enabled =>
        {
            Permission::Granted
        }
        AuthorizationStatus::Authorized | AuthorizationStatus::Provisional => Permission::Denied,
        _ => Permission::Unavailable,
    };
    Readiness::known(permission)
}
pub async fn present(notice: &Notice) -> Result<Presented, PresentationResult> {
    let handle = mac_usernotifications::Notification::new()
        .id(&notice.claim_id)
        .title(notice.kind.title())
        .message(notice.kind.body())
        .action(mac_usernotifications::Action::button(
            "open",
            crate::localization::text(crate::localization::Message::Open),
        ))
        .send()
        .await
        .map_err(|_| PresentationResult::Failed)?;
    Ok(Presented {
        id: handle.notification_id().to_owned(),
        handle: Some(handle),
    })
}
impl Presented {
    pub async fn wait(&mut self) -> bool {
        let Some(handle) = self.handle.take() else {
            return false;
        };
        match handle.response().await {
            Ok(value) => value.is_default_action() || value.action_identifier == "open",
            Err(_) => false,
        }
    }

    pub async fn close(&self) {
        mac_usernotifications::close_delivered(&self.id).await;
    }
}
