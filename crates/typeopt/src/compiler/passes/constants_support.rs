// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Shared proofs for the constant-propagation passes
//! (`enum-runtime-reduction` and `constant-property-inlining`).
//!
//! - [`ModuleGraph`]: strongly connected components of the runtime import
//!   graph. A module outside the component of a declaring module can only run
//!   after that module finished evaluating, so it never observes the
//!   declaration before initialization.
//! - [`init_safe`]: whether a read site always executes after its declaration
//!   completed, so replacing it never hides a `TypeError` or temporal-dead-zone
//!   `ReferenceError`.
//! - [`classify`]: every runtime reference of an object binding (an enum
//!   object, a `const` object literal or a class constructor), split into
//!   static member reads, member writes, member calls (which pass the object as
//!   `this`), shape/identity observations and escapes to unknown code.
//! - [`escaped_exports`]: exports of modules whose namespace object may reach
//!   unknown code, where any property could be written.
//! - Literal rendering and site typing: a replacement is `(<value> as <type>)`,
//!   erased by the emitter, where `<type>` reproduces the original expression
//!   type exactly (including literal freshness), so type checking of the
//!   surrounding code is unchanged.
use std::collections::{BTreeSet, HashSet};

use super::super::analysis::{Ctx, RefKind, skip_outer, walk_up_outer};
use crate::{compiler::api::*, plan::Reason};

/// Strongly connected components of the application import graph that
/// survives TypeScript import elision. Extra edges only make components
/// larger, which is conservative.
pub(super) struct ModuleGraph {
    component: Vec<usize>,
    cyclic: Vec<bool>,
    /// An application import could not be resolved; cross-module ordering
    /// is then unknown.
    incomplete: bool,
}

impl ModuleGraph {
    pub fn build(ctx: &mut Ctx) -> Self {
        let count = ctx.files.len();
        let options = ctx.checker.compiler_options;
        let keep_all =
            options.verbatim_module_syntax.is_true() || options.emit_decorator_metadata.is_true();
        let classic_jsx = options.jsx == JsxEmit::React;
        let mut edges = vec![Vec::new(); count];
        let mut incomplete = false;
        for file in 0..count {
            let js = ctx.files[file].js;
            let tsx = ctx.files[file].name.ends_with(".tsx");
            let statements: Vec<Node> = ctx.files[file].node.statements().iter().collect();
            for statement in statements {
                let kept = match statement.kind() {
                    SyntaxKind::ImportDeclaration => {
                        let clause = statement.import_clause();
                        if clause.is_some() && clause.is_type_only() {
                            false
                        } else {
                            js || keep_all
                                || (tsx && classic_jsx)
                                || import_kept(ctx, file, statement)
                        }
                    }
                    SyntaxKind::ExportDeclaration => {
                        statement.module_specifier().is_some() && (js || export_kept(statement))
                    }
                    _ => false,
                };
                if !kept {
                    continue;
                }
                let specifier = statement.module_specifier();
                match resolve_module(ctx, specifier) {
                    Some(target) => edges[file].push(target),
                    None => {
                        let text = specifier.text();
                        // Relative or path-mapped specifiers may name an
                        // application module the analysis cannot see.
                        if text.starts_with('.') || text.starts_with('/') || js {
                            incomplete |= !is_json_or_external(ctx, specifier);
                        }
                    }
                }
            }
        }
        let (component, cyclic) = components(&edges);
        tracing::debug!(
            modules = count,
            cyclic = cyclic.iter().filter(|&&c| c).count(),
            incomplete,
            "module graph built"
        );
        Self {
            component,
            cyclic,
            incomplete,
        }
    }

    pub fn same_component(&self, a: usize, b: usize) -> bool {
        self.incomplete || self.component[a] == self.component[b]
    }

    pub fn cyclic(&self, file: usize) -> bool {
        self.incomplete || self.cyclic[file]
    }
}

fn is_json_or_external(ctx: &mut Ctx, specifier: Node) -> bool {
    let symbol = ctx.checker.get_symbol_at_location(specifier);
    if symbol.is_nil() {
        return false;
    }
    let Some(&declaration) = ctx.checker.sym(symbol).declarations.first() else {
        return false;
    };
    let name = source_file_info(get_source_file_of_node(declaration))
        .file_name
        .clone();
    name.ends_with(".json") || ctx.file_of(&name).is_none()
}

/// Application module (TypeScript or JavaScript) a module specifier names.
pub(super) fn resolve_module(ctx: &mut Ctx, specifier: Node) -> Option<usize> {
    if specifier.is_nil() {
        return None;
    }
    let symbol = ctx.checker.get_symbol_at_location(specifier);
    if symbol.is_nil() {
        return None;
    }
    let declaration = ctx.checker.sym(symbol).declarations.first().copied()?;
    let name = source_file_info(get_source_file_of_node(declaration))
        .file_name
        .clone();
    ctx.file_of(&name)
}

/// Whether TypeScript keeps a value import declaration in emitted output: some
/// binding is used in a value position of the importing module.
fn import_kept(ctx: &mut Ctx, file: usize, statement: Node) -> bool {
    let clause = statement.import_clause();
    if clause.is_nil() {
        return true;
    }
    let mut names = Vec::new();
    if clause.name().is_some() {
        names.push(clause.name());
    }
    let bindings = clause.named_bindings();
    if bindings.is_some() {
        if bindings.kind() == SyntaxKind::NamespaceImport {
            names.push(bindings.name());
        } else {
            names.extend(
                bindings
                    .elements()
                    .iter()
                    .filter(|e| !e.is_type_only())
                    .map(|e| e.name()),
            );
        }
    }
    names.into_iter().any(|name| {
        let symbol = ctx.resolve(name);
        symbol.is_nil()
            || ctx.refs(symbol).iter().any(|r| {
                r.file == file
                    && match r.kind {
                        RefKind::Declaration | RefKind::Type => false,
                        RefKind::Alias => r.node.parent().kind() == SyntaxKind::ExportSpecifier,
                        _ => true,
                    }
            })
    })
}

fn export_kept(statement: Node) -> bool {
    if statement.is_type_only() {
        return false;
    }
    let clause = statement.export_clause();
    clause.is_nil()
        || clause.kind() == SyntaxKind::NamespaceExport
        || clause.elements().iter().any(|e| !e.is_type_only())
}

/// Iterative Tarjan: component id per node and whether it lies on a cycle.
fn components(edges: &[Vec<usize>]) -> (Vec<usize>, Vec<bool>) {
    let count = edges.len();
    let mut index = vec![usize::MAX; count];
    let mut low = vec![0; count];
    let mut on_stack = vec![false; count];
    let mut stack = Vec::new();
    let mut component = vec![usize::MAX; count];
    let mut cyclic = vec![false; count];
    let mut next = 0;
    let mut components = 0;
    for root in 0..count {
        if index[root] != usize::MAX {
            continue;
        }
        let mut work = vec![(root, 0usize)];
        while let Some(&mut (node, ref mut child)) = work.last_mut() {
            if *child == 0 {
                index[node] = next;
                low[node] = next;
                next += 1;
                stack.push(node);
                on_stack[node] = true;
            }
            if let Some(&target) = edges[node].get(*child) {
                *child += 1;
                if target == node {
                    cyclic[node] = true;
                }
                if index[target] == usize::MAX {
                    work.push((target, 0));
                } else if on_stack[target] {
                    low[node] = low[node].min(index[target]);
                }
                continue;
            }
            work.pop();
            if let Some(&(parent, _)) = work.last() {
                low[parent] = low[parent].min(low[node]);
            }
            if low[node] == index[node] {
                let mut members = Vec::new();
                loop {
                    let member = stack.pop().expect("tarjan stack");
                    on_stack[member] = false;
                    component[member] = components;
                    members.push(member);
                    if member == node {
                        break;
                    }
                }
                if members.len() > 1 {
                    for member in members {
                        cyclic[member] = true;
                    }
                }
                components += 1;
            }
        }
    }
    (component, cyclic)
}

/// Kinds whose bodies run later than the code around them.
pub(super) fn is_function_boundary(node: Node) -> bool {
    match node.kind() {
        SyntaxKind::FunctionDeclaration
        | SyntaxKind::FunctionExpression
        | SyntaxKind::ArrowFunction
        | SyntaxKind::MethodDeclaration
        | SyntaxKind::Constructor
        | SyntaxKind::GetAccessor
        | SyntaxKind::SetAccessor => true,
        // Instance field initializers run during construction.
        SyntaxKind::PropertyDeclaration => !is_static(node),
        _ => false,
    }
}

/// A declaration that initializes a binding when its statement completes.
pub(super) struct Declared {
    pub file: usize,
    /// The statement in its statement list (module, block or clause).
    pub statement: Node,
    /// Evaluating the declaration statement itself runs no user code, so no
    /// function can observe the binding during its own initialization.
    pub self_inert: bool,
    /// For a class field, the field member; reads inside the class body are
    /// ordered against it. Nil otherwise.
    pub member: Node,
}

/// Whether `site` (in `site_file`) always executes after `declared` has
/// completed.
///
/// - Same statement list, no function boundary in between: the site's statement
///   must follow the declaration statement.
/// - A function in the declaring module: the module must not lie on an import
///   cycle and no statement up to and including the declaration may run user
///   code, so the function cannot run earlier.
/// - Another module: it must lie outside the declaring module's import cycle;
///   ESM then evaluates it only after the declaring module completed.
pub(super) fn init_safe(
    ctx: &mut Ctx,
    graph: &ModuleGraph,
    declared: &Declared,
    site_file: usize,
    site: Node,
) -> bool {
    let list = declared.statement.parent();
    let top_level = list.kind() == SyntaxKind::SourceFile;
    if site_file != declared.file {
        return top_level && !graph.same_component(site_file, declared.file);
    }
    let mut crossed = false;
    let mut node = site;
    while node.is_some() {
        crossed |= is_function_boundary(node);
        if node.parent() == list {
            break;
        }
        node = node.parent();
    }
    if node.is_nil() {
        return false;
    }
    if !crossed {
        return node.pos() >= declared.statement.end();
    }
    top_level
        && declared.self_inert
        && !graph.cyclic(declared.file)
        && inert_prefix(ctx, declared.file, declared.statement)
}

/// Every module statement before `statement` evaluates without running user
/// code or reading bindings that could be uninitialized.
pub(super) fn inert_prefix(ctx: &mut Ctx, file: usize, statement: Node) -> bool {
    let statements: Vec<Node> = ctx.files[file].node.statements().iter().collect();
    for current in statements {
        if current == statement {
            return true;
        }
        if !inert_statement(ctx, current) {
            return false;
        }
    }
    false
}

fn inert_statement(ctx: &mut Ctx, statement: Node) -> bool {
    if statement.flags().intersects(NodeFlags::Ambient)
        || has_syntactic_modifier(statement, ModifierFlags::Ambient)
    {
        return true;
    }
    match statement.kind() {
        SyntaxKind::ImportDeclaration
        | SyntaxKind::ExportDeclaration
        | SyntaxKind::InterfaceDeclaration
        | SyntaxKind::TypeAliasDeclaration
        | SyntaxKind::FunctionDeclaration
        | SyntaxKind::EmptyStatement => true,
        SyntaxKind::ExportAssignment => pure_value(ctx, statement.expression()),
        SyntaxKind::VariableStatement => statement
            .declaration_list()
            .declarations()
            .nodes()
            .iter()
            .collect::<Vec<_>>()
            .into_iter()
            .all(|d| {
                d.name().kind() == SyntaxKind::Identifier
                    && (d.initializer().is_nil() || pure_value(ctx, d.initializer()))
            }),
        SyntaxKind::EnumDeclaration => statement
            .members()
            .iter()
            .all(|m| m.initializer().is_nil() || literal_initializer(skip_outer(m.initializer()))),
        SyntaxKind::ClassDeclaration => class_inert(ctx, statement, None),
        _ => false,
    }
}

/// `ctx.is_pure`, extended to signed numeric literals and object or array
/// literals of pure values.
pub(super) fn pure_value(ctx: &mut Ctx, node: Node) -> bool {
    let node = skip_outer(node);
    match node.kind() {
        SyntaxKind::PrefixUnaryExpression
            if matches!(
                node.operator(),
                SyntaxKind::MinusToken | SyntaxKind::PlusToken | SyntaxKind::TildeToken
            ) && matches!(
                skip_outer(node.operand()).kind(),
                SyntaxKind::NumericLiteral
            ) =>
        {
            true
        }
        SyntaxKind::PrefixUnaryExpression
            if node.operator() == SyntaxKind::MinusToken
                && skip_outer(node.operand()).kind() == SyntaxKind::BigIntLiteral =>
        {
            true
        }
        SyntaxKind::ObjectLiteralExpression => node.properties().iter().all(|p| match p.kind() {
            SyntaxKind::PropertyAssignment => {
                p.name().kind() != SyntaxKind::ComputedPropertyName
                    && pure_value(ctx, p.initializer())
            }
            SyntaxKind::ShorthandPropertyAssignment => ctx.is_pure(p.name()),
            SyntaxKind::MethodDeclaration => p.name().kind() != SyntaxKind::ComputedPropertyName,
            _ => false,
        }),
        SyntaxKind::ArrayLiteralExpression => node
            .elements()
            .iter()
            .all(|e| e.kind() != SyntaxKind::SpreadElement && pure_value(ctx, e)),
        _ => ctx.is_pure(node),
    }
}

fn literal_initializer(node: Node) -> bool {
    match node.kind() {
        SyntaxKind::NumericLiteral | SyntaxKind::StringLiteral => true,
        SyntaxKind::PrefixUnaryExpression => {
            node.operator() == SyntaxKind::MinusToken
                && node.operand().kind() == SyntaxKind::NumericLiteral
        }
        _ => false,
    }
}

/// Class definition (up to static member `until`, exclusive) runs no user
/// code: no decorators, heritage, computed keys or static blocks, and only
/// pure static initializers.
pub(super) fn class_inert(ctx: &mut Ctx, class: Node, until: Option<Node>) -> bool {
    if !class.decorators().is_empty()
        || class
            .heritage_clauses()
            .nodes()
            .iter()
            .any(|c| c.token() == SyntaxKind::ExtendsKeyword)
    {
        return false;
    }
    let members: Vec<Node> = class.members().iter().collect();
    for member in members {
        if Some(member) == until {
            return true;
        }
        if !member.decorators().is_empty()
            || (member.name().is_some() && member.name().kind() == SyntaxKind::ComputedPropertyName)
            || member.kind() == SyntaxKind::ClassStaticBlockDeclaration
        {
            return false;
        }
        if member.kind() == SyntaxKind::PropertyDeclaration
            && is_static(member)
            && member.initializer().is_some()
            && !pure_value(ctx, member.initializer())
        {
            return false;
        }
    }
    true
}

/// A static-name member access on the analyzed object.
#[derive(Clone, Copy, Debug)]
pub(super) struct Access {
    /// The property or element access expression.
    pub node: Node,
    pub file: usize,
    /// The receiver is exactly the object reference (no wrapper), so the
    /// access may be replaced while keeping its type.
    pub direct: bool,
}

/// Classified runtime references of one object binding.
#[derive(Default)]
pub(super) struct Uses {
    /// Static-name reads by member name.
    pub reads: Vec<(String, Access)>,
    /// Static member names that are assigned, updated or deleted.
    pub writes: BTreeSet<String>,
    /// Calls through a member: `object.name(...)` passes the object as
    /// `this`.
    pub calls: Vec<(String, Access)>,
    /// First use that lets the object reach unknown code.
    pub escape: Option<(usize, Node)>,
    /// A computed-key write may target any member.
    pub dynamic_write: bool,
    /// The set of members is observed (reflection, enumeration, dynamic
    /// reads, spread or destructuring); no member may be removed.
    pub shape_observed: bool,
    /// The binding's runtime value is observed as a whole (identity,
    /// `typeof`, truthiness or a re-export); the object must stay.
    pub binding_observed: bool,
    /// The binding is re-exported by name, which TypeScript keeps at
    /// runtime.
    pub reexported: bool,
    /// Type positions refer to the binding (`typeof X`).
    pub type_refs: bool,
    /// Type queries (`typeof X`) depend on the whole object type.
    pub type_queries: bool,
    /// Members whose type a type query reads (`typeof X.a`,
    /// `(typeof X)["a"]`).
    pub type_members: BTreeSet<String>,
}

impl Uses {
    /// Whether removing member `name` could change a type the program
    /// depends on.
    pub fn member_typed(&self, name: &str) -> bool {
        self.type_queries || self.type_members.contains(name)
    }

    pub fn first_blocker(&self) -> Option<Reason> {
        if self.escape.is_some() || !self.calls.is_empty() {
            Some(Reason::Escapes)
        } else if self.dynamic_write {
            Some(Reason::Mutation)
        } else {
            None
        }
    }
}

/// Reflection built-ins that read an object without calling into it.
const OBJECT_READERS: [&str; 7] = [
    "keys",
    "values",
    "entries",
    "getOwnPropertyNames",
    "freeze",
    "isFrozen",
    "hasOwn",
];

/// Whether the global `Object` is the built-in everywhere in the
/// application: every use of the global binding reads a member, calls or
/// constructs it, or compares it, so application code never replaces
/// `Object.keys` and friends or hands `Object` to code that could.
pub(super) fn object_builtins_intact(ctx: &mut Ctx) -> bool {
    for file in 0..ctx.files.len() {
        let nodes: Vec<Node> = ctx.files[file]
            .nodes
            .iter()
            .copied()
            .filter(|n| n.kind() == SyntaxKind::Identifier && n.text() == "Object")
            .collect();
        for node in nodes {
            if is_part_of_type_node(node) || in_type_query(node) {
                continue;
            }
            let symbol = ctx.resolve(node);
            if symbol.is_some() {
                let declarations = ctx.checker.sym(symbol).declarations.to_vec();
                if declarations.iter().any(|&d| ctx.file_of_node(d).is_some()) {
                    // An application binding named `Object`, not the global.
                    continue;
                }
            }
            let parent = node.parent();
            let expression =
                if parent.kind() == SyntaxKind::PropertyAccessExpression && parent.name() == node {
                    parent
                } else if is_declaration_name(node) {
                    continue;
                } else {
                    node
                };
            if !builtin_use(expression) {
                tracing::debug!(context = ?walk_up_outer(expression).parent().kind(), "global Object may be replaced");
                return false;
            }
        }
    }
    true
}

fn builtin_use(expression: Node) -> bool {
    let outer = walk_up_outer(expression);
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
            if parent.expression() == outer =>
        {
            !is_write_access(parent)
                && walk_up_outer(parent).parent().kind() != SyntaxKind::DeleteExpression
        }
        SyntaxKind::CallExpression | SyntaxKind::NewExpression => parent.expression() == outer,
        SyntaxKind::BinaryExpression => {
            matches!(
                parent.operator_token().kind(),
                SyntaxKind::EqualsEqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsEqualsToken
                    | SyntaxKind::EqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsToken
            ) || (parent.operator_token().kind() == SyntaxKind::InstanceOfKeyword
                && parent.right() == outer)
        }
        _ => false,
    }
}

/// Key text of a static member access, or `None` for a computed key.
fn static_key(access: Node) -> Option<String> {
    if access.kind() == SyntaxKind::PropertyAccessExpression {
        let name = access.name();
        return (name.kind() == SyntaxKind::Identifier).then(|| name.text().to_owned());
    }
    let key = access.argument_expression();
    match key.kind() {
        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral => {
            Some(key.text().to_owned())
        }
        SyntaxKind::NumericLiteral => canonical_index(key.text()),
        _ => None,
    }
}

/// Numeric keys are supported only in canonical integer form, where the
/// property key string equals the literal text.
pub(super) fn canonical_index(text: &str) -> Option<String> {
    let canonical = text == "0"
        || (!text.is_empty()
            && !text.starts_with('0')
            && text.len() < 16
            && text.bytes().all(|b| b.is_ascii_digit()));
    canonical.then(|| text.to_owned())
}

/// Classifies every runtime reference of `symbol`.
pub(super) fn classify(ctx: &mut Ctx, symbol: SymbolId, object_builtins: bool) -> Uses {
    let mut uses = Uses::default();
    // With verbatim module syntax every value import stays at runtime.
    let verbatim = ctx
        .checker
        .compiler_options
        .verbatim_module_syntax
        .is_true();
    let refs = ctx.refs(symbol).to_vec();
    for r in refs {
        let parent = r.node.parent();
        if parent.kind() == SyntaxKind::ExportSpecifier
            && matches!(r.kind, RefKind::Declaration | RefKind::Alias)
        {
            // `export { X }` and `export { X } from`: TypeScript keeps the
            // runtime binding unless the specifier is type-only.
            if !parent.is_type_only() && !parent.parent().parent().is_type_only() {
                uses.reexported = true;
                uses.binding_observed = true;
            }
            continue;
        }
        match r.kind {
            RefKind::Declaration => {}
            RefKind::Type => {
                uses.type_refs = true;
                if in_type_query(r.node) {
                    match queried_member(r.node) {
                        Some(name) => {
                            uses.type_members.insert(name);
                        }
                        None => uses.type_queries = true,
                    }
                }
            }
            RefKind::Alias => {
                if verbatim
                    && matches!(
                        parent.kind(),
                        SyntaxKind::ImportSpecifier | SyntaxKind::ImportClause
                    )
                    && !parent.is_type_only()
                    && !(parent.kind() == SyntaxKind::ImportSpecifier
                        && parent.parent().parent().is_type_only())
                {
                    uses.binding_observed = true;
                }
            }
            RefKind::Read | RefKind::Call | RefKind::New | RefKind::Write
                if qualified_type_name(r.node).is_some() =>
            {
                // The left side of a qualified type name (`E.A` as a type).
                if qualified_type_name(r.node) == Some(false) {
                    uses.escape.get_or_insert((r.file, r.node));
                } else {
                    uses.type_refs = true;
                    if in_type_query(r.node) {
                        match queried_member(r.node) {
                            Some(name) => {
                                uses.type_members.insert(name);
                            }
                            None => uses.type_queries = true,
                        }
                    }
                }
            }
            RefKind::Read | RefKind::Call | RefKind::New | RefKind::Write => {
                let node = r.node;
                let parent = node.parent();
                let receiver = if parent.kind() == SyntaxKind::PropertyAccessExpression
                    && parent.name() == node
                {
                    // `ns.X` through a module namespace import.
                    let namespace = skip_outer(parent.expression());
                    let symbol = if namespace.kind() == SyntaxKind::Identifier {
                        ctx.resolve(namespace)
                    } else {
                        SymbolId::NIL
                    };
                    if symbol.is_nil()
                        || !ctx
                            .checker
                            .sym(symbol)
                            .flags
                            .intersects(SymbolFlags::ValueModule)
                    {
                        uses.escape.get_or_insert((r.file, node));
                        continue;
                    }
                    parent
                } else {
                    node
                };
                classify_receiver(&mut uses, r.file, receiver, object_builtins);
            }
        }
    }
    uses
}

/// The single member a type query reads: `typeof X.a` or
/// `(typeof X)["a"]`.
fn queried_member(identifier: Node) -> Option<String> {
    let parent = identifier.parent();
    if parent.kind() == SyntaxKind::QualifiedName && parent.left() == identifier {
        return Some(parent.right().text().to_owned());
    }
    if parent.kind() != SyntaxKind::TypeQuery {
        return None;
    }
    let mut query = parent;
    while query.parent().kind() == SyntaxKind::ParenthesizedType {
        query = query.parent();
    }
    let access = query.parent();
    if access.kind() != SyntaxKind::IndexedAccessType || access.object_type() != query {
        return None;
    }
    let index = access.index_type();
    (index.kind() == SyntaxKind::LiteralType && index.literal().kind() == SyntaxKind::StringLiteral)
        .then(|| index.literal().text().to_owned())
}

/// For the left identifier of a qualified name: `Some(true)` in a type
/// position, `Some(false)` in an `import x = A.B` alias (a runtime read);
/// `None` for other identifiers.
fn qualified_type_name(identifier: Node) -> Option<bool> {
    let mut name = identifier.parent();
    if name.kind() != SyntaxKind::QualifiedName || name.left() != identifier {
        return None;
    }
    while name.parent().kind() == SyntaxKind::QualifiedName {
        name = name.parent();
    }
    Some(name.parent().kind() != SyntaxKind::ImportEqualsDeclaration)
}

fn in_type_query(mut node: Node) -> bool {
    while node.is_some() {
        if node.kind() == SyntaxKind::TypeQuery {
            return true;
        }
        node = node.parent();
    }
    false
}

fn classify_receiver(uses: &mut Uses, file: usize, receiver: Node, object_builtins: bool) {
    let outer = walk_up_outer(receiver);
    let direct = outer == receiver;
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
            if parent.expression() == outer =>
        {
            let key = static_key(parent);
            let access = Access {
                node: parent,
                file,
                direct,
            };
            let wrapped = walk_up_outer(parent);
            let holder = wrapped.parent();
            if is_write_access(parent) || holder.kind() == SyntaxKind::DeleteExpression {
                match key {
                    Some(name) => {
                        uses.writes.insert(name);
                    }
                    None => uses.dynamic_write = true,
                }
            } else if (holder.kind() == SyntaxKind::CallExpression
                && holder.expression() == wrapped)
                || (holder.kind() == SyntaxKind::TaggedTemplateExpression
                    && holder.tag() == wrapped)
            {
                match key {
                    Some(name) => {
                        tracing::trace!("member call through object");
                        uses.calls.push((name, access))
                    }
                    None => {
                        uses.escape.get_or_insert((file, parent));
                    }
                }
            } else {
                match key {
                    Some(name) => uses.reads.push((name, access)),
                    None => uses.shape_observed = true,
                }
            }
        }
        SyntaxKind::TypeOfExpression => uses.binding_observed = true,
        SyntaxKind::PrefixUnaryExpression if parent.operator() == SyntaxKind::ExclamationToken => {
            uses.binding_observed = true;
        }
        SyntaxKind::BinaryExpression
            if matches!(
                parent.operator_token().kind(),
                SyntaxKind::EqualsEqualsEqualsToken | SyntaxKind::ExclamationEqualsEqualsToken
            ) =>
        {
            uses.binding_observed = true;
        }
        SyntaxKind::BinaryExpression
            if parent.operator_token().kind() == SyntaxKind::InKeyword
                && parent.right() == outer =>
        {
            uses.binding_observed = true;
            uses.shape_observed = true;
        }
        SyntaxKind::IfStatement
        | SyntaxKind::WhileStatement
        | SyntaxKind::DoStatement
        | SyntaxKind::ConditionalExpression
            if condition_of(parent) == outer =>
        {
            uses.binding_observed = true;
        }
        SyntaxKind::ForInStatement if parent.expression() == outer => {
            uses.binding_observed = true;
            uses.shape_observed = true;
        }
        SyntaxKind::SpreadAssignment => {
            uses.binding_observed = true;
            uses.shape_observed = true;
        }
        SyntaxKind::CallExpression
            if object_builtins
                && parent.expression() != outer
                && is_object_reader(parent.expression()) =>
        {
            uses.binding_observed = true;
            uses.shape_observed = true;
        }
        _ => {
            tracing::trace!(context = ?parent.kind(), "object reference escapes");
            uses.escape.get_or_insert((file, receiver));
        }
    }
}

fn condition_of(statement: Node) -> Node {
    match statement.kind() {
        SyntaxKind::IfStatement | SyntaxKind::ConditionalExpression => statement.condition(),
        _ => statement.expression(),
    }
}

fn is_object_reader(callee: Node) -> bool {
    let callee = skip_outer(callee);
    callee.kind() == SyntaxKind::PropertyAccessExpression
        && callee.question_dot_token().is_nil()
        && skip_outer(callee.expression()).kind() == SyntaxKind::Identifier
        && skip_outer(callee.expression()).text() == "Object"
        && OBJECT_READERS.contains(&callee.name().text())
}

/// Exports of application modules whose namespace object may be observed by
/// code the analysis does not index: public namespaces, namespaces used other
/// than as `ns.name` reads, and dynamically imported modules.
pub(super) fn escaped_exports(ctx: &mut Ctx) -> HashSet<SymbolId> {
    let mut escaped_modules = BTreeSet::new();
    let mut all = false;
    for file in 0..ctx.files.len() {
        let calls: Vec<Node> = ctx.files[file]
            .nodes
            .iter()
            .copied()
            .filter(|n| n.kind() == SyntaxKind::CallExpression && module_loader(n.expression()))
            .collect();
        for call in calls {
            let argument = call.arguments().first().map(skip_outer);
            match argument {
                Some(arg)
                    if matches!(
                        arg.kind(),
                        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
                    ) =>
                {
                    if let Some(target) = resolve_module(ctx, arg) {
                        escaped_modules.insert(target);
                    }
                }
                Some(arg)
                    if arg.kind() == SyntaxKind::TemplateExpression
                        && (arg.head().text().starts_with("./")
                            || arg.head().text().starts_with("../")) =>
                {
                    // Only modules under the static relative prefix can be
                    // loaded by this template.
                    let prefix = relative_prefix(&ctx.files[file].name, arg.head().text());
                    for (index, info) in ctx.files.iter().enumerate() {
                        if info.name.starts_with(&prefix) {
                            escaped_modules.insert(index);
                        }
                    }
                }
                _ => all = true,
            }
        }
    }
    let mut escaped = HashSet::new();
    let mut escaped_namespaces = 0usize;
    for file in 0..ctx.files.len() {
        if ctx.files[file].js {
            continue;
        }
        let node = ctx.files[file].node;
        if node.symbol().is_nil() {
            continue;
        }
        let module = ctx.checker.get_merged_symbol_exported(node.symbol());
        let escapes = all
            || escaped_modules.contains(&file)
            || ctx.public.contains(&module)
            || ctx.js_referenced.contains(&module)
            || ctx.refs(module).to_vec().into_iter().any(|r| {
                matches!(
                    r.kind,
                    RefKind::Read | RefKind::Call | RefKind::New | RefKind::Write
                ) && !namespace_member_read(r.node)
                    && !namespace_function_call(ctx, r.node)
                    && qualified_type_name(r.node) != Some(true)
            });
        if !escapes {
            continue;
        }
        escaped_namespaces += 1;
        for export in ctx.checker.get_exports_of_module_exported(module) {
            escaped.insert(export);
            let target = ctx.skip_alias(export);
            escaped.insert(target);
        }
    }
    tracing::debug!(
        dynamic_imports = escaped_modules.len(),
        unresolved_dynamic_import = all,
        escaped_namespaces,
        escaped_exports = escaped.len(),
        "module namespace escapes analyzed"
    );
    escaped
}

/// Callees that load an ESM module and hand out its namespace object:
/// `import()` and bundler globs (`import.meta.glob`). Application modules are
/// ESM, which a host `require` cannot load.
fn module_loader(callee: Node) -> bool {
    match callee.kind() {
        SyntaxKind::ImportKeyword => true,
        SyntaxKind::PropertyAccessExpression => {
            callee.expression().kind() == SyntaxKind::MetaProperty
                && callee.name().text().starts_with("glob")
        }
        _ => false,
    }
}

/// Absolute path prefix that a relative template head selects, with `.` and
/// `..` segments resolved; the trailing partial segment stays a prefix.
fn relative_prefix(importer: &str, head: &str) -> String {
    let mut segments: Vec<&str> = importer.split('/').collect();
    segments.pop();
    let mut parts = head.split('/').peekable();
    let mut tail = "";
    while let Some(part) = parts.next() {
        if parts.peek().is_none() {
            tail = part;
            break;
        }
        match part {
            "." | "" => {}
            ".." => {
                segments.pop();
            }
            other => segments.push(other),
        }
    }
    format!("{}/{tail}", segments.join("/"))
}

/// `ns.f(...)` where `f` is an application function declaration that never
/// uses `this`, so the namespace object passed as `this` is unobservable.
fn namespace_function_call(ctx: &mut Ctx, node: Node) -> bool {
    let outer = walk_up_outer(node);
    let access = outer.parent();
    if access.kind() != SyntaxKind::PropertyAccessExpression || access.expression() != outer {
        return false;
    }
    let wrapped = walk_up_outer(access);
    let call = wrapped.parent();
    if call.kind() != SyntaxKind::CallExpression || call.expression() != wrapped {
        return false;
    }
    let symbol = ctx.resolve(access.name());
    if symbol.is_nil() {
        return false;
    }
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    !declarations.is_empty()
        && declarations.iter().all(|&d| {
            d.kind() == SyntaxKind::FunctionDeclaration
                && ctx.file_of_node(d).is_some_and(|f| !ctx.files[f].js)
                && !contains_this(d)
        })
}

/// Whether `this` or `super` occurs anywhere below `node`.
pub(super) fn contains_this(node: Node) -> bool {
    let mut found = false;
    node.for_each_child(|child| {
        found = matches!(
            child.kind(),
            SyntaxKind::ThisKeyword | SyntaxKind::SuperKeyword
        ) || contains_this(child);
        found
    });
    found
}

/// `ns.name` read that neither writes, deletes nor calls through the
/// namespace object.
fn namespace_member_read(node: Node) -> bool {
    let outer = walk_up_outer(node);
    let parent = outer.parent();
    if parent.kind() != SyntaxKind::PropertyAccessExpression || parent.expression() != outer {
        return false;
    }
    let wrapped = walk_up_outer(parent);
    let holder = wrapped.parent();
    !is_write_access(parent)
        && holder.kind() != SyntaxKind::DeleteExpression
        && !(holder.kind() == SyntaxKind::CallExpression && holder.expression() == wrapped)
        && !(holder.kind() == SyntaxKind::TaggedTemplateExpression && holder.tag() == wrapped)
}

/// Text of a finite JavaScript number that parses back to the same value.
pub(super) fn number_text(value: f64) -> Option<String> {
    value.is_finite().then(|| {
        if value == 0.0 && value.is_sign_negative() {
            "-0".to_owned()
        } else {
            format!("{value}")
        }
    })
}

/// JavaScript string literal for `value`, or `None` when the compiler's
/// value may have lost lone surrogates.
pub(super) fn string_text(value: &str) -> Option<String> {
    (!value.contains('\u{FFFD}')).then(|| serde_json::to_string(value).expect("string serializes"))
}

/// `(<value> as <type>)`, with `!` for fresh literal types so contextual
/// widening behaves as for the original expression. The emitter drops the
/// assertion's parentheses, so a negative value keeps its own parentheses
/// unless the site's context cannot join the minus sign to a neighboring
/// operator.
pub(super) fn render(site: Node, value: &str, ty: &str, fresh: bool) -> String {
    let value = if value.starts_with('-') && !bare_operand(site) {
        format!("({value})")
    } else {
        value.to_owned()
    };
    if crate::app::bundle_mode() {
        // JavaScript chunks have no recheck to satisfy; the emitter would
        // drop the assertion's parentheses as well.
        return value;
    }
    let bang = if fresh { "!" } else { "" };
    format!("({value} as {ty}){bang}")
}

fn bare_operand(site: Node) -> bool {
    let outer = walk_up_outer(site);
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::PropertyAssignment
        | SyntaxKind::VariableDeclaration
        | SyntaxKind::CaseClause
        | SyntaxKind::ReturnStatement
        | SyntaxKind::ArrayLiteralExpression
        | SyntaxKind::TemplateSpan
        | SyntaxKind::JsxExpression
        | SyntaxKind::ExpressionStatement => true,
        SyntaxKind::CallExpression | SyntaxKind::NewExpression => parent.expression() != outer,
        SyntaxKind::ConditionalExpression => parent.condition() != outer,
        SyntaxKind::BinaryExpression => matches!(
            parent.operator_token().kind(),
            SyntaxKind::EqualsEqualsEqualsToken
                | SyntaxKind::ExclamationEqualsEqualsToken
                | SyntaxKind::EqualsEqualsToken
                | SyntaxKind::ExclamationEqualsToken
                | SyntaxKind::LessThanToken
                | SyntaxKind::GreaterThanToken
                | SyntaxKind::LessThanEqualsToken
                | SyntaxKind::GreaterThanEqualsToken
                | SyntaxKind::CommaToken
                | SyntaxKind::EqualsToken
                | SyntaxKind::AmpersandAmpersandToken
                | SyntaxKind::BarBarToken
                | SyntaxKind::QuestionQuestionToken
        ),
        _ => false,
    }
}

/// `(typeof <receiver>)["<name>"]`: the declared type of the member,
/// including literal freshness.
pub(super) fn member_type(receiver: &str, name: &str) -> String {
    format!(
        "(typeof {receiver})[{}]",
        serde_json::to_string(name).expect("string serializes")
    )
}

/// Type text that denotes exactly `ty` without referring to any binding:
/// primitive keywords and non-enum literal types, possibly in a union, each
/// printed on its own so no type alias name appears.
pub(super) fn print_type(ctx: &mut Ctx, ty: TypeId) -> Option<String> {
    let flags = ctx.checker.ty(ty).flags;
    if flags.intersects(TypeFlags::Union) {
        let members = ctx.checker.ty(ty).types_list();
        if members.len() > 64 {
            return None;
        }
        let mut parts = Vec::new();
        for &member in members.iter() {
            parts.push(print_single(ctx, member)?);
        }
        return Some(parts.join(" | "));
    }
    print_single(ctx, ty)
}

fn print_single(ctx: &mut Ctx, ty: TypeId) -> Option<String> {
    let printable = TypeFlags::String
        | TypeFlags::Number
        | TypeFlags::Boolean
        | TypeFlags::BigInt
        | TypeFlags::StringLiteral
        | TypeFlags::NumberLiteral
        | TypeFlags::BooleanLiteral
        | TypeFlags::BigIntLiteral
        | TypeFlags::Null
        | TypeFlags::Undefined
        | TypeFlags::Any;
    let flags = ctx.checker.ty(ty).flags;
    if !flags.intersects(printable)
        || flags.intersects(TypeFlags::EnumLike | TypeFlags::Union)
        || ctx.checker.ty(ty).alias.is_some()
    {
        return None;
    }
    let text = ctx.checker.type_to_string(ty);
    (!text.contains("...") && !text.is_empty()).then_some(text)
}

/// Whether a literal at `site` would be rejected by the compiler's syntactic
/// checks for conditions that are always truthy, always falsy or never
/// nullish, which look through the replacement's assertion.
pub(super) fn condition_hazard(site: Node, value: &str) -> bool {
    let fixed_truthiness =
        !(matches!(value, "0" | "1" | "true" | "false") || value.starts_with('-'));
    let mut node = walk_up_outer(site);
    loop {
        let parent = node.parent();
        match parent.kind() {
            SyntaxKind::ConditionalExpression => {
                if parent.condition() == node {
                    return fixed_truthiness;
                }
                node = walk_up_outer(parent);
            }
            SyntaxKind::BinaryExpression => {
                let operator = parent.operator_token().kind();
                let left = parent.left() == node;
                match operator {
                    // A side-effect-free left comma operand is reported.
                    SyntaxKind::CommaToken if left => return true,
                    SyntaxKind::QuestionQuestionToken | SyntaxKind::QuestionQuestionEqualsToken
                        if left =>
                    {
                        return true;
                    }
                    SyntaxKind::AmpersandAmpersandToken
                    | SyntaxKind::BarBarToken
                    | SyntaxKind::AmpersandAmpersandEqualsToken
                    | SyntaxKind::BarBarEqualsToken
                        if left =>
                    {
                        return fixed_truthiness;
                    }
                    SyntaxKind::QuestionQuestionToken
                    | SyntaxKind::QuestionQuestionEqualsToken
                    | SyntaxKind::AmpersandAmpersandToken
                    | SyntaxKind::BarBarToken
                    | SyntaxKind::CommaToken
                    | SyntaxKind::EqualsToken
                        if !left =>
                    {
                        node = walk_up_outer(parent);
                    }
                    _ => return false,
                }
            }
            SyntaxKind::IfStatement | SyntaxKind::WhileStatement | SyntaxKind::DoStatement
                if parent.expression() == node =>
            {
                return fixed_truthiness;
            }
            SyntaxKind::ForStatement if parent.condition() == node => return fixed_truthiness,
            SyntaxKind::PrefixUnaryExpression
                if parent.operator() == SyntaxKind::ExclamationToken =>
            {
                return fixed_truthiness;
            }
            _ => return false,
        }
    }
}

/// Whether replacing `site` with text that starts with `(` could join it to
/// the previous statement through automatic semicolon insertion.
pub(super) fn asi_hazard(text: &str, site: Node) -> bool {
    let start = Ctx::start(site);
    let mut node = site;
    let mut statement_start = false;
    while node.is_some() && Ctx::start(node) == start {
        if node.kind() == SyntaxKind::ExpressionStatement {
            statement_start = true;
            break;
        }
        node = node.parent();
    }
    if !statement_start {
        return false;
    }
    let before = text[..start].trim_end();
    !(before.is_empty() || before.ends_with(';') || before.ends_with('{'))
}

/// Positions where an access expression cannot become a parenthesized
/// literal: JSX tag names, decorators, heritage clauses, `new` callees and
/// optional chains.
pub(super) fn replaceable(access: Node) -> bool {
    if access.question_dot_token().is_some() || access.flags().intersects(NodeFlags::OptionalChain)
    {
        return false;
    }
    let outer = walk_up_outer(access);
    let parent = outer.parent();
    !matches!(
        parent.kind(),
        SyntaxKind::JsxOpeningElement
            | SyntaxKind::JsxSelfClosingElement
            | SyntaxKind::JsxClosingElement
            | SyntaxKind::Decorator
            | SyntaxKind::ExpressionWithTypeArguments
    ) && !(parent.kind() == SyntaxKind::NewExpression && parent.expression() == outer)
}

/// A computed member name in a position the emitter erases (signatures,
/// ambient, abstract or overload members): not a runtime read.
pub(super) fn erased_computed_name(access: Node) -> bool {
    let outer = walk_up_outer(access);
    let parent = outer.parent();
    if parent.kind() != SyntaxKind::ComputedPropertyName {
        return false;
    }
    let member = parent.parent();
    let container = member.parent();
    matches!(
        member.kind(),
        SyntaxKind::PropertySignature
            | SyntaxKind::MethodSignature
            | SyntaxKind::IndexSignature
            | SyntaxKind::CallSignature
            | SyntaxKind::ConstructSignature
    ) || matches!(
        container.kind(),
        SyntaxKind::InterfaceDeclaration | SyntaxKind::TypeLiteral
    ) || member.flags().intersects(NodeFlags::Ambient)
        || has_syntactic_modifier(member, ModifierFlags::Ambient | ModifierFlags::Abstract)
        || (matches!(
            member.kind(),
            SyntaxKind::MethodDeclaration
                | SyntaxKind::Constructor
                | SyntaxKind::GetAccessor
                | SyntaxKind::SetAccessor
        ) && member.body().is_nil())
}

/// The access has its declared member type at this site (no control-flow
/// narrowing), so the member type reproduces it.
pub(super) fn unnarrowed(ctx: &mut Ctx, access: Node, declared: TypeId) -> bool {
    let at_site = ctx.checker.get_type_at_location(access);
    let regular = ctx.checker.get_regular_type_of_literal_type(declared);
    at_site == regular
}
