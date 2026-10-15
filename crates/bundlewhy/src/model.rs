//! Serde types that follow the proto3 JSON mapping of `bundlewhy.v1`.
//!
//! Field names are camelCase and enums serialize as their proto names, so the
//! UI parses these values with the generated protobuf-es schemas. Every field
//! has a default so omitted proto3 defaults deserialize.

use serde::{Deserialize, Serialize};

pub const SCHEMA_VERSION: u32 = 1;

macro_rules! proto_enum {
    ($name:ident { $($variant:ident = $wire:literal),+ $(,)? }) => {
        #[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, PartialOrd, Ord, Default, Serialize, Deserialize)]
        pub enum $name {
            #[default]
            $(
                #[serde(rename = $wire)]
                $variant,
            )+
        }
    };
}

proto_enum!(OptimizationCategory {
    Unspecified = "OPTIMIZATION_CATEGORY_UNSPECIFIED",
    TreeShaking = "OPTIMIZATION_CATEGORY_TREE_SHAKING",
    DeadCodeElimination = "OPTIMIZATION_CATEGORY_DEAD_CODE_ELIMINATION",
    Inlining = "OPTIMIZATION_CATEGORY_INLINING",
    ConstantFolding = "OPTIMIZATION_CATEGORY_CONSTANT_FOLDING",
    NameMangling = "OPTIMIZATION_CATEGORY_NAME_MANGLING",
    ModuleConcatenation = "OPTIMIZATION_CATEGORY_MODULE_CONCATENATION",
    ChunkSplitting = "OPTIMIZATION_CATEGORY_CHUNK_SPLITTING",
    DuplicateModules = "OPTIMIZATION_CATEGORY_DUPLICATE_MODULES",
});

impl OptimizationCategory {
    pub const ALL: [Self; 8] = [
        Self::TreeShaking,
        Self::DeadCodeElimination,
        Self::Inlining,
        Self::ConstantFolding,
        Self::NameMangling,
        Self::ModuleConcatenation,
        Self::ChunkSplitting,
        Self::DuplicateModules,
    ];

    pub const fn label(self) -> &'static str {
        match self {
            Self::Unspecified => "Unspecified",
            Self::TreeShaking => "Tree shaking",
            Self::DeadCodeElimination => "Dead-code elimination",
            Self::Inlining => "Inlining",
            Self::ConstantFolding => "Constant folding",
            Self::NameMangling => "Name mangling",
            Self::ModuleConcatenation => "Module concatenation",
            Self::ChunkSplitting => "Chunk splitting",
            Self::DuplicateModules => "Duplicate modules",
        }
    }
}

proto_enum!(EvidenceStatus {
    Unspecified = "EVIDENCE_STATUS_UNSPECIFIED",
    Confirmed = "EVIDENCE_STATUS_CONFIRMED",
    Inferred = "EVIDENCE_STATUS_INFERRED",
    Unavailable = "EVIDENCE_STATUS_UNAVAILABLE",
});

proto_enum!(ConditionOutcome {
    Unspecified = "CONDITION_OUTCOME_UNSPECIFIED",
    Met = "CONDITION_OUTCOME_MET",
    Unmet = "CONDITION_OUTCOME_UNMET",
    Unknown = "CONDITION_OUTCOME_UNKNOWN",
});

proto_enum!(RunStatus {
    Unspecified = "RUN_STATUS_UNSPECIFIED",
    Running = "RUN_STATUS_RUNNING",
    Complete = "RUN_STATUS_COMPLETE",
    Partial = "RUN_STATUS_PARTIAL",
    Cancelled = "RUN_STATUS_CANCELLED",
    Failed = "RUN_STATUS_FAILED",
});

proto_enum!(RunStage {
    Unspecified = "RUN_STAGE_UNSPECIFIED",
    Detecting = "RUN_STAGE_DETECTING",
    Building = "RUN_STAGE_BUILDING",
    Collecting = "RUN_STAGE_COLLECTING",
    Analyzing = "RUN_STAGE_ANALYZING",
    Measuring = "RUN_STAGE_MEASURING",
    Saving = "RUN_STAGE_SAVING",
    Finished = "RUN_STAGE_FINISHED",
});

impl RunStage {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Unspecified => "unspecified",
            Self::Detecting => "detecting",
            Self::Building => "building",
            Self::Collecting => "collecting",
            Self::Analyzing => "analyzing",
            Self::Measuring => "measuring",
            Self::Saving => "saving",
            Self::Finished => "finished",
        }
    }

    pub fn from_adapter(stage: &str) -> Self {
        match stage {
            "detecting" => Self::Detecting,
            "building" => Self::Building,
            "collecting" => Self::Collecting,
            _ => Self::Unspecified,
        }
    }
}

proto_enum!(Bundler {
    Unspecified = "BUNDLER_UNSPECIFIED",
    Vite = "BUNDLER_VITE",
    Rspack = "BUNDLER_RSPACK",
});

proto_enum!(EngineSupport {
    Unspecified = "ENGINE_SUPPORT_UNSPECIFIED",
    Supported = "ENGINE_SUPPORT_SUPPORTED",
    UnsupportedSelected = "ENGINE_SUPPORT_UNSUPPORTED_SELECTED",
});

proto_enum!(CoverageState {
    Unspecified = "COVERAGE_STATE_UNSPECIFIED",
    Covered = "COVERAGE_STATE_COVERED",
    NotApplicable = "COVERAGE_STATE_NOT_APPLICABLE",
    Unavailable = "COVERAGE_STATE_UNAVAILABLE",
    Partial = "COVERAGE_STATE_PARTIAL",
});

proto_enum!(LocationKind {
    Unspecified = "LOCATION_KIND_UNSPECIFIED",
    Original = "LOCATION_KIND_ORIGINAL",
    Generated = "LOCATION_KIND_GENERATED",
});

proto_enum!(SourceOrigin {
    Unspecified = "SOURCE_ORIGIN_UNSPECIFIED",
    Project = "SOURCE_ORIGIN_PROJECT",
    Dependency = "SOURCE_ORIGIN_DEPENDENCY",
    Virtual = "SOURCE_ORIGIN_VIRTUAL",
    Generated = "SOURCE_ORIGIN_GENERATED",
});

proto_enum!(SourceRevisionEvidence {
    Unspecified = "SOURCE_REVISION_EVIDENCE_UNSPECIFIED",
    BuildSnapshot = "SOURCE_REVISION_EVIDENCE_BUILD_SNAPSHOT",
    Unavailable = "SOURCE_REVISION_EVIDENCE_UNAVAILABLE",
});

proto_enum!(AttributionState {
    Unspecified = "ATTRIBUTION_STATE_UNSPECIFIED",
    Supported = "ATTRIBUTION_STATE_SUPPORTED",
    Unavailable = "ATTRIBUTION_STATE_UNAVAILABLE",
});

proto_enum!(ProblemSeverity {
    Unspecified = "PROBLEM_SEVERITY_UNSPECIFIED",
    Warning = "PROBLEM_SEVERITY_WARNING",
    Error = "PROBLEM_SEVERITY_ERROR",
});

proto_enum!(ReportMode {
    Unspecified = "REPORT_MODE_UNSPECIFIED",
    Live = "REPORT_MODE_LIVE",
    Saved = "REPORT_MODE_SAVED",
    Exported = "REPORT_MODE_EXPORTED",
});

proto_enum!(ExportUsageState {
    Unspecified = "EXPORT_USAGE_STATE_UNSPECIFIED",
    Available = "EXPORT_USAGE_STATE_AVAILABLE",
    Unavailable = "EXPORT_USAGE_STATE_UNAVAILABLE",
});

proto_enum!(ExportScope {
    Unspecified = "EXPORT_SCOPE_UNSPECIFIED",
    Named = "EXPORT_SCOPE_NAMED",
    Module = "EXPORT_SCOPE_MODULE",
});

proto_enum!(ExportUsagePathEnd {
    Unspecified = "EXPORT_USAGE_PATH_END_UNSPECIFIED",
    Root = "EXPORT_USAGE_PATH_END_ROOT",
    Cycle = "EXPORT_USAGE_PATH_END_CYCLE",
    Truncated = "EXPORT_USAGE_PATH_END_TRUNCATED",
    Incomplete = "EXPORT_USAGE_PATH_END_INCOMPLETE",
});

proto_enum!(ExportUsageTruncation {
    Unspecified = "EXPORT_USAGE_TRUNCATION_UNSPECIFIED",
    None = "EXPORT_USAGE_TRUNCATION_NONE",
    PathCount = "EXPORT_USAGE_TRUNCATION_PATH_COUNT",
    Depth = "EXPORT_USAGE_TRUNCATION_DEPTH",
    ExpansionBudget = "EXPORT_USAGE_TRUNCATION_EXPANSION_BUDGET",
});

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct EngineIdentity {
    pub bundler: Bundler,
    pub bundler_package: String,
    pub bundler_version: String,
    pub minifier: String,
    pub minifier_version: String,
    pub default_minifier: bool,
    pub adapter_id: String,
    pub adapter_version: String,
    pub support: EngineSupport,
    pub supported_versions: String,
    pub diagnostic_engine: bool,
    pub diagnostic_notice: String,
    pub overrides: Vec<String>,
}

impl EngineIdentity {
    pub fn summary(&self) -> String {
        format!(
            "{} {} + {} {}",
            self.bundler_package, self.bundler_version, self.minifier, self.minifier_version
        )
    }
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct SourceLocation {
    pub kind: LocationKind,
    pub source_id: String,
    pub path: String,
    pub line: u32,
    pub column: u32,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct CodeSnippet {
    pub kind: LocationKind,
    pub path: String,
    pub start_line: u32,
    pub highlight_line: u32,
    pub text: String,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Condition {
    pub description: String,
    pub outcome: ConditionOutcome,
    pub detail: String,
}

impl Condition {
    pub fn new(description: &str, outcome: ConditionOutcome, detail: impl Into<String>) -> Self {
        Self {
            description: description.to_owned(),
            outcome,
            detail: detail.into(),
        }
    }
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Evidence {
    pub provenance: String,
    pub detail: String,
    pub status: EvidenceStatus,
}

impl Evidence {
    pub fn new(provenance: &str, status: EvidenceStatus, detail: impl Into<String>) -> Self {
        Self {
            provenance: provenance.to_owned(),
            detail: detail.into(),
            status,
        }
    }
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct DependencyPath {
    pub modules: Vec<String>,
}

/// A module (display path) plus an export path inside it.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ExportReference {
    pub module: String,
    /// Export path segments; empty when `scope` is module scope.
    pub export_path: Vec<String>,
    pub scope: ExportScope,
}

/// One export dependency that the engine reported: `consumer` uses `provider`.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ExportUsageEdge {
    pub consumer: ExportReference,
    pub provider: ExportReference,
    pub dependency_id: String,
    pub location: String,
}

/// An ordered chain of engine edges from the outermost consumer to the
/// diagnostic module. The ordering is derived by bundlewhy.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ExportUsagePath {
    pub edges: Vec<ExportUsageEdge>,
    pub end: ExportUsagePathEnd,
}

/// Export-level usage evidence for one diagnostic.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ExportUsage {
    pub state: ExportUsageState,
    /// Why the evidence is unavailable.
    pub note: String,
    pub paths: Vec<ExportUsagePath>,
    pub truncation: ExportUsageTruncation,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct CorrectiveAction {
    pub direction: String,
    pub safety: String,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Diagnostic {
    pub id: String,
    pub category: OptimizationCategory,
    pub rule: String,
    pub status: EvidenceStatus,
    pub title: String,
    pub summary: String,
    pub conditions: Vec<Condition>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub location: Option<SourceLocation>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub generated_location: Option<SourceLocation>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub snippet: Option<CodeSnippet>,
    pub module: String,
    pub dependency_paths: Vec<DependencyPath>,
    pub evidence: Vec<Evidence>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub corrective_action: Option<CorrectiveAction>,
    pub engine: String,
    pub dependency: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub export_usage: Option<ExportUsage>,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct CategoryCoverage {
    pub category: OptimizationCategory,
    pub state: CoverageState,
    pub note: String,
    pub rules: Vec<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct SourceAttribution {
    pub path: String,
    pub generated_bytes: u32,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ChunkSize {
    pub file: String,
    pub raw_bytes: u32,
    pub gzip_bytes: u32,
    pub brotli_bytes: u32,
    pub attribution: AttributionState,
    pub sources: Vec<SourceAttribution>,
    pub unmapped_bytes: u32,
    pub entry: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Problem {
    pub code: String,
    pub severity: ProblemSeverity,
    pub message: String,
    pub recovery: String,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ConfigEntry {
    pub key: String,
    pub value: String,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct SourceFile {
    pub id: String,
    pub path: String,
    pub origin: SourceOrigin,
    pub sha256: String,
    pub content: String,
    pub content_included: bool,
    pub stale: bool,
    pub revision_evidence: SourceRevisionEvidence,
}

impl SourceFile {
    pub fn summary(&self) -> SourceFileSummary {
        SourceFileSummary {
            id: self.id.clone(),
            path: self.path.clone(),
            origin: self.origin,
            sha256: self.sha256.clone(),
            content_included: self.content_included,
            stale: self.stale,
            revision_evidence: self.revision_evidence,
        }
    }
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct SourceFileSummary {
    pub id: String,
    pub path: String,
    pub origin: SourceOrigin,
    pub sha256: String,
    pub content_included: bool,
    pub stale: bool,
    pub revision_evidence: SourceRevisionEvidence,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Analysis {
    pub schema_version: u32,
    pub run_id: String,
    pub project_name: String,
    pub config_file: String,
    pub status: RunStatus,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub engine: Option<EngineIdentity>,
    pub diagnostics: Vec<Diagnostic>,
    pub coverage: Vec<CategoryCoverage>,
    pub chunks: Vec<ChunkSize>,
    pub problems: Vec<Problem>,
    pub config_summary: Vec<ConfigEntry>,
    pub sources: Vec<SourceFileSummary>,
    pub started_at: String,
    pub finished_at: String,
    pub duration_ms: u32,
    pub mode: ReportMode,
    pub tool_version: String,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct RunProgress {
    pub run_id: String,
    pub status: RunStatus,
    pub stage: RunStage,
    pub message: String,
    pub elapsed_ms: u32,
    pub mode: ReportMode,
    pub cancellable: bool,
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct RunSummary {
    pub run_id: String,
    pub status: RunStatus,
    pub project_name: String,
    pub bundler: Bundler,
    pub started_at: String,
    pub active: bool,
}

/// Request and response messages of `AnalysisService`.
pub mod rpc {
    use serde::{Deserialize, Serialize};

    use super::{Analysis, RunProgress, RunSummary, SourceFile};

    #[derive(Debug, Default, Deserialize)]
    #[serde(rename_all = "camelCase", default)]
    pub struct Empty {}

    #[derive(Debug, Default, Serialize)]
    #[serde(rename_all = "camelCase")]
    pub struct ProgressResponse {
        pub progress: RunProgress,
    }

    #[derive(Debug, Default, Serialize)]
    #[serde(rename_all = "camelCase")]
    pub struct GetAnalysisResponse {
        pub analysis: Analysis,
    }

    #[derive(Debug, Default, Deserialize)]
    #[serde(rename_all = "camelCase", default)]
    pub struct GetSourceFileRequest {
        pub source_id: String,
    }

    #[derive(Debug, Default, Serialize)]
    #[serde(rename_all = "camelCase")]
    pub struct GetSourceFileResponse {
        pub source: SourceFile,
    }

    #[derive(Debug, Default, Serialize)]
    #[serde(rename_all = "camelCase")]
    pub struct ListRunsResponse {
        pub runs: Vec<RunSummary>,
    }

    #[derive(Debug, Default, Deserialize)]
    #[serde(rename_all = "camelCase", default)]
    pub struct SelectRunRequest {
        pub run_id: String,
    }

    #[derive(Debug, Default, Deserialize)]
    #[serde(rename_all = "camelCase", default)]
    pub struct ExportReportRequest {
        pub include_sources: bool,
    }

    #[derive(Debug, Default, Serialize)]
    #[serde(rename_all = "camelCase")]
    pub struct ExportReportResponse {
        pub file_name: String,
        pub sources_included: bool,
        pub size_bytes: u32,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn enums_use_proto_json_names() {
        let json = serde_json::to_string(&OptimizationCategory::TreeShaking).unwrap();
        assert_eq!(json, "\"OPTIMIZATION_CATEGORY_TREE_SHAKING\"");
        let status: EvidenceStatus = serde_json::from_str("\"EVIDENCE_STATUS_INFERRED\"").unwrap();
        assert_eq!(status, EvidenceStatus::Inferred);
        assert!(serde_json::from_str::<EvidenceStatus>("\"CONFIRMED\"").is_err());
    }

    #[test]
    fn omitted_proto3_defaults_deserialize() {
        let analysis: Analysis = serde_json::from_str(r#"{"schemaVersion":1}"#).unwrap();
        assert_eq!(analysis.status, RunStatus::Unspecified);
        assert!(analysis.diagnostics.is_empty());
        let json = serde_json::to_value(&analysis).unwrap();
        assert!(json.get("engine").is_none());
        assert_eq!(json["schemaVersion"], 1);
    }
}
