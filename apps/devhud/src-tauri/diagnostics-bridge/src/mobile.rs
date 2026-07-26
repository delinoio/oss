use serde::de::DeserializeOwned;
use tauri::{
    AppHandle, Runtime,
    plugin::{PluginApi, PluginHandle},
};

use crate::{DiagnosticsExportOutcome, Error, ExportDiagnosticsRequest};

#[cfg(target_os = "android")]
const PLUGIN_IDENTIFIER: &str = "dev.deli.devhud.diagnostics";

#[cfg(target_os = "ios")]
tauri::ios_plugin_binding!(init_plugin_devhud_diagnostics);

pub fn init<R: Runtime, C: DeserializeOwned>(
    _app: &AppHandle<R>,
    api: PluginApi<R, C>,
) -> Result<DevHudDiagnosticsBridge<R>, Error> {
    #[cfg(target_os = "android")]
    let handle = api.register_android_plugin(PLUGIN_IDENTIFIER, "DevHudDiagnosticsPlugin")?;
    #[cfg(target_os = "ios")]
    let handle = api.register_ios_plugin(init_plugin_devhud_diagnostics)?;
    Ok(DevHudDiagnosticsBridge(handle))
}

pub struct DevHudDiagnosticsBridge<R: Runtime>(PluginHandle<R>);

impl<R: Runtime> DevHudDiagnosticsBridge<R> {
    pub fn export(
        &self,
        file_name: String,
        bundle: String,
    ) -> Result<DiagnosticsExportOutcome, Error> {
        Ok(self.0.run_mobile_plugin(
            "exportDiagnostics",
            ExportDiagnosticsRequest { file_name, bundle },
        )?)
    }
}
