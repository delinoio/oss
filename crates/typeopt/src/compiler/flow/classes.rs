// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Closed classes: class instances as values of the property flow.
//!
//! A class is closed when the analysis sees every instance it creates and
//! every receiver its instance code runs with:
//! - it is a class declaration, or a class expression that initializes a
//!   variable, declared once in an application module (not a script or a
//!   namespace) and not exposed (public entry exports, JavaScript references
//!   and namespace objects);
//! - its symbol is only declared, imported, exported, constructed with `new`,
//!   extended by another closed class, used as the right operand of
//!   `instanceof`, or read as `C.member` without writing it, where the class
//!   chain declares `member` as a static member or `member` is `name` or
//!   `length` (`Function.prototype` methods such as `bind` could construct
//!   instances);
//! - it extends nothing or another closed class, implements nothing, has no
//!   decorators (on members or parameters), `declare`, `abstract`, `accessor`,
//!   overloads or parameter properties, never uses `new.target` or
//!   `import.meta`, returns no value from its constructor, uses `super` only as
//!   `super(…)` in the constructor or `super.m(…)` in instance members, and has
//!   no static member that uses `this` or `super`. The one exception is a
//!   `keepNames` static block `static{H(this,"Name")}`, where `H` is a helper
//!   that only runs
//!   `Object.defineProperty(a,"name",{value:b,configurable:true})` on its
//!   arguments (see `name_helper_block`): it defines the constructor's own
//!   `name` and exposes neither the class nor an instance. After call inlining,
//!   the same block may be the inlined body
//!   `D(this,"name",{value:"Name",configurable:true})`.
//!
//! One value stands for the instances of each closed class. `this` in the
//! instance members of a class holds that value and the values of every
//! subclass. A construction passes its arguments to the nearest explicit
//! constructor, `x.m(…)` dispatches on each instance that reaches `x`, and
//! `C.m(…)` uses the summary of a static method whose every reference is a
//! direct call with its exact arity. A later prototype member replaces an
//! earlier one of the same name, and an instance field of any class in the
//! chain shadows both.
//! Members with computed (well-known symbol) names have no summary, because
//! protocol code calls them. Methods with rest or nested-pattern
//! parameters, `arguments`, or async or generator bodies have no summary:
//! their arguments escape and their results are unknown. Loading a method
//! without calling it, calling anything other than a method and `delete` or
//! tagged templates through an instance preserve its region, because the
//! function could then run with another receiver. When an instance escapes,
//! the parameters and results of every instance and static method and the
//! `this` cells of its class chain become unknown.
//!
//! A value on the left of `instanceof` stays owned only when the right
//! operand is a closed class whose chain has no computed static member, so no
//! custom `Symbol.hasInstance` can receive it.
use std::collections::{BTreeMap, HashMap, HashSet};

use super::{
    Builder, CellId, EMPTY, Key, Kind, MethodCall, SPECIAL_NAMES, Target, UNKNOWN, Value,
    collect_nodes, flat_pattern, in_namespace, occurrence, owner,
};
use crate::{
    compiler::{
        analysis::{RefKind, skip_outer, walk_up_outer},
        api::*,
    },
    optimizer::{Object, Preservation, Property},
};

/// Name of the constructor entry in method tables; never a property name.
pub(super) const CONSTRUCTOR: &str = " new";

/// Properties of `Symbol` whose values are well-known symbols in every
/// supported engine. Any other property is a string or number key
/// (`Symbol.name`, `Symbol.length`) or may be missing (`Symbol.dispose`),
/// which makes the key the string `"undefined"`.
const WELL_KNOWN_SYMBOLS: &[&str] = &[
    "asyncIterator",
    "hasInstance",
    "isConcatSpreadable",
    "iterator",
    "match",
    "matchAll",
    "replace",
    "search",
    "species",
    "split",
    "toPrimitive",
    "toStringTag",
    "unscopables",
];

/// Names that every object inherits from `Object.prototype` or that carry
/// protocol meaning. An instance access to one of them is never renamed.
pub(super) fn inherited_name(name: &str) -> bool {
    SPECIAL_NAMES.contains(&name)
        || matches!(
            name,
            "hasOwnProperty"
                | "isPrototypeOf"
                | "propertyIsEnumerable"
                | "__defineGetter__"
                | "__defineSetter__"
                | "__lookupGetter__"
                | "__lookupSetter__"
        )
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Member {
    Method(Node),
    Accessor,
    Field,
}

pub(super) struct Class {
    node: Node,
    value: Value,
    this_cell: CellId,
    parent_symbol: Option<SymbolId>,
    parent: Option<usize>,
    /// Own instance members by name, first declaration first. Private names
    /// keep their `#`. A name that a class declares both as a method and
    /// otherwise resolves to the other declaration.
    members: Vec<(&'static str, Member)>,
    constructor: Option<Node>,
    /// Static members by name; methods carry their declaration.
    statics: Vec<(&'static str, Option<Node>)>,
    /// `C.name` references through this class's symbols, with the call
    /// that each one is the callee of.
    static_uses: Vec<(&'static str, Option<Node>)>,
    /// A static member has a computed name (it may define
    /// `Symbol.hasInstance`).
    static_computed: bool,
}

/// A closed-class candidate: binding symbols (the inner name of a named
/// class expression second), base class symbol, the classes that extend
/// it and its static member references.
pub(super) struct Candidate {
    symbols: Vec<SymbolId>,
    parent: Option<SymbolId>,
    subclasses: Vec<Node>,
    static_uses: Vec<(&'static str, Option<Node>)>,
    /// Names of the static members the class declares itself.
    static_names: Vec<&'static str>,
}

/// Static reads of a class that no declared member answers and that cannot
/// reach the constructor: `Function.prototype` methods such as `bind`,
/// `call` or `toString` could construct instances or reveal the source.
const SAFE_CONSTRUCTOR_READS: &[&str] = &["name", "length"];

/// Why a static block that uses `this` is not the `keepNames` naming block
/// `static{H(this,"Name")}` of a proven helper `H`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum NameBlockMismatch {
    /// The block is not exactly one call `H(…)` with an identifier callee and
    /// two arguments without spread.
    NotSingleCall,
    /// The first argument is not `this`.
    ReceiverArgument,
    /// The second argument is not a string or a substitution-free template.
    NonLiteralName,
    /// The callee does not resolve to a symbol with exactly one declaration
    /// in a checked file.
    HelperUnresolved,
    /// The helper symbol is assigned somewhere.
    HelperWritten,
    /// The helper is not exactly
    /// `(a,b)=>D(a,"name",{value:b,configurable:true})`.
    HelperShape,
    /// `D` is neither `Object.defineProperty` nor a single-declaration,
    /// unwritten alias of it.
    DefinePropertyAlias,
    /// `Object` is not the library global.
    ShadowedObject,
    /// Application code, or an external runtime import, may replace `Object`
    /// or `Object.defineProperty`, so a call to either form can run
    /// arbitrary code with the constructor.
    DefinePropertyPatched,
}

fn contains_receiver(node: Node) -> bool {
    let mut nodes = Vec::new();
    collect_nodes(node, &mut nodes);
    nodes
        .iter()
        .any(|n| matches!(n.kind(), SyntaxKind::ThisKeyword | SyntaxKind::SuperKeyword))
}

/// A string or a substitution-free template literal.
fn is_string_literal(node: Node) -> bool {
    matches!(
        node.kind(),
        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
    )
}

/// `{value: V, configurable: true}` in either property order, with plain
/// (non-computed, non-shorthand) keys, where `value_ok` accepts `V`.
fn name_descriptor(descriptor: Node, mut value_ok: impl FnMut(Node) -> bool) -> bool {
    if descriptor.kind() != SyntaxKind::ObjectLiteralExpression
        || descriptor.properties().len() != 2
    {
        return false;
    }
    let (mut value, mut configurable) = (false, false);
    for property in descriptor.properties().iter() {
        if property.kind() != SyntaxKind::PropertyAssignment {
            return false;
        }
        let key = property.name();
        let initializer = skip_outer(property.initializer());
        if !matches!(
            key.kind(),
            SyntaxKind::Identifier | SyntaxKind::StringLiteral
        ) {
            return false;
        }
        match key.text() {
            "value" if !value && value_ok(initializer) => value = true,
            "configurable" if !configurable && true_literal(initializer) => configurable = true,
            _ => return false,
        }
    }
    value && configurable
}

/// `true` or its minified form `!0`.
fn true_literal(node: Node) -> bool {
    match node.kind() {
        SyntaxKind::TrueKeyword => true,
        SyntaxKind::PrefixUnaryExpression => {
            let operand = skip_outer(node.operand());
            node.operator() == SyntaxKind::ExclamationToken
                && operand.kind() == SyntaxKind::NumericLiteral
                && operand.text() == "0"
        }
        _ => false,
    }
}

/// A summarized member owns its parameters and results.
fn summarizable(node: Node) -> bool {
    if node.body().is_nil()
        || get_combined_modifier_flags(node).intersects(ModifierFlags::Async)
        || node.asterisk_token().is_some()
    {
        return false;
    }
    let parameters = node.parameters().iter().all(|p| {
        p.dot_dot_dot_token().is_nil()
            && match p.name().kind() {
                SyntaxKind::Identifier => p.name().text() != "this",
                SyntaxKind::ObjectBindingPattern => flat_pattern(p.name()),
                _ => false,
            }
    });
    if !parameters {
        return false;
    }
    let mut nodes = Vec::new();
    collect_nodes(node, &mut nodes);
    !nodes
        .iter()
        .any(|n| n.kind() == SyntaxKind::Identifier && n.text() == "arguments")
}

impl Builder<'_, '_> {
    /// The binding that names a class: its declaration name or the
    /// variable that a class expression initializes.
    fn class_binding(node: Node) -> Option<Node> {
        match node.kind() {
            SyntaxKind::ClassDeclaration if node.name().is_some() => Some(node.name()),
            SyntaxKind::ClassExpression
                if node.parent().kind() == SyntaxKind::VariableDeclaration
                    && node.parent().initializer() == node
                    && node.parent().name().kind() == SyntaxKind::Identifier =>
            {
                Some(node.parent().name())
            }
            _ => None,
        }
    }

    /// `Symbol.iterator` and the other well-known symbols name no string
    /// property.
    fn well_known_symbol(&self, key: Node) -> bool {
        let expression = skip_outer(key.expression());
        if expression.kind() != SyntaxKind::PropertyAccessExpression {
            return false;
        }
        let object = skip_outer(expression.expression());
        expression.name().kind() == SyntaxKind::Identifier
            && WELL_KNOWN_SYMBOLS.contains(&expression.name().text())
            && object.kind() == SyntaxKind::Identifier
            && object.text() == "Symbol"
            && self.lib_global(object)
    }

    /// `node` is an identifier that names a library global: it resolves to no
    /// declaration or only to declarations in `.d.ts` files.
    fn lib_global(&self, node: Node) -> bool {
        self.symbol(node).is_none_or(|symbol| {
            self.ctx.checker.sym(symbol).declarations.iter().all(|&d| {
                source_file_info(get_source_file_of_node(d))
                    .file_name
                    .ends_with(".d.ts")
            })
        })
    }

    /// The only declaration of `symbol` when it is a variable with an
    /// identifier name or a function, written nowhere, in a checked (not
    /// JavaScript boundary) file.
    fn single_binding(&self, symbol: SymbolId) -> Result<Node, NameBlockMismatch> {
        let declarations = &self.ctx.checker.sym(symbol).declarations;
        let [declaration] = declarations.as_slice() else {
            return Err(NameBlockMismatch::HelperUnresolved);
        };
        let declaration = *declaration;
        let shape = match declaration.kind() {
            SyntaxKind::VariableDeclaration => declaration.name().kind() == SyntaxKind::Identifier,
            SyntaxKind::FunctionDeclaration => declaration.name().kind() == SyntaxKind::Identifier,
            _ => false,
        };
        let checked = self
            .ctx
            .file_of_node(declaration)
            .is_some_and(|file| !self.ctx.files[file].js);
        if !shape || !checked || declaration.flags().intersects(NodeFlags::Ambient) {
            return Err(NameBlockMismatch::HelperUnresolved);
        }
        if self
            .ctx
            .refs(symbol)
            .iter()
            .any(|r| r.kind == RefKind::Write)
        {
            return Err(NameBlockMismatch::HelperWritten);
        }
        Ok(declaration)
    }

    /// Neither the `Object` global nor `Object.defineProperty` is replaced
    /// by application code or an external runtime import. Computed once per
    /// program through the shared intrinsic patch scan; it covers the direct
    /// and the aliased form of the callee.
    fn define_property_intact(&mut self) -> bool {
        if let Some(intact) = self.define_property_intact {
            return intact;
        }
        let intact = super::builtin_intact(self.ctx, "Object", "defineProperty");
        tracing::debug!(intact, "Object.defineProperty patch query");
        self.define_property_intact = Some(intact);
        intact
    }

    /// `node` is exactly `Object.defineProperty` with the library `Object`.
    fn is_define_property(&self, node: Node) -> Result<(), NameBlockMismatch> {
        let node = skip_outer(node);
        if node.kind() != SyntaxKind::PropertyAccessExpression
            || node.question_dot_token().is_some()
            || node.name().kind() != SyntaxKind::Identifier
            || node.name().text() != "defineProperty"
        {
            return Err(NameBlockMismatch::DefinePropertyAlias);
        }
        let object = skip_outer(node.expression());
        if object.kind() != SyntaxKind::Identifier || object.text() != "Object" {
            return Err(NameBlockMismatch::DefinePropertyAlias);
        }
        if !self.lib_global(object) {
            return Err(NameBlockMismatch::ShadowedObject);
        }
        Ok(())
    }

    /// `node` is `Object.defineProperty` or an identifier of a single
    /// unwritten variable initialized with exactly that.
    fn define_property_callee(&self, node: Node) -> Result<(), NameBlockMismatch> {
        let node = skip_outer(node);
        if node.kind() != SyntaxKind::Identifier {
            return self.is_define_property(node);
        }
        let symbol = self
            .symbol(node)
            .ok_or(NameBlockMismatch::DefinePropertyAlias)?;
        let declaration = self
            .single_binding(symbol)
            .map_err(|_| NameBlockMismatch::DefinePropertyAlias)?;
        if declaration.kind() != SyntaxKind::VariableDeclaration
            || declaration.initializer().is_nil()
        {
            return Err(NameBlockMismatch::DefinePropertyAlias);
        }
        self.is_define_property(declaration.initializer())
    }

    /// `static{D(this,"name",{value:"Name",configurable:true})}`: the helper
    /// call after call inlining. `D` is `Object.defineProperty` or an alias
    /// of it, as in `name_helper_block`.
    fn inlined_name_block(
        &self,
        callee: Node,
        arguments: NodeSlice,
    ) -> Result<(), NameBlockMismatch> {
        if skip_outer(arguments.get(0)).kind() != SyntaxKind::ThisKeyword {
            return Err(NameBlockMismatch::ReceiverArgument);
        }
        let key = skip_outer(arguments.get(1));
        if !is_string_literal(key) {
            return Err(NameBlockMismatch::NonLiteralName);
        }
        let descriptor = skip_outer(arguments.get(2));
        if key.text() != "name" || !name_descriptor(descriptor, is_string_literal) {
            return Err(NameBlockMismatch::HelperShape);
        }
        self.define_property_callee(callee)?;
        Ok(())
    }

    /// `static{H(this,"Name")}` where `H` is the `keepNames` helper. Such a
    /// block only runs `Object.defineProperty(C,"name",{value,configurable})`
    /// on the class constructor: it creates no instance and hands the class
    /// to no other code, because the helper is checked exactly:
    /// - the block is one call `H(this, literal)` with an identifier callee;
    /// - `H` has one declaration in a checked file and no write, and is a
    ///   variable initialized with an arrow function or a non-async,
    ///   non-generator function expression, or a function declaration, with two
    ///   plain identifier parameters `a` and `b` whose body is exactly
    ///   `D(a,"name",{value:b,configurable:true})` (`!0` for `true`, an
    ///   optional `return`);
    /// - `D` is `Object.defineProperty` or a single-declaration unwritten alias
    ///   of it, and `Object` is the library global.
    fn name_helper_block(&self, member: Node) -> Result<(), NameBlockMismatch> {
        let body = member.static_block_body();
        let statements = body.statements().to_vec();
        let [statement] = statements.as_slice() else {
            return Err(NameBlockMismatch::NotSingleCall);
        };
        if statement.kind() != SyntaxKind::ExpressionStatement {
            return Err(NameBlockMismatch::NotSingleCall);
        }
        let call = skip_outer(statement.expression());
        if call.kind() != SyntaxKind::CallExpression {
            return Err(NameBlockMismatch::NotSingleCall);
        }
        let callee = skip_outer(call.expression());
        let arguments = call.arguments();
        if call.question_dot_token().is_some()
            || !matches!(arguments.len(), 2 | 3)
            || arguments
                .iter()
                .any(|a| a.kind() == SyntaxKind::SpreadElement)
        {
            return Err(NameBlockMismatch::NotSingleCall);
        }
        if arguments.len() == 3 {
            return self.inlined_name_block(callee, arguments);
        }
        if callee.kind() != SyntaxKind::Identifier {
            return Err(NameBlockMismatch::NotSingleCall);
        }
        if skip_outer(arguments.get(0)).kind() != SyntaxKind::ThisKeyword {
            return Err(NameBlockMismatch::ReceiverArgument);
        }
        let name = skip_outer(arguments.get(1));
        if !is_string_literal(name) {
            return Err(NameBlockMismatch::NonLiteralName);
        }
        let symbol = self
            .symbol(callee)
            .ok_or(NameBlockMismatch::HelperUnresolved)?;
        let declaration = self.single_binding(symbol)?;
        let function = match declaration.kind() {
            SyntaxKind::FunctionDeclaration => declaration,
            _ if declaration.initializer().is_some() => skip_outer(declaration.initializer()),
            _ => return Err(NameBlockMismatch::HelperShape),
        };
        if !matches!(
            function.kind(),
            SyntaxKind::ArrowFunction
                | SyntaxKind::FunctionExpression
                | SyntaxKind::FunctionDeclaration
        ) || get_combined_modifier_flags(function).intersects(ModifierFlags::Async)
            || function.asterisk_token().is_some()
            || function.parameters().len() != 2
        {
            return Err(NameBlockMismatch::HelperShape);
        }
        let mut names = Vec::new();
        for parameter in function.parameters().iter() {
            let plain = parameter.name().kind() == SyntaxKind::Identifier
                && parameter.name().text() != "this"
                && parameter.dot_dot_dot_token().is_nil()
                && parameter.question_token().is_nil()
                && parameter.initializer().is_nil();
            if !plain {
                return Err(NameBlockMismatch::HelperShape);
            }
            names.push(parameter.name().text());
        }
        if names[0] == names[1] {
            return Err(NameBlockMismatch::HelperShape);
        }
        let mut inner = function.body();
        if inner.kind() == SyntaxKind::Block {
            let statements = inner.statements().to_vec();
            let [statement] = statements.as_slice() else {
                return Err(NameBlockMismatch::HelperShape);
            };
            if !matches!(
                statement.kind(),
                SyntaxKind::ReturnStatement | SyntaxKind::ExpressionStatement
            ) || statement.expression().is_nil()
            {
                return Err(NameBlockMismatch::HelperShape);
            }
            inner = statement.expression();
        }
        let define = skip_outer(inner);
        if define.kind() != SyntaxKind::CallExpression || define.question_dot_token().is_some() {
            return Err(NameBlockMismatch::HelperShape);
        }
        let arguments = define.arguments();
        if arguments.len() != 3 {
            return Err(NameBlockMismatch::HelperShape);
        }
        let (target, key, descriptor) = (
            skip_outer(arguments.get(0)),
            skip_outer(arguments.get(1)),
            skip_outer(arguments.get(2)),
        );
        if target.kind() != SyntaxKind::Identifier
            || target.text() != names[0]
            || !is_string_literal(key)
            || key.text() != "name"
            || descriptor.kind() != SyntaxKind::ObjectLiteralExpression
            || descriptor.properties().len() != 2
        {
            return Err(NameBlockMismatch::HelperShape);
        }
        if !name_descriptor(descriptor, |value| {
            value.kind() == SyntaxKind::Identifier && value.text() == names[1]
        }) {
            return Err(NameBlockMismatch::HelperShape);
        }
        self.define_property_callee(define.expression())?;
        Ok(())
    }

    fn class_candidate(
        &mut self,
        node: Node,
        name_blocks: &mut usize,
    ) -> Result<Candidate, &'static str> {
        let binding = Self::class_binding(node).ok_or("anonymous")?;
        let symbol = self.symbol(binding).ok_or("unresolved")?;
        if !is_external_module(get_source_file_of_node(node)) || in_namespace(node) {
            return Err("scope");
        }
        if !node.decorators().is_empty() {
            return Err("decorator");
        }
        if !self.internal(symbol) || self.ctx.checker.sym(symbol).declarations.len() != 1 {
            return Err("external");
        }
        let mut symbols = vec![(symbol, binding)];
        if node.kind() == SyntaxKind::ClassExpression && node.name().is_some() {
            let inner = self.symbol(node.name()).ok_or("named-class-expression")?;
            symbols.push((inner, node.name()));
        }
        let mut parent = None;
        for clause in node.heritage_clauses().iter() {
            if clause.token() != SyntaxKind::ExtendsKeyword {
                return Err("implements");
            }
            let expression = clause
                .types()
                .first()
                .map(|t| skip_outer(t.expression()))
                .filter(|e| e.kind() == SyntaxKind::Identifier)
                .ok_or("heritage")?;
            parent = Some(self.symbol(expression).ok_or("heritage")?);
        }
        for member in node.members().iter() {
            if !member.decorators().is_empty()
                || (matches!(
                    member.kind(),
                    SyntaxKind::Constructor
                        | SyntaxKind::MethodDeclaration
                        | SyntaxKind::GetAccessor
                        | SyntaxKind::SetAccessor
                ) && member
                    .parameters()
                    .iter()
                    .any(|parameter| !parameter.decorators().is_empty()))
            {
                return Err("decorator");
            }
            if has_syntactic_modifier(
                member,
                ModifierFlags::Ambient | ModifierFlags::Abstract | ModifierFlags::Accessor,
            ) {
                return Err("modifier");
            }
            match member.kind() {
                SyntaxKind::Constructor => {
                    if member.body().is_nil() {
                        return Err("overload");
                    }
                    if member.parameters().iter().any(|p| {
                        has_syntactic_modifier(p, ModifierFlags::ParameterPropertyModifier)
                    }) {
                        return Err("parameter-property");
                    }
                }
                SyntaxKind::MethodDeclaration
                | SyntaxKind::GetAccessor
                | SyntaxKind::SetAccessor => {
                    if member.body().is_nil() {
                        return Err("overload");
                    }
                }
                SyntaxKind::PropertyDeclaration
                | SyntaxKind::ClassStaticBlockDeclaration
                | SyntaxKind::IndexSignature
                | SyntaxKind::SemicolonClassElement => {}
                _ => return Err("member"),
            }
            if (is_static(member) || member.kind() == SyntaxKind::ClassStaticBlockDeclaration)
                && contains_receiver(member)
            {
                if member.kind() != SyntaxKind::ClassStaticBlockDeclaration {
                    return Err("static-receiver");
                }
                let block = self.name_helper_block(member).and_then(|()| {
                    if self.define_property_intact() {
                        Ok(())
                    } else {
                        Err(NameBlockMismatch::DefinePropertyPatched)
                    }
                });
                match block {
                    Ok(()) => {
                        *name_blocks += 1;
                        tracing::debug!(
                            file = %source_file_info(get_source_file_of_node(node)).file_name,
                            "keepNames static block accepted"
                        );
                    }
                    Err(mismatch) => {
                        tracing::debug!(
                            file = %source_file_info(get_source_file_of_node(node)).file_name,
                            reason = ?mismatch,
                            "keepNames-shaped static block rejected"
                        );
                        return Err("static-receiver");
                    }
                }
            }
        }
        let mut nodes = Vec::new();
        collect_nodes(node, &mut nodes);
        for &n in &nodes {
            match n.kind() {
                SyntaxKind::MetaProperty => return Err("meta-property"),
                SyntaxKind::SuperKeyword => {
                    let container = get_this_container(n, false, false);
                    if container.parent() != node {
                        continue;
                    }
                    let p = n.parent();
                    let call = p.kind() == SyntaxKind::CallExpression
                        && p.expression() == n
                        && container.kind() == SyntaxKind::Constructor;
                    let method = p.kind() == SyntaxKind::PropertyAccessExpression
                        && p.expression() == n
                        && p.name().kind() == SyntaxKind::Identifier
                        && p.parent().kind() == SyntaxKind::CallExpression
                        && p.parent().expression() == p;
                    if parent.is_none() || !(call || method) {
                        return Err("super");
                    }
                }
                SyntaxKind::ReturnStatement if n.expression().is_some() => {
                    let function = owner(n);
                    if function.kind() == SyntaxKind::Constructor && function.parent() == node {
                        return Err("constructor-return");
                    }
                }
                _ => {}
            }
        }
        let mut subclasses = Vec::new();
        let mut static_uses = Vec::new();
        let references: Vec<_> = symbols
            .iter()
            .flat_map(|&(symbol, binding)| self.ctx.refs(symbol).iter().map(move |&r| (r, binding)))
            .collect();
        for (r, binding) in references {
            if self.ctx.files[r.file].js {
                return Err("reference");
            }
            let accepted = match r.kind {
                RefKind::Declaration => {
                    r.node == binding || r.node.parent().kind() == SyntaxKind::ExportSpecifier
                }
                RefKind::Alias => matches!(
                    r.node.parent().kind(),
                    SyntaxKind::ImportSpecifier
                        | SyntaxKind::ExportSpecifier
                        | SyntaxKind::ImportClause
                ),
                RefKind::Type | RefKind::New => true,
                RefKind::Read => {
                    let outer = walk_up_outer(r.node);
                    let p = outer.parent();
                    if p.kind() == SyntaxKind::BinaryExpression
                        && p.operator_token().kind() == SyntaxKind::InstanceOfKeyword
                        && p.right() == outer
                    {
                        true
                    } else if p.kind() == SyntaxKind::ExpressionWithTypeArguments
                        && p.parent().kind() == SyntaxKind::HeritageClause
                        && p.parent().token() == SyntaxKind::ExtendsKeyword
                    {
                        subclasses.push(p.parent().parent());
                        true
                    } else if p.kind() == SyntaxKind::PropertyAccessExpression
                        && p.expression() == outer
                        && p.name().kind() == SyntaxKind::Identifier
                        && p.name().text() != "prototype"
                        && super::assignment_target(p).is_nil()
                    {
                        let callee = walk_up_outer(p);
                        let call = callee.parent();
                        let direct = call.kind() == SyntaxKind::CallExpression
                            && call.expression() == callee;
                        static_uses.push((p.name().text(), direct.then_some(call)));
                        true
                    } else {
                        false
                    }
                }
                _ => false,
            };
            if !accepted {
                return Err("reference");
            }
        }
        let static_names = node
            .members()
            .iter()
            .filter(|&member| {
                is_static(member)
                    && matches!(
                        member.name().kind(),
                        SyntaxKind::Identifier | SyntaxKind::StringLiteral
                    )
            })
            .map(|member| member.name().text())
            .collect();
        Ok(Candidate {
            symbols: symbols.into_iter().map(|(symbol, _)| symbol).collect(),
            parent,
            subclasses,
            static_uses,
            static_names,
        })
    }

    /// Closed classes with their base class symbols. A class whose base or
    /// any subclass is not closed is not closed either.
    pub(super) fn closed_classes(&mut self, files: &[usize]) -> HashMap<Node, Candidate> {
        let mut candidates = HashMap::<Node, Candidate>::new();
        let mut rejected = BTreeMap::<&'static str, usize>::new();
        let mut name_blocks = 0usize;
        for &file in files {
            for index in 0..self.ctx.files[file].nodes.len() {
                let node = self.ctx.files[file].nodes[index];
                if !matches!(
                    node.kind(),
                    SyntaxKind::ClassDeclaration | SyntaxKind::ClassExpression
                ) {
                    continue;
                }
                match self.class_candidate(node, &mut name_blocks) {
                    Ok(candidate) => {
                        candidates.insert(node, candidate);
                    }
                    Err(reason) => *rejected.entry(reason).or_default() += 1,
                }
            }
        }
        // `C.name` must read a static member that the class chain declares.
        let by_symbol: HashMap<SymbolId, Node> = candidates
            .iter()
            .flat_map(|(&node, c)| c.symbols.iter().map(move |&symbol| (symbol, node)))
            .collect();
        let declared = |candidates: &HashMap<Node, Candidate>, node: Node, name: &str| {
            let mut current = Some(node);
            while let Some(class) = current.and_then(|node| candidates.get(&node)) {
                if class.static_names.contains(&name) {
                    return true;
                }
                current = class.parent.and_then(|p| by_symbol.get(&p).copied());
            }
            false
        };
        let unresolved: Vec<Node> = candidates
            .iter()
            .filter(|&(&node, c)| {
                c.static_uses.iter().any(|&(name, _)| {
                    !SAFE_CONSTRUCTOR_READS.contains(&name) && !declared(&candidates, node, name)
                })
            })
            .map(|(&node, _)| node)
            .collect();
        for node in unresolved {
            candidates.remove(&node);
            *rejected.entry("static-reference").or_default() += 1;
        }
        loop {
            let symbols: HashSet<SymbolId> = candidates
                .values()
                .flat_map(|c| c.symbols.iter().copied())
                .collect();
            let open: Vec<Node> = candidates
                .iter()
                .filter(|(_, c)| {
                    c.parent.is_some_and(|p| !symbols.contains(&p))
                        || c.subclasses.iter().any(|s| !candidates.contains_key(s))
                })
                .map(|(&node, _)| node)
                .collect();
            if open.is_empty() {
                break;
            }
            for node in open {
                candidates.remove(&node);
                *rejected.entry("hierarchy").or_default() += 1;
            }
        }
        tracing::debug!(
            closed = candidates.len(),
            ?rejected,
            name_blocks,
            "closed classes classified"
        );
        candidates
    }

    /// Creates the instance value of a closed class with its declared
    /// instance members as properties.
    pub(super) fn class_allocation(&mut self, file: usize, node: Node, candidate: &Candidate) {
        let mut object = Object {
            file: self.ctx.files[file].name.clone(),
            start: node.pos() as usize,
            properties: Vec::new(),
            preservation: None,
        };
        let mut reason = None;
        let mut members: Vec<(&'static str, Member)> = Vec::new();
        let mut constructor = None;
        let mut statics = Vec::new();
        let mut static_computed = false;
        for member in node.members().iter() {
            static_computed |=
                is_static(member) && member.name().kind() == SyntaxKind::ComputedPropertyName;
            if member.kind() == SyntaxKind::Constructor {
                constructor = Some(member);
                continue;
            }
            if is_static(member)
                && matches!(
                    member.name().kind(),
                    SyntaxKind::Identifier | SyntaxKind::StringLiteral
                )
            {
                // A later static member replaces an earlier one.
                let name = member.name().text();
                let method = member.kind() == SyntaxKind::MethodDeclaration;
                statics.retain(|(existing, _)| *existing != name);
                statics.push((name, method.then_some(member)));
            }
            let kind = match member.kind() {
                SyntaxKind::MethodDeclaration => Member::Method(member),
                SyntaxKind::GetAccessor | SyntaxKind::SetAccessor => Member::Accessor,
                SyntaxKind::PropertyDeclaration => Member::Field,
                _ => continue,
            };
            if is_static(member) {
                continue;
            }
            let name = member.name();
            match name.kind() {
                SyntaxKind::PrivateIdentifier => {}
                SyntaxKind::Identifier | SyntaxKind::StringLiteral => {
                    let key = name.text();
                    if inherited_name(key) {
                        reason.get_or_insert(Preservation::SpecialProperty);
                    }
                    if kind == Member::Field
                        && matches!(
                            skip_outer(member.initializer()).kind(),
                            SyntaxKind::FunctionExpression
                                | SyntaxKind::ArrowFunction
                                | SyntaxKind::ClassExpression
                        )
                    {
                        // Renaming changes the inferred function or class
                        // `.name`.
                        reason.get_or_insert(Preservation::ObservableOperation);
                    }
                    let occurrence = occurrence(&self.ctx.files[file].name, name, false);
                    if let Some(existing) = object.properties.iter_mut().find(|p| p.name == key) {
                        existing.occurrences.push(occurrence);
                    } else {
                        object.properties.push(Property {
                            name: key.to_owned(),
                            occurrences: vec![occurrence],
                        });
                    }
                }
                SyntaxKind::ComputedPropertyName if self.well_known_symbol(name) => continue,
                _ => {
                    reason.get_or_insert(Preservation::ExternalOrUnsupportedShape);
                    continue;
                }
            }
            let key = name.text();
            match members.iter_mut().find(|(n, _)| *n == key) {
                // Instance fields shadow prototype members; among prototype
                // members the later definition replaces the earlier one.
                Some((_, existing)) => {
                    *existing = match (*existing, kind) {
                        (Member::Field, _) | (_, Member::Field) => Member::Field,
                        (_, later) => later,
                    };
                }
                None => members.push((key, kind)),
            }
        }
        let index = self.classes.len();
        let value = self.objects.len() as Value;
        self.objects.push(object);
        self.intrinsic.push(reason);
        self.kinds.push(Kind::Instance(index));
        self.class_by_node.insert(node, index);
        for &symbol in &candidate.symbols {
            self.class_by_symbol.insert(symbol, index);
        }
        self.classes.push(Class {
            node,
            value,
            this_cell: UNKNOWN,
            parent_symbol: candidate.parent,
            parent: None,
            members,
            constructor,
            statics,
            static_uses: candidate.static_uses.clone(),
            static_computed,
        });
    }

    pub(super) fn link_classes(&mut self) {
        for index in 0..self.classes.len() {
            self.classes[index].parent = self.classes[index]
                .parent_symbol
                .and_then(|symbol| self.class_by_symbol.get(&symbol).copied());
        }
    }

    /// `this` cells: each holds its class's instances, and a subclass's
    /// instances also run the members of every base class.
    pub(super) fn class_cells(&mut self) {
        for index in 0..self.classes.len() {
            self.classes[index].this_cell = self.graph.holding(self.classes[index].value);
        }
        for index in 0..self.classes.len() {
            if let Some(parent) = self.classes[index].parent {
                let (from, to) = (
                    self.classes[index].this_cell,
                    self.classes[parent].this_cell,
                );
                self.graph.edge(from, to);
            }
        }
    }

    pub(super) fn summarize_members(&mut self) {
        let mut summarized = 0usize;
        for index in 0..self.classes.len() {
            let members: Vec<Node> = self.classes[index].node.members().iter().collect();
            for member in members {
                if matches!(
                    member.kind(),
                    SyntaxKind::MethodDeclaration
                        | SyntaxKind::GetAccessor
                        | SyntaxKind::SetAccessor
                        | SyntaxKind::Constructor
                ) && !is_static(member)
                    // Protocol code calls well-known-symbol members with its
                    // own arguments and reads their results.
                    && member.name().kind() != SyntaxKind::ComputedPropertyName
                    && summarizable(member)
                {
                    self.summarize(member);
                    summarized += 1;
                }
            }
        }
        // A static method gets a summary when every reference through its
        // class or a subclass calls it directly with exactly its arity.
        let mut statics = 0usize;
        for index in 0..self.classes.len() {
            for (name, method) in self.classes[index].statics.clone() {
                let Some(method) = method else {
                    continue;
                };
                let arity = method.parameters().len();
                let direct = (0..self.classes.len())
                    .filter(|&c| self.extends(c, index))
                    .flat_map(|c| self.classes[c].static_uses.iter())
                    .filter(|(n, _)| *n == name)
                    .all(|(_, call)| {
                        call.is_some_and(|call| {
                            call.arguments().len() == arity
                                && !call
                                    .arguments()
                                    .iter()
                                    .any(|a| a.kind() == SyntaxKind::SpreadElement)
                        })
                    });
                if direct && summarizable(method) {
                    let function = self.summarize(method);
                    self.static_targets.insert((index, name), function);
                    statics += 1;
                }
            }
        }
        tracing::debug!(
            classes = self.classes.len(),
            summarized,
            statics,
            "class member summaries created"
        );
    }

    /// `class` is `ancestor` or extends it.
    fn extends(&self, class: usize, ancestor: usize) -> bool {
        let mut current = Some(class);
        while let Some(c) = current {
            if c == ancestor {
                return true;
            }
            current = self.classes[c].parent;
        }
        false
    }

    /// The summarized static method that `C.name` resolves to.
    pub(super) fn static_target(&self, class: usize, name: &str) -> Option<usize> {
        let mut current = Some(class);
        while let Some(c) = current {
            if let Some(&(name, _)) = self.classes[c].statics.iter().find(|(n, _)| *n == name) {
                return self.static_targets.get(&(c, name)).copied();
            }
            current = self.classes[c].parent;
        }
        None
    }

    fn target_of(&self, node: Node) -> Target {
        let Some(&function) = self.owners.get(&node) else {
            return Target::Opaque;
        };
        let params: Option<Vec<CellId>> = (0..self.functions[function].parameters.len())
            .map(|position| self.parameter_cell(function, position))
            .collect();
        match params {
            Some(params) => Target::Summary {
                params,
                result: self.functions[function].result,
            },
            None => Target::Opaque,
        }
    }

    /// Flattened method tables of every instance value: the nearest
    /// definition of each name along the class chain, and the nearest
    /// explicit constructor.
    pub(super) fn method_tables(&mut self) {
        for index in 0..self.classes.len() {
            let value = self.classes[index].value;
            // Every constructor of the chain defines its fields as own
            // properties, which shadow prototype members of any class.
            let mut seen = HashSet::<&'static str>::new();
            let mut current = Some(index);
            while let Some(class) = current {
                for &(name, member) in &self.classes[class].members {
                    if member == Member::Field {
                        seen.insert(name);
                    }
                }
                current = self.classes[class].parent;
            }
            let mut exposure = Vec::new();
            let mut constructor = None;
            let mut current = Some(index);
            while let Some(class) = current {
                exposure.push(self.classes[class].this_cell);
                constructor = constructor.or(self.classes[class].constructor);
                // `instance.constructor` reaches the static methods too.
                for &(name, _) in &self.classes[class].statics {
                    if let Some(&function) = self.static_targets.get(&(class, name)) {
                        for position in 0..self.functions[function].parameters.len() {
                            exposure.extend(self.parameter_cell(function, position));
                        }
                        exposure.push(self.functions[function].result);
                    }
                }
                for (name, member) in self.classes[class].members.clone() {
                    if !seen.insert(name) {
                        continue;
                    }
                    if let Member::Method(node) = member {
                        let target = self.target_of(node);
                        if let Target::Summary { params, result } = &target {
                            exposure.extend(params.iter().copied());
                            exposure.push(*result);
                        }
                        self.graph.methods.insert((value, name), target);
                    }
                }
                current = self.classes[class].parent;
            }
            let target = match constructor {
                Some(node) => self.target_of(node),
                None => Target::Summary {
                    params: Vec::new(),
                    result: self.graph.cell(false),
                },
            };
            if let Target::Summary { params, .. } = &target {
                exposure.extend(params.iter().copied());
            }
            self.graph.methods.insert((value, CONSTRUCTOR), target);
            self.graph.exposure.insert(value, exposure);
        }
    }

    /// The class whose instance members bind `this` (and `super`) at
    /// `node`.
    fn enclosing_class(&self, node: Node) -> Option<usize> {
        let container = get_this_container(node, false, false);
        match container.kind() {
            SyntaxKind::MethodDeclaration
            | SyntaxKind::GetAccessor
            | SyntaxKind::SetAccessor
            | SyntaxKind::Constructor
            | SyntaxKind::PropertyDeclaration
                if !is_static(container) =>
            {
                self.class_by_node.get(&container.parent()).copied()
            }
            _ => None,
        }
    }

    /// `x instanceof right` only walks the prototype chain of `x`: `right`
    /// is a closed class, and no class of its chain can define a custom
    /// `Symbol.hasInstance` that would receive `x`.
    pub(super) fn ordinary_instance_check(&self, right: Node) -> bool {
        let right = skip_outer(right);
        let Some(mut current) = (right.kind() == SyntaxKind::Identifier)
            .then(|| self.symbol(right))
            .flatten()
            .and_then(|symbol| self.class_by_symbol.get(&symbol).copied())
            .map(Some)
        else {
            return false;
        };
        while let Some(class) = current {
            if self.classes[class].static_computed {
                return false;
            }
            current = self.classes[class].parent;
        }
        true
    }

    /// A static or private name of an instance field initializer of a closed
    /// class.
    pub(super) fn instance_field(&self, node: Node) -> bool {
        !is_static(node)
            && self.class_by_node.contains_key(&node.parent())
            && matches!(
                node.name().kind(),
                SyntaxKind::Identifier | SyntaxKind::StringLiteral | SyntaxKind::PrivateIdentifier
            )
    }

    pub(super) fn this_cell(&self, node: Node) -> CellId {
        self.enclosing_class(node)
            .map_or(UNKNOWN, |class| self.classes[class].this_cell)
    }

    /// `super` resolves members from the base class.
    pub(super) fn super_cell(&mut self, node: Node) -> CellId {
        match self
            .enclosing_class(node)
            .and_then(|class| self.classes[class].parent)
        {
            Some(parent) => self.graph.holding(self.classes[parent].value),
            None => UNKNOWN,
        }
    }

    /// Field initializers store into every instance of the class chain;
    /// getters store their results, setters load what code stores.
    pub(super) fn member_edges(&mut self, node: Node) {
        if is_static(node) {
            return;
        }
        let Some(&class) = self.class_by_node.get(&node.parent()) else {
            return;
        };
        let name = node.name();
        if !matches!(
            name.kind(),
            SyntaxKind::Identifier | SyntaxKind::StringLiteral | SyntaxKind::PrivateIdentifier
        ) {
            return;
        }
        let key = Key::Name(name.text());
        let this = self.classes[class].this_cell;
        match node.kind() {
            SyntaxKind::PropertyDeclaration => {
                if node.initializer().is_some() {
                    let from = self.expr(node.initializer());
                    self.graph.flow(this, key, from, true);
                }
                if node.type_().is_some() {
                    let cell = self.load(this, key);
                    self.annotate(node.type_(), cell);
                }
            }
            SyntaxKind::GetAccessor => match self.owners.get(&node) {
                Some(&function) => {
                    let result = self.functions[function].result;
                    self.graph.flow(this, key, result, true);
                }
                None => self.graph.flow(this, key, UNKNOWN, true),
            },
            SyntaxKind::SetAccessor => {
                match self
                    .owners
                    .get(&node)
                    .and_then(|&function| self.parameter_cell(function, 0))
                {
                    Some(parameter) => self.graph.flow(this, key, parameter, false),
                    None => {
                        let sink = self.load(this, key);
                        self.sinks.push((sink, "sink:unsummarized-setter"));
                    }
                }
            }
            _ => {}
        }
    }

    fn dispatch_call(&mut self, node: Node, receiver: CellId, name: &'static str) -> CellId {
        let mut spread = false;
        let mut args = Vec::new();
        for argument in node.arguments().iter() {
            if argument.kind() == SyntaxKind::SpreadElement {
                spread = true;
                args.push(UNKNOWN);
            } else {
                args.push(self.expr(argument));
            }
        }
        let result = self.graph.cell(false);
        let site = self.array_site(node, name);
        let array = site
            .as_ref()
            .filter(|site| site.well_formed && self.options.arrays.enabled(site.method))
            .map(|site| site.method);
        self.graph
            .dispatch(receiver, name, args.clone(), spread, result, site);
        self.calls.push(MethodCall {
            receiver,
            name,
            args,
            array,
        });
        self.dispatched.insert(node);
        result
    }

    /// `new C(…)` of a closed class.
    pub(super) fn construction(&mut self, node: Node) -> CellId {
        let callee = skip_outer(node.expression());
        let class = (callee.kind() == SyntaxKind::Identifier)
            .then(|| self.symbol(callee))
            .flatten()
            .and_then(|symbol| self.class_by_symbol.get(&symbol).copied());
        let Some(class) = class else {
            return UNKNOWN;
        };
        let value = self.classes[class].value;
        let receiver = self.graph.holding(value);
        self.dispatch_call(node, receiver, CONSTRUCTOR);
        self.graph.holding(value)
    }

    /// `x.m(…)`, `super.m(…)` and `super(…)`; any other call has an unknown
    /// result.
    pub(super) fn method_call(&mut self, node: Node) -> CellId {
        let callee = skip_outer(node.expression());
        match callee.kind() {
            SyntaxKind::SuperKeyword => {
                let receiver = self.expr(callee);
                if receiver <= EMPTY {
                    return UNKNOWN;
                }
                self.dispatch_call(node, receiver, CONSTRUCTOR);
                self.this_cell(callee)
            }
            SyntaxKind::PropertyAccessExpression
                if matches!(
                    callee.name().kind(),
                    SyntaxKind::Identifier | SyntaxKind::PrivateIdentifier
                ) =>
            {
                let receiver = self.expr(callee.expression());
                if receiver <= EMPTY {
                    self.expose_inline_callback(node);
                    return UNKNOWN;
                }
                self.dispatch_call(node, receiver, callee.name().text())
            }
            SyntaxKind::ElementAccessExpression
                if skip_outer(callee.argument_expression()).kind() == SyntaxKind::StringLiteral =>
            {
                let receiver = self.expr(callee.expression());
                if receiver <= EMPTY {
                    return UNKNOWN;
                }
                let name = skip_outer(callee.argument_expression()).text();
                self.dispatch_call(node, receiver, name)
            }
            _ => UNKNOWN,
        }
    }
}
