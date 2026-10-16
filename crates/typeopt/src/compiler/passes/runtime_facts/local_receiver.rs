// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Receivers that are provably not a built-in: the other half of the
//! intrinsic allowlist.
//!
//! The allowlist in [`super::intrinsic_exposure`] keeps every *named* handle
//! (`Array.prototype`, the global object, `getPrototypeOf` results) out of
//! variables, parameters and return values. That covers a closed program. It
//! does not cover a value whose origin the analysis cannot see: a parameter of
//! an exported or escaping function (`export function install(p){p.pop=f}`
//! can be called with `Array.prototype` by an importer or a host), a callback
//! parameter filled by unknown code, the `this` of a method called with
//! `.call`, a property read from a shared object, or the result of a call.
//! Telling which of them can hold an intrinsic needs a real escape analysis, so
//! the scan inverts the question as it does for handles:
//!
//! > A property write, deletion or define-like call is ignored by the patch
//! > scan only when its receiver is **provably a local, fresh, non-intrinsic
//! > object**. Every other receiver is a possible patch of an intrinsic.
//!
//! "Possible patch" has the weight of the key. A write with a static key
//! records `*.<key>` (see `Patches`), so only the model of that member
//! stops: `p.items = []` is harmless and `p.pop = f` removes the `pop` model.
//! A computed or numeric key records `*.*` or a numeric member, which
//! disables array and numeric-index modeling for the whole program.
//!
//! # Provably local receivers
//!
//! - An object literal, an array literal or a function or class expression.
//! - `new C(…)` where `C` is a short list of library constructors that return a
//!   new object (not `Object`, `Function` or `Proxy`) and that nothing replaces
//!   (`Date = …`, `globalThis.Date = …`), or an application class declaration
//!   without a class decorator whose constructor chain never returns a value
//!   and extends only such classes. A class decorator may replace the class
//!   with a constructor that returns any object, so a decorated class is never
//!   local.
//! - `this` (and `super`) in a constructor, a field initializer or a static
//!   block of a class: the new instance or the class itself.
//! - A variable whose every declaration is a plain `const`, `let` or `var` with
//!   a local initializer, that no code assigns again and that no unchecked
//!   JavaScript module references; a function or class declaration that no code
//!   assigns again.
//! - A conditional, `||`, `??` or `&&` of such receivers (`&&` yields its right
//!   operand for any object left operand), a comma and an assignment, by the
//!   value they produce.
//!
//! Known limits (documented in `docs/crates-typeopt-foundation.md`): a
//! property read (`state.cache[k] = v`), a call result, a parameter, an
//! import, `this` in a method and a variable assigned twice are never local,
//! even when the program only ever passes fresh objects to them. Treating
//! them as local needs an escape analysis; the conservative default only
//! costs optimization, never correctness. A constructor chain whose base
//! returns an object that a caller supplied is a residual of the same kind.
use std::collections::HashSet;

use super::lib_global_name;
use crate::compiler::{
    analysis::{Ctx, RefKind, skip_outer},
    api::*,
};

/// Bound on alias and class-chain depth; a chain longer than this is not
/// proven local.
const MAX_DEPTH: usize = 8;

/// Library constructors whose `new` result is always a new object that never
/// aliases an argument. `Object` (`new Object(x)` returns `x` for an object),
/// `Function`, `Proxy` and `Reflect` are deliberately absent.
const FRESH_LIBRARY_CLASSES: [&str; 19] = [
    "Array",
    "ArrayBuffer",
    "Boolean",
    "Date",
    "Error",
    "Map",
    "Number",
    "Promise",
    "RangeError",
    "RegExp",
    "Set",
    "String",
    "SyntaxError",
    "TypeError",
    "URL",
    "URLSearchParams",
    "WeakMap",
    "WeakSet",
    "AbortController",
];

/// Names of library constructors that application code may replace (an
/// assignment to the global binding, a write to the same member of the global
/// object, a global augmentation). A replaced constructor can return any object
/// a caller supplied, so `new` of it is not fresh.
pub(super) type Replaced = HashSet<String>;

/// The library constructors whose `new` result can be fresh.
pub(super) fn fresh_library_classes() -> impl Iterator<Item = &'static str> {
    FRESH_LIBRARY_CLASSES.into_iter()
}

/// `new name(...)` yields a new object: `name` is a listed constructor and
/// nothing replaces it.
fn fresh_library_class(name: &str, replaced: &Replaced) -> bool {
    FRESH_LIBRARY_CLASSES.contains(&name) && !replaced.contains(name)
}

/// The receiver `node` of a write is provably a fresh, non-intrinsic object.
pub(super) fn provably_local(ctx: &mut Ctx, node: Node, replaced: &Replaced) -> bool {
    let local = local(ctx, node, 0, replaced);
    tracing::trace!(local, "write receiver locality");
    local
}

fn local(ctx: &mut Ctx, node: Node, depth: usize, replaced: &Replaced) -> bool {
    if depth > MAX_DEPTH {
        return false;
    }
    let node = skip_outer(node);
    match node.kind() {
        SyntaxKind::ObjectLiteralExpression
        | SyntaxKind::ArrayLiteralExpression
        | SyntaxKind::FunctionExpression
        | SyntaxKind::ArrowFunction => true,
        // A class decorator may replace the class with any value.
        SyntaxKind::ClassExpression => node.decorators().is_empty(),
        SyntaxKind::NewExpression => new_is_fresh(ctx, node, depth, replaced),
        SyntaxKind::ThisKeyword | SyntaxKind::SuperKeyword => {
            this_is_fresh(ctx, node, depth, replaced)
        }
        SyntaxKind::ConditionalExpression => {
            local(ctx, node.when_true(), depth + 1, replaced)
                && local(ctx, node.when_false(), depth + 1, replaced)
        }
        SyntaxKind::BinaryExpression => match node.operator_token().kind() {
            SyntaxKind::BarBarToken | SyntaxKind::QuestionQuestionToken => {
                local(ctx, node.left(), depth + 1, replaced)
                    && local(ctx, node.right(), depth + 1, replaced)
            }
            SyntaxKind::AmpersandAmpersandToken
            | SyntaxKind::CommaToken
            | SyntaxKind::EqualsToken => local(ctx, node.right(), depth + 1, replaced),
            _ => false,
        },
        SyntaxKind::Identifier => identifier_is_local(ctx, node, depth, replaced),
        _ => false,
    }
}

/// A variable or declaration that always holds a fresh object.
fn identifier_is_local(ctx: &mut Ctx, node: Node, depth: usize, replaced: &Replaced) -> bool {
    let symbol = ctx.resolve(node);
    if symbol.is_nil() || ctx.js_referenced.contains(&symbol) {
        return false;
    }
    if ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write) {
        return false;
    }
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    if declarations.is_empty() {
        return false;
    }
    declarations.into_iter().all(|declaration| {
        let application = !declaration.flags().intersects(NodeFlags::Ambient)
            && ctx
                .file_of_node(declaration)
                .is_some_and(|index| !ctx.files[index].js);
        if !application {
            return false;
        }
        match declaration.kind() {
            SyntaxKind::VariableDeclaration => {
                declaration.name().kind() == SyntaxKind::Identifier
                    && declaration.initializer().is_some()
                    && local(ctx, declaration.initializer(), depth + 1, replaced)
            }
            SyntaxKind::FunctionDeclaration => true,
            // A class decorator may replace the binding with any value.
            SyntaxKind::ClassDeclaration => declaration.decorators().is_empty(),
            _ => false,
        }
    })
}

/// `this` is the new instance (constructor, field initializer) or the class
/// (static block) rather than a receiver chosen by a caller.
fn this_is_fresh(ctx: &mut Ctx, node: Node, depth: usize, replaced: &Replaced) -> bool {
    let container = get_this_container(node, false, false);
    match container.kind() {
        SyntaxKind::Constructor => {
            let class = container.parent();
            class_is_fresh(ctx, class, depth + 1, replaced)
        }
        SyntaxKind::PropertyDeclaration | SyntaxKind::ClassStaticBlockDeclaration => {
            class_is_fresh(ctx, container.parent(), depth + 1, replaced)
        }
        _ => false,
    }
}

fn new_is_fresh(ctx: &mut Ctx, node: Node, depth: usize, replaced: &Replaced) -> bool {
    let callee = skip_outer(node.expression());
    if callee.kind() != SyntaxKind::Identifier {
        return false;
    }
    if let Some(name) = lib_global_name(ctx, callee) {
        return fresh_library_class(name, replaced);
    }
    class_of_identifier(ctx, callee)
        .is_some_and(|class| class_is_fresh(ctx, class, depth + 1, replaced))
}

/// The single class declaration an identifier names, if it is an application
/// class that no code assigns again.
fn class_of_identifier(ctx: &mut Ctx, identifier: Node) -> Option<Node> {
    let symbol = ctx.resolve(identifier);
    if symbol.is_nil() || ctx.js_referenced.contains(&symbol) {
        return None;
    }
    if ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write) {
        return None;
    }
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    match declarations.as_slice() {
        [class] if class.kind() == SyntaxKind::ClassDeclaration => {
            let application = ctx
                .file_of_node(*class)
                .is_some_and(|index| !ctx.files[index].js);
            application.then_some(*class)
        }
        _ => None,
    }
}

/// Constructing the class always yields a new object: no constructor of the
/// chain returns a value, and the chain ends in an application class or a
/// fresh library class.
fn class_is_fresh(ctx: &mut Ctx, class: Node, depth: usize, replaced: &Replaced) -> bool {
    if depth > MAX_DEPTH
        || !matches!(
            class.kind(),
            SyntaxKind::ClassDeclaration | SyntaxKind::ClassExpression
        )
    {
        return false;
    }
    // A class decorator may replace the class with a constructor that returns
    // an arbitrary object, which the constructor chain below cannot show.
    if !class.decorators().is_empty() {
        return false;
    }
    for member in class.members().iter() {
        if member.kind() == SyntaxKind::Constructor
            && member.body().is_some()
            && constructor_returns_value(member)
        {
            return false;
        }
    }
    for clause in class.heritage_clauses().iter() {
        if clause.token() != SyntaxKind::ExtendsKeyword {
            continue;
        }
        let Some(base) = clause.types().iter().next() else {
            return false;
        };
        let base = skip_outer(base.expression());
        if base.kind() != SyntaxKind::Identifier {
            return false;
        }
        let fresh = match lib_global_name(ctx, base) {
            Some(name) => fresh_library_class(name, replaced),
            None => class_of_identifier(ctx, base)
                .is_some_and(|parent| class_is_fresh(ctx, parent, depth + 1, replaced)),
        };
        if !fresh {
            return false;
        }
    }
    true
}

/// The constructor has a `return <expression>` of its own (not of a nested
/// function), which replaces the constructed object.
fn constructor_returns_value(constructor: Node) -> bool {
    let mut nodes = Vec::new();
    super::super::super::collect(constructor.body(), &mut nodes);
    nodes.into_iter().any(|node| {
        if node.kind() != SyntaxKind::ReturnStatement || node.expression().is_nil() {
            return false;
        }
        let mut owner = node.parent();
        while owner.is_some() {
            if matches!(
                owner.kind(),
                SyntaxKind::FunctionDeclaration
                    | SyntaxKind::FunctionExpression
                    | SyntaxKind::ArrowFunction
                    | SyntaxKind::MethodDeclaration
                    | SyntaxKind::Constructor
                    | SyntaxKind::GetAccessor
                    | SyntaxKind::SetAccessor
                    | SyntaxKind::ClassStaticBlockDeclaration
            ) {
                return owner == constructor;
            }
            owner = owner.parent();
        }
        false
    })
}
