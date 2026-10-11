// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `residual-dependency-removal`: deletes functions, classes, variables and
//! enums of application TypeScript modules that have no remaining runtime
//! or type reference, together with the import and export specifiers that
//! only forward them. Declarations that are referenced solely from inside
//! other removed declarations (helpers, mutual recursion, chains left by
//! earlier transformations) are removed in the same bundle.
//!
//! Proof obligations:
//! - The binding is internal: declared only in application TypeScript, outside
//!   public entry exports, never referenced from application JavaScript, not
//!   ambient and not merged with other declarations.
//! - The module is an ES module (script declarations are globals), and when the
//!   declaration is exported, no namespace object of a module that exposes it
//!   can be observed by name: entries, JavaScript importers, dynamic imports,
//!   `export * as` and escaping `import * as` bindings make every export
//!   reachable.
//! - Every reference is the declaration itself, an import or export specifier
//!   that is removed with it, or lies inside a removed declaration. Type
//!   references keep the declaration so types stay valid. `export default`
//!   declarations stay.
//! - Evaluating the declaration has no effect: function declarations, variables
//!   with pure initializers (`Ctx::is_pure`), enums with literal initializers
//!   and classes without decorators, computed keys, static blocks or impure
//!   static initializers whose heritage is absent or an earlier class
//!   declaration of the same statement list.
//! - Module initialization is preserved: when a removed specifier was the last
//!   runtime use of an import, the pipeline's initialization guard keeps a bare
//!   import unless the target initializes without effects. A removed re-export
//!   declaration becomes a bare import of its target unless that target
//!   initializes without effects.
use std::collections::{BTreeMap, BTreeSet};

use anyhow::Result;
use ts_goport::prelude::*;

use super::{
    super::{
        analysis::{Ctx, RefKind},
        token_start,
    },
    deadcode_support::{Exposure, Keys, list_removals, module_index, removal_start, within},
};
use crate::{
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::ResidualDependencyRemoval;

/// Names that the classic JSX transform references implicitly.
const JSX_NAMES: &[&str] = &["React", "h", "Fragment", "jsx", "jsxs", "jsxDEV"];

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum UnitKind {
    Function,
    Class,
    Variable,
    Enum,
}

impl UnitKind {
    fn as_str(self) -> &'static str {
        match self {
            UnitKind::Function => "function",
            UnitKind::Class => "class",
            UnitKind::Variable => "variable",
            UnitKind::Enum => "enum",
        }
    }
}

struct Unit {
    file: usize,
    symbol: SymbolId,
    kind: UnitKind,
    /// The declaration: implementation function, class, variable
    /// declarator or enum.
    node: Node,
    /// Whole statements deleted with the unit (function overloads and
    /// implementation, class, enum). Variables are deleted per declarator.
    statements: Vec<Node>,
    /// Source ranges whose references disappear with the unit.
    extents: Vec<(usize, usize)>,
    key: String,
}

pub(in crate::compiler) fn plan(ctx: &mut Ctx, _enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let exposure = Exposure::compute(ctx);
    let mut keys = Keys::default();
    let mut units = Vec::new();
    let mut skips = 0usize;
    for file in 0..ctx.files.len() {
        crate::app::check_cancelled()?;
        if ctx.files[file].js || !is_external_module(ctx.files[file].node) {
            continue;
        }
        let nodes = ctx.files[file].nodes.clone();
        for statement in nodes {
            let list = statement.parent();
            if list.is_nil() || !matches!(list.kind(), SyntaxKind::SourceFile | SyntaxKind::Block) {
                continue;
            }
            for unit in collect_units(ctx, file, statement, &mut keys) {
                match eligibility(ctx, &exposure, &unit) {
                    None => units.push(unit),
                    Some(reason) => {
                        if !runtime_used(ctx, &unit) {
                            skips += 1;
                            out.skips.push(ctx.skip(
                                PASS,
                                unit.key.clone(),
                                file,
                                unit.node,
                                reason,
                            ));
                        }
                    }
                }
            }
        }
    }
    let candidates = units.len();
    let groups = solve(ctx, &exposure, &units, out);
    let mut planned = 0usize;
    for group in groups {
        if let Some(candidate) = bundle(ctx, &units, &group) {
            out.candidates.push(candidate);
            planned += 1;
        }
    }
    tracing::debug!(
        pass = %PASS,
        units = candidates,
        bundles = planned,
        skipped = skips,
        "residual dependencies planned"
    );
    Ok(())
}

/// Declaration units of one statement.
fn collect_units(ctx: &mut Ctx, file: usize, statement: Node, keys: &mut Keys) -> Vec<Unit> {
    let name = ctx.files[file].name.clone();
    let mut units = Vec::new();
    let mut unit = |ctx: &mut Ctx, kind: UnitKind, node: Node, statements: Vec<Node>| {
        let symbol = ctx.resolve(node.name());
        if symbol.is_nil() {
            return;
        }
        let extents = if kind == UnitKind::Variable {
            vec![(token_start(node), node.end() as usize)]
        } else {
            statements
                .iter()
                .map(|&s| (token_start(s), s.end() as usize))
                .collect()
        };
        units.push(Unit {
            file,
            symbol,
            kind,
            node,
            statements,
            extents,
            key: keys.unique(format!(
                "residual:{}:{name}:{}",
                kind.as_str(),
                node.name().text()
            )),
        });
    };
    match statement.kind() {
        SyntaxKind::FunctionDeclaration
            if statement.body().is_some() && statement.name().is_some() =>
        {
            let symbol = ctx.resolve(statement.name());
            let declarations: Vec<Node> = if symbol.is_nil() {
                vec![statement]
            } else {
                ctx.checker.sym(symbol).declarations.to_vec()
            };
            unit(ctx, UnitKind::Function, statement, declarations);
        }
        SyntaxKind::ClassDeclaration if statement.name().is_some() => {
            unit(ctx, UnitKind::Class, statement, vec![statement]);
        }
        SyntaxKind::EnumDeclaration => {
            unit(ctx, UnitKind::Enum, statement, vec![statement]);
        }
        SyntaxKind::VariableStatement => {
            let list = statement.declaration_list();
            for declaration in list.declarations().nodes().iter() {
                if declaration.name().kind() == SyntaxKind::Identifier {
                    unit(ctx, UnitKind::Variable, declaration, Vec::new());
                }
            }
        }
        _ => {}
    }
    units
}

/// The static reason a unit can never be removed, if any.
fn eligibility(ctx: &mut Ctx, exposure: &Exposure, unit: &Unit) -> Option<Reason> {
    let symbol = unit.symbol;
    if ctx.public.contains(&symbol) {
        return Some(Reason::PublicExport);
    }
    if ctx.js_referenced.contains(&symbol) || !ctx.is_internal(symbol) {
        return Some(Reason::ExternalBoundary);
    }
    if JSX_NAMES.contains(&unit.node.name().text())
        || ctx.checker.compiler_options.jsx_factory.split('.').next()
            == Some(unit.node.name().text())
    {
        return Some(Reason::ObservableOperation);
    }
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    let statement = statement_of(unit);
    let expected = match unit.kind {
        UnitKind::Function => declarations.iter().all(|d| {
            d.kind() == SyntaxKind::FunctionDeclaration && d.parent() == unit.node.parent()
        }),
        _ => declarations.len() == 1 && declarations[0] == unit.node,
    };
    if !expected {
        return Some(Reason::DeclarationMerging);
    }
    if statement.flags().intersects(NodeFlags::AMBIENT)
        || has_syntactic_modifier(statement, ModifierFlags::AMBIENT)
    {
        return Some(Reason::DeclarationMerging);
    }
    if has_syntactic_modifier(statement, ModifierFlags::DEFAULT) {
        return Some(Reason::UnsupportedSyntax);
    }
    if has_syntactic_modifier(statement, ModifierFlags::EXPORT) && exposure.observed(unit.file) {
        return Some(Reason::Reflection);
    }
    let pure = match unit.kind {
        UnitKind::Function => true,
        UnitKind::Variable => {
            // `using` and `await using` dispose their value at scope exit.
            let scoped = unit.node.parent().flags() & NodeFlags::BLOCK_SCOPED;
            scoped != NodeFlags::USING
                && scoped != NodeFlags::AWAIT_USING
                && (unit.node.initializer().is_nil() || ctx.is_pure(unit.node.initializer()))
        }
        UnitKind::Enum => unit.node.members().iter().all(|member| {
            let initializer = member.initializer();
            initializer.is_nil()
                || matches!(
                    super::super::analysis::skip_outer(initializer).kind(),
                    SyntaxKind::NumericLiteral | SyntaxKind::StringLiteral
                )
        }),
        UnitKind::Class => class_definition_pure(ctx, unit.node),
    };
    if !pure {
        return Some(Reason::SideEffects);
    }
    None
}

/// The statement that carries modifiers for the unit.
fn statement_of(unit: &Unit) -> Node {
    match unit.kind {
        UnitKind::Variable => unit.node.parent().parent(),
        _ => unit.node,
    }
}

/// Defining the class evaluates nothing observable.
fn class_definition_pure(ctx: &mut Ctx, class: Node) -> bool {
    if !class.decorators().is_empty() {
        return false;
    }
    for clause in class.heritage_clauses().nodes().iter() {
        if clause.token() != SyntaxKind::ExtendsKeyword {
            continue;
        }
        for base in clause.types().nodes().iter() {
            let expression = super::super::analysis::skip_outer(base.expression());
            if expression.kind() != SyntaxKind::Identifier {
                return false;
            }
            // An earlier class declaration of the same statement list is
            // initialized and constructible when this class is defined.
            let symbol = ctx.resolve(expression);
            if symbol.is_nil() {
                return false;
            }
            let declaration = ctx.checker.sym(symbol).value_declaration;
            if declaration.is_nil()
                || declaration.kind() != SyntaxKind::ClassDeclaration
                || declaration.parent() != class.parent()
                || declaration.end() > class.pos()
            {
                return false;
            }
        }
    }
    class.members().iter().all(|member| {
        if !member.decorators().is_empty() {
            return false;
        }
        if member.kind() == SyntaxKind::Constructor
            && member
                .parameters()
                .iter()
                .any(|p| !p.decorators().is_empty())
        {
            return false;
        }
        let name = member.name();
        if name.is_some() && name.kind() == SyntaxKind::ComputedPropertyName {
            return false;
        }
        match member.kind() {
            SyntaxKind::ClassStaticBlockDeclaration => false,
            SyntaxKind::PropertyDeclaration if is_static(member) => {
                member.initializer().is_nil() || ctx.is_pure(member.initializer())
            }
            _ => true,
        }
    })
}

/// Whether the unit has a runtime reference outside its own extent; such
/// units are ordinary live code and are not reported as skipped.
fn runtime_used(ctx: &Ctx, unit: &Unit) -> bool {
    ctx.refs(unit.symbol).iter().any(|r| {
        matches!(
            effective_kind(r),
            RefKind::Read | RefKind::Call | RefKind::New | RefKind::Write
        ) && !(r.file == unit.file && unit.extents.iter().any(|&e| within(r.node, e)))
    })
}

/// Greatest fixed point of removable units: a unit stays removable while
/// every reference lies in its own extent, in a removable unit's extent or
/// in a removable alias specifier. Returns connected groups.
fn solve(ctx: &mut Ctx, exposure: &Exposure, units: &[Unit], out: &mut Plan) -> Vec<Vec<usize>> {
    let mut alive = vec![true; units.len()];
    let mut by_file: BTreeMap<usize, Vec<usize>> = BTreeMap::new();
    for (index, unit) in units.iter().enumerate() {
        by_file.entry(unit.file).or_default().push(index);
    }
    let mut blockers: Vec<Option<Reason>> = vec![None; units.len()];
    let mut edges: Vec<BTreeSet<usize>> = vec![BTreeSet::new(); units.len()];
    let mut changed = true;
    while changed {
        changed = false;
        for index in 0..units.len() {
            if !alive[index] {
                continue;
            }
            edges[index].clear();
            let unit = &units[index];
            let mut blocked = None;
            for r in ctx.refs(unit.symbol) {
                if r.file == unit.file && unit.extents.iter().any(|&e| within(r.node, e)) {
                    continue;
                }
                match effective_kind(r) {
                    RefKind::Alias => match r.node.parent().kind() {
                        SyntaxKind::ImportSpecifier => continue,
                        SyntaxKind::ExportSpecifier if !exposure.observed(r.file) => continue,
                        SyntaxKind::ExportSpecifier => {
                            blocked = Some(Reason::Reflection);
                            break;
                        }
                        _ => {
                            blocked = Some(Reason::UnsupportedSyntax);
                            break;
                        }
                    },
                    RefKind::Declaration => {
                        blocked = Some(Reason::DeclarationMerging);
                        break;
                    }
                    kind => {
                        let container = by_file.get(&r.file).and_then(|candidates| {
                            candidates.iter().copied().find(|&other| {
                                other != index
                                    && alive[other]
                                    && units[other].extents.iter().any(|&e| within(r.node, e))
                            })
                        });
                        match container {
                            Some(other) => {
                                edges[index].insert(other);
                            }
                            None => {
                                blocked = Some(if kind == RefKind::Type {
                                    Reason::TypeConstraint
                                } else {
                                    Reason::Referenced
                                });
                                break;
                            }
                        }
                    }
                }
            }
            if let Some(reason) = blocked {
                alive[index] = false;
                blockers[index] = Some(reason);
                changed = true;
            }
        }
    }
    // Report removable-looking declarations that only types or
    // namespace exposure keep alive.
    for (index, unit) in units.iter().enumerate() {
        if let Some(reason @ (Reason::TypeConstraint | Reason::Reflection)) = blockers[index]
            && !runtime_used(ctx, unit)
        {
            out.skips
                .push(ctx.skip(PASS, unit.key.clone(), unit.file, unit.node, reason));
        }
    }
    // Union removable units that reference each other, and nested units
    // with their enclosing units.
    let mut parent: Vec<usize> = (0..units.len()).collect();
    fn find(parent: &mut [usize], mut i: usize) -> usize {
        while parent[i] != i {
            parent[i] = parent[parent[i]];
            i = parent[i];
        }
        i
    }
    for index in 0..units.len() {
        if !alive[index] {
            continue;
        }
        for &other in &edges[index] {
            let (a, b) = (find(&mut parent, index), find(&mut parent, other));
            parent[a.max(b)] = a.min(b);
        }
        let unit = &units[index];
        for &other in &by_file[&unit.file] {
            if other != index
                && alive[other]
                && units[other]
                    .extents
                    .iter()
                    .any(|&e| unit.extents.iter().all(|&own| own.0 >= e.0 && own.1 <= e.1))
            {
                let (a, b) = (find(&mut parent, index), find(&mut parent, other));
                parent[a.max(b)] = a.min(b);
            }
        }
    }
    let mut groups: BTreeMap<usize, Vec<usize>> = BTreeMap::new();
    for index in 0..units.len() {
        if alive[index] {
            let root = find(&mut parent, index);
            groups.entry(root).or_default().push(index);
        }
    }
    groups.into_values().collect()
}

/// The shared index classifies every declaration-position name as a
/// declaration. Two of them use another binding: export specifier names
/// forward it like import specifiers, and shorthand property names read it.
fn effective_kind(r: &super::super::analysis::Ref) -> RefKind {
    if r.kind != RefKind::Declaration {
        return r.kind;
    }
    match r.node.parent().kind() {
        SyntaxKind::ExportSpecifier => RefKind::Alias,
        SyntaxKind::ShorthandPropertyAssignment => RefKind::Read,
        _ => r.kind,
    }
}

/// One candidate deleting a group of units and their alias specifiers.
fn bundle(ctx: &mut Ctx, units: &[Unit], group: &[usize]) -> Option<Candidate> {
    let first = &units[group[0]];
    let mut candidate = Candidate::new(
        PASS,
        first.key.clone(),
        ctx.location(first.file, first.node),
    );
    // Units nested in another unit of the group disappear with it.
    let nested = |index: usize| {
        group.iter().any(|&other| {
            other != index
                && units[other].file == units[index].file
                && units[other].extents.iter().any(|&e| {
                    units[index]
                        .extents
                        .iter()
                        .all(|&own| own.0 >= e.0 && own.1 <= e.1 && own != e)
                })
        })
    };
    let mut variables: BTreeMap<(usize, Node), BTreeSet<Node>> = BTreeMap::new();
    let mut imports: BTreeMap<(usize, Node), BTreeSet<Node>> = BTreeMap::new();
    let mut exports: BTreeMap<(usize, Node), BTreeSet<Node>> = BTreeMap::new();
    for &index in group {
        let unit = &units[index];
        for r in ctx.refs(unit.symbol) {
            if effective_kind(r) != RefKind::Alias {
                continue;
            }
            let specifier = r.node.parent();
            let (map, declaration) = match specifier.kind() {
                SyntaxKind::ImportSpecifier => (&mut imports, specifier.parent().parent().parent()),
                SyntaxKind::ExportSpecifier => (&mut exports, specifier.parent().parent()),
                _ => continue,
            };
            map.entry((r.file, declaration))
                .or_default()
                .insert(specifier);
        }
        if nested(index) {
            continue;
        }
        let text = &ctx.files[unit.file].text;
        let name = &ctx.files[unit.file].name;
        match unit.kind {
            UnitKind::Variable => {
                variables
                    .entry((unit.file, unit.node.parent().parent()))
                    .or_default()
                    .insert(unit.node);
            }
            _ => {
                for &statement in &unit.statements {
                    candidate.edits.push(FileEdit::delete(
                        name,
                        removal_start(text, statement),
                        statement.end() as usize,
                    ));
                }
            }
        }
    }
    for ((file, statement), dead) in variables {
        let elements: Vec<Node> = statement
            .declaration_list()
            .declarations()
            .nodes()
            .iter()
            .collect();
        remove_list(ctx, &mut candidate, file, statement, &elements, &dead, None);
    }
    for ((file, declaration), dead) in imports {
        let clause = declaration.import_clause();
        let elements: Vec<Node> = clause.named_bindings().elements().iter().collect();
        if dead.len() == elements.len() && clause.name().is_some() {
            // `import D, { dead } from` keeps the default import.
            let name = ctx.files[file].name.clone();
            candidate.edits.push(FileEdit::delete(
                &name,
                clause.name().end() as usize,
                clause.named_bindings().end() as usize,
            ));
            continue;
        }
        remove_list(
            ctx,
            &mut candidate,
            file,
            declaration,
            &elements,
            &dead,
            None,
        );
    }
    for ((file, declaration), dead) in exports {
        let elements: Vec<Node> = declaration.export_clause().elements().iter().collect();
        let specifier = declaration.module_specifier();
        // Deleting a runtime re-export also deletes the module dependency.
        let replacement = if specifier.is_some()
            && !declaration.is_type_only()
            && elements.iter().any(|e| !e.is_type_only())
        {
            let droppable = module_index(ctx, specifier)
                .is_some_and(|target| ctx.module_initialization_pure(target));
            (!droppable).then(|| format!("import {};", ctx.text(file, specifier)))
        } else {
            None
        };
        remove_list(
            ctx,
            &mut candidate,
            file,
            declaration,
            &elements,
            &dead,
            replacement,
        );
    }
    (!candidate.edits.is_empty()).then_some(candidate)
}

/// Deletes the dead elements of a list statement, or the whole statement
/// (optionally replaced) when every element is dead.
fn remove_list(
    ctx: &Ctx,
    candidate: &mut Candidate,
    file: usize,
    statement: Node,
    elements: &[Node],
    dead: &BTreeSet<Node>,
    replacement: Option<String>,
) {
    let name = &ctx.files[file].name;
    let flags: Vec<bool> = elements.iter().map(|e| dead.contains(e)).collect();
    if flags.iter().all(|&d| d) {
        match replacement {
            Some(text) => candidate.edits.push(FileEdit::replace(
                name,
                token_start(statement),
                statement.end() as usize,
                text,
            )),
            None => candidate.edits.push(FileEdit::delete(
                name,
                removal_start(&ctx.files[file].text, statement),
                statement.end() as usize,
            )),
        }
        return;
    }
    for (start, end) in list_removals(elements, &flags) {
        candidate.edits.push(FileEdit::delete(name, start, end));
    }
}
