use serde::Serialize;

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[cfg(mobile)]
    #[error("the DevHud authentication bridge failed")]
    Plugin(#[from] tauri::plugin::mobile::PluginInvokeError),
    #[cfg(mobile)]
    #[error("the DevHud authentication bridge could not initialize")]
    Tauri(#[from] tauri::Error),
    #[cfg(mobile)]
    #[error("the DevHud authentication bridge rejected the operation")]
    Rejected,
}

impl Serialize for Error {
    fn serialize<S>(&self, serializer: S) -> std::result::Result<S::Ok, S::Error>
    where
        S: serde::Serializer,
    {
        serializer.serialize_str("secure-vault-unavailable")
    }
}

pub type Result<T> = std::result::Result<T, Error>;
