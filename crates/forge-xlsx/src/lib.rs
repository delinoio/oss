//! Native spreadsheets with caller-owned formula caches and application recalc.
#![forbid(unsafe_code)]
mod emit;
mod import;
mod model;
mod styles;
pub use emit::generate;
pub use import::{EditValue, Imported, Target, TargetKind, address, import, range, replace};
pub use model::*;
mod fonts;
pub use fonts::{check_validation_fonts, prepare_cell_fonts};
mod measure;
pub use measure::measure;
