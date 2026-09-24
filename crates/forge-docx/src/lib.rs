//! Native WordprocessingML authoring and region-preserving edits.
#![forbid(unsafe_code)]
mod emit;
mod import;
mod model;
pub use emit::generate;
pub use import::{Imported, Target, TargetKind, import, replace};
pub use model::*;
mod fonts;
pub use fonts::prepare_fonts;
mod measure;
pub use measure::{measure, measure_blocks};
