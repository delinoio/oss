//! Independent PDF authoring with native shaping, pagination and semantic tags.
#![forbid(unsafe_code)]
mod emit;
mod layout;
mod model;
pub use emit::generate;
pub use layout::{Geometry, Layout, layout};
pub use model::*;
