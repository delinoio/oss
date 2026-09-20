use std::{io::IsTerminal, net::Ipv6Addr, path::PathBuf, time::Duration};

use clap::{builder::TypedValueParser, Args, Parser, Subcommand, ValueEnum};
use reqwest::Url;

use crate::transform_error::{Code, Error, Result as TransformResult};

const IO_HELP: &str = "Input defaults to stdin until EOF; --input - explicitly selects stdin.
--text supplies UTF-8 bytes with no added newline. Explicit input ignores stdin.
Output defaults to stdout. File output is prepared beside its destination and
published only on completion; --force permits replacement while preserving access
permissions. Linked replacement destinations are rejected. No backups or locking
are provided; the last successful replacement wins. Interrupted streaming stdout
may be partial. Exit codes: 0 success, 1 operation failure, 2 invalid arguments.";

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
    about = "Cross-platform developer utilities distributed through Cargo and npm",
    after_help = "Use clibox <command> <operation> --help for options and examples.
Diagnostics use stderr, omit sensitive inputs, and honor RUST_LOG and NO_COLOR.
Waits observe readiness without reserving a resource or guaranteeing continued readiness.
Wait exit codes: 0 ready, 1 failure/timeout, 2 invalid input, 130 Ctrl+C, 143 Unix SIGTERM."
)]
pub struct Cli {
    #[command(subcommand)]
    pub command: Option<Command>,
}

#[derive(Subcommand)]
pub enum Command {
    #[command(flatten)]
    System(crate::system::Action),
    #[command(flatten)]
    Transform(TransformCommand),
    /// Wait for one resource to become ready (no stdin or subsequent command).
    #[command(subcommand)]
    Wait(Wait),
}

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
    #[command(after_help = IO_HELP, after_long_help = "Examples:
  clibox hash encode --input archive.zip --format checksum --output checksums/SHA256SUMS
  clibox hash verify --check checksums/SHA256SUMS

Checksum format requires a file input and uses GNU filename escaping.
With --output, relative input paths are resolved and recorded relative to the
manifest directory; different Windows volumes use an absolute path. Absolute
inputs and stdout records retain their supplied paths. Use --output when saving
a manifest in another directory; shell redirection cannot rebase its records.
Use -h for shared input/output rules.")]
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

impl TransformCommand {
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

#[derive(Subcommand)]
pub enum Wait {
    /// Observe TCP connectivity, without sending application data.
    #[command(
        after_help = "Example: clibox wait tcp localhost:3000 --timeout 30s\nConnectivity does \
                      not establish application readiness."
    )]
    Tcp {
        #[arg(value_name = "HOST:PORT", value_parser = parse_tcp)]
        target: TcpTarget,
        #[command(flatten)]
        options: Options,
        #[command(flatten)]
        network: NetworkOptions,
    },
    /// Observe HTTP response headers, without following redirects or reading a
    /// body.
    #[command(
        after_help = "Example: clibox wait http https://example.com/health --method head --status \
                      204 --timeout 1m\nDefault: GET and any 2xx. HTTPS verifies OS trust and \
                      hostname.\nNo proxies, authentication, custom headers/CA, client \
                      certificates, or TLS bypass."
    )]
    Http {
        #[arg(value_name = "URL", value_parser = parse_url)]
        target: Url,
        #[arg(long, value_enum, default_value = "get")]
        method: Method,
        #[arg(long, value_parser = parse_status, help = "Exact final response code (200-599); default: any 2xx")]
        status: Option<u16>,
        #[command(flatten)]
        options: Options,
        #[command(flatten)]
        network: NetworkOptions,
    },
    /// Observe a regular file (including empty files); follow symbolic links.
    #[command(
        after_help = "Example: clibox wait file \"build/ready file\" --timeout 2m \
                      --json\nRelative paths use the current directory. Missing paths/dangling \
                      links retry;\nother file types and filesystem errors fail. Existence does \
                      not mean writing finished."
    )]
    File {
        #[arg(value_name = "PATH", value_parser = clap::builder::OsStringValueParser::new().try_map(|value| parse_path(&value)))]
        target: PathBuf,
        #[command(flatten)]
        options: Options,
    },
}

#[derive(Args, Clone)]
pub struct Options {
    /// Overall deadline; default: unlimited. Use integer ms/s/m/h, or bare 0
    /// for unlimited.
    #[arg(long, value_name = "DURATION", value_parser = parse_timeout)]
    pub timeout: Option<Duration>,
    /// Delay after each unsuccessful attempt completes (positive integer
    /// ms/s/m/h).
    #[arg(long, default_value = "250ms", value_name = "DURATION", value_parser = parse_positive)]
    pub interval: Duration,
    /// Suppress stdout results; failure diagnostics remain on stderr.
    #[arg(long, conflicts_with = "json")]
    pub quiet: bool,
    /// Emit exactly one final JSON result, including handled failure or
    /// cancellation.
    #[arg(long)]
    pub json: bool,
}

#[derive(Args)]
pub struct NetworkOptions {
    /// Budget for DNS, connection, TLS and headers, clipped to the overall
    /// deadline.
    #[arg(long, default_value = "3s", value_name = "DURATION", value_parser = parse_positive)]
    pub attempt_timeout: Duration,
}

#[derive(Clone, Copy, ValueEnum)]
pub enum Method {
    Get,
    Head,
}

// Targets intentionally have no Debug implementation: neither parser nor
// tracing diagnostics may accidentally format user-controlled locators.
#[derive(Clone)]
pub struct TcpTarget {
    pub host: String,
    pub port: u16,
}

fn duration(value: &str, unlimited: bool) -> Result<Duration, &'static str> {
    if unlimited && value == "0" {
        return Ok(Duration::ZERO);
    }
    let (digits, scale) = [("ms", 1u64), ("s", 1000), ("m", 60_000), ("h", 3_600_000)]
        .into_iter()
        .find_map(|(suffix, scale)| value.strip_suffix(suffix).map(|digits| (digits, scale)))
        .ok_or("Use an integer duration with ms, s, m, or h.")?;
    if digits.is_empty() || !digits.bytes().all(|c| c.is_ascii_digit()) {
        return Err("Use a nonnegative integer duration.");
    }
    let millis = digits
        .parse::<u64>()
        .ok()
        .and_then(|n| n.checked_mul(scale))
        .ok_or("Duration is too large.")?;
    let result = Duration::from_millis(millis);
    // Tokio/OS instants must also represent the deadline without panicking.
    if std::time::Instant::now().checked_add(result).is_none() {
        return Err("Duration is too large.");
    }
    if millis == 0 && !unlimited {
        return Err("Interval and attempt timeout must be positive.");
    }
    Ok(result)
}

fn parse_timeout(value: &str) -> Result<Duration, &'static str> {
    duration(value, true)
}
fn parse_positive(value: &str) -> Result<Duration, &'static str> {
    duration(value, false)
}

pub fn parse_tcp(value: &str) -> Result<TcpTarget, &'static str> {
    const ERROR: &str = "Use a DNS name, IPv4 address, or bracketed IPv6 address with a decimal \
                         port from 1 to 65535.";
    let (host, port) = if let Some(rest) = value.strip_prefix('[') {
        let (host, port) = rest.split_once("]:").ok_or(ERROR)?;
        host.parse::<Ipv6Addr>().map_err(|_| ERROR)?;
        (host, port)
    } else {
        let (host, port) = value.split_once(':').ok_or(ERROR)?;
        if host.is_empty() || host.len() > 253 || host.contains(':') {
            return Err(ERROR);
        }
        if host.bytes().all(|b| b.is_ascii_digit() || b == b'.') {
            host.parse::<std::net::Ipv4Addr>().map_err(|_| ERROR)?;
        } else if !host
            .strip_suffix('.')
            .unwrap_or(host)
            .split('.')
            .all(|label| {
                !label.is_empty()
                    && label.len() <= 63
                    && !label.starts_with('-')
                    && !label.ends_with('-')
                    && label
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
            })
        {
            return Err(ERROR);
        }
        (host, port)
    };
    if port.is_empty() || !port.bytes().all(|b| b.is_ascii_digit()) {
        return Err(ERROR);
    }
    let port = port.parse::<u16>().ok().filter(|p| *p != 0).ok_or(ERROR)?;
    Ok(TcpTarget {
        host: host.to_owned(),
        port,
    })
}

fn parse_url(value: &str) -> Result<Url, &'static str> {
    const ERROR: &str = "Use an absolute http or https URL without user information and with a \
                         valid host and port.";
    // Inspect raw authority first: WHATWG parsing normalizes empty userinfo,
    // backslashes and whitespace that must never become implicit credentials.
    let (scheme, rest) = value.split_once("://").ok_or(ERROR)?;
    if !scheme.eq_ignore_ascii_case("http") && !scheme.eq_ignore_ascii_case("https") {
        return Err(ERROR);
    }
    let authority = rest.split(['/', '?', '#']).next().ok_or(ERROR)?;
    if authority.is_empty()
        || authority.contains('@')
        || value.contains('\\')
        || value.chars().any(char::is_whitespace)
        || value.chars().any(char::is_control)
    {
        return Err(ERROR);
    }
    if authority.ends_with(':') {
        return Err(ERROR);
    }
    let url = Url::parse(value).map_err(|_| ERROR)?;
    if url.host().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.port_or_known_default() == Some(0)
    {
        return Err(ERROR);
    }
    Ok(url)
}

fn parse_status(value: &str) -> Result<u16, &'static str> {
    if value.len() == 3 && value.bytes().all(|c| c.is_ascii_digit()) {
        if let Ok(code @ 200..=599) = value.parse() {
            return Ok(code);
        }
    }
    Err("Select an exact HTTP status from 200 to 599.")
}

fn parse_path(value: &std::ffi::OsStr) -> Result<PathBuf, &'static str> {
    if value.is_empty() || value.as_encoded_bytes().contains(&0) {
        return Err("Provide one nonempty file path without NUL bytes.");
    }
    Ok(PathBuf::from(value))
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
