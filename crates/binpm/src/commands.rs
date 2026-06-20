use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    path::{Path, PathBuf},
    str::FromStr,
};

use sha2::{Digest, Sha256};
use tracing::{debug, info};

use crate::{
    assets::{gitlab_https_eligible, select_asset, ArtifactKind},
    cli::{
        AddArgs, CacheCommand, Cli, Command, EnvArgs, ExecArgs, ExplainArgs, InfoArgs, InitArgs,
        InstallArgs, RemoveArgs, ScopedArgs, Shell, UpdateArgs, VerifyArgs,
    },
    contract::{ArchiveFormat, ChecksumSource, HostTarget, Scope, SourceSpec, TargetOs},
    error::{BinpmError, Result},
    release::{client_for_source, GitHubReleaseClient, GitLabReleaseClient, ReleaseAsset},
    storage::{
        archive_format, clean_cache, deterministic_installed_path, install_bare_executable,
        installed_filename, list_package_records, managed_installed_path,
        package_record_from_resolved, package_record_path, populate_cache_from_bytes, prune_cache,
        read_cache_records, read_lockfile, read_manifest, read_package_record,
        record_verified_cache_hit, referenced_cache_keys, remove_cache_ref,
        remove_installed_binary, remove_package_record, remove_path_if_exists,
        sanitize_persisted_url, validate_command_name, validate_download_url,
        validate_installed_binary_path, validate_sha256_digest, write_cache_ref, write_lockfile,
        write_manifest, write_package_record, CachePaths, LockTool, Manifest, ManifestTool,
        PackageRecord, ResolvedAsset, ScopePaths, LOCKFILE_FILE, MANIFEST_FILE,
    },
};

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
    let mut manifest = if manifest_path.exists() {
        read_manifest(&manifest_path)?
    } else {
        Manifest {
            version: 1,
            tools: BTreeMap::new(),
        }
    };
    let manifest_tool = manifest.tools.get(&args.cmd).cloned();
    let prior_state = capture_local_tool_state(&root, &args.cmd)?;
    let install = install_local_tool(
        &root,
        &args.cmd,
        &spec,
        manifest_tool.as_ref(),
        args.lockfile.frozen_lockfile(),
        args.require_verified,
    )?;
    let record = install.record;
    manifest.tools.insert(
        args.cmd.clone(),
        update_manifest_tool_source(manifest_tool, &spec),
    );
    if let Err(error) = write_manifest(&manifest_path, &manifest) {
        rollback_local_install_state(&root, &args.cmd, &record, prior_state);
        if install.populated_cache_entry {
            let cache_paths = CachePaths::new(&binpm_home()?);
            remove_unreferenced_cache_entry(&cache_paths, &record.sha256, Some(&root))?;
        }
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
    let resolved_command = args.cmd().to_string_lossy();
    let forwarded_arg_count = args.args().len();

    if let Some(source) = &args.package {
        let spec = SourceSpec::from_str(source)?;
        info!(
            command = "x",
            resolved_command = %resolved_command,
            explicit_package = true,
            source_provider = spec.provider.as_str(),
            source_host = spec.host,
            source_path = spec.path,
            source_version = spec.version.as_deref().unwrap_or(""),
            forwarded_arg_count,
            frozen_lockfile = args.lockfile.frozen_lockfile(),
            "Prepared explicit-package execution request"
        );
    } else {
        info!(
            command = "x",
            resolved_command = %resolved_command,
            explicit_package = false,
            forwarded_arg_count,
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
            let home = binpm_home()?;
            let paths = CachePaths::new(&home);
            let global_paths = ScopePaths::global(home);
            let local_paths = project_root().ok().map(ScopePaths::local);
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
            let local_paths = project_root().ok().map(ScopePaths::local);
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
    }
    log_read_only_scope("info", args.scope.scope());
    not_implemented("info")
}

fn outdated(args: ScopedArgs) -> Result<i32> {
    log_read_only_scope("outdated", args.scope.scope());
    not_implemented("outdated")
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
    not_implemented("explain")
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
            if let Some(selected) = select_explain_asset(&selection.decisions) {
                println!("selected_asset: {}", selected.asset_name);
                println!("selected_asset_url: {}", selected.canonical_url);
                println!(
                    "selected_asset_score: {}",
                    selected.score.unwrap_or_default()
                );
            } else {
                println!("selected_asset: <none>");
            }
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

fn select_explain_asset(
    decisions: &[crate::assets::CandidateDecision],
) -> Option<&crate::assets::CandidateDecision> {
    decisions
        .iter()
        .find(|decision| decision.eligible && decision.kind == ArtifactKind::BareExecutable)
        .or_else(|| decisions.iter().find(|decision| decision.eligible))
}

fn release_api_url(spec: &SourceSpec) -> String {
    match spec.provider {
        crate::contract::SourceProvider::GitHub => GitHubReleaseClient::releases_api_url(spec),
        crate::contract::SourceProvider::GitLab => GitLabReleaseClient::releases_api_url(spec),
    }
}

fn install_global_source(spec: SourceSpec, require_verified: bool) -> Result<i32> {
    let cmd = repo_name(&spec).to_string();
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
    let record = install.record;
    if let Err(error) = write_package_record(&scope_paths, &cmd, &record) {
        let rollback_result = rollback_failed_install(&scope_paths, &cmd, &record);
        restore_runtime_tool_state(&scope_paths, &cmd, prior_state);
        let cache_cleanup_result = if install.populated_cache_entry {
            remove_unreferenced_cache_entry(&cache_paths, &record.sha256, None)
        } else {
            Ok(())
        };
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
    let manifest_path = root.join(MANIFEST_FILE);
    let mut manifest = read_manifest(&manifest_path)?;
    let manifest_tool = manifest.tools.get(&cmd).cloned();
    let prior_state = capture_local_tool_state(&root, &cmd)?;
    let install = install_local_tool(
        &root,
        &cmd,
        &spec,
        manifest_tool.as_ref(),
        frozen_lockfile,
        require_verified,
    )?;
    let record = install.record;
    manifest.tools.insert(
        cmd.clone(),
        update_manifest_tool_source(manifest_tool, &spec),
    );
    if let Err(error) = write_manifest(&manifest_path, &manifest) {
        rollback_local_install_state(&root, &cmd, &record, prior_state);
        if install.populated_cache_entry {
            let cache_paths = CachePaths::new(&binpm_home()?);
            remove_unreferenced_cache_entry(&cache_paths, &record.sha256, Some(&root))?;
        }
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
    let mut completed = Vec::new();
    for (cmd, tool) in &manifest.tools {
        if !selected.is_empty() && !selected.contains(cmd) {
            continue;
        }
        validate_command_name(cmd)?;
        let mut spec = parse_manifest_source(&tool.source)?;
        spec.version = tool.version.clone();
        let prior_state = capture_local_tool_state(&root, cmd)?;
        match install_local_tool(
            &root,
            cmd,
            &spec,
            Some(tool),
            frozen_lockfile,
            require_verified,
        ) {
            Ok(install) => completed.push((
                cmd.clone(),
                install.record,
                install.populated_cache_entry,
                prior_state,
            )),
            Err(error) => {
                let cache_paths = CachePaths::new(&binpm_home()?);
                for (completed_cmd, completed_record, populated_cache_entry, completed_state) in
                    completed.into_iter().rev()
                {
                    rollback_local_install_state(
                        &root,
                        &completed_cmd,
                        &completed_record,
                        completed_state,
                    );
                    if populated_cache_entry {
                        remove_unreferenced_cache_entry(
                            &cache_paths,
                            &completed_record.sha256,
                            Some(&root),
                        )?;
                    }
                }
                return Err(error);
            }
        }
    }
    if selected.is_empty() {
        if let Err(error) = remove_local_manifest_orphans(&root, &manifest.tools, frozen_lockfile) {
            let cache_paths = CachePaths::new(&binpm_home()?);
            for (completed_cmd, completed_record, populated_cache_entry, completed_state) in
                completed.into_iter().rev()
            {
                rollback_local_install_state(
                    &root,
                    &completed_cmd,
                    &completed_record,
                    completed_state,
                );
                if populated_cache_entry {
                    remove_unreferenced_cache_entry(
                        &cache_paths,
                        &completed_record.sha256,
                        Some(&root),
                    )?;
                }
            }
            return Err(error);
        }
    }
    Ok(0)
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
    let record = install.record;

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
        if install.populated_cache_entry {
            remove_unreferenced_cache_entry(&cache_paths, &record.sha256, Some(root))?;
        }
        return Err(error);
    }
    println!("installed {cmd} {}", record.installed_path);
    Ok(InstalledPackage {
        record,
        populated_cache_entry: install.populated_cache_entry,
    })
}

struct InstalledPackage {
    record: PackageRecord,
    populated_cache_entry: bool,
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
    scope_paths.ensure()?;
    cache_paths.ensure()?;
    let resolved = resolve_asset(spec, tool)?;
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
    if resolved.archive_format != ArchiveFormat::BareExecutable {
        return Err(BinpmError::ArchiveExtractionNotImplemented {
            asset: resolved.decision.asset_name,
        });
    }
    if let Some(expected) = &resolved.provider_digest_sha256 {
        let cache_asset = cache_paths.asset_path(expected);
        if cache_asset.exists() && crate::storage::verify_sha256(&cache_asset, expected).is_ok() {
            record_verified_cache_hit(cache_paths, &resolved)?;
            let installed_path = managed_installed_path(scope_paths, cmd, resolved.target.os);
            install_bare_executable(&cache_asset, &installed_path)?;
            return Ok(InstalledPackage {
                record: package_record_from_resolved(
                    cmd,
                    &resolved,
                    expected.clone(),
                    &cache_asset,
                    &installed_path,
                    true,
                )?,
                populated_cache_entry: false,
            });
        }
    }
    let bytes = download_asset(&resolved.decision.download_url)?;
    let sha256 = format!("{:x}", Sha256::digest(&bytes));
    let cache_asset = cache_paths.asset_path(&sha256);
    let had_verified_cache_entry =
        cache_asset.exists() && crate::storage::verify_sha256(&cache_asset, &sha256).is_ok();
    if let Some(expected) = &resolved.provider_digest_sha256 {
        if &sha256 != expected {
            return Err(BinpmError::DigestMismatch {
                path: cache_paths.asset_path(expected),
                expected: expected.clone(),
                actual: sha256,
            });
        }
    }
    let (sha256, cache_asset) = populate_cache_from_bytes(cache_paths, &resolved, &bytes)?;
    let populated_cache_entry = !had_verified_cache_entry;
    let installed_path = managed_installed_path(scope_paths, cmd, resolved.target.os);
    if let Err(error) = install_bare_executable(&cache_asset, &installed_path) {
        if populated_cache_entry {
            remove_unreferenced_cache_entry(cache_paths, &sha256, local_root)?;
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
    if record.archive_format != ArchiveFormat::BareExecutable {
        return Err(BinpmError::ArchiveExtractionNotImplemented {
            asset: record.asset_name,
        });
    }

    let home = binpm_home()?;
    let cache_paths = CachePaths::new(&home);
    cache_paths.ensure()?;
    validate_sha256_digest(&record.sha256)?;
    let cache_asset = cache_paths.asset_path(&record.sha256);
    let mut populated_cache_entry = false;
    if cache_asset.exists() && crate::storage::verify_sha256(&cache_asset, &record.sha256).is_err()
    {
        remove_path_if_exists(&cache_asset)?;
    }
    if !cache_asset.exists() {
        let download_url = locked_record_download_url(&record)?;
        let bytes = download_asset(&download_url)?;
        let actual = format!("{:x}", Sha256::digest(&bytes));
        if actual != record.sha256 {
            return Err(BinpmError::DigestMismatch {
                path: cache_asset,
                expected: record.sha256,
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
                kind: ArtifactKind::BareExecutable,
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
        populated_cache_entry = true;
    }

    let prior_state = capture_local_tool_state(root, cmd)?;
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let installed_path = managed_installed_path(&scope_paths, cmd, target.os);
    if let Err(error) =
        install_bare_executable(&cache_paths.asset_path(&record.sha256), &installed_path)
    {
        if populated_cache_entry {
            remove_unreferenced_cache_entry(&cache_paths, &record.sha256, Some(root))?;
        }
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
        if populated_cache_entry {
            remove_unreferenced_cache_entry(&cache_paths, &runtime_record.sha256, Some(root))?;
        }
        return Err(error);
    }
    println!("installed {cmd} {}", runtime_record.installed_path);
    Ok(InstalledPackage {
        record: runtime_record,
        populated_cache_entry,
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
        || record.target_os != target.os
        || record.target_arch != target.arch
        || record.target_libc != target.libc
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
    if format != ArchiveFormat::BareExecutable {
        return Err(BinpmError::ArchiveExtractionNotImplemented {
            asset: record.asset_name.clone(),
        });
    }
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

fn locked_record_download_url(record: &PackageRecord) -> Result<String> {
    let mut spec = SourceSpec::from_str(&record.source)?;
    spec.version = Some(record.release_tag.clone());
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

fn assert_lock_matches_manifest_tool(
    root: &Path,
    cmd: &str,
    tool: Option<&ManifestTool>,
    target: &HostTarget,
    record: &PackageRecord,
) -> Result<()> {
    if let Some(override_target) = manifest_target_override(tool, target)? {
        if let Some(checksum_source) = override_target.checksum_source {
            return Err(BinpmError::UnverifiedChecksumSourceOverride {
                checksum_source: checksum_source.as_str().to_string(),
            });
        }
        if record.asset_name != override_target.asset
            || record.selected_binary != override_target.bin
        {
            return Err(BinpmError::StaleLockfile {
                path: root.join(LOCKFILE_FILE),
                cmd: cmd.to_string(),
            });
        }
        return Ok(());
    }
    if let Some(bin) = tool.and_then(|tool| tool.bin.as_ref()) {
        if record.selected_binary != *bin {
            return Err(BinpmError::StaleLockfile {
                path: root.join(LOCKFILE_FILE),
                cmd: cmd.to_string(),
            });
        }
    }
    Ok(())
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
    let manifest_checksum_source = manifest_checksum_source(tool, &target)?;
    let selected_binary = match manifest_target_override(tool, &target)?
        .map(|override_target| override_target.bin.as_str())
        .or_else(|| tool.and_then(|tool| tool.bin.as_deref()))
    {
        Some(bin) => bin.to_string(),
        None => decision.asset_name.clone(),
    };
    let provider_digest_sha256 = release
        .assets
        .iter()
        .find(|asset| asset.name == decision.asset_name)
        .and_then(|asset| github_sha256_digest(asset.digest.as_deref()));
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
) -> Result<ChecksumSource> {
    if let Some(checksum_source) = manifest_target_override(tool, target)?
        .and_then(|override_target| override_target.checksum_source)
    {
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

    let tool_bin = tool.and_then(|tool| tool.bin.as_deref());
    let selection =
        select_asset(spec.provider, target, assets).ok_or_else(|| BinpmError::AssetNotFound {
            package: spec.to_string(),
            target: target_key.clone(),
        })?;
    let eligible = selection
        .decisions
        .into_iter()
        .filter(|decision| decision.eligible)
        .collect::<Vec<_>>();
    if let Some(bin) = tool_bin {
        return eligible
            .into_iter()
            .find(|decision| {
                decision.kind == ArtifactKind::BareExecutable && decision.asset_name == bin
            })
            .ok_or_else(|| BinpmError::AssetNotFound {
                package: spec.to_string(),
                target: target_key,
            });
    }

    eligible
        .iter()
        .find(|decision| decision.kind == ArtifactKind::BareExecutable)
        .cloned()
        .or_else(|| eligible.into_iter().next())
        .ok_or_else(|| BinpmError::AssetNotFound {
            package: spec.to_string(),
            target: target_key,
        })
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
    installed_bytes: Option<Vec<u8>>,
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
    let installed_bytes = package_record
        .as_ref()
        .map(
            |record| match validate_installed_binary_path(paths, cmd, record) {
                Ok(path) => match fs::read(&path) {
                    Ok(bytes) => Ok(Some(bytes)),
                    Err(source) if source.kind() == std::io::ErrorKind::NotFound => Ok(None),
                    Err(source) => Err(BinpmError::ReadFile { path, source }),
                },
                Err(BinpmError::UnsafeInstalledPath { .. }) => Ok(None),
                Err(error) => Err(error),
            },
        )
        .transpose()?
        .flatten();
    Ok(RuntimeToolState {
        package_record,
        installed_bytes,
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
            orphan_cmds.insert(cmd);
        }
    }

    let lockfile_path = root.join(LOCKFILE_FILE);
    let mut lockfile = read_lockfile(&lockfile_path)?;
    for cmd in lockfile.tools.keys() {
        if !manifest_tools.contains_key(cmd) {
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
        let (stale_installed_path, stale_target_os) = if record_path.exists() {
            let record = read_package_record(&record_path)?;
            let installed_path = managed_installed_path(paths, cmd, record.target_os);
            if !is_manifest_managed_installed_path(
                paths,
                manifest_tools,
                &installed_path,
                record.target_os,
            ) {
                match remove_installed_binary(paths, cmd, &record) {
                    Ok(()) | Err(BinpmError::UnsafeInstalledPath { .. }) => {}
                    Err(error) => return Err(error),
                }
            }
            (installed_path, record.target_os)
        } else {
            let target_os = HostTarget::current()?.os;
            (current_platform_installed_path(paths, cmd), target_os)
        };
        remove_package_record(paths, cmd)?;
        remove_cache_ref(cache_paths, root, cmd)?;
        if !is_manifest_managed_installed_path(
            paths,
            manifest_tools,
            &stale_installed_path,
            stale_target_os,
        ) {
            remove_path_if_exists(&stale_installed_path)?;
        }
        Ok(())
    })();
    if let Err(error) = cleanup_result {
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
    manifest_tools
        .keys()
        .any(|cmd| managed_installed_path(paths, cmd, target_os) == path)
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
                let _ = remove_package_record(paths, cmd);
                return;
            }
            let _ = write_package_record(paths, cmd, previous);
            if let Some(bytes) = prior_state.installed_bytes {
                let _ = restore_executable_bytes(&previous_path, &bytes);
            } else if let Some(cache_path) = &previous.cache_path {
                let expected_cache_path =
                    binpm_home().map(|home| CachePaths::new(&home).asset_path(&previous.sha256));
                if expected_cache_path.as_ref().ok() == Some(&PathBuf::from(cache_path)) {
                    let _ = install_bare_executable(Path::new(cache_path), &previous_path);
                }
            }
        }
        None => {
            let _ = remove_package_record(paths, cmd);
        }
    }
}

fn restore_executable_bytes(path: &Path, bytes: &[u8]) -> Result<()> {
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
        permissions.set_mode(0o755);
        fs::set_permissions(path, permissions).map_err(|source| BinpmError::WriteFile {
            path: path.to_path_buf(),
            source,
        })?;
    }
    Ok(())
}

fn download_asset(url: &str) -> Result<Vec<u8>> {
    validate_download_url(url)?;
    info!(
        asset_url = url.split(['?', '#']).next().unwrap_or(url),
        "Downloading release asset"
    );
    let response = reqwest::blocking::Client::builder()
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
        .map_err(BinpmError::ReleaseHttpClient)?
        .get(url)
        .send()
        .map_err(|error| BinpmError::ReleaseLookup(error.without_url()))?
        .error_for_status()
        .map_err(|error| BinpmError::ReleaseLookup(error.without_url()))?;
    validate_download_url(response.url().as_str())?;
    response
        .bytes()
        .map(|bytes| bytes.to_vec())
        .map_err(|error| BinpmError::ReleaseLookup(error.without_url()))
}

fn remove_local_tool(cmd: &str) -> Result<i32> {
    let root = require_manifest_root()?;
    validate_command_name(cmd)?;
    let manifest_path = root.join(MANIFEST_FILE);
    let lockfile_path = root.join(LOCKFILE_FILE);
    let paths = ScopePaths::local(root.clone());
    let prior_state = capture_local_remove_state(&root, cmd)?;
    let record_path = package_record_path(&paths, cmd);
    let cleanup_result = (|| {
        let stale_installed_path = if record_path.exists() {
            let record = read_package_record(&record_path)?;
            let installed_path = managed_installed_path(&paths, cmd, record.target_os);
            match remove_installed_binary(&paths, cmd, &record) {
                Ok(()) | Err(BinpmError::UnsafeInstalledPath { .. }) => {}
                Err(error) => return Err(error),
            }
            installed_path
        } else {
            current_platform_installed_path(&paths, cmd)
        };
        remove_package_record(&paths, cmd)?;
        remove_cache_ref(&CachePaths::new(&binpm_home()?), &root, cmd)?;
        remove_path_if_exists(&stale_installed_path)?;
        Ok(())
    })();
    if let Err(error) = cleanup_result {
        restore_local_remove_state(&root, cmd, prior_state);
        return Err(error);
    }

    let mut manifest = match read_manifest(&manifest_path) {
        Ok(manifest) => manifest,
        Err(error) => {
            restore_local_remove_state(&root, cmd, prior_state);
            return Err(error);
        }
    };
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
    let stale_installed_path = if record_path.exists() {
        let record = read_package_record(&record_path)?;
        let installed_path = managed_installed_path(paths, cmd, record.target_os);
        match remove_installed_binary(paths, cmd, &record) {
            Ok(()) | Err(BinpmError::UnsafeInstalledPath { .. }) => {}
            Err(error) => return Err(error),
        }
        installed_path
    } else {
        current_platform_installed_path(paths, cmd)
    };
    if let Err(error) =
        remove_package_record(paths, cmd).and_then(|_| remove_path_if_exists(&stale_installed_path))
    {
        restore_runtime_tool_state(paths, cmd, prior_state);
        return Err(error);
    }
    Ok(())
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
    Ok(find_manifest_root(&cwd)
        .or_else(|| find_git_root(&cwd))
        .unwrap_or(&cwd)
        .to_path_buf())
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
        validate_package_record_metadata(&cache_paths, &record)?;
        verify_runtime_cache_bytes(&cache_paths, &record)?;
        let installed_path = validate_installed_binary_path(&paths, &cmd, &record)?;
        crate::storage::verify_sha256(&installed_path, &record.sha256)?;
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
    if let Some(cache_path) = &record.cache_path {
        let cache_path = Path::new(cache_path);
        let expected_cache_path = cache_paths.asset_path(&record.sha256);
        if cache_path != expected_cache_path {
            return Err(BinpmError::UnsafeCachePath {
                path: cache_path.to_path_buf(),
                expected: expected_cache_path,
            });
        }
    }
    Ok(())
}

fn verify_runtime_cache_bytes(cache_paths: &CachePaths, record: &PackageRecord) -> Result<()> {
    crate::storage::verify_sha256(&cache_paths.asset_path(&record.sha256), &record.sha256)
}

fn validate_provider_digest_evidence(record: &PackageRecord) -> Result<()> {
    if record.checksum_source == ChecksumSource::GitHubDigest
        && record.provider_digest_sha256.as_deref() != Some(record.sha256.as_str())
    {
        return Err(BinpmError::ProviderDigestMismatch {
            package: record.package_spec.clone(),
        });
    }
    Ok(())
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

fn project_root() -> Result<PathBuf> {
    let cwd = current_dir()?;
    Ok(project_root_from(&cwd))
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

fn find_manifest_root(start: &Path) -> Option<&Path> {
    start
        .ancestors()
        .find(|path| path.join(MANIFEST_FILE).exists())
}

fn find_git_root(start: &Path) -> Option<&Path> {
    start.ancestors().find(|path| path.join(".git").exists())
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
        path::{Path, PathBuf},
        str::FromStr,
    };

    use super::{
        assert_local_runtime_records_complete, assert_lock_matches_manifest_tool,
        assert_lock_record_matches_source_and_target, assert_runtime_record_matches_lock,
        binpm_home_from_values, capture_local_remove_state, capture_runtime_tool_state,
        deterministic_installed_path, github_sha256_digest, local_runtime_lock_records,
        lock_targets_conflict_with_manifest, lock_targets_conflict_with_record, lockfile_digest,
        manifest_checksum_source, manifest_creation_root_from, manifest_target_override,
        manifest_tool_from_source, parse_manifest_source, project_root_from,
        remove_global_tool_from_paths, remove_local_manifest_orphans, restore_local_remove_state,
        restore_runtime_tool_state, select_explain_asset, select_manifest_asset, shell_path,
        shell_quote, source_install_scope, update_manifest_tool_source,
        validate_locked_record_artifact, validate_package_record_metadata, verify_lockfile_records,
        verify_runtime_cache_bytes, ArtifactKind, RuntimeToolState,
    };
    use crate::{
        cli::Shell,
        contract::{
            ArchiveFormat, ChecksumSource, HostTarget, Scope, SourceProvider, SourceSpec,
            TargetArch, TargetLibc, TargetOs,
        },
        release::ReleaseAsset,
        storage::{
            write_lockfile, write_manifest, write_package_record, CachePaths, LockTool, Lockfile,
            Manifest, ManifestTargetOverride, ManifestTool, PackageRecord, ScopePaths,
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
    fn missing_lockfile_has_stable_empty_digest() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let digest = lockfile_digest(&temp_dir.path().join("binpm.lock")).expect("digest");

        assert_eq!(
            digest,
            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
        );
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
            deterministic_installed_path("tool", TargetOs::Linux),
            ".binpm/bin/tool"
        );
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
        darwin_record.package_spec = "github:owner/tool@1.0.0#darwin".to_string();
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

        assert!(error.to_string().contains("github:owner/tool@1.0.0#darwin"));
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
        assert!(state.installed_bytes.is_none());
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

        let error = manifest_checksum_source(Some(&tool), &target)
            .expect_err("unverified checksum source override");

        assert!(error.to_string().contains("cannot be used"));
        assert_eq!(
            manifest_checksum_source(None, &target).expect("default checksum source"),
            ChecksumSource::Local
        );
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
                installed_bytes: Some(b"changed".to_vec()),
            },
        );

        assert_eq!(
            std::fs::read_to_string(&outside).expect("read outside file"),
            "original"
        );
        assert!(!crate::storage::package_record_path(&paths, "tool").exists());
    }

    #[test]
    fn global_remove_skips_unsafe_installed_path_and_cleans_record() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = crate::storage::ScopePaths::global(temp_dir.path().join("home"));
        let outside = temp_dir.path().join("outside-tool");
        std::fs::write(&outside, "original").expect("write outside file");
        let mut record = package_record();
        record.installed_path = outside.display().to_string();
        write_package_record(&paths, "tool", &record).expect("write package record");
        std::fs::write(paths.bin.join("tool"), "shim").expect("write bin candidate");

        remove_global_tool_from_paths(&paths, "tool").expect("remove global tool");

        assert_eq!(
            std::fs::read_to_string(&outside).expect("read outside file"),
            "original"
        );
        assert!(!crate::storage::package_record_path(&paths, "tool").exists());
        assert!(!paths.bin.join("tool").exists());
    }

    #[test]
    fn global_remove_preserves_exe_sibling_for_linux_tool() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = crate::storage::ScopePaths::global(temp_dir.path().join("home"));
        let record = package_record();
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
    fn automatic_asset_selection_prefers_bare_executable_over_archive() {
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
            select_manifest_asset(&spec, None, &target, &assets).expect("bare executable");

        assert_eq!(selected.asset_name, "tool-linux-x64");
        assert_eq!(selected.kind, ArtifactKind::BareExecutable);
    }

    #[test]
    fn manifest_bin_override_constrains_bare_executable_selection() {
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: Some("tool-linux-secondary".to_string()),
            targets: BTreeMap::new(),
        };
        let assets = [
            ReleaseAsset {
                name: "tool-linux-primary".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-primary"
                    .to_string(),
                provider_url: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            },
            ReleaseAsset {
                name: "tool-linux-secondary".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-secondary"
                    .to_string(),
                provider_url: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            },
        ];

        let selected =
            select_manifest_asset(&spec, Some(&tool), &target, &assets).expect("selected asset");

        assert_eq!(selected.asset_name, "tool-linux-secondary");
    }

    #[test]
    fn manifest_bin_override_rejects_non_matching_bare_executable() {
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

        let error =
            select_manifest_asset(&spec, Some(&tool), &target, &assets).expect_err("missing bin");

        assert!(error.to_string().contains("No installable asset"));
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
    fn explain_selection_reports_install_selected_bare_executable() {
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

        let selected = select_explain_asset(&selection.decisions).expect("explain selection");

        assert_eq!(selected.asset_name, "tool-linux-x64");
        assert_eq!(selected.kind, ArtifactKind::BareExecutable);
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
    fn frozen_lock_reclassifies_locked_asset_names() {
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
        .expect_err("locked archive rejected");

        assert!(error
            .to_string()
            .contains("Archive extraction is not implemented"));
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
    fn lockfile_verify_honors_explicit_target_override_asset_names() {
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
        let manifest = Manifest {
            version: 1,
            tools: BTreeMap::from([("tool".to_string(), manifest_tool)]),
        };
        let lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::from([(
                "tool".to_string(),
                LockTool {
                    source: "github:owner/tool".to_string(),
                    targets: BTreeMap::from([(target.key(), record)]),
                },
            )]),
        };

        verify_lockfile_records(
            &temp_dir.path().join("binpm.lock"),
            lockfile,
            Some((&manifest, temp_dir.path())),
            false,
        )
        .expect("manifest override asset is accepted during verify");
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
    fn runtime_cache_verify_uses_expected_cache_path_when_record_path_is_missing() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let mut record = package_record();
        record.cache_key = Some(crate::storage::cache_key(&record.sha256));
        record.cache_path = None;
        validate_package_record_metadata(&cache, &record).expect("metadata without cache path");

        let error = verify_runtime_cache_bytes(&cache, &record).expect_err("missing cache asset");

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

    fn mark_github_verified(record: &mut PackageRecord) {
        record.checksum_source = ChecksumSource::GitHubDigest;
        record.provider_digest_sha256 = Some(record.sha256.clone());
    }
}
