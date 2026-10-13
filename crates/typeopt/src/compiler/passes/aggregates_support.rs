// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Syntax and module-graph helpers shared by `aggregate-elimination` and
//! `discriminant-compaction`. Everything here is derived from the current
//! program text; nothing is cached across rounds.
use std::collections::{BTreeSet, HashSet};

use super::super::analysis::{Ctx, skip_outer};
use crate::compiler::api::*;

/// The nearest enclosing node that delimits when code runs: a function-like
/// declaration, a class static block or field initializer, or the source
/// file. Code in a different runner may execute at another time than code
/// in the runner of a declaration.
pub(super) fn runner(mut node: Node) -> Node {
    node = node.parent();
    while node.is_some() {
        if matches!(
            node.kind(),
            SyntaxKind::FunctionDeclaration
                | SyntaxKind::FunctionExpression
                | SyntaxKind::ArrowFunction
                | SyntaxKind::MethodDeclaration
                | SyntaxKind::Constructor
                | SyntaxKind::GetAccessor
                | SyntaxKind::SetAccessor
                | SyntaxKind::ClassStaticBlockDeclaration
                | SyntaxKind::PropertyDeclaration
                | SyntaxKind::SourceFile
        ) {
            return node;
        }
        node = node.parent();
    }
    node
}

/// Whether `node` lies inside `ancestor` (or is it).
pub(super) fn within(mut node: Node, ancestor: Node) -> bool {
    while node.is_some() {
        if node == ancestor {
            return true;
        }
        node = node.parent();
    }
    false
}

/// An anonymous function or class definition receives the name of the
/// binding or property it initializes (`NamedEvaluation`). Moving it to a
/// binding with another name changes the observable `.name`.
pub(super) fn is_anonymous_definition(expression: Node) -> bool {
    let inner = skip_outer(expression);
    match inner.kind() {
        SyntaxKind::ArrowFunction => true,
        SyntaxKind::FunctionExpression | SyntaxKind::ClassExpression => inner.name().is_nil(),
        _ => false,
    }
}

/// Every identifier spelling in a file. A generated binding name that does
/// not occur here can neither shadow nor be shadowed by any binding the file
/// refers to.
pub(super) fn identifier_texts(ctx: &Ctx, file: usize) -> HashSet<String> {
    ctx.files[file]
        .nodes
        .iter()
        .filter(|n| n.kind() == SyntaxKind::Identifier && !property_name_only(**n))
        .map(|n| n.text().to_owned())
        .collect()
}

/// Whether an identifier only names a property, which never resolves
/// through the scope chain. Shorthand properties are references too and do
/// not qualify.
fn property_name_only(node: Node) -> bool {
    let parent = node.parent();
    match parent.kind() {
        SyntaxKind::PropertyAccessExpression => parent.name() == node,
        SyntaxKind::QualifiedName => parent.right() == node,
        SyntaxKind::PropertyAssignment
        | SyntaxKind::PropertySignature
        | SyntaxKind::PropertyDeclaration
        | SyntaxKind::MethodDeclaration
        | SyntaxKind::MethodSignature
        | SyntaxKind::GetAccessor
        | SyntaxKind::SetAccessor
        | SyntaxKind::EnumMember => parent.name() == node,
        SyntaxKind::BindingElement => parent.property_name() == node,
        _ => false,
    }
}

/// Words that must not name a generated binding: reserved and
/// strict-mode-reserved words, contextual keywords and globals whose
/// shadowing would be confusing even when unused.
const RESERVED: &[&str] = &[
    "arguments",
    "as",
    "async",
    "await",
    "break",
    "case",
    "catch",
    "class",
    "const",
    "continue",
    "debugger",
    "declare",
    "default",
    "delete",
    "do",
    "else",
    "enum",
    "eval",
    "export",
    "exports",
    "extends",
    "false",
    "finally",
    "for",
    "from",
    "function",
    "get",
    "globalThis",
    "if",
    "implements",
    "import",
    "in",
    "Infinity",
    "instanceof",
    "interface",
    "let",
    "module",
    "NaN",
    "namespace",
    "new",
    "null",
    "of",
    "package",
    "private",
    "protected",
    "public",
    "require",
    "return",
    "satisfies",
    "set",
    "static",
    "super",
    "switch",
    "this",
    "throw",
    "true",
    "try",
    "type",
    "typeof",
    "undefined",
    "var",
    "void",
    "while",
    "with",
    "yield",
];

/// Picks the first of `preferred` that is a plain identifier, not reserved
/// and not in `used`; otherwise the last preference plus the smallest free
/// numeric suffix. The chosen name is reserved in `used`.
pub(super) fn fresh_name(preferred: &[String], used: &mut HashSet<String>) -> String {
    let available = |name: &str, used: &HashSet<String>| {
        is_plain_identifier(name) && !RESERVED.contains(&name) && !used.contains(name)
    };
    let name = match preferred.iter().find(|name| available(name, used)) {
        Some(name) => name.clone(),
        None => {
            let base = preferred.last().map_or("value", String::as_str);
            let mut counter = 2;
            loop {
                let name = format!("{base}{counter}");
                if available(&name, used) {
                    break name;
                }
                counter += 1;
            }
        }
    };
    used.insert(name.clone());
    name
}

/// Whether `text` is a plain ASCII identifier that needs no quoting.
pub(super) fn is_plain_identifier(text: &str) -> bool {
    let mut chars = text.chars();
    chars
        .next()
        .is_some_and(|c| c.is_ascii_alphabetic() || c == '_' || c == '$')
        && chars.all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '$')
}

/// Static import graph of application modules, built lazily.
#[derive(Default)]
pub(super) struct ModuleGraph {
    dependencies: std::collections::HashMap<usize, BTreeSet<usize>>,
}

impl ModuleGraph {
    /// Application modules that the module at `index` statically imports or
    /// re-exports, including type-only forms conservatively.
    fn dependencies(&mut self, ctx: &mut Ctx, index: usize) -> BTreeSet<usize> {
        if let Some(found) = self.dependencies.get(&index) {
            return found.clone();
        }
        let mut targets = BTreeSet::new();
        let statements: Vec<Node> = ctx.files[index].node.statements().iter().collect();
        for statement in statements {
            if !matches!(
                statement.kind(),
                SyntaxKind::ImportDeclaration | SyntaxKind::ExportDeclaration
            ) {
                continue;
            }
            let specifier = statement.module_specifier();
            if specifier.is_nil() {
                continue;
            }
            let symbol = ctx.checker.get_symbol_at_location(specifier);
            if symbol.is_nil() {
                continue;
            }
            let Some(&declaration) = ctx.checker.sym(symbol).declarations.first() else {
                continue;
            };
            let file = get_source_file_of_node(declaration);
            if let Some(target) = ctx.file_of(&source_file_info(file).file_name) {
                targets.insert(target);
            }
        }
        self.dependencies.insert(index, targets.clone());
        targets
    }

    /// Whether the module at `index` can reach itself through static
    /// imports. A module outside every import cycle finishes evaluating its
    /// body before any other module can obtain one of its bindings, so no
    /// foreign code can run against its uninitialized bindings.
    pub(super) fn in_cycle(&mut self, ctx: &mut Ctx, index: usize) -> bool {
        let mut seen = BTreeSet::new();
        let mut stack: Vec<usize> = self.dependencies(ctx, index).into_iter().collect();
        while let Some(next) = stack.pop() {
            if next == index {
                return true;
            }
            if !seen.insert(next) {
                continue;
            }
            stack.extend(self.dependencies(ctx, next));
        }
        false
    }
}
