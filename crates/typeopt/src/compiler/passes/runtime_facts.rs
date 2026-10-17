// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Conservative runtime facts shared by the `simplify` family
//! (`type-directed-control-flow`, `primitive-operation-simplification` and
//! `intrinsic-specialization`).
//!
//! A [`Fact`] is a set of possible runtime kinds plus an optional literal
//! value. It is derived only from runtime evidence: literals, the results of
//! operators, never-reassigned bindings with analyzable initializers,
//! parameters of closed internal functions (every call site is a known direct
//! call), and guards that dominate a read of a never-reassigned binding.
//! Checker types never contribute to a fact. They only nominate sites and
//! decide whether an edit keeps the program type-correct.
//!
//! The module also proves that built-in intrinsics are unmodified by
//! application code ([`Facts::patched`]) and provides the text helpers the
//! planners share (precedence, statement boundaries and type neutrality).
use std::collections::{HashMap, HashSet};

use super::super::analysis::{Ctx, RefKind, skip_outer, walk_up_outer};
use crate::compiler::api::*;

mod intrinsic_exposure;
mod local_receiver;
pub(in crate::compiler) use intrinsic_exposure::IntrinsicExposure;

/// Possible runtime kinds of a value.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) struct Kinds(u16);

impl Kinds {
    pub const ALL: Kinds = Kinds((1 << 10) - 1);
    pub const BIGINT: Kinds = Kinds(1 << 5);
    pub const BOOLEAN: Kinds = Kinds(1 << 2);
    pub const FUNCTION: Kinds = Kinds(1 << 8);
    /// Host objects with `[[IsHTMLDDA]]` (`document.all`): `typeof` is
    /// `"undefined"`, falsy and loosely equal to `null`.
    pub const HTMLDDA: Kinds = Kinds(1 << 9);
    /// Values loosely equal to `null`.
    pub const LOOSE_NULLISH: Kinds = Kinds(Self::NULLISH.0 | Self::HTMLDDA.0);
    pub const NONE: Kinds = Kinds(0);
    pub const NULL: Kinds = Kinds(1 << 1);
    /// Values that short-circuit `?.` and `??`.
    pub const NULLISH: Kinds = Kinds(Self::UNDEFINED.0 | Self::NULL.0);
    pub const NUMBER: Kinds = Kinds(1 << 3);
    /// Ordinary non-callable objects (`typeof` is `"object"`, truthy).
    pub const OBJECT: Kinds = Kinds(1 << 7);
    /// Values whose primitive conversion may call user code.
    pub const OBJECTS: Kinds = Kinds(Self::OBJECT.0 | Self::FUNCTION.0 | Self::HTMLDDA.0);
    /// Primitives whose string or number conversion never throws and never
    /// calls user code.
    pub const PLAIN_PRIMITIVES: Kinds =
        Kinds(Self::UNDEFINED.0 | Self::NULL.0 | Self::BOOLEAN.0 | Self::NUMBER.0 | Self::STRING.0);
    pub const STRING: Kinds = Kinds(1 << 4);
    pub const SYMBOL: Kinds = Kinds(1 << 6);
    pub const UNDEFINED: Kinds = Kinds(1);

    pub fn union(self, other: Kinds) -> Kinds {
        Kinds(self.0 | other.0)
    }

    pub fn intersect(self, other: Kinds) -> Kinds {
        Kinds(self.0 & other.0)
    }

    pub fn without(self, other: Kinds) -> Kinds {
        Kinds(self.0 & !other.0)
    }

    pub fn intersects(self, other: Kinds) -> bool {
        self.0 & other.0 != 0
    }

    /// Every possible kind is in `other`.
    pub fn within(self, other: Kinds) -> bool {
        self.0 & !other.0 == 0
    }

    pub fn is_empty(self) -> bool {
        self.0 == 0
    }
}

/// A known primitive value.
#[derive(Clone, Debug)]
pub(super) enum Lit {
    Undefined,
    Null,
    Bool(bool),
    Num(f64),
    Str(String),
}

impl Lit {
    pub fn kinds(&self) -> Kinds {
        match self {
            Lit::Undefined => Kinds::UNDEFINED,
            Lit::Null => Kinds::NULL,
            Lit::Bool(_) => Kinds::BOOLEAN,
            Lit::Num(_) => Kinds::NUMBER,
            Lit::Str(_) => Kinds::STRING,
        }
    }

    /// Identity of the value for the lattice (`NaN` equals itself, `-0`
    /// differs from `+0`).
    pub fn same(&self, other: &Lit) -> bool {
        match (self, other) {
            (Lit::Undefined, Lit::Undefined) | (Lit::Null, Lit::Null) => true,
            (Lit::Bool(a), Lit::Bool(b)) => a == b,
            (Lit::Num(a), Lit::Num(b)) => a.to_bits() == b.to_bits(),
            (Lit::Str(a), Lit::Str(b)) => a == b,
            _ => false,
        }
    }

    /// `IsStrictlyEqual`.
    pub fn strict_equals(&self, other: &Lit) -> bool {
        match (self, other) {
            (Lit::Num(a), Lit::Num(b)) => a == b,
            _ => self.same(other),
        }
    }

    pub fn truthy(&self) -> bool {
        match self {
            Lit::Undefined | Lit::Null => false,
            Lit::Bool(b) => *b,
            Lit::Num(n) => *n != 0.0 && !n.is_nan(),
            Lit::Str(s) => !s.is_empty(),
        }
    }
}

/// Conservative runtime knowledge about the value of an expression when its
/// evaluation completes normally.
#[derive(Clone, Debug)]
pub(super) struct Fact {
    pub kinds: Kinds,
    pub lit: Option<Lit>,
}

impl Fact {
    pub fn top() -> Fact {
        Fact {
            kinds: Kinds::ALL,
            lit: None,
        }
    }

    pub fn of(kinds: Kinds) -> Fact {
        Fact { kinds, lit: None }
    }

    pub fn lit(lit: Lit) -> Fact {
        Fact {
            kinds: lit.kinds(),
            lit: Some(lit),
        }
    }

    pub fn union(&self, other: &Fact) -> Fact {
        let lit = match (&self.lit, &other.lit) {
            (Some(a), Some(b)) if a.same(b) => Some(a.clone()),
            _ => None,
        };
        // An unreachable side contributes nothing.
        if self.kinds.is_empty() {
            return other.clone();
        }
        if other.kinds.is_empty() {
            return self.clone();
        }
        Fact {
            kinds: self.kinds.union(other.kinds),
            lit,
        }
    }

    /// Restricts the possible kinds (control-flow narrowing).
    pub fn restrict(&self, kinds: Kinds) -> Fact {
        let kinds = self.kinds.intersect(kinds);
        let lit = self.lit.clone().filter(|lit| lit.kinds().intersects(kinds));
        Fact { kinds, lit }
    }

    /// `ToBoolean` of the value, when it is determined.
    pub fn truthy(&self) -> Option<bool> {
        if let Some(lit) = &self.lit {
            return Some(lit.truthy());
        }
        if self.kinds.is_empty() {
            return None;
        }
        if self
            .kinds
            .within(Kinds::OBJECT.union(Kinds::FUNCTION).union(Kinds::SYMBOL))
        {
            return Some(true);
        }
        if self.kinds.within(Kinds::LOOSE_NULLISH) {
            return Some(false);
        }
        None
    }

    /// Whether the value is `undefined` or `null` (the `?.`/`??` test),
    /// when determined.
    pub fn nullish(&self) -> Option<bool> {
        if self.kinds.is_empty() {
            return None;
        }
        if self.kinds.within(Kinds::NULLISH) {
            Some(true)
        } else if !self.kinds.intersects(Kinds::NULLISH) {
            Some(false)
        } else {
            None
        }
    }

    pub fn typeof_result(&self) -> Option<&'static str> {
        let mut result = None;
        for (kind, name) in TYPEOF {
            if self.kinds.intersects(kind) {
                if result.is_some_and(|r| r != name) {
                    return None;
                }
                result = Some(name);
            }
        }
        result
    }

    pub fn is(&self, kinds: Kinds) -> bool {
        !self.kinds.is_empty() && self.kinds.within(kinds)
    }
}

const TYPEOF: [(Kinds, &str); 10] = [
    (Kinds::UNDEFINED, "undefined"),
    (Kinds::HTMLDDA, "undefined"),
    (Kinds::NULL, "object"),
    (Kinds::OBJECT, "object"),
    (Kinds::BOOLEAN, "boolean"),
    (Kinds::NUMBER, "number"),
    (Kinds::STRING, "string"),
    (Kinds::BIGINT, "bigint"),
    (Kinds::SYMBOL, "symbol"),
    (Kinds::FUNCTION, "function"),
];

/// Kinds whose `typeof` is `name`.
fn typeof_kinds(name: &str) -> Kinds {
    TYPEOF
        .iter()
        .filter(|(_, n)| *n == name)
        .fold(Kinds::NONE, |acc, (k, _)| acc.union(*k))
}

/// Owner of a patched member in [`Patches`].
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
enum Owner {
    /// The global object (`globalThis.String = ...`).
    Global,
    /// A built-in constructor and its prototype, by global name.
    Named(String),
    Any,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
enum Member {
    Named(String),
    Any,
}

/// Members of built-ins that application code may replace.
#[derive(Clone, Default)]
struct Patches {
    records: HashSet<(Owner, Member)>,
    /// Members written through a receiver that is not provably a local
    /// object (a parameter, a property read, a call result). The receiver may
    /// be an intrinsic that a caller supplied, so the write is a possible
    /// patch of that member on any owner. Only the array model consults these
    /// (see [`local_receiver`]); other passes keep their own evidence.
    unproven: HashSet<Member>,
    /// Global-object members written under a computed key with a known
    /// literal prefix (`window[`app${name}`] = ...`). Such a write can only
    /// replace a global whose name starts with the prefix.
    global_prefixes: HashSet<String>,
}

impl Patches {
    fn add(&mut self, owner: Owner, member: Member) {
        self.records.insert((owner, member));
    }

    /// The record that lets application code replace `member` of `owner`, in
    /// a stable textual form (`Array.push`, `*.push`, `Array.*`, `*.*`).
    fn record(&self, owner: &Owner, member: &str) -> Option<String> {
        let name = Member::Named(member.to_owned());
        let prefixed = (*owner == Owner::Global)
            .then(|| {
                self.global_prefixes
                    .iter()
                    .filter(|prefix| member.starts_with(prefix.as_str()))
                    .map(|prefix| format!("globalThis.{prefix}*"))
                    .min()
            })
            .flatten();
        [owner.clone(), Owner::Any]
            .iter()
            .flat_map(|o| [(o.clone(), name.clone()), (o.clone(), Member::Any)])
            .filter(|record| self.records.contains(record))
            .map(|(owner, member)| describe_record(&owner, &member))
            .chain(prefixed)
            .min()
    }

    /// The first unproven write (see [`Patches::unproven`]) of `member`, or of
    /// an unknown member, in a stable textual form.
    fn unproven_record(&self, member: &str) -> Option<String> {
        [Member::Named(member.to_owned()), Member::Any]
            .iter()
            .filter(|record| self.unproven.contains(record))
            .map(|record| describe_record(&Owner::Any, record))
            .min()
    }

    /// The first record (in textual order) that could give an array element
    /// read or write a different meaning: a write to a member of `Array`,
    /// `Object` or an unknown owner whose member is unknown or looks like a
    /// numeric key (`1`, `-1`, `1.5`, `NaN`, `Infinity`, `undefined`) or is
    /// `__proto__` (a replaced prototype can bring any numeric accessor).
    /// Prototype getters and setters for such keys see array holes and
    /// appended elements.
    fn numeric_key_record(&self) -> Option<String> {
        self.records
            .iter()
            .filter(|(owner, member)| {
                let owner_matches = match owner {
                    Owner::Any => true,
                    Owner::Named(name) => matches!(name.as_str(), "Array" | "Object"),
                    Owner::Global => false,
                };
                owner_matches
                    && match member {
                        Member::Any => true,
                        // Replacing the prototype of the chain installs any
                        // numeric accessor.
                        Member::Named(name) => name == "__proto__" || numeric_like_key(name),
                    }
            })
            .map(|(owner, member)| describe_record(owner, member))
            .chain(
                self.unproven
                    .iter()
                    .filter(|member| match member {
                        Member::Any => true,
                        Member::Named(name) => name == "__proto__" || numeric_like_key(name),
                    })
                    .map(|member| describe_record(&Owner::Any, member)),
            )
            .min()
    }
}

fn describe_record(owner: &Owner, member: &Member) -> String {
    let owner = match owner {
        Owner::Global => "globalThis",
        Owner::Named(name) => name.as_str(),
        Owner::Any => "*",
    };
    let member = match member {
        Member::Named(name) => name.as_str(),
        Member::Any => "*",
    };
    format!("{owner}.{member}")
}

/// A property name that array index reads and writes may produce: a number
/// (any form), `NaN`, `Infinity` or `undefined`.
fn numeric_like_key(name: &str) -> bool {
    name.starts_with(|c: char| c.is_ascii_digit() || matches!(c, '-' | '+' | '.'))
        || matches!(name, "NaN" | "Infinity" | "undefined")
}

/// Global names that denote the global object itself.
const GLOBAL_OBJECTS: [&str; 7] = [
    "globalThis",
    "window",
    "self",
    "global",
    "top",
    "parent",
    "frames",
];

/// Lazily computed runtime facts for one planning round.
pub(super) struct Facts {
    bindings: HashMap<SymbolId, Fact>,
    active: HashSet<SymbolId>,
    /// Facts per expression node; facts depend only on the node and the
    /// program, so each is computed once.
    expressions: HashMap<Node, Fact>,
    patches: Option<Patches>,
    depth: usize,
}

const MAX_DEPTH: usize = 48;

impl Facts {
    pub fn new() -> Self {
        Self {
            bindings: HashMap::new(),
            active: HashSet::new(),
            expressions: HashMap::new(),
            patches: None,
            depth: 0,
        }
    }

    /// Fact of an expression.
    pub fn expr(&mut self, ctx: &mut Ctx, node: Node) -> Fact {
        if let Some(fact) = self.expressions.get(&node) {
            return fact.clone();
        }
        if self.depth > MAX_DEPTH {
            return Fact::top();
        }
        self.depth += 1;
        let fact = self.expr_inner(ctx, node);
        self.depth -= 1;
        self.expressions.insert(node, fact.clone());
        fact
    }

    fn expr_inner(&mut self, ctx: &mut Ctx, node: Node) -> Fact {
        let node = skip_outer(node);
        match node.kind() {
            SyntaxKind::NumericLiteral => match parse_number(node.text()) {
                Some(n) => Fact::lit(Lit::Num(n)),
                None => Fact::of(Kinds::NUMBER),
            },
            SyntaxKind::BigIntLiteral => Fact::of(Kinds::BIGINT),
            SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral => {
                match string_value(ctx, node) {
                    Some(s) => Fact::lit(Lit::Str(s)),
                    None => Fact::of(Kinds::STRING),
                }
            }
            SyntaxKind::TemplateExpression => Fact::of(Kinds::STRING),
            SyntaxKind::TrueKeyword => Fact::lit(Lit::Bool(true)),
            SyntaxKind::FalseKeyword => Fact::lit(Lit::Bool(false)),
            SyntaxKind::NullKeyword => Fact::lit(Lit::Null),
            SyntaxKind::ObjectLiteralExpression
            | SyntaxKind::ArrayLiteralExpression
            | SyntaxKind::RegularExpressionLiteral => Fact::of(Kinds::OBJECT),
            SyntaxKind::FunctionExpression
            | SyntaxKind::ArrowFunction
            | SyntaxKind::ClassExpression => Fact::of(Kinds::FUNCTION),
            // Construction always yields an object.
            SyntaxKind::NewExpression => Fact::of(Kinds::OBJECT.union(Kinds::FUNCTION)),
            SyntaxKind::VoidExpression => Fact::lit(Lit::Undefined),
            SyntaxKind::DeleteExpression => Fact::of(Kinds::BOOLEAN),
            SyntaxKind::TypeOfExpression => {
                let operand = self.expr(ctx, node.expression());
                match operand.typeof_result() {
                    Some(name) => Fact::lit(Lit::Str(name.to_owned())),
                    None => Fact::of(Kinds::STRING),
                }
            }
            SyntaxKind::PrefixUnaryExpression => self.prefix(ctx, node),
            SyntaxKind::PostfixUnaryExpression => Fact::of(Kinds::NUMBER.union(Kinds::BIGINT)),
            SyntaxKind::BinaryExpression => self.binary(ctx, node),
            SyntaxKind::ConditionalExpression => {
                let condition = self.expr(ctx, node.condition());
                match condition.truthy() {
                    Some(true) => self.expr(ctx, node.when_true()),
                    Some(false) => self.expr(ctx, node.when_false()),
                    None => {
                        let a = self.expr(ctx, node.when_true());
                        let b = self.expr(ctx, node.when_false());
                        a.union(&b)
                    }
                }
            }
            SyntaxKind::Identifier => self.identifier(ctx, node),
            SyntaxKind::PropertyAccessExpression
                if node.question_dot_token().is_nil()
                    && node.name().kind() == SyntaxKind::Identifier
                    && node.name().text() == "length" =>
            {
                // `length` of a string primitive is an own, non-configurable
                // number.
                let receiver = self.expr(ctx, node.expression());
                if receiver.is(Kinds::STRING) {
                    Fact::of(Kinds::NUMBER)
                } else {
                    Fact::top()
                }
            }
            _ => Fact::top(),
        }
    }

    fn prefix(&mut self, ctx: &mut Ctx, node: Node) -> Fact {
        let operand = self.expr(ctx, node.operand());
        match node.operator() {
            SyntaxKind::ExclamationToken => match operand.truthy() {
                Some(b) => Fact::lit(Lit::Bool(!b)),
                None => Fact::of(Kinds::BOOLEAN),
            },
            // Unary plus throws for BigInt and otherwise yields a number.
            SyntaxKind::PlusToken => match operand.lit {
                Some(Lit::Num(n)) => Fact::lit(Lit::Num(n)),
                _ => Fact::of(Kinds::NUMBER),
            },
            SyntaxKind::MinusToken if matches!(operand.lit, Some(Lit::Num(_))) => {
                let Some(Lit::Num(n)) = operand.lit else {
                    unreachable!()
                };
                Fact::lit(Lit::Num(-n))
            }
            SyntaxKind::MinusToken | SyntaxKind::TildeToken => {
                if operand
                    .kinds
                    .intersects(Kinds::BIGINT.union(Kinds::OBJECTS))
                {
                    Fact::of(Kinds::NUMBER.union(Kinds::BIGINT))
                } else {
                    Fact::of(Kinds::NUMBER)
                }
            }
            _ => Fact::of(Kinds::NUMBER.union(Kinds::BIGINT)),
        }
    }

    fn binary(&mut self, ctx: &mut Ctx, node: Node) -> Fact {
        let operator = node.operator_token().kind();
        if matches!(binary_precedence(operator), 5..=14) {
            // Arithmetic, bitwise, relational and equality operators over
            // known primitives have an exactly computable result.
            let left = self.expr(ctx, node.left());
            if left.lit.is_some() {
                let right = self.expr(ctx, node.right());
                if let (Some(a), Some(b)) = (&left.lit, &right.lit)
                    && let Some(result) = evaluate(operator, a, b)
                {
                    return Fact::lit(result);
                }
            }
        }
        match operator {
            SyntaxKind::EqualsEqualsEqualsToken
            | SyntaxKind::ExclamationEqualsEqualsToken
            | SyntaxKind::EqualsEqualsToken
            | SyntaxKind::ExclamationEqualsToken => {
                let left = self.expr(ctx, node.left());
                let right = self.expr(ctx, node.right());
                let strict = matches!(
                    operator,
                    SyntaxKind::EqualsEqualsEqualsToken | SyntaxKind::ExclamationEqualsEqualsToken
                );
                let negate = matches!(
                    operator,
                    SyntaxKind::ExclamationEqualsEqualsToken | SyntaxKind::ExclamationEqualsToken
                );
                match equality(&left, &right, strict) {
                    Some(b) => Fact::lit(Lit::Bool(b != negate)),
                    None => Fact::of(Kinds::BOOLEAN),
                }
            }
            SyntaxKind::LessThanToken
            | SyntaxKind::GreaterThanToken
            | SyntaxKind::LessThanEqualsToken
            | SyntaxKind::GreaterThanEqualsToken
            | SyntaxKind::InstanceOfKeyword
            | SyntaxKind::InKeyword => Fact::of(Kinds::BOOLEAN),
            SyntaxKind::AmpersandAmpersandToken => {
                let left = self.expr(ctx, node.left());
                match left.truthy() {
                    Some(false) => left,
                    Some(true) => self.expr(ctx, node.right()),
                    None => {
                        let right = self.expr(ctx, node.right());
                        // Only the falsy part of the left side flows through.
                        let falsy = left
                            .restrict(Kinds::ALL.without(
                                Kinds::OBJECT.union(Kinds::FUNCTION).union(Kinds::SYMBOL),
                            ));
                        falsy.union(&right)
                    }
                }
            }
            SyntaxKind::BarBarToken => {
                let left = self.expr(ctx, node.left());
                match left.truthy() {
                    Some(true) => left,
                    Some(false) => self.expr(ctx, node.right()),
                    None => {
                        let right = self.expr(ctx, node.right());
                        let truthy = left.restrict(Kinds::ALL.without(Kinds::LOOSE_NULLISH));
                        truthy.union(&right)
                    }
                }
            }
            SyntaxKind::QuestionQuestionToken => {
                let left = self.expr(ctx, node.left());
                match left.nullish() {
                    Some(false) => left,
                    Some(true) => self.expr(ctx, node.right()),
                    None => {
                        let right = self.expr(ctx, node.right());
                        left.restrict(Kinds::ALL.without(Kinds::NULLISH))
                            .union(&right)
                    }
                }
            }
            SyntaxKind::CommaToken | SyntaxKind::EqualsToken => self.expr(ctx, node.right()),
            SyntaxKind::PlusToken => {
                let left = self.expr(ctx, node.left());
                let right = self.expr(ctx, node.right());
                if left.is(Kinds::STRING) || right.is(Kinds::STRING) {
                    Fact::of(Kinds::STRING)
                } else if left.is(Kinds::PLAIN_PRIMITIVES.without(Kinds::STRING))
                    && right.is(Kinds::PLAIN_PRIMITIVES.without(Kinds::STRING))
                {
                    Fact::of(Kinds::NUMBER)
                } else {
                    Fact::of(Kinds::STRING.union(Kinds::NUMBER).union(Kinds::BIGINT))
                }
            }
            SyntaxKind::MinusToken
            | SyntaxKind::AsteriskToken
            | SyntaxKind::SlashToken
            | SyntaxKind::PercentToken
            | SyntaxKind::AsteriskAsteriskToken
            | SyntaxKind::AmpersandToken
            | SyntaxKind::BarToken
            | SyntaxKind::CaretToken
            | SyntaxKind::LessThanLessThanToken
            | SyntaxKind::GreaterThanGreaterThanToken => {
                let left = self.expr(ctx, node.left());
                let right = self.expr(ctx, node.right());
                if left.is(Kinds::PLAIN_PRIMITIVES) && right.is(Kinds::PLAIN_PRIMITIVES) {
                    Fact::of(Kinds::NUMBER)
                } else {
                    Fact::of(Kinds::NUMBER.union(Kinds::BIGINT))
                }
            }
            SyntaxKind::GreaterThanGreaterThanGreaterThanToken => Fact::of(Kinds::NUMBER),
            _ => Fact::top(),
        }
    }

    fn identifier(&mut self, ctx: &mut Ctx, node: Node) -> Fact {
        let symbol = ctx.resolve(node);
        if symbol.is_nil() {
            return Fact::top();
        }
        let declarations = ctx.checker.sym(symbol).declarations.to_vec();
        if declarations.is_empty() {
            // The checker's `undefined` symbol has no declaration.
            return if node.text() == "undefined" {
                Fact::lit(Lit::Undefined)
            } else {
                Fact::top()
            };
        }
        if lib_only(&declarations) {
            // Non-writable, non-configurable properties of the global object.
            return match node.text() {
                "undefined" => Fact::lit(Lit::Undefined),
                "NaN" => Fact::lit(Lit::Num(f64::NAN)),
                "Infinity" => Fact::lit(Lit::Num(f64::INFINITY)),
                _ => Fact::top(),
            };
        }
        let mut fact = self.binding(ctx, symbol);
        let declaration = declarations[0];
        if declaration.kind() == SyntaxKind::VariableDeclaration
            && !declaration
                .parent()
                .flags()
                .intersects(NodeFlags::BlockScoped)
            && !initialized_before(declaration, node)
        {
            // A `var` read before its initializer runs sees `undefined`.
            fact = fact.union(&Fact::lit(Lit::Undefined));
        }
        if self.stable(ctx, symbol, declaration) {
            fact = self.narrow(ctx, node, symbol, fact);
        }
        fact
    }

    /// Whether the binding never changes after initialization, so a guard
    /// on it holds for every later read.
    fn stable(&mut self, ctx: &mut Ctx, symbol: SymbolId, declaration: Node) -> bool {
        let block_scoped = declaration.kind() == SyntaxKind::VariableDeclaration
            && declaration
                .parent()
                .flags()
                .intersects(NodeFlags::BlockScoped);
        (block_scoped || declaration.kind() == SyntaxKind::Parameter)
            && declaration.name().kind() == SyntaxKind::Identifier
            && !ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write)
    }

    /// Fact of a binding before any narrowing, memoized per symbol.
    fn binding(&mut self, ctx: &mut Ctx, symbol: SymbolId) -> Fact {
        if let Some(fact) = self.bindings.get(&symbol) {
            return fact.clone();
        }
        if !self.active.insert(symbol) {
            // Recursive definition: assume nothing.
            return Fact::top();
        }
        let fact = self.binding_inner(ctx, symbol);
        self.active.remove(&symbol);
        self.bindings.insert(symbol, fact.clone());
        fact
    }

    fn binding_inner(&mut self, ctx: &mut Ctx, symbol: SymbolId) -> Fact {
        let declarations = ctx.checker.sym(symbol).declarations.to_vec();
        if declarations.len() != 1 {
            return Fact::top();
        }
        let declaration = declarations[0];
        if declaration.flags().intersects(NodeFlags::Ambient)
            || !ctx
                .file_of_node(declaration)
                .is_some_and(|f| !ctx.files[f].js)
        {
            return Fact::top();
        }
        if ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write) {
            return Fact::top();
        }
        match declaration.kind() {
            SyntaxKind::VariableDeclaration => {
                let list = declaration.parent();
                if declaration.name().kind() != SyntaxKind::Identifier
                    || list.kind() != SyntaxKind::VariableDeclarationList
                    || list.parent().kind() != SyntaxKind::VariableStatement
                {
                    return Fact::top();
                }
                let init = declaration.initializer();
                if init.is_nil() {
                    return if list.flags().intersects(NodeFlags::Const | NodeFlags::Using) {
                        Fact::top()
                    } else {
                        Fact::lit(Lit::Undefined)
                    };
                }
                self.expr(ctx, init)
            }
            SyntaxKind::Parameter => self.parameter(ctx, declaration),
            SyntaxKind::FunctionDeclaration | SyntaxKind::ClassDeclaration => {
                Fact::of(Kinds::FUNCTION)
            }
            _ => Fact::top(),
        }
    }

    /// Union of the arguments at every call site of a closed function.
    fn parameter(&mut self, ctx: &mut Ctx, parameter: Node) -> Fact {
        if parameter.dot_dot_dot_token().is_some()
            || parameter.name().kind() != SyntaxKind::Identifier
            || !parameter.decorators().is_empty()
        {
            return Fact::top();
        }
        let function = parameter.parent();
        let Some(callee) = closed_function_symbol(ctx, function) else {
            return Fact::top();
        };
        let Some(index) = function
            .parameters()
            .iter()
            .filter(|p| !(p.name().kind() == SyntaxKind::Identifier && p.name().text() == "this"))
            .position(|p| p == parameter)
        else {
            return Fact::top();
        };
        let calls: Vec<Node> = ctx
            .refs(callee)
            .iter()
            .filter(|r| r.kind == RefKind::Call)
            .map(|r| walk_up_outer(r.node).parent())
            .collect();
        if calls.is_empty() {
            return Fact::top();
        }
        let mut fact: Option<Fact> = None;
        for call in calls {
            if call.kind() != SyntaxKind::CallExpression {
                return Fact::top();
            }
            let arguments = call.arguments().to_vec();
            if arguments
                .iter()
                .take(index + 1)
                .any(|a| a.kind() == SyntaxKind::SpreadElement)
            {
                return Fact::top();
            }
            let argument = match arguments.get(index) {
                Some(&argument) => self.expr(ctx, argument),
                None => Fact::lit(Lit::Undefined),
            };
            fact = Some(match fact {
                Some(f) => f.union(&argument),
                None => argument,
            });
        }
        let mut fact = fact.unwrap_or_else(Fact::top);
        let default = parameter.initializer();
        if default.is_some() && fact.kinds.intersects(Kinds::UNDEFINED) {
            let defined = fact.restrict(Kinds::ALL.without(Kinds::UNDEFINED));
            let default = self.expr(ctx, default);
            fact = defined.union(&default);
        }
        fact
    }

    /// Applies guards that dominate `read` to the fact of a stable binding.
    fn narrow(&mut self, ctx: &mut Ctx, read: Node, symbol: SymbolId, mut fact: Fact) -> Fact {
        let mut child = read;
        let mut node = read.parent();
        while node.is_some() && node.kind() != SyntaxKind::SourceFile {
            match node.kind() {
                SyntaxKind::IfStatement => {
                    if child == node.then_statement() {
                        fact = self.assume(ctx, node.expression(), true, symbol, fact);
                    } else if child == node.else_statement() {
                        fact = self.assume(ctx, node.expression(), false, symbol, fact);
                    }
                }
                SyntaxKind::ConditionalExpression => {
                    if child == node.when_true() {
                        fact = self.assume(ctx, node.condition(), true, symbol, fact);
                    } else if child == node.when_false() {
                        fact = self.assume(ctx, node.condition(), false, symbol, fact);
                    }
                }
                SyntaxKind::BinaryExpression if child == node.right() => {
                    match node.operator_token().kind() {
                        SyntaxKind::AmpersandAmpersandToken => {
                            fact = self.assume(ctx, node.left(), true, symbol, fact);
                        }
                        SyntaxKind::BarBarToken => {
                            fact = self.assume(ctx, node.left(), false, symbol, fact);
                        }
                        _ => {}
                    }
                }
                _ => {}
            }
            // Early exits in the same statement list. A hoisted function
            // declaration may run before the earlier statements.
            if is_statement_list(node) && child.kind() != SyntaxKind::FunctionDeclaration {
                for statement in node.statements().iter() {
                    if statement == child {
                        break;
                    }
                    if statement.kind() == SyntaxKind::IfStatement
                        && statement.else_statement().is_nil()
                        && never_completes(statement.then_statement())
                    {
                        fact = self.assume(ctx, statement.expression(), false, symbol, fact);
                    }
                }
            }
            child = node;
            node = node.parent();
        }
        fact
    }

    /// Refines `fact` (of `symbol`) assuming `condition` evaluated to
    /// `assume`.
    fn assume(
        &mut self,
        ctx: &mut Ctx,
        condition: Node,
        assume: bool,
        symbol: SymbolId,
        fact: Fact,
    ) -> Fact {
        let condition = skip_outer(condition);
        let is_symbol = |ctx: &mut Ctx, node: Node| {
            let node = skip_outer(node);
            node.kind() == SyntaxKind::Identifier && ctx.resolve(node) == symbol
        };
        match condition.kind() {
            SyntaxKind::PrefixUnaryExpression
                if condition.operator() == SyntaxKind::ExclamationToken =>
            {
                self.assume(ctx, condition.operand(), !assume, symbol, fact)
            }
            SyntaxKind::Identifier if is_symbol(ctx, condition) => {
                if assume {
                    let fact = fact.restrict(Kinds::ALL.without(Kinds::LOOSE_NULLISH));
                    if fact.lit.as_ref().is_some_and(|l| !l.truthy()) {
                        Fact::of(Kinds::NONE)
                    } else {
                        fact
                    }
                } else {
                    let fact = fact.restrict(
                        Kinds::ALL
                            .without(Kinds::OBJECT.union(Kinds::FUNCTION).union(Kinds::SYMBOL)),
                    );
                    if fact.lit.as_ref().is_some_and(Lit::truthy) {
                        Fact::of(Kinds::NONE)
                    } else {
                        fact
                    }
                }
            }
            SyntaxKind::BinaryExpression => {
                let operator = condition.operator_token().kind();
                match operator {
                    SyntaxKind::AmpersandAmpersandToken if assume => {
                        let fact = self.assume(ctx, condition.left(), true, symbol, fact);
                        self.assume(ctx, condition.right(), true, symbol, fact)
                    }
                    SyntaxKind::BarBarToken if !assume => {
                        let fact = self.assume(ctx, condition.left(), false, symbol, fact);
                        self.assume(ctx, condition.right(), false, symbol, fact)
                    }
                    SyntaxKind::EqualsEqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsEqualsToken
                    | SyntaxKind::EqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsToken => {
                        let strict = matches!(
                            operator,
                            SyntaxKind::EqualsEqualsEqualsToken
                                | SyntaxKind::ExclamationEqualsEqualsToken
                        );
                        let equal = assume
                            != matches!(
                                operator,
                                SyntaxKind::ExclamationEqualsEqualsToken
                                    | SyntaxKind::ExclamationEqualsToken
                            );
                        let (left, right) =
                            (skip_outer(condition.left()), skip_outer(condition.right()));
                        for (subject, other) in [(left, right), (right, left)] {
                            // typeof x === "..."
                            if subject.kind() == SyntaxKind::TypeOfExpression
                                && is_symbol(ctx, subject.expression())
                                && let Some(Lit::Str(name)) = self.expr(ctx, other).lit
                                && is_literal_node(other)
                            {
                                let kinds = typeof_kinds(&name);
                                return if equal {
                                    fact.restrict(kinds)
                                } else {
                                    fact.restrict(Kinds::ALL.without(kinds))
                                };
                            }
                            if is_symbol(ctx, subject) && is_literal_node(other) {
                                let Some(lit) = self.expr(ctx, other).lit else {
                                    continue;
                                };
                                return narrow_equal(fact, &lit, strict, equal);
                            }
                        }
                        fact
                    }
                    _ => fact,
                }
            }
            _ => fact,
        }
    }

    /// Whether application code may replace member `member` of the global
    /// object (`owner == None`) or of built-in `owner` and its prototype.
    pub fn patched(&mut self, ctx: &mut Ctx, owner: Option<&str>, member: &str) -> bool {
        self.patch_record(ctx, owner, member).is_some()
    }

    /// The patch record that makes [`Facts::patched`] true, for diagnostics.
    pub fn patch_record(
        &mut self,
        ctx: &mut Ctx,
        owner: Option<&str>,
        member: &str,
    ) -> Option<String> {
        let owner = match owner {
            Some(name) => Owner::Named(name.to_owned()),
            None => Owner::Global,
        };
        self.patches(ctx).record(&owner, member)
    }

    /// The unproven write of `member` (see [`Patches::unproven`]), if any.
    pub fn unproven_record(&mut self, ctx: &mut Ctx, member: &str) -> Option<String> {
        self.patches(ctx).unproven_record(member)
    }

    fn patches(&mut self, ctx: &mut Ctx) -> &Patches {
        if self.patches.is_none() {
            let patches = scan_patches(ctx);
            tracing::debug!(
                records = patches.records.len(),
                unproven = patches.unproven.len(),
                "intrinsic patch scan"
            );
            self.patches = Some(patches);
        }
        self.patches.as_ref().unwrap()
    }

    /// `ident` is a read of the unmodified built-in global `name`.
    pub fn intrinsic_global(&mut self, ctx: &mut Ctx, ident: Node, name: &str) -> bool {
        let ident = skip_outer(ident);
        if ident.kind() != SyntaxKind::Identifier || ident.text() != name {
            return false;
        }
        let symbol = ctx.resolve(ident);
        if symbol.is_nil() {
            return false;
        }
        let declarations = ctx.checker.sym(symbol).declarations.to_vec();
        lib_only(&declarations)
            && !ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write)
            && !self.patched(ctx, None, name)
    }

    /// `callee` is `Owner.member` where `Owner` is an unmodified built-in
    /// global and `member` is unmodified.
    pub fn intrinsic_static(
        &mut self,
        ctx: &mut Ctx,
        callee: Node,
        owner: &str,
        member: &str,
    ) -> bool {
        let callee = skip_outer(callee);
        callee.kind() == SyntaxKind::PropertyAccessExpression
            && callee.question_dot_token().is_nil()
            && callee.name().kind() == SyntaxKind::Identifier
            && callee.name().text() == member
            && self.intrinsic_global(ctx, callee.expression(), owner)
            && !self.patched(ctx, Some(owner), member)
    }

    /// Whether evaluating `node` has no observable effect and never throws.
    /// Stricter than [`Ctx::is_pure`] for bindings in their temporal dead
    /// zone; also accepts operators over proven primitives.
    pub fn pure(&mut self, ctx: &mut Ctx, node: Node) -> bool {
        if self.depth > MAX_DEPTH {
            return false;
        }
        self.depth += 1;
        let pure = self.pure_inner(ctx, node);
        self.depth -= 1;
        pure
    }

    fn pure_inner(&mut self, ctx: &mut Ctx, node: Node) -> bool {
        let node = skip_outer(node);
        match node.kind() {
            SyntaxKind::NumericLiteral
            | SyntaxKind::StringLiteral
            | SyntaxKind::BigIntLiteral
            | SyntaxKind::NoSubstitutionTemplateLiteral
            | SyntaxKind::TrueKeyword
            | SyntaxKind::FalseKeyword
            | SyntaxKind::NullKeyword
            | SyntaxKind::RegularExpressionLiteral
            | SyntaxKind::ArrowFunction
            | SyntaxKind::FunctionExpression => true,
            SyntaxKind::Identifier => read_safe(ctx, node),
            SyntaxKind::TypeOfExpression => {
                let operand = skip_outer(node.expression());
                if operand.kind() == SyntaxKind::Identifier {
                    // `typeof` of an undeclared global does not throw; a
                    // binding in its temporal dead zone does.
                    let symbol = ctx.resolve(operand);
                    symbol.is_nil() || read_safe(ctx, operand)
                } else {
                    self.pure(ctx, operand)
                }
            }
            SyntaxKind::VoidExpression => self.pure(ctx, node.expression()),
            SyntaxKind::PrefixUnaryExpression => {
                let operand = node.operand();
                match node.operator() {
                    SyntaxKind::ExclamationToken => self.pure(ctx, operand),
                    SyntaxKind::PlusToken => {
                        self.pure(ctx, operand)
                            && self.expr(ctx, operand).is(Kinds::PLAIN_PRIMITIVES)
                    }
                    SyntaxKind::MinusToken | SyntaxKind::TildeToken => {
                        self.pure(ctx, operand)
                            && self
                                .expr(ctx, operand)
                                .is(Kinds::PLAIN_PRIMITIVES.union(Kinds::BIGINT))
                    }
                    _ => false,
                }
            }
            SyntaxKind::ConditionalExpression => {
                self.pure(ctx, node.condition())
                    && self.pure(ctx, node.when_true())
                    && self.pure(ctx, node.when_false())
            }
            SyntaxKind::TemplateExpression => node.template_spans().nodes().iter().all(|span| {
                self.pure(ctx, span.expression())
                    && self
                        .expr(ctx, span.expression())
                        .is(Kinds::PLAIN_PRIMITIVES.union(Kinds::BIGINT))
            }),
            SyntaxKind::BinaryExpression => {
                let operator = node.operator_token().kind();
                let (left, right) = (node.left(), node.right());
                if !self.pure(ctx, left) || !self.pure(ctx, right) {
                    return false;
                }
                match operator {
                    SyntaxKind::EqualsEqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsEqualsToken
                    | SyntaxKind::AmpersandAmpersandToken
                    | SyntaxKind::BarBarToken
                    | SyntaxKind::QuestionQuestionToken
                    | SyntaxKind::CommaToken => true,
                    SyntaxKind::EqualsEqualsToken | SyntaxKind::ExclamationEqualsToken => {
                        // Loose equality calls user code only when an object
                        // meets a non-nullish primitive.
                        let l = self.expr(ctx, left);
                        let r = self.expr(ctx, right);
                        l.is(Kinds::LOOSE_NULLISH)
                            || r.is(Kinds::LOOSE_NULLISH)
                            || (!l.kinds.intersects(Kinds::OBJECTS)
                                && !r.kinds.intersects(Kinds::OBJECTS))
                    }
                    SyntaxKind::PlusToken
                    | SyntaxKind::MinusToken
                    | SyntaxKind::AsteriskToken
                    | SyntaxKind::SlashToken
                    | SyntaxKind::PercentToken
                    | SyntaxKind::AmpersandToken
                    | SyntaxKind::BarToken
                    | SyntaxKind::CaretToken
                    | SyntaxKind::LessThanLessThanToken
                    | SyntaxKind::GreaterThanGreaterThanToken
                    | SyntaxKind::GreaterThanGreaterThanGreaterThanToken
                    | SyntaxKind::LessThanToken
                    | SyntaxKind::GreaterThanToken
                    | SyntaxKind::LessThanEqualsToken
                    | SyntaxKind::GreaterThanEqualsToken => {
                        self.expr(ctx, left).is(Kinds::PLAIN_PRIMITIVES)
                            && self.expr(ctx, right).is(Kinds::PLAIN_PRIMITIVES)
                    }
                    _ => false,
                }
            }
            SyntaxKind::ArrayLiteralExpression => node
                .elements()
                .iter()
                .all(|e| e.kind() != SyntaxKind::SpreadElement && self.pure(ctx, e)),
            SyntaxKind::OmittedExpression => true,
            SyntaxKind::ObjectLiteralExpression => {
                node.properties().iter().all(|p| match p.kind() {
                    SyntaxKind::PropertyAssignment => {
                        p.name().kind() != SyntaxKind::ComputedPropertyName
                            && self.pure(ctx, p.initializer())
                    }
                    SyntaxKind::ShorthandPropertyAssignment => {
                        p.initializer().is_nil() && read_safe(ctx, p.name())
                    }
                    SyntaxKind::MethodDeclaration
                    | SyntaxKind::GetAccessor
                    | SyntaxKind::SetAccessor => {
                        p.name().kind() != SyntaxKind::ComputedPropertyName
                    }
                    _ => false,
                })
            }
            _ => false,
        }
    }
}

/// `IsStrictlyEqual`/`IsLooselyEqual` of two facts, when determined.
fn equality(left: &Fact, right: &Fact, strict: bool) -> Option<bool> {
    if left.kinds.is_empty() || right.kinds.is_empty() {
        return None;
    }
    if let (Some(a), Some(b)) = (&left.lit, &right.lit) {
        if strict || a.kinds() == b.kinds() {
            return Some(a.strict_equals(b));
        }
        let nullish = |l: &Lit| matches!(l, Lit::Undefined | Lit::Null);
        if nullish(a) || nullish(b) {
            return Some(nullish(a) && nullish(b));
        }
        return None;
    }
    if strict {
        if !left.kinds.intersects(right.kinds) {
            return Some(false);
        }
        return None;
    }
    // Loose comparison against `null`/`undefined` never converts.
    for (subject, other) in [(left, right), (right, left)] {
        if other.is(Kinds::NULLISH) && other.lit.is_some() {
            if subject.kinds.within(Kinds::LOOSE_NULLISH) {
                return Some(true);
            }
            if !subject.kinds.intersects(Kinds::LOOSE_NULLISH) {
                return Some(false);
            }
        }
    }
    None
}

/// Narrows `fact` by `x === lit` (or `==`) being `equal`.
fn narrow_equal(fact: Fact, lit: &Lit, strict: bool, equal: bool) -> Fact {
    let nullish = matches!(lit, Lit::Undefined | Lit::Null);
    if !strict {
        if !nullish {
            return fact;
        }
        return if equal {
            fact.restrict(Kinds::LOOSE_NULLISH)
        } else {
            fact.restrict(Kinds::ALL.without(Kinds::LOOSE_NULLISH))
        };
    }
    if equal {
        if matches!(lit, Lit::Num(n) if n.is_nan()) {
            return Fact::of(Kinds::NONE);
        }
        if fact.lit.as_ref().is_some_and(|l| !l.strict_equals(lit)) {
            return Fact::of(Kinds::NONE);
        }
        // `0 === -0`: keep the number kind without a literal.
        if matches!(lit, Lit::Num(n) if *n == 0.0) {
            return fact.restrict(Kinds::NUMBER);
        }
        let narrowed = fact.restrict(lit.kinds());
        if narrowed.kinds.is_empty() {
            narrowed
        } else {
            Fact::lit(lit.clone())
        }
    } else {
        match lit {
            Lit::Undefined | Lit::Null => fact.restrict(Kinds::ALL.without(lit.kinds())),
            _ if fact.lit.as_ref().is_some_and(|l| l.strict_equals(lit)) => Fact::of(Kinds::NONE),
            _ => fact,
        }
    }
}

/// The symbol of a function whose every runtime reference is a direct call
/// from indexed application TypeScript.
pub(super) fn closed_function_symbol(ctx: &mut Ctx, function: Node) -> Option<SymbolId> {
    let symbol = match function.kind() {
        SyntaxKind::FunctionDeclaration if function.name().is_some() => {
            ctx.resolve(function.name())
        }
        SyntaxKind::FunctionExpression | SyntaxKind::ArrowFunction => {
            let declaration = function.parent();
            if declaration.kind() != SyntaxKind::VariableDeclaration
                || declaration.initializer() != function
                || declaration.name().kind() != SyntaxKind::Identifier
                || !declaration.parent().flags().intersects(NodeFlags::Const)
            {
                return None;
            }
            ctx.resolve(declaration.name())
        }
        _ => return None,
    };
    if symbol.is_nil()
        || ctx.checker.sym(symbol).declarations.len() != 1
        || !ctx.is_internal(symbol)
    {
        return None;
    }
    let closed = ctx.refs(symbol).iter().all(|r| {
        matches!(
            r.kind,
            RefKind::Declaration | RefKind::Alias | RefKind::Type | RefKind::Call
        )
    });
    closed.then_some(symbol)
}

/// Every declaration comes from a bundled TypeScript library file.
pub(super) fn lib_only(declarations: &[Node]) -> bool {
    !declarations.is_empty()
        && declarations.iter().all(|&d| {
            source_file_info(get_source_file_of_node(d))
                .file_name
                .starts_with("bundled:")
        })
}

pub(super) fn is_statement_list(node: Node) -> bool {
    matches!(
        node.kind(),
        SyntaxKind::Block
            | SyntaxKind::SourceFile
            | SyntaxKind::ModuleBlock
            | SyntaxKind::CaseClause
            | SyntaxKind::DefaultClause
    )
}

/// The statement never completes normally: it always returns, throws,
/// breaks or continues.
pub(super) fn never_completes(statement: Node) -> bool {
    match statement.kind() {
        SyntaxKind::ReturnStatement
        | SyntaxKind::ThrowStatement
        | SyntaxKind::BreakStatement
        | SyntaxKind::ContinueStatement => true,
        SyntaxKind::Block => statement.statements().last().is_some_and(never_completes),
        _ => false,
    }
}

/// A syntactic literal (the value is known without evaluating anything).
pub(super) fn is_literal_node(node: Node) -> bool {
    let node = skip_outer(node);
    match node.kind() {
        SyntaxKind::NumericLiteral
        | SyntaxKind::StringLiteral
        | SyntaxKind::NoSubstitutionTemplateLiteral
        | SyntaxKind::TrueKeyword
        | SyntaxKind::FalseKeyword
        | SyntaxKind::NullKeyword => true,
        SyntaxKind::Identifier => node.text() == "undefined",
        SyntaxKind::PrefixUnaryExpression => {
            node.operator() == SyntaxKind::MinusToken
                && skip_outer(node.operand()).kind() == SyntaxKind::NumericLiteral
        }
        _ => false,
    }
}

/// Parses a numeric literal's text (decimal, hex, octal, binary).
pub(super) fn parse_number(text: &str) -> Option<f64> {
    let text: String = text.chars().filter(|&c| c != '_').collect();
    let radix = |prefix: &[&str], radix| {
        prefix
            .iter()
            .find_map(|p| text.strip_prefix(p))
            .and_then(|digits| u64::from_str_radix(digits, radix).ok())
            .filter(|&v| v <= (1u64 << 53))
            .map(|v| v as f64)
    };
    if text.len() > 1 && text.starts_with('0') && text.as_bytes()[1].is_ascii_alphabetic() {
        return radix(&["0x", "0X"], 16)
            .or_else(|| radix(&["0o", "0O"], 8))
            .or_else(|| radix(&["0b", "0B"], 2));
    }
    if text.len() > 1 && text.starts_with('0') && text.as_bytes()[1].is_ascii_digit() {
        // Legacy octal-like literals are not valid in modules; be safe.
        return None;
    }
    let value: f64 = text.parse().ok()?;
    value.is_finite().then_some(value)
}

/// The value of a string literal whose source has no escape sequences, so
/// the cooked text is exactly the raw text.
pub(super) fn string_value(ctx: &Ctx, node: Node) -> Option<String> {
    let file = ctx.file_of_node(node)?;
    let raw = ctx.text(file, node);
    if raw.len() < 2 || raw.contains('\\') {
        return None;
    }
    Some(raw[1..raw.len() - 1].to_owned())
}

/// Whether a read of identifier `read` can neither throw (temporal dead
/// zone, missing global) nor observe anything.
pub(super) fn read_safe(ctx: &mut Ctx, read: Node) -> bool {
    let symbol = ctx.resolve(read);
    if symbol.is_nil() {
        return false;
    }
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    if declarations.is_empty() {
        return read.text() == "undefined";
    }
    if lib_only(&declarations) {
        return matches!(read.text(), "undefined" | "NaN" | "Infinity" | "globalThis");
    }
    if declarations.len() != 1 {
        return false;
    }
    let declaration = declarations[0];
    if ctx.file_of_node(declaration).is_none() {
        return false;
    }
    match declaration.kind() {
        SyntaxKind::Parameter => {
            // A default initializer may read a later parameter in its
            // temporal dead zone.
            let function = declaration.parent();
            !function
                .parameters()
                .iter()
                .any(|p| p.initializer().is_some() && contains(p, read))
        }
        SyntaxKind::FunctionDeclaration => true,
        SyntaxKind::VariableDeclaration => {
            let list = declaration.parent();
            !list.flags().intersects(NodeFlags::BlockScoped)
                || runs_after(ctx, declaration, read, &mut HashSet::new())
        }
        _ => false,
    }
}

/// Whether the variable declared by `declaration` is certainly initialized
/// whenever `read` executes: the read lies in a later statement of the same
/// statement list, and that statement is not a hoisted function
/// declaration.
pub(super) fn initialized_before(declaration: Node, read: Node) -> bool {
    let list = declaration.parent();
    if list.kind() != SyntaxKind::VariableDeclarationList {
        return false;
    }
    let statement = list.parent();
    if statement.kind() != SyntaxKind::VariableStatement {
        return false;
    }
    let container = statement.parent();
    if !is_statement_list(container)
        || get_source_file_of_node(container) != get_source_file_of_node(read)
    {
        return false;
    }
    let mut node = read;
    while node.is_some() && node.parent() != container {
        node = node.parent();
    }
    node.is_some()
        && node != statement
        && node.pos() >= statement.end()
        && node.kind() != SyntaxKind::FunctionDeclaration
}

/// Whether `node` can only execute after `declaration` was initialized:
/// either [`initialized_before`] holds, or `node` lies in a hoisted function
/// declaration of the same statement list whose every call site (all known
/// direct calls) itself runs after the initialization. Calls from inside a
/// function only run while that function runs, so recursion is assumed to
/// satisfy the property.
pub(super) fn runs_after(
    ctx: &mut Ctx,
    declaration: Node,
    node: Node,
    visited: &mut HashSet<Node>,
) -> bool {
    if initialized_before(declaration, node) {
        return true;
    }
    let statement = declaration.parent().parent();
    if statement.kind() != SyntaxKind::VariableStatement {
        return false;
    }
    let container = statement.parent();
    if get_source_file_of_node(container) != get_source_file_of_node(node) {
        return false;
    }
    let mut top = node;
    while top.is_some() && top.parent() != container {
        top = top.parent();
    }
    if top.is_nil() || top.kind() != SyntaxKind::FunctionDeclaration {
        return false;
    }
    if !visited.insert(top) {
        return true;
    }
    let Some(symbol) = closed_function_symbol(ctx, top) else {
        return false;
    };
    let calls: Vec<Node> = ctx
        .refs(symbol)
        .iter()
        .filter(|r| r.kind == RefKind::Call)
        .map(|r| r.node)
        .collect();
    calls
        .into_iter()
        .all(|call| runs_after(ctx, declaration, call, visited))
}

/// `inner` lies within `outer` (same file).
pub(super) fn contains(outer: Node, inner: Node) -> bool {
    let mut node = inner;
    while node.is_some() {
        if node == outer {
            return true;
        }
        node = node.parent();
    }
    false
}

/// Neither the built-in global `owner` nor its member `member` may be
/// replaced by application code or an external runtime import, according to
/// the shared intrinsic patch scan. Runs the whole scan, so callers cache it.
pub(in crate::compiler) fn builtin_intact(ctx: &mut Ctx, owner: &str, member: &str) -> bool {
    let mut facts = Facts::new();
    !facts.patched(ctx, None, owner) && !facts.patched(ctx, Some(owner), member)
}

/// The shared intrinsic patch scan, as the property flow queries it to decide
/// which `Array` built-ins it may model.
pub(in crate::compiler) struct PatchView {
    facts: Facts,
}

pub(in crate::compiler) fn patch_view() -> PatchView {
    PatchView {
        facts: Facts::new(),
    }
}

impl PatchView {
    /// The record that lets application code replace `Array.prototype`
    /// member `member` (or the `Array` constructor's), if any.
    pub fn array_member(&mut self, ctx: &mut Ctx, member: &str) -> Option<String> {
        self.facts
            .patch_record(ctx, Some("Array"), member)
            .or_else(|| self.facts.unproven_record(ctx, member))
    }

    /// The record that could change the meaning of element reads and writes
    /// (see [`Patches::numeric_key_record`]), if any.
    pub fn numeric_keys(&mut self, ctx: &mut Ctx) -> Option<String> {
        self.facts.patches(ctx).numeric_key_record()
    }

    /// The first form in the program that the intrinsic allowlist rejects, if
    /// any. The rule is documented in [`intrinsic_exposure`].
    pub fn intrinsic_exposure(&mut self, ctx: &mut Ctx) -> Option<IntrinsicExposure> {
        intrinsic_exposure::find(ctx, &mut self.facts)
    }
}

/// The calls through a local alias `const d = Object.defineProperty` when
/// `access` is exactly that initializer and the alias has one declaration, no
/// write and no use other than being called. Every other use lets the member
/// reach unknown code, so the caller falls back to the conservative record.
fn reflection_alias_calls(ctx: &mut Ctx, access: Node) -> Option<Vec<Node>> {
    let declaration = walk_up_outer(access).parent();
    if declaration.kind() != SyntaxKind::VariableDeclaration
        || declaration.name().kind() != SyntaxKind::Identifier
        || skip_outer(declaration.initializer()) != access
    {
        return None;
    }
    let symbol = ctx.resolve(declaration.name());
    if symbol.is_nil() {
        return None;
    }
    if ctx.checker.sym(symbol).declarations.len() != 1 || !ctx.is_internal(symbol) {
        // An exported alias is called by code the scan cannot see.
        return None;
    }
    let mut calls = Vec::new();
    for reference in ctx.refs(symbol) {
        match reference.kind {
            RefKind::Declaration | RefKind::Type => {}
            RefKind::Call => {
                let call = walk_up_outer(reference.node).parent();
                if call.kind() != SyntaxKind::CallExpression
                    || skip_outer(call.expression()) != reference.node
                    || call.question_dot_token().is_some()
                {
                    return None;
                }
                calls.push(call);
            }
            _ => return None,
        }
    }
    Some(calls)
}

/// The name of the reflection writer that `access` selects from the library
/// global `Object` or `Reflect` (`outer` is that global with its parentheses
/// and assertions): `Object.assign`, `Object["assign"]`, `` Object[`assign`] ``
/// and `(Object)[("assign")]` all name `assign`. Only a static name counts: a
/// dynamic key is not a writer here, and the intrinsic allowlist rejects the
/// read of its member on its own.
fn reflection_writer_name(ctx: &Ctx, access: Node, outer: Node) -> Option<String> {
    if !matches!(
        access.kind(),
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
    ) || access.expression() != outer
    {
        return None;
    }
    let name = match access.kind() {
        SyntaxKind::PropertyAccessExpression => access.name().text().to_owned(),
        SyntaxKind::ElementAccessExpression => {
            match literal_member(ctx, access.argument_expression()) {
                Member::Named(name) => name,
                _ => return None,
            }
        }
        _ => return None,
    };
    REFLECTION_WRITERS.contains(&name.as_str()).then_some(name)
}

/// Scans application code for writes to built-ins.
fn scan_patches(ctx: &mut Ctx) -> Patches {
    let mut patches = Patches::default();
    // Built-in constructors that application code lets escape as values; an
    // unknown receiver of a prototype write may be one of them.
    let mut escaping: HashSet<String> = HashSet::new();
    let mut writes: Vec<(Node, Node, Member, Option<Node>, bool)> = Vec::new();
    // Executable code outside the application cannot be inspected, so any
    // runtime import of it may replace any built-in member at initialization.
    // Bundle mode rejects imports outside the bundle before planning.
    if !crate::app::bundle_mode() && imports_external_runtime(ctx) {
        patches.add(Owner::Any, Member::Any);
    }
    for index in 0..ctx.files.len() {
        if ctx.files[index].js {
            let text = &ctx.files[index].text;
            if [
                "prototype",
                "__proto__",
                "getPrototypeOf",
                "setPrototypeOf",
                "defineProperty",
                "__defineGetter__",
                "__defineSetter__",
                "globalThis",
            ]
            .iter()
            .any(|needle| text.contains(needle))
            {
                patches.add(Owner::Any, Member::Any);
            }
            continue;
        }
        let nodes = ctx.files[index].nodes.clone();
        for node in nodes {
            match node.kind() {
                SyntaxKind::Identifier => {
                    let Some(name) = lib_global_name(ctx, node) else {
                        continue;
                    };
                    if escapes_as_value(node) {
                        escaping.insert(name.to_owned());
                    }
                    // Destructuring or aliasing reflection helpers.
                    if matches!(name, "Object" | "Reflect") {
                        let outer = walk_up_outer(node);
                        let parent = outer.parent();
                        if parent.kind() == SyntaxKind::VariableDeclaration
                            && parent.initializer() == outer
                            && parent.name().kind() != SyntaxKind::Identifier
                        {
                            patches.add(Owner::Any, Member::Any);
                        }
                        if let Some(writer) = reflection_writer_name(ctx, parent, outer) {
                            let call = walk_up_outer(parent).parent();
                            let direct = call.kind() == SyntaxKind::CallExpression
                                && skip_outer(call.expression()) == parent;
                            let aliased = if direct {
                                None
                            } else {
                                reflection_alias_calls(ctx, parent)
                            };
                            if let Some(calls) = aliased {
                                // An unwritten local alias that is only
                                // called is analysed like the direct calls.
                                for call in calls {
                                    // A spread hides the real target, and
                                    // `Reflect.set` with a receiver writes
                                    // the receiver, not the target.
                                    if call
                                        .arguments()
                                        .iter()
                                        .any(|a| a.kind() == SyntaxKind::SpreadElement)
                                        || (writer == "set" && call.arguments().len() >= 4)
                                    {
                                        patches.add(Owner::Any, Member::Any);
                                        continue;
                                    }
                                    if let Some(target) = call.arguments().first() {
                                        let key = matches!(
                                            writer.as_str(),
                                            "defineProperty" | "set" | "deleteProperty"
                                        )
                                        .then(|| call.arguments().to_vec().get(1).copied())
                                        .flatten();
                                        let member = key
                                            .map(|k| literal_member(ctx, k))
                                            .unwrap_or(Member::Any);
                                        writes.push((node, target, member, key, true));
                                    }
                                }
                            } else if !direct {
                                patches.add(Owner::Any, Member::Any);
                            } else if writer == "set" && call.arguments().len() >= 4 {
                                // The fourth argument receives the write.
                                patches.add(Owner::Any, Member::Any);
                            } else if let Some(target) = call.arguments().first() {
                                let key = matches!(
                                    writer.as_str(),
                                    "defineProperty" | "set" | "deleteProperty"
                                )
                                .then(|| call.arguments().to_vec().get(1).copied())
                                .flatten();
                                let member =
                                    key.map(|k| literal_member(ctx, k)).unwrap_or(Member::Any);
                                writes.push((node, target, member, key, false));
                            }
                        }
                    }
                    // Plain assignment to a global binding.
                    if is_write_access(node) && walk_up_outer(node) == node {
                        patches.add(Owner::Global, Member::Named(name.to_owned()));
                    }
                }
                SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
                    if is_write_access(node) =>
                {
                    let (member, key) = if node.kind() == SyntaxKind::PropertyAccessExpression {
                        (Member::Named(node.name().text().to_owned()), None)
                    } else {
                        let key = node.argument_expression();
                        (literal_member(ctx, key), Some(key))
                    };
                    writes.push((node, node.expression(), member, key, false));
                }
                SyntaxKind::DeleteExpression => {
                    let target = skip_outer(node.expression());
                    if matches!(
                        target.kind(),
                        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
                    ) {
                        let (member, key) = if target.kind() == SyntaxKind::PropertyAccessExpression
                        {
                            (Member::Named(target.name().text().to_owned()), None)
                        } else {
                            let key = target.argument_expression();
                            (literal_member(ctx, key), Some(key))
                        };
                        writes.push((node, target.expression(), member, key, false));
                    }
                }
                SyntaxKind::ModuleDeclaration
                    if node.name().kind() == SyntaxKind::Identifier
                        && node.name().text() == "global" =>
                {
                    // `declare global` augmentation of a built-in suggests a
                    // runtime patch.
                    let mut inner = Vec::new();
                    super::super::collect(node, &mut inner);
                    for declaration in inner {
                        if matches!(
                            declaration.kind(),
                            SyntaxKind::InterfaceDeclaration | SyntaxKind::VariableDeclaration
                        ) && declaration.name().kind() == SyntaxKind::Identifier
                        {
                            let name = declaration.name().text();
                            if let Some(base) = AUGMENTED.iter().find(|(n, _)| *n == name) {
                                patches.add(Owner::Named(base.1.to_owned()), Member::Any);
                                patches.add(Owner::Global, Member::Named(base.1.to_owned()));
                            }
                        }
                    }
                }
                _ => {}
            }
        }
    }
    // Whether `new C()` of a library constructor is fresh depends on whether
    // anything replaces `C`, and the writes that replace it are what this scan
    // finds. Start from "nothing is replaced" and grow the set until it is
    // stable: until the first constructor is replaced at run time every
    // judgement of the first pass holds, so the write that replaces it is
    // recorded, and a larger set only makes later passes more conservative.
    let mut replaced = local_receiver::Replaced::new();
    loop {
        let scanned = classify_writes(ctx, &writes, &escaping, patches.clone(), &replaced);
        let next: local_receiver::Replaced = local_receiver::fresh_library_classes()
            .filter(|name| {
                scanned.record(&Owner::Global, name).is_some()
                    || scanned.unproven_record(name).is_some()
            })
            .map(str::to_owned)
            .collect();
        if next.is_subset(&replaced) {
            tracing::debug!(replaced = replaced.len(), "replaced library constructors");
            return scanned;
        }
        replaced.extend(next);
    }
}

/// Adds the record of every deferred write to `patches`.
fn classify_writes(
    ctx: &mut Ctx,
    writes: &[(Node, Node, Member, Option<Node>, bool)],
    escaping: &HashSet<String>,
    mut patches: Patches,
    replaced: &local_receiver::Replaced,
) -> Patches {
    let mut symbol_keyed = false;
    for (_, target, member, key, strict) in writes {
        let member = member.clone();
        match write_owner(ctx, *target, replaced) {
            WriteOwner::App => {}
            WriteOwner::Owner(Owner::Global) if member == Member::Any => {
                // The key is computed. It cannot replace a named global when
                // it is a symbol, and it can only replace a global whose name
                // starts with a known literal prefix.
                match key.map_or(KeyShape::Unknown, |key| key_shape(ctx, key, 0)) {
                    KeyShape::Symbol => symbol_keyed = true,
                    KeyShape::Prefix(prefix) => {
                        patches.global_prefixes.insert(prefix);
                    }
                    KeyShape::Unknown => patches.add(Owner::Global, member),
                }
            }
            WriteOwner::Owner(owner) => patches.add(owner, member),
            WriteOwner::Unknown => {
                for name in escaping {
                    patches.add(Owner::Named(name.clone()), member.clone());
                }
            }
            WriteOwner::Opaque => patches.add(Owner::Any, member),
            // An alias call is judged like the old conservative fallback: an
            // unproven target may be any built-in.
            WriteOwner::Unproven if *strict => patches.add(Owner::Any, member),
            WriteOwner::Unproven => {
                patches.unproven.insert(member);
            }
        }
    }
    // `Symbol(...)` and `Symbol.for(...)` return symbols only while nothing
    // replaces them.
    if symbol_keyed
        && (patches.record(&Owner::Global, "Symbol").is_some()
            || patches
                .record(&Owner::Named("Symbol".to_owned()), "for")
                .is_some()
            || patches.unproven_record("Symbol").is_some()
            || patches.unproven_record("for").is_some())
    {
        patches.add(Owner::Global, Member::Any);
    }
    patches
}

/// How an application module reaches the module a specifier names.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum ImportTarget {
    /// An application module; the scan inspects its statements itself.
    Application,
    /// A host module or a JSON file that cannot patch built-ins.
    Inert,
    /// An external package, a declaration-only package, an ambient module
    /// declaration or an unresolved specifier. Its runtime code is unknown.
    Unknown,
}

/// Bare names in Node 24's `module.builtinModules` inventory. Keep this list
/// explicit so a prefix-only `node:` module never becomes a bare host import.
/// The inventory test compares every entry with the active Node 24 runtime.
const BARE_NODE_CORE_MODULES: &[&str] = &[
    "_http_agent",
    "_http_client",
    "_http_common",
    "_http_incoming",
    "_http_outgoing",
    "_http_server",
    "_stream_duplex",
    "_stream_passthrough",
    "_stream_readable",
    "_stream_transform",
    "_stream_wrap",
    "_stream_writable",
    "_tls_common",
    "_tls_wrap",
    "assert",
    "assert/strict",
    "async_hooks",
    "buffer",
    "child_process",
    "cluster",
    "console",
    "constants",
    "crypto",
    "dgram",
    "diagnostics_channel",
    "dns",
    "dns/promises",
    "domain",
    "events",
    "fs",
    "fs/promises",
    "http",
    "http2",
    "https",
    "inspector",
    "inspector/promises",
    "module",
    "net",
    "os",
    "path",
    "path/posix",
    "path/win32",
    "perf_hooks",
    "process",
    "punycode",
    "querystring",
    "readline",
    "readline/promises",
    "repl",
    "stream",
    "stream/consumers",
    "stream/promises",
    "stream/web",
    "string_decoder",
    "sys",
    "timers",
    "timers/promises",
    "tls",
    "trace_events",
    "tty",
    "url",
    "util",
    "util/types",
    "v8",
    "vm",
    "wasi",
    "worker_threads",
    "zlib",
];

fn is_bare_node_core_module(name: &str) -> bool {
    BARE_NODE_CORE_MODULES.binary_search(&name).is_ok()
}

/// JavaScript `require` literals have no checker symbol. An ambient module
/// declaration supplies their host identity, unless a loaded userland package
/// with the same root could be selected by an alternate resolver.
fn ambient_bare_node_core_module(ctx: &Ctx, name: &str) -> bool {
    if !is_bare_node_core_module(name) {
        return false;
    }
    let root = name.split('/').next().unwrap_or(name);
    let package_segment = format!("/node_modules/{root}/");
    if ctx
        .externals
        .iter()
        .any(|&file| source_file_info(file).file_name.contains(&package_segment))
    {
        return false;
    }
    ctx.externals.iter().any(|&file| {
        file.statements().iter().any(|declaration| {
            declaration.kind() == SyntaxKind::ModuleDeclaration && declaration.name().text() == name
        })
    })
}

/// Classifies a literal module specifier without loading the module.
fn import_target(ctx: &mut Ctx, importer: &str, specifier: Node) -> ImportTarget {
    let text = specifier.text();
    // The explicit scheme identifies a host module. A bare builtin is inert
    // only if resolution has not identified a concrete userland module.
    if text.starts_with("node:") {
        return ImportTarget::Inert;
    }
    let symbol = ctx.checker.get_symbol_at_location(specifier);
    if symbol.is_nil() {
        // A side-effect import of a script (a file without imports or
        // exports) has no module symbol; match relative specifiers against
        // the application files by path.
        return if relative_application_file(ctx, importer, text) {
            ImportTarget::Application
        } else if ambient_bare_node_core_module(ctx, text) {
            ImportTarget::Inert
        } else {
            ImportTarget::Unknown
        };
    }
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    // An ambient declaration cannot redirect a Node 24 bare builtin at
    // runtime. Other ambient names still designate unknown runtime code.
    let Some(file) = declarations
        .first()
        .copied()
        .filter(|d| d.kind() == SyntaxKind::SourceFile)
    else {
        return if !declarations.is_empty() && ambient_bare_node_core_module(ctx, text) {
            ImportTarget::Inert
        } else {
            ImportTarget::Unknown
        };
    };
    let name = source_file_info(file).file_name.clone();
    if name.ends_with(".json") {
        ImportTarget::Inert
    } else if ctx.file_of(&name).is_some() {
        ImportTarget::Application
    } else {
        ImportTarget::Unknown
    }
}

/// Whether a relative specifier names an application file, matching the
/// extension substitutions and index files TypeScript tries.
fn relative_application_file(ctx: &Ctx, importer: &str, specifier: &str) -> bool {
    if !specifier.starts_with("./") && !specifier.starts_with("../") {
        return false;
    }
    let mut segments: Vec<&str> = importer.split('/').collect();
    segments.pop();
    for segment in specifier.split('/') {
        match segment {
            "" | "." => {}
            ".." => {
                segments.pop();
            }
            other => segments.push(other),
        }
    }
    let base = segments.join("/");
    let stem = base
        .rsplit_once('.')
        .filter(|(_, ext)| matches!(*ext, "js" | "jsx" | "mjs" | "cjs"))
        .map(|(stem, _)| stem);
    let mut candidates = vec![base.clone()];
    for extension in ["ts", "tsx", "mts", "cts", "js", "jsx", "mjs", "cjs"] {
        candidates.push(format!("{base}.{extension}"));
        candidates.push(format!("{base}/index.{extension}"));
        if let Some(stem) = stem {
            candidates.push(format!("{stem}.{extension}"));
        }
    }
    candidates
        .iter()
        .any(|candidate| ctx.file_of(candidate).is_some())
}

/// Local names of loaders returned by `createRequire` from the host module.
/// This intentionally recognizes a bounded set of direct aliases. Unknown
/// loader calls are not used as evidence that an intrinsic is intact.
fn is_host_module_expression(expression: Node, host_modules: &HashSet<String>) -> bool {
    let expression = skip_outer(expression);
    if expression.kind() == SyntaxKind::AwaitExpression {
        return is_host_module_expression(expression.expression(), host_modules);
    }
    if expression.kind() == SyntaxKind::Identifier {
        return host_modules.contains(expression.text());
    }
    if expression.kind() != SyntaxKind::CallExpression {
        return false;
    }
    let Some(argument) = expression.arguments().first() else {
        return false;
    };
    if !matches!(
        argument.kind(),
        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
    ) || !matches!(argument.text(), "node:module" | "module")
    {
        return false;
    }
    let callee = skip_outer(expression.expression());
    match callee.kind() {
        SyntaxKind::ImportKeyword => true,
        SyntaxKind::Identifier => callee.text() == "require",
        _ => false,
    }
}

/// Whether a module namespace accesses the `createRequire` factory.
fn is_create_require_access(callee: Node) -> bool {
    match callee.kind() {
        SyntaxKind::PropertyAccessExpression => callee.name().text() == "createRequire",
        SyntaxKind::ElementAccessExpression => {
            let key = skip_outer(callee.argument_expression());
            matches!(
                key.kind(),
                SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
            ) && key.text() == "createRequire"
        }
        _ => false,
    }
}

fn created_require_loaders(nodes: &[Node]) -> HashSet<String> {
    let mut factories = HashSet::new();
    let mut host_modules = HashSet::new();
    for &node in nodes {
        match node.kind() {
            SyntaxKind::ImportDeclaration
                if matches!(node.module_specifier().text(), "node:module" | "module") =>
            {
                let clause = node.import_clause();
                if clause.is_nil() || clause.is_type_only() {
                    continue;
                }
                if clause.name().is_some() {
                    host_modules.insert(clause.name().text().to_owned());
                }
                let bindings = clause.named_bindings();
                match bindings.kind() {
                    SyntaxKind::NamespaceImport => {
                        host_modules.insert(bindings.name().text().to_owned());
                    }
                    SyntaxKind::NamedImports => {
                        for specifier in bindings.elements().iter() {
                            let imported = if specifier.property_name().is_some() {
                                specifier.property_name()
                            } else {
                                specifier.name()
                            };
                            if imported.text() == "createRequire" && !specifier.is_type_only() {
                                factories.insert(specifier.name().text().to_owned());
                            }
                        }
                    }
                    _ => {}
                }
            }
            SyntaxKind::VariableDeclaration if node.name().kind() == SyntaxKind::Identifier => {
                let init = skip_outer(node.initializer());
                if is_host_module_expression(init, &host_modules) {
                    host_modules.insert(node.name().text().to_owned());
                }
            }
            SyntaxKind::VariableDeclaration
                if node.name().kind() == SyntaxKind::ObjectBindingPattern =>
            {
                let init = skip_outer(node.initializer());
                if !is_host_module_expression(init, &host_modules) {
                    continue;
                }
                for binding in node.name().elements().iter() {
                    let imported = if binding.property_name().is_some() {
                        binding.property_name()
                    } else {
                        binding.name()
                    };
                    if imported.text() == "createRequire"
                        && binding.name().kind() == SyntaxKind::Identifier
                    {
                        factories.insert(binding.name().text().to_owned());
                    }
                }
            }
            _ => {}
        }
    }
    let mut loaders = HashSet::new();
    // A direct default export has no VariableDeclaration to seed below.
    for &node in nodes {
        if node.kind() != SyntaxKind::ExportAssignment {
            continue;
        }
        let init = skip_outer(node.expression());
        if init.kind() != SyntaxKind::CallExpression {
            continue;
        }
        let callee = skip_outer(init.expression());
        let from_factory = (callee.kind() == SyntaxKind::Identifier
            && factories.contains(callee.text()))
            || (is_create_require_access(callee)
                && is_host_module_expression(callee.expression(), &host_modules));
        if from_factory {
            loaders.insert("default".to_owned());
        }
    }
    // A direct initializer and a local alias both retain the loader identity.
    for _ in 0..nodes.len() {
        let mut changed = false;
        for &node in nodes {
            if node.kind() != SyntaxKind::VariableDeclaration
                || node.name().kind() != SyntaxKind::Identifier
            {
                continue;
            }
            let init = skip_outer(node.initializer());
            let from_factory = if init.kind() != SyntaxKind::CallExpression {
                false
            } else {
                let callee = skip_outer(init.expression());
                (callee.kind() == SyntaxKind::Identifier && factories.contains(callee.text()))
                    || (is_create_require_access(callee)
                        && is_host_module_expression(callee.expression(), &host_modules))
            };
            let from_loader =
                init.kind() == SyntaxKind::Identifier && loaders.contains(init.text());
            if (from_factory || from_loader) && loaders.insert(node.name().text().to_owned()) {
                changed = true;
            }
        }
        if !changed {
            break;
        }
    }
    loaders
}

fn module_require_call(ctx: &Ctx, callee: Node, loaders: &HashSet<String>) -> bool {
    let callee = skip_outer(callee);
    if callee.kind() == SyntaxKind::Identifier {
        return loaders.contains(callee.text());
    }
    let is_require_member = match callee.kind() {
        SyntaxKind::PropertyAccessExpression => callee.name().text() == "require",
        SyntaxKind::ElementAccessExpression => {
            literal_member(ctx, callee.argument_expression()) == Member::Named("require".to_owned())
        }
        _ => false,
    };
    is_require_member
        && skip_outer(callee.expression()).kind() == SyntaxKind::Identifier
        && skip_outer(callee.expression()).text() == "module"
}

fn element_access_symbol(ctx: &mut Ctx, access: Node) -> SymbolId {
    let key = skip_outer(access.argument_expression());
    let Some(name) = string_value(ctx, key) else {
        return SymbolId::NIL;
    };
    let ty = ctx.checker.get_type_at_location(access.expression());
    ctx.checker
        .get_properties_of_type(ty)
        .into_iter()
        .find(|&symbol| ctx.checker.sym(symbol).name == name)
        .map_or(SymbolId::NIL, |symbol| ctx.skip_alias(symbol))
}

/// Whether any application module has a runtime import, re-export, dynamic
/// import or `require` of code outside the application. Type-only imports
/// and re-exports are erased and exempt. The scan reads syntax and resolves
/// specifiers through the checker; it never executes or reads dependencies.
fn imports_external_runtime(ctx: &mut Ctx) -> bool {
    let local_loaders: Vec<HashSet<String>> = ctx
        .files
        .iter()
        .map(|file| created_require_loaders(&file.nodes))
        .collect();
    let mut loader_symbols = HashSet::new();
    for index in 0..ctx.files.len() {
        let nodes = ctx.files[index].nodes.clone();
        if local_loaders[index].contains("default") {
            let module = ctx.files[index].node.symbol();
            if !module.is_nil() {
                let module = ctx.checker.get_merged_symbol_exported(module);
                let default_export = ctx
                    .checker
                    .symbols
                    .get(ctx.checker.sym(module).exports, "default");
                if !default_export.is_nil() {
                    loader_symbols.insert(default_export);
                    let target = ctx.skip_alias(default_export);
                    if !target.is_nil() {
                        loader_symbols.insert(target);
                    }
                }
            }
        }
        for node in nodes {
            if node.kind() != SyntaxKind::VariableDeclaration
                || node.name().kind() != SyntaxKind::Identifier
                || !local_loaders[index].contains(node.name().text())
            {
                continue;
            }
            let symbol = ctx.resolve(node.name());
            if !symbol.is_nil() {
                loader_symbols.insert(symbol);
            }
        }
    }
    // Preserve loader identity through application aliases. `resolve` skips
    // import/export aliases, while this loop also covers local forwarding
    // bindings such as `const exported = importedLoader`.
    loop {
        let mut changed = false;
        for index in 0..ctx.files.len() {
            let nodes = ctx.files[index].nodes.clone();
            for node in nodes {
                if node.kind() != SyntaxKind::VariableDeclaration
                    || node.name().kind() != SyntaxKind::Identifier
                {
                    continue;
                }
                let init = skip_outer(node.initializer());
                let from_loader = init.kind() == SyntaxKind::Identifier
                    && loader_symbols.contains(&ctx.resolve(init));
                if !local_loaders[index].contains(node.name().text()) && !from_loader {
                    continue;
                }
                let symbol = ctx.resolve(node.name());
                if !symbol.is_nil() && loader_symbols.insert(symbol) {
                    changed = true;
                }
            }
        }
        if !changed {
            break;
        }
    }

    for index in 0..ctx.files.len() {
        let loaders = &local_loaders[index];
        for position in 0..ctx.files[index].nodes.len() {
            let node = ctx.files[index].nodes[position];
            let specifier = match node.kind() {
                SyntaxKind::ImportDeclaration => {
                    let clause = node.import_clause();
                    if clause.is_some() && clause.is_type_only() {
                        continue;
                    }
                    node.module_specifier()
                }
                SyntaxKind::ExportDeclaration => {
                    if node.is_type_only() {
                        continue;
                    }
                    node.module_specifier()
                }
                SyntaxKind::CallExpression => {
                    let callee = skip_outer(node.expression());
                    let dynamic = callee.kind() == SyntaxKind::ImportKeyword;
                    let require = callee.kind() == SyntaxKind::Identifier
                        && callee.text() == "require"
                        && ctx.files[index].js;
                    let created_loader = match callee.kind() {
                        SyntaxKind::Identifier => {
                            loaders.contains(callee.text())
                                || loader_symbols.contains(&ctx.resolve(callee))
                        }
                        SyntaxKind::PropertyAccessExpression => {
                            loader_symbols.contains(&ctx.resolve(callee.name()))
                        }
                        SyntaxKind::ElementAccessExpression => {
                            let key = skip_outer(callee.argument_expression());
                            matches!(
                                key.kind(),
                                SyntaxKind::StringLiteral
                                    | SyntaxKind::NoSubstitutionTemplateLiteral
                            ) && loader_symbols.contains(&element_access_symbol(ctx, callee))
                        }
                        _ => false,
                    };
                    if !dynamic
                        && !require
                        && !created_loader
                        && !(ctx.files[index].js && module_require_call(ctx, callee, loaders))
                    {
                        continue;
                    }
                    match node.arguments().first() {
                        Some(argument)
                            if matches!(
                                argument.kind(),
                                SyntaxKind::StringLiteral
                                    | SyntaxKind::NoSubstitutionTemplateLiteral
                            ) =>
                        {
                            if created_loader
                                && (argument.text().starts_with("./")
                                    || argument.text().starts_with("../"))
                            {
                                tracing::debug!(
                                    file = %ctx.files[index].name,
                                    specifier = argument.text(),
                                    "createdRequire loader may resolve relative code outside the importer"
                                );
                                return true;
                            }
                            argument
                        }
                        // A computed specifier may load any package.
                        _ => {
                            tracing::debug!(
                                file = %ctx.files[index].name,
                                "computed module load may run unknown runtime code"
                            );
                            return true;
                        }
                    }
                }
                _ => continue,
            };
            if specifier.is_nil() {
                continue;
            }
            let importer = ctx.files[index].name.clone();
            if import_target(ctx, &importer, specifier) == ImportTarget::Unknown {
                tracing::debug!(
                    file = %ctx.files[index].name,
                    specifier = specifier.text(),
                    "external runtime import may replace built-ins"
                );
                return true;
            }
        }
    }
    false
}

const REFLECTION_WRITERS: [&str; 7] = [
    "defineProperty",
    "defineProperties",
    "setPrototypeOf",
    "assign",
    "set",
    "deleteProperty",
    "__defineGetter__",
];

/// Interface names whose augmentation targets a built-in.
const AUGMENTED: [(&str, &str); 14] = [
    ("Array", "Array"),
    ("ArrayConstructor", "Array"),
    ("ReadonlyArray", "Array"),
    ("String", "String"),
    ("StringConstructor", "String"),
    ("Number", "Number"),
    ("NumberConstructor", "Number"),
    ("Boolean", "Boolean"),
    ("BooleanConstructor", "Boolean"),
    ("Object", "Object"),
    ("ObjectConstructor", "Object"),
    ("Symbol", "Symbol"),
    ("IterableIterator", "Array"),
    ("ArrayIterator", "Array"),
];

enum WriteOwner {
    /// An application object; built-ins are unaffected.
    App,
    /// A receiver that is not provably a local object (a parameter, a
    /// property read, a call result): a possible patch of any intrinsic.
    Unproven,
    Owner(Owner),
    /// May be any built-in that escapes as a value.
    Unknown,
    /// Reached through prototype reflection; may be anything.
    Opaque,
}

/// The literal member a property or element access names, if any
/// (`x.name`, `x["name"]`).
fn literal_access_name(ctx: &Ctx, node: Node) -> Option<String> {
    match node.kind() {
        SyntaxKind::PropertyAccessExpression => Some(node.name().text().to_owned()),
        SyntaxKind::ElementAccessExpression => {
            match literal_member(ctx, node.argument_expression()) {
                Member::Named(name) => Some(name),
                Member::Any => None,
            }
        }
        _ => None,
    }
}

fn write_owner(ctx: &mut Ctx, target: Node, replaced: &local_receiver::Replaced) -> WriteOwner {
    let target = skip_outer(target);
    // Any prototype reflection in the receiver chain may reach a built-in.
    let mut node = target;
    loop {
        match node.kind() {
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
                if literal_access_name(ctx, node)
                    .is_some_and(|name| matches!(name.as_str(), "__proto__" | "constructor"))
                {
                    return WriteOwner::Opaque;
                }
                node = skip_outer(node.expression());
            }
            SyntaxKind::CallExpression => {
                let callee = skip_outer(node.expression());
                if callee.kind() == SyntaxKind::PropertyAccessExpression
                    && callee.name().text() == "getPrototypeOf"
                {
                    return WriteOwner::Opaque;
                }
                break;
            }
            _ => break,
        }
    }
    let base = |ctx: &mut Ctx, node: Node| -> Option<String> {
        let node = skip_outer(node);
        if node.kind() == SyntaxKind::Identifier {
            return lib_global_name(ctx, node).map(str::to_owned);
        }
        // `globalThis.Array` and similar.
        if node.kind() == SyntaxKind::PropertyAccessExpression
            && lib_global_name(ctx, skip_outer(node.expression()))
                .is_some_and(|n| GLOBAL_OBJECTS.contains(&n))
        {
            return Some(node.name().text().to_owned());
        }
        None
    };
    // `X.prototype` and `X["prototype"]`
    if literal_access_name(ctx, target).as_deref() == Some("prototype") {
        return match base(ctx, target.expression()) {
            Some(name) if GLOBAL_OBJECTS.contains(&name.as_str()) => WriteOwner::Opaque,
            Some(name) => WriteOwner::Owner(Owner::Named(name)),
            None => {
                if skip_outer(target.expression()).kind() == SyntaxKind::Identifier {
                    WriteOwner::Unknown
                } else {
                    WriteOwner::Opaque
                }
            }
        };
    }
    match base(ctx, target) {
        Some(name) if GLOBAL_OBJECTS.contains(&name.as_str()) => WriteOwner::Owner(Owner::Global),
        Some(name) => WriteOwner::Owner(Owner::Named(name)),
        None => {
            if chain_mentions_prototype(ctx, target) {
                WriteOwner::Unknown
            } else if local_receiver::provably_local(ctx, target, replaced) {
                WriteOwner::App
            } else {
                // A parameter, a property read, a call result or a reassigned
                // variable may hold an intrinsic that a caller supplied.
                WriteOwner::Unproven
            }
        }
    }
}

fn chain_mentions_prototype(ctx: &Ctx, mut node: Node) -> bool {
    loop {
        node = skip_outer(node);
        match node.kind() {
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
                if literal_access_name(ctx, node).as_deref() == Some("prototype") {
                    return true;
                }
                node = node.expression();
            }
            _ => return false,
        }
    }
}

fn literal_member(ctx: &Ctx, key: Node) -> Member {
    let key = skip_outer(key);
    match key.kind() {
        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral => {
            string_value(ctx, key).map_or(Member::Any, Member::Named)
        }
        SyntaxKind::NumericLiteral => Member::Named(key.text().to_owned()),
        _ => Member::Any,
    }
}

/// What a computed property key is known to be.
enum KeyShape {
    /// Always a symbol, so it never names a string-keyed global.
    Symbol,
    /// A string that starts with this non-empty literal text.
    Prefix(String),
    Unknown,
}

/// Classifies a computed key: `Symbol(...)`, `Symbol.for(...)`, a template
/// literal or `"literal" + x` concatenation with a literal start, or a `const`
/// bound to one of those.
fn key_shape(ctx: &mut Ctx, key: Node, depth: usize) -> KeyShape {
    let key = skip_outer(key);
    if depth > 3 {
        return KeyShape::Unknown;
    }
    match key.kind() {
        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral => {
            match string_value(ctx, key) {
                Some(text) if !text.is_empty() => KeyShape::Prefix(text),
                _ => KeyShape::Unknown,
            }
        }
        SyntaxKind::TemplateExpression => {
            let head = key.head().text();
            if head.is_empty() || head.contains('\\') {
                KeyShape::Unknown
            } else {
                KeyShape::Prefix(head.to_owned())
            }
        }
        SyntaxKind::BinaryExpression if key.operator_token().kind() == SyntaxKind::PlusToken => {
            match key_shape(ctx, key.left(), depth + 1) {
                // A string on the left makes the sum a string with that start.
                KeyShape::Prefix(prefix)
                    if skip_outer(key.left()).kind() != SyntaxKind::Identifier =>
                {
                    KeyShape::Prefix(prefix)
                }
                _ => KeyShape::Unknown,
            }
        }
        SyntaxKind::CallExpression => {
            let callee = skip_outer(key.expression());
            let symbol_global = |ctx: &mut Ctx, node: Node| {
                node.kind() == SyntaxKind::Identifier
                    && node.text() == "Symbol"
                    && lib_global_name(ctx, node) == Some("Symbol")
            };
            let is_symbol = symbol_global(ctx, callee)
                || (callee.kind() == SyntaxKind::PropertyAccessExpression
                    && callee.name().text() == "for"
                    && symbol_global(ctx, skip_outer(callee.expression())));
            if is_symbol {
                KeyShape::Symbol
            } else {
                KeyShape::Unknown
            }
        }
        SyntaxKind::Identifier => {
            let symbol = ctx.resolve(key);
            if symbol.is_nil() {
                return KeyShape::Unknown;
            }
            let declaration = ctx.checker.sym(symbol).value_declaration;
            // A `let` or `var` that nothing writes holds its initializer
            // from then on, like a `const`; before that it is `undefined`,
            // which names only a read-only global.
            if declaration.is_nil()
                || declaration.kind() != SyntaxKind::VariableDeclaration
                || declaration.parent().flags().intersects(NodeFlags::Using)
                || declaration.initializer().is_nil()
                || ctx.checker.sym(symbol).declarations.len() != 1
                || ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write)
            {
                return KeyShape::Unknown;
            }
            let initializer = declaration.initializer();
            key_shape(ctx, initializer, depth + 1)
        }
        _ => KeyShape::Unknown,
    }
}

/// Name of the built-in global an identifier reads, if it is one.
fn lib_global_name(ctx: &mut Ctx, node: Node) -> Option<&'static str> {
    if node.kind() != SyntaxKind::Identifier {
        return None;
    }
    let parent = node.parent();
    if matches!(
        parent.kind(),
        SyntaxKind::PropertyAccessExpression | SyntaxKind::QualifiedName
    ) && (parent.name() == node)
    {
        return None;
    }
    let symbol = ctx.resolve(node);
    if symbol.is_nil() {
        return None;
    }
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    // The checker's `globalThis` symbol has no declaration.
    (lib_only(&declarations) || (declarations.is_empty() && node.text() == "globalThis"))
        .then(|| node.text())
}

/// For a read of a built-in global, whether it escapes as a value (anything
/// other than a call, construction, member read, comparison or `typeof`).
fn escapes_as_value(node: Node) -> bool {
    if is_part_of_type_node(node) {
        return false;
    }
    let outer = walk_up_outer(node);
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::CallExpression | SyntaxKind::NewExpression => parent.expression() != outer,
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
            parent.expression() != outer
        }
        SyntaxKind::TypeOfExpression => false,
        SyntaxKind::BinaryExpression => !matches!(
            parent.operator_token().kind(),
            SyntaxKind::EqualsEqualsEqualsToken
                | SyntaxKind::ExclamationEqualsEqualsToken
                | SyntaxKind::EqualsEqualsToken
                | SyntaxKind::ExclamationEqualsToken
                | SyntaxKind::InstanceOfKeyword
        ),
        SyntaxKind::ExpressionWithTypeArguments | SyntaxKind::HeritageClause => false,
        _ => !is_declaration_name(node),
    }
}

/// Checker-type kind classification for type neutrality decisions.
pub(super) fn type_constituents(ctx: &mut Ctx, t: TypeId) -> Vec<TypeId> {
    if ctx.checker.ty(t).flags().intersects(TypeFlags::Union) {
        ctx.checker.ty(t).types_list().to_vec()
    } else {
        vec![t]
    }
}

/// The checker type at `node` excludes `null`, `undefined` and other
/// possibly nullish types, so a non-optional access type-checks.
pub(super) fn type_non_nullable(ctx: &mut Ctx, node: Node) -> bool {
    let t = ctx.checker.get_type_at_location(node);
    if t.is_nil() {
        return false;
    }
    type_constituents(ctx, t).into_iter().all(|c| {
        !ctx.checker.ty(c).flags().intersects(
            TypeFlags::Null
                | TypeFlags::Undefined
                | TypeFlags::Void
                | TypeFlags::Unknown
                | TypeFlags::Never
                | TypeFlags::Instantiable,
        )
    })
}

/// A non-null assertion on `node` yields a type whose members can be
/// accessed: no `unknown` or generic constituents and at least one
/// non-nullish constituent.
pub(super) fn type_assertable(ctx: &mut Ctx, node: Node) -> bool {
    let t = ctx.checker.get_type_at_location(node);
    if t.is_nil() {
        return false;
    }
    let constituents = type_constituents(ctx, t);
    let mut present = false;
    for c in constituents {
        let flags = ctx.checker.ty(c).flags();
        if flags.intersects(TypeFlags::Unknown | TypeFlags::Instantiable) {
            return false;
        }
        if !flags
            .intersects(TypeFlags::Null | TypeFlags::Undefined | TypeFlags::Void | TypeFlags::Never)
        {
            present = true;
        }
    }
    present
}

/// Every constituent of the checker type at `node` has one of `flags`.
pub(super) fn type_all(ctx: &mut Ctx, node: Node, flags: TypeFlags) -> bool {
    let t = ctx.checker.get_type_at_location(node);
    if t.is_nil() {
        return false;
    }
    let constituents = type_constituents(ctx, t);
    !constituents.is_empty()
        && constituents
            .into_iter()
            .all(|c| ctx.checker.ty(c).flags().intersects(flags))
}

/// Whether the checker types of two expressions are identical.
pub(super) fn same_type(ctx: &mut Ctx, a: Node, b: Node) -> bool {
    let ta = ctx.checker.get_type_at_location(a);
    let tb = ctx.checker.get_type_at_location(b);
    if ta.is_nil() {
        return false;
    }
    if ta == tb {
        return true;
    }
    // A fresh array literal type is the array type once widened.
    skip_outer(b).kind() == SyntaxKind::ArrayLiteralExpression
        && tb.is_some()
        && ctx.checker.ty(tb).flags().intersects(TypeFlags::Object)
        && ctx
            .checker
            .ty(tb)
            .object_flags()
            .intersects(ObjectFlags::ArrayLiteral)
        && ctx.checker.get_widened_type(tb) == ta
}

/// Byte span of a node without leading trivia.
pub(super) fn span(node: Node) -> (usize, usize) {
    (Ctx::start(node), node.end() as usize)
}

/// Whether the value of expression `node` only matters through `ToBoolean`
/// (conditions, `!` operands and logical operands in such positions).
pub(super) fn in_test_context(node: Node) -> bool {
    let outer = walk_up_outer(node);
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::IfStatement | SyntaxKind::WhileStatement | SyntaxKind::DoStatement => {
            parent.expression() == outer
        }
        SyntaxKind::ForStatement => parent.condition() == outer,
        SyntaxKind::ConditionalExpression => parent.condition() == outer,
        SyntaxKind::PrefixUnaryExpression => parent.operator() == SyntaxKind::ExclamationToken,
        SyntaxKind::BinaryExpression => match parent.operator_token().kind() {
            SyntaxKind::AmpersandAmpersandToken | SyntaxKind::BarBarToken => {
                in_test_context(parent)
            }
            _ => false,
        },
        _ => false,
    }
}

/// The outermost test-position construct around `node` and whether its
/// narrowing may extend past it (statements and loops).
pub(super) fn test_scope(node: Node) -> (Node, bool) {
    let mut current = node;
    loop {
        let outer = walk_up_outer(current);
        let parent = outer.parent();
        match parent.kind() {
            SyntaxKind::PrefixUnaryExpression
                if parent.operator() == SyntaxKind::ExclamationToken =>
            {
                current = parent;
            }
            SyntaxKind::BinaryExpression
                if matches!(
                    parent.operator_token().kind(),
                    SyntaxKind::AmpersandAmpersandToken
                        | SyntaxKind::BarBarToken
                        | SyntaxKind::QuestionQuestionToken
                ) =>
            {
                current = parent;
            }
            SyntaxKind::ConditionalExpression if parent.condition() == outer => {
                return (parent, false);
            }
            SyntaxKind::IfStatement | SyntaxKind::WhileStatement | SyntaxKind::DoStatement
                if parent.expression() == outer =>
            {
                return (parent, true);
            }
            SyntaxKind::ForStatement if parent.condition() == outer => return (parent, true),
            _ => return (outer, false),
        }
    }
}

/// TypeScript reports conditions that are syntactically always truthy or
/// always falsy (and some literals); such expressions must not be moved into
/// a test position.
pub(super) fn syntactically_uncertain(node: Node) -> bool {
    let node = skip_outer(node);
    match node.kind() {
        SyntaxKind::NumericLiteral => matches!(node.text(), "0" | "1"),
        SyntaxKind::ArrayLiteralExpression
        | SyntaxKind::ArrowFunction
        | SyntaxKind::BigIntLiteral
        | SyntaxKind::ClassExpression
        | SyntaxKind::FunctionExpression
        | SyntaxKind::JsxElement
        | SyntaxKind::JsxSelfClosingElement
        | SyntaxKind::JsxFragment
        | SyntaxKind::ObjectLiteralExpression
        | SyntaxKind::RegularExpressionLiteral
        | SyntaxKind::VoidExpression
        | SyntaxKind::NullKeyword
        | SyntaxKind::StringLiteral
        | SyntaxKind::NoSubstitutionTemplateLiteral
        | SyntaxKind::TemplateExpression => false,
        SyntaxKind::Identifier => node.text() != "undefined",
        SyntaxKind::ConditionalExpression => {
            syntactically_uncertain(node.when_true()) || syntactically_uncertain(node.when_false())
        }
        _ => true,
    }
}

/// Operator precedence of an expression for parenthesization decisions.
/// Higher binds tighter.
pub(super) fn precedence(node: Node) -> u8 {
    let node_kind = node.kind();
    match node_kind {
        SyntaxKind::CommaToken => 0,
        SyntaxKind::BinaryExpression => binary_precedence(node.operator_token().kind()),
        SyntaxKind::ConditionalExpression
        | SyntaxKind::ArrowFunction
        | SyntaxKind::YieldExpression => 2,
        SyntaxKind::AsExpression | SyntaxKind::SatisfiesExpression => 10,
        SyntaxKind::PrefixUnaryExpression
        | SyntaxKind::TypeOfExpression
        | SyntaxKind::VoidExpression
        | SyntaxKind::DeleteExpression
        | SyntaxKind::AwaitExpression
        | SyntaxKind::TypeAssertionExpression => 15,
        SyntaxKind::PostfixUnaryExpression => 16,
        _ => 20,
    }
}

pub(super) fn binary_precedence(operator: SyntaxKind) -> u8 {
    match operator {
        SyntaxKind::CommaToken => 0,
        SyntaxKind::EqualsToken
        | SyntaxKind::PlusEqualsToken
        | SyntaxKind::MinusEqualsToken
        | SyntaxKind::AsteriskEqualsToken
        | SyntaxKind::AsteriskAsteriskEqualsToken
        | SyntaxKind::SlashEqualsToken
        | SyntaxKind::PercentEqualsToken
        | SyntaxKind::LessThanLessThanEqualsToken
        | SyntaxKind::GreaterThanGreaterThanEqualsToken
        | SyntaxKind::GreaterThanGreaterThanGreaterThanEqualsToken
        | SyntaxKind::AmpersandEqualsToken
        | SyntaxKind::BarEqualsToken
        | SyntaxKind::BarBarEqualsToken
        | SyntaxKind::AmpersandAmpersandEqualsToken
        | SyntaxKind::QuestionQuestionEqualsToken
        | SyntaxKind::CaretEqualsToken => 1,
        SyntaxKind::QuestionQuestionToken => 3,
        SyntaxKind::BarBarToken => 3,
        SyntaxKind::AmpersandAmpersandToken => 4,
        SyntaxKind::BarToken => 5,
        SyntaxKind::CaretToken => 6,
        SyntaxKind::AmpersandToken => 7,
        SyntaxKind::EqualsEqualsToken
        | SyntaxKind::ExclamationEqualsToken
        | SyntaxKind::EqualsEqualsEqualsToken
        | SyntaxKind::ExclamationEqualsEqualsToken => 8,
        SyntaxKind::LessThanToken
        | SyntaxKind::GreaterThanToken
        | SyntaxKind::LessThanEqualsToken
        | SyntaxKind::GreaterThanEqualsToken
        | SyntaxKind::InstanceOfKeyword
        | SyntaxKind::InKeyword => 10,
        SyntaxKind::LessThanLessThanToken
        | SyntaxKind::GreaterThanGreaterThanToken
        | SyntaxKind::GreaterThanGreaterThanGreaterThanToken => 11,
        SyntaxKind::PlusToken | SyntaxKind::MinusToken => 12,
        SyntaxKind::AsteriskToken | SyntaxKind::SlashToken | SyntaxKind::PercentToken => 13,
        SyntaxKind::AsteriskAsteriskToken => 14,
        _ => 0,
    }
}

/// Whether the position of `node` accepts any assignment-level expression
/// without parentheses.
fn accepts_assignment(node: Node) -> bool {
    let parent = node.parent();
    match parent.kind() {
        SyntaxKind::ParenthesizedExpression
        | SyntaxKind::ExpressionStatement
        | SyntaxKind::ReturnStatement
        | SyntaxKind::ThrowStatement
        | SyntaxKind::IfStatement
        | SyntaxKind::WhileStatement
        | SyntaxKind::DoStatement
        | SyntaxKind::SwitchStatement
        | SyntaxKind::CaseClause
        | SyntaxKind::TemplateSpan
        | SyntaxKind::SpreadElement
        | SyntaxKind::SpreadAssignment
        | SyntaxKind::ArrayLiteralExpression
        | SyntaxKind::JsxExpression
        | SyntaxKind::ComputedPropertyName => true,
        SyntaxKind::ElementAccessExpression => parent.argument_expression() == node,
        SyntaxKind::CallExpression | SyntaxKind::NewExpression => parent.expression() != node,
        SyntaxKind::VariableDeclaration
        | SyntaxKind::PropertyAssignment
        | SyntaxKind::Parameter
        | SyntaxKind::BindingElement
        | SyntaxKind::PropertyDeclaration => parent.initializer() == node,
        SyntaxKind::ConditionalExpression => parent.condition() != node,
        SyntaxKind::ArrowFunction => parent.body() == node,
        SyntaxKind::ForStatement => true,
        _ => false,
    }
}

/// Text that replaces expression `original` with an expression of
/// precedence `replacement_precedence`, parenthesized when needed.
pub(super) fn replace_expr(
    original: Node,
    replacement: &str,
    replacement_precedence: u8,
) -> String {
    let hazard = starts_statement(original)
        && (replacement.starts_with('{')
            || replacement.starts_with("function")
            || replacement.starts_with("class")
            || replacement.starts_with("let")
            || replacement.starts_with("async"));
    let parent = original.parent();
    let exponent_base = parent.kind() == SyntaxKind::BinaryExpression
        && parent.operator_token().kind() == SyntaxKind::AsteriskAsteriskToken
        && parent.left() == original
        && replacement_precedence < 16;
    let coalesce_mix = parent.kind() == SyntaxKind::BinaryExpression
        && ((parent.operator_token().kind() == SyntaxKind::QuestionQuestionToken
            && matches!(replacement_precedence, 3 | 4))
            || (matches!(
                parent.operator_token().kind(),
                SyntaxKind::BarBarToken | SyntaxKind::AmpersandAmpersandToken
            ) && replacement.contains("??")));
    // The left operand of a left-associative operator accepts the same
    // precedence.
    let left_operand = parent.kind() == SyntaxKind::BinaryExpression
        && parent.left() == original
        && parent.operator_token().kind() != SyntaxKind::AsteriskAsteriskToken
        && binary_precedence(parent.operator_token().kind()) >= 3
        && replacement_precedence >= binary_precedence(parent.operator_token().kind());
    let fits = ((replacement_precedence >= precedence(original) && replacement_precedence >= 3)
        || left_operand
        || accepts_assignment(original))
        && !coalesce_mix;
    if hazard || exponent_base || !fits {
        format!("({replacement})")
    } else {
        replacement.to_owned()
    }
}

/// Pads `text`, which replaces `source[start..end]`, with spaces where it
/// would otherwise merge with the neighboring source into another token
/// (`return(x)` to `returnx`, `a - (-1)` to `a--1`).
pub(super) fn guard_glue(source: &str, start: usize, end: usize, text: String) -> String {
    fn word(c: char) -> bool {
        c.is_alphanumeric() || c == '_' || c == '$' || !c.is_ascii()
    }
    fn glues(a: char, b: char) -> bool {
        (word(a) && word(b))
            || (a == '+' && b == '+')
            || (a == '-' && b == '-')
            || (a == '/' && (b == '/' || b == '*'))
            || (a == '.' && b.is_ascii_digit())
            || (a.is_ascii_digit() && b == '.')
    }
    let before = source[..start].chars().next_back();
    let after = source[end..].chars().next();
    let mut text = text;
    if let (Some(a), Some(b)) = (before, text.chars().next())
        && glues(a, b)
    {
        text.insert(0, ' ');
    }
    if let (Some(a), Some(b)) = (text.chars().next_back(), after)
        && glues(a, b)
    {
        text.push(' ');
    }
    text
}

/// Whether `node` begins an expression statement, an arrow function's
/// concise body or an `export default` expression, where a leading `{`,
/// `function` or `class` would change the parse.
fn starts_statement(node: Node) -> bool {
    let start = Ctx::start(node);
    let mut current = node;
    loop {
        let parent = current.parent();
        if parent.is_nil() {
            return false;
        }
        match parent.kind() {
            SyntaxKind::ExpressionStatement | SyntaxKind::ExportAssignment => return true,
            SyntaxKind::ArrowFunction => return parent.body() == current,
            _ => {}
        }
        if Ctx::start(parent) != start || !is_expression_node(parent) {
            return false;
        }
        current = parent;
    }
}

/// A statement text that ends with a statement boundary.
pub(super) fn terminated(text: &str) -> String {
    let trimmed = text.trim_end();
    if trimmed.ends_with(';') || trimmed.ends_with('}') {
        trimmed.to_owned()
    } else {
        format!("{trimmed};")
    }
}

/// Replacement text for deleting statement `statement` from a statement
/// list: an empty statement when the previous statement relies on automatic
/// semicolon insertion.
pub(super) fn deleted_statement(ctx: &Ctx, file: usize, statement: Node) -> &'static str {
    let parent = statement.parent();
    let previous = parent
        .statements()
        .iter()
        .take_while(|&s| s != statement)
        .last();
    match previous {
        Some(previous) => {
            let text = ctx.text(file, previous);
            if text.ends_with(';') || text.ends_with('}') {
                ""
            } else {
                ";"
            }
        }
        None => "",
    }
}

/// Whether removing `region` would remove an assignment that the checker's
/// assignment narrowing relies on: a write to a variable declared outside the
/// region whose declared type is a union or evolving.
pub(super) fn removes_narrowing_writes(ctx: &mut Ctx, file: usize, region: Node) -> bool {
    let nodes: Vec<Node> = {
        let mut nodes = Vec::new();
        super::super::collect(region, &mut nodes);
        nodes
    };
    for node in nodes {
        if node.kind() != SyntaxKind::Identifier || !is_write_access(node) {
            continue;
        }
        let symbol = ctx.resolve(node);
        if symbol.is_nil() {
            return true;
        }
        let declaration = ctx.checker.sym(symbol).value_declaration;
        if declaration.is_nil() {
            return true;
        }
        if contains(region, declaration) {
            continue;
        }
        if declaration.kind() == SyntaxKind::VariableDeclaration
            && declaration.type_().is_nil()
            && declaration.initializer().is_nil()
        {
            return true;
        }
        let t = ctx.checker.get_type_of_symbol(symbol);
        if t.is_nil() || ctx.checker.ty(t).flags().intersects(TypeFlags::Union) {
            return true;
        }
        let _ = file;
    }
    false
}

/// Whether removing `region` would remove a hoisted `var` declaration or a
/// function declaration visible outside it.
pub(super) fn declares_hoisted(region: Node) -> bool {
    let mut found = false;
    let mut stack = vec![region];
    while let Some(node) = stack.pop() {
        match node.kind() {
            SyntaxKind::VariableDeclarationList
                if !node.flags().intersects(NodeFlags::BlockScoped) =>
            {
                found = true;
            }
            SyntaxKind::FunctionDeclaration if node == region => found = true,
            SyntaxKind::FunctionDeclaration
            | SyntaxKind::FunctionExpression
            | SyntaxKind::ArrowFunction
            | SyntaxKind::MethodDeclaration
            | SyntaxKind::GetAccessor
            | SyntaxKind::SetAccessor
            | SyntaxKind::Constructor
            | SyntaxKind::ClassDeclaration
            | SyntaxKind::ClassExpression => continue,
            _ => {}
        }
        if found {
            return true;
        }
        node.for_each_child(|child| {
            stack.push(child);
            false
        });
    }
    false
}

/// Type-neutrality oracle for removing a test. TypeScript narrows references
/// inside `region` (and after it when `after` is set) using the test; the
/// removal is type-neutral when every remaining reference of each binding
/// mentioned in `test` already has its declared type, so no narrowing is
/// lost. `removed` lists subtrees that disappear.
pub(super) fn narrowing_neutral(
    ctx: &mut Ctx,
    test: Node,
    region: Node,
    after: bool,
    removed: &[Node],
) -> bool {
    let mut nodes = Vec::new();
    super::super::collect(test, &mut nodes);
    let mut symbols = Vec::new();
    let mut accessed = HashSet::new();
    for node in nodes {
        match node.kind() {
            SyntaxKind::Identifier => {
                let parent = node.parent();
                if matches!(
                    parent.kind(),
                    SyntaxKind::PropertyAccessExpression | SyntaxKind::QualifiedName
                ) && parent.name() == node
                {
                    continue;
                }
                let symbol = ctx.resolve(node);
                if symbol.is_nil() {
                    continue;
                }
                if !symbols.contains(&symbol) {
                    symbols.push(symbol);
                }
                // A member access in the test may narrow that member too.
                let outer = walk_up_outer(node);
                if matches!(
                    outer.parent().kind(),
                    SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
                ) && outer.parent().expression() == outer
                {
                    accessed.insert(symbol);
                }
            }
            SyntaxKind::ThisKeyword => return false,
            _ => {}
        }
    }
    let region_end = if after {
        // References after the region inside the enclosing function body.
        let mut function = region.parent();
        while function.is_some()
            && !matches!(
                function.kind(),
                SyntaxKind::FunctionDeclaration
                    | SyntaxKind::FunctionExpression
                    | SyntaxKind::ArrowFunction
                    | SyntaxKind::MethodDeclaration
                    | SyntaxKind::Constructor
                    | SyntaxKind::GetAccessor
                    | SyntaxKind::SetAccessor
                    | SyntaxKind::SourceFile
                    | SyntaxKind::ClassStaticBlockDeclaration
            )
        {
            function = function.parent();
        }
        function.end() as usize
    } else {
        region.end() as usize
    };
    let region_start = region.pos() as usize;
    let region_file = get_source_file_of_node(region);
    for symbol in symbols {
        let flags = ctx.checker.sym(symbol).flags;
        if !flags.intersects(SymbolFlags::Variable | SymbolFlags::Property) {
            continue;
        }
        let declared = ctx.checker.get_type_of_symbol(symbol);
        let refs: Vec<Node> = ctx
            .refs(symbol)
            .iter()
            .filter(|r| matches!(r.kind, RefKind::Read | RefKind::Call))
            .map(|r| r.node)
            .collect();
        for reference in refs {
            if get_source_file_of_node(reference) != region_file {
                continue;
            }
            let position = reference.pos() as usize;
            if position < region_start || position >= region_end {
                continue;
            }
            if contains(test, reference) || removed.iter().any(|&r| contains(r, reference)) {
                continue;
            }
            if accessed.contains(&symbol) {
                let outer = walk_up_outer(reference);
                if matches!(
                    outer.parent().kind(),
                    SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
                ) && outer.parent().expression() == outer
                {
                    return false;
                }
            }
            let actual = ctx.checker.get_type_at_location(reference);
            if actual != declared {
                return false;
            }
        }
    }
    true
}

/// Formats a number as a JavaScript numeric literal that parses back to
/// exactly `value`, or `None` for values without a literal form.
pub(super) fn number_literal(value: f64) -> Option<String> {
    if !value.is_finite() {
        return None;
    }
    if value == 0.0 {
        return Some(if value.is_sign_negative() { "-0" } else { "0" }.to_owned());
    }
    let text = format!("{value}");
    // Rust prints the shortest round-tripping decimal without an exponent.
    (text.parse::<f64>().ok() == Some(value)).then_some(text)
}

/// A double-quoted JavaScript string literal with exactly `value`.
pub(super) fn string_literal(value: &str) -> String {
    let mut out = String::with_capacity(value.len() + 2);
    out.push('"');
    for c in value.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\u{2028}' => out.push_str("\\u2028"),
            '\u{2029}' => out.push_str("\\u2029"),
            c if (c as u32) < 0x20 => out.push_str(&format!("\\x{:02x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

/// JavaScript semantics of `a <operator> b` over primitive literals, when
/// the result is exactly representable.
pub(super) fn evaluate(operator: SyntaxKind, a: &Lit, b: &Lit) -> Option<Lit> {
    use SyntaxKind as K;
    let number = |lit: &Lit| match lit {
        Lit::Num(n) => Some(*n),
        Lit::Bool(b) => Some(if *b { 1.0 } else { 0.0 }),
        Lit::Null => Some(0.0),
        Lit::Undefined => Some(f64::NAN),
        // String-to-number conversion is not modeled.
        Lit::Str(_) => None,
    };
    match operator {
        K::EqualsEqualsEqualsToken => Some(Lit::Bool(a.strict_equals(b))),
        K::ExclamationEqualsEqualsToken => Some(Lit::Bool(!a.strict_equals(b))),
        K::EqualsEqualsToken | K::ExclamationEqualsToken => {
            let equal = if a.kinds() == b.kinds() {
                a.strict_equals(b)
            } else {
                let nullish = |l: &Lit| matches!(l, Lit::Undefined | Lit::Null);
                if nullish(a) || nullish(b) {
                    nullish(a) && nullish(b)
                } else {
                    return None;
                }
            };
            Some(Lit::Bool(equal == (operator == K::EqualsEqualsToken)))
        }
        K::LessThanToken
        | K::GreaterThanToken
        | K::LessThanEqualsToken
        | K::GreaterThanEqualsToken => {
            let ordering = match (a, b) {
                (Lit::Num(x), Lit::Num(y)) => x.partial_cmp(y),
                (Lit::Str(x), Lit::Str(y)) if x.is_ascii() && y.is_ascii() => Some(x.cmp(y)),
                _ => return None,
            };
            let result = match ordering {
                // Any comparison with NaN is false.
                None => false,
                Some(o) => match operator {
                    K::LessThanToken => o.is_lt(),
                    K::GreaterThanToken => o.is_gt(),
                    K::LessThanEqualsToken => o.is_le(),
                    _ => o.is_ge(),
                },
            };
            Some(Lit::Bool(result))
        }
        K::PlusToken => match (a, b) {
            (Lit::Str(x), Lit::Str(y)) => Some(Lit::Str(format!("{x}{y}"))),
            (Lit::Str(x), other) => Some(Lit::Str(format!("{x}{}", primitive_string(other)?))),
            (other, Lit::Str(y)) => Some(Lit::Str(format!("{}{y}", primitive_string(other)?))),
            _ => finite(number(a)? + number(b)?),
        },
        K::MinusToken => finite(number(a)? - number(b)?),
        K::AsteriskToken => finite(number(a)? * number(b)?),
        K::SlashToken => finite(number(a)? / number(b)?),
        K::PercentToken => finite(number(a)? % number(b)?),
        K::AmpersandToken => Some(Lit::Num(
            (to_int32(number(a)?) & to_int32(number(b)?)) as f64,
        )),
        K::BarToken => Some(Lit::Num(
            (to_int32(number(a)?) | to_int32(number(b)?)) as f64,
        )),
        K::CaretToken => Some(Lit::Num(
            (to_int32(number(a)?) ^ to_int32(number(b)?)) as f64,
        )),
        K::LessThanLessThanToken => {
            let shift = to_uint32(number(b)?) & 31;
            Some(Lit::Num(to_int32(number(a)?).wrapping_shl(shift) as f64))
        }
        K::GreaterThanGreaterThanToken => {
            let shift = to_uint32(number(b)?) & 31;
            Some(Lit::Num((to_int32(number(a)?) >> shift) as f64))
        }
        K::GreaterThanGreaterThanGreaterThanToken => {
            let shift = to_uint32(number(b)?) & 31;
            Some(Lit::Num((to_uint32(number(a)?) >> shift) as f64))
        }
        _ => None,
    }
}

fn finite(value: f64) -> Option<Lit> {
    value.is_finite().then_some(Lit::Num(value))
}

/// `ToString` of a primitive whose result is certain (integers only for
/// numbers, so no exponent formatting is involved).
fn primitive_string(lit: &Lit) -> Option<String> {
    match lit {
        Lit::Undefined => Some("undefined".to_owned()),
        Lit::Null => Some("null".to_owned()),
        Lit::Bool(b) => Some(b.to_string()),
        Lit::Num(n) if n.fract() == 0.0 && n.abs() < 9_007_199_254_740_992.0 => {
            Some(format!("{}", *n as i64))
        }
        Lit::Num(_) => None,
        Lit::Str(s) => Some(s.clone()),
    }
}

fn to_uint32(value: f64) -> u32 {
    if !value.is_finite() {
        return 0;
    }
    let truncated = value.trunc();
    truncated.rem_euclid(4_294_967_296.0) as u32
}

fn to_int32(value: f64) -> i32 {
    to_uint32(value) as i32
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bare_node_core_inventory_matches_node_24() {
        let output = std::process::Command::new("node")
            .args([
                "-e",
                r#"const { builtinModules, isBuiltin } = require('node:module');
if (process.versions.node.split('.')[0] !== '24') throw new Error('Node 24 required');
if (isBuiltin('test') || !isBuiltin('node:test')) throw new Error('prefix-only boundary');
console.log(JSON.stringify(builtinModules.filter(name => !name.startsWith('node:')).sort()));"#,
            ])
            .output()
            .expect("Node 24 is required for the bare builtin inventory check");
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        let host_modules: Vec<String> = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(
            BARE_NODE_CORE_MODULES,
            host_modules.iter().map(String::as_str).collect::<Vec<_>>()
        );
        for name in ["test", "sea", "sqlite", "test/reporters", "http-polyfill"] {
            assert!(!is_bare_node_core_module(name), "{name}");
        }
    }

    #[test]
    fn numbers_round_trip() {
        assert_eq!(parse_number("0x10"), Some(16.0));
        assert_eq!(parse_number("1_000"), Some(1000.0));
        assert_eq!(parse_number("1e3"), Some(1000.0));
        assert_eq!(parse_number(".5"), Some(0.5));
        assert_eq!(parse_number("010"), None);
        assert_eq!(number_literal(-0.0).as_deref(), Some("-0"));
        assert_eq!(
            number_literal(0.1 + 0.2).as_deref(),
            Some("0.30000000000000004")
        );
        assert_eq!(number_literal(f64::NAN), None);
        assert_eq!(string_literal("a\"b\\\n"), "\"a\\\"b\\\\\\n\"");
    }

    #[test]
    fn equality_respects_nan_and_signed_zero() {
        let nan = Fact::lit(Lit::Num(f64::NAN));
        let zero = Fact::lit(Lit::Num(0.0));
        let negative_zero = Fact::lit(Lit::Num(-0.0));
        assert_eq!(equality(&nan, &nan, true), Some(false));
        assert_eq!(equality(&zero, &negative_zero, true), Some(true));
        let object = Fact::of(Kinds::OBJECT);
        let null = Fact::lit(Lit::Null);
        assert_eq!(equality(&object, &null, false), Some(false));
        assert_eq!(equality(&Fact::top(), &null, false), None);
        assert_eq!(Fact::of(Kinds::HTMLDDA).truthy(), Some(false));
        assert_eq!(Fact::of(Kinds::HTMLDDA).nullish(), Some(false));
    }
}
