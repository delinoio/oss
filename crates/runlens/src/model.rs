use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::{entries::Entries, error::ErrorCode};

pub const SCHEMA_VERSION: u32 = 1;
pub const ENGINE_REVISION: &str = "13aa80a0dac698023ce68ba16497b16e5330600b+runlens.1";
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum ReportKind {
    Run,
    Clean,
    Repeat,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum Role {
    Target,
    Preparation,
    Baseline,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum Knowledge {
    Known,
    Missing,
    Unknown,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum FileKind {
    File,
    Directory,
    Symlink,
    Other,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
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
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
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
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Access {
    pub read: bool,
    pub write: bool,
    pub read_directory: bool,
    pub unsupported: bool,
    pub in_scope: bool,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum ChangeKind {
    Created,
    Modified,
    Deleted,
    TypeChanged,
    Unknown,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum Classification {
    Observed,
    Candidate,
    Violation,
    Unknown,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Evidence {
    #[serde(deserialize_with = "uuid_v7")]
    #[schemars(with = "uuid::Uuid")]
    pub execution_id: Uuid,
    pub path: Option<String>,
    pub source: EvidenceSource,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum EvidenceSource {
    Access,
    Before,
    After,
    Outcome,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Finding {
    pub code: FindingCode,
    pub classification: Classification,
    #[serde(deserialize_with = "bounded_vec::<_, _, 32>")]
    pub evidence: Vec<Evidence>,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
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
#[derive(Debug, Clone, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Environment {
    #[serde(deserialize_with = "bounded_text")]
    pub os: String,
    #[serde(deserialize_with = "bounded_text")]
    pub architecture: String,
    #[serde(deserialize_with = "bounded_optional_text")]
    pub os_version: Option<String>,
    #[serde(deserialize_with = "bounded_text")]
    pub runlens_version: String,
    #[serde(deserialize_with = "bounded_text")]
    pub engine_version: String,
    #[serde(deserialize_with = "bounded_optional_text")]
    pub executable_sha256: Option<String>,
    #[serde(deserialize_with = "bounded_optional_text")]
    pub source_revision: Option<String>,
    pub working_tree_included: bool,
    #[serde(deserialize_with = "bounded_strings::<_, 1024>")]
    pub environment_names: Vec<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Identity {
    #[serde(deserialize_with = "bounded_optional_text")]
    pub name: Option<String>,
    #[serde(deserialize_with = "bounded_strings::<_, 1024>")]
    pub argv: Vec<String>,
    #[serde(deserialize_with = "bounded_text")]
    pub cwd: String,
}
#[derive(Debug, Clone, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Scope {
    #[serde(deserialize_with = "bounded_text")]
    pub root: String,
    #[serde(deserialize_with = "bounded_strings::<_, 4096>")]
    pub exclusions: Vec<String>,
    #[serde(deserialize_with = "bounded_strings::<_, 4096>")]
    pub input_patterns: Vec<String>,
    #[serde(deserialize_with = "bounded_strings::<_, 4096>")]
    pub output_patterns: Vec<String>,
    pub before_complete: bool,
    pub after_complete: bool,
    pub redacted_paths: bool,
}
#[derive(Debug, Clone, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Outcome {
    pub child_exit_code: Option<i32>,
    pub child_signal: Option<i32>,
    pub collection_complete: bool,
    #[serde(deserialize_with = "bounded_vec::<_, _, 10>")]
    pub errors: Vec<ErrorCode>,
    pub elapsed_ms: u64,
}
impl Outcome {
    pub fn success(&self) -> bool {
        self.child_exit_code == Some(0) && self.collection_complete && self.errors.is_empty()
    }
}
#[derive(Debug, Clone, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Execution {
    #[serde(deserialize_with = "uuid_v7")]
    #[schemars(with = "uuid::Uuid")]
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
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum Verdict {
    Passed,
    Failed,
    Inconclusive,
}
#[derive(Debug, Clone, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Report {
    pub schema_version: u32,
    pub kind: ReportKind,
    #[serde(deserialize_with = "bounded_vec::<_, _, 1056>")]
    pub executions: Vec<Execution>,
    pub findings: Entries<Finding>,
    pub verification: Option<Verdict>,
    #[serde(deserialize_with = "bounded_strings::<_, 64>")]
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

    pub fn current_executions(&self) -> impl Iterator<Item = &Execution> {
        self.executions
            .iter()
            .filter(|execution| execution.role != Role::Baseline)
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

pub const MAX_ENVELOPE_BYTES: usize = 1024 * 1024;
thread_local! { static ENVELOPE_BYTES: std::cell::Cell<usize> = const { std::cell::Cell::new(0) }; }
pub fn reset_envelope_budget() {
    ENVELOPE_BYTES.set(0);
}
fn charge<E: serde::de::Error>(bytes: usize) -> Result<(), E> {
    let total = ENVELOPE_BYTES.get().saturating_add(bytes);
    if total > MAX_ENVELOPE_BYTES {
        return Err(E::custom("report envelope exceeds 1 MiB"));
    }
    ENVELOPE_BYTES.set(total);
    Ok(())
}
fn bounded_text<'de, D: serde::Deserializer<'de>>(deserializer: D) -> Result<String, D::Error> {
    let value = String::deserialize(deserializer)?;
    charge::<D::Error>(value.len() + std::mem::size_of::<String>())?;
    Ok(value)
}
fn bounded_optional_text<'de, D: serde::Deserializer<'de>>(
    deserializer: D,
) -> Result<Option<String>, D::Error> {
    let value = Option::<String>::deserialize(deserializer)?;
    if let Some(value) = &value {
        charge::<D::Error>(value.len() + std::mem::size_of::<String>())?;
    }
    Ok(value)
}
fn bounded_vec<'de, D, T, const N: usize>(deserializer: D) -> Result<Vec<T>, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Deserialize<'de>,
{
    struct Bounded<T, const N: usize>(std::marker::PhantomData<T>);
    impl<'de, T: Deserialize<'de>, const N: usize> serde::de::Visitor<'de> for Bounded<T, N> {
        type Value = Vec<T>;

        fn expecting(&self, f: &mut std::fmt::Formatter) -> std::fmt::Result {
            write!(f, "at most {N} elements")
        }

        fn visit_seq<S: serde::de::SeqAccess<'de>>(
            self,
            mut seq: S,
        ) -> Result<Self::Value, S::Error> {
            let mut values = Vec::new();
            while let Some(value) = seq.next_element()? {
                if values.len() == N {
                    return Err(serde::de::Error::custom("too many report array elements"));
                }
                values.push(value);
            }
            Ok(values)
        }
    }
    deserializer.deserialize_seq(Bounded::<T, N>(std::marker::PhantomData))
}
fn bounded_strings<'de, D: serde::Deserializer<'de>, const N: usize>(
    deserializer: D,
) -> Result<Vec<String>, D::Error> {
    // A string is charged as soon as it is decoded, before the array can grow.
    #[derive(Deserialize)]
    struct Text(#[serde(deserialize_with = "bounded_text")] String);
    Ok(bounded_vec::<D, Text, N>(deserializer)?
        .into_iter()
        .map(|text| text.0)
        .collect())
}
