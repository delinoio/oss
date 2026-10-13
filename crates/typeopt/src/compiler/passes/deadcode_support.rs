// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Shared facts for the dead-code family (`unused-method-removal`,
//! `unused-field-removal` and `residual-dependency-removal`): deletion spans
//! that keep directives, module namespace exposure, property-name mention
//! indexing, "may hold an instance" queries and class instance confinement.
//!
//! Everything here is recomputed from the current program in every round.
use std::collections::{HashMap, HashSet};

use super::super::{
    analysis::{Ctx, RefKind, skip_outer, walk_up_outer},
    token_start,
};
use crate::compiler::api::*;

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
    // Strings that numbers, booleans and nullish keys convert to.
    "true",
    "false",
    "null",
    "undefined",
    "NaN",
    "Infinity",
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
    let symbol = ctx.checker.get_symbol_at_location(specifier);
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

/// Whether a static type says nothing about its values: `any`, `unknown`,
/// `object`, `{}` and unconstrained (or imprecisely constrained) type
/// parameters, or a union with such a member.
pub(super) fn imprecise(ctx: &mut Ctx, ty: TypeId, depth: usize) -> bool {
    if ty.is_nil() || depth > 8 {
        return true;
    }
    let flags = ctx.checker.ty(ty).flags();
    if flags.intersects(TypeFlags::AnyOrUnknown | TypeFlags::NonPrimitive) {
        return true;
    }
    if flags.intersects(TypeFlags::Union) {
        let members = ctx.checker.ty(ty).types_list();
        return members.iter().any(|&m| imprecise(ctx, m, depth + 1));
    }
    if flags.intersects(TypeFlags::Instantiable) {
        let constraint = ctx.checker.get_base_constraint_of_type(ty);
        return constraint.is_nil() || constraint == ty || imprecise(ctx, constraint, depth + 1);
    }
    flags.intersects(TypeFlags::Object) && ctx.checker.is_empty_object_type(ty)
}

/// Value flows between static types of the application: a value of the
/// source type is stored in a slot of the target type. Edges come from
/// contextual typing (arguments, initializers, returns, assignments,
/// properties, elements, JSX attributes, conditional branches), assertions,
/// declared generic parameters of callees outside the standard library and
/// callback parameters whose declared types differ from what the caller
/// provides. Thrown values reach `catch` bindings of unknown type.
pub(super) struct TypeFlows {
    edges: Vec<(TypeId, TypeId)>,
    thrown: Vec<TypeId>,
    /// Application class declarations and their application base class.
    bases: Vec<(Node, Node)>,
}

impl TypeFlows {
    pub fn compute(ctx: &mut Ctx) -> Self {
        let mut edges = HashSet::new();
        let mut thrown = HashSet::new();
        let mut bases = Vec::new();
        for index in 0..ctx.files.len() {
            if ctx.files[index].js {
                continue;
            }
            let nodes = ctx.files[index].nodes.clone();
            for node in nodes {
                if matches!(
                    node.kind(),
                    SyntaxKind::ClassDeclaration | SyntaxKind::ClassExpression
                ) {
                    bases.push((node, base_class(ctx, node)));
                }
                let parent = node.parent();
                if parent.is_some() && parent.kind() == SyntaxKind::ThrowStatement {
                    thrown.insert(ctx.checker.get_type_at_location(node));
                    continue;
                }
                for (source, target) in flows_of(ctx, node) {
                    if source.is_some() && target.is_some() && source != target {
                        edges.insert((source, target));
                    }
                }
            }
        }
        let mut edges: Vec<_> = edges.into_iter().collect();
        edges.sort();
        let mut thrown: Vec<_> = thrown.into_iter().collect();
        thrown.sort();
        tracing::debug!(edges = edges.len(), "type flows computed");
        Self {
            edges,
            thrown,
            bases,
        }
    }

    /// Static types that may hold an instance, the prototype or the
    /// constructor of `class`: the class, its application ancestors and
    /// descendants (inherited code sees instances through `this`), closed
    /// over every value flow.
    pub fn holders(&self, ctx: &mut Ctx, class: Node) -> Holders {
        let mut related = vec![class];
        let mut base = self.base_of(class);
        while base.is_some() && !related.contains(&base) {
            related.push(base);
            base = self.base_of(base);
        }
        let mut changed = true;
        while changed {
            changed = false;
            for &(derived, base) in &self.bases {
                if base.is_some() && related.contains(&base) && !related.contains(&derived) {
                    related.push(derived);
                    changed = true;
                }
            }
        }
        let mut types = HashSet::new();
        for declaration in related {
            let symbol = ctx.checker.get_symbol_of_declaration(declaration);
            types.insert(ctx.checker.get_declared_type_of_symbol(symbol));
            types.insert(ctx.checker.get_type_of_symbol(symbol));
            // External bases see instances through their own types.
            for clause in declaration.heritage_clauses().nodes().iter() {
                if clause.token() == SyntaxKind::ExtendsKeyword {
                    for heritage in clause.types().nodes().iter() {
                        types.insert(ctx.checker.get_type_at_location(heritage));
                    }
                }
            }
        }
        let mut changed = true;
        while changed {
            changed = false;
            for &(source, target) in &self.edges {
                if !types.contains(&target) && holds(ctx, &types, source, 0) {
                    types.insert(target);
                    changed = true;
                }
            }
        }
        let mut escapes = self
            .thrown
            .iter()
            .any(|&thrown| holds(ctx, &types, thrown, 0));
        if !escapes {
            let mut sorted: Vec<TypeId> = types.iter().copied().collect();
            sorted.sort();
            escapes = sorted.into_iter().any(|t| imprecise(ctx, t, 0));
        }
        Holders { types, escapes }
    }

    fn base_of(&self, class: Node) -> Node {
        self.bases
            .iter()
            .find(|&&(declaration, _)| declaration == class)
            .map_or(Node::NIL, |&(_, base)| base)
    }
}

/// The application class declaration a class extends, or nil.
fn base_class(ctx: &mut Ctx, class: Node) -> Node {
    for clause in class.heritage_clauses().nodes().iter() {
        if clause.token() != SyntaxKind::ExtendsKeyword {
            continue;
        }
        for heritage in clause.types().nodes().iter() {
            let expression = skip_outer(heritage.expression());
            if expression.kind() != SyntaxKind::Identifier {
                return Node::NIL;
            }
            let symbol = ctx.resolve(expression);
            if symbol.is_nil() {
                return Node::NIL;
            }
            let declaration = ctx.checker.sym(symbol).value_declaration;
            if declaration.is_some()
                && matches!(
                    declaration.kind(),
                    SyntaxKind::ClassDeclaration | SyntaxKind::ClassExpression
                )
            {
                return declaration;
            }
        }
    }
    Node::NIL
}

/// The set of static types that may hold values of one class, and whether
/// such values reach imprecise types, where any imprecisely typed value may
/// be one of them.
pub(super) struct Holders {
    types: HashSet<TypeId>,
    pub escapes: bool,
}

impl Holders {
    /// Whether `expression` may evaluate to a value of the class.
    /// Assertions are seen through: the value keeps the type of the asserted
    /// expression.
    pub fn may_hold(&self, ctx: &mut Ctx, expression: Node) -> bool {
        let inner = skip_outer(expression);
        let ty = ctx.checker.get_type_at_location(inner);
        holds(ctx, &self.types, ty, 0) || (self.escapes && imprecise(ctx, ty, 0))
    }
}

fn holds(ctx: &mut Ctx, types: &HashSet<TypeId>, ty: TypeId, depth: usize) -> bool {
    if ty.is_nil() || depth > 8 {
        return false;
    }
    if types.contains(&ty) {
        return true;
    }
    let flags = ctx.checker.ty(ty).flags();
    if flags.intersects(TypeFlags::UnionOrIntersection) {
        let members = ctx.checker.ty(ty).types_list();
        return members.iter().any(|&m| holds(ctx, types, m, depth + 1));
    }
    if flags.intersects(TypeFlags::Instantiable) {
        let constraint = ctx.checker.get_base_constraint_of_type(ty);
        return constraint.is_some()
            && constraint != ty
            && holds(ctx, types, constraint, depth + 1);
    }
    false
}

/// `(source, target)` value flows that `node` takes part in.
fn flows_of(ctx: &mut Ctx, node: Node) -> Vec<(TypeId, TypeId)> {
    let parent = node.parent();
    if parent.is_nil() {
        return Vec::new();
    }
    let position = match parent.kind() {
        SyntaxKind::CallExpression | SyntaxKind::NewExpression => {
            parent.expression() != node && parent.arguments().iter().any(|a| a == node)
        }
        SyntaxKind::AsExpression | SyntaxKind::TypeAssertionExpression => {
            if parent.expression() != node {
                return Vec::new();
            }
            return vec![(
                ctx.checker.get_type_at_location(node),
                ctx.checker.get_type_at_location(parent),
            )];
        }
        SyntaxKind::VariableDeclaration
        | SyntaxKind::PropertyDeclaration
        | SyntaxKind::Parameter
        | SyntaxKind::BindingElement => parent.initializer() == node,
        SyntaxKind::BinaryExpression => {
            parent.right() == node && parent.operator_token().kind() == SyntaxKind::EqualsToken
        }
        SyntaxKind::PropertyAssignment => parent.initializer() == node,
        SyntaxKind::ShorthandPropertyAssignment => parent.name() == node,
        SyntaxKind::ReturnStatement
        | SyntaxKind::ArrayLiteralExpression
        | SyntaxKind::JsxExpression
        | SyntaxKind::YieldExpression
        | SyntaxKind::SpreadElement => true,
        SyntaxKind::ArrowFunction => parent.body() == node,
        SyntaxKind::ConditionalExpression => parent.condition() != node,
        _ => false,
    };
    if !position {
        return Vec::new();
    }
    let mut flows = Vec::new();
    let source = ctx.checker.get_type_at_location(node);
    let target = ctx.checker.get_contextual_type(node, ContextFlags::None);
    if target.is_some() {
        flows.push((source, target));
        flows.extend(callback_flows(ctx, source, target));
    }
    if matches!(
        parent.kind(),
        SyntaxKind::CallExpression | SyntaxKind::NewExpression
    ) && let Some(declared) = generic_parameter(ctx, parent, node)
    {
        flows.push((source, declared));
    }
    flows
}

/// `(provided, declared)` parameter flows of a function value stored in a
/// function-typed slot: callers provide the slot's parameter types to the
/// function's own parameters.
fn callback_flows(ctx: &mut Ctx, source: TypeId, target: TypeId) -> Vec<(TypeId, TypeId)> {
    let mut flows = Vec::new();
    let sources = ctx
        .checker
        .get_signatures_of_type(source, SignatureKind::Call);
    if sources.is_empty() {
        return flows;
    }
    let targets = ctx
        .checker
        .get_signatures_of_type(target, SignatureKind::Call);
    for &own in sources.iter() {
        for &context in targets.iter() {
            let count = ctx
                .checker
                .get_parameter_count(own)
                .min(ctx.checker.get_parameter_count(context));
            for position in 0..count {
                let declared = ctx.checker.get_type_at_position(own, position);
                let provided = ctx.checker.get_type_at_position(context, position);
                flows.push((provided, declared));
            }
        }
    }
    flows
}

/// The declared type of the parameter an argument binds, when it mentions
/// type parameters of a callee outside the standard library: the value then
/// reaches code that sees it through a type variable. Calls without a known
/// declaration bind an unknown parameter.
fn generic_parameter(ctx: &mut Ctx, call: Node, argument: Node) -> Option<TypeId> {
    let signature = ctx.checker.get_resolved_signature_exported(call);
    if signature.is_nil() {
        return None;
    }
    let declaration = ctx.checker.sig(signature).declaration;
    if declaration.is_nil() {
        return None;
    }
    let file = source_file_info(get_source_file_of_node(declaration))
        .file_name
        .clone();
    if file.starts_with("bundled:") {
        return None;
    }
    let declared = ctx.checker.get_signature_from_declaration(declaration);
    let position = call.arguments().iter().position(|a| a == argument)?;
    let count = ctx.checker.get_parameter_count(declared);
    if count == 0 {
        return None;
    }
    let position = (position as i32).min(count - 1);
    let ty = ctx.checker.get_type_at_position(declared, position);
    ctx.checker.could_contain_type_variables(ty).then_some(ty)
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

/// Operations that look up a class member by a computed name: dynamic
/// element accesses and `in` tests (with their keys), and reflective calls
/// that list or read arbitrary own keys (without a key). `for...in` and
/// `Object.keys`-style enumeration only produce enumerable keys, which never
/// name a class method or accessor.
pub(super) struct DynamicSites {
    sites: Vec<(Node, Node, crate::plan::Reason)>,
    /// Application JavaScript is unchecked: its lookups are not indexed.
    javascript: bool,
}

impl DynamicSites {
    pub fn compute(ctx: &Ctx) -> Self {
        use crate::plan::Reason;
        let mut sites: Vec<(Node, Node, Reason)> = ctx
            .dynamic_accesses
            .iter()
            .map(|a| {
                (
                    a.node.expression(),
                    a.node.argument_expression(),
                    Reason::DynamicAccess,
                )
            })
            .collect();
        for file in &ctx.files {
            if file.js {
                continue;
            }
            for &node in &file.nodes {
                match node.kind() {
                    SyntaxKind::CallExpression => {
                        if let Some(arguments) = reflective_call(node) {
                            if enumerating_call(node) {
                                continue;
                            }
                            for argument in arguments.iter() {
                                let argument = if argument.kind() == SyntaxKind::SpreadElement {
                                    argument.expression()
                                } else {
                                    argument
                                };
                                sites.push((argument, Node::NIL, Reason::Reflection));
                            }
                        }
                    }
                    SyntaxKind::BinaryExpression
                        if node.operator_token().kind() == SyntaxKind::InKeyword =>
                    {
                        let key = skip_outer(node.left());
                        if key.kind() != SyntaxKind::PrivateIdentifier {
                            sites.push((node.right(), key, Reason::ObservableOperation));
                        }
                    }
                    _ => {}
                }
            }
        }
        Self {
            sites,
            javascript: ctx.files.iter().any(|f| f.js),
        }
    }

    /// The first site that may look up a member of the class by a name that
    /// is not spelled in the source.
    pub fn observes(&self, ctx: &mut Ctx, holders: &Holders) -> Option<crate::plan::Reason> {
        // Imprecisely typed values may flow into unchecked JavaScript.
        if holders.escapes && self.javascript {
            return Some(crate::plan::Reason::ExternalBoundary);
        }
        let mut provenance = KeyProvenance::default();
        for &(receiver, key, reason) in &self.sites {
            if (key.is_nil() || provenance.opaque(ctx, key, 0)) && holders.may_hold(ctx, receiver) {
                return Some(reason);
            }
        }
        None
    }
}

/// `Object.keys`, `Object.values`, `Object.entries`, `JSON.stringify`,
/// `Object.assign`, `Object.freeze` and similar calls that only see
/// enumerable own properties or do not look properties up by name.
fn enumerating_call(call: Node) -> bool {
    let callee = skip_outer(call.expression());
    let member = callee.name().text();
    let receiver = skip_outer(callee.expression()).text();
    matches!(
        (receiver, member),
        (
            "Object",
            "keys" | "values" | "entries" | "assign" | "freeze" | "seal"
        ) | ("Object", "preventExtensions" | "groupBy")
            | ("JSON", "stringify")
    )
}

/// Runtime provenance of computed keys: whether a key may evaluate to a
/// string that names a non-enumerable member and is not spelled as a
/// literal anywhere. Numbers, literal strings and keys produced by
/// enumerating enumerable properties cannot.
#[derive(Default)]
struct KeyProvenance {
    bindings: HashMap<SymbolId, bool>,
}

impl KeyProvenance {
    fn opaque(&mut self, ctx: &mut Ctx, key: Node, depth: usize) -> bool {
        if depth > 8 {
            return true;
        }
        let key = skip_outer(key);
        match key.kind() {
            SyntaxKind::NumericLiteral
            | SyntaxKind::BigIntLiteral
            | SyntaxKind::StringLiteral
            | SyntaxKind::NoSubstitutionTemplateLiteral
            | SyntaxKind::TrueKeyword
            | SyntaxKind::FalseKeyword
            | SyntaxKind::NullKeyword => false,
            SyntaxKind::PrefixUnaryExpression | SyntaxKind::PostfixUnaryExpression => {
                // `-x`, `+x`, `~x`, `!x`, `++x` and `x--` produce numbers,
                // big integers or booleans.
                false
            }
            SyntaxKind::BinaryExpression => {
                let operator = key.operator_token().kind();
                match operator {
                    // `+` concatenates unless both operands are numeric.
                    SyntaxKind::PlusToken => {
                        !(self.numeric(ctx, key.left(), depth + 1)
                            && self.numeric(ctx, key.right(), depth + 1))
                    }
                    SyntaxKind::CommaToken => self.opaque(ctx, key.right(), depth + 1),
                    SyntaxKind::AmpersandAmpersandToken
                    | SyntaxKind::BarBarToken
                    | SyntaxKind::QuestionQuestionToken => {
                        self.opaque(ctx, key.left(), depth + 1)
                            || self.opaque(ctx, key.right(), depth + 1)
                    }
                    _ if is_assignment_operator(operator) => true,
                    // Arithmetic, bitwise, comparison and `in`/`instanceof`
                    // operators produce numbers, big integers or booleans.
                    _ => false,
                }
            }
            SyntaxKind::ConditionalExpression => {
                self.opaque(ctx, key.when_true(), depth + 1)
                    || self.opaque(ctx, key.when_false(), depth + 1)
            }
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
                ctx.checker.get_constant_value(key).is_none()
            }
            SyntaxKind::Identifier => self.binding(ctx, key, depth),
            _ => true,
        }
    }

    /// Whether the expression evaluates to a number or big integer.
    fn numeric(&mut self, ctx: &mut Ctx, node: Node, depth: usize) -> bool {
        if depth > 8 {
            return false;
        }
        let node = skip_outer(node);
        match node.kind() {
            SyntaxKind::NumericLiteral | SyntaxKind::BigIntLiteral => true,
            SyntaxKind::PrefixUnaryExpression => {
                !matches!(node.operator(), SyntaxKind::ExclamationToken)
            }
            SyntaxKind::PostfixUnaryExpression => true,
            SyntaxKind::BinaryExpression => match node.operator_token().kind() {
                SyntaxKind::PlusToken => {
                    self.numeric(ctx, node.left(), depth + 1)
                        && self.numeric(ctx, node.right(), depth + 1)
                }
                SyntaxKind::MinusToken
                | SyntaxKind::AsteriskToken
                | SyntaxKind::AsteriskAsteriskToken
                | SyntaxKind::SlashToken
                | SyntaxKind::PercentToken
                | SyntaxKind::LessThanLessThanToken
                | SyntaxKind::GreaterThanGreaterThanToken
                | SyntaxKind::GreaterThanGreaterThanGreaterThanToken
                | SyntaxKind::AmpersandToken
                | SyntaxKind::BarToken
                | SyntaxKind::CaretToken => true,
                _ => false,
            },
            SyntaxKind::Identifier => {
                // A binding whose every value is numeric, such as a loop
                // counter.
                let symbol = ctx.resolve(node);
                symbol.is_some() && self.binding_values(ctx, symbol, depth, true)
            }
            _ => false,
        }
    }

    fn binding(&mut self, ctx: &mut Ctx, identifier: Node, depth: usize) -> bool {
        let symbol = ctx.resolve(identifier);
        if symbol.is_nil() {
            return true;
        }
        if let Some(&known) = self.bindings.get(&symbol) {
            return known;
        }
        // Assume opaque while the binding is being resolved (cycles).
        self.bindings.insert(symbol, true);
        let opaque = !self.binding_values(ctx, symbol, depth, false);
        self.bindings.insert(symbol, opaque);
        opaque
    }

    /// Whether every value the binding can hold is transparent (or numeric
    /// when `numeric`): its initializer, every assignment, and enumeration
    /// of enumerable keys for loop and callback bindings.
    fn binding_values(
        &mut self,
        ctx: &mut Ctx,
        symbol: SymbolId,
        depth: usize,
        numeric: bool,
    ) -> bool {
        let declaration = ctx.checker.sym(symbol).value_declaration;
        if declaration.is_nil() {
            return false;
        }
        let check = |this: &mut Self, ctx: &mut Ctx, value: Node| {
            if numeric {
                this.numeric(ctx, value, depth + 1)
            } else {
                !this.opaque(ctx, value, depth + 1)
            }
        };
        let initial = match declaration.kind() {
            SyntaxKind::VariableDeclaration => {
                let list = declaration.parent();
                let holder = list.parent();
                if holder.kind() == SyntaxKind::ForInStatement {
                    // Enumerable string keys only.
                    !numeric
                } else if holder.kind() == SyntaxKind::ForOfStatement {
                    !numeric && enumerated_keys(holder.expression())
                } else if declaration.initializer().is_some() {
                    check(self, ctx, declaration.initializer())
                } else {
                    true
                }
            }
            SyntaxKind::BindingElement => {
                // `for (const [key] of Object.entries(x))` and
                // `Object.entries(x).forEach(([key]) => ...)`.
                let pattern = declaration.parent();
                pattern.kind() == SyntaxKind::ArrayBindingPattern
                    && pattern.elements().first() == Some(declaration)
                    && !numeric
                    && {
                        let holder = pattern.parent();
                        (holder.kind() == SyntaxKind::VariableDeclaration
                            && holder.parent().parent().kind() == SyntaxKind::ForOfStatement
                            && entries_call(holder.parent().parent().expression()))
                            || (holder.kind() == SyntaxKind::Parameter
                                && enumeration_callback(holder, true))
                    }
            }
            SyntaxKind::Parameter => !numeric && enumeration_callback(declaration, false),
            _ => false,
        };
        if !initial {
            return false;
        }
        // Every later write must keep the property.
        let refs = ctx.refs(symbol).to_vec();
        for r in refs {
            if r.kind != RefKind::Write {
                continue;
            }
            let outer = walk_up_outer(r.node);
            let parent = outer.parent();
            let fine = match parent.kind() {
                SyntaxKind::PrefixUnaryExpression | SyntaxKind::PostfixUnaryExpression => true,
                SyntaxKind::BinaryExpression if parent.left() == outer => {
                    match parent.operator_token().kind() {
                        SyntaxKind::EqualsToken => check(self, ctx, parent.right()),
                        SyntaxKind::PlusEqualsToken => {
                            self.numeric(ctx, parent.right(), depth + 1) && numeric
                        }
                        operator => {
                            is_assignment_operator(operator)
                                && !matches!(
                                    operator,
                                    SyntaxKind::AmpersandAmpersandEqualsToken
                                        | SyntaxKind::BarBarEqualsToken
                                        | SyntaxKind::QuestionQuestionEqualsToken
                                )
                        }
                    }
                }
                _ => false,
            };
            if !fine {
                return false;
            }
        }
        true
    }
}

fn is_assignment_operator(kind: SyntaxKind) -> bool {
    matches!(
        kind,
        SyntaxKind::EqualsToken
            | SyntaxKind::PlusEqualsToken
            | SyntaxKind::MinusEqualsToken
            | SyntaxKind::AsteriskEqualsToken
            | SyntaxKind::AsteriskAsteriskEqualsToken
            | SyntaxKind::SlashEqualsToken
            | SyntaxKind::PercentEqualsToken
            | SyntaxKind::LessThanLessThanEqualsToken
            | SyntaxKind::GreaterThanGreaterThanEqualsToken
            | SyntaxKind::GreaterThanGreaterThanGreaterThanEqualsToken
            | SyntaxKind::AmpersandEqualsToken
            | SyntaxKind::BarEqualsToken
            | SyntaxKind::CaretEqualsToken
            | SyntaxKind::AmpersandAmpersandEqualsToken
            | SyntaxKind::BarBarEqualsToken
            | SyntaxKind::QuestionQuestionEqualsToken
    )
}

/// `Object.<name>(...)` with one of the given names.
fn object_call(node: Node, names: &[&str]) -> bool {
    let node = skip_outer(node);
    if node.kind() != SyntaxKind::CallExpression {
        return false;
    }
    let callee = skip_outer(node.expression());
    callee.kind() == SyntaxKind::PropertyAccessExpression
        && skip_outer(callee.expression()).kind() == SyntaxKind::Identifier
        && skip_outer(callee.expression()).text() == "Object"
        && names.contains(&callee.name().text())
}

/// `Object.keys(x)`: an array of enumerable own string keys.
fn enumerated_keys(node: Node) -> bool {
    object_call(node, &["keys"])
}

/// `Object.entries(x)`: pairs whose first element is an enumerable key.
fn entries_call(node: Node) -> bool {
    object_call(node, &["entries"])
}

/// The first parameter (or the first element of its array pattern when
/// `entries`) of a callback passed directly to an array method of
/// `Object.keys(...)` (or `Object.entries(...)`).
fn enumeration_callback(parameter: Node, entries: bool) -> bool {
    let function = parameter.parent();
    if !matches!(
        function.kind(),
        SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression
    ) || function.parameters().first() != Some(parameter)
    {
        return false;
    }
    let call = walk_up_outer(function).parent();
    if call.kind() != SyntaxKind::CallExpression
        || call.arguments().first().map(skip_outer) != Some(function)
    {
        return false;
    }
    let callee = skip_outer(call.expression());
    callee.kind() == SyntaxKind::PropertyAccessExpression
        && matches!(
            callee.name().text(),
            "forEach" | "map" | "filter" | "some" | "every" | "find" | "findIndex" | "flatMap"
        )
        && if entries {
            entries_call(callee.expression())
        } else {
            enumerated_keys(callee.expression())
        }
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

/// Replacement text for an assignment `target = value` that keeps only
/// the evaluation of `value`. The value fills the same grammar slot, so it
/// only needs parentheses where it starts an expression statement or an
/// arrow function body and could be misparsed as a block or declaration.
pub(super) fn evaluated(assignment: Node, text: &str) -> String {
    let outer = walk_up_outer(assignment);
    let parent = outer.parent();
    let leading = outer == assignment
        && (parent.kind() == SyntaxKind::ExpressionStatement
            || (parent.kind() == SyntaxKind::ArrowFunction && parent.body() == outer));
    let trimmed = text.trim_start();
    let ambiguous = trimmed.starts_with('{')
        || trimmed.starts_with("function")
        || trimmed.starts_with("class")
        || trimmed.starts_with("let")
        || trimmed.starts_with("async");
    if leading && ambiguous {
        format!("({text})")
    } else {
        text.to_owned()
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
    /// Why an instance may reach other code, if it may.
    pub instances: Option<crate::plan::Reason>,
    /// Why the constructor may reach other code, if it may. Instances that
    /// such code creates are not confined either.
    pub constructor: Option<crate::plan::Reason>,
}

/// Which values of a class an access goes through.
#[derive(Clone, Copy, PartialEq, Eq, Hash)]
enum Side {
    Instance,
    Constructor,
}

/// Proves that the instances and the constructor of `class` are only used
/// as receivers of accesses to the class's own declared members: no such
/// value reaches other code, reflection, enumeration or external code. Each
/// side records why its proof fails; class-wide obstacles fail both.
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
    let mut members: HashMap<(Side, &'static str), SyntaxKind> = HashMap::new();
    for member in class.members().iter() {
        if !member.decorators().is_empty() {
            return Err(Reason::Decorator);
        }
        if member.kind() == SyntaxKind::Constructor {
            for parameter in member.parameters().iter() {
                if !parameter.decorators().is_empty() {
                    return Err(Reason::Decorator);
                }
                if !parameter.modifier_flags().is_empty()
                    && let Some(name) = static_name(parameter)
                {
                    members.insert((Side::Instance, name), SyntaxKind::Parameter);
                }
            }
            continue;
        }
        if matches!(
            member.kind(),
            SyntaxKind::SemicolonClassElement
                | SyntaxKind::IndexSignature
                | SyntaxKind::ClassStaticBlockDeclaration
        ) {
            continue;
        }
        let Some(name) = static_name(member) else {
            return Err(Reason::DynamicAccess);
        };
        if PROTOCOL_NAMES.contains(&name) {
            return Err(Reason::SpecialProperty);
        }
        let side = if is_static(member) {
            Side::Constructor
        } else {
            Side::Instance
        };
        members.insert((side, name), member.kind());
    }
    let mut proof = Proof {
        members: &members,
        confinement: Confinement {
            accesses: HashMap::new(),
            extracted: HashSet::new(),
            instances: None,
            constructor: None,
        },
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
                let result = proof.instance_value(ctx, expression);
                proof.fail(Side::Instance, result);
            }
            RefKind::Read | RefKind::Call | RefKind::Write => {
                let result = proof.member_access(walk_up_outer(r.node), Side::Constructor);
                proof.fail(Side::Constructor, result);
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
            // function only receives a value of the class as `this` through
            // a call that is itself an escape (an argument, or a call
            // through a non-arrow field), which the other checks reject.
            continue;
        }
        let side = if is_static(owner) || owner.kind() == SyntaxKind::ClassStaticBlockDeclaration {
            Side::Constructor
        } else {
            Side::Instance
        };
        let result = proof.member_access(walk_up_outer(this), side);
        proof.fail(side, result);
    }
    // A call through a field passes the receiver as `this` to the stored
    // function. Only arrow initializers that are never reassigned ignore it.
    for (side, name) in proof.field_calls.clone() {
        let arrow = class.members().iter().any(|member| {
            member.kind() == SyntaxKind::PropertyDeclaration
                && (side == Side::Constructor) == is_static(member)
                && static_name(member) == Some(name.as_str())
                && member.initializer().is_some()
                && skip_outer(member.initializer()).kind() == SyntaxKind::ArrowFunction
        });
        let written = proof
            .confinement
            .accesses
            .get(&name)
            .is_some_and(|accesses| accesses.iter().any(|&a| is_write_access(a)));
        if !arrow || written {
            proof.fail(side, Err(Reason::Escapes));
        }
    }
    let mut confinement = proof.confinement;
    // Code that receives the constructor can create unconfined instances.
    if confinement.instances.is_none() {
        confinement.instances = confinement.constructor;
    }
    Ok(confinement)
}

struct Proof<'a> {
    members: &'a HashMap<(Side, &'static str), SyntaxKind>,
    confinement: Confinement,
    field_calls: HashSet<(Side, String)>,
}

impl Proof<'_> {
    fn fail(&mut self, side: Side, result: Result<(), crate::plan::Reason>) {
        if let Err(reason) = result {
            let slot = match side {
                Side::Instance => &mut self.confinement.instances,
                Side::Constructor => &mut self.confinement.constructor,
            };
            slot.get_or_insert(reason);
        }
    }

    /// A `new C(...)` value: discarded, used directly as a member receiver,
    /// or bound to a `const` whose every use is a member receiver.
    fn instance_value(
        &mut self,
        ctx: &mut Ctx,
        expression: Node,
    ) -> Result<(), crate::plan::Reason> {
        use crate::plan::Reason;
        let outer = walk_up_outer(expression);
        let parent = outer.parent();
        match parent.kind() {
            SyntaxKind::ExpressionStatement => Ok(()),
            SyntaxKind::PropertyAccessExpression if parent.expression() == outer => {
                self.member_access(outer, Side::Instance)
            }
            SyntaxKind::VariableDeclaration
                if parent.initializer() == outer
                    && parent.name().kind() == SyntaxKind::Identifier
                    && (parent.parent().flags() & NodeFlags::BlockScoped) == NodeFlags::Const =>
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
                        RefKind::Declaration if r.node == parent.name() => {}
                        RefKind::Type => {}
                        RefKind::Read | RefKind::Call => {
                            self.member_access(walk_up_outer(r.node), Side::Instance)?
                        }
                        _ => return Err(Reason::Escapes),
                    }
                }
                Ok(())
            }
            _ => Err(Reason::Escapes),
        }
    }

    /// `receiver.member` where `member` is declared by the class on this
    /// side. Calls through fields must target arrow functions; other calls
    /// pass the receiver as `this` to the class's own methods only.
    fn member_access(&mut self, receiver: Node, side: Side) -> Result<(), crate::plan::Reason> {
        use crate::plan::Reason;
        let access = receiver.parent();
        if access.kind() != SyntaxKind::PropertyAccessExpression || access.expression() != receiver
        {
            return Err(Reason::Escapes);
        }
        let name = access.name().text();
        let Some(&kind) = self.members.get(&(side, name)) else {
            return Err(Reason::Escapes);
        };
        let outer = walk_up_outer(access);
        let holder = outer.parent();
        if holder.kind() == SyntaxKind::DeleteExpression {
            return Err(Reason::ObservableOperation);
        }
        let called = (holder.kind() == SyntaxKind::CallExpression && holder.expression() == outer)
            || (holder.kind() == SyntaxKind::TaggedTemplateExpression && holder.tag() == outer);
        if called && kind != SyntaxKind::MethodDeclaration {
            self.field_calls.insert((side, name.to_owned()));
        }
        if !called && kind == SyntaxKind::MethodDeclaration {
            self.confinement.extracted.insert(name.to_owned());
        }
        self.confinement
            .accesses
            .entry(name.to_owned())
            .or_default()
            .push(access);
        Ok(())
    }
}
