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
    #[arg(long)]
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
