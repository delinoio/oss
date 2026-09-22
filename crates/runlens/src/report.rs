use std::{
    fs::File,
    io::{BufReader, Read, Seek, SeekFrom, Write},
    path::Path,
};

use serde::Serialize;

use crate::{
    entries::MAX_RECORD_BYTES,
    error::{Error, ErrorCode, Result},
    model::*,
};
pub const MAX_REPORT_BYTES: u64 = 1024 * 1024 * 1024;

pub fn read(path: &Path) -> Result<Report> {
    let file = File::open(path).map_err(|_| Error::input("report cannot be opened"))?;
    if !file
        .metadata()
        .is_ok_and(|m| m.is_file() && m.len() <= MAX_REPORT_BYTES)
    {
        return Err(Error::input(
            "report must be a regular file no larger than 1 GiB",
        ));
    }
    let mut reader = BufReader::new(file);
    guard_json(&mut reader)?;
    reader
        .seek(SeekFrom::Start(0))
        .map_err(|_| Error::input("report cannot be read"))?;
    let report: Report = crate::model::with_envelope_budget(|| serde_json::from_reader(reader))
        .map_err(|_| Error::input("invalid or incompatible report; expected schema v1"))?;
    validate(&report)?;
    let spilled_maps = usize::from(report.findings.spilled())
        + report
            .executions
            .iter()
            .map(|execution| {
                [
                    execution.accesses.spilled(),
                    execution.before.spilled(),
                    execution.after.spilled(),
                    execution.changes.spilled(),
                ]
                .into_iter()
                .filter(|spilled| *spilled)
                .count()
            })
            .sum::<usize>();
    tracing::debug!(
        stage = "report-read",
        spilled_maps,
        retained_memory_bytes = crate::entries::memory_used(),
        "loaded report metadata"
    );
    Ok(report)
}
fn guard_json(reader: &mut impl Read) -> Result<()> {
    let mut buffer = [0u8; 65536];
    let mut bytes = 0u64;
    let mut depth = 0usize;
    let mut in_string = false;
    let mut escaped = false;
    let mut string_bytes = 0usize;
    loop {
        let count = reader
            .read(&mut buffer)
            .map_err(|_| Error::input("report cannot be read"))?;
        if count == 0 {
            break;
        }
        bytes += count as u64;
        if bytes > MAX_REPORT_BYTES {
            return Err(Error::input("report exceeds the size limit"));
        }
        for byte in &buffer[..count] {
            if in_string {
                string_bytes += 1;
                if string_bytes > MAX_RECORD_BYTES {
                    return Err(Error::input("report contains an oversized string"));
                }
                if escaped {
                    escaped = false;
                } else if *byte == b'\\' {
                    escaped = true;
                } else if *byte == b'"' {
                    in_string = false;
                }
            } else {
                match byte {
                    b'"' => {
                        in_string = true;
                        string_bytes = 0;
                    }
                    b'{' | b'[' => {
                        depth += 1;
                        if depth > 48 {
                            return Err(Error::input("report nesting exceeds the limit"));
                        }
                    }
                    b'}' | b']' => depth = depth.saturating_sub(1),
                    _ => {}
                }
            }
        }
    }
    Ok(())
}
/// Shared by report validation and prelaunch planning so serialization limits
/// cannot first reject a command after its side effects have occurred.
#[derive(Default)]
pub(crate) struct MetadataBudget(usize);
impl MetadataBudget {
    pub(crate) fn planned() -> Self {
        // Reserve the bounded, built-in run/clean/repeat limitation notices.
        Self(4096)
    }

    fn text(&mut self, value: &str) -> Result<()> {
        self.0 = self
            .0
            .saturating_add(value.len() + std::mem::size_of::<String>());
        if value.len() > MAX_RECORD_BYTES
            || self.0 > MAX_ENVELOPE_BYTES
            || serde_json::to_string(value)
                .map_err(|_| Error::input("invalid metadata"))?
                .len()
                - 1
                > MAX_RECORD_BYTES
        {
            return Err(Error::input("report metadata exceeds the envelope limit"));
        }
        Ok(())
    }

    pub(crate) fn execution(
        &mut self,
        command: &Identity,
        env: &Environment,
        scope: &Scope,
    ) -> Result<()> {
        if command.argv.is_empty()
            || command.argv.len() > 1024
            || env.environment_names.len() > 1024
            || scope.root != "${workspace}"
            || scope.exclusions.len() > 4096
            || scope.input_patterns.len() > 4096
            || scope.output_patterns.len() > 4096
        {
            return Err(Error::input("invalid execution metadata"));
        }
        for value in command
            .argv
            .iter()
            .chain(command.name.iter())
            .chain([
                &command.cwd,
                &scope.root,
                &env.os,
                &env.architecture,
                &env.runlens_version,
                &env.engine_version,
            ])
            .chain(scope.exclusions.iter())
            .chain(scope.input_patterns.iter())
            .chain(scope.output_patterns.iter())
            .chain(env.environment_names.iter())
            .chain(env.os_version.iter())
            .chain(env.source_revision.iter())
            .chain(env.executable_sha256.iter())
        {
            self.text(value)?;
        }
        valid_path(&command.cwd)?;
        if let Some(hash) = &env.executable_sha256 {
            valid_digest(hash)?;
        }
        Ok(())
    }
}

pub fn validate(report: &Report) -> Result<()> {
    if report.schema_version != 1 {
        return Err(Error::input("unsupported report schema version"));
    }
    if report.executions.is_empty()
        || report.executions.len() > MAX_EXECUTIONS
        || report.limitations.len() > 64
    {
        return Err(Error::input("invalid report envelope size"));
    }
    let mut ids = std::collections::BTreeSet::new();
    let mut envelope = MetadataBudget::default();
    for value in &report.limitations {
        envelope.text(value)?;
    }
    for execution in &report.executions {
        if execution.id.get_version_num() != 7 || !ids.insert(execution.id) {
            return Err(Error::input("execution IDs must be unique UUID v7 values"));
        }
        envelope.execution(&execution.command, &execution.environment, &execution.scope)?;
        if execution.outcome.child_exit_code.is_some() && execution.outcome.child_signal.is_some() {
            return Err(Error::input("contradictory child termination outcome"));
        }
        if execution.outcome.collection_complete
            && execution.outcome.errors.iter().any(|e| {
                matches!(
                    e,
                    ErrorCode::Incomplete
                        | ErrorCode::Unsupported
                        | ErrorCode::Timeout
                        | ErrorCode::Cancelled
                        | ErrorCode::CleanupFailed
                )
            })
        {
            return Err(Error::input("inconsistent collection outcome"));
        }
        for (states, complete) in [
            (&execution.before, execution.scope.before_complete),
            (&execution.after, execution.scope.after_complete),
        ] {
            for item in states.iter() {
                let (path, state) = item?;
                valid_path(&path)?;
                if let Some(hash) = state.sha256.as_ref() {
                    valid_digest(hash)?;
                }
                if state.knowledge == Knowledge::Known && state.kind.is_none()
                    || state.knowledge == Knowledge::Unknown && state.reason.is_none()
                {
                    return Err(Error::input("invalid filesystem knowledge state"));
                }
                if state.knowledge == Knowledge::Known {
                    let valid = match state.kind {
                        Some(FileKind::File) => {
                            state.sha256.is_some()
                                && state.size.is_some()
                                && state.link_target.is_none()
                                && (state.executable.is_some()
                                    == (execution.environment.os != "windows"))
                        }
                        Some(FileKind::Directory) => {
                            state.sha256.is_some()
                                && state.size.is_none()
                                && state.executable.is_none()
                                && state.link_target.is_none()
                        }
                        Some(FileKind::Symlink) => {
                            state.link_target.is_some()
                                && state.sha256.is_none()
                                && state.size.is_none()
                                && state.executable.is_none()
                        }
                        Some(FileKind::Other) | None => false,
                    };
                    if !valid {
                        return Err(Error::input(
                            "known state lacks required kind-specific metadata",
                        ));
                    }
                }
                if complete && state.knowledge == Knowledge::Unknown {
                    return Err(Error::input("complete snapshot contains unknown state"));
                }
                if state.knowledge == Knowledge::Missing && state != FileState::missing() {
                    return Err(Error::input("missing state contains observed metadata"));
                }
                if state.knowledge == Knowledge::Known && state.reason.is_some() {
                    return Err(Error::input("known state contains an observation failure"));
                }
            }
        }
        for access in execution.accesses.iter() {
            let (path, access) = access?;
            valid_path(&path)?;
            if !access.read && !access.write && !access.read_directory && !access.unsupported {
                return Err(Error::input("empty access observation"));
            }
            if access.unsupported && execution.outcome.collection_complete {
                return Err(Error::input(
                    "unsupported access cannot have a complete outcome",
                ));
            }
        }
        // Require every derived change as well as validating supplied changes.
        // Omitting a changed output must not turn an edited report into a pass.
        for states in [&execution.before, &execution.after] {
            for item in states.iter() {
                let (path, _) = item?;
                let absent = |complete| {
                    if complete {
                        FileState::missing()
                    } else {
                        FileState::unknown(ObservationIssue::CollectionLimit)
                    }
                };
                let before = execution
                    .before
                    .get(&path)?
                    .unwrap_or_else(|| absent(execution.scope.before_complete));
                let after = execution
                    .after
                    .get(&path)?
                    .unwrap_or_else(|| absent(execution.scope.after_complete));
                if crate::snapshot::difference(&before, &after) != execution.changes.get(&path)? {
                    return Err(Error::input(
                        "snapshot change evidence is missing or inconsistent",
                    ));
                }
            }
        }
        for change in execution.changes.iter() {
            let (path, change) = change?;
            let before = execution.before.get(&path)?;
            let after = execution.after.get(&path)?;
            if before.is_none() && after.is_none() {
                return Err(Error::input("change refers to missing snapshot evidence"));
            }
            let absent = |complete| {
                if complete {
                    FileState::missing()
                } else {
                    FileState::unknown(ObservationIssue::CollectionLimit)
                }
            };
            let expected = crate::snapshot::difference(
                &before.unwrap_or_else(|| absent(execution.scope.before_complete)),
                &after.unwrap_or_else(|| absent(execution.scope.after_complete)),
            );
            if expected != Some(change) {
                return Err(Error::input(
                    "change classification contradicts its snapshots",
                ));
            }
        }
        if execution.outcome.collection_complete
            && (!execution.scope.before_complete
                || !execution.scope.after_complete
                || execution.scope.redacted_paths)
        {
            return Err(Error::input(
                "incomplete snapshot scope cannot have a complete outcome",
            ));
        }
    }
    if report.kind == ReportKind::Repeat && report.verification == Some(Verdict::Passed) {
        let targets = report.targets().collect::<Vec<_>>();
        if targets.len() < 2
            || targets
                .iter()
                .enumerate()
                .any(|(index, target)| target.repetition as usize != index + 1)
        {
            return Err(Error::input(
                "passed repeat reports require consecutive target repetitions starting at one",
            ));
        }
    }
    if report.targets().next().is_none() && report.verification == Some(Verdict::Passed) {
        return Err(Error::input(
            "verification cannot pass without a target execution",
        ));
    }
    if report.verification == Some(Verdict::Passed)
        && report.current_executions().any(|e| !e.outcome.success())
    {
        return Err(Error::input(
            "failed or incomplete execution cannot pass verification",
        ));
    }
    for item in report.findings.iter() {
        let (_, finding) = item?;
        if finding.evidence.is_empty() || finding.evidence.len() > 32 {
            return Err(Error::input("finding evidence is invalid"));
        }
        if report.verification == Some(Verdict::Passed)
            && matches!(
                finding.classification,
                Classification::Unknown | Classification::Violation
            )
        {
            return Err(Error::input(
                "unknown or violated evidence cannot pass verification",
            ));
        }
        for reference in &finding.evidence {
            let execution = report
                .executions
                .iter()
                .find(|e| e.id == reference.execution_id)
                .ok_or_else(|| Error::input("finding refers to an unknown execution"))?;
            match (reference.source, reference.path.as_ref()) {
                (EvidenceSource::Outcome, None) => {}
                (EvidenceSource::Access, Some(path)) if execution.accesses.get(path)?.is_some() => {
                }
                (EvidenceSource::Before, Some(path)) if execution.before.get(path)?.is_some() => {}
                (EvidenceSource::After, Some(path)) if execution.after.get(path)?.is_some() => {}
                _ => return Err(Error::input("finding refers to unavailable evidence")),
            }
        }
    }
    Ok(())
}
fn valid_digest(hash: &str) -> Result<()> {
    if hash.len() != 64
        || !hash
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
    {
        Err(Error::input("invalid content digest"))
    } else {
        Ok(())
    }
}
fn valid_path(path: &str) -> Result<()> {
    if path.is_empty() || path.len() > 32768 || path.chars().any(char::is_control) {
        Err(Error::input("invalid normalized report path"))
    } else {
        Ok(())
    }
}
pub fn destination_available(path: &Path) -> Result<()> {
    if std::fs::symlink_metadata(path).is_ok() {
        return Err(Error::new(
            ErrorCode::SaveFailed,
            "destination already exists; select a new report path",
        ));
    }
    let parent = path
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    if !parent.is_dir() {
        return Err(Error::new(
            ErrorCode::SaveFailed,
            "destination directory does not exist",
        ));
    }
    Ok(())
}
pub fn save(path: &Path, report: &Report, html: bool) -> Result<()> {
    validate(report)?;
    atomic_write(path, |writer| {
        if html {
            write_html(writer, report)
        } else {
            serde_json::to_writer_pretty(writer, report).map_err(std::io::Error::other)
        }
    })
}
pub fn atomic_write(
    path: &Path,
    write: impl FnOnce(&mut dyn Write) -> std::io::Result<()>,
) -> Result<()> {
    destination_available(path)?;
    let parent = path
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    let mut file = tempfile::NamedTempFile::new_in(parent)
        .map_err(|_| Error::new(ErrorCode::SaveFailed, "cannot create report destination"))?;
    let mut bounded = LimitedWriter {
        inner: file.as_file_mut(),
        bytes: 0,
    };
    write(&mut bounded).map_err(|_| {
        Error::new(
            ErrorCode::SaveFailed,
            "report could not be written completely",
        )
    })?;
    file.as_file_mut()
        .sync_all()
        .map_err(|_| Error::new(ErrorCode::SaveFailed, "report could not be synchronized"))?;
    file.persist_noclobber(path).map_err(|_| {
        Error::new(
            ErrorCode::SaveFailed,
            "report publication failed or destination already exists",
        )
    })?;
    #[cfg(unix)]
    File::open(parent)
        .and_then(|dir| dir.sync_all())
        .map_err(|_| {
            Error::new(
                ErrorCode::SaveFailed,
                "report directory synchronization failed; check the selected destination",
            )
        })?;
    Ok(())
}
struct LimitedWriter<'a> {
    inner: &'a mut File,
    bytes: u64,
}
impl Write for LimitedWriter<'_> {
    fn write(&mut self, bytes: &[u8]) -> std::io::Result<usize> {
        if self.bytes + bytes.len() as u64 > MAX_REPORT_BYTES {
            return Err(std::io::Error::other("report size limit"));
        }
        let count = self.inner.write(bytes)?;
        self.bytes += count as u64;
        Ok(count)
    }

    fn flush(&mut self) -> std::io::Result<()> {
        self.inner.flush()
    }
}
pub fn json(value: &impl Serialize) -> Result<()> {
    let stdout = std::io::stdout();
    let mut writer = stdout.lock();
    serde_json::to_writer_pretty(&mut writer, value)
        .map_err(|_| Error::new(ErrorCode::SaveFailed, "machine output could not be written"))?;
    writeln!(writer)
        .map_err(|_| Error::new(ErrorCode::SaveFailed, "machine output could not be written"))?;
    Ok(())
}
fn escaped(writer: &mut dyn Write, value: &str) -> std::io::Result<()> {
    for byte in value.as_bytes() {
        match byte {
            b'&' => writer.write_all(b"&amp;")?,
            b'<' => writer.write_all(b"&lt;")?,
            b'>' => writer.write_all(b"&gt;")?,
            b'"' => writer.write_all(b"&quot;")?,
            b'\'' => writer.write_all(b"&#39;")?,
            _ => writer.write_all(&[*byte])?,
        }
    }
    Ok(())
}
fn write_html(writer: &mut dyn Write, report: &Report) -> std::io::Result<()> {
    writer.write_all(b"<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; base-uri 'none'; form-action 'none'\"><title>Runlens execution report</title></head><body><a href=\"#main\">Skip to report</a><main id=\"main\"><h1>Runlens execution report</h1><p>Metadata only. Access attempts do not establish successful reads or causality.</p><h2>Executions</h2><table><caption>Command and collection outcomes</caption><thead><tr><th scope=\"col\">Execution</th><th scope=\"col\">Command</th><th scope=\"col\">Child status</th><th scope=\"col\">Collection</th></tr></thead><tbody>")?;
    for execution in &report.executions {
        writer.write_all(b"<tr><th scope=\"row\">")?;
        escaped(writer, &execution.id.to_string())?;
        writer.write_all(b"</th><td>")?;
        escaped(writer, &execution.command.argv.join(" "))?;
        write!(
            writer,
            "</td><td>{:?}</td><td>{}</td></tr>",
            execution.outcome.child_exit_code,
            if execution.outcome.collection_complete {
                "Complete within declared coverage"
            } else {
                "Incomplete; verification cannot pass"
            }
        )?;
    }
    writer.write_all(b"</tbody></table><h2>Limitations</h2><ul>")?;
    for limitation in &report.limitations {
        writer.write_all(b"<li>")?;
        escaped(writer, limitation)?;
        writer.write_all(b"</li>")?;
    }
    writer.write_all(b"</ul><h2>Evidence</h2><p>Before/after states, changes, and access attempts are separate fields. Unknown evidence is not an empty file.</p><details open><summary>Full metadata in JSON</summary><pre>")?;
    struct EscapeWriter<'a>(&'a mut dyn Write);
    impl Write for EscapeWriter<'_> {
        fn write(&mut self, bytes: &[u8]) -> std::io::Result<usize> {
            for byte in bytes {
                match byte {
                    b'&' => self.0.write_all(b"&amp;")?,
                    b'<' => self.0.write_all(b"&lt;")?,
                    b'>' => self.0.write_all(b"&gt;")?,
                    _ => self.0.write_all(&[*byte])?,
                }
            }
            Ok(bytes.len())
        }

        fn flush(&mut self) -> std::io::Result<()> {
            self.0.flush()
        }
    }
    serde_json::to_writer_pretty(EscapeWriter(writer), report).map_err(std::io::Error::other)?;
    writer.write_all(b"</pre></details></main></body></html>")
}
