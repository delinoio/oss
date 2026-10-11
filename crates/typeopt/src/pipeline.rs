// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Transactional optimization pipeline: plan, size-gate, apply, recheck and
//! repeat until no accepted reduction remains. The pipeline only sees plain
//! text and byte spans; analysis lives behind [`Backend`].
use std::collections::{BTreeMap, BTreeSet, HashMap};

use anyhow::{Result, bail, ensure};

use crate::{
    optimizer::{Mapping, Preservation},
    passes::{PassId, PassSet},
    plan::{Candidate, Edit, Location, Phase, Plan, PropertyStats, Reason, Skip},
};

/// Upper bound on structural rounds. Each accepted round strictly reduces the
/// emitted size, so the loop terminates anyway; the bound keeps worst-case
/// run time predictable.
pub const MAX_ROUNDS: usize = 64;

pub trait Backend {
    /// Analyze the program with `texts` substituted for application sources
    /// and plan candidates for `phase`. Disabled passes may still analyze but
    /// must not be applied; the pipeline enforces this.
    fn plan(
        &mut self,
        texts: &BTreeMap<String, String>,
        phase: Phase,
        enabled: &PassSet,
    ) -> Result<Plan>;
    /// Type-check the program with `texts`; returns modules with semantic
    /// diagnostics. Syntax errors and compiler failures are returned as
    /// errors because they indicate an internal transformation failure.
    fn check(&mut self, texts: &BTreeMap<String, String>) -> Result<Vec<String>>;
    /// Emitted ESM byte count for one module, using equal emitter settings
    /// for every call. Source maps are excluded.
    fn emitted_size(&mut self, file: &str, text: &str) -> Result<usize>;
}

#[derive(Clone, Debug)]
pub struct Bundle {
    pub id: usize,
    pub phase: Phase,
    pub round: usize,
    pub pass: PassId,
    pub uses: Vec<PassId>,
    pub key: String,
    /// Location in the original source.
    pub location: Location,
    pub files: Vec<String>,
    pub bytes_before: usize,
    pub bytes_after: usize,
}

#[derive(Debug, Default)]
pub struct Outcome {
    pub texts: BTreeMap<String, String>,
    /// Per module, the accepted edits of each round, relative to the text of
    /// that round. Applying them in order reproduces `texts`.
    pub chains: BTreeMap<String, Vec<Vec<Edit>>>,
    pub bundles: Vec<Bundle>,
    pub skips: Vec<Skip>,
    pub mappings: Vec<Mapping>,
    pub property_stats: Option<PropertyStats>,
    pub rounds: usize,
    pub round_limit_reached: bool,
    pub size_before: usize,
    pub size_after: usize,
}

pub fn apply(source: &str, edits: &[Edit]) -> Result<String> {
    let mut text = String::with_capacity(source.len());
    let mut previous = 0;
    for edit in edits {
        ensure!(
            edit.start >= previous
                && edit.end >= edit.start
                && edit.end <= source.len()
                && source.is_char_boundary(edit.start)
                && source.is_char_boundary(edit.end),
            "invalid or overlapping transformation edits"
        );
        text.push_str(&source[previous..edit.start]);
        text.push_str(&edit.replacement);
        previous = edit.end;
    }
    text.push_str(&source[previous..]);
    Ok(text)
}

/// Maps an offset in the text produced by `edits` back to the text they were
/// applied to. Offsets inside a replacement map to the replaced range start.
pub fn original_offset(offset: usize, edits: &[Edit]) -> usize {
    let mut delta: isize = 0;
    for edit in edits {
        let start = (edit.start as isize + delta) as usize;
        if offset < start {
            break;
        }
        if offset < start + edit.replacement.len() {
            return edit.start;
        }
        delta += edit.replacement.len() as isize - (edit.end - edit.start) as isize;
    }
    (offset as isize - delta).max(0) as usize
}

/// Maps an offset in the final text through every round back to the source.
pub fn source_offset(mut offset: usize, chain: &[Vec<Edit>]) -> usize {
    for edits in chain.iter().rev() {
        offset = original_offset(offset, edits);
    }
    offset
}

/// Two edits conflict when their ranges intersect or they start at the same
/// offset, where application order would be ambiguous.
fn conflicts(a: &Edit, b: &Edit) -> bool {
    a.start == b.start || (a.start < b.end && b.start < a.end)
}

struct Gate<'a, B: Backend> {
    backend: &'a mut B,
    texts: &'a BTreeMap<String, String>,
    accepted: BTreeMap<String, Vec<Edit>>,
    sizes: HashMap<String, usize>,
}

impl<B: Backend> Gate<'_, B> {
    fn current_size(&mut self, file: &str) -> Result<usize> {
        if let Some(&size) = self.sizes.get(file) {
            return Ok(size);
        }
        let Some(text) = self.texts.get(file) else {
            bail!("transformation targets a module outside the analyzed program");
        };
        let text = apply(text, self.accepted.get(file).map_or(&[], Vec::as_slice))?;
        let size = self.backend.emitted_size(file, &text)?;
        self.sizes.insert(file.to_owned(), size);
        Ok(size)
    }

    /// Returns `Some((before, after))` if accepted, `None` on no benefit.
    fn try_accept(&mut self, candidate: &Candidate) -> Result<Option<(usize, usize)>> {
        let mut by_file = BTreeMap::<&str, Vec<Edit>>::new();
        for edit in &candidate.edits {
            by_file
                .entry(edit.file.as_str())
                .or_default()
                .push(edit.edit.clone());
        }
        let mut before = 0;
        let mut after = 0;
        let mut merged_files = Vec::new();
        for (file, mut edits) in by_file {
            edits.sort();
            for pair in edits.windows(2) {
                ensure!(
                    !conflicts(&pair[0], &pair[1]),
                    "{} produced overlapping edits",
                    candidate.pass
                );
            }
            before += self.current_size(file)?;
            let mut merged = self.accepted.get(file).cloned().unwrap_or_default();
            merged.extend(edits);
            merged.sort();
            let text = apply(&self.texts[file], &merged)?;
            let size = self.backend.emitted_size(file, &text).map_err(|error| {
                anyhow::anyhow!(
                    "{} produced output the compiler rejects in a module: {error}",
                    candidate.pass
                )
            })?;
            after += size;
            merged_files.push((file.to_owned(), merged, size));
        }
        if after >= before {
            return Ok(None);
        }
        for (file, merged, size) in merged_files {
            self.sizes.insert(file.clone(), size);
            self.accepted.insert(file, merged);
        }
        Ok(Some((before, after)))
    }

    fn overlaps(&self, candidate: &Candidate) -> bool {
        candidate.edits.iter().any(|edit| {
            self.accepted
                .get(&edit.file)
                .is_some_and(|accepted| accepted.iter().any(|a| conflicts(a, &edit.edit)))
        })
    }
}

pub struct Pipeline<'a, B: Backend> {
    pub backend: &'a mut B,
    pub enabled: &'a PassSet,
}

struct Round {
    plan: Plan,
    accepted: Vec<(Candidate, usize, usize)>,
    skips: Vec<Skip>,
    /// Property mappings of rejected bundles, now reported as preserved.
    rejected: Vec<Mapping>,
}

/// Mappings of a bundle that was not applied keep their original names.
fn preserved(candidate: &Candidate, reason: Preservation) -> impl Iterator<Item = Mapping> + '_ {
    candidate.mappings.iter().map(move |m| Mapping {
        renamed: m.original.clone(),
        preservation: Some(reason.clone()),
        ..m.clone()
    })
}

impl<B: Backend> Pipeline<'_, B> {
    pub fn run(&mut self, sources: &BTreeMap<String, String>) -> Result<Outcome> {
        let mut outcome = Outcome {
            texts: sources.clone(),
            ..Outcome::default()
        };
        outcome.size_before = self.total_size(sources)?;
        let mut blacklist = BTreeSet::new();
        let mut permanent = Vec::new();
        for phase in [Phase::Structural, Phase::Property] {
            if phase == Phase::Property && !self.enabled.any_property() {
                continue;
            }
            let mut final_skips = Vec::new();
            for round in 0.. {
                if round == MAX_ROUNDS {
                    outcome.round_limit_reached = true;
                    tracing::warn!(?phase, "round limit reached; stopping deterministically");
                    break;
                }
                crate::app::check_cancelled()?;
                let current = self.round(&outcome.texts, phase, &blacklist)?;
                outcome.rounds += 1;
                final_skips = current.skips;
                if phase == Phase::Property {
                    outcome
                        .mappings
                        .extend(current.plan.mappings.iter().cloned());
                    outcome.mappings.extend(current.rejected.iter().cloned());
                    outcome.property_stats = current.plan.property_stats.clone();
                }
                if current.accepted.is_empty() {
                    break;
                }
                let applied = self.commit(
                    &mut outcome,
                    current.accepted,
                    phase,
                    round,
                    &mut blacklist,
                    &mut permanent,
                )?;
                tracing::info!(
                    ?phase,
                    round,
                    accepted = applied,
                    "transformation round committed"
                );
                // Property allocation is global; one round produces the
                // complete mapping. A second round would only see renamed
                // names.
                if phase == Phase::Property {
                    break;
                }
            }
            for skip in final_skips {
                outcome.skips.push(skip);
            }
        }
        outcome.skips.extend(permanent);
        // Report source coordinates of the original text.
        for skip in &mut outcome.skips {
            skip.location.offset = source_offset(
                skip.location.offset,
                outcome
                    .chains
                    .get(&skip.location.file)
                    .map_or(&[], Vec::as_slice),
            );
        }
        outcome.skips.sort_by(|a, b| {
            (a.pass.rank(), &a.location, &a.key, a.reason).cmp(&(
                b.pass.rank(),
                &b.location,
                &b.key,
                b.reason,
            ))
        });
        outcome.skips.dedup_by(|a, b| {
            a.pass == b.pass && a.key == b.key && a.reason == b.reason && a.location == b.location
        });
        outcome.size_after = self.total_size(&outcome.texts)?;
        Ok(outcome)
    }

    fn total_size(&mut self, texts: &BTreeMap<String, String>) -> Result<usize> {
        let mut total = 0;
        for (file, text) in texts {
            crate::app::check_cancelled()?;
            total += self.backend.emitted_size(file, text)?;
        }
        Ok(total)
    }

    fn round(
        &mut self,
        texts: &BTreeMap<String, String>,
        phase: Phase,
        blacklist: &BTreeSet<String>,
    ) -> Result<Round> {
        let mut plan = self.backend.plan(texts, phase, self.enabled)?;
        let mut candidates = std::mem::take(&mut plan.candidates);
        candidates.sort_by(|a, b| {
            (a.pass.rank(), &a.location, &a.key).cmp(&(b.pass.rank(), &b.location, &b.key))
        });
        let mut skips = std::mem::take(&mut plan.skips);
        let mut accepted = Vec::new();
        let mut rejected = Vec::new();
        let mut gate = Gate {
            backend: &mut *self.backend,
            texts,
            accepted: BTreeMap::new(),
            sizes: HashMap::new(),
        };
        let mut seen = BTreeSet::new();
        for candidate in candidates {
            crate::app::check_cancelled()?;
            ensure!(
                seen.insert((candidate.pass, candidate.key.clone())),
                "{} produced duplicate candidate identities",
                candidate.pass
            );
            let skip = |reason| Skip {
                pass: candidate.pass,
                key: candidate.key.clone(),
                location: candidate.location.clone(),
                reason,
            };
            if candidate.passes().any(|p| !self.enabled.contains(p)) {
                skips.push(skip(Reason::RequiresDisabledPass));
                rejected.extend(preserved(&candidate, Preservation::RequiresDisabledPass));
                continue;
            }
            if blacklist.contains(&candidate.key) {
                // The permanent record already explains this candidate.
                rejected.extend(preserved(&candidate, Preservation::RecheckFailed));
                continue;
            }
            if candidate.edits.is_empty() {
                continue;
            }
            if gate.overlaps(&candidate) {
                // Re-planned against the updated text in the next round.
                continue;
            }
            match gate.try_accept(&candidate)? {
                Some((before, after)) => accepted.push((candidate, before, after)),
                None => {
                    skips.push(skip(Reason::NoSizeBenefit));
                    rejected.extend(preserved(&candidate, Preservation::NoSizeBenefit));
                }
            }
        }
        Ok(Round {
            plan,
            accepted,
            skips,
            rejected,
        })
    }

    fn commit(
        &mut self,
        outcome: &mut Outcome,
        mut accepted: Vec<(Candidate, usize, usize)>,
        phase: Phase,
        round: usize,
        blacklist: &mut BTreeSet<String>,
        permanent: &mut Vec<Skip>,
    ) -> Result<usize> {
        // Recheck the transformed program. On failure, find the first bundle
        // whose prefix fails, roll it back and retry with the remainder.
        loop {
            let texts = Self::applied(&outcome.texts, &accepted)?;
            let failing = self.backend.check(&texts)?;
            if failing.is_empty() {
                break;
            }
            let culprit = self.first_failing(&outcome.texts, &accepted)?;
            let (candidate, _, _) = accepted.remove(culprit);
            tracing::warn!(
                pass = %candidate.pass,
                modules = failing.len(),
                "transformed program failed type checking; rolling back one bundle"
            );
            blacklist.insert(candidate.key.clone());
            outcome
                .mappings
                .extend(preserved(&candidate, Preservation::RecheckFailed));
            permanent.push(Skip {
                pass: candidate.pass,
                key: candidate.key.clone(),
                location: Location {
                    file: candidate.location.file.clone(),
                    offset: source_offset(
                        candidate.location.offset,
                        outcome
                            .chains
                            .get(&candidate.location.file)
                            .map_or(&[], Vec::as_slice),
                    ),
                },
                reason: Reason::RecheckFailed,
            });
            if accepted.is_empty() {
                return Ok(0);
            }
        }
        let mut per_file = BTreeMap::<String, Vec<Edit>>::new();
        for (candidate, before, after) in &accepted {
            let mut files = BTreeSet::new();
            for edit in &candidate.edits {
                per_file
                    .entry(edit.file.clone())
                    .or_default()
                    .push(edit.edit.clone());
                files.insert(edit.file.clone());
            }
            let chain = outcome
                .chains
                .get(&candidate.location.file)
                .map_or(&[][..], Vec::as_slice);
            outcome.bundles.push(Bundle {
                id: outcome.bundles.len(),
                phase,
                round,
                pass: candidate.pass,
                uses: candidate.uses.clone(),
                key: candidate.key.clone(),
                location: Location {
                    file: candidate.location.file.clone(),
                    offset: source_offset(candidate.location.offset, chain),
                },
                files: files.into_iter().collect(),
                bytes_before: *before,
                bytes_after: *after,
            });
            outcome.mappings.extend(candidate.mappings.iter().cloned());
        }
        for (file, mut edits) in per_file {
            edits.sort();
            let text = apply(&outcome.texts[&file], &edits)?;
            outcome.texts.insert(file.clone(), text);
            let chain = outcome.chains.entry(file).or_default();
            chain.push(edits);
        }
        Ok(accepted.len())
    }

    fn applied(
        texts: &BTreeMap<String, String>,
        accepted: &[(Candidate, usize, usize)],
    ) -> Result<BTreeMap<String, String>> {
        let mut per_file = BTreeMap::<&str, Vec<Edit>>::new();
        for (candidate, _, _) in accepted {
            for edit in &candidate.edits {
                per_file
                    .entry(edit.file.as_str())
                    .or_default()
                    .push(edit.edit.clone());
            }
        }
        let mut result = texts.clone();
        for (file, mut edits) in per_file {
            edits.sort();
            result.insert(file.to_owned(), apply(&texts[file], &edits)?);
        }
        Ok(result)
    }

    /// Smallest index `i` such that bundles `0..=i` fail type checking.
    fn first_failing(
        &mut self,
        texts: &BTreeMap<String, String>,
        accepted: &[(Candidate, usize, usize)],
    ) -> Result<usize> {
        let (mut low, mut high) = (0, accepted.len() - 1);
        while low < high {
            crate::app::check_cancelled()?;
            let middle = (low + high) / 2;
            let prefix = Self::applied(texts, &accepted[..=middle])?;
            if self.backend.check(&prefix)?.is_empty() {
                low = middle + 1;
            } else {
                high = middle;
            }
        }
        Ok(low)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::plan::FileEdit;

    /// Test-only backend: candidates come from a fixed script, size is byte
    /// length and a module "fails" type checking when it contains `BAD`.
    struct Fake {
        script: Box<dyn FnMut(&BTreeMap<String, String>, Phase) -> Vec<Candidate>>,
        checks: usize,
    }
    impl Backend for Fake {
        fn plan(
            &mut self,
            texts: &BTreeMap<String, String>,
            phase: Phase,
            _: &PassSet,
        ) -> Result<Plan> {
            Ok(Plan {
                candidates: (self.script)(texts, phase),
                ..Plan::default()
            })
        }

        fn check(&mut self, texts: &BTreeMap<String, String>) -> Result<Vec<String>> {
            self.checks += 1;
            Ok(texts
                .iter()
                .filter(|(_, t)| t.contains("BAD"))
                .map(|(f, _)| f.clone())
                .collect())
        }

        fn emitted_size(&mut self, _: &str, text: &str) -> Result<usize> {
            Ok(text.len())
        }
    }

    fn candidate(pass: PassId, key: &str, edits: Vec<FileEdit>) -> Candidate {
        let mut c = Candidate::new(
            pass,
            key,
            Location {
                file: edits[0].file.clone(),
                offset: edits[0].edit.start,
            },
        );
        c.edits = edits;
        c
    }

    fn sources(text: &str) -> BTreeMap<String, String> {
        [("a.ts".to_owned(), text.to_owned())].into()
    }

    #[test]
    fn accepts_only_strict_reductions_and_converges() {
        let mut fake = Fake {
            script: Box::new(|texts, phase| {
                let text = &texts["a.ts"];
                let mut out = Vec::new();
                if phase != Phase::Structural {
                    return out;
                }
                if let Some(at) = text.find("long") {
                    // Insertion plus deletion: a temporary growth that nets
                    // a decrease is one bundle.
                    out.push(candidate(
                        PassId::CallInlining,
                        &format!("shrink-{at}"),
                        vec![
                            FileEdit::delete("a.ts", at, at + 4),
                            FileEdit::insert("a.ts", text.len(), "xx"),
                        ],
                    ));
                }
                out.push(candidate(
                    PassId::SignatureReduction,
                    "grow",
                    vec![FileEdit::insert("a.ts", text.len(), "growth")],
                ));
                out
            }),
            checks: 0,
        };
        let enabled = PassSet::all();
        let outcome = Pipeline {
            backend: &mut fake,
            enabled: &enabled,
        }
        .run(&sources("long long "))
        .unwrap();
        // Each round removes one `long` (net -2) until none remain; the
        // growth candidate is never accepted.
        assert_eq!(outcome.texts["a.ts"], "  xxxx");
        assert_eq!(outcome.bundles.len(), 2);
        assert!(
            outcome
                .bundles
                .iter()
                .all(|b| b.bytes_after < b.bytes_before)
        );
        assert!(
            outcome
                .skips
                .iter()
                .any(|s| s.key == "grow" && s.reason == Reason::NoSizeBenefit)
        );
        assert_eq!(outcome.size_before - outcome.size_after, 4);
        // Source offsets of later rounds map back to the original text.
        assert_eq!(outcome.bundles[1].location.offset, 5);
    }

    #[test]
    fn disabled_cleanup_is_never_applied() {
        let mut fake = Fake {
            script: Box::new(|texts, phase| {
                if phase != Phase::Structural || !texts["a.ts"].contains("dead") {
                    return Vec::new();
                }
                let mut c = candidate(
                    PassId::CallInlining,
                    "inline-with-cleanup",
                    vec![FileEdit::delete("a.ts", 0, 4)],
                );
                c.uses = vec![PassId::ResidualDependencyRemoval];
                vec![c]
            }),
            checks: 0,
        };
        let enabled = PassSet::without(&[PassId::ResidualDependencyRemoval]);
        let outcome = Pipeline {
            backend: &mut fake,
            enabled: &enabled,
        }
        .run(&sources("dead code"))
        .unwrap();
        assert_eq!(outcome.texts["a.ts"], "dead code");
        assert!(outcome.bundles.is_empty());
        assert_eq!(outcome.skips[0].reason, Reason::RequiresDisabledPass);
    }

    #[test]
    fn failing_recheck_rolls_back_only_the_culprit() {
        let mut fake = Fake {
            script: Box::new(|texts, phase| {
                let text = &texts["a.ts"];
                if phase != Phase::Structural {
                    return Vec::new();
                }
                let mut out = Vec::new();
                if let Some(at) = text.find("one ") {
                    out.push(candidate(
                        PassId::UnusedFieldRemoval,
                        "one",
                        vec![FileEdit::delete("a.ts", at, at + 4)],
                    ));
                }
                if let Some(at) = text.find("twotwo") {
                    out.push(candidate(
                        PassId::UnusedMethodRemoval,
                        "two",
                        vec![FileEdit::replace("a.ts", at, at + 6, "BAD")],
                    ));
                }
                if let Some(at) = text.find("three ") {
                    out.push(candidate(
                        PassId::ResidualDependencyRemoval,
                        "three",
                        vec![FileEdit::delete("a.ts", at, at + 6)],
                    ));
                }
                out
            }),
            checks: 0,
        };
        let enabled = PassSet::all();
        let outcome = Pipeline {
            backend: &mut fake,
            enabled: &enabled,
        }
        .run(&sources("one twotwo three end"))
        .unwrap();
        assert_eq!(outcome.texts["a.ts"], "twotwo end");
        assert!(
            outcome
                .skips
                .iter()
                .any(|s| s.key == "two" && s.reason == Reason::RecheckFailed)
        );
        assert_eq!(outcome.bundles.len(), 2);
    }

    #[test]
    fn overlapping_candidates_wait_for_the_next_round() {
        let mut fake = Fake {
            script: Box::new(|texts, phase| {
                let text = &texts["a.ts"];
                if phase != Phase::Structural {
                    return Vec::new();
                }
                let mut out = Vec::new();
                if let Some(at) = text.find("abcdef") {
                    out.push(candidate(
                        PassId::EnumRuntimeReduction,
                        "outer",
                        vec![FileEdit::replace("a.ts", at, at + 6, "ab")],
                    ));
                    out.push(candidate(
                        PassId::ResidualDependencyRemoval,
                        "inner",
                        vec![FileEdit::delete("a.ts", at + 2, at + 4)],
                    ));
                }
                out
            }),
            checks: 0,
        };
        let enabled = PassSet::all();
        let outcome = Pipeline {
            backend: &mut fake,
            enabled: &enabled,
        }
        .run(&sources("abcdef"))
        .unwrap();
        assert_eq!(outcome.texts["a.ts"], "ab");
        assert_eq!(outcome.bundles.len(), 1);
        assert_eq!(outcome.bundles[0].pass, PassId::EnumRuntimeReduction);
    }

    #[test]
    fn offsets_map_through_rounds() {
        let first = vec![Edit {
            start: 2,
            end: 6,
            replacement: "x".into(),
        }];
        let second = vec![Edit {
            start: 0,
            end: 0,
            replacement: "inserted".into(),
        }];
        // "ab" + "x" + rest; then "inserted" prefix.
        assert_eq!(source_offset(3, &[first.clone(), second.clone()]), 0);
        assert_eq!(source_offset(8 + 2, &[first.clone(), second.clone()]), 2);
        assert_eq!(source_offset(8 + 3, &[first, second]), 6);
    }
}
