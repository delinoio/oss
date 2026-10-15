//! Matched native injection artifact; not a public Rust API.
#[cfg(target_os = "linux")]
mod unix;

#[cfg(windows)]
mod windows;
// Reuse the pinned, patched Detours transaction helper rather than maintaining
// a second trampoline implementation. Generic fspy remains an observer.
#[cfg(windows)]
#[path = "../../fspy_preload_windows/src/windows/detour.rs"]
mod windows_detour;
#[cfg(windows)]
#[path = "../../pnport/src/windows_launch.rs"]
mod windows_launch;

#[used]
#[no_mangle]
pub static PNPORT_PRELOAD_ABI: [u8; 35] = *b"PNPORT_PRELOAD_0.1.0_FORMAT_1_READY";
