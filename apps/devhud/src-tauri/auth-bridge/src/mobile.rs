use serde::de::DeserializeOwned;
use tauri::{
    AppHandle, Runtime,
    plugin::{PluginApi, PluginHandle},
};

use crate::{
    CallbackResponse, EmptyRequest, Error, OpenAuthorizationRequest, OperationResponse, Result,
    SessionRecord, WriteSessionRequest,
};

#[cfg(target_os = "android")]
const PLUGIN_IDENTIFIER: &str = "dev.deli.devhud.auth";

#[cfg(target_os = "ios")]
tauri::ios_plugin_binding!(init_plugin_devhud_auth);

pub fn init<R: Runtime, C: DeserializeOwned>(
    _app: &AppHandle<R>,
    api: PluginApi<R, C>,
) -> Result<DevHudAuthBridge<R>> {
    #[cfg(target_os = "android")]
    let handle = api.register_android_plugin(PLUGIN_IDENTIFIER, "DevHudAuthPlugin")?;
    #[cfg(target_os = "ios")]
    let handle = api.register_ios_plugin(init_plugin_devhud_auth)?;
    Ok(DevHudAuthBridge(handle))
}

pub struct DevHudAuthBridge<R: Runtime>(PluginHandle<R>);

impl<R: Runtime> DevHudAuthBridge<R> {
    pub fn read_session(&self) -> Result<Option<String>> {
        let response: SessionRecord = self
            .0
            .run_mobile_plugin("readSession", EmptyRequest::default())?;
        Ok(response.record)
    }

    pub fn write_session(&self, record: String) -> Result<()> {
        let response: OperationResponse = self
            .0
            .run_mobile_plugin("writeSession", WriteSessionRequest { record })?;
        response.completed.then_some(()).ok_or(Error::Rejected)
    }

    pub fn clear_session(&self) -> Result<()> {
        let response: OperationResponse = self
            .0
            .run_mobile_plugin("clearSession", EmptyRequest::default())?;
        response.completed.then_some(()).ok_or(Error::Rejected)
    }

    pub fn open_authorization(&self, url: String) -> Result<()> {
        let response: OperationResponse = self
            .0
            .run_mobile_plugin("openAuthorization", OpenAuthorizationRequest { url })?;
        response.completed.then_some(()).ok_or(Error::Rejected)
    }

    pub fn open_pull_request(&self, url: String) -> Result<()> {
        let response: OperationResponse = self
            .0
            .run_mobile_plugin("openPullRequest", OpenAuthorizationRequest { url })?;
        response.completed.then_some(()).ok_or(Error::Rejected)
    }

    pub fn take_callback(&self) -> Result<Option<String>> {
        let response: CallbackResponse = self
            .0
            .run_mobile_plugin("takeCallback", EmptyRequest::default())?;
        Ok(response.url)
    }
}
