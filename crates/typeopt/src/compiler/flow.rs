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
//! - Classes: instances of closed classes are values too (see [`classes`]).
//!   `this` in their instance members holds the instances of the class and its
//!   subclasses, `new` passes arguments to the nearest constructor, and
//!   `x.m(…)` dispatches to the method that each instance resolves `m` to.
//!   Names that instance code reads or writes become properties of the instance
//!   even without a declaration.
//! - Arrays: arrays created in analyzed code are container values (see
//!   [`arrays`]) with one element cell. Their elements flow through literals,
//!   spread, `push`-like methods, proven numeric indexes, `for…of` and inline
//!   callbacks, under the same escape rules: an array that reaches unknown
//!   code, an unmodeled member or a patchable built-in escapes with its
//!   elements. Arrays have no properties and are never reported.
use std::collections::{BTreeSet, HashMap, HashSet, VecDeque};

mod arrays;
mod classes;

use anyhow::Result;
pub(in crate::compiler) use arrays::{ArrayBuiltins, ArrayMethod};
use arrays::{ArraySite, ELEMENTS, Iterate};

use super::{
    analysis::{Ctx, RefKind, skip_outer, walk_up_outer},
    passes::runtime_facts::builtin_intact,
    token_start,
};
use crate::{
    compiler::api::*,
    optimizer::{Object, Occurrence, Preservation, Property},
};

#[derive(Clone, Copy, Debug)]
pub(super) struct Options {
    /// Coordinate finite computed keys with property renaming.
    pub finite_keys: bool,
    /// Built-in `Array` operations that application code cannot replace.
    pub arrays: ArrayBuiltins,
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

/// What an object value stands for.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Kind {
    /// A fresh object literal.
    Literal,
    /// The instances of a closed class (index into `Builder::classes`).
    Instance(usize),
    /// An array created in analyzed code (see [`arrays`]); never reported.
    Array,
}

#[derive(Clone, Copy)]
enum Key {
    Name(&'static str),
    Cell(CellId),
    Dynamic,
    /// A proven numeric key. On an array it reads or writes the element
    /// cell; on any other object it is an unresolved key. With a proof cell
    /// the key is `x.length`, numeric only while every value of the cell is
    /// an array.
    Index(Option<CellId>),
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

/// What a class instance resolves a method name to.
#[derive(Clone)]
enum Target {
    /// A summarized method: argument `i` flows into `params[i]`.
    Summary { params: Vec<CellId>, result: CellId },
    /// A method without a summary; its arguments escape.
    Opaque,
}

/// A call `receiver.name(args)` (or a construction) dispatched on the
/// instances that reach the receiver.
struct Dispatch {
    receiver: CellId,
    name: &'static str,
    args: Vec<CellId>,
    /// A spread argument shifts every later position.
    spread: bool,
    result: CellId,
    done: HashSet<Value>,
    /// The call is a modeled `Array.prototype` method name.
    array: Option<ArraySite>,
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
    dispatches: Vec<Dispatch>,
    dispatch_watchers: Vec<Vec<usize>>,
    /// Method of an instance by name, the nearest definition in its class
    /// chain (constructors under [`classes::CONSTRUCTOR`]).
    methods: HashMap<(Value, &'static str), Target>,
    /// Cells that unknown code reaches once an instance escapes: method
    /// parameters and results and the `this` cells of its class chain.
    exposure: HashMap<Value, Vec<CellId>>,
    kinds: Vec<Kind>,
    arrays: ArrayBuiltins,
    iterations: Vec<Iterate>,
    iteration_watchers: Vec<Vec<usize>>,
}

impl Graph {
    fn new(
        objects: usize,
        names: Vec<&'static str>,
        kinds: Vec<Kind>,
        arrays: ArrayBuiltins,
    ) -> Self {
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
            dispatches: Vec::new(),
            dispatch_watchers: Vec::new(),
            methods: HashMap::new(),
            exposure: HashMap::new(),
            kinds,
            arrays,
            iterations: Vec::new(),
            iteration_watchers: Vec::new(),
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
        self.dispatch_watchers.push(Vec::new());
        self.iteration_watchers.push(Vec::new());
        self.queued.push(false);
        id
    }

    fn dispatch(
        &mut self,
        receiver: CellId,
        name: &'static str,
        args: Vec<CellId>,
        spread: bool,
        result: CellId,
        array: Option<ArraySite>,
    ) {
        let index = self.dispatches.len();
        self.dispatches.push(Dispatch {
            receiver,
            name,
            args,
            spread,
            result,
            done: HashSet::new(),
            array,
        });
        self.dispatch_watchers[receiver as usize].push(index);
        self.fire_dispatch(index);
    }

    /// Connects the arguments and the result of a dispatched call with the
    /// method of every instance that reached its receiver. Any other
    /// receiver value calls code the analysis does not own.
    fn fire_dispatch(&mut self, index: usize) {
        let (receiver, name, spread, result) = {
            let d = &self.dispatches[index];
            (d.receiver, d.name, d.spread, d.result)
        };
        let values = self.cells[receiver as usize].clone();
        if values.unknown {
            self.set_unknown(result);
            self.expose_callback(index);
        }
        for value in values.values {
            if !self.dispatches[index].done.insert(value) {
                continue;
            }
            if self.is_array(value) {
                self.fire_array(index, value);
                continue;
            }
            // Any callback runs in code the analysis does not model.
            self.expose_callback(index);
            match self.methods.get(&(value, name)).cloned() {
                Some(Target::Summary {
                    params,
                    result: returned,
                }) => {
                    let args = self.dispatches[index].args.clone();
                    for (position, &param) in params.iter().enumerate() {
                        if spread {
                            self.set_unknown(param);
                            for &arg in &args {
                                self.edge(arg, param);
                            }
                        } else if let Some(&arg) = args.get(position) {
                            self.edge(arg, param);
                        }
                    }
                    self.edge(returned, result);
                }
                _ => self.set_unknown(result),
            }
        }
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
        match key {
            Key::Cell(key) | Key::Index(Some(key)) => self.watchers[key as usize].push(index),
            _ => {}
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
            // Resolved per receiver value below.
            Key::Index(_) => (Vec::new(), false),
        };
        let index_key = match key {
            Key::Index(proof) => Some(self.index_proven(proof)),
            _ => None,
        };
        let objects: Vec<Value> = values
            .values
            .iter()
            .copied()
            .filter(|&v| self.is_object(v))
            .collect();
        // An index key reaches the elements of arrays only; any other object
        // gets an unresolved key.
        let unresolved_index = index_key
            .is_some_and(|proven| !proven || objects.iter().any(|&object| !self.is_array(object)));
        if !store
            && (values.unknown
                || objects.len() != values.values.len()
                || (key_unknown && !objects.is_empty())
                || unresolved_index)
        {
            self.set_unknown(other);
        }
        for object in objects {
            // `length` of an array is a number; elements live in one cell.
            let array = self.is_array(object);
            if array && matches!(key, Key::Name("length")) {
                continue;
            }
            if let Some(proven) = index_key {
                if proven && array && self.flows[index].done.insert((object, NO_TOKEN)) {
                    let field = self.field(object, ELEMENTS);
                    if store {
                        self.edge(other, field);
                    } else {
                        self.edge(field, other);
                    }
                }
                continue;
            }
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
        // Unknown code may call an escaped instance's methods with any
        // arguments and receivers and observe their results.
        for cell in self.exposure.get(&object).cloned().unwrap_or_default() {
            if cell > EMPTY {
                self.set_unknown(cell);
            }
        }
    }

    fn solve(&mut self) -> Result<usize> {
        let mut steps = 0usize;
        while let Some(cell) = self.queue.pop_front() {
            steps += 1;
            if steps.is_multiple_of(65536) {
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
            for dispatch in self.dispatch_watchers[cell as usize].clone() {
                self.fire_dispatch(dispatch);
            }
            for iteration in self.iteration_watchers[cell as usize].clone() {
                self.fire_iterate(iteration);
            }
        }
        Ok(steps)
    }
}

struct Function {
    parameters: Vec<Node>,
    /// Cells of destructured parameters; identifier parameters use their
    /// binding cell.
    patterns: Vec<Option<CellId>>,
    result: CellId,
}

/// Why a function receives no trusted summary.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
enum FunctionBoundary {
    /// Script, namespace or bodiless declaration.
    Scope,
    AsyncOrGenerator,
    /// Nested pattern, rest or `this` parameters.
    Parameters,
    /// `this`, `super`, `arguments` or `new.target`.
    Receiver,
    /// Public, JavaScript-visible, exposed or merged symbol.
    External,
    /// A reference other than a direct fixed-arity call.
    References,
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
    /// A proven numeric key (see [`Key::Index`]).
    Index(Option<CellId>),
}

/// How an access uses the property.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Usage {
    /// A read or write of the value.
    Data,
    /// `receiver.name(…)`: the property is called with the receiver as
    /// `this`.
    Call,
    /// Tagged templates and `delete` observe more than a data property.
    Other,
}

#[derive(Clone)]
struct Access {
    receiver: CellId,
    key: AccessKey,
    usage: Usage,
    /// The access is a write target.
    write: bool,
}

/// A dispatched call whose arguments escape unless every receiver value is
/// an instance with a summarized method of that name.
struct MethodCall {
    receiver: CellId,
    name: &'static str,
    args: Vec<CellId>,
    /// The array method that the call runs on array receivers when its shape
    /// is understood and the built-in is intact; arrays are otherwise
    /// unresolved receivers.
    array: Option<ArrayMethod>,
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
    /// Array values are accepted in this context (a `for…of` operand or a
    /// spread into a tracked array literal); `objects` still applies to
    /// every other object.
    arrays: bool,
    /// Objects escape with this reason.
    objects: Option<Preservation2>,
    /// Key literals escape (their text would be observed).
    tokens: bool,
    /// Debug label of the context that makes objects escape.
    cause: &'static str,
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
    /// Cached answer of `define_property_intact`.
    define_property_intact: Option<bool>,
    token_info: Vec<Token>,
    /// Static access spans, referenced by index.
    spans: Vec<Occurrence>,
    uses: Vec<Use>,
    accesses: Vec<Access>,
    stores: Vec<Store>,
    annotations: Vec<(Node, CellId)>,
    /// Cells whose values escape, with a debug label.
    sinks: Vec<(CellId, &'static str)>,
    constraints: Vec<CellId>,
    /// `(receiver, key)` of every analyzed computed access.
    computed_keys: Vec<(CellId, CellId)>,
    /// Exports observable through module namespace objects.
    exposed: HashSet<SymbolId>,
    /// Why a function binding has no trusted summary (debug causes).
    boundary_of: HashMap<SymbolId, FunctionBoundary>,
    /// Closed classes and their lookup tables.
    classes: Vec<classes::Class>,
    class_by_node: HashMap<Node, usize>,
    class_by_symbol: HashMap<SymbolId, usize>,
    /// What each object value stands for.
    kinds: Vec<Kind>,
    /// Allocation sites of array values.
    array_allocs: HashMap<Node, Value>,
    /// Result cells of summarized callbacks (debug attribution).
    exposed_callbacks: Vec<CellId>,
    /// Summarized inline callbacks, by the call that passes them.
    callbacks: HashMap<Node, usize>,
    array_stats: arrays::ArrayStats,
    /// Cached numeric proofs of bindings and the proofs in progress.
    numeric_bindings: HashMap<SymbolId, bool>,
    numeric_active: HashSet<SymbolId>,
    /// Dispatched calls and constructions.
    calls: Vec<MethodCall>,
    dispatched: HashSet<Node>,
    /// Summaries of static methods of closed classes.
    static_targets: HashMap<(usize, &'static str), usize>,
}

/// Everything decided from the current cells.
struct Evaluation {
    reasons: Vec<Option<Preservation>>,
    /// Property index per occurrence; indices past an object's declared
    /// properties refer to `extras`.
    occurrences: Vec<Vec<(usize, Occurrence)>>,
    /// Undeclared names that code reads or writes on instances.
    extras: Vec<Vec<&'static str>>,
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

/// `{a, b: c, d = e}` with identifier bindings, static keys and no rest.
fn flat_pattern(pattern: Node) -> bool {
    pattern.kind() == SyntaxKind::ObjectBindingPattern
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
    /// The value stands for the instances of a closed class.
    fn is_instance(&self, value: usize) -> bool {
        matches!(self.kinds[value], Kind::Instance(_))
    }

    fn ts_files(&self) -> Vec<usize> {
        (0..self.ctx.files.len())
            .filter(|&i| !self.ctx.files[i].js)
            .collect()
    }

    /// Every runtime use of the symbol is an indexed reference.
    fn internal(&mut self, symbol: SymbolId) -> bool {
        !self.exposed.contains(&symbol) && self.ctx.is_internal(symbol)
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

    fn closed_function(
        &mut self,
        node: Node,
        binding: Node,
        symbol: SymbolId,
    ) -> Result<(), FunctionBoundary> {
        let file = get_source_file_of_node(node);
        if !is_external_module(file) || in_namespace(node) || node.body().is_nil() {
            return Err(FunctionBoundary::Scope);
        }
        if get_combined_modifier_flags(node).intersects(ModifierFlags::Async)
            || node.asterisk_token().is_some()
        {
            return Err(FunctionBoundary::AsyncOrGenerator);
        }
        // Generic signatures are fine: an annotation naming a type
        // parameter is a type constraint on the values it describes.
        let parameters: Vec<Node> = node.parameters().iter().collect();
        if !parameters.iter().all(|p| {
            p.dot_dot_dot_token().is_nil()
                && match p.name().kind() {
                    SyntaxKind::Identifier => p.name().text() != "this",
                    SyntaxKind::ObjectBindingPattern => flat_pattern(p.name()),
                    _ => false,
                }
        }) {
            return Err(FunctionBoundary::Parameters);
        }
        let mut body = Vec::new();
        collect_nodes(node.body(), &mut body);
        if body.iter().any(|n| {
            matches!(
                n.kind(),
                SyntaxKind::ThisKeyword | SyntaxKind::SuperKeyword | SyntaxKind::MetaProperty
            ) || (n.kind() == SyntaxKind::Identifier && n.text() == "arguments")
        }) {
            return Err(FunctionBoundary::Receiver);
        }
        if !self.internal(symbol) || self.ctx.checker.sym(symbol).declarations.len() != 1 {
            return Err(FunctionBoundary::External);
        }
        let calls_only = self.ctx.refs(symbol).iter().all(|r| match r.kind {
            // Export specifiers name the alias they declare.
            RefKind::Declaration => {
                r.node == binding || r.node.parent().kind() == SyntaxKind::ExportSpecifier
            }
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
        });
        if calls_only {
            Ok(())
        } else {
            Err(FunctionBoundary::References)
        }
    }

    /// Creates the summary of a function-like node whose parameters and
    /// returns the analysis owns.
    fn summarize(&mut self, node: Node) -> usize {
        let result = self.graph.cell(false);
        let index = self.functions.len();
        let parameters: Vec<Node> = node.parameters().iter().collect();
        let mut patterns = Vec::new();
        for &parameter in &parameters {
            if parameter.name().kind() == SyntaxKind::Identifier {
                patterns.push(None);
            } else {
                let cell = self.graph.cell(false);
                self.annotate(parameter.type_(), cell);
                patterns.push(Some(cell));
            }
        }
        self.functions.push(Function {
            parameters,
            patterns,
            result,
        });
        self.owners.insert(node, index);
        self.annotate(node.type_(), result);
        index
    }

    fn target(&self, call: Node) -> Option<usize> {
        if call.kind() != SyntaxKind::CallExpression {
            return None;
        }
        let callee = skip_outer(call.expression());
        match callee.kind() {
            SyntaxKind::Identifier => self.targets.get(&self.symbol(callee)?).copied(),
            // `C.m(…)` of a summarized static method of a closed class.
            SyntaxKind::PropertyAccessExpression => {
                let object = skip_outer(callee.expression());
                if object.kind() != SyntaxKind::Identifier
                    || callee.name().kind() != SyntaxKind::Identifier
                {
                    return None;
                }
                let class = *self.class_by_symbol.get(&self.symbol(object)?)?;
                self.static_target(class, callee.name().text())
            }
            _ => None,
        }
    }

    /// Debug label of why a call passes its arguments to unknown code.
    fn call_boundary(&self, call: Node) -> &'static str {
        let callee = skip_outer(call.expression());
        match callee.kind() {
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression => {
                "call-argument:method-call"
            }
            SyntaxKind::Identifier => {
                let Some(symbol) = self.symbol(callee) else {
                    return "call-argument:unresolved-callee";
                };
                match self.boundary_of.get(&symbol) {
                    Some(FunctionBoundary::Scope) => "call-argument:function-scope",
                    Some(FunctionBoundary::AsyncOrGenerator) => "call-argument:async-or-generator",
                    Some(FunctionBoundary::Parameters) => "call-argument:function-parameters",
                    Some(FunctionBoundary::Receiver) => "call-argument:function-receiver",
                    Some(FunctionBoundary::External) => "call-argument:function-external",
                    Some(FunctionBoundary::References) => "call-argument:function-references",
                    None => {
                        let declaration = self.ctx.checker.sym(symbol).value_declaration;
                        let function_valued = declaration.is_some()
                            && declaration.kind() == SyntaxKind::VariableDeclaration
                            && matches!(
                                skip_outer(declaration.initializer()).kind(),
                                SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression
                            );
                        if function_valued {
                            "call-argument:non-const-function-binding"
                        } else if declaration.is_some()
                            && declaration.kind() == SyntaxKind::Parameter
                        {
                            "call-argument:parameter-callee"
                        } else {
                            "call-argument:other-callee"
                        }
                    }
                }
            }
            _ => "call-argument:computed-callee",
        }
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
            SyntaxKind::ArrayLiteralExpression => match self.array_allocs.get(&node) {
                Some(&array) => self.graph.holding(array),
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
                None => self.method_call(node),
            },
            SyntaxKind::ThisKeyword => self.this_cell(node),
            SyntaxKind::SuperKeyword => self.super_cell(node),
            SyntaxKind::NewExpression => self.construction(node),
            SyntaxKind::PropertyAccessExpression => {
                let receiver = self.expr(node.expression());
                if matches!(
                    node.name().kind(),
                    SyntaxKind::Identifier | SyntaxKind::PrivateIdentifier
                ) {
                    self.load(receiver, Key::Name(node.name().text()))
                } else {
                    UNKNOWN
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
        } else if let Some(key) = self.index_key(argument) {
            key
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
        self.kinds.push(Kind::Literal);
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

    /// A flat destructuring declaration `const {a, b: c = d} = value` or
    /// a flat destructured parameter of a summarized function.
    fn supported_pattern(&self, pattern: Node) -> bool {
        flat_pattern(pattern)
            && match pattern.parent().kind() {
                SyntaxKind::VariableDeclaration => pattern.parent().initializer().is_some(),
                SyntaxKind::Parameter => self.owners.contains_key(&pattern.parent().parent()),
                _ => false,
            }
    }

    /// The cell receiving a summarized function's argument.
    fn parameter_cell(&self, function: usize, position: usize) -> Option<CellId> {
        let f = &self.functions[function];
        match f.patterns[position] {
            Some(cell) => Some(cell),
            None => self
                .symbol(f.parameters[position].name())
                .and_then(|s| self.bindings.get(&s))
                .copied(),
        }
    }

    /// Loads of a flat object pattern from `receiver`.
    fn destructure(&mut self, receiver: CellId, pattern: Node) {
        for element in pattern.elements().iter() {
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
                    usage: Usage::Data,
                    write: false,
                });
            }
        }
    }

    fn build(&mut self) -> Result<()> {
        let files = self.ts_files();
        // Symbols of every application identifier.
        for symbol in self.ctx.sorted_symbols() {
            for r in self.ctx.refs(symbol) {
                self.symbols.insert(r.node, (symbol, r.kind));
            }
        }
        // Class closure needs the exports that module namespaces expose.
        self.exposed = exposed_exports(self.ctx);
        let closed = self.closed_classes(&files);
        // Allocations, closed classes and key literals, in file and
        // preorder order.
        for &file in &files {
            crate::app::check_cancelled()?;
            let nodes = std::mem::take(&mut self.ctx.files[file].nodes);
            for &node in &nodes {
                if node.kind() == SyntaxKind::ObjectLiteralExpression
                    && assignment_target(node).is_nil()
                {
                    self.allocation(file, node);
                } else if let Some(candidate) = closed.get(&node) {
                    self.class_allocation(file, node, candidate);
                } else if Self::array_allocation_site(node) {
                    self.array_allocation(file, node);
                }
            }
            self.ctx.files[file].nodes = nodes;
        }
        self.link_classes();
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
        self.graph = Graph::new(
            self.objects.len(),
            names,
            self.kinds.clone(),
            self.options.arrays,
        );
        self.class_cells();
        // Function summaries.
        let mut boundaries = std::collections::BTreeMap::<FunctionBoundary, usize>::new();
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
                            && node.parent().parent().flags().intersects(NodeFlags::Const)
                            && node.parent().type_().is_nil() =>
                    {
                        node.parent().name()
                    }
                    _ => continue,
                };
                let Some(symbol) = self.symbol(binding) else {
                    continue;
                };
                if let Err(boundary) = self.closed_function(node, binding, symbol) {
                    *boundaries.entry(boundary).or_default() += 1;
                    self.boundary_of.insert(symbol, boundary);
                    continue;
                }
                let index = self.summarize(node);
                self.targets.insert(symbol, index);
            }
        }
        tracing::debug!(
            summaries = self.functions.len(),
            ?boundaries,
            "function summaries classified"
        );
        self.summarize_members();
        self.summarize_callbacks(&files);
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
                        if !module || in_namespace(node) || !self.internal(symbol) {
                            self.graph.set_unknown(cell);
                            let cause = if !module {
                                "sink:script-binding"
                            } else if in_namespace(node) {
                                "sink:namespace-binding"
                            } else if self.exposed.contains(&symbol) {
                                "sink:exposed-export"
                            } else if self.ctx.public.contains(&symbol) {
                                "sink:public-export"
                            } else if self.ctx.js_referenced.contains(&symbol) {
                                "sink:referenced-from-unchecked-code"
                            } else {
                                "sink:declared-outside-application"
                            };
                            self.sinks.push((cell, cause));
                        }
                        cell
                    }
                };
                let understood = match node.kind() {
                    SyntaxKind::VariableDeclaration => {
                        node.initializer().is_some()
                            || node.parent().parent().kind() == SyntaxKind::VariableStatement
                            || self.for_of_binding(node)
                    }
                    SyntaxKind::Parameter => self.owners.contains_key(&node.parent()),
                    _ => self.supported_pattern(node.parent()),
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
                            self.sinks.push((cell, "sink:non-value-reference"));
                        } else if assignment_target(r.node).is_some() && !simple_assignment(r.node)
                        {
                            self.graph.set_unknown(cell);
                        }
                    }
                }
            }
        }
        self.method_tables();
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
                } else if self.supported_pattern(name) {
                    let receiver = self.expr(node.initializer());
                    self.destructure(receiver, name);
                }
            }
            SyntaxKind::Parameter => {
                let Some(&function) = self.owners.get(&node.parent()) else {
                    return;
                };
                let position = node
                    .parent()
                    .parameters()
                    .iter()
                    .position(|p| p == node)
                    .expect("parameter belongs to its function");
                let Some(cell) = self.parameter_cell(function, position) else {
                    return;
                };
                // A default value applies when the argument is undefined.
                if node.initializer().is_some() {
                    let from = self.expr(node.initializer());
                    self.graph.edge(from, cell);
                }
                if node.name().kind() == SyntaxKind::ObjectBindingPattern {
                    self.destructure(cell, node.name());
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
                if node.kind() == SyntaxKind::ElementAccessExpression
                    && let Key::Cell(key) = self.element_key(node)
                {
                    self.computed_keys.push((receiver, key));
                }
                if receiver > EMPTY {
                    let key = if node.kind() == SyntaxKind::PropertyAccessExpression {
                        self.static_key(node.name(), false)
                    } else {
                        match self.element_key(node) {
                            Key::Name(_) => self.static_key(node.argument_expression(), false),
                            Key::Cell(cell) => AccessKey::Computed(cell),
                            Key::Dynamic => AccessKey::Dynamic,
                            Key::Index(proof) => AccessKey::Index(proof),
                        }
                    };
                    let outer = walk_up_outer(node);
                    let parent = outer.parent();
                    // A call counts as a method call only when it was
                    // dispatched; any other call through an instance
                    // connects neither its arguments nor its result.
                    let usage = if parent.kind() == SyntaxKind::CallExpression
                        && parent.expression() == outer
                    {
                        self.expr(parent);
                        if self.dispatched.contains(&parent) {
                            Usage::Call
                        } else {
                            Usage::Other
                        }
                    } else if (parent.kind() == SyntaxKind::TaggedTemplateExpression
                        && parent.tag() == outer)
                        || parent.kind() == SyntaxKind::DeleteExpression
                    {
                        Usage::Other
                    } else {
                        Usage::Data
                    };
                    self.accesses.push(Access {
                        receiver,
                        key,
                        usage,
                        write: assignment_target(node).is_some(),
                    });
                }
                self.expr(node);
            }
            SyntaxKind::CallExpression => {
                if let Some(index) = self.target(node) {
                    let arguments: Vec<Node> = node.arguments().iter().collect();
                    for (position, argument) in arguments.into_iter().enumerate() {
                        let from = self.expr(argument);
                        if let Some(cell) = self.parameter_cell(index, position) {
                            self.graph.edge(from, cell);
                        }
                    }
                } else {
                    // Dispatches method calls.
                    self.expr(node);
                }
            }
            SyntaxKind::NewExpression => {
                self.expr(node);
            }
            SyntaxKind::ArrayLiteralExpression => self.array_literal_edges(node),
            SyntaxKind::ForOfStatement => self.for_of_edges(node),
            SyntaxKind::PropertyDeclaration | SyntaxKind::GetAccessor | SyntaxKind::SetAccessor => {
                self.member_edges(node)
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
            if matches!(
                target.name().kind(),
                SyntaxKind::Identifier | SyntaxKind::PrivateIdentifier
            ) {
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
            SyntaxKind::ArrayLiteralExpression => self.array_allocs.contains_key(&node),
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
            | SyntaxKind::ElementAccessExpression
            | SyntaxKind::ThisKeyword
            | SyntaxKind::NewExpression => true,
            _ => false,
        };
        if !carrier {
            return;
        }
        let cell = self.expr(node);
        if cell <= EMPTY {
            return;
        }
        let (objects, tokens, cause) = self.context(node);
        if objects.is_some() || tokens {
            let arrays = objects.is_some() && self.arrays_accepted(node);
            self.uses.push(Use {
                cell,
                arrays,
                objects,
                tokens,
                cause,
            });
        }
    }

    /// `(object escape reason, key literals escape)` for a value in the
    /// context of `node`'s parent.
    fn context(&self, node: Node) -> (Option<Preservation2>, bool, &'static str) {
        const ACCEPT: (Option<Preservation2>, bool, &str) = (None, false, "");
        const fn escape(cause: &'static str) -> (Option<Preservation2>, bool, &'static str) {
            (Some(Preservation2::Escapes), true, cause)
        }
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
                escape("compound-write")
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
                    if tracked {
                        ACCEPT
                    } else {
                        escape("assignment-to-untracked")
                    }
                }
                SyntaxKind::AmpersandAmpersandToken
                | SyntaxKind::BarBarToken
                | SyntaxKind::QuestionQuestionToken
                | SyntaxKind::CommaToken => ACCEPT,
                SyntaxKind::EqualsEqualsEqualsToken | SyntaxKind::ExclamationEqualsEqualsToken => {
                    (None, true, "")
                }
                // Reads only the prototype chain of the left operand.
                SyntaxKind::InstanceOfKeyword
                    if parent.left() == node && self.ordinary_instance_check(parent.right()) =>
                {
                    (None, true, "")
                }
                _ => escape("binary-operator"),
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
                        escape("untracked-variable")
                    }
                } else if self.supported_pattern(name) {
                    (None, true, "")
                } else {
                    (Some(Preservation2::Unsupported), true, "")
                }
            }
            SyntaxKind::Parameter if parent.initializer() == node => {
                if self.owners.contains_key(&parent.parent()) {
                    ACCEPT
                } else {
                    escape("default-of-unsummarized-parameter")
                }
            }
            SyntaxKind::BindingElement if parent.initializer() == node => {
                if self.supported_pattern(parent.parent()) {
                    ACCEPT
                } else {
                    escape("binding-default")
                }
            }
            // Dispatched arguments escape during evaluation unless every
            // receiver resolves to a summarized method.
            SyntaxKind::CallExpression if parent.expression() != node => {
                if self.target(parent).is_some() || self.dispatched.contains(&parent) {
                    ACCEPT
                } else {
                    escape(self.call_boundary(parent))
                }
            }
            SyntaxKind::NewExpression
                if parent.expression() != node && self.dispatched.contains(&parent) =>
            {
                ACCEPT
            }
            SyntaxKind::PropertyDeclaration if parent.initializer() == node => {
                if self.instance_field(parent) {
                    ACCEPT
                } else {
                    escape("field-of-untracked-class")
                }
            }
            SyntaxKind::ReturnStatement => {
                if self.owners.contains_key(&owner(parent)) {
                    ACCEPT
                } else {
                    escape("return-from-unsummarized")
                }
            }
            SyntaxKind::ArrowFunction if parent.body() == node => {
                if self.owners.contains_key(&parent) {
                    ACCEPT
                } else {
                    escape("arrow-body-of-unsummarized")
                }
            }
            // Receivers: accesses are analyzed separately. A key literal as
            // a receiver exposes its text.
            SyntaxKind::PropertyAccessExpression if parent.expression() == node => (None, true, ""),
            SyntaxKind::ElementAccessExpression if parent.expression() == node => (None, true, ""),
            // Keys: objects are converted to strings.
            SyntaxKind::ElementAccessExpression => {
                (Some(Preservation2::Escapes), false, "element-key")
            }
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
                    escape("field-of-untracked-object")
                }
            }
            SyntaxKind::ShorthandPropertyAssignment if parent.name() == node => {
                if self.allocations.contains_key(&parent.parent()) {
                    ACCEPT
                } else {
                    escape("shorthand-of-untracked-object")
                }
            }
            SyntaxKind::DeleteExpression => (Some(Preservation2::Operation), true, ""),
            // Elements of a tracked array literal flow into its element cell.
            SyntaxKind::ArrayLiteralExpression if self.array_allocs.contains_key(&parent) => ACCEPT,
            kind => escape(kind_label(kind)),
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
        // Debug attribution: the first context that preserved each object.
        let debug = tracing::enabled!(tracing::Level::DEBUG);
        let mut causes: Vec<&'static str> = vec![""; objects];
        let stamp = |reasons: &[Option<Preservation>],
                     causes: &mut Vec<&'static str>,
                     label: &'static str| {
            if debug {
                for (object, cause) in causes.iter_mut().enumerate() {
                    if cause.is_empty() && reasons[object].is_some() {
                        *cause = label;
                    }
                }
            }
        };
        stamp(&reasons, &mut causes, "intrinsic");
        let preserve = |reasons: &mut Vec<Option<Preservation>>,
                        token_escaped: &mut Vec<bool>,
                        cell: CellId,
                        reason: Option<Preservation>,
                        tokens: bool,
                        arrays: bool| {
            for &value in &graph.cells[cell as usize].values {
                if graph.is_object(value) {
                    // A context may accept arrays and still escape the rest.
                    if arrays && graph.is_array(value) {
                        continue;
                    }
                    if let Some(reason) = &reason {
                        reasons[value as usize].get_or_insert(reason.clone());
                    }
                } else if tokens {
                    token_escaped[(value - graph.objects) as usize] = true;
                }
            }
        };
        for &(cell, cause) in &self.sinks {
            preserve(
                &mut reasons,
                &mut token_escaped,
                cell,
                Some(Preservation::Escapes),
                true,
                false,
            );
            if debug {
                for &value in &graph.cells[cell as usize].values {
                    if graph.is_object(value) && causes[value as usize].is_empty() {
                        causes[value as usize] = cause;
                    }
                }
            }
        }
        for &cell in &self.constraints {
            preserve(
                &mut reasons,
                &mut token_escaped,
                cell,
                Some(Preservation::TypeConstraint),
                true,
                false,
            );
        }
        stamp(&reasons, &mut causes, "type-constraint");
        for u in &self.uses {
            preserve(
                &mut reasons,
                &mut token_escaped,
                u.cell,
                u.objects.map(Preservation2::reason),
                u.tokens,
                u.arrays,
            );
            if debug && u.objects.is_some() {
                for &value in &graph.cells[u.cell as usize].values {
                    if graph.is_object(value)
                        && !(u.arrays && graph.is_array(value))
                        && causes[value as usize].is_empty()
                    {
                        causes[value as usize] = if u.cause.is_empty() { "use" } else { u.cause };
                    }
                }
            }
        }
        // Arguments of calls that may reach code without a summary escape.
        // Calls on arrays that are escaped, or that the model does not
        // cover, are attributed to the array.
        let mut escaping_calls = HashMap::<&str, usize>::new();
        let mut unresolved: Vec<(&MethodCall, bool)> = Vec::new();
        for call in &self.calls {
            let receiver = &graph.cells[call.receiver as usize];
            let mut array_blocked = false;
            let mut resolved = !receiver.unknown && !receiver.values.is_empty();
            for &v in &receiver.values {
                if graph.is_array(v) {
                    if call.array.is_none() || graph.escaped[v as usize] {
                        array_blocked = true;
                        resolved = false;
                    }
                } else if !matches!(
                    graph.methods.get(&(v, call.name)),
                    Some(Target::Summary { .. })
                ) {
                    resolved = false;
                }
            }
            if resolved {
                // Arguments that are not element inputs (an index or a bound)
                // escape as they do for any call.
                if call.array.is_some_and(ArrayMethod::escapes_arguments) {
                    for &arg in &call.args {
                        preserve(
                            &mut reasons,
                            &mut token_escaped,
                            arg,
                            Some(Preservation::Escapes),
                            true,
                            false,
                        );
                    }
                }
                continue;
            }
            if debug
                && call.args.iter().any(|&arg| {
                    graph.cells[arg as usize]
                        .values
                        .iter()
                        .any(|&v| graph.is_object(v) && self.is_instance(v as usize))
                })
            {
                *escaping_calls.entry(call.name).or_default() += 1;
            }
            unresolved.push((call, array_blocked));
        }
        for (label, array_calls) in [
            ("call-argument:escaped-array", true),
            ("call-argument:method-call", false),
        ] {
            for &(call, array_blocked) in &unresolved {
                if array_blocked != array_calls {
                    continue;
                }
                for &arg in &call.args {
                    preserve(
                        &mut reasons,
                        &mut token_escaped,
                        arg,
                        Some(Preservation::Escapes),
                        true,
                        false,
                    );
                }
            }
            stamp(&reasons, &mut causes, label);
        }
        if debug {
            let mut escaping_calls: Vec<_> = escaping_calls.into_iter().collect();
            escaping_calls.sort_by(|a, b| b.1.cmp(&a.1).then(a.0.cmp(b.0)));
            escaping_calls.truncate(30);
            tracing::debug!(?escaping_calls, "instances passed to unresolved calls");
        }
        // The results of a summarized callback are unknown when unknown code
        // can run it.
        for &cell in &self.exposed_callbacks {
            if graph.cells[cell as usize].unknown {
                preserve(
                    &mut reasons,
                    &mut token_escaped,
                    cell,
                    Some(Preservation::Escapes),
                    true,
                    false,
                );
            }
        }
        stamp(&reasons, &mut causes, "sink:exposed-callback-result");
        // Values joined with unknown ones cannot be renamed alone (unknown
        // code may read an escaped object's fields, including key literals
        // stored there), and every cell joins the objects it may hold.
        for cell in &graph.cells {
            let mut first = None;
            for &value in &cell.values {
                if !graph.is_object(value) {
                    if cell.unknown {
                        token_escaped[(value - graph.objects) as usize] = true;
                    }
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
        stamp(&reasons, &mut causes, "joined-with-unknown-value");
        // A key literal used on a receiver that is not purely tracked
        // objects keeps its text.
        for &(receiver, key) in &self.computed_keys {
            let cell = &graph.cells[receiver as usize];
            let owned = receiver > EMPTY
                && !cell.unknown
                && !cell.values.is_empty()
                && cell
                    .values
                    .iter()
                    .all(|&v| graph.is_object(v) && !graph.is_array(v));
            if !owned {
                preserve(&mut reasons, &mut token_escaped, key, None, true, false);
            }
        }
        // Writes into unknown receivers or unresolved keys escape the value.
        let mut length_stores = Vec::new();
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
                    Key::Index(proof) => {
                        !graph.index_proven(proof)
                            || receiver.values.iter().any(|&v| !graph.is_array(v))
                    }
                };
            if unresolved {
                preserve(
                    &mut reasons,
                    &mut token_escaped,
                    store.value,
                    Some(Preservation::Escapes),
                    true,
                    false,
                );
            }
            if matches!(store.key, Key::Name("length"))
                && receiver.values.iter().any(|&v| graph.is_array(v))
            {
                length_stores.push(store.value);
            }
        }
        stamp(&reasons, &mut causes, "store-into-unknown-receiver");
        // `array.length = value` truncates or extends the array.
        for value in length_stores {
            preserve(
                &mut reasons,
                &mut token_escaped,
                value,
                Some(Preservation::Escapes),
                true,
                false,
            );
        }
        stamp(&reasons, &mut causes, "store-into-array-length");
        // Arrays: a receiver that mixes arrays with other objects, and
        // members that the model does not cover, preserve the array. An
        // array that a dispatch already escaped is preserved too.
        let findings = self.array_findings();
        for &object in &findings.mixed {
            reasons[object].get_or_insert(Preservation::Escapes);
        }
        stamp(&reasons, &mut causes, "access:mixed-array-receiver");
        for &object in &findings.unmodeled {
            reasons[object].get_or_insert(Preservation::Escapes);
        }
        for object in 0..objects {
            if self.is_array(object) && graph.escaped[object] {
                reasons[object].get_or_insert(Preservation::Escapes);
            }
        }
        stamp(&reasons, &mut causes, "array:unmodeled-member");
        let mut extras: Vec<Vec<&'static str>> = vec![Vec::new(); objects];
        // Names that code writes on each instance.
        let mut written: Vec<HashSet<&'static str>> = vec![HashSet::new(); objects];
        // Literals have exactly their own properties. Instances gain every
        // undeclared name that code reads or writes, except private names
        // (never renamed) and names that `Object.prototype` defines.
        let record = |reasons: &mut Vec<Option<Preservation>>,
                      occurrences: &mut Vec<Vec<(usize, Occurrence)>>,
                      extras: &mut Vec<Vec<&'static str>>,
                      object: usize,
                      name: &'static str,
                      occurrence: &Occurrence| {
            let properties = &self.objects[object].properties;
            if let Some(property) = properties.iter().position(|p| p.name == name) {
                occurrences[object].push((property, occurrence.clone()));
            } else if !self.is_instance(object) {
                reasons[object].get_or_insert(Preservation::ObservableOperation);
            } else if classes::inherited_name(name) {
                reasons[object].get_or_insert(Preservation::SpecialProperty);
            } else if !name.starts_with('#') {
                let list = &mut extras[object];
                let index = match list.iter().position(|&n| n == name) {
                    Some(index) => index,
                    None => {
                        list.push(name);
                        list.len() - 1
                    }
                };
                occurrences[object].push((properties.len() + index, occurrence.clone()));
            }
        };
        // A call through an instance needs a method; reading or writing a
        // method's slot observes the function (and its `this`).
        let observes_method = |object: usize, name: &'static str, usage: Usage| {
            let method = graph.methods.contains_key(&(object as Value, name));
            match usage {
                Usage::Data => method,
                Usage::Call => !method,
                Usage::Other => true,
            }
        };
        for access in &self.accesses {
            let receiver = &graph.cells[access.receiver as usize];
            // Arrays have no properties; their accesses are classified above.
            let members: Vec<usize> = receiver
                .values
                .iter()
                .filter(|&&v| graph.is_object(v) && !graph.is_array(v))
                .map(|&v| v as usize)
                .collect();
            if members.is_empty() {
                continue;
            }
            let reaching = receiver
                .values
                .iter()
                .filter(|&&v| graph.is_object(v))
                .count();
            let mixed = receiver.unknown || reaching != receiver.values.len();
            let names: Vec<&'static str> = match access.key {
                AccessKey::Static { name, .. } => vec![name],
                AccessKey::Computed(cell) => graph.cells[cell as usize]
                    .values
                    .iter()
                    .filter(|&&v| !graph.is_object(v))
                    .map(|&v| graph.names[(v - graph.objects) as usize])
                    .collect(),
                AccessKey::Dynamic | AccessKey::Index(_) => Vec::new(),
            };
            for &object in &members {
                if mixed {
                    reasons[object].get_or_insert(Preservation::Escapes);
                }
                if access.write {
                    written[object].extend(names.iter().copied());
                }
                let observable = if self.is_instance(object) {
                    access.usage == Usage::Other
                        || names
                            .iter()
                            .any(|&name| observes_method(object, name, access.usage))
                } else {
                    access.usage != Usage::Data
                };
                if observable {
                    reasons[object].get_or_insert(Preservation::ObservableOperation);
                }
            }
            match access.key {
                AccessKey::Static { name, occurrence } => {
                    for &object in &members {
                        record(
                            &mut reasons,
                            &mut occurrences,
                            &mut extras,
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
                                    &mut extras,
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
                // An index key on a value that is not an array is unresolved.
                AccessKey::Dynamic | AccessKey::Index(_) => {
                    for &object in &members {
                        reasons[object].get_or_insert(Preservation::DynamicAccess);
                    }
                }
            }
        }
        stamp(&reasons, &mut causes, "access");
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
        stamp(&reasons, &mut causes, "annotation");
        // A name that no instance of a region declares or writes resolves
        // through the prototype chain, which code may extend
        // (`Object.prototype.x = …`); reading it keeps the region.
        let mut known = HashMap::<usize, HashSet<&str>>::new();
        for object in 0..objects {
            if !self.is_instance(object) {
                continue;
            }
            let names = known.entry(root(&mut parents, object)).or_default();
            names.extend(
                self.objects[object]
                    .properties
                    .iter()
                    .map(|p| p.name.as_str()),
            );
            names.extend(written[object].iter().copied());
        }
        for object in 0..objects {
            if extras[object].is_empty() {
                continue;
            }
            let names = &known[&root(&mut parents, object)];
            if extras[object].iter().any(|name| !names.contains(name)) {
                reasons[object].get_or_insert(Preservation::SpecialProperty);
            }
        }
        stamp(&reasons, &mut causes, "prototype-read");
        // Region preservation.
        let mut region = vec![None::<Preservation>; total];
        let mut region_cause = vec![""; total];
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
                if region[r].is_none() {
                    region_cause[r] = if element < objects {
                        causes[element]
                    } else {
                        "escaped-key-literal"
                    };
                }
                region[r].get_or_insert(reason);
            }
        }
        for (object, reason) in reasons.iter_mut().enumerate() {
            let r = root(&mut parents, object);
            if let Some(region) = &region[r] {
                if debug && reason.is_none() {
                    causes[object] = region_cause[r];
                }
                reason.get_or_insert(region.clone());
            }
        }
        if debug {
            // Preserved properties per cause, the most frequent first.
            let mut totals = HashMap::<&str, usize>::new();
            for (object, cause) in causes.iter().enumerate() {
                if reasons[object].is_some() {
                    *totals
                        .entry(if cause.is_empty() {
                            "unattributed"
                        } else {
                            cause
                        })
                        .or_default() += self.objects[object].properties.len();
                }
            }
            let mut totals: Vec<_> = totals.into_iter().collect();
            totals.sort_by(|a, b| b.1.cmp(&a.1).then(a.0.cmp(b.0)));
            totals.truncate(30);
            tracing::debug!(?totals, "property preservation causes");
            // Closed classes per cause, the most frequent first.
            let mut classes = HashMap::<&str, usize>::new();
            let mut renamed = 0usize;
            for (object, cause) in causes.iter().enumerate() {
                if !self.is_instance(object) {
                    continue;
                }
                tracing::debug!(
                    file = %self.objects[object].file,
                    start = self.objects[object].start,
                    reason = ?reasons[object],
                    cause,
                    "class instance classified"
                );
                if reasons[object].is_none() {
                    renamed += 1;
                } else {
                    *classes
                        .entry(if cause.is_empty() {
                            "unattributed"
                        } else {
                            cause
                        })
                        .or_default() += 1;
                }
            }
            let mut classes: Vec<_> = classes.into_iter().collect();
            classes.sort_by(|a, b| b.1.cmp(&a.1).then(a.0.cmp(b.0)));
            classes.truncate(30);
            tracing::debug!(renamed, preserved = ?classes, "class preservation causes");
            // Arrays per cause, the most frequent first, and the members of
            // arrays that the model does not cover.
            let mut preserved = HashMap::<&str, usize>::new();
            let mut intact = 0usize;
            for (object, cause) in causes.iter().enumerate() {
                if !self.is_array(object) {
                    continue;
                }
                if reasons[object].is_none() {
                    intact += 1;
                } else {
                    *preserved
                        .entry(if cause.is_empty() {
                            "unattributed"
                        } else {
                            cause
                        })
                        .or_default() += 1;
                }
            }
            let mut preserved: Vec<_> = preserved.into_iter().collect();
            preserved.sort_by(|a, b| b.1.cmp(&a.1).then(a.0.cmp(b.0)));
            preserved.truncate(30);
            let mut unmodeled_members: Vec<_> = findings.names.iter().collect();
            unmodeled_members.sort_by(|a, b| b.1.cmp(a.1).then(a.0.cmp(b.0)));
            unmodeled_members.truncate(30);
            tracing::debug!(
                intact,
                ?preserved,
                ?unmodeled_members,
                "array preservation causes"
            );
        }
        Evaluation {
            reasons,
            occurrences,
            extras,
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
            extras,
            mut parents,
        } = evaluation;
        let mut objects = std::mem::take(&mut self.objects);
        for (index, list) in occurrences.into_iter().enumerate() {
            for name in &extras[index] {
                objects[index].properties.push(Property {
                    name: (*name).to_owned(),
                    occurrences: Vec::new(),
                });
            }
            for (property, occurrence) in list {
                objects[index].properties[property]
                    .occurrences
                    .push(occurrence);
            }
            objects[index].preservation = reasons[index].clone();
        }
        // Regions in order of their first object. Arrays have no properties
        // and are never reported, even as the root of a region.
        let mut merged = Vec::<Object>::new();
        let mut position = HashMap::<usize, usize>::new();
        for (index, object) in objects.into_iter().enumerate() {
            if self.kinds[index] == Kind::Array {
                continue;
            }
            let id = root(&mut parents, index);
            if let Some(&target) = position.get(&id) {
                let target = &mut merged[target];
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
                position.insert(id, merged.len());
                merged.push(object);
            }
        }
        for object in &mut merged {
            for property in &mut object.properties {
                property
                    .occurrences
                    .sort_by(|a, b| (&a.file, a.start, a.end).cmp(&(&b.file, b.start, b.end)));
                property
                    .occurrences
                    .dedup_by(|a, b| a.file == b.file && a.start == b.start && a.end == b.end);
            }
        }
        Ok(merged)
    }
}

/// Exports that code can reach without an indexed reference: every export
/// of a module whose namespace object is a value (dynamic `import()`,
/// `import * as ns` used other than as `ns.member`, a namespace re-exported
/// from an entry, or a namespace that JavaScript imports). A dynamic import
/// with a computed specifier exposes every module.
fn exposed_exports(ctx: &mut Ctx) -> HashSet<SymbolId> {
    let mut modules = Vec::<SymbolId>::new();
    let mut all = false;
    for file in 0..ctx.files.len() {
        for index in 0..ctx.files[file].nodes.len() {
            let node = ctx.files[file].nodes[index];
            if node.kind() != SyntaxKind::CallExpression
                || node.expression().kind() != SyntaxKind::ImportKeyword
            {
                continue;
            }
            let specifier = node.arguments().first();
            match specifier {
                Some(specifier) if specifier.kind() == SyntaxKind::StringLiteral => {
                    let module = ctx.checker.get_symbol_at_location(specifier);
                    if module.is_some() {
                        modules.push(module);
                    }
                }
                // A computed specifier with a data-file extension (for
                // example `./locales/${code}.json`) cannot load a module.
                Some(specifier)
                    if specifier.kind() == SyntaxKind::TemplateExpression
                        && specifier
                            .template_spans()
                            .nodes()
                            .last()
                            .is_some_and(|span| {
                                let tail = span.literal().text();
                                tail.ends_with(".json") || tail.ends_with(".css")
                            }) => {}
                _ => all = true,
            }
        }
    }
    if all {
        for file in &ctx.files {
            let symbol = file.node.symbol();
            if symbol.is_some() {
                modules.push(symbol);
            }
        }
    }
    // Namespace values: module symbols referenced other than as `ns.member`.
    let mut symbols: Vec<SymbolId> = ctx.public.iter().copied().collect();
    symbols.extend(ctx.js_referenced.iter().copied());
    symbols.sort_by_key(|s| s.0);
    for symbol in symbols {
        if ctx
            .checker
            .sym(symbol)
            .flags
            .intersects(SymbolFlags::ValueModule)
        {
            modules.push(symbol);
        }
    }
    for symbol in ctx.sorted_symbols() {
        if !ctx
            .checker
            .sym(symbol)
            .flags
            .intersects(SymbolFlags::ValueModule)
        {
            continue;
        }
        let escapes = ctx.refs(symbol).iter().any(|r| match r.kind {
            RefKind::Alias | RefKind::Declaration => false,
            _ => {
                let parent = r.node.parent();
                !((parent.kind() == SyntaxKind::PropertyAccessExpression
                    && parent.expression() == r.node)
                    || (parent.kind() == SyntaxKind::QualifiedName && parent.left() == r.node))
            }
        });
        if escapes {
            modules.push(symbol);
        }
    }
    let mut exposed = HashSet::new();
    let mut seen = HashSet::new();
    while let Some(module) = modules.pop() {
        let module = ctx.checker.get_merged_symbol_exported(module);
        if !seen.insert(module)
            || !ctx
                .checker
                .sym(module)
                .flags
                .intersects(SymbolFlags::ValueModule)
        {
            continue;
        }
        for export in ctx.checker.get_exports_of_module_exported(module) {
            let target = ctx.skip_alias(export);
            exposed.insert(export);
            exposed.insert(target);
            if ctx
                .checker
                .sym(target)
                .flags
                .intersects(SymbolFlags::ValueModule)
            {
                modules.push(target);
            }
        }
    }
    tracing::debug!(
        modules = seen.len(),
        symbols = exposed.len(),
        "module namespace exports exposed"
    );
    exposed
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
        graph: Graph::new(0, Vec::new(), Vec::new(), ArrayBuiltins::NONE),
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
        define_property_intact: None,
        token_info: Vec::new(),
        spans: Vec::new(),
        uses: Vec::new(),
        accesses: Vec::new(),
        stores: Vec::new(),
        annotations: Vec::new(),
        sinks: Vec::new(),
        constraints: Vec::new(),
        computed_keys: Vec::new(),
        exposed: HashSet::new(),
        boundary_of: HashMap::new(),
        classes: Vec::new(),
        class_by_node: HashMap::new(),
        class_by_symbol: HashMap::new(),
        kinds: Vec::new(),
        array_allocs: HashMap::new(),
        exposed_callbacks: Vec::new(),
        callbacks: HashMap::new(),
        array_stats: arrays::ArrayStats::default(),
        numeric_bindings: HashMap::new(),
        numeric_active: HashSet::new(),
        calls: Vec::new(),
        dispatched: HashSet::new(),
        static_targets: HashMap::new(),
    };
    builder.build()?;
    builder.finish()
}

/// Static debug label of an unsupported value context.
fn kind_label(kind: SyntaxKind) -> &'static str {
    thread_local! {
        static LABELS: std::cell::RefCell<HashMap<SyntaxKind, &'static str>> =
            std::cell::RefCell::new(HashMap::new());
    }
    LABELS.with(|labels| {
        *labels
            .borrow_mut()
            .entry(kind)
            .or_insert_with(|| Box::leak(format!("context:{kind:?}").into_boxed_str()))
    })
}
