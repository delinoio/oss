// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Shared facts for the dead-code family (`unused-method-removal`,
//! `unused-field-removal` and `residual-dependency-removal`): deletion spans
//! that keep directives, module namespace exposure, property-name mention
//! indexing, "may hold an instance" queries and class instance confinement.
//!
//! Everything here is recomputed from the current program in every round.
use std::collections::{HashMap, HashSet};

use ts_goport::prelude::*;

use super::super::{
    analysis::{Ctx, RefKind, skip_outer, walk_up_outer},
    token_start,
};

/// Property names that runtimes and libraries probe by convention without
/// necessarily declaring them in a type: thenables, serialization, coercion,
/// iteration protocols, DOM listener objects, disposal and inspection.
pub(super) const PROTOCOL_NAMES: &[&str] = &[
    "constructor",
    "prototype",
    "__proto__",
    "then",
    "catch",
    "finally",
    "toString",
    "toLocaleString",
    "valueOf",
    "toJSON",
    "inspect",
    "handleEvent",
    "next",
    "return",
    "throw",
    "length",
    "name",
    "caller",
    "arguments",
    "call",
    "apply",
    "bind",
    "dispose",
    "asyncDispose",
    "default",
];

/// Comment markers whose deletion could change the meaning of other code.
const PROTECTED_TRIVIA: &[&str] = &["///", "#!", "@ts-nocheck", "@jsx", "@license", "@preserve"];

/// Start of the deletion span of a statement or class member. Leading
/// documentation comments on their own lines are deleted with the node; a
/// trailing comment of the previous line and file-level directives stay.
pub(super) fn removal_start(text: &str, node: Node) -> usize {
    let start = token_start(node);
    let pos = (node.pos() as usize).min(start);
    let trivia = &text[pos..start];
    if PROTECTED_TRIVIA
        .iter()
        .any(|marker| trivia.contains(marker))
    {
        return start;
    }
    match trivia.find('\n') {
        Some(newline) => pos + newline + 1,
        None => pos,
    }
}

/// Deletion ranges for removing the `dead` elements of a comma-separated list
/// (declarators, import or export specifiers) when at least one element
/// stays. Each maximal run of dead elements is deleted together with one
/// adjacent comma, so independently planned runs either do not touch or
/// conflict in the pipeline's overlap check.
pub(super) fn list_removals(elements: &[Node], dead: &[bool]) -> Vec<(usize, usize)> {
    let mut ranges = Vec::new();
    let mut index = 0;
    while index < elements.len() {
        if !dead[index] {
            index += 1;
            continue;
        }
        let first = index;
        while index < elements.len() && dead[index] {
            index += 1;
        }
        let last = index - 1;
        if index < elements.len() {
            ranges.push((token_start(elements[first]), token_start(elements[index])));
        } else {
            debug_assert!(first > 0, "a fully dead list is deleted as a whole");
            ranges.push((
                elements[first - 1].end() as usize,
                elements[last].end() as usize,
            ));
        }
    }
    ranges
}

/// Application module index that a module specifier resolves to.
pub(super) fn module_index(ctx: &mut Ctx, specifier: Node) -> Option<usize> {
    if specifier.is_nil() {
        return None;
    }
    let symbol = ctx.checker.get_symbol_at_location(specifier, true);
    if symbol.is_nil() {
        return None;
    }
    let declaration = ctx.checker.sym(symbol).declarations.first().copied()?;
    let file = get_source_file_of_node(declaration);
    ctx.file_of(&source_file_info(file).file_name)
}

/// Application module of a module symbol (the target of a namespace alias).
fn module_of_symbol(ctx: &mut Ctx, symbol: SymbolId) -> Option<usize> {
    let declaration = ctx.checker.sym(symbol).declarations.first().copied()?;
    if declaration.kind() != SyntaxKind::SourceFile {
        return None;
    }
    ctx.file_of(&source_file_info(declaration).file_name)
}

/// Modules whose namespace object may be observed by code outside the
/// analysis: entries, JavaScript importers, dynamic imports, namespace
/// re-exports and namespace imports that are used other than as `ns.name`
/// reads. Exposure propagates through `export *`. Every export of an exposed
/// module, including star re-exports, is externally reachable by name.
pub(super) struct Exposure {
    observed: Vec<bool>,
    all: bool,
}

impl Exposure {
    pub fn compute(ctx: &mut Ctx) -> Self {
        let count = ctx.files.len();
        let mut observed: Vec<bool> = ctx.files.iter().map(|f| f.entry || f.js).collect();
        let mut all = false;
        let mut stars: Vec<(usize, usize)> = Vec::new();
        for index in 0..count {
            let js = ctx.files[index].js;
            let nodes = ctx.files[index].nodes.clone();
            for node in nodes {
                match node.kind() {
                    SyntaxKind::ImportDeclaration => {
                        let clause = node.import_clause();
                        if clause.is_some() && clause.is_type_only() {
                            continue;
                        }
                        let Some(target) = module_index(ctx, node.module_specifier()) else {
                            continue;
                        };
                        if js {
                            observed[target] = true;
                            continue;
                        }
                        let bindings = if clause.is_some() {
                            clause.named_bindings()
                        } else {
                            Node::NIL
                        };
                        if bindings.is_some() && bindings.kind() == SyntaxKind::NamespaceImport {
                            let symbol = ctx.resolve(bindings.name());
                            if symbol.is_nil() || namespace_escapes(ctx, symbol) {
                                observed[target] = true;
                            }
                        }
                    }
                    SyntaxKind::ExportDeclaration => {
                        if node.is_type_only() {
                            continue;
                        }
                        let Some(target) = module_index(ctx, node.module_specifier()) else {
                            continue;
                        };
                        let clause = node.export_clause();
                        if js || (clause.is_some() && clause.kind() == SyntaxKind::NamespaceExport)
                        {
                            observed[target] = true;
                        } else if clause.is_nil() {
                            stars.push((index, target));
                        }
                    }
                    SyntaxKind::CallExpression
                        if node.expression().kind() == SyntaxKind::ImportKeyword =>
                    {
                        let argument = node.arguments().first().map(skip_outer);
                        match argument {
                            Some(literal)
                                if matches!(
                                    literal.kind(),
                                    SyntaxKind::StringLiteral
                                        | SyntaxKind::NoSubstitutionTemplateLiteral
                                ) =>
                            {
                                if let Some(target) = module_index(ctx, literal) {
                                    observed[target] = true;
                                }
                            }
                            Some(template)
                                if template.kind() == SyntaxKind::TemplateExpression
                                    && template_targets_data(template) => {}
                            _ => all = true,
                        }
                    }
                    _ => {}
                }
            }
        }
        // Exposure flows from a module to the modules it star re-exports.
        let mut changed = true;
        while changed {
            changed = false;
            for &(from, to) in &stars {
                if observed[from] && !observed[to] {
                    observed[to] = true;
                    changed = true;
                }
            }
        }
        tracing::debug!(
            exposed = observed.iter().filter(|&&o| o).count(),
            unknown_dynamic_import = all,
            "module namespace exposure computed"
        );
        Self { observed, all }
    }

    pub fn observed(&self, file: usize) -> bool {
        self.all || self.observed[file]
    }
}

/// A template dynamic import whose last literal part names a data or asset
/// extension cannot load an application TypeScript module.
fn template_targets_data(template: Node) -> bool {
    let spans = template.template_spans().nodes();
    let Some(last) = spans.last() else {
        return false;
    };
    let tail = last.literal().text();
    [".json", ".css", ".scss", ".svg", ".png", ".wasm"]
        .iter()
        .any(|extension| tail.ends_with(extension))
}

/// Whether a namespace import binding is used other than as `ns.name` reads.
fn namespace_escapes(ctx: &mut Ctx, module: SymbolId) -> bool {
    let refs = ctx.refs(module).to_vec();
    refs.iter().any(|r| match r.kind {
        RefKind::Declaration | RefKind::Alias | RefKind::Type => false,
        _ => {
            let outer = walk_up_outer(r.node);
            let parent = outer.parent();
            !(parent.kind() == SyntaxKind::PropertyAccessExpression
                && parent.expression() == outer
                && parent.name().kind() == SyntaxKind::Identifier
                && !is_write_access(parent))
        }
    }) || module_of_symbol(ctx, module).is_none()
}

/// Property-like name mentions of application code, by name. TypeScript
/// mentions follow the shared `mentioned_names` rules; JavaScript modules
/// are unchecked, so every identifier and string there counts.
pub(super) struct Mentions {
    by_name: HashMap<String, Vec<(usize, Node)>>,
}

impl Mentions {
    pub fn compute(ctx: &Ctx) -> Self {
        let mut by_name: HashMap<String, Vec<(usize, Node)>> = HashMap::new();
        for (index, file) in ctx.files.iter().enumerate() {
            for &node in &file.nodes {
                let name = if file.js {
                    match node.kind() {
                        SyntaxKind::Identifier
                        | SyntaxKind::PrivateIdentifier
                        | SyntaxKind::StringLiteral
                        | SyntaxKind::NoSubstitutionTemplateLiteral => Some(node.text()),
                        _ => None,
                    }
                } else {
                    mentioned_name(node)
                };
                if let Some(name) = name {
                    by_name
                        .entry(name.to_owned())
                        .or_default()
                        .push((index, node));
                }
            }
        }
        Self { by_name }
    }

    pub fn of(&self, name: &str) -> &[(usize, Node)] {
        self.by_name.get(name).map_or(&[], Vec::as_slice)
    }
}

/// Mirrors `analysis::mentioned_name`: the property-like name a node
/// mentions, if any.
fn mentioned_name(node: Node) -> Option<&'static str> {
    let parent = node.parent();
    match node.kind() {
        SyntaxKind::Identifier | SyntaxKind::PrivateIdentifier => {
            let mentions = match parent.kind() {
                SyntaxKind::PropertyAccessExpression => parent.name() == node,
                SyntaxKind::PropertyAssignment
                | SyntaxKind::ShorthandPropertyAssignment
                | SyntaxKind::PropertyDeclaration
                | SyntaxKind::MethodDeclaration
                | SyntaxKind::GetAccessor
                | SyntaxKind::SetAccessor
                | SyntaxKind::PropertySignature
                | SyntaxKind::MethodSignature
                | SyntaxKind::EnumMember => parent.name() == node,
                SyntaxKind::BindingElement => {
                    parent.property_name() == node
                        || (parent.property_name().is_nil() && parent.name() == node)
                }
                SyntaxKind::QualifiedName => parent.right() == node,
                SyntaxKind::JsxAttribute => true,
                // `#x in obj` mentions a private name without an access.
                SyntaxKind::BinaryExpression => node.kind() == SyntaxKind::PrivateIdentifier,
                _ => false,
            };
            mentions.then(|| node.text())
        }
        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral => Some(node.text()),
        _ => None,
    }
}

/// Whether `inner` lies inside `[start, end)` of the same file.
pub(super) fn within(node: Node, span: (usize, usize)) -> bool {
    let at = token_start(node);
    at >= span.0 && at < span.1
}

/// Static types a class's values can have: its instance type and its
/// constructor type.
#[derive(Clone, Copy)]
pub(super) struct ClassTypes {
    pub instance: TypeId,
    pub constructor: TypeId,
}

impl ClassTypes {
    pub fn of(ctx: &mut Ctx, class_symbol: SymbolId) -> Self {
        Self {
            instance: ctx.checker.get_declared_type_of_symbol(class_symbol),
            constructor: ctx.checker.get_type_of_symbol(class_symbol),
        }
    }
}

/// Type nomination of whether `expression` may evaluate to an instance,
/// prototype or constructor of the class. Assertions are seen through: the
/// value keeps the type of the asserted expression. `any`, `unknown`, type
/// parameters and top object types may hold anything.
pub(super) fn may_hold(ctx: &mut Ctx, expression: Node, class: ClassTypes) -> bool {
    let inner = skip_outer(expression);
    let ty = ctx.checker.get_type_at_location(inner);
    type_may_hold(ctx, ty, class, 0)
}

fn type_may_hold(ctx: &mut Ctx, ty: TypeId, class: ClassTypes, depth: usize) -> bool {
    if ty.is_nil() || depth > 8 {
        return true;
    }
    let flags = ctx.checker.ty(ty).flags();
    if flags
        .intersects(TypeFlags::ANY_OR_UNKNOWN | TypeFlags::INSTANTIABLE | TypeFlags::NON_PRIMITIVE)
    {
        return true;
    }
    if flags.intersects(TypeFlags::UNION_OR_INTERSECTION) {
        let members = ctx.checker.ty(ty).types_list();
        return members
            .iter()
            .any(|&member| type_may_hold(ctx, member, class, depth + 1));
    }
    if flags.intersects(TypeFlags::OBJECT) {
        return ty == class.instance
            || ty == class.constructor
            || ctx.checker.is_type_assignable_to(class.instance, ty)
            || ctx.checker.is_type_assignable_to(class.constructor, ty);
    }
    false
}

/// Reflection and descriptor APIs that can list or read non-enumerable or
/// arbitrary own properties.
const REFLECTION: &[(&str, &[&str])] = &[
    (
        "Object",
        &[
            "getOwnPropertyNames",
            "getOwnPropertyDescriptor",
            "getOwnPropertyDescriptors",
            "getOwnPropertySymbols",
            "getPrototypeOf",
            "setPrototypeOf",
            "defineProperty",
            "defineProperties",
            "entries",
            "keys",
            "values",
            "assign",
            "freeze",
            "seal",
            "preventExtensions",
            "hasOwn",
            "groupBy",
        ],
    ),
    ("Reflect", &[]),
    ("JSON", &["stringify"]),
];

/// `Object.keys(...)`-style reflective calls: the callee is a property of a
/// global reflection namespace. Returns the call's arguments when it is one.
pub(super) fn reflective_call(node: Node) -> Option<NodeSlice> {
    if node.kind() != SyntaxKind::CallExpression {
        return None;
    }
    let callee = skip_outer(node.expression());
    if callee.kind() != SyntaxKind::PropertyAccessExpression {
        return None;
    }
    let receiver = skip_outer(callee.expression());
    if receiver.kind() != SyntaxKind::Identifier {
        return None;
    }
    let member = callee.name().text();
    REFLECTION
        .iter()
        .any(|(namespace, members)| {
            receiver.text() == *namespace && (members.is_empty() || members.contains(&member))
        })
        .then(|| node.arguments())
}

/// Facts that make values of a class reachable by computed names: dynamic
/// element accesses, `in` tests with computed keys, reflective calls and
/// `for...in` whose subject may hold the class.
pub(super) fn dynamically_observed(
    ctx: &mut Ctx,
    class: ClassTypes,
) -> Option<crate::plan::Reason> {
    use crate::plan::Reason;
    let accesses: Vec<Node> = ctx.dynamic_accesses.iter().map(|a| a.node).collect();
    for access in accesses {
        if may_hold(ctx, access.expression(), class) {
            return Some(Reason::DynamicAccess);
        }
    }
    for index in 0..ctx.files.len() {
        if ctx.files[index].js {
            continue;
        }
        let nodes = ctx.files[index].nodes.clone();
        for node in nodes {
            match node.kind() {
                SyntaxKind::CallExpression => {
                    if let Some(arguments) = reflective_call(node) {
                        for argument in arguments.iter() {
                            let argument = if argument.kind() == SyntaxKind::SpreadElement {
                                argument.expression()
                            } else {
                                argument
                            };
                            if may_hold(ctx, argument, class) {
                                return Some(Reason::Reflection);
                            }
                        }
                    }
                }
                SyntaxKind::BinaryExpression
                    if node.operator_token().kind() == SyntaxKind::InKeyword =>
                {
                    let key = skip_outer(node.left());
                    if !matches!(
                        key.kind(),
                        SyntaxKind::StringLiteral | SyntaxKind::PrivateIdentifier
                    ) && may_hold(ctx, node.right(), class)
                    {
                        return Some(Reason::ObservableOperation);
                    }
                }
                SyntaxKind::ForInStatement => {
                    if may_hold(ctx, node.expression(), class) {
                        return Some(Reason::Reflection);
                    }
                }
                _ => {}
            }
        }
    }
    None
}

/// Class symbol used in reference lookups, or nil for anonymous classes.
pub(super) fn class_symbol(ctx: &mut Ctx, class: Node) -> SymbolId {
    if class.kind() == SyntaxKind::ClassDeclaration && class.name().is_some() {
        return ctx.resolve(class.name());
    }
    let parent = class.parent();
    if class.kind() == SyntaxKind::ClassExpression
        && parent.kind() == SyntaxKind::VariableDeclaration
        && parent.initializer() == class
        && parent.name().kind() == SyntaxKind::Identifier
    {
        return ctx.resolve(parent.name());
    }
    SymbolId::NIL
}

/// Display name of a class for candidate keys.
pub(super) fn class_name(class: Node) -> &'static str {
    let name = class.name();
    if name.is_some() {
        return name.text();
    }
    let parent = class.parent();
    if parent.kind() == SyntaxKind::VariableDeclaration
        && parent.name().kind() == SyntaxKind::Identifier
    {
        return parent.name().text();
    }
    "default"
}

/// Name text of a class member or object literal property with a static,
/// non-numeric name.
pub(super) fn static_name(node: Node) -> Option<&'static str> {
    let name = node.name();
    if name.is_nil() {
        return None;
    }
    match name.kind() {
        SyntaxKind::Identifier | SyntaxKind::PrivateIdentifier | SyntaxKind::StringLiteral => {
            Some(name.text())
        }
        _ => None,
    }
}

/// Ordinal-disambiguated keys: the first occurrence of a base key keeps it,
/// later ones get `#n`. Inputs are visited in deterministic order.
#[derive(Default)]
pub(super) struct Keys {
    seen: HashMap<String, usize>,
}

impl Keys {
    pub fn unique(&mut self, base: String) -> String {
        let count = self.seen.entry(base.clone()).or_default();
        *count += 1;
        if *count == 1 {
            base
        } else {
            format!("{base}#{count}")
        }
    }
}

/// Text that is safe as an expression statement or operand when it replaces
/// an assignment: parenthesized unless it is a plain call, identifier or
/// literal form that cannot be misparsed.
pub(super) fn evaluated(text: &str, statement: bool) -> String {
    let trimmed = text.trim_start();
    let ambiguous = trimmed.starts_with('{')
        || trimmed.starts_with("function")
        || trimmed.starts_with("class")
        || trimmed.starts_with("let")
        || trimmed.starts_with("async");
    if statement && !ambiguous {
        text.to_owned()
    } else {
        format!("({text})")
    }
}

/// The class member that directly owns `this` at `node`, and whether that
/// position is inside an arrow function (which keeps the member's `this`).
pub(super) fn this_owner(node: Node) -> Node {
    get_this_container(node, false, false)
}

/// Classes and members that a `this`-confinement proof covers.
pub(super) struct Confinement {
    /// Member accesses through confined receivers, by member name.
    pub accesses: HashMap<String, Vec<Node>>,
    /// Methods that are read without being called on the receiver.
    pub extracted: HashSet<String>,
    /// Fields and accessors whose values are called with the receiver.
    pub field_calls: HashSet<String>,
}

/// Proves that every instance and the constructor of `class` are only used
/// as receivers of accesses to the class's own declared members: no value of
/// the class reaches other code, reflection, enumeration or external code.
/// Returns the reason when the proof fails.
pub(super) fn confine(
    ctx: &mut Ctx,
    class: Node,
    symbol: SymbolId,
) -> Result<Confinement, crate::plan::Reason> {
    use crate::plan::Reason;
    if class.kind() != SyntaxKind::ClassDeclaration {
        return Err(Reason::UnsupportedSyntax);
    }
    if !class.decorators().is_empty() {
        return Err(Reason::Decorator);
    }
    if class
        .heritage_clauses()
        .nodes()
        .iter()
        .any(|clause| clause.token() == SyntaxKind::ExtendsKeyword)
    {
        return Err(Reason::Inheritance);
    }
    let mut instance_members = HashMap::<&'static str, SyntaxKind>::new();
    let mut static_members = HashMap::<&'static str, SyntaxKind>::new();
    for member in class.members().iter() {
        if !member.decorators().is_empty() {
            return Err(Reason::Decorator);
        }
        if member.kind() == SyntaxKind::Constructor {
            for parameter in member.parameters().iter() {
                if !parameter.modifier_flags().is_empty() {
                    if let Some(name) = static_name(parameter) {
                        instance_members.insert(name, SyntaxKind::PropertyDeclaration);
                    }
                }
            }
            continue;
        }
        if matches!(
            member.kind(),
            SyntaxKind::SemicolonClassElement | SyntaxKind::IndexSignature
        ) {
            continue;
        }
        if member.kind() == SyntaxKind::ClassStaticBlockDeclaration {
            continue;
        }
        let Some(name) = static_name(member) else {
            return Err(Reason::DynamicAccess);
        };
        if PROTOCOL_NAMES.contains(&name) {
            return Err(Reason::SpecialProperty);
        }
        if is_static(member) {
            static_members.insert(name, member.kind());
        } else {
            instance_members.insert(name, member.kind());
        }
    }
    let mut confinement = Confinement {
        accesses: HashMap::new(),
        extracted: HashSet::new(),
        field_calls: HashSet::new(),
    };
    // The constructor value: only `new C(...)`, `C.staticMember` and type
    // or module-alias positions.
    let refs = ctx.refs(symbol).to_vec();
    for r in refs {
        match r.kind {
            RefKind::Declaration | RefKind::Alias | RefKind::Type => {}
            RefKind::New => {
                let expression = walk_up_outer(r.node).parent();
                instance_value(ctx, expression, &instance_members, &mut confinement)?;
            }
            RefKind::Read | RefKind::Call | RefKind::Write => {
                let outer = walk_up_outer(r.node);
                member_access(outer, &static_members, &mut confinement)?;
            }
        }
    }
    // `this` inside the class's own members.
    let file = ctx.file_of_node(class).ok_or(Reason::ExternalBoundary)?;
    let span = (token_start(class), class.end() as usize);
    let nodes: Vec<Node> = ctx.files[file]
        .nodes
        .iter()
        .copied()
        .filter(|&n| n.kind() == SyntaxKind::ThisKeyword && within(n, span))
        .collect();
    for this in nodes {
        let owner = this_owner(this);
        if owner.parent() != class {
            // A nested function or class binds its own `this`. Such a
            // function only receives an instance as `this` through a call
            // that is itself an escape (an instance argument or a call
            // through a non-arrow field), which the other checks reject.
            continue;
        }
        let members = if is_static(owner) || owner.kind() == SyntaxKind::ClassStaticBlockDeclaration
        {
            &static_members
        } else {
            &instance_members
        };
        member_access(walk_up_outer(this), members, &mut confinement)?;
    }
    // A call through a field passes the receiver as `this` to the stored
    // function. Only arrow initializers that are never reassigned ignore it.
    for name in confinement.field_calls.clone() {
        let arrow = class.members().iter().any(|member| {
            member.kind() == SyntaxKind::PropertyDeclaration
                && static_name(member) == Some(name.as_str())
                && member.initializer().is_some()
                && skip_outer(member.initializer()).kind() == SyntaxKind::ArrowFunction
        });
        let written = confinement
            .accesses
            .get(&name)
            .is_some_and(|accesses| accesses.iter().any(|&a| is_write_access(a)));
        if !arrow || written {
            return Err(Reason::Escapes);
        }
    }
    Ok(confinement)
}

/// A `new C(...)` value: discarded, used directly as a member receiver, or
/// bound to a `const` whose every use is a member receiver.
fn instance_value(
    ctx: &mut Ctx,
    expression: Node,
    members: &HashMap<&'static str, SyntaxKind>,
    confinement: &mut Confinement,
) -> Result<(), crate::plan::Reason> {
    use crate::plan::Reason;
    let outer = walk_up_outer(expression);
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::ExpressionStatement => Ok(()),
        SyntaxKind::PropertyAccessExpression if parent.expression() == outer => {
            member_access(outer, members, confinement)
        }
        SyntaxKind::VariableDeclaration
            if parent.initializer() == outer
                && parent.name().kind() == SyntaxKind::Identifier
                && parent.parent().flags().intersects(NodeFlags::CONST) =>
        {
            let binding = ctx.resolve(parent.name());
            if binding.is_nil()
                || ctx.public.contains(&binding)
                || ctx.js_referenced.contains(&binding)
            {
                return Err(Reason::Escapes);
            }
            let refs = ctx.refs(binding).to_vec();
            for r in refs {
                match r.kind {
                    RefKind::Declaration | RefKind::Type => {}
                    RefKind::Read | RefKind::Call => {
                        member_access(walk_up_outer(r.node), members, confinement)?
                    }
                    _ => return Err(Reason::Escapes),
                }
            }
            Ok(())
        }
        _ => Err(Reason::Escapes),
    }
}

/// `receiver.member` where `member` is declared by the class on this side.
/// Calls through fields must target arrow functions; other calls pass the
/// receiver as `this` to the class's own methods only.
fn member_access(
    receiver: Node,
    members: &HashMap<&'static str, SyntaxKind>,
    confinement: &mut Confinement,
) -> Result<(), crate::plan::Reason> {
    use crate::plan::Reason;
    let access = receiver.parent();
    if access.kind() != SyntaxKind::PropertyAccessExpression || access.expression() != receiver {
        return Err(Reason::Escapes);
    }
    let name = access.name().text();
    let Some(&kind) = members.get(name) else {
        return Err(Reason::Escapes);
    };
    let outer = walk_up_outer(access);
    let holder = outer.parent();
    let called = matches!(
        holder.kind(),
        SyntaxKind::CallExpression | SyntaxKind::TaggedTemplateExpression
    ) && (holder.kind() == SyntaxKind::CallExpression && holder.expression() == outer
        || holder.kind() == SyntaxKind::TaggedTemplateExpression && holder.tag() == outer);
    if called && kind != SyntaxKind::MethodDeclaration {
        confinement.field_calls.insert(name.to_owned());
    }
    if !called && kind == SyntaxKind::MethodDeclaration {
        confinement.extracted.insert(name.to_owned());
    }
    if holder.kind() == SyntaxKind::DeleteExpression {
        return Err(Reason::ObservableOperation);
    }
    confinement
        .accesses
        .entry(name.to_owned())
        .or_default()
        .push(access);
    Ok(())
}
