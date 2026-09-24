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
        "Use clibox <command> --help or clibox <command> <operation> --help for options and examples.\n",
        "Diagnostics use stderr, omit sensitive inputs, and honor RUST_LOG and NO_COLOR.\n",
        "Exit codes: 0 success, 1 operation failure, 2 invalid input,\n",
        "130 Ctrl+C/Windows Ctrl+Break, 143 Unix SIGTERM.\n",
        "run env preserves the child command's exit status and termination signals.\n\n",
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
pub fn parser_message(kind: clap::error::ErrorKind, raw: &[std::ffi::OsString]) -> String {
    use clap::error::ErrorKind;
    // Inspect only the fixed command positions. Never echo option values, unknown
    // tokens, or delegated argv, even when they happen to name another command.
    let group = raw.get(1).and_then(|value| value.to_str());
    let operation = raw.get(2).and_then(|value| value.to_str());
    let migration = match (group, operation) {
        (Some("env"), Some("run")) => Some("env run was renamed; use clibox run env --help."),
        (Some("port"), Some("which")) => {
            Some("port which was renamed; use clibox port list --help.")
        }
        (Some("hash"), Some("encode")) => {
            Some("hash encode was renamed; use clibox hash compute --help.")
        }
        _ => None,
    };
    if let Some(message) = migration {
        return message.to_owned();
    }
    let command = match (group, operation) {
        (Some("run"), Some("env")) => "clibox run env",
        (Some("run"), Some("with-rate-limit")) => "clibox run with-rate-limit",
        (Some("run"), Some("with-lock")) => "clibox run with-lock",
        (Some("run"), Some("with-service")) => "clibox run with-service",
        (Some("run"), Some("with-retry")) => "clibox run with-retry",
        (Some("run"), Some("with-timeout")) => "clibox run with-timeout",
        (Some("port"), Some("list")) => "clibox port list",
        (Some("port"), Some("kill")) => "clibox port kill",
        (Some("clipboard"), Some("copy")) => "clibox clipboard copy",
        (Some("clipboard"), Some("paste")) => "clibox clipboard paste",
        (Some("system"), Some("cpus")) => "clibox system cpus",
        (Some("dotenv"), Some("list")) => "clibox dotenv list",
        (Some("dotenv"), Some("merge")) => "clibox dotenv merge",
        (Some("yaml"), Some("normalize")) => "clibox yaml normalize",
        (Some("text"), Some("replace")) => "clibox text replace",
        (Some("time"), Some("format")) => "clibox time format",
        (Some("time"), Some("add")) => "clibox time add",
        (Some("base64"), Some("encode")) => "clibox base64 encode",
        (Some("base64"), Some("decode")) => "clibox base64 decode",
        (Some("hash"), Some("compute")) => "clibox hash compute",
        (Some("hash"), Some("verify")) => "clibox hash verify",
        (Some("wait"), Some("tcp")) => "clibox wait tcp",
        (Some("wait"), Some("http")) => "clibox wait http",
        (Some("wait"), Some("file")) => "clibox wait file",
        (Some("run"), _) => "clibox run",
        (Some("port"), _) => "clibox port",
        (Some("open"), _) => "clibox open",
        (Some("clipboard"), _) => "clibox clipboard",
        (Some("system"), _) => "clibox system",
        (Some("dotenv"), _) => "clibox dotenv",
        (Some("yaml"), _) => "clibox yaml",
        (Some("text"), _) => "clibox text",
        (Some("time"), _) => "clibox time",
        (Some("base64"), _) => "clibox base64",
        (Some("hash"), _) => "clibox hash",
        (Some("wait"), _) => "clibox wait",
        _ => "clibox",
    };
    let guidance = match kind {
        ErrorKind::ArgumentConflict => "Conflicting options.",
        ErrorKind::MissingRequiredArgument | ErrorKind::MissingSubcommand => {
            "Provide the required command and arguments."
        }
        ErrorKind::ValueValidation | ErrorKind::InvalidValue => "Invalid argument value.",
        _ => "Unexpected or malformed argument.",
    };
    format!("{guidance} See {command} --help.")
}
