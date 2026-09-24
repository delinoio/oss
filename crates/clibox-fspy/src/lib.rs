//! Internal, versioned file-access trace model and validation.
//!
//! The record reader is deliberately strict: analysis must never mistake a
//! truncated or unsupported trace for a complete execution.

pub mod trace;
