// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Plain transformation-planning data. Planners in the compiler adapter
//! produce candidates; the pipeline decides which ones become accepted state.
//! Nothing here holds compiler-owned identities.
use serde::Serialize;

use crate::{optimizer::Mapping, passes::PassId};

/// Replacement of `source[start..end]`. An empty range is an insertion and an
/// empty replacement is a deletion.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Edit {
    pub start: usize,
    pub end: usize,
    pub replacement: String,
}

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct FileEdit {
    pub file: String,
    pub edit: Edit,
}

impl FileEdit {
    pub fn replace(file: &str, start: usize, end: usize, replacement: impl Into<String>) -> Self {
        Self {
            file: file.to_owned(),
            edit: Edit {
                start,
                end,
                replacement: replacement.into(),
            },
        }
    }

    pub fn delete(file: &str, start: usize, end: usize) -> Self {
        Self::replace(file, start, end, "")
    }

    pub fn insert(file: &str, at: usize, text: impl Into<String>) -> Self {
        Self::replace(file, at, at, text)
    }
}

/// A byte offset in a module's current text. The pipeline maps it back to the
/// original source before reporting.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Location {
    pub file: String,
    pub offset: usize,
}

/// Stable reason codes for skipped or preserved transformations.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Reason {
    /// The complete bundle did not strictly reduce emitted ESM bytes.
    NoSizeBenefit,
    /// The bundle needs a transformation from a disabled pass.
    RequiresDisabledPass,
    /// The transformed program no longer type-checked; the bundle was rolled
    /// back.
    RecheckFailed,
    /// A public entry export or another external contract observes it.
    PublicExport,
    /// External declarations, JavaScript, unknown calls or values reach it.
    ExternalBoundary,
    /// The value escapes the analyzed closed region.
    Escapes,
    /// A computed key or other unresolved lookup may observe it.
    DynamicAccess,
    /// Reflection, enumeration or serialization may observe it.
    Reflection,
    /// Other observable operations (calls through, deletion, `in`, ...).
    ObservableOperation,
    /// A function or class `.name` may be observed.
    ObservableName,
    /// A function `.length` or argument object may be observed.
    ObservableArity,
    /// Object or function identity may be observed.
    ObservableIdentity,
    /// Getters, setters or property descriptors are involved.
    Accessor,
    /// Inheritance, overrides or `super` lookup are involved.
    Inheritance,
    /// Decorators or decorator metadata are involved.
    Decorator,
    /// Prototype mutation or `Object.setPrototypeOf`-style operations.
    PrototypeMutation,
    /// The value or binding is mutated.
    Mutation,
    /// Removing or moving the code would change effects or exceptions.
    SideEffects,
    /// The value is not proven constant at runtime.
    NotConstant,
    /// A type annotation constrains the shape; it is not runtime evidence.
    TypeConstraint,
    /// Special protocol properties such as `then` or `toJSON`.
    SpecialProperty,
    /// The construct is outside the supported proof.
    UnsupportedSyntax,
    /// Only type-level evidence exists; no runtime provenance.
    UnprovenRuntimeFact,
    /// The declaration is still referenced.
    Referenced,
    /// A call target cannot be resolved to a single known function.
    UnresolvedCall,
    /// Recursion prevents the transformation.
    Recursion,
    /// Declaration merging or ambient declarations are involved.
    DeclarationMerging,
    /// Module initialization effects must be preserved.
    ModuleInitialization,
    /// A built-in may be replaced or overridden.
    IntrinsicMayBeReplaced,
    /// Iteration may call a user-defined iterator.
    CustomIterator,
    /// Array holes may be observed.
    SparseArray,
    /// Bundle mode: a boundary chunk can import the chunk, so it keeps its
    /// name and content to leave one instance of the module.
    BoundaryReachable,
}

/// A transformation bundle: the edits of one pass plus any required cleanup
/// from other passes. The pipeline accepts or rejects it as a whole.
#[derive(Clone, Debug)]
pub struct Candidate {
    pub pass: PassId,
    /// Other passes whose transformations this bundle includes as cleanup.
    /// Every listed pass must be enabled.
    pub uses: Vec<PassId>,
    /// Stable semantic identity, independent of byte offsets.
    pub key: String,
    pub location: Location,
    pub edits: Vec<FileEdit>,
    /// Property mappings described by this bundle (property passes only).
    pub mappings: Vec<Mapping>,
}

impl Candidate {
    pub fn new(pass: PassId, key: impl Into<String>, location: Location) -> Self {
        Self {
            pass,
            uses: Vec::new(),
            key: key.into(),
            location,
            edits: Vec::new(),
            mappings: Vec::new(),
        }
    }

    pub fn passes(&self) -> impl Iterator<Item = PassId> + '_ {
        std::iter::once(self.pass).chain(self.uses.iter().copied())
    }
}

#[derive(Clone, Debug)]
pub struct Skip {
    pub pass: PassId,
    pub key: String,
    pub location: Location,
    pub reason: Reason,
}

/// Property analysis statistics retained from report v0.
#[derive(Clone, Debug, Default)]
pub struct PropertyStats {
    pub object_regions: usize,
}

/// A value import declaration of a module. When a transformation removes the
/// last runtime use of its bindings, the gate either keeps the dependency
/// with a bare import inserted at `offset` or, if `droppable`, lets it go.
#[derive(Clone, Debug)]
pub struct ImportSite {
    pub specifier: String,
    /// Start of the first import declaration with this specifier.
    pub offset: usize,
    /// The target module and everything it imports initialize without
    /// observable effects, so skipping its evaluation is unobservable.
    pub droppable: bool,
}

#[derive(Clone, Debug, Default)]
pub struct Plan {
    /// Value import declarations per module.
    pub imports: std::collections::BTreeMap<String, Vec<ImportSite>>,
    pub candidates: Vec<Candidate>,
    pub skips: Vec<Skip>,
    /// Preserved property mappings (property phase only).
    pub mappings: Vec<Mapping>,
    pub property_stats: Option<PropertyStats>,
}

/// Which transformations a planning round may propose.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Phase {
    Structural,
    Property,
}
