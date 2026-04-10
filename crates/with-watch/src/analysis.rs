use std::{ffi::OsString, path::Path};

use crate::{
    error::Result,
    parser::{ParsedShellExpression, ShellRedirect, ShellRedirectOperator},
    snapshot::{WatchInput, WatchInputKind},
};

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
    Sed,
    Awk,
    Find,
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
            Self::Sed => "sed",
            Self::Awk => "awk",
            Self::Find => "find",
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
    let mut analysis = match command_name.as_str() {
        "env" => analyze_env_wrapper(argv, redirects, cwd)?,
        "nice" => analyze_nice_wrapper(argv, redirects, cwd)?,
        "nohup" => analyze_nohup_wrapper(argv, redirects, cwd)?,
        "stdbuf" => analyze_stdbuf_wrapper(argv, redirects, cwd)?,
        "timeout" => analyze_timeout_wrapper(argv, redirects, cwd)?,
        "cp" => analyze_copy_like(
            argv,
            CommandAdapterId::CopyLike,
            SideEffectProfile::WritesExcludedOutputs,
            redirects,
            cwd,
        )?,
        "mv" => analyze_copy_like(
            argv,
            CommandAdapterId::MoveLike,
            SideEffectProfile::WritesWatchedInputs,
            redirects,
            cwd,
        )?,
        "install" => analyze_install(argv, redirects, cwd)?,
        "ln" | "link" => analyze_link_like(argv, redirects, cwd)?,
        "rm" | "unlink" | "rmdir" | "shred" => analyze_remove_like(argv, redirects, cwd)?,
        "sort" => analyze_sort(argv, redirects, cwd)?,
        "uniq" => analyze_uniq(argv, redirects, cwd)?,
        "split" => analyze_split(argv, redirects, cwd)?,
        "csplit" => analyze_csplit(argv, redirects, cwd)?,
        "tee" => analyze_tee(argv, redirects, cwd)?,
        "grep" | "egrep" | "fgrep" => analyze_grep(argv, redirects, cwd)?,
        "sed" => analyze_sed(argv, redirects, cwd)?,
        "awk" | "gawk" | "mawk" | "nawk" => analyze_awk(argv, redirects, cwd)?,
        "find" => analyze_find(argv, redirects, cwd)?,
        "xargs" => analyze_xargs(argv, redirects, cwd)?,
        "tar" => analyze_tar(argv, redirects, cwd)?,
        "touch" => analyze_touch_like(argv, CommandAdapterId::Touch, redirects, cwd)?,
        "truncate" => analyze_touch_like(argv, CommandAdapterId::Truncate, redirects, cwd)?,
        "chmod" | "chown" | "chgrp" => analyze_change_attributes(argv, redirects, cwd)?,
        "dd" => analyze_dd(argv, redirects, cwd)?,
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
    };

    if analysis.adapter_ids.is_empty() {
        analysis.adapter_ids.push(CommandAdapterId::Fallback);
    }

    Ok(analysis.finalize())
}

const DEFAULT_CURRENT_DIR_COMMANDS: &[&str] = &["ls", "dir", "vdir", "du"];
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
                    GrepShortPatternOption::PatternInline => {}
                    GrepShortPatternOption::PatternNext => {
                        index += 2;
                        continue;
                    }
                    GrepShortPatternOption::PatternFileInline(value) => {
                        push_inferred_input(&mut inputs, value, cwd)?;
                    }
                    GrepShortPatternOption::PatternFileNext => {
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
    PatternInline,
    PatternNext,
    PatternFileInline(&'a str),
    PatternFileNext,
}

fn parse_grep_short_pattern_option(token: &str) -> Option<GrepShortPatternOption<'_>> {
    if !token.starts_with('-') || token == "-" || token.starts_with("--") {
        return None;
    }

    let flags = token.trim_start_matches('-');
    for (index, flag) in flags.char_indices() {
        let value = &flags[index + flag.len_utf8()..];
        match flag {
            'e' if value.is_empty() => return Some(GrepShortPatternOption::PatternNext),
            'e' => return Some(GrepShortPatternOption::PatternInline),
            'f' if value.is_empty() => return Some(GrepShortPatternOption::PatternFileNext),
            'f' => return Some(GrepShortPatternOption::PatternFileInline(value)),
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

fn has_glob_magic(raw: &str) -> bool {
    raw.contains('*') || raw.contains('?') || raw.contains('[')
}

fn path_exists(raw: &str, cwd: &Path) -> bool {
    let expanded = expand_tilde(raw);
    let path = Path::new(expanded.as_str());
    let absolute = if path.is_absolute() {
        path.to_path_buf()
    } else {
        cwd.join(path)
    };
    absolute.exists()
}

fn expand_tilde(raw: &str) -> String {
    if let Some(suffix) = raw.strip_prefix("~/") {
        if let Ok(home) = std::env::var("HOME") {
            return format!("{home}/{suffix}");
        }
    }
    raw.to_string()
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
    use std::ffi::OsString;

    use super::{
        analyze_argv, analyze_shell_expression, CommandAdapterId, CommandAnalysisStatus,
        SideEffectProfile,
    };
    use crate::parser::parse_shell_expression;

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
    fn pathless_allowlist_defaults_to_current_directory() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let analysis = analyze_argv(&[OsString::from("ls"), OsString::from("-l")], cwd.path())
            .expect("analyze");

        assert_eq!(analysis.inputs.len(), 1);
        assert!(analysis.default_watch_root_used);

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
    fn shell_analysis_keeps_input_redirects_and_filters_outputs() {
        let cwd = tempfile::tempdir().expect("create tempdir");
        let parsed =
            parse_shell_expression("grep hello < input.txt > output.txt").expect("parse shell");
        let analysis = analyze_shell_expression(&parsed, cwd.path()).expect("analyze shell");

        assert_eq!(analysis.inputs.len(), 1);
        assert_eq!(analysis.filtered_output_count, 1);
    }
}
