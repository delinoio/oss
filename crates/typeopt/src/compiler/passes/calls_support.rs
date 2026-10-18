// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Facts shared by the call family (`method-devirtualization` and
//! `call-inlining`): scope-hygienic name resolution, module evaluation-order
//! proofs for temporal-dead-zone safety, movable argument classification,
//! fresh identifier allocation and precedence helpers.
//!
//! Every fact here is runtime evidence derived from syntax, symbols and the
//! static import graph. Checker types never decide safety on their own.
use std::collections::{HashMap, HashSet};

use super::{
    super::{
        analysis::{Ctx, RefKind},
        collect,
    },
    calls_init::InitCheck,
};
use crate::compiler::api::*;

/// Canonical identity of a symbol for scope comparisons: the exported
/// symbol of an exported local, alias targets and merged symbols.
pub(super) fn norm(ctx: &mut Ctx, symbol: SymbolId) -> SymbolId {
    if symbol.is_nil() {
        return symbol;
    }
    let mut symbol = symbol;
    let export = ctx.checker.sym(symbol).export_symbol;
    if export.is_some() {
        symbol = export;
    }
    let symbol = ctx.skip_alias(symbol);
    ctx.checker.get_merged_symbol(symbol)
}

/// What `name` means at `location` (normalized), or nil when unresolved.
pub(super) fn resolve_at(
    ctx: &mut Ctx,
    name: &str,
    location: Node,
    meaning: SymbolFlags,
) -> SymbolId {
    let raw = ctx
        .checker
        .resolve_name_exported(name, location, meaning, false);
    norm(ctx, raw)
}

/// Whether `name` resolves at `location` through a type-only import, which
/// cannot provide a runtime value.
pub(super) fn type_only_alias_at(ctx: &mut Ctx, name: &str, location: Node) -> bool {
    let raw = ctx
        .checker
        .resolve_name_exported(name, location, SymbolFlags::Value, false);
    if raw.is_nil() || !ctx.checker.sym(raw).flags.intersects(SymbolFlags::Alias) {
        return false;
    }
    ctx.checker
        .sym(raw)
        .declarations
        .iter()
        .any(|&d| match d.kind() {
            SyntaxKind::ImportSpecifier => d.is_type_only() || d.parent().parent().is_type_only(),
            SyntaxKind::ImportClause => d.is_type_only(),
            SyntaxKind::NamespaceImport => d.parent().is_type_only(),
            _ => false,
        })
}

/// Lazily computed whole-program facts for one planning call.
pub(super) struct Facts {
    /// Static app-module import edges per file.
    edges: Option<Vec<Vec<usize>>>,
    /// Strongly connected component of every module.
    components: Option<Vec<usize>>,
    cyclic: HashMap<usize, bool>,
    /// Index of the first non-import top-level statement that may run code.
    first_impure: HashMap<usize, usize>,
    /// Modules whose namespace object may be observed (namespace imports,
    /// `export *`, dynamic imports).
    namespace_observed: Option<(HashSet<usize>, bool)>,
    identifiers: HashMap<usize, HashSet<String>>,
}

impl Facts {
    pub fn new() -> Self {
        Self {
            edges: None,
            components: None,
            cyclic: HashMap::new(),
            first_impure: HashMap::new(),
            namespace_observed: None,
            identifiers: HashMap::new(),
        }
    }

    /// Application module a module specifier string resolves to.
    pub fn target(ctx: &mut Ctx, specifier: Node) -> Option<usize> {
        if specifier.is_nil()
            || !matches!(
                specifier.kind(),
                SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
            )
        {
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

    fn edges(&mut self, ctx: &mut Ctx) -> &Vec<Vec<usize>> {
        if self.edges.is_none() {
            let mut edges = vec![Vec::new(); ctx.files.len()];
            for (index, list) in edges.iter_mut().enumerate() {
                let statements: Vec<Node> = ctx.files[index].node.statements().to_vec();
                for statement in statements {
                    if !matches!(
                        statement.kind(),
                        SyntaxKind::ImportDeclaration | SyntaxKind::ExportDeclaration
                    ) || !runtime_dependency(ctx, index, statement)
                    {
                        continue;
                    }
                    if let Some(target) = Self::target(ctx, statement.module_specifier()) {
                        list.push(target);
                    }
                }
            }
            self.edges = Some(edges);
        }
        self.edges.as_ref().unwrap()
    }

    /// Whether no other module of `index`'s static import cycle can run
    /// application code during initialization. Only modules of the same
    /// strongly connected component can be evaluated before `index` and
    /// still reach its bindings; modules outside the application never
    /// import application modules.
    pub fn cycle_safe(&mut self, ctx: &mut Ctx, index: usize) -> bool {
        if self.components.is_none() {
            let edges = self.edges(ctx).clone();
            self.components = Some(components(&edges));
        }
        if let Some(&safe) = self.cyclic.get(&index) {
            return safe;
        }
        let components = self.components.as_ref().unwrap();
        let component = components[index];
        let members: Vec<usize> = (0..components.len())
            .filter(|&m| m != index && components[m] == component)
            .collect();
        let mut safe = true;
        for member in members {
            if ctx.files[member].js
                || self.first_impure(ctx, member) < ctx.files[member].node.statements().len()
            {
                safe = false;
                break;
            }
        }
        self.cyclic.insert(index, safe);
        safe
    }

    /// Whether two modules lie in the same strongly connected component of
    /// the static import graph. A module outside the component of one it
    /// imports starts evaluating after that whole component finished.
    pub fn same_component(&mut self, ctx: &mut Ctx, a: usize, b: usize) -> bool {
        if self.components.is_none() {
            let edges = self.edges(ctx).clone();
            self.components = Some(components(&edges));
        }
        let components = self.components.as_ref().unwrap();
        components[a] == components[b]
    }

    /// Whether no module, including `index` itself, can import `index`
    /// while its bindings are still initializing.
    pub fn alone_in_component(&mut self, ctx: &mut Ctx, index: usize) -> bool {
        if self.components.is_none() {
            let edges = self.edges(ctx).clone();
            self.components = Some(components(&edges));
        }
        let components = self.components.as_ref().unwrap();
        (0..components.len()).all(|m| m == index || components[m] != components[index])
            && !self.edges(ctx)[index].contains(&index)
    }

    fn first_impure(&mut self, ctx: &mut Ctx, index: usize) -> usize {
        if let Some(&first) = self.first_impure.get(&index) {
            return first;
        }
        let statements: Vec<Node> = ctx.files[index].node.statements().to_vec();
        let mut first = statements.len();
        for (position, statement) in statements.into_iter().enumerate() {
            // Static imports are hoisted; the acyclic-graph proof covers
            // them, not their textual position.
            let hoisted = matches!(
                statement.kind(),
                SyntaxKind::ImportDeclaration | SyntaxKind::ExportDeclaration
            ) && statement.module_specifier().is_some();
            if !hoisted && InitCheck::new(ctx).statement_runs_app_code(statement) {
                first = position;
                break;
            }
        }
        self.first_impure.insert(index, first);
        first
    }

    /// Whether reading the binding can never observe its temporal dead
    /// zone: a top-level `const` in an application module outside any import
    /// cycle whose initializer and every earlier top-level statement run no
    /// code. No code can then hold a reference to the binding before it is
    /// initialized.
    pub fn initialized_early(&mut self, ctx: &mut Ctx, symbol: SymbolId) -> bool {
        self.initialized_early_declaration(ctx, symbol, false)
    }

    /// [`Self::initialized_early`] for a `let` or `var` binding as well. The
    /// caller proves that nothing writes the binding; only the ordering
    /// argument applies then, and it is the same for every declaration kind
    /// (a top-level `var` is read as `undefined` before its initializer, a
    /// `let` or `const` throws).
    pub fn initialized_early_unwritten(&mut self, ctx: &mut Ctx, symbol: SymbolId) -> bool {
        self.initialized_early_declaration(ctx, symbol, true)
    }

    fn initialized_early_declaration(
        &mut self,
        ctx: &mut Ctx,
        symbol: SymbolId,
        any_kind: bool,
    ) -> bool {
        let declaration = ctx.checker.sym(symbol).value_declaration;
        if declaration.is_nil() || declaration.kind() != SyntaxKind::VariableDeclaration {
            return false;
        }
        let list = declaration.parent();
        if (!any_kind && !list.flags().intersects(NodeFlags::Const))
            || list.flags().intersects(NodeFlags::Using)
        {
            return false;
        }
        let statement = list.parent();
        if statement.kind() != SyntaxKind::VariableStatement
            || statement.parent().kind() != SyntaxKind::SourceFile
            || statement.flags().intersects(NodeFlags::Ambient)
        {
            return false;
        }
        let Some(file) = ctx.file_of_node(declaration) else {
            return false;
        };
        if ctx.files[file].js || !self.cycle_safe(ctx, file) {
            return false;
        }
        let position = ctx.files[file]
            .node
            .statements()
            .iter()
            .position(|s| s == statement);
        let Some(position) = position else {
            return false;
        };
        if self.first_impure(ctx, file) < position {
            return false;
        }
        for item in list.declarations().nodes().iter() {
            let initializer = item.initializer();
            if initializer.is_nil() {
                if item == declaration {
                    return false;
                }
                continue;
            }
            if InitCheck::new(ctx).app_code(initializer) {
                return false;
            }
            if item == declaration {
                break;
            }
        }
        true
    }

    /// Whether reading the identifier has no effect, cannot throw and yields
    /// a value no evaluation in between can change.
    pub fn stable_binding(&mut self, ctx: &mut Ctx, node: Node) -> bool {
        if node.kind() != SyntaxKind::Identifier {
            return false;
        }
        let raw = ctx.resolve(node);
        if raw.is_nil() {
            return false;
        }
        let symbol = norm(ctx, raw);
        if symbol == norm(ctx, ctx.checker.undefined_symbol) {
            return true;
        }
        if ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write)
            || ctx.refs(raw).iter().any(|r| r.kind == RefKind::Write)
        {
            return false;
        }
        let declaration = ctx.checker.sym(symbol).value_declaration;
        if declaration.is_nil() || declaration.flags().intersects(NodeFlags::Ambient) {
            return false;
        }
        match declaration.kind() {
            SyntaxKind::FunctionDeclaration => ctx.file_of_node(declaration).is_some(),
            SyntaxKind::Parameter => {
                declaration.name().kind() == SyntaxKind::Identifier
                    && declaration.name().text() != "this"
            }
            SyntaxKind::VariableDeclaration => {
                declaration.parent().flags().intersects(NodeFlags::Const)
                    && (ctx.is_pure(node) || self.initialized_early(ctx, symbol))
            }
            _ => false,
        }
    }

    /// Whether evaluating `node` has no effect, cannot throw and can be moved
    /// anywhere within the same expression. With `duplicable`, evaluating it
    /// more than once must also be unobservable (no fresh identities).
    pub fn movable(&mut self, ctx: &mut Ctx, node: Node, duplicable: bool) -> bool {
        match node.kind() {
            SyntaxKind::ParenthesizedExpression
            | SyntaxKind::AsExpression
            | SyntaxKind::SatisfiesExpression
            | SyntaxKind::TypeAssertionExpression
            | SyntaxKind::NonNullExpression => self.movable(ctx, node.expression(), duplicable),
            SyntaxKind::NumericLiteral
            | SyntaxKind::StringLiteral
            | SyntaxKind::BigIntLiteral
            | SyntaxKind::NoSubstitutionTemplateLiteral
            | SyntaxKind::TrueKeyword
            | SyntaxKind::FalseKeyword
            | SyntaxKind::NullKeyword => true,
            SyntaxKind::Identifier => self.stable_binding(ctx, node),
            SyntaxKind::ThisKeyword => this_is_initialized(node),
            SyntaxKind::PrefixUnaryExpression => match node.operator() {
                SyntaxKind::ExclamationToken => self.movable(ctx, node.operand(), duplicable),
                SyntaxKind::MinusToken | SyntaxKind::PlusToken => matches!(
                    node.operand().kind(),
                    SyntaxKind::NumericLiteral | SyntaxKind::BigIntLiteral
                ),
                _ => false,
            },
            SyntaxKind::TypeOfExpression | SyntaxKind::VoidExpression => {
                self.movable(ctx, node.expression(), duplicable)
            }
            SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression => !duplicable,
            SyntaxKind::ArrayLiteralExpression if !duplicable => node.elements().iter().all(|e| {
                e.kind() != SyntaxKind::SpreadElement
                    && (e.kind() == SyntaxKind::OmittedExpression || self.movable(ctx, e, false))
            }),
            SyntaxKind::ObjectLiteralExpression if !duplicable => {
                node.properties().iter().all(|p| match p.kind() {
                    SyntaxKind::PropertyAssignment => {
                        p.name().kind() != SyntaxKind::ComputedPropertyName
                            && p.name().text() != "__proto__"
                            && self.movable(ctx, p.initializer(), false)
                    }
                    SyntaxKind::ShorthandPropertyAssignment => {
                        p.name().text() != "__proto__" && self.stable_binding(ctx, p.name())
                    }
                    _ => false,
                })
            }
            _ => false,
        }
    }

    /// Modules whose namespace object may be observed, and whether an
    /// unresolved dynamic import exists.
    pub fn namespace_observed(&mut self, ctx: &mut Ctx, index: usize) -> bool {
        if self.namespace_observed.is_none() {
            let mut observed = HashSet::new();
            let mut unknown = false;
            for file in 0..ctx.files.len() {
                let nodes = ctx.files[file].nodes.clone();
                for node in nodes {
                    match node.kind() {
                        SyntaxKind::ImportDeclaration => {
                            let clause = node.import_clause();
                            let namespace = clause.is_some()
                                && clause.named_bindings().is_some()
                                && clause.named_bindings().kind() == SyntaxKind::NamespaceImport;
                            if namespace
                                && let Some(target) = Self::target(ctx, node.module_specifier())
                            {
                                observed.insert(target);
                            }
                        }
                        SyntaxKind::ExportDeclaration => {
                            let clause = node.export_clause();
                            if (clause.is_nil() || clause.kind() == SyntaxKind::NamespaceExport)
                                && let Some(target) = Self::target(ctx, node.module_specifier())
                            {
                                observed.insert(target);
                            }
                        }
                        SyntaxKind::CallExpression
                            if node.expression().kind() == SyntaxKind::ImportKeyword =>
                        {
                            match node.arguments().first().and_then(|a| Self::target(ctx, a)) {
                                Some(target) => {
                                    observed.insert(target);
                                }
                                None => unknown = true,
                            }
                        }
                        _ => {}
                    }
                }
            }
            self.namespace_observed = Some((observed, unknown));
        }
        let (observed, unknown) = self.namespace_observed.as_ref().unwrap();
        *unknown || observed.contains(&index)
    }

    /// Whether any identifier of the module has this text.
    pub fn mentions(&mut self, ctx: &Ctx, index: usize, name: &str) -> bool {
        self.identifiers(ctx, index).contains(name)
    }

    /// Every identifier text of a module, including private names.
    fn identifiers(&mut self, ctx: &Ctx, index: usize) -> &HashSet<String> {
        self.identifiers.entry(index).or_insert_with(|| {
            ctx.files[index]
                .nodes
                .iter()
                .filter(|n| {
                    matches!(
                        n.kind(),
                        SyntaxKind::Identifier | SyntaxKind::PrivateIdentifier
                    )
                })
                .map(|n| n.text().to_owned())
                .collect()
        })
    }

    /// The shortest identifiers that no listed module mentions anywhere and
    /// that are not reserved. A name that is never mentioned can neither
    /// capture nor shadow any binding of those modules.
    pub fn fresh_names(
        &mut self,
        ctx: &Ctx,
        files: &[usize],
        taken: &HashSet<String>,
        count: usize,
    ) -> Vec<String> {
        let mut used: HashSet<String> = taken.clone();
        for &file in files {
            used.extend(self.identifiers(ctx, file).iter().cloned());
        }
        let mut names = Vec::new();
        for name in identifier_sequence() {
            if names.len() == count {
                break;
            }
            if !used.contains(&name) && !RESERVED.contains(&name.as_str()) {
                names.push(name);
            }
        }
        names
    }
}

const FIRST: &[u8] = b"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_$";
const REST: &[u8] = b"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_$0123456789";

fn identifier_sequence() -> impl Iterator<Item = String> {
    let one = FIRST.iter().map(|&c| (c as char).to_string());
    let two = FIRST.iter().flat_map(|&a| {
        REST.iter()
            .map(move |&b| format!("{}{}", a as char, b as char))
    });
    let three = FIRST.iter().flat_map(|&a| {
        REST.iter().flat_map(move |&b| {
            REST.iter()
                .map(move |&c| format!("{}{}{}", a as char, b as char, c as char))
        })
    });
    one.chain(two).chain(three)
}

/// Reserved words, strict-mode restrictions, contextual TypeScript keywords
/// and well-known globals that must never be introduced as new bindings.
const RESERVED: &[&str] = &[
    "do",
    "if",
    "in",
    "as",
    "is",
    "of",
    "for",
    "let",
    "new",
    "try",
    "var",
    "get",
    "set",
    "any",
    "NaN",
    "case",
    "else",
    "enum",
    "eval",
    "null",
    "this",
    "true",
    "void",
    "with",
    "type",
    "from",
    "never",
    "async",
    "await",
    "break",
    "catch",
    "class",
    "const",
    "false",
    "super",
    "throw",
    "while",
    "yield",
    "delete",
    "export",
    "import",
    "public",
    "return",
    "static",
    "switch",
    "typeof",
    "default",
    "extends",
    "finally",
    "package",
    "private",
    "continue",
    "debugger",
    "function",
    "arguments",
    "interface",
    "protected",
    "implements",
    "instanceof",
    "undefined",
    "Infinity",
    "declare",
    "module",
    "namespace",
    "unknown",
    "keyof",
    "infer",
    "readonly",
    "abstract",
    "require",
    "exports",
    "global",
    "globalThis",
    "using",
    "accessor",
    "satisfies",
];

/// Whether reading `this` at `node` cannot throw: not in a derived class
/// constructor, where `this` is uninitialized before `super()`.
pub(super) fn this_is_initialized(node: Node) -> bool {
    let container = get_this_container(node, false, false);
    if container.kind() != SyntaxKind::Constructor {
        return true;
    }
    let class = container.parent();
    class
        .heritage_clauses()
        .nodes()
        .iter()
        .all(|clause| clause.token() != SyntaxKind::ExtendsKeyword)
}

/// Whether `node`'s text can be placed in any operand position without
/// parentheses. Optional chains are excluded because a continuation would
/// join the chain.
pub(super) fn primary(node: Node) -> bool {
    if node.flags().intersects(NodeFlags::OptionalChain) {
        return false;
    }
    matches!(
        node.kind(),
        SyntaxKind::Identifier
            | SyntaxKind::StringLiteral
            | SyntaxKind::NoSubstitutionTemplateLiteral
            | SyntaxKind::TemplateExpression
            | SyntaxKind::TrueKeyword
            | SyntaxKind::FalseKeyword
            | SyntaxKind::NullKeyword
            | SyntaxKind::ThisKeyword
            | SyntaxKind::ParenthesizedExpression
            | SyntaxKind::ArrayLiteralExpression
            | SyntaxKind::PropertyAccessExpression
            | SyntaxKind::ElementAccessExpression
            | SyntaxKind::CallExpression
            | SyntaxKind::RegularExpressionLiteral
    )
}

/// Whether `node` is the callee of a call or the tag of a tagged template,
/// where a member expression would change the `this` binding.
pub(super) fn callee_position(node: Node) -> bool {
    let outer = super::super::analysis::walk_up_outer(node);
    let parent = outer.parent();
    (parent.kind() == SyntaxKind::CallExpression && parent.expression() == outer)
        || (parent.kind() == SyntaxKind::TaggedTemplateExpression && parent.tag() == outer)
}

/// Whether a statement-leading replacement that starts with `text` could be
/// absorbed by the previous line through automatic semicolon insertion.
pub(super) fn asi_hazard(ctx: &Ctx, file: usize, node: Node, text: &str) -> bool {
    if !text.starts_with(['(', '[', '`', '+', '-', '/']) {
        return false;
    }
    // Find the statement that starts exactly where the node starts.
    let start = Ctx::start(node);
    let mut current = node;
    loop {
        let parent = current.parent();
        if parent.is_nil() {
            return true;
        }
        if Ctx::start(parent) != start {
            break;
        }
        current = parent;
        if current.kind() == SyntaxKind::ExpressionStatement {
            let before = ctx.files[file].text[..start].trim_end();
            return !(before.is_empty() || before.ends_with([';', '{', '}']));
        }
    }
    false
}

/// Every node of the subtree in preorder.
pub(super) fn subtree(node: Node) -> Vec<Node> {
    let mut nodes = Vec::new();
    collect(node, &mut nodes);
    nodes
}

/// Whether the leading trivia of a statement can be deleted with it.
pub(super) fn removal_start(ctx: &Ctx, file: usize, statement: Node) -> usize {
    let start = Ctx::start(statement);
    let pos = statement.pos() as usize;
    let trivia = &ctx.files[file].text[pos..start];
    if pos == 0 || trivia.contains("@ts-") || trivia.contains("///") || trivia.contains("#!") {
        start
    } else {
        pos
    }
}

/// Identifier nodes that refer to bindings by scope (not property names,
/// keys, labels or declaration names).
pub(super) fn scope_reference(node: Node) -> bool {
    if node.kind() != SyntaxKind::Identifier {
        return false;
    }
    let parent = node.parent();
    match parent.kind() {
        SyntaxKind::PropertyAccessExpression => parent.expression() == node,
        SyntaxKind::QualifiedName => parent.left() == node,
        SyntaxKind::PropertyAssignment
        | SyntaxKind::PropertySignature
        | SyntaxKind::MethodSignature
        | SyntaxKind::MethodDeclaration
        | SyntaxKind::PropertyDeclaration
        | SyntaxKind::GetAccessor
        | SyntaxKind::SetAccessor
        | SyntaxKind::EnumMember
        | SyntaxKind::Parameter
        | SyntaxKind::TypeParameter
        | SyntaxKind::VariableDeclaration
        | SyntaxKind::FunctionDeclaration
        | SyntaxKind::FunctionExpression
        | SyntaxKind::ClassDeclaration
        | SyntaxKind::ClassExpression
        | SyntaxKind::NamedTupleMember => parent.name() != node,
        SyntaxKind::BindingElement => parent.name() != node && parent.property_name() != node,
        SyntaxKind::JsxAttribute
        | SyntaxKind::LabeledStatement
        | SyntaxKind::BreakStatement
        | SyntaxKind::ContinueStatement => false,
        _ => true,
    }
}

/// Whether an expression replacing `node` may be any assignment expression
/// (everything but a comma sequence) without parentheses. `text` is the
/// replacement, checked for statement-start ambiguities.
pub(super) fn accepts_assignment_expression(node: Node, text: &str) -> bool {
    let parent = node.parent();
    let statement_start = text.starts_with('{')
        || text.starts_with("function")
        || text.starts_with("class")
        || text.starts_with("async")
        || text.starts_with("let");
    match parent.kind() {
        SyntaxKind::ParenthesizedExpression
        | SyntaxKind::SpreadElement
        | SyntaxKind::SpreadAssignment
        | SyntaxKind::TemplateSpan
        | SyntaxKind::ComputedPropertyName
        | SyntaxKind::JsxExpression
        | SyntaxKind::ReturnStatement
        | SyntaxKind::ThrowStatement
        | SyntaxKind::IfStatement
        | SyntaxKind::WhileStatement
        | SyntaxKind::DoStatement
        | SyntaxKind::SwitchStatement
        | SyntaxKind::CaseClause => parent.expression() == node,
        SyntaxKind::ArrayLiteralExpression => true,
        SyntaxKind::CallExpression | SyntaxKind::NewExpression => parent.expression() != node,
        SyntaxKind::VariableDeclaration
        | SyntaxKind::PropertyAssignment
        | SyntaxKind::PropertyDeclaration
        | SyntaxKind::Parameter
        | SyntaxKind::BindingElement => parent.initializer() == node,
        SyntaxKind::ElementAccessExpression => parent.argument_expression() == node,
        SyntaxKind::ConditionalExpression => parent.condition() != node,
        SyntaxKind::BinaryExpression => {
            parent.right() == node
                && (parent.operator_token().kind() == SyntaxKind::EqualsToken
                    || is_compound_assignment(parent.operator_token().kind()))
        }
        SyntaxKind::ExpressionStatement | SyntaxKind::ExportAssignment => !statement_start,
        SyntaxKind::ArrowFunction => parent.body() == node && !text.starts_with('{'),
        _ => false,
    }
}

/// Operator precedence of an expression (higher binds tighter), following
/// the ECMAScript grammar levels used by the TypeScript printer.
pub(super) fn precedence(node: Node) -> u8 {
    match node.kind() {
        SyntaxKind::BinaryExpression => binary_precedence(node.operator_token().kind()),
        SyntaxKind::ConditionalExpression => 4,
        SyntaxKind::PrefixUnaryExpression
        | SyntaxKind::TypeOfExpression
        | SyntaxKind::VoidExpression
        | SyntaxKind::DeleteExpression
        | SyntaxKind::AwaitExpression => 16,
        _ if primary(node) => 20,
        _ => 0,
    }
}

fn binary_precedence(operator: SyntaxKind) -> u8 {
    match operator {
        SyntaxKind::CommaToken => 0,
        SyntaxKind::QuestionQuestionToken => 4,
        SyntaxKind::BarBarToken => 5,
        SyntaxKind::AmpersandAmpersandToken => 6,
        SyntaxKind::BarToken => 7,
        SyntaxKind::CaretToken => 8,
        SyntaxKind::AmpersandToken => 9,
        SyntaxKind::EqualsEqualsToken
        | SyntaxKind::ExclamationEqualsToken
        | SyntaxKind::EqualsEqualsEqualsToken
        | SyntaxKind::ExclamationEqualsEqualsToken => 10,
        SyntaxKind::LessThanToken
        | SyntaxKind::GreaterThanToken
        | SyntaxKind::LessThanEqualsToken
        | SyntaxKind::GreaterThanEqualsToken
        | SyntaxKind::InstanceOfKeyword
        | SyntaxKind::InKeyword => 11,
        SyntaxKind::LessThanLessThanToken
        | SyntaxKind::GreaterThanGreaterThanToken
        | SyntaxKind::GreaterThanGreaterThanGreaterThanToken => 12,
        SyntaxKind::PlusToken | SyntaxKind::MinusToken => 13,
        SyntaxKind::AsteriskToken | SyntaxKind::SlashToken | SyntaxKind::PercentToken => 14,
        SyntaxKind::AsteriskAsteriskToken => 15,
        // Assignments and anything unknown bind loosest.
        _ => 1,
    }
}

/// Whether an expression `replacement` can replace `node` as an operand of
/// a binary or conditional parent without parentheses.
pub(super) fn fits_operand(node: Node, replacement: Node) -> bool {
    let parent = node.parent();
    let own = precedence(replacement);
    match parent.kind() {
        SyntaxKind::BinaryExpression => {
            let operator = parent.operator_token().kind();
            let outer = binary_precedence(operator);
            if outer <= 1 {
                return false;
            }
            let logical = |k: SyntaxKind| {
                matches!(
                    k,
                    SyntaxKind::BarBarToken | SyntaxKind::AmpersandAmpersandToken
                )
            };
            if replacement.kind() == SyntaxKind::BinaryExpression {
                let inner = replacement.operator_token().kind();
                // `??` cannot mix with `||`/`&&` without parentheses.
                if (inner == SyntaxKind::QuestionQuestionToken && logical(operator))
                    || (operator == SyntaxKind::QuestionQuestionToken && logical(inner))
                {
                    return false;
                }
            }
            if own == 16 && operator == SyntaxKind::AsteriskAsteriskToken {
                // A unary operand of `**` is a syntax error.
                return false;
            }
            let right_associative = operator == SyntaxKind::AsteriskAsteriskToken;
            if parent.left() == node {
                own > outer || (own == outer && !right_associative)
            } else {
                own > outer || (own == outer && right_associative)
            }
        }
        SyntaxKind::ConditionalExpression if parent.condition() == node => own > 4,
        _ => false,
    }
}

/// Whether the expression is a top-level comma sequence.
pub(super) fn comma(node: Node) -> bool {
    node.kind() == SyntaxKind::BinaryExpression
        && node.operator_token().kind() == SyntaxKind::CommaToken
}

/// Whether a top-level statement makes its file an ES module for
/// TypeScript (import or export syntax).
fn module_indicator(statement: Node) -> bool {
    matches!(
        statement.kind(),
        SyntaxKind::ImportDeclaration
            | SyntaxKind::ImportEqualsDeclaration
            | SyntaxKind::ExportDeclaration
            | SyntaxKind::ExportAssignment
    ) || has_syntactic_modifier(statement, ModifierFlags::Export)
}

/// Appends `export {};` to modules whose every import and export statement
/// the edits delete, so TypeScript keeps treating them as modules rather
/// than global scripts.
pub(super) fn keep_module_syntax(ctx: &Ctx, edits: &mut Vec<crate::plan::FileEdit>) {
    let mut files: Vec<String> = edits.iter().map(|e| e.file.clone()).collect();
    files.sort();
    files.dedup();
    for name in files {
        let Some(index) = ctx.file_of(&name) else {
            continue;
        };
        let remaining = ctx.files[index]
            .node
            .statements()
            .iter()
            .filter(|&statement| module_indicator(statement))
            .any(|statement| {
                let (start, end) = (Ctx::start(statement), statement.end() as usize);
                !edits.iter().any(|e| {
                    e.file == name
                        && e.edit.replacement.is_empty()
                        && e.edit.start <= start
                        && end <= e.edit.end
                })
            });
        if !remaining {
            let end = ctx.files[index].text.len();
            edits.push(crate::plan::FileEdit::insert(&name, end, "\nexport {};\n"));
        }
    }
}

/// Whether an import or export-from declaration survives TypeScript's
/// import elision and so evaluates its target at runtime. Unknown cases
/// count as runtime dependencies.
pub(super) fn runtime_dependency(ctx: &mut Ctx, file: usize, statement: Node) -> bool {
    if statement.module_specifier().is_nil() {
        return false;
    }
    if statement.kind() == SyntaxKind::ImportDeclaration {
        let clause = statement.import_clause();
        if clause.is_some() && clause.is_type_only() {
            return false;
        }
    } else if statement.is_type_only() {
        return false;
    }
    if ctx.files[file].js
        || ctx
            .checker
            .compiler_options
            .verbatim_module_syntax
            .is_true()
    {
        return true;
    }
    let value_used = |ctx: &mut Ctx, name: Node| {
        let symbol = ctx.resolve(name);
        symbol.is_nil()
            || ctx.refs(symbol).iter().any(|r| {
                r.file == file
                    && !matches!(
                        r.kind,
                        RefKind::Type | RefKind::Alias | RefKind::Declaration
                    )
            })
    };
    if statement.kind() == SyntaxKind::ImportDeclaration {
        let clause = statement.import_clause();
        if clause.is_nil() {
            return true;
        }
        if clause.is_type_only() {
            return false;
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
                for element in bindings.elements().iter() {
                    if !element.is_type_only() {
                        names.push(element.name());
                    }
                }
            }
        }
        return names.into_iter().any(|name| value_used(ctx, name));
    }
    let clause = statement.export_clause();
    if clause.is_nil() || clause.kind() != SyntaxKind::NamedExports {
        return true;
    }
    clause.elements().iter().any(|element| {
        if element.is_type_only() {
            return false;
        }
        let symbol = ctx.resolve(element.name());
        symbol.is_nil()
            || ctx
                .checker
                .get_symbol_flags(symbol)
                .intersects(SymbolFlags::Value)
    })
}

/// Strongly connected component index of every node (Kosaraju).
fn components(edges: &[Vec<usize>]) -> Vec<usize> {
    let count = edges.len();
    let mut order = Vec::with_capacity(count);
    let mut seen = vec![false; count];
    for root in 0..count {
        if seen[root] {
            continue;
        }
        seen[root] = true;
        let mut stack = vec![(root, 0usize)];
        while let Some((node, next)) = stack.pop() {
            if next < edges[node].len() {
                stack.push((node, next + 1));
                let child = edges[node][next];
                if !seen[child] {
                    seen[child] = true;
                    stack.push((child, 0));
                }
            } else {
                order.push(node);
            }
        }
    }
    let mut reverse = vec![Vec::new(); count];
    for (from, targets) in edges.iter().enumerate() {
        for &to in targets {
            reverse[to].push(from);
        }
    }
    let mut component = vec![usize::MAX; count];
    let mut next_id = 0;
    for &root in order.iter().rev() {
        if component[root] != usize::MAX {
            continue;
        }
        component[root] = next_id;
        let mut stack = vec![root];
        while let Some(node) = stack.pop() {
            for &previous in &reverse[node] {
                if component[previous] == usize::MAX {
                    component[previous] = next_id;
                    stack.push(previous);
                }
            }
        }
        next_id += 1;
    }
    component
}

/// Whether the expression's type depends on its contextual type: array,
/// object and function literals, possibly behind conditionals, logical
/// operators, commas and parentheses.
pub(super) fn context_sensitive(node: Node) -> bool {
    match node.kind() {
        SyntaxKind::ParenthesizedExpression => context_sensitive(node.expression()),
        SyntaxKind::ArrayLiteralExpression
        | SyntaxKind::ObjectLiteralExpression
        | SyntaxKind::ArrowFunction
        | SyntaxKind::FunctionExpression
        | SyntaxKind::ClassExpression => true,
        SyntaxKind::ConditionalExpression => {
            context_sensitive(node.when_true()) || context_sensitive(node.when_false())
        }
        SyntaxKind::BinaryExpression => match node.operator_token().kind() {
            SyntaxKind::AmpersandAmpersandToken
            | SyntaxKind::BarBarToken
            | SyntaxKind::QuestionQuestionToken => {
                context_sensitive(node.left()) || context_sensitive(node.right())
            }
            SyntaxKind::CommaToken => context_sensitive(node.right()),
            _ => false,
        },
        _ => false,
    }
}
