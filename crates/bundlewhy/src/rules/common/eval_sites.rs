//! Classification of `eval` identifiers that are followed by `(`.
//!
//! One forward pass over the comment-stripped token stream keeps an explicit
//! stack of syntactic contexts (statement block, class body, object literal,
//! type body, parenthesized list, array and template substitution). Each
//! `eval` token is classified from that stack and from the tokens around it,
//! so no decision depends on guessing the context from the text behind the
//! token.
//!
//! The pass is a lexical scan, not a parser. When a construct needs a real
//! parser to decide, the token is reported as a direct call so that a possible
//! direct `eval` is never hidden. The documented known limits are pinned by
//! tests at the end of this file.

/// What an `eval` identifier followed by `(` is.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub(super) enum EvalSiteKind {
    /// A call of the global function, which is a possible direct eval.
    DirectCall,
    /// A method, accessor or function declaration named `eval`, including
    /// overload signatures in a class body.
    Declaration,
    /// A bodyless signature in an interface, a type literal or a type alias.
    Signature,
    /// A member access such as `object.eval(value)` or `object?.eval(value)`.
    MemberAccess,
    /// A private name such as `#eval(value)` or `this.#eval(value)`.
    PrivateName,
}

/// One classified `eval` token.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub(super) struct EvalSite {
    /// Byte offset of the `eval` (or `#eval`) token in the source.
    pub(super) start: usize,
    pub(super) kind: EvalSiteKind,
}

/// Classifies every `eval(` and `#eval(` token in `source`. `ignored` holds the
/// byte ranges of comments, strings, template text and regular expression
/// literals, sorted by start offset.
pub(super) fn classify_eval_sites(source: &str, ignored: &[(usize, usize)]) -> Vec<EvalSite> {
    let tokens = tokenize(source, ignored);
    let mut scanner = Scanner {
        tokens: &tokens,
        stack: vec![Frame::new(FrameKind::Block, TypeMode::None, false)],
        sites: Vec::new(),
    };
    for index in 0..tokens.len() {
        scanner.step(index);
    }
    scanner.sites
}

// ---------------------------------------------------------------------------
// Tokens
// ---------------------------------------------------------------------------

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum TokenKind {
    /// An identifier or keyword.
    Word,
    /// A private name such as `#eval`.
    Private,
    /// A number, string, template piece or regular expression literal.
    Value,
    Punct,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum TemplateEdge {
    None,
    /// The text up to a `${` that opens a substitution.
    Open,
    /// The text between a `}` and the next `${`.
    Middle,
    /// The text from the `}` that closes a substitution.
    Close,
}

#[derive(Clone, Copy, Debug)]
struct Token<'a> {
    kind: TokenKind,
    text: &'a str,
    start: usize,
    /// A line terminator, possibly inside a comment, precedes the token.
    line_break_before: bool,
    template: TemplateEdge,
}

impl Token<'_> {
    fn is_word(&self, text: &str) -> bool {
        self.kind == TokenKind::Word && self.text == text
    }

    fn is_punct(&self, text: &str) -> bool {
        self.kind == TokenKind::Punct && self.text == text
    }
}

fn is_line_terminator(character: char) -> bool {
    matches!(character, '\n' | '\r' | '\u{2028}' | '\u{2029}')
}

fn contains_line_terminator(text: &str) -> bool {
    text.contains(is_line_terminator)
}

fn is_identifier_start(character: char) -> bool {
    character.is_alphabetic() || character == '_' || character == '$'
}

/// ECMAScript allows the zero-width (non-)joiner after the first character.
fn is_identifier_part(character: char) -> bool {
    character.is_alphanumeric()
        || character == '_'
        || character == '$'
        || character == '\u{200C}'
        || character == '\u{200D}'
}

fn tokenize<'a>(source: &'a str, ignored: &[(usize, usize)]) -> Vec<Token<'a>> {
    let mut tokens = Vec::new();
    let mut index = 0usize;
    let mut cursor = 0usize;
    let mut line_break = false;
    while index < source.len() {
        if !source.is_char_boundary(index) {
            index += 1;
            continue;
        }
        while cursor < ignored.len() && ignored[cursor].1 <= index {
            cursor += 1;
        }
        if let Some(&(start, end)) = ignored.get(cursor)
            && start <= index
        {
            let mut end = end.min(source.len());
            while !source.is_char_boundary(end) {
                end += 1;
            }
            let text = &source[index..end];
            if text.starts_with("//") || text.starts_with("/*") {
                line_break |= contains_line_terminator(text);
            } else {
                let opens = text.ends_with("${");
                let template = if text.starts_with('`') && opens {
                    TemplateEdge::Open
                } else if text.starts_with('}') {
                    if opens {
                        TemplateEdge::Middle
                    } else {
                        TemplateEdge::Close
                    }
                } else {
                    TemplateEdge::None
                };
                tokens.push(Token {
                    kind: TokenKind::Value,
                    text,
                    start: index,
                    line_break_before: line_break,
                    template,
                });
                line_break = false;
            }
            index = end;
            continue;
        }
        let rest = &source[index..];
        let Some(character) = rest.chars().next() else {
            break;
        };
        if character.is_whitespace() {
            line_break |= is_line_terminator(character);
            index += character.len_utf8();
            continue;
        }
        let (kind, length) = if is_identifier_start(character) {
            let length = rest
                .char_indices()
                .find(|(_, next)| !is_identifier_part(*next))
                .map_or(rest.len(), |(offset, _)| offset);
            (TokenKind::Word, length)
        } else if character.is_ascii_digit() {
            let mut length = 0usize;
            let mut characters = rest.char_indices().peekable();
            while let Some((offset, next)) = characters.next() {
                let fraction = next == '.'
                    && characters
                        .peek()
                        .is_some_and(|(_, after)| after.is_ascii_digit());
                if !is_identifier_part(next) && !fraction {
                    break;
                }
                length = offset + next.len_utf8();
            }
            (TokenKind::Value, length)
        } else if character == '#' && rest[1..].chars().next().is_some_and(is_identifier_start) {
            let length = 1 + rest[1..]
                .char_indices()
                .find(|(_, next)| !is_identifier_part(*next))
                .map_or(rest.len() - 1, |(offset, _)| offset);
            (TokenKind::Private, length)
        } else if rest.starts_with("...") {
            (TokenKind::Punct, 3)
        } else if rest.starts_with("=>")
            || rest.starts_with("++")
            || rest.starts_with("--")
            || (rest.starts_with("?.")
                && !rest[2..].starts_with(|next: char| next.is_ascii_digit()))
        {
            (TokenKind::Punct, 2)
        } else {
            (TokenKind::Punct, character.len_utf8())
        };
        tokens.push(Token {
            kind,
            text: &rest[..length],
            start: index,
            line_break_before: line_break,
            template: TemplateEdge::None,
        });
        line_break = false;
        index += length;
    }
    tokens
}

// ---------------------------------------------------------------------------
// Token tables
// ---------------------------------------------------------------------------

/// Keywords that need an operand (or, for type keywords, a type) after them.
/// A token after one of these continues the expression whatever line it is on.
const OPERATOR_KEYWORDS: &[&str] = &[
    "typeof",
    "delete",
    "await",
    "yield",
    "new",
    "in",
    "of",
    "instanceof",
    "extends",
    "implements",
    "as",
    "satisfies",
    "keyof",
    "infer",
    "unique",
    "is",
    "asserts",
    "else",
    "do",
    "case",
    "throw",
];

/// Words that precede an object literal in expression position.
const OBJECT_LITERAL_KEYWORDS: &[&str] = &[
    "return",
    "yield",
    "await",
    "throw",
    "typeof",
    "void",
    "delete",
    "in",
    "of",
    "default",
    "extends",
    "const",
    "let",
    "var",
    "instanceof",
    "as",
    "satisfies",
];

/// Keywords with a restricted production: a line break before the next token
/// ends the statement or expression, so that token never continues the keyword
/// (`yield\n{ ... }` is a `yield` followed by a block). `throw` is included
/// because a line break after it is an error, and the block reading is the one
/// that keeps a possible direct `eval` visible.
const RESTRICTED_KEYWORDS: &[&str] = &["return", "yield", "throw", "break", "continue"];

/// Modifiers that can precede a class, object or type member name.
const MEMBER_MODIFIERS: &[&str] = &[
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
    "declare",
    "accessor",
];

/// Words that can precede a declaration at statement start.
const STATEMENT_MODIFIERS: &[&str] = &["export", "declare", "abstract", "default"];

/// Reports whether `token` ends an operand: a value, a name or a closing
/// delimiter. `in_type` makes `void` a type instead of an operator.
fn ends_operand(token: &Token<'_>, in_type: bool) -> bool {
    match token.kind {
        TokenKind::Value | TokenKind::Private => true,
        TokenKind::Word => {
            if token.text == "void" {
                in_type
            } else {
                !OPERATOR_KEYWORDS.contains(&token.text)
            }
        }
        TokenKind::Punct => matches!(token.text, ")" | "]" | "}" | "++" | "--"),
    }
}

/// Reports whether the token after `token` continues the same expression even
/// when a line break separates them, so automatic semicolon insertion does not
/// apply.
fn continues_after(token: &Token<'_>, in_type: bool) -> bool {
    match token.kind {
        TokenKind::Value | TokenKind::Private => false,
        TokenKind::Word => {
            if token.text == "void" {
                !in_type
            } else {
                OPERATOR_KEYWORDS.contains(&token.text)
            }
        }
        TokenKind::Punct => !matches!(token.text, ")" | "]" | "}" | ";" | "{" | "++" | "--"),
    }
}

/// Reports whether `token`, at the start of a line, continues the expression
/// that ends on the previous line.
fn continues_before(token: &Token<'_>) -> bool {
    match token.kind {
        TokenKind::Punct => matches!(
            token.text,
            "(" | "["
                | "."
                | "?."
                | ","
                | "="
                | "=>"
                | "+"
                | "-"
                | "*"
                | "/"
                | "%"
                | "&"
                | "|"
                | "^"
                | "<"
                | ">"
                | "?"
                | ":"
        ),
        TokenKind::Word => matches!(
            token.text,
            "in" | "instanceof" | "of" | "as" | "satisfies" | "extends" | "implements" | "is"
        ),
        // A template literal after an expression is a tagged template.
        TokenKind::Value => token.text.starts_with('`'),
        TokenKind::Private => false,
    }
}

/// Reports whether `next` is cut off from the restricted keyword `previous` by
/// a line break, so automatic semicolon insertion ends the keyword's operand.
fn ends_at_line_break(previous: &Token<'_>, next: &Token<'_>) -> bool {
    next.line_break_before
        && previous.kind == TokenKind::Word
        && RESTRICTED_KEYWORDS.contains(&previous.text)
}

/// Reports whether `{` after `previous` starts the body that belongs to a
/// `class` or `interface` header instead of a brace inside the header.
fn header_body_follows(previous: Option<&Token<'_>>) -> bool {
    previous.is_some_and(|token| ends_operand(token, false) || token.is_punct(">"))
}

// ---------------------------------------------------------------------------
// Context stack
// ---------------------------------------------------------------------------

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum FrameKind {
    /// A statement list: top level, function body, static block, block.
    Block,
    ClassBody,
    /// An object literal or destructuring pattern.
    ObjectLiteral,
    /// An interface body or a type literal.
    TypeBody,
    /// A parenthesized expression, argument list or parameter list.
    Paren,
    /// An array literal, index or computed key.
    Bracket,
    /// A template literal substitution.
    Template,
}

impl FrameKind {
    fn has_members(self) -> bool {
        matches!(
            self,
            Self::Block | Self::ClassBody | Self::ObjectLiteral | Self::TypeBody
        )
    }

    fn is_brace(self) -> bool {
        self.has_members()
    }

    fn declares_named_members(self) -> bool {
        matches!(self, Self::ClassBody | Self::ObjectLiteral | Self::TypeBody)
    }
}

/// Whether the tokens being read are part of a type.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum TypeMode {
    None,
    /// After a `:` that annotates a binding, parameter, field or return type.
    Annotation,
    /// After the `=` of `type Name = ...`.
    Alias,
}

/// A `class` or `interface` keyword whose body brace has not been read yet.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Header {
    None,
    Class,
    Interface,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Decorator {
    None,
    /// After `@` or a `.` inside a decorator expression.
    AfterAt,
    /// After a decorator name, where `.` or `(` can continue it.
    AfterName,
}

#[derive(Clone, Debug)]
struct Frame<'a> {
    kind: FrameKind,
    /// The next token starts a statement or member (after modifiers).
    member_start: bool,
    /// A class field initializer is being read.
    in_initializer: bool,
    decorator: Decorator,
    type_mode: TypeMode,
    header: Header,
    /// A `type Name` statement started and its `=` has not been read yet.
    alias_candidate: bool,
    /// Unmatched `?` of conditional expressions.
    ternary: usize,
    /// Index of the last `:` that belonged to a conditional expression.
    ternary_colon: Option<usize>,
    statement_tokens: usize,
    statement_first: Option<Token<'a>>,
    /// The frame is an `interface` body, which ends its statement.
    closes_statement: bool,
}

impl Frame<'_> {
    fn new(kind: FrameKind, type_mode: TypeMode, closes_statement: bool) -> Self {
        Self {
            kind,
            member_start: kind.has_members(),
            in_initializer: false,
            decorator: Decorator::None,
            type_mode,
            header: Header::None,
            alias_candidate: false,
            ternary: 0,
            ternary_colon: None,
            statement_tokens: 0,
            statement_first: None,
            closes_statement,
        }
    }

    fn start_statement(&mut self) {
        if self.kind.has_members() {
            self.member_start = true;
        }
        self.in_initializer = false;
        self.type_mode = TypeMode::None;
        self.alias_candidate = false;
        self.ternary = 0;
        self.statement_tokens = 0;
        self.statement_first = None;
    }

    fn in_type(&self) -> bool {
        self.kind == FrameKind::TypeBody || self.type_mode != TypeMode::None
    }
}

struct Scanner<'a> {
    tokens: &'a [Token<'a>],
    stack: Vec<Frame<'a>>,
    sites: Vec<EvalSite>,
}

impl<'a> Scanner<'a> {
    fn top(&self) -> &Frame<'a> {
        self.stack.last().expect("the root frame is never popped")
    }

    fn top_mut(&mut self) -> &mut Frame<'a> {
        self.stack
            .last_mut()
            .expect("the root frame is never popped")
    }

    fn token(&self, index: usize) -> Token<'a> {
        self.tokens[index]
    }

    fn previous(&self, index: usize) -> Option<Token<'a>> {
        index.checked_sub(1).map(|previous| self.tokens[previous])
    }

    fn step(&mut self, index: usize) {
        let token = self.token(index);
        if matches!(token.template, TemplateEdge::Middle | TemplateEdge::Close) {
            self.close_template();
        }
        self.apply_line_break(index);
        let before = self.count_token(token);
        self.record_site(index);
        match token.kind {
            TokenKind::Word => self.word(index),
            TokenKind::Private | TokenKind::Value => self.operand(),
            TokenKind::Punct => self.punct(index, before),
        }
        if matches!(token.template, TemplateEdge::Open | TemplateEdge::Middle) {
            self.stack
                .push(Frame::new(FrameKind::Template, TypeMode::None, false));
        }
    }

    fn close_template(&mut self) {
        if let Some(position) = self
            .stack
            .iter()
            .rposition(|frame| frame.kind == FrameKind::Template)
        {
            self.stack.truncate(position);
        }
    }

    /// Automatic semicolon insertion: a line break ends the statement or member
    /// unless the previous token needs an operand or the next token continues
    /// the expression.
    fn apply_line_break(&mut self, index: usize) {
        let token = self.token(index);
        let Some(previous) = self.previous(index) else {
            return;
        };
        let top = self.top();
        if !token.line_break_before
            || !matches!(
                top.kind,
                FrameKind::Block | FrameKind::ClassBody | FrameKind::TypeBody
            )
        {
            return;
        }
        if !continues_after(&previous, top.in_type()) && !continues_before(&token) {
            self.top_mut().start_statement();
        }
    }

    /// Counts `token` in the current statement and returns how many tokens
    /// preceded it.
    fn count_token(&mut self, token: Token<'a>) -> usize {
        let top = self.top_mut();
        let before = top.statement_tokens;
        top.statement_tokens += 1;
        if before == 0 {
            top.statement_first = Some(token);
        }
        before
    }

    fn operand(&mut self) {
        let top = self.top_mut();
        if top.kind.has_members() {
            top.member_start = false;
        }
    }

    // -- `eval` sites ------------------------------------------------------

    fn record_site(&mut self, index: usize) {
        let token = self.token(index);
        let followed_by_parenthesis = self
            .tokens
            .get(index + 1)
            .is_some_and(|next| next.is_punct("("));
        if !followed_by_parenthesis {
            return;
        }
        let kind = match token.kind {
            TokenKind::Private if token.text == "#eval" => EvalSiteKind::PrivateName,
            TokenKind::Word if token.text == "eval" => self.classify_eval(index),
            _ => return,
        };
        self.sites.push(EvalSite {
            start: token.start,
            kind,
        });
    }

    /// Decides what the `eval` word at `index`, followed by `(`, is.
    ///
    /// | Context at the token                               | Kind          |
    /// |----------------------------------------------------|---------------|
    /// | after `.` or `?.`                                   | member access |
    /// | after `function` or `function *`                    | declaration   |
    /// | decorator expression                                | direct call   |
    /// | class body, outside a field initializer             | declaration   |
    /// | type body at a member start                         | signature     |
    /// | object literal member start, then `{` or `:`        | declaration   |
    /// | object literal member start, then anything else     | direct call   |
    /// | statement start, then `{` on the same line or `:`   | declaration   |
    /// | anywhere else                                       | direct call   |
    fn classify_eval(&self, index: usize) -> EvalSiteKind {
        let previous = self.previous(index);
        if previous.is_some_and(|token| token.is_punct(".") || token.is_punct("?.")) {
            return EvalSiteKind::MemberAccess;
        }
        let function_keyword = previous.is_some_and(|token| token.is_word("function"))
            || (previous.is_some_and(|token| token.is_punct("*"))
                && index
                    .checked_sub(2)
                    .is_some_and(|before| self.tokens[before].is_word("function")));
        if function_keyword {
            return EvalSiteKind::Declaration;
        }
        let top = self.top();
        if top.decorator == Decorator::AfterAt {
            return EvalSiteKind::DirectCall;
        }
        match top.kind {
            FrameKind::ClassBody if !top.in_initializer => EvalSiteKind::Declaration,
            FrameKind::TypeBody if top.member_start => EvalSiteKind::Signature,
            FrameKind::ObjectLiteral if top.member_start => {
                match self.token_after_parameters(index) {
                    Some(next) if next.is_punct("{") || next.is_punct(":") => {
                        EvalSiteKind::Declaration
                    }
                    _ => EvalSiteKind::DirectCall,
                }
            }
            FrameKind::Block if top.member_start => match self.token_after_parameters(index) {
                Some(next)
                    if (next.is_punct("{") && !next.line_break_before) || next.is_punct(":") =>
                {
                    EvalSiteKind::Declaration
                }
                _ => EvalSiteKind::DirectCall,
            },
            _ => EvalSiteKind::DirectCall,
        }
    }

    /// The token after the parenthesis list that opens at `index + 1`.
    fn token_after_parameters(&self, index: usize) -> Option<Token<'a>> {
        let mut depth = 0usize;
        for (offset, token) in self.tokens.iter().enumerate().skip(index + 1) {
            if token.is_punct("(") {
                depth += 1;
            } else if token.is_punct(")") {
                depth = depth.checked_sub(1)?;
                if depth == 0 {
                    return self.tokens.get(offset + 1).copied();
                }
            }
        }
        None
    }

    // -- Words -------------------------------------------------------------

    /// Reports whether the `class` word at `index` starts a class header
    /// instead of being a JSX tag or attribute name (`<class-list>`,
    /// `<div class="a">`).
    ///
    /// A class header is `class`, an optional name, and then `{`, a type
    /// parameter list, `extends` or `implements`. A tag name is also rejected
    /// by its position after `<` or `</`. A boolean attribute followed by a
    /// spread (`<div class {...props}>`) has the shape of an anonymous class;
    /// the spread then closes as an empty body and the next brace is a block,
    /// so a direct call stays visible.
    fn class_header_follows(&self, index: usize) -> bool {
        let opens_tag = match self.previous(index) {
            Some(previous) if previous.is_punct("<") => true,
            Some(previous) if previous.is_punct("/") => index
                .checked_sub(2)
                .is_some_and(|before| self.tokens[before].is_punct("<")),
            _ => false,
        };
        if opens_tag {
            return false;
        }
        let mut next = index + 1;
        if self.tokens.get(next).is_some_and(|name| {
            name.kind == TokenKind::Word && !matches!(name.text, "extends" | "implements")
        }) {
            next += 1;
        }
        self.tokens.get(next).is_some_and(|token| {
            token.is_punct("{")
                || token.is_punct("<")
                || token.is_word("extends")
                || token.is_word("implements")
        })
    }

    fn word(&mut self, index: usize) {
        let token = self.token(index);
        let previous = self.previous(index);
        let after_dot = previous.is_some_and(|token| token.is_punct(".") || token.is_punct("?."));
        let next = self.tokens.get(index + 1).copied();
        let third = self.tokens.get(index + 2).copied();
        let class_header = token.text == "class" && self.class_header_follows(index);
        let top = self.top_mut();
        match top.decorator {
            Decorator::AfterAt => {
                top.decorator = Decorator::AfterName;
                return;
            }
            Decorator::AfterName if after_dot => {
                top.decorator = Decorator::AfterAt;
                return;
            }
            Decorator::AfterName => top.decorator = Decorator::None,
            Decorator::None => {}
        }
        if after_dot {
            if top.kind.has_members() {
                top.member_start = false;
            }
            return;
        }
        let at_start = top.kind.has_members() && top.member_start;
        match token.text {
            "class" if at_start && top.kind.declares_named_members() => {
                // `class` is a member name in a class body, object literal or
                // type body, not the keyword of a nested class.
                top.member_start = false;
            }
            "class" if class_header => {
                top.header = Header::Class;
                top.member_start = false;
            }
            "interface"
                if at_start
                    && top.kind == FrameKind::Block
                    && next.is_some_and(|next| next.kind == TokenKind::Word) =>
            {
                top.header = Header::Interface;
                top.member_start = false;
            }
            "type"
                if at_start
                    && top.kind == FrameKind::Block
                    && next.is_some_and(|next| next.kind == TokenKind::Word)
                    && third.is_some_and(|third| third.is_punct("=") || third.is_punct("<")) =>
            {
                top.alias_candidate = true;
                top.member_start = false;
            }
            text if at_start
                && top.kind == FrameKind::Block
                && STATEMENT_MODIFIERS.contains(&text)
                && next.is_some_and(|next| next.kind == TokenKind::Word) => {}
            text if at_start
                && top.kind.declares_named_members()
                && MEMBER_MODIFIERS.contains(&text)
                && next.is_some_and(|next| starts_member_name(&next, text)) => {}
            _ => {
                if top.kind.has_members() {
                    top.member_start = false;
                }
            }
        }
    }

    // -- Punctuation -------------------------------------------------------

    fn punct(&mut self, index: usize, before: usize) {
        let token = self.token(index);
        match token.text {
            "(" => self.open_delimiter(FrameKind::Paren),
            "[" => self.open_delimiter(FrameKind::Bracket),
            "{" => self.open_brace(index),
            ")" | "]" | "}" => self.close_delimiter(token.text),
            ";" => {
                let top = self.top_mut();
                top.start_statement();
                top.header = Header::None;
            }
            "," => {
                let top = self.top_mut();
                match top.kind {
                    FrameKind::ObjectLiteral | FrameKind::TypeBody => top.member_start = true,
                    _ => {
                        if top.type_mode == TypeMode::Annotation {
                            top.type_mode = TypeMode::None;
                        }
                        if top.kind.has_members() {
                            top.member_start = false;
                        }
                    }
                }
            }
            "=" => self.assignment(),
            "=>" => {
                let top = self.top_mut();
                if top.kind == FrameKind::Block && top.type_mode == TypeMode::Annotation {
                    top.type_mode = TypeMode::None;
                }
                if top.kind.has_members() {
                    top.member_start = false;
                }
            }
            "?" => {
                let optional = self.tokens.get(index + 1).is_some_and(|next| {
                    next.kind == TokenKind::Punct
                        && matches!(next.text, ":" | ")" | "," | "=" | ";")
                });
                let top = self.top_mut();
                if !optional {
                    top.ternary += 1;
                }
                if top.kind.has_members() {
                    top.member_start = false;
                }
            }
            ":" => self.colon(index, before),
            "@" => {
                let top = self.top_mut();
                if top.kind.declares_named_members()
                    && top.member_start
                    && top.decorator == Decorator::None
                {
                    top.decorator = Decorator::AfterAt;
                } else if top.kind.has_members() {
                    top.member_start = false;
                }
            }
            "." | "?." => {
                let top = self.top_mut();
                if top.decorator == Decorator::AfterName {
                    top.decorator = Decorator::AfterAt;
                } else if top.kind.has_members() {
                    top.member_start = false;
                }
            }
            "*" => {
                // A generator marker keeps the member start; elsewhere it is
                // an operator.
                let top = self.top_mut();
                if !(top.kind.declares_named_members() && top.member_start)
                    && top.kind.has_members()
                {
                    top.member_start = false;
                }
            }
            _ => {
                let top = self.top_mut();
                if top.kind.has_members() {
                    top.member_start = false;
                }
            }
        }
    }

    fn open_delimiter(&mut self, kind: FrameKind) {
        let top = self.top_mut();
        let inherited = if top.type_mode != TypeMode::None {
            top.type_mode
        } else if top.kind == FrameKind::TypeBody {
            TypeMode::Annotation
        } else {
            TypeMode::None
        };
        // A decorator call keeps the member start of the member it decorates.
        if top.decorator == Decorator::AfterName {
            top.decorator = Decorator::None;
        } else if top.kind.has_members() {
            top.member_start = false;
        }
        self.stack.push(Frame::new(kind, inherited, false));
    }

    fn open_brace(&mut self, index: usize) {
        let brace = self.token(index);
        let previous = self.previous(index);
        let top = self.top();
        let header = top.header;
        let header_body = header != Header::None && header_body_follows(previous.as_ref());
        let kind = if header_body {
            match header {
                Header::Interface => FrameKind::TypeBody,
                _ => FrameKind::ClassBody,
            }
        } else if top.kind == FrameKind::TypeBody {
            FrameKind::TypeBody
        } else {
            match top.type_mode {
                TypeMode::Alias => FrameKind::TypeBody,
                TypeMode::Annotation => {
                    if previous.is_some_and(|token| token.is_punct("=>") || ends_type(&token)) {
                        FrameKind::Block
                    } else {
                        FrameKind::TypeBody
                    }
                }
                TypeMode::None => expression_brace(top, &brace, index, previous.as_ref()),
            }
        };
        let top = self.top_mut();
        if header_body {
            top.header = Header::None;
        }
        if kind == FrameKind::Block && top.type_mode == TypeMode::Annotation {
            // The brace is the body that follows the annotation.
            top.type_mode = TypeMode::None;
        }
        if top.kind.has_members() {
            top.member_start = false;
        }
        let closes_statement = header_body && header == Header::Interface;
        self.stack
            .push(Frame::new(kind, TypeMode::None, closes_statement));
    }

    fn close_delimiter(&mut self, text: &str) {
        let matches_kind = |kind: FrameKind| match text {
            ")" => kind == FrameKind::Paren,
            "]" => kind == FrameKind::Bracket,
            _ => kind.is_brace(),
        };
        // Search down to, but not into, the root frame and a template frame;
        // an unbalanced closer is ignored.
        let mut found = None;
        for (position, frame) in self.stack.iter().enumerate().rev() {
            if position == 0 || frame.kind == FrameKind::Template {
                break;
            }
            if matches_kind(frame.kind) {
                found = Some(position);
                break;
            }
        }
        let Some(position) = found else {
            return;
        };
        let popped = self.stack[position].clone();
        self.stack.truncate(position);
        if text != "}" {
            return;
        }
        let parent = self.top_mut();
        let ends_statement = match popped.kind {
            FrameKind::Block | FrameKind::ClassBody => parent.kind == FrameKind::Block,
            FrameKind::TypeBody => parent.kind == FrameKind::Block && popped.closes_statement,
            _ => false,
        };
        let ends_member = popped.kind == FrameKind::Block
            && parent.kind == FrameKind::ClassBody
            && !parent.in_initializer;
        if ends_statement {
            parent.start_statement();
        } else if ends_member {
            parent.member_start = true;
        }
    }

    fn assignment(&mut self) {
        let top = self.top_mut();
        match top.kind {
            FrameKind::ClassBody => {
                top.in_initializer = true;
                top.type_mode = TypeMode::None;
                top.member_start = false;
            }
            FrameKind::Block => {
                if top.alias_candidate {
                    top.type_mode = TypeMode::Alias;
                } else if top.type_mode != TypeMode::Alias {
                    top.type_mode = TypeMode::None;
                }
                top.member_start = false;
            }
            FrameKind::Paren | FrameKind::Bracket => top.type_mode = TypeMode::None,
            FrameKind::ObjectLiteral | FrameKind::TypeBody => top.member_start = false,
            FrameKind::Template => {}
        }
    }

    fn colon(&mut self, index: usize, before: usize) {
        let top = self.top_mut();
        if top.ternary > 0 {
            top.ternary -= 1;
            top.ternary_colon = Some(index);
            if top.kind.has_members() {
                top.member_start = false;
            }
            return;
        }
        match top.kind {
            FrameKind::Block => {
                let label = top.statement_first.is_some_and(|first| {
                    first.kind == TokenKind::Word
                        && (before == 1 || matches!(first.text, "case" | "default"))
                });
                if label {
                    // A case clause or a label starts a new statement.
                    top.start_statement();
                } else {
                    top.type_mode = TypeMode::Annotation;
                    top.member_start = false;
                }
            }
            FrameKind::ClassBody => {
                if !top.in_initializer {
                    top.type_mode = TypeMode::Annotation;
                }
                top.member_start = false;
            }
            FrameKind::Paren => top.type_mode = TypeMode::Annotation,
            FrameKind::ObjectLiteral | FrameKind::TypeBody => top.member_start = false,
            FrameKind::Bracket | FrameKind::Template => {}
        }
    }
}

/// Reports whether `token` ends a type, after which `{` starts a body.
fn ends_type(token: &Token<'_>) -> bool {
    ends_operand(token, true) || token.is_punct(">")
}

/// Reports whether `next` can start the member name that follows `modifier`.
fn starts_member_name(next: &Token<'_>, modifier: &str) -> bool {
    match next.kind {
        TokenKind::Word | TokenKind::Value | TokenKind::Private => true,
        TokenKind::Punct => {
            matches!(next.text, "[" | "*" | "@") || (modifier == "static" && next.text == "{")
        }
    }
}

/// Classifies a `{` in expression position from the token before it.
fn expression_brace(
    top: &Frame<'_>,
    brace: &Token<'_>,
    index: usize,
    previous: Option<&Token<'_>>,
) -> FrameKind {
    let Some(previous) = previous else {
        return FrameKind::Block;
    };
    if ends_at_line_break(previous, brace) {
        return FrameKind::Block;
    }
    match previous.kind {
        TokenKind::Punct => match previous.text {
            ")" | "=>" | ";" | "{" | "}" | "]" | ">" => FrameKind::Block,
            // A property value or a conditional branch is an object literal;
            // a case clause or a label is a statement.
            ":" => {
                if top.kind == FrameKind::ObjectLiteral || top.ternary_colon == index.checked_sub(1)
                {
                    FrameKind::ObjectLiteral
                } else {
                    FrameKind::Block
                }
            }
            _ => FrameKind::ObjectLiteral,
        },
        TokenKind::Word if OBJECT_LITERAL_KEYWORDS.contains(&previous.text) => {
            FrameKind::ObjectLiteral
        }
        _ => FrameKind::Block,
    }
}

#[cfg(test)]
mod tests {
    use super::{super::non_code_ranges, *};

    const CALL: EvalSiteKind = EvalSiteKind::DirectCall;
    const DECLARATION: EvalSiteKind = EvalSiteKind::Declaration;
    const SIGNATURE: EvalSiteKind = EvalSiteKind::Signature;
    const MEMBER: EvalSiteKind = EvalSiteKind::MemberAccess;
    const PRIVATE: EvalSiteKind = EvalSiteKind::PrivateName;

    fn kinds(source: &str) -> Vec<EvalSiteKind> {
        classify_eval_sites(source, &non_code_ranges(source))
            .into_iter()
            .map(|site| site.kind)
            .collect()
    }

    fn assert_table(rows: &[(&str, &[EvalSiteKind])]) {
        for (source, expected) in rows {
            assert_eq!(kinds(source), *expected, "{source}");
        }
    }

    #[test]
    fn keyword_operators_continue_a_field_initializer_across_lines() {
        assert_table(&[
            ("class C { value = typeof\n eval(code) }", &[CALL]),
            ("class C { value = void\n eval(code) }", &[CALL]),
            ("class C { value = delete\n eval(code) }", &[CALL]),
            ("class C { value = await\n eval(code) }", &[CALL]),
            ("class C { value = yield\n eval(code) }", &[CALL]),
            ("class C { value = new\n eval(code) }", &[CALL]),
            ("class C { value = a in\n eval(code) }", &[CALL]),
            ("class C { value = a instanceof\n eval(code) }", &[CALL]),
            ("class C { value = a +\n eval(code) }", &[CALL]),
            ("class C { value = a ?? \n eval(code) }", &[CALL]),
            ("class C { value = a\n  + eval(code) }", &[CALL]),
            // A value ends the initializer, so the next line is a member.
            (
                "class C { value = typeof x\n eval(code) {} }",
                &[DECLARATION],
            ),
            ("class C { value = 1\n eval(code) {} }", &[DECLARATION]),
            ("class C { value = a\n eval(code) {} }", &[DECLARATION]),
        ]);
    }

    #[test]
    fn braces_in_a_class_heritage_do_not_hide_the_class_keyword() {
        assert_table(&[
            ("class C extends mixin({}) { eval(x)\n {} }", &[DECLARATION]),
            (
                "class C extends mixin({ a: 1 }) { value = 1\n eval(x) {} }",
                &[DECLARATION],
            ),
            ("class C extends (class {}) { eval(x) {} }", &[DECLARATION]),
            (
                "const C = class extends mixin({}) { eval(x) {} };",
                &[DECLARATION],
            ),
            ("class C extends Base {\n  eval(x) {}\n}", &[DECLARATION]),
            // Allman style puts the body brace on its own line.
            ("class C extends Base\n{\n  eval(x) {}\n}", &[DECLARATION]),
            (
                "class C<T extends { a: number }> extends Base<T> { eval(x) {} }",
                &[DECLARATION],
            ),
            ("class C implements I { eval(x) {} }", &[DECLARATION]),
            ("export default class { eval(x) {} }", &[DECLARATION]),
            ("export abstract class C { eval(x) {} }", &[DECLARATION]),
            ("@dec class C { eval(x) {} }", &[DECLARATION]),
        ]);
    }

    #[test]
    fn a_member_named_class_is_not_a_class_header() {
        assert_table(&[
            ("const o = { class () { eval(code); } }", &[CALL]),
            ("const o = { class() { eval(code); } }", &[CALL]),
            (
                "const o = { class() { eval(code); }, eval(v) {} }",
                &[CALL, DECLARATION],
            ),
            ("class C { class() { eval(code); } }", &[CALL]),
            ("class C { static class() {} eval(v) {} }", &[DECLARATION]),
            ("class C { class = 1\n eval(v) {} }", &[DECLARATION]),
            ("const o = { class: 1, eval(v) {} };", &[DECLARATION]),
            ("x.class\neval(code)", &[CALL]),
        ]);
    }

    #[test]
    fn restricted_keywords_end_before_a_brace_on_the_next_line() {
        assert_table(&[
            // A line break after the keyword ends it; the brace is a block.
            ("function* f() { yield\n{ eval(code) } }", &[CALL]),
            ("function f() { return\n{ eval(code) } }", &[CALL]),
            ("function f() { throw\n{ eval(code) } }", &[CALL]),
            ("function* f() { yield /*\n*/ { eval(code) } }", &[CALL]),
            ("function* f() { yield // note\n{ eval(code) } }", &[CALL]),
            ("function f() { return\n{ eval(code); } }", &[CALL]),
            ("function f() { return\n{ eval(v) {} } }", &[DECLARATION]),
            // On the same line the brace is an object literal.
            ("function* f() { yield { eval(v) {} } }", &[DECLARATION]),
            ("function f() { return { eval(v) {} } }", &[DECLARATION]),
            ("function f() { throw { eval(v) {} } }", &[DECLARATION]),
            (
                "function f() { return /*\n*/ { eval(v) {} } }",
                &[DECLARATION],
            ),
            // Other keywords continue across the line break.
            (
                "function* f() { x = typeof\n{ eval(v) {} } }",
                &[DECLARATION],
            ),
            (
                "async function f() { await\n{ eval(v) {} } }",
                &[DECLARATION],
            ),
            ("const x = a in\n{ eval(v) {} };", &[DECLARATION]),
        ]);
    }

    #[test]
    fn jsx_names_are_not_class_headers() {
        assert_table(&[
            (
                "const view = <class-list>{eval(code)}</class-list>;",
                &[CALL],
            ),
            ("const view = <class>{eval(code)}</class>;", &[CALL]),
            (
                "const view = <class.Item>{eval(code)}</class.Item>;",
                &[CALL],
            ),
            ("const view = <ns:class>{eval(code)}</ns:class>;", &[CALL]),
            ("const view = <class-list />;\neval(code);", &[CALL]),
            (
                "const view = <class {...props}>{eval(code)}</class>;",
                &[CALL],
            ),
            ("const view = <div class=\"a\">{eval(code)}</div>;", &[CALL]),
            (
                "const view = <div class={name}>{eval(code)}</div>;",
                &[CALL],
            ),
            ("const view = <div class>{eval(code)}</div>;", &[CALL]),
            ("const view = <div title={eval(code)} />;", &[CALL]),
            ("const view = <div class />;\neval(code);", &[CALL]),
            (
                "const view = <div class id=\"a\">{eval(code)}</div>;",
                &[CALL],
            ),
            (
                "const view = <div id=\"a\" class>{eval(code)}</div>;",
                &[CALL],
            ),
            (
                "const view = <div class {...props}>{eval(code)}</div>;",
                &[CALL],
            ),
            // A class keyword that follows a tag still opens a class header.
            (
                "const view = <class-list />;\nclass C { eval(v) {} }",
                &[DECLARATION],
            ),
            ("const C = class<T> { eval(v) {} };", &[DECLARATION]),
            ("const C = class Named { eval(v) {} };", &[DECLARATION]),
            (
                "const C = class implements I { eval(v) {} };",
                &[DECLARATION],
            ),
        ]);
    }

    #[test]
    fn private_names_are_not_the_global_eval() {
        assert_table(&[
            ("class C { #eval(value) {} }", &[PRIVATE]),
            ("class C { static #eval(value) {} }", &[PRIVATE]),
            ("class C { async #eval(value) {} }", &[PRIVATE]),
            ("this.#eval(value);", &[PRIVATE]),
            (
                "class C { #eval(value) {} run() { return this.#eval(1); } }",
                &[PRIVATE, PRIVATE],
            ),
            (
                "class C { #eval(value) {} run() { eval(code); } }",
                &[PRIVATE, CALL],
            ),
            ("other?.#eval(value);", &[PRIVATE]),
            ("eval(code); this.#eval(value);", &[CALL, PRIVATE]),
        ]);
    }

    #[test]
    fn bodyless_members_are_signatures() {
        assert_table(&[
            ("interface X { eval(value: string): void; }", &[SIGNATURE]),
            ("interface X { eval(value: string): void }", &[SIGNATURE]),
            (
                "export interface X { eval(value: string): void }",
                &[SIGNATURE],
            ),
            (
                "declare interface X { eval(value: string): void }",
                &[SIGNATURE],
            ),
            (
                "interface X<T> extends Y<T> { eval(value: T): void; run(): void }",
                &[SIGNATURE],
            ),
            (
                "interface X\n{\n  eval(value: string): void\n  eval(value: number): void\n}",
                &[SIGNATURE, SIGNATURE],
            ),
            (
                "interface X { run(): void\n  eval(value: string): void }",
                &[SIGNATURE],
            ),
            ("type X = { eval(value: string): void }", &[SIGNATURE]),
            ("type X = { eval(value: string): void; }", &[SIGNATURE]),
            ("type X = A & { eval(value: string): void; }", &[SIGNATURE]),
            ("type X = A | { eval(value: string): void }", &[SIGNATURE]),
            (
                "export type X<T = string> = { eval(value: T): void }",
                &[SIGNATURE],
            ),
            ("type X = {\n  eval(value: string): void\n}", &[SIGNATURE]),
            (
                "const x: { eval(value: string): void } = impl;",
                &[SIGNATURE],
            ),
            (
                "let x: { a: number; eval(value: string): void };",
                &[SIGNATURE],
            ),
            (
                "function f(x: { eval(value: string): void }) { return x; }",
                &[SIGNATURE],
            ),
            (
                "function f(): { eval(value: string): void } { return impl; }",
                &[SIGNATURE],
            ),
            (
                "const f = (x: { eval(value: string): void }): void => {};",
                &[SIGNATURE],
            ),
            (
                "class C { value: { eval(v: string): void } = impl\n eval(v) {} }",
                &[SIGNATURE, DECLARATION],
            ),
            // Real calls next to signatures are still calls.
            (
                "interface X { eval(value: string): void }\neval(code);",
                &[SIGNATURE, CALL],
            ),
            (
                "type X = {\n  eval(value: string): void\n}\neval(code)",
                &[SIGNATURE, CALL],
            ),
            (
                "type X = { eval(value: string): void }; eval(code)",
                &[SIGNATURE, CALL],
            ),
        ]);
    }

    #[test]
    fn declarations_and_members_of_classes_and_objects() {
        assert_table(&[
            ("class C { eval(value) { return value; } }", &[DECLARATION]),
            ("class C { run() {} eval(value) {} }", &[DECLARATION]),
            ("class C { async eval(value) {} }", &[DECLARATION]),
            ("class C { static async eval(value) {} }", &[DECLARATION]),
            ("class C { get eval() { return 1; } }", &[DECLARATION]),
            ("class C { set eval(value) {} }", &[DECLARATION]),
            ("class C { *eval(value) {} }", &[DECLARATION]),
            ("class C { async *eval(value) {} }", &[DECLARATION]),
            (
                "class C { public eval(value: string): void {} }",
                &[DECLARATION],
            ),
            (
                "class C { eval(o): Promise<{ ok: boolean }> { return null; } }",
                &[DECLARATION],
            ),
            (
                "class C { eval(v: string): void; eval(v: number): void; eval(v: any) {} }",
                &[DECLARATION, DECLARATION, DECLARATION],
            ),
            (
                "abstract class C { abstract eval(value: string): void; }",
                &[DECLARATION],
            ),
            ("declare class C { eval(value); }", &[DECLARATION]),
            ("class C { @dec eval(value) {} }", &[DECLARATION]),
            ("class C { @dec()\n eval(value) {} }", &[DECLARATION]),
            ("class C { @a.b(options)\n eval(value) {} }", &[DECLARATION]),
            ("class C { @dec static eval(value) {} }", &[DECLARATION]),
            ("class C { value = 1\n eval(input) {} }", &[DECLARATION]),
            ("class C { value = 1; eval(input) {} }", &[DECLARATION]),
            ("class C { value\n eval(input) {} }", &[DECLARATION]),
            ("class C { value: number\n eval(input) {} }", &[DECLARATION]),
            (
                "class C { value = () => 1\n eval(input) {} }",
                &[DECLARATION],
            ),
            (
                "class C { value = { a: 1 }\n eval(input) {} }",
                &[DECLARATION],
            ),
            (
                "class C { value = [1, 2]\n eval(input) {} }",
                &[DECLARATION],
            ),
            (
                "class C { value = 'text'\n eval(input) {} }",
                &[DECLARATION],
            ),
            (
                "class C { value = `text`\n eval(input) {} }",
                &[DECLARATION],
            ),
            ("class C { static {} eval(input) {} }", &[DECLARATION]),
            (
                "const o = { eval(value) { return value; } };",
                &[DECLARATION],
            ),
            (
                "const o = { eval(value)\n{ return value; } };",
                &[DECLARATION],
            ),
            ("const o = { a: 1, eval(value) {} };", &[DECLARATION]),
            ("const o = { async *eval(value) {} };", &[DECLARATION]),
            ("const o = { get eval() { return 1; } };", &[DECLARATION]),
            ("const o = { a: { eval(value) {} } };", &[DECLARATION]),
            ("const o = ok ? { eval(value) {} } : null;", &[DECLARATION]),
            ("const o = ok ? null : { eval(value) {} };", &[DECLARATION]),
            (
                "class C { value = ok ? 1 : { eval(v) {} } }",
                &[DECLARATION],
            ),
            ("run({ eval(value) {} });", &[DECLARATION]),
            ("export default { eval(value) {} };", &[DECLARATION]),
            (
                "function f() { return { eval(value)\n{ return value; } }; }",
                &[DECLARATION],
            ),
            (
                "function f<T extends { eval(v: string): void }>() {}",
                &[DECLARATION],
            ),
            ("function eval(code) { return code; }", &[DECLARATION]),
            (
                "const f = function eval(code) { return code; };",
                &[DECLARATION],
            ),
            ("function* eval(code) { yield code; }", &[DECLARATION]),
            (
                "declare function eval(value: string): void;",
                &[DECLARATION],
            ),
            ("eval(value) { return value; }", &[DECLARATION]),
            ("run();\neval(value) { return value; }", &[DECLARATION]),
        ]);
    }

    #[test]
    fn member_accesses_are_not_calls() {
        assert_table(&[
            (
                "instance.eval(value); other?.eval(value);",
                &[MEMBER, MEMBER],
            ),
            ("instance.\n  eval(value);", &[MEMBER]),
            ("instance\n  .eval(value);", &[MEMBER]),
            ("a?.\neval(value)", &[MEMBER]),
            ("class C { run() { return this.eval(1); } }", &[MEMBER]),
            (
                "class C { eval(o) {} } new C().eval(1);",
                &[DECLARATION, MEMBER],
            ),
            ("@dec.eval(options) class C {}", &[MEMBER]),
        ]);
    }

    #[test]
    fn calls_in_expression_statement_and_initializer_positions() {
        assert_table(&[
            ("eval(code)", &[CALL]),
            ("eval(code)\n{ cleanup(); }", &[CALL]),
            ("run();\neval(code)\n{ cleanup(); }", &[CALL]),
            (
                "function run() {\n  eval(code)\n  { cleanup(); }\n}",
                &[CALL],
            ),
            (
                "const f = () => {\n  eval(code)\n  { cleanup(); }\n};",
                &[CALL],
            ),
            (
                "class C { run() {\n  eval(code)\n  { cleanup(); }\n} }",
                &[CALL],
            ),
            (
                "const o = { run() {\n  eval(code)\n  { cleanup(); }\n} };",
                &[CALL],
            ),
            ("a = 1\neval(code)\n{ cleanup(); }", &[CALL]),
            ("outer:\neval(code)\n{ cleanup(); }", &[CALL]),
            ("outer: eval(code)", &[CALL]),
            (
                "switch (x) {\n  case 1:\n    eval(code)\n    { cleanup(); }\n}",
                &[CALL],
            ),
            ("switch (x) { default: eval(code) }", &[CALL]),
            ("if (ready) eval(code);", &[CALL]),
            ("if (ready) {} eval(code);", &[CALL]),
            ("class C {} eval(code);", &[CALL]),
            ("const x = ok ? eval(a) : { b: 1 };", &[CALL]),
            ("x = cond\n  ? eval(a)\n  : { b: 1 };", &[CALL]),
            ("return eval(this.code);", &[CALL]),
            ("typeof eval(code)", &[CALL]),
            ("new eval(code)", &[CALL]),
            ("await eval(code)", &[CALL]),
            ("const v = [a, eval(b)];", &[CALL]),
            ("foo(eval(code)\n)", &[CALL]),
            ("foo(a,\n  eval(code)\n)", &[CALL]),
            ("run(...eval(code));", &[CALL]),
            ("const f = () => eval(code)\n{ cleanup(); }", &[CALL]),
            ("const f = () =>\n  eval(code)\n{ cleanup(); }", &[CALL]),
            ("const f = async () => eval(code);", &[CALL]),
            ("const f = (x): number => { return eval(x); };", &[CALL]),
            ("x = a +\n  eval(code)\n{ cleanup(); }", &[CALL]),
            ("@eval(options)\nclass C {}", &[CALL]),
            ("export default eval(code);", &[CALL]),
            ("export const value = eval(code);", &[CALL]),
            ("for (;;) eval(code);", &[CALL]),
            ("for (const a of eval(code)) {}", &[CALL]),
            ("label: for (;;) { eval(code); }", &[CALL]),
            ("(0, eval)(code); eval(other);", &[CALL]),
        ]);
    }

    #[test]
    fn calls_inside_classes_objects_and_templates() {
        assert_table(&[
            ("class C { value = eval(code) }", &[CALL]),
            ("class C { value = eval(code); }", &[CALL]),
            ("class C { value =\n    eval(code)\n}", &[CALL]),
            ("class C { value = 1 + eval(code) }", &[CALL]),
            ("class C { static value = eval(code) }", &[CALL]),
            ("class C { value = () => eval(code) }", &[CALL]),
            ("class C { value = () =>\n  eval(code)\n}", &[CALL]),
            ("class C { [eval(key)]() {} }", &[CALL]),
            ("class C { [eval(key)] = 1 }", &[CALL]),
            ("class C { @eval(options) run() {} }", &[CALL]),
            ("class C { @a.b(eval(options)) run() {} }", &[CALL]),
            ("class C { static { eval(code); } }", &[CALL]),
            (
                "class C { static {\n  eval(code)\n  { cleanup(); }\n} }",
                &[CALL],
            ),
            ("class C { static {} run() { eval(code) } }", &[CALL]),
            ("class C { run() { return eval(this.code); } }", &[CALL]),
            ("class C { value = ok ? 1 : eval(code) }", &[CALL]),
            ("class C { value = class { run() { eval(x) } } }", &[CALL]),
            ("const o = { a: eval(code) };", &[CALL]),
            ("const o = { a: 1, b: eval(code) };", &[CALL]),
            ("const o = { a: { b: eval(code) } };", &[CALL]),
            ("const o = { [eval(key)]: 1 };", &[CALL]),
            ("const o = { a: eval(code)\n};", &[CALL]),
            ("const o = { ...eval(code) };", &[CALL]),
            ("const o = { eval(value), a: 1 };", &[CALL]),
            ("const o = { a: 1, eval(value) };", &[CALL]),
            ("const o = { run() { return eval(code); } };", &[CALL]),
            ("const s = `${eval(code)}`;", &[CALL]),
            ("const s = `a${eval(code)}b${eval(other)}c`;", &[CALL, CALL]),
            ("class C { value = `${\n eval(code)}` }", &[CALL]),
            (
                "class C { value = `${eval(code)}`\n eval(input) {} }",
                &[CALL, DECLARATION],
            ),
            (
                "const s = `eval(code)`; const t = 'eval(code)'; // eval(code)",
                &[],
            ),
            ("/* eval(code) */ const r = /eval(code)/;", &[]),
        ]);
    }

    #[test]
    fn calls_after_declarations_are_still_found() {
        assert_table(&[
            (
                "class C {\n  eval(value) { return value; }\n}\nconst c = new \
                 C();\nc.eval(1);\neval(code);\n",
                &[DECLARATION, MEMBER, CALL],
            ),
            (
                "class C { #eval(v) {} eval(v) {} run() { return eval(1); } }",
                &[PRIVATE, DECLARATION, CALL],
            ),
            (
                "const o = { eval(v) {} }; eval(code);",
                &[DECLARATION, CALL],
            ),
        ]);
    }

    /// Constructs that need a real parser are reported as direct calls, which
    /// is the conservative direction. These rows pin that behavior; the same
    /// constructs are listed under "Known limits" in the foundation contract.
    #[test]
    fn known_limits_are_reported_as_possible_direct_calls() {
        assert_table(&[
            // An object type literal without a return type is read as an
            // object literal, whose bodyless member ends in `;`.
            ("function f<T extends { eval(v: string); }>() {}", &[CALL]),
            // A closing generic angle bracket at the end of a line is read as a
            // comparison, so the initializer continues onto the next line.
            ("class C { value = new Box<string>\n eval(x) {} }", &[CALL]),
            // A generic default `=` is read as the start of a field
            // initializer, which hides the member boundary after the method.
            ("class C { m<T = string>() {} eval(v) {} }", &[CALL]),
        ]);
    }
}
