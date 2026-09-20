use std::{io::IsTerminal, net::Ipv6Addr, path::PathBuf, time::Duration};

use clap::{builder::TypedValueParser, Args, Parser, Subcommand, ValueEnum};
use reqwest::Url;

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
    about = "Portable developer utilities",
    disable_help_subcommand = true,
    after_help = "Waits observe one target without reserving it or guaranteeing continued \
                  readiness.\nExit codes: 0 ready, 1 failure/timeout, 2 invalid input, 130 \
                  Ctrl+C, 143 Unix SIGTERM.\nRUST_LOG=clibox=debug enables redacted stderr \
                  diagnostics. NO_COLOR disables color."
)]
pub struct Cli {
    #[command(subcommand)]
    pub command: Option<Command>,
}

#[derive(Subcommand)]
pub enum Command {
    /// Wait for one resource to become ready (no stdin or subsequent command).
    #[command(subcommand)]
    Wait(Wait),
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

/// Never render clap errors: they may embed argv, credentials, and suggestions
/// derived from a secret value. Only static, actionable guidance crosses
/// stderr.
pub fn parser_message(kind: clap::error::ErrorKind) -> &'static str {
    use clap::error::ErrorKind;
    match kind {
        ErrorKind::ArgumentConflict => {
            "Conflicting options; --quiet and --json cannot be combined."
        }
        ErrorKind::MissingRequiredArgument | ErrorKind::MissingSubcommand => {
            "Select wait tcp, http, or file and provide exactly one target. See clibox wait --help."
        }
        ErrorKind::ValueValidation | ErrorKind::InvalidValue => {
            "Invalid value. Check target syntax, get/head method, status 200-599, and integer \
             ms/s/m/h durations; interval and attempt timeout must be positive. See command --help."
        }
        _ => {
            "Unexpected or malformed argument. Provide exactly one target and supported options; \
             see clibox --help."
        }
    }
}
