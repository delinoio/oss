//! Token-based classification of `await(` in generated JavaScript.
//!
//! `await` is the unary operator in a module and inside an `async` function
//! body, and an ordinary identifier in classic script code (`function f(await)
//! { await(x) }` calls `await`). One classifier answers both questions for a
//! whole chunk in a single pass over its token stream, so callers can cache the
//! answer per chunk instead of rescanning the text for every operand.
//!
//! The scan is lexical, not a parser. Tokens are identifiers (including
//! keywords, private names and numbers), punctuators, string, template and
//! regular expression literals, and line terminators. Comments are skipped but
//! a block comment that contains a line terminator counts as one. A case that
//! needs a real parser takes the conservative direction: `await(` stays an
//! ordinary call.

use super::common::{
    AwaitWord, arrow_body_ends, brace_holds_members, class_heading_precedes, is_identifier_part,
    is_line_terminator,
};

#[derive(Clone, Copy, PartialEq, Eq)]
enum Kind {
    /// Identifier, keyword, private name (`#name`) or number.
    Word,
    /// String, regular expression, or a template text piece.
    Literal,
    Punct(u8),
    /// `=>`.
    Arrow,
    /// `?.`.
    OptionalChain,
    /// `...`.
    Spread,
    /// `++` or `--`.
    Increment,
    /// Any other punctuator (`??`) or character.
    Other,
}

#[derive(Clone, Copy)]
struct Token {
    kind: Kind,
    start: usize,
    end: usize,
    /// True when a line terminator, possibly inside a block comment, separates
    /// this token from the previous one.
    newline_before: bool,
}

/// Words that start a statement head with a parenthesized part followed by a
/// block, so `keyword (...) {` is a block and not a function body.
const BLOCK_HEAD_WORDS: [&str; 7] = ["if", "for", "while", "switch", "catch", "with", "await"];

/// What a frame on the context stack represents.
#[derive(Clone, Copy)]
enum FrameKind {
    /// A bracket pair or a template substitution.
    Group,
    /// A concise arrow function body, which ends without a closing token.
    ArrowBody { ternaries: usize },
}

#[derive(Clone, Copy)]
struct Frame {
    kind: FrameKind,
    is_async: bool,
    /// Index of the opening token, when the frame has one.
    opener: Option<usize>,
}

struct Scan<'a> {
    code: &'a str,
    ignored: &'a [(usize, usize)],
    tokens: Vec<Token>,
    /// For every closing token, the index of its opening token.
    matching_open: Vec<Option<usize>>,
}

/// Byte offsets of every `await` that is the unary operator and is followed by
/// `(`, in ascending order.
///
/// `await(` is an operator in a module (the chunk has an `import` or `export`
/// declaration or `import.meta`) and inside the body of an `async` function,
/// `async` arrow or `async` method. A plain function nested in an async
/// function, a classic script and text that only resembles those constructs
/// leave `await(` as an ordinary call.
pub(super) fn await_operator_offsets(code: &str, ignored: &[(usize, usize)]) -> Vec<usize> {
    if !code.contains("await") {
        return Vec::new();
    }
    let mut scan = Scan {
        code,
        ignored,
        tokens: tokenize(code, ignored),
        matching_open: Vec::new(),
    };
    scan.matching_open = vec![None; scan.tokens.len()];
    scan.classify()
}

fn tokenize(code: &str, ignored: &[(usize, usize)]) -> Vec<Token> {
    let bytes = code.as_bytes();
    let mut tokens = Vec::new();
    let mut newline = false;
    let mut position = 0;
    let mut range = 0;
    while position < code.len() {
        while range < ignored.len() && ignored[range].1 <= position {
            range += 1;
        }
        if let Some(&(start, end)) = ignored.get(range)
            && start <= position
        {
            let text = &code[start..end];
            if text.starts_with("/*") {
                newline |= text.contains(is_line_terminator);
            } else if !text.starts_with("//") {
                tokens.push(Token {
                    kind: Kind::Literal,
                    start,
                    end,
                    newline_before: std::mem::take(&mut newline),
                });
            }
            position = end;
            range += 1;
            continue;
        }
        let character = code[position..].chars().next().expect("position in bounds");
        let width = character.len_utf8();
        if is_line_terminator(character) {
            newline = true;
            position += width;
            continue;
        }
        if character.is_whitespace() {
            position += width;
            continue;
        }
        let start = position;
        let next = bytes.get(position + 1).copied();
        let after = bytes.get(position + 2).copied();
        let (kind, length) = if is_identifier_part(character)
            || character == '#'
            || (character == '.' && next.is_some_and(|byte| byte.is_ascii_digit()))
        {
            let numeric = character.is_ascii_digit() || character == '.';
            let mut end = position + width;
            while let Some(following) = code[end..].chars().next() {
                let decimal_point = numeric
                    && following == '.'
                    && bytes.get(end + 1).is_some_and(u8::is_ascii_digit);
                if !is_identifier_part(following) && !decimal_point {
                    break;
                }
                end += following.len_utf8();
            }
            (Kind::Word, end - position)
        } else {
            match (bytes[position], next, after) {
                (b'=', Some(b'>'), _) => (Kind::Arrow, 2),
                (b'?', Some(b'.'), after) if !after.is_some_and(|byte| byte.is_ascii_digit()) => {
                    (Kind::OptionalChain, 2)
                }
                (b'?', Some(b'?'), _) => (Kind::Other, 2),
                (b'.', Some(b'.'), Some(b'.')) => (Kind::Spread, 3),
                (b'+', Some(b'+'), _) | (b'-', Some(b'-'), _) => (Kind::Increment, 2),
                (byte, ..) if byte.is_ascii() => (Kind::Punct(byte), 1),
                _ => (Kind::Other, width),
            }
        };
        position += length;
        tokens.push(Token {
            kind,
            start,
            end: position,
            newline_before: std::mem::take(&mut newline),
        });
    }
    tokens
}

impl Scan<'_> {
    fn text(&self, index: usize) -> &str {
        let token = self.tokens[index];
        &self.code[token.start..token.end]
    }

    fn is_punct(&self, index: usize, byte: u8) -> bool {
        self.tokens
            .get(index)
            .is_some_and(|token| token.kind == Kind::Punct(byte))
    }

    /// True when the token follows `.` or `?.`, so it names a property.
    fn is_member(&self, index: usize) -> bool {
        index > 0
            && matches!(
                self.tokens[index - 1].kind,
                Kind::Punct(b'.') | Kind::OptionalChain
            )
    }

    /// True when the token is the identifier or keyword `word` and not a
    /// property name.
    fn is_word(&self, index: usize, word: &str) -> bool {
        self.tokens
            .get(index)
            .is_some_and(|token| token.kind == Kind::Word)
            && self.text(index) == word
            && !self.is_member(index)
    }

    /// True when the token at `index` is directly preceded by the `async`
    /// keyword on the same line. `async` followed by a line terminator is an
    /// ordinary identifier, so ASI ends its statement.
    fn async_before(&self, index: usize) -> bool {
        index > 0 && self.is_word(index - 1, "async") && !self.tokens[index].newline_before
    }

    fn is_string_literal(&self, index: usize) -> bool {
        self.tokens
            .get(index)
            .is_some_and(|token| token.kind == Kind::Literal)
            && matches!(self.code.as_bytes()[self.tokens[index].start], b'"' | b'\'')
    }

    /// True when the method name at `name` (a word, string, number or private
    /// name, or the `[` of a computed name) is declared `async`, optionally as
    /// a generator (`async *name() {}`).
    fn method_is_async(&self, name: usize) -> bool {
        let mut index = name;
        if index > 0 && self.is_punct(index - 1, b'*') {
            index -= 1;
        }
        self.async_before(index)
    }

    /// True when the arrow function whose `=>` is at `arrow` is `async`.
    fn arrow_is_async(&self, arrow: usize) -> bool {
        let Some(previous) = arrow.checked_sub(1) else {
            return false;
        };
        match self.tokens[previous].kind {
            Kind::Punct(b')') => {
                self.matching_open[previous].is_some_and(|open| self.async_before(open))
            }
            Kind::Word => self.async_before(previous),
            _ => false,
        }
    }

    /// True when the word at `index` is preceded by a method modifier, so a
    /// keyword-named word is a method name instead of a statement keyword.
    fn has_method_modifier(&self, index: usize) -> bool {
        index > 0
            && (self.is_punct(index - 1, b'*')
                || ["get", "set", "static", "async"]
                    .iter()
                    .any(|modifier| self.is_word(index - 1, modifier)))
    }

    /// True when the `{` at `enclosing` holds object-literal or class members,
    /// where `if (a) {}` declares a method named `if` instead of starting a
    /// statement block.
    fn encloses_members(&self, enclosing: Option<usize>) -> bool {
        enclosing.is_some_and(|open| {
            self.tokens[open].kind == Kind::Punct(b'{')
                && brace_holds_members(self.code, self.tokens[open].start, self.ignored)
        })
    }

    /// The body of a method declaration whose name ends at `name`. A method
    /// exists only in an object literal or class body; after a call in a
    /// statement list (`foo()` + line break + `{`) the brace is an ordinary
    /// block that keeps the enclosing context.
    fn method_body(&self, name: usize, enclosing: Option<usize>) -> Option<bool> {
        self.encloses_members(enclosing)
            .then(|| self.method_is_async(name))
    }

    /// Classifies the `{` at `brace`: `Some(true)` for an async function,
    /// async arrow or async method body, `Some(false)` for any other function
    /// or method body, and `None` for a block, object literal or class body,
    /// which keep the enclosing context.
    fn function_body(&self, brace: usize, enclosing: Option<usize>) -> Option<bool> {
        let previous = brace.checked_sub(1)?;
        if self.tokens[previous].kind == Kind::Arrow {
            return Some(self.arrow_is_async(previous));
        }
        if self.tokens[previous].kind != Kind::Punct(b')') {
            return None;
        }
        let open = self.matching_open[previous]?;
        let head = open.checked_sub(1)?;
        match self.tokens[head].kind {
            // A computed method name: `async [name]() {}`.
            Kind::Punct(b']') => self.method_body(self.matching_open[head]?, enclosing),
            // An anonymous generator: `function* () {}`.
            Kind::Punct(b'*') => {
                let function = head.checked_sub(1)?;
                self.is_word(function, "function")
                    .then(|| self.async_before(function))
            }
            // A string-literal method name: `async "name"() {}`.
            Kind::Literal if self.is_string_literal(head) => self.method_body(head, enclosing),
            Kind::Word if self.is_member(head) => None,
            Kind::Word => {
                if self.text(head) == "function" {
                    return Some(self.async_before(head));
                }
                if BLOCK_HEAD_WORDS.contains(&self.text(head))
                    && !self.has_method_modifier(head)
                    && !self.encloses_members(enclosing)
                {
                    return None;
                }
                // `class A extends mix(B) {` heads a class body.
                if class_heading_precedes(self.code, self.tokens[previous].end, self.ignored) {
                    return None;
                }
                // `function name() {}` and `function* name() {}`.
                if let Some(function) = head.checked_sub(1)
                    && self.is_word(function, "function")
                {
                    return Some(self.async_before(function));
                }
                if let Some(function) = head.checked_sub(2)
                    && self.is_punct(head - 1, b'*')
                    && self.is_word(function, "function")
                {
                    return Some(self.async_before(function));
                }
                // A method or accessor of an object literal or class.
                self.method_body(head, enclosing)
            }
            _ => None,
        }
    }

    /// The `function` keyword whose parameter list opens at `open`, for
    /// `function (`, `function name (`, `function* (` and `function* name (`.
    /// A property named `function` (`obj.function(`) is not a keyword.
    fn function_keyword_before_params(&self, open: usize) -> Option<usize> {
        let mut head = open.checked_sub(1)?;
        if self.is_word(head, "function") {
            return Some(head);
        }
        if self.tokens[head].kind == Kind::Word && !self.is_member(head) {
            head = head.checked_sub(1)?;
        }
        if self.is_punct(head, b'*') {
            head = head.checked_sub(1)?;
        }
        self.is_word(head, "function").then_some(head)
    }

    /// True when the line break before the token at `index` ends the
    /// statement by automatic semicolon insertion. The decision is the shared
    /// one that the environment scan uses for its statement boundaries
    /// ([`arrow_body_ends`]), so a continuation such as `??`, `?.` or a binary
    /// operator keeps a concise arrow body open here too.
    ///
    /// `await_word` says how a bare `await` before the line break reads: the
    /// operator takes its operand from the next line, an identifier ends the
    /// statement.
    fn line_break_ends_statement(&self, index: usize, await_word: AwaitWord) -> bool {
        index > 0
            && self.tokens[index].newline_before
            && arrow_body_ends(
                self.code,
                self.tokens[index - 1].end,
                self.tokens[index].start,
                self.ignored,
                await_word,
            )
    }

    fn classify(&mut self) -> Vec<usize> {
        let mut frames = vec![Frame {
            kind: FrameKind::Group,
            is_async: false,
            opener: None,
        }];
        let is_module = (0..self.tokens.len()).any(|index| self.declares_module(index));
        let mut sites: Vec<(usize, bool)> = Vec::new();
        for index in 0..self.tokens.len() {
            let token = self.tokens[index];
            // A line break ends a concise arrow body when the next token
            // cannot continue it.
            let enclosing_async = frames.last().is_some_and(|frame| frame.is_async);
            let await_word = if is_module || enclosing_async {
                AwaitWord::Operator
            } else {
                AwaitWord::Identifier
            };
            if self.line_break_ends_statement(index, await_word) {
                pop_arrow_bodies(&mut frames);
            }
            let enclosing_async = frames.last().is_some_and(|frame| frame.is_async);
            match token.kind {
                Kind::Punct(b';' | b',') => pop_arrow_bodies(&mut frames),
                Kind::Punct(b'?') => {
                    if let Some(Frame {
                        kind: FrameKind::ArrowBody { ternaries },
                        ..
                    }) = frames.last_mut()
                    {
                        *ternaries += 1;
                    }
                }
                Kind::Punct(b':') => {
                    // A `:` that closes no ternary of its own ends the arrow
                    // body (`c ? () => 1 : 2`).
                    while let Some(Frame {
                        kind: FrameKind::ArrowBody { ternaries },
                        ..
                    }) = frames.last_mut()
                    {
                        if *ternaries > 0 {
                            *ternaries -= 1;
                            break;
                        }
                        frames.pop();
                    }
                }
                Kind::Punct(b'(' | b'[') => {
                    // The parameter list of a plain `function` is not an
                    // async context, even inside an async function, so
                    // `await(` in a default initializer is a call.
                    let is_async = if token.kind == Kind::Punct(b'(') {
                        self.function_keyword_before_params(index)
                            .map_or(enclosing_async, |function| self.async_before(function))
                    } else {
                        enclosing_async
                    };
                    frames.push(Frame {
                        kind: FrameKind::Group,
                        is_async,
                        opener: Some(index),
                    });
                }
                Kind::Punct(b'{') => {
                    let enclosing = frames.last().and_then(|frame| frame.opener);
                    let is_async = self
                        .function_body(index, enclosing)
                        .unwrap_or(enclosing_async);
                    frames.push(Frame {
                        kind: FrameKind::Group,
                        is_async,
                        opener: Some(index),
                    });
                }
                Kind::Punct(b')' | b']' | b'}') => self.close_group(&mut frames, index),
                Kind::Arrow if !self.is_punct(index + 1, b'{') => frames.push(Frame {
                    kind: FrameKind::ArrowBody { ternaries: 0 },
                    is_async: self.arrow_is_async(index),
                    opener: None,
                }),
                // Template text pieces: `}...` closes a substitution and
                // `...${` opens one.
                Kind::Literal => {
                    let closes = self.text(index).starts_with('}');
                    let opens = self.text(index).ends_with("${");
                    if closes {
                        self.close_group(&mut frames, index);
                    }
                    if opens {
                        frames.push(Frame {
                            kind: FrameKind::Group,
                            is_async: enclosing_async,
                            opener: None,
                        });
                    }
                }
                Kind::Word if self.is_word(index, "await") && self.is_punct(index + 1, b'(') => {
                    sites.push((token.start, enclosing_async));
                }
                _ => {}
            }
        }
        sites
            .into_iter()
            .filter(|(_, is_async)| is_module || *is_async)
            .map(|(offset, _)| offset)
            .collect()
    }

    fn close_group(&mut self, frames: &mut Vec<Frame>, index: usize) {
        pop_arrow_bodies(frames);
        if frames.len() > 1
            && let Some(frame) = frames.pop()
        {
            self.matching_open[index] = frame.opener;
        }
    }

    /// True when the word at `index` is an `import` or `export` declaration or
    /// `import.meta`, which only a module can contain.
    fn declares_module(&self, index: usize) -> bool {
        let next_kind = self.tokens.get(index + 1).map(|token| token.kind);
        if self.is_word(index, "export") {
            return matches!(next_kind, Some(Kind::Word | Kind::Punct(b'{' | b'*')));
        }
        if self.is_word(index, "import") {
            return match next_kind {
                Some(Kind::Word | Kind::Punct(b'{' | b'*')) => true,
                Some(Kind::Literal) => self.is_string_literal(index + 1),
                Some(Kind::Punct(b'.')) => {
                    self.tokens
                        .get(index + 2)
                        .is_some_and(|token| token.kind == Kind::Word)
                        && self.text(index + 2) == "meta"
                }
                _ => false,
            };
        }
        false
    }
}

fn pop_arrow_bodies(frames: &mut Vec<Frame>) {
    while frames
        .last()
        .is_some_and(|frame| matches!(frame.kind, FrameKind::ArrowBody { .. }))
    {
        frames.pop();
    }
}
