use std::{
    collections::BTreeMap,
    fs,
    path::{Path, PathBuf},
    str::FromStr,
};

use sha2::{Digest, Sha256};
use tracing::{debug, info};

use crate::{
    assets::{select_asset, ArtifactKind},
    cli::{
        AddArgs, CacheCommand, Cli, Command, EnvArgs, ExecArgs, ExplainArgs, InfoArgs, InitArgs,
        InstallArgs, RemoveArgs, ScopedArgs, Shell, UpdateArgs, VerifyArgs,
    },
    contract::{ArchiveFormat, ChecksumSource, HostTarget, Scope, SourceSpec},
    error::{BinpmError, Result},
    release::{client_for_source, GitHubReleaseClient, GitLabReleaseClient},
    storage::{
        archive_format, clean_cache, install_bare_executable, list_package_records,
        package_record_from_resolved, package_record_path, populate_cache_from_bytes, prune_cache,
        read_cache_records, read_lockfile, read_manifest, read_package_record,
        referenced_cache_keys, remove_installed_binary, remove_package_record,
        remove_path_if_exists, write_lockfile, write_manifest, write_package_record, CachePaths,
        LockTool, Manifest, ManifestTool, PackageRecord, ResolvedAsset, ScopePaths, LOCKFILE_FILE,
        MANIFEST_FILE,
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
        let selected_scope = select_scope(scope)?;
        if selected_scope == Scope::Local {
            return install_local_source(spec, frozen_lockfile, args.require_verified);
        }
        return install_global_source(spec, args.require_verified);
    } else {
        info!(
            command = "install",
            scope = scope.as_str(),
            frozen_lockfile,
            require_verified = args.require_verified,
            no_confirm = args.no_confirm,
            "Prepared local manifest sync request"
        );
        return install_local_manifest(frozen_lockfile, args.require_verified);
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
    let mut manifest = if manifest_path.exists() {
        read_manifest(&manifest_path)?
    } else {
        Manifest {
            version: 1,
            tools: BTreeMap::new(),
        }
    };
    manifest.tools.insert(
        args.cmd.clone(),
        ManifestTool {
            source: spec.source_without_version(),
            version: spec.version.clone(),
            bin: None,
            targets: BTreeMap::new(),
        },
    );
    write_manifest(&manifest_path, &manifest)?;
    install_local_tool(
        &root,
        &args.cmd,
        &spec,
        args.lockfile.frozen_lockfile(),
        args.require_verified,
    )?;
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
            let referenced = referenced_cache_keys(&global_paths, local_paths.as_ref())?;
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
    info!(
        command = "update",
        selected_scope = args.scope.scope().as_str(),
        selected_count = args.cmd.len(),
        frozen_lockfile = args.lockfile.frozen_lockfile(),
        require_verified = args.require_verified,
        no_confirm = args.no_confirm,
        "Prepared update request"
    );
    match select_scope(args.scope.scope())? {
        Scope::Local => {
            install_local_manifest(args.lockfile.frozen_lockfile(), args.require_verified)
        }
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
    let record = install_resolved(&scope_paths, &cache_paths, &cmd, &spec, require_verified)?;
    write_package_record(&scope_paths, &cmd, &record)?;
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
    install_local_tool(&root, &cmd, &spec, frozen_lockfile, require_verified)?;
    Ok(0)
}

fn install_local_manifest(frozen_lockfile: bool, require_verified: bool) -> Result<i32> {
    let root = require_manifest_root()?;
    let manifest = read_manifest(&root.join(MANIFEST_FILE))?;
    for (cmd, tool) in manifest.tools {
        let mut spec = SourceSpec::from_str(&tool.source)?;
        spec.version = tool.version;
        install_local_tool(&root, &cmd, &spec, frozen_lockfile, require_verified)?;
    }
    Ok(0)
}

fn install_local_tool(
    root: &Path,
    cmd: &str,
    spec: &SourceSpec,
    frozen_lockfile: bool,
    require_verified: bool,
) -> Result<PackageRecord> {
    let lockfile_path = root.join(LOCKFILE_FILE);
    if frozen_lockfile {
        return install_local_from_lock(root, cmd, require_verified);
    }

    let home = binpm_home()?;
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let cache_paths = CachePaths::new(&home);
    let record = install_resolved(&scope_paths, &cache_paths, cmd, spec, require_verified)?;

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
    let lock_record = record.lock_record();
    tool.targets.insert(target_key, lock_record);
    write_lockfile(&lockfile_path, &lockfile)?;
    write_package_record(&scope_paths, cmd, &record)?;
    println!("installed {cmd} {}", record.installed_path);
    Ok(record)
}

fn install_resolved(
    scope_paths: &ScopePaths,
    cache_paths: &CachePaths,
    cmd: &str,
    spec: &SourceSpec,
    require_verified: bool,
) -> Result<PackageRecord> {
    scope_paths.ensure()?;
    cache_paths.ensure()?;
    let resolved = resolve_asset(spec)?;
    if require_verified
        && !(resolved.checksum_source.is_upstream_verified()
            || (resolved.checksum_source == ChecksumSource::Signature
                && resolved.signature_verified))
    {
        return Err(BinpmError::VerificationRequired {
            package: spec.to_string(),
        });
    }
    if resolved.archive_format != ArchiveFormat::BareExecutable {
        return Err(BinpmError::ArchiveExtractionNotImplemented {
            asset: resolved.decision.asset_name,
        });
    }
    let bytes = download_asset(&resolved.decision.canonical_url)?;
    let (sha256, cache_asset) = populate_cache_from_bytes(cache_paths, &resolved, &bytes)?;
    let installed_path = scope_paths.bin.join(cmd);
    install_bare_executable(&cache_asset, &installed_path)?;
    package_record_from_resolved(cmd, &resolved, sha256, &cache_asset, &installed_path, true)
}

fn install_local_from_lock(
    root: &Path,
    cmd: &str,
    require_verified: bool,
) -> Result<PackageRecord> {
    let lockfile_path = root.join(LOCKFILE_FILE);
    let lockfile = read_lockfile(&lockfile_path)?;
    let target = HostTarget::current()?;
    let record = lockfile
        .tools
        .get(cmd)
        .and_then(|tool| tool.targets.get(&target.key()))
        .cloned()
        .ok_or(BinpmError::FrozenLockfile {
            path: lockfile_path.clone(),
        })?;
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
    if cache_asset.exists() {
        crate::storage::verify_sha256(&cache_asset, &record.sha256)?;
    } else {
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
            target,
            decision: crate::assets::CandidateDecision {
                asset_name: record.asset_name.clone(),
                canonical_url: record.asset_url.clone(),
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
    let installed_path = scope_paths.bin.join(cmd);
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
    println!("installed {cmd} {}", runtime_record.installed_path);
    Ok(runtime_record)
}

fn resolve_asset(spec: &SourceSpec) -> Result<ResolvedAsset> {
    let target = HostTarget::current()?;
    let client = client_for_source(spec)?;
    let release = client.resolve_release(spec)?.release;
    let selection = select_asset(spec.provider, &target, &release.assets).ok_or_else(|| {
        BinpmError::AssetNotFound {
            package: spec.to_string(),
            target: target.key(),
        }
    })?;
    let archive_format =
        archive_format(selection.selected.kind).ok_or_else(|| BinpmError::AssetNotFound {
            package: spec.to_string(),
            target: target.key(),
        })?;
    let selected_binary = match selection.selected.kind {
        ArtifactKind::BareExecutable => selection.selected.asset_name.clone(),
        ArtifactKind::Archive(_) => selection.selected.asset_name.clone(),
        _ => selection.selected.asset_name.clone(),
    };
    Ok(ResolvedAsset {
        source: spec.clone(),
        release_tag: release.tag,
        target,
        decision: selection.selected,
        archive_format,
        selected_binary,
        checksum_source: ChecksumSource::Local,
        signature_available: false,
        signature_verified: false,
    })
}

fn download_asset(url: &str) -> Result<Vec<u8>> {
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
    response
        .bytes()
        .map(|bytes| bytes.to_vec())
        .map_err(BinpmError::ReleaseLookup)
}

fn remove_local_tool(cmd: &str) -> Result<i32> {
    let root = require_manifest_root()?;
    let manifest_path = root.join(MANIFEST_FILE);
    let lockfile_path = root.join(LOCKFILE_FILE);
    let mut manifest = read_manifest(&manifest_path)?;
    manifest.tools.remove(cmd);
    write_manifest(&manifest_path, &manifest)?;

    let mut lockfile = read_lockfile(&lockfile_path)?;
    lockfile.tools.remove(cmd);
    write_lockfile(&lockfile_path, &lockfile)?;

    let paths = ScopePaths::local(root);
    let record_path = package_record_path(&paths, cmd);
    if record_path.exists() {
        let record = read_package_record(&record_path)?;
        remove_installed_binary(&record)?;
    }
    remove_package_record(&paths, cmd)?;
    remove_path_if_exists(&paths.bin.join(cmd))?;
    println!("removed {cmd}");
    Ok(0)
}

fn remove_global_tool(cmd: &str) -> Result<i32> {
    let paths = ScopePaths::global(binpm_home()?);
    let record_path = package_record_path(&paths, cmd);
    if record_path.exists() {
        let record = read_package_record(&record_path)?;
        remove_installed_binary(&record)?;
    }
    remove_package_record(&paths, cmd)?;
    remove_path_if_exists(&paths.bin.join(cmd))?;
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
    let paths = match scope {
        Scope::Local => ScopePaths::local(require_manifest_root()?),
        Scope::Global => ScopePaths::global(binpm_home()?),
        Scope::Auto => unreachable!("select_scope never returns auto"),
    };
    let mut checked = 0usize;
    for (cmd, record) in list_package_records(&paths)? {
        if args.require_verified && !record.has_verified_source() {
            return Err(BinpmError::VerificationRequired {
                package: record.package_spec,
            });
        }
        if let Some(cache_path) = &record.cache_path {
            crate::storage::verify_sha256(Path::new(cache_path), &record.sha256)?;
        }
        println!("{cmd} verified {}", record.checksum_source.as_str());
        checked += 1;
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
    use std::path::{Path, PathBuf};

    use super::{
        binpm_home_from_values, lockfile_digest, manifest_creation_root_from, project_root_from,
        shell_path, shell_quote,
    };
    use crate::cli::Shell;

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
}
