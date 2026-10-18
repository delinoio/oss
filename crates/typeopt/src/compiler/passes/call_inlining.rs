// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `call-inlining`: replaces direct calls of small resolved functions with
//! their single returned expression.
//!
//! Supported targets are function declarations and `const`, `let` or `var`
//! arrow or function expressions whose body is one `return <expr>;` (or an
//! expression body). The planner proves, per target:
//!
//! - one declaration, no writes to the binding, no type parameters, no
//!   rest/default/destructured/`this` parameters, not async or a generator, no
//!   type-predicate return, no binding annotation;
//! - the binding is never read before its initializer runs: it is a top-level
//!   declaration of a module outside any static import cycle and no earlier
//!   top-level statement or initializer runs code, or a function-local
//!   declaration whose sites follow it in the same function. A function-scoped
//!   `var` must sit directly in a function body or the module, so no site after
//!   an enclosing block can run without the initializer;
//! - the expression contains only supported operators (no nested functions or
//!   classes, `this`, `super`, `arguments`, meta properties, `delete`,
//!   `await`/`yield`, tagged templates or JSX) and no recursion. Assignments
//!   and updates may write properties and free bindings, never parameters (that
//!   would write the caller's argument) or destructuring patterns; their reads
//!   and writes are effects in the evaluation order.
//!
//! Per call site it proves:
//!
//! - the callee is the plain binding (no optional call, spread or missing
//!   arguments) and the site is outside the target itself;
//! - every free name of the expression (values and type names) resolves to the
//!   same binding at the call site, so no capture or shadowing occurs and no
//!   import is needed;
//! - evaluation order, count and exceptions are preserved: arguments with
//!   effects are used exactly once, unconditionally, in argument order, and
//!   nothing observable in the expression runs before the last of them;
//!   arguments used more than once or dropped are side-effect free, cannot
//!   throw and are stable (literals, `const`s proven initialized, never-written
//!   parameters);
//! - types are unchanged: each argument has the parameter's type or is asserted
//!   to the parameter's annotation, and the expression has the function's
//!   return type or is asserted to its annotation. Bundle chunks are unchecked
//!   JavaScript, where types never change what runs, so these checks do not
//!   apply there and a missing argument becomes `void 0`.
//!
//! When every reference is an inlined call or an internal import
//! specifier, the bundle also removes the declaration and those
//! specifiers. That removal belongs to `residual-dependency-removal` and is
//! only proposed while that pass is enabled; otherwise every call site is
//! a separate candidate that must reduce size on its own.
use std::collections::HashMap;

use anyhow::Result;

use super::{
    super::analysis::{Ctx, RefKind, skip_outer},
    calls_support::{self as support, Facts},
};
use crate::{
    compiler::api::*,
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::CallInlining;

thread_local! {
    /// Skip causes below the stable reason codes, logged at debug level after
    /// each planning run. They show where the proofs fall short most often;
    /// they are not part of the report.
    static DETAILS: std::cell::RefCell<std::collections::BTreeMap<String, usize>> =
        Default::default();
}

/// Records a detailed skip cause and returns its reason.
fn detail(reason: Reason, cause: impl Into<String>) -> Reason {
    DETAILS.with(|details| *details.borrow_mut().entry(cause.into()).or_default() += 1);
    reason
}

/// Innermost function-like, class or source file around `node`. Code in
/// different containers can run in an order the site cannot see.
fn run_container(node: Node) -> Node {
    let mut current = node.parent();
    while current.is_some()
        && !is_function_like(current)
        && !matches!(
            current.kind(),
            SyntaxKind::SourceFile
                | SyntaxKind::ClassDeclaration
                | SyntaxKind::ClassExpression
                | SyntaxKind::ClassStaticBlockDeclaration
        )
    {
        current = current.parent();
    }
    current
}

/// Whether nothing the inlined body does can change the value of a mutable
/// binding read by `argument`: the binding is a parameter or a `let` or `var`
/// of the function (or module) that contains the site, every write to it
/// lies directly in that same function, and the inlined expression does not
/// write it. Code run by an effect then cannot write it: other functions do
/// not write it, and a recursive entry of the function writes its own fresh
/// binding.
fn private_to_site_function(ctx: &mut Ctx, target: &Target, argument: Node, call: Node) -> bool {
    let argument = skip_outer(argument);
    if argument.kind() != SyntaxKind::Identifier {
        return false;
    }
    let raw = ctx.resolve(argument);
    if raw.is_nil() {
        return false;
    }
    let symbol = support::norm(ctx, raw);
    if symbol != raw || target.writes.contains(&raw) {
        // An alias (import) or a name whose declaration merges elsewhere.
        return false;
    }
    let declaration = ctx.checker.sym(symbol).value_declaration;
    if declaration.is_nil() || declaration.flags().intersects(NodeFlags::Ambient) {
        return false;
    }
    let container = run_container(call);
    let owner = match declaration.kind() {
        SyntaxKind::Parameter => declaration.parent(),
        SyntaxKind::VariableDeclaration => {
            let list = declaration.parent();
            if list.flags().intersects(NodeFlags::Using)
                || list.parent().kind() != SyntaxKind::VariableStatement
            {
                return false;
            }
            run_container(declaration)
        }
        _ => return false,
    };
    if owner.is_nil() || owner != container {
        return false;
    }
    ctx.refs(symbol)
        .iter()
        .filter(|r| r.kind == RefKind::Write)
        .all(|r| run_container(r.node) == container)
}

pub(in crate::compiler) fn plan(ctx: &mut Ctx, enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let mut facts = Facts::new();
    let mut targets = Vec::new();
    for file in 0..ctx.files.len() {
        crate::app::check_cancelled()?;
        if ctx.files[file].js {
            continue;
        }
        let nodes = ctx.files[file].nodes.clone();
        for node in nodes {
            let Some(shape) = Shape::of(node) else {
                continue;
            };
            match Target::analyze(ctx, &mut facts, file, shape) {
                Ok(target) => targets.push(target),
                Err((reason, at)) => {
                    let key = function_key(ctx, file, shape.name);
                    out.skips.push(ctx.skip(PASS, key, file, at, reason));
                }
            }
        }
    }
    let mut planned = Vec::new();
    for target in &targets {
        crate::app::check_cancelled()?;
        planned.push(collect_sites(ctx, &mut facts, target, out));
    }
    // A function that an inlined expression mentions is copied to new
    // places this round, so it is not removed in the same round.
    let mut copied: std::collections::HashSet<SymbolId> = std::collections::HashSet::new();
    for (target, planned) in targets.iter().zip(&planned) {
        if !planned.sites.is_empty() {
            copied.extend(target.free.iter().map(|f| f.symbol));
        }
    }
    let mut candidates = 0;
    for (target, planned) in targets.iter().zip(planned) {
        let removable = !copied.contains(&support::norm(ctx, target.symbol));
        candidates += plan_target(ctx, &mut facts, enabled, target, planned, removable, out);
    }
    tracing::debug!(
        pass = %PASS,
        targets = targets.len(),
        candidates,
        "call inlining planned"
    );
    let details = DETAILS.with(|details| std::mem::take(&mut *details.borrow_mut()));
    tracing::debug!(pass = %PASS, ?details, "call inlining skip details");
    Ok(())
}

/// Syntactic shape of a potential target.
#[derive(Clone, Copy)]
struct Shape {
    /// The declaration carrying the binding name.
    declaration: Node,
    name: Node,
    function: Node,
    expression: Node,
}

impl Shape {
    fn of(node: Node) -> Option<Shape> {
        let (function, name) = match node.kind() {
            SyntaxKind::FunctionDeclaration if node.name().is_some() => (node, node.name()),
            SyntaxKind::VariableDeclaration
                if node.name().kind() == SyntaxKind::Identifier && node.initializer().is_some() =>
            {
                let mut initializer = node.initializer();
                while initializer.kind() == SyntaxKind::ParenthesizedExpression {
                    initializer = initializer.expression();
                }
                if !matches!(
                    initializer.kind(),
                    SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression
                ) {
                    return None;
                }
                (initializer, node.name())
            }
            _ => return None,
        };
        let body = function.body();
        if body.is_nil() {
            return None;
        }
        let expression = if body.kind() == SyntaxKind::Block {
            let statements = body.statements();
            if statements.len() != 1 {
                return None;
            }
            let statement = statements.get(0);
            if statement.kind() != SyntaxKind::ReturnStatement || statement.expression().is_nil() {
                return None;
            }
            statement.expression()
        } else {
            body
        };
        Some(Shape {
            declaration: node,
            name,
            function,
            expression,
        })
    }
}

/// One use of a parameter inside the inlined expression.
struct Use {
    node: Node,
    shorthand: bool,
    callee: bool,
    member_object: bool,
}

struct Param {
    symbol: SymbolId,
    annotation: Node,
    annotation_names: Vec<FreeName>,
    uses: Vec<Use>,
}

/// A name the expression resolves through scope; it must resolve to the
/// same binding at every call site.
#[derive(Clone)]
struct FreeName {
    name: String,
    meaning: SymbolFlags,
    symbol: SymbolId,
}

/// Evaluation-order event of the inlined expression.
#[derive(Clone, Copy)]
enum Event {
    /// A parameter is evaluated; `true` when evaluation is conditional.
    Param(usize, bool),
    /// Anything that may have an effect, throw or observe state.
    Effect,
}

struct Target {
    file: usize,
    name: String,
    symbol: SymbolId,
    shape: Shape,
    params: Vec<Param>,
    events: Vec<Event>,
    free: Vec<FreeName>,
    /// The expression branches (conditionals, logical operators, optional
    /// chains), so control-flow narrowing may relate parameter uses.
    branching: bool,
    /// The expression's type differs from the return type; sites assert
    /// it to the return annotation.
    retype: bool,
    return_names: Vec<FreeName>,
    /// Statement removed with the declaration.
    statement: Node,
    /// A function-local `const` whose temporal dead zone is checked per
    /// call site.
    local: bool,
    /// The `local` declaration is a top-level statement of its module, so
    /// call sites are judged by [`site_runs_after`] instead of by position.
    top_level: bool,
    /// Free bindings that the expression assigns or updates.
    writes: Vec<SymbolId>,
}

type Rejection = (Reason, Node);

impl Target {
    fn analyze(
        ctx: &mut Ctx,
        facts: &mut Facts,
        file: usize,
        shape: Shape,
    ) -> Result<Target, Rejection> {
        let function = shape.function;
        let reject = |reason| Err((reason, shape.name));
        if function.asterisk_token().is_some()
            || function.modifier_flags().intersects(ModifierFlags::Async)
            || !function.type_parameters().is_empty()
            || !function.decorators().is_empty()
        {
            return reject(detail(
                Reason::UnsupportedSyntax,
                "target:async-generator-generic-or-decorated",
            ));
        }
        let return_annotation = function.type_();
        if return_annotation.is_some() && return_annotation.kind() == SyntaxKind::TypePredicate {
            return reject(detail(Reason::TypeConstraint, "target:type-predicate"));
        }
        let symbol = ctx.resolve(shape.name);
        if symbol.is_nil() {
            return reject(Reason::UnresolvedCall);
        }
        if ctx.checker.sym(symbol).declarations.len() != 1 {
            return reject(Reason::DeclarationMerging);
        }
        if ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write) {
            return reject(detail(Reason::Mutation, "target:binding-written"));
        }
        let mut local = false;
        let mut top_level = false;
        let statement = match shape.declaration.kind() {
            SyntaxKind::VariableDeclaration => {
                let list = shape.declaration.parent();
                if list.flags().intersects(NodeFlags::Using) {
                    return reject(detail(Reason::UnsupportedSyntax, "target:using"));
                }
                if shape.declaration.type_().is_some() {
                    return reject(detail(Reason::TypeConstraint, "target:annotated-binding"));
                }
                let statement = list.parent();
                // Removing a loop header's declaration would remove the
                // condition and body along with the function binding.
                if statement.kind() != SyntaxKind::VariableStatement {
                    return reject(detail(
                        Reason::UnsupportedSyntax,
                        "target:non-standalone-binding",
                    ));
                }
                // A `let` or `var` that nothing writes (checked above) holds
                // the function from its initialization on, like a `const`.
                // A `var` is function-scoped, so a site after an enclosing
                // block could run without the initializer; only a `var`
                // directly in a function body or the module qualifies.
                let block_scoped = list.flags().intersects(NodeFlags::Const | NodeFlags::Let);
                if !block_scoped {
                    let container = statement.parent();
                    let top_level = container.kind() == SyntaxKind::SourceFile
                        || (container.kind() == SyntaxKind::Block
                            && is_function_like(container.parent()));
                    if !top_level {
                        return reject(detail(Reason::Mutation, "target:var-in-nested-block"));
                    }
                }
                if !facts.initialized_early_unwritten(ctx, symbol) {
                    // A function-local constant is safe at sites that the
                    // declaration's own function reaches after it.
                    if matches!(
                        statement.parent().kind(),
                        SyntaxKind::CaseClause | SyntaxKind::DefaultClause
                    ) {
                        return reject(detail(Reason::ModuleInitialization, "target:tdz"));
                    }
                    local = true;
                    top_level = statement.parent().kind() == SyntaxKind::SourceFile;
                }
                if list.declarations().nodes().len() == 1 {
                    list.parent()
                } else {
                    Node::NIL
                }
            }
            _ => shape.declaration,
        };
        let mut params = Vec::new();
        let mut index = HashMap::new();
        for parameter in function.parameters().iter() {
            let name = parameter.name();
            if name.kind() != SyntaxKind::Identifier
                || name.text() == "this"
                || parameter.dot_dot_dot_token().is_some()
                || parameter.initializer().is_some()
                || !parameter.modifiers().nodes().is_empty()
            {
                let cause = if name.kind() != SyntaxKind::Identifier {
                    "target:destructured-parameter"
                } else if parameter.dot_dot_dot_token().is_some() {
                    "target:rest-parameter"
                } else if parameter.initializer().is_some() {
                    "target:default-parameter"
                } else {
                    "target:this-or-modified-parameter"
                };
                return reject(detail(Reason::UnsupportedSyntax, cause));
            }
            let param_symbol = parameter.symbol();
            index.insert(param_symbol, params.len());
            let annotation = parameter.type_();
            let mut annotation_names = Vec::new();
            if annotation.is_some() && !type_names(ctx, annotation, &mut annotation_names) {
                // Unnameable annotations only prevent assertions at sites.
                annotation_names.clear();
                params.push(Param {
                    symbol: param_symbol,
                    annotation: Node::NIL,
                    annotation_names,
                    uses: Vec::new(),
                });
                continue;
            }
            params.push(Param {
                symbol: param_symbol,
                annotation,
                annotation_names,
                uses: Vec::new(),
            });
        }
        let mut walker = Walker {
            ctx,
            facts,
            params: &index,
            own: [symbol, function.symbol()],
            events: Vec::new(),
            free: Vec::new(),
            uses: Vec::new(),
            branching: false,
            writes: Vec::new(),
        };
        walker.walk(shape.expression, false)?;
        let Walker {
            events,
            free,
            uses,
            branching,
            writes,
            ..
        } = walker;
        for (param, value) in uses {
            params[param].uses.push(value);
        }
        // Types: the inlined expression must keep the call's type.
        let signature = ctx.checker.get_signature_from_declaration(function);
        let return_type = ctx.checker.get_return_type_of_signature(signature);
        let expression_type = ctx.checker.get_type_at_location(shape.expression);
        // A context-sensitive expression (literals of arrays, objects or
        // functions) was typed against the return annotation; without it
        // a call site could type it differently.
        let contextual = support::context_sensitive(shape.expression);
        // Bundle chunks are unchecked JavaScript: types never change what
        // runs, so the inlined expression needs no type preservation there.
        // A chunk that opts into checking is rechecked after each round.
        let typed = !crate::app::bundle_mode();
        let retype = typed
            && (return_type != expression_type || (contextual && return_annotation.is_some()));
        let mut return_names = Vec::new();
        if (typed && contextual && return_annotation.is_nil())
            || (retype
                && (return_annotation.is_nil()
                    || !type_names(ctx, return_annotation, &mut return_names)))
        {
            return Err((
                detail(Reason::TypeConstraint, "target:return-type"),
                shape.expression,
            ));
        }
        Ok(Target {
            file,
            name: shape.name.text().to_owned(),
            symbol,
            shape,
            params,
            events,
            free,
            branching,
            retype,
            return_names,
            writes,
            statement,
            local,
            top_level,
        })
    }
}

/// Collects the scope-resolved names of a type node; `false` when it holds
/// constructs that cannot be moved to another scope.
fn type_names(ctx: &mut Ctx, node: Node, out: &mut Vec<FreeName>) -> bool {
    for item in support::subtree(node) {
        match item.kind() {
            SyntaxKind::TypeQuery
            | SyntaxKind::ImportType
            | SyntaxKind::ThisType
            | SyntaxKind::InferType
            | SyntaxKind::MappedType
            | SyntaxKind::TypePredicate => return false,
            SyntaxKind::Identifier if support::scope_reference(item) => {
                let parent = item.parent();
                let meaning = if parent.kind() == SyntaxKind::QualifiedName {
                    SymbolFlags::Namespace
                } else if parent.kind() == SyntaxKind::TypeReference {
                    SymbolFlags::Type
                } else {
                    return false;
                };
                let name = item.text();
                let symbol = support::resolve_at(ctx, name, item, meaning);
                if symbol.is_nil() {
                    return false;
                }
                out.push(FreeName {
                    name: name.to_owned(),
                    meaning,
                    symbol,
                });
            }
            _ => {}
        }
    }
    true
}

struct Walker<'a, 'b, 'c> {
    ctx: &'a mut Ctx<'c>,
    facts: &'b mut Facts,
    params: &'b HashMap<SymbolId, usize>,
    /// The target's own symbols (binding and function expression name).
    own: [SymbolId; 2],
    events: Vec<Event>,
    free: Vec<FreeName>,
    uses: Vec<(usize, Use)>,
    branching: bool,
    writes: Vec<SymbolId>,
}

impl Walker<'_, '_, '_> {
    fn identifier(
        &mut self,
        node: Node,
        conditional: bool,
        shorthand: bool,
    ) -> Result<(), Rejection> {
        let name = node.text();
        if name == "arguments" {
            return Err((Reason::ObservableArity, node));
        }
        let raw = self.ctx.checker.get_symbol_at_location(node);
        let local = if shorthand {
            self.ctx
                .checker
                .get_shorthand_assignment_value_symbol(node.parent())
        } else {
            raw
        };
        if let Some(&param) = self.params.get(&local) {
            self.events.push(Event::Param(param, conditional));
            let outer = super::super::analysis::walk_up_outer(node);
            let parent = outer.parent();
            self.uses.push((
                param,
                Use {
                    node,
                    shorthand,
                    callee: support::callee_position(node),
                    member_object: matches!(
                        parent.kind(),
                        SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
                    ) && parent.expression() == outer,
                },
            ));
            return Ok(());
        }
        let symbol = support::resolve_at(self.ctx, name, node, SymbolFlags::Value);
        if symbol.is_nil() {
            return Err((Reason::UnresolvedCall, node));
        }
        let own: Vec<SymbolId> = self
            .own
            .iter()
            .filter(|s| s.is_some())
            .map(|&s| support::norm(self.ctx, s))
            .collect();
        if own.contains(&symbol) {
            return Err((Reason::Recursion, node));
        }
        self.free.push(FreeName {
            name: name.to_owned(),
            meaning: SymbolFlags::Value,
            symbol,
        });
        if !self.facts.stable_binding(self.ctx, node) {
            self.events.push(Event::Effect);
        }
        Ok(())
    }

    fn types(&mut self, node: Node) -> Result<(), Rejection> {
        if !type_names(self.ctx, node, &mut self.free) {
            return Err((
                detail(Reason::UnsupportedSyntax, "expression:type-names"),
                node,
            ));
        }
        Ok(())
    }

    /// The target of an assignment or update: a property or element of any
    /// supported expression, or a free binding, which means the same binding
    /// at every site (hygiene). Writing a parameter would write the caller's
    /// argument instead, and destructuring patterns are unsupported.
    /// Evaluates the reference's parts and, for compound forms, the read.
    fn target(&mut self, target: Node, conditional: bool, reads: bool) -> Result<(), Rejection> {
        let mut target = target;
        while target.kind() == SyntaxKind::ParenthesizedExpression {
            target = target.expression();
        }
        match target.kind() {
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
                if target.expression().kind() != SyntaxKind::SuperKeyword
                    && !target.flags().intersects(NodeFlags::OptionalChain) =>
            {
                self.walk(target.expression(), conditional)?;
                if target.kind() == SyntaxKind::ElementAccessExpression {
                    self.walk(target.argument_expression(), conditional)?;
                }
            }
            SyntaxKind::Identifier => {
                let symbol = self.ctx.checker.get_symbol_at_location(target);
                if self.params.contains_key(&symbol) {
                    return Err((
                        detail(Reason::UnsupportedSyntax, "expression:parameter-write"),
                        target,
                    ));
                }
                self.identifier(target, conditional, false)?;
                if let Some(free) = self.free.last() {
                    self.writes.push(free.symbol);
                }
            }
            _ => {
                return Err((
                    detail(Reason::UnsupportedSyntax, "expression:assignment-pattern"),
                    target,
                ));
            }
        }
        if reads {
            // A compound form reads the reference (a getter may run).
            self.events.push(Event::Effect);
        }
        Ok(())
    }

    fn assignment(&mut self, node: Node, conditional: bool) -> Result<(), Rejection> {
        let operator = node.operator_token().kind();
        let logical = matches!(
            operator,
            SyntaxKind::AmpersandAmpersandEqualsToken
                | SyntaxKind::BarBarEqualsToken
                | SyntaxKind::QuestionQuestionEqualsToken
        );
        if logical {
            self.branching = true;
        }
        self.target(
            node.left(),
            conditional,
            operator != SyntaxKind::EqualsToken,
        )?;
        // A logical assignment evaluates its value only when it writes.
        self.walk(node.right(), conditional || logical)?;
        self.events.push(Event::Effect);
        Ok(())
    }

    fn update(&mut self, node: Node, conditional: bool) -> Result<(), Rejection> {
        self.target(node.operand(), conditional, true)?;
        self.events.push(Event::Effect);
        Ok(())
    }

    fn walk(&mut self, node: Node, conditional: bool) -> Result<(), Rejection> {
        // Inside an optional chain everything after its base is
        // conditional.
        let chained = conditional || node.flags().intersects(NodeFlags::OptionalChain);
        if node.flags().intersects(NodeFlags::OptionalChain)
            || node.kind() == SyntaxKind::ConditionalExpression
            || (node.kind() == SyntaxKind::BinaryExpression
                && matches!(
                    node.operator_token().kind(),
                    SyntaxKind::AmpersandAmpersandToken
                        | SyntaxKind::BarBarToken
                        | SyntaxKind::QuestionQuestionToken
                ))
        {
            self.branching = true;
        }
        match node.kind() {
            SyntaxKind::ParenthesizedExpression | SyntaxKind::NonNullExpression => {
                self.walk(node.expression(), conditional)
            }
            SyntaxKind::AsExpression | SyntaxKind::SatisfiesExpression => {
                self.types(node.type_())?;
                self.walk(node.expression(), conditional)
            }
            SyntaxKind::TypeAssertionExpression => {
                self.types(node.type_())?;
                self.walk(node.expression(), conditional)
            }
            SyntaxKind::Identifier => self.identifier(node, conditional, false),
            SyntaxKind::NumericLiteral
            | SyntaxKind::StringLiteral
            | SyntaxKind::BigIntLiteral
            | SyntaxKind::NoSubstitutionTemplateLiteral
            | SyntaxKind::RegularExpressionLiteral
            | SyntaxKind::TrueKeyword
            | SyntaxKind::FalseKeyword
            | SyntaxKind::NullKeyword => Ok(()),
            SyntaxKind::TemplateExpression => {
                for span in node.template_spans().nodes().iter() {
                    self.walk(span.expression(), conditional)?;
                    // String conversion may call user code.
                    self.events.push(Event::Effect);
                }
                Ok(())
            }
            SyntaxKind::PropertyAccessExpression => {
                if node.expression().kind() == SyntaxKind::SuperKeyword {
                    return Err((Reason::Inheritance, node));
                }
                self.walk(node.expression(), conditional)?;
                self.events.push(Event::Effect);
                Ok(())
            }
            SyntaxKind::ElementAccessExpression => {
                if node.expression().kind() == SyntaxKind::SuperKeyword {
                    return Err((Reason::Inheritance, node));
                }
                self.walk(node.expression(), conditional)?;
                self.walk(node.argument_expression(), chained)?;
                self.events.push(Event::Effect);
                Ok(())
            }
            SyntaxKind::CallExpression | SyntaxKind::NewExpression => {
                let callee = node.expression();
                if matches!(
                    callee.kind(),
                    SyntaxKind::SuperKeyword | SyntaxKind::ImportKeyword
                ) {
                    return Err((
                        detail(Reason::UnsupportedSyntax, "expression:super-or-import-call"),
                        node,
                    ));
                }
                for argument in node.type_arguments().iter() {
                    self.types(argument)?;
                }
                self.walk(callee, conditional)?;
                for argument in node.arguments().iter() {
                    if argument.kind() == SyntaxKind::SpreadElement {
                        self.walk(argument.expression(), chained)?;
                        self.events.push(Event::Effect);
                    } else {
                        self.walk(argument, chained)?;
                    }
                }
                self.events.push(Event::Effect);
                Ok(())
            }
            SyntaxKind::BinaryExpression => {
                let operator = node.operator_token().kind();
                match operator {
                    SyntaxKind::AmpersandAmpersandToken
                    | SyntaxKind::BarBarToken
                    | SyntaxKind::QuestionQuestionToken => {
                        self.walk(node.left(), conditional)?;
                        self.walk(node.right(), true)
                    }
                    SyntaxKind::CommaToken
                    | SyntaxKind::EqualsEqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsEqualsToken => {
                        self.walk(node.left(), conditional)?;
                        self.walk(node.right(), conditional)
                    }
                    _ if is_assignment_operator(operator) => self.assignment(node, conditional),
                    _ => {
                        self.walk(node.left(), conditional)?;
                        self.walk(node.right(), conditional)?;
                        // Coercion, `in` and `instanceof` may run user code
                        // or throw.
                        self.events.push(Event::Effect);
                        Ok(())
                    }
                }
            }
            SyntaxKind::ConditionalExpression => {
                self.walk(node.condition(), conditional)?;
                self.walk(node.when_true(), true)?;
                self.walk(node.when_false(), true)
            }
            SyntaxKind::PostfixUnaryExpression => self.update(node, conditional),
            SyntaxKind::PrefixUnaryExpression
                if matches!(
                    node.operator(),
                    SyntaxKind::PlusPlusToken | SyntaxKind::MinusMinusToken
                ) =>
            {
                self.update(node, conditional)
            }
            SyntaxKind::PrefixUnaryExpression => match node.operator() {
                SyntaxKind::ExclamationToken => self.walk(node.operand(), conditional),
                SyntaxKind::MinusToken | SyntaxKind::PlusToken | SyntaxKind::TildeToken => {
                    self.walk(node.operand(), conditional)?;
                    self.events.push(Event::Effect);
                    Ok(())
                }
                operator => Err((
                    detail(
                        Reason::UnsupportedSyntax,
                        format!("expression:prefix:{operator:?}"),
                    ),
                    node,
                )),
            },
            SyntaxKind::TypeOfExpression | SyntaxKind::VoidExpression => {
                self.walk(node.expression(), conditional)
            }
            SyntaxKind::ArrayLiteralExpression => {
                for element in node.elements().iter() {
                    match element.kind() {
                        SyntaxKind::OmittedExpression => {}
                        SyntaxKind::SpreadElement => {
                            self.walk(element.expression(), conditional)?;
                            self.events.push(Event::Effect);
                        }
                        _ => self.walk(element, conditional)?,
                    }
                }
                Ok(())
            }
            SyntaxKind::ObjectLiteralExpression => {
                for property in node.properties().iter() {
                    match property.kind() {
                        SyntaxKind::PropertyAssignment => {
                            let name = property.name();
                            if name.kind() == SyntaxKind::ComputedPropertyName {
                                self.walk(name.expression(), conditional)?;
                                self.events.push(Event::Effect);
                            } else if name.text() == "__proto__" {
                                return Err((Reason::PrototypeMutation, property));
                            }
                            self.walk(property.initializer(), conditional)?;
                        }
                        SyntaxKind::ShorthandPropertyAssignment => {
                            if property.object_assignment_initializer().is_some() {
                                return Err((
                                    detail(
                                        Reason::UnsupportedSyntax,
                                        "expression:shorthand-initializer",
                                    ),
                                    property,
                                ));
                            }
                            self.identifier(property.name(), conditional, true)?;
                        }
                        SyntaxKind::SpreadAssignment => {
                            self.walk(property.expression(), conditional)?;
                            self.events.push(Event::Effect);
                        }
                        kind => {
                            return Err((
                                detail(
                                    Reason::UnsupportedSyntax,
                                    format!("expression:property:{kind:?}"),
                                ),
                                property,
                            ));
                        }
                    }
                }
                Ok(())
            }
            kind => Err((
                detail(Reason::UnsupportedSyntax, format!("expression:{kind:?}")),
                node,
            )),
        }
    }
}

fn is_assignment_operator(operator: SyntaxKind) -> bool {
    operator == SyntaxKind::EqualsToken || is_compound_assignment(operator)
}

/// A planned call site: its edit and whether it nests in another site.
struct Site {
    file: usize,
    call: Node,
    replacement: String,
    /// Specifiers to add to existing imports of the site module.
    imports: Vec<(Node, String)>,
}

/// Specifiers added to import declarations, keyed by declaration.
type Additions = std::collections::BTreeMap<(usize, usize), (usize, Node, Vec<String>)>;

fn add_imports(additions: &mut Additions, file: usize, imports: &[(Node, String)]) {
    for (declaration, specifier) in imports {
        let entry = additions
            .entry((file, Ctx::start(*declaration)))
            .or_insert_with(|| (file, *declaration, Vec::new()));
        if !entry.2.contains(specifier) {
            entry.2.push(specifier.clone());
        }
    }
}

/// Insertions after the last named import of each declaration.
fn addition_edits(ctx: &Ctx, additions: &Additions) -> Vec<FileEdit> {
    additions
        .values()
        .filter_map(|(file, declaration, names)| {
            let last = declaration
                .import_clause()
                .named_bindings()
                .elements()
                .last()?;
            Some(FileEdit::insert(
                &ctx.files[*file].name,
                last.end() as usize,
                format!(", {}", names.join(", ")),
            ))
        })
        .collect()
}

/// Call sites of one target and whether its references allow removal.
struct Planned {
    sites: Vec<Site>,
    specifiers: Vec<(usize, Node)>,
    removable: bool,
}

fn collect_sites(ctx: &mut Ctx, facts: &mut Facts, target: &Target, out: &mut Plan) -> Planned {
    let refs = ctx.refs(target.symbol).to_vec();
    let mut sites = Vec::new();
    let mut removable = target.statement.is_some();
    let mut specifiers = Vec::new();
    for reference in refs {
        match reference.kind {
            RefKind::Declaration => {}
            RefKind::Call => {
                let call = super::super::analysis::walk_up_outer(reference.node).parent();
                match site(ctx, facts, target, reference.file, reference.node, call) {
                    Ok((replacement, imports)) => sites.push(Site {
                        file: reference.file,
                        call,
                        replacement,
                        imports,
                    }),
                    Err(reason) => {
                        removable = false;
                        let key = site_key(ctx, target, reference.file, call);
                        out.skips
                            .push(ctx.skip(PASS, key, reference.file, call, reason));
                    }
                }
            }
            RefKind::Alias if reference.node.parent().kind() == SyntaxKind::ImportSpecifier => {
                specifiers.push((reference.file, reference.node.parent()));
            }
            _ => removable = false,
        }
    }
    Planned {
        sites,
        specifiers,
        removable,
    }
}

fn plan_target(
    ctx: &mut Ctx,
    facts: &mut Facts,
    enabled: &PassSet,
    target: &Target,
    planned: Planned,
    removable: bool,
    out: &mut Plan,
) -> usize {
    let Planned {
        sites,
        specifiers,
        removable: references_removable,
    } = planned;
    let mut removable = removable && references_removable;
    if sites.is_empty() {
        return 0;
    }
    // Sites nested in another site's arguments overlap; plan them again
    // after the outer one is inlined.
    let nested: Vec<bool> = sites
        .iter()
        .map(|inner| {
            sites.iter().any(|outer| {
                outer.file == inner.file
                    && outer.call != inner.call
                    && Ctx::start(outer.call) <= Ctx::start(inner.call)
                    && inner.call.end() <= outer.call.end()
            })
        })
        .collect();
    if nested.iter().any(|&n| n) {
        removable = false;
    }
    let exported = has_syntactic_modifier(
        if target.statement.is_some() {
            target.statement
        } else {
            target.shape.declaration
        },
        ModifierFlags::Export,
    );
    if removable
        && enabled.contains(PassId::ResidualDependencyRemoval)
        && ctx.is_internal(target.symbol)
        && !(exported && facts.namespace_observed(ctx, target.file))
        && let Some(removal) = {
            let mut additions = Additions::new();
            for site in &sites {
                add_imports(&mut additions, site.file, &site.imports);
            }
            removal_edits(ctx, target, &specifiers, additions)
        }
    {
        let key = function_key(ctx, target.file, target.shape.name);
        let mut candidate = Candidate::new(PASS, key, ctx.location(target.file, target.shape.name));
        candidate.uses.push(PassId::ResidualDependencyRemoval);
        for site in &sites {
            candidate.edits.push(FileEdit::replace(
                &ctx.files[site.file].name,
                Ctx::start(site.call),
                site.call.end() as usize,
                site.replacement.clone(),
            ));
        }
        candidate.edits.extend(removal);
        out.candidates.push(candidate);
        return 1;
    }
    let mut count = 0;
    for (site, nested) in sites.iter().zip(nested) {
        if nested {
            continue;
        }
        let key = site_key(ctx, target, site.file, site.call);
        let mut candidate = Candidate::new(PASS, key, ctx.location(site.file, site.call));
        candidate.edits.push(FileEdit::replace(
            &ctx.files[site.file].name,
            Ctx::start(site.call),
            site.call.end() as usize,
            site.replacement.clone(),
        ));
        let mut additions = Additions::new();
        add_imports(&mut additions, site.file, &site.imports);
        candidate.edits.extend(addition_edits(ctx, &additions));
        out.candidates.push(candidate);
        count += 1;
    }
    count
}

/// Same-named local functions may share a module, so keys carry the
/// declaration offset.
fn function_key(ctx: &Ctx, file: usize, name: Node) -> String {
    format!(
        "function:{}:{}@{}",
        ctx.files[file].name,
        name.text(),
        Ctx::start(name)
    )
}

fn site_key(ctx: &Ctx, target: &Target, file: usize, call: Node) -> String {
    format!(
        "site:{}:{}@{}:{}",
        ctx.files[target.file].name,
        target.name,
        ctx.files[file].name,
        Ctx::start(call)
    )
}

/// Edits deleting the declaration and every import specifier of it, or
/// `None` when an import cannot be removed exactly.
fn removal_edits(
    ctx: &mut Ctx,
    target: &Target,
    specifiers: &[(usize, Node)],
    mut additions: Additions,
) -> Option<Vec<FileEdit>> {
    let parent = target.statement.parent().kind();
    if !matches!(parent, SyntaxKind::SourceFile | SyntaxKind::Block) {
        return None;
    }
    let mut edits = vec![FileEdit::delete(
        &ctx.files[target.file].name,
        support::removal_start(ctx, target.file, target.statement),
        target.statement.end() as usize,
    )];
    let mut seen = std::collections::HashSet::new();
    for &(file, specifier) in specifiers {
        let named = specifier.parent();
        let clause = named.parent();
        let declaration = clause.parent();
        if !seen.insert(declaration) {
            // Several specifiers of one declaration.
            return None;
        }
        let name = &ctx.files[file].name.clone();
        let elements: Vec<Node> = named.elements().to_vec();
        if let Some((_, _, names)) = additions.remove(&(file, Ctx::start(declaration))) {
            // The new specifiers take the removed one's place.
            edits.push(FileEdit::replace(
                name,
                Ctx::start(specifier),
                specifier.end() as usize,
                names.join(", "),
            ));
        } else if elements.len() > 1 {
            let position = elements.iter().position(|&e| e == specifier)?;
            if position + 1 < elements.len() {
                edits.push(FileEdit::delete(
                    name,
                    Ctx::start(specifier),
                    Ctx::start(elements[position + 1]),
                ));
            } else {
                edits.push(FileEdit::delete(
                    name,
                    elements[position - 1].end() as usize,
                    specifier.end() as usize,
                ));
            }
        } else if clause.name().is_some() {
            edits.push(FileEdit::delete(
                name,
                clause.name().end() as usize,
                named.end() as usize,
            ));
        } else {
            // The whole declaration goes. Keep the dependency's evaluation
            // unless its initialization is proven unobservable.
            let target_module = Facts::target(ctx, declaration.module_specifier())?;
            if ctx.module_initialization_pure(target_module) {
                edits.push(FileEdit::delete(
                    name,
                    support::removal_start(ctx, file, declaration),
                    declaration.end() as usize,
                ));
            } else {
                edits.push(FileEdit::delete(
                    name,
                    Ctx::start(clause),
                    Ctx::start(declaration.module_specifier()),
                ));
            }
        }
    }
    edits.extend(addition_edits(ctx, &additions));
    support::keep_module_syntax(ctx, &mut edits);
    Some(edits)
}

/// Plans one call site; returns the replacement text of the call.
fn site(
    ctx: &mut Ctx,
    facts: &mut Facts,
    target: &Target,
    file: usize,
    reference: Node,
    call: Node,
) -> Result<(String, Vec<(Node, String)>), Reason> {
    if call.kind() != SyntaxKind::CallExpression
        || call.flags().intersects(NodeFlags::OptionalChain)
        || call.question_dot_token().is_some()
    {
        return Err(detail(
            Reason::UnsupportedSyntax,
            if call.kind() == SyntaxKind::CallExpression {
                "site:optional-call"
            } else {
                "site:not-a-call"
            },
        ));
    }
    let mut callee = call.expression();
    while callee.kind() == SyntaxKind::ParenthesizedExpression {
        callee = callee.expression();
    }
    if callee != reference {
        // Type assertions on the callee change the call's type.
        return Err(detail(Reason::TypeConstraint, "site:asserted-callee"));
    }
    if target.local
        && !reached_after(target.shape.declaration, call)
        && !(target.top_level && site_runs_after(ctx, facts, target.shape.declaration, call))
    {
        return Err(Reason::ModuleInitialization);
    }
    // Recursion: the site lies inside the target itself.
    let mut ancestor = call.parent();
    while ancestor.is_some() {
        if ancestor == target.shape.function {
            return Err(Reason::Recursion);
        }
        ancestor = ancestor.parent();
    }
    let expression = skip_outer(target.shape.expression);
    let outer = super::super::analysis::walk_up_outer(call);
    if outer.parent().kind() == SyntaxKind::DeleteExpression
        || (support::callee_position(call)
            && matches!(
                expression.kind(),
                SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
            ))
    {
        return Err(Reason::ObservableOperation);
    }
    let arguments: Vec<Node> = call.arguments().to_vec();
    if arguments
        .iter()
        .any(|a| a.kind() == SyntaxKind::SpreadElement)
    {
        return Err(detail(Reason::UnsupportedSyntax, "site:spread-argument"));
    }
    // A missing argument is `undefined`. Bundle chunks substitute
    // `void 0` for it; checked code would need the parameter's type.
    let typed = !crate::app::bundle_mode();
    if typed && arguments.len() < target.params.len() {
        return Err(detail(Reason::UnsupportedSyntax, "site:missing-arguments"));
    }
    for &extra in arguments.get(target.params.len()..).unwrap_or_default() {
        if !facts.movable(ctx, extra, false) {
            return Err(detail(Reason::SideEffects, "site:extra-argument-effect"));
        }
    }
    // A written binding must be the site module's own: an import of it (an
    // existing one, or one the hygiene step below would add) is read-only,
    // so the assignment would throw where the function wrote its binding.
    for &written in &target.writes {
        let declaration = ctx.checker.sym(written).value_declaration;
        if declaration.is_nil() || ctx.file_of_node(declaration) != Some(file) {
            return Err(detail(Reason::Mutation, "site:write-to-imported-binding"));
        }
    }
    // Hygiene: every free name means the same binding here, or a module
    // the site already imports at runtime exports it under that name.
    let mut imports: Vec<(Node, String)> = Vec::new();
    for free in &target.free {
        if support::resolve_at(ctx, &free.name, call, free.meaning) != free.symbol
            || (free.meaning == SymbolFlags::Value
                && support::type_only_alias_at(ctx, &free.name, call))
        {
            let addition = import_for(ctx, facts, file, free)
                .ok_or_else(|| detail(Reason::UnsupportedSyntax, "site:free-name-not-visible"))?;
            if !imports.contains(&addition) {
                imports.push(addition);
            }
        }
    }
    let mut effectful = Vec::new();
    let mut read_only = vec![false; target.params.len()];
    let mut asserted = vec![false; target.params.len()];
    for (index, param) in target.params.iter().enumerate() {
        // `void 0` is a stable literal without effects.
        let Some(&argument) = arguments.get(index) else {
            continue;
        };
        let uses = param.uses.len();
        let parameter_type = ctx.checker.get_type_of_symbol(param.symbol);
        let argument_type = ctx.checker.get_type_at_location(argument);
        // Context-sensitive arguments were typed against the parameter.
        let same_type =
            !typed || (parameter_type == argument_type && !support::context_sensitive(argument));
        let stable = facts.movable(ctx, argument, true);
        // A side-effect-free read of a binding that may change later.
        read_only[index] = !stable
            && uses > 0
            && argument.kind() == SyntaxKind::Identifier
            && ctx.is_pure(argument);
        let assertable =
            param.annotation.is_some() && names_resolve(ctx, &param.annotation_names, call);
        match uses {
            0 => {
                if !facts.movable(ctx, argument, false) {
                    return Err(detail(Reason::SideEffects, "site:unused-argument-effect"));
                }
            }
            1 => {
                if !read_only[index] && !facts.movable(ctx, argument, false) {
                    effectful.push(index);
                }
                if !same_type {
                    if !assertable {
                        return Err(detail(Reason::TypeConstraint, "site:argument-type"));
                    }
                    asserted[index] = true;
                }
            }
            _ => {
                if !stable && !read_only[index] {
                    return Err(detail(
                        Reason::SideEffects,
                        "site:repeated-argument-unstable",
                    ));
                }
                if !same_type {
                    // Separate assertions would break narrowing between
                    // the uses.
                    if target.branching || !assertable {
                        return Err(detail(
                            Reason::TypeConstraint,
                            "site:repeated-argument-type",
                        ));
                    }
                    asserted[index] = true;
                }
            }
        }
        if param.uses.iter().any(|u| u.callee)
            && matches!(
                skip_outer(argument).kind(),
                SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
            )
        {
            return Err(Reason::ObservableOperation);
        }
    }
    // Arguments with effects keep their order relative to each other and
    // to every effect of the expression.
    // Reads of bindings that may change happen before anything that could
    // change them, and only when no argument has effects.
    if !effectful.is_empty() && read_only.iter().any(|&r| r) {
        return Err(detail(Reason::SideEffects, "site:effect-with-mutable-read"));
    }
    let mut next = 0;
    let mut effect_seen = false;
    for event in &target.events {
        match *event {
            Event::Param(index, conditional) => {
                if read_only[index]
                    && effect_seen
                    && !private_to_site_function(ctx, target, arguments[index], call)
                {
                    return Err(detail(
                        Reason::SideEffects,
                        "site:mutable-read-after-effect",
                    ));
                }
                if let Some(position) = effectful.iter().position(|&e| e == index) {
                    if conditional {
                        return Err(detail(
                            Reason::SideEffects,
                            "site:conditional-effectful-argument",
                        ));
                    }
                    if position != next {
                        return Err(detail(
                            Reason::SideEffects,
                            "site:reordered-effectful-argument",
                        ));
                    }
                    next += 1;
                }
            }
            Event::Effect => {
                effect_seen = true;
                if next < effectful.len() {
                    return Err(detail(Reason::SideEffects, "site:effect-before-argument"));
                }
            }
        }
    }
    if target.retype && !names_resolve(ctx, &target.return_names, call) {
        return Err(detail(Reason::TypeConstraint, "site:return-names"));
    }
    // Build the replacement from the declaration's current text.
    let source = &ctx.files[target.file].text;
    let start = Ctx::start(target.shape.expression);
    let end = target.shape.expression.end() as usize;
    let mut replacements: Vec<(usize, usize, String)> = Vec::new();
    for (index, param) in target.params.iter().enumerate() {
        let Some(&argument) = arguments.get(index) else {
            for value in &param.uses {
                const UNDEFINED: &str = "void 0";
                let wrapped = if value.shorthand {
                    format!("{}: {UNDEFINED}", value.node.text())
                } else if support::accepts_assignment_expression(value.node, UNDEFINED) {
                    UNDEFINED.to_owned()
                } else {
                    format!("({UNDEFINED})")
                };
                replacements.push((Ctx::start(value.node), value.node.end() as usize, wrapped));
            }
            continue;
        };
        let text = ctx.text(file, argument).to_owned();
        for value in &param.uses {
            let mut wrapped = if asserted[index] {
                assertion(&text, argument, ctx.text(target.file, param.annotation))
            } else if needs_parens(argument, &text, value) {
                format!("({text})")
            } else {
                text.clone()
            };
            if value.shorthand {
                wrapped = format!("{}: {}", value.node.text(), wrapped);
            }
            replacements.push((Ctx::start(value.node), value.node.end() as usize, wrapped));
        }
    }
    replacements.sort_by_key(|r| r.0);
    let mut text = String::new();
    let mut cursor = start;
    for (from, to, replacement) in replacements {
        text.push_str(&source[cursor..from]);
        text.push_str(&replacement);
        cursor = to;
    }
    text.push_str(&source[cursor..end]);
    let bare_parameter = target
        .params
        .iter()
        .any(|p| p.uses.iter().any(|u| u.node == expression));
    let primary = if let Some(param) = target
        .params
        .iter()
        .position(|p| p.uses.iter().any(|u| u.node == expression))
    {
        // The expression is a bare parameter: the argument decides.
        !asserted[param]
            && arguments
                .get(param)
                .is_some_and(|&argument| support::primary(skip_outer(argument)))
    } else {
        support::primary(target.shape.expression)
    };
    let text = if target.retype {
        let annotation = target.shape.function.type_();
        let operand = if bare_parameter && !primary {
            format!("({text})")
        } else {
            text
        };
        assertion(&operand, expression, ctx.text(target.file, annotation))
    } else if primary
        || (!support::comma(expression) && support::accepts_assignment_expression(call, &text))
        || (!bare_parameter && support::fits_operand(call, expression))
    {
        text
    } else {
        format!("({text})")
    };
    if support::asi_hazard(ctx, file, call, &text) {
        return Err(detail(Reason::UnsupportedSyntax, "site:asi-hazard"));
    }
    Ok((text, imports))
}

/// An existing named import declaration of `file` whose module exports the
/// free name's binding under the same name, with the specifier to add. The
/// name must not occur anywhere in `file`, so it shadows nothing.
fn import_for(
    ctx: &mut Ctx,
    facts: &mut Facts,
    file: usize,
    free: &FreeName,
) -> Option<(Node, String)> {
    if facts.mentions(ctx, file, &free.name) {
        return None;
    }
    let statements: Vec<Node> = ctx.files[file].node.statements().to_vec();
    for statement in statements {
        if statement.kind() != SyntaxKind::ImportDeclaration {
            continue;
        }
        let clause = statement.import_clause();
        if clause.is_nil() {
            continue;
        }
        let bindings = clause.named_bindings();
        if bindings.is_nil()
            || bindings.kind() != SyntaxKind::NamedImports
            || bindings.elements().is_empty()
        {
            continue;
        }
        let module = ctx
            .checker
            .get_symbol_at_location(statement.module_specifier());
        if module.is_nil() {
            continue;
        }
        let exports = ctx.checker.get_exports_of_symbol(module);
        let exported = ctx.checker.symbols.get(exports, &free.name);
        if exported.is_nil() || support::norm(ctx, exported) != free.symbol {
            continue;
        }
        let value = free.meaning == SymbolFlags::Value;
        if value {
            // Only an import that already evaluates its module at runtime.
            if clause.is_type_only() || !support::runtime_dependency(ctx, file, statement) {
                continue;
            }
            return Some((statement, free.name.clone()));
        }
        let text = if clause.is_type_only() {
            free.name.clone()
        } else {
            format!("type {}", free.name)
        };
        return Some((statement, text));
    }
    None
}

fn names_resolve(ctx: &mut Ctx, names: &[FreeName], location: Node) -> bool {
    names
        .iter()
        .all(|n| support::resolve_at(ctx, &n.name, location, n.meaning) == n.symbol)
}

/// Whether an argument needs parentheses at a parameter's position.
fn needs_parens(argument: Node, text: &str, value: &Use) -> bool {
    if value.member_object && argument.kind() == SyntaxKind::NumericLiteral {
        return true;
    }
    if !support::comma(argument)
        && (value.shorthand || support::accepts_assignment_expression(value.node, text))
    {
        return false;
    }
    if support::fits_operand(value.node, argument) {
        return false;
    }
    !support::primary(argument)
        && !matches!(
            argument.kind(),
            SyntaxKind::NumericLiteral | SyntaxKind::BigIntLiteral
        )
}

/// `(text as annotation)`, parenthesizing operands that bind looser than
/// `as`.
fn assertion(text: &str, operand: Node, annotation: &str) -> String {
    if support::precedence(operand) > 11 {
        format!("({text} as {annotation})")
    } else {
        format!("(({text}) as {annotation})")
    }
}

/// Functions judged during one proof. A function being judged again adds no
/// new way to run early, so it counts as late; a "late" answer that relied on
/// such an assumption is not cached, a "not late" answer always is.
#[derive(Default)]
struct Proof {
    visiting: Vec<Node>,
    done: Vec<(Node, bool)>,
    assumptions: usize,
}

/// Where code that contains a node runs from.
enum RunContext {
    /// Straight-line top-level code of the module.
    Top,
    /// The body of a function-like node.
    Function(Node),
    /// Class field initializers and static blocks, which run when a class
    /// is defined or instantiated.
    Unknown,
}

fn run_context(node: Node) -> RunContext {
    let mut current = node.parent();
    while current.is_some() {
        if is_function_like(current) {
            return RunContext::Function(current);
        }
        match current.kind() {
            SyntaxKind::SourceFile => return RunContext::Top,
            SyntaxKind::PropertyDeclaration | SyntaxKind::ClassStaticBlockDeclaration => {
                return RunContext::Unknown;
            }
            _ => current = current.parent(),
        }
    }
    RunContext::Unknown
}

/// Whether the call site can only run after the top-level declaration of its
/// callee initialized the binding. The site runs late when it follows the
/// declaration in top-level code, lies in another module outside the
/// declaration's import cycle, or sits in a function that no code before the
/// declaration can reach: a function created after it, or one whose every
/// reference is itself late (a function declaration is hoisted, so its
/// references decide). Methods and accessors count as reachable at any time.
fn site_runs_after(ctx: &mut Ctx, facts: &mut Facts, declaration: Node, site: Node) -> bool {
    let Some(file) = ctx.file_of_node(declaration) else {
        return false;
    };
    // Cross-module ordering rests on the module graph, which only ES modules
    // have: script files share one global scope in an order the graph does
    // not describe.
    if !is_external_module(ctx.files[file].node) {
        return false;
    }
    let mut visiting = Proof::default();
    node_runs_after(ctx, facts, declaration, file, site, &mut visiting)
}

fn node_runs_after(
    ctx: &mut Ctx,
    facts: &mut Facts,
    declaration: Node,
    file: usize,
    node: Node,
    visiting: &mut Proof,
) -> bool {
    if ctx.file_of_node(node) != Some(file) {
        return match ctx.file_of_node(node) {
            Some(other) => {
                is_external_module(ctx.files[other].node) && !facts.same_component(ctx, file, other)
            }
            None => false,
        };
    }
    match run_context(node) {
        RunContext::Top => Ctx::start(node) >= declaration.end() as usize,
        RunContext::Function(function) => {
            function_runs_after(ctx, facts, declaration, file, function, visiting)
        }
        RunContext::Unknown => false,
    }
}

fn function_runs_after(
    ctx: &mut Ctx,
    facts: &mut Facts,
    declaration: Node,
    file: usize,
    function: Node,
    visiting: &mut Proof,
) -> bool {
    if let Some(&(_, late)) = visiting.done.iter().find(|(f, _)| *f == function) {
        return late;
    }
    // A function that is already being judged adds no new way to run early.
    if visiting.visiting.contains(&function) {
        visiting.assumptions += 1;
        return true;
    }
    if matches!(
        function.parent().kind(),
        SyntaxKind::MethodDeclaration
            | SyntaxKind::Constructor
            | SyntaxKind::GetAccessor
            | SyntaxKind::SetAccessor
    ) || matches!(
        function.kind(),
        SyntaxKind::MethodDeclaration
            | SyntaxKind::Constructor
            | SyntaxKind::GetAccessor
            | SyntaxKind::SetAccessor
    ) || function.parent().kind() == SyntaxKind::ModuleBlock
        || (function.parent().kind() == SyntaxKind::VariableDeclaration
            && function.parent().initializer() == function
            && function.parent().parent().parent().parent().kind() == SyntaxKind::ModuleBlock
            && has_syntactic_modifier(function.parent().parent().parent(), ModifierFlags::Export)
            && Ctx::start(function) < declaration.end() as usize)
    {
        // Exported namespace members are reachable through the namespace
        // object, including functions stored in variable declarations.
        return false;
    }
    let before = visiting.assumptions;
    visiting.visiting.push(function);
    let late = judge_function(ctx, facts, declaration, file, function, visiting);
    visiting.visiting.pop();
    if !late || visiting.assumptions == before {
        visiting.done.push((function, late));
    }
    late
}

fn judge_function(
    ctx: &mut Ctx,
    facts: &mut Facts,
    declaration: Node,
    file: usize,
    function: Node,
    visiting: &mut Proof,
) -> bool {
    let name = match function.kind() {
        SyntaxKind::FunctionDeclaration => function.name(),
        _ => {
            // Created after the declaration, so it cannot run before it.
            if node_runs_after(ctx, facts, declaration, file, function, visiting) {
                return true;
            }
            let binding = function.parent();
            if binding.kind() == SyntaxKind::VariableDeclaration
                && binding.initializer() == function
            {
                binding.name()
            } else {
                return false;
            }
        }
    };
    if name.is_nil() || name.kind() != SyntaxKind::Identifier {
        return false;
    }
    let symbol = ctx.resolve(name);
    if symbol.is_nil() {
        return false;
    }
    // Exported bindings are reached by importers, through names or through
    // the namespace object. Importers run after this module finished unless
    // one shares its import cycle, so an exported function needs a module
    // that is alone in its component.
    let statement = if function.kind() == SyntaxKind::FunctionDeclaration {
        function
    } else {
        function.parent().parent().parent()
    };
    let exported = ctx
        .refs(symbol)
        .iter()
        .any(|r| r.kind == RefKind::Alias && r.file == file)
        || (statement.parent().kind() == SyntaxKind::SourceFile
            && has_syntactic_modifier(statement, ModifierFlags::Export));
    if exported && !facts.alone_in_component(ctx, file) {
        return false;
    }
    let references: Vec<_> = ctx
        .refs(symbol)
        .iter()
        .filter(|r| {
            matches!(
                r.kind,
                RefKind::Call | RefKind::New | RefKind::Read | RefKind::Alias
            )
        })
        .copied()
        .collect();
    references.iter().all(|reference| {
        reference.kind == RefKind::Alias
            || node_runs_after(ctx, facts, declaration, file, reference.node, visiting)
    })
}

/// Whether `site` runs only after the local declaration initialized its
/// binding: it follows the declaration in the same function body, without
/// crossing into a closure that could run earlier.
fn reached_after(declaration: Node, site: Node) -> bool {
    fn function_of(mut node: Node) -> Node {
        loop {
            node = node.parent();
            if node.is_nil() || is_function_like(node) || node.kind() == SyntaxKind::SourceFile {
                return node;
            }
        }
    }
    Ctx::start(site) >= declaration.end() as usize && function_of(site) == function_of(declaration)
}
