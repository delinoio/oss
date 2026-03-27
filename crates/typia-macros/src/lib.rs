#![forbid(unsafe_code)]

//! Proc-macro scaffold crate for `typia`.
//!
//! Public derive identifiers are intentionally not stabilized yet.

use proc_macro::TokenStream;

/// Internal-only scaffold macro used to keep the proc-macro crate wired into
/// the workspace.
///
/// This identifier is intentionally not part of the stable public contract and
/// may change.
#[doc(hidden)]
#[proc_macro]
pub fn __typia_scaffold(input: TokenStream) -> TokenStream {
    input
}
