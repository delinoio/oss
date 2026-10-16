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
    collect::CollectedModule,
    model::{Condition, ConditionOutcome, Evidence, EvidenceStatus},
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
            "return" | "typeof" | "case" | "void" | "yield" | "throw" | "new" | "in" | "of"
            | "delete" | "await" | "instanceof" | "default" => true,
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
            return true;
        }
        if in_ranges(ignored, position) {
            continue;
        }
        match character {
            ')' | ']' => depth += 1,
            '(' | '[' => match depth.checked_sub(1) {
                Some(next) => depth = next,
                None => return false,
            },
            // The `>` of an arrow (`=>`) never closes a type-argument list.
            '>' if depth == 0 && !code[..position].ends_with('=') => {
                if let Some(open) = type_arguments_open(code, position, ignored) {
                    position = open;
                }
            }
            '{' | '}' | ';' if depth == 0 => return false,
            '=' if depth == 0 && stop_at_assignment => return false,
            _ => {}
        }
    }
    word_end.is_some_and(|finish| depth == 0 && is_class(0, finish))
}

/// The offset of the `<` that opens the type-argument list closed by the `>` at
/// `close`, or `None` when the `>` is an operator (`a > b`, `x >= y`) because
/// no balanced `<` precedes it in the same statement.
///
/// The scan walks backwards over characters. Brackets, parentheses and braces
/// inside the list (`T extends { a: Array<1> }`) must balance; an unmatched
/// opener or a top-level `;` means the `>` is not a closing angle bracket.
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
                    return Some(position);
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

/// Keywords after which an object literal can start.
const OBJECT_VALUE_KEYWORDS: [&str; 5] = ["return", "default", "yield", "await", "throw"];

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
                "=" | "(" | "," | "[" | "?" | "|" | "&" => true,
                // A property value (`{ key: { eval(x) {} } }`); a label or a
                // `case` clause is a statement block and stays unplaced.
                ":" => self
                    .direct_enclosing_block(open)
                    .is_some_and(|(outer, _)| self.brace_kind(outer) == BraceKind::ObjectLiteral),
                _ => false,
            },
            Token::Word { text, member } => !member && OBJECT_VALUE_KEYWORDS.contains(&text),
            _ => false,
        };
        if is_object {
            BraceKind::ObjectLiteral
        } else {
            BraceKind::Other
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
                Token::Punct(")" | "]") | Token::Literal | Token::Number | Token::Template => {
                    return kind == BraceKind::ClassBody
                        && self.new_line_between(token_end, member_start);
                }
                Token::Word {
                    text,
                    member: false,
                } => {
                    return kind == BraceKind::ClassBody
                        && !REGEX_KEYWORDS.contains(&text)
                        && self.new_line_between(token_end, member_start);
                }
                _ => return false,
            }
        }
    }

    fn new_line_between(&self, start: usize, end: usize) -> bool {
        self.source[start..end].contains(is_line_terminator)
    }

    /// Whether the `eval` at `start..end` names a method: it sits directly in
    /// a class body or object literal at a member position, and its parameter
    /// list is followed by a body (after an optional TypeScript return type).
    /// When the scan cannot tell, the name is treated as a call.
    fn declares_method(&self, start: usize, end: usize) -> bool {
        let (open_paren, token) = next_significant_token(self.source, end, self.ignored);
        if token != Token::Punct("(") {
            return false;
        }
        let bytes = self.code.as_bytes();
        let Some(close_paren) = matching_delimiter(bytes, open_paren, b'(', b')') else {
            return false;
        };
        if function_body_block(&self.code, bytes, &self.blocks, close_paren + 1).is_none() {
            return false;
        }
        let Some((open, _)) = self.direct_enclosing_block(start) else {
            return false;
        };
        let kind = self.brace_kind(open);
        kind != BraceKind::Other && self.at_member_position(start, kind)
    }
}

fn direct_eval_line(source: &str) -> Option<(u32, u32)> {
    // `eval(` inside strings, templates, comments or regular expressions is
    // not a call.
    let ignored = non_code_ranges(source);
    // A binding named `eval` makes calls inside its scope ordinary calls that
    // cannot observe the surrounding scope; shadowing is decided per call.
    let shadows = eval_shadow_scopes(source, &ignored);
    let mut declarations: Option<MemberScan<'_>> = None;
    let start = source.match_indices("eval").find_map(|(start, name)| {
        let end = start + name.len();
        let whole_word = !source[..start].ends_with(is_identifier_part)
            && !source[end..].starts_with(is_identifier_part);
        if !whole_word || in_ranges(&ignored, start) {
            return None;
        }
        // A property or private name (`obj.eval(x)`, `obj. // c` + newline +
        // `eval(x)`, `this.#eval(x)`) is an ordinary call. A spread
        // (`...eval(x)`) is not a member access.
        let member = matches!(
            previous_significant_token(source, end, &ignored),
            Token::Word { member: true, .. }
        );
        // Comments and line terminators may separate the name and `(`.
        let called = next_significant_token(source, end, &ignored).1 == Token::Punct("(");
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
    /// engine log, when requested, names the first module.
    fn eval_rule_drafts_for_modules(
        modules: &[(&str, &str)],
        engine_reports_eval: bool,
    ) -> Vec<DiagnosticDraft> {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();
        let build_dir = root.join("build");
        std::fs::create_dir_all(&build_dir).unwrap();
        std::fs::write(build_dir.join("main.js"), "const longLocalName = 1;").unwrap();
        let mut sources = crate::sources::SourceStore::new(true);
        let mut collected = Vec::new();
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
                chunks: vec!["main.js".to_owned()],
                ..CollectedModule::default()
            });
        }
        let collection = crate::collect::Collection {
            default_minifier: true,
            minify_enabled: true,
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
        let chunk = crate::chunks::Chunk::load(&build_dir, "main.js", None, true, root).unwrap();
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
            "export abstract class C<T extends { a: 1 }> { abstract eval(value): void; \
             eval(value) {} }",
            "class C<T extends { a: 1 }> { run() {} eval(value) {} }",
            "class C<T extends { a: 1 }> { x: Array<number> = []; eval(value) {} }",
            "class C { eval(value: Array<{ a: 1 }>): Promise<{ b: 2 }> {} }",
            // A comparison before the class does not hide its header.
            "a > b; class C<T extends { a: 1 }> { eval(value) {} }",
        ] {
            assert_eq!(direct_eval_line(source), None, "{source:?}");
        }
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
            "class Caf\u{e9}<T extends { \u{e9}: 1 }> extends \u{c9}<{ a: 1 }> { eval(value) {} }",
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
        // An engine-reported eval is still reported for a method-only module.
        assert_eq!(eval_rule_drafts(&method_module, true).len(), 1);
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
            // Engine-reported evals are unaffected by the source scan.
            assert_eq!(eval_rule_drafts(&shadowed, true).len(), 1);
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
