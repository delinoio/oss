// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `function-specialization`: clones a closed function per literal value of
//! a union-typed parameter, folds the branches that test that parameter in
//! each clone and redirects every call to the matching clone.
//!
//! Proof obligations:
//! - The type checker only nominates: the parameter's declared type is a union
//!   of string, number or boolean literals (including `boolean`).
//! - The function is closed (see `signature_support`): internal, every runtime
//!   reference is a direct call or a module alias, not observable through a
//!   namespace object, no `arguments`, no `this` parameter. Clone identity and
//!   `.name` are therefore unobservable.
//! - Runtime evidence: every call passes a primitive literal at that position
//!   (recursive calls may forward the parameter itself), so each call's value
//!   is known. The parameter is a plain identifier without a default, never
//!   written and only read inside the body. At least two distinct values occur;
//!   one value keeps the original binding and the others get fresh, unshadowed
//!   names declared right after it, so they are initialized whenever the
//!   original is.
//! - Branch folding (the narrow literal-parameter part of
//!   type-directed-control-flow, which this bundle lists in `uses`) only
//!   decides tests built from the parameter and literals with `!`, `===`,
//!   `!==`, same-kind `==`/`!=`, `&&` and `||`; such tests evaluate without
//!   effects. Switches fold only when every case test is a literal and no other
//!   `break` targets the switch. Statements after a statement that always
//!   returns or throws are dropped only when none of them declares anything.
//!   Remaining uses become `(literal as T)`.
//! - Clones with identical code share one implementation; recursion only
//!   redirects to the fixed set of clones, so expansion is bounded.
//! - Cross-module calls are redirected through unrenamed named imports from the
//!   declaring module; any other alias form preserves the function.
//! - Disabled type-directed-control-flow means no specialization at all. The
//!   original binding is reused for one value, so no declaration is deleted and
//!   residual-dependency-removal is not needed.
use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet};

use anyhow::Result;

use super::{
    super::analysis::{Ctx, RefKind, skip_outer, walk_up_outer},
    signature_support::{
        CallSite, Function, Literal, Observable, closure, delete_items, finalize, functions,
        identifier_texts, is_alias, is_value_read, mentions_type_parameter, target_module, within,
    },
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::FunctionSpecialization;
/// Upper bound on clones per function.
const MAX_VALUES: usize = 8;
/// Functions larger than this are not cloned.
const MAX_STATEMENT_BYTES: usize = 64 * 1024;

pub(in crate::compiler) fn plan(ctx: &mut Ctx, enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let observable = Observable::compute(ctx);
    let functions = functions(ctx);
    let mut planned = 0usize;
    for function in &functions {
        crate::app::check_cancelled()?;
        let nominated = nominate(ctx, function);
        if nominated.is_empty() || !literal_opportunity(ctx, function, &nominated) {
            continue;
        }
        let key = format!("{PASS}:{}", function.ident);
        let skip =
            |ctx: &Ctx, reason| ctx.skip(PASS, key.clone(), function.file, function.name, reason);
        let calls = match closure(ctx, function, &observable) {
            Ok(calls) => calls,
            Err(reason) => {
                out.skips.push(skip(ctx, reason));
                continue;
            }
        };
        if calls.is_empty() {
            continue;
        }
        let mut chosen = None;
        let mut unproven = false;
        for &index in &nominated {
            match call_values(ctx, function, &calls, index) {
                Some(values) if values.len() >= 2 && values.len() <= MAX_VALUES => {
                    chosen = Some((index, values));
                    break;
                }
                Some(_) => {}
                None => unproven = true,
            }
        }
        let Some((index, values)) = chosen else {
            if unproven {
                out.skips.push(skip(ctx, Reason::UnprovenRuntimeFact));
            }
            continue;
        };
        if !enabled.contains(PassId::TypeDirectedControlFlow) {
            out.skips.push(skip(ctx, Reason::RequiresDisabledPass));
            continue;
        }
        match specialize(ctx, function, &calls, index, &values, &key) {
            Ok(Some(candidate)) => {
                planned += 1;
                out.candidates.push(candidate);
            }
            Ok(None) => {}
            Err(reason) => out.skips.push(skip(ctx, reason)),
        }
    }
    tracing::debug!(
        pass = %PASS,
        functions = functions.len(),
        candidates = planned,
        "function specialization planned"
    );
    Ok(())
}

/// Whether direct calls pass at least two distinct literals at a nominated
/// position. Only such functions are worth reporting when preserved.
fn literal_opportunity(ctx: &Ctx, function: &Function, nominated: &[usize]) -> bool {
    let calls: Vec<Node> = ctx
        .refs(function.symbol)
        .iter()
        .filter(|r| r.kind == RefKind::Call)
        .map(|r| walk_up_outer(r.node).parent())
        .filter(|c| c.kind() == SyntaxKind::CallExpression)
        .collect();
    nominated.iter().any(|&index| {
        let mut keys = HashSet::new();
        for call in &calls {
            let args = call.arguments();
            if index < args.len()
                && let Some(value) = Literal::of(args.get(index))
            {
                keys.insert(value.key());
            }
        }
        keys.len() >= 2
    })
}

/// Parameters whose declared type is a finite union of primitive literals
/// and which are only read inside the body.
fn nominate(ctx: &mut Ctx, function: &Function) -> Vec<usize> {
    let params = function.parameters();
    let span = match (params.first(), params.last()) {
        (Some(&first), Some(&last)) => (Ctx::start(first), last.end() as usize),
        _ => return Vec::new(),
    };
    let mut result = Vec::new();
    for (index, &p) in params.iter().enumerate() {
        let name = p.name();
        if name.kind() != SyntaxKind::Identifier
            || name.text() == "this"
            || p.initializer().is_some()
            || p.question_token().is_some()
            || p.dot_dot_dot_token().is_some()
            || p.type_().is_nil()
            || mentions_type_parameter(function.node, p)
        {
            continue;
        }
        let symbol = ctx.resolve(name);
        if symbol.is_nil() {
            continue;
        }
        let ty = ctx.checker.get_type_of_symbol(symbol);
        let flags = ctx.checker.ty(ty).flags();
        if !flags.intersects(TypeFlags::Union) {
            continue;
        }
        let members = ctx.checker.ty(ty).types_list();
        let literal_union = members.iter().all(|&m| {
            ctx.checker.ty(m).flags().intersects(
                TypeFlags::StringLiteral | TypeFlags::NumberLiteral | TypeFlags::BooleanLiteral,
            )
        });
        if !literal_union {
            continue;
        }
        let only_reads = ctx.refs(symbol).iter().all(|r| match r.kind {
            _ if is_value_read(r) => {
                let at = Ctx::start(r.node);
                !(at >= span.0 && at < span.1)
            }
            RefKind::Declaration => r.node == name,
            _ => false,
        });
        if only_reads {
            result.push(index);
        }
    }
    result
}

/// What a call passes at the specialized position.
#[derive(Clone)]
enum Passed {
    Value(Literal),
    /// A recursive call forwarding the parameter itself.
    Forward,
}

/// Distinct literal values passed at `index`, or `None` when some call's
/// value is not a literal.
fn call_values(
    ctx: &mut Ctx,
    function: &Function,
    calls: &[CallSite],
    index: usize,
) -> Option<Vec<Literal>> {
    let symbol = ctx.resolve(function.parameters()[index].name());
    let mut values: Vec<Literal> = Vec::new();
    for &site in calls {
        match passed(ctx, function, site, index, symbol)? {
            Passed::Value(value) => {
                if !values.iter().any(|v| v.key() == value.key()) {
                    values.push(value);
                }
            }
            Passed::Forward => {}
        }
    }
    Some(values)
}

fn passed(
    ctx: &mut Ctx,
    function: &Function,
    site: CallSite,
    index: usize,
    symbol: SymbolId,
) -> Option<Passed> {
    let args = site.call.arguments();
    if index >= args.len() {
        return None;
    }
    let arg = args.get(index);
    if let Some(value) = Literal::of(arg) {
        return Some(Passed::Value(value));
    }
    let inner = skip_outer(arg);
    (site.file == function.file
        && within(site.call, function.node)
        && inner.kind() == SyntaxKind::Identifier
        && ctx.resolve(inner) == symbol)
        .then_some(Passed::Forward)
}

/// Placeholder for a clone's own name while clone texts are compared.
const SELF_NAME: &str = "\u{1}";

fn value_placeholder(value: &Literal) -> String {
    format!("\u{2}{}\u{3}", value.key())
}

fn specialize(
    ctx: &mut Ctx,
    function: &Function,
    calls: &[CallSite],
    index: usize,
    values: &[Literal],
    key: &str,
) -> Result<Option<Candidate>, Reason> {
    let file = function.file;
    let statement = function.statement;
    if has_syntactic_modifier(statement, ModifierFlags::Default) {
        return Err(Reason::UnsupportedSyntax);
    }
    if statement.kind() == SyntaxKind::VariableStatement
        && statement.declaration_list().declarations().nodes().len() != 1
    {
        return Err(Reason::UnsupportedSyntax);
    }
    let statement_bytes = statement.end() as usize - Ctx::start(statement);
    if statement_bytes > MAX_STATEMENT_BYTES {
        return Err(Reason::NoSizeBenefit);
    }
    let params = function.parameters();
    let parameter = params[index];
    let symbol = ctx.resolve(parameter.name());
    let reads: HashSet<Node> = ctx
        .refs(symbol)
        .iter()
        .filter(|r| is_value_read(r))
        .map(|r| r.node)
        .collect();
    let mut inside: HashMap<Node, Passed> = HashMap::new();
    let mut outside: Vec<(CallSite, Literal)> = Vec::new();
    for &site in calls {
        if site.call.expression().kind() != SyntaxKind::Identifier {
            return Err(Reason::UnsupportedSyntax);
        }
        let value =
            passed(ctx, function, site, index, symbol).ok_or(Reason::UnprovenRuntimeFact)?;
        if site.file == file && within(site.call, statement) {
            inside.insert(site.call, value);
        } else {
            match value {
                Passed::Value(value) => outside.push((site, value)),
                Passed::Forward => return Err(Reason::UnsupportedSyntax),
            }
        }
    }
    let cast_type = ctx.text(file, parameter.type_()).to_owned();
    let mut identifiers = Vec::new();
    crate::compiler::collect(statement, &mut identifiers);
    identifiers.retain(|n| n.kind() == SyntaxKind::Identifier);
    // Render one clone per value with placeholders for names.
    let mut texts: Vec<(Literal, String)> = Vec::new();
    let mut folded_any = false;
    for value in values {
        let mut render = Render {
            ctx,
            file,
            reads: &reads,
            value: value.clone(),
            cast: format!("({} as {cast_type})", literal_text(value)),
            calls: &inside,
            index,
            folded: 0,
            identifiers: &identifiers,
        };
        let text = render
            .declaration(function, &params)
            .ok_or(Reason::UnsupportedSyntax)?;
        folded_any |= render.folded > 0;
        texts.push((value.clone(), text));
    }
    if !folded_any {
        // Nothing decides on the parameter; cloning cannot pay off.
        return Ok(None);
    }
    // Share equivalent clones. A group is identified by its text.
    let mut groups: Vec<(String, Vec<Literal>)> = Vec::new();
    for (value, text) in texts {
        if let Some(group) = groups.iter_mut().find(|(t, _)| *t == text) {
            group.1.push(value);
        } else {
            groups.push((text, vec![value]));
        }
    }
    if groups.len() < 2 {
        // Every value behaves the same: constant propagation, not
        // specialization, is the right tool.
        return Ok(None);
    }
    // The group with most outside calls keeps the original binding.
    let count = |group: &[Literal]| {
        outside
            .iter()
            .filter(|(_, v)| group.iter().any(|g| g.key() == v.key()))
            .count()
    };
    let mut order: Vec<usize> = (0..groups.len()).collect();
    order.sort_by_key(|&g| std::cmp::Reverse(count(&groups[g].1)));
    // Fresh names, unshadowed in every module the bundle touches.
    let mut involved: BTreeSet<usize> = BTreeSet::from([file]);
    for (site, _) in &outside {
        involved.insert(site.file);
    }
    let taken = identifier_texts(ctx, &involved);
    let base = function.name.text().to_owned();
    let mut names: Vec<String> = vec![String::new(); groups.len()];
    let mut chosen: HashSet<String> = HashSet::new();
    for (rank, &g) in order.iter().enumerate() {
        if rank == 0 {
            names[g] = base.clone();
            continue;
        }
        let suffix = sanitize(&groups[g].1[0]);
        let mut attempt = 0usize;
        let name = loop {
            let candidate = match (&suffix, attempt) {
                (Some(s), 0) => format!("{base}_{s}"),
                _ => format!("{base}_{}", rank + attempt),
            };
            attempt += 1;
            if attempt > 64 {
                return Err(Reason::UnsupportedSyntax);
            }
            if taken.contains(&candidate) || chosen.contains(&candidate) {
                continue;
            }
            let existing =
                ctx.checker
                    .resolve_name_exported(&candidate, statement, SymbolFlags::All, false);
            if existing.is_some() {
                continue;
            }
            break candidate;
        };
        chosen.insert(name.clone());
        names[g] = name;
    }
    let name_of = |value: &Literal| -> &str {
        let g = groups
            .iter()
            .position(|(_, vs)| vs.iter().any(|v| v.key() == value.key()))
            .expect("every value has a group");
        &names[g]
    };
    // Assemble the declarations: the original binding first, then clones.
    let mut declarations = Vec::new();
    for &g in &order {
        let mut text = groups[g].0.replace(SELF_NAME, &names[g]);
        for value in values {
            text = text.replace(&value_placeholder(value), name_of(value));
        }
        declarations.push(text);
    }
    let file_name = ctx.files[file].name.clone();
    let mut edits = vec![FileEdit::replace(
        &file_name,
        Ctx::start(statement),
        statement.end() as usize,
        declarations.join("\n"),
    )];
    // Redirect every outside call.
    let mut needed: BTreeMap<usize, BTreeSet<String>> = BTreeMap::new();
    for (site, value) in &outside {
        let name = name_of(value).to_owned();
        let call = site.call;
        let site_file = ctx.files[site.file].name.clone();
        let callee = call.expression();
        if name != base {
            edits.push(FileEdit::replace(
                &site_file,
                Ctx::start(callee),
                callee.end() as usize,
                name.clone(),
            ));
            needed.entry(site.file).or_default().insert(name);
        }
        let args: Vec<(usize, usize)> = call
            .arguments()
            .iter()
            .map(|a| (Ctx::start(a), a.end() as usize))
            .collect();
        let delete: Vec<bool> = (0..args.len()).map(|i| i == index).collect();
        edits.extend(
            delete_items(&site_file, &ctx.files[site.file].text, &args, &delete)
                .ok_or(Reason::UnsupportedSyntax)?,
        );
    }
    // Make clones reachable from importing modules.
    let clone_names: Vec<String> = order[1..].iter().map(|&g| names[g].clone()).collect();
    for r in ctx.refs(function.symbol).to_vec() {
        if !is_alias(&r) {
            continue;
        }
        let specifier = r.node.parent();
        let alias_file = ctx.files[r.file].name.clone();
        match specifier.kind() {
            SyntaxKind::ImportSpecifier => {
                if specifier.property_name().is_some() || specifier.is_type_only() {
                    return Err(Reason::UnsupportedSyntax);
                }
                let declaration = specifier.parent().parent().parent();
                if declaration.kind() != SyntaxKind::ImportDeclaration
                    || declaration.import_clause().is_type_only()
                    || target_module(ctx, declaration.module_specifier()) != Some(file)
                {
                    return Err(Reason::UnsupportedSyntax);
                }
                if let Some(names) = needed.get(&r.file) {
                    let list: Vec<&str> = names.iter().map(String::as_str).collect();
                    edits.push(FileEdit::insert(
                        &alias_file,
                        specifier.end() as usize,
                        format!(", {}", list.join(", ")),
                    ));
                }
            }
            SyntaxKind::ExportSpecifier => {
                let declaration = specifier.parent().parent();
                if specifier.property_name().is_some()
                    || r.file != file
                    || declaration.kind() != SyntaxKind::ExportDeclaration
                    || declaration.module_specifier().is_some()
                    || declaration.is_type_only()
                {
                    return Err(Reason::UnsupportedSyntax);
                }
                edits.push(FileEdit::insert(
                    &alias_file,
                    specifier.end() as usize,
                    format!(", {}", clone_names.join(", ")),
                ));
            }
            _ => return Err(Reason::UnsupportedSyntax),
        }
    }
    // A module that calls a clone must have imported the original.
    for &module in needed.keys() {
        if module != file
            && !ctx
                .refs(function.symbol)
                .iter()
                .any(|r| is_alias(r) && r.file == module)
        {
            return Err(Reason::UnsupportedSyntax);
        }
    }
    let edits = finalize(edits).ok_or(Reason::UnsupportedSyntax)?;
    let mut candidate = Candidate::new(PASS, key, ctx.location(file, function.name));
    candidate.uses.push(PassId::TypeDirectedControlFlow);
    candidate.edits = edits;
    Ok(Some(candidate))
}

/// Source text of a literal value.
fn literal_text(value: &Literal) -> String {
    match value {
        Literal::Str(s) => serde_json::to_string(s).expect("strings serialize"),
        Literal::Num(n) => {
            if *n == 0.0 && n.is_sign_negative() {
                "-0".into()
            } else {
                format!("{n}")
            }
        }
        Literal::Bool(b) => b.to_string(),
        Literal::Null => "null".into(),
        Literal::BigInt(b) => format!("{b}n"),
    }
}

/// Identifier-safe suffix derived from a value.
fn sanitize(value: &Literal) -> Option<String> {
    let raw = match value {
        Literal::Str(s) => s.clone(),
        Literal::Bool(b) => b.to_string(),
        Literal::Null => "null".into(),
        Literal::Num(n) if n.fract() == 0.0 && *n >= 0.0 && *n < 1e9 => format!("{n}"),
        _ => return None,
    };
    let cleaned: String = raw
        .chars()
        .map(|c| if c.is_ascii_alphanumeric() { c } else { '_' })
        .collect();
    (!cleaned.is_empty() && cleaned.len() <= 24 && cleaned.chars().any(|c| c != '_'))
        .then_some(cleaned)
}

/// Renders the text of one clone, folding decided branches.
struct Render<'a, 'c> {
    ctx: &'a Ctx<'c>,
    file: usize,
    reads: &'a HashSet<Node>,
    value: Literal,
    cast: String,
    calls: &'a HashMap<Node, Passed>,
    index: usize,
    folded: usize,
    /// Identifiers of the whole declaration, for scope checks.
    identifiers: &'a [Node],
}

impl Render<'_, '_> {
    fn text(&self, start: usize, end: usize) -> &str {
        &self.ctx.files[self.file].text[start..end]
    }

    fn declaration(&mut self, function: &Function, params: &[Node]) -> Option<String> {
        let statement = function.statement;
        let node = function.node;
        let body = node.body();
        let text = &self.ctx.files[self.file].text;
        let mut out = String::new();
        out.push_str(self.text(Ctx::start(statement), Ctx::start(function.name)));
        out.push_str(SELF_NAME);
        let first = Ctx::start(params[0]);
        out.push_str(self.text(function.name.end() as usize, first));
        let mut kept = Vec::new();
        for (i, &p) in params.iter().enumerate() {
            if i != self.index {
                kept.push(self.render(p)?);
            }
        }
        out.push_str(&kept.join(", "));
        let mut after = params[params.len() - 1].end() as usize;
        if kept.is_empty() {
            let bytes = text.as_bytes();
            while after < bytes.len()
                && (bytes[after].is_ascii_whitespace() || bytes[after] == b',')
            {
                after += 1;
            }
            if bytes.get(after) != Some(&b')') {
                return None;
            }
        }
        out.push_str(self.text(after, Ctx::start(body)));
        out.push_str(&self.render(body)?);
        out.push_str(self.text(body.end() as usize, statement.end() as usize));
        Some(out)
    }

    /// Runtime value of a decidable test expression.
    fn value_of(&self, node: Node) -> Option<Literal> {
        let node = skip_outer(node);
        if node.kind() == SyntaxKind::Identifier && self.reads.contains(&node) {
            return Some(self.value.clone());
        }
        if let Some(literal) = Literal::of(node) {
            return Some(literal);
        }
        match node.kind() {
            SyntaxKind::PrefixUnaryExpression
                if node.operator() == SyntaxKind::ExclamationToken =>
            {
                Some(Literal::Bool(!self.value_of(node.operand())?.truthy()))
            }
            SyntaxKind::BinaryExpression => {
                let operator = node.operator_token().kind();
                match operator {
                    SyntaxKind::AmpersandAmpersandToken => {
                        let left = self.value_of(node.left())?;
                        if left.truthy() {
                            self.value_of(node.right())
                        } else {
                            Some(left)
                        }
                    }
                    SyntaxKind::BarBarToken => {
                        let left = self.value_of(node.left())?;
                        if left.truthy() {
                            Some(left)
                        } else {
                            self.value_of(node.right())
                        }
                    }
                    SyntaxKind::EqualsEqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsEqualsToken
                    | SyntaxKind::EqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsToken => {
                        let left = self.value_of(node.left())?;
                        let right = self.value_of(node.right())?;
                        let loose = matches!(
                            operator,
                            SyntaxKind::EqualsEqualsToken | SyntaxKind::ExclamationEqualsToken
                        );
                        if loose && std::mem::discriminant(&left) != std::mem::discriminant(&right)
                        {
                            return None;
                        }
                        let equal = left.strict_equals(&right);
                        let negated = matches!(
                            operator,
                            SyntaxKind::ExclamationEqualsEqualsToken
                                | SyntaxKind::ExclamationEqualsToken
                        );
                        Some(Literal::Bool(equal != negated))
                    }
                    _ => None,
                }
            }
            _ => None,
        }
    }

    /// Whether a test mentions the parameter and can be decided.
    fn decide(&self, node: Node) -> Option<bool> {
        let mut nodes = Vec::new();
        crate::compiler::collect(node, &mut nodes);
        if !nodes.iter().any(|n| self.reads.contains(n)) {
            return None;
        }
        self.value_of(node).map(|v| v.truthy())
    }

    /// Whether executing the (folded) statement always returns or throws.
    fn terminates(&self, node: Node) -> bool {
        match node.kind() {
            SyntaxKind::ReturnStatement | SyntaxKind::ThrowStatement => true,
            SyntaxKind::Block => node.statements().iter().any(|s| self.terminates(s)),
            SyntaxKind::IfStatement => match self.decide(node.expression()) {
                Some(true) => self.terminates(node.then_statement()),
                Some(false) => {
                    node.else_statement().is_some() && self.terminates(node.else_statement())
                }
                None => {
                    node.else_statement().is_some()
                        && self.terminates(node.then_statement())
                        && self.terminates(node.else_statement())
                }
            },
            _ => false,
        }
    }

    fn render(&mut self, node: Node) -> Option<String> {
        match node.kind() {
            SyntaxKind::Identifier if self.reads.contains(&node) => return Some(self.cast.clone()),
            SyntaxKind::ShorthandPropertyAssignment if self.reads.contains(&node.name()) => {
                if node.object_assignment_initializer().is_some() {
                    return None;
                }
                return Some(format!("{}: {}", node.name().text(), self.cast));
            }
            SyntaxKind::CallExpression if self.calls.contains_key(&node) => {
                return self.redirect(node);
            }
            SyntaxKind::IfStatement => {
                if let Some(decision) = self.decide(node.expression()) {
                    self.folded += 1;
                    let chosen = if decision {
                        node.then_statement()
                    } else {
                        node.else_statement()
                    };
                    return self.replacement_statement(node, chosen);
                }
            }
            SyntaxKind::ConditionalExpression => {
                if let Some(decision) = self.decide(node.condition()) {
                    self.folded += 1;
                    let chosen = if decision {
                        node.when_true()
                    } else {
                        node.when_false()
                    };
                    let text = self.render(chosen)?;
                    return Some(if text.starts_with('{') {
                        format!("({text})")
                    } else {
                        text
                    });
                }
            }
            SyntaxKind::SwitchStatement => {
                if let Some(text) = self.switch(node) {
                    self.folded += 1;
                    return Some(text);
                }
            }
            _ => {}
        }
        self.splice(node)
    }

    /// Generic rendering: the node's text with every child re-rendered.
    /// Statement lists drop statements that follow a terminating one.
    fn splice(&mut self, node: Node) -> Option<String> {
        let start = Ctx::start(node);
        let end = node.end() as usize;
        let mut children = Vec::new();
        node.for_each_child(|child| {
            children.push(child);
            false
        });
        let list = matches!(
            node.kind(),
            SyntaxKind::Block | SyntaxKind::CaseClause | SyntaxKind::DefaultClause
        );
        let statements: Vec<Node> = if list {
            node.statements().to_vec()
        } else {
            Vec::new()
        };
        let mut out = String::new();
        let mut cursor = start;
        let mut dead = false;
        for child in children {
            let child_start = Ctx::start(child);
            let child_end = child.end() as usize;
            if child_end <= child_start && child_start == cursor {
                continue;
            }
            if child_start < cursor || child_end > end {
                return None;
            }
            let is_statement = list && statements.contains(&child);
            if is_statement && dead {
                cursor = child_end;
                continue;
            }
            out.push_str(self.text(cursor, child_start));
            out.push_str(&self.render(child)?);
            cursor = child_end;
            if is_statement && self.terminates(child) {
                let position = statements.iter().position(|&s| s == child)?;
                dead = statements[position + 1..].iter().all(|&s| !declares(s));
            }
        }
        out.push_str(self.text(cursor, end));
        Some(out)
    }

    /// Text replacing a folded statement `node` by `chosen` (possibly nil).
    fn replacement_statement(&mut self, node: Node, chosen: Node) -> Option<String> {
        let in_list = matches!(
            node.parent().kind(),
            SyntaxKind::Block | SyntaxKind::CaseClause | SyntaxKind::DefaultClause
        );
        if chosen.is_nil() {
            return Some(if in_list { String::new() } else { ";".into() });
        }
        let text = self.render(chosen)?;
        if chosen.kind() == SyntaxKind::Block {
            let statements = chosen.statements().to_vec();
            if in_list && self.unwrappable(&statements, chosen) && text.len() >= 2 {
                return Some(text[1..text.len() - 1].trim().to_owned());
            }
            return Some(text);
        }
        // A bare statement in a nested position keeps braces so that an
        // inner `if` cannot capture a following `else`.
        Some(if in_list { text } else { format!("{{{text}}}") })
    }

    /// Whether `statements`, currently scoped by `region`, can move into the
    /// enclosing statement list: their block-scoped bindings are plain
    /// `let`/`const` names that nothing else in the declaration mentions.
    fn unwrappable(&self, statements: &[Node], region: Node) -> bool {
        let mut names = HashSet::new();
        for &statement in statements {
            if !declares_lexically(statement) {
                continue;
            }
            if statement.kind() != SyntaxKind::VariableStatement
                || statement
                    .declaration_list()
                    .flags()
                    .intersects(NodeFlags::Using)
            {
                return false;
            }
            for declaration in statement.declaration_list().declarations().nodes().iter() {
                if declaration.name().kind() != SyntaxKind::Identifier {
                    return false;
                }
                names.insert(declaration.name().text());
            }
        }
        if names.is_empty() {
            return true;
        }
        self.identifiers
            .iter()
            .all(|&n| within(n, region) || !names.contains(n.text()))
    }

    /// Folds `switch (parameter)` with literal case tests.
    fn switch(&mut self, node: Node) -> Option<String> {
        let discriminant = skip_outer(node.expression());
        if !(discriminant.kind() == SyntaxKind::Identifier && self.reads.contains(&discriminant)) {
            return None;
        }
        let clauses: Vec<Node> = node.case_block().clauses().nodes().to_vec();
        let mut matched = None;
        let mut default = None;
        for (i, &clause) in clauses.iter().enumerate() {
            if clause.kind() == SyntaxKind::DefaultClause {
                default = Some(i);
                continue;
            }
            let test = Literal::of(clause.expression())?;
            if matched.is_none() && test.strict_equals(&self.value) {
                matched = Some(i);
            }
        }
        let in_list = matches!(
            node.parent().kind(),
            SyntaxKind::Block | SyntaxKind::CaseClause | SyntaxKind::DefaultClause
        );
        let Some(first) = matched.or(default) else {
            return Some(if in_list { String::new() } else { ";".into() });
        };
        // Execution falls through until a clause ends with an exit.
        let mut statements: Vec<Node> = Vec::new();
        for &clause in &clauses[first..] {
            let body: Vec<Node> = clause.statements().to_vec();
            statements.extend(body.iter().copied());
            if body.last().is_some_and(|&s| {
                matches!(
                    s.kind(),
                    SyntaxKind::BreakStatement
                        | SyntaxKind::ReturnStatement
                        | SyntaxKind::ThrowStatement
                        | SyntaxKind::ContinueStatement
                )
            }) {
                break;
            }
        }
        if statements
            .last()
            .is_some_and(|&s| s.kind() == SyntaxKind::BreakStatement && s.label().is_nil())
        {
            statements.pop();
        }
        for &statement in &statements {
            if breaks_switch(statement, node) {
                return None;
            }
        }
        let mut parts = Vec::new();
        for &statement in &statements {
            parts.push(self.render(statement)?);
            if self.terminates(statement) {
                break;
            }
        }
        let body = parts.join("\n");
        Some(if in_list && self.unwrappable(&statements, node) {
            body
        } else {
            format!("{{{body}}}")
        })
    }

    /// A recursive call redirected to the clone for its value.
    fn redirect(&mut self, call: Node) -> Option<String> {
        let target = match self.calls.get(&call)? {
            Passed::Value(value) => value_placeholder(value),
            // Forwarding the parameter calls the clone itself, so clones
            // that differ only in their own value stay shareable.
            Passed::Forward => SELF_NAME.to_owned(),
        };
        let callee = call.expression();
        let args: Vec<Node> = call.arguments().to_vec();
        let mut out = target;
        out.push_str(self.text(callee.end() as usize, Ctx::start(args[0])));
        let mut kept = Vec::new();
        for (i, &arg) in args.iter().enumerate() {
            if i != self.index {
                kept.push(self.render(arg)?);
            }
        }
        out.push_str(&kept.join(", "));
        if kept.is_empty() {
            out.push(')');
        } else {
            out.push_str(self.text(args[args.len() - 1].end() as usize, call.end() as usize));
        }
        Some(out)
    }
}

/// Whether a statement declares a binding (hoisted or lexical).
fn declares(statement: Node) -> bool {
    matches!(
        statement.kind(),
        SyntaxKind::VariableStatement
            | SyntaxKind::FunctionDeclaration
            | SyntaxKind::ClassDeclaration
            | SyntaxKind::EnumDeclaration
            | SyntaxKind::ModuleDeclaration
            | SyntaxKind::InterfaceDeclaration
            | SyntaxKind::TypeAliasDeclaration
            | SyntaxKind::ImportDeclaration
            | SyntaxKind::ExportDeclaration
            | SyntaxKind::LabeledStatement
    )
}

/// Whether a statement declares a block-scoped binding.
fn declares_lexically(statement: Node) -> bool {
    match statement.kind() {
        SyntaxKind::VariableStatement => statement
            .declaration_list()
            .flags()
            .intersects(NodeFlags::BlockScoped),
        SyntaxKind::FunctionDeclaration
        | SyntaxKind::ClassDeclaration
        | SyntaxKind::EnumDeclaration
        | SyntaxKind::ModuleDeclaration
        | SyntaxKind::InterfaceDeclaration
        | SyntaxKind::TypeAliasDeclaration => true,
        _ => false,
    }
}

/// Whether `statement` contains an unlabeled `break` that targets `switch`.
fn breaks_switch(statement: Node, switch: Node) -> bool {
    let mut nodes = Vec::new();
    crate::compiler::collect(statement, &mut nodes);
    nodes.into_iter().any(|n| {
        if n.kind() != SyntaxKind::BreakStatement || n.label().is_some() {
            return false;
        }
        let mut current = n.parent();
        while current.is_some() {
            match current.kind() {
                SyntaxKind::ForStatement
                | SyntaxKind::ForInStatement
                | SyntaxKind::ForOfStatement
                | SyntaxKind::WhileStatement
                | SyntaxKind::DoStatement => return false,
                SyntaxKind::SwitchStatement => return current == switch,
                _ if is_function_like(current) => return false,
                _ => current = current.parent(),
            }
        }
        false
    })
}
