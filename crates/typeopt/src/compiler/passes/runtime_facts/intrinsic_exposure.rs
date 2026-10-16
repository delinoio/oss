// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! The intrinsic allowlist: when array and numeric-index modeling is sound.
//!
//! Array modeling assumes that nothing changes what an array does behind the
//! analysis: not the methods of `Array.prototype`, not a numeric accessor on
//! `Array.prototype` or `Object.prototype`, not the iterator protocol. The
//! patch scan ([`super::scan_patches`]) records the writes whose receiver it
//! can name (`Array.prototype.pop = …`, `Object.defineProperty(Array.prototype,
//! …)`). Every other route to a shared built-in object is invisible to it. A
//! rule per spelling never converges, so this module inverts the question:
//!
//! > Array and numeric-index modeling is enabled only when every module of the
//! > program, checked or unchecked, reaches the realm's intrinsics through the
//! > forms below and no others. Anything else disables modeling for the whole
//! > program.
//!
//! The argument is an invariant. A *handle* is a value that can be written to
//! change a shared built-in: `Array.prototype`, `Object.prototype`,
//! `Function.prototype`, the result of `getPrototypeOf`, a constructor read
//! from a value (`x.constructor`) and the global object. The allowlist lets a
//! handle appear only where it cannot be stored, passed or called, so a handle
//! never reaches a variable, a parameter, a property or a return value. Code
//! that holds no handle cannot mutate a shared built-in, and a write that names
//! its handle is a patch record.
//!
//! # Sources of handles
//!
//! The globals `Array`, `Object`, `Reflect`, `Function`, `Proxy`, `eval` and
//! the timers (string code), and the global object under every alias
//! (`globalThis`, `window`, `self`, `global`, `top`, `parent`, `frames`), in
//! any spelling: a plain identifier, a member of the global object
//! (`globalThis.Array`, `window["Object"]`), a destructured or aliased copy.
//! The reflection keys `__proto__`, `constructor`, `__lookupGetter__`,
//! `__lookupSetter__`, `__defineGetter__`, `__defineSetter__`, `defaultView`,
//! `prepareStackTrace`, `getThis` and `getFunction`, in a static, a literal
//! computed, a destructured or a type-proven spelling. A computed member key
//! that is not proven to be a number, a symbol or a harmless string literal
//! (`o[k]` with `k: string` can read `"constructor"`). The sloppy-mode `this`
//! (the global object when a function runs without a receiver). A `with`
//! statement, a dynamic `import()` specifier and a timer called with a string.
//!
//! # Allowlisted forms
//!
//! - Type positions and `typeof x`, `x === y`, `x == y` and the same negated,
//!   `y instanceof x`: for every guarded global, handle and constructor value.
//! - A guarded global as the receiver of a read of a function that returns
//!   neither a handle nor the global object: `Array.isArray`, `Array.from`,
//!   `Array.of`; `Object.keys`, `values`, `entries`, `hasOwn`, `is`,
//!   `fromEntries`, `groupBy`, `create`, `freeze`, `seal`, `preventExtensions`,
//!   `isFrozen`, `isSealed`, `isExtensible`, `getOwnPropertyNames`,
//!   `getOwnPropertySymbols`, `getOwnPropertyDescriptor(s)` and the writers
//!   `assign`, `defineProperty`, `defineProperties`, `setPrototypeOf`;
//!   `Reflect.has`, `ownKeys`, `isExtensible`, `apply`, `construct`,
//!   `getOwnPropertyDescriptor` and the writers. Their targets are never
//!   handles (see below), so they act on application objects, and an
//!   application array that reaches them escapes in the property flow.
//! - `getPrototypeOf` (`Object` or `Reflect`) called, when the result is only
//!   compared or passed to `typeof`.
//! - `Array.prototype`, `Object.prototype` and `Function.prototype`, spelled
//!   directly or on the global object, as: an operand of `typeof` or an
//!   equality comparison; the receiver of a write (`Array.prototype.pop = …`,
//!   `delete Array.prototype.x`), which is a patch record; the receiver of the
//!   read of a literal member that no write replaces and that is not called
//!   with the handle as `this` (`Array.prototype.slice.call(…)`); the source of
//!   an object pattern with such static members; the first argument of a
//!   verified `Object.<writer>` or `Reflect.<writer>` call in statement
//!   position, where `Object` or `Reflect` is the library global and the writer
//!   is not replaced.
//! - `x.constructor` and `x.__proto__` as an operand of `typeof` or an equality
//!   comparison, and `x.constructor.name`.
//! - A global-object member that is not a guarded name (`globalThis.hook`),
//!   read, written, compared or constructed with `new`, but **not called**: a
//!   call passes the global object as `this`, and the global object inherits
//!   `Object.prototype`, so `globalThis.valueOf()` returns the global object.
//!   Only the boolean predicates (`hasOwnProperty`, `isPrototypeOf`,
//!   `propertyIsEnumerable`) and the primitive-returning global functions
//!   (`parseInt`, `isNaN`, `encodeURIComponent`, …) may be called, while no
//!   write replaces them.
//! - The strict-mode `this` (class bodies, modules and `"use strict"` code).
//!
//! The allowlist covers handles that the program names. A write through a value
//! of unknown origin (a parameter that an importer fills with
//! `Array.prototype`) is covered by the receiver rule in
//! [`super::local_receiver`]: only a provably local receiver is ignored by the
//! patch scan; every other write is a possible patch of its key.
//!
//! Evidence rules. A computed key is harmless only while nothing overrides its
//! type (an assertion, a JSDoc cast or an `any` value reaching a number-typed
//! variable make the type a claim without runtime evidence), and a timer
//! callback is a function only when the expression or the declaration it names
//! shows it, never because of a call signature in its type. A key or callback
//! that arrives through a parameter, a property or a call result keeps the
//! conservative default (a callback is assumed to be string code; a key keeps
//! its declared type), and calling code the program does not define (a host
//! global, an ambient declaration) is trusted like any host function: both
//! need an escape analysis or a host trust model and are documented limits in
//! `docs/crates-typeopt-foundation.md`.
//!
//! Residuals. The rule is about the language's own routes. Routes through the
//! host stay open: a module loader (`require("vm")`, `process.binding`), DOM
//! script injection (`document.write`, a script element) and an `import()` of a
//! `data:` module whose specifier is a literal. Bundle mode rejects imports
//! outside the bundle before planning, and the patch scan treats an unknown
//! external runtime import as a numeric patch in project mode.
use super::{Facts, GLOBAL_OBJECTS, Member, REFLECTION_WRITERS, lib_global_name, literal_member};
use crate::compiler::{
    analysis::{Ctx, RefKind, skip_outer, walk_up_outer},
    api::*,
    has_esm_statements, uses_import_meta,
};

/// Why modeling is disabled, for diagnostics. The variants name the class of
/// form that the allowlist rejects, never source text.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(in crate::compiler) enum IntrinsicExposure {
    /// A guarded global or the global object outside the allowlist.
    Global,
    /// A prototype object outside the allowlist.
    Prototype,
    /// A constructor value read from a value outside the allowlist.
    Constructor,
    /// A reflection key (`__proto__`, `__lookupGetter__`, …).
    ReflectionKey,
    /// A computed member key that is not proven harmless.
    DynamicKey,
    /// A pattern that takes a reflection key or an unproven key.
    Destructuring,
    /// A handle passed to a callee that is not the verified reflection writer.
    UnverifiedWriter,
    /// A call of a member of the global object outside the handle-free
    /// allowlist (`globalThis.valueOf()` returns the global object itself).
    GlobalMemberCall,
    /// A sloppy-mode `this` that can be the global object.
    ThisBinding,
    /// Code that evaluates a string or rebinds names (`with`, a timer string,
    /// a dynamic import).
    DynamicCode,
}

impl IntrinsicExposure {
    /// Stable text for logs.
    pub(in crate::compiler) fn describe(self) -> &'static str {
        match self {
            IntrinsicExposure::Global => "intrinsic global outside the allowlist",
            IntrinsicExposure::Prototype => "prototype object outside the allowlist",
            IntrinsicExposure::Constructor => "constructor value outside the allowlist",
            IntrinsicExposure::ReflectionKey => "prototype reflection key",
            IntrinsicExposure::DynamicKey => "computed member key not proven harmless",
            IntrinsicExposure::Destructuring => "pattern takes a reflection or unproven key",
            IntrinsicExposure::UnverifiedWriter => {
                "prototype passed to an unverified reflection writer"
            }
            IntrinsicExposure::GlobalMemberCall => {
                "call of a global-object member outside the allowlist"
            }
            IntrinsicExposure::ThisBinding => "sloppy this can be the global object",
            IntrinsicExposure::DynamicCode => "dynamic code evaluation",
        }
    }
}

/// Keys that reach the prototype chain, a constructor or the realm from any
/// value. Reads of `constructor` and `__proto__` have a narrow allowlist; the
/// others are rejected in every spelling.
const REFLECTION_KEYS: [&str; 10] = [
    "__proto__",
    "constructor",
    "__lookupGetter__",
    "__lookupSetter__",
    "__defineGetter__",
    "__defineSetter__",
    "defaultView",
    "prepareStackTrace",
    "getThis",
    "getFunction",
];

/// Library functions of `Array` that return neither a handle nor the global
/// object.
const ARRAY_FUNCTIONS: [&str; 3] = ["isArray", "from", "of"];

/// Library functions of `Object`, including the writers whose first argument
/// is only ever a verified handle or an application object.
const OBJECT_FUNCTIONS: [&str; 22] = [
    "keys",
    "values",
    "entries",
    "hasOwn",
    "is",
    "fromEntries",
    "groupBy",
    "create",
    "freeze",
    "seal",
    "preventExtensions",
    "isFrozen",
    "isSealed",
    "isExtensible",
    "getOwnPropertyNames",
    "getOwnPropertySymbols",
    "getOwnPropertyDescriptor",
    "getOwnPropertyDescriptors",
    "assign",
    "defineProperty",
    "defineProperties",
    "setPrototypeOf",
];

/// Library functions of `Reflect`, including the writers.
const REFLECT_FUNCTIONS: [&str; 10] = [
    "has",
    "ownKeys",
    "isExtensible",
    "apply",
    "construct",
    "getOwnPropertyDescriptor",
    "defineProperty",
    "deleteProperty",
    "set",
    "setPrototypeOf",
];

/// Inherited `Object.prototype` predicates of the global object that return a
/// boolean. Every other inherited method (`valueOf`, `toString`,
/// `toLocaleString`, `constructor`, `__lookupGetter__`, …) can return `this`
/// or a handle and is rejected when called.
const GLOBAL_PREDICATES: [&str; 3] = ["hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable"];

/// Global functions of the language that return a primitive.
const GLOBAL_FUNCTIONS: [&str; 10] = [
    "parseInt",
    "parseFloat",
    "isNaN",
    "isFinite",
    "encodeURI",
    "encodeURIComponent",
    "decodeURI",
    "decodeURIComponent",
    "escape",
    "unescape",
];

/// The classes of guarded global, by what they allow.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Guarded {
    /// The global object under any alias.
    GlobalObject,
    Array,
    Object,
    Reflect,
    Function,
    Proxy,
    /// `eval`, `importScripts`: code from a string or a URL.
    Eval,
    /// `setTimeout`, `setInterval`: code from a string unless called with a
    /// function.
    Timer,
}

fn guarded_by_name(name: &str) -> Option<Guarded> {
    Some(match name {
        "Array" => Guarded::Array,
        "Object" => Guarded::Object,
        "Reflect" => Guarded::Reflect,
        "Function" => Guarded::Function,
        "Proxy" => Guarded::Proxy,
        "eval" | "importScripts" => Guarded::Eval,
        "setTimeout" | "setInterval" => Guarded::Timer,
        _ if GLOBAL_OBJECTS.contains(&name) => Guarded::GlobalObject,
        _ => return None,
    })
}

/// The first form in the program that the allowlist rejects, if any. Visits
/// every file of the context, including unchecked JavaScript.
pub(super) fn find(ctx: &mut Ctx, facts: &mut Facts) -> Option<IntrinsicExposure> {
    let mut scanned = 0usize;
    for index in 0..ctx.files.len() {
        let nodes = ctx.files[index].nodes.clone();
        let module = file_is_module(ctx, index);
        for node in nodes {
            scanned += 1;
            if let Some(exposure) = exposure_at(ctx, node, module, facts) {
                tracing::debug!(
                    exposure = exposure.describe(),
                    unchecked = ctx.files[index].js,
                    scanned,
                    "intrinsic allowlist rejected a form"
                );
                return Some(exposure);
            }
        }
    }
    tracing::debug!(scanned, "intrinsic allowlist accepted the program");
    None
}

/// The file runs as an ES module, so its top-level `this` is `undefined` and
/// its code is strict.
fn file_is_module(ctx: &Ctx, index: usize) -> bool {
    let file = &ctx.files[index];
    let esm = has_esm_statements(file.node) || uses_import_meta(&file.nodes);
    !crate::app::bundle_excluded_or_classic(&file.name, esm)
}

fn exposure_at(
    ctx: &mut Ctx,
    node: Node,
    module: bool,
    facts: &mut Facts,
) -> Option<IntrinsicExposure> {
    if is_part_of_type_node(node) {
        return None;
    }
    match node.kind() {
        SyntaxKind::Identifier => identifier_exposure(ctx, node, facts),
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
            access_exposure(ctx, node)
        }
        SyntaxKind::ThisKeyword => this_exposure(node, module),
        SyntaxKind::WithStatement => Some(IntrinsicExposure::DynamicCode),
        SyntaxKind::CallExpression => import_exposure(node),
        SyntaxKind::BindingElement => binding_exposure(ctx, node),
        SyntaxKind::PropertyAssignment | SyntaxKind::ShorthandPropertyAssignment => {
            assignment_target_exposure(ctx, node)
        }
        _ => None,
    }
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

/// The key a member or element access names: a literal, or `Any`.
fn access_key(ctx: &Ctx, node: Node) -> Member {
    match node.kind() {
        SyntaxKind::PropertyAccessExpression => Member::Named(node.name().text().to_owned()),
        _ => literal_member(ctx, node.argument_expression()),
    }
}

/// `access` is the operand of `delete`.
fn deleted(access: Node) -> bool {
    walk_up_outer(access).parent().kind() == SyntaxKind::DeleteExpression
}

/// A write or a deletion: the scan records it as a patch.
fn written(access: Node) -> bool {
    is_write_access(access) || deleted(access)
}

/// Whether a key of type `t` can only be a number, a symbol, a primitive that
/// is not a name, or a string literal that is no reflection key.
fn key_type_safe(ctx: &mut Ctx, t: TypeId) -> bool {
    if t.is_nil() {
        return false;
    }
    let flags = ctx.checker.ty(t).flags();
    if flags.intersects(TypeFlags::Union) {
        let parts = ctx.checker.ty(t).types_list();
        return parts.into_iter().all(|part| key_type_safe(ctx, part));
    }
    if flags.intersects(TypeFlags::StringLiteral) {
        return match ctx.checker.ty(t).as_literal_type().value() {
            Some(LiteralValue::String(value)) => !REFLECTION_KEYS.contains(&value),
            _ => false,
        };
    }
    flags.intersects(
        TypeFlags::NumberLike
            | TypeFlags::BigIntLike
            | TypeFlags::BooleanLike
            | TypeFlags::ESSymbolLike
            | TypeFlags::Undefined
            | TypeFlags::Null
            | TypeFlags::Void
            | TypeFlags::Never,
    )
}

/// The expression `key` of a computed member is proven harmless.
///
/// The checker's type is evidence only while nothing in the program overrides
/// it. A type assertion (`"constructor" as unknown as number`, a JSDoc cast) or
/// an `any` value assigned to a number-typed variable makes the type claim the
/// opposite of what the runtime holds, so the proof follows the key through
/// assertions, conditional and logical operands, and the initializer and the
/// assignments of a variable, and rejects the key as soon as one of them has a
/// type that is not itself harmless. A value that arrives through a parameter,
/// a property or a call result keeps its declared type as evidence: telling
/// what a distant producer really returns needs an escape analysis (a known
/// limit, see `docs/crates-typeopt-foundation.md`).
fn key_expression_safe(ctx: &mut Ctx, key: Node) -> bool {
    let t = ctx.checker.get_type_at_location(key);
    key_type_safe(ctx, t) && !key_overridden(ctx, key, 0)
}

/// Bound on the alias and assertion depth of a key proof.
const KEY_PROOF_DEPTH: usize = 8;

/// The expression `value` is proven harmless by its own type.
fn value_type_safe(ctx: &mut Ctx, value: Node) -> bool {
    let t = ctx.checker.get_type_at_location(value);
    key_type_safe(ctx, t)
}

/// Some assertion, `any` initializer or assignment behind the key expression
/// `node` has a type that is not harmless, so the key's own type may be a
/// claim without runtime evidence.
fn key_overridden(ctx: &mut Ctx, node: Node, depth: usize) -> bool {
    if depth > KEY_PROOF_DEPTH {
        return true;
    }
    match node.kind() {
        SyntaxKind::ParenthesizedExpression
        | SyntaxKind::NonNullExpression
        | SyntaxKind::SatisfiesExpression
        | SyntaxKind::AsExpression
        | SyntaxKind::TypeAssertionExpression => {
            let inner = node.expression();
            !value_type_safe(ctx, inner) || key_overridden(ctx, inner, depth + 1)
        }
        SyntaxKind::ConditionalExpression => [node.when_true(), node.when_false()]
            .into_iter()
            .any(|branch| !value_type_safe(ctx, branch) || key_overridden(ctx, branch, depth + 1)),
        SyntaxKind::BinaryExpression => match node.operator_token().kind() {
            SyntaxKind::BarBarToken
            | SyntaxKind::QuestionQuestionToken
            | SyntaxKind::AmpersandAmpersandToken => [node.left(), node.right()]
                .into_iter()
                .any(|side| !value_type_safe(ctx, side) || key_overridden(ctx, side, depth + 1)),
            SyntaxKind::CommaToken | SyntaxKind::EqualsToken => {
                let right = node.right();
                !value_type_safe(ctx, right) || key_overridden(ctx, right, depth + 1)
            }
            _ => false,
        },
        SyntaxKind::Identifier => variable_overridden(ctx, node, depth),
        _ => false,
    }
}

/// An initializer or an assignment of the variable that `identifier` reads
/// holds a value that is not harmless by its own type.
fn variable_overridden(ctx: &mut Ctx, identifier: Node, depth: usize) -> bool {
    let symbol = ctx.resolve(identifier);
    if symbol.is_nil() {
        return false;
    }
    let mut sources = Vec::new();
    for declaration in ctx.checker.sym(symbol).declarations.to_vec() {
        if declaration.kind() == SyntaxKind::VariableDeclaration
            && declaration.initializer().is_some()
        {
            sources.push(declaration.initializer());
        }
    }
    for reference in ctx.refs(symbol).to_vec() {
        if reference.kind != RefKind::Write {
            continue;
        }
        let outer = walk_up_outer(reference.node);
        let parent = outer.parent();
        if parent.kind() == SyntaxKind::BinaryExpression
            && parent.left() == outer
            && matches!(
                parent.operator_token().kind(),
                SyntaxKind::EqualsToken | SyntaxKind::PlusEqualsToken
            )
        {
            sources.push(parent.right());
        }
    }
    sources
        .into_iter()
        .any(|source| !value_type_safe(ctx, source) || key_overridden(ctx, source, depth + 1))
}

// ---------------------------------------------------------------------------
// Property and element accesses on any receiver
// ---------------------------------------------------------------------------

fn access_exposure(ctx: &mut Ctx, node: Node) -> Option<IntrinsicExposure> {
    match access_key(ctx, node) {
        Member::Named(key) => {
            let write = written(node);
            match key.as_str() {
                "__proto__" | "constructor" if !write => {
                    check_opaque_handle(node, key == "constructor")
                }
                "__proto__" | "constructor" => None,
                _ if REFLECTION_KEYS.contains(&key.as_str()) => {
                    Some(IntrinsicExposure::ReflectionKey)
                }
                _ => None,
            }
        }
        Member::Any => {
            if written(node) || key_expression_safe(ctx, node.argument_expression()) {
                None
            } else {
                Some(IntrinsicExposure::DynamicKey)
            }
        }
    }
}

/// A handle whose owner the scan cannot name (`x.__proto__`, `x.constructor`,
/// the result of `getPrototypeOf`) is only compared, passed to `typeof`, or,
/// for a constructor, asked for its `name`.
fn check_opaque_handle(handle: Node, constructor: bool) -> Option<IntrinsicExposure> {
    let outer = walk_up_outer(handle);
    let parent = outer.parent();
    let exposure = if constructor {
        IntrinsicExposure::Constructor
    } else {
        IntrinsicExposure::Prototype
    };
    match parent.kind() {
        SyntaxKind::TypeOfExpression => None,
        SyntaxKind::BinaryExpression if is_equality(parent) => None,
        SyntaxKind::PropertyAccessExpression
            if constructor
                && parent.expression() == outer
                && parent.name().text() == "name"
                && !written(parent)
                && !is_called(parent) =>
        {
            None
        }
        _ => Some(exposure),
    }
}

fn is_equality(binary: Node) -> bool {
    matches!(
        binary.operator_token().kind(),
        SyntaxKind::EqualsEqualsEqualsToken
            | SyntaxKind::ExclamationEqualsEqualsToken
            | SyntaxKind::EqualsEqualsToken
            | SyntaxKind::ExclamationEqualsToken
    )
}

/// The access is called, constructed or tagged, so its receiver becomes the
/// `this` of the callee.
fn is_called(access: Node) -> bool {
    let outer = walk_up_outer(access);
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::CallExpression | SyntaxKind::NewExpression => parent.expression() == outer,
        SyntaxKind::TaggedTemplateExpression => parent.tag() == outer,
        _ => false,
    }
}

// ---------------------------------------------------------------------------
// Destructuring
// ---------------------------------------------------------------------------

/// The key node of a binding element.
fn binding_key(element: Node) -> Node {
    if element.property_name().is_some() {
        element.property_name()
    } else {
        element.name()
    }
}

/// The key a pattern element or object member name spells.
fn pattern_key(ctx: &Ctx, key: Node) -> Member {
    match key.kind() {
        SyntaxKind::Identifier => Member::Named(key.text().to_owned()),
        SyntaxKind::ComputedPropertyName => literal_member(ctx, key.expression()),
        _ => literal_member(ctx, key),
    }
}

/// A pattern key is a reflection key or an unproven computed key.
fn pattern_key_exposure(ctx: &mut Ctx, key: Node) -> Option<IntrinsicExposure> {
    match pattern_key(ctx, key) {
        Member::Named(name) if REFLECTION_KEYS.contains(&name.as_str()) => {
            Some(IntrinsicExposure::Destructuring)
        }
        Member::Named(_) => None,
        Member::Any => {
            let expression = if key.kind() == SyntaxKind::ComputedPropertyName {
                key.expression()
            } else {
                key
            };
            (!key_expression_safe(ctx, expression)).then_some(IntrinsicExposure::Destructuring)
        }
    }
}

fn binding_exposure(ctx: &mut Ctx, element: Node) -> Option<IntrinsicExposure> {
    if element.parent().kind() != SyntaxKind::ObjectBindingPattern
        || element.dot_dot_dot_token().is_some()
    {
        return None;
    }
    pattern_key_exposure(ctx, binding_key(element))
}

/// A property of an object literal that is the target of a destructuring
/// assignment (`({constructor: C} = x)`).
fn assignment_target_exposure(ctx: &mut Ctx, property: Node) -> Option<IntrinsicExposure> {
    let literal = property.parent();
    if literal.kind() != SyntaxKind::ObjectLiteralExpression || !is_write_access(literal) {
        return None;
    }
    pattern_key_exposure(ctx, property.name())
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

/// The static keys a pattern takes from a handle, or `None` when a rest
/// element, a computed key or another shape makes them unknown.
fn handle_pattern_keys(ctx: &Ctx, pattern: Node) -> Option<Vec<String>> {
    let mut keys = Vec::new();
    match pattern.kind() {
        SyntaxKind::ObjectBindingPattern => {
            for element in pattern.elements().iter() {
                if element.dot_dot_dot_token().is_some() {
                    return None;
                }
                match pattern_key(ctx, binding_key(element)) {
                    Member::Named(name) => keys.push(name),
                    Member::Any => return None,
                }
            }
        }
        SyntaxKind::ObjectLiteralExpression => {
            for property in pattern.properties().iter() {
                match property.kind() {
                    SyntaxKind::PropertyAssignment | SyntaxKind::ShorthandPropertyAssignment => {
                        match pattern_key(ctx, property.name()) {
                            Member::Named(name) => keys.push(name),
                            Member::Any => return None,
                        }
                    }
                    _ => return None,
                }
            }
        }
        _ => return None,
    }
    Some(keys)
}

// ---------------------------------------------------------------------------
// Guarded globals
// ---------------------------------------------------------------------------

/// The identifier is a reference to a value, not a member name, a key or a
/// declaration.
fn is_reference(node: Node) -> bool {
    let parent = node.parent();
    match parent.kind() {
        SyntaxKind::PropertyAccessExpression => parent.name() != node,
        SyntaxKind::QualifiedName => parent.right() != node,
        SyntaxKind::PropertyAssignment
        | SyntaxKind::PropertyDeclaration
        | SyntaxKind::MethodDeclaration
        | SyntaxKind::GetAccessor
        | SyntaxKind::SetAccessor
        | SyntaxKind::PropertySignature
        | SyntaxKind::MethodSignature
        | SyntaxKind::EnumMember
        | SyntaxKind::JsxAttribute => parent.name() != node,
        SyntaxKind::BindingElement => parent.property_name() != node && parent.name() != node,
        _ => !is_declaration_name(node),
    }
}

/// The declaration lives in the global scope of a script, so it adds to or
/// replaces the global binding rather than a local one.
fn script_global_declaration(ctx: &Ctx, declaration: Node) -> bool {
    let mut node = declaration.parent();
    while node.is_some() {
        match node.kind() {
            SyntaxKind::SourceFile => {
                return ctx
                    .file_of_node(node)
                    .is_some_and(|index| !file_is_module(ctx, index));
            }
            SyntaxKind::FunctionDeclaration
            | SyntaxKind::FunctionExpression
            | SyntaxKind::ArrowFunction
            | SyntaxKind::MethodDeclaration
            | SyntaxKind::Constructor
            | SyntaxKind::GetAccessor
            | SyntaxKind::SetAccessor
            | SyntaxKind::ClassStaticBlockDeclaration
            | SyntaxKind::ModuleBlock => return false,
            _ => node = node.parent(),
        }
    }
    false
}

/// The guarded global that identifier `node` reads, if any. An unresolved name
/// and a script-level redeclaration count as the global; a module-local or
/// function-local binding does not.
fn guarded_identifier(ctx: &mut Ctx, node: Node) -> Option<Guarded> {
    let guarded = guarded_by_name(node.text())?;
    if !is_reference(node) {
        return None;
    }
    let symbol = ctx.resolve(node);
    if symbol.is_nil() {
        return Some(guarded);
    }
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    // The symbol is the global binding when any declaration is outside the
    // application (the library, or a global augmentation merged into the
    // library symbol) or at the top level of a script. Only a binding whose
    // every declaration is a module-scoped or function-scoped application
    // declaration shadows the global.
    let global = declarations.is_empty()
        || declarations.iter().any(|&declaration| {
            ctx.file_of_node(declaration).is_none() || script_global_declaration(ctx, declaration)
        });
    global.then_some(guarded)
}

fn identifier_exposure(ctx: &mut Ctx, node: Node, facts: &mut Facts) -> Option<IntrinsicExposure> {
    let guarded = guarded_identifier(ctx, node)?;
    check_reference(ctx, guarded, node, facts)
}

/// `expression` (an identifier, or a member of the global object) reads the
/// guarded global `guarded`. It may only appear in the allowlisted positions.
fn check_reference(
    ctx: &mut Ctx,
    guarded: Guarded,
    expression: Node,
    facts: &mut Facts,
) -> Option<IntrinsicExposure> {
    let outer = walk_up_outer(expression);
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::TypeOfExpression => return None,
        SyntaxKind::BinaryExpression => {
            let operator = parent.operator_token().kind();
            if is_equality(parent)
                || (operator == SyntaxKind::InstanceOfKeyword && parent.right() == outer)
            {
                return None;
            }
        }
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
            if parent.expression() == outer =>
        {
            return member_exposure(ctx, guarded, parent, facts);
        }
        SyntaxKind::CallExpression if guarded == Guarded::Timer && parent.expression() == outer => {
            return timer_exposure(ctx, parent);
        }
        _ => {}
    }
    Some(IntrinsicExposure::Global)
}

/// The access `access` reads a member of the guarded global `guarded`.
fn member_exposure(
    ctx: &mut Ctx,
    guarded: Guarded,
    access: Node,
    facts: &mut Facts,
) -> Option<IntrinsicExposure> {
    let Member::Named(key) = access_key(ctx, access) else {
        return Some(IntrinsicExposure::DynamicKey);
    };
    if REFLECTION_KEYS.contains(&key.as_str()) {
        return Some(IntrinsicExposure::ReflectionKey);
    }
    if guarded == Guarded::GlobalObject {
        // `globalThis.Array` reads the global `Array`; any other member is an
        // ordinary property of the global object.
        return match guarded_by_name(&key) {
            Some(member) => check_reference(ctx, member, access, facts),
            None => global_member_exposure(ctx, access, &key, facts),
        };
    }
    let function = |names: &[&str]| names.contains(&key.as_str()) && !written(access);
    match guarded {
        Guarded::Array if key == "prototype" => check_handle(ctx, access, "Array", facts),
        Guarded::Object if key == "prototype" => check_handle(ctx, access, "Object", facts),
        Guarded::Function if key == "prototype" => check_handle(ctx, access, "Function", facts),
        Guarded::Array if function(&ARRAY_FUNCTIONS) => None,
        Guarded::Object if function(&OBJECT_FUNCTIONS) => None,
        Guarded::Reflect if function(&REFLECT_FUNCTIONS) => None,
        Guarded::Object | Guarded::Reflect if key == "getPrototypeOf" && !written(access) => {
            // The result is a handle whose owner the scan cannot name.
            let outer = walk_up_outer(access);
            let call = outer.parent();
            if call.kind() == SyntaxKind::CallExpression && call.expression() == outer {
                check_opaque_handle(call, false)
            } else {
                Some(IntrinsicExposure::Prototype)
            }
        }
        _ => Some(IntrinsicExposure::Global),
    }
}

/// `access` reads the member `key` of the global object, which is not a
/// guarded name. Reading, writing and comparing it is harmless: a handle never
/// reaches a variable, so nothing can use the value to change an intrinsic.
/// *Calling* it is not: the callee receives the global object as `this`, and an
/// ordinary global-object member includes the inherited `Object.prototype`
/// methods, so `globalThis.valueOf()` returns the global object itself and
/// `globalThis.m()` runs a function that can return its `this`. A call is
/// therefore rejected unless the member is on a short list of functions that
/// return neither a handle nor `this` and nothing replaces them. `new` is
/// harmless because the callee gets a fresh `this`.
fn global_member_exposure(
    ctx: &mut Ctx,
    access: Node,
    key: &str,
    facts: &mut Facts,
) -> Option<IntrinsicExposure> {
    let outer = walk_up_outer(access);
    let parent = outer.parent();
    let called = match parent.kind() {
        SyntaxKind::CallExpression => parent.expression() == outer,
        SyntaxKind::TaggedTemplateExpression => parent.tag() == outer,
        _ => false,
    };
    if !called {
        return None;
    }
    let intact = if GLOBAL_PREDICATES.contains(&key) {
        // Inherited from `Object.prototype`: also intact there and on the
        // global object.
        facts.patch_record(ctx, Some("Object"), key).is_none()
            && facts.unproven_record(ctx, key).is_none()
    } else {
        GLOBAL_FUNCTIONS.contains(&key) && facts.unproven_record(ctx, key).is_none()
    };
    let intact = intact && facts.patch_record(ctx, None, key).is_none();
    (!intact).then_some(IntrinsicExposure::GlobalMemberCall)
}

/// A timer call runs a string as code unless its first argument is a function.
/// The proof is the expression itself, never its type: a string asserted or
/// annotated as callable has a call signature and still runs as code.
fn timer_exposure(ctx: &mut Ctx, call: Node) -> Option<IntrinsicExposure> {
    let Some(callback) = call.arguments().first() else {
        return Some(IntrinsicExposure::DynamicCode);
    };
    (!runtime_function(ctx, skip_outer(callback))).then_some(IntrinsicExposure::DynamicCode)
}

/// `node` is a function at run time: a function or arrow expression, or a
/// reference to an application function declaration, or to a variable
/// initialized with a function expression, that nothing assigns again. A
/// parameter, a property or a call result is not proven, whatever its
/// type says.
fn runtime_function(ctx: &mut Ctx, node: Node) -> bool {
    match node.kind() {
        SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression => true,
        SyntaxKind::Identifier => {
            let symbol = ctx.resolve(node);
            if symbol.is_nil()
                || ctx.js_referenced.contains(&symbol)
                || ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write)
            {
                return false;
            }
            let declarations = ctx.checker.sym(symbol).declarations.to_vec();
            !declarations.is_empty()
                && declarations.into_iter().all(|declaration| {
                    let application = !declaration.flags().intersects(NodeFlags::Ambient)
                        && ctx.file_of_node(declaration).is_some();
                    application
                        && match declaration.kind() {
                            SyntaxKind::FunctionDeclaration => true,
                            // `const tick = () => …` and `var tick = function(){…}`.
                            SyntaxKind::VariableDeclaration => {
                                declaration.name().kind() == SyntaxKind::Identifier
                                    && declaration.initializer().is_some()
                                    && matches!(
                                        skip_outer(declaration.initializer()).kind(),
                                        SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression
                                    )
                            }
                            _ => false,
                        }
                })
        }
        _ => false,
    }
}

/// A dynamic `import()` with anything but a relative or bare string literal
/// specifier can load code from a `data:` URL into the realm.
fn import_exposure(call: Node) -> Option<IntrinsicExposure> {
    if call.expression().kind() != SyntaxKind::ImportKeyword {
        return None;
    }
    let specifier = call.arguments().first().map(skip_outer);
    let literal = specifier.is_some_and(|node| {
        matches!(
            node.kind(),
            SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
        ) && !node.text().contains(':')
    });
    (!literal).then_some(IntrinsicExposure::DynamicCode)
}

// ---------------------------------------------------------------------------
// Prototype handles
// ---------------------------------------------------------------------------

/// `handle` is `Array.prototype`, `Object.prototype` or `Function.prototype`
/// (`owner` names the constructor). It may only be compared, written through,
/// read for a member nobody replaces, destructured with such members, or the
/// first argument of a verified writer.
fn check_handle(
    ctx: &mut Ctx,
    handle: Node,
    owner: &str,
    facts: &mut Facts,
) -> Option<IntrinsicExposure> {
    let outer = walk_up_outer(handle);
    let parent = outer.parent();
    match parent.kind() {
        SyntaxKind::TypeOfExpression => return None,
        SyntaxKind::BinaryExpression if is_equality(parent) => return None,
        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
            if parent.expression() == outer =>
        {
            return handle_member_exposure(ctx, parent, owner, facts);
        }
        SyntaxKind::CallExpression if parent.arguments().first() == Some(outer) => {
            return writer_exposure(ctx, parent, facts);
        }
        _ => {}
    }
    if let Some(pattern) = pattern_of_source(outer) {
        let readable = handle_pattern_keys(ctx, pattern).is_some_and(|keys| {
            keys.iter().all(|key| {
                !REFLECTION_KEYS.contains(&key.as_str())
                    && facts.patch_record(ctx, Some(owner), key).is_none()
            })
        });
        return (!readable).then_some(IntrinsicExposure::Destructuring);
    }
    Some(IntrinsicExposure::Prototype)
}

/// A member of a prototype handle: a write is a patch record; a read must name
/// a member that no write replaces, and must not bind the handle as `this`.
fn handle_member_exposure(
    ctx: &mut Ctx,
    access: Node,
    owner: &str,
    facts: &mut Facts,
) -> Option<IntrinsicExposure> {
    let write = written(access);
    match access_key(ctx, access) {
        Member::Named(key) => {
            if REFLECTION_KEYS.contains(&key.as_str()) {
                return Some(IntrinsicExposure::ReflectionKey);
            }
            if write {
                return None;
            }
            if facts.patch_record(ctx, Some(owner), &key).is_some() || is_called(access) {
                return Some(IntrinsicExposure::Prototype);
            }
            None
        }
        // A write through a computed key is recorded as an unknown member.
        Member::Any => (!write).then_some(IntrinsicExposure::DynamicKey),
    }
}

/// The handle is the first argument of `call`: the call must be the verified
/// reflection writer, in statement position so its result (the handle) is not
/// kept.
fn writer_exposure(ctx: &mut Ctx, call: Node, facts: &mut Facts) -> Option<IntrinsicExposure> {
    let statement = matches!(
        walk_up_outer(call).parent().kind(),
        SyntaxKind::ExpressionStatement | SyntaxKind::VoidExpression
    );
    (!(statement && reflection_writer(ctx, call, facts)))
        .then_some(IntrinsicExposure::UnverifiedWriter)
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
    // Both the library global and the writer must be intact: a replaced
    // global or a replaced writer runs application code on the prototype.
    facts.patch_record(ctx, None, owner).is_none()
        && facts.patch_record(ctx, Some(owner), writer).is_none()
}

// ---------------------------------------------------------------------------
// `this`
// ---------------------------------------------------------------------------

/// A `this` that is the global object when a function runs without a receiver:
/// every non-strict function, and the top level of a classic script.
fn this_exposure(node: Node, module: bool) -> Option<IntrinsicExposure> {
    let container = get_this_container(node, false, false);
    (!strict_code(container, module)).then_some(IntrinsicExposure::ThisBinding)
}

/// The code of `node` is strict: inside a class, inside a function or file
/// with a `"use strict"` prologue, or in an ES module.
fn strict_code(node: Node, module: bool) -> bool {
    let mut node = node;
    while node.is_some() {
        match node.kind() {
            SyntaxKind::ClassDeclaration | SyntaxKind::ClassExpression => return true,
            SyntaxKind::SourceFile => return module || has_use_strict(node.statements()),
            SyntaxKind::FunctionDeclaration
            | SyntaxKind::FunctionExpression
            | SyntaxKind::ArrowFunction
            | SyntaxKind::MethodDeclaration
            | SyntaxKind::GetAccessor
            | SyntaxKind::SetAccessor => {
                let body = node.body();
                if body.is_some()
                    && body.kind() == SyntaxKind::Block
                    && has_use_strict(body.statements())
                {
                    return true;
                }
            }
            _ => {}
        }
        node = node.parent();
    }
    false
}

/// The directive prologue holds `"use strict"`.
fn has_use_strict(statements: NodeSlice) -> bool {
    for statement in statements.iter() {
        if statement.kind() != SyntaxKind::ExpressionStatement
            || statement.expression().kind() != SyntaxKind::StringLiteral
        {
            return false;
        }
        if statement.expression().text() == "use strict" {
            return true;
        }
    }
    false
}
