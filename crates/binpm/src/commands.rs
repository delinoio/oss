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
    contract::{ArchiveFormat, ChecksumSource, HostTarget, Scope, SourceSpec},
    error::{BinpmError, Result},
    release::{client_for_source, GitHubReleaseClient, GitLabReleaseClient, ReleaseAsset},
    storage::{
        archive_format, clean_cache, deterministic_installed_path, install_bare_executable,
        list_package_records, managed_installed_path, package_record_from_resolved,
        package_record_path, populate_cache_from_bytes, prune_cache, read_cache_records,
        read_lockfile, read_manifest, read_package_record, referenced_cache_keys, remove_cache_ref,
        remove_installed_binary, remove_package_record, remove_path_if_exists,
        sanitize_persisted_url, validate_command_name, write_cache_ref, write_lockfile,
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
        if scope == Scope::Local {
            return install_local_source(spec, frozen_lockfile, args.require_verified);
        }
        install_global_source(spec, args.require_verified)
    } else {
        info!(
            command = "install",
            scope = scope.as_str(),
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
    let prior_state = capture_local_tool_state(&root, &args.cmd)?;
    let record = install_local_tool(
        &root,
        &args.cmd,
        &spec,
        None,
        args.lockfile.frozen_lockfile(),
        args.require_verified,
    )?;
    manifest.tools.insert(
        args.cmd.clone(),
        ManifestTool {
            source: spec.source_without_version(),
            version: spec.version.clone(),
            bin: None,
            targets: BTreeMap::new(),
        },
    );
    if let Err(error) = write_manifest(&manifest_path, &manifest) {
        rollback_local_install_state(&root, &args.cmd, &record, prior_state);
        return Err(error);
    }
    println!("added {}", args.cmd);
    Ok(0)
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
            let paths = CachePaths::new(&binpm_home()?);
            for record in read_cache_records(&paths)? {
                println!(
                    "{} {} {} {}/{} {} {} {}",
                    record.cache_key,
                    record.byte_size.unwrap_or_default(),
                    record.source_provider.as_str(),
                    record.source_host,
                    record.source_path,
                    record.release_tag,
                    record.asset_name,
                    record.checksum_source.as_str()
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
            for (cmd, tool) in manifest.tools {
                let state = package_record_path(&paths, &cmd);
                let installed = if state.exists() {
                    "installed"
                } else {
                    "declared"
                };
                println!(
                    "{cmd} {installed} {} {}",
                    tool.source,
                    tool.version.as_deref().unwrap_or("<latest>")
                );
            }
        }
        Scope::Global => {
            let paths = ScopePaths::global(binpm_home()?);
            for (cmd, record) in list_package_records(&paths)? {
                println!(
                    "{cmd} installed {} {} {}",
                    record.source, record.release_tag, record.installed_path
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
            println!("selected_asset: {}", selection.selected.asset_name);
            println!("selected_asset_url: {}", selection.selected.canonical_url);
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
    let record = install_resolved(
        &scope_paths,
        &cache_paths,
        &cmd,
        &spec,
        None,
        require_verified,
    )?;
    if let Err(error) = write_package_record(&scope_paths, &cmd, &record) {
        rollback_failed_install(&scope_paths, &cmd, &record, &cache_paths, None)?;
        restore_runtime_tool_state(&scope_paths, &cmd, prior_state);
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
    install_local_tool(&root, &cmd, &spec, None, frozen_lockfile, require_verified)?;
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
    for (cmd, tool) in manifest.tools {
        if !selected.is_empty() && !selected.contains(&cmd) {
            continue;
        }
        validate_command_name(&cmd)?;
        let mut spec = parse_manifest_source(&tool.source)?;
        spec.version = tool.version.clone();
        install_local_tool(
            &root,
            &cmd,
            &spec,
            Some(&tool),
            frozen_lockfile,
            require_verified,
        )?;
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
) -> Result<PackageRecord> {
    validate_command_name(cmd)?;
    let lockfile_path = root.join(LOCKFILE_FILE);
    if frozen_lockfile {
        return install_local_from_lock(root, cmd, spec, tool, require_verified);
    }

    let home = binpm_home()?;
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let cache_paths = CachePaths::new(&home);
    let prior_state = capture_local_tool_state(root, cmd)?;
    let record = install_resolved(
        &scope_paths,
        &cache_paths,
        cmd,
        spec,
        tool,
        require_verified,
    )?;

    let mut lockfile = read_lockfile(&lockfile_path)?;
    let target_key = HostTarget {
        os: record.target_os,
        arch: record.target_arch,
        libc: record.target_libc,
    }
    .key();
    let tool = lockfile
        .tools
        .entry(cmd.to_string())
        .or_insert_with(|| LockTool {
            source: record.source.clone(),
            targets: BTreeMap::new(),
        });
    tool.source = record.source.clone();
    let mut lock_record = record.lock_record();
    lock_record.installed_path = deterministic_installed_path(cmd, record.target_os);
    tool.targets.insert(target_key, lock_record);
    if let Err(error) = write_lockfile(&lockfile_path, &lockfile)
        .and_then(|_| write_package_record(&scope_paths, cmd, &record))
        .and_then(|_| write_cache_ref(&cache_paths, root, cmd, &record))
    {
        rollback_local_install_state(root, cmd, &record, prior_state);
        return Err(error);
    }
    println!("installed {cmd} {}", record.installed_path);
    Ok(record)
}

fn install_resolved(
    scope_paths: &ScopePaths,
    cache_paths: &CachePaths,
    cmd: &str,
    spec: &SourceSpec,
    tool: Option<&ManifestTool>,
    require_verified: bool,
) -> Result<PackageRecord> {
    validate_command_name(cmd)?;
    scope_paths.ensure()?;
    cache_paths.ensure()?;
    let resolved = resolve_asset(spec, tool)?;
    if require_verified
        && !(resolved.checksum_source.is_upstream_verified()
            || (resolved.checksum_source == ChecksumSource::Signature
                && resolved.signature_verified))
    {
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
    let bytes = download_asset(&resolved.decision.download_url)?;
    let (sha256, cache_asset) = populate_cache_from_bytes(cache_paths, &resolved, &bytes)?;
    let installed_path = managed_installed_path(scope_paths, cmd, resolved.target.os);
    install_bare_executable(&cache_asset, &installed_path)?;
    package_record_from_resolved(cmd, &resolved, sha256, &cache_asset, &installed_path, true)
}

fn install_local_from_lock(
    root: &Path,
    cmd: &str,
    spec: &SourceSpec,
    tool: Option<&ManifestTool>,
    require_verified: bool,
) -> Result<PackageRecord> {
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
    if record.archive_format != ArchiveFormat::BareExecutable {
        return Err(BinpmError::ArchiveExtractionNotImplemented {
            asset: record.asset_name,
        });
    }

    let home = binpm_home()?;
    let cache_paths = CachePaths::new(&home);
    cache_paths.ensure()?;
    let cache_asset = cache_paths.asset_path(&record.sha256);
    if cache_asset.exists() && crate::storage::verify_sha256(&cache_asset, &record.sha256).is_err()
    {
        remove_path_if_exists(&cache_asset)?;
    }
    if !cache_asset.exists() {
        let bytes = download_asset(&record.asset_url)?;
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
                download_url: record.asset_url.clone(),
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
            checksum_source: record.checksum_source,
            signature_available: record.signature_available,
            signature_verified: record.signature_verified,
        };
        populate_cache_from_bytes(&cache_paths, &resolved, &bytes)?;
    }

    let scope_paths = ScopePaths::local(root.to_path_buf());
    let installed_path = managed_installed_path(&scope_paths, cmd, target.os);
    install_bare_executable(&cache_paths.asset_path(&record.sha256), &installed_path)?;
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
    write_package_record(&scope_paths, cmd, &runtime_record)?;
    write_cache_ref(&cache_paths, root, cmd, &runtime_record)?;
    println!("installed {cmd} {}", runtime_record.installed_path);
    Ok(runtime_record)
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
    Ok(())
}

fn assert_lock_matches_manifest_tool(
    root: &Path,
    cmd: &str,
    tool: Option<&ManifestTool>,
    target: &HostTarget,
    record: &PackageRecord,
) -> Result<()> {
    let Some(tool) = tool else {
        return Ok(());
    };
    if let Some(override_target) = tool.targets.get(&target.key()) {
        if record.asset_name != override_target.asset
            || record.selected_binary != override_target.bin
            || override_target
                .checksum_source
                .is_some_and(|checksum_source| record.checksum_source != checksum_source)
        {
            return Err(BinpmError::StaleLockfile {
                path: root.join(LOCKFILE_FILE),
                cmd: cmd.to_string(),
            });
        }
        return Ok(());
    }
    if let Some(bin) = &tool.bin {
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
    let selected_binary = match tool.and_then(|tool| {
        tool.targets
            .get(&target.key())
            .map(|override_target| override_target.bin.as_str())
            .or(tool.bin.as_deref())
    }) {
        Some(bin) => bin.to_string(),
        None => decision.asset_name.clone(),
    };
    let checksum_source = manifest_checksum_source(tool, &target)?;
    Ok(ResolvedAsset {
        source: spec.clone(),
        release_tag: release.tag,
        target,
        decision,
        archive_format,
        selected_binary,
        checksum_source,
        signature_available: false,
        signature_verified: false,
    })
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

fn manifest_checksum_source(
    tool: Option<&ManifestTool>,
    target: &HostTarget,
) -> Result<ChecksumSource> {
    if let Some(checksum_source) = tool.and_then(|tool| {
        tool.targets
            .get(&target.key())
            .and_then(|override_target| override_target.checksum_source)
    }) {
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
    if let Some(override_target) = tool.and_then(|tool| tool.targets.get(&target_key)) {
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
            return Err(BinpmError::UnsafeUrl {
                url: asset
                    .provider_url
                    .as_deref()
                    .unwrap_or(&asset.url)
                    .to_string(),
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

    select_asset(spec.provider, target, assets)
        .map(|selection| selection.selected)
        .ok_or_else(|| BinpmError::AssetNotFound {
            package: spec.to_string(),
            target: target_key,
        })
}

fn rollback_failed_install(
    scope_paths: &ScopePaths,
    cmd: &str,
    record: &PackageRecord,
    cache_paths: &CachePaths,
    local_root: Option<&Path>,
) -> Result<()> {
    remove_installed_binary(scope_paths, cmd, record)?;
    let Some(cache_key) = &record.cache_key else {
        return Ok(());
    };
    let home = cache_paths
        .root
        .parent()
        .map(Path::to_path_buf)
        .ok_or(BinpmError::MissingGlobalHome)?;
    let global_paths = ScopePaths::global(home);
    let local_paths = local_root.map(|root| ScopePaths::local(root.to_path_buf()));
    let referenced = referenced_cache_keys(&global_paths, local_paths.as_ref(), cache_paths)?;
    if !referenced.contains(cache_key) {
        remove_path_if_exists(&cache_paths.entry_dir(&record.sha256))?;
    }
    Ok(())
}

#[derive(Debug, Clone)]
struct LocalToolState {
    lockfile: crate::storage::Lockfile,
    runtime: RuntimeToolState,
}

fn capture_local_tool_state(root: &Path, cmd: &str) -> Result<LocalToolState> {
    let scope_paths = ScopePaths::local(root.to_path_buf());
    Ok(LocalToolState {
        lockfile: read_lockfile(&root.join(LOCKFILE_FILE))?,
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
        .and_then(|record| fs::read(&record.installed_path).ok());
    Ok(RuntimeToolState {
        package_record,
        installed_bytes,
    })
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
    let _ = write_lockfile(&root.join(LOCKFILE_FILE), &prior_state.lockfile);
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
                let _ = install_bare_executable(Path::new(cache_path), &previous_path);
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
    sanitize_persisted_url(url)?;
    info!(
        asset_url = url.split(['?', '#']).next().unwrap_or(url),
        "Downloading release asset"
    );
    let response = reqwest::blocking::Client::builder()
        .user_agent(concat!("binpm/", env!("CARGO_PKG_VERSION")))
        .build()
        .map_err(BinpmError::ReleaseHttpClient)?
        .get(url)
        .send()
        .map_err(BinpmError::ReleaseLookup)?
        .error_for_status()
        .map_err(BinpmError::ReleaseLookup)?;
    sanitize_persisted_url(response.url().as_str())?;
    response
        .bytes()
        .map(|bytes| bytes.to_vec())
        .map_err(BinpmError::ReleaseLookup)
}

fn remove_local_tool(cmd: &str) -> Result<i32> {
    let root = require_manifest_root()?;
    validate_command_name(cmd)?;
    let manifest_path = root.join(MANIFEST_FILE);
    let lockfile_path = root.join(LOCKFILE_FILE);
    let paths = ScopePaths::local(root.clone());
    let record_path = package_record_path(&paths, cmd);
    if record_path.exists() {
        let record = read_package_record(&record_path)?;
        remove_installed_binary(&paths, cmd, &record)?;
    }
    remove_package_record(&paths, cmd)?;
    remove_cache_ref(&CachePaths::new(&binpm_home()?), &root, cmd)?;
    remove_path_if_exists(&paths.bin.join(cmd))?;
    remove_path_if_exists(&paths.bin.join(format!("{cmd}.exe")))?;

    let mut manifest = read_manifest(&manifest_path)?;
    manifest.tools.remove(cmd);
    write_manifest(&manifest_path, &manifest)?;

    let mut lockfile = read_lockfile(&lockfile_path)?;
    lockfile.tools.remove(cmd);
    write_lockfile(&lockfile_path, &lockfile)?;
    println!("removed {cmd}");
    Ok(0)
}

fn remove_global_tool(cmd: &str) -> Result<i32> {
    validate_command_name(cmd)?;
    let paths = ScopePaths::global(binpm_home()?);
    let record_path = package_record_path(&paths, cmd);
    if record_path.exists() {
        let record = read_package_record(&record_path)?;
        remove_installed_binary(&paths, cmd, &record)?;
    }
    remove_package_record(&paths, cmd)?;
    remove_path_if_exists(&paths.bin.join(cmd))?;
    remove_path_if_exists(&paths.bin.join(format!("{cmd}.exe")))?;
    println!("removed {cmd}");
    Ok(0)
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
    let root = match scope {
        Scope::Local => Some(require_manifest_root()?),
        Scope::Global => None,
        Scope::Auto => unreachable!("select_scope never returns auto"),
    };
    let paths = match scope {
        Scope::Local => ScopePaths::local(root.clone().expect("local root is set")),
        Scope::Global => ScopePaths::global(binpm_home()?),
        Scope::Auto => unreachable!("select_scope never returns auto"),
    };
    let mut checked = 0usize;
    let mut locked = BTreeSet::new();
    if let Some(root) = &root {
        let lockfile = read_lockfile(&root.join(LOCKFILE_FILE))?;
        let target_key = HostTarget::current()?.key();
        for (cmd, tool) in lockfile.tools {
            validate_command_name(&cmd)?;
            if let Some(record) = tool.targets.get(&target_key) {
                if args.require_verified && !record.has_verified_source() {
                    return Err(BinpmError::VerificationRequired {
                        package: record.package_spec.clone(),
                    });
                }
                locked.insert(cmd.clone());
                println!("{cmd} lock verified {}", record.checksum_source.as_str());
                checked += 1;
            }
        }
    }
    for (cmd, record) in list_package_records(&paths)? {
        validate_command_name(&cmd)?;
        if args.require_verified && !record.has_verified_source() {
            return Err(BinpmError::VerificationRequired {
                package: record.package_spec,
            });
        }
        if let Some(cache_path) = &record.cache_path {
            let cache_path = Path::new(cache_path);
            if cache_path.exists() {
                crate::storage::verify_sha256(cache_path, &record.sha256)?;
            }
        }
        crate::storage::verify_sha256(Path::new(&record.installed_path), &record.sha256)?;
        println!("{cmd} verified {}", record.checksum_source.as_str());
        if !locked.contains(&cmd) {
            checked += 1;
        }
    }
    println!("checked {checked}");
    Ok(0)
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
        path::{Path, PathBuf},
        str::FromStr,
    };

    use super::{
        assert_lock_matches_manifest_tool, assert_lock_record_matches_source_and_target,
        binpm_home_from_values, deterministic_installed_path, lockfile_digest,
        manifest_checksum_source, manifest_creation_root_from, parse_manifest_source,
        project_root_from, restore_runtime_tool_state, select_manifest_asset, shell_path,
        shell_quote, RuntimeToolState,
    };
    use crate::{
        cli::Shell,
        contract::{
            ArchiveFormat, ChecksumSource, HostTarget, SourceProvider, SourceSpec, TargetArch,
            TargetLibc, TargetOs,
        },
        release::ReleaseAsset,
        storage::{ManifestTargetOverride, ManifestTool, PackageRecord},
    };

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
    fn manifest_source_rejects_embedded_version() {
        let error = parse_manifest_source("github:owner/tool@1.0.0")
            .expect_err("versioned manifest source");

        assert!(error.to_string().contains("must be versionless"));
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
            url: "https://gitlab.com/owner/tool/-/releases/v1/downloads/tool-linux".to_string(),
            provider_url: None,
            source_archive: false,
            final_url_https: Some(false),
        }];

        let error =
            select_manifest_asset(&spec, Some(&tool), &target, &assets).expect_err("unsafe URL");

        assert!(error.to_string().contains("not HTTPS eligible"));
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
            sha256: "abc123".to_string(),
            checksum_source: ChecksumSource::Local,
            installed_at: None,
            signature_available: false,
            signature_verified: false,
        }
    }
}
