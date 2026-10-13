// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Shared closed-call-graph facts for `signature-reduction` and
//! `function-specialization`.
//!
//! A function is *closed* when every runtime reference to it is the callee of
//! a direct call `f(...)` (or a module alias that does not observe the value)
//! and nothing outside the indexed application TypeScript can reach it. Then
//! its value never flows anywhere: `.length`, `.name`, identity, `this` and
//! `arguments` of a caller cannot observe its signature, and the complete
//! set of call sites is known.
use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet};

use super::super::analysis::{Ctx, Ref, RefKind, skip_outer, walk_up_outer};
use crate::{
    compiler::api::*,
    plan::{Edit, FileEdit, Reason},
};

/// A named function binding that a closed call graph may own.
pub(super) struct Function {
    pub file: usize,
    /// The function-like node (declaration, arrow or function expression).
    pub node: Node,
    /// The statement that declares the binding.
    pub statement: Node,
    pub name: Node,
    pub symbol: SymbolId,
    /// Stable identity: `<file>:<name>#<ordinal among same names>`.
    pub ident: String,
    /// Reachable through a module export (modifier or export specifier).
    pub exported: bool,
}

impl Function {
    pub fn is_declaration(&self) -> bool {
        self.node.kind() == SyntaxKind::FunctionDeclaration
    }

    pub fn parameters(&self) -> Vec<Node> {
        self.node.parameters().to_vec()
    }
}

#[derive(Clone, Copy)]
pub(super) struct CallSite {
    pub file: usize,
    pub call: Node,
}

/// Every named function binding of application TypeScript in deterministic
/// order: function declarations with bodies and `const` bindings initialized
/// directly with an arrow function or function expression.
pub(super) fn functions(ctx: &mut Ctx) -> Vec<Function> {
    let mut result = Vec::new();
    for file in 0..ctx.files.len() {
        if ctx.files[file].js {
            continue;
        }
        let mut ordinals: HashMap<String, usize> = HashMap::new();
        let nodes = ctx.files[file].nodes.clone();
        for node in nodes {
            let (function, statement, name) = match node.kind() {
                SyntaxKind::FunctionDeclaration
                    if node.name().is_some() && node.body().is_some() =>
                {
                    (node, node, node.name())
                }
                SyntaxKind::VariableDeclaration => {
                    let init = node.initializer();
                    let list = node.parent();
                    if node.name().kind() != SyntaxKind::Identifier
                        || init.is_nil()
                        || !matches!(
                            init.kind(),
                            SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression
                        )
                        || node.type_().is_some()
                        || node.exclamation_token().is_some()
                        || list.kind() != SyntaxKind::VariableDeclarationList
                        || !list.flags().intersects(NodeFlags::Const)
                        || list.parent().kind() != SyntaxKind::VariableStatement
                    {
                        continue;
                    }
                    (init, list.parent(), node.name())
                }
                _ => continue,
            };
            let text = name.text().to_owned();
            let ordinal = ordinals.entry(text.clone()).or_default();
            let ident = format!("{}:{}#{}", ctx.files[file].name, text, ordinal);
            *ordinal += 1;
            let symbol = ctx.resolve(name);
            if symbol.is_nil() {
                continue;
            }
            let exported = has_syntactic_modifier(statement, ModifierFlags::Export)
                || ctx.refs(symbol).iter().any(is_alias);
            result.push(Function {
                file,
                node: function,
                statement,
                name,
                symbol,
                ident,
                exported,
            });
        }
    }
    result
}

/// Modules whose exports are observable as a namespace object: targets of
/// `import * as`, `export * as`, dynamic `import()`, and everything they
/// re-export with `export *`. A namespace object exposes every export as a
/// property without an identifier reference to it.
pub(super) struct Observable {
    modules: HashSet<usize>,
    all: bool,
}

impl Observable {
    pub fn compute(ctx: &mut Ctx) -> Self {
        let mut modules = HashSet::new();
        let mut all = false;
        let mut star_edges: BTreeMap<usize, Vec<usize>> = BTreeMap::new();
        for file in 0..ctx.files.len() {
            let nodes = ctx.files[file].nodes.clone();
            for node in nodes {
                match node.kind() {
                    SyntaxKind::ImportDeclaration => {
                        let clause = node.import_clause();
                        if clause.is_some()
                            && clause.named_bindings().is_some()
                            && clause.named_bindings().kind() == SyntaxKind::NamespaceImport
                            && let Some(target) = target_module(ctx, node.module_specifier())
                        {
                            modules.insert(target);
                        }
                    }
                    SyntaxKind::ExportDeclaration => {
                        let specifier = node.module_specifier();
                        if specifier.is_nil() {
                            continue;
                        }
                        let Some(target) = target_module(ctx, specifier) else {
                            continue;
                        };
                        let clause = node.export_clause();
                        if clause.is_nil() {
                            star_edges.entry(file).or_default().push(target);
                        } else if clause.kind() == SyntaxKind::NamespaceExport {
                            modules.insert(target);
                        }
                    }
                    SyntaxKind::CallExpression
                        if node.expression().kind() == SyntaxKind::ImportKeyword =>
                    {
                        let argument = node.arguments().first();
                        match argument {
                            Some(a)
                                if matches!(
                                    a.kind(),
                                    SyntaxKind::StringLiteral
                                        | SyntaxKind::NoSubstitutionTemplateLiteral
                                ) =>
                            {
                                if let Some(target) = target_module(ctx, a) {
                                    modules.insert(target);
                                }
                            }
                            Some(a) if a.kind() == SyntaxKind::TemplateExpression => {
                                match template_matches(ctx, file, a) {
                                    Some(matched) => modules.extend(matched),
                                    None => all = true,
                                }
                            }
                            _ => all = true,
                        }
                    }
                    _ => {}
                }
            }
        }
        let mut work: Vec<usize> = modules.iter().copied().collect();
        while let Some(module) = work.pop() {
            for &target in star_edges.get(&module).map_or(&[][..], Vec::as_slice) {
                if modules.insert(target) {
                    work.push(target);
                }
            }
        }
        tracing::debug!(
            namespace_modules = modules.len(),
            unresolved_dynamic_import = all,
            "namespace-observable modules computed"
        );
        Self { modules, all }
    }

    pub fn contains(&self, file: usize) -> bool {
        self.all || self.modules.contains(&file)
    }
}

/// Application modules a template `import()` specifier such as
/// `./locales/${code}.json` may load: paths under the static relative
/// prefix that end with the static suffix (with or without an extension).
/// `None` when the prefix is not relative.
fn template_matches(ctx: &Ctx, file: usize, template: Node) -> Option<Vec<usize>> {
    let prefix = template.head().text();
    let spans = template.template_spans().nodes();
    let suffix = spans.last()?.literal().text();
    if !(prefix.starts_with("./") || prefix.starts_with("../")) {
        return None;
    }
    let importer = std::path::Path::new(&ctx.files[file].name).parent()?;
    let mut base: Vec<&str> = importer
        .to_str()?
        .split('/')
        .filter(|c| !c.is_empty())
        .collect();
    let (directory, partial) = prefix.rsplit_once('/')?;
    for component in directory.split('/') {
        match component {
            "" | "." => {}
            ".." => {
                base.pop()?;
            }
            other => base.push(other),
        }
    }
    let root = format!("/{}/{}", base.join("/"), partial);
    let mut matched = Vec::new();
    for (index, info) in ctx.files.iter().enumerate() {
        let path = info.name.as_str();
        if !path.starts_with(&root) {
            continue;
        }
        let stem = path.rsplit_once('.').map_or(path, |(stem, _)| stem);
        if suffix.is_empty() || path.ends_with(suffix) || stem.ends_with(suffix) {
            matched.push(index);
        }
    }
    Some(matched)
}

/// Application module index a module specifier resolves to.
pub(super) fn target_module(ctx: &mut Ctx, specifier: Node) -> Option<usize> {
    if specifier.is_nil() {
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

/// Whether a reference is a module alias: an import or export specifier
/// or import clause. Export specifier names count as declarations in the
/// shared index, but they only re-expose the binding.
pub(super) fn is_alias(r: &Ref) -> bool {
    r.kind == RefKind::Alias
        || (r.kind == RefKind::Declaration && r.node.parent().kind() == SyntaxKind::ExportSpecifier)
}

/// Whether a reference is the value read of a shorthand property `{ x }`,
/// which the shared index records as the property's declaration name.
pub(super) fn is_shorthand_read(r: &Ref) -> bool {
    r.kind == RefKind::Declaration && {
        let parent = r.node.parent();
        parent.kind() == SyntaxKind::ShorthandPropertyAssignment && parent.name() == r.node
    }
}

/// Whether a reference reads a value at runtime: a plain read or a
/// shorthand property value.
pub(super) fn is_value_read(r: &Ref) -> bool {
    r.kind == RefKind::Read || is_shorthand_read(r)
}

/// Proves that `function` is closed and returns its complete call-site set.
pub(super) fn closure(
    ctx: &mut Ctx,
    function: &Function,
    observable: &Observable,
) -> Result<Vec<CallSite>, Reason> {
    let symbol = function.symbol;
    if ctx.public.contains(&symbol) {
        return Err(Reason::PublicExport);
    }
    if !ctx.is_internal(symbol) {
        return Err(Reason::ExternalBoundary);
    }
    if ctx.checker.sym(symbol).declarations.len() != 1 {
        return Err(Reason::DeclarationMerging);
    }
    if function.statement.flags().intersects(NodeFlags::Ambient)
        || has_syntactic_modifier(function.statement, ModifierFlags::Ambient)
    {
        return Err(Reason::DeclarationMerging);
    }
    if function.exported && observable.contains(function.file) {
        return Err(Reason::Escapes);
    }
    // Script globals and namespace members are properties of objects that
    // code can enumerate without naming the binding.
    if !is_external_module(ctx.files[function.file].node) {
        return Err(Reason::Escapes);
    }
    let mut ancestor = function.statement.parent();
    while ancestor.is_some() {
        if ancestor.kind() == SyntaxKind::ModuleDeclaration {
            return Err(Reason::Escapes);
        }
        ancestor = ancestor.parent();
    }
    let mut calls = Vec::new();
    let mut declared = false;
    for r in ctx.refs(symbol).to_vec() {
        match r.kind {
            _ if is_alias(&r) => {}
            RefKind::Declaration => {
                if is_shorthand_read(&r) {
                    return Err(Reason::Escapes);
                }
                if r.node != function.name {
                    return Err(Reason::DeclarationMerging);
                }
                declared = true;
            }
            RefKind::Alias => {}
            RefKind::Call => {
                let call = walk_up_outer(r.node).parent();
                if call.kind() != SyntaxKind::CallExpression
                    || call
                        .arguments()
                        .iter()
                        .any(|a| a.kind() == SyntaxKind::SpreadElement)
                {
                    return Err(Reason::UnsupportedSyntax);
                }
                calls.push(CallSite { file: r.file, call });
            }
            RefKind::Type => return Err(Reason::TypeConstraint),
            RefKind::Write => return Err(Reason::Mutation),
            RefKind::New => return Err(Reason::ObservableOperation),
            RefKind::Read => return Err(Reason::Escapes),
        }
    }
    if !declared {
        return Err(Reason::UnsupportedSyntax);
    }
    // `arguments` observes the actual argument list; a `this` parameter
    // shifts runtime positions.
    let mut nodes = Vec::new();
    crate::compiler::collect(function.node, &mut nodes);
    if nodes
        .iter()
        .any(|n| n.kind() == SyntaxKind::Identifier && n.text() == "arguments")
    {
        return Err(Reason::ObservableArity);
    }
    if function
        .parameters()
        .iter()
        .any(|p| p.name().kind() == SyntaxKind::Identifier && p.name().text() == "this")
    {
        return Err(Reason::UnsupportedSyntax);
    }
    if function
        .parameters()
        .iter()
        .any(|p| !p.decorators().is_empty())
    {
        return Err(Reason::Decorator);
    }
    calls.sort_by_key(|c| (c.file, c.call.pos()));
    Ok(calls)
}

/// Whether `node` lies inside `outer` (inclusive).
pub(super) fn within(node: Node, outer: Node) -> bool {
    Ctx::start(node) >= Ctx::start(outer) && node.end() <= outer.end()
}

/// Whether the type annotation of `parameter` mentions a type parameter of
/// `function`. Removing such a parameter would change generic inference.
pub(super) fn mentions_type_parameter(function: Node, parameter: Node) -> bool {
    let names: HashSet<&str> = function
        .type_parameters()
        .iter()
        .map(|t| t.name().text())
        .collect();
    if names.is_empty() {
        return false;
    }
    let ty = parameter.type_();
    if ty.is_nil() {
        return false;
    }
    let mut nodes = Vec::new();
    crate::compiler::collect(ty, &mut nodes);
    nodes
        .iter()
        .any(|n| n.kind() == SyntaxKind::Identifier && names.contains(n.text()))
}

/// Runtime value of a primitive literal expression.
#[derive(Clone, Debug, PartialEq)]
pub(super) enum Literal {
    Str(String),
    Num(f64),
    Bool(bool),
    Null,
    BigInt(String),
}

impl Literal {
    /// Parses a literal expression, looking through type-only wrappers.
    pub fn of(node: Node) -> Option<Literal> {
        let node = skip_outer(node);
        match node.kind() {
            SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral => {
                Some(Literal::Str(node.text().to_owned()))
            }
            SyntaxKind::NumericLiteral => parse_number(node.text()).map(Literal::Num),
            SyntaxKind::BigIntLiteral => Some(Literal::BigInt(normalize_bigint(node.text())?)),
            SyntaxKind::TrueKeyword => Some(Literal::Bool(true)),
            SyntaxKind::FalseKeyword => Some(Literal::Bool(false)),
            SyntaxKind::NullKeyword => Some(Literal::Null),
            SyntaxKind::PrefixUnaryExpression if node.operator() == SyntaxKind::MinusToken => {
                let operand = node.operand();
                match operand.kind() {
                    SyntaxKind::NumericLiteral => {
                        parse_number(operand.text()).map(|n| Literal::Num(-n))
                    }
                    _ => None,
                }
            }
            _ => None,
        }
    }

    /// Stable key distinguishing runtime values. `-0` and `0` are distinct
    /// values, and NaN never appears in literal syntax.
    pub fn key(&self) -> String {
        match self {
            Literal::Str(s) => format!("s:{s}"),
            Literal::Num(n) => format!("n:{:x}", n.to_bits()),
            Literal::Bool(b) => format!("b:{b}"),
            Literal::Null => "null".into(),
            Literal::BigInt(b) => format!("i:{b}"),
        }
    }

    pub fn truthy(&self) -> bool {
        match self {
            Literal::Str(s) => !s.is_empty(),
            Literal::Num(n) => *n != 0.0 && !n.is_nan(),
            Literal::Bool(b) => *b,
            Literal::Null => false,
            Literal::BigInt(b) => b != "0",
        }
    }

    /// `===` between two primitive values.
    pub fn strict_equals(&self, other: &Literal) -> bool {
        match (self, other) {
            (Literal::Num(a), Literal::Num(b)) => a == b,
            (a, b) => a == b,
        }
    }
}

fn parse_number(text: &str) -> Option<f64> {
    let text: String = text.chars().filter(|&c| c != '_').collect();
    let lower = text.to_ascii_lowercase();
    let radix = |digits: &str, radix: u32| {
        u64::from_str_radix(digits, radix)
            .ok()
            .filter(|&v| v < (1u64 << 53))
            .map(|v| v as f64)
    };
    if let Some(digits) = lower.strip_prefix("0x") {
        radix(digits, 16)
    } else if let Some(digits) = lower.strip_prefix("0o") {
        radix(digits, 8)
    } else if let Some(digits) = lower.strip_prefix("0b") {
        radix(digits, 2)
    } else if lower.len() > 1 && lower.starts_with('0') && !lower.contains(['.', 'e']) {
        // Legacy octal-like literals are rejected in modules; be safe.
        None
    } else {
        text.parse::<f64>().ok().filter(|n| n.is_finite())
    }
}

fn normalize_bigint(text: &str) -> Option<String> {
    let digits: String = text
        .trim_end_matches('n')
        .chars()
        .filter(|&c| c != '_')
        .collect();
    if digits.chars().all(|c| c.is_ascii_digit()) && !digits.is_empty() {
        let trimmed = digits.trim_start_matches('0');
        Some(if trimmed.is_empty() { "0" } else { trimmed }.to_owned())
    } else {
        None
    }
}

/// Edits deleting the list items flagged in `delete` from a comma-separated
/// list whose items span `items` in `text`. Items that stay keep their exact
/// text, so edits nested inside them remain valid. Returns `None` when the
/// closing delimiter cannot be located safely.
pub(super) fn delete_items(
    file: &str,
    text: &str,
    items: &[(usize, usize)],
    delete: &[bool],
) -> Option<Vec<FileEdit>> {
    let mut edits = Vec::new();
    let Some(last_kept) = (0..items.len()).rev().find(|&i| !delete[i]) else {
        if items.is_empty() {
            return Some(edits);
        }
        // Everything goes: delete through any trailing comma up to `)`.
        let mut end = items[items.len() - 1].1;
        let bytes = text.as_bytes();
        while end < bytes.len() && (bytes[end].is_ascii_whitespace() || bytes[end] == b',') {
            end += 1;
        }
        if bytes.get(end) != Some(&b')') {
            return None;
        }
        edits.push(FileEdit::delete(file, items[0].0, end));
        return Some(edits);
    };
    for i in 0..items.len() {
        if !delete[i] {
            continue;
        }
        if i < last_kept {
            edits.push(FileEdit::delete(file, items[i].0, items[i + 1].0));
        } else {
            edits.push(FileEdit::delete(file, items[i - 1].1, items[i].1));
        }
    }
    Some(edits)
}

/// Normalizes a bundle's edits: merges an insertion with a replacement that
/// starts at the same offset, drops edits nested inside a replaced range
/// (their text no longer exists) and rejects any remaining conflict.
pub(super) fn finalize(edits: Vec<FileEdit>) -> Option<Vec<FileEdit>> {
    let mut by_file: BTreeMap<String, Vec<Edit>> = BTreeMap::new();
    for edit in edits {
        by_file.entry(edit.file).or_default().push(edit.edit);
    }
    let mut result = Vec::new();
    for (file, mut edits) in by_file {
        edits.sort();
        edits.dedup();
        // Drop edits strictly nested in another replaced range.
        let ranges: Vec<(usize, usize)> = edits
            .iter()
            .filter(|e| e.end > e.start)
            .map(|e| (e.start, e.end))
            .collect();
        edits.retain(|e| {
            !ranges.iter().any(|&(s, t)| {
                (s, t) != (e.start, e.end)
                    && s <= e.start
                    && e.end <= t
                    && !(e.start == e.end && (e.start == s || e.start == t))
            })
        });
        // Merge an insertion into a replacement starting at the same place.
        let mut merged: Vec<Edit> = Vec::new();
        for edit in edits {
            if let Some(previous) = merged.last_mut()
                && previous.start == edit.start
            {
                if previous.start == previous.end && edit.end > edit.start {
                    previous.end = edit.end;
                    previous.replacement.push_str(&edit.replacement);
                    continue;
                }
                return None;
            }
            merged.push(edit);
        }
        for pair in merged.windows(2) {
            if pair[1].start < pair[0].end {
                return None;
            }
        }
        result.extend(merged.into_iter().map(|edit| FileEdit {
            file: file.clone(),
            edit,
        }));
    }
    Some(result)
}

/// Text of an expression that TypeScript would flag as an unused
/// side-effect-free comma operand, made explicit with `void`.
pub(super) fn discarded(ctx: &Ctx, node: Node) -> (String, String) {
    let side_effect_free = ctx.checker.is_side_effect_free(node);
    if !side_effect_free {
        return (String::new(), String::new());
    }
    let primary = matches!(
        node.kind(),
        SyntaxKind::Identifier
            | SyntaxKind::ParenthesizedExpression
            | SyntaxKind::ArrayLiteralExpression
            | SyntaxKind::ObjectLiteralExpression
            | SyntaxKind::TemplateExpression
            | SyntaxKind::NoSubstitutionTemplateLiteral
            | SyntaxKind::StringLiteral
            | SyntaxKind::NumericLiteral
    );
    if primary {
        ("void ".into(), String::new())
    } else {
        ("void (".into(), ")".into())
    }
}

/// Whether an expression can be moved after later arguments without
/// changing its value: a primitive literal.
pub(super) fn is_movable_literal(node: Node) -> bool {
    Literal::of(node).is_some() && skip_outer(node) == node
}

/// Whether `as const` leaves the type of an array element unchanged: the
/// element is not itself a literal form that a const context narrows or
/// makes readonly.
fn const_neutral(node: Node) -> bool {
    let node = skip_parentheses(node);
    match node.kind() {
        SyntaxKind::StringLiteral
        | SyntaxKind::NoSubstitutionTemplateLiteral
        | SyntaxKind::TemplateExpression
        | SyntaxKind::NumericLiteral
        | SyntaxKind::BigIntLiteral
        | SyntaxKind::TrueKeyword
        | SyntaxKind::FalseKeyword
        | SyntaxKind::PrefixUnaryExpression
        | SyntaxKind::ArrayLiteralExpression
        | SyntaxKind::ObjectLiteralExpression
        | SyntaxKind::ConditionalExpression
        | SyntaxKind::ArrowFunction
        | SyntaxKind::FunctionExpression
        | SyntaxKind::ClassExpression => false,
        SyntaxKind::BinaryExpression => !matches!(
            node.operator_token().kind(),
            SyntaxKind::BarBarToken
                | SyntaxKind::AmpersandAmpersandToken
                | SyntaxKind::QuestionQuestionToken
                | SyntaxKind::CommaToken
        ),
        _ => true,
    }
}

/// Rewrites one call so that the arguments flagged in `remove` are no longer
/// passed while every removed argument with effects still evaluates exactly
/// once in its original order. Kept arguments keep their exact text, so
/// edits nested inside them stay valid. `None` when no order-preserving form
/// exists.
pub(super) fn rewrite_arguments(
    ctx: &mut Ctx,
    site: CallSite,
    remove: &[bool],
    callee_hoisted: bool,
) -> Option<Vec<FileEdit>> {
    let file = site.file;
    let name = ctx.files[file].name.clone();
    let args: Vec<Node> = site.call.arguments().to_vec();
    let flag = |i: usize| remove.get(i).copied().unwrap_or(false);
    if !(0..args.len()).any(flag) {
        return Some(Vec::new());
    }
    let pure: Vec<bool> = args.iter().map(|&a| ctx.is_pure(a)).collect();
    let items: Vec<(usize, usize)> = args
        .iter()
        .map(|&a| (Ctx::start(a), a.end() as usize))
        .collect();
    let mut delete: Vec<bool> = (0..args.len()).map(|i| flag(i) && pure[i]).collect();
    let mut edits: Vec<FileEdit> = Vec::new();
    let kept: Vec<usize> = (0..args.len()).filter(|&i| !flag(i)).collect();
    let last_kept = kept.last().copied();
    // Each effectful removed argument becomes a discarded comma operand.
    let operand = |edits: &mut Vec<FileEdit>, r: usize, open_group: bool| {
        let (open, close) = discarded(ctx, args[r]);
        let prefix = if open_group { format!("({open}") } else { open };
        if !prefix.is_empty() {
            edits.push(FileEdit::insert(&name, items[r].0, prefix));
        }
        if !close.is_empty() {
            edits.push(FileEdit::insert(&name, items[r].1, close));
        }
    };
    // `(r1, r2, kept)`: the comma operator evaluates the removed arguments
    // first and yields the following kept argument.
    let mut group_closer: Option<usize> = None;
    let mut pending: Vec<usize> = Vec::new();
    for i in 0..args.len() {
        if flag(i) {
            if !pure[i] {
                pending.push(i);
            }
            continue;
        }
        if pending.is_empty() {
            continue;
        }
        for (n, &r) in pending.iter().enumerate() {
            operand(&mut edits, r, n == 0);
        }
        pending.clear();
        if Some(i) == last_kept {
            group_closer = Some(i);
        } else {
            edits.push(FileEdit::insert(&name, items[i].1, ")"));
        }
    }
    let trailing = pending;
    let closer = |edits: &mut Vec<FileEdit>, at: usize| {
        if let Some(i) = group_closer {
            edits.push(FileEdit::insert(&name, at.max(items[i].1), ")"));
        }
    };
    if trailing.is_empty() {
        closer(&mut edits, 0);
    } else {
        let last = *trailing.last().unwrap();
        match last_kept {
            Some(p) if group_closer.is_none() && is_movable_literal(args[p]) => {
                // `(r1, r2, literal)`: a literal evaluates without effects,
                // so moving it after the removed arguments is unobservable.
                let literal = ctx.text(file, args[p]).to_owned();
                delete[p] = true;
                for (n, &r) in trailing.iter().enumerate() {
                    operand(&mut edits, r, n == 0);
                }
                edits.push(FileEdit::insert(
                    &name,
                    items[last].1,
                    format!(", {literal})"),
                ));
            }
            Some(p) if const_neutral(args[p]) => {
                // `([kept, r1, r2] as const)[0]`: array elements evaluate in
                // order and the own element `0` yields the kept value.
                edits.push(FileEdit::insert(&name, items[p].0, "(["));
                let close = if group_closer.is_some() {
                    "] as const)[0])"
                } else {
                    "] as const)[0]"
                };
                edits.push(FileEdit::insert(&name, items[last].1, close));
            }
            None if callee_hoisted => {
                // `(r1, r2, f)()`: a hoisted function binding reads without
                // effects, so evaluating the callee after the arguments is
                // unobservable; the call still passes `this = undefined`.
                let callee = site.call.expression();
                if callee.kind() != SyntaxKind::Identifier
                    || site.call.question_dot_token().is_some()
                    || !site.call.type_arguments().is_empty()
                {
                    return None;
                }
                let callee_text = callee.text().to_owned();
                edits.push(FileEdit::replace(
                    &name,
                    Ctx::start(callee),
                    items[0].0,
                    "(",
                ));
                for &r in &trailing {
                    operand(&mut edits, r, false);
                }
                // Pure removed arguments after the last effectful one fall
                // inside this replacement and are dropped by `finalize`.
                edits.push(FileEdit::replace(
                    &name,
                    items[last].1,
                    site.call.end() as usize,
                    format!(", {callee_text})()"),
                ));
            }
            _ => return None,
        }
    }
    // Effectful removed arguments stay in place; pure removed ones go.
    let text = &ctx.files[file].text;
    edits.extend(delete_items(&name, text, &items, &delete)?);
    Some(edits)
}

/// Enclosing function-like node of `node` (not `node` itself).
pub(super) fn enclosing_function(node: Node) -> Node {
    let mut current = node.parent();
    while current.is_some() && !is_function_like(current) {
        if current.kind() == SyntaxKind::ClassStaticBlockDeclaration {
            return current;
        }
        current = current.parent();
    }
    current
}

/// Identifier texts mentioned anywhere in the given files.
pub(super) fn identifier_texts(ctx: &Ctx, files: &BTreeSet<usize>) -> HashSet<String> {
    let mut names = HashSet::new();
    for &file in files {
        for node in &ctx.files[file].nodes {
            if node.kind() == SyntaxKind::Identifier {
                names.insert(node.text().to_owned());
            }
        }
    }
    names
}
