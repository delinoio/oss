// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `discriminant-compaction`: replaces the string literals of a closed
//! discriminated union's tag property with small numbers, consistently in
//! the type declarations, every producer and every consumer.
//!
//! Types nominate a domain: a union type alias in application TypeScript
//! whose object members (inline type literals, interfaces or aliases of type
//! literals) each declare the same required property with a string literal
//! type. Unions sharing members are one domain.
//!
//! Runtime evidence comes from a closed-world flow check over every module
//! that can hold a domain value (modules that name a domain type, read the
//! tag, export a value whose type carries a domain member, or import such a
//! value). A type "carries" the domain when a member, a tag declaration, or
//! a type argument, property, signature or index type of an application or
//! generic library type does. In those modules:
//!
//! - every value whose type carries the domain may only be compared, tested,
//!   read through static properties, iterated as a built-in collection, or flow
//!   into a slot whose declared type also carries the domain: application
//!   variables, parameters, properties and returns, or the instantiated
//!   parameters of built-in `Array`, `Map`, `Set`, `WeakMap`, `WeakSet` and
//!   `Promise` operations. Flows into `any`, `unknown`, `object`, structural
//!   look-alikes, generic application functions, other library calls
//!   (`JSON.stringify`, `console.log`, `Object.keys`, ...), JSX, templates,
//!   dynamic keys, `for...in`, `throw`, `yield` and type predicates that forget
//!   the domain preserve the tags;
//! - every value that enters a domain slot either already carries the domain or
//!   is a fresh object literal whose contextual tag property is the domain's,
//!   an array literal, a primitive, or a function whose parameters keep the
//!   domain. `any` values (`JSON.parse`), structural look-alikes and generic
//!   results are external producers and preserve the tags;
//! - every read of the tag property is a strict or loose (in)equality with one
//!   of the domain's literals or another tag of the same domain, a `switch`
//!   whose cases are domain literals, or a copy into another producer. Any
//!   other use (variables, arguments, keys, templates, destructuring) observes
//!   the representation and preserves the tags;
//! - producers set the tag to a domain literal, a conditional of domain
//!   literals or another tag of the same domain.
//!
//! Public entry exports and JavaScript references of carrying values or
//! domain types, other literal types spelling a domain value, indexed access
//! types of the tag, declaration merging and special protocol properties
//! (`toString`, `toJSON`, `then`, ...) preserve the domain. Codes are
//! assigned in member order, so the result is deterministic.
use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet};

use anyhow::Result;

use super::super::analysis::{Ctx, skip_outer};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::DiscriminantCompaction;

/// Members that protocols or the runtime look up by name.
const SPECIAL: &[&str] = &[
    "toString",
    "valueOf",
    "toJSON",
    "toLocaleString",
    "then",
    "constructor",
    "__proto__",
];

/// Built-in collection types whose operations store and return elements
/// without inspecting or serializing them.
const COLLECTIONS: &[&str] = &[
    "Array",
    "ReadonlyArray",
    "Map",
    "ReadonlyMap",
    "Set",
    "ReadonlySet",
    "WeakMap",
    "WeakSet",
    "Promise",
    "PromiseLike",
    "Iterable",
    "IterableIterator",
    "Iterator",
    "ArrayIterator",
    "MapIterator",
    "SetIterator",
];

/// Constructors of built-in collections.
const COLLECTION_CONSTRUCTORS: &[&str] = &["Array", "Map", "Set", "WeakMap", "WeakSet", "Promise"];

pub(in crate::compiler) fn plan(ctx: &mut Ctx, _enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let domains = nominate(ctx);
    if domains.is_empty() {
        return Ok(());
    }
    let tags: HashSet<Node> = domains
        .iter()
        .flat_map(|d| d.members.iter().map(|m| m.literal))
        .collect();
    let index = TypeIndex::build(ctx);
    let (mut proposed, mut skipped) = (0usize, 0usize);
    for domain in &domains {
        crate::app::check_cancelled()?;
        let file = domain.file;
        let key = format!(
            "discriminant:{}:{}.{}",
            ctx.files[file].name,
            domain.alias.name().text(),
            domain.key
        );
        let mut proof = Proof::new(domain);
        match proof.run(ctx, &tags, &index) {
            Ok(edits) => {
                let mut candidate = Candidate::new(PASS, key, ctx.location(file, domain.alias));
                candidate.edits = edits;
                out.candidates.push(candidate);
                proposed += 1;
            }
            Err(reason) => {
                out.skips
                    .push(ctx.skip(PASS, key, file, domain.alias, reason));
                skipped += 1;
            }
        }
        tracing::debug!(
            pass = %PASS,
            modules = proof.scope.len(),
            types = proof.memo.len(),
            "discriminant domain analyzed"
        );
    }
    tracing::debug!(
        pass = %PASS,
        domains = domains.len(),
        proposed,
        skipped,
        "discriminant domains planned"
    );
    Ok(())
}

/// One union member's tag declaration.
struct Member {
    /// The interface declaration or type literal node.
    node: Node,
    /// The tag's property signature.
    signature: Node,
    /// The string literal inside the tag's literal type.
    literal: Node,
    value: String,
}

/// A nominated discriminant domain.
struct Domain {
    file: usize,
    /// The first union type alias that nominated the domain.
    alias: Node,
    key: String,
    members: Vec<Member>,
    /// Type symbols that name domain members or unions of them.
    symbols: Vec<SymbolId>,
    /// Distinct tag values in member order; the code of a value is its
    /// position.
    values: Vec<String>,
}

impl Domain {
    fn code(&self, value: &str) -> Option<usize> {
        self.values.iter().position(|v| v == value)
    }
}

/// The object members of a union type alias, or `None` when a constituent
/// is not a supported member.
fn union_members(ctx: &mut Ctx, alias: Node) -> Option<(Vec<Node>, Vec<SymbolId>)> {
    let mut ty = alias.type_();
    while ty.kind() == SyntaxKind::ParenthesizedType {
        ty = ty.type_();
    }
    if ty.kind() != SyntaxKind::UnionType {
        return None;
    }
    let mut members = Vec::new();
    let mut symbols = Vec::new();
    for mut part in ty.types().nodes().iter() {
        while part.kind() == SyntaxKind::ParenthesizedType {
            part = part.type_();
        }
        match part.kind() {
            SyntaxKind::TypeLiteral => members.push(part),
            SyntaxKind::TypeReference => {
                let name = part.type_name();
                if name.kind() != SyntaxKind::Identifier {
                    return None;
                }
                let symbol = ctx.resolve(name);
                if symbol.is_nil() {
                    return None;
                }
                let declarations = ctx.checker.sym(symbol).declarations.to_vec();
                let [declaration] = declarations.as_slice() else {
                    return None;
                };
                let file = ctx.file_of_node(*declaration)?;
                if ctx.files[file].js || declaration.flags().intersects(NodeFlags::Ambient) {
                    return None;
                }
                match declaration.kind() {
                    SyntaxKind::InterfaceDeclaration => members.push(*declaration),
                    SyntaxKind::TypeAliasDeclaration => {
                        let mut target = declaration.type_();
                        while target.kind() == SyntaxKind::ParenthesizedType {
                            target = target.type_();
                        }
                        if target.kind() != SyntaxKind::TypeLiteral {
                            return None;
                        }
                        members.push(target);
                    }
                    _ => return None,
                }
                symbols.push(symbol);
            }
            SyntaxKind::LiteralType if matches!(part.literal().kind(), SyntaxKind::NullKeyword) => {
            }
            SyntaxKind::UndefinedKeyword => {}
            _ => return None,
        }
    }
    (members.len() >= 2).then_some((members, symbols))
}

/// The tag property signature named `key` of a member and its literal.
fn tag_of(member: Node, key: &str) -> Option<(Node, Node)> {
    let signature = member.members().iter().find(|m| {
        m.kind() == SyntaxKind::PropertySignature
            && matches!(
                m.name().kind(),
                SyntaxKind::Identifier | SyntaxKind::StringLiteral
            )
            && m.name().text() == key
    })?;
    if signature.question_token().is_some() {
        return None;
    }
    let ty = signature.type_();
    if ty.is_nil() || ty.kind() != SyntaxKind::LiteralType {
        return None;
    }
    let literal = ty.literal();
    matches!(
        literal.kind(),
        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
    )
    .then_some((signature, literal))
}

/// A union alias nominating a domain: tag key, members, type symbols,
/// alias declaration and file.
type Seed = (String, Vec<Node>, Vec<SymbolId>, Node, usize);

fn nominate(ctx: &mut Ctx) -> Vec<Domain> {
    // Seeds in deterministic order.
    let mut seeds: Vec<Seed> = Vec::new();
    for file in 0..ctx.files.len() {
        if ctx.files[file].js {
            continue;
        }
        let nodes = ctx.files[file].nodes.clone();
        for node in nodes {
            if node.kind() != SyntaxKind::TypeAliasDeclaration
                || node.flags().intersects(NodeFlags::Ambient)
            {
                continue;
            }
            let Some((members, mut symbols)) = union_members(ctx, node) else {
                continue;
            };
            let first = members[0];
            let keys: Vec<String> = first
                .members()
                .iter()
                .filter(|m| m.kind() == SyntaxKind::PropertySignature)
                .map(|m| m.name().text().to_owned())
                .filter(|key| members.iter().all(|&m| tag_of(m, key).is_some()))
                .collect();
            let alias_symbol = ctx.resolve(node.name());
            if alias_symbol.is_some() {
                symbols.push(alias_symbol);
            }
            for key in keys {
                seeds.push((key, members.clone(), symbols.clone(), node, file));
            }
        }
    }
    // Unions that share a member under the same tag form one domain.
    let mut domains: Vec<Domain> = Vec::new();
    let mut owner: HashMap<(String, Node), usize> = HashMap::new();
    for (key, members, symbols, alias, file) in seeds {
        let mut targets: BTreeSet<usize> = members
            .iter()
            .filter_map(|&m| owner.get(&(key.clone(), m)).copied())
            .collect();
        let index = match targets.pop_first() {
            Some(index) => index,
            None => {
                domains.push(Domain {
                    file,
                    alias,
                    key: key.clone(),
                    members: Vec::new(),
                    symbols: Vec::new(),
                    values: Vec::new(),
                });
                domains.len() - 1
            }
        };
        // Fold any other domain sharing a member into `index`.
        for other in targets.into_iter().rev() {
            let folded = std::mem::take(&mut domains[other].members);
            let folded_symbols = std::mem::take(&mut domains[other].symbols);
            domains[index].members.extend(folded);
            domains[index].symbols.extend(folded_symbols);
            domains[other].key.clear();
        }
        for member in members {
            if domains[index].members.iter().any(|m| m.node == member) {
                continue;
            }
            let (signature, literal) = tag_of(member, &key).expect("checked tag");
            domains[index].members.push(Member {
                node: member,
                signature,
                literal,
                value: literal.text().to_owned(),
            });
        }
        domains[index].symbols.extend(symbols);
        for m in &domains[index].members {
            owner.insert((key.clone(), m.node), index);
        }
    }
    domains.retain(|d| !d.key.is_empty() && !d.members.is_empty());
    for domain in &mut domains {
        domain.members.sort_by_key(|m| {
            (
                source_file_info(get_source_file_of_node(m.node))
                    .file_name
                    .clone(),
                m.node.pos(),
            )
        });
        domain.symbols.sort_by_key(|s| s.0);
        domain.symbols.dedup();
        let mut values = Vec::new();
        for member in &domain.members {
            if !values.contains(&member.value) {
                values.push(member.value.clone());
            }
        }
        domain.values = values;
    }
    domains
}

/// Domain-independent indexes over application type positions.
struct TypeIndex {
    /// String literal types by text, with their literal node.
    literals: HashMap<String, Vec<Node>>,
    /// Indexed access types whose index is a string literal type, by text.
    indexed: HashMap<String, Vec<Node>>,
    /// Dynamic `import()` calls and the module they load.
    dynamic_imports: Vec<(usize, usize)>,
    /// Symbols that project sources declare in the global scope or in
    /// module augmentations. Code anywhere can reach them without an
    /// import.
    globals: Vec<SymbolId>,
}

/// Declarations of a script or augmentation body.
fn declared_symbols(statements: NodeSlice, out: &mut Vec<SymbolId>) {
    for statement in statements.iter() {
        if statement.kind() == SyntaxKind::VariableStatement {
            for declaration in statement.declaration_list().declarations().nodes().iter() {
                if declaration.symbol().is_some() {
                    out.push(declaration.symbol());
                }
            }
        } else if statement.symbol().is_some() {
            out.push(statement.symbol());
        }
    }
}

/// Global declarations and augmentations of a project source file.
fn global_symbols(file: Node, out: &mut Vec<SymbolId>) {
    if !is_external_module(file) {
        declared_symbols(file.statements(), out);
    }
    let mut nodes = Vec::new();
    crate::compiler::collect(file, &mut nodes);
    for node in nodes {
        if node.kind() == SyntaxKind::ModuleDeclaration
            && (node.name().kind() == SyntaxKind::StringLiteral
                || is_global_scope_augmentation(node))
            && node.body().is_some()
            && node.body().kind() == SyntaxKind::ModuleBlock
        {
            declared_symbols(node.body().statements(), out);
        }
    }
}

impl TypeIndex {
    fn build(ctx: &mut Ctx) -> Self {
        let mut literals: HashMap<String, Vec<Node>> = HashMap::new();
        let mut indexed: HashMap<String, Vec<Node>> = HashMap::new();
        let mut dynamic_imports = Vec::new();
        for file in 0..ctx.files.len() {
            if ctx.files[file].js {
                continue;
            }
            let nodes = ctx.files[file].nodes.clone();
            for node in nodes {
                match node.kind() {
                    // An indexed access key names a property, not a value.
                    SyntaxKind::LiteralType
                        if !(node.parent().kind() == SyntaxKind::IndexedAccessType
                            && node.parent().index_type() == node) =>
                    {
                        let literal = node.literal();
                        if matches!(
                            literal.kind(),
                            SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
                        ) {
                            literals
                                .entry(literal.text().to_owned())
                                .or_default()
                                .push(literal);
                        }
                    }
                    SyntaxKind::IndexedAccessType => {
                        let index = node.index_type();
                        if index.kind() == SyntaxKind::LiteralType
                            && matches!(
                                index.literal().kind(),
                                SyntaxKind::StringLiteral
                                    | SyntaxKind::NoSubstitutionTemplateLiteral
                            )
                        {
                            indexed
                                .entry(index.literal().text().to_owned())
                                .or_default()
                                .push(node);
                        }
                    }
                    SyntaxKind::CallExpression
                        if node.expression().kind() == SyntaxKind::ImportKeyword =>
                    {
                        if let Some(argument) = node.arguments().first() {
                            let symbol = ctx.checker.get_symbol_at_location(argument);
                            let target = if symbol.is_nil() {
                                None
                            } else {
                                ctx.checker
                                    .sym(symbol)
                                    .declarations
                                    .first()
                                    .copied()
                                    .and_then(|d| ctx.file_of_node(d))
                            };
                            if let Some(target) = target {
                                dynamic_imports.push((file, target));
                            }
                        }
                    }
                    _ => {}
                }
            }
        }
        let mut globals = Vec::new();
        for info in &ctx.files {
            if !info.js {
                global_symbols(info.node, &mut globals);
            }
        }
        for &external in &ctx.externals {
            let name = &source_file_info(external).file_name;
            if !name.contains("/node_modules/")
                && !name.starts_with("bundled:")
                && !is_source_file_from_external_library(external)
            {
                global_symbols(external, &mut globals);
            }
        }
        globals.sort_by_key(|s| s.0);
        globals.dedup();
        Self {
            literals,
            indexed,
            dynamic_imports,
            globals,
        }
    }
}

type Proven<T> = std::result::Result<T, Reason>;

struct Proof<'d> {
    domain: &'d Domain,
    member_nodes: HashSet<Node>,
    signatures: HashSet<Node>,
    memo: HashMap<TypeId, bool>,
    scope: BTreeSet<usize>,
    /// Literal nodes to rewrite to their code.
    rewrites: BTreeMap<(usize, usize), (Node, usize)>,
    /// Object literals proven to be producers of this domain.
    producers: HashSet<Node>,
}

impl<'d> Proof<'d> {
    fn new(domain: &'d Domain) -> Self {
        Self {
            domain,
            member_nodes: domain.members.iter().map(|m| m.node).collect(),
            signatures: domain.members.iter().map(|m| m.signature).collect(),
            memo: HashMap::new(),
            scope: BTreeSet::new(),
            rewrites: BTreeMap::new(),
            producers: HashSet::new(),
        }
    }

    fn run(
        &mut self,
        ctx: &mut Ctx,
        tags: &HashSet<Node>,
        index: &TypeIndex,
    ) -> Proven<Vec<FileEdit>> {
        self.check_declarations(ctx, tags, index)?;
        self.collect_scope(ctx, index)?;
        let files: Vec<usize> = self.scope.iter().copied().collect();
        for &file in &files {
            // Scripts share their top-level bindings with unknown code.
            if ctx.files[file].js || !is_external_module(ctx.files[file].node) {
                return Err(Reason::ExternalBoundary);
            }
            self.find_producers(ctx, file)?;
        }
        for &file in &files {
            crate::app::check_cancelled().map_err(|_| Reason::UnsupportedSyntax)?;
            if let Err((node, reason)) = self.check_file(ctx, file) {
                tracing::trace!(
                    pass = %PASS,
                    ?reason,
                    kind = ?node.kind(),
                    "discriminant flow obligation failed"
                );
                return Err(reason);
            }
        }
        for member in &self.domain.members {
            let file = ctx
                .file_of_node(member.literal)
                .ok_or(Reason::ExternalBoundary)?;
            self.rewrite(member.literal, file)?;
        }
        Ok(self
            .rewrites
            .values()
            .map(|&(node, file)| {
                let code = self
                    .domain
                    .code(node.text())
                    .expect("rewritten literals are domain values");
                FileEdit::replace(
                    &ctx.files[file].name,
                    Ctx::start(node),
                    node.end() as usize,
                    code.to_string(),
                )
            })
            .collect())
    }

    fn rewrite(&mut self, literal: Node, file: usize) -> Proven<()> {
        if self.domain.code(literal.text()).is_none() {
            return Err(Reason::UnprovenRuntimeFact);
        }
        self.rewrites
            .insert((file, Ctx::start(literal)), (literal, file));
        Ok(())
    }

    /// Declaration-level obligations: internal, unmerged member types without
    /// protocol members, and no other type spelling a domain value or
    /// indexing the tag.
    fn check_declarations(
        &mut self,
        ctx: &mut Ctx,
        tags: &HashSet<Node>,
        index: &TypeIndex,
    ) -> Proven<()> {
        if SPECIAL.contains(&self.domain.key.as_str()) {
            return Err(Reason::SpecialProperty);
        }
        for member in &self.domain.members {
            for property in member.node.members().iter() {
                let name = property.name();
                if property.kind() == SyntaxKind::IndexSignature
                    || name.kind() == SyntaxKind::ComputedPropertyName
                {
                    return Err(Reason::DynamicAccess);
                }
                if name.is_some() && SPECIAL.contains(&name.text()) {
                    return Err(Reason::SpecialProperty);
                }
            }
            if member.node.kind() == SyntaxKind::InterfaceDeclaration {
                let symbol = ctx.resolve(member.node.name());
                if symbol.is_nil() || ctx.checker.sym(symbol).declarations.len() != 1 {
                    return Err(Reason::DeclarationMerging);
                }
            }
        }
        for &symbol in &self.domain.symbols {
            if ctx.public.contains(&symbol) {
                return Err(Reason::PublicExport);
            }
            if ctx.js_referenced.contains(&symbol) {
                return Err(Reason::ExternalBoundary);
            }
        }
        // Another literal type spelling a domain value only conflicts when
        // its type expression relates to the domain, as in
        // `Extract<Shape, { kind: "circle" }>`; unrelated literal types
        // never meet a domain value, which the flow check guarantees.
        for value in &self.domain.values {
            let Some(nodes) = index.literals.get(value) else {
                continue;
            };
            for &literal in nodes {
                if tags.contains(&literal) {
                    continue;
                }
                let top = outermost_type(literal);
                if self.mentions_domain(ctx, top) {
                    return Err(Reason::TypeConstraint);
                }
            }
        }
        // Global declarations are reachable from modules outside the scope.
        for &symbol in &index.globals {
            let flags = ctx.checker.sym(symbol).flags;
            let ty = if flags.intersects(SymbolFlags::Value) {
                ctx.checker.get_type_of_symbol(symbol)
            } else if flags
                .intersects(SymbolFlags::Class | SymbolFlags::Interface | SymbolFlags::TypeAlias)
            {
                ctx.checker.get_declared_type_of_symbol(symbol)
            } else {
                continue;
            };
            if self.carries(ctx, ty) {
                return Err(Reason::ExternalBoundary);
            }
        }
        if let Some(nodes) = index.indexed.get(&self.domain.key) {
            for &node in nodes {
                let object = ctx.checker.get_type_from_type_node(node.object_type());
                if self.carries(ctx, object) {
                    return Err(Reason::TypeConstraint);
                }
            }
        }
        Ok(())
    }

    /// Whether the tag property of an object type is this domain's tag.
    /// Union constituents without the property (`null`, `PromiseLike` in an
    /// async return context, primitives) do not take part; constituents
    /// mixing domain and other tag declarations are an error.
    fn tag_of_type(&mut self, ctx: &mut Ctx, ty: TypeId) -> Proven<bool> {
        if ty.is_nil() {
            return Ok(false);
        }
        let parts: Vec<TypeId> = if ctx.checker.ty(ty).flags().intersects(TypeFlags::Union) {
            ctx.checker.ty(ty).types_list().to_vec()
        } else {
            vec![ty]
        };
        let (mut ours, mut others) = (false, false);
        for part in parts {
            if !ctx
                .checker
                .ty(part)
                .flags()
                .intersects(TypeFlags::Object | TypeFlags::Intersection)
            {
                continue;
            }
            let property = ctx.checker.get_property_of_type(part, &self.domain.key);
            if property.is_nil() {
                continue;
            }
            if self.tag_symbol(ctx, property)? {
                ours = true;
            } else {
                others = true;
            }
        }
        if ours && others {
            return Err(Reason::Escapes);
        }
        Ok(ours)
    }

    /// Whether a type expression refers to a type that carries the domain.
    fn mentions_domain(&mut self, ctx: &mut Ctx, top: Node) -> bool {
        let mut nodes = Vec::new();
        crate::compiler::collect(top, &mut nodes);
        nodes.into_iter().any(|n| {
            matches!(
                n.kind(),
                SyntaxKind::TypeReference
                    | SyntaxKind::TypeQuery
                    | SyntaxKind::ExpressionWithTypeArguments
            ) && {
                let ty = ctx.checker.get_type_from_type_node(n);
                self.carries(ctx, ty)
            }
        })
    }

    /// Whether `symbol` is a tag property of this domain. Errors when an
    /// access mixes domain and other declarations.
    fn tag_symbol(&self, ctx: &Ctx, symbol: SymbolId) -> Proven<bool> {
        if symbol.is_nil() {
            return Ok(false);
        }
        let declarations = &ctx.checker.sym(symbol).declarations;
        let ours = declarations
            .iter()
            .filter(|d| self.signatures.contains(d))
            .count();
        if ours == 0 {
            Ok(false)
        } else if ours == declarations.len() {
            Ok(true)
        } else {
            Err(Reason::Escapes)
        }
    }

    /// Every module that may hold a domain value.
    fn collect_scope(&mut self, ctx: &mut Ctx, index: &TypeIndex) -> Proven<()> {
        let mut scope = BTreeSet::new();
        for &symbol in &self.domain.symbols {
            for r in ctx.refs(symbol) {
                scope.insert(r.file);
            }
        }
        for member in &self.domain.members {
            if let Some(file) = ctx.file_of_node(member.node) {
                scope.insert(file);
            }
        }
        // Tag reads anywhere.
        let key = self.domain.key.clone();
        for symbol in ctx.sorted_symbols() {
            if ctx.checker.sym(symbol).name.as_str() != key {
                continue;
            }
            if self.tag_symbol(ctx, symbol)? {
                for r in ctx.refs(symbol) {
                    scope.insert(r.file);
                }
            }
        }
        // Values whose types carry the domain cross modules through exports.
        for file in 0..ctx.files.len() {
            let module = ctx.files[file].node.symbol();
            if module.is_nil() {
                continue;
            }
            let exports = ctx.checker.sym(module).exports;
            let entries = ctx.checker.symbols.values(exports);
            for export in entries {
                let target = ctx.skip_alias(export);
                if target.is_nil() || !ctx.checker.sym(target).flags.intersects(SymbolFlags::Value)
                {
                    continue;
                }
                let ty = ctx.checker.get_type_of_symbol(target);
                if !self.carries(ctx, ty) {
                    continue;
                }
                if ctx.public.contains(&target) || ctx.public.contains(&export) {
                    return Err(Reason::PublicExport);
                }
                if ctx.js_referenced.contains(&target) {
                    return Err(Reason::ExternalBoundary);
                }
                scope.insert(file);
                for r in ctx.refs(target) {
                    scope.insert(r.file);
                }
            }
        }
        // Namespace and dynamic imports of scoped modules see their values.
        loop {
            let before = scope.len();
            for file in scope.clone() {
                let module = ctx.files[file].node.symbol();
                if module.is_some() {
                    for r in ctx.refs(module) {
                        scope.insert(r.file);
                    }
                }
            }
            for &(importer, target) in &index.dynamic_imports {
                if scope.contains(&target) {
                    scope.insert(importer);
                }
            }
            if scope.len() == before {
                break;
            }
        }
        self.scope = scope;
        Ok(())
    }

    /// Whether values of type `t` may contain a domain object.
    fn carries(&mut self, ctx: &mut Ctx, t: TypeId) -> bool {
        if t.is_nil() {
            return false;
        }
        if let Some(&known) = self.memo.get(&t) {
            return known;
        }
        self.memo.insert(t, false);
        let result = self.compute_carries(ctx, t);
        self.memo.insert(t, result);
        result
    }

    fn compute_carries(&mut self, ctx: &mut Ctx, t: TypeId) -> bool {
        let flags = ctx.checker.ty(t).flags();
        if flags.intersects(TypeFlags::Union | TypeFlags::Intersection) {
            let parts = ctx.checker.ty(t).types_list();
            return parts.iter().any(|&p| self.carries(ctx, p));
        }
        if flags.intersects(TypeFlags::Instantiable) {
            let constraint = ctx.checker.get_base_constraint_of_type(t);
            return constraint != t && self.carries(ctx, constraint);
        }
        if !flags.intersects(TypeFlags::Object) {
            return false;
        }
        let symbol = ctx.checker.ty(t).symbol;
        let declarations = if symbol.is_some() {
            ctx.checker.sym(symbol).declarations.to_vec()
        } else {
            Vec::new()
        };
        if declarations.iter().any(|d| self.member_nodes.contains(d)) {
            return true;
        }
        let object_flags = ctx.checker.ty(t).object_flags();
        if object_flags.intersects(ObjectFlags::Reference) {
            let arguments = ctx.checker.get_type_arguments(t);
            if arguments.iter().any(|&a| self.carries(ctx, a)) {
                return true;
            }
        }
        let external =
            !declarations.is_empty() && declarations.iter().all(|&d| ctx.file_of_node(d).is_none());
        if (declarations.is_empty() || external)
            && !object_flags.intersects(
                ObjectFlags::Instantiated | ObjectFlags::Mapped | ObjectFlags::ReverseMapped,
            )
        {
            // A non-generic library type cannot mention application types.
            return false;
        }
        let properties = ctx.checker.get_properties_of_type(t);
        for &property in properties.iter() {
            if ctx
                .checker
                .sym(property)
                .declarations
                .iter()
                .any(|d| self.signatures.contains(d))
            {
                return true;
            }
            let ty = ctx.checker.get_type_of_symbol(property);
            if self.carries(ctx, ty) {
                return true;
            }
        }
        for kind in [SignatureKind::Call, SignatureKind::Construct] {
            let signatures = ctx.checker.get_signatures_of_type(t, kind);
            for &signature in signatures.iter() {
                if self.signature_carries(ctx, signature) {
                    return true;
                }
            }
        }
        let infos = ctx.checker.get_index_infos_of_type(t);
        for &info in infos.iter() {
            let value = ctx.checker.index_info(info).value_type;
            if self.carries(ctx, value) {
                return true;
            }
        }
        false
    }

    fn signature_carries(&mut self, ctx: &mut Ctx, signature: SignatureId) -> bool {
        let parameters = ctx.checker.sig(signature).parameters.clone();
        for parameter in parameters {
            let ty = ctx.checker.get_type_of_symbol(parameter);
            if self.carries(ctx, ty) {
                return true;
            }
        }
        let result = ctx.checker.get_return_type_of_signature(signature);
        self.carries(ctx, result)
    }

    /// Records the object literals whose contextual tag is this domain's and
    /// checks their tag values.
    fn find_producers(&mut self, ctx: &mut Ctx, file: usize) -> Proven<()> {
        let nodes = ctx.files[file].nodes.clone();
        for node in nodes {
            if node.kind() != SyntaxKind::ObjectLiteralExpression {
                continue;
            }
            let tag = node.properties().iter().find(|p| {
                matches!(
                    p.kind(),
                    SyntaxKind::PropertyAssignment | SyntaxKind::ShorthandPropertyAssignment
                ) && matches!(
                    p.name().kind(),
                    SyntaxKind::Identifier | SyntaxKind::StringLiteral
                ) && p.name().text() == self.domain.key
            });
            let Some(tag) = tag else {
                continue;
            };
            let contextual = ctx.checker.get_contextual_type(node, ContextFlags::None);
            if contextual.is_nil() {
                continue;
            }
            if !self.tag_of_type(ctx, contextual)? {
                continue;
            }
            if tag.kind() == SyntaxKind::ShorthandPropertyAssignment {
                return Err(Reason::UnprovenRuntimeFact);
            }
            self.tag_value(ctx, file, tag.initializer())?;
            self.producers.insert(node);
        }
        Ok(())
    }

    /// A value stored into the tag: a domain literal, another tag of this
    /// domain, or a conditional of those.
    fn tag_value(&mut self, ctx: &mut Ctx, file: usize, value: Node) -> Proven<()> {
        let inner = skip_outer(value);
        match inner.kind() {
            SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral => {
                self.rewrite(inner, file)
            }
            SyntaxKind::ConditionalExpression => {
                self.tag_value(ctx, file, inner.when_true())?;
                self.tag_value(ctx, file, inner.when_false())
            }
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
                if self.tag_access(ctx, inner)? =>
            {
                Ok(())
            }
            _ => Err(Reason::UnprovenRuntimeFact),
        }
    }

    /// Whether the access reads this domain's tag.
    fn tag_access(&mut self, ctx: &mut Ctx, access: Node) -> Proven<bool> {
        let name = match access.kind() {
            SyntaxKind::PropertyAccessExpression => access.name(),
            SyntaxKind::ElementAccessExpression => {
                let argument = skip_outer(access.argument_expression());
                if !matches!(
                    argument.kind(),
                    SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral
                ) {
                    return Ok(false);
                }
                argument
            }
            _ => return Ok(false),
        };
        if name.text() != self.domain.key {
            return Ok(false);
        }
        let symbol = ctx.checker.get_symbol_at_location(name);
        self.tag_symbol(ctx, symbol)
    }

    fn check_file(&mut self, ctx: &mut Ctx, file: usize) -> Result<(), (Node, Reason)> {
        let nodes = ctx.files[file].nodes.clone();
        for node in nodes {
            self.check_node(ctx, file, node).map_err(|r| (node, r))?;
        }
        Ok(())
    }

    fn check_node(&mut self, ctx: &mut Ctx, file: usize, node: Node) -> Proven<()> {
        {
            match node.kind() {
                SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
                    if self.tag_access(ctx, node)? {
                        self.tag_use(ctx, file, node)?;
                    }
                }
                SyntaxKind::BindingElement => self.binding_element(ctx, node)?,
                SyntaxKind::TypePredicate => self.type_predicate(ctx, node)?,
                SyntaxKind::Identifier if node.text() == "arguments" => {
                    self.arguments_object(ctx, node)?
                }
                _ => {}
            }
            self.edge_site(ctx, node)?;
            if is_value_expression(node) {
                let ty = expression_type(ctx, node);
                if self.carries(ctx, ty) {
                    self.value_context(ctx, node, ty)?;
                }
            }
        }
        Ok(())
    }

    /// A read or write of the tag.
    fn tag_use(&mut self, ctx: &mut Ctx, file: usize, access: Node) -> Proven<()> {
        let outer = walk_up_transparent(access);
        let holder = outer.parent();
        if is_assignment_target(access) {
            if holder.kind() == SyntaxKind::BinaryExpression
                && holder.operator_token().kind() == SyntaxKind::EqualsToken
                && holder.left() == outer
            {
                return self.tag_value(ctx, file, holder.right());
            }
            return Err(Reason::Mutation);
        }
        match holder.kind() {
            SyntaxKind::BinaryExpression
                if matches!(
                    holder.operator_token().kind(),
                    SyntaxKind::EqualsEqualsEqualsToken
                        | SyntaxKind::ExclamationEqualsEqualsToken
                        | SyntaxKind::EqualsEqualsToken
                        | SyntaxKind::ExclamationEqualsToken
                ) =>
            {
                let other = if holder.left() == outer {
                    holder.right()
                } else {
                    holder.left()
                };
                self.comparand(ctx, file, other)
            }
            SyntaxKind::SwitchStatement if holder.expression() == outer => {
                for clause in holder.case_block().clauses().nodes().iter() {
                    if clause.kind() == SyntaxKind::CaseClause {
                        self.comparand(ctx, file, clause.expression())?;
                    }
                }
                Ok(())
            }
            // A copy into another producer of this domain.
            SyntaxKind::PropertyAssignment
                if holder.initializer() == outer
                    && self.producers.contains(&holder.parent())
                    && holder.name().text() == self.domain.key =>
            {
                Ok(())
            }
            SyntaxKind::ConditionalExpression
                if holder.condition() != outer && self.feeds_producer(holder) =>
            {
                Ok(())
            }
            SyntaxKind::BinaryExpression
                if holder.operator_token().kind() == SyntaxKind::EqualsToken
                    && holder.right() == outer
                    && self.tag_target(ctx, holder.left())? =>
            {
                Ok(())
            }
            _ => Err(Reason::ObservableOperation),
        }
    }

    /// Whether a conditional's value ends up as a producer's tag.
    fn feeds_producer(&self, mut node: Node) -> bool {
        loop {
            let outer = walk_up_transparent(node);
            let holder = outer.parent();
            match holder.kind() {
                SyntaxKind::ConditionalExpression if holder.condition() != outer => node = holder,
                SyntaxKind::PropertyAssignment => {
                    return holder.initializer() == outer
                        && self.producers.contains(&holder.parent())
                        && holder.name().text() == self.domain.key;
                }
                _ => return false,
            }
        }
    }

    fn tag_target(&mut self, ctx: &mut Ctx, target: Node) -> Proven<bool> {
        let target = skip_outer(target);
        if !matches!(
            target.kind(),
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
        ) {
            return Ok(false);
        }
        self.tag_access(ctx, target)
    }

    /// The other side of a tag comparison: a domain literal or another tag
    /// of this domain.
    fn comparand(&mut self, ctx: &mut Ctx, file: usize, other: Node) -> Proven<()> {
        let inner = skip_outer(other);
        match inner.kind() {
            SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral => {
                self.rewrite(inner, file)
            }
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
                if self.tag_access(ctx, inner)? =>
            {
                Ok(())
            }
            _ => Err(Reason::ObservableOperation),
        }
    }

    /// Destructuring the tag or the rest of a domain object observes it.
    fn binding_element(&mut self, ctx: &mut Ctx, element: Node) -> Proven<()> {
        let pattern = element.parent();
        let source = ctx.checker.get_type_at_location(pattern);
        if !self.carries(ctx, source) {
            return Ok(());
        }
        if pattern.kind() == SyntaxKind::ArrayBindingPattern {
            // Array destructuring runs the source's iterator.
            return if library_iterable(ctx, source) {
                Ok(())
            } else {
                Err(Reason::CustomIterator)
            };
        }
        if element.dot_dot_dot_token().is_some() {
            return Err(Reason::Reflection);
        }
        let key = if element.property_name().is_some() {
            element.property_name()
        } else {
            element.name()
        };
        if key.kind() == SyntaxKind::ComputedPropertyName {
            return Err(Reason::DynamicAccess);
        }
        if key.text() == self.domain.key {
            return Err(Reason::ObservableOperation);
        }
        Ok(())
    }

    /// A type predicate that forgets the domain lets a domain value be seen
    /// under another type.
    fn type_predicate(&mut self, ctx: &mut Ctx, predicate: Node) -> Proven<()> {
        let function = predicate.parent();
        let name = predicate.parameter_name();
        if name.kind() == SyntaxKind::ThisType {
            return Err(Reason::UnsupportedSyntax);
        }
        let Some(parameter) = function
            .parameters()
            .iter()
            .find(|p| p.name().kind() == SyntaxKind::Identifier && p.name().text() == name.text())
        else {
            return Ok(());
        };
        let declared = ctx.checker.get_type_at_location(parameter.name());
        if !self.carries(ctx, declared) || predicate.type_().is_nil() {
            return Ok(());
        }
        let narrowed = ctx.checker.get_type_from_type_node(predicate.type_());
        if self.carries(ctx, narrowed) {
            Ok(())
        } else {
            Err(Reason::Escapes)
        }
    }

    fn arguments_object(&mut self, ctx: &mut Ctx, node: Node) -> Proven<()> {
        let symbol = ctx.checker.get_symbol_at_location(node);
        let declaration = if symbol.is_nil() {
            Node::NIL
        } else {
            ctx.checker.sym(symbol).value_declaration
        };
        if declaration.is_some() && ctx.file_of_node(declaration).is_some() {
            // A binding of the application named `arguments`.
            return Ok(());
        }
        let function = get_containing_function(node);
        if function.is_nil() {
            return Ok(());
        }
        let parameters: Vec<Node> = function.parameters().iter().collect();
        for parameter in parameters {
            let ty = ctx.checker.get_type_at_location(parameter.name());
            if self.carries(ctx, ty) {
                return Err(Reason::Reflection);
            }
        }
        Ok(())
    }

    /// Checks the flow edge that `node` starts, if any.
    fn edge_site(&mut self, ctx: &mut Ctx, node: Node) -> Proven<()> {
        match node.kind() {
            SyntaxKind::VariableDeclaration if node.initializer().is_some() => {
                let dest = if node.type_().is_some() {
                    ctx.checker.get_type_from_type_node(node.type_())
                } else if node.name().kind() == SyntaxKind::Identifier {
                    let symbol = ctx.checker.get_symbol_at_location(node.name());
                    if symbol.is_nil() {
                        return Ok(());
                    }
                    ctx.checker.get_type_of_symbol(symbol)
                } else {
                    return Ok(());
                };
                self.edge(ctx, node.initializer(), dest)
            }
            SyntaxKind::Parameter | SyntaxKind::PropertyDeclaration
                if node.initializer().is_some() =>
            {
                let symbol = node.symbol();
                if symbol.is_nil() {
                    return Ok(());
                }
                let dest = ctx.checker.get_type_of_symbol(symbol);
                self.edge(ctx, node.initializer(), dest)
            }
            SyntaxKind::BinaryExpression
                if matches!(
                    node.operator_token().kind(),
                    SyntaxKind::EqualsToken
                        | SyntaxKind::BarBarEqualsToken
                        | SyntaxKind::AmpersandAmpersandEqualsToken
                        | SyntaxKind::QuestionQuestionEqualsToken
                ) =>
            {
                let target = skip_outer(node.left());
                let dest = match target.kind() {
                    SyntaxKind::Identifier => {
                        let symbol = ctx.checker.get_symbol_at_location(target);
                        if symbol.is_nil() {
                            return Err(Reason::UnresolvedCall);
                        }
                        ctx.checker.get_type_of_symbol(symbol)
                    }
                    SyntaxKind::PropertyAccessExpression => {
                        let symbol = ctx.checker.get_symbol_at_location(target.name());
                        if symbol.is_nil() {
                            ctx.checker.get_type_at_location(target)
                        } else {
                            ctx.checker.get_type_of_symbol(symbol)
                        }
                    }
                    SyntaxKind::ElementAccessExpression => ctx.checker.get_type_at_location(target),
                    _ => {
                        // Destructuring assignment.
                        let ty = expression_type(ctx, node.right());
                        return if self.carries(ctx, ty) {
                            Err(Reason::UnsupportedSyntax)
                        } else {
                            Ok(())
                        };
                    }
                };
                self.edge(ctx, node.right(), dest)
            }
            SyntaxKind::CallExpression | SyntaxKind::NewExpression => self.call(ctx, node),
            SyntaxKind::ReturnStatement if node.expression().is_some() => {
                let function = get_containing_function(node);
                self.returned(ctx, function, node.expression())
            }
            SyntaxKind::ArrowFunction if node.body().kind() != SyntaxKind::Block => {
                self.returned(ctx, node, node.body())
            }
            SyntaxKind::PropertyAssignment => {
                let dest = ctx
                    .checker
                    .get_contextual_type(node.initializer(), ContextFlags::None);
                self.edge(ctx, node.initializer(), dest)
            }
            SyntaxKind::ShorthandPropertyAssignment => {
                let dest = ctx
                    .checker
                    .get_contextual_type(node.name(), ContextFlags::None);
                self.edge(ctx, node.name(), dest)
            }
            SyntaxKind::ArrayLiteralExpression => {
                let elements: Vec<Node> = node.elements().iter().collect();
                for element in elements {
                    if element.kind() == SyntaxKind::SpreadElement
                        || element.kind() == SyntaxKind::OmittedExpression
                    {
                        continue;
                    }
                    let dest = ctx.checker.get_contextual_type(element, ContextFlags::None);
                    self.edge(ctx, element, dest)?;
                }
                Ok(())
            }
            SyntaxKind::AsExpression | SyntaxKind::TypeAssertionExpression => {
                let ty = node.type_();
                if ty.kind() == SyntaxKind::TypeReference
                    && ty.type_name().kind() == SyntaxKind::Identifier
                    && ty.type_name().text() == "const"
                {
                    return Ok(());
                }
                let dest = ctx.checker.get_type_from_type_node(ty);
                self.edge(ctx, node.expression(), dest)
            }
            SyntaxKind::YieldExpression | SyntaxKind::JsxAttribute => {
                let value = if node.kind() == SyntaxKind::YieldExpression {
                    node.expression()
                } else {
                    node.initializer()
                };
                if value.is_some() {
                    let ty = expression_type(ctx, value);
                    if self.carries(ctx, ty) {
                        return Err(Reason::ExternalBoundary);
                    }
                }
                Ok(())
            }
            _ => Ok(()),
        }
    }

    fn returned(&mut self, ctx: &mut Ctx, function: Node, value: Node) -> Proven<()> {
        if function.is_nil() {
            return Ok(());
        }
        let signature = ctx.checker.get_signature_from_declaration(function);
        if signature.is_nil() {
            return Err(Reason::UnresolvedCall);
        }
        let mut dest = ctx.checker.get_return_type_of_signature(signature);
        if function.asterisk_token().is_some() {
            let ty = expression_type(ctx, value);
            if self.carries(ctx, ty) || self.carries(ctx, dest) {
                return Err(Reason::UnsupportedSyntax);
            }
            return Ok(());
        }
        if get_combined_modifier_flags(function).intersects(ModifierFlags::Async) {
            // An async function settles its promise with the value.
            dest = awaited(ctx, dest);
        }
        self.edge(ctx, value, dest)
    }

    /// Arguments of calls and constructions.
    fn call(&mut self, ctx: &mut Ctx, call: Node) -> Proven<()> {
        if call.kind() == SyntaxKind::CallExpression
            && matches!(
                call.expression().kind(),
                SyntaxKind::ImportKeyword | SyntaxKind::SuperKeyword
            )
        {
            return Ok(());
        }
        let arguments: Vec<Node> = call.arguments().iter().collect();
        let result = expression_type(ctx, call);
        let result_carries = self.carries(ctx, result);
        let mut argument_types = Vec::with_capacity(arguments.len());
        let mut any_carries = false;
        for &argument in &arguments {
            let ty = expression_type(ctx, argument);
            let carries = self.carries(ctx, ty);
            any_carries |= carries;
            argument_types.push(carries);
        }
        let signature = ctx.checker.get_resolved_signature_exported(call);
        let declaration = if signature.is_nil() {
            Node::NIL
        } else {
            ctx.checker.sig(signature).declaration
        };
        let application = declaration.is_some() && ctx.file_of_node(declaration).is_some();
        if !application {
            let collection = self.collection_operation(ctx, call);
            if !collection {
                // Library code may inspect or manufacture domain values.
                if any_carries || result_carries {
                    return Err(Reason::ExternalBoundary);
                }
                if signature.is_some() {
                    let parameters = ctx.checker.sig(signature).parameters.clone();
                    for parameter in parameters {
                        let ty = ctx.checker.get_type_of_symbol(parameter);
                        if self.carries(ctx, ty) {
                            return Err(Reason::ExternalBoundary);
                        }
                    }
                }
                return Ok(());
            }
            // Built-in collections: instantiated parameter types.
            if signature.is_nil() {
                return if any_carries || result_carries {
                    Err(Reason::UnresolvedCall)
                } else {
                    Ok(())
                };
            }
            let parameters = ctx.checker.sig(signature).parameters.clone();
            for (i, &argument) in arguments.iter().enumerate() {
                if argument.kind() == SyntaxKind::SpreadElement {
                    if !argument_types[i] {
                        continue;
                    }
                    // A built-in iterable spread into a rest parameter of a
                    // built-in operation, such as `list.push(...more)`.
                    let source = expression_type(ctx, argument);
                    let declared_rest = ctx
                        .checker
                        .sig(signature)
                        .declaration
                        .parameters()
                        .last()
                        .is_some_and(|p| p.dot_dot_dot_token().is_some());
                    let rest = parameters
                        .last()
                        .copied()
                        .filter(|_| declared_rest && i + 1 >= parameters.len());
                    let rest_type = rest.map(|p| ctx.checker.get_type_of_symbol(p));
                    match rest_type {
                        Some(dest) if library_iterable(ctx, source) && self.carries(ctx, dest) => {}
                        _ => return Err(Reason::ExternalBoundary),
                    }
                    continue;
                }
                let Some(&parameter) = parameters.get(i).or(parameters.last()) else {
                    if argument_types[i] {
                        return Err(Reason::ExternalBoundary);
                    }
                    continue;
                };
                let mut dest = ctx.checker.get_type_of_symbol(parameter);
                if i >= parameters.len() - 1 && ctx.checker.is_array_type(dest) {
                    // A rest parameter receives the argument as an element.
                    let rest = ctx
                        .checker
                        .sig(signature)
                        .declaration
                        .parameters()
                        .last()
                        .is_some_and(|p| p.dot_dot_dot_token().is_some());
                    if rest {
                        dest = ctx.checker.get_element_type_of_array_type(dest);
                    }
                }
                self.edge(ctx, argument, dest)?;
            }
            return Ok(());
        }
        // Application callee: its declared (uninstantiated) parameter types
        // are the slots its body sees.
        let generic = ctx.checker.sig(signature).target.is_some()
            || !declaration.type_parameters().is_empty();
        if generic && (any_carries || result_carries) {
            return Err(Reason::TypeConstraint);
        }
        let parameters: Vec<Node> = declaration.parameters().iter().collect();
        for (i, &argument) in arguments.iter().enumerate() {
            if argument.kind() == SyntaxKind::SpreadElement {
                if argument_types[i] {
                    return Err(Reason::Reflection);
                }
                continue;
            }
            let parameter = parameters.get(i).copied().or_else(|| {
                parameters
                    .last()
                    .copied()
                    .filter(|p| p.dot_dot_dot_token().is_some())
            });
            let Some(parameter) = parameter else {
                if argument_types[i] {
                    // Only `arguments` could see it.
                    return Err(Reason::ObservableArity);
                }
                continue;
            };
            let symbol = parameter.symbol();
            if symbol.is_nil() {
                return Err(Reason::UnresolvedCall);
            }
            let mut dest = ctx.checker.get_type_of_symbol(symbol);
            if parameter.dot_dot_dot_token().is_some() && ctx.checker.is_array_type(dest) {
                dest = ctx.checker.get_element_type_of_array_type(dest);
            }
            self.edge(ctx, argument, dest)?;
        }
        Ok(())
    }

    /// Whether the call is an operation of a built-in collection or a
    /// built-in collection constructor.
    fn collection_operation(&mut self, ctx: &mut Ctx, call: Node) -> bool {
        let callee = skip_outer(call.expression());
        if call.kind() == SyntaxKind::NewExpression {
            return callee.kind() == SyntaxKind::Identifier
                && COLLECTION_CONSTRUCTORS.contains(&callee.text())
                && global_library_symbol(ctx, callee);
        }
        if callee.kind() != SyntaxKind::PropertyAccessExpression {
            return false;
        }
        let receiver = skip_outer(callee.expression());
        if receiver.kind() == SyntaxKind::Identifier
            && matches!(receiver.text(), "Array" | "Promise")
            && global_library_symbol(ctx, receiver)
        {
            return matches!(
                callee.name().text(),
                "from" | "of" | "isArray" | "resolve" | "all" | "allSettled" | "race" | "any"
            );
        }
        let ty = expression_type(ctx, receiver);
        let ty = ctx.checker.get_non_nullable_type(ty);
        let apparent = ctx.checker.get_apparent_type(ty);
        library_collection(ctx, apparent)
    }

    /// Checks one flow of `src` into a slot of type `dest`.
    fn edge(&mut self, ctx: &mut Ctx, src: Node, dest: TypeId) -> Proven<()> {
        if dest.is_nil() || src.is_nil() {
            return Ok(());
        }
        let src_type = expression_type(ctx, src);
        let from = self.carries(ctx, src_type);
        let to = self.carries(ctx, dest);
        match (from, to) {
            (false, false) => Ok(()),
            (true, false) => Err(Reason::Escapes),
            (false, true) => self.fresh(ctx, src, dest),
            (true, true) => self.compatible(ctx, src_type, dest, 0),
        }
    }

    /// A value whose type does not carry the domain enters a domain slot.
    fn fresh(&mut self, ctx: &mut Ctx, src: Node, dest: TypeId) -> Proven<()> {
        let inner = skip_transparent(src);
        match inner.kind() {
            SyntaxKind::ObjectLiteralExpression => self.object_into(ctx, inner, dest),
            SyntaxKind::ArrayLiteralExpression => Ok(()),
            SyntaxKind::ConditionalExpression => {
                self.edge(ctx, inner.when_true(), dest)?;
                self.edge(ctx, inner.when_false(), dest)
            }
            SyntaxKind::BinaryExpression
                if matches!(
                    inner.operator_token().kind(),
                    SyntaxKind::BarBarToken
                        | SyntaxKind::QuestionQuestionToken
                        | SyntaxKind::AmpersandAmpersandToken
                ) =>
            {
                self.edge(ctx, inner.left(), dest)?;
                self.edge(ctx, inner.right(), dest)
            }
            SyntaxKind::BinaryExpression
                if inner.operator_token().kind() == SyntaxKind::CommaToken =>
            {
                self.edge(ctx, inner.right(), dest)
            }
            SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression => {
                // Parameters the slot passes domain values to must keep the
                // domain; fewer parameters ignore them.
                let src_type = expression_type(ctx, inner);
                self.compatible(ctx, src_type, dest, 0)
            }
            SyntaxKind::NewExpression if self.collection_operation(ctx, inner) => Ok(()),
            _ => {
                let ty = expression_type(ctx, inner);
                if plain_data(ctx, ty) {
                    Ok(())
                } else {
                    Err(Reason::ExternalBoundary)
                }
            }
        }
    }

    /// A fresh object literal entering a domain slot must be a recognized
    /// producer when the slot has a domain tag.
    fn object_into(&mut self, ctx: &mut Ctx, literal: Node, dest: TypeId) -> Proven<()> {
        if !self.tag_of_type(ctx, dest)? {
            // A container of domain values; its fields are flows of their own.
            return Ok(());
        }
        if self.producers.contains(&literal) {
            return Ok(());
        }
        let has_tag = literal
            .properties()
            .iter()
            .any(|p| p.name().is_some() && p.name().text() == self.domain.key);
        if has_tag {
            return Err(Reason::UnprovenRuntimeFact);
        }
        // Spreads must come from domain values; their elements are checked
        // as values.
        let spreads: Vec<Node> = literal
            .properties()
            .iter()
            .filter(|p| p.kind() == SyntaxKind::SpreadAssignment)
            .collect();
        for spread in spreads {
            let ty = expression_type(ctx, spread.expression());
            if !self.carries(ctx, ty) {
                return Err(Reason::UnprovenRuntimeFact);
            }
        }
        Ok(())
    }

    /// Function values and objects with methods: a slot that passes domain
    /// values to a parameter needs a parameter that keeps the domain, and a
    /// returned domain value needs a slot that does.
    fn compatible(&mut self, ctx: &mut Ctx, src: TypeId, dest: TypeId, depth: usize) -> Proven<()> {
        if depth > 2 || src == dest {
            return Ok(());
        }
        let src_signatures = ctx.checker.get_signatures_of_type(src, SignatureKind::Call);
        let dest_signatures = ctx
            .checker
            .get_signatures_of_type(dest, SignatureKind::Call);
        if !dest_signatures.is_empty() {
            for &target in dest_signatures.iter() {
                let target_parameters = ctx.checker.sig(target).parameters.clone();
                let target_result = ctx.checker.get_return_type_of_signature(target);
                let target_result_carries = self.carries(ctx, target_result);
                for &source in src_signatures.iter() {
                    let source_parameters = ctx.checker.sig(source).parameters.clone();
                    for (i, &parameter) in target_parameters.iter().enumerate() {
                        let Some(&own) = source_parameters.get(i) else {
                            break;
                        };
                        let dest_ty = ctx.checker.get_type_of_symbol(parameter);
                        let own_ty = ctx.checker.get_type_of_symbol(own);
                        if self.carries(ctx, dest_ty) && !self.carries(ctx, own_ty) {
                            return Err(Reason::Escapes);
                        }
                    }
                    let result = ctx.checker.get_return_type_of_signature(source);
                    if self.carries(ctx, result) && !target_result_carries {
                        return Err(Reason::Escapes);
                    }
                }
            }
        }
        // Methods of application object types, one level deep. Built-in
        // collection methods are specified and never bivariant overrides.
        let src_flags = ctx.checker.ty(src).flags();
        let dest_flags = ctx.checker.ty(dest).flags();
        let dest_symbol = ctx.checker.ty(dest).symbol;
        let application = dest_symbol.is_some()
            && ctx
                .checker
                .sym(dest_symbol)
                .declarations
                .iter()
                .any(|&d| ctx.file_of_node(d).is_some());
        if application
            && src_flags.intersects(TypeFlags::Object)
            && dest_flags.intersects(TypeFlags::Object)
        {
            let properties = ctx.checker.get_properties_of_type(dest);
            for &property in properties.iter() {
                let dest_ty = ctx.checker.get_type_of_symbol(property);
                if ctx
                    .checker
                    .get_signatures_of_type(dest_ty, SignatureKind::Call)
                    .is_empty()
                {
                    continue;
                }
                let property_info = ctx.checker.sym(property);
                let name = property_info.name.as_str();
                let own = ctx.checker.get_property_of_type(src, name);
                if own.is_nil() {
                    continue;
                }
                let own_ty = ctx.checker.get_type_of_symbol(own);
                self.compatible(ctx, own_ty, dest_ty, depth + 1)?;
            }
        }
        Ok(())
    }

    /// The context of an expression whose type carries the domain.
    fn value_context(&mut self, ctx: &mut Ctx, node: Node, ty: TypeId) -> Proven<()> {
        let outer = walk_up_value(node);
        let holder = outer.parent();
        match holder.kind() {
            SyntaxKind::ExpressionStatement
            | SyntaxKind::VoidExpression
            | SyntaxKind::TypeOfExpression
            | SyntaxKind::AwaitExpression
            | SyntaxKind::IfStatement
            | SyntaxKind::WhileStatement
            | SyntaxKind::DoStatement
            | SyntaxKind::ConditionalExpression
            | SyntaxKind::CaseClause
            | SyntaxKind::VariableDeclaration
            | SyntaxKind::Parameter
            | SyntaxKind::PropertyDeclaration
            | SyntaxKind::PropertyAssignment
            | SyntaxKind::ShorthandPropertyAssignment
            | SyntaxKind::SpreadAssignment
            | SyntaxKind::ArrayLiteralExpression
            | SyntaxKind::ReturnStatement
            | SyntaxKind::ArrowFunction
            | SyntaxKind::NewExpression
            | SyntaxKind::AsExpression
            | SyntaxKind::TypeAssertionExpression => Ok(()),
            SyntaxKind::ForStatement => Ok(()),
            SyntaxKind::SwitchStatement => Ok(()),
            SyntaxKind::PrefixUnaryExpression
                if holder.operator() == SyntaxKind::ExclamationToken =>
            {
                Ok(())
            }
            SyntaxKind::BinaryExpression => match holder.operator_token().kind() {
                SyntaxKind::EqualsEqualsEqualsToken
                | SyntaxKind::ExclamationEqualsEqualsToken
                | SyntaxKind::EqualsEqualsToken
                | SyntaxKind::ExclamationEqualsToken
                | SyntaxKind::AmpersandAmpersandToken
                | SyntaxKind::BarBarToken
                | SyntaxKind::QuestionQuestionToken
                | SyntaxKind::CommaToken
                | SyntaxKind::EqualsToken
                | SyntaxKind::BarBarEqualsToken
                | SyntaxKind::AmpersandAmpersandEqualsToken
                | SyntaxKind::QuestionQuestionEqualsToken => Ok(()),
                SyntaxKind::InKeyword if holder.right() == outer => Ok(()),
                SyntaxKind::InstanceOfKeyword if holder.left() == outer => Ok(()),
                _ => Err(Reason::ObservableOperation),
            },
            SyntaxKind::CallExpression => {
                if holder.expression() == outer {
                    return Ok(());
                }
                Ok(())
            }
            SyntaxKind::SpreadElement => {
                let container = holder.parent();
                if container.kind() == SyntaxKind::ArrayLiteralExpression
                    && library_iterable(ctx, ty)
                {
                    Ok(())
                } else {
                    Err(Reason::Reflection)
                }
            }
            SyntaxKind::PropertyAccessExpression if holder.expression() == outer => {
                if is_callee(holder) {
                    self.method_call(ctx, holder, ty)
                } else if holder.parent().kind() == SyntaxKind::DeleteExpression {
                    Err(Reason::ObservableOperation)
                } else {
                    Ok(())
                }
            }
            SyntaxKind::ElementAccessExpression if holder.expression() == outer => {
                let argument = skip_outer(holder.argument_expression());
                if holder.parent().kind() == SyntaxKind::DeleteExpression {
                    return Err(Reason::ObservableOperation);
                }
                if is_callee(holder) {
                    return Err(Reason::ObservableOperation);
                }
                let apparent = ctx.checker.get_non_nullable_type(ty);
                match argument.kind() {
                    SyntaxKind::NumericLiteral
                    | SyntaxKind::StringLiteral
                    | SyntaxKind::NoSubstitutionTemplateLiteral => Ok(()),
                    _ if ctx.checker.is_array_type(apparent)
                        || ctx.checker.is_tuple_type(apparent) =>
                    {
                        Ok(())
                    }
                    // A computed key could read the tag of a domain object;
                    // containers without the tag only yield their elements.
                    _ if !self.tag_of_type(ctx, apparent)? => Ok(()),
                    _ => Err(Reason::DynamicAccess),
                }
            }
            SyntaxKind::ForOfStatement if holder.expression() == outer => {
                if library_iterable(ctx, ty) {
                    Ok(())
                } else {
                    Err(Reason::CustomIterator)
                }
            }
            SyntaxKind::ForOfStatement => Ok(()),
            SyntaxKind::ForInStatement => Err(Reason::Reflection),
            _ => Err(Reason::Escapes),
        }
    }

    /// `value.method(...)` passes the value as `this`.
    fn method_call(&mut self, ctx: &mut Ctx, access: Node, ty: TypeId) -> Proven<()> {
        let receiver = ctx.checker.get_non_nullable_type(ty);
        let apparent = ctx.checker.get_apparent_type(receiver);
        if library_collection(ctx, apparent) {
            return Ok(());
        }
        let call = walk_up_transparent(access).parent();
        if call.kind() != SyntaxKind::CallExpression {
            return Err(Reason::ObservableOperation);
        }
        let signature = ctx.checker.get_resolved_signature_exported(call);
        if signature.is_nil() {
            return Err(Reason::UnresolvedCall);
        }
        let declaration = ctx.checker.sig(signature).declaration;
        // A class method sees `this` as its class, which the flow check
        // follows like any other carrying value.
        if declaration.kind() == SyntaxKind::MethodDeclaration
            && matches!(
                declaration.parent().kind(),
                SyntaxKind::ClassDeclaration | SyntaxKind::ClassExpression
            )
            && ctx.file_of_node(declaration).is_some()
        {
            return Ok(());
        }
        Err(Reason::ObservableOperation)
    }
}

/// The outermost type expression or type member around a type node.
fn outermost_type(mut node: Node) -> Node {
    loop {
        let parent = node.parent();
        let continues = is_type_node(parent)
            || matches!(
                parent.kind(),
                SyntaxKind::PropertySignature
                    | SyntaxKind::MethodSignature
                    | SyntaxKind::IndexSignature
                    | SyntaxKind::CallSignature
                    | SyntaxKind::ConstructSignature
                    | SyntaxKind::TypeParameter
            )
            || (parent.kind() == SyntaxKind::Parameter && is_type_node(parent.parent()));
        if !continues || parent.is_nil() {
            return node;
        }
        node = parent;
    }
}

/// The promised type of an async function's declared return type.
fn awaited(ctx: &mut Ctx, ty: TypeId) -> TypeId {
    let symbol = ctx.checker.ty(ty).symbol;
    if symbol.is_some()
        && ctx.checker.sym(symbol).name.as_str() == "Promise"
        && ctx
            .checker
            .ty(ty)
            .object_flags()
            .intersects(ObjectFlags::Reference)
        && let Some(&inner) = ctx.checker.get_type_arguments(ty).first()
    {
        return inner;
    }
    ty
}

/// Whether a type is a built-in collection declared by the library.
fn library_collection(ctx: &mut Ctx, ty: TypeId) -> bool {
    let flags = ctx.checker.ty(ty).flags();
    if flags.intersects(TypeFlags::Union | TypeFlags::Intersection) {
        let parts = ctx.checker.ty(ty).types_list();
        return !parts.is_empty() && parts.iter().all(|&p| library_collection(ctx, p));
    }
    if ctx.checker.is_array_type(ty) || ctx.checker.is_tuple_type(ty) {
        return true;
    }
    let symbol = ctx.checker.ty(ty).symbol;
    if symbol.is_nil() {
        return false;
    }
    let symbol_info = ctx.checker.sym(symbol);
    let name = symbol_info.name.as_str();
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    COLLECTIONS.contains(&name)
        && !declarations.is_empty()
        && declarations.iter().all(|&d| ctx.file_of_node(d).is_none())
}

fn library_iterable(ctx: &mut Ctx, ty: TypeId) -> bool {
    let ty = ctx.checker.get_non_nullable_type(ty);
    let apparent = ctx.checker.get_apparent_type(ty);
    library_collection(ctx, apparent)
}

/// Whether an identifier resolves to a library global, not an application
/// binding that shadows it.
fn global_library_symbol(ctx: &mut Ctx, identifier: Node) -> bool {
    let symbol = ctx.checker.get_symbol_at_location(identifier);
    if symbol.is_nil() {
        return false;
    }
    let declarations = ctx.checker.sym(symbol).declarations.to_vec();
    !declarations.is_empty() && declarations.iter().all(|&d| ctx.file_of_node(d).is_none())
}

/// Primitive and literal values carry no object identity or properties.
fn plain_data(ctx: &mut Ctx, ty: TypeId) -> bool {
    let flags = ctx.checker.ty(ty).flags();
    if flags.intersects(TypeFlags::Union) {
        let parts = ctx.checker.ty(ty).types_list();
        return parts.iter().all(|&p| plain_data(ctx, p));
    }
    !flags.intersects(
        TypeFlags::Object
            | TypeFlags::Any
            | TypeFlags::Unknown
            | TypeFlags::NonPrimitive
            | TypeFlags::Instantiable
            | TypeFlags::Intersection,
    )
}

/// Whether `access` is the callee of a call or tagged template.
fn is_callee(access: Node) -> bool {
    let outer = walk_up_transparent(access);
    let holder = outer.parent();
    (holder.kind() == SyntaxKind::CallExpression && holder.expression() == outer)
        || (holder.kind() == SyntaxKind::TaggedTemplateExpression && holder.tag() == outer)
}

/// Removes parentheses, non-null assertions and `satisfies`, which neither
/// change the value nor its type.
fn skip_transparent(mut node: Node) -> Node {
    while matches!(
        node.kind(),
        SyntaxKind::ParenthesizedExpression
            | SyntaxKind::NonNullExpression
            | SyntaxKind::SatisfiesExpression
    ) {
        node = node.expression();
    }
    node
}

fn walk_up_transparent(mut node: Node) -> Node {
    while matches!(
        node.parent().kind(),
        SyntaxKind::ParenthesizedExpression
            | SyntaxKind::NonNullExpression
            | SyntaxKind::SatisfiesExpression
    ) {
        node = node.parent();
    }
    node
}

/// Like [`walk_up_transparent`], but also through `as`; the assertion's
/// own flow is an edge.
fn walk_up_value(node: Node) -> Node {
    walk_up_transparent(node)
}

/// Whether the node is an expression whose value the flow check follows.
fn is_value_expression(node: Node) -> bool {
    match node.kind() {
        SyntaxKind::Identifier => identifier_value(node),
        SyntaxKind::ThisKeyword
        | SyntaxKind::PropertyAccessExpression
        | SyntaxKind::ElementAccessExpression
        | SyntaxKind::CallExpression
        | SyntaxKind::NewExpression
        | SyntaxKind::ObjectLiteralExpression
        | SyntaxKind::ArrayLiteralExpression
        | SyntaxKind::ConditionalExpression
        | SyntaxKind::AwaitExpression
        | SyntaxKind::FunctionExpression
        | SyntaxKind::ArrowFunction
        | SyntaxKind::ClassExpression
        | SyntaxKind::TaggedTemplateExpression
        | SyntaxKind::AsExpression
        | SyntaxKind::TypeAssertionExpression => !in_type(node),
        SyntaxKind::BinaryExpression => matches!(
            node.operator_token().kind(),
            SyntaxKind::AmpersandAmpersandToken
                | SyntaxKind::BarBarToken
                | SyntaxKind::QuestionQuestionToken
                | SyntaxKind::CommaToken
                | SyntaxKind::EqualsToken
                | SyntaxKind::BarBarEqualsToken
                | SyntaxKind::AmpersandAmpersandEqualsToken
                | SyntaxKind::QuestionQuestionEqualsToken
        ),
        _ => false,
    }
}

fn in_type(node: Node) -> bool {
    is_part_of_type_node(node) || is_part_of_type_query(node)
}

/// Identifiers in value positions, including shorthand property values.
fn identifier_value(node: Node) -> bool {
    if in_type(node) {
        return false;
    }
    let parent = node.parent();
    match parent.kind() {
        SyntaxKind::ShorthandPropertyAssignment => parent.name() == node,
        SyntaxKind::PropertyAccessExpression => parent.expression() == node,
        SyntaxKind::QualifiedName
        | SyntaxKind::PropertyAssignment
        | SyntaxKind::PropertySignature
        | SyntaxKind::PropertyDeclaration
        | SyntaxKind::MethodDeclaration
        | SyntaxKind::MethodSignature
        | SyntaxKind::GetAccessor
        | SyntaxKind::SetAccessor
        | SyntaxKind::EnumMember
        | SyntaxKind::JsxAttribute
        | SyntaxKind::ImportSpecifier
        | SyntaxKind::ExportSpecifier
        | SyntaxKind::ImportClause
        | SyntaxKind::NamespaceImport
        | SyntaxKind::NamespaceExport
        | SyntaxKind::LabeledStatement
        | SyntaxKind::BreakStatement
        | SyntaxKind::ContinueStatement
        | SyntaxKind::MetaProperty => false,
        SyntaxKind::BindingElement => false,
        _ => !is_declaration_name(node),
    }
}

/// The type of an expression as its context sees it.
fn expression_type(ctx: &mut Ctx, node: Node) -> TypeId {
    if node.kind() == SyntaxKind::Identifier
        && node.parent().kind() == SyntaxKind::ShorthandPropertyAssignment
    {
        let symbol = ctx
            .checker
            .get_shorthand_assignment_value_symbol(node.parent());
        if symbol.is_nil() {
            return TypeId::NIL;
        }
        return ctx.checker.get_type_of_symbol(symbol);
    }
    if node.kind() == SyntaxKind::SpreadElement {
        return ctx.checker.get_type_at_location(node.expression());
    }
    ctx.checker.get_type_at_location(node)
}
