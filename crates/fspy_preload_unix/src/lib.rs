#![feature(c_variadic)]

// Compile as an empty crate on non-unix targets and on musl (where seccomp
// alone handles access tracking).

#[cfg(all(
    unix,
    not(target_env = "musl"),
    not(all(target_os = "macos", feature = "pnport"))
))]
mod client;
#[cfg(all(
    unix,
    not(target_env = "musl"),
    not(all(target_os = "macos", feature = "pnport"))
))]
mod interceptions;
#[cfg(all(unix, not(target_env = "musl")))]
mod libc;
#[cfg(all(unix, not(target_env = "musl")))]
mod macros;
#[cfg(all(target_os = "macos", not(feature = "pnport")))]
mod operation;

#[cfg(all(target_os = "macos", feature = "pnport"))]
mod pnport;

#[cfg(all(target_os = "macos", feature = "pnport"))]
#[used]
#[unsafe(no_mangle)]
pub static PNPORT_PRELOAD_ABI: [u8; 35] = *b"PNPORT_PRELOAD_0.1.0_FORMAT_1_READY";
