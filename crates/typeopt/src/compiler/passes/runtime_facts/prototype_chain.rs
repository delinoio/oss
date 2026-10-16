// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Prototype-chain exposure: the one rule that protects array and numeric-index
//! modeling from writes the patch scan cannot see.
//!
//! The patch scan ([`super::scan_patches`]) records writes whose receiver it
//! can name: `Array.prototype.pop = …`, `Object.defineProperty(Array.prototype,
//! …)`. A write through an alias, a parameter or a reflection result is
//! invisible to it. Array modeling therefore relies on one conservative rule:
//!
//! > Application code must never let the prototype chain of an array, that is
//! > `Array.prototype`, `Object.prototype` or anything reached from them
//! > through `__proto__`, `getPrototypeOf` or `constructor.prototype`, flow
//! > anywhere the scan does not follow. Otherwise every array and numeric-index
//! > operation is unmodeled.
//!
//! A *handle* is an expression that evaluates to a prototype object. The scan
//! follows a *followed* handle: it resolves the owner of a write through it.
//! - Followed: `C.prototype` and `C["prototype"]` where `C` is `Array` or
//!   `Object`, spelled directly or as a member of the global object
//!   (`globalThis.Array`); `x.__proto__` and `x["__proto__"]` on any receiver;
//!   calls of `getPrototypeOf` (`Object.getPrototypeOf(x)`).
//! - Opaque: `C[k]` for a computed key `k`, and `x.prototype` or
//!   `x["prototype"]` on any other receiver once `Array` or `Object` reaches a
//!   receiver as a value (see below). The scan cannot name the owner of a write
//!   through them.
//!
//! A followed handle is permitted only in these positions:
//! - the receiver of a member or element access, except the reflection members
//!   `__defineGetter__` and `__defineSetter__` and a computed key that is read
//!   (a write through such an access is a patch record);
//! - the first argument of `Object.<writer>` or `Reflect.<writer>` (see
//!   `REFLECTION_WRITERS`) when `Object` or `Reflect` is the unreplaced library
//!   global and the writer is not replaced; the scan records such a call as a
//!   write to the handle's owner;
//! - an operand of an equality comparison or of `typeof`;
//! - the source of an object destructuring pattern with static keys that avoid
//!   the hazards above.
//!
//! An opaque handle is permitted only as an operand of a comparison or of
//! `typeof`. Every other position (`const p = Array.prototype`, a call
//! argument, a return value, a property value, a spread,
//! `helper.assign(Array.prototype)`) is an exposure. A constructor reaches an
//! unknown receiver when `Array` or `Object` is used as a value,
//! `x.constructor` is read other than to compare it, call it or read a member
//! that is not `prototype`, or a pattern destructures `constructor`. Patterns
//! that take `prototype`, `__proto__`, `getPrototypeOf`, a computed key or a
//! rest out of a constructor are exposures too. Unchecked JavaScript files are
//! covered by the text needles of the scan itself.
use super::{
    Facts, GLOBAL_OBJECTS, Member, REFLECTION_WRITERS, escapes_as_value, lib_global_name,
    literal_member,
};
use crate::compiler::{
    analysis::{Ctx, skip_outer, walk_up_outer},
    api::*,
};

/// Why the prototype chain of arrays is not followed, for diagnostics.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(in crate::compiler) enum PrototypeExposure {
    /// A handle on the prototype chain is used as a value.
    Handle,
    /// A handle is passed to a callee that is not the real reflection writer.
    UnverifiedWriter,
    /// A pattern takes `prototype`, `__proto__`, a computed key or a rest out
    /// of a constructor or prototype object.
    Destructuring,
    /// `getPrototypeOf` is used other than as a direct call.
    ReflectionGetter,
}

impl PrototypeExposure {
    /// Stable text for logs.
    pub(in crate::compiler) fn describe(self) -> &'static str {
        match self {
            PrototypeExposure::Handle => "prototype chain used as a value",
            PrototypeExposure::UnverifiedWriter => {
                "prototype chain passed to an unverified reflection writer"
            }
            PrototypeExposure::Destructuring => "prototype chain destructured",
            PrototypeExposure::ReflectionGetter => "getPrototypeOf used as a value",
        }
    }
}

/// Members of a prototype object that mutate it without being a write the scan
/// records.
const HAZARD_MEMBERS: [&str; 2] = ["__defineGetter__", "__defineSetter__"];

/// Names that reach the prototype chain from any value. As an access,
/// `__proto__` is a handle; `getPrototypeOf` is one when called.
const CHAIN_KEYS: [&str; 2] = ["__proto__", "getPrototypeOf"];

/// The first exposure of the prototype chain in unchecked-free application
/// code, if any. `files` are the nodes of every checked file.
pub(super) fn find(
    ctx: &mut Ctx,
    files: &[Vec<Node>],
    facts: &mut Facts,
) -> Option<PrototypeExposure> {
    let constructor_escapes = files
        .iter()
        .flatten()
        .any(|&node| constructor_reaches_values(ctx, node));
    for &node in files.iter().flatten() {
        if let Some(exposure) = exposure_at(ctx, node, constructor_escapes, facts) {
            tracing::debug!(
                exposure = exposure.describe(),
                constructor_escapes,
                "prototype chain exposed"
            );
            return Some(exposure);
        }
    }
    None
}

/// The key a member or element access names: a literal, or `Any`.
fn access_key(ctx: &Ctx, node: Node) -> Option<Member> {
    match node.kind() {
        SyntaxKind::PropertyAccessExpression => Some(Member::Named(node.name().text().to_owned())),
        SyntaxKind::ElementAccessExpression => {
            Some(literal_member(ctx, node.argument_expression()))
        }
        _ => None,
    }
}

fn is_access(node: Node) -> bool {
    matches!(
        node.kind(),
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
    )
}

/// `node` is the global object (`globalThis`, `window.self`, …).
fn global_object(ctx: &mut Ctx, node: Node) -> bool {
    let node = skip_outer(node);
    match node.kind() {
        SyntaxKind::Identifier => {
            lib_global_name(ctx, node).is_some_and(|name| GLOBAL_OBJECTS.contains(&name))
        }
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
            match access_key(ctx, node) {
                Some(Member::Named(name)) if GLOBAL_OBJECTS.contains(&name.as_str()) => {
                    global_object(ctx, node.expression())
                }
                _ => false,
            }
        }
        _ => false,
    }
}

/// The built-in constructor `Array` or `Object` that `node` spells directly.
fn builtin_constructor(ctx: &mut Ctx, node: Node) -> Option<&'static str> {
    let node = skip_outer(node);
    let named = |name: &str| ["Array", "Object"].into_iter().find(|&c| c == name);
    match node.kind() {
        SyntaxKind::Identifier => {
            if !matches!(node.text(), "Array" | "Object") {
                return None;
            }
            lib_global_name(ctx, node).and_then(named)
        }
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
            match access_key(ctx, node)? {
                Member::Named(name) => {
                    named(&name).filter(|_| global_object(ctx, node.expression()))
                }
                Member::Any => None,
            }
        }
        _ => None,
    }
}

/// The static key text of a pattern element or object member name.
fn static_key(ctx: &Ctx, key: Node) -> Option<String> {
    match key.kind() {
        SyntaxKind::Identifier => Some(key.text().to_owned()),
        SyntaxKind::ComputedPropertyName => match literal_member(ctx, key.expression()) {
            Member::Named(name) => Some(name),
            Member::Any => None,
        },
        _ => match literal_member(ctx, key) {
            Member::Named(name) => Some(name),
            Member::Any => None,
        },
    }
}

/// The keys an object destructuring pattern reads, or `None` when a rest
/// element or a computed key makes them unknown. Accepts a binding pattern and
/// the object literal of a destructuring assignment.
fn pattern_keys(ctx: &Ctx, pattern: Node) -> Option<Vec<String>> {
    let mut keys = Vec::new();
    match pattern.kind() {
        SyntaxKind::ObjectBindingPattern => {
            for element in pattern.elements().iter() {
                if element.dot_dot_dot_token().is_some() {
                    return None;
                }
                let key = if element.property_name().is_some() {
                    element.property_name()
                } else {
                    element.name()
                };
                keys.push(static_key(ctx, key)?);
            }
        }
        SyntaxKind::ObjectLiteralExpression => {
            for property in pattern.properties().iter() {
                match property.kind() {
                    SyntaxKind::PropertyAssignment | SyntaxKind::ShorthandPropertyAssignment => {
                        keys.push(static_key(ctx, property.name())?)
                    }
                    _ => return None,
                }
            }
        }
        _ => return None,
    }
    Some(keys)
}

/// The destructuring pattern whose source is `outer`, if any.
fn pattern_of_source(outer: Node) -> Option<Node> {
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::VariableDeclaration
            if parent.initializer() == outer
                && parent.name().kind() == SyntaxKind::ObjectBindingPattern =>
        {
            Some(parent.name())
        }
        SyntaxKind::BinaryExpression
            if parent.operator_token().kind() == SyntaxKind::EqualsToken
                && parent.right() == outer
                && parent.left().kind() == SyntaxKind::ObjectLiteralExpression =>
        {
            Some(parent.left())
        }
        _ => None,
    }
}

/// `Array` or `Object` can reach a receiver that code names `x`: pass one of
/// the rule. Evaluated per node and OR-ed over the program.
fn constructor_reaches_values(ctx: &mut Ctx, node: Node) -> bool {
    match node.kind() {
        SyntaxKind::Identifier
        | SyntaxKind::PropertyAccessExpression
        | SyntaxKind::ElementAccessExpression => {}
        SyntaxKind::BindingElement => {
            let key = if node.property_name().is_some() {
                node.property_name()
            } else {
                node.name()
            };
            return node.parent().kind() == SyntaxKind::ObjectBindingPattern
                && static_key(ctx, key).as_deref() == Some("constructor");
        }
        _ => return false,
    }
    if is_part_of_type_node(node) {
        return false;
    }
    if builtin_constructor(ctx, node).is_some() {
        // Member reads (`Array.isArray`) and calls do not leak the value.
        return escapes_as_value(node);
    }
    if !is_access(node) || is_write_access(node) {
        return false;
    }
    if access_key(ctx, node) != Some(Member::Named("constructor".to_owned())) {
        return false;
    }
    let outer = walk_up_outer(node);
    let parent = outer.parent();
    if is_access(parent) && parent.expression() == outer {
        // `x.constructor.name` is harmless, `x.constructor.prototype` and
        // `x.constructor[k]` reach the prototype.
        return match access_key(ctx, parent) {
            Some(Member::Named(name)) => name == "prototype",
            _ => true,
        };
    }
    escapes_as_value(node)
}

/// How well the patch scan follows a handle.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Handle {
    /// The scan resolves the owner of writes through it: `Array.prototype`,
    /// `Array["prototype"]`, `x.__proto__` and `getPrototypeOf(x)`.
    Followed,
    /// The scan cannot name the owner: a computed key on `Array` or `Object`,
    /// or `prototype` of a receiver that a constructor reaches as a value.
    Opaque,
}

/// `node` is a handle on the prototype chain (see the module documentation).
fn handle_kind(ctx: &mut Ctx, node: Node, constructor_escapes: bool) -> Option<Handle> {
    match node.kind() {
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
            if is_write_access(node) {
                // A write is a patch record, not a value.
                return None;
            }
            match access_key(ctx, node)? {
                Member::Named(name) if name == "__proto__" => Some(Handle::Followed),
                Member::Named(name) if name == "prototype" => {
                    if builtin_constructor(ctx, node.expression()).is_some() {
                        Some(Handle::Followed)
                    } else if constructor_escapes && !other_builtin(ctx, node.expression()) {
                        Some(Handle::Opaque)
                    } else {
                        None
                    }
                }
                Member::Named(_) => None,
                // `Array[k]` may read `prototype`.
                Member::Any => builtin_constructor(ctx, node.expression()).map(|_| Handle::Opaque),
            }
        }
        SyntaxKind::CallExpression => {
            let callee = skip_outer(node.expression());
            (is_access(callee)
                && access_key(ctx, callee) == Some(Member::Named("getPrototypeOf".to_owned())))
            .then_some(Handle::Followed)
        }
        _ => None,
    }
}

/// `node` is a library global other than `Array` and `Object` (`String`,
/// `Function`), whose `prototype` is not on the chain of an array.
fn other_builtin(ctx: &mut Ctx, node: Node) -> bool {
    let node = skip_outer(node);
    node.kind() == SyntaxKind::Identifier
        && lib_global_name(ctx, node).is_some_and(|name| !matches!(name, "Array" | "Object"))
}

fn exposure_at(
    ctx: &mut Ctx,
    node: Node,
    constructor_escapes: bool,
    facts: &mut Facts,
) -> Option<PrototypeExposure> {
    if is_part_of_type_node(node) {
        return None;
    }
    match node.kind() {
        SyntaxKind::ObjectBindingPattern => pattern_exposure(ctx, node, node, constructor_escapes),
        SyntaxKind::BinaryExpression
            if node.operator_token().kind() == SyntaxKind::EqualsToken
                && node.left().kind() == SyntaxKind::ObjectLiteralExpression =>
        {
            pattern_exposure(ctx, node.left(), node.right(), constructor_escapes)
        }
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
            // `getPrototypeOf` read as a value rather than called.
            if access_key(ctx, node) == Some(Member::Named("getPrototypeOf".to_owned())) {
                let outer = walk_up_outer(node);
                let parent = outer.parent();
                let called = parent.kind() == SyntaxKind::CallExpression
                    && skip_outer(parent.expression()) == node;
                if !called {
                    return Some(PrototypeExposure::ReflectionGetter);
                }
            }
            handle_exposure(ctx, node, constructor_escapes, facts)
        }
        SyntaxKind::CallExpression => handle_exposure(ctx, node, constructor_escapes, facts),
        _ => None,
    }
}

/// Destructuring that reaches the prototype chain. `pattern` holds the keys;
/// `source` is the value they are taken from (the pattern itself for a binding
/// pattern whose source is checked through its declaration).
fn pattern_exposure(
    ctx: &mut Ctx,
    pattern: Node,
    source: Node,
    constructor_escapes: bool,
) -> Option<PrototypeExposure> {
    let keys = pattern_keys(ctx, pattern);
    let chain = keys
        .as_ref()
        .is_some_and(|keys| keys.iter().any(|k| CHAIN_KEYS.contains(&k.as_str())));
    if chain {
        return Some(PrototypeExposure::Destructuring);
    }
    let reaches_prototype = keys
        .as_ref()
        .is_none_or(|keys| keys.iter().any(|k| k == "prototype"));
    if !reaches_prototype {
        return None;
    }
    let from_constructor = if pattern == source {
        pattern_source_is_constructor(ctx, pattern)
    } else {
        builtin_constructor(ctx, source).is_some()
    };
    (from_constructor || constructor_escapes).then_some(PrototypeExposure::Destructuring)
}

fn pattern_source_is_constructor(ctx: &mut Ctx, pattern: Node) -> bool {
    let declaration = pattern.parent();
    declaration.kind() == SyntaxKind::VariableDeclaration
        && declaration.name() == pattern
        && declaration.initializer().is_some()
        && builtin_constructor(ctx, declaration.initializer()).is_some()
}

/// A handle used in a position the scan does not follow.
fn handle_exposure(
    ctx: &mut Ctx,
    node: Node,
    constructor_escapes: bool,
    facts: &mut Facts,
) -> Option<PrototypeExposure> {
    let kind = handle_kind(ctx, node, constructor_escapes)?;
    let outer = walk_up_outer(node);
    let parent = outer.parent();
    if kind == Handle::Followed && is_access(parent) && parent.expression() == outer {
        let hazard = match access_key(ctx, parent) {
            Some(Member::Named(name)) => HAZARD_MEMBERS.contains(&name.as_str()),
            // A computed key that is read can name a hazard member; a write
            // through it is recorded as a patch.
            Some(Member::Any) => !is_write_access(parent) && !deleted(parent),
            None => false,
        };
        return hazard.then_some(PrototypeExposure::Handle);
    }
    if kind == Handle::Followed
        && parent.kind() == SyntaxKind::CallExpression
        && parent.arguments().first() == Some(outer)
    {
        return if reflection_writer(ctx, parent, facts) {
            None
        } else {
            Some(PrototypeExposure::UnverifiedWriter)
        };
    }
    match parent.kind() {
        SyntaxKind::TypeOfExpression => return None,
        SyntaxKind::BinaryExpression => {
            if matches!(
                parent.operator_token().kind(),
                SyntaxKind::EqualsEqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsEqualsToken
                    | SyntaxKind::EqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsToken
            ) {
                return None;
            }
        }
        _ => {}
    }
    if kind == Handle::Followed
        && let Some(pattern) = pattern_of_source(outer)
    {
        let safe = pattern_keys(ctx, pattern).is_some_and(|keys| {
            keys.iter().all(|key| {
                !CHAIN_KEYS.contains(&key.as_str()) && !HAZARD_MEMBERS.contains(&key.as_str())
            })
        });
        return (!safe).then_some(PrototypeExposure::Destructuring);
    }
    Some(PrototypeExposure::Handle)
}

/// `access` is the operand of `delete`.
fn deleted(access: Node) -> bool {
    let outer = walk_up_outer(access);
    outer.parent().kind() == SyntaxKind::DeleteExpression
}

/// `call` is exactly `Object.<writer>(…)` or `Reflect.<writer>(…)` with the
/// unreplaced library global and an unreplaced writer, so the patch scan has
/// recorded its first argument as the write target.
fn reflection_writer(ctx: &mut Ctx, call: Node, facts: &mut Facts) -> bool {
    let callee = skip_outer(call.expression());
    if callee.kind() != SyntaxKind::PropertyAccessExpression {
        return false;
    }
    let writer = callee.name().text();
    if writer == "__defineGetter__" || !REFLECTION_WRITERS.contains(&writer) {
        return false;
    }
    let receiver = skip_outer(callee.expression());
    if receiver.kind() != SyntaxKind::Identifier {
        return false;
    }
    let Some(owner) = lib_global_name(ctx, receiver).filter(|n| matches!(*n, "Object" | "Reflect"))
    else {
        return false;
    };
    !facts.patched(ctx, None, owner) || facts.patched(ctx, Some(owner), writer)
}
