// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! property-shortening, property-disambiguation and property-ambiguation.
//!
//! Object regions come from the whole-program closed-region flow analysis
//! ([`flow`]): fresh object literals, their bindings, internal function
//! summaries across module boundaries, contained objects and finite
//! computed keys. Allocation is global and runs once after structural
//! rounds converge.
//!
//! Each accepted rename unit is attributed to the most specific pass that
//! shaped it:
//! - `property-ambiguation` when the unit reuses a name that another region
//!   also uses (impossible with globally unique names);
//! - `property-disambiguation` when the unit renames a name that another region
//!   also defines (impossible with name-based identities);
//! - `property-shortening` otherwise.
//!
//! Renaming itself is shortening's transformation, so the other two always
//! list it in `uses`, together with `finite-key-rewriting` when key
//! literals of finite computed accesses are renamed. Disabled passes never
//! shape an allocation: without disambiguation same-named properties form
//! one identity, and without ambiguation every new name is unique.
use anyhow::Result;

use super::super::{analysis::Ctx, flow};
use crate::{
    optimizer::{self, Strategy},
    passes::{PassId, PassSet},
    plan::{Candidate, FileEdit, Location, Plan, PropertyStats},
};

pub(in crate::compiler) fn plan(ctx: &mut Ctx, enabled: &PassSet, out: &mut Plan) -> Result<()> {
    let options = flow::Options {
        finite_keys: enabled.contains(PassId::FiniteKeyRewriting),
        arrays: flow::ArrayBuiltins::detect(ctx),
    };
    let mut objects = flow::analyze(ctx, options)?;
    let regions = objects.len();
    let strategy = Strategy {
        disambiguate: enabled.contains(PassId::PropertyDisambiguation),
        ambiguate: enabled.contains(PassId::PropertyAmbiguation),
    };
    let (allocations, preserved) = optimizer::allocate(&mut objects, strategy);
    out.property_stats = Some(PropertyStats {
        object_regions: regions,
    });
    out.mappings.extend(preserved);
    let mut counts = [0usize; 3];
    for allocation in allocations {
        let pass = if allocation.reused {
            PassId::PropertyAmbiguation
        } else if allocation.separated {
            PassId::PropertyDisambiguation
        } else {
            PassId::PropertyShortening
        };
        counts[match pass {
            PassId::PropertyAmbiguation => 2,
            PassId::PropertyDisambiguation => 1,
            _ => 0,
        }] += 1;
        let mut candidate = Candidate::new(
            pass,
            allocation.key,
            Location {
                file: allocation.file,
                offset: allocation.start,
            },
        );
        if pass != PassId::PropertyShortening {
            candidate.uses.push(PassId::PropertyShortening);
        }
        if allocation.separated && pass != PassId::PropertyDisambiguation {
            candidate.uses.push(PassId::PropertyDisambiguation);
        }
        if allocation.finite_keys {
            candidate.uses.push(PassId::FiniteKeyRewriting);
        }
        candidate.edits = allocation
            .edits
            .into_iter()
            .map(|(file, edit)| FileEdit { file, edit })
            .collect();
        candidate.mappings = allocation.mappings;
        out.candidates.push(candidate);
    }
    tracing::debug!(
        regions,
        shortening = counts[0],
        disambiguation = counts[1],
        ambiguation = counts[2],
        finite_keys = options.finite_keys,
        "property allocation planned"
    );
    Ok(())
}
