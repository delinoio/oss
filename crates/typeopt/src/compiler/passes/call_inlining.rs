// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `call-inlining`: replaces direct calls of small resolved functions with
//! their single returned expression.
//!
//! Supported targets are function declarations and `const` arrow or
//! function expressions whose body is one `return <expr>;` (or an
//! expression body). The planner proves, per target:
//!
//! - one declaration, no writes to the binding, no type parameters, no
//!   rest/default/destructured/`this` parameters, not async or a generator, no
//!   type-predicate return;
//! - a `const` binding is never read in its temporal dead zone: it is a
//!   top-level declaration of a module outside any static import cycle and no
//!   earlier top-level statement or initializer runs code;
//! - the expression contains only supported operators (no nested functions or
//!   classes, `this`, `super`, `arguments`, meta properties, `delete`,
//!   assignments, `await`/`yield`, tagged templates or JSX) and no recursion.
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
//!   return type or is asserted to its annotation.
//!
//! When every reference is an inlined call or an internal import
//! specifier, the bundle also removes the declaration and those
//! specifiers. That removal belongs to `residual-dependency-removal` and is
//! only proposed while that pass is enabled; otherwise every call site is
//! a separate candidate that must reduce size on its own.
use std::collections::HashMap;

use anyhow::Result;
use ts_goport::prelude::*;

use super::{
    super::analysis::{Ctx, RefKind, skip_outer},
    calls_support::{self as support, Facts},
};
use crate::{
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Plan, Reason},
};

const PASS: PassId = PassId::CallInlining;

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
    let mut candidates = 0;
    for target in &targets {
        crate::app::check_cancelled()?;
        candidates += plan_target(ctx, &mut facts, enabled, target, out);
    }
    tracing::debug!(
        pass = %PASS,
        targets = targets.len(),
        candidates,
        "call inlining planned"
    );
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
            || function.modifier_flags().intersects(ModifierFlags::ASYNC)
            || !function.type_parameters().is_empty()
            || !function.decorators().is_empty()
        {
            return reject(Reason::UnsupportedSyntax);
        }
        let return_annotation = function.type_();
        if return_annotation.is_some() && return_annotation.kind() == SyntaxKind::TypePredicate {
            return reject(Reason::TypeConstraint);
        }
        let symbol = ctx.resolve(shape.name);
        if symbol.is_nil() {
            return reject(Reason::UnresolvedCall);
        }
        if ctx.checker.sym(symbol).declarations.len() != 1 {
            return reject(Reason::DeclarationMerging);
        }
        if ctx.refs(symbol).iter().any(|r| r.kind == RefKind::Write) {
            return reject(Reason::Mutation);
        }
        let statement = match shape.declaration.kind() {
            SyntaxKind::VariableDeclaration => {
                let list = shape.declaration.parent();
                if !list.flags().intersects(NodeFlags::CONST)
                    || list.flags().intersects(NodeFlags::USING)
                    || shape.declaration.type_().is_some()
                {
                    return reject(Reason::Mutation);
                }
                if !facts.initialized_early(ctx, symbol) {
                    return reject(Reason::ModuleInitialization);
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
                return reject(Reason::UnsupportedSyntax);
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
        };
        walker.walk(shape.expression, false)?;
        let Walker {
            events,
            free,
            uses,
            branching,
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
        let retype = return_type != expression_type || (contextual && return_annotation.is_some());
        let mut return_names = Vec::new();
        if (contextual && return_annotation.is_nil())
            || (retype
                && (return_annotation.is_nil()
                    || !type_names(ctx, return_annotation, &mut return_names)))
        {
            return Err((Reason::TypeConstraint, shape.expression));
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
            statement,
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
                    SymbolFlags::NAMESPACE
                } else if parent.kind() == SyntaxKind::TypeReference {
                    SymbolFlags::TYPE
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
        let raw = self.ctx.checker.get_symbol_at_location(node, true);
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
        let symbol = support::resolve_at(self.ctx, name, node, SymbolFlags::VALUE);
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
            meaning: SymbolFlags::VALUE,
            symbol,
        });
        if !self.facts.stable_binding(self.ctx, node) {
            self.events.push(Event::Effect);
        }
        Ok(())
    }

    fn types(&mut self, node: Node) -> Result<(), Rejection> {
        if !type_names(self.ctx, node, &mut self.free) {
            return Err((Reason::UnsupportedSyntax, node));
        }
        Ok(())
    }

    fn walk(&mut self, node: Node, conditional: bool) -> Result<(), Rejection> {
        // Inside an optional chain everything after its base is
        // conditional.
        let chained = conditional || node.flags().intersects(NodeFlags::OPTIONAL_CHAIN);
        if node.flags().intersects(NodeFlags::OPTIONAL_CHAIN)
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
                    return Err((Reason::UnsupportedSyntax, node));
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
                    _ if is_assignment_operator(operator) => Err((Reason::UnsupportedSyntax, node)),
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
            SyntaxKind::PrefixUnaryExpression => match node.operator() {
                SyntaxKind::ExclamationToken => self.walk(node.operand(), conditional),
                SyntaxKind::MinusToken | SyntaxKind::PlusToken | SyntaxKind::TildeToken => {
                    self.walk(node.operand(), conditional)?;
                    self.events.push(Event::Effect);
                    Ok(())
                }
                _ => Err((Reason::UnsupportedSyntax, node)),
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
                                return Err((Reason::UnsupportedSyntax, property));
                            }
                            self.identifier(property.name(), conditional, true)?;
                        }
                        SyntaxKind::SpreadAssignment => {
                            self.walk(property.expression(), conditional)?;
                            self.events.push(Event::Effect);
                        }
                        _ => return Err((Reason::UnsupportedSyntax, property)),
                    }
                }
                Ok(())
            }
            SyntaxKind::ThisKeyword => Err((Reason::UnsupportedSyntax, node)),
            _ => Err((Reason::UnsupportedSyntax, node)),
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
}

fn plan_target(
    ctx: &mut Ctx,
    facts: &mut Facts,
    enabled: &PassSet,
    target: &Target,
    out: &mut Plan,
) -> usize {
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
                    Ok(replacement) => sites.push(Site {
                        file: reference.file,
                        call,
                        replacement,
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
        ModifierFlags::EXPORT,
    );
    if removable
        && enabled.contains(PassId::ResidualDependencyRemoval)
        && ctx.is_internal(target.symbol)
        && !(exported && facts.namespace_observed(ctx, target.file))
        && let Some(removal) = removal_edits(ctx, facts, target, &specifiers)
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
    facts: &mut Facts,
    target: &Target,
    specifiers: &[(usize, Node)],
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
        if elements.len() > 1 {
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
        let _ = facts;
    }
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
) -> Result<String, Reason> {
    if call.kind() != SyntaxKind::CallExpression
        || call.flags().intersects(NodeFlags::OPTIONAL_CHAIN)
        || call.question_dot_token().is_some()
    {
        return Err(Reason::UnsupportedSyntax);
    }
    let mut callee = call.expression();
    while callee.kind() == SyntaxKind::ParenthesizedExpression {
        callee = callee.expression();
    }
    if callee != reference {
        // Type assertions on the callee change the call's type.
        return Err(Reason::TypeConstraint);
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
        return Err(Reason::UnsupportedSyntax);
    }
    if arguments.len() < target.params.len() {
        return Err(Reason::UnsupportedSyntax);
    }
    for &extra in &arguments[target.params.len()..] {
        if !facts.movable(ctx, extra, false) {
            return Err(Reason::SideEffects);
        }
    }
    // Hygiene: every free name means the same binding here.
    for free in &target.free {
        if support::resolve_at(ctx, &free.name, call, free.meaning) != free.symbol
            || (free.meaning == SymbolFlags::VALUE
                && support::type_only_alias_at(ctx, &free.name, call))
        {
            return Err(Reason::UnsupportedSyntax);
        }
    }
    let mut effectful = Vec::new();
    let mut read_only = vec![false; target.params.len()];
    let mut asserted = vec![false; target.params.len()];
    for (index, param) in target.params.iter().enumerate() {
        let argument = arguments[index];
        let uses = param.uses.len();
        let parameter_type = ctx.checker.get_type_of_symbol(param.symbol);
        let argument_type = ctx.checker.get_type_at_location(argument);
        let same_type = parameter_type == argument_type;
        let stable = facts.movable(ctx, argument, true);
        // Context-sensitive arguments were typed against the parameter.
        let same_type = same_type && !support::context_sensitive(argument);
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
                    return Err(Reason::SideEffects);
                }
            }
            1 => {
                if !read_only[index] && !facts.movable(ctx, argument, false) {
                    effectful.push(index);
                }
                if !same_type {
                    if !assertable {
                        return Err(Reason::TypeConstraint);
                    }
                    asserted[index] = true;
                }
            }
            _ => {
                if !stable && !read_only[index] {
                    return Err(Reason::SideEffects);
                }
                if !same_type {
                    // Separate assertions would break narrowing between
                    // the uses.
                    if target.branching || !assertable {
                        return Err(Reason::TypeConstraint);
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
        return Err(Reason::SideEffects);
    }
    let mut next = 0;
    let mut effect_seen = false;
    for event in &target.events {
        match *event {
            Event::Param(index, conditional) => {
                if read_only[index] && effect_seen {
                    return Err(Reason::SideEffects);
                }
                if let Some(position) = effectful.iter().position(|&e| e == index) {
                    if conditional || position != next {
                        return Err(Reason::SideEffects);
                    }
                    next += 1;
                }
            }
            Event::Effect => {
                effect_seen = true;
                if next < effectful.len() {
                    return Err(Reason::SideEffects);
                }
            }
        }
    }
    if target.retype && !names_resolve(ctx, &target.return_names, call) {
        return Err(Reason::TypeConstraint);
    }
    // Build the replacement from the declaration's current text.
    let source = &ctx.files[target.file].text;
    let start = Ctx::start(target.shape.expression);
    let end = target.shape.expression.end() as usize;
    let mut replacements: Vec<(usize, usize, String)> = Vec::new();
    for (index, param) in target.params.iter().enumerate() {
        let argument = arguments[index];
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
        !asserted[param] && support::primary(skip_outer(arguments[param]))
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
        return Err(Reason::UnsupportedSyntax);
    }
    Ok(text)
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
