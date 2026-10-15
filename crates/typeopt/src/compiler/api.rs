// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
// Checker predicates adapted from tsrs (Copyright 2026 Max Schwenk and
// contributors) and TypeScript (Copyright Microsoft Corporation). See NOTICE.
//! Private tsrs object access. Handles are valid only inside their owning
//! program's scope.
pub use tsrs_ast::{FunctionFlags, Kind as SyntaxKind, ModifierFlags, NodeFlags, SymbolFlags};
pub use tsrs_checker::{ContextFlags, LiteralValue, ObjectFlags, SignatureKind, TypeFlags};
use tsrs_core::P;
pub use tsrs_core::{CompilerOptions, JsxEmit, ModuleKind, Tristate};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Node(pub Option<P<tsrs_ast::Node>>);
impl Node {
    pub const NIL: Self = Self(None);

    pub fn is_nil(self) -> bool {
        self.0.is_none()
    }

    pub fn is_some(self) -> bool {
        self.0.is_some()
    }

    pub fn raw(self) -> P<tsrs_ast::Node> {
        self.0.expect("non-null AST node")
    }

    pub fn kind(self) -> SyntaxKind {
        self.0.map_or(SyntaxKind::Unknown, |n| n.kind())
    }

    pub fn text(self) -> &'static str {
        self.raw().text()
    }

    pub fn symbol(self) -> SymbolId {
        SymbolId::new(self.0.and_then(|n| n.symbol()))
    }

    pub fn for_each_child(self, mut f: impl FnMut(Node) -> bool) -> bool {
        self.raw().for_each_child(&mut |n| f(Node(Some(n))))
    }

    pub fn modifier_flags(self) -> ModifierFlags {
        self.raw().modifier_flags()
    }

    pub fn flags(self) -> NodeFlags {
        self.raw().flags()
    }

    pub fn pos(self) -> i32 {
        self.raw().pos().source_offset().map_or(-1, |n| n as i32)
    }

    pub fn end(self) -> i32 {
        self.raw().end().source_offset().map_or(-1, |n| n as i32)
    }

    pub fn is_type_only(self) -> bool {
        self.raw().is_type_only()
    }

    pub fn parent(self) -> Node {
        Node(self.0.and_then(|n| n.parent()))
    }

    pub fn name(self) -> Node {
        Node(self.0.and_then(|n| n.name()))
    }

    pub fn expression(self) -> Node {
        Node(self.0.and_then(|n| n.expression()))
    }

    pub fn initializer(self) -> Node {
        Node(self.0.and_then(|n| n.initializer()))
    }

    pub fn type_(self) -> Node {
        Node(self.0.and_then(|n| n.type_node()))
    }

    pub fn property_name(self) -> Node {
        Node(self.0.and_then(|n| n.property_name()))
    }

    pub fn body(self) -> Node {
        Node(self.0.and_then(|n| n.body()))
    }

    pub fn question_token(self) -> Node {
        Node(self.0.and_then(|n| n.question_token()))
    }

    pub fn question_dot_token(self) -> Node {
        Node(self.0.and_then(|n| n.question_dot_token()))
    }

    pub fn module_specifier(self) -> Node {
        Node(self.0.and_then(|n| n.module_specifier()))
    }

    pub fn import_clause(self) -> Node {
        Node(self.0.and_then(|n| n.import_clause()))
    }

    pub fn parameters(self) -> NodeSlice {
        NodeSlice(self.raw().parameters())
    }

    pub fn arguments(self) -> NodeSlice {
        NodeSlice(self.raw().arguments())
    }

    pub fn type_arguments(self) -> NodeSlice {
        NodeSlice(self.raw().type_arguments())
    }

    pub fn type_parameters(self) -> NodeSlice {
        NodeSlice(self.raw().type_parameters())
    }

    pub fn members(self) -> NodeSlice {
        NodeSlice(self.raw().members())
    }

    pub fn statements(self) -> NodeSlice {
        NodeSlice(self.raw().statements())
    }

    pub fn properties(self) -> NodeSlice {
        NodeSlice(self.raw().properties())
    }

    pub fn elements(self) -> NodeSlice {
        NodeSlice(self.raw().elements())
    }

    pub fn decorators(self) -> NodeSlice {
        NodeSlice(tsrs_core::alloc_vec(self.raw().decorators()))
    }

    pub fn left(self) -> Node {
        Node::from(if self.kind() == SyntaxKind::QualifiedName {
            self.raw().as_qualified_name().left
        } else {
            self.raw().as_binary_expression().left
        })
    }

    pub fn right(self) -> Node {
        Node::from(if self.kind() == SyntaxKind::QualifiedName {
            self.raw().as_qualified_name().right
        } else {
            self.raw().as_binary_expression().right()
        })
    }

    pub fn operator_token(self) -> Node {
        Node::from(self.raw().as_binary_expression().operator_token)
    }

    pub fn argument_expression(self) -> Node {
        Node::from(
            self.raw()
                .as_element_access_expression()
                .argument_expression,
        )
    }

    pub fn head(self) -> Node {
        Node::from(self.raw().as_template_expression().head)
    }

    pub fn literal(self) -> Node {
        Node::from(if self.kind() == SyntaxKind::LiteralType {
            self.raw().as_literal_type_node().literal
        } else {
            self.raw().as_template_span().literal
        })
    }

    pub fn argument(self) -> Node {
        Node::from(self.raw().as_import_type_node().argument)
    }

    pub fn tag(self) -> Node {
        Node::from(self.raw().as_tagged_template_expression().tag)
    }

    pub fn object_type(self) -> Node {
        Node::from(self.raw().as_indexed_access_type_node().object_type)
    }

    pub fn index_type(self) -> Node {
        Node::from(self.raw().as_indexed_access_type_node().index_type)
    }

    pub fn parameter_name(self) -> Node {
        Node::from(self.raw().as_type_predicate_node().parameter_name)
    }

    pub fn type_name(self) -> Node {
        Node::from(self.raw().as_type_reference_node().type_name)
    }

    pub fn expr_name(self) -> Node {
        Node::from(self.raw().as_type_query_node().expr_name)
    }

    pub fn when_true(self) -> Node {
        Node::from(self.raw().as_conditional_expression().when_true)
    }

    pub fn when_false(self) -> Node {
        Node::from(self.raw().as_conditional_expression().when_false)
    }

    pub fn condition(self) -> Node {
        Node::from(match self.kind() {
            SyntaxKind::ForStatement => self.raw().as_for_statement().condition,
            SyntaxKind::IfStatement => Some(self.raw().as_if_statement().expression),
            _ => Some(self.raw().as_conditional_expression().condition),
        })
    }

    pub fn case_block(self) -> Node {
        Node::from(self.raw().as_switch_statement().case_block)
    }

    pub fn declaration_list(self) -> Node {
        Node::from(self.raw().as_variable_statement().declaration_list)
    }

    pub fn named_bindings(self) -> Node {
        Node::from(self.raw().as_import_clause().named_bindings)
    }

    pub fn export_clause(self) -> Node {
        Node::from(self.raw().as_export_declaration().export_clause)
    }

    pub fn object_assignment_initializer(self) -> Node {
        Node::from(
            self.raw()
                .as_shorthand_property_assignment()
                .object_assignment_initializer
                .get(),
        )
    }

    pub fn then_statement(self) -> Node {
        Node::from(self.raw().as_if_statement().then_statement)
    }

    pub fn else_statement(self) -> Node {
        Node::from(self.raw().as_if_statement().else_statement)
    }

    pub fn declarations(self) -> NodeSlice {
        node_list(self.raw().as_variable_declaration_list().declarations)
    }

    pub fn clauses(self) -> NodeSlice {
        node_list(self.raw().as_case_block().clauses)
    }

    pub fn template_spans(self) -> NodeSlice {
        node_list(self.raw().as_template_expression().template_spans)
    }

    pub fn heritage_clauses(self) -> NodeSlice {
        node_list(match self.kind() {
            SyntaxKind::ClassExpression => self.raw().as_class_expression().heritage_clauses(),
            SyntaxKind::InterfaceDeclaration => {
                self.raw().as_interface_declaration().heritage_clauses
            }
            _ => self.raw().as_class_declaration().heritage_clauses(),
        })
    }

    pub fn types(self) -> NodeSlice {
        node_list(match self.kind() {
            SyntaxKind::HeritageClause => self.raw().as_heritage_clause().types.get(),
            SyntaxKind::IntersectionType => self.raw().as_intersection_type_node().types(),
            _ => self.raw().as_union_type_node().types(),
        })
    }

    pub fn asterisk_token(self) -> Node {
        Node(match self.kind() {
            SyntaxKind::FunctionDeclaration => {
                self.raw().as_function_declaration().asterisk_token()
            }
            SyntaxKind::FunctionExpression => self.raw().as_function_expression().asterisk_token(),
            SyntaxKind::MethodDeclaration => self.raw().as_method_declaration().asterisk_token(),
            SyntaxKind::YieldExpression => self.raw().as_yield_expression().asterisk_token(),
            _ => None,
        })
    }

    pub fn dot_dot_dot_token(self) -> Node {
        Node(match self.kind() {
            SyntaxKind::Parameter => self.raw().as_parameter_declaration().dot_dot_dot_token(),
            SyntaxKind::BindingElement => self.raw().as_binding_element().dot_dot_dot_token(),
            SyntaxKind::NamedTupleMember => self.raw().as_named_tuple_member().dot_dot_dot_token,
            SyntaxKind::JsxExpression => self.raw().as_jsx_expression().dot_dot_dot_token,
            _ => None,
        })
    }

    pub fn exclamation_token(self) -> Node {
        Node(self.raw().as_variable_declaration().exclamation_token())
    }

    pub fn is_export_equals(self) -> bool {
        self.raw().as_export_assignment().is_export_equals
    }

    pub fn token(self) -> SyntaxKind {
        self.raw().as_heritage_clause().token
    }

    pub fn operator(self) -> SyntaxKind {
        if self.kind() == SyntaxKind::PrefixUnaryExpression {
            self.raw().as_prefix_unary_expression().operator
        } else if self.kind() == SyntaxKind::TypeOperator {
            self.raw().as_type_operator_node().operator
        } else {
            self.raw().as_postfix_unary_expression().operator
        }
    }

    pub fn operand(self) -> Node {
        Node(Some(if self.kind() == SyntaxKind::PrefixUnaryExpression {
            self.raw().as_prefix_unary_expression().operand
        } else {
            self.raw().as_postfix_unary_expression().operand
        }))
    }
}
impl From<P<tsrs_ast::Node>> for Node {
    fn from(n: P<tsrs_ast::Node>) -> Self {
        Self(Some(n))
    }
}
impl From<Option<P<tsrs_ast::Node>>> for Node {
    fn from(n: Option<P<tsrs_ast::Node>>) -> Self {
        Self(n)
    }
}
#[derive(Clone, Copy)]
pub struct NodeSlice(&'static [P<tsrs_ast::Node>]);
impl NodeSlice {
    pub fn nodes(self) -> Self {
        self
    }

    pub fn iter(self) -> impl DoubleEndedIterator<Item = Node> + ExactSizeIterator {
        self.0.iter().copied().map(Node::from)
    }

    pub fn len(self) -> usize {
        self.0.len()
    }

    pub fn is_empty(self) -> bool {
        self.0.is_empty()
    }

    pub fn to_vec(self) -> Vec<Node> {
        self.iter().collect()
    }

    pub fn first(self) -> Option<Node> {
        self.0.first().copied().map(Node::from)
    }

    pub fn last(self) -> Option<Node> {
        self.0.last().copied().map(Node::from)
    }
}
fn node_list(list: impl Into<Option<P<tsrs_ast::NodeList>>>) -> NodeSlice {
    NodeSlice(list.into().map_or(&[], |l| l.nodes()))
}
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct SymbolId(pub u64, Option<P<tsrs_ast::Symbol>>);
impl SymbolId {
    pub const NIL: Self = Self(0, None);

    fn new(symbol: Option<P<tsrs_ast::Symbol>>) -> Self {
        // Assign the compiler identity when the symbol enters the adapter,
        // before it enters unordered analysis collections. Ordering by arena
        // addresses would let allocation layout affect optimization decisions.
        Self(symbol.map_or(0, |s| tsrs_ast::get_symbol_id(s).0), symbol)
    }

    pub fn is_nil(self) -> bool {
        self.1.is_none()
    }

    pub fn is_some(self) -> bool {
        self.1.is_some()
    }

    fn raw(self) -> P<tsrs_ast::Symbol> {
        self.1.expect("non-null compiler identity")
    }
}
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct TypeId(pub Option<P<tsrs_checker::Type>>);
impl TypeId {
    pub const NIL: Self = Self(None);

    pub fn is_nil(self) -> bool {
        self.0.is_none()
    }

    pub fn is_some(self) -> bool {
        self.0.is_some()
    }

    fn raw(self) -> P<tsrs_checker::Type> {
        self.0.expect("non-null compiler identity")
    }
}
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct SignatureId(pub Option<P<tsrs_checker::Signature>>);
impl SignatureId {
    pub fn is_nil(self) -> bool {
        self.0.is_none()
    }

    pub fn is_some(self) -> bool {
        self.0.is_some()
    }

    fn raw(self) -> P<tsrs_checker::Signature> {
        self.0.expect("non-null compiler identity")
    }
}
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct IndexInfoId(pub Option<P<tsrs_checker::IndexInfo>>);
impl IndexInfoId {
    fn raw(self) -> P<tsrs_checker::IndexInfo> {
        self.0.expect("non-null compiler identity")
    }
}

pub struct CompilerProgram {
    pub program: &'static tsrs_compiler::Program,
    pub region: tsrs_core::arena::Region,
}
impl Drop for CompilerProgram {
    fn drop(&mut self) {
        // SAFETY: every build is independent, checker guards have ended, and no
        // identity leaves this adapter. The region is dropped only
        // after the program and its hosts.
        unsafe { tsrs_compiler::free_unshared_program(self.program) };
    }
}
thread_local! { static CURRENT: std::cell::Cell<Option<&'static tsrs_compiler::Program>> = const { std::cell::Cell::new(None) }; }
pub struct ProgramScope<'a> {
    _owner: std::marker::PhantomData<&'a CompilerProgram>,
    previous: Option<&'static tsrs_compiler::Program>,
    _scope: tsrs_core::arena::RegionScope,
}
impl Drop for ProgramScope<'_> {
    fn drop(&mut self) {
        CURRENT.with(|c| c.set(self.previous));
    }
}
pub fn enter_program(program: Option<&CompilerProgram>) -> ProgramScope<'_> {
    let p = program.expect("program scope requires an owner");
    let scope = p.region.enter();
    let previous = CURRENT.with(|c| c.replace(Some(p.program)));
    ProgramScope {
        _owner: std::marker::PhantomData,
        previous,
        _scope: scope,
    }
}
fn current() -> &'static tsrs_compiler::Program {
    CURRENT.with(|c| c.get().expect("active program"))
}
pub fn source_files() -> Vec<Node> {
    current()
        .get_source_files()
        .iter()
        .map(|f| Node(Some(f.as_node())))
        .collect()
}
pub struct SourceInfo {
    pub file_name: String,
}
pub fn source_file_info(file: Node) -> SourceInfo {
    SourceInfo {
        file_name: file.raw().as_source_file().file_name().to_owned(),
    }
}
pub fn source_file_reference_targets(file: Node) -> Vec<String> {
    let origin = file.raw().as_source_file_p();
    file.raw()
        .as_source_file()
        .referenced_files()
        .iter()
        .chain(
            file.raw()
                .as_source_file()
                .type_reference_directives()
                .iter(),
        )
        .map(|reference| {
            current()
                .get_source_file_from_reference(origin, *reference)
                .map_or_else(
                    || reference.file_name.clone(),
                    |target| target.file_name().to_owned(),
                )
        })
        .collect()
}
pub fn source_file_text(file: Node) -> &'static str {
    file.raw().as_source_file().text()
}
pub fn is_source_file_from_external_library(file: Node) -> bool {
    current().is_source_file_from_external_library(file.raw().as_source_file_p())
}
pub fn get_program_diagnostics() -> Vec<P<tsrs_ast::Diagnostic>> {
    current().get_program_diagnostics()
}
pub fn get_global_diagnostics() -> Vec<P<tsrs_ast::Diagnostic>> {
    current().get_global_diagnostics(&tsrs_core::context::Context::background())
}
pub fn get_syntactic_diagnostics(file: Node) -> Vec<P<tsrs_ast::Diagnostic>> {
    current().get_syntactic_diagnostics(
        &tsrs_core::context::Context::background(),
        Some(file.raw().as_source_file_p()),
    )
}
pub fn get_semantic_diagnostics(file: Node) -> Vec<P<tsrs_ast::Diagnostic>> {
    current().get_semantic_diagnostics(
        &tsrs_core::context::Context::background(),
        Some(file.raw().as_source_file_p()),
    )
}
pub fn is_compound_assignment(token: SyntaxKind) -> bool {
    tsrs_ast::is_compound_assignment(token)
}
pub fn is_assignment_target(node: Node) -> bool {
    tsrs_ast::is_assignment_target(node.raw())
}
pub fn get_assignment_target(node: Node) -> Node {
    Node::from(tsrs_ast::get_assignment_target(node.raw()))
}
pub fn is_function_like(node: Node) -> bool {
    tsrs_ast::is_function_like(node.0)
}
pub fn is_type_node(node: Node) -> bool {
    tsrs_ast::is_type_node(node.raw())
}
pub fn skip_parentheses(node: Node) -> Node {
    Node::from(tsrs_ast::skip_parentheses(node.raw()))
}
pub fn get_source_file_of_node(node: Node) -> Node {
    Node(tsrs_ast::get_source_file_of_node(node.0).map(|f| f.as_node()))
}
pub fn get_containing_function(node: Node) -> Node {
    Node::from(tsrs_ast::get_containing_function(node.raw()))
}
pub fn get_function_flags(node: Node) -> FunctionFlags {
    tsrs_ast::get_function_flags(node.0)
}
pub fn is_static(node: Node) -> bool {
    tsrs_ast::is_static(node.raw())
}
pub fn get_combined_modifier_flags(node: Node) -> ModifierFlags {
    tsrs_ast::get_combined_modifier_flags(node.raw())
}
pub fn is_declaration_name(node: Node) -> bool {
    tsrs_ast::is_declaration_name(node.raw())
}
pub fn is_external_module(node: Node) -> bool {
    tsrs_ast::is_external_module(node.raw().as_source_file_p())
}
pub fn is_global_scope_augmentation(node: Node) -> bool {
    tsrs_ast::is_global_scope_augmentation(node.raw())
}
pub fn is_part_of_type_query(node: Node) -> bool {
    tsrs_ast::is_part_of_type_query(node.raw())
}
pub fn is_write_access(node: Node) -> bool {
    tsrs_ast::is_write_access(node.raw())
}
pub fn is_expression_node(node: Node) -> bool {
    tsrs_ast::is_expression_node(node.raw())
}
pub fn is_part_of_type_node(node: Node) -> bool {
    tsrs_ast::is_part_of_type_node(node.raw())
}
pub fn is_const_type_reference(node: Node) -> bool {
    tsrs_ast::is_const_type_reference(node.raw())
}
pub fn has_syntactic_modifier(node: Node, flags: ModifierFlags) -> bool {
    tsrs_ast::has_syntactic_modifier(node.raw(), flags)
}
pub fn get_this_container(node: Node, arrows: bool, computed: bool) -> Node {
    Node::from(tsrs_ast::get_this_container(node.raw(), arrows, computed))
}

pub struct Checker {
    inner: tsrs_compiler::CheckerHandle,
    pub compiler_options: P<CompilerOptions>,
    pub undefined_symbol: SymbolId,
    pub symbols: Symbols,
}
pub fn with_type_checker_for_file<R>(_file: Node, f: impl FnOnce(&mut Checker) -> R) -> R {
    let inner = current().get_type_checker(&tsrs_core::context::Context::background());
    let mut checker = Checker {
        compiler_options: inner.compiler_options,
        undefined_symbol: SymbolId::new(Some(inner.undefined_symbol)),
        inner,
        symbols: Symbols,
    };
    f(&mut checker)
}
pub struct SymbolView {
    pub name: String,
    pub exports: SymbolTable,
    pub flags: SymbolFlags,
    pub declarations: Vec<Node>,
    pub value_declaration: Node,
    pub export_symbol: SymbolId,
}
pub struct SignatureView {
    pub type_parameters: Vec<TypeId>,
    pub declaration: Node,
    pub parameters: Vec<SymbolId>,
    pub target: SignatureId,
}
pub struct IndexInfoView {
    pub value_type: TypeId,
}
pub struct TypeView {
    pub flags: TypeFlags,
    pub alias: Option<()>,
    raw: P<tsrs_checker::Type>,
    pub symbol: SymbolId,
}
impl TypeView {
    pub fn flags(&self) -> TypeFlags {
        self.raw.flags()
    }

    pub fn object_flags(&self) -> ObjectFlags {
        self.raw.object_flags()
    }

    pub fn types_list(&self) -> Vec<TypeId> {
        self.raw.types().iter().map(|&t| TypeId(Some(t))).collect()
    }

    pub fn as_literal_type(&self) -> &'static tsrs_checker::LiteralType {
        self.raw.as_literal_type()
    }
}
pub struct Symbols;
#[derive(Clone, Copy)]
pub struct SymbolTable(Option<P<tsrs_ast::SymbolTable>>);
impl Symbols {
    pub fn get(&self, table: SymbolTable, name: &str) -> SymbolId {
        SymbolId::new(table.0.and_then(|t| t.lookup(name)))
    }

    pub fn values(&self, table: SymbolTable) -> Vec<SymbolId> {
        table.0.map_or_else(Vec::new, |t| {
            t.values()
                .into_iter()
                .map(|s| SymbolId::new(Some(s)))
                .collect()
        })
    }
}
impl Checker {
    pub fn sym(&self, s: SymbolId) -> SymbolView {
        let s = s.raw();
        SymbolView {
            name: s.name().to_owned(),
            exports: SymbolTable(s.exports()),
            flags: s.flags(),
            declarations: s.declarations().iter().map(|&n| Node::from(n)).collect(),
            value_declaration: Node::from(s.value_declaration()),
            export_symbol: SymbolId::new(s.export_symbol()),
        }
    }

    pub fn ty(&self, t: TypeId) -> TypeView {
        let raw = t.raw();
        TypeView {
            flags: raw.flags(),
            alias: raw.alias().map(|_| ()),
            symbol: SymbolId::new(raw.symbol()),
            raw,
        }
    }

    pub fn sig(&self, s: SignatureId) -> SignatureView {
        let s = s.raw();
        SignatureView {
            type_parameters: s
                .type_parameters
                .get()
                .iter()
                .map(|&t| TypeId(Some(t)))
                .collect(),
            declaration: Node::from(s.declaration.get()),
            parameters: s
                .parameters()
                .iter()
                .map(|&s| SymbolId::new(Some(s)))
                .collect(),
            target: SignatureId(s.target()),
        }
    }

    pub fn index_info(&self, i: IndexInfoId) -> IndexInfoView {
        IndexInfoView {
            value_type: TypeId(Some(i.raw().value_type())),
        }
    }

    pub fn get_apparent_type(&mut self, t: TypeId) -> TypeId {
        TypeId(Some(self.inner.get_apparent_type_exported(t.raw())))
    }

    pub fn get_base_constraint_of_type(&mut self, t: TypeId) -> TypeId {
        TypeId(self.inner.get_base_constraint_of_type_exported(t.raw()))
    }

    pub fn get_base_type_of_literal_type(&mut self, t: TypeId) -> TypeId {
        TypeId(Some(
            self.inner.get_base_type_of_literal_type_exported(t.raw()),
        ))
    }

    pub fn get_constant_value(&mut self, node: Node) -> Option<LiteralValue> {
        self.inner.get_constant_value(node.raw())
    }

    pub fn get_contextual_type(&mut self, node: Node, context_flags: ContextFlags) -> TypeId {
        TypeId(
            self.inner
                .get_contextual_type_exported(node.raw(), context_flags),
        )
    }

    pub fn get_declared_type_of_symbol(&mut self, symbol: SymbolId) -> TypeId {
        TypeId(Some(
            self.inner
                .get_declared_type_of_symbol_exported(symbol.raw()),
        ))
    }

    pub fn get_element_type_of_array_type(&mut self, t: TypeId) -> TypeId {
        TypeId(self.inner.get_element_type_of_array_type_exported(t.raw()))
    }

    pub fn get_export_specifier_local_target_symbol(&mut self, node: Node) -> SymbolId {
        SymbolId::new(
            self.inner
                .get_export_specifier_local_target_symbol(node.raw()),
        )
    }

    pub fn get_exports_of_module_exported(&mut self, symbol: SymbolId) -> Vec<SymbolId> {
        self.inner
            .get_exports_of_module_exported(symbol.raw())
            .iter()
            .map(|&v| SymbolId::new(Some(v)))
            .collect()
    }

    pub fn get_index_infos_of_type(&mut self, t: TypeId) -> Vec<IndexInfoId> {
        self.inner
            .get_index_infos_of_type_exported(t.raw())
            .iter()
            .map(|&v| IndexInfoId(Some(v)))
            .collect()
    }

    pub fn get_merged_symbol(&mut self, symbol: SymbolId) -> SymbolId {
        SymbolId::new(Some(self.inner.get_merged_symbol_exported(symbol.raw())))
    }

    pub fn get_merged_symbol_exported(&mut self, symbol: SymbolId) -> SymbolId {
        SymbolId::new(Some(self.inner.get_merged_symbol_exported(symbol.raw())))
    }

    pub fn get_non_nullable_type(&mut self, t: TypeId) -> TypeId {
        TypeId(Some(self.inner.get_non_nullable_type(t.raw())))
    }

    pub fn get_properties_of_type(&mut self, t: TypeId) -> Vec<SymbolId> {
        self.inner
            .get_properties_of_type_exported(t.raw())
            .iter()
            .map(|&v| SymbolId::new(Some(v)))
            .collect()
    }

    pub fn get_resolved_signature_exported(&mut self, node: Node) -> SignatureId {
        SignatureId(Some(self.inner.get_resolved_signature_exported(node.raw())))
    }

    pub fn get_return_type_of_signature(&mut self, sig: SignatureId) -> TypeId {
        TypeId(Some(
            self.inner.get_return_type_of_signature_exported(sig.raw()),
        ))
    }

    pub fn get_shorthand_assignment_value_symbol(&mut self, location: Node) -> SymbolId {
        SymbolId::new(self.inner.get_shorthand_assignment_value_symbol(location.0))
    }

    pub fn get_signature_from_declaration(&mut self, declaration: Node) -> SignatureId {
        SignatureId(Some(
            self.inner
                .get_signature_from_declaration_exported(declaration.raw()),
        ))
    }

    pub fn get_signatures_of_type(&mut self, t: TypeId, kind: SignatureKind) -> Vec<SignatureId> {
        self.inner
            .get_signatures_of_type_exported(t.raw(), kind)
            .iter()
            .map(|&v| SignatureId(Some(v)))
            .collect()
    }

    pub fn get_symbol_at_location(&mut self, node: Node) -> SymbolId {
        SymbolId::new(self.inner.get_symbol_at_location_exported(node.raw()))
    }

    pub fn get_symbol_flags(&mut self, symbol: SymbolId) -> SymbolFlags {
        self.inner.get_symbol_flags_exported(symbol.raw())
    }

    pub fn get_type_arguments(&mut self, t: TypeId) -> Vec<TypeId> {
        self.inner
            .get_type_arguments_exported(t.raw())
            .iter()
            .map(|&v| TypeId(Some(v)))
            .collect()
    }

    pub fn get_type_at_location(&mut self, node: Node) -> TypeId {
        TypeId(Some(self.inner.get_type_at_location(node.raw())))
    }

    pub fn get_type_at_position(&mut self, signature: SignatureId, pos: i32) -> TypeId {
        TypeId(Some(
            self.inner
                .get_type_at_position_exported(signature.raw(), pos as usize),
        ))
    }

    pub fn get_type_from_type_node(&mut self, node: Node) -> TypeId {
        TypeId(Some(
            self.inner.get_type_from_type_node_exported(node.raw()),
        ))
    }

    pub fn get_type_of_symbol(&mut self, symbol: SymbolId) -> TypeId {
        TypeId(Some(self.inner.get_type_of_symbol_exported(symbol.raw())))
    }

    pub fn get_unknown_signature(&mut self) -> SignatureId {
        SignatureId(Some(self.inner.get_unknown_signature()))
    }

    pub fn get_widened_type(&mut self, t: TypeId) -> TypeId {
        TypeId(Some(self.inner.get_widened_type_exported(t.raw())))
    }

    pub fn is_array_type(&mut self, t: TypeId) -> bool {
        self.inner.is_array_type_exported(t.raw())
    }

    pub fn is_type_assignable_to_exported(&mut self, source: TypeId, target: TypeId) -> bool {
        self.inner
            .is_type_assignable_to_exported(source.raw(), target.raw())
    }

    pub fn is_undefined_symbol(&mut self, symbol: SymbolId) -> bool {
        self.inner.is_undefined_symbol(symbol.raw())
    }

    pub fn is_unknown_symbol(&mut self, symbol: SymbolId) -> bool {
        self.inner.is_unknown_symbol(symbol.raw())
    }

    pub fn resolve_name_exported(
        &mut self,
        name: &str,
        location: Node,
        meaning: SymbolFlags,
        exclude_globals: bool,
    ) -> SymbolId {
        SymbolId::new(
            self.inner
                .resolve_name_exported(name, location.0, meaning, exclude_globals),
        )
    }

    pub fn skip_alias_exported(&mut self, symbol: SymbolId) -> SymbolId {
        SymbolId::new(Some(self.inner.skip_alias(symbol.raw())))
    }

    pub fn type_to_string(&mut self, t: TypeId) -> String {
        self.inner.type_to_string_exported(t.raw())
    }

    pub fn is_tuple_type(&self, t: TypeId) -> bool {
        tsrs_checker::is_tuple_type_exported(t.raw())
    }

    pub fn get_symbol_of_declaration(&mut self, node: Node) -> SymbolId {
        let s = node.symbol();
        if s.is_nil() {
            s
        } else {
            self.get_merged_symbol(s)
        }
    }

    pub fn get_exports_of_symbol(&mut self, symbol: SymbolId) -> SymbolTable {
        let merged = self.get_merged_symbol(symbol);
        self.inner.get_exports_of_module_exported(merged.raw());
        SymbolTable(merged.raw().exports())
    }

    pub fn is_fresh_literal_type(&self, t: TypeId) -> bool {
        let t = t.raw();
        t.flags().intersects(TypeFlags::Freshable) && t.as_literal_type().fresh_type() == Some(t)
    }

    pub fn get_regular_type_of_literal_type(&mut self, t: TypeId) -> TypeId {
        let raw = t.raw();
        if raw.flags().intersects(TypeFlags::Freshable) {
            return TypeId(raw.as_literal_type().regular_type());
        }
        if raw.flags().intersects(TypeFlags::Union) {
            let parts: Vec<_> = raw
                .types()
                .iter()
                .map(|&p| self.get_regular_type_of_literal_type(TypeId(Some(p))).raw())
                .collect();
            return TypeId(Some(self.inner.get_union_type(&parts)));
        }
        t
    }

    pub fn get_parameter_count(&mut self, s: SignatureId) -> i32 {
        let signature = s.raw();
        let len = signature.parameters().len();
        if signature
            .flags()
            .intersects(tsrs_checker::SignatureFlags::HasRestParameter)
        {
            let t = self
                .inner
                .get_type_of_symbol_exported(signature.parameters()[len - 1]);
            if tsrs_checker::is_tuple_type_exported(t) {
                let target = t.target_tuple_type();
                return (len + target.fixed_length.get()
                    - usize::from(
                        !target
                            .combined_flags
                            .get()
                            .intersects(tsrs_checker::ElementFlags::Variable),
                    )) as i32;
            }
        }
        len as i32
    }

    pub fn is_empty_object_type(&mut self, t: TypeId) -> bool {
        let raw = t.raw();
        let flags = raw.flags();
        if flags.intersects(TypeFlags::Object) {
            // This query is used only to detect imprecise value-flow
            // boundaries. tsrs keeps its generic-mapped-type
            // predicate private. Treat an empty mapped shape as
            // imprecise too, since it must not justify
            // removing members from a value that flows through it. This can be
            // narrowed when tsrs exposes that predicate.
            if raw == self.inner.any_function_type {
                return false;
            }
            return self.inner.get_properties_of_type_exported(raw).is_empty()
                && self
                    .inner
                    .get_signatures_of_type_exported(raw, SignatureKind::Call)
                    .is_empty()
                && self
                    .inner
                    .get_signatures_of_type_exported(raw, SignatureKind::Construct)
                    .is_empty()
                && self.inner.get_index_infos_of_type_exported(raw).is_empty();
        }
        if flags.intersects(TypeFlags::NonPrimitive) {
            return true;
        }
        if flags.intersects(TypeFlags::Union) {
            return raw
                .types()
                .iter()
                .any(|&p| self.is_empty_object_type(TypeId(Some(p))));
        }
        if flags.intersects(TypeFlags::Intersection) {
            return raw
                .types()
                .iter()
                .all(|&p| self.is_empty_object_type(TypeId(Some(p))));
        }
        false
    }

    // This checker predicate only nominates a possible generic boundary.
    // Runtime proofs and semantic rechecks still decide whether an edit is
    // safe.
    pub fn could_contain_type_variables(&mut self, t: TypeId) -> bool {
        let raw = t.raw();
        let flags = raw.flags();
        if !flags.intersects(TypeFlags::StructuredOrInstantiable) {
            return false;
        }
        let objects = raw.object_flags_lazy();
        if objects.intersects(ObjectFlags::CouldContainTypeVariablesComputed) {
            return objects.intersects(ObjectFlags::CouldContainTypeVariables);
        }
        let top_level = raw.alias().is_some_and(|alias| {
            if !alias.type_arguments().is_empty() {
                return false;
            }
            alias.symbol().is_some_and(|s| {
                s.declarations()
                    .iter()
                    .find(|d| {
                        matches!(
                            d.kind(),
                            SyntaxKind::TypeAliasDeclaration | SyntaxKind::JSTypeAliasDeclaration
                        )
                    })
                    .is_some_and(|d| {
                        let mut parent = d.parent();
                        while let Some(p) = parent {
                            match p.kind() {
                                SyntaxKind::SourceFile => return true,
                                SyntaxKind::ModuleDeclaration => return false,
                                _ => parent = p.parent(),
                            }
                        }
                        false
                    })
            })
        });
        if flags.intersects(TypeFlags::Instantiable) {
            return true;
        }
        if flags.intersects(TypeFlags::Object) && !top_level {
            if objects.intersects(ObjectFlags::Reference)
                && (raw.as_type_reference().node.get().is_some()
                    || self
                        .get_type_arguments(t)
                        .iter()
                        .any(|&t| self.could_contain_type_variables(t)))
            {
                return true;
            }
            if objects.intersects(ObjectFlags::Anonymous)
                && raw.symbol().is_some_and(|s| {
                    s.flags().intersects(
                        SymbolFlags::Function
                            | SymbolFlags::Method
                            | SymbolFlags::Class
                            | SymbolFlags::TypeLiteral
                            | SymbolFlags::ObjectLiteral,
                    ) && !s.declarations().is_empty()
                })
            {
                return true;
            }
            if objects.intersects(
                ObjectFlags::Mapped
                    | ObjectFlags::ReverseMapped
                    | ObjectFlags::ObjectRestType
                    | ObjectFlags::InstantiationExpressionType,
            ) {
                return true;
            }
        }
        flags.intersects(TypeFlags::UnionOrIntersection)
            && !flags.intersects(TypeFlags::EnumLiteral)
            && !top_level
            && raw
                .types()
                .iter()
                .any(|&t| self.could_contain_type_variables(TypeId(Some(t))))
    }

    // Matches the compiler's syntactic predicate; callers additionally prove
    // runtime purity.
    pub fn is_side_effect_free(&self, node: Node) -> bool {
        let node = skip_parentheses(node);
        match node.kind() {
            SyntaxKind::Identifier
            | SyntaxKind::StringLiteral
            | SyntaxKind::RegularExpressionLiteral
            | SyntaxKind::TaggedTemplateExpression
            | SyntaxKind::TemplateExpression
            | SyntaxKind::NoSubstitutionTemplateLiteral
            | SyntaxKind::NumericLiteral
            | SyntaxKind::BigIntLiteral
            | SyntaxKind::TrueKeyword
            | SyntaxKind::FalseKeyword
            | SyntaxKind::NullKeyword
            | SyntaxKind::UndefinedKeyword
            | SyntaxKind::FunctionExpression
            | SyntaxKind::ClassExpression
            | SyntaxKind::ArrowFunction
            | SyntaxKind::ArrayLiteralExpression
            | SyntaxKind::ObjectLiteralExpression
            | SyntaxKind::TypeOfExpression
            | SyntaxKind::NonNullExpression
            | SyntaxKind::JsxSelfClosingElement
            | SyntaxKind::JsxElement => true,
            SyntaxKind::ConditionalExpression => {
                self.is_side_effect_free(node.when_true())
                    && self.is_side_effect_free(node.when_false())
            }
            SyntaxKind::BinaryExpression => {
                !tsrs_ast::is_assignment_operator(node.operator_token().kind())
                    && self.is_side_effect_free(node.left())
                    && self.is_side_effect_free(node.right())
            }
            SyntaxKind::PrefixUnaryExpression => matches!(
                node.operator(),
                SyntaxKind::ExclamationToken
                    | SyntaxKind::PlusToken
                    | SyntaxKind::MinusToken
                    | SyntaxKind::TildeToken
            ),
            _ => false,
        }
    }
}

impl NodeSlice {
    pub fn get(self, i: usize) -> Node {
        Node::from(self.0[i])
    }
}
impl Node {
    pub fn label(self) -> Node {
        Node(self.raw().label())
    }

    pub fn module_reference(self) -> Node {
        Node::from(self.raw().as_import_equals_declaration().module_reference)
    }

    /// The block of a `static { … }` class member.
    pub fn static_block_body(self) -> Node {
        Node::from(self.raw().as_class_static_block_declaration().body())
    }

    pub fn keyword_token(self) -> SyntaxKind {
        self.raw().as_meta_property().keyword_token
    }

    pub fn modifiers(self) -> NodeSlice {
        NodeSlice(self.raw().modifiers().map_or(&[], |m| m.nodes()))
    }
}
impl Checker {
    pub fn get_property_of_type(&mut self, t: TypeId, name: &str) -> SymbolId {
        SymbolId::new(self.inner.get_property_of_type_exported(t.raw(), name))
    }
}
pub fn get_config_file_parsing_diagnostics() -> Vec<P<tsrs_ast::Diagnostic>> {
    current().get_config_file_parsing_diagnostics()
}
