// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `constant-property-inlining`: replaces reads of proven-constant
//! primitive properties with their literal values and removes the property
//! definition when nothing can observe it any more.
//!
//! Supported definitions:
//! - Data properties of a `const` binding initialized with an object literal
//!   (`const THEME = { LIGHT: "light" } as const`), read as `THEME.LIGHT` or
//!   `THEME["LIGHT"]`, also across internal modules.
//! - `static` fields of a top-level class declaration, read as `C.X`.
//!
//! Proof obligations (`readonly`, `as const` and literal types are never
//! evidence; all references are):
//! - The binding is internal: declared once in application TypeScript, not
//!   ambient or merged, not reachable through public entry exports, JavaScript
//!   or an escaping module namespace object.
//! - The object never escapes: every runtime reference is a static member read,
//!   a write of a different member, or a read-only observation (identity,
//!   `typeof`, truthiness, `in`, `for...in`, spread and the built-in
//!   `Object.keys`-style readers when the global `Object` is never replaced).
//!   Passing the object anywhere, calls through its members (`this`),
//!   computed-key writes and writes or deletion of the property prevent
//!   inlining.
//! - The definition is a plain data property with a primitive literal value (no
//!   getters, setters, spreads, computed keys or `__proto__`); class fields
//!   additionally require no heritage, decorators, instances, `this` in static
//!   members or names that collide with function properties.
//! - Each replaced read executes after the definition was evaluated, so no
//!   temporal-dead-zone `ReferenceError` or `undefined` read is lost.
//! - The replacement `(<value> as <type>)` reproduces the original type exactly
//!   and emits only the literal.
//! - The definition is removed only when every read of it was replaced, the
//!   object's shape is not observed, no type query depends on it and the
//!   declaration carries no type constraint.
use std::collections::HashSet;

use anyhow::Result;

use super::{
    super::analysis::{Ctx, skip_outer},
    constants_support::{
        Access, Declared, ModuleGraph, Uses, asi_hazard, canonical_index, class_inert, classify,
        condition_hazard, contains_this, erased_computed_name, escaped_exports, inert_prefix,
        init_safe, is_function_boundary, member_type, object_builtins_intact, print_type,
        pure_value, render, replaceable, unnarrowed,
    },
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::ConstantPropertyInlining;

/// Names that collide with own properties of functions or with special
/// object-literal semantics.
const SPECIAL_NAMES: [&str; 7] = [
    "__proto__",
    "constructor",
    "prototype",
    "name",
    "length",
    "caller",
    "arguments",
];

struct Shared<'a> {
    graph: &'a ModuleGraph,
    escaped: &'a HashSet<SymbolId>,
    object_builtins: bool,
}

/// A literal-valued property definition.
struct Definition {
    name: String,
    /// The property assignment or class member.
    node: Node,
    /// JavaScript literal text of the value.
    value: String,
}

pub(in crate::compiler) fn plan(ctx: &mut Ctx, _enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let mut objects = Vec::new();
    let mut classes = Vec::new();
    for (file, info) in ctx.files.iter().enumerate() {
        if info.js {
            continue;
        }
        for &node in &info.nodes {
            match node.kind() {
                SyntaxKind::VariableDeclaration
                    if node.name().kind() == SyntaxKind::Identifier
                        && node.initializer().is_some()
                        && skip_outer(node.initializer()).kind()
                            == SyntaxKind::ObjectLiteralExpression =>
                {
                    objects.push((file, node));
                }
                SyntaxKind::ClassDeclaration
                    if node.parent().kind() == SyntaxKind::SourceFile && node.name().is_some() =>
                {
                    classes.push((file, node));
                }
                _ => {}
            }
        }
    }
    if objects.is_empty() && classes.is_empty() {
        return Ok(());
    }
    let graph = ModuleGraph::build(ctx);
    let escaped = escaped_exports(ctx);
    let object_builtins = object_builtins_intact(ctx);
    let shared = Shared {
        graph: &graph,
        escaped: &escaped,
        object_builtins,
    };
    let before = out.candidates.len();
    for (file, declaration) in objects {
        crate::app::check_cancelled()?;
        plan_object(ctx, &shared, file, declaration, out);
    }
    for (file, class) in classes {
        crate::app::check_cancelled()?;
        plan_class(ctx, &shared, file, class, out);
    }
    tracing::debug!(
        pass = %PASS,
        candidates = out.candidates.len() - before,
        "constant properties planned"
    );
    Ok(())
}

/// JavaScript literal text of a primitive literal expression.
fn literal_value(ctx: &mut Ctx, file: usize, node: Node) -> Option<String> {
    let node = skip_outer(node);
    match node.kind() {
        SyntaxKind::StringLiteral
        | SyntaxKind::NoSubstitutionTemplateLiteral
        | SyntaxKind::NumericLiteral
        | SyntaxKind::BigIntLiteral
        | SyntaxKind::TrueKeyword
        | SyntaxKind::FalseKeyword
        | SyntaxKind::NullKeyword => Some(ctx.text(file, node).to_owned()),
        SyntaxKind::PrefixUnaryExpression
            if node.operator() == SyntaxKind::MinusToken
                && matches!(
                    node.operand().kind(),
                    SyntaxKind::NumericLiteral | SyntaxKind::BigIntLiteral
                ) =>
        {
            Some(format!("-{}", ctx.text(file, node.operand())))
        }
        SyntaxKind::Identifier if node.text() == "undefined" => {
            // Only the global `undefined`, never a shadowing binding.
            let symbol = ctx.resolve(node);
            let global = symbol.is_some()
                && ctx
                    .checker
                    .sym(symbol)
                    .declarations
                    .to_vec()
                    .iter()
                    .all(|&d| ctx.file_of_node(d).is_none());
            global.then(|| "void 0".to_owned())
        }
        _ => None,
    }
}

fn plan_object(ctx: &mut Ctx, shared: &Shared, file: usize, declaration: Node, out: &mut Plan) {
    let list = declaration.parent();
    let statement = list.parent();
    if !list.flags().intersects(NodeFlags::Const)
        || list.flags().intersects(NodeFlags::Using)
        || statement.kind() != SyntaxKind::VariableStatement
        || statement.flags().intersects(NodeFlags::Ambient)
        || has_syntactic_modifier(statement, ModifierFlags::Ambient)
    {
        return;
    }
    let literal = skip_outer(declaration.initializer());
    let name = declaration.name().text().to_owned();
    let mut definitions = Vec::new();
    let mut seen = HashSet::new();
    let mut shape: Option<Reason> = None;
    for property in literal.properties().iter() {
        let key = property.name();
        let key_text = if key.is_nil() {
            None
        } else {
            match key.kind() {
                SyntaxKind::Identifier | SyntaxKind::StringLiteral => Some(key.text().to_owned()),
                SyntaxKind::NumericLiteral => canonical_index(key.text()),
                _ => None,
            }
        };
        let reason = match property.kind() {
            SyntaxKind::PropertyAssignment
            | SyntaxKind::ShorthandPropertyAssignment
            | SyntaxKind::MethodDeclaration => None,
            SyntaxKind::GetAccessor | SyntaxKind::SetAccessor => Some(Reason::Accessor),
            _ => Some(Reason::UnsupportedSyntax),
        };
        let reason = reason.or_else(|| match &key_text {
            None => Some(Reason::DynamicAccess),
            Some(text) if text == "__proto__" => Some(Reason::SpecialProperty),
            Some(text) if !seen.insert(text.clone()) => Some(Reason::UnsupportedSyntax),
            Some(_) => None,
        });
        if let Some(reason) = reason {
            shape.get_or_insert(reason);
            continue;
        }
        if property.kind() == SyntaxKind::PropertyAssignment
            && let Some(value) = literal_value(ctx, file, property.initializer())
        {
            definitions.push(Definition {
                name: key_text.expect("checked above"),
                node: property,
                value,
            });
        }
    }
    if definitions.is_empty() {
        return;
    }
    let key = format!("const-object:{}:{}", ctx.files[file].name, name);
    let skip = |ctx: &Ctx, reason| ctx.skip(PASS, key.clone(), file, declaration, reason);
    if let Some(reason) = shape {
        out.skips.push(skip(ctx, reason));
        return;
    }
    let symbol = ctx.resolve(declaration.name());
    let Some(uses) = binding_uses(ctx, shared, symbol, out, &HashSet::new(), skip) else {
        return;
    };
    if uses.reads.is_empty() {
        return;
    }
    let declared = Declared {
        file,
        statement,
        member: Node::NIL,
        self_inert: literal.properties().iter().all(|p| match p.kind() {
            SyntaxKind::PropertyAssignment => pure_value(ctx, p.initializer()),
            SyntaxKind::ShorthandPropertyAssignment => ctx.is_pure(p.name()),
            _ => true,
        }),
    };
    // Removing a property changes the object's type; only an unconstrained
    // literal (optionally `as const`) keeps every other type intact.
    let initializer = declaration.initializer();
    let unconstrained = declaration.type_().is_nil()
        && (initializer == literal
            || (initializer.kind() == SyntaxKind::AsExpression
                && initializer.expression() == literal
                && is_const_assertion(initializer.type_())));
    let removable_shape = unconstrained && !uses.shape_observed;
    let object_type = ctx.checker.get_type_of_symbol(symbol);
    for definition in definitions {
        let property = ctx
            .checker
            .get_property_of_type(object_type, &definition.name);
        if property.is_nil() {
            continue;
        }
        let property_key = format!(
            "const-prop:{}:{}.{}",
            ctx.files[file].name, name, definition.name
        );
        let removal = (removable_shape && !uses.member_typed(&definition.name))
            .then(|| property_removal(ctx, file, literal, definition.node));
        plan_property(
            ctx,
            shared,
            &uses,
            &declared,
            &definition,
            property,
            property_key,
            (file, definition.node),
            removal,
            out,
        );
    }
}

fn is_const_assertion(ty: Node) -> bool {
    ty.kind() == SyntaxKind::TypeReference
        && ty.type_name().kind() == SyntaxKind::Identifier
        && ty.type_name().text() == "const"
}

/// Deletes a property definition and its separating comma.
fn property_removal(ctx: &Ctx, file: usize, literal: Node, property: Node) -> FileEdit {
    let properties: Vec<Node> = literal.properties().iter().collect();
    let index = properties
        .iter()
        .position(|&p| p == property)
        .expect("property of its literal");
    let start = Ctx::start(property);
    let text = &ctx.files[file].text;
    let end = match properties.get(index + 1) {
        Some(&next) => Ctx::start(next),
        None => {
            let end = property.end() as usize;
            let rest = &text[end..];
            let trimmed = rest.trim_start();
            if trimmed.starts_with(',') {
                end + (rest.len() - trimmed.len()) + 1
            } else {
                end
            }
        }
    };
    FileEdit::delete(&ctx.files[file].name, start, end)
}

/// Shared binding checks; records a skip and returns `None` when the
/// binding cannot carry constant properties.
fn binding_uses(
    ctx: &mut Ctx,
    shared: &Shared,
    symbol: SymbolId,
    out: &mut Plan,
    allowed_calls: &HashSet<String>,
    skip: impl Fn(&Ctx, Reason) -> crate::plan::Skip,
) -> Option<Uses> {
    if symbol.is_nil() {
        return None;
    }
    if ctx.checker.sym(symbol).declarations.len() != 1 {
        out.skips.push(skip(ctx, Reason::DeclarationMerging));
        return None;
    }
    if !ctx.is_internal(symbol) {
        let reason = if ctx.public.contains(&symbol) {
            Reason::PublicExport
        } else {
            Reason::ExternalBoundary
        };
        out.skips.push(skip(ctx, reason));
        return None;
    }
    if shared.escaped.contains(&symbol) {
        out.skips.push(skip(ctx, Reason::Escapes));
        return None;
    }
    let mut uses = classify(ctx, symbol, shared.object_builtins);
    // A reassigned method may be any function; calling it passes the object.
    let writes = uses.writes.clone();
    uses.calls
        .retain(|(name, _)| !allowed_calls.contains(name) || writes.contains(name));
    if let Some(reason) = uses.first_blocker() {
        out.skips.push(skip(ctx, reason));
        return None;
    }
    Some(uses)
}

/// Plans one constant property: inline every safe read and, with
/// `removal`, delete the definition when no read remains.
#[allow(clippy::too_many_arguments)]
fn plan_property(
    ctx: &mut Ctx,
    shared: &Shared,
    uses: &Uses,
    declared: &Declared,
    definition: &Definition,
    property: SymbolId,
    key: String,
    location: (usize, Node),
    removal: Option<FileEdit>,
    out: &mut Plan,
) {
    let skip = |ctx: &Ctx, reason| ctx.skip(PASS, key.clone(), location.0, location.1, reason);
    let reads: Vec<Access> = uses
        .reads
        .iter()
        .filter(|(name, _)| name == &definition.name)
        .map(|(_, access)| *access)
        .collect();
    if reads.is_empty() {
        return;
    }
    if uses.writes.contains(&definition.name) {
        out.skips.push(skip(ctx, Reason::Mutation));
        return;
    }
    let declared_type = ctx.checker.get_type_of_symbol(property);
    let fresh = ctx.checker.is_fresh_literal_type(declared_type);
    let regular = ctx.checker.get_regular_type_of_literal_type(declared_type);
    let printed = if fresh {
        None
    } else {
        print_type(ctx, regular)
    };
    let mut edits = Vec::new();
    let mut retained: Option<Reason> = None;
    let mut typed_by_member = false;
    for access in reads {
        if !access.direct || !replaceable(access.node) || erased_computed_name(access.node) {
            retained.get_or_insert(Reason::UnsupportedSyntax);
            continue;
        }
        if !site_initialized(ctx, shared, declared, access) {
            retained.get_or_insert(Reason::ModuleInitialization);
            continue;
        }
        if !unnarrowed(ctx, access.node, declared_type)
            || asi_hazard(&ctx.files[access.file].text, access.node)
            || condition_hazard(access.node, &definition.value)
        {
            retained.get_or_insert(Reason::UnsupportedSyntax);
            continue;
        }
        let ty = match &printed {
            Some(text) => text.clone(),
            None => {
                typed_by_member = true;
                let receiver = ctx.text(access.file, access.node.expression()).to_owned();
                member_type(&receiver, &definition.name)
            }
        };
        edits.push(FileEdit::replace(
            &ctx.files[access.file].name,
            Ctx::start(access.node),
            access.node.end() as usize,
            render(access.node, &definition.value, &ty, fresh),
        ));
    }
    if let Some(reason) = retained {
        out.skips.push(skip(ctx, reason));
    }
    tracing::trace!(
        pass = %PASS,
        sites = edits.len(),
        retained = ?retained,
        typed_by_member,
        removable = removal.is_some(),
        "constant property planned"
    );
    if edits.is_empty() {
        return;
    }
    if retained.is_none()
        && !typed_by_member
        && let Some(removal) = removal
    {
        edits.push(removal);
    }
    let mut candidate = Candidate::new(PASS, key.clone(), ctx.location(location.0, location.1));
    candidate.edits = edits;
    out.candidates.push(candidate);
}

/// Initialization order of a read: class members use their own ordering;
/// everything else the declaring statement.
fn site_initialized(ctx: &mut Ctx, shared: &Shared, declared: &Declared, access: Access) -> bool {
    if declared.statement.kind() != SyntaxKind::ClassDeclaration || access.file != declared.file {
        return init_safe(ctx, shared.graph, declared, access.file, access.node);
    }
    let class = declared.statement;
    let mut crossed = false;
    let mut node = access.node;
    while node.is_some() && node.parent() != class {
        crossed |= is_function_boundary(node);
        node = node.parent();
    }
    if node.is_nil() {
        // Outside the class body.
        return init_safe(ctx, shared.graph, declared, access.file, access.node);
    }
    crossed |= is_function_boundary(node);
    let member = node;
    let field = declared.member;
    if !crossed {
        // A later static initializer runs after the field was defined.
        return member.kind() == SyntaxKind::PropertyDeclaration
            && is_static(member)
            && member.pos() >= field.end();
    }
    // A method runs only when called: nothing before the field may run user
    // code, so no call can happen before the field is defined.
    !shared.graph.cyclic(declared.file)
        && inert_prefix(ctx, declared.file, class)
        && class_inert(ctx, class, Some(field))
}

fn plan_class(ctx: &mut Ctx, shared: &Shared, file: usize, class: Node, out: &mut Plan) {
    if class.flags().intersects(NodeFlags::Ambient)
        || has_syntactic_modifier(class, ModifierFlags::Ambient | ModifierFlags::Abstract)
    {
        return;
    }
    let mut fields = Vec::new();
    for member in class.members().iter() {
        if member.kind() != SyntaxKind::PropertyDeclaration
            || !is_static(member)
            || member.initializer().is_nil()
            || has_syntactic_modifier(
                member,
                ModifierFlags::Ambient | ModifierFlags::Abstract | ModifierFlags::Accessor,
            )
            || !matches!(
                member.name().kind(),
                SyntaxKind::Identifier | SyntaxKind::StringLiteral
            )
            || SPECIAL_NAMES.contains(&member.name().text())
        {
            continue;
        }
        if let Some(value) = literal_value(ctx, file, member.initializer()) {
            fields.push(Definition {
                name: member.name().text().to_owned(),
                node: member,
                value,
            });
        }
    }
    if fields.is_empty() {
        return;
    }
    let name = class.name().text().to_owned();
    let key = format!("static-class:{}:{}", ctx.files[file].name, name);
    let skip = |ctx: &Ctx, reason| ctx.skip(PASS, key.clone(), file, class, reason);
    // Inheritance, decorators and `this` in static code give other paths to
    // the static fields than the indexed references.
    let static_this = class.members().iter().any(|member| {
        (is_static(member) || member.kind() == SyntaxKind::ClassStaticBlockDeclaration)
            && contains_this(member)
    });
    let reason = if !class.heritage_clauses().nodes().is_empty()
        && class
            .heritage_clauses()
            .nodes()
            .iter()
            .any(|c| c.token() == SyntaxKind::ExtendsKeyword)
    {
        Some(Reason::Inheritance)
    } else if !class.decorators().is_empty()
        || class.members().iter().any(|m| !m.decorators().is_empty())
    {
        Some(Reason::Decorator)
    } else if static_this {
        Some(Reason::ObservableOperation)
    } else {
        None
    };
    if let Some(reason) = reason {
        out.skips.push(skip(ctx, reason));
        return;
    }
    let symbol = ctx.resolve(class.name());
    if symbol.is_nil()
        || ctx
            .checker
            .sym(symbol)
            .flags
            .intersects(SymbolFlags::Interface | SymbolFlags::ValueModule)
    {
        out.skips.push(skip(ctx, Reason::DeclarationMerging));
        return;
    }
    // Static calls pass the class as `this`; with no `this` in static code
    // a call through an own static method cannot reach the class otherwise.
    let own_methods: HashSet<String> = class
        .members()
        .iter()
        .filter(|m| {
            is_static(*m)
                && (m.kind() == SyntaxKind::MethodDeclaration
                    || (m.kind() == SyntaxKind::PropertyDeclaration
                        && m.initializer().is_some()
                        && matches!(
                            skip_outer(m.initializer()).kind(),
                            SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression
                        )))
                && matches!(
                    m.name().kind(),
                    SyntaxKind::Identifier | SyntaxKind::StringLiteral
                )
        })
        .map(|m| m.name().text().to_owned())
        .collect();
    let Some(uses) = binding_uses(ctx, shared, symbol, out, &own_methods, skip) else {
        return;
    };
    if uses.reads.is_empty() {
        return;
    }
    let declared = Declared {
        file,
        statement: class,
        self_inert: class_inert(ctx, class, None),
        member: Node::NIL,
    };
    for field in fields {
        let property = ctx.checker.get_symbol_of_declaration(field.node);
        if property.is_nil() {
            continue;
        }
        // Every reference of the field (including `this.X` and type
        // queries) must be one of the replaced reads before it can go.
        let reads: HashSet<Node> = uses
            .reads
            .iter()
            .filter(|(n, _)| n == &field.name)
            .map(|(_, a)| a.node)
            .collect();
        let only_replaced_reads = ctx.refs(property).iter().all(|r| {
            r.kind == super::super::analysis::RefKind::Declaration
                || reads.contains(&r.node.parent())
        });
        let removable =
            only_replaced_reads && !uses.shape_observed && !uses.member_typed(&field.name);
        let removal = removable.then(|| {
            FileEdit::delete(
                &ctx.files[file].name,
                Ctx::start(field.node),
                field.node.end() as usize,
            )
        });
        let property_key = format!("static:{}:{}.{}", ctx.files[file].name, name, field.name);
        let declared = Declared {
            member: field.node,
            ..declared
        };
        plan_property(
            ctx,
            shared,
            &uses,
            &declared,
            &field,
            property,
            property_key,
            (file, field.node),
            removal,
            out,
        );
    }
}
