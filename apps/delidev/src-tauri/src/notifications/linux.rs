use std::collections::HashMap;

use futures_lite::StreamExt;
use zbus::{
    Connection, MatchRule, MessageStream, Proxy, fdo::DBusProxy, message::Type,
    names::OwnedUniqueName, zvariant::Value,
};

use super::{Notice, Permission, PermissionProblem, PresentationResult, Readiness};

// The pinned Tauri bundler names the desktop file after productName.
// Keep this basename aligned with tauri.conf.json (DeliDev.desktop).
const DESKTOP_ENTRY: &str = "DeliDev";
const SERVICE: &str = "org.freedesktop.Notifications";
const PATH: &str = "/org/freedesktop/Notifications";
pub struct Presented {
    connection: Connection,
    owner: OwnedUniqueName,
    id: u32,
    signals: MessageStream,
}

async fn endpoint() -> zbus::Result<(Connection, OwnedUniqueName)> {
    let connection = Connection::session().await?;
    let proxy = Proxy::new(&connection, SERVICE, PATH, SERVICE).await?;
    // The well-known call may activate the service. Verify capabilities again
    // through its pinned unique owner so a replacement cannot inherit a prior
    // daemon's action support between discovery and Notify.
    let _: Vec<String> = proxy.call("GetCapabilities", &()).await?;
    let owner = DBusProxy::new(&connection)
        .await?
        .get_name_owner(SERVICE.try_into()?)
        .await?;
    let pinned = Proxy::new(&connection, owner.clone(), PATH, SERVICE).await?;
    let capabilities: Vec<String> = pinned.call("GetCapabilities", &()).await?;
    if !capabilities.iter().any(|v| v == "actions") {
        return Err(zbus::Error::Unsupported);
    }
    Ok((connection, owner))
}
pub async fn permission(_request: bool) -> Readiness {
    match endpoint().await {
        // The Freedesktop interface has no universal user-permission query.
        // Service/action availability is not proof that the desktop will show
        // an alert when its own suppression or do-not-disturb policy applies.
        Ok(_) => Readiness::known(Permission::ServiceAvailable),
        Err(zbus::Error::Unsupported) => {
            Readiness::unavailable(PermissionProblem::ActionsUnavailable)
        }
        Err(_) => Readiness::unavailable(PermissionProblem::OsUnavailable),
    }
}
pub async fn present(notice: &Notice) -> Result<Presented, PresentationResult> {
    async {
        let (connection, owner) = endpoint().await?;
        // Pin the exact bus owner, including CloseNotification. A restarted
        // service may reuse numeric IDs; it must not activate or close a
        // different notification on behalf of this original handle.
        let rule = MatchRule::builder()
            .msg_type(Type::Signal)
            .sender(owner.clone())?
            .path(PATH)?
            .interface(SERVICE)?
            .build();
        let signals = MessageStream::for_match_rule(rule, &connection, Some(32)).await?;
        let proxy = Proxy::new(&connection, owner.clone(), PATH, SERVICE).await?;
        let hints = HashMap::from([
            ("desktop-entry", Value::from(DESKTOP_ENTRY)),
            ("suppress-sound", Value::from(true)),
        ]);
        let id: u32 = proxy
            .call(
                "Notify",
                &(
                    "DeliDev",
                    0u32,
                    "",
                    notice.kind.title(),
                    notice.kind.body(),
                    vec!["default", "Open DeliDev"],
                    hints,
                    -1i32,
                ),
            )
            .await?;
        Ok::<_, zbus::Error>(Presented {
            connection,
            owner,
            id,
            signals,
        })
    }
    .await
    .map_err(|_| PresentationResult::Failed)
}
impl Presented {
    pub async fn wait(&mut self) -> bool {
        while let Some(message) = self.signals.next().await {
            let Ok(message) = message else { return false };
            match message.header().member().map(|v| v.as_str()) {
                Some("ActionInvoked") => {
                    if let Ok((id, action)) = message.body().deserialize::<(u32, String)>() {
                        if id == self.id && action == "default" {
                            return true;
                        }
                    }
                }
                Some("NotificationClosed") => {
                    if let Ok((id, _)) = message.body().deserialize::<(u32, u32)>() {
                        if id == self.id {
                            return false;
                        }
                    }
                }
                _ => {}
            }
        }
        false
    }

    pub async fn close(&self) {
        if let Ok(proxy) = Proxy::new(&self.connection, self.owner.clone(), PATH, SERVICE).await {
            let result: zbus::Result<()> = proxy.call("CloseNotification", &(self.id,)).await;
            if result.is_err() {
                tracing::warn!(operation = "notification_close", code = "os-unavailable");
            }
        }
    }
}
