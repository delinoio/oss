#![cfg_attr(not(feature = "std"), no_std)]
#![forbid(unsafe_code)]

//! Size-first scaffolding crate for future serde derive integration.
//! Public derive macro identifiers are intentionally not stabilized yet.

pub use serde;
