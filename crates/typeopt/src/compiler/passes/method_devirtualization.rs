// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `method-devirtualization`: rewrites `receiver.method(args)` into
//! `fn(receiver, args)` and moves the method body to a module-level
//! function whose `this` becomes an explicit receiver parameter.
//!
//! A method of an internal, top-level, non-decorated class is eligible
//! when the planner proves:
//!
//! - the method name is declared exactly once in application TypeScript, no
//!   external declaration names it and no application JavaScript mentions it,
//!   so no override, own property or external caller exists;
//! - every mention of the name is the callee of a direct, non-optional member
//!   call (`x.m(...)`): no extraction, `super.m`, `.call`/`.apply`/ `.bind`,
//!   string keys, destructuring or type queries, so the function's identity,
//!   `.name` and `.length` are unobserved;
//! - the class value is only constructed, used in types, `instanceof` or as an
//!   application base class; it does not extend external code;
//! - no computed access, `in` test, `.constructor`/`__proto__` read or
//!   prototype reflection can reach the method on an instance;
//! - every receiver is runtime evidence of a non-nullish instance: `this`
//!   inside instance members of the class or its subclasses (whose methods are
//!   themselves only invoked as member calls), `new` expressions of the class
//!   or a subclass, or `const` bindings of them;
//! - the body uses no `super`, `arguments`, `new.target`, `this` type or
//!   private/protected members, and uses `this` only as a member-access
//!   receiver.
//!
//! The original evaluates the receiver, looks up the method (no effect on a
//! proven instance without getters or proxies), then the arguments; the
//! rewrite reads a hoisted function binding, then the receiver and the
//! arguments in the same order. The new function takes the shortest
//! identifier unused in every affected module. Cross-module call sites
//! import it through an existing runtime import of the class module.
use std::collections::{BTreeMap, HashMap, HashSet};

use anyhow::Result;

use super::{
    super::analysis::{Ctx, RefKind, skip_outer, walk_up_outer},
    calls_support::{self as support, Facts},
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::MethodDevirtualization;

/// Reflection that enumerates keys, including non-enumerable prototype
/// methods. Prototype identity operations (`getPrototypeOf`,
/// `.constructor`, `__proto__`) observe nothing by themselves: any later
/// keyed or enumerating use of the prototype is itself checked.
const ENUMERATING: &[&str] = &[
    "getOwnPropertyNames",
    "getOwnPropertyDescriptors",
    "ownKeys",
];

/// Reflection that reads, tests or defines one key of its target.
const KEYED: &[&str] = &[
    "get",
    "set",
    "has",
    "defineProperty",
    "deleteProperty",
    "getOwnPropertyDescriptor",
    "__lookupGetter__",
    "__lookupSetter__",
    "__defineGetter__",
    "__defineSetter__",
];

pub(in crate::compiler) fn plan(ctx: &mut Ctx, _enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let mut index = Index::build(ctx);
    let mut facts = Facts::new();
    let mut candidates = 0;
    let classes: Vec<(usize, Node)> = (0..ctx.files.len())
        .filter(|&f| !ctx.files[f].js)
        .flat_map(|f| {
            ctx.files[f]
                .node
                .statements()
                .iter()
                .filter(|s| s.kind() == SyntaxKind::ClassDeclaration && s.name().is_some())
                .map(move |s| (f, s))
                .collect::<Vec<_>>()
        })
        .collect();
    for (file, class) in classes {
        crate::app::check_cancelled()?;
        let methods: Vec<Node> = class
            .members()
            .iter()
            .filter(|m| m.kind() == SyntaxKind::MethodDeclaration)
            .collect();
        for method in methods {
            let name = method.name();
            if !matches!(
                name.kind(),
                SyntaxKind::Identifier | SyntaxKind::PrivateIdentifier
            ) {
                continue;
            }
            let key = format!(
                "method:{}:{}.{}",
                ctx.files[file].name,
                class.name().text(),
                name.text()
            );
            match plan_method(ctx, &mut facts, &mut index, file, class, method) {
                Ok(candidate) => {
                    let mut candidate = candidate;
                    candidate.key = key;
                    out.candidates.push(candidate);
                    candidates += 1;
                }
                Err(Some((reason, at))) => {
                    out.skips.push(ctx.skip(PASS, key, file, at, reason));
                }
                Err(None) => {}
            }
        }
    }
    tracing::debug!(pass = %PASS, candidates, "method devirtualization planned");
    Ok(())
}

/// `None` rejections are not notable enough to report (for example a
/// method that is never called).
type Rejection = Option<(Reason, Node)>;

/// Whole-program member-name facts.
struct Index {
    /// Property accesses by member name, in deterministic order.
    accesses: HashMap<String, Vec<(usize, Node)>>,
    /// Dynamic (non-literal) keyed lookups: element accesses and `in`
    /// tests, as (file, object, key).
    dynamic: Vec<(usize, Node, Node)>,
    /// Reflective calls and `.constructor`/`__proto__` reads, as the
    /// expressions whose prototype they may observe.
    reflective: Vec<Node>,
    /// Text of application JavaScript modules.
    javascript: Vec<String>,
    /// Classes proven to hold no instance in a reflective or dynamic site.
    unreachable: HashMap<SymbolId, bool>,
}

impl Index {
    fn build(ctx: &mut Ctx) -> Self {
        let mut accesses: HashMap<String, Vec<(usize, Node)>> = HashMap::new();
        let mut dynamic = Vec::new();
        let mut reflective = Vec::new();
        let mut javascript = Vec::new();
        for (file, info) in ctx.files.iter().enumerate() {
            if info.js {
                javascript.push(info.text.clone());
                continue;
            }
            for &node in &info.nodes {
                match node.kind() {
                    SyntaxKind::PropertyAccessExpression => {
                        let name = node.name().text();
                        accesses
                            .entry(name.to_owned())
                            .or_default()
                            .push((file, node));
                    }
                    SyntaxKind::BinaryExpression
                        if node.operator_token().kind() == SyntaxKind::InKeyword
                            && !matches!(
                                skip_outer(node.left()).kind(),
                                SyntaxKind::StringLiteral
                                    | SyntaxKind::NumericLiteral
                                    | SyntaxKind::NoSubstitutionTemplateLiteral
                                    | SyntaxKind::PrivateIdentifier
                            ) =>
                    {
                        dynamic.push((file, node.right(), node.left()));
                    }
                    SyntaxKind::CallExpression => {
                        let callee = skip_outer(node.expression());
                        let name = match callee.kind() {
                            SyntaxKind::PropertyAccessExpression => callee.name().text(),
                            SyntaxKind::Identifier => callee.text(),
                            _ => "",
                        };
                        // `Reflect.*` and `Object.*` style statics, plus the
                        // legacy accessor helpers called on any object.
                        let arguments: Vec<Node> = node.arguments().to_vec();
                        let Some(&target) = arguments.first() else {
                            continue;
                        };
                        if KEYED.contains(&name)
                            && callee.kind() == SyntaxKind::PropertyAccessExpression
                        {
                            let receiver = skip_outer(callee.expression());
                            let reflect = receiver.kind() == SyntaxKind::Identifier
                                && matches!(receiver.text(), "Reflect" | "Object");
                            if name.starts_with("__") {
                                // `obj.__defineGetter__(key, ...)`.
                                dynamic.push((file, callee.expression(), target));
                            } else if reflect && let Some(&key) = arguments.get(1) {
                                dynamic.push((file, target, key));
                            }
                        } else if ENUMERATING.contains(&name) {
                            reflective.push(target);
                        }
                    }
                    _ => {}
                }
            }
        }
        for access in &ctx.dynamic_accesses {
            dynamic.push((
                access.file,
                access.node.expression(),
                access.node.argument_expression(),
            ));
        }
        for list in accesses.values_mut() {
            list.sort_by_key(|(file, node)| (*file, Ctx::start(*node)));
        }
        Self {
            accesses,
            dynamic,
            reflective,
            javascript,
            unreachable: HashMap::new(),
        }
    }
}

/// Whether a value of `ty` could be an instance of the class at runtime as
/// far as types tell. Only used to discard unrelated dynamic sites; a
/// `true` answer always preserves the code.
fn may_hold(ctx: &mut Ctx, ty: TypeId, instance: TypeId) -> bool {
    let flags = ctx.checker.ty(ty).flags();
    if flags.intersects(
        TypeFlags::Any
            | TypeFlags::Unknown
            | TypeFlags::TypeParameter
            | TypeFlags::Index
            | TypeFlags::IndexedAccess
            | TypeFlags::Conditional
            | TypeFlags::Substitution
            | TypeFlags::NonPrimitive,
    ) {
        return true;
    }
    if flags.intersects(TypeFlags::Union | TypeFlags::Intersection) {
        let parts = ctx.checker.ty(ty).types_list();
        return parts.iter().any(|&part| may_hold(ctx, part, instance));
    }
    if flags.intersects(TypeFlags::Object) {
        return ty == instance || ctx.checker.is_type_assignable_to_exported(instance, ty);
    }
    false
}

/// Whether the value of an expression may be a class instance. A
/// never-written parameter of an internal function that is only called
/// directly takes its values from the call arguments, which are examined
/// instead of the (possibly generic) parameter type.
fn expression_may_hold(ctx: &mut Ctx, expression: Node, instance: TypeId, depth: usize) -> bool {
    let node = skip_outer(expression);
    if depth < 4
        && node.kind() == SyntaxKind::Identifier
        && let Some(arguments) = parameter_arguments(ctx, node)
    {
        return arguments
            .into_iter()
            .any(|argument| expression_may_hold(ctx, argument, instance, depth + 1));
    }
    let ty = ctx.checker.get_type_at_location(expression);
    may_hold(ctx, ty, instance)
}

/// Every argument passed to the parameter that `identifier` reads, when the
/// parameter is never written and its function is internal and only called
/// directly with that argument present (or `None`).
fn parameter_arguments(ctx: &mut Ctx, identifier: Node) -> Option<Vec<Node>> {
    let symbol = ctx.resolve(identifier);
    if symbol.is_nil() || ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write) {
        return None;
    }
    let parameter = ctx.checker.sym(symbol).value_declaration;
    if parameter.is_nil()
        || parameter.kind() != SyntaxKind::Parameter
        || parameter.dot_dot_dot_token().is_some()
        || parameter.name().kind() != SyntaxKind::Identifier
    {
        return None;
    }
    let function = parameter.parent();
    let position = function.parameters().iter().position(|p| p == parameter)?;
    let name = match function.kind() {
        SyntaxKind::FunctionDeclaration => function.name(),
        SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression => {
            let mut holder = function.parent();
            while holder.kind() == SyntaxKind::ParenthesizedExpression {
                holder = holder.parent();
            }
            if holder.kind() != SyntaxKind::VariableDeclaration
                || !holder.parent().flags().intersects(NodeFlags::Const)
            {
                return None;
            }
            holder.name()
        }
        _ => return None,
    };
    if name.is_nil() || name.kind() != SyntaxKind::Identifier {
        return None;
    }
    let callee = ctx.resolve(name);
    if callee.is_nil() || !ctx.is_internal(callee) {
        return None;
    }
    let mut arguments = Vec::new();
    for reference in ctx.refs(callee).to_vec() {
        match reference.kind {
            RefKind::Declaration => {}
            RefKind::Alias
                if matches!(
                    reference.node.parent().kind(),
                    SyntaxKind::ImportSpecifier | SyntaxKind::ExportSpecifier
                ) => {}
            RefKind::Call => {
                let call = walk_up_outer(reference.node).parent();
                let list = call.arguments();
                if list
                    .iter()
                    .take(position + 1)
                    .any(|a| a.kind() == SyntaxKind::SpreadElement)
                {
                    return None;
                }
                arguments.push(list.iter().nth(position)?);
            }
            _ => return None,
        }
    }
    Some(arguments)
}

/// Whether a key of type `ty` could equal `name` at runtime.
fn key_may_be(ctx: &mut Ctx, ty: TypeId, name: &str) -> bool {
    let flags = ctx.checker.ty(ty).flags();
    if flags.intersects(TypeFlags::Union) {
        let parts = ctx.checker.ty(ty).types_list();
        return parts.iter().any(|&part| key_may_be(ctx, part, name));
    }
    if flags.intersects(TypeFlags::StringLiteral) {
        return match ctx.checker.ty(ty).as_literal_type().value() {
            Some(LiteralValue::String(value)) => value == name,
            _ => true,
        };
    }
    if flags.intersects(
        TypeFlags::NumberLike
            | TypeFlags::BigIntLike
            | TypeFlags::BooleanLike
            | TypeFlags::ESSymbolLike
            | TypeFlags::Undefined
            | TypeFlags::Null
            | TypeFlags::Void
            | TypeFlags::Never,
    ) {
        // Number keys only produce numeric strings.
        return flags.intersects(TypeFlags::NumberLike) && matches!(name, "NaN" | "Infinity");
    }
    true
}

/// A class proven eligible as a whole.
struct ClassFacts {
    symbol: SymbolId,
    /// The class and its application subclasses.
    family: Vec<SymbolId>,
}

/// Whether the class value is only constructed, used in types, tested with
/// `instanceof`, extended by application classes or aliased by imports.
fn class_closed(ctx: &mut Ctx, class: Node) -> Result<ClassFacts, (Reason, Node)> {
    let symbol = ctx.resolve(class.name());
    if symbol.is_nil() || ctx.checker.sym(symbol).declarations.len() != 1 {
        return Err((Reason::DeclarationMerging, class.name()));
    }
    if !ctx.is_internal(symbol) {
        let reason = if ctx.public.contains(&symbol) {
            Reason::PublicExport
        } else {
            Reason::ExternalBoundary
        };
        return Err((reason, class.name()));
    }
    if !class.decorators().is_empty() || class.members().iter().any(|m| !m.decorators().is_empty())
    {
        return Err((Reason::Decorator, class.name()));
    }
    // The base chain must stay within analyzed application classes.
    if let Some(base) = base_class(ctx, class) {
        let Some(base) = base else {
            return Err((Reason::ExternalBoundary, class.name()));
        };
        class_closed(ctx, base)?;
    }
    let mut family = vec![symbol];
    let refs = ctx.refs(symbol).to_vec();
    for reference in refs {
        match reference.kind {
            RefKind::Declaration | RefKind::Type | RefKind::New => {}
            RefKind::Alias
                if matches!(
                    reference.node.parent().kind(),
                    SyntaxKind::ImportSpecifier | SyntaxKind::ImportClause
                ) => {}
            RefKind::Read => {
                let outer = walk_up_outer(reference.node);
                let parent = outer.parent();
                if parent.kind() == SyntaxKind::BinaryExpression
                    && parent.operator_token().kind() == SyntaxKind::InstanceOfKeyword
                    && parent.right() == outer
                {
                    continue;
                }
                // `class D extends C`: D joins the family.
                if parent.kind() == SyntaxKind::ExpressionWithTypeArguments
                    && parent.parent().kind() == SyntaxKind::HeritageClause
                    && parent.parent().token() == SyntaxKind::ExtendsKeyword
                {
                    let subclass = parent.parent().parent();
                    if subclass.kind() == SyntaxKind::ClassDeclaration && subclass.name().is_some()
                    {
                        let facts = class_closed(ctx, subclass)?;
                        family.extend(facts.family);
                        continue;
                    }
                }
                return Err((Reason::ObservableIdentity, reference.node));
            }
            _ => return Err((Reason::ObservableIdentity, reference.node)),
        }
    }
    family.sort_by_key(|s| s.0);
    family.dedup();
    Ok(ClassFacts { symbol, family })
}

/// `None` without a base, `Some(None)` for a base outside application
/// TypeScript classes.
fn base_class(ctx: &mut Ctx, class: Node) -> Option<Option<Node>> {
    let clause = class
        .heritage_clauses()
        .nodes()
        .iter()
        .find(|c| c.token() == SyntaxKind::ExtendsKeyword)?;
    let expression = clause.types().nodes().first()?.expression();
    if expression.kind() != SyntaxKind::Identifier {
        return Some(None);
    }
    let symbol = ctx.resolve(expression);
    if symbol.is_nil() {
        return Some(None);
    }
    let declaration = ctx.checker.sym(symbol).value_declaration;
    if declaration.is_nil()
        || declaration.kind() != SyntaxKind::ClassDeclaration
        || ctx
            .file_of_node(declaration)
            .is_none_or(|f| ctx.files[f].js)
    {
        return Some(None);
    }
    Some(Some(declaration))
}

/// Whether a member name is only ever invoked as `x.name(...)`, so `this`
/// inside it is the non-nullish receiver of that call.
fn member_called_only(ctx: &mut Ctx, index: &Index, name: &str) -> bool {
    if ctx.external_names().contains(name) || index.javascript.iter().any(|t| t.contains(name)) {
        return false;
    }
    let calls = index.accesses.get(name).map_or(0, |list| {
        list.iter()
            .filter(|(_, access)| callee_of_call(*access))
            .count()
    });
    ctx.name_mentions(name) == calls + 1
}

/// Whether the property access is the callee of a call expression.
fn callee_of_call(access: Node) -> bool {
    let outer = walk_up_outer(access);
    let parent = outer.parent();
    parent.kind() == SyntaxKind::CallExpression && parent.expression() == outer
}

/// Runtime evidence that the receiver is a non-nullish instance of the
/// class family.
fn proven_receiver(
    ctx: &mut Ctx,
    index: &Index,
    family: &[SymbolId],
    receiver: Node,
    depth: usize,
) -> bool {
    let receiver = skip_outer(receiver);
    match receiver.kind() {
        SyntaxKind::ThisKeyword => this_is_instance(ctx, index, family, receiver),
        SyntaxKind::NewExpression => {
            let callee = skip_outer(receiver.expression());
            callee.kind() == SyntaxKind::Identifier && {
                let symbol = ctx.resolve(callee);
                family.contains(&symbol)
            }
        }
        SyntaxKind::Identifier if depth < 4 => {
            let symbol = ctx.resolve(receiver);
            if symbol.is_nil() || ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write) {
                return false;
            }
            let declaration = ctx.checker.sym(symbol).value_declaration;
            if declaration.is_some() && declaration.kind() == SyntaxKind::Parameter {
                return parameter_receiver(ctx, index, family, declaration, depth);
            }
            declaration.is_some()
                && declaration.kind() == SyntaxKind::VariableDeclaration
                && declaration.name().kind() == SyntaxKind::Identifier
                && declaration.parent().flags().intersects(NodeFlags::Const)
                && !declaration.parent().flags().intersects(NodeFlags::Using)
                && declaration.initializer().is_some()
                && ctx.file_of_node(declaration).is_some()
                && proven_receiver(ctx, index, family, declaration.initializer(), depth + 1)
        }
        _ => false,
    }
}

/// A never-written plain parameter of a function declaration that is only
/// ever called directly, with a proven receiver in that position at every
/// call (for example the receiver parameter of a devirtualized method).
fn parameter_receiver(
    ctx: &mut Ctx,
    index: &Index,
    family: &[SymbolId],
    parameter: Node,
    depth: usize,
) -> bool {
    let function = parameter.parent();
    if function.kind() != SyntaxKind::FunctionDeclaration
        || function.name().is_nil()
        || parameter.dot_dot_dot_token().is_some()
        || parameter.initializer().is_some()
        || parameter.name().kind() != SyntaxKind::Identifier
        || ctx.file_of_node(function).is_none_or(|f| ctx.files[f].js)
    {
        return false;
    }
    let Some(position) = function.parameters().iter().position(|p| p == parameter) else {
        return false;
    };
    let symbol = ctx.resolve(function.name());
    if symbol.is_nil() || !ctx.is_internal(symbol) {
        return false;
    }
    let refs = ctx.refs(symbol).to_vec();
    let mut calls = 0;
    for reference in refs {
        match reference.kind {
            RefKind::Declaration => {}
            RefKind::Alias
                if matches!(
                    reference.node.parent().kind(),
                    SyntaxKind::ImportSpecifier | SyntaxKind::ExportSpecifier
                ) => {}
            RefKind::Call => {
                let call = walk_up_outer(reference.node).parent();
                let arguments = call.arguments();
                if arguments
                    .iter()
                    .take(position + 1)
                    .any(|a| a.kind() == SyntaxKind::SpreadElement)
                {
                    return false;
                }
                let Some(argument) = arguments.iter().nth(position) else {
                    return false;
                };
                if !proven_receiver(ctx, index, family, argument, depth + 1) {
                    return false;
                }
                calls += 1;
            }
            _ => return false,
        }
    }
    calls > 0
}

/// `this` inside an instance member of a family class, where the member
/// only runs with an instance receiver.
fn this_is_instance(ctx: &mut Ctx, index: &Index, family: &[SymbolId], node: Node) -> bool {
    let container = get_this_container(node, false, false);
    let class = container.parent();
    if !matches!(
        class.kind(),
        SyntaxKind::ClassDeclaration | SyntaxKind::ClassExpression
    ) || class.name().is_nil()
    {
        return false;
    }
    let symbol = ctx.resolve(class.name());
    if !family.contains(&symbol) || is_static(container) {
        return false;
    }
    match container.kind() {
        // `this` before `super()` throws identically in both forms.
        SyntaxKind::Constructor => true,
        SyntaxKind::PropertyDeclaration => container.initializer().is_some(),
        SyntaxKind::MethodDeclaration => {
            let name = container.name();
            matches!(
                name.kind(),
                SyntaxKind::Identifier | SyntaxKind::PrivateIdentifier
            ) && member_called_only(ctx, index, name.text())
                && !reachable_dynamically(ctx, index, family, name.text())
        }
        _ => false,
    }
}

/// Whether a computed access or `in` test may use `name` as a key on a
/// value that may be a family instance, or reflection may reach the
/// family's prototypes.
fn reachable_dynamically(ctx: &mut Ctx, index: &Index, family: &[SymbolId], name: &str) -> bool {
    for &symbol in family {
        let instance = ctx.checker.get_declared_type_of_symbol(symbol);
        for &(_, object, key) in &index.dynamic {
            let key_type = ctx.checker.get_type_at_location(key);
            if !key_may_be(ctx, key_type, name) {
                continue;
            }
            if expression_may_hold(ctx, object, instance, 0) {
                return true;
            }
        }
    }
    false
}

/// Whether reflective sites may observe a family instance or prototype.
fn reflected(ctx: &mut Ctx, index: &mut Index, family: &[SymbolId]) -> bool {
    for &symbol in family {
        if let Some(&reached) = index.unreachable.get(&symbol) {
            if reached {
                return true;
            }
            continue;
        }
        let instance = ctx.checker.get_declared_type_of_symbol(symbol);
        let mut reached = false;
        for &expression in &index.reflective {
            if expression_may_hold(ctx, expression, instance, 0) {
                reached = true;
                break;
            }
        }
        index.unreachable.insert(symbol, reached);
        if reached {
            return true;
        }
    }
    false
}

/// A planned call site.
struct Site {
    file: usize,
    call: Node,
    access: Node,
}

fn plan_method(
    ctx: &mut Ctx,
    facts: &mut Facts,
    index: &mut Index,
    file: usize,
    class: Node,
    method: Node,
) -> Result<Candidate, Rejection> {
    let name_node = method.name();
    let name = name_node.text().to_owned();
    let body = method.body();
    if body.is_nil()
        || is_static(method)
        || method.question_token().is_some()
        || has_syntactic_modifier(method, ModifierFlags::Abstract | ModifierFlags::Override)
    {
        return Err(None);
    }
    // Every mention is a direct member call.
    let sites: Vec<Site> = index
        .accesses
        .get(&name)
        .map(|list| {
            list.iter()
                .filter(|(_, access)| callee_of_call(*access))
                .map(|&(file, access)| Site {
                    file,
                    call: walk_up_outer(access).parent(),
                    access,
                })
                .collect()
        })
        .unwrap_or_default();
    if sites.is_empty() {
        return Err(None);
    }
    if ctx.external_names().contains(name.as_str()) {
        return Err(Some((Reason::ExternalBoundary, name_node)));
    }
    if index.javascript.iter().any(|t| t.contains(name.as_str())) {
        return Err(Some((Reason::ExternalBoundary, name_node)));
    }
    if ctx.name_mentions(&name) != sites.len() + 1 {
        return Err(Some((Reason::ObservableIdentity, name_node)));
    }
    let class_facts = class_closed(ctx, class).map_err(Some)?;
    if reachable_dynamically(ctx, index, &class_facts.family, &name) {
        return Err(Some((Reason::DynamicAccess, name_node)));
    }
    if reflected(ctx, index, &class_facts.family) {
        return Err(Some((Reason::Reflection, name_node)));
    }
    // Receivers: runtime evidence of non-nullish family instances.
    for site in &sites {
        if site.call.flags().intersects(NodeFlags::OptionalChain)
            || site.call.question_dot_token().is_some()
            || site.access.question_dot_token().is_some()
            || !site.call.type_arguments().is_empty()
        {
            return Err(Some((Reason::UnsupportedSyntax, site.call)));
        }
        let receiver = site.access.expression();
        if receiver.kind() == SyntaxKind::SuperKeyword {
            return Err(Some((Reason::Inheritance, site.call)));
        }
        if !proven_receiver(ctx, index, &class_facts.family, receiver, 0) {
            return Err(Some((Reason::UnprovenRuntimeFact, site.call)));
        }
    }
    check_body(ctx, method).map_err(Some)?;
    // Allocate the function and receiver names.
    let mut files: Vec<usize> = sites.iter().map(|s| s.file).collect();
    files.push(file);
    files.sort();
    files.dedup();
    let names = facts.fresh_names(ctx, &files, &HashSet::new(), 1);
    let function_name = names[0].clone();
    let taken: HashSet<String> = [function_name.clone()].into();
    let receiver_name = facts.fresh_names(ctx, &[file], &taken, 1)[0].clone();
    // Cross-module sites need an export and an existing runtime import.
    let mut edits = Vec::new();
    let cross: Vec<usize> = files.iter().copied().filter(|&f| f != file).collect();
    let exported = !cross.is_empty();
    if exported && (ctx.files[file].entry || facts.namespace_observed(ctx, file)) {
        return Err(Some((Reason::PublicExport, name_node)));
    }
    for &site_file in &cross {
        let edit = import_edit(ctx, site_file, file, &function_name)
            .ok_or(Some((Reason::ModuleInitialization, name_node)))?;
        edits.push(edit);
    }
    // Site rewrites, nesting inner rewrites into outer ones.
    edits.extend(site_edits(ctx, &sites, &function_name)?);
    // Move the method.
    let function = function_text(
        ctx,
        file,
        class,
        method,
        &function_name,
        &receiver_name,
        exported,
    )
    .ok_or(Some((Reason::UnsupportedSyntax, name_node)))?;
    let source = &ctx.files[file].name;
    edits.push(FileEdit::delete(
        source,
        support::removal_start(ctx, file, method),
        method.end() as usize,
    ));
    edits.push(FileEdit::insert(source, class.end() as usize, function));
    // Within one bundle, edits must not overlap.
    let mut sorted: Vec<&FileEdit> = edits.iter().collect();
    sorted.sort_by(|a, b| {
        (&a.file, a.edit.start, a.edit.end).cmp(&(&b.file, b.edit.start, b.edit.end))
    });
    for pair in sorted.windows(2) {
        if pair[0].file == pair[1].file
            && (pair[0].edit.start == pair[1].edit.start || pair[1].edit.start < pair[0].edit.end)
        {
            return Err(Some((Reason::UnsupportedSyntax, name_node)));
        }
    }
    let _ = class_facts.symbol;
    let mut candidate = Candidate::new(PASS, String::new(), ctx.location(file, name_node));
    candidate.edits = edits;
    Ok(candidate)
}

/// Rejects bodies whose meaning depends on being a method.
fn check_body(ctx: &mut Ctx, method: Node) -> Result<(), (Reason, Node)> {
    let mut nodes = support::subtree(method.body());
    for parameter in method.parameters().iter() {
        if parameter.name().kind() == SyntaxKind::Identifier && parameter.name().text() == "this" {
            return Err((Reason::UnsupportedSyntax, parameter));
        }
        nodes.extend(support::subtree(parameter));
    }
    if method.type_().is_some() {
        nodes.extend(support::subtree(method.type_()));
    }
    for node in nodes {
        match node.kind() {
            SyntaxKind::SuperKeyword => return Err((Reason::Inheritance, node)),
            SyntaxKind::ThisType | SyntaxKind::PrivateIdentifier => {
                return Err((Reason::UnsupportedSyntax, node));
            }
            SyntaxKind::MetaProperty => return Err((Reason::UnsupportedSyntax, node)),
            SyntaxKind::Identifier if node.text() == "arguments" => {
                return Err((Reason::ObservableArity, node));
            }
            SyntaxKind::ThisKeyword => {
                if get_this_container(node, false, false) != method {
                    continue;
                }
                let parent = walk_up_outer(node).parent();
                let receiver = matches!(
                    parent.kind(),
                    SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
                ) && skip_outer(parent.expression()) == node;
                if !receiver {
                    return Err((Reason::ObservableIdentity, node));
                }
            }
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
                let name = if node.kind() == SyntaxKind::PropertyAccessExpression {
                    node.name()
                } else {
                    node.argument_expression()
                };
                if node.kind() == SyntaxKind::ElementAccessExpression {
                    // Bracket access is exempt from accessibility checks.
                    continue;
                }
                let symbol = ctx.checker.get_symbol_at_location(name);
                if symbol.is_some() {
                    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
                    if declarations.iter().any(|&d| {
                        has_syntactic_modifier(d, ModifierFlags::Private | ModifierFlags::Protected)
                    }) {
                        return Err((Reason::UnsupportedSyntax, node));
                    }
                }
            }
            SyntaxKind::NewExpression => {
                let callee = skip_outer(node.expression());
                if callee.kind() == SyntaxKind::Identifier {
                    let symbol = ctx.resolve(callee);
                    if symbol.is_some() {
                        let declarations = ctx.checker.sym(symbol).declarations.to_vec();
                        let restricted = declarations.iter().any(|&d| {
                            d.kind() == SyntaxKind::ClassDeclaration
                                && d.members().iter().any(|m| {
                                    m.kind() == SyntaxKind::Constructor
                                        && has_syntactic_modifier(
                                            m,
                                            ModifierFlags::Private | ModifierFlags::Protected,
                                        )
                                })
                        });
                        if restricted {
                            return Err((Reason::UnsupportedSyntax, node));
                        }
                    }
                }
            }
            _ => {}
        }
    }
    Ok(())
}

/// The module-level function replacing the method.
fn function_text(
    ctx: &Ctx,
    file: usize,
    class: Node,
    method: Node,
    name: &str,
    receiver: &str,
    exported: bool,
) -> Option<String> {
    let text = &ctx.files[file].text;
    let slice = |node: Node| &text[Ctx::start(node)..node.end() as usize];
    // Type parameters: the class's, then the method's.
    let mut type_parameters: Vec<String> = Vec::new();
    let mut type_arguments: Vec<String> = Vec::new();
    for parameter in class.type_parameters().iter() {
        type_parameters.push(slice(parameter).to_owned());
        type_arguments.push(parameter.name().text().to_owned());
    }
    for parameter in method.type_parameters().iter() {
        type_parameters.push(slice(parameter).to_owned());
    }
    let class_type = if type_arguments.is_empty() {
        class.name().text().to_owned()
    } else {
        format!("{}<{}>", class.name().text(), type_arguments.join(", "))
    };
    let mut parameters = vec![format!("{receiver}: {class_type}")];
    for parameter in method.parameters().iter() {
        parameters.push(slice(parameter).to_owned());
    }
    let return_type = if method.type_().is_some() {
        format!(": {}", slice(method.type_()))
    } else {
        String::new()
    };
    // Replace `this` of the method itself.
    let body = method.body();
    let mut replacements: Vec<(usize, usize)> = Vec::new();
    let mut parameter_nodes = Vec::new();
    for parameter in method.parameters().iter() {
        parameter_nodes.extend(support::subtree(parameter));
    }
    let body_nodes = support::subtree(body);
    for &node in body_nodes.iter() {
        if node.kind() == SyntaxKind::ThisKeyword
            && get_this_container(node, false, false) == method
        {
            replacements.push((Ctx::start(node), node.end() as usize));
        }
    }
    // Parameter initializers may also use `this`.
    if parameter_nodes
        .iter()
        .any(|n| n.kind() == SyntaxKind::ThisKeyword)
    {
        return None;
    }
    replacements.sort();
    let mut body_text = String::new();
    let mut cursor = Ctx::start(body);
    for (start, end) in replacements {
        body_text.push_str(&text[cursor..start]);
        body_text.push_str(receiver);
        cursor = end;
    }
    body_text.push_str(&text[cursor..body.end() as usize]);
    let flags = method.modifier_flags();
    let prefix = if exported { "export " } else { "" };
    let asynchronous = if flags.intersects(ModifierFlags::Async) {
        "async "
    } else {
        ""
    };
    let star = if method.asterisk_token().is_some() {
        "*"
    } else {
        ""
    };
    let generics = if type_parameters.is_empty() {
        String::new()
    } else {
        format!("<{}>", type_parameters.join(", "))
    };
    Some(format!(
        "\n{prefix}{asynchronous}function{star} {name}{generics}({}){return_type} {body_text}",
        parameters.join(", ")
    ))
}

/// Adds the new function to an existing runtime named import of the class
/// module in `site_file`.
fn import_edit(ctx: &mut Ctx, site_file: usize, target: usize, name: &str) -> Option<FileEdit> {
    let statements: Vec<Node> = ctx.files[site_file].node.statements().to_vec();
    for statement in statements {
        if statement.kind() != SyntaxKind::ImportDeclaration {
            continue;
        }
        let clause = statement.import_clause();
        if clause.is_nil() || clause.is_type_only() {
            continue;
        }
        if Facts::target(ctx, statement.module_specifier()) != Some(target) {
            continue;
        }
        let bindings = clause.named_bindings();
        if bindings.is_nil() || bindings.kind() != SyntaxKind::NamedImports {
            continue;
        }
        // The declaration must already be a runtime import, or adding a
        // value binding would add a module evaluation.
        let used = bindings.elements().iter().any(|element| {
            if element.is_type_only() {
                return false;
            }
            let symbol = ctx.resolve(element.name());
            symbol.is_some()
                && ctx.refs(symbol).iter().any(|r| {
                    r.file == site_file
                        && !matches!(
                            r.kind,
                            RefKind::Type | RefKind::Alias | RefKind::Declaration
                        )
                })
        });
        if !used {
            continue;
        }
        let last = bindings.elements().last()?;
        return Some(FileEdit::insert(
            &ctx.files[site_file].name,
            last.end() as usize,
            format!(", {name}"),
        ));
    }
    None
}

/// Rewrites every call site. Sites nested in another site's receiver or
/// arguments are folded into the outer replacement.
fn site_edits(ctx: &Ctx, sites: &[Site], function: &str) -> Result<Vec<FileEdit>, Rejection> {
    let mut by_file: BTreeMap<usize, Vec<&Site>> = BTreeMap::new();
    for site in sites {
        by_file.entry(site.file).or_default().push(site);
    }
    let mut edits = Vec::new();
    for (file, mut list) in by_file {
        // Inner (smaller) sites first.
        list.sort_by_key(|s| {
            (
                s.call.end() as usize - Ctx::start(s.call),
                Ctx::start(s.call),
            )
        });
        let mut done: Vec<(usize, usize, String)> = Vec::new();
        for site in list {
            let text = |start: usize, end: usize, done: &[(usize, usize, String)]| {
                let mut inner: Vec<&(usize, usize, String)> = done
                    .iter()
                    .filter(|(s, e, _)| start <= *s && *e <= end)
                    .collect();
                inner.sort_by_key(|(s, _, _)| *s);
                let mut out = String::new();
                let mut cursor = start;
                for (s, e, replacement) in inner {
                    if *s < cursor {
                        continue;
                    }
                    out.push_str(&ctx.files[file].text[cursor..*s]);
                    out.push_str(replacement);
                    cursor = *e;
                }
                out.push_str(&ctx.files[file].text[cursor..end]);
                out
            };
            let receiver = site.access.expression();
            let receiver_text = text(Ctx::start(receiver), receiver.end() as usize, &done);
            let receiver_text = if support::comma(skip_outer(receiver)) {
                format!("({receiver_text})")
            } else {
                receiver_text
            };
            let arguments: Vec<Node> = site.call.arguments().to_vec();
            let mut replacement = format!("{function}({receiver_text}");
            if let (Some(first), Some(last)) = (arguments.first(), arguments.last()) {
                replacement.push_str(", ");
                replacement.push_str(&text(Ctx::start(*first), last.end() as usize, &done));
            }
            replacement.push(')');
            let (start, end) = (Ctx::start(site.call), site.call.end() as usize);
            // Drop folded inner sites; the outer replacement contains them.
            done.retain(|(s, e, _)| !(start <= *s && *e <= end));
            done.push((start, end, replacement));
        }
        for (start, end, replacement) in done {
            edits.push(FileEdit::replace(
                &ctx.files[file].name,
                start,
                end,
                replacement,
            ));
        }
    }
    Ok(edits)
}
