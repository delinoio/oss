//! Rules shared by both engines: duplicate packages and minifier blockers.

use std::{
    collections::{BTreeMap, BTreeSet},
    path::Path,
    sync::LazyLock,
};

use regex::Regex;

use self::env_guard::env_guards;
use super::{Context, DiagnosticDraft, RuleId};
use crate::{
    chunks::ResourceIdentity,
    collect::{CollectedModule, MangleSetting},
    model::{Condition, ConditionOutcome, Evidence, EvidenceStatus, LocationKind, SourceLocation},
};

mod env_guard;

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
pub(super) fn is_line_terminator(character: char) -> bool {
    matches!(character, '\n' | '\r' | '\u{2028}' | '\u{2029}')
}

/// True for a character that ends a statement in the heuristic scans: `;` or
/// any line terminator.
fn is_statement_end(character: char) -> bool {
    character == ';' || is_line_terminator(character)
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

/// A lexical token as far as the heuristic scans need to classify it.
///
/// The scans are not a JavaScript parser. Every question of the form "what is
/// the previous (or next) significant token" goes through
/// [`previous_significant_token`] and [`next_significant_token`], so the
/// regular expression context, the statement-boundary (ASI) decision, the
/// expression-bodied arrow end, control keyword detection and the direct
/// `eval` call check agree on comments, line terminators and member names.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Token<'a> {
    /// Nothing but whitespace and comments in this direction.
    Edge,
    /// An identifier name or keyword. `member` is true in a name position that
    /// is never a keyword: after `.` or `?.` (`obj.return`) and after `#` (a
    /// private name). A spread `...name` is not a member access.
    Word {
        text: &'a str,
        member: bool,
    },
    Number,
    /// A string literal, a closed template literal or a regular expression
    /// literal.
    Literal,
    /// The start of a template literal that follows (next token only).
    Template,
    /// An operator or punctuator: one character, or `++`, `--` or `...`.
    Punct(&'a str),
}

/// Keywords after which an expression, and so a regular expression, starts.
/// `await` counts as the unary operator; a script that uses `await` as a plain
/// identifier is a known limit of the scan.
const REGEX_KEYWORDS: [&str; 16] = [
    "return",
    "typeof",
    "case",
    "do",
    "else",
    "in",
    "instanceof",
    "of",
    "void",
    "yield",
    "delete",
    "throw",
    "new",
    "await",
    "default",
    "extends",
];

/// Keywords that cannot end an expression: a line terminator after them never
/// starts a new statement. Every entry is also in [`REGEX_KEYWORDS`]; the
/// restricted productions `return` and `throw` are left out because a line
/// terminator after them does end the statement.
const OPEN_KEYWORDS: [&str; 9] = [
    "in",
    "instanceof",
    "typeof",
    "void",
    "delete",
    "new",
    "of",
    "await",
    "yield",
];

/// True when the range is a comment rather than a string, template or regular
/// expression literal.
fn is_comment_range(code: &str, (start, end): (usize, usize)) -> bool {
    let text = &code.as_bytes()[start..end.min(code.len())];
    text.starts_with(b"//") || text.starts_with(b"/*")
}

/// The last significant token before `index`: comments, whitespace and every
/// ECMAScript line terminator are skipped, and a word right after `.`, `?.` or
/// `#` is reported as a member name even when comments or line terminators
/// separate the two (`obj. // c` + newline + `return`).
///
/// `ignored` holds the non-code ranges found before `index`; a range that ends
/// at the token is a literal (or the `${` that opens a template substitution,
/// which reads as `{`).
fn previous_significant_token<'a>(
    code: &'a str,
    index: usize,
    ignored: &[(usize, usize)],
) -> Token<'a> {
    let end = strip_comments_for_prefix(code, index, ignored).len();
    let Some(last) = code[..end].chars().next_back() else {
        return Token::Edge;
    };
    if let Some(range_index) = ignored
        .partition_point(|(start, _)| *start < end)
        .checked_sub(1)
    {
        let range = ignored[range_index];
        if range.1 == end && !is_comment_range(code, range) {
            let text = &code.as_bytes()[range.0..range.1];
            let substitution = text.starts_with(b"`") || text.starts_with(b"}");
            let substitution = substitution && text.ends_with(b"${");
            return if substitution {
                Token::Punct(&code[end - 1..end])
            } else {
                Token::Literal
            };
        }
    }
    let prefix = &code[..end];
    if is_identifier_part(last) {
        let text = trailing_word(prefix);
        if text.starts_with(|character: char| character.is_ascii_digit()) {
            return Token::Number;
        }
        let before = strip_comments_for_prefix(code, end - text.len(), ignored);
        let member = before.ends_with('#')
            || (before.ends_with('.')
                && !before.ends_with("...")
                // `1. in x`: the dot belongs to a number literal.
                && !trailing_word(&before[..before.len() - 1]).starts_with(|character: char| {
                    character.is_ascii_digit()
                }));
        return Token::Word { text, member };
    }
    for operator in ["...", "++", "--"] {
        if prefix.ends_with(operator) {
            return Token::Punct(&prefix[end - operator.len()..]);
        }
    }
    Token::Punct(&prefix[end - last.len_utf8()..])
}

/// The first significant token at or after `start`, with its position:
/// comments, whitespace and every line terminator are skipped. A string or
/// regular expression literal is a [`Token::Literal`] and a template literal is
/// a [`Token::Template`]; the text of a literal is never read as code.
fn next_significant_token<'a>(
    code: &'a str,
    start: usize,
    ignored: &[(usize, usize)],
) -> (usize, Token<'a>) {
    let mut index = start;
    while index < code.len() {
        let index_range = ignored.partition_point(|(begin, _)| *begin <= index);
        if let Some(range) = index_range.checked_sub(1).map(|found| ignored[found])
            && index < range.1
        {
            if is_comment_range(code, range) {
                index = range.1;
                continue;
            }
            let token = if code.as_bytes()[range.0] == b'`' {
                Token::Template
            } else {
                Token::Literal
            };
            return (index, token);
        }
        let Some(character) = code[index..].chars().next() else {
            break;
        };
        if character.is_whitespace() {
            index += character.len_utf8();
            continue;
        }
        if is_identifier_part(character) {
            let length = code[index..]
                .find(|part: char| !is_identifier_part(part))
                .unwrap_or(code.len() - index);
            let text = &code[index..index + length];
            let token = if character.is_ascii_digit() {
                Token::Number
            } else {
                Token::Word {
                    text,
                    member: false,
                }
            };
            return (index, token);
        }
        let doubled = matches!(character, '+' | '-') && code[index + 1..].starts_with(character);
        let length = if doubled { 2 } else { character.len_utf8() };
        return (index, Token::Punct(&code[index..index + length]));
    }
    (code.len(), Token::Edge)
}

/// Whether a `/` at `start` begins a regular expression literal, decided from
/// the previous significant token and the non-code `ranges` found so far.
///
/// A keyword counts only outside member-name position, so `else` followed by a
/// comment still starts a regular expression while `obj.return // c` + newline
/// + `/ x / 2` divides.
fn regex_allowed(code: &str, ranges: &[(usize, usize)], start: usize) -> bool {
    match previous_significant_token(code, start, ranges) {
        Token::Edge => true,
        Token::Word { text, member } => !member && REGEX_KEYWORDS.contains(&text),
        Token::Number | Token::Literal | Token::Template => false,
        Token::Punct(text) => {
            !matches!(text, "++" | "--")
                && text
                    .chars()
                    .all(|character| "(,=:[!&|?{};+-*%<>~^".contains(character))
        }
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
            }
            b'`' => {
                let (end, opened) = scan_until(index + 1, b'`', true);
                ranges.push((index, end));
                if opened {
                    templates.push(0);
                }
                index = end;
            }
            b'}' if templates.last() == Some(&0) => {
                templates.pop();
                let (end, opened) = scan_until(index + 1, b'`', true);
                ranges.push((index, end));
                if opened {
                    templates.push(0);
                }
                index = end;
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
            b'/' if regex_allowed(code, &ranges, index) => {
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

/// How a bare `await` token is read by a statement-boundary decision.
///
/// `await` is the unary operator only in a module and inside an `async`
/// function body; elsewhere it is an identifier that can end an expression, so
/// a line terminator after it is an automatic semicolon. A caller that tracks
/// the function context passes the proven reading. A caller without that
/// context reads `await` as the operator, which is a documented limit of the
/// scan.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum AwaitWord {
    Operator,
    Identifier,
}

/// Whether the line terminator that starts at `terminator` (and ends at
/// `after`) ends an expression-bodied arrow function, by the ECMAScript
/// automatic semicolon insertion rule: the body ends when the next token
/// cannot continue the expression, unless the previous token cannot end one.
/// A `{` cannot continue an expression, so there it opens a block statement
/// (`(eval) => eval` + line terminator + `{ eval(code) }`).
pub(super) fn arrow_body_ends(
    code: &str,
    terminator: usize,
    after: usize,
    ignored: &[(usize, usize)],
    await_word: AwaitWord,
) -> bool {
    statement_boundary(code, terminator, after, ignored, true, await_word)
}

fn statement_boundary(
    code: &str,
    terminator: usize,
    after: usize,
    ignored: &[(usize, usize)],
    block_starts_statement: bool,
    await_word: AwaitWord,
) -> bool {
    let ends_expression = match previous_significant_token(code, terminator, ignored) {
        Token::Word { text, member } => {
            member
                || !OPEN_KEYWORDS.contains(&text)
                || (text == "await" && await_word == AwaitWord::Identifier)
        }
        Token::Punct(text) => matches!(text, ")" | "]" | "}" | "++" | "--" | ";"),
        Token::Edge | Token::Number | Token::Literal | Token::Template => true,
    };
    if !ends_expression {
        return false;
    }
    let (position, next) = next_significant_token(code, after, ignored);
    !match next {
        Token::Word { text, .. } => matches!(text, "in" | "instanceof"),
        Token::Template => true,
        Token::Punct("++" | "--") => false,
        // `!=` and `!==` continue; a prefix `!` starts a new statement.
        Token::Punct("!") => code[position..].starts_with("!="),
        Token::Punct("{") => !block_starts_statement,
        Token::Punct(text) => text
            .chars()
            .all(|character| "%&()*+,-./:;<=>?[]^|}".contains(character)),
        Token::Edge | Token::Number | Token::Literal => false,
    }
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

/// True for a character that can continue a JavaScript identifier
/// (`IdentifierPart`): any Unicode `ID_Continue` character (letters, digits,
/// combining marks, connector punctuation such as `_`), `$`, and the
/// zero-width joiners U+200C and U+200D.
///
/// Every scan that must tell a keyword from a longer name uses this one
/// definition. Identifier escapes (`\u0069f`) are not decoded.
pub(super) fn is_identifier_part(character: char) -> bool {
    if character.is_ascii() {
        return character.is_ascii_alphanumeric() || matches!(character, '_' | '$');
    }
    static ID_CONTINUE: LazyLock<Regex> =
        LazyLock::new(|| Regex::new(r"^\p{ID_Continue}$").expect("static regex"));
    matches!(character, '\u{200c}' | '\u{200d}')
        || ID_CONTINUE.is_match(character.encode_utf8(&mut [0; 4]))
}

/// The identifier-like word at the end of `text`, or an empty string.
fn trailing_word(text: &str) -> &str {
    let start = text
        .rfind(|character: char| !is_identifier_part(character))
        .map_or(0, |index| {
            index + text[index..].chars().next().map_or(1, char::len_utf8)
        });
    &text[start..]
}

/// Whether the `{` at `open` starts an object literal or a class body, whose
/// members may be named like control keywords, instead of a statement block.
///
/// Decided from the code before the brace, comments removed:
/// - blocks follow `)`, `;`, `{`, `}`, `=>`, a statement keyword (`else`,
///   `try`, `do`, `finally`, `static`) or the start of the code;
/// - an object literal follows an operator, an opener, a comma, a colon that is
///   not a `case` label, or an expression keyword such as `return`;
/// - a class body follows a heading that contains `class`, even when the
///   heritage is a call expression (`class A extends mix(B) {`).
///
/// This is a lexical guess, not a parse. A brace that cannot be classified is
/// treated as holding members, which keeps its keyword-named calls unguarded.
pub(super) fn brace_holds_members(code: &str, open: usize, ignored: &[(usize, usize)]) -> bool {
    let prefix = strip_comments_for_prefix(code, open, ignored);
    if prefix.ends_with("=>") {
        return false;
    }
    let Some(last) = prefix.chars().next_back() else {
        return false;
    };
    match last {
        ')' => class_heading_precedes(code, prefix.len(), ignored),
        ';' | '{' | '}' => false,
        ':' => !is_case_label(prefix),
        _ if is_identifier_part(last) => match trailing_word(prefix) {
            "else" | "try" | "do" | "finally" | "static" => false,
            word if OBJECT_VALUE_KEYWORDS.contains(&word) => true,
            _ => class_heading_precedes(code, prefix.len(), ignored),
        },
        _ => true,
    }
}

/// Whether the statement head that ends at `end` contains the `class` keyword
/// outside nested parentheses, brackets or TypeScript type-argument lists. The
/// scan walks backwards over characters and stops at the previous `;`, `{` or
/// `}`, or at an unbalanced opener. A type-argument list (`class C<T extends {
/// a: 1 }>`, `extends B<{ a: 1 }>`) is skipped as a whole, so the braces and
/// `=` inside it do not end the head.
pub(super) fn class_heading_precedes(code: &str, end: usize, ignored: &[(usize, usize)]) -> bool {
    class_heading_scan(code, end, ignored, false)
}

/// Shared body of [`class_heading_precedes`]. `stop_at_assignment` also ends
/// the head at a top-level `=`, so a field initializer such as `x = { ... }`
/// does not read an enclosing class header as its own.
fn class_heading_scan(
    code: &str,
    end: usize,
    ignored: &[(usize, usize)],
    stop_at_assignment: bool,
) -> bool {
    class_keyword_scan(code, end, ignored, stop_at_assignment).is_some()
}

/// The offset of the `class` keyword that starts the head ending at `end`, or
/// `None` when the head holds none. The scan is described on
/// [`class_heading_precedes`].
///
/// A class expression in the heritage clause (`class C extends class {} {`)
/// has a body of its own. When a top-level `}` closes the body of a class
/// expression whose `class` keyword follows `extends`, the scan jumps to that
/// keyword and keeps reading the outer head. Any other `}` ends the head.
fn class_keyword_scan(
    code: &str,
    end: usize,
    ignored: &[(usize, usize)],
    stop_at_assignment: bool,
) -> Option<usize> {
    let mut depth = 0usize;
    // End offset of the identifier-like word being read backwards.
    let mut word_end: Option<usize> = None;
    let is_class = |word_start: usize, word_end: usize| {
        // `class` is a keyword unless it names a member (`a.class`, `#class`)
        // or a property or method (`class: {`, `class() {`).
        &code[word_start..word_end] == "class"
            && !code[..word_start].trim_end().ends_with(['.', '#'])
            && !code[word_end..end]
                .trim_start()
                .starts_with([':', '(', '=', ','])
    };
    // Walk char boundaries only: `position` is always the start of a char.
    let mut position = end;
    while let Some(character) = code[..position].chars().next_back() {
        let width = character.len_utf8();
        position -= width;
        if !in_ranges(ignored, position) && is_identifier_part(character) {
            word_end.get_or_insert(position + width);
            continue;
        }
        if let Some(finish) = word_end.take()
            && depth == 0
            && is_class(position + width, finish)
        {
            return Some(position + width);
        }
        if in_ranges(ignored, position) {
            continue;
        }
        match character {
            ')' | ']' => depth += 1,
            '(' | '[' => depth = depth.checked_sub(1)?,
            // The `>` of an arrow (`=>`) never closes a type-argument list.
            '>' if depth == 0 && !code[..position].ends_with('=') => {
                if let Some(open) = type_arguments_open(code, position, ignored) {
                    position = open;
                }
            }
            '}' if depth == 0 => {
                position = heritage_class_expression_start(code, position, ignored)?;
            }
            '{' | ';' if depth == 0 => return None,
            '=' if depth == 0 && stop_at_assignment => return None,
            _ => {}
        }
    }
    word_end
        .filter(|finish| depth == 0 && is_class(0, *finish))
        .map(|_| 0)
}

/// The offset of the `class` keyword of the class expression whose body ends
/// with the `}` at `close`, when that keyword directly follows `extends`
/// (`class C extends class { ... } {`). `None` for any other brace pair.
fn heritage_class_expression_start(
    code: &str,
    close: usize,
    ignored: &[(usize, usize)],
) -> Option<usize> {
    let mut nesting = 0usize;
    let mut position = close;
    let open = loop {
        let character = code[..position].chars().next_back()?;
        position -= character.len_utf8();
        if in_ranges(ignored, position) {
            continue;
        }
        match character {
            '}' => nesting += 1,
            '{' => match nesting.checked_sub(1) {
                Some(next) => nesting = next,
                None => break position,
            },
            _ => {}
        }
    };
    let keyword = class_keyword_scan(code, open, ignored, true)?;
    let before = strip_comments_for_prefix(code, keyword, ignored);
    (trailing_word(before) == "extends").then_some(keyword)
}

/// The offset of the `<` that opens the type-argument list closed by the `>` at
/// `close`, or `None` when the `>` is an operator (`a > b`, `x >= y`) because
/// no balanced `<` precedes it in the same statement.
///
/// The scan walks backwards over characters. Brackets, parentheses and braces
/// inside the list (`T extends { a: Array<1> }`) must balance; an unmatched
/// opener or a top-level `;` means the `>` is not a closing angle bracket.
///
/// A type-argument list is written directly after the name it applies to
/// (`f<A>`, `class C<T>`, `extends B<T>`), so a `<` that follows whitespace or
/// an operator is a comparison: in `left < right > { ... }` the `>` closes
/// nothing. A `<` attached to a name stays a type-argument opener even when it
/// could be written as a comparison without spaces (`a<b>{ ... }`), which keeps
/// the finding for that brace; this is the documented limit of the scan.
fn type_arguments_open(code: &str, close: usize, ignored: &[(usize, usize)]) -> Option<usize> {
    let mut angles = 1usize;
    let mut nesting = 0usize;
    let mut position = close;
    while let Some(character) = code[..position].chars().next_back() {
        position -= character.len_utf8();
        if in_ranges(ignored, position) {
            continue;
        }
        match character {
            ')' | ']' | '}' => nesting += 1,
            '(' | '[' | '{' => nesting = nesting.checked_sub(1)?,
            ';' if nesting == 0 => return None,
            '>' if nesting == 0 && !code[..position].ends_with('=') => angles += 1,
            '<' if nesting == 0 => {
                angles -= 1;
                if angles == 0 {
                    return code[..position]
                        .chars()
                        .next_back()
                        .is_some_and(is_identifier_part)
                        .then_some(position);
                }
            }
            _ => {}
        }
    }
    None
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
            let (mapped_source, unmapped_segment) = if vite {
                let chunk = &context.chunks[index];
                let (line, column) = chunk.position(offset);
                (
                    chunk
                        .original(line, column)
                        .map(|(path, _, _)| context.display_path(&path)),
                    chunk.lacks_source_mapping(line, column),
                )
            } else {
                (None, false)
            };
            let located = context.chunk_location(index, offset);
            let mapped = located.generated.is_some();
            if vite
                && !mapped
                && let Some(source) = mapped_source
            {
                context.vite_env_source_gaps.insert(source);
            } else if vite && unmapped_segment {
                // The candidate has no original path to record, so it is
                // tracked by its generated position instead.
                let chunk_file = context.chunks[index].file.clone();
                tracing::debug!(
                    chunk = %chunk_file,
                    offset,
                    name = %name,
                    "guarded Vite environment candidate has no source mapping"
                );
                context
                    .vite_env_unmapped_candidates
                    .insert((chunk_file, offset));
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
                .rfind(|character: char| !is_identifier_part(character))
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
            && name.chars().all(is_identifier_part)
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
/// The source with every non-code range (comment, string, template text and
/// regular expression) replaced by spaces, keeping line terminators, so byte
/// offsets agree with `source`.
fn masked_code(source: &str, ignored: &[(usize, usize)]) -> String {
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

/// Every balanced `{ ... }` pair of masked code as `(open, close)` offsets, in
/// the order the closing braces appear.
fn brace_blocks(bytes: &[u8]) -> Vec<(usize, usize)> {
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
    blocks
}

fn eval_shadow_scopes(source: &str, ignored: &[(usize, usize)]) -> Vec<(usize, usize)> {
    let code = masked_code(source, ignored);
    let bytes = code.as_bytes();
    let blocks = brace_blocks(bytes);
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
    // TypeScript `namespace` and `module` bodies own their `var` declarations
    // just like function bodies.
    let namespaces = namespace_bodies(source, &code, ignored, &blocks);
    bodies.extend(namespaces.iter().map(|namespace| namespace.body));
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
            .rsplit(|character: char| !is_identifier_part(character))
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
            Some(body - 1).filter(|open| !literal_between(source, ignored, whole.start(), *open))
        } else {
            code[body..]
                .bytes()
                .position(|byte| !byte.is_ascii_whitespace())
                .map(|offset| body + offset)
                .filter(|open| bytes.get(*open) == Some(&b'{'))
                .filter(|open| !literal_between(source, ignored, body, *open))
        };
        let block = open.and_then(|open| blocks.iter().find(|(start, _)| *start == open).copied());
        let end = block.map_or_else(
            || arrow_expression_end(source, body, ignored),
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
            .rsplit(|character: char| !is_identifier_part(character))
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
        // A literal between the parameters and the next `{` makes that brace
        // part of a later statement: the arrow body is the literal expression.
        let literal_body = literal_between(source, ignored, close, body);
        let block = blocks
            .iter()
            .find(|(start, _)| *start == body && !literal_body)
            .copied();
        let expression_start = if literal_body {
            source[close..]
                .find("=>")
                .map_or(body, |offset| close + offset + 2)
        } else {
            body
        };
        let end = block.map_or_else(
            || arrow_expression_end(source, expression_start, ignored),
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
        let end = arrow_expression_end(source, found.end(), ignored);
        scopes.push((found.start(), end));
    }
    // A single-parameter arrow does not have parentheses to identify its
    // body. Use ECMAScript's Unicode identifier classes so names such as `λ`
    // are scoped like their ASCII counterparts. ZWNJ and ZWJ are permitted
    // after the first identifier character by the ECMAScript grammar.
    let single_arrow =
        Regex::new(&format!(r"{UNICODE_IDENTIFIER}\s*=>\s*\{{")).expect("static regex");
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
        // An exported member is visible in every declaration that merges into
        // the same namespace symbol; other declarations stay local to their
        // own body.
        if exported_declaration(&code, found.start())
            && let Some(namespace) = namespaces
                .iter()
                .find(|namespace| namespace.body == enclosing(found.start()))
        {
            scopes.extend(
                namespaces
                    .iter()
                    .filter(|merged| merged.key == namespace.key)
                    .map(|merged| merged.body),
            );
        }
    }
    scopes
}

/// True when a string, template or regular expression literal lies in
/// `from..to`. The scopes are found on a copy of the code whose literals are
/// blanked, so a literal body (`=> 's'`) would otherwise look like whitespace
/// before the next `{`.
fn literal_between(source: &str, ignored: &[(usize, usize)], from: usize, to: usize) -> bool {
    ignored
        .iter()
        .any(|range| range.0 >= from && range.0 < to && !is_comment_range(source, *range))
}

/// End of an expression-bodied arrow function whose body starts at `from`:
/// the first top-level `;` or `,`, the closer of the enclosing group, or a
/// line terminator at which automatic semicolon insertion starts a new
/// statement ([`arrow_body_ends`]). A line terminator before a binary operator
/// (`(eval) => 1` + CR + `+eval(code)`) continues the body.
fn arrow_expression_end(source: &str, from: usize, ignored: &[(usize, usize)]) -> usize {
    let bytes = source.as_bytes();
    let mut depth = 0usize;
    let mut index = from;
    while index < bytes.len() {
        let range = ignored.partition_point(|(start, _)| *start <= index);
        if let Some((_, stop)) = range.checked_sub(1).map(|found| ignored[found])
            && index < stop
        {
            index = stop;
            continue;
        }
        let terminator = line_terminator_len(bytes, index);
        if terminator > 0 {
            if depth == 0
                && arrow_body_ends(
                    source,
                    index,
                    index + terminator,
                    ignored,
                    AwaitWord::Operator,
                )
            {
                return index;
            }
            index += terminator;
            continue;
        }
        match bytes[index] {
            b'(' | b'[' | b'{' => depth += 1,
            b')' | b']' | b'}' => match depth.checked_sub(1) {
                Some(next) => depth = next,
                None => return index,
            },
            b';' | b',' if depth == 0 => return index,
            _ => {}
        }
        index += 1;
    }
    bytes.len()
}

/// ECMAScript identifier syntax: Unicode `ID_Start` or `$`/`_`, then
/// `ID_Continue`, `$`, `_`, ZWNJ or ZWJ.
const UNICODE_IDENTIFIER: &str =
    r"(?:\p{XID_Start}|[$_])(?:\p{XID_Continue}|[$_\u{200C}\u{200D}])*";

/// Words that can follow `module`/`namespace` in expressions such as
/// `module in {}`; they are never namespace names.
const NON_NAMESPACE_WORDS: [&str; 3] = ["in", "instanceof", "of"];

/// A TypeScript `namespace`/`module` declaration body. Declarations with the
/// same `key` merge into one symbol and share its exported members.
struct NamespaceBody {
    key: String,
    body: (usize, usize),
}

/// Finds TypeScript namespace and module bodies, including dotted names,
/// nested declarations, Unicode names and quoted ambient module names.
///
/// The keyword and the name must share a line, as in TypeScript's grammar, so
/// automatic semicolon insertion keeps `module\nN\n{}` an ordinary block.
/// Keys follow the textual qualified name; an exported nested declaration
/// extends the key of its enclosing namespace, while any other nested
/// declaration gets a block-local key because it cannot merge with outer
/// declarations.
fn namespace_bodies(
    source: &str,
    code: &str,
    ignored: &[(usize, usize)],
    blocks: &[(usize, usize)],
) -> Vec<NamespaceBody> {
    let keyword = Regex::new(r"\b(namespace|module)").expect("static regex");
    let path = Regex::new(&format!(
        r"^({UNICODE_IDENTIFIER}(?:\s*\.\s*{UNICODE_IDENTIFIER})*)\s*\{{"
    ))
    .expect("static regex");
    let mut namespaces: Vec<NamespaceBody> = Vec::new();
    for found in keyword.find_iter(code) {
        let kind = found.as_str();
        // Literals are masked in `code`, so measure the same-line trivia gap in
        // the original source.
        let Some(cursor) = same_line_trivia_end(source, ignored, found.end()) else {
            continue;
        };
        // Member accesses such as `module.exports` or `a.namespace` are not
        // declarations.
        let preceding = code[..found.start()]
            .bytes()
            .rev()
            .find(|byte| !byte.is_ascii_whitespace());
        if matches!(preceding, Some(b'.' | b'$')) {
            continue;
        }
        let rest = &code[cursor..];
        let (name, open) = if let Some(captures) = path.captures(rest)
            && let (Some(name), Some(all)) = (captures.get(1), captures.get(0))
        {
            let name: String = name
                .as_str()
                .split('.')
                .map(str::trim)
                .collect::<Vec<_>>()
                .join(".");
            if NON_NAMESPACE_WORDS.contains(&name.as_str()) {
                continue;
            }
            (name, cursor + all.end() - 1)
        } else if kind == "module"
            && matches!(source.as_bytes().get(cursor), Some(b'"' | b'\''))
            && let Some(&(_, end)) = ignored.iter().find(|(start, _)| *start == cursor)
            && let Some(offset) = code[end..].find(|character: char| !character.is_whitespace())
            && code.as_bytes().get(end + offset) == Some(&b'{')
        {
            // `declare module "pkg" {}`: the literal is masked in `code`.
            (
                format!("'{}'", &source[cursor + 1..end.saturating_sub(1)]),
                end + offset,
            )
        } else {
            continue;
        };
        let Some(body) = blocks.iter().find(|(start, _)| *start == open).copied() else {
            continue;
        };
        let parent = blocks
            .iter()
            .filter(|(start, end)| *start < found.start() && found.start() < *end)
            .min_by_key(|(start, end)| end - start);
        let key = match parent {
            None => name,
            // Only an exported nested namespace is a member of its parent
            // symbol; any other nested declaration merges only inside its
            // own enclosing body.
            Some(parent) => match namespaces.iter().find(|outer| outer.body == *parent) {
                Some(outer) if exported_declaration(code, found.start()) => {
                    format!("{}.{name}", outer.key)
                }
                _ => format!("@{}.{name}", parent.0),
            },
        };
        namespaces.push(NamespaceBody { key, body });
    }
    namespaces
}

/// Skips whitespace and comments that stay on one line, starting at `from`.
/// Returns the position of the next token, or `None` when no trivia separates
/// it from `from` or a line terminator intervenes. A multi-line comment that
/// contains a line terminator counts as a line break, as in TypeScript.
fn same_line_trivia_end(source: &str, ignored: &[(usize, usize)], from: usize) -> Option<usize> {
    let is_line_terminator =
        |character: char| matches!(character, '\n' | '\r' | '\u{2028}' | '\u{2029}');
    let mut cursor = from;
    loop {
        let rest = &source[cursor..];
        if let Some(character) = rest.chars().next()
            && character.is_whitespace()
            && !is_line_terminator(character)
        {
            cursor += character.len_utf8();
            continue;
        }
        if rest.starts_with("/*")
            && let Some(&(_, end)) = ignored.iter().find(|(start, _)| *start == cursor)
            && !source[cursor..end.min(source.len())].contains(is_line_terminator)
        {
            cursor = end.min(source.len());
            continue;
        }
        break;
    }
    (cursor > from).then_some(cursor)
}

/// Modifiers that can sit between `export` and a declaration keyword, for
/// example `export declare var` or `export async function`.
const DECLARATION_MODIFIERS: [&str; 2] = ["declare", "async"];

/// Whether the declaration starting at `start` is directly preceded by
/// `export`, optionally followed by any of `DECLARATION_MODIFIERS` in any
/// order. Comments are masked in `code`, so they count as whitespace.
fn exported_declaration(code: &str, start: usize) -> bool {
    let word_boundary = |before: &str| {
        before.chars().next_back().is_none_or(|character| {
            !(character.is_alphanumeric() || character == '_' || character == '$')
        })
    };
    let mut prefix = code[..start].trim_end();
    'modifiers: loop {
        for modifier in DECLARATION_MODIFIERS {
            if let Some(before) = prefix.strip_suffix(modifier)
                && word_boundary(before)
            {
                prefix = before.trim_end();
                continue 'modifiers;
            }
        }
        break;
    }
    prefix.strip_suffix("export").is_some_and(word_boundary)
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
        before_for
            .chars()
            .next_back()
            .is_none_or(|character| !is_identifier_part(character))
    })
}

/// Finds the block that starts a function body after its parameter list.
/// See [`body_open_after_parameters`] for the rules; this adds the matching
/// `}` from the brace pairs of the masked code.
fn function_body_block(
    code: &str,
    _bytes: &[u8],
    blocks: &[(usize, usize)],
    cursor: usize,
) -> Option<(usize, usize)> {
    let open = body_open_after_parameters(code, cursor)?;
    blocks.iter().find(|(start, _)| *start == open).copied()
}

/// Whether a `{` at `open` of masked code starts a TypeScript type (an object
/// type literal) inside a return-type annotation instead of the body.
///
/// A type is expected where the previous token is `:` (the annotation colon or
/// the `: B` branch of a conditional type), `?` (the `A ? { .. } : B` branch),
/// an opener or separator (`<`, `(`, `[`, `,`), a type operator (`|`, `&`,
/// `=>`) or one of the keywords `extends` and `is` (`T extends { a: 1 } ? X :
/// Y`, `v is { a: 1 }`). Any other previous token (`)`, `]`, `>`, a type name
/// such as `void`) ends the type, so the brace is the body.
fn opens_type_literal(code: &str, open: usize) -> bool {
    let before = code[..open].trim_end();
    before.ends_with(['<', '|', '&', ',', '[', '(', ':', '?'])
        || before.ends_with("=>")
        || matches!(trailing_word(before), "extends" | "is")
}

/// The offset of the `{` that opens a function or method body, given the
/// offset just after the `)` of its parameter list, or `None` when there is no
/// body. One rule set decides it, on masked code:
///
/// 1. After optional whitespace, a `{` is the body.
/// 2. Otherwise a `:` starts a return-type annotation, anything else (`;`, the
///    next member, `}`) means the signature has no body.
/// 3. The annotation extends to the first `{` that is not a type literal (see
///    [`opens_type_literal`]); a type literal is skipped as a whole, so object
///    branches of a conditional type (`T extends U ? { a: 1 } : V`) and object
///    types in generics or function types never end the annotation.
/// 4. The annotation ends without a body at a `;`, at the `}` that closes the
///    enclosing block, or at a line break that cannot continue its type
///    (automatic semicolon insertion, see [`annotation_continues`]); an open
///    `(` or `[` keeps the annotation going across line breaks.
fn body_open_after_parameters(code: &str, after: usize) -> Option<usize> {
    let bytes = code.as_bytes();
    let mut cursor = skip_trivia(code, after);
    match bytes.get(cursor) {
        Some(b'{') => return Some(cursor),
        Some(b':') => cursor += 1,
        _ => return None,
    }
    // Open `(` and `[` of the annotation (`(a: number) => void`); a line break
    // or `;` inside them never ends the member.
    let mut nesting = 0usize;
    while let Some(character) = code[cursor..].chars().next() {
        if character.is_whitespace() {
            let run_start = cursor;
            let mut crossed_line = false;
            while let Some(next) = code[cursor..].chars().next() {
                if !next.is_whitespace() {
                    break;
                }
                crossed_line |= is_line_terminator(next);
                cursor += next.len_utf8();
            }
            if crossed_line && nesting == 0 && !annotation_continues(code, run_start, cursor) {
                return None;
            }
            continue;
        }
        match character {
            '{' => {
                if nesting == 0 && !opens_type_literal(code, cursor) {
                    return Some(cursor);
                }
                cursor = matching_delimiter(bytes, cursor, b'{', b'}')?;
            }
            // The member ends here: a `;`, or the `}` of the enclosing block
            // (every `}` of the annotation's own braces is skipped above).
            ';' | '}' => return None,
            '(' | '[' => nesting += 1,
            ')' | ']' => nesting = nesting.checked_sub(1)?,
            _ => {}
        }
        cursor += character.len_utf8();
    }
    None
}

/// Whether a line break between `run_start` and `next` (the whitespace run of
/// masked code) leaves the member signature open instead of ending it. The
/// body brace, a dangling operator before the break and a type operator or
/// closer after it continue the signature; any other token starts the next
/// member or statement.
fn annotation_continues(code: &str, run_start: usize, next: usize) -> bool {
    let bytes = code.as_bytes();
    let previous = bytes[..run_start].last().copied();
    let following = bytes.get(next).copied();
    let dangling = matches!(
        previous,
        Some(b':' | b'<' | b'|' | b'&' | b',' | b'[' | b'(' | b'=' | b'.' | b'?')
    ) || bytes[..run_start].ends_with(b"=>")
        || TYPE_OPERATOR_KEYWORDS.contains(&trailing_word(&code[..run_start]));
    let continuing = matches!(
        following,
        Some(b'{' | b'|' | b'&' | b'.' | b'?' | b':' | b'=' | b')' | b']' | b',' | b'<' | b'>')
    );
    dangling || continuing
}

/// Type operators that need a type after them, so a line break right after one
/// never ends the annotation (`T extends\n string ? A : B`).
const TYPE_OPERATOR_KEYWORDS: [&str; 7] = [
    "extends", "keyof", "typeof", "infer", "unique", "readonly", "is",
];

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
        .rsplit(|character: char| !is_identifier_part(character))
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
    !raw_prefix[keyword_start..].contains(is_line_terminator)
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
        .rsplit_once(|character: char| {
            matches!(character, '{' | '}') || is_statement_end(character)
        })
        .map_or(label, |(_, rest)| rest)
        .trim();
    statement == "default"
        || statement
            .strip_prefix("case")
            .is_some_and(|rest| rest.chars().next().is_some_and(char::is_whitespace))
}

/// Whether the `?` at `index` of masked code is the question mark of a
/// conditional expression, not part of `??`, `?.` (outside `?.5`) or an
/// optional marker (`x?: T`).
fn is_conditional_question(bytes: &[u8], index: usize) -> bool {
    let next = bytes.get(index + 1).copied();
    let previous = index
        .checked_sub(1)
        .and_then(|before| bytes.get(before))
        .copied();
    if next == Some(b'?') || previous == Some(b'?') || next == Some(b':') {
        return false;
    }
    !(next == Some(b'.') && !bytes.get(index + 2).is_some_and(u8::is_ascii_digit))
}

/// What a `{` opens, as far as a lexical scan can tell.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum BraceKind {
    /// The body of a `class` declaration or expression.
    ClassBody,
    /// An object literal (a value position, not a statement block).
    ObjectLiteral,
    /// A statement block, function body, or any brace the scan cannot place.
    Other,
}

/// Modifiers that may precede a method name in a class body or object literal.
const METHOD_MODIFIERS: [&str; 10] = [
    "async",
    "static",
    "get",
    "set",
    "public",
    "private",
    "protected",
    "override",
    "readonly",
    "abstract",
];

/// Keywords after which an expression, and so an object literal, can start.
/// [`brace_holds_members`] and the method scan share this one table.
const OBJECT_VALUE_KEYWORDS: [&str; 13] = [
    "return",
    "typeof",
    "case",
    "void",
    "yield",
    "throw",
    "new",
    "in",
    "of",
    "delete",
    "await",
    "instanceof",
    "default",
];

/// Keywords that end their statement when a line terminator follows them
/// (ECMAScript restricted productions): `return\n{ ... }` returns nothing and
/// then opens a block, `break` and `continue` take only a label on their line.
const RESTRICTED_KEYWORDS: [&str; 5] = ["return", "throw", "yield", "break", "continue"];

/// Lazily built lexical context that decides whether an `eval` name followed
/// by a parameter list is a class or object method declaration.
struct MemberScan<'a> {
    source: &'a str,
    ignored: &'a [(usize, usize)],
    code: String,
    blocks: Vec<(usize, usize)>,
}

impl<'a> MemberScan<'a> {
    fn new(source: &'a str, ignored: &'a [(usize, usize)]) -> Self {
        let code = masked_code(source, ignored);
        let blocks = brace_blocks(code.as_bytes());
        Self {
            source,
            ignored,
            code,
            blocks,
        }
    }

    /// The innermost brace pair that contains `position` with only balanced
    /// brackets between the opening brace and `position`, so `position` sits
    /// directly in that brace's body and not inside a call or array.
    fn direct_enclosing_block(&self, position: usize) -> Option<(usize, usize)> {
        let (open, close) = self
            .blocks
            .iter()
            .filter(|(open, close)| *open < position && position < *close)
            .min_by_key(|(open, close)| close - open)
            .copied()?;
        let mut depth = 0usize;
        for byte in &self.code.as_bytes()[open + 1..position] {
            match byte {
                b'(' | b'[' | b'{' => depth += 1,
                b')' | b']' | b'}' => depth = depth.checked_sub(1)?,
                _ => {}
            }
        }
        (depth == 0).then_some((open, close))
    }

    /// Whether the header before the brace at `open` contains a `class`
    /// keyword (`class A extends B {`, `const C = class {`). The scan reads
    /// the masked code by character, so non-ASCII names and type-argument
    /// lists with braces (`class C<T extends { a: 1 }> {`) are handled.
    fn has_class_header(&self, open: usize) -> bool {
        class_heading_scan(&self.code, open, &[], true)
    }

    fn brace_kind(&self, open: usize) -> BraceKind {
        if self.has_class_header(open) {
            return BraceKind::ClassBody;
        }
        let is_object = match previous_significant_token(self.source, open, self.ignored) {
            Token::Punct(text) => match text {
                // An opener, a comma, a spread, or any unary, binary, logical,
                // nullish, assignment or conditional operator: each ends in one
                // of these characters and is followed by an expression, so a
                // brace there is an object literal (`typeof {`, `...{`,
                // `a + {`, `a ?? {`, `a && {`, `a **= {`, `x ? {`, `a | {`).
                "=" | "(" | "," | "[" | "?" | "|" | "&" | "..." | "+" | "-" | "*" | "/" | "%"
                | "^" | "~" | "!" | "<" => true,
                // `=>` opens a function body, a closing type-argument angle
                // bracket is followed by a body (`interface I<T> {`), and any
                // other `>` is a comparison or shift operator.
                ">" => self.greater_than_precedes_object(open),
                // A property value (`{ key: { eval(x) {} } }`) or the
                // alternate of a conditional (`c ? a : { eval(x) {} }`); a
                // label or a `case` clause is a statement block and stays
                // unplaced.
                ":" => {
                    self.colon_ends_conditional(open)
                        || self.direct_enclosing_block(open).is_some_and(|(outer, _)| {
                            self.brace_kind(outer) == BraceKind::ObjectLiteral
                        })
                }
                // The `${` of a template substitution holds an expression.
                "{" => {
                    self.opens_template_substitution(open)
                        || self.opens_jsx_attribute_container(open)
                }
                _ => false,
            },
            Token::Word { text, member } => {
                !member
                    && OBJECT_VALUE_KEYWORDS.contains(&text)
                    // A line terminator after a restricted keyword (comments
                    // included) ends the statement, so the brace is a block.
                    && !(RESTRICTED_KEYWORDS.contains(&text) && self.line_break_before(open))
            }
            _ => false,
        };
        if is_object {
            BraceKind::ObjectLiteral
        } else {
            BraceKind::Other
        }
    }

    /// Whether the `>` before the brace at `open` is a comparison or shift
    /// operator. An arrow (`=>`) and the end of a type-argument list are not.
    fn greater_than_precedes_object(&self, open: usize) -> bool {
        let prefix = strip_comments_for_prefix(self.source, open, self.ignored);
        !prefix.ends_with("=>")
            && type_arguments_open(self.source, prefix.len() - 1, self.ignored).is_none()
    }

    /// Whether the `:` before the brace at `open` is the alternate branch of a
    /// conditional expression: an unmatched `?` precedes it in the same
    /// statement and brackets, after nested conditionals are paired off.
    fn colon_ends_conditional(&self, open: usize) -> bool {
        let bytes = self.code.as_bytes();
        let mut index = strip_comments_for_prefix(self.source, open, self.ignored)
            .len()
            .saturating_sub(1);
        // Colons that still wait for their `?`, starting with this one.
        let mut pending = 1usize;
        let mut depth = 0usize;
        while index > 0 {
            index -= 1;
            match bytes[index] {
                b')' | b']' | b'}' => depth += 1,
                b'(' | b'[' | b'{' => match depth.checked_sub(1) {
                    Some(next) => depth = next,
                    None => return false,
                },
                b';' if depth == 0 => return false,
                b':' if depth == 0 => pending += 1,
                b'?' if depth == 0 && is_conditional_question(bytes, index) => {
                    pending -= 1;
                    if pending == 0 {
                        return true;
                    }
                }
                _ => {}
            }
        }
        false
    }

    /// Whether the `{` before the brace at `open` is the `${` that starts a
    /// template substitution. The previous-token scan reports that `${` as `{`.
    fn opens_template_substitution(&self, open: usize) -> bool {
        let end = strip_comments_for_prefix(self.source, open, self.ignored).len();
        self.ignored.iter().any(|(start, finish)| {
            let text = &self.source.as_bytes()[*start..(*finish).min(self.source.len())];
            *finish == end && text.ends_with(b"${") && matches!(text.first(), Some(b'`' | b'}'))
        })
    }

    /// Whether the `{` before the brace at `open` opens a JSX attribute
    /// expression container (`<Widget value={{ ... }} />`): the brace follows
    /// `name=`. A plain `= {` followed by a second `{` is not valid script
    /// code, so the pattern cannot hide a statement block.
    fn opens_jsx_attribute_container(&self, open: usize) -> bool {
        let container = strip_comments_for_prefix(self.source, open, self.ignored).len() - 1;
        matches!(
            previous_significant_token(self.source, container, self.ignored),
            Token::Punct("=")
        ) && {
            let equals = strip_comments_for_prefix(self.source, container, self.ignored).len() - 1;
            matches!(
                previous_significant_token(self.source, equals, self.ignored),
                Token::Word { .. }
            )
        }
    }

    /// Whether the offset starts a member in the body of a `kind` brace: the
    /// previous token (past any modifiers and a generator `*`) ends an earlier
    /// member or opens the body.
    fn at_member_position(&self, name_start: usize, kind: BraceKind) -> bool {
        let mut member_start = name_start;
        loop {
            let token = previous_significant_token(self.source, member_start, self.ignored);
            let token_end =
                strip_comments_for_prefix(self.source, member_start, self.ignored).len();
            // A decorator (`@dec`, `@a.b`, `@dec(args)`, `@(expr)`) on the same
            // line is part of the member, whatever token ends it.
            if matches!(token, Token::Word { .. } | Token::Punct(")"))
                && let Some(at) = self.decorator_start(token_end)
            {
                member_start = at;
                continue;
            }
            match token {
                Token::Word {
                    text,
                    member: false,
                } if METHOD_MODIFIERS.contains(&text) => {
                    member_start = token_end - text.len();
                }
                Token::Punct("*") => member_start = token_end - 1,
                Token::Punct("{" | "}" | ";") => return true,
                Token::Punct(",") => return kind == BraceKind::ObjectLiteral,
                // A class body has no automatic-semicolon-free call
                // statements, so a member that starts a new line after a
                // complete field or decorator is a declaration.
                Token::Punct(")" | "]" | "++" | "--")
                | Token::Literal
                | Token::Number
                | Token::Template => {
                    return kind == BraceKind::ClassBody
                        && self.new_line_between(token_end, member_start);
                }
                // A name ends a field or signature, a member access
                // (`x = a.b`) and a private name (`#state`) included; only an
                // expression keyword (`x = typeof`) leaves the member open.
                Token::Word { text, member } => {
                    return kind == BraceKind::ClassBody
                        && (member
                            || !REGEX_KEYWORDS.contains(&text)
                            || self.void_ends_type(token_end, text))
                        && self.new_line_between(token_end, member_start);
                }
                _ => return false,
            }
        }
    }

    /// Whether the word `text` that ends at `end` is the type `void` of a
    /// field or method annotation (`run(): void`, `x: A | void`), which ends a
    /// member like any other type name, not the unary operator `void`.
    fn void_ends_type(&self, end: usize, text: &str) -> bool {
        if text != "void" {
            return false;
        }
        let start = end - text.len();
        let before = strip_comments_for_prefix(self.source, start, self.ignored);
        before.ends_with("=>") || before.ends_with([':', '|', '&'])
    }

    /// The offset of the `@` of the decorator that ends at `end`, or `None`
    /// when the code before `end` is not a decorator. A decorator is `@` plus
    /// a dotted name with optional call arguments (`@a.b(1)`), or a
    /// parenthesized expression (`@(expr)`).
    fn decorator_start(&self, end: usize) -> Option<usize> {
        let code = self.code.as_str();
        let mut cursor = end;
        if code[..cursor].ends_with(')') {
            cursor = matching_backward(code.as_bytes(), cursor - 1, b'(', b')')?;
            // `@(expr)`: the parenthesis itself follows the `@`.
            if code[..cursor].ends_with('@') {
                return Some(cursor - 1);
            }
        }
        loop {
            let word = trailing_word(&code[..cursor]);
            if word.is_empty() {
                return None;
            }
            cursor -= word.len();
            if code[..cursor].ends_with('@') {
                return Some(cursor - 1);
            }
            cursor = cursor
                .checked_sub(1)
                .filter(|_| code[..cursor].ends_with('.'))?;
        }
    }

    fn new_line_between(&self, start: usize, end: usize) -> bool {
        self.source[start..end].contains(is_line_terminator)
    }

    /// Whether a line terminator, possibly inside a block comment, separates
    /// the previous significant token from `offset`.
    fn line_break_before(&self, offset: usize) -> bool {
        let token_end = strip_comments_for_prefix(self.source, offset, self.ignored).len();
        self.new_line_between(token_end, offset)
    }

    /// The offset of the `(` that opens the parameter list of a function-like
    /// `eval`: the next significant token, or the one after an optional
    /// balanced type-parameter list (`eval<T>(value: T)`).
    fn parameters_open(&self, name_end: usize) -> Option<usize> {
        let bytes = self.code.as_bytes();
        let mut cursor = skip_trivia(&self.code, name_end);
        if bytes.get(cursor) == Some(&b'<') {
            cursor = skip_trivia(&self.code, type_parameters_end(bytes, cursor)?);
        }
        (bytes.get(cursor) == Some(&b'(')).then_some(cursor)
    }

    /// Whether the `eval` at `start..end` names a method. Both parts of one
    /// rule set must match unambiguously; anything else is a call:
    ///
    /// 1. Signature: an optional balanced type-parameter list, a balanced
    ///    parameter list, an optional return-type annotation and a `{` body
    ///    (see [`body_open_after_parameters`]).
    /// 2. Context: the name sits directly in a class body or in an object
    ///    literal (see [`Self::brace_kind`]) at a member position.
    fn declares_method(&self, start: usize, name_end: usize) -> bool {
        let Some(open_paren) = self.parameters_open(name_end) else {
            return false;
        };
        let Some(close_paren) = matching_delimiter(self.code.as_bytes(), open_paren, b'(', b')')
        else {
            return false;
        };
        if body_open_after_parameters(&self.code, close_paren + 1).is_none() {
            return false;
        }
        let Some((open, _)) = self.direct_enclosing_block(start) else {
            return false;
        };
        let kind = self.brace_kind(open);
        kind != BraceKind::Other && self.at_member_position(start, kind)
    }
}

/// The offset of the first non-whitespace character at or after `from`.
fn skip_trivia(code: &str, from: usize) -> usize {
    from + (code[from..].len() - code[from..].trim_start().len())
}

/// The offset just after the `>` that closes the type-parameter list opened by
/// the `<` at `open` of masked code, or `None` when the angle brackets do not
/// balance before a statement end.
///
/// Brackets, parentheses and braces nest (`T extends { a: Array<1> }`,
/// `T extends (x: number) => void`) and hide their angle brackets; the `>` of
/// an arrow (`=>`) closes nothing.
fn type_parameters_end(bytes: &[u8], open: usize) -> Option<usize> {
    let mut angles = 0usize;
    let mut nesting = 0usize;
    for (index, byte) in bytes.iter().enumerate().skip(open) {
        match byte {
            b'(' | b'[' | b'{' => nesting += 1,
            b')' | b']' | b'}' => nesting = nesting.checked_sub(1)?,
            b';' if nesting == 0 => return None,
            b'<' if nesting == 0 => angles += 1,
            b'>' if nesting == 0 && bytes[index - 1] != b'=' => {
                angles -= 1;
                if angles == 0 {
                    return Some(index + 1);
                }
            }
            _ => {}
        }
    }
    None
}

/// Zero-based line and byte column of the first direct `eval` call in
/// `source`.
#[cfg(test)]
fn direct_eval_line(source: &str) -> Option<(u32, u32)> {
    let start = direct_eval_offsets(source).into_iter().next()?;
    offset_position(source, start)
}

/// Zero-based line and column of `offset` in `source`, as `u32` values.
fn offset_position(source: &str, offset: usize) -> Option<(u32, u32)> {
    let (line, column) = line_and_column(source, offset);
    Some((u32::try_from(line).ok()?, u32::try_from(column).ok()?))
}

/// Byte offsets of every direct `eval` call in `source`, in source order. The
/// same classifier reads original modules and emitted chunks.
fn direct_eval_offsets(source: &str) -> Vec<usize> {
    // `eval(` inside strings, templates, comments or regular expressions is
    // not a call.
    let ignored = non_code_ranges(source);
    // A binding named `eval` makes calls inside its scope ordinary calls that
    // cannot observe the surrounding scope; shadowing is decided per call.
    let shadows = eval_shadow_scopes(source, &ignored);
    let mut declarations: Option<MemberScan<'_>> = None;
    source
        .match_indices("eval")
        .filter_map(|(start, name)| {
            let end = start + name.len();
            let whole_word = !source[..start].ends_with(is_identifier_part)
                && !source[end..].starts_with(is_identifier_part);
            if !whole_word || in_ranges(&ignored, start) {
                return None;
            }
            // A property or private name (`obj.eval(x)`, `obj. // c` + newline
            // + `eval(x)`, `this.#eval(x)`) is an ordinary call. A spread
            // (`...eval(x)`) is not a member access.
            let member = matches!(
                previous_significant_token(source, end, &ignored),
                Token::Word { member: true, .. }
            );
            // Comments and line terminators may separate the name and `(`; a
            // balanced type-parameter list may be attached to the name
            // (`eval<T>(x)`).
            let called = match next_significant_token(source, end, &ignored).1 {
                Token::Punct("(") => true,
                // A `<` that follows the name after whitespace or a comment
                // is a comparison (`eval < value > (code)`), as for every
                // other type-argument list in these scans.
                Token::Punct("<") => {
                    source[end..].starts_with('<')
                        && declarations
                            .get_or_insert_with(|| MemberScan::new(source, &ignored))
                            .parameters_open(end)
                            .is_some()
                }
                _ => false,
            };
            let shadowed = shadows
                .iter()
                .any(|(begin, finish)| (*begin..*finish).contains(&start));
            if member || !called || shadowed {
                return None;
            }
            // `eval(code) { ... }` in a class body or object literal declares a
            // method named `eval`; it never calls the global function.
            let declaration = declarations.get_or_insert_with(|| MemberScan::new(source, &ignored));
            (!declaration.declares_method(start, end)).then_some(start)
        })
        .collect()
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

/// A direct `eval` call found in an emitted chunk whose usable source map
/// attributes its generated position to a module's resource. A chunk without a
/// usable source map never yields one: being the only listed module, or the
/// number of calls, does not prove who owns generated bytes.
#[derive(Debug, Clone, Copy)]
struct EmittedEvalCall {
    chunk: usize,
    offset: usize,
}

/// What the emitted chunks show about the direct eval calls of one module.
#[derive(Debug)]
enum EmittedEval {
    /// At least one emitted direct eval call belongs to the module.
    /// `unverified` keeps one reason for every other copy of the module whose
    /// calls could not be tied to it (a chunk without a usable source map, an
    /// unreadable chunk, an unmapped segment, a differing or shared resource):
    /// that copy may still spell names the attributed copy mangled, so its
    /// uncertainty must survive next to the attributed calls.
    Emitted {
        calls: Vec<EmittedEvalCall>,
        unverified: Vec<String>,
    },
    /// Every chunk of the module was checked and none holds a direct eval call
    /// that belongs to it: the build removed the call.
    Absent,
    /// The emitted calls cannot be tied to the module; every entry names one
    /// evidence limit.
    Unverified(Vec<String>),
}

/// Direct eval offsets per emitted chunk, computed once per chunk.
#[derive(Default)]
struct EmittedEvalScan {
    offsets: BTreeMap<usize, Vec<usize>>,
}

impl EmittedEvalScan {
    fn chunk_offsets(&mut self, context: &Context<'_>, index: usize) -> &[usize] {
        self.offsets
            .entry(index)
            .or_insert_with(|| direct_eval_offsets(&context.chunks[index].code))
    }

    /// Finds the direct eval calls of the emitted chunks that belong to the
    /// module. One conservative rule decides attribution: a call counts only
    /// when the chunk source map attributes its generated position to the
    /// module's resource (file, import query and fragment, with the same
    /// sharing rules as `Context::final_emission`). A direct eval call in a
    /// chunk without a usable source map is never attributed to a module, and
    /// no original location is claimed from call counts: the chunk may hold
    /// copies of a module, code of other modules, or code that a banner, a
    /// runtime or a late output plugin injected. Such a call is unverified:
    /// it leaves the result unverified, and next to an attributed call in
    /// another chunk it stays listed in [`EmittedEval::Emitted`], so one
    /// mapped copy never speaks for an unchecked one. A chunk without any
    /// direct eval call is still proof that the build removed the call from
    /// that chunk.
    fn classify(&mut self, context: &Context<'_>, module: &CollectedModule) -> EmittedEval {
        let Some(path) = module.path.as_deref() else {
            return EmittedEval::Unverified(vec![
                "the module has no file path to match against source map sources".to_owned(),
            ]);
        };
        if module.chunks.is_empty() {
            return EmittedEval::Unverified(vec![
                "the engine lists no output chunk for the module".to_owned(),
            ]);
        }
        let identity = ResourceIdentity::from_module(&module.id, path);
        let sharing_modules = context.resource_sharing().count(&identity);
        let mut calls = Vec::new();
        let mut unverified: Vec<String> = Vec::new();
        for file in &module.chunks {
            let Some(index) = context.chunk_index(file) else {
                unverified.push(format!("{file} could not be read"));
                continue;
            };
            let offsets = self.chunk_offsets(context, index).to_vec();
            if offsets.is_empty() {
                continue;
            }
            let chunk = &context.chunks[index];
            if chunk.map.is_none() {
                unverified.push(format!(
                    "{file} has no usable source map, so its direct eval calls cannot be \
                     attributed to a module"
                ));
                continue;
            }
            for offset in offsets {
                let (line, column) = chunk.position(offset);
                match chunk.original(line, column) {
                    Some((source, _, _)) => {
                        let source = ResourceIdentity::from_source_path(&source);
                        if source.file != identity.file {
                            continue;
                        }
                        if source.query != identity.query || source.fragment != identity.fragment {
                            unverified.push(format!(
                                "{file} maps a direct eval call at {}:{} to the same file under a \
                                 different resource query or fragment",
                                line + 1,
                                column + 1
                            ));
                        } else if sharing_modules > 1 {
                            unverified.push(format!(
                                "{file} maps a direct eval call at {}:{} to a resource that \
                                 {sharing_modules} collected modules share",
                                line + 1,
                                column + 1
                            ));
                        } else {
                            calls.push(EmittedEvalCall {
                                chunk: index,
                                offset,
                            });
                        }
                    }
                    None if chunk.lacks_source_mapping(line, column) => {
                        unverified.push(format!(
                            "{file} has a direct eval call at {}:{} that no source mapping \
                             attributes",
                            line + 1,
                            column + 1
                        ));
                    }
                    // The call maps to a source that is not a file (a bundler
                    // virtual module), so it is not this module's.
                    None => {}
                }
            }
        }
        if !calls.is_empty() {
            EmittedEval::Emitted { calls, unverified }
        } else if unverified.is_empty() {
            EmittedEval::Absent
        } else {
            EmittedEval::Unverified(unverified)
        }
    }

    /// The declared `names` of the module that the chunk at `chunk_index` still
    /// spells. Only occurrences in code that the chunk's usable source map
    /// attributes to the module's resource count, so a name that a sibling
    /// module of a shared chunk spells the same way, or that no mapping covers,
    /// never supports the finding. A chunk without a usable source map is not
    /// counted at all: it cannot attribute any occurrence to the module.
    fn retained_names(
        &self,
        context: &Context<'_>,
        module: &CollectedModule,
        chunk_index: usize,
        names: &[String],
    ) -> Vec<String> {
        let Some(path) = module.path.as_deref() else {
            return Vec::new();
        };
        let chunk = &context.chunks[chunk_index];
        if chunk.map.is_none() {
            return Vec::new();
        }
        let identity = ResourceIdentity::from_module(&module.id, path);
        names
            .iter()
            .filter(|name| {
                let pattern = Regex::new(&format!(
                    r"(?:^|[^\w$])({})(?:[^\w$]|$)",
                    regex::escape(name)
                ))
                .expect("escaped regex");
                // Search from the end of each name, not of its trailing
                // delimiter, so adjacent occurrences (`name,name`) are found.
                let mut from = 0;
                while let Some(found) = pattern
                    .captures_at(&chunk.code, from)
                    .and_then(|captures| captures.get(1))
                {
                    let (line, column) = chunk.position(found.start());
                    if chunk.original(line, column).is_some_and(|(source, _, _)| {
                        ResourceIdentity::from_source_path(&source) == identity
                    }) {
                        return true;
                    }
                    from = found.end();
                }
                false
            })
            .cloned()
            .collect()
    }
}

fn direct_eval_mangling(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let collection = context.collection;
    if !collection.minify_enabled || !collection.default_minifier {
        return;
    }
    // Without mangling, local names stay readable whatever the module
    // contains, so a direct eval call blocks nothing. An engine EVAL log or a
    // source-scan candidate never overrides an explicit disabled setting.
    if collection.mangle == MangleSetting::Disabled {
        tracing::debug!("skipping direct-eval-blocks-mangling: the build disables name mangling");
        return;
    }
    let mut emitted_scan: Option<EmittedEvalScan> = None;
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
        // The original-source candidate: a call in the module text proves
        // nothing about the output, because the build can remove it.
        let source_offsets = direct_eval_offsets(&source);
        let source_eval = source_offsets
            .first()
            .and_then(|offset| offset_position(&source, *offset));
        if rolldown_log.is_none() && rspack_bailout.is_none() && source_eval.is_none() {
            continue;
        }
        let engine_reported = rolldown_log.is_some() || rspack_bailout.is_some();
        // Both engines report eval while they scan modules, before tree
        // shaking and minification, so a report does not prove that the call
        // is emitted. Every finding needs an emitted call attributed to the
        // module; an engine report whose emission cannot be checked stays a
        // finding with Inferred status, and a call the build removed is not
        // one.
        let scan = emitted_scan.get_or_insert_with(EmittedEvalScan::default);
        let emission = scan.classify(context, module);
        let (calls, unverified) = match emission {
            EmittedEval::Emitted { calls, unverified } => (calls, unverified),
            EmittedEval::Absent => {
                tracing::debug!(
                    module = %context.module_log_label(&module.id),
                    engine_reported,
                    "skipping direct-eval-blocks-mangling: no emitted direct eval call belongs to the module"
                );
                continue;
            }
            EmittedEval::Unverified(reasons) => {
                if !engine_reported {
                    tracing::debug!(
                        module = %context.module_log_label(&module.id),
                        reason = %reasons.join("; "),
                        "skipping direct-eval-blocks-mangling: emitted direct eval calls cannot be attributed to the module"
                    );
                    continue;
                }
                (Vec::new(), reasons)
            }
        };
        // Retained names count only in chunks that hold an emitted call
        // attributed to the module, and there only at positions the chunk's
        // usable source map attributes to the module. Without an attributed
        // call no chunk is scanned: a name in a copy that holds no call says
        // nothing about a different copy whose call could not be attributed.
        let mut call_chunks: Vec<usize> = calls.iter().map(|call| call.chunk).collect();
        call_chunks.sort_unstable();
        call_chunks.dedup();
        let names = declared_names(&source);
        let mut retained: BTreeSet<String> = BTreeSet::new();
        let mut chunk_files = Vec::new();
        for index in &call_chunks {
            chunk_files.push(context.chunks[*index].file.clone());
            retained.extend(scan.retained_names(context, module, *index, &names));
        }
        // Copies that hold a direct eval call but no usable source map cannot
        // attribute any name to the module.
        let mut unchecked_files: Vec<String> = Vec::new();
        for file in &module.chunks {
            let Some(index) = context.chunk_index(file) else {
                continue;
            };
            if context.chunks[index].map.is_none()
                && !scan.chunk_offsets(context, index).is_empty()
                && !unchecked_files.contains(&context.chunks[index].file)
            {
                unchecked_files.push(context.chunks[index].file.clone());
            }
        }
        // Names that survive nowhere checked and no copy left unchecked: the
        // minifier mangled them despite eval, so nothing is blocked. Any
        // unverified copy, an engine-reported eval without an attributed call
        // included, keeps the finding Inferred instead.
        if retained.is_empty() && unverified.is_empty() {
            continue;
        }
        let emitted = !calls.is_empty();
        let (line, column) = rolldown_log
            .and_then(|log| Some((log.line?.saturating_sub(1), log.column.unwrap_or(0))))
            .or(source_eval)
            .unwrap_or((0, 0));
        let display = context.module_display(module);
        let mut located = match calls.first() {
            // The engine log and the source scan name the first call they saw,
            // and the build can remove that call while a later one survives.
            // Locate the finding at the call that survives: the original
            // position the source map gives the emitted call.
            Some(call) => context.chunk_location(call.chunk, call.offset),
            // Without an attributed call nothing ties the engine or source
            // position to the emitted code, so it is only a candidate.
            None => context.module_location(module, line, column),
        };
        if let Some(call) = calls.first() {
            let chunk = &context.chunks[call.chunk];
            let (generated_line, generated_column) = chunk.position(call.offset);
            let emitted_location = SourceLocation {
                kind: LocationKind::Generated,
                source_id: String::new(),
                path: chunk.file.clone(),
                line: generated_line + 1,
                column: generated_column + 1,
            };
            // A location that already is the generated position is not
            // repeated as the paired generated location.
            if located
                .location
                .as_ref()
                .is_none_or(|location| location.kind != LocationKind::Generated)
            {
                located.generated = Some(emitted_location);
            }
        }
        let retained_list: Vec<String> = retained.into_iter().take(12).collect();
        let mut evidence = Vec::new();
        if let Some(log) = rolldown_log {
            evidence.push(Evidence::new(
                "rolldown log EVAL",
                EvidenceStatus::Confirmed,
                format!(
                    "{} (reported while scanning modules, before tree shaking)",
                    log.message.lines().next().unwrap_or_default()
                ),
            ));
        }
        if let Some(bailout) = rspack_bailout {
            evidence.push(Evidence::new(
                "rspack stats (optimizationBailout)",
                EvidenceStatus::Confirmed,
                bailout.clone(),
            ));
        }
        if let Some((source_line, _)) = source_eval {
            evidence.push(Evidence::new(
                "bundlewhy source scan",
                EvidenceStatus::Inferred,
                format!(
                    "Direct eval found in the original source at line {}{}. This is a candidate \
                     only: the call can be removed before output, so it does not show that the \
                     call blocks mangling.",
                    source_line + 1,
                    if engine_reported {
                        ""
                    } else {
                        "; the engine did not report it"
                    }
                ),
            ));
        }
        if emitted {
            let described: Vec<String> = calls
                .iter()
                .take(3)
                .map(|call| {
                    let chunk = &context.chunks[call.chunk];
                    let (call_line, call_column) = chunk.position(call.offset);
                    format!(
                        "{}:{}:{} (attributed by the source map)",
                        chunk.file,
                        call_line + 1,
                        call_column + 1
                    )
                })
                .collect();
            let more = calls.len().saturating_sub(described.len());
            evidence.push(Evidence::new(
                "emitted chunk scan",
                EvidenceStatus::Confirmed,
                format!(
                    "Direct eval call survives in the emitted code at {}{}",
                    described.join(", "),
                    if more > 0 {
                        format!(" and {more} more")
                    } else {
                        String::new()
                    }
                ),
            ));
        } else if !unverified.is_empty() {
            evidence.push(Evidence::new(
                "emitted chunk scan",
                EvidenceStatus::Inferred,
                format!(
                    "No emitted direct eval call could be tied to the module: {}",
                    unverified.join("; ")
                ),
            ));
        }
        if emitted && !unverified.is_empty() {
            evidence.push(Evidence::new(
                "emitted chunk scan",
                EvidenceStatus::Inferred,
                format!(
                    "Other emitted copies of the module could not be checked: {}",
                    unverified.join("; ")
                ),
            ));
        }
        if !retained_list.is_empty() {
            evidence.push(Evidence::new(
                "generated chunk text",
                EvidenceStatus::Confirmed,
                format!(
                    "Declared names still present in {}: {}",
                    chunk_files.join(", "),
                    retained_list.join(", ")
                ),
            ));
        }
        if !unchecked_files.is_empty() {
            evidence.push(Evidence::new(
                "generated chunk text",
                EvidenceStatus::Inferred,
                format!(
                    "Declared names were not counted in {}: a chunk without a usable source map \
                     cannot attribute any name to the module",
                    unchecked_files.join(", ")
                ),
            ));
        }
        let mut summary = "Direct eval can read any variable in scope by name, so the minifier \
                           keeps the names of every binding the eval call can reach."
            .to_owned();
        if !emitted {
            summary.push_str(
                " The emitted code could not be checked for the call, so the build may already \
                 have removed it.",
            );
        }
        drafts.push(DiagnosticDraft {
            rule: RuleId::DirectEvalBlocksMangling,
            // The engine report confirms the eval; keeping the finding
            // Confirmed also needs the call to be present in the emitted code.
            status: if engine_reported && emitted {
                EvidenceStatus::Confirmed
            } else {
                EvidenceStatus::Inferred
            },
            title: format!("Direct eval in {display} keeps names unmangled"),
            summary,
            conditions: vec![
                Condition::new(
                    "No direct eval can observe local names",
                    ConditionOutcome::Unmet,
                    match (emitted, engine_reported) {
                        (true, true) => {
                            "Direct eval reported by the engine and present in the emitted code"
                        }
                        (true, false) => {
                            "Direct eval found in the source and present in the emitted code"
                        }
                        (false, _) => {
                            "Direct eval reported by the engine; the emitted code could not be \
                             checked"
                        }
                    },
                ),
                if retained_list.is_empty() {
                    Condition::new(
                        "Declared names are shortened in the output",
                        ConditionOutcome::Unknown,
                        format!("Not checked: {}", unverified.join("; ")),
                    )
                } else {
                    Condition::new(
                        "Declared names are shortened in the output",
                        ConditionOutcome::Unmet,
                        format!("Unmangled: {}", retained_list.join(", ")),
                    )
                },
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
    use super::{
        super::await_context::await_operator_offsets,
        env_guard::{EnvGuard, env_guard},
        *,
    };

    /// Keyword unary operators whose operand may be parenthesized.
    const UNARY_KEYWORDS: [&str; 4] = ["typeof", "await", "void", "delete"];

    fn eval_rule_drafts(source: &str, engine_reports_eval: bool) -> Vec<DiagnosticDraft> {
        eval_rule_drafts_for_modules(&[("src/main.js", source)], engine_reports_eval)
    }

    /// Runs the rule over several modules that share one generated chunk. The
    /// chunk is emitted as written: every module source in order, mapped line
    /// by line to its own file. The engine log, when requested, names the
    /// first module.
    fn eval_rule_drafts_for_modules(
        modules: &[(&str, &str)],
        engine_reports_eval: bool,
    ) -> Vec<DiagnosticDraft> {
        let lines: Vec<(usize, &str)> = modules
            .iter()
            .enumerate()
            .flat_map(|(index, (_, source))| source.lines().map(move |line| (index, line)))
            .collect();
        eval_rule_drafts_with_chunk(modules, &lines, true, engine_reports_eval)
    }

    /// Runs the rule over modules whose shared chunk `emitted` holds: each
    /// entry is a generated line and the index of the module its source map
    /// segment names. With `mapped` false the chunk has no source map.
    fn eval_rule_drafts_with_chunk(
        modules: &[(&str, &str)],
        emitted: &[(usize, &str)],
        mapped: bool,
        engine_reports_eval: bool,
    ) -> Vec<DiagnosticDraft> {
        let mut code = emitted
            .iter()
            .map(|(_, line)| *line)
            .collect::<Vec<_>>()
            .join("\n");
        code.push('\n');
        // Each generated line maps to the next line of its own module's
        // source, so a reported original line is the line in that file.
        let mut next_source_line = vec![0u32; modules.len()];
        let tokens: Vec<MapToken> = emitted
            .iter()
            .enumerate()
            .map(|(line, (module, _))| {
                let source_line = next_source_line[*module];
                next_source_line[*module] += 1;
                (u32::try_from(line).unwrap(), 0, *module, source_line, 0)
            })
            .collect();
        eval_rule_drafts_with_tokens(
            modules,
            &code,
            mapped.then_some(&tokens),
            engine_reports_eval,
        )
    }

    /// A source-map token: generated line and column, module index, source line
    /// and column (all zero-based).
    type MapToken = (u32, u32, usize, u32, u32);

    /// Runs the rule over modules that share the chunk text `code`. `tokens`
    /// is the chunk's source map, or `None` for a chunk without one.
    fn eval_rule_drafts_with_tokens(
        modules: &[(&str, &str)],
        code: &str,
        tokens: Option<&Vec<MapToken>>,
        engine_reports_eval: bool,
    ) -> Vec<DiagnosticDraft> {
        eval_rule_drafts_with_chunks(modules, &[("main.js", code, tokens)], engine_reports_eval)
    }

    /// An emitted chunk of a test build: file name, text and source map tokens
    /// (`None` for a chunk without a usable source map).
    type TestChunk<'a> = (&'a str, &'a str, Option<&'a Vec<MapToken>>);

    /// Runs the rule over modules that every listed chunk may hold. Every
    /// module lists every chunk, and each map names the modules in order.
    fn eval_rule_drafts_with_chunks(
        modules: &[(&str, &str)],
        chunks: &[TestChunk<'_>],
        engine_reports_eval: bool,
    ) -> Vec<DiagnosticDraft> {
        eval_rule_drafts_with_listing(modules, chunks, None, engine_reports_eval)
    }

    /// Like [`eval_rule_drafts_with_chunks`], but every module lists exactly
    /// the `listed` chunk files instead of every loaded chunk. A listed file
    /// that is not loaded is an unreadable chunk; an empty list is a module
    /// the engine lists in no output chunk.
    fn eval_rule_drafts_with_listing(
        modules: &[(&str, &str)],
        chunks: &[TestChunk<'_>],
        listed: Option<&[&str]>,
        engine_reports_eval: bool,
    ) -> Vec<DiagnosticDraft> {
        eval_rule_drafts_with_mangle(
            modules,
            chunks,
            listed,
            engine_reports_eval,
            MangleSetting::Enabled,
        )
    }

    /// Like [`eval_rule_drafts_with_listing`], with the collection's mangle
    /// setting chosen by the test.
    fn eval_rule_drafts_with_mangle(
        modules: &[(&str, &str)],
        chunks: &[TestChunk<'_>],
        listed: Option<&[&str]>,
        engine_reports_eval: bool,
        mangle: MangleSetting,
    ) -> Vec<DiagnosticDraft> {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        let build_dir = root.join("build");
        std::fs::create_dir_all(&build_dir).unwrap();
        let mut sources = crate::sources::SourceStore::new(true);
        let mut collected = Vec::new();
        let files: Vec<String> = match listed {
            Some(listed) => listed.iter().map(|file| (*file).to_owned()).collect(),
            None => chunks
                .iter()
                .map(|(file, _, _)| (*file).to_owned())
                .collect(),
        };
        for (name, source) in modules {
            let source_path = root.join(name);
            std::fs::create_dir_all(source_path.parent().unwrap()).unwrap();
            std::fs::write(&source_path, source).unwrap();
            // `module_source` reads build-backed text, so model the
            // source-map snapshot used by a collected build instead of
            // relying on an unverified live file.
            sources.snapshots.insert(
                crate::sources::normalize(&source_path),
                (*source).to_owned(),
            );
            collected.push(CollectedModule {
                id: source_path.to_string_lossy().into_owned(),
                path: Some(source_path.to_string_lossy().into_owned()),
                included: true,
                chunks: files.clone(),
                ..CollectedModule::default()
            });
        }
        for (file, code, tokens) in chunks {
            std::fs::write(build_dir.join(file), code).unwrap();
            let Some(tokens) = tokens else {
                continue;
            };
            let mut builder = sourcemap::SourceMapBuilder::new(Some(file));
            let source_ids: Vec<u32> = modules
                .iter()
                .map(|(name, _)| builder.add_source(&root.join(name).to_string_lossy()))
                .collect();
            for (line, column, module, source_line, source_column) in tokens.iter() {
                builder.add_raw(
                    *line,
                    *column,
                    *source_line,
                    *source_column,
                    Some(source_ids[*module]),
                    None,
                    false,
                );
            }
            let mut map = Vec::new();
            builder.into_sourcemap().to_writer(&mut map).unwrap();
            std::fs::write(build_dir.join(format!("{file}.map")), map).unwrap();
        }
        let collection = crate::collect::Collection {
            default_minifier: true,
            minify_enabled: true,
            mangle,
            logs: if engine_reports_eval {
                vec![crate::collect::EngineLog {
                    code: Some("EVAL".to_owned()),
                    id: Some(collected[0].id.clone()),
                    ..crate::collect::EngineLog::default()
                }]
            } else {
                Vec::new()
            },
            modules: collected,
            ..crate::collect::Collection::default()
        };
        let loaded: Vec<crate::chunks::Chunk> = chunks
            .iter()
            .map(|(file, _, _)| {
                crate::chunks::Chunk::load(&build_dir, file, None, true, root).unwrap()
            })
            .collect();
        let mut context = Context::new(
            &collection,
            root.to_path_buf(),
            "app".to_owned(),
            loaded,
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
    fn regex_after_default_and_extends_starts_a_regular_expression() {
        for word in ["default", "extends"] {
            let code = format!("{word} /process.env.FLAG/.test(x)");
            let start = code.find("process.env.FLAG").unwrap();
            assert!(in_ranges(&non_code_ranges(&code), start), "{code}");
        }
        let code = "xreturn /process.env.FLAG/.test(x)";
        let start = code.find("process.env.FLAG").unwrap();
        assert!(!in_ranges(&non_code_ranges(code), start), "{code}");
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
    fn method_declarations_named_eval_are_not_direct_eval() {
        for source in [
            "class C { eval(value) { return value; } }",
            "class C {\n  eval(object) {\n    return object;\n  }\n}",
            "class C { run() {} eval(value) {} }",
            "class C { x = 1; eval(value) {} }",
            "class C { x = 1\n  eval(value) {} }",
            "class C { x = \"a\"\n  eval(value) {} }",
            "class C { @dec()\n  eval(value) {} }",
            "class C extends Base { eval(value) {} }",
            "class C extends mix(A, B) { eval(value) {} }",
            "class C<T> implements I { eval(value) {} }",
            "export default class { eval(value) {} }",
            "const C = class { eval(value) {} };",
            "const C = class extends B { eval(value) {} };",
            "class C { async eval(value) {} }",
            "class C { *eval(value) {} }",
            "class C { async *eval(value) {} }",
            "class C { static eval(value) {} }",
            "class C { static async eval(value) {} }",
            "class C { get eval() { return 1; } }",
            "class C { set eval(value) {} }",
            "class C { public eval(value: string): number { return 1; } }",
            "class C { private static async eval(value: string): Promise<void> {} }",
            "class C { eval(value: string): { a: number } { return { a: 1 }; } }",
            "class C { eval/* c */(value) {} }",
            "class C { eval // c\n(value) {} }",
            "}class C { run() {}eval(object) {} }",
            "const o = { eval(value) { return value; } };",
            "const o = { a: 1, eval(value) {} };",
            "const o = { async eval(value) {} };",
            "const o = { *eval(value) {} };",
            "const o = { async *eval(value) {} };",
            "const o = { get eval() { return 1; } };",
            "const o = { set eval(value) {} };",
            "const o = { eval(value): number { return 1; } };",
            "module.exports = { eval(value) {} };",
            "export default { eval(value) {} };",
            "run({ eval(value) {} });",
            "run(a, { eval(value) {} });",
            "const list = [{ eval(value) {} }];",
            "const o = { nested: { eval(value) {} } };",
            "const o = c ? { eval(value) {} } : null;",
            "function f() { return { eval(value) {} }; }",
            "const o = { run() {}, eval(value) {} };",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
        }
    }

    /// The offset-based expectation of a source whose only direct eval is the
    /// first `eval(` that is followed by `marker`.
    fn assert_reports_at(source: &str, marker: &str) {
        let offset = source.find(marker).expect(source);
        let (line, column) = line_and_column(source, offset);
        assert_eq!(
            direct_eval_line(source),
            Some((line as u32, column as u32)),
            "{source:?}"
        );
    }

    #[test]
    fn object_literals_after_every_expression_operator_hold_methods() {
        let method = "{ eval(input) {} }";
        let mut sources: Vec<String> = Vec::new();
        // Keyword operators.
        for keyword in [
            "typeof", "void", "delete", "new", "return", "throw", "default", "yield", "await",
        ] {
            sources.push(format!("function* f() {{ {keyword} {method}; }}"));
        }
        sources.push(format!("async function f() {{ await {method}; }}"));
        sources.push(format!("const v = x in {method};"));
        sources.push(format!("const v = x instanceof {method};"));
        sources.push(format!("for (const k of {method}) {{}}"));
        sources.push(format!("switch (x) {{ case {method}: break; }}"));
        sources.push(format!("export default {method};"));
        // Unary, binary, logical, nullish and relational operators.
        for operator in [
            "!", "~", "+", "-", "left +", "left -", "left *", "left /", "left %", "left **",
            "left &", "left |", "left ^", "left <<", "left >>", "left >>>", "left <", "left >",
            "left <=", "left >=", "left ==", "left ===", "left !=", "left !==", "left &&",
            "left ||", "left ??",
        ] {
            sources.push(format!("const v = {operator} {method};"));
        }
        // Assignment operators.
        for operator in [
            "=", "+=", "-=", "*=", "/=", "%=", "**=", "&=", "|=", "^=", "<<=", ">>=", ">>>=",
            "&&=", "||=", "??=",
        ] {
            sources.push(format!("target {operator} {method};"));
        }
        // Spread, comma, conditional, call and index contexts.
        for context in [
            "[...{m}]",
            "run(...{m})",
            "({ ...{m} })",
            "(left, {m})",
            "left, {m}",
            "c ? {m} : null",
            "c ? null : {m}",
            "a ? b ? c : d : {m}",
            "a ? b : c ? d : {m}",
            "a ? (b ? c : d) : {m}",
            "({ k: c ? 1 : {m} })",
            "({ k: c ? {m} : 1 })",
            "({ k: {m} })",
            "run(a, {m})",
            "list[{m}]",
            "[{m}]",
            "(({m}))",
        ] {
            sources.push(format!("const v = {};", context.replace("{m}", method)));
        }
        // Arrow bodies and template substitutions.
        for context in [
            "() => ({m})",
            "async () => ({m})",
            "x => y => ({m})",
            "(x) => (({m}))",
            "`${{m}}`",
            "`a${x}b${{m}}c`",
            "`${`${{m}}`}`",
            "`${ /* c */ {m}}`",
        ] {
            sources.push(format!("const v = {};", context.replace("{m}", method)));
        }
        for source in &sources {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
            // A real call next to the literal keeps its own location.
            let with_call = format!("{source}\neval(code);");
            assert_reports_at(&with_call, "eval(code)");
        }
    }

    #[test]
    fn braces_that_are_not_object_literals_keep_their_eval_reported() {
        for source in [
            "if (x) { eval(code) {} }",
            "label: { eval(code) {} }",
            "switch (x) { case 1: { eval(code) {} } }",
            "const f = () => { eval(code) {} };",
            "function run() { eval(code) {} }",
            "a++\n{ eval(code) {} }",
            "x = a ? b : c\nlabel: { eval(code) {} }",
            "interface I<T> { eval(code) {} }",
            "const v = f<A>\n{ eval(code) {} }",
            "try { eval(code) {} } finally { }",
            "do { eval(code) {} } while (x);",
            "{ eval(code) {} }",
            "x;\n{ eval(code) {} }",
        ] {
            assert_reports_at(source, "eval(code)");
        }
    }

    #[test]
    fn signatures_without_a_body_stop_at_their_own_member() {
        for (source, marker) in [
            // A `;` ends the signature; the next member's body is not its own.
            (
                "abstract class C { abstract eval(v): void; run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { run() {} abstract eval(v): void; }",
                "eval(v)",
            ),
            (
                "abstract class C { x = 1; abstract eval(v): void; y = 2; run() {} }",
                "eval(v)",
            ),
            ("abstract class C { abstract eval(v); run() {} }", "eval(v)"),
            (
                "abstract class C { abstract eval(v: { a: number; b: string }): void; run() {} }",
                "eval(v",
            ),
            (
                "abstract class C { abstract eval(v): { a: number; b: string }; run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): (a: number; b) => void; run() {} }",
                "eval(v)",
            ),
            // Overload signatures.
            (
                "class C { eval(v: string): void; eval(v: number): void; run() {} }",
                "eval(v: string)",
            ),
            (
                "class C { eval(v: string): void; eval(v) {} }",
                "eval(v: string)",
            ),
            (
                "class C { run() {} eval(v: string): void; eval(v) {} }",
                "eval(v: string)",
            ),
            (
                "class C { static eval(v: string): void; static eval(v) {} }",
                "eval(v: string)",
            ),
            (
                "class C { public eval(v: string): void; run() {} }",
                "eval(v: string)",
            ),
            // `declare` classes.
            ("declare class C { eval(v): void; run() {} }", "eval(v)"),
            (
                "declare class C { run() {} eval(v): void }\nfunction later() {}",
                "eval(v)",
            ),
            (
                "declare class C { eval(v): void }\nfunction later() {}",
                "eval(v)",
            ),
            (
                "export declare class C { eval(v): void; }\nclass D { run() {} }",
                "eval(v)",
            ),
            // Interface-like and type-literal members.
            ("interface I { eval(v): void; run(): void }", "eval(v)"),
            (
                "interface I { eval(v): void }\nfunction later() {}",
                "eval(v)",
            ),
            (
                "type T = { eval(v): void; run(): void };\nfunction later() {}",
                "eval(v)",
            ),
            (
                "declare const o: { eval(v): void };\nconst later = { run() {} };",
                "eval(v)",
            ),
            // Index signatures as neighbors.
            (
                "class C { [key: string]: any; abstract eval(v): void; run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): void; [key: string]: any; run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { [key: string]: any; abstract eval(v): void; [other: number]: \
                 any; run() {} }",
                "eval(v)",
            ),
            // No semicolon: the line break ends a complete signature.
            (
                "declare class C { eval(v: string): void\n run() {} }",
                "eval(v: string)",
            ),
            (
                "abstract class C { abstract eval(v)\n run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): { a: number }\n run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): Promise<void>\n run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): string[]\n run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): void\n [key]() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): void\n static run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): void\n async run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): void\n get x() { return 1; } }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): void\n @dec() run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): void\n *gen() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): void\n #priv() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): void // c\n run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): void\u{2028} run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): void\r\n run() {} }",
                "eval(v)",
            ),
            // A trailing signature is bounded by the closing brace.
            (
                "class C { eval(v): void }\nconst o = { run() {} };",
                "eval(v)",
            ),
        ] {
            assert_reports_at(source, marker);
        }
    }

    #[test]
    fn multi_line_signatures_with_a_body_stay_method_declarations() {
        for source in [
            "class C { eval(v): void { } }",
            "class C { eval(v): void\n{ } }",
            "class C { eval(v)\n{ } }",
            "class C { eval(v):\n  void { } }",
            "class C { eval(v): A\n  | B { } }",
            "class C { eval(v): A |\n  B { } }",
            "class C { eval(v): A &\n  B { } }",
            "class C { eval(v): A\n  & B { } }",
            "class C { eval(v): (\n  a: number\n) => void { } }",
            "class C { eval(v): (a: number,\n b: string) => void { } }",
            "class C { eval(v): Foo<\n  A,\n  B\n> { } }",
            "class C { eval(v): Promise<\n  { a: number }\n> { } }",
            "class C { eval(v): { a: number;\n b: string } { } }",
            "class C { eval(v): { a: number; b: string } { } }",
            "class C { eval(v): A extends B\n ? C : D { } }",
            "class C { eval(v): A[\n] { } }",
            "class C { eval(v) // c\n  { } }",
            "class C { eval(v) /* c */ : void { } }",
            "abstract class C { abstract run(): void; eval(v) { } }",
            "abstract class C { abstract run(): void\n eval(v) { } }",
            "abstract class C { run: () => void\n eval(v) { } }",
            "abstract class C { run: A | void\n eval(v) { } }",
            "abstract class C { run: A & void\n eval(v) { } }",
            "abstract class C { run(): void // c\n eval(v) { } }",
            "class C { eval(v) { } run(): void; }",
            "const o = { eval(v): void { } };",
            "const o = { eval(v): void\n { } };",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
        }
    }

    #[test]
    fn eval_methods_behind_type_arguments_are_declarations() {
        for source in [
            "class C<T extends {a:1}> { eval(value) {} }",
            "class C<T extends { a: 1 }> { eval(value) {} }",
            "class C<T = {a:1}> { eval(value) {} }",
            "class C<T extends Array<{ a: 1 }>> { eval(value) {} }",
            "class C<T extends Record<string, { a: Array<number> }>> { eval(value) {} }",
            "class C<T extends (x: number) => void> { eval(value) {} }",
            "class C<T extends () => { a: 1 }> { eval(value) {} }",
            "class C<K, V = Map<K, { a: 1 }>> { eval(value) {} }",
            "class C<const T extends { a: 1 }> { eval(value) {} }",
            "class C extends B<{ a: 1 }> { eval(value) {} }",
            "class C extends B<{ a: 1 }> implements I<{ b: 2 }> { eval(value) {} }",
            "class C<T extends { a: 1 }> extends B<T> implements I<T> { eval(value) {} }",
            "class C<T extends\n  { a: 1 }\n> {\n  eval(value) {}\n}",
            "export default class<T extends { a: 1 }> { eval(value) {} }",
            "const C = class<T extends { a: 1 }> { eval(value) {} };",
            "const C = class D<T = { a: 1 }> extends B { eval(value) {} };",
            "export abstract class C<T extends { a: 1 }> { abstract run(value): void; eval(value) \
             {} }",
            "class C<T extends { a: 1 }> { run() {} eval(value) {} }",
            "class C<T extends { a: 1 }> { x: Array<number> = []; eval(value) {} }",
            "class C { eval(value: Array<{ a: 1 }>): Promise<{ b: 2 }> {} }",
            // A comparison before the class does not hide its header.
            "a > b; class C<T extends { a: 1 }> { eval(value) {} }",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
        }
    }

    /// Method declarations: the whole source holds no direct eval.
    fn assert_all_declarations(sources: &[&str]) {
        for source in sources {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
            // A real call next to the declaration keeps its own location.
            let with_call = format!("{source}\neval(fresh);");
            assert_reports_at(&with_call, "eval(fresh)");
        }
    }

    /// Findings: the first `marker` is reported, because the scan cannot prove
    /// a method declaration there.
    fn assert_all_reported(cases: &[(&str, &str)]) {
        for (source, marker) in cases {
            assert_reports_at(source, marker);
        }
    }

    #[test]
    fn generic_eval_methods_skip_the_type_parameter_list() {
        assert_all_declarations(&[
            "class C { eval<T>(value: T) {} }",
            "const o = { eval<T>(value: T) {} };",
            "class C { eval <T>(value: T) {} }",
            "class C { eval/* c */<T>/* d */(value: T) {} }",
            "class C { eval<T>\n(value: T) {} }",
            "class C { eval<T, U>(value: T, other: U) {} }",
            "class C { eval<T = string>(value: T) {} }",
            "class C { eval<T = Array<number>>(value: T) {} }",
            "class C { eval<T extends Array<Array<number>>>(value: T) {} }",
            "class C { eval<T extends { a: 1 }>(value: T) {} }",
            "class C { eval<T extends { a: Array<1> }, U>(value: T) {} }",
            "class C { eval<T extends (x: number) => void>(value: T) {} }",
            "class C { eval<T extends () => { a: 1 }>(value: T) {} }",
            "class C { eval<T extends [number, string]>(value: T) {} }",
            "class C { eval<const T>(value: T) {} }",
            "class C { public async eval<T>(value: T): Promise<T> {} }",
            "class C { static *eval<T>(value: T) {} }",
            "class C { eval<T>(value: T): T extends U ? { a: 1 } : V {} }",
            "class C { eval<T>(value: T)\n{} }",
            "const o = { async eval<T>(value: T) {} };",
            "const o = { a: 1, eval<T>(value: T): T {} };",
            "class C { @dec() eval<T>(value: T) {} }",
            "class C { x = 1\n eval<T>(value: T) {} }",
        ]);
        assert_all_reported(&[
            // A generic signature without a body is not a declaration.
            (
                "abstract class C { abstract eval<T>(v: T): void; run() {} }",
                "eval<T>",
            ),
            (
                "declare class C { eval<T>(v: T): T }\nfunction later() {}",
                "eval<T>",
            ),
            // A generic call, or a comparison chain read as one.
            ("eval<string>(code);", "eval<string>"),
            ("const v = x ? eval<T>(code) : null;", "eval<T>"),
            ("function run() { eval<T>(code) {} }", "eval<T>"),
            ("if (x) { eval<T>(code) {} }", "eval<T>"),
        ]);
        // An unbalanced list is not a call at all.
        for source in [
            "eval < max && (x)",
            "if (eval < a; b > (c)) {}",
            "eval<T(x)",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
        }
    }

    #[test]
    fn heritage_class_expressions_do_not_hide_the_outer_class_body() {
        assert_all_declarations(&[
            "class C extends class {} { eval(value) {} }",
            "class C extends class { run() {} } { eval(value) {} }",
            "class C extends class extends class {} {} { eval(value) {} }",
            "class C extends class B { x = { a: 1 } } { eval(value) {} }",
            "const D = class C extends class {} { eval(value) {} };",
            "class C extends class<T extends { a: 1 }> {} { eval(value) {} }",
            "class C extends class { eval(v) {} } { eval(value) {} }",
        ]);
        // A class expression that is not the heritage ends the head, so the
        // block after it keeps its finding.
        assert_all_reported(&[
            ("const D = class {}\n{ eval(code) {} }", "eval(code)"),
            ("class C extends B {}\n{ eval(code) {} }", "eval(code)"),
            (
                "const D = class extends B {};\nif (x) { eval(code) {} }",
                "eval(code)",
            ),
        ]);
    }

    #[test]
    fn conditional_return_types_continue_after_type_operators() {
        assert_all_declarations(&[
            "class C { eval(): T extends\n string ? T : never\n {} }",
            "class C { eval(): T extends\n  string\n  ? T\n  : never {} }",
            "class C { eval(): keyof\n T {} }",
            "class C { eval(): typeof\n value {} }",
            "class C { eval(): U extends (infer\n V) ? V : never {} }",
            "class C { eval(): readonly\n string[] {} }",
            "class C { eval(v): v is\n string {} }",
        ]);
        // A signature without a body still ends at an ordinary line break.
        assert_all_reported(&[(
            "declare class C { eval(v): string\n run() {} }\nfunction f() {}",
            "eval(v)",
        )]);
    }

    #[test]
    fn postfix_updates_complete_a_class_field() {
        assert_all_declarations(&[
            "class C { x = value++\n eval(input) {} }",
            "class C { x = value--\n eval(input) {} }",
            "class C { x = a.b++\n eval(input) {} }",
            "class C { x = value++ // c\n eval(input) {} }",
            "class C { x = value++\n static eval(input) {} }",
        ]);
        // On the same line the update is still an expression.
        assert_all_reported(&[("class C { x = value++ eval(code) {} }", "eval(code)")]);
        // An object literal member after a postfix update is not a member.
        assert_all_reported(&[("const o = { x: value++\n eval(code) {} };", "eval(code)")]);
    }

    #[test]
    fn jsx_attribute_containers_hold_object_literals() {
        assert_all_declarations(&[
            "const a = <Widget value={{ eval(input) {} }} />;",
            "const a = <Widget value={ { eval(input) {} } } />;",
            "const a = <Widget data-id={{ a: 1, eval(input) {} }} />;",
            "const a = <Widget value={/* c */{ eval(input) {} }} />;",
            "const a = <Widget first={1} value={{ async eval(input) {} }} />;",
        ]);
        // A block inside a block or an arrow body is not an object literal.
        assert_all_reported(&[
            ("function f() { { eval(code) {} } }", "eval(code)"),
            ("const f = () => {{ eval(code) {} }};", "eval(code)"),
            ("if (x) {{ eval(code) {} }}", "eval(code)"),
        ]);
    }

    #[test]
    fn spaced_angle_brackets_after_eval_are_comparisons() {
        for source in [
            "const result = eval < value > (code);",
            "const result = eval <value> (code);",
            "const result = eval\n< value >\n(code);",
            "const result = eval /* c */ < value > (code);",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
            let with_call = format!("{source}\neval(fresh);");
            assert_reports_at(&with_call, "eval(fresh)");
        }
        // An attached list keeps the documented generic-call reading.
        assert_all_reported(&[("const r = eval<string>(code);", "eval<string>")]);
    }

    #[test]
    fn comparison_chains_are_not_type_argument_lists() {
        let method = "{ eval(input) {} }";
        let mut sources: Vec<String> = Vec::new();
        for prefix in [
            "const v = left < right >",
            "const v = left <right >",
            "const v = a < b > c >",
            "const v = a < b < c >",
            "const v = (a < b) >",
            "const v = a >= b >",
            "const v = a < b ? c > d : e >",
            "x = y < z >",
            "if (a < b >",
            "run(a < b >",
            "const v = [a < b >",
        ] {
            let closing = if prefix.starts_with("if (") || prefix.starts_with("run(") {
                format!("{prefix} {method}) {{}}")
            } else if prefix.starts_with("const v = [") {
                format!("{prefix} {method}];")
            } else {
                format!("{prefix} {method};")
            };
            sources.push(closing);
        }
        let sources: Vec<&str> = sources.iter().map(String::as_str).collect();
        assert_all_declarations(&sources);
        // A `<` attached to a name opens a type-argument list: the brace after
        // its `>` is a body or block, so the finding stays.
        assert_all_reported(&[
            ("const v = f<A>\n{ eval(code) {} }", "eval(code)"),
            ("const v = a.b<A>\n{ eval(code) {} }", "eval(code)"),
            ("const v = f<A, B>\n{ eval(code) {} }", "eval(code)"),
            ("const v = f<Array<A>>\n{ eval(code) {} }", "eval(code)"),
            ("const v = f<(a) => void>\n{ eval(code) {} }", "eval(code)"),
            ("const v = f<{ a: 1 }>\n{ eval(code) {} }", "eval(code)"),
            ("function f<T>() { eval(code) {} }", "eval(code)"),
            ("interface I<T> { eval(code) {} }", "eval(code)"),
            // The documented limit: a comparison whose `<` touches the left
            // operand reads as a type-argument list.
            ("const v = a<b>{ eval(code) {} };", "eval(code)"),
            ("const v = left< right > { eval(code) {} };", "eval(code)"),
        ]);
    }

    #[test]
    fn return_type_braces_never_end_the_annotation() {
        assert_all_declarations(&[
            "class C { eval(v): T extends U ? { a: 1 } : V { return 1; } }",
            "class C { eval(v): T extends U ? V : { a: 1 } {} }",
            "class C { eval(v): T extends U ? { a: 1 } : { b: 2 } {} }",
            "class C { eval(v): T extends { a: 1 } ? X : Y {} }",
            "class C { eval(v): T extends U ? A extends B ? { a: 1 } : C : D {} }",
            "class C { eval(v): T extends U\n ? { a: 1 }\n : V {} }",
            "class C { eval(v): v is { a: 1 } {} }",
            "class C { eval(v): Array<{ a: 1 }> {} }",
            "class C { eval(v): [{ a: 1 }, { b: 2 }] {} }",
            "class C { eval(v): { a: 1 } | { b: 2 } {} }",
            "class C { eval(v): { a: 1 } & { b: 2 } {} }",
            "class C { eval(v): (a: { x: 1 }) => { y: 2 } {} }",
            "class C { eval(v): () => void {} }",
            "class C { eval(v): Promise<void> {} }",
            "class C { eval(v): A[] {} }",
            "const o = { eval(v): A extends B ? { a: 1 } : { b: 2 } {} };",
        ]);
        assert_all_reported(&[
            (
                "abstract class C { abstract eval(v): T extends U ? { a: 1 } : V; run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): T extends U ? V : { a: 1 }; run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): T extends U ? { a: 1 } : { b: 2 }; run() {} \
                 }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): T extends { a: 1 } ? X : Y; run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): v is { a: 1 }; run() {} }",
                "eval(v)",
            ),
            (
                "abstract class C { abstract eval(v): T extends U ? { a: 1 } : V\n run() {} }",
                "eval(v)",
            ),
            (
                "declare class C { eval(v): T extends U ? { a: 1 } : V }\nfunction later() {}",
                "eval(v)",
            ),
            (
                "interface I { eval(v): T extends U ? { a: 1 } : V; run(): void }",
                "eval(v)",
            ),
            (
                "class C { eval(v): T extends U ? { a: 1 } : V; eval(v) {} }",
                "eval(v): T",
            ),
        ]);
    }

    #[test]
    fn private_and_member_fields_complete_a_class_member() {
        assert_all_declarations(&[
            "class C { #state\n eval(value) {} }",
            "class C { #state\n  eval(value) {}\n}",
            "class C { #state = 1\n eval(value) {} }",
            "class C { #state = 'a'\n eval(value) {} }",
            "class C { #state;\n eval(value) {} }",
            "class C { #state; eval(value) {} }",
            "class C { #state;eval(value) {} }",
            "class C { static #state\n eval(value) {} }",
            "class C { #state: number\n eval(value) {} }",
            "class C { #state: A | B\n eval(value) {} }",
            "class C { #state = this.#other\n eval(value) {} }",
            "class C { x = a.b\n eval(value) {} }",
            "class C { x = a?.b\n eval(value) {} }",
            "class C { #state // c\n eval(value) {} }",
            "class C { #state /* c */\n eval(value) {} }",
            "class C { #state\n static eval(value) {} }",
            "class C { #state\n async eval(value) {} }",
            "class C { #state\n get eval() { return 1; } }",
            "class C { #state\n @dec() eval(value) {} }",
            "class C { #a\n #b\n eval(value) {} }",
            "class C { #state\u{2028} eval(value) {} }",
        ]);
        assert_all_reported(&[
            // No line break: the next token continues the field expression.
            ("class C { x = a.b eval(value) {} }", "eval(value)"),
            ("class C { #state eval(value) {} }", "eval(value)"),
            // The documented limit: an optional or definite marker alone on
            // the end of a line is not read as a complete field.
            ("class C { x?\n eval(value) {} }", "eval(value)"),
            ("class C { x!\n eval(value) {} }", "eval(value)"),
            // A keyword that cannot end a field leaves the member open.
            ("class C { x = typeof\n eval(value) {} }", "eval(value)"),
            ("class C { x = a.b +\n eval(value) {} }", "eval(value)"),
            // An object literal needs a comma, never a line break.
            ("const o = { a: b\n eval(value) {} };", "eval(value)"),
            ("const o = { a: b.c\n eval(value) {} };", "eval(value)"),
        ]);
    }

    #[test]
    fn restricted_keywords_end_the_statement_before_an_object_literal() {
        assert_all_reported(&[
            ("function f() { return\n{ eval(code)\n{} } }", "eval(code)"),
            ("function f() { return\n{ eval(code) {} } }", "eval(code)"),
            ("function f() { return\r\n{ eval(code) {} } }", "eval(code)"),
            (
                "function f() { return\u{2028}{ eval(code) {} } }",
                "eval(code)",
            ),
            (
                "function f() { return // c\n{ eval(code) {} } }",
                "eval(code)",
            ),
            (
                "function f() { return /*\n*/ { eval(code) {} } }",
                "eval(code)",
            ),
            (
                "function f() { return /* c */\n/* d */ { eval(code) {} } }",
                "eval(code)",
            ),
            ("function f() { throw\n{ eval(code) {} } }", "eval(code)"),
            ("function* f() { yield\n{ eval(code) {} } }", "eval(code)"),
            ("while (x) { break\n{ eval(code) {} } }", "eval(code)"),
            ("for (;;) { continue\n{ eval(code) {} } }", "eval(code)"),
            (
                "function f() { if (x) return\n{ eval(code) {} } }",
                "eval(code)",
            ),
            // A member named like a keyword is a plain name that ends the
            // expression, so a brace after it opens a block.
            ("const v = a.return { eval(code) {} };", "eval(code)"),
            ("const v = a.return\n{ eval(code) {} };", "eval(code)"),
        ]);
        assert_all_declarations(&[
            "function f() { return { eval(code) {} }; }",
            "function f() { return {\n eval(code) {} }; }",
            "function f() { return /* c */ { eval(code) {} }; }",
            "function f() { return /* c */{ eval(code) {} }; }",
            "function f() { throw { eval(code) {} }; }",
            "function* f() { yield { eval(code) {} }; }",
            "function* f() { yield\n; return { eval(code) {} }; }",
            // The unrestricted keywords continue across a line break.
            "const v = typeof\n{ eval(code) {} };",
            "const v = void\n{ eval(code) {} };",
            "const v = new\n{ eval(code) {} };",
            "const v = x in\n{ eval(code) {} };",
            "async function f() { await\n{ eval(code) {} }; }",
            "export default\n{ eval(code) {} };",
            "switch (x) { case\n{ eval(code) {} }: break; }",
            // Operators continue across a line break.
            "const v =\n{ eval(code) {} };",
            "const v = a +\n{ eval(code) {} };",
            "const v = c ?\n{ eval(code) {} } : null;",
            "run(\n{ eval(code) {} });",
        ]);
    }

    #[test]
    fn eval_methods_with_non_ascii_names_do_not_panic_or_report() {
        for source in [
            "class Caf\u{e9} { eval(value) {} }",
            "class \u{c9}<T extends { \u{e9}: 1 }> { eval(value) {} }",
            "class \u{4e2d}\u{6587} extends \u{540d}\u{524d} { eval(value) {} }",
            "class C extends Caf\u{e9}<{ a: 1 }> { eval(value) {} }",
            "class C { caf\u{e9} = 1\n  eval(value) {} }",
            "class C { caf\u{e9}() {} eval(value) {} }",
            "class C { \u{1f600} = 1; eval(value) {} }",
            "class C { x = \"\u{e9}\"\n  eval(value) {} }",
            "class C { /* \u{e9} */ eval(value) {} }",
            "// \u{e9}\nclass C { eval(value) {} }",
            "class C { @d\u{e9}c() eval(value) {} }",
            "class C { @d\u{e9}c eval(value) {} }",
            "const o = { caf\u{e9}: 1, eval(value) {} };",
            "const o = { \"\u{e9}\": 1, eval(value) {} };",
            "const o = { \u{e9}val() {}, eval(value) {} };",
            "const o = { nested: { caf\u{e9}: 1, eval(value) {} } };",
            "const caf\u{e9} = { eval(value) {} };",
            "const C = class Caf\u{e9} { eval(value) {} };",
            "class C {\u{2028}  eval(value) {} }",
            "class C { x = 1\u{2028}  eval(value) {} }",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
        }
        // Real calls next to non-ASCII names keep their own location.
        for (source, call) in [
            ("class Caf\u{e9} { run() { eval(code); } }", "eval(code)"),
            ("class Caf\u{e9} {} eval(code);", "eval(code)"),
            ("const caf\u{e9} = { a: eval(code) };", "eval(code)"),
            ("const o = { caf\u{e9}: eval(code) };", "eval(code)"),
            ("class C { caf\u{e9} = eval(code); }", "eval(code)"),
            (
                "class C<T extends { \u{e9}: 1 }> { run() { eval(code) } }",
                "eval(code)",
            ),
        ] {
            let offset = source.find(call).expect(source);
            let (line, column) = line_and_column(source, offset);
            assert_eq!(
                direct_eval_line(source),
                Some((line as u32, column as u32)),
                "{source:?}"
            );
        }
    }

    #[test]
    fn scans_never_panic_on_any_char_boundary_cut() {
        for source in [
            "class Caf\u{e9}<T extends { \u{e9}: 1 }> extends \u{c9}<{ a: 1 }> { \
             @d\u{e9}c(\"\u{e9}\") eval(value) {} }",
            "const o = { caf\u{e9}: `\u{e9}${eval(x)}`, eval(v) {}, \u{1f600}: /\u{e9}/ };",
            "class C { x = 1\u{2028}  eval(value) {}\u{2029}run() { eval(code) } }",
        ] {
            for end in source.char_indices().map(|(index, _)| index) {
                let _ = direct_eval_line(&source[..end]);
                let _ = direct_eval_line(&source[end..]);
            }
            let ignored = non_code_ranges(source);
            assert_eq!(
                masked_code(source, &ignored).len(),
                source.len(),
                "{source:?}"
            );
        }
    }

    #[test]
    fn decorated_eval_methods_are_declarations() {
        for source in [
            "class C { @dec() eval(value) {} }",
            "class C { @dec eval(value) {} }",
            "class C { @dec\n  eval(value) {} }",
            "class C { @dec()\n  eval(value) {} }",
            "class C { @a.b.c eval(value) {} }",
            "class C { @a.b.c(1, { x: 2 }) eval(value) {} }",
            "class C { @a.b(\")\") eval(value) {} }",
            "class C { @(factory()) eval(value) {} }",
            "class C { @dec() @other(1) eval(value) {} }",
            "class C { @dec() @other eval(value) {} }",
            "class C { @dec() static eval(value) {} }",
            "class C { @dec() async eval(value) {} }",
            "class C { @dec() static async *eval(value) {} }",
            "class C { @dec() get eval() { return 1; } }",
            "class C { @dec() set eval(value) {} }",
            "class C { @dec() public override eval(value): void {} }",
            "class C { @get eval(value) {} }",
            "class C { @static eval(value) {} }",
            "class C { @dec(/* c */) eval(value) {} }",
            "class C { @dec() /* c */ eval(value) {} }",
            "class C { run() {} @dec() eval(value) {} }",
            "class C { x = 1; @dec() eval(value) {} }",
            "class C { x = 1\n  @dec() eval(value) {} }",
            "@dec() class C { @dec() eval(value) {} }",
            "export default @dec() class { @dec() eval(value) {} }",
            "const C = class { @dec() eval(value) {} };",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
        }
        // A decorator that calls eval is still a real call.
        for (source, call) in [
            ("class C { @eval(code) run() {} }", "eval(code)"),
            ("class C { @dec(eval(code)) run() {} }", "eval(code)"),
            (
                "class C { @dec() eval(value) {} run() { eval(code) } }",
                "eval(code)",
            ),
            ("class C { @dec(eval(code)) eval(value) {} }", "eval(code)"),
            ("class C { x = f() eval(code) }", "eval(code)"),
            ("class C { x = f(a) ; y = eval(code) }", "eval(code)"),
        ] {
            let offset = source.find(call).expect(source);
            let (line, column) = line_and_column(source, offset);
            assert_eq!(
                direct_eval_line(source),
                Some((line as u32, column as u32)),
                "{source:?}"
            );
        }
    }

    #[test]
    fn computed_keys_and_accessors_around_eval_methods() {
        for source in [
            "class C { [key]() {} eval(value) {} }",
            "class C { [key] = 1; eval(value) {} }",
            "class C { [key] = 1\n  eval(value) {} }",
            "class C { static [key]() {} eval(value) {} }",
            "class C { get [key]() { return 1; } eval(value) {} }",
            "class C { get eval() { return 1; } set eval(value) {} }",
            "class C { static get eval() { return 1; } }",
            "class C { static set eval(value) {} }",
            "class C { async *[Symbol.iterator]() {} eval(value) {} }",
            "const o = { [key]: 1, eval(value) {} };",
            "const o = { [key]() {}, eval(value) {} };",
            "const o = { get [key]() { return 1; }, eval(value) {} };",
            "const o = { get eval() { return 1; }, set eval(value) {} };",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
        }
        // Calls inside a computed key or an accessor body are real calls.
        for (source, call) in [
            ("class C { [eval(code)]() {} }", "eval(code)"),
            ("class C { static [eval(code)] = 1; }", "eval(code)"),
            ("class C { get [eval(code)]() { return 1; } }", "eval(code)"),
            ("class C { get a() { return eval(code); } }", "eval(code)"),
            ("class C { set a(v) { eval(code); } }", "eval(code)"),
            ("const o = { [eval(code)]() {} };", "eval(code)"),
            (
                "const o = { get a() { return eval(code); } };",
                "eval(code)",
            ),
            (
                "const o = { [eval(code)]: 1, eval(value) {} };",
                "eval(code)",
            ),
            (
                "a > b; class C<T extends { a: 1 }> { run() { eval(code) } }",
                "eval(code)",
            ),
            (
                "const x = a > b ? { eval(value) {} } : 0; eval(code);",
                "eval(code)",
            ),
            (
                "class A {}\nconst r = a > b ? eval(code) : 0;",
                "eval(code)",
            ),
        ] {
            let offset = source.find(call).expect(source);
            let (line, column) = line_and_column(source, offset);
            assert_eq!(
                direct_eval_line(source),
                Some((line as u32, column as u32)),
                "{source:?}"
            );
        }
    }

    #[test]
    fn calls_next_to_method_declarations_are_still_direct_eval() {
        // The reported position is the call, not the declaration.
        for (source, call) in [
            ("class C { eval(value) {} }\neval(code);", "eval(code)"),
            (
                "class C { eval(value) { return eval(value); } }",
                "eval(value);",
            ),
            (
                "class C {\n  eval(value) {}\n  run() { eval(code); }\n}",
                "eval(code)",
            ),
            (
                "const o = { eval(value) {} };\nconst x = eval(code);",
                "eval(code)",
            ),
            ("const o = { a: eval(code), eval(value) {} };", "eval(code)"),
            (
                "const o = { eval(value) { return eval(code); } };",
                "eval(code)",
            ),
            ("const o = { a: eval(code) };", "eval(code)"),
            ("const o = { a: c ? eval(code) : { b: 1 } };", "eval(code)"),
            ("class C { x = eval(code); }", "eval(code)"),
            ("class C { x = c ? eval(code) : { b: 1 }; }", "eval(code)"),
            ("class C { [eval(code)]() {} }", "eval(code)"),
            ("class C { @eval(code) run() {} }", "eval(code)"),
            ("class C { static { eval(code); } }", "eval(code)"),
            ("class C { run() { eval(code) } }", "eval(code)"),
            ("run(a, eval(code)) ;", "eval(code)"),
            ("run(eval(code)) ;", "eval(code)"),
            ("x = a ? eval(code) : b;", "eval(code)"),
            ("if (x) { eval(code) }", "eval(code)"),
            ("function run() { eval(code) }", "eval(code)"),
            ("const f = () => { eval(code) };", "eval(code)"),
            ("{ eval(code) }", "eval(code)"),
            ("a;\neval(code)\n{ b }", "eval(code)"),
            ("const o = { a, b }; eval(code);", "eval(code)"),
        ] {
            let offset = source.find(call).expect(source);
            let expected = line_and_column(source, offset);
            let expected = (expected.0 as u32, expected.1 as u32);
            assert_eq!(direct_eval_line(source), Some(expected), "{source:?}");
        }
        // Member calls stay member calls.
        for source in [
            "class C { run() { return this.eval(value); } }",
            "instance.eval(value);",
            "const o = { eval(value) {} }; o.eval(1);",
            "class C { eval(value) {} } new C().eval(1);",
            "class C { eval(value) { return this.#eval(value); } }",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
        }
    }

    #[test]
    fn eval_methods_in_one_module_do_not_hide_a_real_call_in_another() {
        let prefix = "const longLocalName = 1;\n";
        let method_module = format!(
            "{prefix}export class Prefilter {{\n  eval(input, pos) {{ return input; }}\n}}\n"
        );
        let call_module =
            format!("{prefix}export const run = (code) => {{\n  return eval(code);\n}};\n");
        // Only the method: no finding.
        let drafts = eval_rule_drafts_for_modules(&[("src/method.js", &method_module)], false);
        assert!(drafts.is_empty());
        // The method dependency and the real call dependency: one finding on
        // the real call.
        let drafts = eval_rule_drafts_for_modules(
            &[
                ("src/method.js", &method_module),
                ("src/call.js", &call_module),
            ],
            false,
        );
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].rule, RuleId::DirectEvalBlocksMangling);
        assert_eq!(drafts[0].status, EvidenceStatus::Inferred);
        let location = drafts[0].located.location.as_ref().unwrap();
        assert!(location.path.ends_with("src/call.js"));
        assert_eq!(location.line, 3);
        // Reversing the module order does not change the result.
        let drafts = eval_rule_drafts_for_modules(
            &[
                ("src/call.js", &call_module),
                ("src/method.js", &method_module),
            ],
            false,
        );
        assert_eq!(drafts.len(), 1);
        // A method and a real call in the same module report the call.
        let both = format!(
            "{prefix}class Prefilter {{\n  eval(input, pos) {{ return input; }}\n}}\neval(code);\n"
        );
        let drafts = eval_rule_drafts(&both, false);
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].located.location.as_ref().unwrap().line, 5);
        // An engine report alone is not enough: both engines report eval before
        // tree shaking, and the method-only chunk holds no call.
        assert!(eval_rule_drafts(&method_module, true).is_empty());
    }

    /// A dependency whose `eval(value)` follows an unconditional `return`.
    const DEAD_EVAL_SOURCE: &str = "const longLocalName = 1;\nexport function \
                                    toFastProperties(value) {\n  function Fake() {}\n  \
                                    Fake.prototype = value;\n  return value;\n  eval(value);\n}\n";

    fn evidence<'a>(draft: &'a DiagnosticDraft, provenance: &str) -> Option<&'a Evidence> {
        draft
            .evidence
            .iter()
            .find(|entry| entry.provenance == provenance)
    }

    #[test]
    fn eval_removed_from_the_emitted_chunk_is_not_a_blocker() {
        // The build dropped `eval(value)` but the declared names remain.
        let emitted = [
            (0, "const longLocalName = 1;"),
            (0, "export function toFastProperties(value) {"),
            (0, "  return value;"),
            (0, "}"),
        ];
        for engine_reports_eval in [false, true] {
            let drafts = eval_rule_drafts_with_chunk(
                &[("src/chevrotain.js", DEAD_EVAL_SOURCE)],
                &emitted,
                true,
                engine_reports_eval,
            );
            assert!(drafts.is_empty(), "engine reported: {engine_reports_eval}");
        }
    }

    #[test]
    fn eval_that_survives_in_the_emitted_chunk_is_a_blocker_with_emitted_evidence() {
        let source = "const longLocalName = 1;\nexport function run(value) {\n  return \
                      eval(value) + longLocalName;\n}\n";
        let drafts = eval_rule_drafts(source, false);
        assert_eq!(drafts.len(), 1);
        let draft = &drafts[0];
        assert_eq!(draft.status, EvidenceStatus::Inferred);
        // The original-source candidate stays a noncausal observation.
        let candidate = evidence(draft, "bundlewhy source scan").unwrap();
        assert_eq!(candidate.status, EvidenceStatus::Inferred);
        assert!(candidate.detail.contains("line 3"));
        assert!(candidate.detail.contains("candidate"));
        // The emitted call is the blocker evidence, at its generated location.
        let emitted = evidence(draft, "emitted chunk scan").unwrap();
        assert_eq!(emitted.status, EvidenceStatus::Confirmed);
        assert!(
            emitted.detail.contains("main.js:3:10"),
            "{}",
            emitted.detail
        );
        assert!(emitted.detail.contains("source map"));
        let generated = draft.located.generated.as_ref().unwrap();
        assert_eq!(generated.kind, LocationKind::Generated);
        assert_eq!(generated.path, "main.js");
        assert_eq!((generated.line, generated.column), (3, 10));
        let location = draft.located.location.as_ref().unwrap();
        assert_eq!(location.kind, LocationKind::Original);
        assert_eq!(location.line, 3);
        // The retained names are Confirmed generated chunk evidence.
        let names = evidence(draft, "generated chunk text").unwrap();
        assert_eq!(names.status, EvidenceStatus::Confirmed);
        assert!(names.detail.contains("longLocalName"));
        // An engine report on a surviving call confirms the finding.
        let drafts = eval_rule_drafts(source, true);
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].status, EvidenceStatus::Confirmed);
        assert!(evidence(&drafts[0], "emitted chunk scan").is_some());
    }

    #[test]
    fn disabled_mangling_suppresses_the_direct_eval_finding_for_every_signal() {
        let source = "const longLocalName = 1;\nexport function run(value) {\n  return \
                      eval(value) + longLocalName;\n}\n";
        let code = source;
        let tokens: Vec<MapToken> = (0..4u32).map(|line| (line, 0, 0usize, line, 0)).collect();
        for engine_reports_eval in [false, true] {
            // Names are retained and the call survives in the emitted chunk,
            // but the build mangles nothing, so eval blocks nothing.
            let drafts = eval_rule_drafts_with_mangle(
                &[("src/main.js", source)],
                &[("main.js", code, Some(&tokens))],
                None,
                engine_reports_eval,
                MangleSetting::Disabled,
            );
            assert!(drafts.is_empty(), "engine reported: {engine_reports_eval}");
            // The same collection with mangling enabled keeps the finding.
            let drafts = eval_rule_drafts_with_mangle(
                &[("src/main.js", source)],
                &[("main.js", code, Some(&tokens))],
                None,
                engine_reports_eval,
                MangleSetting::Enabled,
            );
            assert_eq!(drafts.len(), 1, "engine reported: {engine_reports_eval}");
            assert!(evidence(&drafts[0], "emitted chunk scan").is_some());
        }
    }

    #[test]
    fn a_collection_without_a_mangle_field_keeps_mangling_enabled() {
        let collection: crate::collect::Collection =
            serde_json::from_str(r#"{"minifyEnabled": true}"#).unwrap();
        assert_eq!(collection.mangle, MangleSetting::Enabled);
        let collection: crate::collect::Collection =
            serde_json::from_str(r#"{"mangle": "disabled"}"#).unwrap();
        assert_eq!(collection.mangle, MangleSetting::Disabled);
    }

    #[test]
    fn eval_before_the_return_survives_and_after_it_does_not() {
        let live = "const longLocalName = 1;\nexport function run(value) {\n  eval(value);\n  \
                    return value;\n}\n";
        assert_eq!(eval_rule_drafts(live, false).len(), 1);
        // Same source shape, but the emitted code lost the dead call.
        let emitted = [
            (0, "const longLocalName = 1;"),
            (0, "export function run(value) {"),
            (0, "  return value;"),
            (0, "}"),
        ];
        let drafts = eval_rule_drafts_with_chunk(
            &[("src/main.js", DEAD_EVAL_SOURCE)],
            &emitted,
            true,
            false,
        );
        assert!(drafts.is_empty());
    }

    #[test]
    fn unrelated_names_in_a_shared_chunk_do_not_blame_a_removed_eval() {
        let live =
            "const liveLocalName = 1;\nexport const run = (code) => {\n  return eval(code);\n};\n";
        let modules = [("src/dead.js", DEAD_EVAL_SOURCE), ("src/live.js", live)];
        let emitted = [
            (0, "const longLocalName = 1;"),
            (0, "export function toFastProperties(value) {"),
            (0, "  return value;"),
            (0, "}"),
            (1, "const liveLocalName = 1;"),
            (1, "export const run = (code) => {"),
            (1, "  return eval(code);"),
            (1, "};"),
        ];
        for engine_reports_eval in [false, true] {
            let drafts = eval_rule_drafts_with_chunk(&modules, &emitted, true, engine_reports_eval);
            // Only the module that owns the emitted call is reported; the dead
            // module's names in the same chunk are not proof.
            assert_eq!(drafts.len(), 1, "engine reported: {engine_reports_eval}");
            assert!(drafts[0].module.ends_with("src/live.js"));
            let names = evidence(&drafts[0], "generated chunk text").unwrap();
            assert!(names.detail.contains("liveLocalName"));
        }
    }

    #[test]
    fn eval_text_in_strings_and_comments_of_the_output_is_not_a_call() {
        let source = "const longLocalName = 1;\nexport function run(value) {\n  return value;\n  \
                      eval(value);\n}\n";
        let emitted = [
            (0, "const longLocalName = 1; // eval(value)"),
            (0, "export function run(value) { return \"eval(value)\"; }"),
            (0, "/* eval(value) */ const pattern = /eval(x)/;"),
            (0, "const target = obj.eval(value);"),
        ];
        let drafts = eval_rule_drafts_with_chunk(&[("src/main.js", source)], &emitted, true, false);
        assert!(drafts.is_empty());
    }

    #[test]
    fn eval_calls_mapped_to_another_module_are_not_this_modules() {
        // The dead module's call is gone; the live call is inlined next to its
        // names and mapped to the live module. Neither chunk text nor the
        // dead module's names tie the call to the dead module.
        let live = "export const run = (c) => eval(c);\n";
        let modules = [("src/dead.js", DEAD_EVAL_SOURCE), ("src/live.js", live)];
        let emitted = [
            (0, "const longLocalName = 1;"),
            (1, "export const run = (c) => eval(c);"),
        ];
        let drafts = eval_rule_drafts_with_chunk(&modules, &emitted, true, true);
        assert!(drafts.is_empty(), "live.js declares no long names");
    }

    /// One input of the mapless-chunk table: the modules, the chunks, and what
    /// the rule may say about them.
    struct MaplessCase {
        name: &'static str,
        modules: Vec<(&'static str, &'static str)>,
        /// Chunk file and text; none of them has a usable source map.
        chunks: Vec<(&'static str, &'static str)>,
    }

    /// Asserts the one conservative rule for chunks without a usable source
    /// map: an engine report keeps an Inferred finding that states the limit,
    /// claims neither an emitted call, a generated location nor retained names,
    /// and a source-scan candidate alone yields no finding.
    fn assert_mapless_finding_is_unverified(case: &MaplessCase) {
        let chunks: Vec<TestChunk<'_>> = case
            .chunks
            .iter()
            .map(|(file, code)| (*file, *code, None))
            .collect();
        let name = case.name;
        let drafts = eval_rule_drafts_with_chunks(&case.modules, &chunks, false);
        assert!(drafts.is_empty(), "{name}: source scan alone");
        let drafts = eval_rule_drafts_with_chunks(&case.modules, &chunks, true);
        assert_eq!(drafts.len(), 1, "{name}");
        let draft = &drafts[0];
        assert_eq!(draft.status, EvidenceStatus::Inferred, "{name}");
        let emitted = evidence(draft, "emitted chunk scan").unwrap();
        assert_eq!(emitted.status, EvidenceStatus::Inferred, "{name}");
        assert!(
            emitted.detail.contains("no usable source map"),
            "{name}: {}",
            emitted.detail
        );
        assert!(
            draft.evidence.iter().all(|entry| {
                entry.provenance != "emitted chunk scan"
                    || entry.status != EvidenceStatus::Confirmed
            }),
            "{name}"
        );
        // No emitted position is claimed, in either location slot.
        assert!(draft.located.generated.is_none(), "{name}");
        assert!(
            draft
                .located
                .location
                .as_ref()
                .is_none_or(|location| location.kind != LocationKind::Generated),
            "{name}"
        );
        // No name is counted in a chunk that cannot attribute names.
        assert!(
            draft.evidence.iter().all(|entry| {
                entry.provenance != "generated chunk text"
                    || entry.status != EvidenceStatus::Confirmed
            }),
            "{name}"
        );
        assert!(
            draft
                .conditions
                .iter()
                .any(|condition| condition.outcome == ConditionOutcome::Unknown),
            "{name}"
        );
    }

    #[test]
    fn mapless_chunks_never_attribute_an_eval_call_to_a_module() {
        let source =
            "const longLocalName = 1;\nexport function run(value) {\n  return eval(value);\n}\n";
        let other = "export const other = 1;\n";
        let cases = [
            MaplessCase {
                name: "one module, one candidate, one chunk",
                modules: vec![("src/main.js", source)],
                chunks: vec![(
                    "main.js",
                    "const longLocalName = 1;\nexport function run(value) { return eval(value); \
                     }\n",
                )],
            },
            MaplessCase {
                name: "one module, two candidates, one survives",
                modules: vec![("src/main.js", FIRST_EVAL_REMOVED_SOURCE)],
                chunks: vec![(
                    "main.js",
                    "const longLocalName = 1;\nfunction b(c) { return eval(c); }\n",
                )],
            },
            MaplessCase {
                name: "one module, two candidates, both survive",
                modules: vec![("src/main.js", FIRST_EVAL_REMOVED_SOURCE)],
                chunks: vec![(
                    "main.js",
                    "const longLocalName = 1;\nfunction a(v) { return v, eval(v); }\nfunction \
                     b(c) { return eval(c); }\n",
                )],
            },
            MaplessCase {
                // The aggregate counts match (two candidates, two calls) while
                // only the later candidate survived, in two copies.
                name: "later candidate copied into two chunks",
                modules: vec![("src/main.js", FIRST_EVAL_REMOVED_SOURCE)],
                chunks: vec![
                    (
                        "a.js",
                        "const longLocalName = 1;\nfunction b(c) { return eval(c); }\n",
                    ),
                    (
                        "b.js",
                        "const longLocalName = 1;\nfunction b(c) { return eval(c); }\n",
                    ),
                ],
            },
            MaplessCase {
                // The module's own call is gone; a banner or runtime plugin
                // appended an unrelated call to the chunk of its only module.
                name: "injected banner call in a chunk of one module",
                modules: vec![("src/main.js", DEAD_EVAL_SOURCE)],
                chunks: vec![(
                    "main.js",
                    "/* banner */ (0, eval)('globalThis.x = 1'); \
                     eval(globalThis.bannerHook);\nconst longLocalName = 1;\nexport function \
                     toFastProperties(value) {\n  return value;\n}\n",
                )],
            },
            MaplessCase {
                name: "injected runtime call in a chunk of one module",
                modules: vec![("src/main.js", DEAD_EVAL_SOURCE)],
                chunks: vec![(
                    "main.js",
                    "const longLocalName = 1;\nexport function toFastProperties(value) {\n  \
                     return value;\n}\nfunction __runtime(code) { return eval(code); }\n",
                )],
            },
            MaplessCase {
                name: "chunk listing many modules",
                modules: vec![("src/main.js", source), ("src/other.js", other)],
                chunks: vec![(
                    "main.js",
                    "const longLocalName = 1;\nexport function run(value) { return eval(value); \
                     }\nexport const other = 1;\n",
                )],
            },
        ];
        for case in &cases {
            assert_mapless_finding_is_unverified(case);
        }
    }

    #[test]
    fn a_mapless_chunk_without_any_eval_call_proves_the_removal() {
        // Scanning the whole chunk finds no direct eval call, so the build
        // removed the call whatever the attribution; the engine report that
        // predates tree shaking does not change that.
        let chunks: [TestChunk<'_>; 1] = [(
            "main.js",
            "const longLocalName = 1;\nexport function toFastProperties(value) {\n  return \
             value;\n}\n",
            None,
        )];
        for engine_reports_eval in [false, true] {
            let drafts = eval_rule_drafts_with_chunks(
                &[("src/main.js", DEAD_EVAL_SOURCE)],
                &chunks,
                engine_reports_eval,
            );
            assert!(drafts.is_empty(), "engine={engine_reports_eval}");
        }
    }

    #[test]
    fn mapped_chunks_still_attribute_calls_and_ignore_mapless_copies() {
        let source =
            "const longLocalName = 1;\nexport function run(value) {\n  return eval(value);\n}\n";
        let mapped =
            "const longLocalName = 1;\nexport function run(value) { return eval(value); }\n";
        let eval_column = mapped.lines().nth(1).unwrap().find("eval").unwrap() as u32;
        let tokens: Vec<MapToken> =
            vec![(0, 0, 0, 0, 0), (1, 0, 0, 1, 0), (1, eval_column, 0, 2, 9)];
        let mapless =
            "const longLocalName = 1;\nexport function run(value) { return eval(value); }\n";
        // A mapped chunk alone: the source map attributes the call.
        let drafts = eval_rule_drafts_with_chunks(
            &[("src/main.js", source)],
            &[("main.js", mapped, Some(&tokens))],
            true,
        );
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].status, EvidenceStatus::Confirmed);
        let location = drafts[0].located.location.as_ref().unwrap();
        assert_eq!(location.kind, LocationKind::Original);
        assert_eq!((location.line, location.column), (3, 10));
        // The same module also printed into a mapless chunk: only the mapped
        // chunk's call is attributed, listed and counted.
        let drafts = eval_rule_drafts_with_chunks(
            &[("src/main.js", source)],
            &[
                ("main.js", mapped, Some(&tokens)),
                ("copy.js", mapless, None),
            ],
            true,
        );
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].status, EvidenceStatus::Confirmed);
        let emitted = evidence(&drafts[0], "emitted chunk scan").unwrap();
        assert!(emitted.detail.contains("main.js:2:"), "{}", emitted.detail);
        assert!(!emitted.detail.contains("copy.js"), "{}", emitted.detail);
        let names = evidence(&drafts[0], "generated chunk text").unwrap();
        assert!(!names.detail.contains("copy.js"), "{}", names.detail);
        // A mapless copy alone never produces a located finding.
        let drafts = eval_rule_drafts_with_chunks(
            &[("src/main.js", source)],
            &[("copy.js", mapless, None)],
            true,
        );
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].status, EvidenceStatus::Inferred);
    }

    #[test]
    fn a_mapped_chunk_keeps_injected_code_unverified() {
        // A banner line that no source mapping covers is not the module's.
        let code = "eval(globalThis.bannerHook);\nconst longLocalName = 1;\nexport function \
                    toFastProperties(value) {\n  return value;\n}\n";
        let tokens: Vec<MapToken> = vec![(1, 0, 0, 0, 0), (2, 0, 0, 1, 0), (3, 0, 0, 3, 0)];
        for engine_reports_eval in [false, true] {
            let drafts = eval_rule_drafts_with_tokens(
                &[("src/main.js", DEAD_EVAL_SOURCE)],
                code,
                Some(&tokens),
                engine_reports_eval,
            );
            if engine_reports_eval {
                assert_eq!(drafts.len(), 1);
                assert_eq!(drafts[0].status, EvidenceStatus::Inferred);
                assert!(drafts[0].located.generated.is_none());
            } else {
                assert!(drafts.is_empty());
            }
        }
    }

    /// The Unknown-or-Unmet outcome of the retained-name condition.
    fn names_condition(draft: &DiagnosticDraft) -> &Condition {
        draft
            .conditions
            .iter()
            .find(|condition| condition.description == "Declared names are shortened in the output")
            .expect("names condition")
    }

    const COPY_SOURCE: &str =
        "const longLocalName = 1;\nexport function run(value) {\n  return eval(value);\n}\n";
    /// A mapped copy of [`COPY_SOURCE`] whose names the minifier mangled.
    const MANGLED_COPY: &str = "const a = 1;\nexport function run(c) { return eval(c); }\n";
    /// A copy that kept the names, with or without the call.
    const KEPT_COPY: &str =
        "const longLocalName = 1;\nexport function run(value) { return eval(value); }\n";
    const KEPT_COPY_WITHOUT_EVAL: &str =
        "const longLocalName = 1;\nexport function run(value) { return value; }\n";

    /// One table row: name, loaded chunks, the files the module lists (all
    /// loaded chunks when `None`) and the reason the finding must state.
    type ListingCase<'a> = (&'a str, Vec<TestChunk<'a>>, Option<&'a [&'a str]>, &'a str);

    fn mangled_copy_tokens() -> Vec<MapToken> {
        let eval_column = MANGLED_COPY.lines().nth(1).unwrap().find("eval").unwrap() as u32;
        vec![(0, 0, 0, 0, 0), (1, 0, 0, 1, 0), (1, eval_column, 0, 2, 9)]
    }

    /// Every finding that survives next to an unverified copy states that the
    /// names could not be checked there: the names condition is Unknown, an
    /// Inferred `emitted chunk scan` entry names the reason, and no retained
    /// name is claimed from a copy that holds no attributed call.
    fn assert_unchecked_copy_finding(draft: &DiagnosticDraft, reason: &str, name: &str) {
        let condition = names_condition(draft);
        assert_eq!(condition.outcome, ConditionOutcome::Unknown, "{name}");
        assert!(condition.detail.contains(reason), "{name}: {condition:?}");
        assert!(
            draft.evidence.iter().any(|entry| {
                entry.provenance == "emitted chunk scan"
                    && entry.status == EvidenceStatus::Inferred
                    && entry.detail.contains(reason)
            }),
            "{name}: {:?}",
            draft.evidence
        );
        assert!(
            draft.evidence.iter().all(|entry| {
                entry.provenance != "generated chunk text"
                    || entry.status != EvidenceStatus::Confirmed
            }),
            "{name}"
        );
    }

    #[test]
    fn an_unverified_copy_keeps_the_finding_next_to_a_mangled_attributed_copy() {
        let tokens = mangled_copy_tokens();
        // The attributed copy mangled its names; the other copy of the module
        // cannot be checked, so the mangled copy alone must not hide it.
        let banner_tokens: Vec<MapToken> = vec![(1, 0, 0, 0, 0), (2, 0, 0, 1, 0)];
        let banner = "eval(globalThis.bannerHook);\nconst longLocalName = 1;\n";
        let cases: [ListingCase<'_>; 4] = [
            (
                "mapless copy that keeps the names",
                vec![
                    ("main.js", MANGLED_COPY, Some(&tokens)),
                    ("copy.js", KEPT_COPY, None),
                ],
                None,
                "copy.js has no usable source map",
            ),
            (
                "mapless copy that mangled its names too",
                vec![
                    ("main.js", MANGLED_COPY, Some(&tokens)),
                    ("copy.js", MANGLED_COPY, None),
                ],
                None,
                "copy.js has no usable source map",
            ),
            (
                "listed chunk that cannot be read",
                vec![("main.js", MANGLED_COPY, Some(&tokens))],
                Some(&["main.js", "missing.js"]),
                "missing.js could not be read",
            ),
            (
                "mapped copy with a call no mapping covers",
                vec![
                    ("main.js", MANGLED_COPY, Some(&tokens)),
                    ("banner.js", banner, Some(&banner_tokens)),
                ],
                None,
                "banner.js has a direct eval call at 1:1 that no source mapping attributes",
            ),
        ];
        for (name, chunks, listed, reason) in cases {
            for engine_reports_eval in [false, true] {
                let drafts = eval_rule_drafts_with_listing(
                    &[("src/main.js", COPY_SOURCE)],
                    &chunks,
                    listed,
                    engine_reports_eval,
                );
                assert_eq!(drafts.len(), 1, "{name}, engine={engine_reports_eval}");
                // The attributed call still locates the finding.
                let location = drafts[0].located.location.as_ref().unwrap();
                assert_eq!((location.line, location.column), (3, 10), "{name}");
                assert_unchecked_copy_finding(&drafts[0], reason, name);
            }
        }
        // Copies that provably hold no direct eval call, and a mangled
        // attributed copy, leave nothing blocked.
        let tokens_without_names = mangled_copy_tokens();
        for (name, chunks) in [
            (
                "mapless copy without eval",
                vec![
                    ("main.js", MANGLED_COPY, Some(&tokens_without_names)),
                    ("copy.js", KEPT_COPY_WITHOUT_EVAL, None),
                ],
            ),
            (
                "mapped copy without eval",
                vec![
                    ("main.js", MANGLED_COPY, Some(&tokens_without_names)),
                    (
                        "copy.js",
                        KEPT_COPY_WITHOUT_EVAL,
                        Some(&tokens_without_names),
                    ),
                ],
            ),
        ] {
            for engine_reports_eval in [false, true] {
                let drafts = eval_rule_drafts_with_chunks(
                    &[("src/main.js", COPY_SOURCE)],
                    &chunks,
                    engine_reports_eval,
                );
                assert!(drafts.is_empty(), "{name}, engine={engine_reports_eval}");
            }
        }
    }

    #[test]
    fn retained_names_are_scanned_only_in_chunks_with_an_attributed_call() {
        let tokens: Vec<MapToken> = vec![(0, 0, 0, 0, 0), (1, 0, 0, 1, 0)];
        // A mapped copy keeps the long name but holds no call; a different,
        // mapless copy holds the only call. The two copies are unrelated, so
        // the name is no evidence against the call.
        let chunks: [TestChunk<'_>; 2] = [
            ("mapped.js", KEPT_COPY_WITHOUT_EVAL, Some(&tokens)),
            ("copy.js", KEPT_COPY, None),
        ];
        let drafts = eval_rule_drafts_with_chunks(&[("src/main.js", COPY_SOURCE)], &chunks, true);
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].status, EvidenceStatus::Inferred);
        assert_unchecked_copy_finding(
            &drafts[0],
            "copy.js has no usable source map",
            "unrelated mapped copy",
        );
        assert!(
            drafts[0]
                .conditions
                .iter()
                .all(|condition| condition.outcome != ConditionOutcome::Unmet
                    || condition.description.starts_with("No direct eval")),
            "{:?}",
            drafts[0].conditions
        );
        // Without an engine report the source-scan candidate alone is omitted.
        let drafts = eval_rule_drafts_with_chunks(&[("src/main.js", COPY_SOURCE)], &chunks, false);
        assert!(drafts.is_empty());
        // A name in the chunk that holds the attributed call still counts.
        let attributed_tokens = {
            let eval_column = KEPT_COPY.lines().nth(1).unwrap().find("eval").unwrap() as u32;
            vec![(0, 0, 0, 0, 0), (1, 0, 0, 1, 0), (1, eval_column, 0, 2, 9)]
        };
        let chunks: [TestChunk<'_>; 2] = [
            ("mapped.js", KEPT_COPY_WITHOUT_EVAL, Some(&tokens)),
            ("kept.js", KEPT_COPY, Some(&attributed_tokens)),
        ];
        let drafts = eval_rule_drafts_with_chunks(&[("src/main.js", COPY_SOURCE)], &chunks, true);
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].status, EvidenceStatus::Confirmed);
        let condition = names_condition(&drafts[0]);
        assert_eq!(condition.outcome, ConditionOutcome::Unmet);
        let names = evidence(&drafts[0], "generated chunk text").unwrap();
        assert!(names.detail.contains("kept.js"), "{}", names.detail);
        assert!(!names.detail.contains("mapped.js"), "{}", names.detail);
    }

    #[test]
    fn an_engine_report_with_uninspectable_chunks_stays_an_inferred_finding() {
        let banner = "eval(globalThis.bannerHook);\nconst a = 1;\nexport function run(c) {\n  \
                      return c;\n}\n";
        let banner_tokens: Vec<MapToken> = vec![(1, 0, 0, 0, 0), (2, 0, 0, 1, 0), (3, 0, 0, 3, 0)];
        let mangled_mapless = "const a = 1;\nexport function run(c) { return eval(c); }\n";
        let cases: [ListingCase<'_>; 4] = [
            (
                "no chunk lists the module",
                vec![("main.js", KEPT_COPY_WITHOUT_EVAL, None)],
                Some(&[]),
                "the engine lists no output chunk for the module",
            ),
            (
                "the only listed chunk cannot be read",
                vec![("main.js", KEPT_COPY_WITHOUT_EVAL, None)],
                Some(&["missing.js"]),
                "missing.js could not be read",
            ),
            (
                "a call sits in an unmapped segment of a usable map",
                vec![("main.js", banner, Some(&banner_tokens))],
                None,
                "main.js has a direct eval call at 1:1 that no source mapping attributes",
            ),
            (
                "a mapless chunk holds the call and the names are mangled",
                vec![("main.js", mangled_mapless, None)],
                None,
                "main.js has no usable source map",
            ),
        ];
        for (name, chunks, listed, reason) in cases {
            let drafts = eval_rule_drafts_with_listing(
                &[("src/main.js", COPY_SOURCE)],
                &chunks,
                listed,
                true,
            );
            assert_eq!(drafts.len(), 1, "{name}");
            let draft = &drafts[0];
            assert_eq!(draft.status, EvidenceStatus::Inferred, "{name}");
            assert_unchecked_copy_finding(draft, reason, name);
            let emitted = draft
                .conditions
                .iter()
                .find(|condition| condition.description.starts_with("No direct eval"))
                .unwrap();
            assert_eq!(emitted.outcome, ConditionOutcome::Unmet, "{name}");
            assert!(emitted.detail.contains("could not be checked"), "{name}");
            // The summary keeps the limit visible.
            assert!(draft.summary.contains("could not be checked"), "{name}");
            // The source-scan candidate alone stays omitted.
            let drafts = eval_rule_drafts_with_listing(
                &[("src/main.js", COPY_SOURCE)],
                &chunks,
                listed,
                false,
            );
            assert!(drafts.is_empty(), "{name}: source scan alone");
        }
    }

    #[test]
    fn an_unverified_finding_shows_the_candidate_original_location_not_an_emitted_one() {
        // The location of a finding without an attributed call is the
        // original-source candidate (kind original, with a snippet of the
        // module), and the emitted location stays unavailable.
        let chunks: [TestChunk<'_>; 1] = [("main.js", KEPT_COPY, None)];
        let drafts = eval_rule_drafts_with_chunks(&[("src/main.js", COPY_SOURCE)], &chunks, true);
        assert_eq!(drafts.len(), 1);
        let located = &drafts[0].located;
        assert!(located.generated.is_none());
        let location = located.location.as_ref().unwrap();
        assert_eq!(location.kind, LocationKind::Original);
        assert!(location.path.ends_with("src/main.js"), "{}", location.path);
        // The first direct eval of the module source, not of the emitted code.
        assert_eq!((location.line, location.column), (3, 10));
        let snippet = located.snippet.as_ref().unwrap();
        assert_eq!(snippet.kind, LocationKind::Original);
        assert_eq!(snippet.highlight_line, 3);
        // Neither the engine log nor the module text names a position: the
        // candidate is the first line of the module file.
        let no_source_call = "const longLocalName = 1;\nexport const run = (value) => value;\n";
        let drafts =
            eval_rule_drafts_with_chunks(&[("src/main.js", no_source_call)], &chunks, true);
        assert_eq!(drafts.len(), 1);
        let location = drafts[0].located.location.as_ref().unwrap();
        assert_eq!(location.kind, LocationKind::Original);
        assert_eq!((location.line, location.column), (1, 1));
        assert!(drafts[0].located.generated.is_none());
    }

    /// A module whose first eval call (line 4) is dead code and whose second
    /// (line 7) is the call the build keeps.
    const FIRST_EVAL_REMOVED_SOURCE: &str = "const longLocalName = 1;\nexport function a(v) {\n  \
                                             return v;\n  eval(v);\n}\nexport function b(c) {\n  \
                                             return eval(c);\n}\n";

    #[test]
    fn the_location_is_the_surviving_call_not_the_first_candidate() {
        // The emitted chunk keeps `eval(c)` only; its source map points at
        // line 7 of the module while the first source candidate is line 4.
        let code = "const longLocalName = 1;\nfunction a(v) { return v; }\nfunction b(c) { return \
                    eval(c); }\n";
        let eval_column = code.lines().nth(2).unwrap().find("eval").unwrap() as u32;
        let tokens: Vec<MapToken> = vec![
            (0, 0, 0, 0, 0),
            (1, 0, 0, 1, 0),
            (2, 0, 0, 5, 0),
            (2, eval_column, 0, 6, 9),
        ];
        for engine_reports_eval in [false, true] {
            let drafts = eval_rule_drafts_with_tokens(
                &[("src/main.js", FIRST_EVAL_REMOVED_SOURCE)],
                code,
                Some(&tokens),
                engine_reports_eval,
            );
            assert_eq!(drafts.len(), 1, "engine={engine_reports_eval}");
            let location = drafts[0].located.location.as_ref().unwrap();
            assert!(location.path.ends_with("src/main.js"));
            assert_eq!((location.line, location.column), (7, 10));
            let generated = drafts[0].located.generated.as_ref().unwrap();
            assert_eq!(generated.line, 3);
            assert_eq!(generated.column as usize, eval_column as usize + 1);
        }
    }

    #[test]
    fn the_location_follows_the_first_emitted_call_when_several_survive() {
        // The build reordered the functions: the emitted chunk holds `b`
        // first, so its call locates the finding, and both calls are listed.
        let code = "const longLocalName = 1;\nfunction b(c) { return eval(c); }\nfunction a(v) { \
                    return v, eval(v); }\n";
        let column = |line: usize| code.lines().nth(line).unwrap().find("eval").unwrap() as u32;
        let tokens: Vec<MapToken> = vec![
            (0, 0, 0, 0, 0),
            (1, 0, 0, 5, 0),
            (1, column(1), 0, 6, 9),
            (2, 0, 0, 1, 0),
            (2, column(2), 0, 3, 2),
        ];
        let drafts = eval_rule_drafts_with_tokens(
            &[("src/main.js", FIRST_EVAL_REMOVED_SOURCE)],
            code,
            Some(&tokens),
            true,
        );
        assert_eq!(drafts.len(), 1);
        let location = drafts[0].located.location.as_ref().unwrap();
        assert_eq!((location.line, location.column), (7, 10));
        assert_eq!(drafts[0].located.generated.as_ref().unwrap().line, 2);
        let emitted = evidence(&drafts[0], "emitted chunk scan").unwrap();
        assert!(emitted.detail.contains("main.js:2:"), "{}", emitted.detail);
        assert!(emitted.detail.contains("main.js:3:"), "{}", emitted.detail);
    }

    const MANGLED_OWNER_SOURCE: &str =
        "export function run(c) {\n  const longLocalName = 1;\n  return eval(c);\n}\n";
    const SIBLING_SOURCE: &str = "export const longLocalName = 2;\n";

    #[test]
    fn retained_names_of_a_sibling_module_do_not_block_the_evaluated_module() {
        let modules = [
            ("src/owner.js", MANGLED_OWNER_SOURCE),
            ("src/sibling.js", SIBLING_SOURCE),
        ];
        // The owner's own local was mangled; only the sibling spells the name.
        let mangled = [
            (0, "export function run(c) { const a = 1; return eval(c); }"),
            (1, "export const longLocalName = 2;"),
        ];
        for engine_reports_eval in [false, true] {
            let drafts = eval_rule_drafts_with_chunk(&modules, &mangled, true, engine_reports_eval);
            assert!(drafts.is_empty(), "engine={engine_reports_eval}");
        }
        // The sibling comes first in the chunk: the order does not matter.
        let reordered = [mangled[1], mangled[0]];
        let drafts = eval_rule_drafts_with_chunk(&modules, &reordered, true, true);
        assert!(drafts.is_empty());
        // The owner keeps the name in its own code: the finding is reported
        // and lists the name, whether or not the sibling spells it too.
        let kept = [
            (
                0,
                "export function run(c) { const longLocalName = 1; return eval(c); }",
            ),
            (1, "export const longLocalName = 2;"),
        ];
        let drafts = eval_rule_drafts_with_chunk(&modules, &kept, true, true);
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].status, EvidenceStatus::Confirmed);
        let names = evidence(&drafts[0], "generated chunk text").unwrap();
        assert!(names.detail.contains("longLocalName"), "{}", names.detail);
    }

    #[test]
    fn retained_names_are_attributed_per_occurrence_inside_one_line() {
        // One generated line holds two spellings of the name, back to back:
        // the first belongs to the sibling, the second to the owner.
        let modules = [
            ("src/owner.js", MANGLED_OWNER_SOURCE),
            ("src/sibling.js", SIBLING_SOURCE),
        ];
        let code = "const pair = [longLocalName,longLocalName];\nexport function run(c) { return \
                    eval(c); }\n";
        let first = code.find("longLocalName").unwrap() as u32;
        let second = first + "longLocalName,".len() as u32;
        let eval_column = code.lines().nth(1).unwrap().find("eval").unwrap() as u32;
        let tokens = |second_owner: usize| -> Vec<MapToken> {
            vec![
                (0, 0, 1, 0, 0),
                (0, first, 1, 0, 0),
                (0, second, second_owner, 1, 8),
                (1, 0, 0, 0, 0),
                (1, eval_column, 0, 2, 9),
            ]
        };
        // The second spelling is the owner's: the name is retained.
        let drafts = eval_rule_drafts_with_tokens(&modules, code, Some(&tokens(0)), true);
        assert_eq!(drafts.len(), 1);
        // Both spellings are the sibling's: nothing is blocked.
        let drafts = eval_rule_drafts_with_tokens(&modules, code, Some(&tokens(1)), true);
        assert!(drafts.is_empty());
    }

    #[test]
    fn emitted_eval_scan_leaves_calls_of_a_mapless_chunk_unattributed() {
        let source =
            "const longLocalName = 1;\nexport function run(value) {\n  return eval(value);\n}\n";
        let dir = tempfile::tempdir().unwrap();
        let module = CollectedModule {
            id: dir
                .path()
                .join("src/main.js")
                .to_string_lossy()
                .into_owned(),
            path: Some(
                dir.path()
                    .join("src/main.js")
                    .to_string_lossy()
                    .into_owned(),
            ),
            included: true,
            chunks: vec!["main.js".to_owned()],
            ..CollectedModule::default()
        };
        let collection = crate::collect::Collection {
            modules: vec![module.clone()],
            ..crate::collect::Collection::default()
        };
        std::fs::create_dir_all(dir.path().join("build")).unwrap();
        std::fs::write(dir.path().join("build/main.js"), source).unwrap();
        let chunk = crate::chunks::Chunk::load(
            &dir.path().join("build"),
            "main.js",
            None,
            true,
            dir.path(),
        )
        .unwrap();
        let context = Context::new(
            &collection,
            dir.path().to_path_buf(),
            "app".to_owned(),
            vec![chunk],
            crate::sources::SourceStore::new(true),
        );
        // The chunk has no source map and lists only this module: its call is
        // still unattributed.
        let mut scan = EmittedEvalScan::default();
        match scan.classify(&context, &module) {
            EmittedEval::Unverified(reasons) => {
                let reason = reasons.join("; ");
                assert!(reason.contains("no usable source map"), "{reason}");
            }
            other => panic!("unexpected {other:?}"),
        }
        assert_eq!(
            scan.chunk_offsets(&context, 0),
            [source.find("eval(value)").unwrap()]
        );
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
    fn namespace_local_var_eval_does_not_shadow_outside_calls() {
        for keyword in ["namespace", "module"] {
            assert_eq!(
                direct_eval_line(&format!(
                    "{keyword} N {{ var eval = helper; }}\neval(code);"
                )),
                Some((1, 0))
            );
            assert_eq!(
                direct_eval_line(&format!("{keyword} N {{ var eval = helper; eval(code); }}")),
                None
            );
            assert_eq!(
                direct_eval_line(&format!(
                    "{keyword} N {{ var eval = helper; eval(code); }}\neval(code);"
                )),
                Some((1, 0))
            );
        }
        // Nested and dotted namespaces.
        assert_eq!(
            direct_eval_line(
                "namespace A { namespace B { var eval = helper; eval(code); } }\neval(code);"
            ),
            Some((1, 0))
        );
        assert_eq!(
            direct_eval_line("namespace A.B { var eval = helper; eval(code); }"),
            None
        );
        assert_eq!(
            direct_eval_line("export namespace A.B { var eval = helper; }\neval(code);"),
            Some((1, 0))
        );
        assert_eq!(
            direct_eval_line("declare namespace N { var eval: any; }\neval(code);"),
            Some((1, 0))
        );
        // A `var` in a nested block belongs to the enclosing namespace.
        assert_eq!(
            direct_eval_line(
                "namespace N { if (x) { var eval = helper; } eval(code); }\neval(code);"
            ),
            Some((1, 0))
        );
        // A function keeps its own `var` boundary inside a namespace.
        assert_eq!(
            direct_eval_line("namespace N { function f() { var eval = helper; } eval(code); }"),
            Some((0, 50))
        );
        // Lexical declarations keep the block path.
        assert_eq!(
            direct_eval_line("namespace N { let eval = helper; eval(code); }\neval(code);"),
            Some((1, 0))
        );
        // Plain blocks still shadow the surrounding source scope.
        assert_eq!(direct_eval_line("{ var eval = helper; } eval(code);"), None);
        // Identifiers that merely resemble the keywords stay ordinary blocks.
        assert_eq!(
            direct_eval_line("module.exports = { run() { var eval = helper; } }; eval(code);"),
            Some((0, 51))
        );
        assert_eq!(
            direct_eval_line("{ var namespace = 1; } { var eval = helper; } eval(code);"),
            None
        );
    }

    #[test]
    fn namespace_var_eval_scope_table() {
        // (source, expected direct-eval position). `None` means every call is
        // shadowed by a binding; `Some` is the first unshadowed call.
        let cases: &[(&str, Option<(u32, u32)>)] = &[
            // Quoted ambient module names.
            (
                "declare module \"pkg\" { export var eval: any }\neval(code);",
                Some((1, 0)),
            ),
            (
                "declare module 'pkg' { var eval: any }\neval(code);",
                Some((1, 0)),
            ),
            (
                "declare module \"pkg\" { var eval: any; eval(code); }",
                None,
            ),
            (
                "module \"a{b\" { var eval = helper; }\neval(code);",
                Some((1, 0)),
            ),
            // Unicode namespace names.
            (
                "namespace \u{3a9} { var eval = helper }\neval(code);",
                Some((1, 0)),
            ),
            ("namespace \u{3a9} { var eval = helper; eval(code); }", None),
            (
                "namespace \u{3a9}.\u{3bb}x { var eval = helper }\neval(code);",
                Some((1, 0)),
            ),
            (
                "module caf\u{e9} { var eval = helper }\neval(code);",
                Some((1, 0)),
            ),
            // The keyword and the name share a line; otherwise automatic
            // semicolon insertion leaves an ordinary block.
            ("module\nN\n{ var eval = helper; }\neval(code);", None),
            ("namespace\nN\n{ var eval = helper; }\neval(code);", None),
            ("module\n\"pkg\"\n{ var eval = helper; }\neval(code);", None),
            (
                "module N\n{ var eval = helper; }\neval(code);",
                Some((2, 0)),
            ),
            (
                "namespace N.\nM\n{ var eval = helper; }\neval(code);",
                Some((3, 0)),
            ),
            // Same-line comments are trivia between the keyword and the name.
            (
                "namespace/* note */N { var eval = helper; } eval(code);",
                Some((0, 44)),
            ),
            (
                "namespace /* note */ N { var eval = helper; }\neval(code);",
                Some((1, 0)),
            ),
            (
                "module/* a */N/* b */.M { var eval = helper; }\neval(code);",
                Some((1, 0)),
            ),
            (
                "module/* note */\"pkg\" { var eval = helper; }\neval(code);",
                Some((1, 0)),
            ),
            // A comment that contains a line terminator is a line break.
            (
                "namespace /*\n*/ N { var eval = helper; }\neval(code);",
                None,
            ),
            (
                "namespace // note\nN { var eval = helper; }\neval(code);",
                None,
            ),
            // Words that follow the keywords in expressions are not names.
            (
                "module in { a: 1 }; { var eval = helper; } eval(code);",
                None,
            ),
            // Modifiers between `export` and the declaration keyword.
            (
                "namespace N { export async function eval() {} }\nnamespace N { eval(code); }",
                None,
            ),
            (
                "namespace N { export declare async function eval(): void; }\nnamespace N { \
                 eval(code); }",
                None,
            ),
            (
                "namespace N { export async /* c */ function eval() {} }\nnamespace N { \
                 eval(code); }",
                None,
            ),
            (
                "namespace N { async function eval() {} }\nnamespace N { eval(code); }",
                Some((1, 14)),
            ),
            // Exported members are shared by merged declarations.
            (
                "namespace N { export var eval = helper; }\nnamespace N { eval(code); }",
                None,
            ),
            (
                "namespace N { eval(code); }\nnamespace N { export var eval = helper; }",
                None,
            ),
            (
                "module N { export let eval = helper; }\nmodule N { eval(code); }",
                None,
            ),
            (
                "namespace N { export const eval = helper; }\nnamespace N { eval(code); }",
                None,
            ),
            (
                "namespace N { export function eval() {} }\nnamespace N { eval(code); }",
                None,
            ),
            (
                "namespace N { export declare var eval: any; }\nnamespace N { eval(code); }",
                None,
            ),
            (
                "declare module \"pkg\" { export var eval: any }\ndeclare module 'pkg' { \
                 eval(code); }",
                None,
            ),
            (
                "namespace A.B { export var eval = helper; }\nnamespace A { export namespace B { \
                 eval(code); } }",
                None,
            ),
            // The merge never reaches outside the merged declarations.
            (
                "namespace N { export var eval = helper; }\nnamespace N { }\neval(code);",
                Some((2, 0)),
            ),
            (
                "namespace N { export var eval = helper; }\nnamespace M { eval(code); }",
                Some((1, 14)),
            ),
            (
                "declare module \"a\" { export var eval: any }\ndeclare module \"b\" { \
                 eval(code); }",
                Some((1, 21)),
            ),
            (
                "namespace N { export var eval = helper; }\nfunction f() { namespace N { \
                 eval(code); } }",
                Some((1, 29)),
            ),
            // Non-exported bindings stay local to their own declaration.
            (
                "namespace N { var eval = helper; }\nnamespace N { eval(code); }",
                Some((1, 14)),
            ),
            (
                "namespace N { let eval = helper; }\nnamespace N { eval(code); }",
                Some((1, 14)),
            ),
            (
                "namespace N { export namespace M { export var eval = helper; } }\nnamespace N { \
                 export namespace M { eval(code); } }",
                None,
            ),
            // A nested namespace that is not exported is a separate symbol in
            // each outer declaration.
            (
                "namespace N { namespace M { export var eval = helper; } }\nnamespace N { \
                 namespace M { eval(code); } }",
                Some((1, 28)),
            ),
        ];
        for (source, expected) in cases {
            assert_eq!(direct_eval_line(source), *expected, "{source}");
        }
    }

    #[test]
    fn namespace_scan_known_limits_are_pinned() {
        // The scan has no parser: merged declarations are matched by their
        // written qualified name, so a merge through an alias or a class or
        // function with the same name is not followed, and the exported
        // binding stays in its own declaration.
        assert_eq!(
            direct_eval_line(
                "namespace N { export var eval = helper; }\nnamespace N\n.M { eval(code); }"
            ),
            Some((2, 5))
        );
        // Constructs without a recognized shape keep the program-wide `var`
        // boundary, which suppresses calls elsewhere in the source.
        assert_eq!(
            direct_eval_line("declare global { var eval: any }\neval(code);"),
            None
        );
        // Escapes in module names are compared as written.
        assert_eq!(
            direct_eval_line(
                "declare module \"p\\x6bg\" { export var eval: any }\ndeclare module \"pkg\" { \
                 eval(code); }"
            ),
            Some((1, 23))
        );
    }

    #[test]
    fn namespace_local_var_eval_reaches_the_mangling_rule() {
        let prefix = "const longLocalName = 1;\n";
        for keyword in ["namespace", "module"] {
            let outside = format!("{prefix}{keyword} N {{ var eval = helper; }}\neval(code);");
            let drafts = eval_rule_drafts(&outside, false);
            assert_eq!(drafts.len(), 1);
            assert_eq!(drafts[0].rule, RuleId::DirectEvalBlocksMangling);
            assert_eq!(drafts[0].status, EvidenceStatus::Inferred);
            let shadowed = format!("{prefix}{keyword} N {{ var eval = helper; eval(code); }}");
            assert!(eval_rule_drafts(&shadowed, false).is_empty());
            // The emitted chunk holds no call either, so the engine report
            // alone does not produce a finding.
            assert!(eval_rule_drafts(&shadowed, true).is_empty());
        }
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
        // The emitted chunk holds no call that reaches the global scope.
        assert!(eval_rule_drafts(source, true).is_empty());
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

    const AND_GUARD: &str = "the retained read controls a && short-circuit branch";

    #[test]
    fn keyword_named_properties_are_division_operands() {
        let read = "process.env.FLAG";
        let in_regex = |code: &str| in_ranges(&non_code_ranges(code), code.find(read).unwrap());
        for terminator in LINE_TERMINATORS {
            for keyword in REGEX_KEYWORDS {
                for access in [".", "?.", ". /* c */ ", ".// c\n", ". "] {
                    for comment in ["// c", "//", "/* c */ // d", "/* a\nb */"] {
                        let code = format!(
                            "obj{access}{keyword} {comment}{terminator}/ {read} / 2 && run()"
                        );
                        assert!(!in_regex(&code), "{code:?}");
                        assert_eq!(
                            keyword_guard_in_code(&code, read),
                            Some(AND_GUARD),
                            "{code:?}"
                        );
                    }
                }
                // A private name is a name too, while a dot that ends a number
                // literal is not a member access.
                let code = format!("this.#{keyword} // c{terminator}/ {read} / 2 && run()");
                assert!(!in_regex(&code), "{code:?}");
                let code = format!("1. {keyword} // c{terminator}/{read}/.test(x)");
                assert!(in_regex(&code), "{code:?}");
                // The keyword itself still starts a regular expression, also
                // as a spread operand.
                let code = format!("{keyword} // c{terminator}/{read}/.test(x) && run()");
                assert!(in_regex(&code), "{code:?}");
            }
            // A postfix increment or decrement ends an operand, so the slash
            // divides; after a prefix operator or an operator it starts a
            // regular expression.
            for code in [
                format!("x = i++{terminator}/{read}/2"),
                format!("x = i-- /{read}/ 2"),
            ] {
                assert!(!in_regex(&code), "{code:?}");
            }
            let code = format!("x = i + {terminator}/{read}/.test(y)");
            assert!(in_regex(&code), "{code:?}");
        }
        // A template substitution starts an expression.
        let code = format!("`${{/{read}/.test(x)}}`");
        assert!(in_regex(&code), "{code:?}");
    }

    #[test]
    fn asi_decision_follows_the_previous_and_next_token() {
        let read = "process.env.FLAG";
        for terminator in LINE_TERMINATORS {
            // The next token continues the expression, so the later `&&`
            // still sees the read.
            for continuation in [
                "+ready",
                "- ready",
                "+ +ready",
                "* ready",
                "% ready",
                "/ ready",
                ".ready",
                "?.ready",
                "[0]",
                "(ready)",
                "`t`",
                "in ready",
                "instanceof ready",
                "!= ready",
                "!== ready",
                "== ready",
                "< ready",
                "> ready",
                "|| ready",
                "?? ready",
                "& ready",
                "^ ready",
                "| ready",
                "?ready:ready",
            ] {
                for comment in ["", "// c", "/* c */"] {
                    let code =
                        format!("{read}{terminator}{comment}{terminator}{continuation} && run()");
                    assert!(keyword_guard(&code, read).is_some(), "{code:?}");
                }
            }
            // The next token starts a new statement, so the read is not a
            // guard for the later `&&`.
            for statement in [
                "++ready",
                "--ready",
                "/* c */ ++ready",
                "// c\n++ready",
                "ready",
                "12",
                "'a'",
                "\"a\"",
                "!ready",
                "~ready",
                "@ready",
                "#ready",
                "return",
                "const",
            ] {
                for before in ["", " // c", " /* c */", "  // c  "] {
                    let code = format!("{read}{before}{terminator}{statement} && run()");
                    assert_eq!(keyword_guard(&code, read), None, "{code:?}");
                }
            }
            // A prefix operator pair needs adjacent characters.
            let code = format!("{read}{terminator}+ +ready && run()");
            assert!(keyword_guard(&code, read).is_some(), "{code:?}");
            // A previous token that cannot end an expression continues it.
            for operator in [
                "+",
                "-",
                "*",
                "/",
                ".",
                "in",
                "instanceof",
                ",",
                "=>",
                "&",
                "<",
            ] {
                if operator == "," || operator == "=>" {
                    continue;
                }
                let code = format!("{read} {operator}{terminator}ready && run()");
                assert!(keyword_guard(&code, read).is_some(), "{code:?}");
            }
            // The statement before the read is not affected by the next line.
            let code = format!("x = 1{terminator}++y; {read} && run()");
            assert!(keyword_guard(&code, read).is_some(), "{code:?}");
        }
        // Tokens that need a parser stay on the conservative side: `as` and
        // `satisfies` (TypeScript) and `of` do not continue an expression, so
        // the read is not treated as guarded.
        for word in ["as T", "satisfies T", "of items"] {
            let code = format!("{read}\n{word} && run()");
            assert_eq!(keyword_guard(&code, read), None, "{code:?}");
        }
    }

    #[test]
    fn keyword_tables_agree_on_operand_keywords() {
        let read = "process.env.FLAG";
        for keyword in OPEN_KEYWORDS {
            assert!(REGEX_KEYWORDS.contains(&keyword), "{keyword}");
        }
        for terminator in LINE_TERMINATORS {
            for keyword in REGEX_KEYWORDS {
                // A comment and any line terminator between the keyword and
                // the slash still leave the slash starting a regular
                // expression.
                for comment in ["// c", "/* c */ // c", "/* a\nb */"] {
                    let code = format!("{keyword} {comment}{terminator}/{read}/.test(x) && run()");
                    assert!(
                        in_ranges(&non_code_ranges(&code), code.find(read).unwrap()),
                        "{code:?}"
                    );
                }
            }
            // A keyword that cannot end an expression takes its operand from
            // the next line.
            for keyword in OPEN_KEYWORDS {
                let code = format!("x = {keyword}{terminator}{read} && run()");
                // `typeof`, `void`, `delete` and `new` do not pass the value
                // of their operand on, so the read does not control the
                // branch.
                let expected = if matches!(keyword, "typeof" | "void" | "delete" | "new") {
                    None
                } else {
                    Some(AND_GUARD)
                };
                assert_eq!(keyword_guard(&code, read), expected, "{code:?}");
            }
        }
        // The operand of `await` is a regular expression, not a division.
        let code = "async () => await // c\r/process.env.FLAG/.test(x) && run()";
        assert!(in_ranges(&non_code_ranges(code), code.find(read).unwrap()));
    }

    #[test]
    fn a_statement_boundary_ends_an_expression_bodied_arrow_for_guards() {
        let read = "process.env.FLAG";
        for terminator in LINE_TERMINATORS {
            // The arrow ends at the boundary, so the read after it is a plain
            // statement that its own `&&` guards.
            let code = format!("f = () => x{terminator}{read} && run()");
            assert_eq!(keyword_guard(&code, read), Some(AND_GUARD), "{code:?}");
            // A continuation keeps the read inside the arrow body, where it
            // still controls the branch.
            let code = format!("f = () => {read}{terminator}&& run()");
            assert_eq!(keyword_guard(&code, read), Some(AND_GUARD), "{code:?}");
            let code = format!("f = () => {read}{terminator}++x; g(run && y)");
            assert_eq!(keyword_guard(&code, read), None, "{code:?}");
        }
    }

    #[test]
    fn lexical_token_limits_stay_conservative() {
        let read = "process.env.FLAG";
        // A comma inside TypeScript type arguments ends an arrow body early,
        // so the call is reported as a possible direct eval.
        let source = "const f = (eval) => g<A, B>(eval(code));";
        assert!(direct_eval_line(source).is_some(), "{source:?}");
        // A method declaration named `eval` is no call, but a declaration the
        // scan cannot place (a statement block that only looks like a member
        // list, or a `case` clause) is still reported.
        for source in [
            "if (x) { eval(code) }",
            "label: { eval(code) {} }",
            "switch (a) { case 1: { eval(code) {} } }",
        ] {
            assert!(direct_eval_line(source).is_some(), "{source:?}");
        }
        // The keywords `as`, `satisfies` and `of` never continue an
        // expression after a line terminator, so the read stays unguarded.
        for source in [
            format!("{read}\nas boolean && run()"),
            format!("{read}\nsatisfies boolean && run()"),
        ] {
            assert_eq!(keyword_guard(&source, read), None, "{source:?}");
        }
        // A slash after `)` or `}` divides, as in the previous token rule.
        let code = format!("if (x) /{read}/.test(y)");
        assert!(!in_ranges(
            &non_code_ranges(&code),
            code.find(read).unwrap()
        ));
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
    fn keyword_named_methods_are_never_guards() {
        let read = "process.env.FLAG";
        for terminator in LINE_TERMINATORS {
            for (keyword, _) in CONTROL_KEYWORDS {
                for gap in ["", " ", " /* c */ ", " // c\n", " // c\r"] {
                    let gap = format!("{gap}{terminator}");
                    let call = format!("{keyword}{gap}(a={read}){{}}");
                    for member in [
                        // Object literals in every expression position.
                        format!("const o={{{call}}}"),
                        format!("o={{a:1,{call}}}"),
                        format!("o={{a(){{}}{call}}}"),
                        format!("f({{{call}}})"),
                        format!("return {{{call}}}"),
                        format!("[{{{call}}}]"),
                        format!("x=y?{{{call}}}:z"),
                        format!("o={{get {call}}}"),
                        format!("o={{set {call}}}"),
                        format!("o={{async {call}}}"),
                        format!("o={{*{call}}}"),
                        format!("o={{async *{call}}}"),
                        // Class bodies, including call-expression heritage.
                        format!("class A{{{call}}}"),
                        format!("class A extends B{{{call}}}"),
                        format!("class A extends mix(B, {{x: 1}}){{{call}}}"),
                        format!("x=class{{{call}}}"),
                        format!("class A{{x=1;{call}}}"),
                        format!("class A{{m(){{}}{call}}}"),
                        format!("class A{{static {call}}}"),
                        format!("class A{{static async {call}}}"),
                        format!("class A{{#p=1;{call}}}"),
                    ] {
                        assert_eq!(keyword_guard(&member, read), None, "{member:?}");
                    }
                }
            }
        }
    }

    #[test]
    fn keywords_in_statement_blocks_stay_guards() {
        let read = "process.env.FLAG";
        for (keyword, evidence) in CONTROL_KEYWORDS {
            let arguments = keyword_arguments(keyword, read);
            for gap in ["", " ", " /* c */ ", " // c\n"] {
                let statement = format!("{keyword}{gap}({arguments}");
                for code in [
                    format!("function f(){{{statement}}}"),
                    format!("function f(a){{x();{statement}}}"),
                    format!("if(x){{{statement}}}"),
                    format!("if(x){{}}else{{{statement}}}"),
                    format!("try{{{statement}}}finally{{{statement}}}"),
                    format!("do{{{statement}}}while(y)"),
                    format!("const f=()=>{{{statement}}}"),
                    format!("const f=x=>{{{statement}}}"),
                    format!("class A{{m(){{{statement}}}}}"),
                    format!("class A{{static{{{statement}}}}}"),
                    format!("class A extends mix(B){{m(){{{statement}}}}}"),
                    format!("o={{m(){{{statement}}}}}"),
                    format!("o={{get v(){{{statement}}}}}"),
                    format!("switch(a){{case 1:{{{statement}}}}}"),
                    format!("switch(a){{default:{{{statement}}}}}"),
                    format!("x=1;{{{statement}}}"),
                    format!("{{}}{{{statement}}}"),
                    format!("o={{a:function(){{{statement}}}}}"),
                ] {
                    assert_eq!(keyword_guard(&code, read), Some(evidence), "{code:?}");
                }
            }
        }
    }

    /// A labeled block is a statement block, so a keyword that starts it is a
    /// control keyword and keeps its guard.
    #[test]
    fn labeled_blocks_are_statement_blocks() {
        let read = "process.env.FLAG";
        for (keyword, evidence) in CONTROL_KEYWORDS {
            let arguments = keyword_arguments(keyword, read);
            let code = format!("outer:{{{keyword}({arguments}}}");
            assert_eq!(keyword_guard(&code, read), Some(evidence), "{code:?}");
        }
    }

    /// Known limits: identifier escapes are not decoded, and source snippets
    /// split lines on LF and CRLF only.
    #[test]
    fn undecidable_lexical_cases_stay_conservative() {
        let read = "process.env.FLAG";
        for code in [
            format!("\\u0069f ({read}) run()"),
            format!("i\\u0066 ({read}) run()"),
        ] {
            assert_eq!(keyword_guard(&code, read), None, "{code:?}");
        }
        let (start, text) = crate::chunks::snippet_lines("a\u{2028}b\rc\nd", 1);
        assert_eq!((start, text.as_str()), (1, "a\u{2028}b\rc\nd"));
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
                // Combining marks, connector punctuation, ZWNJ and
                // `Other_ID_Continue` characters continue an identifier too.
                "a\u{301}",
                "\u{203f}",
                "a\u{200c}",
                "a\u{b7}",
                "\u{e33}",
                "\u{1d7d8}",
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
    fn combining_marks_keep_keywords_from_guarding() {
        let read = "process.env.FLAG";
        for terminator in LINE_TERMINATORS {
            for (keyword, _) in CONTROL_KEYWORDS {
                let arguments = keyword_arguments(keyword, read);
                for name in ["a\u{301}", "\u{203f}", "a\u{200c}", "a\u{200d}"] {
                    let code = format!("{name}{keyword} // c{terminator}({arguments}");
                    assert_eq!(keyword_guard(&code, read), None, "{code:?}");
                }
            }
            // A keyword-like suffix of a longer name before a comment is an
            // operand, so the next slash divides.
            let code = format!("a\u{301}else // c{terminator}/{read}/ 2");
            assert!(
                !in_ranges(&non_code_ranges(&code), code.find(read).unwrap()),
                "{code:?}"
            );
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

    const LINE_TERMINATORS: [&str; 5] = ["\n", "\r", "\r\n", "\u{2028}", "\u{2029}"];

    /// The previous token for the text before the marker `|` in `code`.
    fn previous_token_before_bar(code: &str) -> Token<'_> {
        let index = code.find('|').unwrap();
        let ignored = non_code_ranges(code);
        previous_significant_token(code, index, &ignored)
    }

    #[test]
    fn previous_token_skips_comments_and_every_line_terminator() {
        for terminator in LINE_TERMINATORS {
            let word = |text, member| Token::Word { text, member };
            let cases: Vec<(String, Token<'static>)> = vec![
                (String::new(), Token::Edge),
                (format!("// c{terminator}/* d */ |"), Token::Edge),
                (format!("a // c{terminator}|"), word("a", false)),
                (format!("a /* c{terminator}d */ |"), word("a", false)),
                (format!("else // c{terminator}|"), word("else", false)),
                (
                    format!("obj.return // c{terminator}|"),
                    word("return", true),
                ),
                ("obj?.typeof|".to_string(), word("typeof", true)),
                (format!("obj. // c{terminator}else|"), word("else", true)),
                (format!("obj.{terminator}in|"), word("in", true)),
                ("obj /* c */ . /* d */ do|".to_string(), word("do", true)),
                ("this.#new|".to_string(), word("new", true)),
                ("x = ...of|".to_string(), word("of", false)),
                ("1. in|".to_string(), word("in", false)),
                (format!("x.a{terminator}return|"), word("return", false)),
                (format!("x = 12{terminator}|"), Token::Number),
                (format!("x = 1.5 // c{terminator}|"), Token::Number),
                (format!("x = 'a' // c{terminator}|"), Token::Literal),
                ("x = \"a\"/* c */|".to_string(), Token::Literal),
                (format!("x = `a${{b}}c` // c{terminator}|"), Token::Literal),
                (format!("x = /a b/ // c{terminator}|"), Token::Literal),
                ("x = /a b/g|".to_string(), word("g", false)),
                ("x = `a${|".to_string(), Token::Punct("{")),
                (format!("f() // c{terminator}|"), Token::Punct(")")),
                (format!("a++ // c{terminator}|"), Token::Punct("++")),
                ("a -- /* c */|".to_string(), Token::Punct("--")),
                (format!("[...{terminator}|"), Token::Punct("...")),
                (format!("a = // c{terminator}|"), Token::Punct("=")),
                (format!("f = x =>{terminator}|"), Token::Punct(">")),
                ("a \u{e9}\u{bb}|".to_string(), Token::Punct("\u{bb}")),
            ];
            for (code, expected) in cases {
                let code = if code.contains('|') {
                    code
                } else {
                    format!("{code}|")
                };
                assert_eq!(previous_token_before_bar(&code), expected, "{code:?}");
            }
        }
    }

    #[test]
    fn next_token_skips_comments_and_classifies_literals() {
        for terminator in LINE_TERMINATORS {
            let word = |text| Token::Word {
                text,
                member: false,
            };
            let cases: Vec<(String, Token<'static>)> = vec![
                (String::new(), Token::Edge),
                (
                    format!("{terminator} // c{terminator}/* d */ "),
                    Token::Edge,
                ),
                (format!("{terminator}ready"), word("ready")),
                (format!("{terminator}// c{terminator}in x"), word("in")),
                (format!("{terminator}12"), Token::Number),
                (format!("{terminator}'a'"), Token::Literal),
                (format!("{terminator}`a`"), Token::Template),
                (format!("{terminator}/* c */ ++x"), Token::Punct("++")),
                (format!("{terminator}--x"), Token::Punct("--")),
                (format!("{terminator}+ +x"), Token::Punct("+")),
                (format!("{terminator}-x"), Token::Punct("-")),
                (format!("{terminator}.a"), Token::Punct(".")),
                (format!("{terminator}(a)"), Token::Punct("(")),
                (format!("{terminator}\u{e9}a"), word("\u{e9}a")),
            ];
            for (code, expected) in cases {
                let ignored = non_code_ranges(&code);
                assert_eq!(
                    next_significant_token(&code, 0, &ignored).1,
                    expected,
                    "{code:?}"
                );
            }
        }
    }

    #[test]
    fn a_block_after_an_arrow_expression_is_a_new_statement() {
        for terminator in LINE_TERMINATORS {
            for body in ["eval", "g(1)", "a.b", "[1]", "'s'", "1"] {
                for comment in ["", "// c", "/* c */"] {
                    // The line terminator ends the arrow body, so `eval` in
                    // the block is the global one.
                    let source = format!(
                        "const f = (eval) => {body}{terminator}{comment}{terminator}{{ eval(code) \
                         }}"
                    );
                    assert!(direct_eval_line(&source).is_some(), "{source:?}");
                    // After an operator the block is the arrow body.
                    let source = "const f = (eval) => { eval(code) }".to_string();
                    assert_eq!(direct_eval_line(&source), None, "{source:?}");
                    let source = format!(
                        "const f = (eval) =>{terminator}{comment}{terminator}{{ eval(code) }}"
                    );
                    assert_eq!(direct_eval_line(&source), None, "{source:?}");
                }
            }
        }
    }

    #[test]
    fn continued_arrow_bodies_keep_the_shadow_scope() {
        for terminator in LINE_TERMINATORS {
            // The next line starts with a binary operator, so the call is part
            // of the arrow body and `eval` is the parameter.
            for source in [
                format!("const f = (eval) => 1{terminator}+eval(code)"),
                format!("const f = eval => 1{terminator}+eval(code)"),
                format!("const f = (eval) => 1{terminator}-eval(code)"),
                format!("const f = (eval) => a{terminator}.b(eval(code))"),
                format!("const f = (eval) => a{terminator}|| eval(code)"),
                format!("const f = (eval) => a {terminator}// c{terminator}* eval(code)"),
                format!("const f = (eval) => a{terminator}[eval(code)]"),
                format!("const f = (eval) =>{terminator}eval(code)"),
                format!("const f = (eval) => 1 +{terminator}eval(code)"),
                format!("const f = eval => g(1,{terminator}eval(code))"),
            ] {
                assert_eq!(direct_eval_line(&source), None, "{source:?}");
            }
            // A statement boundary ends the body, so the next call is direct.
            for source in [
                format!("const f = (eval) => 1{terminator}++x; eval(code)"),
                format!("const f = (eval) => 1{terminator}--x\neval(code)"),
                format!("const f = (eval) => 1{terminator}x\neval(code)"),
                format!("const f = (eval) => 1{terminator}'s'; eval(code)"),
                format!("const f = (eval) => 1;{terminator}eval(code)"),
                "foo((eval) => eval(1), eval(2))".to_string(),
                format!("foo((eval) => eval(1)){terminator}eval(2)"),
                "x = [eval => eval(1)]; eval(2)".to_string(),
            ] {
                assert!(direct_eval_line(&source).is_some(), "{source:?}");
            }
        }
        let source = "foo((eval) => eval(1), eval(2))";
        assert_eq!(direct_eval_line(source), Some((0, 23)));
    }

    #[test]
    fn member_calls_named_eval_are_never_direct_across_comments() {
        for terminator in LINE_TERMINATORS {
            for source in [
                "obj.eval(code)".to_string(),
                "obj?.eval(code)".to_string(),
                format!("obj. // c{terminator}eval(code)"),
                format!("obj.{terminator}eval(code)"),
                format!("obj. /* c{terminator}d */ eval(code)"),
                format!("obj?. // c{terminator}eval(code)"),
                "obj /* a */ . /* b */ eval(code)".to_string(),
                format!("obj // a{terminator}.{terminator}eval(code)"),
                "this.#eval(code)".to_string(),
                format!("this. // c{terminator}#eval(code)"),
                format!("eval{terminator}`t`"),
                format!("eval // c{terminator}"),
                "eval?.(code)".to_string(),
            ] {
                assert_eq!(direct_eval_line(&source), None, "{source:?}");
            }
            // A call, including one with comments before the parenthesis or
            // after a spread operator, is direct.
            for source in [
                format!("eval // c{terminator}(code)"),
                "eval /* c */ (code)".to_string(),
                format!("eval{terminator}(code)"),
                "f(...eval(code))".to_string(),
                format!("[...{terminator}eval(code)]"),
                format!("x = 1.{terminator}+eval(code)"),
                format!("a; // obj.{terminator}eval(code)"),
            ] {
                assert!(direct_eval_line(&source).is_some(), "{source:?}");
            }
        }
        // Whole words only.
        for source in [
            "evaluate(code)",
            "x.eval2(code)",
            "reeval(code)",
            "éeval(code)",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
        }
    }

    #[test]
    fn identifier_parts_follow_ecmascript_id_continue() {
        for character in [
            'a',
            'Z',
            '0',
            '_',
            '$',
            '\u{e9}',
            '\u{4e2d}',
            '\u{301}',
            '\u{200c}',
            '\u{200d}',
            '\u{203f}',
            '\u{b7}',
            '\u{e33}',
            '\u{3099}',
            '\u{1d7d8}',
            '\u{ff9e}',
        ] {
            assert!(is_identifier_part(character), "{character:?}");
        }
        for character in [
            ' ',
            '-',
            '.',
            '#',
            '(',
            '\n',
            '\r',
            '\u{2028}',
            '\u{2029}',
            '\u{a0}',
            '\u{2e2e}',
            '\u{1f600}',
        ] {
            assert!(!is_identifier_part(character), "{character:?}");
        }
        // The definition matches the Unicode property over the whole BMP and
        // the first supplementary plane, apart from the three ECMAScript
        // additions.
        let property = Regex::new(r"^\p{ID_Continue}$").unwrap();
        for code in 0..0x2_0000u32 {
            let Some(character) = char::from_u32(code) else {
                continue;
            };
            let expected = property.is_match(&character.to_string())
                || matches!(character, '$' | '\u{200c}' | '\u{200d}');
            assert_eq!(is_identifier_part(character), expected, "{code:#x}");
        }
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
    fn eval_shadow_scopes_end_at_every_line_terminator() {
        for terminator in LINE_TERMINATORS {
            // An expression-bodied arrow ends at the line terminator, so the
            // next line's eval is a real direct eval.
            for source in [
                format!("const f = (eval) => eval(1){terminator}eval(2)"),
                format!("const f = eval => eval(1){terminator}eval(2)"),
            ] {
                let (line, _) = direct_eval_line(&source).expect(&source);
                assert_eq!(line, 1, "{source:?}");
            }
            // `return` followed by a line terminator ends the statement, so
            // the function is a declaration scoped to its block.
            let source = format!(
                "function outer() {{ return{terminator}function eval() {{}} }} eval(code);"
            );
            assert!(direct_eval_line(&source).is_some(), "{source:?}");
            // A `case` label is recognized after any terminator.
            let source =
                format!("switch (a) {{ case 1:{terminator}function eval() {{}} }} eval(code);");
            assert!(direct_eval_line(&source).is_some(), "{source:?}");
        }
    }

    #[test]
    fn keyword_unary_parentheses_keep_the_enclosing_guard() {
        let guard = |code: &str, read: &str| {
            let start = code.find(read).unwrap();
            env_guard(code, start, start + read.len(), &non_code_ranges(code))
                .map(EnvGuard::evidence)
        };
        let read = "process.env.FLAG";
        let vite_read = "({}).FLAG";
        for read in [read, vite_read] {
            for operator in ["typeof", "await", "void", "delete"] {
                for (statement, evidence) in [
                    (
                        "if ({operator}({read}) === \"undefined\") run()",
                        "the retained read is in an if condition",
                    ),
                    (
                        "if ({operator} ({read})) run()",
                        "the retained read is in an if condition",
                    ),
                    (
                        "if (!{operator}({read})) run()",
                        "the retained read is in an if condition",
                    ),
                    (
                        "if (({operator}({read}))) run()",
                        "the retained read is in an if condition",
                    ),
                    (
                        "if ({operator}/* c */({read})) run()",
                        "the retained read is in an if condition",
                    ),
                    (
                        "if ({operator} // c\n({read})) run()",
                        "the retained read is in an if condition",
                    ),
                    (
                        "while ({operator}({read})) run()",
                        "the retained read is in a while condition",
                    ),
                    (
                        "switch ({operator}({read})) {default: run()}",
                        "the retained read is in a switch condition",
                    ),
                ] {
                    // The enclosing `async` function makes `await` a keyword.
                    let code = format!(
                        "async function f() {{ {} }}",
                        statement
                            .replace("{operator}", operator)
                            .replace("{read}", read)
                    );
                    // The branch evidence follows the operand's value: `await`
                    // passes it on, `typeof` yields a type name that only an
                    // equality test reads, and `void` and `delete` drop it.
                    let expected = match operator {
                        "await" => Some(evidence),
                        "typeof" if statement.contains("===") => Some(evidence),
                        _ => None,
                    };
                    assert_eq!(guard(&code, read), expected, "{code:?}");
                }
                for code in [
                    "const value = {operator}({read});",
                    "const value = {operator} ({read});",
                    "run({operator}({read}))",
                    "if (check({operator}({read}))) run()",
                    "for ({operator}({read}); ; ) run()",
                    "for (; ; {operator}({read})) run()",
                ] {
                    let code = format!(
                        "async function f() {{ {} }}",
                        code.replace("{operator}", operator).replace("{read}", read)
                    );
                    assert_eq!(guard(&code, read), None, "{code:?}");
                }
            }
            // The test of a `for` loop keeps its for-condition evidence.
            let code = format!("for (; typeof({read}) === 'undefined'; ) run()");
            assert_eq!(
                guard(&code, read),
                Some("the retained read is in a for condition")
            );
            // Ordinary calls and property or identifier lookalikes are not
            // keyword operators.
            for call in [
                "check",
                "obj.typeof",
                "mytypeof",
                "$typeof",
                "_typeof",
                "obj.await",
                "myawait",
                "obj.void",
                "obj.delete",
                "undelete",
            ] {
                let code = format!("if ({call}({read})) run()");
                assert_eq!(guard(&code, read), None, "{code:?}");
                let code = format!("const value = {call}({read});");
                assert_eq!(guard(&code, read), None, "{code:?}");
            }
        }
        // Existing behavior: operands without parentheses and symbolic unary
        // operators are unchanged.
        assert_eq!(
            guard(
                "if (typeof process.env.FLAG === 'undefined') run()",
                "process.env.FLAG"
            ),
            Some("the retained read is in an if condition")
        );
        assert_eq!(
            guard("if (!(process.env.FLAG)) run()", "process.env.FLAG"),
            Some("the retained read is in an if condition")
        );
        assert_eq!(
            guard("const value = typeof process.env.FLAG;", "process.env.FLAG"),
            None
        );
    }

    /// Wraps `statement` in each syntactic context that decides whether
    /// `await` is the unary operator. `typeof`, `void` and `delete` are
    /// operators in all of them.
    fn keyword_contexts(statement: &str) -> [(&'static str, String, bool); 3] {
        [
            ("classic script", statement.to_string(), false),
            (
                "async function",
                format!("async function f() {{ {statement} }}"),
                true,
            ),
            ("module", format!("export {{}};\n{statement}"), true),
        ]
    }

    #[test]
    fn keyword_unary_operators_follow_the_script_or_module_context() {
        let guard = |code: &str, read: &str| {
            let start = code.find(read).unwrap();
            env_guard(code, start, start + read.len(), &non_code_ranges(code))
                .map(EnvGuard::evidence)
        };
        let read = "process.env.FLAG";
        let condition = Some("the retained read is in an if condition");
        for operator in UNARY_KEYWORDS {
            for statement in [
                format!("if ({operator}({read})) run()"),
                format!("if ({operator} ({read})) run()"),
            ] {
                for (context, code, is_operator_context) in keyword_contexts(&statement) {
                    // Only `await` depends on the context: it is an ordinary
                    // call in a classic script outside an async function.
                    // `typeof`, `void` and `delete` do not pass their operand
                    // on, so the read guards nothing even as an operator.
                    let expected = if operator != "await" || !is_operator_context {
                        None
                    } else {
                        condition
                    };
                    assert_eq!(guard(&code, read), expected, "{context}: {code:?}");
                }
            }
            // Property and identifier lookalikes are ordinary calls in every
            // context.
            for lookalike in [
                format!("obj.{operator}"),
                format!("obj?.{operator}"),
                format!("obj. /* c */ {operator}"),
                format!("obj.\n{operator}"),
                format!("this.#{operator}"),
                format!("my{operator}"),
                format!("{operator}2"),
            ] {
                let statement = format!("if ({lookalike}({read})) run()");
                for (context, code, _) in keyword_contexts(&statement) {
                    assert_eq!(guard(&code, read), None, "{context}: {code:?}");
                }
            }
        }
    }

    #[test]
    fn await_is_an_operator_only_in_proven_module_or_async_contexts() {
        let guard = |code: &str, read: &str| {
            let start = code.find(read).unwrap();
            env_guard(code, start, start + read.len(), &non_code_ranges(code))
                .map(EnvGuard::evidence)
        };
        let read = "process.env.FLAG";
        let condition = Some("the retained read is in an if condition");
        let gate = format!("if (await({read})) run()");
        // Contexts that make `await` a reserved word.
        for code in [
            format!("async function f() {{ {gate} }}"),
            format!("const f = async function () {{ {gate} }}"),
            format!("const f = async function named() {{ {gate} }}"),
            format!("async function* f() {{ {gate} }}"),
            format!("const f = async () => {{ {gate} }}"),
            format!("const f = async (a, b) => {{ {gate} }}"),
            format!("const f = async x => {{ {gate} }}"),
            format!("foo(async () => {{ {gate} }})"),
            format!("const o = {{ async m() {{ {gate} }} }}"),
            format!("class A {{ async m(a) {{ {gate} }} static async s() {{ {gate} }} }}"),
            format!("class A {{ async *g() {{ {gate} }} }}"),
            format!("const o = {{ async [key](a) {{ {gate} }} }}"),
            format!("async function f() {{ if (a) {{ for (;;) {{ {gate} }} }} }}"),
            format!("async function f() {{ try {{ {gate} }} catch (e) {{}} }}"),
            format!("async function f() {{ const o = {{ k: 1 }}; {gate} }}"),
            format!("async function f() {{ g(function () {{}}, 1); {gate} }}"),
            format!("async function f() {{ /* ) {{ */ {gate} }}"),
            format!("async function f() {{ const s = '){{'; {gate} }}"),
            format!("export {{}};\n{gate}"),
            format!("export default function f() {{ {gate} }}"),
            format!("import a from 'a';\nfunction f() {{ {gate} }}"),
            format!("import {{ a }} from 'a';\nfunction f() {{ {gate} }}"),
            format!("import * as a from 'a';\nfunction f() {{ {gate} }}"),
            format!("import 'a';\nfunction f() {{ {gate} }}"),
            format!("const url = import.meta.url;\nfunction f() {{ {gate} }}"),
            format!("function f() {{ {gate} }} export {{}}"),
        ] {
            assert_eq!(guard(&code, read), condition, "{code:?}");
        }
        // Classic script code, where `await` is an identifier.
        for code in [
            gate.clone(),
            format!("function f() {{ {gate} }}"),
            format!("function f(await) {{ {gate} }}"),
            format!("const f = function () {{ {gate} }}"),
            format!("const f = () => {{ {gate} }}"),
            format!("const f = x => {{ {gate} }}"),
            format!("const f = async;\nconst g = () => {{ {gate} }}"),
            format!("const o = {{ m() {{ {gate} }} }}"),
            format!("class A {{ m() {{ {gate} }} get v() {{ {gate} }} }}"),
            format!("const o = {{ async: 1, m() {{ {gate} }} }}"),
            format!("const o = {{ get async() {{ {gate} }} }}"),
            format!("obj.async(function () {{ {gate} }})"),
            format!("var await = f; {gate}"),
            format!("async function f() {{ function g() {{ {gate} }} }}"),
            format!("async function f() {{ const g = () => {{ {gate} }} }}"),
            format!("async function f() {{ class A {{ m() {{ {gate} }} }} }}"),
            format!("async function f() {{}}\n{gate}"),
            format!("const f = async () => 1;\n{gate}"),
            format!("const f = async () => 1, g = () => {{ {gate} }}"),
            // Text that only looks like module syntax or async functions.
            format!("const s = 'export default 1';\n{gate}"),
            format!("const s = \"import a from 'a'\";\n{gate}"),
            format!("// export const x = 1;\n{gate}"),
            format!("/* import a from 'a'; */\n{gate}"),
            format!("const s = `\nexport {{}}\n`;\n{gate}"),
            format!("const p = import('a');\n{gate}"),
            format!("const x = a.export;\n{gate}"),
            format!("const s = 'async function f() {{';\n{gate}"),
            format!("/* async function f() {{ */ {gate}"),
        ] {
            assert_eq!(guard(&code, read), None, "{code:?}");
            // The same layout with `!` keeps its guard, so the missing
            // guard above comes from `await` being an identifier.
            let operator_code = code.replace("await(", "!(");
            assert_eq!(guard(&operator_code, read), condition, "{operator_code:?}");
        }
    }

    const AWAIT_READ: &str = "process.env.FLAG";
    const AWAIT_CONDITION: &str = "the retained read is in an if condition";

    fn await_guard(code: &str) -> Option<&'static str> {
        let start = code.find(AWAIT_READ).unwrap();
        env_guard(
            code,
            start,
            start + AWAIT_READ.len(),
            &non_code_ranges(code),
        )
        .map(EnvGuard::evidence)
    }

    /// `await(` is an ordinary call in `code`. The same layout with `!`
    /// keeps its guard, so the missing guard comes from the classification.
    fn assert_await_is_a_call(code: &str) {
        assert_eq!(await_guard(code), None, "{code:?}");
        let operator_code = code.replace("await(", "!(");
        assert_eq!(
            await_guard(&operator_code),
            Some(AWAIT_CONDITION),
            "{operator_code:?}"
        );
    }

    fn assert_await_is_an_operator(code: &str) {
        assert_eq!(await_guard(code), Some(AWAIT_CONDITION), "{code:?}");
    }

    fn await_gate() -> String {
        format!("if (await({AWAIT_READ})) run()")
    }

    #[test]
    fn module_words_after_a_member_access_do_not_make_a_module() {
        let gate = await_gate();
        for prelude in [
            "const u = obj.import.meta;",
            "const u = obj?.import.meta;",
            "const u = obj\n  .import\n  .meta;",
            "const u = obj. /* c */ import /* d */ . meta;",
            "const u = obj?. /* c */ import . meta;",
            "const o = { import: 1, export: 2 };",
            "class A { import() {} export() {} }",
            "const p = import('a');",
            "const e = obj.export;",
        ] {
            assert_await_is_a_call(&format!("{prelude}\n{gate}"));
            assert_await_is_a_call(&format!("{prelude}\nfunction f() {{ {gate} }}"));
        }
    }

    #[test]
    fn comments_between_module_keywords_keep_the_module_context() {
        let gate = await_gate();
        for module_syntax in [
            "export {};",
            "export /* c */ {};",
            "export // c\n{};",
            "export\n{};",
            "export /* c */ * from 'a';",
            "export/**/*/**/from 'a';",
            "export /* c */ default 1;",
            "export /* c */ const x = 1;",
            "export /* c */ function g() {}",
            "import a from 'a';",
            "import /* c */ a from 'a';",
            "import /* c */ { a } from 'a';",
            "import/**/{ a } from 'a';",
            "import // c\n{ a } from 'a';",
            "import /* c */ * as a from 'a';",
            "import /* c */ 'a';",
            "const u = import.meta.url;",
            "const u = import . meta . url;",
            "const u = import /* c */ . /* d */ meta;",
            "const u = import\n.meta;",
        ] {
            assert_await_is_an_operator(&format!("{module_syntax}\n{gate}"));
            assert_await_is_an_operator(&format!("{module_syntax}\nfunction f() {{ {gate} }}"));
            // A statement or assignment after the guard would end its scan, so
            // only declarations without either follow it.
            let trailing = module_syntax.trim_end_matches(';');
            if !trailing.contains('=') {
                assert_await_is_an_operator(&format!("function f() {{ {gate} }} {trailing}"));
            }
        }
    }

    #[test]
    fn async_followed_by_a_line_break_is_not_an_async_modifier() {
        let gate = await_gate();
        for code in [
            format!("var async; async\nfunction f(await) {{ {gate} }}"),
            format!("var async; async\nfunction* f() {{ {gate} }}"),
            format!("var async; async\nx => {{ {gate} }}"),
            format!("var async; async /*\n*/ function f(await) {{ {gate} }}"),
            format!("var async; async // c\nfunction f(await) {{ {gate} }}"),
            format!("class A {{ async\nm() {{ {gate} }} }}"),
            format!("class A {{ async\n*g() {{ {gate} }} }}"),
            format!("class A {{ static async\nm() {{ {gate} }} }}"),
        ] {
            assert_await_is_a_call(&code);
        }
        for code in [
            format!("async /* c */ function f() {{ {gate} }}"),
            format!("async\tfunction f() {{ {gate} }}"),
            format!("async /* c */ x => {{ {gate} }}"),
            format!("async /* c */ (x) => {{ {gate} }}"),
            format!("class A {{ async /* c */ m() {{ {gate} }} }}"),
        ] {
            assert_await_is_an_operator(&code);
        }
    }

    #[test]
    fn string_and_private_method_names_keep_the_async_modifier() {
        let gate = await_gate();
        for code in [
            format!("const o = {{ async \"load\"() {{ {gate} }} }}"),
            format!("const o = {{ async 'load'() {{ {gate} }} }}"),
            format!("const o = {{ async 42() {{ {gate} }} }}"),
            format!("const o = {{ async *\"gen\"() {{ {gate} }} }}"),
            format!("class C {{ async #load() {{ {gate} }} }}"),
            format!("class C {{ static async #load() {{ {gate} }} }}"),
            format!("class C {{ async \"load\"() {{ {gate} }} }}"),
            format!("class C {{ static async 'a b'() {{ {gate} }} }}"),
            format!("class C {{ async *#gen() {{ {gate} }} }}"),
            format!("class C {{ async *\"gen\"() {{ {gate} }} }}"),
            format!("class C {{ async get() {{ {gate} }} }}"),
            format!("class C {{ async static() {{ {gate} }} }}"),
            format!("class C {{ async [key]() {{ {gate} }} }}"),
            format!("class C extends mix(B) {{ async #load() {{ {gate} }} }}"),
        ] {
            assert_await_is_an_operator(&code);
        }
        for code in [
            format!("const o = {{ \"load\"() {{ {gate} }} }}"),
            format!("const o = {{ 42() {{ {gate} }} }}"),
            format!("class C {{ #load() {{ {gate} }} }}"),
            format!("class C {{ static \"load\"() {{ {gate} }} }}"),
            format!("class C {{ async; #load() {{ {gate} }} }}"),
            format!("class C {{ async() {{ {gate} }} }}"),
            format!("class C {{ static async() {{ {gate} }} }}"),
            format!("class C {{ async = 1; \"load\"() {{ {gate} }} }}"),
            format!("class C {{ get #p() {{ {gate} }} }}"),
            format!("class C extends mix(B) {{ #load() {{ {gate} }} }}"),
        ] {
            assert_await_is_a_call(&code);
        }
    }

    #[test]
    fn a_concise_arrow_body_ends_at_a_line_break_that_starts_a_statement() {
        let gate = await_gate();
        for prelude in [
            "const g = async () => 1",
            "const g = async x => x",
            "const g = async (a, b) => a + b",
            "const g = async () => f(1)",
            "const g = async () => [1, 2]",
            "const g = async () => c ? 1 : 2",
            "const g = async () => ({})",
            "const g = async () => 'x'",
            "const g = async () => `x`",
            "const g = async x => y => 1",
            "const g = async () => 1 // note",
            "const g = async () => 1 /* note */",
            "const g = async () => x++",
            "const h = c ? async () => 1 : 2",
            "const o = { a: async () => 1, b: 2 }",
            "foo(async () => 1)",
            "foo(async () => 1, async () => 2)",
            "const g = async () => 1\nconst h = async () => 2",
        ] {
            assert_await_is_a_call(&format!("{prelude}\n{gate}"));
            assert_await_is_a_call(&format!("{prelude};\n{gate}"));
            assert_await_is_a_call(&format!("{prelude}\nfunction f() {{ {gate} }}"));
        }
    }

    /// Offsets of the `await(` operators that the classifier finds in `code`.
    fn await_operators_in(code: &str) -> Vec<usize> {
        await_operator_offsets(code, &non_code_ranges(code))
    }

    #[test]
    fn a_concise_async_arrow_body_continues_across_line_breaks() {
        for code in [
            "const g = async () => await(x) && run()",
            "const g = async () =>\n  await(x) && run()",
            "const g = async () => 1 +\n  (await(x) && run())",
            "const g = async () => f\n  (await(x) && run())",
            "const g = async () => c\n  ? 1\n  : (await(x) && run())",
            "const g = async () => a.b\n  .c(await(x))",
            "foo(async () => 1, async () => await(x) && run())",
            "const g = c ? async () => 1 : async () => await(x) && run()",
            "const g = async () => c ? 1 : a ? 2 : await(x) && run()",
            "const g = async () => `a${await(x)}`",
            "const g = async () => 1 // note\n  + await(x)",
            "const g = async () => {} \n; foo(async () => await(x))",
        ] {
            assert_eq!(await_operators_in(code).len(), 1, "{code:?}");
        }
        for code in [
            "const g = () => await(x) && run()",
            "const g = async x => y => await(x) && run()",
            "const g = async () => () => await(x)",
            "const g = async () => 1\nawait(x) && run()",
            "const g = async () => c ? 1 : 2\nawait(x) && run()",
            "const g = async () => 1;\nawait(x) && run()",
            "const g = async () => 1\n+await(x)",
            "const g = async () => 1\nfoo(async () => 2)\nawait(x)",
            "const g = async () => c ? 1 : 2, h = await(x)",
            "foo(async () => 1, await(x))",
            "const o = { a: async () => 1, b: await(x) }",
            "const g = c ? async () => 1 : await(x)",
            "const g = async () => `a${1}`\nawait(x)",
        ] {
            // `+await(x)` after a line break continues the expression, so the
            // arrow body is still open there.
            let expected = usize::from(code.contains("1\n+await"));
            assert_eq!(await_operators_in(code).len(), expected, "{code:?}");
        }
    }

    /// Operators and keywords that continue an expression from the next line,
    /// so a concise arrow body is still open when one of them starts a line.
    const LINE_START_CONTINUATIONS: [&str; 41] = [
        "??",
        "??=",
        "?.",
        "||",
        "&&",
        "||=",
        "&&=",
        "|",
        "&",
        "^",
        "*",
        "**",
        "/",
        "%",
        "+",
        "-",
        "<",
        ">",
        "<=",
        ">=",
        "<<",
        ">>",
        ">>>",
        "==",
        "===",
        "!=",
        "!==",
        "=",
        "+=",
        "-=",
        "?",
        ":",
        ".",
        "in",
        "instanceof",
        "(",
        "[",
        "`t`",
        "*=",
        "%=",
        "^=",
    ];

    #[test]
    fn each_line_start_continuation_keeps_a_concise_async_arrow_open() {
        for operator in LINE_START_CONTINUATIONS {
            // The operand must be valid after every operator, so wrap it in
            // parentheses and give a `?` or `:` its other branch.
            let operand = match operator {
                "?" => "(await(x) ? 2 : 3) : 4",
                ":" => "(await(x))",
                "(" | "[" | "`t`" | "." | "?." => "await(x)",
                _ => "(await(x) ? 2 : 3)",
            };
            let code = match operator {
                "(" => format!("const g = async () => f\n  ({operand})"),
                "[" => format!("const g = async () => f\n  [{operand}]"),
                "`t`" => format!("const g = async () => f\n  `${{{operand}}}`"),
                "." => format!("const g = async () => a\n  .b({operand})"),
                "?." => format!("const g = async () => a\n  ?.b({operand})"),
                "?" => format!("const g = async () => c\n  ? {operand}"),
                ":" => format!("const g = async () => c ? 1\n  : {operand}"),
                _ => format!("const g = async () => a\n  {operator} {operand}"),
            };
            assert_eq!(await_operators_in(&code).len(), 1, "{operator:?}: {code:?}");
            // Comments between the line break and the operator change nothing.
            let commented = code.replacen("\n  ", " // c\n  /* d */ ", 1);
            assert_eq!(
                await_operators_in(&commented).len(),
                1,
                "{operator:?}: {commented:?}"
            );
        }
    }

    #[test]
    fn nullish_coalescing_on_the_next_line_keeps_the_async_arrow_guard() {
        // The scenario of the review: `??` continues the concise arrow body,
        // so `await(` is the operator and the read keeps its guard.
        for code in [
            "const g = async () => 1\n  ?? (await(process.env.FLAG) ? 2 : 3)",
            "const g = async () => 1\n  ?? 2\n  ?? (await(process.env.FLAG) ? 2 : 3)",
            "var await = f; const g = async () => 1\n  ?? (await(process.env.FLAG) ? 2 : 3)",
        ] {
            let operator_code = code.replace("await(", "!(");
            let expected = await_guard(&operator_code);
            assert!(expected.is_some(), "{operator_code:?}");
            assert_eq!(await_guard(code), expected, "{code:?}");
            assert_eq!(await_operators_in(code).len(), 1, "{code:?}");
        }
        // A new statement after the arrow body still makes `await(` a call.
        for code in [
            "var await = f; const g = async () => 1;\n?? (await(process.env.FLAG) ? 2 : 3)",
            "var await = f; const g = async () => 1\n!(await(process.env.FLAG) ? 2 : 3)",
        ] {
            let operator_code = code.replace("await(", "!(");
            assert!(await_guard(&operator_code).is_some(), "{operator_code:?}");
            assert_eq!(await_operators_in(code).len(), 0, "{code:?}");
        }
    }

    #[test]
    fn every_await_operand_reuses_one_classification_per_chunk() {
        let mut code = String::new();
        for index in 0..1500 {
            code.push_str(&format!(
                "function s{index}() {{ if (await(process.env.FLAG_{index})) run() }} async \
                 function a{index}() {{ if (await(process.env.FLAG_{index})) run() }} "
            ));
        }
        let ignored = non_code_ranges(&code);
        let operators = await_operator_offsets(&code, &ignored);
        assert_eq!(operators.len(), 1500);
        assert!(operators.windows(2).all(|pair| pair[0] < pair[1]));
        let reads: Vec<(usize, usize)> = Regex::new(r"process\.env\.FLAG_\d+")
            .unwrap()
            .find_iter(&code)
            .map(|found| (found.start(), found.end()))
            .collect();
        assert_eq!(env_guards(&code, &reads, &ignored).len(), 3000);
        // Module syntax at the end of the chunk makes every operand an
        // operator.
        code.push_str("export {}");
        let ignored = non_code_ranges(&code);
        assert_eq!(await_operator_offsets(&code, &ignored).len(), 3000);
    }

    #[test]
    fn a_block_after_a_call_statement_keeps_the_enclosing_async_context() {
        let gate = await_gate();
        // Each prefix ends a statement; the brace after it starts a block, not
        // a method body.
        for prefix in [
            "foo()",
            "foo.bar()",
            "foo(a, b)",
            "foo[\"x\"]()",
            "foo[x]()",
            "new Foo()",
        ] {
            for terminator in LINE_TERMINATORS {
                for code in [
                    format!("async function outer() {{ {prefix}{terminator}{{ {gate} }} }}"),
                    format!("export {{}};{terminator}{prefix}{terminator}{{ {gate} }}"),
                ] {
                    assert_await_is_an_operator(&code);
                }
                // The same shape in an object literal or class body declares
                // a method, which is synchronous.
                let name = prefix.trim_end_matches("()");
                if name == "foo" {
                    for code in [
                        format!(
                            "async function outer() {{ const o = {{ foo(){terminator}{{ {gate} }} \
                             }} }}"
                        ),
                        format!(
                            "async function outer() {{ class C {{ foo(){terminator}{{ {gate} }} \
                             }} }}"
                        ),
                    ] {
                        assert_eq!(await_guard(&code), None, "{code:?}");
                    }
                }
            }
        }
    }

    #[test]
    fn method_name_separators_do_not_apply_to_unary_operators_outside_members() {
        let read = AWAIT_READ;
        for operator in UNARY_KEYWORDS {
            for separator in [",", "*", ", /* c */", " *\n"] {
                // An expression: the separator precedes an operand.
                let expression =
                    format!("if ((sideEffect(){separator} {operator}({read}) === 1)) run()");
                for (context, code, _) in keyword_contexts(&expression) {
                    if operator == "await" && context == "classic script" {
                        continue;
                    }
                    // `void` and `delete` drop the operand's value, while the
                    // equality test reads the operand of `typeof`.
                    let expected = match operator {
                        "void" | "delete" => None,
                        _ => Some(AWAIT_CONDITION),
                    };
                    assert_eq!(await_guard(&code), expected, "{context}: {code:?}");
                }
            }
        }
    }

    #[test]
    fn keyword_named_methods_and_unparsed_headers_keep_await_a_call() {
        let gate = await_gate();
        // A method named like a statement keyword is not a statement block, so
        // it does not inherit the surrounding `async` function.
        for code in [
            format!("async function outer() {{ const o = {{ if(a) {{ {gate} }} }}; }}"),
            format!("async function outer() {{ class C {{ while(a) {{ {gate} }} }} }}"),
            format!("async function outer() {{ class C {{ static for(a) {{ {gate} }} }} }}"),
            // Known limit: a labeled block looks like an object literal.
            format!("async function outer() {{ out: {{ if (a) {{ {gate} }} }} }}"),
            // Known limit: identifier escapes are not decoded.
            format!("async function f() {{ if (aw\\u0061it({AWAIT_READ})) run() }}"),
            format!("\\u0061sync function f() {{ {gate} }}"),
            format!("const f = \\u0061sync () => {{ {gate} }}"),
            // Known limit: a return type annotation hides the function header.
            format!("async function f(): Promise<void> {{ {gate} }}"),
        ] {
            assert_eq!(await_guard(&code), None, "{code:?}");
        }
        for code in [
            format!("async function outer() {{ if (a) {{ {gate} }} }}"),
            format!("async function outer() {{ while (a) {{ {gate} }} }}"),
            format!("async function outer() {{ for (;;) {{ {gate} }} }}"),
            format!("async function outer() {{ switch (a) {{ default: {gate} }} }}"),
            format!("async function outer() {{ try {{ x() }} catch (e) {{ {gate} }} }}"),
            format!(
                "async function outer() {{ class C {{ m() {{ if (a) {{ run() }} }} }} {gate} }}"
            ),
            format!("class C {{ async if(a) {{ {gate} }} }}"),
            format!("class C {{ static async for(a) {{ {gate} }} }}"),
            format!("class C {{ async *while(a) {{ {gate} }} }}"),
        ] {
            assert_await_is_an_operator(&code);
        }
    }

    #[test]
    fn keyword_unary_names_with_unicode_identifier_parts_are_not_operators() {
        let guard = |code: &str, read: &str| {
            let start = code.find(read).unwrap();
            env_guard(code, start, start + read.len(), &non_code_ranges(code))
                .map(EnvGuard::evidence)
        };
        let read = "process.env.FLAG";
        // Every ECMAScript IdentifierPart class that can sit directly before
        // the keyword letters: ASCII and Unicode letters, decimal digits,
        // combining marks, connector punctuation, `Other_ID_Continue`, `$`,
        // `_` and the zero-width joiners.
        let identifier_parts = [
            "a", "Z", "5", "_", "$", "\u{3bb}", "\u{e9}", "\u{4e2d}", "\u{663}", "\u{301}",
            "\u{93e}", "\u{203f}", "\u{b7}", "\u{200c}", "\u{200d}",
        ];
        // Text that ends before the keyword without continuing an identifier,
        // so the keyword stands alone as an operator.
        let separators = [
            "if (",
            "if ( ",
            "if (!",
            "if ((",
            "if (a ||\n",
            "if (a ||\u{a0}",
            "if (a ||\u{2028}",
            "if (a ||/* c */",
        ];
        for operator in UNARY_KEYWORDS {
            // The enclosing `async` function makes `await` a keyword, so the
            // only difference between the cases is the preceding text.
            let wrap = |statement: String| format!("async function f() {{ {statement} }}");
            for part in identifier_parts {
                for statement in [
                    format!("if ({part}{operator}({read})) run()"),
                    format!("if (obj.{part}{operator}({read})) run()"),
                    format!("while ({part}{operator} ({read})) run()"),
                    format!("switch (a{part}{operator}({read})) {{default: run()}}"),
                ] {
                    let code = wrap(statement);
                    assert_eq!(guard(&code, read), None, "{code:?}");
                }
            }
            for separator in separators {
                let code = wrap(format!("{separator}{operator}({read})) run()"));
                // Only `await` passes its operand on as the branch value.
                let expected =
                    (operator == "await").then_some("the retained read is in an if condition");
                assert_eq!(guard(&code, read), expected, "{code:?}");
            }
        }
        // The same identifier-part sweep applies to the control keywords.
        for keyword in ["if", "while", "switch", "for"] {
            for part in identifier_parts {
                let code = format!("{part}{keyword}({read}) run()");
                assert_eq!(guard(&code, read), None, "{code:?}");
            }
        }
    }
}
