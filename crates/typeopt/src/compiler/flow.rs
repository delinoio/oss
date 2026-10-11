// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Context-insensitive, module-local points-to analysis. Unknown call targets
//! never receive a trusted summary. Every graph edge only adds information.
use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet};

use super::*;

#[derive(Default, Clone)]
struct Values {
    objects: BTreeSet<usize>,
    unknown: bool,
}
#[derive(Default)]
struct Graph {
    values: Vec<Values>,
    edges: BTreeSet<(usize, usize)>,
}
impl Graph {
    fn cell(&mut self, unknown: bool) -> usize {
        let id = self.values.len();
        self.values.push(Values {
            unknown,
            ..Values::default()
        });
        id
    }

    fn solve(&mut self) -> Result<usize> {
        let mut rounds = 0;
        loop {
            crate::app::check_cancelled()?;
            rounds += 1;
            let mut changed = false;
            for &(from, to) in &self.edges {
                let source = self.values[from].clone();
                let target = &mut self.values[to];
                let before = (target.objects.len(), target.unknown);
                target.objects.extend(source.objects);
                target.unknown |= source.unknown;
                changed |= before != (target.objects.len(), target.unknown);
            }
            if !changed {
                return Ok(rounds);
            }
        }
    }
}
struct Function {
    return_type: Node,
    binding: Node,
    parameters: Vec<Node>,
    result: usize,
    closed: bool,
}
fn symbol(node: Node, checker: &mut Checker) -> SymbolId {
    if node.parent().kind() == SyntaxKind::ExportSpecifier {
        checker.get_export_specifier_local_target_symbol(node.parent())
    } else if node.parent().kind() == SyntaxKind::ShorthandPropertyAssignment {
        checker.get_shorthand_assignment_value_symbol(node.parent())
    } else {
        checker.get_symbol_at_location(node, false)
    }
}
fn function_node(node: Node) -> bool {
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
    while node.is_some() && !function_node(node) {
        node = node.parent();
    }
    node
}
fn parameter_type(node: Node) -> bool {
    let ty = node.type_();
    ty.is_nil()
        || matches!(
            ty.kind(),
            SyntaxKind::AnyKeyword
                | SyntaxKind::UnknownKeyword
                | SyntaxKind::NumberKeyword
                | SyntaxKind::StringKeyword
                | SyntaxKind::BooleanKeyword
                | SyntaxKind::VoidKeyword
        )
        || (ty.kind() == SyntaxKind::TypeLiteral
            && ty.members().iter().all(|member| {
                member.kind() == SyntaxKind::PropertySignature
                    && matches!(
                        member.name().kind(),
                        SyntaxKind::Identifier | SyntaxKind::StringLiteral
                    )
                    && (member.type_().is_nil()
                        || !matches!(
                            member.type_().kind(),
                            SyntaxKind::FunctionType | SyntaxKind::TypeQuery
                        ))
            }))
}
struct Analysis<'a> {
    checker: &'a mut Checker,
    graph: Graph,
    bindings: HashMap<SymbolId, usize>,
    expressions: HashMap<Node, usize>,
    functions: Vec<Function>,
    targets: HashMap<SymbolId, usize>,
    owners: HashMap<Node, usize>,
}
impl Analysis<'_> {
    fn target(&mut self, call: Node) -> Option<usize> {
        if call.kind() != SyntaxKind::CallExpression
            || call.expression().kind() != SyntaxKind::Identifier
        {
            return None;
        }
        let key = symbol(call.expression(), self.checker);
        self.targets
            .get(&key)
            .copied()
            .filter(|&i| self.functions[i].closed)
    }

    fn expression(&mut self, node: Node) -> usize {
        if let Some(&cell) = self.expressions.get(&node) {
            return cell;
        }
        if node.kind() == SyntaxKind::Identifier
            && let Some(&cell) = self.bindings.get(&symbol(node, self.checker))
        {
            return cell;
        }
        let cell = self.graph.cell(false);
        self.expressions.insert(node, cell);
        match node.kind() {
            SyntaxKind::ParenthesizedExpression => {
                let source = self.expression(node.expression());
                self.graph.edges.insert((source, cell));
            }
            SyntaxKind::ConditionalExpression => {
                for value in [node.when_true(), node.when_false()] {
                    let source = self.expression(value);
                    self.graph.edges.insert((source, cell));
                }
            }
            SyntaxKind::CallExpression => {
                if let Some(index) = self.target(node) {
                    self.graph
                        .edges
                        .insert((self.functions[index].result, cell));
                } else {
                    self.graph.values[cell].unknown = true;
                }
            }
            SyntaxKind::BinaryExpression
                if node.operator_token().kind() == SyntaxKind::EqualsToken
                    && node.left().kind() == SyntaxKind::Identifier =>
            {
                let source = self.expression(node.right());
                self.graph.edges.insert((source, cell));
            }
            _ => self.graph.values[cell].unknown = true,
        }
        cell
    }
}

pub(super) fn analyze(
    nodes: &[Node],
    checker: &mut Checker,
    source: &str,
    mut objects: Vec<Object>,
    allocations: HashMap<Node, usize>,
) -> Result<Vec<Object>> {
    let mut a = Analysis {
        checker,
        graph: Graph::default(),
        bindings: HashMap::new(),
        expressions: HashMap::new(),
        functions: Vec::new(),
        targets: HashMap::new(),
        owners: HashMap::new(),
    };
    let mut declarations = HashSet::new();
    for &node in nodes {
        if matches!(
            node.kind(),
            SyntaxKind::VariableDeclaration | SyntaxKind::Parameter
        ) && node.name().kind() == SyntaxKind::Identifier
        {
            let key = symbol(node.name(), a.checker);
            let cell = a.graph.cell(false);
            a.bindings.entry(key).or_insert(cell);
            declarations.insert(node.name());
        }
        let binding = match node.kind() {
            SyntaxKind::FunctionDeclaration if node.name().is_some() && node.body().is_some() => {
                node.name()
            }
            SyntaxKind::ArrowFunction
                if node.parent().kind() == SyntaxKind::VariableDeclaration
                    && node.parent().initializer() == node
                    && node.parent().name().kind() == SyntaxKind::Identifier
                    && node.parent().parent().flags().intersects(NodeFlags::CONST)
                    && node.parent().type_().is_nil() =>
            {
                node.parent().name()
            }
            _ => continue,
        };
        let mut body_nodes = Vec::new();
        collect(node.body(), &mut body_nodes);
        let closed = !get_combined_modifier_flags(node)
            .intersects(ModifierFlags::EXPORT | ModifierFlags::ASYNC)
            && !get_combined_modifier_flags(binding.parent()).intersects(ModifierFlags::EXPORT)
            && node.asterisk_token().is_nil()
            && node.type_parameters().is_empty()
            && parameter_type(node)
            && node.parameters().iter().all(|p| {
                p.name().kind() == SyntaxKind::Identifier
                    && p.initializer().is_nil()
                    && p.dot_dot_dot_token().is_nil()
                    && parameter_type(p)
            })
            && !body_nodes.iter().any(|n| {
                matches!(
                    n.kind(),
                    SyntaxKind::ThisKeyword | SyntaxKind::SuperKeyword | SyntaxKind::MetaProperty
                ) || (n.kind() == SyntaxKind::Identifier && n.text() == "arguments")
            });
        let index = a.functions.len();
        a.targets.insert(symbol(binding, a.checker), index);
        a.owners.insert(node, index);
        let body = node.body();
        // A fallthrough return contributes an unknown value, never a fresh
        // object.
        let fallthrough = body.kind() == SyntaxKind::Block
            && body
                .statements()
                .iter()
                .last()
                .is_none_or(|last| last.kind() != SyntaxKind::ReturnStatement);
        let result = a.graph.cell(fallthrough);
        a.functions.push(Function {
            return_type: node.type_(),
            binding,
            parameters: node.parameters().iter().collect(),
            result,
            closed,
        });
    }
    // A summary is closed only if every reference names a direct call with the
    // exact fixed arity. Reassignment, exports, aliases and callbacks
    // invalidate it.
    for &node in nodes {
        if node.kind() != SyntaxKind::Identifier {
            continue;
        }
        let key = symbol(node, a.checker);
        let Some(&index) = a.targets.get(&key) else {
            continue;
        };
        let f = &mut a.functions[index];
        if node == f.binding {
            continue;
        }
        let parent = node.parent();
        if parent.kind() != SyntaxKind::CallExpression
            || parent.expression() != node
            || parent.arguments().len() != f.parameters.len()
            || parent
                .arguments()
                .iter()
                .any(|arg| arg.kind() == SyntaxKind::SpreadElement)
        {
            f.closed = false;
        }
    }
    for (&node, &index) in &allocations {
        let cell = a.graph.cell(false);
        a.graph.values[cell].objects.insert(index);
        a.expressions.insert(node, cell);
    }
    for &node in nodes {
        match node.kind() {
            SyntaxKind::VariableDeclaration if node.name().kind() == SyntaxKind::Identifier => {
                let target = a.bindings[&symbol(node.name(), a.checker)];
                if node.initializer().is_some() {
                    let from = a.expression(node.initializer());
                    a.graph.edges.insert((from, target));
                } else {
                    a.graph.values[target].unknown = true;
                }
            }
            SyntaxKind::Parameter if node.name().kind() == SyntaxKind::Identifier => {
                let target = a.bindings[&symbol(node.name(), a.checker)];
                if a.owners
                    .get(&node.parent())
                    .is_none_or(|&i| !a.functions[i].closed)
                {
                    a.graph.values[target].unknown = true;
                }
            }
            SyntaxKind::BinaryExpression
                if node.operator_token().kind() == SyntaxKind::EqualsToken
                    && node.left().kind() == SyntaxKind::Identifier =>
            {
                let key = symbol(node.left(), a.checker);
                if let Some(&to) = a.bindings.get(&key) {
                    let from = a.expression(node.right());
                    a.graph.edges.insert((from, to));
                }
            }
            SyntaxKind::CallExpression => {
                if let Some(i) = a.target(node) {
                    let parameters = a.functions[i].parameters.clone();
                    for (arg, parameter) in node.arguments().iter().zip(parameters) {
                        let from = a.expression(arg);
                        let to = a.bindings[&symbol(parameter.name(), a.checker)];
                        a.graph.edges.insert((from, to));
                    }
                }
                a.expression(node);
            }
            SyntaxKind::ReturnStatement => {
                if let Some(&i) = a.owners.get(&owner(node))
                    && a.functions[i].closed
                    && node.expression().is_some()
                {
                    let from = a.expression(node.expression());
                    a.graph.edges.insert((from, a.functions[i].result));
                }
            }
            SyntaxKind::ArrowFunction if node.body().kind() != SyntaxKind::Block => {
                if let Some(&i) = a.owners.get(&node)
                    && a.functions[i].closed
                {
                    let from = a.expression(node.body());
                    a.graph.edges.insert((from, a.functions[i].result));
                }
            }
            _ => {}
        }
    }
    // Materialize value cells before solving so transparent expressions cannot
    // introduce new graph edges after the fixed point.
    let mut references = Vec::new();
    for &node in nodes {
        let tracked = allocations.contains_key(&node)
            || (node.kind() == SyntaxKind::Identifier
                && !declarations.contains(&node)
                && a.bindings.contains_key(&symbol(node, a.checker)))
            || matches!(
                node.kind(),
                SyntaxKind::CallExpression
                    | SyntaxKind::ParenthesizedExpression
                    | SyntaxKind::ConditionalExpression
                    | SyntaxKind::BinaryExpression
            );
        if tracked {
            let cell = a.expression(node);
            references.push((node, cell));
        }
    }
    let rounds = a.graph.solve()?;
    tracing::debug!(
        functions = a.functions.iter().filter(|f| f.closed).count(),
        cells = a.graph.values.len(),
        edges = a.graph.edges.len(),
        rounds,
        "interprocedural flow reached fixed point"
    );
    // Joining whole allocation regions is conservative: all keys in the union
    // interfere, including preserved keys and keys absent from another shape.
    let mut parents: Vec<usize> = (0..objects.len()).collect();
    fn root(parents: &[usize], mut i: usize) -> usize {
        while parents[i] != i {
            i = parents[i];
        }
        i
    }
    for values in &a.graph.values {
        if let Some(&first) = values.objects.first() {
            for &index in &values.objects {
                let r = root(&parents, first);
                let s = root(&parents, index);
                parents[r.max(s)] = r.min(s);
                if values.unknown {
                    objects[index].preservation = Some(Preservation::Escapes);
                }
            }
        }
    }
    let preserve = |objects: &mut [Object], values: &Values, reason: Preservation| {
        for &index in &values.objects {
            objects[index].preservation = Some(reason.clone());
        }
    };
    // Type-literal keys are uses of the same runtime property group. Shared
    // named declarations are rejected during summary construction instead.
    for f in &a.functions {
        if !f.closed {
            continue;
        }
        if f.return_type.is_some() && f.return_type.kind() == SyntaxKind::TypeLiteral {
            for member in f.return_type.members().iter() {
                access(
                    &mut objects,
                    &a.graph.values[f.result],
                    member.name(),
                    source,
                    false,
                    true,
                );
            }
        }
        for &parameter in &f.parameters {
            if parameter.type_().is_some() && parameter.type_().kind() == SyntaxKind::TypeLiteral {
                let cell = a.bindings[&symbol(parameter.name(), a.checker)];
                for member in parameter.type_().members().iter() {
                    access(
                        &mut objects,
                        &a.graph.values[cell],
                        member.name(),
                        source,
                        false,
                        true,
                    );
                }
            }
        }
    }
    for (node, cell) in references {
        let values = &a.graph.values[cell].clone();
        if values.objects.is_empty() {
            continue;
        }
        let parent = node.parent();
        let accepted = match parent.kind() {
            SyntaxKind::VariableDeclaration
                if parent.initializer() == node
                    && parent.name().kind() == SyntaxKind::Identifier =>
            {
                parent.type_().is_nil()
                    && !get_combined_modifier_flags(parent).intersects(ModifierFlags::EXPORT)
            }
            SyntaxKind::BinaryExpression
                if parent.operator_token().kind() == SyntaxKind::EqualsToken =>
            {
                parent.left().kind() == SyntaxKind::Identifier
                    && a.bindings.contains_key(&symbol(parent.left(), a.checker))
                    && (parent.left() == node || parent.right() == node)
            }
            SyntaxKind::CallExpression if parent.expression() != node => a.target(parent).is_some(),
            SyntaxKind::ReturnStatement => a
                .owners
                .get(&owner(parent))
                .is_some_and(|&i| a.functions[i].closed),
            SyntaxKind::ArrowFunction if parent.body() == node => a
                .owners
                .get(&parent)
                .is_some_and(|&i| a.functions[i].closed),
            SyntaxKind::ParenthesizedExpression => true,
            SyntaxKind::ConditionalExpression => {
                parent.when_true() == node || parent.when_false() == node
            }
            SyntaxKind::ExpressionStatement => {
                node.kind() == SyntaxKind::CallExpression
                    || node.kind() == SyntaxKind::BinaryExpression
            }
            SyntaxKind::PropertyAccessExpression | SyntaxKind::ElementAccessExpression
                if parent.expression() == node =>
            {
                let key = if parent.kind() == SyntaxKind::PropertyAccessExpression {
                    parent.name()
                } else {
                    parent.argument_expression()
                };
                if (parent.kind() == SyntaxKind::ElementAccessExpression
                    && key.kind() != SyntaxKind::StringLiteral)
                    || !matches!(
                        key.kind(),
                        SyntaxKind::Identifier | SyntaxKind::StringLiteral
                    )
                {
                    preserve(&mut objects, values, Preservation::DynamicAccess);
                } else {
                    let outer = parent.parent();
                    if matches!(
                        outer.kind(),
                        SyntaxKind::CallExpression
                            | SyntaxKind::TaggedTemplateExpression
                            | SyntaxKind::DeleteExpression
                    ) && outer.expression() == parent
                    {
                        preserve(&mut objects, values, Preservation::ObservableOperation);
                    }
                    access(&mut objects, values, key, source, false, false);
                }
                true
            }
            SyntaxKind::VariableDeclaration
                if parent.initializer() == node
                    && parent.name().kind() == SyntaxKind::ObjectBindingPattern
                    && parent.type_().is_nil() =>
            {
                for element in parent.name().elements().iter() {
                    let key = if element.property_name().is_some() {
                        element.property_name()
                    } else {
                        element.name()
                    };
                    if element.dot_dot_dot_token().is_some()
                        || element.name().kind() != SyntaxKind::Identifier
                        || !matches!(
                            key.kind(),
                            SyntaxKind::Identifier | SyntaxKind::StringLiteral
                        )
                    {
                        preserve(
                            &mut objects,
                            values,
                            Preservation::ExternalOrUnsupportedShape,
                        );
                    } else {
                        access(
                            &mut objects,
                            values,
                            key,
                            source,
                            element.property_name().is_nil(),
                            false,
                        );
                    }
                }
                true
            }
            _ => false,
        };
        if !accepted {
            preserve(&mut objects, values, Preservation::Escapes);
        }
    }
    let mut merged = BTreeMap::<usize, Object>::new();
    for (index, object) in objects.into_iter().enumerate() {
        let id = root(&parents, index);
        if let Some(target) = merged.get_mut(&id) {
            target.start = target.start.min(object.start);
            if object.preservation.is_some() {
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
            property.occurrences.sort_by_key(|o| (o.start, o.end));
            property.occurrences.dedup_by_key(|o| (o.start, o.end));
        }
    }
    Ok(merged.into_values().collect())
}
fn access(
    objects: &mut [Object],
    values: &Values,
    key: Node,
    source: &str,
    shorthand: bool,
    erased: bool,
) {
    for &index in &values.objects {
        if let Some(property) = objects[index]
            .properties
            .iter_mut()
            .find(|p| p.name == key.text())
        {
            let mut occurrence = span(
                key,
                source,
                shorthand,
                key.kind() == SyntaxKind::StringLiteral,
            );
            occurrence.erased = erased;
            property.occurrences.push(occurrence);
        } else {
            objects[index].preservation = Some(Preservation::ObservableOperation);
        }
    }
}
