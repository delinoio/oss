// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `unused-field-removal`: deletes class fields and object literal
//! properties that are never read, together with their dead writes. The
//! right-hand side of a removed write still evaluates exactly once, in
//! place, and keeps the assignment expression's value.
//!
//! Fields and literal properties are own enumerable data properties, so
//! removing one is unobservable only when no code can enumerate, reflect on,
//! serialize, spread or look up the owning object by a computed key. The
//! proof therefore requires runtime confinement of the owner, not types:
//! - Class fields (`x = v`, `x;`, `#x = v`, static or instance): the class is
//!   internal (no public entry export, JavaScript reference or observable
//!   module namespace), has no decorators and no `extends`, and every instance
//!   and the constructor are confined: the class binding is only used by `new
//!   C(...)`, `C.staticMember` and type positions; each `new C(...)` is
//!   discarded, used directly as a member receiver or bound to a `const` used
//!   only as a member receiver; and every `this` of the class's own members is
//!   a member receiver. Calls through fields must target never-reassigned arrow
//!   initializers. Members are the class's own declarations, so no getter,
//!   setter or inherited lookup is involved. `#private` fields are not
//!   properties at all and only need the private name to be unused except for
//!   removable writes.
//! - Every other mention of the field name in application code is a removable
//!   write `receiver.x = value` (no compound or logical assignment,
//!   destructuring, `delete` or `in`), so types stay valid. Writes through
//!   `this` happen in the constructor, a field initializer, an accessor or a
//!   method that is only ever called on its receiver, so the receiver is an
//!   instance of the class. Writes through `new C()` receivers stay.
//! - Initializers are pure (`Ctx::is_pure`), so field initialization order and
//!   effects are unchanged.
//! - Object literals: a `const` initialized with a fresh literal of plain data
//!   properties (no spread, methods, accessors, computed keys, `__proto__` or
//!   non-arrow function values), without a type annotation or `typeof` use,
//!   whose every reference is a member access of one of its own properties.
//!   Calls through the binding target arrow-valued properties that are never
//!   written. Unread properties with pure values and their writes are removed.
use std::collections::{BTreeMap, BTreeSet};

use anyhow::Result;

use super::{
    super::{
        analysis::{Ctx, RefKind, skip_outer, walk_up_outer},
        token_start,
    },
    deadcode_support::{
        Confinement, Exposure, Keys, Mentions, class_name, class_symbol, confine, evaluated,
        list_removals, removal_start, static_name, this_owner, within,
    },
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::UnusedFieldRemoval;

pub(in crate::compiler) fn plan(ctx: &mut Ctx, _enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let mentions = Mentions::compute(ctx);
    let mut exposure = None;
    let mut keys = Keys::default();
    let (mut candidates, mut skips) = (0usize, 0usize);
    for file in 0..ctx.files.len() {
        crate::app::check_cancelled()?;
        if ctx.files[file].js || !is_external_module(ctx.files[file].node) {
            continue;
        }
        let nodes = ctx.files[file].nodes.clone();
        for node in nodes {
            let results = match node.kind() {
                SyntaxKind::ClassDeclaration => {
                    let exposure = exposure.get_or_insert_with(|| Exposure::compute(ctx));
                    class_fields(ctx, &mentions, exposure, &mut keys, file, node)
                }
                SyntaxKind::VariableDeclaration => {
                    let exposure = exposure.get_or_insert_with(|| Exposure::compute(ctx));
                    object_properties(ctx, exposure, &mut keys, file, node)
                }
                _ => continue,
            };
            for result in results {
                match result {
                    Ok(candidate) => {
                        candidates += 1;
                        out.candidates.push(candidate);
                    }
                    Err(skip) => {
                        skips += 1;
                        out.skips.push(skip);
                    }
                }
            }
        }
    }
    tracing::debug!(pass = %PASS, candidates, skipped = skips, "unused fields planned");
    Ok(())
}

type Outcome = Result<Candidate, crate::plan::Skip>;

/// One removal per unread field of a class.
fn class_fields(
    ctx: &mut Ctx,
    mentions: &Mentions,
    exposure: &Exposure,
    keys: &mut Keys,
    file: usize,
    class: Node,
) -> Vec<Outcome> {
    let fields: Vec<Node> = class
        .members()
        .iter()
        .filter(|m| m.kind() == SyntaxKind::PropertyDeclaration && static_name(*m).is_some())
        .collect();
    if fields.is_empty() {
        return Vec::new();
    }
    let symbol = class_symbol(ctx, class);
    let mut confinement: Option<Result<Confinement, Reason>> = None;
    let mut results = Vec::new();
    for field in fields {
        let name = static_name(field).unwrap_or_default();
        let span = (token_start(field), field.end() as usize);
        let private = name.starts_with('#');
        // Every mention other than the declaration and its own initializer.
        let others: Vec<Node> = if private {
            let body = (token_start(class), class.end() as usize);
            ctx.files[file]
                .nodes
                .iter()
                .copied()
                .filter(|&n| {
                    n.kind() == SyntaxKind::PrivateIdentifier
                        && n.text() == name
                        && within(n, body)
                        && !within(n, span)
                })
                .collect()
        } else {
            mentions
                .of(name)
                .iter()
                .filter(|&&(f, n)| !(f == file && within(n, span)))
                .map(|&(_, n)| n)
                .collect()
        };
        let writes: Option<Vec<Node>> = others.iter().map(|&n| simple_write(n)).collect();
        let Some(writes) = writes else {
            // Read somewhere: ordinary live field.
            continue;
        };
        let key = keys.unique(format!(
            "field:{}:{}.{name}",
            ctx.files[file].name,
            class_name(class)
        ));
        let skip = |ctx: &Ctx, reason| Err(ctx.skip(PASS, key.clone(), file, field, reason));
        if !field.decorators().is_empty() || !class.decorators().is_empty() {
            results.push(skip(ctx, Reason::Decorator));
            continue;
        }
        if has_syntactic_modifier(
            field,
            ModifierFlags::Ambient | ModifierFlags::Abstract | ModifierFlags::Accessor,
        ) {
            results.push(skip(ctx, Reason::UnsupportedSyntax));
            continue;
        }
        if field.initializer().is_some() && !ctx.is_pure(field.initializer()) {
            results.push(skip(ctx, Reason::SideEffects));
            continue;
        }
        // Public and TypeScript-private fields are own properties: the
        // owner must be confined. Private names are invisible to reflection.
        let confined = if private {
            None
        } else {
            if symbol.is_nil() {
                results.push(skip(ctx, Reason::Escapes));
                continue;
            }
            if let Some(reason) = class_boundary(ctx, exposure, class, symbol) {
                results.push(skip(ctx, reason));
                continue;
            }
            let both_sides = class
                .members()
                .iter()
                .filter(|m| static_name(*m) == Some(name))
                .count()
                > 1;
            if both_sides {
                results.push(skip(ctx, Reason::UnsupportedSyntax));
                continue;
            }
            match confinement.get_or_insert_with(|| confine(ctx, class, symbol)) {
                Ok(confinement) => {
                    // Instance fields are own properties of instances, which
                    // code holding the constructor can create; static fields
                    // of the constructor, which every instance exposes as
                    // `instance.constructor`. Both sides must be confined.
                    if let Some(reason) = confinement.instances.or(confinement.constructor) {
                        results.push(skip(ctx, reason));
                        continue;
                    }
                    Some(&*confinement)
                }
                Err(reason) => {
                    let reason = *reason;
                    results.push(skip(ctx, reason));
                    continue;
                }
            }
        };
        let extracted =
            |method: &str| confined.is_none_or(|c: &Confinement| c.extracted.contains(method));
        // Writes of public names must target confined instances; other
        // objects may own a property of the same name.
        if let Some(confinement) = confined {
            let tracked = confinement
                .accesses
                .get(name)
                .map_or(&[][..], Vec::as_slice);
            let foreign = writes
                .iter()
                .any(|&w| !tracked.contains(&skip_outer(w.left())));
            if foreign {
                results.push(skip(ctx, Reason::Referenced));
                continue;
            }
        }
        let mut edits = vec![FileEdit::delete(
            &ctx.files[file].name,
            removal_start(&ctx.files[file].text, field),
            field.end() as usize,
        )];
        let mut blocked = None;
        for &write in &writes {
            match write_edit(ctx, class, write, &extracted, private) {
                Ok(edit) => edits.push(edit),
                Err(reason) => {
                    blocked = Some(reason);
                    break;
                }
            }
        }
        if let Some(reason) = blocked {
            results.push(skip(ctx, reason));
            continue;
        }
        let mut candidate = Candidate::new(PASS, key, ctx.location(file, field));
        candidate.edits = edits;
        results.push(Ok(candidate));
    }
    results
}

/// The assignment of a name mention that is the target of a plain `=`
/// write `receiver.name = value`, or `None` for any other use.
fn simple_write(name: Node) -> Option<Node> {
    let access = name.parent();
    if access.kind() != SyntaxKind::PropertyAccessExpression || access.name() != name {
        return None;
    }
    if access.question_dot_token().is_some() {
        return None;
    }
    let outer = walk_up_outer(access);
    let assignment = outer.parent();
    (assignment.kind() == SyntaxKind::BinaryExpression
        && assignment.left() == outer
        && assignment.operator_token().kind() == SyntaxKind::EqualsToken)
        .then_some(assignment)
}

/// Replaces a dead write by the evaluation of its right-hand side.
fn write_edit(
    ctx: &mut Ctx,
    class: Node,
    assignment: Node,
    extracted: &dyn Fn(&str) -> bool,
    private: bool,
) -> Result<FileEdit, Reason> {
    let access = skip_outer(assignment.left());
    let receiver = skip_outer(access.expression());
    match receiver.kind() {
        SyntaxKind::ThisKeyword => {
            let owner = this_owner(receiver);
            if owner.parent() != class {
                return Err(Reason::Escapes);
            }
            let instance_receiver = match owner.kind() {
                SyntaxKind::Constructor => true,
                // Field initializers, accessors and static blocks run with
                // the instance (or class) as receiver; private writes
                // outside the constructor could throw on foreign receivers.
                SyntaxKind::PropertyDeclaration
                | SyntaxKind::GetAccessor
                | SyntaxKind::SetAccessor
                | SyntaxKind::ClassStaticBlockDeclaration => !private,
                SyntaxKind::MethodDeclaration => static_name(owner).is_some_and(|m| !extracted(m)),
                _ => false,
            };
            if !instance_receiver {
                return Err(Reason::Mutation);
            }
        }
        SyntaxKind::Identifier if !private => {
            if !ctx.is_pure(receiver) {
                return Err(Reason::SideEffects);
            }
        }
        _ => return Err(Reason::Mutation),
    }
    let file = ctx
        .file_of_node(assignment)
        .ok_or(Reason::ExternalBoundary)?;
    let name = ctx.files[file].name.clone();
    let value = assignment.right();
    let statement = walk_up_outer(assignment).parent();
    if statement.kind() == SyntaxKind::ExpressionStatement {
        if ctx.is_pure(value) {
            return Ok(FileEdit::delete(
                &name,
                removal_start(&ctx.files[file].text, statement),
                statement.end() as usize,
            ));
        }
        let text = evaluated(assignment, ctx.text(file, value));
        return Ok(FileEdit::replace(
            &name,
            token_start(assignment),
            assignment.end() as usize,
            text,
        ));
    }
    let text = evaluated(assignment, ctx.text(file, value));
    Ok(FileEdit::replace(
        &name,
        token_start(assignment),
        assignment.end() as usize,
        text,
    ))
}

/// The class is reachable only through indexed application references.
fn class_boundary(
    ctx: &mut Ctx,
    exposure: &Exposure,
    class: Node,
    symbol: SymbolId,
) -> Option<Reason> {
    if ctx.public.contains(&symbol) {
        return Some(Reason::PublicExport);
    }
    if !ctx.is_internal(symbol) {
        return Some(Reason::ExternalBoundary);
    }
    let file = ctx.file_of_node(class)?;
    let exported_here =
        has_syntactic_modifier(class, ModifierFlags::Export) && exposure.observed(file);
    let reexported = ctx.refs(symbol).iter().any(|r| {
        matches!(r.kind, RefKind::Alias | RefKind::Declaration)
            && r.node.parent().kind() == SyntaxKind::ExportSpecifier
            && exposure.observed(r.file)
    });
    (exported_here || reexported).then_some(Reason::Reflection)
}

/// Unread properties of a confined `const` object literal.
fn object_properties(
    ctx: &mut Ctx,
    exposure: &Exposure,
    keys: &mut Keys,
    file: usize,
    declaration: Node,
) -> Vec<Outcome> {
    let literal = declaration.initializer();
    if literal.is_nil()
        || literal.kind() != SyntaxKind::ObjectLiteralExpression
        || declaration.name().kind() != SyntaxKind::Identifier
        || declaration.type_().is_some()
        || !declaration.parent().flags().intersects(NodeFlags::Const)
        || (declaration.parent().flags() & NodeFlags::BlockScoped) != NodeFlags::Const
    {
        return Vec::new();
    }
    // Plain data properties with distinct static names.
    let properties: Vec<Node> = literal.properties().iter().collect();
    let mut names: BTreeMap<&'static str, Node> = BTreeMap::new();
    for &property in &properties {
        let plain = matches!(
            property.kind(),
            SyntaxKind::PropertyAssignment | SyntaxKind::ShorthandPropertyAssignment
        ) && matches!(
            property.name().kind(),
            SyntaxKind::Identifier | SyntaxKind::StringLiteral
        );
        if !plain {
            return Vec::new();
        }
        let name = property.name().text();
        if name == "__proto__" || names.insert(name, property).is_some() {
            return Vec::new();
        }
        if property.kind() == SyntaxKind::PropertyAssignment {
            let value = skip_outer(property.initializer());
            if matches!(
                value.kind(),
                SyntaxKind::FunctionExpression | SyntaxKind::ClassExpression
            ) {
                return Vec::new();
            }
        }
    }
    let symbol = ctx.resolve(declaration.name());
    if symbol.is_nil() || ctx.public.contains(&symbol) || !ctx.is_internal(symbol) {
        return Vec::new();
    }
    let statement = declaration.parent().parent();
    if has_syntactic_modifier(statement, ModifierFlags::Export) && exposure.observed(file) {
        return Vec::new();
    }
    // An unreferenced binding is left whole to residual-dependency-removal.
    if ctx
        .refs(symbol)
        .iter()
        .all(|r| r.node == declaration.name())
    {
        return Vec::new();
    }
    // Every reference is a member access of an own property.
    let mut reads: BTreeSet<&'static str> = BTreeSet::new();
    let mut writes: BTreeMap<&'static str, Vec<Node>> = BTreeMap::new();
    let mut called: BTreeSet<&'static str> = BTreeSet::new();
    for r in ctx.refs(symbol).to_vec() {
        match r.kind {
            RefKind::Declaration if r.node == declaration.name() => continue,
            RefKind::Read | RefKind::Call => {}
            _ => return Vec::new(),
        }
        let outer = walk_up_outer(r.node);
        let access = outer.parent();
        if access.kind() != SyntaxKind::PropertyAccessExpression || access.expression() != outer {
            return Vec::new();
        }
        let Some((&name, _)) = names.get_key_value(access.name().text()) else {
            return Vec::new();
        };
        let holder = walk_up_outer(access).parent();
        if holder.kind() == SyntaxKind::DeleteExpression {
            return Vec::new();
        }
        if holder.kind() == SyntaxKind::CallExpression
            && holder.expression() == walk_up_outer(access)
        {
            called.insert(name);
        }
        match simple_write(access.name()) {
            Some(assignment) => writes.entry(name).or_default().push(assignment),
            None if is_write_access(access) => return Vec::new(),
            None => {
                reads.insert(name);
            }
        }
    }
    // A call passes the object as `this` unless the callee is an arrow
    // function that is never replaced.
    for name in &called {
        let property = names[name];
        let arrow = property.kind() == SyntaxKind::PropertyAssignment
            && skip_outer(property.initializer()).kind() == SyntaxKind::ArrowFunction;
        if !arrow || writes.contains_key(name) {
            return Vec::new();
        }
    }
    let key = keys.unique(format!(
        "object:{}:{}",
        ctx.files[file].name,
        declaration.name().text()
    ));
    let mut dead = Vec::new();
    let mut impure = false;
    for &property in &properties {
        let name = property.name().text();
        let unread = !reads.contains(name);
        let value = if property.kind() == SyntaxKind::PropertyAssignment {
            property.initializer()
        } else {
            property.name()
        };
        let removable = unread && ctx.is_pure(value);
        impure |= unread && !removable;
        dead.push(removable);
    }
    if !dead.iter().any(|&d| d) {
        return if impure {
            vec![Err(ctx.skip(
                PASS,
                key,
                file,
                declaration,
                Reason::SideEffects,
            ))]
        } else {
            Vec::new()
        };
    }
    let name = ctx.files[file].name.clone();
    let mut candidate = Candidate::new(PASS, key, ctx.location(file, declaration));
    if dead.iter().all(|&d| d) {
        candidate.edits.push(FileEdit::replace(
            &name,
            token_start(literal),
            literal.end() as usize,
            "{}",
        ));
    } else {
        for (start, end) in list_removals(&properties, &dead) {
            candidate.edits.push(FileEdit::delete(&name, start, end));
        }
    }
    for (index, &property) in properties.iter().enumerate() {
        if !dead[index] {
            continue;
        }
        for &assignment in writes.get(property.name().text()).into_iter().flatten() {
            let value = assignment.right();
            let statement = walk_up_outer(assignment).parent();
            let edit = if statement.kind() == SyntaxKind::ExpressionStatement {
                if ctx.is_pure(value) {
                    FileEdit::delete(
                        &name,
                        removal_start(&ctx.files[file].text, statement),
                        statement.end() as usize,
                    )
                } else {
                    FileEdit::replace(
                        &name,
                        token_start(assignment),
                        assignment.end() as usize,
                        evaluated(assignment, ctx.text(file, value)),
                    )
                }
            } else {
                FileEdit::replace(
                    &name,
                    token_start(assignment),
                    assignment.end() as usize,
                    evaluated(assignment, ctx.text(file, value)),
                )
            };
            // Writes from other modules are edits of their own files.
            if ctx.file_of_node(assignment) != Some(file) {
                return vec![Err(ctx.skip(
                    PASS,
                    candidate.key.clone(),
                    file,
                    declaration,
                    Reason::UnsupportedSyntax,
                ))];
            }
            candidate.edits.push(edit);
        }
    }
    vec![Ok(candidate)]
}
