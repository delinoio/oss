//! Rules shared by both engines: duplicate packages and minifier blockers.

use std::{
    collections::{BTreeMap, BTreeSet},
    path::Path,
};

use regex::Regex;

use super::{Context, DiagnosticDraft, RuleId};
use crate::{
    collect::CollectedModule,
    model::{Condition, ConditionOutcome, Evidence, EvidenceStatus},
};

pub fn run(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    duplicate_packages(context, drafts);
    if context.is_cancelled() {
        return;
    }
    // Without a minifier, retained `process.env` reads are expected; the
    // confirmed minification-disabled finding explains them instead.
    if context.collection.minify_enabled {
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

/// True for the four ECMAScript line terminators: LF, CR, U+2028 and U+2029.
fn is_line_terminator(character: char) -> bool {
    matches!(character, '\n' | '\r' | '\u{2028}' | '\u{2029}')
}

/// Byte length of the ECMAScript line terminator that starts at `index`, or 0
/// when none does: 1 for LF and CR, 3 for U+2028 and U+2029 (`E2 80 A8` and
/// `E2 80 A9`). Every byte-level scan uses this with [`is_line_terminator`] so
/// that all consumers agree on what ends a line.
fn line_terminator_len(bytes: &[u8], index: usize) -> usize {
    match bytes.get(index) {
        Some(b'\n' | b'\r') => 1,
        Some(0xe2)
            if bytes.get(index + 1) == Some(&0x80)
                && matches!(bytes.get(index + 2), Some(0xa8 | 0xa9)) =>
        {
            3
        }
        _ => 0,
    }
}

/// Whether a `/` at `start` begins a regular expression literal, given the
/// last significant byte before it and the non-code `ranges` found so far.
///
/// After an alphabetic byte the previous token is decided on the code with
/// comments removed, so a keyword such as `else` still counts when a comment
/// sits between it and the slash.
fn regex_allowed(
    code: &str,
    ranges: &[(usize, usize)],
    previous: Option<u8>,
    start: usize,
) -> bool {
    match previous {
        None => true,
        Some(byte) if b"(,=:[!&|?{};+-*%<>~^".contains(&byte) => true,
        Some(byte) if byte.is_ascii_alphabetic() => {
            let head = strip_comments_for_prefix(code, start, ranges);
            [
                "return", "typeof", "case", "do", "else", "in", "of", "void", "yield", "delete",
                "throw", "new",
            ]
            .iter()
            .any(|keyword| {
                head.ends_with(keyword)
                    && !head[..head.len() - keyword.len()].ends_with(is_identifier_char)
            })
        }
        _ => false,
    }
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
    // Scans a quoted string or template text starting after the opener.
    let scan_until = |mut position: usize, terminator: u8, template: bool| -> (usize, bool) {
        while position < bytes.len() {
            match bytes[position] {
                // A backslash continues the string over a line terminator; CRLF
                // counts as a single terminator.
                b'\\' if bytes[position + 1..].starts_with(b"\r\n") => position += 3,
                b'\\' => position += 2,
                byte if byte == terminator => return (position + 1, false),
                b'$' if template && bytes.get(position + 1) == Some(&b'{') => {
                    return (position + 2, true);
                }
                b'\n' | b'\r' if !template => return (position, false),
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
                // A line comment stops before any ECMAScript line terminator:
                // LF, CR, U+2028 or U+2029. A lone CR ends it just as CRLF
                // does.
                let end = code[index..]
                    .find(is_line_terminator)
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
            b'/' if regex_allowed(code, &ranges, previous_significant, index) => {
                let mut position = index + 1;
                let mut in_class = false;
                // True when a multi-byte line terminator ended the literal. It
                // stays outside the range so range ends fall on char
                // boundaries.
                let mut stopped_before = false;
                while position < bytes.len() {
                    match bytes[position] {
                        b'\\' => position += 1,
                        b'[' => in_class = true,
                        b']' => in_class = false,
                        b'/' if !in_class => break,
                        _ if line_terminator_len(bytes, position) > 0 => {
                            stopped_before = line_terminator_len(bytes, position) > 1;
                            break;
                        }
                        _ => {}
                    }
                    position += 1;
                }
                let end = if stopped_before {
                    position
                } else {
                    (position + 1).min(bytes.len())
                };
                ranges.push((index, end));
                index = end;
                previous_significant = Some(b'"');
            }
            // U+2028 and U+2029 are whitespace, not a token that decides
            // whether a following `/` starts a regular expression.
            _ if line_terminator_len(bytes, index) == 3 => {
                index += 3;
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

fn in_ranges(ranges: &[(usize, usize)], offset: usize) -> bool {
    let index = ranges.partition_point(|(start, _)| *start <= offset);
    index > 0 && offset < ranges[index - 1].1
}

/// The next byte that is neither inside an ignored range nor whitespace,
/// including every ECMAScript line terminator, and its position.
fn next_code_byte(bytes: &[u8], start: usize, ignored: &[(usize, usize)]) -> Option<(usize, u8)> {
    let mut index = start;
    while index < bytes.len() {
        let terminator = line_terminator_len(bytes, index);
        if in_ranges(ignored, index) || bytes[index].is_ascii_whitespace() {
            index += 1;
        } else if terminator > 0 {
            index += terminator;
        } else {
            return Some((index, bytes[index]));
        }
    }
    None
}

/// Whether the line terminator that ends just before `after` ends a statement:
/// the next code token cannot continue the expression.
fn asi_terminates(bytes: &[u8], after: usize, ignored: &[(usize, usize)]) -> bool {
    let Some((position, next)) = next_code_byte(bytes, after, ignored) else {
        return true;
    };
    let repeated = |byte: u8| {
        next_code_byte(bytes, position + 1, ignored).map(|(_, following)| following) != Some(byte)
    };
    match next {
        b'+' => repeated(b'+'),
        b'-' => repeated(b'-'),
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

        let terminator = line_terminator_len(bytes, index);
        if terminator > 0 && asi_terminates(bytes, index + terminator, ignored) {
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
                if next_code_byte(bytes, index + 2, ignored).map(|(_, next)| next) != Some(b'{') {
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

/// Returns the code before `index` with trailing whitespace and comments
/// removed, so a control keyword separated from its `(` by comments is still
/// the last token of the prefix.
///
/// A block comment must end exactly at the trimmed end. A line comment ends at
/// its line terminator (see [`is_line_terminator`]), so trimming whitespace may
/// stop inside the comment (trailing spaces); the remainder after the trimmed
/// end is then only whitespace that lies before `index`. String, template and
/// regular expression ranges are never skipped.
///
/// Every consumer that needs the previous token (regular expression context,
/// control keywords, member names) reads it from this prefix.
fn strip_comments_for_prefix<'a>(
    code: &'a str,
    index: usize,
    ignored: &[(usize, usize)],
) -> &'a str {
    let mut end = code[..index].trim_end().len();
    loop {
        let Some(range_index) = ignored
            .partition_point(|(start, _)| *start < end)
            .checked_sub(1)
        else {
            return &code[..end];
        };
        let (start, stop) = ignored[range_index];
        let comment = &code[start..stop];
        let is_block_comment = comment.starts_with("/*") && stop == end;
        let is_line_comment = comment.starts_with("//") && stop >= end && stop <= index;
        if !is_block_comment && !is_line_comment {
            return &code[..end];
        }
        end = code[..start].trim_end().len();
    }
}

/// True for a character that can continue a JavaScript identifier, including
/// non-ASCII letters and digits, `_`, `$` and the zero-width joiners.
fn is_identifier_char(character: char) -> bool {
    character.is_alphanumeric() || matches!(character, '_' | '$' | '\u{200c}' | '\u{200d}')
}

/// True when `prefix` (the code before an opening parenthesis, as returned by
/// [`strip_comments_for_prefix`]) ends with `keyword` used as a control
/// keyword.
///
/// Rejected: an identifier that merely ends in the keyword (`notif`, `$if`,
/// `éif`), and a property, method or private name written after a member
/// access, even when whitespace or comments separate the dot from the name
/// (`obj.if`, `obj?.while`, `obj. /* c */ for`, `this.#if`).
fn ends_with_control_keyword(
    code: &str,
    prefix: &str,
    keyword: &str,
    ignored: &[(usize, usize)],
) -> bool {
    if !prefix.ends_with(keyword) {
        return false;
    }
    let start = prefix.len() - keyword.len();
    match code[..start].chars().next_back() {
        Some(character) if is_identifier_char(character) || character == '#' => false,
        _ => !strip_comments_for_prefix(code, start, ignored).ends_with('.'),
    }
}

fn scope_context(
    code: &str,
    index: usize,
    opener: u8,
    parent: ScopeContext,
    ignored: &[(usize, usize)],
) -> ScopeContext {
    let prefix = strip_comments_for_prefix(code, index, ignored);
    let last = prefix.chars().next_back();
    if opener == b'[' {
        // A bracket after an expression is a computed member access and keeps
        // the surrounding condition. A bracket after an operator starts an
        // array literal, whose contents do not form a guard by themselves.
        if last.is_some_and(|character| {
            is_identifier_char(character)
                || matches!(character, ']' | ')' | '}' | '\'' | '"' | '`' | '.')
        }) {
            return parent;
        }
        return ScopeContext::Invalid;
    }
    if opener != b'(' {
        return ScopeContext::Invalid;
    }
    if ends_with_control_keyword(code, prefix, "for", ignored) {
        return ScopeContext::For(ForClause::Initializer);
    }
    for (keyword, guard) in [
        ("if", EnvGuard::If),
        ("while", EnvGuard::While),
        ("switch", EnvGuard::Switch),
    ] {
        if ends_with_control_keyword(code, prefix, keyword, ignored) {
            return ScopeContext::Guard(guard);
        }
    }
    if last.is_some_and(|character| {
        is_identifier_char(character) || matches!(character, ']' | ')' | '.')
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
        summary: format!(
            "{option} is {value}, so dead-code elimination, name mangling and other minifier \
             optimizations do not run."
        ),
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
            "resolved bundler configuration",
            EvidenceStatus::Confirmed,
            format!("{option}: {value}"),
        )],
        direction: format!("Enable {option} for production builds."),
        safety: "Minified output changes function and class names; code that depends on \
                 Function.prototype.name needs keep-names options."
            .to_owned(),
        dependency: false,
        export_usage: None,
    });
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
fn eval_shadow_scopes(source: &str, ignored: &[(usize, usize)]) -> Vec<(usize, usize)> {
    let mut masked = source.as_bytes().to_vec();
    for (start, end) in ignored {
        for byte in &mut masked[*start..(*end).min(source.len())] {
            if *byte != b'\n' {
                *byte = b' ';
            }
        }
    }
    let code = String::from_utf8_lossy(&masked).into_owned();
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

fn direct_eval_line(source: &str) -> Option<(u32, u32)> {
    let pattern = Regex::new(r"(^|[^.\w$])eval\s*\(").expect("static regex");
    // `eval(` inside strings, templates, comments or regular expressions is
    // not a call.
    let ignored = non_code_ranges(source);
    // A binding named `eval` makes calls inside its scope ordinary calls that
    // cannot observe the surrounding scope; shadowing is decided per call.
    let shadows = eval_shadow_scopes(source, &ignored);
    let start = pattern.find_iter(source).find_map(|found| {
        let start = found.start() + found.as_str().find("eval")?;
        let shadowed = shadows
            .iter()
            .any(|(begin, end)| (*begin..*end).contains(&start));
        (!in_ranges(&ignored, start) && !shadowed).then_some(start)
    })?;
    let (line, column) = line_and_column(source, start);
    Some((u32::try_from(line).ok()?, u32::try_from(column).ok()?))
}

/// Zero-based line and byte column of `offset`, counting every ECMAScript
/// line terminator (CRLF is one terminator) so the position agrees with the
/// scanners that end comments and statements at those terminators.
fn line_and_column(source: &str, offset: usize) -> (usize, usize) {
    let bytes = source.as_bytes();
    let mut line = 0usize;
    let mut line_start = 0usize;
    let mut index = 0usize;
    while index < offset {
        let length = line_terminator_len(bytes, index);
        if length == 0 {
            index += 1;
            continue;
        }
        // A CR directly followed by LF is a single terminator.
        let length = if bytes[index] == b'\r' && bytes.get(index + 1) == Some(&b'\n') {
            2
        } else {
            length
        };
        index += length;
        if index <= offset {
            line += 1;
            line_start = index;
        }
    }
    (line, offset - line_start)
}

fn direct_eval_mangling(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let collection = context.collection;
    if !collection.minify_enabled || !collection.default_minifier {
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
        let names = declared_names(&source);
        let mut retained: BTreeSet<String> = BTreeSet::new();
        let mut chunk_files = Vec::new();
        for file in &module.chunks {
            let Some(index) = context.chunk_index(file) else {
                continue;
            };
            chunk_files.push(file.clone());
            for name in &names {
                let pattern = Regex::new(&format!(r"(^|[^\w$]){}([^\w$]|$)", regex::escape(name)))
                    .expect("escaped regex");
                if pattern.is_match(&context.chunks[index].code) {
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
        let located = context.module_location(module, line, column);
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
        if !engine_reported {
            evidence.push(Evidence::new(
                "bundlewhy source scan",
                EvidenceStatus::Inferred,
                "Direct eval found in the source; the engine did not report it.",
            ));
        }
        evidence.push(Evidence::new(
            "generated chunk text",
            EvidenceStatus::Confirmed,
            format!(
                "Declared names still present in {}: {}",
                chunk_files.join(", "),
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
                        "Direct eval reported by the engine"
                    } else {
                        "Direct eval found in the source"
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
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        let source_path = root.join("src/main.js");
        let build_dir = root.join("build");
        std::fs::create_dir_all(source_path.parent().unwrap()).unwrap();
        std::fs::create_dir_all(&build_dir).unwrap();
        std::fs::write(&source_path, source).unwrap();
        std::fs::write(build_dir.join("main.js"), "const longLocalName = 1;").unwrap();
        let module_id = source_path.to_string_lossy().into_owned();
        let collection = crate::collect::Collection {
            default_minifier: true,
            minify_enabled: true,
            logs: if engine_reports_eval {
                vec![crate::collect::EngineLog {
                    code: Some("EVAL".to_owned()),
                    id: Some(module_id.clone()),
                    ..crate::collect::EngineLog::default()
                }]
            } else {
                Vec::new()
            },
            modules: vec![CollectedModule {
                id: module_id,
                path: Some(source_path.to_string_lossy().into_owned()),
                included: true,
                chunks: vec!["main.js".to_owned()],
                ..CollectedModule::default()
            }],
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
    fn comments_between_a_keyword_and_its_condition_keep_the_guard() {
        let guard = |code: &str, read: &str| {
            let start = code.find(read).unwrap();
            env_guard(code, start, start + read.len(), &non_code_ranges(code))
                .map(EnvGuard::evidence)
        };
        let read = "process.env.FLAG";
        let vite_read = "({}).FLAG";
        for (keyword, body, evidence) in [
            (
                "if",
                "{read}) run()",
                "the retained read is in an if condition",
            ),
            (
                "while",
                "{read}) run()",
                "the retained read is in a while condition",
            ),
            (
                "switch",
                "{read}) {default: run()}",
                "the retained read is in a switch condition",
            ),
            (
                "for",
                ";{read};) run()",
                "the retained read is in a for condition",
            ),
        ] {
            for read in [read, vite_read] {
                let body = body.replace("{read}", read);
                let plain = format!("{keyword} ({body}");
                assert_eq!(guard(&plain, read), Some(evidence), "{plain}");
                for separator in [
                    "/* c */",
                    "/*!license*/\n",
                    "// c\n",
                    "// c\r\n",
                    "//\n",
                    "// c   \n",
                    "// c \t\r\n",
                    "// one\n// two\n",
                    "// one\n/* two */ // three\n",
                    "\n// c\n\n  ",
                    "// if (x)\n",
                ] {
                    let code = format!("{keyword} {separator}({body}");
                    assert_eq!(guard(&code, read), Some(evidence), "{code:?}");
                    let code = format!("{keyword}{separator}({body}");
                    assert_eq!(guard(&code, read), Some(evidence), "{code:?}");
                }
            }
        }
        for code in [
            "run // c\n(process.env.FLAG)",
            "run // c\r\n(process.env.FLAG)",
            "obj.if // c\n(process.env.FLAG)",
            "obj.while /* c */ (process.env.FLAG)",
            "notif // c\n(process.env.FLAG)",
            "if // c\n(process.env.FLAG, ready) run()",
            "for // c\n(process.env.FLAG;;) work()",
            "for // c\n(;;process.env.FLAG) work()",
            "console.log // c\n(process.env.FLAG)",
            "\"if\" // c\n(process.env.FLAG)",
            "`if` // c\n(process.env.FLAG)",
        ] {
            assert_eq!(guard(code, "process.env.FLAG"), None, "{code:?}");
        }
        // A string ending in a keyword is not a keyword.
        assert_eq!(
            guard("x = 'if' // c\n(process.env.FLAG)", "process.env.FLAG"),
            None
        );
    }

    const LINE_TERMINATORS: [&str; 5] = ["\n", "\r", "\r\n", "\u{2028}", "\u{2029}"];

    #[test]
    fn line_comments_end_at_every_ecmascript_line_terminator() {
        for terminator in LINE_TERMINATORS {
            // The comment range stops before the terminator, so the code that
            // follows a lone CR, U+2028 or U+2029 is scanned as code.
            let code = format!("a // c{terminator}b");
            let ranges = non_code_ranges(&code);
            assert_eq!(ranges, vec![(2, 6)], "{code:?}");
            assert!(code.is_char_boundary(ranges[0].1), "{code:?}");

            let read = "process.env.FLAG";
            let code = format!("// c{terminator}{read} && run()");
            let ignored = non_code_ranges(&code);
            assert!(!in_ranges(&ignored, code.find(read).unwrap()), "{code:?}");
            assert_eq!(
                env_guard(
                    &code,
                    code.find(read).unwrap(),
                    code.find(read).unwrap() + read.len(),
                    &ignored
                )
                .map(EnvGuard::evidence),
                Some("the retained read controls a && short-circuit branch"),
                "{code:?}"
            );

            // A block comment may contain any terminator and still ends at
            // `*/`.
            let code = format!("/* c{terminator}d */ {read}");
            let ignored = non_code_ranges(&code);
            assert_eq!(ignored.len(), 1, "{code:?}");
            assert!(!in_ranges(&ignored, code.find(read).unwrap()), "{code:?}");

            // A quoted string or regular expression ends at a line terminator
            // that is not escaped, so the next line is code again. Strings may
            // contain U+2028 and U+2029; regular expressions may not.
            // U+2028 and U+2029 are legal inside string literals (ES2019).
            let openers: &[&str] =
                if terminator.starts_with('\u{2028}') || terminator.starts_with('\u{2029}') {
                    &["x = /a"]
                } else {
                    &["x = 'a", "x = \"a", "x = /a"]
                };
            for opener in openers {
                let code = format!("{opener}{terminator}{read}");
                let ignored = non_code_ranges(&code);
                assert!(!in_ranges(&ignored, code.find(read).unwrap()), "{code:?}");
                assert!(
                    ignored.iter().all(|(_, end)| code.is_char_boundary(*end)),
                    "{code:?}"
                );
            }

            // A regular expression after a line terminator is still a regular
            // expression: the terminator is whitespace, not a division operand.
            let code = format!("x = // c{terminator}/a|b/.test(y)");
            let ignored = non_code_ranges(&code);
            assert!(in_ranges(&ignored, code.find("a|b").unwrap()), "{code:?}");
        }
        // A backslash continues a string over CRLF as a single terminator.
        let code = "x = 'a\\\r\nb'; process.env.FLAG";
        let ignored = non_code_ranges(code);
        assert!(in_ranges(&ignored, code.find('b').unwrap()), "{code:?}");
        assert!(
            !in_ranges(&ignored, code.find("process").unwrap()),
            "{code:?}"
        );
    }

    #[test]
    fn every_line_terminator_after_a_comment_keeps_the_guard() {
        let guard = |code: &str, read: &str| {
            let start = code.find(read).unwrap();
            env_guard(code, start, start + read.len(), &non_code_ranges(code))
                .map(EnvGuard::evidence)
        };
        let read = "process.env.FLAG";
        for terminator in LINE_TERMINATORS {
            for (keyword, body, evidence) in [
                ("if", ") run()", "the retained read is in an if condition"),
                (
                    "while",
                    ") run()",
                    "the retained read is in a while condition",
                ),
                (
                    "switch",
                    ") {default: run()}",
                    "the retained read is in a switch condition",
                ),
                (
                    "for",
                    ";;) work()",
                    "the retained read is in a for condition",
                ),
            ] {
                let body = if keyword == "for" {
                    format!(";{read};) work()")
                } else {
                    format!("{read}{body}")
                };
                for comment in ["// c", "// c  ", "//", "// c \t", "// if (x)"] {
                    for gap in [" ", ""] {
                        let code = format!("{keyword}{gap}{comment}{terminator}({body}");
                        assert_eq!(guard(&code, read), Some(evidence), "{code:?}");
                        let code = format!(
                            "{keyword}{gap}/* a{terminator}b \
                             */{gap}{comment}{terminator}{gap}({body}"
                        );
                        assert_eq!(guard(&code, read), Some(evidence), "{code:?}");
                    }
                }
                let code = format!("{keyword} /* a{terminator}b */ ({body}");
                assert_eq!(guard(&code, read), Some(evidence), "{code:?}");
            }
            // A comment before a non-keyword never creates a guard.
            for code in [
                format!("run // c{terminator}({read})"),
                format!("run /* a{terminator}b */ ({read})"),
                format!("x = 1 // c{terminator}({read})"),
            ] {
                assert_eq!(guard(&code, read), None, "{code:?}");
            }
        }
    }

    #[test]
    fn every_line_terminator_is_an_asi_boundary() {
        let read = "process.env.FLAG";
        for terminator in LINE_TERMINATORS {
            // A statement boundary before `ready` ends the read, so the later
            // `&&` belongs to another expression.
            for code in [
                format!("// c{terminator}{read}{terminator}ready&&run()"),
                format!("/* c */ {read}{terminator}ready&&run()"),
                format!("{read}{terminator}// c{terminator}ready&&run()"),
                format!("x = 1; {read}{terminator} {terminator}ready||run()"),
            ] {
                assert_eq!(keyword_guard(&code, read), None, "{code:?}");
            }
            // A line that continues the expression keeps the read guarded,
            // however many terminators and comments come first.
            for code in [
                format!("{read}{terminator}&&run()"),
                format!("{read}{terminator}// c{terminator}{terminator}&&run()"),
                format!("{read}{terminator}/* c */ ||run()"),
                format!("// c{terminator}{read}{terminator}?run():stop()"),
            ] {
                assert!(keyword_guard(&code, read).is_some(), "{code:?}");
            }
        }
        // The scan resumes after a three-byte terminator, not inside it.
        assert_eq!(
            line_terminator_len("\u{2028}".as_bytes(), 0),
            3,
            "U+2028 is three bytes"
        );
        assert_eq!(line_terminator_len("a\r".as_bytes(), 1), 1);
        assert_eq!(line_terminator_len("\u{2027}".as_bytes(), 0), 0);
    }

    #[test]
    fn regex_context_ignores_comments_before_the_slash() {
        let read = "process.env.FLAG";
        let in_regex = |code: &str| in_ranges(&non_code_ranges(code), code.find(read).unwrap());
        for terminator in LINE_TERMINATORS {
            for keyword in [
                "return", "typeof", "else", "do", "void", "yield", "delete", "throw", "new", "in",
                "of",
            ] {
                for comment in ["// c", "//", "// c  ", "/* c */ // d", "/* a\nb */"] {
                    for gap in [" ", ""] {
                        let code =
                            format!("{keyword}{gap}{comment}{terminator}/{read}/.test(x)&&run()");
                        assert!(in_regex(&code), "{code:?}");
                    }
                }
            }
            // The slash after `if(x)foo();else` from a real bundle shape.
            let code = format!("if(x)foo();else // c{terminator}/{read}/.test(x)&&run()");
            assert!(in_regex(&code), "{code:?}");
            assert_eq!(keyword_guard_in_code(&code, read), None, "{code:?}");
            // An identifier that merely ends in a keyword before a comment is
            // an operand, so the slash divides.
            for name in ["notreturn", "$do", "x_in"] {
                let code = format!("{name} // c{terminator}/{read}/ 2");
                assert!(!in_regex(&code), "{code:?}");
            }
        }
    }

    /// Like `keyword_guard`, but skips reads that sit in non-code text.
    fn keyword_guard_in_code(code: &str, read: &str) -> Option<&'static str> {
        let start = code.find(read).unwrap();
        let ignored = non_code_ranges(code);
        if in_ranges(&ignored, start) {
            return None;
        }
        env_guard(code, start, start + read.len(), &ignored).map(EnvGuard::evidence)
    }

    fn keyword_guard(code: &str, read: &str) -> Option<&'static str> {
        let start = code.find(read).unwrap();
        env_guard(code, start, start + read.len(), &non_code_ranges(code)).map(EnvGuard::evidence)
    }

    const CONTROL_KEYWORDS: [(&str, &str); 4] = [
        ("if", "the retained read is in an if condition"),
        ("while", "the retained read is in a while condition"),
        ("switch", "the retained read is in a switch condition"),
        ("for", "the retained read is in a for condition"),
    ];

    /// The argument list that follows a keyword, with the read in a position
    /// that is a guard for that keyword (`for` guards its test clause).
    fn keyword_arguments(keyword: &str, read: &str) -> String {
        if keyword == "for" {
            format!(";{read};) work()")
        } else {
            format!("{read}) work()")
        }
    }

    #[test]
    fn keyword_named_members_are_never_guards() {
        let read = "process.env.FLAG";
        for (keyword, _) in CONTROL_KEYWORDS {
            let arguments = keyword_arguments(keyword, read);
            let mut members = vec![
                format!("obj.{keyword}("),
                format!("obj?.{keyword}("),
                format!("obj. {keyword}("),
                format!("obj .{keyword}("),
                format!("obj./* c */{keyword}("),
                format!("obj. /* c */ {keyword} /* d */ ("),
                format!("obj.{keyword} // c\n("),
                format!("obj.{keyword} /* c */ ("),
                format!("obj?. /* c */ // d\n {keyword}("),
                format!("this.#{keyword}("),
                format!("this.#{keyword} /* c */ ("),
                format!("a.b.c.{keyword}("),
                format!("a[0].{keyword}("),
                format!("a().{keyword}("),
                format!("a.\n{keyword}("),
            ];
            for terminator in LINE_TERMINATORS {
                members.push(format!("obj.{terminator}{keyword}("));
                members.push(format!("obj. // c{terminator}{keyword}("));
                members.push(format!("obj. /* a{terminator}b */ {keyword}("));
                members.push(format!("obj.{keyword} // c{terminator}("));
                members.push(format!(
                    "obj?.// c{terminator}/* d */{terminator}{keyword}("
                ));
            }
            for member in members {
                let code = format!("{member}{arguments}");
                assert_eq!(keyword_guard(&code, read), None, "{code:?}");
            }
        }
    }

    #[test]
    fn identifiers_ending_in_a_keyword_are_never_guards() {
        let read = "process.env.FLAG";
        for (keyword, _) in CONTROL_KEYWORDS {
            let arguments = keyword_arguments(keyword, read);
            for name in [
                "not",
                "_",
                "$",
                "x1",
                "\u{e9}",
                "\u{4e2d}",
                "a\u{200d}",
                "\u{3b1}\u{3b2}",
            ] {
                for gap in ["", " ", " /* c */ ", " // c\n"] {
                    let code = format!("{name}{keyword}{gap}({arguments}");
                    assert_eq!(keyword_guard(&code, read), None, "{code:?}");
                }
            }
            // A keyword inside a longer identifier is not the identifier's end.
            let code = format!("{keyword}Value({arguments}");
            assert_eq!(keyword_guard(&code, read), None, "{code:?}");
        }
    }

    #[test]
    fn keywords_after_statement_boundaries_and_comments_stay_guards() {
        let read = "process.env.FLAG";
        for (keyword, evidence) in CONTROL_KEYWORDS {
            let arguments = keyword_arguments(keyword, read);
            let mut prefixes = vec![
                String::new(),
                "run();".to_string(),
                "{".to_string(),
                "{}".to_string(),
                "run()\n".to_string(),
                "x = 1; ".to_string(),
                "else ".to_string(),
                "do ".to_string(),
                // Dots that belong to comments or numbers never make a member.
                "a; /* end. */ ".to_string(),
                "a; // end.\n".to_string(),
                "a; // end.\n/* more. */\n".to_string(),
                "x = 1.5;".to_string(),
                "x = a.b;".to_string(),
                "x = '.';".to_string(),
                "x = \".\";".to_string(),
                "x = `.`;".to_string(),
                "x = /./;".to_string(),
                "run() // see obj.\n".to_string(),
            ];
            for terminator in LINE_TERMINATORS {
                prefixes.push(format!("a; // end.{terminator}"));
                prefixes.push(format!("a; // end.{terminator}// more.{terminator}"));
                prefixes.push(format!("a.b; /* end. */{terminator}"));
            }
            for prefix in prefixes {
                for gap in ["", " ", " /* c */ ", " // c\n"] {
                    let code = format!("{prefix}{keyword}{gap}({arguments}");
                    assert_eq!(keyword_guard(&code, read), Some(evidence), "{code:?}");
                }
            }
        }
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
    fn direct_eval_positions_count_every_line_terminator() {
        for terminator in LINE_TERMINATORS {
            let source = format!("// note{terminator}eval(code)");
            assert_eq!(direct_eval_line(&source), Some((1, 0)), "{source:?}");
            let source = format!("a{terminator}b{terminator}  return eval(x)");
            assert_eq!(direct_eval_line(&source), Some((2, 9)), "{source:?}");
            let source = format!("{terminator}{terminator}eval(x)");
            assert_eq!(direct_eval_line(&source), Some((2, 0)), "{source:?}");
        }
        // CRLF is one terminator; a CR and an LF that are not adjacent are
        // two.
        assert_eq!(line_and_column("a\r\nb", 3), (1, 0));
        assert_eq!(line_and_column("a\r\r\nb", 4), (2, 0));
        assert_eq!(line_and_column("a\n\rb", 3), (2, 0));
        assert_eq!(line_and_column("a\u{2028}b", 4), (1, 0));
        // The column counts bytes after the last terminator.
        assert_eq!(line_and_column("\u{2029}\u{e9}x", 5), (1, 2));
        assert_eq!(line_and_column("abc", 2), (0, 2));
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
