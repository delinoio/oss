// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `finite-key-rewriting`: resolves computed property accesses whose key has
//! a proven finite runtime value.
//!
//! Structural phase (this planner): a computed access `receiver[key]` whose
//! key is a singleton becomes the static access `receiver.name`:
//! - a string literal key (`o["name"]`), or
//! - a reference to a `const` binding initialized with a string literal.
//!
//! The rewrite reads the same property through the same receiver, so it
//! preserves getters, prototype lookup, `this`, evaluation order and
//! exceptions. Proof obligations:
//! - Runtime provenance: the key value comes from source literals, never from a
//!   type. A `const` key binding must be application TypeScript, not ambient,
//!   and initialized before every rewritten access can run: a module-level
//!   declaration preceded only by inert statements in a module outside every
//!   import cycle, or an earlier declaration in the same function. A key whose
//!   type is a finite literal set without such provenance (for example a
//!   `keyof` parameter) is skipped as `unproven_runtime_fact`.
//! - Static spelling: the name is an ASCII identifier and not a protocol name
//!   such as `__proto__`; the receiver is not a numeric literal or `super`.
//! - Type stability: the checker resolves the name to a declared, public
//!   property of the receiver's type, declared only in application TypeScript
//!   (no index signature, `any`, private or external member), so the static
//!   form type-checks like the computed one.
//!
//! When every reference of a key binding is rewritten and
//! `residual-dependency-removal` is enabled, the bundle also deletes the
//! unexported, now unused declaration and records that pass as cleanup.
//!
//! Property phase: multi-key finite sets (conditionals, joins, parameters
//! of internal functions, fields) are coordinated with property renaming by
//! the shared flow analysis, which renames the key literals together with
//! the properties they select. That coordination only runs while this pass
//! is enabled and is recorded in the rename bundle's `uses`.
use std::collections::{BTreeMap, HashMap, HashSet};

use anyhow::Result;

use super::super::{
    analysis::{Ctx, RefKind, skip_outer},
    flow::SPECIAL_NAMES,
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::FiniteKeyRewriting;

fn identifier_name(text: &str) -> bool {
    let mut chars = text.chars();
    chars
        .next()
        .is_some_and(|c| c.is_ascii_alphabetic() || c == '_' || c == '$')
        && chars.all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '$')
        && !SPECIAL_NAMES.contains(&text)
}

/// The key expression without parentheses. A type assertion on a key
/// forces a type-level key set and keeps the access computed.
fn unparenthesized(mut node: Node) -> Node {
    while node.kind() == SyntaxKind::ParenthesizedExpression {
        node = node.expression();
    }
    node
}

fn string_literal(node: Node) -> Option<&'static str> {
    let node = skip_outer(node);
    matches!(
        node.kind(),
        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
    )
    .then(|| node.text())
}

fn function_like(node: Node) -> bool {
    matches!(
        node.kind(),
        SyntaxKind::FunctionDeclaration
            | SyntaxKind::FunctionExpression
            | SyntaxKind::ArrowFunction
            | SyntaxKind::MethodDeclaration
            | SyntaxKind::GetAccessor
            | SyntaxKind::SetAccessor
            | SyntaxKind::Constructor
            | SyntaxKind::ClassStaticBlockDeclaration
            | SyntaxKind::PropertyDeclaration
    )
}

/// Innermost function-like container, or the source file.
fn scope(mut node: Node) -> Node {
    node = node.parent();
    while node.is_some() && node.kind() != SyntaxKind::SourceFile && !function_like(node) {
        node = node.parent();
    }
    node
}

/// Innermost container that may re-run its body (function or loop).
fn runner(mut node: Node) -> Node {
    node = node.parent();
    while node.is_some()
        && !matches!(
            node.kind(),
            SyntaxKind::SourceFile
                | SyntaxKind::ForStatement
                | SyntaxKind::ForInStatement
                | SyntaxKind::ForOfStatement
                | SyntaxKind::WhileStatement
                | SyntaxKind::DoStatement
        )
        && !function_like(node)
    {
        node = node.parent();
    }
    node
}

/// Modules that belong to a static import cycle. Their top-level bindings
/// may be read before initialization by another module of the cycle.
fn cyclic_modules(ctx: &mut Ctx) -> HashSet<usize> {
    let mut graph: Vec<Vec<usize>> = vec![Vec::new(); ctx.files.len()];
    for file in 0..ctx.files.len() {
        let statements: Vec<Node> = ctx.files[file].node.statements().iter().collect();
        for statement in statements {
            if !matches!(
                statement.kind(),
                SyntaxKind::ImportDeclaration | SyntaxKind::ExportDeclaration
            ) {
                continue;
            }
            // `import type` and `export type` are erased and never evaluate
            // the target module.
            let type_only = if statement.kind() == SyntaxKind::ImportDeclaration {
                let clause = statement.import_clause();
                clause.is_some() && clause.is_type_only()
            } else {
                statement.is_type_only()
            };
            let specifier = statement.module_specifier();
            if type_only || specifier.is_nil() {
                continue;
            }
            let module = ctx.checker.get_symbol_at_location(specifier);
            if module.is_nil() {
                continue;
            }
            let declarations = ctx.checker.sym(module).declarations.to_vec();
            if let Some(&declaration) = declarations.first()
                && let Some(target) = ctx.file_of_node(declaration)
            {
                graph[file].push(target);
            }
        }
    }
    // Tarjan's strongly connected components, iterative.
    let n = graph.len();
    let mut index = vec![usize::MAX; n];
    let mut low = vec![0; n];
    let mut on_stack = vec![false; n];
    let mut stack = Vec::new();
    let mut counter = 0;
    let mut cyclic = HashSet::new();
    for start in 0..n {
        if index[start] != usize::MAX {
            continue;
        }
        let mut frames = vec![(start, 0usize)];
        index[start] = counter;
        low[start] = counter;
        counter += 1;
        stack.push(start);
        on_stack[start] = true;
        while let Some(&mut (node, ref mut next)) = frames.last_mut() {
            if *next < graph[node].len() {
                let target = graph[node][*next];
                *next += 1;
                if target == node {
                    cyclic.insert(node);
                } else if index[target] == usize::MAX {
                    index[target] = counter;
                    low[target] = counter;
                    counter += 1;
                    stack.push(target);
                    on_stack[target] = true;
                    frames.push((target, 0));
                } else if on_stack[target] {
                    low[node] = low[node].min(index[target]);
                }
                continue;
            }
            frames.pop();
            if let Some(&(parent, _)) = frames.last() {
                low[parent] = low[parent].min(low[node]);
            }
            if low[node] == index[node] {
                let mut component = Vec::new();
                while let Some(member) = stack.pop() {
                    on_stack[member] = false;
                    component.push(member);
                    if member == node {
                        break;
                    }
                }
                if component.len() > 1 {
                    cyclic.extend(component);
                }
            }
        }
    }
    cyclic
}

/// A top-level statement that runs no user code and reads no binding that
/// could be uninitialized.
fn inert(ctx: &mut Ctx, statement: Node) -> bool {
    if statement.flags().intersects(NodeFlags::Ambient)
        || has_syntactic_modifier(statement, ModifierFlags::Ambient)
    {
        return true;
    }
    match statement.kind() {
        // Imports evaluate before the module body; outside import cycles
        // they cannot call back into it.
        SyntaxKind::ImportDeclaration
        | SyntaxKind::ExportDeclaration
        | SyntaxKind::InterfaceDeclaration
        | SyntaxKind::TypeAliasDeclaration
        | SyntaxKind::FunctionDeclaration
        | SyntaxKind::EmptyStatement => true,
        SyntaxKind::VariableStatement => {
            let declarations: Vec<Node> = statement
                .declaration_list()
                .declarations()
                .nodes()
                .iter()
                .collect();
            declarations
                .into_iter()
                .all(|d| d.initializer().is_nil() || ctx.is_pure(d.initializer()))
        }
        _ => false,
    }
}

/// A `const` key binding with a string-literal initializer.
struct KeyBinding {
    file: usize,
    declaration: Node,
    name: &'static str,
    /// Top-level declaration in a module outside import cycles whose
    /// preceding statements are inert.
    module_initialized: bool,
}

fn key_binding(ctx: &mut Ctx, cyclic: &HashSet<usize>, symbol: SymbolId) -> Option<KeyBinding> {
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    let [declaration] = declarations.as_slice() else {
        return None;
    };
    let declaration = *declaration;
    if declaration.kind() != SyntaxKind::VariableDeclaration
        || declaration.name().kind() != SyntaxKind::Identifier
        || !declaration.parent().flags().intersects(NodeFlags::Const)
        || declaration.flags().intersects(NodeFlags::Ambient)
        || declaration.initializer().is_nil()
        || declaration.type_().is_some()
    {
        return None;
    }
    let name = string_literal(declaration.initializer())?;
    let file = ctx.file_of_node(declaration)?;
    if ctx.files[file].js {
        return None;
    }
    let statement = declaration.parent().parent();
    let source = ctx.files[file].node;
    let module_initialized = statement.kind() == SyntaxKind::VariableStatement
        && statement.parent() == source
        && is_external_module(source)
        && !cyclic.contains(&file)
        && {
            let statements: Vec<Node> = source.statements().iter().collect();
            let position = statements.iter().position(|&s| s == statement)?;
            statements[..position]
                .iter()
                .all(|&previous| inert(ctx, previous))
        };
    Some(KeyBinding {
        file,
        declaration,
        name,
        module_initialized,
    })
}

/// Whether the binding is initialized whenever `access` evaluates.
fn initialized_at(binding: &KeyBinding, access: Node, access_file: usize) -> bool {
    if binding.module_initialized {
        if access_file != binding.file {
            // The importer runs after the declaring module, which is
            // outside every cycle.
            return true;
        }
        // Same module: functions run after module initialization reaches
        // them (no inert prefix calls them); top-level code runs in order.
        return scope(access).kind() != SyntaxKind::SourceFile
            || access.pos() >= binding.declaration.end();
    }
    access_file == binding.file
        && runner(binding.declaration) == runner(access)
        && scope(binding.declaration) == scope(access)
        && access.pos() >= binding.declaration.end()
}

/// Application-owned, public, declared property of the receiver type.
fn owned_property(ctx: &mut Ctx, receiver: Node, name: &str) -> Result<(), Reason> {
    let ty = ctx.checker.get_type_at_location(receiver);
    if ty.is_nil() {
        return Err(Reason::UnprovenRuntimeFact);
    }
    let flags = ctx.checker.ty(ty).flags();
    if flags.intersects(TypeFlags::Any | TypeFlags::Unknown) {
        return Err(Reason::ExternalBoundary);
    }
    let apparent = ctx.checker.get_apparent_type(ty);
    let property = ctx.checker.get_property_of_type(apparent, name);
    if property.is_nil() {
        return Err(Reason::DynamicAccess);
    }
    let declarations = ctx.checker.sym(property).declarations.to_vec();
    if declarations.is_empty() {
        return Err(Reason::TypeConstraint);
    }
    for declaration in declarations {
        let application = ctx
            .file_of_node(declaration)
            .is_some_and(|file| !ctx.files[file].js);
        if !application || declaration.flags().intersects(NodeFlags::Ambient) {
            return Err(Reason::ExternalBoundary);
        }
        if get_combined_modifier_flags(declaration)
            .intersects(ModifierFlags::Private | ModifierFlags::Protected)
            || (declaration.name().is_some()
                && declaration.name().kind() == SyntaxKind::PrivateIdentifier)
        {
            return Err(Reason::ObservableOperation);
        }
    }
    Ok(())
}

/// Replacement of `[key]` (and `?.[key]`) by `.name` (`?.name`).
fn rewrite(ctx: &Ctx, file: usize, access: Node, name: &str) -> FileEdit {
    let receiver_end = access.expression().end() as usize;
    let prefix = if access.question_dot_token().is_some() {
        "?."
    } else {
        "."
    };
    FileEdit::replace(
        &ctx.files[file].name,
        receiver_end,
        access.end() as usize,
        format!("{prefix}{name}"),
    )
}

/// A `const` binding whose initializer only yields string literals.
fn literal_set(ctx: &mut Ctx, symbol: SymbolId) -> bool {
    fn literals(node: Node) -> bool {
        let node = skip_outer(node);
        string_literal(node).is_some()
            || (node.kind() == SyntaxKind::ConditionalExpression
                && literals(node.when_true())
                && literals(node.when_false()))
    }
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    matches!(declarations.as_slice(), [d] if d.kind() == SyntaxKind::VariableDeclaration
        && d.parent().flags().intersects(NodeFlags::Const)
        && d.initializer().is_some()
        && literals(d.initializer()))
}

/// A literal type or union of string literal types: a type-level finite key
/// set without runtime provenance.
fn finite_type(ctx: &mut Ctx, key: Node) -> bool {
    let ty = ctx.checker.get_type_at_location(key);
    if ty.is_nil() {
        return false;
    }
    let flags = ctx.checker.ty(ty).flags();
    if flags.intersects(TypeFlags::StringLiteral) {
        return true;
    }
    flags.intersects(TypeFlags::Union)
        && ctx.checker.ty(ty).types_list().iter().all(|&member| {
            ctx.checker
                .ty(member)
                .flags()
                .intersects(TypeFlags::StringLiteral)
        })
}

pub(in crate::compiler) fn plan(ctx: &mut Ctx, enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let cyclic = cyclic_modules(ctx);
    let mut literal = 0usize;
    let mut by_binding = BTreeMap::<(usize, usize), (KeyBinding, Vec<(usize, Node)>)>::new();
    let mut bindings = HashMap::<SymbolId, Option<(usize, usize)>>::new();
    for file in 0..ctx.files.len() {
        crate::app::check_cancelled()?;
        if ctx.files[file].js {
            continue;
        }
        for index in 0..ctx.files[file].nodes.len() {
            let access = ctx.files[file].nodes[index];
            if access.kind() != SyntaxKind::ElementAccessExpression {
                continue;
            }
            let receiver = access.expression();
            if matches!(
                skip_outer(receiver).kind(),
                SyntaxKind::NumericLiteral | SyntaxKind::SuperKeyword
            ) {
                continue;
            }
            let key = unparenthesized(access.argument_expression());
            if let Some(name) = string_literal(key) {
                if !identifier_name(name) {
                    continue;
                }
                let skip_key =
                    format!("finite-key:{}:{}", ctx.files[file].name, Ctx::start(access));
                if let Err(reason) = owned_property(ctx, receiver, name) {
                    if reason != Reason::DynamicAccess {
                        out.skips
                            .push(ctx.skip(PASS, skip_key, file, access, reason));
                    }
                    continue;
                }
                let mut candidate = Candidate::new(PASS, skip_key, ctx.location(file, access));
                candidate.edits.push(rewrite(ctx, file, access, name));
                out.candidates.push(candidate);
                literal += 1;
                continue;
            }
            if key.kind() != SyntaxKind::Identifier {
                continue;
            }
            let symbol = ctx.resolve(key);
            if symbol.is_nil() {
                continue;
            }
            let entry = match bindings.get(&symbol) {
                Some(entry) => *entry,
                None => {
                    let entry = key_binding(ctx, &cyclic, symbol).map(|binding| {
                        let id = (binding.file, Ctx::start(binding.declaration));
                        by_binding.entry(id).or_insert((binding, Vec::new()));
                        id
                    });
                    bindings.insert(symbol, entry);
                    entry
                }
            };
            let Some(id) = entry else {
                // Literal sets with runtime provenance are coordinated with
                // renaming in the property phase; a type-level key set alone
                // is never runtime evidence.
                if !literal_set(ctx, symbol) && finite_type(ctx, key) {
                    out.skips.push(ctx.skip(
                        PASS,
                        format!("finite-key:{}:{}", ctx.files[file].name, Ctx::start(access)),
                        file,
                        access,
                        Reason::UnprovenRuntimeFact,
                    ));
                }
                continue;
            };
            by_binding.get_mut(&id).unwrap().1.push((file, access));
        }
    }
    let mut bound = 0usize;
    for ((declaration_file, start), (binding, accesses)) in by_binding {
        let key = format!(
            "finite-key-binding:{}:{start}",
            ctx.files[declaration_file].name
        );
        let mut candidate = Candidate::new(
            PASS,
            key.clone(),
            ctx.location(declaration_file, binding.declaration),
        );
        let name = binding.name;
        let mut rewritten = HashSet::new();
        for &(file, access) in &accesses {
            let reason = if !identifier_name(name) {
                Some(Reason::UnsupportedSyntax)
            } else if !initialized_at(&binding, access, file) {
                Some(Reason::ModuleInitialization)
            } else {
                owned_property(ctx, access.expression(), name).err()
            };
            match reason {
                None => {
                    candidate.edits.push(rewrite(ctx, file, access, name));
                    rewritten.insert(unparenthesized(access.argument_expression()));
                }
                Some(Reason::DynamicAccess) => {}
                Some(reason) => out.skips.push(ctx.skip(
                    PASS,
                    format!("finite-key:{}:{}", ctx.files[file].name, Ctx::start(access)),
                    file,
                    access,
                    reason,
                )),
            }
        }
        if candidate.edits.is_empty() {
            continue;
        }
        // Delete the declaration when no other reference remains.
        let symbol = ctx.resolve(binding.declaration.name());
        let statement = binding.declaration.parent().parent();
        let removable = enabled.contains(PassId::ResidualDependencyRemoval)
            && ctx.is_internal(symbol)
            && statement.kind() == SyntaxKind::VariableStatement
            && statement.declaration_list().declarations().nodes().len() == 1
            && !has_syntactic_modifier(statement, ModifierFlags::Export)
            && matches!(
                statement.parent().kind(),
                SyntaxKind::SourceFile | SyntaxKind::Block
            )
            && ctx.refs(symbol).iter().all(|r| match r.kind {
                RefKind::Declaration => r.node == binding.declaration.name(),
                RefKind::Read => rewritten.contains(&r.node),
                _ => false,
            });
        if removable {
            candidate.edits.push(FileEdit::delete(
                &ctx.files[declaration_file].name,
                Ctx::start(statement),
                statement.end() as usize,
            ));
            candidate.uses.push(PassId::ResidualDependencyRemoval);
        }
        bound += 1;
        out.candidates.push(candidate);
    }
    tracing::debug!(
        literal,
        bindings = bound,
        cyclic_modules = cyclic.len(),
        "finite computed keys resolved"
    );
    Ok(())
}
