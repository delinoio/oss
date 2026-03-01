use std::{
    ffi::{OsStr, OsString},
    path::Path,
};

use clap::{ArgAction, Args, Parser, Subcommand};

use crate::types::{BumpLevel, OutputFormat};

#[derive(Debug, Parser)]
#[command(
    name = "cargo mono",
    bin_name = "cargo mono",
    version,
    about = "Cargo-based Rust monorepo management tool"
)]
pub struct Cli {
    /// Output format for command results.
    #[arg(long, global = true, value_enum, default_value_t = OutputFormat::Human)]
    pub output: OutputFormat,

    #[command(subcommand)]
    pub command: Command,
}

pub fn parse_from_env() -> Cli {
    Cli::parse_from(normalized_args_os(std::env::args_os()))
}

fn normalized_args_os<I>(args: I) -> Vec<OsString>
where
    I: IntoIterator<Item = OsString>,
{
    let mut normalized: Vec<OsString> = args.into_iter().collect();
    if should_strip_forwarded_mono_token(&normalized) {
        tracing::debug!(
            action = "normalize-cargo-external-subcommand-args",
            outcome = "strip-forwarded-mono-token",
            "Stripped Cargo-forwarded `mono` token from argv"
        );
        normalized.remove(1);
    }

    normalized
}

fn should_strip_forwarded_mono_token(args: &[OsString]) -> bool {
    let Some(argv0) = args.first() else {
        return false;
    };
    let Some(first_arg) = args.get(1) else {
        return false;
    };
    if first_arg != OsStr::new("mono") {
        return false;
    }

    let Some(executable_name) = Path::new(argv0).file_name().and_then(|name| name.to_str()) else {
        return false;
    };

    matches!(executable_name, "cargo-mono" | "cargo-mono.exe")
}

#[derive(Debug, Subcommand)]
pub enum Command {
    /// List workspace packages and publishability metadata.
    List,
    /// List changed workspace packages from git history.
    Changed(ChangedArgs),
    /// Bump workspace package versions.
    Bump(BumpArgs),
    /// Publish workspace packages to the registry.
    Publish(PublishArgs),
}

#[derive(Debug, Clone, Args)]
pub struct ChangedArgs {
    /// Base ref used for merge-base and diff calculation.
    #[arg(long, default_value = "origin/main")]
    pub base: String,
    /// Include staged, unstaged, and untracked paths.
    #[arg(long)]
    pub include_uncommitted: bool,
    /// Disable reverse dependency expansion and return direct matches only.
    #[arg(long)]
    pub direct_only: bool,
}

#[derive(Debug, Clone, Args)]
#[group(id = "target-selector", multiple = false)]
pub struct TargetArgs {
    /// Select all workspace packages (default when omitted).
    #[arg(long, action = ArgAction::SetTrue, group = "target-selector")]
    pub all: bool,
    /// Select changed packages.
    #[arg(long, action = ArgAction::SetTrue, group = "target-selector")]
    pub changed: bool,
    /// Select one or more explicit package names.
    #[arg(long, value_name = "PACKAGE", group = "target-selector")]
    pub package: Vec<String>,
}

#[derive(Debug, Clone, Args)]
pub struct BumpArgs {
    #[command(flatten)]
    pub target: TargetArgs,
    #[command(flatten)]
    pub changed: ChangedArgs,
    /// Bump level.
    #[arg(long, value_enum)]
    pub level: BumpLevel,
    /// Prerelease identifier used with `--level prerelease`.
    #[arg(long, required_if_eq("level", "prerelease"))]
    pub preid: Option<String>,
    /// Also apply patch bumps to dependent workspace packages.
    #[arg(long)]
    pub bump_dependents: bool,
    /// Allow execution with a dirty working tree.
    #[arg(long)]
    pub allow_dirty: bool,
}

#[derive(Debug, Clone, Args)]
pub struct PublishArgs {
    #[command(flatten)]
    pub target: TargetArgs,
    #[command(flatten)]
    pub changed: ChangedArgs,
    /// Validate publish without uploading artifacts.
    #[arg(long)]
    pub dry_run: bool,
    /// Allow execution with a dirty working tree.
    #[arg(long)]
    pub allow_dirty: bool,
    /// Override publish registry.
    #[arg(long)]
    pub registry: Option<String>,
}

#[cfg(test)]
mod tests {
    use std::ffi::OsString;

    use clap::Parser;

    use super::{normalized_args_os, Cli};

    #[test]
    fn bump_requires_level() {
        let parsed = Cli::try_parse_from(["cargo", "bump"]);
        assert!(parsed.is_err());
    }

    #[test]
    fn bump_rejects_multiple_target_selectors() {
        let parsed = Cli::try_parse_from([
            "cargo",
            "bump",
            "--level",
            "patch",
            "--all",
            "--package",
            "nodeup",
        ]);
        assert!(parsed.is_err());
    }

    #[test]
    fn bump_requires_preid_for_prerelease_level() {
        let parsed = Cli::try_parse_from(["cargo", "bump", "--level", "prerelease"]);
        assert!(parsed.is_err());
    }

    #[test]
    fn strips_forwarded_mono_token_when_first_runtime_arg() {
        let normalized = normalized_args_os(vec![
            OsString::from("/tmp/cargo-mono"),
            OsString::from("mono"),
            OsString::from("list"),
        ]);

        assert_eq!(
            normalized,
            vec![OsString::from("/tmp/cargo-mono"), OsString::from("list")]
        );
    }

    #[test]
    fn keeps_args_unchanged_for_direct_invocation_shape() {
        let normalized = normalized_args_os(vec![
            OsString::from("/tmp/cargo-mono"),
            OsString::from("--output"),
            OsString::from("json"),
            OsString::from("list"),
        ]);

        assert_eq!(
            normalized,
            vec![
                OsString::from("/tmp/cargo-mono"),
                OsString::from("--output"),
                OsString::from("json"),
                OsString::from("list"),
            ]
        );
    }

    #[test]
    fn keeps_args_unchanged_when_argv0_is_not_cargo_mono() {
        let normalized = normalized_args_os(vec![
            OsString::from("/tmp/custom-runner"),
            OsString::from("mono"),
            OsString::from("list"),
        ]);

        assert_eq!(
            normalized,
            vec![
                OsString::from("/tmp/custom-runner"),
                OsString::from("mono"),
                OsString::from("list"),
            ]
        );
    }
}
