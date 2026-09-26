//! Pre-execution file selection and actual-read coverage analysis.

use std::{
    collections::{BTreeMap, BTreeSet},
    fs, io,
    path::{Path, PathBuf},
};

use globset::{GlobBuilder, GlobSet, GlobSetBuilder};
use walkdir::WalkDir;

use crate::record::{CompleteRecord, FileIdentity, NativePath, PathClass, Platform};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CoverageFailure {
    InvalidSelector,
    Unavailable,
    EmptySelection,
    IncompatibleRecord,
}

impl std::fmt::Display for CoverageFailure {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(match self {
            Self::InvalidSelector => "invalid_selector",
            Self::Unavailable => "selection_unavailable",
            Self::EmptySelection => "empty_selection",
            Self::IncompatibleRecord => "incompatible_record",
        })
    }
}

impl std::error::Error for CoverageFailure {}

#[derive(Debug, Clone)]
pub struct SelectedFile {
    pub identity: FileIdentity,
    pub logical: NativePath,
    pub initially_empty: bool,
}

#[derive(Debug, Clone)]
pub struct Denominator {
    pub root: PathBuf,
    pub files: BTreeMap<FileIdentity, SelectedFile>,
}

#[derive(Debug, Clone)]
pub struct CoverageReport {
    pub covered: Vec<SelectedFile>,
    pub uncovered: Vec<SelectedFile>,
    pub percentage: f64,
}

impl CoverageReport {
    pub fn fails_threshold(&self, threshold: f64) -> bool {
        self.percentage < threshold
    }
}

fn valid_selector(selector: &str) -> bool {
    !selector.is_empty()
        && !selector.starts_with('/')
        && !selector.starts_with('\\')
        && (!cfg!(windows) || !selector.contains(':'))
        && !selector.contains('\0')
        && !selector
            .split(['/', '\\'])
            .any(|component| component == "..")
}

fn glob_set(patterns: &[String]) -> Result<GlobSet, CoverageFailure> {
    let mut builder = GlobSetBuilder::new();
    for pattern in patterns {
        if !valid_selector(pattern) {
            return Err(CoverageFailure::InvalidSelector);
        }
        builder.add(
            GlobBuilder::new(pattern)
                .literal_separator(true)
                .build()
                .map_err(|_| CoverageFailure::InvalidSelector)?,
        );
    }
    builder
        .build()
        .map_err(|_| CoverageFailure::InvalidSelector)
}

fn native_path(path: &Path) -> NativePath {
    #[cfg(unix)]
    {
        use std::os::unix::ffi::OsStrExt;
        NativePath::UnixBytes(path.as_os_str().as_bytes().to_vec())
    }
    #[cfg(windows)]
    {
        use std::os::windows::ffi::OsStrExt;
        NativePath::WindowsUtf16(path.as_os_str().encode_wide().collect())
    }
}

fn host_platform() -> Platform {
    #[cfg(target_os = "linux")]
    {
        Platform::Linux
    }
    #[cfg(target_os = "macos")]
    {
        Platform::Macos
    }
    #[cfg(windows)]
    {
        Platform::Windows
    }
}

fn canonical_inside(root: &Path, path: &Path) -> io::Result<bool> {
    Ok(fs::canonicalize(path)?.starts_with(root))
}

/// Fix the denominator before launching the command. Symlinked directories
/// inside the root are traversed; links escaping the root are never selected.
pub fn select_existing(
    root: &Path,
    includes: &[String],
    excludes: &[String],
) -> Result<Denominator, CoverageFailure> {
    if includes.is_empty() {
        return Err(CoverageFailure::InvalidSelector);
    }
    let include = glob_set(includes)?;
    let exclude = glob_set(excludes)?;
    let root = fs::canonicalize(root).map_err(|_| CoverageFailure::Unavailable)?;
    if !root.is_dir() {
        return Err(CoverageFailure::Unavailable);
    }
    let mut files = BTreeMap::<FileIdentity, SelectedFile>::new();
    let mut unavailable = false;
    let walker = WalkDir::new(&root)
        .follow_links(true)
        .into_iter()
        .filter_entry(|entry| {
            match canonical_inside(&root, entry.path()) {
                Ok(inside) => inside,
                Err(_) => {
                    // A dangling link is not an existing regular file. Other
                    // enumeration failures make the denominator unknowable.
                    let missing_link = entry.path_is_symlink()
                        && fs::metadata(entry.path())
                            .is_err_and(|error| error.kind() == io::ErrorKind::NotFound);
                    if !missing_link {
                        unavailable = true;
                    }
                    false
                }
            }
        });
    for entry in walker {
        let entry = entry.map_err(|_| CoverageFailure::Unavailable)?;
        if !entry.file_type().is_file() {
            continue;
        }
        let relative = entry
            .path()
            .strip_prefix(&root)
            .map_err(|_| CoverageFailure::Unavailable)?;
        if !include.is_match(relative) || exclude.is_match(relative) {
            continue;
        }
        let identity = FileIdentity::from(
            file_id::get_file_id(entry.path()).map_err(|_| CoverageFailure::Unavailable)?,
        );
        let initially_empty = fs::metadata(entry.path())
            .map_err(|_| CoverageFailure::Unavailable)?
            .len()
            == 0;
        files.entry(identity).or_insert_with(|| SelectedFile {
            identity,
            logical: native_path(relative),
            initially_empty,
        });
    }
    if unavailable {
        return Err(CoverageFailure::Unavailable);
    }
    if files.is_empty() {
        return Err(CoverageFailure::EmptySelection);
    }
    Ok(Denominator { root, files })
}

/// Count a selected file only after a successful content read. A successful
/// EOF read counts solely when that file was empty before the test command.
pub fn analyze(
    denominator: &Denominator,
    record: &CompleteRecord,
) -> Result<CoverageReport, CoverageFailure> {
    if !record.summary.complete
        || native_path(&denominator.root) != record.header.root
        || record.header.platform != host_platform()
    {
        return Err(CoverageFailure::IncompatibleRecord);
    }
    let mut covered = BTreeSet::new();
    for pair in &record.operations {
        if !pair.start.operation.is_content_read() || pair.completion.native_error.is_some() {
            continue;
        }
        let Some(bytes) = pair.completion.byte_count else {
            return Err(CoverageFailure::IncompatibleRecord);
        };
        for path in &pair.start.paths {
            if path.class != PathClass::Project {
                continue;
            }
            if let Some(identity) = path.identity {
                if let Some(selected) = denominator.files.get(&identity) {
                    if bytes > 0 || selected.initially_empty {
                        covered.insert(identity);
                    }
                }
            }
        }
    }
    let mut yes = Vec::new();
    let mut no = Vec::new();
    for selected in denominator.files.values() {
        if covered.contains(&selected.identity) {
            yes.push(selected.clone());
        } else {
            no.push(selected.clone());
        }
    }
    Ok(CoverageReport {
        percentage: 100.0 * yes.len() as f64 / denominator.files.len() as f64,
        covered: yes,
        uncovered: no,
    })
}

#[cfg(test)]
mod tests {
    use uuid::Uuid;

    use super::*;
    use crate::record::{
        AccessPath, Backend, Completion, CoverageBoundary, Header, Operation, OperationPair, Start,
        Summary,
    };

    fn read_record(denominator: &Denominator, file: &SelectedFile, bytes: u64) -> CompleteRecord {
        CompleteRecord {
            header: Header {
                schema_version: 1,
                execution_id: Uuid::parse_str("01890f7e-4b1c-7cc2-9dce-dfca43a88f6f").unwrap(),
                platform: host_platform(),
                backend: Backend::Injection,
                root: native_path(&denominator.root),
                coverage: CoverageBoundary::SynchronousFileOperationsV1,
            },
            operations: vec![OperationPair {
                start: Start {
                    sequence: 1,
                    correlation_id: 1,
                    pid: 1,
                    tid: 1,
                    parent_pid: None,
                    operation: Operation::Read,
                    paths: vec![AccessPath {
                        class: PathClass::Project,
                        logical: file.logical.clone(),
                        resolved: None,
                        project_relative: Some(file.logical.clone()),
                        identity: Some(file.identity),
                    }],
                    descriptor: None,
                    monotonic_ns: 1,
                    requested_delay_ns: 0,
                },
                completion: Completion {
                    sequence: 2,
                    correlation_id: 1,
                    pid: 1,
                    tid: 1,
                    monotonic_ns: 2,
                    native_result: bytes as i64,
                    native_error: None,
                    byte_count: Some(bytes),
                    observed_delay_ns: 0,
                },
            }],
            summary: Summary {
                complete: true,
                child_exit_code: Some(0),
                child_signal: None,
                operation_count: 1,
                failure_count: 0,
                failure: None,
            },
        }
    }

    #[test]
    fn selects_existing_files_once_across_aliases() {
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("a.txt"), b"data").unwrap();
        fs::hard_link(
            directory.path().join("a.txt"),
            directory.path().join("b.txt"),
        )
        .unwrap();
        fs::write(directory.path().join("c.txt"), b"").unwrap();
        let selected = select_existing(directory.path(), &["*.txt".into()], &[]).unwrap();
        assert_eq!(selected.files.len(), 2);
        let nonempty = selected
            .files
            .values()
            .find(|file| !file.initially_empty)
            .unwrap();
        let report = analyze(&selected, &read_record(&selected, nonempty, 1)).unwrap();
        assert_eq!(report.covered.len(), 1);
        assert_eq!(report.uncovered.len(), 1);
        assert_eq!(report.percentage, 50.0);
        assert!(report.fails_threshold(50.1));
        assert!(!report.fails_threshold(50.0));
    }

    #[test]
    fn only_initially_empty_file_gets_eof_coverage() {
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("empty"), b"").unwrap();
        fs::write(directory.path().join("full"), b"x").unwrap();
        let selected = select_existing(directory.path(), &["*".into()], &[]).unwrap();
        let empty = selected
            .files
            .values()
            .find(|file| file.initially_empty)
            .unwrap();
        let full = selected
            .files
            .values()
            .find(|file| !file.initially_empty)
            .unwrap();
        assert_eq!(
            analyze(&selected, &read_record(&selected, empty, 0))
                .unwrap()
                .covered
                .len(),
            1
        );
        assert_eq!(
            analyze(&selected, &read_record(&selected, full, 0))
                .unwrap()
                .covered
                .len(),
            0
        );
    }

    #[test]
    fn rejects_escaping_and_empty_selection() {
        let directory = tempfile::tempdir().unwrap();
        assert_eq!(
            select_existing(directory.path(), &["../*.txt".into()], &[]).unwrap_err(),
            CoverageFailure::InvalidSelector,
        );
        assert_eq!(
            select_existing(directory.path(), &["*.txt".into()], &[]).unwrap_err(),
            CoverageFailure::EmptySelection,
        );
    }

    #[cfg(unix)]
    #[test]
    fn follows_internal_links_without_selecting_external_targets() {
        use std::os::unix::fs::symlink;

        let root = tempfile::tempdir().unwrap();
        let external = tempfile::tempdir().unwrap();
        fs::create_dir(root.path().join("nested")).unwrap();
        fs::write(root.path().join("nested/input.txt"), b"inside").unwrap();
        fs::write(external.path().join("outside.txt"), b"outside").unwrap();
        symlink("nested", root.path().join("alias")).unwrap();
        symlink(external.path(), root.path().join("outside")).unwrap();
        let selected = select_existing(root.path(), &["**/*.txt".into()], &[]).unwrap();
        assert_eq!(selected.files.len(), 1);
    }

    #[test]
    fn a_file_open_and_external_read_do_not_cover_project_input() {
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("input"), b"x").unwrap();
        let selected = select_existing(directory.path(), &["*".into()], &[]).unwrap();
        let file = selected.files.values().next().unwrap();
        let mut record = read_record(&selected, file, 1);
        record.operations[0].start.operation = Operation::Open;
        record.operations[0].completion.byte_count = None;
        assert_eq!(analyze(&selected, &record).unwrap().covered.len(), 0);
        record.operations[0].start.operation = Operation::Read;
        record.operations[0].completion.byte_count = Some(1);
        record.operations[0].start.paths[0].class = PathClass::External;
        record.operations[0].start.paths[0].project_relative = None;
        assert_eq!(analyze(&selected, &record).unwrap().covered.len(), 0);
    }
}
