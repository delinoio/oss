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

#[doc(hidden)]
pub mod __private {
    #[cfg(feature = "std")]
    pub type OwnedFieldName = std::string::String;

    #[inline]
    pub fn select_field_index(field_name: &str, known_fields: &[&str]) -> Option<usize> {
        known_fields
            .iter()
            .position(|candidate| *candidate == field_name)
    }
}
