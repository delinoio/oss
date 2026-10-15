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
    /// How the opener of this scope was classified. See `ContainerKind`.
    kind: ContainerKind,
    /// Byte offset of the opener; the root scope uses 0.
    open: usize,
    /// A `typeof`, `void` or `delete` operand is being scanned. Reads that
    /// start in it are not values of the guard (see `read_propagation_rows`).
    discard: bool,
    /// A `return` statement is being scanned in this scope.
    returning: bool,
    /// Reads returned by `return` statements of this function body.
    returned: Vec<usize>,
    /// Reads returned by function objects that end this scope's expression.
    function_returns: Vec<usize>,
    /// Number of top-level commas, which is the index of the last element of
    /// an array literal.
    elements: usize,
    /// An array literal with a spread element has unknown element indexes.
    spread: bool,
    /// Key of the last property of an object literal: byte range of its name.
    last_key: Option<(usize, usize)>,
}

impl ScanScope {
    fn new(parent: usize, context: ScopeContext, kind: ContainerKind, open: usize) -> Self {
        Self {
            parent,
            context,
            conditional_questions: 0,
            pending_reads: Vec::new(),
            conditional_pending: Vec::new(),
            arrow_expression: false,
            kind,
            open,
            discard: false,
            returning: false,
            returned: Vec::new(),
            function_returns: Vec::new(),
            elements: 0,
            spread: false,
            last_key: None,
        }
    }
}

/// Classification of a `(`, `[` or `{` opener, decided by `preceding_token`
/// and documented as one table in `docs/crates-bundlewhy-foundation.md`.
/// Call arguments, array and object literals and blocks are value containers:
/// a closed value container evaluates to a value, so reads that were only
/// values inside it do not inherit an operator that follows it.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum ContainerKind {
    /// Parenthesized sub-expression; inherits the surrounding context.
    Grouping,
    /// Parentheses of an `if`, `while`, `for` or `switch` head.
    StatementHead,
    /// Computed member access; inherits the surrounding context.
    ComputedMember,
    CallArguments,
    /// Parenthesized operand of `typeof`, `void` or `delete`.
    Discarded,
    ArrayLiteral,
    ObjectLiteral,
    /// Function, class or statement block.
    Block,
}

impl ContainerKind {
    fn is_value_container(self) -> bool {
        matches!(
            self,
            Self::CallArguments
                | Self::Discarded
                | Self::ArrayLiteral
                | Self::ObjectLiteral
                | Self::Block
        )
    }
}

/// What a block opener starts, decided from the tokens before it.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum BodyKind {
    /// Body of a `function` expression or declaration.
    Function,
    /// Block body of an arrow function.
    Arrow,
    /// Statement block (`if`, `else`, loops, `try`, a bare block).
    Statement,
    /// Class bodies, methods and anything else that is never invoked in place.
    Other,
}

/// Tokens that are keywords for the scanner's own bookkeeping.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum WordEvent {
    /// `typeof`, `void` or `delete`: the operand is not a value of the guard.
    Discard,
    /// `return`: the expression is the value of the enclosing function.
    Return,
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
    let mut scopes = vec![ScanScope::new(
        0,
        ScopeContext::Neutral,
        ContainerKind::Grouping,
        0,
    )];
    let mut current_scope = 0;
    let mut next_start = 0;
    let mut next_end = 0;
    let mut ignored_index = 0;
    // Closing bracket most recently seen and how its scope was classified.
    let mut last_close: Option<(usize, ContainerKind)> = None;

    for index in 0..bytes.len() {
        while next_start < reads.len() && reads[next_start].0 == index {
            contexts[next_start] = scopes[current_scope].context;
            next_start += 1;
        }
        while next_end < reads.len() && reads[next_end].1 <= index {
            if scopes[current_scope].discard {
                // The operand of `typeof`, `void` or `delete` is not a value
                // of any branch that follows it.
                contexts[next_end] = ScopeContext::Invalid;
            } else {
                scopes[current_scope].pending_reads.push(next_end);
            }
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

        if scopes[current_scope].discard && ends_discarded_operand(bytes, index) {
            scopes[current_scope].discard = false;
        }
        match word_event(code, index, ignored) {
            Some(WordEvent::Discard) => scopes[current_scope].discard = true,
            Some(WordEvent::Return) => scopes[current_scope].returning = true,
            None => {}
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
            // arrow body remain recorded on their own reads. When the arrow
            // ends a parenthesized expression, its body is what a call of
            // that expression returns.
            if bytes[index] == b')'
                && arrow_kind(code, scopes[current_scope].open, ignored) == BodyKind::Arrow
            {
                scopes[parent].function_returns.extend(pending_reads);
                scopes[parent].function_returns.extend(conditional_pending);
            }
            current_scope = parent;
        }

        if bytes[index] == b'\n' && asi_terminates(bytes, index, ignored) {
            scopes[current_scope].discard = false;
            settle_return(&mut scopes[current_scope]);
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
                let (context, kind) = scope_context(
                    code,
                    index,
                    bytes[index],
                    scopes[current_scope].context,
                    last_close,
                    ignored,
                );
                let mut scope = ScanScope::new(current_scope, context, kind, index);
                // A parenthesized or computed operand of `typeof`, `void` or
                // `delete` is still that operand.
                scope.discard = scopes[current_scope].discard
                    && matches!(
                        kind,
                        ContainerKind::Grouping | ContainerKind::ComputedMember
                    );
                scopes.push(scope);
                current_scope = scopes.len() - 1;
            }
            b')' | b']' | b'}' if current_scope != 0 => {
                let parent = scopes[current_scope].parent;
                let pending_reads = std::mem::take(&mut scopes[current_scope].pending_reads);
                let conditional_pending =
                    std::mem::take(&mut scopes[current_scope].conditional_pending);
                if scopes[current_scope].returning {
                    // The closing bracket ends a `return` expression.
                    scopes[current_scope]
                        .returned
                        .extend(pending_reads.iter().chain(&conditional_pending));
                    scopes[current_scope].returning = false;
                }
                let returned = std::mem::take(&mut scopes[current_scope].returned);
                let function_returns = std::mem::take(&mut scopes[current_scope].function_returns);
                // A closed value container evaluates to a value (array,
                // object, function object or call result). Reads that were
                // only values inside it must not inherit an operator that
                // follows the container. Guards found inside the container
                // remain recorded on their own reads. An array or object
                // literal whose selected element or property is the read
                // (`[read][0]`) yields that read, so it stays pending.
                let kind = scopes[current_scope].kind;
                last_close = Some((index, kind));
                let selected = match kind {
                    ContainerKind::ArrayLiteral => {
                        !scopes[current_scope].spread
                            && array_element_selected(
                                bytes,
                                index + 1,
                                ignored,
                                scopes[current_scope].elements,
                            )
                    }
                    ContainerKind::ObjectLiteral => {
                        scopes[current_scope].last_key.is_some_and(|(start, end)| {
                            property_selected(bytes, index + 1, ignored, &bytes[start..end])
                        })
                    }
                    _ => false,
                };
                if selected {
                    flow_reads(
                        &mut scopes,
                        &mut contexts,
                        parent,
                        pending_reads.into_iter().chain(conditional_pending),
                    );
                } else if !kind.is_value_container() {
                    scopes[parent].pending_reads.extend(pending_reads);
                    scopes[parent].pending_reads.extend(conditional_pending);
                    scopes[parent].pending_reads.sort_unstable();
                }
                // A call result is the value a function returns, so the reads
                // it returns reach the operator that follows the call.
                let called = next_code_byte(bytes, index + 1, ignored) == Some(b'(');
                match kind {
                    ContainerKind::Block if !returned.is_empty() => {
                        match body_kind(code, scopes[current_scope].open, ignored) {
                            BodyKind::Function if called => {
                                flow_reads(&mut scopes, &mut contexts, parent, returned);
                            }
                            BodyKind::Function | BodyKind::Arrow => {
                                if next_code_byte(bytes, index + 1, ignored) == Some(b')') {
                                    scopes[parent].function_returns.extend(returned);
                                }
                            }
                            BodyKind::Statement => scopes[parent].returned.extend(returned),
                            BodyKind::Other => {}
                        }
                    }
                    ContainerKind::Grouping if called && !function_returns.is_empty() => {
                        flow_reads(&mut scopes, &mut contexts, parent, function_returns);
                    }
                    _ => {}
                }
                current_scope = parent;
            }
            b';' => {
                settle_return(&mut scopes[current_scope]);
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
                scopes[current_scope].elements += 1;
                scopes[current_scope].last_key = None;
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
                    if scopes[current_scope].kind == ContainerKind::ObjectLiteral {
                        scopes[current_scope].last_key = property_key(code, index, ignored);
                    }
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
            b'.' if scopes[current_scope].kind == ContainerKind::ArrayLiteral
                && bytes[index..].starts_with(b"...") =>
            {
                // A spread element shifts the indexes of the elements after it.
                scopes[current_scope].spread = true;
            }
            b'=' if bytes.get(index + 1) == Some(&b'>') => {
                if next_code_byte(bytes, index + 2, ignored) != Some(b'{') {
                    let mut scope = ScanScope::new(
                        current_scope,
                        ScopeContext::Neutral,
                        ContainerKind::Grouping,
                        index,
                    );
                    scope.arrow_expression = true;
                    scopes.push(scope);
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
    let bytes = code.as_bytes();
    let mut end = code[..index].trim_end().len();
    loop {
        // Last ignored range that starts before `end`.
        let Some(range_index) = ignored
            .partition_point(|(start, _)| *start < end)
            .checked_sub(1)
        else {
            return &code[..end];
        };
        let (start, stop) = ignored[range_index];
        let is_comment = bytes[start] == b'/' && matches!(bytes.get(start + 1), Some(b'/' | b'*'));
        if stop < end || !is_comment {
            return &code[..end];
        }
        end = code[..start].trim_end().len();
    }
}

/// Move the reads of a finished `return` expression to the function's
/// returned reads instead of dropping them with the statement.
fn settle_return(scope: &mut ScanScope) {
    if scope.returning {
        let pending = std::mem::take(&mut scope.pending_reads);
        let conditional = std::mem::take(&mut scope.conditional_pending);
        scope.returned.extend(pending);
        scope.returned.extend(conditional);
        scope.returning = false;
    }
}

/// Hand reads to the expression that contains the value they now produce.
/// Their fallback condition becomes the one of that expression.
fn flow_reads(
    scopes: &mut [ScanScope],
    contexts: &mut [ScopeContext],
    target: usize,
    reads: impl IntoIterator<Item = usize>,
) {
    let discard = scopes[target].discard;
    let enclosing = scopes[target].context;
    for read in reads {
        if discard {
            contexts[read] = ScopeContext::Invalid;
        } else {
            contexts[read] = enclosing;
            scopes[target].pending_reads.push(read);
        }
    }
    scopes[target].pending_reads.sort_unstable();
}

/// True for a token that ends the operand of `typeof`, `void` or `delete`.
/// Prefix operators (`!`, `+`, `-`, `~`) can start the operand, so they do
/// not end it; keeping the operand open only drops more reads.
fn ends_discarded_operand(bytes: &[u8], index: usize) -> bool {
    match bytes[index] {
        b'&' | b'|' | b',' | b';' | b':' | b'=' | b'<' | b'>' | b'*' | b'/' | b'%' | b'^' => true,
        // `?.` is optional chaining, but `?.5` is a conditional.
        b'?' => {
            !(bytes.get(index + 1) == Some(&b'.')
                && !bytes.get(index + 2).is_some_and(u8::is_ascii_digit))
        }
        _ => false,
    }
}

/// Recognize the words the scanner tracks, at the first byte of a word that
/// is not a property name.
fn word_event(code: &str, index: usize, ignored: &[(usize, usize)]) -> Option<WordEvent> {
    let bytes = code.as_bytes();
    if !matches!(bytes[index], b't' | b'v' | b'd' | b'r')
        || (index > 0 && is_identifier_byte(bytes[index - 1]))
    {
        return None;
    }
    let length = bytes[index..]
        .iter()
        .take_while(|byte| is_identifier_byte(**byte))
        .count();
    let event = match &code[index..index + length] {
        "typeof" | "void" | "delete" => WordEvent::Discard,
        "return" => WordEvent::Return,
        _ => return None,
    };
    let prefix = scope_prefix(code, index, ignored);
    // A property named like a keyword is an ordinary member.
    if prefix.ends_with('.') && !prefix.ends_with("...") {
        return None;
    }
    Some(event)
}

/// Index of the next byte that is code (not whitespace, comment or literal).
fn next_code_index(bytes: &[u8], start: usize, ignored: &[(usize, usize)]) -> Option<usize> {
    (start..bytes.len())
        .find(|&index| !in_ranges(ignored, index) && !bytes[index].is_ascii_whitespace())
}

/// The string, template, comment or regular expression range that starts at
/// `offset`.
fn ignored_range_at(ignored: &[(usize, usize)], offset: usize) -> Option<(usize, usize)> {
    let index = ignored.partition_point(|(start, _)| *start < offset);
    ignored
        .get(index)
        .copied()
        .filter(|(start, _)| *start == offset)
}

/// Name of the property whose value ends before the `:` at `colon`.
fn property_key(code: &str, colon: usize, ignored: &[(usize, usize)]) -> Option<(usize, usize)> {
    let prefix = scope_prefix(code, colon, ignored);
    let last = prefix.len().checked_sub(1)?;
    if in_ranges(ignored, last) {
        let index = ignored.partition_point(|(start, _)| *start <= last);
        let (start, end) = ignored[index.checked_sub(1)?];
        let quoted = matches!(code.as_bytes()[start], b'\'' | b'"');
        return (quoted && end >= start + 2 && end == prefix.len()).then_some((start + 1, end - 1));
    }
    let word = word_before(prefix)?;
    Some((prefix.len() - word.len(), prefix.len()))
}

/// Member access that follows a closed literal.
enum MemberAccess {
    /// `[` at this index.
    Computed(usize),
    /// `.` or `?.`; the member name starts after this index.
    Named(usize),
    /// `?.[`; the `[` is at this index.
    OptionalComputed(usize),
}

fn member_access(bytes: &[u8], start: usize, ignored: &[(usize, usize)]) -> Option<MemberAccess> {
    let index = next_code_index(bytes, start, ignored)?;
    match bytes[index] {
        b'[' => Some(MemberAccess::Computed(index)),
        b'.' if !bytes[index..].starts_with(b"...") => Some(MemberAccess::Named(index + 1)),
        // `?.` is optional chaining, but `?.5` is a conditional.
        b'?' if bytes.get(index + 1) == Some(&b'.')
            && !bytes.get(index + 2).is_some_and(u8::is_ascii_digit) =>
        {
            match next_code_index(bytes, index + 2, ignored) {
                Some(open) if bytes[open] == b'[' => Some(MemberAccess::OptionalComputed(open)),
                _ => Some(MemberAccess::Named(index + 2)),
            }
        }
        _ => None,
    }
}

/// True when the member access after an array literal selects its last
/// element: `[...][N]`, `[...]?.[N]`, `[...].at(N)` or `[...]?.at(N)`, where
/// `N` is the decimal index of that element.
fn array_element_selected(
    bytes: &[u8],
    start: usize,
    ignored: &[(usize, usize)],
    element: usize,
) -> bool {
    let (open, closing) = match member_access(bytes, start, ignored) {
        Some(MemberAccess::Computed(open) | MemberAccess::OptionalComputed(open)) => (open, b']'),
        Some(MemberAccess::Named(after)) => {
            let Some(name) = next_code_index(bytes, after, ignored) else {
                return false;
            };
            let length = bytes[name..]
                .iter()
                .take_while(|byte| is_identifier_byte(**byte))
                .count();
            if &bytes[name..name + length] != b"at" {
                return false;
            }
            match next_code_index(bytes, name + length, ignored) {
                Some(open) if bytes[open] == b'(' => (open, b')'),
                _ => return false,
            }
        }
        None => return false,
    };
    let Some(digits) = next_code_index(bytes, open + 1, ignored) else {
        return false;
    };
    let length = bytes[digits..]
        .iter()
        .take_while(|byte| byte.is_ascii_digit())
        .count();
    let index = std::str::from_utf8(&bytes[digits..digits + length])
        .ok()
        .and_then(|text| text.parse::<usize>().ok());
    index == Some(element)
        && next_code_index(bytes, digits + length, ignored).is_some_and(|end| bytes[end] == closing)
}

/// True when the member access after an object literal selects the property
/// named `key`: `{...}.key`, `{...}?.key`, `{...}["key"]` or `{...}['key']`.
fn property_selected(bytes: &[u8], start: usize, ignored: &[(usize, usize)], key: &[u8]) -> bool {
    match member_access(bytes, start, ignored) {
        Some(MemberAccess::Named(after)) => {
            let Some(name) = next_code_index(bytes, after, ignored) else {
                return false;
            };
            let length = bytes[name..]
                .iter()
                .take_while(|byte| is_identifier_byte(**byte))
                .count();
            &bytes[name..name + length] == key
        }
        Some(MemberAccess::Computed(open) | MemberAccess::OptionalComputed(open)) => {
            // The quoted name is a string literal, which the code scan skips.
            let Some(quote) =
                (open + 1..bytes.len()).find(|&index| !bytes[index].is_ascii_whitespace())
            else {
                return false;
            };
            let Some((string_start, string_end)) = ignored_range_at(ignored, quote) else {
                return false;
            };
            matches!(bytes[string_start], b'\'' | b'"')
                && string_end >= string_start + 2
                && &bytes[string_start + 1..string_end - 1] == key
                && next_code_index(bytes, string_end, ignored).is_some_and(|end| bytes[end] == b']')
        }
        None => false,
    }
}

/// Classify an arrow function whose `=>` starts at `arrow`. An async arrow
/// returns a promise, never the value of its body.
fn arrow_kind(code: &str, arrow: usize, ignored: &[(usize, usize)]) -> BodyKind {
    let prefix = scope_prefix(code, arrow, ignored);
    let head = if prefix.ends_with(')') {
        matching_backward(prefix.as_bytes(), prefix.len() - 1, b'(', b')')
            .map(|paren| prefix[..paren].trim_end())
    } else {
        word_before(prefix).map(|word| prefix[..prefix.len() - word.len()].trim_end())
    };
    if head.is_some_and(|head| word_before(head) == Some("async")) {
        BodyKind::Other
    } else {
        BodyKind::Arrow
    }
}

/// Classify the block whose `{` is at `open`.
fn body_kind(code: &str, open: usize, ignored: &[(usize, usize)]) -> BodyKind {
    let prefix = scope_prefix(code, open, ignored);
    if prefix.ends_with("=>") {
        return arrow_kind(code, prefix.len() - 2, ignored);
    }
    let Some(&last) = prefix.as_bytes().last() else {
        return BodyKind::Statement;
    };
    if matches!(last, b';' | b'{' | b'}') {
        return BodyKind::Statement;
    }
    if last != b')' {
        return match word_before(prefix) {
            Some("else" | "do" | "try" | "finally") => BodyKind::Statement,
            _ => BodyKind::Other,
        };
    }
    let Some(paren) = matching_backward(prefix.as_bytes(), prefix.len() - 1, b'(', b')') else {
        return BodyKind::Other;
    };
    // `function name(` and `function* name(`; an async or generator function
    // returns a promise or an iterator, never the returned value itself.
    let head = prefix[..paren].trim_end();
    let Some(word) = word_before(head) else {
        return BodyKind::Other;
    };
    let function_head = |text: &str| -> Option<BodyKind> {
        let text = text.trim_end();
        let (text, generator) = match text.strip_suffix('*') {
            Some(text) => (text.trim_end(), true),
            None => (text, false),
        };
        if word_before(text) != Some("function") {
            return None;
        }
        let before = text[..text.len() - "function".len()].trim_end();
        Some(if generator || word_before(before) == Some("async") {
            BodyKind::Other
        } else {
            BodyKind::Function
        })
    };
    match word {
        "if" | "for" | "while" | "switch" | "catch" | "with" | "await" => BodyKind::Statement,
        _ => function_head(head)
            .or_else(|| function_head(&head[..head.len() - word.len()]))
            .unwrap_or(BodyKind::Other),
    }
}

/// The token before an opener, reduced to what decides how the opener parses.
#[derive(Clone, Copy)]
enum PrecedingToken {
    /// A new statement starts here (input start, `;`, `{`, `=>`, `else`, `do`,
    /// a block's `}` or the `)` that closes a statement head).
    StatementStart,
    /// An operator, separator or operator keyword; an operand starts here.
    Operator,
    /// `typeof`, `void` or `delete`: an operand starts here, but the result
    /// does not preserve the operand's truthiness.
    DiscardOperator,
    /// `if`, `while`, `for` or `switch`, which take a parenthesized head.
    StatementHead(ScopeContext),
    /// An operand just ended (identifier, number, `)`, `]`, an object
    /// literal's `}`, a property name, a string, template or regular
    /// expression literal).
    OperandEnd,
}

const OPERATOR_KEYWORDS: [&str; 11] = [
    "return",
    "await",
    "yield",
    "in",
    "of",
    "instanceof",
    "case",
    "throw",
    "new",
    "extends",
    "default",
];

/// Unary operators whose result does not depend on the operand's value.
const DISCARDING_KEYWORDS: [&str; 3] = ["typeof", "void", "delete"];

fn is_identifier_byte(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'$') || byte >= 0x80
}

/// Classify the token that ends `prefix` (see `ContainerKind` for the table).
fn preceding_token(
    code: &str,
    prefix: &str,
    last_close: Option<(usize, ContainerKind)>,
    ignored: &[(usize, usize)],
) -> PrecedingToken {
    let bytes = prefix.as_bytes();
    let Some(&last) = bytes.last() else {
        return PrecedingToken::StatementStart;
    };
    let last_index = bytes.len() - 1;
    if in_ranges(ignored, last_index) {
        // Strings, templates and regular expressions end an operand. Template
        // text that stops at `${` opens a substitution instead. Comments were
        // removed from the prefix already.
        return if code.as_bytes()[..=last_index].ends_with(b"${") {
            PrecedingToken::Operator
        } else {
            PrecedingToken::OperandEnd
        };
    }
    let closed = |kind: fn(ContainerKind) -> bool| {
        last_close.is_some_and(|(index, closed)| index == last_index && kind(closed))
    };
    match last {
        b';' | b'{' => PrecedingToken::StatementStart,
        b'}' if closed(|kind| kind == ContainerKind::Block) => PrecedingToken::StatementStart,
        b')' if closed(|kind| kind == ContainerKind::StatementHead) => {
            PrecedingToken::StatementStart
        }
        b')' | b']' | b'}' => PrecedingToken::OperandEnd,
        b'>' if prefix.ends_with("=>") => PrecedingToken::StatementStart,
        // Spread is an operator; any other `.` is a member access.
        b'.' if prefix.ends_with("...") => PrecedingToken::Operator,
        b'.' => PrecedingToken::OperandEnd,
        b'*' => {
            // `function*` is followed by a parameter list.
            let head = prefix[..last_index].trim_end();
            if word_before(head).is_some_and(|word| word == "function") {
                PrecedingToken::OperandEnd
            } else {
                PrecedingToken::Operator
            }
        }
        byte if is_identifier_byte(byte) => {
            let word = word_before(prefix).expect("identifier byte ends a word");
            let head = prefix[..prefix.len() - word.len()].trim_end();
            // A property name is never a keyword. A spread `...` is not a
            // member access.
            if head.ends_with('.') && !head.ends_with("...") {
                return PrecedingToken::OperandEnd;
            }
            match word {
                "else" | "do" => PrecedingToken::StatementStart,
                "if" => PrecedingToken::StatementHead(ScopeContext::Guard(EnvGuard::If)),
                "while" => PrecedingToken::StatementHead(ScopeContext::Guard(EnvGuard::While)),
                "switch" => PrecedingToken::StatementHead(ScopeContext::Guard(EnvGuard::Switch)),
                "for" => PrecedingToken::StatementHead(ScopeContext::For(ForClause::Initializer)),
                word if DISCARDING_KEYWORDS.contains(&word) => PrecedingToken::DiscardOperator,
                word if OPERATOR_KEYWORDS.contains(&word) => PrecedingToken::Operator,
                _ => PrecedingToken::OperandEnd,
            }
        }
        // Quotes always end a string or template literal.
        b'\'' | b'"' | b'`' => PrecedingToken::OperandEnd,
        // Every other punctuation byte, including postfix `++`/`--` (which
        // cannot be followed by a call or member access), starts an operand.
        _ => PrecedingToken::Operator,
    }
}

/// The identifier word that ends `text`, if any.
fn word_before(text: &str) -> Option<&str> {
    let bytes = text.as_bytes();
    let mut start = bytes.len();
    while start > 0 && is_identifier_byte(bytes[start - 1]) {
        start -= 1;
    }
    // Non-ASCII identifier bytes are never split because every byte of a
    // multi-byte character is >= 0x80.
    (start < bytes.len()).then(|| &text[start..])
}

fn scope_context(
    code: &str,
    index: usize,
    opener: u8,
    parent: ScopeContext,
    last_close: Option<(usize, ContainerKind)>,
    ignored: &[(usize, usize)],
) -> (ScopeContext, ContainerKind) {
    let prefix = scope_prefix(code, index, ignored);
    let preceding = preceding_token(code, prefix, last_close, ignored);
    match (opener, preceding) {
        // A statement head owns its parenthesized condition.
        (b'(', PrecedingToken::StatementHead(context)) => (context, ContainerKind::StatementHead),
        // Parentheses after an operand are call arguments; elsewhere they
        // only group and keep the surrounding condition.
        (b'(', PrecedingToken::OperandEnd) => (ScopeContext::Invalid, ContainerKind::CallArguments),
        // The operand of `typeof`, `void` or `delete` is not a value of the
        // branch that follows; a branch inside it still guards its own read.
        (b'(', PrecedingToken::DiscardOperator) => {
            (ScopeContext::Invalid, ContainerKind::Discarded)
        }
        (b'(', _) => (parent, ContainerKind::Grouping),
        // A bracket after an operand is a computed member access and keeps the
        // surrounding condition; elsewhere it starts an array literal.
        (b'[', PrecedingToken::OperandEnd) => (parent, ContainerKind::ComputedMember),
        (b'[', _) => (ScopeContext::Invalid, ContainerKind::ArrayLiteral),
        // A brace where an operand starts is an object literal; elsewhere it
        // is a function, class or statement block.
        (_, PrecedingToken::Operator | PrecedingToken::DiscardOperator) => {
            (ScopeContext::Invalid, ContainerKind::ObjectLiteral)
        }
        _ => (ScopeContext::Invalid, ContainerKind::Block),
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
    let line = source[..start].matches('\n').count();
    let column = start - source[..start].rfind('\n').map_or(0, |index| index + 1);
    Some((u32::try_from(line).ok()?, u32::try_from(column).ok()?))
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
    fn invalid_containers_do_not_inherit_outer_guards() {
        let guard = |code: &str| {
            let read = "process.env.FLAG";
            let start = code.find(read).unwrap();
            env_guard(code, start, start + read.len(), &non_code_ranges(code))
                .map(EnvGuard::evidence)
        };
        // Value-only reads inside array literals, object literals, function
        // objects and call argument lists never control a following operator.
        for code in [
            "[process.env.FLAG] && run()",
            "[process.env.FLAG] || run()",
            "[process.env.FLAG] ?? run()",
            "[process.env.FLAG] ? a() : b()",
            "x = [process.env.FLAG] || run()",
            "[[process.env.FLAG]] && run()",
            "[[process.env.FLAG], 1] && run()",
            "[1, [2, [process.env.FLAG]]] && run()",
            "([process.env.FLAG]) && run()",
            "(function(){ return process.env.FLAG }) && run()",
            "(function(){ return process.env.FLAG; }) && run()",
            "(function(){ return process.env.FLAG }) || run()",
            "(function(){ return process.env.FLAG; }) ?? run()",
            "(function(){ return process.env.FLAG }) ? a() : b()",
            "(()=>process.env.FLAG) && run()",
            "(()=>{ return process.env.FLAG }) && run()",
            "(()=>{ return process.env.FLAG; }) && run()",
            "check(process.env.FLAG) && run()",
            "{a:process.env.FLAG} && run()",
            "({a:process.env.FLAG}) && run()",
        ] {
            assert_eq!(guard(code), None, "{code}");
        }
        // Real branches keep their guards, including branches inside
        // containers and parenthesized or computed-member conditions.
        for (code, evidence) in [
            (
                "process.env.FLAG && run()",
                "the retained read controls a && short-circuit branch",
            ),
            (
                "(process.env.FLAG) && run()",
                "the retained read controls a && short-circuit branch",
            ),
            (
                "[process.env.FLAG && run()]",
                "the retained read controls a && short-circuit branch",
            ),
            (
                "[[process.env.FLAG || run()]]",
                "the retained read controls a || short-circuit branch",
            ),
            (
                "function run(){process.env.FLAG&&work()}",
                "the retained read controls a && short-circuit branch",
            ),
            (
                "function run(){process.env.FLAG&&work();}",
                "the retained read controls a && short-circuit branch",
            ),
            (
                "check(a && process.env.FLAG && run())",
                "the retained read controls a && short-circuit branch",
            ),
            (
                "[(process.env.FLAG) && run()]",
                "the retained read controls a && short-circuit branch",
            ),
            (
                "if({on:true}[process.env.FLAG])run()",
                "the retained read is in an if condition",
            ),
        ] {
            assert_eq!(guard(code), Some(evidence), "{code}");
        }
    }

    /// Classify the opener that follows `prefix`. `closed` is the kind of the
    /// scope whose closing bracket ends `prefix`, when it does.
    fn opener_kind(prefix: &str, opener: char, closed: Option<ContainerKind>) -> ContainerKind {
        let code = format!("{prefix}{opener}");
        let ignored = non_code_ranges(&code);
        let last_close = closed.map(|kind| (prefix.trim_end().len() - 1, kind));
        scope_context(
            &code,
            prefix.len(),
            opener as u8,
            ScopeContext::Neutral,
            last_close,
            &ignored,
        )
        .1
    }

    #[test]
    fn opener_classification_table_covers_every_preceding_token() {
        use ContainerKind::{
            ArrayLiteral, Block, CallArguments, ComputedMember, Discarded, Grouping, ObjectLiteral,
            StatementHead,
        };
        // Each row: preceding code, then the kinds of `(`, `[` and `{`.
        let rows: &[(&str, ContainerKind, ContainerKind, ContainerKind)] = &[
            // Start of input and statement boundaries.
            ("", Grouping, ArrayLiteral, Block),
            ("a;", Grouping, ArrayLiteral, Block),
            ("a{", Grouping, ArrayLiteral, Block),
            ("f=>", Grouping, ArrayLiteral, Block),
            ("f =>", Grouping, ArrayLiteral, Block),
            ("else", Grouping, ArrayLiteral, Block),
            ("else ", Grouping, ArrayLiteral, Block),
            ("do", Grouping, ArrayLiteral, Block),
            // Statement heads take a condition list in `(` only.
            ("if", StatementHead, ArrayLiteral, Block),
            ("while ", StatementHead, ArrayLiteral, Block),
            ("for", StatementHead, ArrayLiteral, Block),
            ("switch", StatementHead, ArrayLiteral, Block),
            ("a;if", StatementHead, ArrayLiteral, Block),
            // Operator keywords start an operand.
            ("return", Grouping, ArrayLiteral, ObjectLiteral),
            ("return ", Grouping, ArrayLiteral, ObjectLiteral),
            ("typeof", Discarded, ArrayLiteral, ObjectLiteral),
            ("await", Grouping, ArrayLiteral, ObjectLiteral),
            ("yield", Grouping, ArrayLiteral, ObjectLiteral),
            ("a in", Grouping, ArrayLiteral, ObjectLiteral),
            ("a of", Grouping, ArrayLiteral, ObjectLiteral),
            ("a instanceof", Grouping, ArrayLiteral, ObjectLiteral),
            ("case", Grouping, ArrayLiteral, ObjectLiteral),
            ("void", Discarded, ArrayLiteral, ObjectLiteral),
            ("delete", Discarded, ArrayLiteral, ObjectLiteral),
            ("typeof ", Discarded, ArrayLiteral, ObjectLiteral),
            ("a;typeof/* c */", Discarded, ArrayLiteral, ObjectLiteral),
            ("mytypeof", CallArguments, ComputedMember, Block),
            ("a.typeof", CallArguments, ComputedMember, Block),
            ("a.void", CallArguments, ComputedMember, Block),
            ("a.delete", CallArguments, ComputedMember, Block),
            ("throw", Grouping, ArrayLiteral, ObjectLiteral),
            ("new", Grouping, ArrayLiteral, ObjectLiteral),
            ("class A extends", Grouping, ArrayLiteral, ObjectLiteral),
            ("export default", Grouping, ArrayLiteral, ObjectLiteral),
            // Words that merely contain a keyword, and property names, end an
            // operand.
            ("returns", CallArguments, ComputedMember, Block),
            ("myreturn", CallArguments, ComputedMember, Block),
            ("$in", CallArguments, ComputedMember, Block),
            ("a.return", CallArguments, ComputedMember, Block),
            ("a. return", CallArguments, ComputedMember, Block),
            ("a?.await", CallArguments, ComputedMember, Block),
            ("a.if", CallArguments, ComputedMember, Block),
            ("a.for", CallArguments, ComputedMember, Block),
            ("a....return", Grouping, ArrayLiteral, ObjectLiteral),
            // Operands: identifiers, numbers, other words and non-ASCII names.
            ("foo", CallArguments, ComputedMember, Block),
            ("foo ", CallArguments, ComputedMember, Block),
            ("$", CallArguments, ComputedMember, Block),
            ("_a1", CallArguments, ComputedMember, Block),
            ("1", CallArguments, ComputedMember, Block),
            ("this", CallArguments, ComputedMember, Block),
            ("function", CallArguments, ComputedMember, Block),
            ("function*", CallArguments, ComputedMember, Block),
            ("function *", CallArguments, ComputedMember, Block),
            ("catch", CallArguments, ComputedMember, Block),
            ("\u{e9}t\u{e9}", CallArguments, ComputedMember, Block),
            // Member access dots.
            ("a.", CallArguments, ComputedMember, Block),
            ("a?.", CallArguments, ComputedMember, Block),
            // Spread is an operator.
            ("f(...", Grouping, ArrayLiteral, ObjectLiteral),
            ("[...", Grouping, ArrayLiteral, ObjectLiteral),
            // Punctuation and operators start an operand.
            ("a(", Grouping, ArrayLiteral, ObjectLiteral),
            ("a[", Grouping, ArrayLiteral, ObjectLiteral),
            ("a,", Grouping, ArrayLiteral, ObjectLiteral),
            ("a=", Grouping, ArrayLiteral, ObjectLiteral),
            ("a==", Grouping, ArrayLiteral, ObjectLiteral),
            ("a:", Grouping, ArrayLiteral, ObjectLiteral),
            ("a?", Grouping, ArrayLiteral, ObjectLiteral),
            ("a??", Grouping, ArrayLiteral, ObjectLiteral),
            ("!", Grouping, ArrayLiteral, ObjectLiteral),
            ("a&&", Grouping, ArrayLiteral, ObjectLiteral),
            ("a||", Grouping, ArrayLiteral, ObjectLiteral),
            ("a&", Grouping, ArrayLiteral, ObjectLiteral),
            ("a|", Grouping, ArrayLiteral, ObjectLiteral),
            ("a+", Grouping, ArrayLiteral, ObjectLiteral),
            ("a-", Grouping, ArrayLiteral, ObjectLiteral),
            ("a*", Grouping, ArrayLiteral, ObjectLiteral),
            ("a**", Grouping, ArrayLiteral, ObjectLiteral),
            ("a%", Grouping, ArrayLiteral, ObjectLiteral),
            ("a<", Grouping, ArrayLiteral, ObjectLiteral),
            ("a>", Grouping, ArrayLiteral, ObjectLiteral),
            ("a>=", Grouping, ArrayLiteral, ObjectLiteral),
            ("~", Grouping, ArrayLiteral, ObjectLiteral),
            ("a^", Grouping, ArrayLiteral, ObjectLiteral),
            ("a /", Grouping, ArrayLiteral, ObjectLiteral),
            // Postfix operators cannot be followed by a call or member access.
            ("a++", Grouping, ArrayLiteral, ObjectLiteral),
            ("a--", Grouping, ArrayLiteral, ObjectLiteral),
            ("++", Grouping, ArrayLiteral, ObjectLiteral),
            // Strings, templates and regular expressions end an operand.
            ("'x'", CallArguments, ComputedMember, Block),
            ("\"x\"", CallArguments, ComputedMember, Block),
            ("`x`", CallArguments, ComputedMember, Block),
            ("a=/x/", CallArguments, ComputedMember, Block),
            ("a=/x/g", CallArguments, ComputedMember, Block),
            ("return /x/", CallArguments, ComputedMember, Block),
            ("a=/[/]/", CallArguments, ComputedMember, Block),
            // Template text that opens a substitution starts an operand.
            ("`a${", Grouping, ArrayLiteral, ObjectLiteral),
            ("`a${b}c${", Grouping, ArrayLiteral, ObjectLiteral),
            ("`a${b}`", CallArguments, ComputedMember, Block),
            // Comments are transparent.
            ("a/* c */", CallArguments, ComputedMember, Block),
            ("a // c\n", CallArguments, ComputedMember, Block),
            ("a // c  \n", CallArguments, ComputedMember, Block),
            ("a /* x */ /* y */", CallArguments, ComputedMember, Block),
            ("a= // c\n", Grouping, ArrayLiteral, ObjectLiteral),
            ("return/* c */", Grouping, ArrayLiteral, ObjectLiteral),
            ("return // c\n", Grouping, ArrayLiteral, ObjectLiteral),
            ("// c\n", Grouping, ArrayLiteral, Block),
        ];
        for &(prefix, call, bracket, brace) in rows {
            assert_eq!(opener_kind(prefix, '(', None), call, "{prefix:?} (");
            assert_eq!(opener_kind(prefix, '[', None), bracket, "{prefix:?} [");
            assert_eq!(opener_kind(prefix, '{', None), brace, "{prefix:?} {{");
        }
        // Closing brackets depend on what they closed.
        let closers: &[(
            &str,
            ContainerKind,
            ContainerKind,
            ContainerKind,
            ContainerKind,
        )] = &[
            // `)` of a statement head and a block's `}` end a statement.
            (")", StatementHead, Grouping, ArrayLiteral, Block),
            ("}", Block, Grouping, ArrayLiteral, Block),
            // Every other closing bracket ends an operand.
            (")", Grouping, CallArguments, ComputedMember, Block),
            (")", CallArguments, CallArguments, ComputedMember, Block),
            (")", Discarded, CallArguments, ComputedMember, Block),
            ("]", ComputedMember, CallArguments, ComputedMember, Block),
            ("]", ArrayLiteral, CallArguments, ComputedMember, Block),
            ("}", ObjectLiteral, CallArguments, ComputedMember, Block),
        ];
        for &(prefix, closed, call, bracket, brace) in closers {
            let prefix = format!("x{prefix} ");
            let closed = Some(closed);
            assert_eq!(
                opener_kind(&prefix, '(', closed),
                call,
                "{prefix:?} {closed:?} ("
            );
            assert_eq!(
                opener_kind(&prefix, '[', closed),
                bracket,
                "{prefix:?} {closed:?} ["
            );
            assert_eq!(
                opener_kind(&prefix, '{', closed),
                brace,
                "{prefix:?} {closed:?} {{"
            );
        }
        // A closing bracket the scanner never saw close is an operand end.
        assert_eq!(opener_kind("a)", '(', None), CallArguments);
        assert_eq!(opener_kind("a}", '[', None), ComputedMember);
    }

    #[test]
    fn reported_container_shapes_keep_their_branch_evidence() {
        let guard = |code: &str| {
            let read = "process.env.FLAG";
            let start = code.find(read).unwrap();
            env_guard(code, start, start + read.len(), &non_code_ranges(code))
                .map(EnvGuard::evidence)
        };
        let and = Some("the retained read controls a && short-circuit branch");
        // An array literal after a keyword is a value, not a member access.
        for code in [
            "return[process.env.FLAG]&&run()",
            "return [process.env.FLAG] && run()",
            "typeof[process.env.FLAG]&&run()",
            "await[process.env.FLAG]&&run()",
            "yield[process.env.FLAG]&&run()",
            "void[process.env.FLAG]&&run()",
            "throw[process.env.FLAG]&&run()",
            "a=1;else[process.env.FLAG]&&run()",
            "if(a)[process.env.FLAG]&&run()",
            "while(a)[process.env.FLAG]&&run()",
            "for(;;)[process.env.FLAG]&&run()",
            "x++;[process.env.FLAG]&&run()",
            "f(...[process.env.FLAG])&&run()",
            "a{}[process.env.FLAG]&&run()",
        ] {
            assert_eq!(guard(code), None, "{code}");
        }
        // Parentheses after a keyword only group their operand.
        for code in [
            "return(process.env.FLAG)&&run()",
            "return (process.env.FLAG) && run()",
            "await(process.env.FLAG)&&run()",
            "yield(process.env.FLAG)&&run()",
            "x=a in(process.env.FLAG)&&run()",
            "if(a)(process.env.FLAG)&&run()",
        ] {
            assert_eq!(guard(code), and, "{code}");
        }
        // The operand of `typeof`, `void` and `delete` is not a value of the
        // branch that follows it.
        for code in [
            "typeof(process.env.FLAG)&&run()",
            "void(process.env.FLAG)&&run()",
            "delete(process.env.FLAG)&&run()",
        ] {
            assert_eq!(guard(code), None, "{code}");
        }
        // A property named like a keyword is an ordinary call or member.
        assert_eq!(guard("a.return(process.env.FLAG)&&run()"), None);
        assert_eq!(guard("a.return[process.env.FLAG]&&run()"), and);
        // A bracket after a regular expression literal is a computed member.
        for code in [
            "/x/[process.env.FLAG]&&run()",
            "a=/x/g[process.env.FLAG]&&run()",
            "return/x/[process.env.FLAG]&&run()",
            "a=/[/]/[process.env.FLAG]&&run()",
        ] {
            assert_eq!(guard(code), and, "{code}");
        }
        // Computed members of strings, templates and closed brackets, and a
        // line comment before the bracket, keep their evidence.
        for code in [
            "'x'[process.env.FLAG]&&run()",
            "`x`[process.env.FLAG]&&run()",
            "a[0][process.env.FLAG]&&run()",
            "a(0)[process.env.FLAG]&&run()",
            "a // c\n[process.env.FLAG]&&run()",
            "a /* c */[process.env.FLAG]&&run()",
            "this[process.env.FLAG]&&run()",
            "a?.[process.env.FLAG]&&run()",
        ] {
            assert_eq!(guard(code), and, "{code}");
        }
        // A statement-start bracket after a line comment is an array literal.
        assert_eq!(guard("a=\n// c\n[process.env.FLAG]&&run()"), None);
        // Template substitutions start an operand.
        assert_eq!(guard("`x${[process.env.FLAG]&&run()}`"), None);
        assert_eq!(guard("`x${(process.env.FLAG)&&run()}`"), and);
    }

    #[test]
    fn literals_read_through_a_member_access_keep_their_branch_evidence() {
        let guard = |code: &str| {
            let read = "process.env.FLAG";
            let start = code.find(read).unwrap();
            env_guard(code, start, start + read.len(), &non_code_ranges(code))
                .map(EnvGuard::evidence)
        };
        let and = Some("the retained read controls a && short-circuit branch");
        let or = Some("the retained read controls a || short-circuit branch");
        let nullish = Some("the retained read controls a ?? fallback branch");
        let conditional = Some("the retained read controls a conditional (?:) branch");
        // The element of an array literal, or the property of an object
        // literal, is the value that reaches the operator.
        assert_eq!(guard("[process.env.FLAG][0]&&run()"), and);
        assert_eq!(guard("[process.env.FLAG] [0] && run()"), and);
        assert_eq!(guard("[process.env.FLAG]\n[0]&&run()"), and);
        assert_eq!(guard("[process.env.FLAG] /* c */ [0]&&run()"), and);
        assert_eq!(guard("x=[process.env.FLAG][0]&&run()"), and);
        assert_eq!(guard("[process.env.FLAG][0]||run()"), or);
        assert_eq!(guard("[process.env.FLAG][0]??run()"), nullish);
        assert_eq!(guard("[process.env.FLAG][0]?a():b()"), conditional);
        assert_eq!(guard("[process.env.FLAG].at(0)&&run()"), and);
        assert_eq!(guard("[process.env.FLAG]?.[0]&&run()"), and);
        assert_eq!(guard("[process.env.FLAG]?.at(0)&&run()"), and);
        assert_eq!(guard("[process.env.FLAG][0][0]&&run()"), and);
        assert_eq!(guard("x={a:process.env.FLAG}.a&&run()"), and);
        assert_eq!(guard("x={a:process.env.FLAG}[\"a\"]&&run()"), and);
        assert_eq!(
            guard("if([process.env.FLAG][0])run()"),
            Some("the retained read is in an if condition")
        );
        // Without a member access, or when a call, block or conditional
        // follows, the literal stays a plain value.
        for code in [
            "[process.env.FLAG]&&run()",
            "[process.env.FLAG](0)&&run()",
            "[process.env.FLAG]?.5:1",
            "[process.env.FLAG]\n(0)&&run()",
            "{a:process.env.FLAG}.a&&run()",
            "check(process.env.FLAG)[0]&&run()",
            "check(process.env.FLAG).a&&run()",
            "(function(){return process.env.FLAG}).a&&run()",
            "(function(){return process.env.FLAG})[0]&&run()",
            "[process.env.FLAG,1][1]&&run()",
        ] {
            assert_eq!(guard(code), None, "{code}");
        }
    }

    /// Short names for the guards a table row can expect.
    fn guard_name(guard: Option<EnvGuard>) -> Option<&'static str> {
        guard.map(|guard| match guard {
            EnvGuard::ShortCircuitAnd => "and",
            EnvGuard::ShortCircuitOr => "or",
            EnvGuard::NullishCoalescing => "nullish",
            EnvGuard::Conditional => "conditional",
            EnvGuard::If => "if",
            EnvGuard::While => "while",
            EnvGuard::Switch => "switch",
            EnvGuard::For => "for",
        })
    }

    fn flag_guard(code: &str) -> Option<&'static str> {
        let read = "process.env.FLAG";
        let start = code.find(read).unwrap();
        guard_name(env_guards(code, &[(start, start + read.len())], &non_code_ranges(code))[0])
    }

    /// One row per construct that a read can flow through before it reaches a
    /// branch operator. Each row is a cell of the read-propagation table in
    /// `docs/crates-bundlewhy-foundation.md`.
    #[test]
    fn read_propagation_rows() {
        // (construct, code, expected guard)
        let rows: &[(&str, &str, Option<&str>)] = &[
            // Operators that derive a value from the read keep it.
            ("equality", "'on'===process.env.FLAG&&run()", Some("and")),
            ("relational", "process.env.FLAG>1&&run()", Some("and")),
            ("arithmetic", "process.env.FLAG+1||run()", Some("or")),
            ("logical not", "!process.env.FLAG&&run()", Some("and")),
            ("unary minus", "-process.env.FLAG&&run()", Some("and")),
            ("unary plus", "+process.env.FLAG&&run()", Some("and")),
            ("bitwise not", "~process.env.FLAG&&run()", Some("and")),
            ("await", "await process.env.FLAG&&run()", Some("and")),
            ("in", "process.env.FLAG in obj&&run()", Some("and")),
            (
                "instanceof",
                "process.env.FLAG instanceof T&&run()",
                Some("and"),
            ),
            (
                "assignment target value",
                "x=process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "compound assignment value",
                "x+=process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "second operand of &&",
                "a&&process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "first operand of ||",
                "process.env.FLAG||a&&run()",
                Some("or"),
            ),
            (
                "plain assignment value",
                "x=process.env.FLAG;ready&&run()",
                None,
            ),
            (
                "plain initializer",
                "var x=process.env.FLAG,y=ready&&run()",
                None,
            ),
            // Members and calls of the read itself derive from the read.
            (
                "member of the read",
                "process.env.FLAG.length&&run()",
                Some("and"),
            ),
            (
                "computed member of the read",
                "process.env.FLAG[0]&&run()",
                Some("and"),
            ),
            (
                "optional member of the read",
                "process.env.FLAG?.x&&run()",
                Some("and"),
            ),
            (
                "method call on the read",
                "process.env.FLAG.trim()&&run()",
                Some("and"),
            ),
            ("call of the read", "process.env.FLAG()&&run()", Some("and")),
            (
                "read as computed key",
                "a[process.env.FLAG]&&run()",
                Some("and"),
            ),
            (
                "read as optional computed key",
                "a?.[process.env.FLAG]&&run()",
                Some("and"),
            ),
            // A call argument is not the call's value.
            ("call argument", "f(process.env.FLAG)&&run()", None),
            (
                "optional call argument",
                "f?.(process.env.FLAG)&&run()",
                None,
            ),
            ("new argument", "new F(process.env.FLAG)&&run()", None),
            (
                "spread call argument",
                "f(...process.env.FLAG)&&run()",
                None,
            ),
            ("spread array element", "[...process.env.FLAG]&&run()", None),
            (
                "member of a call",
                "f(process.env.FLAG).length&&run()",
                None,
            ),
            // Array literals: only the selected element is the value.
            ("array", "[process.env.FLAG]&&run()", None),
            ("array length", "[process.env.FLAG].length&&run()", None),
            (
                "array length (nested)",
                "[[process.env.FLAG]].length&&run()",
                None,
            ),
            (
                "array unrelated index",
                "[process.env.FLAG][1]&&run()",
                None,
            ),
            (
                "array first element",
                "[process.env.FLAG][0]&&run()",
                Some("and"),
            ),
            (
                "array first element (optional)",
                "[process.env.FLAG]?.[0]&&run()",
                Some("and"),
            ),
            (
                "array element at",
                "[process.env.FLAG].at(0)&&run()",
                Some("and"),
            ),
            (
                "array element at (optional)",
                "[process.env.FLAG]?.at(0)&&run()",
                Some("and"),
            ),
            ("array wrong at", "[process.env.FLAG].at(1)&&run()", None),
            (
                "array last element",
                "[a,process.env.FLAG][1]&&run()",
                Some("and"),
            ),
            (
                "array earlier element",
                "[a,process.env.FLAG][0]&&run()",
                None,
            ),
            ("array first of two", "[process.env.FLAG,a][0]&&run()", None),
            ("array hole", "[,process.env.FLAG][1]&&run()", Some("and")),
            (
                "array after spread",
                "[...a,process.env.FLAG][1]&&run()",
                None,
            ),
            ("array method", "[process.env.FLAG].map(f)&&run()", None),
            ("array join", "[process.env.FLAG].join('')&&run()", None),
            (
                "array includes",
                "[process.env.FLAG].includes(a)&&run()",
                None,
            ),
            (
                "array optional method",
                "[process.env.FLAG]?.map(f)&&run()",
                None,
            ),
            (
                "array computed string",
                "[process.env.FLAG][\"length\"]&&run()",
                None,
            ),
            (
                "array conditional",
                "[process.env.FLAG][0]?a():b()",
                Some("conditional"),
            ),
            ("array nullish", "[process.env.FLAG][0]??b", Some("nullish")),
            (
                "array in condition",
                "if([process.env.FLAG][0])run()",
                Some("if"),
            ),
            (
                "array length in condition",
                "if([process.env.FLAG].length)run()",
                None,
            ),
            // Object literals: only the selected property is the value.
            ("object", "x={a:process.env.FLAG}&&run()", None),
            (
                "object property",
                "x={a:process.env.FLAG}.a&&run()",
                Some("and"),
            ),
            (
                "object property (optional)",
                "x={a:process.env.FLAG}?.a&&run()",
                Some("and"),
            ),
            (
                "object computed property",
                "x={a:process.env.FLAG}[\"a\"]&&run()",
                Some("and"),
            ),
            (
                "object computed single quotes",
                "x={a:process.env.FLAG}['a']&&run()",
                Some("and"),
            ),
            (
                "object quoted key",
                "x={'a':process.env.FLAG}.a&&run()",
                Some("and"),
            ),
            (
                "object last property",
                "x={a:1,b:process.env.FLAG}.b&&run()",
                Some("and"),
            ),
            (
                "object other property",
                "x={a:process.env.FLAG}.b&&run()",
                None,
            ),
            (
                "object other computed property",
                "x={a:process.env.FLAG}['b']&&run()",
                None,
            ),
            (
                "object earlier property",
                "x={a:process.env.FLAG,b:1}.a&&run()",
                None,
            ),
            (
                "object inherited member",
                "x={a:process.env.FLAG}.toString()&&run()",
                None,
            ),
            (
                "object property prefix",
                "x={ab:process.env.FLAG}.a&&run()",
                None,
            ),
            (
                "object property longer",
                "x={a:process.env.FLAG}.ab&&run()",
                None,
            ),
            (
                "object conditional value",
                "x={a:c?d:process.env.FLAG}.a&&run()",
                Some("and"),
            ),
            // typeof, void and delete discard their operand's value.
            ("typeof", "typeof process.env.FLAG&&run()", None),
            (
                "typeof parenthesized",
                "typeof(process.env.FLAG)&&run()",
                None,
            ),
            ("typeof spaced", "typeof (process.env.FLAG)&&run()", None),
            (
                "typeof double parentheses",
                "typeof((process.env.FLAG))&&run()",
                None,
            ),
            (
                "typeof outside parentheses",
                "(typeof process.env.FLAG)&&run()",
                None,
            ),
            (
                "typeof comparison",
                "typeof process.env.FLAG==='string'&&run()",
                None,
            ),
            (
                "typeof inequality",
                "typeof process.env.FLAG!=='undefined'||run()",
                None,
            ),
            (
                "typeof conditional",
                "typeof process.env.FLAG==='string'?a():b()",
                None,
            ),
            (
                "typeof computed member",
                "typeof a[process.env.FLAG]&&run()",
                None,
            ),
            (
                "typeof member",
                "typeof process.env.FLAG.length&&run()",
                None,
            ),
            (
                "typeof array element",
                "typeof [process.env.FLAG][0]&&run()",
                None,
            ),
            (
                "typeof comment",
                "typeof/* c */process.env.FLAG&&run()",
                None,
            ),
            ("typeof in if", "if(typeof process.env.FLAG)run()", None),
            (
                "typeof parenthesized in if",
                "if(typeof(process.env.FLAG))run()",
                None,
            ),
            (
                "typeof comparison in if",
                "if(typeof process.env.FLAG==='string')run()",
                None,
            ),
            (
                "typeof in while",
                "while(typeof process.env.FLAG)run()",
                None,
            ),
            ("void", "void process.env.FLAG&&run()", None),
            ("void parenthesized", "void(process.env.FLAG)&&run()", None),
            ("delete", "delete process.env.FLAG&&run()", None),
            (
                "delete parenthesized",
                "delete(process.env.FLAG)&&run()",
                None,
            ),
            ("delete member", "delete a[process.env.FLAG]&&run()", None),
            (
                "typeof inner branch",
                "typeof(process.env.FLAG&&a)&&run()",
                Some("and"),
            ),
            (
                "typeof then comparison read",
                "typeof a===process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "typeof then second operand",
                "typeof a&&process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "typeof then conditional",
                "typeof a?process.env.FLAG:b",
                None,
            ),
            (
                "typeof then sequence",
                "typeof a,process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "typeof then statement",
                "typeof a;process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "typeof on a new line",
                "typeof a\nprocess.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "typeof in a nested scope",
                "typeof f(a,process.env.FLAG)&&run()",
                None,
            ),
            (
                "typeof property",
                "a.typeof+process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "void property",
                "a.void+process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "delete property",
                "a.delete+process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "typeof prefix word",
                "mytypeof+process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "typeof suffix word",
                "typeofa+process.env.FLAG&&run()",
                Some("and"),
            ),
            // Immediately invoked functions return their value.
            (
                "function object",
                "(function(){return process.env.FLAG})&&run()",
                None,
            ),
            (
                "function object call member",
                "(function(){return process.env.FLAG}).call()&&run()",
                None,
            ),
            ("arrow object", "(()=>process.env.FLAG)&&run()", None),
            (
                "iife",
                "(function(){return process.env.FLAG})()&&run()",
                Some("and"),
            ),
            (
                "iife semicolon",
                "(function(){return process.env.FLAG;})()&&run()",
                Some("and"),
            ),
            (
                "iife spaced",
                "(function () { return process.env.FLAG } ) ( ) && run()",
                Some("and"),
            ),
            (
                "iife inner call",
                "(function(){return process.env.FLAG}())&&run()",
                Some("and"),
            ),
            (
                "iife inner call semicolon",
                "(function(){return process.env.FLAG;}())&&run()",
                Some("and"),
            ),
            (
                "iife named",
                "(function f(){return process.env.FLAG})()&&run()",
                Some("and"),
            ),
            (
                "iife with arguments",
                "(function(a){return process.env.FLAG})(1)&&run()",
                Some("and"),
            ),
            (
                "iife prefix not",
                "!function(){return process.env.FLAG}()&&run()",
                Some("and"),
            ),
            (
                "iife prefix void",
                "void function(){return process.env.FLAG}()&&run()",
                None,
            ),
            (
                "iife arrow block",
                "(()=>{return process.env.FLAG})()&&run()",
                Some("and"),
            ),
            (
                "iife arrow expression",
                "(()=>process.env.FLAG)()&&run()",
                Some("and"),
            ),
            (
                "iife arrow parameters",
                "((a,b)=>process.env.FLAG)(1,2)&&run()",
                Some("and"),
            ),
            (
                "iife arrow single parameter",
                "(a=>process.env.FLAG)(1)&&run()",
                Some("and"),
            ),
            (
                "iife conditional",
                "(function(){return process.env.FLAG})()?a():b()",
                Some("conditional"),
            ),
            (
                "iife nullish",
                "(function(){return process.env.FLAG})()??b",
                Some("nullish"),
            ),
            (
                "iife or",
                "(function(){return process.env.FLAG})()||b",
                Some("or"),
            ),
            (
                "iife in if",
                "if((function(){return process.env.FLAG})())run()",
                Some("if"),
            ),
            (
                "iife arrow in if",
                "if((()=>process.env.FLAG)())run()",
                Some("if"),
            ),
            (
                "iife assignment",
                "x=(function(){return process.env.FLAG})()&&run()",
                Some("and"),
            ),
            (
                "iife nested statement",
                "(function(){if(a){return process.env.FLAG}})()&&run()",
                Some("and"),
            ),
            (
                "iife unbraced return",
                "(function(){if(a)return process.env.FLAG;return 0})()&&run()",
                Some("and"),
            ),
            (
                "iife inner branch",
                "(function(){return process.env.FLAG&&a})()||run()",
                Some("and"),
            ),
            (
                "iife no return",
                "(function(){process.env.FLAG})()&&run()",
                None,
            ),
            (
                "iife statement before return",
                "(function(){process.env.FLAG;return 1})()&&run()",
                None,
            ),
            (
                "iife return then read",
                "(function(){return 1;process.env.FLAG})()&&run()",
                None,
            ),
            (
                "iife return on its own line",
                "(function(){return\nprocess.env.FLAG})()&&run()",
                None,
            ),
            (
                "iife comma last",
                "(function(){return a,process.env.FLAG})()&&run()",
                Some("and"),
            ),
            (
                "iife comma first",
                "(function(){return process.env.FLAG,a})()&&run()",
                None,
            ),
            (
                "iife as argument",
                "f((function(){return process.env.FLAG})())&&run()",
                None,
            ),
            (
                "iife async function",
                "(async function(){return process.env.FLAG})()&&run()",
                None,
            ),
            (
                "iife async arrow",
                "(async()=>process.env.FLAG)()&&run()",
                None,
            ),
            (
                "iife async arrow parameter",
                "(async a=>process.env.FLAG)(1)&&run()",
                None,
            ),
            (
                "iife async arrow block",
                "(async()=>{return process.env.FLAG})()&&run()",
                None,
            ),
            (
                "iife generator",
                "(function*(){return process.env.FLAG})()&&run()",
                None,
            ),
            (
                "iife typeof",
                "typeof (function(){return process.env.FLAG})()&&run()",
                None,
            ),
            (
                "iife method",
                "({m(){return process.env.FLAG}}).m()&&run()",
                None,
            ),
            (
                "iife class method",
                "(class{m(){return process.env.FLAG}})&&run()",
                None,
            ),
            (
                "iife not last in group",
                "(function(){return process.env.FLAG},a)()&&run()",
                None,
            ),
            // Comma and conditional expressions.
            ("sequence last", "(a,process.env.FLAG)&&run()", Some("and")),
            ("sequence first", "(process.env.FLAG,a)&&run()", None),
            (
                "sequence statement",
                "a,process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "conditional test",
                "process.env.FLAG?a:b",
                Some("conditional"),
            ),
            (
                "conditional consequent",
                "ready?process.env.FLAG:other",
                None,
            ),
            (
                "conditional consequent then &&",
                "ready?process.env.FLAG:other&&run()",
                None,
            ),
            (
                "conditional group",
                "(ready?process.env.FLAG:other)&&run()",
                Some("and"),
            ),
            (
                "conditional group alternate",
                "(ready?other:process.env.FLAG)||run()",
                Some("or"),
            ),
            (
                "conditional in if",
                "if(ready?process.env.FLAG:other)run()",
                Some("if"),
            ),
        ];
        for &(construct, code, expected) in rows {
            assert_eq!(flag_guard(code), expected, "{construct}: {code}");
        }
    }

    /// Constructs that need a real parser. The scanner picks the direction in
    /// which the read is not treated as guarded, so the finding stays an
    /// inference only when the generated code shows the branch. Each row is
    /// listed under "Known limits" in `docs/crates-bundlewhy-foundation.md`.
    #[test]
    fn read_propagation_known_limits_are_not_guarded() {
        for (limit, code) in [
            (
                "array element selected by a negative or non-literal index",
                "[process.env.FLAG].at(-1)&&run()",
            ),
            (
                "array element selected through a string index",
                "[process.env.FLAG][\"0\"]&&run()",
            ),
            (
                "array element selected through a variable index",
                "[process.env.FLAG][index]&&run()",
            ),
            (
                "array element selected through a nested literal",
                "[[process.env.FLAG]][0][0]&&run()",
            ),
            (
                "array element selected through a method",
                "[process.env.FLAG].find(Boolean)&&run()",
            ),
            (
                "object property with a computed key",
                "x={[key]:process.env.FLAG}.key&&run()",
            ),
            (
                "object property reached through a spread",
                "x={...{a:process.env.FLAG}}.a&&run()",
            ),
            (
                "object property reached through a getter",
                "x={get a(){return process.env.FLAG}}.a&&run()",
            ),
            (
                "operand of typeof extended by a binary operator",
                "typeof a+process.env.FLAG&&run()",
            ),
            (
                "function value called through call",
                "(function(){return process.env.FLAG}).call(this)&&run()",
            ),
            (
                "function value called through a variable",
                "var f=function(){return process.env.FLAG};f()&&run()",
            ),
            (
                "function returned by an immediately invoked function",
                "(function(){return function(){return process.env.FLAG}})()()&&run()",
            ),
        ] {
            assert_eq!(flag_guard(code), None, "{limit}: {code}");
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
