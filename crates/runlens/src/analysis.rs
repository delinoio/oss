use serde::{Deserialize, Serialize};

use crate::{
    config::{self, Command, Policy},
    entries::Entries,
    error::{Error, Result},
    model::*,
    snapshot,
};

#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum AnalysisKind {
    Compare,
    Cache,
    Receipt,
    Policy,
    Explain,
    Conflicts,
}
#[derive(Debug, Serialize)]
pub struct Analysis {
    pub schema_version: u32,
    pub kind: AnalysisKind,
    pub verdict: Option<Verdict>,
    pub findings: Entries<Finding>,
    pub differences: Entries<Difference>,
    pub environment_differences: Entries<EnvironmentDifference>,
    pub usages: Entries<Usage>,
    pub limitations: Vec<String>,
}
#[derive(Debug, Serialize, Deserialize)]
pub struct EnvironmentDifference {
    pub left: Environment,
    pub right: Environment,
}
#[derive(Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Difference {
    pub path: String,
    pub change: ChangeKind,
    pub before: FileState,
    pub after: FileState,
}
#[derive(Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Usage {
    pub execution_id: uuid::Uuid,
    pub command: Identity,
    pub access: Option<Access>,
    pub change: Option<ChangeKind>,
    pub producer_candidate: bool,
    pub consumer_candidate: bool,
}
impl Analysis {
    pub fn new(kind: AnalysisKind) -> Self {
        Self {
            schema_version: 1,
            kind,
            verdict: None,
            findings: Entries::default(),
            differences: Entries::default(),
            environment_differences: Entries::default(),
            usages: Entries::default(),
            limitations: vec![
                "Accesses are attempts. Producer/consumer relationships and conflicts are \
                 candidates, not proven causality or races."
                    .into(),
                "A passing check applies only to supplied observations and configured rules; it \
                 does not certify universal cache safety or determinism."
                    .into(),
                "Selected environment values are not retained; reports with selected environment \
                 names cannot establish environment compatibility."
                    .into(),
            ],
        }
    }

    pub fn finding(
        &mut self,
        code: FindingCode,
        classification: Classification,
        evidence: Vec<Evidence>,
    ) -> Result<()> {
        self.findings.insert(
            format!("f{:08}", self.findings.len()),
            Finding {
                code,
                classification,
                evidence,
            },
        )?;
        Ok(())
    }

    pub fn finish(&mut self) -> Result<()> {
        let mut violation = false;
        let mut unknown = false;
        for finding in self.findings.iter() {
            let (_, finding) = finding?;
            violation |= finding.classification == Classification::Violation;
            unknown |= finding.classification == Classification::Unknown;
        }
        self.verdict = Some(if violation {
            Verdict::Failed
        } else if unknown {
            Verdict::Inconclusive
        } else {
            Verdict::Passed
        });
        Ok(())
    }
}
fn evidence(execution: &Execution, path: Option<&str>, source: EvidenceSource) -> Evidence {
    Evidence {
        execution_id: execution.id,
        path: path.map(str::to_owned),
        source,
    }
}
fn quality(result: &mut Analysis, execution: &Execution) -> Result<()> {
    if !execution.outcome.collection_complete
        || !execution.scope.before_complete
        || !execution.scope.after_complete
        || execution.scope.redacted_paths
    {
        result.finding(
            FindingCode::CollectionIncomplete,
            Classification::Unknown,
            vec![evidence(execution, None, EvidenceSource::Outcome)],
        )?;
    }
    if execution.outcome.child_exit_code != Some(0) || !execution.outcome.errors.is_empty() {
        result.finding(
            FindingCode::FailedExecution,
            Classification::Unknown,
            vec![evidence(execution, None, EvidenceSource::Outcome)],
        )?;
    }
    Ok(())
}
fn matches(set: &globset::GlobSet, path: &str) -> bool {
    set.is_match(config::relative_pattern_path(path)) || set.is_match(path)
}
pub fn cache(report: &Report, command: &Command, expected: &Identity) -> Result<Analysis> {
    let mut result = Analysis::new(AnalysisKind::Cache);
    target_quality(&mut result, report)?;
    for execution in report.targets() {
        quality(&mut result, execution)?;
        if !identity_matches(expected, &execution.command) {
            result.finding(
                FindingCode::UnknownEvidence,
                Classification::Unknown,
                vec![evidence(execution, None, EvidenceSource::Outcome)],
            )?;
            continue;
        }
        coverage(&mut result, execution, command, true, true)?;
    }
    result.finish()?;
    Ok(result)
}
fn identity_matches(expected: &Identity, recorded: &Identity) -> bool {
    !expected.argv.is_empty()
        && expected.argv == recorded.argv
        && expected.cwd == recorded.cwd
        && (recorded.name.is_none() || recorded.name == expected.name)
        && expected
            .argv
            .iter()
            .chain([&expected.cwd])
            .chain(expected.name.iter())
            .all(|value| !value.contains("[redacted]"))
}
fn target_quality(result: &mut Analysis, report: &Report) -> Result<()> {
    if report.targets().next().is_none() {
        result.finding(
            FindingCode::FailedExecution,
            Classification::Unknown,
            report
                .executions
                .first()
                .map(|execution| evidence(execution, None, EvidenceSource::Outcome))
                .into_iter()
                .collect(),
        )?;
    }
    for execution in report
        .executions
        .iter()
        .filter(|e| e.role == Role::Preparation)
    {
        quality(result, execution)?;
    }
    Ok(())
}
fn coverage(
    result: &mut Analysis,
    execution: &Execution,
    command: &Command,
    inputs_required: bool,
    outputs_required: bool,
) -> Result<()> {
    let inputs = config::patterns_for_os(&command.inputs, &execution.environment.os)?;
    let outputs = config::patterns_for_os(&command.outputs, &execution.environment.os)?;
    for item in execution.accesses.iter() {
        let (path, access) = item?;
        let input = matches(&inputs, &path);
        let output = matches(&outputs, &path);
        let existed_before = execution
            .before
            .get(&path)?
            .is_some_and(|state| state.knowledge != Knowledge::Missing);
        if inputs_required
            && (access.read || access.read_directory)
            && !input
            && (!output || existed_before)
        {
            result.finding(
                FindingCode::UndeclaredInput,
                Classification::Violation,
                vec![evidence(execution, Some(&path), EvidenceSource::Access)],
            )?;
        }
        if outputs_required && access.write && !output {
            result.finding(
                FindingCode::UncoveredOutput,
                Classification::Violation,
                vec![evidence(execution, Some(&path), EvidenceSource::Access)],
            )?;
        }
        if input && output {
            result.finding(
                FindingCode::InputOutputOverlap,
                Classification::Violation,
                vec![evidence(execution, Some(&path), EvidenceSource::Access)],
            )?;
        }
        if access.unsupported || !access.in_scope {
            result.finding(
                FindingCode::UnknownEvidence,
                Classification::Unknown,
                vec![evidence(execution, Some(&path), EvidenceSource::Access)],
            )?;
        }
    }
    let ancestors = allowed_write_ancestors(execution, Some(&outputs))?;
    for item in execution.changes.iter() {
        let (path, change) = item?;
        let output = matches(&outputs, &path);
        let state = execution.after.get(&path)?.or(execution.before.get(&path)?);
        // Directory membership ancestors are described in receipts. Changed leaf
        // entries are audited independently, avoiding an implicit whole-root output.
        let ancestor = change != ChangeKind::TypeChanged
            && ancestors.get(&path)?.is_some()
            && state.is_some_and(|s| {
                s.knowledge == Knowledge::Known && s.kind == Some(FileKind::Directory)
            });
        if change == ChangeKind::Unknown {
            result.finding(
                FindingCode::UnknownEvidence,
                Classification::Unknown,
                vec![evidence(
                    execution,
                    Some(&path),
                    if execution.after.get(&path)?.is_some() {
                        EvidenceSource::After
                    } else {
                        EvidenceSource::Before
                    },
                )],
            )?;
        } else if outputs_required && !output && !ancestor {
            let source = if change == ChangeKind::Deleted {
                EvidenceSource::Before
            } else {
                EvidenceSource::After
            };
            result.finding(
                FindingCode::UncoveredOutput,
                Classification::Violation,
                vec![evidence(execution, Some(&path), source)],
            )?;
        }
        if matches(&inputs, &path) && output {
            let source = if change == ChangeKind::Deleted {
                EvidenceSource::Before
            } else {
                EvidenceSource::After
            };
            result.finding(
                FindingCode::InputOutputOverlap,
                Classification::Violation,
                vec![evidence(execution, Some(&path), source)],
            )?;
        }
    }
    for input in &command.inputs {
        if command.outputs.contains(input) {
            result.finding(
                FindingCode::InputOutputOverlap,
                Classification::Violation,
                vec![evidence(execution, None, EvidenceSource::Outcome)],
            )?;
        }
    }
    Ok(())
}
// Index only ancestors of known, explicitly allowed changes. Every sibling and
// leaf still undergoes its own boundary check. Entries keeps this index bounded
// and spillable even for large reports; unknown changes never grant permission.
fn allowed_write_ancestors(
    execution: &Execution,
    allowed: Option<&globset::GlobSet>,
) -> Result<Entries<bool>> {
    let mut ancestors = Entries::default();
    let Some(allowed) = allowed else {
        return Ok(ancestors);
    };
    for entry in execution.changes.iter() {
        let (path, change) = entry?;
        if change == ChangeKind::Unknown
            || !matches(allowed, &path)
            || !path.starts_with("${workspace}/")
        {
            continue;
        }
        let mut current = path.as_str();
        while let Some((parent, _)) = current.rsplit_once('/') {
            if execution.changes.get(parent)?.is_some()
                && !ancestors.insert(parent.to_owned(), true)?
            {
                break;
            }
            current = parent;
        }
    }
    Ok(ancestors)
}

pub fn policy(
    report: &Report,
    rules: &Policy,
    commands: &std::collections::BTreeMap<String, Command>,
    expected: &std::collections::BTreeMap<String, Identity>,
    baseline: Option<&Report>,
) -> Result<Analysis> {
    if rules.fail_new_accesses && baseline.is_none() {
        return Err(Error::input(
            "fail_new_accesses requires an explicit baseline report",
        ));
    }
    let mut result = Analysis::new(AnalysisKind::Policy);
    target_quality(&mut result, report)?;
    for execution in report.current_executions() {
        let allow_read = rules
            .allow_reads
            .as_ref()
            .map(|p| config::patterns_for_os(p, &execution.environment.os))
            .transpose()?;
        let allow_write = rules
            .allow_writes
            .as_ref()
            .map(|p| config::patterns_for_os(p, &execution.environment.os))
            .transpose()?;
        let deny_read = config::patterns_for_os(&rules.deny_reads, &execution.environment.os)?;
        let deny_write = config::patterns_for_os(&rules.deny_writes, &execution.environment.os)?;
        let target = execution.role == Role::Target;
        if target {
            quality(&mut result, execution)?;
        }
        if target && (rules.require_inputs || rules.require_outputs) {
            let command = execution
                .command
                .name
                .as_ref()
                .and_then(|name| commands.get(name))
                .ok_or_else(|| {
                    Error::input("coverage policies require a matching configured command name")
                })?;
            let identity_known = execution
                .command
                .name
                .as_ref()
                .and_then(|name| expected.get(name))
                .is_some_and(|identity| identity_matches(identity, &execution.command));
            if identity_known {
                coverage(
                    &mut result,
                    execution,
                    command,
                    rules.require_inputs,
                    rules.require_outputs,
                )?;
            } else {
                result.finding(
                    FindingCode::UnknownEvidence,
                    Classification::Unknown,
                    vec![evidence(execution, None, EvidenceSource::Outcome)],
                )?;
            }
        }
        let previous = baseline.filter(|_| target).and_then(|report| {
            report.targets().find(|old| {
                old.command.name == execution.command.name
                    && old.command.argv == execution.command.argv
            })
        });
        if let Some(old) = previous {
            quality(&mut result, old)?;
            if !compatible(old, execution) {
                result.finding(
                    FindingCode::IncomparableEnvironment,
                    Classification::Unknown,
                    vec![
                        evidence(old, None, EvidenceSource::Outcome),
                        evidence(execution, None, EvidenceSource::Outcome),
                    ],
                )?;
            }
        }
        if target && baseline.is_some() && previous.is_none() {
            result.finding(
                FindingCode::UnknownEvidence,
                Classification::Unknown,
                vec![evidence(execution, None, EvidenceSource::Outcome)],
            )?;
        }
        for entry in execution.accesses.iter() {
            let (path, access) = entry?;
            // Lexical aliases containing `..` may cross symlinks, so collapsing
            // them cannot safely prove filesystem identity. Uncovered workspace
            // paths are uncertain too. Ordinary absolute external paths (such as
            // the executable) can still be checked against literal boundaries;
            // cache coverage separately requires their missing snapshot evidence.
            if access.unsupported
                || path.split('/').any(|part| matches!(part, "." | ".."))
                || !access.in_scope && (path == "${workspace}" || path.starts_with("${workspace}/"))
            {
                result.finding(
                    FindingCode::UnknownEvidence,
                    Classification::Unknown,
                    vec![evidence(execution, Some(&path), EvidenceSource::Access)],
                )?;
            }
            if (access.read || access.read_directory)
                && (matches(&deny_read, &path)
                    || allow_read.as_ref().is_some_and(|set| !matches(set, &path)))
            {
                result.finding(
                    FindingCode::ReadBoundary,
                    Classification::Violation,
                    vec![evidence(execution, Some(&path), EvidenceSource::Access)],
                )?;
            }
            if access.write
                && (matches(&deny_write, &path)
                    || allow_write.as_ref().is_some_and(|set| !matches(set, &path)))
            {
                result.finding(
                    FindingCode::WriteBoundary,
                    Classification::Violation,
                    vec![evidence(execution, Some(&path), EvidenceSource::Access)],
                )?;
            }
            if let Some(old) =
                previous.filter(|old| compatible(old, execution) && old.outcome.collection_complete)
            {
                let prior = old.accesses.get(&path)?;
                let new = prior.is_none_or(|p| {
                    access.read && !p.read
                        || access.write && !p.write
                        || access.read_directory && !p.read_directory
                });
                if new {
                    result.finding(
                        FindingCode::NewAccess,
                        if rules.fail_new_accesses {
                            Classification::Violation
                        } else {
                            Classification::Observed
                        },
                        vec![evidence(execution, Some(&path), EvidenceSource::Access)],
                    )?;
                }
            }
        }
        let ancestors = allowed_write_ancestors(execution, allow_write.as_ref())?;
        // A backend may miss a write while snapshots still prove a change.
        for entry in execution.changes.iter() {
            let (path, change) = entry?;
            // Directory membership changes accompany permitted descendant
            // creation/removal. Never exempt explicit denies, type changes,
            // unknown state, or access attempts at the ancestor itself.
            let ancestor = change != ChangeKind::TypeChanged
                && ancestors.get(&path)?.is_some()
                && execution
                    .after
                    .get(&path)?
                    .filter(|s| s.knowledge == Knowledge::Known)
                    .or(execution
                        .before
                        .get(&path)?
                        .filter(|s| s.knowledge == Knowledge::Known))
                    .is_some_and(|s| s.kind == Some(FileKind::Directory));
            if change != ChangeKind::Unknown
                && (matches(&deny_write, &path)
                    || !ancestor && allow_write.as_ref().is_some_and(|set| !matches(set, &path)))
            {
                result.finding(
                    FindingCode::WriteBoundary,
                    Classification::Violation,
                    vec![evidence(
                        execution,
                        Some(&path),
                        if change == ChangeKind::Deleted {
                            EvidenceSource::Before
                        } else {
                            EvidenceSource::After
                        },
                    )],
                )?;
            }
        }
    }
    result.finish()?;
    Ok(result)
}
pub fn compatible(left: &Execution, right: &Execution) -> bool {
    left.environment.os == right.environment.os
        && left.environment.architecture == right.environment.architecture
        && left.environment.os_version == right.environment.os_version
        && left.environment.runlens_version == right.environment.runlens_version
        && left.environment.engine_version == right.environment.engine_version
        && left.environment.source_revision == right.environment.source_revision
        && left.environment.working_tree_included == right.environment.working_tree_included
        && left.environment.executable_sha256.is_some()
        && left.environment.executable_sha256 == right.environment.executable_sha256
        && left.environment.os_version.is_some()
        && (left.environment.os != "linux"
            || left.environment.os_version.as_deref().is_some_and(crate::platform::known_linux_identity))
        && !left
            .command
            .argv
            .iter()
            .chain(right.command.argv.iter())
            .any(|arg| arg.contains("[redacted]"))
        && left.command.argv == right.command.argv
        && left.command.cwd == right.command.cwd
        && left.environment.environment_names == right.environment.environment_names
        // Names do not prove equality of omitted values. Do not store value
        // hashes either: low-entropy secrets would be recoverable by guessing.
        && left.environment.environment_names.is_empty()
        && left.scope.exclusions == right.scope.exclusions
        && !left.scope.redacted_paths
        && !right.scope.redacted_paths
}
#[derive(Debug, Clone)]
pub struct PathMap(pub String, pub String);
impl std::str::FromStr for PathMap {
    type Err = String;

    fn from_str(value: &str) -> std::result::Result<Self, Self::Err> {
        let (from, to) = value
            .split_once('=')
            .ok_or("path mapping must be FROM=TO")?;
        if from.is_empty()
            || to.is_empty()
            || from.len() > 32768
            || to.len() > 32768
            || from.chars().chain(to.chars()).any(char::is_control)
        {
            return Err("invalid path mapping".into());
        }
        Ok(Self(
            from.trim_end_matches('/').into(),
            to.trim_end_matches('/').into(),
        ))
    }
}
fn mapped(path: &str, mappings: &[PathMap]) -> String {
    mappings
        .iter()
        .filter(|m| path == m.0 || path.strip_prefix(&m.0).is_some_and(|s| s.starts_with('/')))
        .max_by_key(|m| m.0.len())
        .map_or_else(|| path.into(), |m| format!("{}{}", m.1, &path[m.0.len()..]))
}
pub fn compare(left: &Report, right: &Report, mappings: &[PathMap]) -> Result<Analysis> {
    let mut result = Analysis::new(AnalysisKind::Compare);
    let left_targets = left.targets().collect::<Vec<_>>();
    let right_targets = right.targets().collect::<Vec<_>>();
    if left_targets.is_empty()
        || right_targets.is_empty()
        || left_targets.len() != right_targets.len()
    {
        result.verdict = Some(Verdict::Inconclusive);
        result.limitations.push(
            "The reports have no target execution or different target execution counts.".into(),
        );
        return Ok(result);
    }
    target_quality(&mut result, left)?;
    target_quality(&mut result, right)?;
    for (left, right) in left_targets.into_iter().zip(right_targets) {
        quality(&mut result, left)?;
        quality(&mut result, right)?;
        if serde_json::to_value(&left.environment).map_err(|_| Error::storage())?
            != serde_json::to_value(&right.environment).map_err(|_| Error::storage())?
        {
            result.environment_differences.insert(
                left.id.to_string(),
                EnvironmentDifference {
                    left: left.environment.clone(),
                    right: right.environment.clone(),
                },
            )?;
        }
        if !compatible(left, right) {
            result.finding(
                FindingCode::IncomparableEnvironment,
                Classification::Unknown,
                vec![
                    evidence(left, None, EvidenceSource::Outcome),
                    evidence(right, None, EvidenceSource::Outcome),
                ],
            )?;
        }
        for (label, left_states, right_states, left_complete, right_complete) in [
            (
                "before",
                &left.before,
                &right.before,
                left.scope.before_complete,
                right.scope.before_complete,
            ),
            (
                "after",
                &left.after,
                &right.after,
                left.scope.after_complete,
                right.scope.after_complete,
            ),
        ] {
            let mut remapped = Entries::default();
            for entry in right_states.iter() {
                let (path, state) = entry?;
                if !remapped.insert(mapped(&path, mappings), state)? {
                    return Err(Error::input(
                        "path mapping creates ambiguous duplicate paths",
                    ));
                }
            }
            for entry in left_states.iter() {
                let (path, before) = entry?;
                let after = remapped
                    .get(&path)?
                    .unwrap_or_else(|| absent_state(right_complete));
                if let Some(change) = snapshot::difference(&before, &after) {
                    result.differences.insert(
                        format!("{}:{label}:{path}", left.id),
                        Difference {
                            path,
                            change,
                            before,
                            after,
                        },
                    )?;
                }
            }
            for entry in remapped.iter() {
                let (path, after) = entry?;
                if left_states.get(&path)?.is_none() {
                    let before = absent_state(left_complete);
                    let change =
                        snapshot::difference(&before, &after).unwrap_or(ChangeKind::Unknown);
                    result.differences.insert(
                        format!("{}:{label}:{path}", left.id),
                        Difference {
                            path,
                            change,
                            before,
                            after,
                        },
                    )?;
                }
            }
        }
        let mut mapped_accesses = Entries::default();
        let mut original_paths = Entries::default();
        for item in right.accesses.iter() {
            let (path, access) = item?;
            original_paths.insert(mapped(&path, mappings), path.clone())?;
            if !mapped_accesses.insert(mapped(&path, mappings), access)? {
                return Err(Error::input(
                    "path mapping creates ambiguous duplicate paths",
                ));
            }
        }
        for item in left.accesses.iter() {
            let (path, access) = item?;
            if mapped_accesses.get(&path)?.as_ref() != Some(&access) {
                result.finding(
                    FindingCode::NewAccess,
                    Classification::Observed,
                    vec![evidence(left, Some(&path), EvidenceSource::Access)],
                )?;
            }
        }
        for item in mapped_accesses.iter() {
            let (path, access) = item?;
            if left.accesses.get(&path)?.as_ref() != Some(&access) {
                let original: Option<String> = original_paths.get(&path)?;
                result.finding(
                    FindingCode::NewAccess,
                    Classification::Observed,
                    vec![evidence(right, original.as_deref(), EvidenceSource::Access)],
                )?;
            }
        }
        if left.outcome.child_exit_code != right.outcome.child_exit_code
            || left.outcome.child_signal != right.outcome.child_signal
        {
            result.finding(
                FindingCode::FailedExecution,
                Classification::Observed,
                vec![
                    evidence(left, None, EvidenceSource::Outcome),
                    evidence(right, None, EvidenceSource::Outcome),
                ],
            )?;
        }
    }
    result.finish()?;
    // Comparison success means the analysis completed, not that two executions
    // are reproducible. Equivalence checks consume differences separately.
    Ok(result)
}
pub fn receipt(report: &Report) -> Result<Analysis> {
    let mut result = Analysis::new(AnalysisKind::Receipt);
    for execution in &report.executions {
        for entry in execution.accesses.iter() {
            let (path, access) = entry?;
            result.usages.insert(
                format!("{}:{path}", execution.id),
                Usage {
                    execution_id: execution.id,
                    command: execution.command.clone(),
                    consumer_candidate: access.read || access.read_directory,
                    producer_candidate: access.write,
                    access: Some(access),
                    change: execution.changes.get(&path)?,
                },
            )?;
        }
        for entry in execution.changes.iter() {
            let (path, change) = entry?;
            let before = execution.before.get(&path)?.unwrap_or_else(|| {
                if execution.scope.before_complete {
                    FileState::missing()
                } else {
                    FileState::unknown(ObservationIssue::CollectionLimit)
                }
            });
            let after = execution.after.get(&path)?.unwrap_or_else(|| {
                if execution.scope.after_complete {
                    FileState::missing()
                } else {
                    FileState::unknown(ObservationIssue::CollectionLimit)
                }
            });
            result.differences.insert(
                format!("{}:{path}", execution.id),
                Difference {
                    path,
                    change,
                    before,
                    after,
                },
            )?;
        }
        quality(&mut result, execution)?;
    }
    Ok(result)
}
pub fn explain(path: &str, reports: &[Report]) -> Result<Analysis> {
    let mut result = Analysis::new(AnalysisKind::Explain);
    for report in reports {
        for execution in &report.executions {
            // Interpret source-platform syntax even when querying a saved report
            // on another OS; Unix backslashes remain literal filename bytes.
            let windows = execution.environment.os == "windows";
            let path = if windows {
                crate::privacy::windows_path(path)
            } else {
                path.to_owned()
            };
            let drive_absolute = windows
                && path.as_bytes().first().is_some_and(u8::is_ascii_alphabetic)
                && path.as_bytes().get(1..3) == Some(b":/");
            let path = if path.starts_with('/') || path.starts_with("${") || drive_absolute {
                path
            } else {
                format!("${{workspace}}/{}", path.trim_start_matches("./"))
            };
            let access = execution.accesses.get(&path)?;
            let change = execution.changes.get(&path)?;
            if access.is_some() || change.is_some() {
                result.usages.insert(
                    format!("{}:{path}", execution.id),
                    Usage {
                        execution_id: execution.id,
                        command: execution.command.clone(),
                        consumer_candidate: access
                            .as_ref()
                            .is_some_and(|a| a.read || a.read_directory),
                        producer_candidate: access.as_ref().is_some_and(|a| a.write)
                            || change.is_some_and(|c| c != ChangeKind::Unknown),
                        access,
                        change,
                    },
                )?;
            }
            quality(&mut result, execution)?;
        }
    }
    Ok(result)
}
pub const MAX_CONFLICT_TARGET_PAIRS: usize = 65_536;

pub fn conflicts(reports: &[Report]) -> Result<Analysis> {
    // Bound aggregate cross-report work before examining paths or generating
    // findings. Individually bounded reports can still have a huge cross product.
    let mut previous_targets = 0usize;
    let mut pairs = 0usize;
    for report in reports {
        let targets = report.targets().count();
        pairs = targets
            .checked_mul(previous_targets)
            .and_then(|additional| pairs.checked_add(additional))
            .filter(|total| *total <= MAX_CONFLICT_TARGET_PAIRS)
            .ok_or_else(|| {
                Error::input(
                    "conflict analysis exceeds 65,536 target pairs; select fewer reports or \
                     target executions",
                )
            })?;
        previous_targets = previous_targets.saturating_add(targets);
    }
    let mut result = Analysis::new(AnalysisKind::Conflicts);
    for (index, left) in reports.iter().enumerate() {
        for right in &reports[index + 1..] {
            for a in left.targets() {
                for b in right.targets() {
                    quality(&mut result, a)?;
                    quality(&mut result, b)?;
                    let mut paths: Entries<bool> = Entries::default();
                    for source in [&a.accesses, &b.accesses] {
                        for entry in source.iter() {
                            let (path, access) = entry?;
                            if access.write {
                                paths.insert(path, true)?;
                            }
                        }
                    }
                    for source in [&a.changes, &b.changes] {
                        for entry in source.iter() {
                            let (path, change) = entry?;
                            if change != ChangeKind::Unknown {
                                paths.insert(path, true)?;
                            }
                        }
                    }
                    for entry in paths.iter() {
                        let (path, _) = entry?;
                        let aa = a.accesses.get(&path)?;
                        let ba = b.accesses.get(&path)?;
                        let aw = aa.as_ref().is_some_and(|a| a.write)
                            || a.changes
                                .get(&path)?
                                .is_some_and(|c| c != ChangeKind::Unknown);
                        let bw = ba.as_ref().is_some_and(|a| a.write)
                            || b.changes
                                .get(&path)?
                                .is_some_and(|c| c != ChangeKind::Unknown);
                        let a_read = read_reference(a, &path)?;
                        let b_read = read_reference(b, &path)?;
                        let ar = a_read.is_some();
                        let br = b_read.is_some();
                        if aw && bw || aw && br || bw && ar {
                            let references = if aw && bw {
                                vec![write_reference(a, &path)?, write_reference(b, &path)?]
                            } else if aw {
                                vec![
                                    write_reference(a, &path)?,
                                    b_read.expect("read relationship checked"),
                                ]
                            } else {
                                vec![
                                    a_read.expect("read relationship checked"),
                                    write_reference(b, &path)?,
                                ]
                            };
                            result.finding(
                                if aw && bw {
                                    FindingCode::PotentialWriteConflict
                                } else {
                                    FindingCode::PotentialReadWriteConflict
                                },
                                Classification::Candidate,
                                references,
                            )?;
                            result.usages.insert(
                                format!("{}:{path}", a.id),
                                Usage {
                                    execution_id: a.id,
                                    command: a.command.clone(),
                                    access: aa,
                                    change: a.changes.get(&path)?,
                                    producer_candidate: aw,
                                    consumer_candidate: ar,
                                },
                            )?;
                            result.usages.insert(
                                format!("{}:{path}", b.id),
                                Usage {
                                    execution_id: b.id,
                                    command: b.command.clone(),
                                    access: ba,
                                    change: b.changes.get(&path)?,
                                    producer_candidate: bw,
                                    consumer_candidate: br,
                                },
                            )?;
                        }
                    }
                }
            }
        }
    }
    Ok(result)
}
fn read_reference(execution: &Execution, path: &str) -> Result<Option<Evidence>> {
    let mut current = Some(path);
    while let Some(candidate) = current {
        if execution
            .accesses
            .get(candidate)?
            .is_some_and(|access| access.read_directory || candidate == path && access.read)
        {
            return Ok(Some(evidence(
                execution,
                Some(candidate),
                EvidenceSource::Access,
            )));
        }
        current = candidate
            .rsplit_once('/')
            .map(|(parent, _)| parent)
            .filter(|parent| !parent.is_empty());
    }
    Ok(None)
}
fn write_reference(execution: &Execution, path: &str) -> Result<Evidence> {
    let source = if execution
        .accesses
        .get(path)?
        .is_some_and(|access| access.write)
    {
        EvidenceSource::Access
    } else if execution.after.get(path)?.is_some() {
        EvidenceSource::After
    } else {
        EvidenceSource::Before
    };
    Ok(evidence(execution, Some(path), source))
}
pub fn repeated_outputs(report: &Report, outputs: &[String]) -> Result<Analysis> {
    if outputs.is_empty() {
        return Err(Error::input(
            "repeat verification requires declared outputs",
        ));
    }
    let mut result = Analysis::new(AnalysisKind::Compare);
    let targets = report.targets().collect::<Vec<_>>();
    if targets.len() < 2 {
        return Err(Error::input(
            "repeat verification requires at least two target executions",
        ));
    }
    for target in &targets {
        quality(&mut result, target)?;
        for pattern in outputs {
            let matcher =
                config::patterns_for_os(std::slice::from_ref(pattern), &target.environment.os)?;
            let mut found = false;
            for entry in target.after.iter() {
                let (path, state) = entry?;
                if matches(&matcher, &path) && state.knowledge == Knowledge::Known {
                    found = true;
                }
            }
            if !found {
                result.finding(
                    FindingCode::MissingOutput,
                    Classification::Violation,
                    vec![evidence(target, None, EvidenceSource::Outcome)],
                )?;
            }
        }
    }
    for target in &targets[1..] {
        let first = targets[0];
        if !compatible(first, target) {
            result.finding(
                FindingCode::IncomparableEnvironment,
                Classification::Unknown,
                vec![
                    evidence(first, None, EvidenceSource::Outcome),
                    evidence(target, None, EvidenceSource::Outcome),
                ],
            )?;
        }
        let mut paths: Entries<bool> = Entries::default();
        for execution in [first, *target] {
            let set = config::patterns_for_os(outputs, &execution.environment.os)?;
            for entry in execution.after.iter() {
                let (path, _) = entry?;
                if matches(&set, &path) {
                    paths.insert(path, true)?;
                }
            }
        }
        for entry in paths.iter() {
            let (path, _) = entry?;
            let before = first.after.get(&path)?.unwrap_or_else(FileState::missing);
            let after = target.after.get(&path)?.unwrap_or_else(FileState::missing);
            if let Some(change) = snapshot::difference(&before, &after) {
                let class = if change == ChangeKind::Unknown {
                    Classification::Unknown
                } else {
                    Classification::Violation
                };
                result.finding(
                    FindingCode::DifferentOutput,
                    class,
                    vec![
                        evidence(first, None, EvidenceSource::Outcome),
                        evidence(target, None, EvidenceSource::Outcome),
                    ],
                )?;
                result.differences.insert(
                    format!("{}:{path}", target.id),
                    Difference {
                        path,
                        change,
                        before,
                        after,
                    },
                )?;
            }
        }
    }
    result.finish()?;
    Ok(result)
}

fn absent_state(complete: bool) -> FileState {
    if complete {
        FileState::missing()
    } else {
        FileState::unknown(ObservationIssue::CollectionLimit)
    }
}
