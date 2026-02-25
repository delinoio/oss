use std::{
    collections::BTreeSet,
    ffi::OsString,
    path::PathBuf,
    process::{Command, Output},
};

use crate::errors::{with_context, CargoMonoError, ErrorKind, Result};

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
        with_context(
            ErrorKind::Git,
            &format!("Failed to resolve merge-base for base ref `{base_ref}`"),
            error,
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
    let output = run_git_capture(&["status", "--porcelain", "--untracked-files=normal"])?;
    Ok(output.trim().is_empty())
}

pub fn ensure_clean_working_tree(allow_dirty: bool) -> Result<()> {
    if allow_dirty {
        return Ok(());
    }

    if is_working_tree_clean()? {
        return Ok(());
    }

    Err(CargoMonoError::conflict(
        "Working tree is dirty; re-run with --allow-dirty to bypass this check",
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
    let output = Command::new("git")
        .args(args)
        .output()
        .map_err(|error| with_context(ErrorKind::Git, "Failed to execute git", error))?;

    ensure_success(&output, args.join(" "))?;
    Ok(output)
}

fn run_git_os(args: Vec<OsString>) -> Result<Output> {
    let output = Command::new("git")
        .args(args.iter().map(OsString::as_os_str))
        .output()
        .map_err(|error| with_context(ErrorKind::Git, "Failed to execute git", error))?;

    let command = args
        .iter()
        .map(|part| part.to_string_lossy().to_string())
        .collect::<Vec<_>>()
        .join(" ");
    ensure_success(&output, command)?;
    Ok(output)
}

fn run_git_capture(args: &[&str]) -> Result<String> {
    let output = run_git(args)?;
    Ok(String::from_utf8_lossy(&output.stdout).trim().to_string())
}

fn ensure_success(output: &Output, command: String) -> Result<()> {
    if output.status.success() {
        return Ok(());
    }

    let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
    let message = if stderr.is_empty() {
        format!("git {command} failed with status {}", output.status)
    } else {
        format!("git {command} failed: {stderr}")
    };

    Err(CargoMonoError::git(message))
}
