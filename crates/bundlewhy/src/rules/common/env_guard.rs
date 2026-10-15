//! Branch evidence for retained environment reads.
//!
//! The scan builds a small expression tree from the comment-free token stream
//! of a generated chunk and asks, for every branch site (`&&`, `||`, `??`,
//! `?:`, `if`, `while`, `switch`, the test of `for`), which retained reads may
//! determine the truth value of the tested expression. Only tree nodes that can
//! carry a read are kept, so unrelated code costs no memory. The propagation
//! rules for every node kind are listed in
//! `docs/crates-bundlewhy-foundation.md` and pinned by the tests in this file.
//!
//! This is not a complete JavaScript parser. A construct that needs one (a
//! labelled block against an object literal at the start of a statement,
//! getters, proxies, runtime values) is resolved in the direction in which the
//! read is not treated as guarded, so the finding is not produced.

use std::collections::HashMap;

/// The branch construct a retained read controls.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum EnvGuard {
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
    pub(super) fn evidence(self) -> &'static str {
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
pub(super) fn env_guard(
    code: &str,
    start: usize,
    end: usize,
    ignored: &[(usize, usize)],
) -> Option<EnvGuard> {
    env_guards(code, &[(start, end)], ignored)
        .into_iter()
        .next()
        .flatten()
}

/// Classify every read (byte ranges in `code`) in one pass. `ignored` holds the
/// string, template text, comment and regular expression ranges of `code`.
pub(super) fn env_guards(
    code: &str,
    reads: &[(usize, usize)],
    ignored: &[(usize, usize)],
) -> Vec<Option<EnvGuard>> {
    if reads.is_empty() {
        return Vec::new();
    }
    let tokens = tokenize(code, ignored);
    let mut parser = Parser::new(tokens, reads);
    parser.parse_program();
    parser.resolve(reads.len())
}

// ---------------------------------------------------------------------------
// Tokens
// ---------------------------------------------------------------------------

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum TemplatePart {
    /// A template without substitutions.
    Whole,
    /// Text up to the first `${`.
    Head,
    /// `}` ... `${` between two substitutions.
    Middle,
    /// `}` ... closing backtick.
    Tail,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum TokenKind {
    /// Identifiers and keywords.
    Word,
    Number,
    Str,
    Regex,
    Template(TemplatePart),
    Punct,
}

#[derive(Clone, Copy, Debug)]
struct Token<'a> {
    kind: TokenKind,
    text: &'a str,
    start: usize,
    end: usize,
    /// A line terminator, including one inside a block comment, separates this
    /// token from the previous one.
    newline: bool,
}

const PUNCTUATORS: [&str; 33] = [
    ">>>=", "...", "===", "!==", "**=", "<<=", ">>=", ">>>", "&&=", "||=", "??=", "=>", "==", "!=",
    "<=", ">=", "&&", "||", "??", "?.", "++", "--", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=",
    "<<", ">>", "**",
];

fn is_word_byte(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'$') || byte >= 0x80
}

/// Split `code` into tokens. Comments are dropped (a line terminator inside
/// one is remembered on the next token); strings, regular expressions and
/// template pieces are single tokens taken from `ignored`.
fn tokenize<'a>(code: &'a str, ignored: &[(usize, usize)]) -> Vec<Token<'a>> {
    let bytes = code.as_bytes();
    let mut tokens: Vec<Token<'a>> = Vec::new();
    let mut index = 0usize;
    let mut range = 0usize;
    let mut newline = false;
    while index < bytes.len() {
        while range < ignored.len() && ignored[range].1 <= index {
            range += 1;
        }
        if range < ignored.len() && ignored[range].0 <= index {
            let start = index;
            let mut end = ignored[range].1.min(bytes.len());
            let text = code.get(start..end).unwrap_or("");
            let first = text.as_bytes().first().copied().unwrap_or(b' ');
            let second = text.as_bytes().get(1).copied();
            if first == b'/' && matches!(second, Some(b'/' | b'*')) {
                if text.contains('\n') {
                    newline = true;
                }
                index = end.max(start + 1);
                continue;
            }
            let kind = match first {
                b'/' => {
                    // Flags are word bytes right after the closing slash.
                    while end < bytes.len() && is_word_byte(bytes[end]) {
                        end += 1;
                    }
                    TokenKind::Regex
                }
                b'\'' | b'"' => TokenKind::Str,
                b'`' | b'}' => {
                    let opens = text.len() >= 3 && text.ends_with("${");
                    let closes = text.len() >= 2 && text.ends_with('`');
                    match (first == b'`', opens, closes) {
                        (true, true, _) => TokenKind::Template(TemplatePart::Head),
                        (true, false, true) => TokenKind::Template(TemplatePart::Whole),
                        (false, true, _) => TokenKind::Template(TemplatePart::Middle),
                        (false, false, true) => TokenKind::Template(TemplatePart::Tail),
                        // Unterminated: nothing is known about the text.
                        _ => TokenKind::Str,
                    }
                }
                _ => TokenKind::Str,
            };
            let end = end.max(start + 1);
            tokens.push(Token {
                kind,
                text: code.get(start..end).unwrap_or(""),
                start,
                end,
                newline,
            });
            newline = false;
            index = end;
            continue;
        }
        let byte = bytes[index];
        if byte == b'\n' || byte == b'\r' {
            newline = true;
            index += 1;
            continue;
        }
        if byte.is_ascii_whitespace() {
            index += 1;
            continue;
        }
        let start = index;
        let kind = if byte.is_ascii_digit()
            || (byte == b'.' && bytes.get(index + 1).is_some_and(u8::is_ascii_digit))
        {
            index = number_end(bytes, index);
            TokenKind::Number
        } else if is_word_byte(byte) || byte == b'#' {
            index += 1;
            while index < bytes.len() && is_word_byte(bytes[index]) {
                index += 1;
            }
            TokenKind::Word
        } else {
            index += punctuator_length(code, index);
            TokenKind::Punct
        };
        tokens.push(Token {
            kind,
            text: &code[start..index],
            start,
            end: index,
            newline,
        });
        newline = false;
    }
    tokens
}

/// End of the numeric literal that starts at `start`.
fn number_end(bytes: &[u8], start: usize) -> usize {
    let hex = bytes[start] == b'0' && matches!(bytes.get(start + 1), Some(b'x' | b'X'));
    let mut index = start;
    let mut dot = bytes[start] == b'.';
    if dot {
        index += 1;
    }
    while index < bytes.len() {
        let byte = bytes[index];
        if is_word_byte(byte) {
            index += 1;
        } else if byte == b'.' && !dot && !hex {
            dot = true;
            index += 1;
        } else if matches!(byte, b'+' | b'-')
            && !hex
            && index > start
            && matches!(bytes[index - 1], b'e' | b'E')
        {
            index += 1;
        } else {
            break;
        }
    }
    index
}

/// Byte length of the punctuator at `index`; `?.` followed by a digit is a
/// conditional and a decimal number.
fn punctuator_length(code: &str, index: usize) -> usize {
    let rest = &code[index..];
    for punctuator in PUNCTUATORS {
        if rest.starts_with(punctuator) {
            if punctuator == "?."
                && rest
                    .as_bytes()
                    .get(2)
                    .is_some_and(|byte| byte.is_ascii_digit())
            {
                continue;
            }
            return punctuator.len();
        }
    }
    1
}

/// Index of the closing token for every opening token (`(`, `[`, `{` and the
/// pieces of a template substitution); `usize::MAX` when there is none.
fn match_brackets(tokens: &[Token<'_>]) -> Vec<usize> {
    const TEMPLATE: u8 = b'$';
    let mut close = vec![usize::MAX; tokens.len()];
    let mut stack: Vec<(usize, u8)> = Vec::new();
    let mut close_top = |stack: &mut Vec<(usize, u8)>, kind: u8, at: usize| {
        if let Some(position) = stack.iter().rposition(|(_, open)| *open == kind) {
            close[stack[position].0] = at;
            stack.truncate(position);
        }
    };
    for (at, token) in tokens.iter().enumerate() {
        match token.kind {
            TokenKind::Punct => match token.text {
                "(" | "[" | "{" => stack.push((at, token.text.as_bytes()[0])),
                ")" => close_top(&mut stack, b'(', at),
                "]" => close_top(&mut stack, b'[', at),
                "}" => close_top(&mut stack, b'{', at),
                _ => {}
            },
            TokenKind::Template(TemplatePart::Head) => stack.push((at, TEMPLATE)),
            TokenKind::Template(TemplatePart::Middle) => {
                close_top(&mut stack, TEMPLATE, at);
                stack.push((at, TEMPLATE));
            }
            TokenKind::Template(TemplatePart::Tail) => close_top(&mut stack, TEMPLATE, at),
            _ => {}
        }
    }
    close
}

// ---------------------------------------------------------------------------
// Expression tree
// ---------------------------------------------------------------------------

type Id = usize;

/// A parsed expression that may carry reads; `None` for an expression that
/// cannot (a literal, an identifier, `void x`, `new X()` and so on).
type Expr = Option<Id>;

/// How a member access picks its property.
#[derive(Clone, Copy, Debug)]
enum Selector<'a> {
    /// `.name` or `?.name`.
    Name(&'a str),
    /// `[0]` or `["name"]`; `index` is set for a plain decimal number.
    Literal { text: &'a str, index: Option<usize> },
    /// `[expression]`.
    Computed(Expr),
}

impl<'a> Selector<'a> {
    fn static_key(self) -> Option<&'a str> {
        match self {
            Self::Name(text) | Self::Literal { text, .. } => Some(text),
            Self::Computed(_) => None,
        }
    }
}

#[derive(Debug)]
struct Element {
    value: Expr,
    spread: bool,
}

#[derive(Debug)]
struct Property<'a> {
    /// Static key; `None` when it is not known.
    key: Option<&'a str>,
    value: Expr,
    /// A spread or a computed key may replace any earlier property.
    wildcard: bool,
}

/// One row per kind in the node-kind table of
/// `docs/crates-bundlewhy-foundation.md`.
#[derive(Debug)]
enum Node<'a> {
    /// A retained `process.env.NAME` read (by index).
    Read(usize),
    /// `!x`, `-x`, `+x`, `~x`, `++x`, `x++`, `await x`: derived from the
    /// operand.
    Derived(Id),
    /// `typeof x`: a type name; the operand matters only to an equality test.
    TypeOf(Id),
    /// Arithmetic, relational, bitwise, `in`, `instanceof`, `&&`, `||`, `??`
    /// and compound assignment: derived from either operand.
    Either(Expr, Expr),
    /// `==`, `!=`, `===`, `!==`: either operand, and the operand of a `typeof`.
    Equality(Expr, Expr),
    /// `c ? a : b`: either branch.
    Conditional(Expr, Expr),
    /// `object.name`, `object[0]`, `object[expression]`.
    Member {
        object: Expr,
        selector: Selector<'a>,
    },
    /// A call; `element` is the literal index of a sole `.at(N)` argument.
    Call { callee: Id, element: Option<usize> },
    /// A synchronous function whose result can carry reads.
    Function { returns: Vec<Id> },
    /// An array literal.
    Array { elements: Vec<Element> },
    /// An object literal.
    Object { properties: Vec<Property<'a>> },
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum BinaryOp {
    Logical(EnvGuard),
    Equality,
    Power,
    Other,
}

#[derive(Clone, Copy)]
enum AssignOp {
    Plain,
    Compound,
    Logical(EnvGuard),
}

/// Nesting limit of statements and assignment expressions. Deeper input is
/// skipped as opaque so that hostile or generated code cannot exhaust the
/// stack.
const MAX_DEPTH: usize = 160;
/// Nesting limit of prefix operators and `new`.
const MAX_CHAIN: usize = 200;

/// Words that cannot start an operand; the statement loop skips them.
const NON_OPERAND_WORDS: [&str; 8] = [
    "in",
    "instanceof",
    "else",
    "case",
    "default",
    "catch",
    "finally",
    "extends",
];

struct Parser<'a> {
    tokens: Vec<Token<'a>>,
    close: Vec<usize>,
    /// Token index of a read's first token: (read index, end token index).
    reads_at: HashMap<usize, (usize, usize)>,
    pos: usize,
    depth: usize,
    chain: usize,
    /// `in` is not an operator (the initializer of a `for` head).
    no_in: bool,
    nodes: Vec<Node<'a>>,
    /// Branch sites in source order of their operator.
    sites: Vec<(EnvGuard, Id)>,
    /// The function bodies being parsed, innermost last.
    returns: Vec<Returns>,
}

/// What the `return` statements of one function body produced.
#[derive(Default)]
struct Returns {
    /// Returned expressions that can carry a read.
    values: Vec<Id>,
    /// Number of `return` statements, with or without a value.
    statements: usize,
}

impl<'a> Parser<'a> {
    fn new(tokens: Vec<Token<'a>>, reads: &[(usize, usize)]) -> Self {
        let close = match_brackets(&tokens);
        let mut reads_at = HashMap::new();
        for (index, &(start, end)) in reads.iter().enumerate() {
            let Ok(first) = tokens.binary_search_by_key(&start, |token| token.start) else {
                continue;
            };
            let mut last = first;
            while last < tokens.len() && tokens[last].end < end {
                last += 1;
            }
            if last < tokens.len() && tokens[last].end == end {
                reads_at.entry(first).or_insert((index, last + 1));
            }
        }
        Self {
            tokens,
            close,
            reads_at,
            pos: 0,
            depth: 0,
            chain: 0,
            no_in: false,
            nodes: Vec::new(),
            sites: Vec::new(),
            returns: vec![Returns::default()],
        }
    }

    // -- token helpers ------------------------------------------------------

    fn tok(&self) -> Option<Token<'a>> {
        self.tokens.get(self.pos).copied()
    }

    fn at(&self, offset: usize) -> Option<Token<'a>> {
        self.tokens.get(self.pos + offset).copied()
    }

    fn bump(&mut self) {
        if self.pos < self.tokens.len() {
            self.pos += 1;
        }
    }

    fn is_punct(&self, text: &str) -> bool {
        is_punct(self.tok(), text)
    }

    fn is_word(&self, text: &str) -> bool {
        is_word(self.tok(), text)
    }

    fn eat_punct(&mut self, text: &str) -> bool {
        let found = self.is_punct(text);
        if found {
            self.bump();
        }
        found
    }

    fn eat_word(&mut self, text: &str) -> bool {
        let found = self.is_word(text);
        if found {
            self.bump();
        }
        found
    }

    fn push(&mut self, node: Node<'a>) -> Expr {
        self.nodes.push(node);
        Some(self.nodes.len() - 1)
    }

    fn site(&mut self, guard: EnvGuard, expr: Expr) {
        if let Some(id) = expr {
            self.sites.push((guard, id));
        }
    }

    /// Run `parse` with `in` allowed as an operator again (inside brackets).
    fn within<T>(&mut self, parse: impl FnOnce(&mut Self) -> T) -> T {
        let saved = std::mem::replace(&mut self.no_in, false);
        let result = parse(self);
        self.no_in = saved;
        result
    }

    /// Input nested deeper than the limits is skipped as an opaque group.
    fn skip_nested(&mut self) -> Expr {
        if let Some(close) = self.close.get(self.pos).copied() {
            self.pos = if close != usize::MAX && close >= self.pos {
                close + 1
            } else {
                self.pos + 1
            };
        }
        None
    }

    // -- statements ---------------------------------------------------------

    fn parse_program(&mut self) {
        while self.pos < self.tokens.len() {
            let before = self.pos;
            self.parse_statement();
            if self.pos == before {
                self.bump();
            }
        }
    }

    fn parse_statement(&mut self) {
        if self.depth >= MAX_DEPTH {
            self.skip_nested();
            return;
        }
        self.depth += 1;
        self.parse_statement_inner();
        self.depth -= 1;
    }

    fn parse_statement_inner(&mut self) {
        let Some(token) = self.tok() else {
            return;
        };
        match token.kind {
            TokenKind::Punct if token.text == ";" => self.bump(),
            TokenKind::Punct if token.text == "{" => self.within(Self::parse_block),
            TokenKind::Word => self.parse_keyword_statement(token),
            _ => self.expression_statement(),
        }
    }

    fn parse_keyword_statement(&mut self, token: Token<'a>) {
        match token.text {
            "if" => {
                self.bump();
                self.parse_condition(Some(EnvGuard::If));
                self.parse_statement();
                if self.eat_word("else") {
                    self.parse_statement();
                }
            }
            "while" => {
                self.bump();
                self.parse_condition(Some(EnvGuard::While));
                self.parse_statement();
            }
            "with" => {
                self.bump();
                self.parse_condition(None);
                self.parse_statement();
            }
            "do" => {
                self.bump();
                self.parse_statement();
                if self.eat_word("while") {
                    self.parse_condition(Some(EnvGuard::While));
                }
                self.eat_punct(";");
            }
            "for" => self.parse_for(),
            "switch" => self.parse_switch(),
            "try" => self.parse_try(),
            "return" => {
                self.bump();
                let value = match self.tok() {
                    Some(next)
                        if !next.newline
                            && !(next.kind == TokenKind::Punct
                                && matches!(next.text, ";" | "}")) =>
                    {
                        self.parse_expr()
                    }
                    _ => None,
                };
                if let Some(frame) = self.returns.last_mut() {
                    frame.statements += 1;
                    frame.values.extend(value);
                }
                self.eat_punct(";");
            }
            "throw" => {
                self.bump();
                if self.tok().is_some_and(|next| !next.newline) {
                    self.parse_expr();
                }
                self.eat_punct(";");
            }
            "break" | "continue" => {
                self.bump();
                if self
                    .tok()
                    .is_some_and(|next| !next.newline && next.kind == TokenKind::Word)
                {
                    self.bump();
                }
                self.eat_punct(";");
            }
            "debugger" => {
                self.bump();
                self.eat_punct(";");
            }
            "var" | "const" => {
                self.parse_declarations();
                self.eat_punct(";");
            }
            "let" if self.declaration_follows() => {
                self.parse_declarations();
                self.eat_punct(";");
            }
            "function" => {
                self.bump();
                self.eat_punct("*");
                self.parse_function_rest(false);
            }
            "async"
                if is_word(self.at(1), "function") && !self.at(1).is_some_and(|t| t.newline) =>
            {
                self.bump();
                self.bump();
                self.eat_punct("*");
                self.parse_function_rest(false);
            }
            "class" => {
                self.bump();
                self.parse_class_rest();
            }
            "import" if !is_punct(self.at(1), "(") && !is_punct(self.at(1), ".") => {
                self.skip_import();
            }
            "export" => self.parse_export(),
            text if is_punct(self.at(1), ":") && !matches!(text, "default" | "case") => {
                self.bump();
                self.bump();
                self.parse_statement();
            }
            _ => self.expression_statement(),
        }
    }

    fn expression_statement(&mut self) {
        self.parse_expr();
        self.eat_punct(";");
    }

    fn parse_block(&mut self) {
        if !self.eat_punct("{") {
            return;
        }
        loop {
            match self.tok() {
                None => break,
                Some(token) if is_punct(Some(token), "}") => {
                    self.bump();
                    break;
                }
                Some(_) => {
                    let before = self.pos;
                    self.parse_statement();
                    if self.pos == before {
                        self.bump();
                    }
                }
            }
        }
    }

    /// Parse the body of a function: its `return` values are collected apart
    /// from those of the enclosing function.
    fn parse_body(&mut self) -> Vec<Id> {
        self.returns.push(Returns::default());
        if self.is_punct("{") {
            self.within(Self::parse_block);
        }
        self.returns.pop().unwrap_or_default().values
    }

    /// `( condition )` of `if`, `while`, `switch` and `with`.
    fn parse_condition(&mut self, guard: Option<EnvGuard>) {
        if !self.eat_punct("(") {
            return;
        }
        let condition = self.within(Self::parse_expr);
        if let Some(guard) = guard {
            self.site(guard, condition);
        }
        self.eat_punct(")");
    }

    fn declaration_follows(&self) -> bool {
        match self.at(1) {
            Some(next) if next.kind == TokenKind::Word => {
                !matches!(next.text, "in" | "of" | "instanceof")
            }
            next => is_punct(next, "[") || is_punct(next, "{"),
        }
    }

    /// `var`, `let` or `const` and its declarators.
    fn parse_declarations(&mut self) {
        self.bump();
        loop {
            match self.tok() {
                Some(token) if token.kind == TokenKind::Word => self.bump(),
                Some(token) if is_punct(Some(token), "[") || is_punct(Some(token), "{") => {
                    self.parse_primary();
                }
                _ => break,
            }
            if self.eat_punct("=") {
                self.parse_assign();
            }
            if !self.eat_punct(",") {
                break;
            }
        }
    }

    fn parse_for(&mut self) {
        self.bump();
        self.eat_word("await");
        if !self.eat_punct("(") {
            return;
        }
        let saved = std::mem::replace(&mut self.no_in, true);
        if self.is_punct(";") {
        } else if self.is_word("var")
            || self.is_word("const")
            || (self.is_word("let") && self.declaration_follows())
        {
            self.parse_declarations();
        } else {
            self.parse_expr();
        }
        self.no_in = false;
        if self.is_word("of") || self.is_word("in") {
            self.bump();
            self.parse_expr();
            self.eat_punct(")");
        } else {
            self.eat_punct(";");
            let test = if self.is_punct(";") || self.is_punct(")") {
                None
            } else {
                self.parse_expr()
            };
            self.site(EnvGuard::For, test);
            self.eat_punct(";");
            if !self.is_punct(")") {
                self.parse_expr();
            }
            self.eat_punct(")");
        }
        self.no_in = saved;
        self.parse_statement();
    }

    fn parse_switch(&mut self) {
        self.bump();
        self.parse_condition(Some(EnvGuard::Switch));
        if !self.eat_punct("{") {
            return;
        }
        loop {
            match self.tok() {
                None => break,
                Some(token) if is_punct(Some(token), "}") => {
                    self.bump();
                    break;
                }
                Some(token) if token.kind == TokenKind::Word && token.text == "case" => {
                    self.bump();
                    self.parse_expr();
                    self.eat_punct(":");
                }
                Some(token) if token.kind == TokenKind::Word && token.text == "default" => {
                    self.bump();
                    self.eat_punct(":");
                }
                Some(_) => {
                    let before = self.pos;
                    self.parse_statement();
                    if self.pos == before {
                        self.bump();
                    }
                }
            }
        }
    }

    /// A `return` in a `finally` block replaces the value returned by the
    /// `try` and `catch` blocks, so those values are dropped.
    fn parse_try(&mut self) {
        self.bump();
        let start = self.returns.last().map_or(0, |frame| frame.values.len());
        self.within(Self::parse_block);
        if self.eat_word("catch") {
            if self.is_punct("(") {
                self.parse_call_arguments();
            }
            self.within(Self::parse_block);
        }
        let (middle, statements) = self
            .returns
            .last()
            .map_or((0, 0), |frame| (frame.values.len(), frame.statements));
        if self.eat_word("finally") {
            self.within(Self::parse_block);
            if let Some(frame) = self.returns.last_mut()
                && frame.statements > statements
            {
                frame.values.drain(start..middle);
            }
        }
    }

    fn skip_import(&mut self) {
        while let Some(token) = self.tok() {
            self.bump();
            if token.kind == TokenKind::Str {
                break;
            }
            if is_punct(Some(token), ";") {
                return;
            }
        }
        self.eat_punct(";");
    }

    fn parse_export(&mut self) {
        self.bump();
        if self.eat_word("default") {
            let declaration = self.is_word("function")
                || self.is_word("class")
                || (self.is_word("async") && is_word(self.at(1), "function"));
            if declaration {
                self.parse_statement();
            } else {
                self.parse_assign();
                self.eat_punct(";");
            }
            return;
        }
        if self.is_punct("{") {
            let close = self.close.get(self.pos).copied().unwrap_or(usize::MAX);
            self.pos = if close != usize::MAX {
                close + 1
            } else {
                self.pos + 1
            };
        } else if self.eat_punct("*") && self.eat_word("as") {
            self.bump();
        } else {
            self.parse_statement();
            return;
        }
        if self.eat_word("from") && self.tok().is_some_and(|t| t.kind == TokenKind::Str) {
            self.bump();
        }
        self.eat_punct(";");
    }

    // -- functions and classes ----------------------------------------------

    /// After the `function` keyword (and `*`): name, parameters and body.
    /// `sync` is false for generators and async functions.
    fn parse_function_rest(&mut self, sync: bool) -> Expr {
        if self
            .tok()
            .is_some_and(|token| token.kind == TokenKind::Word)
        {
            self.bump();
        }
        if self.is_punct("(") {
            self.parse_call_arguments();
        }
        let returns = self.parse_body();
        self.function_node(sync, returns)
    }

    fn function_node(&mut self, sync: bool, returns: Vec<Id>) -> Expr {
        if sync && !returns.is_empty() {
            self.push(Node::Function { returns })
        } else {
            None
        }
    }

    /// After the parameters of an arrow function: `=>` and the body.
    fn parse_arrow_rest(&mut self, sync: bool) -> Expr {
        self.eat_punct("=>");
        let returns = if self.is_punct("{") {
            self.parse_body()
        } else {
            self.within(Self::parse_assign).into_iter().collect()
        };
        self.function_node(sync, returns)
    }

    /// After the `class` keyword.
    fn parse_class_rest(&mut self) {
        if self
            .tok()
            .is_some_and(|token| token.kind == TokenKind::Word && token.text != "extends")
        {
            self.bump();
        }
        if self.eat_word("extends") {
            self.parse_lhs();
        }
        if !self.eat_punct("{") {
            return;
        }
        loop {
            match self.tok() {
                None => break,
                Some(token) if is_punct(Some(token), "}") => {
                    self.bump();
                    break;
                }
                Some(token) if is_punct(Some(token), ";") => self.bump(),
                Some(_) => self.parse_class_member(),
            }
        }
    }

    fn parse_class_member(&mut self) {
        let start = self.pos;
        while let Some(token) = self.tok() {
            if token.kind != TokenKind::Punct {
                self.bump();
                continue;
            }
            match token.text {
                "[" => {
                    self.bump();
                    self.within(Self::parse_assign);
                    self.eat_punct("]");
                }
                "(" => {
                    self.parse_call_arguments();
                    self.parse_body();
                    break;
                }
                "=" => {
                    self.bump();
                    self.within(Self::parse_assign);
                    break;
                }
                "{" => {
                    self.parse_body();
                    break;
                }
                ";" | "}" => break,
                _ => self.bump(),
            }
        }
        if self.pos == start {
            self.bump();
        }
    }

    // -- expressions --------------------------------------------------------

    /// Comma expression; its value is the last operand.
    fn parse_expr(&mut self) -> Expr {
        let mut last = self.parse_assign();
        while self.eat_punct(",") {
            last = self.parse_assign();
        }
        last
    }

    fn parse_assign(&mut self) -> Expr {
        if self.depth >= MAX_DEPTH {
            return self.skip_nested();
        }
        self.depth += 1;
        let expr = self.parse_assign_inner();
        self.depth -= 1;
        expr
    }

    fn parse_assign_inner(&mut self) -> Expr {
        if self.is_word("yield") && self.operand_follows(true) {
            self.bump();
            self.eat_punct("*");
            self.parse_assign();
            return None;
        }
        let left = self.parse_conditional();
        let Some(token) = self.tok().filter(|token| token.kind == TokenKind::Punct) else {
            return left;
        };
        let op = match token.text {
            "=" => AssignOp::Plain,
            "+=" | "-=" | "*=" | "/=" | "%=" | "**=" | "<<=" | ">>=" | ">>>=" | "&=" | "|="
            | "^=" => AssignOp::Compound,
            "&&=" => AssignOp::Logical(EnvGuard::ShortCircuitAnd),
            "||=" => AssignOp::Logical(EnvGuard::ShortCircuitOr),
            "??=" => AssignOp::Logical(EnvGuard::NullishCoalescing),
            _ => return left,
        };
        self.bump();
        if let AssignOp::Logical(guard) = op {
            self.site(guard, left);
        }
        let right = self.parse_assign();
        match op {
            AssignOp::Plain => right,
            AssignOp::Compound | AssignOp::Logical(_) => self.combine(BinaryOp::Other, left, right),
        }
    }

    fn parse_conditional(&mut self) -> Expr {
        let test = self.parse_binary(0);
        if !self.is_punct("?") {
            return test;
        }
        self.bump();
        self.site(EnvGuard::Conditional, test);
        let consequent = self.within(Self::parse_assign);
        self.eat_punct(":");
        let alternate = self.parse_assign();
        if consequent.is_none() && alternate.is_none() {
            None
        } else {
            self.push(Node::Conditional(consequent, alternate))
        }
    }

    fn binary_operator(&self) -> Option<(u8, BinaryOp)> {
        let token = self.tok()?;
        match token.kind {
            TokenKind::Punct => Some(match token.text {
                "??" => (1, BinaryOp::Logical(EnvGuard::NullishCoalescing)),
                "||" => (2, BinaryOp::Logical(EnvGuard::ShortCircuitOr)),
                "&&" => (3, BinaryOp::Logical(EnvGuard::ShortCircuitAnd)),
                "|" => (4, BinaryOp::Other),
                "^" => (5, BinaryOp::Other),
                "&" => (6, BinaryOp::Other),
                "==" | "!=" | "===" | "!==" => (7, BinaryOp::Equality),
                "<" | ">" | "<=" | ">=" => (8, BinaryOp::Other),
                "<<" | ">>" | ">>>" => (9, BinaryOp::Other),
                "+" | "-" => (10, BinaryOp::Other),
                "*" | "/" | "%" => (11, BinaryOp::Other),
                "**" => (12, BinaryOp::Power),
                _ => return None,
            }),
            TokenKind::Word => match token.text {
                "instanceof" => Some((8, BinaryOp::Other)),
                "in" if !self.no_in => Some((8, BinaryOp::Other)),
                _ => None,
            },
            _ => None,
        }
    }

    fn parse_binary(&mut self, min_precedence: u8) -> Expr {
        let mut left = self.parse_unary();
        while let Some((precedence, op)) = self.binary_operator() {
            if precedence < min_precedence {
                break;
            }
            self.bump();
            if op == BinaryOp::Power {
                left = self.parse_power(left);
                continue;
            }
            if let BinaryOp::Logical(guard) = op {
                self.site(guard, left);
            }
            let right = self.parse_binary(precedence + 1);
            left = self.combine(op, left, right);
        }
        left
    }

    /// The rest of a right-associative `**` chain after its first operator.
    /// `**` binds tighter than every other binary operator, so each operand
    /// is a unary expression; the chain is read without recursion so that an
    /// arbitrarily long one cannot exhaust the stack.
    fn parse_power(&mut self, base: Expr) -> Expr {
        let mut operands = vec![base, self.parse_unary()];
        while self.is_punct("**") {
            self.bump();
            operands.push(self.parse_unary());
        }
        let mut value = operands.pop().flatten();
        while let Some(left) = operands.pop() {
            value = self.combine(BinaryOp::Power, left, value);
        }
        value
    }

    fn combine(&mut self, op: BinaryOp, left: Expr, right: Expr) -> Expr {
        if left.is_none() && right.is_none() {
            return None;
        }
        self.push(if op == BinaryOp::Equality {
            Node::Equality(left, right)
        } else {
            Node::Either(left, right)
        })
    }

    fn derived(&mut self, operand: Expr) -> Expr {
        operand.and_then(|operand| self.push(Node::Derived(operand)))
    }

    /// True when the token after the current keyword starts an operand
    /// (`await` and `yield` are identifiers otherwise).
    fn operand_follows(&self, same_line: bool) -> bool {
        let Some(next) = self.at(1) else {
            return false;
        };
        if same_line && next.newline {
            return false;
        }
        match next.kind {
            TokenKind::Punct => matches!(
                next.text,
                "(" | "[" | "{" | "!" | "~" | "+" | "-" | "++" | "--"
            ),
            _ => true,
        }
    }

    fn parse_unary(&mut self) -> Expr {
        if self.chain >= MAX_CHAIN {
            return self.skip_nested();
        }
        self.chain += 1;
        let expr = self.parse_unary_inner();
        self.chain -= 1;
        expr
    }

    fn parse_unary_inner(&mut self) -> Expr {
        let token = self.tok()?;
        match (token.kind, token.text) {
            (TokenKind::Punct, "!" | "-" | "+" | "~" | "++" | "--") => {
                self.bump();
                let operand = self.parse_unary();
                self.derived(operand)
            }
            (TokenKind::Word, "typeof") => {
                self.bump();
                let operand = self.parse_unary();
                operand.and_then(|operand| self.push(Node::TypeOf(operand)))
            }
            (TokenKind::Word, "void" | "delete") => {
                self.bump();
                self.parse_unary();
                None
            }
            (TokenKind::Word, "await") if self.operand_follows(false) => {
                self.bump();
                let operand = self.parse_unary();
                self.derived(operand)
            }
            _ => self.parse_lhs(),
        }
    }

    /// Member accesses, calls and postfix operators after a primary.
    fn parse_lhs(&mut self) -> Expr {
        let (mut expr, terminal) = self.parse_primary();
        if terminal {
            return expr;
        }
        while let Some(token) = self.tok() {
            match token.kind {
                TokenKind::Punct => match token.text {
                    "." => {
                        self.bump();
                        let Some(name) = self.tok().filter(|name| name.kind == TokenKind::Word)
                        else {
                            break;
                        };
                        self.bump();
                        expr = self.member(expr, Selector::Name(name.text));
                    }
                    "?." => {
                        self.bump();
                        if self.is_punct("(") {
                            expr = self.call(expr);
                        } else if self.is_punct("[") {
                            let selector = self.parse_index();
                            expr = self.member(expr, selector);
                        } else if let Some(name) =
                            self.tok().filter(|name| name.kind == TokenKind::Word)
                        {
                            self.bump();
                            expr = self.member(expr, Selector::Name(name.text));
                        } else {
                            break;
                        }
                    }
                    "[" => {
                        let selector = self.parse_index();
                        expr = self.member(expr, selector);
                    }
                    "(" => expr = self.call(expr),
                    _ => break,
                },
                // A tagged template calls the tag.
                TokenKind::Template(TemplatePart::Whole) => {
                    self.bump();
                    expr = self.tagged(expr);
                }
                TokenKind::Template(TemplatePart::Head) => {
                    self.parse_template();
                    expr = self.tagged(expr);
                }
                _ => break,
            }
        }
        if let Some(token) = self.tok()
            && token.kind == TokenKind::Punct
            && matches!(token.text, "++" | "--")
            && !token.newline
        {
            self.bump();
            expr = self.derived(expr);
        }
        expr
    }

    fn member(&mut self, object: Expr, selector: Selector<'a>) -> Expr {
        let keyed = matches!(selector, Selector::Computed(Some(_)));
        if object.is_none() && !keyed {
            return None;
        }
        self.push(Node::Member { object, selector })
    }

    fn call(&mut self, callee: Expr) -> Expr {
        let element = self.parse_call_arguments();
        callee.and_then(|callee| self.push(Node::Call { callee, element }))
    }

    fn tagged(&mut self, tag: Expr) -> Expr {
        tag.and_then(|callee| {
            self.push(Node::Call {
                callee,
                element: None,
            })
        })
    }

    /// `[` ... `]` of a member access.
    fn parse_index(&mut self) -> Selector<'a> {
        if let (Some(key), true) = (self.at(1), is_punct(self.at(2), "]")) {
            match key.kind {
                TokenKind::Number => {
                    self.pos += 3;
                    return Selector::Literal {
                        text: key.text,
                        index: decimal_index(key.text),
                    };
                }
                TokenKind::Str => {
                    self.pos += 3;
                    return match string_content(key.text) {
                        Some(text) => Selector::Literal { text, index: None },
                        None => Selector::Computed(None),
                    };
                }
                _ => {}
            }
        }
        self.bump();
        let key = self.within(Self::parse_expr);
        self.eat_punct("]");
        Selector::Computed(key)
    }

    /// Parse `( arguments )` at the current `(`. Arguments are values passed
    /// on, never the value of the call. Returns the literal index when the
    /// only argument is a decimal number.
    fn parse_call_arguments(&mut self) -> Option<usize> {
        let sole = match (self.at(1), self.at(2)) {
            (Some(argument), Some(close))
                if argument.kind == TokenKind::Number && is_punct(Some(close), ")") =>
            {
                decimal_index(argument.text)
            }
            _ => None,
        };
        self.bump();
        let saved = std::mem::replace(&mut self.no_in, false);
        while let Some(token) = self.tok() {
            if is_punct(Some(token), ")") {
                self.bump();
                break;
            }
            if is_punct(Some(token), ",") {
                self.bump();
                continue;
            }
            let before = self.pos;
            self.eat_punct("...");
            self.parse_assign();
            if self.pos == before {
                self.bump();
            }
        }
        self.no_in = saved;
        sole
    }

    fn parse_template(&mut self) {
        // At the head piece.
        self.bump();
        loop {
            self.within(Self::parse_expr);
            match self.tok() {
                Some(token) if token.kind == TokenKind::Template(TemplatePart::Middle) => {
                    self.bump();
                }
                Some(token) if token.kind == TokenKind::Template(TemplatePart::Tail) => {
                    self.bump();
                    break;
                }
                _ => break,
            }
        }
    }

    /// An operand. The flag is true for an arrow function, which no member
    /// access or call can follow.
    fn parse_primary(&mut self) -> (Expr, bool) {
        if let Some(&(read, end)) = self.reads_at.get(&self.pos) {
            self.pos = end;
            return (self.push(Node::Read(read)), false);
        }
        let Some(token) = self.tok() else {
            return (None, false);
        };
        match token.kind {
            TokenKind::Number
            | TokenKind::Str
            | TokenKind::Regex
            | TokenKind::Template(TemplatePart::Whole) => {
                self.bump();
                (None, false)
            }
            TokenKind::Template(TemplatePart::Head) => {
                self.parse_template();
                (None, false)
            }
            TokenKind::Template(_) => (None, false),
            TokenKind::Punct => match token.text {
                "(" => self.parse_paren(),
                "[" => (self.parse_array(), false),
                "{" => (self.parse_object(), false),
                _ => (None, false),
            },
            TokenKind::Word => self.parse_word(token),
        }
    }

    /// True when the group that opens at token `at` is an arrow's parameters.
    fn arrow_follows(&self, at: usize) -> bool {
        match self.close.get(at).copied() {
            Some(close) if close != usize::MAX => self
                .tokens
                .get(close + 1)
                .is_some_and(|next| next.kind == TokenKind::Punct && next.text == "=>"),
            _ => false,
        }
    }

    fn parse_paren(&mut self) -> (Expr, bool) {
        if self.arrow_follows(self.pos) {
            self.parse_call_arguments();
            return (self.parse_arrow_rest(true), true);
        }
        self.bump();
        let inner = self.within(Self::parse_expr);
        self.eat_punct(")");
        (inner, false)
    }

    fn parse_word(&mut self, token: Token<'a>) -> (Expr, bool) {
        match token.text {
            "function" => {
                self.bump();
                let generator = self.eat_punct("*");
                (self.parse_function_rest(!generator), false)
            }
            "class" => {
                self.bump();
                self.parse_class_rest();
                (None, false)
            }
            "new" => {
                self.parse_new();
                (None, false)
            }
            "async" => self.parse_async(),
            text if NON_OPERAND_WORDS.contains(&text) => (None, false),
            _ => {
                self.bump();
                if self.is_punct("=>") && !self.tok().is_some_and(|arrow| arrow.newline) {
                    return (self.parse_arrow_rest(true), true);
                }
                (None, false)
            }
        }
    }

    fn parse_async(&mut self) -> (Expr, bool) {
        let next = self.at(1).filter(|next| !next.newline);
        match next {
            Some(next) if next.kind == TokenKind::Word && next.text == "function" => {
                self.bump();
                self.bump();
                self.eat_punct("*");
                self.parse_function_rest(false);
                (None, false)
            }
            Some(next) if next.kind == TokenKind::Word && is_punct(self.at(2), "=>") => {
                self.bump();
                self.bump();
                self.parse_arrow_rest(false);
                (None, true)
            }
            Some(next) if is_punct(Some(next), "(") && self.arrow_follows(self.pos + 1) => {
                self.bump();
                self.parse_call_arguments();
                self.parse_arrow_rest(false);
                (None, true)
            }
            _ => {
                self.bump();
                (None, false)
            }
        }
    }

    /// `new Callee(arguments)`: the value is an object, never a read.
    fn parse_new(&mut self) {
        if self.chain >= MAX_CHAIN {
            self.skip_nested();
            return;
        }
        self.chain += 1;
        self.bump();
        if self.eat_punct(".") {
            // `new.target`
            self.bump();
        } else {
            if self.is_word("new") {
                self.parse_new();
            } else {
                self.parse_primary();
            }
            loop {
                if self.eat_punct(".") {
                    self.bump();
                } else if self.is_punct("[") {
                    self.parse_index();
                } else {
                    break;
                }
            }
            if self.is_punct("(") {
                self.parse_call_arguments();
            }
        }
        self.chain -= 1;
    }

    fn parse_array(&mut self) -> Expr {
        self.bump();
        let saved = std::mem::replace(&mut self.no_in, false);
        let mut elements = Vec::new();
        while let Some(token) = self.tok() {
            if is_punct(Some(token), "]") {
                self.bump();
                break;
            }
            if is_punct(Some(token), ",") {
                self.bump();
                elements.push(Element {
                    value: None,
                    spread: false,
                });
                continue;
            }
            let before = self.pos;
            let spread = self.eat_punct("...");
            let value = self.parse_assign();
            elements.push(Element { value, spread });
            if !self.eat_punct(",") && self.pos == before {
                self.bump();
            }
        }
        self.no_in = saved;
        if elements.iter().any(|element| element.value.is_some()) {
            self.push(Node::Array { elements })
        } else {
            None
        }
    }

    fn parse_object(&mut self) -> Expr {
        self.bump();
        let saved = std::mem::replace(&mut self.no_in, false);
        let mut properties = Vec::new();
        while let Some(token) = self.tok() {
            if is_punct(Some(token), "}") {
                self.bump();
                break;
            }
            if is_punct(Some(token), ",") {
                self.bump();
                continue;
            }
            let before = self.pos;
            properties.push(self.parse_property());
            if self.pos == before {
                self.bump();
            }
        }
        self.no_in = saved;
        if properties.iter().any(|property| property.value.is_some()) {
            self.push(Node::Object { properties })
        } else {
            None
        }
    }

    fn parse_property(&mut self) -> Property<'a> {
        let mut property = Property {
            key: None,
            value: None,
            wildcard: false,
        };
        if self.eat_punct("...") {
            self.parse_assign();
            property.wildcard = true;
            return property;
        }
        // `get`, `set` and `async` are modifiers unless they are the key.
        if let Some(token) = self.tok()
            && token.kind == TokenKind::Word
            && matches!(token.text, "get" | "set" | "async")
            && self.at(1).is_some_and(|next| {
                !(next.kind == TokenKind::Punct && matches!(next.text, "(" | ":" | "," | "}" | "="))
            })
        {
            self.bump();
        }
        self.eat_punct("*");
        let Some(token) = self.tok() else {
            property.wildcard = true;
            return property;
        };
        match token.kind {
            TokenKind::Word | TokenKind::Number => {
                self.bump();
                property.key = Some(token.text);
            }
            TokenKind::Str => {
                self.bump();
                match string_content(token.text) {
                    Some(key) => property.key = Some(key),
                    None => property.wildcard = true,
                }
            }
            TokenKind::Punct if token.text == "[" => {
                self.bump();
                self.within(Self::parse_assign);
                self.eat_punct("]");
                property.wildcard = true;
            }
            _ => {
                property.wildcard = true;
                return property;
            }
        }
        if self.is_punct("(") {
            // A method: its result is only seen when the method is called.
            self.parse_call_arguments();
            self.parse_body();
        } else if self.eat_punct(":") {
            property.value = self.parse_assign();
        } else if self.eat_punct("=") {
            self.parse_assign();
        }
        property
    }

    // -- evaluation ---------------------------------------------------------

    /// Assign each branch site's guard to the reads that may determine the
    /// tested value. The first site in source order wins.
    fn resolve(mut self, count: usize) -> Vec<Option<EnvGuard>> {
        let mut guards = vec![None; count];
        let mut done = vec![false; self.nodes.len()];
        let sites = std::mem::take(&mut self.sites);
        let mut reads = Vec::new();
        for (guard, root) in sites {
            reads.clear();
            self.value_reads(root, &mut done, &mut reads);
            for &read in &reads {
                if guards[read].is_none() {
                    guards[read] = Some(guard);
                }
            }
        }
        guards
    }

    /// Collect the reads that may determine the value of `root`. A node is
    /// expanded once: its value set never changes, and an earlier site already
    /// gave its reads a guard.
    fn value_reads(&self, root: Id, done: &mut [bool], out: &mut Vec<usize>) {
        let mut work: Vec<Expr> = vec![Some(root)];
        while let Some(expr) = work.pop() {
            let Some(id) = expr else {
                continue;
            };
            if std::mem::replace(&mut done[id], true) {
                continue;
            }
            match &self.nodes[id] {
                Node::Read(read) => out.push(*read),
                Node::Derived(operand) => work.push(Some(*operand)),
                Node::TypeOf(_)
                | Node::Function { .. }
                | Node::Array { .. }
                | Node::Object { .. } => {}
                Node::Either(left, right) | Node::Conditional(left, right) => {
                    work.push(*left);
                    work.push(*right);
                }
                Node::Equality(left, right) => {
                    work.push(*left);
                    work.push(*right);
                    // `typeof x === "undefined"` tests x.
                    for operand in [left, right].into_iter().flatten() {
                        if let Node::TypeOf(inner) = &self.nodes[*operand] {
                            work.push(Some(*inner));
                        }
                    }
                }
                Node::Member { object, selector } => {
                    self.member_value(*object, *selector, &mut work);
                }
                Node::Call { callee, element } => self.call_value(*callee, *element, &mut work),
            }
        }
    }

    fn member_value(&self, object: Expr, selector: Selector<'a>, work: &mut Vec<Expr>) {
        if let Selector::Computed(key) = selector {
            work.push(key);
        }
        let Some(object_id) = object else {
            return;
        };
        match &self.nodes[object_id] {
            Node::Array { elements } => {
                if let Selector::Literal {
                    index: Some(index), ..
                } = selector
                {
                    array_element(elements, index, work);
                }
            }
            Node::Object { properties } => {
                if let Some(key) = selector.static_key() {
                    object_property(properties, key, work);
                }
            }
            _ => work.push(object),
        }
    }

    fn call_value(&self, callee: Id, element: Option<usize>, work: &mut Vec<Expr>) {
        match &self.nodes[callee] {
            Node::Function { returns } => work.extend(returns.iter().map(|id| Some(*id))),
            Node::Member {
                object: Some(object),
                selector: Selector::Name("at"),
            } if element.is_some() => match (&self.nodes[*object], element) {
                (Node::Array { elements }, Some(index)) => array_element(elements, index, work),
                _ => work.push(Some(callee)),
            },
            _ => work.push(Some(callee)),
        }
    }
}

fn is_punct(token: Option<Token<'_>>, text: &str) -> bool {
    token.is_some_and(|token| token.kind == TokenKind::Punct && token.text == text)
}

fn is_word(token: Option<Token<'_>>, text: &str) -> bool {
    token.is_some_and(|token| token.kind == TokenKind::Word && token.text == text)
}

/// Decimal index of an array element: digits only, no leading zero.
fn decimal_index(text: &str) -> Option<usize> {
    if text.is_empty()
        || !text.bytes().all(|byte| byte.is_ascii_digit())
        || (text.len() > 1 && text.starts_with('0'))
    {
        return None;
    }
    text.parse().ok()
}

/// Text of a terminated string literal without escapes.
fn string_content(text: &str) -> Option<&str> {
    let quote = *text.as_bytes().first()?;
    if text.len() < 2 || !matches!(quote, b'\'' | b'"') || text.as_bytes().last() != Some(&quote) {
        return None;
    }
    let inner = &text[1..text.len() - 1];
    (!inner.contains('\\')).then_some(inner)
}

/// The element of an array literal at `index`, unless a spread at or before it
/// makes the position unknown.
fn array_element(elements: &[Element], index: usize, work: &mut Vec<Expr>) {
    if index < elements.len() && !elements[..=index].iter().any(|element| element.spread) {
        work.push(elements[index].value);
    }
}

/// The value of the last property that defines `key`, unless a later spread or
/// computed key may have replaced it.
fn object_property(properties: &[Property<'_>], key: &str, work: &mut Vec<Expr>) {
    for property in properties.iter().rev() {
        if property.wildcard {
            return;
        }
        if property.key == Some(key) {
            work.push(property.value);
            return;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{super::non_code_ranges, *};

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

    /// A right-associative `**` chain is read without recursion.
    #[test]
    fn exponent_chains_are_read_iteratively() {
        let rows: &[(&str, &str, Option<&str>)] = &[
            ("exponent base", "process.env.FLAG**2&&run()", Some("and")),
            ("exponent power", "2**process.env.FLAG&&run()", Some("and")),
            (
                "chained exponent",
                "2**3**process.env.FLAG||run()",
                Some("or"),
            ),
            (
                "unary exponent operand",
                "2**-process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "multiplied exponent",
                "a*process.env.FLAG**2&&run()",
                Some("and"),
            ),
            (
                "exponent in a conditional",
                "process.env.FLAG**2**3?a:b",
                Some("conditional"),
            ),
            ("exponent without a read", "2**3**4&&process.env.FLAG", None),
        ];
        for &(kind, code, expected) in rows {
            assert_eq!(flag_guard(code), expected, "{kind}: {code}");
        }
        for operators in [300_000, 1_000_000] {
            let code = format!("{}process.env.FLAG&&run()", "1**".repeat(operators));
            assert_eq!(
                flag_guard(&code),
                Some("and"),
                "{operators} exponent operators"
            );
            let code = format!("process.env.FLAG{}||run()", "**1".repeat(operators));
            assert_eq!(
                flag_guard(&code),
                Some("or"),
                "{operators} exponent operators after the read"
            );
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
            (
                "array first of two",
                "[process.env.FLAG,a][0]&&run()",
                Some("and"),
            ),
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
                "object first property",
                "x={a:process.env.FLAG,b:1}.a&&run()",
                Some("and"),
            ),
            (
                "object property after the read",
                "x={a:process.env.FLAG,b:1}.b&&run()",
                None,
            ),
            (
                "object property redefined",
                "x={a:process.env.FLAG,a:1}.a&&run()",
                None,
            ),
            (
                "object property redefined by the read",
                "x={a:1,a:process.env.FLAG}.a&&run()",
                Some("and"),
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
            // A comparison of the type name tests the operand.
            (
                "typeof comparison",
                "typeof process.env.FLAG==='string'&&run()",
                Some("and"),
            ),
            (
                "typeof undefined guard",
                "typeof process.env.FLAG === \"undefined\" && run()",
                Some("and"),
            ),
            (
                "typeof inequality",
                "typeof process.env.FLAG!=='undefined'||run()",
                Some("or"),
            ),
            (
                "typeof loose equality",
                "typeof process.env.FLAG=='undefined'??b",
                Some("nullish"),
            ),
            (
                "typeof conditional",
                "typeof process.env.FLAG==='string'?a():b()",
                Some("conditional"),
            ),
            (
                "typeof comparison reversed",
                "'string'===typeof process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "typeof comparison grouped",
                "(typeof process.env.FLAG)==='string'&&run()",
                Some("and"),
            ),
            (
                "typeof comparison of a member",
                "typeof process.env.FLAG.length==='number'&&run()",
                Some("and"),
            ),
            (
                "typeof then additive operand",
                "typeof a+process.env.FLAG&&run()",
                Some("and"),
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
                Some("if"),
            ),
            (
                "typeof relational",
                "typeof process.env.FLAG<'u'&&run()",
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

    /// The shapes reported against the earlier character-level scan, each as
    /// one row, with the neighbouring forms that must agree with it.
    #[test]
    fn reported_expression_shapes() {
        let rows: &[(&str, &str, Option<&str>)] = &[
            // The `}` that ends a function expression does not end the
            // expression, so the parentheses after it are its call.
            (
                "arguments of an invoked function expression",
                "!function(){}(process.env.FLAG)&&run()",
                None,
            ),
            (
                "arguments of an invoked function expression (void)",
                "void function(){}(process.env.FLAG)&&run()",
                None,
            ),
            (
                "arguments of an invoked function expression (group)",
                "(function(){}(process.env.FLAG))&&run()",
                None,
            ),
            (
                "arguments of an invoked arrow function",
                "(()=>{})(process.env.FLAG)&&run()",
                None,
            ),
            // `typeof x === "undefined"` is a guard on x.
            (
                "typeof undefined guard",
                "typeof process.env.FLAG === \"undefined\" && run()",
                Some("and"),
            ),
            (
                "typeof undefined guard (single quotes)",
                "typeof process.env.FLAG==='undefined'&&run()",
                Some("and"),
            ),
            (
                "typeof undefined guard (in a condition)",
                "if(typeof process.env.FLAG!==\"undefined\")run()",
                Some("if"),
            ),
            ("typeof alone", "typeof process.env.FLAG && run()", None),
            // The literal index selects exactly one element.
            (
                "index selects the first element",
                "[process.env.FLAG, 1][0]&&run()",
                Some("and"),
            ),
            (
                "index selects the second element",
                "[1, process.env.FLAG][1]&&run()",
                Some("and"),
            ),
            (
                "index selects another element",
                "[process.env.FLAG, 1][1]&&run()",
                None,
            ),
            (
                "index selects an element before a spread",
                "[process.env.FLAG, ...rest][0]&&run()",
                Some("and"),
            ),
            (
                "at selects the first element",
                "[process.env.FLAG, 1].at(0)&&run()",
                Some("and"),
            ),
            (
                "comment inside the index",
                "[process.env.FLAG, 1][/* c */0]&&run()",
                Some("and"),
            ),
            (
                "comment inside the at argument",
                "[process.env.FLAG, 1].at(/* c */0)&&run()",
                Some("and"),
            ),
            // A block comment with a line terminator ends the `return`.
            (
                "return before a block comment with a line terminator",
                "(function(){return /*\n*/ process.env.FLAG})()&&run()",
                None,
            ),
            (
                "return before a line comment",
                "(function(){return // c\nprocess.env.FLAG})()&&run()",
                None,
            ),
            (
                "return before a block comment without a line terminator",
                "(function(){return /* c */ process.env.FLAG})()&&run()",
                Some("and"),
            ),
            // A `finally` return replaces the `try` and `catch` values.
            (
                "finally return replaces the try value",
                "(function(){try{return process.env.FLAG}finally{return 1}})()&&run()",
                None,
            ),
            (
                "finally return replaces the catch value",
                "(function(){try{f()}catch(e){return process.env.FLAG}finally{return 1}})()&&run()",
                None,
            ),
            (
                "finally return replaces both values",
                "(function(){try{return process.env.FLAG}catch(e){return 2}finally{return \
                 1}})()&&run()",
                None,
            ),
            (
                "finally without a return keeps the try value",
                "(function(){try{return process.env.FLAG}finally{cleanup()}})()&&run()",
                Some("and"),
            ),
            (
                "catch value without a finally",
                "(function(){try{f()}catch(e){return process.env.FLAG}})()&&run()",
                Some("and"),
            ),
            (
                "finally return of the read",
                "(function(){try{f()}finally{return process.env.FLAG}})()&&run()",
                Some("and"),
            ),
            (
                "return before the try statement",
                "(function(){if(a)return process.env.FLAG;try{f()}finally{return 1}})()&&run()",
                Some("and"),
            ),
            // A constructor call ignores a primitive result.
            (
                "constructor with a returned read",
                "new function(){return process.env.FLAG}()&&run()",
                None,
            ),
            (
                "constructor with a returned read (group)",
                "new (function(){return process.env.FLAG})()&&run()",
                None,
            ),
            (
                "constructor with an arrow argument",
                "new F(()=>process.env.FLAG)&&run()",
                None,
            ),
            // The selected property controls the operator.
            (
                "parenthesized object literal property",
                "({a:process.env.FLAG}).a&&run()",
                Some("and"),
            ),
            (
                "parenthesized object literal computed property",
                "({a:process.env.FLAG})[\"a\"]&&run()",
                Some("and"),
            ),
            (
                "doubly parenthesized object literal property",
                "(({a:process.env.FLAG})).a&&run()",
                Some("and"),
            ),
            (
                "parenthesized object literal other property",
                "({a:process.env.FLAG}).b&&run()",
                None,
            ),
            (
                "parenthesized array literal element",
                "([process.env.FLAG])[0]&&run()",
                Some("and"),
            ),
            // A comment between the bracket and the key is not a token.
            (
                "comment before a literal key",
                "x={a:process.env.FLAG}[/* comment */\"a\"]&&run()",
                Some("and"),
            ),
            (
                "comment after a literal key",
                "x={a:process.env.FLAG}[\"a\"/* comment */]&&run()",
                Some("and"),
            ),
            (
                "comment before a member name",
                "x={a:process.env.FLAG}./* comment */a&&run()",
                Some("and"),
            ),
            (
                "comment before a different literal key",
                "x={a:process.env.FLAG}[/* comment */\"b\"]&&run()",
                None,
            ),
        ];
        for &(shape, code, expected) in rows {
            assert_eq!(flag_guard(code), expected, "{shape}: {code}");
        }
    }

    /// One row per node kind of the expression tree, in the order of the
    /// node-kind table in `docs/crates-bundlewhy-foundation.md`.
    #[test]
    fn node_kind_rows() {
        let rows: &[(&str, &str, Option<&str>)] = &[
            ("read", "process.env.FLAG&&run()", Some("and")),
            (
                "derived: logical not",
                "!process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "derived: unary minus",
                "-process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "derived: unary plus",
                "+process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "derived: bitwise not",
                "~process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "derived: await",
                "await process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "derived: prefix increment",
                "++process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "derived: postfix increment",
                "process.env.FLAG++&&run()",
                Some("and"),
            ),
            ("typeof", "typeof process.env.FLAG&&run()", None),
            ("void", "void process.env.FLAG&&run()", None),
            ("delete", "delete process.env.FLAG&&run()", None),
            (
                "either: arithmetic",
                "process.env.FLAG*2||run()",
                Some("or"),
            ),
            (
                "either: relational",
                "2<process.env.FLAG&&run()",
                Some("and"),
            ),
            ("either: bitwise", "process.env.FLAG|1&&run()", Some("and")),
            ("either: shift", "1<<process.env.FLAG&&run()", Some("and")),
            (
                "either: exponent",
                "2**process.env.FLAG&&run()",
                Some("and"),
            ),
            ("either: in", "process.env.FLAG in o&&run()", Some("and")),
            (
                "either: instanceof",
                "o instanceof process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "either: logical and",
                "(a&&process.env.FLAG)||run()",
                Some("or"),
            ),
            (
                "either: logical or",
                "(a||process.env.FLAG)&&run()",
                Some("and"),
            ),
            (
                "either: nullish",
                "(a??process.env.FLAG)&&run()",
                Some("and"),
            ),
            (
                "either: compound assignment",
                "(x+=process.env.FLAG)&&run()",
                Some("and"),
            ),
            (
                "either: logical assignment",
                "(x||=process.env.FLAG)&&run()",
                Some("and"),
            ),
            (
                "equality: strict",
                "a===process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "equality: loose inequality",
                "process.env.FLAG!=a||run()",
                Some("or"),
            ),
            (
                "equality: typeof operand",
                "typeof process.env.FLAG==='string'&&run()",
                Some("and"),
            ),
            (
                "conditional: consequent",
                "(c?process.env.FLAG:d)&&run()",
                Some("and"),
            ),
            (
                "conditional: alternate",
                "(c?d:process.env.FLAG)&&run()",
                Some("and"),
            ),
            (
                "conditional: test",
                "process.env.FLAG?a():b()",
                Some("conditional"),
            ),
            (
                "member: name",
                "process.env.FLAG.length&&run()",
                Some("and"),
            ),
            (
                "member: literal index",
                "process.env.FLAG[0]&&run()",
                Some("and"),
            ),
            (
                "member: computed key",
                "a[process.env.FLAG]&&run()",
                Some("and"),
            ),
            (
                "member: array element",
                "[process.env.FLAG][0]&&run()",
                Some("and"),
            ),
            (
                "member: array other member",
                "[process.env.FLAG].length&&run()",
                None,
            ),
            (
                "member: object property",
                "x={a:process.env.FLAG}.a&&run()",
                Some("and"),
            ),
            (
                "member: object other member",
                "x={a:process.env.FLAG}.b&&run()",
                None,
            ),
            ("call: callee", "process.env.FLAG()&&run()", Some("and")),
            (
                "call: method of the read",
                "process.env.FLAG.trim()&&run()",
                Some("and"),
            ),
            ("call: argument", "f(process.env.FLAG)&&run()", None),
            (
                "call: optional argument",
                "f?.(process.env.FLAG)&&run()",
                None,
            ),
            (
                "call: function result",
                "(function(){return process.env.FLAG})()&&run()",
                Some("and"),
            ),
            (
                "call: arrow result",
                "(()=>process.env.FLAG)()&&run()",
                Some("and"),
            ),
            (
                "call: array at",
                "[process.env.FLAG].at(0)&&run()",
                Some("and"),
            ),
            (
                "call: tagged template",
                "process.env.FLAG`x`&&run()",
                Some("and"),
            ),
            ("call: new", "new F(process.env.FLAG)&&run()", None),
            (
                "function: not invoked",
                "(function(){return process.env.FLAG})&&run()",
                None,
            ),
            (
                "function: async",
                "(async function(){return process.env.FLAG})()&&run()",
                None,
            ),
            (
                "function: generator",
                "(function*(){return process.env.FLAG})()&&run()",
                None,
            ),
            (
                "function: nested result",
                "(function(){return function(){return process.env.FLAG}})()&&run()",
                None,
            ),
            ("array: element", "[process.env.FLAG]&&run()", None),
            ("array: spread", "[...process.env.FLAG]&&run()", None),
            ("object: property", "x={a:process.env.FLAG}&&run()", None),
            ("object: spread", "x={...process.env.FLAG}&&run()", None),
            ("sequence: last", "(a,process.env.FLAG)&&run()", Some("and")),
            ("sequence: first", "(process.env.FLAG,a)&&run()", None),
            (
                "assignment: value",
                "x=process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "assignment: result is the value",
                "(x=process.env.FLAG)&&run()",
                Some("and"),
            ),
            (
                "assignment: target index",
                "(a[process.env.FLAG]=1)&&run()",
                None,
            ),
            (
                "template: substitution",
                "`${process.env.FLAG}`&&run()",
                None,
            ),
            (
                "template: branch inside",
                "`${process.env.FLAG&&a}`",
                Some("and"),
            ),
            ("template: tag", "tag`${process.env.FLAG}`&&run()", None),
            (
                "class: field initializer",
                "class A{x=process.env.FLAG&&a}",
                Some("and"),
            ),
            (
                "class: method body",
                "class A{m(){return process.env.FLAG&&a}}",
                Some("and"),
            ),
            (
                "class: heritage",
                "class A extends (process.env.FLAG&&B){}",
                Some("and"),
            ),
            (
                "class: expression",
                "(class{m(){return process.env.FLAG}})&&run()",
                None,
            ),
        ];
        for &(kind, code, expected) in rows {
            assert_eq!(flag_guard(code), expected, "{kind}: {code}");
        }
    }

    /// Statement forms that hold a branch condition or a nested function.
    #[test]
    fn statement_rows() {
        let rows: &[(&str, &str, Option<&str>)] = &[
            ("if", "if(process.env.FLAG)run()", Some("if")),
            (
                "if else",
                "if(a)b();else if(process.env.FLAG)c()",
                Some("if"),
            ),
            ("while", "while(process.env.FLAG)run()", Some("while")),
            (
                "do while",
                "do run();while(process.env.FLAG)",
                Some("while"),
            ),
            (
                "switch",
                "switch(process.env.FLAG){case 1:run()}",
                Some("switch"),
            ),
            (
                "switch case",
                "switch(a){case process.env.FLAG:run()}",
                None,
            ),
            (
                "switch body",
                "switch(a){case 1:process.env.FLAG&&run()}",
                Some("and"),
            ),
            (
                "for test",
                "for(let i=0;process.env.FLAG;i++)run()",
                Some("for"),
            ),
            ("for of", "for(const x of process.env.FLAG)run()", None),
            ("for in", "for(const x in process.env.FLAG)run()", None),
            (
                "for initializer with in",
                "for(var i=(a in b)?1:0;process.env.FLAG;)run()",
                Some("for"),
            ),
            (
                "for await",
                "for await(const x of process.env.FLAG)run()",
                None,
            ),
            ("labelled", "outer:if(process.env.FLAG)run()", Some("if")),
            ("block", "{if(process.env.FLAG)run()}", Some("if")),
            ("declaration", "var a=process.env.FLAG&&b", Some("and")),
            (
                "declaration pattern",
                "var {a=process.env.FLAG&&b}=c",
                Some("and"),
            ),
            (
                "function declaration",
                "function f(){return process.env.FLAG&&a}",
                Some("and"),
            ),
            (
                "parameter default",
                "function f(a=process.env.FLAG&&b){}",
                Some("and"),
            ),
            (
                "arrow parameter default",
                "((a=process.env.FLAG&&b)=>a)()",
                Some("and"),
            ),
            (
                "async arrow body",
                "async()=>{if(process.env.FLAG)run()}",
                Some("if"),
            ),
            (
                "method body",
                "x={m(){if(process.env.FLAG)run()}}",
                Some("if"),
            ),
            (
                "getter body",
                "x={get a(){return process.env.FLAG?1:2}}",
                Some("conditional"),
            ),
            (
                "export default",
                "export default process.env.FLAG&&a",
                Some("and"),
            ),
            (
                "export declaration",
                "export const a=process.env.FLAG&&b",
                Some("and"),
            ),
            (
                "export list",
                "export{a as b};process.env.FLAG&&run()",
                Some("and"),
            ),
            (
                "import",
                "import a,{b}from'x';process.env.FLAG&&run()",
                Some("and"),
            ),
            ("dynamic import", "import(process.env.FLAG)&&run()", None),
            ("throw", "throw process.env.FLAG&&a", Some("and")),
            (
                "yield",
                "function*g(){yield process.env.FLAG&&a}",
                Some("and"),
            ),
            (
                "regular expression then member",
                "/x/.test(process.env.FLAG)&&run()",
                None,
            ),
            (
                "template substitution branch",
                "`${process.env.FLAG?1:2}`",
                Some("conditional"),
            ),
            (
                "nested template",
                "`a${`b${process.env.FLAG&&c}`}`",
                Some("and"),
            ),
        ];
        for &(statement, code, expected) in rows {
            assert_eq!(flag_guard(code), expected, "{statement}: {code}");
        }
    }

    /// Forms that need a complete parser or runtime knowledge. The scan picks
    /// the direction in which the read is not guarded. Each row is listed under
    /// "Known limits" in `docs/crates-bundlewhy-foundation.md`.
    #[test]
    fn expression_tree_known_limits_are_not_guarded() {
        for (limit, code) in [
            (
                "object literal at the start of a statement is read as a labelled block",
                "{a:process.env.FLAG}.a&&run()",
            ),
            (
                "object literal with an index at the start of a statement",
                "{a:process.env.FLAG}[\"a\"]&&run()",
            ),
            (
                "conditional return in a finally block",
                "(function(){try{return process.env.FLAG}finally{if(x)return 1}})()&&run()",
            ),
            (
                "object property read through a variable",
                "var o={a:process.env.FLAG};o.a&&run()",
            ),
            (
                "array element read through a variable",
                "var o=[process.env.FLAG];o[0]&&run()",
            ),
            (
                "object property replaced by a later spread",
                "x={a:process.env.FLAG,...y}.a&&run()",
            ),
            (
                "object property replaced by a later computed key",
                "x={a:process.env.FLAG,[k]:1}.a&&run()",
            ),
            (
                "object property with an escaped key",
                "x={\"\\u0061\":process.env.FLAG}.a&&run()",
            ),
            (
                "array element selected by a string index",
                "[process.env.FLAG][\"0\"]&&run()",
            ),
            (
                "array element selected by a leading-zero index",
                "[process.env.FLAG][00]&&run()",
            ),
            (
                "value returned through a promise",
                "(async()=>process.env.FLAG)().then(f)&&run()",
            ),
        ] {
            assert_eq!(flag_guard(code), None, "{limit}: {code}");
        }
    }

    #[test]
    fn tokens_drop_comments_and_remember_line_terminators() {
        let texts = |code: &str| -> Vec<(String, bool)> {
            tokenize(code, &non_code_ranges(code))
                .iter()
                .map(|token| (token.text.to_owned(), token.newline))
                .collect()
        };
        let row = |text: &str, newline: bool| (text.to_owned(), newline);
        assert_eq!(
            texts("a/*\n*/b // c\nd /* e */ f"),
            [
                row("a", false),
                row("b", true),
                row("d", true),
                row("f", false)
            ]
        );
        // Only a comment that holds a line terminator separates statements.
        assert_eq!(texts("a/* x */b"), [row("a", false), row("b", false)]);
        // Optional chaining, a conditional with a decimal and numbers.
        assert_eq!(
            texts("a?.b?.5:1"),
            [
                row("a", false),
                row("?.", false),
                row("b", false),
                row("?", false),
                row(".5", false),
                row(":", false),
                row("1", false)
            ]
        );
        assert_eq!(
            texts("1..x+0x1e+5+1e+5"),
            [
                row("1.", false),
                row(".", false),
                row("x", false),
                row("+", false),
                row("0x1e", false),
                row("+", false),
                row("5", false),
                row("+", false),
                row("1e+5", false)
            ]
        );
        // A regular expression keeps its flags; a string keeps its quotes.
        assert_eq!(
            texts("x=/a\\/b/gi.test('s')"),
            [
                row("x", false),
                row("=", false),
                row("/a\\/b/gi", false),
                row(".", false),
                row("test", false),
                row("(", false),
                row("'s'", false),
                row(")", false)
            ]
        );
        // Template pieces and the substitutions between them.
        let code = "`a${b}c${d}e`+`f`";
        let kinds: Vec<TokenKind> = tokenize(code, &non_code_ranges(code))
            .iter()
            .map(|token| token.kind)
            .collect();
        assert_eq!(
            kinds,
            [
                TokenKind::Template(TemplatePart::Head),
                TokenKind::Word,
                TokenKind::Template(TemplatePart::Middle),
                TokenKind::Word,
                TokenKind::Template(TemplatePart::Tail),
                TokenKind::Punct,
                TokenKind::Template(TemplatePart::Whole),
            ]
        );
        // Longest punctuator first.
        assert_eq!(
            texts("a>>>=b??=c...d=>e"),
            [
                row("a", false),
                row(">>>=", false),
                row("b", false),
                row("??=", false),
                row("c", false),
                row("...", false),
                row("d", false),
                row("=>", false),
                row("e", false)
            ]
        );
    }

    #[test]
    fn brackets_match_across_template_substitutions() {
        let code = "f(`a${[b]}c`)+{d}";
        let tokens = tokenize(code, &non_code_ranges(code));
        let close = match_brackets(&tokens);
        let text = |index: usize| tokens[index].text;
        let open = tokens.iter().position(|token| token.text == "(").unwrap();
        assert_eq!(text(close[open]), ")");
        let head = tokens
            .iter()
            .position(|token| token.kind == TokenKind::Template(TemplatePart::Head))
            .unwrap();
        assert_eq!(
            tokens[close[head]].kind,
            TokenKind::Template(TemplatePart::Tail)
        );
        let brace = tokens.iter().position(|token| token.text == "{").unwrap();
        assert_eq!(text(close[brace]), "}");
    }

    /// Deep, long and malformed input must neither overflow the stack of a
    /// default test thread nor loop.
    #[test]
    fn deep_and_malformed_input_terminates() {
        std::thread::Builder::new()
            .stack_size(2 * 1024 * 1024)
            .spawn(|| {
                let read = "process.env.FLAG";
                let classify = |code: &str| {
                    let ignored = non_code_ranges(code);
                    let reads: Vec<(usize, usize)> = code
                        .match_indices(read)
                        .map(|(start, text)| (start, start + text.len()))
                        .collect();
                    env_guards(code, &reads, &ignored)
                };
                // Moderate nesting still resolves.
                for (open, close) in [("(", ")"), ("[", "][0]"), ("(function(){return ", "})()")] {
                    let code = format!("{}{read}{}&&run()", open.repeat(40), close.repeat(40));
                    assert!(
                        classify(&code)[0].is_some(),
                        "{open} nesting should resolve"
                    );
                }
                // Hostile nesting is skipped.
                for (open, close) in [
                    ("(", ")"),
                    ("[", "]"),
                    ("{a:", "}"),
                    ("f(", ")"),
                    ("function(){return ", "}"),
                    ("()=>", ""),
                    ("a?", ":b"),
                    ("if(a){", "}"),
                    ("!", ""),
                    ("-", ""),
                    ("typeof ", ""),
                    ("await ", ""),
                    ("new ", ""),
                    ("x=", ""),
                    ("`${", "}`"),
                    ("a&&(", ")"),
                    ("class{m(){", "}}"),
                ] {
                    let code = format!(
                        "{}{read}{}&&run()",
                        open.repeat(30_000),
                        close.repeat(30_000)
                    );
                    assert_eq!(classify(&code).len(), 1);
                }
                // A long chain is resolved in linear time.
                let chain = format!("{read}{}", "||a".repeat(200_000));
                assert_eq!(classify(&chain), [Some(EnvGuard::ShortCircuitOr)]);
                let chain = format!("x={}{read}", "a+".repeat(200_000));
                assert_eq!(classify(&chain), [None]);
                // Token soup in a fixed pseudo-random order.
                let words = [
                    "(", ")", "[", "]", "{", "}", "function", "=>", "return", "if", "else", "for",
                    "while", "do", "switch", "case", "default", "try", "catch", "finally", "class",
                    "new", "typeof", "void", "delete", "async", "await", "yield", "?", ":", ";",
                    ",", ".", "?.", "...", "&&", "||", "??", "=", "x", read, "1", "'s'", "/re/",
                    "`t${", "}`", "\n", "in", "of", "let", "import", "export", "a:", "++", "get",
                    "static", "extends", "*", "/*\n*/", "//c\n",
                ];
                let mut seed = 0x2545_f491_4f6c_dd1du64;
                for _ in 0..400 {
                    let mut code = String::new();
                    for _ in 0..200 {
                        seed = seed
                            .wrapping_mul(6_364_136_223_846_793_005)
                            .wrapping_add(1_442_695_040_888_963_407);
                        code.push_str(words[(seed >> 33) as usize % words.len()]);
                        if (seed >> 20) & 1 == 1 {
                            code.push(' ');
                        }
                    }
                    classify(&code);
                }
            })
            .unwrap()
            .join()
            .unwrap();
    }

    /// Statement and expression shapes found in generated bundles.
    #[test]
    fn generated_bundle_shapes() {
        let rows: &[(&str, Option<&str>)] = &[
            (
                "export default function(){ return process.env.FLAG ? 1 : 2 }",
                Some("conditional"),
            ),
            (
                "const a = async function*(){ if (process.env.FLAG) yield 1 }",
                Some("if"),
            ),
            (
                "label: for(;;){ if(process.env.FLAG) break label; }",
                Some("if"),
            ),
            (
                "x = y\n/foo/.test(z) && process.env.FLAG && run()",
                Some("and"),
            ),
            (
                "class A extends B { static #p = 1; static { if (process.env.FLAG) run(); } get \
                 x(){return 1} }",
                Some("if"),
            ),
            ("a = b ? c : d ? process.env.FLAG : e; x && y", None),
            (
                "const {a, b = process.env.FLAG ? 1 : 2} = o",
                Some("conditional"),
            ),
            (
                "try { f() } catch { if (process.env.FLAG) g() }",
                Some("if"),
            ),
            (
                "switch (a) { default: if (process.env.FLAG) run() }",
                Some("if"),
            ),
            ("a?.[process.env.FLAG ? 0 : 1]", Some("conditional")),
            ("new Foo(process.env.FLAG ? 1 : 2)", Some("conditional")),
            (
                "x = function(){ return this }.call(process.env.FLAG) && 1",
                None,
            ),
            ("if (a) b; else if (process.env.FLAG) c; else d", Some("if")),
            (
                "const f = (a, b) => a && process.env.FLAG && b",
                Some("and"),
            ),
            ("var x = {}; x.y = process.env.FLAG || 1;", Some("or")),
            (
                "`${ {a:1}.a }` + (process.env.FLAG ? 1 : 2)",
                Some("conditional"),
            ),
            (
                "function f(){ return a ? (b, process.env.FLAG) : c }\nf() && run()",
                None,
            ),
            (
                "var f = function(){ return process.env.FLAG }, g = f() && 1",
                None,
            ),
            (
                "(function(){ 'use strict'; return process.env.FLAG })() && run()",
                Some("and"),
            ),
            (
                "(function(){ return (process.env.FLAG) })() && run()",
                Some("and"),
            ),
            (
                "(function(){ return !process.env.FLAG })() && run()",
                Some("and"),
            ),
            (
                "(function(){ return function(){}, process.env.FLAG })() && run()",
                Some("and"),
            ),
            (
                "!(function(){ return process.env.FLAG })() && run()",
                Some("and"),
            ),
            ("a ? b : c\nprocess.env.FLAG && d", Some("and")),
            ("a\n++\nb\nprocess.env.FLAG && d", Some("and")),
            ("while (i--) { if (process.env.FLAG) break }", Some("if")),
            ("process.env.FLAG ||= 5", Some("or")),
            ("(await import('x')).default(process.env.FLAG) && 1", None),
            (
                "if (typeof window !== 'undefined' && process.env.FLAG) run()",
                Some("if"),
            ),
            (
                "a = typeof process.env.FLAG !== 'undefined' ? 1 : 2",
                Some("conditional"),
            ),
        ];
        for &(code, expected) in rows {
            assert_eq!(flag_guard(code), expected, "{code}");
        }
    }
}
