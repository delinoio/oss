// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! property-shortening, property-disambiguation and property-ambiguation.
//! Object regions come from the closed-region flow analysis; allocation is
//! global and runs once after structural rounds converge.
use std::collections::HashMap;

use anyhow::Result;
use ts_goport::prelude::*;

use super::super::{analysis::Ctx, flow, span};
use crate::{
    compiler::collect,
    optimizer::{self, Object, Preservation, Property, Strategy},
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Location, Plan, PropertyStats},
};

pub(in crate::compiler) fn plan(ctx: &mut Ctx, enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let mut objects = Vec::new();
    for index in 0..ctx.files.len() {
        crate::app::check_cancelled()?;
        if ctx.files[index].js {
            continue;
        }
        let file = ctx.files[index].node;
        objects.extend(analyze_file(file, ctx.checker)?);
    }
    let regions = objects.len();
    let strategy = Strategy {
        disambiguate: enabled.contains(PassId::PropertyDisambiguation),
        ambiguate: enabled.contains(PassId::PropertyAmbiguation),
    };
    let (allocations, preserved) = optimizer::allocate(&mut objects, strategy);
    out.property_stats = Some(PropertyStats {
        object_regions: regions,
        properties: preserved.len() + allocations.iter().map(|a| a.mappings.len()).sum::<usize>(),
    });
    out.mappings.extend(preserved);
    for allocation in allocations {
        let mut candidate = Candidate::new(
            PassId::PropertyShortening,
            allocation.key,
            Location {
                file: allocation.file,
                offset: allocation.start,
            },
        );
        // Group separation and name reuse are part of the bundle only when
        // their passes shaped the allocation.
        if strategy.disambiguate {
            candidate.uses.push(PassId::PropertyDisambiguation);
        }
        if strategy.ambiguate {
            candidate.uses.push(PassId::PropertyAmbiguation);
        }
        candidate.edits = allocation
            .edits
            .into_iter()
            .map(|(file, edit)| FileEdit { file, edit })
            .collect();
        candidate.mappings = allocation.mappings;
        out.candidates.push(candidate);
    }
    Ok(())
}

fn analyze_file(file: Node, checker: &mut Checker) -> Result<Vec<Object>> {
    let source = source_file_text(file).to_string();
    let name = source_file_info(file).file_name.clone();
    let mut nodes = Vec::new();
    collect(file, &mut nodes);
    let mut objects = Vec::<Object>::new();
    let mut allocations = HashMap::new();
    for &node in &nodes {
        if node.kind() != SyntaxKind::ObjectLiteralExpression {
            continue;
        }
        let declaration = node.parent();
        let local = declaration.kind() == SyntaxKind::VariableDeclaration
            && declaration.initializer() == node
            && declaration.name().kind() == SyntaxKind::Identifier;
        let mut object = Object {
            file: name.clone(),
            start: node.pos() as usize,
            properties: Vec::new(),
            preservation: None,
        };
        if !is_external_module(file) {
            object.preservation = Some(Preservation::Exported);
        } else if local && declaration.type_().is_some() {
            object.preservation = Some(Preservation::TypeConstraint);
        }
        for property in node.properties().iter() {
            let property_name = property.name();
            if !matches!(
                property.kind(),
                SyntaxKind::PropertyAssignment | SyntaxKind::ShorthandPropertyAssignment
            ) || !matches!(
                property_name.kind(),
                SyntaxKind::Identifier | SyntaxKind::StringLiteral
            ) {
                object.preservation = Some(Preservation::ExternalOrUnsupportedShape);
                continue;
            }
            let mut value_nodes = Vec::new();
            if property.kind() == SyntaxKind::PropertyAssignment {
                collect(property.initializer(), &mut value_nodes);
            }
            if value_nodes.iter().any(|n| {
                matches!(
                    n.kind(),
                    SyntaxKind::FunctionExpression
                        | SyntaxKind::ArrowFunction
                        | SyntaxKind::ClassExpression
                )
            }) {
                // Renaming an inferred function/class name changes .name.
                object.preservation = Some(Preservation::ObservableOperation);
            }
            let key = property_name.text().to_string();
            if ["__proto__", "constructor", "prototype", "then", "toJSON"].contains(&key.as_str()) {
                object.preservation = Some(Preservation::SpecialProperty);
            }
            let occurrence = span(
                property_name,
                &source,
                property.kind() == SyntaxKind::ShorthandPropertyAssignment,
                property_name.kind() == SyntaxKind::StringLiteral,
            );
            if let Some(existing) = object.properties.iter_mut().find(|p| p.name == key) {
                existing.occurrences.push(occurrence);
            } else {
                object.properties.push(Property {
                    name: key,
                    occurrences: vec![occurrence],
                });
            }
        }
        if local {
            // Type evidence is necessary, but never sufficient: actual
            // references below must also stay within this fresh
            // object's closed region.
            let ty = checker.get_type_at_location(declaration.name());
            let typed_properties = checker.get_properties_of_type(ty);
            if typed_properties.len() != object.properties.len() {
                object.preservation = Some(Preservation::TypeConstraint);
            }
        }
        allocations.insert(node, objects.len());
        objects.push(object);
    }
    flow::analyze(&nodes, checker, &source, objects, allocations)
}
