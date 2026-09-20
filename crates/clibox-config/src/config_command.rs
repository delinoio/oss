use std::path::{Path, PathBuf};

use clap::{Args, Subcommand};

use crate::{
    config_publication,
    config_runtime::{self, Cancellation, Failure, Result, LIMIT},
    dotenv, yaml,
};

const IO_HELP: &str =
    "Inputs must be UTF-8 (initial BOM accepted), without NUL. Aggregate raw input and serialized \
     output are independently limited to 64 MiB. Relative paths use the current directory. \
     --input - selects stdin; explicit files do not consume stdin. Results default to stdout. \
     --output is always a filesystem path, including '-'. Empty/comment-only input produces zero \
     bytes; nonempty results end in LF. All input is validated before any result is emitted. A \
     stdout write failure or interruption may leave partial output.\n\nFile output uses a private \
     temporary file on the destination filesystem (inside a private directory on Unix) and is \
     published only on success. New Unix files use mode 0600; Windows files inherit the parent \
     ACL. Replacements preserve access permissions and reject symbolic links or multiple hard \
     links. Existing output requires --force (except --in-place). No backups, locks, or \
     concurrent-change detection; the last successful replacement wins. Windows sharing rules may \
     reject overlapping replacements; retry after competing handles close. Handled cancellation \
     cleans unpublished temporaries and never undoes completed writes. No persistent state, shell \
     execution, expansion, or network access.\n\nExit codes: 0 success, 1 \
     content/filesystem/limit/runtime failure, 2 invalid arguments, 130 Ctrl+C, 143 Unix SIGTERM. \
     Diagnostics are redacted structured stderr events: operation, input/document ordinal and \
     line/column, never content, keys, values, paths or argv. Warnings/errors are enabled by \
     default; RUST_LOG=clibox=debug adds operation detail. Use the reported position to inspect \
     input locally. Diagnostic color requires a terminal and honors NO_COLOR.";
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

#[derive(Subcommand)]
pub enum Configuration {
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
pub struct FileOutput {
    /// Write only to this filesystem destination (existing files require
    /// --force).
    #[arg(long, value_name = "FILE")]
    output: Option<PathBuf>,
    /// Authorize replacing an existing file output.
    #[arg(long)]
    force: bool,
}
#[derive(Subcommand)]
pub enum Dotenv {
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
pub enum Yaml {
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
        let in_place = self.in_place;
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
                        let file = if in_place {
                            config_publication::regular_input(&path)?
                        } else {
                            std::fs::File::open(path).map_err(|_| Failure::Read)?
                        };
                        config_runtime::read(file, remaining, &token)
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
            config_publication::publish(&path, &result, self.replace, cancel)?;
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

pub fn execute(command: Configuration) -> ! {
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
