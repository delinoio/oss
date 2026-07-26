#[cfg(mobile)]
use tauri::{
    Manager, Runtime, State,
    plugin::{Builder, TauriPlugin},
};

#[cfg(mobile)]
mod mobile;

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(tag = "status", rename_all = "kebab-case")]
pub enum DiagnosticsExportOutcome {
    Exported,
    Cancelled,
}

#[cfg(mobile)]
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct ExportDiagnosticsRequest {
    file_name: String,
    bundle: String,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DiagnosticsBridgeErrorCode {
    Busy,
    PickerUnavailable,
    WriteFailed,
}

#[cfg(any(mobile, test))]
impl DiagnosticsBridgeErrorCode {
    fn from_wire_value(value: &str) -> Option<Self> {
        match value {
            "busy" => Some(Self::Busy),
            "picker-unavailable" => Some(Self::PickerUnavailable),
            "write-failed" => Some(Self::WriteFailed),
            _ => None,
        }
    }
}

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[cfg(mobile)]
    #[error("the native diagnostics export bridge failed")]
    PluginInvoke(#[from] tauri::plugin::mobile::PluginInvokeError),
    #[cfg(mobile)]
    #[error("the diagnostics export bridge could not be initialized")]
    Tauri(#[from] tauri::Error),
}

#[cfg(mobile)]
impl Error {
    pub fn code(&self) -> Option<DiagnosticsBridgeErrorCode> {
        match self {
            Self::PluginInvoke(tauri::plugin::mobile::PluginInvokeError::InvokeRejected(
                response,
            )) => response
                .code
                .as_deref()
                .and_then(DiagnosticsBridgeErrorCode::from_wire_value),
            _ => None,
        }
    }
}

#[cfg(mobile)]
pub use mobile::DevHudDiagnosticsBridge;

#[cfg(mobile)]
pub trait DevHudDiagnosticsBridgeExt<R: Runtime> {
    fn devhud_diagnostics_bridge(&self) -> State<'_, DevHudDiagnosticsBridge<R>>;
}

#[cfg(mobile)]
impl<R: Runtime, T: Manager<R>> DevHudDiagnosticsBridgeExt<R> for T {
    fn devhud_diagnostics_bridge(&self) -> State<'_, DevHudDiagnosticsBridge<R>> {
        self.state::<DevHudDiagnosticsBridge<R>>()
    }
}

#[cfg(mobile)]
pub fn init<R: Runtime>() -> TauriPlugin<R> {
    Builder::new("devhud-diagnostics")
        .setup(|app, api| {
            app.manage(mobile::init(app, api)?);
            Ok(())
        })
        .build()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_only_closed_native_error_codes() {
        assert_eq!(
            DiagnosticsBridgeErrorCode::from_wire_value("picker-unavailable"),
            Some(DiagnosticsBridgeErrorCode::PickerUnavailable)
        );
        assert_eq!(
            DiagnosticsBridgeErrorCode::from_wire_value("/private/file"),
            None
        );
    }
}
