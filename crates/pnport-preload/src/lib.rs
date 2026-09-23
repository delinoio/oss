#![feature(c_variadic)]
//! Matched native injection artifact; not a public Rust API.
#[cfg(target_os = "linux")]
mod unix;

#[used]
#[no_mangle]
pub static PNPORT_PRELOAD_ABI: [u8; 35] = *b"PNPORT_PRELOAD_0.1.0_FORMAT_1_READY";
