// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Module-initialization facts for temporal-dead-zone proofs: whether a
//! top-level statement can execute application code while its module
//! initializes.
//!
//! A statement "runs application code" when evaluating it may call an
//! application function, constructor, getter, setter, coercion hook or
//! iterator, directly or through external code that receives an
//! application value. Reading bindings (even ones that may throw) and
//! creating functions do not run application code. Everything unknown
//! counts as running application code.
use std::collections::HashSet;

use super::super::analysis::Ctx;
use crate::compiler::api::*;

pub(super) struct InitCheck<'a, 'c> {
    pub ctx: &'a mut Ctx<'c>,
    /// Constants whose initializer is being examined (cycle guard).
    visiting: HashSet<SymbolId>,
}

impl<'a, 'c> InitCheck<'a, 'c> {
    pub fn new(ctx: &'a mut Ctx<'c>) -> Self {
        Self {
            ctx,
            visiting: HashSet::new(),
        }
    }

    /// Whether every declaration of the symbol is outside the application.
    fn external(&mut self, symbol: SymbolId) -> bool {
        if symbol.is_nil() || self.ctx.checker.is_unknown_symbol(symbol) {
            return false;
        }
        let declarations = self.ctx.checker.sym(symbol).declarations.to_vec();
        !declarations.is_empty()
            && declarations
                .iter()
                .all(|&d| self.ctx.file_of_node(d).is_none())
    }

    /// Whether a top-level statement may run application code while its
    /// module initializes. Static imports are hoisted and handled by the
    /// import graph, so they never count here.
    pub fn statement_runs_app_code(&mut self, statement: Node) -> bool {
        if statement.flags().intersects(NodeFlags::Ambient)
            || has_syntactic_modifier(statement, ModifierFlags::Ambient)
        {
            return false;
        }
        match statement.kind() {
            SyntaxKind::ImportDeclaration
            | SyntaxKind::ExportDeclaration
            | SyntaxKind::InterfaceDeclaration
            | SyntaxKind::TypeAliasDeclaration
            | SyntaxKind::FunctionDeclaration
            | SyntaxKind::EmptyStatement => false,
            SyntaxKind::ExportAssignment => self.app_code(statement.expression()),
            SyntaxKind::ExpressionStatement => self.app_code(statement.expression()),
            SyntaxKind::VariableStatement => {
                let declarations: Vec<Node> =
                    statement.declaration_list().declarations().nodes().to_vec();
                declarations.into_iter().any(|d| {
                    d.name().kind() != SyntaxKind::Identifier
                        || (d.initializer().is_some() && self.app_code(d.initializer()))
                })
            }
            SyntaxKind::EnumDeclaration => {
                let members: Vec<Node> = statement.members().to_vec();
                members.into_iter().any(|m| {
                    (m.initializer().is_some() && self.app_code(m.initializer()))
                        || m.name().kind() == SyntaxKind::ComputedPropertyName
                })
            }
            SyntaxKind::ClassDeclaration => self.class_runs_app_code(statement),
            _ => true,
        }
    }

    fn class_runs_app_code(&mut self, class: Node) -> bool {
        if !class.decorators().is_empty() {
            return true;
        }
        let clauses: Vec<Node> = class.heritage_clauses().nodes().to_vec();
        for clause in clauses {
            if clause.token() != SyntaxKind::ExtendsKeyword {
                continue;
            }
            let types: Vec<Node> = clause.types().nodes().to_vec();
            for item in types {
                if self.app_code(item.expression()) {
                    return true;
                }
            }
        }
        let members: Vec<Node> = class.members().to_vec();
        for member in members {
            if !member.decorators().is_empty()
                || member.kind() == SyntaxKind::ClassStaticBlockDeclaration
            {
                return true;
            }
            let name = member.name();
            if name.is_some()
                && name.kind() == SyntaxKind::ComputedPropertyName
                && (self.app_code(name.expression()) || !self.inert(name.expression()))
            {
                return true;
            }
            if member.kind() == SyntaxKind::PropertyDeclaration
                && is_static(member)
                && member.initializer().is_some()
                && self.app_code(member.initializer())
            {
                return true;
            }
        }
        false
    }

    /// Whether evaluating the expression may run application code.
    pub fn app_code(&mut self, node: Node) -> bool {
        match node.kind() {
            SyntaxKind::ParenthesizedExpression
            | SyntaxKind::AsExpression
            | SyntaxKind::SatisfiesExpression
            | SyntaxKind::TypeAssertionExpression
            | SyntaxKind::NonNullExpression
            | SyntaxKind::TypeOfExpression
            | SyntaxKind::VoidExpression => self.app_code(node.expression()),
            SyntaxKind::NumericLiteral
            | SyntaxKind::StringLiteral
            | SyntaxKind::BigIntLiteral
            | SyntaxKind::NoSubstitutionTemplateLiteral
            | SyntaxKind::RegularExpressionLiteral
            | SyntaxKind::TrueKeyword
            | SyntaxKind::FalseKeyword
            | SyntaxKind::NullKeyword
            | SyntaxKind::ThisKeyword
            | SyntaxKind::Identifier
            | SyntaxKind::MetaProperty
            | SyntaxKind::FunctionExpression
            | SyntaxKind::ArrowFunction => false,
            SyntaxKind::ClassExpression => self.class_runs_app_code(node),
            SyntaxKind::TemplateExpression => node
                .template_spans()
                .nodes()
                .to_vec()
                .into_iter()
                .any(|span| self.app_code(span.expression()) || !self.inert(span.expression())),
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
                let base = node.expression();
                if base.kind() == SyntaxKind::MetaProperty {
                    return false;
                }
                if !self.external_value(base) || self.app_code(base) {
                    return true;
                }
                node.kind() == SyntaxKind::ElementAccessExpression
                    && (self.app_code(node.argument_expression())
                        || !self.inert(node.argument_expression()))
            }
            SyntaxKind::CallExpression | SyntaxKind::NewExpression => {
                let callee = node.expression();
                if !self.external_callee(callee) {
                    return true;
                }
                let arguments: Vec<Node> = node.arguments().to_vec();
                arguments.into_iter().any(|a| {
                    a.kind() == SyntaxKind::SpreadElement || self.app_code(a) || !self.inert(a)
                })
            }
            SyntaxKind::BinaryExpression => {
                let operator = node.operator_token().kind();
                let (left, right) = (node.left(), node.right());
                match operator {
                    SyntaxKind::EqualsEqualsEqualsToken
                    | SyntaxKind::ExclamationEqualsEqualsToken
                    | SyntaxKind::AmpersandAmpersandToken
                    | SyntaxKind::BarBarToken
                    | SyntaxKind::QuestionQuestionToken
                    | SyntaxKind::CommaToken => self.app_code(left) || self.app_code(right),
                    SyntaxKind::InstanceOfKeyword | SyntaxKind::InKeyword => true,
                    _ if operator == SyntaxKind::EqualsToken
                        || is_compound_assignment(operator) =>
                    {
                        true
                    }
                    // Coercing operators only call hooks of non-inert
                    // operands.
                    _ => {
                        self.app_code(left)
                            || self.app_code(right)
                            || !self.inert(left)
                            || !self.inert(right)
                    }
                }
            }
            SyntaxKind::PrefixUnaryExpression => match node.operator() {
                SyntaxKind::ExclamationToken => self.app_code(node.operand()),
                SyntaxKind::MinusToken | SyntaxKind::PlusToken | SyntaxKind::TildeToken => {
                    self.app_code(node.operand()) || !self.inert(node.operand())
                }
                _ => true,
            },
            SyntaxKind::ConditionalExpression => {
                self.app_code(node.condition())
                    || self.app_code(node.when_true())
                    || self.app_code(node.when_false())
            }
            SyntaxKind::ArrayLiteralExpression => {
                node.elements()
                    .to_vec()
                    .into_iter()
                    .any(|e| match e.kind() {
                        SyntaxKind::OmittedExpression => false,
                        SyntaxKind::SpreadElement => true,
                        _ => self.app_code(e),
                    })
            }
            SyntaxKind::ObjectLiteralExpression => {
                node.properties().to_vec().into_iter().any(|p| {
                    let name = p.name();
                    let computed = name.is_some()
                        && name.kind() == SyntaxKind::ComputedPropertyName
                        && (self.app_code(name.expression()) || !self.inert(name.expression()));
                    computed
                        || match p.kind() {
                            SyntaxKind::PropertyAssignment => self.app_code(p.initializer()),
                            SyntaxKind::ShorthandPropertyAssignment
                            | SyntaxKind::MethodDeclaration
                            | SyntaxKind::GetAccessor
                            | SyntaxKind::SetAccessor => false,
                            _ => true,
                        }
                })
            }
            _ => true,
        }
    }

    /// The callee is an external function reached through external values
    /// (`Math.max`, `new Map`, `React.createContext`), so the call itself
    /// starts no application code.
    fn external_callee(&mut self, callee: Node) -> bool {
        match callee.kind() {
            SyntaxKind::ParenthesizedExpression | SyntaxKind::NonNullExpression => {
                self.external_callee(callee.expression())
            }
            SyntaxKind::Identifier => {
                let symbol = self.ctx.resolve(callee);
                self.external(symbol)
            }
            SyntaxKind::PropertyAccessExpression => {
                let symbol = self.ctx.resolve(callee.name());
                self.external(symbol) && self.external_value(callee.expression())
            }
            _ => false,
        }
    }

    /// Values that originate outside the application: external bindings
    /// and properties read from them. Application code cannot have stored
    /// application functions in them during initialization because such
    /// stores would have run application-reaching statements.
    fn external_value(&mut self, node: Node) -> bool {
        match node.kind() {
            SyntaxKind::ParenthesizedExpression | SyntaxKind::NonNullExpression => {
                self.external_value(node.expression())
            }
            SyntaxKind::Identifier => {
                let symbol = self.ctx.resolve(node);
                self.external(symbol)
            }
            SyntaxKind::PropertyAccessExpression => self.external_value(node.expression()),
            _ => false,
        }
    }

    /// Whether the value cannot lead external code or coercion into
    /// application code: primitives, external values and literal
    /// containers of inert values without functions.
    pub fn inert(&mut self, node: Node) -> bool {
        match node.kind() {
            SyntaxKind::ParenthesizedExpression
            | SyntaxKind::AsExpression
            | SyntaxKind::SatisfiesExpression
            | SyntaxKind::TypeAssertionExpression
            | SyntaxKind::NonNullExpression => self.inert(node.expression()),
            SyntaxKind::NumericLiteral
            | SyntaxKind::StringLiteral
            | SyntaxKind::BigIntLiteral
            | SyntaxKind::NoSubstitutionTemplateLiteral
            | SyntaxKind::RegularExpressionLiteral
            | SyntaxKind::TrueKeyword
            | SyntaxKind::FalseKeyword
            | SyntaxKind::NullKeyword
            | SyntaxKind::TemplateExpression
            | SyntaxKind::TypeOfExpression
            | SyntaxKind::VoidExpression => true,
            // Results are always primitives.
            SyntaxKind::BinaryExpression => match node.operator_token().kind() {
                SyntaxKind::AmpersandAmpersandToken
                | SyntaxKind::BarBarToken
                | SyntaxKind::QuestionQuestionToken => {
                    self.inert(node.left()) && self.inert(node.right())
                }
                SyntaxKind::CommaToken => self.inert(node.right()),
                operator => {
                    !(operator == SyntaxKind::EqualsToken || is_compound_assignment(operator))
                }
            },
            SyntaxKind::PrefixUnaryExpression => true,
            SyntaxKind::ConditionalExpression => {
                self.inert(node.when_true()) && self.inert(node.when_false())
            }
            SyntaxKind::Identifier => {
                // Only the global `undefined`; a shadowing binding is an
                // ordinary application value.
                if self.ctx.is_global_undefined(node) {
                    return true;
                }
                let symbol = self.ctx.resolve(node);
                if self.external(symbol) {
                    return true;
                }
                // An application constant with an inert initializer.
                let declaration = if symbol.is_nil() {
                    Node::NIL
                } else {
                    self.ctx.checker.sym(symbol).value_declaration
                };
                if declaration.is_nil()
                    || declaration.kind() != SyntaxKind::VariableDeclaration
                    || !declaration.parent().flags().intersects(NodeFlags::Const)
                    || declaration.initializer().is_nil()
                    || !self.visiting.insert(symbol)
                {
                    return false;
                }
                let inert = self.inert(declaration.initializer());
                self.visiting.remove(&symbol);
                inert
            }
            SyntaxKind::PropertyAccessExpression => self.external_value(node),
            SyntaxKind::CallExpression | SyntaxKind::NewExpression => {
                // Fresh results of external code that received only inert
                // values.
                self.external_callee(node.expression()) && !self.app_code(node)
            }
            SyntaxKind::ArrayLiteralExpression => node.elements().to_vec().into_iter().all(|e| {
                e.kind() == SyntaxKind::OmittedExpression
                    || (e.kind() != SyntaxKind::SpreadElement && self.inert(e))
            }),
            SyntaxKind::ObjectLiteralExpression => {
                node.properties()
                    .to_vec()
                    .into_iter()
                    .all(|p| match p.kind() {
                        SyntaxKind::PropertyAssignment => {
                            p.name().kind() != SyntaxKind::ComputedPropertyName
                                && self.inert(p.initializer())
                        }
                        SyntaxKind::ShorthandPropertyAssignment => self.inert(p.name()),
                        _ => false,
                    })
            }
            _ => false,
        }
    }
}
