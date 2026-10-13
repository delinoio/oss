// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `enum-runtime-reduction`: replaces member reads `E.A` of source-backed
//! enums with their constant values and, when no runtime use of the enum
//! object remains, removes the enum object together with its reverse
//! mappings by turning the declaration into an ambient `declare enum` (types
//! are unchanged; nothing is emitted).
//!
//! Proof obligations, all from runtime evidence; types only nominate:
//! - The enum is a single non-ambient, non-`const` declaration at module top
//!   level in application TypeScript (no declaration or namespace merging), is
//!   not observable through public entry exports, JavaScript or an escaping
//!   module namespace object.
//! - Member values come from the compiler's constant evaluation of the
//!   initializers (finite numbers and strings only).
//! - Every runtime reference of the enum binding is classified. Any escape to
//!   unknown code (passing the object, calls through members, aliases) or a
//!   computed-key write prevents all inlining, because the object could be
//!   mutated; static member writes block that member.
//! - Each replaced read executes after the enum initialized (same module and
//!   later at top level, a function of an acyclic module whose preceding
//!   statements run no code, or a module outside the import cycle), so no
//!   `TypeError` from reading an uninitialized enum is lost.
//! - The replacement `(<value> as (typeof E)["A"])!` has exactly the original
//!   type, including literal freshness, and emits only the literal.
//! - The object is removed only when every runtime read is replaced and the
//!   object is not otherwise observed (dynamic or reverse lookup, enumeration,
//!   identity, `typeof`, re-export by name).
use anyhow::Result;

use super::{
    super::analysis::Ctx,
    constants_support::{
        Declared, ModuleGraph, asi_hazard, classify, condition_hazard, erased_computed_name,
        escaped_exports, init_safe, member_type, number_text, object_builtins_intact, render,
        replaceable, string_text, unnarrowed,
    },
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::EnumRuntimeReduction;

struct Member {
    name: String,
    /// Source text of the member name.
    name_text: String,
    /// JavaScript literal of the constant value.
    value: Option<String>,
    symbol: SymbolId,
}

pub(in crate::compiler) fn plan(ctx: &mut Ctx, _enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let mut enums = Vec::new();
    for (file, info) in ctx.files.iter().enumerate() {
        if info.js {
            continue;
        }
        let statements: Vec<Node> = info.node.statements().iter().collect();
        enums.extend(
            statements
                .into_iter()
                .filter(|s| s.kind() == SyntaxKind::EnumDeclaration)
                .map(|s| (file, s)),
        );
    }
    if enums.is_empty() {
        return Ok(());
    }
    let graph = ModuleGraph::build(ctx);
    let escaped = escaped_exports(ctx);
    let object_builtins = object_builtins_intact(ctx);
    let (mut inlined, mut removed) = (0usize, 0usize);
    for (file, declaration) in enums {
        crate::app::check_cancelled()?;
        if let Some((sites, removal)) = plan_enum(
            ctx,
            &graph,
            &escaped,
            object_builtins,
            file,
            declaration,
            out,
        ) {
            inlined += sites;
            removed += usize::from(removal);
        }
    }
    tracing::debug!(
        pass = %PASS,
        inlined_sites = inlined,
        removed_objects = removed,
        "enum reduction planned"
    );
    Ok(())
}

fn plan_enum(
    ctx: &mut Ctx,
    graph: &ModuleGraph,
    escaped: &std::collections::HashSet<SymbolId>,
    object_builtins: bool,
    file: usize,
    declaration: Node,
    out: &mut Plan,
) -> Option<(usize, bool)> {
    if declaration.flags().intersects(NodeFlags::Ambient)
        || has_syntactic_modifier(declaration, ModifierFlags::Ambient | ModifierFlags::Const)
    {
        // Ambient enums have no trusted runtime object; const enums are
        // already inlined by the compiler.
        return None;
    }
    let name = declaration.name().text().to_owned();
    let key = format!("enum:{}:{}", ctx.files[file].name, name);
    let skip = |ctx: &Ctx, reason| ctx.skip(PASS, key.clone(), file, declaration, reason);
    let symbol = ctx.resolve(declaration.name());
    if symbol.is_nil() {
        return None;
    }
    let flags = ctx.checker.sym(symbol).flags;
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    if declarations.len() != 1
        || flags.intersects(SymbolFlags::ValueModule | SymbolFlags::NamespaceModule)
    {
        // One record per merged symbol.
        if declarations.first() == Some(&declaration) {
            out.skips.push(skip(ctx, Reason::DeclarationMerging));
        }
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
    if escaped.contains(&symbol) {
        out.skips.push(skip(ctx, Reason::Escapes));
        return None;
    }
    let mut members = Vec::new();
    for member in declaration.members().iter() {
        let member_name = member.name();
        if !matches!(
            member_name.kind(),
            SyntaxKind::Identifier | SyntaxKind::StringLiteral
        ) {
            out.skips.push(skip(ctx, Reason::UnsupportedSyntax));
            return None;
        }
        let value = match ctx.checker.get_constant_value(member) {
            Some(LiteralValue::Number(number)) => number_text(number.0),
            Some(LiteralValue::String(text)) => string_text(text),
            _ => None,
        };
        members.push(Member {
            name: member_name.text().to_owned(),
            name_text: ctx.text(file, member_name).to_owned(),
            value,
            symbol: ctx.checker.get_symbol_of_declaration(member),
        });
    }
    let uses = classify(ctx, symbol, object_builtins);
    if let Some(reason) = uses.first_blocker() {
        out.skips.push(skip(ctx, reason));
        return None;
    }
    let declared = Declared {
        file,
        statement: declaration,
        self_inert: true,
        member: Node::NIL,
    };
    let mut edits = Vec::new();
    // A runtime read that stays keeps the enum object alive.
    let mut retained: Option<Reason> = None;
    for (member_name, access) in &uses.reads {
        let access = *access;
        let Some(member) = members.iter().find(|m| &m.name == member_name) else {
            // Prototype or missing members (`E.toString`).
            retained.get_or_insert(Reason::ObservableOperation);
            continue;
        };
        if erased_computed_name(access.node) || folded_initializer(ctx, file, access.node) {
            continue;
        }
        let Some(value) = member.value.as_deref() else {
            retained.get_or_insert(Reason::NotConstant);
            continue;
        };
        if uses.writes.contains(member_name) {
            retained.get_or_insert(Reason::Mutation);
            continue;
        }
        if !access.direct || !replaceable(access.node) {
            retained.get_or_insert(Reason::UnsupportedSyntax);
            continue;
        }
        if !init_safe(ctx, graph, &declared, access.file, access.node) {
            retained.get_or_insert(Reason::ModuleInitialization);
            continue;
        }
        let declared_type = ctx.checker.get_type_of_symbol(member.symbol);
        if !unnarrowed(ctx, access.node, declared_type)
            || asi_hazard(&ctx.files[access.file].text, access.node)
            || condition_hazard(access.node, value)
        {
            retained.get_or_insert(Reason::UnsupportedSyntax);
            continue;
        }
        let receiver = ctx.text(access.file, access.node.expression()).to_owned();
        let replacement = render(
            access.node,
            value,
            &member_type(&receiver, &member.name),
            true,
        );
        edits.push(FileEdit::replace(
            &ctx.files[access.file].name,
            Ctx::start(access.node),
            access.node.end() as usize,
            replacement,
        ));
    }
    let sites = edits.len();
    let blocker = retained
        .or_else(|| uses.reexported.then_some(Reason::PublicExport))
        .or_else(|| uses.shape_observed.then_some(Reason::Reflection))
        .or_else(|| uses.binding_observed.then_some(Reason::ObservableIdentity))
        .or_else(|| {
            members
                .iter()
                .any(|m| m.value.is_none())
                .then_some(Reason::NotConstant)
        });
    let removal = blocker.is_none();
    if removal {
        let export = if has_syntactic_modifier(declaration, ModifierFlags::Export) {
            "export "
        } else {
            ""
        };
        let body: Vec<String> = members
            .iter()
            .map(|m| {
                format!(
                    "{} = {}",
                    m.name_text,
                    m.value.as_deref().unwrap_or_default()
                )
            })
            .collect();
        edits.push(FileEdit::replace(
            &ctx.files[file].name,
            Ctx::start(declaration),
            declaration.end() as usize,
            format!("{export}declare enum {name} {{ {} }}", body.join(", ")),
        ));
    } else if let Some(reason) = blocker {
        out.skips.push(skip(ctx, reason));
    }
    if edits.is_empty() {
        return None;
    }
    let mut candidate = Candidate::new(PASS, key.clone(), ctx.location(file, declaration));
    candidate.edits = edits;
    out.candidates.push(candidate);
    Some((sites, removal))
}

/// A read inside a constant initializer of an enum member in the same
/// module. The emitter folds such initializers to literals, so the read is
/// not a runtime use and the reference stays valid against an ambient enum.
fn folded_initializer(ctx: &mut Ctx, file: usize, access: Node) -> bool {
    let mut node = access;
    while node.is_some() {
        if node.kind() == SyntaxKind::EnumMember {
            return ctx.file_of_node(node) == Some(file)
                && ctx.checker.get_constant_value(node).is_some();
        }
        if super::constants_support::is_function_boundary(node) {
            return false;
        }
        node = node.parent();
    }
    false
}
