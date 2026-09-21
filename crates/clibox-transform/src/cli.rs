use std::path::PathBuf;

use clap::{Args, Subcommand, ValueEnum};

use crate::transform_error::{Code, Error, Result as TransformResult};

const IO_HELP: &str = "Input defaults to stdin until EOF; --input - explicitly selects stdin.
--text supplies UTF-8 bytes with no added newline. Explicit input ignores stdin.
Output defaults to stdout; --output - also selects stdout (use ./- for a dash file).
--force requires file output or --in-place. File output is prepared beside its destination and
published only on completion; --force permits replacement while preserving access
permissions. Linked replacement destinations are rejected. No backups or locking
are provided; the last successful replacement wins. Interrupted streaming stdout
may be partial. Exit codes: 0 success, 1 operation failure, 2 invalid arguments,
130 Ctrl+C/Windows Ctrl+Break, 143 Unix SIGTERM.";

#[derive(Subcommand)]
pub enum TransformCommand {
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
    #[arg(long, value_name = "FILE", conflicts_with = "text")]
    pub input: Option<PathBuf>,
    /// Use exact UTF-8 bytes without an added newline.
    #[arg(long, allow_hyphen_values = true)]
    pub text: Option<String>,
}

#[derive(Args, Default)]
pub struct Output {
    /// Publish to a file, or - for stdout (the default).
    #[arg(long, value_name = "FILE")]
    pub output: Option<PathBuf>,
    /// Allow replacing a file; requires file output or --in-place.
    #[arg(long)]
    pub force: bool,
}

#[derive(Subcommand)]
pub enum TextCommand {
    /// Replace non-overlapping matches; preserves other bytes and line endings.
    #[command(after_help = format!("{IO_HELP}\n\nExamples:\n  clibox text replace old new --text 'old old'\n  clibox text replace old new --input notes.txt --in-place"), after_long_help = format!("{IO_HELP}\n\nRust regex syntax: inline flags, $1, $name, braced names, and $$ are supported.
Lookaround and pattern backreferences are not supported. Optional unmatched groups
expand to empty text. Empty patterns and nonexistent capture references are errors.
Text replacement holds the full input and result in memory, without a fixed limit.

Examples (Git Bash):
  clibox text replace old new --text 'old old'
  clibox text replace '(hello)' '$1 world' --regex --text hello
  clibox text replace old new --input 'file with spaces.txt' --in-place

--in-place explicitly authorizes replacement of one regular input file.
Output adds no newline."))]
    Replace(TextReplace),
}

#[derive(Args)]
pub struct TextReplace {
    /// Literal search text, or a Rust regex with --regex.
    pub pattern: String,
    /// Replacement text (capture references require --regex).
    pub replacement: String,
    /// Interpret the pattern and replacement using Rust regex syntax.
    #[arg(long)]
    pub regex: bool,
    /// Replace only the first match.
    #[arg(long)]
    pub first: bool,
    /// Fail if the input contains no match.
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
    #[command(after_help = format!("{IO_HELP}\n\nExample: clibox base64 encode --text hello"))]
    Encode(Base64Args),
    /// Decode canonical input, ignoring ASCII whitespace only.
    #[command(after_help = format!("{IO_HELP}\n\nExample: clibox base64 decode --text aGVsbG8="))]
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
pub enum ComputeFormat {
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
    #[command(after_help = format!("{IO_HELP}\n\nExample: clibox hash compute --input archive.zip --format checksum"), after_long_help = format!("{IO_HELP}\n\nExamples:
  clibox hash compute --input archive.zip --format checksum --output checksums/SHA256SUMS
  clibox hash verify --check checksums/SHA256SUMS

Checksum format requires a file input and uses GNU filename escaping.
With file --output (not -), relative input paths are resolved and recorded relative to the
manifest directory; different Windows volumes use an absolute path. Absolute
inputs and stdout records retain their supplied paths. Use --output when saving
a manifest in another directory; shell redirection cannot rebase its records.
"))]
    Compute(HashCompute),
    /// Verify a digest or ordered GNU checksum manifest.
    #[command(after_help = format!("{IO_HELP}\n\nExample: clibox hash verify --check SHA256SUMS --json\n--quiet suppresses results and conflicts with --json and --output."), after_long_help = format!("{IO_HELP}\n\nExamples:
  clibox hash compute --algorithm sha256 --input archive.zip --format checksum
  clibox hash verify --check SHA256SUMS --json

--check resolves relative filenames against the manifest directory (cwd for -).
It accepts GNU untagged text/binary records, including escaped filenames, and
continues after malformed, missing, or mismatched entries. Empty manifests fail.
Reports contain sources and statuses, never input text or expected/actual digests.
A complete failure report is still published to --output, with exit code 1.
--quiet suppresses results and conflicts with --json and --output. Failures retain a
redacted stderr summary. JSON contains ordered
results and errors; manifest errors include line numbers when available.
"))]
    Verify(HashVerify),
}

#[derive(Args)]
pub struct HashCompute {
    /// Select the checksum algorithm.
    #[arg(long, value_enum, default_value = "sha256")]
    pub algorithm: Algorithm,
    #[command(flatten)]
    pub source: Input,
    /// Select the digest representation.
    #[arg(long, value_enum, default_value = "hex")]
    pub format: ComputeFormat,
    #[command(flatten)]
    pub destination: Output,
}

#[derive(Args)]
pub struct HashVerify {
    /// Expected digest in the selected format.
    #[arg(required_unless_present = "check", conflicts_with = "check")]
    pub expected: Option<String>,
    /// Read a GNU checksum manifest from a file, or - for stdin.
    #[arg(long, value_name = "FILE", conflicts_with_all = ["expected", "input", "text", "format"])]
    pub check: Option<PathBuf>,
    /// Select the checksum algorithm.
    #[arg(long, value_enum, default_value = "sha256")]
    pub algorithm: Algorithm,
    #[command(flatten)]
    pub source: Input,
    /// Select the digest representation.
    #[arg(long, value_enum)]
    pub format: Option<VerifyFormat>,
    /// Suppress stdout results; failure diagnostics remain on stderr.
    #[arg(long, conflicts_with_all = ["json", "output"])]
    pub quiet: bool,
    /// Emit an ordered JSON verification report.
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
No stdin is read; output goes to stdout and ends with LF.
Exit codes: 0 success, 1 runtime failure, 2 invalid arguments,
130 Ctrl+C/Windows Ctrl+Break, 143 Unix SIGTERM.

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
    /// Input instant; omit to capture the current time once.
    #[arg(allow_negative_numbers = true)]
    pub value: Option<String>,
    /// Select the input representation (default: rfc3339).
    #[arg(long, value_enum, conflicts_with = "input_format")]
    pub from: Option<TimeFrom>,
    /// Parse input using a custom strftime format.
    #[arg(long)]
    pub input_format: Option<String>,
    /// Select the output representation (default: rfc3339).
    #[arg(long, value_enum, conflicts_with = "format")]
    pub to: Option<TimeTo>,
    /// Format output using a custom strftime format.
    #[arg(long)]
    pub format: Option<String>,
    /// Interpret offset-free input and format output in this IANA zone.
    #[arg(long, value_name = "ZONE", default_value = "UTC")]
    pub timezone: String,
}

#[derive(Args)]
#[group(id = "units", required = true, multiple = true,
    args = ["years", "months", "weeks", "days", "hours", "minutes", "seconds"])]
pub struct TimeAdd {
    #[command(flatten)]
    pub time: TimeArgs,
    /// Add calendar years.
    #[arg(long, value_name = "N", allow_negative_numbers = true)]
    pub years: Option<i64>,
    /// Add calendar months, clamping the day to the month end.
    #[arg(long, value_name = "N", allow_negative_numbers = true)]
    pub months: Option<i64>,
    /// Add calendar weeks.
    #[arg(long, value_name = "N", allow_negative_numbers = true)]
    pub weeks: Option<i64>,
    /// Add calendar days, which may differ from 24 elapsed hours.
    #[arg(long, value_name = "N", allow_negative_numbers = true)]
    pub days: Option<i64>,
    /// Add elapsed hours.
    #[arg(long, value_name = "N", allow_negative_numbers = true)]
    pub hours: Option<i64>,
    /// Add elapsed minutes.
    #[arg(long, value_name = "N", allow_negative_numbers = true)]
    pub minutes: Option<i64>,
    /// Add elapsed seconds.
    #[arg(long, value_name = "N", allow_negative_numbers = true)]
    pub seconds: Option<i64>,
}

impl TransformCommand {
    pub fn operation(&self) -> &'static str {
        match self {
            Self::Text { .. } => "text-replace",
            Self::Time {
                command: TimeCommand::Format(_),
            } => "time-format",
            Self::Time {
                command: TimeCommand::Add(_),
            } => "time-add",
            Self::Base64 {
                command: Base64Command::Encode(_),
            } => "base64-encode",
            Self::Base64 {
                command: Base64Command::Decode(_),
            } => "base64-decode",
            Self::Hash {
                command: HashCommand::Compute(_),
            } => "hash-compute",
            Self::Hash {
                command: HashCommand::Verify(_),
            } => "hash-verify",
        }
    }

    pub fn output(&self) -> TransformResult<(Option<PathBuf>, bool)> {
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
                command: HashCommand::Compute(args),
            } => &args.destination,
            Self::Hash {
                command: HashCommand::Verify(args),
            } => &args.destination,
            Self::Time { .. } => return Ok((None, false)),
        };
        let path = output.file_path();
        if output.force && path.is_none() {
            return Err(Error::argument(Code::Arguments));
        }
        Ok((path.map(PathBuf::from), output.force))
    }
}

impl Output {
    pub fn file_path(&self) -> Option<&std::path::Path> {
        self.output
            .as_deref()
            .filter(|path| path.as_os_str() != "-")
    }
}
