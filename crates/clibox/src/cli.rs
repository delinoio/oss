use std::path::PathBuf;

use clap::{Args, Parser, Subcommand, ValueEnum};

use crate::error::{Code, Error, Result};

const IO_HELP: &str = "Input defaults to stdin until EOF; --input - explicitly selects stdin.
--text supplies UTF-8 bytes with no added newline. Explicit input ignores stdin.
Output defaults to stdout. File output is prepared beside its destination and
published only on completion; --force permits replacement while preserving access
permissions. Linked replacement destinations are rejected. No backups or locking
are provided; the last successful replacement wins. Interrupted streaming stdout
may be partial. Exit codes: 0 success, 1 operation failure, 2 invalid arguments.";

#[derive(Parser)]
#[command(
    name = "clibox",
    version,
    about = "Portable offline developer utilities",
    after_help = "Use clibox <command> <operation> --help for options and examples.
Diagnostics use stderr, omit sensitive inputs, and honor RUST_LOG and NO_COLOR."
)]
pub struct Cli {
    #[command(subcommand)]
    pub command: Option<Command>,
}

#[derive(Subcommand)]
pub enum Command {
    /// Replace UTF-8 text.
    Text {
        #[command(subcommand)]
        command: TextCommand,
    },
    /// Format instants and perform calendar/elapsed arithmetic.
    Time {
        #[command(subcommand)]
        command: TimeCommand,
    },
    /// Stream binary Base64 transformations without adding newlines.
    Base64 {
        #[command(subcommand)]
        command: Base64Command,
    },
    /// Generate and verify binary checksums.
    Hash {
        #[command(subcommand)]
        command: HashCommand,
    },
}

#[derive(Args, Default)]
pub struct Input {
    /// Read a file, or - for stdin.
    #[arg(long, conflicts_with = "text")]
    pub input: Option<PathBuf>,
    /// Use exact UTF-8 bytes without an added newline.
    #[arg(long, allow_hyphen_values = true)]
    pub text: Option<String>,
}

#[derive(Args, Default)]
pub struct Output {
    /// Publish the result to a file instead of stdout.
    #[arg(long)]
    pub output: Option<PathBuf>,
    /// Allow replacement of an existing output file.
    #[arg(long)]
    pub force: bool,
}

#[derive(Subcommand)]
pub enum TextCommand {
    /// Replace non-overlapping matches; preserves other bytes and line endings.
    #[command(after_help = IO_HELP, after_long_help = "Rust regex syntax: inline flags, $1, $name, braced names, and $$ are supported.
Lookaround and pattern backreferences are not supported. Optional unmatched groups
expand to empty text. Empty patterns and nonexistent capture references are errors.
Text replacement holds the full input and result in memory, without a fixed limit.

Examples (Git Bash):
  clibox text replace old new --text 'old old'
  clibox text replace '(hello)' '$1 world' --regex --text hello
  clibox text replace old new --input 'file with spaces.txt' --in-place

--in-place explicitly authorizes replacement of one regular input file.
Output adds no newline. Use -h for the shared input/output rules.")]
    Replace(TextReplace),
}

#[derive(Args)]
pub struct TextReplace {
    pub pattern: String,
    pub replacement: String,
    #[arg(long)]
    pub regex: bool,
    #[arg(long)]
    pub first: bool,
    #[arg(long)]
    pub require_match: bool,
    #[command(flatten)]
    pub source: Input,
    #[command(flatten)]
    pub destination: Output,
    /// Atomically replace the explicitly selected regular input file.
    #[arg(long, requires = "input", conflicts_with_all = ["output", "text"])]
    pub in_place: bool,
}

#[derive(Subcommand)]
pub enum Base64Command {
    /// Encode one unwrapped stream, without an added newline.
    #[command(after_help = IO_HELP)]
    Encode(Base64Args),
    /// Decode canonical input, ignoring ASCII whitespace only.
    #[command(after_help = IO_HELP)]
    Decode(Base64Args),
}

#[derive(Args)]
pub struct Base64Args {
    #[command(flatten)]
    pub source: Input,
    #[command(flatten)]
    pub destination: Output,
    /// Select the URL-safe alphabet, without autodetection.
    #[arg(long)]
    pub url_safe: bool,
    /// Require unpadded input / produce unpadded output.
    #[arg(long)]
    pub no_padding: bool,
}

#[derive(Clone, Copy, ValueEnum)]
pub enum Algorithm {
    Sha256,
    Sha512,
    Blake3,
}

#[derive(Clone, Copy, ValueEnum)]
pub enum EncodeFormat {
    Hex,
    Base64,
    Checksum,
}

#[derive(Clone, Copy, ValueEnum)]
pub enum VerifyFormat {
    Hex,
    Base64,
}

#[derive(Subcommand)]
pub enum HashCommand {
    /// Hash exact input bytes; append one LF to the encoded result.
    #[command(after_help = IO_HELP)]
    Encode(HashEncode),
    /// Verify a digest or ordered GNU checksum manifest.
    #[command(after_help = IO_HELP, after_long_help = "Examples:
  clibox hash encode --algorithm sha256 --input archive.zip --format checksum
  clibox hash verify --check SHA256SUMS --json

--check resolves relative filenames against the manifest directory (cwd for -).
It accepts GNU untagged text/binary records, including escaped filenames, and
continues after malformed, missing, or mismatched entries. Empty manifests fail.
Reports contain sources and statuses, never input text or expected/actual digests.
A complete failure report is still published to --output, with exit code 1.
--quiet suppresses results and conflicts with --output. JSON contains ordered
results and errors; manifest errors include line numbers when available.
Use -h for shared input/output rules.")]
    Verify(HashVerify),
}

#[derive(Args)]
pub struct HashEncode {
    #[arg(long, value_enum, default_value = "sha256")]
    pub algorithm: Algorithm,
    #[command(flatten)]
    pub source: Input,
    #[arg(long, value_enum, default_value = "hex")]
    pub format: EncodeFormat,
    #[command(flatten)]
    pub destination: Output,
}

#[derive(Args)]
pub struct HashVerify {
    #[arg(required_unless_present = "check", conflicts_with = "check")]
    pub expected: Option<String>,
    #[arg(long, conflicts_with_all = ["expected", "input", "text", "format"])]
    pub check: Option<PathBuf>,
    #[arg(long, value_enum, default_value = "sha256")]
    pub algorithm: Algorithm,
    #[command(flatten)]
    pub source: Input,
    #[arg(long, value_enum)]
    pub format: Option<VerifyFormat>,
    #[arg(long, conflicts_with_all = ["json", "output"])]
    pub quiet: bool,
    #[arg(long)]
    pub json: bool,
    #[command(flatten)]
    pub destination: Output,
}

#[derive(Clone, Copy, ValueEnum, Default)]
pub enum TimeFrom {
    #[default]
    Rfc3339,
    Date,
    UnixS,
    UnixMs,
}

#[derive(Clone, Copy, ValueEnum, Default)]
pub enum TimeTo {
    #[default]
    Rfc3339,
    UnixS,
    UnixMs,
}

const TIME_HELP: &str = "Defaults: current instant captured once, RFC 3339 input/output, UTC.
Integer epochs require --from unix-s or unix-ms; negative values are accepted.
Dates use YYYY-MM-DD at midnight. Custom formats use Chrono strftime syntax and
fixed English/C locale; %Z cannot be parsed. Offset-free input uses --timezone.
Timezone rules are bundled IANA data, updated only through clibox releases.
Years 1-9999 and nanoseconds are supported; leap seconds and ambiguous/nonexistent
local times are rejected. Integer Unix output floors negative fractions.
Output ends with LF. Exit codes: 0 success, 1 runtime failure, 2 invalid arguments.

Examples:
  clibox time format 2026-01-31 --from date --format '%Y/%m/%d'
  clibox time add 2026-01-31 --from date --months 1
  clibox time add --days -1 --timezone America/New_York

Arithmetic combines years/months, clamps month ends, applies calendar weeks/days,
then elapsed hours/minutes/seconds. A day can differ from 24 hours across DST.
At least one unit is required by add; explicit zero is valid.";

#[derive(Subcommand)]
pub enum TimeCommand {
    /// Format an instant using a bundled timezone.
    #[command(after_help = TIME_HELP)]
    Format(TimeArgs),
    /// Apply calendar units followed by elapsed units.
    #[command(after_help = TIME_HELP)]
    Add(TimeAdd),
}

#[derive(Args)]
pub struct TimeArgs {
    #[arg(allow_negative_numbers = true)]
    pub value: Option<String>,
    #[arg(long, value_enum, conflicts_with = "input_format")]
    pub from: Option<TimeFrom>,
    #[arg(long)]
    pub input_format: Option<String>,
    #[arg(long, value_enum, conflicts_with = "format")]
    pub to: Option<TimeTo>,
    #[arg(long)]
    pub format: Option<String>,
    #[arg(long, default_value = "UTC")]
    pub timezone: String,
}

#[derive(Args)]
#[group(id = "units", required = true, multiple = true,
    args = ["years", "months", "weeks", "days", "hours", "minutes", "seconds"])]
pub struct TimeAdd {
    #[command(flatten)]
    pub time: TimeArgs,
    #[arg(long, allow_negative_numbers = true)]
    pub years: Option<i64>,
    #[arg(long, allow_negative_numbers = true)]
    pub months: Option<i64>,
    #[arg(long, allow_negative_numbers = true)]
    pub weeks: Option<i64>,
    #[arg(long, allow_negative_numbers = true)]
    pub days: Option<i64>,
    #[arg(long, allow_negative_numbers = true)]
    pub hours: Option<i64>,
    #[arg(long, allow_negative_numbers = true)]
    pub minutes: Option<i64>,
    #[arg(long, allow_negative_numbers = true)]
    pub seconds: Option<i64>,
}

impl Command {
    pub fn output(&self) -> Result<(Option<PathBuf>, bool)> {
        let output = match self {
            Self::Text {
                command: TextCommand::Replace(args),
            } => {
                if args.in_place {
                    let path = args.source.input.as_ref().unwrap();
                    if path.as_os_str() == "-" {
                        return Err(Error::argument(Code::Arguments));
                    }
                    return Ok((Some(path.clone()), true));
                }
                &args.destination
            }
            Self::Base64 {
                command: Base64Command::Encode(args) | Base64Command::Decode(args),
            } => &args.destination,
            Self::Hash {
                command: HashCommand::Encode(args),
            } => &args.destination,
            Self::Hash {
                command: HashCommand::Verify(args),
            } => &args.destination,
            Self::Time { .. } => return Ok((None, false)),
        };
        Ok((output.output.clone(), output.force))
    }
}
