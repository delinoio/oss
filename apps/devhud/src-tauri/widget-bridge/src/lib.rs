#[cfg(mobile)]
use tauri::{
    Manager, Runtime, State,
    plugin::{Builder, TauriPlugin},
};

mod error;
mod models;

#[cfg(mobile)]
mod mobile;

pub use error::{Error, Result, WidgetBridgeErrorCode};
#[cfg(mobile)]
pub use mobile::DevHudWidgetBridge;
pub use models::*;

#[cfg(mobile)]
pub trait DevHudWidgetBridgeExt<R: Runtime> {
    fn devhud_widget_bridge(&self) -> State<'_, DevHudWidgetBridge<R>>;
}

#[cfg(mobile)]
impl<R: Runtime, T: Manager<R>> DevHudWidgetBridgeExt<R> for T {
    fn devhud_widget_bridge(&self) -> State<'_, DevHudWidgetBridge<R>> {
        self.state::<DevHudWidgetBridge<R>>()
    }
}

#[cfg(mobile)]
pub fn init<R: Runtime>() -> TauriPlugin<R> {
    Builder::new("devhud-widget")
        .setup(|app, api| {
            #[cfg(mobile)]
            app.manage(mobile::init(app, api)?);
            #[cfg(desktop)]
            {
                let _ = (app, api);
            }
            Ok(())
        })
        .build()
}
