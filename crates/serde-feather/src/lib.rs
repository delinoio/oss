#![cfg_attr(not(feature = "std"), no_std)]
#![forbid(unsafe_code)]

//! Size-first scaffolding crate for future serde derive integration.
//! Stable derive macros are available behind the `derive` feature:
//! `FeatherSerialize` and `FeatherDeserialize`.

#[cfg(all(feature = "derive", not(feature = "std")))]
compile_error!("`serde-feather` feature `derive` requires `std`.");

pub use serde;
#[cfg(feature = "derive")]
pub use serde_feather_macros::{FeatherDeserialize, FeatherSerialize};
