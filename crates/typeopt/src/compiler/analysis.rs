// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Shared whole-program facts for pass planners. Everything here is computed
//! from the current program text; nothing survives a round, so accepted
//! changes always invalidate and recompute every fact.
//!
//! Facts distinguish type-derived candidates from runtime evidence: symbols,
//! references and syntax are runtime evidence; checker types only nominate
//! candidates and never prove a closed world on their own.
use std::collections::{HashMap, HashSet};

use anyhow::Result;

use super::{collect, is_js, token_start};
use crate::{
    compiler::api::*,
    plan::{ImportSite, Location, Reason, Skip},
};

pub(crate) struct SourceInfo {
    pub node: Node,
    pub name: String,
    pub text: String,
    pub js: bool,
    pub entry: bool,
    /// Every node of the file in preorder.
    pub nodes: Vec<Node>,
}

/// How an identifier refers to a resolved symbol.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum RefKind {
    /// The declaration's own name.
    Declaration,
    /// An import or export specifier, or an import clause: a module-level
    /// alias that does not observe the value by itself.
    Alias,
    /// Callee of a direct call `f(...)`.
    Call,
    /// Target of `new C(...)`.
    New,
    /// Assignment or update target.
    Write,
    /// Inside a type position or `typeof` type query; erased from output.
    Type,
    /// Any other runtime read (the value may flow anywhere).
    Read,
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct Ref {
    pub node: Node,
    pub file: usize,
    pub kind: RefKind,
}

/// A computed element access whose key is not a literal.
#[derive(Clone, Copy, Debug)]
pub(crate) struct DynamicAccess {
    pub node: Node,
    pub file: usize,
}

pub(crate) struct Ctx<'c> {
    pub checker: &'c mut Checker,
    /// Application modules (TypeScript and pass-through JavaScript) in
    /// deterministic path order.
    pub files: Vec<SourceInfo>,
    /// Declarations and other non-application program files.
    pub externals: Vec<Node>,
    file_index: HashMap<String, usize>,
    /// Alias-resolved symbol of every application identifier.
    pub references: HashMap<SymbolId, Vec<Ref>>,
    /// Symbols observable through public entry exports.
    pub public: HashSet<SymbolId>,
    /// Symbols that application JavaScript (an unchecked boundary)
    /// references.
    pub js_referenced: HashSet<SymbolId>,
    /// Computed accesses with non-literal keys in application TypeScript.
    pub dynamic_accesses: Vec<DynamicAccess>,
    /// Every property-like name mentioned in application code: member
    /// names, accesses, object keys, binding keys and string literals.
    pub mentioned_names: HashMap<String, usize>,
    external_names: Option<HashSet<String>>,
    pure_modules: HashMap<usize, Option<bool>>,
}

impl<'c> Ctx<'c> {
    pub fn new(
        checker: &'c mut Checker,
        files: Vec<Node>,
        externals: Vec<Node>,
        entries: &[String],
    ) -> Result<Self> {
        let mut infos: Vec<SourceInfo> = files
            .into_iter()
            .map(|node| {
                let name = source_file_info(node).file_name.clone();
                let mut nodes = Vec::new();
                collect(node, &mut nodes);
                SourceInfo {
                    node,
                    entry: entries.contains(&name),
                    // `@ts-nocheck` suppresses the semantic recheck that
                    // backs every transformation, so such a module is an
                    // unchecked boundary exactly like JavaScript.
                    js: if crate::app::bundle_mode() {
                        crate::app::bundle_boundary(
                            &name,
                            super::has_esm_statements(node) || super::uses_import_meta(&nodes),
                        ) || super::uses_eval_or_with(&nodes)
                    } else {
                        is_js(node) || is_unchecked(node)
                    },
                    text: source_file_text(node).to_string(),
                    name,
                    nodes,
                }
            })
            .collect();
        infos.sort_by(|a, b| a.name.cmp(&b.name));
        let file_index = infos
            .iter()
            .enumerate()
            .map(|(i, f)| (f.name.clone(), i))
            .collect();
        let mut ctx = Self {
            checker,
            files: infos,
            externals,
            file_index,
            references: HashMap::new(),
            public: HashSet::new(),
            js_referenced: HashSet::new(),
            dynamic_accesses: Vec::new(),
            mentioned_names: HashMap::new(),
            external_names: None,
            pure_modules: HashMap::new(),
        };
        ctx.index()?;
        Ok(ctx)
    }

    fn index(&mut self) -> Result<()> {
        for index in 0..self.files.len() {
            crate::app::check_cancelled()?;
            let js = self.files[index].js;
            let nodes = std::mem::take(&mut self.files[index].nodes);
            for &node in &nodes {
                match node.kind() {
                    SyntaxKind::Identifier => {
                        let symbol = self.resolve(node);
                        if symbol.is_nil() {
                            continue;
                        }
                        if js {
                            self.js_referenced.insert(symbol);
                            continue;
                        }
                        let kind = ref_kind(node);
                        self.references.entry(symbol).or_default().push(Ref {
                            node,
                            file: index,
                            kind,
                        });
                    }
                    SyntaxKind::ElementAccessExpression if !js => {
                        let key = skip_outer(node.argument_expression());
                        if !matches!(
                            key.kind(),
                            SyntaxKind::StringLiteral
                                | SyntaxKind::NumericLiteral
                                | SyntaxKind::NoSubstitutionTemplateLiteral
                        ) {
                            self.dynamic_accesses
                                .push(DynamicAccess { node, file: index });
                        }
                    }
                    _ => {}
                }
                if !js && let Some(name) = mentioned_name(node) {
                    *self.mentioned_names.entry(name.to_owned()).or_default() += 1;
                }
                // Bundled code can parse strings into member lookups at
                // runtime (template engines, `new Function`, path helpers),
                // so every identifier-like token of a string counts.
                if crate::app::bundle_mode()
                    && matches!(
                        node.kind(),
                        SyntaxKind::StringLiteral
                            | SyntaxKind::NoSubstitutionTemplateLiteral
                            | SyntaxKind::TemplateHead
                            | SyntaxKind::TemplateMiddle
                            | SyntaxKind::TemplateTail
                    )
                {
                    for token in node
                        .text()
                        .split(|c: char| !(c.is_alphanumeric() || c == '_' || c == '$'))
                        .filter(|t| !t.is_empty())
                    {
                        *self.mentioned_names.entry(token.to_owned()).or_default() += 1;
                    }
                }
            }
            self.files[index].nodes = nodes;
        }
        // Public entry exports, including `export *` re-exports, and every
        // symbol a JavaScript module imports form external contracts.
        for index in 0..self.files.len() {
            if !self.files[index].entry {
                continue;
            }
            let file = self.files[index].node;
            if file.symbol().is_nil() {
                continue;
            }
            let module = self.checker.get_merged_symbol_exported(file.symbol());
            for export in self.checker.get_exports_of_module_exported(module) {
                let target = self.skip_alias(export);
                self.public.insert(export);
                self.public.insert(target);
            }
        }
        tracing::debug!(
            symbols = self.references.len(),
            public = self.public.len(),
            js_referenced = self.js_referenced.len(),
            dynamic_accesses = self.dynamic_accesses.len(),
            "shared facts indexed"
        );
        Ok(())
    }

    pub fn file_of(&self, name: &str) -> Option<usize> {
        self.file_index.get(name).copied()
    }

    /// Alias-resolved symbol of an identifier, or nil.
    pub fn resolve(&mut self, node: Node) -> SymbolId {
        let parent = node.parent();
        let symbol =
            if parent.kind() == SyntaxKind::ShorthandPropertyAssignment && parent.name() == node {
                self.checker.get_shorthand_assignment_value_symbol(parent)
            } else if parent.kind() == SyntaxKind::ExportSpecifier && !is_module_export_name(node) {
                self.checker
                    .get_export_specifier_local_target_symbol(parent)
            } else {
                self.checker.get_symbol_at_location(node)
            };
        if symbol.is_nil() {
            return symbol;
        }
        self.skip_alias(symbol)
    }

    pub fn skip_alias(&mut self, symbol: SymbolId) -> SymbolId {
        if symbol.is_nil()
            || !self
                .checker
                .sym(symbol)
                .flags
                .intersects(SymbolFlags::Alias)
        {
            return symbol;
        }
        let target = self.checker.skip_alias_exported(symbol);
        if target.is_nil() || self.checker.is_unknown_symbol(target) {
            symbol
        } else {
            target
        }
    }

    pub fn refs(&self, symbol: SymbolId) -> &[Ref] {
        self.references.get(&symbol).map_or(&[], Vec::as_slice)
    }

    /// The symbol is declared only in application TypeScript, is not part of
    /// a public entry export, is not referenced from JavaScript and is not an
    /// ambient or merged declaration. Its every runtime use is therefore one
    /// of the indexed references.
    pub fn is_internal(&mut self, symbol: SymbolId) -> bool {
        if symbol.is_nil() || self.public.contains(&symbol) || self.js_referenced.contains(&symbol)
        {
            return false;
        }
        let declarations = self.checker.sym(symbol).declarations.to_vec();
        !declarations.is_empty()
            && declarations.iter().all(|&d| {
                let file = get_source_file_of_node(d);
                !d.flags().intersects(NodeFlags::Ambient)
                    && self
                        .file_of(&source_file_info(file).file_name)
                        .is_some_and(|i| !self.files[i].js)
            })
    }

    /// Property names declared by external declarations (libraries, lib
    /// files and project `.d.ts`). External code may read or call these by
    /// name on any object it receives.
    pub fn external_names(&mut self) -> &HashSet<String> {
        if self.external_names.is_none() {
            let mut names = HashSet::new();
            for &file in &self.externals {
                let mut nodes = Vec::new();
                collect(file, &mut nodes);
                for node in nodes {
                    if matches!(
                        node.kind(),
                        SyntaxKind::PropertySignature
                            | SyntaxKind::MethodSignature
                            | SyntaxKind::PropertyDeclaration
                            | SyntaxKind::MethodDeclaration
                            | SyntaxKind::GetAccessor
                            | SyntaxKind::SetAccessor
                            | SyntaxKind::PropertyAssignment
                    ) {
                        let name = node.name();
                        if matches!(
                            name.kind(),
                            SyntaxKind::Identifier
                                | SyntaxKind::StringLiteral
                                | SyntaxKind::NumericLiteral
                                | SyntaxKind::PrivateIdentifier
                        ) {
                            names.insert(name.text().to_owned());
                        }
                    }
                }
            }
            self.external_names = Some(names);
        }
        self.external_names.as_ref().unwrap()
    }

    /// Source text of a node without leading trivia.
    pub fn text(&self, file: usize, node: Node) -> &str {
        &self.files[file].text[token_start(node)..node.end() as usize]
    }

    pub fn start(node: Node) -> usize {
        token_start(node)
    }

    pub fn location(&self, file: usize, node: Node) -> Location {
        Location {
            file: self.files[file].name.clone(),
            offset: token_start(node),
        }
    }

    pub fn skip(
        &self,
        pass: crate::passes::PassId,
        key: impl Into<String>,
        file: usize,
        node: Node,
        reason: Reason,
    ) -> Skip {
        Skip {
            pass,
            key: key.into(),
            location: self.location(file, node),
            reason,
        }
    }

    /// Index of the file containing `node`.
    pub fn file_of_node(&self, node: Node) -> Option<usize> {
        self.file_of(&source_file_info(get_source_file_of_node(node)).file_name)
    }

    /// Whether `typeof name` evaluates without throwing. A name that
    /// resolves to no declaration, or only to ambient declarations (project
    /// `.d.ts` files, `declare` statements and libraries), creates no
    /// runtime lexical binding in application code, so `typeof` reads the
    /// host global and never throws. A declared binding of the application
    /// still throws while it is in its temporal dead zone and needs the
    /// ordinary identifier initialization proof. An imported binding is
    /// always an application binding: its declaration file may describe a
    /// module that initializes later in a cycle.
    fn typeof_operand_safe(&mut self, operand: Node) -> bool {
        let symbol = self.resolve(operand);
        if symbol.is_nil() {
            return true;
        }
        let imported = {
            let local = self.checker.get_symbol_at_location(operand);
            local.is_some() && self.checker.sym(local).flags.intersects(SymbolFlags::Alias)
        };
        if !imported {
            let declarations = self.checker.sym(symbol).declarations.to_vec();
            if !declarations.is_empty()
                && declarations.iter().all(|&d| {
                    d.flags().intersects(NodeFlags::Ambient)
                        || has_syntactic_modifier(d, ModifierFlags::Ambient)
                })
            {
                return true;
            }
            // Declaration kinds the ordinary identifier proof does not cover.
            let declaration = self.checker.sym(symbol).value_declaration;
            if declaration.is_some() {
                match declaration.kind() {
                    // A class binding is lexical: it is initialized only
                    // after its declaration ran in the same function.
                    SyntaxKind::ClassDeclaration => {
                        return declared_before(declaration, operand);
                    }
                    // A top-level non-const enum emits a hoisted `var`.
                    SyntaxKind::EnumDeclaration
                        if declaration.parent().kind() == SyntaxKind::SourceFile
                            && !has_syntactic_modifier(declaration, ModifierFlags::Const) =>
                    {
                        return true;
                    }
                    _ => {}
                }
            }
        }
        self.is_pure(operand)
    }

    /// Whether the expression evaluates without observable effects and
    /// without throwing. Conservative: unknown forms are impure.
    pub fn is_pure(&mut self, node: Node) -> bool {
        let node = skip_outer(node);
        match node.kind() {
            SyntaxKind::NumericLiteral
            | SyntaxKind::StringLiteral
            | SyntaxKind::BigIntLiteral
            | SyntaxKind::NoSubstitutionTemplateLiteral
            | SyntaxKind::TrueKeyword
            | SyntaxKind::FalseKeyword
            | SyntaxKind::NullKeyword
            | SyntaxKind::RegularExpressionLiteral
            | SyntaxKind::ArrowFunction
            | SyntaxKind::FunctionExpression => true,
            SyntaxKind::Identifier => {
                if self.is_global_undefined(node) {
                    return true;
                }
                // Reads of initialized locals are pure. Bindings that may be
                // in their temporal dead zone are not.
                let symbol = self.resolve(node);
                if symbol.is_nil() {
                    return false;
                }
                let flags = self.checker.sym(symbol).flags;
                if flags.intersects(SymbolFlags::Function) {
                    return true;
                }
                let declaration = self.checker.sym(symbol).value_declaration;
                if declaration.is_nil() {
                    return false;
                }
                match declaration.kind() {
                    SyntaxKind::Parameter => parameter_initialized(declaration, node),
                    SyntaxKind::VariableDeclaration => {
                        let list = declaration.parent();
                        let block_scoped = list
                            .flags()
                            .intersects(NodeFlags::Let | NodeFlags::Const | NodeFlags::Using);
                        // Declared earlier in the same file and not inside
                        // a loop body that could re-enter the TDZ.
                        !block_scoped || declared_before(declaration, node)
                    }
                    _ => false,
                }
            }
            SyntaxKind::TypeOfExpression => {
                let operand = skip_outer(node.expression());
                if operand.kind() == SyntaxKind::Identifier {
                    self.typeof_operand_safe(operand)
                } else {
                    self.is_pure(operand)
                }
            }
            SyntaxKind::VoidExpression => self.is_pure(node.expression()),
            SyntaxKind::PrefixUnaryExpression => {
                matches!(node.operator(), SyntaxKind::ExclamationToken)
                    && self.is_pure(node.operand())
            }
            SyntaxKind::ConditionalExpression => {
                self.is_pure(node.condition())
                    && self.is_pure(node.when_true())
                    && self.is_pure(node.when_false())
            }
            SyntaxKind::BinaryExpression => {
                let operator = node.operator_token().kind();
                matches!(
                    operator,
                    SyntaxKind::EqualsEqualsEqualsToken
                        | SyntaxKind::ExclamationEqualsEqualsToken
                        | SyntaxKind::AmpersandAmpersandToken
                        | SyntaxKind::BarBarToken
                        | SyntaxKind::QuestionQuestionToken
                        | SyntaxKind::CommaToken
                ) && self.is_pure(node.left())
                    && self.is_pure(node.right())
            }
            SyntaxKind::ArrayLiteralExpression => node
                .elements()
                .iter()
                .all(|e| e.kind() != SyntaxKind::SpreadElement && self.is_pure(e)),
            SyntaxKind::ObjectLiteralExpression => {
                node.properties().iter().all(|p| match p.kind() {
                    SyntaxKind::PropertyAssignment => {
                        p.name().kind() != SyntaxKind::ComputedPropertyName
                            && self.is_pure(p.initializer())
                    }
                    SyntaxKind::ShorthandPropertyAssignment => self.is_pure(p.name()),
                    SyntaxKind::MethodDeclaration => {
                        p.name().kind() != SyntaxKind::ComputedPropertyName
                    }
                    _ => false,
                })
            }
            _ => false,
        }
    }

    /// Application module that a module specifier resolves to, or `None`
    /// for external, JavaScript or unresolved modules. JSON is reported as
    /// `Some(None)`: it initializes without effects.
    fn target_module(&mut self, specifier: Node) -> Option<Option<usize>> {
        let symbol = self.checker.get_symbol_at_location(specifier);
        if symbol.is_nil() {
            return None;
        }
        let declarations = self.checker.sym(symbol).declarations.to_vec();
        let file = declarations.first().copied()?;
        let file = get_source_file_of_node(file);
        let name = source_file_info(file).file_name.clone();
        if name.ends_with(".json") {
            return Some(None);
        }
        let index = self.file_of(&name)?;
        (!self.files[index].js).then_some(Some(index))
    }

    /// Whether evaluating module `index` (and, transitively, its imports)
    /// has no observable effect, so skipping or reordering its
    /// initialization is unobservable. Cycles are conservatively impure.
    pub fn module_initialization_pure(&mut self, index: usize) -> bool {
        match self.pure_modules.get(&index) {
            Some(Some(pure)) => return *pure,
            Some(None) => return false,
            None => {}
        }
        self.pure_modules.insert(index, None);
        let statements: Vec<Node> = self.files[index].node.statements().iter().collect();
        let pure = statements
            .into_iter()
            .all(|statement| self.statement_pure(statement));
        self.pure_modules.insert(index, Some(pure));
        pure
    }

    fn statement_pure(&mut self, statement: Node) -> bool {
        if statement.flags().intersects(NodeFlags::Ambient)
            || has_syntactic_modifier(statement, ModifierFlags::Ambient)
        {
            return true;
        }
        match statement.kind() {
            SyntaxKind::InterfaceDeclaration
            | SyntaxKind::TypeAliasDeclaration
            | SyntaxKind::FunctionDeclaration
            | SyntaxKind::EmptyStatement => true,
            SyntaxKind::ImportDeclaration | SyntaxKind::ExportDeclaration => {
                let clause = if statement.kind() == SyntaxKind::ImportDeclaration {
                    statement.import_clause()
                } else {
                    Node::default()
                };
                if (clause.is_some() && clause.is_type_only())
                    || (statement.kind() == SyntaxKind::ExportDeclaration
                        && statement.is_type_only())
                {
                    return true;
                }
                let specifier = statement.module_specifier();
                if specifier.is_nil() {
                    return true;
                }
                match self.target_module(specifier) {
                    Some(None) => true,
                    Some(Some(target)) => self.module_initialization_pure(target),
                    None => false,
                }
            }
            SyntaxKind::ExportAssignment => self.is_pure(statement.expression()),
            // Directives such as "use client" evaluate a string literal.
            SyntaxKind::ExpressionStatement => {
                skip_outer(statement.expression()).kind() == SyntaxKind::StringLiteral
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
                        && (d.initializer().is_nil() || self.is_pure(d.initializer()))
                }),
            SyntaxKind::ClassDeclaration => {
                if !statement.decorators().is_empty() {
                    return false;
                }
                let heritage_pure = statement.heritage_clauses().nodes().iter().all(|clause| {
                    clause.token() == SyntaxKind::ImplementsKeyword
                        || clause
                            .types()
                            .nodes()
                            .iter()
                            .all(|t| self.initialized_class(statement, t.expression()))
                });
                heritage_pure
                    && statement
                        .members()
                        .iter()
                        .collect::<Vec<_>>()
                        .into_iter()
                        .all(|member| {
                            if !member.decorators().is_empty()
                                || (member.name().is_some()
                                    && member.name().kind() == SyntaxKind::ComputedPropertyName)
                            {
                                return false;
                            }
                            match member.kind() {
                                SyntaxKind::ClassStaticBlockDeclaration => false,
                                SyntaxKind::PropertyDeclaration if is_static(member) => {
                                    member.initializer().is_nil()
                                        || self.is_pure(member.initializer())
                                }
                                _ => true,
                            }
                        })
            }
            SyntaxKind::EnumDeclaration => statement.members().iter().all(|m| {
                let init = m.initializer();
                init.is_nil()
                    || matches!(
                        skip_outer(init).kind(),
                        SyntaxKind::NumericLiteral | SyntaxKind::StringLiteral
                    )
            }),
            _ => false,
        }
    }

    /// Value import declarations of every application TypeScript module,
    /// with droppability for the pipeline's initialization guard.
    pub fn import_sites(&mut self) -> std::collections::BTreeMap<String, Vec<ImportSite>> {
        let mut sites = std::collections::BTreeMap::new();
        for index in 0..self.files.len() {
            if self.files[index].js {
                continue;
            }
            let statements: Vec<Node> = self.files[index].node.statements().iter().collect();
            let mut list: Vec<ImportSite> = Vec::new();
            for statement in statements {
                if statement.kind() != SyntaxKind::ImportDeclaration {
                    continue;
                }
                let clause = statement.import_clause();
                if clause.is_nil() || clause.is_type_only() {
                    continue;
                }
                let specifier = statement.module_specifier();
                let text = specifier.text().to_owned();
                if list.iter().any(|s| s.specifier == text) {
                    continue;
                }
                let droppable = match self.target_module(specifier) {
                    Some(None) => true,
                    Some(Some(target)) => self.module_initialization_pure(target),
                    None => false,
                };
                list.push(ImportSite {
                    specifier: text,
                    offset: token_start(statement),
                    droppable,
                });
            }
            if !list.is_empty() {
                sites.insert(self.files[index].name.clone(), list);
            }
        }
        sites
    }

    /// Whether an identifier reads the global `undefined`, never an
    /// application binding that shadows the name.
    pub fn is_global_undefined(&mut self, node: Node) -> bool {
        if node.kind() != SyntaxKind::Identifier || node.text() != "undefined" {
            return false;
        }
        let symbol = self.checker.get_symbol_at_location(node);
        symbol.is_some() && self.checker.is_undefined_symbol(symbol)
    }

    /// Symbols of every name in a deterministic order.
    pub fn sorted_symbols(&self) -> Vec<SymbolId> {
        let mut symbols: Vec<_> = self.references.keys().copied().collect();
        symbols.sort_by_key(|s| s.0);
        symbols
    }

    /// Whether `name` is mentioned anywhere in application code other than
    /// at `except` occurrences.
    pub fn name_mentions(&self, name: &str) -> usize {
        self.mentioned_names.get(name).copied().unwrap_or(0)
    }

    /// Whether a class heritage expression evaluates to an initialized
    /// class without effects: an identifier bound to a class declaration
    /// that is either imported (its module's initialization is checked
    /// through the import, where cycles count as impure) or declared earlier
    /// in the same statement list. Any other value may be in its temporal
    /// dead zone or not be a constructor, which throws.
    fn initialized_class(&mut self, class: Node, expression: Node) -> bool {
        let expression = skip_outer(expression);
        if expression.kind() != SyntaxKind::Identifier {
            return false;
        }
        let symbol = self.resolve(expression);
        if symbol.is_nil() {
            return false;
        }
        let declaration = self.checker.sym(symbol).value_declaration;
        if declaration.is_nil()
            || declaration.kind() != SyntaxKind::ClassDeclaration
            || declaration.flags().intersects(NodeFlags::Ambient)
            || has_syntactic_modifier(declaration, ModifierFlags::Ambient)
        {
            return false;
        }
        // Ambient or external declarations are no runtime evidence.
        let file = get_source_file_of_node(declaration);
        if self
            .file_of(&source_file_info(file).file_name)
            .is_none_or(|index| self.files[index].js)
        {
            return false;
        }
        file != get_source_file_of_node(class)
            || (declaration.parent() == class.parent() && declaration.end() <= class.pos())
    }
}

/// Whether a TypeScript module disables semantic checking with a leading
/// `@ts-nocheck` comment.
pub(crate) fn is_unchecked(file: Node) -> bool {
    let text = source_file_text(file);
    let header_end = file
        .statements()
        .iter()
        .next()
        .map_or(text.len(), token_start);
    text[..header_end].contains("@ts-nocheck")
}

/// Removes parentheses, type assertions, `satisfies` and non-null wrappers,
/// none of which have runtime effects.
pub(crate) fn skip_outer(mut node: Node) -> Node {
    while matches!(
        node.kind(),
        SyntaxKind::ParenthesizedExpression
            | SyntaxKind::AsExpression
            | SyntaxKind::TypeAssertionExpression
            | SyntaxKind::SatisfiesExpression
            | SyntaxKind::NonNullExpression
    ) {
        node = node.expression();
    }
    node
}

fn is_module_export_name(node: Node) -> bool {
    let parent = node.parent();
    parent.property_name().is_some() && parent.name() == node && parent.property_name() != node
}

/// Whether `parameter` is certainly bound when `read` executes. Parameters
/// are initialized left to right, so a read inside a parameter list entry
/// (default initializer or binding pattern) observes that same parameter or
/// any later one in its temporal dead zone. Reads in the function body, or
/// in the entry of a later parameter, see an initialized binding.
fn parameter_initialized(parameter: Node, read: Node) -> bool {
    let function = parameter.parent();
    !function.parameters().iter().any(|p| {
        let mut node = read;
        while node.is_some() && node != p {
            node = node.parent();
        }
        node == p && parameter.pos() >= p.pos()
    })
}

/// Whether a lexical `declaration` precedes `read` in the same file and the
/// same function, outside any loop body that could re-enter its temporal
/// dead zone.
fn declared_before(declaration: Node, read: Node) -> bool {
    get_source_file_of_node(declaration) == get_source_file_of_node(read)
        && declaration.end() <= read.pos()
        && same_function(declaration, read)
}

fn same_function(a: Node, b: Node) -> bool {
    fn owner(mut n: Node) -> Node {
        while n.is_some()
            && !matches!(
                n.kind(),
                SyntaxKind::FunctionDeclaration
                    | SyntaxKind::FunctionExpression
                    | SyntaxKind::ArrowFunction
                    | SyntaxKind::MethodDeclaration
                    | SyntaxKind::Constructor
                    | SyntaxKind::GetAccessor
                    | SyntaxKind::SetAccessor
                    | SyntaxKind::SourceFile
                    | SyntaxKind::ForStatement
                    | SyntaxKind::ForInStatement
                    | SyntaxKind::ForOfStatement
                    | SyntaxKind::WhileStatement
                    | SyntaxKind::DoStatement
            )
        {
            n = n.parent();
        }
        n
    }
    owner(a) == owner(b)
}

fn ref_kind(node: Node) -> RefKind {
    let parent = node.parent();
    // A shorthand property `{ f }` declares a property but reads the value
    // of the binding `f`; the indexed symbol is that binding.
    if parent.kind() == SyntaxKind::ShorthandPropertyAssignment && parent.name() == node {
        return RefKind::Read;
    }
    // `export { f }` names the local binding without observing its value.
    if matches!(
        parent.kind(),
        SyntaxKind::ExportSpecifier | SyntaxKind::ImportSpecifier
    ) {
        return RefKind::Alias;
    }
    if is_declaration_name(node) {
        return match parent.kind() {
            SyntaxKind::ImportSpecifier
            | SyntaxKind::ImportClause
            | SyntaxKind::NamespaceImport => RefKind::Alias,
            _ => RefKind::Declaration,
        };
    }
    if matches!(
        parent.kind(),
        SyntaxKind::ExportSpecifier | SyntaxKind::ImportSpecifier
    ) {
        return RefKind::Alias;
    }
    if is_part_of_type_node(node) || in_type_query(node) {
        return RefKind::Type;
    }
    if parent.kind() == SyntaxKind::ExportAssignment {
        return RefKind::Read;
    }
    let outer = walk_up_outer(node);
    let holder = outer.parent();
    if holder.kind() == SyntaxKind::CallExpression && holder.expression() == outer {
        return RefKind::Call;
    }
    if holder.kind() == SyntaxKind::NewExpression && holder.expression() == outer {
        return RefKind::New;
    }
    if is_write_access(node) {
        return RefKind::Write;
    }
    RefKind::Read
}

fn in_type_query(mut node: Node) -> bool {
    while node.is_some() {
        match node.kind() {
            SyntaxKind::TypeQuery => return true,
            SyntaxKind::Identifier | SyntaxKind::QualifiedName => node = node.parent(),
            _ => return false,
        }
    }
    false
}

/// Outermost runtime-transparent wrapper of an expression.
pub(crate) fn walk_up_outer(mut node: Node) -> Node {
    while matches!(
        node.parent().kind(),
        SyntaxKind::ParenthesizedExpression
            | SyntaxKind::AsExpression
            | SyntaxKind::TypeAssertionExpression
            | SyntaxKind::SatisfiesExpression
            | SyntaxKind::NonNullExpression
    ) {
        node = node.parent();
    }
    node
}

/// The property-like name a node mentions, if any.
fn mentioned_name(node: Node) -> Option<&'static str> {
    let parent = node.parent();
    match node.kind() {
        SyntaxKind::Identifier | SyntaxKind::PrivateIdentifier => {
            let mentions = match parent.kind() {
                SyntaxKind::PropertyAccessExpression => parent.name() == node,
                SyntaxKind::PropertyAssignment
                | SyntaxKind::ShorthandPropertyAssignment
                | SyntaxKind::PropertyDeclaration
                | SyntaxKind::MethodDeclaration
                | SyntaxKind::GetAccessor
                | SyntaxKind::SetAccessor
                | SyntaxKind::PropertySignature
                | SyntaxKind::MethodSignature
                | SyntaxKind::EnumMember => parent.name() == node,
                SyntaxKind::BindingElement => {
                    parent.property_name() == node
                        || (parent.property_name().is_nil() && parent.name() == node)
                }
                SyntaxKind::QualifiedName => parent.right() == node,
                SyntaxKind::JsxAttribute => true,
                _ => false,
            };
            mentions.then(|| node.text())
        }
        SyntaxKind::StringLiteral | SyntaxKind::NoSubstitutionTemplateLiteral => Some(node.text()),
        _ => None,
    }
}
