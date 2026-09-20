mod clipboard;
mod config_runtime;
mod dotenv;
mod environment;
mod error;
mod open;
mod port;
mod publication;
mod runtime;
mod yaml;

use std::{
    ffi::OsString,
    io::IsTerminal,
    path::{Path, PathBuf},
};

use clap::{Args, CommandFactory, Parser, Subcommand};
use config_runtime::{Cancellation, Error, Failure, Result, LIMIT};

const IO_HELP: &str =
    "Inputs must be UTF-8 (initial BOM accepted), without NUL. Aggregate raw input and serialized \
     output are independently limited to 64 MiB. Relative paths use the current directory. \
     --input - selects stdin; explicit files do not consume stdin. Results default to stdout. \
     --output is always a filesystem path, including '-'. Empty/comment-only input produces zero \
     bytes; nonempty results end in LF. All input is validated before any result is emitted. A \
     stdout write failure or interruption may leave partial output.\n\nFile output uses a private \
     temporary file in the destination directory and is published only on success. New Unix files \
     use mode 0600; Windows files inherit the parent ACL. Replacements preserve access \
     permissions and reject symbolic links or multiple hard links. Existing output requires \
     --force (except --in-place). No backups, locks, or concurrent-change detection; the last \
     successful replacement wins. Handled cancellation cleans unpublished temporaries and never \
     undoes completed writes. No persistent state, shell execution, expansion, or network \
     access.\n\nExit codes: 0 success, 1 content/filesystem/limit/runtime failure, 2 invalid \
     arguments, 130 Ctrl+C, 143 Unix SIGTERM. Diagnostics are redacted structured stderr events: \
     operation, input/document ordinal and line/column, never content, keys, values, paths or \
     argv. Warnings/errors are enabled by default; RUST_LOG=clibox=debug adds operation detail. \
     Use the reported position to inspect input locally. Diagnostic color requires a terminal and \
     honors NO_COLOR.";
const DOTENV_HELP: &str =
    "Node.js dotenv baseline: ASCII keys [A-Za-z_][A-Za-z0-9_]*, optional export, whitespace, \
     comments, empty values and multiline single/double quotes. Every record is validated; \
     invalid keys, missing assignments, unterminated quotes and trailing content after a quote \
     fail. Keys are case-sensitive and sorted lexically. Last assignment in each file wins. Value \
     tokens retain original quotes, escapes, literal $VAR/${VAR}/command substitutions and quoted \
     internal line endings; values are never expanded or executed.";
const YAML_HELP: &str =
    "YAML 1.2 Core: null, booleans, arbitrary-precision numeric values, strings, sequences and \
     string-key mappings. Anchors are document-local. Aliases expand; shallow merges give \
     explicit keys priority, then earlier mappings in a merge sequence. Quoted or !!str-tagged << \
     keys remain ordinary keys. Reject duplicate ordinary keys, invalid merge operands, \
     unresolved/cyclic aliases, non-string keys, unsupported tags and version directives other \
     than 1.2.\n\nMappings sort recursively by Unicode scalar value; scalar \
     values/types/precision, sequence order and document order are preserved. Comments/anchors \
     are removed. Output uses deterministic two-space block indentation and quotes/escapes \
     strings. One document has no leading marker; multiple documents each begin with ---. \
     Explicit empty documents normalize to null. Output is byte-idempotent. Collection nesting, \
     including aliases, is limited to 128 levels with a root collection at level one.";

#[derive(Parser)]
#[command(
    name = "clibox",
    version,
    about = "Cross-platform developer utilities distributed through Cargo and npm"
)]
struct Cli {
    #[command(subcommand)]
    command: Option<Command>,
}
#[derive(Subcommand)]
enum Command {
    #[command(flatten)]
    Configuration(Configuration),
    /// Run commands with a child-only environment.
    Run {
        #[command(subcommand)]
        command: Run,
    },
    /// Inspect or forcibly terminate local port owners.
    Port {
        #[command(subcommand)]
        command: port::Action,
    },
    /// Open one file, directory or registered URI.
    #[command(
        after_help = "Examples:\n  clibox open .\n  clibox open https://example.com\n  clibox \
                      open report.txt --app TextEdit --wait\n\n--wait observes application \
                      termination, not document or tab closure."
    )]
    Open {
        target: OsString,
        #[arg(long)]
        app: Option<OsString>,
        #[arg(long, requires = "app")]
        wait: bool,
    },
    /// Copy or paste the desktop session's ordinary text clipboard.
    Clipboard {
        #[command(subcommand)]
        command: clipboard::Action,
    },
}

#[derive(Subcommand)]
enum Run {
    /// Set cross-env compatible assignments and wait for a child command.
    #[command(
        after_help = "Examples:\n  clibox run env NODE_ENV=production node build.js\n  clibox run \
                      env -- node script.js\n\nInherits cwd and stdio. No shell expressions. \
                      Empty child arguments and exit signals are preserved."
    )]
    Env {
        #[arg(value_name = "KEY=VALUE ... COMMAND ARG", trailing_var_arg = true, allow_hyphen_values = true, num_args = 1..)]
        args: Vec<OsString>,
    },
}

#[derive(Subcommand)]
enum Configuration {
    /// Inspect and combine dotenv files without loading the environment.
    Dotenv {
        #[command(subcommand)]
        command: Dotenv,
    },
    /// Work with YAML 1.2 configuration files.
    Yaml {
        #[command(subcommand)]
        command: Yaml,
    },
}
#[derive(Args)]
struct FileOutput {
    /// Write only to this filesystem destination (existing files require
    /// --force).
    #[arg(long, value_name = "FILE")]
    output: Option<PathBuf>,
    /// Authorize replacing an existing file output.
    #[arg(long)]
    force: bool,
}
#[derive(Subcommand)]
enum Dotenv {
    /// List unique keys, never values; defaults to .env in the current
    /// directory only.
    #[command(after_long_help = format!("{DOTENV_HELP}\n\n{IO_HELP}\n\nExamples:\n  clibox dotenv list\n  clibox dotenv list --input -\n  clibox dotenv list --input local.env --output keys.txt"))]
    List {
        /// Input file, or - for stdin; no parent-directory or related-file
        /// discovery.
        #[arg(long, value_name = "FILE", default_value = ".env")]
        input: PathBuf,
        #[command(flatten)]
        output: FileOutput,
    },
    /// Merge one or more inputs; later files win, including explicit empty
    /// values.
    #[command(after_long_help = format!("{DOTENV_HELP}\n\nEmit sorted KEY=<winning value token> records, removing export, assignment whitespace, comments outside values and blank lines. Validate even overwritten records. Read all inputs before publishing. Input files are unchanged unless an authorized destination explicitly names one.\n\n{IO_HELP}\n\nExamples:\n  clibox dotenv merge base.env local.env\n  clibox dotenv merge base.env - --output merged.env\n  clibox dotenv merge local.env --output local.env --force"))]
    Merge {
        /// Ordered input files; - may appear once for stdin at that position.
        #[arg(required = true, num_args = 1.., value_name = "FILE")]
        files: Vec<PathBuf>,
        #[command(flatten)]
        output: FileOutput,
    },
}
#[derive(Subcommand)]
enum Yaml {
    /// Resolve YAML references and sort mappings; defaults to stdin.
    #[command(after_long_help = format!("{YAML_HELP}\n\n{IO_HELP}\n\nExamples:\n  clibox yaml normalize < config.yaml\n  clibox yaml normalize --input config.yaml --output normalized.yaml\n  clibox yaml normalize --input config.yaml --in-place"))]
    Normalize {
        /// Input file, or - for stdin; omitted input also reads stdin.
        #[arg(long, value_name = "FILE")]
        input: Option<PathBuf>,
        /// Replace the explicitly selected regular input file; --force is
        /// unnecessary.
        #[arg(long, conflicts_with = "output")]
        in_place: bool,
        #[command(flatten)]
        output: FileOutput,
    },
}
#[derive(Clone, Copy)]
enum Operation {
    DotenvList,
    DotenvMerge,
    YamlNormalize,
}
impl Operation {
    fn name(self) -> &'static str {
        match self {
            Self::DotenvList => "dotenv_list",
            Self::DotenvMerge => "dotenv_merge",
            Self::YamlNormalize => "yaml_normalize",
        }
    }
}
struct Job {
    operation: Operation,
    inputs: Vec<PathBuf>,
    output: Option<PathBuf>,
    replace: bool,
    in_place: bool,
}
impl Job {
    fn new(command: Configuration) -> Result<Self> {
        let (operation, inputs, output, in_place) = match command {
            Configuration::Dotenv {
                command: Dotenv::List { input, output },
            } => (Operation::DotenvList, vec![input], output, false),
            Configuration::Dotenv {
                command: Dotenv::Merge { files, output },
            } => (Operation::DotenvMerge, files, output, false),
            Configuration::Yaml {
                command:
                    Yaml::Normalize {
                        input,
                        output,
                        in_place,
                    },
            } => {
                if in_place && (input.is_none() || input.as_deref() == Some(Path::new("-"))) {
                    return Err(Failure::Arguments.into());
                }
                (
                    Operation::YamlNormalize,
                    vec![input.unwrap_or_else(|| "-".into())],
                    output,
                    in_place,
                )
            }
        };
        if inputs.iter().filter(|p| p.as_os_str() == "-").count() > 1
            || (output.force && output.output.is_none() && !in_place)
        {
            return Err(Failure::Arguments.into());
        }
        let destination = if in_place {
            Some(inputs[0].clone())
        } else {
            output.output
        };
        Ok(Self {
            operation,
            inputs,
            output: destination,
            replace: output.force || in_place,
            in_place,
        })
    }

    fn run(self, cancel: &Cancellation) -> Result<()> {
        tracing::debug!(operation = self.operation.name(), "operation_started");
        if self.in_place {
            publication::regular_input(&self.inputs[0])?;
        }
        let mut remaining = LIMIT;
        let mut values = dotenv::Values::new();
        let mut result = Vec::new();
        for (index, path) in self.inputs.into_iter().enumerate() {
            tracing::debug!(
                operation = self.operation.name(),
                input = index + 1,
                "input_started"
            );
            let token = cancel.clone();
            let bytes = cancel
                .blocking(move || {
                    if path.as_os_str() == "-" {
                        config_runtime::read(std::io::stdin().lock(), remaining, &token)
                    } else {
                        config_runtime::read(
                            std::fs::File::open(path).map_err(|_| Failure::Read)?,
                            remaining,
                            &token,
                        )
                    }
                })
                .map_err(|e| e.input(index + 1))?;
            remaining -= bytes.len();
            let text = config_runtime::decode(bytes).map_err(|e| e.input(index + 1))?;
            match self.operation {
                Operation::DotenvList | Operation::DotenvMerge => {
                    dotenv::parse(&text, &mut values, cancel)
                }
                Operation::YamlNormalize => yaml::normalize(&text, cancel).map(|bytes| {
                    result = bytes;
                }),
            }
            .map_err(|e| e.input(index + 1))?;
        }
        if !matches!(self.operation, Operation::YamlNormalize) {
            result = dotenv::render(
                values,
                matches!(self.operation, Operation::DotenvMerge),
                cancel,
            )?;
        }
        cancel.check()?;
        if let Some(path) = self.output {
            publication::publish(&path, &result, self.replace, cancel)?;
        } else {
            let token = cancel.clone();
            cancel.blocking(move || {
                config_runtime::write(std::io::stdout().lock(), &result, &token)
            })?;
        }
        cancel.check()?;
        tracing::debug!(operation = self.operation.name(), "operation_completed");
        Ok(())
    }
}

fn main() {
    // Do not install dependency logging targets or interpolate filter errors:
    // RUST_LOG itself may contain sensitive material. No raw clap errors either.
    let filter =
        tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "warn".into());
    tracing_subscriber::fmt()
        // The subscriber's fallback uses eprintln!, which panics on closed
        // stderr and can recursively panic through our redacted panic hook.
        // Failed diagnostics must not change command or cancellation status.
        .log_internal_errors(false)
        .with_env_filter(filter)
        .with_writer(std::io::stderr)
        .with_ansi(std::io::stderr().is_terminal() && std::env::var_os("NO_COLOR").is_none())
        .without_time()
        .with_target(false)
        .init();
    // Dependency panics can contain input slices; replace the panic payload and
    // location with a stable classification. Normal errors never panic.
    std::panic::set_hook(Box::new(|_| {
        Error::from(Failure::Internal).report("runtime")
    }));
    let raw: Vec<_> = std::env::args_os().collect();
    // Clap consumes this separator, but run env must distinguish it from an
    // assignment token and preserve the child command boundary.
    let leading_separator = raw.get(1).is_some_and(|s| s == "run")
        && raw.get(2).is_some_and(|s| s == "env")
        && raw.get(3).is_some_and(|s| s == "--");
    let cli = match Cli::try_parse_from(raw) {
        Ok(cli) => cli,
        Err(error)
            if matches!(
                error.kind(),
                clap::error::ErrorKind::DisplayHelp | clap::error::ErrorKind::DisplayVersion
            ) =>
        {
            std::process::exit(if error.print().is_ok() { 0 } else { 1 });
        }
        Err(_) => {
            Error::from(Failure::Arguments).report("arguments");
            std::process::exit(2);
        }
    };
    let Some(command) = cli.command else {
        let code = if Cli::command().print_help().is_ok() {
            if std::io::Write::write_all(&mut std::io::stdout(), b"\n").is_ok() {
                0
            } else {
                1
            }
        } else {
            1
        };
        std::process::exit(code);
    };
    match command {
        Command::Configuration(command) => execute_configuration(command),
        Command::Run {
            command: Run::Env { mut args },
        } => execute_utility(|| {
            if leading_separator {
                args.insert(0, "--".into());
            }
            runtime::exit_child(environment::execute(args)?);
        }),
        Command::Port { command } => execute_utility(|| port::execute(command)),
        Command::Open { target, app, wait } => execute_utility(|| {
            open::execute(target, app, wait)?;
            Ok(0)
        }),
        Command::Clipboard { command } => execute_utility(|| {
            clipboard::execute(command)?;
            Ok(0)
        }),
    }
}

fn execute_utility(work: impl FnOnce() -> error::Result<i32>) -> ! {
    let result = runtime::install_signals().and_then(|()| work());
    let code = match result {
        Ok(code) => code,
        Err(error) => {
            error.report("clibox");
            if error.code == error::Code::InvalidInput {
                2
            } else {
                1
            }
        }
    };
    runtime::finish(code);
}

fn execute_configuration(command: Configuration) -> ! {
    // Configuration cancellation returns a numeric status after temporary-file
    // cleanup; utility commands instead retain OS/child signal semantics. Install
    // only the selected command family's handlers for each process.
    let job = match Job::new(command) {
        Ok(job) => job,
        Err(error) => {
            error.report("arguments");
            std::process::exit(2);
        }
    };
    let operation = job.operation.name();
    let cancel = match Cancellation::install() {
        Ok(cancel) => cancel,
        Err(error) => {
            error.report(operation);
            std::process::exit(1);
        }
    };
    let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| job.run(&cancel)))
        .unwrap_or_else(|_| Err(Failure::Internal.into()));
    let code = match result {
        Ok(()) => 0,
        Err(error) => {
            error.report(operation);
            if error.kind == Failure::Cancelled {
                cancel.code() as i32
            } else {
                1
            }
        }
    };
    // Explicit exit does not wait for a blocked stdin/stdout worker. Publication
    // locals have already dropped, so no unpublished file is abandoned.
    std::process::exit(code);
}
