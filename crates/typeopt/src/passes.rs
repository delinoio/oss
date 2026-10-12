// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Stable optimization pass inventory. The identifiers are CLI and report
//! contracts; never rename one without a report schema change.
use std::collections::BTreeSet;

use serde::Serialize;

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, clap::ValueEnum)]
#[serde(rename_all = "kebab-case")]
#[value(rename_all = "kebab-case")]
pub enum PassId {
    PropertyShortening,
    PropertyDisambiguation,
    PropertyAmbiguation,
    FiniteKeyRewriting,
    UnusedMethodRemoval,
    UnusedFieldRemoval,
    ConstantPropertyInlining,
    MethodDevirtualization,
    CallInlining,
    FunctionSpecialization,
    SignatureReduction,
    TypeDirectedControlFlow,
    PrimitiveOperationSimplification,
    EnumRuntimeReduction,
    DiscriminantCompaction,
    AggregateElimination,
    IntrinsicSpecialization,
    ResidualDependencyRemoval,
}

impl PassId {
    /// Every pass in inventory order.
    pub const ALL: [PassId; 18] = [
        PassId::PropertyShortening,
        PassId::PropertyDisambiguation,
        PassId::PropertyAmbiguation,
        PassId::FiniteKeyRewriting,
        PassId::UnusedMethodRemoval,
        PassId::UnusedFieldRemoval,
        PassId::ConstantPropertyInlining,
        PassId::MethodDevirtualization,
        PassId::CallInlining,
        PassId::FunctionSpecialization,
        PassId::SignatureReduction,
        PassId::TypeDirectedControlFlow,
        PassId::PrimitiveOperationSimplification,
        PassId::EnumRuntimeReduction,
        PassId::DiscriminantCompaction,
        PassId::AggregateElimination,
        PassId::IntrinsicSpecialization,
        PassId::ResidualDependencyRemoval,
    ];
    /// Passes available in bundle mode (stage 2). Bundled JavaScript is not
    /// type-checked, so inferred types can be wrong, not only imprecise.
    /// Only passes whose proofs rest on syntax, references, points-to flows
    /// and runtime facts are available. Passes that rely on static types to
    /// track where class instances or values flow (method and field removal,
    /// devirtualization, discriminant compaction, ...) are not.
    pub const BUNDLE: [PassId; 8] = [
        PassId::PropertyShortening,
        PassId::PropertyDisambiguation,
        PassId::PropertyAmbiguation,
        PassId::CallInlining,
        PassId::SignatureReduction,
        PassId::TypeDirectedControlFlow,
        PassId::PrimitiveOperationSimplification,
        PassId::ResidualDependencyRemoval,
    ];
    pub const PROPERTY: [PassId; 3] = [
        PassId::PropertyShortening,
        PassId::PropertyDisambiguation,
        PassId::PropertyAmbiguation,
    ];
    /// Deterministic order in which structural candidates are evaluated
    /// within one round. Value-propagating passes run before the passes that
    /// clean up the code they make unused. Property passes run in their own
    /// phase after structural rounds converge.
    pub const STRUCTURAL_ORDER: [PassId; 15] = [
        PassId::EnumRuntimeReduction,
        PassId::ConstantPropertyInlining,
        PassId::FiniteKeyRewriting,
        PassId::TypeDirectedControlFlow,
        PassId::PrimitiveOperationSimplification,
        PassId::IntrinsicSpecialization,
        PassId::SignatureReduction,
        PassId::FunctionSpecialization,
        PassId::MethodDevirtualization,
        PassId::CallInlining,
        PassId::AggregateElimination,
        PassId::DiscriminantCompaction,
        PassId::UnusedFieldRemoval,
        PassId::UnusedMethodRemoval,
        PassId::ResidualDependencyRemoval,
    ];

    pub fn as_str(self) -> &'static str {
        match self {
            PassId::PropertyShortening => "property-shortening",
            PassId::PropertyDisambiguation => "property-disambiguation",
            PassId::PropertyAmbiguation => "property-ambiguation",
            PassId::FiniteKeyRewriting => "finite-key-rewriting",
            PassId::UnusedMethodRemoval => "unused-method-removal",
            PassId::UnusedFieldRemoval => "unused-field-removal",
            PassId::ConstantPropertyInlining => "constant-property-inlining",
            PassId::MethodDevirtualization => "method-devirtualization",
            PassId::CallInlining => "call-inlining",
            PassId::FunctionSpecialization => "function-specialization",
            PassId::SignatureReduction => "signature-reduction",
            PassId::TypeDirectedControlFlow => "type-directed-control-flow",
            PassId::PrimitiveOperationSimplification => "primitive-operation-simplification",
            PassId::EnumRuntimeReduction => "enum-runtime-reduction",
            PassId::DiscriminantCompaction => "discriminant-compaction",
            PassId::AggregateElimination => "aggregate-elimination",
            PassId::IntrinsicSpecialization => "intrinsic-specialization",
            PassId::ResidualDependencyRemoval => "residual-dependency-removal",
        }
    }

    /// Position used to order candidates deterministically.
    pub fn rank(self) -> usize {
        Self::STRUCTURAL_ORDER
            .iter()
            .chain(Self::PROPERTY.iter())
            .position(|&p| p == self)
            .expect("every pass has a rank")
    }
}

impl std::fmt::Display for PassId {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

/// The enabled pass set. Analyses may still run for a disabled pass, but a
/// disabled transformation is never applied, including as another pass's
/// cleanup.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PassSet {
    enabled: BTreeSet<PassId>,
}

impl PassSet {
    pub fn all() -> Self {
        Self {
            enabled: PassId::ALL.into_iter().collect(),
        }
    }

    pub fn without(disabled: &[PassId]) -> Self {
        let mut set = Self::all();
        for pass in disabled {
            set.enabled.remove(pass);
        }
        set
    }

    /// Bundle-mode passes without `disabled`.
    pub fn bundle_without(disabled: &[PassId]) -> Self {
        let mut set = Self {
            enabled: PassId::BUNDLE.into_iter().collect(),
        };
        for pass in disabled {
            set.enabled.remove(pass);
        }
        set
    }

    pub fn contains(&self, pass: PassId) -> bool {
        self.enabled.contains(&pass)
    }

    pub fn enabled(&self) -> Vec<PassId> {
        self.enabled.iter().copied().collect()
    }

    pub fn disabled(&self) -> Vec<PassId> {
        PassId::ALL
            .into_iter()
            .filter(|p| !self.enabled.contains(p))
            .collect()
    }

    pub fn any_property(&self) -> bool {
        PassId::PROPERTY.iter().any(|&p| self.contains(p))
    }
}

#[cfg(test)]
mod tests {
    use clap::ValueEnum;

    use super::*;

    #[test]
    fn identifiers_are_stable_and_round_trip() {
        for pass in PassId::ALL {
            let name = pass.as_str();
            assert_eq!(
                serde_json::to_value(pass).unwrap(),
                serde_json::Value::String(name.into())
            );
            assert_eq!(PassId::from_str(name, false).unwrap(), pass);
            assert_eq!(
                pass.to_possible_value().unwrap().get_name(),
                name,
                "clap and serde names must agree"
            );
        }
        assert!(PassId::from_str("unknown-pass", false).is_err());
        let ranks: BTreeSet<_> = PassId::ALL.iter().map(|p| p.rank()).collect();
        assert_eq!(ranks.len(), PassId::ALL.len());
    }

    #[test]
    fn disabled_passes_are_excluded() {
        let set = PassSet::without(&[PassId::CallInlining, PassId::CallInlining]);
        assert!(!set.contains(PassId::CallInlining));
        assert_eq!(set.disabled(), vec![PassId::CallInlining]);
        assert_eq!(set.enabled().len(), 17);
    }
}
