use serde::de::DeserializeOwned;
use tauri::{
    AppHandle, Runtime,
    plugin::{PluginApi, PluginHandle},
};

use crate::{
    EmptyRequest, PrepareResetResponse, ReadConfigurationResponse, Result, WidgetRefreshResponse,
    WriteConfigurationRequest,
};

#[cfg(target_os = "android")]
const PLUGIN_IDENTIFIER: &str = "dev.deli.devhud.widget";

#[cfg(target_os = "ios")]
tauri::ios_plugin_binding!(init_plugin_devhud_widget);

pub fn init<R: Runtime, C: DeserializeOwned>(
    _app: &AppHandle<R>,
    api: PluginApi<R, C>,
) -> Result<DevHudWidgetBridge<R>> {
    #[cfg(target_os = "android")]
    let handle = api.register_android_plugin(PLUGIN_IDENTIFIER, "DevHudWidgetPlugin")?;
    #[cfg(target_os = "ios")]
    let handle = api.register_ios_plugin(init_plugin_devhud_widget)?;
    Ok(DevHudWidgetBridge(handle))
}

pub struct DevHudWidgetBridge<R: Runtime>(PluginHandle<R>);

impl<R: Runtime> DevHudWidgetBridge<R> {
    pub fn read_configuration(&self) -> Result<Option<String>> {
        let response: ReadConfigurationResponse = self
            .0
            .run_mobile_plugin("readConfiguration", EmptyRequest::default())?;
        Ok(response.record)
    }

    pub fn write_configuration(&self, record: String) -> Result<u32> {
        let response: WidgetRefreshResponse = self
            .0
            .run_mobile_plugin("writeConfiguration", WriteConfigurationRequest { record })?;
        Ok(response.refreshed_widget_count)
    }

    pub fn prepare_reset(&self) -> Result<()> {
        let response: PrepareResetResponse = self
            .0
            .run_mobile_plugin("prepareReset", EmptyRequest::default())?;
        if response.prepared {
            Ok(())
        } else {
            Err(crate::Error::ResetPrecondition)
        }
    }

    pub fn reset_configuration(&self) -> Result<u32> {
        let response: WidgetRefreshResponse = self
            .0
            .run_mobile_plugin("resetConfiguration", EmptyRequest::default())?;
        Ok(response.refreshed_widget_count)
    }
}
