//! Local desktop state and integration for `FixKey`.

#[cfg(feature = "desktop")]
pub mod desktop;
pub mod diagnostics;
#[cfg(feature = "desktop")]
mod native;
#[cfg(feature = "desktop")]
mod notifications;
pub mod preferences;
pub mod settings;
pub mod updates;
