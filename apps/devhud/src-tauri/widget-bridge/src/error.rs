#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum WidgetBridgeErrorCode {
    Corrupt,
    FutureVersion,
    Incompatible,
    RefreshFailed,
    StorageUnavailable,
    WriteFailed,
}

#[cfg(any(mobile, test))]
impl WidgetBridgeErrorCode {
    fn from_wire_value(value: &str) -> Option<Self> {
        match value {
            "corrupt" => Some(Self::Corrupt),
            "future-version" => Some(Self::FutureVersion),
            "incompatible" => Some(Self::Incompatible),
            "refresh-failed" => Some(Self::RefreshFailed),
            "shared-store-unavailable" | "storage-unavailable" => Some(Self::StorageUnavailable),
            "write-failed" => Some(Self::WriteFailed),
            _ => None,
        }
    }
}

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[cfg(mobile)]
    #[error("the native widget bridge failed")]
    PluginInvoke(#[from] tauri::plugin::mobile::PluginInvokeError),
    #[cfg(mobile)]
    #[error("the widget bridge could not be initialized")]
    Tauri(#[from] tauri::Error),
}

#[cfg(mobile)]
impl Error {
    pub fn code(&self) -> Option<WidgetBridgeErrorCode> {
        match self {
            Self::PluginInvoke(tauri::plugin::mobile::PluginInvokeError::InvokeRejected(
                response,
            )) => response
                .code
                .as_deref()
                .and_then(WidgetBridgeErrorCode::from_wire_value),
            _ => None,
        }
    }
}

pub type Result<T> = std::result::Result<T, Error>;

#[cfg(test)]
mod tests {
    use super::WidgetBridgeErrorCode;

    #[test]
    fn parses_platform_error_codes_without_exposing_native_messages() {
        assert_eq!(
            WidgetBridgeErrorCode::from_wire_value("future-version"),
            Some(WidgetBridgeErrorCode::FutureVersion)
        );
        assert_eq!(
            WidgetBridgeErrorCode::from_wire_value("shared-store-unavailable"),
            Some(WidgetBridgeErrorCode::StorageUnavailable)
        );
        assert_eq!(WidgetBridgeErrorCode::from_wire_value("unknown"), None);
    }
}
