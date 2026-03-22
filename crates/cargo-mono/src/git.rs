use std::{
    collections::BTreeSet,
    ffi::OsString,
    path::PathBuf,
    process::{Command, Output},
};

use crate::errors::{CargoMonoError, ErrorKind, Result};

const DIRTY_SAMPLE_LIMIT: usize = 5;

#[derive(Debug, Clone)]
pub struct ChangedFiles {
    pub merge_base: String,
    pub paths: BTreeSet<PathBuf>,
}

pub fn current_head() -> Result<String> {
    run_git_capture(&["rev-parse", "HEAD"])
}

pub fn merge_base(base_ref: &str) -> Result<String> {
    run_git_capture(&["merge-base", base_ref, "HEAD"]).map_err(|error| {
        CargoMonoError::with_details(
            ErrorKind::Git,
            "Failed to resolve merge base.",
            vec![
                ("base_ref", base_ref.to_string()),
                ("command", format!("git merge-base {base_ref} HEAD")),
                ("cause", error.message),
            ],
            "Ensure the base ref exists locally (for example, run `git fetch`) and retry with \
             `--base <ref>`.",
        )
    })
}

pub fn changed_files(base_ref: &str, include_uncommitted: bool) -> Result<ChangedFiles> {
    let merge_base = merge_base(base_ref)?;
    let diff_output = run_git_capture(&["diff", "--name-only", &merge_base, "HEAD"])?;
    let mut paths = parse_paths(&diff_output);

    if include_uncommitted {
        let staged_output = run_git_capture(&["diff", "--name-only", "--cached"])?;
        let unstaged_output = run_git_capture(&["diff", "--name-only"])?;
        let untracked_output = run_git_capture(&["ls-files", "--others", "--exclude-standard"])?;

        paths.extend(parse_paths(&staged_output));
        paths.extend(parse_paths(&unstaged_output));
        paths.extend(parse_paths(&untracked_output));
    }

    Ok(ChangedFiles { merge_base, paths })
}

pub fn is_working_tree_clean() -> Result<bool> {
    Ok(working_tree_status_entries()?.is_empty())
}

pub fn ensure_clean_working_tree(allow_dirty: bool) -> Result<()> {
    if allow_dirty {
        return Ok(());
    }

    let dirty_entries = working_tree_status_entries()?;
    if dirty_entries.is_empty() {
        return Ok(());
    }

    let dirty_entry_count = dirty_entries.len();
    let visible_sample_count = dirty_entry_count.min(DIRTY_SAMPLE_LIMIT);
    let mut dirty_sample = dirty_entries
        .iter()
        .take(visible_sample_count)
        .cloned()
        .collect::<Vec<_>>()
        .join(" | ");
    if dirty_entry_count > visible_sample_count {
        dirty_sample.push_str(&format!(
            " ... (+{} more)",
            dirty_entry_count - visible_sample_count
        ));
    }

    Err(CargoMonoError::with_details(
        ErrorKind::Conflict,
        "Working tree is dirty and cannot pass preflight checks.",
        vec![
            ("allow_dirty", allow_dirty.to_string()),
            ("dirty_entry_count", dirty_entry_count.to_string()),
            ("dirty_sample", dirty_sample),
            (
                "command",
                "git status --porcelain --untracked-files=normal".to_string(),
            ),
        ],
        "Commit or stash local changes, or rerun with `--allow-dirty` when this is intentional.",
    ))
}

pub fn add_paths(paths: &BTreeSet<PathBuf>) -> Result<()> {
    if paths.is_empty() {
        return Ok(());
    }

    let mut args = Vec::<OsString>::new();
    args.push(OsString::from("add"));
    args.push(OsString::from("--"));
    for path in paths {
        args.push(path.as_os_str().to_os_string());
    }

    run_git_os(args)?;
    Ok(())
}

pub fn commit_paths(message: &str, paths: &BTreeSet<PathBuf>) -> Result<String> {
    let mut args = Vec::<OsString>::new();
    args.push(OsString::from("commit"));
    args.push(OsString::from("-m"));
    args.push(OsString::from(message));

    if !paths.is_empty() {
        args.push(OsString::from("--"));
        for path in paths {
            args.push(path.as_os_str().to_os_string());
        }
    }

    run_git_os(args)?;
    current_head()
}

pub fn create_tag(tag: &str) -> Result<()> {
    run_git(&["tag", tag])?;
    Ok(())
}

fn parse_paths(output: &str) -> BTreeSet<PathBuf> {
    output
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty())
        .map(PathBuf::from)
        .collect()
}

fn run_git(args: &[&str]) -> Result<Output> {
    let command = format!("git {}", args.join(" "));
    let output = Command::new("git")
        .args(args)
        .output()
        .map_err(|error| start_git_error(&command, error))?;

    ensure_success(&output, command)?;
    Ok(output)
}

fn run_git_os(args: Vec<OsString>) -> Result<Output> {
    let command = format!(
        "git {}",
        args.iter()
            .map(|part| part.to_string_lossy().to_string())
            .collect::<Vec<_>>()
            .join(" ")
    );
    let output = Command::new("git")
        .args(args.iter().map(OsString::as_os_str))
        .output()
        .map_err(|error| start_git_error(&command, error))?;
    ensure_success(&output, command)?;
    Ok(output)
}

fn run_git_capture(args: &[&str]) -> Result<String> {
    let output = run_git(args)?;
    Ok(String::from_utf8_lossy(&output.stdout).trim().to_string())
}

fn working_tree_status_entries() -> Result<Vec<String>> {
    let output = run_git_capture(&["status", "--porcelain", "--untracked-files=normal"])?;
    Ok(output
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty())
        .map(ToString::to_string)
        .collect())
}

fn start_git_error(command: &str, error: std::io::Error) -> CargoMonoError {
    CargoMonoError::with_details(
        ErrorKind::Git,
        "Failed to start git command.",
        vec![
            ("command", command.to_string()),
            ("error", error.to_string()),
        ],
        "Ensure `git` is installed and available in PATH.",
    )
}

fn ensure_success(output: &Output, command: String) -> Result<()> {
    if output.status.success() {
        return Ok(());
    }

    let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
    let stdout = String::from_utf8_lossy(&output.stdout).trim().to_string();
    let details_excerpt = if stderr.is_empty() { stdout } else { stderr };
    let mut context = vec![
        ("command", command.clone()),
        ("status", output.status.to_string()),
    ];
    if !details_excerpt.is_empty() {
        context.push(("details_excerpt", details_excerpt));
    }

    Err(CargoMonoError::with_details(
        ErrorKind::Git,
        "Git command failed.",
        context,
        format!("Run `{command}` directly to inspect and resolve the underlying problem."),
    ))
}
