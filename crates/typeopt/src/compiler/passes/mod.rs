// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Pass planners. Each planner reads shared facts from [`Ctx`] and proposes
//! candidate bundles; the pipeline decides which ones are applied.
use std::time::Instant;

use anyhow::Result;

use super::analysis::Ctx;
use crate::{
    passes::{PassId, PassSet},
    plan::{Phase, Plan},
};

mod aggregate_elimination;
mod aggregates_support;
mod call_inlining;
mod calls_init;
mod calls_support;
mod constant_property_inlining;
mod constants_support;
mod deadcode_support;
mod discriminant_compaction;
mod enum_runtime_reduction;
mod finite_key_rewriting;
mod function_specialization;
mod intrinsic_specialization;
mod method_devirtualization;
mod primitive_operation_simplification;
mod property;
mod residual_dependency_removal;
pub(in crate::compiler) mod runtime_facts;
mod signature_reduction;
mod signature_support;
mod type_directed_control_flow;
mod unused_field_removal;
mod unused_method_removal;

type Planner = fn(&mut Ctx, &PassSet, &mut Plan) -> Result<()>;

fn planner(pass: PassId) -> Planner {
    match pass {
        PassId::EnumRuntimeReduction => enum_runtime_reduction::plan,
        PassId::ConstantPropertyInlining => constant_property_inlining::plan,
        PassId::FiniteKeyRewriting => finite_key_rewriting::plan,
        PassId::TypeDirectedControlFlow => type_directed_control_flow::plan,
        PassId::PrimitiveOperationSimplification => primitive_operation_simplification::plan,
        PassId::IntrinsicSpecialization => intrinsic_specialization::plan,
        PassId::SignatureReduction => signature_reduction::plan,
        PassId::FunctionSpecialization => function_specialization::plan,
        PassId::MethodDevirtualization => method_devirtualization::plan,
        PassId::CallInlining => call_inlining::plan,
        PassId::AggregateElimination => aggregate_elimination::plan,
        PassId::DiscriminantCompaction => discriminant_compaction::plan,
        PassId::UnusedFieldRemoval => unused_field_removal::plan,
        PassId::UnusedMethodRemoval => unused_method_removal::plan,
        PassId::ResidualDependencyRemoval => residual_dependency_removal::plan,
        PassId::PropertyShortening
        | PassId::PropertyDisambiguation
        | PassId::PropertyAmbiguation => property::plan,
    }
}

pub(super) fn plan(ctx: &mut Ctx, phase: Phase, enabled: &PassSet) -> Result<Plan> {
    let mut plan = Plan::default();
    let passes: &[PassId] = match phase {
        Phase::Structural => &PassId::STRUCTURAL_ORDER,
        // One planner allocates all three property passes together.
        Phase::Property => &[PassId::PropertyShortening],
    };
    for &pass in passes {
        // A disabled pass's own transformation never runs. Property
        // allocation is shared, so it runs when any property pass is on and
        // honors each disabled one inside the allocation strategy.
        let active = match phase {
            Phase::Structural => enabled.contains(pass),
            Phase::Property => enabled.contains(PassId::PropertyShortening),
        };
        if !active {
            continue;
        }
        crate::app::check_cancelled()?;
        let started = Instant::now();
        let (candidates, skips) = (plan.candidates.len(), plan.skips.len());
        planner(pass)(ctx, enabled, &mut plan)?;
        tracing::debug!(
            pass = %pass,
            candidates = plan.candidates.len() - candidates,
            skipped = plan.skips.len() - skips,
            elapsed_ms = started.elapsed().as_millis() as u64,
            "pass planned"
        );
    }
    Ok(plan)
}
