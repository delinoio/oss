#[cfg(mobile)]
use tauri::{
    Manager, Runtime, State,
    plugin::{Builder, TauriPlugin},
};

mod error;
#[cfg(mobile)]
mod mobile;
mod models;

pub use error::{Error, Result};
#[cfg(mobile)]
pub use mobile::DevHudAuthBridge;
pub use models::*;

#[cfg(mobile)]
pub trait DevHudAuthBridgeExt<R: Runtime> {
    fn devhud_auth_bridge(&self) -> State<'_, DevHudAuthBridge<R>>;
}

#[cfg(mobile)]
impl<R: Runtime, T: Manager<R>> DevHudAuthBridgeExt<R> for T {
    fn devhud_auth_bridge(&self) -> State<'_, DevHudAuthBridge<R>> {
        self.state::<DevHudAuthBridge<R>>()
    }
}

#[cfg(mobile)]
pub fn init<R: Runtime>() -> TauriPlugin<R> {
    Builder::new("devhud-auth")
        .setup(|app, api| {
            app.manage(mobile::init(app, api)?);
            Ok(())
        })
        .build()
}
