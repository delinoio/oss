#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[cfg(mobile)]
    #[error("the native widget bridge failed")]
    PluginInvoke(#[from] tauri::plugin::mobile::PluginInvokeError),
    #[cfg(mobile)]
    #[error("the widget bridge could not be initialized")]
    Tauri(#[from] tauri::Error),
}

pub type Result<T> = std::result::Result<T, Error>;
