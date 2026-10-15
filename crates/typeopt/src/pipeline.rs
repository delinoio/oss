// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Transactional optimization pipeline: plan, size-gate, apply, recheck and
//! repeat until no accepted reduction remains. The pipeline only sees plain
//! text and byte spans; analysis lives behind [`Backend`].
use std::collections::{BTreeMap, BTreeSet, HashMap};

use anyhow::{Result, bail, ensure};

use crate::{
    optimizer::{Mapping, Preservation},
    passes::{PassId, PassSet},
    plan::{
        Candidate, Edit, FileEdit, ImportSite, Location, Phase, Plan, PropertyStats, Reason, Skip,
    },
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
    /// Emits one module with equal emitter settings for every call and
    /// returns its ESM byte count (source maps excluded) and its static
    /// import specifiers in evaluation order.
    fn emit_module(&mut self, file: &str, text: &str) -> Result<Emitted>;
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Emitted {
    pub size: usize,
    pub imports: Vec<String>,
}

/// Static module specifiers of emitted ESM in statement order. The same
/// scanner reads both sides of every comparison.
pub fn static_imports(code: &str) -> Vec<String> {
    let mut imports = Vec::new();
    let mut offset = 0;
    for line in code.split_inclusive('\n') {
        let start = offset;
        offset += line.len();
        let keyword = if line.starts_with("import") {
            "import"
        } else if line.starts_with("export") {
            "export"
        } else {
            continue;
        };
        let tail = &code[start..];
        let statement = &tail[..statement_end(tail)];
        let after_keyword = statement[keyword.len()..].trim_start();
        let literal = if keyword == "import" && after_keyword.starts_with(['"', '\'']) {
            Some(after_keyword)
        } else {
            statement
                .rfind(" from ")
                .map(|i| statement[i + 6..].trim_start())
                .filter(|s| s.starts_with(['"', '\'']))
        };
        if let Some(literal) = literal {
            let quote = literal.as_bytes()[0] as char;
            if let Some(end) = literal[1..].find(quote) {
                imports.push(literal[1..1 + end].to_owned());
            }
        }
    }
    imports
}

/// Offset of the first byte at or after `at` that is not whitespace or part
/// of a `/* */` or `//` comment.
pub fn skip_js_trivia(code: &str, mut at: usize) -> usize {
    let len = code.len();
    loop {
        at = at.min(len);
        // ECMAScript whitespace and line terminators.
        while let Some(c) = code[at..].chars().next()
            && is_js_space(c)
        {
            at += c.len_utf8();
        }
        let rest = &code[at..];
        if let Some(block) = rest.strip_prefix("/*") {
            match block.find("*/") {
                Some(end) => at += 2 + end + 2,
                None => return len,
            }
        } else if rest.starts_with("//") {
            match rest.find(['\n', '\r', '\u{2028}', '\u{2029}']) {
                Some(end) => at += end,
                None => return len,
            }
        } else {
            return at;
        }
    }
}

/// ECMAScript whitespace (including NBSP, ZWNBSP and every space
/// separator) or line terminator.
pub fn is_js_space(c: char) -> bool {
    (c.is_whitespace() && c != '\u{85}') || c == '\u{feff}'
}

/// Static module specifiers of bundler output in source order, independent
/// of line layout: every `from"…"` and side-effect `import"…"` at a token
/// boundary, with optional whitespace and comments before the quote. Minified
/// chunks put many import statements on one line, which [`static_imports`] does
/// not scan. Dynamic `import(…)` and members such as `Array.from(…)` do not
/// match. A string that merely contains such text can add a specifier; that
/// is conservative, because the gate rejects a bundle whose specifiers are
/// not a subsequence of the previous ones.
pub fn bundle_static_imports(code: &str) -> Vec<String> {
    let bytes = code.as_bytes();
    // Whether the character before `at` continues an identifier or a member
    // access, so `from`/`import` there is not a keyword.
    let joined = |at: usize| {
        code[..at]
            .chars()
            .next_back()
            .is_some_and(|c| c.is_alphanumeric() || matches!(c, '_' | '$' | '.'))
    };
    let mut imports = Vec::new();
    let mut at = 0;
    while at < bytes.len() {
        // Skip to the next byte that can start a keyword. The gate scans
        // every candidate chunk, so a per-byte prefix comparison over a
        // multi-megabyte chunk dominated bundle-mode run time.
        match bytes[at..].iter().position(|&b| b == b'f' || b == b'i') {
            Some(offset) => at += offset,
            None => break,
        }
        // Compare bytes: `at` can be inside a multi-byte character.
        let keyword = if bytes[at..].starts_with(b"from") {
            4
        } else if bytes[at..].starts_with(b"import") {
            6
        } else {
            at += 1;
            continue;
        };
        let start = at;
        at += keyword;
        if joined(start) {
            continue;
        }
        // Whitespace and comments may separate the keyword from the quote.
        let quote_at = skip_js_trivia(code, at);
        let Some(&quote) = bytes.get(quote_at) else {
            break;
        };
        if quote != b'"' && quote != b'\'' {
            continue;
        }
        if let Some(end) = code[quote_at + 1..].find(quote as char) {
            imports.push(code[quote_at + 1..quote_at + 1 + end].to_owned());
            at = quote_at + 1 + end + 1;
        }
    }
    imports
}

/// End of the statement that starts `code`: one past the first `;` outside a
/// string literal, so a `;` inside a module specifier does not cut it short.
fn statement_end(code: &str) -> usize {
    let mut quote = None;
    let mut escaped = false;
    for (index, ch) in code.char_indices() {
        match quote {
            Some(open) => {
                if escaped {
                    escaped = false;
                } else if ch == '\\' {
                    escaped = true;
                } else if ch == open {
                    quote = None;
                }
            }
            None if ch == '"' || ch == '\'' || ch == '`' => quote = Some(ch),
            None if ch == ';' => return index + 1,
            None => {}
        }
    }
    code.len()
}

/// Elements of `before` missing from `after`, if `after` is `before` with
/// some elements removed in order; `None` when `after` adds or reorders.
fn removed_imports(before: &[String], after: &[String]) -> Option<Vec<String>> {
    let mut removed = Vec::new();
    let mut remaining = after.iter().peekable();
    for import in before {
        if remaining.peek() == Some(&import) {
            remaining.next();
        } else {
            removed.push(import.clone());
        }
    }
    remaining.peek().is_none().then_some(removed)
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

/// Offset `offset` of the original text in the text that `edits` (sorted,
/// against the original) produce, for a position outside every edit.
fn forward_offset(offset: usize, edits: &[Edit]) -> usize {
    let mut shifted = offset as isize;
    for edit in edits {
        if edit.end > offset || edit.start >= offset {
            break;
        }
        shifted += edit.replacement.len() as isize - (edit.end - edit.start) as isize;
    }
    shifted.max(0) as usize
}

/// Two edits conflict when their ranges intersect or they start at the same
/// offset, where application order would be ambiguous.
fn conflicts(a: &Edit, b: &Edit) -> bool {
    a.start == b.start || (a.start < b.end && b.start < a.end)
}

struct Gate<'a, B: Backend> {
    backend: &'a mut B,
    texts: &'a BTreeMap<String, String>,
    imports: &'a BTreeMap<String, Vec<ImportSite>>,
    enabled: &'a PassSet,
    accepted: BTreeMap<String, Vec<Edit>>,
    current: HashMap<String, Emitted>,
    /// Bundle mode: each file's text with its accepted edits applied, and
    /// where its trailing source map comment starts.
    current_text: HashMap<String, (String, Option<usize>)>,
}

/// Whether text near `edit` (in `text`) or its replacement can start or end
/// a static import, so the edit may change the import list.
fn may_change_imports(text: &str, edit: &Edit) -> bool {
    const NEAR: usize = 16;
    let mut from = edit.start.saturating_sub(NEAR);
    let mut to = (edit.end + NEAR).min(text.len());
    while !text.is_char_boundary(from) {
        from -= 1;
    }
    while !text.is_char_boundary(to) {
        to += 1;
    }
    let near = &text[from..to];
    ["import", "from"]
        .iter()
        .any(|keyword| near.contains(keyword) || edit.replacement.contains(keyword))
}

enum Decision {
    Accept { before: usize, after: usize },
    Reject(Reason),
}

impl<B: Backend> Gate<'_, B> {
    fn current(&mut self, file: &str) -> Result<Emitted> {
        if let Some(emitted) = self.current.get(file) {
            return Ok(emitted.clone());
        }
        let Some(text) = self.texts.get(file) else {
            bail!("transformation targets a module outside the analyzed program");
        };
        let text = apply(text, self.accepted.get(file).map_or(&[], Vec::as_slice))?;
        let emitted = self.backend.emit_module(file, &text)?;
        self.current.insert(file.to_owned(), emitted.clone());
        Ok(emitted)
    }

    /// Bundle mode: the file's text with its accepted edits applied, and the
    /// start of its trailing source map comment.
    fn current_text(&mut self, file: &str) -> Result<(&str, Option<usize>)> {
        if !self.current_text.contains_key(file) {
            let Some(text) = self.texts.get(file) else {
                bail!("transformation targets a module outside the analyzed program");
            };
            let accepted = self.accepted.get(file).map_or(&[][..], Vec::as_slice);
            let text = apply(text, accepted)?;
            let comment = crate::bundle::map_comment_start(&text);
            self.current_text.insert(file.to_owned(), (text, comment));
        }
        let (text, comment) = &self.current_text[file];
        Ok((text, *comment))
    }

    /// Evaluates the complete bundle, including module-initialization
    /// preservation. TypeScript elides an import whose bindings lose their
    /// last runtime use, which would skip or reorder that module's
    /// initialization. The gate keeps such a dependency with a bare
    /// `import "specifier";` at the original position, unless
    /// residual-dependency-removal is enabled and the analysis proved the
    /// module initialization pure; then the import may disappear and the
    /// bundle records that pass as cleanup.
    fn try_accept(&mut self, candidate: &mut Candidate) -> Result<Decision> {
        for attempt in 0..2 {
            let mut by_file = BTreeMap::<String, Vec<Edit>>::new();
            for edit in &candidate.edits {
                by_file
                    .entry(edit.file.clone())
                    .or_default()
                    .push(edit.edit.clone());
            }
            let mut before = 0;
            let mut after = 0;
            let mut merged_files = Vec::new();
            let mut insertions = Vec::new();
            let mut uses_residual = false;
            let bundle = crate::app::bundle_mode();
            for (file, mut edits) in by_file {
                edits.sort();
                for pair in edits.windows(2) {
                    ensure!(
                        !conflicts(&pair[0], &pair[1]),
                        "{} produced overlapping edits",
                        candidate.pass
                    );
                }
                let current = self.current(&file)?;
                before += current.size;
                let mut merged = self.accepted.get(&file).cloned().unwrap_or_default();
                merged.extend(edits.iter().cloned());
                merged.sort();
                // Bundle mode measures the chunk text itself: the size follows
                // from the edit lengths, and the import list changes only when
                // an edit touches import syntax, so no candidate copies the
                // chunk.
                let shifted: Vec<Edit> = if bundle {
                    let accepted = self.accepted.get(&file).map_or(&[][..], Vec::as_slice);
                    edits
                        .iter()
                        .map(|e| {
                            let start = forward_offset(e.start, accepted);
                            Edit {
                                start,
                                end: start + (e.end - e.start),
                                replacement: e.replacement.clone(),
                            }
                        })
                        .collect()
                } else {
                    Vec::new()
                };
                let emitted = if bundle {
                    let (text, comment) = self.current_text(&file)?;
                    let delta: isize = shifted
                        .iter()
                        .map(|e| e.replacement.len() as isize - (e.end - e.start) as isize)
                        .sum();
                    // An edit at or after the trailing source map comment can
                    // stop it from being trailing (an appended `export {}`),
                    // which the measured size then includes.
                    let at_comment =
                        comment.is_some_and(|start| shifted.iter().any(|e| e.end >= start));
                    if at_comment || shifted.iter().any(|e| may_change_imports(text, e)) {
                        let edited = apply(text, &shifted)?;
                        Emitted {
                            size: crate::bundle::chunk_code_len(&edited),
                            imports: bundle_static_imports(&edited),
                        }
                    } else {
                        Emitted {
                            size: (current.size as isize + delta) as usize,
                            imports: current.imports.clone(),
                        }
                    }
                } else {
                    let text = apply(&self.texts[&file], &merged)?;
                    self.backend.emit_module(&file, &text).map_err(|error| {
                        anyhow::anyhow!(
                            "{} produced output the compiler rejects in a module: {error}",
                            candidate.pass
                        )
                    })?
                };
                let Some(removed) = removed_imports(&current.imports, &emitted.imports) else {
                    return Ok(Decision::Reject(Reason::ModuleInitialization));
                };
                for specifier in removed {
                    let site = self.imports.get(&file).and_then(|sites| {
                        sites
                            .iter()
                            .find(|s| same_specifier(&s.specifier, &specifier))
                    });
                    let Some(site) = site else {
                        return Ok(Decision::Reject(Reason::ModuleInitialization));
                    };
                    if site.droppable && self.enabled.contains(PassId::ResidualDependencyRemoval) {
                        uses_residual = true;
                    } else if attempt == 0 {
                        insertions.push(FileEdit::insert(
                            &file,
                            site.offset,
                            format!("import {};\n", quote(&site.specifier)),
                        ));
                    } else {
                        return Ok(Decision::Reject(Reason::ModuleInitialization));
                    }
                }
                after += emitted.size;
                merged_files.push((file, merged, emitted));
            }
            if !insertions.is_empty() {
                let conflict =
                    insertions.iter().any(|insertion| {
                        candidate.edits.iter().any(|e| {
                            e.file == insertion.file && conflicts(&e.edit, &insertion.edit)
                        }) || self
                            .accepted
                            .get(&insertion.file)
                            .is_some_and(|a| a.iter().any(|e| conflicts(e, &insertion.edit)))
                    });
                if conflict {
                    return Ok(Decision::Reject(Reason::ModuleInitialization));
                }
                candidate.edits.extend(insertions);
                continue;
            }
            if after >= before {
                return Ok(Decision::Reject(Reason::NoSizeBenefit));
            }
            if uses_residual
                && candidate.pass != PassId::ResidualDependencyRemoval
                && !candidate.uses.contains(&PassId::ResidualDependencyRemoval)
            {
                candidate.uses.push(PassId::ResidualDependencyRemoval);
            }
            for (file, merged, emitted) in merged_files {
                if self.current_text.contains_key(&file) {
                    let text = apply(&self.texts[&file], &merged)?;
                    let comment = crate::bundle::map_comment_start(&text);
                    self.current_text.insert(file.clone(), (text, comment));
                }
                self.current.insert(file.clone(), emitted);
                self.accepted.insert(file, merged);
            }
            return Ok(Decision::Accept { before, after });
        }
        Ok(Decision::Reject(Reason::ModuleInitialization))
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
                let converged = current.accepted.is_empty();
                let Round {
                    plan,
                    accepted,
                    skips,
                    rejected,
                } = current;
                let applied = if converged {
                    Some(0)
                } else {
                    self.commit(
                        &mut outcome,
                        accepted,
                        phase,
                        round,
                        &mut blacklist,
                        &mut permanent,
                    )?
                };
                let Some(applied) = applied else {
                    // A bundle was rolled back; plan this round again.
                    continue;
                };
                if converged || phase == Phase::Property {
                    final_skips = skips;
                    if phase == Phase::Property {
                        outcome.mappings.extend(plan.mappings);
                        outcome.mappings.extend(rejected);
                        outcome.property_stats = plan.property_stats;
                    }
                }
                if converged {
                    break;
                }
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
            // Each skip maps to source coordinates exactly once, through the
            // chain of the text it was planned on: final-round skips here,
            // rollbacks when they happen.
            for mut skip in final_skips {
                skip.location.offset = source_offset(
                    skip.location.offset,
                    outcome
                        .chains
                        .get(&skip.location.file)
                        .map_or(&[], Vec::as_slice),
                );
                outcome.skips.push(skip);
            }
        }
        outcome.skips.extend(permanent);
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
            total += self.backend.emit_module(file, text)?.size;
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
        let imports = std::mem::take(&mut plan.imports);
        let mut gate = Gate {
            backend: &mut *self.backend,
            texts,
            imports: &imports,
            enabled: self.enabled,
            accepted: BTreeMap::new(),
            current: HashMap::new(),
            current_text: HashMap::new(),
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
            let mut candidate = candidate;
            let started = std::time::Instant::now();
            let decision = gate.try_accept(&mut candidate)?;
            let elapsed = started.elapsed();
            if elapsed.as_secs() >= 1 {
                tracing::debug!(
                    pass = %candidate.pass,
                    files = candidate.edits.len(),
                    elapsed_ms = elapsed.as_millis() as u64,
                    "slow size evaluation"
                );
            }
            match decision {
                Decision::Accept { before, after } => accepted.push((candidate, before, after)),
                Decision::Reject(reason) => {
                    skips.push(Skip {
                        pass: candidate.pass,
                        key: candidate.key.clone(),
                        location: candidate.location.clone(),
                        reason,
                    });
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
    ) -> Result<Option<usize>> {
        // Recheck the transformed program. On failure, find the first bundle
        // whose prefix fails and blacklist it; the caller plans again.
        let texts = Self::applied(&outcome.texts, &accepted)?;
        let started = std::time::Instant::now();
        let failing = self.backend.check(&texts)?;
        tracing::debug!(
            bundles = accepted.len(),
            failing = failing.len(),
            elapsed_ms = started.elapsed().as_millis() as u64,
            "transformed program rechecked"
        );
        if !failing.is_empty() {
            let culprit = self.first_failing(&outcome.texts, &accepted)?;
            let (candidate, _, _) = accepted.remove(culprit);
            tracing::warn!(
                pass = %candidate.pass,
                modules = failing.len(),
                "transformed program failed type checking; rolling back one bundle"
            );
            blacklist.insert(candidate.key.clone());
            // The skip refers to this round's text, so it maps through the
            // chain as it is now, exactly once.
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
            // Size and import-initialization decisions of the remaining
            // bundles were measured with the culprit applied. Discard them
            // and re-plan from the unchanged text with the culprit
            // blacklisted.
            return Ok(None);
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
        Ok(Some(accepted.len()))
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

/// Double-quoted JavaScript string literal.
fn quote(value: &str) -> String {
    serde_json::to_string(value).expect("strings serialize")
}

/// Whether an emitted import specifier is the source specifier, possibly
/// rewritten by `rewriteRelativeImportExtensions` (`./a.ts` emits as
/// `./a.js`).
fn same_specifier(source: &str, emitted: &str) -> bool {
    if source == emitted {
        return true;
    }
    if !source.starts_with('.') || source.ends_with(".d.ts") {
        return false;
    }
    [
        (".ts", ".js"),
        (".tsx", ".js"),
        (".mts", ".mjs"),
        (".cts", ".cjs"),
    ]
    .iter()
    .any(|(from, to)| {
        source
            .strip_suffix(from)
            .is_some_and(|stem| emitted.strip_suffix(to) == Some(stem))
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::plan::FileEdit;

    #[test]
    fn forward_offsets_skip_earlier_edits_only() {
        let edits = [
            Edit {
                start: 2,
                end: 4,
                replacement: "xyz".into(),
            },
            Edit {
                start: 10,
                end: 10,
                replacement: "ab".into(),
            },
        ];
        // Before every edit, after the first, and at the insertion point.
        assert_eq!(forward_offset(1, &edits), 1);
        assert_eq!(forward_offset(6, &edits), 7);
        assert_eq!(forward_offset(10, &edits), 11);
        assert_eq!(forward_offset(12, &edits), 15);
    }

    /// Test-only backend: candidates come from a fixed script, size is byte
    /// length and a module "fails" type checking when it contains `BAD`.
    type Script = Box<dyn FnMut(&BTreeMap<String, String>, Phase) -> Vec<Candidate>>;

    struct Fake {
        script: Script,
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

        fn emit_module(&mut self, _: &str, text: &str) -> Result<Emitted> {
            Ok(Emitted {
                size: text.len(),
                imports: static_imports(text),
            })
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
    fn rollback_replans_and_maps_its_location_once() {
        // Round 0 shifts the text; in round 1 the culprit is planned at a
        // shifted offset. Its skip must point at the original source, and
        // the surviving bundle must be re-gated in a fresh round.
        let plans = std::rc::Rc::new(std::cell::Cell::new(0));
        let counter = plans.clone();
        let mut fake = Fake {
            script: Box::new(move |texts, phase| {
                counter.set(counter.get() + 1);
                let text = &texts["a.ts"];
                if phase != Phase::Structural {
                    return Vec::new();
                }
                let mut out = Vec::new();
                if let Some(at) = text.find("prefix ") {
                    out.push(candidate(
                        PassId::EnumRuntimeReduction,
                        "prefix",
                        vec![FileEdit::delete("a.ts", at, at + 7)],
                    ));
                    return out;
                }
                if let Some(at) = text.find("badbad") {
                    out.push(candidate(
                        PassId::CallInlining,
                        "bad",
                        vec![FileEdit::replace("a.ts", at, at + 6, "BAD")],
                    ));
                }
                if let Some(at) = text.find("good ") {
                    out.push(candidate(
                        PassId::UnusedMethodRemoval,
                        "good",
                        vec![FileEdit::delete("a.ts", at, at + 5)],
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
        .run(&sources("prefix badbad good end"))
        .unwrap();
        assert_eq!(outcome.texts["a.ts"], "badbad end");
        let rollback = outcome
            .skips
            .iter()
            .find(|s| s.reason == Reason::RecheckFailed)
            .unwrap();
        // "badbad" starts at byte 7 of the original text.
        assert_eq!(rollback.location.offset, 7);
        // Structural: prefix round, failed round, re-planned round and
        // converged round; then the property phase.
        assert_eq!(plans.get(), 5);
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
    fn bundle_static_imports_scan_minified_lines() {
        let code = r#"import{a as b}from"./a.js";import"./side.js";import * as c from './c.js';export{d}from"./d.js";export*from"./e.js";const f=import("./lazy.js"),g=Array.from("xy"),h=x.from("no"),i=frommer;"#;
        assert_eq!(
            bundle_static_imports(code),
            ["./a.js", "./side.js", "./c.js", "./d.js", "./e.js"]
        );
        // ECMAScript whitespace beyond ASCII and line terminators.
        assert_eq!(
            bundle_static_imports(
                "f();\u{a0}import{u}from\u{a0}\"./a.js\";import\u{feff}'./b.js';import// \
                 c\u{2028}\"./c.js\""
            ),
            ["./a.js", "./b.js", "./c.js"]
        );
        // Comments between the keyword and the specifier.
        assert_eq!(
            bundle_static_imports(
                "import/* kept */\"./side.js\";export{x}from/* kept */'./dep.js';import// \
                 c\n\"./n.js\";"
            ),
            ["./side.js", "./dep.js", "./n.js"]
        );
        // Multi-byte characters before and between imports.
        assert_eq!(
            bundle_static_imports(r#"/*ü*/import"./ä.js";const s="한글→";import{x}from"./b.js";"#),
            ["./ä.js", "./b.js"]
        );
    }

    #[test]
    fn static_imports_follow_statement_order() {
        let code = "import { a } from \"./a\";\nimport \"./side\";\nimport {\n  b,\n  c\n} from \
                    './bc';\nexport * from \"./star\";\nexport { d };\nconst s = \"import x from \
                    'no'\";\n";
        assert_eq!(static_imports(code), ["./a", "./side", "./bc", "./star"]);
        // A `;` inside a specifier does not end the statement.
        assert_eq!(
            static_imports("import { x } from \"./side;effect\";\nimport \"./a;b\";\n"),
            ["./side;effect", "./a;b"]
        );
        assert_eq!(
            removed_imports(
                &["a".into(), "b".into(), "c".into()],
                &["a".into(), "c".into()]
            ),
            Some(vec!["b".to_owned()])
        );
        assert_eq!(removed_imports(&["a".into()], &["b".into()]), None);
    }

    #[test]
    fn elided_imports_keep_module_initialization() {
        // Removing the only use of `x` would drop `import {x} from "m"`; the
        // gate keeps the dependency with a bare import.
        let source = "import {x} from \"m\";\nuse(x);\nlongDeadCode;\n";
        let mut fake = Fake {
            script: Box::new(|texts, phase| {
                let text = &texts["a.ts"];
                if phase != Phase::Structural || !text.contains("use(x);") {
                    return Vec::new();
                }
                let at = text.find("use(x);").unwrap();
                let dead = text.find("longDeadCode;").unwrap();
                vec![candidate(
                    PassId::CallInlining,
                    "drop-use",
                    vec![
                        FileEdit::delete("a.ts", at, at + 7),
                        FileEdit::delete("a.ts", dead, dead + 13),
                    ],
                )]
            }),
            checks: 0,
        };
        struct Elide<'a>(&'a mut Fake, bool);
        impl Backend for Elide<'_> {
            fn plan(
                &mut self,
                texts: &BTreeMap<String, String>,
                phase: Phase,
                enabled: &PassSet,
            ) -> Result<Plan> {
                let mut plan = self.0.plan(texts, phase, enabled)?;
                plan.imports.insert(
                    "a.ts".into(),
                    vec![ImportSite {
                        specifier: "m".into(),
                        offset: 0,
                        droppable: self.1,
                    }],
                );
                Ok(plan)
            }

            fn check(&mut self, texts: &BTreeMap<String, String>) -> Result<Vec<String>> {
                self.0.check(texts)
            }

            fn emit_module(&mut self, _: &str, text: &str) -> Result<Emitted> {
                // Mimic TypeScript import elision.
                let code = if text.contains("use(x)") {
                    text.to_owned()
                } else {
                    text.replace("import {x} from \"m\";\n", "")
                };
                Ok(Emitted {
                    size: code.len(),
                    imports: static_imports(&code),
                })
            }
        }
        let enabled = PassSet::all();
        let outcome = Pipeline {
            backend: &mut Elide(&mut fake, false),
            enabled: &enabled,
        }
        .run(&sources(source))
        .unwrap();
        assert!(outcome.texts["a.ts"].starts_with("import \"m\";\nimport {x}"));
        assert!(outcome.bundles[0].uses.is_empty());
        let outcome = Pipeline {
            backend: &mut Elide(&mut fake, true),
            enabled: &enabled,
        }
        .run(&sources(source))
        .unwrap();
        assert!(!outcome.texts["a.ts"].contains("import \"m\""));
        assert_eq!(outcome.bundles[0].uses, [PassId::ResidualDependencyRemoval]);
        let without = PassSet::without(&[PassId::ResidualDependencyRemoval]);
        let outcome = Pipeline {
            backend: &mut Elide(&mut fake, true),
            enabled: &without,
        }
        .run(&sources(source))
        .unwrap();
        assert!(outcome.texts["a.ts"].starts_with("import \"m\";"));
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
