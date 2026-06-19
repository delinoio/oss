use std::{
    fs,
    path::{Path, PathBuf},
    str::FromStr,
};

use sha2::{Digest, Sha256};
use tracing::{debug, info};

use crate::{
    cli::{
        AddArgs, CacheCommand, Cli, Command, EnvArgs, ExecArgs, ExplainArgs, InfoArgs, InitArgs,
        InstallArgs, RemoveArgs, ScopedArgs, Shell, UpdateArgs, VerifyArgs,
    },
    contract::{HostTarget, Scope, SourceSpec},
    error::{BinpmError, Result},
};

const MANIFEST_FILE: &str = "binpm.toml";
const LOCKFILE_FILE: &str = "binpm.lock";

pub fn run(cli: Cli) -> Result<i32> {
    match cli.command {
        Command::Install(args) => install(args),
        Command::Add(args) => add(args),
        Command::Exec(args) => exec(args),
        Command::Cache(args) => cache(args.command),
        Command::List(args) => list(args),
        Command::Remove(args) => remove(args),
        Command::Info(args) => info_cmd(args),
        Command::Outdated(args) => outdated(args),
        Command::Update(args) => update(args),
        Command::Doctor => doctor(),
        Command::Explain(args) => explain(args),
        Command::Verify(args) => verify(args),
        Command::Init(args) => init(args),
        Command::Env(args) => env_cmd(args),
    }
}

fn install(args: InstallArgs) -> Result<i32> {
    let scope = args.scope.scope();
    let frozen_lockfile = args.lockfile.frozen_lockfile();

    if let Some(source) = &args.source {
        let spec = SourceSpec::from_str(source)?;
        info!(
            command = "install",
            scope = scope.as_str(),
            frozen_lockfile,
            require_verified = args.require_verified,
            no_confirm = args.no_confirm,
            source_provider = spec.provider.as_str(),
            source_host = spec.host,
            source_path = spec.path,
            source_version = spec.version.as_deref().unwrap_or(""),
            "Prepared global install request"
        );
    } else {
        info!(
            command = "install",
            scope = scope.as_str(),
            frozen_lockfile,
            require_verified = args.require_verified,
            no_confirm = args.no_confirm,
            "Prepared local manifest sync request"
        );
    }

    not_implemented("install")
}

fn add(args: AddArgs) -> Result<i32> {
    let spec = SourceSpec::from_str(&args.source)?;
    info!(
        command = "add",
        local_cmd = args.cmd,
        source_provider = spec.provider.as_str(),
        source_host = spec.host,
        source_path = spec.path,
        source_version = spec.version.as_deref().unwrap_or(""),
        frozen_lockfile = args.lockfile.frozen_lockfile(),
        require_verified = args.require_verified,
        no_confirm = args.no_confirm,
        "Prepared local tool declaration request"
    );
    not_implemented("add")
}

fn exec(args: ExecArgs) -> Result<i32> {
    if let Some(source) = &args.package {
        let spec = SourceSpec::from_str(source)?;
        info!(
            command = "x",
            resolved_command = args.cmd,
            explicit_package = true,
            source_provider = spec.provider.as_str(),
            source_host = spec.host,
            source_path = spec.path,
            source_version = spec.version.as_deref().unwrap_or(""),
            forwarded_arg_count = args.args.len(),
            frozen_lockfile = args.lockfile.frozen_lockfile(),
            "Prepared explicit-package execution request"
        );
    } else {
        info!(
            command = "x",
            resolved_command = args.cmd,
            explicit_package = false,
            forwarded_arg_count = args.args.len(),
            frozen_lockfile = args.lockfile.frozen_lockfile(),
            "Prepared local manifest execution request"
        );
    }

    not_implemented("x")
}

fn cache(command: CacheCommand) -> Result<i32> {
    match command {
        CacheCommand::List => {
            info!(
                command = "cache list",
                read_only = true,
                "Prepared cache list request"
            );
            not_implemented("cache list")
        }
        CacheCommand::Prune { .. } => {
            info!(
                command = "cache prune",
                read_only = false,
                "Prepared cache prune request"
            );
            not_implemented("cache prune")
        }
        CacheCommand::Clean { .. } => {
            info!(
                command = "cache clean",
                read_only = false,
                "Prepared cache clean request"
            );
            not_implemented("cache clean")
        }
        CacheCommand::Key => cache_key(),
    }
}

fn cache_key() -> Result<i32> {
    let cwd = current_dir()?;
    let lockfile_path = cwd.join(LOCKFILE_FILE);
    let target = HostTarget::current();
    let digest = lockfile_digest(&lockfile_path)?;
    let cache_key = format!("binpm-v1-{}-{digest}", target.key());

    info!(
        command = "cache key",
        read_only = true,
        target = target.key(),
        lockfile_path = %lockfile_path.display(),
        "Computed binpm cache key"
    );
    println!("{cache_key}");
    Ok(0)
}

fn list(args: ScopedArgs) -> Result<i32> {
    log_read_only_scope("list", args.scope.scope());
    not_implemented("list")
}

fn remove(args: RemoveArgs) -> Result<i32> {
    info!(
        command = "remove",
        selected_scope = args.scope.scope().as_str(),
        local_cmd = args.cmd,
        no_confirm = args.no_confirm,
        "Prepared remove request"
    );
    not_implemented("remove")
}

fn info_cmd(args: InfoArgs) -> Result<i32> {
    if let Ok(spec) = SourceSpec::from_str(&args.cmd_or_source) {
        debug!(
            command = "info",
            source_provider = spec.provider.as_str(),
            source_host = spec.host,
            source_path = spec.path,
            source_version = spec.version.as_deref().unwrap_or(""),
            "Parsed info argument as source"
        );
    }
    log_read_only_scope("info", args.scope.scope());
    not_implemented("info")
}

fn outdated(args: ScopedArgs) -> Result<i32> {
    log_read_only_scope("outdated", args.scope.scope());
    not_implemented("outdated")
}

fn update(args: UpdateArgs) -> Result<i32> {
    info!(
        command = "update",
        selected_scope = args.scope.scope().as_str(),
        selected_count = args.cmd.len(),
        frozen_lockfile = args.lockfile.frozen_lockfile(),
        require_verified = args.require_verified,
        no_confirm = args.no_confirm,
        "Prepared update request"
    );
    not_implemented("update")
}

fn doctor() -> Result<i32> {
    let cwd = current_dir()?;
    let manifest_path = cwd.join(MANIFEST_FILE);
    let lockfile_path = cwd.join(LOCKFILE_FILE);
    let home = binpm_home();

    info!(
        command = "doctor",
        read_only = true,
        manifest_path = %manifest_path.display(),
        lockfile_path = %lockfile_path.display(),
        binpm_home = %home.display(),
        "Prepared doctor inspection"
    );
    println!("binpm doctor");
    println!("manifest: {}", path_state(&manifest_path));
    println!("lockfile: {}", path_state(&lockfile_path));
    println!("global_home: {}", home.display());
    Ok(0)
}

fn explain(args: ExplainArgs) -> Result<i32> {
    if let Ok(spec) = SourceSpec::from_str(&args.cmd_or_source) {
        info!(
            command = "explain",
            read_only = true,
            selected_scope = args.scope.scope().as_str(),
            source_provider = spec.provider.as_str(),
            source_host = spec.host,
            source_path = spec.path,
            source_version = spec.version.as_deref().unwrap_or(""),
            "Prepared source explanation"
        );
    } else {
        info!(
            command = "explain",
            read_only = true,
            selected_scope = args.scope.scope().as_str(),
            local_cmd = args.cmd_or_source,
            "Prepared local command explanation"
        );
    }
    not_implemented("explain")
}

fn verify(args: VerifyArgs) -> Result<i32> {
    info!(
        command = "verify",
        read_only = true,
        selected_scope = args.scope.scope().as_str(),
        require_verified = args.require_verified,
        "Prepared verification request"
    );
    not_implemented("verify")
}

fn init(args: InitArgs) -> Result<i32> {
    let cwd = current_dir()?;
    let manifest_path = cwd.join(MANIFEST_FILE);

    if manifest_path.exists() && !args.force {
        return Err(BinpmError::ManifestExists {
            path: manifest_path,
        });
    }

    fs::write(&manifest_path, "version = 1\n").map_err(|source| BinpmError::WriteFile {
        path: manifest_path.clone(),
        source,
    })?;

    info!(
        command = "init",
        manifest_path = %manifest_path.display(),
        force = args.force,
        "Wrote minimal binpm manifest"
    );
    println!("created {}", manifest_path.display());
    Ok(0)
}

fn env_cmd(args: EnvArgs) -> Result<i32> {
    let home = binpm_home();
    let global_bin = home.join("bin");
    let local_bin = PathBuf::from(".binpm").join("bin");

    info!(
        command = "env",
        shell = args.shell.as_str(),
        read_only = true,
        global_bin = %global_bin.display(),
        local_bin = %local_bin.display(),
        "Rendered PATH environment commands"
    );

    print_env(args.shell, &global_bin, &local_bin);
    Ok(0)
}

fn print_env(shell: Shell, global_bin: &Path, local_bin: &Path) {
    let global = global_bin.display();
    let local = local_bin.display();
    match shell {
        Shell::Bash | Shell::Zsh => {
            println!("export PATH=\"{local}:{global}:$PATH\"");
        }
        Shell::Fish => {
            println!("fish_add_path {local}");
            println!("fish_add_path {global}");
        }
        Shell::Powershell => {
            println!("$env:PATH = \"{local};{global};$env:PATH\"");
        }
    }
}

fn log_read_only_scope(command: &'static str, scope: Scope) {
    info!(
        command,
        read_only = true,
        selected_scope = scope.as_str(),
        "Prepared read-only command request"
    );
}

fn not_implemented(command: &'static str) -> Result<i32> {
    Err(BinpmError::NotImplemented { command })
}

fn lockfile_digest(path: &Path) -> Result<String> {
    let bytes = match fs::read(path) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Vec::new(),
        Err(source) => {
            return Err(BinpmError::ReadFile {
                path: path.to_path_buf(),
                source,
            })
        }
    };

    let digest = Sha256::digest(bytes);
    Ok(format!("{digest:x}"))
}

fn current_dir() -> Result<PathBuf> {
    std::env::current_dir().map_err(BinpmError::CurrentDirectory)
}

fn binpm_home() -> PathBuf {
    std::env::var_os("BINPM_HOME")
        .map(PathBuf::from)
        .or_else(|| std::env::var_os("HOME").map(|home| PathBuf::from(home).join(".binpm")))
        .or_else(|| std::env::var_os("USERPROFILE").map(|home| PathBuf::from(home).join(".binpm")))
        .unwrap_or_else(|| PathBuf::from(".binpm"))
}

fn path_state(path: &Path) -> &'static str {
    if path.exists() {
        "present"
    } else {
        "missing"
    }
}

#[cfg(test)]
mod tests {
    use super::lockfile_digest;

    #[test]
    fn missing_lockfile_has_stable_empty_digest() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let digest = lockfile_digest(&temp_dir.path().join("binpm.lock")).expect("digest");

        assert_eq!(
            digest,
            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
        );
    }
}
