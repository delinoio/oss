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
    // Whether the previous token was the `}` that closed a statement block.
    let mut after_block = false;
    // Open `{` blocks; each entry says whether the block is a statement block.
    let mut blocks: Vec<bool> = Vec::new();
    // Scans a quoted string or template text starting after the opener.
    let scan_until = |mut position: usize, terminator: u8, template: bool| -> (usize, bool) {
        while position < bytes.len() {
            match bytes[position] {
                b'\\'
                    if bytes.get(position + 1) == Some(&b'\r')
                        && bytes.get(position + 2) == Some(&b'\n') =>
                {
                    position += 3;
                }
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
                after_block = false;
            }
            b'`' => {
                let (end, opened) = scan_until(index + 1, b'`', true);
                ranges.push((index, end));
                if opened {
                    templates.push(0);
                }
                index = end;
                previous_significant = Some(b'"');
                after_block = false;
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
                after_block = false;
            }
            b'/' if bytes.get(index + 1) == Some(&b'/') => {
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
            b'/' if regex_allowed(bytes, index, previous_significant, after_block, &ranges) => {
                let mut position = index + 1;
                let mut in_class = false;
                let mut closed = false;
                while position < bytes.len() {
                    if line_terminator_at(bytes, position).is_some() {
                        break;
                    }
                    match bytes[position] {
                        b'\\' => position += 1,
                        b'[' => in_class = true,
                        b']' => in_class = false,
                        b'/' if !in_class => {
                            closed = true;
                            break;
                        }
                        _ => {}
                    }
                    position += 1;
                }
                // An unterminated literal stops before its line terminator so
                // that the terminator stays visible to the ASI decision.
                let end = if closed {
                    position + 1
                } else {
                    position.min(bytes.len())
                };
                ranges.push((index, end));
                index = end;
                previous_significant = Some(b'"');
                after_block = false;
            }
            _ => {
                if let Some(depth) = templates.last_mut() {
                    if byte == b'{' {
                        *depth += 1;
                    } else if byte == b'}' {
                        *depth -= 1;
                    }
                }
                if let Some(character) = char_at(bytes, index)
                    && (is_js_whitespace(character) || is_line_terminator(character))
                {
                    index += character.len_utf8();
                    continue;
                }
                after_block = false;
                match byte {
                    b'{' => blocks.push(opens_statement_block(bytes, index, &ranges)),
                    b'}' => after_block = blocks.pop().unwrap_or(false),
                    _ => {}
                }
                previous_significant = Some(byte);
                index += 1;
            }
        }
    }
    ranges
}

/// Whether a `/` after the previous token starts a regular expression literal.
/// `previous` is the last significant byte; a word is checked as a token, so
/// trivia between the keyword and the slash never matters. A `}` that closed a
/// statement block ends a statement, so a slash after it starts a new one.
fn regex_allowed(
    bytes: &[u8],
    start: usize,
    previous: Option<u8>,
    after_block: bool,
    ranges: &[(usize, usize)],
) -> bool {
    match previous {
        None => true,
        Some(b'}') => after_block,
        Some(byte) if b"(,=:[!&|?{};+-*%<>~^".contains(&byte) => true,
        Some(byte) if byte.is_ascii_alphabetic() => previous_token_end(bytes, start, ranges)
            .is_some_and(|end| {
                matches!(
                    token_ending_at(bytes, end, ranges),
                    TokenBefore::Keyword(word) if REGEX_KEYWORDS.contains(&word)
                )
            }),
        _ => false,
    }
}

fn in_ranges(ranges: &[(usize, usize)], offset: usize) -> bool {
    let index = ranges.partition_point(|(start, _)| *start <= offset);
    index > 0 && offset < ranges[index - 1].1
}

/// ECMAScript `WhiteSpace`: tab, vertical tab, form feed, space, no-break
/// space, zero-width no-break space (BOM) and every Unicode `Zs` code point.
fn is_js_whitespace(character: char) -> bool {
    matches!(
        character,
        '\u{0009}' | '\u{000B}' | '\u{000C}' | '\u{0020}' | '\u{00A0}' | '\u{1680}' | '\u{2000}'
            ..='\u{200A}' | '\u{202F}' | '\u{205F}' | '\u{3000}' | '\u{FEFF}'
    )
}

/// ECMAScript `LineTerminator`: LF, CR, LS (U+2028) and PS (U+2029).
fn is_line_terminator(character: char) -> bool {
    matches!(character, '\n' | '\r' | '\u{2028}' | '\u{2029}')
}

/// Decodes the UTF-8 character that starts at `index`.
fn char_at(bytes: &[u8], index: usize) -> Option<char> {
    let length = match *bytes.get(index)? {
        0x00..=0x7f => 1,
        0xc0..=0xdf => 2,
        0xe0..=0xef => 3,
        0xf0..=0xf7 => 4,
        _ => return None,
    };
    std::str::from_utf8(bytes.get(index..index + length)?)
        .ok()?
        .chars()
        .next()
}

/// Decodes the UTF-8 character that ends right before `end`.
fn char_before(bytes: &[u8], end: usize) -> Option<char> {
    let mut start = end.checked_sub(1)?;
    while start > 0 && end - start < 4 && bytes[start] & 0xc0 == 0x80 {
        start -= 1;
    }
    char_at(bytes, start).filter(|character| start + character.len_utf8() == end)
}

/// Byte length of the line terminator that starts at `index`, if any.
fn line_terminator_at(bytes: &[u8], index: usize) -> Option<usize> {
    char_at(bytes, index)
        .filter(|character| is_line_terminator(*character))
        .map(char::len_utf8)
}

/// The comment range (`//...` or `/*...*/`) that contains `index`, if any.
/// Other ignored ranges (strings, templates, regular expressions) are tokens.
fn comment_range(bytes: &[u8], index: usize, ignored: &[(usize, usize)]) -> Option<(usize, usize)> {
    let position = ignored.partition_point(|(start, _)| *start <= index);
    let (start, end) = *ignored.get(position.checked_sub(1)?)?;
    (index < end && matches!(bytes.get(start..start + 2), Some(b"//" | b"/*")))
        .then_some((start, end))
}

/// True for a `/* ... */` comment that contains a line terminator. JavaScript
/// treats such a comment as a line terminator.
fn comment_has_line_terminator(bytes: &[u8], start: usize, end: usize) -> bool {
    bytes.get(start..start + 2) == Some(b"/*")
        && std::str::from_utf8(&bytes[start..end])
            .is_ok_and(|text| text.contains(is_line_terminator))
}

/// Result of skipping the trivia between two tokens.
struct Trivia {
    /// Start of the next token, or `None` at the end of the input.
    next: Option<usize>,
    /// Whether a line terminator (including one inside a block comment) was
    /// crossed on the way.
    crossed_terminator: bool,
}

/// Skips every character that separates two tokens: JavaScript whitespace,
/// line terminators and comments. Strings, templates and regular expressions
/// are tokens and stop the scan.
fn skip_trivia(bytes: &[u8], start: usize, ignored: &[(usize, usize)]) -> Trivia {
    let mut index = start;
    let mut crossed_terminator = false;
    while index < bytes.len() {
        if in_ranges(ignored, index) {
            let Some((comment_start, comment_end)) = comment_range(bytes, index, ignored) else {
                break;
            };
            crossed_terminator |= comment_has_line_terminator(bytes, comment_start, comment_end);
            index = comment_end;
            continue;
        }
        match char_at(bytes, index) {
            Some(character) if is_js_whitespace(character) => index += character.len_utf8(),
            Some(character) if is_line_terminator(character) => {
                crossed_terminator = true;
                index += character.len_utf8();
            }
            _ => {
                return Trivia {
                    next: Some(index),
                    crossed_terminator,
                };
            }
        }
    }
    Trivia {
        next: (index < bytes.len()).then_some(index),
        crossed_terminator,
    }
}

fn next_code_byte(bytes: &[u8], start: usize, ignored: &[(usize, usize)]) -> Option<u8> {
    skip_trivia(bytes, start, ignored)
        .next
        .map(|index| bytes[index])
}

/// Returns true when the next code token is exactly `operator` doubled
/// (`++` or `--`) with no whitespace or comment between the two bytes.
fn is_adjacent_doubled_operator(
    bytes: &[u8],
    token: usize,
    operator: u8,
    ignored: &[(usize, usize)],
) -> bool {
    bytes.get(token + 1) == Some(&operator) && !in_ranges(ignored, token + 1)
}

fn is_identifier_byte(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'$') || byte >= 0x80
}

/// End (exclusive) of the last token before `end`, skipping whitespace, line
/// terminators and comments backwards.
fn previous_token_end(bytes: &[u8], end: usize, ignored: &[(usize, usize)]) -> Option<usize> {
    let mut position = end;
    while position > 0 {
        if let Some((comment_start, _)) = comment_range(bytes, position - 1, ignored) {
            position = comment_start;
            continue;
        }
        match char_before(bytes, position) {
            Some(character) if is_js_whitespace(character) || is_line_terminator(character) => {
                position -= character.len_utf8();
            }
            _ => return Some(position),
        }
    }
    None
}

/// True for a character that can be part of an identifier-like word. Every
/// non-ASCII character counts except ECMAScript whitespace and line
/// terminators, which separate tokens.
fn is_identifier_char(character: char) -> bool {
    character.is_ascii_alphanumeric()
        || matches!(character, '_' | '$')
        || (!character.is_ascii() && !is_js_whitespace(character) && !is_line_terminator(character))
}

/// The identifier-like word that ends at `end`, if the token there is one.
fn word_before(bytes: &[u8], end: usize) -> &str {
    let mut start = end;
    while let Some(character) = char_before(bytes, start).filter(|c| is_identifier_char(*c)) {
        start -= character.len_utf8();
    }
    std::str::from_utf8(&bytes[start..end]).unwrap_or("")
}

/// The identifier-like word that starts at `start`.
fn word_at(bytes: &[u8], start: usize) -> &str {
    let mut end = start;
    while let Some(character) = char_at(bytes, end).filter(|c| is_identifier_char(*c)) {
        end += character.len_utf8();
    }
    std::str::from_utf8(&bytes[start..end]).unwrap_or("")
}

/// True when the word that starts at `start` is a property name, that is, the
/// previous token (trivia skipped) is a member-access `.` or `?.` and not the
/// end of a `...` spread.
fn is_property_name(bytes: &[u8], start: usize, ignored: &[(usize, usize)]) -> bool {
    previous_token_end(bytes, start, ignored).is_some_and(|end| {
        bytes[end - 1] == b'.' && !in_ranges(ignored, end - 1) && !bytes[..end].ends_with(b"...")
    })
}

/// Keywords after which a `/` starts a regular expression literal.
const REGEX_KEYWORDS: &[&str] = &[
    "return",
    "typeof",
    "case",
    "do",
    "else",
    "in",
    "of",
    "void",
    "yield",
    "delete",
    "throw",
    "new",
    "instanceof",
    "await",
];

/// Keywords whose grammar cannot finish at a line break: they need an operand
/// (`typeof`, `in`, `case`), a body (`function`, `class`, `else`, `try`) or a
/// parenthesized head (`if`, `for`, `while`). `return`, `throw`, `break`,
/// `continue` and `yield` are restricted productions and are deliberately
/// absent.
const OPERAND_KEYWORDS: &[&str] = &[
    "typeof",
    "void",
    "delete",
    "new",
    "in",
    "instanceof",
    "await",
    "case",
    "function",
    "class",
    "extends",
    "if",
    "else",
    "while",
    "for",
    "switch",
    "with",
    "catch",
    "do",
    "try",
    "finally",
    "var",
    "const",
];

/// The token that ends at a given position, as seen by every lookup of the
/// env-condition scan. Keeping the classification in one place means that
/// regular expression detection, automatic semicolon insertion and scope
/// detection agree on keywords, property names, private names and numbers.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum TokenBefore<'a> {
    /// A word from [`REGEX_KEYWORDS`] or [`OPERAND_KEYWORDS`] in keyword
    /// position.
    Keyword(&'a str),
    /// Any other identifier, a property name after `.` or `?.`, or a private
    /// name such as `#typeof`.
    Name,
    /// A numeric literal, including one with a trailing decimal point (`1.`).
    Number,
    /// A string, template text, or regular expression literal.
    Literal,
    /// Template text that ends by opening a `${` substitution.
    SubstitutionOpen,
    /// A punctuator ending in this byte.
    Punct(u8),
}

/// Classifies the token whose last byte is right before `end`. `end` must come
/// from [`previous_token_end`], so no trivia sits at the end of the token.
fn token_ending_at<'a>(bytes: &'a [u8], end: usize, ignored: &[(usize, usize)]) -> TokenBefore<'a> {
    let last = bytes[end - 1];
    if in_ranges(ignored, end - 1) {
        return if ends_template_substitution(bytes, end - 1, ignored) {
            TokenBefore::SubstitutionOpen
        } else {
            TokenBefore::Literal
        };
    }
    if is_identifier_byte(last) {
        let word = word_before(bytes, end);
        let start = end - word.len();
        return if word.is_empty() {
            TokenBefore::Name
        } else if start > 0 && bytes[start - 1] == b'#' && !in_ranges(ignored, start - 1) {
            // A private name is never a keyword.
            TokenBefore::Name
        } else if word.as_bytes()[0].is_ascii_digit() {
            TokenBefore::Number
        } else if is_property_name(bytes, start, ignored) {
            TokenBefore::Name
        } else if REGEX_KEYWORDS.contains(&word) || OPERAND_KEYWORDS.contains(&word) {
            TokenBefore::Keyword(word)
        } else {
            TokenBefore::Name
        };
    }
    if last == b'.' && dot_is_decimal_point(bytes, end - 1) {
        return TokenBefore::Number;
    }
    TokenBefore::Punct(last)
}

/// True when the `.` at `dot` is the decimal point of a numeric literal such
/// as `1.`: the token before it is made of decimal digits only. A `.` after a
/// name, a fraction (`1.5.`), an exponent, a prefix (`0x1.`) or a bigint
/// suffix is member access.
fn dot_is_decimal_point(bytes: &[u8], dot: usize) -> bool {
    let mut start = dot;
    while let Some(character) =
        char_before(bytes, start).filter(|c| is_identifier_char(*c) || *c == '.')
    {
        start -= character.len_utf8();
    }
    let run = &bytes[start..dot];
    run.first().is_some_and(u8::is_ascii_digit)
        && run
            .iter()
            .all(|byte| byte.is_ascii_digit() || *byte == b'_')
}

/// True when the token that ends at `end` can end an operand (identifier,
/// literal, closing bracket), so that a following `++` or `--` is postfix.
fn token_ends_operand(bytes: &[u8], end: usize, ignored: &[(usize, usize)]) -> bool {
    match token_ending_at(bytes, end, ignored) {
        TokenBefore::Literal | TokenBefore::Name | TokenBefore::Number => true,
        TokenBefore::Keyword(word) => !OPERAND_KEYWORDS.contains(&word),
        TokenBefore::SubstitutionOpen => false,
        TokenBefore::Punct(byte) => matches!(byte, b')' | b']' | b'}'),
    }
}

/// True when the `{` at `index` opens a block (statement block, function body
/// or class body) rather than an object literal. A closing `}` of a block ends
/// a statement, so the next token starts a new one whatever it is.
///
/// This is a lexical guess from the previous token. A function or class
/// expression body (`x = function () {}`) is treated as a block as well.
fn opens_statement_block(bytes: &[u8], index: usize, ignored: &[(usize, usize)]) -> bool {
    let Some(end) = previous_token_end(bytes, index, ignored) else {
        return true;
    };
    match token_ending_at(bytes, end, ignored) {
        TokenBefore::Punct(b';' | b'{' | b'}' | b')') | TokenBefore::Name => true,
        TokenBefore::Punct(b':') => colon_opens_block(bytes, end, ignored),
        TokenBefore::Keyword(word) => {
            matches!(word, "else" | "do" | "try" | "finally" | "catch" | "class")
        }
        _ => false,
    }
}

/// True when the `:` that ends at `colon_end` belongs to a label or a `case`
/// clause, so that a `{` after it opens a block. Any other `:` (property
/// value, conditional branch, type annotation) is followed by an object
/// literal. A label counts only after a statement boundary (`;`, `}` or the
/// start of the text) and a `case` clause only for a single-token test, because
/// a lexical scan cannot tell a label from a property name after `{`.
fn colon_opens_block(bytes: &[u8], colon_end: usize, ignored: &[(usize, usize)]) -> bool {
    let Some(label_end) = previous_token_end(bytes, colon_end - 1, ignored) else {
        return false;
    };
    let token = token_ending_at(bytes, label_end, ignored);
    let label_start = match token {
        TokenBefore::Name | TokenBefore::Number => label_end - word_before(bytes, label_end).len(),
        TokenBefore::Literal => {
            let position = ignored.partition_point(|(start, _)| *start < label_end);
            match position.checked_sub(1).and_then(|at| ignored.get(at)) {
                Some(&(start, _)) => start,
                None => return false,
            }
        }
        _ => return false,
    };
    let Some(before_end) = previous_token_end(bytes, label_start, ignored) else {
        return token == TokenBefore::Name;
    };
    match token_ending_at(bytes, before_end, ignored) {
        TokenBefore::Keyword("case") => true,
        TokenBefore::Punct(b';' | b'}') => token == TokenBefore::Name,
        _ => false,
    }
}

/// True when the ignored range that holds `index` is template text that ends
/// by opening a `${` substitution.
fn ends_template_substitution(bytes: &[u8], index: usize, ignored: &[(usize, usize)]) -> bool {
    let position = ignored.partition_point(|(start, _)| *start <= index);
    let Some(&(start, end)) = position.checked_sub(1).and_then(|at| ignored.get(at)) else {
        return false;
    };
    matches!(bytes[start], b'`' | b'}') && end - start >= 2 && &bytes[end - 2..end] == b"${"
}

/// True when the tokens before `end` leave an expression unfinished, such as a
/// trailing binary operator, so that the next line continues it.
fn expression_is_unfinished(bytes: &[u8], end: usize, ignored: &[(usize, usize)]) -> bool {
    let Some(token_end) = previous_token_end(bytes, end, ignored) else {
        return false;
    };
    match token_ending_at(bytes, token_end, ignored) {
        TokenBefore::SubstitutionOpen => true,
        TokenBefore::Literal | TokenBefore::Name | TokenBefore::Number => false,
        TokenBefore::Keyword(word) => OPERAND_KEYWORDS.contains(&word),
        TokenBefore::Punct(sign @ (b'+' | b'-')) => {
            // Tokens are matched greedily, so a run of an odd length ends in
            // a single binary or unary sign.
            let mut run_start = token_end - 1;
            while run_start > 0
                && bytes[run_start - 1] == sign
                && !in_ranges(ignored, run_start - 1)
            {
                run_start -= 1;
            }
            if (token_end - run_start) % 2 == 1 {
                return true;
            }
            // A trailing `++` or `--` is postfix after an operand and a prefix
            // operator otherwise.
            !previous_token_end(bytes, run_start, ignored)
                .is_some_and(|before| token_ends_operand(bytes, before, ignored))
        }
        TokenBefore::Punct(
            b'!' | b'%' | b'&' | b'(' | b'*' | b',' | b'.' | b'/' | b':' | b'<' | b'=' | b'>'
            | b'?' | b'[' | b'^' | b'|' | b'~',
        ) => true,
        TokenBefore::Punct(_) => false,
    }
}

/// Decides whether a line break ends the current statement. `start` is the
/// first byte of the line break: a line terminator, or a block comment that
/// contains one.
///
/// `block_close` is the position of the most recent `}` that closed a
/// statement block. A line break right after it always ends the statement,
/// because a block statement needs no semicolon and the next token, even a
/// `/`, `(`, `[` or template, starts a new statement.
fn asi_terminates(
    bytes: &[u8],
    start: usize,
    ignored: &[(usize, usize)],
    block_close: Option<usize>,
) -> bool {
    let trivia = skip_trivia(bytes, start, ignored);
    let Some(token) = trivia.next else {
        return true;
    };
    if !trivia.crossed_terminator {
        return false;
    }
    if let Some(close) = block_close
        && previous_token_end(bytes, start, ignored) == Some(close + 1)
    {
        return true;
    }
    // No semicolon is inserted while the previous line leaves an expression
    // unfinished, whatever starts the next line.
    if expression_is_unfinished(bytes, start, ignored) {
        return false;
    }
    match bytes[token] {
        // A prefix `++`/`--` starts a new statement after a line terminator
        // (restricted production), while a single `+`/`-` continues the
        // current expression as a binary operator.
        operator @ (b'+' | b'-') => is_adjacent_doubled_operator(bytes, token, operator, ignored),
        // A template literal after an expression is a tagged template.
        b'`' => false,
        b'!' | b'%' | b'&' | b'(' | b')' | b'*' | b',' | b'.' | b'/' | b':' | b';' | b'<'
        | b'=' | b'>' | b'?' | b'[' | b']' | b'^' | b'{' | b'}' | b'|' | b'~' => false,
        _ => !matches!(word_at(bytes, token), "in" | "instanceof"),
    }
}

/// Remembers the ASI decision for the trivia run between two tokens. Every
/// line break in one run sees the same previous and next token, so the decision
/// is computed once per run instead of once per line break.
#[derive(Default)]
struct AsiMemo {
    /// End of the trivia run and the decision for it.
    run: Option<(usize, bool)>,
}

impl AsiMemo {
    /// Same as [`asi_terminates`] for the line break at `start`. Line breaks
    /// must be queried in increasing order.
    fn terminates(
        &mut self,
        bytes: &[u8],
        start: usize,
        ignored: &[(usize, usize)],
        block_close: Option<usize>,
    ) -> bool {
        if let Some((run_end, decision)) = self.run
            && start < run_end
        {
            return decision;
        }
        let run_end = skip_trivia(bytes, start, ignored)
            .next
            .unwrap_or(bytes.len());
        let decision = asi_terminates(bytes, start, ignored, block_close);
        self.run = Some((run_end, decision));
        decision
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
    /// A `{` block (statement block, function body or class body): its
    /// closing `}` ends a statement, so its reads never reach the parent.
    statement_block: bool,
}

/// Marks the unguarded reads that are still pending in `scope` as blocked: a
/// statement boundary ended them before any branch operator used them.
fn block_pending_reads(scope: &mut ScanScope, guards: &[Option<EnvGuard>], blocked: &mut [bool]) {
    for read in scope
        .pending_reads
        .drain(..)
        .chain(scope.conditional_pending.drain(..))
    {
        if guards[read].is_none() {
            blocked[read] = true;
        }
    }
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
        statement_block: false,
    }];
    let mut current_scope = 0;
    let mut next_start = 0;
    let mut next_end = 0;
    let mut ignored_index = 0;
    let mut asi = AsiMemo::default();
    let mut block_close: Option<usize> = None;

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
            // A block comment that contains a line terminator is itself a
            // line break; the rest of an ignored range never matters.
            let (start, end) = ignored[ignored_index];
            if index == start
                && comment_has_line_terminator(bytes, start, end)
                && asi.terminates(bytes, start, ignored, block_close)
            {
                block_pending_reads(&mut scopes[current_scope], &guards, &mut blocked);
            }
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

        if line_terminator_at(bytes, index).is_some()
            && asi.terminates(bytes, index, ignored, block_close)
        {
            block_pending_reads(&mut scopes[current_scope], &guards, &mut blocked);
        }

        match bytes[index] {
            b'(' | b'[' | b'{' => {
                let context = scope_context(
                    bytes,
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
                    statement_block: bytes[index] == b'{'
                        && opens_statement_block(bytes, index, ignored),
                });
                current_scope = scopes.len() - 1;
            }
            b'}' if current_scope != 0 && scopes[current_scope].statement_block => {
                block_pending_reads(&mut scopes[current_scope], &guards, &mut blocked);
                block_close = Some(index);
                current_scope = scopes[current_scope].parent;
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
                        statement_block: false,
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

fn scope_context(
    bytes: &[u8],
    index: usize,
    opener: u8,
    parent: ScopeContext,
    ignored: &[(usize, usize)],
) -> ScopeContext {
    let before =
        previous_token_end(bytes, index, ignored).map(|end| token_ending_at(bytes, end, ignored));
    if opener == b'[' {
        // A bracket after an expression is a computed member access and keeps
        // the surrounding condition. A bracket after an operator starts an
        // array literal, whose contents do not form a guard by themselves.
        return match before {
            Some(
                TokenBefore::Name
                | TokenBefore::Number
                | TokenBefore::Keyword(_)
                | TokenBefore::Literal
                | TokenBefore::Punct(b']' | b')' | b'}' | b'.'),
            ) => parent,
            _ => ScopeContext::Invalid,
        };
    }
    if opener != b'(' {
        return ScopeContext::Invalid;
    }
    match before {
        Some(TokenBefore::Keyword("for")) => ScopeContext::For(ForClause::Initializer),
        Some(TokenBefore::Keyword("if")) => ScopeContext::Guard(EnvGuard::If),
        Some(TokenBefore::Keyword("while")) => ScopeContext::Guard(EnvGuard::While),
        Some(TokenBefore::Keyword("switch")) => ScopeContext::Guard(EnvGuard::Switch),
        Some(
            TokenBefore::Name
            | TokenBefore::Number
            | TokenBefore::Keyword(_)
            | TokenBefore::Punct(b']' | b')' | b'.'),
        ) => ScopeContext::Invalid,
        _ => parent,
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
    fn blocks_after_labels_and_case_clauses_are_statement_blocks() {
        // Each cell: source, whether the first `{` opens a statement block.
        let cells: &[(&str, bool)] = &[
            ("a;label: {", true),
            ("label: {", true),
            ("x();\nlabel: {", true),
            ("if (a) { b }\nlabel: {", true),
            ("switch (a) { case 1: {", true),
            ("switch (a) { case \"s\": {", true),
            ("switch (a) { case b: {", true),
            ("switch (a) { case 1: x(); break; default: {", true),
            // Object literals keep their reading.
            ("x = { a: {", false),
            ("x = c ? d : {", false),
            ("f(a, b ? c : {", false),
            ("x = { default: {", false),
            ("x = { a: 1, b: {", false),
            ("x = { 'a': {", false),
            ("x = y.z ? w.v : {", false),
        ];
        for (source, expected) in cells {
            let index = source.rfind('{').unwrap();
            let ranges = non_code_ranges(&source[..index]);
            assert_eq!(
                opens_statement_block(source.as_bytes(), index, &ranges),
                *expected,
                "{source}"
            );
        }
    }

    #[test]
    fn regex_after_a_labeled_or_case_block_is_not_code() {
        for source in [
            "label: { work(); }\n/process.env.FLAG&&run/.test(source)",
            "switch (a) { case 1: { work(); }\n/process.env.FLAG&&run/.test(s) }",
        ] {
            let ranges = non_code_ranges(source);
            assert!(
                in_ranges(&ranges, source.find("process.env").unwrap()),
                "{source}"
            );
        }
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
    fn env_guard_newline_plus_minus_follows_asi_rules() {
        let guard = |code: &str| {
            let read = "process.env.FLAG";
            let start = code.find(read).unwrap();
            env_guard(code, start, start + read.len(), &non_code_ranges(code))
                .map(EnvGuard::evidence)
        };
        let and = Some("the retained read controls a && short-circuit branch");
        // A single sign after a newline continues the arithmetic expression.
        for code in [
            "process.env.FLAG\n+suffix&&run()",
            "process.env.FLAG\n-suffix&&run()",
            "process.env.FLAG\n  +suffix&&run()",
            "process.env.FLAG\n  -suffix&&run()",
            "process.env.FLAG /* c */\n+suffix&&run()",
            "process.env.FLAG\n/* c */ -suffix&&run()",
            "process.env.FLAG\n+ +suffix&&run()",
            "process.env.FLAG\n- -suffix&&run()",
            "process.env.FLAG\n+/* c */+suffix&&run()",
            "process.env.FLAG\n-/* c */-suffix&&run()",
            "process.env.FLAG+suffix&&run()",
        ] {
            assert_eq!(guard(code), and, "{code:?}");
        }
        // A doubled prefix update after a newline starts a new statement.
        for code in [
            "process.env.FLAG\n++suffix&&run()",
            "process.env.FLAG\n--suffix&&run()",
            "process.env.FLAG\n  ++suffix&&run()",
            "process.env.FLAG\n  --suffix&&run()",
            "process.env.FLAG /* c */\n++suffix&&run()",
            "process.env.FLAG\n/* c */ --suffix&&run()",
            "process.env.FLAG;\nready&&run()",
            "console.log(process.env.FLAG); ready&&run()",
        ] {
            assert_eq!(guard(code), None, "{code:?}");
        }
    }

    const JS_WHITESPACE: [char; 18] = [
        '\u{0009}', '\u{000B}', '\u{000C}', '\u{0020}', '\u{00A0}', '\u{1680}', '\u{2000}',
        '\u{2001}', '\u{2002}', '\u{2003}', '\u{2004}', '\u{2005}', '\u{2006}', '\u{2007}',
        '\u{2008}', '\u{2009}', '\u{200A}', '\u{FEFF}',
    ];
    const JS_WHITESPACE_TAIL: [char; 3] = ['\u{202F}', '\u{205F}', '\u{3000}'];
    const LINE_TERMINATORS: [&str; 5] = ["\n", "\r", "\r\n", "\u{2028}", "\u{2029}"];

    fn flag_guard(code: &str) -> Option<&'static str> {
        let read = "process.env.FLAG";
        let start = code.find(read).unwrap();
        env_guard(code, start, start + read.len(), &non_code_ranges(code)).map(EnvGuard::evidence)
    }

    const AND_GUARD: Option<&str> = Some("the retained read controls a && short-circuit branch");

    #[test]
    fn js_whitespace_and_line_terminators_are_classified_exactly() {
        for character in JS_WHITESPACE.into_iter().chain(JS_WHITESPACE_TAIL) {
            assert!(is_js_whitespace(character), "{character:?}");
            assert!(!is_line_terminator(character), "{character:?}");
        }
        for character in ['\n', '\r', '\u{2028}', '\u{2029}'] {
            assert!(is_line_terminator(character), "{character:?}");
            assert!(!is_js_whitespace(character), "{character:?}");
        }
        // Not WhiteSpace in ECMAScript: NEL, the zero-width spaces and the
        // Mongolian vowel separator.
        for character in [
            '\u{0085}', '\u{180E}', '\u{200B}', '\u{200C}', '\u{2060}', 'a', '+',
        ] {
            assert!(!is_js_whitespace(character), "{character:?}");
            assert!(!is_line_terminator(character), "{character:?}");
        }
    }

    #[test]
    fn skip_trivia_reports_whether_a_terminator_was_crossed() {
        let skip = |code: &str| {
            let trivia = skip_trivia(code.as_bytes(), 0, &non_code_ranges(code));
            (trivia.next, trivia.crossed_terminator)
        };
        assert_eq!(skip("  \u{a0}\u{feff}\t+"), (Some(8), false));
        assert_eq!(skip("/* c */ // d"), (None, false));
        assert_eq!(skip("/* a\nb */+"), (Some(9), true));
        assert_eq!(skip("// c\n+"), (Some(5), true));
        assert_eq!(skip(" \u{2028}+"), (Some(4), true));
        assert_eq!(skip("\"s\"+"), (Some(0), false));
        assert_eq!(skip("\n"), (None, true));
    }

    #[test]
    fn env_guard_separators_between_tokens_follow_javascript_trivia() {
        // WhiteSpace before a single sign keeps the continuation; before a
        // doubled sign the line break still starts a new statement.
        for whitespace in JS_WHITESPACE.into_iter().chain(JS_WHITESPACE_TAIL) {
            for (sign, expected) in [
                ("+", AND_GUARD),
                ("-", AND_GUARD),
                ("++", None),
                ("--", None),
            ] {
                let code = format!("process.env.FLAG\n{whitespace}{sign}suffix&&run()");
                assert_eq!(flag_guard(&code), expected, "{code:?}");
                let code = format!("process.env.FLAG{whitespace}\n{sign}suffix&&run()");
                assert_eq!(flag_guard(&code), expected, "{code:?}");
            }
            // Whitespace between two signs makes two tokens: binary plus a
            // unary sign, never a doubled update operator.
            for sign in ["+", "-"] {
                let code = format!("process.env.FLAG\n{sign}{whitespace}{sign}suffix&&run()");
                assert_eq!(flag_guard(&code), AND_GUARD, "{code:?}");
            }
        }
        // Every LineTerminator separates statements before a doubled sign and
        // continues the expression before a single sign.
        for terminator in LINE_TERMINATORS {
            for (sign, expected) in [
                ("+", AND_GUARD),
                ("-", AND_GUARD),
                ("++", None),
                ("--", None),
            ] {
                let code = format!("process.env.FLAG{terminator}{sign}suffix&&run()");
                assert_eq!(flag_guard(&code), expected, "{code:?}");
                let code = format!("process.env.FLAG {terminator} {sign}suffix&&run()");
                assert_eq!(flag_guard(&code), expected, "{code:?}");
                // A line comment ends at any line terminator.
                let code = format!("process.env.FLAG // c{terminator}{sign}suffix&&run()");
                assert_eq!(flag_guard(&code), expected, "{code:?}");
                // A block comment that contains a line terminator is a line
                // break itself.
                let code = format!("process.env.FLAG/*{terminator}*/{sign}suffix&&run()");
                assert_eq!(flag_guard(&code), expected, "{code:?}");
                let code = format!("process.env.FLAG/* a{terminator}b */ {sign}suffix&&run()");
                assert_eq!(flag_guard(&code), expected, "{code:?}");
                let code = format!("process.env.FLAG /* a */ /*{terminator}*/ {sign}suffix&&run()");
                assert_eq!(flag_guard(&code), expected, "{code:?}");
            }
        }
        // A block comment without a line terminator is not a line break.
        for code in [
            "process.env.FLAG/* c */&&run()",
            "process.env.FLAG /* a */ /* b */ + suffix&&run()",
            "process.env.FLAG\n+/* c */+suffix&&run()",
            "process.env.FLAG\n-/*\n*/-suffix&&run()",
        ] {
            assert_eq!(flag_guard(code), AND_GUARD, "{code:?}");
        }
        // A string, template or statement terminator is a token, not trivia.
        for code in [
            "process.env.FLAG\n\"+\"&&run()",
            "process.env.FLAG\n'+'&&run()",
            "process.env.FLAG\n7&&run()",
            "process.env.FLAG;\n+suffix&&run()",
        ] {
            assert_eq!(flag_guard(code), None, "{code:?}");
        }
        assert_eq!(flag_guard("process.env.FLAG\n`x`&&run()"), AND_GUARD);
        // A regular expression after non-ASCII whitespace is still a literal.
        let code = "x=\u{a0}/process.env.FLAG/.test(y)\u{2028}z";
        assert!(in_ranges(
            &non_code_ranges(code),
            code.find("process").unwrap()
        ));
    }

    #[test]
    fn env_guard_unfinished_expressions_continue_across_line_breaks() {
        // The previous line ends in an operator, so the next line continues it
        // even when a doubled sign or another statement-like token follows.
        for code in [
            "process.env.FLAG +\n++suffix&&run()",
            "process.env.FLAG -\n--suffix&&run()",
            "process.env.FLAG +\n+suffix&&run()",
            "process.env.FLAG -\n-suffix&&run()",
            "process.env.FLAG +\nsuffix&&run()",
            "process.env.FLAG *\nsuffix&&run()",
            "process.env.FLAG ===\nsuffix&&run()",
            "process.env.FLAG in\nsuffix&&run()",
            "process.env.FLAG instanceof\nsuffix&&run()",
            "process.env.FLAG + ++\nsuffix&&run()",
            "process.env.FLAG +++\nsuffix&&run()",
            "process.env.FLAG +/* c */\n++suffix&&run()",
            "process.env.FLAG +/*\n*/++suffix&&run()",
            "process.env.FLAG + // c\n++suffix&&run()",
            "process.env.FLAG +\u{a0}\n\u{feff}++suffix&&run()",
            "process.env.FLAG +\u{2028}++suffix&&run()",
            "process.env.FLAG\n+\n++suffix&&run()",
            "process.env.FLAG\n+ ++suffix&&run()",
            "process.env.FLAG\n-\u{a0}--suffix&&run()",
            "process.env.FLAG\nin suffix&&run()",
            "process.env.FLAG\ninstanceof suffix&&run()",
        ] {
            assert_eq!(flag_guard(code), AND_GUARD, "{code:?}");
        }
        // The previous line is a complete statement, so the doubled sign starts
        // the next one.
        for code in [
            "process.env.FLAG++\n++suffix&&run()",
            "process.env.FLAG--\n--suffix&&run()",
            "process.env.FLAG ++ // c\n++suffix&&run()",
            "process.env.FLAG\u{a0}++\u{2029}++suffix&&run()",
            "process.env.FLAG)\n++suffix&&run()",
            "process.env.FLAG]\n++suffix&&run()",
            "process.env.FLAG;\n++suffix&&run()",
            "process.env.FLAG\nsuffix&&run()",
        ] {
            assert_eq!(flag_guard(code), None, "{code:?}");
        }
    }

    const KEYWORD_SEPARATORS: [&str; 9] = [
        "", " ", "/* c */", "\n", "\r\n", "\u{a0}", "\u{2028}", "/*\n*/", "// c\n",
    ];

    #[test]
    fn env_guard_keyword_property_names_are_decided_across_trivia() {
        for keyword in OPERAND_KEYWORDS {
            // A keyword in keyword position leaves the expression unfinished.
            let code = format!("process.env.FLAG + {keyword}\nsuffix&&run()");
            assert_eq!(flag_guard(&code), AND_GUARD, "{code:?}");
            let code = format!("process.env.FLAG + {keyword} ++\n++suffix&&run()");
            assert_eq!(flag_guard(&code), AND_GUARD, "{code:?}");
            for before in KEYWORD_SEPARATORS {
                for after in KEYWORD_SEPARATORS {
                    for dot in [".", "?."] {
                        // The same word after a member access is a property
                        // name, so the expression is finished.
                        let access = format!("obj{before}{dot}{after}{keyword}");
                        let code = format!("process.env.FLAG + {access}\nsuffix&&run()");
                        assert_eq!(flag_guard(&code), None, "{code:?}");
                        let code = format!("process.env.FLAG.{access}\nsuffix&&run()");
                        assert_eq!(flag_guard(&code), None, "{code:?}");
                        let code = format!("process.env.FLAG + {access} ++\n++suffix&&run()");
                        assert_eq!(flag_guard(&code), None, "{code:?}");
                    }
                }
            }
            // Separators before the keyword, other than a member access, keep
            // it a keyword.
            for separator in KEYWORD_SEPARATORS {
                let code = format!("process.env.FLAG +{separator}{keyword}\nsuffix&&run()");
                assert_eq!(flag_guard(&code), AND_GUARD, "{code:?}");
            }
        }
        let bytes = b"a...typeof";
        assert!(!is_property_name(bytes, 4, &[]));
        assert!(is_property_name(b"a . typeof", 4, &[]));
        assert!(is_property_name(b"a?.typeof", 3, &[]));
        assert!(is_property_name(
            b"a./* c */typeof",
            9,
            &non_code_ranges("a./* c */typeof")
        ));
    }

    #[test]
    fn env_guard_words_stop_at_unicode_separators() {
        for keyword in ["in", "instanceof"] {
            for separator in JS_WHITESPACE
                .into_iter()
                .chain(JS_WHITESPACE_TAIL)
                .map(String::from)
                .chain(LINE_TERMINATORS.map(String::from))
                .chain(["/* c */".to_owned(), "/*\n*/".to_owned()])
            {
                // The operator ends before the separator, so the next line
                // continues the expression.
                let code = format!("process.env.FLAG\n{keyword}{separator}suffix&&run()");
                assert_eq!(flag_guard(&code), AND_GUARD, "{code:?}");
                // The keyword is found when separators precede it as well.
                let code =
                    format!("process.env.FLAG +{separator}{keyword}{separator}\nsuffix&&run()");
                assert_eq!(flag_guard(&code), AND_GUARD, "{code:?}");
            }
            // A non-separator character makes the word a different identifier.
            for suffix in ["\u{e9}", "x", "$", "_", "\u{3b1}"] {
                let code = format!("process.env.FLAG\n{keyword}{suffix}&&run()");
                assert_eq!(flag_guard(&code), None, "{code:?}");
            }
        }
        for keyword in OPERAND_KEYWORDS {
            for prefix in ["\u{e9}", "x", "$", "_", "\u{3b1}"] {
                let code = format!("process.env.FLAG + {prefix}{keyword}\nsuffix&&run()");
                assert_eq!(flag_guard(&code), None, "{code:?}");
            }
        }
    }

    /// Every separator the lookups must treat as trivia between two tokens.
    fn all_separators() -> Vec<String> {
        JS_WHITESPACE
            .into_iter()
            .chain(JS_WHITESPACE_TAIL)
            .map(String::from)
            .chain(LINE_TERMINATORS.map(String::from))
            .chain(["/* c */", "/*\n*/", "// c\n", " /* c */ \u{feff}"].map(String::from))
            .collect()
    }

    #[test]
    fn token_before_classifies_every_lookup_through_one_helper() {
        fn classify(code: &str) -> Option<TokenBefore<'_>> {
            let ignored = non_code_ranges(code);
            let end = previous_token_end(code.as_bytes(), code.len(), &ignored)?;
            Some(token_ending_at(code.as_bytes(), end, &ignored))
        }
        for (code, expected) in [
            ("a = typeof", TokenBefore::Keyword("typeof")),
            ("a = x /* c */ in", TokenBefore::Keyword("in")),
            ("a = obj.typeof", TokenBefore::Name),
            ("a = obj?./* c */typeof", TokenBefore::Name),
            ("a = obj.#typeof", TokenBefore::Name),
            ("class A { #function }", TokenBefore::Punct(b'}')),
            ("a = #in", TokenBefore::Name),
            ("a = value", TokenBefore::Name),
            ("a = 7", TokenBefore::Number),
            ("a = 0x1f", TokenBefore::Number),
            ("a = 1.", TokenBefore::Number),
            ("a = 1_0.", TokenBefore::Number),
            ("a = 1.5.", TokenBefore::Punct(b'.')),
            ("a = 1e5.", TokenBefore::Punct(b'.')),
            ("a = 0x1.", TokenBefore::Punct(b'.')),
            ("a = 1n.", TokenBefore::Punct(b'.')),
            ("a = 1..", TokenBefore::Punct(b'.')),
            ("a = a1.", TokenBefore::Punct(b'.')),
            ("a = $1.", TokenBefore::Punct(b'.')),
            ("a = f().", TokenBefore::Punct(b'.')),
            ("a = 'text'", TokenBefore::Literal),
            ("a = /re/", TokenBefore::Literal),
            ("a = `t${", TokenBefore::SubstitutionOpen),
            ("a = b +", TokenBefore::Punct(b'+')),
        ] {
            assert_eq!(classify(code), Some(expected), "{code:?}");
        }
        assert_eq!(classify("  /* c */ // d"), None);
    }

    #[test]
    fn regex_literals_follow_keywords_across_all_trivia() {
        let read = |code: &str| in_ranges(&non_code_ranges(code), code.find("process").unwrap());
        for keyword in REGEX_KEYWORDS {
            for separator in all_separators() {
                // A keyword in keyword position starts a regular expression
                // whatever sits between it and the slash.
                let code = format!("{keyword}{separator}/process.env.FLAG/.test(y)");
                assert!(read(&code), "{code:?}");
                // A property named like a keyword is an operand, so the slash
                // divides.
                for dot in [".", "?."] {
                    let code = format!("obj{dot}{keyword}{separator}/process.env.FLAG/2");
                    assert!(!read(&code), "{code:?}");
                }
            }
            // A longer identifier is not the keyword.
            for prefix in ["x", "$", "_", "\u{e9}"] {
                let code = format!("{prefix}{keyword} /process.env.FLAG/2");
                assert!(!read(&code), "{code:?}");
            }
        }
        // The bug case: a regular expression after `return` and a BOM.
        let code = "function f(){return\u{feff}/process.env.FLAG/.test(x)&&run()}";
        assert!(read(code));
    }

    #[test]
    fn private_names_are_never_keywords() {
        for keyword in OPERAND_KEYWORDS {
            for access in [".#", "?.#", " . #", ".\n#", "./* c */#"] {
                // The completed member access ends the expression, so the next
                // line starts a new statement.
                let code = format!("process.env.FLAG + obj{access}{keyword}\nsuffix&&run()");
                assert_eq!(flag_guard(&code), None, "{code:?}");
                let code = format!("process.env.FLAG + obj{access}{keyword} ++\n++suffix&&run()");
                assert_eq!(flag_guard(&code), None, "{code:?}");
            }
            // The same word in keyword position still continues.
            let code = format!("process.env.FLAG + {keyword}\nsuffix&&run()");
            assert_eq!(flag_guard(&code), AND_GUARD, "{code:?}");
        }
        // A private name that is not a keyword behaves the same.
        assert_eq!(
            flag_guard("process.env.FLAG + obj.#count\nsuffix&&run()"),
            None
        );
    }

    #[test]
    fn statement_blocks_end_the_statement_at_their_closing_brace() {
        let followers = [
            "/abc/.test(x)&&run()",
            "(a)&&run()",
            "[a]&&run()",
            "`t`&&run()",
            "+a&&run()",
            "-a&&run()",
            "++a&&run()",
            "*a&&run()",
            "in a&&run()",
            "&&run()",
        ];
        for head in [
            "if(x){",
            "while(x){",
            "for(;;){",
            "function f(){",
            "class A{m(){",
            "if(x){}else{",
            "try{",
            "{",
        ] {
            let tail = "}".repeat(head.matches('{').count());
            for boundary in BOUNDARIES {
                for follower in followers {
                    let code = format!("{head}process.env.FLAG\n{tail}{boundary}{follower}");
                    assert_eq!(flag_guard(&code), None, "{code:?}");
                    let code = format!("{head}process.env.FLAG{tail}{boundary}{follower}");
                    assert_eq!(flag_guard(&code), None, "{code:?}");
                }
            }
            // On one line the pending read is already drained at the brace.
            let code = format!("{head}process.env.FLAG{tail} (a)&&run()");
            assert_eq!(flag_guard(&code), None, "{code:?}");
        }
        // An object literal is an operand: the expression continues past its
        // closing brace on the next line.
        for (code, expected) in [
            ("process.env.FLAG + {a:1}\n(x)&&run()", AND_GUARD),
            ("process.env.FLAG + {a:1}\n/abc/.test(x)&&run()", AND_GUARD),
            ("process.env.FLAG + [{a:1}]\n(x)&&run()", AND_GUARD),
            ("process.env.FLAG + f({a:1})\n(x)&&run()", AND_GUARD),
            ("process.env.FLAG + x => {}\n(x)&&run()", AND_GUARD),
        ] {
            assert_eq!(flag_guard(code), expected, "{code:?}");
        }
        // A slash after a statement block starts a regular expression, after an
        // object literal it divides.
        let read = |code: &str| in_ranges(&non_code_ranges(code), code.find("process").unwrap());
        assert!(read("if(x){}\n/process.env.FLAG/.test(y)"));
        assert!(read("function f(){}\n/process.env.FLAG/.test(y)"));
        assert!(read("{}\n/process.env.FLAG/.test(y)"));
        assert!(read("if(a){if(b){}}/process.env.FLAG/.test(y)"));
        assert!(!read("x={}\n/process.env.FLAG/2"));
        assert!(!read("f({})/process.env.FLAG/2"));
        assert!(!read("x=[{}]/process.env.FLAG/2"));
        assert!(!read("a?{}:{}/process.env.FLAG/2"));
    }

    #[test]
    fn known_limits_choose_the_conservative_direction() {
        // A function or class expression body is read as a statement block, so
        // a line break after it ends the statement. JavaScript would continue
        // the expression, and the read is conservatively left unreported.
        for code in [
            "process.env.FLAG + function(){}\n(x)&&run()",
            "process.env.FLAG + class{}\n+a&&run()",
        ] {
            assert_eq!(flag_guard(code), None, "{code:?}");
        }
        // The same reading makes a slash after such a body a regular
        // expression, although JavaScript divides.
        let code = "x=function(){}/process.env.FLAG/2";
        assert!(in_ranges(
            &non_code_ranges(code),
            code.find("process").unwrap()
        ));
        // A `)` is always an operand end before a slash, so the body of
        // `if (a) /re/.test(b)` is read as code, as is a call result.
        for code in ["f(a)/process.env.FLAG/2", "if(a)/process.env.FLAG/.test(b)"] {
            assert!(
                !in_ranges(&non_code_ranges(code), code.find("process").unwrap()),
                "{code:?}"
            );
        }
        // A word that is a keyword only in some positions (`of`, `yield`,
        // `await`) counts as one wherever it is not a property or private name.
        let code = "const of=1;of/process.env.FLAG/.test(a)";
        assert!(in_ranges(
            &non_code_ranges(code),
            code.find("process").unwrap()
        ));
    }

    #[test]
    fn expression_keywords_stay_pending_across_line_breaks() {
        for (code, expected) in [
            ("process.env.FLAG + function\nfoo(){} && run()", AND_GUARD),
            (
                "process.env.FLAG + function\u{a0}\n*gen(){} && run()",
                AND_GUARD,
            ),
            (
                "process.env.FLAG + async function\nfoo(){} && run()",
                AND_GUARD,
            ),
            ("process.env.FLAG + class\nA{} && run()", AND_GUARD),
            (
                "process.env.FLAG + class A extends\nB{} && run()",
                AND_GUARD,
            ),
            (
                "process.env.FLAG + class A extends // c\nB{} && run()",
                AND_GUARD,
            ),
            (
                "process.env.FLAG + (class extends\nB{}) && run()",
                AND_GUARD,
            ),
            ("process.env.FLAG + (function\nfoo(){}) && run()", AND_GUARD),
            // A property with the same name leaves nothing unfinished.
            ("process.env.FLAG + obj.function\nfoo && run()", None),
            ("process.env.FLAG + obj.class\nfoo && run()", None),
            ("process.env.FLAG + obj?.extends\nfoo && run()", None),
            // A longer identifier is not the keyword.
            ("process.env.FLAG + functions\nfoo && run()", None),
            ("process.env.FLAG + classes\nfoo && run()", None),
            ("process.env.FLAG + subclass\nfoo && run()", None),
        ] {
            assert_eq!(flag_guard(code), expected, "{code:?}");
        }
    }

    #[test]
    fn decimal_points_end_numeric_literals() {
        // `1.` is a complete operand, so the prefix update on the next line
        // starts a new statement.
        for number in ["1.", "0.", "10.", "1_0.", "007."] {
            for update in ["++", "--"] {
                let code = format!("process.env.FLAG + {number}\n{update}x&&run()");
                assert_eq!(flag_guard(&code), None, "{code:?}");
                let code = format!("process.env.FLAG + {number} /* c */ \n{update}x&&run()");
                assert_eq!(flag_guard(&code), None, "{code:?}");
            }
            let code = format!("process.env.FLAG + {number}\nx&&run()");
            assert_eq!(flag_guard(&code), None, "{code:?}");
        }
        // Every other trailing dot is member access and leaves the expression
        // unfinished.
        for lhs in [
            "a.", "a1.", "$1.", "1.5.", "1e5.", "0x1.", "1n.", "1..", "obj?.", "f().", "[1].",
            "a.b.", "x_1.",
        ] {
            let code = format!("process.env.FLAG + {lhs}\n++x&&run()");
            assert_eq!(flag_guard(&code), AND_GUARD, "{code:?}");
            let code = format!("process.env.FLAG + {lhs}\nx&&run()");
            assert_eq!(flag_guard(&code), AND_GUARD, "{code:?}");
        }
    }

    #[test]
    fn scope_heads_are_found_across_all_trivia() {
        let guard = |code: &str| flag_guard(code);
        for (keyword, evidence) in [
            ("if", "the retained read is in an if condition"),
            ("while", "the retained read is in a while condition"),
            ("switch", "the retained read is in a switch condition"),
        ] {
            for separator in all_separators() {
                let code = format!("{keyword}{separator}(process.env.FLAG) run()");
                assert_eq!(guard(&code), Some(evidence), "{code:?}");
                // A property call is not a statement head.
                let code = format!("obj.{keyword}{separator}(process.env.FLAG) run()");
                assert_eq!(guard(&code), None, "{code:?}");
            }
        }
        for separator in all_separators() {
            let code = format!("for{separator}(;process.env.FLAG;) run()");
            assert_eq!(
                guard(&code),
                Some("the retained read is in a for condition"),
                "{code:?}"
            );
            let code = format!("obj.for{separator}(;process.env.FLAG;) run()");
            assert_eq!(guard(&code), None, "{code:?}");
        }
    }

    /// Boundary kinds that the env-condition scan treats as line breaks.
    const BOUNDARIES: [&str; 8] = [
        "\n",
        "\r",
        "\r\n",
        "\u{2028}",
        "\u{2029}",
        "/*\n*/",
        "/*\r*/",
        "/*\u{2028}*/",
    ];

    #[test]
    fn asi_memo_matches_the_per_line_break_decision() {
        let followers = [
            "&&run()",
            "+suffix&&run()",
            "++suffix&&run()",
            "in x&&run()",
            "suffix&&run()",
            "",
        ];
        for boundary in BOUNDARIES {
            for follower in followers {
                for previous in [
                    "process.env.FLAG",
                    "process.env.FLAG +",
                    "process.env.FLAG ++",
                ] {
                    for count in [1, 2, 5] {
                        let code = format!(
                            "{previous} {}{follower}",
                            format!("{boundary} // c\u{2028} ").repeat(count)
                        );
                        let bytes = code.as_bytes();
                        let ignored = non_code_ranges(&code);
                        let mut memo = AsiMemo::default();
                        let mut index = 0;
                        while index < bytes.len() {
                            let in_comment = comment_range(bytes, index, &ignored);
                            let is_break = match in_comment {
                                Some((start, end)) => {
                                    index == start && comment_has_line_terminator(bytes, start, end)
                                }
                                None => line_terminator_at(bytes, index).is_some(),
                            };
                            if is_break {
                                assert_eq!(
                                    memo.terminates(bytes, index, &ignored, None),
                                    asi_terminates(bytes, index, &ignored, None),
                                    "{code:?} at {index}"
                                );
                            }
                            index += 1;
                        }
                    }
                }
            }
        }
    }

    #[test]
    fn env_guard_line_breaks_are_decided_once_per_trivia_run() {
        // Thousands of consecutive line breaks between two tokens must not
        // rescan the remaining trivia at every one of them.
        const COUNT: usize = 40_000;
        for boundary in BOUNDARIES {
            for (follower, expected) in [("&&run()", AND_GUARD), ("+suffix&&run()", AND_GUARD)] {
                let code = format!("process.env.FLAG{}{follower}", boundary.repeat(COUNT));
                let started = std::time::Instant::now();
                let guard = flag_guard(&code);
                assert!(
                    started.elapsed() < std::time::Duration::from_secs(5),
                    "{boundary:?} took {:?}",
                    started.elapsed()
                );
                assert_eq!(guard, expected, "{boundary:?} {follower:?}");
            }
            // A read that is already blocked by the first boundary drains
            // once; the remaining boundaries find nothing pending.
            let code = format!("process.env.FLAG{}x&&run()", boundary.repeat(COUNT));
            let started = std::time::Instant::now();
            assert_eq!(flag_guard(&code), None, "{boundary:?}");
            assert!(started.elapsed() < std::time::Duration::from_secs(5));
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
