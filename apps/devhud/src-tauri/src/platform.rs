use std::fmt;

#[derive(Clone, Copy, Debug, Eq, PartialEq, serde::Serialize)]
#[serde(rename_all = "kebab-case")]
#[allow(dead_code)] // A native build constructs only its own member of the six-target contract.
pub enum DesktopTarget {
    MacOsX64,
    MacOsArm64,
    WindowsX64,
    WindowsArm64,
    LinuxX64,
    LinuxArm64,
}

impl DesktopTarget {
    #[cfg(all(target_os = "macos", target_arch = "x86_64"))]
    pub const fn current() -> Self {
        Self::MacOsX64
    }

    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    pub const fn current() -> Self {
        Self::MacOsArm64
    }

    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    pub const fn current() -> Self {
        Self::WindowsX64
    }

    #[cfg(all(target_os = "windows", target_arch = "aarch64"))]
    pub const fn current() -> Self {
        Self::WindowsArm64
    }

    #[cfg(all(target_os = "linux", target_arch = "x86_64"))]
    pub const fn current() -> Self {
        Self::LinuxX64
    }

    #[cfg(all(target_os = "linux", target_arch = "aarch64"))]
    pub const fn current() -> Self {
        Self::LinuxArm64
    }

    pub const fn rust_target(self) -> &'static str {
        match self {
            Self::MacOsX64 => "x86_64-apple-darwin",
            Self::MacOsArm64 => "aarch64-apple-darwin",
            Self::WindowsX64 => "x86_64-pc-windows-msvc",
            Self::WindowsArm64 => "aarch64-pc-windows-msvc",
            Self::LinuxX64 => "x86_64-unknown-linux-gnu",
            Self::LinuxArm64 => "aarch64-unknown-linux-gnu",
        }
    }
}

impl fmt::Display for DesktopTarget {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.rust_target())
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum LinuxDisplayMode {
    X11,
    XWayland,
}

#[derive(Debug, Eq, PartialEq)]
pub enum PlatformError {
    NativeWaylandUnsupported,
    X11DisplayMissing,
}

impl fmt::Display for PlatformError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::NativeWaylandUnsupported => formatter.write_str(
                "native Wayland is unsupported; start DevHUD in an X11 session or through XWayland",
            ),
            Self::X11DisplayMissing => formatter
                .write_str("an X11 DISPLAY is required; DevHUD supports Ubuntu 22.04+ on X11"),
        }
    }
}

impl std::error::Error for PlatformError {}

pub fn validate_linux_display(
    display: Option<&str>,
    wayland_display: Option<&str>,
    session_type: Option<&str>,
) -> Result<LinuxDisplayMode, PlatformError> {
    let has_x11 = display.is_some_and(|value| !value.is_empty());
    let has_wayland = wayland_display.is_some_and(|value| !value.is_empty())
        || session_type.is_some_and(|value| value.eq_ignore_ascii_case("wayland"));

    match (has_x11, has_wayland) {
        (true, true) => Ok(LinuxDisplayMode::XWayland),
        (true, false) => Ok(LinuxDisplayMode::X11),
        (false, true) => Err(PlatformError::NativeWaylandUnsupported),
        (false, false) => Err(PlatformError::X11DisplayMissing),
    }
}

#[cfg(target_os = "linux")]
pub fn validate_current_environment() -> Result<LinuxDisplayMode, PlatformError> {
    let display = std::env::var("DISPLAY").ok();
    let wayland_display = std::env::var("WAYLAND_DISPLAY").ok();
    let session_type = std::env::var("XDG_SESSION_TYPE").ok();
    validate_linux_display(
        display.as_deref(),
        wayland_display.as_deref(),
        session_type.as_deref(),
    )
}

#[cfg(not(target_os = "linux"))]
pub fn validate_current_environment() -> Result<(), PlatformError> {
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{DesktopTarget, LinuxDisplayMode, PlatformError, validate_linux_display};

    #[test]
    fn target_has_an_explicit_rust_triple() {
        assert!(!DesktopTarget::current().rust_target().is_empty());
    }

    #[test]
    fn accepts_x11() {
        assert_eq!(
            validate_linux_display(Some(":99"), None, Some("x11")),
            Ok(LinuxDisplayMode::X11)
        );
    }

    #[test]
    fn treats_wayland_with_display_as_xwayland() {
        assert_eq!(
            validate_linux_display(Some(":0"), Some("wayland-0"), Some("wayland")),
            Ok(LinuxDisplayMode::XWayland)
        );
    }

    #[test]
    fn rejects_native_wayland() {
        assert_eq!(
            validate_linux_display(None, Some("wayland-0"), Some("wayland")),
            Err(PlatformError::NativeWaylandUnsupported)
        );
    }

    #[test]
    fn diagnoses_missing_x11() {
        assert_eq!(
            validate_linux_display(None, None, None),
            Err(PlatformError::X11DisplayMissing)
        );
    }
}
