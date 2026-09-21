use std::io::IsTerminal;

use clap::{Parser, Subcommand};

fn color_choice() -> clap::ColorChoice {
    if std::io::stdout().is_terminal() && std::env::var_os("NO_COLOR").is_none() {
        clap::ColorChoice::Auto
    } else {
        clap::ColorChoice::Never
    }
}

#[derive(Parser)]
#[command(
    name = "clibox",
    color = color_choice(),
    version,
    about = "Cross-platform developer utilities distributed through native packages and npm",
    after_help = concat!(
        "Use clibox <command> <operation> --help for options and examples.\n",
        "Diagnostics use stderr, omit sensitive inputs, and honor RUST_LOG and NO_COLOR.\n",
        "Waits observe readiness without reserving a resource or guaranteeing continued readiness.\n",
        "Wait exit codes: 0 ready, 1 failure/timeout, 2 invalid input, 130 Ctrl+C, 143 Unix SIGTERM.\n\n",
        "Version: ", env!("CARGO_PKG_VERSION"), "\n",
        "Maintained by: ", env!("CARGO_PKG_AUTHORS"), "\n",
        "Repository: ", env!("CARGO_PKG_REPOSITORY"), "\n",
        "License: ", env!("CARGO_PKG_LICENSE"), "\n",
        "Support: ", env!("CARGO_PKG_REPOSITORY"), "/issues"
    )
)]
pub struct Cli {
    #[command(subcommand)]
    pub command: Option<Command>,
}

#[derive(Subcommand)]
pub enum Command {
    #[command(flatten)]
    Configuration(clibox_config::Command),
    #[command(flatten)]
    System(clibox_system::Command),
    #[command(flatten)]
    Transform(clibox_transform::Command),
    /// Wait for one resource to become ready (no stdin or subsequent command).
    #[command(subcommand)]
    Wait(clibox_wait::Command),
}

/// Never render clap's input-error diagnostics: they may embed argv,
/// credentials, and suggestions derived from a secret value. Generated help
/// and version output are handled separately; other failures use static
/// guidance.
pub fn parser_message(kind: clap::error::ErrorKind) -> &'static str {
    use clap::error::ErrorKind;
    match kind {
        ErrorKind::ArgumentConflict => {
            "Conflicting options; check command --help. --quiet and --json cannot be combined."
        }
        ErrorKind::MissingRequiredArgument | ErrorKind::MissingSubcommand => {
            "Select a command and provide its required arguments. Wait commands require exactly \
             one target. See clibox --help."
        }
        ErrorKind::ValueValidation | ErrorKind::InvalidValue => {
            "Invalid value; see command --help. For waits, check target syntax, get/head method, \
             status 200-599, and integer ms/s/m/h durations; interval and attempt timeout must be \
             positive."
        }
        _ => {
            "Unexpected or malformed argument; see command --help. Wait commands accept exactly \
             one target and supported options."
        }
    }
}
