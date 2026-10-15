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

use std::collections::{HashMap, HashSet, VecDeque};

use super::Context;
use crate::{
    collect::{
        CollectedExportEdge, CollectedExportRef, CollectedExportUsage, ExportUsageReason,
        SkippedProviderScope,
    },
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
/// Most chains waiting to be extended at one time; bounds memory on dense
/// graphs, where one chain can have as many extensions as the graph has edges.
pub const MAX_QUEUED: usize = 10_000;

/// Display and work limits for one traversal.
#[derive(Debug, Clone, Copy)]
pub struct Limits {
    pub max_paths: usize,
    pub max_edges_per_path: usize,
    pub max_expansions: usize,
    pub max_queued: usize,
}

impl Limits {
    pub const DEFAULT: Self = Self {
        max_paths: MAX_PATHS,
        max_edges_per_path: MAX_EDGES_PER_PATH,
        max_expansions: MAX_EXPANSIONS,
        max_queued: MAX_QUEUED,
    };
}

/// Why the evidence is unavailable.
pub const NOTE_NOT_COLLECTED: &str = "The collection has no export usage data. It was collected \
                                      by an adapter version that does not record it, or by an \
                                      engine without this evidence.";
pub const NOTE_NO_EDGE: &str = "Rspack reported no export usage edge that targets this module, so \
                                no export-level chain is available for it.";
/// Used when edges were dropped, so "Rspack reported none" would be untrue.
pub const NOTE_NO_USABLE_EDGE: &str =
    "No export usage edge that bundlewhy could use targets this module. Edges that involve \
     modules bundlewhy could not identify (for example concatenated module containers) or export \
     references bundlewhy could not read were not used and may account for this.";
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
    /// Edges that provide exactly one export, in stable order.
    by_provider: HashMap<&'a CollectedExportRef, Vec<&'a CollectedExportEdge>>,
    /// Edges that provide any export of one module, in stable order.
    by_provider_module: HashMap<&'a str, Vec<&'a CollectedExportEdge>>,
    /// Edges dropped here because an endpoint cannot be shown.
    unusable_edges: u32,
    /// Exports that a dropped edge targeted by name or by module scope: an
    /// edge into exactly that export exists but cannot be shown.
    skipped_exports: HashSet<CollectedExportRef>,
    /// Modules with a dropped edge whose provider export could not be read:
    /// the edge may have targeted any export of the module.
    skipped_unreadable: HashSet<&'a str>,
    /// Every module with a dropped edge, whatever its export.
    skipped_modules: HashSet<&'a str>,
    /// Dropped edges whose provider module is unknown. Such an edge may be
    /// an earlier edge of any chain.
    unattributed_edges: u32,
    /// Edges dropped by the adapter plus edges dropped here.
    skipped_total: u32,
}

impl<'a> ExportUsageIndex<'a> {
    pub fn new(usage: &'a CollectedExportUsage) -> Self {
        let mut unusable_edges = 0u32;
        let mut skipped_exports: HashSet<CollectedExportRef> = HashSet::new();
        let mut skipped_unreadable: HashSet<&str> = HashSet::new();
        let mut skipped_modules: HashSet<&str> = HashSet::new();
        let mut unattributed_edges = usage.unattributed_edges;
        let mut adapter_attributed = u64::from(usage.unattributed_edges);
        for skipped in usage
            .skipped_providers
            .iter()
            .filter(|entry| entry.edges > 0)
        {
            adapter_attributed += u64::from(skipped.edges);
            if skipped.module.is_empty() {
                unattributed_edges = unattributed_edges.saturating_add(skipped.edges);
                continue;
            }
            skipped_modules.insert(skipped.module.as_str());
            let named =
                !skipped.export.is_empty() && skipped.export.iter().all(|part| !part.is_empty());
            match skipped.scope {
                SkippedProviderScope::Named if named => {
                    skipped_exports.insert(CollectedExportRef {
                        module: skipped.module.clone(),
                        export: Some(skipped.export.clone()),
                    });
                }
                SkippedProviderScope::Module => {
                    skipped_exports.insert(CollectedExportRef {
                        module: skipped.module.clone(),
                        export: None,
                    });
                }
                // A named entry without a readable path is as uncertain as
                // an unknown scope.
                SkippedProviderScope::Named | SkippedProviderScope::Unknown => {
                    skipped_unreadable.insert(skipped.module.as_str());
                }
            }
        }
        let mut edges: Vec<&CollectedExportEdge> = Vec::with_capacity(usage.edges.len());
        for edge in &usage.edges {
            if usable_reference(&edge.consumer) && usable_reference(&edge.provider) {
                edges.push(edge);
                continue;
            }
            unusable_edges = unusable_edges.saturating_add(1);
            if edge.provider.module.is_empty() {
                unattributed_edges = unattributed_edges.saturating_add(1);
                continue;
            }
            skipped_modules.insert(edge.provider.module.as_str());
            if usable_reference(&edge.provider) {
                // Only the consumer is unusable: the provider export is known.
                skipped_exports.insert(edge.provider.clone());
            } else {
                skipped_unreadable.insert(edge.provider.module.as_str());
            }
        }
        // The adapter total never counts less than its own attribution, so a
        // chain end that is not a confirmed start always has a disclosure.
        let adapter_total = u64::from(usage.skipped_edges).max(adapter_attributed);
        let skipped_total = u32::try_from(adapter_total)
            .unwrap_or(u32::MAX)
            .saturating_add(unusable_edges);
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
            by_provider,
            by_provider_module,
            unusable_edges,
            skipped_exports,
            skipped_unreadable,
            skipped_modules,
            unattributed_edges,
            skipped_total,
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
        self.skipped_total
    }

    /// True when an edge that could have targeted some export of `module` was
    /// dropped, so "no edge targets this module" would be a claim that the
    /// data cannot support. A dropped edge counts when its provider module is
    /// `module` or when its provider is unknown. An edge into another module,
    /// including a module that was not collected, cannot be an earlier edge
    /// of a chain through `module`.
    fn may_hide_edge_into(&self, module: &str) -> bool {
        self.unattributed_edges > 0 || self.skipped_modules.contains(module)
    }

    /// True when a dropped edge could have been an earlier edge of a chain
    /// whose first export is `reference`: an edge that targeted exactly that
    /// export, an edge that targeted an unreadable export of its module, or
    /// an edge whose provider is unknown. An edge into a different export of
    /// the same module is not one: the traversal links edges by exact module
    /// and export, so a chain never continues through another export.
    fn may_hide_edge_into_export(&self, reference: &CollectedExportRef) -> bool {
        self.unattributed_edges > 0
            || self.skipped_unreadable.contains(reference.module.as_str())
            || self.skipped_exports.contains(reference)
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
    let (result, stats) = traverse_with_stats(context, index, module_id, Limits::DEFAULT);
    tracing::debug!(
        module = %context.module_log_label(module_id),
        state = ?result.state,
        paths = result.paths.len(),
        incomplete_paths = result
            .paths
            .iter()
            .filter(|path| path.end == ExportUsagePathEnd::Incomplete)
            .count(),
        truncation = ?result.truncation,
        expansions = stats.expansions,
        peak_queued = stats.peak_queued,
        skipped_edges = index.skipped_edges(),
        dropped_by_index = index.unusable_edges,
        unattributed_edges = index.unattributed_edges,
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

/// What one traversal did; reported in the debug log and checked by tests.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct TraversalStats {
    /// Chains that were extended.
    pub expansions: usize,
    /// Most chains that waited in the queue at one time.
    pub peak_queued: usize,
    /// True when the run was cancelled and the traversal stopped early. The
    /// result is partial and the caller discards it with the rest of the
    /// cancelled analysis.
    pub cancelled: bool,
}

pub fn traverse(
    context: &Context<'_>,
    index: &ExportUsageIndex<'_>,
    module_id: &str,
    limits: Limits,
) -> ExportUsage {
    traverse_with_stats(context, index, module_id, limits).0
}

pub fn traverse_with_stats(
    context: &Context<'_>,
    index: &ExportUsageIndex<'_>,
    module_id: &str,
    limits: Limits,
) -> (ExportUsage, TraversalStats) {
    let starts = index.edges_into_module(module_id);
    let skipped = index.skipped_edges();
    let mut stats = TraversalStats::default();
    if starts.is_empty() {
        let note = if index.may_hide_edge_into(module_id) {
            NOTE_NO_USABLE_EDGE
        } else {
            NOTE_NO_EDGE
        };
        return (unavailable(note), stats);
    }
    // The queue never holds more than `max_queued` chains (at least one, so
    // that a diagnostic with edges always shows a chain). A chain that does
    // not fit is not queued and the traversal is marked as cut short.
    let max_queued = limits.max_queued.max(1);
    let mut truncation = ExportUsageTruncation::None;
    if starts.len() > max_queued {
        note_limit(&mut truncation, ExportUsageTruncation::ExpansionBudget);
    }
    let mut queue: VecDeque<Candidate<'_>> = starts
        .iter()
        .take(max_queued)
        .map(|edge| Candidate {
            path: vec![*edge],
            // A self-referencing export closes its cycle with its first edge:
            // the chain is terminal as soon as it exists, like any chain
            // whose extension revisits an export.
            end: (edge.consumer == edge.provider).then_some(ExportUsagePathEnd::Cycle),
        })
        .collect();
    stats.peak_queued = queue.len();
    // Each chain is stored outermost consumer last while it grows.
    let mut finished: Vec<(EdgePath<'_>, ExportUsagePathEnd)> = Vec::new();
    while let Some(candidate) = queue.pop_front() {
        // Polled once per chain taken from the queue; the work between two
        // polls is bounded by the queue bound and the chain depth limit.
        if context.is_cancelled() {
            stats.cancelled = true;
            break;
        }
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
        if stats.expansions >= limits.max_expansions {
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
        stats.expansions += 1;
        let Some(outermost) = path.last().copied() else {
            continue;
        };
        let upstream = index
            .by_provider
            .get(&outermost.consumer)
            .map(Vec::as_slice)
            .unwrap_or_default();
        if upstream.is_empty() {
            // No usable edge consumes the first export. That is a confirmed
            // start only when no dropped edge could have been one.
            let end = if index.may_hide_edge_into_export(&outermost.consumer) {
                ExportUsagePathEnd::Incomplete
            } else {
                ExportUsagePathEnd::Root
            };
            finished.push((path, end));
            continue;
        }
        if path.len() >= limits.max_edges_per_path {
            note_limit(&mut truncation, ExportUsageTruncation::Depth);
            finished.push((path, ExportUsagePathEnd::Truncated));
            continue;
        }
        for edge in upstream {
            if path.iter().any(|seen| std::ptr::eq(*seen, *edge)) {
                continue;
            }
            if queue.len() >= max_queued {
                // Extending the chain by the remaining edges would hold more
                // chains than the memory bound allows.
                note_limit(&mut truncation, ExportUsageTruncation::ExpansionBudget);
                break;
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
        stats.peak_queued = stats.peak_queued.max(queue.len());
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
    let usage = ExportUsage {
        state: ExportUsageState::Available,
        note: if skipped == 0 {
            String::new()
        } else {
            format!(
                "{skipped} engine edge(s) could not be used and are not shown. Such edges involve \
                 modules that bundlewhy could not identify (for example concatenated module \
                 containers) or export references that bundlewhy could not read."
            )
        },
        paths,
        truncation,
    };
    (usage, stats)
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
    use std::{
        path::PathBuf,
        sync::{
            Arc, Mutex,
            atomic::{AtomicUsize, Ordering},
        },
    };

    use super::*;
    use crate::{
        collect::{CollectedModule, CollectedSkippedProvider, Collection, SkippedProviderScope},
        sources::SourceStore,
    };

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
                edges,
                ..CollectedExportUsage::default()
            }),
            ..Collection::default()
        }
    }

    fn run_with_stats(
        collection: &Collection,
        module: &str,
        limits: Limits,
    ) -> (ExportUsage, TraversalStats) {
        let context = Context::new(
            collection,
            PathBuf::from("/project"),
            "project".to_owned(),
            Vec::new(),
            SourceStore::new(false),
        );
        let usage = collection.export_usage.as_ref().unwrap();
        traverse_with_stats(&context, &ExportUsageIndex::new(usage), module, limits)
    }

    fn run(collection: &Collection, module: &str, limits: Limits) -> ExportUsage {
        run_with_stats(collection, module, limits).0
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
            max_queued: MAX_QUEUED,
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

    /// Records what the adapter reported about dropped edges.
    fn skip_edges(data: &mut Collection, total: u32, providers: &[(&str, u32)], unattributed: u32) {
        let usage = data.export_usage.as_mut().unwrap();
        usage.skipped_edges = total;
        usage.skipped_providers = providers
            .iter()
            .map(|(module, edges)| CollectedSkippedProvider {
                module: (*module).to_owned(),
                edges: *edges,
                ..CollectedSkippedProvider::default()
            })
            .collect();
        usage.unattributed_edges = unattributed;
    }

    /// Dropped edge as the adapter reports it: provider module, what is known
    /// about the provider export, and the number of edges.
    fn skipped(
        module: &str,
        scope: SkippedProviderScope,
        export: &[&str],
        edges: u32,
    ) -> CollectedSkippedProvider {
        CollectedSkippedProvider {
            module: module.to_owned(),
            scope,
            export: export.iter().map(|part| (*part).to_owned()).collect(),
            edges,
        }
    }

    /// Chain end per dropped edge and first export. The chain below always
    /// ends at the first export of `x`, which no usable edge consumes; only a
    /// dropped edge that could have been an edge into exactly that export
    /// makes the start unconfirmed. Mirrors the "Dropped edge and chain end"
    /// table in the contract.
    #[test]
    fn dropped_edge_and_chain_end_table() {
        use ExportUsagePathEnd::{Incomplete, Root};
        use SkippedProviderScope::{Module, Named, Unknown};
        enum Drop {
            /// Reported by the adapter in `skippedProviders`.
            Adapter(CollectedSkippedProvider),
            /// Reported by the adapter in `unattributedEdges`.
            Unattributed,
            /// A collected edge that the index drops.
            Index(CollectedExportEdge),
        }
        let first_named: Option<&[&str]> = Some(A);
        let first_module: Option<&[&str]> = None;
        let unusable_consumer = |provider: (&'static str, Option<&'static [&'static str]>)| {
            edge(("", None), provider, "9")
        };
        let cases: Vec<(&str, Drop, ExportUsagePathEnd, ExportUsagePathEnd)> = vec![
            // (name, dropped edge, end when the first export is x.a,
            //  end when the first export is x at module scope)
            (
                "named export of the first export",
                Drop::Adapter(skipped("x", Named, &["a"], 1)),
                Incomplete,
                Root,
            ),
            (
                "named export that differs in the last segment",
                Drop::Adapter(skipped("x", Named, &["b"], 1)),
                Root,
                Root,
            ),
            (
                "named export that extends the first export's path",
                Drop::Adapter(skipped("x", Named, &["a", "sub"], 1)),
                Root,
                Root,
            ),
            (
                "module scope of the same module",
                Drop::Adapter(skipped("x", Module, &[], 1)),
                Root,
                Incomplete,
            ),
            (
                "unreadable export of the same module",
                Drop::Adapter(skipped("x", Unknown, &[], 1)),
                Incomplete,
                Incomplete,
            ),
            (
                "named scope without a path counts as unreadable",
                Drop::Adapter(skipped("x", Named, &[], 1)),
                Incomplete,
                Incomplete,
            ),
            (
                "named scope with an empty segment counts as unreadable",
                Drop::Adapter(skipped("x", Named, &["a", ""], 1)),
                Incomplete,
                Incomplete,
            ),
            (
                "another module",
                Drop::Adapter(skipped("elsewhere", Unknown, &[], 1)),
                Root,
                Root,
            ),
            (
                "entry without a module is unattributed",
                Drop::Adapter(skipped("", Named, &["a"], 1)),
                Incomplete,
                Incomplete,
            ),
            (
                "edge with an unknown provider module",
                Drop::Unattributed,
                Incomplete,
                Incomplete,
            ),
            (
                "index drop with an unusable consumer into the first export",
                Drop::Index(unusable_consumer(("x", Some(A)))),
                Incomplete,
                Root,
            ),
            (
                "index drop with an unusable consumer into another export",
                Drop::Index(unusable_consumer(("x", Some(B)))),
                Root,
                Root,
            ),
            (
                "index drop with an unusable consumer into module scope",
                Drop::Index(unusable_consumer(("x", None))),
                Root,
                Incomplete,
            ),
            (
                "index drop with an unreadable provider export",
                Drop::Index(edge(("entry", None), ("x", Some(&[])), "9")),
                Incomplete,
                Incomplete,
            ),
            (
                "index drop with an empty provider module",
                Drop::Index(edge(("entry", None), ("", None), "9")),
                Incomplete,
                Incomplete,
            ),
            (
                "index drop into another module",
                Drop::Index(unusable_consumer(("elsewhere", Some(A)))),
                Root,
                Root,
            ),
        ];
        for (name, dropped, named_end, module_end) in cases {
            for (first, expected) in [(first_named, named_end), (first_module, module_end)] {
                let mut edges = vec![edge(("x", first), ("m", Some(B)), "1")];
                let mut providers = Vec::new();
                let mut total = 0;
                let mut unattributed = 0;
                match &dropped {
                    Drop::Adapter(entry) => {
                        total = entry.edges;
                        providers.push(entry.clone());
                    }
                    Drop::Unattributed => {
                        total = 1;
                        unattributed = 1;
                    }
                    Drop::Index(dropped_edge) => edges.push(dropped_edge.clone()),
                }
                let mut data = collection(edges);
                let usage = data.export_usage.as_mut().unwrap();
                usage.skipped_edges = total;
                usage.skipped_providers = providers;
                usage.unattributed_edges = unattributed;
                let result = build_for(&data, "m");
                assert_eq!(
                    result.paths.iter().map(|path| path.end).collect::<Vec<_>>(),
                    [expected],
                    "{name} (first export {first:?})"
                );
                if expected == Incomplete {
                    assert!(
                        result.note.contains("engine edge(s)"),
                        "{name}: {}",
                        result.note
                    );
                }
            }
        }
    }

    #[test]
    fn skipped_provider_scope_reads_known_values_and_falls_back_to_unknown() {
        let read = |json: &str| -> CollectedSkippedProvider { serde_json::from_str(json).unwrap() };
        assert_eq!(
            read(r#"{"module":"x","scope":"named","export":["a","b"],"edges":2}"#),
            skipped("x", SkippedProviderScope::Named, &["a", "b"], 2)
        );
        assert_eq!(
            read(r#"{"module":"x","scope":"module","export":[],"edges":1}"#),
            skipped("x", SkippedProviderScope::Module, &[], 1)
        );
        // An entry from an adapter without export tracking, an unreadable
        // export and a scope from a newer adapter all mean "any export".
        for json in [
            r#"{"module":"x","edges":3}"#,
            r#"{"module":"x","scope":"unknown","export":[],"edges":3}"#,
            r#"{"module":"x","scope":"from-the-future","export":["a"],"edges":3}"#,
        ] {
            let entry = read(json);
            assert_eq!(entry.scope, SkippedProviderScope::Unknown, "{json}");
            assert_eq!(entry.edges, 3, "{json}");
        }
    }

    #[test]
    fn a_dropped_edge_into_another_export_does_not_hide_the_absence_of_edges() {
        // Starting edges are module-level: any dropped edge into the
        // diagnostic module could have been one, whatever its export.
        let mut data = collection(vec![edge(("a", Some(A)), ("b", Some(B)), "1")]);
        data.export_usage.as_mut().unwrap().skipped_edges = 1;
        data.export_usage.as_mut().unwrap().skipped_providers =
            vec![skipped("a", SkippedProviderScope::Named, &["other"], 1)];
        assert_eq!(build_for(&data, "a").note, NOTE_NO_USABLE_EDGE);
    }

    #[test]
    fn a_module_whose_edges_were_all_skipped_is_not_reported_as_having_none() {
        let mut data = collection(vec![edge(("a", Some(A)), ("b", Some(B)), "1")]);
        skip_edges(&mut data, 2, &[("a", 2)], 0);
        let usage = build_for(&data, "a");
        assert_eq!(usage.state, ExportUsageState::Unavailable);
        assert_eq!(usage.note, NOTE_NO_USABLE_EDGE);
        assert!(usage.paths.is_empty());
        // An edge that was skipped into another module cannot target `a`, so
        // the plain statement stays accurate for `a`.
        skip_edges(&mut data, 2, &[("elsewhere", 2)], 0);
        assert_eq!(build_for(&data, "a").note, NOTE_NO_EDGE);
        // An edge with an unknown provider might target any module.
        skip_edges(&mut data, 1, &[], 1);
        assert_eq!(build_for(&data, "a").note, NOTE_NO_USABLE_EDGE);
        // With nothing skipped the plain statement stays accurate.
        skip_edges(&mut data, 0, &[], 0);
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
    fn the_disclosure_names_every_kind_of_dropped_edge() {
        // Both endpoint modules are known; only the export reference is
        // unreadable. The note must not blame the module mapping alone.
        let mut unreadable = edge(("x", Some(A)), ("b", Some(B)), "9");
        unreadable.consumer.export = Some(Vec::new());
        let data = collection(vec![edge(("a", Some(A)), ("b", Some(B)), "1"), unreadable]);
        let usage = build_for(&data, "b");
        assert!(usage.note.starts_with("1 engine edge(s)"), "{}", usage.note);
        for cause in [
            "modules that bundlewhy could not identify",
            "export references that bundlewhy could not read",
        ] {
            assert!(usage.note.contains(cause), "{}", usage.note);
        }
        for cause in [
            "modules bundlewhy could not identify",
            "export references bundlewhy could not read",
        ] {
            assert!(NOTE_NO_USABLE_EDGE.contains(cause), "{NOTE_NO_USABLE_EDGE}");
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

    /// Terminal classification of a chain: every way a chain can stop and the
    /// label that it gets. Mirrors the "Chain end" table in the contract.
    #[test]
    fn chain_end_classification_table() {
        use ExportUsagePathEnd::{Cycle, Incomplete, Root, Truncated};
        struct Case {
            name: &'static str,
            edges: Vec<CollectedExportEdge>,
            module: &'static str,
            /// Adapter total, per-provider counts and unattributed count.
            skips: (u32, Vec<(&'static str, u32)>, u32),
            limits: Limits,
            ends: Vec<ExportUsagePathEnd>,
            truncation: ExportUsageTruncation,
        }
        let lone = || vec![edge(("entry", None), ("m", Some(A)), "1")];
        let through_mid = || {
            vec![
                edge(("entry", None), ("mid", Some(A)), "1"),
                edge(("mid", Some(A)), ("m", Some(B)), "2"),
            ]
        };
        let only_mid = || vec![edge(("mid", Some(A)), ("m", Some(B)), "2")];
        let fan_in = || {
            (0..4)
                .map(|index| edge((&format!("c{index}"), None), ("leaf", Some(A)), "1"))
                .collect::<Vec<_>>()
        };
        let line = || {
            ["m0", "m1", "m2", "m3", "m4"]
                .windows(2)
                .map(|pair| edge((pair[0], Some(A)), (pair[1], Some(A)), "1"))
                .collect::<Vec<_>>()
        };
        let mut dropped_into_entry = edge(("gone", Some(A)), ("entry", None), "9");
        dropped_into_entry.consumer.module.clear();
        let mut dropped_unknown_provider = edge(("gone", Some(A)), ("entry", None), "9");
        dropped_unknown_provider.provider.module.clear();
        let mut dropped_into_elsewhere = edge(("gone", Some(A)), ("elsewhere", None), "9");
        dropped_into_elsewhere.consumer.module.clear();
        let with = |mut edges: Vec<CollectedExportEdge>, extra: CollectedExportEdge| {
            edges.push(extra);
            edges
        };
        let none = (0, Vec::new(), 0);
        let cases = vec![
            Case {
                name: "no edge was dropped: a confirmed start",
                edges: lone(),
                module: "m",
                skips: none.clone(),
                limits: Limits::DEFAULT,
                ends: vec![Root],
                truncation: ExportUsageTruncation::None,
            },
            Case {
                name: "an edge was dropped into another module: still a confirmed start",
                edges: lone(),
                module: "m",
                skips: (1, vec![("elsewhere", 1)], 0),
                limits: Limits::DEFAULT,
                ends: vec![Root],
                truncation: ExportUsageTruncation::None,
            },
            Case {
                name: "an edge was dropped into the first export's module: not confirmed",
                edges: lone(),
                module: "m",
                skips: (1, vec![("entry", 1)], 0),
                limits: Limits::DEFAULT,
                ends: vec![Incomplete],
                truncation: ExportUsageTruncation::None,
            },
            Case {
                name: "a provider entry without edges drops nothing",
                edges: lone(),
                module: "m",
                skips: (0, vec![("entry", 0)], 0),
                limits: Limits::DEFAULT,
                ends: vec![Root],
                truncation: ExportUsageTruncation::None,
            },
            Case {
                name: "a dropped edge into a module inside the chain does not change the end",
                edges: through_mid(),
                module: "m",
                skips: (1, vec![("mid", 1)], 0),
                limits: Limits::DEFAULT,
                ends: vec![Root],
                truncation: ExportUsageTruncation::None,
            },
            Case {
                name: "the only earlier edge of an inner module was dropped: not confirmed",
                edges: only_mid(),
                module: "m",
                skips: (1, vec![("mid", 1)], 0),
                limits: Limits::DEFAULT,
                ends: vec![Incomplete],
                truncation: ExportUsageTruncation::None,
            },
            Case {
                name: "an edge with an unknown provider may be any earlier edge",
                edges: fan_in(),
                module: "leaf",
                skips: (1, Vec::new(), 1),
                limits: Limits::DEFAULT,
                ends: vec![Incomplete; 4],
                truncation: ExportUsageTruncation::None,
            },
            Case {
                name: "a cycle end is not changed by a dropped edge",
                edges: vec![
                    edge(("a", Some(A)), ("b", Some(B)), "1"),
                    edge(("b", Some(B)), ("a", Some(A)), "2"),
                ],
                module: "b",
                skips: (1, vec![("a", 1)], 0),
                limits: Limits::DEFAULT,
                ends: vec![Cycle],
                truncation: ExportUsageTruncation::None,
            },
            Case {
                name: "path cap: kept chains are classified, the cap is reported",
                edges: fan_in(),
                module: "leaf",
                skips: (1, vec![("c0", 1)], 0),
                limits: Limits {
                    max_paths: 2,
                    ..Limits::DEFAULT
                },
                ends: vec![Incomplete, Root],
                truncation: ExportUsageTruncation::PathCount,
            },
            Case {
                name: "depth cap: the chain never reached the start, a dropped edge there is moot",
                edges: line(),
                module: "m4",
                skips: (1, vec![("m0", 1)], 0),
                limits: Limits {
                    max_edges_per_path: 2,
                    ..Limits::DEFAULT
                },
                ends: vec![Truncated],
                truncation: ExportUsageTruncation::Depth,
            },
            Case {
                name: "expansion budget: remaining chains stay truncated",
                edges: line(),
                module: "m4",
                skips: (1, vec![("m0", 1)], 0),
                limits: Limits {
                    max_expansions: 2,
                    ..Limits::DEFAULT
                },
                ends: vec![Truncated],
                truncation: ExportUsageTruncation::ExpansionBudget,
            },
            Case {
                name: "an edge dropped by the index (empty consumer module) counts for its \
                       provider",
                edges: with(lone(), dropped_into_entry),
                module: "m",
                skips: none.clone(),
                limits: Limits::DEFAULT,
                ends: vec![Incomplete],
                truncation: ExportUsageTruncation::None,
            },
            Case {
                name: "an edge dropped by the index into another module does not",
                edges: with(lone(), dropped_into_elsewhere),
                module: "m",
                skips: none.clone(),
                limits: Limits::DEFAULT,
                ends: vec![Root],
                truncation: ExportUsageTruncation::None,
            },
            Case {
                name: "an edge dropped by the index (empty provider module) is unattributed",
                edges: with(lone(), dropped_unknown_provider),
                module: "m",
                skips: none,
                limits: Limits::DEFAULT,
                ends: vec![Incomplete],
                truncation: ExportUsageTruncation::None,
            },
        ];
        for case in cases {
            let mut data = collection(case.edges);
            let (total, providers, unattributed) = &case.skips;
            skip_edges(&mut data, *total, providers, *unattributed);
            let context = Context::new(
                &data,
                PathBuf::from("/project"),
                "project".to_owned(),
                Vec::new(),
                SourceStore::new(false),
            );
            let usage = data.export_usage.as_ref().unwrap();
            let result = traverse(
                &context,
                &ExportUsageIndex::new(usage),
                case.module,
                case.limits,
            );
            let ends: Vec<_> = result.paths.iter().map(|path| path.end).collect();
            assert_eq!(ends, case.ends, "{}", case.name);
            assert_eq!(result.truncation, case.truncation, "{}", case.name);
            // A chain whose start is not confirmed always comes with the
            // disclosure of the dropped edges.
            if ends.contains(&Incomplete) {
                assert!(
                    result.note.contains("engine edge(s)"),
                    "{}: {}",
                    case.name,
                    result.note
                );
            }
        }
    }

    #[test]
    fn the_disclosure_never_counts_fewer_edges_than_the_adapter_attributed() {
        let mut data = collection(vec![edge(("entry", None), ("m", Some(A)), "1")]);
        // A hand-written collection that attributes more edges than it counts.
        skip_edges(&mut data, 0, &[("entry", 2)], 1);
        let usage = build_for(&data, "m");
        assert_eq!(usage.paths[0].end, ExportUsagePathEnd::Incomplete);
        assert!(usage.note.starts_with("3 engine edge(s)"), "{}", usage.note);
    }

    #[test]
    fn a_dropped_upstream_edge_into_the_first_consumer_is_not_called_a_start() {
        // `entry` uses `m.a`; Rspack also reported an edge into `entry.x`
        // whose consumer is not a collected module, so the adapter dropped it.
        let mut data = collection(vec![edge(("entry", Some(A)), ("m", Some(A)), "1")]);
        skip_edges(&mut data, 1, &[("entry", 1)], 0);
        let usage = build_for(&data, "m");
        assert_eq!(usage.state, ExportUsageState::Available);
        assert_eq!(usage.paths.len(), 1);
        assert_eq!(usage.paths[0].end, ExportUsagePathEnd::Incomplete);
        assert_eq!(usage.truncation, ExportUsageTruncation::None);
        assert!(usage.note.starts_with("1 engine edge(s)"), "{}", usage.note);
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

    /// `count` distinct edges from `consumer` into `provider`.
    fn fan(
        count: usize,
        consumer: impl Fn(usize) -> (String, Option<&'static [&'static str]>),
        provider: (&str, Option<&[&str]>),
    ) -> Vec<CollectedExportEdge> {
        (0..count)
            .map(|index| {
                let (module, export) = consumer(index);
                edge((&module, export), provider, &format!("d{index}"))
            })
            .collect()
    }

    #[test]
    fn a_dense_permitted_graph_never_queues_more_chains_than_the_bound() {
        // Every edge into the diagnostic export is consumed by one shared
        // export, and that export is consumed by as many edges again: both
        // lists fit the edge collection limit, but extending one chain would
        // otherwise queue every upstream edge at once.
        let count = 50_000;
        let mut edges = fan(count, |_| ("shared".to_owned(), Some(A)), ("leaf", Some(B)));
        edges.extend(fan(
            count,
            |index| (format!("user{index}"), None),
            ("shared", Some(A)),
        ));
        let (usage, stats) = run_with_stats(&collection(edges), "leaf", Limits::DEFAULT);
        assert!(stats.peak_queued <= MAX_QUEUED, "{stats:?}");
        assert!(stats.expansions <= MAX_EXPANSIONS, "{stats:?}");
        assert_eq!(usage.state, ExportUsageState::Available);
        assert_eq!(usage.truncation, ExportUsageTruncation::ExpansionBudget);
        assert_eq!(usage.paths.len(), MAX_PATHS);
        assert!(
            usage
                .paths
                .iter()
                .all(|path| path.end == ExportUsagePathEnd::Truncated)
        );
    }

    /// Queue bound: every combination of queue state and event. Mirrors the
    /// "Queue bound" table in the contract.
    #[test]
    fn queue_bound_table() {
        struct Case {
            name: &'static str,
            edges: Vec<CollectedExportEdge>,
            max_queued: usize,
            truncation: ExportUsageTruncation,
            consumers: Vec<&'static str>,
            peak: usize,
        }
        let roots = |count| {
            fan(
                count,
                |index| (format!("c{index}"), None),
                ("leaf", Some(A)),
            )
        };
        let cases = [
            Case {
                name: "starts fit the bound",
                edges: roots(3),
                max_queued: 3,
                truncation: ExportUsageTruncation::None,
                consumers: vec!["c0", "c1", "c2"],
                peak: 3,
            },
            Case {
                name: "starts exceed the bound: the first starts in order are kept",
                edges: roots(4),
                max_queued: 2,
                truncation: ExportUsageTruncation::ExpansionBudget,
                consumers: vec!["c0", "c1"],
                peak: 2,
            },
            Case {
                name: "a bound of zero still queues one chain",
                edges: roots(3),
                max_queued: 0,
                truncation: ExportUsageTruncation::ExpansionBudget,
                consumers: vec!["c0"],
                peak: 1,
            },
            Case {
                name: "extensions fit the bound",
                edges: {
                    let mut edges = fan(1, |_| ("mid".to_owned(), Some(B)), ("leaf", Some(A)));
                    edges.extend(fan(
                        3,
                        |index| (format!("u{index}"), None),
                        ("mid", Some(B)),
                    ));
                    edges
                },
                max_queued: 3,
                truncation: ExportUsageTruncation::None,
                consumers: vec!["u0", "u1", "u2"],
                peak: 3,
            },
            Case {
                name: "extensions reach the bound: the rest are not queued",
                edges: {
                    let mut edges = fan(1, |_| ("mid".to_owned(), Some(B)), ("leaf", Some(A)));
                    edges.extend(fan(
                        5,
                        |index| (format!("u{index}"), None),
                        ("mid", Some(B)),
                    ));
                    edges
                },
                max_queued: 3,
                truncation: ExportUsageTruncation::ExpansionBudget,
                consumers: vec!["u0", "u1", "u2"],
                peak: 3,
            },
        ];
        for case in cases {
            let limits = Limits {
                max_queued: case.max_queued,
                ..Limits::DEFAULT
            };
            let (usage, stats) = run_with_stats(&collection(case.edges), "leaf", limits);
            assert_eq!(usage.truncation, case.truncation, "{}", case.name);
            let consumers: Vec<String> = usage
                .paths
                .iter()
                .map(|path| path.edges[0].consumer.module.clone())
                .collect();
            assert_eq!(consumers, case.consumers, "{}", case.name);
            assert!(
                usage
                    .paths
                    .iter()
                    .all(|path| path.end == ExportUsagePathEnd::Root),
                "{}",
                case.name
            );
            assert_eq!(stats.peak_queued, case.peak, "{}", case.name);
            assert!(stats.peak_queued <= case.max_queued.max(1), "{}", case.name);
        }
    }

    #[test]
    fn a_chain_that_closes_on_itself_ends_once_even_when_the_queue_is_full() {
        let mut edges = vec![edge(("a", Some(A)), ("a", Some(A)), "self")];
        edges.extend(fan(3, |index| (format!("u{index}"), None), ("a", Some(A))));
        let limits = Limits {
            max_queued: 1,
            ..Limits::DEFAULT
        };
        let usage = run(&collection(edges), "a", limits);
        assert_eq!(usage.truncation, ExportUsageTruncation::ExpansionBudget);
        let cycles = usage
            .paths
            .iter()
            .filter(|path| path.end == ExportUsagePathEnd::Cycle)
            .count();
        assert_eq!(cycles, 1, "{usage:?}");
    }

    /// Cycle detection: a chain is terminal as soon as it exists when its
    /// first edge consumes the export it provides, and when an extension
    /// revisits an export. Mirrors the "Cycle detection" table in the
    /// contract.
    #[test]
    fn cycle_detection_table() {
        use ExportUsagePathEnd::{Cycle, Root, Truncated};
        struct Case {
            name: &'static str,
            edges: Vec<CollectedExportEdge>,
            module: &'static str,
            limits: Limits,
            /// Module names along each chain with its end, in output order.
            chains: Vec<(Vec<&'static str>, ExportUsagePathEnd)>,
            truncation: ExportUsageTruncation,
        }
        let none = ExportUsageTruncation::None;
        let cases = [
            Case {
                name: "a self-referencing export alone ends once",
                edges: vec![edge(("a", Some(A)), ("a", Some(A)), "1")],
                module: "a",
                limits: Limits::DEFAULT,
                chains: vec![(vec!["a", "a"], Cycle)],
                truncation: none,
            },
            Case {
                name: "a self-referencing export with another consumer is not extended by it",
                edges: vec![
                    edge(("a", Some(A)), ("a", Some(A)), "1"),
                    edge(("entry", None), ("a", Some(A)), "2"),
                ],
                module: "a",
                limits: Limits::DEFAULT,
                chains: vec![(vec!["a", "a"], Cycle), (vec!["entry", "a"], Root)],
                truncation: none,
            },
            Case {
                name: "a self-referencing module-scope export ends once",
                edges: vec![
                    edge(("a", None), ("a", None), "1"),
                    edge(("entry", None), ("a", None), "2"),
                ],
                module: "a",
                limits: Limits::DEFAULT,
                chains: vec![(vec!["a", "a"], Cycle), (vec!["entry", "a"], Root)],
                truncation: none,
            },
            Case {
                name: "an edge between two exports of one module is not a self-reference",
                edges: vec![
                    edge(("a", Some(A)), ("a", Some(B)), "1"),
                    edge(("entry", None), ("a", Some(A)), "2"),
                ],
                module: "a",
                limits: Limits::DEFAULT,
                chains: vec![(vec!["entry", "a"], Root), (vec!["entry", "a", "a"], Root)],
                truncation: none,
            },
            Case {
                name: "a self-referencing export keeps its end at a one-edge depth limit",
                edges: vec![edge(("a", Some(A)), ("a", Some(A)), "1")],
                module: "a",
                limits: Limits {
                    max_edges_per_path: 1,
                    ..Limits::DEFAULT
                },
                chains: vec![(vec!["a", "a"], Cycle)],
                truncation: none,
            },
            Case {
                name: "a self-referencing export keeps its end when the expansion budget is spent",
                edges: vec![
                    edge(("a", Some(A)), ("a", Some(A)), "1"),
                    edge(("entry", None), ("a", Some(A)), "2"),
                ],
                module: "a",
                limits: Limits {
                    max_expansions: 0,
                    ..Limits::DEFAULT
                },
                chains: vec![(vec!["a", "a"], Cycle), (vec!["entry", "a"], Truncated)],
                truncation: ExportUsageTruncation::ExpansionBudget,
            },
            Case {
                name: "a self-referencing consumer upstream closes a cycle and is extended no \
                       further",
                edges: vec![
                    edge(("a", Some(A)), ("b", Some(B)), "1"),
                    edge(("a", Some(A)), ("a", Some(A)), "2"),
                    edge(("entry", None), ("a", Some(A)), "3"),
                ],
                module: "b",
                limits: Limits::DEFAULT,
                chains: vec![
                    (vec!["a", "a", "b"], Cycle),
                    (vec!["entry", "a", "b"], Root),
                ],
                truncation: none,
            },
            Case {
                name: "a self-referencing export does not use up the chain quota",
                edges: vec![
                    edge(("a", Some(A)), ("a", Some(A)), "1"),
                    edge(("e1", None), ("a", Some(A)), "2"),
                    edge(("e2", None), ("a", Some(A)), "3"),
                ],
                module: "a",
                limits: Limits {
                    max_paths: 3,
                    ..Limits::DEFAULT
                },
                chains: vec![
                    (vec!["a", "a"], Cycle),
                    (vec!["e1", "a"], Root),
                    (vec!["e2", "a"], Root),
                ],
                truncation: none,
            },
        ];
        for case in cases {
            let usage = run(&collection(case.edges), case.module, case.limits);
            let chains: Vec<(Vec<String>, ExportUsagePathEnd)> = usage
                .paths
                .iter()
                .map(|path| (modules(path), path.end))
                .collect();
            let expected: Vec<(Vec<String>, ExportUsagePathEnd)> = case
                .chains
                .iter()
                .map(|(names, end)| (names.iter().map(|n| (*n).to_owned()).collect(), *end))
                .collect();
            assert_eq!(chains, expected, "{}", case.name);
            assert_eq!(usage.truncation, case.truncation, "{}", case.name);
        }
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

    /// Traversal with a cancellation signal that turns true at its
    /// `cancel_at`-th poll (zero-based); `None` never cancels.
    fn run_cancellable(
        collection: &Collection,
        module: &str,
        cancel_at: Option<usize>,
    ) -> (ExportUsage, TraversalStats) {
        let polls = AtomicUsize::new(0);
        let cancelled = || cancel_at.is_some_and(|at| polls.fetch_add(1, Ordering::SeqCst) >= at);
        let mut context = Context::new(
            collection,
            PathBuf::from("/project"),
            "project".to_owned(),
            Vec::new(),
            SourceStore::new(false),
        );
        context.cancelled = Some(&cancelled);
        let usage = collection.export_usage.as_ref().unwrap();
        traverse_with_stats(
            &context,
            &ExportUsageIndex::new(usage),
            module,
            Limits::DEFAULT,
        )
    }

    /// Cancellation of the traversal. Mirrors the cancellation table in the
    /// contract: the signal is polled before each chain is taken from the
    /// queue, and a set signal stops the traversal without further work.
    #[test]
    fn traversal_stops_when_the_run_is_cancelled() {
        let names = ["m0", "m1", "m2", "m3", "m4"];
        let edges: Vec<CollectedExportEdge> = names
            .windows(2)
            .map(|pair| edge((pair[0], Some(A)), (pair[1], Some(A)), "1"))
            .collect();
        let data = collection(edges);
        // Never cancelled: every chain is extended to its start.
        let (usage, stats) = run_cancellable(&data, "m4", None);
        assert!(!stats.cancelled);
        assert_eq!(stats.expansions, 4);
        assert_eq!(usage.paths[0].end, ExportUsagePathEnd::Root);
        // Cancelled before the first chain is taken: no extension happens.
        let (usage, stats) = run_cancellable(&data, "m4", Some(0));
        assert!(stats.cancelled);
        assert_eq!(stats.expansions, 0);
        assert!(usage.paths.is_empty());
        // Cancelled later: the extensions done so far are all that happens.
        for at in 1..4 {
            let (_, stats) = run_cancellable(&data, "m4", Some(at));
            assert!(stats.cancelled, "cancel at {at}");
            assert_eq!(stats.expansions, at, "cancel at {at}");
        }
        // A signal that turns true after the last chain changes nothing.
        let (usage, stats) = run_cancellable(&data, "m4", Some(5));
        assert!(!stats.cancelled);
        assert_eq!(usage.paths[0].end, ExportUsagePathEnd::Root);
    }

    #[test]
    fn a_dense_graph_stops_at_the_first_poll_after_cancellation() {
        // Every consumer has the same many consumers: uncancelled, the
        // traversal would run to its limits.
        let mut edges = fan(
            40,
            |index| (format!("mid{index}"), Some(A)),
            ("leaf", Some(A)),
        );
        for index in 0..40 {
            edges.extend(fan(
                40,
                |outer| (format!("outer{index}-{outer}"), Some(A)),
                (&format!("mid{index}"), Some(A)),
            ));
        }
        let (_, stats) = run_cancellable(&collection(edges), "leaf", Some(3));
        assert!(stats.cancelled);
        assert_eq!(stats.expansions, 3);
    }

    #[derive(Clone)]
    struct LogWriter(Arc<Mutex<Vec<u8>>>);

    impl std::io::Write for LogWriter {
        fn write(&mut self, bytes: &[u8]) -> std::io::Result<usize> {
            self.0.lock().unwrap().extend_from_slice(bytes);
            Ok(bytes.len())
        }

        fn flush(&mut self) -> std::io::Result<()> {
            Ok(())
        }
    }

    /// The debug log written while deriving paths for `module_id`.
    fn log_for(modules: Vec<CollectedModule>, module_id: &str) -> String {
        let logs = Arc::new(Mutex::new(Vec::new()));
        let writer = logs.clone();
        let subscriber = tracing_subscriber::fmt()
            .with_ansi(false)
            .with_max_level(tracing::Level::DEBUG)
            .with_writer(move || LogWriter(writer.clone()))
            .finish();
        let _guard = tracing::subscriber::set_default(subscriber);
        let mut data = collection(vec![edge(("a", Some(A)), (module_id, Some(B)), "1")]);
        data.modules = modules;
        build_for(&data, module_id);
        let bytes = logs.lock().unwrap().clone();
        String::from_utf8(bytes).unwrap()
    }

    /// What the debug log names for a module. Mirrors the logging table in
    /// the contract: never the raw identifier.
    #[test]
    fn the_log_never_names_the_raw_module_identifier() {
        let secret = "/project/src/app.js?token=hunter2";
        let loader =
            "/project/node_modules/loader.js??ruleSet[1].rules[0].use[0]!/project/src/app.js";
        let collected = |id: &str, path: Option<&str>| CollectedModule {
            id: id.to_owned(),
            path: path.map(str::to_owned),
            ..CollectedModule::default()
        };
        // A collected module with a path is named by its display path.
        let log = log_for(vec![collected(secret, Some("/project/src/app.js"))], secret);
        assert!(log.contains("module=src/app.js"), "{log}");
        assert!(!log.contains("hunter2"), "{log}");
        // A collected module without a path and a module that was not
        // collected are named by a digest prefix.
        for modules in [vec![collected(loader, None)], Vec::new()] {
            let log = log_for(modules, loader);
            assert!(log.contains("module=module-"), "{log}");
            assert!(!log.contains("ruleSet"), "{log}");
            assert!(!log.contains("loader.js"), "{log}");
        }
        // The digest is stable for one identifier and differs between two.
        let empty = Collection::default();
        let context = Context::new(
            &empty,
            PathBuf::from("/project"),
            "project".to_owned(),
            Vec::new(),
            SourceStore::new(false),
        );
        let first = context.module_log_label(secret);
        assert_eq!(first, context.module_log_label(secret));
        assert_ne!(first, context.module_log_label(loader));
        assert_eq!(first.len(), "module-".len() + 16);
    }
}
