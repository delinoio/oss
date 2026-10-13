// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `primitive-operation-simplification`: removes redundant primitive
//! conversions and folds exact primitive operations.
//!
//! Supported forms:
//! - `!!x` and `Boolean(x)` in test positions become `x`; elsewhere they become
//!   `x` for a proven boolean and `Boolean(x)` becomes `!!x`.
//! - `String(s)`, `"" + s`, `s + ""` and `` `${s}` `` become `s` for a proven
//!   string; `String(p)` becomes `"" + p` for other proven plain primitives.
//! - `Number(n)` and `+n` become `n` for a proven number; `Number(p)` becomes
//!   `+p` for other proven plain primitives.
//! - `x === true`/`x !== false` become `x` and `x === false`/`x !== true`
//!   become `!x` for a proven boolean.
//! - Comparisons, arithmetic, bitwise operations and concatenation over proven
//!   literal values fold to a literal when the result is exact.
//!
//! Proof obligations: operand kinds come from [`Facts`] (runtime evidence
//! only). A conversion is removed only when the operand can never be an
//! object (no `valueOf`/`toString`/`Symbol.toPrimitive` call), a symbol or a
//! BigInt where the semantics differ. `String`, `Number` and `Boolean` must
//! resolve to the unmodified built-in global (not a local, not written by
//! application code). Folding uses IEEE-754 double arithmetic exactly as
//! JavaScript does, preserves `-0`, refuses `NaN`/infinite results and
//! BigInt operands, and only drops pure operands. Replacements keep the
//! checker type, or occur where only `ToBoolean` or string conversion of the
//! value is observed.
use anyhow::Result;

use super::{
    super::analysis::{Ctx, skip_outer, walk_up_outer},
    runtime_facts::{
        Fact, Facts, Kinds, Lit, evaluate, guard_glue, in_test_context, lib_only,
        narrowing_neutral, number_literal, precedence, replace_expr, same_type, span,
        string_literal, syntactically_uncertain, test_scope, type_all, type_constituents,
    },
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::PrimitiveOperationSimplification;

pub(in crate::compiler) fn plan(ctx: &mut Ctx, _enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let mut facts = Facts::new();
    let mut planner = Planner {
        facts: &mut facts,
        out,
        candidates: 0,
    };
    for file in 0..ctx.files.len() {
        crate::app::check_cancelled()?;
        if ctx.files[file].js {
            continue;
        }
        let nodes = ctx.files[file].nodes.clone();
        for node in nodes {
            match node.kind() {
                SyntaxKind::PrefixUnaryExpression => planner.prefix(ctx, file, node),
                SyntaxKind::CallExpression => planner.conversion_call(ctx, file, node),
                SyntaxKind::BinaryExpression => planner.binary(ctx, file, node),
                SyntaxKind::TemplateExpression => planner.template(ctx, file, node),
                _ => {}
            }
        }
    }
    tracing::debug!(
        pass = %PASS,
        candidates = planner.candidates,
        "primitive candidates planned"
    );
    Ok(())
}

struct Planner<'a> {
    facts: &'a mut Facts,
    out: &'a mut Plan,
    candidates: usize,
}

fn key(ctx: &Ctx, file: usize, tag: &str, node: Node) -> String {
    let (start, end) = span(node);
    format!("{tag}:{}:{start}-{end}", ctx.files[file].name)
}

/// Text of `node` usable as an operand at `minimum` precedence.
fn operand(ctx: &Ctx, file: usize, node: Node, minimum: u8) -> String {
    let text = ctx.text(file, node);
    if precedence(node) >= minimum {
        text.to_owned()
    } else {
        format!("({text})")
    }
}

impl Planner<'_> {
    fn emit(&mut self, ctx: &Ctx, file: usize, tag: &str, node: Node, text: String) {
        let (start, end) = span(node);
        let mut candidate =
            Candidate::new(PASS, key(ctx, file, tag, node), ctx.location(file, node));
        let text = guard_glue(&ctx.files[file].text, start, end, text);
        candidate.edits = vec![FileEdit::replace(&ctx.files[file].name, start, end, text)];
        self.out.candidates.push(candidate);
        self.candidates += 1;
    }

    fn skip(&mut self, ctx: &Ctx, file: usize, tag: &str, node: Node, reason: Reason) {
        self.out
            .skips
            .push(ctx.skip(PASS, key(ctx, file, tag, node), file, node, reason));
    }

    /// Replaces `node` by `value` (an existing expression) when the checker
    /// type is unchanged or only `ToBoolean` of the value matters.
    fn substitute(&mut self, ctx: &mut Ctx, file: usize, tag: &str, node: Node, value: Node) {
        let test = in_test_context(node);
        if test && !syntactically_uncertain(value) {
            self.skip(ctx, file, tag, node, Reason::TypeConstraint);
            return;
        }
        if !test && !same_type(ctx, node, value) {
            self.skip(ctx, file, tag, node, Reason::TypeConstraint);
            return;
        }
        // An identifier needs none of the parentheses around the replaced
        // expression.
        let mut target = node;
        if skip_outer(value).kind() == SyntaxKind::Identifier && skip_outer(value) == value {
            while target.parent().kind() == SyntaxKind::ParenthesizedExpression {
                target = target.parent();
            }
        }
        let text = replace_expr(target, ctx.text(file, value), precedence(value));
        self.emit(ctx, file, tag, target, text);
    }

    /// `!!x` and `+n`.
    fn prefix(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        match node.operator() {
            SyntaxKind::ExclamationToken => {
                let inner = skip_outer(node.operand());
                if inner.kind() != SyntaxKind::PrefixUnaryExpression
                    || inner.operator() != SyntaxKind::ExclamationToken
                    || inner != node.operand()
                {
                    return;
                }
                let value = inner.operand();
                if in_test_context(node) {
                    // ToBoolean(ToBoolean(x)) is ToBoolean(x); TypeScript
                    // narrows `!!x` and `x` identically.
                    if syntactically_uncertain(value) {
                        let text = replace_expr(node, ctx.text(file, value), precedence(value));
                        self.emit(ctx, file, "not-not", node, text);
                    }
                    return;
                }
                if self.facts.expr(ctx, value).is(Kinds::BOOLEAN) {
                    self.substitute(ctx, file, "not-not", node, value);
                }
            }
            SyntaxKind::PlusToken => {
                let value = node.operand();
                if self.facts.expr(ctx, value).is(Kinds::NUMBER) {
                    self.substitute(ctx, file, "unary-plus", node, value);
                }
            }
            _ => {}
        }
    }

    /// `Boolean(x)`, `String(x)` and `Number(x)`.
    fn conversion_call(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let callee = skip_outer(node.expression());
        if callee.kind() != SyntaxKind::Identifier || node.question_dot_token().is_some() {
            return;
        }
        let name = callee.text();
        if !matches!(name, "Boolean" | "String" | "Number") {
            return;
        }
        let arguments = node.arguments();
        if arguments.len() != 1 || arguments.get(0).kind() == SyntaxKind::SpreadElement {
            return;
        }
        if !node.type_arguments().is_empty() {
            return;
        }
        let value = arguments.get(0);
        if !self.facts.intrinsic_global(ctx, callee, name) {
            // A written global or a patched built-in; a local binding of the
            // same name is simply a different function.
            let symbol = ctx.resolve(callee);
            if !symbol.is_nil() {
                let declarations = ctx.checker.sym(symbol).declarations.to_vec();
                if lib_only(&declarations) {
                    self.skip(
                        ctx,
                        file,
                        "conversion",
                        node,
                        Reason::IntrinsicMayBeReplaced,
                    );
                }
            }
            return;
        }
        let fact = self.facts.expr(ctx, value);
        match name {
            "Boolean" => {
                if in_test_context(node) {
                    if syntactically_uncertain(value) {
                        let text = replace_expr(node, ctx.text(file, value), precedence(value));
                        self.emit(ctx, file, "boolean", node, text);
                    }
                } else if fact.is(Kinds::BOOLEAN) {
                    self.substitute(ctx, file, "boolean", node, value);
                } else if boolean_typed_negation(ctx, value) {
                    let text = format!("!!{}", operand(ctx, file, value, 15));
                    let text = replace_expr(node, &text, 15);
                    self.emit(ctx, file, "boolean", node, text);
                }
            }
            "String" => {
                if fact.is(Kinds::STRING) {
                    self.substitute(ctx, file, "string", node, value);
                } else if fact.is(Kinds::PLAIN_PRIMITIVES.union(Kinds::BIGINT)) {
                    let text = format!("\"\" + {}", operand(ctx, file, value, 13));
                    let text = replace_expr(node, &text, 12);
                    self.emit(ctx, file, "string", node, text);
                } else if type_all(ctx, value, TypeFlags::StringLike) {
                    self.skip(ctx, file, "string", node, unproven(&fact));
                }
            }
            _ => {
                if fact.is(Kinds::NUMBER) {
                    self.substitute(ctx, file, "number", node, value);
                } else if fact.is(Kinds::PLAIN_PRIMITIVES) {
                    let text = format!("+{}", operand(ctx, file, value, 15));
                    let text = replace_expr(node, &text, 15);
                    self.emit(ctx, file, "number", node, text);
                } else if type_all(ctx, value, TypeFlags::NumberLike) {
                    self.skip(ctx, file, "number", node, unproven(&fact));
                }
            }
        }
    }

    fn binary(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let operator = node.operator_token().kind();
        let (left, right) = (node.left(), node.right());
        match operator {
            SyntaxKind::PlusToken => {
                for (empty, value) in [(left, right), (right, left)] {
                    let empty = skip_outer(empty);
                    if matches!(
                        empty.kind(),
                        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
                    ) && empty.text().is_empty()
                        && self.facts.expr(ctx, value).is(Kinds::STRING)
                    {
                        self.substitute(ctx, file, "concat-empty", node, value);
                        return;
                    }
                }
            }
            SyntaxKind::EqualsEqualsEqualsToken
            | SyntaxKind::ExclamationEqualsEqualsToken
            | SyntaxKind::EqualsEqualsToken
            | SyntaxKind::ExclamationEqualsToken => {
                let negate = matches!(
                    operator,
                    SyntaxKind::ExclamationEqualsEqualsToken | SyntaxKind::ExclamationEqualsToken
                );
                for (literal, value) in [(left, right), (right, left)] {
                    let literal = skip_outer(literal);
                    let expected = match literal.kind() {
                        SyntaxKind::TrueKeyword => true,
                        SyntaxKind::FalseKeyword => false,
                        _ => continue,
                    };
                    if !self.facts.expr(ctx, value).is(Kinds::BOOLEAN)
                        || !type_all(ctx, value, TypeFlags::BooleanLike)
                    {
                        continue;
                    }
                    // For an all-boolean type, TypeScript narrows `x`, `!x`
                    // and `x === true` identically, so test positions keep
                    // their types; values must keep the checker type.
                    if !in_test_context(node) && !same_type(ctx, node, value) {
                        self.skip(ctx, file, "boolean-compare", node, Reason::TypeConstraint);
                        return;
                    }
                    let text = if expected != negate {
                        replace_expr(node, ctx.text(file, value), precedence(value))
                    } else {
                        replace_expr(node, &format!("!{}", operand(ctx, file, value, 15)), 15)
                    };
                    self.emit(ctx, file, "boolean-compare", node, text);
                    return;
                }
            }
            _ => {}
        }
        self.fold(ctx, file, node);
    }

    /// Folds an operation over proven literal operands.
    fn fold(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let operator = node.operator_token().kind();
        let (left, right) = (node.left(), node.right());
        let Some(a) = self.facts.expr(ctx, left).lit else {
            return;
        };
        let Some(b) = self.facts.expr(ctx, right).lit else {
            return;
        };
        let Some(result) = evaluate(operator, &a, &b) else {
            return;
        };
        if !self.facts.pure(ctx, left) || !self.facts.pure(ctx, right) {
            self.skip(ctx, file, "fold", node, Reason::SideEffects);
            return;
        }
        let (text, result_precedence) = match &result {
            Lit::Bool(b) => (b.to_string(), 20),
            Lit::Num(n) => {
                let Some(text) = number_literal(*n) else {
                    return;
                };
                let p = if text.starts_with('-') { 15 } else { 20 };
                (text, p)
            }
            Lit::Str(s) => (string_literal(s), 20),
            Lit::Undefined | Lit::Null => return,
        };
        if !literal_context(ctx, node, &result) {
            self.skip(ctx, file, "fold", node, Reason::TypeConstraint);
            return;
        }
        if matches!(result, Lit::Bool(_)) {
            // A comparison may narrow the bindings it mentions.
            let (scope, after) = test_scope(node);
            if !narrowing_neutral(ctx, node, scope, after, &[]) {
                self.skip(ctx, file, "fold", node, Reason::TypeConstraint);
                return;
            }
        }
        let text = replace_expr(node, &text, result_precedence);
        self.emit(ctx, file, "fold", node, text);
    }

    /// `` `${s}` `` with a proven string `s`.
    fn template(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let spans: Vec<Node> = node.template_spans().nodes().iter().collect();
        if spans.len() != 1
            || !node.head().text().is_empty()
            || !spans[0].literal().text().is_empty()
        {
            return;
        }
        let value = spans[0].expression();
        if self.facts.expr(ctx, value).is(Kinds::STRING) {
            self.substitute(ctx, file, "template", node, value);
        }
    }
}

fn unproven(fact: &Fact) -> Reason {
    if fact.kinds.intersects(Kinds::OBJECTS) {
        Reason::UnprovenRuntimeFact
    } else {
        Reason::NotConstant
    }
}

/// `!!x` has the checker type `boolean` (not a literal): the type of `x` has
/// both possibly-truthy and possibly-falsy constituents.
fn boolean_typed_negation(ctx: &mut Ctx, value: Node) -> bool {
    let t = ctx.checker.get_type_at_location(value);
    if t.is_nil() {
        return false;
    }
    let mut maybe_truthy = false;
    let mut maybe_falsy = false;
    for c in type_constituents(ctx, t) {
        let flags = ctx.checker.ty(c).flags();
        if flags.intersects(TypeFlags::Any | TypeFlags::Unknown) {
            return true;
        }
        if flags.intersects(TypeFlags::Instantiable) {
            return false;
        }
        if flags.intersects(TypeFlags::Null | TypeFlags::Undefined | TypeFlags::Void) {
            maybe_falsy = true;
        } else if flags.intersects(TypeFlags::Literal) {
            let truthy = match ctx.checker.ty(c).as_literal_type().value() {
                Some(LiteralValue::Boolean(b)) => b,
                Some(LiteralValue::String(s)) => !s.is_empty(),
                Some(LiteralValue::Number(n)) => n.0 != 0.0 && !n.0.is_nan(),
                _ => return false,
            };
            if truthy {
                maybe_truthy = true;
            } else {
                maybe_falsy = true;
            }
        } else if flags.intersects(
            TypeFlags::String | TypeFlags::Number | TypeFlags::BigInt | TypeFlags::Boolean,
        ) {
            maybe_truthy = true;
            maybe_falsy = true;
        } else {
            maybe_truthy = true;
        }
    }
    maybe_truthy && maybe_falsy
}

/// Whether a folded literal keeps the program's types: positions where the
/// literal type widens, is converted, or is only tested.
fn literal_context(ctx: &mut Ctx, node: Node, result: &Lit) -> bool {
    if in_test_context(node) {
        return matches!(result, Lit::Bool(_));
    }
    let outer = walk_up_outer(node);
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::TemplateSpan => true,
        SyntaxKind::VariableDeclaration => {
            parent.initializer() == outer
                && (parent.type_().is_some()
                    || !parent.parent().flags().intersects(NodeFlags::Const))
        }
        SyntaxKind::PropertyAssignment => parent.initializer() == outer,
        SyntaxKind::ArrayLiteralExpression => true,
        SyntaxKind::BinaryExpression => matches!(
            parent.operator_token().kind(),
            SyntaxKind::PlusToken
                | SyntaxKind::MinusToken
                | SyntaxKind::AsteriskToken
                | SyntaxKind::SlashToken
                | SyntaxKind::PercentToken
                | SyntaxKind::LessThanToken
                | SyntaxKind::GreaterThanToken
                | SyntaxKind::LessThanEqualsToken
                | SyntaxKind::GreaterThanEqualsToken
                | SyntaxKind::AmpersandToken
                | SyntaxKind::BarToken
                | SyntaxKind::CaretToken
                | SyntaxKind::LessThanLessThanToken
                | SyntaxKind::GreaterThanGreaterThanToken
                | SyntaxKind::GreaterThanGreaterThanGreaterThanToken
                | SyntaxKind::AsteriskAsteriskToken
        ),
        // Arguments of non-generic calls: the literal is checked against
        // the declared parameter type, and nothing is inferred from it.
        SyntaxKind::CallExpression | SyntaxKind::NewExpression if parent.expression() != outer => {
            let signature = ctx.checker.get_resolved_signature_exported(parent);
            if signature.is_nil() || signature == ctx.checker.get_unknown_signature() {
                return false;
            }
            let signature = ctx.checker.sig(signature);
            signature.type_parameters.is_empty() && signature.target.is_nil()
        }
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn folding_follows_javascript() {
        use SyntaxKind as K;
        let n = |v: f64| Lit::Num(v);
        let fold = |op, a: Lit, b: Lit| evaluate(op, &a, &b);
        assert!(
            matches!(fold(K::AsteriskToken, n(0.0), n(-1.0)), Some(Lit::Num(v)) if v == 0.0 && v.is_sign_negative())
        );
        assert!(fold(K::SlashToken, n(0.0), n(0.0)).is_none());
        assert!(fold(K::SlashToken, n(1.0), n(0.0)).is_none());
        assert!(matches!(fold(K::PercentToken, n(-5.0), n(3.0)), Some(Lit::Num(v)) if v == -2.0));
        assert!(
            matches!(fold(K::BarToken, n(4_294_967_297.0), n(0.0)), Some(Lit::Num(v)) if v == 1.0)
        );
        assert!(
            matches!(fold(K::GreaterThanGreaterThanGreaterThanToken, n(-1.0), n(0.0)), Some(Lit::Num(v)) if v == 4_294_967_295.0)
        );
        assert!(
            matches!(fold(K::LessThanLessThanToken, n(1.0), n(31.0)), Some(Lit::Num(v)) if v == -2_147_483_648.0)
        );
        assert!(matches!(
            fold(K::LessThanToken, n(f64::NAN), n(1.0)),
            Some(Lit::Bool(false))
        ));
        assert!(fold(K::PlusToken, Lit::Str("a".into()), n(1.5)).is_none());
        assert!(
            matches!(fold(K::PlusToken, Lit::Str("a".into()), n(-0.0)), Some(Lit::Str(s)) if s == "a0")
        );
        assert!(matches!(
            fold(K::EqualsEqualsToken, Lit::Null, Lit::Undefined),
            Some(Lit::Bool(true))
        ));
        assert!(fold(K::EqualsEqualsToken, Lit::Str("1".into()), n(1.0)).is_none());
    }
}
