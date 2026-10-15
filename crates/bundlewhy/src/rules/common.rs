//! Rules shared by both engines: duplicate packages and minifier blockers.

use std::{
    collections::{BTreeMap, BTreeSet},
    path::Path,
};

use regex::Regex;

use super::{Context, DiagnosticDraft, RuleId};
use crate::{
    collect::{CollectedModule, CompressionSetting, ManglingSetting, MinifyOverride},
    model::{Condition, ConditionOutcome, Evidence, EvidenceStatus},
};

mod eval_sites;

pub fn run(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    duplicate_packages(context, drafts);
    if context.is_cancelled() {
        return;
    }
    // Without a minifier, retained `process.env` reads are expected; the
    // confirmed minification-disabled finding explains them instead.
    // Without the compress stage no dead code is removed either, so retained
    // reads are expected then too.
    if context.collection.minify_enabled
        && context.collection.compress == CompressionSetting::Enabled
    {
        unresolved_env_conditions(context, drafts);
    }
    minification_disabled(context, drafts);
    direct_eval_mangling(context, drafts);
}

fn duplicate_packages(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let collection = context.collection;
    // package name -> package directory -> included modules
    let mut packages: BTreeMap<&str, BTreeMap<&str, Vec<&CollectedModule>>> = BTreeMap::new();
    for module in &collection.modules {
        if !module.included {
            continue;
        }
        if let (Some(name), Some(dir)) = (
            module.package_name.as_deref(),
            module.package_dir.as_deref(),
        ) {
            packages
                .entry(name)
                .or_default()
                .entry(dir)
                .or_default()
                .push(module);
        }
    }
    for (name, copies) in packages {
        if copies.len() < 2 {
            continue;
        }
        let descriptions: Vec<String> = copies
            .iter()
            .map(|(dir, modules)| {
                let version = modules[0].package_version.as_deref().unwrap_or("unknown");
                let display = context.display_path(Path::new(dir));
                format!(
                    "{version} at {} ({} module(s))",
                    if display.is_empty() {
                        ".".to_owned()
                    } else {
                        display
                    },
                    modules.len()
                )
            })
            .collect();
        let first_modules: Vec<&CollectedModule> =
            copies.values().map(|modules| modules[0]).collect();
        let second = first_modules[1];
        let located = context.module_location(second, 0, 0);
        let mut dependency_paths = Vec::new();
        for module in &first_modules {
            dependency_paths.extend(context.dependency_paths(&module.id).into_iter().take(1));
        }
        let display = context.module_display(second);
        drafts.push(DiagnosticDraft {
            rule: RuleId::DuplicatePackage,
            status: EvidenceStatus::Confirmed,
            title: format!("{name} is bundled {} times", copies.len()),
            summary: format!(
                "Modules from {} different copies of {name} are included in the build. Each copy \
                 is shipped and evaluated separately.",
                copies.len()
            ),
            conditions: vec![Condition::new(
                "One copy of the package is bundled",
                ConditionOutcome::Unmet,
                descriptions.join("; "),
            )],
            located,
            module: display,
            dependency_paths,
            evidence: descriptions
                .iter()
                .map(|description| {
                    Evidence::new(
                        "module paths and nearest package.json",
                        EvidenceStatus::Confirmed,
                        description.clone(),
                    )
                })
                .collect(),
            direction: format!(
                "Align the version ranges that depend on {name} and deduplicate the lockfile (for \
                 example pnpm dedupe, npm dedupe, or overrides/resolutions), or alias the package \
                 to a single copy."
            ),
            safety: "Only merge copies whose versions are API compatible; run the application's \
                     tests after deduplicating."
                .to_owned(),
            dependency: true,
            export_usage: None,
        });
    }
}

/// True when `text` reads `process.env.NAME` or `process.env["NAME"]`.
fn reads_process_env(text: &str, name: &str) -> bool {
    let name = regex::escape(name);
    Regex::new(&format!(
        r#"process\.env(?:\.{name}\b|\[\s*"{name}"\s*\]|\[\s*'{name}'\s*\])"#
    ))
    .is_ok_and(|pattern| pattern.is_match(text))
}

/// Byte ranges of string literals, template literal text, comments and regular
/// expression literals in JavaScript. Text there is never a property read.
///
/// This is a lexical scan, not a parser. Regular expression literals are
/// recognized by the preceding token (an operator, punctuation or keyword such
/// as `return`), which matches minified bundler output.
fn non_code_ranges(code: &str) -> Vec<(usize, usize)> {
    let bytes = code.as_bytes();
    let mut ranges = Vec::new();
    // Open `${` substitutions; each entry is the brace depth inside it.
    let mut templates: Vec<usize> = Vec::new();
    let mut previous_significant: Option<u8> = None;
    let mut index = 0usize;
    let regex_allowed = |previous: Option<u8>, start: usize| -> bool {
        match previous {
            None => true,
            // `</tag>` is a JSX closing tag, and `<a b={c} />` or
            // `<A<B> />` a self-closing one. A slash after a `<` is a closing
            // tag only when a tag name and `>` follow it; otherwise it starts
            // a regular expression (`value < /eval(code)/.test(text)`). No
            // regular expression starts with `>` right after a `}` or a `>`.
            Some(b'<') if closes_jsx_tag(bytes, start) => false,
            Some(b'}' | b'>') if bytes.get(start + 1) == Some(&b'>') => false,
            Some(byte) if b"(,=:[!&|?{};+-*%<>~^".contains(&byte) => true,
            Some(byte) if byte.is_ascii_alphabetic() => {
                let head = &code[..start];
                let trimmed = head.trim_end();
                [
                    "return", "typeof", "case", "do", "else", "in", "of", "void", "yield",
                    "delete", "throw", "new",
                ]
                .iter()
                .any(|keyword| {
                    trimmed.ends_with(keyword)
                        && !trimmed[..trimmed.len() - keyword.len()].ends_with(|character: char| {
                            character.is_alphanumeric() || character == '_' || character == '$'
                        })
                })
            }
            _ => false,
        }
    };
    // Scans a quoted string or template text starting after the opener.
    let scan_until = |mut position: usize, terminator: u8, template: bool| -> (usize, bool) {
        while position < bytes.len() {
            match bytes[position] {
                b'\\' => position += 2,
                byte if byte == terminator => return (position + 1, false),
                b'$' if template && bytes.get(position + 1) == Some(&b'{') => {
                    return (position + 2, true);
                }
                b'\n' if !template => return (position, false),
                _ => position += 1,
            }
        }
        (bytes.len(), false)
    };
    while index < bytes.len() {
        let byte = bytes[index];
        match byte {
            b'\'' | b'"' => {
                let (end, _) = scan_until(index + 1, byte, false);
                ranges.push((index, end));
                index = end;
                previous_significant = Some(b'"');
            }
            b'`' => {
                let (end, opened) = scan_until(index + 1, b'`', true);
                ranges.push((index, end));
                if opened {
                    templates.push(0);
                }
                index = end;
                previous_significant = Some(b'"');
            }
            b'}' if templates.last() == Some(&0) => {
                templates.pop();
                let (end, opened) = scan_until(index + 1, b'`', true);
                ranges.push((index, end));
                if opened {
                    templates.push(0);
                }
                index = end;
                previous_significant = Some(b'"');
            }
            b'/' if bytes.get(index + 1) == Some(&b'/') => {
                let end = code[index..]
                    .find('\n')
                    .map_or(bytes.len(), |offset| index + offset);
                ranges.push((index, end));
                index = end;
            }
            b'/' if bytes.get(index + 1) == Some(&b'*') => {
                let end = code[index + 2..]
                    .find("*/")
                    .map_or(bytes.len(), |offset| index + 2 + offset + 2);
                ranges.push((index, end));
                index = end;
            }
            b'/' if regex_allowed(previous_significant, index) => {
                let mut position = index + 1;
                let mut in_class = false;
                while position < bytes.len() {
                    match bytes[position] {
                        b'\\' => position += 1,
                        b'[' => in_class = true,
                        b']' => in_class = false,
                        b'/' if !in_class => break,
                        b'\n' => break,
                        _ => {}
                    }
                    position += 1;
                }
                let end = (position + 1).min(bytes.len());
                ranges.push((index, end));
                index = end;
                previous_significant = Some(b'"');
            }
            _ => {
                if let Some(depth) = templates.last_mut() {
                    if byte == b'{' {
                        *depth += 1;
                    } else if byte == b'}' {
                        *depth -= 1;
                    }
                }
                if !byte.is_ascii_whitespace() {
                    previous_significant = Some(byte);
                }
                index += 1;
            }
        }
    }
    ranges
}

/// True when the `/` at `slash` is followed by an optional JSX tag name and
/// `>`, as in `</div>`, `</Foo.Bar>`, `</ns:tag >` and the fragment `</>`.
fn closes_jsx_tag(bytes: &[u8], slash: usize) -> bool {
    let mut cursor = slash + 1;
    while bytes
        .get(cursor)
        .is_some_and(|byte| byte.is_ascii_alphanumeric() || b"_$.:-".contains(byte))
    {
        cursor += 1;
    }
    while bytes.get(cursor).is_some_and(u8::is_ascii_whitespace) {
        cursor += 1;
    }
    bytes.get(cursor) == Some(&b'>')
}

fn in_ranges(ranges: &[(usize, usize)], offset: usize) -> bool {
    let index = ranges.partition_point(|(start, _)| *start <= offset);
    index > 0 && offset < ranges[index - 1].1
}

fn next_code_byte(bytes: &[u8], start: usize, ignored: &[(usize, usize)]) -> Option<u8> {
    let mut index = start;
    while index < bytes.len() {
        if in_ranges(ignored, index) || bytes[index].is_ascii_whitespace() {
            index += 1;
        } else {
            return Some(bytes[index]);
        }
    }
    None
}

fn asi_terminates(bytes: &[u8], index: usize, ignored: &[(usize, usize)]) -> bool {
    let Some(next) = next_code_byte(bytes, index + 1, ignored) else {
        return true;
    };
    match next {
        b'+' => next_code_byte(bytes, index + 2, ignored) != Some(b'+'),
        b'-' => next_code_byte(bytes, index + 2, ignored) != Some(b'-'),
        b'!' | b'%' | b'&' | b'(' | b')' | b'*' | b',' | b'.' | b'/' | b':' | b';' | b'<'
        | b'=' | b'>' | b'?' | b'[' | b']' | b'^' | b'{' | b'}' | b'|' | b'~' => false,
        _ => true,
    }
}

#[derive(Clone, Copy)]
enum EnvGuard {
    ShortCircuitAnd,
    ShortCircuitOr,
    NullishCoalescing,
    Conditional,
    If,
    While,
    Switch,
    For,
}

impl EnvGuard {
    fn evidence(self) -> &'static str {
        match self {
            Self::ShortCircuitAnd => "the retained read controls a && short-circuit branch",
            Self::ShortCircuitOr => "the retained read controls a || short-circuit branch",
            Self::NullishCoalescing => "the retained read controls a ?? fallback branch",
            Self::Conditional => "the retained read controls a conditional (?:) branch",
            Self::If => "the retained read is in an if condition",
            Self::While => "the retained read is in a while condition",
            Self::Switch => "the retained read is in a switch condition",
            Self::For => "the retained read is in a for condition",
        }
    }
}

/// Recognize only a generated branch whose test contains this read. A plain
/// value read in a call, assignment or another data expression is not a DCE
/// blocker.
#[cfg(test)]
fn env_guard(code: &str, start: usize, end: usize, ignored: &[(usize, usize)]) -> Option<EnvGuard> {
    env_guards(code, &[(start, end)], ignored)
        .into_iter()
        .next()
        .flatten()
}

#[derive(Clone, Copy)]
enum ScopeContext {
    Neutral,
    Invalid,
    Guard(EnvGuard),
    For(ForClause),
}

#[derive(Clone, Copy)]
enum ForClause {
    Initializer,
    Test,
    Update,
}

struct ScanScope {
    parent: usize,
    context: ScopeContext,
    conditional_questions: usize,
    pending_reads: Vec<usize>,
    conditional_pending: Vec<usize>,
    arrow_expression: bool,
}

fn env_guards(
    code: &str,
    reads: &[(usize, usize)],
    ignored: &[(usize, usize)],
) -> Vec<Option<EnvGuard>> {
    if reads.is_empty() {
        return Vec::new();
    }
    let bytes = code.as_bytes();
    let mut guards = vec![None; reads.len()];
    let mut blocked = vec![false; reads.len()];
    let mut contexts = vec![ScopeContext::Neutral; reads.len()];
    let mut scopes = vec![ScanScope {
        parent: 0,
        context: ScopeContext::Neutral,
        conditional_questions: 0,
        pending_reads: Vec::new(),
        conditional_pending: Vec::new(),
        arrow_expression: false,
    }];
    let mut current_scope = 0;
    let mut next_start = 0;
    let mut next_end = 0;
    let mut ignored_index = 0;

    for index in 0..bytes.len() {
        while next_start < reads.len() && reads[next_start].0 == index {
            contexts[next_start] = scopes[current_scope].context;
            next_start += 1;
        }
        while next_end < reads.len() && reads[next_end].1 <= index {
            scopes[current_scope].pending_reads.push(next_end);
            next_end += 1;
        }
        while ignored_index < ignored.len() && ignored[ignored_index].1 <= index {
            ignored_index += 1;
        }
        if ignored_index < ignored.len()
            && ignored[ignored_index].0 <= index
            && index < ignored[ignored_index].1
        {
            continue;
        }

        if scopes[current_scope].arrow_expression
            && matches!(bytes[index], b',' | b';' | b')' | b']' | b'}')
        {
            let parent = scopes[current_scope].parent;
            let pending_reads = std::mem::take(&mut scopes[current_scope].pending_reads);
            let conditional_pending =
                std::mem::take(&mut scopes[current_scope].conditional_pending);
            // An expression-bodied arrow evaluates to a function object at
            // the enclosing expression. Do not let its unguarded reads flow
            // into an enclosing branch operator. Guards found inside the
            // arrow body remain recorded on their own reads.
            drop(pending_reads);
            drop(conditional_pending);
            current_scope = parent;
        }

        if bytes[index] == b'\n' && asi_terminates(bytes, index, ignored) {
            for read in scopes[current_scope].pending_reads.drain(..) {
                if guards[read].is_none() {
                    blocked[read] = true;
                }
            }
            for read in scopes[current_scope].conditional_pending.drain(..) {
                if guards[read].is_none() {
                    blocked[read] = true;
                }
            }
        }

        match bytes[index] {
            b'(' | b'[' | b'{' => {
                let context = scope_context(
                    code,
                    index,
                    bytes[index],
                    scopes[current_scope].context,
                    ignored,
                );
                scopes.push(ScanScope {
                    parent: current_scope,
                    context,
                    conditional_questions: 0,
                    pending_reads: Vec::new(),
                    conditional_pending: Vec::new(),
                    arrow_expression: false,
                });
                current_scope = scopes.len() - 1;
            }
            b')' | b']' | b'}' if current_scope != 0 => {
                let parent = scopes[current_scope].parent;
                let pending_reads = std::mem::take(&mut scopes[current_scope].pending_reads);
                let conditional_pending =
                    std::mem::take(&mut scopes[current_scope].conditional_pending);
                scopes[parent].pending_reads.extend(pending_reads);
                scopes[parent].pending_reads.extend(conditional_pending);
                scopes[parent].pending_reads.sort_unstable();
                current_scope = parent;
            }
            b';' => {
                let context = scopes[current_scope].context;
                match context {
                    ScopeContext::For(ForClause::Test) => {
                        for read in scopes[current_scope].pending_reads.drain(..) {
                            if guards[read].is_none() {
                                guards[read] = Some(EnvGuard::For);
                            }
                        }
                    }
                    _ => {
                        for read in scopes[current_scope].pending_reads.drain(..) {
                            if guards[read].is_none() {
                                blocked[read] = true;
                            }
                        }
                    }
                }
                for read in scopes[current_scope].conditional_pending.drain(..) {
                    if matches!(context, ScopeContext::For(ForClause::Test)) {
                        if guards[read].is_none() {
                            guards[read] = Some(EnvGuard::For);
                        }
                    } else if guards[read].is_none() {
                        blocked[read] = true;
                    }
                }
                if let ScopeContext::For(clause) = context {
                    scopes[current_scope].context = ScopeContext::For(match clause {
                        ForClause::Initializer => ForClause::Test,
                        ForClause::Test | ForClause::Update => ForClause::Update,
                    });
                }
            }
            b',' => {
                for read in scopes[current_scope].pending_reads.drain(..) {
                    if guards[read].is_none() {
                        blocked[read] = true;
                    }
                }
                for read in scopes[current_scope].conditional_pending.drain(..) {
                    if guards[read].is_none() {
                        blocked[read] = true;
                    }
                }
            }
            b':' => {
                if scopes[current_scope].conditional_questions > 0 {
                    let pending_reads = std::mem::take(&mut scopes[current_scope].pending_reads);
                    scopes[current_scope]
                        .conditional_pending
                        .extend(pending_reads);
                    scopes[current_scope].conditional_questions -= 1;
                } else {
                    for read in scopes[current_scope].pending_reads.drain(..) {
                        if guards[read].is_none() {
                            blocked[read] = true;
                        }
                    }
                    for read in scopes[current_scope].conditional_pending.drain(..) {
                        if guards[read].is_none() {
                            blocked[read] = true;
                        }
                    }
                }
            }
            b'=' if bytes.get(index + 1) == Some(&b'>') => {
                if next_code_byte(bytes, index + 2, ignored) != Some(b'{') {
                    scopes.push(ScanScope {
                        parent: current_scope,
                        context: ScopeContext::Neutral,
                        conditional_questions: 0,
                        pending_reads: Vec::new(),
                        conditional_pending: Vec::new(),
                        arrow_expression: true,
                    });
                    current_scope = scopes.len() - 1;
                }
            }
            b'=' if bytes.get(index + 1) != Some(&b'=')
                && bytes.get(index.wrapping_sub(1)) != Some(&b'=')
                && bytes.get(index.wrapping_sub(1)) != Some(&b'!')
                && bytes.get(index.wrapping_sub(1)) != Some(&b'<')
                && bytes.get(index.wrapping_sub(1)) != Some(&b'>') =>
            {
                for read in scopes[current_scope].pending_reads.drain(..) {
                    if guards[read].is_none() {
                        blocked[read] = true;
                    }
                }
                for read in scopes[current_scope].conditional_pending.drain(..) {
                    if guards[read].is_none() {
                        blocked[read] = true;
                    }
                }
            }
            b'&' if bytes.get(index + 1) == Some(&b'&') => {
                for &read in &scopes[current_scope].pending_reads {
                    if guards[read].is_none() {
                        guards[read] = Some(EnvGuard::ShortCircuitAnd);
                    }
                }
            }
            b'|' if bytes.get(index + 1) == Some(&b'|') => {
                for &read in &scopes[current_scope].pending_reads {
                    if guards[read].is_none() {
                        guards[read] = Some(EnvGuard::ShortCircuitOr);
                    }
                }
            }
            b'?' if bytes.get(index + 1) == Some(&b'?') => {
                for &read in &scopes[current_scope].pending_reads {
                    if guards[read].is_none() {
                        guards[read] = Some(EnvGuard::NullishCoalescing);
                    }
                }
            }
            b'?' if bytes.get(index + 1) != Some(&b'.')
                && bytes.get(index.wrapping_sub(1)) != Some(&b'?') =>
            {
                for &read in &scopes[current_scope].pending_reads {
                    if guards[read].is_none() {
                        guards[read] = Some(EnvGuard::Conditional);
                    }
                }
                scopes[current_scope].conditional_questions += 1;
            }
            _ => {}
        }
    }

    while scopes[current_scope].arrow_expression {
        let parent = scopes[current_scope].parent;
        current_scope = parent;
    }

    guards
        .into_iter()
        .zip(contexts.into_iter().zip(blocked))
        .map(|(guard, (context, blocked))| match context {
            // An invalid enclosing expression blocks inherited branch
            // evidence, but an operator found inside that expression still
            // directly controls its own branch.
            ScopeContext::Invalid => guard,
            ScopeContext::Neutral => guard,
            ScopeContext::Guard(fallback) if !blocked => guard.or(Some(fallback)),
            ScopeContext::Guard(_) => guard,
            ScopeContext::For(ForClause::Test) if !blocked => guard.or(Some(EnvGuard::For)),
            ScopeContext::For(_) => guard,
        })
        .collect()
}

fn scope_prefix<'a>(code: &'a str, index: usize, ignored: &[(usize, usize)]) -> &'a str {
    let mut end = code[..index].trim_end().len();
    loop {
        let Some(comment_index) = ignored
            .partition_point(|(_, stop)| *stop <= end)
            .checked_sub(1)
        else {
            return &code[..end];
        };
        let (start, stop) = ignored[comment_index];
        if stop != end || !code[start..stop].starts_with("/*") {
            return &code[..end];
        }
        end = code[..start].trim_end().len();
    }
}

fn scope_context(
    code: &str,
    index: usize,
    opener: u8,
    parent: ScopeContext,
    ignored: &[(usize, usize)],
) -> ScopeContext {
    let prefix = scope_prefix(code, index, ignored);
    if opener == b'[' {
        // A bracket after an expression is a computed member access and keeps
        // the surrounding condition. A bracket after an operator starts an
        // array literal, whose contents do not form a guard by themselves.
        if prefix.as_bytes().last().is_some_and(|byte| {
            byte.is_ascii_alphanumeric()
                || matches!(
                    *byte,
                    b'_' | b'$' | b']' | b')' | b'}' | b'\'' | b'"' | b'`' | b'.'
                )
        }) {
            return parent;
        }
        return ScopeContext::Invalid;
    }
    if opener != b'(' {
        return ScopeContext::Invalid;
    }
    if prefix.ends_with("for")
        && prefix
            .as_bytes()
            .get(..prefix.len() - "for".len())
            .and_then(|prefix| prefix.last())
            .is_none_or(|byte| {
                !byte.is_ascii_alphanumeric() && *byte != b'_' && *byte != b'$' && *byte != b'.'
            })
    {
        return ScopeContext::For(ForClause::Initializer);
    }
    for (keyword, guard) in [
        ("if", EnvGuard::If),
        ("while", EnvGuard::While),
        ("switch", EnvGuard::Switch),
    ] {
        if prefix.ends_with(keyword)
            && prefix
                .as_bytes()
                .get(..prefix.len() - keyword.len())
                .and_then(|prefix| prefix.last())
                .is_none_or(|byte| {
                    !byte.is_ascii_alphanumeric() && *byte != b'_' && *byte != b'$' && *byte != b'.'
                })
        {
            return ScopeContext::Guard(guard);
        }
    }
    if prefix.as_bytes().last().is_some_and(|byte| {
        byte.is_ascii_alphanumeric() || matches!(*byte, b'_' | b'$' | b']' | b')' | b'.')
    }) {
        ScopeContext::Invalid
    } else {
        parent
    }
}

fn unresolved_env_conditions(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let vite = context.collection.bundler == "vite";
    // Vite replaces `process.env` with `{}` in client builds; Rspack leaves
    // unknown `process.env.X` reads untouched.
    // Dotted reads and static bracket-string reads (`["NAME"]`, `['NAME']`).
    let member =
        r#"(?:\.([A-Za-z_$][\w$]*)|\[\s*"([A-Za-z_$][\w$]*)"\s*\]|\[\s*'([A-Za-z_$][\w$]*)'\s*\])"#;
    let pattern = if vite {
        Regex::new(&format!(r"\(\{{\}}\){member}"))
    } else {
        Regex::new(&format!(r"\bprocess\.env{member}"))
    }
    .expect("static regex");
    let mut seen: BTreeSet<(String, String)> = BTreeSet::new();
    for index in 0..context.chunks.len() {
        if context.is_cancelled() {
            return;
        }
        let ignored = non_code_ranges(&context.chunks[index].code);
        let matches: Vec<(usize, usize, usize, String)> = pattern
            .captures_iter(&context.chunks[index].code)
            .filter_map(|captures| {
                let whole = captures.get(0)?;
                // Text inside strings, templates, comments or regular
                // expressions is not a runtime read.
                if in_ranges(&ignored, whole.start()) {
                    return None;
                }
                // Vite's `(` belongs to the preceding mapping segment; the
                // `{}` replacement maps to the original `process.env` read.
                let offset = if vite {
                    whole.start() + 1
                } else {
                    whole.start()
                };
                let name = captures
                    .get(1)
                    .or_else(|| captures.get(2))
                    .or_else(|| captures.get(3))?;
                Some((whole.start(), whole.end(), offset, name.as_str().to_owned()))
            })
            .collect();
        let reads: Vec<(usize, usize)> = matches
            .iter()
            .map(|(start, end, _, _)| (*start, *end))
            .collect();
        let guards = env_guards(&context.chunks[index].code, &reads, &ignored);
        for ((_start, _end, offset, name), guard) in matches.into_iter().zip(guards) {
            let Some(guard) = guard else {
                continue;
            };
            let mapped_source = if vite {
                let chunk = &context.chunks[index];
                let (line, column) = chunk.position(offset);
                chunk
                    .original(line, column)
                    .map(|(path, _, _)| context.display_path(&path))
            } else {
                None
            };
            let located = context.chunk_location(index, offset);
            let mapped = located.generated.is_some();
            if vite
                && !mapped
                && let Some(source) = mapped_source
            {
                context.vite_env_source_gaps.insert(source);
            }
            tracing::debug!(
                chunk = %context.chunks[index].file,
                offset,
                name = %name,
                mapped,
                location = ?located.location.as_ref().map(|location| (&location.path, location.line)),
                "unresolved env candidate"
            );
            if vite {
                // `({}).X` is only attributable to process.env when the
                // original source at the mapped line reads process.env.X.
                let Some(snippet) = located.snippet.as_ref() else {
                    continue;
                };
                if !mapped || !reads_process_env(&snippet.text, &name) {
                    continue;
                }
            }
            let path = located
                .location
                .as_ref()
                .map(|location| location.path.clone())
                .unwrap_or_default();
            if !seen.insert((path.clone(), name.clone())) {
                continue;
            }
            let chunk_file = context.chunks[index].file.clone();
            let dependency = path_is_dependency(&path);
            let generated_text = if vite {
                format!("({{}}).{name}")
            } else {
                format!("process.env.{name}")
            };
            drafts.push(DiagnosticDraft {
                rule: RuleId::UnresolvedEnvCondition,
                status: EvidenceStatus::Inferred,
                title: format!("process.env.{name} is not a compile-time constant"),
                summary: format!(
                    "The generated code retains a branch guarded by {generated_text}. Because \
                     this value is read at runtime, bundlewhy infers that the minifier could not \
                     resolve the branch at build time."
                ),
                conditions: vec![
                    Condition::new(
                        &format!("process.env.{name} is replaced with a constant at build time"),
                        ConditionOutcome::Unmet,
                        format!("{generated_text} remains in {chunk_file}"),
                    ),
                    Condition::new(
                        "The retained environment read guards a branch",
                        ConditionOutcome::Met,
                        guard.evidence(),
                    ),
                ],
                located,
                module: path,
                dependency_paths: Vec::new(),
                evidence: vec![
                    Evidence::new(
                        "generated chunk text",
                        EvidenceStatus::Confirmed,
                        format!("{generated_text} found in {chunk_file}"),
                    ),
                    Evidence::new(
                        "bundlewhy analysis",
                        EvidenceStatus::Inferred,
                        if mapped {
                            format!(
                                "{}; mapped to the original source through the chunk source map. \
                                 The engine does not report why the branch was kept.",
                                guard.evidence()
                            )
                        } else {
                            format!(
                                "{}; no source mapping is available for this position. The engine \
                                 does not report why the branch was kept.",
                                guard.evidence()
                            )
                        },
                    ),
                ],
                direction: if vite {
                    format!(
                        "Provide the value at build time with Vite `define` (for example define: \
                         {{ 'process.env.{name}': JSON.stringify(value) }}) or read an \
                         import.meta.env variable instead."
                    )
                } else {
                    format!(
                        "Provide the value at build time with DefinePlugin (for example new \
                         rspack.DefinePlugin({{ 'process.env.{name}': JSON.stringify(value) }}))."
                    )
                },
                safety: "Only define values that are constant for the deployed build. Defined \
                         values are inlined into the client bundle, so never define secrets."
                    .to_owned(),
                dependency,
                export_usage: None,
            });
        }
    }
}

fn path_is_dependency(path: &str) -> bool {
    path.starts_with("node_modules/") || path.contains("/node_modules/") || path.starts_with("../")
}

/// Text of a configuration-disabled finding.
pub struct DisabledOptimization {
    pub rule: RuleId,
    pub title: &'static str,
    pub option: &'static str,
    pub summary: &'static str,
    pub direction: &'static str,
    pub safety: &'static str,
}

/// Confirmed finding located at the configuration file for an optimization
/// that the resolved configuration turned off.
pub fn configuration_disabled(
    context: &mut Context<'_>,
    drafts: &mut Vec<DiagnosticDraft>,
    finding: &DisabledOptimization,
) {
    let config_file = context.collection.config_file.clone();
    let located = match &config_file {
        Some(file) => context.original_location_without_coverage_gap(Path::new(file), 0, 0),
        None => super::Located {
            location: None,
            generated: None,
            snippet: None,
        },
    };
    let module = config_file
        .as_deref()
        .map(|file| context.display_path(Path::new(file)))
        .unwrap_or_default();
    drafts.push(DiagnosticDraft {
        rule: finding.rule,
        status: EvidenceStatus::Confirmed,
        title: finding.title.to_owned(),
        summary: finding.summary.to_owned(),
        conditions: vec![Condition::new(
            &format!("{} is enabled", finding.option),
            ConditionOutcome::Unmet,
            format!("{}: false", finding.option),
        )],
        located,
        module,
        dependency_paths: Vec::new(),
        evidence: vec![Evidence::new(
            "resolved bundler configuration",
            EvidenceStatus::Confirmed,
            format!("{}: false", finding.option),
        )],
        direction: finding.direction.to_owned(),
        safety: finding.safety.to_owned(),
        dependency: false,
        export_usage: None,
    });
}

fn minification_disabled(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    if context.collection.minify_enabled {
        return;
    }
    let vite = context.collection.bundler == "vite";
    let override_kind = context.collection.minify_override;
    // An output option, or a plugin output hook, that turned the selected
    // minifier off is named as the cause: `build.minify` still reads as
    // enabled in that case.
    let (option, value) = match override_kind {
        MinifyOverride::None => {
            let option = if vite {
                "build.minify"
            } else {
                "optimization.minimize"
            };
            let value = context
                .collection
                .config_summary
                .get(option)
                .cloned()
                .unwrap_or_else(|| "false".to_owned());
            (option, value)
        }
        _ => {
            let option = if override_kind.by_hook() {
                FINAL_OUTPUT_MINIFY_OPTION
            } else {
                OUTPUT_MINIFY_OPTION
            };
            let value = if override_kind.dce_only() {
                "\"dce-only\"".to_owned()
            } else {
                "false".to_owned()
            };
            (option, value)
        }
    };
    let (summary, direction, evidence_label) = if override_kind == MinifyOverride::None {
        (
            format!(
                "{option} is {value}, so dead-code elimination, name mangling and other minifier \
                 optimizations do not run."
            ),
            format!("Enable {option} for production builds."),
            "resolved bundler configuration",
        )
    } else {
        let cause = if override_kind.by_hook() {
            format!("A plugin outputOptions hook set the final output minify option to {value}")
        } else {
            format!("{OUTPUT_MINIFY_OPTION} is {value}")
        };
        let effect = if override_kind.dce_only() {
            "compression and name mangling do not run. Rolldown still removes dead code in this \
             mode; bundlewhy has no verified detector for that stage."
        } else {
            "dead-code elimination, name mangling and other minifier optimizations do not run."
        };
        let direction = override_kind.direction().unwrap_or_default();
        (
            format!(
                "{cause} although build.minify is {}, so {effect}",
                build_minify_value(context)
            ),
            direction.to_owned(),
            if override_kind.by_hook() {
                "final Rolldown output options"
            } else {
                "resolved bundler configuration"
            },
        )
    };
    let config_file = context.collection.config_file.clone();
    let located = match &config_file {
        Some(file) => context.original_location_without_coverage_gap(Path::new(file), 0, 0),
        None => super::Located {
            location: None,
            generated: None,
            snippet: None,
        },
    };
    drafts.push(DiagnosticDraft {
        rule: RuleId::MinificationDisabled,
        status: EvidenceStatus::Confirmed,
        title: "JavaScript minification is disabled".to_owned(),
        summary,
        conditions: vec![Condition::new(
            "Minification is enabled",
            ConditionOutcome::Unmet,
            format!("{option}: {value}"),
        )],
        located,
        module: config_file
            .as_deref()
            .map(|file| context.display_path(Path::new(file)))
            .unwrap_or_default(),
        dependency_paths: Vec::new(),
        evidence: vec![Evidence::new(
            evidence_label,
            EvidenceStatus::Confirmed,
            format!("{option}: {value}"),
        )],
        direction,
        safety: "Minified output changes function and class names; code that depends on \
                 Function.prototype.name needs keep-names options."
            .to_owned(),
        dependency: false,
        export_usage: None,
    });
}

/// Label of the configured Rolldown output `minify` option in diagnostics.
const OUTPUT_MINIFY_OPTION: &str = "build.rolldownOptions.output.minify";

/// Label of the finalized Rolldown output `minify` option in diagnostics.
const FINAL_OUTPUT_MINIFY_OPTION: &str = "final build.rolldownOptions.output.minify";

fn build_minify_value(context: &Context<'_>) -> String {
    context
        .collection
        .config_summary
        .get("build.minify")
        .cloned()
        .unwrap_or_else(|| "unknown".to_owned())
}

/// Every binding name of at least four characters introduced by `const`,
/// `let` or `var` declarations, including comma-separated declarators and
/// destructuring patterns. Function declaration names are excluded: bundlers
/// may keep top-level function names for reasons unrelated to eval. Text in
/// strings, templates, comments and regular expressions is masked first.
fn declared_names(source: &str) -> Vec<String> {
    let mut masked = source.as_bytes().to_vec();
    for (start, end) in non_code_ranges(source) {
        for byte in &mut masked[start..end.min(source.len())] {
            if *byte != b'\n' {
                *byte = b' ';
            }
        }
    }
    let code = String::from_utf8_lossy(&masked).into_owned();
    let keyword = Regex::new(r"\b(?:const|let|var)\s+").expect("static regex");
    let mut names = Vec::new();
    for found in keyword.find_iter(&code) {
        let declaration = declaration_text(&code[found.end()..]);
        for declarator in split_top_level(declaration, b',') {
            // Drop a TypeScript annotation (`name: Type`, `{ a }: Type`) and a
            // definite-assignment `!`; pattern-internal `key: value` pairs
            // are nested deeper and stay intact.
            let target = split_top_level(binding_target(declarator), b':')[0]
                .trim()
                .trim_end_matches('!');
            collect_binding_names(target, &mut names);
        }
    }
    collect_parameter_names(&code, &mut names);
    // `this` parameters are TypeScript-only and never runtime bindings.
    names.retain(|name| name.len() >= 4 && name != "this");
    names.sort();
    names.dedup();
    names
}

/// Parameter bindings of function declarations/expressions and arrow
/// functions, including destructured, defaulted, rest and TypeScript-annotated
/// parameters. Direct eval can read parameters by name too.
fn collect_parameter_names(code: &str, names: &mut Vec<String>) {
    let bytes = code.as_bytes();
    let mut lists: Vec<&str> = Vec::new();
    // function name(params) / function (params) / function* gen(params)
    let function = Regex::new(
        r"\bfunction\b\s*\*?\s*[A-Za-z_$][\w$]*\s*(?:<[^>()]*>)?\s*\(|\bfunction\b\s*\*?\s*\(",
    )
    .expect("static regex");
    for found in function.find_iter(code) {
        let open = found.end() - 1;
        if let Some(close) = matching(bytes, open, b'(', b')') {
            lists.push(&code[open + 1..close]);
        }
    }
    // (params) => and single-identifier name =>
    let mut search = 0usize;
    while let Some(offset) = code[search..].find("=>") {
        let arrow = search + offset;
        search = arrow + 2;
        let mut before = code[..arrow].trim_end();
        // A TypeScript return type `(a: T): R =>` sits between the parameter
        // list and the arrow; step back over it to the list.
        if !before.ends_with(')')
            && let Some(colon) = return_type_colon(before)
            && before[..colon].trim_end().ends_with(')')
        {
            before = before[..colon].trim_end();
        }
        if before.ends_with(')') {
            let close = before.len() - 1;
            if let Some(open) = matching_backward(bytes, close, b'(', b')') {
                lists.push(&code[open + 1..close]);
            }
        } else {
            let start = before
                .rfind(|character: char| {
                    !(character.is_alphanumeric() || character == '_' || character == '$')
                })
                .map_or(0, |index| index + 1);
            if start < before.len() {
                names.push(before[start..].to_owned());
            }
        }
    }
    for list in lists {
        for parameter in split_top_level(list, b',') {
            let target = split_top_level(binding_target(parameter), b':')[0]
                .trim()
                .trim_end_matches('?')
                .trim_start_matches("...");
            collect_binding_names(target, names);
        }
    }
}

/// Position of the `:` that starts a return-type annotation ending at the end
/// of `before`, scanning backwards at bracket depth zero.
fn return_type_colon(before: &str) -> Option<usize> {
    let bytes = before.as_bytes();
    let mut depth = 0i32;
    for index in (0..bytes.len()).rev() {
        match bytes[index] {
            b')' | b']' | b'}' | b'>' => depth += 1,
            b'(' | b'[' | b'{' | b'<' => depth -= 1,
            b':' if depth == 0 => return Some(index),
            b';' | b'=' | b',' if depth == 0 => return None,
            _ => {}
        }
        if depth < 0 {
            return None;
        }
    }
    None
}

fn matching(bytes: &[u8], open: usize, open_byte: u8, close_byte: u8) -> Option<usize> {
    let mut depth = 0i32;
    for (index, byte) in bytes.iter().enumerate().skip(open) {
        if *byte == open_byte {
            depth += 1;
        } else if *byte == close_byte {
            depth -= 1;
            if depth == 0 {
                return Some(index);
            }
        }
    }
    None
}

fn matching_backward(bytes: &[u8], close: usize, open_byte: u8, close_byte: u8) -> Option<usize> {
    let mut depth = 0i32;
    for index in (0..=close).rev() {
        if bytes[index] == close_byte {
            depth += 1;
        } else if bytes[index] == open_byte {
            depth -= 1;
            if depth == 0 {
                return Some(index);
            }
        }
    }
    None
}

/// Text of one declaration: up to a top-level `;`, or a top-level line break
/// that does not continue the declaration.
fn declaration_text(rest: &str) -> &str {
    let bytes = rest.as_bytes();
    let mut depth = 0i32;
    let mut last_significant = 0u8;
    for (index, byte) in bytes.iter().enumerate() {
        match byte {
            b'(' | b'[' | b'{' => depth += 1,
            b')' | b']' | b'}' => {
                depth -= 1;
                if depth < 0 {
                    return &rest[..index];
                }
            }
            b';' if depth == 0 => return &rest[..index],
            b'\n' if depth == 0 && !matches!(last_significant, b',' | b'=' | 0) => {
                return &rest[..index];
            }
            _ => {}
        }
        if !byte.is_ascii_whitespace() {
            last_significant = *byte;
        }
    }
    rest
}

fn split_top_level(text: &str, separator: u8) -> Vec<&str> {
    let mut parts = Vec::new();
    let mut depth = 0i32;
    let mut start = 0usize;
    for (index, byte) in text.bytes().enumerate() {
        match byte {
            b'(' | b'[' | b'{' => depth += 1,
            b')' | b']' | b'}' => depth -= 1,
            _ if byte == separator && depth == 0 => {
                parts.push(&text[start..index]);
                start = index + 1;
            }
            _ => {}
        }
    }
    parts.push(&text[start..]);
    parts
}

/// The binding part of a declarator (before its top-level `=` initializer).
fn binding_target(declarator: &str) -> &str {
    let mut depth = 0i32;
    let bytes = declarator.as_bytes();
    for (index, byte) in bytes.iter().enumerate() {
        match byte {
            b'(' | b'[' | b'{' => depth += 1,
            b')' | b']' | b'}' => depth -= 1,
            b'=' if depth == 0
                && bytes.get(index + 1) != Some(&b'=')
                && bytes.get(index + 1) != Some(&b'>') =>
            {
                return &declarator[..index];
            }
            // `for (const name of items)` and `for (const key in object)`.
            b'o' | b'i'
                if depth == 0
                    && index > 0
                    && bytes[index - 1].is_ascii_whitespace()
                    && (declarator[index..].starts_with("of")
                        || declarator[index..].starts_with("in"))
                    && bytes.get(index + 2).is_none_or(|next| {
                        next.is_ascii_whitespace() || b"[{(\"'`".contains(next)
                    }) =>
            {
                return &declarator[..index];
            }
            _ => {}
        }
    }
    declarator
}

/// Collects identifiers bound by a pattern: plain names, array elements,
/// object shorthand and `key: binding` values, rest elements and nested
/// patterns. Defaults after `=` and object keys are not bindings.
fn collect_binding_names(pattern: &str, names: &mut Vec<String>) {
    let pattern = pattern.trim();
    let inner = if (pattern.starts_with('{') && pattern.ends_with('}'))
        || (pattern.starts_with('[') && pattern.ends_with(']'))
    {
        Some(&pattern[1..pattern.len() - 1])
    } else {
        None
    };
    let Some(inner) = inner else {
        let name = pattern.trim_start_matches("...").trim();
        if !name.is_empty()
            && name
                .chars()
                .next()
                .is_some_and(|first| first.is_alphabetic() || first == '_' || first == '$')
            && name.chars().all(|character| {
                character.is_alphanumeric() || character == '_' || character == '$'
            })
        {
            names.push(name.to_owned());
        }
        return;
    };
    let object = pattern.starts_with('{');
    for element in split_top_level(inner, b',') {
        let element = binding_target(element);
        let value = if object {
            split_top_level(element, b':')
                .last()
                .copied()
                .unwrap_or(element)
        } else {
            element
        };
        collect_binding_names(value, names);
    }
}

/// Source ranges in which a binding named `eval` is visible. Lexical
/// declarations use their block, while `var` uses its containing function.
/// A named function expression binds its name only inside its own body.
/// Replaces string, template, comment and regular expression text with spaces
/// while keeping newlines, so byte offsets stay aligned with `source`.
fn mask_ignored(source: &str, ignored: &[(usize, usize)]) -> String {
    let mut masked = source.as_bytes().to_vec();
    for (start, end) in ignored {
        for byte in &mut masked[*start..(*end).min(source.len())] {
            if *byte != b'\n' {
                *byte = b' ';
            }
        }
    }
    String::from_utf8_lossy(&masked).into_owned()
}

fn eval_shadow_scopes(source: &str, ignored: &[(usize, usize)]) -> Vec<(usize, usize)> {
    let code = mask_ignored(source, ignored);
    let bytes = code.as_bytes();
    let mut blocks = Vec::new();
    let mut stack = Vec::new();
    for (index, byte) in bytes.iter().enumerate() {
        match byte {
            b'{' => stack.push(index),
            b'}' => {
                if let Some(open) = stack.pop() {
                    blocks.push((open, index));
                }
            }
            _ => {}
        }
    }
    let enclosing = |position: usize| {
        blocks
            .iter()
            .filter(|(open, close)| *open < position && position < *close)
            .min_by_key(|(open, close)| close - open)
            .map_or((0, bytes.len()), |(open, close)| (*open, *close))
    };
    // The lexical scan does not parse JavaScript. Match function bodies by
    // balancing their parameter lists, then use the already paired braces.
    // This keeps nested `var` declarations inside the nearest function.
    let function =
        Regex::new(r"\bfunction(?:\s*\*)?(?:\s+[A-Za-z_$][\w$]*)?").expect("static regex");
    let function_bodies: Vec<(usize, (usize, usize))> = function
        .find_iter(&code)
        .filter_map(|found| {
            // A member call such as `obj.function()` is not a declaration.
            let preceding = code[..found.start()]
                .bytes()
                .rev()
                .find(|byte| !byte.is_ascii_whitespace());
            if preceding == Some(b'.') {
                return None;
            }
            let mut cursor = found.end();
            while bytes.get(cursor).is_some_and(u8::is_ascii_whitespace) {
                cursor += 1;
            }
            if bytes.get(cursor) != Some(&b'(') {
                return None;
            }
            cursor = matching_delimiter(bytes, cursor, b'(', b')')? + 1;
            let body = function_body_block(&code, bytes, &blocks, cursor)?;
            Some((found.start(), body))
        })
        .collect();
    let mut bodies: Vec<(usize, usize)> = function_bodies.iter().map(|(_, body)| *body).collect();
    let mut scopes = Vec::new();
    // Parameter lists followed by a body: `(...) {` or `(...) =>`.
    let parameters = Regex::new(r"\(([^()]*)\)\s*(?:=>|\{)").expect("static regex");
    for captures in parameters.captures_iter(&code) {
        let (Some(list), Some(whole)) = (captures.get(1), captures.get(0)) else {
            continue;
        };
        // `if (eval) {`, `while (eval) {` and similar are conditions, not
        // parameter lists. `catch (eval) {` does bind `eval`.
        let keyword = code[..whole.start()]
            .trim_end()
            .rsplit(|character: char| {
                !(character.is_alphanumeric() || character == '_' || character == '$')
            })
            .next()
            .unwrap_or("");
        if whole.as_str().ends_with('{')
            && is_control_flow_parentheses(&code, whole.start(), keyword)
        {
            continue;
        }
        let mut names = Vec::new();
        for parameter in split_top_level(list.as_str(), b',') {
            let target = split_top_level(binding_target(parameter), b':')[0]
                .trim()
                .trim_end_matches('?')
                .trim_start_matches("...");
            collect_binding_names(target, &mut names);
        }
        let body = whole.end();
        let open = if whole.as_str().ends_with('{') {
            Some(body - 1)
        } else {
            code[body..]
                .bytes()
                .position(|byte| !byte.is_ascii_whitespace())
                .map(|offset| body + offset)
                .filter(|open| bytes.get(*open) == Some(&b'{'))
        };
        let block = open.and_then(|open| blocks.iter().find(|(start, _)| *start == open).copied());
        let end = block.map_or_else(
            || {
                code[body..]
                    .find([';', '\n'])
                    .map_or(bytes.len(), |offset| body + offset)
            },
            |(_, close)| close,
        );
        if let Some(block) = block
            && keyword != "catch"
        {
            bodies.push(block);
        }
        if names.iter().any(|name| name == "eval") {
            scopes.push((list.start(), end));
        }
    }
    // The compatibility regex above handles the common case. Scan balanced
    // delimiters as well so defaults such as `value = make()` do not hide a
    // parameter binding.
    for (open, byte) in bytes.iter().enumerate() {
        if *byte != b'(' {
            continue;
        }
        let Some(close) = matching_delimiter(bytes, open, b'(', b')') else {
            continue;
        };
        let Some((body, arrow)) = arrow_or_function_body(bytes, close) else {
            continue;
        };
        let keyword = code[..open]
            .trim_end()
            .rsplit(|character: char| {
                !(character.is_alphanumeric() || character == '_' || character == '$')
            })
            .next()
            .unwrap_or("");
        if !arrow && is_control_flow_parentheses(&code, open, keyword) {
            continue;
        }
        let list = &code[open + 1..close];
        let mut names = Vec::new();
        for parameter in split_top_level(list, b',') {
            let target = split_top_level(binding_target(parameter), b':')[0]
                .trim()
                .trim_end_matches('?')
                .trim_start_matches("...");
            collect_binding_names(target, &mut names);
        }
        let block = blocks.iter().find(|(start, _)| *start == body).copied();
        let end = block.map_or_else(
            || {
                code[body..]
                    .find([';', '\n'])
                    .map_or(bytes.len(), |offset| body + offset)
            },
            |(_, close)| close,
        );
        if let Some(block) = block
            && keyword != "catch"
        {
            bodies.push(block);
        }
        if names.iter().any(|name| name == "eval") {
            scopes.push((open + 1, end));
        }
    }

    let single = Regex::new(r"\beval\s*=>").expect("static regex");
    for found in single.find_iter(&code) {
        let end = code[found.end()..]
            .find([';', '\n'])
            .map_or(bytes.len(), |offset| found.end() + offset);
        scopes.push((found.start(), end));
    }
    // A single-parameter arrow does not have parentheses to identify its
    // body. Use ECMAScript's Unicode identifier classes so names such as `λ`
    // are scoped like their ASCII counterparts. ZWNJ and ZWJ are permitted
    // after the first identifier character by the ECMAScript grammar.
    let single_arrow =
        Regex::new(r"(?:\p{XID_Start}|[$_])(?:\p{XID_Continue}|[$_\u{200C}\u{200D}])*\s*=>\s*\{")
            .expect("static regex");
    for found in single_arrow.find_iter(&code) {
        let open = found.end() - 1;
        if let Some(body) = blocks.iter().find(|(start, _)| *start == open) {
            bodies.push(*body);
        }
    }
    let declaration = Regex::new(r"\b(const|let|var|function)\s+eval\b").expect("static regex");
    for captures in declaration.captures_iter(&code) {
        let (Some(found), Some(kind)) = (captures.get(0), captures.get(1)) else {
            continue;
        };
        let scope = match kind.as_str() {
            "var" => bodies
                .iter()
                .filter(|(open, close)| *open < found.start() && found.start() < *close)
                .min_by_key(|(open, close)| close - open)
                .copied()
                .unwrap_or((0, bytes.len())),
            "function" if named_function_is_expression(&code, found.start()) => function_bodies
                .iter()
                .find(|(start, _)| *start == found.start())
                .map_or_else(
                    || enclosing(found.start()),
                    |(_, body)| (found.start(), body.1),
                ),
            _ => enclosing(found.start()),
        };
        scopes.push(scope);
    }
    scopes
}

/// Finds a block-bodied function or arrow body after a parameter list.
/// TypeScript arrow return annotations can sit between `)` and `=>`, and
/// their nested delimiters must be skipped before locating the executable body.
fn arrow_or_function_body(bytes: &[u8], close: usize) -> Option<(usize, bool)> {
    let mut cursor = close + 1;
    while bytes.get(cursor).is_some_and(u8::is_ascii_whitespace) {
        cursor += 1;
    }
    if bytes.get(cursor) == Some(&b'{') {
        return Some((cursor, false));
    }
    if bytes.get(cursor..cursor.saturating_add(2)) == Some(b"=>") {
        cursor += 2;
        while bytes.get(cursor).is_some_and(u8::is_ascii_whitespace) {
            cursor += 1;
        }
        return (bytes.get(cursor) == Some(&b'{')).then_some((cursor, true));
    }
    if bytes.get(cursor) != Some(&b':') {
        return None;
    }
    cursor += 1;
    let mut parentheses = 0i32;
    let mut brackets = 0i32;
    let mut braces = 0i32;
    let mut angles = 0i32;
    while let Some(byte) = bytes.get(cursor) {
        if *byte == b'{' && parentheses == 0 && brackets == 0 && braces == 0 && angles == 0 {
            // A method can have a TypeScript return annotation before its
            // body. Object-shaped return types also use braces, so skip a
            // brace preceded by a type delimiter and continue to the next
            // executable block.
            let type_brace = bytes[..cursor]
                .iter()
                .rev()
                .find(|byte| !byte.is_ascii_whitespace())
                .is_some_and(|byte| matches!(byte, b':' | b'<' | b'|' | b'&' | b',' | b'[' | b'('))
                || bytes[..cursor].ends_with(b"=>");
            if !type_brace {
                return Some((cursor, false));
            }
            cursor = matching_delimiter(bytes, cursor, b'{', b'}')? + 1;
            continue;
        }
        if *byte == b'='
            && bytes.get(cursor + 1) == Some(&b'>')
            && parentheses == 0
            && brackets == 0
            && braces == 0
            && angles == 0
        {
            cursor += 2;
            while bytes.get(cursor).is_some_and(u8::is_ascii_whitespace) {
                cursor += 1;
            }
            return (bytes.get(cursor) == Some(&b'{')).then_some((cursor, true));
        }
        match *byte {
            b'(' => parentheses += 1,
            b')' => parentheses = parentheses.checked_sub(1)?,
            b'[' => brackets += 1,
            b']' => brackets = brackets.checked_sub(1)?,
            b'{' => braces += 1,
            b'}' => braces = braces.checked_sub(1)?,
            b'<' => angles += 1,
            b'>' if angles > 0 => angles -= 1,
            _ => {}
        }
        cursor += 1;
    }
    None
}

fn is_control_flow_parentheses(code: &str, open: usize, keyword: &str) -> bool {
    if matches!(keyword, "if" | "while" | "for" | "switch" | "with") {
        return true;
    }
    if keyword != "await" {
        return false;
    }
    let prefix = code[..open].trim_end();
    let await_start = prefix.len().saturating_sub("await".len());
    let before_await = prefix[..await_start].trim_end();
    before_await.strip_suffix("for").is_some_and(|before_for| {
        before_for.chars().next_back().is_none_or(|character| {
            !(character.is_alphanumeric() || character == '_' || character == '$')
        })
    })
}

/// Finds the block that starts a function body after its parameter list.
/// TypeScript return annotations can contain object types with braces of
/// their own, so skip those before selecting the executable block.
fn function_body_block(
    code: &str,
    bytes: &[u8],
    blocks: &[(usize, usize)],
    mut cursor: usize,
) -> Option<(usize, usize)> {
    let mut return_annotation = false;
    while let Some(byte) = bytes.get(cursor) {
        if byte.is_ascii_whitespace() {
            cursor += 1;
            continue;
        }
        if *byte == b'{' {
            let type_brace = return_annotation
                && code[..cursor]
                    .bytes()
                    .rev()
                    .find(|byte| !byte.is_ascii_whitespace())
                    .is_some_and(|byte| {
                        matches!(byte, b':' | b'<' | b'|' | b'&' | b',' | b'[' | b'(')
                    })
                || return_annotation && code[..cursor].trim_end().ends_with("=>");
            if !type_brace {
                return blocks.iter().find(|(open, _)| *open == cursor).copied();
            }
            let (_, close) = blocks.iter().find(|(open, _)| *open == cursor).copied()?;
            cursor = close + 1;
            continue;
        }
        if *byte == b':' {
            return_annotation = true;
        }
        cursor += 1;
    }
    None
}

fn named_function_is_expression(code: &str, start: usize) -> bool {
    let raw_prefix = &code[..start];
    let prefix = raw_prefix.trim_end();
    let prefix = prefix.strip_suffix("async").unwrap_or(prefix).trim_end();
    if prefix.ends_with("=>") {
        return true;
    }
    let preceding = prefix.as_bytes().last().copied();
    if preceding == Some(b':') && is_case_label(prefix) {
        return false;
    }
    if preceding.is_some_and(|byte| b"=([,:?!+-*/%&|^~".contains(&byte)) {
        return true;
    }
    let keyword = prefix
        .rsplit(|character: char| !(character.is_alphanumeric() || character == '_'))
        .next();
    if !matches!(
        keyword,
        Some("return" | "throw" | "yield" | "await" | "void" | "new")
    ) {
        return false;
    }
    let keyword_start = prefix
        .len()
        .saturating_sub(keyword.unwrap_or_default().len());
    !raw_prefix[keyword_start..].contains(['\n', '\r'])
}

fn matching_delimiter(bytes: &[u8], open: usize, opening: u8, closing: u8) -> Option<usize> {
    let mut depth = 0usize;
    for (index, byte) in bytes.iter().enumerate().skip(open) {
        match byte {
            byte if *byte == opening => depth += 1,
            byte if *byte == closing => {
                depth = depth.checked_sub(1)?;
                if depth == 0 {
                    return Some(index);
                }
            }
            _ => {}
        }
    }
    None
}

fn is_case_label(prefix: &str) -> bool {
    let label = prefix.strip_suffix(':').unwrap_or(prefix);
    let statement = label
        .rsplit_once(['{', '}', ';', '\n'])
        .map_or(label, |(_, rest)| rest)
        .trim();
    statement == "default"
        || statement
            .strip_prefix("case")
            .is_some_and(|rest| rest.chars().next().is_some_and(char::is_whitespace))
}

/// Byte offsets of every unshadowed direct `eval(...)` call in `source`, in
/// source order. Text in strings, templates, comments and regular expressions
/// is not a call, a binding named `eval` makes the calls in its scope ordinary
/// calls, and member accesses, private names, declarations and bodyless
/// signatures named `eval` are not calls.
fn direct_eval_offsets(source: &str) -> Vec<usize> {
    // `eval(` inside strings, templates, comments or regular expressions is
    // not a call.
    let ignored = non_code_ranges(source);
    // A binding named `eval` makes calls inside its scope ordinary calls that
    // cannot observe the surrounding scope; shadowing is decided per call.
    let shadows = eval_shadow_scopes(source, &ignored);
    // One forward pass over the tokens decides which `eval(` tokens call the
    // global function. Comments are token separators, so `eval /* note */
    // (code)` is a call.
    eval_sites::classify_eval_sites(source, &ignored)
        .into_iter()
        .filter(|site| site.kind == eval_sites::EvalSiteKind::DirectCall)
        .map(|site| site.start)
        .filter(|start| {
            !shadows
                .iter()
                .any(|(begin, end)| (*begin..*end).contains(start))
        })
        .collect()
}

fn direct_eval_line(source: &str) -> Option<(u32, u32)> {
    let start = *direct_eval_offsets(source).first()?;
    let line = source[..start].matches('\n').count();
    let column = start - source[..start].rfind('\n').map_or(0, |index| index + 1);
    Some((u32::try_from(line).ok()?, u32::try_from(column).ok()?))
}

/// A direct `eval` call that survives in an emitted chunk and belongs to the
/// analyzed module.
struct EmittedEval {
    chunk_index: usize,
    offset: usize,
}

/// Finds the emitted direct `eval` calls that are attributable to `module` in
/// `chunk_files`. A call is attributable when the chunk's source map resolves
/// its generated position to the module's source file, or, for a chunk without
/// a usable map, when the module is the only included module the chunk comes
/// from, whether or not the other modules have a file path. A chunk shared with
/// other modules and lacking a map proves nothing about which module owns an
/// `eval`, so it is not counted.
fn emitted_evals(
    context: &Context<'_>,
    module: &CollectedModule,
    module_path: &Path,
    chunk_files: &[String],
) -> Vec<EmittedEval> {
    let mut found = Vec::new();
    for file in chunk_files {
        let Some(index) = context.chunk_index(file) else {
            continue;
        };
        let chunk = &context.chunks[index];
        let offsets = direct_eval_offsets(&chunk.code);
        if offsets.is_empty() {
            continue;
        }
        // Every included module that shares the chunk owns part of its text,
        // including virtual and plugin-generated modules that have no file
        // path: their `eval` calls must not be blamed on the analyzed module.
        let owners = context
            .collection
            .modules
            .iter()
            .filter(|other| other.included && other.chunks.iter().any(|f| f == file))
            .count();
        let has_map = chunk.map.is_some();
        for offset in offsets {
            let attributable = if has_map {
                let (line, column) = chunk.position(offset);
                chunk
                    .original(line, column)
                    .is_some_and(|(path, _, _)| crate::sources::normalize(&path) == module_path)
            } else {
                owners <= 1
            };
            tracing::debug!(
                rule = "minifier/direct-eval-blocks-mangling",
                module = %context.module_log_label(&module.id),
                chunk = %file,
                offset,
                has_map,
                owners,
                attributable,
                "emitted direct eval candidate"
            );
            if attributable {
                found.push(EmittedEval {
                    chunk_index: index,
                    offset,
                });
            }
        }
    }
    found
}

fn direct_eval_mangling(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let collection = context.collection;
    if !collection.minify_enabled || !collection.default_minifier {
        return;
    }
    // Disabled mangling keeps every name readable, so retained names cannot be
    // attributed to eval. A source scan never overrides this explicit setting.
    if collection.mangle == ManglingSetting::Disabled {
        tracing::debug!(
            rule = "minifier/direct-eval-blocks-mangling",
            "skipping direct eval check because name mangling is disabled"
        );
        return;
    }
    for module in &collection.modules {
        if context.is_cancelled() {
            return;
        }
        if !module.included || module.path.is_none() {
            continue;
        }
        let rolldown_log = collection.logs.iter().find(|log| {
            log.code.as_deref() == Some("EVAL") && log.id.as_deref() == Some(module.id.as_str())
        });
        let rspack_bailout = module
            .bailouts
            .iter()
            .find(|entry| entry.contains("Module uses eval()"));
        let Some(source) = context.module_source(module) else {
            continue;
        };
        let source_eval = direct_eval_line(&source);
        if rolldown_log.is_none() && rspack_bailout.is_none() && source_eval.is_none() {
            continue;
        }
        let chunk_files: Vec<String> = module
            .chunks
            .iter()
            .filter(|file| context.chunk_index(file).is_some())
            .cloned()
            .collect();
        let module_path = crate::sources::normalize(Path::new(
            module.path.as_deref().expect("module path checked above"),
        ));
        // The source scan and the engine report only name a candidate. Tree
        // shaking and minification may have removed the call, so a finding
        // needs an eval call in the emitted chunk that maps back to this
        // module.
        let emitted = emitted_evals(context, module, &module_path, &chunk_files);
        let Some(first_emitted) = emitted.first() else {
            tracing::debug!(
                rule = "minifier/direct-eval-blocks-mangling",
                module = %context.module_log_label(&module.id),
                "skipping direct eval candidate with no attributable emitted eval call"
            );
            continue;
        };
        let names = declared_names(&source);
        let mut retained: BTreeSet<String> = BTreeSet::new();
        let mut emitted_files: Vec<String> = Vec::new();
        for call in &emitted {
            let chunk = &context.chunks[call.chunk_index];
            if emitted_files.contains(&chunk.file) {
                continue;
            }
            emitted_files.push(chunk.file.clone());
            for name in &names {
                let pattern = Regex::new(&format!(r"(^|[^\w$]){}([^\w$]|$)", regex::escape(name)))
                    .expect("escaped regex");
                if pattern.is_match(&chunk.code) {
                    retained.insert(name.clone());
                }
            }
        }
        if retained.is_empty() {
            // Names were mangled despite eval, so nothing is blocked.
            continue;
        }
        let engine_reported = rolldown_log.is_some() || rspack_bailout.is_some();
        let (line, column) = rolldown_log
            .and_then(|log| Some((log.line?.saturating_sub(1), log.column.unwrap_or(0))))
            .or(source_eval)
            .unwrap_or((0, 0));
        let display = context.module_display(module);
        // The location is the original-source candidate; the emitted call is
        // attached as the generated location. When the source map resolves the
        // emitted call to the module, that mapped position is more precise.
        let emitted_located =
            context.chunk_location(first_emitted.chunk_index, first_emitted.offset);
        let located = if emitted_located.generated.is_some() {
            emitted_located
        } else {
            let mut located = context.module_location(module, line, column);
            located.generated = emitted_located.location;
            located
        };
        let retained_list: Vec<String> = retained.into_iter().take(12).collect();
        let mut evidence = Vec::new();
        if let Some(log) = rolldown_log {
            evidence.push(Evidence::new(
                "rolldown log EVAL",
                EvidenceStatus::Confirmed,
                log.message.lines().next().unwrap_or_default().to_owned(),
            ));
        }
        if let Some(bailout) = rspack_bailout {
            evidence.push(Evidence::new(
                "rspack stats (optimizationBailout)",
                EvidenceStatus::Confirmed,
                bailout.clone(),
            ));
        }
        // The source scan names the original-source candidate only; the
        // emitted call below is what blocks mangling.
        if source_eval.is_some() {
            evidence.push(Evidence::new(
                "bundlewhy source scan",
                EvidenceStatus::Inferred,
                if engine_reported {
                    "Direct eval candidate found in the original source."
                } else {
                    "Direct eval candidate found in the original source; the engine did not report \
                     it."
                },
            ));
        }
        let emitted_sites: Vec<String> = emitted
            .iter()
            .take(3)
            .map(|call| {
                let chunk = &context.chunks[call.chunk_index];
                let (line, column) = chunk.position(call.offset);
                format!("{}:{}:{}", chunk.file, line + 1, column + 1)
            })
            .collect();
        evidence.push(Evidence::new(
            "emitted chunk text",
            EvidenceStatus::Confirmed,
            format!(
                "Direct eval call survives in the generated output and maps to this module ({} \
                 call(s)): {}",
                emitted.len(),
                emitted_sites.join(", ")
            ),
        ));
        evidence.push(Evidence::new(
            "generated chunk text",
            EvidenceStatus::Confirmed,
            format!(
                "Declared names still present in {}: {}",
                emitted_files.join(", "),
                retained_list.join(", ")
            ),
        ));
        drafts.push(DiagnosticDraft {
            rule: RuleId::DirectEvalBlocksMangling,
            status: if engine_reported {
                EvidenceStatus::Confirmed
            } else {
                EvidenceStatus::Inferred
            },
            title: format!("Direct eval in {display} keeps names unmangled"),
            summary: "Direct eval can read any variable in scope by name, so the minifier keeps \
                      the names of every binding the eval call can reach."
                .to_owned(),
            conditions: vec![
                Condition::new(
                    "No direct eval can observe local names",
                    ConditionOutcome::Unmet,
                    if engine_reported {
                        "Direct eval reported by the engine and present in the emitted output"
                    } else {
                        "Direct eval found in the source and present in the emitted output"
                    },
                ),
                Condition::new(
                    "Declared names are shortened in the output",
                    ConditionOutcome::Unmet,
                    format!("Unmangled: {}", retained_list.join(", ")),
                ),
            ],
            located,
            module: display,
            dependency_paths: context.dependency_paths(&module.id),
            evidence,
            direction: "Replace direct eval with indirect eval ((0, eval)(code)) or new \
                        Function(...), which run in global scope and let the minifier mangle \
                        local names."
                .to_owned(),
            safety: "Indirect eval and new Function cannot read local variables; pass required \
                     values explicitly. Evaluating untrusted strings remains unsafe either way."
                .to_owned(),
            dependency: context.is_dependency(module),
            export_usage: None,
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn eval_rule_drafts(source: &str, engine_reports_eval: bool) -> Vec<DiagnosticDraft> {
        eval_rule_drafts_with_mangling(source, engine_reports_eval, ManglingSetting::Enabled)
    }

    fn eval_rule_drafts_with_mangling(
        source: &str,
        engine_reports_eval: bool,
        mangle: ManglingSetting,
    ) -> Vec<DiagnosticDraft> {
        eval_rule_drafts_for_chunk(
            source,
            engine_reports_eval,
            mangle,
            &EmittedChunk::plain("const longLocalName = 1;\neval(code);"),
        )
    }

    /// Emitted chunk text, an optional source map, the number of other file
    /// modules that share the chunk and the number of virtual modules (no file
    /// path) that share it.
    struct EmittedChunk {
        code: String,
        map: Option<String>,
        other_modules: usize,
        virtual_modules: usize,
    }

    impl EmittedChunk {
        fn plain(code: &str) -> Self {
            Self {
                code: code.to_owned(),
                map: None,
                other_modules: 0,
                virtual_modules: 0,
            }
        }
    }

    fn eval_rule_drafts_for_chunk(
        source: &str,
        engine_reports_eval: bool,
        mangle: ManglingSetting,
        emitted: &EmittedChunk,
    ) -> Vec<DiagnosticDraft> {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        let source_path = root.join("src/main.js");
        let build_dir = root.join("build");
        std::fs::create_dir_all(source_path.parent().unwrap()).unwrap();
        std::fs::create_dir_all(&build_dir).unwrap();
        std::fs::write(&source_path, source).unwrap();
        std::fs::write(build_dir.join("main.js"), &emitted.code).unwrap();
        if let Some(map) = &emitted.map {
            std::fs::write(build_dir.join("main.js.map"), map).unwrap();
        }
        // The identifier carries a resource query, as a bundler identifier can;
        // no log line may repeat it.
        let module_id = format!("{}?token=hunter2", source_path.to_string_lossy());
        let mut modules = vec![CollectedModule {
            id: module_id.clone(),
            path: Some(source_path.to_string_lossy().into_owned()),
            included: true,
            chunks: vec!["main.js".to_owned()],
            ..CollectedModule::default()
        }];
        for index in 0..emitted.other_modules {
            let other = root.join(format!("src/other{index}.js"));
            modules.push(CollectedModule {
                id: other.to_string_lossy().into_owned(),
                path: Some(other.to_string_lossy().into_owned()),
                included: true,
                chunks: vec!["main.js".to_owned()],
                ..CollectedModule::default()
            });
        }
        for index in 0..emitted.virtual_modules {
            modules.push(CollectedModule {
                id: format!("\0virtual:generated-{index}?token=secret"),
                path: None,
                included: true,
                chunks: vec!["main.js".to_owned()],
                ..CollectedModule::default()
            });
        }
        let collection = crate::collect::Collection {
            default_minifier: true,
            minify_enabled: true,
            mangle,
            logs: if engine_reports_eval {
                vec![crate::collect::EngineLog {
                    code: Some("EVAL".to_owned()),
                    id: Some(module_id.clone()),
                    ..crate::collect::EngineLog::default()
                }]
            } else {
                Vec::new()
            },
            modules,
            ..crate::collect::Collection::default()
        };
        let chunk = crate::chunks::Chunk::load(&build_dir, "main.js", None, true, root).unwrap();
        // `module_source` reads build-backed text, so model the source-map
        // snapshot used by a collected build instead of relying on an
        // unverified live file.
        let mut sources = crate::sources::SourceStore::new(true);
        sources
            .snapshots
            .insert(crate::sources::normalize(&source_path), source.to_owned());
        let mut context = Context::new(
            &collection,
            root.to_path_buf(),
            "app".to_owned(),
            vec![chunk],
            sources,
        );
        let mut drafts = Vec::new();
        direct_eval_mangling(&mut context, &mut drafts);
        drafts
    }

    fn package_module(
        id: &str,
        importers: &[&str],
        path: &Path,
        version: &str,
        dir: &Path,
    ) -> CollectedModule {
        CollectedModule {
            id: id.to_owned(),
            path: Some(path.to_string_lossy().into_owned()),
            importers: importers.iter().map(|value| (*value).to_owned()).collect(),
            included: true,
            package_name: Some("@dnd-kit/sortable".to_owned()),
            package_version: Some(version.to_owned()),
            package_dir: Some(dir.to_string_lossy().into_owned()),
            ..CollectedModule::default()
        }
    }

    fn write_package_file(dir: &Path) -> std::path::PathBuf {
        let file = dir.join("dist/sortable.esm.js");
        std::fs::create_dir_all(file.parent().unwrap()).unwrap();
        std::fs::write(&file, "export const v = 1;").unwrap();
        file
    }

    fn run_duplicate_packages(root: &Path, modules: Vec<CollectedModule>) -> Vec<DiagnosticDraft> {
        let collection = crate::collect::Collection {
            modules,
            ..crate::collect::Collection::default()
        };
        let mut context = Context::new(
            &collection,
            root.to_path_buf(),
            "app".to_owned(),
            Vec::new(),
            crate::sources::SourceStore::new(true),
        );
        let mut drafts = Vec::new();
        duplicate_packages(&mut context, &mut drafts);
        drafts
    }

    #[test]
    fn duplicate_packages_distinguish_nested_node_modules_copies() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        let top_dir = root.join("node_modules/@dnd-kit/sortable");
        let nested_dir = root.join("node_modules/@douyinfe/semi-ui/node_modules/@dnd-kit/sortable");
        let top_file = write_package_file(&top_dir);
        let nested_file = write_package_file(&nested_dir);
        let mut entry = CollectedModule {
            id: "entry".to_owned(),
            is_entry: true,
            included: true,
            ..CollectedModule::default()
        };
        entry.path = Some(root.join("src/main.js").to_string_lossy().into_owned());
        let modules = vec![
            entry,
            package_module("top", &["entry"], &top_file, "10.0.0", &top_dir),
            package_module("nested", &["entry"], &nested_file, "7.0.0", &nested_dir),
        ];
        let drafts = run_duplicate_packages(root, modules);
        assert_eq!(drafts.len(), 1);
        let draft = &drafts[0];
        let top_display = "node_modules/@dnd-kit/sortable";
        let nested_display = "node_modules/@douyinfe/semi-ui/node_modules/@dnd-kit/sortable";
        let detail = &draft.conditions[0].detail;
        assert!(
            detail.contains(&format!("10.0.0 at {top_display} (1 module(s))")),
            "{detail}"
        );
        assert!(
            detail.contains(&format!("7.0.0 at {nested_display} (1 module(s))")),
            "{detail}"
        );
        let evidence_text: Vec<&str> = draft
            .evidence
            .iter()
            .map(|evidence| evidence.detail.as_str())
            .collect();
        assert!(evidence_text.iter().any(|text| text.contains(top_display)));
        assert!(
            evidence_text
                .iter()
                .any(|text| text.contains(nested_display))
        );
        // The located module is the second copy (by package directory order).
        let root_text = root.to_string_lossy();
        assert!(
            draft.module.starts_with("node_modules/"),
            "{}",
            draft.module
        );
        assert!(!draft.module.contains(root_text.as_ref()));
        let location = draft.located.location.as_ref().unwrap();
        assert_eq!(location.path, draft.module);
        // Endpoints of the two dependency paths are distinct displays.
        assert_eq!(draft.dependency_paths.len(), 2);
        let endpoints: BTreeSet<&str> = draft
            .dependency_paths
            .iter()
            .map(|path| path.modules.last().unwrap().as_str())
            .collect();
        assert_eq!(endpoints.len(), 2);
        assert!(
            endpoints
                .iter()
                .all(|endpoint| !endpoint.contains(root_text.as_ref()))
        );
    }

    #[test]
    fn single_included_copy_emits_no_duplicate_package() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        let top_dir = root.join("node_modules/@dnd-kit/sortable");
        let nested_dir = root.join("node_modules/@douyinfe/semi-ui/node_modules/@dnd-kit/sortable");
        let top_file = write_package_file(&top_dir);
        let nested_file = write_package_file(&nested_dir);
        let mut excluded = package_module("nested", &[], &nested_file, "7.0.0", &nested_dir);
        excluded.included = false;
        let modules = vec![
            package_module("top", &[], &top_file, "10.0.0", &top_dir),
            excluded,
        ];
        assert!(run_duplicate_packages(root, modules).is_empty());
    }

    #[test]
    fn nested_node_modules_paths_count_as_dependencies() {
        assert!(path_is_dependency("node_modules/a/index.js"));
        assert!(path_is_dependency("packages/a/node_modules/x/index.js"));
        assert!(path_is_dependency("../node_modules/x/index.js"));
        assert!(!path_is_dependency("src/node_modules_like/index.js"));
    }

    #[test]
    fn non_code_ranges_cover_strings_comments_templates_and_regexes() {
        let code = r#"a("process.env.A");/* process.env.B */x=`process.env.C ${process.env.D} t`;// process.env.E
y=/process.env.F/.test(z),process.env.G"#;
        let ranges = non_code_ranges(code);
        let ignored =
            |name: &str| in_ranges(&ranges, code.find(&format!("process.env.{name}")).unwrap());
        for name in ["A", "B", "C", "E", "F"] {
            assert!(ignored(name), "{name} should be ignored");
        }
        for name in ["D", "G"] {
            assert!(!ignored(name), "{name} is code");
        }
        // Division is not a regular expression.
        let division = "a=b/2,process.env.H/3";
        assert!(!in_ranges(
            &non_code_ranges(division),
            division.find("process").unwrap()
        ));
    }

    #[test]
    fn jsx_closing_and_self_closing_tags_are_not_regular_expressions() {
        for code in [
            "x=<b>t</b>;process.env.H</p>",
            "x=<b>t</b>{process.env.H}</p>",
            "x=<></>;process.env.H</p>",
            "x=<b a={1} />;process.env.H</p>",
            "x=<b {...p} />{process.env.H}</p>",
            "x=<b a=\"1\" />{process.env.H}</p>",
            "x=<br />{process.env.H}</p>",
            "x=<A<B> />{process.env.H}</p>",
            "x=<b>t</b >{process.env.H}</p>",
            "x=<b>t</ns:b>{process.env.H}</p>",
            "x=<b>t</Foo.Bar-x>{process.env.H}</p>",
        ] {
            assert!(
                !in_ranges(&non_code_ranges(code), code.find("process").unwrap()),
                "{code}"
            );
        }
        // Regular expressions after other tokens, including `<`, `}` and `>`,
        // are still literals.
        for code in [
            "x=a< /process.env.H/.test(b)",
            "x=a</process.env.H/.test(b)",
            "if(a < /process.env.H/.test(b))run()",
            "x=a<b>/process.env.H/.test(c)",
            "x=a>/process.env.H/.test(b)",
            "x=a+/process.env.H/.test(b)",
            "if(a){}/process.env.H/.test(b)",
            "x=(/process.env.H/g)",
            "x=a.replace(/>/g,\"&gt;\");y=/process.env.H/",
        ] {
            assert!(
                in_ranges(&non_code_ranges(code), code.find("process").unwrap()),
                "{code}"
            );
        }
    }

    #[test]
    fn arrow_parameters_are_found_behind_return_type_annotations() {
        let names = declared_names("const run = (descriptiveName: string): void => eval(code);");
        assert!(names.contains(&"descriptiveName".to_owned()));
        assert!(!names.contains(&"void".to_owned()));
        let names = declared_names("const f = (inputValue): Promise<Map<string, number>> => g();");
        assert!(names.contains(&"inputValue".to_owned()));
    }

    #[test]
    fn computed_env_reads_are_recognized() {
        assert!(reads_process_env(
            r#"if (process.env["FEATURE_FLAG"]) {}"#,
            "FEATURE_FLAG"
        ));
        assert!(reads_process_env(
            "if (process.env['FEATURE_FLAG']) {}",
            "FEATURE_FLAG"
        ));
        assert!(reads_process_env(
            "if (process.env.FEATURE_FLAG) {}",
            "FEATURE_FLAG"
        ));
        assert!(!reads_process_env(
            "if (process.env.FEATURE_FLAGS) {}",
            "FEATURE_FLAG"
        ));
    }

    #[test]
    fn env_reads_require_a_generated_branch() {
        let guard = |code: &str, read: &str| {
            let start = code.find(read).unwrap();
            env_guard(code, start, start + read.len(), &non_code_ranges(code))
                .map(EnvGuard::evidence)
        };
        for code in [
            "console.log(process.env.VERSION);",
            "const version=process.env.VERSION;ready&&run()",
            "console.log(process.env.VERSION),ready&&run()",
            "if(console.log(process.env.VERSION))run()",
            "if(process.env.VERSION,ready)run()",
            "if((process.env.VERSION),ready)run()",
            "if([process.env.VERSION])run()",
            "if({value:process.env.VERSION})run()",
            "console.log(process.env.VERSION, '&&')",
        ] {
            assert_eq!(guard(code, "process.env.VERSION"), None, "{code}");
        }
        assert_eq!(guard("console.log(({}).VERSION)", "({}).VERSION"), None);
        assert_eq!(
            guard("'on'===process.env.VERSION&&run()", "process.env.VERSION"),
            Some("the retained read controls a && short-circuit branch")
        );
        assert_eq!(
            guard(
                "(sideEffect(), process.env.VERSION)&&run()",
                "process.env.VERSION"
            ),
            Some("the retained read controls a && short-circuit branch")
        );
        assert_eq!(
            guard("({}).VERSION==='on'&&run()", "({}).VERSION"),
            Some("the retained read controls a && short-circuit branch")
        );
        assert_eq!(
            guard("if((process.env.VERSION))run()", "process.env.VERSION"),
            Some("the retained read is in an if condition")
        );
        assert_eq!(
            guard(
                "if({on:true}[process.env.VERSION])run()",
                "process.env.VERSION"
            ),
            Some("the retained read is in an if condition")
        );
        assert_eq!(
            guard(
                "if(ready?process.env.VERSION:false)run()",
                "process.env.VERSION"
            ),
            Some("the retained read is in an if condition")
        );
        assert_eq!(
            guard(
                "if/*!license*/(process.env.VERSION)run()",
                "process.env.VERSION"
            ),
            Some("the retained read is in an if condition")
        );
        for code in [
            "obj.if(process.env.VERSION)run()",
            "obj.switch(process.env.VERSION)run()",
        ] {
            assert_eq!(guard(code, "process.env.VERSION"), None, "{code}");
        }
        assert_eq!(
            guard("process.env.VERSION?on():off()", "process.env.VERSION"),
            Some("the retained read controls a conditional (?:) branch")
        );
        assert_eq!(
            guard("process.env.VERSION??fallback", "process.env.VERSION"),
            Some("the retained read controls a ?? fallback branch")
        );
        assert_eq!(
            guard("(()=>process.env.VERSION)&&run()", "process.env.VERSION"),
            None
        );
        assert_eq!(
            guard(
                "function run(){process.env.VERSION&&work()}",
                "process.env.VERSION"
            ),
            Some("the retained read controls a && short-circuit branch")
        );
        assert_eq!(
            guard(
                "for (; process.env.VERSION; ) work()",
                "process.env.VERSION"
            ),
            Some("the retained read is in a for condition")
        );
        assert_eq!(
            guard(
                "for (process.env.VERSION; ; ) work()",
                "process.env.VERSION"
            ),
            None
        );
        assert_eq!(
            guard(
                "for (; ; process.env.VERSION) work()",
                "process.env.VERSION"
            ),
            None
        );
        assert_eq!(
            guard(
                "ready ? process.env.VERSION : fallback && work()",
                "process.env.VERSION"
            ),
            None
        );
        assert_eq!(
            guard(
                "(ready ? process.env.VERSION : fallback) && work()",
                "process.env.VERSION"
            ),
            Some("the retained read controls a && short-circuit branch")
        );
        assert_eq!(
            guard(
                "process.env.VERSION ? yes : no && work()",
                "process.env.VERSION"
            ),
            Some("the retained read controls a conditional (?:) branch")
        );
        assert_eq!(
            guard("process.env.VERSION\nready&&run()", "process.env.VERSION"),
            None
        );
        assert_eq!(
            guard("process.env.VERSION\n&&run()", "process.env.VERSION"),
            Some("the retained read controls a && short-circuit branch")
        );
        assert_eq!(
            guard("if (process.env.VERSION\n) run()", "process.env.VERSION"),
            Some("the retained read is in an if condition")
        );
    }

    #[test]
    fn env_guard_scans_many_reads_in_one_pass() {
        let mut code = String::new();
        let mut reads = Vec::new();
        for index in 0..256 {
            let read = format!("process.env.FLAG{index}");
            let start = code.len();
            code.push_str(&read);
            reads.push((start, start + read.len()));
            code.push_str("&&run();");
        }
        let guards = env_guards(&code, &reads, &[]);
        assert_eq!(guards.len(), reads.len());
        assert!(
            guards
                .iter()
                .all(|guard| matches!(guard, Some(EnvGuard::ShortCircuitAnd)))
        );
    }

    #[test]
    fn eval_shadowing_is_decided_per_call() {
        // A shadowing parameter in one function does not hide a real direct
        // eval in another.
        let source = "function a(eval) {\n  eval(x);\n}\nfunction b() {\n  return eval(y);\n}\n";
        assert_eq!(direct_eval_line(source), Some((4, 9)));
        assert_eq!(direct_eval_line("function run(eval) { eval(code); }"), None);
        assert_eq!(
            direct_eval_line("{ const eval = (x) => x; eval(y); }"),
            None
        );
        assert_eq!(direct_eval_line("const f = (eval) => eval(z);"), None);
        assert_eq!(
            direct_eval_line("{ let eval = g; } eval(w);"),
            Some((0, 18))
        );
        // Control-flow conditions are not parameter lists.
        assert_eq!(direct_eval_line("if (eval) { eval(code); }"), Some((0, 12)));
        assert_eq!(
            direct_eval_line("while (eval) { eval(code); }"),
            Some((0, 15))
        );
        assert_eq!(
            direct_eval_line("try {} catch (eval) { eval(code); }"),
            None
        );
    }

    #[test]
    fn named_function_expression_eval_shadows_only_its_body() {
        assert_eq!(
            direct_eval_line("const helper = function eval() { return 1; }; eval(code);"),
            Some((0, 46))
        );
        assert_eq!(
            direct_eval_line("const helper = function eval() { return eval(code); };"),
            None
        );
        assert_eq!(
            direct_eval_line("function eval(code) { return code; } eval(code);"),
            None
        );
        let source = "const helper = function eval() { return eval(code); };\nconst longLocalName \
                      = 1;\neval('longLocalName');";
        assert_eq!(direct_eval_line(source), Some((2, 0)));
        let drafts = eval_rule_drafts(source, false);
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].rule, RuleId::DirectEvalBlocksMangling);
        assert_eq!(drafts[0].status, EvidenceStatus::Inferred);
        assert_eq!(drafts[0].located.location.as_ref().unwrap().line, 3);
    }

    #[test]
    fn eval_method_declarations_are_not_direct_eval() {
        // Class, object-literal and modifier forms declare a member.
        for source in [
            "class C { eval(value) { return value; } }",
            "class C {\n  eval(value) {\n    return value;\n  }\n}",
            "class C { run() {} eval(value) { return value; } }",
            "const o = { eval(value) { return value; } };",
            "const o = { a: 1, eval(value) { return value; } };",
            "class C { async eval(value) { return value; } }",
            "class C { static eval(value) { return value; } }",
            "class C { static async eval(value) { return value; } }",
            "class C { get eval() { return 1; } }",
            "class C { set eval(value) { this.value = value; } }",
            "class C { *eval(value) { yield value; } }",
            "class C { async *eval(value) { yield value; } }",
            "const o = { *eval(value) { yield value; } };",
            "class C { public eval(o: object): boolean { return true; } }",
            "class C { eval(o): boolean { return true; } }",
            "class C { eval(o): Promise<{ ok: boolean }> { return null; } }",
            "class C { eval(o) { return 1; } } const c = new C(); c.eval(1);",
            "eval(value) { return value; }",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source}");
        }
        // Member calls and ordinary uses of a different token stay ignored.
        assert_eq!(
            direct_eval_line("instance.eval(value); other?.eval(value);"),
            None
        );
        assert_eq!(direct_eval_line("instance.\n  eval(value);"), None);
    }

    #[test]
    fn eval_calls_next_to_method_declarations_are_still_found() {
        let source = "class C {\n  eval(value) { return value; }\n}\nconst c = new \
                      C();\nc.eval(1);\neval(code);\n";
        assert_eq!(direct_eval_line(source), Some((5, 0)));
        // A call that is followed by a block or sits in expression position is
        // not a declaration.
        assert_eq!(
            direct_eval_line("run();\nif (ready) eval(code);"),
            Some((1, 11))
        );
        assert_eq!(
            direct_eval_line("const x = ok ? eval(a) : { b: 1 };"),
            Some((0, 15))
        );
        assert_eq!(direct_eval_line("return eval(this.code);"), Some((0, 7)));
        assert_eq!(direct_eval_line("const v = [a, eval(b)];"), Some((0, 14)));
        assert_eq!(
            direct_eval_line("class C { run() { return eval(this.code); } }"),
            Some((0, 25))
        );
    }

    #[test]
    fn eval_rule_ignores_method_declarations_but_reports_real_calls() {
        let method = "const longLocalName = 1;\nexport class Pattern {\n  eval(value) {\n    \
                      return value;\n  }\n}\nnew Pattern().eval(1);\n";
        assert!(eval_rule_drafts(method, false).is_empty());
        let call = "const longLocalName = 1;\nexport class Program {\n  eval(value) {\n    return \
                    value;\n  }\n  run() {\n    return eval(this.code);\n  }\n}\n";
        let drafts = eval_rule_drafts(call, false);
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].rule, RuleId::DirectEvalBlocksMangling);
        assert_eq!(drafts[0].status, EvidenceStatus::Inferred);
        // The finding points at the call on line 7, not at the declaration.
        assert_eq!(drafts[0].located.location.as_ref().unwrap().line, 7);
        assert_eq!(drafts[0].located.location.as_ref().unwrap().column, 12);
    }

    const DEAD_EVAL_SOURCE: &str = "const longLocalName = 1;\nexport function run(code) {\n  \
                                    return longLocalName;\n  return eval(code);\n}\n";

    #[test]
    fn source_eval_removed_from_emitted_chunk_gives_no_finding() {
        // Tree shaking and minification dropped the call, but the declared
        // name is still present in the chunk.
        let emitted = EmittedChunk::plain("const longLocalName = 1;\nconsole.log(longLocalName);");
        for engine_reports_eval in [false, true] {
            let drafts = eval_rule_drafts_for_chunk(
                DEAD_EVAL_SOURCE,
                engine_reports_eval,
                ManglingSetting::Enabled,
                &emitted,
            );
            assert!(
                drafts.is_empty(),
                "engine_reports_eval={engine_reports_eval}"
            );
        }
    }

    #[test]
    fn emitted_eval_text_in_strings_and_comments_is_not_a_call() {
        let emitted = EmittedChunk::plain(
            "const longLocalName = 1;\n// eval(code)\nconsole.log(\"eval(code)\", longLocalName);",
        );
        let drafts =
            eval_rule_drafts_for_chunk(DEAD_EVAL_SOURCE, true, ManglingSetting::Enabled, &emitted);
        assert!(drafts.is_empty());
    }

    #[test]
    fn surviving_emitted_eval_reports_with_emitted_evidence() {
        let emitted =
            EmittedChunk::plain("const longLocalName = 1;\nexport const r = (c) => eval(c);");
        let drafts =
            eval_rule_drafts_for_chunk(DEAD_EVAL_SOURCE, false, ManglingSetting::Enabled, &emitted);
        assert_eq!(drafts.len(), 1);
        let draft = &drafts[0];
        assert_eq!(draft.rule, RuleId::DirectEvalBlocksMangling);
        // A source-only candidate stays Inferred even with the emitted call.
        assert_eq!(draft.status, EvidenceStatus::Inferred);
        let labels: Vec<&str> = draft
            .evidence
            .iter()
            .map(|e| e.provenance.as_str())
            .collect();
        assert!(labels.contains(&"bundlewhy source scan"), "{labels:?}");
        let emitted_evidence = draft
            .evidence
            .iter()
            .find(|e| e.provenance == "emitted chunk text")
            .expect("emitted chunk evidence");
        assert!(
            emitted_evidence.detail.contains("main.js:2:"),
            "{}",
            emitted_evidence.detail
        );
        // The original-source candidate is the location; the emitted call is
        // the generated location.
        let location = draft.located.location.as_ref().unwrap();
        assert_eq!(location.kind, crate::model::LocationKind::Original);
        assert_eq!(location.line, 4);
        let generated = draft.located.generated.as_ref().unwrap();
        assert_eq!(generated.kind, crate::model::LocationKind::Generated);
        assert_eq!(generated.line, 2);
    }

    #[test]
    fn engine_reported_eval_is_confirmed_only_with_emitted_eval() {
        let with_call = EmittedChunk::plain("const longLocalName = 1;\neval(code);");
        let drafts = eval_rule_drafts_for_chunk(
            DEAD_EVAL_SOURCE,
            true,
            ManglingSetting::Enabled,
            &with_call,
        );
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].status, EvidenceStatus::Confirmed);
        let without_call = EmittedChunk::plain("const longLocalName = 1;");
        assert!(
            eval_rule_drafts_for_chunk(
                DEAD_EVAL_SOURCE,
                true,
                ManglingSetting::Enabled,
                &without_call
            )
            .is_empty()
        );
    }

    #[test]
    fn engine_report_without_source_eval_needs_emitted_eval() {
        let source = "const longLocalName = 1;\nexport default longLocalName;\n";
        let without_call = EmittedChunk::plain("const longLocalName = 1;");
        assert!(
            eval_rule_drafts_for_chunk(source, true, ManglingSetting::Enabled, &without_call)
                .is_empty()
        );
        let with_call = EmittedChunk::plain("const longLocalName = 1;\neval(code);");
        let drafts = eval_rule_drafts_for_chunk(source, true, ManglingSetting::Enabled, &with_call);
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].status, EvidenceStatus::Confirmed);
    }

    #[test]
    fn shared_chunk_without_map_does_not_attribute_eval_to_module() {
        // Another module shares the chunk and no source map says which module
        // the call belongs to, so the declared names alone prove nothing.
        let emitted = EmittedChunk {
            code: "const longLocalName = 1;\neval(code);".to_owned(),
            map: None,
            other_modules: 1,
            virtual_modules: 0,
        };
        let drafts =
            eval_rule_drafts_for_chunk(DEAD_EVAL_SOURCE, true, ManglingSetting::Enabled, &emitted);
        assert!(drafts.is_empty());
    }

    #[derive(Clone)]
    struct LogWriter(std::sync::Arc<std::sync::Mutex<Vec<u8>>>);

    impl std::io::Write for LogWriter {
        fn write(&mut self, bytes: &[u8]) -> std::io::Result<usize> {
            self.0.lock().unwrap().extend_from_slice(bytes);
            Ok(bytes.len())
        }

        fn flush(&mut self) -> std::io::Result<()> {
            Ok(())
        }
    }

    /// The debug log written while the rule analyzes `emitted`. Tests run in
    /// parallel, and a call site first reached on a thread without a
    /// subscriber caches that no one listens until a later subscriber is
    /// registered, so a run that captured nothing is repeated.
    fn eval_rule_log(emitted: &EmittedChunk) -> String {
        for _ in 0..50 {
            let logs = std::sync::Arc::new(std::sync::Mutex::new(Vec::new()));
            let writer = logs.clone();
            let subscriber = tracing_subscriber::fmt()
                .with_ansi(false)
                .with_max_level(tracing::Level::DEBUG)
                .with_writer(move || LogWriter(writer.clone()))
                .finish();
            let guard = tracing::subscriber::set_default(subscriber);
            eval_rule_drafts_for_chunk(DEAD_EVAL_SOURCE, true, ManglingSetting::Enabled, emitted);
            drop(guard);
            let bytes = logs.lock().unwrap().clone();
            if !bytes.is_empty() {
                return String::from_utf8(bytes).unwrap();
            }
            std::thread::sleep(std::time::Duration::from_millis(20));
        }
        String::new()
    }

    #[test]
    fn eval_attribution_logs_never_name_the_raw_module_identifier() {
        // An attributable emitted call logs the candidate event, a removed
        // call logs the skip event, and a shared chunk logs the owner count.
        let cases = [
            (
                "candidate",
                EmittedChunk::plain("const longLocalName = 1;\neval(code);"),
            ),
            ("skipped", EmittedChunk::plain("const longLocalName = 1;")),
            (
                "shared",
                EmittedChunk {
                    code: "const longLocalName = 1;\neval(code);".to_owned(),
                    map: None,
                    other_modules: 1,
                    virtual_modules: 1,
                },
            ),
        ];
        for (name, emitted) in cases {
            let log = eval_rule_log(&emitted);
            assert!(log.contains("module=src/main.js"), "{name}: {log}");
            assert!(!log.contains("hunter2"), "{name}: {log}");
            assert!(!log.contains("token="), "{name}: {log}");
            assert!(!log.contains("virtual:"), "{name}: {log}");
        }
    }

    #[test]
    fn virtual_module_sharing_a_chunk_without_map_blocks_attribution() {
        // A virtual or plugin-generated module has no file path but still owns
        // part of the chunk, so the call cannot be blamed on the file module.
        let emitted = EmittedChunk {
            code: "const longLocalName = 1;\neval(code);".to_owned(),
            map: None,
            other_modules: 0,
            virtual_modules: 1,
        };
        for engine_reports_eval in [true, false] {
            let drafts = eval_rule_drafts_for_chunk(
                DEAD_EVAL_SOURCE,
                engine_reports_eval,
                ManglingSetting::Enabled,
                &emitted,
            );
            assert!(drafts.is_empty(), "engine report: {engine_reports_eval}");
        }
        // Several virtual modules and a file module change nothing.
        let crowded = EmittedChunk {
            other_modules: 1,
            virtual_modules: 2,
            ..emitted
        };
        assert!(
            eval_rule_drafts_for_chunk(DEAD_EVAL_SOURCE, true, ManglingSetting::Enabled, &crowded)
                .is_empty()
        );
    }

    #[test]
    fn virtual_module_in_a_chunk_with_map_leaves_mapped_attribution() {
        // The source map resolves the call to the module, so a virtual
        // neighbour does not hide it.
        let mut emitted = shared_chunk_with_map();
        emitted.virtual_modules = 1;
        let drafts =
            eval_rule_drafts_for_chunk(DEAD_EVAL_SOURCE, true, ManglingSetting::Enabled, &emitted);
        assert_eq!(drafts.len(), 1);
    }

    fn shared_chunk_with_map() -> EmittedChunk {
        // Line 1 maps to `other0.js` and line 2 (the eval call) maps to
        // `main.js`.
        let map = r#"{"version":3,"file":"main.js","sources":["../src/other0.js","../src/main.js"],"names":[],"mappings":"AAAA;ACAA"}"#;
        EmittedChunk {
            code: "const longLocalName = 1;\neval(code);".to_owned(),
            map: Some(map.to_owned()),
            other_modules: 1,
            virtual_modules: 0,
        }
    }

    #[test]
    fn shared_chunk_eval_mapped_to_module_is_reported() {
        let drafts = eval_rule_drafts_for_chunk(
            DEAD_EVAL_SOURCE,
            true,
            ManglingSetting::Enabled,
            &shared_chunk_with_map(),
        );
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].status, EvidenceStatus::Confirmed);
        // The source map resolves the emitted call to the module, so the
        // generated position accompanies the original location.
        let generated = drafts[0]
            .located
            .generated
            .as_ref()
            .expect("generated location");
        assert_eq!(generated.line, 2);
    }

    #[test]
    fn shared_chunk_eval_mapped_to_another_module_is_not_reported() {
        let mut emitted = shared_chunk_with_map();
        // Swap the sources so the call belongs to the other module.
        emitted.map = emitted.map.map(|map| {
            map.replace(
                r#""sources":["../src/other0.js","../src/main.js"]"#,
                r#""sources":["../src/main.js","../src/other0.js"]"#,
            )
        });
        let drafts =
            eval_rule_drafts_for_chunk(DEAD_EVAL_SOURCE, true, ManglingSetting::Enabled, &emitted);
        assert!(drafts.is_empty());
    }

    #[test]
    fn comments_between_eval_and_its_arguments_keep_the_direct_call() {
        for source in [
            "eval/*!license*/(code);",
            "eval /* note */ (code);",
            "eval /* a */ /* b */ (code);",
            "eval // note\n(code);",
            "eval // note\n  // more\n  (code);",
            "eval\n/* multi\nline */\n(code);",
            "/* before */ eval /* c */ (x);",
            "const run = (c) => eval /* c */ (c);",
        ] {
            let start = source.find("eval").unwrap();
            assert_eq!(direct_eval_offsets(source), vec![start], "{source}");
        }
        // A comment before the callee keeps the location on the `eval` token.
        assert_eq!(
            direct_eval_line("const a = 1;\n/* c */ eval /* c */ (x);"),
            Some((1, 8))
        );
        // Member access, shadowing and method declarations still apply.
        for source in [
            "obj.eval /* c */ (x);",
            "obj. /* c */ eval(x);",
            "obj // c\n  .eval(x);",
            "function f(eval) { eval /* c */ (x); }",
            "class C { eval /* c */ (x) { return x; } }",
            "const o = { eval // c\n (x) { return x; } };",
        ] {
            assert!(direct_eval_offsets(source).is_empty(), "{source}");
        }
        // Text in comments, strings and a longer identifier are not calls.
        for source in [
            "/* eval (x) */ run();",
            "// eval (x)\nrun();",
            "const t = 'eval /* c */ (x)';",
            "evaluate /* c */ (x);",
            "eval /* c */ ;",
            "eval /* c */ ? (x) : y;",
        ] {
            assert!(direct_eval_offsets(source).is_empty(), "{source}");
        }
    }

    /// Every way an `eval` call can appear in emitted text, with the number of
    /// direct calls it contains. The same table drives the offset scan and the
    /// rule, so a form is classified the same way in the source and in the
    /// emitted chunk.
    const EMITTED_EVAL_FORMS: &[(&str, usize)] = &[
        // Plain and spread calls are direct.
        ("eval(c);", 1),
        ("export const r = (c) => eval(c);", 1),
        ("run(...eval(c));", 1),
        ("eval(...args);", 1),
        ("eval(c), eval(d);", 2),
        // Whitespace and comments between the callee and its arguments.
        ("eval /* c */ (c);", 1),
        ("eval/*!x*/(c);", 1),
        ("eval // c\n(c);", 1),
        // Grouping parentheses keep the reference, so the call stays direct.
        ("(eval)(c);", 1),
        ("((eval))(c);", 1),
        ("(/* c */ eval /* c */)(c);", 1),
        ("export const r = (c) => (eval)(c);", 1),
        ("run((eval)(c));", 1),
        ("(eval)(c); eval(d);", 2),
        // A comma expression yields a value, so these calls are indirect.
        ("(0, eval)(c);", 0),
        ("(0,eval)(c);", 0),
        ("(void 0, eval)(c);", 0),
        ("(eval, 0)(c);", 0),
        // An optional call is never a direct eval.
        ("eval?.(c);", 0),
        ("(eval)?.(c);", 0),
        // Other uses of the name.
        ("run(eval)(c);", 0),
        ("obj.eval(c);", 0),
        ("(obj.eval)(c);", 0),
        ("obj?.eval(c);", 0),
        ("globalThis.eval(c);", 0),
        ("window['eval'](c);", 0),
        ("const f = eval;", 0),
        ("run(eval, c);", 0),
        ("new (eval)(c);", 0),
        ("evaluate(c);", 0),
        ("myeval(c);", 0),
        ("eval$(c);", 0),
        // Text that is not code.
        ("// eval(c)\nrun();", 0),
        ("/* (eval)(c) */ run();", 0),
        ("run('eval(c)', \"(eval)(c)\", `eval(c)`);", 0),
        ("const re = /eval(c)/;", 0),
        // Scope: a binding named `eval` makes its calls ordinary.
        ("function f(eval) { (eval)(c); eval(c); }", 0),
        ("function f(eval) { return eval(c); } eval(d);", 1),
    ];

    #[test]
    fn emitted_eval_call_forms_are_classified_by_one_table() {
        for (form, expected) in EMITTED_EVAL_FORMS {
            let offsets = direct_eval_offsets(form);
            assert_eq!(offsets.len(), *expected, "offsets for {form}");
            for offset in &offsets {
                assert!(form[*offset..].starts_with("eval"), "{form}: {offset}");
            }
            let emitted = EmittedChunk::plain(&format!("const longLocalName = 1;\n{form}"));
            for engine_reports_eval in [true, false] {
                let drafts = eval_rule_drafts_for_chunk(
                    DEAD_EVAL_SOURCE,
                    engine_reports_eval,
                    ManglingSetting::Enabled,
                    &emitted,
                );
                assert_eq!(
                    drafts.len(),
                    usize::from(*expected > 0),
                    "{form}, engine report: {engine_reports_eval}"
                );
            }
        }
    }

    #[test]
    fn emitted_eval_with_comment_before_arguments_reports() {
        let emitted =
            EmittedChunk::plain("const longLocalName = 1;\nexport const r = (c) => eval/*!x*/(c);");
        for engine_reports_eval in [true, false] {
            let drafts = eval_rule_drafts_for_chunk(
                DEAD_EVAL_SOURCE,
                engine_reports_eval,
                ManglingSetting::Enabled,
                &emitted,
            );
            assert_eq!(drafts.len(), 1, "engine report: {engine_reports_eval}");
        }
        let commented_source =
            "const longLocalName = 1;\nexport const r = (c) => eval /* c */ (c);";
        let drafts = eval_rule_drafts_for_chunk(
            commented_source,
            false,
            ManglingSetting::Enabled,
            &EmittedChunk::plain("const longLocalName = 1;\neval // c\n(code);"),
        );
        assert_eq!(drafts.len(), 1);
    }

    #[test]
    fn direct_eval_offsets_lists_every_unshadowed_call() {
        let source = "eval(a); function f(eval) { eval(b); } o.eval(c); eval(d);";
        let offsets = direct_eval_offsets(source);
        assert_eq!(offsets, vec![0, source.rfind("eval(d)").unwrap()]);
        assert_eq!(direct_eval_line(source), Some((0, 0)));
    }

    #[test]
    fn eval_call_before_a_standalone_block_is_a_direct_eval() {
        // Automatic semicolon insertion ends the call, so the block that
        // follows on the next line is not a method body.
        for (source, expected) in [
            ("eval(code)\n{ cleanup(); }", (0, 0)),
            ("run();\neval(code)\n{ cleanup(); }", (1, 0)),
            (
                "function run() {\n  eval(code)\n  { cleanup(); }\n}",
                (1, 2),
            ),
            (
                "const f = () => {\n  eval(code)\n  { cleanup(); }\n};",
                (1, 2),
            ),
            ("if (ready) {\n  eval(code)\n  { cleanup(); }\n}", (1, 2)),
            (
                "class C { run() {\n  eval(code)\n  { cleanup(); }\n} }",
                (1, 2),
            ),
            (
                "const o = { run() {\n  eval(code)\n  { cleanup(); }\n} };",
                (1, 2),
            ),
            ("a = 1\neval(code)\n{ cleanup(); }", (1, 0)),
            ("outer:\neval(code)\n{ cleanup(); }", (1, 0)),
            (
                "switch (x) {\n  case 1:\n    eval(code)\n    { cleanup(); }\n}",
                (2, 4),
            ),
        ] {
            assert_eq!(
                direct_eval_line(source),
                Some((expected.0, expected.1)),
                "{source}"
            );
        }
    }

    #[test]
    fn eval_members_after_class_fields_are_declarations() {
        for source in [
            "class C { value = 1\n eval(input) { return input } }",
            "class C { value = 1; eval(input) { return input } }",
            "class C { value\n eval(input) { return input } }",
            "class C { value: number\n eval(input) { return input } }",
            "class C { value = () => 1\n eval(input) { return input } }",
            "class C { value = { a: 1 }\n eval(input) { return input } }",
            "class C { value = [1, 2]\n eval(input) { return input } }",
            "class C { value = 'text'\n eval(input) { return input } }",
            "class C { static value = 1\n static eval(input) { return input } }",
            "class C { value = 1\n async eval(input) { return input } }",
            "class C { value = 1;\n *eval(input) { yield input } }",
            "class C { value = 1\n get eval() { return 1 } }",
            "class C { value = 1\n eval(input)\n { return input } }",
            "class C {\n  value = 1\n  eval(input) {\n    return input\n  }\n}",
            "class C extends Base { value = 1\n eval(input) { return input } }",
            "const C = class { value = 1\n eval(input) { return input } };",
            "export default class { value = 1\n eval(input) { return input } }",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source}");
        }
    }

    #[test]
    fn eval_calls_in_class_field_initializers_and_keys_are_direct_eval() {
        for (source, expected) in [
            ("class C { value = eval(code) }", (0, 18)),
            ("class C { value = eval(code); }", (0, 18)),
            ("class C {\n  value =\n    eval(code)\n}", (2, 4)),
            ("class C { value = 1 + eval(code) }", (0, 22)),
            ("class C { static value = eval(code) }", (0, 25)),
            ("class C { value = () => eval(code) }", (0, 24)),
            ("class C { value = () =>\n  eval(code)\n}", (1, 2)),
            ("class C { [eval(key)]() {} }", (0, 11)),
            ("class C { [eval(key)] = 1 }", (0, 11)),
            ("class C { @eval(options) run() {} }", (0, 11)),
            ("class C { static { eval(code); } }", (0, 19)),
            (
                "class C { static {\n  eval(code)\n  { cleanup(); }\n} }",
                (1, 2),
            ),
            ("class C { static {} run() { eval(code) } }", (0, 28)),
        ] {
            assert_eq!(
                direct_eval_line(source),
                Some((expected.0, expected.1)),
                "{source}"
            );
        }
    }

    #[test]
    fn disabled_mangling_never_blames_direct_eval() {
        let source = "const longLocalName = 1;\nexport function run(code) {\n  return eval(code) \
                      + longLocalName;\n}\n";
        for engine_reports_eval in [false, true] {
            assert!(
                eval_rule_drafts_with_mangling(
                    source,
                    engine_reports_eval,
                    ManglingSetting::Disabled
                )
                .is_empty(),
                "engine_reports_eval={engine_reports_eval}"
            );
            let drafts = eval_rule_drafts_with_mangling(
                source,
                engine_reports_eval,
                ManglingSetting::Enabled,
            );
            assert_eq!(drafts.len(), 1, "engine_reports_eval={engine_reports_eval}");
            assert_eq!(drafts[0].rule, RuleId::DirectEvalBlocksMangling);
        }
    }

    #[test]
    fn eval_object_literal_members_are_declarations_but_values_are_calls() {
        for source in [
            "const o = { eval(value) { return value; } };",
            "const o = { eval(value)\n{ return value; } };",
            "const o = {\n  a: 1,\n  eval(value)\n  {\n    return value;\n  }\n};",
            "const o = { async eval(value) { return value; } };",
            "const o = { async *eval(value) { yield value; } };",
            "const o = { get eval() { return 1; } };",
            "const o = { set eval(value) { this.value = value; } };",
            "const o = { a: { eval(value) { return value; } } };",
            "const o = { a: { eval(value)\n{ return value; } } };",
            "const o = ok ? { eval(value) { return value; } } : null;",
            "run({ eval(value) { return value; } });",
            "export default { eval(value) { return value; } };",
            "function f() { return { eval(value)\n{ return value; } }; }",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source}");
        }
        for (source, expected) in [
            ("const o = { a: eval(code) };", (0, 15)),
            ("const o = { a: 1, b: eval(code) };", (0, 21)),
            ("const o = { a: { b: eval(code) } };", (0, 20)),
            ("const o = { [eval(key)]: 1 };", (0, 13)),
            ("const o = { a: eval(code)\n};", (0, 15)),
            ("const o = { ...eval(code) };", (0, 15)),
            ("run(...eval(code));", (0, 7)),
            ("const o = { run() { return eval(code); } };", (0, 27)),
        ] {
            assert_eq!(
                direct_eval_line(source),
                Some((expected.0, expected.1)),
                "{source}"
            );
        }
    }

    #[test]
    fn eval_decorated_and_typed_members_are_declarations() {
        for source in [
            "class C { @dec eval(value) { return value; } }",
            "class C { @dec()\n eval(value) { return value; } }",
            "class C { @a.b(options)\n  eval(value) { return value; } }",
            "class C { @dec static eval(value) { return value; } }",
            "class C { public eval(value: string): void { } }",
            "class C { private readonly eval(value) { } }",
            "class C { protected override eval(value) { } }",
            "class C { public async eval(value: string): Promise<void> { } }",
            "class C { eval(value: string): void; eval(value: number): void; eval(value: any) { } \
             }",
            "class C {\n  eval(value: string): void\n  eval(value: number): void\n  eval(value: \
             any) {}\n}",
            "abstract class C { abstract eval(value: string): void; }",
            "abstract class C { protected abstract eval(value: string): void }",
            "declare class C { eval(value: string): void; }",
            "declare class C { eval(value); }",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source}");
        }
    }

    #[test]
    fn eval_calls_in_statement_labels_and_arrow_bodies_stay_direct_eval() {
        for (source, expected) in [
            ("outer: eval(code)", (0, 7)),
            ("outer:\neval(code)", (1, 0)),
            ("const f = () => eval(code)\n{ cleanup(); }", (0, 16)),
            ("const f = () =>\n  eval(code)\n{ cleanup(); }", (1, 2)),
            ("const f = async () => eval(code);", (0, 22)),
            ("const f = () => { return eval(code) }", (0, 25)),
            ("x = cond ? eval(a) : { b: 1 };", (0, 11)),
            ("x = cond\n  ? eval(a)\n  : { b: 1 };", (1, 4)),
            ("x = a +\n  eval(code)\n{ cleanup(); }", (1, 2)),
            ("foo(eval(code)\n)", (0, 4)),
            ("foo(a,\n  eval(code)\n)", (1, 2)),
            ("const a = [\n  eval(code)\n];", (1, 2)),
            ("@eval(options)\nclass C {}", (0, 1)),
        ] {
            assert_eq!(
                direct_eval_line(source),
                Some((expected.0, expected.1)),
                "{source}"
            );
        }
    }

    #[test]
    fn eval_rule_reports_calls_before_blocks_and_ignores_field_adjacent_methods() {
        let call = "const longLocalName = 1;\neval(longLocalName)\n{ cleanup(); }\n";
        let drafts = eval_rule_drafts(call, false);
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].rule, RuleId::DirectEvalBlocksMangling);
        assert_eq!(drafts[0].status, EvidenceStatus::Inferred);
        assert_eq!(drafts[0].located.location.as_ref().unwrap().line, 2);
        assert_eq!(drafts[0].located.location.as_ref().unwrap().column, 1);
        let nested = "const longLocalName = 1;\nfunction run() {\n  eval(longLocalName)\n  { \
                      cleanup(); }\n}\n";
        let drafts = eval_rule_drafts(nested, false);
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].located.location.as_ref().unwrap().line, 3);

        let method = "const longLocalName = 1;\nexport class Pattern {\n  value = 1\n  \
                      eval(input) {\n    return input\n  }\n}\n";
        assert!(eval_rule_drafts(method, false).is_empty());
        let typed = "const longLocalName = 1;\nexport class Pattern {\n  eval(input: string): \
                     void\n  eval(input: number): void\n  eval(input: any) {}\n}\n";
        assert!(eval_rule_drafts(typed, false).is_empty());
        let object = "const longLocalName = 1;\nexport const pattern = {\n  eval(input)\n  {\n    \
                      return input\n  }\n};\n";
        assert!(eval_rule_drafts(object, false).is_empty());

        let field_call = "const longLocalName = 1;\nexport class Program {\n  value = 1\n  code = \
                          eval(this.source)\n}\n";
        let drafts = eval_rule_drafts(field_call, false);
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].located.location.as_ref().unwrap().line, 4);
        assert_eq!(drafts[0].located.location.as_ref().unwrap().column, 10);
    }

    #[test]
    fn eval_context_shapes_are_decided_by_the_token_stream() {
        // Declarations, private names and bodyless signatures are not calls.
        for source in [
            "class C extends mixin({}) { eval(x)\n {} }",
            "const o = { eval(value) {} };",
            "class C { #eval(value) {} run() { return this.#eval(value); } }",
            "interface X { eval(value: string): void; }",
            "type X = { eval(value: string): void }",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source}");
        }
        // A method named `class` is not a class header, so its body is code.
        assert_eq!(
            direct_eval_line("const o = { class () { eval(code); } }"),
            Some((0, 23))
        );
        // A keyword operator keeps the operand on the next line in the field
        // initializer, so the call is found.
        assert_eq!(
            direct_eval_line("class C { value = typeof\n eval(code) }"),
            Some((1, 1))
        );
    }

    #[test]
    fn a_regular_expression_after_a_less_than_operator_is_a_literal() {
        for source in [
            "const ok = value < /eval(code)/.test(text);",
            "if (a </eval(code)/.test(text)) run();",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source}");
        }
        // A call after the literal stays visible.
        let source = "const ok = value < /eval(code)/.test(text), r = eval(x);";
        assert_eq!(
            direct_eval_line(source),
            Some((0, u32::try_from(source.rfind("eval(x)").unwrap()).unwrap()))
        );
    }

    #[test]
    fn closing_tags_do_not_hide_a_direct_eval_after_them() {
        // `</b>` is not the start of a regular expression, so the call after
        // a closing or self-closing tag on the same line is found.
        for (source, expected) in [
            ("const view = <b>x</b>{eval(code)}</p>;", Some((0, 22))),
            ("const view = <b a={1} />{eval(code)}</p>;", Some((0, 25))),
            ("const view = <A<B> />{eval(code)}</p>;", Some((0, 22))),
        ] {
            assert_eq!(direct_eval_line(source), expected, "{source}");
        }
    }

    #[test]
    fn a_member_named_function_does_not_hide_a_direct_eval() {
        for (source, expected) in [
            ("const result = obj.function * eval(code);", Some((0, 30))),
            ("const result = obj?.function * eval(code);", Some((0, 31))),
        ] {
            assert_eq!(direct_eval_line(source), expected, "{source}");
        }
        // The keyword still names a declaration, which is not a call.
        assert_eq!(direct_eval_line("function * eval(code) {}"), None);
    }

    #[test]
    fn jsx_text_does_not_hide_a_direct_eval() {
        // Class-shaped JSX text does not open a class body, so the call in
        // the next expression container is reported.
        for (source, expected) in [
            (
                "const view = <div>class C <span>{eval(code)}</span></div>;",
                Some((0, 33)),
            ),
            (
                "const view = <p>class Foo extends Bar</p>;\nconst x = <i>{eval(code)}</i>;",
                Some((1, 14)),
            ),
        ] {
            assert_eq!(direct_eval_line(source), expected, "{source}");
        }
    }

    #[test]
    fn function_bodies_skip_typescript_return_annotations() {
        let source = "function run(): void { var eval = helper; } eval(code);";
        assert!(direct_eval_line(source).is_some());
        let source = "function run(): Promise<{ ok: boolean }> { var eval = helper; } eval(code);";
        assert!(direct_eval_line(source).is_some());
        let source = "function run(): () => { ok: boolean } { var eval = helper; } eval(code);";
        assert!(direct_eval_line(source).is_some());
    }

    #[test]
    fn eval_shadowing_handles_function_scan_edge_cases() {
        assert_eq!(
            direct_eval_line("obj.function(); if (condition) { var eval = helper; } eval(code);"),
            None
        );
        assert!(direct_eval_line("const make = () => function eval() {}; eval(code);").is_some());
        assert!(direct_eval_line("const instance = new function eval() {}; eval(code);").is_some());
        assert!(
            direct_eval_line("const run = (value = make()) => { var eval = helper; }; eval(code);")
                .is_some()
        );
        assert!(
            direct_eval_line("const run = (value): void => { var eval = helper; }; eval(code);")
                .is_some()
        );
        assert!(direct_eval_line("const run = λ => { var eval = helper; }; eval(code);").is_some());
        assert!(
            direct_eval_line("class C { run(): void { var eval = helper; } } eval(code);")
                .is_some()
        );
        assert!(
            direct_eval_line(
                "class C { run(): Promise<{ ok: boolean }> { var eval = helper; } } eval(code);"
            )
            .is_some()
        );
        assert_eq!(
            direct_eval_line(
                "async function run() { for await (const item of items) { var eval = helper; } \
                 eval(code); }"
            ),
            None
        );
        assert!(
            direct_eval_line("function outer() { return\nfunction eval() {}; } eval(code);")
                .is_some()
        );
    }

    #[test]
    fn switch_case_function_declarations_keep_their_declaration_scope() {
        for source in [
            "switch (value) { case 0: function eval(x) { return x; } eval(code); }",
            "switch (value) { default: function eval(x) { return x; } eval(code); }",
        ] {
            assert_eq!(direct_eval_line(source), None);
        }
    }

    #[test]
    fn var_eval_uses_the_containing_function_scope() {
        let shadowed = "function run() { if (condition) { var eval = helper; } eval(code); }";
        assert_eq!(direct_eval_line(shadowed), None);
        assert_eq!(
            direct_eval_line(&format!("{shadowed}\nfunction other() {{ eval(code); }}")),
            Some((1, 19))
        );
        assert_eq!(
            direct_eval_line("function run() { if (true) { let eval = helper; } eval(code); }"),
            Some((0, 50))
        );
        assert_eq!(
            direct_eval_line("function run() { if (true) { const eval = helper; } eval(code); }"),
            Some((0, 52))
        );
        assert_eq!(direct_eval_line("{ var eval = helper; } eval(code);"), None);
        for source in [
            "const run = () => { var eval = helper; eval(code); }; eval(code);",
            "const run = value => { var eval = helper; eval(code); }; eval(code);",
        ] {
            assert_eq!(
                direct_eval_line(source),
                Some((
                    0,
                    u32::try_from(source.rfind("eval(code)").unwrap()).unwrap()
                ))
            );
        }
        let source = "function run() { if (condition) { var eval = helper; } const longLocalName \
                      = 1; return eval(code); }";
        assert!(eval_rule_drafts(source, false).is_empty());
        let reported = eval_rule_drafts(source, true);
        assert_eq!(reported.len(), 1);
        assert_eq!(reported[0].status, EvidenceStatus::Confirmed);
    }

    #[test]
    fn finds_direct_eval_but_not_member_eval() {
        assert_eq!(
            direct_eval_line("const a = 1;\n  return eval(x);"),
            Some((1, 9))
        );
        assert_eq!(direct_eval_line("obj.eval(x); evaluate(y);"), None);
        assert_eq!(
            direct_eval_line(
                "const a = \"eval(x)\"; // eval(y)\n/* eval(z) */ b = `eval(t)`; c = /eval(r)/;"
            ),
            None
        );
    }

    #[test]
    fn collects_declared_names_of_four_or_more_characters() {
        let names =
            declared_names("const ab = 1; let local = 2; var other = 3; function helper() {}");
        assert_eq!(names, vec!["local".to_owned(), "other".to_owned()]);
        let names = declared_names(
            "const real = 1; const text = \"const retainedLiteralName\"; // let commented",
        );
        assert_eq!(names, vec!["real".to_owned(), "text".to_owned()]);
        let names = declared_names(
            "const x = f(1, 2), descriptiveName = 1;\nlet { alpha, beta: renamedBeta = 3, \
             ...restOfIt } = o, [first, , second] = a;",
        );
        assert_eq!(
            names,
            [
                "alpha",
                "descriptiveName",
                "first",
                "renamedBeta",
                "restOfIt",
                "second"
            ]
            .map(str::to_owned)
            .to_vec()
        );
    }

    #[test]
    fn configuration_only_locations_do_not_reduce_coverage() {
        let dir = tempfile::tempdir().unwrap();
        let config = dir.path().join("rspack.config.js");
        std::fs::write(&config, "module.exports = {};").unwrap();
        let collection = crate::collect::Collection {
            bundler: "rspack".to_owned(),
            config_file: Some(config.to_string_lossy().into_owned()),
            minify_enabled: false,
            config_summary: BTreeMap::from([(
                "optimization.minimize".to_owned(),
                "false".to_owned(),
            )]),
            ..crate::collect::Collection::default()
        };
        let mut context = Context::new(
            &collection,
            dir.path().to_path_buf(),
            "app".to_owned(),
            Vec::new(),
            crate::sources::SourceStore::new(true),
        );
        let mut drafts = Vec::new();
        configuration_disabled(
            &mut context,
            &mut drafts,
            &DisabledOptimization {
                rule: RuleId::RspackSideEffectsDisabled,
                title: "Side effects are disabled",
                option: "optimization.sideEffects",
                summary: "The option is disabled.",
                direction: "Enable the option.",
                safety: "Verify side effects before changing it.",
            },
        );
        minification_disabled(&mut context, &mut drafts);
        assert_eq!(drafts.len(), 2);
        assert!(context.sources.unverified.is_empty());
    }
}
