// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `report.json` schema v1. Everything except `measurements` is deterministic
//! for identical inputs, configuration, options and tool version.
use std::collections::BTreeMap;

use serde::Serialize;

use crate::{
    compiler::Snapshot,
    passes::{PassId, PassSet},
    pipeline::Outcome,
    plan::{Phase, Reason},
};

pub const SCHEMA_VERSION: u8 = 1;

#[derive(Serialize)]
pub struct Report {
    pub schema_version: u8,
    pub tool_version: &'static str,
    pub compiler_revision: &'static str,
    pub files: usize,
    pub object_regions: usize,
    pub analysis_scope: &'static str,
    pub no_change_reason: Option<&'static str>,
    pub properties: usize,
    pub renamed_properties: usize,
    pub changed_occurrences: usize,
    pub preservation_reasons: BTreeMap<String, usize>,
    pub passes: PassConfig,
    pub rounds: usize,
    pub round_limit_reached: bool,
    pub size: Size,
    pub pass_summary: BTreeMap<PassId, PassSummary>,
    pub transformations: Vec<Transformation>,
    pub skipped: Vec<Skipped>,
    /// Bundle mode: chunks that a boundary chunk can import. They are not
    /// optimized or renamed.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub frozen_chunks: Vec<FrozenChunk>,
    pub measurements: Measurements,
}

/// A bundle chunk left unchanged because a boundary chunk can import it.
#[derive(Serialize)]
pub struct FrozenChunk {
    pub file: String,
    /// The boundary chunk that reaches the chunk.
    pub boundary: String,
    pub reason: Reason,
}

#[derive(Serialize)]
pub struct PassConfig {
    pub enabled: Vec<PassId>,
    pub disabled: Vec<PassId>,
}

/// Emitted ESM bytes of all transformed-language application modules, with
/// equal emitter settings for both sides and no source maps.
/// What `size.before` and `size.after` count.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum SizeMetric {
    /// Stage 1: ESM bytes the compiler emits for the analyzed modules.
    EmittedEsmBytes,
    /// Stage 2: bytes of the edited bundler chunk text, without its trailing
    /// source map comment.
    ChunkTextBytes,
}

#[derive(Serialize)]
pub struct Size {
    pub metric: SizeMetric,
    pub before: usize,
    pub after: usize,
    pub saved: isize,
    /// Stage 2: code bytes of original chunks published next to their renamed
    /// optimized copies because encoded references or non-import mentions in
    /// boundary chunks still name them. Chunks that a boundary chunk imports
    /// are never renamed, so they add nothing here. They are not part of
    /// `after`, which describes the optimized chunk graph. Always 0 in
    /// stage 1.
    pub retained: usize,
    /// Stage 2 with `--minify`: code bytes that SWC minification of edited
    /// chunks removed. They are included in `after` and `saved`; omitted
    /// when nothing was minified.
    #[serde(skip_serializing_if = "is_zero")]
    pub minified: usize,
}

fn is_zero(value: &usize) -> bool {
    *value == 0
}

#[derive(Serialize, Default)]
pub struct PassSummary {
    /// Accepted bundles whose primary transformation is this pass.
    pub applied: usize,
    /// Accepted bundles that include this pass as cleanup.
    pub cleanup_in: usize,
    pub skipped: usize,
    pub skip_reasons: BTreeMap<Reason, usize>,
}

/// One accepted bundle. Byte savings belong to the whole bundle; they are
/// never split among the passes it combines.
#[derive(Serialize)]
pub struct Transformation {
    pub bundle: usize,
    /// Stable semantic identity of the bundle.
    pub key: String,
    pub pass: PassId,
    pub uses: Vec<PassId>,
    pub phase: Phase,
    pub round: usize,
    pub file: String,
    pub line: usize,
    pub column: usize,
    pub files: Vec<String>,
    pub bytes_before: usize,
    pub bytes_after: usize,
    pub bytes_saved: usize,
}

#[derive(Serialize)]
pub struct Skipped {
    pub pass: PassId,
    pub file: String,
    pub line: usize,
    pub column: usize,
    pub reason: Reason,
}

#[derive(Serialize, Default)]
pub struct Measurements {
    pub elapsed_ms: u128,
    pub peak_rss_bytes: u64,
}

impl Report {
    pub fn measure(&mut self, started: std::time::Instant) {
        self.measurements = Measurements {
            elapsed_ms: started.elapsed().as_millis(),
            peak_rss_bytes: crate::output::peak_rss(),
        };
    }
}

/// One-based line and UTF-16 column, matching compiler diagnostics.
pub fn line_column(text: &str, offset: usize) -> (usize, usize) {
    let mut offset = offset.min(text.len());
    while !text.is_char_boundary(offset) {
        offset -= 1;
    }
    let before = &text[..offset];
    let line_start = before
        .rfind(['\n', '\r', '\u{2028}', '\u{2029}'])
        .map_or(0, |i| i + before[i..].chars().next().unwrap().len_utf8());
    let line = before
        .char_indices()
        .filter(|&(i, c)| {
            matches!(c, '\n' | '\u{2028}' | '\u{2029}')
                || (c == '\r' && !before[i + 1..].starts_with('\n'))
        })
        .count();
    (line + 1, before[line_start..].encode_utf16().count() + 1)
}

pub fn build(snapshot: &Snapshot, outcome: &Outcome, passes: &PassSet) -> Report {
    let location = |file: &str, offset: usize| -> (usize, usize) {
        snapshot
            .sources
            .get(file)
            .map_or((0, 0), |text| line_column(text, offset))
    };
    let mut summary: BTreeMap<PassId, PassSummary> = PassId::ALL
        .into_iter()
        .map(|p| (p, PassSummary::default()))
        .collect();
    let transformations: Vec<Transformation> = outcome
        .bundles
        .iter()
        .map(|bundle| {
            summary.get_mut(&bundle.pass).unwrap().applied += 1;
            for pass in &bundle.uses {
                summary.get_mut(pass).unwrap().cleanup_in += 1;
            }
            let (line, column) = location(&bundle.location.file, bundle.location.offset);
            Transformation {
                bundle: bundle.id,
                key: bundle.key.clone(),
                pass: bundle.pass,
                uses: bundle.uses.clone(),
                phase: bundle.phase,
                round: bundle.round,
                file: bundle.location.file.clone(),
                line,
                column,
                files: bundle.files.clone(),
                bytes_before: bundle.bytes_before,
                bytes_after: bundle.bytes_after,
                bytes_saved: bundle.bytes_before - bundle.bytes_after,
            }
        })
        .collect();
    let skipped: Vec<Skipped> = outcome
        .skips
        .iter()
        .map(|skip| {
            let entry = summary.get_mut(&skip.pass).unwrap();
            entry.skipped += 1;
            *entry.skip_reasons.entry(skip.reason).or_default() += 1;
            let (line, column) = location(&skip.location.file, skip.location.offset);
            Skipped {
                pass: skip.pass,
                file: skip.location.file.clone(),
                line,
                column,
                reason: skip.reason,
            }
        })
        .collect();
    let mut preservation_reasons = BTreeMap::new();
    for mapping in &outcome.mappings {
        if let Some(reason) = &mapping.preservation {
            let key = serde_json::to_value(reason)
                .ok()
                .and_then(|v| v.as_str().map(str::to_owned))
                .unwrap_or_default();
            *preservation_reasons.entry(key).or_default() += 1;
        }
    }
    let renamed: Vec<_> = outcome
        .mappings
        .iter()
        .filter(|m| m.original != m.renamed)
        .collect();
    let stats = outcome.property_stats.clone().unwrap_or_default();
    Report {
        schema_version: SCHEMA_VERSION,
        tool_version: env!("CARGO_PKG_VERSION"),
        compiler_revision: crate::app::COMPILER_REVISION,
        files: snapshot.sources.len(),
        object_regions: stats.object_regions,
        analysis_scope: "whole_program",
        no_change_reason: outcome.bundles.is_empty().then_some(
            "No profitable transformation was proven safe; unsupported or observable code is \
             preserved.",
        ),
        properties: outcome.mappings.len(),
        renamed_properties: renamed.len(),
        changed_occurrences: renamed.iter().map(|m| m.occurrences).sum(),
        preservation_reasons,
        passes: PassConfig {
            enabled: passes.enabled(),
            disabled: passes.disabled(),
        },
        rounds: outcome.rounds,
        round_limit_reached: outcome.round_limit_reached,
        size: Size {
            metric: if crate::app::bundle_mode() {
                SizeMetric::ChunkTextBytes
            } else {
                SizeMetric::EmittedEsmBytes
            },
            before: outcome.size_before,
            after: outcome.size_after,
            saved: outcome.size_before as isize - outcome.size_after as isize,
            retained: 0,
            minified: 0,
        },
        pass_summary: summary,
        transformations,
        skipped,
        frozen_chunks: crate::app::bundle_frozen()
            .map(|(file, boundary)| FrozenChunk {
                file: file.clone(),
                boundary: boundary.clone(),
                reason: Reason::BoundaryReachable,
            })
            .collect(),
        measurements: Measurements::default(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn line_columns_count_utf16_units() {
        assert_eq!(line_column("ab", 1), (1, 2));
        assert_eq!(line_column("😀x", 4), (1, 3));
        assert_eq!(line_column("a\r\nb", 3), (2, 1));
        assert_eq!(line_column("a\rb\nc", 4), (3, 1));
    }

    #[test]
    fn reason_and_pass_keys_serialize_as_stable_strings() {
        let mut summary = BTreeMap::new();
        summary.insert(PassId::CallInlining, PassSummary::default());
        summary
            .get_mut(&PassId::CallInlining)
            .unwrap()
            .skip_reasons
            .insert(Reason::NoSizeBenefit, 1);
        let value = serde_json::to_value(&summary).unwrap();
        assert_eq!(value["call-inlining"]["skip_reasons"]["no_size_benefit"], 1);
    }
}
