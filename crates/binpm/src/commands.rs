use std::{
    collections::{BTreeMap, BTreeSet},
    env, fs,
    io::{Cursor, ErrorKind, IsTerminal, Read, Write},
    path::{Path, PathBuf},
    process::Command as ProcessCommand,
    str::FromStr,
    thread,
    time::{Duration, Instant},
};

use sha2::{Digest, Sha256};
use tracing::{debug, info, warn};

#[cfg(test)]
use crate::assets::ArtifactKind;
use crate::{
    assets::{
        discover_archive_binary, gitlab_https_eligible, select_asset, target_archive_candidates,
        ArchiveMember, BinaryDiscovery,
    },
    cli::{
        AddArgs, CacheCommand, Cli, Command, EnvArgs, ExecArgs, ExplainArgs, InfoArgs, InitArgs,
        InstallArgs, RemoveArgs, ScopedArgs, Shell, UpdateArgs, VerifyArgs,
    },
    contract::{ArchiveFormat, ChecksumSource, HostTarget, Scope, SourceSpec, TargetOs},
    error::{BinpmError, Result},
    release::{client_for_source, GitHubReleaseClient, GitLabReleaseClient, ReleaseAsset},
    storage::{
        archive_format, cache_asset_is_verified_regular, clean_cache, deterministic_installed_path,
        install_bare_executable, install_executable_bytes, installed_filename,
        list_package_records, managed_installed_path, package_record_from_resolved,
        package_record_path, populate_cache_from_bytes, prune_cache, read_cache_records,
        read_lockfile, read_manifest, read_package_record, record_verified_cache_hit,
        referenced_cache_keys, reject_symlinked_cache_entry, remove_cache_ref,
        remove_installed_binary, remove_package_record, remove_path_if_exists,
        require_regular_managed_file, require_verified_regular_cache_asset, sanitize_persisted_url,
        validate_command_name, validate_download_url, validate_installed_binary_path,
        validate_sha256_digest, write_cache_ref, write_lockfile, write_manifest,
        write_package_record, CachePaths, LockTool, Manifest, ManifestTool, PackageRecord,
        ResolvedAsset, ScopePaths, LOCKFILE_FILE, MANIFEST_FILE,
    },
};

const DOWNLOAD_RETRY_ATTEMPTS: usize = 3;
const DOWNLOAD_RETRY_BASE_DELAY: Duration = Duration::from_millis(200);
const DOWNLOAD_PROGRESS_THRESHOLD_BYTES: u64 = 5 * 1024 * 1024;
const DOWNLOAD_PROGRESS_STEP_BYTES: u64 = 5 * 1024 * 1024;
const DOWNLOAD_PROGRESS_INTERVAL: Duration = Duration::from_secs(2);

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
    let requested_scope = args.scope.scope();
    let frozen_lockfile = args.lockfile.frozen_lockfile();

    if let Some(source) = &args.source {
        let spec = SourceSpec::from_str(source)?;
        let scope = source_install_scope(requested_scope);
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
            "Prepared source install request"
        );
        if scope == Scope::Local {
            return install_local_source(spec, frozen_lockfile, args.require_verified);
        }
        install_global_source(spec, args.require_verified)
    } else {
        if requested_scope == Scope::Global {
            return Err(BinpmError::NotImplemented {
                command: "install --global without a source",
            });
        }
        info!(
            command = "install",
            scope = requested_scope.as_str(),
            frozen_lockfile,
            require_verified = args.require_verified,
            no_confirm = args.no_confirm,
            "Prepared local manifest sync request"
        );
        install_local_manifest(frozen_lockfile, args.require_verified, &[])
    }
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
    let root = require_manifest_root_or_creation_root()?;
    let manifest_path = root.join(MANIFEST_FILE);
    validate_command_name(&args.cmd)?;
    let manifest_existed = path_exists_or_unreadable(&manifest_path);
    let mut manifest = if manifest_existed {
        read_manifest(&manifest_path)?
    } else {
        Manifest {
            version: 1,
            tools: BTreeMap::new(),
        }
    };
    let prior_manifest = manifest.clone();
    let manifest_tool = manifest.tools.get(&args.cmd).cloned();
    let next_manifest_tool = update_manifest_tool_source(manifest_tool.clone(), &spec);
    manifest.tools.insert(args.cmd.clone(), next_manifest_tool);
    ensure_no_selected_install_path_collisions(&manifest, std::slice::from_ref(&args.cmd))?;
    let prior_state = capture_local_tool_state(&root, &args.cmd)?;
    let install = install_local_tool(
        &root,
        &args.cmd,
        &spec,
        manifest_tool.as_ref(),
        args.lockfile.frozen_lockfile(),
        args.require_verified,
    )?;
    let record = install.record.clone();
    if let Err(error) = write_manifest(&manifest_path, &manifest) {
        rollback_local_install_state(&root, &args.cmd, &record, prior_state);
        cleanup_failed_install_cache(
            &CachePaths::new(&binpm_home()?),
            &record.sha256,
            Some(&root),
            &install,
        )?;
        return Err(error);
    }
    if let Err(error) = commit_deferred_cache_hit(&CachePaths::new(&binpm_home()?), &install) {
        rollback_local_install_state(&root, &args.cmd, &record, prior_state);
        if manifest_existed {
            let _ = write_manifest(&manifest_path, &prior_manifest);
        } else {
            let _ = remove_path_if_exists(&manifest_path);
        }
        cleanup_failed_install_cache(
            &CachePaths::new(&binpm_home()?),
            &record.sha256,
            Some(&root),
            &install,
        )?;
        return Err(error);
    }
    println!("added {}", args.cmd);
    Ok(0)
}

fn source_install_scope(requested_scope: Scope) -> Scope {
    match requested_scope {
        Scope::Local => Scope::Local,
        Scope::Global | Scope::Auto => Scope::Global,
    }
}

fn exec(args: ExecArgs) -> Result<i32> {
    let cmd = args.cmd().to_string_lossy().to_string();
    let forwarded_arg_count = args.args().len();

    if let Some(source) = &args.package {
        let spec = SourceSpec::from_str(source)?;
        info!(
            command = "x",
            resolved_command = %cmd,
            explicit_package = true,
            source_provider = spec.provider.as_str(),
            source_host = spec.host,
            source_path = spec.path,
            source_version = spec.version.as_deref().unwrap_or(""),
            forwarded_arg_count,
            frozen_lockfile = args.lockfile.frozen_lockfile(),
            "Prepared explicit-package execution request"
        );
        let home = binpm_home()?;
        let install_root = home.join("tmp").join("x").join(format!(
            "{:x}",
            Sha256::digest(format!("{source}:{cmd}").as_bytes())
        ));
        let scope_paths = ScopePaths {
            root: install_root.clone(),
            bin: install_root.join("bin"),
            packages: install_root.join("packages"),
            tmp: install_root.join("tmp"),
        };
        let cache_paths = CachePaths::new(&home);
        let tool = ManifestTool {
            source: spec.source_without_version(),
            version: spec.version.clone(),
            bin: Some(cmd.clone()),
            targets: BTreeMap::new(),
        };
        let install = install_resolved(
            &scope_paths,
            &cache_paths,
            &cmd,
            &spec,
            Some(&tool),
            false,
            None,
        )?;
        commit_deferred_cache_hit(&cache_paths, &install)?;
        let mut path_entries = vec![scope_paths.bin];
        if let Some(root) = manifest_project_root()? {
            path_entries.push(ScopePaths::local(root).bin);
        }
        return execute_command(&cmd, args.args(), &path_entries);
    } else {
        info!(
            command = "x",
            resolved_command = %cmd,
            explicit_package = false,
            forwarded_arg_count,
            frozen_lockfile = args.lockfile.frozen_lockfile(),
            "Prepared local manifest execution request"
        );
    }

    validate_command_name(&cmd)?;
    let root = require_manifest_root()?;
    let manifest = read_manifest(&root.join(MANIFEST_FILE))?;
    let tool = manifest
        .tools
        .get(&cmd)
        .ok_or_else(|| BinpmError::ExecToolMissing {
            cmd: cmd.clone(),
            manifest: root.join(MANIFEST_FILE),
        })?
        .clone();
    let mut spec = parse_manifest_source(&tool.source)?;
    spec.version = tool.version.clone();
    if local_tool_execution_ready(&root, &cmd, &spec, Some(&tool))? {
        return execute_command(&cmd, args.args(), &[ScopePaths::local(root).bin]);
    }
    let prior_state = capture_local_tool_state(&root, &cmd)?;
    let install = install_local_tool(
        &root,
        &cmd,
        &spec,
        Some(&tool),
        args.lockfile.frozen_lockfile(),
        false,
    )?;
    let cache_paths = CachePaths::new(&binpm_home()?);
    if let Err(error) = commit_deferred_cache_hit(&cache_paths, &install) {
        rollback_local_install_state(&root, &cmd, &install.record, prior_state);
        cleanup_failed_install_cache(&cache_paths, &install.record.sha256, Some(&root), &install)?;
        return Err(error);
    }
    execute_command(&cmd, args.args(), &[ScopePaths::local(root).bin])
}

fn cache(command: CacheCommand) -> Result<i32> {
    match command {
        CacheCommand::List => {
            info!(
                command = "cache list",
                read_only = true,
                "Prepared cache list request"
            );
            let home = binpm_home()?;
            let paths = CachePaths::new(&home);
            let global_paths = ScopePaths::global(home);
            let local_paths = manifest_project_root()?.map(ScopePaths::local);
            let referenced = referenced_cache_keys(&global_paths, local_paths.as_ref(), &paths)?;
            for record in read_cache_records(&paths)? {
                let reference_state = if referenced.contains(&record.cache_key) {
                    "referenced"
                } else {
                    "unreferenced"
                };
                println!(
                    "{} {} {} {}/{} {} {} {} {} {}",
                    record.cache_key,
                    record.byte_size.unwrap_or_default(),
                    record.source_provider.as_str(),
                    record.source_host,
                    record.source_path,
                    record.release_tag,
                    record.asset_name,
                    record.checksum_source.as_str(),
                    record.last_used_at.as_deref().unwrap_or("<unknown>"),
                    reference_state
                );
            }
            Ok(0)
        }
        CacheCommand::Prune { .. } => {
            info!(
                command = "cache prune",
                read_only = false,
                "Prepared cache prune request"
            );
            let home = binpm_home()?;
            let cache_paths = CachePaths::new(&home);
            let global_paths = ScopePaths::global(home);
            let local_paths = manifest_project_root()?.map(ScopePaths::local);
            let referenced =
                referenced_cache_keys(&global_paths, local_paths.as_ref(), &cache_paths)?;
            let removed = prune_cache(&cache_paths, &referenced)?;
            println!("pruned {removed}");
            Ok(0)
        }
        CacheCommand::Clean { .. } => {
            info!(
                command = "cache clean",
                read_only = false,
                "Prepared cache clean request"
            );
            let paths = CachePaths::new(&binpm_home()?);
            let removed = clean_cache(&paths)?;
            println!("cleaned {removed}");
            Ok(0)
        }
        CacheCommand::Key => cache_key(),
    }
}

fn cache_key() -> Result<i32> {
    let project_root = project_root()?;
    let lockfile_path = project_root.join(LOCKFILE_FILE);
    let target = HostTarget::current()?;
    let digest = lockfile_digest(&lockfile_path)?;
    let target_key = target.key();
    let cache_key = format!("binpm-v1-{target_key}-{digest}");

    info!(
        command = "cache key",
        read_only = true,
        target = target_key,
        lockfile_path = %lockfile_path.display(),
        "Computed binpm cache key"
    );
    println!("{cache_key}");
    Ok(0)
}

fn list(args: ScopedArgs) -> Result<i32> {
    let scope = select_scope(args.scope.scope())?;
    log_read_only_scope("list", scope);
    match scope {
        Scope::Local => {
            let root = require_manifest_root()?;
            let manifest = read_manifest(&root.join(MANIFEST_FILE))?;
            let paths = ScopePaths::local(root);
            let mut printed = BTreeSet::new();
            for (cmd, tool) in manifest.tools {
                validate_command_name(&cmd)?;
                printed.insert(cmd.clone());
                let state = package_record_path(&paths, &cmd);
                if state.exists() {
                    let record = read_package_record(&state)?;
                    println!(
                        "{cmd} installed {} {} {} {} {} {}",
                        record.source,
                        record.requested_version.as_deref().unwrap_or("<latest>"),
                        record.release_tag,
                        record.selected_binary,
                        record.installed_path,
                        verification_state(&record)
                    );
                } else {
                    println!(
                        "{cmd} declared {} {} <unknown> <unknown> <unknown> <unknown>",
                        tool.source,
                        tool.version.as_deref().unwrap_or("<latest>")
                    );
                }
            }
            for (cmd, record) in list_package_records(&paths)? {
                if printed.contains(&cmd) {
                    continue;
                }
                println!(
                    "{cmd} installed {} {} {} {} {} {}",
                    record.source,
                    record.requested_version.as_deref().unwrap_or("<latest>"),
                    record.release_tag,
                    record.selected_binary,
                    record.installed_path,
                    verification_state(&record)
                );
            }
        }
        Scope::Global => {
            let paths = ScopePaths::global(binpm_home()?);
            for (cmd, record) in list_package_records(&paths)? {
                println!(
                    "{cmd} installed {} {} {} {} {} {}",
                    record.source,
                    record.requested_version.as_deref().unwrap_or("<latest>"),
                    record.release_tag,
                    record.selected_binary,
                    record.installed_path,
                    verification_state(&record)
                );
            }
        }
        Scope::Auto => unreachable!("select_scope never returns auto"),
    }
    Ok(0)
}

fn remove(args: RemoveArgs) -> Result<i32> {
    info!(
        command = "remove",
        selected_scope = args.scope.scope().as_str(),
        local_cmd = args.cmd,
        no_confirm = args.no_confirm,
        "Prepared remove request"
    );
    let scope = select_scope(args.scope.scope())?;
    match scope {
        Scope::Local => remove_local_tool(&args.cmd),
        Scope::Global => remove_global_tool(&args.cmd),
        Scope::Auto => unreachable!("select_scope never returns auto"),
    }
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
        log_read_only_scope("info", args.scope.scope());
        return print_source_info(&spec);
    }
    log_read_only_scope("info", args.scope.scope());
    let scope = select_scope(args.scope.scope())?;
    let paths = match scope {
        Scope::Local => ScopePaths::local(require_manifest_root()?),
        Scope::Global => ScopePaths::global(binpm_home()?),
        Scope::Auto => unreachable!("select_scope never returns auto"),
    };
    validate_command_name(&args.cmd_or_source)?;
    let record = read_package_record(&package_record_path(&paths, &args.cmd_or_source))?;
    print_package_record_info(&args.cmd_or_source, &record);
    Ok(0)
}

fn outdated(args: ScopedArgs) -> Result<i32> {
    let scope = select_scope(args.scope.scope())?;
    log_read_only_scope("outdated", scope);
    let mut checked = 0usize;
    match scope {
        Scope::Local => {
            let root = require_manifest_root()?;
            let manifest = read_manifest(&root.join(MANIFEST_FILE))?;
            let lockfile = read_lockfile(&root.join(LOCKFILE_FILE))?;
            let target_key = HostTarget::current()?.key();
            for (cmd, tool) in manifest.tools {
                validate_command_name(&cmd)?;
                let mut spec = parse_manifest_source(&tool.source)?;
                spec.version = tool.version.clone();
                let current = lockfile
                    .tools
                    .get(&cmd)
                    .and_then(|tool| tool.targets.get(&target_key))
                    .map(|record| record.release_tag.clone())
                    .unwrap_or_else(|| "<not-installed>".to_string());
                let mut latest_spec = spec.clone();
                latest_spec.version = None;
                let latest = client_for_source(&latest_spec)?
                    .resolve_release(&latest_spec)?
                    .release
                    .tag;
                if current != latest {
                    println!("{cmd} {current} -> {latest}");
                }
                checked += 1;
            }
        }
        Scope::Global => {
            for (cmd, record) in list_package_records(&ScopePaths::global(binpm_home()?))? {
                let mut spec = SourceSpec::from_str(&record.source)?;
                spec.version = None;
                let latest = client_for_source(&spec)?
                    .resolve_release(&spec)?
                    .release
                    .tag;
                if record.release_tag != latest {
                    println!("{cmd} {} -> {latest}", record.release_tag);
                }
                checked += 1;
            }
        }
        Scope::Auto => unreachable!("select_scope never returns auto"),
    }
    println!("checked {checked}");
    Ok(0)
}

fn update(args: UpdateArgs) -> Result<i32> {
    let frozen_lockfile = args.lockfile.frozen_lockfile();
    info!(
        command = "update",
        selected_scope = args.scope.scope().as_str(),
        selected_count = args.cmd.len(),
        frozen_lockfile,
        require_verified = args.require_verified,
        no_confirm = args.no_confirm,
        "Prepared update request"
    );
    match select_scope(args.scope.scope())? {
        Scope::Local if frozen_lockfile => Err(BinpmError::FrozenLockfile {
            path: require_manifest_root()?.join(LOCKFILE_FILE),
        }),
        Scope::Local => install_local_manifest(false, args.require_verified, &args.cmd),
        Scope::Global => Err(BinpmError::NotImplemented {
            command: "update global",
        }),
        Scope::Auto => unreachable!("select_scope never returns auto"),
    }
}

fn doctor() -> Result<i32> {
    let project_root = project_root()?;
    let manifest_path = project_root.join(MANIFEST_FILE);
    let lockfile_path = project_root.join(LOCKFILE_FILE);
    let home = binpm_home()?;

    info!(
        command = "doctor",
        read_only = true,
        project_root = %project_root.display(),
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
        let target = HostTarget::current()?;
        info!(
            command = "explain",
            read_only = true,
            selected_scope = args.scope.scope().as_str(),
            source_provider = spec.provider.as_str(),
            source_host = spec.host,
            source_path = spec.path,
            source_version = spec.version.as_deref().unwrap_or(""),
            target = target.key(),
            "Prepared source explanation"
        );
        return explain_source(spec, target);
    } else {
        info!(
            command = "explain",
            read_only = true,
            selected_scope = args.scope.scope().as_str(),
            local_cmd = args.cmd_or_source,
            "Prepared local command explanation"
        );
    }
    let scope = select_scope(args.scope.scope())?;
    let paths = match scope {
        Scope::Local => ScopePaths::local(require_manifest_root()?),
        Scope::Global => ScopePaths::global(binpm_home()?),
        Scope::Auto => unreachable!("select_scope never returns auto"),
    };
    validate_command_name(&args.cmd_or_source)?;
    let record = read_package_record(&package_record_path(&paths, &args.cmd_or_source))?;
    println!("binpm explain");
    println!("cmd: {}", args.cmd_or_source);
    println!("source: {}", record.source);
    println!("release: {}", record.release_tag);
    println!(
        "target: {}-{}-{}",
        record.target_os.as_str(),
        record.target_arch.as_str(),
        record.target_libc.as_str()
    );
    println!("selected_asset: {}", record.asset_name);
    println!("selected_binary: {}", record.selected_binary);
    println!("archive_format: {}", record.archive_format.as_str());
    println!("checksum_source: {}", record.checksum_source.as_str());
    println!("verification: {}", verification_state(&record));
    Ok(0)
}

fn explain_source(spec: SourceSpec, target: HostTarget) -> Result<i32> {
    println!("binpm explain");
    println!("source: {spec}");
    println!("normalized_source: {}", spec.source_without_version());
    println!("provider: {}", spec.provider.as_str());
    println!("host: {}", spec.host);
    println!("path: {}", spec.path);
    println!(
        "requested_version: {}",
        spec.version.as_deref().unwrap_or("<latest-stable>")
    );
    println!("target: {}", target.key());
    println!("release_api: {}", release_api_url(&spec));

    let client = client_for_source(&spec)?;
    let selection = client.resolve_release(&spec)?;
    println!("release: {}", selection.release.tag);
    println!("release_decision: {}", selection.decision);

    match select_asset(spec.provider, &target, &selection.release.assets) {
        Some(selection) => {
            println!("selected_asset: {}", selection.selected.asset_name);
            println!(
                "selected_asset_url: {}",
                selected_asset_display_url(&selection.selected)?
            );
            println!(
                "selected_asset_score: {}",
                selection.selected.score.unwrap_or_default()
            );
            for decision in selection.decisions {
                println!("{}", decision.explain_line());
            }
        }
        None => {
            println!("selected_asset: <none>");
            for decision in
                crate::assets::score_assets(spec.provider, &target, &selection.release.assets)
            {
                println!("{}", decision.explain_line());
            }
        }
    }

    Ok(0)
}

fn print_source_info(spec: &SourceSpec) -> Result<i32> {
    let target = HostTarget::current()?;
    let selection = client_for_source(spec)?.resolve_release(spec)?;
    println!("binpm info");
    println!("source: {spec}");
    println!("normalized_source: {}", spec.source_without_version());
    println!("provider: {}", spec.provider.as_str());
    println!("host: {}", spec.host);
    println!("path: {}", spec.path);
    println!("release: {}", selection.release.tag);
    println!("target: {}", target.key());
    match select_asset(spec.provider, &target, &selection.release.assets) {
        Some(selection) => {
            println!("selected_asset: {}", selection.selected.asset_name);
            println!(
                "selected_asset_url: {}",
                selected_asset_display_url(&selection.selected)?
            );
            println!(
                "archive_format: {}",
                archive_format(selection.selected.kind)
                    .map(ArchiveFormat::as_str)
                    .unwrap_or("unknown")
            );
        }
        None => println!("selected_asset: <none>"),
    }
    Ok(0)
}

fn print_package_record_info(cmd: &str, record: &PackageRecord) {
    println!("binpm info");
    println!("cmd: {cmd}");
    println!("source: {}", record.source);
    println!("package_spec: {}", record.package_spec);
    println!("release: {}", record.release_tag);
    println!("selected_asset: {}", record.asset_name);
    println!("selected_binary: {}", record.selected_binary);
    println!("installed_path: {}", record.installed_path);
    println!("checksum_source: {}", record.checksum_source.as_str());
    println!("verification: {}", verification_state(record));
}

fn selected_asset_display_url(decision: &crate::assets::CandidateDecision) -> Result<String> {
    sanitize_persisted_url(&decision.canonical_url)
}

fn release_api_url(spec: &SourceSpec) -> String {
    match spec.provider {
        crate::contract::SourceProvider::GitHub => GitHubReleaseClient::releases_api_url(spec),
        crate::contract::SourceProvider::GitLab => GitLabReleaseClient::releases_api_url(spec),
    }
}

fn install_global_source(spec: SourceSpec, require_verified: bool) -> Result<i32> {
    let cmd = repo_name(&spec).to_string();
    validate_command_name(&cmd)?;
    let home = binpm_home()?;
    let scope_paths = ScopePaths::global(home.clone());
    let cache_paths = CachePaths::new(&home);
    let prior_state = capture_runtime_tool_state(&scope_paths, &cmd)?;
    let install = install_resolved(
        &scope_paths,
        &cache_paths,
        &cmd,
        &spec,
        None,
        require_verified,
        None,
    )?;
    let record = install.record.clone();
    if let Err(error) = write_package_record(&scope_paths, &cmd, &record)
        .and_then(|_| commit_deferred_cache_hit(&cache_paths, &install))
    {
        let rollback_result = rollback_failed_install(&scope_paths, &cmd, &record);
        restore_runtime_tool_state(&scope_paths, &cmd, prior_state);
        let cache_cleanup_result =
            cleanup_failed_install_cache(&cache_paths, &record.sha256, None, &install);
        rollback_result?;
        cache_cleanup_result?;
        return Err(error);
    }
    println!("installed {cmd} {}", record.installed_path);
    Ok(0)
}

fn install_local_source(
    spec: SourceSpec,
    frozen_lockfile: bool,
    require_verified: bool,
) -> Result<i32> {
    let root = require_manifest_root()?;
    let cmd = repo_name(&spec).to_string();
    validate_command_name(&cmd)?;
    let manifest_path = root.join(MANIFEST_FILE);
    let mut manifest = read_manifest(&manifest_path)?;
    let prior_manifest = manifest.clone();
    let manifest_tool = manifest.tools.get(&cmd).cloned();
    manifest.tools.insert(
        cmd.clone(),
        update_manifest_tool_source(manifest_tool.clone(), &spec),
    );
    ensure_no_selected_install_path_collisions(&manifest, std::slice::from_ref(&cmd))?;
    let prior_state = capture_local_tool_state(&root, &cmd)?;
    let install = install_local_tool(
        &root,
        &cmd,
        &spec,
        manifest_tool.as_ref(),
        frozen_lockfile,
        require_verified,
    )?;
    let record = install.record.clone();
    if let Err(error) = write_manifest(&manifest_path, &manifest) {
        rollback_local_install_state(&root, &cmd, &record, prior_state);
        cleanup_failed_install_cache(
            &CachePaths::new(&binpm_home()?),
            &record.sha256,
            Some(&root),
            &install,
        )?;
        return Err(error);
    }
    if let Err(error) = commit_deferred_cache_hit(&CachePaths::new(&binpm_home()?), &install) {
        rollback_local_install_state(&root, &cmd, &record, prior_state);
        let _ = write_manifest(&manifest_path, &prior_manifest);
        cleanup_failed_install_cache(
            &CachePaths::new(&binpm_home()?),
            &record.sha256,
            Some(&root),
            &install,
        )?;
        return Err(error);
    }
    Ok(0)
}

fn install_local_manifest(
    frozen_lockfile: bool,
    require_verified: bool,
    selected: &[String],
) -> Result<i32> {
    let root = require_manifest_root()?;
    let manifest = read_manifest(&root.join(MANIFEST_FILE))?;
    for cmd in selected {
        validate_command_name(cmd)?;
        if !manifest.tools.contains_key(cmd) {
            return Err(BinpmError::MissingTool {
                cmd: cmd.clone(),
                manifest: root.join(MANIFEST_FILE),
            });
        }
    }
    validate_selected_manifest_entries(&manifest, selected)?;
    ensure_no_selected_install_path_collisions(&manifest, selected)?;
    let mut completed = Vec::new();
    for (cmd, tool) in &manifest.tools {
        if !selected.is_empty() && !selected.contains(cmd) {
            continue;
        }
        validate_command_name(cmd)?;
        let mut spec = parse_manifest_source(&tool.source)?;
        spec.version = tool.version.clone();
        let prior_state = match capture_local_tool_state(&root, cmd) {
            Ok(prior_state) => prior_state,
            Err(error) => {
                rollback_completed_local_installs(
                    &root,
                    completed,
                    &CachePaths::new(&binpm_home()?),
                )?;
                return Err(error);
            }
        };
        match install_local_tool(
            &root,
            cmd,
            &spec,
            Some(tool),
            frozen_lockfile,
            require_verified,
        ) {
            Ok(install) => completed.push(CompletedLocalInstall {
                cmd: cmd.clone(),
                install,
                prior_state,
            }),
            Err(error) => {
                let cache_paths = CachePaths::new(&binpm_home()?);
                rollback_completed_local_installs(&root, completed, &cache_paths)?;
                return Err(error);
            }
        }
    }
    let orphan_states = if selected.is_empty() {
        match capture_local_manifest_orphan_states(&root, &manifest.tools) {
            Ok(orphan_states) => orphan_states,
            Err(error) => {
                rollback_completed_local_installs(
                    &root,
                    completed,
                    &CachePaths::new(&binpm_home()?),
                )?;
                return Err(error);
            }
        }
    } else {
        Vec::new()
    };
    if selected.is_empty() {
        if let Err(error) = remove_local_manifest_orphans(&root, &manifest.tools, frozen_lockfile) {
            let cache_paths = CachePaths::new(&binpm_home()?);
            rollback_completed_local_installs(&root, completed, &cache_paths)?;
            return Err(error);
        }
    }
    let cache_paths = CachePaths::new(&binpm_home()?);
    let mut committed_deferred_cache_hits: Vec<CacheMetadataSnapshot> = Vec::new();
    for completed_install in &completed {
        if let Some(resolved) = &completed_install.install.deferred_cache_hit {
            let committed_cache_snapshot = match resolved
                .provider_digest_sha256
                .as_deref()
                .map(|sha256| snapshot_cache_metadata(&cache_paths, sha256))
                .transpose()
            {
                Ok(snapshot) => snapshot,
                Err(error) => {
                    let scope_paths = ScopePaths::local(root.clone());
                    for (orphan_cmd, orphan_state) in orphan_states {
                        restore_local_runtime_and_cache_ref(
                            &root,
                            &scope_paths,
                            &cache_paths,
                            &orphan_cmd,
                            orphan_state,
                        );
                    }
                    rollback_completed_local_installs_ref(&root, &completed, &cache_paths)?;
                    for snapshot in committed_deferred_cache_hits {
                        restore_cache_metadata(&cache_paths, &snapshot)?;
                    }
                    return Err(error);
                }
            };
            if let Err(error) = record_verified_cache_hit(&cache_paths, resolved) {
                let scope_paths = ScopePaths::local(root.clone());
                for (orphan_cmd, orphan_state) in orphan_states {
                    restore_local_runtime_and_cache_ref(
                        &root,
                        &scope_paths,
                        &cache_paths,
                        &orphan_cmd,
                        orphan_state,
                    );
                }
                rollback_completed_local_installs_ref(&root, &completed, &cache_paths)?;
                for snapshot in committed_deferred_cache_hits {
                    restore_cache_metadata(&cache_paths, &snapshot)?;
                }
                if let Some(snapshot) = committed_cache_snapshot {
                    restore_cache_metadata(&cache_paths, &snapshot)?;
                }
                return Err(error);
            }
            if let Some(snapshot) = committed_cache_snapshot {
                committed_deferred_cache_hits.push(snapshot);
            }
        }
    }
    Ok(0)
}

fn validate_selected_manifest_entries(manifest: &Manifest, selected: &[String]) -> Result<()> {
    for (cmd, tool) in &manifest.tools {
        if !selected.is_empty() && !selected.contains(cmd) {
            continue;
        }
        validate_command_name(cmd)?;
        parse_manifest_source(&tool.source)?;
    }
    Ok(())
}

fn install_local_tool(
    root: &Path,
    cmd: &str,
    spec: &SourceSpec,
    tool: Option<&ManifestTool>,
    frozen_lockfile: bool,
    require_verified: bool,
) -> Result<InstalledPackage> {
    validate_command_name(cmd)?;
    let lockfile_path = root.join(LOCKFILE_FILE);
    if frozen_lockfile {
        return install_local_from_lock(root, cmd, spec, tool, require_verified);
    }

    let home = binpm_home()?;
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let cache_paths = CachePaths::new(&home);
    let prior_state = capture_local_tool_state(root, cmd)?;
    let mut lockfile = read_lockfile(&lockfile_path)?;
    let install = install_resolved(
        &scope_paths,
        &cache_paths,
        cmd,
        spec,
        tool,
        require_verified,
        Some(root),
    )?;
    let record = install.record.clone();

    let target_key = HostTarget {
        os: record.target_os,
        arch: record.target_arch,
        libc: record.target_libc,
    }
    .key();
    let manifest_tool = tool;
    let lock_tool = lockfile
        .tools
        .entry(cmd.to_string())
        .or_insert_with(|| LockTool {
            source: record.source.clone(),
            targets: BTreeMap::new(),
        });
    lock_tool.source = record.source.clone();
    let mut lock_record = record.lock_record();
    lock_record.installed_path = deterministic_installed_path(cmd, record.target_os);
    if lock_targets_conflict_with_record(lock_tool, &lock_record)
        || lock_targets_conflict_with_manifest(
            &lockfile_path,
            root,
            cmd,
            spec,
            manifest_tool,
            lock_tool,
        )
    {
        lock_tool.targets.clear();
    }
    lock_tool.targets.insert(target_key, lock_record);
    if let Err(error) = write_lockfile(&lockfile_path, &lockfile)
        .and_then(|_| write_package_record(&scope_paths, cmd, &record))
        .and_then(|_| write_cache_ref(&cache_paths, root, cmd, &record))
    {
        rollback_local_install_state(root, cmd, &record, prior_state);
        cleanup_failed_install_cache(&cache_paths, &record.sha256, Some(root), &install)?;
        return Err(error);
    }
    println!("installed {cmd} {}", record.installed_path);
    Ok(InstalledPackage {
        record,
        populated_cache_entry: install.populated_cache_entry,
        deferred_cache_hit: install.deferred_cache_hit,
        cache_metadata_snapshot: install.cache_metadata_snapshot,
    })
}

fn local_tool_execution_ready(
    root: &Path,
    cmd: &str,
    spec: &SourceSpec,
    tool: Option<&ManifestTool>,
) -> Result<bool> {
    let target = HostTarget::current()?;
    let lockfile_path = root.join(LOCKFILE_FILE);
    let lockfile = read_lockfile(&lockfile_path)?;
    let Some(locked_tool) = lockfile.tools.get(cmd) else {
        return Ok(false);
    };
    if locked_tool.source != spec.source_without_version()
        || lock_targets_conflict_with_manifest(&lockfile_path, root, cmd, spec, tool, locked_tool)
    {
        return Ok(false);
    }

    let Some(lock_record) = locked_tool.targets.get(&target.key()) else {
        return Ok(false);
    };
    if lock_record.requested_version != spec.version {
        return Ok(false);
    }
    if assert_lock_record_matches_source_and_target(&lockfile_path, cmd, spec, &target, lock_record)
        .is_err()
        || assert_lock_matches_manifest_tool(root, cmd, tool, &target, lock_record).is_err()
    {
        return Ok(false);
    }

    let paths = ScopePaths::local(root.to_path_buf());
    let runtime_record = match read_package_record(&package_record_path(&paths, cmd)) {
        Ok(record) => record,
        Err(BinpmError::ReadFile { source, .. }) if source.kind() == ErrorKind::NotFound => {
            return Ok(false)
        }
        Err(error) => return Err(error),
    };
    if assert_runtime_record_matches_lock(root, cmd, lock_record, &runtime_record).is_err() {
        return Ok(false);
    }

    let installed_path = validate_installed_binary_path(&paths, cmd, &runtime_record)?;
    match require_regular_managed_file(&installed_path) {
        Ok(()) => {}
        Err(BinpmError::ReadFile { source, .. }) if source.kind() == ErrorKind::NotFound => {
            return Ok(false)
        }
        Err(error) => return Err(error),
    }
    match require_executable_managed_file(&installed_path) {
        Ok(()) => {}
        Err(BinpmError::UnsafeManagedFile { .. }) => return Ok(false),
        Err(error) => return Err(error),
    }
    Ok(true)
}

struct InstalledPackage {
    record: PackageRecord,
    populated_cache_entry: bool,
    deferred_cache_hit: Option<ResolvedAsset>,
    cache_metadata_snapshot: Option<CacheMetadataSnapshot>,
}

struct CompletedLocalInstall {
    cmd: String,
    install: InstalledPackage,
    prior_state: LocalToolState,
}

#[derive(Debug, Clone)]
struct CacheMetadataSnapshot {
    sha256: String,
    asset_bytes: Option<Vec<u8>>,
    bytes: Option<Vec<u8>>,
}

fn commit_deferred_cache_hit(cache_paths: &CachePaths, install: &InstalledPackage) -> Result<()> {
    if let Some(resolved) = &install.deferred_cache_hit {
        record_verified_cache_hit(cache_paths, resolved)?;
    }
    Ok(())
}

fn snapshot_cache_metadata(
    cache_paths: &CachePaths,
    sha256: &str,
) -> Result<CacheMetadataSnapshot> {
    let asset_path = cache_paths.asset_path(sha256);
    let asset_bytes =
        match fs::symlink_metadata(&asset_path) {
            Ok(metadata) if metadata.is_file() => Some(fs::read(&asset_path).map_err(
                |source| BinpmError::ReadFile {
                    path: asset_path.clone(),
                    source,
                },
            )?),
            Ok(_) => None,
            Err(source) if source.kind() == std::io::ErrorKind::NotFound => None,
            Err(source) => {
                return Err(BinpmError::ReadFile {
                    path: asset_path,
                    source,
                })
            }
        };
    let path = cache_paths.metadata_path(sha256);
    let bytes = match fs::read(&path) {
        Ok(bytes) => Some(bytes),
        Err(source) if source.kind() == std::io::ErrorKind::NotFound => None,
        Err(source) => return Err(BinpmError::ReadFile { path, source }),
    };
    Ok(CacheMetadataSnapshot {
        sha256: sha256.to_string(),
        asset_bytes,
        bytes,
    })
}

fn restore_cache_metadata(
    cache_paths: &CachePaths,
    snapshot: &CacheMetadataSnapshot,
) -> Result<()> {
    let asset_path = cache_paths.asset_path(&snapshot.sha256);
    match &snapshot.asset_bytes {
        Some(bytes) => crate::storage::atomic_write_bytes(&asset_path, bytes)?,
        None => remove_path_if_exists(&asset_path)?,
    }
    let path = cache_paths.metadata_path(&snapshot.sha256);
    match &snapshot.bytes {
        Some(bytes) => crate::storage::atomic_write_bytes(&path, bytes),
        None => remove_path_if_exists(&path),
    }
}

fn cleanup_failed_install_cache(
    cache_paths: &CachePaths,
    sha256: &str,
    local_root: Option<&Path>,
    install: &InstalledPackage,
) -> Result<()> {
    if install.populated_cache_entry {
        remove_unreferenced_cache_entry(cache_paths, sha256, local_root)?;
    } else if let Some(snapshot) = &install.cache_metadata_snapshot {
        restore_cache_metadata(cache_paths, snapshot)?;
    }
    Ok(())
}

fn rollback_completed_local_installs(
    root: &Path,
    completed: Vec<CompletedLocalInstall>,
    cache_paths: &CachePaths,
) -> Result<()> {
    let mut first_error = None;
    for completed_install in completed.into_iter().rev() {
        if let Err(error) =
            rollback_one_completed_local_install(root, completed_install, cache_paths)
        {
            first_error.get_or_insert(error);
        }
    }
    if let Some(error) = first_error {
        Err(error)
    } else {
        Ok(())
    }
}

fn rollback_completed_local_installs_ref(
    root: &Path,
    completed: &[CompletedLocalInstall],
    cache_paths: &CachePaths,
) -> Result<()> {
    let mut first_error = None;
    for completed_install in completed.iter().rev() {
        rollback_local_install_state(
            root,
            &completed_install.cmd,
            &completed_install.install.record,
            completed_install.prior_state.clone(),
        );
        if let Err(error) = cleanup_failed_install_cache(
            cache_paths,
            &completed_install.install.record.sha256,
            Some(root),
            &completed_install.install,
        ) {
            first_error.get_or_insert(error);
        }
    }
    if let Some(error) = first_error {
        Err(error)
    } else {
        Ok(())
    }
}

fn rollback_one_completed_local_install(
    root: &Path,
    completed_install: CompletedLocalInstall,
    cache_paths: &CachePaths,
) -> Result<()> {
    rollback_local_install_state(
        root,
        &completed_install.cmd,
        &completed_install.install.record,
        completed_install.prior_state,
    );
    cleanup_failed_install_cache(
        cache_paths,
        &completed_install.install.record.sha256,
        Some(root),
        &completed_install.install,
    )
}

#[cfg(test)]
fn has_current_cache_record(cache_paths: &CachePaths, sha256: &str) -> Result<bool> {
    match crate::storage::read_cache_record(cache_paths, sha256) {
        Ok(record) => Ok(record.is_some_and(|record| {
            record.sha256 == sha256 && record.cache_key == crate::storage::cache_key(sha256)
        })),
        Err(BinpmError::ParseToml { path, .. }) if path == cache_paths.metadata_path(sha256) => {
            Ok(false)
        }
        Err(error) => Err(error),
    }
}

fn install_resolved(
    scope_paths: &ScopePaths,
    cache_paths: &CachePaths,
    cmd: &str,
    spec: &SourceSpec,
    tool: Option<&ManifestTool>,
    require_verified: bool,
    local_root: Option<&Path>,
) -> Result<InstalledPackage> {
    validate_command_name(cmd)?;
    let mut resolved = resolve_asset(spec, tool)?;
    if require_verified && !resolved.checksum_source.is_upstream_verified() {
        return Err(BinpmError::VerificationRequired {
            package: spec.to_string(),
        });
    }
    if resolved.checksum_source == ChecksumSource::Local {
        eprintln!(
            "warning: no upstream checksum or verified signature was available for {}; using a \
             locally computed SHA-256",
            spec
        );
    }
    ensure_no_package_record_install_path_collision(scope_paths, cmd, resolved.target.os)?;
    if let Some(expected) = resolved.provider_digest_sha256.clone() {
        let cache_asset = cache_paths.asset_path(&expected);
        reject_symlinked_cache_entry(cache_paths, &expected)?;
        if cache_asset_is_verified_regular(&cache_asset, &expected)? {
            let installed_path = managed_installed_path(scope_paths, cmd, resolved.target.os);
            let selected_binary = selected_binary_override(tool, &resolved.target)?;
            install_selected_executable(
                &cache_asset,
                &installed_path,
                &mut resolved,
                selected_binary,
            )?;
            let record = package_record_from_resolved(
                cmd,
                &resolved,
                expected,
                &cache_asset,
                &installed_path,
                true,
            )?;
            return Ok(InstalledPackage {
                record,
                populated_cache_entry: false,
                deferred_cache_hit: Some(resolved),
                cache_metadata_snapshot: None,
            });
        }
    }
    let bytes = download_asset(&resolved.decision.download_url)?;
    let sha256 = format!("{:x}", Sha256::digest(&bytes));
    let cache_asset = cache_paths.asset_path(&sha256);
    let had_existing_cache_entry = cache_asset.symlink_metadata().is_ok();
    let had_verified_cache_entry = cache_asset_is_verified_regular(&cache_asset, &sha256)?;
    let cache_metadata_snapshot = if had_existing_cache_entry {
        Some(snapshot_cache_metadata(cache_paths, &sha256)?)
    } else {
        None
    };
    if let Some(expected) = &resolved.provider_digest_sha256 {
        if &sha256 != expected {
            return Err(BinpmError::DigestMismatch {
                path: cache_paths.asset_path(expected),
                expected: expected.clone(),
                actual: sha256,
            });
        }
    }
    let (sha256, cache_asset) = match populate_cache_from_bytes(cache_paths, &resolved, &bytes) {
        Ok(cache_entry) => cache_entry,
        Err(error) => {
            if let Some(snapshot) = &cache_metadata_snapshot {
                restore_cache_metadata(cache_paths, snapshot)?;
            }
            return Err(error);
        }
    };
    let populated_cache_entry = !had_verified_cache_entry;
    let installed_path = managed_installed_path(scope_paths, cmd, resolved.target.os);
    let selected_binary = selected_binary_override(tool, &resolved.target)?;
    if let Err(error) = install_selected_executable(
        &cache_asset,
        &installed_path,
        &mut resolved,
        selected_binary,
    ) {
        if populated_cache_entry {
            remove_unreferenced_cache_entry(cache_paths, &sha256, local_root)?;
        } else if let Some(snapshot) = &cache_metadata_snapshot {
            restore_cache_metadata(cache_paths, snapshot)?;
        }
        return Err(error);
    }
    Ok(InstalledPackage {
        record: package_record_from_resolved(
            cmd,
            &resolved,
            sha256,
            &cache_asset,
            &installed_path,
            true,
        )?,
        populated_cache_entry,
        deferred_cache_hit: None,
        cache_metadata_snapshot,
    })
}

fn install_local_from_lock(
    root: &Path,
    cmd: &str,
    spec: &SourceSpec,
    tool: Option<&ManifestTool>,
    require_verified: bool,
) -> Result<InstalledPackage> {
    validate_command_name(cmd)?;
    let lockfile_path = root.join(LOCKFILE_FILE);
    let lockfile = read_lockfile(&lockfile_path)?;
    let target = HostTarget::current()?;
    let locked_tool = lockfile.tools.get(cmd).ok_or(BinpmError::FrozenLockfile {
        path: lockfile_path.clone(),
    })?;
    if locked_tool.source != spec.source_without_version() {
        return Err(BinpmError::StaleLockfile {
            path: lockfile_path.clone(),
            cmd: cmd.to_string(),
        });
    }
    if lock_targets_conflict_with_manifest(&lockfile_path, root, cmd, spec, tool, locked_tool) {
        return Err(BinpmError::StaleLockfile {
            path: lockfile_path.clone(),
            cmd: cmd.to_string(),
        });
    }
    let record = lockfile
        .tools
        .get(cmd)
        .and_then(|tool| tool.targets.get(&target.key()))
        .cloned()
        .ok_or(BinpmError::FrozenLockfile {
            path: lockfile_path.clone(),
        })?;
    if record.requested_version != spec.version {
        return Err(BinpmError::StaleLockfile {
            path: lockfile_path.clone(),
            cmd: cmd.to_string(),
        });
    }
    assert_lock_record_matches_source_and_target(&lockfile_path, cmd, spec, &target, &record)?;
    assert_lock_matches_manifest_tool(root, cmd, tool, &target, &record)?;
    if require_verified && !record.has_verified_source() {
        return Err(BinpmError::VerificationRequired {
            package: record.package_spec,
        });
    }
    validate_provider_digest_evidence(&record)?;
    validate_locked_record_artifact(&lockfile_path, cmd, &record, &target, tool)?;
    validate_locked_record_current_release(&lockfile_path, cmd, &record)?;
    let home = binpm_home()?;
    let cache_paths = CachePaths::new(&home);
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let prior_state = capture_local_tool_state(root, cmd)?;
    scope_paths.ensure()?;
    cache_paths.ensure()?;
    validate_sha256_digest(&record.sha256)?;
    reject_symlinked_cache_entry(&cache_paths, &record.sha256)?;
    ensure_no_package_record_install_path_collision(&scope_paths, cmd, target.os)?;
    let cache_asset = cache_paths.asset_path(&record.sha256);
    let mut populated_cache_entry = false;
    let had_existing_cache_entry = cache_asset.symlink_metadata().is_ok();
    let cache_metadata_snapshot = if had_existing_cache_entry {
        Some(snapshot_cache_metadata(&cache_paths, &record.sha256)?)
    } else {
        None
    };
    if !cache_asset_is_verified_regular(&cache_asset, &record.sha256)? {
        let repair_result = (|| {
            let download_url = locked_record_download_url(&record)?;
            let bytes = download_asset(&download_url)?;
            let actual = format!("{:x}", Sha256::digest(&bytes));
            if actual != record.sha256 {
                return Err(BinpmError::DigestMismatch {
                    path: cache_asset.clone(),
                    expected: record.sha256.clone(),
                    actual,
                });
            }
            let resolved = ResolvedAsset {
                source: SourceSpec::from_str(
                    &record
                        .requested_version
                        .as_ref()
                        .map(|version| format!("{}@{version}", record.source))
                        .unwrap_or_else(|| record.source.clone()),
                )?,
                release_tag: record.release_tag.clone(),
                target: target.clone(),
                decision: crate::assets::CandidateDecision {
                    asset_name: record.asset_name.clone(),
                    canonical_url: record.asset_url.clone(),
                    download_url,
                    kind: crate::assets::classify_artifact(&record.asset_name, false),
                    detected_os: Some(record.target_os),
                    detected_arch: Some(record.target_arch),
                    detected_libc: Some(record.target_libc),
                    score: None,
                    eligible: true,
                    recognized_pattern: true,
                    rejection_reason: None,
                },
                archive_format: record.archive_format,
                selected_binary: record.selected_binary.clone(),
                provider_digest_sha256: None,
                checksum_source: record.checksum_source,
                signature_available: record.signature_available,
                signature_verified: record.signature_verified,
            };
            populate_cache_from_bytes(&cache_paths, &resolved, &bytes)?;
            Ok(())
        })();
        if let Err(error) = repair_result {
            if let Some(snapshot) = &cache_metadata_snapshot {
                restore_cache_metadata(&cache_paths, snapshot)?;
            }
            return Err(error);
        }
        populated_cache_entry = cache_metadata_snapshot.is_none();
    }

    let installed_path = managed_installed_path(&scope_paths, cmd, target.os);
    let mut resolved_for_install = ResolvedAsset {
        source: SourceSpec::from_str(
            &record
                .requested_version
                .as_ref()
                .map(|version| format!("{}@{version}", record.source))
                .unwrap_or_else(|| record.source.clone()),
        )?,
        release_tag: record.release_tag.clone(),
        target: target.clone(),
        decision: crate::assets::CandidateDecision {
            asset_name: record.asset_name.clone(),
            canonical_url: record.asset_url.clone(),
            download_url: locked_record_download_url(&record)?,
            kind: crate::assets::classify_artifact(&record.asset_name, false),
            detected_os: Some(record.target_os),
            detected_arch: Some(record.target_arch),
            detected_libc: Some(record.target_libc),
            score: None,
            eligible: true,
            recognized_pattern: true,
            rejection_reason: None,
        },
        archive_format: record.archive_format,
        selected_binary: record.selected_binary.clone(),
        provider_digest_sha256: record.provider_digest_sha256.clone(),
        checksum_source: record.checksum_source,
        signature_available: record.signature_available,
        signature_verified: record.signature_verified,
    };
    if let Err(error) = install_selected_executable(
        &cache_paths.asset_path(&record.sha256),
        &installed_path,
        &mut resolved_for_install,
        Some(record.selected_binary.clone()),
    ) {
        let install = InstalledPackage {
            record: record.clone(),
            populated_cache_entry,
            deferred_cache_hit: None,
            cache_metadata_snapshot: cache_metadata_snapshot.clone(),
        };
        cleanup_failed_install_cache(&cache_paths, &record.sha256, Some(root), &install)?;
        return Err(error);
    }
    let mut runtime_record = record;
    runtime_record.cache_key = Some(crate::storage::cache_key(&runtime_record.sha256));
    runtime_record.cache_path = Some(
        cache_paths
            .asset_path(&runtime_record.sha256)
            .display()
            .to_string(),
    );
    runtime_record.installed_at = Some(chrono::Utc::now().to_rfc3339());
    runtime_record.installed_path = installed_path.display().to_string();
    if let Err(error) = write_package_record(&scope_paths, cmd, &runtime_record)
        .and_then(|_| write_cache_ref(&cache_paths, root, cmd, &runtime_record))
    {
        rollback_local_install_state(root, cmd, &runtime_record, prior_state);
        let install = InstalledPackage {
            record: runtime_record.clone(),
            populated_cache_entry,
            deferred_cache_hit: None,
            cache_metadata_snapshot: cache_metadata_snapshot.clone(),
        };
        cleanup_failed_install_cache(&cache_paths, &runtime_record.sha256, Some(root), &install)?;
        return Err(error);
    }
    println!("installed {cmd} {}", runtime_record.installed_path);
    Ok(InstalledPackage {
        record: runtime_record,
        populated_cache_entry,
        deferred_cache_hit: None,
        cache_metadata_snapshot,
    })
}

fn assert_lock_record_matches_source_and_target(
    lockfile_path: &Path,
    cmd: &str,
    spec: &SourceSpec,
    target: &HostTarget,
    record: &PackageRecord,
) -> Result<()> {
    if record.source != spec.source_without_version()
        || record.source_provider != spec.provider
        || record.source_host != spec.host
        || record.source_path != spec.path
        || record.package_spec != expected_package_spec(spec, record)
        || record.target_os != target.os
        || record.target_arch != target.arch
        || record.target_libc != target.libc
        || record.release_tag.trim().is_empty()
    {
        return Err(BinpmError::StaleLockfile {
            path: lockfile_path.to_path_buf(),
            cmd: cmd.to_string(),
        });
    }
    validate_sha256_digest(&record.sha256)?;
    sanitize_persisted_url(&record.asset_url)?;
    if record.installed_path != deterministic_installed_path(cmd, target.os) {
        return Err(BinpmError::StaleLockfile {
            path: lockfile_path.to_path_buf(),
            cmd: cmd.to_string(),
        });
    }
    if record.cache_key.is_some() || record.cache_path.is_some() || record.installed_at.is_some() {
        return Err(BinpmError::StaleLockfile {
            path: lockfile_path.to_path_buf(),
            cmd: cmd.to_string(),
        });
    }
    Ok(())
}

fn expected_package_spec(spec: &SourceSpec, record: &PackageRecord) -> String {
    let source = spec.source_without_version();
    if let Some(version) = &record.requested_version {
        format!("{source}@{version}")
    } else {
        format!("{}@{}", source, record.release_tag)
    }
}

fn validate_locked_record_artifact(
    lockfile_path: &Path,
    cmd: &str,
    record: &PackageRecord,
    target: &HostTarget,
    tool: Option<&ManifestTool>,
) -> Result<()> {
    let kind = crate::assets::classify_artifact(&record.asset_name, false);
    let Some(format) = archive_format(kind) else {
        return Err(BinpmError::AssetNotFound {
            package: record.package_spec.clone(),
            target: target.key(),
        });
    };
    if format != record.archive_format {
        return Err(BinpmError::StaleLockfile {
            path: lockfile_path.to_path_buf(),
            cmd: cmd.to_string(),
        });
    }
    let has_explicit_asset_override = manifest_target_override(tool, target)?
        .map(|override_target| override_target.asset == record.asset_name)
        .unwrap_or(false);
    if !has_explicit_asset_override {
        let scored = crate::assets::score_assets(
            record.source_provider,
            target,
            &[ReleaseAsset {
                name: record.asset_name.clone(),
                url: record.asset_url.clone(),
                provider_url: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            }],
        );
        if !scored
            .first()
            .map(|decision| decision.eligible)
            .unwrap_or(false)
        {
            return Err(BinpmError::AssetNotFound {
                package: record.package_spec.clone(),
                target: target.key(),
            });
        }
    }
    Ok(())
}

fn validate_locked_record_current_release(
    lockfile_path: &Path,
    cmd: &str,
    record: &PackageRecord,
) -> Result<()> {
    let spec = locked_release_lookup_spec(record)?;
    let release = client_for_source(&spec)?.resolve_release(&spec)?.release;
    if release.tag != record.release_tag {
        return Err(BinpmError::StaleLockfile {
            path: lockfile_path.to_path_buf(),
            cmd: cmd.to_string(),
        });
    }
    validate_locked_record_current_asset(lockfile_path, cmd, record, &release.assets)?;
    validate_locked_record_current_provider_digest(lockfile_path, cmd, record, &release.assets)?;
    Ok(())
}

fn validate_locked_record_current_asset(
    lockfile_path: &Path,
    cmd: &str,
    record: &PackageRecord,
    assets: &[ReleaseAsset],
) -> Result<()> {
    for asset in assets
        .iter()
        .filter(|asset| asset.name == record.asset_name)
    {
        if release_asset_display_url(asset)? == record.asset_url {
            return Ok(());
        }
    }
    Err(BinpmError::StaleLockfile {
        path: lockfile_path.to_path_buf(),
        cmd: cmd.to_string(),
    })
}

fn release_asset_display_url(asset: &ReleaseAsset) -> Result<String> {
    let raw = asset
        .provider_url
        .as_deref()
        .unwrap_or(&asset.url)
        .split(['?', '#'])
        .next()
        .unwrap_or(&asset.url);
    sanitize_persisted_url(raw)
}

fn validate_locked_record_current_provider_digest(
    lockfile_path: &Path,
    cmd: &str,
    record: &PackageRecord,
    assets: &[ReleaseAsset],
) -> Result<()> {
    if record_matches_current_provider_digest(record, assets) {
        return Ok(());
    }
    Err(BinpmError::StaleLockfile {
        path: lockfile_path.to_path_buf(),
        cmd: cmd.to_string(),
    })
}

fn record_matches_current_provider_digest(record: &PackageRecord, assets: &[ReleaseAsset]) -> bool {
    let current_digest = assets
        .iter()
        .find(|asset| asset.name == record.asset_name)
        .and_then(|asset| github_sha256_digest(asset.digest.as_deref()));
    match current_digest {
        Some(current_digest) => current_digest == record.sha256,
        None => record.checksum_source != ChecksumSource::GitHubDigest,
    }
}

fn locked_record_download_url(record: &PackageRecord) -> Result<String> {
    let spec = locked_release_lookup_spec(record)?;
    let client = client_for_source(&spec)?;
    let selection = client.resolve_release(&spec)?;
    let asset = selection
        .release
        .assets
        .iter()
        .find(|asset| asset.name == record.asset_name)
        .ok_or_else(|| BinpmError::AssetNotFound {
            package: record.package_spec.clone(),
            target: HostTarget {
                os: record.target_os,
                arch: record.target_arch,
                libc: record.target_libc,
            }
            .key(),
        })?;
    if record.source_provider == crate::contract::SourceProvider::GitLab && asset.source_archive {
        return Err(BinpmError::AssetNotFound {
            package: record.package_spec.clone(),
            target: HostTarget {
                os: record.target_os,
                arch: record.target_arch,
                libc: record.target_libc,
            }
            .key(),
        });
    }
    if record.source_provider == crate::contract::SourceProvider::GitLab
        && !gitlab_https_eligible(asset)
    {
        let diagnostic_url = asset
            .provider_url
            .as_deref()
            .unwrap_or(&asset.url)
            .split(['?', '#'])
            .next()
            .unwrap_or(&asset.url)
            .to_string();
        return Err(BinpmError::UnsafeUrl {
            url: diagnostic_url,
            message: "gitlab asset link is not HTTPS eligible".to_string(),
        });
    }
    Ok(asset
        .provider_url
        .as_deref()
        .unwrap_or(&asset.url)
        .to_string())
}

fn locked_release_lookup_spec(record: &PackageRecord) -> Result<SourceSpec> {
    let mut spec = SourceSpec::from_str(&record.source)?;
    spec.version = Some(record.release_tag.clone());
    Ok(spec)
}

fn assert_lock_matches_manifest_tool(
    root: &Path,
    cmd: &str,
    tool: Option<&ManifestTool>,
    target: &HostTarget,
    record: &PackageRecord,
) -> Result<()> {
    if let Some(override_target) = manifest_target_override(tool, target)? {
        if let Some(checksum_source) = override_target.checksum_source {
            if !(checksum_source == ChecksumSource::GitHubDigest
                && record.source_provider == crate::contract::SourceProvider::GitHub
                && record.checksum_source == ChecksumSource::GitHubDigest
                && record.provider_digest_sha256.as_deref() == Some(record.sha256.as_str()))
            {
                return Err(BinpmError::UnverifiedChecksumSourceOverride {
                    checksum_source: checksum_source.as_str().to_string(),
                });
            }
        }
        if record.asset_name != override_target.asset
            || !manifest_bin_matches_record(&override_target.bin, &record.selected_binary)
        {
            return Err(BinpmError::StaleLockfile {
                path: root.join(LOCKFILE_FILE),
                cmd: cmd.to_string(),
            });
        }
        return Ok(());
    }
    if let Some(bin) = tool.and_then(|tool| tool.bin.as_ref()) {
        if !manifest_bin_matches_record(bin, &record.selected_binary) {
            return Err(BinpmError::StaleLockfile {
                path: root.join(LOCKFILE_FILE),
                cmd: cmd.to_string(),
            });
        }
    }
    Ok(())
}

fn manifest_bin_matches_record(manifest_bin: &str, record_selected_binary: &str) -> bool {
    record_selected_binary == manifest_bin
        || archive_basename(record_selected_binary) == manifest_bin
}

fn resolve_asset(spec: &SourceSpec, tool: Option<&ManifestTool>) -> Result<ResolvedAsset> {
    let target = HostTarget::current()?;
    let client = client_for_source(spec)?;
    let release = client.resolve_release(spec)?.release;
    let decision = select_manifest_asset(spec, tool, &target, &release.assets)?;
    let archive_format =
        archive_format(decision.kind).ok_or_else(|| BinpmError::AssetNotFound {
            package: spec.to_string(),
            target: target.key(),
        })?;
    let selected_binary = match selected_binary_override(tool, &target)? {
        Some(bin) => bin,
        None if matches!(archive_format, ArchiveFormat::BareExecutable) => {
            decision.asset_name.clone()
        }
        None => String::new(),
    };
    let provider_digest_sha256 = release
        .assets
        .iter()
        .find(|asset| asset.name == decision.asset_name)
        .and_then(|asset| github_sha256_digest(asset.digest.as_deref()));
    let manifest_checksum_source =
        manifest_checksum_source(tool, &target, provider_digest_sha256.as_deref())?;
    let checksum_source = if spec.provider == crate::contract::SourceProvider::GitHub
        && provider_digest_sha256.is_some()
    {
        ChecksumSource::GitHubDigest
    } else {
        manifest_checksum_source
    };
    Ok(ResolvedAsset {
        source: spec.clone(),
        release_tag: release.tag,
        target,
        decision,
        archive_format,
        selected_binary,
        provider_digest_sha256,
        checksum_source,
        signature_available: false,
        signature_verified: false,
    })
}

fn selected_binary_override(
    tool: Option<&ManifestTool>,
    target: &HostTarget,
) -> Result<Option<String>> {
    Ok(manifest_target_override(tool, target)?
        .map(|override_target| override_target.bin.clone())
        .or_else(|| tool.and_then(|tool| tool.bin.clone())))
}

fn install_selected_executable(
    cache_asset: &Path,
    installed_path: &Path,
    resolved: &mut ResolvedAsset,
    selected_binary: Option<String>,
) -> Result<()> {
    match resolved.archive_format {
        ArchiveFormat::BareExecutable => {
            resolved.selected_binary = selected_binary
                .unwrap_or_else(|| resolved.selected_binary.clone())
                .trim()
                .to_string();
            if resolved.selected_binary.is_empty() {
                resolved.selected_binary = resolved.decision.asset_name.clone();
            }
            install_bare_executable(cache_asset, installed_path)
        }
        format => {
            let repo = repo_name(&resolved.source);
            let selected = read_archive_selected_binary(
                cache_asset,
                format,
                &resolved.decision.asset_name,
                repo,
                &resolved.target,
                selected_binary.as_deref(),
            )?;
            resolved.selected_binary = selected_binary
                .as_deref()
                .map(str::trim)
                .filter(|bin| !bin.is_empty())
                .unwrap_or(&selected.path)
                .to_string();
            install_executable_bytes(installed_path, &selected.bytes)
        }
    }
}

#[derive(Debug)]
struct SelectedArchiveBinary {
    path: String,
    bytes: Vec<u8>,
}

fn read_archive_selected_binary(
    archive_path: &Path,
    format: ArchiveFormat,
    asset_name: &str,
    repo_name: &str,
    target: &HostTarget,
    explicit_binary: Option<&str>,
) -> Result<SelectedArchiveBinary> {
    let bytes = fs::read(archive_path).map_err(|source| BinpmError::ReadFile {
        path: archive_path.to_path_buf(),
        source,
    })?;
    match format {
        ArchiveFormat::TarGz | ArchiveFormat::Tgz => {
            let decoder = flate2::read::GzDecoder::new(Cursor::new(bytes));
            read_tar_selected_binary(decoder, asset_name, repo_name, target, explicit_binary)
        }
        ArchiveFormat::TarXz | ArchiveFormat::Txz => {
            let decoder = xz2::read::XzDecoder::new(Cursor::new(bytes));
            read_tar_selected_binary(decoder, asset_name, repo_name, target, explicit_binary)
        }
        ArchiveFormat::TarZst => {
            let decoder =
                zstd::stream::read::Decoder::new(Cursor::new(bytes)).map_err(|error| {
                    BinpmError::ArchiveExtraction {
                        asset: asset_name.to_string(),
                        message: error.to_string(),
                    }
                })?;
            read_tar_selected_binary(decoder, asset_name, repo_name, target, explicit_binary)
        }
        ArchiveFormat::Zip => read_zip_selected_binary(
            Cursor::new(bytes),
            asset_name,
            repo_name,
            target,
            explicit_binary,
        ),
        ArchiveFormat::BareExecutable => unreachable!("bare executable is not an archive"),
    }
}

fn read_tar_selected_binary<R: Read>(
    reader: R,
    asset_name: &str,
    repo_name: &str,
    target: &HostTarget,
    explicit_binary: Option<&str>,
) -> Result<SelectedArchiveBinary> {
    let mut archive = tar::Archive::new(reader);
    let mut members = Vec::new();
    let mut member_bytes = BTreeMap::new();
    for entry in archive
        .entries()
        .map_err(|error| BinpmError::ArchiveExtraction {
            asset: asset_name.to_string(),
            message: error.to_string(),
        })?
    {
        let mut entry = entry.map_err(|error| BinpmError::ArchiveExtraction {
            asset: asset_name.to_string(),
            message: error.to_string(),
        })?;
        let entry_type = entry.header().entry_type();
        if !entry_type.is_file() {
            if entry_type.is_symlink() || entry_type.is_hard_link() {
                let path = entry
                    .path()
                    .map_err(|error| BinpmError::ArchiveExtraction {
                        asset: asset_name.to_string(),
                        message: error.to_string(),
                    })?;
                let path = validate_archive_member_path(asset_name, &path)?;
                return Err(BinpmError::UnsafeArchivePath {
                    asset: asset_name.to_string(),
                    path,
                    message: "symlinks and hard links are not installable".to_string(),
                });
            }
            continue;
        }
        let path = entry
            .path()
            .map_err(|error| BinpmError::ArchiveExtraction {
                asset: asset_name.to_string(),
                message: error.to_string(),
            })?;
        let path = validate_archive_member_path(asset_name, &path)?;
        let executable = entry
            .header()
            .mode()
            .map(|mode| mode & 0o111 != 0)
            .unwrap_or(false)
            || archive_exe_is_executable(&path, target);
        let mut bytes = Vec::new();
        entry
            .read_to_end(&mut bytes)
            .map_err(|error| BinpmError::ArchiveExtraction {
                asset: asset_name.to_string(),
                message: error.to_string(),
            })?;
        if member_bytes.contains_key(&path) {
            return Err(duplicate_archive_member(asset_name, &path));
        }
        members.push(ArchiveMember {
            path: path.clone(),
            executable,
        });
        member_bytes.insert(path, bytes);
    }
    select_archive_member(
        asset_name,
        repo_name,
        target,
        explicit_binary,
        members,
        member_bytes,
    )
}

fn read_zip_selected_binary<R: Read + std::io::Seek>(
    reader: R,
    asset_name: &str,
    repo_name: &str,
    target: &HostTarget,
    explicit_binary: Option<&str>,
) -> Result<SelectedArchiveBinary> {
    let mut archive =
        zip::ZipArchive::new(reader).map_err(|error| BinpmError::ArchiveExtraction {
            asset: asset_name.to_string(),
            message: error.to_string(),
        })?;
    let mut members = Vec::new();
    let mut member_bytes = BTreeMap::new();
    for index in 0..archive.len() {
        let mut file = archive
            .by_index(index)
            .map_err(|error| BinpmError::ArchiveExtraction {
                asset: asset_name.to_string(),
                message: error.to_string(),
            })?;
        let path = validate_archive_member_path(asset_name, Path::new(file.name()))?;
        if file.is_dir() {
            continue;
        }
        if zip_file_is_symlink(file.unix_mode()) {
            return Err(BinpmError::UnsafeArchivePath {
                asset: asset_name.to_string(),
                path,
                message: "symlinks and hard links are not installable".to_string(),
            });
        }
        if !zip_file_is_regular(file.unix_mode()) {
            return Err(BinpmError::UnsafeArchivePath {
                asset: asset_name.to_string(),
                path,
                message: "non-regular zip entries are not installable".to_string(),
            });
        }
        let executable = file
            .unix_mode()
            .map(|mode| mode & 0o111 != 0)
            .unwrap_or(false)
            || archive_exe_is_executable(&path, target);
        let mut bytes = Vec::new();
        file.read_to_end(&mut bytes)
            .map_err(|error| BinpmError::ArchiveExtraction {
                asset: asset_name.to_string(),
                message: error.to_string(),
            })?;
        if member_bytes.contains_key(&path) {
            return Err(duplicate_archive_member(asset_name, &path));
        }
        members.push(ArchiveMember {
            path: path.clone(),
            executable,
        });
        member_bytes.insert(path, bytes);
    }
    select_archive_member(
        asset_name,
        repo_name,
        target,
        explicit_binary,
        members,
        member_bytes,
    )
}

fn zip_file_is_symlink(unix_mode: Option<u32>) -> bool {
    const UNIX_FILE_TYPE_MASK: u32 = 0o170000;
    const UNIX_SYMLINK_TYPE: u32 = 0o120000;
    unix_mode
        .map(|mode| mode & UNIX_FILE_TYPE_MASK == UNIX_SYMLINK_TYPE)
        .unwrap_or(false)
}

fn zip_file_is_regular(unix_mode: Option<u32>) -> bool {
    const UNIX_FILE_TYPE_MASK: u32 = 0o170000;
    const UNIX_REGULAR_TYPE: u32 = 0o100000;
    unix_mode
        .map(|mode| {
            let file_type = mode & UNIX_FILE_TYPE_MASK;
            file_type == 0 || file_type == UNIX_REGULAR_TYPE
        })
        .unwrap_or(true)
}

fn archive_exe_is_executable(path: &str, target: &HostTarget) -> bool {
    target.os == TargetOs::Windows && path.to_ascii_lowercase().ends_with(".exe")
}

fn duplicate_archive_member(asset_name: &str, path: &str) -> BinpmError {
    BinpmError::UnsafeArchivePath {
        asset: asset_name.to_string(),
        path: path.to_string(),
        message: "duplicate archive member path is not allowed".to_string(),
    }
}

fn select_archive_member(
    asset_name: &str,
    repo_name: &str,
    target: &HostTarget,
    explicit_binary: Option<&str>,
    members: Vec<ArchiveMember>,
    mut member_bytes: BTreeMap<String, Vec<u8>>,
) -> Result<SelectedArchiveBinary> {
    let selected_path = if let Some(explicit_binary) = explicit_binary {
        let explicit_binary = explicit_binary.trim();
        if explicit_binary.is_empty() {
            return Err(BinpmError::ArchiveMemberNotFound {
                asset: asset_name.to_string(),
                member: explicit_binary.to_string(),
            });
        }
        let explicit_path = validate_archive_member_path(asset_name, Path::new(explicit_binary))?;
        if members
            .iter()
            .any(|member| member.path == explicit_path && member.executable)
        {
            explicit_path
        } else {
            let matches = members
                .iter()
                .filter(|member| {
                    member.executable
                        && archive_binary_name_matches(target, &member.path, explicit_binary)
                })
                .map(|member| member.path.clone())
                .collect::<Vec<_>>();
            let matches = target_archive_candidates(target, matches);
            match matches.as_slice() {
                [path] => path.clone(),
                [] => {
                    return Err(BinpmError::ArchiveMemberNotFound {
                        asset: asset_name.to_string(),
                        member: explicit_path,
                    })
                }
                _ => {
                    return Err(BinpmError::AmbiguousArchiveBinaries {
                        asset: asset_name.to_string(),
                        candidates: matches,
                    })
                }
            }
        }
    } else {
        match discover_archive_binary(repo_name, target, &members) {
            BinaryDiscovery::Selected(path) => path,
            BinaryDiscovery::Ambiguous(candidates) => {
                return Err(BinpmError::AmbiguousArchiveBinaries {
                    asset: asset_name.to_string(),
                    candidates,
                })
            }
            BinaryDiscovery::NotFound => {
                return Err(BinpmError::ArchiveBinaryNotFound {
                    asset: asset_name.to_string(),
                })
            }
        }
    };
    let bytes =
        member_bytes
            .remove(&selected_path)
            .ok_or_else(|| BinpmError::ArchiveMemberNotFound {
                asset: asset_name.to_string(),
                member: selected_path.clone(),
            })?;
    Ok(SelectedArchiveBinary {
        path: selected_path,
        bytes,
    })
}

fn archive_basename(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}

fn archive_binary_name_matches(target: &HostTarget, path: &str, expected: &str) -> bool {
    let basename = archive_basename(path);
    if basename == expected {
        return true;
    }
    if target.os != TargetOs::Windows {
        return false;
    }
    basename.eq_ignore_ascii_case(expected)
        || basename
            .to_ascii_lowercase()
            .strip_suffix(".exe")
            .is_some_and(|stripped| stripped.eq_ignore_ascii_case(expected))
}

fn validate_archive_member_path(asset_name: &str, path: &Path) -> Result<String> {
    if path.is_absolute() {
        return Err(BinpmError::UnsafeArchivePath {
            asset: asset_name.to_string(),
            path: path.display().to_string(),
            message: "absolute paths are not allowed".to_string(),
        });
    }
    let mut parts = Vec::new();
    for component in path.components() {
        match component {
            std::path::Component::Normal(part) => parts.push(part.to_string_lossy().into_owned()),
            std::path::Component::CurDir => {}
            std::path::Component::ParentDir => {
                return Err(BinpmError::UnsafeArchivePath {
                    asset: asset_name.to_string(),
                    path: path.display().to_string(),
                    message: "parent-directory traversal is not allowed".to_string(),
                })
            }
            _ => {
                return Err(BinpmError::UnsafeArchivePath {
                    asset: asset_name.to_string(),
                    path: path.display().to_string(),
                    message: "path component is not safe".to_string(),
                })
            }
        }
    }
    if parts.is_empty() {
        return Err(BinpmError::UnsafeArchivePath {
            asset: asset_name.to_string(),
            path: path.display().to_string(),
            message: "empty archive member paths are not allowed".to_string(),
        });
    }
    Ok(parts.join("/"))
}

fn github_sha256_digest(raw: Option<&str>) -> Option<String> {
    let digest = raw?.strip_prefix("sha256:")?;
    if digest.len() == 64
        && digest
            .chars()
            .all(|character| character.is_ascii_hexdigit())
    {
        Some(digest.to_ascii_lowercase())
    } else {
        None
    }
}

fn parse_manifest_source(raw: &str) -> Result<SourceSpec> {
    let spec = SourceSpec::from_str(raw)?;
    if spec.version.is_some() {
        return Err(BinpmError::InvalidSourceSpec {
            raw: raw.to_string(),
            message: "manifest tool sources must be versionless; use the `version` field"
                .to_string(),
        });
    }
    Ok(spec)
}

fn manifest_tool_from_source(spec: &SourceSpec) -> ManifestTool {
    ManifestTool {
        source: spec.source_without_version(),
        version: spec.version.clone(),
        bin: None,
        targets: BTreeMap::new(),
    }
}

fn update_manifest_tool_source(tool: Option<ManifestTool>, spec: &SourceSpec) -> ManifestTool {
    let mut tool = tool.unwrap_or_else(|| manifest_tool_from_source(spec));
    tool.source = spec.source_without_version();
    tool.version = spec.version.clone();
    tool
}

fn lock_targets_conflict_with_record(tool: &LockTool, record: &PackageRecord) -> bool {
    tool.source != record.source
        || tool.targets.values().any(|target_record| {
            target_record.source != record.source
                || target_record.requested_version != record.requested_version
                || target_record.release_tag != record.release_tag
        })
}

fn lock_targets_conflict_with_manifest(
    lockfile_path: &Path,
    root: &Path,
    cmd: &str,
    spec: &SourceSpec,
    manifest_tool: Option<&ManifestTool>,
    lock_tool: &LockTool,
) -> bool {
    lock_tool.targets.iter().any(|(target_key, record)| {
        let Ok(target) = HostTarget::from_str(target_key) else {
            return true;
        };
        target_key != &target.key()
            || record.requested_version != spec.version
            || assert_lock_record_matches_source_and_target(
                lockfile_path,
                cmd,
                spec,
                &target,
                record,
            )
            .is_err()
            || assert_lock_matches_manifest_tool(root, cmd, manifest_tool, &target, record).is_err()
    })
}

fn manifest_checksum_source(
    tool: Option<&ManifestTool>,
    target: &HostTarget,
    provider_digest_sha256: Option<&str>,
) -> Result<ChecksumSource> {
    if let Some(checksum_source) = manifest_target_override(tool, target)?
        .and_then(|override_target| override_target.checksum_source)
    {
        if checksum_source == ChecksumSource::GitHubDigest && provider_digest_sha256.is_some() {
            return Ok(ChecksumSource::GitHubDigest);
        }
        return Err(BinpmError::UnverifiedChecksumSourceOverride {
            checksum_source: checksum_source.as_str().to_string(),
        });
    }
    Ok(ChecksumSource::Local)
}

fn select_manifest_asset(
    spec: &SourceSpec,
    tool: Option<&ManifestTool>,
    target: &HostTarget,
    assets: &[ReleaseAsset],
) -> Result<crate::assets::CandidateDecision> {
    let target_key = target.key();
    if let Some(override_target) = manifest_target_override(tool, target)? {
        let asset = assets
            .iter()
            .find(|asset| asset.name == override_target.asset)
            .ok_or_else(|| BinpmError::AssetNotFound {
                package: spec.to_string(),
                target: target_key.clone(),
            })?;
        let kind = crate::assets::classify_artifact(&asset.name, asset.source_archive);
        if spec.provider == crate::contract::SourceProvider::GitLab && !gitlab_https_eligible(asset)
        {
            let diagnostic_url = asset
                .provider_url
                .as_deref()
                .unwrap_or(&asset.url)
                .split(['?', '#'])
                .next()
                .unwrap_or(&asset.url)
                .to_string();
            return Err(BinpmError::UnsafeUrl {
                url: diagnostic_url,
                message: "gitlab asset link is not HTTPS eligible".to_string(),
            });
        }
        return Ok(crate::assets::CandidateDecision {
            asset_name: asset.name.clone(),
            canonical_url: asset
                .provider_url
                .as_deref()
                .unwrap_or(&asset.url)
                .split(['?', '#'])
                .next()
                .unwrap_or(&asset.url)
                .to_string(),
            download_url: asset
                .provider_url
                .as_deref()
                .unwrap_or(&asset.url)
                .to_string(),
            kind,
            detected_os: Some(target.os),
            detected_arch: Some(target.arch),
            detected_libc: Some(target.libc),
            score: None,
            eligible: true,
            recognized_pattern: true,
            rejection_reason: None,
        });
    }

    let selection =
        select_asset(spec.provider, target, assets).ok_or_else(|| BinpmError::AssetNotFound {
            package: spec.to_string(),
            target: target_key.clone(),
        })?;

    Ok(selection.selected)
}

fn rollback_failed_install(
    scope_paths: &ScopePaths,
    cmd: &str,
    record: &PackageRecord,
) -> Result<()> {
    remove_installed_binary(scope_paths, cmd, record)?;
    Ok(())
}

fn remove_unreferenced_cache_entry(
    cache_paths: &CachePaths,
    sha256: &str,
    local_root: Option<&Path>,
) -> Result<()> {
    let cache_key = crate::storage::cache_key(sha256);
    let home = cache_paths
        .root
        .parent()
        .map(Path::to_path_buf)
        .ok_or(BinpmError::MissingGlobalHome)?;
    let global_paths = ScopePaths::global(home);
    let local_paths = local_root.map(|root| ScopePaths::local(root.to_path_buf()));
    let referenced = referenced_cache_keys(&global_paths, local_paths.as_ref(), cache_paths)?;
    if !referenced.contains(&cache_key) {
        remove_path_if_exists(&cache_paths.entry_dir(sha256))?;
    }
    Ok(())
}

fn verification_state(record: &PackageRecord) -> &'static str {
    if record.has_verified_source() {
        "verified"
    } else {
        "unverified"
    }
}

#[derive(Debug, Clone)]
struct LocalToolState {
    lockfile: crate::storage::Lockfile,
    lockfile_existed: bool,
    runtime: RuntimeToolState,
}

#[derive(Debug, Clone)]
struct LocalRemoveState {
    manifest: Manifest,
    lockfile: crate::storage::Lockfile,
    lockfile_existed: bool,
    runtime: RuntimeToolState,
}

fn capture_local_tool_state(root: &Path, cmd: &str) -> Result<LocalToolState> {
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let lockfile_path = root.join(LOCKFILE_FILE);
    Ok(LocalToolState {
        lockfile_existed: lockfile_path.exists(),
        lockfile: read_lockfile(&lockfile_path)?,
        runtime: capture_runtime_tool_state(&scope_paths, cmd)?,
    })
}

fn capture_local_remove_state(root: &Path, cmd: &str) -> Result<LocalRemoveState> {
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let lockfile_path = root.join(LOCKFILE_FILE);
    Ok(LocalRemoveState {
        manifest: read_manifest(&root.join(MANIFEST_FILE))?,
        lockfile_existed: lockfile_path.exists(),
        lockfile: read_lockfile(&lockfile_path)?,
        runtime: capture_runtime_tool_state(&scope_paths, cmd)?,
    })
}

#[derive(Debug, Clone)]
struct RuntimeToolState {
    package_record: Option<PackageRecord>,
    installed_path: Option<PathBuf>,
    installed_snapshot: Option<InstalledPathSnapshot>,
}

#[derive(Debug, Clone)]
enum InstalledPathSnapshot {
    RegularFile {
        bytes: Vec<u8>,
        #[cfg(unix)]
        mode: u32,
    },
    Symlink(PathBuf),
}

fn capture_runtime_tool_state(paths: &ScopePaths, cmd: &str) -> Result<RuntimeToolState> {
    let package_record = match read_package_record(&package_record_path(paths, cmd)) {
        Ok(record) => Some(record),
        Err(BinpmError::ReadFile { source, .. })
            if source.kind() == std::io::ErrorKind::NotFound =>
        {
            None
        }
        Err(error) => return Err(error),
    };
    let installed_path = match &package_record {
        Some(record) => match validate_installed_binary_path(paths, cmd, record) {
            Ok(path) => Some(path),
            Err(BinpmError::UnsafeInstalledPath { .. }) => None,
            Err(error) => return Err(error),
        },
        None => Some(current_platform_installed_path(paths, cmd)),
    };
    let installed_snapshot = installed_path
        .as_ref()
        .map(|path| match fs::symlink_metadata(path) {
            Ok(metadata) if metadata.file_type().is_symlink() => fs::read_link(path)
                .map(|target| Some(InstalledPathSnapshot::Symlink(target)))
                .map_err(|source| BinpmError::ReadFile {
                    path: path.clone(),
                    source,
                }),
            Ok(metadata) => fs::read(path)
                .map(|bytes| {
                    Some(InstalledPathSnapshot::RegularFile {
                        bytes,
                        #[cfg(unix)]
                        mode: {
                            use std::os::unix::fs::PermissionsExt;

                            metadata.permissions().mode()
                        },
                    })
                })
                .map_err(|source| BinpmError::ReadFile {
                    path: path.clone(),
                    source,
                }),
            Err(source) if source.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(source) => Err(BinpmError::ReadFile {
                path: path.clone(),
                source,
            }),
        })
        .transpose()?
        .flatten();
    Ok(RuntimeToolState {
        package_record,
        installed_path,
        installed_snapshot,
    })
}

fn remove_local_manifest_orphans(
    root: &Path,
    manifest_tools: &BTreeMap<String, ManifestTool>,
    frozen_lockfile: bool,
) -> Result<()> {
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let mut orphan_cmds = BTreeSet::new();
    for (cmd, _) in list_package_records(&scope_paths)? {
        if !manifest_tools.contains_key(&cmd) {
            validate_command_name(&cmd)?;
            orphan_cmds.insert(cmd);
        }
    }

    let lockfile_path = root.join(LOCKFILE_FILE);
    let mut lockfile = read_lockfile(&lockfile_path)?;
    for cmd in lockfile.tools.keys() {
        if !manifest_tools.contains_key(cmd) {
            validate_command_name(cmd)?;
            orphan_cmds.insert(cmd.clone());
        }
    }

    if orphan_cmds.is_empty() {
        return Ok(());
    }
    if frozen_lockfile {
        return Err(BinpmError::FrozenLockfile {
            path: lockfile_path,
        });
    }

    let cache_paths = CachePaths::new(&binpm_home()?);
    let prior_states = orphan_cmds
        .iter()
        .map(|cmd| Ok((cmd.clone(), capture_runtime_tool_state(&scope_paths, cmd)?)))
        .collect::<Result<Vec<_>>>()?;
    for cmd in &orphan_cmds {
        if let Err(error) =
            remove_local_orphan_runtime(root, &scope_paths, &cache_paths, cmd, manifest_tools)
        {
            for (restored_cmd, prior_state) in prior_states {
                restore_local_runtime_and_cache_ref(
                    root,
                    &scope_paths,
                    &cache_paths,
                    &restored_cmd,
                    prior_state,
                );
            }
            return Err(error);
        }
        lockfile.tools.remove(cmd);
    }
    if let Err(error) = write_lockfile(&lockfile_path, &lockfile) {
        for (cmd, prior_state) in prior_states {
            restore_local_runtime_and_cache_ref(
                root,
                &scope_paths,
                &cache_paths,
                &cmd,
                prior_state,
            );
        }
        return Err(error);
    }
    Ok(())
}

fn remove_local_orphan_runtime(
    root: &Path,
    paths: &ScopePaths,
    cache_paths: &CachePaths,
    cmd: &str,
    manifest_tools: &BTreeMap<String, ManifestTool>,
) -> Result<()> {
    validate_command_name(cmd)?;
    let prior_state = capture_runtime_tool_state(paths, cmd)?;
    let record_path = package_record_path(paths, cmd);
    let cleanup_result = (|| {
        let stale_install = if record_path.exists() {
            let record = read_package_record(&record_path)?;
            let installed_path = managed_installed_path(paths, cmd, record.target_os);
            if !is_manifest_managed_installed_path(
                paths,
                manifest_tools,
                &installed_path,
                record.target_os,
            ) {
                remove_installed_binary(paths, cmd, &record)?;
            }
            Some((installed_path, record.target_os))
        } else {
            None
        };
        remove_package_record(paths, cmd)?;
        remove_cache_ref(cache_paths, root, cmd)?;
        if let Some((stale_installed_path, stale_target_os)) = stale_install {
            if !is_manifest_managed_installed_path(
                paths,
                manifest_tools,
                &stale_installed_path,
                stale_target_os,
            ) {
                remove_path_if_exists(&stale_installed_path)?;
            }
        }
        Ok(())
    })();
    if let Err(error) = cleanup_result {
        if matches!(error, BinpmError::UnsafeInstalledPath { .. }) {
            return Err(error);
        }
        restore_local_runtime_and_cache_ref(root, paths, cache_paths, cmd, prior_state);
        return Err(error);
    }
    println!("removed {cmd}");
    Ok(())
}

fn restore_local_runtime_and_cache_ref(
    root: &Path,
    paths: &ScopePaths,
    cache_paths: &CachePaths,
    cmd: &str,
    prior_state: RuntimeToolState,
) {
    let package_record = prior_state.package_record.clone();
    restore_runtime_tool_state(paths, cmd, prior_state);
    match package_record {
        Some(previous) => {
            let _ = write_cache_ref(cache_paths, root, cmd, &previous);
        }
        None => {
            let _ = remove_cache_ref(cache_paths, root, cmd);
        }
    }
}

fn is_manifest_managed_installed_path(
    paths: &ScopePaths,
    manifest_tools: &BTreeMap<String, ManifestTool>,
    path: &Path,
    target_os: TargetOs,
) -> bool {
    let key = install_path_collision_key(path, target_os);
    manifest_tools.keys().any(|cmd| {
        install_path_collision_key(&managed_installed_path(paths, cmd, target_os), target_os) == key
    })
}

fn ensure_no_selected_install_path_collisions(
    manifest: &Manifest,
    selected: &[String],
) -> Result<()> {
    let target = HostTarget::current()?;
    let mut owners = BTreeMap::new();
    for cmd in manifest.tools.keys() {
        let path = PathBuf::from(deterministic_installed_path(cmd, target.os));
        let key = install_path_collision_key(&path, target.os);
        if let Some((existing, existing_path)) = owners.insert(key, (cmd.clone(), path.clone())) {
            if selected.is_empty() || selected.contains(cmd) || selected.contains(&existing) {
                return Err(BinpmError::InstalledPathCollision {
                    cmd: existing,
                    other_cmd: cmd.clone(),
                    path: existing_path,
                });
            }
        }
    }
    Ok(())
}

fn ensure_no_package_record_install_path_collision(
    paths: &ScopePaths,
    cmd: &str,
    target_os: TargetOs,
) -> Result<()> {
    let path = managed_installed_path(paths, cmd, target_os);
    let key = install_path_collision_key(&path, target_os);
    for (other_cmd, record) in list_package_records(paths)? {
        if other_cmd == cmd {
            continue;
        }
        let other_path = managed_installed_path(paths, &other_cmd, record.target_os);
        if install_path_collision_key(&other_path, target_os) == key {
            return Err(BinpmError::InstalledPathCollision {
                cmd: other_cmd,
                other_cmd: cmd.to_string(),
                path,
            });
        }
    }
    Ok(())
}

fn install_path_collision_key(path: &Path, target_os: TargetOs) -> String {
    let key = path.to_string_lossy().into_owned();
    if matches!(target_os, TargetOs::Darwin | TargetOs::Windows) {
        key.to_ascii_lowercase()
    } else {
        key
    }
}

fn capture_local_manifest_orphan_states(
    root: &Path,
    manifest_tools: &BTreeMap<String, ManifestTool>,
) -> Result<Vec<(String, RuntimeToolState)>> {
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let mut orphan_cmds = BTreeSet::new();
    for (cmd, _) in list_package_records(&scope_paths)? {
        if !manifest_tools.contains_key(&cmd) {
            validate_command_name(&cmd)?;
            orphan_cmds.insert(cmd);
        }
    }
    for cmd in read_lockfile(&root.join(LOCKFILE_FILE))?.tools.keys() {
        if !manifest_tools.contains_key(cmd) {
            validate_command_name(cmd)?;
            orphan_cmds.insert(cmd.clone());
        }
    }
    orphan_cmds
        .into_iter()
        .map(|cmd| Ok((cmd.clone(), capture_runtime_tool_state(&scope_paths, &cmd)?)))
        .collect()
}

fn rollback_local_install_state(
    root: &Path,
    cmd: &str,
    record: &PackageRecord,
    prior_state: LocalToolState,
) {
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let cache_paths = binpm_home().ok().map(|home| CachePaths::new(&home));
    let _ = remove_installed_binary(&scope_paths, cmd, record);
    restore_runtime_tool_state(&scope_paths, cmd, prior_state.runtime.clone());
    match &prior_state.runtime.package_record {
        Some(previous) => {
            if let Some(cache_paths) = &cache_paths {
                let _ = write_cache_ref(cache_paths, root, cmd, previous);
            }
        }
        None => {
            if let Some(cache_paths) = &cache_paths {
                let _ = remove_cache_ref(cache_paths, root, cmd);
            }
        }
    }
    let lockfile_path = root.join(LOCKFILE_FILE);
    if prior_state.lockfile_existed {
        let _ = write_lockfile(&lockfile_path, &prior_state.lockfile);
    } else {
        let _ = remove_path_if_exists(&lockfile_path);
    }
}

fn manifest_target_override<'tool>(
    tool: Option<&'tool ManifestTool>,
    target: &HostTarget,
) -> Result<Option<&'tool crate::storage::ManifestTargetOverride>> {
    let Some(tool) = tool else {
        return Ok(None);
    };
    let target_key = target.key();
    let mut selected = None;
    for (raw_key, override_target) in &tool.targets {
        let canonical_key = HostTarget::from_str(raw_key)?.key();
        if raw_key != &canonical_key {
            return Err(BinpmError::InvalidTargetKey {
                raw: raw_key.clone(),
            });
        }
        if canonical_key == target_key {
            selected = Some(override_target);
        }
    }
    Ok(selected)
}

fn restore_local_remove_state(root: &Path, cmd: &str, prior_state: LocalRemoveState) {
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let cache_paths = binpm_home().ok().map(|home| CachePaths::new(&home));
    restore_runtime_tool_state(&scope_paths, cmd, prior_state.runtime.clone());
    match &prior_state.runtime.package_record {
        Some(previous) => {
            if let Some(cache_paths) = &cache_paths {
                let _ = write_cache_ref(cache_paths, root, cmd, previous);
            }
        }
        None => {
            if let Some(cache_paths) = &cache_paths {
                let _ = remove_cache_ref(cache_paths, root, cmd);
            }
        }
    }
    let _ = write_manifest(&root.join(MANIFEST_FILE), &prior_state.manifest);
    let lockfile_path = root.join(LOCKFILE_FILE);
    if prior_state.lockfile_existed {
        let _ = write_lockfile(&lockfile_path, &prior_state.lockfile);
    } else {
        let _ = remove_path_if_exists(&lockfile_path);
    }
}

fn restore_runtime_tool_state(paths: &ScopePaths, cmd: &str, prior_state: RuntimeToolState) {
    match &prior_state.package_record {
        Some(previous) => {
            let previous_path = PathBuf::from(&previous.installed_path);
            let expected_path = managed_installed_path(paths, cmd, previous.target_os);
            if previous_path != expected_path {
                let _ = write_package_record(paths, cmd, previous);
                return;
            }
            let _ = write_package_record(paths, cmd, previous);
            if let Some(snapshot) = prior_state.installed_snapshot {
                let _ = restore_executable_snapshot(&previous_path, snapshot);
            }
        }
        None => {
            let _ = remove_package_record(paths, cmd);
            if let (Some(path), Some(snapshot)) =
                (prior_state.installed_path, prior_state.installed_snapshot)
            {
                let _ = restore_executable_snapshot(&path, snapshot);
            }
        }
    }
}

fn restore_executable_snapshot(path: &Path, snapshot: InstalledPathSnapshot) -> Result<()> {
    match snapshot {
        InstalledPathSnapshot::RegularFile {
            bytes,
            #[cfg(unix)]
            mode,
        } => restore_executable_bytes(
            path,
            &bytes,
            #[cfg(unix)]
            mode,
        ),
        InstalledPathSnapshot::Symlink(target) => restore_executable_symlink(path, &target),
    }
}

fn restore_executable_bytes(path: &Path, bytes: &[u8], #[cfg(unix)] mode: u32) -> Result<()> {
    crate::storage::atomic_write_bytes(path, bytes)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;

        let mut permissions = fs::metadata(path)
            .map_err(|source| BinpmError::ReadFile {
                path: path.to_path_buf(),
                source,
            })?
            .permissions();
        permissions.set_mode(mode);
        fs::set_permissions(path, permissions).map_err(|source| BinpmError::WriteFile {
            path: path.to_path_buf(),
            source,
        })?;
    }
    Ok(())
}

#[cfg(unix)]
fn restore_executable_symlink(path: &Path, target: &Path) -> Result<()> {
    use std::os::unix::fs::symlink;

    remove_path_if_exists(path)?;
    symlink(target, path).map_err(|source| BinpmError::WriteFile {
        path: path.to_path_buf(),
        source,
    })
}

#[cfg(windows)]
fn restore_executable_symlink(path: &Path, target: &Path) -> Result<()> {
    use std::os::windows::fs::symlink_file;

    remove_path_if_exists(path)?;
    symlink_file(target, path).map_err(|source| BinpmError::WriteFile {
        path: path.to_path_buf(),
        source,
    })
}

fn download_asset(url: &str) -> Result<Vec<u8>> {
    validate_download_url(url)?;
    let sanitized_url = sanitize_download_diagnostic_url(url);
    let asset_name = download_asset_name(&sanitized_url);
    info!(
        asset_url = %sanitized_url,
        asset_name = %asset_name,
        retry_attempt = 0usize,
        "Starting release asset download"
    );
    let client = reqwest::blocking::Client::builder()
        .user_agent(concat!("binpm/", env!("CARGO_PKG_VERSION")))
        .redirect(reqwest::redirect::Policy::custom(|attempt| {
            if let Err(error) = validate_download_url(attempt.url().as_str()) {
                attempt.error(error)
            } else if attempt.previous().len() >= 10 {
                attempt.error("too many redirects while downloading release asset")
            } else {
                attempt.follow()
            }
        }))
        .build()
        .map_err(BinpmError::ReleaseHttpClient)?;

    let mut last_error = None;
    for attempt in 1..=DOWNLOAD_RETRY_ATTEMPTS {
        match download_asset_attempt(&client, url, attempt, &sanitized_url, &asset_name) {
            Ok(bytes) => return Ok(bytes),
            Err(error)
                if attempt < DOWNLOAD_RETRY_ATTEMPTS && is_retryable_download_error(&error) =>
            {
                let delay = download_retry_delay(attempt);
                warn!(
                    asset_url = %sanitized_url,
                    asset_name = %asset_name,
                    retry_attempt = attempt,
                    retry_delay_ms = delay.as_millis(),
                    error = %error,
                    "Retrying release asset download"
                );
                eprintln!(
                    "binpm: retrying download of {asset_name} after a transient failure (attempt \
                     {}/{})",
                    attempt + 1,
                    DOWNLOAD_RETRY_ATTEMPTS
                );
                thread::sleep(delay);
                last_error = Some(error);
            }
            Err(error) => return Err(error),
        }
    }

    Err(last_error.expect("download retry loop always returns before exhaustion"))
}

fn download_asset_attempt(
    client: &reqwest::blocking::Client,
    url: &str,
    attempt: usize,
    sanitized_url: &str,
    asset_name: &str,
) -> Result<Vec<u8>> {
    let mut response = client
        .get(url)
        .send()
        .map_err(|error| BinpmError::ReleaseLookup(error.without_url()))?;
    validate_download_url(response.url().as_str())?;
    let final_url = sanitize_download_diagnostic_url(response.url().as_str());
    let status = response.status();
    if !status.is_success() {
        let error = response
            .error_for_status()
            .expect_err("non-success status must produce an error")
            .without_url();
        if is_retryable_status(status) {
            return Err(BinpmError::ReleaseLookup(error));
        }
        return Err(BinpmError::ReleaseLookup(error));
    }

    let total_bytes = response.content_length();
    let show_progress = download_progress_enabled(total_bytes);
    if show_progress {
        eprintln!(
            "binpm: downloading {asset_name}{}",
            total_bytes
                .map(|bytes| format!(" ({})", human_bytes(bytes)))
                .unwrap_or_default()
        );
    }

    let mut bytes =
        Vec::with_capacity(total_bytes.unwrap_or_default().min(usize::MAX as u64) as usize);
    let mut buffer = [0u8; 64 * 1024];
    let mut downloaded = 0u64;
    let mut next_progress_at = DOWNLOAD_PROGRESS_STEP_BYTES;
    let mut last_progress_at = Instant::now();

    loop {
        let read = response
            .read(&mut buffer)
            .map_err(|source| BinpmError::DownloadStream {
                url: final_url.clone(),
                source,
            })?;
        if read == 0 {
            break;
        }
        bytes.extend_from_slice(&buffer[..read]);
        downloaded += read as u64;

        if show_progress
            && (downloaded >= next_progress_at
                || last_progress_at.elapsed() >= DOWNLOAD_PROGRESS_INTERVAL)
        {
            eprintln!(
                "binpm: downloading {asset_name} {}",
                format_download_progress(downloaded, total_bytes)
            );
            let _ = std::io::stderr().flush();
            next_progress_at =
                ((downloaded / DOWNLOAD_PROGRESS_STEP_BYTES) + 1) * DOWNLOAD_PROGRESS_STEP_BYTES;
            last_progress_at = Instant::now();
        }
    }

    if show_progress {
        eprintln!(
            "binpm: downloaded {asset_name} {}",
            format_download_progress(downloaded, total_bytes)
        );
    }
    info!(
        asset_url = %sanitized_url,
        final_url = %final_url,
        asset_name = %asset_name,
        cache_bytes = downloaded,
        retry_attempt = attempt.saturating_sub(1),
        outcome = "success",
        "Downloaded release asset"
    );
    Ok(bytes)
}

fn is_retryable_download_error(error: &BinpmError) -> bool {
    match error {
        BinpmError::ReleaseLookup(source) => source
            .status()
            .map(is_retryable_status)
            .unwrap_or_else(|| source.is_connect() || source.is_timeout() || source.is_body()),
        BinpmError::DownloadStream { .. } => true,
        _ => false,
    }
}

fn is_retryable_status(status: reqwest::StatusCode) -> bool {
    status == reqwest::StatusCode::TOO_MANY_REQUESTS || status.is_server_error()
}

fn download_retry_delay(attempt: usize) -> Duration {
    DOWNLOAD_RETRY_BASE_DELAY * attempt as u32
}

fn download_progress_enabled(total_bytes: Option<u64>) -> bool {
    let ci = env::var("CI")
        .map(|value| {
            let value = value.trim().to_ascii_lowercase();
            !(value.is_empty() || value == "0" || value == "false")
        })
        .unwrap_or(false);
    !ci && std::io::stderr().is_terminal()
        && total_bytes
            .map(|bytes| bytes >= DOWNLOAD_PROGRESS_THRESHOLD_BYTES)
            .unwrap_or(true)
}

fn format_download_progress(downloaded: u64, total: Option<u64>) -> String {
    match total {
        Some(total) => format!("{}/{}", human_bytes(downloaded), human_bytes(total)),
        None => human_bytes(downloaded),
    }
}

fn human_bytes(bytes: u64) -> String {
    const UNITS: &[&str] = &["B", "KiB", "MiB", "GiB"];
    let mut value = bytes as f64;
    let mut unit = 0usize;
    while value >= 1024.0 && unit + 1 < UNITS.len() {
        value /= 1024.0;
        unit += 1;
    }
    if unit == 0 {
        format!("{bytes} {}", UNITS[unit])
    } else {
        format!("{value:.1} {}", UNITS[unit])
    }
}

fn download_asset_name(sanitized_url: &str) -> String {
    reqwest::Url::parse(sanitized_url)
        .ok()
        .and_then(|url| {
            url.path_segments()
                .and_then(|mut segments| segments.next_back().map(str::to_string))
        })
        .filter(|name| !name.is_empty())
        .unwrap_or_else(|| "release asset".to_string())
}

fn sanitize_download_diagnostic_url(url: &str) -> String {
    let without_query = url.split(['?', '#']).next().unwrap_or(url);
    let Ok(mut parsed) = reqwest::Url::parse(without_query) else {
        return without_query.to_string();
    };
    if !parsed.username().is_empty() || parsed.password().is_some() {
        let _ = parsed.set_username("");
        let _ = parsed.set_password(None);
    }
    parsed.to_string()
}

fn execute_command(
    cmd: &str,
    args: &[std::ffi::OsString],
    path_entries: &[PathBuf],
) -> Result<i32> {
    let path = prepend_path_entries(path_entries)?;
    let executable = resolve_managed_executable(cmd, path_entries);
    info!(
        command = "x",
        resolved_command = cmd,
        path_entry_count = path_entries.len(),
        forwarded_arg_count = args.len(),
        "Executing binpm-managed command"
    );
    let status = ProcessCommand::new(&executable)
        .args(args)
        .env("PATH", path)
        .status()
        .map_err(|source| BinpmError::Execute {
            cmd: cmd.to_string(),
            source,
        })?;
    Ok(status.code().unwrap_or(1))
}

fn resolve_managed_executable(cmd: &str, path_entries: &[PathBuf]) -> PathBuf {
    let filename = current_platform_installed_filename(cmd);
    path_entries
        .iter()
        .map(|entry| entry.join(&filename))
        .find(|candidate| {
            candidate
                .symlink_metadata()
                .map(|metadata| metadata.is_file())
                .unwrap_or(false)
        })
        .unwrap_or_else(|| {
            path_entries
                .first()
                .map(|entry| entry.join(filename))
                .unwrap_or_else(|| PathBuf::from(cmd))
        })
}

fn current_platform_installed_filename(cmd: &str) -> String {
    #[cfg(windows)]
    {
        installed_filename(cmd, TargetOs::Windows)
    }
    #[cfg(not(windows))]
    {
        installed_filename(cmd, TargetOs::Linux)
    }
}

fn prepend_path_entries(entries: &[PathBuf]) -> Result<std::ffi::OsString> {
    let existing = env::var_os("PATH").unwrap_or_default();
    let mut paths = entries.to_vec();
    paths.extend(env::split_paths(&existing));
    env::join_paths(paths).map_err(|error| BinpmError::UnsafeUrl {
        url: "<PATH>".to_string(),
        message: error.to_string(),
    })
}

fn remove_local_tool(cmd: &str) -> Result<i32> {
    let root = require_manifest_root()?;
    validate_command_name(cmd)?;
    let manifest_path = root.join(MANIFEST_FILE);
    let lockfile_path = root.join(LOCKFILE_FILE);
    let paths = ScopePaths::local(root.clone());
    let prior_state = capture_local_remove_state(&root, cmd)?;
    let manifest = read_manifest(&manifest_path)?;
    if !manifest.tools.contains_key(cmd) && !has_local_runtime_or_lock_state(cmd, &prior_state) {
        return Err(BinpmError::MissingTool {
            cmd: cmd.to_string(),
            manifest: manifest_path,
        });
    }
    let record_path = package_record_path(&paths, cmd);
    let cleanup_result = (|| {
        let mut remaining_manifest = manifest.clone();
        remaining_manifest.tools.remove(cmd);
        let stale_installed = if record_path.exists() {
            let record = read_package_record(&record_path)?;
            let installed_path = managed_installed_path(&paths, cmd, record.target_os);
            if !is_manifest_managed_installed_path(
                &paths,
                &remaining_manifest.tools,
                &installed_path,
                record.target_os,
            ) {
                remove_installed_binary(&paths, cmd, &record)?;
            }
            Some((installed_path, record.target_os))
        } else {
            None
        };
        remove_package_record(&paths, cmd)?;
        remove_cache_ref(&CachePaths::new(&binpm_home()?), &root, cmd)?;
        if let Some((stale_installed_path, stale_target_os)) = stale_installed {
            if !is_manifest_managed_installed_path(
                &paths,
                &remaining_manifest.tools,
                &stale_installed_path,
                stale_target_os,
            ) {
                remove_path_if_exists(&stale_installed_path)?;
            }
        }
        Ok(())
    })();
    if let Err(error) = cleanup_result {
        if matches!(error, BinpmError::UnsafeInstalledPath { .. }) {
            return Err(error);
        }
        restore_local_remove_state(&root, cmd, prior_state);
        return Err(error);
    }

    let mut manifest = manifest;
    manifest.tools.remove(cmd);
    if let Err(error) = write_manifest(&manifest_path, &manifest) {
        restore_local_remove_state(&root, cmd, prior_state);
        return Err(error);
    }

    let mut lockfile = match read_lockfile(&lockfile_path) {
        Ok(lockfile) => lockfile,
        Err(error) => {
            restore_local_remove_state(&root, cmd, prior_state);
            return Err(error);
        }
    };
    lockfile.tools.remove(cmd);
    if let Err(error) = write_lockfile(&lockfile_path, &lockfile) {
        restore_local_remove_state(&root, cmd, prior_state);
        return Err(error);
    }
    println!("removed {cmd}");
    Ok(0)
}

fn has_local_runtime_or_lock_state(cmd: &str, state: &LocalRemoveState) -> bool {
    state.lockfile.tools.contains_key(cmd) || state.runtime.package_record.is_some()
}

fn remove_global_tool(cmd: &str) -> Result<i32> {
    validate_command_name(cmd)?;
    let paths = ScopePaths::global(binpm_home()?);
    remove_global_tool_from_paths(&paths, cmd)?;
    println!("removed {cmd}");
    Ok(0)
}

fn remove_global_tool_from_paths(paths: &ScopePaths, cmd: &str) -> Result<()> {
    let prior_state = capture_runtime_tool_state(paths, cmd)?;
    let record_path = package_record_path(paths, cmd);
    let record = read_package_record(&record_path)?;
    let stale_installed_path = managed_installed_path(paths, cmd, record.target_os);
    if !is_global_managed_installed_path(paths, cmd, &stale_installed_path)? {
        remove_installed_binary(paths, cmd, &record)?;
    }
    if let Err(error) = remove_package_record(paths, cmd).and_then(|_| {
        if is_global_managed_installed_path(paths, cmd, &stale_installed_path)? {
            Ok(())
        } else {
            remove_path_if_exists(&stale_installed_path)
        }
    }) {
        restore_runtime_tool_state(paths, cmd, prior_state);
        return Err(error);
    }
    Ok(())
}

fn is_global_managed_installed_path(
    paths: &ScopePaths,
    removed_cmd: &str,
    path: &Path,
) -> Result<bool> {
    for (cmd, record) in list_package_records(paths)? {
        if cmd == removed_cmd {
            continue;
        }
        let key = install_path_collision_key(path, record.target_os);
        let managed_path = managed_installed_path(paths, &cmd, record.target_os);
        if install_path_collision_key(&managed_path, record.target_os) == key {
            return Ok(true);
        }
    }
    Ok(false)
}

fn current_platform_installed_path(paths: &ScopePaths, cmd: &str) -> PathBuf {
    #[cfg(windows)]
    let target_os = crate::contract::TargetOs::Windows;
    #[cfg(not(windows))]
    let target_os = crate::contract::TargetOs::Linux;

    paths.bin.join(installed_filename(cmd, target_os))
}

fn select_scope(scope: Scope) -> Result<Scope> {
    match scope {
        Scope::Local | Scope::Global => Ok(scope),
        Scope::Auto => {
            if find_manifest_root(&current_dir()?).is_some() {
                Ok(Scope::Local)
            } else {
                Ok(Scope::Global)
            }
        }
    }
}

fn require_manifest_root() -> Result<PathBuf> {
    let cwd = current_dir()?;
    find_manifest_root(&cwd)
        .map(Path::to_path_buf)
        .ok_or(BinpmError::MissingManifest { start: cwd })
}

fn require_manifest_root_or_creation_root() -> Result<PathBuf> {
    let cwd = current_dir()?;
    Ok(manifest_root_or_creation_root_from(&cwd))
}

fn repo_name(spec: &SourceSpec) -> &str {
    spec.path.rsplit('/').next().unwrap_or(&spec.path)
}

fn verify(args: VerifyArgs) -> Result<i32> {
    info!(
        command = "verify",
        read_only = true,
        selected_scope = args.scope.scope().as_str(),
        require_verified = args.require_verified,
        "Prepared verification request"
    );
    let scope = select_scope(args.scope.scope())?;
    let home = binpm_home()?;
    let root = match scope {
        Scope::Local => Some(require_manifest_root()?),
        Scope::Global => None,
        Scope::Auto => unreachable!("select_scope never returns auto"),
    };
    let paths = match scope {
        Scope::Local => ScopePaths::local(root.clone().expect("local root is set")),
        Scope::Global => ScopePaths::global(home.clone()),
        Scope::Auto => unreachable!("select_scope never returns auto"),
    };
    let cache_paths = CachePaths::new(&home);
    let mut checked = 0usize;
    let mut locked = BTreeSet::new();
    let mut local_runtime_locks = BTreeMap::new();
    if let Some(root) = &root {
        let manifest = read_manifest(&root.join(MANIFEST_FILE))?;
        let lockfile = read_lockfile(&root.join(LOCKFILE_FILE))?;
        local_runtime_locks =
            local_runtime_lock_records(&manifest, &lockfile, &HostTarget::current()?)?;
        let (lock_checked, lock_commands) = verify_lockfile_records(
            &root.join(LOCKFILE_FILE),
            lockfile,
            Some((&manifest, root.as_path())),
            args.require_verified,
        )?;
        checked += lock_checked;
        locked = lock_commands;
    }
    for (cmd, record) in list_package_records(&paths)? {
        validate_command_name(&cmd)?;
        validate_package_record_source_identity(&cmd, &record)?;
        if let Some(lock_record) = local_runtime_locks.remove(&cmd) {
            assert_runtime_record_matches_lock(
                root.as_deref().expect("local root"),
                &cmd,
                &lock_record,
                &record,
            )?;
        } else if let Some(root) = &root {
            return Err(BinpmError::StaleLockfile {
                path: root.join(LOCKFILE_FILE),
                cmd,
            });
        }
        if args.require_verified && !record.has_verified_source() {
            return Err(BinpmError::VerificationRequired {
                package: record.package_spec,
            });
        }
        validate_provider_digest_evidence(&record)?;
        validate_package_record_current_provider_digest(&record)?;
        validate_package_record_metadata(&cache_paths, &record)?;
        verify_runtime_cache_bytes(&cache_paths, &record)?;
        let installed_path = validate_installed_binary_path(&paths, &cmd, &record)?;
        require_regular_managed_file(&installed_path)?;
        require_executable_managed_file(&installed_path)?;
        verify_installed_binary_contents(&cache_paths, &record, &installed_path)?;
        println!("{cmd} verified {}", record.checksum_source.as_str());
        if !locked.contains(&cmd) {
            checked += 1;
        }
    }
    if let Some(root) = &root {
        assert_local_runtime_records_complete(root, &local_runtime_locks)?;
    }
    println!("checked {checked}");
    Ok(0)
}

fn verify_installed_binary_contents(
    cache_paths: &CachePaths,
    record: &PackageRecord,
    installed_path: &Path,
) -> Result<()> {
    if record.archive_format == ArchiveFormat::BareExecutable {
        return crate::storage::verify_sha256(installed_path, &record.sha256);
    }

    let spec = SourceSpec::from_str(
        &record
            .requested_version
            .as_ref()
            .map(|version| format!("{}@{version}", record.source))
            .unwrap_or_else(|| record.source.clone()),
    )?;
    let selected = read_archive_selected_binary(
        &cache_paths.asset_path(&record.sha256),
        record.archive_format,
        &record.asset_name,
        repo_name(&spec),
        &HostTarget {
            os: record.target_os,
            arch: record.target_arch,
            libc: record.target_libc,
        },
        Some(&record.selected_binary),
    )?;
    let installed_bytes = fs::read(installed_path).map_err(|source| BinpmError::ReadFile {
        path: installed_path.to_path_buf(),
        source,
    })?;
    if installed_bytes != selected.bytes {
        return Err(BinpmError::DigestMismatch {
            path: installed_path.to_path_buf(),
            expected: format!("{:x}", Sha256::digest(&selected.bytes)),
            actual: format!("{:x}", Sha256::digest(&installed_bytes)),
        });
    }
    Ok(())
}

fn validate_package_record_source_identity(cmd: &str, record: &PackageRecord) -> Result<()> {
    let spec = SourceSpec::from_str(
        &record
            .requested_version
            .as_ref()
            .map(|version| format!("{}@{version}", record.source))
            .unwrap_or_else(|| record.source.clone()),
    )?;
    if record.source != spec.source_without_version()
        || record.source_provider != spec.provider
        || record.source_host != spec.host
        || record.source_path != spec.path
        || record.package_spec != expected_package_spec(&spec, record)
    {
        return Err(BinpmError::StalePackageRecord {
            cmd: cmd.to_string(),
        });
    }
    Ok(())
}

fn validate_package_record_metadata(
    cache_paths: &CachePaths,
    record: &PackageRecord,
) -> Result<()> {
    sanitize_persisted_url(&record.asset_url)?;
    validate_sha256_digest(&record.sha256)?;
    let expected_cache_key = crate::storage::cache_key(&record.sha256);
    let Some(cache_key) = &record.cache_key else {
        return Err(BinpmError::UnsafeCachePath {
            path: PathBuf::from("<missing cache key>"),
            expected: PathBuf::from(expected_cache_key),
        });
    };
    if cache_key != &expected_cache_key {
        return Err(BinpmError::UnsafeCachePath {
            path: PathBuf::from(cache_key),
            expected: PathBuf::from(expected_cache_key),
        });
    }
    let expected_cache_path = cache_paths.asset_path(&record.sha256);
    let Some(cache_path) = &record.cache_path else {
        return Err(BinpmError::UnsafeCachePath {
            path: PathBuf::from("<missing cache path>"),
            expected: expected_cache_path,
        });
    };
    let cache_path = Path::new(cache_path);
    if cache_path != expected_cache_path {
        return Err(BinpmError::UnsafeCachePath {
            path: cache_path.to_path_buf(),
            expected: expected_cache_path,
        });
    }
    Ok(())
}

fn verify_runtime_cache_bytes(cache_paths: &CachePaths, record: &PackageRecord) -> Result<()> {
    reject_symlinked_cache_entry(cache_paths, &record.sha256)?;
    require_verified_regular_cache_asset(&cache_paths.asset_path(&record.sha256), &record.sha256)
}

#[cfg(unix)]
fn require_executable_managed_file(path: &Path) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;

    let metadata = fs::symlink_metadata(path).map_err(|source| BinpmError::ReadFile {
        path: path.to_path_buf(),
        source,
    })?;
    if metadata.permissions().mode() & 0o111 == 0 {
        return Err(BinpmError::UnsafeManagedFile {
            path: path.to_path_buf(),
        });
    }
    Ok(())
}

#[cfg(not(unix))]
fn require_executable_managed_file(_path: &Path) -> Result<()> {
    Ok(())
}

fn validate_provider_digest_evidence(record: &PackageRecord) -> Result<()> {
    if record.checksum_source == ChecksumSource::GitHubDigest
        && (record.source_provider != crate::contract::SourceProvider::GitHub
            || record.provider_digest_sha256.as_deref() != Some(record.sha256.as_str()))
    {
        return Err(BinpmError::ProviderDigestMismatch {
            package: record.package_spec.clone(),
        });
    }
    Ok(())
}

fn validate_package_record_current_provider_digest(record: &PackageRecord) -> Result<()> {
    if record.checksum_source != ChecksumSource::GitHubDigest {
        return Ok(());
    }
    let mut spec = SourceSpec::from_str(&record.source)?;
    spec.version = Some(record.release_tag.clone());
    let release = client_for_source(&spec)?.resolve_release(&spec)?.release;
    if record_matches_current_provider_digest(record, &release.assets) {
        return Ok(());
    }
    Err(BinpmError::ProviderDigestMismatch {
        package: record.package_spec.clone(),
    })
}

fn local_runtime_lock_records(
    manifest: &Manifest,
    lockfile: &crate::storage::Lockfile,
    target: &HostTarget,
) -> Result<BTreeMap<String, PackageRecord>> {
    let mut records = BTreeMap::new();
    let target_key = target.key();
    for cmd in manifest.tools.keys() {
        let locked_tool = lockfile.tools.get(cmd).ok_or(BinpmError::StaleLockfile {
            path: PathBuf::from(LOCKFILE_FILE),
            cmd: cmd.clone(),
        })?;
        let record = locked_tool
            .targets
            .get(&target_key)
            .ok_or(BinpmError::StaleLockfile {
                path: PathBuf::from(LOCKFILE_FILE),
                cmd: cmd.clone(),
            })?;
        records.insert(cmd.clone(), record.clone());
    }
    Ok(records)
}

fn assert_local_runtime_records_complete(
    root: &Path,
    remaining_locks: &BTreeMap<String, PackageRecord>,
) -> Result<()> {
    if let Some(cmd) = remaining_locks.keys().next() {
        return Err(BinpmError::StaleLockfile {
            path: root.join(LOCKFILE_FILE),
            cmd: cmd.clone(),
        });
    }
    Ok(())
}

fn assert_runtime_record_matches_lock(
    root: &Path,
    cmd: &str,
    lock_record: &PackageRecord,
    runtime_record: &PackageRecord,
) -> Result<()> {
    if runtime_record.source != lock_record.source
        || runtime_record.source_provider != lock_record.source_provider
        || runtime_record.source_host != lock_record.source_host
        || runtime_record.source_path != lock_record.source_path
        || runtime_record.requested_version != lock_record.requested_version
        || runtime_record.release_tag != lock_record.release_tag
        || runtime_record.asset_name != lock_record.asset_name
        || runtime_record.asset_url != lock_record.asset_url
        || runtime_record.target_os != lock_record.target_os
        || runtime_record.target_arch != lock_record.target_arch
        || runtime_record.target_libc != lock_record.target_libc
        || runtime_record.archive_format != lock_record.archive_format
        || runtime_record.selected_binary != lock_record.selected_binary
        || runtime_record.sha256 != lock_record.sha256
        || runtime_record.checksum_source != lock_record.checksum_source
        || runtime_record.signature_available != lock_record.signature_available
        || runtime_record.signature_verified != lock_record.signature_verified
    {
        return Err(BinpmError::StaleLockfile {
            path: root.join(LOCKFILE_FILE),
            cmd: cmd.to_string(),
        });
    }
    Ok(())
}

fn verify_lockfile_records(
    lockfile_path: &Path,
    lockfile: crate::storage::Lockfile,
    manifest: Option<(&Manifest, &Path)>,
    require_verified: bool,
) -> Result<(usize, BTreeSet<String>)> {
    let mut checked = 0usize;
    let mut locked = BTreeSet::new();
    if let Some((manifest, root)) = manifest {
        for (cmd, manifest_tool) in &manifest.tools {
            validate_command_name(cmd)?;
            let mut spec = parse_manifest_source(&manifest_tool.source)?;
            spec.version = manifest_tool.version.clone();
            let locked_tool = lockfile.tools.get(cmd).ok_or(BinpmError::FrozenLockfile {
                path: lockfile_path.to_path_buf(),
            })?;
            if locked_tool.source != spec.source_without_version() || locked_tool.targets.is_empty()
            {
                return Err(BinpmError::StaleLockfile {
                    path: lockfile_path.to_path_buf(),
                    cmd: cmd.clone(),
                });
            }
            for (target_key, record) in &locked_tool.targets {
                let target = HostTarget::from_str(target_key)?;
                if target_key != &target.key() {
                    return Err(BinpmError::StaleLockfile {
                        path: lockfile_path.to_path_buf(),
                        cmd: cmd.clone(),
                    });
                }
                if record.requested_version != spec.version {
                    return Err(BinpmError::StaleLockfile {
                        path: lockfile_path.to_path_buf(),
                        cmd: cmd.clone(),
                    });
                }
                assert_lock_record_matches_source_and_target(
                    lockfile_path,
                    cmd,
                    &spec,
                    &target,
                    record,
                )?;
                assert_lock_matches_manifest_tool(root, cmd, Some(manifest_tool), &target, record)?;
            }
        }
    }
    for (cmd, tool) in lockfile.tools {
        validate_command_name(&cmd)?;
        if let Some((manifest, _)) = manifest {
            if !manifest.tools.contains_key(&cmd) {
                return Err(BinpmError::StaleLockfile {
                    path: lockfile_path.to_path_buf(),
                    cmd: cmd.clone(),
                });
            }
        }
        for (target_key, record) in tool.targets {
            let target = HostTarget::from_str(&target_key)?;
            if target_key != target.key() {
                return Err(BinpmError::StaleLockfile {
                    path: lockfile_path.to_path_buf(),
                    cmd: cmd.clone(),
                });
            }
            let spec = SourceSpec::from_str(
                &record
                    .requested_version
                    .as_ref()
                    .map(|version| format!("{}@{version}", record.source))
                    .unwrap_or_else(|| record.source.clone()),
            )?;
            if tool.source != record.source || tool.source != spec.source_without_version() {
                return Err(BinpmError::StaleLockfile {
                    path: lockfile_path.to_path_buf(),
                    cmd: cmd.clone(),
                });
            }
            assert_lock_record_matches_source_and_target(
                lockfile_path,
                &cmd,
                &spec,
                &target,
                &record,
            )?;
            let manifest_tool = manifest.and_then(|(manifest, _)| manifest.tools.get(&cmd));
            validate_locked_record_artifact(lockfile_path, &cmd, &record, &target, manifest_tool)?;
            if require_verified && !record.has_verified_source() {
                return Err(BinpmError::VerificationRequired {
                    package: record.package_spec,
                });
            }
            validate_provider_digest_evidence(&record)?;
            validate_locked_record_current_release(lockfile_path, &cmd, &record)?;
            locked.insert(cmd.clone());
            println!(
                "{cmd} lock verified {target_key} {}",
                record.checksum_source.as_str()
            );
            checked += 1;
        }
    }
    Ok((checked, locked))
}

fn init(args: InitArgs) -> Result<i32> {
    let project_root = manifest_creation_root()?;
    let manifest_path = project_root.join(MANIFEST_FILE);

    if path_exists_or_unreadable(&manifest_path) && !args.force {
        return Err(BinpmError::ManifestExists {
            path: manifest_path,
        });
    }

    write_manifest(
        &manifest_path,
        &Manifest {
            version: 1,
            tools: BTreeMap::new(),
        },
    )?;

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
    let project_root = project_root()?;
    let home = binpm_home()?;
    let global_bin = home.join("bin");
    let local_bin = project_root.join(".binpm").join("bin");

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
    let global = shell_quote(shell, global_bin);
    let local = shell_quote(shell, local_bin);
    match shell {
        Shell::Bash | Shell::Zsh => {
            println!("export PATH={local}:{global}${{PATH:+:$PATH}}");
        }
        Shell::Fish => {
            println!("set -gx PATH {local} {global} $PATH");
        }
        Shell::Powershell => {
            println!(
                "$env:PATH = {local} + [System.IO.Path]::PathSeparator + {global} + $(if \
                 ($env:PATH) {{ [System.IO.Path]::PathSeparator + $env:PATH }} else {{ '' }})"
            );
        }
    }
}

fn shell_quote(shell: Shell, path: &Path) -> String {
    let raw = shell_path(shell, &path.display().to_string());
    match shell {
        Shell::Bash | Shell::Zsh => posix_single_quote(&raw),
        Shell::Fish => fish_single_quote(&raw),
        Shell::Powershell => powershell_single_quote(&raw),
    }
}

fn shell_path(shell: Shell, raw: &str) -> String {
    match shell {
        Shell::Bash | Shell::Zsh => {
            windows_path_for_posix_shell(raw).unwrap_or_else(|| raw.to_owned())
        }
        Shell::Fish | Shell::Powershell => raw.to_owned(),
    }
}

fn windows_path_for_posix_shell(raw: &str) -> Option<String> {
    if let Some(unc) = raw
        .strip_prefix(r"\\?\UNC\")
        .or_else(|| raw.strip_prefix(r"\\.\UNC\"))
    {
        return Some(format!("//{}", unc.replace('\\', "/")));
    }

    let raw = raw
        .strip_prefix(r"\\?\")
        .or_else(|| raw.strip_prefix(r"\\.\"))
        .unwrap_or(raw);

    if let Some(unc) = raw.strip_prefix(r"\\") {
        return Some(format!("//{}", unc.replace('\\', "/")));
    }

    let bytes = raw.as_bytes();
    if bytes.len() >= 3
        && bytes[0].is_ascii_alphabetic()
        && bytes[1] == b':'
        && matches!(bytes[2], b'\\' | b'/')
    {
        let drive = (bytes[0] as char).to_ascii_lowercase();
        let rest = raw[2..].replace('\\', "/");
        return Some(format!("/{drive}{}", rest));
    }

    None
}

fn posix_single_quote(raw: &str) -> String {
    format!("'{}'", raw.replace('\'', "'\\''"))
}

fn fish_single_quote(raw: &str) -> String {
    format!("'{}'", raw.replace('\\', "\\\\").replace('\'', "\\'"))
}

fn powershell_single_quote(raw: &str) -> String {
    format!("'{}'", raw.replace('\'', "''"))
}

fn log_read_only_scope(command: &'static str, scope: Scope) {
    info!(
        command,
        read_only = true,
        selected_scope = scope.as_str(),
        "Prepared read-only command request"
    );
}

fn lockfile_digest(path: &Path) -> Result<String> {
    let bytes = match fs::read(path) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            match fs::symlink_metadata(path) {
                Ok(_) => {
                    return Err(BinpmError::ReadFile {
                        path: path.to_path_buf(),
                        source: error,
                    })
                }
                Err(metadata_error) if metadata_error.kind() == std::io::ErrorKind::NotFound => {
                    Vec::new()
                }
                Err(source) => {
                    return Err(BinpmError::ReadFile {
                        path: path.to_path_buf(),
                        source,
                    })
                }
            }
        }
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

fn project_root() -> Result<PathBuf> {
    let cwd = current_dir()?;
    Ok(project_root_from(&cwd))
}

fn manifest_project_root() -> Result<Option<PathBuf>> {
    let cwd = current_dir()?;
    Ok(manifest_project_root_from(&cwd))
}

fn manifest_project_root_from(start: &Path) -> Option<PathBuf> {
    find_manifest_root(start).map(Path::to_path_buf)
}

fn project_root_from(start: &Path) -> PathBuf {
    find_manifest_root(start)
        .or_else(|| find_git_root(start))
        .unwrap_or(start)
        .to_path_buf()
}

fn manifest_creation_root() -> Result<PathBuf> {
    let cwd = current_dir()?;
    Ok(manifest_creation_root_from(&cwd))
}

fn manifest_creation_root_from(start: &Path) -> PathBuf {
    find_git_root(start)
        .or_else(|| find_manifest_root(start))
        .unwrap_or(start)
        .to_path_buf()
}

fn manifest_root_or_creation_root_from(start: &Path) -> PathBuf {
    find_manifest_root(start)
        .map(Path::to_path_buf)
        .unwrap_or_else(|| manifest_creation_root_from(start))
}

fn find_manifest_root(start: &Path) -> Option<&Path> {
    start
        .ancestors()
        .find(|path| path_exists_or_unreadable(&path.join(MANIFEST_FILE)))
}

fn find_git_root(start: &Path) -> Option<&Path> {
    start.ancestors().find(|path| path.join(".git").exists())
}

fn path_exists_or_unreadable(path: &Path) -> bool {
    match fs::symlink_metadata(path) {
        Ok(_) => true,
        Err(source) => source.kind() != ErrorKind::NotFound,
    }
}

fn binpm_home() -> Result<PathBuf> {
    binpm_home_from_values(
        env_path("BINPM_HOME"),
        env_path("HOME"),
        env_path("USERPROFILE"),
    )
}

fn env_path(name: &str) -> Option<PathBuf> {
    std::env::var_os(name)
        .filter(|value| !value.as_os_str().is_empty())
        .map(PathBuf::from)
}

fn absolute_global_home(name: &'static str, path: PathBuf) -> Result<PathBuf> {
    if path.is_absolute() {
        Ok(path)
    } else {
        Err(BinpmError::InvalidGlobalHome { name, path })
    }
}

fn binpm_home_from_values(
    binpm_home: Option<PathBuf>,
    home: Option<PathBuf>,
    userprofile: Option<PathBuf>,
) -> Result<PathBuf> {
    if let Some(home) = binpm_home {
        return absolute_global_home("BINPM_HOME", home);
    }

    let home_error = if let Some(home) = home {
        match absolute_global_home("HOME", home.join(".binpm")) {
            Ok(home) => return Ok(home),
            Err(error) => Some(error),
        }
    } else {
        None
    };

    if let Some(home) = userprofile {
        return absolute_global_home("USERPROFILE", home.join(".binpm"));
    }

    if let Some(error) = home_error {
        return Err(error);
    }

    Err(BinpmError::MissingGlobalHome)
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
    use std::{
        collections::BTreeMap,
        fs,
        io::Write,
        path::{Path, PathBuf},
        str::FromStr,
    };

    use sha2::{Digest, Sha256};

    use super::{
        assert_local_runtime_records_complete, assert_lock_matches_manifest_tool,
        assert_lock_record_matches_source_and_target, assert_runtime_record_matches_lock,
        binpm_home_from_values, capture_local_remove_state, capture_runtime_tool_state,
        cleanup_failed_install_cache, commit_deferred_cache_hit, deterministic_installed_path,
        download_asset_name, ensure_no_package_record_install_path_collision, execute_command,
        format_download_progress, github_sha256_digest, has_current_cache_record,
        has_local_runtime_or_lock_state, install_local_from_lock, install_path_collision_key,
        is_retryable_status, local_runtime_lock_records, local_tool_execution_ready,
        lock_targets_conflict_with_manifest, lock_targets_conflict_with_record,
        locked_release_lookup_spec, lockfile_digest, manifest_checksum_source,
        manifest_creation_root_from, manifest_project_root_from,
        manifest_root_or_creation_root_from, manifest_target_override, manifest_tool_from_source,
        parse_manifest_source, project_root_from, read_archive_selected_binary,
        record_matches_current_provider_digest, remove_global_tool_from_paths,
        remove_local_manifest_orphans, require_executable_managed_file, restore_local_remove_state,
        restore_runtime_tool_state, sanitize_download_diagnostic_url, select_manifest_asset,
        selected_asset_display_url, shell_path, shell_quote, snapshot_cache_metadata,
        source_install_scope, update_manifest_tool_source, validate_locked_record_artifact,
        validate_locked_record_current_asset, validate_locked_record_current_provider_digest,
        validate_package_record_metadata, validate_package_record_source_identity,
        validate_provider_digest_evidence, validate_selected_manifest_entries,
        verify_installed_binary_contents, verify_lockfile_records, verify_runtime_cache_bytes,
        zip_file_is_regular, zip_file_is_symlink, ArtifactKind, InstalledPackage,
        InstalledPathSnapshot, LocalRemoveState, RuntimeToolState,
    };
    use crate::{
        assets::CandidateDecision,
        cli::Shell,
        contract::{
            ArchiveFormat, ChecksumSource, HostTarget, Scope, SourceProvider, SourceSpec,
            TargetArch, TargetLibc, TargetOs,
        },
        error::BinpmError,
        release::ReleaseAsset,
        storage::{
            managed_installed_path, read_cache_records, require_regular_managed_file,
            validate_installed_binary_path, write_cache_record, write_lockfile, write_manifest,
            write_package_record, CachePaths, CacheRecord, LockTool, Lockfile, Manifest,
            ManifestTargetOverride, ManifestTool, PackageRecord, ResolvedAsset, ScopePaths,
            LOCKFILE_FILE, MANIFEST_FILE,
        },
    };

    #[test]
    fn source_installs_default_to_global_scope() {
        assert_eq!(source_install_scope(Scope::Auto), Scope::Global);
        assert_eq!(source_install_scope(Scope::Global), Scope::Global);
        assert_eq!(source_install_scope(Scope::Local), Scope::Local);
    }

    #[test]
    fn download_diagnostic_urls_redact_credentials_queries_and_fragments() {
        assert_eq!(
            sanitize_download_diagnostic_url("https://token:secret@example.com/asset?sig=secret#x"),
            "https://example.com/asset"
        );
    }

    #[test]
    fn download_asset_name_uses_sanitized_path_basename() {
        assert_eq!(
            download_asset_name("https://example.com/releases/download/v1/tool.tar.gz"),
            "tool.tar.gz"
        );
        assert_eq!(download_asset_name("not a url"), "release asset");
    }

    #[test]
    fn download_progress_format_is_human_readable() {
        assert_eq!(
            format_download_progress(5 * 1024 * 1024, Some(10 * 1024 * 1024)),
            "5.0 MiB/10.0 MiB"
        );
    }

    #[test]
    fn retryable_download_statuses_are_limited_to_rate_limits_and_server_errors() {
        assert!(is_retryable_status(reqwest::StatusCode::TOO_MANY_REQUESTS));
        assert!(is_retryable_status(reqwest::StatusCode::BAD_GATEWAY));
        assert!(!is_retryable_status(reqwest::StatusCode::NOT_FOUND));
    }

    #[test]
    fn archive_extraction_discovers_nested_repo_binary() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.tar.gz");
        write_tar_gz(
            &archive_path,
            &[
                ("tool-1.0.0/README.md", b"docs".as_slice(), 0o644),
                ("tool-1.0.0/tool", b"#!/bin/sh\nexit 0\n".as_slice(), 0o755),
            ],
        );

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::TarGz,
            "tool.tar.gz",
            "tool",
            &linux_target(),
            None,
        )
        .expect("selected binary");

        assert_eq!(selected.path, "tool-1.0.0/tool");
        assert_eq!(selected.bytes, b"#!/bin/sh\nexit 0\n");
    }

    #[test]
    fn archive_extraction_skips_root_tar_directory_entry() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.tar.gz");
        let file = fs::File::create(&archive_path).expect("create archive");
        let encoder = flate2::write::GzEncoder::new(file, flate2::Compression::default());
        let mut builder = tar::Builder::new(encoder);
        let mut directory_header = tar::Header::new_gnu();
        directory_header.set_entry_type(tar::EntryType::Directory);
        directory_header.set_size(0);
        directory_header.set_mode(0o755);
        directory_header.set_cksum();
        builder
            .append_data(&mut directory_header, ".", std::io::empty())
            .expect("append root directory entry");
        let mut file_header = tar::Header::new_gnu();
        let bytes = b"#!/bin/sh\nexit 0\n";
        file_header.set_size(bytes.len() as u64);
        file_header.set_mode(0o755);
        file_header.set_cksum();
        builder
            .append_data(&mut file_header, "tool", bytes.as_slice())
            .expect("append executable entry");
        builder.finish().expect("finish tar");
        let encoder = builder.into_inner().expect("finish gzip stream");
        encoder.finish().expect("finish gzip file");

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::TarGz,
            "tool.tar.gz",
            "tool",
            &linux_target(),
            None,
        )
        .expect("selected binary");

        assert_eq!(selected.path, "tool");
        assert_eq!(selected.bytes, bytes);
    }

    #[test]
    fn archive_extraction_rejects_parent_directory_traversal() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip(&archive_path, &[("../tool", b"bad".as_slice(), 0o755)]);

        let error = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect_err("unsafe archive path");

        assert!(matches!(error, BinpmError::UnsafeArchivePath { .. }));
        assert!(error.to_string().contains("parent-directory traversal"));
    }

    #[test]
    fn archive_extraction_rejects_duplicate_member_paths() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip(
            &archive_path,
            &[
                ("./pkg/tool", b"first".as_slice(), 0o755),
                ("pkg/tool", b"second".as_slice(), 0o755),
            ],
        );

        let error = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect_err("duplicate archive path");

        assert!(matches!(error, BinpmError::UnsafeArchivePath { .. }));
        assert!(error.to_string().contains("duplicate archive member path"));
    }

    #[test]
    fn archive_extraction_requires_explicit_member_to_be_executable() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip(
            &archive_path,
            &[
                ("pkg/tool", b"config".as_slice(), 0o644),
                ("pkg/helper", b"#!/bin/sh\nexit 0\n".as_slice(), 0o755),
            ],
        );

        let error = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            Some("pkg/tool"),
        )
        .expect_err("non-executable explicit member");

        assert!(matches!(error, BinpmError::ArchiveMemberNotFound { .. }));
    }

    #[test]
    fn archive_extraction_ignores_exe_candidates_on_non_windows_targets() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip(
            &archive_path,
            &[("pkg/tool.exe", b"windows".as_slice(), 0o644)],
        );

        let error = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect_err("windows exe is not a linux executable candidate");

        assert!(matches!(error, BinpmError::ArchiveBinaryNotFound { .. }));
    }

    #[test]
    fn archive_extraction_matches_explicit_windows_binary_without_exe_suffix() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip(
            &archive_path,
            &[("pkg/tool.exe", b"windows".as_slice(), 0o100644)],
        );

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &HostTarget {
                os: TargetOs::Windows,
                arch: TargetArch::X86_64,
                libc: TargetLibc::Msvc,
            },
            Some("tool"),
        )
        .expect("selected windows exe");

        assert_eq!(selected.path, "pkg/tool.exe");
        assert_eq!(selected.bytes, b"windows");
    }

    #[test]
    fn archive_extraction_filters_explicit_basename_matches_by_target() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip(
            &archive_path,
            &[
                ("bin/darwin/bar", b"darwin".as_slice(), 0o100755),
                ("bin/linux/bar", b"linux".as_slice(), 0o100755),
            ],
        );

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            Some("bar"),
        )
        .expect("selected target-matching explicit binary");

        assert_eq!(selected.path, "bin/linux/bar");
        assert_eq!(selected.bytes, b"linux");
    }

    #[test]
    fn zip_symlink_detection_checks_unix_file_type_bits() {
        assert!(zip_file_is_symlink(Some(0o120777)));
        assert!(!zip_file_is_symlink(Some(0o100755)));
        assert!(!zip_file_is_symlink(Some(0o755)));
        assert!(!zip_file_is_symlink(None));
    }

    #[test]
    fn zip_regular_file_detection_rejects_non_regular_file_type_bits() {
        assert!(zip_file_is_regular(Some(0o100755)));
        assert!(zip_file_is_regular(Some(0o755)));
        assert!(zip_file_is_regular(None));
        assert!(!zip_file_is_regular(Some(0o010755)));
        assert!(!zip_file_is_regular(Some(0o020755)));
        assert!(!zip_file_is_regular(Some(0o120777)));
    }

    #[test]
    fn package_record_verify_checks_archive_installed_member_bytes() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(&temp_dir.path().join("home"));
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip(
            &archive_path,
            &[("pkg/bin/tool", b"#!/bin/sh\nexit 0\n".as_slice(), 0o755)],
        );
        let archive_bytes = fs::read(&archive_path).expect("read archive");
        let sha256 = format!("{:x}", Sha256::digest(&archive_bytes));
        fs::create_dir_all(cache.entry_dir(&sha256)).expect("cache entry");
        fs::write(cache.asset_path(&sha256), archive_bytes).expect("cache asset");

        let installed_path = temp_dir.path().join("tool");
        fs::write(&installed_path, b"#!/bin/sh\nexit 1\n").expect("bad installed binary");
        let mut record = package_record();
        record.sha256 = sha256;
        record.archive_format = ArchiveFormat::Zip;
        record.asset_name = "tool.zip".to_string();
        record.selected_binary = "tool".to_string();

        let error = verify_installed_binary_contents(&cache, &record, &installed_path)
            .expect_err("installed bytes differ from archive member");
        assert!(matches!(error, BinpmError::DigestMismatch { .. }));

        fs::write(&installed_path, b"#!/bin/sh\nexit 0\n").expect("good installed binary");
        verify_installed_binary_contents(&cache, &record, &installed_path)
            .expect("installed archive member matches");
    }

    #[cfg(unix)]
    #[test]
    fn execution_prepends_path_forwards_args_and_preserves_current_directory() {
        use std::os::unix::fs::PermissionsExt;

        let temp_dir = tempfile::tempdir().expect("tempdir");
        let bin_dir = temp_dir.path().join("bin");
        let work_dir = temp_dir.path().join("work");
        let output_path = temp_dir.path().join("out.txt");
        fs::create_dir_all(&bin_dir).expect("create bin");
        fs::create_dir_all(&work_dir).expect("create work");
        let script = bin_dir.join("probe");
        fs::write(
            &script,
            format!(
                "#!/bin/sh\nprintf 'pwd=%s\\nargs=%s|%s\\n' \"$PWD\" \"$1\" \"$2\" > '{}'\n",
                output_path.display()
            ),
        )
        .expect("write script");
        let mut permissions = fs::metadata(&script).expect("metadata").permissions();
        permissions.set_mode(0o755);
        fs::set_permissions(&script, permissions).expect("chmod");
        let prior_cwd = std::env::current_dir().expect("current dir");
        std::env::set_current_dir(&work_dir).expect("set cwd");

        let result = execute_command(
            "probe",
            &["--flag".into(), "value with spaces".into()],
            std::slice::from_ref(&bin_dir),
        );

        std::env::set_current_dir(prior_cwd).expect("restore cwd");
        assert_eq!(result.expect("execute probe"), 0);
        let output = fs::read_to_string(output_path).expect("read output");
        assert!(output.contains(&format!("pwd={}", work_dir.display())));
        assert!(output.contains("args=--flag|value with spaces"));
    }

    #[test]
    fn manifest_tool_source_update_preserves_overrides() {
        let spec = SourceSpec::from_str("github:owner/new-tool@2.0.0").expect("source");
        let mut targets = BTreeMap::new();
        targets.insert(
            "linux-x86_64-gnu".to_string(),
            ManifestTargetOverride {
                asset: "custom-asset".to_string(),
                bin: "custom-bin".to_string(),
                checksum_source: None,
            },
        );
        let existing = ManifestTool {
            source: "github:owner/old-tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: Some("custom-bin".to_string()),
            targets: targets.clone(),
        };

        let updated = update_manifest_tool_source(Some(existing), &spec);

        assert_eq!(updated.source, "github:owner/new-tool");
        assert_eq!(updated.version.as_deref(), Some("2.0.0"));
        assert_eq!(updated.bin.as_deref(), Some("custom-bin"));
        assert_eq!(
            updated.targets.keys().collect::<Vec<_>>(),
            targets.keys().collect::<Vec<_>>()
        );
    }

    #[test]
    fn global_home_falls_back_to_userprofile_after_invalid_home() {
        let userprofile = tempfile::tempdir().expect("userprofile");
        let home = binpm_home_from_values(
            None,
            Some(PathBuf::from("relative-home")),
            Some(userprofile.path().to_path_buf()),
        )
        .expect("global home");

        assert_eq!(home, userprofile.path().join(".binpm"));
    }

    #[test]
    fn global_home_keeps_invalid_home_error_without_userprofile() {
        let error = binpm_home_from_values(None, Some(PathBuf::from("relative-home")), None)
            .expect_err("invalid home");

        assert!(error.to_string().contains("Invalid HOME"));
    }

    #[test]
    fn manifest_project_root_ignores_git_roots_without_manifest() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        fs::create_dir(temp_dir.path().join(".git")).expect("git dir");
        let nested = temp_dir.path().join("nested");
        fs::create_dir(&nested).expect("nested dir");

        assert_eq!(manifest_project_root_from(&nested), None);

        write_manifest(
            &temp_dir.path().join(MANIFEST_FILE),
            &Manifest {
                version: 1,
                tools: BTreeMap::new(),
            },
        )
        .expect("write manifest");

        assert_eq!(
            manifest_project_root_from(&nested).as_deref(),
            Some(temp_dir.path())
        );
    }

    #[test]
    fn missing_lockfile_has_stable_empty_digest() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let digest = lockfile_digest(&temp_dir.path().join("binpm.lock")).expect("digest");

        assert_eq!(
            digest,
            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
        );
    }

    #[cfg(unix)]
    #[test]
    fn lockfile_digest_reports_broken_symlink() {
        use std::os::unix::fs::symlink;

        let temp_dir = tempfile::tempdir().expect("tempdir");
        let lockfile_path = temp_dir.path().join("binpm.lock");
        symlink(temp_dir.path().join("missing.lock"), &lockfile_path).expect("broken symlink");

        let error = lockfile_digest(&lockfile_path).expect_err("broken symlink is unreadable");

        assert!(error.to_string().contains("Failed to read"));
    }

    #[test]
    fn deterministic_local_installed_paths_use_windows_exe_suffix() {
        assert_eq!(
            deterministic_installed_path("tool", TargetOs::Windows),
            ".binpm/bin/tool.exe"
        );
        assert_eq!(
            deterministic_installed_path("tool.exe", TargetOs::Windows),
            ".binpm/bin/tool.exe"
        );
        assert_eq!(
            deterministic_installed_path("TOOL.EXE", TargetOs::Windows),
            ".binpm/bin/tool.exe"
        );
        assert_eq!(
            deterministic_installed_path("tool", TargetOs::Linux),
            ".binpm/bin/tool"
        );
    }

    #[test]
    fn global_source_install_rejects_windows_exe_collision() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::global(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let mut record = package_record();
        record.target_os = TargetOs::Windows;
        write_package_record(&paths, "foo", &record).expect("write package record");

        let error =
            ensure_no_package_record_install_path_collision(&paths, "foo.exe", TargetOs::Windows)
                .expect_err("collision");

        assert!(matches!(error, BinpmError::InstalledPathCollision { .. }));
    }

    #[test]
    fn local_source_install_rejects_windows_exe_collision() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let mut record = package_record();
        record.target_os = TargetOs::Windows;
        write_package_record(&paths, "foo", &record).expect("write package record");

        let error =
            ensure_no_package_record_install_path_collision(&paths, "foo.exe", TargetOs::Windows)
                .expect_err("collision");

        assert!(matches!(error, BinpmError::InstalledPathCollision { .. }));
    }

    #[test]
    fn global_source_install_rejects_darwin_case_insensitive_collision() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::global(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let mut record = package_record();
        record.target_os = TargetOs::Darwin;
        write_package_record(&paths, "foo", &record).expect("write package record");

        let error =
            ensure_no_package_record_install_path_collision(&paths, "FOO", TargetOs::Darwin)
                .expect_err("collision");

        assert!(matches!(error, BinpmError::InstalledPathCollision { .. }));
    }

    #[test]
    fn darwin_install_path_collision_keys_are_case_insensitive() {
        assert_eq!(
            install_path_collision_key(Path::new(".binpm/bin/FOO"), TargetOs::Darwin),
            install_path_collision_key(Path::new(".binpm/bin/foo"), TargetOs::Darwin)
        );
        assert_ne!(
            install_path_collision_key(Path::new(".binpm/bin/FOO"), TargetOs::Linux),
            install_path_collision_key(Path::new(".binpm/bin/foo"), TargetOs::Linux)
        );
    }

    #[test]
    fn selected_manifest_entries_validate_sources_before_installing_any_tool() {
        let first_spec = SourceSpec::from_str("github:owner/first").expect("first source");
        let manifest = Manifest {
            version: 1,
            tools: BTreeMap::from([
                ("first".to_string(), manifest_tool_from_source(&first_spec)),
                (
                    "second".to_string(),
                    ManifestTool {
                        source: "github:owner/second@1.0.0".to_string(),
                        version: None,
                        bin: None,
                        targets: BTreeMap::new(),
                    },
                ),
            ]),
        };

        let error = validate_selected_manifest_entries(&manifest, &[]).expect_err("invalid source");

        assert!(matches!(error, BinpmError::InvalidSourceSpec { .. }));
    }

    #[test]
    fn frozen_local_install_rejects_stale_non_current_lock_targets() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source");
        let current_target = HostTarget::current().expect("current target");
        let mut current_record = package_record();
        current_record.target_os = current_target.os;
        current_record.target_arch = current_target.arch;
        current_record.target_libc = current_target.libc;
        current_record.installed_path = deterministic_installed_path("tool", current_target.os);

        let mut stale_windows_record = package_record();
        stale_windows_record.requested_version = Some("0.9.0".to_string());
        stale_windows_record.target_os = TargetOs::Windows;
        stale_windows_record.target_arch = TargetArch::X86_64;
        stale_windows_record.target_libc = TargetLibc::Msvc;
        stale_windows_record.installed_path =
            deterministic_installed_path("tool", TargetOs::Windows);

        write_lockfile(
            &temp_dir.path().join(LOCKFILE_FILE),
            &Lockfile {
                version: 1,
                tools: BTreeMap::from([(
                    "tool".to_string(),
                    LockTool {
                        source: "github:owner/tool".to_string(),
                        targets: BTreeMap::from([
                            (current_target.key(), current_record),
                            ("windows-x86_64-msvc".to_string(), stale_windows_record),
                        ]),
                    },
                )]),
            },
        )
        .expect("write lockfile");

        let error = match install_local_from_lock(temp_dir.path(), "tool", &spec, None, false) {
            Ok(_) => panic!("expected stale lockfile"),
            Err(error) => error,
        };

        assert!(matches!(error, BinpmError::StaleLockfile { .. }));
    }

    #[test]
    fn frozen_lock_detects_changed_target_override_asset() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: None,
            targets: BTreeMap::from([(
                target.key(),
                ManifestTargetOverride {
                    asset: "tool-new".to_string(),
                    bin: "tool".to_string(),
                    checksum_source: None,
                },
            )]),
        };
        let mut record = package_record();
        record.asset_name = "tool-old".to_string();
        record.selected_binary = "tool".to_string();

        let error = assert_lock_matches_manifest_tool(
            temp_dir.path(),
            "tool",
            Some(&tool),
            &target,
            &record,
        )
        .expect_err("stale lockfile");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn frozen_lock_detects_changed_manifest_bin_override() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: Some("new-bin".to_string()),
            targets: BTreeMap::new(),
        };
        let mut record = package_record();
        record.selected_binary = "old-bin".to_string();

        let error = assert_lock_matches_manifest_tool(
            temp_dir.path(),
            "tool",
            Some(&tool),
            &target,
            &record,
        )
        .expect_err("stale lockfile");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn frozen_lock_rejects_manifest_checksum_source_override() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: None,
            targets: BTreeMap::from([(
                target.key(),
                ManifestTargetOverride {
                    asset: "tool-linux".to_string(),
                    bin: "tool-linux".to_string(),
                    checksum_source: Some(ChecksumSource::Manifest),
                },
            )]),
        };
        let mut record = package_record();
        record.checksum_source = ChecksumSource::Manifest;

        let error = assert_lock_matches_manifest_tool(
            temp_dir.path(),
            "tool",
            Some(&tool),
            &target,
            &record,
        )
        .expect_err("declarative checksum override");

        assert!(error.to_string().contains("cannot be used"));
    }

    #[test]
    fn manifest_source_rejects_embedded_version() {
        let error = parse_manifest_source("github:owner/tool@1.0.0")
            .expect_err("versioned manifest source");

        assert!(error.to_string().contains("must be versionless"));
    }

    #[test]
    fn source_install_manifest_entry_keeps_version_separate() {
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let tool = manifest_tool_from_source(&spec);

        assert_eq!(tool.source, "github:owner/tool");
        assert_eq!(tool.version.as_deref(), Some("1.0.0"));
        assert!(tool.bin.is_none());
        assert!(tool.targets.is_empty());
    }

    #[test]
    fn source_install_manifest_update_preserves_existing_overrides() {
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@2.0.0").expect("source spec");
        let existing = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: Some("custom-bin".to_string()),
            targets: BTreeMap::from([(
                target.key(),
                ManifestTargetOverride {
                    asset: "custom-asset".to_string(),
                    bin: "custom-bin".to_string(),
                    checksum_source: None,
                },
            )]),
        };

        let updated = update_manifest_tool_source(Some(existing), &spec);

        assert_eq!(updated.source, "github:owner/tool");
        assert_eq!(updated.version.as_deref(), Some("2.0.0"));
        assert_eq!(updated.bin.as_deref(), Some("custom-bin"));
        assert_eq!(updated.targets[&target.key()].asset, "custom-asset");
    }

    #[test]
    fn lock_targets_are_cleared_when_resolution_changes() {
        let target = linux_target();
        let current = package_record();
        let mut stale = package_record();
        stale.target_os = TargetOs::Darwin;
        stale.target_libc = TargetLibc::Any;
        stale.release_tag = "0.9.0".to_string();
        stale.requested_version = Some("0.9.0".to_string());
        let tool = LockTool {
            source: "github:owner/tool".to_string(),
            targets: BTreeMap::from([
                (target.key(), current.clone()),
                ("darwin-x86_64-any".to_string(), stale),
            ]),
        };

        assert!(lock_targets_conflict_with_record(&tool, &current));

        let matching_tool = LockTool {
            source: "github:owner/tool".to_string(),
            targets: BTreeMap::from([(target.key(), current.clone())]),
        };
        assert!(!lock_targets_conflict_with_record(&matching_tool, &current));
    }

    #[test]
    fn lock_targets_are_cleared_when_manifest_override_changes() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source");
        let mut current = package_record();
        current.selected_binary = "tool-new".to_string();
        let mut stale = package_record();
        stale.selected_binary = "tool-old".to_string();
        let lock_tool = LockTool {
            source: "github:owner/tool".to_string(),
            targets: BTreeMap::from([(target.key(), stale)]),
        };
        let manifest_tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: Some("tool-new".to_string()),
            targets: BTreeMap::new(),
        };

        assert!(lock_targets_conflict_with_manifest(
            &temp_dir.path().join("binpm.lock"),
            temp_dir.path(),
            "tool",
            &spec,
            Some(&manifest_tool),
            &lock_tool,
        ));

        let matching_tool = LockTool {
            source: "github:owner/tool".to_string(),
            targets: BTreeMap::from([(target.key(), current)]),
        };
        assert!(!lock_targets_conflict_with_manifest(
            &temp_dir.path().join("binpm.lock"),
            temp_dir.path(),
            "tool",
            &spec,
            Some(&manifest_tool),
            &matching_tool,
        ));
    }

    #[test]
    fn strict_lockfile_verify_checks_every_target_record() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let mut linux_record = package_record();
        mark_github_verified(&mut linux_record);
        let mut darwin_record = package_record();
        darwin_record.target_os = TargetOs::Darwin;
        darwin_record.target_libc = TargetLibc::Any;
        darwin_record.checksum_source = ChecksumSource::Local;
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                LockTool {
                    source: "github:owner/tool".to_string(),
                    targets: BTreeMap::from([
                        ("linux-x86_64-gnu".to_string(), linux_record),
                        ("darwin-x86_64-any".to_string(), darwin_record),
                    ]),
                },
            )]),
        };

        let error =
            verify_lockfile_records(&temp_dir.path().join("binpm.lock"), lockfile, None, true)
                .expect_err("unverified target is rejected");

        assert!(error.to_string().contains("github:owner/tool@1.0.0"));
    }

    #[test]
    fn strict_lockfile_verify_rejects_digest_label_without_evidence() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let mut record = package_record();
        record.checksum_source = ChecksumSource::GitHubDigest;
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                LockTool {
                    source: "github:owner/tool".to_string(),
                    targets: BTreeMap::from([("linux-x86_64-gnu".to_string(), record)]),
                },
            )]),
        };

        let error =
            verify_lockfile_records(&temp_dir.path().join("binpm.lock"), lockfile, None, false)
                .expect_err("missing digest evidence is rejected");

        assert!(error
            .to_string()
            .contains("Provider digest evidence does not match"));
    }

    #[test]
    fn local_runtime_locks_require_current_target_record() {
        let mut manifest = Manifest {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                ManifestTool {
                    source: "github:owner/tool".to_string(),
                    version: Some("1.0.0".to_string()),
                    bin: None,
                    targets: BTreeMap::new(),
                },
            )]),
        };
        let mut darwin_record = package_record();
        darwin_record.target_os = TargetOs::Darwin;
        darwin_record.target_libc = TargetLibc::Any;
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                LockTool {
                    source: "github:owner/tool".to_string(),
                    targets: BTreeMap::from([("darwin-x86_64-any".to_string(), darwin_record)]),
                },
            )]),
        };

        let error = local_runtime_lock_records(&manifest, &lockfile, &linux_target())
            .expect_err("missing current target is stale");

        assert!(error.to_string().contains("stale"));

        manifest.tools.clear();
        assert!(
            local_runtime_lock_records(&manifest, &lockfile, &linux_target())
                .expect("no manifest tools to verify")
                .is_empty()
        );
    }

    #[test]
    fn local_verify_rejects_missing_runtime_records_for_current_locks() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let remaining_locks = BTreeMap::from([("tool".to_string(), package_record())]);

        let error = assert_local_runtime_records_complete(temp_dir.path(), &remaining_locks)
            .expect_err("missing runtime record is stale");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn rollback_capture_ignores_unmanaged_installed_path() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let mut record = package_record();
        record.installed_path = "/dev/zero".to_string();
        write_package_record(&paths, "tool", &record).expect("write package record");

        let state = capture_runtime_tool_state(&paths, "tool").expect("capture runtime state");

        assert!(state.package_record.is_some());
        assert!(state.installed_snapshot.is_none());
    }

    #[test]
    fn rollback_capture_preserves_unrecorded_managed_installed_path() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let installed_path = paths.bin.join("tool");
        fs::write(&installed_path, b"stale tool").expect("write stale executable");

        let state = capture_runtime_tool_state(&paths, "tool").expect("capture runtime state");
        fs::remove_file(&installed_path).expect("remove stale executable");
        restore_runtime_tool_state(&paths, "tool", state);

        assert_eq!(
            fs::read(&installed_path).expect("restored stale executable"),
            b"stale tool"
        );
    }

    #[cfg(unix)]
    #[test]
    fn rollback_capture_preserves_unrecorded_managed_symlink() {
        use std::os::unix::fs::symlink;

        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let target = temp_dir.path().join("target-tool");
        fs::write(&target, b"target tool").expect("write target executable");
        let installed_path = paths.bin.join("tool");
        symlink(&target, &installed_path).expect("symlink stale executable");

        let state = capture_runtime_tool_state(&paths, "tool").expect("capture runtime state");
        fs::remove_file(&installed_path).expect("remove stale symlink");
        restore_runtime_tool_state(&paths, "tool", state);

        assert_eq!(
            fs::read_link(&installed_path).expect("restored stale symlink"),
            target
        );
    }

    #[test]
    fn rollback_does_not_recreate_missing_recorded_installed_path_from_cache() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let installed_path = paths.bin.join("tool");
        let cache_path = temp_dir.path().join("cache-tool");
        fs::write(&cache_path, b"cached tool").expect("write cache");
        let mut record = package_record();
        record.installed_path = installed_path.display().to_string();
        record.cache_path = Some(cache_path.display().to_string());
        let state = RuntimeToolState {
            package_record: Some(record),
            installed_path: Some(installed_path.clone()),
            installed_snapshot: None,
        };

        restore_runtime_tool_state(&paths, "tool", state);

        assert!(!installed_path.exists());
        assert!(paths.packages.join("tool.toml").exists());
    }

    #[test]
    fn rollback_capture_rejects_unreadable_managed_installed_path() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let mut record = package_record();
        let installed_path = paths.bin.join("tool");
        record.installed_path = installed_path.display().to_string();
        write_package_record(&paths, "tool", &record).expect("write package record");
        fs::create_dir(&installed_path).expect("create unreadable-as-file path");

        let error =
            capture_runtime_tool_state(&paths, "tool").expect_err("managed read error is fatal");

        assert!(error.to_string().contains("Failed to read"));
    }

    #[test]
    fn local_remove_missing_manifest_tool_detects_stale_runtime_state() {
        let mut record = package_record();
        record.installed_path = ".binpm/bin/tool".to_string();
        let state = LocalRemoveState {
            manifest: Manifest {
                version: 1,
                tools: BTreeMap::new(),
            },
            lockfile_existed: false,
            lockfile: Lockfile {
                version: 1,
                tools: BTreeMap::new(),
            },
            runtime: RuntimeToolState {
                package_record: Some(record),
                installed_path: None,
                installed_snapshot: None,
            },
        };

        assert!(has_local_runtime_or_lock_state("tool", &state));
    }

    #[test]
    fn local_remove_missing_manifest_tool_detects_stale_lock_state() {
        let record = package_record();
        let state = LocalRemoveState {
            manifest: Manifest {
                version: 1,
                tools: BTreeMap::new(),
            },
            lockfile_existed: true,
            lockfile: Lockfile {
                version: 1,
                tools: BTreeMap::from([(
                    "tool".to_string(),
                    LockTool {
                        source: "github:owner/tool".to_string(),
                        targets: BTreeMap::from([("linux-x86_64-gnu".to_string(), record)]),
                    },
                )]),
            },
            runtime: RuntimeToolState {
                package_record: None,
                installed_path: None,
                installed_snapshot: None,
            },
        };

        assert!(has_local_runtime_or_lock_state("tool", &state));
    }

    #[test]
    fn local_remove_missing_manifest_tool_ignores_manual_bin_file() {
        let state = LocalRemoveState {
            manifest: Manifest {
                version: 1,
                tools: BTreeMap::new(),
            },
            lockfile_existed: false,
            lockfile: Lockfile {
                version: 1,
                tools: BTreeMap::new(),
            },
            runtime: RuntimeToolState {
                package_record: None,
                installed_path: Some(PathBuf::from(".binpm/bin/tool")),
                installed_snapshot: Some(InstalledPathSnapshot::RegularFile {
                    bytes: b"manual tool".to_vec(),
                    #[cfg(unix)]
                    mode: 0o700,
                }),
            },
        };

        assert!(!has_local_runtime_or_lock_state("tool", &state));
    }

    #[test]
    fn manifest_sync_removes_local_package_and_lock_orphans() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let mut record = package_record();
        let installed_path = paths.bin.join("tool");
        record.installed_path = installed_path.display().to_string();
        write_package_record(&paths, "tool", &record).expect("write package record");
        fs::write(&installed_path, b"old tool").expect("write installed binary");
        write_lockfile(
            &temp_dir.path().join(LOCKFILE_FILE),
            &Lockfile {
                version: 1,
                tools: BTreeMap::from([(
                    "tool".to_string(),
                    LockTool {
                        source: "github:owner/tool".to_string(),
                        targets: BTreeMap::from([("linux-x86_64-gnu".to_string(), record)]),
                    },
                )]),
            },
        )
        .expect("write lockfile");

        remove_local_manifest_orphans(temp_dir.path(), &BTreeMap::new(), false)
            .expect("remove orphans");

        assert!(!paths.packages.join("tool.toml").exists());
        assert!(!installed_path.exists());
        let lockfile = crate::storage::read_lockfile(&temp_dir.path().join(LOCKFILE_FILE))
            .expect("read lockfile");
        assert!(lockfile.tools.is_empty());
    }

    #[test]
    fn manifest_sync_preserves_manual_binary_for_lock_only_orphan() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let manual_path = paths.bin.join("tool");
        fs::write(&manual_path, b"manual tool").expect("write manual binary");
        write_lockfile(
            &temp_dir.path().join(LOCKFILE_FILE),
            &Lockfile {
                version: 1,
                tools: BTreeMap::from([(
                    "tool".to_string(),
                    LockTool {
                        source: "github:owner/tool".to_string(),
                        targets: BTreeMap::from([(
                            "linux-x86_64-gnu".to_string(),
                            package_record(),
                        )]),
                    },
                )]),
            },
        )
        .expect("write lockfile");

        remove_local_manifest_orphans(temp_dir.path(), &BTreeMap::new(), false)
            .expect("remove lock orphan");

        assert_eq!(
            fs::read(&manual_path).expect("manual binary remains"),
            b"manual tool"
        );
        let lockfile = crate::storage::read_lockfile(&temp_dir.path().join(LOCKFILE_FILE))
            .expect("read lockfile");
        assert!(lockfile.tools.is_empty());
    }

    #[test]
    fn manifest_sync_rejects_invalid_lock_orphan_name_before_runtime_capture() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let victim = temp_dir.path().join("victim.toml");
        fs::write(&victim, "do not read as package record").expect("write victim");
        write_lockfile(
            &temp_dir.path().join(LOCKFILE_FILE),
            &Lockfile {
                version: 1,
                tools: BTreeMap::from([(
                    "../../victim".to_string(),
                    LockTool {
                        source: "github:owner/tool".to_string(),
                        targets: BTreeMap::from([(
                            "linux-x86_64-gnu".to_string(),
                            package_record(),
                        )]),
                    },
                )]),
            },
        )
        .expect("write lockfile");

        let error = remove_local_manifest_orphans(temp_dir.path(), &BTreeMap::new(), false)
            .expect_err("invalid orphan command");

        assert!(error.to_string().contains("Invalid command name"));
        assert_eq!(
            fs::read_to_string(&victim).expect("read victim"),
            "do not read as package record"
        );
    }

    #[test]
    fn frozen_manifest_sync_rejects_lock_orphans_without_removing_them() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let mut record = package_record();
        mark_github_verified(&mut record);
        write_lockfile(
            &temp_dir.path().join(LOCKFILE_FILE),
            &Lockfile {
                version: 1,
                tools: BTreeMap::from([(
                    "tool".to_string(),
                    LockTool {
                        source: "github:owner/tool".to_string(),
                        targets: BTreeMap::from([("linux-x86_64-gnu".to_string(), record)]),
                    },
                )]),
            },
        )
        .expect("write lockfile");

        let error = remove_local_manifest_orphans(temp_dir.path(), &BTreeMap::new(), true)
            .expect_err("frozen orphan cleanup is rejected");

        assert!(error.to_string().contains("Frozen lockfile"));
        let lockfile = crate::storage::read_lockfile(&temp_dir.path().join(LOCKFILE_FILE))
            .expect("read lockfile");
        assert!(lockfile.tools.contains_key("tool"));
    }

    #[test]
    fn manifest_sync_keeps_declared_windows_exe_collision_path() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let mut record = package_record();
        record.target_os = TargetOs::Windows;
        record.installed_path = paths.bin.join("foo.exe").display().to_string();
        write_package_record(&paths, "foo", &record).expect("write package record");
        fs::write(paths.bin.join("foo.exe"), b"declared tool").expect("write installed binary");
        write_lockfile(
            &temp_dir.path().join(LOCKFILE_FILE),
            &Lockfile {
                version: 1,
                tools: BTreeMap::from([(
                    "foo".to_string(),
                    LockTool {
                        source: "github:owner/tool".to_string(),
                        targets: BTreeMap::from([("windows-x86_64-any".to_string(), record)]),
                    },
                )]),
            },
        )
        .expect("write lockfile");
        let manifest_tools = BTreeMap::from([(
            "foo.exe".to_string(),
            ManifestTool {
                source: "github:owner/tool".to_string(),
                version: Some("1.0.0".to_string()),
                bin: None,
                targets: BTreeMap::new(),
            },
        )]);

        remove_local_manifest_orphans(temp_dir.path(), &manifest_tools, false)
            .expect("remove colliding orphan");

        assert!(!paths.packages.join("foo.toml").exists());
        assert_eq!(
            fs::read(paths.bin.join("foo.exe")).expect("declared executable remains"),
            b"declared tool"
        );
        let lockfile = crate::storage::read_lockfile(&temp_dir.path().join(LOCKFILE_FILE))
            .expect("read lockfile");
        assert!(!lockfile.tools.contains_key("foo"));
    }

    #[test]
    fn manifest_sync_keeps_declared_darwin_case_collision_path() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let mut record = package_record();
        record.target_os = TargetOs::Darwin;
        record.installed_path = paths.bin.join("foo").display().to_string();
        write_package_record(&paths, "foo", &record).expect("write package record");
        fs::write(paths.bin.join("foo"), b"declared tool").expect("write installed binary");
        write_lockfile(
            &temp_dir.path().join(LOCKFILE_FILE),
            &Lockfile {
                version: 1,
                tools: BTreeMap::from([(
                    "foo".to_string(),
                    LockTool {
                        source: "github:owner/tool".to_string(),
                        targets: BTreeMap::from([("darwin-x86_64-any".to_string(), record)]),
                    },
                )]),
            },
        )
        .expect("write lockfile");
        let manifest_tools = BTreeMap::from([(
            "FOO".to_string(),
            ManifestTool {
                source: "github:owner/tool".to_string(),
                version: Some("1.0.0".to_string()),
                bin: None,
                targets: BTreeMap::new(),
            },
        )]);

        remove_local_manifest_orphans(temp_dir.path(), &manifest_tools, false)
            .expect("remove case-colliding orphan");

        assert!(!paths.packages.join("foo.toml").exists());
        assert_eq!(
            fs::read(paths.bin.join("foo")).expect("declared executable remains"),
            b"declared tool"
        );
        let lockfile = crate::storage::read_lockfile(&temp_dir.path().join(LOCKFILE_FILE))
            .expect("read lockfile");
        assert!(!lockfile.tools.contains_key("foo"));
    }

    #[cfg(unix)]
    #[test]
    fn manifest_sync_restores_orphan_runtime_when_lockfile_rewrite_fails() {
        use std::os::unix::fs::PermissionsExt;

        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let mut record = package_record();
        let installed_path = paths.bin.join("tool");
        record.installed_path = installed_path.display().to_string();
        write_package_record(&paths, "tool", &record).expect("write package record");
        fs::write(&installed_path, b"old tool").expect("write installed binary");
        write_lockfile(
            &temp_dir.path().join(LOCKFILE_FILE),
            &Lockfile {
                version: 1,
                tools: BTreeMap::from([(
                    "tool".to_string(),
                    LockTool {
                        source: "github:owner/tool".to_string(),
                        targets: BTreeMap::from([("linux-x86_64-gnu".to_string(), record)]),
                    },
                )]),
            },
        )
        .expect("write lockfile");
        let original_permissions = fs::metadata(temp_dir.path())
            .expect("metadata")
            .permissions();
        let mut read_only = original_permissions.clone();
        read_only.set_mode(0o555);
        fs::set_permissions(temp_dir.path(), read_only).expect("make root read-only");

        let error = remove_local_manifest_orphans(temp_dir.path(), &BTreeMap::new(), false)
            .expect_err("lockfile rewrite fails");

        fs::set_permissions(temp_dir.path(), original_permissions).expect("restore permissions");
        assert!(error.to_string().contains("Failed to write"));
        assert!(paths.packages.join("tool.toml").exists());
        assert_eq!(
            fs::read(&installed_path).expect("installed binary restored"),
            b"old tool"
        );
    }

    #[test]
    fn lockfile_verify_rejects_record_under_mismatched_target_key() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let mut record = package_record();
        record.checksum_source = ChecksumSource::GitHubDigest;
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                LockTool {
                    source: "github:owner/tool".to_string(),
                    targets: BTreeMap::from([("darwin-x86_64-any".to_string(), record)]),
                },
            )]),
        };

        let error =
            verify_lockfile_records(&temp_dir.path().join("binpm.lock"), lockfile, None, true)
                .expect_err("mismatched target is stale");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn local_lockfile_verify_requires_manifest_tools() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let manifest = Manifest {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                ManifestTool {
                    source: "github:owner/tool".to_string(),
                    version: Some("1.0.0".to_string()),
                    bin: None,
                    targets: BTreeMap::new(),
                },
            )]),
        };
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::new(),
        };

        let error = verify_lockfile_records(
            &temp_dir.path().join("binpm.lock"),
            lockfile,
            Some((&manifest, temp_dir.path())),
            true,
        )
        .expect_err("manifest tool must be locked");

        assert!(error.to_string().contains("Frozen lockfile"));
    }

    #[test]
    fn local_lockfile_verify_rejects_tools_absent_from_manifest() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let manifest = Manifest {
            version: 1,
            tools: BTreeMap::new(),
        };
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                LockTool {
                    source: "github:owner/tool".to_string(),
                    targets: BTreeMap::from([("linux-x86_64-gnu".to_string(), package_record())]),
                },
            )]),
        };

        let error = verify_lockfile_records(
            &temp_dir.path().join("binpm.lock"),
            lockfile,
            Some((&manifest, temp_dir.path())),
            true,
        )
        .expect_err("lock-only tool is stale");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn github_digest_parser_accepts_only_sha256_digests() {
        assert_eq!(
            github_sha256_digest(Some(
                "sha256:ABCDEFabcdef0123456789abcdef0123456789abcdef0123456789abcdef0123"
            ))
            .as_deref(),
            Some("abcdefabcdef0123456789abcdef0123456789abcdef0123456789abcdef0123")
        );
        assert_eq!(github_sha256_digest(Some("md5:abc")), None);
        assert_eq!(github_sha256_digest(Some("sha256:not-hex")), None);
        assert_eq!(github_sha256_digest(None), None);
    }

    #[test]
    fn manifest_target_override_checksum_source_is_not_verification_evidence() {
        let target = linux_target();
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: None,
            targets: BTreeMap::from([(
                target.key(),
                ManifestTargetOverride {
                    asset: "tool-linux".to_string(),
                    bin: "tool".to_string(),
                    checksum_source: Some(ChecksumSource::Manifest),
                },
            )]),
        };

        let error = manifest_checksum_source(Some(&tool), &target, None)
            .expect_err("unverified checksum source override");

        assert!(error.to_string().contains("cannot be used"));
        assert_eq!(
            manifest_checksum_source(None, &target, None).expect("default checksum source"),
            ChecksumSource::Local
        );
    }

    #[test]
    fn manifest_target_override_accepts_github_digest_with_provider_evidence() {
        let target = linux_target();
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: None,
            targets: BTreeMap::from([(
                target.key(),
                ManifestTargetOverride {
                    asset: "tool-linux".to_string(),
                    bin: "tool".to_string(),
                    checksum_source: Some(ChecksumSource::GitHubDigest),
                },
            )]),
        };

        assert_eq!(
            manifest_checksum_source(Some(&tool), &target, Some(&package_record().sha256))
                .expect("github digest override"),
            ChecksumSource::GitHubDigest
        );
        assert!(manifest_checksum_source(Some(&tool), &target, None).is_err());
    }

    #[test]
    fn frozen_lock_accepts_github_digest_override_with_matching_provider_evidence() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: None,
            targets: BTreeMap::from([(
                target.key(),
                ManifestTargetOverride {
                    asset: "tool-linux".to_string(),
                    bin: "tool-linux".to_string(),
                    checksum_source: Some(ChecksumSource::GitHubDigest),
                },
            )]),
        };
        let mut record = package_record();
        record.checksum_source = ChecksumSource::GitHubDigest;
        record.provider_digest_sha256 = Some(record.sha256.clone());

        assert_lock_matches_manifest_tool(temp_dir.path(), "tool", Some(&tool), &target, &record)
            .expect("github digest override with provider evidence");
    }

    #[test]
    fn manifest_target_override_keys_must_be_canonical() {
        let target = linux_target();
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: None,
            targets: BTreeMap::from([(
                "linux-amd64-glibc".to_string(),
                ManifestTargetOverride {
                    asset: "tool-linux".to_string(),
                    bin: "tool".to_string(),
                    checksum_source: None,
                },
            )]),
        };

        let error =
            manifest_target_override(Some(&tool), &target).expect_err("target aliases rejected");

        assert!(error.to_string().contains("Invalid target key"));

        let invalid_tool = ManifestTool {
            targets: BTreeMap::from([(
                "linux-amd64-surprise".to_string(),
                ManifestTargetOverride {
                    asset: "tool-linux".to_string(),
                    bin: "tool".to_string(),
                    checksum_source: None,
                },
            )]),
            ..tool
        };

        assert!(manifest_target_override(Some(&invalid_tool), &target).is_err());
    }

    #[test]
    fn local_runtime_records_must_match_current_lock_record() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let lock_record = package_record();
        let manifest = Manifest {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                ManifestTool {
                    source: "github:owner/tool".to_string(),
                    version: Some("1.0.0".to_string()),
                    bin: None,
                    targets: BTreeMap::new(),
                },
            )]),
        };
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                LockTool {
                    source: "github:owner/tool".to_string(),
                    targets: BTreeMap::from([(target.key(), lock_record.clone())]),
                },
            )]),
        };
        let runtime_locks =
            local_runtime_lock_records(&manifest, &lockfile, &target).expect("runtime locks");
        let mut stale_runtime = lock_record.clone();
        stale_runtime.sha256 = "def456".to_string();

        let error = assert_runtime_record_matches_lock(
            temp_dir.path(),
            "tool",
            &runtime_locks["tool"],
            &stale_runtime,
        )
        .expect_err("stale runtime record");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn frozen_lock_rejects_mismatched_embedded_source() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let mut record = package_record();
        record.source_path = "attacker/tool".to_string();

        let error = assert_lock_record_matches_source_and_target(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &spec,
            &target,
            &record,
        )
        .expect_err("stale lockfile");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn frozen_lock_rejects_mismatched_package_spec() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let mut record = package_record();
        record.package_spec = "github:attacker/tool@1.0.0".to_string();

        let error = assert_lock_record_matches_source_and_target(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &spec,
            &target,
            &record,
        )
        .expect_err("stale lockfile");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn frozen_lock_rejects_mismatched_embedded_target() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let mut record = package_record();
        record.target_arch = TargetArch::Aarch64;

        let error = assert_lock_record_matches_source_and_target(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &spec,
            &target,
            &record,
        )
        .expect_err("stale lockfile");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn rollback_skips_restoring_unmanaged_installed_path() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = crate::storage::ScopePaths::local(temp_dir.path().to_path_buf());
        let outside = temp_dir.path().join("outside-tool");
        std::fs::write(&outside, "original").expect("write outside file");
        let mut record = package_record();
        record.installed_path = outside.display().to_string();

        restore_runtime_tool_state(
            &paths,
            "tool",
            RuntimeToolState {
                package_record: Some(record),
                installed_path: Some(outside.clone()),
                installed_snapshot: Some(InstalledPathSnapshot::RegularFile {
                    bytes: b"changed".to_vec(),
                    #[cfg(unix)]
                    mode: 0o755,
                }),
            },
        );

        assert_eq!(
            std::fs::read_to_string(&outside).expect("read outside file"),
            "original"
        );
        let restored = crate::storage::read_package_record(&crate::storage::package_record_path(
            &paths, "tool",
        ))
        .expect("restored package record");
        assert_eq!(restored.installed_path, outside.display().to_string());
    }

    #[cfg(unix)]
    #[test]
    fn rollback_restores_regular_file_mode() {
        use std::os::unix::fs::PermissionsExt;

        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = crate::storage::ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let installed_path = paths.bin.join("tool");
        std::fs::write(&installed_path, "replacement").expect("write replacement file");
        let mut record = package_record();
        record.installed_path = installed_path.display().to_string();

        restore_runtime_tool_state(
            &paths,
            "tool",
            RuntimeToolState {
                package_record: Some(record),
                installed_path: Some(installed_path.clone()),
                installed_snapshot: Some(InstalledPathSnapshot::RegularFile {
                    bytes: b"original".to_vec(),
                    mode: 0o750,
                }),
            },
        );

        assert_eq!(
            std::fs::read_to_string(&installed_path).expect("read restored file"),
            "original"
        );
        assert_eq!(
            std::fs::metadata(&installed_path)
                .expect("restored metadata")
                .permissions()
                .mode()
                & 0o777,
            0o750
        );
    }

    #[test]
    fn global_remove_rejects_unsafe_installed_path_and_preserves_state() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = crate::storage::ScopePaths::global(temp_dir.path().join("home"));
        let outside = temp_dir.path().join("outside-tool");
        std::fs::write(&outside, "original").expect("write outside file");
        let mut record = package_record();
        record.installed_path = outside.display().to_string();
        write_package_record(&paths, "tool", &record).expect("write package record");
        std::fs::write(paths.bin.join("tool"), "shim").expect("write bin candidate");

        let error = remove_global_tool_from_paths(&paths, "tool").expect_err("unsafe path");

        assert!(error.to_string().contains("Unsafe installed path"));
        assert_eq!(
            std::fs::read_to_string(&outside).expect("read outside file"),
            "original"
        );
        assert!(crate::storage::package_record_path(&paths, "tool").exists());
        assert_eq!(
            std::fs::read_to_string(paths.bin.join("tool")).expect("read bin candidate"),
            "shim"
        );
    }

    #[test]
    fn global_remove_preserves_exe_sibling_for_linux_tool() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = crate::storage::ScopePaths::global(temp_dir.path().join("home"));
        let mut record = package_record();
        record.installed_path = paths.bin.join("tool").display().to_string();
        write_package_record(&paths, "tool", &record).expect("write package record");
        std::fs::write(paths.bin.join("tool"), "linux tool").expect("write linux tool");
        std::fs::write(paths.bin.join("tool.exe"), "sibling tool").expect("write exe sibling");

        remove_global_tool_from_paths(&paths, "tool").expect("remove global tool");

        assert!(!crate::storage::package_record_path(&paths, "tool").exists());
        assert!(!paths.bin.join("tool").exists());
        assert_eq!(
            std::fs::read_to_string(paths.bin.join("tool.exe")).expect("read exe sibling"),
            "sibling tool"
        );
    }

    #[test]
    fn global_remove_preserves_windows_exe_path_owned_by_remaining_record() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = crate::storage::ScopePaths::global(temp_dir.path().join("home"));
        let mut removed = package_record();
        removed.target_os = TargetOs::Windows;
        removed.installed_path = paths.bin.join("tool.exe").display().to_string();
        let mut remaining = package_record();
        remaining.target_os = TargetOs::Windows;
        remaining.installed_path = paths.bin.join("tool.exe").display().to_string();
        write_package_record(&paths, "tool", &removed).expect("write removed record");
        write_package_record(&paths, "tool.exe", &remaining).expect("write remaining record");
        std::fs::write(paths.bin.join("tool.exe"), "remaining tool").expect("write exe");

        remove_global_tool_from_paths(&paths, "tool").expect("remove global tool");

        assert!(!crate::storage::package_record_path(&paths, "tool").exists());
        assert!(crate::storage::package_record_path(&paths, "tool.exe").exists());
        assert_eq!(
            std::fs::read_to_string(paths.bin.join("tool.exe")).expect("read exe"),
            "remaining tool"
        );
    }

    #[test]
    fn global_remove_preserves_darwin_case_insensitive_path_owned_by_remaining_record() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = crate::storage::ScopePaths::global(temp_dir.path().join("home"));
        let mut removed = package_record();
        removed.target_os = TargetOs::Darwin;
        removed.installed_path = paths.bin.join("foo").display().to_string();
        let mut remaining = package_record();
        remaining.target_os = TargetOs::Darwin;
        remaining.installed_path = paths.bin.join("FOO").display().to_string();
        write_package_record(&paths, "foo", &removed).expect("write removed record");
        write_package_record(&paths, "FOO", &remaining).expect("write remaining record");
        std::fs::write(paths.bin.join("foo"), "remaining tool").expect("write darwin tool");

        remove_global_tool_from_paths(&paths, "foo").expect("remove global tool");

        assert!(!crate::storage::package_record_path(&paths, "foo").exists());
        assert!(crate::storage::package_record_path(&paths, "FOO").exists());
        assert_eq!(
            std::fs::read_to_string(paths.bin.join("foo")).expect("read darwin tool"),
            "remaining tool"
        );
    }

    #[test]
    fn global_remove_requires_package_record_before_deleting_binary() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = crate::storage::ScopePaths::global(temp_dir.path().join("home"));
        paths.ensure().expect("create paths");
        std::fs::write(paths.bin.join("tool"), "manual tool").expect("write manual binary");

        let error =
            remove_global_tool_from_paths(&paths, "tool").expect_err("missing package record");

        assert!(error.to_string().contains("Failed to read"));
        assert_eq!(
            std::fs::read_to_string(paths.bin.join("tool")).expect("read manual binary"),
            "manual tool"
        );
    }

    #[test]
    fn manifest_gitlab_overrides_reject_non_https_redirects() {
        let target = linux_target();
        let spec = SourceSpec {
            provider: SourceProvider::GitLab,
            host: "gitlab.com".to_string(),
            path: "owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
        };
        let tool = ManifestTool {
            source: "gitlab:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: None,
            targets: BTreeMap::from([(
                target.key(),
                ManifestTargetOverride {
                    asset: "tool-linux".to_string(),
                    bin: "tool".to_string(),
                    checksum_source: None,
                },
            )]),
        };
        let assets = [ReleaseAsset {
            name: "tool-linux".to_string(),
            url: "https://gitlab.com/owner/tool/-/releases/v1/downloads/tool-linux?token=secret"
                .to_string(),
            provider_url: Some(
                "https://gitlab.com/owner/tool/-/releases/v1/downloads/tool-linux?token=secret"
                    .to_string(),
            ),
            digest: None,
            source_archive: false,
            final_url_https: Some(false),
        }];

        let error =
            select_manifest_asset(&spec, Some(&tool), &target, &assets).expect_err("unsafe URL");

        assert!(error.to_string().contains("not HTTPS eligible"));
        assert!(!error.to_string().contains("secret"));
    }

    #[test]
    fn automatic_asset_selection_honors_scored_release_selection() {
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let assets = [
            ReleaseAsset {
                name: "tool-x86_64-unknown-linux-gnu.tar.gz".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool.tar.gz"
                    .to_string(),
                provider_url: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            },
            ReleaseAsset {
                name: "tool-linux-x64".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64"
                    .to_string(),
                provider_url: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            },
        ];

        let selected =
            select_manifest_asset(&spec, None, &target, &assets).expect("selected asset");

        assert_eq!(selected.asset_name, "tool-x86_64-unknown-linux-gnu.tar.gz");
        assert!(matches!(selected.kind, ArtifactKind::Archive(_)));
    }

    #[test]
    fn manifest_bin_does_not_override_scored_asset_selection() {
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: Some("rg".to_string()),
            targets: BTreeMap::new(),
        };
        let assets = [
            ReleaseAsset {
                name: "tool-x86_64-unknown-linux-gnu.tar.gz".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool.tar.gz"
                    .to_string(),
                provider_url: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            },
            ReleaseAsset {
                name: "tool-linux-x64".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64"
                    .to_string(),
                provider_url: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            },
        ];

        let selected =
            select_manifest_asset(&spec, Some(&tool), &target, &assets).expect("selected asset");

        assert_eq!(selected.asset_name, "tool-x86_64-unknown-linux-gnu.tar.gz");
        assert!(matches!(selected.kind, ArtifactKind::Archive(_)));
    }

    #[test]
    fn manifest_bin_does_not_require_matching_bare_executable_asset() {
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: Some("rg".to_string()),
            targets: BTreeMap::new(),
        };
        let assets = [ReleaseAsset {
            name: "tool-linux-x64".to_string(),
            url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64".to_string(),
            provider_url: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
        }];

        let selected =
            select_manifest_asset(&spec, Some(&tool), &target, &assets).expect("selected asset");

        assert_eq!(selected.asset_name, "tool-linux-x64");
    }

    #[test]
    fn manifest_asset_selection_falls_back_to_archive_when_no_bare_executable_matches() {
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let assets = [ReleaseAsset {
            name: "tool-x86_64-unknown-linux-gnu.tar.gz".to_string(),
            url: "https://github.com/owner/tool/releases/download/1.0.0/tool.tar.gz".to_string(),
            provider_url: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
        }];

        let selected = select_manifest_asset(&spec, None, &target, &assets)
            .expect("archive fallback selection");

        assert_eq!(selected.asset_name, "tool-x86_64-unknown-linux-gnu.tar.gz");
        assert!(matches!(selected.kind, ArtifactKind::Archive(_)));
    }

    #[test]
    fn explain_selection_reports_scored_selection() {
        let target = linux_target();
        let assets = [
            ReleaseAsset {
                name: "tool-x86_64-unknown-linux-gnu.tar.gz".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool.tar.gz"
                    .to_string(),
                provider_url: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            },
            ReleaseAsset {
                name: "tool-linux-x64".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64"
                    .to_string(),
                provider_url: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            },
        ];
        let selection =
            crate::assets::select_asset(SourceProvider::GitHub, &target, &assets).expect("asset");

        assert_eq!(
            selection.selected.asset_name,
            "tool-x86_64-unknown-linux-gnu.tar.gz"
        );
        assert!(matches!(selection.selected.kind, ArtifactKind::Archive(_)));
    }

    #[test]
    fn explain_selected_asset_url_rejects_credentials_without_echoing_them() {
        let decision = CandidateDecision {
            asset_name: "tool".to_string(),
            canonical_url: "https://token@example.com/tool".to_string(),
            download_url: "https://token@example.com/tool".to_string(),
            kind: ArtifactKind::BareExecutable,
            detected_os: Some(TargetOs::Linux),
            detected_arch: Some(TargetArch::X86_64),
            detected_libc: Some(TargetLibc::Gnu),
            score: Some(1),
            eligible: true,
            recognized_pattern: true,
            rejection_reason: None,
        };

        let error = selected_asset_display_url(&decision).expect_err("credential URL rejected");

        assert!(error.to_string().contains("credentials"));
        assert!(!error.to_string().contains("token"));
    }

    #[test]
    fn frozen_lock_rejects_path_like_sha_before_cache_paths() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let mut record = package_record();
        record.sha256 = "../outside".to_string();

        let error = assert_lock_record_matches_source_and_target(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &spec,
            &target,
            &record,
        )
        .expect_err("invalid digest");

        assert!(error.to_string().contains("Invalid SHA-256"));
    }

    #[test]
    fn frozen_lock_rejects_reclassified_asset_format_mismatch() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let mut record = package_record();
        record.asset_name = "tool-x86_64-unknown-linux-gnu.tar.gz".to_string();

        let error = validate_locked_record_artifact(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &record,
            &target,
            None,
        )
        .expect_err("locked format mismatch rejected");

        assert!(error.to_string().contains("Frozen lockfile"));
    }

    #[test]
    fn frozen_lock_rejects_asset_names_for_another_target_without_override() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let mut record = package_record();
        record.asset_name = "tool-windows-x64.exe".to_string();

        let error = validate_locked_record_artifact(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &record,
            &target,
            None,
        )
        .expect_err("target-mismatched asset rejected");

        assert!(error.to_string().contains("No installable asset"));
    }

    #[test]
    fn frozen_lock_allows_explicit_target_override_asset_names() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let mut record = package_record();
        record.asset_name = "custom-binary".to_string();
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: None,
            targets: BTreeMap::from([(
                target.key(),
                ManifestTargetOverride {
                    asset: "custom-binary".to_string(),
                    bin: "custom-binary".to_string(),
                    checksum_source: None,
                },
            )]),
        };

        validate_locked_record_artifact(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &record,
            &target,
            Some(&tool),
        )
        .expect("explicit override asset is accepted");
    }

    #[test]
    fn frozen_lock_preserves_locked_asset_when_better_release_asset_appears() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let record = package_record();
        let assets = [
            ReleaseAsset {
                name: record.asset_name.clone(),
                url: record.asset_url.clone(),
                provider_url: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            },
            ReleaseAsset {
                name: "tool-x86_64-unknown-linux-gnu".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool-x86_64-unknown-linux-gnu"
                    .to_string(),
                provider_url: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            },
        ];
        let selected =
            select_manifest_asset(&spec, None, &target, &assets).expect("best current asset");

        assert_ne!(selected.asset_name, record.asset_name);
        validate_locked_record_current_asset(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &record,
            &assets,
        )
        .expect("locked asset remains valid while present with the same URL");
    }

    #[test]
    fn frozen_lock_rejects_locked_asset_with_changed_current_url() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let record = package_record();
        let assets = [ReleaseAsset {
            name: record.asset_name.clone(),
            url: "https://github.com/owner/tool/releases/download/1.0.0/renamed-tool-linux"
                .to_string(),
            provider_url: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
        }];

        let error = validate_locked_record_current_asset(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &record,
            &assets,
        )
        .expect_err("changed locked asset URL rejected");

        assert!(matches!(error, BinpmError::StaleLockfile { .. }));
    }

    #[test]
    fn frozen_lock_rejects_changed_github_provider_digest() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let mut record = package_record();
        mark_github_verified(&mut record);
        let changed_digest = "1111111111111111111111111111111111111111111111111111111111111111";
        let assets = [ReleaseAsset {
            name: record.asset_name.clone(),
            url: record.asset_url.clone(),
            provider_url: None,
            digest: Some(format!("sha256:{changed_digest}")),
            source_archive: false,
            final_url_https: None,
        }];

        let error = validate_locked_record_current_provider_digest(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &record,
            &assets,
        )
        .expect_err("changed digest rejected");

        assert!(matches!(error, BinpmError::StaleLockfile { .. }));
    }

    #[test]
    fn frozen_lock_rejects_local_record_when_current_provider_digest_differs() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let record = package_record();
        let changed_digest = "1111111111111111111111111111111111111111111111111111111111111111";
        let assets = [ReleaseAsset {
            name: record.asset_name.clone(),
            url: record.asset_url.clone(),
            provider_url: None,
            digest: Some(format!("sha256:{changed_digest}")),
            source_archive: false,
            final_url_https: None,
        }];

        let error = validate_locked_record_current_provider_digest(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &record,
            &assets,
        )
        .expect_err("current provider digest must be strongest evidence");

        assert!(matches!(error, BinpmError::StaleLockfile { .. }));
    }

    #[test]
    fn package_record_provider_digest_requires_matching_current_asset_digest() {
        let mut record = package_record();
        mark_github_verified(&mut record);
        let assets = [ReleaseAsset {
            name: record.asset_name.clone(),
            url: record.asset_url.clone(),
            provider_url: None,
            digest: Some(format!("sha256:{}", record.sha256)),
            source_archive: false,
            final_url_https: None,
        }];

        assert!(record_matches_current_provider_digest(&record, &assets));
    }

    #[test]
    fn package_record_provider_digest_rejects_missing_current_asset_digest() {
        let mut record = package_record();
        mark_github_verified(&mut record);
        let assets = [ReleaseAsset {
            name: record.asset_name.clone(),
            url: record.asset_url.clone(),
            provider_url: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
        }];

        assert!(!record_matches_current_provider_digest(&record, &assets));
    }

    #[test]
    fn package_record_local_checksum_accepts_matching_current_provider_digest() {
        let record = package_record();
        let assets = [ReleaseAsset {
            name: record.asset_name.clone(),
            url: record.asset_url.clone(),
            provider_url: None,
            digest: Some(format!("sha256:{}", record.sha256)),
            source_archive: false,
            final_url_https: None,
        }];

        assert!(record_matches_current_provider_digest(&record, &assets));
    }

    #[test]
    fn locked_release_lookup_uses_record_release_tag_for_versionless_sources() {
        let mut record = package_record();
        record.requested_version = None;
        record.package_spec = "github:owner/tool@1.0.0".to_string();
        record.release_tag = "1.0.0".to_string();

        let spec = locked_release_lookup_spec(&record).expect("lookup spec");

        assert_eq!(spec.version.as_deref(), Some("1.0.0"));
    }

    #[test]
    fn frozen_lock_local_validation_honors_explicit_target_override_asset_names() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let target = linux_target();
        let mut record = package_record();
        record.asset_name = "custom-binary".to_string();
        record.selected_binary = "custom-binary".to_string();
        let manifest_tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: None,
            targets: BTreeMap::from([(
                target.key(),
                ManifestTargetOverride {
                    asset: "custom-binary".to_string(),
                    bin: "custom-binary".to_string(),
                    checksum_source: None,
                },
            )]),
        };

        assert_lock_matches_manifest_tool(
            temp_dir.path(),
            "tool",
            Some(&manifest_tool),
            &target,
            &record,
        )
        .expect("manifest override metadata is accepted");
        validate_locked_record_artifact(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &record,
            &target,
            Some(&manifest_tool),
        )
        .expect("manifest override asset is accepted locally");
    }

    #[test]
    fn lockfile_verify_rejects_query_bearing_asset_urls() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let mut record = package_record();
        record.checksum_source = ChecksumSource::GitHubDigest;
        record.asset_url =
            "https://github.com/owner/tool/releases/download/1.0.0/tool?token=secret".to_string();
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                LockTool {
                    source: "github:owner/tool".to_string(),
                    targets: BTreeMap::from([("linux-x86_64-gnu".to_string(), record)]),
                },
            )]),
        };

        let error =
            verify_lockfile_records(&temp_dir.path().join("binpm.lock"), lockfile, None, true)
                .expect_err("unsafe asset url");

        assert!(error.to_string().contains("must not include query"));
        assert!(!error.to_string().contains("secret"));
    }

    #[test]
    fn lockfile_verify_rejects_nondeterministic_installed_path() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let mut record = package_record();
        record.installed_path = temp_dir
            .path()
            .join(".binpm/bin/tool")
            .display()
            .to_string();
        record.checksum_source = ChecksumSource::GitHubDigest;
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                LockTool {
                    source: "github:owner/tool".to_string(),
                    targets: BTreeMap::from([("linux-x86_64-gnu".to_string(), record)]),
                },
            )]),
        };

        let error =
            verify_lockfile_records(&temp_dir.path().join("binpm.lock"), lockfile, None, true)
                .expect_err("absolute installed path is stale");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn lockfile_verify_rejects_non_canonical_target_keys() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let mut record = package_record();
        record.checksum_source = ChecksumSource::GitHubDigest;
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                LockTool {
                    source: "github:owner/tool".to_string(),
                    targets: BTreeMap::from([("linux-amd64-glibc".to_string(), record)]),
                },
            )]),
        };

        let error =
            verify_lockfile_records(&temp_dir.path().join("binpm.lock"), lockfile, None, true)
                .expect_err("target alias is stale");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn lockfile_verify_rejects_runtime_only_fields() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let mut record = package_record();
        record.cache_key = Some("sha256:abcdef".to_string());
        record.cache_path = Some("/tmp/binpm-cache/asset".to_string());
        record.installed_at = Some("2026-06-20T00:00:00Z".to_string());
        record.checksum_source = ChecksumSource::GitHubDigest;
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                LockTool {
                    source: "github:owner/tool".to_string(),
                    targets: BTreeMap::from([("linux-x86_64-gnu".to_string(), record)]),
                },
            )]),
        };

        let error =
            verify_lockfile_records(&temp_dir.path().join("binpm.lock"), lockfile, None, true)
                .expect_err("runtime fields are stale");

        assert!(error.to_string().contains("stale"));
    }

    #[test]
    fn package_record_verify_rejects_query_bearing_asset_urls() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let mut record = package_record();
        record.cache_key = Some(crate::storage::cache_key(&record.sha256));
        record.asset_url =
            "https://github.com/owner/tool/releases/download/1.0.0/tool?token=secret".to_string();

        let error =
            validate_package_record_metadata(&cache, &record).expect_err("unsafe package URL");

        assert!(error.to_string().contains("must not include query"));
        assert!(!error.to_string().contains("secret"));
    }

    #[test]
    fn package_record_verify_rejects_unmanaged_cache_path() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let mut record = package_record();
        record.cache_key = Some(crate::storage::cache_key(&record.sha256));
        record.cache_path = Some(
            temp_dir
                .path()
                .join("outside-cache-asset")
                .display()
                .to_string(),
        );

        let error =
            validate_package_record_metadata(&cache, &record).expect_err("unsafe cache path");

        assert!(error.to_string().contains("Unsafe cache path"));
        assert!(error
            .to_string()
            .contains(&cache.asset_path(&record.sha256).display().to_string()));
    }

    #[test]
    fn package_record_verify_rejects_stale_cache_key() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let mut record = package_record();
        record.cache_key = Some("sha256:stale".to_string());

        let error = validate_package_record_metadata(&cache, &record).expect_err("stale cache key");

        assert!(error.to_string().contains("Unsafe cache path"));
        assert!(error
            .to_string()
            .contains(&format!("sha256:{}", record.sha256)));
    }

    #[test]
    fn package_record_verify_rejects_mismatched_embedded_source_identity() {
        let mut record = package_record();
        record.source_path = "attacker/tool".to_string();

        let error = validate_package_record_source_identity("tool", &record)
            .expect_err("stale package record");

        assert!(matches!(error, BinpmError::StalePackageRecord { .. }));
    }

    #[test]
    fn package_record_verify_rejects_mismatched_package_spec() {
        let mut record = package_record();
        record.package_spec = "github:attacker/tool@1.0.0".to_string();

        let error = validate_package_record_source_identity("tool", &record)
            .expect_err("stale package record");

        assert!(matches!(error, BinpmError::StalePackageRecord { .. }));
    }

    #[test]
    fn package_record_verify_rejects_missing_cache_path() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let mut record = package_record();
        record.cache_key = Some(crate::storage::cache_key(&record.sha256));
        record.cache_path = None;

        let error =
            validate_package_record_metadata(&cache, &record).expect_err("missing cache path");

        assert!(error.to_string().contains("Unsafe cache path"));
        assert!(error
            .to_string()
            .contains(&cache.asset_path(&record.sha256).display().to_string()));
    }

    #[test]
    fn local_remove_rollback_preserves_absent_lockfile() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let root = temp_dir.path();
        write_manifest(
            &root.join(MANIFEST_FILE),
            &Manifest {
                version: 1,
                tools: BTreeMap::new(),
            },
        )
        .expect("manifest");
        let lockfile_path = root.join(LOCKFILE_FILE);
        assert!(!lockfile_path.exists());
        let state = capture_local_remove_state(root, "tool").expect("remove state");
        write_lockfile(
            &lockfile_path,
            &Lockfile {
                version: 1,
                tools: BTreeMap::new(),
            },
        )
        .expect("temporary lockfile");

        restore_local_remove_state(root, "tool", state);

        assert!(!lockfile_path.exists());
    }

    #[test]
    fn cache_hit_metadata_is_deferred_until_committed() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        cache.ensure().expect("cache paths");
        let bytes = b"cached tool";
        let sha256 = format!("{:x}", Sha256::digest(bytes));
        fs::create_dir_all(cache.entry_dir(&sha256)).expect("cache entry dir");
        fs::write(cache.asset_path(&sha256), bytes).expect("cache asset");
        let mut record = package_record();
        record.sha256 = sha256.clone();
        let install = InstalledPackage {
            record,
            populated_cache_entry: false,
            deferred_cache_hit: Some(resolved_asset(&sha256)),
            cache_metadata_snapshot: None,
        };

        assert!(read_cache_records(&cache)
            .expect("records before")
            .is_empty());
        commit_deferred_cache_hit(&cache, &install).expect("commit cache hit");

        let records = read_cache_records(&cache).expect("records after");
        assert_eq!(records.len(), 1);
        assert_eq!(records[0].sha256, sha256);
    }

    #[test]
    fn existing_cache_bytes_without_metadata_are_not_current_records() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        cache.ensure().expect("cache paths");
        let bytes = b"cached tool";
        let sha256 = format!("{:x}", Sha256::digest(bytes));
        fs::create_dir_all(cache.entry_dir(&sha256)).expect("cache entry dir");
        fs::write(cache.asset_path(&sha256), bytes).expect("cache asset");

        assert!(!has_current_cache_record(&cache, &sha256).expect("cache record check"));
    }

    #[test]
    fn malformed_target_cache_metadata_is_not_a_current_record() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        cache.ensure().expect("cache paths");
        let sha256 = package_record().sha256;
        fs::create_dir_all(cache.entry_dir(&sha256)).expect("cache entry dir");
        fs::write(cache.metadata_path(&sha256), "not = [valid").expect("corrupt metadata");

        assert!(!has_current_cache_record(&cache, &sha256).expect("cache record check"));
    }

    #[test]
    fn failed_install_cleanup_restores_existing_cache_metadata() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        cache.ensure().expect("cache paths");
        let bytes = b"cached tool";
        let sha256 = format!("{:x}", Sha256::digest(bytes));
        fs::create_dir_all(cache.entry_dir(&sha256)).expect("cache entry dir");
        fs::write(cache.asset_path(&sha256), bytes).expect("cache asset");
        let mut original = cache_record(&sha256);
        original.release_tag = "1.0.0".to_string();
        write_cache_record(&cache, &original).expect("original cache record");
        let snapshot = snapshot_cache_metadata(&cache, &sha256).expect("metadata snapshot");
        let mut rewritten = cache_record(&sha256);
        rewritten.release_tag = "2.0.0".to_string();
        write_cache_record(&cache, &rewritten).expect("rewritten cache record");
        let install = InstalledPackage {
            record: package_record(),
            populated_cache_entry: false,
            deferred_cache_hit: None,
            cache_metadata_snapshot: Some(snapshot),
        };

        cleanup_failed_install_cache(&cache, &sha256, None, &install).expect("cleanup cache");

        let restored = fs::read_to_string(cache.metadata_path(&sha256)).expect("metadata");
        assert!(restored.contains("release_tag = \"1.0.0\""));
        assert!(!restored.contains("release_tag = \"2.0.0\""));
    }

    #[test]
    fn failed_install_cleanup_preserves_existing_cache_asset_without_metadata() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        cache.ensure().expect("cache paths");
        let bytes = b"cached tool";
        let sha256 = format!("{:x}", Sha256::digest(bytes));
        fs::create_dir_all(cache.entry_dir(&sha256)).expect("cache entry dir");
        fs::write(cache.asset_path(&sha256), bytes).expect("cache asset");
        let snapshot = snapshot_cache_metadata(&cache, &sha256).expect("metadata snapshot");
        write_cache_record(&cache, &cache_record(&sha256)).expect("rewritten cache record");
        let mut record = package_record();
        record.sha256 = sha256.clone();
        let install = InstalledPackage {
            record,
            populated_cache_entry: false,
            deferred_cache_hit: None,
            cache_metadata_snapshot: Some(snapshot),
        };

        cleanup_failed_install_cache(&cache, &sha256, None, &install).expect("cleanup cache");

        assert_eq!(
            fs::read(cache.asset_path(&sha256)).expect("cache asset"),
            bytes
        );
        assert!(!cache.metadata_path(&sha256).exists());
    }

    #[test]
    fn failed_install_cleanup_restores_corrupted_existing_cache_asset() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        cache.ensure().expect("cache paths");
        let expected_bytes = b"expected tool";
        let corrupted_bytes = b"corrupted tool";
        let sha256 = format!("{:x}", Sha256::digest(expected_bytes));
        fs::create_dir_all(cache.entry_dir(&sha256)).expect("cache entry dir");
        fs::write(cache.asset_path(&sha256), corrupted_bytes).expect("corrupt cache asset");
        let snapshot = snapshot_cache_metadata(&cache, &sha256).expect("cache snapshot");
        fs::write(cache.asset_path(&sha256), expected_bytes).expect("repair cache asset");
        write_cache_record(&cache, &cache_record(&sha256)).expect("rewritten cache record");
        let mut record = package_record();
        record.sha256 = sha256.clone();
        let install = InstalledPackage {
            record,
            populated_cache_entry: false,
            deferred_cache_hit: None,
            cache_metadata_snapshot: Some(snapshot),
        };

        cleanup_failed_install_cache(&cache, &sha256, None, &install).expect("cleanup cache");

        assert_eq!(
            fs::read(cache.asset_path(&sha256)).expect("cache asset"),
            corrupted_bytes
        );
        assert!(!cache.metadata_path(&sha256).exists());
    }

    #[test]
    fn current_cache_record_requires_matching_cache_key() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        cache.ensure().expect("cache paths");
        let sha256 = package_record().sha256;
        write_cache_record(
            &cache,
            &CacheRecord {
                version: 1,
                cache_key: crate::storage::cache_key(&sha256),
                source_provider: SourceProvider::GitHub,
                source_host: "github.com".to_string(),
                source_path: "owner/tool".to_string(),
                release_tag: "1.0.0".to_string(),
                asset_name: "tool-linux".to_string(),
                asset_url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux"
                    .to_string(),
                byte_size: Some(11),
                sha256: sha256.clone(),
                checksum_source: ChecksumSource::Local,
                created_at: "2026-01-01T00:00:00Z".to_string(),
                last_used_at: None,
            },
        )
        .expect("write cache record");

        assert!(has_current_cache_record(&cache, &sha256).expect("cache record check"));
    }

    #[test]
    fn current_cache_record_ignores_unrelated_corrupt_metadata() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        cache.ensure().expect("cache paths");
        let sha256 = package_record().sha256;
        write_cache_record(
            &cache,
            &CacheRecord {
                version: 1,
                cache_key: crate::storage::cache_key(&sha256),
                source_provider: SourceProvider::GitHub,
                source_host: "github.com".to_string(),
                source_path: "owner/tool".to_string(),
                release_tag: "1.0.0".to_string(),
                asset_name: "tool-linux".to_string(),
                asset_url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux"
                    .to_string(),
                byte_size: Some(11),
                sha256: sha256.clone(),
                checksum_source: ChecksumSource::Local,
                created_at: "2026-01-01T00:00:00Z".to_string(),
                last_used_at: None,
            },
        )
        .expect("write target cache record");
        let corrupt_sha =
            "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff".to_string();
        std::fs::create_dir_all(cache.entry_dir(&corrupt_sha)).expect("corrupt entry");
        std::fs::write(cache.metadata_path(&corrupt_sha), "not = [valid")
            .expect("corrupt metadata");

        assert!(has_current_cache_record(&cache, &sha256).expect("cache record check"));
    }

    #[test]
    fn provider_digest_evidence_rejects_non_github_sources() {
        let mut record = package_record();
        mark_github_verified(&mut record);
        record.source_provider = SourceProvider::GitLab;
        record.source = "gitlab:owner/tool".to_string();
        record.package_spec = "gitlab:owner/tool@1.0.0".to_string();

        let error =
            validate_provider_digest_evidence(&record).expect_err("non-github github digest");

        assert!(matches!(error, BinpmError::ProviderDigestMismatch { .. }));
    }

    #[test]
    fn package_record_verify_requires_cache_key() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let record = package_record();

        let error = validate_package_record_metadata(&cache, &record).expect_err("missing key");

        assert!(error.to_string().contains("Unsafe cache path"));
        assert!(error
            .to_string()
            .contains(&format!("sha256:{}", record.sha256)));
    }

    #[test]
    fn failed_frozen_cache_repair_restores_existing_corrupt_asset() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let sha256 = format!("{:x}", Sha256::digest(b"expected bytes"));
        let asset_path = cache.asset_path(&sha256);
        fs::create_dir_all(asset_path.parent().expect("cache entry")).expect("cache entry");
        fs::write(&asset_path, b"corrupt bytes").expect("write corrupt asset");
        let snapshot = snapshot_cache_metadata(&cache, &sha256).expect("snapshot cache");
        fs::write(&asset_path, b"partial replacement").expect("write replacement");
        let mut record = package_record();
        record.sha256 = sha256.clone();
        let install = InstalledPackage {
            record,
            populated_cache_entry: false,
            deferred_cache_hit: None,
            cache_metadata_snapshot: Some(snapshot),
        };

        cleanup_failed_install_cache(&cache, &sha256, None, &install).expect("restore cache");

        assert_eq!(
            fs::read(asset_path).expect("read restored asset"),
            b"corrupt bytes"
        );
    }

    #[cfg(unix)]
    #[test]
    fn package_record_verify_rejects_symlinked_cache_asset() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let outside = tempfile::tempdir().expect("outside");
        let bytes = b"expected bytes";
        let sha256 = format!("{:x}", Sha256::digest(bytes));
        let asset_path = cache.asset_path(&sha256);
        let outside_asset = outside.path().join("asset");
        std::fs::create_dir_all(asset_path.parent().expect("cache entry")).expect("cache entry");
        std::fs::write(&outside_asset, bytes).expect("write outside asset");
        std::os::unix::fs::symlink(&outside_asset, &asset_path).expect("symlink asset");
        let mut record = package_record();
        record.sha256 = sha256;

        let error = verify_runtime_cache_bytes(&cache, &record).expect_err("symlinked asset");

        assert!(matches!(error, BinpmError::UnsafeManagedFile { .. }));
    }

    #[cfg(unix)]
    #[test]
    fn package_record_verify_rejects_symlinked_cache_digest_dir() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let outside = tempfile::tempdir().expect("outside");
        let bytes = b"expected bytes";
        let sha256 = format!("{:x}", Sha256::digest(bytes));
        std::fs::create_dir_all(cache.root.join("sha256")).expect("sha256 root");
        std::fs::write(outside.path().join("asset"), bytes).expect("outside asset");
        std::os::unix::fs::symlink(outside.path(), cache.entry_dir(&sha256))
            .expect("symlink digest dir");
        let mut record = package_record();
        record.sha256 = sha256;

        let error = verify_runtime_cache_bytes(&cache, &record).expect_err("symlinked digest dir");

        assert!(matches!(error, BinpmError::UnsafeManagedDirectory { .. }));
    }

    #[cfg(unix)]
    #[test]
    fn package_record_verify_rejects_symlinked_installed_executable() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let outside = tempfile::tempdir().expect("outside");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        std::fs::create_dir_all(&paths.bin).expect("bin dir");
        let bytes = b"expected bytes";
        let sha256 = format!("{:x}", Sha256::digest(bytes));
        let outside_asset = outside.path().join("tool");
        std::fs::write(&outside_asset, bytes).expect("outside executable");
        let installed_path = managed_installed_path(&paths, "tool", TargetOs::Linux);
        std::os::unix::fs::symlink(&outside_asset, &installed_path).expect("symlink executable");
        let mut record = package_record();
        record.sha256 = sha256;
        record.installed_path = installed_path.display().to_string();

        let installed_path =
            validate_installed_binary_path(&paths, "tool", &record).expect("installed path");
        let error = require_regular_managed_file(&installed_path).expect_err("symlinked install");

        assert!(matches!(error, BinpmError::UnsafeManagedFile { .. }));
    }

    #[cfg(unix)]
    #[test]
    fn package_record_verify_rejects_non_executable_installed_file() {
        use std::os::unix::fs::PermissionsExt;

        let temp_dir = tempfile::tempdir().expect("tempdir");
        let installed_path = temp_dir.path().join("tool");
        fs::write(&installed_path, b"expected bytes").expect("write installed file");
        let mut permissions = fs::metadata(&installed_path)
            .expect("metadata")
            .permissions();
        permissions.set_mode(0o644);
        fs::set_permissions(&installed_path, permissions).expect("chmod non-executable");

        let error =
            require_executable_managed_file(&installed_path).expect_err("non-executable install");

        assert!(matches!(error, BinpmError::UnsafeManagedFile { .. }));

        let mut permissions = fs::metadata(&installed_path)
            .expect("metadata")
            .permissions();
        permissions.set_mode(0o755);
        fs::set_permissions(&installed_path, permissions).expect("chmod executable");
        require_executable_managed_file(&installed_path).expect("executable install");
    }

    #[cfg(unix)]
    #[test]
    fn local_tool_execution_ready_treats_non_executable_binary_as_stale() {
        use std::os::unix::fs::PermissionsExt;

        let temp_dir = tempfile::tempdir().expect("tempdir");
        let root = temp_dir.path();
        let target = HostTarget::current().expect("current target");
        let paths = ScopePaths::local(root.to_path_buf());
        paths.ensure().expect("local scope dirs");
        let installed_path = managed_installed_path(&paths, "tool", target.os);
        fs::write(&installed_path, b"#!/bin/sh\nexit 0\n").expect("write installed file");
        let mut permissions = fs::metadata(&installed_path)
            .expect("metadata")
            .permissions();
        permissions.set_mode(0o644);
        fs::set_permissions(&installed_path, permissions).expect("chmod non-executable");

        let mut lock_record = package_record();
        lock_record.target_os = target.os;
        lock_record.target_arch = target.arch;
        lock_record.target_libc = target.libc;
        lock_record.installed_path = deterministic_installed_path("tool", target.os);
        let mut runtime_record = lock_record.clone();
        runtime_record.installed_path = installed_path.display().to_string();
        write_lockfile(
            &root.join(LOCKFILE_FILE),
            &Lockfile {
                version: 1,
                tools: BTreeMap::from([(
                    "tool".to_string(),
                    LockTool {
                        source: "github:owner/tool".to_string(),
                        targets: BTreeMap::from([(target.key(), lock_record)]),
                    },
                )]),
            },
        )
        .expect("write lockfile");
        write_package_record(&paths, "tool", &runtime_record).expect("write runtime record");
        let mut spec = SourceSpec::from_str("github:owner/tool").expect("parse source");
        spec.version = Some("1.0.0".to_string());

        assert!(
            !local_tool_execution_ready(root, "tool", &spec, None).expect("readiness check"),
            "non-executable managed binaries should be repaired by local x"
        );
    }

    #[test]
    fn project_root_uses_nearest_git_ancestor() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        std::fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
        let nested = temp_dir.path().join("nested").join("deeper");
        std::fs::create_dir_all(&nested).expect("create nested dir");

        assert_eq!(project_root_from(&nested), temp_dir.path());
    }

    #[test]
    fn project_root_uses_nearest_manifest_ancestor() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        std::fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n")
            .expect("write manifest");
        let nested = temp_dir.path().join("nested").join("deeper");
        std::fs::create_dir_all(&nested).expect("create nested dir");

        assert_eq!(project_root_from(&nested), temp_dir.path());
    }

    #[cfg(unix)]
    #[test]
    fn project_root_treats_broken_manifest_symlink_as_manifest() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        std::os::unix::fs::symlink(
            temp_dir.path().join("missing-manifest-target"),
            temp_dir.path().join("binpm.toml"),
        )
        .expect("create broken manifest symlink");
        let nested = temp_dir.path().join("nested").join("deeper");
        std::fs::create_dir_all(&nested).expect("create nested dir");

        assert_eq!(project_root_from(&nested), temp_dir.path());
    }

    #[test]
    fn project_root_prefers_manifest_ancestor_over_git_ancestor() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        std::fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
        let package = temp_dir.path().join("packages").join("cli");
        std::fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n")
            .expect("write root manifest");
        std::fs::create_dir_all(&package).expect("create package dir");
        std::fs::write(package.join("binpm.toml"), "version = 1\n")
            .expect("write package manifest");
        let nested = package.join("nested");
        std::fs::create_dir(&nested).expect("create nested dir");

        assert_eq!(project_root_from(&nested), package);
    }

    #[test]
    fn manifest_creation_root_uses_git_ancestor_before_manifest_ancestor() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        std::fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
        let nested = temp_dir.path().join("nested").join("deeper");
        std::fs::create_dir_all(&nested).expect("create nested dir");
        std::fs::write(nested.join("binpm.toml"), "version = 1\n").expect("write manifest");

        assert_eq!(manifest_creation_root_from(&nested), temp_dir.path());
    }

    #[test]
    fn add_root_prefers_nearest_manifest_before_creation_root() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        std::fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
        let package = temp_dir.path().join("packages").join("cli");
        std::fs::create_dir_all(&package).expect("create package dir");
        std::fs::write(package.join("binpm.toml"), "version = 1\n")
            .expect("write package manifest");
        let nested = package.join("nested");
        std::fs::create_dir(&nested).expect("create nested dir");

        assert_eq!(manifest_root_or_creation_root_from(&nested), package);
    }

    #[test]
    fn add_root_uses_creation_root_when_no_manifest_exists() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        std::fs::create_dir(temp_dir.path().join(".git")).expect("create .git");
        let nested = temp_dir.path().join("nested").join("deeper");
        std::fs::create_dir_all(&nested).expect("create nested dir");

        assert_eq!(
            manifest_root_or_creation_root_from(&nested),
            temp_dir.path()
        );
    }

    #[test]
    fn manifest_creation_root_uses_manifest_ancestor_without_git_ancestor() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        std::fs::write(temp_dir.path().join("binpm.toml"), "version = 1\n")
            .expect("write manifest");
        let nested = temp_dir.path().join("nested").join("deeper");
        std::fs::create_dir_all(&nested).expect("create nested dir");

        assert_eq!(manifest_creation_root_from(&nested), temp_dir.path());
    }

    #[test]
    fn project_root_falls_back_to_start_without_git_ancestor() {
        let temp_dir = tempfile::tempdir().expect("tempdir");

        assert_eq!(project_root_from(temp_dir.path()), temp_dir.path());
    }

    #[test]
    fn bash_env_paths_are_single_quoted() {
        let path = Path::new("/tmp/binpm home/$(touch x)/`cmd`");

        assert_eq!(
            shell_quote(Shell::Bash, path),
            "'/tmp/binpm home/$(touch x)/`cmd`'"
        );
    }

    #[test]
    fn bash_env_paths_escape_single_quotes() {
        let path = Path::new("/tmp/binpm'home");

        assert_eq!(shell_quote(Shell::Bash, path), "'/tmp/binpm'\\''home'");
    }

    #[test]
    fn bash_env_converts_windows_drive_paths_to_posix_paths() {
        assert_eq!(
            shell_path(Shell::Bash, r"C:\Users\me\.binpm\bin"),
            "/c/Users/me/.binpm/bin"
        );
    }

    #[test]
    fn zsh_env_converts_windows_verbatim_drive_paths_to_posix_paths() {
        assert_eq!(
            shell_path(Shell::Zsh, r"\\?\C:\repo\.binpm\bin"),
            "/c/repo/.binpm/bin"
        );
    }

    #[test]
    fn bash_env_converts_windows_unc_paths_to_posix_paths() {
        assert_eq!(
            shell_path(Shell::Bash, r"\\server\share\.binpm\bin"),
            "//server/share/.binpm/bin"
        );
    }

    #[test]
    fn powershell_env_preserves_windows_paths() {
        assert_eq!(
            shell_path(Shell::Powershell, r"C:\Users\me\.binpm\bin"),
            r"C:\Users\me\.binpm\bin"
        );
    }

    fn linux_target() -> HostTarget {
        HostTarget {
            os: TargetOs::Linux,
            arch: TargetArch::X86_64,
            libc: TargetLibc::Gnu,
        }
    }

    fn package_record() -> PackageRecord {
        PackageRecord {
            package_spec: "github:owner/tool@1.0.0".to_string(),
            source: "github:owner/tool".to_string(),
            source_provider: SourceProvider::GitHub,
            source_host: "github.com".to_string(),
            source_path: "owner/tool".to_string(),
            requested_version: Some("1.0.0".to_string()),
            release_tag: "1.0.0".to_string(),
            asset_name: "tool-linux".to_string(),
            asset_url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux"
                .to_string(),
            target_os: TargetOs::Linux,
            target_arch: TargetArch::X86_64,
            target_libc: TargetLibc::Gnu,
            archive_format: ArchiveFormat::BareExecutable,
            selected_binary: "tool-linux".to_string(),
            installed_path: ".binpm/bin/tool".to_string(),
            cache_key: None,
            cache_path: None,
            sha256: "abcdefabcdef0123456789abcdef0123456789abcdef0123456789abcdef0123".to_string(),
            checksum_source: ChecksumSource::Local,
            provider_digest_sha256: None,
            installed_at: None,
            signature_available: false,
            signature_verified: false,
        }
    }

    fn cache_record(sha256: &str) -> CacheRecord {
        CacheRecord {
            version: 1,
            cache_key: crate::storage::cache_key(sha256),
            source_provider: SourceProvider::GitHub,
            source_host: "github.com".to_string(),
            source_path: "owner/tool".to_string(),
            release_tag: "1.0.0".to_string(),
            asset_name: "tool-linux".to_string(),
            asset_url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux"
                .to_string(),
            byte_size: Some(11),
            sha256: sha256.to_string(),
            checksum_source: ChecksumSource::Local,
            created_at: "2026-01-01T00:00:00Z".to_string(),
            last_used_at: None,
        }
    }

    fn resolved_asset(sha256: &str) -> ResolvedAsset {
        ResolvedAsset {
            source: SourceSpec::from_str("github:owner/tool@1.0.0").expect("source"),
            release_tag: "1.0.0".to_string(),
            target: linux_target(),
            decision: CandidateDecision {
                asset_name: "tool-linux".to_string(),
                canonical_url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux"
                    .to_string(),
                download_url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux"
                    .to_string(),
                kind: ArtifactKind::BareExecutable,
                detected_os: Some(TargetOs::Linux),
                detected_arch: Some(TargetArch::X86_64),
                detected_libc: Some(TargetLibc::Gnu),
                score: None,
                eligible: true,
                recognized_pattern: true,
                rejection_reason: None,
            },
            archive_format: ArchiveFormat::BareExecutable,
            selected_binary: "tool-linux".to_string(),
            provider_digest_sha256: Some(sha256.to_string()),
            checksum_source: ChecksumSource::GitHubDigest,
            signature_available: false,
            signature_verified: false,
        }
    }

    fn write_tar_gz(path: &Path, entries: &[(&str, &[u8], u32)]) {
        let file = fs::File::create(path).expect("create archive");
        let encoder = flate2::write::GzEncoder::new(file, flate2::Compression::default());
        let mut builder = tar::Builder::new(encoder);
        for (name, bytes, mode) in entries {
            let mut header = tar::Header::new_gnu();
            header.set_size(bytes.len() as u64);
            header.set_mode(*mode);
            header.set_cksum();
            builder
                .append_data(&mut header, *name, *bytes)
                .expect("append archive entry");
        }
        builder.finish().expect("finish tar");
        let encoder = builder.into_inner().expect("finish gzip stream");
        encoder.finish().expect("finish gzip file");
    }

    fn write_zip(path: &Path, entries: &[(&str, &[u8], u32)]) {
        let file = fs::File::create(path).expect("create zip");
        let mut writer = zip::ZipWriter::new(file);
        for (name, bytes, mode) in entries {
            let options = zip::write::SimpleFileOptions::default()
                .unix_permissions(*mode)
                .compression_method(zip::CompressionMethod::Deflated);
            writer.start_file(*name, options).expect("start zip entry");
            writer.write_all(bytes).expect("write zip entry");
        }
        writer.finish().expect("finish zip");
    }

    fn mark_github_verified(record: &mut PackageRecord) {
        record.checksum_source = ChecksumSource::GitHubDigest;
        record.provider_digest_sha256 = Some(record.sha256.clone());
    }
}
