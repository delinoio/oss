// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `intrinsic-specialization`: replaces built-in string/array/object
//! operations on proven fresh receivers with equivalent literal syntax.
//!
//! Supported forms:
//! - `[a, b].includes(x)` becomes `x === a || x === b` for literal elements.
//! - `[...[a, b], c]`, `f(...[a, b])` and `new C(...[a])` flatten the spread of
//!   a fresh array literal; `{...{a: 1}, b}` flattens the spread of a fresh
//!   object literal.
//! - `Array.from([a, b])`, `[a, b].slice()` and `[a].concat([b], c)` become
//!   array literals; `Object.assign({}, {a: 1}, {b})` becomes an object
//!   literal.
//! - `s.concat(a, b)` becomes `s + a + b` for a proven string `s`.
//! - `const [a, b] = [x, y]` becomes `const a = x, b = y`.
//!
//! Proof obligations: receivers are fresh literals in the source (or proven
//! primitive strings), never values that are merely typed as arrays or
//! strings. The invoked intrinsic and the iteration protocol must be
//! unmodified by application code ([`Facts::patched`] covers prototype and
//! static member writes, `Object.defineProperty`-style reflection and global
//! replacement). Arrays with holes are preserved because spreading,
//! `Array.from` and destructuring turn holes into `undefined`. `includes`
//! uses SameValueZero, so elements are non-NaN literals. Object literals
//! with accessors, methods, computed keys, `__proto__` or duplicate keys are
//! preserved. Rewrites that duplicate or reorder operands require pure
//! operands, and `concat` arguments must be primitives so no user
//! `toString`/`valueOf` runs. Edits keep the checker types of the original
//! expressions.
use std::collections::HashSet;

use anyhow::Result;

use super::{
    super::analysis::{Ctx, RefKind, skip_outer},
    runtime_facts::{
        Facts, Kinds, Lit, contains, guard_glue, initialized_before, is_literal_node, precedence,
        read_safe, replace_expr, same_type, span, string_value, test_scope, type_constituents,
    },
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::IntrinsicSpecialization;

/// Pseudo member name for `Symbol.iterator` in patch queries; any computed
/// member write matches it.
const ITERATOR: &str = "@@iterator";
const SPECIES: &str = "@@species";
const CONCAT_SPREADABLE: &str = "@@isConcatSpreadable";

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
                SyntaxKind::CallExpression => planner.call(ctx, file, node),
                SyntaxKind::SpreadElement => planner.array_spread(ctx, file, node),
                SyntaxKind::SpreadAssignment => planner.object_spread(ctx, file, node),
                SyntaxKind::VariableDeclaration => planner.destructuring(ctx, file, node),
                _ => {}
            }
        }
    }
    tracing::debug!(
        pass = %PASS,
        candidates = planner.candidates,
        "intrinsic candidates planned"
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

/// A fresh array literal without holes, or the reason it does not qualify.
fn dense_array(node: Node) -> Result<Node, Reason> {
    let node = skip_outer(node);
    if node.kind() != SyntaxKind::ArrayLiteralExpression {
        return Err(Reason::UnprovenRuntimeFact);
    }
    if node
        .elements()
        .iter()
        .any(|e| e.kind() == SyntaxKind::OmittedExpression)
    {
        return Err(Reason::SparseArray);
    }
    Ok(node)
}

fn elements_text(ctx: &Ctx, file: usize, array: Node) -> Vec<String> {
    array
        .elements()
        .iter()
        .map(|e| ctx.text(file, e).to_owned())
        .collect()
}

/// Property name text of a plain data property, or `None` when the property
/// is not a plain data property with a static, non-`__proto__` key.
fn data_property_name(ctx: &Ctx, property: Node) -> Option<String> {
    let name = property.name();
    let text = match property.kind() {
        SyntaxKind::PropertyAssignment => match name.kind() {
            SyntaxKind::Identifier => name.text().to_owned(),
            SyntaxKind::StringLiteral => string_value(ctx, name)?,
            SyntaxKind::NumericLiteral => {
                // Canonical numeric keys only.
                let text = name.text();
                if text.chars().all(|c| c.is_ascii_digit())
                    && (text == "0" || !text.starts_with('0'))
                {
                    text.to_owned()
                } else {
                    return None;
                }
            }
            _ => return None,
        },
        SyntaxKind::ShorthandPropertyAssignment if property.initializer().is_nil() => {
            name.text().to_owned()
        }
        _ => return None,
    };
    (text != "__proto__").then_some(text)
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

    fn array_patched(&mut self, ctx: &mut Ctx, members: &[&str]) -> bool {
        members
            .iter()
            .any(|m| self.facts.patched(ctx, Some("Array"), m))
    }

    fn call(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        if node.question_dot_token().is_some() {
            return;
        }
        let callee = skip_outer(node.expression());
        if callee.kind() != SyntaxKind::PropertyAccessExpression
            || callee.question_dot_token().is_some()
            || callee.name().kind() != SyntaxKind::Identifier
        {
            return;
        }
        let member = callee.name().text();
        let receiver = skip_outer(callee.expression());
        if receiver.kind() == SyntaxKind::ArrayLiteralExpression {
            match member {
                "includes" => self.includes(ctx, file, node, receiver),
                "slice" | "concat" => self.array_copy(ctx, file, node, receiver, member),
                _ => {}
            }
            return;
        }
        if receiver.kind() == SyntaxKind::Identifier {
            match (receiver.text(), member) {
                ("Array", "from") => return self.array_from(ctx, file, node, callee),
                ("Object", "assign") => return self.object_assign(ctx, file, node, callee),
                _ => {}
            }
        }
        if member == "concat" {
            self.string_concat(ctx, file, node, receiver);
        }
    }

    /// `[a, b].includes(x)` to `x === a || x === b`.
    fn includes(&mut self, ctx: &mut Ctx, file: usize, node: Node, array: Node) {
        let arguments = node.arguments();
        if arguments.len() != 1 {
            return;
        }
        let value = skip_outer(arguments.get(0));
        let elements: Vec<Node> = array.elements().iter().collect();
        if elements.is_empty() || elements.len() > 8 {
            return;
        }
        // Literal, non-NaN elements: SameValueZero and === agree.
        let mut literals = Vec::new();
        for &element in &elements {
            if !is_literal_node(element) {
                return;
            }
            match self.facts.expr(ctx, element).lit {
                Some(Lit::Num(n)) if n.is_nan() => return,
                Some(lit) => literals.push(lit),
                None => return,
            }
        }
        if value.kind() != SyntaxKind::Identifier || !read_safe(ctx, value) {
            self.skip(ctx, file, "includes", node, Reason::SideEffects);
            return;
        }
        if self.array_patched(ctx, &["includes"]) {
            self.skip(ctx, file, "includes", node, Reason::IntrinsicMayBeReplaced);
            return;
        }
        // Every comparison must type-check, and new narrowing must not reach
        // other references.
        if !literals.iter().all(|lit| comparable(ctx, value, lit))
            || introduces_narrowing(ctx, node, value)
        {
            self.skip(ctx, file, "includes", node, Reason::TypeConstraint);
            return;
        }
        let subject = ctx.text(file, value).to_owned();
        let text = elements
            .iter()
            .map(|&e| format!("{subject} === {}", ctx.text(file, e)))
            .collect::<Vec<_>>()
            .join(" || ");
        let text_precedence = if elements.len() == 1 { 8 } else { 3 };
        let text = replace_expr(node, &text, text_precedence);
        self.emit(ctx, file, "includes", node, text);
    }

    /// `[a].slice()` and `[a].concat([b], c)` to array literals.
    fn array_copy(&mut self, ctx: &mut Ctx, file: usize, node: Node, array: Node, member: &str) {
        let array = match dense_array(array) {
            Ok(array) => array,
            Err(reason) => {
                self.skip(ctx, file, member, node, reason);
                return;
            }
        };
        let arguments: Vec<Node> = node.arguments().iter().collect();
        let mut elements = elements_text(ctx, file, array);
        if member == "slice" {
            if !arguments.is_empty() {
                return;
            }
        } else {
            if arguments.is_empty() {
                return;
            }
            for &argument in &arguments {
                if argument.kind() == SyntaxKind::SpreadElement {
                    return;
                }
                if skip_outer(argument).kind() == SyntaxKind::ArrayLiteralExpression {
                    match dense_array(argument) {
                        Ok(inner) => elements.extend(elements_text(ctx, file, inner)),
                        Err(reason) => {
                            self.skip(ctx, file, member, node, reason);
                            return;
                        }
                    }
                } else if self.facts.expr(ctx, argument).is(Kinds::PLAIN_PRIMITIVES
                    .union(Kinds::BIGINT)
                    .union(Kinds::SYMBOL))
                {
                    // Primitives are never concat-spreadable.
                    elements.push(ctx.text(file, argument).to_owned());
                } else {
                    self.skip(ctx, file, member, node, Reason::UnprovenRuntimeFact);
                    return;
                }
            }
        }
        if self.array_patched(ctx, &[member, "constructor", SPECIES, CONCAT_SPREADABLE]) {
            self.skip(ctx, file, member, node, Reason::IntrinsicMayBeReplaced);
            return;
        }
        if !same_type(ctx, node, array) {
            self.skip(ctx, file, member, node, Reason::TypeConstraint);
            return;
        }
        let text = replace_expr(node, &format!("[{}]", elements.join(", ")), 20);
        self.emit(ctx, file, member, node, text);
    }

    /// `Array.from([a, b])` to `[a, b]`.
    fn array_from(&mut self, ctx: &mut Ctx, file: usize, node: Node, callee: Node) {
        let arguments = node.arguments();
        if arguments.len() != 1 || arguments.get(0).kind() == SyntaxKind::SpreadElement {
            return;
        }
        let source = arguments.get(0);
        let array = match dense_array(source) {
            Ok(array) => array,
            Err(Reason::UnprovenRuntimeFact) => {
                // Typed as an array, but iteration may run user code.
                if skip_outer(source).kind() == SyntaxKind::Identifier {
                    self.skip(ctx, file, "array-from", node, Reason::CustomIterator);
                }
                return;
            }
            Err(reason) => {
                self.skip(ctx, file, "array-from", node, reason);
                return;
            }
        };
        if !self.facts.intrinsic_static(ctx, callee, "Array", "from")
            || self.array_patched(ctx, &[ITERATOR])
        {
            self.skip(
                ctx,
                file,
                "array-from",
                node,
                Reason::IntrinsicMayBeReplaced,
            );
            return;
        }
        if !same_type(ctx, node, array) {
            self.skip(ctx, file, "array-from", node, Reason::TypeConstraint);
            return;
        }
        let text = replace_expr(node, ctx.text(file, array), 20);
        self.emit(ctx, file, "array-from", node, text);
    }

    /// `Object.assign({}, {a: 1}, {b})` to `{a: 1, b}`.
    fn object_assign(&mut self, ctx: &mut Ctx, file: usize, node: Node, callee: Node) {
        let arguments: Vec<Node> = node.arguments().iter().collect();
        if arguments.len() < 2 {
            return;
        }
        let target = skip_outer(arguments[0]);
        if target.kind() != SyntaxKind::ObjectLiteralExpression || !target.properties().is_empty() {
            return;
        }
        let mut names = HashSet::new();
        let mut properties = Vec::new();
        for &argument in &arguments[1..] {
            let source = skip_outer(argument);
            if source.kind() != SyntaxKind::ObjectLiteralExpression {
                return;
            }
            for property in source.properties().iter() {
                let Some(name) = data_property_name(ctx, property) else {
                    self.skip(ctx, file, "object-assign", node, Reason::Accessor);
                    return;
                };
                if !names.insert(name) {
                    self.skip(ctx, file, "object-assign", node, Reason::UnsupportedSyntax);
                    return;
                }
                properties.push(ctx.text(file, property).to_owned());
            }
        }
        if !self.facts.intrinsic_static(ctx, callee, "Object", "assign") {
            self.skip(
                ctx,
                file,
                "object-assign",
                node,
                Reason::IntrinsicMayBeReplaced,
            );
            return;
        }
        // `Object.assign` uses [[Set]]: a setter on `Object.prototype` would
        // observe it.
        let mut sorted: Vec<&String> = names.iter().collect();
        sorted.sort();
        for name in sorted {
            if self.facts.patched(ctx, Some("Object"), name) {
                self.skip(
                    ctx,
                    file,
                    "object-assign",
                    node,
                    Reason::IntrinsicMayBeReplaced,
                );
                return;
            }
        }
        // The literal's type differs from the intersection the call returns;
        // only positions that do not observe the static type are rewritten.
        let parent = super::super::analysis::walk_up_outer(node).parent();
        let type_free = match parent.kind() {
            SyntaxKind::SpreadAssignment | SyntaxKind::ExpressionStatement => true,
            SyntaxKind::VariableDeclaration => parent.type_().is_some(),
            _ => false,
        };
        if !type_free {
            self.skip(ctx, file, "object-assign", node, Reason::TypeConstraint);
            return;
        }
        let text = replace_expr(node, &format!("{{{}}}", properties.join(", ")), 20);
        self.emit(ctx, file, "object-assign", node, text);
    }

    /// `s.concat(a, b)` to `s + a + b` for a proven primitive string `s`.
    fn string_concat(&mut self, ctx: &mut Ctx, file: usize, node: Node, receiver: Node) {
        if !self.facts.expr(ctx, receiver).is(Kinds::STRING) {
            return;
        }
        let arguments: Vec<Node> = node.arguments().iter().collect();
        if arguments.is_empty() {
            return;
        }
        for &argument in &arguments {
            if argument.kind() == SyntaxKind::SpreadElement
                || !self
                    .facts
                    .expr(ctx, argument)
                    .is(Kinds::PLAIN_PRIMITIVES.union(Kinds::BIGINT))
            {
                // Objects would be converted with a different hint.
                self.skip(
                    ctx,
                    file,
                    "string-concat",
                    node,
                    Reason::UnprovenRuntimeFact,
                );
                return;
            }
        }
        if self.facts.patched(ctx, Some("String"), "concat") {
            self.skip(
                ctx,
                file,
                "string-concat",
                node,
                Reason::IntrinsicMayBeReplaced,
            );
            return;
        }
        let operand = |node: Node, minimum: u8| {
            let text = ctx.text(file, node);
            if precedence(node) >= minimum {
                text.to_owned()
            } else {
                format!("({text})")
            }
        };
        let mut text = operand(receiver, 12);
        for &argument in &arguments {
            text.push_str(" + ");
            text.push_str(&operand(argument, 13));
        }
        let text = replace_expr(node, &text, 12);
        self.emit(ctx, file, "string-concat", node, text);
    }

    /// `[...[a, b]]` and `f(...[a, b])`.
    fn array_spread(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let parent = node.parent();
        let in_arguments = matches!(
            parent.kind(),
            SyntaxKind::CallExpression | SyntaxKind::NewExpression
        ) && parent.expression() != node;
        if parent.kind() != SyntaxKind::ArrayLiteralExpression && !in_arguments {
            return;
        }
        // In a destructuring assignment target the spread is a rest element.
        if parent.kind() == SyntaxKind::ArrayLiteralExpression && is_assignment_target(parent) {
            return;
        }
        let expression = skip_outer(node.expression());
        if expression.kind() != SyntaxKind::ArrayLiteralExpression {
            return;
        }
        let array = match dense_array(expression) {
            Ok(array) => array,
            Err(reason) => {
                self.skip(ctx, file, "spread", node, reason);
                return;
            }
        };
        if array.elements().is_empty() {
            return;
        }
        if self.array_patched(ctx, &[ITERATOR]) {
            self.skip(ctx, file, "spread", node, Reason::IntrinsicMayBeReplaced);
            return;
        }
        let text = elements_text(ctx, file, array).join(", ");
        self.emit(ctx, file, "spread", node, text);
    }

    /// `{...{a: 1}}` inside an object literal.
    fn object_spread(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let outer = node.parent();
        if outer.kind() != SyntaxKind::ObjectLiteralExpression || is_assignment_target(outer) {
            return;
        }
        let inner = skip_outer(node.expression());
        if inner.kind() != SyntaxKind::ObjectLiteralExpression || inner.properties().is_empty() {
            return;
        }
        let mut names = HashSet::new();
        for property in outer.properties().iter() {
            if property == node || property.kind() == SyntaxKind::SpreadAssignment {
                continue;
            }
            let name = property.name();
            let text = match name.kind() {
                SyntaxKind::Identifier | SyntaxKind::NumericLiteral => name.text().to_owned(),
                SyntaxKind::StringLiteral => match string_value(ctx, name) {
                    Some(text) => text,
                    None => return,
                },
                _ => return,
            };
            names.insert(text);
        }
        let mut properties = Vec::new();
        for property in inner.properties().iter() {
            if property.kind() == SyntaxKind::SpreadAssignment {
                properties.push(ctx.text(file, property).to_owned());
                continue;
            }
            let Some(name) = data_property_name(ctx, property) else {
                // Accessors and methods would be copied as values.
                self.skip(ctx, file, "object-spread", node, Reason::Accessor);
                return;
            };
            if !names.insert(name) {
                self.skip(ctx, file, "object-spread", node, Reason::UnsupportedSyntax);
                return;
            }
            properties.push(ctx.text(file, property).to_owned());
        }
        self.emit(ctx, file, "object-spread", node, properties.join(", "));
    }

    /// `const [a, b] = [x, y]` to `const a = x, b = y`.
    fn destructuring(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let pattern = node.name();
        if pattern.kind() != SyntaxKind::ArrayBindingPattern {
            return;
        }
        let initializer = node.initializer();
        if initializer.is_nil()
            || initializer.kind() != SyntaxKind::ArrayLiteralExpression
            || node.type_().is_some()
        {
            return;
        }
        let list = node.parent();
        if list.kind() != SyntaxKind::VariableDeclarationList
            || list.parent().kind() != SyntaxKind::VariableStatement
            || list
                .flags()
                .intersects(NodeFlags::Using | NodeFlags::Ambient)
            || list.parent().flags().intersects(NodeFlags::Ambient)
        {
            return;
        }
        let array = match dense_array(initializer) {
            Ok(array) => array,
            Err(reason) => {
                self.skip(ctx, file, "destructuring", node, reason);
                return;
            }
        };
        let values: Vec<Node> = array.elements().iter().collect();
        let bindings: Vec<Node> = pattern.elements().iter().collect();
        if bindings.is_empty() || values.len() < bindings.len() {
            return;
        }
        if values
            .iter()
            .any(|&v| v.kind() == SyntaxKind::SpreadElement)
        {
            return;
        }
        for &binding in &bindings {
            if binding.kind() == SyntaxKind::OmittedExpression {
                continue;
            }
            if binding.kind() != SyntaxKind::BindingElement
                || binding.dot_dot_dot_token().is_some()
                || binding.initializer().is_some()
                || binding.property_name().is_some()
                || binding.name().kind() != SyntaxKind::Identifier
            {
                return;
            }
        }
        // Element-by-element binding interleaves evaluation and binding.
        // Elements without a binding disappear, so they must be pure. Later
        // elements then run after earlier bindings exist; that is
        // unobservable when every element is pure (a pure element cannot read
        // a binding in its temporal dead zone) or when every reference to the
        // new bindings runs only after the whole declaration completed.
        let mut all_pure = true;
        for (index, &value) in values.iter().enumerate() {
            let bound = bindings
                .get(index)
                .is_some_and(|b| b.kind() != SyntaxKind::OmittedExpression);
            if self.facts.pure(ctx, value) {
                continue;
            }
            all_pure = false;
            if !bound {
                self.skip(ctx, file, "destructuring", node, Reason::SideEffects);
                return;
            }
        }
        if !all_pure {
            for &binding in &bindings {
                if binding.kind() == SyntaxKind::OmittedExpression {
                    continue;
                }
                let symbol = ctx.resolve(binding.name());
                let early = symbol.is_nil()
                    || ctx.refs(symbol).iter().any(|r| {
                        r.kind != RefKind::Declaration && !initialized_before(node, r.node)
                    });
                if early {
                    self.skip(ctx, file, "destructuring", node, Reason::SideEffects);
                    return;
                }
            }
        }
        if self.array_patched(ctx, &[ITERATOR]) {
            self.skip(
                ctx,
                file,
                "destructuring",
                node,
                Reason::IntrinsicMayBeReplaced,
            );
            return;
        }
        let constant = list.flags().intersects(NodeFlags::Const);
        let mut parts = Vec::new();
        for (index, &binding) in bindings.iter().enumerate() {
            if binding.kind() == SyntaxKind::OmittedExpression {
                continue;
            }
            let value = values[index];
            let name = binding.name();
            let symbol = ctx.resolve(name);
            if symbol.is_nil() {
                return;
            }
            let declared = ctx.checker.get_type_of_symbol(symbol);
            let actual = ctx.checker.get_type_at_location(value);
            let literal = is_literal_node(value);
            let widened = if literal {
                ctx.checker.get_base_type_of_literal_type(actual)
            } else {
                actual
            };
            let value_text = ctx.text(file, value);
            if declared.is_some() && declared == actual {
                parts.push(format!("{} = {value_text}", name.text()));
            } else if declared.is_some() && declared == widened && !constant {
                // `let`/`var` widen the literal type the same way.
                parts.push(format!("{} = {value_text}", name.text()));
            } else if declared.is_some() && declared == widened && literal {
                // A `const` keeps the literal type; the destructured binding
                // had the widened primitive type. The assertion is erased.
                let flags = ctx.checker.ty(declared).flags();
                let keyword = if flags.intersects(TypeFlags::Number) {
                    "number"
                } else if flags.intersects(TypeFlags::String) {
                    "string"
                } else if flags.intersects(TypeFlags::Boolean) {
                    "boolean"
                } else {
                    self.skip(ctx, file, "destructuring", node, Reason::TypeConstraint);
                    return;
                };
                parts.push(format!("{} = {value_text} as {keyword}", name.text()));
            } else {
                self.skip(ctx, file, "destructuring", node, Reason::TypeConstraint);
                return;
            }
        }
        if parts.is_empty() {
            return;
        }
        self.emit(ctx, file, "destructuring", node, parts.join(", "));
    }
}

/// `value === lit` type-checks: some constituent of the value's checker type
/// overlaps the literal.
fn comparable(ctx: &mut Ctx, value: Node, lit: &Lit) -> bool {
    let t = ctx.checker.get_type_at_location(value);
    if t.is_nil() {
        return false;
    }
    type_constituents(ctx, t).into_iter().any(|c| {
        let ty = ctx.checker.ty(c);
        let flags = ty.flags();
        if flags.intersects(TypeFlags::Any | TypeFlags::Unknown) {
            return true;
        }
        let literal =
            if flags.intersects(TypeFlags::Literal) && !flags.intersects(TypeFlags::EnumLiteral) {
                ty.as_literal_type().value()
            } else {
                None
            };
        match lit {
            Lit::Str(s) => {
                flags.intersects(
                    TypeFlags::String | TypeFlags::TemplateLiteral | TypeFlags::StringMapping,
                ) || matches!(&literal, Some(LiteralValue::String(v)) if v == s)
            }
            Lit::Num(n) => {
                flags.intersects(TypeFlags::Number)
                    || matches!(&literal, Some(LiteralValue::Number(v)) if v.0 == *n)
            }
            Lit::Bool(b) => matches!(&literal, Some(LiteralValue::Boolean(v)) if v == b),
            Lit::Null => flags.intersects(TypeFlags::Null),
            Lit::Undefined => flags.intersects(TypeFlags::Undefined | TypeFlags::Void),
        }
    })
}

/// Whether rewriting `node` into equality tests on `value` would let the
/// checker narrow `value` at other references.
fn introduces_narrowing(ctx: &mut Ctx, node: Node, value: Node) -> bool {
    let (scope, after) = test_scope(node);
    if scope == node || !super::runtime_facts::in_test_context(node) {
        return false;
    }
    let symbol = ctx.resolve(value);
    if symbol.is_nil() {
        return true;
    }
    let end = if after {
        usize::MAX
    } else {
        scope.end() as usize
    };
    let start = scope.pos() as usize;
    let file = get_source_file_of_node(node);
    ctx.refs(symbol).iter().any(|r| {
        matches!(r.kind, RefKind::Read | RefKind::Call)
            && get_source_file_of_node(r.node) == file
            && (r.node.pos() as usize) >= start
            && (r.node.pos() as usize) < end
            && !contains(node, r.node)
    })
}
