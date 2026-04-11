use std::{env, ffi::OsString, fs, path::Path};

use crate::{
    error::Result,
    parser::{ParsedShellExpression, ShellRedirect, ShellRedirectOperator},
    snapshot::{absolutize, PathSnapshotMode, WatchInput, WatchInputKind},
};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ExplicitCommandHandler {
    EnvWrapper,
    NiceWrapper,
    NohupWrapper,
    StdbufWrapper,
    TimeoutWrapper,
    CopyLike,
    MoveLike,
    Install,
    LinkLike,
    RemoveLike,
    Sort,
    Uniq,
    Split,
    Csplit,
    Tee,
    Grep,
    Ripgrep,
    SilverSearcher,
    Sed,
    Awk,
    Find,
    LsLike,
    Fd,
    Xargs,
    Tar,
    Touch,
    Truncate,
    ChangeAttributes,
    Dd,
    Protoc,
    Flatc,
    Thrift,
    Capnp,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum HelpInventoryGroup {
    Wrapper,
    DedicatedBuiltIn,
}

#[derive(Debug, Clone, Copy)]
struct ExplicitCommandSpec {
    aliases: &'static [&'static str],
    handler: ExplicitCommandHandler,
    help_group: HelpInventoryGroup,
    safe_current_dir_default: bool,
}

impl ExplicitCommandSpec {
    const fn wrapper(aliases: &'static [&'static str], handler: ExplicitCommandHandler) -> Self {
        Self {
            aliases,
            handler,
            help_group: HelpInventoryGroup::Wrapper,
            safe_current_dir_default: false,
        }
    }

    const fn dedicated(aliases: &'static [&'static str], handler: ExplicitCommandHandler) -> Self {
        Self {
            aliases,
            handler,
            help_group: HelpInventoryGroup::DedicatedBuiltIn,
            safe_current_dir_default: false,
        }
    }

    const fn dedicated_with_safe_current_dir_default(
        aliases: &'static [&'static str],
        handler: ExplicitCommandHandler,
    ) -> Self {
        Self {
            aliases,
            handler,
            help_group: HelpInventoryGroup::DedicatedBuiltIn,
            safe_current_dir_default: true,
        }
    }
}

const EXPLICIT_COMMAND_SPECS: &[ExplicitCommandSpec] = &[
    ExplicitCommandSpec::wrapper(&["env"], ExplicitCommandHandler::EnvWrapper),
    ExplicitCommandSpec::wrapper(&["nice"], ExplicitCommandHandler::NiceWrapper),
    ExplicitCommandSpec::wrapper(&["nohup"], ExplicitCommandHandler::NohupWrapper),
    ExplicitCommandSpec::wrapper(&["stdbuf"], ExplicitCommandHandler::StdbufWrapper),
    ExplicitCommandSpec::wrapper(&["timeout"], ExplicitCommandHandler::TimeoutWrapper),
    ExplicitCommandSpec::dedicated(&["cp"], ExplicitCommandHandler::CopyLike),
    ExplicitCommandSpec::dedicated(&["mv"], ExplicitCommandHandler::MoveLike),
    ExplicitCommandSpec::dedicated(&["install"], ExplicitCommandHandler::Install),
    ExplicitCommandSpec::dedicated(&["ln", "link"], ExplicitCommandHandler::LinkLike),
    ExplicitCommandSpec::dedicated(
        &["rm", "unlink", "rmdir", "shred"],
        ExplicitCommandHandler::RemoveLike,
    ),
    ExplicitCommandSpec::dedicated(&["sort"], ExplicitCommandHandler::Sort),
    ExplicitCommandSpec::dedicated(&["uniq"], ExplicitCommandHandler::Uniq),
    ExplicitCommandSpec::dedicated(&["split"], ExplicitCommandHandler::Split),
    ExplicitCommandSpec::dedicated(&["csplit"], ExplicitCommandHandler::Csplit),
    ExplicitCommandSpec::dedicated(&["tee"], ExplicitCommandHandler::Tee),
    ExplicitCommandSpec::dedicated(&["grep", "egrep", "fgrep"], ExplicitCommandHandler::Grep),
    ExplicitCommandSpec::dedicated(&["rg"], ExplicitCommandHandler::Ripgrep),
    ExplicitCommandSpec::dedicated(&["ag"], ExplicitCommandHandler::SilverSearcher),
    ExplicitCommandSpec::dedicated(&["sed"], ExplicitCommandHandler::Sed),
    ExplicitCommandSpec::dedicated(
        &["awk", "gawk", "mawk", "nawk"],
        ExplicitCommandHandler::Awk,
    ),
    ExplicitCommandSpec::dedicated_with_safe_current_dir_default(
        &["find"],
        ExplicitCommandHandler::Find,
    ),
    ExplicitCommandSpec::dedicated_with_safe_current_dir_default(
        &["ls", "dir", "vdir"],
        ExplicitCommandHandler::LsLike,
    ),
    ExplicitCommandSpec::dedicated(&["fd"], ExplicitCommandHandler::Fd),
    ExplicitCommandSpec::dedicated(&["xargs"], ExplicitCommandHandler::Xargs),
    ExplicitCommandSpec::dedicated(&["tar"], ExplicitCommandHandler::Tar),
    ExplicitCommandSpec::dedicated(&["touch"], ExplicitCommandHandler::Touch),
    ExplicitCommandSpec::dedicated(&["truncate"], ExplicitCommandHandler::Truncate),
    ExplicitCommandSpec::dedicated(
        &["chmod", "chown", "chgrp"],
        ExplicitCommandHandler::ChangeAttributes,
    ),
    ExplicitCommandSpec::dedicated(&["dd"], ExplicitCommandHandler::Dd),
    ExplicitCommandSpec::dedicated(&["protoc"], ExplicitCommandHandler::Protoc),
    ExplicitCommandSpec::dedicated(&["flatc"], ExplicitCommandHandler::Flatc),
    ExplicitCommandSpec::dedicated(&["thrift"], ExplicitCommandHandler::Thrift),
    ExplicitCommandSpec::dedicated(&["capnp"], ExplicitCommandHandler::Capnp),
];

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CommandAdapterId {
    WrapperEnv,
    WrapperNice,
    WrapperNohup,
    WrapperStdbuf,
    WrapperTimeout,
    CopyLike,
    MoveLike,
    Install,
    LinkLike,
    RemoveLike,
    ReadPaths,
    DefaultCurrentDir,
    Grep,
    Ripgrep,
    SilverSearcher,
    Sed,
    Awk,
    Find,
    Fd,
    Xargs,
    Tar,
    Sort,
    Uniq,
    Split,
    Csplit,
    Tee,
    Touch,
    Truncate,
    ChangeAttributes,
    Dd,
    Protoc,
    Flatc,
    Thrift,
    CapnpCompile,
    NonWatchable,
    Fallback,
}

impl CommandAdapterId {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::WrapperEnv => "wrapper-env",
            Self::WrapperNice => "wrapper-nice",
            Self::WrapperNohup => "wrapper-nohup",
            Self::WrapperStdbuf => "wrapper-stdbuf",
            Self::WrapperTimeout => "wrapper-timeout",
            Self::CopyLike => "copy-like",
            Self::MoveLike => "move-like",
            Self::Install => "install",
            Self::LinkLike => "link-like",
            Self::RemoveLike => "remove-like",
            Self::ReadPaths => "read-paths",
            Self::DefaultCurrentDir => "default-current-dir",
            Self::Grep => "grep",
            Self::Ripgrep => "ripgrep",
            Self::SilverSearcher => "silver-searcher",
            Self::Sed => "sed",
            Self::Awk => "awk",
            Self::Find => "find",
            Self::Fd => "fd",
            Self::Xargs => "xargs",
            Self::Tar => "tar",
            Self::Sort => "sort",
            Self::Uniq => "uniq",
            Self::Split => "split",
            Self::Csplit => "csplit",
            Self::Tee => "tee",
            Self::Touch => "touch",
            Self::Truncate => "truncate",
            Self::ChangeAttributes => "change-attributes",
            Self::Dd => "dd",
            Self::Protoc => "protoc",
            Self::Flatc => "flatc",
            Self::Thrift => "thrift",
            Self::CapnpCompile => "capnp-compile",
            Self::NonWatchable => "non-watchable",
            Self::Fallback => "fallback",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum SideEffectProfile {
    ReadOnly,
    WritesExcludedOutputs,
    WritesWatchedInputs,
}

impl SideEffectProfile {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::ReadOnly => "read-only",
            Self::WritesExcludedOutputs => "writes-excluded-outputs",
            Self::WritesWatchedInputs => "writes-watched-inputs",
        }
    }

    fn merge(self, other: Self) -> Self {
        self.max(other)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CommandAnalysisStatus {
    Resolved,
    NoInputs,
    AmbiguousFallback,
}

impl CommandAnalysisStatus {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Resolved => "resolved",
            Self::NoInputs => "no-inputs",
            Self::AmbiguousFallback => "ambiguous-fallback",
        }
    }
}

#[derive(Debug, Clone)]
pub struct CommandAnalysis {
    pub inputs: Vec<WatchInput>,
    pub adapter_ids: Vec<CommandAdapterId>,
    pub fallback_used: bool,
    pub default_watch_root_used: bool,
    pub filtered_output_count: usize,
    pub side_effect_profile: SideEffectProfile,
    pub status: CommandAnalysisStatus,
}

impl CommandAnalysis {
    pub fn adapter_field(&self) -> String {
        self.adapter_ids
            .iter()
            .map(|adapter| adapter.as_str())
            .collect::<Vec<_>>()
            .join(",")
    }
}

#[derive(Debug, Clone)]
struct SingleCommandAnalysis {
    inputs: Vec<WatchInput>,
    adapter_ids: Vec<CommandAdapterId>,
    fallback_used: bool,
    default_watch_root_used: bool,
    filtered_output_count: usize,
    side_effect_profile: SideEffectProfile,
    status: CommandAnalysisStatus,
}

impl SingleCommandAnalysis {
    fn empty(adapter_id: CommandAdapterId) -> Self {
        Self {
            inputs: Vec::new(),
            adapter_ids: vec![adapter_id],
            fallback_used: adapter_id == CommandAdapterId::Fallback,
            default_watch_root_used: false,
            filtered_output_count: 0,
            side_effect_profile: SideEffectProfile::ReadOnly,
            status: CommandAnalysisStatus::NoInputs,
        }
    }

    fn finalize(mut self) -> Self {
        if self.status != CommandAnalysisStatus::AmbiguousFallback {
            self.status = if self.inputs.is_empty() {
                CommandAnalysisStatus::NoInputs
            } else {
                CommandAnalysisStatus::Resolved
            };
        }
        self
    }
}

pub fn analyze_argv(argv: &[OsString], cwd: &Path) -> Result<CommandAnalysis> {
    let argv = argv
        .iter()
        .map(|value| value.to_string_lossy().into_owned())
        .collect::<Vec<_>>();

    let analysis = if argv.is_empty() {
        SingleCommandAnalysis::empty(CommandAdapterId::NonWatchable)
    } else {
        analyze_command_tokens(&argv, &[], cwd)?
    };

    Ok(aggregate_analyses([analysis]))
}

pub fn analyze_shell_expression(
    expression: &ParsedShellExpression,
    cwd: &Path,
) -> Result<CommandAnalysis> {
    let mut analyses = Vec::new();

    for command in &expression.commands {
        analyses.push(analyze_command_tokens(
            &command.argv,
            &command.redirects,
            cwd,
        )?);
    }

    Ok(aggregate_analyses(analyses))
}

fn aggregate_analyses<I>(analyses: I) -> CommandAnalysis
where
    I: IntoIterator<Item = SingleCommandAnalysis>,
{
    let mut inputs = Vec::new();
    let mut adapter_ids = Vec::new();
    let mut fallback_used = false;
    let mut default_watch_root_used = false;
    let mut filtered_output_count = 0usize;
    let mut side_effect_profile = SideEffectProfile::ReadOnly;
    let mut status = CommandAnalysisStatus::NoInputs;

    for analysis in analyses {
        for input in analysis.inputs {
            if !inputs.contains(&input) {
                inputs.push(input);
            }
        }
        for adapter_id in analysis.adapter_ids {
            if !adapter_ids.contains(&adapter_id) {
                adapter_ids.push(adapter_id);
            }
        }
        fallback_used |= analysis.fallback_used;
        default_watch_root_used |= analysis.default_watch_root_used;
        filtered_output_count += analysis.filtered_output_count;
        side_effect_profile = side_effect_profile.merge(analysis.side_effect_profile);
        if analysis.status == CommandAnalysisStatus::AmbiguousFallback {
            status = CommandAnalysisStatus::AmbiguousFallback;
        } else if analysis.status == CommandAnalysisStatus::Resolved
            && status != CommandAnalysisStatus::AmbiguousFallback
        {
            status = CommandAnalysisStatus::Resolved;
        }
    }

    if status != CommandAnalysisStatus::AmbiguousFallback && inputs.is_empty() {
        status = CommandAnalysisStatus::NoInputs;
    }

    CommandAnalysis {
        inputs,
        adapter_ids,
        fallback_used,
        default_watch_root_used,
        filtered_output_count,
        side_effect_profile,
        status,
    }
}

fn analyze_command_tokens(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    if argv.is_empty() {
        return Ok(SingleCommandAnalysis::empty(CommandAdapterId::NonWatchable));
    }

    let command_name = command_name(argv[0].as_str());
    let mut analysis = if let Some(handler) = explicit_command_handler(command_name.as_str()) {
        analyze_explicit_command(handler, argv, redirects, cwd)?
    } else {
        match command_name.as_str() {
            name if DEFAULT_CURRENT_DIR_COMMANDS.contains(&name) => {
                analyze_default_current_dir_reader(argv, redirects, cwd)?
            }
            name if NONWATCHABLE_COMMANDS.contains(&name) => {
                analyze_non_watchable(argv, redirects, cwd)?
            }
            name if GENERIC_READ_PATH_COMMANDS.contains(&name) => {
                analyze_generic_read_paths(argv, redirects, cwd)?
            }
            _ => analyze_fallback(argv, redirects, cwd)?,
        }
    };

    if analysis.adapter_ids.is_empty() {
        analysis.adapter_ids.push(CommandAdapterId::Fallback);
    }

    Ok(analysis.finalize())
}

const DEFAULT_CURRENT_DIR_COMMANDS: &[&str] = &["du"];
const NONWATCHABLE_COMMANDS: &[&str] = &[
    "echo", "printf", "seq", "yes", "sleep", "date", "uname", "pwd", "true", "false", "basename",
    "dirname", "nproc", "printenv", "whoami", "logname", "users", "hostid", "numfmt", "mktemp",
    "mkdir", "mkfifo", "mknod",
];
const GENERIC_READ_PATH_COMMANDS: &[&str] = &[
    "cat",
    "tac",
    "head",
    "tail",
    "wc",
    "nl",
    "od",
    "cut",
    "fmt",
    "fold",
    "paste",
    "pr",
    "tr",
    "expand",
    "unexpand",
    "stat",
    "readlink",
    "realpath",
    "md5sum",
    "b2sum",
    "cksum",
    "sum",
    "sha1sum",
    "sha224sum",
    "sha256sum",
    "sha384sum",
    "sha512sum",
    "sha512_224sum",
    "sha512_256sum",
    "base32",
    "base64",
    "basenc",
    "comm",
    "join",
    "cmp",
    "tsort",
    "shuf",
];

struct HelpInventory {
    wrapper_commands: Vec<&'static str>,
    dedicated_built_ins: Vec<&'static str>,
    generic_read_path_commands: &'static [&'static str],
    safe_current_dir_defaults: Vec<&'static str>,
    non_watchable_commands: &'static [&'static str],
}

fn help_inventory() -> HelpInventory {
    let mut wrapper_commands = Vec::new();
    let mut dedicated_built_ins = Vec::new();
    let mut safe_current_dir_defaults = Vec::new();

    for spec in EXPLICIT_COMMAND_SPECS {
        match spec.help_group {
            HelpInventoryGroup::Wrapper => wrapper_commands.extend_from_slice(spec.aliases),
            HelpInventoryGroup::DedicatedBuiltIn => {
                dedicated_built_ins.extend_from_slice(spec.aliases)
            }
        }

        if spec.safe_current_dir_default {
            safe_current_dir_defaults.extend_from_slice(spec.aliases);
        }
    }

    safe_current_dir_defaults.extend_from_slice(DEFAULT_CURRENT_DIR_COMMANDS);

    HelpInventory {
        wrapper_commands,
        dedicated_built_ins,
        generic_read_path_commands: GENERIC_READ_PATH_COMMANDS,
        safe_current_dir_defaults,
        non_watchable_commands: NONWATCHABLE_COMMANDS,
    }
}

pub fn render_after_long_help() -> String {
    let inventory = help_inventory();

    format!(
        "Command modes:\n  Passthrough: with-watch [--no-hash] [--clear] <utility> [args...]\n  \
         Shell: with-watch [--no-hash] [--clear] --shell '<expr>'\n  Explicit inputs: with-watch \
         exec [--no-hash] [--clear] --input <glob>... -- <command> [args...]\n\nWrapper \
         commands:\n  {}\n\nDedicated built-in adapters and aliases:\n  {}\n\nGeneric read-path \
         commands:\n  {}\n\nSafe current-directory defaults:\n  {}\n\nRecognized but not \
         auto-watchable commands:\n  {}\n  These commands are recognized, but they do not expose \
         stable filesystem inputs on their own.\n\nexec --input escape hatch:\n  Use `with-watch \
         exec --input <glob>... -- <command> [args...]` when inference is ambiguous, when a \
         command has no stable filesystem inputs, or when you want an explicit watch set.",
        join_command_names(&inventory.wrapper_commands),
        join_command_names(&inventory.dedicated_built_ins),
        join_command_names(inventory.generic_read_path_commands),
        join_command_names(&inventory.safe_current_dir_defaults),
        join_command_names(inventory.non_watchable_commands),
    )
}

fn join_command_names(commands: &[&str]) -> String {
    commands.join(", ")
}

fn explicit_command_handler(command_name: &str) -> Option<ExplicitCommandHandler> {
    EXPLICIT_COMMAND_SPECS
        .iter()
        .find(|spec| spec.aliases.contains(&command_name))
        .map(|spec| spec.handler)
}

fn analyze_explicit_command(
    handler: ExplicitCommandHandler,
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    match handler {
        ExplicitCommandHandler::EnvWrapper => analyze_env_wrapper(argv, redirects, cwd),
        ExplicitCommandHandler::NiceWrapper => analyze_nice_wrapper(argv, redirects, cwd),
        ExplicitCommandHandler::NohupWrapper => analyze_nohup_wrapper(argv, redirects, cwd),
        ExplicitCommandHandler::StdbufWrapper => analyze_stdbuf_wrapper(argv, redirects, cwd),
        ExplicitCommandHandler::TimeoutWrapper => analyze_timeout_wrapper(argv, redirects, cwd),
        ExplicitCommandHandler::CopyLike => analyze_copy_like(
            argv,
            CommandAdapterId::CopyLike,
            SideEffectProfile::WritesExcludedOutputs,
            redirects,
            cwd,
        ),
        ExplicitCommandHandler::MoveLike => analyze_copy_like(
            argv,
            CommandAdapterId::MoveLike,
            SideEffectProfile::WritesWatchedInputs,
            redirects,
            cwd,
        ),
        ExplicitCommandHandler::Install => analyze_install(argv, redirects, cwd),
        ExplicitCommandHandler::LinkLike => analyze_link_like(argv, redirects, cwd),
        ExplicitCommandHandler::RemoveLike => analyze_remove_like(argv, redirects, cwd),
        ExplicitCommandHandler::Sort => analyze_sort(argv, redirects, cwd),
        ExplicitCommandHandler::Uniq => analyze_uniq(argv, redirects, cwd),
        ExplicitCommandHandler::Split => analyze_split(argv, redirects, cwd),
        ExplicitCommandHandler::Csplit => analyze_csplit(argv, redirects, cwd),
        ExplicitCommandHandler::Tee => analyze_tee(argv, redirects, cwd),
        ExplicitCommandHandler::Grep => analyze_grep(argv, redirects, cwd),
        ExplicitCommandHandler::Ripgrep => analyze_ripgrep(argv, redirects, cwd),
        ExplicitCommandHandler::SilverSearcher => analyze_silver_searcher(argv, redirects, cwd),
        ExplicitCommandHandler::Sed => analyze_sed(argv, redirects, cwd),
        ExplicitCommandHandler::Awk => analyze_awk(argv, redirects, cwd),
        ExplicitCommandHandler::Find => analyze_find(argv, redirects, cwd),
        ExplicitCommandHandler::LsLike => analyze_ls_like(argv, redirects, cwd),
        ExplicitCommandHandler::Fd => analyze_fd(argv, redirects, cwd),
        ExplicitCommandHandler::Xargs => analyze_xargs(argv, redirects, cwd),
        ExplicitCommandHandler::Tar => analyze_tar(argv, redirects, cwd),
        ExplicitCommandHandler::Touch => {
            analyze_touch_like(argv, CommandAdapterId::Touch, redirects, cwd)
        }
        ExplicitCommandHandler::Truncate => {
            analyze_touch_like(argv, CommandAdapterId::Truncate, redirects, cwd)
        }
        ExplicitCommandHandler::ChangeAttributes => analyze_change_attributes(argv, redirects, cwd),
        ExplicitCommandHandler::Dd => analyze_dd(argv, redirects, cwd),
        ExplicitCommandHandler::Protoc => analyze_protoc(argv, redirects, cwd),
        ExplicitCommandHandler::Flatc => analyze_flatc(argv, redirects, cwd),
        ExplicitCommandHandler::Thrift => analyze_thrift(argv, redirects, cwd),
        ExplicitCommandHandler::Capnp => analyze_capnp(argv, redirects, cwd),
    }
}

fn command_name(program: &str) -> String {
    Path::new(program)
        .file_name()
        .unwrap_or_else(|| program.as_ref())
        .to_string_lossy()
        .to_ascii_lowercase()
}

fn analyze_env_wrapper(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if token == "--" || token == "-" {
            index += 1;
            break;
        }
        if token == "-u" || token == "--unset" || token == "-C" || token == "--chdir" {
            index += 2;
            continue;
        }
        if token == "-S"
            || token == "--split-string"
            || token.starts_with("--unset=")
            || token.starts_with("--chdir=")
            || token.starts_with("--split-string=")
            || token == "-i"
            || token == "--ignore-environment"
        {
            index += 1;
            continue;
        }
        if token.contains('=') && !token.starts_with('=') {
            index += 1;
            continue;
        }
        break;
    }

    wrap_analysis(CommandAdapterId::WrapperEnv, &argv[index..], redirects, cwd)
}

fn analyze_nice_wrapper(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if token == "--" {
            index += 1;
            break;
        }
        if token == "-n" || token == "--adjustment" {
            index += 2;
            continue;
        }
        if token.starts_with("--adjustment=")
            || token == "--help"
            || token == "--version"
            || is_signed_integer(token)
        {
            index += 1;
            continue;
        }
        break;
    }

    wrap_analysis(
        CommandAdapterId::WrapperNice,
        &argv[index..],
        redirects,
        cwd,
    )
}

fn analyze_nohup_wrapper(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut index = 1usize;
    if index < argv.len() && argv[index] == "--" {
        index += 1;
    }
    wrap_analysis(
        CommandAdapterId::WrapperNohup,
        &argv[index..],
        redirects,
        cwd,
    )
}

fn analyze_stdbuf_wrapper(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if token == "--" {
            index += 1;
            break;
        }
        if token == "-i" || token == "-o" || token == "-e" {
            index += 2;
            continue;
        }
        if token.starts_with("--input=")
            || token.starts_with("--output=")
            || token.starts_with("--error=")
        {
            index += 1;
            continue;
        }
        if token == "--input" || token == "--output" || token == "--error" {
            index += 2;
            continue;
        }
        break;
    }

    wrap_analysis(
        CommandAdapterId::WrapperStdbuf,
        &argv[index..],
        redirects,
        cwd,
    )
}

fn analyze_timeout_wrapper(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if token == "--" {
            index += 1;
            break;
        }
        if token == "-s" || token == "--signal" || token == "-k" || token == "--kill-after" {
            index += 2;
            continue;
        }
        if token.starts_with("--signal=")
            || token.starts_with("--kill-after=")
            || token == "--foreground"
            || token == "--preserve-status"
            || token == "--verbose"
        {
            index += 1;
            continue;
        }
        break;
    }

    if index < argv.len() {
        index += 1;
    }

    wrap_analysis(
        CommandAdapterId::WrapperTimeout,
        &argv[index..],
        redirects,
        cwd,
    )
}

fn wrap_analysis(
    wrapper_id: CommandAdapterId,
    inner_argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut analysis = if inner_argv.is_empty() {
        SingleCommandAnalysis::empty(wrapper_id)
    } else {
        analyze_command_tokens(inner_argv, redirects, cwd)?
    };

    if !analysis.adapter_ids.contains(&wrapper_id) {
        analysis.adapter_ids.insert(0, wrapper_id);
    }
    Ok(analysis)
}

fn analyze_copy_like(
    argv: &[String],
    adapter_id: CommandAdapterId,
    side_effect_profile: SideEffectProfile,
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;
    let mut target_directory = None::<String>;
    let mut operands = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "-t" || token == "--target-directory" {
                if let Some(value) = argv.get(index + 1) {
                    target_directory = Some(value.clone());
                }
                index += 2;
                continue;
            }
            if let Some(value) = token.strip_prefix("--target-directory=") {
                target_directory = Some(value.to_string());
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        operands.push(argv[index].clone());
        index += 1;
    }

    if target_directory.is_some() {
        filtered_output_count += 1;
        for operand in operands {
            push_inferred_input(&mut inputs, operand.as_str(), cwd)?;
        }
        let mut analysis = SingleCommandAnalysis {
            inputs,
            adapter_ids: vec![adapter_id],
            fallback_used: false,
            default_watch_root_used: false,
            filtered_output_count,
            side_effect_profile,
            status: CommandAnalysisStatus::NoInputs,
        };
        apply_redirects(&mut analysis, redirects, cwd)?;
        return Ok(analysis);
    }

    let split_index = operands.len().saturating_sub(1);
    for operand in &operands[..split_index] {
        push_inferred_input(&mut inputs, operand.as_str(), cwd)?;
    }
    if operands.len() >= 2 {
        filtered_output_count += 1;
    } else if let Some(operand) = operands.first() {
        push_inferred_input(&mut inputs, operand.as_str(), cwd)?;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![adapter_id],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_install(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;
    let mut target_directory = None::<String>;
    let mut compare_reference = None::<String>;
    let mut operands = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "-t" || token == "--target-directory" {
                if let Some(value) = argv.get(index + 1) {
                    target_directory = Some(value.clone());
                }
                index += 2;
                continue;
            }
            if token == "-C" || token == "--compare" {
                index += 1;
                continue;
            }
            if token == "--compare-with" {
                if let Some(value) = argv.get(index + 1) {
                    compare_reference = Some(value.clone());
                }
                index += 2;
                continue;
            }
            if let Some(value) = token.strip_prefix("--target-directory=") {
                target_directory = Some(value.to_string());
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--compare-with=") {
                compare_reference = Some(value.to_string());
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        operands.push(argv[index].clone());
        index += 1;
    }

    if let Some(compare_reference) = compare_reference {
        push_inferred_input(&mut inputs, compare_reference.as_str(), cwd)?;
    }

    if let Some(_target_directory) = target_directory {
        filtered_output_count += 1;
        for operand in operands {
            push_inferred_input(&mut inputs, operand.as_str(), cwd)?;
        }
    } else {
        let split_index = operands.len().saturating_sub(1);
        for operand in &operands[..split_index] {
            push_inferred_input(&mut inputs, operand.as_str(), cwd)?;
        }
        if operands.len() >= 2 {
            filtered_output_count += 1;
        } else if let Some(operand) = operands.first() {
            push_inferred_input(&mut inputs, operand.as_str(), cwd)?;
        }
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Install],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile: SideEffectProfile::WritesExcludedOutputs,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_link_like(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    analyze_copy_like(
        argv,
        CommandAdapterId::LinkLike,
        SideEffectProfile::WritesExcludedOutputs,
        redirects,
        cwd,
    )
}

fn analyze_remove_like(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }
        if !positional_only && token.starts_with('-') {
            index += 1;
            continue;
        }
        push_inferred_input(&mut inputs, token, cwd)?;
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::RemoveLike],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::WritesWatchedInputs,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_sort(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;
    let mut operands = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }
        if !positional_only {
            if token == "-o" || token == "--output" {
                if argv.get(index + 1).is_some() {
                    filtered_output_count += 1;
                }
                index += 2;
                continue;
            }
            if token == "--files0-from" {
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if token.starts_with("-o") && token.len() > 2 {
                filtered_output_count += 1;
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--output=") {
                if !value.is_empty() {
                    filtered_output_count += 1;
                }
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--files0-from=") {
                push_inferred_input(&mut inputs, value, cwd)?;
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        operands.push(argv[index].clone());
        index += 1;
    }

    for operand in operands {
        if operand != "-" {
            push_inferred_input(&mut inputs, operand.as_str(), cwd)?;
        }
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Sort],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile: if filtered_output_count > 0 {
            SideEffectProfile::WritesExcludedOutputs
        } else {
            SideEffectProfile::ReadOnly
        },
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_uniq(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;
    let mut operands = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }
        if !positional_only && token.starts_with('-') {
            index += 1;
            continue;
        }
        operands.push(argv[index].clone());
        index += 1;
    }

    if let Some(input) = operands.first() {
        if input != "-" {
            push_inferred_input(&mut inputs, input.as_str(), cwd)?;
        }
    }
    if operands.len() >= 2 {
        filtered_output_count += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Uniq],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile: if filtered_output_count > 0 {
            SideEffectProfile::WritesExcludedOutputs
        } else {
            SideEffectProfile::ReadOnly
        },
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_split(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;
    let mut operands = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }
        if !positional_only {
            if token == "--filter"
                || token == "--separator"
                || token == "--additional-suffix"
                || token == "--number"
            {
                index += 2;
                continue;
            }
            if token == "-n"
                || token == "-a"
                || token == "-b"
                || token == "-C"
                || token == "-l"
                || token == "-t"
            {
                index += 2;
                continue;
            }
            if token.starts_with("--filter=")
                || token.starts_with("--separator=")
                || token.starts_with("--additional-suffix=")
                || token.starts_with("--number=")
                || token.starts_with("-n")
                || token.starts_with("-a")
                || token.starts_with("-b")
                || token.starts_with("-C")
                || token.starts_with("-l")
                || token.starts_with("-t")
            {
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        operands.push(argv[index].clone());
        index += 1;
    }

    if let Some(input) = operands.first() {
        if input != "-" {
            push_inferred_input(&mut inputs, input.as_str(), cwd)?;
        }
    }
    if operands.len() >= 2 {
        filtered_output_count += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Split],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile: SideEffectProfile::WritesExcludedOutputs,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_csplit(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;
    let mut first_operand = None::<String>;
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }
        if !positional_only {
            if token == "-f" || token == "--prefix" || token == "-b" || token == "--suffix-format" {
                if token == "-f" || token == "--prefix" {
                    filtered_output_count += 1;
                }
                index += 2;
                continue;
            }
            if token.starts_with("--prefix=") {
                filtered_output_count += 1;
                index += 1;
                continue;
            }
            if token.starts_with("--suffix-format=") || token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        if first_operand.is_none() {
            first_operand = Some(argv[index].clone());
        }
        index += 1;
    }

    if let Some(input) = first_operand {
        if input != "-" {
            push_inferred_input(&mut inputs, input.as_str(), cwd)?;
        }
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Csplit],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile: SideEffectProfile::WritesExcludedOutputs,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_tee(
    _argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut analysis = SingleCommandAnalysis::empty(CommandAdapterId::Tee);
    analysis.side_effect_profile = SideEffectProfile::WritesExcludedOutputs;
    analysis.filtered_output_count = 1;
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_grep(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut explicit_pattern = false;
    let mut consumed_pattern = false;
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "-e" || token == "--regexp" {
                explicit_pattern = true;
                index += 2;
                continue;
            }
            if token == "-f" || token == "--file" {
                explicit_pattern = true;
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if let Some(value) = token.strip_prefix("--regexp=") {
                explicit_pattern = true;
                let _ = value;
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--file=") {
                explicit_pattern = true;
                push_inferred_input(&mut inputs, value, cwd)?;
                index += 1;
                continue;
            }
            if let Some(option) = parse_grep_short_pattern_option(token) {
                explicit_pattern = true;
                match option {
                    GrepShortPatternOption::Inline => {}
                    GrepShortPatternOption::Next => {
                        index += 2;
                        continue;
                    }
                    GrepShortPatternOption::FileInline(value) => {
                        push_inferred_input(&mut inputs, value, cwd)?;
                    }
                    GrepShortPatternOption::FileNext => {
                        if let Some(value) = argv.get(index + 1) {
                            push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                        }
                        index += 2;
                        continue;
                    }
                }
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        if !explicit_pattern && !consumed_pattern {
            consumed_pattern = true;
        } else if token != "-" {
            push_inferred_input(&mut inputs, token, cwd)?;
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Grep],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::ReadOnly,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum GrepShortPatternOption<'a> {
    Inline,
    Next,
    FileInline(&'a str),
    FileNext,
}

fn parse_grep_short_pattern_option(token: &str) -> Option<GrepShortPatternOption<'_>> {
    if !token.starts_with('-') || token == "-" || token.starts_with("--") {
        return None;
    }

    let flags = token.trim_start_matches('-');
    for (index, flag) in flags.char_indices() {
        let value = &flags[index + flag.len_utf8()..];
        match flag {
            'e' if value.is_empty() => return Some(GrepShortPatternOption::Next),
            'e' => return Some(GrepShortPatternOption::Inline),
            'f' if value.is_empty() => return Some(GrepShortPatternOption::FileNext),
            'f' => return Some(GrepShortPatternOption::FileInline(value)),
            _ => {}
        }
    }

    None
}

fn analyze_ripgrep(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut explicit_patterns = false;
    let mut files_mode = false;
    let mut consumed_implicit_pattern = false;
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "--files" {
                files_mode = true;
                index += 1;
                continue;
            }
            if token == "-e" || token == "--regexp" {
                explicit_patterns = true;
                index += 2;
                continue;
            }
            if token == "-f" || token == "--file" {
                explicit_patterns = true;
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if token == "-g"
                || token == "--glob"
                || token == "--iglob"
                || token == "--pre-glob"
                || token == "--type"
                || token == "--type-not"
                || token == "--type-add"
            {
                index += 2;
                continue;
            }
            if token == "--ignore-file" {
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if matches!(
                token,
                "--pre"
                    | "--dfa-size-limit"
                    | "--encoding"
                    | "--engine"
                    | "--max-count"
                    | "--threads"
                    | "--max-depth"
                    | "--max-filesize"
                    | "--type-clear"
                    | "--after-context"
                    | "--before-context"
                    | "--context"
                    | "--color"
                    | "--colors"
                    | "--context-separator"
                    | "--field-context-separator"
                    | "--field-match-separator"
                    | "--hostname-bin"
                    | "--hyperlink-format"
                    | "--max-columns"
                    | "--path-separator"
                    | "--replace"
                    | "--sort"
                    | "--sortr"
                    | "--generate"
            ) {
                index += 2;
                continue;
            }
            if let Some(value) = token.strip_prefix("--file=") {
                explicit_patterns = true;
                push_inferred_input(&mut inputs, value, cwd)?;
                index += 1;
                continue;
            }
            if token.starts_with("--regexp=")
                || token.starts_with("--glob=")
                || token.starts_with("--iglob=")
                || token.starts_with("--pre-glob=")
                || token.starts_with("--pre=")
                || token.starts_with("--dfa-size-limit=")
                || token.starts_with("--encoding=")
                || token.starts_with("--engine=")
                || token.starts_with("--max-count=")
                || token.starts_with("--threads=")
                || token.starts_with("--max-depth=")
                || token.starts_with("--max-filesize=")
                || token.starts_with("--type-add=")
                || token.starts_with("--type=")
                || token.starts_with("--type-not=")
                || token.starts_with("--type-clear=")
                || token.starts_with("--after-context=")
                || token.starts_with("--before-context=")
                || token.starts_with("--context=")
                || token.starts_with("--color=")
                || token.starts_with("--colors=")
                || token.starts_with("--context-separator=")
                || token.starts_with("--field-context-separator=")
                || token.starts_with("--field-match-separator=")
                || token.starts_with("--hostname-bin=")
                || token.starts_with("--hyperlink-format=")
                || token.starts_with("--max-columns=")
                || token.starts_with("--path-separator=")
                || token.starts_with("--replace=")
                || token.starts_with("--sort=")
                || token.starts_with("--sortr=")
                || token.starts_with("--generate=")
            {
                explicit_patterns |= token.starts_with("--regexp=");
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--ignore-file=") {
                push_inferred_input(&mut inputs, value, cwd)?;
                index += 1;
                continue;
            }
            if let Some(option) = parse_ripgrep_short_option(token) {
                match option {
                    RipgrepShortOption::PatternInline => explicit_patterns = true,
                    RipgrepShortOption::PatternNext => {
                        explicit_patterns = true;
                        index += 2;
                        continue;
                    }
                    RipgrepShortOption::PatternFileInline(value) => {
                        explicit_patterns = true;
                        push_inferred_input(&mut inputs, value, cwd)?;
                    }
                    RipgrepShortOption::PatternFileNext => {
                        explicit_patterns = true;
                        if let Some(value) = argv.get(index + 1) {
                            push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                        }
                        index += 2;
                        continue;
                    }
                    RipgrepShortOption::GlobInline
                    | RipgrepShortOption::TypeInline
                    | RipgrepShortOption::TypeNotInline
                    | RipgrepShortOption::ControlValueInline => {}
                    RipgrepShortOption::GlobNext
                    | RipgrepShortOption::TypeNext
                    | RipgrepShortOption::TypeNotNext
                    | RipgrepShortOption::ControlValueNext => {
                        index += 2;
                        continue;
                    }
                }
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        if files_mode || explicit_patterns || consumed_implicit_pattern {
            push_inferred_input(&mut inputs, token, cwd)?;
        } else {
            consumed_implicit_pattern = true;
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Ripgrep],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::ReadOnly,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum RipgrepShortOption<'a> {
    PatternInline,
    PatternNext,
    PatternFileInline(&'a str),
    PatternFileNext,
    GlobInline,
    GlobNext,
    TypeInline,
    TypeNext,
    TypeNotInline,
    TypeNotNext,
    ControlValueInline,
    ControlValueNext,
}

fn parse_ripgrep_short_option(token: &str) -> Option<RipgrepShortOption<'_>> {
    if !token.starts_with('-') || token == "-" || token.starts_with("--") {
        return None;
    }

    let flags = token.trim_start_matches('-');
    for (index, flag) in flags.char_indices() {
        let value = &flags[index + flag.len_utf8()..];
        match flag {
            'e' if value.is_empty() => return Some(RipgrepShortOption::PatternNext),
            'e' => return Some(RipgrepShortOption::PatternInline),
            'f' if value.is_empty() => return Some(RipgrepShortOption::PatternFileNext),
            'f' => return Some(RipgrepShortOption::PatternFileInline(value)),
            'g' if value.is_empty() => return Some(RipgrepShortOption::GlobNext),
            'g' => return Some(RipgrepShortOption::GlobInline),
            't' if value.is_empty() => return Some(RipgrepShortOption::TypeNext),
            't' => return Some(RipgrepShortOption::TypeInline),
            'T' if value.is_empty() => return Some(RipgrepShortOption::TypeNotNext),
            'T' => return Some(RipgrepShortOption::TypeNotInline),
            'A' | 'B' | 'C' | 'E' | 'M' | 'd' | 'j' | 'm' | 'r' if value.is_empty() => {
                return Some(RipgrepShortOption::ControlValueNext);
            }
            'A' | 'B' | 'C' | 'E' | 'M' | 'd' | 'j' | 'm' | 'r' => {
                return Some(RipgrepShortOption::ControlValueInline);
            }
            _ => {}
        }
    }

    None
}

fn analyze_silver_searcher(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut filename_pattern_mode = false;
    let mut positionals = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "--filename-pattern" {
                filename_pattern_mode = true;
                index += 2;
                continue;
            }
            if token == "--ignore" {
                index += 2;
                continue;
            }
            if token == "--file-search-regex" {
                index += 2;
                continue;
            }
            if token == "--path-to-ignore" {
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if let Some(_value) = token.strip_prefix("--filename-pattern=") {
                filename_pattern_mode = true;
                index += 1;
                continue;
            }
            if token.starts_with("--file-search-regex=") {
                index += 1;
                continue;
            }
            if token.starts_with("--ignore=") {
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--path-to-ignore=") {
                push_inferred_input(&mut inputs, value, cwd)?;
                index += 1;
                continue;
            }
            if let Some(option) = parse_silver_searcher_short_option(token) {
                match option {
                    SilverSearcherShortOption::FilenamePatternInline => {
                        filename_pattern_mode = true;
                    }
                    SilverSearcherShortOption::FileSearchRegexInline => {}
                    SilverSearcherShortOption::FilenamePatternNext => {
                        filename_pattern_mode = true;
                        index += 2;
                        continue;
                    }
                    SilverSearcherShortOption::FileSearchRegexNext => {
                        index += 2;
                        continue;
                    }
                    SilverSearcherShortOption::PathToIgnoreInline(value) => {
                        push_inferred_input(&mut inputs, value, cwd)?;
                    }
                    SilverSearcherShortOption::PathToIgnoreNext => {
                        if let Some(value) = argv.get(index + 1) {
                            push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                        }
                        index += 2;
                        continue;
                    }
                }
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        positionals.push(token.to_owned());
        index += 1;
    }

    for (position, token) in positionals.into_iter().enumerate() {
        if !filename_pattern_mode && position == 0 {
            continue;
        }
        push_inferred_input(&mut inputs, token.as_str(), cwd)?;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::SilverSearcher],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::ReadOnly,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum SilverSearcherShortOption<'a> {
    FilenamePatternInline,
    FilenamePatternNext,
    FileSearchRegexInline,
    FileSearchRegexNext,
    PathToIgnoreInline(&'a str),
    PathToIgnoreNext,
}

fn parse_silver_searcher_short_option(token: &str) -> Option<SilverSearcherShortOption<'_>> {
    if !token.starts_with('-') || token == "-" || token.starts_with("--") {
        return None;
    }

    let flags = token.trim_start_matches('-');
    for (index, flag) in flags.char_indices() {
        let value = &flags[index + flag.len_utf8()..];
        match flag {
            'g' if value.is_empty() => {
                return Some(SilverSearcherShortOption::FilenamePatternNext);
            }
            'g' => return Some(SilverSearcherShortOption::FilenamePatternInline),
            'G' if value.is_empty() => {
                return Some(SilverSearcherShortOption::FileSearchRegexNext);
            }
            'G' => return Some(SilverSearcherShortOption::FileSearchRegexInline),
            'p' if value.is_empty() => return Some(SilverSearcherShortOption::PathToIgnoreNext),
            'p' => return Some(SilverSearcherShortOption::PathToIgnoreInline(value)),
            _ => {}
        }
    }

    None
}

fn analyze_fd(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    if argv.iter().skip(1).any(|token| {
        matches!(
            token.as_str(),
            "-x" | "-X" | "--exec" | "--exec-batch" | "--list-details"
        )
    }) {
        return analyze_fallback(argv, redirects, cwd);
    }

    let mut inputs = Vec::new();
    let mut base_dir: Option<String> = None;
    let mut deferred_inputs = Vec::new();
    let mut deferred_search_roots = Vec::new();
    let mut extension_filter_present = false;
    let mut positionals = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "--search-path" {
                if let Some(value) = argv.get(index + 1) {
                    deferred_search_roots.push(value.clone());
                }
                index += 2;
                continue;
            }
            if token == "--ignore-file" {
                if let Some(value) = argv.get(index + 1) {
                    deferred_inputs.push(value.clone());
                }
                index += 2;
                continue;
            }
            if token == "--base-directory" {
                if let Some(value) = argv.get(index + 1) {
                    base_dir = Some(value.clone());
                }
                index += 2;
                continue;
            }
            if token == "--extension" {
                extension_filter_present = true;
                index += 2;
                continue;
            }
            if let Some(value) = token.strip_prefix("--search-path=") {
                deferred_search_roots.push(value.to_owned());
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--ignore-file=") {
                deferred_inputs.push(value.to_owned());
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--base-directory=") {
                base_dir = Some(value.to_owned());
                index += 1;
                continue;
            }
            if token.starts_with("--extension=") {
                extension_filter_present = true;
                index += 1;
                continue;
            }
            if matches!(
                token,
                "-E" | "-t"
                    | "-c"
                    | "-d"
                    | "-j"
                    | "-o"
                    | "-S"
                    | "--exclude"
                    | "--type"
                    | "--color"
                    | "--max-depth"
                    | "--min-depth"
                    | "--threads"
                    | "--size"
                    | "--owner"
                    | "--changed-within"
                    | "--changed-before"
                    | "--changed-after"
                    | "--change-newer-than"
                    | "--change-older-than"
                    | "--newer"
                    | "--older"
                    | "--path-separator"
                    | "--format"
                    | "--ignore-contain"
                    | "--max-results"
            ) {
                index += 2;
                continue;
            }
            if token.starts_with("--exclude=")
                || token.starts_with("--type=")
                || token.starts_with("--color=")
                || token.starts_with("--max-depth=")
                || token.starts_with("--min-depth=")
                || token.starts_with("--threads=")
                || token.starts_with("--size=")
                || token.starts_with("--owner=")
                || token.starts_with("--changed-within=")
                || token.starts_with("--changed-before=")
                || token.starts_with("--changed-after=")
                || token.starts_with("--change-newer-than=")
                || token.starts_with("--change-older-than=")
                || token.starts_with("--newer=")
                || token.starts_with("--older=")
                || token.starts_with("--path-separator=")
                || token.starts_with("--format=")
                || token.starts_with("--ignore-contain=")
                || token.starts_with("--max-results=")
            {
                index += 1;
                continue;
            }
            if let Some(option) = parse_fd_short_option(token) {
                match option {
                    FdShortOption::BaseDirectoryInline(value) => {
                        base_dir = Some(value.to_owned());
                    }
                    FdShortOption::BaseDirectoryNext => {
                        if let Some(value) = argv.get(index + 1) {
                            base_dir = Some(value.clone());
                        }
                        index += 2;
                        continue;
                    }
                    FdShortOption::ExtensionInline => {
                        extension_filter_present = true;
                    }
                    FdShortOption::ExtensionNext => {
                        extension_filter_present = true;
                        index += 2;
                        continue;
                    }
                    FdShortOption::ValueInline => {}
                    FdShortOption::ValueNext => {
                        index += 2;
                        continue;
                    }
                }
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        positionals.push(token.to_owned());
        index += 1;
    }

    let fd_cwd = base_dir
        .as_deref()
        .map(|value| absolutize(value, cwd))
        .unwrap_or_else(|| cwd.to_path_buf());

    match positionals.len() {
        0 => {}
        1 if extension_filter_present && deferred_search_roots.is_empty() => {
            deferred_search_roots.push(positionals.remove(0));
        }
        1 => {}
        _ => {
            for token in positionals.into_iter().skip(1) {
                deferred_search_roots.push(token);
            }
        }
    }

    for input in deferred_inputs {
        push_inferred_input(&mut inputs, input.as_str(), fd_cwd.as_path())?;
    }
    for root in deferred_search_roots {
        push_inferred_input(&mut inputs, root.as_str(), fd_cwd.as_path())?;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Fd],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::ReadOnly,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum FdShortOption<'a> {
    BaseDirectoryInline(&'a str),
    BaseDirectoryNext,
    ExtensionInline,
    ExtensionNext,
    ValueInline,
    ValueNext,
}

fn parse_fd_short_option(token: &str) -> Option<FdShortOption<'_>> {
    if !token.starts_with('-') || token == "-" || token.starts_with("--") {
        return None;
    }

    let flags = token.trim_start_matches('-');
    for (index, flag) in flags.char_indices() {
        let value = &flags[index + flag.len_utf8()..];
        match flag {
            'C' if value.is_empty() => return Some(FdShortOption::BaseDirectoryNext),
            'C' => return Some(FdShortOption::BaseDirectoryInline(value)),
            'e' if value.is_empty() => return Some(FdShortOption::ExtensionNext),
            'e' => return Some(FdShortOption::ExtensionInline),
            'E' | 'S' | 'c' | 'd' | 'j' | 'o' | 't' if value.is_empty() => {
                return Some(FdShortOption::ValueNext);
            }
            'E' | 'S' | 'c' | 'd' | 'j' | 'o' | 't' => {
                return Some(FdShortOption::ValueInline);
            }
            _ => {}
        }
    }

    None
}

fn analyze_sed(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut explicit_script = false;
    let mut consumed_script = false;
    let mut in_place = false;
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "-e" || token == "--expression" {
                explicit_script = true;
                index += 2;
                continue;
            }
            if token == "-f" || token == "--file" {
                explicit_script = true;
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if token == "-i" || token == "--in-place" {
                in_place = true;
                if argv
                    .get(index + 1)
                    .is_some_and(|value| !value.starts_with('-'))
                {
                    index += 2;
                } else {
                    index += 1;
                }
                continue;
            }
            if token.starts_with("--expression=") {
                explicit_script = true;
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--file=") {
                explicit_script = true;
                push_inferred_input(&mut inputs, value, cwd)?;
                index += 1;
                continue;
            }
            if token.starts_with("--in-place=") || token.starts_with("-i") {
                in_place = true;
                index += 1;
                continue;
            }
            if token.starts_with("-e") && token.len() > 2 {
                explicit_script = true;
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("-f") {
                if !value.is_empty() {
                    explicit_script = true;
                    push_inferred_input(&mut inputs, value, cwd)?;
                    index += 1;
                    continue;
                }
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        if !explicit_script && !consumed_script {
            consumed_script = true;
        } else if token != "-" {
            push_inferred_input(&mut inputs, token, cwd)?;
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Sed],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: if in_place {
            SideEffectProfile::WritesWatchedInputs
        } else {
            SideEffectProfile::ReadOnly
        },
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_awk(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut explicit_program = false;
    let mut consumed_program = false;
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "-f" || token == "--file" {
                explicit_program = true;
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if token == "-v" || token == "-F" {
                index += 2;
                continue;
            }
            if let Some(value) = token.strip_prefix("--file=") {
                explicit_program = true;
                push_inferred_input(&mut inputs, value, cwd)?;
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("-f") {
                if !value.is_empty() {
                    explicit_program = true;
                    push_inferred_input(&mut inputs, value, cwd)?;
                    index += 1;
                    continue;
                }
            }
            if token.starts_with("-v") || token.starts_with("-F") || token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        if !explicit_program && !consumed_program {
            consumed_program = true;
        } else if token != "-" && !looks_like_variable_assignment(token) {
            push_inferred_input(&mut inputs, token, cwd)?;
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Awk],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::ReadOnly,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_find(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut saw_expression = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if token == "--" {
            index += 1;
            continue;
        }
        if !saw_expression {
            if let Some(next_index) = consume_find_global_option(argv, index) {
                index = next_index;
                continue;
            }
        }
        if !saw_expression && !is_find_expression_token(token) {
            push_inferred_input(&mut inputs, token, cwd)?;
        } else {
            saw_expression = true;
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Find],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::ReadOnly,
        status: CommandAnalysisStatus::NoInputs,
    };

    if analysis.inputs.is_empty() {
        push_inferred_input(&mut analysis.inputs, ".", cwd)?;
        analysis.default_watch_root_used = true;
    }

    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn consume_find_global_option(argv: &[String], index: usize) -> Option<usize> {
    let token = argv[index].as_str();
    match token {
        "-H" | "-L" | "-P" => Some(index + 1),
        "-D" => Some((index + 2).min(argv.len())),
        "-O" => {
            if argv
                .get(index + 1)
                .is_some_and(|value| is_unsigned_integer(value))
            {
                Some(index + 2)
            } else {
                Some(index + 1)
            }
        }
        _ if token.starts_with("-D") && token.len() > 2 => Some(index + 1),
        _ if token.starts_with("-O") && token.len() > 2 => Some(index + 1),
        _ => None,
    }
}

fn is_unsigned_integer(token: &str) -> bool {
    let trimmed = token.trim();
    !trimmed.is_empty() && trimmed.chars().all(|character| character.is_ascii_digit())
}

fn analyze_xargs(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if token == "--" {
            break;
        }
        if token == "-a" || token == "--arg-file" {
            if let Some(value) = argv.get(index + 1) {
                push_inferred_input(&mut inputs, value.as_str(), cwd)?;
            }
            index += 2;
            continue;
        }
        if token == "-I" || token == "-i" || token == "--replace" {
            index += 2;
            continue;
        }
        if token == "-n"
            || token == "-L"
            || token == "-s"
            || token == "-P"
            || token == "-E"
            || token == "--delimiter"
            || token == "--eof"
            || token == "--max-args"
            || token == "--max-lines"
            || token == "--max-procs"
            || token == "--max-chars"
        {
            index += 2;
            continue;
        }
        if let Some(value) = token.strip_prefix("--arg-file=") {
            push_inferred_input(&mut inputs, value, cwd)?;
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Xargs],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::ReadOnly,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_tar(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;
    let mut mode = TarMode::Unknown;
    let mut archive_path = None::<String>;
    let mut positional_operands = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "-f" || token == "--file" {
                if let Some(value) = argv.get(index + 1) {
                    archive_path = Some(value.clone());
                }
                index += 2;
                continue;
            }
            if token == "-C" || token == "--directory" {
                filtered_output_count += usize::from(matches!(mode, TarMode::ReadArchive));
                index += 2;
                continue;
            }
            if let Some(value) = token.strip_prefix("--file=") {
                archive_path = Some(value.to_string());
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--directory=") {
                if matches!(mode, TarMode::ReadArchive) {
                    let _ = value;
                    filtered_output_count += 1;
                }
                index += 1;
                continue;
            }
            if token.starts_with("--create")
                || token.starts_with("--append")
                || token.starts_with("--update")
            {
                mode = TarMode::CreateLike;
                index += 1;
                continue;
            }
            if token.starts_with("--extract")
                || token.starts_with("--get")
                || token.starts_with("--list")
                || token.starts_with("--diff")
                || token.starts_with("--compare")
            {
                mode = TarMode::ReadArchive;
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                mode = mode.merge(parse_tar_short_mode(token));
                if let Some(archive_value) = parse_tar_short_archive(token) {
                    archive_path = Some(archive_value);
                    index += 1;
                    continue;
                }
                if tar_short_option_consumes_next_archive(token) {
                    if let Some(value) = argv.get(index + 1) {
                        archive_path = Some(value.clone());
                    }
                    index += 2;
                    continue;
                }
                index += 1;
                continue;
            }
        }

        positional_operands.push(argv[index].clone());
        index += 1;
    }

    match mode {
        TarMode::CreateLike => {
            if let Some(archive_path) = archive_path {
                let _ = archive_path;
                filtered_output_count += 1;
            }
            for operand in positional_operands {
                push_inferred_input(&mut inputs, operand.as_str(), cwd)?;
            }
        }
        TarMode::ReadArchive | TarMode::Unknown => {
            if let Some(archive_path) = archive_path {
                push_inferred_input(&mut inputs, archive_path.as_str(), cwd)?;
            }
        }
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Tar],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile: if matches!(mode, TarMode::CreateLike) {
            SideEffectProfile::WritesExcludedOutputs
        } else {
            SideEffectProfile::ReadOnly
        },
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum TarMode {
    Unknown,
    CreateLike,
    ReadArchive,
}

impl TarMode {
    fn merge(self, other: Self) -> Self {
        match (self, other) {
            (Self::CreateLike, _) | (_, Self::CreateLike) => Self::CreateLike,
            (Self::ReadArchive, _) | (_, Self::ReadArchive) => Self::ReadArchive,
            _ => Self::Unknown,
        }
    }
}

fn parse_tar_short_mode(token: &str) -> TarMode {
    let raw = token.trim_start_matches('-');
    if raw.contains('c') || raw.contains('r') || raw.contains('u') {
        TarMode::CreateLike
    } else if raw.contains('x') || raw.contains('t') || raw.contains('d') {
        TarMode::ReadArchive
    } else {
        TarMode::Unknown
    }
}

fn parse_tar_short_archive(token: &str) -> Option<String> {
    let raw = token.trim_start_matches('-');
    let index = raw.find('f')?;
    let attached = &raw[index + 1..];
    if attached.is_empty() {
        None
    } else {
        Some(attached.to_string())
    }
}

fn tar_short_option_consumes_next_archive(token: &str) -> bool {
    let raw = token.trim_start_matches('-');
    raw.contains('f') && parse_tar_short_archive(token).is_none()
}

fn analyze_touch_like(
    argv: &[String],
    adapter_id: CommandAdapterId,
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }
        if !positional_only {
            if token == "-r" || token == "--reference" {
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if let Some(value) = token.strip_prefix("--reference=") {
                push_inferred_input(&mut inputs, value, cwd)?;
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }
        push_inferred_input(&mut inputs, token, cwd)?;
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![adapter_id],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::WritesWatchedInputs,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_change_attributes(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;
    let mut metadata_arg_consumed = false;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }
        if !positional_only {
            if token == "--reference" {
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if let Some(value) = token.strip_prefix("--reference=") {
                push_inferred_input(&mut inputs, value, cwd)?;
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }
        if !metadata_arg_consumed {
            metadata_arg_consumed = true;
        } else {
            push_inferred_input(&mut inputs, token, cwd)?;
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::ChangeAttributes],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::WritesWatchedInputs,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_dd(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;

    for token in argv.iter().skip(1) {
        if let Some(value) = token.strip_prefix("if=") {
            push_inferred_input(&mut inputs, value, cwd)?;
        } else if token.starts_with("iflag=") {
        } else if token.starts_with("of=") || token.starts_with("seek=") {
            filtered_output_count += 1;
        }
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Dd],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile: if filtered_output_count > 0 {
            SideEffectProfile::WritesExcludedOutputs
        } else {
            SideEffectProfile::ReadOnly
        },
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_protoc(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    if argv
        .iter()
        .skip(1)
        .any(|token| token == "--plugin" || token.starts_with("--plugin="))
    {
        return analyze_fallback(argv, redirects, cwd);
    }

    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if token.starts_with('@') {
            push_inferred_input(&mut inputs, &token[1..], cwd)?;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "-I" || token == "--proto_path" {
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if token == "--descriptor_set_in" {
                if let Some(value) = argv.get(index + 1) {
                    push_split_inferred_inputs(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if token == "-o" || token == "--descriptor_set_out" || token == "--dependency_out" {
                filtered_output_count += usize::from(argv.get(index + 1).is_some());
                index += 2;
                continue;
            }
            if token.starts_with("-I") && token.len() > 2 {
                push_inferred_input(&mut inputs, &token[2..], cwd)?;
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--proto_path=") {
                push_inferred_input(&mut inputs, value, cwd)?;
                index += 1;
                continue;
            }
            if let Some(value) = token.strip_prefix("--descriptor_set_in=") {
                push_split_inferred_inputs(&mut inputs, value, cwd)?;
                index += 1;
                continue;
            }
            if token.starts_with("-o") && token.len() > 2 {
                filtered_output_count += 1;
                index += 1;
                continue;
            }
            if token.starts_with("--descriptor_set_out=")
                || token.starts_with("--dependency_out=")
                || is_protoc_output_option(token)
            {
                filtered_output_count += 1;
                index += 1;
                continue;
            }
            if token.starts_with("--") && token.trim_start_matches('-').ends_with("_out") {
                filtered_output_count += usize::from(argv.get(index + 1).is_some());
                index += 2;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        if token.ends_with(".proto") {
            push_inferred_input(&mut inputs, token, cwd)?;
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Protoc],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile: if filtered_output_count > 0 {
            SideEffectProfile::WritesExcludedOutputs
        } else {
            SideEffectProfile::ReadOnly
        },
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_flatc(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    if argv.iter().skip(1).any(|token| token == "--") {
        return analyze_fallback(argv, redirects, cwd);
    }

    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if token == "-I" || token == "--conform" || token == "--conform-includes" {
            if let Some(value) = argv.get(index + 1) {
                push_inferred_input(&mut inputs, value.as_str(), cwd)?;
            }
            index += 2;
            continue;
        }
        if token == "-o" {
            filtered_output_count += usize::from(argv.get(index + 1).is_some());
            index += 2;
            continue;
        }
        if token.starts_with("-I") && token.len() > 2 {
            push_inferred_input(&mut inputs, &token[2..], cwd)?;
            index += 1;
            continue;
        }
        if token.starts_with("-o") && token.len() > 2 {
            filtered_output_count += 1;
            index += 1;
            continue;
        }
        if let Some(value) = token.strip_prefix("--conform=") {
            push_inferred_input(&mut inputs, value, cwd)?;
            index += 1;
            continue;
        }
        if let Some(value) = token.strip_prefix("--conform-includes=") {
            push_inferred_input(&mut inputs, value, cwd)?;
            index += 1;
            continue;
        }
        if token.starts_with('-') {
            index += 1;
            continue;
        }

        push_inferred_input(&mut inputs, token, cwd)?;
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Flatc],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile: if filtered_output_count > 0 {
            SideEffectProfile::WritesExcludedOutputs
        } else {
            SideEffectProfile::ReadOnly
        },
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_thrift(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "-I" {
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if token == "-out" || token == "-o" {
                filtered_output_count += usize::from(argv.get(index + 1).is_some());
                index += 2;
                continue;
            }
            if token == "--gen" {
                index += 2;
                continue;
            }
            if token == "-r" || token == "--recurse" || token.starts_with("--gen=") {
                index += 1;
                continue;
            }
            if token.starts_with("-I") && token.len() > 2 {
                push_inferred_input(&mut inputs, &token[2..], cwd)?;
                index += 1;
                continue;
            }
            if token.starts_with("-out") && token.len() > 4 {
                filtered_output_count += 1;
                index += 1;
                continue;
            }
            if token.starts_with("-o") && token.len() > 2 {
                filtered_output_count += 1;
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        if token.ends_with(".thrift") {
            push_inferred_input(&mut inputs, token, cwd)?;
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Thrift],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile: if filtered_output_count > 0 {
            SideEffectProfile::WritesExcludedOutputs
        } else {
            SideEffectProfile::ReadOnly
        },
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_capnp(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    if argv.get(1).map(String::as_str) != Some("compile") {
        return analyze_fallback(argv, redirects, cwd);
    }

    let mut inputs = Vec::new();
    let mut filtered_output_count = 0usize;
    let mut positional_only = false;
    let mut index = 2usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "-I" {
                if let Some(value) = argv.get(index + 1) {
                    push_inferred_input(&mut inputs, value.as_str(), cwd)?;
                }
                index += 2;
                continue;
            }
            if token == "-o" {
                filtered_output_count += usize::from(argv.get(index + 1).is_some());
                index += 2;
                continue;
            }
            if token.starts_with("-I") && token.len() > 2 {
                push_inferred_input(&mut inputs, &token[2..], cwd)?;
                index += 1;
                continue;
            }
            if token.starts_with("-o") && token.len() > 2 {
                filtered_output_count += 1;
                index += 1;
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        if token.ends_with(".capnp") {
            push_inferred_input(&mut inputs, token, cwd)?;
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::CapnpCompile],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count,
        side_effect_profile: if filtered_output_count > 0 {
            SideEffectProfile::WritesExcludedOutputs
        } else {
            SideEffectProfile::ReadOnly
        },
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn is_protoc_output_option(token: &str) -> bool {
    token.starts_with("--")
        && token
            .trim_start_matches('-')
            .split_once('=')
            .map(|(name, _)| name)
            .unwrap_or_else(|| token.trim_start_matches('-'))
            .ends_with("_out")
}

fn analyze_default_current_dir_reader(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }
        if !positional_only && token.starts_with('-') {
            index += 1;
            continue;
        }
        push_inferred_input(&mut inputs, token, cwd)?;
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::DefaultCurrentDir],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::ReadOnly,
        status: CommandAnalysisStatus::NoInputs,
    };

    if analysis.inputs.is_empty() {
        push_inferred_input(&mut analysis.inputs, ".", cwd)?;
        analysis.default_watch_root_used = true;
    }

    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_ls_like(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut positional_only = false;
    let mut recursive = false;
    let mut directory_mode = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }

        if !positional_only {
            if token == "-R" || token == "--recursive" {
                recursive = true;
                index += 1;
                continue;
            }
            if token == "-d" || token == "--directory" {
                directory_mode = true;
                index += 1;
                continue;
            }
            if token.starts_with("--") {
                index += 1;
                continue;
            }
            if token.starts_with('-') && token != "-" {
                recursive |= token.contains('R');
                directory_mode |= token.contains('d');
                index += 1;
                continue;
            }
        }

        push_inferred_path_with_mode(
            &mut inputs,
            token,
            cwd,
            ls_like_snapshot_mode(token, cwd, recursive, directory_mode),
        )?;
        index += 1;
    }

    if inputs.is_empty() {
        push_inferred_path_with_mode(
            &mut inputs,
            ".",
            cwd,
            default_ls_snapshot_mode(recursive, directory_mode),
        )?;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::DefaultCurrentDir],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::ReadOnly,
        status: CommandAnalysisStatus::NoInputs,
    };

    if argv.len() == 1
        || argv
            .iter()
            .skip(1)
            .all(|token| token == "--" || (token.starts_with('-') && token != "-"))
    {
        analysis.default_watch_root_used = true;
    }

    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn default_ls_snapshot_mode(recursive: bool, directory_mode: bool) -> PathSnapshotMode {
    if directory_mode {
        PathSnapshotMode::MetadataPath
    } else if recursive {
        PathSnapshotMode::MetadataTree
    } else {
        PathSnapshotMode::MetadataChildren
    }
}

fn ls_like_snapshot_mode(
    raw: &str,
    cwd: &Path,
    recursive: bool,
    directory_mode: bool,
) -> PathSnapshotMode {
    if directory_mode {
        return PathSnapshotMode::MetadataPath;
    }

    let absolute_path = absolutize(raw, cwd);
    match fs::metadata(&absolute_path) {
        Ok(metadata) if metadata.is_dir() => {
            if recursive {
                PathSnapshotMode::MetadataTree
            } else {
                PathSnapshotMode::MetadataChildren
            }
        }
        Ok(_) | Err(_) => PathSnapshotMode::MetadataPath,
    }
}

fn analyze_non_watchable(
    _argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut analysis = SingleCommandAnalysis::empty(CommandAdapterId::NonWatchable);
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_generic_read_paths(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }
        if !positional_only {
            if try_push_path_option_value(&mut inputs, argv, &mut index, cwd)? {
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }
        if token != "-" {
            push_inferred_input(&mut inputs, token, cwd)?;
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::ReadPaths],
        fallback_used: false,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::ReadOnly,
        status: CommandAnalysisStatus::NoInputs,
    };
    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn analyze_fallback(
    argv: &[String],
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<SingleCommandAnalysis> {
    let mut inputs = Vec::new();
    let mut ambiguous_missing = Vec::new();
    let mut positional_only = false;
    let mut index = 1usize;

    while index < argv.len() {
        let token = argv[index].as_str();
        if !positional_only && token == "--" {
            positional_only = true;
            index += 1;
            continue;
        }
        if !positional_only {
            if try_push_path_option_value(&mut inputs, argv, &mut index, cwd)? {
                continue;
            }
            if token.starts_with('-') {
                index += 1;
                continue;
            }
        }

        if should_ignore_fallback_token(token) {
            index += 1;
            continue;
        }

        if has_glob_magic(token) || path_exists(token, cwd) {
            push_inferred_input(&mut inputs, token, cwd)?;
        } else if is_path_shaped(token) {
            ambiguous_missing.push(token.to_string());
        }
        index += 1;
    }

    let mut analysis = SingleCommandAnalysis {
        inputs,
        adapter_ids: vec![CommandAdapterId::Fallback],
        fallback_used: true,
        default_watch_root_used: false,
        filtered_output_count: 0,
        side_effect_profile: SideEffectProfile::ReadOnly,
        status: CommandAnalysisStatus::NoInputs,
    };

    if ambiguous_missing.len() > 1 {
        analysis.status = CommandAnalysisStatus::AmbiguousFallback;
    } else if let Some(token) = ambiguous_missing.first() {
        push_inferred_input(&mut analysis.inputs, token, cwd)?;
    }

    apply_redirects(&mut analysis, redirects, cwd)?;
    Ok(analysis)
}

fn apply_redirects(
    analysis: &mut SingleCommandAnalysis,
    redirects: &[ShellRedirect],
    cwd: &Path,
) -> Result<()> {
    for redirect in redirects {
        if is_dynamic_shell_token(redirect.target.as_str()) {
            continue;
        }
        if redirect.operator.reads_input() {
            push_inferred_input(&mut analysis.inputs, redirect.target.as_str(), cwd)?;
        } else if redirect.operator.writes_output()
            || matches!(redirect.operator, ShellRedirectOperator::Other(_))
        {
            analysis.filtered_output_count += 1;
        }
    }
    Ok(())
}

fn try_push_path_option_value(
    inputs: &mut Vec<WatchInput>,
    argv: &[String],
    index: &mut usize,
    cwd: &Path,
) -> Result<bool> {
    let token = argv[*index].as_str();
    if let Some((option_name, value)) = split_long_option(token) {
        if is_path_option_name(option_name) {
            push_inferred_input(inputs, value, cwd)?;
            *index += 1;
            return Ok(true);
        }
        return Ok(false);
    }

    if token.starts_with("--") && is_path_option_name(token) {
        if let Some(value) = argv.get(*index + 1) {
            push_inferred_input(inputs, value.as_str(), cwd)?;
        }
        *index += 2;
        return Ok(true);
    }

    Ok(false)
}

fn split_long_option(token: &str) -> Option<(&str, &str)> {
    if !token.starts_with("--") {
        return None;
    }
    let (name, value) = token.split_once('=')?;
    Some((name, value))
}

fn is_path_option_name(option_name: &str) -> bool {
    matches!(
        option_name
            .trim_start_matches('-')
            .to_ascii_lowercase()
            .as_str(),
        "file"
            | "files"
            | "files0-from"
            | "path"
            | "paths"
            | "dir"
            | "directory"
            | "input"
            | "inputs"
            | "from"
            | "glob"
            | "arg-file"
            | "reference"
    )
}

fn push_split_inferred_inputs(inputs: &mut Vec<WatchInput>, raw: &str, cwd: &Path) -> Result<()> {
    for value in env::split_paths(&OsString::from(raw)) {
        let value = value.to_string_lossy();
        push_inferred_input(inputs, value.as_ref(), cwd)?;
    }
    Ok(())
}

fn push_inferred_input(inputs: &mut Vec<WatchInput>, raw: &str, cwd: &Path) -> Result<()> {
    let trimmed = raw.trim();
    if trimmed.is_empty() || trimmed == "-" {
        return Ok(());
    }

    let input = if has_glob_magic(trimmed) {
        WatchInput::glob(trimmed, cwd)?
    } else {
        WatchInput::path(trimmed, cwd, WatchInputKind::Inferred)?
    };

    if !inputs.contains(&input) {
        inputs.push(input);
    }

    Ok(())
}

fn push_inferred_path_with_mode(
    inputs: &mut Vec<WatchInput>,
    raw: &str,
    cwd: &Path,
    snapshot_mode: PathSnapshotMode,
) -> Result<()> {
    let trimmed = raw.trim();
    if trimmed.is_empty() || trimmed == "-" {
        return Ok(());
    }

    let input =
        WatchInput::path_with_snapshot_mode(trimmed, cwd, WatchInputKind::Inferred, snapshot_mode)?;

    if !inputs.contains(&input) {
        inputs.push(input);
    }

    Ok(())
}

fn has_glob_magic(raw: &str) -> bool {
    raw.contains('*') || raw.contains('?') || raw.contains('[')
}

fn path_exists(raw: &str, cwd: &Path) -> bool {
    absolutize(raw, cwd).exists()
}

fn is_path_shaped(token: &str) -> bool {
    token.starts_with('/')
        || token.starts_with("./")
        || token.starts_with("../")
        || token.starts_with("~/")
        || token.contains('/')
        || token.contains('\\')
        || token.contains('.')
}

fn looks_like_variable_assignment(token: &str) -> bool {
    let Some((name, _value)) = token.split_once('=') else {
        return false;
    };
    !name.is_empty()
        && name
            .chars()
            .all(|character| character == '_' || character.is_ascii_alphanumeric())
}

fn is_signed_integer(token: &str) -> bool {
    let trimmed = token.trim();
    if trimmed.is_empty() {
        return false;
    }
    let rest = trimmed
        .strip_prefix('+')
        .or_else(|| trimmed.strip_prefix('-'))
        .unwrap_or(trimmed);
    !rest.is_empty() && rest.chars().all(|character| character.is_ascii_digit())
}

fn is_find_expression_token(token: &str) -> bool {
    token == "!" || token == "(" || token == ")" || token.starts_with('-') || token.starts_with(',')
}

fn should_ignore_fallback_token(token: &str) -> bool {
    token.is_empty()
        || is_signed_integer(token)
        || looks_like_variable_assignment(token)
        || is_dynamic_shell_token(token)
}

fn is_dynamic_shell_token(token: &str) -> bool {
    token.starts_with("$(")
        || token.starts_with("`")
        || token.starts_with("<(")
        || token.starts_with(">(")
        || token.starts_with("${")
        || token == "$@"
        || token == "$*"
}

#[cfg(test)]
mod tests {
    use std::{collections::BTreeSet, env, ffi::OsString, fs, path::PathBuf};

    use super::{
        analyze_argv, analyze_shell_expression, help_inventory, render_after_long_help,
        CommandAdapterId, CommandAnalysisStatus, SideEffectProfile,
    };
    use crate::{
        parser::parse_shell_expression,
        snapshot::{PathSnapshotMode, WatchInput},
    };

    #[test]
    fn cp_watches_only_sources() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let analysis = analyze_argv(
            &[
                OsString::from("cp"),
                OsString::from("src.txt"),
                OsString::from("dest.txt"),
            ],
            cwd.path(),
        )
        .expect("analyze");

        assert_eq!(analysis.adapter_ids, vec![CommandAdapterId::CopyLike]);
        assert_eq!(analysis.inputs.len(), 1);
        assert_eq!(analysis.filtered_output_count, 1);
        assert_eq!(
            analysis.side_effect_profile,
            SideEffectProfile::WritesExcludedOutputs
        );
    }

    #[test]
    fn mv_marks_watched_inputs_as_self_mutating() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let analysis = analyze_argv(
            &[
                OsString::from("mv"),
                OsString::from("src.txt"),
                OsString::from("dest.txt"),
            ],
            cwd.path(),
        )
        .expect("analyze");

        assert_eq!(analysis.inputs.len(), 1);
        assert_eq!(
            analysis.side_effect_profile,
            SideEffectProfile::WritesWatchedInputs
        );
    }

    #[test]
    fn grep_ignores_pattern_but_keeps_pattern_files() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let analysis = analyze_argv(
            &[
                OsString::from("grep"),
                OsString::from("hello"),
                OsString::from("file.txt"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(analysis.inputs.len(), 1);

        let analysis = analyze_argv(
            &[
                OsString::from("grep"),
                OsString::from("-f"),
                OsString::from("patterns.txt"),
                OsString::from("file.txt"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(analysis.inputs.len(), 2);

        let grouped = analyze_argv(
            &[
                OsString::from("grep"),
                OsString::from("-rf"),
                OsString::from("patterns.txt"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(grouped.inputs.len(), 2);
    }

    #[test]
    fn ripgrep_and_ag_watch_roots_but_not_patterns() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let pattern_file = cwd.path().join("patterns.txt");
        let ignore_file = cwd.path().join(".rgignore");

        let rg = analyze_argv(
            &[
                OsString::from("rg"),
                OsString::from("TODO"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(rg.adapter_ids, vec![CommandAdapterId::Ripgrep]);
        assert_path_inputs(&rg, &[cwd.path().join("src")]);

        let rg_with_pattern_files = analyze_argv(
            &[
                OsString::from("rg"),
                OsString::from("-g"),
                OsString::from("*.rs"),
                OsString::from("-f"),
                pattern_file.clone().into_os_string(),
                OsString::from("--ignore-file"),
                ignore_file.clone().into_os_string(),
                OsString::from("crates/with-watch"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            rg_with_pattern_files.adapter_ids,
            vec![CommandAdapterId::Ripgrep]
        );
        assert_path_inputs(
            &rg_with_pattern_files,
            &[
                pattern_file,
                ignore_file,
                cwd.path().join("crates/with-watch"),
            ],
        );

        let ag = analyze_argv(
            &[
                OsString::from("ag"),
                OsString::from("TODO"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(ag.adapter_ids, vec![CommandAdapterId::SilverSearcher]);
        assert_path_inputs(&ag, &[cwd.path().join("src")]);

        let rg_with_long_type = analyze_argv(
            &[
                OsString::from("rg"),
                OsString::from("--type"),
                OsString::from("rust"),
                OsString::from("TODO"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            rg_with_long_type.adapter_ids,
            vec![CommandAdapterId::Ripgrep]
        );
        assert_path_inputs(&rg_with_long_type, &[cwd.path().join("src")]);

        let rg_with_long_value_flag = analyze_argv(
            &[
                OsString::from("rg"),
                OsString::from("--max-count"),
                OsString::from("1"),
                OsString::from("TODO"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            rg_with_long_value_flag.adapter_ids,
            vec![CommandAdapterId::Ripgrep]
        );
        assert_path_inputs(&rg_with_long_value_flag, &[cwd.path().join("src")]);

        let rg_with_inline_replace = analyze_argv(
            &[
                OsString::from("rg"),
                OsString::from("-rfoo"),
                OsString::from("TODO"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            rg_with_inline_replace.adapter_ids,
            vec![CommandAdapterId::Ripgrep]
        );
        assert_path_inputs(&rg_with_inline_replace, &[cwd.path().join("src")]);

        let ag_with_path_to_ignore = analyze_argv(
            &[
                OsString::from("ag"),
                OsString::from("-p"),
                OsString::from(".ignore"),
                OsString::from("TODO"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            ag_with_path_to_ignore.adapter_ids,
            vec![CommandAdapterId::SilverSearcher]
        );
        assert_path_inputs(
            &ag_with_path_to_ignore,
            &[cwd.path().join(".ignore"), cwd.path().join("src")],
        );

        let ag_with_inline_path_to_ignore = analyze_argv(
            &[
                OsString::from("ag"),
                OsString::from("--path-to-ignore=.agignore"),
                OsString::from("TODO"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            ag_with_inline_path_to_ignore.adapter_ids,
            vec![CommandAdapterId::SilverSearcher]
        );
        assert_path_inputs(
            &ag_with_inline_path_to_ignore,
            &[cwd.path().join(".agignore"), cwd.path().join("src")],
        );

        let ag_with_filename_pattern = analyze_argv(
            &[
                OsString::from("ag"),
                OsString::from("-g"),
                OsString::from("\\.rs$"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            ag_with_filename_pattern.adapter_ids,
            vec![CommandAdapterId::SilverSearcher]
        );
        assert_path_inputs(&ag_with_filename_pattern, &[cwd.path().join("src")]);
    }

    #[test]
    fn fd_uses_query_pattern_then_roots_and_exec_variants_fall_back() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        fs::create_dir_all(cwd.path().join("proto")).expect("create proto dir");
        fs::create_dir_all(cwd.path().join("src")).expect("create src dir");
        fs::create_dir_all(cwd.path().join("workspace/src")).expect("create nested src dir");

        let analysis = analyze_argv(
            &[
                OsString::from("fd"),
                OsString::from("\\.proto$"),
                OsString::from("proto"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(analysis.adapter_ids, vec![CommandAdapterId::Fd]);
        assert_path_inputs(&analysis, &[cwd.path().join("proto")]);

        let analysis_with_value_flags = analyze_argv(
            &[
                OsString::from("fd"),
                OsString::from("--ignore-file"),
                OsString::from(".fdignore"),
                OsString::from("--max-results"),
                OsString::from("1"),
                OsString::from("TODO"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            analysis_with_value_flags.adapter_ids,
            vec![CommandAdapterId::Fd]
        );
        assert_path_inputs(
            &analysis_with_value_flags,
            &[cwd.path().join(".fdignore"), cwd.path().join("src")],
        );

        let analysis_with_base_directory = analyze_argv(
            &[
                OsString::from("fd"),
                OsString::from("--base-directory"),
                OsString::from("workspace"),
                OsString::from("TODO"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            analysis_with_base_directory.adapter_ids,
            vec![CommandAdapterId::Fd]
        );
        assert_path_inputs(
            &analysis_with_base_directory,
            &[cwd.path().join("workspace/src")],
        );

        let analysis_without_explicit_pattern = analyze_argv(
            &[
                OsString::from("fd"),
                OsString::from("-e"),
                OsString::from("rs"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            analysis_without_explicit_pattern.adapter_ids,
            vec![CommandAdapterId::Fd]
        );
        assert_path_inputs(
            &analysis_without_explicit_pattern,
            &[cwd.path().join("src")],
        );

        let analysis_with_single_positional_pattern =
            analyze_argv(&[OsString::from("fd"), OsString::from("src")], cwd.path())
                .expect("analyze");
        assert_eq!(
            analysis_with_single_positional_pattern.adapter_ids,
            vec![CommandAdapterId::Fd]
        );
        assert!(analysis_with_single_positional_pattern.inputs.is_empty());

        let fallback = analyze_argv(
            &[
                OsString::from("fd"),
                OsString::from("-x"),
                OsString::from("echo"),
                OsString::from("{}"),
                OsString::from("src"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(fallback.adapter_ids, vec![CommandAdapterId::Fallback]);
        assert_path_inputs(&fallback, &[cwd.path().join("src")]);
    }

    #[test]
    fn sed_and_awk_ignore_inline_scripts() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let sed = analyze_argv(
            &[
                OsString::from("sed"),
                OsString::from("-n"),
                OsString::from("1,2p"),
                OsString::from("file.txt"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(sed.inputs.len(), 1);

        let awk = analyze_argv(
            &[
                OsString::from("awk"),
                OsString::from("{print $1}"),
                OsString::from("file.txt"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(awk.inputs.len(), 1);
    }

    #[test]
    fn schema_codegen_adapters_watch_inputs_and_filter_outputs() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let descriptor_set = env::join_paths([
            cwd.path().join("first.binpb"),
            cwd.path().join("second.binpb"),
        ])
        .expect("join descriptor set paths");

        let protoc = analyze_argv(
            &[
                OsString::from("protoc"),
                OsString::from("-I"),
                OsString::from("proto"),
                OsString::from("--descriptor_set_in"),
                descriptor_set.clone(),
                OsString::from("proto/service.proto"),
                OsString::from("--go_out"),
                OsString::from("gen"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(protoc.adapter_ids, vec![CommandAdapterId::Protoc]);
        assert_eq!(protoc.filtered_output_count, 1);
        assert_eq!(
            protoc.side_effect_profile,
            SideEffectProfile::WritesExcludedOutputs
        );
        assert_path_inputs(
            &protoc,
            &[
                cwd.path().join("proto"),
                cwd.path().join("first.binpb"),
                cwd.path().join("second.binpb"),
                cwd.path().join("proto/service.proto"),
            ],
        );

        let flatc = analyze_argv(
            &[
                OsString::from("flatc"),
                OsString::from("--rust"),
                OsString::from("-I"),
                OsString::from("schemas/include"),
                OsString::from("--conform"),
                OsString::from("base.fbs"),
                OsString::from("-o"),
                OsString::from("gen"),
                OsString::from("schema.fbs"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(flatc.adapter_ids, vec![CommandAdapterId::Flatc]);
        assert_eq!(flatc.filtered_output_count, 1);
        assert_path_inputs(
            &flatc,
            &[
                cwd.path().join("schemas/include"),
                cwd.path().join("base.fbs"),
                cwd.path().join("schema.fbs"),
            ],
        );

        let thrift = analyze_argv(
            &[
                OsString::from("thrift"),
                OsString::from("--gen"),
                OsString::from("go"),
                OsString::from("-out"),
                OsString::from("gen"),
                OsString::from("-I"),
                OsString::from("idl"),
                OsString::from("api.thrift"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(thrift.adapter_ids, vec![CommandAdapterId::Thrift]);
        assert_eq!(thrift.filtered_output_count, 1);
        assert_path_inputs(
            &thrift,
            &[cwd.path().join("idl"), cwd.path().join("api.thrift")],
        );

        let capnp = analyze_argv(
            &[
                OsString::from("capnp"),
                OsString::from("compile"),
                OsString::from("-ocapnp"),
                OsString::from("-I"),
                OsString::from("schemas"),
                OsString::from("schema.capnp"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(capnp.adapter_ids, vec![CommandAdapterId::CapnpCompile]);
        assert_eq!(capnp.filtered_output_count, 1);
        assert_path_inputs(
            &capnp,
            &[cwd.path().join("schemas"), cwd.path().join("schema.capnp")],
        );
    }

    #[test]
    fn schema_codegen_fallback_cases_stay_explicit() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        fs::create_dir_all(cwd.path().join("src")).expect("create src dir");

        let protoc_plugin = analyze_argv(
            &[
                OsString::from("protoc"),
                OsString::from("--plugin=protoc-gen-custom=/tmp/plugin"),
                OsString::from("src/service.proto"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(protoc_plugin.adapter_ids, vec![CommandAdapterId::Fallback]);
        assert_path_inputs(&protoc_plugin, &[cwd.path().join("src/service.proto")]);

        let capnp_non_compile = analyze_argv(
            &[
                OsString::from("capnp"),
                OsString::from("eval"),
                OsString::from("schema.capnp"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            capnp_non_compile.adapter_ids,
            vec![CommandAdapterId::Fallback]
        );
        assert_path_inputs(&capnp_non_compile, &[cwd.path().join("schema.capnp")]);
    }

    #[test]
    fn pathless_allowlist_defaults_to_current_directory() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let analysis = analyze_argv(&[OsString::from("ls"), OsString::from("-l")], cwd.path())
            .expect("analyze");

        assert_eq!(analysis.inputs.len(), 1);
        assert!(analysis.default_watch_root_used);
        assert_path_snapshot_mode(
            &analysis.inputs[0],
            cwd.path(),
            PathSnapshotMode::MetadataChildren,
        );

        let analysis = analyze_argv(&[OsString::from("find")], cwd.path()).expect("analyze");
        assert_eq!(analysis.inputs.len(), 1);
        assert!(analysis.default_watch_root_used);

        let analysis = analyze_argv(
            &[
                OsString::from("find"),
                OsString::from("-D"),
                OsString::from("stat"),
                OsString::from("-name"),
                OsString::from("*.rs"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(analysis.inputs.len(), 1);
        assert!(analysis.default_watch_root_used);

        let analysis = analyze_argv(
            &[
                OsString::from("find"),
                OsString::from("-O"),
                OsString::from("3"),
                OsString::from("-name"),
                OsString::from("*.rs"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(analysis.inputs.len(), 1);
        assert!(analysis.default_watch_root_used);
    }

    #[test]
    fn ls_like_inputs_use_listing_snapshot_modes() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        fs::create_dir_all(cwd.path().join("subdir").join("nested")).expect("create nested dir");
        fs::write(cwd.path().join("file.txt"), "alpha\n").expect("write file");

        let default_ls = analyze_argv(&[OsString::from("ls")], cwd.path()).expect("analyze");
        assert_path_snapshot_mode(
            &default_ls.inputs[0],
            cwd.path(),
            PathSnapshotMode::MetadataChildren,
        );

        let directory_ls = analyze_argv(
            &[OsString::from("ls"), OsString::from("subdir")],
            cwd.path(),
        )
        .expect("analyze");
        assert_path_snapshot_mode(
            &directory_ls.inputs[0],
            &cwd.path().join("subdir"),
            PathSnapshotMode::MetadataChildren,
        );

        let recursive_ls = analyze_argv(
            &[
                OsString::from("ls"),
                OsString::from("-R"),
                OsString::from("subdir"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_path_snapshot_mode(
            &recursive_ls.inputs[0],
            &cwd.path().join("subdir"),
            PathSnapshotMode::MetadataTree,
        );

        let directory_flag_ls = analyze_argv(
            &[
                OsString::from("ls"),
                OsString::from("-d"),
                OsString::from("subdir"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_path_snapshot_mode(
            &directory_flag_ls.inputs[0],
            &cwd.path().join("subdir"),
            PathSnapshotMode::MetadataPath,
        );
    }

    #[test]
    fn tar_excludes_archive_outputs_for_create_and_reads_archives_for_extract() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let create = analyze_argv(
            &[
                OsString::from("tar"),
                OsString::from("-cf"),
                OsString::from("out.tar"),
                OsString::from("src"),
                OsString::from("dir"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(create.inputs.len(), 2);
        assert_eq!(create.filtered_output_count, 1);

        let extract = analyze_argv(
            &[
                OsString::from("tar"),
                OsString::from("-xf"),
                OsString::from("archive.tar"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(extract.inputs.len(), 1);
    }

    #[test]
    fn wrappers_unwrap_before_adapter_selection() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let analysis = analyze_argv(
            &[
                OsString::from("env"),
                OsString::from("FOO=bar"),
                OsString::from("grep"),
                OsString::from("hello"),
                OsString::from("file.txt"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            analysis.adapter_ids,
            vec![CommandAdapterId::WrapperEnv, CommandAdapterId::Grep]
        );

        let analysis = analyze_argv(
            &[
                OsString::from("timeout"),
                OsString::from("5"),
                OsString::from("grep"),
                OsString::from("hello"),
                OsString::from("file.txt"),
            ],
            cwd.path(),
        )
        .expect("analyze");
        assert_eq!(
            analysis.adapter_ids,
            vec![CommandAdapterId::WrapperTimeout, CommandAdapterId::Grep]
        );
    }

    #[test]
    fn unknown_command_fallback_ignores_opaque_words() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let analysis = analyze_argv(
            &[
                OsString::from("mystery"),
                OsString::from("hello"),
                OsString::from("1,2p"),
            ],
            cwd.path(),
        )
        .expect("analyze");

        assert_eq!(analysis.adapter_ids, vec![CommandAdapterId::Fallback]);
        assert_eq!(analysis.status, CommandAnalysisStatus::NoInputs);
    }

    #[test]
    fn help_inventory_preserves_source_order_and_grouping() {
        let inventory = help_inventory();

        assert_eq!(
            inventory.wrapper_commands,
            vec!["env", "nice", "nohup", "stdbuf", "timeout"]
        );
        assert_eq!(
            inventory.safe_current_dir_defaults,
            vec!["find", "ls", "dir", "vdir", "du"]
        );
        assert!(inventory
            .dedicated_built_ins
            .starts_with(&["cp", "mv", "install"]));
        assert_eq!(
            inventory.non_watchable_commands,
            &[
                "echo", "printf", "seq", "yes", "sleep", "date", "uname", "pwd", "true", "false",
                "basename", "dirname", "nproc", "printenv", "whoami", "logname", "users", "hostid",
                "numfmt", "mktemp", "mkdir", "mkfifo", "mknod",
            ]
        );

        let dedicated_set = inventory
            .dedicated_built_ins
            .iter()
            .copied()
            .collect::<BTreeSet<_>>();
        assert!(dedicated_set.contains("find"));
        assert!(dedicated_set.contains("grep"));
        assert!(dedicated_set.contains("fgrep"));
        assert!(dedicated_set.contains("rg"));
        assert!(dedicated_set.contains("ag"));
        assert!(dedicated_set.contains("fd"));
        assert!(dedicated_set.contains("protoc"));
        assert!(dedicated_set.contains("flatc"));
        assert!(dedicated_set.contains("thrift"));
        assert!(dedicated_set.contains("capnp"));
        assert!(dedicated_set.contains("chgrp"));
    }

    #[test]
    fn long_help_appendix_renders_full_inventory_sections() {
        let help = render_after_long_help();

        assert!(help.contains("Command modes:"));
        assert!(help.contains("Wrapper commands:"));
        assert!(help.contains("Dedicated built-in adapters and aliases:"));
        assert!(help.contains("Generic read-path commands:"));
        assert!(help.contains("Safe current-directory defaults:"));
        assert!(help.contains("Recognized but not auto-watchable commands:"));
        assert!(help.contains("exec --input escape hatch:"));
        assert!(help.contains("grep, egrep, fgrep, rg, ag"));
        assert!(help.contains("fd, xargs"));
        assert!(help.contains("protoc, flatc, thrift, capnp"));
        assert!(help.contains("find, ls, dir, vdir, du"));
        assert!(help.contains("echo, printf, seq, yes, sleep"));
    }

    #[test]
    fn shell_analysis_keeps_input_redirects_and_filters_outputs() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let parsed =
            parse_shell_expression("grep hello < input.txt > output.txt").expect("parse shell");
        let analysis = analyze_shell_expression(&parsed, cwd.path()).expect("analyze shell");

        assert_eq!(analysis.inputs.len(), 1);
        assert_eq!(analysis.filtered_output_count, 1);
    }

    fn assert_path_snapshot_mode(
        input: &WatchInput,
        expected_path: &std::path::Path,
        expected_snapshot_mode: PathSnapshotMode,
    ) {
        match input {
            WatchInput::Path {
                path,
                snapshot_mode,
                ..
            } => {
                assert_eq!(path, expected_path);
                assert_eq!(*snapshot_mode, expected_snapshot_mode);
            }
            other => panic!("unexpected watch input: {other:?}"),
        }
    }

    fn assert_path_inputs(analysis: &super::CommandAnalysis, expected_paths: &[PathBuf]) {
        let actual = analysis
            .inputs
            .iter()
            .map(|input| match input {
                WatchInput::Path { path, .. } => path.clone(),
                other => panic!("unexpected non-path watch input: {other:?}"),
            })
            .collect::<BTreeSet<_>>();
        let expected = expected_paths.iter().cloned().collect::<BTreeSet<_>>();
        assert_eq!(actual, expected);
    }
}
