pub mod ipc;

#[cfg(target_os = "macos")]
pub mod macho;

#[cfg(windows)]
pub mod windows;
