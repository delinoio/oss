//! Export-level usage paths for Rspack diagnostics.
//!
//! The engine reports export-to-export edges (`consumer` uses `provider`).
//! This module only follows those edges from the diagnostic module toward its
//! consumers. The edges are engine observations; the chains, their order and
//! the choice of which chains to show are derived by bundlewhy. A chain is
//! not a call sequence and says nothing about removable code or byte savings.
//!
//! Every note shown for an unavailable state comes from a fixed set: the
//! constants and `reason_note` below. No adapter or engine text is displayed.

use std::collections::{HashMap, VecDeque};

use super::Context;
use crate::{
    collect::{CollectedExportEdge, CollectedExportRef, CollectedExportUsage, ExportUsageReason},
    model::{
        ExportReference, ExportScope, ExportUsage, ExportUsageEdge, ExportUsagePath,
        ExportUsagePathEnd, ExportUsageState, ExportUsageTruncation,
    },
};

/// Most chains kept for one diagnostic.
pub const MAX_PATHS: usize = 5;
/// Most edges in one chain; a longer chain stops here and is marked truncated.
pub const MAX_EDGES_PER_PATH: usize = 24;
/// Most chains extended per diagnostic; bounds work on dense graphs.
pub const MAX_EXPANSIONS: usize = 10_000;

/// Display and work limits for one traversal.
#[derive(Debug, Clone, Copy)]
pub struct Limits {
    pub max_paths: usize,
    pub max_edges_per_path: usize,
    pub max_expansions: usize,
}

impl Limits {
    pub const DEFAULT: Self = Self {
        max_paths: MAX_PATHS,
        max_edges_per_path: MAX_EDGES_PER_PATH,
        max_expansions: MAX_EXPANSIONS,
    };
}

/// Why the evidence is unavailable.
pub const NOTE_NOT_COLLECTED: &str = "The collection has no export usage data. It was collected \
                                      by an adapter version that does not record it, or by an \
                                      engine without this evidence.";
pub const NOTE_NO_EDGE: &str = "Rspack reported no export usage edge that targets this module, so \
                                no export-level chain is available for it.";
/// Used when edges were dropped, so "Rspack reported none" would be untrue.
pub const NOTE_NO_USABLE_EDGE: &str = "No export usage edge that bundlewhy could use targets this \
                                       module. Edges that involve modules bundlewhy could not \
                                       identify (for example concatenated module containers) were \
                                       not used and may account for this.";
pub const NOTE_NO_REASON: &str = "The engine did not provide export usage data for this build.";

/// Fixed text for each adapter reason code.
pub fn reason_note(reason: Option<ExportUsageReason>) -> &'static str {
    match reason {
        None => NOTE_NO_REASON,
        Some(ExportUsageReason::PluginApiMissing) => {
            "This Rspack version does not provide the RsdoctorPlugin API that reports export usage \
             edges."
        }
        Some(ExportUsageReason::PluginAlreadyConfigured) => {
            "The project already configures RsdoctorPlugin, so bundlewhy did not add a second \
             instance."
        }
        Some(ExportUsageReason::PluginsNotArray) => {
            "The project sets plugins to a value that is not a list, so bundlewhy passed it to \
             Rspack unchanged and did not add the RsdoctorPlugin observer."
        }
        Some(ExportUsageReason::PluginConstructionFailed) => {
            "bundlewhy could not create the RsdoctorPlugin observer with this Rspack version."
        }
        Some(ExportUsageReason::PluginApplyFailed) => {
            "The RsdoctorPlugin observer could not be applied to the Rspack compiler."
        }
        Some(ExportUsageReason::HookRegistrationFailed) => {
            "bundlewhy could not use the RsdoctorPlugin module graph hook of this Rspack version."
        }
        Some(ExportUsageReason::BuildFailedWithObserver) => {
            "The diagnostic build failed with the RsdoctorPlugin observer attached, so it was \
             repeated without it."
        }
        Some(ExportUsageReason::GraphNotDelivered) => {
            "Rspack did not deliver the RsdoctorPlugin module graph for this build."
        }
        Some(ExportUsageReason::ModulesPayloadInvalid) => {
            "The RsdoctorPlugin payload has an unexpected shape: its module list is missing."
        }
        Some(ExportUsageReason::EdgesPayloadInvalid) => {
            "The RsdoctorPlugin payload has an unexpected shape: its export usage edge list is \
             missing."
        }
        Some(ExportUsageReason::EdgeLimitExceeded) => {
            "Rspack reported more export usage edges than the storage limit, so none were stored."
        }
        Some(ExportUsageReason::Unknown) => {
            "The adapter reported an export usage reason that this bundlewhy version does not \
             recognize."
        }
    }
}

/// True when the reference can be shown and passes saved-bundle validation:
/// a non-empty module and, when a path is named, only non-empty segments.
fn usable_reference(reference: &CollectedExportRef) -> bool {
    !reference.module.is_empty()
        && reference
            .export
            .as_deref()
            .is_none_or(|path| !path.is_empty() && path.iter().all(|part| !part.is_empty()))
}

/// Engine edges grouped for traversal.
pub struct ExportUsageIndex<'a> {
    usage: &'a CollectedExportUsage,
    /// Edges that provide exactly one export, in stable order.
    by_provider: HashMap<&'a CollectedExportRef, Vec<&'a CollectedExportEdge>>,
    /// Edges that provide any export of one module, in stable order.
    by_provider_module: HashMap<&'a str, Vec<&'a CollectedExportEdge>>,
    /// Edges dropped here because an endpoint cannot be shown.
    unusable_edges: u32,
}

impl<'a> ExportUsageIndex<'a> {
    pub fn new(usage: &'a CollectedExportUsage) -> Self {
        let mut unusable_edges = 0u32;
        let mut edges: Vec<&CollectedExportEdge> = Vec::with_capacity(usage.edges.len());
        for edge in &usage.edges {
            if usable_reference(&edge.consumer) && usable_reference(&edge.provider) {
                edges.push(edge);
            } else {
                unusable_edges = unusable_edges.saturating_add(1);
            }
        }
        edges.sort_by(|left, right| {
            (
                &left.provider,
                &left.consumer,
                &left.dependency_id,
                &left.location,
            )
                .cmp(&(
                    &right.provider,
                    &right.consumer,
                    &right.dependency_id,
                    &right.location,
                ))
        });
        // The adapter stores each edge once; an identical repeat adds no
        // information and would only repeat a chain.
        edges.dedup_by(|left, right| left == right);
        let mut by_provider: HashMap<&CollectedExportRef, Vec<&CollectedExportEdge>> =
            HashMap::new();
        let mut by_provider_module: HashMap<&str, Vec<&CollectedExportEdge>> = HashMap::new();
        for edge in edges {
            by_provider.entry(&edge.provider).or_default().push(edge);
            by_provider_module
                .entry(edge.provider.module.as_str())
                .or_default()
                .push(edge);
        }
        Self {
            usage,
            by_provider,
            by_provider_module,
            unusable_edges,
        }
    }

    /// Edges whose provider is any export of `module`, in stable order.
    fn edges_into_module(&self, module: &str) -> &[&'a CollectedExportEdge] {
        self.by_provider_module
            .get(module)
            .map(Vec::as_slice)
            .unwrap_or_default()
    }

    /// Edges the adapter skipped plus edges dropped while indexing.
    fn skipped_edges(&self) -> u32 {
        self.usage.skipped_edges.saturating_add(self.unusable_edges)
    }
}

fn unavailable(note: &str) -> ExportUsage {
    ExportUsage {
        state: ExportUsageState::Unavailable,
        note: note.to_owned(),
        paths: Vec::new(),
        truncation: ExportUsageTruncation::Unspecified,
    }
}

/// Export usage evidence for the diagnostic on `module_id`.
pub fn build(context: &Context<'_>, module_id: &str) -> ExportUsage {
    let Some(usage) = context.collection.export_usage.as_ref() else {
        return unavailable(NOTE_NOT_COLLECTED);
    };
    if !usage.available {
        return unavailable(reason_note(usage.reason));
    }
    let index = context
        .export_usage_index
        .get_or_init(|| ExportUsageIndex::new(usage));
    let result = traverse(context, index, module_id, Limits::DEFAULT);
    tracing::debug!(
        module = module_id,
        state = ?result.state,
        paths = result.paths.len(),
        truncation = ?result.truncation,
        skipped_edges = index.skipped_edges(),
        "export usage paths derived"
    );
    result
}

/// Records the first limit that cut the traversal short; later limits do not
/// replace it.
fn note_limit(current: &mut ExportUsageTruncation, limit: ExportUsageTruncation) {
    if *current == ExportUsageTruncation::None {
        *current = limit;
    }
}

type EdgePath<'a> = Vec<&'a CollectedExportEdge>;

/// A chain waiting in the breadth-first queue. A terminal chain is complete
/// (it closed a cycle) and is only waiting for its turn in the ordering.
struct Candidate<'a> {
    path: EdgePath<'a>,
    end: Option<ExportUsagePathEnd>,
}

pub fn traverse(
    context: &Context<'_>,
    index: &ExportUsageIndex<'_>,
    module_id: &str,
    limits: Limits,
) -> ExportUsage {
    let starts = index.edges_into_module(module_id);
    let skipped = index.skipped_edges();
    if starts.is_empty() {
        return unavailable(if skipped == 0 {
            NOTE_NO_EDGE
        } else {
            NOTE_NO_USABLE_EDGE
        });
    }
    let mut queue: VecDeque<Candidate<'_>> = starts
        .iter()
        .map(|edge| Candidate {
            path: vec![*edge],
            end: None,
        })
        .collect();
    // Each chain is stored outermost consumer last while it grows.
    let mut finished: Vec<(EdgePath<'_>, ExportUsagePathEnd)> = Vec::new();
    let mut truncation = ExportUsageTruncation::None;
    let mut expansions = 0usize;
    while let Some(candidate) = queue.pop_front() {
        if finished.len() >= limits.max_paths {
            note_limit(&mut truncation, ExportUsageTruncation::PathCount);
            break;
        }
        let Candidate { path, end } = candidate;
        if let Some(end) = end {
            // A cycle chain takes its turn in the same breadth-first order
            // as every other chain; it needs no further work.
            finished.push((path, end));
            continue;
        }
        if expansions >= limits.max_expansions {
            note_limit(&mut truncation, ExportUsageTruncation::ExpansionBudget);
            let rest = std::iter::once(Candidate { path, end: None }).chain(queue.drain(..));
            for candidate in rest {
                if finished.len() >= limits.max_paths {
                    break;
                }
                let end = candidate.end.unwrap_or(ExportUsagePathEnd::Truncated);
                finished.push((candidate.path, end));
            }
            break;
        }
        expansions += 1;
        let Some(outermost) = path.last().copied() else {
            continue;
        };
        let upstream = index
            .by_provider
            .get(&outermost.consumer)
            .map(Vec::as_slice)
            .unwrap_or_default();
        if upstream.is_empty() {
            finished.push((path, ExportUsagePathEnd::Root));
            continue;
        }
        if path.len() >= limits.max_edges_per_path {
            note_limit(&mut truncation, ExportUsageTruncation::Depth);
            finished.push((path, ExportUsagePathEnd::Truncated));
            continue;
        }
        let mut closed_on_itself = false;
        for edge in upstream {
            if path.iter().any(|seen| std::ptr::eq(*seen, *edge)) {
                // The edge is already part of the chain (a self-referencing
                // export); end the chain once instead of repeating the edge.
                closed_on_itself = true;
                continue;
            }
            let revisits = path
                .iter()
                .any(|seen| seen.consumer == edge.consumer || seen.provider == edge.consumer);
            let mut next = path.clone();
            next.push(edge);
            queue.push_back(Candidate {
                path: next,
                end: revisits.then_some(ExportUsagePathEnd::Cycle),
            });
        }
        if closed_on_itself {
            // This chain is the one being processed now, so its turn in the
            // breadth-first order is also now.
            finished.push((path, ExportUsagePathEnd::Cycle));
        }
    }
    let paths = finished
        .into_iter()
        .map(|(mut edges, end)| {
            edges.reverse();
            ExportUsagePath {
                edges: edges
                    .into_iter()
                    .map(|edge| display_edge(context, edge))
                    .collect(),
                end,
            }
        })
        .collect();
    ExportUsage {
        state: ExportUsageState::Available,
        note: if skipped == 0 {
            String::new()
        } else {
            format!(
                "{skipped} engine edge(s) involve modules that bundlewhy could not identify (for \
                 example concatenated module containers) and are not shown."
            )
        },
        paths,
        truncation,
    }
}

fn display_ref(context: &Context<'_>, reference: &CollectedExportRef) -> ExportReference {
    let (export_path, scope) = match reference.export.as_deref() {
        Some(path) if !path.is_empty() => (path.to_vec(), ExportScope::Named),
        _ => (Vec::new(), ExportScope::Module),
    };
    ExportReference {
        module: context.module_display_by_id(&reference.module),
        export_path,
        scope,
    }
}

fn display_edge(context: &Context<'_>, edge: &CollectedExportEdge) -> ExportUsageEdge {
    ExportUsageEdge {
        consumer: display_ref(context, &edge.consumer),
        provider: display_ref(context, &edge.provider),
        dependency_id: edge.dependency_id.clone().unwrap_or_default(),
        location: edge.location.clone().unwrap_or_default(),
    }
}

#[cfg(test)]
mod tests {
    use std::path::PathBuf;

    use super::*;
    use crate::{collect::Collection, sources::SourceStore};

    fn reference(module: &str, export: Option<&[&str]>) -> CollectedExportRef {
        CollectedExportRef {
            module: module.to_owned(),
            export: export.map(|path| path.iter().map(|part| (*part).to_owned()).collect()),
        }
    }

    fn edge(
        consumer: (&str, Option<&[&str]>),
        provider: (&str, Option<&[&str]>),
        dependency_id: &str,
    ) -> CollectedExportEdge {
        CollectedExportEdge {
            consumer: reference(consumer.0, consumer.1),
            provider: reference(provider.0, provider.1),
            dependency_id: Some(dependency_id.to_owned()),
            location: Some(format!("{dependency_id}:1-2")),
        }
    }

    fn collection(edges: Vec<CollectedExportEdge>) -> Collection {
        Collection {
            export_usage: Some(CollectedExportUsage {
                available: true,
                reason: None,
                skipped_edges: 0,
                edges,
            }),
            ..Collection::default()
        }
    }

    fn run(collection: &Collection, module: &str, limits: Limits) -> ExportUsage {
        let context = Context::new(
            collection,
            PathBuf::from("/project"),
            "project".to_owned(),
            Vec::new(),
            SourceStore::new(false),
        );
        let usage = collection.export_usage.as_ref().unwrap();
        traverse(&context, &ExportUsageIndex::new(usage), module, limits)
    }

    fn modules(path: &ExportUsagePath) -> Vec<String> {
        let mut names: Vec<String> = path
            .edges
            .iter()
            .map(|edge| edge.consumer.module.clone())
            .collect();
        names.push(path.edges.last().unwrap().provider.module.clone());
        names
    }

    const A: &[&str] = &["a"];
    const B: &[&str] = &["b"];
    const C: &[&str] = &["c"];

    #[test]
    fn chain_runs_from_the_outermost_consumer_to_the_diagnostic_module() {
        let data = collection(vec![
            edge(("entry", None), ("lib", Some(A)), "1"),
            edge(("lib", Some(A)), ("shared", Some(B)), "2"),
        ]);
        let usage = run(&data, "shared", Limits::DEFAULT);
        assert_eq!(usage.state, ExportUsageState::Available);
        assert_eq!(usage.truncation, ExportUsageTruncation::None);
        assert_eq!(usage.paths.len(), 1);
        let path = &usage.paths[0];
        assert_eq!(path.end, ExportUsagePathEnd::Root);
        assert_eq!(modules(path), ["entry", "lib", "shared"]);
        assert_eq!(path.edges[0].dependency_id, "1");
        assert_eq!(path.edges[1].dependency_id, "2");
        assert_eq!(path.edges[1].location, "2:1-2");
    }

    #[test]
    fn module_scope_and_named_exports_stay_distinct() {
        let data = collection(vec![
            edge(("entry", None), ("ns", Some(A)), "1"),
            edge(("ns", Some(A)), ("leaf", None), "2"),
        ]);
        let usage = run(&data, "leaf", Limits::DEFAULT);
        let edges = &usage.paths[0].edges;
        assert_eq!(edges[0].consumer.scope, ExportScope::Module);
        assert!(edges[0].consumer.export_path.is_empty());
        assert_eq!(edges[0].provider.scope, ExportScope::Named);
        assert_eq!(edges[0].provider.export_path, ["a"]);
        assert_eq!(edges[1].provider.scope, ExportScope::Module);
        assert!(edges[1].provider.export_path.is_empty());
    }

    #[test]
    fn named_export_never_links_to_a_module_scope_consumer_of_the_same_module() {
        // `lib` uses `shared.b` from module scope, but entry only uses
        // `lib.a`: no edge connects them, so the chain stops at `lib`.
        let data = collection(vec![
            edge(("entry", None), ("lib", Some(A)), "1"),
            edge(("lib", None), ("shared", Some(B)), "2"),
        ]);
        let usage = run(&data, "shared", Limits::DEFAULT);
        assert_eq!(usage.paths.len(), 1);
        assert_eq!(modules(&usage.paths[0]), ["lib", "shared"]);
        assert_eq!(usage.paths[0].end, ExportUsagePathEnd::Root);
    }

    #[test]
    fn branches_produce_one_path_per_consumer_in_stable_order() {
        let data = collection(vec![
            edge(("entry", None), ("left", Some(A)), "1"),
            edge(("entry", None), ("right", Some(A)), "2"),
            edge(("left", Some(A)), ("shared", Some(B)), "3"),
            edge(("right", Some(A)), ("shared", Some(B)), "4"),
        ]);
        let usage = run(&data, "shared", Limits::DEFAULT);
        let rendered: Vec<Vec<String>> = usage.paths.iter().map(modules).collect();
        assert_eq!(
            rendered,
            [
                vec!["entry", "left", "shared"],
                vec!["entry", "right", "shared"]
            ]
        );
        assert!(
            usage
                .paths
                .iter()
                .all(|path| path.end == ExportUsagePathEnd::Root)
        );
    }

    #[test]
    fn cycle_ends_with_the_closing_edge_and_a_cycle_marker() {
        let data = collection(vec![
            edge(("a", Some(A)), ("b", Some(B)), "1"),
            edge(("b", Some(B)), ("a", Some(A)), "2"),
        ]);
        let usage = run(&data, "b", Limits::DEFAULT);
        assert_eq!(usage.truncation, ExportUsageTruncation::None);
        assert_eq!(usage.paths.len(), 1);
        let path = &usage.paths[0];
        assert_eq!(path.end, ExportUsagePathEnd::Cycle);
        assert_eq!(modules(path), ["b", "a", "b"]);
    }

    #[test]
    fn self_referencing_export_ends_once_without_repeating_the_edge() {
        let data = collection(vec![edge(("a", Some(A)), ("a", Some(A)), "1")]);
        let usage = run(&data, "a", Limits::DEFAULT);
        assert_eq!(usage.paths.len(), 1);
        assert_eq!(usage.paths[0].edges.len(), 1);
        assert_eq!(usage.paths[0].end, ExportUsagePathEnd::Cycle);
    }

    #[test]
    fn path_count_cap_marks_truncation() {
        let edges = (0..4)
            .map(|index| edge((&format!("c{index}"), None), ("leaf", Some(A)), "1"))
            .collect();
        let limits = Limits {
            max_paths: 2,
            ..Limits::DEFAULT
        };
        let usage = run(&collection(edges), "leaf", limits);
        assert_eq!(usage.paths.len(), 2);
        assert_eq!(usage.truncation, ExportUsageTruncation::PathCount);
        assert_eq!(modules(&usage.paths[0])[0], "c0");
        assert_eq!(modules(&usage.paths[1])[0], "c1");
    }

    #[test]
    fn exactly_the_path_cap_is_not_truncated() {
        let edges = (0..2)
            .map(|index| edge((&format!("c{index}"), None), ("leaf", Some(A)), "1"))
            .collect();
        let limits = Limits {
            max_paths: 2,
            ..Limits::DEFAULT
        };
        let usage = run(&collection(edges), "leaf", limits);
        assert_eq!(usage.paths.len(), 2);
        assert_eq!(usage.truncation, ExportUsageTruncation::None);
    }

    #[test]
    fn depth_cap_stops_a_long_chain_with_a_truncated_end() {
        let names = ["m0", "m1", "m2", "m3", "m4"];
        let edges = names
            .windows(2)
            .map(|pair| edge((pair[0], Some(A)), (pair[1], Some(A)), "1"))
            .collect();
        let limits = Limits {
            max_edges_per_path: 2,
            ..Limits::DEFAULT
        };
        let usage = run(&collection(edges), "m4", limits);
        assert_eq!(usage.truncation, ExportUsageTruncation::Depth);
        assert_eq!(usage.paths.len(), 1);
        assert_eq!(usage.paths[0].end, ExportUsagePathEnd::Truncated);
        assert_eq!(modules(&usage.paths[0]), ["m2", "m3", "m4"]);
    }

    #[test]
    fn chain_exactly_at_the_depth_cap_is_not_truncated() {
        let edges = vec![
            edge(("m0", Some(A)), ("m1", Some(A)), "1"),
            edge(("m1", Some(A)), ("m2", Some(A)), "2"),
        ];
        let limits = Limits {
            max_edges_per_path: 2,
            ..Limits::DEFAULT
        };
        let usage = run(&collection(edges), "m2", limits);
        assert_eq!(usage.truncation, ExportUsageTruncation::None);
        assert_eq!(usage.paths[0].end, ExportUsagePathEnd::Root);
    }

    #[test]
    fn expansion_budget_stops_work_and_marks_remaining_chains_truncated() {
        let names = ["m0", "m1", "m2", "m3", "m4"];
        let edges = names
            .windows(2)
            .map(|pair| edge((pair[0], Some(A)), (pair[1], Some(A)), "1"))
            .collect();
        let limits = Limits {
            max_expansions: 2,
            ..Limits::DEFAULT
        };
        let usage = run(&collection(edges), "m4", limits);
        assert_eq!(usage.truncation, ExportUsageTruncation::ExpansionBudget);
        assert_eq!(usage.paths.len(), 1);
        assert_eq!(usage.paths[0].end, ExportUsagePathEnd::Truncated);
        assert_eq!(modules(&usage.paths[0]), ["m1", "m2", "m3", "m4"]);
    }

    #[test]
    fn the_first_limit_reached_is_the_one_reported() {
        // Depth stops the first chain; the path cap then stops the search.
        let mut edges: Vec<CollectedExportEdge> = (0..3)
            .map(|index| edge((&format!("c{index}"), None), ("leaf", Some(A)), "1"))
            .collect();
        edges.push(edge(("x", Some(A)), ("c0", Some(A)), "2"));
        edges.push(edge(("c0", Some(A)), ("leaf", Some(B)), "3"));
        let limits = Limits {
            max_paths: 1,
            max_edges_per_path: 1,
            max_expansions: 100,
        };
        let usage = run(&collection(edges), "leaf", limits);
        assert_eq!(usage.truncation, ExportUsageTruncation::PathCount);
        assert_eq!(usage.paths.len(), 1);
    }

    #[test]
    fn a_module_without_incoming_edges_is_unavailable() {
        let data = collection(vec![edge(("a", Some(A)), ("b", Some(B)), "1")]);
        let usage = run(&data, "a", Limits::DEFAULT);
        assert_eq!(usage.state, ExportUsageState::Unavailable);
        assert_eq!(usage.note, NOTE_NO_EDGE);
        assert!(usage.paths.is_empty());
    }

    fn build_for(collection: &Collection, module: &str) -> ExportUsage {
        let context = Context::new(
            collection,
            PathBuf::from("/project"),
            "project".to_owned(),
            Vec::new(),
            SourceStore::new(false),
        );
        build(&context, module)
    }

    #[test]
    fn a_collection_without_export_usage_is_unavailable() {
        let usage = build_for(&Collection::default(), "a");
        assert_eq!(usage.state, ExportUsageState::Unavailable);
        assert_eq!(usage.note, NOTE_NOT_COLLECTED);
    }

    const REASONS: [(ExportUsageReason, &str); 12] = [
        (ExportUsageReason::PluginApiMissing, "plugin-api-missing"),
        (
            ExportUsageReason::PluginAlreadyConfigured,
            "plugin-already-configured",
        ),
        (ExportUsageReason::PluginsNotArray, "plugins-not-array"),
        (
            ExportUsageReason::PluginConstructionFailed,
            "plugin-construction-failed",
        ),
        (ExportUsageReason::PluginApplyFailed, "plugin-apply-failed"),
        (
            ExportUsageReason::HookRegistrationFailed,
            "hook-registration-failed",
        ),
        (
            ExportUsageReason::BuildFailedWithObserver,
            "build-failed-with-observer",
        ),
        (ExportUsageReason::GraphNotDelivered, "graph-not-delivered"),
        (
            ExportUsageReason::ModulesPayloadInvalid,
            "modules-payload-invalid",
        ),
        (
            ExportUsageReason::EdgesPayloadInvalid,
            "edges-payload-invalid",
        ),
        (ExportUsageReason::EdgeLimitExceeded, "edge-limit-exceeded"),
        (ExportUsageReason::Unknown, "some-future-code"),
    ];

    fn unavailable_collection(reason: Option<&str>) -> Collection {
        let mut data = collection(Vec::new());
        data.export_usage = Some(CollectedExportUsage {
            available: false,
            reason: reason.map(|code| {
                serde_json::from_value(serde_json::Value::String(code.to_owned())).unwrap()
            }),
            ..CollectedExportUsage::default()
        });
        data
    }

    #[test]
    fn every_reason_code_maps_to_one_fixed_note() {
        let mut seen = std::collections::HashSet::new();
        for (reason, code) in REASONS {
            let usage = build_for(&unavailable_collection(Some(code)), "a");
            assert_eq!(usage.state, ExportUsageState::Unavailable, "{code}");
            assert!(usage.paths.is_empty(), "{code}");
            assert_eq!(usage.note, reason_note(Some(reason)), "{code}");
            // Fixed English text: one line, no quoting, no paths.
            assert!(!usage.note.is_empty(), "{code}");
            assert!(
                !usage.note.contains(['\n', '"', '\'', '`', '/', '\\']),
                "{code}: {}",
                usage.note
            );
            assert!(seen.insert(usage.note), "{code} repeats another note");
        }
        // Without a reason the default note is used.
        let none = build_for(&unavailable_collection(None), "a");
        assert_eq!(none.note, NOTE_NO_REASON);
    }

    #[test]
    fn an_unrecognized_reason_code_never_surfaces_as_text() {
        let usage = build_for(&unavailable_collection(Some("secret /home/me/app.js")), "a");
        assert_eq!(usage.note, reason_note(Some(ExportUsageReason::Unknown)));
        assert!(!usage.note.contains("secret"));
    }

    #[test]
    fn the_reason_table_in_the_contract_lists_every_code_and_note() {
        let path = concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../docs/crates-bundlewhy-foundation.md"
        );
        let doc = std::fs::read_to_string(path).unwrap();
        let rows: Vec<&str> = doc.lines().filter(|line| line.starts_with('|')).collect();
        for (reason, code) in REASONS {
            if reason == ExportUsageReason::Unknown {
                continue;
            }
            let note = reason_note(Some(reason));
            assert!(
                rows.iter()
                    .any(|row| row.contains(&format!("`{code}`")) && row.contains(note)),
                "contract table has no row for {code} with its note"
            );
        }
        for note in [
            NOTE_NOT_COLLECTED,
            NOTE_NO_EDGE,
            NOTE_NO_USABLE_EDGE,
            NOTE_NO_REASON,
            reason_note(Some(ExportUsageReason::Unknown)),
        ] {
            assert!(
                rows.iter().any(|row| row.contains(note)),
                "contract table is missing: {note}"
            );
        }
    }

    #[test]
    fn skipped_edges_are_disclosed_without_hiding_the_paths() {
        let mut data = collection(vec![edge(("a", Some(A)), ("b", Some(B)), "1")]);
        data.export_usage.as_mut().unwrap().skipped_edges = 3;
        let usage = build_for(&data, "b");
        assert_eq!(usage.state, ExportUsageState::Available);
        assert_eq!(usage.paths.len(), 1);
        assert!(usage.note.starts_with("3 engine edge(s)"));
    }

    #[test]
    fn a_module_whose_edges_were_all_skipped_is_not_reported_as_having_none() {
        let mut data = collection(vec![edge(("a", Some(A)), ("b", Some(B)), "1")]);
        data.export_usage.as_mut().unwrap().skipped_edges = 2;
        let usage = build_for(&data, "a");
        assert_eq!(usage.state, ExportUsageState::Unavailable);
        assert_eq!(usage.note, NOTE_NO_USABLE_EDGE);
        assert!(usage.paths.is_empty());
        // With nothing skipped the plain statement stays accurate.
        data.export_usage.as_mut().unwrap().skipped_edges = 0;
        assert_eq!(build_for(&data, "a").note, NOTE_NO_EDGE);
    }

    /// Edge classification: every kind of collected edge and what becomes of
    /// it. Mirrors the "Edge classification" table in the contract.
    #[test]
    fn edge_classification_table() {
        struct Case {
            name: &'static str,
            edges: Vec<CollectedExportEdge>,
            module: &'static str,
            retained_paths: usize,
            unusable: u32,
            end: Option<ExportUsagePathEnd>,
        }
        let mut empty_segment = edge(("a", Some(A)), ("b", Some(B)), "1");
        empty_segment.provider.export = Some(vec![String::new()]);
        let mut empty_path = edge(("a", Some(A)), ("b", Some(B)), "1");
        empty_path.consumer.export = Some(Vec::new());
        let mut empty_module = edge(("a", Some(A)), ("b", Some(B)), "1");
        empty_module.consumer.module.clear();
        let mapped = edge(("a", Some(A)), ("b", Some(B)), "1");
        let cases = [
            Case {
                name: "mapped edge is retained",
                edges: vec![mapped.clone()],
                module: "b",
                retained_paths: 1,
                unusable: 0,
                end: Some(ExportUsagePathEnd::Root),
            },
            Case {
                name: "empty export segment is dropped and counted",
                edges: vec![empty_segment],
                module: "b",
                retained_paths: 0,
                unusable: 1,
                end: None,
            },
            Case {
                name: "empty export path list is dropped and counted",
                edges: vec![empty_path],
                module: "b",
                retained_paths: 0,
                unusable: 1,
                end: None,
            },
            Case {
                name: "empty module id is dropped and counted",
                edges: vec![empty_module],
                module: "b",
                retained_paths: 0,
                unusable: 1,
                end: None,
            },
            Case {
                name: "identical repeat is retained once",
                edges: vec![mapped.clone(), mapped.clone()],
                module: "b",
                retained_paths: 1,
                unusable: 0,
                end: Some(ExportUsagePathEnd::Root),
            },
            Case {
                name: "self edge ends one cycle path",
                edges: vec![edge(("a", Some(A)), ("a", Some(A)), "1")],
                module: "a",
                retained_paths: 1,
                unusable: 0,
                end: Some(ExportUsagePathEnd::Cycle),
            },
            Case {
                name: "two-module cycle ends one cycle path",
                edges: vec![
                    edge(("a", Some(A)), ("b", Some(B)), "1"),
                    edge(("b", Some(B)), ("a", Some(A)), "2"),
                ],
                module: "b",
                retained_paths: 1,
                unusable: 0,
                end: Some(ExportUsagePathEnd::Cycle),
            },
            Case {
                name: "edge with a different dependency is a different edge",
                edges: vec![
                    edge(("a", Some(A)), ("b", Some(B)), "1"),
                    edge(("a", Some(A)), ("b", Some(B)), "2"),
                ],
                module: "b",
                retained_paths: 2,
                unusable: 0,
                end: Some(ExportUsagePathEnd::Root),
            },
        ];
        for case in cases {
            let data = collection(case.edges);
            let usage = data.export_usage.as_ref().unwrap();
            let index = ExportUsageIndex::new(usage);
            assert_eq!(index.unusable_edges, case.unusable, "{}", case.name);
            let result = run(&data, case.module, Limits::DEFAULT);
            assert_eq!(result.paths.len(), case.retained_paths, "{}", case.name);
            match case.end {
                Some(end) => {
                    assert!(
                        result.paths.iter().all(|path| path.end == end),
                        "{}",
                        case.name
                    );
                    assert_eq!(result.state, ExportUsageState::Available, "{}", case.name);
                }
                None => {
                    // Every edge was dropped: not "none reported".
                    assert_eq!(result.state, ExportUsageState::Unavailable, "{}", case.name);
                    assert_eq!(result.note, NOTE_NO_USABLE_EDGE, "{}", case.name);
                }
            }
        }
    }

    #[test]
    fn dropped_edges_are_added_to_the_adapter_skip_count_in_the_disclosure() {
        let mut bad = edge(("x", Some(A)), ("b", Some(B)), "9");
        bad.consumer.module.clear();
        let mut data = collection(vec![edge(("a", Some(A)), ("b", Some(B)), "1"), bad]);
        data.export_usage.as_mut().unwrap().skipped_edges = 2;
        let usage = build_for(&data, "b");
        assert_eq!(usage.paths.len(), 1);
        assert!(usage.note.starts_with("3 engine edge(s)"), "{}", usage.note);
    }

    #[test]
    fn cycle_paths_keep_the_shortest_first_order() {
        // `x` uses `m.b`, and `m.b` uses `x.a`: following `x` back reaches the
        // diagnostic module again after two edges. `y` uses `m.b` directly
        // and is one edge long, so it must come first.
        let edges = vec![
            edge(("x", Some(A)), ("m", Some(B)), "1"),
            edge(("y", Some(A)), ("m", Some(B)), "2"),
            edge(("m", Some(B)), ("x", Some(A)), "3"),
        ];
        let data = collection(edges);
        let usage = run(&data, "m", Limits::DEFAULT);
        let rendered: Vec<(Vec<String>, ExportUsagePathEnd)> = usage
            .paths
            .iter()
            .map(|path| (modules(path), path.end))
            .collect();
        assert_eq!(
            rendered,
            [
                (
                    vec!["y".to_owned(), "m".to_owned()],
                    ExportUsagePathEnd::Root
                ),
                (
                    vec!["m".to_owned(), "x".to_owned(), "m".to_owned()],
                    ExportUsagePathEnd::Cycle
                ),
            ]
        );
        assert!(
            usage
                .paths
                .windows(2)
                .all(|pair| pair[0].edges.len() <= pair[1].edges.len())
        );
        // With room for one chain, the shorter chain wins and the cap is
        // reported.
        let limits = Limits {
            max_paths: 1,
            ..Limits::DEFAULT
        };
        let capped = run(&data, "m", limits);
        assert_eq!(capped.paths.len(), 1);
        assert_eq!(capped.paths[0].end, ExportUsagePathEnd::Root);
        assert_eq!(capped.truncation, ExportUsageTruncation::PathCount);
    }

    #[test]
    fn a_cycle_chain_waiting_in_the_queue_survives_the_expansion_budget() {
        let edges = vec![
            edge(("x", Some(A)), ("m", Some(B)), "1"),
            edge(("m", Some(B)), ("x", Some(A)), "3"),
            edge(("p0", Some(A)), ("y", Some(A)), "4"),
            edge(("y", Some(A)), ("m", Some(B)), "2"),
        ];
        let limits = Limits {
            max_expansions: 2,
            ..Limits::DEFAULT
        };
        let usage = run(&collection(edges), "m", limits);
        assert_eq!(usage.truncation, ExportUsageTruncation::ExpansionBudget);
        let ends: Vec<_> = usage.paths.iter().map(|path| path.end).collect();
        assert!(ends.contains(&ExportUsagePathEnd::Cycle), "{ends:?}");
        assert!(ends.contains(&ExportUsagePathEnd::Truncated), "{ends:?}");
    }

    #[test]
    fn input_order_does_not_change_the_result() {
        let edges = vec![
            edge(("entry", None), ("left", Some(A)), "1"),
            edge(("entry", None), ("right", Some(A)), "2"),
            edge(("left", Some(A)), ("shared", Some(B)), "3"),
            edge(("right", Some(A)), ("shared", Some(B)), "4"),
            edge(("shared", Some(B)), ("left", Some(A)), "5"),
            edge(("other", Some(C)), ("shared", Some(B)), "6"),
        ];
        let forward = run(&collection(edges.clone()), "shared", Limits::DEFAULT);
        let mut reversed = edges;
        reversed.reverse();
        assert_eq!(
            forward,
            run(&collection(reversed), "shared", Limits::DEFAULT)
        );
    }
}
