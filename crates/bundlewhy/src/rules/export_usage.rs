//! Export-level usage paths for Rspack diagnostics.
//!
//! The engine reports export-to-export edges (`consumer` uses `provider`).
//! This module only follows those edges from the diagnostic module toward its
//! consumers. The edges are engine observations; the chains, their order and
//! the choice of which chains to show are derived by bundlewhy. A chain is
//! not a call sequence and says nothing about removable code or byte savings.

use std::collections::{HashMap, VecDeque};

use super::Context;
use crate::{
    collect::{CollectedExportEdge, CollectedExportRef, CollectedExportUsage},
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
const NOTE_NO_REASON: &str = "The engine did not provide export usage data for this build.";

/// Engine edges grouped by the export they provide.
pub struct ExportUsageIndex<'a> {
    usage: &'a CollectedExportUsage,
    /// Edges sorted by consumer, provider, dependency ID and location.
    by_provider: HashMap<&'a CollectedExportRef, Vec<&'a CollectedExportEdge>>,
}

impl<'a> ExportUsageIndex<'a> {
    pub fn new(usage: &'a CollectedExportUsage) -> Self {
        let mut edges: Vec<&CollectedExportEdge> = usage.edges.iter().collect();
        edges.sort_by(|left, right| {
            (
                &left.consumer,
                &left.provider,
                &left.dependency_id,
                &left.location,
            )
                .cmp(&(
                    &right.consumer,
                    &right.provider,
                    &right.dependency_id,
                    &right.location,
                ))
        });
        let mut by_provider: HashMap<&CollectedExportRef, Vec<&CollectedExportEdge>> =
            HashMap::new();
        for edge in edges {
            by_provider.entry(&edge.provider).or_default().push(edge);
        }
        Self { usage, by_provider }
    }

    /// Edges whose provider is any export of `module`, in stable order.
    fn edges_into_module(&self, module: &str) -> Vec<&'a CollectedExportEdge> {
        let mut edges: Vec<&CollectedExportEdge> = self
            .by_provider
            .iter()
            .filter(|(provider, _)| provider.module == module)
            .flat_map(|(_, edges)| edges.iter().copied())
            .collect();
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
        edges
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
        return unavailable(usage.reason.as_deref().unwrap_or(NOTE_NO_REASON));
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

pub fn traverse(
    context: &Context<'_>,
    index: &ExportUsageIndex<'_>,
    module_id: &str,
    limits: Limits,
) -> ExportUsage {
    let starts = index.edges_into_module(module_id);
    if starts.is_empty() {
        return unavailable(NOTE_NO_EDGE);
    }
    let mut queue: VecDeque<EdgePath<'_>> = starts.into_iter().map(|edge| vec![edge]).collect();
    // Each chain is stored outermost consumer last while it grows.
    let mut finished: Vec<(EdgePath<'_>, ExportUsagePathEnd)> = Vec::new();
    let mut truncation = ExportUsageTruncation::None;
    let mut expansions = 0usize;
    'search: while let Some(path) = queue.pop_front() {
        if finished.len() >= limits.max_paths {
            note_limit(&mut truncation, ExportUsageTruncation::PathCount);
            break;
        }
        if expansions >= limits.max_expansions {
            note_limit(&mut truncation, ExportUsageTruncation::ExpansionBudget);
            for rest in std::iter::once(path).chain(queue.drain(..)) {
                if finished.len() >= limits.max_paths {
                    break;
                }
                finished.push((rest, ExportUsagePathEnd::Truncated));
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
            if revisits {
                if finished.len() >= limits.max_paths {
                    note_limit(&mut truncation, ExportUsageTruncation::PathCount);
                    break 'search;
                }
                finished.push((next, ExportUsagePathEnd::Cycle));
            } else {
                queue.push_back(next);
            }
        }
        if closed_on_itself {
            if finished.len() >= limits.max_paths {
                note_limit(&mut truncation, ExportUsageTruncation::PathCount);
                break;
            }
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
    let skipped = index.usage.skipped_edges;
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

    #[test]
    fn an_unavailable_graph_keeps_the_adapter_reason_or_a_default() {
        let mut data = collection(Vec::new());
        data.export_usage = Some(CollectedExportUsage {
            available: false,
            reason: Some("hook missing".to_owned()),
            ..CollectedExportUsage::default()
        });
        let usage = build_for(&data, "a");
        assert_eq!(usage.state, ExportUsageState::Unavailable);
        assert_eq!(usage.note, "hook missing");
        data.export_usage = Some(CollectedExportUsage::default());
        assert_eq!(build_for(&data, "a").note, NOTE_NO_REASON);
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
