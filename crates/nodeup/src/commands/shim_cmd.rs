use std::{
    env, fs,
    path::{Path, PathBuf},
};

use serde::Serialize;
use serde_json::json;
use tracing::info;

use crate::{
    cli::{OutputColorMode, OutputFormat, ShimCommand},
    commands::print_output,
    errors::{ErrorDiagnostics, ErrorKind, NodeupError, Result},
    types::{ManagedAlias, PlatformTarget},
    NodeupApp,
};

const NODEUP_SHIM_DIR: &str = "NODEUP_SHIM_DIR";
const NODEUP_SELF_BIN_PATH: &str = "NODEUP_SELF_BIN_PATH";
const MANAGED_ALIASES: [ManagedAlias; 5] = [
    ManagedAlias::Node,
    ManagedAlias::Npm,
    ManagedAlias::Npx,
    ManagedAlias::Yarn,
    ManagedAlias::Pnpm,
];

#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "kebab-case")]
enum ShimAction {
    Setup,
}

impl ShimAction {
    fn as_str(self) -> &'static str {
        match self {
            Self::Setup => "shim setup",
        }
    }

    fn command_path(self) -> &'static str {
        match self {
            Self::Setup => "nodeup.shim.setup",
        }
    }
}

#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
enum ShimSetupOutcome {
    Created,
    Repaired,
    AlreadyConfigured,
}

impl ShimSetupOutcome {
    fn as_str(self) -> &'static str {
        match self {
            Self::Created => "created",
            Self::Repaired => "repaired",
            Self::AlreadyConfigured => "already-configured",
        }
    }
}

#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
enum ShimEntryStatus {
    Created,
    Repaired,
    Existing,
}

#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "kebab-case")]
enum ShimMethod {
    Symlink,
    Copy,
}

impl ShimMethod {
    fn as_str(self) -> &'static str {
        match self {
            Self::Symlink => "symlink",
            Self::Copy => "copy",
        }
    }
}

#[derive(Debug, Serialize)]
struct ShimEntry {
    alias: &'static str,
    path: String,
    status: ShimEntryStatus,
    method: ShimMethod,
}

#[derive(Debug, Serialize)]
struct ShimSetupResponse {
    action: ShimAction,
    status: ShimSetupOutcome,
    shim_dir: String,
    nodeup_binary: String,
    path_active: bool,
    path_instruction: Option<String>,
    detected_shell: &'static str,
    operating_system: &'static str,
    path_next_steps: Vec<String>,
    verification_commands: Vec<String>,
    shims: Vec<ShimEntry>,
}

struct ShimPlan {
    alias: ManagedAlias,
    path: PathBuf,
    action: ShimPlanAction,
}

#[derive(Clone, Copy)]
enum ShimPlanAction {
    Create,
    Repair,
    Keep,
}

pub fn execute(
    command: ShimCommand,
    output: OutputFormat,
    color: Option<OutputColorMode>,
    _app: &NodeupApp,
) -> Result<i32> {
    match command {
        ShimCommand::Setup { dir } => setup(dir.as_deref(), output, color),
    }
}

fn setup(
    requested_dir: Option<&str>,
    output: OutputFormat,
    color: Option<OutputColorMode>,
) -> Result<i32> {
    let action = ShimAction::Setup;
    let nodeup_binary = env::current_exe().map_err(|error| {
        shim_internal(format!(
            "Failed to resolve current nodeup executable path: {error}"
        ))
    })?;
    let nodeup_binary = normalize_existing_path(&nodeup_binary)?;
    let shim_dir = resolve_shim_dir(requested_dir);
    fs::create_dir_all(&shim_dir)?;

    let method = if host_is_windows() {
        ShimMethod::Copy
    } else {
        ShimMethod::Symlink
    };

    let mut plans = Vec::new();
    for alias in MANAGED_ALIASES {
        let path = shim_path(&shim_dir, alias, method);
        if matches!(method, ShimMethod::Copy) {
            preflight_windows_alias_conflicts(&path)?;
        }
        let action = plan_shim(&path, &nodeup_binary, method)?;
        plans.push(ShimPlan {
            alias,
            path,
            action,
        });
    }

    let mut shims = Vec::new();
    for plan in plans {
        let status = apply_shim_plan(&plan.path, &nodeup_binary, method, plan.action)?;
        shims.push(ShimEntry {
            alias: plan.alias.as_str(),
            path: plan.path.display().to_string(),
            status,
            method,
        });
    }

    let status = summarize_status(&shims);
    let path_active = path_contains_dir(&shim_dir);
    let shell = ShellKind::detect();
    let path_instruction = if path_active {
        None
    } else {
        Some(path_instruction(&shim_dir, shell))
    };
    let path_next_steps = path_next_steps(&shim_dir, shell, path_active);
    let verification_commands = verification_commands(&shim_dir, shell);

    info!(
        command_path = action.command_path(),
        action = action.as_str(),
        outcome = status.as_str(),
        shim_dir = %shim_dir.display(),
        nodeup_binary = %nodeup_binary.display(),
        method = method.as_str(),
        path_active,
        "Processed shim setup"
    );

    let response = ShimSetupResponse {
        action,
        status,
        shim_dir: shim_dir.display().to_string(),
        nodeup_binary: nodeup_binary.display().to_string(),
        path_active,
        path_instruction,
        detected_shell: shell.as_str(),
        operating_system: operating_system_label(),
        path_next_steps,
        verification_commands,
        shims,
    };

    let mut human = format!(
        "Shim setup status: {} (dir: {}, shims: {})",
        response.status.as_str(),
        response.shim_dir,
        response.shims.len()
    );
    if response.path_active {
        human.push_str(" | PATH: active");
    } else if let Some(instruction) = &response.path_instruction {
        human.push_str(&format!(
            " | PATH next step: {instruction} | persist it in your shell profile manually"
        ));
    }
    human.push_str(&format!(
        " | verify shims: {}",
        response.verification_commands.join(" && ")
    ));

    print_output(output, color, &human, &response)?;
    Ok(0)
}

fn plan_shim(path: &Path, nodeup_binary: &Path, method: ShimMethod) -> Result<ShimPlanAction> {
    match method {
        ShimMethod::Symlink => plan_symlink(path, nodeup_binary),
        ShimMethod::Copy => plan_copy(path, nodeup_binary),
    }
}

fn plan_symlink(path: &Path, nodeup_binary: &Path) -> Result<ShimPlanAction> {
    match fs::symlink_metadata(path) {
        Ok(metadata) => {
            if metadata.file_type().is_symlink() {
                let existing_target = fs::read_link(path)?;
                if existing_target == nodeup_binary {
                    return Ok(ShimPlanAction::Keep);
                }
                if !looks_like_nodeup_binary_path(&existing_target, nodeup_binary) {
                    return Err(shim_conflict_with_ownership(
                        path,
                        "external-symlink",
                        "move the existing symlink or choose another shim directory",
                        format!(
                            "Refusing to replace non-nodeup shim target; existing_target={}",
                            existing_target.display()
                        ),
                    ));
                }
                return Ok(ShimPlanAction::Repair);
            }

            if metadata.is_file() && same_file_content(path, nodeup_binary)? {
                return Ok(ShimPlanAction::Repair);
            }

            Err(shim_conflict_with_ownership(
                path,
                "external-command",
                "move the existing command or choose another shim directory",
                "Refusing to replace non-nodeup shim target; existing target is not a \
                 Nodeup-managed shim",
            ))
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(ShimPlanAction::Create),
        Err(error) => Err(error.into()),
    }
}

fn plan_copy(path: &Path, nodeup_binary: &Path) -> Result<ShimPlanAction> {
    match fs::symlink_metadata(path) {
        Ok(metadata) => {
            if metadata.file_type().is_symlink() {
                return Err(shim_conflict_with_ownership(
                    path,
                    "external-symlink",
                    "remove the symlink or choose another shim directory",
                    "Refusing to replace symlink shim target in copy mode; copy-mode Windows \
                     shims require a regular .exe file with a Nodeup ownership marker",
                ));
            }

            if !metadata.is_file() {
                return Err(shim_conflict_with_ownership(
                    path,
                    "external-non-file",
                    "move the existing filesystem entry or choose another shim directory",
                    "Refusing to replace non-file shim target; shim target is not a regular file",
                ));
            }

            let has_copy_marker = has_regular_copy_marker(path)?;

            if same_file_content(path, nodeup_binary)? {
                return Ok(ShimPlanAction::Keep);
            }

            if has_copy_marker {
                return Ok(ShimPlanAction::Repair);
            }

            Err(shim_conflict_with_ownership(
                path,
                "external-command",
                "move the existing command, add the matching Nodeup ownership marker only for a \
                 Nodeup-created copy, or choose another shim directory",
                "Refusing to replace existing shim target with different content; existing file \
                 differs from the Nodeup binary and has no Nodeup ownership marker",
            ))
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            has_regular_copy_marker(path)?;
            Ok(ShimPlanAction::Create)
        }
        Err(error) => Err(error.into()),
    }
}

fn preflight_windows_alias_conflicts(path: &Path) -> Result<()> {
    for candidate in windows_alias_conflict_paths(path) {
        if candidate.exists() {
            return Err(shim_conflict_with_ownership(
                &candidate,
                "external-pathext-command",
                "move the existing PATHEXT command or choose another shim directory",
                "Refusing to create Windows .exe shim because another command name already \
                 exists; Windows command resolution would prefer or collide with this existing \
                 command name",
            ));
        }
    }

    Ok(())
}

fn windows_alias_conflict_paths(path: &Path) -> Vec<PathBuf> {
    let Some(stem) = path.file_stem().and_then(|value| value.to_str()) else {
        return Vec::new();
    };

    windows_command_extensions()
        .into_iter()
        .map(|extension| path.with_file_name(format!("{stem}.{extension}")))
        .collect()
}

fn windows_command_extensions() -> Vec<String> {
    let pathext = env::var("PATHEXT").unwrap_or_else(|_| ".COM;.EXE;.BAT;.CMD".to_string());
    let mut extensions = Vec::new();

    for value in pathext.split(';') {
        let extension = value.trim().trim_start_matches('.');
        if extension.is_empty() || extension.eq_ignore_ascii_case("exe") {
            continue;
        }

        let extension = extension.to_ascii_lowercase();
        if !extensions.contains(&extension) {
            extensions.push(extension);
        }
    }

    extensions
}

fn apply_shim_plan(
    path: &Path,
    nodeup_binary: &Path,
    method: ShimMethod,
    action: ShimPlanAction,
) -> Result<ShimEntryStatus> {
    match (method, action) {
        (ShimMethod::Symlink, ShimPlanAction::Keep) => Ok(ShimEntryStatus::Existing),
        (ShimMethod::Copy, ShimPlanAction::Keep) => {
            write_copy_marker(path)?;
            Ok(ShimEntryStatus::Existing)
        }
        (ShimMethod::Symlink, ShimPlanAction::Create) => {
            create_symlink(nodeup_binary, path)?;
            Ok(ShimEntryStatus::Created)
        }
        (ShimMethod::Symlink, ShimPlanAction::Repair) => {
            fs::remove_file(path)?;
            create_symlink(nodeup_binary, path)?;
            Ok(ShimEntryStatus::Repaired)
        }
        (ShimMethod::Copy, ShimPlanAction::Create) => {
            fs::copy(nodeup_binary, path)?;
            write_copy_marker(path)?;
            Ok(ShimEntryStatus::Created)
        }
        (ShimMethod::Copy, ShimPlanAction::Repair) => {
            fs::copy(nodeup_binary, path)?;
            write_copy_marker(path)?;
            Ok(ShimEntryStatus::Repaired)
        }
    }
}

#[cfg(unix)]
fn create_symlink(source: &Path, target: &Path) -> std::io::Result<()> {
    std::os::unix::fs::symlink(source, target)
}

#[cfg(windows)]
fn create_symlink(source: &Path, target: &Path) -> std::io::Result<()> {
    std::os::windows::fs::symlink_file(source, target)
}

#[cfg(not(any(unix, windows)))]
fn create_symlink(source: &Path, target: &Path) -> std::io::Result<()> {
    fs::copy(source, target).map(|_| ())
}

fn summarize_status(shims: &[ShimEntry]) -> ShimSetupOutcome {
    if shims
        .iter()
        .any(|shim| shim.status == ShimEntryStatus::Repaired)
    {
        ShimSetupOutcome::Repaired
    } else if shims
        .iter()
        .any(|shim| shim.status == ShimEntryStatus::Created)
    {
        ShimSetupOutcome::Created
    } else {
        ShimSetupOutcome::AlreadyConfigured
    }
}

fn resolve_shim_dir(requested_dir: Option<&str>) -> PathBuf {
    if let Some(dir) = requested_dir {
        return PathBuf::from(dir);
    }

    if let Some(dir) = env::var_os(NODEUP_SHIM_DIR) {
        return PathBuf::from(dir);
    }

    home_dir().join(".local").join("bin")
}

fn shim_path(shim_dir: &Path, alias: ManagedAlias, method: ShimMethod) -> PathBuf {
    match method {
        ShimMethod::Symlink => shim_dir.join(alias.as_str()),
        ShimMethod::Copy => shim_dir.join(format!("{}.exe", alias.as_str())),
    }
}

fn path_contains_dir(dir: &Path) -> bool {
    let Ok(path) = env::var("PATH") else {
        return false;
    };

    env::split_paths(&path).any(|entry| paths_equal(&entry, dir))
}

fn paths_equal(left: &Path, right: &Path) -> bool {
    if left == right {
        return true;
    }

    let left = normalize_existing_path(left).unwrap_or_else(|_| left.to_path_buf());
    let right = normalize_existing_path(right).unwrap_or_else(|_| right.to_path_buf());
    left == right
}

#[derive(Clone, Copy)]
enum ShellKind {
    Bash,
    Zsh,
    Fish,
    PowerShellWindows,
    PowerShellUnix,
    Posix,
}

impl ShellKind {
    fn detect() -> Self {
        let shell_name = env::var_os("SHELL")
            .and_then(|value| PathBuf::from(value).file_name().map(|name| name.to_owned()))
            .and_then(|name| name.to_str().map(|value| value.to_ascii_lowercase()));

        match shell_name.as_deref() {
            Some("bash") => Self::Bash,
            Some("zsh") => Self::Zsh,
            Some("fish") => Self::Fish,
            Some("pwsh") | Some("powershell") if host_is_windows() => Self::PowerShellWindows,
            Some("pwsh") | Some("powershell") => Self::PowerShellUnix,
            _ if host_is_windows() => Self::PowerShellWindows,
            _ => Self::Posix,
        }
    }

    fn as_str(self) -> &'static str {
        match self {
            Self::Bash => "bash",
            Self::Zsh => "zsh",
            Self::Fish => "fish",
            Self::PowerShellWindows | Self::PowerShellUnix => "powershell",
            Self::Posix => "posix",
        }
    }
}

fn path_instruction(dir: &Path, shell: ShellKind) -> String {
    let dir = shell_path_text(&dir.display().to_string(), shell);
    match shell {
        ShellKind::PowerShellWindows => format!(
            "$env:Path = '{};' + $env:Path",
            escape_powershell_single_quoted(&dir)
        ),
        ShellKind::PowerShellUnix => format!(
            "$env:PATH = '{}:' + $env:PATH",
            escape_powershell_single_quoted(&dir)
        ),
        ShellKind::Fish => format!("set -gx PATH {} $PATH", shell_single_quote(&dir)),
        ShellKind::Bash | ShellKind::Zsh | ShellKind::Posix => {
            format!("export PATH={}:\"$PATH\"", shell_single_quote(&dir))
        }
    }
}

fn path_next_steps(dir: &Path, shell: ShellKind, path_active: bool) -> Vec<String> {
    let dir = dir.display().to_string();
    let mut steps = Vec::new();

    if path_active {
        steps.push(format!(
            "PATH is active for this process; commands should resolve from {}.",
            dir
        ));
    } else {
        steps.push(format!(
            "Run this command in the current shell: {}",
            path_instruction(Path::new(&dir), shell)
        ));
        steps.push(profile_persistence_hint(&dir, shell));
    }

    steps.push(format!(
        "Verify that node, npm, npx, yarn, and pnpm print paths under {}.",
        dir
    ));
    steps
}

fn profile_persistence_hint(dir: &str, shell: ShellKind) -> String {
    match shell {
        ShellKind::PowerShellWindows | ShellKind::PowerShellUnix => format!(
            "For future PowerShell sessions, add {} to the user PATH or PowerShell profile \
             manually.",
            dir
        ),
        ShellKind::Fish => format!(
            "For future fish sessions, add {} to fish_user_paths or config.fish manually.",
            dir
        ),
        ShellKind::Bash => format!(
            "For future bash sessions, add {} to ~/.bashrc, ~/.bash_profile, or your chosen \
             profile manually.",
            dir
        ),
        ShellKind::Zsh => format!(
            "For future zsh sessions, add {} to ~/.zshrc or your chosen profile manually.",
            dir
        ),
        ShellKind::Posix => format!(
            "For future shell sessions, add {} to the appropriate shell profile manually.",
            dir
        ),
    }
}

fn verification_commands(dir: &Path, shell: ShellKind) -> Vec<String> {
    let dir = shell_path_text(&dir.display().to_string(), shell);
    let dir_prefix = dir.trim_end_matches(['/', '\\']);
    match shell {
        ShellKind::PowerShellWindows => vec![format!(
            "$nodeupShimDir = [IO.Path]::GetFullPath('{}'); foreach ($cmd in \
             'node','npm','npx','yarn','pnpm') {{ $resolved = Get-Command $cmd -ErrorAction Stop; \
             $resolvedPath = [IO.Path]::GetFullPath($resolved.Source); if (-not \
             $resolvedPath.StartsWith($nodeupShimDir.TrimEnd([IO.Path]::DirectorySeparatorChar, \
             [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar, \
             [StringComparison]::OrdinalIgnoreCase)) {{ Write-Output \
             \"nodeup-shim-inactive:$cmd\"; exit 1 }}; $resolved | Select-Object Name,Source }}; \
             Write-Output nodeup-shim-active",
            escape_powershell_single_quoted(dir_prefix)
        )],
        ShellKind::PowerShellUnix => vec![format!(
            "$nodeupShimDir = '{}'; foreach ($cmd in 'node','npm','npx','yarn','pnpm') {{ \
             $resolved = Get-Command $cmd -ErrorAction Stop; if (-not \
             $resolved.Source.StartsWith($nodeupShimDir + [IO.Path]::DirectorySeparatorChar, \
             [StringComparison]::Ordinal)) {{ Write-Output \"nodeup-shim-inactive:$cmd\"; exit 1 \
             }}; $resolved | Select-Object Name,Source }}; Write-Output nodeup-shim-active",
            escape_powershell_single_quoted(dir_prefix)
        )],
        ShellKind::Fish => vec![
            "for cmd in node npm npx yarn pnpm; command -v $cmd; end".to_string(),
            format!(
                "for cmd in node npm npx yarn pnpm; set resolved (command -v $cmd); or exit 1; \
                 set nodeup_shim_prefix {}; test (string sub -s 1 -l (string length -- \
                 $nodeup_shim_prefix) -- $resolved) = $nodeup_shim_prefix; or begin; echo \
                 nodeup-shim-inactive:$cmd; exit 1; end; echo $resolved; end; echo \
                 nodeup-shim-active",
                shell_single_quote(&format!("{}/", dir.trim_end_matches('/')))
            ),
        ],
        ShellKind::Bash | ShellKind::Zsh | ShellKind::Posix => vec![
            "for cmd in node npm npx yarn pnpm; do command -v \"$cmd\"; done".to_string(),
            format!(
                "for cmd in node npm npx yarn pnpm; do resolved=$(command -v \"$cmd\") || exit 1; \
                 case \"$resolved\" in {}/*) ;; *) echo nodeup-shim-inactive:$cmd; exit 1;; esac; \
                 printf '%s\\n' \"$resolved\"; done; echo nodeup-shim-active",
                shell_single_quote(dir_prefix)
            ),
        ],
    }
}

fn shell_path_text(path: &str, shell: ShellKind) -> String {
    if host_is_windows()
        && matches!(
            shell,
            ShellKind::Bash | ShellKind::Zsh | ShellKind::Fish | ShellKind::Posix
        )
    {
        windows_drive_path_to_posix(path).unwrap_or_else(|| path.to_string())
    } else {
        path.to_string()
    }
}

fn windows_drive_path_to_posix(path: &str) -> Option<String> {
    let bytes = path.as_bytes();
    if bytes.len() < 3
        || bytes[1] != b':'
        || !bytes[0].is_ascii_alphabetic()
        || !matches!(bytes[2], b'\\' | b'/')
    {
        return None;
    }

    let drive = (bytes[0] as char).to_ascii_lowercase();
    let rest = path[2..].trim_start_matches(['\\', '/']).replace('\\', "/");
    if rest.is_empty() {
        Some(format!("/{drive}"))
    } else {
        Some(format!("/{drive}/{rest}"))
    }
}

fn operating_system_label() -> &'static str {
    match PlatformTarget::from_host() {
        Some(PlatformTarget::WindowsX64 | PlatformTarget::WindowsArm64) => "windows",
        Some(PlatformTarget::DarwinX64 | PlatformTarget::DarwinArm64) => "macos",
        Some(PlatformTarget::LinuxX64 | PlatformTarget::LinuxArm64) => "linux",
        None if cfg!(target_os = "windows") => "windows",
        None if cfg!(target_os = "macos") => "macos",
        None if cfg!(target_os = "linux") => "linux",
        None => "unix",
    }
}

fn normalize_existing_path(path: &Path) -> Result<PathBuf> {
    if path.exists() {
        return Ok(path.canonicalize()?);
    }
    Ok(path.to_path_buf())
}

fn same_file_content(left: &Path, right: &Path) -> Result<bool> {
    Ok(fs::read(left)? == fs::read(right)?)
}

fn copy_marker_path(path: &Path) -> PathBuf {
    let file_name = path
        .file_name()
        .and_then(|value| value.to_str())
        .unwrap_or("shim");
    path.with_file_name(format!(".{file_name}.nodeup-shim"))
}

fn has_regular_copy_marker(path: &Path) -> Result<bool> {
    let marker = copy_marker_path(path);
    match fs::symlink_metadata(&marker) {
        Ok(metadata) if metadata.file_type().is_symlink() => Err(shim_conflict_with_ownership(
            &marker,
            "external-marker-symlink",
            "remove the symlink marker and retry; Nodeup only trusts regular marker files it can \
             overwrite",
            "Refusing to use symlink Windows shim ownership marker; ownership marker is a symlink",
        )),
        Ok(metadata) if metadata.is_file() => Ok(true),
        Ok(_) => Err(shim_conflict_with_ownership(
            &marker,
            "external-marker-non-file",
            "move the non-file marker and retry; Nodeup only trusts regular marker files",
            "Refusing to use non-file Windows shim ownership marker; ownership marker is not a \
             regular file",
        )),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(false),
        Err(error) => Err(error.into()),
    }
}

pub(super) fn is_nodeup_owned_shim_path(path: &Path) -> bool {
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.file_type().is_symlink() => {
            let Ok(existing_target) = fs::read_link(path) else {
                return false;
            };
            let Some(nodeup_binary) = configured_nodeup_binary_path() else {
                return false;
            };
            looks_like_nodeup_binary_path(&existing_target, &nodeup_binary)
        }
        Ok(metadata) if metadata.is_file() => has_regular_copy_marker(path).unwrap_or(false),
        Ok(_) | Err(_) => false,
    }
}

pub(super) fn is_nodeup_copy_marker_path(path: &Path) -> bool {
    fs::symlink_metadata(path).is_ok_and(|metadata| metadata.is_file())
        && path
            .file_name()
            .and_then(|value| value.to_str())
            .is_some_and(|name| name.starts_with('.') && name.ends_with(".exe.nodeup-shim"))
}

fn write_copy_marker(path: &Path) -> Result<()> {
    has_regular_copy_marker(path)?;
    fs::write(copy_marker_path(path), b"nodeup shim copy\n")?;
    Ok(())
}

fn looks_like_nodeup_binary_path(existing_target: &Path, nodeup_binary: &Path) -> bool {
    existing_target
        .file_name()
        .zip(nodeup_binary.file_name())
        .is_some_and(|(existing, expected)| existing == expected)
}

fn configured_nodeup_binary_path() -> Option<PathBuf> {
    env::var_os(NODEUP_SELF_BIN_PATH)
        .map(PathBuf::from)
        .or_else(|| env::current_exe().ok())
}

fn host_is_windows() -> bool {
    PlatformTarget::from_host().is_some_and(|target| {
        matches!(
            target,
            PlatformTarget::WindowsX64 | PlatformTarget::WindowsArm64
        )
    }) || cfg!(windows)
}

fn home_dir() -> PathBuf {
    env::var_os("HOME")
        .map(PathBuf::from)
        .or_else(|| env::var_os("USERPROFILE").map(PathBuf::from))
        .unwrap_or_else(|| PathBuf::from("."))
}

fn shell_single_quote(value: &str) -> String {
    format!("'{}'", value.replace('\'', "'\"'\"'"))
}

fn escape_powershell_single_quoted(value: &str) -> String {
    value.replace('\'', "''")
}

fn shim_internal(cause: impl Into<String>) -> NodeupError {
    NodeupError::internal_with_hint(
        cause,
        "Retry `nodeup shim setup`. If it keeps failing, run with `RUST_LOG=nodeup=debug` and \
         inspect logs.",
    )
}

fn shim_conflict_with_ownership(
    path: &Path,
    ownership: &'static str,
    remediation: &'static str,
    detail: impl Into<String>,
) -> NodeupError {
    let path = path.display().to_string();
    let detail = detail.into();
    let mut diagnostics = ErrorDiagnostics::new();
    diagnostics.insert(
        "conflicts".to_string(),
        json!([
            {
                "path": path,
                "ownership": ownership,
                "remediation": remediation,
                "detail": detail,
            }
        ]),
    );
    NodeupError::with_hint_and_diagnostics(
        ErrorKind::Conflict,
        format!(
            "Shim setup conflict: path={path}, ownership={ownership}, remediation={remediation}, \
             detail={detail}"
        ),
        "Resolve the listed shim conflict and rerun `nodeup shim setup`; use `nodeup shim setup \
         --dir <path>` to choose a different shim directory.",
        diagnostics,
    )
}

#[cfg(test)]
mod tests {
    use super::windows_drive_path_to_posix;

    #[test]
    fn converts_windows_drive_paths_to_posix_shell_paths() {
        assert_eq!(
            windows_drive_path_to_posix(r"C:\Users\me\.local\bin").as_deref(),
            Some("/c/Users/me/.local/bin")
        );
        assert_eq!(
            windows_drive_path_to_posix("D:/Tools/nodeup").as_deref(),
            Some("/d/Tools/nodeup")
        );
        assert_eq!(windows_drive_path_to_posix("/already/posix"), None);
    }
}
