//! Adapter for the pinned hephaex MeCab-Ko engine and system dictionary.
//!
//! Use the public dictionary, lattice and Viterbi interfaces together: the
//! high-level tokenizer omits node provenance and swallows lookup failures.
//! Reading the selected lattice nodes retains known/unknown evidence and
//! lets dictionary failures remain unavailable rather than become rejection.

use std::path::Path;

use mecab_ko_core::{
    lattice::{Lattice, NodeBuilder, NodeType},
    unknown::UnknownHandler,
    viterbi::ViterbiSearcher,
};
use mecab_ko_dict::{Matrix, SystemDictionary};

use crate::korean::{AnalyzerError, KoreanAnalyzer, Morpheme, Pos, WORD_END, WORD_START, connects};

/// Korean analyzer backed by an explicitly selected bundled dictionary.
/// No environment-based discovery, user dictionary or network access occurs.
pub struct MecabAnalyzer {
    dictionary: SystemDictionary,
}

impl MecabAnalyzer {
    /// Loads the compiled system dictionary from the application's resources.
    ///
    /// # Errors
    ///
    /// Returns [`AnalyzerError::DictionaryUnavailable`] if any resource cannot
    /// be loaded. Upstream errors can contain paths, so they are not logged.
    pub fn load(directory: &Path) -> Result<Self, AnalyzerError> {
        let started = std::time::Instant::now();
        let dictionary = SystemDictionary::load(directory).map_err(|_| {
            tracing::warn!(
                reason = "dictionary-unavailable",
                "fixkey dictionary load failed"
            );
            AnalyzerError::DictionaryUnavailable
        })?;
        tracing::info!(
            language = "ko-dubeolsik",
            entry_count = dictionary.entry_count(),
            duration_us = u64::try_from(started.elapsed().as_micros()).unwrap_or(u64::MAX),
            "fixkey dictionary loaded"
        );
        Ok(Self { dictionary })
    }
}

impl KoreanAnalyzer for MecabAnalyzer {
    fn analyze(&self, word: &str) -> Result<Vec<Morpheme>, AnalyzerError> {
        if word.is_empty() {
            return Ok(Vec::new());
        }
        let mut lattice = Lattice::new(word);
        let unknown = UnknownHandler::korean_default();
        for (start, (byte_start, _)) in word.char_indices().enumerate() {
            let entries = self
                .dictionary
                .common_prefix_search(&word[byte_start..])
                .map_err(|_| AnalyzerError::Failed)?;
            let found = !entries.is_empty();
            for (entry, byte_len) in entries {
                let byte_end = byte_start
                    .checked_add(byte_len)
                    .ok_or(AnalyzerError::Failed)?;
                let surface = word
                    .get(byte_start..byte_end)
                    .ok_or(AnalyzerError::Failed)?;
                if entry.surface != surface || surface.is_empty() {
                    return Err(AnalyzerError::Failed);
                }
                lattice.add_node(
                    NodeBuilder::new(surface, start, start + surface.chars().count())
                        .byte_positions(byte_start, byte_end)
                        .left_id(entry.left_id)
                        .right_id(entry.right_id)
                        .word_cost(i32::from(entry.cost))
                        .node_type(NodeType::Known)
                        .feature(&entry.feature),
                );
            }
            unknown.add_unknown_nodes(&mut lattice, start, found);
        }
        // A sentence tokenizer may prefer a connective ending even when the
        // same surface has a listed sentence-final analysis (for example 해).
        // Rank complete dictionary paths under FixKey's POS grammar instead
        // of accepting EC or relabeling the tokenizer's preferred analysis.
        let path = complete_path(&lattice, &self.dictionary).unwrap_or_else(|| {
            ViterbiSearcher::new().search(&mut lattice, self.dictionary.matrix())
        });
        path.into_iter()
            .map(|id| {
                let node = lattice.node(id).ok_or(AnalyzerError::Failed)?;
                Ok(Morpheme {
                    surface: node.surface.to_string(),
                    pos: Pos::parse_compound(node.feature.split(',').next().unwrap_or_default())
                        .unwrap_or_default(),
                    in_dictionary: node.node_type == NodeType::Known,
                })
            })
            .collect()
    }
}

fn node_tags(node: &mecab_ko_core::lattice::Node) -> Option<Vec<Pos>> {
    let tags = Pos::parse_compound(node.feature.split(',').next()?)?;
    (!tags.is_empty() && tags.windows(2).all(|pair| connects(pair[0], pair[1]))).then_some(tags)
}

/// Finds the least-cost complete dictionary path allowed by the POS rules.
/// Costs and all candidate surfaces/features come from the system dictionary.
fn complete_path(lattice: &Lattice, dictionary: &SystemDictionary) -> Option<Vec<u32>> {
    let mut scores: Vec<Option<(i64, u32, Pos)>> = vec![None; lattice.node_count()];
    for position in 0..lattice.char_len() {
        for node in lattice.nodes_starting_at(position) {
            if node.node_type != NodeType::Known {
                continue;
            }
            let Some(tags) = node_tags(node) else {
                continue;
            };
            let first = *tags.first()?;
            let last = *tags.last()?;
            let previous = if position == 0 {
                WORD_START.contains(&first).then(|| {
                    (
                        i64::from(dictionary.matrix().get(0, node.left_id)),
                        lattice.bos().id,
                    )
                })
            } else {
                lattice
                    .nodes_ending_at(position)
                    .filter_map(|previous| {
                        let (cost, _, previous_tag) = scores[previous.id as usize]?;
                        connects(previous_tag, first).then(|| {
                            (
                                cost + i64::from(
                                    dictionary.matrix().get(previous.right_id, node.left_id),
                                ),
                                previous.id,
                            )
                        })
                    })
                    .min_by_key(|&(cost, _)| cost)
            };
            if let Some((cost, previous)) = previous {
                scores[node.id as usize] = Some((cost + i64::from(node.word_cost), previous, last));
            }
        }
    }
    let mut id = lattice
        .nodes_ending_at(lattice.char_len())
        .filter_map(|node| {
            let (cost, _, last) = scores[node.id as usize]?;
            WORD_END.contains(&last).then(|| {
                (
                    cost + i64::from(dictionary.matrix().get(node.right_id, 0)),
                    node.id,
                )
            })
        })
        .min_by_key(|&(cost, _)| cost)?
        .1;
    let mut path = Vec::new();
    while id != lattice.bos().id {
        path.push(id);
        id = scores[id as usize]?.1;
    }
    path.reverse();
    Some(path)
}
