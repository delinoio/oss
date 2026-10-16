// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Arrays as container values of the property flow.
//!
//! Arrays created in analyzed code are values like object literals, so the
//! objects stored in them can still be renamed. Nothing here renames an array:
//! an array has no properties, never becomes a rename unit and is dropped
//! before the object regions are reported.
//!
//! Allocation sites (one value each, allocated before the graph exists):
//! - every `ArrayLiteralExpression` that is not an assignment target;
//! - every call `x.map(…)`, `x.filter(…)` or `x.slice(…)`, standing for the
//!   array that the call may return.
//!
//! `new Array`, `Array.of` and `Array.from` are not allocation sites; their
//! results stay unknown. Each array has one synthetic field named
//! [`ELEMENTS`], a name that can never be a property. Every element that code
//! stores flows into it and every element that code reads flows out of it.
//!
//! Element flows:
//! - literal elements flow into the element cell; `...x` in a literal loads the
//!   element cell of each array that reaches `x` and makes the elements unknown
//!   for any other operand;
//! - `arr[k]` reads and writes the element cell only when `k` is proven numeric
//!   ([`Key::Index`]); on a value that is not an array an index key behaves as
//!   an unresolved key;
//! - `for (const|let x of arr)` loads the element cell into `x`;
//! - reading `.length` yields a value that is never an object; a write to it
//!   (assignment, compound assignment, `++`, `--`, destructuring target)
//!   truncates or extends the array and is an escape boundary;
//! - the methods of [`ArrayMethod`] are modeled where a dispatched call meets
//!   an array receiver (`Graph::fire_array`); instances of closed classes use
//!   their own method tables.
//!
//! Soundness rules:
//! - Built-in precondition. A method is modeled only when application code
//!   cannot replace it (`Facts::patched` through [`ArrayBuiltins::detect`]).
//!   `map`, `filter` and `slice` also need `constructor` (species), iteration
//!   also needs `Symbol.iterator` and `next`. A write that could change what a
//!   numeric key means on `Array` or `Object.prototype`, and any form outside
//!   the intrinsic allowlist (see `intrinsic_exposure` in the runtime facts:
//!   every module, checked or unchecked, may reach `Array`, `Object`,
//!   `Reflect`, `Function`, `Proxy`, `eval`, the global object, the reflection
//!   keys and unproven computed keys only in the listed benign forms), disables
//!   every modeled operation.
//! - Callbacks. An inline arrow or function expression is summarized only as
//!   the single argument of a callback method (see [`CallbackBoundary`] for the
//!   rejections). Its parameters and result become unknown unless every
//!   receiver value of the call is a non-escaped array and the method is
//!   modeled. An array whose call has a callback that is not summarized
//!   escapes, because unknown code runs with its elements.
//! - Escapes. An escaped array exposes the results, callback cells and
//!   iteration variables of every call on it, because unknown code may have
//!   given it own members. Any member other than `length`, a proven index and a
//!   modeled method call preserves the array, and so does a receiver cell that
//!   mixes arrays with other objects.
use std::collections::{BTreeMap, HashMap, HashSet};

use super::{
    Builder, CellId, Dispatch, EMPTY, Graph, Key, Kind, UNKNOWN, Usage, Value, flat_pattern,
};
use crate::{
    compiler::{
        analysis::{Ctx, RefKind, skip_outer, walk_up_outer},
        api::*,
        passes::runtime_facts::patch_view,
    },
    optimizer::{Object, Preservation},
};

/// Name of the synthetic element field of an array. It starts with a space,
/// so it can never equal a property name that code writes.
pub(super) const ELEMENTS: &str = " elements";

/// The `Array.prototype` methods whose element flows are modeled.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub(in crate::compiler) enum ArrayMethod {
    Push,
    Unshift,
    Pop,
    Shift,
    At,
    Find,
    Slice,
    Filter,
    Map,
    IndexOf,
    Includes,
    FindIndex,
    Some,
    Every,
    ForEach,
}

impl ArrayMethod {
    pub(in crate::compiler) const ALL: [ArrayMethod; 15] = [
        ArrayMethod::Push,
        ArrayMethod::Unshift,
        ArrayMethod::Pop,
        ArrayMethod::Shift,
        ArrayMethod::At,
        ArrayMethod::Find,
        ArrayMethod::Slice,
        ArrayMethod::Filter,
        ArrayMethod::Map,
        ArrayMethod::IndexOf,
        ArrayMethod::Includes,
        ArrayMethod::FindIndex,
        ArrayMethod::Some,
        ArrayMethod::Every,
        ArrayMethod::ForEach,
    ];

    pub(in crate::compiler) fn name(self) -> &'static str {
        match self {
            ArrayMethod::Push => "push",
            ArrayMethod::Unshift => "unshift",
            ArrayMethod::Pop => "pop",
            ArrayMethod::Shift => "shift",
            ArrayMethod::At => "at",
            ArrayMethod::Find => "find",
            ArrayMethod::Slice => "slice",
            ArrayMethod::Filter => "filter",
            ArrayMethod::Map => "map",
            ArrayMethod::IndexOf => "indexOf",
            ArrayMethod::Includes => "includes",
            ArrayMethod::FindIndex => "findIndex",
            ArrayMethod::Some => "some",
            ArrayMethod::Every => "every",
            ArrayMethod::ForEach => "forEach",
        }
    }

    pub(super) fn from_name(name: &str) -> Option<ArrayMethod> {
        Self::ALL.into_iter().find(|method| method.name() == name)
    }

    fn bit(self) -> u16 {
        1 << (self as u16)
    }

    /// The single argument is a callback that receives the elements.
    pub(super) fn callback(self) -> bool {
        matches!(
            self,
            ArrayMethod::Find
                | ArrayMethod::Filter
                | ArrayMethod::Map
                | ArrayMethod::FindIndex
                | ArrayMethod::Some
                | ArrayMethod::Every
                | ArrayMethod::ForEach
        )
    }

    /// The call returns a fresh array that is created through the species
    /// constructor.
    pub(super) fn allocates(self) -> bool {
        matches!(
            self,
            ArrayMethod::Slice | ArrayMethod::Filter | ArrayMethod::Map
        )
    }

    /// Arguments that are not element inputs (an index or a bound) escape as
    /// they do for any call.
    pub(super) fn escapes_arguments(self) -> bool {
        matches!(
            self,
            ArrayMethod::At | ArrayMethod::Slice | ArrayMethod::Pop | ArrayMethod::Shift
        )
    }

    /// Whether the call shape is one the model understands.
    fn shape_ok(self, arguments: usize, spread: bool) -> bool {
        match self {
            ArrayMethod::Push | ArrayMethod::Unshift => true,
            _ if spread => false,
            ArrayMethod::Pop | ArrayMethod::Shift => arguments == 0,
            ArrayMethod::Slice => arguments <= 2,
            _ => arguments == 1,
        }
    }
}

/// Built-ins of `Array` that application code cannot replace, decided once
/// per program from the shared intrinsic patch scan.
#[derive(Clone, Copy, Debug)]
pub(in crate::compiler) struct ArrayBuiltins {
    methods: u16,
    /// Numeric index reads and writes are modeled.
    index: bool,
    /// `for…of` and spread over arrays are modeled.
    iteration: bool,
}

impl ArrayBuiltins {
    /// Nothing is modeled; every array escapes at its first use.
    pub(in crate::compiler) const NONE: ArrayBuiltins = ArrayBuiltins {
        methods: 0,
        index: false,
        iteration: false,
    };

    pub(super) fn enabled(self, method: ArrayMethod) -> bool {
        self.methods & method.bit() != 0
    }

    /// Queries the patch scan for every operation the model relies on.
    pub(in crate::compiler) fn detect(ctx: &mut Ctx) -> Self {
        let mut view = patch_view();
        let numeric = view.numeric_keys(ctx);
        let exposed = view
            .intrinsic_exposure(ctx)
            .map(|exposure| exposure.describe().to_owned());
        let global = numeric.clone().or(exposed);
        let mut disabled: Vec<(&'static str, String)> = Vec::new();
        let mut builtins = ArrayBuiltins::NONE;
        let constructor = view.array_member(ctx, "constructor");
        for method in ArrayMethod::ALL {
            let record = global
                .clone()
                .or_else(|| view.array_member(ctx, method.name()))
                .or_else(|| {
                    if method.allocates() {
                        constructor.clone()
                    } else {
                        None
                    }
                });
            match record {
                Some(record) => disabled.push((method.name(), record)),
                None => builtins.methods |= method.bit(),
            }
        }
        // `for…of` calls `[Symbol.iterator]` and then `next`.
        let iterator = global
            .clone()
            .or_else(|| view.array_member(ctx, "Symbol.iterator"))
            .or_else(|| view.array_member(ctx, "next"));
        match &iterator {
            Some(record) => disabled.push(("iteration", record.clone())),
            None => builtins.iteration = true,
        }
        // Holes and appended elements consult the prototype chain.
        match &global {
            Some(record) => disabled.push(("index", record.clone())),
            None => builtins.index = true,
        }
        let modeled: Vec<&str> = ArrayMethod::ALL
            .into_iter()
            .filter(|&method| builtins.enabled(method))
            .map(ArrayMethod::name)
            .collect();
        tracing::debug!(
            ?modeled,
            ?disabled,
            index = builtins.index,
            iteration = builtins.iteration,
            "array builtins intact"
        );
        builtins
    }
}

/// Why an inline function is not summarized as a callback.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub(super) enum CallbackBoundary {
    /// The call has other than exactly one argument (for example a `thisArg`).
    Arguments,
    /// The argument is not an inline arrow or function expression.
    NotInline,
    AsyncOrGenerator,
    /// A named function expression.
    NamedExpression,
    /// Rest, `this` or array-pattern parameters.
    Parameters,
    /// A function expression that uses `this`, `super` or `new.target`.
    Receiver,
    /// The body uses `arguments`.
    ArgumentsObject,
}

/// The summarized callback of a call: cells of parameters 0 to 2 (the
/// element, the index and the array) and the result.
#[derive(Clone)]
pub(super) struct CallbackCells {
    pub params: [Option<CellId>; 3],
    pub result: CellId,
}

/// What a dispatch needs to treat an array receiver.
#[derive(Clone)]
pub(super) struct ArraySite {
    pub method: ArrayMethod,
    /// The call shape is understood: argument count, no unsupported spread,
    /// a summarized callback where the method takes one, an allocated result
    /// array where the method creates one.
    pub well_formed: bool,
    /// The array that the call may return and a cell holding only it.
    pub alloc: Option<(Value, CellId)>,
    pub callback: Option<CallbackCells>,
}

/// A watcher that loads the elements of every array in `source` into
/// `target`; any other value makes `target` unknown.
pub(super) struct Iterate {
    pub source: CellId,
    pub target: CellId,
    pub done: HashSet<Value>,
}

/// Counters of the array allocation and callback summarization stages.
#[derive(Default)]
pub(super) struct ArrayStats {
    pub literals: usize,
    pub call_results: usize,
    pub summarized: usize,
    pub rejected: BTreeMap<CallbackBoundary, usize>,
}

/// Array accesses that preserve their receiver.
pub(super) struct ArrayFindings {
    /// Arrays in a receiver cell that also holds other objects, with those
    /// objects.
    pub mixed: Vec<usize>,
    /// Arrays that code reaches through a member the model does not cover.
    pub unmodeled: Vec<usize>,
    /// Counts of the unmodeled member names (debug).
    pub names: HashMap<String, usize>,
}

impl Graph {
    pub(super) fn is_array(&self, value: Value) -> bool {
        self.is_object(value) && self.kinds[value as usize] == Kind::Array
    }

    /// An index key is modeled and proven numeric for every value of its
    /// proof cell.
    pub(super) fn index_proven(&self, proof: Option<CellId>) -> bool {
        self.arrays.index
            && proof.is_none_or(|cell| {
                let cell = &self.cells[cell as usize];
                !cell.unknown && cell.values.iter().all(|&v| self.is_array(v))
            })
    }

    /// Connects `source`'s arrays to `target`; see [`Iterate`].
    pub(super) fn iterate(&mut self, source: CellId, target: CellId) {
        debug_assert!(target > EMPTY);
        match source {
            UNKNOWN => self.set_unknown(target),
            EMPTY => {}
            _ => {
                let index = self.iterations.len();
                self.iterations.push(Iterate {
                    source,
                    target,
                    done: HashSet::new(),
                });
                self.iteration_watchers[source as usize].push(index);
                self.fire_iterate(index);
            }
        }
    }

    pub(super) fn fire_iterate(&mut self, index: usize) {
        let (source, target) = (self.iterations[index].source, self.iterations[index].target);
        let values = self.cells[source as usize].clone();
        if values.unknown {
            self.set_unknown(target);
        }
        for value in values.values {
            if !self.iterations[index].done.insert(value) {
                continue;
            }
            if !self.is_array(value) || !self.arrays.iteration {
                self.set_unknown(target);
            } else if self.escaped[value as usize] {
                // Own members of an escaped array may replace the iterator.
                self.set_unknown(target);
            } else {
                let elements = self.field(value, ELEMENTS);
                self.edge(elements, target);
                self.exposure.entry(value).or_default().push(target);
            }
        }
    }

    /// Callback cells of a dispatch hold values that unknown code chose.
    pub(super) fn expose_callback(&mut self, index: usize) {
        let Some(callback) = self.dispatches[index]
            .array
            .as_ref()
            .and_then(|site| site.callback.clone())
        else {
            return;
        };
        for cell in callback.params.into_iter().flatten() {
            self.set_unknown(cell);
        }
        self.set_unknown(callback.result);
    }

    /// A call `array.name(…)` on one array value.
    pub(super) fn fire_array(&mut self, index: usize, value: Value) {
        let (result, args) = {
            let dispatch: &Dispatch = &self.dispatches[index];
            (dispatch.result, dispatch.args.clone())
        };
        let model = self.dispatches[index].array.clone().filter(|site| {
            site.well_formed && self.arrays.enabled(site.method) && !self.escaped[value as usize]
        });
        let Some(site) = model else {
            // An unmodeled method, call shape or built-in, or an array that
            // unknown code may have given own members: the call may run any
            // code with the arguments and the array.
            self.escape(value);
            self.set_unknown(result);
            self.expose_callback(index);
            return;
        };
        let elements = self.field(value, ELEMENTS);
        let mut exposure = vec![result];
        match site.method {
            ArrayMethod::Push | ArrayMethod::Unshift => {
                // A spread argument is the unknown cell.
                for &arg in &args {
                    self.edge(arg, elements);
                }
            }
            ArrayMethod::Pop | ArrayMethod::Shift | ArrayMethod::At | ArrayMethod::Find => {
                self.edge(elements, result);
            }
            ArrayMethod::Slice | ArrayMethod::Filter | ArrayMethod::Map => {
                if let Some((alloc, alloc_cell)) = site.alloc {
                    let target = self.field(alloc, ELEMENTS);
                    self.edge(alloc_cell, result);
                    match (&site.callback, site.method) {
                        (Some(callback), ArrayMethod::Map) => self.edge(callback.result, target),
                        _ => self.edge(elements, target),
                    }
                }
            }
            _ => {}
        }
        if let Some(callback) = &site.callback {
            exposure.extend(callback.params.iter().flatten().copied());
            exposure.push(callback.result);
            if let Some(element) = callback.params[0] {
                self.edge(elements, element);
            }
            if let Some(array) = callback.params[2] {
                let holder = self.holding(value);
                self.edge(holder, array);
            }
        }
        let list = self.exposure.entry(value).or_default();
        list.extend(exposure.into_iter().filter(|&cell| cell > EMPTY));
    }
}

impl Builder<'_, '_> {
    pub(super) fn is_array(&self, value: usize) -> bool {
        self.kinds[value] == Kind::Array
    }

    /// Whether `node` allocates an array value.
    pub(super) fn array_allocation_site(node: Node) -> bool {
        match node.kind() {
            // A destructuring target is not a value.
            SyntaxKind::ArrayLiteralExpression => super::assignment_target(node).is_nil(),
            SyntaxKind::CallExpression => {
                let callee = skip_outer(node.expression());
                callee.kind() == SyntaxKind::PropertyAccessExpression
                    && callee.name().kind() == SyntaxKind::Identifier
                    && ArrayMethod::from_name(callee.name().text())
                        .is_some_and(ArrayMethod::allocates)
            }
            _ => false,
        }
    }

    /// Creates the value of an array allocation site. Arrays have no
    /// properties and are never reported.
    pub(super) fn array_allocation(&mut self, file: usize, node: Node) {
        let source = get_source_file_of_node(node);
        let object = Object {
            file: self.ctx.files[file].name.clone(),
            start: node.pos() as usize,
            properties: Vec::new(),
            preservation: None,
        };
        let reason = (!is_external_module(source) || super::in_namespace(node))
            .then_some(Preservation::Exported);
        let id = self.objects.len() as Value;
        self.array_allocs.insert(node, id);
        self.objects.push(object);
        self.intrinsic.push(reason);
        self.kinds.push(Kind::Array);
        if node.kind() == SyntaxKind::ArrayLiteralExpression {
            self.array_stats.literals += 1;
        } else {
            self.array_stats.call_results += 1;
        }
    }

    /// Summarizes the inline callbacks of calls to callback methods and logs
    /// the array allocation counters.
    pub(super) fn summarize_callbacks(&mut self, files: &[usize]) {
        for &file in files {
            for index in 0..self.ctx.files[file].nodes.len() {
                let call = self.ctx.files[file].nodes[index];
                if call.kind() != SyntaxKind::CallExpression {
                    continue;
                }
                let callee = skip_outer(call.expression());
                if callee.kind() != SyntaxKind::PropertyAccessExpression
                    || callee.name().kind() != SyntaxKind::Identifier
                    || !ArrayMethod::from_name(callee.name().text())
                        .is_some_and(ArrayMethod::callback)
                    || self.target(call).is_some()
                {
                    continue;
                }
                match self.callback_function(call) {
                    Ok(function) => {
                        let summary = self.summarize(function);
                        self.exposed_callbacks.push(self.functions[summary].result);
                        self.callbacks.insert(call, summary);
                        self.array_stats.summarized += 1;
                    }
                    Err(boundary) => {
                        *self.array_stats.rejected.entry(boundary).or_default() += 1;
                    }
                }
            }
        }
        tracing::debug!(
            literals = self.array_stats.literals,
            call_results = self.array_stats.call_results,
            summarized_callbacks = self.array_stats.summarized,
            rejected_callbacks = ?self.array_stats.rejected,
            "array values allocated"
        );
    }

    /// The inline function that a callback method call passes, if the model
    /// can summarize it.
    fn callback_function(&self, call: Node) -> Result<Node, CallbackBoundary> {
        let arguments: Vec<Node> = call.arguments().iter().collect();
        let [argument] = arguments.as_slice() else {
            return Err(CallbackBoundary::Arguments);
        };
        let mut function = *argument;
        while function.kind() == SyntaxKind::ParenthesizedExpression {
            function = function.expression();
        }
        if !matches!(
            function.kind(),
            SyntaxKind::ArrowFunction | SyntaxKind::FunctionExpression
        ) {
            return Err(CallbackBoundary::NotInline);
        }
        if get_combined_modifier_flags(function).intersects(ModifierFlags::Async)
            || function.asterisk_token().is_some()
        {
            return Err(CallbackBoundary::AsyncOrGenerator);
        }
        if function.name().is_some() {
            return Err(CallbackBoundary::NamedExpression);
        }
        let parameters_ok = function.parameters().iter().all(|p| {
            p.dot_dot_dot_token().is_nil()
                && match p.name().kind() {
                    SyntaxKind::Identifier => p.name().text() != "this",
                    SyntaxKind::ObjectBindingPattern => flat_pattern(p.name()),
                    _ => false,
                }
        });
        if !parameters_ok {
            return Err(CallbackBoundary::Parameters);
        }
        let mut body = Vec::new();
        super::collect_nodes(function.body(), &mut body);
        if body
            .iter()
            .any(|n| n.kind() == SyntaxKind::Identifier && n.text() == "arguments")
        {
            return Err(CallbackBoundary::ArgumentsObject);
        }
        if function.kind() == SyntaxKind::FunctionExpression
            && body.iter().any(|n| {
                matches!(
                    n.kind(),
                    SyntaxKind::ThisKeyword | SyntaxKind::SuperKeyword | SyntaxKind::MetaProperty
                )
            })
        {
            return Err(CallbackBoundary::Receiver);
        }
        Ok(function)
    }

    /// A receiver that never reaches a dispatch (unknown or nullish) hands
    /// the callback to unknown code, or to none.
    pub(super) fn expose_inline_callback(&mut self, call: Node) {
        if let Some(callback) = self.callback_cells(call) {
            for cell in callback.params.into_iter().flatten() {
                self.graph.set_unknown(cell);
            }
            self.graph.set_unknown(callback.result);
        }
    }

    /// The cells of the summarized callback that `call` passes. Parameter
    /// bindings exist only after the summaries, so the cells are resolved when
    /// the call is dispatched.
    fn callback_cells(&self, call: Node) -> Option<CallbackCells> {
        let &summary = self.callbacks.get(&call)?;
        let params = [0, 1, 2].map(|position| {
            (position < self.functions[summary].parameters.len())
                .then(|| self.parameter_cell(summary, position))
                .flatten()
        });
        Some(CallbackCells {
            params,
            result: self.functions[summary].result,
        })
    }

    /// The array side of a dispatched call `receiver.name(…)`.
    pub(super) fn array_site(&mut self, call: Node, name: &'static str) -> Option<ArraySite> {
        if call.kind() != SyntaxKind::CallExpression {
            return None;
        }
        let method = ArrayMethod::from_name(name)?;
        let arguments = call.arguments();
        let spread = arguments
            .iter()
            .any(|a| a.kind() == SyntaxKind::SpreadElement);
        let mut well_formed = method.shape_ok(arguments.len(), spread);
        let alloc = if method.allocates() {
            let alloc = self.array_allocs.get(&call).copied();
            well_formed &= alloc.is_some();
            alloc.map(|value| (value, self.graph.holding(value)))
        } else {
            None
        };
        let callback = if method.callback() {
            let callback = self.callback_cells(call);
            well_formed &= callback.is_some();
            callback
        } else {
            None
        };
        Some(ArraySite {
            method,
            well_formed,
            alloc,
            callback,
        })
    }

    // ------------------------------------------------------------------
    // Iteration
    // ------------------------------------------------------------------

    /// The supported form `for (const|let x of …)`; returns the loop
    /// variable's declaration.
    pub(super) fn for_of_declaration(&self, statement: Node) -> Option<Node> {
        if statement.kind() != SyntaxKind::ForOfStatement
            || statement
                .raw()
                .as_for_in_or_of_statement()
                .await_modifier()
                .is_some()
        {
            return None;
        }
        let list = statement.initializer();
        if list.kind() != SyntaxKind::VariableDeclarationList
            || !list.flags().intersects(NodeFlags::Const | NodeFlags::Let)
            || list.flags().intersects(NodeFlags::Using)
        {
            return None;
        }
        let declarations: Vec<Node> = list.declarations().iter().collect();
        let [declaration] = declarations.as_slice() else {
            return None;
        };
        (declaration.name().kind() == SyntaxKind::Identifier).then_some(*declaration)
    }

    /// `loop variable` of a supported `for…of`.
    pub(super) fn for_of_binding(&self, declaration: Node) -> bool {
        let list = declaration.parent();
        list.kind() == SyntaxKind::VariableDeclarationList
            && self
                .for_of_declaration(list.parent())
                .is_some_and(|d| d == declaration)
    }

    /// `for…of` and spread operands whose arrays are not escaped by the
    /// loop or the spread itself.
    pub(super) fn arrays_accepted(&self, node: Node) -> bool {
        let parent = node.parent();
        match parent.kind() {
            SyntaxKind::ForOfStatement if parent.expression() == node => {
                self.options.arrays.iteration && self.for_of_declaration(parent).is_some()
            }
            SyntaxKind::SpreadElement if parent.expression() == node => {
                let literal = parent.parent();
                self.options.arrays.iteration
                    && literal.kind() == SyntaxKind::ArrayLiteralExpression
                    && self.array_allocs.contains_key(&literal)
            }
            _ => false,
        }
    }

    /// Element flows of a tracked array literal.
    pub(super) fn array_literal_edges(&mut self, node: Node) {
        let Some(&array) = self.array_allocs.get(&node) else {
            return;
        };
        let elements = self.graph.field(array, ELEMENTS);
        for element in node.elements().iter() {
            match element.kind() {
                SyntaxKind::OmittedExpression => {}
                SyntaxKind::SpreadElement => {
                    let source = self.expr(element.expression());
                    if self.options.arrays.iteration {
                        self.graph.iterate(source, elements);
                    } else {
                        self.graph.set_unknown(elements);
                    }
                }
                _ => {
                    let from = self.expr(element);
                    self.graph.edge(from, elements);
                }
            }
        }
    }

    /// Loads the elements of the iterated arrays into a `for…of` variable.
    pub(super) fn for_of_edges(&mut self, statement: Node) {
        let Some(declaration) = self.for_of_declaration(statement) else {
            return;
        };
        let Some(&binding) = self
            .symbol(declaration.name())
            .and_then(|symbol| self.bindings.get(&symbol))
        else {
            return;
        };
        let source = self.expr(statement.expression());
        if self.options.arrays.iteration {
            self.graph.iterate(source, binding);
        } else {
            self.graph.set_unknown(binding);
        }
    }

    // ------------------------------------------------------------------
    // Index keys
    // ------------------------------------------------------------------

    /// `arr[k]` with `k` proven numeric, or `arr[x.length]`.
    pub(super) fn index_key(&mut self, argument: Node) -> Option<Key> {
        let argument = skip_outer(argument);
        if self.numeric(argument) {
            return Some(Key::Index(None));
        }
        if argument.kind() == SyntaxKind::PropertyAccessExpression
            && argument.question_dot_token().is_nil()
            && argument.name().kind() == SyntaxKind::Identifier
            && argument.name().text() == "length"
        {
            // Numeric when every receiver value is an array.
            let receiver = self.expr(argument.expression());
            if receiver > EMPTY {
                return Some(Key::Index(Some(receiver)));
            }
        }
        None
    }

    /// The value of `node` is always a number (or a bigint), whatever its
    /// operands are.
    fn numeric(&mut self, node: Node) -> bool {
        let node = skip_outer(node);
        match node.kind() {
            SyntaxKind::NumericLiteral => true,
            SyntaxKind::PrefixUnaryExpression => matches!(
                node.operator(),
                SyntaxKind::MinusToken
                    | SyntaxKind::PlusToken
                    | SyntaxKind::TildeToken
                    | SyntaxKind::PlusPlusToken
                    | SyntaxKind::MinusMinusToken
            ),
            SyntaxKind::PostfixUnaryExpression => true,
            SyntaxKind::BinaryExpression => match node.operator_token().kind() {
                SyntaxKind::MinusToken
                | SyntaxKind::AsteriskToken
                | SyntaxKind::AsteriskAsteriskToken
                | SyntaxKind::SlashToken
                | SyntaxKind::PercentToken
                | SyntaxKind::LessThanLessThanToken
                | SyntaxKind::GreaterThanGreaterThanToken
                | SyntaxKind::GreaterThanGreaterThanGreaterThanToken
                | SyntaxKind::AmpersandToken
                | SyntaxKind::BarToken
                | SyntaxKind::CaretToken => true,
                SyntaxKind::PlusToken => self.numeric(node.left()) && self.numeric(node.right()),
                SyntaxKind::EqualsToken | SyntaxKind::CommaToken => self.numeric(node.right()),
                _ => false,
            },
            SyntaxKind::ConditionalExpression => {
                self.numeric(node.when_true()) && self.numeric(node.when_false())
            }
            SyntaxKind::Identifier => self.numeric_binding(node),
            _ => false,
        }
    }

    /// A variable of this module or function with a numeric initializer
    /// whose every write is numeric (`++`, `--`, a compound assignment other
    /// than `||=`, `&&=` and `??=` with a numeric operand, or `=` with a
    /// numeric right side).
    fn numeric_binding(&mut self, identifier: Node) -> bool {
        let Some(symbol) = self.symbol(identifier) else {
            return false;
        };
        if let Some(&known) = self.numeric_bindings.get(&symbol) {
            return known;
        }
        // A binding that depends on itself is numeric when its other
        // definitions are.
        if !self.numeric_active.insert(symbol) {
            return true;
        }
        let proven = self.numeric_binding_proof(symbol);
        self.numeric_active.remove(&symbol);
        if self.numeric_active.is_empty() {
            self.numeric_bindings.insert(symbol, proven);
        }
        proven
    }

    fn numeric_binding_proof(&mut self, symbol: SymbolId) -> bool {
        let declarations = self.ctx.checker.sym(symbol).declarations.to_vec();
        let [declaration] = declarations.as_slice() else {
            return false;
        };
        if declaration.kind() != SyntaxKind::VariableDeclaration
            || declaration.name().kind() != SyntaxKind::Identifier
            || declaration.initializer().is_nil()
            || !self.internal(symbol)
            || !self.numeric(declaration.initializer())
        {
            return false;
        }
        let refs = self.ctx.refs(symbol).to_vec();
        refs.iter()
            .filter(|r| r.kind == RefKind::Write)
            .all(|r| self.numeric_write(r.node))
    }

    fn numeric_write(&mut self, node: Node) -> bool {
        let outer = walk_up_outer(node);
        let parent = outer.parent();
        match parent.kind() {
            SyntaxKind::PrefixUnaryExpression | SyntaxKind::PostfixUnaryExpression => {
                matches!(
                    parent.operator(),
                    SyntaxKind::PlusPlusToken | SyntaxKind::MinusMinusToken
                )
            }
            SyntaxKind::BinaryExpression if parent.left() == outer => {
                match parent.operator_token().kind() {
                    SyntaxKind::EqualsToken | SyntaxKind::PlusEqualsToken => {
                        self.numeric(parent.right())
                    }
                    SyntaxKind::MinusEqualsToken
                    | SyntaxKind::AsteriskEqualsToken
                    | SyntaxKind::AsteriskAsteriskEqualsToken
                    | SyntaxKind::SlashEqualsToken
                    | SyntaxKind::PercentEqualsToken
                    | SyntaxKind::LessThanLessThanEqualsToken
                    | SyntaxKind::GreaterThanGreaterThanEqualsToken
                    | SyntaxKind::GreaterThanGreaterThanGreaterThanEqualsToken
                    | SyntaxKind::AmpersandEqualsToken
                    | SyntaxKind::BarEqualsToken
                    | SyntaxKind::CaretEqualsToken => true,
                    _ => false,
                }
            }
            _ => false,
        }
    }

    // ------------------------------------------------------------------
    // Accesses
    // ------------------------------------------------------------------

    /// Classifies the accesses whose receivers hold arrays.
    pub(super) fn array_findings(&self) -> ArrayFindings {
        let graph = &self.graph;
        let mut findings = ArrayFindings {
            mixed: Vec::new(),
            unmodeled: Vec::new(),
            names: HashMap::new(),
        };
        for access in &self.accesses {
            let receiver = &graph.cells[access.receiver as usize];
            let members: Vec<usize> = receiver
                .values
                .iter()
                .filter(|&&v| graph.is_object(v))
                .map(|&v| v as usize)
                .collect();
            let arrays: Vec<usize> = members
                .iter()
                .copied()
                .filter(|&v| self.is_array(v))
                .collect();
            if arrays.is_empty() {
                continue;
            }
            if arrays.len() != members.len() {
                findings.mixed.extend(members.iter().copied());
            }
            let modeled = match (&access.key, access.usage) {
                // Reading `length` yields a number; every write truncates or
                // extends the array, so it is an escape boundary.
                (super::AccessKey::Static { name, .. }, Usage::Data) => {
                    *name == "length" && !access.write
                }
                (super::AccessKey::Static { name, .. }, Usage::Call) => {
                    ArrayMethod::from_name(name)
                        .is_some_and(|method| self.options.arrays.enabled(method))
                }
                (super::AccessKey::Index(proof), Usage::Data) => graph.index_proven(*proof),
                _ => false,
            };
            if !modeled {
                let name = match &access.key {
                    super::AccessKey::Static { name, .. } => (*name).to_owned(),
                    super::AccessKey::Index(_) => "<index>".to_owned(),
                    super::AccessKey::Computed(_) => "<computed>".to_owned(),
                    super::AccessKey::Dynamic => "<dynamic>".to_owned(),
                };
                *findings.names.entry(name).or_default() += 1;
                findings.unmodeled.extend(arrays);
            }
        }
        findings
    }
}
