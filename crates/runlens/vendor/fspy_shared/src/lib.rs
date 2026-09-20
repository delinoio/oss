pub mod ipc;
pub mod windows_access;

#[cfg(target_os = "macos")]
pub mod macho;

#[cfg(windows)]
pub mod windows;
