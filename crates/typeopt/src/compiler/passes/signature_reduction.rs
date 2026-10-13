// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `signature-reduction`: removes unused parameters and their arguments,
//! propagates a common constant argument into the body and drops return
//! values that no caller uses, within closed call graphs.
//!
//! Proof obligations (see `signature_support` for the closure proof):
//! - The function binding is internal (not a public entry export, not
//!   referenced from JavaScript, not ambient or merged), every runtime
//!   reference is the callee of a direct call without spread arguments or a
//!   module alias, and its module is not observable as a namespace object.
//!   Therefore `.length`, `.name`, identity and unknown callers cannot observe
//!   the signature.
//! - The body never mentions `arguments` and has no `this` parameter.
//! - A removed parameter is a plain identifier without references (including
//!   type queries), whose default, if any, is pure, and whose type does not
//!   mention the function's own type parameters (generic inference stays).
//!   Every removed argument still evaluates once in order: pure arguments are
//!   deleted, effectful ones become comma operands of the next kept argument,
//!   array elements before an own index read, or operands before a hoisted
//!   callee.
//! - A propagated constant parameter has no default, is never written, only
//!   read inside the body, and every non-recursive call passes the same
//!   primitive literal while recursive calls pass that literal or the parameter
//!   itself. Uses become `(literal as T)` so types stay unchanged.
//! - Return values are dropped only when every call discards its result
//!   (expression statement, `void`, left comma operand, or a recursive `return
//!   f(...)` of the function itself), the function is neither async nor a
//!   generator and has no type-predicate return type. Pure returned expressions
//!   disappear; an effectful one stays as an expression statement when it is
//!   the final statement; otherwise the function is preserved.
use anyhow::Result;

use super::{
    super::analysis::{Ctx, RefKind, skip_outer, walk_up_outer},
    signature_support::{
        CallSite, Function, Literal, Observable, closure, delete_items, enclosing_function,
        finalize, functions, is_value_read, mentions_type_parameter, rewrite_arguments, within,
    },
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::SignatureReduction;

/// A parameter-removal variant: key kind, removed parameters and the literal
/// text replacing each propagated constant parameter.
type Variant = (&'static str, Vec<bool>, Vec<Option<String>>);

pub(in crate::compiler) fn plan(ctx: &mut Ctx, _enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let observable = Observable::compute(ctx);
    let functions = functions(ctx);
    let (mut parameters, mut results) = (0usize, 0usize);
    let mut open: std::collections::BTreeMap<Reason, usize> = std::collections::BTreeMap::new();
    let mut closed = 0usize;
    for function in &functions {
        crate::app::check_cancelled()?;
        let params = function.parameters();
        let unused = unused_parameters(ctx, function, &params);
        let calls = match closure(ctx, function, &observable) {
            Ok(calls) => {
                closed += 1;
                calls
            }
            Err(reason) => {
                *open.entry(reason).or_default() += 1;
                // Report only directly called functions: a callback with an
                // unused parameter is not a signature-reduction opportunity.
                let called = ctx
                    .refs(function.symbol)
                    .iter()
                    .any(|r| r.kind == RefKind::Call);
                if called && unused.iter().any(|&u| u) {
                    out.skips.push(ctx.skip(
                        PASS,
                        key("params", function),
                        function.file,
                        function.name,
                        reason,
                    ));
                }
                continue;
            }
        };
        if calls.is_empty() {
            // Uncalled code is residual-dependency-removal's concern.
            continue;
        }
        let rest = params
            .last()
            .is_some_and(|p| p.dot_dot_dot_token().is_some());
        if !rest
            && calls
                .iter()
                .any(|c| c.call.arguments().len() > params.len())
        {
            if unused.iter().any(|&u| u) {
                out.skips.push(ctx.skip(
                    PASS,
                    key("params", function),
                    function.file,
                    function.name,
                    Reason::ObservableArity,
                ));
            }
            continue;
        }
        let constants = constant_parameters(ctx, function, &params, &calls, &unused);
        let has_unused = unused.iter().any(|&u| u);
        let has_constants = constants.iter().any(Option::is_some);
        let mut variants: Vec<Variant> = Vec::new();
        if has_unused || has_constants {
            let all: Vec<bool> = (0..params.len())
                .map(|i| unused[i] || constants[i].is_some())
                .collect();
            variants.push(("params-all", all, constants.clone()));
            if has_unused && has_constants {
                variants.push(("params-unused", unused.clone(), vec![None; params.len()]));
                let only: Vec<bool> = constants.iter().map(Option::is_some).collect();
                variants.push(("params-constant", only, constants.clone()));
            }
        }
        for (kind, remove, constants) in variants {
            match parameter_candidate(ctx, function, &params, &calls, &remove, &constants, kind) {
                Ok(candidate) => {
                    parameters += 1;
                    out.candidates.push(candidate);
                }
                Err(reason) => out.skips.push(ctx.skip(
                    PASS,
                    key(kind, function),
                    function.file,
                    function.name,
                    reason,
                )),
            }
        }
        if let Some(candidate) = result_candidate(ctx, function, &calls) {
            results += 1;
            out.candidates.push(candidate);
        }
    }
    tracing::debug!(
        pass = %PASS,
        functions = functions.len(),
        closed,
        open = ?open,
        parameter_candidates = parameters,
        result_candidates = results,
        "signature reduction planned"
    );
    Ok(())
}

fn key(kind: &str, function: &Function) -> String {
    format!("{PASS}:{kind}:{}", function.ident)
}

/// Parameters that nothing reads and whose removal is unobservable.
fn unused_parameters(ctx: &mut Ctx, function: &Function, params: &[Node]) -> Vec<bool> {
    params
        .iter()
        .map(|&p| {
            let name = p.name();
            if name.kind() != SyntaxKind::Identifier
                || name.text() == "this"
                || !p.decorators().is_empty()
                || mentions_type_parameter(function.node, p)
            {
                return false;
            }
            let init = p.initializer();
            if init.is_some() && !ctx.is_pure(init) {
                return false;
            }
            let symbol = ctx.resolve(name);
            !symbol.is_nil()
                && ctx
                    .refs(symbol)
                    .iter()
                    .all(|r| r.kind == RefKind::Declaration && r.node == name)
        })
        .collect()
}

/// For each parameter, the literal text every call passes, when the
/// parameter can be replaced by it.
fn constant_parameters(
    ctx: &mut Ctx,
    function: &Function,
    params: &[Node],
    calls: &[CallSite],
    unused: &[bool],
) -> Vec<Option<String>> {
    let list = params_span(params);
    params
        .iter()
        .enumerate()
        .map(|(index, &p)| {
            let name = p.name();
            if unused[index]
                || name.kind() != SyntaxKind::Identifier
                || p.initializer().is_some()
                || p.question_token().is_some()
                || p.dot_dot_dot_token().is_some()
                || p.type_().is_nil()
                || mentions_type_parameter(function.node, p)
            {
                return None;
            }
            let symbol = ctx.resolve(name);
            if symbol.is_nil() {
                return None;
            }
            let refs = ctx.refs(symbol).to_vec();
            if refs.iter().any(|r| match r.kind {
                _ if is_value_read(r) => list.is_some_and(|(s, e)| {
                    // A read inside the parameter list may observe the
                    // parameter in its temporal dead zone.
                    let at = Ctx::start(r.node);
                    at >= s && at < e
                }),
                RefKind::Declaration => r.node != name,
                _ => true,
            }) {
                return None;
            }
            common_literal(ctx, function, calls, index, symbol).map(|(text, _)| text)
        })
        .collect()
}

/// The literal passed at `index` by every call, with recursive calls
/// allowed to forward the parameter itself.
pub(super) fn common_literal(
    ctx: &mut Ctx,
    function: &Function,
    calls: &[CallSite],
    index: usize,
    symbol: SymbolId,
) -> Option<(String, Literal)> {
    let mut found: Option<(String, Literal)> = None;
    for site in calls {
        let args = site.call.arguments();
        if index >= args.len() {
            return None;
        }
        let arg = args.get(index);
        let recursive = site.file == function.file && within(site.call, function.node);
        if recursive
            && skip_outer(arg).kind() == SyntaxKind::Identifier
            && ctx.resolve(skip_outer(arg)) == symbol
        {
            continue;
        }
        let literal = Literal::of(arg)?;
        match &found {
            Some((_, existing)) if existing.key() != literal.key() => return None,
            Some(_) => {}
            None => found = Some((ctx.text(site.file, skip_outer(arg)).to_owned(), literal)),
        }
    }
    found
}

fn params_span(params: &[Node]) -> Option<(usize, usize)> {
    Some((Ctx::start(*params.first()?), params.last()?.end() as usize))
}

/// Argument removal flags for a call with `count` arguments.
fn argument_flags(params: &[Node], remove: &[bool], count: usize) -> Vec<bool> {
    let rest = params
        .last()
        .is_some_and(|p| p.dot_dot_dot_token().is_some());
    (0..count)
        .map(|j| {
            let index = if rest { j.min(params.len() - 1) } else { j };
            remove.get(index).copied().unwrap_or(false)
        })
        .collect()
}

fn parameter_candidate(
    ctx: &mut Ctx,
    function: &Function,
    params: &[Node],
    calls: &[CallSite],
    remove: &[bool],
    constants: &[Option<String>],
    kind: &str,
) -> Result<Candidate, Reason> {
    let file_name = ctx.files[function.file].name.clone();
    let items: Vec<(usize, usize)> = params
        .iter()
        .map(|&p| (Ctx::start(p), p.end() as usize))
        .collect();
    let mut edits = delete_items(&file_name, &ctx.files[function.file].text, &items, remove)
        .ok_or(Reason::UnsupportedSyntax)?;
    for &site in calls {
        let flags = argument_flags(params, remove, site.call.arguments().len());
        let call_edits = rewrite_arguments(ctx, site, &flags, function.is_declaration())
            .ok_or(Reason::SideEffects)?;
        edits.extend(call_edits);
    }
    for (index, constant) in constants.iter().enumerate() {
        let Some(literal) = constant else { continue };
        let p = params[index];
        let ty = ctx.text(function.file, p.type_()).to_owned();
        let replacement = format!("({literal} as {ty})");
        let symbol = ctx.resolve(p.name());
        for r in ctx.refs(symbol).to_vec() {
            if !is_value_read(&r) {
                continue;
            }
            let parent = r.node.parent();
            let text = if parent.kind() == SyntaxKind::ShorthandPropertyAssignment
                && parent.name() == r.node
            {
                if parent.object_assignment_initializer().is_some() {
                    return Err(Reason::UnsupportedSyntax);
                }
                format!("{}: {replacement}", r.node.text())
            } else {
                replacement.clone()
            };
            edits.push(FileEdit::replace(
                &ctx.files[r.file].name,
                Ctx::start(r.node),
                r.node.end() as usize,
                text,
            ));
        }
    }
    let edits = finalize(edits).ok_or(Reason::UnsupportedSyntax)?;
    let mut candidate = Candidate::new(
        PASS,
        key(kind, function),
        ctx.location(function.file, function.name),
    );
    candidate.edits = edits;
    Ok(candidate)
}

/// Whether the value of `call` is discarded. Recursive calls returned by the
/// function itself forward a value that is discarded in turn.
fn result_unused(function: &Function, site: &CallSite) -> bool {
    let outer = walk_up_outer(site.call);
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::ExpressionStatement | SyntaxKind::VoidExpression => true,
        SyntaxKind::BinaryExpression => {
            parent.operator_token().kind() == SyntaxKind::CommaToken && parent.left() == outer
        }
        SyntaxKind::ReturnStatement => {
            site.file == function.file && enclosing_function(parent) == function.node
        }
        _ => false,
    }
}

fn result_candidate(ctx: &mut Ctx, function: &Function, calls: &[CallSite]) -> Option<Candidate> {
    let node = function.node;
    let flags = get_function_flags(node);
    if flags.intersects(FunctionFlags::Async | FunctionFlags::Generator) {
        return None;
    }
    let annotation = node.type_();
    if annotation.is_some()
        && matches!(
            annotation.kind(),
            SyntaxKind::TypePredicate | SyntaxKind::VoidKeyword
        )
    {
        return None;
    }
    if !calls.iter().all(|c| result_unused(function, c)) {
        return None;
    }
    let file = function.file;
    let file_name = ctx.files[file].name.clone();
    let body = node.body();
    let mut edits = Vec::new();
    if body.kind() != SyntaxKind::Block {
        // Expression-bodied arrow: only a pure body is worth dropping.
        if !ctx.is_pure(body) {
            return None;
        }
        edits.push(FileEdit::replace(
            &file_name,
            Ctx::start(body),
            body.end() as usize,
            "{}",
        ));
    } else {
        let mut nodes = Vec::new();
        crate::compiler::collect(body, &mut nodes);
        let last = body.statements().last();
        for statement in nodes {
            if statement.kind() != SyntaxKind::ReturnStatement
                || enclosing_function(statement) != node
            {
                continue;
            }
            let value = statement.expression();
            if value.is_nil() {
                continue;
            }
            let inner = skip_outer(value);
            if inner.kind() == SyntaxKind::CallExpression && calls.iter().any(|c| c.call == inner) {
                // A forwarded recursive result is discarded by every caller.
                continue;
            }
            let start = Ctx::start(statement);
            let end = statement.end() as usize;
            let final_statement = statement.parent() == body && last == Some(statement);
            if ctx.is_pure(value) {
                let replacement = if final_statement { "" } else { "return;" };
                edits.push(FileEdit::replace(&file_name, start, end, replacement));
            } else if final_statement {
                let text = ctx.text(file, value).to_owned();
                let ambiguous = text.starts_with('{')
                    || text.starts_with("function")
                    || text.starts_with("class")
                    || text.starts_with("let")
                    || text.starts_with("async");
                let replacement = if ambiguous {
                    format!("({text});")
                } else {
                    format!("{text};")
                };
                edits.push(FileEdit::replace(&file_name, start, end, replacement));
            } else {
                // `return value;` in the middle is already the shortest form
                // that evaluates the value and leaves.
                return None;
            }
        }
    }
    if edits.is_empty() {
        return None;
    }
    if annotation.is_some() {
        edits.push(FileEdit::replace(
            &file_name,
            Ctx::start(annotation),
            annotation.end() as usize,
            "void",
        ));
    }
    let edits = finalize(edits)?;
    let mut candidate = Candidate::new(
        PASS,
        key("result", function),
        ctx.location(file, function.name),
    );
    candidate.edits = edits;
    Some(candidate)
}
