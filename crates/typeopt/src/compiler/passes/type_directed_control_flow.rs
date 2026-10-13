// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `type-directed-control-flow`: removes control flow whose outcome is
//! proven by runtime facts.
//!
//! Supported forms: `if` statements, conditional expressions and `&&`/`||`/
//! `??` operands in test positions whose condition is constant (type tests
//! such as `typeof x === "string"`, null checks, literal discriminants,
//! truthiness), `switch` statements over a proven literal, optional chains
//! (`x?.y` to `x.y`) on proven non-nullish receivers, and statements made
//! unreachable by a preceding `return`/`throw`/`break`/`continue` in the same
//! statement list.
//!
//! Proof obligations:
//! - The condition's value comes from [`Facts`]: literals, operators,
//!   never-reassigned bindings, closed internal function parameters and
//!   dominating guards. Checker types never prove a condition.
//! - A dropped condition must be pure ([`Facts::pure`], which rejects reads in
//!   the temporal dead zone). An impure `if` condition is kept as a statement.
//! - Removed code must not declare hoisted `var`s or functions, must not remove
//!   assignments that the checker's assignment narrowing relies on, and the
//!   checker must not lose narrowing in the kept code ([`narrowing_neutral`]);
//!   otherwise the site is preserved with `type_constraint`.
//! - Value replacements outside test positions keep the checker type (identical
//!   type identity), and test positions never receive syntactically constant
//!   expressions that TypeScript rejects.
//! - `?.` is removed only when the receiver is proven not `undefined`/`null` at
//!   runtime and its checker type is non-nullable.
use anyhow::Result;

use super::{
    super::analysis::{Ctx, skip_outer, walk_up_outer},
    runtime_facts::{
        Facts, contains, declares_hoisted, deleted_statement, guard_glue, in_test_context,
        is_literal_node, is_statement_list, narrowing_neutral, never_completes, precedence,
        removes_narrowing_writes, replace_expr, same_type, span, syntactically_uncertain,
        terminated, test_scope, type_assertable, type_non_nullable,
    },
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::TypeDirectedControlFlow;

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
                SyntaxKind::IfStatement => planner.if_statement(ctx, file, node),
                SyntaxKind::ConditionalExpression => planner.conditional(ctx, file, node),
                SyntaxKind::BinaryExpression => planner.logical(ctx, file, node),
                SyntaxKind::PropertyAccessExpression
                | SyntaxKind::ElementAccessExpression
                | SyntaxKind::CallExpression => planner.optional_chain(ctx, file, node),
                SyntaxKind::SwitchStatement => planner.switch(ctx, file, node),
                _ => {}
            }
            if is_statement_list(node) {
                planner.unreachable(ctx, file, node);
            }
        }
    }
    tracing::debug!(
        pass = %PASS,
        candidates = planner.candidates,
        "control-flow candidates planned"
    );
    Ok(())
}

struct Planner<'a> {
    facts: &'a mut Facts,
    out: &'a mut Plan,
    candidates: usize,
}

/// Stable identity of a site: its kind and current span in the module.
fn key(ctx: &Ctx, file: usize, tag: &str, node: Node) -> String {
    let (start, end) = span(node);
    format!("{tag}:{}:{start}-{end}", ctx.files[file].name)
}

impl Planner<'_> {
    fn emit(&mut self, ctx: &Ctx, file: usize, tag: &str, node: Node, edits: Vec<FileEdit>) {
        let mut candidate =
            Candidate::new(PASS, key(ctx, file, tag, node), ctx.location(file, node));
        candidate.edits = edits;
        self.out.candidates.push(candidate);
        self.candidates += 1;
    }

    fn skip(&mut self, ctx: &Ctx, file: usize, tag: &str, node: Node, reason: Reason) {
        self.out
            .skips
            .push(ctx.skip(PASS, key(ctx, file, tag, node), file, node, reason));
    }

    fn replace(ctx: &Ctx, file: usize, node: Node, text: impl Into<String>) -> FileEdit {
        let (start, end) = span(node);
        let text = guard_glue(&ctx.files[file].text, start, end, text.into());
        FileEdit::replace(&ctx.files[file].name, start, end, text)
    }

    /// `if (c) A else B` with a constant `c`.
    fn if_statement(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let condition = node.expression();
        let Some(truthy) = self.facts.expr(ctx, condition).truthy() else {
            return;
        };
        let (kept, removed) = if truthy {
            (node.then_statement(), node.else_statement())
        } else {
            (node.else_statement(), node.then_statement())
        };
        if let Some(reason) = self.removal_obstacle(ctx, file, condition, node, removed, true) {
            self.skip(ctx, file, "if", node, reason);
            return;
        }
        let pure = self.facts.pure(ctx, condition);
        let mut text = String::new();
        if !pure {
            text.push_str(&expression_statement(ctx, file, condition));
        }
        let in_list = is_statement_list(node.parent());
        if kept.is_some() {
            let kept_text = if in_list && unwrappable(kept) {
                // The block's braces only delimit a scope it does not use.
                kept.statements()
                    .iter()
                    .map(|s| statement_text(ctx, file, s))
                    .collect::<Vec<_>>()
                    .join("\n")
            } else {
                statement_text(ctx, file, kept)
            };
            if !in_list && kept.kind() == SyntaxKind::IfStatement {
                // Keep a following `else` from attaching to the kept `if`.
                text.push_str(&format!("{{{kept_text}}}"));
            } else {
                if !text.is_empty() && !text.ends_with(' ') {
                    text.push(' ');
                }
                text.push_str(&kept_text);
            }
        }
        let replacement = if text.is_empty() {
            if in_list {
                deleted_statement(ctx, file, node).to_owned()
            } else {
                ";".to_owned()
            }
        } else if !in_list && !pure {
            format!("{{{text}}}")
        } else if in_list {
            guard_asi(ctx, file, node, text)
        } else {
            text
        };
        let edit = Self::replace(ctx, file, node, replacement);
        self.emit(ctx, file, "if", node, vec![edit]);
    }

    /// Shared obligations for dropping `removed` because `test` is constant
    /// within `construct`.
    fn removal_obstacle(
        &mut self,
        ctx: &mut Ctx,
        file: usize,
        test: Node,
        construct: Node,
        removed: Node,
        statement: bool,
    ) -> Option<Reason> {
        if removed.is_some() {
            if declares_hoisted(removed) {
                return Some(Reason::UnsupportedSyntax);
            }
            if removes_narrowing_writes(ctx, file, removed) {
                return Some(Reason::TypeConstraint);
            }
        }
        let after = statement && removed.is_some() && has_exit(removed);
        let removed: Vec<Node> = removed.is_some().then_some(removed).into_iter().collect();
        if !narrowing_neutral(ctx, test, construct, after, &removed) {
            return Some(Reason::TypeConstraint);
        }
        None
    }

    /// `c ? a : b` with a constant `c`.
    fn conditional(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let condition = node.condition();
        let Some(truthy) = self.facts.expr(ctx, condition).truthy() else {
            return;
        };
        let (kept, removed) = if truthy {
            (node.when_true(), node.when_false())
        } else {
            (node.when_false(), node.when_true())
        };
        if !self.facts.pure(ctx, condition) {
            self.skip(ctx, file, "conditional", node, Reason::SideEffects);
            return;
        }
        if let Some(reason) = self.value_obstacle(ctx, node, kept) {
            self.skip(ctx, file, "conditional", node, reason);
            return;
        }
        let (scope, after) = test_scope(node);
        let construct = if scope == node { node } else { scope };
        if let Some(reason) = self.removal_obstacle(ctx, file, condition, construct, removed, after)
        {
            self.skip(ctx, file, "conditional", node, reason);
            return;
        }
        let text = replace_expr(node, ctx.text(file, kept), precedence(kept));
        let edit = Self::replace(ctx, file, node, text);
        self.emit(ctx, file, "conditional", node, vec![edit]);
    }

    /// Whether `kept` may replace the value of `node` without changing the
    /// checker's view of the program.
    fn value_obstacle(&mut self, ctx: &mut Ctx, node: Node, kept: Node) -> Option<Reason> {
        let parent = walk_up_outer(node).parent();
        if parent.kind() == SyntaxKind::BinaryExpression
            && parent.operator_token().kind() == SyntaxKind::QuestionQuestionToken
        {
            return Some(Reason::TypeConstraint);
        }
        if in_test_context(node) {
            return (!syntactically_uncertain(kept)).then_some(Reason::TypeConstraint);
        }
        (!same_type(ctx, node, kept)).then_some(Reason::TypeConstraint)
    }

    /// `a && b`, `a || b` and `a ?? b` with a constant left operand.
    fn logical(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let operator = node.operator_token().kind();
        let left = node.left();
        let right = node.right();
        let fact = match operator {
            SyntaxKind::AmpersandAmpersandToken | SyntaxKind::BarBarToken => {
                self.facts.expr(ctx, left).truthy()
            }
            SyntaxKind::QuestionQuestionToken => self.facts.expr(ctx, left).nullish(),
            _ => return,
        };
        let Some(value) = fact else {
            self.neutral_right(ctx, file, node);
            return;
        };
        // Whether the result is the left operand (the right one is never
        // evaluated) or the right operand (the left one is dropped).
        let keep_left = match operator {
            SyntaxKind::AmpersandAmpersandToken => !value,
            SyntaxKind::BarBarToken => value,
            _ => !value,
        };
        let (kept, removed) = if keep_left {
            (left, right)
        } else {
            (right, left)
        };
        if !keep_left && !self.facts.pure(ctx, left) {
            self.skip(ctx, file, "logical", node, Reason::SideEffects);
            return;
        }
        if let Some(reason) = self.value_obstacle(ctx, node, kept) {
            self.skip(ctx, file, "logical", node, reason);
            return;
        }
        let (scope, after) = test_scope(node);
        if let Some(reason) = self.removal_obstacle(ctx, file, left, scope, removed, after) {
            self.skip(ctx, file, "logical", node, reason);
            return;
        }
        let text = replace_expr(node, ctx.text(file, kept), precedence(kept));
        let edit = Self::replace(ctx, file, node, text);
        self.emit(ctx, file, "logical", node, vec![edit]);
    }

    /// `a || false` and `a && true` in test positions: only `ToBoolean(a)`
    /// is observed, which the constant right operand does not change.
    fn neutral_right(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let operator = node.operator_token().kind();
        let (left, right) = (node.left(), node.right());
        let neutral = match operator {
            SyntaxKind::AmpersandAmpersandToken => Some(true),
            SyntaxKind::BarBarToken => Some(false),
            _ => None,
        };
        if neutral.is_none() || !in_test_context(node) {
            return;
        }
        if self.facts.expr(ctx, right).truthy() != neutral {
            return;
        }
        if !self.facts.pure(ctx, right) {
            self.skip(ctx, file, "logical", node, Reason::SideEffects);
            return;
        }
        if !syntactically_uncertain(left) {
            self.skip(ctx, file, "logical", node, Reason::TypeConstraint);
            return;
        }
        let (scope, after) = test_scope(node);
        if let Some(reason) = self.removal_obstacle(ctx, file, right, scope, right, after) {
            self.skip(ctx, file, "logical", node, reason);
            return;
        }
        let text = replace_expr(node, ctx.text(file, left), precedence(left));
        let edit = Self::replace(ctx, file, node, text);
        self.emit(ctx, file, "logical", node, vec![edit]);
    }

    /// `x?.y`, `x?.[k]` and `x?.()` with a proven non-nullish `x`.
    fn optional_chain(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let token = node.question_dot_token();
        if token.is_nil() {
            return;
        }
        let receiver = node.expression();
        let fact = self.facts.expr(ctx, receiver);
        let typed = type_non_nullable(ctx, receiver);
        match fact.nullish() {
            Some(false) => {}
            _ => {
                if typed {
                    // The checker says non-nullable, but nothing proves it at
                    // runtime.
                    let reason = unproven_reason(ctx, receiver);
                    self.skip(ctx, file, "optional", node, reason);
                }
                return;
            }
        }
        // The runtime proof is stated with a non-null assertion, which emits
        // nothing. It stays valid even when the receiver's checker type is
        // non-nullable only through narrowing that another edit may remove.
        let assertion = if crate::app::bundle_mode() {
            // JavaScript chunks have no recheck to satisfy.
            ""
        } else if type_assertable(ctx, receiver) {
            "!"
        } else if typed {
            ""
        } else {
            self.skip(ctx, file, "optional", node, Reason::TypeConstraint);
            return;
        };
        // As an operand of a condition or logical operator, `x?.y` narrows `x`
        // for the guarded code; `x!.y` does not.
        let (scope, after) = test_scope(node);
        if scope != walk_up_outer(node) && !narrowing_neutral(ctx, node, scope, after, &[]) {
            self.skip(ctx, file, "optional", node, Reason::TypeConstraint);
            return;
        }
        let (start, end) = span(token);
        let replacement = if node.kind() == SyntaxKind::PropertyAccessExpression {
            format!("{assertion}.")
        } else {
            assertion.to_owned()
        };
        let edit = FileEdit::replace(&ctx.files[file].name, start, end, replacement);
        self.emit(ctx, file, "optional", node, vec![edit]);
    }

    /// `switch (d)` over a proven literal with literal case labels.
    fn switch(&mut self, ctx: &mut Ctx, file: usize, node: Node) {
        let discriminant = node.expression();
        let Some(value) = self.facts.expr(ctx, discriminant).lit else {
            return;
        };
        let clauses: Vec<Node> = node.case_block().clauses().nodes().iter().collect();
        let mut selected = None;
        let mut default = None;
        for (index, &clause) in clauses.iter().enumerate() {
            if clause.kind() == SyntaxKind::DefaultClause {
                default = Some(index);
                continue;
            }
            let label = clause.expression();
            if !is_literal_node(label) {
                return;
            }
            let Some(label_value) = self.facts.expr(ctx, label).lit else {
                return;
            };
            if selected.is_none() && label_value.strict_equals(&value) {
                selected = Some(index);
            }
        }
        if !self.facts.pure(ctx, discriminant) {
            self.skip(ctx, file, "switch", node, Reason::SideEffects);
            return;
        }
        let start = selected.or(default);
        // Statements that run: from the selected clause through fallthrough
        // until a clause that never completes.
        let mut kept_statements = Vec::new();
        let mut kept_clauses = Vec::new();
        if let Some(start) = start {
            for &clause in &clauses[start..] {
                kept_clauses.push(clause);
                let statements: Vec<Node> = clause.statements().iter().collect();
                let done = statements.last().is_some_and(|&s| never_completes(s));
                kept_statements.extend(statements);
                if done {
                    break;
                }
            }
        }
        // Drop the final `break` that leaves the switch.
        if kept_statements
            .last()
            .is_some_and(|s| s.kind() == SyntaxKind::BreakStatement && s.label().is_nil())
        {
            kept_statements.pop();
        }
        if kept_statements.iter().any(|&s| breaks_out(s)) {
            self.skip(ctx, file, "switch", node, Reason::UnsupportedSyntax);
            return;
        }
        for &clause in &clauses {
            if kept_clauses.contains(&clause) {
                continue;
            }
            let lexical = clause.statements().iter().any(|s| {
                matches!(
                    s.kind(),
                    SyntaxKind::VariableStatement
                        | SyntaxKind::FunctionDeclaration
                        | SyntaxKind::ClassDeclaration
                )
            });
            if lexical || declares_hoisted(clause) || removes_narrowing_writes(ctx, file, clause) {
                self.skip(ctx, file, "switch", node, Reason::UnsupportedSyntax);
                return;
            }
        }
        let removed: Vec<Node> = clauses
            .iter()
            .copied()
            .filter(|c| !kept_clauses.contains(c))
            .collect();
        let after = removed.iter().any(|&c| has_exit(c));
        if !narrowing_neutral(ctx, discriminant, node, after, &removed) {
            self.skip(ctx, file, "switch", node, Reason::TypeConstraint);
            return;
        }
        let body: Vec<String> = kept_statements
            .iter()
            .map(|&s| statement_text(ctx, file, s))
            .collect();
        let replacement = if body.is_empty() {
            if is_statement_list(node.parent()) {
                deleted_statement(ctx, file, node).to_owned()
            } else {
                ";".to_owned()
            }
        } else if is_statement_list(node.parent()) && kept_statements.iter().all(|&s| joinable(s)) {
            guard_asi(ctx, file, node, body.join("\n"))
        } else {
            format!("{{{}}}", body.join("\n"))
        };
        let edit = Self::replace(ctx, file, node, replacement);
        self.emit(ctx, file, "switch", node, vec![edit]);
    }

    /// Statements after an unconditional jump in the same statement list.
    fn unreachable(&mut self, ctx: &mut Ctx, file: usize, list: Node) {
        let statements: Vec<Node> = list.statements().iter().collect();
        let Some(jump) = statements.iter().position(|&s| never_completes(s)) else {
            return;
        };
        let mut edits = Vec::new();
        let mut run: Option<(Node, Node)> = None;
        let flush = |run: &mut Option<(Node, Node)>, edits: &mut Vec<FileEdit>| {
            if let Some((first, last)) = run.take() {
                let (start, _) = span(first);
                let end = last.end() as usize;
                edits.push(FileEdit::replace(
                    &ctx.files[file].name,
                    start,
                    end,
                    deleted_statement(ctx, file, first),
                ));
            }
        };
        for &statement in &statements[jump + 1..] {
            if removable(statement) {
                run = Some(match run {
                    Some((first, _)) => (first, statement),
                    None => (statement, statement),
                });
            } else {
                flush(&mut run, &mut edits);
            }
        }
        flush(&mut run, &mut edits);
        if edits.is_empty() {
            return;
        }
        self.emit(ctx, file, "unreachable", list, edits);
    }
}

/// A block whose statements can join the enclosing statement list: it
/// declares nothing block-scoped.
fn unwrappable(block: Node) -> bool {
    block.kind() == SyntaxKind::Block && block.statements().iter().all(joinable)
}

/// A statement that declares nothing block-scoped, so it can move to the
/// enclosing statement list.
fn joinable(statement: Node) -> bool {
    match statement.kind() {
        SyntaxKind::VariableStatement => !statement
            .declaration_list()
            .flags()
            .intersects(NodeFlags::BlockScoped),
        SyntaxKind::FunctionDeclaration
        | SyntaxKind::ClassDeclaration
        | SyntaxKind::EnumDeclaration
        | SyntaxKind::ModuleDeclaration
        | SyntaxKind::InterfaceDeclaration
        | SyntaxKind::TypeAliasDeclaration => false,
        _ => true,
    }
}

/// Unreachable statements that can go without changing scoping.
fn removable(statement: Node) -> bool {
    !matches!(
        statement.kind(),
        SyntaxKind::VariableStatement
            | SyntaxKind::FunctionDeclaration
            | SyntaxKind::ClassDeclaration
            | SyntaxKind::EnumDeclaration
            | SyntaxKind::ModuleDeclaration
            | SyntaxKind::ImportDeclaration
            | SyntaxKind::ImportEqualsDeclaration
            | SyntaxKind::ExportDeclaration
            | SyntaxKind::ExportAssignment
            | SyntaxKind::InterfaceDeclaration
            | SyntaxKind::TypeAliasDeclaration
    ) && !declares_hoisted(statement)
}

/// The subtree contains a jump that may end the enclosing flow.
fn has_exit(node: Node) -> bool {
    let mut found = false;
    let mut stack = vec![node];
    while let Some(current) = stack.pop() {
        match current.kind() {
            SyntaxKind::ReturnStatement
            | SyntaxKind::ThrowStatement
            | SyntaxKind::BreakStatement
            | SyntaxKind::ContinueStatement => found = true,
            SyntaxKind::FunctionDeclaration
            | SyntaxKind::FunctionExpression
            | SyntaxKind::ArrowFunction
            | SyntaxKind::MethodDeclaration
            | SyntaxKind::ClassDeclaration
            | SyntaxKind::ClassExpression => continue,
            _ => {}
        }
        if found {
            return true;
        }
        current.for_each_child(|child| {
            stack.push(child);
            false
        });
    }
    false
}

/// Whether `statement` contains an unlabeled `break` that leaves the
/// enclosing `switch` (outside nested loops, switches and functions).
fn breaks_out(statement: Node) -> bool {
    let mut found = false;
    let mut stack = vec![statement];
    while let Some(current) = stack.pop() {
        match current.kind() {
            SyntaxKind::BreakStatement if current.label().is_nil() => found = true,
            SyntaxKind::ForStatement
            | SyntaxKind::ForInStatement
            | SyntaxKind::ForOfStatement
            | SyntaxKind::WhileStatement
            | SyntaxKind::DoStatement
            | SyntaxKind::SwitchStatement
            | SyntaxKind::FunctionDeclaration
            | SyntaxKind::FunctionExpression
            | SyntaxKind::ArrowFunction
            | SyntaxKind::MethodDeclaration
            | SyntaxKind::ClassDeclaration
            | SyntaxKind::ClassExpression => continue,
            _ => {}
        }
        if found {
            return true;
        }
        current.for_each_child(|child| {
            stack.push(child);
            false
        });
    }
    false
}

/// Statement text that ends with an explicit statement boundary.
fn statement_text(ctx: &Ctx, file: usize, statement: Node) -> String {
    let text = ctx.text(file, statement);
    match statement.kind() {
        SyntaxKind::ExpressionStatement
        | SyntaxKind::VariableStatement
        | SyntaxKind::ReturnStatement
        | SyntaxKind::ThrowStatement
        | SyntaxKind::BreakStatement
        | SyntaxKind::ContinueStatement
        | SyntaxKind::DoStatement
        | SyntaxKind::DebuggerStatement => {
            if text.trim_end().ends_with(';') {
                text.to_owned()
            } else {
                format!("{};", text.trim_end())
            }
        }
        _ => terminated(text),
    }
}

/// `expression;` as a statement that cannot be misparsed.
fn expression_statement(ctx: &Ctx, file: usize, mut expression: Node) -> String {
    while expression.kind() == SyntaxKind::ParenthesizedExpression {
        expression = expression.expression();
    }
    let text = ctx.text(file, expression);
    let hazard = ["{", "function", "class", "let", "async"]
        .iter()
        .any(|p| text.starts_with(p));
    if hazard {
        format!("({text});")
    } else {
        format!("{text};")
    }
}

/// Prefixes `;` when a statement replacement starts with a token that would
/// continue the previous statement through automatic semicolon insertion.
fn guard_asi(ctx: &Ctx, file: usize, statement: Node, text: String) -> String {
    let risky = text.starts_with(['(', '[', '`', '+', '-', '/']);
    if risky && !deleted_statement(ctx, file, statement).is_empty() {
        format!(";{text}")
    } else {
        text
    }
}

/// Why a nominated receiver lacks a runtime proof.
fn unproven_reason(ctx: &mut Ctx, receiver: Node) -> Reason {
    let receiver = skip_outer(receiver);
    if receiver.kind() != SyntaxKind::Identifier {
        return Reason::UnprovenRuntimeFact;
    }
    let symbol = ctx.resolve(receiver);
    if symbol.is_nil() {
        return Reason::ExternalBoundary;
    }
    let declaration = ctx.checker.sym(symbol).value_declaration;
    if declaration.is_nil() || ctx.file_of_node(declaration).is_none() {
        return Reason::ExternalBoundary;
    }
    if declaration.kind() == SyntaxKind::Parameter {
        let function = declaration.parent();
        let name = match function.kind() {
            SyntaxKind::FunctionDeclaration => function.name(),
            _ if function.parent().kind() == SyntaxKind::VariableDeclaration => {
                function.parent().name()
            }
            _ => Node::NIL,
        };
        if name.is_some() && name.kind() == SyntaxKind::Identifier {
            let callee = ctx.resolve(name);
            if ctx.public.contains(&callee) {
                return Reason::PublicExport;
            }
            if !callee.is_nil() && !ctx.is_internal(callee) {
                return Reason::ExternalBoundary;
            }
            return Reason::Escapes;
        }
    }
    let _ = contains;
    Reason::UnprovenRuntimeFact
}
