// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Whole-program, context-insensitive points-to analysis for property
//! renaming. One analysis covers every application TypeScript module with
//! the shared checker, so objects connected through internal imports,
//! exports, re-exports, aliases and module cycles form one region.
//!
//! Values are fresh object literals and, for finite-key coordination, string
//! literals whose runtime values may reach computed property keys. A cell
//! holds the values that a binding, parameter, call result, object field or
//! expression may contain; an `unknown` flag marks values the analysis does
//! not own (external code, untracked bindings, unknown calls). Every graph
//! edge only adds information, and the solver iterates until a fixed point.
//!
//! Proof obligations, all established from source and symbol references
//! rather than from types:
//! - A function receives a trusted summary only when its symbol is internal
//!   (not a public entry export, not referenced from JavaScript, declared only
//!   once in application TypeScript) and every reference is its own
//!   declaration, an import/export alias or a direct call with exactly its
//!   fixed arity and no spread. Async/generator/generic functions, `this`,
//!   `super`, `arguments`, `new.target`, rest/default/pattern parameters and
//!   named function expressions remain boundaries.
//! - A binding is tracked only when internal and declared in a module (not a
//!   script or namespace). Every write other than a plain `=` and every
//!   unsupported definition makes its value unknown.
//! - Any use of a value outside the supported contexts (unknown calls,
//!   containers that are not tracked object literals, spreads, operators,
//!   reflection, serialization, `in`, `delete`, method calls, dynamic keys,
//!   JSX, exports of public bindings) escapes it. Escaped objects make their
//!   fields unknown, so contained objects escape transitively.
//! - Values that meet in one cell join one region; one unknown value or one
//!   escape preserves the whole region. Type annotations never prove anything:
//!   inline type literals are renamed with their values, other annotations and
//!   `typeof` queries preserve the values they describe.
//! - Finite keys: a computed key resolves to string-literal values only when
//!   every value reaching it is such a literal and those literals reach no
//!   other observable use. Their literal spans become property occurrences, and
//!   every object accessed through one literal joins one region.
use std::collections::{BTreeSet, HashMap, HashSet, VecDeque};

use anyhow::Result;
use ts_goport::prelude::*;

use super::{
    analysis::{Ctx, RefKind, skip_outer, walk_up_outer},
    token_start,
};
use crate::optimizer::{Object, Occurrence, Preservation, Property};

#[derive(Clone, Copy, Debug)]
pub(super) struct Options {
    /// Coordinate finite computed keys with property renaming.
    pub finite_keys: bool,
}

/// Property names with protocol meaning; they are never renamed.
pub(super) const SPECIAL_NAMES: &[&str] = &[
    "__proto__",
    "constructor",
    "prototype",
    "then",
    "toJSON",
    "toString",
    "valueOf",
    "toLocaleString",
];

/// Value identity: object allocations first, then key literals.
type Value = u32;
type CellId = u32;
/// Shared cell of every untracked value. Never an edge target.
const UNKNOWN: CellId = 0;
/// Shared cell of `null`/`undefined`. Never an edge target.
const EMPTY: CellId = 1;
const NO_TOKEN: Value = Value::MAX;

#[derive(Clone, Default)]
struct Cell {
    values: BTreeSet<Value>,
    unknown: bool,
}

#[derive(Clone, Copy)]
enum Key {
    Name(&'static str),
    Cell(CellId),
    Dynamic,
}

/// A property load (`other` receives the field) or store (`other` flows
/// into the field) through a receiver.
struct Flow {
    receiver: CellId,
    key: Key,
    other: CellId,
    store: bool,
    done: HashSet<(Value, Value)>,
}

struct Graph {
    cells: Vec<Cell>,
    succ: Vec<Vec<CellId>>,
    edges: HashSet<(CellId, CellId)>,
    watchers: Vec<Vec<usize>>,
    flows: Vec<Flow>,
    fields: HashMap<(Value, &'static str), CellId>,
    object_fields: Vec<Vec<CellId>>,
    escaped: Vec<bool>,
    objects: Value,
    names: Vec<&'static str>,
    queue: VecDeque<CellId>,
    queued: Vec<bool>,
}

impl Graph {
    fn new(objects: usize, names: Vec<&'static str>) -> Self {
        let mut graph = Self {
            cells: Vec::new(),
            succ: Vec::new(),
            edges: HashSet::new(),
            watchers: Vec::new(),
            flows: Vec::new(),
            fields: HashMap::new(),
            object_fields: vec![Vec::new(); objects],
            escaped: vec![false; objects],
            objects: objects as Value,
            names,
            queue: VecDeque::new(),
            queued: Vec::new(),
        };
        graph.cell(true);
        graph.cell(false);
        graph
    }

    fn cell(&mut self, unknown: bool) -> CellId {
        let id = self.cells.len() as CellId;
        self.cells.push(Cell {
            values: BTreeSet::new(),
            unknown,
        });
        self.succ.push(Vec::new());
        self.watchers.push(Vec::new());
        self.queued.push(false);
        id
    }

    fn holding(&mut self, value: Value) -> CellId {
        let cell = self.cell(false);
        self.cells[cell as usize].values.insert(value);
        cell
    }

    fn is_object(&self, value: Value) -> bool {
        value < self.objects
    }

    fn enqueue(&mut self, cell: CellId) {
        if !self.queued[cell as usize] {
            self.queued[cell as usize] = true;
            self.queue.push_back(cell);
        }
    }

    fn merge(&mut self, from: CellId, to: CellId) -> bool {
        if from == to {
            return false;
        }
        let source = self.cells[from as usize].clone();
        let target = &mut self.cells[to as usize];
        let before = (target.values.len(), target.unknown);
        target.values.extend(source.values);
        target.unknown |= source.unknown;
        before != (target.values.len(), target.unknown)
    }

    fn edge(&mut self, from: CellId, to: CellId) {
        debug_assert!(to > EMPTY, "shared cells never receive values");
        if from == to || from == EMPTY || !self.edges.insert((from, to)) {
            return;
        }
        self.succ[from as usize].push(to);
        if self.merge(from, to) {
            self.enqueue(to);
        }
    }

    fn set_unknown(&mut self, cell: CellId) {
        debug_assert!(cell > EMPTY);
        if !self.cells[cell as usize].unknown {
            self.cells[cell as usize].unknown = true;
            self.enqueue(cell);
        }
    }

    fn field(&mut self, object: Value, name: &'static str) -> CellId {
        if let Some(&cell) = self.fields.get(&(object, name)) {
            return cell;
        }
        let cell = self.cell(self.escaped[object as usize]);
        self.fields.insert((object, name), cell);
        self.object_fields[object as usize].push(cell);
        cell
    }

    fn flow(&mut self, receiver: CellId, key: Key, other: CellId, store: bool) {
        let index = self.flows.len();
        self.flows.push(Flow {
            receiver,
            key,
            other,
            store,
            done: HashSet::new(),
        });
        self.watchers[receiver as usize].push(index);
        if let Key::Cell(key) = key {
            self.watchers[key as usize].push(index);
        }
        self.fire(index);
    }

    fn fire(&mut self, index: usize) {
        let (receiver, key, other, store) = {
            let flow = &self.flows[index];
            (flow.receiver, flow.key, flow.other, flow.store)
        };
        let values = self.cells[receiver as usize].clone();
        let (names, key_unknown): (Vec<(Value, &'static str)>, bool) = match key {
            Key::Name(name) => (vec![(NO_TOKEN, name)], false),
            Key::Cell(cell) => {
                let keys = &self.cells[cell as usize];
                let mut names = Vec::new();
                let mut unknown = keys.unknown;
                for &value in &keys.values {
                    if self.is_object(value) {
                        unknown = true;
                    } else {
                        names.push((value, self.names[(value - self.objects) as usize]));
                    }
                }
                (names, unknown)
            }
            Key::Dynamic => (Vec::new(), true),
        };
        let objects: Vec<Value> = values
            .values
            .iter()
            .copied()
            .filter(|&v| self.is_object(v))
            .collect();
        if !store
            && (values.unknown
                || objects.len() != values.values.len()
                || (key_unknown && !objects.is_empty()))
        {
            self.set_unknown(other);
        }
        for object in objects {
            for &(token, name) in &names {
                if !self.flows[index].done.insert((object, token)) {
                    continue;
                }
                let field = self.field(object, name);
                if store {
                    self.edge(other, field);
                } else {
                    self.edge(field, other);
                }
            }
        }
    }

    /// Unknown code may read and overwrite every field of an escaped object.
    fn escape(&mut self, object: Value) {
        if self.escaped[object as usize] {
            return;
        }
        self.escaped[object as usize] = true;
        for field in self.object_fields[object as usize].clone() {
            self.set_unknown(field);
        }
    }

    fn solve(&mut self) -> Result<usize> {
        let mut steps = 0usize;
        while let Some(cell) = self.queue.pop_front() {
            steps += 1;
            if steps % 65536 == 0 {
                crate::app::check_cancelled()?;
            }
            self.queued[cell as usize] = false;
            for target in self.succ[cell as usize].clone() {
                if self.merge(cell, target) {
                    self.enqueue(target);
                }
            }
            for flow in self.watchers[cell as usize].clone() {
                self.fire(flow);
            }
        }
        Ok(steps)
    }
}

struct Function {
    parameters: Vec<Node>,
    result: CellId,
}

#[derive(Clone)]
enum AccessKey {
    /// `.name`, `["name"]` or a destructured key.
    Static {
        name: &'static str,
        occurrence: usize,
    },
    /// A computed key whose values are analyzed.
    Computed(CellId),
    /// An unresolved key.
    Dynamic,
}

#[derive(Clone)]
struct Access {
    receiver: CellId,
    key: AccessKey,
    /// Calls through the property, tagged templates and `delete` observe
    /// more than a data property.
    operation: bool,
}

#[derive(Clone, Copy)]
struct Store {
    receiver: CellId,
    key: Key,
    value: CellId,
}

/// What an expression's context does with its value.
#[derive(Clone, Copy)]
struct Use {
    cell: CellId,
    /// Objects escape with this reason.
    objects: Option<Preservation2>,
    /// Key literals escape (their text would be observed).
    tokens: bool,
}

/// `Preservation` is not `Copy`; uses keep a compact reason.
#[derive(Clone, Copy)]
enum Preservation2 {
    Escapes,
    Unsupported,
    Operation,
}

impl Preservation2 {
    fn reason(self) -> Preservation {
        match self {
            Preservation2::Escapes => Preservation::Escapes,
            Preservation2::Unsupported => Preservation::ExternalOrUnsupportedShape,
            Preservation2::Operation => Preservation::ObservableOperation,
        }
    }
}

struct Token {
    occurrence: Occurrence,
}

struct Builder<'a, 'c> {
    ctx: &'a mut Ctx<'c>,
    options: Options,
    graph: Graph,
    symbols: HashMap<Node, (SymbolId, RefKind)>,
    bindings: HashMap<SymbolId, CellId>,
    expressions: HashMap<Node, CellId>,
    functions: Vec<Function>,
    targets: HashMap<SymbolId, usize>,
    owners: HashMap<Node, usize>,
    allocations: HashMap<Node, Value>,
    tokens: HashMap<Node, Value>,
    objects: Vec<Object>,
    intrinsic: Vec<Option<Preservation>>,
    token_info: Vec<Token>,
    /// Static access spans, referenced by index.
    spans: Vec<Occurrence>,
    uses: Vec<Use>,
    accesses: Vec<Access>,
    stores: Vec<Store>,
    annotations: Vec<(Node, CellId)>,
    sinks: Vec<CellId>,
    constraints: Vec<CellId>,
}

/// Everything decided from the current cells.
struct Evaluation {
    reasons: Vec<Option<Preservation>>,
    occurrences: Vec<Vec<(usize, Occurrence)>>,
    parents: Vec<usize>,
}

fn root(parents: &mut [usize], mut i: usize) -> usize {
    while parents[i] != i {
        parents[i] = parents[parents[i]];
        i = parents[i];
    }
    i
}

fn union(parents: &mut [usize], a: usize, b: usize) {
    let (a, b) = (root(parents, a), root(parents, b));
    if a != b {
        parents[a.max(b)] = a.min(b);
    }
}

fn function_like(node: Node) -> bool {
    matches!(
        node.kind(),
        SyntaxKind::FunctionDeclaration
            | SyntaxKind::FunctionExpression
            | SyntaxKind::ArrowFunction
            | SyntaxKind::MethodDeclaration
            | SyntaxKind::GetAccessor
            | SyntaxKind::SetAccessor
            | SyntaxKind::Constructor
    )
}

fn owner(mut node: Node) -> Node {
    node = node.parent();
    while node.is_some() && !function_like(node) {
        node = node.parent();
    }
    node
}

fn collect_nodes(node: Node, nodes: &mut Vec<Node>) {
    super::collect(node, nodes);
}

/// The assignment expression, update or loop that writes `node`, if any.
fn assignment_target(node: Node) -> Node {
    get_assignment_target(walk_up_outer(node))
}

/// `node = value` with `node` (possibly parenthesized or asserted) as the
/// whole left side.
fn simple_assignment(node: Node) -> bool {
    let outer = walk_up_outer(node);
    let parent = outer.parent();
    parent.kind() == SyntaxKind::BinaryExpression
        && parent.operator_token().kind() == SyntaxKind::EqualsToken
        && parent.left() == outer
}

fn in_namespace(mut node: Node) -> bool {
    while node.is_some() {
        if node.kind() == SyntaxKind::ModuleDeclaration {
            return true;
        }
        node = node.parent();
    }
    false
}

/// A string usable as a property key literal that may be renamed.
fn renamable_key(text: &str) -> bool {
    !text.is_empty() && !SPECIAL_NAMES.contains(&text)
}

/// `(typeof X)[keyof typeof X]` observes only the value types of `X`.
fn value_union_query(type_query: Node) -> bool {
    fn unparen(mut node: Node) -> Node {
        while node.kind() == SyntaxKind::ParenthesizedType {
            node = node.parent();
        }
        node
    }
    fn same(a: Node, b: Node) -> bool {
        a.kind() == SyntaxKind::TypeQuery
            && b.kind() == SyntaxKind::TypeQuery
            && a.expr_name().kind() == SyntaxKind::Identifier
            && b.expr_name().kind() == SyntaxKind::Identifier
            && a.expr_name().text() == b.expr_name().text()
    }
    fn strip(mut node: Node) -> Node {
        while node.kind() == SyntaxKind::ParenthesizedType {
            node = node.type_();
        }
        node
    }
    let outer = unparen(type_query.parent());
    let (indexed, query) = if outer.kind() == SyntaxKind::IndexedAccessType {
        (outer, type_query)
    } else if outer.kind() == SyntaxKind::TypeOperator
        && outer.operator() == SyntaxKind::KeyOfKeyword
        && unparen(outer.parent()).kind() == SyntaxKind::IndexedAccessType
    {
        (unparen(outer.parent()), type_query)
    } else {
        return false;
    };
    let object = strip(indexed.object_type());
    let index = strip(indexed.index_type());
    index.kind() == SyntaxKind::TypeOperator
        && index.operator() == SyntaxKind::KeyOfKeyword
        && same(object, strip(index.type_()))
        && same(object, query)
}

impl Builder<'_, '_> {
    fn ts_files(&self) -> Vec<usize> {
        (0..self.ctx.files.len())
            .filter(|&i| !self.ctx.files[i].js)
            .collect()
    }

    fn symbol(&self, node: Node) -> Option<SymbolId> {
        self.symbols.get(&node).map(|&(symbol, _)| symbol)
    }

    /// Reference positions that read or write the binding's value.
    fn value_reference(node: Node, kind: RefKind) -> bool {
        let parent = node.parent();
        match kind {
            RefKind::Read | RefKind::Call | RefKind::New | RefKind::Write => {
                !(parent.kind() == SyntaxKind::PropertyAccessExpression && parent.name() == node)
                    && parent.kind() != SyntaxKind::QualifiedName
            }
            RefKind::Declaration => {
                parent.kind() == SyntaxKind::ShorthandPropertyAssignment && parent.name() == node
            }
            RefKind::Alias | RefKind::Type => false,
        }
    }

    fn closed_function(&mut self, node: Node, binding: Node, symbol: SymbolId) -> bool {
        let file = get_source_file_of_node(node);
        if !is_external_module(file)
            || get_combined_modifier_flags(node).intersects(ModifierFlags::ASYNC)
            || node.asterisk_token().is_some()
            || !node.type_parameters().is_empty()
            || node.body().is_nil()
            || in_namespace(node)
        {
            return false;
        }
        let parameters: Vec<Node> = node.parameters().iter().collect();
        if !parameters.iter().all(|p| {
            p.name().kind() == SyntaxKind::Identifier
                && p.name().text() != "this"
                && p.initializer().is_nil()
                && p.dot_dot_dot_token().is_nil()
        }) {
            return false;
        }
        let mut body = Vec::new();
        collect_nodes(node.body(), &mut body);
        if body.iter().any(|n| {
            matches!(
                n.kind(),
                SyntaxKind::ThisKeyword | SyntaxKind::SuperKeyword | SyntaxKind::MetaProperty
            ) || (n.kind() == SyntaxKind::Identifier && n.text() == "arguments")
        }) {
            return false;
        }
        if !self.ctx.is_internal(symbol) || self.ctx.checker.sym(symbol).declarations.len() != 1 {
            return false;
        }
        self.ctx.refs(symbol).iter().all(|r| match r.kind {
            RefKind::Declaration => r.node == binding,
            RefKind::Alias => matches!(
                r.node.parent().kind(),
                SyntaxKind::ImportSpecifier
                    | SyntaxKind::ExportSpecifier
                    | SyntaxKind::ImportClause
            ),
            RefKind::Call => {
                let outer = walk_up_outer(r.node);
                let call = outer.parent();
                call.kind() == SyntaxKind::CallExpression
                    && call.expression() == outer
                    && call.arguments().len() == parameters.len()
                    && !call
                        .arguments()
                        .iter()
                        .any(|a| a.kind() == SyntaxKind::SpreadElement)
            }
            _ => false,
        })
    }

    fn target(&self, call: Node) -> Option<usize> {
        if call.kind() != SyntaxKind::CallExpression {
            return None;
        }
        let callee = skip_outer(call.expression());
        if callee.kind() != SyntaxKind::Identifier {
            return None;
        }
        self.targets.get(&self.symbol(callee)?).copied()
    }

    fn identifier(&mut self, node: Node) -> CellId {
        let Some(&(symbol, kind)) = self.symbols.get(&node) else {
            return UNKNOWN;
        };
        if let Some(&cell) = self.bindings.get(&symbol) {
            return if Self::value_reference(node, kind) {
                cell
            } else {
                UNKNOWN
            };
        }
        if node.text() == "undefined" && self.ctx.checker.sym(symbol).declarations.is_empty() {
            return EMPTY;
        }
        UNKNOWN
    }

    fn expr(&mut self, node: Node) -> CellId {
        if node.is_nil() {
            return UNKNOWN;
        }
        if let Some(&cell) = self.expressions.get(&node) {
            return cell;
        }
        let cell = match node.kind() {
            SyntaxKind::ObjectLiteralExpression => match self.allocations.get(&node) {
                Some(&object) => self.graph.holding(object),
                None => UNKNOWN,
            },
            SyntaxKind::StringLiteral => match self.tokens.get(&node) {
                Some(&token) => self.graph.holding(token),
                None => UNKNOWN,
            },
            SyntaxKind::Identifier => self.identifier(node),
            SyntaxKind::NullKeyword | SyntaxKind::VoidExpression => EMPTY,
            SyntaxKind::ParenthesizedExpression
            | SyntaxKind::AsExpression
            | SyntaxKind::SatisfiesExpression
            | SyntaxKind::NonNullExpression
            | SyntaxKind::TypeAssertionExpression => self.expr(node.expression()),
            SyntaxKind::ConditionalExpression => {
                let cell = self.graph.cell(false);
                let a = self.expr(node.when_true());
                let b = self.expr(node.when_false());
                self.graph.edge(a, cell);
                self.graph.edge(b, cell);
                cell
            }
            SyntaxKind::BinaryExpression => match node.operator_token().kind() {
                SyntaxKind::EqualsToken | SyntaxKind::CommaToken => self.expr(node.right()),
                SyntaxKind::AmpersandAmpersandToken
                | SyntaxKind::BarBarToken
                | SyntaxKind::QuestionQuestionToken => {
                    let cell = self.graph.cell(false);
                    let a = self.expr(node.left());
                    let b = self.expr(node.right());
                    self.graph.edge(a, cell);
                    self.graph.edge(b, cell);
                    cell
                }
                _ => UNKNOWN,
            },
            SyntaxKind::CallExpression => match self.target(node) {
                Some(index) => self.functions[index].result,
                None => UNKNOWN,
            },
            SyntaxKind::PropertyAccessExpression => {
                let receiver = self.expr(node.expression());
                if node.name().kind() != SyntaxKind::Identifier {
                    UNKNOWN
                } else {
                    self.load(receiver, Key::Name(node.name().text()))
                }
            }
            SyntaxKind::ElementAccessExpression => {
                let receiver = self.expr(node.expression());
                let key = self.element_key(node);
                self.load(receiver, key)
            }
            _ => UNKNOWN,
        };
        self.expressions.insert(node, cell);
        cell
    }

    fn element_key(&mut self, node: Node) -> Key {
        let argument = node.argument_expression();
        if argument.kind() == SyntaxKind::StringLiteral {
            Key::Name(argument.text())
        } else if self.options.finite_keys {
            Key::Cell(self.expr(argument))
        } else {
            Key::Dynamic
        }
    }

    fn load(&mut self, receiver: CellId, key: Key) -> CellId {
        match receiver {
            UNKNOWN => UNKNOWN,
            EMPTY => EMPTY,
            _ => {
                let cell = self.graph.cell(false);
                self.graph.flow(receiver, key, cell, false);
                cell
            }
        }
    }

    /// Records the annotation of a tracked value.
    fn annotate(&mut self, ty: Node, cell: CellId) {
        if ty.is_some() && cell > EMPTY {
            self.annotations.push((ty, cell));
        }
    }

    fn allocation(&mut self, file: usize, node: Node) {
        let source = get_source_file_of_node(node);
        let mut object = Object {
            file: self.ctx.files[file].name.clone(),
            start: node.pos() as usize,
            properties: Vec::new(),
            preservation: None,
        };
        let mut reason = None;
        if !is_external_module(source) || in_namespace(node) {
            reason = Some(Preservation::Exported);
        }
        for property in node.properties().iter() {
            let name = property.name();
            if !matches!(
                property.kind(),
                SyntaxKind::PropertyAssignment | SyntaxKind::ShorthandPropertyAssignment
            ) || !matches!(
                name.kind(),
                SyntaxKind::Identifier | SyntaxKind::StringLiteral
            ) {
                reason.get_or_insert(Preservation::ExternalOrUnsupportedShape);
                continue;
            }
            if property.kind() == SyntaxKind::PropertyAssignment
                && matches!(
                    skip_outer(property.initializer()).kind(),
                    SyntaxKind::FunctionExpression
                        | SyntaxKind::ArrowFunction
                        | SyntaxKind::ClassExpression
                )
            {
                // Renaming changes the inferred function or class `.name`.
                reason.get_or_insert(Preservation::ObservableOperation);
            }
            let key = name.text();
            if SPECIAL_NAMES.contains(&key) {
                reason.get_or_insert(Preservation::SpecialProperty);
            }
            let occurrence = occurrence(
                &self.ctx.files[file].name,
                name,
                property.kind() == SyntaxKind::ShorthandPropertyAssignment,
            );
            if let Some(existing) = object.properties.iter_mut().find(|p| p.name == key) {
                existing.occurrences.push(occurrence);
            } else {
                object.properties.push(Property {
                    name: key.to_owned(),
                    occurrences: vec![occurrence],
                });
            }
        }
        // Type evidence is necessary, never sufficient: the checker must see
        // exactly the literal's own data properties.
        let ty = self.ctx.checker.get_type_at_location(node);
        if !ty.is_nil()
            && self.ctx.checker.get_properties_of_type(ty).len() != object.properties.len()
        {
            reason.get_or_insert(Preservation::TypeConstraint);
        }
        let id = self.objects.len() as Value;
        self.allocations.insert(node, id);
        self.objects.push(object);
        self.intrinsic.push(reason);
    }

    /// String literals that may flow to computed keys.
    fn key_literal(node: Node) -> bool {
        if !renamable_key(node.text()) {
            return false;
        }
        let parent = node.parent();
        match parent.kind() {
            SyntaxKind::VariableDeclaration
            | SyntaxKind::PropertyAssignment
            | SyntaxKind::BindingElement => parent.initializer() == node,
            SyntaxKind::ConditionalExpression => parent.condition() != node,
            SyntaxKind::ParenthesizedExpression
            | SyntaxKind::AsExpression
            | SyntaxKind::SatisfiesExpression
            | SyntaxKind::ReturnStatement
            | SyntaxKind::CallExpression => true,
            SyntaxKind::ArrowFunction => parent.body() == node,
            SyntaxKind::BinaryExpression => matches!(
                parent.operator_token().kind(),
                SyntaxKind::EqualsToken
                    | SyntaxKind::CommaToken
                    | SyntaxKind::AmpersandAmpersandToken
                    | SyntaxKind::BarBarToken
                    | SyntaxKind::QuestionQuestionToken
            ),
            _ => false,
        }
    }

    /// A flat destructuring declaration `const {a, b: c = d} = value`.
    fn supported_pattern(pattern: Node) -> bool {
        pattern.kind() == SyntaxKind::ObjectBindingPattern
            && pattern.parent().kind() == SyntaxKind::VariableDeclaration
            && pattern.parent().initializer().is_some()
            && pattern.elements().iter().all(|element| {
                let key = if element.property_name().is_some() {
                    element.property_name()
                } else {
                    element.name()
                };
                element.dot_dot_dot_token().is_nil()
                    && element.name().kind() == SyntaxKind::Identifier
                    && matches!(
                        key.kind(),
                        SyntaxKind::Identifier | SyntaxKind::StringLiteral
                    )
            })
    }

    fn build(&mut self) -> Result<()> {
        let files = self.ts_files();
        // Symbols of every application identifier.
        for symbol in self.ctx.sorted_symbols() {
            for r in self.ctx.refs(symbol) {
                self.symbols.insert(r.node, (symbol, r.kind));
            }
        }
        // Allocations and key literals, in file and preorder order.
        for &file in &files {
            crate::app::check_cancelled()?;
            let nodes = std::mem::take(&mut self.ctx.files[file].nodes);
            for &node in &nodes {
                if node.kind() == SyntaxKind::ObjectLiteralExpression
                    && assignment_target(node).is_nil()
                {
                    self.allocation(file, node);
                }
            }
            self.ctx.files[file].nodes = nodes;
        }
        let mut names = Vec::new();
        if self.options.finite_keys {
            for &file in &files {
                for index in 0..self.ctx.files[file].nodes.len() {
                    let node = self.ctx.files[file].nodes[index];
                    if node.kind() == SyntaxKind::StringLiteral && Self::key_literal(node) {
                        let id = (self.objects.len() + names.len()) as Value;
                        self.tokens.insert(node, id);
                        names.push(node.text());
                        let occurrence = occurrence(&self.ctx.files[file].name, node, false);
                        self.token_info.push(Token {
                            occurrence: Occurrence {
                                key: true,
                                ..occurrence
                            },
                        });
                    }
                }
            }
        }
        self.graph = Graph::new(self.objects.len(), names);
        // Function summaries.
        for &file in &files {
            for index in 0..self.ctx.files[file].nodes.len() {
                let node = self.ctx.files[file].nodes[index];
                let binding = match node.kind() {
                    SyntaxKind::FunctionDeclaration if node.name().is_some() => node.name(),
                    SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression
                        if node.name().is_nil()
                            && node.parent().kind() == SyntaxKind::VariableDeclaration
                            && node.parent().initializer() == node
                            && node.parent().name().kind() == SyntaxKind::Identifier
                            && node.parent().parent().flags().intersects(NodeFlags::CONST)
                            && node.parent().type_().is_nil() =>
                    {
                        node.parent().name()
                    }
                    _ => continue,
                };
                let Some(symbol) = self.symbol(binding) else {
                    continue;
                };
                if !self.closed_function(node, binding, symbol) {
                    continue;
                }
                let result = self.graph.cell(false);
                let index = self.functions.len();
                self.functions.push(Function {
                    parameters: node.parameters().iter().collect(),
                    result,
                });
                self.targets.insert(symbol, index);
                self.owners.insert(node, index);
                self.annotate(node.type_(), result);
            }
        }
        // Bindings: variables, parameters and destructured names.
        for &file in &files {
            let module = is_external_module(self.ctx.files[file].node);
            for index in 0..self.ctx.files[file].nodes.len() {
                let node = self.ctx.files[file].nodes[index];
                if !matches!(
                    node.kind(),
                    SyntaxKind::VariableDeclaration
                        | SyntaxKind::Parameter
                        | SyntaxKind::BindingElement
                ) || node.name().is_nil()
                    || node.name().kind() != SyntaxKind::Identifier
                {
                    continue;
                }
                let Some(symbol) = self.symbol(node.name()) else {
                    continue;
                };
                let cell = match self.bindings.get(&symbol) {
                    Some(&cell) => cell,
                    None => {
                        let cell = self.graph.cell(false);
                        self.bindings.insert(symbol, cell);
                        if !module || in_namespace(node) || !self.ctx.is_internal(symbol) {
                            self.graph.set_unknown(cell);
                            self.sinks.push(cell);
                        }
                        cell
                    }
                };
                let understood = match node.kind() {
                    SyntaxKind::VariableDeclaration => {
                        node.initializer().is_some()
                            || node.parent().parent().kind() == SyntaxKind::VariableStatement
                    }
                    SyntaxKind::Parameter => self.owners.contains_key(&node.parent()),
                    _ => Self::supported_pattern(node.parent()),
                };
                if !understood {
                    self.graph.set_unknown(cell);
                }
                if node.kind() != SyntaxKind::BindingElement {
                    self.annotate(node.type_(), cell);
                }
            }
        }
        // Binding references: writes, `typeof` queries and positions that
        // are not values.
        let tracked: Vec<(SymbolId, CellId)> = {
            let mut list: Vec<_> = self.bindings.iter().map(|(&s, &c)| (s, c)).collect();
            list.sort_by_key(|&(s, _)| s.0);
            list
        };
        for (symbol, cell) in tracked {
            let refs = self.ctx.refs(symbol).to_vec();
            for r in refs {
                if self.ctx.files[r.file].js {
                    continue;
                }
                match r.kind {
                    RefKind::Type => {
                        let query = r.node.parent();
                        if !(query.kind() == SyntaxKind::TypeQuery && value_union_query(query)) {
                            self.constraints.push(cell);
                        }
                    }
                    RefKind::Alias => {}
                    RefKind::Declaration
                        if !Self::value_reference(r.node, RefKind::Declaration) => {}
                    kind => {
                        if !Self::value_reference(r.node, kind) {
                            // Namespace member or qualified access.
                            self.graph.set_unknown(cell);
                            self.sinks.push(cell);
                        } else if assignment_target(r.node).is_some() && !simple_assignment(r.node)
                        {
                            self.graph.set_unknown(cell);
                        }
                    }
                }
            }
        }
        // Graph edges and uses.
        for &file in &files {
            crate::app::check_cancelled()?;
            for index in 0..self.ctx.files[file].nodes.len() {
                let node = self.ctx.files[file].nodes[index];
                self.edges(node);
            }
        }
        for &file in &files {
            crate::app::check_cancelled()?;
            for index in 0..self.ctx.files[file].nodes.len() {
                let node = self.ctx.files[file].nodes[index];
                self.use_of(node);
            }
        }
        Ok(())
    }

    fn edges(&mut self, node: Node) {
        match node.kind() {
            SyntaxKind::VariableDeclaration if node.initializer().is_some() => {
                let name = node.name();
                if name.kind() == SyntaxKind::Identifier {
                    if let Some(&cell) = self.symbol(name).and_then(|s| self.bindings.get(&s)) {
                        let from = self.expr(node.initializer());
                        self.graph.edge(from, cell);
                    }
                } else if Self::supported_pattern(name) {
                    let receiver = self.expr(node.initializer());
                    for element in name.elements().iter() {
                        let key = if element.property_name().is_some() {
                            element.property_name()
                        } else {
                            element.name()
                        };
                        let Some(&cell) = self
                            .symbol(element.name())
                            .and_then(|s| self.bindings.get(&s))
                        else {
                            continue;
                        };
                        if receiver == UNKNOWN {
                            self.graph.set_unknown(cell);
                        } else if receiver != EMPTY {
                            self.graph
                                .flow(receiver, Key::Name(key.text()), cell, false);
                        }
                        if element.initializer().is_some() {
                            let from = self.expr(element.initializer());
                            self.graph.edge(from, cell);
                        }
                        if receiver > EMPTY {
                            let key = self.static_key(key, element.property_name().is_nil());
                            self.accesses.push(Access {
                                receiver,
                                key,
                                operation: false,
                            });
                        }
                    }
                }
            }
            SyntaxKind::BinaryExpression
                if node.operator_token().kind() == SyntaxKind::EqualsToken =>
            {
                let left = skip_outer(node.left());
                let value = self.expr(node.right());
                match left.kind() {
                    SyntaxKind::Identifier => {
                        if let Some(&cell) = self.symbol(left).and_then(|s| self.bindings.get(&s)) {
                            self.graph.edge(value, cell);
                        }
                    }
                    SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
                        self.store(left, value);
                    }
                    _ => {}
                }
            }
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
                // Compound, update, destructuring and loop writes store
                // unknown values.
                if assignment_target(node).is_some() && !simple_assignment(node) {
                    self.store(node, UNKNOWN);
                }
                let receiver = self.expr(node.expression());
                if receiver > EMPTY {
                    let key = if node.kind() == SyntaxKind::PropertyAccessExpression {
                        self.static_key(node.name(), false)
                    } else {
                        match self.element_key(node) {
                            Key::Name(_) => self.static_key(node.argument_expression(), false),
                            Key::Cell(cell) => AccessKey::Computed(cell),
                            Key::Dynamic => AccessKey::Dynamic,
                        }
                    };
                    let outer = walk_up_outer(node);
                    let parent = outer.parent();
                    let operation = (matches!(
                        parent.kind(),
                        SyntaxKind::CallExpression | SyntaxKind::TaggedTemplateExpression
                    ) && parent.expression() == outer)
                        || parent.kind() == SyntaxKind::DeleteExpression;
                    self.accesses.push(Access {
                        receiver,
                        key,
                        operation,
                    });
                }
                self.expr(node);
            }
            SyntaxKind::CallExpression => {
                if let Some(index) = self.target(node) {
                    let parameters = self.functions[index].parameters.clone();
                    for (argument, parameter) in node.arguments().iter().zip(parameters) {
                        let from = self.expr(argument);
                        if let Some(&cell) = self
                            .symbol(parameter.name())
                            .and_then(|s| self.bindings.get(&s))
                        {
                            self.graph.edge(from, cell);
                        }
                    }
                }
            }
            SyntaxKind::ReturnStatement if node.expression().is_some() => {
                if let Some(&index) = self.owners.get(&owner(node)) {
                    let from = self.expr(node.expression());
                    self.graph.edge(from, self.functions[index].result);
                }
            }
            SyntaxKind::ArrowFunction if node.body().kind() != SyntaxKind::Block => {
                if let Some(&index) = self.owners.get(&node) {
                    let from = self.expr(node.body());
                    self.graph.edge(from, self.functions[index].result);
                }
            }
            SyntaxKind::ObjectLiteralExpression => {
                if let Some(&object) = self.allocations.get(&node) {
                    for property in node.properties().iter() {
                        let value = match property.kind() {
                            SyntaxKind::PropertyAssignment => property.initializer(),
                            SyntaxKind::ShorthandPropertyAssignment => property.name(),
                            _ => continue,
                        };
                        let name = property.name();
                        if !matches!(
                            name.kind(),
                            SyntaxKind::Identifier | SyntaxKind::StringLiteral
                        ) {
                            continue;
                        }
                        let from = self.expr(value);
                        let field = self.graph.field(object, name.text());
                        self.graph.edge(from, field);
                    }
                }
            }
            SyntaxKind::AsExpression
            | SyntaxKind::SatisfiesExpression
            | SyntaxKind::TypeAssertionExpression => {
                let cell = self.expr(node.expression());
                if !is_const_type_reference(node.type_()) {
                    self.annotate(node.type_(), cell);
                }
            }
            _ => {}
        }
    }

    fn static_key(&mut self, name: Node, shorthand: bool) -> AccessKey {
        let file = self.file_name(name);
        self.spans.push(occurrence(&file, name, shorthand));
        AccessKey::Static {
            name: name.text(),
            occurrence: self.spans.len() - 1,
        }
    }

    fn store(&mut self, target: Node, value: CellId) {
        let receiver = self.expr(target.expression());
        let key = if target.kind() == SyntaxKind::PropertyAccessExpression {
            if target.name().kind() == SyntaxKind::Identifier {
                Key::Name(target.name().text())
            } else {
                Key::Dynamic
            }
        } else {
            self.element_key(target)
        };
        self.stores.push(Store {
            receiver,
            key,
            value,
        });
        if receiver > EMPTY {
            self.graph.flow(receiver, key, value, true);
        }
    }

    /// Classifies how the context of `node` uses its value.
    fn use_of(&mut self, node: Node) {
        let carrier = match node.kind() {
            SyntaxKind::ObjectLiteralExpression => self.allocations.contains_key(&node),
            SyntaxKind::StringLiteral => self.tokens.contains_key(&node),
            SyntaxKind::Identifier
            | SyntaxKind::ParenthesizedExpression
            | SyntaxKind::AsExpression
            | SyntaxKind::SatisfiesExpression
            | SyntaxKind::NonNullExpression
            | SyntaxKind::TypeAssertionExpression
            | SyntaxKind::ConditionalExpression
            | SyntaxKind::BinaryExpression
            | SyntaxKind::CallExpression
            | SyntaxKind::PropertyAccessExpression
            | SyntaxKind::ElementAccessExpression => true,
            _ => false,
        };
        if !carrier {
            return;
        }
        let cell = self.expr(node);
        if cell <= EMPTY {
            return;
        }
        let (objects, tokens) = self.context(node);
        if objects.is_some() || tokens {
            self.uses.push(Use {
                cell,
                objects,
                tokens,
            });
        }
    }

    /// `(object escape reason, key literals escape)` for a value in the
    /// context of `node`'s parent.
    fn context(&self, node: Node) -> (Option<Preservation2>, bool) {
        const ACCEPT: (Option<Preservation2>, bool) = (None, false);
        const ESCAPE: (Option<Preservation2>, bool) = (Some(Preservation2::Escapes), true);
        // Write targets: a plain assignment, destructuring or loop target
        // does not read the old value; compound writes and updates do.
        let target = assignment_target(node);
        if target.is_some() {
            return if simple_assignment(node)
                || matches!(
                    target.kind(),
                    SyntaxKind::ForInStatement | SyntaxKind::ForOfStatement
                )
                || (target.kind() == SyntaxKind::BinaryExpression
                    && target.operator_token().kind() == SyntaxKind::EqualsToken)
            {
                ACCEPT
            } else {
                ESCAPE
            };
        }
        let parent = node.parent();
        match parent.kind() {
            SyntaxKind::ParenthesizedExpression
            | SyntaxKind::NonNullExpression
            | SyntaxKind::AsExpression
            | SyntaxKind::SatisfiesExpression
            | SyntaxKind::TypeAssertionExpression
            | SyntaxKind::ConditionalExpression
            | SyntaxKind::ExpressionStatement
            | SyntaxKind::TypeOfExpression
            | SyntaxKind::VoidExpression => ACCEPT,
            SyntaxKind::IfStatement | SyntaxKind::WhileStatement | SyntaxKind::DoStatement => {
                ACCEPT
            }
            SyntaxKind::ForStatement if parent.condition() == node => ACCEPT,
            SyntaxKind::PrefixUnaryExpression
                if parent.operator() == SyntaxKind::ExclamationToken =>
            {
                ACCEPT
            }
            SyntaxKind::BinaryExpression => match parent.operator_token().kind() {
                SyntaxKind::EqualsToken => {
                    let left = skip_outer(parent.left());
                    let tracked = match left.kind() {
                        SyntaxKind::Identifier => self
                            .symbol(left)
                            .is_some_and(|s| self.bindings.contains_key(&s)),
                        SyntaxKind::PropertyAccessExpression
                        | SyntaxKind::ElementAccessExpression => true,
                        _ => false,
                    };
                    if tracked { ACCEPT } else { ESCAPE }
                }
                SyntaxKind::AmpersandAmpersandToken
                | SyntaxKind::BarBarToken
                | SyntaxKind::QuestionQuestionToken
                | SyntaxKind::CommaToken => ACCEPT,
                SyntaxKind::EqualsEqualsEqualsToken | SyntaxKind::ExclamationEqualsEqualsToken => {
                    (None, true)
                }
                _ => ESCAPE,
            },
            SyntaxKind::VariableDeclaration if parent.initializer() == node => {
                let name = parent.name();
                if name.kind() == SyntaxKind::Identifier {
                    if self
                        .symbol(name)
                        .is_some_and(|s| self.bindings.contains_key(&s))
                    {
                        ACCEPT
                    } else {
                        ESCAPE
                    }
                } else if Self::supported_pattern(name) {
                    (None, true)
                } else {
                    (Some(Preservation2::Unsupported), true)
                }
            }
            SyntaxKind::BindingElement if parent.initializer() == node => {
                if Self::supported_pattern(parent.parent()) {
                    ACCEPT
                } else {
                    ESCAPE
                }
            }
            SyntaxKind::CallExpression if parent.expression() != node => {
                if self.target(parent).is_some() {
                    ACCEPT
                } else {
                    ESCAPE
                }
            }
            SyntaxKind::ReturnStatement => {
                if self.owners.contains_key(&owner(parent)) {
                    ACCEPT
                } else {
                    ESCAPE
                }
            }
            SyntaxKind::ArrowFunction if parent.body() == node => {
                if self.owners.contains_key(&parent) {
                    ACCEPT
                } else {
                    ESCAPE
                }
            }
            // Receivers: accesses are analyzed separately. A key literal as
            // a receiver exposes its text.
            SyntaxKind::PropertyAccessExpression if parent.expression() == node => (None, true),
            SyntaxKind::ElementAccessExpression if parent.expression() == node => (None, true),
            // Keys: objects are converted to strings.
            SyntaxKind::ElementAccessExpression => (Some(Preservation2::Escapes), false),
            SyntaxKind::PropertyAssignment if parent.initializer() == node => {
                // Only static names are modeled as fields.
                if self.allocations.contains_key(&parent.parent())
                    && matches!(
                        parent.name().kind(),
                        SyntaxKind::Identifier | SyntaxKind::StringLiteral
                    )
                {
                    ACCEPT
                } else {
                    ESCAPE
                }
            }
            SyntaxKind::ShorthandPropertyAssignment if parent.name() == node => {
                if self.allocations.contains_key(&parent.parent()) {
                    ACCEPT
                } else {
                    ESCAPE
                }
            }
            SyntaxKind::DeleteExpression => (Some(Preservation2::Operation), true),
            _ => ESCAPE,
        }
    }

    fn evaluate(&self) -> Evaluation {
        let objects = self.objects.len();
        let total = objects + self.token_info.len();
        let mut reasons = self.intrinsic.clone();
        let mut token_escaped = vec![false; self.token_info.len()];
        let mut occurrences: Vec<Vec<(usize, Occurrence)>> = vec![Vec::new(); objects];
        let mut parents: Vec<usize> = (0..total).collect();
        let graph = &self.graph;
        let preserve = |reasons: &mut Vec<Option<Preservation>>,
                        token_escaped: &mut Vec<bool>,
                        cell: CellId,
                        reason: Option<Preservation>,
                        tokens: bool| {
            for &value in &graph.cells[cell as usize].values {
                if graph.is_object(value) {
                    if let Some(reason) = &reason {
                        reasons[value as usize].get_or_insert(reason.clone());
                    }
                } else if tokens {
                    token_escaped[(value - graph.objects) as usize] = true;
                }
            }
        };
        for &cell in &self.sinks {
            preserve(
                &mut reasons,
                &mut token_escaped,
                cell,
                Some(Preservation::Escapes),
                true,
            );
        }
        for &cell in &self.constraints {
            preserve(
                &mut reasons,
                &mut token_escaped,
                cell,
                Some(Preservation::TypeConstraint),
                true,
            );
        }
        for u in &self.uses {
            preserve(
                &mut reasons,
                &mut token_escaped,
                u.cell,
                u.objects.map(Preservation2::reason),
                u.tokens,
            );
        }
        // Values joined with unknown ones cannot be renamed alone, and every
        // cell joins the objects it may hold.
        for cell in &graph.cells {
            let mut first = None;
            for &value in &cell.values {
                if !graph.is_object(value) {
                    continue;
                }
                if cell.unknown {
                    reasons[value as usize].get_or_insert(Preservation::Escapes);
                }
                match first {
                    None => first = Some(value as usize),
                    Some(first) => union(&mut parents, first, value as usize),
                }
            }
        }
        // Writes into unknown receivers or unresolved keys escape the value.
        for store in &self.stores {
            let receiver = &graph.cells[store.receiver as usize];
            let unresolved = store.receiver == UNKNOWN
                || receiver.unknown
                || receiver.values.iter().any(|&v| !graph.is_object(v))
                || match store.key {
                    Key::Name(_) => false,
                    Key::Dynamic => true,
                    Key::Cell(cell) => {
                        let keys = &graph.cells[cell as usize];
                        keys.unknown
                            || keys.values.is_empty()
                            || keys.values.iter().any(|&v| graph.is_object(v))
                    }
                };
            if unresolved {
                preserve(
                    &mut reasons,
                    &mut token_escaped,
                    store.value,
                    Some(Preservation::Escapes),
                    true,
                );
            }
        }
        let record = |reasons: &mut Vec<Option<Preservation>>,
                      occurrences: &mut Vec<Vec<(usize, Occurrence)>>,
                      object: usize,
                      name: &str,
                      occurrence: &Occurrence| {
            match self.objects[object]
                .properties
                .iter()
                .position(|p| p.name == name)
            {
                Some(property) => occurrences[object].push((property, occurrence.clone())),
                None => {
                    reasons[object].get_or_insert(Preservation::ObservableOperation);
                }
            }
        };
        for access in &self.accesses {
            let receiver = &graph.cells[access.receiver as usize];
            let members: Vec<usize> = receiver
                .values
                .iter()
                .filter(|&&v| graph.is_object(v))
                .map(|&v| v as usize)
                .collect();
            if members.is_empty() {
                continue;
            }
            let mixed = receiver.unknown || members.len() != receiver.values.len();
            for &object in &members {
                if mixed {
                    reasons[object].get_or_insert(Preservation::Escapes);
                }
                if access.operation {
                    reasons[object].get_or_insert(Preservation::ObservableOperation);
                }
            }
            match access.key {
                AccessKey::Static { name, occurrence } => {
                    for &object in &members {
                        record(
                            &mut reasons,
                            &mut occurrences,
                            object,
                            name,
                            &self.spans[occurrence],
                        );
                    }
                }
                AccessKey::Computed(cell) => {
                    let keys = &graph.cells[cell as usize];
                    let resolved = !keys.unknown
                        && !keys.values.is_empty()
                        && keys.values.iter().all(|&v| !graph.is_object(v));
                    for &value in &keys.values {
                        if graph.is_object(value) {
                            continue;
                        }
                        let token = (value - graph.objects) as usize;
                        for &object in &members {
                            union(&mut parents, objects + token, object);
                            if resolved {
                                let name = graph.names[token];
                                record(
                                    &mut reasons,
                                    &mut occurrences,
                                    object,
                                    name,
                                    &self.token_info[token].occurrence,
                                );
                            }
                        }
                    }
                    if !resolved {
                        for &object in &members {
                            reasons[object].get_or_insert(Preservation::DynamicAccess);
                        }
                    }
                }
                AccessKey::Dynamic => {
                    for &object in &members {
                        reasons[object].get_or_insert(Preservation::DynamicAccess);
                    }
                }
            }
        }
        // Inline type literals are renamed with the values they describe;
        // any other annotation is a type constraint.
        for &(ty, cell) in &self.annotations {
            let mut levels = Vec::new();
            self.apply_annotation(
                ty,
                &graph.cells[cell as usize].values,
                &mut reasons,
                &mut token_escaped,
                &mut occurrences,
                &mut levels,
                0,
            );
            // Objects described by one type literal share its erased spans.
            for level in levels {
                for pair in level.windows(2) {
                    union(&mut parents, pair[0], pair[1]);
                }
            }
        }
        // A span renamed for several objects joins them.
        let mut spans = HashMap::<(String, usize, usize), usize>::new();
        for (object, list) in occurrences.iter().enumerate() {
            let definitions = self.objects[object]
                .properties
                .iter()
                .flat_map(|p| p.occurrences.iter());
            for occurrence in definitions.chain(list.iter().map(|(_, o)| o)) {
                let key = (occurrence.file.clone(), occurrence.start, occurrence.end);
                match spans.get(&key) {
                    Some(&other) => union(&mut parents, other, object),
                    None => {
                        spans.insert(key, object);
                    }
                }
            }
        }
        // Region preservation.
        let mut region = vec![None::<Preservation>; total];
        for element in 0..total {
            let reason = if element < objects {
                reasons[element].clone()
            } else if token_escaped[element - objects] {
                Some(Preservation::DynamicAccess)
            } else {
                None
            };
            if let Some(reason) = reason {
                let r = root(&mut parents, element);
                region[r].get_or_insert(reason);
            }
        }
        for object in 0..objects {
            let r = root(&mut parents, object);
            if let Some(reason) = &region[r] {
                reasons[object].get_or_insert(reason.clone());
            }
        }
        Evaluation {
            reasons,
            occurrences,
            parents,
        }
    }

    #[allow(clippy::too_many_arguments)]
    fn apply_annotation(
        &self,
        ty: Node,
        values: &BTreeSet<Value>,
        reasons: &mut [Option<Preservation>],
        token_escaped: &mut [bool],
        occurrences: &mut [Vec<(usize, Occurrence)>],
        levels: &mut Vec<Vec<usize>>,
        depth: usize,
    ) {
        let graph = &self.graph;
        if values.is_empty() || ty.is_nil() {
            return;
        }
        let objects: Vec<usize> = values
            .iter()
            .filter(|&&v| graph.is_object(v))
            .map(|&v| v as usize)
            .collect();
        let constrain = |reasons: &mut [Option<Preservation>], token_escaped: &mut [bool]| {
            for &value in values {
                if graph.is_object(value) {
                    reasons[value as usize].get_or_insert(Preservation::TypeConstraint);
                } else {
                    token_escaped[(value - graph.objects) as usize] = true;
                }
            }
        };
        match ty.kind() {
            SyntaxKind::AnyKeyword | SyntaxKind::UnknownKeyword => {}
            SyntaxKind::StringKeyword if objects.is_empty() => {}
            SyntaxKind::ParenthesizedType => self.apply_annotation(
                ty.type_(),
                values,
                reasons,
                token_escaped,
                occurrences,
                levels,
                depth,
            ),
            SyntaxKind::UnionType => {
                let mut literals = Vec::new();
                let mut supported = true;
                for member in ty.types().nodes().iter() {
                    match member.kind() {
                        SyntaxKind::UndefinedKeyword => {}
                        SyntaxKind::LiteralType
                            if member.literal().kind() == SyntaxKind::NullKeyword => {}
                        SyntaxKind::TypeLiteral => literals.push(member),
                        _ => supported = false,
                    }
                }
                if supported && literals.len() == 1 {
                    self.apply_annotation(
                        literals[0],
                        values,
                        reasons,
                        token_escaped,
                        occurrences,
                        levels,
                        depth,
                    );
                } else {
                    constrain(reasons, token_escaped);
                }
            }
            SyntaxKind::TypeLiteral
                if depth < 8
                    && objects.len() == values.len()
                    && ty.members().iter().all(|m| {
                        m.kind() == SyntaxKind::PropertySignature
                            && matches!(
                                m.name().kind(),
                                SyntaxKind::Identifier | SyntaxKind::StringLiteral
                            )
                    }) =>
            {
                let file = self.file_name(ty);
                levels.push(objects.clone());
                for member in ty.members().iter() {
                    let name = member.name();
                    let mut erased = occurrence(&file, name, false);
                    erased.erased = true;
                    let mut fields = BTreeSet::new();
                    for &object in &objects {
                        match self.objects[object]
                            .properties
                            .iter()
                            .position(|p| p.name == name.text())
                        {
                            Some(property) => occurrences[object].push((property, erased.clone())),
                            None => {
                                reasons[object].get_or_insert(Preservation::TypeConstraint);
                            }
                        }
                        if let Some(&field) = graph.fields.get(&(object as Value, name.text())) {
                            fields.extend(graph.cells[field as usize].values.iter().copied());
                        }
                    }
                    self.apply_annotation(
                        member.type_(),
                        &fields,
                        reasons,
                        token_escaped,
                        occurrences,
                        levels,
                        depth + 1,
                    );
                }
            }
            _ => {
                if !matches!(
                    ty.kind(),
                    SyntaxKind::NumberKeyword
                        | SyntaxKind::BooleanKeyword
                        | SyntaxKind::BigIntKeyword
                        | SyntaxKind::SymbolKeyword
                        | SyntaxKind::NullKeyword
                        | SyntaxKind::UndefinedKeyword
                        | SyntaxKind::VoidKeyword
                        | SyntaxKind::NeverKeyword
                ) || !values.is_empty()
                {
                    constrain(reasons, token_escaped);
                }
            }
        }
    }

    fn file_name(&self, node: Node) -> String {
        source_file_info(get_source_file_of_node(node))
            .file_name
            .clone()
    }

    fn finish(&mut self) -> Result<Vec<Object>> {
        let mut rounds = 0;
        let evaluation = loop {
            rounds += 1;
            let steps = self.graph.solve()?;
            let evaluation = self.evaluate();
            let mut changed = false;
            for object in 0..self.objects.len() {
                if evaluation.reasons[object].is_some() && !self.graph.escaped[object] {
                    self.graph.escape(object as Value);
                    changed = true;
                }
            }
            tracing::debug!(rounds, steps, "property flow iteration");
            if !changed {
                break evaluation;
            }
        };
        tracing::debug!(
            objects = self.objects.len(),
            key_literals = self.token_info.len(),
            functions = self.functions.len(),
            bindings = self.bindings.len(),
            cells = self.graph.cells.len(),
            flows = self.graph.flows.len(),
            rounds,
            "whole-program property flow reached fixed point"
        );
        let Evaluation {
            reasons,
            occurrences,
            mut parents,
        } = evaluation;
        let mut objects = std::mem::take(&mut self.objects);
        for (index, list) in occurrences.into_iter().enumerate() {
            for (property, occurrence) in list {
                objects[index].properties[property]
                    .occurrences
                    .push(occurrence);
            }
            objects[index].preservation = reasons[index].clone();
        }
        let mut merged = std::collections::BTreeMap::<usize, Object>::new();
        for (index, object) in objects.into_iter().enumerate() {
            let id = root(&mut parents, index);
            if let Some(target) = merged.get_mut(&id) {
                if target.preservation.is_none() {
                    target.preservation = object.preservation;
                }
                for property in object.properties {
                    if let Some(existing) = target
                        .properties
                        .iter_mut()
                        .find(|p| p.name == property.name)
                    {
                        existing.occurrences.extend(property.occurrences);
                    } else {
                        target.properties.push(property);
                    }
                }
            } else {
                merged.insert(id, object);
            }
        }
        for object in merged.values_mut() {
            for property in &mut object.properties {
                property
                    .occurrences
                    .sort_by(|a, b| (&a.file, a.start, a.end).cmp(&(&b.file, b.start, b.end)));
                property
                    .occurrences
                    .dedup_by(|a, b| a.file == b.file && a.start == b.start && a.end == b.end);
            }
        }
        Ok(merged.into_values().collect())
    }
}

/// Byte span of a property name; compiler positions are UTF-8 offsets
/// including leading trivia.
fn occurrence(file: &str, node: Node, shorthand: bool) -> Occurrence {
    let end = node.end() as usize;
    let start = token_start(node);
    Occurrence {
        file: file.to_owned(),
        start,
        end,
        shorthand,
        quoted: node.kind() == SyntaxKind::StringLiteral,
        erased: false,
        key: false,
    }
}

/// Analyzes every application TypeScript module and returns the object
/// regions with their property occurrences and preservation reasons.
pub(super) fn analyze(ctx: &mut Ctx, options: Options) -> Result<Vec<Object>> {
    let mut builder = Builder {
        ctx,
        options,
        graph: Graph::new(0, Vec::new()),
        symbols: HashMap::new(),
        bindings: HashMap::new(),
        expressions: HashMap::new(),
        functions: Vec::new(),
        targets: HashMap::new(),
        owners: HashMap::new(),
        allocations: HashMap::new(),
        tokens: HashMap::new(),
        objects: Vec::new(),
        intrinsic: Vec::new(),
        token_info: Vec::new(),
        spans: Vec::new(),
        uses: Vec::new(),
        accesses: Vec::new(),
        stores: Vec::new(),
        annotations: Vec::new(),
        sinks: Vec::new(),
        constraints: Vec::new(),
    };
    builder.build()?;
    builder.finish()
}
