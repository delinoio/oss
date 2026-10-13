// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `unused-method-removal`: deletes instance and static methods and
//! accessors of application classes that no code can reach.
//!
//! Class methods and accessors are non-enumerable prototype or constructor
//! properties, so outside reflection on non-enumerable keys they are only
//! reachable by spelling their name. The proof therefore works on names and
//! on the receivers that could inspect keys dynamically:
//! - `#private` members are lexically scoped: they are unused when the class
//!   body mentions the private name only in the member's own declarations and
//!   bodies.
//! - Other members are unused when no application TypeScript or JavaScript
//!   mentions the name anywhere else: no property access, string literal,
//!   destructuring key, object key, JSX attribute or declaration of another
//!   class, interface or type (so overrides, `super.m()`, interface
//!   requirements, callback extraction and structural uses stay), except
//!   recursion inside the member itself. A name that external declarations
//!   (libraries, lib files, ambient project types) or runtime protocols
//!   (`then`, `toJSON`, `toString`, `handleEvent`, iteration, disposal, ...)
//!   use stays, because external code may call it by name.
//! - The class is internal: not a public entry export, not referenced from
//!   JavaScript and not exposed through an observable module namespace.
//! - No computed-key access, `in` test, `for...in` or reflective call
//!   (`Object.getOwnPropertyNames`, `Reflect.*`, ...) can receive the class,
//!   its prototype or an instance. Receivers are nominated by their static type
//!   before assertions; `any`, `unknown`, type parameters and object types that
//!   the instance or constructor type is assignable to may hold the class.
//! - Decorated classes and members and abstract members stay.
use std::collections::{BTreeMap, HashMap};

use anyhow::Result;

use super::{
    super::{
        analysis::{Ctx, RefKind},
        token_start,
    },
    deadcode_support::{
        DynamicSites, Exposure, Keys, Mentions, PROTOCOL_NAMES, TypeFlows, class_name,
        class_symbol, removal_start, static_name, within,
    },
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::UnusedMethodRemoval;

pub(in crate::compiler) fn plan(ctx: &mut Ctx, _enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let mentions = Mentions::compute(ctx);
    let mut exposure = None;
    let mut sites = None;
    let mut flows = None;
    let mut observed: HashMap<SymbolId, Option<Reason>> = HashMap::new();
    let mut keys = Keys::default();
    let (mut candidates, mut skips) = (0usize, 0usize);
    for file in 0..ctx.files.len() {
        crate::app::check_cancelled()?;
        if ctx.files[file].js || !is_external_module(ctx.files[file].node) {
            continue;
        }
        let classes: Vec<Node> = ctx.files[file]
            .nodes
            .iter()
            .copied()
            .filter(|n| {
                matches!(
                    n.kind(),
                    SyntaxKind::ClassDeclaration | SyntaxKind::ClassExpression
                )
            })
            .collect();
        for class in classes {
            for (name, members) in dead_groups(ctx, &mentions, file, class) {
                let key = keys.unique(format!(
                    "method:{}:{}.{name}",
                    ctx.files[file].name,
                    class_name(class)
                ));
                let reason = if name.starts_with('#') {
                    boundary_of_members(&members, class)
                } else {
                    let exposure = exposure.get_or_insert_with(|| Exposure::compute(ctx));
                    let sites = sites.get_or_insert_with(|| DynamicSites::compute(ctx));
                    let flows = flows.get_or_insert_with(|| TypeFlows::compute(ctx));
                    boundary_of_members(&members, class)
                        .or_else(|| name_boundary(ctx, name))
                        .or_else(|| {
                            class_boundary(ctx, exposure, sites, flows, &mut observed, class)
                        })
                };
                if let Some(reason) = reason {
                    skips += 1;
                    out.skips
                        .push(ctx.skip(PASS, key, file, members[0], reason));
                    continue;
                }
                let mut candidate = Candidate::new(PASS, key, ctx.location(file, members[0]));
                let text = &ctx.files[file].text;
                for &member in &members {
                    candidate.edits.push(FileEdit::delete(
                        &ctx.files[file].name,
                        removal_start(text, member),
                        member.end() as usize,
                    ));
                }
                candidates += 1;
                out.candidates.push(candidate);
            }
        }
    }
    tracing::debug!(pass = %PASS, candidates, skipped = skips, "unused methods planned");
    Ok(())
}

/// Method and accessor groups of a class whose name is mentioned nowhere
/// but in the group's own declarations and bodies.
fn dead_groups(
    ctx: &Ctx,
    mentions: &Mentions,
    file: usize,
    class: Node,
) -> Vec<(&'static str, Vec<Node>)> {
    let mut groups: BTreeMap<usize, (&'static str, Vec<Node>)> = BTreeMap::new();
    let mut order: HashMap<&'static str, usize> = HashMap::new();
    let mut fields: Vec<&'static str> = Vec::new();
    for member in class.members().iter() {
        let Some(name) = static_name(member) else {
            continue;
        };
        match member.kind() {
            SyntaxKind::MethodDeclaration | SyntaxKind::GetAccessor | SyntaxKind::SetAccessor => {
                let next = order.len();
                let index = *order.entry(name).or_insert(next);
                groups
                    .entry(index)
                    .or_insert((name, Vec::new()))
                    .1
                    .push(member);
            }
            _ => fields.push(name),
        }
    }
    groups
        .into_values()
        .filter(|(name, members)| {
            if fields.contains(name) {
                return false;
            }
            let spans: Vec<(usize, usize)> = members
                .iter()
                .map(|&m| (token_start(m), m.end() as usize))
                .collect();
            let inside =
                |(f, n): &(usize, Node)| *f == file && spans.iter().any(|&s| within(*n, s));
            if name.starts_with('#') {
                // Private names resolve lexically inside the class body.
                let body = (token_start(class), class.end() as usize);
                !ctx.files[file].nodes.iter().any(|&n| {
                    n.kind() == SyntaxKind::PrivateIdentifier
                        && n.text() == *name
                        && within(n, body)
                        && !inside(&(file, n))
                })
            } else {
                mentions.of(name).iter().all(inside)
            }
        })
        .collect()
}

/// Boundaries of the members themselves.
fn boundary_of_members(members: &[Node], class: Node) -> Option<Reason> {
    if !class.decorators().is_empty() || members.iter().any(|m| !m.decorators().is_empty()) {
        return Some(Reason::Decorator);
    }
    if members
        .iter()
        .any(|&m| has_syntactic_modifier(m, ModifierFlags::Abstract | ModifierFlags::Ambient))
    {
        return Some(Reason::Inheritance);
    }
    if !members.iter().any(|m| m.body().is_some()) {
        return Some(Reason::UnsupportedSyntax);
    }
    None
}

/// Names that code outside the analysis may use.
fn name_boundary(ctx: &mut Ctx, name: &str) -> Option<Reason> {
    if PROTOCOL_NAMES.contains(&name) {
        return Some(Reason::SpecialProperty);
    }
    if ctx.external_names().contains(name) {
        return Some(Reason::ExternalBoundary);
    }
    None
}

/// Whether the class value itself can reach code or operations outside the
/// name-based proof.
fn class_boundary(
    ctx: &mut Ctx,
    exposure: &Exposure,
    sites: &DynamicSites,
    flows: &TypeFlows,
    observed: &mut HashMap<SymbolId, Option<Reason>>,
    class: Node,
) -> Option<Reason> {
    let symbol = class_symbol(ctx, class);
    if symbol.is_nil() {
        return Some(Reason::Escapes);
    }
    if ctx.public.contains(&symbol) {
        return Some(Reason::PublicExport);
    }
    if !ctx.is_internal(symbol) {
        return Some(Reason::ExternalBoundary);
    }
    let file = ctx.file_of_node(class)?;
    let statement = if class.kind() == SyntaxKind::ClassDeclaration {
        class
    } else {
        class.parent().parent().parent()
    };
    let exported_here =
        has_syntactic_modifier(statement, ModifierFlags::Export) && exposure.observed(file);
    let reexported = ctx.refs(symbol).iter().any(|r| {
        matches!(r.kind, RefKind::Alias | RefKind::Declaration)
            && r.node.parent().kind() == SyntaxKind::ExportSpecifier
            && exposure.observed(r.file)
    });
    if exported_here || reexported {
        return Some(Reason::Reflection);
    }
    if let Some(&reason) = observed.get(&symbol) {
        return reason;
    }
    let holders = flows.holders(ctx, class);
    let reason = sites.observes(ctx, &holders);
    observed.insert(symbol, reason);
    reason
}
