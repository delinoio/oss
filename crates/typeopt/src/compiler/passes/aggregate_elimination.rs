// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `aggregate-elimination`: scalar-replaces nonescaping local objects and
//! tuples and collapses module-local namespace objects into direct bindings.
//!
//! Types nominate candidates: a `const` (or never reassigned `let`) binding
//! whose checker type is a fresh object-literal or array/tuple type. Runtime
//! evidence comes from the binding's complete reference set, which is known
//! because the binding is module-local and not exported:
//!
//! - Scalar replacement (`const p = {x: a, y: b}; p.x` becomes `let p_x = a,
//!   p_y = b; p_x`; `const t = [a, b]; t[1]` becomes `let t_0 = a, t_1 = b;
//!   t_1`). Every reference must be a static read or write of an own field
//!   (`p.x`, `p?.x`, `p["x"]`, `t[0]`) or a read of a tuple's `length`. Any
//!   other use (passing, returning, comparing, spreading, enumerating,
//!   `delete`, `typeof` type queries, method calls that would bind `this`)
//!   means identity, layout or the prototype may be observed and preserves the
//!   object. Literals with accessors, methods, spreads, computed or duplicate
//!   keys, `__proto__`, holes or anonymous function values (whose inferred
//!   `.name` would change) are preserved. Initializers keep their order; unused
//!   fields with effects are evaluated in a comma expression ahead of the next
//!   field. When a later initializer may run code, every use must run after the
//!   declaration in the same function, so the earlier initialization of the
//!   scalar bindings cannot be observed through the original binding's temporal
//!   dead zone.
//! - Namespace collapse (`const ns = {f, g: h}; ns.f()` becomes `f()`). The
//!   object is a module-level, unexported declaration whose fields are
//!   module-level immutable bindings (function or class declarations never
//!   assigned, `const` bindings, or imports of such). Every reference must be a
//!   static read of a field. A call through the namespace may only change its
//!   `this` binding when the callee cannot observe it: an arrow function, a
//!   class, or a function declaration whose body never uses `this`. No other
//!   declaration in the module may reuse a field's binding name, the module
//!   must be outside every import cycle and every earlier top-level statement
//!   must be inert, so no code can read the namespace before its
//!   initialization.
//!
//! Bindings referenced from JavaScript, public entry exports, ambient or
//! merged declarations and type annotations stay boundaries.
use std::collections::HashSet;

use anyhow::Result;

use super::{
    super::analysis::{Ctx, RefKind, skip_outer, walk_up_outer},
    aggregates_support::{
        ModuleGraph, fresh_name, identifier_texts, is_anonymous_definition, is_plain_identifier,
        runner, within,
    },
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::AggregateElimination;

pub(in crate::compiler) fn plan(ctx: &mut Ctx, _enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let mut graph = ModuleGraph::default();
    let (mut scalars, mut namespaces, mut skipped) = (0usize, 0usize, 0usize);
    for file in 0..ctx.files.len() {
        crate::app::check_cancelled()?;
        if ctx.files[file].js {
            continue;
        }
        let mut used: Option<HashSet<String>> = None;
        let nodes = ctx.files[file].nodes.clone();
        for node in nodes {
            let Some(site) = Site::nominate(ctx, node) else {
                continue;
            };
            let key = format!(
                "{}:{}:{}",
                ctx.files[file].name,
                site.name.text(),
                Ctx::start(site.declaration)
            );
            if ctx.refs(site.symbol).iter().all(|r| r.node == site.name) {
                // An unused aggregate is residual code, not ours to remove.
                continue;
            }
            let names = used.get_or_insert_with(|| identifier_texts(ctx, file));
            let namespace_reason = if site.namespace_shaped() {
                match namespace(ctx, file, &site, &mut graph) {
                    Ok(edits) => {
                        let mut candidate = Candidate::new(
                            PASS,
                            format!("namespace:{key}"),
                            site.location(ctx, file),
                        );
                        candidate.edits = edits;
                        out.candidates.push(candidate);
                        namespaces += 1;
                        continue;
                    }
                    Err(reason) => Some(reason),
                }
            } else {
                None
            };
            match scalar(ctx, file, &site, names) {
                Ok(edits) => {
                    let mut candidate =
                        Candidate::new(PASS, format!("scalar:{key}"), site.location(ctx, file));
                    candidate.edits = edits;
                    out.candidates.push(candidate);
                    scalars += 1;
                }
                Err(reason) => {
                    let reason = namespace_reason.unwrap_or(reason);
                    out.skips.push(ctx.skip(
                        PASS,
                        format!("aggregate:{key}"),
                        file,
                        site.declaration,
                        reason,
                    ));
                    skipped += 1;
                }
            }
        }
    }
    tracing::debug!(
        pass = %PASS,
        scalars,
        namespaces,
        skipped,
        "aggregate candidates planned"
    );
    Ok(())
}

/// A nominated aggregate binding: `const name = <object or array literal>`.
struct Site {
    declaration: Node,
    name: Node,
    literal: Node,
    symbol: SymbolId,
    array: bool,
}

impl Site {
    fn nominate(ctx: &mut Ctx, node: Node) -> Option<Self> {
        if node.kind() != SyntaxKind::VariableDeclaration
            || node.name().kind() != SyntaxKind::Identifier
            || node.initializer().is_nil()
        {
            return None;
        }
        let mut literal = node.initializer();
        while literal.kind() == SyntaxKind::ParenthesizedExpression {
            literal = literal.expression();
        }
        // Empty literals are containers filled later, never scalar fields.
        let array = match literal.kind() {
            SyntaxKind::ObjectLiteralExpression if !literal.properties().is_empty() => false,
            SyntaxKind::ArrayLiteralExpression if !literal.elements().is_empty() => true,
            _ => return None,
        };
        let list = node.parent();
        if list.kind() != SyntaxKind::VariableDeclarationList
            || list.parent().kind() != SyntaxKind::VariableStatement
            || !list.flags().intersects(NodeFlags::Const | NodeFlags::Let)
            || list.flags().intersects(NodeFlags::Using)
        {
            return None;
        }
        // Types nominate: a fresh object literal or an array/tuple type.
        let ty = ctx
            .checker
            .get_type_at_location(if array { node.name() } else { literal });
        let nominated = if array {
            ctx.checker.is_array_type(ty) || ctx.checker.is_tuple_type(ty)
        } else {
            // The expression's type is the anonymous type of exactly this
            // literal.
            let symbol = ctx.checker.ty(ty).symbol;
            ctx.checker.ty(ty).flags().intersects(TypeFlags::Object)
                && symbol.is_some()
                && ctx
                    .checker
                    .sym(symbol)
                    .flags
                    .intersects(SymbolFlags::ObjectLiteral)
                && ctx.checker.sym(symbol).declarations.first() == Some(&literal)
        };
        if !nominated {
            return None;
        }
        let symbol = ctx.resolve(node.name());
        if symbol.is_nil() {
            return None;
        }
        Some(Self {
            declaration: node,
            name: node.name(),
            literal,
            symbol,
            array,
        })
    }

    fn statement(&self) -> Node {
        self.declaration.parent().parent()
    }

    fn location(&self, ctx: &Ctx, file: usize) -> crate::plan::Location {
        ctx.location(file, self.declaration)
    }

    /// A module-level object whose every field value is a bare identifier.
    fn namespace_shaped(&self) -> bool {
        !self.array
            && self.statement().parent().kind() == SyntaxKind::SourceFile
            && !self.literal.properties().is_empty()
            && self.literal.properties().iter().all(|p| match p.kind() {
                SyntaxKind::ShorthandPropertyAssignment => true,
                SyntaxKind::PropertyAssignment => p.initializer().kind() == SyntaxKind::Identifier,
                _ => false,
            })
    }

    /// Obligations shared by both transformations: the binding is internal,
    /// not exported and declared once.
    fn check_binding(&self, ctx: &mut Ctx) -> Result<(), Reason> {
        let statement = self.statement();
        if ctx.public.contains(&self.symbol) {
            return Err(Reason::PublicExport);
        }
        if has_syntactic_modifier(statement, ModifierFlags::Export) {
            // Internal exports are read through other modules' bindings.
            return Err(Reason::Escapes);
        }
        if statement.flags().intersects(NodeFlags::Ambient)
            || has_syntactic_modifier(statement, ModifierFlags::Ambient)
        {
            return Err(Reason::DeclarationMerging);
        }
        if !ctx.is_internal(self.symbol) {
            return Err(Reason::ExternalBoundary);
        }
        // Top-level bindings of a script are global and visible to code
        // outside the analyzed program.
        if statement.parent().kind() == SyntaxKind::SourceFile
            && !is_external_module(statement.parent())
        {
            return Err(Reason::ExternalBoundary);
        }
        if ctx.checker.sym(self.symbol).declarations.len() != 1 {
            return Err(Reason::DeclarationMerging);
        }
        Ok(())
    }
}

/// One field of a scalar-replaced aggregate.
struct Field {
    key: String,
    /// Initializer expression (or the shorthand identifier).
    value: Node,
    pure: bool,
    arrow: bool,
    used: bool,
    binding: String,
}

/// How a reference uses the aggregate.
enum Use {
    Field(usize),
    Length,
}

fn object_fields(ctx: &mut Ctx, literal: Node) -> Result<Vec<Field>, Reason> {
    let mut fields: Vec<Field> = Vec::new();
    for property in literal.properties().iter() {
        let (key, value) = match property.kind() {
            SyntaxKind::PropertyAssignment => {
                let name = property.name();
                if name.kind() == SyntaxKind::ComputedPropertyName {
                    return Err(Reason::DynamicAccess);
                }
                if name.kind() != SyntaxKind::Identifier {
                    return Err(Reason::UnsupportedSyntax);
                }
                if is_anonymous_definition(property.initializer()) {
                    return Err(Reason::ObservableName);
                }
                (name.text().to_owned(), property.initializer())
            }
            SyntaxKind::ShorthandPropertyAssignment => {
                if property.object_assignment_initializer().is_some() {
                    return Err(Reason::UnsupportedSyntax);
                }
                (property.name().text().to_owned(), property.name())
            }
            SyntaxKind::GetAccessor | SyntaxKind::SetAccessor => return Err(Reason::Accessor),
            SyntaxKind::MethodDeclaration => return Err(Reason::ObservableOperation),
            SyntaxKind::SpreadAssignment => return Err(Reason::Reflection),
            _ => return Err(Reason::UnsupportedSyntax),
        };
        if key == "__proto__" {
            return Err(Reason::PrototypeMutation);
        }
        if !is_plain_identifier(&key) {
            return Err(Reason::UnsupportedSyntax);
        }
        if fields.iter().any(|f| f.key == key) {
            return Err(Reason::UnsupportedSyntax);
        }
        let pure = ctx.is_pure(value);
        fields.push(Field {
            key,
            value,
            pure,
            arrow: false,
            used: false,
            binding: String::new(),
        });
    }
    Ok(fields)
}

fn array_fields(ctx: &mut Ctx, literal: Node) -> Result<Vec<Field>, Reason> {
    let mut fields = Vec::new();
    for (index, element) in literal.elements().iter().enumerate() {
        match element.kind() {
            SyntaxKind::SpreadElement => return Err(Reason::CustomIterator),
            SyntaxKind::OmittedExpression => return Err(Reason::SparseArray),
            _ => {}
        }
        let pure = ctx.is_pure(element);
        fields.push(Field {
            key: index.to_string(),
            value: element,
            pure,
            arrow: skip_outer(element).kind() == SyntaxKind::ArrowFunction,
            used: false,
            binding: String::new(),
        });
    }
    Ok(fields)
}

/// Classifies the access `parent` made through a reference to the
/// aggregate.
fn classify(site: &Site, fields: &[Field], reference: Node) -> Result<(Node, Use), Reason> {
    let parent = reference.parent();
    match parent.kind() {
        SyntaxKind::PropertyAccessExpression if parent.expression() == reference => {
            let key = parent.name().text();
            if site.array {
                return if key == "length" {
                    Ok((parent, Use::Length))
                } else {
                    Err(Reason::ObservableOperation)
                };
            }
            fields
                .iter()
                .position(|f| f.key == key)
                .map(|i| (parent, Use::Field(i)))
                .ok_or(Reason::ObservableOperation)
        }
        SyntaxKind::ElementAccessExpression if parent.expression() == reference => {
            let argument = skip_outer(parent.argument_expression());
            let key = match argument.kind() {
                SyntaxKind::NumericLiteral if site.array => {
                    let text = argument.text();
                    // Only canonical integer spellings name an index.
                    match text.parse::<usize>() {
                        Ok(index) if index.to_string() == text => index.to_string(),
                        _ => return Err(Reason::DynamicAccess),
                    }
                }
                SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
                    if !site.array =>
                {
                    argument.text().to_owned()
                }
                _ => return Err(Reason::DynamicAccess),
            };
            fields
                .iter()
                .position(|f| f.key == key)
                .map(|i| (parent, Use::Field(i)))
                .ok_or(Reason::ObservableOperation)
        }
        SyntaxKind::BinaryExpression
            if matches!(
                parent.operator_token().kind(),
                SyntaxKind::EqualsEqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsEqualsToken
                    | SyntaxKind::EqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsToken
            ) =>
        {
            Err(Reason::ObservableIdentity)
        }
        SyntaxKind::SpreadElement | SyntaxKind::SpreadAssignment | SyntaxKind::ForInStatement => {
            Err(Reason::Reflection)
        }
        _ => Err(Reason::Escapes),
    }
}

/// Whether `access` is the callee of a call or tagged template, which would
/// pass the aggregate as `this`.
fn is_callee(access: Node) -> bool {
    let outer = walk_up_outer(access);
    let holder = outer.parent();
    (holder.kind() == SyntaxKind::CallExpression && holder.expression() == outer)
        || (holder.kind() == SyntaxKind::TaggedTemplateExpression && holder.tag() == outer)
}

fn is_deleted(access: Node) -> bool {
    walk_up_outer(access).parent().kind() == SyntaxKind::DeleteExpression
}

fn scalar(
    ctx: &mut Ctx,
    file: usize,
    site: &Site,
    used_names: &mut HashSet<String>,
) -> Result<Vec<FileEdit>, Reason> {
    site.check_binding(ctx)?;
    let mut fields = if site.array {
        array_fields(ctx, site.literal)?
    } else {
        object_fields(ctx, site.literal)?
    };
    if fields.is_empty() {
        // An empty aggregate is a container that is filled later.
        return Err(Reason::Mutation);
    }
    // A later initializer that may run code could observe scalar bindings
    // that are initialized before the original binding would be.
    let guarded = fields.iter().skip(1).any(|f| !f.pure);
    let home = runner(site.declaration);
    let mut accesses = Vec::new();
    for reference in ctx.refs(site.symbol).to_vec() {
        if reference.node == site.name {
            continue;
        }
        match reference.kind {
            RefKind::Type => return Err(Reason::TypeConstraint),
            RefKind::Alias => return Err(Reason::Escapes),
            RefKind::Write => return Err(Reason::Mutation),
            RefKind::Declaration => return Err(Reason::DeclarationMerging),
            RefKind::Call | RefKind::New | RefKind::Read => {}
        }
        if reference.file != file {
            return Err(Reason::Escapes);
        }
        if within(reference.node, site.literal) {
            return Err(Reason::SideEffects);
        }
        if guarded
            && (runner(reference.node) != home
                || Ctx::start(reference.node) < site.declaration.end() as usize)
        {
            return Err(Reason::SideEffects);
        }
        let (access, usage) = classify(site, &fields, reference.node)?;
        if is_deleted(access) {
            return Err(Reason::ObservableOperation);
        }
        let write = is_assignment_target(access);
        match usage {
            Use::Length if write || is_callee(access) => return Err(Reason::Mutation),
            Use::Field(index) if is_callee(access) && !fields[index].arrow => {
                // `p.f()` passes `p` as `this`; `p_f()` would not.
                return Err(Reason::ObservableOperation);
            }
            Use::Field(index) => fields[index].used = true,
            Use::Length => {}
        }
        accesses.push((access, usage));
    }
    if accesses.is_empty() {
        return Err(Reason::UnsupportedSyntax);
    }
    let list = site.declaration.parent();
    if list.declarations().nodes().len() != 1 {
        // A shared declaration list would force one binding kind on all.
        return Err(Reason::UnsupportedSyntax);
    }
    let mut typings = Vec::with_capacity(fields.len());
    for (index, field) in fields.iter().enumerate() {
        typings.push(if field.used {
            field_typing(ctx, file, site, index, field)?
        } else {
            Typing::Plain
        });
    }
    // Unused fields with effects run, in order, ahead of the next used field.
    let mut pending: Vec<String> = Vec::new();
    let mut declarators: Vec<String> = Vec::new();
    let base = site.name.text().to_owned();
    for (field, typing) in fields.iter_mut().zip(typings) {
        let text = ctx.text(file, field.value).to_owned();
        if !field.used {
            if field.pure {
                continue;
            }
            if !ctx.checker.is_side_effect_free(field.value) {
                pending.push(text);
                continue;
            }
            // The checker rejects syntactically effect-free operands on the
            // left of a comma, although they may still run user code (for
            // example `valueOf` through `+`). They keep their own binding.
            if ctx.checker.compiler_options.no_unused_locals.is_true() {
                return Err(Reason::SideEffects);
            }
            let binding = fresh_name(&field_names(&base, &field.key), used_names);
            let value = if pending.is_empty() {
                text
            } else {
                let mut parts = std::mem::take(&mut pending);
                parts.push(text);
                format!("({})", parts.join(", "))
            };
            declarators.push(format!("{binding} = {value}"));
            continue;
        }
        field.binding = fresh_name(&field_names(&base, &field.key), used_names);
        let (value, tight) = if pending.is_empty() {
            (text, tight(field.value))
        } else {
            let mut parts = std::mem::take(&mut pending);
            parts.push(text);
            (format!("({})", parts.join(", ")), true)
        };
        declarators.push(match typing {
            Typing::Plain => format!("{} = {value}", field.binding),
            Typing::Annotate(ty) => format!("{}: {ty} = {value}", field.binding),
            Typing::Assert(ty) if tight => format!("{} = {value} as {ty}", field.binding),
            Typing::Assert(ty) => format!("{} = ({value}) as {ty}", field.binding),
        });
    }
    if !pending.is_empty() {
        // Trailing effects keep a binding of their own, which is only valid
        // when unused locals are not type errors.
        if ctx.checker.compiler_options.no_unused_locals.is_true() {
            return Err(Reason::SideEffects);
        }
        let binding = fresh_name(&[format!("{base}_")], used_names);
        let value = if pending.len() == 1 {
            pending.remove(0)
        } else {
            format!("({})", pending.join(", "))
        };
        declarators.push(format!("{binding} = {value}"));
    }
    let path = ctx.files[file].name.clone();
    let length = fields.len().to_string();
    if declarators.is_empty() {
        // Every field is unused and pure (for example only `t.length` is
        // read): the whole declaration goes. An empty declaration list is
        // not valid syntax, so only a standalone statement qualifies.
        let statement = list.parent();
        if statement.kind() != SyntaxKind::VariableStatement {
            return Err(Reason::UnsupportedSyntax);
        }
        let mut edits = vec![FileEdit::delete(
            &path,
            Ctx::start(statement),
            statement.end() as usize,
        )];
        for (access, usage) in accesses {
            let Use::Length = usage else {
                return Err(Reason::UnsupportedSyntax);
            };
            edits.push(FileEdit::replace(
                &path,
                Ctx::start(access),
                access.end() as usize,
                length.clone(),
            ));
        }
        return Ok(edits);
    }
    let mut edits = vec![FileEdit::replace(
        &path,
        Ctx::start(site.declaration),
        site.declaration.end() as usize,
        declarators.join(", "),
    )];
    if list.flags().intersects(NodeFlags::Const) {
        // Object properties and array elements widen literal types; `let`
        // keeps the same widening for the scalar bindings.
        let start = Ctx::start(list);
        if !ctx.files[file].text[start..].starts_with("const") {
            return Err(Reason::UnsupportedSyntax);
        }
        edits.push(FileEdit::replace(&path, start, start + 5, "let"));
    }
    for (access, usage) in accesses {
        let replacement = match usage {
            Use::Field(index) => fields[index].binding.clone(),
            Use::Length => length.clone(),
        };
        edits.push(FileEdit::replace(
            &path,
            Ctx::start(access),
            access.end() as usize,
            replacement,
        ));
    }
    Ok(edits)
}

/// Binding names for a field. Combining the aggregate and field names keeps
/// the name distinct from bindings that other transformations of the same
/// round may introduce, which only check freshness against the same text.
fn field_names(base: &str, key: &str) -> Vec<String> {
    vec![format!("{base}_{key}")]
}

/// How a scalar binding keeps the type its field had.
enum Typing {
    /// The initializer's own widened type is the field type.
    Plain,
    /// An annotation names the field type; it contextually types the
    /// initializer like the original annotation did.
    Annotate(String),
    /// The initializer is narrower than the field type, which a declaration
    /// would narrow to; an assertion keeps the declared field type.
    Assert(String),
}

/// The type the original program gives field `index`, and the spelling that
/// keeps it for the scalar binding.
fn field_typing(
    ctx: &mut Ctx,
    file: usize,
    site: &Site,
    index: usize,
    field: &Field,
) -> Result<Typing, Reason> {
    let annotation = site.declaration.type_();
    let (aggregate, spelled) = if annotation.is_some() {
        let declared = ctx.checker.get_type_from_type_node(annotation);
        if ctx
            .checker
            .ty(declared)
            .flags()
            .intersects(TypeFlags::Union)
        {
            // An indexed access on a union needs the key in every member.
            return Err(Reason::TypeConstraint);
        }
        let key = if site.array {
            index.to_string()
        } else {
            format!("\"{}\"", field.key)
        };
        (
            declared,
            Some(format!("({})[{key}]", ctx.text(file, annotation))),
        )
    } else {
        (ctx.checker.get_type_at_location(site.name), None)
    };
    let field_type = if site.array {
        if ctx.checker.is_tuple_type(aggregate) {
            let elements = ctx.checker.get_type_arguments(aggregate);
            elements.get(index).copied().unwrap_or(TypeId::NIL)
        } else if ctx.checker.is_array_type(aggregate) {
            ctx.checker.get_element_type_of_array_type(aggregate)
        } else {
            TypeId::NIL
        }
    } else {
        let property = ctx.checker.get_property_of_type(aggregate, &field.key);
        if property.is_nil() {
            TypeId::NIL
        } else {
            ctx.checker.get_type_of_symbol(property)
        }
    };
    if field_type.is_nil() {
        return Err(Reason::TypeConstraint);
    }
    let value = skip_outer(field.value);
    let initial = ctx.checker.get_type_at_location(field.value);
    let flags = ctx.checker.ty(field_type).flags();
    // `let x = null`, `undefined` and `[]` declare auto-typed bindings, and a
    // narrower initializer narrows a union-typed binding.
    let auto_typed = (value.kind() == SyntaxKind::ArrayLiteralExpression
        && value.elements().is_empty())
        || value.kind() == SyntaxKind::NullKeyword
        || (value.kind() == SyntaxKind::Identifier && value.text() == "undefined");
    let narrows = flags.intersects(TypeFlags::Union) && initial != field_type;
    if !auto_typed && !narrows {
        return Ok(spelled.map_or(Typing::Plain, Typing::Annotate));
    }
    if let Some(spelled) = spelled {
        return Ok(Typing::Assert(spelled));
    }
    if !printable(ctx, field_type) {
        return Err(Reason::TypeConstraint);
    }
    Ok(Typing::Assert(ctx.checker.type_to_string(field_type)))
}

/// Whether the type consists only of primitives, primitive literals and
/// arrays of them, whose printed form needs no scope.
fn printable(ctx: &mut Ctx, ty: TypeId) -> bool {
    let flags = ctx.checker.ty(ty).flags();
    if flags.intersects(TypeFlags::Union) {
        let members = ctx.checker.ty(ty).types_list();
        return members.iter().all(|&member| printable(ctx, member));
    }
    if flags.intersects(TypeFlags::EnumLike) {
        return false;
    }
    if flags.intersects(
        TypeFlags::String
            | TypeFlags::Number
            | TypeFlags::BigInt
            | TypeFlags::Boolean
            | TypeFlags::StringLiteral
            | TypeFlags::NumberLiteral
            | TypeFlags::BigIntLiteral
            | TypeFlags::BooleanLiteral
            | TypeFlags::Null
            | TypeFlags::Undefined
            | TypeFlags::Void
            | TypeFlags::Never,
    ) {
        return true;
    }
    if ctx.checker.is_array_type(ty) {
        let element = ctx.checker.get_element_type_of_array_type(ty);
        return printable(ctx, element);
    }
    false
}

/// Whether `as T` can follow the expression without parentheses.
fn tight(node: Node) -> bool {
    matches!(
        node.kind(),
        SyntaxKind::Identifier
            | SyntaxKind::NumericLiteral
            | SyntaxKind::StringLiteral
            | SyntaxKind::BigIntLiteral
            | SyntaxKind::NoSubstitutionTemplateLiteral
            | SyntaxKind::TemplateExpression
            | SyntaxKind::TrueKeyword
            | SyntaxKind::FalseKeyword
            | SyntaxKind::NullKeyword
            | SyntaxKind::ThisKeyword
            | SyntaxKind::CallExpression
            | SyntaxKind::PropertyAccessExpression
            | SyntaxKind::ElementAccessExpression
            | SyntaxKind::ObjectLiteralExpression
            | SyntaxKind::ArrayLiteralExpression
            | SyntaxKind::ParenthesizedExpression
            | SyntaxKind::NonNullExpression
    )
}

/// Whether a function body may observe its `this` binding. Nested
/// non-arrow functions and classes have their own `this`.
fn uses_this(node: Node) -> bool {
    let mut found = false;
    node.for_each_child(|child| {
        found = match child.kind() {
            SyntaxKind::ThisKeyword | SyntaxKind::SuperKeyword => true,
            SyntaxKind::FunctionDeclaration
            | SyntaxKind::FunctionExpression
            | SyntaxKind::ClassDeclaration
            | SyntaxKind::ClassExpression => false,
            _ => uses_this(child),
        };
        found
    });
    found
}

/// Whether calling the value bound to `target` with another `this` is
/// unobservable.
fn this_insensitive(ctx: &mut Ctx, target: SymbolId) -> bool {
    let flags = ctx.checker.sym(target).flags;
    let declarations = ctx.checker.sym(target).declarations.to_vec();
    if declarations.is_empty() || declarations.iter().any(|&d| ctx.file_of_node(d).is_none()) {
        return false;
    }
    if flags.intersects(SymbolFlags::Class) {
        // Calling a class constructor throws regardless of `this`.
        return true;
    }
    if flags.intersects(SymbolFlags::Function) {
        return declarations.iter().all(|&d| {
            d.kind() == SyntaxKind::FunctionDeclaration
                && (d.body().is_nil() || !uses_this(d.body()))
        });
    }
    declarations.iter().all(|&d| {
        d.kind() == SyntaxKind::VariableDeclaration
            && d.initializer().is_some()
            && skip_outer(d.initializer()).kind() == SyntaxKind::ArrowFunction
    })
}

/// Whether a top-level statement runs no user code when evaluated.
fn inert(ctx: &mut Ctx, statement: Node) -> bool {
    if statement.flags().intersects(NodeFlags::Ambient)
        || has_syntactic_modifier(statement, ModifierFlags::Ambient)
    {
        return true;
    }
    match statement.kind() {
        SyntaxKind::ImportDeclaration
        | SyntaxKind::ExportDeclaration
        | SyntaxKind::InterfaceDeclaration
        | SyntaxKind::TypeAliasDeclaration
        | SyntaxKind::FunctionDeclaration
        | SyntaxKind::EmptyStatement => true,
        SyntaxKind::ClassDeclaration => {
            statement.decorators().is_empty()
                && statement.heritage_clauses().nodes().iter().all(|clause| {
                    clause.token() == SyntaxKind::ImplementsKeyword
                        || clause
                            .types()
                            .nodes()
                            .iter()
                            .all(|t| skip_outer(t.expression()).kind() == SyntaxKind::Identifier)
                })
                && statement
                    .members()
                    .iter()
                    .collect::<Vec<_>>()
                    .into_iter()
                    .all(|member| {
                        member.decorators().is_empty()
                            && (member.name().is_nil()
                                || member.name().kind() != SyntaxKind::ComputedPropertyName)
                            && match member.kind() {
                                SyntaxKind::ClassStaticBlockDeclaration => false,
                                SyntaxKind::PropertyDeclaration if is_static(member) => {
                                    member.initializer().is_nil()
                                        || ctx.is_pure(member.initializer())
                                }
                                _ => true,
                            }
                    })
        }
        SyntaxKind::VariableStatement => statement
            .declaration_list()
            .declarations()
            .nodes()
            .iter()
            .collect::<Vec<_>>()
            .into_iter()
            .all(|d| {
                d.name().kind() == SyntaxKind::Identifier
                    && (d.initializer().is_nil() || ctx.is_pure(d.initializer()))
            }),
        _ => false,
    }
}

/// A field value binding of a namespace object.
struct Value {
    key: String,
    /// The identifier spelling that names the binding in this module.
    local: String,
    target: SymbolId,
    used: bool,
}

fn namespace(
    ctx: &mut Ctx,
    file: usize,
    site: &Site,
    graph: &mut ModuleGraph,
) -> Result<Vec<FileEdit>, Reason> {
    site.check_binding(ctx)?;
    let statement = site.statement();
    if site.declaration.type_().is_some() {
        return Err(Reason::TypeConstraint);
    }
    if site.declaration.parent().declarations().nodes().len() != 1 {
        return Err(Reason::UnsupportedSyntax);
    }
    let mut values: Vec<Value> = Vec::new();
    for property in site.literal.properties().iter() {
        let (key, identifier) = if property.kind() == SyntaxKind::ShorthandPropertyAssignment {
            if property.object_assignment_initializer().is_some() {
                return Err(Reason::UnsupportedSyntax);
            }
            (property.name().text().to_owned(), property.name())
        } else {
            if property.name().kind() != SyntaxKind::Identifier {
                return Err(Reason::UnsupportedSyntax);
            }
            (property.name().text().to_owned(), property.initializer())
        };
        if key == "__proto__" {
            return Err(Reason::PrototypeMutation);
        }
        if values.iter().any(|v| v.key == key) {
            return Err(Reason::UnsupportedSyntax);
        }
        let local = identifier.text().to_owned();
        // The binding this module sees must be a module-level declaration
        // or import, never a global that could be replaced.
        let local_symbol = if property.kind() == SyntaxKind::ShorthandPropertyAssignment {
            ctx.checker.get_shorthand_assignment_value_symbol(property)
        } else {
            ctx.checker.get_symbol_at_location(identifier)
        };
        if local_symbol.is_nil() {
            return Err(Reason::UnsupportedSyntax);
        }
        let local_declarations = ctx.checker.sym(local_symbol).declarations.to_vec();
        if local_declarations.is_empty()
            || !local_declarations
                .iter()
                .all(|&d| get_source_file_of_node(d) == ctx.files[file].node && module_level(d))
        {
            return Err(Reason::ExternalBoundary);
        }
        let target = ctx.skip_alias(local_symbol);
        if target.is_nil() || ctx.checker.is_unknown_symbol(target) {
            return Err(Reason::ExternalBoundary);
        }
        immutable(ctx, target)?;
        // No other declaration in the module may shadow the binding at a
        // use site.
        let shadowed = ctx.files[file].nodes.clone().into_iter().any(|n| {
            n.kind() == SyntaxKind::Identifier
                && n.text() == local
                && is_declaration_name(n)
                && !local_declarations.contains(&n.parent())
                && n.parent().kind() != SyntaxKind::ImportSpecifier
                && n.parent().kind() != SyntaxKind::ImportClause
                && !(n.parent().kind() == SyntaxKind::PropertyAssignment
                    || n.parent().kind() == SyntaxKind::PropertySignature
                    || n.parent().kind() == SyntaxKind::PropertyDeclaration
                    || n.parent().kind() == SyntaxKind::MethodDeclaration
                    || n.parent().kind() == SyntaxKind::MethodSignature
                    || n.parent().kind() == SyntaxKind::GetAccessor
                    || n.parent().kind() == SyntaxKind::SetAccessor
                    || n.parent().kind() == SyntaxKind::ShorthandPropertyAssignment
                    || n.parent().kind() == SyntaxKind::EnumMember)
        });
        if shadowed {
            return Err(Reason::UnsupportedSyntax);
        }
        values.push(Value {
            key,
            local,
            target,
            used: false,
        });
    }
    let mut accesses = Vec::new();
    for reference in ctx.refs(site.symbol).to_vec() {
        if reference.node == site.name {
            continue;
        }
        match reference.kind {
            RefKind::Type => return Err(Reason::TypeConstraint),
            RefKind::Write => return Err(Reason::Mutation),
            RefKind::Alias | RefKind::Declaration => return Err(Reason::Escapes),
            RefKind::Call | RefKind::New | RefKind::Read => {}
        }
        if reference.file != file || within(reference.node, site.literal) {
            return Err(Reason::Escapes);
        }
        let parent = reference.node.parent();
        let key = match parent.kind() {
            SyntaxKind::PropertyAccessExpression if parent.expression() == reference.node => {
                parent.name().text().to_owned()
            }
            SyntaxKind::ElementAccessExpression if parent.expression() == reference.node => {
                let argument = skip_outer(parent.argument_expression());
                if !matches!(
                    argument.kind(),
                    SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
                ) {
                    return Err(Reason::DynamicAccess);
                }
                argument.text().to_owned()
            }
            _ => return Err(Reason::Escapes),
        };
        let Some(index) = values.iter().position(|v| v.key == key) else {
            return Err(Reason::ObservableOperation);
        };
        if is_assignment_target(parent) {
            return Err(Reason::Mutation);
        }
        if is_deleted(parent) {
            return Err(Reason::ObservableOperation);
        }
        if is_callee(parent) && !this_insensitive(ctx, values[index].target) {
            return Err(Reason::ObservableOperation);
        }
        values[index].used = true;
        accesses.push((parent, index));
    }
    // A field never read through the namespace would leave its value
    // binding without a use when the object disappears.
    for value in &values {
        if !value.used && local_only(ctx, value.target, file) {
            return Err(Reason::Referenced);
        }
    }
    // No code may read the namespace before it is initialized: no import
    // cycle reaches this module and nothing before it runs user code.
    if graph.in_cycle(ctx, file) {
        return Err(Reason::ModuleInitialization);
    }
    let statements: Vec<Node> = ctx.files[file].node.statements().iter().collect();
    for earlier in statements {
        if earlier == statement {
            break;
        }
        if !inert(ctx, earlier) {
            return Err(Reason::ModuleInitialization);
        }
    }
    let path = ctx.files[file].name.clone();
    let mut edits = vec![FileEdit::delete(
        &path,
        Ctx::start(statement),
        statement.end() as usize,
    )];
    for (access, index) in accesses {
        edits.push(FileEdit::replace(
            &path,
            Ctx::start(access),
            access.end() as usize,
            values[index].local.clone(),
        ));
    }
    Ok(edits)
}

/// Whether a declaration is a module-level binding (or an import).
fn module_level(declaration: Node) -> bool {
    match declaration.kind() {
        SyntaxKind::ImportSpecifier | SyntaxKind::ImportClause | SyntaxKind::NamespaceImport => {
            true
        }
        SyntaxKind::VariableDeclaration => {
            let list = declaration.parent();
            list.parent().kind() == SyntaxKind::VariableStatement
                && list.parent().parent().kind() == SyntaxKind::SourceFile
        }
        SyntaxKind::FunctionDeclaration | SyntaxKind::ClassDeclaration => {
            declaration.parent().kind() == SyntaxKind::SourceFile
        }
        _ => false,
    }
}

/// The value bound to `target` can never change after initialization.
fn immutable(ctx: &mut Ctx, target: SymbolId) -> Result<(), Reason> {
    let flags = ctx.checker.sym(target).flags;
    let declarations = ctx.checker.sym(target).declarations.to_vec();
    if declarations.is_empty() || declarations.iter().any(|&d| ctx.file_of_node(d).is_none()) {
        return Err(Reason::ExternalBoundary);
    }
    if ctx.refs(target).iter().any(|r| r.kind == RefKind::Write) {
        return Err(Reason::Mutation);
    }
    if ctx.js_referenced.contains(&target) {
        return Err(Reason::ExternalBoundary);
    }
    if flags.intersects(SymbolFlags::Function | SymbolFlags::Class) {
        if declarations.iter().all(|&d| {
            matches!(
                d.kind(),
                SyntaxKind::FunctionDeclaration | SyntaxKind::ClassDeclaration
            )
        }) {
            return Ok(());
        }
        return Err(Reason::DeclarationMerging);
    }
    if declarations.len() == 1
        && declarations[0].kind() == SyntaxKind::VariableDeclaration
        && declarations[0]
            .parent()
            .flags()
            .intersects(NodeFlags::Const)
    {
        return Ok(());
    }
    Err(Reason::Mutation)
}

/// The binding is declared in `file`, unexported, and the namespace literal
/// is its only runtime use.
fn local_only(ctx: &mut Ctx, target: SymbolId, file: usize) -> bool {
    let declarations = ctx.checker.sym(target).declarations.to_vec();
    let local = declarations.iter().all(|&d| {
        get_source_file_of_node(d) == ctx.files[file].node
            && !get_combined_modifier_flags(d).intersects(ModifierFlags::Export)
    });
    local
        && ctx
            .refs(target)
            .iter()
            .filter(|r| !matches!(r.kind, RefKind::Declaration | RefKind::Type))
            .count()
            <= 1
}
