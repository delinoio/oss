use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::{entries::Entries, error::ErrorCode};

pub const SCHEMA_VERSION: u32 = 1;
pub const ENGINE_REVISION: &str = "13aa80a0dac698023ce68ba16497b16e5330600b+runlens.1";
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ReportKind {
    Run,
    Clean,
    Repeat,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Role {
    Target,
    Preparation,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Knowledge {
    Known,
    Missing,
    Unknown,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum FileKind {
    File,
    Directory,
    Symlink,
    Other,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FileState {
    pub knowledge: Knowledge,
    pub kind: Option<FileKind>,
    pub size: Option<u64>,
    pub sha256: Option<String>,
    pub executable: Option<bool>,
    pub link_target: Option<String>,
    pub reason: Option<ObservationIssue>,
}
impl FileState {
    pub fn unknown(reason: ObservationIssue) -> Self {
        Self {
            knowledge: Knowledge::Unknown,
            kind: None,
            size: None,
            sha256: None,
            executable: None,
            link_target: None,
            reason: Some(reason),
        }
    }

    pub fn missing() -> Self {
        Self {
            knowledge: Knowledge::Missing,
            kind: None,
            size: None,
            sha256: None,
            executable: None,
            link_target: None,
            reason: None,
        }
    }
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ObservationIssue {
    PermissionDenied,
    Unstable,
    Io,
    Excluded,
    OutsideScope,
    NonUnicode,
    Redacted,
    CollectionLimit,
    UnsupportedProcess,
    Cancelled,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Access {
    pub read: bool,
    pub write: bool,
    pub read_directory: bool,
    pub unsupported: bool,
    pub in_scope: bool,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ChangeKind {
    Created,
    Modified,
    Deleted,
    TypeChanged,
    Unknown,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Classification {
    Observed,
    Candidate,
    Violation,
    Unknown,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Evidence {
    #[serde(deserialize_with = "uuid_v7")]
    pub execution_id: Uuid,
    pub path: Option<String>,
    pub source: EvidenceSource,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum EvidenceSource {
    Access,
    Before,
    After,
    Outcome,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Finding {
    pub code: FindingCode,
    pub classification: Classification,
    pub evidence: Vec<Evidence>,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum FindingCode {
    UndeclaredInput,
    UncoveredOutput,
    InputOutputOverlap,
    ReadBoundary,
    WriteBoundary,
    NewAccess,
    PotentialWriteConflict,
    PotentialReadWriteConflict,
    MissingOutput,
    DifferentOutput,
    UnknownEvidence,
    IncomparableEnvironment,
    FailedExecution,
    CollectionIncomplete,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Environment {
    pub os: String,
    pub architecture: String,
    pub os_version: Option<String>,
    pub runlens_version: String,
    pub engine_version: String,
    pub source_revision: Option<String>,
    pub working_tree_included: bool,
    pub environment_names: Vec<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Identity {
    pub name: Option<String>,
    pub argv: Vec<String>,
    pub cwd: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Scope {
    pub root: String,
    pub exclusions: Vec<String>,
    pub input_patterns: Vec<String>,
    pub output_patterns: Vec<String>,
    pub before_complete: bool,
    pub after_complete: bool,
    pub redacted_paths: bool,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Outcome {
    pub child_exit_code: Option<i32>,
    pub child_signal: Option<i32>,
    pub collection_complete: bool,
    pub errors: Vec<ErrorCode>,
    pub elapsed_ms: u64,
}
impl Outcome {
    pub fn success(&self) -> bool {
        self.child_exit_code == Some(0) && self.collection_complete && self.errors.is_empty()
    }
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Execution {
    #[serde(deserialize_with = "uuid_v7")]
    pub id: Uuid,
    pub role: Role,
    pub repetition: u32,
    pub command: Identity,
    pub environment: Environment,
    pub scope: Scope,
    pub before: Entries<FileState>,
    pub after: Entries<FileState>,
    pub accesses: Entries<Access>,
    pub changes: Entries<ChangeKind>,
    pub outcome: Outcome,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Verdict {
    Passed,
    Failed,
    Inconclusive,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Report {
    pub schema_version: u32,
    pub kind: ReportKind,
    pub executions: Vec<Execution>,
    pub findings: Entries<Finding>,
    pub verification: Option<Verdict>,
    pub limitations: Vec<String>,
}
impl Report {
    pub fn new(kind: ReportKind) -> Self {
        Self {
            schema_version: SCHEMA_VERSION,
            kind,
            executions: Vec::new(),
            findings: Entries::default(),
            verification: None,
            limitations: vec![
                "Access modes record attempts, not successful content reads, syscall success, \
                 process attribution, or event order."
                    .into(),
                "Filesystem observation excludes network activity, environment reads, clocks, and \
                 randomness; complete collection is limited to the declared backend and snapshot \
                 scope."
                    .into(),
                "Metadata and automatic masking can still expose sensitive information; review \
                 reports before sharing."
                    .into(),
            ],
        }
    }

    pub fn targets(&self) -> impl Iterator<Item = &Execution> {
        self.executions
            .iter()
            .filter(|execution| execution.role == Role::Target)
    }
}

fn uuid_v7<'de, D: serde::Deserializer<'de>>(deserializer: D) -> Result<Uuid, D::Error> {
    let value = String::deserialize(deserializer)?;
    let id = Uuid::parse_str(&value).map_err(serde::de::Error::custom)?;
    if id.get_version_num() != 7 || id.to_string() != value {
        return Err(serde::de::Error::custom(
            "expected canonical lowercase UUID v7",
        ));
    }
    Ok(id)
}
