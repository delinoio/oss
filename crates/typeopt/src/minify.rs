// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Optional SWC minification of edited bundle chunks (`--minify`).
//!
//! Bundle-mode transformations leave code that a minifier can shorten
//! further (folded branches such as `true?a:b`, single-use bindings, parameter
//! names exposed by inlining). After the pipeline, every edited ES module
//! chunk can be minified with SWC's compressor and mangler. The result is
//! kept only when it compresses smaller: Brotli (quality 11) must shrink and
//! gzip (level 9) must not grow, because minifying already-minified bundler
//! output sometimes compresses worse. Legal comments (`/*!`, `@license`,
//! `@preserve`) are kept, including those of declarations that compression
//! removes; other comments are dropped after the compressor has read
//! annotations such as `/*#__PURE__*/`.
//!
//! Observable names and arity are preserved like every typeopt
//! transformation: function and class names (`.name`, `constructor.name`),
//! names that anonymous functions and classes infer from the binding they
//! initialize or are assigned to, and unused parameters (`.length`).
use std::io::Write as _;

use rustc_hash::FxHashSet;
use swc_core::{
    atoms::Atom,
    common::{
        BytePos, FileName, GLOBALS, Globals, Mark, SourceMap, Spanned,
        comments::{Comment, Comments as _, SingleThreadedComments},
        sync::Lrc,
    },
    ecma::{
        ast::{
            AssignExpr, AssignPat, AssignTarget, CallExpr, Callee, EsVersion, Expr, ExprOrSpread,
            ExprStmt, Id, Ident, MemberExpr, MemberProp, ModuleItem, ObjectPatProp, Pat, Program,
            PropName, SimpleAssignTarget, Stmt, Tpl, VarDeclarator,
        },
        codegen::{
            Config, Emitter,
            text_writer::{JsWriter, omit_trailing_semi},
        },
        minifier::{
            optimize,
            option::{ExtraOptions, MangleOptions, MinifyOptions, terser::TerserCompressorOptions},
        },
        parser::{EsSyntax, Syntax, parse_file_as_module},
        transforms::base::{
            fixer::fixer,
            hygiene::Config as HygieneConfig,
            rename::{Renamer, renamer},
            resolver,
        },
        visit::{Visit, VisitMut, VisitMutWith, VisitWith},
    },
};

/// Names that anonymous functions and classes infer from the binding they
/// initialize or are assigned to (`const f = () => {}`, `x = class {}`,
/// `(p = function () {}) => p`). Renaming such a binding would change the
/// function's or class's `.name`, so the mangler keeps them.
#[derive(Default)]
struct InferredNames(Vec<Id>);

impl InferredNames {
    /// Sorted and deduplicated, for binary search.
    fn names(&self) -> Vec<Atom> {
        let mut names: Vec<Atom> = self.0.iter().map(|(name, _)| name.clone()).collect();
        names.sort();
        names.dedup();
        names
    }
}

/// SWC's hygiene renamer, except that bindings whose names functions or
/// classes infer keep their names; whatever collides with them is renamed
/// instead.
struct InferredHygiene {
    inferred: FxHashSet<Id>,
}

impl Renamer for InferredHygiene {
    type Target = Atom;

    const MANGLE: bool = false;
    const RESET_N: bool = true;

    fn new_name_for(&self, orig: &Id, n: &mut usize) -> Atom {
        let name = if *n == 0 {
            orig.0.clone()
        } else {
            format!("{}{}", orig.0, n).into()
        };
        *n += 1;
        name
    }

    fn preserve_name(&self, orig: &Id) -> bool {
        self.inferred.contains(orig)
    }
}

fn anonymous_callable(expression: &Expr) -> bool {
    match expression {
        Expr::Fn(function) => function.ident.is_none(),
        Expr::Class(class) => class.ident.is_none(),
        Expr::Arrow(_) => true,
        Expr::Paren(inner) => anonymous_callable(&inner.expr),
        _ => false,
    }
}

/// Name of the synthetic call that keeps inferred-name bindings alive during
/// compression; it never occurs in input that minification accepts.
const KEEP: &str = "__typeopt_keep_inferred_names__";

/// Bindings of a statement whose names anonymous functions or classes infer:
/// `const f = () => {}` and `f = class {};`.
fn inferring_bindings(statement: &Stmt) -> Vec<Ident> {
    match statement {
        Stmt::Decl(swc_core::ecma::ast::Decl::Var(declaration)) => declaration
            .decls
            .iter()
            .filter_map(|declarator| match (&declarator.name, &declarator.init) {
                (Pat::Ident(binding), Some(init)) if anonymous_callable(init) => {
                    Some(binding.id.clone())
                }
                _ => None,
            })
            .collect(),
        Stmt::Expr(ExprStmt { expr, .. }) => match &**expr {
            Expr::Assign(assignment) => match &assignment.left {
                AssignTarget::Simple(SimpleAssignTarget::Ident(binding))
                    if anonymous_callable(&assignment.right) =>
                {
                    vec![binding.id.clone()]
                }
                _ => Vec::new(),
            },
            _ => Vec::new(),
        },
        _ => Vec::new(),
    }
}

fn keep_call(bindings: Vec<Ident>) -> Stmt {
    Stmt::Expr(ExprStmt {
        span: Default::default(),
        expr: Box::new(Expr::Call(CallExpr {
            callee: Callee::Expr(Box::new(Expr::Ident(Ident::new_no_ctxt(
                KEEP.into(),
                Default::default(),
            )))),
            args: bindings
                .into_iter()
                .map(|binding| ExprOrSpread {
                    spread: None,
                    expr: Box::new(Expr::Ident(binding)),
                })
                .collect(),
            ..Default::default()
        })),
    })
}

fn is_keep_call(expression: &Expr) -> bool {
    matches!(expression, Expr::Call(CallExpr { callee: Callee::Expr(callee), .. })
        if matches!(&**callee, Expr::Ident(ident) if ident.sym == KEEP))
}

/// Inserts a call of an unknown function after every statement that binds an
/// inferred name, so the compressor sees a second use it cannot remove and
/// does not inline the anonymous function or class into its only other use
/// (which would change its `.name`).
struct InsertKeepCalls;

impl VisitMut for InsertKeepCalls {
    fn visit_mut_module_items(&mut self, items: &mut Vec<ModuleItem>) {
        items.visit_mut_children_with(self);
        let mut result = Vec::with_capacity(items.len());
        for item in items.drain(..) {
            let bindings = match &item {
                ModuleItem::Stmt(statement) => inferring_bindings(statement),
                _ => Vec::new(),
            };
            result.push(item);
            if !bindings.is_empty() {
                result.push(ModuleItem::Stmt(keep_call(bindings)));
            }
        }
        *items = result;
    }

    fn visit_mut_stmts(&mut self, statements: &mut Vec<Stmt>) {
        statements.visit_mut_children_with(self);
        let mut result = Vec::with_capacity(statements.len());
        for statement in statements.drain(..) {
            let bindings = inferring_bindings(&statement);
            result.push(statement);
            if !bindings.is_empty() {
                result.push(keep_call(bindings));
            }
        }
        *statements = result;
    }
}

/// Removes the calls [`InsertKeepCalls`] added. The compressor may have
/// moved other expressions into a call's arguments (an assignment collapsed
/// into its only later use, for example): those still run, in order; only the
/// plain binding references are dropped.
struct RemoveKeepCalls;

/// The expressions of `call`'s arguments that must still be evaluated, boxed
/// like the elements of the sequence expressions they join.
#[allow(clippy::vec_box)]
fn keep_call_effects(call: &mut CallExpr) -> Vec<Box<Expr>> {
    call.args
        .drain(..)
        .filter(|argument| argument.spread.is_none() && !matches!(&*argument.expr, Expr::Ident(_)))
        .map(|argument| argument.expr)
        .collect()
}

impl VisitMut for RemoveKeepCalls {
    fn visit_mut_module_items(&mut self, items: &mut Vec<ModuleItem>) {
        items.visit_mut_children_with(self);
        items.retain(|item| !matches!(item, ModuleItem::Stmt(Stmt::Empty(_))));
    }

    fn visit_mut_stmts(&mut self, statements: &mut Vec<Stmt>) {
        statements.visit_mut_children_with(self);
        statements.retain(|statement| !matches!(statement, Stmt::Empty(_)));
    }

    fn visit_mut_stmt(&mut self, statement: &mut Stmt) {
        let Stmt::Expr(ExprStmt { expr, span }) = statement else {
            statement.visit_mut_children_with(self);
            return;
        };
        // A statement's value is unused: every keep call in it, including
        // the last element of a sequence, reduces to its effects.
        let elements: Vec<Box<Expr>> = match &mut **expr {
            Expr::Seq(sequence) => std::mem::take(&mut sequence.exprs),
            _ => vec![std::mem::replace(
                expr,
                Box::new(Expr::Invalid(Default::default())),
            )],
        };
        let mut kept = Vec::with_capacity(elements.len());
        for mut element in elements {
            if let Expr::Call(call) = &mut *element
                && is_keep_call(&Expr::Call(call.clone()))
            {
                kept.extend(keep_call_effects(call));
            } else {
                kept.push(element);
            }
        }
        for element in &mut kept {
            element.visit_mut_with(self);
        }
        let span = *span;
        *statement = match kept.len() {
            0 => Stmt::Empty(swc_core::ecma::ast::EmptyStmt { span }),
            1 => Stmt::Expr(ExprStmt {
                span,
                expr: kept.pop().unwrap(),
            }),
            _ => Stmt::Expr(ExprStmt {
                span,
                expr: Box::new(Expr::Seq(swc_core::ecma::ast::SeqExpr {
                    span,
                    exprs: kept,
                })),
            }),
        };
    }

    fn visit_mut_expr(&mut self, expression: &mut Expr) {
        if let Expr::Seq(sequence) = expression {
            // Elements are walked here rather than through the children
            // visitor, which would turn an element keep call into `void 0`
            // before its position in the sequence is known.
            let last = sequence.exprs.len().saturating_sub(1);
            let mut exprs = Vec::with_capacity(sequence.exprs.len());
            for (index, mut element) in sequence.exprs.drain(..).enumerate() {
                if let Expr::Call(call) = &mut *element
                    && is_keep_call(&Expr::Call(call.clone()))
                {
                    let mut effects = keep_call_effects(call);
                    for effect in &mut effects {
                        effect.visit_mut_with(self);
                    }
                    exprs.extend(effects);
                    // A keep call yields `undefined`, which the sequence
                    // still yields when it was the last element.
                    if index == last {
                        exprs.push(void_zero());
                    }
                } else {
                    element.visit_mut_with(self);
                    exprs.push(element);
                }
            }
            sequence.exprs = exprs;
            if sequence.exprs.len() == 1 {
                *expression = *sequence.exprs.pop().unwrap();
            }
            return;
        }
        if let Expr::Call(call) = expression
            && is_keep_call(&Expr::Call(call.clone()))
        {
            // The compressor can move a keep call into any expression
            // position (an operand of `&&`, `||` or `??`, an arm of `?:`, a
            // loop body). Its value is `undefined`, so it reduces to its
            // effects followed by `void 0`.
            let mut effects = keep_call_effects(call);
            for effect in &mut effects {
                effect.visit_mut_with(self);
            }
            *expression = match effects.len() {
                0 => *void_zero(),
                1 => Expr::Unary(swc_core::ecma::ast::UnaryExpr {
                    span: Default::default(),
                    op: swc_core::ecma::ast::UnaryOp::Void,
                    arg: effects.pop().unwrap(),
                }),
                _ => {
                    effects.push(void_zero());
                    Expr::Seq(swc_core::ecma::ast::SeqExpr {
                        span: Default::default(),
                        exprs: effects,
                    })
                }
            };
            return;
        }
        expression.visit_mut_children_with(self);
    }
}

/// The expression `void 0`.
fn void_zero() -> Box<Expr> {
    Box::new(Expr::Unary(swc_core::ecma::ast::UnaryExpr {
        span: Default::default(),
        op: swc_core::ecma::ast::UnaryOp::Void,
        arg: Box::new(Expr::Lit(swc_core::ecma::ast::Lit::Num(0.0.into()))),
    }))
}

/// Counts keep calls that removal could not reach.
#[derive(Default)]
struct CountKeepCalls(usize);

impl Visit for CountKeepCalls {
    fn visit_call_expr(&mut self, call: &CallExpr) {
        call.visit_children_with(self);
        if is_keep_call(&Expr::Call(call.clone())) {
            self.0 += 1;
        }
    }
}

impl Visit for InferredNames {
    fn visit_var_declarator(&mut self, declarator: &VarDeclarator) {
        declarator.visit_children_with(self);
        if let (Pat::Ident(binding), Some(init)) = (&declarator.name, &declarator.init)
            && anonymous_callable(init)
        {
            self.0.push(binding.id.to_id());
        }
    }

    fn visit_assign_expr(&mut self, assignment: &AssignExpr) {
        assignment.visit_children_with(self);
        if let AssignTarget::Simple(SimpleAssignTarget::Ident(binding)) = &assignment.left
            && anonymous_callable(&assignment.right)
        {
            self.0.push(binding.id.to_id());
        }
    }

    fn visit_assign_pat(&mut self, pattern: &AssignPat) {
        pattern.visit_children_with(self);
        if let Pat::Ident(binding) = &*pattern.left
            && anonymous_callable(&pattern.right)
        {
            self.0.push(binding.id.to_id());
        }
    }
}

/// Minified code and a source map from it to the input code.
pub(crate) struct Minified {
    pub code: String,
    pub map: sourcemap::SourceMap,
    /// Compressed sizes of `code`.
    pub compressed: Compressed,
}

/// Compressed sizes that decide whether a minified chunk is kept.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) struct Compressed {
    pub gzip: usize,
    pub brotli: usize,
}

pub(crate) fn compressed(code: &str) -> Compressed {
    let mut gzip = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::best());
    let gzip = gzip
        .write_all(code.as_bytes())
        .ok()
        .and_then(|()| gzip.finish().ok())
        .map_or(usize::MAX, |bytes| bytes.len());
    let mut brotli = Vec::new();
    let parameters = brotli::enc::BrotliEncoderParams {
        quality: 11,
        lgwin: 22,
        ..Default::default()
    };
    let brotli = brotli::BrotliCompress(&mut code.as_bytes(), &mut brotli, &parameters)
        .map_or(usize::MAX, |_| brotli.len());
    Compressed { gzip, brotli }
}

/// Whether a minified version is worth publishing instead of `before`.
pub(crate) fn better(before: Compressed, after: Compressed) -> bool {
    after.brotli < before.brotli && after.gzip <= before.gzip
}

fn legal(text: &str) -> bool {
    text.starts_with('!') || text.contains("@license") || text.contains("@preserve")
}

/// Read emitted legal comments as comments, so matching text in a string or
/// another comment cannot stand in for a missing notice.
fn emitted_legal_comments(cm: &Lrc<SourceMap>, code: &str) -> Option<Vec<Comment>> {
    let file = cm.new_source_file(Lrc::new(FileName::Anon), code.to_owned());
    let comments = SingleThreadedComments::default();
    let mut errors = Vec::new();
    parse_file_as_module(
        &file,
        Syntax::Es(EsSyntax::default()),
        EsVersion::latest(),
        Some(&comments),
        &mut errors,
    )
    .ok()?;
    if !errors.is_empty() {
        return None;
    }
    let (leading, trailing) = comments.borrow_all();
    let mut notices: Vec<Comment> = leading
        .values()
        .chain(trailing.values())
        .flatten()
        .filter(|comment| legal(&comment.text))
        .cloned()
        .collect();
    notices.sort_by_key(|comment| comment.span.lo);
    Some(notices)
}

/// Compare emitted comment identities in source order. Emission changes
/// spans, but it must preserve each comment's kind and exact text.
fn same_legal_comments(expected: &[Comment], emitted: &[Comment]) -> bool {
    expected.len() == emitted.len()
        && expected
            .iter()
            .zip(emitted)
            .all(|(left, right)| left.kind == right.kind && left.text == right.text)
}

fn definitely_not_callable(expression: &Expr) -> bool {
    match expression {
        Expr::Lit(
            swc_core::ecma::ast::Lit::Num(_)
            | swc_core::ecma::ast::Lit::Str(_)
            | swc_core::ecma::ast::Lit::Bool(_)
            | swc_core::ecma::ast::Lit::BigInt(_)
            | swc_core::ecma::ast::Lit::Null(_),
        ) => true,
        Expr::Paren(inner) => definitely_not_callable(&inner.expr),
        _ => false,
    }
}

/// Whether a computed property key may name `toString` at runtime. Only string
/// literals other than `toString`, numbers and bigints are known to be safe;
/// every other expression (identifiers, templates, calls, `"toString"` itself)
/// is treated as a possible `toString` read.
fn computed_key_may_be_to_string(key: &Expr) -> bool {
    !matches!(
        key,
        Expr::Lit(swc_core::ecma::ast::Lit::Str(value)) if value.value != "toString"
    ) && !matches!(
        key,
        Expr::Lit(swc_core::ecma::ast::Lit::Num(_) | swc_core::ecma::ast::Lit::BigInt(_))
    )
}

/// A source-text reflection boundary anywhere in the bundle can observe a
/// callable defined in another chunk. Without a closed proof of the target,
/// publication must keep every edited chunk's pre-minification text.
pub(crate) fn may_observe_function_source(code: &str) -> bool {
    let cm: Lrc<SourceMap> = Default::default();
    let file = cm.new_source_file(Lrc::new(FileName::Anon), code.to_owned());
    let mut errors = Vec::new();
    let Ok(module) = parse_file_as_module(
        &file,
        Syntax::Es(EsSyntax::default()),
        EsVersion::latest(),
        None,
        &mut errors,
    ) else {
        return true;
    };
    if !errors.is_empty() {
        return true;
    }
    let mut scan = SourceReflection::default();
    module.visit_with(&mut scan);
    scan.member_to_string || scan.function_prototype
}

#[derive(Default)]
struct SourceReflection {
    member_to_string: bool,
    function_prototype: bool,
}

impl Visit for SourceReflection {
    fn visit_call_expr(&mut self, call: &CallExpr) {
        call.visit_children_with(self);
        // `String(value)` performs the same callable-to-string conversion as
        // an explicit `toString` call. Direct eval can execute source text in
        // the importing lexical scope, so its string may read any callable.
        if let Callee::Expr(callee) = &call.callee
            && matches!(&**callee, Expr::Ident(ident) if ident.sym == "String" || ident.sym == "eval")
        {
            self.member_to_string = true;
        }
    }

    fn visit_tpl(&mut self, template: &Tpl) {
        template.visit_children_with(self);
        if !template.exprs.is_empty() {
            self.member_to_string = true;
        }
    }

    fn visit_member_expr(&mut self, member: &MemberExpr) {
        member.visit_children_with(self);
        match &member.prop {
            MemberProp::Ident(name) if name.sym == "toString" => {
                self.member_to_string |= !definitely_not_callable(&member.obj);
            }
            MemberProp::Ident(name)
                if name.sym == "prototype"
                    && matches!(&*member.obj, Expr::Ident(object) if object.sym == "Function") =>
            {
                self.function_prototype = true;
            }
            MemberProp::Computed(computed) => {
                self.member_to_string |= computed_key_may_be_to_string(&computed.expr)
                    && !definitely_not_callable(&member.obj);
            }
            _ => {}
        }
    }

    fn visit_object_pat_prop(&mut self, property: &ObjectPatProp) {
        property.visit_children_with(self);
        if match property {
            ObjectPatProp::KeyValue(key_value) => match &key_value.key {
                PropName::Ident(name) => name.sym == "toString",
                PropName::Str(value) => value.value == "toString",
                // A pattern has no object expression to prove non-callable,
                // so only a known non-`toString` literal key is safe.
                PropName::Computed(computed) => computed_key_may_be_to_string(&computed.expr),
                _ => false,
            },
            ObjectPatProp::Assign(assignment) => assignment.key.id.sym == "toString",
            ObjectPatProp::Rest(_) => false,
        } {
            self.member_to_string = true;
        }
    }
}

/// Minifies an ES module. `None` when SWC cannot parse it or emit it; the
/// chunk then stays as the pipeline left it.
pub(crate) fn minify_module(code: &str) -> Option<Minified> {
    let cm: Lrc<SourceMap> = Default::default();
    let file = cm.new_source_file(Lrc::new(FileName::Anon), code.to_owned());
    let (code, map) = GLOBALS.set(&Globals::new(), || {
        let unresolved = Mark::new();
        let top_level = Mark::new();
        let comments = SingleThreadedComments::default();
        let mut errors = Vec::new();
        let module = parse_file_as_module(
            &file,
            Syntax::Es(EsSyntax::default()),
            EsVersion::latest(),
            Some(&comments as &dyn swc_core::common::comments::Comments),
            &mut errors,
        )
        .ok()?;
        if !errors.is_empty() {
            tracing::debug!(errors = errors.len(), "minify skipped: parse errors");
            return None;
        }
        let mut program = Program::Module(module);
        // Legal notices may sit on declarations that compression removes;
        // they are restored at the start of the output if the emitter drops
        // them.
        let notices: Vec<Comment> = {
            let (leading, trailing) = comments.borrow_all();
            let mut notices: Vec<Comment> = leading
                .values()
                .chain(trailing.values())
                .flatten()
                .filter(|comment| legal(&comment.text))
                .cloned()
                .collect();
            notices.sort_by_key(|comment| comment.span.lo);
            notices
        };
        if code.contains(KEEP) {
            tracing::debug!("minify skipped: input contains a reserved helper name");
            return None;
        }
        program.visit_mut_with(&mut InsertKeepCalls);
        program.visit_mut_with(&mut resolver(unresolved, top_level, false));
        let mut inferred = InferredNames::default();
        program.visit_with(&mut inferred);
        let reserved = inferred.names();
        // The compressor defaults of SWC's terser-compatible options, as
        // `@swc/core` applies them, for a module, keeping observable names
        // and parameter counts. Hoisting properties into variables would
        // rename the functions that object literal keys name.
        let compressor: TerserCompressorOptions = serde_json::from_str(
            r#"{"module":true,"toplevel":true,"keep_fnames":true,"keep_classnames":true,"keep_fargs":true,"hoist_props":false}"#,
        )
        .ok()?;
        let options = MinifyOptions {
            compress: Some(compressor.into_config(cm.clone())),
            mangle: Some(MangleOptions {
                top_level: Some(true),
                keep_fn_names: true,
                keep_class_names: true,
                reserved: reserved.clone(),
                ..Default::default()
            }),
            ..Default::default()
        };
        let mut program = optimize(
            program,
            cm.clone(),
            Some(&comments),
            None,
            &options,
            &ExtraOptions {
                unresolved_mark: unresolved,
                top_level_mark: top_level,
                mangle_name_cache: None,
            },
        );
        program.visit_mut_with(&mut RemoveKeepCalls);
        let mut leftover = CountKeepCalls::default();
        program.visit_with(&mut leftover);
        if leftover.0 > 0 {
            tracing::debug!(
                calls = leftover.0,
                "minify skipped: name-keeping calls remain"
            );
            return None;
        }
        // The compressor can move references next to a binding of the same
        // name with another syntax context, for example into the head of a
        // `for (let e of …)` loop, which evaluates in the scope of `e`. The
        // mangler leaves names visible to `eval` or `with` alone, so the
        // hygiene pass renames whatever still collides, as `@swc/core` does
        // after optimizing.
        program.visit_mut_with(&mut renamer(
            HygieneConfig {
                keep_class_names: true,
                top_level_mark: top_level,
                ..Default::default()
            },
            InferredHygiene {
                inferred: inferred.0.iter().cloned().collect(),
            },
        ));
        // A binding that names its function or class must keep its name.
        let mut kept = InferredNames::default();
        program.visit_with(&mut kept);
        if let Some(name) = kept
            .names()
            .into_iter()
            .find(|name| reserved.binary_search(name).is_err())
        {
            tracing::debug!(%name, "minify skipped: an inferred name would change");
            return None;
        }
        program.visit_mut_with(&mut fixer(Some(&comments)));
        {
            let (mut leading, mut trailing) = comments.borrow_all_mut();
            for list in leading.values_mut().chain(trailing.values_mut()) {
                list.retain(|comment| legal(&comment.text));
            }
        }
        let emit = |comments: &SingleThreadedComments| {
            let mut output = Vec::new();
            let mut mappings = Vec::new();
            {
                let mut emitter = Emitter {
                    cfg: Config::default()
                        .with_minify(true)
                        .with_target(EsVersion::latest()),
                    cm: cm.clone(),
                    comments: Some(comments),
                    wr: omit_trailing_semi(JsWriter::new(
                        cm.clone(),
                        "\n",
                        &mut output,
                        Some(&mut mappings),
                    )),
                };
                emitter.emit_program(&program).ok()?;
            }
            Some((String::from_utf8(output).ok()?, mappings))
        };
        // Emit every legal notice from a single normalized location. This
        // preserves notices attached to removed declarations and avoids
        // substring matches in code or unrelated comments.
        let start = match &program {
            Program::Module(module) => module.body.first().map_or(BytePos(0), |item| item.span().lo),
            Program::Script(script) => script.body.first().map_or(BytePos(0), |item| item.span().lo),
        };
        {
            let (mut leading, mut trailing) = comments.borrow_all_mut();
            leading.clear();
            trailing.clear();
        }
        for notice in &notices {
            comments.add_leading(start, notice.clone());
        }
        let (code, mappings) = emit(&comments)?;
        let emitted = emitted_legal_comments(&cm, &code)?;
        if !same_legal_comments(&notices, &emitted) {
            tracing::debug!("minify skipped: legal notices could not be verified");
            return None;
        }
        let mut json = Vec::new();
        cm.build_source_map(
            &mappings,
            None,
            swc_core::common::source_map::DefaultSourceMapGenConfig,
        )
        .to_writer(&mut json)
        .ok()?;
        let map = sourcemap::SourceMap::from_slice(&json).ok()?;
        Some((code, map))
    })?;
    // Measured after the SWC program, comments and mappings are dropped, so
    // the compressors do not add to their peak memory.
    drop(file);
    drop(cm);
    Some(Minified {
        compressed: compressed(&code),
        code,
        map,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn source_reflection_covers_computed_pattern_keys() {
        assert!(may_observe_function_source(
            "const {[\"toString\"]: x} = f;"
        ));
        assert!(may_observe_function_source("const {[key]: x} = f;"));
        assert!(may_observe_function_source("const {[`${a}`]: x} = f;"));
        assert!(may_observe_function_source("const {toString: x} = f;"));
        assert!(may_observe_function_source("const {\"toString\": x} = f;"));
        assert!(!may_observe_function_source("const {[\"name\"]: x} = f;"));
        assert!(!may_observe_function_source("const {[0]: x} = f;"));
        assert!(!may_observe_function_source("const {[1n]: x} = f;"));
    }

    #[test]
    fn minifies_and_maps_back_to_the_input() {
        let input = "const longName = 1 + 2;\nexport function compute(value) {\n  return value * \
                     longName;\n}\n";
        let minified = minify_module(input).unwrap();
        assert!(minified.code.len() < input.len(), "{}", minified.code);
        assert!(minified.code.contains("export"), "{}", minified.code);
        // `compute` stays exported under its name.
        assert!(minified.code.contains("compute"), "{}", minified.code);
        assert!(minified.map.tokens().count() > 0);
    }

    #[test]
    fn keeps_legal_comments_and_drops_others() {
        let input = "/*! keep me */\n// drop me\nexport const a = /*#__PURE__*/ f();\nfunction \
                     f() { return 1 }\n";
        let minified = minify_module(input).unwrap();
        assert!(minified.code.contains("keep me"), "{}", minified.code);
        assert!(!minified.code.contains("drop me"), "{}", minified.code);
    }

    fn run_module(code: &str) -> String {
        let mut child = std::process::Command::new("node")
            .args(["--input-type=module", "-"])
            .stdin(std::process::Stdio::piped())
            .stdout(std::process::Stdio::piped())
            .spawn()
            .expect("node");
        child
            .stdin
            .take()
            .unwrap()
            .write_all(code.as_bytes())
            .unwrap();
        let output = child.wait_with_output().unwrap();
        assert!(output.status.success(), "{code}");
        String::from_utf8(output.stdout).unwrap()
    }

    #[test]
    fn preserves_observable_names_and_arity() {
        let input = r#"
function namedFunction() { return 1; }
class NamedClass {}
const inferredArrow = () => 2;
const inferredClass = class {};
let assignedFunction;
assignedFunction = function () { return 3; };
const withDefault = (callback = () => 4) => callback.name;
function withUnusedParameters(first, second, third) { return first; }
const handler = (event, unusedContext) => event;
const codec = { decode: (value) => value + 1, encode: (value) => value - 1 };
console.log(
  namedFunction.name, NamedClass.name, new NamedClass().constructor.name,
  inferredArrow.name, inferredClass.name, assignedFunction.name, withDefault(),
  withUnusedParameters.length, handler.length, withUnusedParameters(5), handler(6),
  codec.decode.name, codec.decode(1), codec.encode(1),
);
export {};
"#;
        let minified = minify_module(input).unwrap();
        assert_eq!(
            run_module(&minified.code),
            run_module(input),
            "{}",
            minified.code
        );
    }

    /// The compressor moves the constructor's statements into the head of
    /// `for (let e of …)`, where `e` names the loop binding; the parameter
    /// `e` keeps its name because a function infers the name `e`.
    #[test]
    fn keeps_loop_heads_apart_from_their_bindings() {
        let input = r#"
const e = () => 1;
function f(component) { return { alpha: component, beta: 2 }; }
function h(read) { console.log(read()); }
class Vo {
  #t;
  constructor(e) {
    this.#t = f(e.component);
    for (let e of Object.keys(this.#t)) h(() => e);
  }
}
new Vo({ component: 1 });
console.log(e.name);
export {};
"#;
        let minified = minify_module(input).unwrap();
        assert_eq!(
            run_module(&minified.code),
            run_module(input),
            "{}",
            minified.code
        );
    }

    /// Asserts that `input` still minifies, leaves no keep call behind and
    /// behaves the same.
    fn assert_minified_equivalent(input: &str) {
        let minified = minify_module(input).expect("minified");
        assert!(!minified.code.contains(KEEP), "{}", minified.code);
        assert_eq!(
            run_module(&minified.code),
            run_module(input),
            "{}",
            minified.code
        );
    }

    #[test]
    fn removes_keep_calls_in_logical_operands() {
        assert_minified_equivalent(
            "let f; if (globalThis.c === undefined) { f = () => 1; } console.log(f && f.name, f \
             && f()); export {};",
        );
    }

    #[test]
    fn removes_keep_calls_in_return_sequences() {
        assert_minified_equivalent(
            "let f; function h(c) { if (c) { f = function () { return 1; }; } return f; } \
             console.log(h(1).name, h(0).name); export {};",
        );
    }

    #[test]
    fn removes_keep_calls_in_conditional_arms() {
        assert_minified_equivalent(
            "let f; function h(c) { if (c) { f = () => 1; } else { console.log(\"no\"); } return \
             f; } console.log(h(1).name); h(0); export {};",
        );
    }

    #[test]
    fn removes_keep_calls_in_or_operands_with_classes() {
        assert_minified_equivalent(
            "let f; function h(c) { if (!c) { f = class {}; } return f; } console.log(h(0).name); \
             export {};",
        );
    }

    #[test]
    fn removes_keep_calls_in_loop_bodies() {
        assert_minified_equivalent(
            "let f; function h(n) { for (let i = 0; i < n; i++) { if (i) { f = () => i; } } \
             return f; } console.log(h(3).name, h(3)()); export {};",
        );
    }

    #[test]
    fn removes_keep_calls_in_value_positions_with_effects() {
        assert_minified_equivalent(
            "let f; function h(c) { if (c) { f = () => 1; } } h(1); console.log(\"ok\"); export \
             {};",
        );
    }

    #[test]
    fn replaces_expression_keep_calls_with_their_effects() {
        fn rewrite(source: &str) -> String {
            let cm: Lrc<SourceMap> = Default::default();
            let file = cm.new_source_file(Lrc::new(FileName::Anon), source.to_owned());
            let mut errors = Vec::new();
            let mut module = parse_file_as_module(
                &file,
                Syntax::Es(EsSyntax::default()),
                EsVersion::latest(),
                None,
                &mut errors,
            )
            .unwrap();
            module.visit_mut_with(&mut RemoveKeepCalls);
            // The production pipeline parenthesizes through the fixer.
            module.visit_mut_with(&mut fixer(None));
            let mut leftover = CountKeepCalls::default();
            module.visit_with(&mut leftover);
            assert_eq!(leftover.0, 0);
            let mut output = Vec::new();
            Emitter {
                cfg: Config::default().with_minify(true),
                cm: cm.clone(),
                comments: None,
                wr: JsWriter::new(cm, "\n", &mut output, None),
            }
            .emit_module(&module)
            .unwrap();
            String::from_utf8(output).unwrap()
        }
        assert_eq!(rewrite(&format!("a && {KEEP}(f);")), "a&&void 0;");
        assert_eq!(rewrite(&format!("a && {KEEP}(f = 1);")), "a&&void(f=1);");
        assert_eq!(
            rewrite(&format!("x = a ? {KEEP}(f = 1, g(), h) : 2;")),
            "x=a?(f=1,g(),void 0):2;"
        );
        assert_eq!(
            rewrite(&format!("x = (a, {KEEP}(f = 1));")),
            "x=(a,f=1,void 0);"
        );
    }

    #[test]
    fn keeps_legal_comments_of_removed_declarations() {
        let input = "/*! keep this license */\nfunction unusedHelper() { return 1; }\n/** \
                     @license MIT */\nconst unusedValue = 2;\nexport const used = 3;\n";
        let minified = minify_module(input).unwrap();
        assert!(!minified.code.contains("unusedHelper"), "{}", minified.code);
        assert!(
            minified.code.contains("keep this license"),
            "{}",
            minified.code
        );
        assert!(minified.code.contains("@license MIT"), "{}", minified.code);
        assert_eq!(minified.code.matches("keep this license").count(), 1);
    }

    #[test]
    fn rejects_unparsable_input() {
        assert!(minify_module("export const = ;").is_none());
    }

    #[test]
    fn keeps_minified_code_only_when_it_compresses_smaller() {
        let before = Compressed {
            gzip: 100,
            brotli: 90,
        };
        assert!(better(
            before,
            Compressed {
                gzip: 100,
                brotli: 89
            }
        ));
        assert!(!better(
            before,
            Compressed {
                gzip: 101,
                brotli: 80
            }
        ));
        assert!(!better(
            before,
            Compressed {
                gzip: 90,
                brotli: 90
            }
        ));
    }
}
