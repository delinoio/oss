use clap::{Parser, Subcommand, ValueEnum};

#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum)]
pub enum OutputFormat {
    Human,
    Json,
}

#[derive(Debug, Parser)]
#[command(
    name = "nodeup",
    version,
    about = "Rustup-like Node.js version manager"
)]
pub struct Cli {
    #[arg(long, global = true, value_enum, default_value_t = OutputFormat::Human)]
    pub output: OutputFormat,

    #[command(subcommand)]
    pub command: Command,
}

#[derive(Debug, Subcommand)]
pub enum Command {
    Toolchain {
        #[command(subcommand)]
        command: ToolchainCommand,
    },
    Default {
        runtime: Option<String>,
    },
    Show {
        #[command(subcommand)]
        command: ShowCommand,
    },
    Update {
        runtimes: Vec<String>,
    },
    Check,
    Override {
        #[command(subcommand)]
        command: OverrideCommand,
    },
    Which {
        #[arg(long)]
        runtime: Option<String>,
        command: String,
    },
    Run {
        #[arg(long)]
        install: bool,
        runtime: String,
        #[arg(required = true, trailing_var_arg = true)]
        command: Vec<String>,
    },
    #[command(name = "self")]
    SelfCmd {
        #[command(subcommand)]
        command: SelfCommand,
    },
    Completions {
        shell: String,
        command: Option<String>,
    },
}

#[derive(Debug, Subcommand)]
pub enum ToolchainCommand {
    List,
    Install { runtimes: Vec<String> },
    Uninstall { runtimes: Vec<String> },
    Link { name: String, path: String },
}

#[derive(Debug, Subcommand)]
pub enum ShowCommand {
    #[command(name = "active-runtime")]
    ActiveRuntime,
    Home,
}

#[derive(Debug, Subcommand)]
pub enum OverrideCommand {
    List,
    Set {
        runtime: String,
        #[arg(long)]
        path: Option<String>,
    },
    Unset {
        #[arg(long)]
        path: Option<String>,
        #[arg(long)]
        nonexistent: bool,
    },
}

#[derive(Debug, Subcommand)]
pub enum SelfCommand {
    Update,
    Uninstall,
    #[command(name = "upgrade-data")]
    UpgradeData,
}
