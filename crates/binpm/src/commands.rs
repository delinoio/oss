use std::{
    collections::{BTreeMap, BTreeSet},
    env, fs,
    io::{Cursor, ErrorKind, IsTerminal, Read, Write},
    path::{Path, PathBuf},
    process::Command as ProcessCommand,
    str::FromStr,
    sync::atomic::{AtomicU64, Ordering},
    thread,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

use serde::Serialize;
use sha2::{Digest, Sha256};
use tracing::{debug, info, warn};

use crate::{
    assets::{
        discover_archive_binary, gitlab_https_diagnostic_url, gitlab_https_eligible,
        gitlab_https_rejection_reason, select_asset, target_archive_candidates, ArchiveMember,
        ArtifactKind, BinaryDiscovery, CandidateDecision,
    },
    cli::{
        AddArgs, CacheCommand, Cli, Command, EnvArgs, ExecArgs, ExplainArgs, InfoArgs, InitArgs,
        InstallArgs, RemoveArgs, ScopedArgs, Shell, UpdateArgs, VerifyArgs,
    },
    contract::{
        normalize_source_input, validate_version_selector, ArchiveFormat, ChecksumSource,
        HostTarget, Scope, SourceProvider, SourceSpec, TargetArch, TargetLibc, TargetOs,
        VerificationState,
    },
    error::{BinpmError, Result},
    release::{
        client_for_source, provider_auth_for_source, GitHubReleaseClient, GitLabReleaseClient,
        ProviderAuth, ReleaseAsset, ReleaseClient, GITHUB_ASSET_DOWNLOAD_ACCEPT,
    },
    storage::{
        archive_format, cache_asset_is_verified_regular, cache_ref_path, clean_cache,
        deterministic_installed_path, ensure_dir, install_bare_executable,
        install_executable_bytes, installed_filename, list_package_records, managed_installed_path,
        package_record_from_resolved, package_record_path, populate_cache_from_bytes, prune_cache,
        read_cache_records, read_lockfile, read_manifest, read_package_record,
        record_verified_cache_hit, referenced_cache_keys, reject_symlinked_cache_entry,
        reject_symlinked_package_record_dirs, remove_cache_ref, remove_installed_binary,
        remove_package_record, remove_path_if_exists, remove_stale_cache_refs,
        require_regular_managed_file, require_verified_regular_cache_asset, sanitize_persisted_url,
        scan_cache_references, validate_command_name, validate_download_url,
        validate_installed_binary_path, validate_sha256_digest, write_cache_ref, write_lockfile,
        write_manifest, write_package_record, CachePaths, LockTool, Manifest, ManifestTool,
        PackageRecord, ResolvedAsset, ScopePaths, SignatureSidecar, LOCKFILE_FILE, MANIFEST_FILE,
    },
};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum OutputMode {
    Human,
    Json,
}

impl OutputMode {
    fn from_json_flag(json: bool) -> Self {
        if json {
            Self::Json
        } else {
            Self::Human
        }
    }

    fn is_json(self) -> bool {
        self == Self::Json
    }
}

#[derive(Debug, Serialize)]
struct ListOutput {
    command: &'static str,
    scope: Scope,
    tools: Vec<ListToolOutput>,
}

#[derive(Debug, Serialize)]
struct ListToolOutput {
    cmd: String,
    state: ToolState,
    source: String,
    requested_version: Option<String>,
    release_tag: Option<String>,
    selected_binary: Option<String>,
    installed_path: Option<String>,
    verification: Option<VerificationState>,
    #[serde(skip_serializing_if = "Option::is_none")]
    next_step: Option<String>,
}

#[derive(Debug, Clone, Copy, Serialize)]
enum ToolState {
    #[serde(rename = "declared")]
    Declared,
    #[serde(rename = "installed")]
    Installed,
}

#[derive(Debug, Serialize)]
struct CacheListOutput {
    command: &'static str,
    entries: Vec<CacheEntryOutput>,
}

#[derive(Debug, Serialize)]
struct CachePruneOutput {
    command: &'static str,
    removed_cache_entries: usize,
    removed_stale_local_project_cache_refs: usize,
    preserved_legacy_cache_refs: usize,
    removed_boundary: String,
    preserved_boundaries: CachePreservedBoundariesOutput,
    migration_hint: String,
}

#[derive(Debug, Serialize)]
struct CacheCleanOutput {
    command: &'static str,
    removed_cache_entries: usize,
    removed_boundary: String,
    preserved_boundaries: CachePreservedBoundariesOutput,
}

#[derive(Debug, Serialize)]
struct CachePreservedBoundariesOutput {
    cache_refs: String,
    package_records: String,
    executables: String,
}

#[derive(Debug, Serialize)]
struct CacheEntryOutput {
    cache_key: String,
    byte_size: Option<u64>,
    source_provider: crate::contract::SourceProvider,
    source_host: String,
    source_path: String,
    release_tag: String,
    asset_name: String,
    checksum_source: ChecksumSource,
    last_used_at: Option<String>,
    reference_state: CacheReferenceState,
}

#[derive(Debug, Clone, Copy, Serialize)]
enum CacheReferenceState {
    #[serde(rename = "referenced")]
    Referenced,
    #[serde(rename = "unreferenced")]
    Unreferenced,
}

#[derive(Debug, Serialize)]
struct OutdatedOutput {
    command: &'static str,
    scope: Scope,
    checked: usize,
    tools: Vec<OutdatedToolOutput>,
}

#[derive(Debug, Serialize)]
struct OutdatedToolOutput {
    cmd: String,
    source: String,
    current: String,
    latest: String,
    outdated: bool,
}

#[derive(Debug, Serialize)]
struct DoctorOutput {
    command: &'static str,
    project_root: String,
    manifest_path: String,
    manifest: PathState,
    lockfile_path: String,
    lockfile: PathState,
    local_bin: String,
    local_bin_on_path: bool,
    global_home: String,
    global_bin: String,
    global_bin_on_path: bool,
    stale_cache_refs: usize,
    legacy_cache_refs: usize,
    declared_only_tools: Vec<DeclaredOnlyToolOutput>,
    declared_only_next_step: Option<&'static str>,
}

#[derive(Debug, Clone, Serialize)]
struct DeclaredOnlyToolOutput {
    cmd: String,
    source: String,
    requested_version: Option<String>,
    expected_executable_path: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
enum PathState {
    #[serde(rename = "present")]
    Present,
    #[serde(rename = "missing")]
    Missing,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
enum CacheKeyStatus {
    #[serde(rename = "lockfile-backed")]
    LockfileBacked,
    #[serde(rename = "missing-lockfile")]
    MissingLockfile,
}

#[derive(Debug, Serialize)]
struct VerifyOutput {
    command: &'static str,
    scope: Scope,
    require_verified: bool,
    checked: usize,
    checks: Vec<VerifyCheckOutput>,
}

#[derive(Debug, Serialize)]
struct VerifyCheckOutput {
    cmd: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    target: Option<HostTarget>,
    checksum_source: ChecksumSource,
    verification: VerificationState,
}

#[derive(Debug, Serialize)]
struct CacheKeyOutput {
    command: &'static str,
    status: CacheKeyStatus,
    cache_key: String,
    target: HostTarget,
    target_key: String,
    lockfile_path: String,
    lockfile: PathState,
    lockfile_digest: String,
    recommended_next_command: Option<&'static str>,
    read_only: bool,
}

#[derive(Debug, Serialize)]
#[serde(tag = "kind", rename_all = "kebab-case")]
enum InfoOutput {
    Source {
        command: &'static str,
        source: String,
        normalized_source: String,
        provider: crate::contract::SourceProvider,
        host: String,
        path: String,
        release: String,
        target: HostTarget,
        selected_asset: Option<SelectedAssetOutput>,
    },
    Package {
        command: &'static str,
        scope: Scope,
        cmd: String,
        record: PackageRecordOutput,
    },
}

#[derive(Debug, Serialize)]
struct SelectedAssetOutput {
    asset_name: String,
    asset_url: String,
    archive_format: Option<ArchiveFormat>,
    score: Option<i32>,
}

#[derive(Debug, Serialize)]
#[serde(tag = "kind", rename_all = "kebab-case")]
enum ExplainOutput {
    Source {
        command: &'static str,
        read_only: bool,
        network_free: bool,
        source: String,
        normalized_source: String,
        provider: crate::contract::SourceProvider,
        host: String,
        path: String,
        requested_version: Option<String>,
        target: HostTarget,
        release_api: String,
        release: String,
        release_decision: String,
        #[serde(skip_serializing_if = "Vec::is_empty")]
        skipped_releases: Vec<SkippedReleaseOutput>,
        selected_asset: Option<SelectedAssetOutput>,
        candidates: Vec<CandidateOutput>,
        #[serde(skip_serializing_if = "Vec::is_empty")]
        release_diagnostics: Vec<ReleaseDiagnosticOutput>,
    },
    Package {
        command: &'static str,
        read_only: bool,
        network_free: bool,
        scope: Scope,
        cmd: String,
        record: PackageRecordOutput,
        override_snippet: String,
    },
}

#[derive(Debug, Serialize)]
struct SkippedReleaseOutput {
    tag: String,
    reason: String,
}

#[derive(Debug, Serialize)]
struct CandidateOutput {
    asset_name: String,
    kind: String,
    archive_format: Option<ArchiveFormat>,
    detected_os: Option<TargetOs>,
    detected_arch: Option<TargetArch>,
    detected_libc: Option<TargetLibc>,
    cpu_feature: Option<crate::assets::CpuFeatureVariant>,
    score: Option<i32>,
    eligible: bool,
    recognized_pattern: bool,
    rejection_reason: Option<String>,
}

#[derive(Debug, Serialize)]
struct ReleaseDiagnosticOutput {
    kind: ReleaseDiagnosticKind,
    target: HostTarget,
    message: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    unsupported_installers: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    source_archives: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    sidecar_assets: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    target_mismatches: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    remediation: Option<String>,
}

#[derive(Debug, Clone, Copy, Serialize)]
enum ReleaseDiagnosticKind {
    #[serde(rename = "no-downloadable-assets")]
    NoDownloadableAssets,
    #[serde(rename = "source-archive-only")]
    SourceArchiveOnly,
    #[serde(rename = "unsupported-installers")]
    UnsupportedInstallers,
    #[serde(rename = "target-mismatch")]
    TargetMismatch,
    #[serde(rename = "target-scoring-remediation")]
    TargetScoringRemediation,
    #[serde(rename = "gitlab-https-rejection")]
    GitLabHttpsRejection,
}

#[derive(Debug, Serialize)]
struct PackageRecordOutput {
    package_spec: String,
    source: String,
    source_provider: crate::contract::SourceProvider,
    source_host: String,
    source_path: String,
    requested_version: Option<String>,
    release_tag: String,
    asset_name: String,
    asset_url: String,
    target: HostTarget,
    archive_format: ArchiveFormat,
    selected_binary: String,
    installed_path: String,
    cache_key: Option<String>,
    cache_path: Option<String>,
    sha256: String,
    checksum_source: ChecksumSource,
    verification: VerificationState,
    signature_available: bool,
    signature_verified: bool,
}

#[derive(Debug, Serialize)]
struct UpdatePlanOutput {
    command: &'static str,
    scope: Scope,
    dry_run: bool,
    frozen_lockfile: bool,
    selected_all_tools: bool,
    planned_updates: Vec<UpdatePlannedToolOutput>,
    file_changes: Vec<String>,
    runtime_changes: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    no_op: Option<UpdateNoOpOutput>,
}

#[derive(Debug, Serialize)]
struct UpdatePlannedToolOutput {
    cmd: String,
    source: String,
    version: String,
}

#[derive(Debug, Serialize)]
struct UpdateNoOpOutput {
    reason: &'static str,
    declared_tools: usize,
    lockfile_created: bool,
    message: &'static str,
}

struct UpdatePlan {
    planned_updates: Vec<UpdatePlannedToolOutput>,
    file_changes: Vec<String>,
    runtime_changes: Vec<String>,
    no_op: Option<UpdateNoOpOutput>,
}

const DOWNLOAD_RETRY_ATTEMPTS: usize = 3;
const DOWNLOAD_RETRY_BASE_DELAY: Duration = Duration::from_millis(200);
const DOWNLOAD_PROGRESS_THRESHOLD_BYTES: u64 = 5 * 1024 * 1024;
const DOWNLOAD_PROGRESS_STEP_BYTES: u64 = 5 * 1024 * 1024;
const DOWNLOAD_PROGRESS_INTERVAL: Duration = Duration::from_secs(2);
const DOWNLOAD_INITIAL_CAPACITY_LIMIT: usize = 8 * 1024 * 1024;
const GITHUB_ACTIONS_OIDC_ISSUER: &str = "https://token.actions.githubusercontent.com";
static SIGSTORE_TEMP_ATTEMPT: AtomicU64 = AtomicU64::new(0);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum EnvPathScope {
    Both,
    Global,
    Local,
}

pub fn run(cli: Cli) -> Result<i32> {
    let output = OutputMode::from_json_flag(cli.json);
    match cli.command {
        Command::Install(args) => install(args, output),
        Command::Add(args) => add(args, output),
        Command::Exec(args) => exec(args, output),
        Command::Cache(args) => cache(args.command, output),
        Command::List(args) => list(args, output),
        Command::Remove(args) => remove(args),
        Command::Info(args) => info_cmd(args, output),
        Command::Outdated(args) => outdated(args, output),
        Command::Update(args) => update(args, output),
        Command::Doctor => doctor(output),
        Command::Explain(args) => explain(args, output),
        Command::Verify(args) => verify(args, output),
        Command::Init(args) => init(args),
        Command::Env(args) => env_cmd(args),
    }
}

fn install(args: InstallArgs, output: OutputMode) -> Result<i32> {
    let requested_scope = args.scope.scope();
    let frozen_lockfile = args.lockfile.frozen_lockfile();
    let explicit_bin = normalize_bin_selection(args.bin.as_deref())?;

    if let Some(source) = &args.source {
        let spec = normalize_source_input(source)?;
        if requested_scope == Scope::Local {
            return Err(BinpmError::UnsupportedLocalSourceInstall {
                package_source: spec.to_string(),
            });
        }
        let scope = Scope::Global;
        let alias = args
            .alias
            .clone()
            .unwrap_or_else(|| repo_name(&spec).to_string());
        validate_command_name(&alias)?;
        info!(
            command = "install",
            scope = scope.as_str(),
            install_alias = alias,
            selected_bin = explicit_bin.as_deref().unwrap_or(""),
            frozen_lockfile,
            require_verified = args.require_verified,
            no_confirm = args.no_confirm,
            source_provider = spec.provider.as_str(),
            source_host = spec.host,
            source_path = spec.path,
            source_version = spec.version.as_deref().unwrap_or(""),
            "Prepared source install request"
        );
        if !output.is_json() {
            print_global_source_install_scope_feedback(&spec)?;
        }
        install_global_source(spec, &alias, explicit_bin, args.require_verified)
    } else {
        if args.alias.is_some() || explicit_bin.is_some() {
            return Err(BinpmError::InvalidSourceSpec {
                raw: "install".to_string(),
                message: "`--as` and `--bin` require an explicit source".to_string(),
            });
        }
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
        let root = require_manifest_root()?;
        if !output.is_json() {
            print_local_install_scope_feedback(&root);
        }
        install_local_manifest_at(root, frozen_lockfile, args.require_verified, &[], output)
    }
}

fn print_global_source_install_scope_feedback(spec: &SourceSpec) -> Result<()> {
    println!("install scope: global");
    println!("install mode: global source install");
    println!("source: {spec}");
    match find_manifest_root(&current_dir()?) {
        Some(root) => {
            println!(
                "project manifest detected: {}",
                root.join(MANIFEST_FILE).display()
            );
            println!(
                "project manifest: not modified; use `binpm add <cmd> {}` for project-local \
                 declaration",
                cli_quote(&spec.to_string())
            );
        }
        None => {
            println!("project manifest: not found; installing to user-global binpm home");
        }
    }
    Ok(())
}

fn print_local_install_scope_feedback(root: &Path) {
    println!("install scope: local");
    println!("install mode: local manifest sync");
    println!("manifest: {}", root.join(MANIFEST_FILE).display());
    println!("local bin: {}", root.join(".binpm").join("bin").display());
}

fn add(args: AddArgs, output: OutputMode) -> Result<i32> {
    let spec = normalize_source_input(&args.source)?;
    let explicit_bin = normalize_bin_selection(args.bin.as_deref())?;
    let additional = parse_additional_declarations(&args.also)?;
    let mut declarations = Vec::with_capacity(1 + additional.len());
    declarations.push(AddDeclaration {
        cmd: args.cmd.clone(),
        bin: explicit_bin.clone(),
    });
    declarations.extend(additional);
    info!(
        command = "add",
        local_cmd = args.cmd,
        selected_bin = explicit_bin.as_deref().unwrap_or(""),
        declaration_count = declarations.len(),
        manifest_only = args.manifest_only,
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
    let mut declared = BTreeSet::new();
    for declaration in &declarations {
        validate_command_name(&declaration.cmd)?;
        if !declared.insert(declaration.cmd.clone()) {
            return Err(BinpmError::DuplicateAddDeclaration {
                cmd: declaration.cmd.clone(),
            });
        }
    }
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
    let current_target = if args.manifest_only {
        None
    } else {
        Some(HostTarget::current()?)
    };
    let mut selected = Vec::with_capacity(declarations.len());
    for declaration in &declarations {
        let manifest_tool = manifest.tools.get(&declaration.cmd).cloned();
        let next_manifest_tool = update_manifest_tool_source(
            manifest_tool,
            &spec,
            declaration.bin.clone(),
            current_target.as_ref(),
        );
        manifest
            .tools
            .insert(declaration.cmd.clone(), next_manifest_tool);
        selected.push(declaration.cmd.clone());
    }
    ensure_no_selected_install_path_collisions(&manifest, &selected)?;
    if args.manifest_only {
        write_manifest(&manifest_path, &manifest)?;
        let scope_paths = ScopePaths::local(root.clone());
        println!("declared {}", selected.join(", "));
        println!("manifest-only: wrote {}", manifest_path.display());
        println!(
            "manifest-only: did not update {}",
            root.join(LOCKFILE_FILE).display()
        );
        for cmd in &selected {
            println!(
                "manifest-only: did not install executable {}",
                current_platform_installed_path(&scope_paths, cmd).display()
            );
        }
        println!("next: run `binpm install` to resolve, lock, and install declared tools");
        return Ok(0);
    }
    let mut completed = Vec::new();
    for cmd in &selected {
        let tool = manifest
            .tools
            .get(cmd)
            .expect("selected command was inserted into manifest")
            .clone();
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
        let install = match install_local_tool(
            &root,
            cmd,
            &spec,
            Some(&tool),
            args.lockfile.frozen_lockfile(),
            args.require_verified,
            output,
        ) {
            Ok(install) => install,
            Err(error) => {
                rollback_completed_local_installs(
                    &root,
                    completed,
                    &CachePaths::new(&binpm_home()?),
                )?;
                return Err(error);
            }
        };
        completed.push(CompletedLocalInstall {
            cmd: cmd.clone(),
            install,
            prior_state,
        });
    }
    if let Err(error) = write_manifest(&manifest_path, &manifest) {
        rollback_completed_local_installs(&root, completed, &CachePaths::new(&binpm_home()?))?;
        return Err(error);
    }
    let cache_paths = CachePaths::new(&binpm_home()?);
    if let Err(error) = completed
        .iter()
        .try_for_each(|completed| commit_deferred_cache_hit(&cache_paths, &completed.install))
    {
        let rollback_error =
            rollback_completed_local_installs_ref(&root, &completed, &cache_paths).err();
        if manifest_existed {
            let _ = write_manifest(&manifest_path, &prior_manifest);
        } else {
            let _ = remove_path_if_exists(&manifest_path);
        }
        if let Some(rollback_error) = rollback_error {
            return Err(rollback_error);
        }
        return Err(error);
    }
    println!("added {}", selected.join(", "));
    for cmd in &selected {
        println!("run: binpm x {cmd}");
    }
    println!(
        "path: use `binpm env --local --shell <bash|zsh|fish|powershell>` for opt-in direct shell \
         access"
    );
    Ok(0)
}

fn exec(args: ExecArgs, output: OutputMode) -> Result<i32> {
    let explicit_bin = normalize_bin_selection(args.bin.as_deref())?;
    let cmd = match args.cmd() {
        Some(cmd) => {
            if args.package.is_some() && cmd.to_string_lossy().starts_with('-') {
                return Err(BinpmError::AmbiguousPackageShortcutArgs);
            }
            cmd.to_string_lossy().to_string()
        }
        None if args.package.is_some() => {
            if !args.args().is_empty() {
                return Err(BinpmError::AmbiguousPackageShortcutArgs);
            }
            package_shortcut_command(args.package.as_deref(), explicit_bin.as_deref())?
        }
        None => return Err(BinpmError::InvalidCommandName { cmd: String::new() }),
    };
    validate_command_name(&cmd)?;
    let forwarded_arg_count = args.args().len();

    if let Some(source) = &args.package {
        let spec = normalize_source_input(source)?;
        info!(
            command = "x",
            resolved_command = %cmd,
            explicit_package = true,
            selected_bin = explicit_bin.as_deref().unwrap_or(""),
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
            Sha256::digest(
                format!("{source}:{cmd}:{}", explicit_bin.as_deref().unwrap_or("")).as_bytes()
            )
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
            bin: explicit_bin.clone(),
            targets: BTreeMap::new(),
        };
        let install = install_resolved(
            &scope_paths,
            &cache_paths,
            &cmd,
            &spec,
            explicit_bin.as_ref().map(|_| &tool),
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
    let spec = parse_manifest_tool_source(&tool)?;
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
        output,
    )?;
    let cache_paths = CachePaths::new(&binpm_home()?);
    if let Err(error) = commit_deferred_cache_hit(&cache_paths, &install) {
        rollback_local_install_state(&root, &cmd, &install.record, prior_state);
        cleanup_failed_install_cache(&cache_paths, &install.record.sha256, Some(&root), &install)?;
        return Err(error);
    }
    execute_command(&cmd, args.args(), &[ScopePaths::local(root).bin])
}

fn package_shortcut_command(source: Option<&str>, explicit_bin: Option<&str>) -> Result<String> {
    if let Some(bin) = explicit_bin {
        let basename = bin.rsplit('/').next().unwrap_or(bin);
        validate_command_name(basename)?;
        return Ok(basename.to_string());
    }
    let source = source.ok_or_else(|| BinpmError::InvalidCommandName { cmd: String::new() })?;
    let spec = normalize_source_input(source)?;
    let cmd = repo_name(&spec).to_string();
    validate_command_name(&cmd)?;
    Ok(cmd)
}

fn cache(command: CacheCommand, output: OutputMode) -> Result<i32> {
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
            let mut entries = Vec::new();
            for record in read_cache_records(&paths)? {
                let reference_state = if referenced.contains(&record.cache_key) {
                    CacheReferenceState::Referenced
                } else {
                    CacheReferenceState::Unreferenced
                };
                if output.is_json() {
                    entries.push(CacheEntryOutput {
                        cache_key: record.cache_key,
                        byte_size: record.byte_size,
                        source_provider: record.source_provider,
                        source_host: record.source_host,
                        source_path: record.source_path,
                        release_tag: record.release_tag,
                        asset_name: record.asset_name,
                        checksum_source: record.checksum_source,
                        last_used_at: record.last_used_at,
                        reference_state,
                    });
                } else {
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
                        match reference_state {
                            CacheReferenceState::Referenced => "referenced",
                            CacheReferenceState::Unreferenced => "unreferenced",
                        }
                    );
                }
            }
            if output.is_json() {
                return print_json(&CacheListOutput {
                    command: "cache list",
                    entries,
                });
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
            let scan = scan_cache_references(&cache_paths)?;
            let stale_refs_removed = remove_stale_cache_refs(&cache_paths, &scan.stale_refs)?;
            let legacy_refs = scan.legacy_refs;
            let referenced =
                referenced_cache_keys(&global_paths, local_paths.as_ref(), &cache_paths)?;
            let removed = prune_cache(&cache_paths, &referenced)?;
            let preserved_boundaries = cache_preserved_boundaries(&cache_paths);
            if output.is_json() {
                return print_json(&CachePruneOutput {
                    command: "cache prune",
                    removed_cache_entries: removed,
                    removed_stale_local_project_cache_refs: stale_refs_removed,
                    preserved_legacy_cache_refs: legacy_refs,
                    removed_boundary: cache_paths.root.join("sha256").display().to_string(),
                    preserved_boundaries,
                    migration_hint: legacy_cache_ref_migration_hint().to_string(),
                });
            }
            println!("pruned cache entries: {removed}");
            println!("removed stale local-project cache refs: {stale_refs_removed}");
            println!("preserved legacy cache refs: {legacy_refs}");
            println!(
                "removed boundary: {}",
                cache_paths.root.join("sha256").display()
            );
            println!("preserved: {}", preserved_boundaries.cache_refs);
            println!("preserved: {}", preserved_boundaries.package_records);
            println!("preserved: {}", preserved_boundaries.executables);
            println!("{}", legacy_cache_ref_migration_hint());
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
            let preserved_boundaries = cache_preserved_boundaries(&paths);
            if output.is_json() {
                return print_json(&CacheCleanOutput {
                    command: "cache clean",
                    removed_cache_entries: removed,
                    removed_boundary: paths.root.join("sha256").display().to_string(),
                    preserved_boundaries,
                });
            }
            println!("removed cache entries: {removed}");
            println!("removed boundary: {}", paths.root.join("sha256").display());
            println!("preserved: {}", preserved_boundaries.cache_refs);
            println!("preserved: {}", preserved_boundaries.package_records);
            println!("preserved: {}", preserved_boundaries.executables);
            Ok(0)
        }
        CacheCommand::Key => cache_key(output),
    }
}

fn cache_preserved_boundaries(paths: &CachePaths) -> CachePreservedBoundariesOutput {
    CachePreservedBoundariesOutput {
        cache_refs: paths.refs.display().to_string(),
        package_records: paths.home.join("packages").display().to_string(),
        executables: paths.home.join("bin").display().to_string(),
    }
}

fn legacy_cache_ref_migration_hint() -> &'static str {
    "legacy cache refs are preserved; run local install/update/remove in those projects to rewrite \
     them as structured refs"
}

fn cache_key(output: OutputMode) -> Result<i32> {
    let project_root = project_root()?;
    let lockfile_path = project_root.join(LOCKFILE_FILE);
    let target = HostTarget::current()?;
    let lockfile = json_path_state(&lockfile_path);
    let status = if lockfile == PathState::Missing {
        CacheKeyStatus::MissingLockfile
    } else {
        CacheKeyStatus::LockfileBacked
    };
    let recommended_next_command = if status == CacheKeyStatus::MissingLockfile {
        Some("binpm install")
    } else {
        None
    };
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
    if output.is_json() {
        return print_json(&CacheKeyOutput {
            command: "cache key",
            status,
            cache_key,
            target,
            target_key,
            lockfile_path: lockfile_path.display().to_string(),
            lockfile,
            lockfile_digest: digest,
            recommended_next_command,
            read_only: true,
        });
    }
    if lockfile == PathState::Missing {
        eprintln!(
            "warning: {} is missing; cache key uses the empty lockfile digest",
            lockfile_path.display()
        );
        println!("missing-lockfile cache key: {cache_key}");
        println!(
            "next command: {}",
            recommended_next_command.unwrap_or("binpm install")
        );
        return Ok(0);
    }
    println!("{cache_key}");
    Ok(0)
}

fn list(args: ScopedArgs, output: OutputMode) -> Result<i32> {
    let scope = select_scope(args.scope.scope())?;
    log_read_only_scope("list", scope);
    if !output.is_json() {
        println!("list scope: {}", scope.as_str());
    }
    let mut tools = Vec::new();
    match scope {
        Scope::Local => {
            let root = require_manifest_root()?;
            let manifest = read_manifest(&root.join(MANIFEST_FILE))?;
            let paths = ScopePaths::local(root.clone());
            let mut printed = BTreeSet::new();
            for (cmd, tool) in manifest.tools {
                validate_command_name(&cmd)?;
                printed.insert(cmd.clone());
                let runtime_record = local_runtime_tool_record(&root, &cmd)?;
                let has_current_manifest_record = match runtime_record.as_ref() {
                    Some(record) => match local_runtime_record_matches_manifest(&tool, record) {
                        Ok(matches) => matches,
                        Err(BinpmError::InvalidSourceSpec { .. }) => false,
                        Err(error) => return Err(error),
                    },
                    None => false,
                };
                if let Some(record) = runtime_record.filter(|_| has_current_manifest_record) {
                    let row = list_installed_tool(cmd, record);
                    print_list_tool(&row, output);
                    tools.push(row);
                } else {
                    let row = list_declared_tool(cmd, tool);
                    print_list_tool(&row, output);
                    tools.push(row);
                }
            }
            for (cmd, record) in list_package_records(&paths)? {
                if printed.contains(&cmd) {
                    continue;
                }
                let row = list_installed_tool(cmd, record);
                print_list_tool(&row, output);
                tools.push(row);
            }
        }
        Scope::Global => {
            let paths = ScopePaths::global(binpm_home()?);
            for (cmd, record) in list_package_records(&paths)? {
                let row = list_installed_tool(cmd, record);
                print_list_tool(&row, output);
                tools.push(row);
            }
        }
        Scope::Auto => unreachable!("select_scope never returns auto"),
    }
    if output.is_json() {
        return print_json(&ListOutput {
            command: "list",
            scope,
            tools,
        });
    }
    Ok(0)
}

fn remove(args: RemoveArgs) -> Result<i32> {
    info!(
        command = "remove",
        selected_scope = args.scope.scope().as_str(),
        local_cmd = args.cmd,
        dry_run = args.dry_run,
        no_confirm = args.no_confirm,
        "Prepared remove request"
    );
    let scope = select_scope(args.scope.scope())?;
    print_selected_mutation_scope("remove", scope);
    if args.dry_run {
        return preview_remove(scope, &args.cmd);
    }
    match scope {
        Scope::Local => remove_local_tool(&args.cmd),
        Scope::Global => remove_global_tool(&args.cmd),
        Scope::Auto => unreachable!("select_scope never returns auto"),
    }
}

fn info_cmd(args: InfoArgs, output: OutputMode) -> Result<i32> {
    if let Some(spec) = parse_source_argument(&args.cmd_or_source)? {
        debug!(
            command = "info",
            source_provider = spec.provider.as_str(),
            source_host = spec.host,
            source_path = spec.path,
            source_version = spec.version.as_deref().unwrap_or(""),
            "Parsed info argument as source"
        );
        log_read_only_scope("info", args.scope.scope());
        return print_source_info(&spec, output);
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
    if output.is_json() {
        return print_json(&InfoOutput::Package {
            command: "info",
            scope,
            cmd: args.cmd_or_source,
            record: package_record_output(&record)?,
        });
    }
    println!("info scope: {}", scope.as_str());
    print_package_record_info(&args.cmd_or_source, &record);
    Ok(0)
}

fn outdated(args: ScopedArgs, output: OutputMode) -> Result<i32> {
    let scope = select_scope(args.scope.scope())?;
    log_read_only_scope("outdated", scope);
    if !output.is_json() {
        println!("outdated scope: {}", scope.as_str());
    }
    let mut checked = 0usize;
    let mut tools = Vec::new();
    match scope {
        Scope::Local => {
            let root = require_manifest_root()?;
            let manifest = read_manifest(&root.join(MANIFEST_FILE))?;
            let lockfile = read_lockfile(&root.join(LOCKFILE_FILE))?;
            let target_key = HostTarget::current()?.key();
            for (cmd, tool) in manifest.tools {
                validate_command_name(&cmd)?;
                let spec = parse_manifest_tool_source(&tool)?;
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
                let is_outdated = current != latest;
                let source = spec.source_without_version();
                if !output.is_json() && current != latest {
                    println!(
                        "{}",
                        format_outdated_tool_line(&cmd, &current, &latest, &source)
                    );
                }
                tools.push(OutdatedToolOutput {
                    cmd,
                    source,
                    current,
                    latest,
                    outdated: is_outdated,
                });
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
                let is_outdated = record.release_tag != latest;
                if !output.is_json() && record.release_tag != latest {
                    println!(
                        "{}",
                        format_outdated_tool_line(
                            &cmd,
                            &record.release_tag,
                            &latest,
                            &record.source
                        )
                    );
                }
                tools.push(OutdatedToolOutput {
                    cmd,
                    source: record.source,
                    current: record.release_tag,
                    latest,
                    outdated: is_outdated,
                });
                checked += 1;
            }
        }
        Scope::Auto => unreachable!("select_scope never returns auto"),
    }
    if output.is_json() {
        return print_json(&OutdatedOutput {
            command: "outdated",
            scope,
            checked,
            tools,
        });
    }
    println!("checked {checked}");
    Ok(0)
}

fn format_outdated_tool_line(cmd: &str, current: &str, latest: &str, source: &str) -> String {
    format!("{cmd} {current} -> {latest} ({source})")
}

fn update(args: UpdateArgs, output: OutputMode) -> Result<i32> {
    let frozen_lockfile = args.lockfile.frozen_lockfile();
    info!(
        command = "update",
        selected_scope = args.scope.scope().as_str(),
        selected_count = args.cmd.len(),
        frozen_lockfile,
        require_verified = args.require_verified,
        dry_run = args.dry_run,
        no_confirm = args.no_confirm,
        "Prepared update request"
    );
    let scope = select_scope(args.scope.scope())?;
    if output.is_json() && args.dry_run {
        return preview_update_json(scope, &args.cmd, frozen_lockfile);
    }
    if output.is_json() && scope == Scope::Local {
        let plan = build_local_update_plan(&args.cmd)?;
        if plan.no_op.is_some() {
            return print_update_plan_json(scope, &args.cmd, frozen_lockfile, false, plan);
        }
    }
    print_selected_mutation_scope("update", scope);
    print_update_mode(scope, &args.cmd);
    if args.dry_run {
        return preview_update(scope, &args.cmd);
    }
    print_update_plan(scope, &args.cmd)?;
    match scope {
        Scope::Local => {
            update_local_manifest(frozen_lockfile, args.require_verified, &args.cmd, output)
        }
        Scope::Global => update_global_packages(args.require_verified, &args.cmd),
        Scope::Auto => unreachable!("select_scope never returns auto"),
    }
}

fn print_selected_mutation_scope(command: &str, scope: Scope) {
    println!("{command} scope: {}", scope.as_str());
}

fn print_update_mode(scope: Scope, selected: &[String]) {
    if selected.is_empty() {
        println!("update mode: all tools in {} scope", scope.as_str());
    } else {
        println!("update mode: selected tools ({})", selected.len());
    }
}

fn preview_remove(scope: Scope, cmd: &str) -> Result<i32> {
    validate_command_name(cmd)?;
    match scope {
        Scope::Local => {
            let root = require_manifest_root()?;
            let manifest_path = root.join(MANIFEST_FILE);
            let manifest = read_manifest(&manifest_path)?;
            let prior_state = capture_local_remove_state(&root, cmd)?;
            if !manifest.tools.contains_key(cmd)
                && !has_local_runtime_or_lock_state(cmd, &prior_state)
            {
                return Err(BinpmError::MissingTool {
                    cmd: cmd.to_string(),
                    manifest: manifest_path,
                });
            }
            println!("would remove {cmd} from local scope");
            println!("would update {}", root.join(MANIFEST_FILE).display());
            println!("would update {}", root.join(LOCKFILE_FILE).display());
            println!("would clean {}", ScopePaths::local(root).root.display());
        }
        Scope::Global => {
            let paths = ScopePaths::global(binpm_home()?);
            read_package_record(&package_record_path(&paths, cmd))?;
            println!("would remove {cmd} from global scope");
            println!("would update {}", paths.packages.display());
            println!("would update {}", paths.bin.display());
        }
        Scope::Auto => unreachable!("select_scope never returns auto"),
    }
    println!("dry run: no changes made");
    Ok(0)
}

fn preview_update(scope: Scope, selected: &[String]) -> Result<i32> {
    print_update_plan(scope, selected)?;
    println!("dry run: no changes made");
    Ok(0)
}

fn preview_update_json(scope: Scope, selected: &[String], frozen_lockfile: bool) -> Result<i32> {
    let plan = match scope {
        Scope::Local => {
            let plan = build_local_update_plan(selected)?;
            if frozen_lockfile {
                validate_frozen_local_update_dry_run(selected)?;
            }
            plan
        }
        Scope::Global => build_global_update_plan(selected)?,
        Scope::Auto => unreachable!("select_scope never returns auto"),
    };
    print_update_plan_json(scope, selected, frozen_lockfile, true, plan)
}

fn print_update_plan_json(
    scope: Scope,
    selected: &[String],
    frozen_lockfile: bool,
    dry_run: bool,
    plan: UpdatePlan,
) -> Result<i32> {
    print_json(&UpdatePlanOutput {
        command: "update",
        scope,
        dry_run,
        frozen_lockfile,
        selected_all_tools: selected.is_empty(),
        planned_updates: plan.planned_updates,
        file_changes: plan.file_changes,
        runtime_changes: plan.runtime_changes,
        no_op: plan.no_op,
    })
}

fn print_update_plan(scope: Scope, selected: &[String]) -> Result<()> {
    match scope {
        Scope::Local => print_local_update_plan(selected),
        Scope::Global => print_global_update_plan(selected),
        Scope::Auto => unreachable!("select_scope never returns auto"),
    }
}

fn print_local_update_plan(selected: &[String]) -> Result<()> {
    let plan = build_local_update_plan(selected)?;
    print_update_plan_details(&plan);
    Ok(())
}

fn validate_frozen_local_update_dry_run(selected: &[String]) -> Result<()> {
    let root = require_manifest_root()?;
    let manifest = read_manifest(&root.join(MANIFEST_FILE))?;
    validate_frozen_local_update_latest(&root, &manifest, selected)?;
    if selected.is_empty() {
        let lockfile_path = root.join(LOCKFILE_FILE);
        let lockfile = read_lockfile(&lockfile_path)?;
        if !local_manifest_orphan_cmds(&root, &lockfile, &manifest.tools)?.is_empty() {
            return Err(BinpmError::FrozenLockfileOrphanCleanup {
                path: lockfile_path,
            });
        }
    }
    Ok(())
}

fn build_local_update_plan(selected: &[String]) -> Result<UpdatePlan> {
    let root = require_manifest_root()?;
    let manifest_path = root.join(MANIFEST_FILE);
    let manifest = read_manifest(&manifest_path)?;
    for cmd in selected {
        validate_command_name(cmd)?;
        if !manifest.tools.contains_key(cmd) {
            return Err(BinpmError::MissingTool {
                cmd: cmd.clone(),
                manifest: manifest_path.clone(),
            });
        }
    }
    validate_selected_manifest_entries(&manifest, selected)?;
    ensure_no_selected_install_path_collisions(&manifest, selected)?;

    let planned: Vec<_> = manifest
        .tools
        .iter()
        .filter(|(cmd, _)| selected.is_empty() || selected.contains(cmd))
        .collect();
    let manifest_can_change = planned.iter().any(|(_, tool)| tool.version.is_some());
    let planned_updates = planned
        .into_iter()
        .map(|(cmd, tool)| UpdatePlannedToolOutput {
            cmd: cmd.clone(),
            source: tool.source.clone(),
            version: tool.version.as_deref().unwrap_or("<latest>").to_string(),
        })
        .collect();
    let lockfile = read_lockfile(&root.join(LOCKFILE_FILE))?;
    let orphan_cmds = local_manifest_orphan_cmds(&root, &lockfile, &manifest.tools)?;
    if manifest.tools.is_empty() && orphan_cmds.is_empty() {
        return Ok(UpdatePlan {
            planned_updates,
            file_changes: Vec::new(),
            runtime_changes: Vec::new(),
            no_op: Some(UpdateNoOpOutput {
                reason: "empty_manifest_no_tools_no_lockfile_changes",
                declared_tools: 0,
                lockfile_created: false,
                message: "empty manifest declares no tools; no lockfile was created",
            }),
        });
    }
    let mut file_changes = Vec::new();
    if manifest_can_change {
        file_changes.push(root.join(MANIFEST_FILE).display().to_string());
    }
    file_changes.push(root.join(LOCKFILE_FILE).display().to_string());
    let scope_paths = ScopePaths::local(root.clone());
    let mut runtime_changes = vec![scope_paths.bin.display().to_string()];
    if !orphan_cmds.is_empty() {
        let cache_paths = CachePaths::new(&binpm_home()?);
        for cmd in orphan_cmds {
            runtime_changes.push(
                package_record_path(&scope_paths, &cmd)
                    .display()
                    .to_string(),
            );
            runtime_changes.push(
                cache_ref_path(&cache_paths, &root, &cmd)
                    .display()
                    .to_string(),
            );
        }
    }
    Ok(UpdatePlan {
        planned_updates,
        file_changes,
        runtime_changes,
        no_op: None,
    })
}

fn print_global_update_plan(selected: &[String]) -> Result<()> {
    let plan = build_global_update_plan(selected)?;
    print_update_plan_details(&plan);
    Ok(())
}

fn build_global_update_plan(selected: &[String]) -> Result<UpdatePlan> {
    let paths = ScopePaths::global(binpm_home()?);
    let planned = selected_global_package_records(&paths, selected)?;
    prepare_global_updates(planned.clone())?;
    let planned_updates = planned
        .into_iter()
        .map(|(cmd, record)| UpdatePlannedToolOutput {
            cmd,
            source: record.source,
            version: record.release_tag,
        })
        .collect();
    Ok(UpdatePlan {
        planned_updates,
        file_changes: vec![paths.packages.display().to_string()],
        runtime_changes: vec![paths.bin.display().to_string()],
        no_op: None,
    })
}

fn print_update_plan_details(plan: &UpdatePlan) {
    println!("planned updates: {}", plan.planned_updates.len());
    for update in &plan.planned_updates {
        println!(
            "would update {} from {} {}",
            update.cmd, update.source, update.version
        );
    }
    if let Some(no_op) = &plan.no_op {
        println!("{}", no_op.message);
        return;
    }
    for path in &plan.file_changes {
        println!("would update {path}");
    }
    for path in &plan.runtime_changes {
        println!("would update {path}");
    }
}

fn doctor(output: OutputMode) -> Result<i32> {
    let project_root = project_root()?;
    let manifest_path = project_root.join(MANIFEST_FILE);
    let lockfile_path = project_root.join(LOCKFILE_FILE);
    let local_bin = project_root.join(".binpm").join("bin");
    let home = binpm_home()?;
    let global_bin = home.join("bin");
    let local_bin_on_path = path_contains_entry(&local_bin);
    let global_bin_on_path = path_contains_entry(&global_bin);
    let cache_paths = CachePaths::new(&home);
    let cache_ref_scan = scan_cache_references(&cache_paths)?;
    let declared_only_tools = declared_only_local_tools(&project_root)?;

    info!(
        command = "doctor",
        read_only = true,
        project_root = %project_root.display(),
        manifest_path = %manifest_path.display(),
        lockfile_path = %lockfile_path.display(),
        binpm_home = %home.display(),
        local_bin = %local_bin.display(),
        local_bin_on_path,
        global_bin = %global_bin.display(),
        global_bin_on_path,
        stale_cache_refs = cache_ref_scan.stale_count(),
        legacy_cache_refs = cache_ref_scan.legacy_refs,
        declared_only_tools = declared_only_tools.len(),
        "Prepared doctor inspection"
    );
    let declared_only_next_step = if declared_only_tools.is_empty() {
        None
    } else {
        Some("binpm install")
    };
    if output.is_json() {
        return print_json(&DoctorOutput {
            command: "doctor",
            project_root: project_root.display().to_string(),
            manifest_path: manifest_path.display().to_string(),
            manifest: json_path_state(&manifest_path),
            lockfile_path: lockfile_path.display().to_string(),
            lockfile: json_path_state(&lockfile_path),
            local_bin: local_bin.display().to_string(),
            local_bin_on_path,
            global_home: home.display().to_string(),
            global_bin: global_bin.display().to_string(),
            global_bin_on_path,
            stale_cache_refs: cache_ref_scan.stale_count(),
            legacy_cache_refs: cache_ref_scan.legacy_refs,
            declared_only_tools,
            declared_only_next_step,
        });
    }
    println!("binpm doctor");
    println!("manifest: {}", path_state(&manifest_path));
    println!("lockfile: {}", path_state(&lockfile_path));
    println!("local_bin: {}", local_bin.display());
    println!("local_bin_on_path: {}", yes_no(local_bin_on_path));
    println!("global_home: {}", home.display());
    println!("global_bin: {}", global_bin.display());
    println!("global_bin_on_path: {}", yes_no(global_bin_on_path));
    println!("stale_cache_refs: {}", cache_ref_scan.stale_count());
    println!("legacy_cache_refs: {}", cache_ref_scan.legacy_refs);
    println!("declared_only_tools: {}", declared_only_tools.len());
    for tool in &declared_only_tools {
        println!(
            "declared_only_tool: cmd={} state=declared-but-not-installed source={} \
             requested_version={} expected_executable_path={} next=`binpm install --local`",
            tool.cmd,
            tool.source,
            tool.requested_version.as_deref().unwrap_or("<latest>"),
            tool.expected_executable_path
        );
    }
    if declared_only_next_step.is_some() {
        println!("declared_only_next_step: binpm install");
    }
    if !global_bin_on_path {
        print_global_path_setup_guidance(&global_bin);
    }
    Ok(0)
}

fn explain(args: ExplainArgs, output: OutputMode) -> Result<i32> {
    match parse_source_argument(&args.cmd_or_source)? {
        Some(spec) => {
            let target = HostTarget::current()?;
            info!(
                command = "explain",
                read_only = true,
                network_free = false,
                selected_scope = args.scope.scope().as_str(),
                source_provider = spec.provider.as_str(),
                source_host = spec.host,
                source_path = spec.path,
                source_version = spec.version.as_deref().unwrap_or(""),
                target = target.key(),
                "Prepared source explanation"
            );
            return explain_source(spec, target, output);
        }
        None => {
            info!(
                command = "explain",
                read_only = true,
                network_free = true,
                selected_scope = args.scope.scope().as_str(),
                local_cmd = args.cmd_or_source,
                "Prepared local command explanation"
            );
        }
    }
    let scope = select_scope(args.scope.scope())?;
    let paths = match scope {
        Scope::Local => ScopePaths::local(require_manifest_root()?),
        Scope::Global => ScopePaths::global(binpm_home()?),
        Scope::Auto => unreachable!("select_scope never returns auto"),
    };
    validate_command_name(&args.cmd_or_source)?;
    let record = read_package_record(&package_record_path(&paths, &args.cmd_or_source))?;
    if output.is_json() {
        let target = HostTarget {
            os: record.target_os,
            arch: record.target_arch,
            libc: record.target_libc,
        };
        let override_snippet = target_override_snippet(
            &args.cmd_or_source,
            &target,
            &record.asset_name,
            &record.selected_binary,
            Some(record.checksum_source),
        );
        return print_json(&ExplainOutput::Package {
            command: "explain",
            read_only: true,
            network_free: true,
            scope,
            cmd: args.cmd_or_source,
            record: package_record_output(&record)?,
            override_snippet,
        });
    }
    println!("binpm explain");
    println!("read_only: true");
    println!("network_free: true");
    println!("explain scope: {}", scope.as_str());
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
    println!("verification: {}", verification_state(&record).as_str());
    println!("override_snippet:");
    println!(
        "{}",
        target_override_snippet(
            &args.cmd_or_source,
            &HostTarget {
                os: record.target_os,
                arch: record.target_arch,
                libc: record.target_libc,
            },
            &record.asset_name,
            &record.selected_binary,
            Some(record.checksum_source),
        )
    );
    Ok(0)
}

fn parse_source_argument(raw: &str) -> Result<Option<SourceSpec>> {
    if raw.starts_with("github:") || raw.starts_with("gitlab:") {
        return SourceSpec::from_str(raw).map(Some);
    }
    if raw.contains(':') {
        return normalize_source_input(raw).map(Some);
    }
    let raw_lower = raw.to_ascii_lowercase();
    if raw_lower.starts_with("https://") || raw_lower.starts_with("http://") {
        return normalize_source_input(raw).map(Some);
    }
    let path = raw.split_once('@').map_or(raw, |(path, _)| path);
    if path.split('/').count() == 2 && path.split('/').all(|segment| !segment.is_empty()) {
        return normalize_source_input(raw).map(Some);
    }

    Ok(None)
}

fn explain_source(spec: SourceSpec, target: HostTarget, output: OutputMode) -> Result<i32> {
    let client = client_for_source(&spec)?;
    let selection = client.resolve_release(&spec)?;
    let asset_selection = select_asset(spec.provider, &target, &selection.release.assets);
    let all_decisions = match &asset_selection {
        Some(selection) => selection.decisions.clone(),
        None => crate::assets::score_assets(spec.provider, &target, &selection.release.assets),
    };

    if output.is_json() {
        let release_api = release_api_url(&spec);
        return print_json(&ExplainOutput::Source {
            command: "explain",
            read_only: true,
            network_free: false,
            source: spec.to_string(),
            normalized_source: spec.source_without_version(),
            provider: spec.provider,
            host: spec.host.clone(),
            path: spec.path.clone(),
            requested_version: spec.version.clone(),
            target: target.clone(),
            release_api,
            release: selection.release.tag,
            release_decision: selection.decision,
            skipped_releases: selection
                .skipped
                .into_iter()
                .map(|skipped| SkippedReleaseOutput {
                    tag: skipped.tag,
                    reason: skipped.reason,
                })
                .collect(),
            selected_asset: asset_selection
                .as_ref()
                .map(|selection| selected_asset_output(&selection.selected))
                .transpose()?,
            candidates: all_decisions.iter().map(candidate_output).collect(),
            release_diagnostics: release_diagnostics(&all_decisions, &target),
        });
    }

    println!("binpm explain");
    println!("read_only: true");
    println!("network_free: false");
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

    println!("release: {}", selection.release.tag);
    println!("release_decision: {}", selection.decision);
    for skipped in &selection.skipped {
        println!("skipped_release: {} ({})", skipped.tag, skipped.reason);
    }

    match asset_selection {
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
            println!("override_snippet_unverified:");
            println!(
                "override_snippet_note: source explain has not downloaded or inspected archive \
                 members; verify the asset and bin values before committing this override"
            );
            println!(
                "{}",
                target_override_snippet(
                    repo_name(&spec),
                    &target,
                    &selection.selected.asset_name,
                    &override_snippet_bin(&spec, &selection.selected),
                    None,
                )
            );
        }
        None => {
            println!("selected_asset: <none>");
            for decision in &all_decisions {
                println!("{}", decision.explain_line());
            }
            for line in release_diagnostic_lines(&all_decisions, &target) {
                println!("{line}");
            }
            if let Some(candidate) = override_snippet_candidate(&all_decisions) {
                println!("override_snippet_unverified:");
                println!(
                    "override_snippet_note: source explain has not downloaded or inspected \
                     archive members; verify compatibility and the bin value before committing \
                     this override"
                );
                println!(
                    "{}",
                    target_override_snippet(
                        repo_name(&spec),
                        &target,
                        &candidate.asset_name,
                        &override_snippet_bin(&spec, candidate),
                        None,
                    )
                );
            }
        }
    }

    Ok(0)
}

fn release_diagnostic_lines(decisions: &[CandidateDecision], target: &HostTarget) -> Vec<String> {
    release_diagnostics(decisions, target)
        .into_iter()
        .flat_map(|diagnostic| {
            let mut lines = vec![format!("diagnostic: {}", diagnostic.message)];
            if !diagnostic.unsupported_installers.is_empty() {
                lines.push(format!(
                    "unsupported_installers: {}",
                    diagnostic.unsupported_installers.join(", ")
                ));
            }
            if !diagnostic.source_archives.is_empty() {
                lines.push(format!(
                    "source_archives: {}",
                    diagnostic.source_archives.join(", ")
                ));
            }
            if !diagnostic.sidecar_assets.is_empty() {
                lines.push(format!(
                    "sidecar_assets: {}",
                    diagnostic.sidecar_assets.join(", ")
                ));
            }
            if !diagnostic.target_mismatches.is_empty() {
                lines.push(format!(
                    "target_mismatches: {}",
                    diagnostic.target_mismatches.join(", ")
                ));
            }
            if let Some(remediation) = diagnostic.remediation {
                lines.push(format!("remediation: {remediation}"));
            }
            lines
        })
        .collect()
}

fn selection_failure_diagnostics(
    decisions: &[CandidateDecision],
    target: &HostTarget,
) -> Vec<String> {
    release_diagnostics(decisions, target)
        .into_iter()
        .map(|diagnostic| {
            let mut parts = vec![diagnostic.message];
            if !diagnostic.unsupported_installers.is_empty() {
                parts.push(format!(
                    "unsupported installers: {}",
                    diagnostic.unsupported_installers.join(", ")
                ));
            }
            if !diagnostic.source_archives.is_empty() {
                parts.push(format!(
                    "source archives: {}",
                    diagnostic.source_archives.join(", ")
                ));
            }
            if !diagnostic.sidecar_assets.is_empty() {
                parts.push(format!(
                    "sidecar assets: {}",
                    diagnostic.sidecar_assets.join(", ")
                ));
            }
            if !diagnostic.target_mismatches.is_empty() {
                parts.push(format!(
                    "target mismatches: {}",
                    diagnostic.target_mismatches.join(", ")
                ));
            }
            if let Some(remediation) = diagnostic.remediation {
                parts.push(format!("remediation: {remediation}"));
            }
            parts.join("; ")
        })
        .collect()
}

fn release_diagnostics(
    decisions: &[CandidateDecision],
    target: &HostTarget,
) -> Vec<ReleaseDiagnosticOutput> {
    if decisions.is_empty() {
        return vec![ReleaseDiagnosticOutput {
            kind: ReleaseDiagnosticKind::NoDownloadableAssets,
            target: target.clone(),
            message: "release has no downloadable assets for binpm to score".to_string(),
            unsupported_installers: Vec::new(),
            source_archives: Vec::new(),
            sidecar_assets: Vec::new(),
            target_mismatches: Vec::new(),
            remediation: None,
        }];
    }

    let installable_count = decisions
        .iter()
        .filter(|decision| decision.kind.is_installable())
        .count();
    let desktop_packages = decisions
        .iter()
        .filter(|decision| decision.kind == ArtifactKind::DesktopPackage)
        .map(|decision| decision.asset_name.as_str())
        .collect::<Vec<_>>();
    let source_archives = decisions
        .iter()
        .filter(|decision| decision.kind == ArtifactKind::SourceArchive)
        .map(|decision| decision.asset_name.as_str())
        .collect::<Vec<_>>();
    let sidecars = decisions
        .iter()
        .filter(|decision| decision.kind == ArtifactKind::Sidecar)
        .map(|decision| decision.asset_name.as_str())
        .collect::<Vec<_>>();
    let source_archive_only = installable_count == 0
        && !source_archives.is_empty()
        && decisions.iter().all(|decision| {
            matches!(
                decision.kind,
                ArtifactKind::SourceArchive | ArtifactKind::Sidecar
            )
        });

    let mut diagnostics = Vec::new();
    if source_archive_only {
        diagnostics.push(ReleaseDiagnosticOutput {
            kind: ReleaseDiagnosticKind::SourceArchiveOnly,
            target: target.clone(),
            message: format!(
                "release only provides source archives for target {}; binpm installs prebuilt \
                 portable archives or bare executables and does not build from source archives",
                target.key()
            ),
            unsupported_installers: Vec::new(),
            source_archives: source_archives
                .iter()
                .map(|asset_name| (*asset_name).to_string())
                .collect(),
            sidecar_assets: if sidecars.is_empty() {
                Vec::new()
            } else {
                sidecars
                    .iter()
                    .map(|asset_name| (*asset_name).to_string())
                    .collect()
            },
            target_mismatches: Vec::new(),
            remediation: Some(format!(
                "ask upstream to publish a target-specific portable binary archive or bare \
                 executable for {}, for example an asset named with {}, {}, and a compatible libc \
                 signal such as musl, gnu, static, portable, universal, or any",
                target.key(),
                target.os.as_str(),
                target.arch.as_str()
            )),
        });
    }

    if installable_count == 0 && !desktop_packages.is_empty() {
        diagnostics.push(ReleaseDiagnosticOutput {
            kind: ReleaseDiagnosticKind::UnsupportedInstallers,
            target: target.clone(),
            message: format!(
                "release only provides unsupported desktop or system installer packages for \
                 target {}; binpm v1 installs portable archives or bare executables by default",
                target.key()
            ),
            unsupported_installers: desktop_packages
                .iter()
                .map(|asset_name| (*asset_name).to_string())
                .collect(),
            source_archives: Vec::new(),
            sidecar_assets: if sidecars.is_empty() {
                Vec::new()
            } else {
                sidecars
                    .iter()
                    .map(|asset_name| (*asset_name).to_string())
                    .collect()
            },
            target_mismatches: Vec::new(),
            remediation: Some(
                "ask upstream for a portable archive or bare executable asset; installer package \
                 installation is not enabled by default"
                    .to_string(),
            ),
        });
    }

    let musl_missing_libc_assets = decisions
        .iter()
        .filter(|decision| {
            decision.rejection_reason.as_deref().is_some_and(|reason| {
                reason.contains("linux musl target requires an explicit libc signal")
            })
        })
        .map(|decision| decision.asset_name.as_str())
        .collect::<Vec<_>>();
    if !musl_missing_libc_assets.is_empty() {
        diagnostics.push(ReleaseDiagnosticOutput {
            kind: ReleaseDiagnosticKind::TargetScoringRemediation,
            target: target.clone(),
            message: "Linux musl target rejected assets whose names do not include a concrete \
                      libc or portability signal"
                .to_string(),
            unsupported_installers: Vec::new(),
            source_archives: Vec::new(),
            sidecar_assets: Vec::new(),
            target_mismatches: musl_missing_libc_assets
                .iter()
                .map(|asset_name| (*asset_name).to_string())
                .collect(),
            remediation: Some(format!(
                "safe next steps: ask upstream to rename or publish assets with musl, static, \
                 portable, universal, or any; if you control the release, publish a {} asset with \
                 one of those libc signals; otherwise download and inspect the binary outside \
                 binpm with tools such as file and ldd/readelf, then add the generated \
                 [tools.<cmd>.targets.{}] override only after confirming musl or static \
                 compatibility",
                target.key(),
                target.key()
            )),
        });
    }

    let gitlab_https_rejections = decisions
        .iter()
        .filter(|decision| {
            decision.rejection_reason.as_deref().is_some_and(|reason| {
                reason.contains("gitlab asset link URL is not HTTPS")
                    || reason.contains("gitlab direct asset URL is not HTTPS")
                    || reason.contains("gitlab asset redirect target is not HTTPS")
            })
        })
        .map(|decision| decision.asset_name.as_str())
        .collect::<Vec<_>>();
    if !gitlab_https_rejections.is_empty() {
        diagnostics.push(ReleaseDiagnosticOutput {
            kind: ReleaseDiagnosticKind::GitLabHttpsRejection,
            target: target.clone(),
            message: "GitLab release assets were rejected before target scoring because every \
                      download URL and redirect target must use HTTPS"
                .to_string(),
            unsupported_installers: gitlab_https_rejections
                .iter()
                .map(|asset_name| (*asset_name).to_string())
                .collect(),
            source_archives: Vec::new(),
            sidecar_assets: Vec::new(),
            target_mismatches: Vec::new(),
            remediation: Some(
                "configure GitLab release links to use HTTPS URLs and HTTPS redirect targets; \
                 prefer secure direct asset URLs when GitLab exposes them"
                    .to_string(),
            ),
        });
    }

    let has_eligible_installable = decisions
        .iter()
        .any(|decision| decision.kind.is_installable() && decision.eligible);
    if !has_eligible_installable
        && decisions.iter().any(|decision| {
            decision.cpu_feature == Some(crate::assets::CpuFeatureVariant::Modern)
                && decision.rejection_reason.as_deref().is_some_and(|reason| {
                    reason
                        .contains("CPU feature variant `modern` requires explicit host capability")
                })
        })
    {
        diagnostics.push(ReleaseDiagnosticOutput {
            kind: ReleaseDiagnosticKind::TargetScoringRemediation,
            target: target.clone(),
            message: "CPU feature variants were detected; binpm defaults to baseline-compatible \
                      assets because host CPU capability selection is not implemented"
                .to_string(),
            unsupported_installers: Vec::new(),
            source_archives: Vec::new(),
            sidecar_assets: Vec::new(),
            target_mismatches: Vec::new(),
            remediation: Some(
                "publish a baseline asset alongside higher-feature variants, or use an explicit \
                 target override only after verifying host compatibility"
                    .to_string(),
            ),
        });
    }

    let target_mismatches = decisions
        .iter()
        .filter(|decision| {
            decision.kind.is_installable()
                && !decision.eligible
                && decision
                    .rejection_reason
                    .as_deref()
                    .is_some_and(is_target_mismatch_rejection)
        })
        .map(|decision| decision.asset_name.as_str())
        .collect::<Vec<_>>();
    if !has_eligible_installable
        && !target_mismatches.is_empty()
        && musl_missing_libc_assets.is_empty()
    {
        diagnostics.push(ReleaseDiagnosticOutput {
            kind: ReleaseDiagnosticKind::TargetMismatch,
            target: target.clone(),
            message: format!(
                "release has installable assets, but none match target {}",
                target.key()
            ),
            unsupported_installers: Vec::new(),
            source_archives: Vec::new(),
            sidecar_assets: Vec::new(),
            target_mismatches: target_mismatches
                .iter()
                .map(|asset_name| (*asset_name).to_string())
                .collect(),
            remediation: Some(format!(
                "publish an archive or bare executable named for {}; use a target override only \
                 after verifying one of the listed assets is compatible with that target",
                target.key()
            )),
        });
    }

    diagnostics
}

fn is_target_mismatch_rejection(reason: &str) -> bool {
    !reason.contains("linux musl target requires an explicit libc signal")
        && !reason.contains("CPU feature variant `modern` requires explicit host capability")
        && !reason.contains("gitlab asset link URL is not HTTPS")
        && !reason.contains("gitlab direct asset URL is not HTTPS")
        && !reason.contains("gitlab asset redirect target is not HTTPS")
}

fn override_snippet_candidate(decisions: &[CandidateDecision]) -> Option<&CandidateDecision> {
    decisions.iter().find(|decision| {
        decision.kind.is_installable()
            && decision.rejection_reason.as_deref().is_some_and(|reason| {
                reason.contains("linux musl target requires an explicit libc signal")
                    || (decision.score.is_some()
                        && reason.contains(
                            "CPU feature variant `modern` requires explicit host capability",
                        ))
            })
    })
}

fn override_snippet_bin(spec: &SourceSpec, decision: &CandidateDecision) -> String {
    match decision.kind {
        ArtifactKind::BareExecutable => decision.asset_name.clone(),
        ArtifactKind::Archive(_) => repo_name(spec).to_string(),
        _ => repo_name(spec).to_string(),
    }
}

fn target_override_snippet(
    cmd: &str,
    target: &HostTarget,
    asset: &str,
    bin: &str,
    checksum_source: Option<ChecksumSource>,
) -> String {
    let mut snippet = format!(
        "[tools.{}.targets.{}]\nasset = {}\nbin = {}",
        toml_key_segment(cmd),
        target.key(),
        toml_string(asset),
        toml_string(bin)
    );
    if checksum_source == Some(ChecksumSource::GitHubDigest) {
        snippet.push_str(&format!(
            "\nchecksum_source = {}",
            toml_string(ChecksumSource::GitHubDigest.as_str())
        ));
    }
    snippet
}

fn toml_key_segment(key: &str) -> String {
    if key
        .chars()
        .all(|character| character.is_ascii_alphanumeric() || character == '_' || character == '-')
        && !key.is_empty()
    {
        key.to_string()
    } else {
        toml_string(key)
    }
}

fn toml_string(value: &str) -> String {
    let mut escaped = String::from("\"");
    for character in value.chars() {
        match character {
            '\u{08}' => escaped.push_str("\\b"),
            '\t' => escaped.push_str("\\t"),
            '\n' => escaped.push_str("\\n"),
            '\u{0c}' => escaped.push_str("\\f"),
            '\r' => escaped.push_str("\\r"),
            '"' => escaped.push_str("\\\""),
            '\\' => escaped.push_str("\\\\"),
            character if character.is_control() => {
                escaped.push_str(&format!("\\u{:04X}", character as u32));
            }
            character => escaped.push(character),
        }
    }
    escaped.push('"');
    escaped
}

fn print_source_info(spec: &SourceSpec, output: OutputMode) -> Result<i32> {
    let target = HostTarget::current()?;
    let selection = client_for_source(spec)?.resolve_release(spec)?;
    let asset_selection = select_asset(spec.provider, &target, &selection.release.assets);
    if output.is_json() {
        return print_json(&InfoOutput::Source {
            command: "info",
            source: spec.to_string(),
            normalized_source: spec.source_without_version(),
            provider: spec.provider,
            host: spec.host.clone(),
            path: spec.path.clone(),
            release: selection.release.tag,
            target,
            selected_asset: asset_selection
                .as_ref()
                .map(|selection| selected_asset_output(&selection.selected))
                .transpose()?,
        });
    }
    println!("binpm info");
    println!("source: {spec}");
    println!("normalized_source: {}", spec.source_without_version());
    println!("provider: {}", spec.provider.as_str());
    println!("host: {}", spec.host);
    println!("path: {}", spec.path);
    println!("release: {}", selection.release.tag);
    println!("target: {}", target.key());
    match asset_selection {
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
    println!("installed_command_alias: {cmd}");
    println!("source: {}", record.source);
    println!("package_spec: {}", record.package_spec);
    println!("release: {}", record.release_tag);
    println!("selected_asset: {}", record.asset_name);
    println!("upstream_binary: {}", record.selected_binary);
    println!("installed_path: {}", record.installed_path);
    println!("checksum_source: {}", record.checksum_source.as_str());
    println!("verification: {}", verification_state(record).as_str());
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

fn install_global_source(
    spec: SourceSpec,
    cmd: &str,
    explicit_bin: Option<String>,
    require_verified: bool,
) -> Result<i32> {
    validate_command_name(cmd)?;
    let home = binpm_home()?;
    let scope_paths = ScopePaths::global(home.clone());
    let cache_paths = CachePaths::new(&home);
    let prior_state = capture_runtime_tool_state(&scope_paths, cmd)?;
    let tool = ManifestTool {
        source: spec.source_without_version(),
        version: spec.version.clone(),
        bin: explicit_bin,
        targets: BTreeMap::new(),
    };
    let install = install_resolved(
        &scope_paths,
        &cache_paths,
        cmd,
        &spec,
        Some(&tool),
        require_verified,
        None,
    )?;
    let record = install.record.clone();
    if let Err(error) = write_package_record(&scope_paths, cmd, &record)
        .and_then(|_| commit_deferred_cache_hit(&cache_paths, &install))
    {
        let rollback_result = rollback_failed_install(&scope_paths, cmd, &record);
        restore_runtime_tool_state(&scope_paths, cmd, prior_state);
        let cache_cleanup_result =
            cleanup_failed_install_cache(&cache_paths, &record.sha256, None, &install);
        rollback_result?;
        cache_cleanup_result?;
        return Err(error);
    }
    print_install_summary(Scope::Global, cmd, &record);
    if !path_contains_entry(&scope_paths.bin) {
        print_global_path_setup_guidance(&scope_paths.bin);
    }
    Ok(0)
}

fn install_local_manifest(
    frozen_lockfile: bool,
    require_verified: bool,
    selected: &[String],
    output: OutputMode,
) -> Result<i32> {
    let root = require_manifest_root()?;
    install_local_manifest_at(root, frozen_lockfile, require_verified, selected, output)
}

fn install_local_manifest_at(
    root: PathBuf,
    frozen_lockfile: bool,
    require_verified: bool,
    selected: &[String],
    output: OutputMode,
) -> Result<i32> {
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
        let spec = parse_manifest_tool_source(tool)?;
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
            output,
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
    let orphan_states = if selected.is_empty() && !frozen_lockfile {
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

fn update_local_manifest(
    frozen_lockfile: bool,
    require_verified: bool,
    selected: &[String],
    output: OutputMode,
) -> Result<i32> {
    let root = require_manifest_root()?;
    let manifest_path = root.join(MANIFEST_FILE);
    let manifest = read_manifest(&manifest_path)?;
    if frozen_lockfile {
        validate_frozen_local_update_latest(&root, &manifest, selected)?;
        return install_local_manifest(frozen_lockfile, require_verified, selected, output);
    }

    let (next_manifest, manifest_changed) =
        local_update_manifest_with_latest_versions(&manifest, selected)?;
    if manifest_changed {
        write_manifest(&manifest_path, &next_manifest)?;
        if let Err(error) =
            install_local_manifest(frozen_lockfile, require_verified, selected, output)
        {
            let _ = write_manifest(&manifest_path, &manifest);
            return Err(error);
        }
        return Ok(0);
    }
    install_local_manifest(frozen_lockfile, require_verified, selected, output)
}

fn local_update_manifest_with_latest_versions(
    manifest: &Manifest,
    selected: &[String],
) -> Result<(Manifest, bool)> {
    local_update_manifest_with_latest_versions_from(
        manifest,
        selected,
        latest_stable_tag_for_update,
    )
}

fn local_update_manifest_with_latest_versions_from(
    manifest: &Manifest,
    selected: &[String],
    latest_tag: impl Fn(&ManifestTool) -> Result<String>,
) -> Result<(Manifest, bool)> {
    validate_selected_manifest_entries(manifest, selected)?;
    let mut next_manifest = manifest.clone();
    let mut changed = false;
    for (cmd, tool) in &manifest.tools {
        if !selected.is_empty() && !selected.contains(cmd) {
            continue;
        }
        if tool.version.is_none() {
            continue;
        }
        let latest = latest_tag(tool)?;
        if tool.version.as_deref() != Some(latest.as_str()) {
            let Some(next_tool) = next_manifest.tools.get_mut(cmd) else {
                continue;
            };
            next_tool.version = Some(latest);
            changed = true;
        }
    }
    Ok((next_manifest, changed))
}

fn latest_stable_tag_for_update(tool: &ManifestTool) -> Result<String> {
    let mut spec = parse_manifest_tool_source(tool)?;
    spec.version = None;
    Ok(client_for_source(&spec)?
        .resolve_release(&spec)?
        .release
        .tag)
}

fn frozen_restore_download_error(
    cmd: &str,
    cache_path: &Path,
    cache_state: &'static str,
    url: &str,
    authenticated: bool,
    source: BinpmError,
) -> BinpmError {
    BinpmError::FrozenRestoreDownload {
        cmd: cmd.to_string(),
        cache_path: cache_path.to_path_buf(),
        cache_state,
        url: sanitize_download_diagnostic_url(url),
        authenticated,
        source: Box::new(source),
    }
}

fn update_global_packages(require_verified: bool, selected: &[String]) -> Result<i32> {
    let home = binpm_home()?;
    let scope_paths = ScopePaths::global(home.clone());
    let records = selected_global_package_records(&scope_paths, selected)?;
    let updates = prepare_global_updates(records)?;
    for update in updates {
        install_global_source(
            update.spec,
            &update.cmd,
            update.selected_binary,
            require_verified,
        )?;
    }
    Ok(0)
}

#[derive(Debug)]
struct PreparedGlobalUpdate {
    cmd: String,
    spec: SourceSpec,
    selected_binary: Option<String>,
}

fn prepare_global_updates(
    records: Vec<(String, PackageRecord)>,
) -> Result<Vec<PreparedGlobalUpdate>> {
    records
        .into_iter()
        .map(|(cmd, record)| {
            validate_command_name(&cmd)?;
            let mut spec = SourceSpec::from_str(&record.source)?;
            spec.version = None;
            let selected_binary = global_update_selected_binary(&record)?;
            Ok(PreparedGlobalUpdate {
                cmd,
                spec,
                selected_binary,
            })
        })
        .collect()
}

fn global_update_selected_binary(record: &PackageRecord) -> Result<Option<String>> {
    if record.archive_format == ArchiveFormat::BareExecutable {
        return Ok(None);
    }
    normalize_bin_selection(Some(&record.selected_binary))
}

fn selected_global_package_records(
    scope_paths: &ScopePaths,
    selected: &[String],
) -> Result<Vec<(String, PackageRecord)>> {
    if !selected.is_empty() {
        reject_symlinked_package_record_dirs(scope_paths)?;
        return selected
            .iter()
            .map(|cmd| {
                validate_command_name(cmd)?;
                let record = read_package_record(&package_record_path(scope_paths, cmd))?;
                Ok((cmd.clone(), record))
            })
            .collect();
    }

    let records = list_package_records(scope_paths)?;
    Ok(records)
}

fn validate_frozen_local_update_latest(
    root: &Path,
    manifest: &Manifest,
    selected: &[String],
) -> Result<()> {
    let lockfile_path = root.join(LOCKFILE_FILE);
    let lockfile = read_lockfile(&lockfile_path)?;
    let target = HostTarget::current()?;
    for (cmd, tool) in &manifest.tools {
        if !selected.is_empty() && !selected.contains(cmd) {
            continue;
        }
        let spec = parse_manifest_tool_source(tool)?;
        let locked_tool = lockfile
            .tools
            .get(cmd)
            .ok_or_else(|| frozen_lockfile_missing_record_error(&lockfile_path, cmd))?;
        let record = locked_tool
            .targets
            .get(&target.key())
            .ok_or_else(|| frozen_lockfile_missing_record_error(&lockfile_path, cmd))?;
        if locked_tool.source != spec.source_without_version()
            || lock_targets_conflict_with_manifest(
                &lockfile_path,
                root,
                cmd,
                &spec,
                Some(tool),
                locked_tool,
            )
        {
            return Err(BinpmError::StaleLockfile {
                path: lockfile_path.clone(),
                cmd: cmd.clone(),
            });
        }
        let latest_tag = latest_stable_tag_for_update(tool)?;
        let expected_requested_version = tool.version.as_ref().map(|_| latest_tag.clone());
        if tool.version.is_some() && tool.version.as_deref() != Some(latest_tag.as_str()) {
            return Err(BinpmError::StaleLockfile {
                path: lockfile_path.clone(),
                cmd: cmd.clone(),
            });
        }
        if record.requested_version != expected_requested_version
            || record.release_tag != latest_tag
        {
            return Err(BinpmError::StaleLockfile {
                path: lockfile_path.clone(),
                cmd: cmd.clone(),
            });
        }
        assert_lock_record_matches_source_and_target(&lockfile_path, cmd, &spec, &target, record)?;
        assert_lock_matches_manifest_tool(root, cmd, Some(tool), &target, record)?;
        validate_frozen_update_current_release(
            &lockfile_path,
            cmd,
            &spec,
            record,
            client_for_source(&spec)?.as_ref(),
        )?;
    }
    Ok(())
}

fn frozen_lockfile_missing_record_error(lockfile_path: &Path, cmd: &str) -> BinpmError {
    if lockfile_path.exists() {
        BinpmError::FrozenLockfileMissingRecord {
            path: lockfile_path.to_path_buf(),
            cmd: cmd.to_string(),
        }
    } else {
        BinpmError::FrozenLockfile {
            path: lockfile_path.to_path_buf(),
        }
    }
}

fn validate_frozen_update_current_release(
    lockfile_path: &Path,
    cmd: &str,
    spec: &SourceSpec,
    record: &PackageRecord,
    client: &dyn ReleaseClient,
) -> Result<()> {
    let release = client.resolve_release(spec)?.release;
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

fn validate_selected_manifest_entries(manifest: &Manifest, selected: &[String]) -> Result<()> {
    for (cmd, tool) in &manifest.tools {
        if !selected.is_empty() && !selected.contains(cmd) {
            continue;
        }
        validate_command_name(cmd)?;
        parse_manifest_tool_source(tool)?;
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
    output: OutputMode,
) -> Result<InstalledPackage> {
    validate_command_name(cmd)?;
    let lockfile_path = root.join(LOCKFILE_FILE);
    if frozen_lockfile {
        return install_local_from_lock(root, cmd, spec, tool, require_verified, output);
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
    print_install_summary(Scope::Local, cmd, &record);
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
        Err(BinpmError::UnsafeManagedFile { .. }) => return Ok(false),
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

fn print_install_summary(scope: Scope, cmd: &str, record: &PackageRecord) {
    println!("installed {cmd} {}", record.installed_path);
    println!("installed command: {cmd}");
    println!("selected binary: {}", record.selected_binary);
    if scope == Scope::Global && command_alias_differs_from_upstream(cmd, &record.selected_binary) {
        println!(
            "alias note: installed command `{cmd}` invokes upstream binary `{}`; use `--as <cmd>` \
             to choose the local/global command alias and `--bin <upstream-binary>` to choose the \
             upstream executable.",
            record.selected_binary
        );
    }
}

fn command_alias_differs_from_upstream(cmd: &str, selected_binary: &str) -> bool {
    upstream_binary_basename(selected_binary)
        .map(|basename| basename != cmd)
        .unwrap_or(false)
}

fn upstream_binary_basename(selected_binary: &str) -> Option<&str> {
    selected_binary
        .rsplit(['/', '\\'])
        .next()
        .filter(|basename| !basename.is_empty())
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct LockedRecordVerification {
    verified: bool,
    signature_reverified: bool,
}

impl LockedRecordVerification {
    const SIGNATURE_REVERIFIED: Self = Self {
        verified: true,
        signature_reverified: true,
    };
    const UNVERIFIED: Self = Self {
        verified: false,
        signature_reverified: false,
    };
    const VERIFIED: Self = Self {
        verified: true,
        signature_reverified: false,
    };
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
        debug!(
            cache_key = crate::storage::cache_key(sha256),
            cache_path = %cache_paths.entry_dir(sha256).display(),
            local_root = local_root.map(|root| root.display().to_string()).unwrap_or_default(),
            cache_action = "preserve-after-install-failure",
            "Preserved verified cache entry after install finalization failed"
        );
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
    if require_verified
        && !resolved.checksum_source.is_upstream_verified()
        && !resolved_has_supported_signature_evidence(&resolved)
    {
        return Err(BinpmError::VerificationRequired {
            package: spec.to_string(),
        });
    }
    ensure_no_package_record_install_path_collision(scope_paths, cmd, resolved.target.os)?;
    let expected_upstream_sha256 = resolved
        .provider_digest_sha256
        .clone()
        .or_else(|| resolved.upstream_checksum_sha256.clone());
    if let Some(expected) = expected_upstream_sha256 {
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
            )
            .map_err(|error| {
                add_binary_retry_suggestions(error, cmd, spec, local_root.is_some())
            })?;
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
    let bytes = download_asset(
        &resolved.decision.download_url,
        resolved.decision.download_auth.as_ref(),
        resolved.decision.download_accept,
    )?;
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
    } else if let Some(expected) = &resolved.upstream_checksum_sha256 {
        if &sha256 != expected {
            return Err(BinpmError::DigestMismatch {
                path: cache_paths.asset_path(expected),
                expected: expected.clone(),
                actual: sha256,
            });
        }
    }
    verify_signature_sidecar(
        cache_paths,
        &mut resolved,
        &bytes,
        require_verified,
        SignatureVerificationOptions::default(),
    )?;
    if require_verified && !resolved_has_verified_source(&resolved) {
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
        let error = add_binary_retry_suggestions(error, cmd, spec, local_root.is_some());
        if let Some(snapshot) = &cache_metadata_snapshot {
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

fn add_binary_retry_suggestions(
    error: BinpmError,
    cmd: &str,
    spec: &SourceSpec,
    include_add: bool,
) -> BinpmError {
    match error {
        BinpmError::AmbiguousArchiveBinaries {
            asset,
            candidates,
            suggestions,
        } if suggestions.is_empty() => BinpmError::AmbiguousArchiveBinaries {
            asset,
            suggestions: binary_retry_suggestions(cmd, spec, &candidates, include_add),
            candidates,
        },
        error => error,
    }
}

fn binary_retry_suggestions(
    cmd: &str,
    spec: &SourceSpec,
    candidates: &[String],
    include_add: bool,
) -> Vec<String> {
    candidates
        .iter()
        .flat_map(|candidate| {
            let mut suggestions = Vec::new();
            suggestions.push(format!(
                "`binpm install {} --as {} --bin {}`",
                cli_quote(&spec.to_string()),
                cli_quote(cmd),
                cli_quote(candidate)
            ));
            if include_add {
                suggestions.push(format!(
                    "`binpm add {} {} --bin {}`",
                    cli_quote(cmd),
                    cli_quote(&spec.to_string()),
                    cli_quote(candidate)
                ));
            }
            suggestions.push(format!(
                "`binpm x --package {} --bin {} {}`",
                cli_quote(&spec.to_string()),
                cli_quote(candidate),
                cli_quote(cmd)
            ));
            suggestions
        })
        .collect()
}

fn cli_quote(raw: &str) -> String {
    if !raw.is_empty()
        && raw.chars().all(|character| {
            character.is_ascii_alphanumeric()
                || matches!(character, '-' | '_' | '.' | '/' | ':' | '@')
        })
    {
        raw.to_string()
    } else {
        posix_single_quote(raw)
    }
}

fn install_local_from_lock(
    root: &Path,
    cmd: &str,
    spec: &SourceSpec,
    tool: Option<&ManifestTool>,
    require_verified: bool,
    output: OutputMode,
) -> Result<InstalledPackage> {
    validate_command_name(cmd)?;
    let lockfile_path = root.join(LOCKFILE_FILE);
    let lockfile = read_lockfile(&lockfile_path)?;
    let target = HostTarget::current()?;
    let locked_tool = lockfile
        .tools
        .get(cmd)
        .ok_or_else(|| frozen_lockfile_missing_record_error(&lockfile_path, cmd))?;
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
        .ok_or_else(|| frozen_lockfile_missing_record_error(&lockfile_path, cmd))?;
    if record.requested_version != spec.version {
        return Err(BinpmError::StaleLockfile {
            path: lockfile_path.clone(),
            cmd: cmd.to_string(),
        });
    }
    assert_lock_record_matches_source_and_target(&lockfile_path, cmd, spec, &target, &record)?;
    assert_lock_matches_manifest_tool(root, cmd, tool, &target, &record)?;
    validate_provider_digest_evidence(&record)?;
    validate_locked_record_artifact(&lockfile_path, cmd, &record, &target, tool)?;
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
    let mut repair_locked_verification = None;
    if require_verified && !record.has_verified_source() && !record_has_signature_evidence(&record)
    {
        return Err(BinpmError::VerificationRequired {
            package: record.package_spec,
        });
    }
    if !cache_asset_is_verified_regular(&cache_asset, &record.sha256)? {
        let cache_state = if had_existing_cache_entry {
            "invalid"
        } else {
            "missing"
        };
        let repair_result = (|| {
            let download_request = locked_record_download_request(&record)?;
            let download_url = download_request.url.clone();
            let download_authenticated = download_request.auth.is_some();
            if !output.is_json() {
                eprintln!(
                    "binpm: frozen restore cache {cache_state} for {cmd}; downloading locked \
                     asset URL (network_access_attempted=true, \
                     provider_authentication_attached={})",
                    download_authenticated
                );
            }
            let bytes = download_asset_with_options(
                &download_request.url,
                download_request.auth.as_ref(),
                download_request.accept,
                DownloadAssetOptions {
                    silent: output.is_json(),
                },
            )
            .map_err(|source| {
                frozen_restore_download_error(
                    cmd,
                    &cache_asset,
                    cache_state,
                    &download_url,
                    download_authenticated,
                    source,
                )
            })?;
            let actual = format!("{:x}", Sha256::digest(&bytes));
            if actual != record.sha256 {
                return Err(frozen_restore_download_error(
                    cmd,
                    &cache_asset,
                    cache_state,
                    &download_url,
                    download_authenticated,
                    BinpmError::DigestMismatch {
                        path: cache_asset.clone(),
                        expected: record.sha256.clone(),
                        actual,
                    },
                ));
            }
            if require_verified && !record.has_verified_source() {
                let verification = reverify_locked_record_signature_with_options(
                    &cache_paths,
                    &record,
                    &bytes,
                    SignatureVerificationOptions {
                        silent: output.is_json(),
                    },
                )
                .map_err(|source| {
                    frozen_restore_download_error(
                        cmd,
                        &cache_asset,
                        cache_state,
                        &download_url,
                        download_authenticated,
                        source,
                    )
                })?;
                if !verification.verified {
                    return Err(frozen_restore_download_error(
                        cmd,
                        &cache_asset,
                        cache_state,
                        &download_url,
                        download_authenticated,
                        BinpmError::VerificationRequired {
                            package: record.package_spec.clone(),
                        },
                    ));
                }
                repair_locked_verification = Some(verification);
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
                    download_url: download_request.url,
                    download_auth: download_request.auth,
                    download_accept: download_request.accept,
                    kind: crate::assets::classify_artifact(&record.asset_name, false),
                    detected_os: Some(record.target_os),
                    detected_arch: Some(record.target_arch),
                    detected_libc: Some(record.target_libc),
                    cpu_feature: None,
                    score: None,
                    eligible: true,
                    recognized_pattern: true,
                    rejection_reason: None,
                },
                archive_format: record.archive_format,
                selected_binary: record.selected_binary.clone(),
                provider_digest_sha256: None,
                upstream_checksum_sha256: None,
                checksum_source: record.checksum_source,
                signature_sidecar: None,
                signature_available: record.signature_available,
                signature_verified: record.signature_verified,
            };
            populate_cache_from_bytes(&cache_paths, &resolved, &bytes).map_err(|source| {
                frozen_restore_download_error(
                    cmd,
                    &cache_asset,
                    cache_state,
                    &download_url,
                    download_authenticated,
                    source,
                )
            })?;
            Ok(())
        })();
        if let Err(error) = repair_result {
            if let Some(snapshot) = &cache_metadata_snapshot {
                restore_cache_metadata(&cache_paths, snapshot)?;
            }
            return Err(error);
        }
        populated_cache_entry = cache_metadata_snapshot.is_none();
    } else if !output.is_json() {
        eprintln!(
            "binpm: frozen restore reused verified cache for {cmd} \
             (network_access_attempted=false)"
        );
    }
    let locked_verification = if require_verified {
        let verification = match repair_locked_verification {
            Some(verification) => verification,
            None => locked_record_verified_source(&cache_paths, &record)?,
        };
        if !verification.verified {
            return Err(BinpmError::VerificationRequired {
                package: record.package_spec.clone(),
            });
        }
        Some(verification)
    } else {
        None
    };

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
            download_url: record.asset_url.clone(),
            download_auth: None,
            download_accept: None,
            kind: crate::assets::classify_artifact(&record.asset_name, false),
            detected_os: Some(record.target_os),
            detected_arch: Some(record.target_arch),
            detected_libc: Some(record.target_libc),
            cpu_feature: None,
            score: None,
            eligible: true,
            recognized_pattern: true,
            rejection_reason: None,
        },
        archive_format: record.archive_format,
        selected_binary: record.selected_binary.clone(),
        provider_digest_sha256: record.provider_digest_sha256.clone(),
        upstream_checksum_sha256: if matches!(
            record.checksum_source,
            ChecksumSource::Sidecar | ChecksumSource::Manifest
        ) {
            Some(record.sha256.clone())
        } else {
            None
        },
        checksum_source: record.checksum_source,
        signature_sidecar: None,
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
    if locked_verification
        .map(|verification| verification.signature_reverified)
        .unwrap_or(false)
    {
        runtime_record.checksum_source = ChecksumSource::Signature;
        runtime_record.signature_available = true;
        runtime_record.signature_verified = true;
    }
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
    print_install_summary(Scope::Local, cmd, &runtime_record);
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
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
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

#[derive(Debug, Clone)]
struct UpstreamChecksumEvidence {
    sha256: String,
    source: ChecksumSource,
}

fn discover_upstream_checksum(
    selected_asset_name: &str,
    assets: &[ReleaseAsset],
) -> Result<Option<UpstreamChecksumEvidence>> {
    info!(
        asset_name = selected_asset_name,
        "Discovering upstream checksum material"
    );

    for asset in checksum_sidecar_candidates(selected_asset_name, assets) {
        if let Some(sha256) = download_checksum_evidence(asset, selected_asset_name, true)? {
            info!(
                asset_name = selected_asset_name,
                checksum_asset = asset.name,
                checksum_source = ChecksumSource::Sidecar.as_str(),
                "Discovered upstream checksum sidecar"
            );
            return Ok(Some(UpstreamChecksumEvidence {
                sha256,
                source: ChecksumSource::Sidecar,
            }));
        }
    }

    for asset in checksum_manifest_candidates(selected_asset_name, assets) {
        if let Some(sha256) = download_checksum_evidence(asset, selected_asset_name, false)? {
            info!(
                asset_name = selected_asset_name,
                checksum_asset = asset.name,
                checksum_source = ChecksumSource::Manifest.as_str(),
                "Discovered upstream checksum manifest"
            );
            return Ok(Some(UpstreamChecksumEvidence {
                sha256,
                source: ChecksumSource::Manifest,
            }));
        }
    }

    Ok(None)
}

fn checksum_sidecar_candidates<'a>(
    selected_asset_name: &str,
    assets: &'a [ReleaseAsset],
) -> Vec<&'a ReleaseAsset> {
    let selected_lower = selected_asset_name.to_ascii_lowercase();
    let mut candidates = assets
        .iter()
        .filter(|asset| {
            let lower = asset.name.to_ascii_lowercase();
            matches!(
                lower.strip_prefix(&selected_lower),
                Some(".sha256" | ".sha256sum" | ".sha256.txt" | ".sha256sum.txt")
            )
        })
        .collect::<Vec<_>>();
    candidates.sort_by(|left, right| left.name.cmp(&right.name));
    candidates
}

fn checksum_manifest_candidates<'a>(
    selected_asset_name: &str,
    assets: &'a [ReleaseAsset],
) -> Vec<&'a ReleaseAsset> {
    let sidecar_names = checksum_sidecar_candidates(selected_asset_name, assets)
        .into_iter()
        .map(|asset| asset.name.clone())
        .collect::<BTreeSet<_>>();
    let mut candidates = assets
        .iter()
        .filter(|asset| !sidecar_names.contains(&asset.name))
        .filter(|asset| is_checksum_manifest_name(&asset.name))
        .collect::<Vec<_>>();
    candidates.sort_by(|left, right| {
        checksum_manifest_priority(&left.name)
            .cmp(&checksum_manifest_priority(&right.name))
            .then_with(|| left.name.cmp(&right.name))
    });
    candidates
}

fn is_checksum_manifest_name(name: &str) -> bool {
    matches!(
        name.to_ascii_lowercase().as_str(),
        "sha256sums" | "sha256sums.txt" | "sha256.sum" | "sha256.txt" | "checksums.txt"
    )
}

fn checksum_manifest_priority(name: &str) -> u8 {
    match name.to_ascii_lowercase().as_str() {
        "sha256sums" | "sha256sums.txt" => 0,
        "sha256.sum" | "sha256.txt" => 1,
        "checksums.txt" => 2,
        _ => 3,
    }
}

fn download_checksum_evidence(
    asset: &ReleaseAsset,
    selected_asset_name: &str,
    allow_single_digest: bool,
) -> Result<Option<String>> {
    let request = release_asset_download_request(asset)?;
    debug!(
        asset_name = selected_asset_name,
        checksum_asset = asset.name,
        checksum_url = sanitize_download_diagnostic_url(&request.url),
        "Downloading checksum metadata"
    );
    let bytes = download_asset(&request.url, request.auth.as_ref(), request.accept)?;
    let text = String::from_utf8_lossy(&bytes);
    checksum_digest_from_text(&text, selected_asset_name, allow_single_digest)
}

fn release_asset_download_request(asset: &ReleaseAsset) -> Result<DownloadRequest> {
    let url = asset
        .download_url
        .as_deref()
        .or(asset.provider_url.as_deref())
        .unwrap_or(asset.url.as_str())
        .to_string();
    validate_download_url(&url)?;
    Ok(DownloadRequest {
        url,
        auth: asset.download_auth.clone(),
        accept: asset.download_accept,
    })
}

fn checksum_digest_from_text(
    text: &str,
    selected_asset_name: &str,
    allow_single_digest: bool,
) -> Result<Option<String>> {
    let mut single_digest = None;
    for line in text.lines() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let Some(digest) = leading_sha256_digest(line) else {
            continue;
        };
        let remainder = line[digest.len()..].trim_start();
        if checksum_line_matches_asset(remainder, selected_asset_name) {
            return Ok(Some(digest.to_ascii_lowercase()));
        }
        if allow_single_digest && remainder.is_empty() {
            single_digest = Some(digest.to_ascii_lowercase());
        }
    }
    Ok(single_digest)
}

fn leading_sha256_digest(line: &str) -> Option<&str> {
    let candidate = line.get(..64)?;
    if candidate
        .chars()
        .all(|character| character.is_ascii_hexdigit())
    {
        return Some(candidate);
    }
    None
}

fn checksum_line_matches_asset(remainder: &str, selected_asset_name: &str) -> bool {
    let normalized = remainder
        .trim_start_matches('*')
        .trim_start_matches("./")
        .trim();
    normalized == selected_asset_name
        || Path::new(normalized)
            .file_name()
            .and_then(|name| name.to_str())
            == Some(selected_asset_name)
}

struct DownloadRequest {
    url: String,
    auth: Option<ProviderAuth>,
    accept: Option<&'static str>,
}

fn locked_record_download_request(record: &PackageRecord) -> Result<DownloadRequest> {
    let source = SourceSpec {
        provider: record.source_provider,
        host: record.source_host.clone(),
        path: record.source_path.clone(),
        version: Some(record.release_tag.clone()),
    };
    let url = sanitize_persisted_url(&record.asset_url)?;
    let auth = provider_origin_download_auth(&source, &url, provider_auth_for_source(&source));
    let accept = match (record.source_provider, auth.as_ref()) {
        (SourceProvider::GitHub, Some(_)) => Some(GITHUB_ASSET_DOWNLOAD_ACCEPT),
        _ => None,
    };
    Ok(DownloadRequest { url, auth, accept })
}

fn locked_record_verified_download_request(record: &PackageRecord) -> Result<DownloadRequest> {
    let source = SourceSpec {
        provider: record.source_provider,
        host: record.source_host.clone(),
        path: record.source_path.clone(),
        version: Some(record.release_tag.clone()),
    };
    let url = sanitize_persisted_url(&record.asset_url)?;
    let auth = provider_origin_download_auth(&source, &url, provider_auth_for_source(&source));
    let accept = match (record.source_provider, auth.as_ref()) {
        (SourceProvider::GitHub, Some(_)) => Some(GITHUB_ASSET_DOWNLOAD_ACCEPT),
        _ => None,
    };

    Ok(DownloadRequest { url, auth, accept })
}

fn locked_record_signature_sidecar(record: &PackageRecord) -> Result<SignatureSidecar> {
    let url = sanitize_persisted_url(&format!("{}.sigstore.json", record.asset_url))?;
    let source = SourceSpec {
        provider: record.source_provider,
        host: record.source_host.clone(),
        path: record.source_path.clone(),
        version: Some(record.release_tag.clone()),
    };
    let auth = provider_origin_download_auth(&source, &url, provider_auth_for_source(&source));
    let accept = match (record.source_provider, auth.as_ref()) {
        (SourceProvider::GitHub, Some(_)) => Some(GITHUB_ASSET_DOWNLOAD_ACCEPT),
        _ => None,
    };

    Ok(SignatureSidecar {
        asset_name: format!("{}.sigstore.json", record.asset_name),
        canonical_url: url.clone(),
        download_url: url,
        download_auth: auth,
        download_accept: accept,
    })
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
    let upstream_checksum = if provider_digest_sha256.is_none() {
        discover_upstream_checksum(&decision.asset_name, &release.assets)?
    } else {
        None
    };
    let manifest_checksum_source =
        manifest_checksum_source(tool, &target, provider_digest_sha256.as_deref())?;
    let checksum_source = if spec.provider == crate::contract::SourceProvider::GitHub
        && provider_digest_sha256.is_some()
    {
        ChecksumSource::GitHubDigest
    } else if let Some(evidence) = &upstream_checksum {
        evidence.source
    } else {
        manifest_checksum_source
    };
    let signature_sidecar = signature_sidecar_for_asset(&decision.asset_name, &release.assets);
    let signature_available = signature_sidecar.is_some();
    Ok(ResolvedAsset {
        source: spec.clone(),
        release_tag: release.tag,
        target,
        decision,
        archive_format,
        selected_binary,
        provider_digest_sha256,
        upstream_checksum_sha256: upstream_checksum.map(|evidence| evidence.sha256),
        checksum_source,
        signature_sidecar,
        signature_available,
        signature_verified: false,
    })
}

fn signature_sidecar_for_asset(
    asset_name: &str,
    assets: &[ReleaseAsset],
) -> Option<SignatureSidecar> {
    let expected_name = format!("{asset_name}.sigstore.json");
    assets
        .iter()
        .find(|asset| asset.name == expected_name)
        .map(|asset| {
            let download_url = asset
                .download_url
                .as_deref()
                .or(asset.provider_url.as_deref())
                .unwrap_or(&asset.url)
                .to_string();
            let canonical_url = asset
                .provider_url
                .as_deref()
                .unwrap_or(&asset.url)
                .split(['?', '#'])
                .next()
                .unwrap_or(&asset.url)
                .to_string();
            SignatureSidecar {
                asset_name: asset.name.clone(),
                canonical_url,
                download_url,
                download_auth: asset.download_auth.clone(),
                download_accept: asset.download_accept,
            }
        })
}

fn verify_signature_sidecar(
    cache_paths: &CachePaths,
    resolved: &mut ResolvedAsset,
    asset_bytes: &[u8],
    require_verified: bool,
    options: SignatureVerificationOptions,
) -> Result<()> {
    if resolved.checksum_source.is_upstream_verified() {
        return Ok(());
    }
    let Some(sidecar) = resolved.signature_sidecar.clone() else {
        return Ok(());
    };
    resolved.signature_available = true;
    let Some(policy) = sigstore_trust_policy(resolved) else {
        warn!(
            package = %resolved.source,
            asset_name = %resolved.decision.asset_name,
            signature_sidecar = %sidecar.asset_name,
            "Skipping package signature verification because no trust policy applies"
        );
        if require_verified && !options.silent {
            eprintln!(
                "warning: signature sidecar {} is present for {}, but binpm has no applicable \
                 trust policy for this package",
                sidecar.asset_name, resolved.source
            );
        }
        return Ok(());
    };

    let bundle_bytes = match download_asset_with_options(
        &sidecar.download_url,
        sidecar.download_auth.as_ref(),
        sidecar.download_accept,
        DownloadAssetOptions {
            silent: options.silent,
        },
    ) {
        Ok(bytes) => bytes,
        Err(error) if !require_verified => {
            warn!(
                package = %resolved.source,
                asset_name = %resolved.decision.asset_name,
                signature_sidecar = %sidecar.asset_name,
                error = %error,
                "Skipping optional package signature verification because sidecar download failed"
            );
            return Ok(());
        }
        Err(error) => return Err(error),
    };
    let temp_paths = match write_sigstore_verification_inputs(
        cache_paths,
        resolved,
        asset_bytes,
        &bundle_bytes,
    ) {
        Ok(paths) => paths,
        Err(error) if !require_verified => {
            warn!(
                package = %resolved.source,
                asset_name = %resolved.decision.asset_name,
                signature_sidecar = %sidecar.asset_name,
                error = %error,
                "Skipping optional package signature verification because verifier input setup failed"
            );
            return Ok(());
        }
        Err(error) => return Err(error),
    };
    let output = ProcessCommand::new("cosign")
        .arg("verify-blob")
        .arg("--bundle")
        .arg(&temp_paths.bundle_path)
        .arg("--certificate-identity-regexp")
        .arg(&policy.identity_regexp)
        .arg("--certificate-oidc-issuer")
        .arg(policy.issuer)
        .arg(&temp_paths.asset_path)
        .output();
    let _ = remove_path_if_exists(&temp_paths.asset_path);
    let _ = remove_path_if_exists(&temp_paths.bundle_path);

    match output {
        Ok(output) if output.status.success() => {
            resolved.checksum_source = ChecksumSource::Signature;
            resolved.signature_verified = true;
            info!(
                package = %resolved.source,
                asset_name = %resolved.decision.asset_name,
                signature_sidecar = %sidecar.asset_name,
                signature_policy = %policy.name,
                "Verified package signature sidecar"
            );
        }
        Ok(output) => {
            let stderr = String::from_utf8_lossy(&output.stderr);
            warn!(
                package = %resolved.source,
                asset_name = %resolved.decision.asset_name,
                signature_sidecar = %sidecar.asset_name,
                signature_policy = %policy.name,
                status = output.status.code().unwrap_or_default(),
                stderr = %stderr.trim(),
                "Package signature sidecar did not verify"
            );
            if require_verified && !options.silent {
                eprintln!(
                    "warning: signature verification failed for {} using sidecar {}",
                    resolved.source, sidecar.asset_name
                );
            }
        }
        Err(error) if error.kind() == ErrorKind::NotFound => {
            warn!(
                package = %resolved.source,
                asset_name = %resolved.decision.asset_name,
                signature_sidecar = %sidecar.asset_name,
                "Skipping package signature verification because cosign is not on PATH"
            );
            if require_verified && !options.silent {
                eprintln!(
                    "warning: --require-verified needs cosign on PATH to validate signature \
                     sidecar {} for {}",
                    sidecar.asset_name, resolved.source
                );
            }
        }
        Err(error) => {
            if require_verified {
                return Err(BinpmError::Execute {
                    cmd: "cosign".to_string(),
                    source: error,
                });
            }
            warn!(
                package = %resolved.source,
                asset_name = %resolved.decision.asset_name,
                signature_sidecar = %sidecar.asset_name,
                error = %error,
                "Skipping optional package signature verification because cosign could not execute"
            );
        }
    }

    Ok(())
}

#[derive(Clone, Copy, Debug, Default)]
struct SignatureVerificationOptions {
    silent: bool,
}

fn resolved_has_verified_source(resolved: &ResolvedAsset) -> bool {
    resolved.checksum_source.is_upstream_verified()
        || (resolved.checksum_source == ChecksumSource::Signature
            && resolved.signature_available
            && resolved.signature_verified)
}

fn resolved_has_supported_signature_evidence(resolved: &ResolvedAsset) -> bool {
    resolved.signature_available && sigstore_trust_policy(resolved).is_some()
}

fn locked_record_verified_source(
    cache_paths: &CachePaths,
    record: &PackageRecord,
) -> Result<LockedRecordVerification> {
    if record.has_verified_source() {
        return Ok(LockedRecordVerification::VERIFIED);
    }
    if !record_has_signature_evidence(record) {
        return Ok(LockedRecordVerification::UNVERIFIED);
    }
    let asset_path = cache_paths.asset_path(&record.sha256);
    let asset_bytes = fs::read(&asset_path).map_err(|source| BinpmError::ReadFile {
        path: asset_path,
        source,
    })?;
    reverify_locked_record_signature(cache_paths, record, &asset_bytes)
}

fn download_locked_record_verified_source(record: &PackageRecord) -> Result<bool> {
    if record.has_verified_source() {
        return Ok(true);
    }
    if !record_has_signature_evidence(record) {
        return Ok(false);
    }
    let download_request = locked_record_verified_download_request(record)?;
    let asset_bytes = download_asset(
        &download_request.url,
        download_request.auth.as_ref(),
        download_request.accept,
    )?;
    let actual = format!("{:x}", Sha256::digest(&asset_bytes));
    if actual != record.sha256 {
        return Err(BinpmError::DigestMismatch {
            path: PathBuf::from(&record.asset_url),
            expected: record.sha256.clone(),
            actual,
        });
    }
    Ok(reverify_locked_record_signature_in_temp(record, &asset_bytes)?.verified)
}

fn record_has_signature_evidence(record: &PackageRecord) -> bool {
    record.signature_available
        && record.source_provider == SourceProvider::GitHub
        && record.source_host == "github.com"
        && record.source_path.split_once('/').is_some()
}

fn reverify_locked_record_signature_in_temp(
    record: &PackageRecord,
    asset_bytes: &[u8],
) -> Result<LockedRecordVerification> {
    let temp_home = env::temp_dir().join(format!("binpm-signature-{}", sigstore_temp_attempt()));
    let cache_paths = CachePaths::new(&temp_home);
    let result = reverify_locked_record_signature(&cache_paths, record, asset_bytes);
    let cleanup_result = fs::remove_dir_all(&temp_home);
    match (result, cleanup_result) {
        (Ok(verification), _) => Ok(verification),
        (Err(error), _) => Err(error),
    }
}

fn reverify_locked_record_signature(
    cache_paths: &CachePaths,
    record: &PackageRecord,
    asset_bytes: &[u8],
) -> Result<LockedRecordVerification> {
    reverify_locked_record_signature_with_options(
        cache_paths,
        record,
        asset_bytes,
        SignatureVerificationOptions::default(),
    )
}

fn reverify_locked_record_signature_with_options(
    cache_paths: &CachePaths,
    record: &PackageRecord,
    asset_bytes: &[u8],
    options: SignatureVerificationOptions,
) -> Result<LockedRecordVerification> {
    let signature_sidecar = locked_record_signature_sidecar(record)?;
    let mut resolved = ResolvedAsset {
        source: SourceSpec::from_str(
            &record
                .requested_version
                .as_ref()
                .map(|version| format!("{}@{version}", record.source))
                .unwrap_or_else(|| record.source.clone()),
        )?,
        release_tag: record.release_tag.clone(),
        target: HostTarget {
            os: record.target_os,
            arch: record.target_arch,
            libc: record.target_libc,
        },
        decision: CandidateDecision {
            asset_name: record.asset_name.clone(),
            canonical_url: record.asset_url.clone(),
            download_url: record.asset_url.clone(),
            download_auth: None,
            download_accept: None,
            kind: crate::assets::classify_artifact(&record.asset_name, false),
            detected_os: Some(record.target_os),
            detected_arch: Some(record.target_arch),
            detected_libc: Some(record.target_libc),
            cpu_feature: None,
            score: None,
            eligible: true,
            recognized_pattern: true,
            rejection_reason: None,
        },
        archive_format: record.archive_format,
        selected_binary: record.selected_binary.clone(),
        provider_digest_sha256: record.provider_digest_sha256.clone(),
        upstream_checksum_sha256: None,
        checksum_source: ChecksumSource::Local,
        signature_sidecar: Some(signature_sidecar),
        signature_available: true,
        signature_verified: false,
    };
    verify_signature_sidecar(cache_paths, &mut resolved, asset_bytes, true, options)?;
    if resolved_has_verified_source(&resolved) {
        Ok(LockedRecordVerification::SIGNATURE_REVERIFIED)
    } else {
        Ok(LockedRecordVerification::UNVERIFIED)
    }
}

struct SigstoreTrustPolicy {
    name: &'static str,
    issuer: &'static str,
    identity_regexp: String,
}

fn sigstore_trust_policy(resolved: &ResolvedAsset) -> Option<SigstoreTrustPolicy> {
    if resolved.source.provider != SourceProvider::GitHub || resolved.source.host != "github.com" {
        return None;
    }
    let (owner, repo) = resolved.source.path.split_once('/')?;
    Some(SigstoreTrustPolicy {
        name: "github-actions-tagged-release",
        issuer: GITHUB_ACTIONS_OIDC_ISSUER,
        identity_regexp: format!(
            "^https://github\\.com/{}/{}/\\.github/workflows/[^@]+@refs/tags/{}$",
            regex_escape(owner),
            regex_escape(repo),
            regex_escape(&resolved.release_tag)
        ),
    })
}

fn regex_escape(raw: &str) -> String {
    let mut escaped = String::with_capacity(raw.len());
    for character in raw.chars() {
        if matches!(
            character,
            '.' | '+'
                | '*'
                | '?'
                | '^'
                | '$'
                | '('
                | ')'
                | '['
                | ']'
                | '{'
                | '}'
                | '|'
                | '\\'
                | '/'
        ) {
            escaped.push('\\');
        }
        escaped.push(character);
    }
    escaped
}

#[derive(Debug)]
struct SigstoreTempPaths {
    asset_path: PathBuf,
    bundle_path: PathBuf,
}

fn write_sigstore_verification_inputs(
    cache_paths: &CachePaths,
    resolved: &ResolvedAsset,
    asset_bytes: &[u8],
    bundle_bytes: &[u8],
) -> Result<SigstoreTempPaths> {
    ensure_dir(&cache_paths.tmp)?;
    let nonce = sigstore_temp_attempt_for_resolved(resolved);
    let asset_path = cache_paths.tmp.join(format!("sigstore-{nonce}.asset"));
    let bundle_path = cache_paths.tmp.join(format!("sigstore-{nonce}.bundle"));
    write_new_file(&asset_path, asset_bytes)?;
    if let Err(error) = write_new_file(&bundle_path, bundle_bytes) {
        let _ = remove_path_if_exists(&asset_path);
        return Err(error);
    }
    Ok(SigstoreTempPaths {
        asset_path,
        bundle_path,
    })
}

fn sigstore_temp_attempt_for_resolved(resolved: &ResolvedAsset) -> String {
    let identity = format!(
        "{}:{}:{}",
        resolved.source, resolved.release_tag, resolved.decision.asset_name
    );
    format!(
        "{}-{:x}",
        sigstore_temp_attempt(),
        Sha256::digest(identity.as_bytes())
    )
}

fn sigstore_temp_attempt() -> String {
    let sequence = SIGSTORE_TEMP_ATTEMPT.fetch_add(1, Ordering::Relaxed);
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_nanos())
        .unwrap_or_default();
    format!("{}-{sequence}-{nanos:x}", std::process::id())
}

fn write_new_file(path: &Path, bytes: &[u8]) -> Result<()> {
    let mut file = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path)
        .map_err(|source| BinpmError::WriteFile {
            path: path.to_path_buf(),
            source,
        })?;
    file.write_all(bytes)
        .map_err(|source| BinpmError::WriteFile {
            path: path.to_path_buf(),
            source,
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
        let has_executable_mode = entry
            .header()
            .mode()
            .map(|mode| mode & 0o111 != 0)
            .unwrap_or(false);
        let executable = has_executable_mode || archive_exe_is_executable(&path, target);
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
            missing_executable_metadata: false,
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

fn read_zip_selected_binary(
    reader: Cursor<Vec<u8>>,
    asset_name: &str,
    repo_name: &str,
    target: &HostTarget,
    explicit_binary: Option<&str>,
) -> Result<SelectedArchiveBinary> {
    let zip_entry_systems = zip_central_directory_systems(reader.get_ref());
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
        let unix_mode = file.unix_mode();
        let has_executable_mode = unix_mode.map(|mode| mode & 0o111 != 0).unwrap_or(false);
        let executable = has_executable_mode || archive_exe_is_executable(&path, target);
        let has_real_unix_mode =
            zip_file_has_real_unix_mode(zip_entry_systems.get(index).copied(), unix_mode);
        let missing_executable_metadata = !has_real_unix_mode
            && !executable
            && target.os != TargetOs::Windows
            && !path.to_ascii_lowercase().ends_with(".exe");
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
            missing_executable_metadata,
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

fn zip_central_directory_systems(bytes: &[u8]) -> Vec<u8> {
    const CENTRAL_DIRECTORY_SIGNATURE: [u8; 4] = [0x50, 0x4b, 0x01, 0x02];
    const CENTRAL_DIRECTORY_HEADER_LEN: usize = 46;
    const VERSION_MADE_BY_SYSTEM_OFFSET: usize = 5;
    const FILE_NAME_LENGTH_OFFSET: usize = 28;
    const EXTRA_FIELD_LENGTH_OFFSET: usize = 30;
    const FILE_COMMENT_LENGTH_OFFSET: usize = 32;

    let Some(directory_bounds) = zip_central_directory_bounds(bytes) else {
        return Vec::new();
    };

    let mut systems = Vec::new();
    let mut index = directory_bounds.0;
    while index + CENTRAL_DIRECTORY_HEADER_LEN <= directory_bounds.1 {
        if !bytes[index..].starts_with(&CENTRAL_DIRECTORY_SIGNATURE) {
            break;
        }

        let name_len = u16::from_le_bytes([
            bytes[index + FILE_NAME_LENGTH_OFFSET],
            bytes[index + FILE_NAME_LENGTH_OFFSET + 1],
        ]) as usize;
        let extra_len = u16::from_le_bytes([
            bytes[index + EXTRA_FIELD_LENGTH_OFFSET],
            bytes[index + EXTRA_FIELD_LENGTH_OFFSET + 1],
        ]) as usize;
        let comment_len = u16::from_le_bytes([
            bytes[index + FILE_COMMENT_LENGTH_OFFSET],
            bytes[index + FILE_COMMENT_LENGTH_OFFSET + 1],
        ]) as usize;
        let name_start = index + CENTRAL_DIRECTORY_HEADER_LEN;
        let Some(name_end) = name_start.checked_add(name_len) else {
            break;
        };
        let Some(next_index) = name_end
            .checked_add(extra_len)
            .and_then(|offset| offset.checked_add(comment_len))
        else {
            break;
        };
        if next_index > directory_bounds.1 {
            break;
        }

        systems.push(bytes[index + VERSION_MADE_BY_SYSTEM_OFFSET]);
        index = next_index;
    }
    systems
}

fn zip_central_directory_bounds(bytes: &[u8]) -> Option<(usize, usize)> {
    const END_OF_CENTRAL_DIRECTORY_SIGNATURE: [u8; 4] = [0x50, 0x4b, 0x05, 0x06];
    const END_OF_CENTRAL_DIRECTORY_LEN: usize = 22;
    const CENTRAL_DIRECTORY_SIZE_OFFSET: usize = 12;
    const CENTRAL_DIRECTORY_OFFSET_OFFSET: usize = 16;
    const END_OF_CENTRAL_DIRECTORY_COMMENT_LENGTH_OFFSET: usize = 20;
    const ZIP64_END_OF_CENTRAL_DIRECTORY_SIGNATURE: [u8; 4] = [0x50, 0x4b, 0x06, 0x06];
    const ZIP64_END_OF_CENTRAL_DIRECTORY_LEN: usize = 56;
    const ZIP64_END_OF_CENTRAL_DIRECTORY_SIZE_OFFSET: usize = 4;
    const ZIP64_CENTRAL_DIRECTORY_SIZE_OFFSET: usize = 40;
    const ZIP64_CENTRAL_DIRECTORY_OFFSET_OFFSET: usize = 48;
    const ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_SIGNATURE: [u8; 4] = [0x50, 0x4b, 0x06, 0x07];
    const ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_LEN: usize = 20;
    const ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_OFFSET_OFFSET: usize = 8;
    const ZIP32_PLACEHOLDER: u32 = u32::MAX;

    let eocd_index = bytes
        .len()
        .checked_sub(END_OF_CENTRAL_DIRECTORY_LEN)
        .and_then(|last_start| {
            let first_start = bytes
                .len()
                .saturating_sub(END_OF_CENTRAL_DIRECTORY_LEN + u16::MAX as usize);
            (first_start..=last_start).rev().find(|index| {
                let index = *index;
                if !bytes[index..].starts_with(&END_OF_CENTRAL_DIRECTORY_SIGNATURE) {
                    return false;
                }
                let comment_len = u16::from_le_bytes([
                    bytes[index + END_OF_CENTRAL_DIRECTORY_COMMENT_LENGTH_OFFSET],
                    bytes[index + END_OF_CENTRAL_DIRECTORY_COMMENT_LENGTH_OFFSET + 1],
                ]) as usize;
                index + END_OF_CENTRAL_DIRECTORY_LEN + comment_len == bytes.len()
            })
        })?;

    let directory_size_32 = u32::from_le_bytes([
        bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET],
        bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET + 1],
        bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET + 2],
        bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET + 3],
    ]);
    let directory_start_32 = u32::from_le_bytes([
        bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET],
        bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET + 1],
        bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET + 2],
        bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET + 3],
    ]);
    if directory_size_32 != ZIP32_PLACEHOLDER && directory_start_32 != ZIP32_PLACEHOLDER {
        return zip_central_directory_bounds_from_values(
            bytes,
            eocd_index,
            directory_size_32 as usize,
            directory_start_32 as usize,
        );
    }

    let locator_index = eocd_index.checked_sub(ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_LEN)?;
    if !bytes[locator_index..].starts_with(&ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_SIGNATURE) {
        return None;
    }
    let zip64_eocd_offset = u64::from_le_bytes([
        bytes[locator_index + ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_OFFSET_OFFSET],
        bytes[locator_index + ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_OFFSET_OFFSET + 1],
        bytes[locator_index + ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_OFFSET_OFFSET + 2],
        bytes[locator_index + ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_OFFSET_OFFSET + 3],
        bytes[locator_index + ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_OFFSET_OFFSET + 4],
        bytes[locator_index + ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_OFFSET_OFFSET + 5],
        bytes[locator_index + ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_OFFSET_OFFSET + 6],
        bytes[locator_index + ZIP64_END_OF_CENTRAL_DIRECTORY_LOCATOR_OFFSET_OFFSET + 7],
    ]);
    let zip64_eocd_offset = usize::try_from(zip64_eocd_offset).ok()?;
    let zip64_eocd_index = (0..=locator_index.saturating_sub(ZIP64_END_OF_CENTRAL_DIRECTORY_LEN))
        .rev()
        .find(|candidate| {
            let candidate = *candidate;
            if !bytes[candidate..].starts_with(&ZIP64_END_OF_CENTRAL_DIRECTORY_SIGNATURE) {
                return false;
            }
            let record_size = u64::from_le_bytes([
                bytes[candidate + ZIP64_END_OF_CENTRAL_DIRECTORY_SIZE_OFFSET],
                bytes[candidate + ZIP64_END_OF_CENTRAL_DIRECTORY_SIZE_OFFSET + 1],
                bytes[candidate + ZIP64_END_OF_CENTRAL_DIRECTORY_SIZE_OFFSET + 2],
                bytes[candidate + ZIP64_END_OF_CENTRAL_DIRECTORY_SIZE_OFFSET + 3],
                bytes[candidate + ZIP64_END_OF_CENTRAL_DIRECTORY_SIZE_OFFSET + 4],
                bytes[candidate + ZIP64_END_OF_CENTRAL_DIRECTORY_SIZE_OFFSET + 5],
                bytes[candidate + ZIP64_END_OF_CENTRAL_DIRECTORY_SIZE_OFFSET + 6],
                bytes[candidate + ZIP64_END_OF_CENTRAL_DIRECTORY_SIZE_OFFSET + 7],
            ]);
            let Some(record_size) = usize::try_from(record_size).ok() else {
                return false;
            };
            let Some(record_end) = candidate
                .checked_add(ZIP64_END_OF_CENTRAL_DIRECTORY_SIZE_OFFSET + 8)
                .and_then(|offset| offset.checked_add(record_size))
            else {
                return false;
            };
            record_end == locator_index
                && candidate
                    .checked_sub(zip64_eocd_offset)
                    .is_some_and(|archive_offset| {
                        archive_offset
                            .checked_add(zip64_eocd_offset)
                            .is_some_and(|offset| offset == candidate)
                    })
        })?;
    let directory_size = u64::from_le_bytes([
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_SIZE_OFFSET],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_SIZE_OFFSET + 1],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_SIZE_OFFSET + 2],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_SIZE_OFFSET + 3],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_SIZE_OFFSET + 4],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_SIZE_OFFSET + 5],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_SIZE_OFFSET + 6],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_SIZE_OFFSET + 7],
    ]);
    let directory_start = u64::from_le_bytes([
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_OFFSET_OFFSET],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_OFFSET_OFFSET + 1],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_OFFSET_OFFSET + 2],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_OFFSET_OFFSET + 3],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_OFFSET_OFFSET + 4],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_OFFSET_OFFSET + 5],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_OFFSET_OFFSET + 6],
        bytes[zip64_eocd_index + ZIP64_CENTRAL_DIRECTORY_OFFSET_OFFSET + 7],
    ]);
    let directory_size = usize::try_from(directory_size).ok()?;
    let directory_start = usize::try_from(directory_start).ok()?;
    zip_central_directory_bounds_from_values(
        bytes,
        zip64_eocd_index,
        directory_size,
        directory_start,
    )
}

fn zip_central_directory_bounds_from_values(
    bytes: &[u8],
    directory_end_index: usize,
    directory_size: usize,
    directory_start: usize,
) -> Option<(usize, usize)> {
    let archive_offset = directory_end_index
        .checked_sub(directory_size)?
        .checked_sub(directory_start)?;
    let directory_start = archive_offset.checked_add(directory_start)?;
    let directory_end = directory_start.checked_add(directory_size)?;
    (directory_start <= directory_end_index
        && directory_end == directory_end_index
        && directory_end <= bytes.len())
    .then_some((directory_start, directory_end))
}

fn zip_file_has_real_unix_mode(entry_system: Option<u8>, unix_mode: Option<u32>) -> bool {
    const ZIP_SYSTEM_UNIX: u8 = 3;
    const UNIX_FILE_TYPE_MASK: u32 = 0o170000;
    const UNIX_PERMISSION_MASK: u32 = 0o7777;

    let Some(mode) = unix_mode else {
        return false;
    };
    let has_usable_unix_mode = mode & (UNIX_FILE_TYPE_MASK | UNIX_PERMISSION_MASK) != 0;
    has_usable_unix_mode
        && entry_system
            .map(|system| system == ZIP_SYSTEM_UNIX)
            .unwrap_or(false)
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
        if members.iter().any(|member| {
            member.path == explicit_path
                && (member.executable || member.missing_executable_metadata)
        }) {
            explicit_path
        } else {
            let matches = members
                .iter()
                .filter(|member| {
                    (member.executable || member.missing_executable_metadata)
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
                        suggestions: Vec::new(),
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
                    suggestions: Vec::new(),
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

fn parse_manifest_tool_source(tool: &ManifestTool) -> Result<SourceSpec> {
    let mut spec = parse_manifest_source(&tool.source)?;
    if let Some(version) = tool.version.as_deref() {
        let raw = format!("{}@{version}", tool.source);
        validate_version_selector(&raw, version)?;
    }
    spec.version = tool.version.clone();
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

fn normalize_bin_selection(bin: Option<&str>) -> Result<Option<String>> {
    match bin {
        Some(raw) => {
            let trimmed = raw.trim();
            if trimmed.is_empty() {
                Err(BinpmError::InvalidBinSelection {
                    bin: raw.to_string(),
                })
            } else {
                Ok(Some(trimmed.to_string()))
            }
        }
        None => Ok(None),
    }
}

#[derive(Debug)]
struct AddDeclaration {
    cmd: String,
    bin: Option<String>,
}

fn parse_additional_declarations(raw: &[String]) -> Result<Vec<AddDeclaration>> {
    raw.iter()
        .map(|value| {
            let (cmd, bin) = value
                .split_once('=')
                .ok_or_else(|| BinpmError::InvalidBinSelection { bin: value.clone() })?;
            validate_command_name(cmd)?;
            let bin = normalize_bin_selection(Some(bin))?;
            Ok(AddDeclaration {
                cmd: cmd.to_string(),
                bin,
            })
        })
        .collect()
}

fn update_manifest_tool_source(
    tool: Option<ManifestTool>,
    spec: &SourceSpec,
    explicit_bin: Option<String>,
    current_target: Option<&HostTarget>,
) -> ManifestTool {
    let mut tool = tool.unwrap_or_else(|| manifest_tool_from_source(spec));
    tool.source = spec.source_without_version();
    tool.version = spec.version.clone();
    if let Some(bin) = explicit_bin {
        tool.bin = Some(bin.clone());
        if let Some(current_target) = current_target {
            if let Some(override_target) = tool.targets.get_mut(&current_target.key()) {
                override_target.bin = bin;
            }
        }
    }
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
        if archive_format(kind).is_none() {
            return Err(BinpmError::AssetSelectionFailed {
                package: spec.to_string(),
                target: target_key.clone(),
                diagnostics: vec![format!(
                    "target override selected `{}` with kind `{}`; choose an archive or bare \
                     executable release asset and keep installer packages out of overrides",
                    asset.name,
                    kind.as_str()
                )],
            });
        }
        if spec.provider == crate::contract::SourceProvider::GitLab && !gitlab_https_eligible(asset)
        {
            return Err(BinpmError::UnsafeUrl {
                url: gitlab_https_diagnostic_url(asset),
                message: gitlab_https_rejection_reason(asset)
                    .unwrap_or_else(|| "gitlab asset link is not HTTPS eligible".to_string()),
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
                .download_url
                .as_deref()
                .or(asset.provider_url.as_deref())
                .unwrap_or(&asset.url)
                .to_string(),
            download_auth: asset.download_auth.clone(),
            download_accept: asset.download_accept,
            kind,
            detected_os: Some(target.os),
            detected_arch: Some(target.arch),
            detected_libc: Some(target.libc),
            cpu_feature: None,
            score: None,
            eligible: true,
            recognized_pattern: true,
            rejection_reason: None,
        });
    }

    let selection = select_asset(spec.provider, target, assets).ok_or_else(|| {
        let decisions = crate::assets::score_assets(spec.provider, target, assets);
        let diagnostics = selection_failure_diagnostics(&decisions, target);
        if diagnostics.is_empty() {
            BinpmError::AssetNotFound {
                package: spec.to_string(),
                target: target_key,
            }
        } else {
            BinpmError::AssetSelectionFailed {
                package: spec.to_string(),
                target: target_key,
                diagnostics,
            }
        }
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

fn print_json(value: &impl Serialize) -> Result<i32> {
    let rendered = serde_json::to_string(value).map_err(BinpmError::SerializeJson)?;
    println!("{rendered}");
    Ok(0)
}

fn verify_check_output(
    cmd: String,
    target: Option<HostTarget>,
    record: &PackageRecord,
) -> VerifyCheckOutput {
    verify_check_output_with_state(cmd, target, record, verification_state(record))
}

fn verify_check_output_with_state(
    cmd: String,
    target: Option<HostTarget>,
    record: &PackageRecord,
    verification: VerificationState,
) -> VerifyCheckOutput {
    VerifyCheckOutput {
        cmd,
        target,
        checksum_source: record.checksum_source,
        verification,
    }
}

fn list_installed_tool(cmd: String, record: PackageRecord) -> ListToolOutput {
    let verification = verification_state(&record);
    ListToolOutput {
        cmd,
        state: ToolState::Installed,
        source: record.source,
        requested_version: record.requested_version,
        release_tag: Some(record.release_tag),
        selected_binary: Some(record.selected_binary),
        installed_path: Some(record.installed_path),
        verification: Some(verification),
        next_step: None,
    }
}

fn list_declared_tool(cmd: String, tool: ManifestTool) -> ListToolOutput {
    ListToolOutput {
        cmd,
        state: ToolState::Declared,
        source: tool.source,
        requested_version: tool.version,
        release_tag: None,
        selected_binary: None,
        installed_path: None,
        verification: None,
        next_step: Some("binpm install --local".to_string()),
    }
}

fn print_list_tool(row: &ListToolOutput, output: OutputMode) {
    if output.is_json() {
        return;
    }
    match row.state {
        ToolState::Declared => println!(
            "installed_command_alias={} state=declared status=declared-but-not-installed \
             source={} requested_version={} release=<unknown> upstream_binary=<unknown> \
             installed_path=<unknown> verification=<unknown> next=`{}`",
            row.cmd,
            row.source,
            row.requested_version.as_deref().unwrap_or("<latest>"),
            row.next_step.as_deref().unwrap_or("binpm install")
        ),
        ToolState::Installed => println!(
            "installed_command_alias={} state=installed source={} requested_version={} release={} \
             upstream_binary={} installed_path={} verification={}",
            row.cmd,
            row.source,
            row.requested_version.as_deref().unwrap_or("<latest>"),
            row.release_tag.as_deref().unwrap_or("<unknown>"),
            row.selected_binary.as_deref().unwrap_or("<unknown>"),
            row.installed_path.as_deref().unwrap_or("<unknown>"),
            row.verification
                .map(VerificationState::as_str)
                .unwrap_or("unknown")
        ),
    }
}

fn package_record_output(record: &PackageRecord) -> Result<PackageRecordOutput> {
    Ok(PackageRecordOutput {
        package_spec: record.package_spec.clone(),
        source: record.source.clone(),
        source_provider: record.source_provider,
        source_host: record.source_host.clone(),
        source_path: record.source_path.clone(),
        requested_version: record.requested_version.clone(),
        release_tag: record.release_tag.clone(),
        asset_name: record.asset_name.clone(),
        asset_url: sanitize_persisted_url(&record.asset_url)?,
        target: HostTarget {
            os: record.target_os,
            arch: record.target_arch,
            libc: record.target_libc,
        },
        archive_format: record.archive_format,
        selected_binary: record.selected_binary.clone(),
        installed_path: record.installed_path.clone(),
        cache_key: record.cache_key.clone(),
        cache_path: record.cache_path.clone(),
        sha256: record.sha256.clone(),
        checksum_source: record.checksum_source,
        verification: verification_state(record),
        signature_available: record.signature_available,
        signature_verified: record.signature_verified,
    })
}

fn selected_asset_output(
    decision: &crate::assets::CandidateDecision,
) -> Result<SelectedAssetOutput> {
    Ok(SelectedAssetOutput {
        asset_name: decision.asset_name.clone(),
        asset_url: selected_asset_display_url(decision)?,
        archive_format: candidate_archive_format(decision.kind),
        score: decision.score,
    })
}

fn candidate_output(decision: &crate::assets::CandidateDecision) -> CandidateOutput {
    CandidateOutput {
        asset_name: decision.asset_name.clone(),
        kind: decision.kind.as_str().to_string(),
        archive_format: candidate_archive_format(decision.kind),
        detected_os: decision.detected_os,
        detected_arch: decision.detected_arch,
        detected_libc: decision.detected_libc,
        cpu_feature: decision.cpu_feature,
        score: decision.score,
        eligible: decision.eligible,
        recognized_pattern: decision.recognized_pattern,
        rejection_reason: decision.rejection_reason.clone(),
    }
}

fn candidate_archive_format(kind: crate::assets::ArtifactKind) -> Option<ArchiveFormat> {
    match kind {
        crate::assets::ArtifactKind::Archive(format) => Some(format),
        crate::assets::ArtifactKind::BareExecutable => Some(ArchiveFormat::BareExecutable),
        _ => None,
    }
}

fn json_path_state(path: &Path) -> PathState {
    if path.exists() {
        PathState::Present
    } else {
        PathState::Missing
    }
}

fn declared_only_local_tools(root: &Path) -> Result<Vec<DeclaredOnlyToolOutput>> {
    let manifest_path = root.join(MANIFEST_FILE);
    if !manifest_path.exists() {
        return Ok(Vec::new());
    }
    let lockfile_path = root.join(LOCKFILE_FILE);

    let manifest = match read_manifest(&manifest_path) {
        Ok(manifest) => manifest,
        Err(
            error @ (BinpmError::ParseToml { .. }
            | BinpmError::UnsupportedStorageVersion {
                kind: "manifest", ..
            }),
        ) => {
            warn!(
                command = "doctor",
                manifest_path = %manifest_path.display(),
                error = %error,
                "Skipping declared-only tool scan because the manifest could not be parsed"
            );
            return Ok(Vec::new());
        }
        Err(error) => return Err(error),
    };
    let mut declared_only_tools = Vec::new();
    for (cmd, tool) in manifest.tools {
        if let Err(error) = validate_command_name(&cmd) {
            warn!(
                command = "doctor",
                manifest_path = %manifest_path.display(),
                tool_cmd = %cmd,
                error = %error,
                "Skipping declared-only tool scan because a manifest tool declaration is invalid"
            );
            return Ok(Vec::new());
        }
        let spec = match parse_manifest_tool_source(&tool) {
            Ok(spec) => spec,
            Err(BinpmError::InvalidSourceSpec { .. }) => {
                warn!(
                    command = "doctor",
                    manifest_path = %manifest_path.display(),
                    tool_cmd = %cmd,
                    error_kind = "invalid source spec",
                    "Skipping declared-only tool scan because a manifest tool source is invalid"
                );
                return Ok(Vec::new());
            }
            Err(error) => return Err(error),
        };
        let runtime_record = local_runtime_tool_record(root, &cmd)?;
        let has_manifest_runtime_record = match runtime_record.as_ref() {
            Some(record) => local_runtime_record_matches_manifest(&tool, record)?,
            None => false,
        };
        match local_tool_execution_ready(root, &cmd, &spec, Some(&tool)) {
            Ok(true) => continue,
            Ok(false) if has_manifest_runtime_record => continue,
            Ok(false) => {}
            Err(
                ref error @ (BinpmError::ParseToml { ref path, .. }
                | BinpmError::UnsupportedStorageVersion {
                    kind: "lockfile",
                    ref path,
                    ..
                }),
            ) if path == &lockfile_path => {
                warn!(
                    command = "doctor",
                    manifest_path = %manifest_path.display(),
                    lockfile_path = %lockfile_path.display(),
                    tool_cmd = %cmd,
                    error = %error,
                    "Skipping declared-only tool scan because the lockfile could not be parsed"
                );
                return Ok(Vec::new());
            }
            Err(error) => {
                if runtime_record.is_some() {
                    return Err(error);
                }
                warn!(
                    command = "doctor",
                    manifest_path = %manifest_path.display(),
                    tool_cmd = %cmd,
                    error = %error,
                    "Treating local tool as declared-only because readiness could not be checked"
                );
            }
        }
        let paths = ScopePaths::local(root.to_path_buf());
        declared_only_tools.push(DeclaredOnlyToolOutput {
            expected_executable_path: current_platform_installed_path(&paths, &cmd)
                .display()
                .to_string(),
            cmd,
            source: tool.source,
            requested_version: tool.version,
        });
    }
    Ok(declared_only_tools)
}

fn local_runtime_record_matches_manifest(
    tool: &ManifestTool,
    record: &PackageRecord,
) -> Result<bool> {
    let spec = parse_manifest_tool_source(tool)?;
    let target = HostTarget::current()?;
    let source_and_target_match = record.source == spec.source_without_version()
        && record.source_provider == spec.provider
        && record.source_host == spec.host
        && record.source_path == spec.path
        && record.requested_version == spec.version
        && record.target_os == target.os
        && record.target_arch == target.arch
        && record.target_libc == target.libc;
    if !source_and_target_match {
        return Ok(false);
    }
    if let Some(override_target) = manifest_target_override(Some(tool), &target)? {
        return Ok(record.asset_name == override_target.asset
            && manifest_bin_matches_record(&override_target.bin, &record.selected_binary));
    }
    Ok(tool
        .bin
        .as_ref()
        .map(|bin| manifest_bin_matches_record(bin, &record.selected_binary))
        .unwrap_or(true))
}

fn local_runtime_tool_record(root: &Path, cmd: &str) -> Result<Option<PackageRecord>> {
    let paths = ScopePaths::local(root.to_path_buf());
    let record = match read_package_record(&package_record_path(&paths, cmd)) {
        Ok(record) => record,
        Err(BinpmError::ReadFile { source, .. }) if source.kind() == ErrorKind::NotFound => {
            return Ok(None)
        }
        Err(error) => return Err(error),
    };

    let installed_path = match validate_installed_binary_path(&paths, cmd, &record) {
        Ok(path) => path,
        Err(BinpmError::UnsafeInstalledPath { .. }) => return Ok(None),
        Err(error) => return Err(error),
    };
    match require_regular_managed_file(&installed_path) {
        Ok(()) => {}
        Err(BinpmError::ReadFile { source, .. }) if source.kind() == ErrorKind::NotFound => {
            return Ok(None)
        }
        Err(BinpmError::UnsafeManagedFile { .. }) => return Ok(None),
        Err(error) => return Err(error),
    }
    match require_executable_managed_file(&installed_path) {
        Ok(()) => Ok(Some(record)),
        Err(BinpmError::UnsafeManagedFile { .. }) => Ok(None),
        Err(error) => Err(error),
    }
}

fn verification_state(record: &PackageRecord) -> VerificationState {
    if record.has_verified_source() || record_reports_verified_signature(record) {
        VerificationState::Verified
    } else {
        VerificationState::Unverified
    }
}

fn record_reports_verified_signature(record: &PackageRecord) -> bool {
    record.checksum_source == ChecksumSource::Signature
        && record.signature_available
        && record.signature_verified
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
    let lockfile_path = root.join(LOCKFILE_FILE);
    let mut lockfile = read_lockfile(&lockfile_path)?;
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let orphan_cmds = local_manifest_orphan_cmds(root, &lockfile, manifest_tools)?;

    if orphan_cmds.is_empty() {
        return Ok(());
    }
    if frozen_lockfile {
        return Err(BinpmError::FrozenLockfileOrphanCleanup {
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
    let lockfile = read_lockfile(&root.join(LOCKFILE_FILE))?;
    let orphan_cmds = local_manifest_orphan_cmds(root, &lockfile, manifest_tools)?;
    orphan_cmds
        .into_iter()
        .map(|cmd| Ok((cmd.clone(), capture_runtime_tool_state(&scope_paths, &cmd)?)))
        .collect()
}

fn local_manifest_orphan_cmds(
    root: &Path,
    lockfile: &crate::storage::Lockfile,
    manifest_tools: &BTreeMap<String, ManifestTool>,
) -> Result<BTreeSet<String>> {
    let scope_paths = ScopePaths::local(root.to_path_buf());
    let mut orphan_cmds = BTreeSet::new();
    for (cmd, _) in list_package_records(&scope_paths)? {
        if !manifest_tools.contains_key(&cmd) {
            validate_command_name(&cmd)?;
            orphan_cmds.insert(cmd);
        }
    }
    for cmd in lockfile.tools.keys() {
        if !manifest_tools.contains_key(cmd) {
            validate_command_name(cmd)?;
            orphan_cmds.insert(cmd.clone());
        }
    }
    Ok(orphan_cmds)
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
                message: format!(
                    "Manifest override keys must be canonical. Use \
                     `[tools.<cmd>.targets.{canonical_key}]`; aliases are accepted in release \
                     asset names, not as persisted override keys."
                ),
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

fn download_asset(
    url: &str,
    auth: Option<&ProviderAuth>,
    accept: Option<&'static str>,
) -> Result<Vec<u8>> {
    download_asset_with_options(url, auth, accept, DownloadAssetOptions::default())
}

#[derive(Clone, Copy, Debug, Default)]
struct DownloadAssetOptions {
    silent: bool,
}

fn download_asset_with_options(
    url: &str,
    auth: Option<&ProviderAuth>,
    accept: Option<&'static str>,
    options: DownloadAssetOptions,
) -> Result<Vec<u8>> {
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
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(BinpmError::ReleaseHttpClient)?;

    let mut last_error = None;
    for attempt in 1..=DOWNLOAD_RETRY_ATTEMPTS {
        let context = DownloadAssetAttemptContext {
            attempt,
            sanitized_url: &sanitized_url,
            asset_name: &asset_name,
            options,
        };
        match download_asset_attempt(&client, url, auth, accept, context) {
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
                if !options.silent {
                    eprintln!(
                        "binpm: retrying download of {asset_name} after a transient failure \
                         (attempt {}/{})",
                        attempt + 1,
                        DOWNLOAD_RETRY_ATTEMPTS
                    );
                }
                thread::sleep(delay);
                last_error = Some(error);
            }
            Err(error) => return Err(error),
        }
    }

    Err(last_error.expect("download retry loop always returns before exhaustion"))
}

#[derive(Clone, Copy, Debug)]
struct DownloadAssetAttemptContext<'a> {
    attempt: usize,
    sanitized_url: &'a str,
    asset_name: &'a str,
    options: DownloadAssetOptions,
}

fn download_asset_attempt(
    client: &reqwest::blocking::Client,
    url: &str,
    auth: Option<&ProviderAuth>,
    accept: Option<&'static str>,
    context: DownloadAssetAttemptContext<'_>,
) -> Result<Vec<u8>> {
    let DownloadAssetAttemptContext {
        attempt,
        sanitized_url,
        asset_name,
        options,
    } = context;
    let origin = reqwest::Url::parse(url).expect("download URL was already validated");
    let mut current_url = url.to_string();
    let mut visited_urls = BTreeSet::new();
    let mut redirects = 0usize;
    let mut response = loop {
        if !visited_urls.insert(current_url.clone()) {
            return Err(BinpmError::UnsafeUrl {
                url: sanitize_download_diagnostic_url(&current_url),
                message: "release asset redirect loop detected".to_string(),
            });
        }
        let current = reqwest::Url::parse(&current_url).map_err(|_| BinpmError::UnsafeUrl {
            url: sanitize_download_diagnostic_url(&current_url),
            message: "persisted release asset URLs must be valid https URLs".to_string(),
        })?;
        validate_download_url(current.as_str())?;
        let mut request = client.get(current.as_str());
        if let Some(accept) = accept {
            request = request.header(reqwest::header::ACCEPT, accept);
        }
        if let Some(auth) = auth.filter(|_| same_download_origin(&origin, &current)) {
            request = request.header(auth.header_name, auth.header_value.as_str());
        }

        let response = request
            .send()
            .map_err(|error| BinpmError::ReleaseLookup(error.without_url()))?;
        validate_download_url(response.url().as_str())?;
        let status = response.status();
        if !status.is_redirection() {
            break response;
        }
        let Some(next_url) = response
            .headers()
            .get(reqwest::header::LOCATION)
            .and_then(|location| location.to_str().ok())
            .and_then(|location| response.url().join(location).ok())
            .map(|location| location.to_string())
        else {
            break response;
        };
        redirects += 1;
        if redirects > 10 {
            return Err(BinpmError::UnsafeUrl {
                url: sanitize_download_diagnostic_url(&next_url),
                message: "release asset redirect chain exceeded limit".to_string(),
            });
        }
        current_url = next_url;
    };
    let final_url = sanitize_download_diagnostic_url(response.url().as_str());
    let status = response.status();
    if !status.is_success() {
        if let Some(error) = response.error_for_status_ref().err() {
            return Err(BinpmError::ReleaseLookup(error.without_url()));
        }
        return Err(BinpmError::ReleaseAssetStatus {
            url: final_url,
            status: status.as_u16(),
        });
    }

    let total_bytes = response.content_length();
    let show_progress = !options.silent && download_progress_enabled(total_bytes);
    if show_progress {
        eprintln!(
            "binpm: downloading {asset_name}{}",
            total_bytes
                .map(|bytes| format!(" ({})", human_bytes(bytes)))
                .unwrap_or_default()
        );
    }

    let mut bytes = Vec::with_capacity(download_initial_capacity(total_bytes));
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

fn same_download_origin(left: &reqwest::Url, right: &reqwest::Url) -> bool {
    left.scheme() == right.scheme()
        && left.host_str() == right.host_str()
        && left.port_or_known_default() == right.port_or_known_default()
}

fn provider_origin_download_auth(
    source: &SourceSpec,
    url: &str,
    auth: Option<ProviderAuth>,
) -> Option<ProviderAuth> {
    let auth = auth?;
    let source_origin = reqwest::Url::parse(&format!("https://{}/", source.host)).ok()?;
    let request_origin = reqwest::Url::parse(url).ok()?;

    same_download_origin(&source_origin, &request_origin).then_some(auth)
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

fn download_initial_capacity(total_bytes: Option<u64>) -> usize {
    total_bytes
        .map(|bytes| bytes.min(DOWNLOAD_INITIAL_CAPACITY_LIMIT as u64) as usize)
        .unwrap_or_default()
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
    println!("cleaned local manifest, lockfile, package record, cache ref, and executable state");
    println!(
        "cache assets preserved; run `binpm cache prune` for unreferenced assets or `binpm cache \
         clean` for all cached assets"
    );
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
    println!("cleaned global package record and executable state");
    println!(
        "cache assets preserved; run `binpm cache prune` for unreferenced assets or `binpm cache \
         clean` for all cached assets"
    );
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

fn verify(args: VerifyArgs, output: OutputMode) -> Result<i32> {
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
    let mut checks = Vec::new();
    let mut locked = BTreeSet::new();
    let mut local_runtime_locks = BTreeMap::new();
    if !output.is_json() {
        println!("verify scope: {}", scope.as_str());
    }
    if let Some(root) = &root {
        let manifest = read_manifest(&root.join(MANIFEST_FILE))?;
        let lockfile = read_lockfile(&root.join(LOCKFILE_FILE))?;
        local_runtime_locks =
            local_runtime_lock_records(&manifest, &lockfile, &HostTarget::current()?)?;
        let (lock_checked, lock_commands, lock_checks) = verify_lockfile_records(
            &root.join(LOCKFILE_FILE),
            lockfile,
            Some((&manifest, root.as_path())),
            args.require_verified,
            output,
        )?;
        checked += lock_checked;
        locked = lock_commands;
        checks.extend(lock_checks);
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
        validate_provider_digest_evidence(&record)?;
        validate_package_record_current_provider_digest(&record)?;
        validate_package_record_metadata(&cache_paths, &record)?;
        verify_runtime_cache_bytes(&cache_paths, &record)?;
        let runtime_check = if args.require_verified {
            if !locked_record_verified_source(&cache_paths, &record)?.verified {
                return Err(BinpmError::VerificationRequired {
                    package: record.package_spec,
                });
            }
            verify_check_output_with_state(cmd.clone(), None, &record, VerificationState::Verified)
        } else {
            verify_check_output(cmd.clone(), None, &record)
        };
        let installed_path = validate_installed_binary_path(&paths, &cmd, &record)?;
        require_regular_managed_file(&installed_path)?;
        require_executable_managed_file(&installed_path)?;
        verify_installed_binary_contents(&cache_paths, &record, &installed_path)?;
        checks.push(runtime_check);
        if !output.is_json() {
            println!("{cmd} verified {}", record.checksum_source.as_str());
        }
        if !locked.contains(&cmd) {
            checked += 1;
        }
    }
    if let Some(root) = &root {
        assert_local_runtime_records_complete(root, &local_runtime_locks)?;
    }
    if output.is_json() {
        return print_json(&VerifyOutput {
            command: "verify",
            scope,
            require_verified: args.require_verified,
            checked,
            checks,
        });
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
        || !runtime_integrity_metadata_matches_lock(lock_record, runtime_record)
    {
        return Err(BinpmError::StaleLockfile {
            path: root.join(LOCKFILE_FILE),
            cmd: cmd.to_string(),
        });
    }
    Ok(())
}

fn runtime_integrity_metadata_matches_lock(
    lock_record: &PackageRecord,
    runtime_record: &PackageRecord,
) -> bool {
    if runtime_record.checksum_source == lock_record.checksum_source
        && runtime_record.signature_available == lock_record.signature_available
        && runtime_record.signature_verified == lock_record.signature_verified
    {
        return true;
    }

    !lock_record.has_verified_source()
        && lock_record.signature_available
        && !lock_record.signature_verified
        && runtime_record.checksum_source == ChecksumSource::Signature
        && runtime_record.signature_available
        && runtime_record.signature_verified
}

fn verify_lockfile_records(
    lockfile_path: &Path,
    lockfile: crate::storage::Lockfile,
    manifest: Option<(&Manifest, &Path)>,
    require_verified: bool,
    output: OutputMode,
) -> Result<(usize, BTreeSet<String>, Vec<VerifyCheckOutput>)> {
    let mut checked = 0usize;
    let mut locked = BTreeSet::new();
    let mut checks = Vec::new();
    if let Some((manifest, root)) = manifest {
        for (cmd, manifest_tool) in &manifest.tools {
            validate_command_name(cmd)?;
            let spec = parse_manifest_tool_source(manifest_tool)?;
            let locked_tool = lockfile
                .tools
                .get(cmd)
                .ok_or_else(|| BinpmError::StaleLockfile {
                    path: lockfile_path.to_path_buf(),
                    cmd: cmd.clone(),
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
            validate_provider_digest_evidence(&record)?;
            validate_locked_record_current_release(lockfile_path, &cmd, &record)?;
            let lock_check = if require_verified {
                if !download_locked_record_verified_source(&record)? {
                    return Err(BinpmError::VerificationRequired {
                        package: record.package_spec,
                    });
                }
                verify_check_output_with_state(
                    cmd.clone(),
                    Some(target.clone()),
                    &record,
                    VerificationState::Verified,
                )
            } else {
                verify_check_output(cmd.clone(), Some(target.clone()), &record)
            };
            locked.insert(cmd.clone());
            checks.push(lock_check);
            if !output.is_json() {
                println!(
                    "{cmd} lock verified {target_key} {}",
                    record.checksum_source.as_str()
                );
            }
            checked += 1;
        }
    }
    Ok((checked, locked, checks))
}

fn init(args: InitArgs) -> Result<i32> {
    let explicit_destination = args.manifest_path.is_some();
    let manifest_path = init_manifest_path(args.manifest_path)?;

    println!("manifest destination: {}", manifest_path.display());

    if path_exists_or_unreadable(&manifest_path) {
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
        explicit_destination,
        "Wrote minimal binpm manifest"
    );
    println!("created manifest: {}", manifest_path.display());
    Ok(0)
}

fn env_cmd(args: EnvArgs) -> Result<i32> {
    let shell = args.shell.map(Ok).unwrap_or_else(infer_env_shell)?;
    let scope = env_path_scope(&args);

    let global_bin = if matches!(scope, EnvPathScope::Both | EnvPathScope::Global) {
        Some(binpm_home()?.join("bin"))
    } else {
        None
    };
    let local_bin = if matches!(scope, EnvPathScope::Both | EnvPathScope::Local) {
        Some(project_root()?.join(".binpm").join("bin"))
    } else {
        None
    };

    if matches!(shell, Shell::Cmd) {
        return Err(BinpmError::UnsupportedShell {
            shell: shell.as_str().to_string(),
            cmd_hint: cmd_path_hint(scope, global_bin.as_deref(), local_bin.as_deref()),
        });
    }

    let global_bin_display = global_bin
        .as_ref()
        .map(|path| path.display().to_string())
        .unwrap_or_else(|| "<not requested>".to_string());
    let local_bin_display = local_bin
        .as_ref()
        .map(|path| path.display().to_string())
        .unwrap_or_else(|| "<not requested>".to_string());

    info!(
        command = "env",
        shell = shell.as_str(),
        path_scope = ?scope,
        read_only = true,
        global_bin = %global_bin_display,
        local_bin = %local_bin_display,
        "Rendered PATH environment commands"
    );

    print_env(shell, scope, global_bin.as_deref(), local_bin.as_deref());
    Ok(0)
}

fn init_manifest_path(explicit: Option<PathBuf>) -> Result<PathBuf> {
    if let Some(path) = explicit {
        if path.file_name() != Some(std::ffi::OsStr::new(MANIFEST_FILE))
            || path
                .components()
                .any(|component| matches!(component, std::path::Component::ParentDir))
        {
            return Err(BinpmError::InvalidInitManifestPath { path });
        }
        if path.is_absolute() {
            return Ok(path);
        }
        return Ok(current_dir()?.join(path));
    }

    Ok(manifest_creation_root()?.join(MANIFEST_FILE))
}

fn env_path_scope(args: &EnvArgs) -> EnvPathScope {
    match (args.global, args.local) {
        (true, false) => EnvPathScope::Global,
        (false, true) => EnvPathScope::Local,
        _ => EnvPathScope::Both,
    }
}

fn infer_env_shell() -> Result<Shell> {
    for name in ["SHELL", "ComSpec"] {
        if let Some(shell) = env::var_os(name).and_then(|value| shell_from_program(&value)) {
            return Ok(shell);
        }
    }
    Err(BinpmError::ShellRequired)
}

fn shell_from_program(program: &std::ffi::OsStr) -> Option<Shell> {
    let name = Path::new(program)
        .file_stem()
        .or_else(|| Path::new(program).file_name())?
        .to_string_lossy()
        .to_ascii_lowercase();
    match name.as_str() {
        "bash" => Some(Shell::Bash),
        "zsh" => Some(Shell::Zsh),
        "fish" => Some(Shell::Fish),
        "powershell" | "pwsh" => Some(Shell::Powershell),
        "cmd" => Some(Shell::Cmd),
        _ => None,
    }
}

fn print_env(
    shell: Shell,
    scope: EnvPathScope,
    global_bin: Option<&Path>,
    local_bin: Option<&Path>,
) {
    match shell {
        Shell::Bash | Shell::Zsh => {
            if matches!(scope, EnvPathScope::Both | EnvPathScope::Global) {
                let global = shell_quote(shell, global_bin.expect("global bin path for env scope"));
                println!("# Global bin: persist this line in shell profiles");
                println!("export PATH={global}${{PATH:+:$PATH}}");
            }
            if matches!(scope, EnvPathScope::Both | EnvPathScope::Local) {
                let local = shell_quote(shell, local_bin.expect("local bin path for env scope"));
                println!("# Project-local bin: use for the current project/session only");
                println!("export PATH={local}${{PATH:+:$PATH}}");
            }
        }
        Shell::Fish => {
            if matches!(scope, EnvPathScope::Both | EnvPathScope::Global) {
                let global = shell_quote(shell, global_bin.expect("global bin path for env scope"));
                println!("# Global bin: persist this line in shell profiles");
                println!("set -gx PATH {global} $PATH");
            }
            if matches!(scope, EnvPathScope::Both | EnvPathScope::Local) {
                let local = shell_quote(shell, local_bin.expect("local bin path for env scope"));
                println!("# Project-local bin: use for the current project/session only");
                println!("set -gx PATH {local} $PATH");
            }
        }
        Shell::Powershell => {
            if matches!(scope, EnvPathScope::Both | EnvPathScope::Global) {
                let global = shell_quote(shell, global_bin.expect("global bin path for env scope"));
                println!("# Global bin: persist this line in shell profiles");
                println!(
                    "$env:PATH = {global} + $(if ($env:PATH) {{ [System.IO.Path]::PathSeparator + \
                     $env:PATH }} else {{ '' }})"
                );
            }
            if matches!(scope, EnvPathScope::Both | EnvPathScope::Local) {
                let local = shell_quote(shell, local_bin.expect("local bin path for env scope"));
                println!("# Project-local bin: use for the current project/session only");
                println!(
                    "$env:PATH = {local} + $(if ($env:PATH) {{ [System.IO.Path]::PathSeparator + \
                     $env:PATH }} else {{ '' }})"
                );
            }
        }
        Shell::Cmd => unreachable!("cmd shell is explicitly deferred before rendering"),
    }
}

fn cmd_path_hint(
    scope: EnvPathScope,
    global_bin: Option<&Path>,
    local_bin: Option<&Path>,
) -> String {
    match scope {
        EnvPathScope::Global => {
            cmd_global_path_hint(global_bin.expect("global bin path for env scope"))
        }
        EnvPathScope::Local => {
            cmd_local_path_hint(local_bin.expect("local bin path for env scope"))
        }
        EnvPathScope::Both => {
            let global_path = global_bin.expect("global bin path for env scope");
            let local_path = local_bin.expect("local bin path for env scope");
            let global = cmd_path(global_path);
            let global_set = cmd_set_path(global_path);
            let local_set = cmd_set_path(local_path);
            format!(
                "For cmd.exe, add the global bin `{global}` to the user PATH in Windows \
                 Environment Variables. For the current project/session, run `set \
                 \"PATH={local_set};%PATH%\"`. To include both in the current cmd.exe session, \
                 run `set \"PATH={local_set};{global_set};%PATH%\"`."
            )
        }
    }
}

fn cmd_global_path_hint(path: &Path) -> String {
    let raw_path = cmd_path(path);
    let set_path = cmd_set_path(path);
    format!(
        "For cmd.exe, add `{raw_path}` to the user PATH in Windows Environment Variables, or for \
         the current cmd.exe session run `set \"PATH={set_path};%PATH%\"`."
    )
}

fn cmd_local_path_hint(path: &Path) -> String {
    let path = cmd_set_path(path);
    format!("For cmd.exe, run `set \"PATH={path};%PATH%\"` for the current project/session.")
}

fn cmd_path(path: &Path) -> String {
    path.display().to_string()
}

fn cmd_set_path(path: &Path) -> String {
    cmd_escape(&cmd_path(path))
}

fn cmd_escape(raw: &str) -> String {
    raw.replace('^', "^^").replace('%', "%%cd:~,%")
}

fn shell_quote(shell: Shell, path: &Path) -> String {
    let raw = shell_path(shell, &path.display().to_string());
    match shell {
        Shell::Bash | Shell::Zsh => posix_single_quote(&raw),
        Shell::Fish => fish_single_quote(&raw),
        Shell::Powershell => powershell_single_quote(&raw),
        Shell::Cmd => unreachable!("cmd shell is explicitly deferred before quoting"),
    }
}

fn shell_path(shell: Shell, raw: &str) -> String {
    match shell {
        Shell::Bash | Shell::Zsh => {
            windows_path_for_posix_shell(raw).unwrap_or_else(|| raw.to_owned())
        }
        Shell::Fish | Shell::Powershell => raw.to_owned(),
        Shell::Cmd => unreachable!("cmd shell is explicitly deferred before path rendering"),
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

fn path_contains_entry(entry: &Path) -> bool {
    env::var_os("PATH")
        .map(|path| env::split_paths(&path).any(|candidate| paths_equivalent(&candidate, entry)))
        .unwrap_or(false)
}

fn paths_equivalent(left: &Path, right: &Path) -> bool {
    if left == right {
        return true;
    }

    match (left.canonicalize(), right.canonicalize()) {
        (Ok(left), Ok(right)) => left == right,
        _ => false,
    }
}

fn yes_no(value: bool) -> &'static str {
    if value {
        "yes"
    } else {
        "no"
    }
}

fn print_global_path_setup_guidance(global_bin: &Path) {
    println!("path_setup: {} is not on PATH", global_bin.display());
    println!(
        "path_setup: run `binpm env --global --shell <bash|zsh|fish|powershell>` to print PATH \
         setup commands"
    );
    println!(
        "path_setup: profile changes are opt-in; persist only the global bin line in shell \
         profiles"
    );
    println!("path_setup: the project-local PATH line is for the current project/session only");
}

#[cfg(test)]
mod tests {
    use std::{
        collections::BTreeMap,
        fs,
        io::Write,
        path::{Path, PathBuf},
        str::FromStr,
        sync::Mutex,
    };

    use sha2::{Digest, Sha256};

    use super::{
        assert_local_runtime_records_complete, assert_lock_matches_manifest_tool,
        assert_lock_record_matches_source_and_target, assert_runtime_record_matches_lock,
        binpm_home_from_values, capture_local_remove_state, capture_runtime_tool_state,
        checksum_digest_from_text, checksum_manifest_candidates, checksum_sidecar_candidates,
        cleanup_failed_install_cache, command_alias_differs_from_upstream,
        commit_deferred_cache_hit, declared_only_local_tools, deterministic_installed_path,
        download_asset_name, download_initial_capacity,
        ensure_no_package_record_install_path_collision, execute_command, format_download_progress,
        format_outdated_tool_line, github_sha256_digest, global_update_selected_binary,
        has_current_cache_record, has_local_runtime_or_lock_state, install_local_from_lock,
        install_path_collision_key, is_retryable_status, local_manifest_orphan_cmds,
        local_runtime_lock_records, local_runtime_record_matches_manifest,
        local_runtime_tool_record, local_tool_execution_ready,
        local_update_manifest_with_latest_versions_from, lock_targets_conflict_with_manifest,
        lock_targets_conflict_with_record, locked_record_download_request,
        locked_record_signature_sidecar, locked_record_verified_download_request,
        locked_release_lookup_spec, lockfile_digest, manifest_checksum_source,
        manifest_creation_root_from, manifest_project_root_from,
        manifest_root_or_creation_root_from, manifest_target_override, manifest_tool_from_source,
        normalize_bin_selection, override_snippet_candidate, package_record_output,
        package_shortcut_command, parse_manifest_source, parse_manifest_tool_source,
        parse_source_argument, prepare_global_updates, project_root_from,
        read_archive_selected_binary, record_has_signature_evidence,
        record_matches_current_provider_digest, regex_escape, release_asset_download_request,
        release_diagnostic_lines, release_diagnostics, remove_global_tool_from_paths,
        remove_local_manifest_orphans, require_executable_managed_file,
        resolved_has_supported_signature_evidence, resolved_has_verified_source,
        restore_local_remove_state, restore_runtime_tool_state, sanitize_download_diagnostic_url,
        select_manifest_asset, selected_asset_display_url, selected_global_package_records,
        shell_path, shell_quote, signature_sidecar_for_asset, sigstore_trust_policy,
        snapshot_cache_metadata, target_override_snippet, update_manifest_tool_source,
        validate_frozen_update_current_release, validate_locked_record_artifact,
        validate_locked_record_current_asset, validate_locked_record_current_provider_digest,
        validate_package_record_metadata, validate_package_record_source_identity,
        validate_provider_digest_evidence, validate_selected_manifest_entries, verification_state,
        verify_check_output, verify_check_output_with_state, verify_installed_binary_contents,
        verify_lockfile_records, verify_runtime_cache_bytes, write_sigstore_verification_inputs,
        zip_file_is_regular, zip_file_is_symlink, ArtifactKind, InstalledPackage,
        InstalledPathSnapshot, LocalRemoveState, OutdatedToolOutput, OutputMode, RuntimeToolState,
        GITHUB_ASSET_DOWNLOAD_ACCEPT,
    };
    use crate::{
        assets::CandidateDecision,
        cli::Shell,
        contract::{
            ArchiveFormat, ChecksumSource, HostTarget, SourceProvider, SourceSpec, TargetArch,
            TargetLibc, TargetOs, VerificationState,
        },
        error::{BinpmError, Result},
        release::{Release, ReleaseAsset, ReleaseClient, ReleaseSelection},
        storage::{
            ensure_dir, managed_installed_path, package_record_path, read_cache_records,
            require_regular_managed_file, validate_installed_binary_path, write_cache_record,
            write_lockfile, write_manifest, write_package_record, CachePaths, CacheRecord,
            LockTool, Lockfile, Manifest, ManifestTargetOverride, ManifestTool, PackageRecord,
            ResolvedAsset, ScopePaths, LOCKFILE_FILE, MANIFEST_FILE,
        },
    };

    static ENV_LOCK: Mutex<()> = Mutex::new(());

    struct StaticReleaseClient {
        tag: &'static str,
        assets: Vec<ReleaseAsset>,
    }

    impl ReleaseClient for StaticReleaseClient {
        fn list_releases(&self, _source: &SourceSpec) -> Result<Vec<Release>> {
            Ok(vec![Release {
                tag: self.tag.to_string(),
                assets: self.assets.clone(),
                stable: true,
                released_at: None,
                stability_reason: None,
            }])
        }

        fn resolve_release(&self, _source: &SourceSpec) -> Result<ReleaseSelection> {
            Ok(ReleaseSelection {
                release: Release {
                    tag: self.tag.to_string(),
                    assets: self.assets.clone(),
                    stable: true,
                    released_at: None,
                    stability_reason: None,
                },
                decision: "test release".to_string(),
                skipped: Vec::new(),
            })
        }
    }

    #[test]
    fn read_only_source_argument_accepts_shorthand_with_slash_bearing_tag() {
        let spec = parse_source_argument("owner/tool@nightly/2026-06-21")
            .expect("parse source argument")
            .expect("source spec");

        assert_eq!(spec.provider, SourceProvider::GitHub);
        assert_eq!(spec.host, "github.com");
        assert_eq!(spec.path, "owner/tool");
        assert_eq!(spec.version.as_deref(), Some("nightly/2026-06-21"));
    }

    #[test]
    fn package_shortcut_command_accepts_normalized_github_shorthand() {
        assert_eq!(
            package_shortcut_command(Some("owner/tool"), None).expect("owner/repo shorthand"),
            "tool"
        );
        assert_eq!(
            package_shortcut_command(Some("https://github.com/owner/tool"), None)
                .expect("url shorthand"),
            "tool"
        );
    }

    fn release_asset_from_record(record: &PackageRecord) -> ReleaseAsset {
        ReleaseAsset {
            name: record.asset_name.clone(),
            url: record.asset_url.clone(),
            provider_url: None,
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: record
                .provider_digest_sha256
                .as_ref()
                .map(|sha256| format!("sha256:{sha256}")),
            source_archive: false,
            final_url_https: None,
            final_url: None,
        }
    }

    #[test]
    fn outdated_human_row_includes_reinstall_source() {
        assert_eq!(
            format_outdated_tool_line("tool", "1.0.0", "1.1.0", "github:owner/tool"),
            "tool 1.0.0 -> 1.1.0 (github:owner/tool)"
        );
    }

    #[test]
    fn outdated_json_tool_includes_reinstall_source() {
        let payload = serde_json::to_value(OutdatedToolOutput {
            cmd: "tool".to_string(),
            source: "github:owner/tool".to_string(),
            current: "1.0.0".to_string(),
            latest: "1.1.0".to_string(),
            outdated: true,
        })
        .expect("serialize outdated tool");

        assert_eq!(payload["source"], "github:owner/tool");
    }

    #[test]
    fn local_update_manifest_advances_pinned_versions_only() {
        let mut manifest = Manifest {
            version: 1,
            tools: BTreeMap::new(),
        };
        manifest.tools.insert(
            "pinned".to_string(),
            ManifestTool {
                source: "github:owner/pinned".to_string(),
                version: Some("1.0.0".to_string()),
                bin: Some("pinned-bin".to_string()),
                targets: BTreeMap::new(),
            },
        );
        manifest.tools.insert(
            "floating".to_string(),
            ManifestTool {
                source: "github:owner/floating".to_string(),
                version: None,
                bin: None,
                targets: BTreeMap::new(),
            },
        );

        let (next_manifest, changed) =
            local_update_manifest_with_latest_versions_from(&manifest, &[], |tool| {
                Ok(match tool.source.as_str() {
                    "github:owner/pinned" => "2.0.0".to_string(),
                    other => panic!("unexpected latest lookup for {other}"),
                })
            })
            .expect("update manifest");

        assert!(changed);
        assert_eq!(
            next_manifest
                .tools
                .get("pinned")
                .expect("pinned tool")
                .version
                .as_deref(),
            Some("2.0.0")
        );
        assert_eq!(
            next_manifest
                .tools
                .get("pinned")
                .expect("pinned tool")
                .bin
                .as_deref(),
            Some("pinned-bin")
        );
        assert!(next_manifest
            .tools
            .get("floating")
            .expect("floating tool")
            .version
            .is_none());
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
    fn checksum_text_parses_single_digest_sidecar() {
        let digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
        assert_eq!(
            checksum_digest_from_text(digest, "tool-linux.tar.gz", true)
                .expect("checksum text")
                .as_deref(),
            Some(digest)
        );
        assert_eq!(
            checksum_digest_from_text(digest, "tool-linux.tar.gz", false).expect("checksum text"),
            None
        );
    }

    #[test]
    fn checksum_text_matches_selected_asset_in_manifest() {
        let selected = "tool-linux.tar.gz";
        let digest = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789";
        let other = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
        let text = format!("{other}  tool-darwin.tar.gz\n{digest} *./dist/{selected}\n");

        assert_eq!(
            checksum_digest_from_text(&text, selected, false)
                .expect("checksum text")
                .as_deref(),
            Some(digest)
        );
    }

    #[test]
    fn checksum_candidates_prefer_exact_sidecars_before_manifests() {
        let assets = vec![
            ReleaseAsset {
                name: "SHA256SUMS".to_string(),
                url: "https://example.com/SHA256SUMS".to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            },
            ReleaseAsset {
                name: "tool-linux.tar.gz.sha256".to_string(),
                url: "https://example.com/tool-linux.tar.gz.sha256".to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            },
            ReleaseAsset {
                name: "checksums.txt".to_string(),
                url: "https://example.com/checksums.txt".to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            },
        ];

        assert_eq!(
            checksum_sidecar_candidates("tool-linux.tar.gz", &assets)
                .iter()
                .map(|asset| asset.name.as_str())
                .collect::<Vec<_>>(),
            vec!["tool-linux.tar.gz.sha256"]
        );
        assert_eq!(
            checksum_manifest_candidates("tool-linux.tar.gz", &assets)
                .iter()
                .map(|asset| asset.name.as_str())
                .collect::<Vec<_>>(),
            vec!["SHA256SUMS", "checksums.txt"]
        );
    }

    #[test]
    fn checksum_download_request_prefers_provider_url_before_link_url() {
        let asset = ReleaseAsset {
            name: "tool-linux.tar.gz.sha256".to_string(),
            url: "https://cdn.example.com/tool-linux.tar.gz.sha256".to_string(),
            provider_url: Some(
                "https://gitlab.example.com/owner/tool/-/releases/v1/downloads/tool-linux.tar.gz.sha256"
                    .to_string(),
            ),
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
            final_url: None,
        };

        let request = release_asset_download_request(&asset).expect("download request");

        assert_eq!(
            request.url,
            "https://gitlab.example.com/owner/tool/-/releases/v1/downloads/tool-linux.tar.gz.sha256"
        );
    }

    #[test]
    fn download_progress_format_is_human_readable() {
        assert_eq!(
            format_download_progress(5 * 1024 * 1024, Some(10 * 1024 * 1024)),
            "5.0 MiB/10.0 MiB"
        );
    }

    #[test]
    fn download_initial_capacity_caps_untrusted_content_length() {
        assert_eq!(download_initial_capacity(None), 0);
        assert_eq!(download_initial_capacity(Some(128 * 1024)), 128 * 1024);
        assert_eq!(
            download_initial_capacity(Some(128 * 1024 * 1024)),
            8 * 1024 * 1024
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
    fn archive_extraction_does_not_recover_explicitly_non_executable_zip_entry() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip(&archive_path, &[("pkg/tool", b"config".as_slice(), 0o644)]);

        let error = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect_err("explicitly non-executable zip entry is not recovered");

        assert!(matches!(error, BinpmError::ArchiveBinaryNotFound { .. }));
    }

    #[test]
    fn archive_extraction_recovers_explicit_member_without_unix_permissions() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip_without_unix_permissions(
            &archive_path,
            &[
                ("pkg/foo", b"#!/bin/sh\necho foo\n".as_slice()),
                ("pkg/bar", b"#!/bin/sh\necho bar\n".as_slice()),
            ],
        );

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            Some("pkg/foo"),
        )
        .expect("explicit missing-metadata member is recovered");

        assert_eq!(selected.path, "pkg/foo");
        assert_eq!(selected.bytes, b"#!/bin/sh\necho foo\n");
    }

    #[test]
    fn zip_extraction_recovers_missing_executable_metadata_for_unambiguous_binary() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip_without_unix_permissions(
            &archive_path,
            &[
                ("pkg/README.md", b"docs".as_slice()),
                ("pkg/tool", b"#!/bin/sh\necho recovered\n".as_slice()),
            ],
        );

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect("selected binary with recovered executable bit");

        assert_eq!(selected.path, "pkg/tool");
        assert_eq!(selected.bytes, b"#!/bin/sh\necho recovered\n");

        let installed_path = temp_dir.path().join("bin").join("tool");
        crate::storage::install_executable_bytes(&installed_path, &selected.bytes)
            .expect("install recovered binary");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;

            let mode = fs::metadata(&installed_path)
                .expect("installed metadata")
                .permissions()
                .mode();
            assert_ne!(mode & 0o111, 0);
        }
    }

    #[test]
    fn zip_extraction_treats_dos_attributes_as_missing_executable_metadata() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip_with_dos_archive_attributes(
            &archive_path,
            &[
                (
                    "pkg/install.sh",
                    b"#!/bin/sh\necho install\n".as_slice(),
                    0o100755,
                ),
                (
                    "pkg/tool",
                    b"#!/bin/sh\necho recovered\n".as_slice(),
                    0o100644,
                ),
            ],
        );

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect("selected repo binary with DOS-only metadata");

        assert_eq!(selected.path, "pkg/tool");
        assert_eq!(selected.bytes, b"#!/bin/sh\necho recovered\n");
    }

    #[test]
    fn zip_extraction_recovers_missing_metadata_with_prepended_data() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip_without_unix_permissions(
            &archive_path,
            &[
                ("pkg/README.md", b"docs".as_slice()),
                ("pkg/tool", b"#!/bin/sh\necho recovered\n".as_slice()),
            ],
        );
        prepend_zip_data(&archive_path, b"#!/bin/sh\nexit 0\n");

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect("selected repo binary from prepended zip");

        assert_eq!(selected.path, "pkg/tool");
        assert_eq!(selected.bytes, b"#!/bin/sh\necho recovered\n");
    }

    #[test]
    fn zip_extraction_ignores_false_eocd_signature_in_comment() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip_without_unix_permissions(
            &archive_path,
            &[
                ("pkg/README.md", b"docs".as_slice()),
                ("pkg/tool", b"#!/bin/sh\necho recovered\n".as_slice()),
            ],
        );
        append_zip_comment(&archive_path, b"comment PK\x05\x06 fake footer");

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect("selected repo binary despite false EOCD signature in comment");

        assert_eq!(selected.path, "pkg/tool");
        assert_eq!(selected.bytes, b"#!/bin/sh\necho recovered\n");
    }

    #[test]
    fn zip_extraction_recovers_missing_metadata_for_legacy_encoded_name() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip_without_unix_permissions(
            &archive_path,
            &[
                ("README.md", b"docs".as_slice()),
                ("x/tool", b"#!/bin/sh\necho recovered\n".as_slice()),
            ],
        );
        patch_zip_member_raw_name(&archive_path, b"x/tool", b"\x82/tool");

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect("selected repo binary with CP437 name");

        assert_eq!(selected.path, "\u{e9}/tool");
        assert_eq!(selected.bytes, b"#!/bin/sh\necho recovered\n");
    }

    #[test]
    fn zip_extraction_skips_false_central_directory_signature_in_payload() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        let mut payload = b"prefix PK\x01\x02".to_vec();
        payload.extend([0xff; 64]);
        write_zip_without_unix_permissions(
            &archive_path,
            &[
                ("pkg/README.md", payload.as_slice()),
                ("pkg/tool", b"#!/bin/sh\necho recovered\n".as_slice()),
            ],
        );

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect("selected repo binary despite false central-directory signature");

        assert_eq!(selected.path, "pkg/tool");
        assert_eq!(selected.bytes, b"#!/bin/sh\necho recovered\n");
    }

    #[test]
    fn zip_extraction_uses_zip64_bounds_before_payload_signatures() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        let mut payload = b"prefix PK\x01\x02".to_vec();
        payload.extend([0xff; 64]);
        write_zip_without_unix_permissions(
            &archive_path,
            &[
                ("pkg/README.md", payload.as_slice()),
                ("pkg/tool", b"#!/bin/sh\necho recovered\n".as_slice()),
            ],
        );
        patch_zip_to_use_zip64_central_directory_bounds(&archive_path);

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect("selected repo binary despite ZIP64 placeholders and payload signature");

        assert_eq!(selected.path, "pkg/tool");
        assert_eq!(selected.bytes, b"#!/bin/sh\necho recovered\n");
    }

    #[test]
    fn zip_extraction_treats_zero_unix_mode_as_missing_executable_metadata() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip_with_unix_zero_attributes(
            &archive_path,
            &[("pkg/tool", b"#!/bin/sh\necho recovered\n".as_slice())],
        );

        let selected = read_archive_selected_binary(
            &archive_path,
            ArchiveFormat::Zip,
            "tool.zip",
            "tool",
            &linux_target(),
            None,
        )
        .expect("selected repo binary with zero Unix mode");

        assert_eq!(selected.path, "pkg/tool");
        assert_eq!(selected.bytes, b"#!/bin/sh\necho recovered\n");
    }

    #[test]
    fn archive_extraction_does_not_guess_between_non_executable_candidates() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip_without_unix_permissions(
            &archive_path,
            &[
                ("pkg/alpha", b"#!/bin/sh\nexit 0\n".as_slice()),
                ("pkg/beta", b"#!/bin/sh\nexit 0\n".as_slice()),
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
        .expect_err("non-executable members without name signal are not guessed");

        assert!(matches!(error, BinpmError::ArchiveBinaryNotFound { .. }));
        assert!(error
            .to_string()
            .contains("unambiguous filename/target match"));
    }

    #[test]
    fn archive_extraction_ignores_exe_candidates_on_non_windows_targets() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let archive_path = temp_dir.path().join("tool.zip");
        write_zip_without_unix_permissions(
            &archive_path,
            &[("pkg/tool.exe", b"windows".as_slice())],
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
        let expected_work_dir = work_dir.canonicalize().unwrap_or_else(|_| work_dir.clone());
        assert!(output.contains(&format!("pwd={}", expected_work_dir.display())));
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

        let updated = update_manifest_tool_source(Some(existing), &spec, None, None);

        assert_eq!(updated.source, "github:owner/new-tool");
        assert_eq!(updated.version.as_deref(), Some("2.0.0"));
        assert_eq!(updated.bin.as_deref(), Some("custom-bin"));
        assert_eq!(
            updated.targets.keys().collect::<Vec<_>>(),
            targets.keys().collect::<Vec<_>>()
        );
    }

    #[test]
    fn manifest_tool_source_update_persists_explicit_bin() {
        let spec = SourceSpec::from_str("github:owner/new-tool@2.0.0").expect("source");
        let existing = ManifestTool {
            source: "github:owner/old-tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: Some("old-bin".to_string()),
            targets: BTreeMap::new(),
        };

        let updated = update_manifest_tool_source(
            Some(existing),
            &spec,
            Some("dist/new-bin".to_string()),
            None,
        );

        assert_eq!(updated.source, "github:owner/new-tool");
        assert_eq!(updated.version.as_deref(), Some("2.0.0"));
        assert_eq!(updated.bin.as_deref(), Some("dist/new-bin"));
    }

    #[test]
    fn selected_global_package_records_reads_only_named_records() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::global(temp_dir.path().join("home"));
        let mut beta = package_record();
        beta.source = "github:owner/beta".to_string();
        write_package_record(&paths, "beta", &beta).expect("write beta");
        fs::write(paths.packages.join("alpha.toml"), b"not toml").expect("write corrupt alpha");

        let records = selected_global_package_records(&paths, &["beta".to_string()])
            .expect("selected record");

        assert_eq!(records.len(), 1);
        assert_eq!(records[0].0, "beta");
        assert_eq!(records[0].1.source, "github:owner/beta");
    }

    #[cfg(unix)]
    #[test]
    fn selected_global_package_records_rejects_symlinked_packages_dir() {
        use std::os::unix::fs::symlink;

        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::global(temp_dir.path().join("home"));
        let outside = temp_dir.path().join("outside");
        fs::create_dir_all(&paths.root).expect("scope root");
        fs::create_dir_all(&outside).expect("outside dir");
        symlink(&outside, &paths.packages).expect("symlink packages");

        let error = selected_global_package_records(&paths, &["beta".to_string()])
            .expect_err("symlinked packages dir");

        assert!(matches!(error, BinpmError::UnsafeManagedDirectory { .. }));
    }

    #[test]
    fn global_update_selected_binary_preserves_archive_member_path() {
        let mut record = package_record();
        record.archive_format = ArchiveFormat::Zip;
        record.selected_binary = "bin/tool".to_string();

        assert_eq!(
            global_update_selected_binary(&record).expect("selection"),
            Some("bin/tool".to_string())
        );
    }

    #[test]
    fn global_update_selected_binary_omits_bare_executable_override() {
        let mut record = package_record();
        record.archive_format = ArchiveFormat::BareExecutable;
        record.selected_binary = "tool-linux-x64".to_string();

        assert_eq!(
            global_update_selected_binary(&record).expect("selection"),
            None
        );
    }

    #[test]
    fn prepare_global_updates_validates_all_records_before_planning_installs() {
        let mut valid = package_record();
        valid.source = "github:owner/valid".to_string();
        let mut invalid = package_record();
        invalid.source = "github:".to_string();

        let error = prepare_global_updates(vec![
            ("valid".to_string(), valid),
            ("invalid".to_string(), invalid),
        ])
        .expect_err("invalid later record");

        assert!(matches!(error, BinpmError::InvalidSourceSpec { .. }));
    }

    #[test]
    fn prepare_global_updates_validates_record_command_names() {
        let record = package_record();

        let error = prepare_global_updates(vec![("bad:name".to_string(), record)])
            .expect_err("invalid command name");

        assert!(matches!(error, BinpmError::InvalidCommandName { .. }));
    }

    #[test]
    fn manifest_tool_source_update_persists_explicit_bin_to_current_target_override() {
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/new-tool@2.0.0").expect("source");
        let existing = ManifestTool {
            source: "github:owner/old-tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: Some("old-bin".to_string()),
            targets: BTreeMap::from([(
                target.key(),
                ManifestTargetOverride {
                    asset: "custom-asset".to_string(),
                    bin: "old-target-bin".to_string(),
                    checksum_source: None,
                },
            )]),
        };

        let updated = update_manifest_tool_source(
            Some(existing),
            &spec,
            Some("dist/new-bin".to_string()),
            Some(&target),
        );

        assert_eq!(updated.bin.as_deref(), Some("dist/new-bin"));
        assert_eq!(updated.targets[&target.key()].bin, "dist/new-bin");
    }

    #[test]
    fn bin_selection_normalization_rejects_empty_values() {
        assert_eq!(
            normalize_bin_selection(Some("  bin/tool  "))
                .expect("normalized bin")
                .as_deref(),
            Some("bin/tool")
        );
        assert!(matches!(
            normalize_bin_selection(Some("  ")),
            Err(BinpmError::InvalidBinSelection { .. })
        ));
    }

    #[test]
    fn ambiguity_errors_include_candidates_and_retry_suggestions() {
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source");
        let error = super::add_binary_retry_suggestions(
            BinpmError::AmbiguousArchiveBinaries {
                asset: "tool-linux.tar.gz".to_string(),
                candidates: vec!["bin/alpha".to_string(), "bin/beta".to_string()],
                suggestions: Vec::new(),
            },
            "tool",
            &spec,
            true,
        );
        let message = error.to_string();

        assert!(message.contains("bin/alpha"));
        assert!(message.contains("bin/beta"));
        assert!(message.contains("binpm add tool github:owner/tool@1.0.0 --bin bin/alpha"));
        assert!(message.contains("binpm x --package github:owner/tool@1.0.0 --bin bin/beta tool"));
        assert!(message.contains("--also <cmd=upstream-binary>"));
        assert!(message.contains("separate `[tools.<cmd>]`"));
    }

    #[test]
    fn install_summary_alias_comparison_uses_upstream_basename() {
        assert!(!command_alias_differs_from_upstream("rg", "rg"));
        assert!(!command_alias_differs_from_upstream("rg", "bin/rg"));
        assert!(command_alias_differs_from_upstream("ripgrep", "bin/rg"));
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

        let error = match install_local_from_lock(
            temp_dir.path(),
            "tool",
            &spec,
            None,
            false,
            OutputMode::Human,
        ) {
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
    fn manifest_source_rejects_shorthand_sources() {
        for raw in ["owner/tool", "https://github.com/owner/tool"] {
            let error = parse_manifest_source(raw).expect_err("manifest shorthand");

            assert!(matches!(error, BinpmError::InvalidSourceSpec { .. }));
        }
    }

    #[test]
    fn manifest_version_rejects_unsupported_selectors() {
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("beta".to_string()),
            bin: None,
            targets: BTreeMap::new(),
        };

        let error = parse_manifest_tool_source(&tool).expect_err("unsupported selector");

        assert!(error
            .to_string()
            .contains("channel selectors are not supported"));
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

        let updated = update_manifest_tool_source(Some(existing), &spec, None, None);

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

        let error = verify_lockfile_records(
            &temp_dir.path().join("binpm.lock"),
            lockfile,
            None,
            true,
            OutputMode::Human,
        )
        .expect_err("unverified target is rejected");

        assert!(error.to_string().contains("github:owner/tool@1.0.0"));
    }

    #[test]
    fn json_lockfile_verify_check_reports_target_record() {
        let mut record = package_record();
        mark_github_verified(&mut record);

        let check = verify_check_output("tool".to_string(), Some(linux_target()), &record);

        assert_eq!(check.cmd, "tool");
        let target = check.target.expect("target");
        assert_eq!(target.os, TargetOs::Linux);
        assert_eq!(target.arch, TargetArch::X86_64);
        assert_eq!(target.libc, TargetLibc::Gnu);
        assert_eq!(check.checksum_source, ChecksumSource::GitHubDigest);
        assert_eq!(check.verification, VerificationState::Verified);
    }

    #[test]
    fn json_verify_check_can_report_reverified_signature_record() {
        let mut record = package_record();
        record.checksum_source = ChecksumSource::Signature;
        record.signature_available = true;
        record.signature_verified = true;

        let check = verify_check_output_with_state(
            "tool".to_string(),
            None,
            &record,
            VerificationState::Verified,
        );

        assert_eq!(check.checksum_source, ChecksumSource::Signature);
        assert_eq!(check.verification, VerificationState::Verified);
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

        let error = verify_lockfile_records(
            &temp_dir.path().join("binpm.lock"),
            lockfile,
            None,
            false,
            OutputMode::Human,
        )
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
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        let mut record = package_record();
        mark_github_verified(&mut record);
        write_package_record(&paths, "tool", &record).expect("write package record");
        fs::create_dir(paths.bin.join("tool")).expect("write unreadable runtime path");
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

        assert!(matches!(
            error,
            BinpmError::FrozenLockfileOrphanCleanup { .. }
        ));
        let lockfile = crate::storage::read_lockfile(&temp_dir.path().join(LOCKFILE_FILE))
            .expect("read lockfile");
        assert!(lockfile.tools.contains_key("tool"));
    }

    #[test]
    fn orphan_key_scan_does_not_snapshot_runtime_paths() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        paths.ensure().expect("scope paths");
        write_package_record(&paths, "tool", &package_record()).expect("write package record");
        fs::create_dir(paths.bin.join("tool")).expect("write unreadable runtime path");
        let lockfile = Lockfile::default();

        let orphan_cmds = local_manifest_orphan_cmds(temp_dir.path(), &lockfile, &BTreeMap::new())
            .expect("scan orphan keys");

        assert!(orphan_cmds.contains("tool"));
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

        let error = verify_lockfile_records(
            &temp_dir.path().join("binpm.lock"),
            lockfile,
            None,
            true,
            OutputMode::Human,
        )
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
            OutputMode::Human,
        )
        .expect_err("manifest tool must be locked");

        let message = error.to_string();
        assert!(message.contains("stale"));
        assert!(!message.contains("Frozen lockfile"));
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
            OutputMode::Human,
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
        assert!(error.to_string().contains("linux-x86_64-gnu"));
        assert!(error.to_string().contains("[tools.<cmd>.targets."));

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
    fn target_override_rejects_non_installable_assets_with_actionable_message() {
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: None,
            targets: BTreeMap::from([(
                target.key(),
                ManifestTargetOverride {
                    asset: "Tool-1.0.0.dmg".to_string(),
                    bin: "tool".to_string(),
                    checksum_source: None,
                },
            )]),
        };

        let error = select_manifest_asset(
            &spec,
            Some(&tool),
            &target,
            &[release_asset("Tool-1.0.0.dmg")],
        )
        .expect_err("installer override rejected");
        let rendered = error.to_string();

        assert!(rendered.contains("target override selected `Tool-1.0.0.dmg`"));
        assert!(rendered.contains("choose an archive or bare executable"));
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
    fn reverified_runtime_signature_metadata_remains_lock_compatible() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let mut lock_record = package_record();
        lock_record.signature_available = true;
        lock_record.signature_verified = false;

        let mut runtime_record = lock_record.clone();
        runtime_record.checksum_source = ChecksumSource::Signature;
        runtime_record.signature_available = true;
        runtime_record.signature_verified = true;

        assert_runtime_record_matches_lock(temp_dir.path(), "tool", &lock_record, &runtime_record)
            .expect("reverified runtime signature metadata is compatible with the lock");
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
        paths.ensure().expect("create paths");
        let case_probe = paths.packages.join("case-probe");
        std::fs::write(&case_probe, "probe").expect("write case probe");
        let case_insensitive_records = paths.packages.join("CASE-PROBE").exists();
        std::fs::remove_file(&case_probe).expect("remove case probe");
        if case_insensitive_records {
            return;
        }
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
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: Some(false),
            final_url: Some("http://cdn.example.com/tool-linux?token=secret".to_string()),
        }];

        let error =
            select_manifest_asset(&spec, Some(&tool), &target, &assets).expect_err("unsafe URL");

        assert!(error
            .to_string()
            .contains("gitlab asset redirect target is not HTTPS"));
        assert!(error.to_string().contains("http://cdn.example.com"));
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
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            },
            ReleaseAsset {
                name: "tool-linux-x64".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64"
                    .to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
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
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            },
            ReleaseAsset {
                name: "tool-linux-x64".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64"
                    .to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
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
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
            final_url: None,
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
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
            final_url: None,
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
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            },
            ReleaseAsset {
                name: "tool-linux-x64".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64"
                    .to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
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
    fn explain_diagnostics_distinguish_installer_only_releases() {
        let target = linux_target();
        let assets = [
            ReleaseAsset {
                name: "Tool-1.0.0.dmg".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/Tool.dmg".to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            },
            ReleaseAsset {
                name: "Tool-1.0.0.msi".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/Tool.msi".to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            },
        ];
        let decisions = crate::assets::score_assets(SourceProvider::GitHub, &target, &assets);

        assert!(override_snippet_candidate(&decisions).is_none());
        let lines = release_diagnostic_lines(&decisions, &target);
        assert!(lines
            .iter()
            .any(|line| line.contains("release only provides unsupported")));
        assert!(lines
            .iter()
            .any(|line| line.contains("Tool-1.0.0.dmg, Tool-1.0.0.msi")));
        assert!(lines
            .iter()
            .any(|line| line.contains("portable archive or bare executable")));
    }

    #[test]
    fn explain_json_diagnostics_preserve_installer_remediation() {
        let target = linux_target();
        let assets = [
            ReleaseAsset {
                name: "Tool-1.0.0.dmg".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/Tool.dmg".to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            },
            ReleaseAsset {
                name: "Tool-1.0.0.msi".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/Tool.msi".to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
                final_url: None,
            },
        ];
        let decisions = crate::assets::score_assets(SourceProvider::GitHub, &target, &assets);
        let diagnostics = release_diagnostics(&decisions, &target);
        let payload = serde_json::to_value(&diagnostics[0]).expect("serialize diagnostic");

        assert_eq!(payload["kind"], "unsupported-installers");
        assert_eq!(payload["target"]["os"], "linux");
        assert_eq!(payload["target"]["arch"], "x86_64");
        assert_eq!(payload["target"]["libc"], "gnu");
        assert_eq!(payload["unsupported_installers"][0], "Tool-1.0.0.dmg");
        assert_eq!(payload["unsupported_installers"][1], "Tool-1.0.0.msi");
        assert!(payload["remediation"]
            .as_str()
            .expect("remediation")
            .contains("portable archive or bare executable"));
    }

    #[test]
    fn install_selection_failure_reports_installer_only_release_boundary() {
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let assets = [
            release_asset("Tool-1.0.0.dmg"),
            release_asset("Tool-1.0.0.msi"),
        ];

        let error =
            select_manifest_asset(&spec, None, &target, &assets).expect_err("installer-only");
        let rendered = error.to_string();

        assert!(rendered.contains("unsupported desktop or system installer packages"));
        assert!(rendered.contains("Tool-1.0.0.dmg, Tool-1.0.0.msi"));
        assert!(rendered.contains("portable archive or bare executable"));
    }

    #[test]
    fn explain_diagnostics_distinguish_source_archive_only_releases() {
        let target = linux_target();
        let assets = [
            ReleaseAsset {
                source_archive: true,
                ..release_asset("source.tar.gz")
            },
            ReleaseAsset {
                source_archive: true,
                ..release_asset("source.zip")
            },
            release_asset("source.tar.gz.sha256"),
        ];
        let decisions = crate::assets::score_assets(SourceProvider::GitHub, &target, &assets);

        assert!(override_snippet_candidate(&decisions).is_none());
        let diagnostics = release_diagnostics(&decisions, &target);
        let payload = serde_json::to_value(&diagnostics[0]).expect("serialize diagnostic");

        assert_eq!(payload["kind"], "source-archive-only");
        assert_eq!(payload["source_archives"][0], "source.zip");
        assert_eq!(payload["source_archives"][1], "source.tar.gz");
        assert!(payload["remediation"]
            .as_str()
            .expect("remediation")
            .contains("target-specific portable binary archive"));

        let lines = release_diagnostic_lines(&decisions, &target);
        assert!(lines
            .iter()
            .any(|line| line.contains("release only provides source archives")));
        assert!(lines
            .iter()
            .any(|line| line.contains("source_archives: source.zip, source.tar.gz")));
    }

    #[test]
    fn install_selection_failure_reports_source_archive_only_boundary() {
        let target = linux_target();
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let assets = [
            ReleaseAsset {
                source_archive: true,
                ..release_asset("source.tar.gz")
            },
            ReleaseAsset {
                source_archive: true,
                ..release_asset("source.zip")
            },
        ];

        let error = select_manifest_asset(&spec, None, &target, &assets).expect_err("source-only");
        let rendered = error.to_string();

        assert!(rendered.contains("release only provides source archives"));
        assert!(rendered.contains("source archives: source.zip, source.tar.gz"));
        assert!(rendered.contains("target-specific portable binary archive"));
    }

    #[test]
    fn explain_diagnostics_suggest_musl_override_for_missing_libc_assets() {
        let target = HostTarget {
            os: TargetOs::Linux,
            arch: TargetArch::X86_64,
            libc: TargetLibc::Musl,
        };
        let assets = [ReleaseAsset {
            name: "tool-linux-x64.tar.gz".to_string(),
            url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64.tar.gz"
                .to_string(),
            provider_url: None,
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
            final_url: None,
        }];
        let decisions = crate::assets::score_assets(SourceProvider::GitHub, &target, &assets);

        assert!(decisions.iter().all(|decision| !decision.eligible));
        assert_eq!(
            override_snippet_candidate(&decisions).map(|decision| decision.asset_name.as_str()),
            Some("tool-linux-x64.tar.gz")
        );
        let lines = release_diagnostic_lines(&decisions, &target);
        assert!(lines
            .iter()
            .any(|line| line.contains("target_mismatches: tool-linux-x64.tar.gz")));
        assert!(lines.iter().any(|line| {
            line.contains("download and inspect the binary outside binpm")
                && line.contains("[tools.<cmd>.targets.linux-x86_64-musl]")
        }));

        let diagnostics = release_diagnostics(&decisions, &target);
        let payload = serde_json::to_value(&diagnostics[0]).expect("serialize diagnostic");
        assert_eq!(payload["kind"], "target-scoring-remediation");
        assert_eq!(payload["target"]["libc"], "musl");
        assert_eq!(payload["target_mismatches"][0], "tool-linux-x64.tar.gz");
        assert!(payload["message"]
            .as_str()
            .expect("message")
            .contains("do not include a concrete libc"));
    }

    #[test]
    fn explain_diagnostics_distinguish_target_mismatch_failures() {
        let target = HostTarget {
            os: TargetOs::Darwin,
            arch: TargetArch::Aarch64,
            libc: TargetLibc::Any,
        };
        let assets = [release_asset("tool-linux-x64-gnu.tar.gz")];
        let decisions = crate::assets::score_assets(SourceProvider::GitHub, &target, &assets);
        let diagnostics = release_diagnostics(&decisions, &target);
        let payload = serde_json::to_value(&diagnostics[0]).expect("serialize diagnostic");

        assert_eq!(payload["kind"], "target-mismatch");
        assert_eq!(payload["target"]["os"], "darwin");
        assert_eq!(payload["target_mismatches"][0], "tool-linux-x64-gnu.tar.gz");
        assert!(payload["message"]
            .as_str()
            .expect("message")
            .contains("none match target darwin-aarch64-any"));
    }

    #[test]
    fn explain_diagnostics_reports_armv7_assets_missing_arch_token_as_target_mismatch() {
        let target = HostTarget {
            os: TargetOs::Linux,
            arch: TargetArch::Armv7,
            libc: TargetLibc::Gnu,
        };
        let assets = [release_asset("tool-linux.tar.gz")];
        let decisions = crate::assets::score_assets(SourceProvider::GitHub, &target, &assets);
        let diagnostics = release_diagnostics(&decisions, &target);
        let payload = serde_json::to_value(&diagnostics[0]).expect("serialize diagnostic");

        assert_eq!(payload["kind"], "target-mismatch");
        assert_eq!(payload["target"]["arch"], "armv7");
        assert_eq!(payload["target_mismatches"][0], "tool-linux.tar.gz");
        assert!(payload["message"]
            .as_str()
            .expect("message")
            .contains("none match target linux-armv7-gnu"));
    }

    #[test]
    fn explain_diagnostics_suggest_override_for_modern_only_compatible_assets() {
        let target = linux_target();
        let assets = [release_asset("tool-linux-x64-modern.tar.gz")];
        let decisions = crate::assets::score_assets(SourceProvider::GitHub, &target, &assets);

        assert!(decisions.iter().all(|decision| !decision.eligible));
        assert_eq!(
            override_snippet_candidate(&decisions).map(|decision| decision.asset_name.as_str()),
            Some("tool-linux-x64-modern.tar.gz")
        );
        let lines = release_diagnostic_lines(&decisions, &target);
        assert!(lines
            .iter()
            .any(|line| line.contains("CPU feature variants were detected")));
    }

    #[test]
    fn explain_diagnostics_suppress_modern_remediation_after_baseline_selection() {
        let target = linux_target();
        let assets = [
            release_asset("tool-linux-x64-baseline.tar.gz"),
            release_asset("tool-linux-x64-modern.tar.gz"),
        ];
        let decisions = crate::assets::score_assets(SourceProvider::GitHub, &target, &assets);

        assert!(decisions.iter().any(|decision| decision.asset_name
            == "tool-linux-x64-baseline.tar.gz"
            && decision.eligible));
        let lines = release_diagnostic_lines(&decisions, &target);
        assert!(!lines
            .iter()
            .any(|line| line.contains("CPU feature variants were detected")));
    }

    #[test]
    fn explain_diagnostics_do_not_suggest_modern_override_for_incompatible_target_assets() {
        let target = linux_target();
        let assets = [release_asset("tool-darwin-aarch64-modern.tar.gz")];
        let decisions = crate::assets::score_assets(SourceProvider::GitHub, &target, &assets);

        assert!(decisions.iter().all(|decision| !decision.eligible));
        assert!(decisions.iter().any(|decision| {
            decision.rejection_reason.as_deref() == Some("asset target does not match host target")
        }));
        assert!(override_snippet_candidate(&decisions).is_none());
    }

    #[test]
    fn explain_diagnostics_do_not_suggest_override_for_incompatible_target_assets() {
        let target = HostTarget {
            os: TargetOs::Darwin,
            arch: TargetArch::Aarch64,
            libc: TargetLibc::Any,
        };
        let assets = [ReleaseAsset {
            name: "tool-linux-x64.tar.gz".to_string(),
            url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64.tar.gz"
                .to_string(),
            provider_url: None,
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
            final_url: None,
        }];
        let decisions = crate::assets::score_assets(SourceProvider::GitHub, &target, &assets);

        assert!(decisions.iter().all(|decision| !decision.eligible));
        assert!(decisions.iter().any(|decision| {
            decision.rejection_reason.as_deref() == Some("asset target does not match host target")
        }));
        assert!(override_snippet_candidate(&decisions).is_none());
    }

    #[test]
    fn target_override_snippet_uses_canonical_key_and_toml_escaped_fields() {
        let target = linux_target();
        let snippet = target_override_snippet(
            "tool.name",
            &target,
            "tool-linux-x64.tar.gz",
            "bin/tool \"quoted\"",
            Some(ChecksumSource::Local),
        );

        assert!(snippet.starts_with("[tools.\"tool.name\".targets.linux-x86_64-gnu]"));
        assert!(snippet.contains("asset = \"tool-linux-x64.tar.gz\""));
        assert!(snippet.contains("bin = \"bin/tool \\\"quoted\\\"\""));
        assert!(!snippet.contains("checksum_source"));
        toml::from_str::<toml::Value>(&snippet).expect("valid TOML snippet");
    }

    #[test]
    fn target_override_snippet_keeps_manifest_accepted_checksum_source() {
        let target = linux_target();
        let snippet = target_override_snippet(
            "tool",
            &target,
            "tool-linux-x64.tar.gz",
            "tool",
            Some(ChecksumSource::GitHubDigest),
        );

        assert!(snippet.contains("checksum_source = \"github-digest\""));
        toml::from_str::<toml::Value>(&snippet).expect("valid TOML snippet");
    }

    #[test]
    fn explain_selected_asset_url_rejects_credentials_without_echoing_them() {
        let decision = CandidateDecision {
            asset_name: "tool".to_string(),
            canonical_url: "https://token@example.com/tool".to_string(),
            download_url: "https://token@example.com/tool".to_string(),
            download_auth: None,
            download_accept: None,
            kind: ArtifactKind::BareExecutable,
            detected_os: Some(TargetOs::Linux),
            detected_arch: Some(TargetArch::X86_64),
            detected_libc: Some(TargetLibc::Gnu),
            cpu_feature: None,
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
    fn package_record_json_rejects_unsafe_persisted_asset_url() {
        let mut record = package_record();
        record.asset_url =
            "https://github.com/owner/tool/releases/download/1.0.0/tool?token=secret#frag"
                .to_string();

        let error = package_record_output(&record).expect_err("unsafe persisted URL");

        assert!(error.to_string().contains("must not include query"));
        assert!(!error.to_string().contains("secret"));
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

        assert!(matches!(error, BinpmError::StaleLockfile { .. }));
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
    fn locked_record_download_request_uses_locked_asset_url() {
        let mut record = package_record();
        record.source = "github:ghe.no-token.example/owner/tool".to_string();
        record.source_host = "ghe.no-token.example".to_string();
        record.asset_url =
            "https://ghe.no-token.example/owner/tool/releases/download/1.0.0/locked-tool-linux"
                .to_string();
        record.asset_name = "tool-linux".to_string();

        let request = locked_record_download_request(&record).expect("download request");

        assert_eq!(request.url, record.asset_url);
        assert_eq!(request.auth, None);
        assert_eq!(request.accept, None);
    }

    #[test]
    fn locked_record_download_request_uses_provider_auth_for_provider_asset_url() {
        let _env_lock = ENV_LOCK.lock().expect("env lock");
        let mut record = package_record();
        record.source = "github:ghe.locked.example/owner/tool".to_string();
        record.source_host = "ghe.locked.example".to_string();
        record.asset_url =
            "https://ghe.locked.example/api/v3/repos/owner/tool/releases/assets/123".to_string();
        std::env::set_var(
            "BINPM_GITHUB_TOKEN_GHE_2E_LOCKED_2E_EXAMPLE",
            "locked-token",
        );

        let request = locked_record_download_request(&record).expect("download request");

        std::env::remove_var("BINPM_GITHUB_TOKEN_GHE_2E_LOCKED_2E_EXAMPLE");
        assert_eq!(request.url, record.asset_url);
        assert_eq!(request.accept, Some(GITHUB_ASSET_DOWNLOAD_ACCEPT));
        let auth = request.auth.expect("provider auth");
        assert_eq!(auth.header_name, "authorization");
        assert_eq!(auth.header_value, "Bearer locked-token");
        assert_eq!(auth.env_var, "BINPM_GITHUB_TOKEN_GHE_2E_LOCKED_2E_EXAMPLE");
    }

    #[test]
    fn locked_record_download_request_omits_provider_auth_for_external_asset_url() {
        let _env_lock = ENV_LOCK.lock().expect("env lock");
        let mut record = package_record();
        record.source = "gitlab:gitlab.locked.example/group/tool".to_string();
        record.source_provider = SourceProvider::GitLab;
        record.source_host = "gitlab.locked.example".to_string();
        record.source_path = "group/tool".to_string();
        record.asset_url = "https://cdn.locked.example/group/tool/releases/tool-linux".to_string();
        std::env::set_var(
            "BINPM_GITLAB_TOKEN_GITLAB_2E_LOCKED_2E_EXAMPLE",
            "locked-token",
        );

        let request = locked_record_download_request(&record).expect("download request");

        std::env::remove_var("BINPM_GITLAB_TOKEN_GITLAB_2E_LOCKED_2E_EXAMPLE");
        assert_eq!(request.url, record.asset_url);
        assert_eq!(request.auth, None);
        assert_eq!(request.accept, None);
    }

    #[test]
    fn locked_record_download_request_uses_gitlab_auth_for_provider_asset_url() {
        let _env_lock = ENV_LOCK.lock().expect("env lock");
        let mut record = package_record();
        record.source = "gitlab:gitlab.locked.example/group/tool".to_string();
        record.source_provider = SourceProvider::GitLab;
        record.source_host = "gitlab.locked.example".to_string();
        record.source_path = "group/tool".to_string();
        record.asset_url =
            "https://gitlab.locked.example/group/tool/-/releases/1.0.0/downloads/tool-linux"
                .to_string();
        std::env::set_var(
            "BINPM_GITLAB_TOKEN_GITLAB_2E_LOCKED_2E_EXAMPLE",
            "locked-token",
        );

        let request = locked_record_download_request(&record).expect("download request");

        std::env::remove_var("BINPM_GITLAB_TOKEN_GITLAB_2E_LOCKED_2E_EXAMPLE");
        assert_eq!(request.url, record.asset_url);
        let auth = request.auth.expect("provider auth");
        assert_eq!(auth.header_name, "PRIVATE-TOKEN");
        assert_eq!(auth.header_value, "locked-token");
        assert_eq!(
            auth.env_var,
            "BINPM_GITLAB_TOKEN_GITLAB_2E_LOCKED_2E_EXAMPLE"
        );
        assert_eq!(request.accept, None);
    }

    #[test]
    fn locked_record_verified_download_request_preserves_provider_auth_for_provider_asset_url() {
        let _env_lock = ENV_LOCK.lock().expect("env lock");
        let mut record = package_record();
        record.source = "github:ghe.locked.example/owner/tool".to_string();
        record.source_host = "ghe.locked.example".to_string();
        record.asset_url =
            "https://ghe.locked.example/owner/tool/releases/download/1.0.0/tool-linux".to_string();
        std::env::set_var(
            "BINPM_GITHUB_TOKEN_GHE_2E_LOCKED_2E_EXAMPLE",
            "locked-token",
        );

        let request = locked_record_verified_download_request(&record).expect("download request");

        std::env::remove_var("BINPM_GITHUB_TOKEN_GHE_2E_LOCKED_2E_EXAMPLE");
        assert_eq!(request.url, record.asset_url);
        assert_eq!(request.accept, Some(GITHUB_ASSET_DOWNLOAD_ACCEPT));
        let auth = request.auth.expect("provider auth");
        assert_eq!(auth.header_name, "authorization");
        assert_eq!(auth.header_value, "Bearer locked-token");
        assert_eq!(auth.env_var, "BINPM_GITHUB_TOKEN_GHE_2E_LOCKED_2E_EXAMPLE");
    }

    #[test]
    fn locked_record_verified_download_request_omits_provider_auth_for_external_asset_url() {
        let _env_lock = ENV_LOCK.lock().expect("env lock");
        let mut record = package_record();
        record.source = "gitlab:gitlab.locked.example/group/tool".to_string();
        record.source_provider = SourceProvider::GitLab;
        record.source_host = "gitlab.locked.example".to_string();
        record.source_path = "group/tool".to_string();
        record.asset_url = "https://cdn.locked.example/group/tool/releases/tool-linux".to_string();
        std::env::set_var(
            "BINPM_GITLAB_TOKEN_GITLAB_2E_LOCKED_2E_EXAMPLE",
            "locked-token",
        );

        let request = locked_record_verified_download_request(&record).expect("download request");

        std::env::remove_var("BINPM_GITLAB_TOKEN_GITLAB_2E_LOCKED_2E_EXAMPLE");
        assert_eq!(request.url, record.asset_url);
        assert_eq!(request.auth, None);
        assert_eq!(request.accept, None);
    }

    #[test]
    fn locked_record_signature_sidecar_uses_locked_asset_url() {
        let mut record = package_record();
        record.source = "github:ghe.no-token.example/owner/tool".to_string();
        record.source_host = "ghe.no-token.example".to_string();
        record.asset_url =
            "https://ghe.no-token.example/owner/tool/releases/download/1.0.0/locked-tool-linux"
                .to_string();
        record.asset_name = "locked-tool-linux".to_string();

        let sidecar = locked_record_signature_sidecar(&record).expect("signature sidecar");

        assert_eq!(sidecar.asset_name, "locked-tool-linux.sigstore.json");
        assert_eq!(
            sidecar.canonical_url,
            "https://ghe.no-token.example/owner/tool/releases/download/1.0.0/locked-tool-linux.sigstore.json"
        );
        assert_eq!(sidecar.download_url, sidecar.canonical_url);
        assert_eq!(sidecar.download_auth, None);
        assert_eq!(sidecar.download_accept, None);
    }

    #[test]
    fn locked_record_signature_sidecar_preserves_provider_auth_metadata() {
        let _env_lock = ENV_LOCK.lock().expect("env lock");
        let mut record = package_record();
        record.source = "github:ghe.locked.example/owner/tool".to_string();
        record.source_host = "ghe.locked.example".to_string();
        record.asset_url =
            "https://ghe.locked.example/owner/tool/releases/download/1.0.0/tool-linux".to_string();
        std::env::set_var(
            "BINPM_GITHUB_TOKEN_GHE_2E_LOCKED_2E_EXAMPLE",
            "locked-token",
        );

        let sidecar = locked_record_signature_sidecar(&record).expect("signature sidecar");

        std::env::remove_var("BINPM_GITHUB_TOKEN_GHE_2E_LOCKED_2E_EXAMPLE");
        assert_eq!(
            sidecar.download_url,
            "https://ghe.locked.example/owner/tool/releases/download/1.0.0/tool-linux.sigstore.json"
        );
        assert_eq!(sidecar.download_accept, Some(GITHUB_ASSET_DOWNLOAD_ACCEPT));
        let auth = sidecar.download_auth.expect("provider auth");
        assert_eq!(auth.header_name, "authorization");
        assert_eq!(auth.header_value, "Bearer locked-token");
        assert_eq!(auth.env_var, "BINPM_GITHUB_TOKEN_GHE_2E_LOCKED_2E_EXAMPLE");
    }

    #[test]
    fn locked_record_signature_sidecar_omits_provider_auth_for_external_asset_url() {
        let _env_lock = ENV_LOCK.lock().expect("env lock");
        let mut record = package_record();
        record.source = "gitlab:gitlab.locked.example/group/tool".to_string();
        record.source_provider = SourceProvider::GitLab;
        record.source_host = "gitlab.locked.example".to_string();
        record.source_path = "group/tool".to_string();
        record.asset_url = "https://cdn.locked.example/group/tool/releases/tool-linux".to_string();
        std::env::set_var(
            "BINPM_GITLAB_TOKEN_GITLAB_2E_LOCKED_2E_EXAMPLE",
            "locked-token",
        );

        let sidecar = locked_record_signature_sidecar(&record).expect("signature sidecar");

        std::env::remove_var("BINPM_GITLAB_TOKEN_GITLAB_2E_LOCKED_2E_EXAMPLE");
        assert_eq!(
            sidecar.download_url,
            "https://cdn.locked.example/group/tool/releases/tool-linux.sigstore.json"
        );
        assert_eq!(sidecar.download_auth, None);
        assert_eq!(sidecar.download_accept, None);
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
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            final_url: None,
            },
            ReleaseAsset {
                name: "tool-x86_64-unknown-linux-gnu".to_string(),
                url: "https://github.com/owner/tool/releases/download/1.0.0/tool-x86_64-unknown-linux-gnu"
                    .to_string(),
                provider_url: None,
                download_url: None,
                download_auth: None,
                download_accept: None,
                digest: None,
                source_archive: false,
                final_url_https: None,
            final_url: None,
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
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
            final_url: None,
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
    fn frozen_update_rejects_versionless_lock_when_latest_changed() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let spec = SourceSpec::from_str("github:owner/tool").expect("source spec");
        let mut record = package_record();
        record.package_spec = "github:owner/tool".to_string();
        record.requested_version = None;
        record.release_tag = "1.0.0".to_string();
        let client = StaticReleaseClient {
            tag: "1.1.0",
            assets: vec![],
        };

        let error = validate_frozen_update_current_release(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &spec,
            &record,
            &client,
        )
        .expect_err("latest moved");

        assert!(matches!(error, BinpmError::StaleLockfile { .. }));
    }

    #[test]
    fn frozen_update_accepts_versionless_lock_when_latest_matches() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let spec = SourceSpec::from_str("github:owner/tool").expect("source spec");
        let mut record = package_record();
        record.package_spec = "github:owner/tool".to_string();
        record.requested_version = None;
        record.release_tag = "1.0.0".to_string();
        let client = StaticReleaseClient {
            tag: "1.0.0",
            assets: vec![release_asset_from_record(&record)],
        };

        validate_frozen_update_current_release(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &spec,
            &record,
            &client,
        )
        .expect("latest still matches lock");
    }

    #[test]
    fn frozen_update_rejects_versionless_lock_when_current_asset_url_changed() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let spec = SourceSpec::from_str("github:owner/tool").expect("source spec");
        let mut record = package_record();
        record.package_spec = "github:owner/tool".to_string();
        record.requested_version = None;
        record.release_tag = "1.0.0".to_string();
        let mut asset = release_asset_from_record(&record);
        asset.url =
            "https://github.com/owner/tool/releases/download/1.0.0/new-tool-linux".to_string();
        let client = StaticReleaseClient {
            tag: "1.0.0",
            assets: vec![asset],
        };

        let error = validate_frozen_update_current_release(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &spec,
            &record,
            &client,
        )
        .expect_err("changed asset URL is stale");

        assert!(matches!(error, BinpmError::StaleLockfile { .. }));
    }

    #[test]
    fn frozen_update_rejects_versionless_lock_when_current_provider_digest_changed() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let spec = SourceSpec::from_str("github:owner/tool").expect("source spec");
        let mut record = package_record();
        mark_github_verified(&mut record);
        record.package_spec = "github:owner/tool".to_string();
        record.requested_version = None;
        record.release_tag = "1.0.0".to_string();
        let mut asset = release_asset_from_record(&record);
        asset.digest = Some(
            "sha256:1111111111111111111111111111111111111111111111111111111111111111".to_string(),
        );
        let client = StaticReleaseClient {
            tag: "1.0.0",
            assets: vec![asset],
        };

        let error = validate_frozen_update_current_release(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &spec,
            &record,
            &client,
        )
        .expect_err("changed provider digest is stale");

        assert!(matches!(error, BinpmError::StaleLockfile { .. }));
    }

    #[test]
    fn frozen_update_accepts_versioned_lock_when_release_asset_matches() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let record = package_record();
        let client = StaticReleaseClient {
            tag: "1.0.0",
            assets: vec![release_asset_from_record(&record)],
        };

        validate_frozen_update_current_release(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &spec,
            &record,
            &client,
        )
        .expect("versioned manifest pins still match release metadata");
    }

    #[test]
    fn frozen_update_rejects_versioned_lock_when_current_asset_url_changed() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let spec = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source spec");
        let record = package_record();
        let mut asset = release_asset_from_record(&record);
        asset.url =
            "https://github.com/owner/tool/releases/download/1.0.0/new-tool-linux".to_string();
        let client = StaticReleaseClient {
            tag: "1.0.0",
            assets: vec![asset],
        };

        let error = validate_frozen_update_current_release(
            &temp_dir.path().join("binpm.lock"),
            "tool",
            &spec,
            &record,
            &client,
        )
        .expect_err("changed exact-version asset URL is stale");

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
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: Some(format!("sha256:{changed_digest}")),
            source_archive: false,
            final_url_https: None,
            final_url: None,
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
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: Some(format!("sha256:{changed_digest}")),
            source_archive: false,
            final_url_https: None,
            final_url: None,
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
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: Some(format!("sha256:{}", record.sha256)),
            source_archive: false,
            final_url_https: None,
            final_url: None,
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
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
            final_url: None,
        }];

        assert!(!record_matches_current_provider_digest(&record, &assets));
    }

    #[test]
    fn signature_sidecar_discovery_matches_selected_asset_sidecar_only() {
        let record = package_record();
        let mut selected = release_asset_from_record(&record);
        selected.name = "tool-linux-amd64".to_string();
        let sidecar = ReleaseAsset {
            name: "tool-linux-amd64.sigstore.json".to_string(),
            url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-amd64.sigstore.json?token=secret".to_string(),
            provider_url: None,
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
            final_url: None,
        };
        let unrelated = ReleaseAsset {
            name: "other-linux-amd64.sigstore.json".to_string(),
            url: "https://github.com/owner/tool/releases/download/1.0.0/other-linux-amd64.sigstore.json".to_string(),
            provider_url: None,
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
            final_url: None,
        };

        let discovered =
            signature_sidecar_for_asset("tool-linux-amd64", &[selected, unrelated, sidecar])
                .expect("matching sigstore sidecar");

        assert_eq!(discovered.asset_name, "tool-linux-amd64.sigstore.json");
        assert_eq!(
            discovered.canonical_url,
            "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-amd64.sigstore.json"
        );
    }

    #[test]
    fn sigstore_trust_policy_is_github_actions_tag_scoped() {
        let mut resolved = resolved_asset(&package_record().sha256);
        resolved.source = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source");
        resolved.release_tag = "tool@v1.0.0".to_string();

        let policy = sigstore_trust_policy(&resolved).expect("github.com policy");

        assert_eq!(policy.name, "github-actions-tagged-release");
        assert_eq!(
            policy.identity_regexp,
            "^https://github\\.com/owner/tool/\\.github/workflows/[^@]+@refs/tags/tool@v1\\.0\\.0$"
        );

        resolved.source =
            SourceSpec::from_str("github:ghe.example.com/owner/tool@1.0.0").expect("ghe source");
        assert!(sigstore_trust_policy(&resolved).is_none());
        assert_eq!(
            regex_escape("tool+linux/v1.0.0"),
            "tool\\+linux\\/v1\\.0\\.0"
        );
    }

    #[test]
    fn resolved_signature_requires_successful_verification() {
        let mut resolved = resolved_asset(&package_record().sha256);
        resolved.provider_digest_sha256 = None;
        resolved.checksum_source = ChecksumSource::Signature;
        resolved.signature_available = true;

        assert!(!resolved_has_verified_source(&resolved));

        resolved.signature_verified = true;
        assert!(resolved_has_verified_source(&resolved));
    }

    #[test]
    fn strict_signature_evidence_requires_supported_policy() {
        let mut resolved = resolved_asset(&package_record().sha256);
        resolved.provider_digest_sha256 = None;
        resolved.checksum_source = ChecksumSource::Local;
        resolved.signature_available = true;

        assert!(resolved_has_supported_signature_evidence(&resolved));

        resolved.source =
            SourceSpec::from_str("github:ghe.example.com/owner/tool@1.0.0").expect("ghe source");
        assert!(!resolved_has_supported_signature_evidence(&resolved));

        resolved.source = SourceSpec::from_str("github:owner/tool@1.0.0").expect("source");
        resolved.signature_available = false;
        assert!(!resolved_has_supported_signature_evidence(&resolved));
    }

    #[test]
    fn verified_signature_record_reports_verified() {
        let mut record = package_record();
        record.checksum_source = ChecksumSource::Signature;
        record.signature_available = true;
        record.signature_verified = true;

        assert!(!record.has_verified_source());
        assert_eq!(verification_state(&record), VerificationState::Verified);
    }

    #[test]
    fn signature_evidence_allows_strict_recheck_when_sidecar_was_not_verified() {
        let mut record = package_record();
        record.checksum_source = ChecksumSource::Local;
        record.signature_available = true;
        record.signature_verified = false;

        assert!(record_has_signature_evidence(&record));
        assert!(!record.has_verified_source());

        record.signature_available = false;
        assert!(!record_has_signature_evidence(&record));
    }

    #[test]
    fn signature_evidence_requires_supported_record_policy() {
        let mut record = package_record();
        record.signature_available = true;

        assert!(record_has_signature_evidence(&record));

        record.source_host = "ghe.example.com".to_string();
        assert!(!record_has_signature_evidence(&record));

        record.source_host = "github.com".to_string();
        record.source_provider = SourceProvider::GitLab;
        assert!(!record_has_signature_evidence(&record));

        record.source_provider = SourceProvider::GitHub;
        record.source_path = "owner".to_string();
        assert!(!record_has_signature_evidence(&record));
    }

    #[cfg(unix)]
    #[test]
    fn sigstore_verification_inputs_reject_symlinked_tmp_dir() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let outside = tempfile::tempdir().expect("outside");
        let cache = CachePaths::new(temp_dir.path());
        std::os::unix::fs::symlink(outside.path(), &cache.tmp).expect("symlink tmp");
        let resolved = resolved_asset(&package_record().sha256);

        let error =
            write_sigstore_verification_inputs(&cache, &resolved, b"asset bytes", b"bundle bytes")
                .expect_err("symlinked temp dir");

        assert!(matches!(error, BinpmError::UnsafeManagedDirectory { .. }));
        assert!(std::fs::read_dir(outside.path())
            .expect("outside dir")
            .next()
            .is_none());
    }

    #[cfg(unix)]
    #[test]
    fn sigstore_verification_inputs_ignore_stale_precreated_symlink_file() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let outside = tempfile::tempdir().expect("outside");
        let cache = CachePaths::new(temp_dir.path());
        let resolved = resolved_asset(&package_record().sha256);
        ensure_dir(&cache.tmp).expect("tmp dir");
        let asset_path = cache.tmp.join("sigstore-stale.asset");
        let outside_target = outside.path().join("asset-target");
        std::os::unix::fs::symlink(&outside_target, &asset_path).expect("symlink asset");

        let paths =
            write_sigstore_verification_inputs(&cache, &resolved, b"asset bytes", b"bundle bytes")
                .expect("input write");

        assert_ne!(paths.asset_path, asset_path);
        assert!(!outside_target.exists());
    }

    #[test]
    fn sigstore_verification_inputs_use_attempt_unique_names() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let resolved = resolved_asset(&package_record().sha256);

        let first =
            write_sigstore_verification_inputs(&cache, &resolved, b"asset bytes", b"bundle bytes")
                .expect("first input write");
        let second =
            write_sigstore_verification_inputs(&cache, &resolved, b"asset bytes", b"bundle bytes")
                .expect("second input write");

        assert_ne!(first.asset_path, second.asset_path);
        assert_ne!(first.bundle_path, second.bundle_path);
    }

    #[test]
    fn package_record_local_checksum_accepts_matching_current_provider_digest() {
        let record = package_record();
        let assets = [ReleaseAsset {
            name: record.asset_name.clone(),
            url: record.asset_url.clone(),
            provider_url: None,
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: Some(format!("sha256:{}", record.sha256)),
            source_archive: false,
            final_url_https: None,
            final_url: None,
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

        let error = verify_lockfile_records(
            &temp_dir.path().join("binpm.lock"),
            lockfile,
            None,
            true,
            OutputMode::Human,
        )
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

        let error = verify_lockfile_records(
            &temp_dir.path().join("binpm.lock"),
            lockfile,
            None,
            true,
            OutputMode::Human,
        )
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

        let error = verify_lockfile_records(
            &temp_dir.path().join("binpm.lock"),
            lockfile,
            None,
            true,
            OutputMode::Human,
        )
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

        let error = verify_lockfile_records(
            &temp_dir.path().join("binpm.lock"),
            lockfile,
            None,
            true,
            OutputMode::Human,
        )
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
    fn failed_install_cleanup_preserves_new_verified_cache_entry_for_retry() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        cache.ensure().expect("cache paths");
        let bytes = b"downloaded tool";
        let sha256 = format!("{:x}", Sha256::digest(bytes));
        fs::create_dir_all(cache.entry_dir(&sha256)).expect("cache entry dir");
        fs::write(cache.asset_path(&sha256), bytes).expect("cache asset");
        write_cache_record(&cache, &cache_record(&sha256)).expect("cache record");
        let mut record = package_record();
        record.sha256 = sha256.clone();
        record.cache_key = Some(crate::storage::cache_key(&sha256));
        record.cache_path = Some(cache.asset_path(&sha256).display().to_string());
        let install = InstalledPackage {
            record,
            populated_cache_entry: true,
            deferred_cache_hit: None,
            cache_metadata_snapshot: None,
        };

        cleanup_failed_install_cache(&cache, &sha256, None, &install).expect("cleanup cache");

        assert_eq!(
            fs::read(cache.asset_path(&sha256)).expect("cache asset"),
            bytes
        );
        assert!(has_current_cache_record(&cache, &sha256).expect("cache record check"));
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
    fn local_tool_execution_ready_treats_non_regular_binary_as_stale() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let root = temp_dir.path();
        let target = HostTarget::current().expect("current target");
        let paths = ScopePaths::local(root.to_path_buf());
        paths.ensure().expect("local scope dirs");
        let installed_path = managed_installed_path(&paths, "tool", target.os);
        fs::create_dir(&installed_path).expect("create installed path directory");

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
            "non-regular managed binaries should be repairable by diagnostics"
        );
    }

    #[test]
    fn doctor_declared_only_scan_ignores_malformed_manifest() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        fs::write(temp_dir.path().join(MANIFEST_FILE), "version = [").expect("write manifest");

        let tools = declared_only_local_tools(temp_dir.path()).expect("declared-only scan");

        assert!(tools.is_empty());
    }

    #[test]
    fn doctor_declared_only_scan_ignores_malformed_lockfile() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let root = temp_dir.path();
        write_manifest(
            &root.join(MANIFEST_FILE),
            &Manifest {
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
            },
        )
        .expect("write manifest");
        fs::write(root.join(LOCKFILE_FILE), "version = [").expect("write malformed lockfile");

        let tools = declared_only_local_tools(root).expect("declared-only scan");

        assert!(tools.is_empty());
    }

    #[test]
    fn doctor_declared_only_scan_ignores_unsupported_lockfile_version() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let root = temp_dir.path();
        write_manifest(
            &root.join(MANIFEST_FILE),
            &Manifest {
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
            },
        )
        .expect("write manifest");
        fs::write(root.join(LOCKFILE_FILE), "version = 2\n").expect("write unsupported lockfile");

        let tools = declared_only_local_tools(root).expect("declared-only scan");

        assert!(tools.is_empty());
    }

    #[test]
    fn doctor_declared_only_scan_ignores_invalid_manifest_source_without_error() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let root = temp_dir.path();
        write_manifest(
            &root.join(MANIFEST_FILE),
            &Manifest {
                version: 1,
                tools: BTreeMap::from([(
                    "tool".to_string(),
                    ManifestTool {
                        source: "https://user:secret@[::1?token=secret".to_string(),
                        version: None,
                        bin: None,
                        targets: BTreeMap::new(),
                    },
                )]),
            },
        )
        .expect("write manifest");

        let tools = declared_only_local_tools(root).expect("declared-only scan");

        assert!(tools.is_empty());
    }

    #[test]
    fn doctor_declared_only_scan_surfaces_corrupt_package_record() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let root = temp_dir.path();
        write_manifest(
            &root.join(MANIFEST_FILE),
            &Manifest {
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
            },
        )
        .expect("write manifest");
        write_lockfile(
            &root.join(LOCKFILE_FILE),
            &Lockfile {
                version: 1,
                tools: BTreeMap::new(),
            },
        )
        .expect("write lockfile");
        let paths = ScopePaths::local(root.to_path_buf());
        paths.ensure().expect("ensure scope paths");
        fs::write(package_record_path(&paths, "tool"), "package_spec = [")
            .expect("write corrupt package record");

        let error = declared_only_local_tools(root).expect_err("corrupt package record");

        assert!(matches!(error, BinpmError::ParseToml { .. }));
    }

    #[test]
    fn doctor_declared_only_scan_reports_existing_stale_record() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let root = temp_dir.path();
        write_manifest(
            &root.join(MANIFEST_FILE),
            &Manifest {
                version: 1,
                tools: BTreeMap::from([(
                    "tool".to_string(),
                    ManifestTool {
                        source: "github:owner/replacement".to_string(),
                        version: Some("2.0.0".to_string()),
                        bin: None,
                        targets: BTreeMap::new(),
                    },
                )]),
            },
        )
        .expect("write manifest");
        let paths = ScopePaths::local(root.to_path_buf());
        write_package_record(&paths, "tool", &package_record()).expect("write stale record");

        let tools = declared_only_local_tools(root).expect("declared-only scan");

        assert_eq!(tools.len(), 1);
        assert_eq!(tools[0].cmd, "tool");
        assert_eq!(tools[0].source, "github:owner/replacement");
        assert_eq!(tools[0].requested_version.as_deref(), Some("2.0.0"));
    }

    #[test]
    fn doctor_declared_only_scan_skips_manifest_matching_runtime_without_lockfile() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let root = temp_dir.path();
        let target = HostTarget::current().expect("current target");
        write_manifest(
            &root.join(MANIFEST_FILE),
            &Manifest {
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
            },
        )
        .expect("write manifest");
        let paths = ScopePaths::local(root.to_path_buf());
        paths.ensure().expect("ensure scope paths");
        let installed_path = managed_installed_path(&paths, "tool", target.os);
        fs::write(&installed_path, b"#!/bin/sh\n").expect("write executable");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;

            let mut permissions = fs::metadata(&installed_path)
                .expect("metadata")
                .permissions();
            permissions.set_mode(0o755);
            fs::set_permissions(&installed_path, permissions).expect("chmod executable");
        }
        let mut record = package_record();
        record.target_os = target.os;
        record.target_arch = target.arch;
        record.target_libc = target.libc;
        record.installed_path = installed_path.display().to_string();
        write_package_record(&paths, "tool", &record).expect("write runtime record");

        let tools = declared_only_local_tools(root).expect("declared-only scan");

        assert!(tools.is_empty());
    }

    #[test]
    fn local_runtime_tool_record_treats_non_regular_binary_as_missing() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let root = temp_dir.path();
        let target = HostTarget::current().expect("current target");
        let paths = ScopePaths::local(root.to_path_buf());
        paths.ensure().expect("ensure scope paths");
        let installed_path = managed_installed_path(&paths, "tool", target.os);
        fs::create_dir(&installed_path).expect("create installed path directory");
        let mut record = package_record();
        record.target_os = target.os;
        record.target_arch = target.arch;
        record.target_libc = target.libc;
        record.installed_path = installed_path.display().to_string();
        write_package_record(&paths, "tool", &record).expect("write runtime record");

        let record = local_runtime_tool_record(root, "tool").expect("runtime record");

        assert!(record.is_none());
    }

    #[test]
    fn local_runtime_tool_record_reports_installed_record_without_lockfile() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let root = temp_dir.path();
        let paths = ScopePaths::local(root.to_path_buf());
        paths.ensure().expect("ensure scope paths");
        let installed_path = root.join(".binpm/bin/tool");
        let mut record = package_record();
        record.installed_path = installed_path.display().to_string();
        write_package_record(&paths, "tool", &record).expect("write runtime record");
        fs::write(&installed_path, b"#!/bin/sh\n").expect("write executable");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;

            let mut permissions = fs::metadata(&installed_path)
                .expect("metadata")
                .permissions();
            permissions.set_mode(0o755);
            fs::set_permissions(&installed_path, permissions).expect("chmod executable");
        }

        let record = local_runtime_tool_record(root, "tool")
            .expect("runtime record")
            .expect("installed");

        assert_eq!(record.source, "github:owner/tool");
        assert_eq!(record.installed_path, installed_path.display().to_string());
    }

    #[test]
    fn local_runtime_record_matches_manifest_rejects_stale_source_version_and_bin() {
        let target = HostTarget::current().expect("current target");
        let mut record = package_record();
        record.target_os = target.os;
        record.target_arch = target.arch;
        record.target_libc = target.libc;
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: None,
            targets: BTreeMap::new(),
        };

        assert!(local_runtime_record_matches_manifest(&tool, &record)
            .expect("matching manifest record"));

        let changed_source = ManifestTool {
            source: "github:owner/other".to_string(),
            ..tool.clone()
        };
        assert!(
            !local_runtime_record_matches_manifest(&changed_source, &record)
                .expect("source mismatch")
        );

        let changed_version = ManifestTool {
            version: Some("2.0.0".to_string()),
            ..tool.clone()
        };
        assert!(
            !local_runtime_record_matches_manifest(&changed_version, &record)
                .expect("version mismatch")
        );

        let changed_bin = ManifestTool {
            bin: Some("other-tool".to_string()),
            ..tool.clone()
        };
        assert!(
            !local_runtime_record_matches_manifest(&changed_bin, &record).expect("bin mismatch")
        );
    }

    #[test]
    fn local_runtime_record_matches_manifest_accepts_selected_binary_basename() {
        let target = HostTarget::current().expect("current target");
        let mut record = package_record();
        record.target_os = target.os;
        record.target_arch = target.arch;
        record.target_libc = target.libc;
        record.selected_binary = "dist/tool".to_string();
        let tool = ManifestTool {
            source: "github:owner/tool".to_string(),
            version: Some("1.0.0".to_string()),
            bin: Some("tool".to_string()),
            targets: BTreeMap::new(),
        };

        assert!(local_runtime_record_matches_manifest(&tool, &record).expect("basename bin match"));
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

    fn release_asset(name: &str) -> ReleaseAsset {
        ReleaseAsset {
            name: name.to_string(),
            url: format!("https://github.com/owner/tool/releases/download/1.0.0/{name}"),
            provider_url: None,
            download_url: None,
            download_auth: None,
            download_accept: None,
            digest: None,
            source_archive: false,
            final_url_https: None,
            final_url: None,
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
                download_auth: None,
                download_accept: None,
                kind: ArtifactKind::BareExecutable,
                detected_os: Some(TargetOs::Linux),
                detected_arch: Some(TargetArch::X86_64),
                detected_libc: Some(TargetLibc::Gnu),
                cpu_feature: None,
                score: None,
                eligible: true,
                recognized_pattern: true,
                rejection_reason: None,
            },
            archive_format: ArchiveFormat::BareExecutable,
            selected_binary: "tool-linux".to_string(),
            provider_digest_sha256: Some(sha256.to_string()),
            upstream_checksum_sha256: None,
            checksum_source: ChecksumSource::GitHubDigest,
            signature_sidecar: None,
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

    fn write_zip_without_unix_permissions(path: &Path, entries: &[(&str, &[u8])]) {
        let file = fs::File::create(path).expect("create zip");
        let mut writer = zip::ZipWriter::new(file);
        for (name, bytes) in entries {
            let options = zip::write::SimpleFileOptions::default()
                .compression_method(zip::CompressionMethod::Deflated);
            writer.start_file(*name, options).expect("start zip entry");
            writer.write_all(bytes).expect("write zip entry");
        }
        writer.finish().expect("finish zip");

        patch_zip_central_directory_external_attributes(path, 0, 0);
    }

    fn write_zip_with_dos_archive_attributes(path: &Path, entries: &[(&str, &[u8], u32)]) {
        write_zip(path, entries);

        patch_zip_central_directory_external_attributes(path, 0, 0x20);
    }

    fn write_zip_with_unix_zero_attributes(path: &Path, entries: &[(&str, &[u8])]) {
        let entries = entries
            .iter()
            .map(|(name, bytes)| (*name, *bytes, 0o100644))
            .collect::<Vec<_>>();
        write_zip(path, &entries);

        patch_zip_central_directory_external_attributes(path, 3, 0);
    }

    fn prepend_zip_data(path: &Path, prefix: &[u8]) {
        let bytes = fs::read(path).expect("read zip for prepending");
        let mut prefixed = prefix.to_vec();
        prefixed.extend(bytes);
        fs::write(path, prefixed).expect("write prepended zip");
    }

    fn append_zip_comment(path: &Path, comment: &[u8]) {
        const END_OF_CENTRAL_DIRECTORY_SIGNATURE: [u8; 4] = [0x50, 0x4b, 0x05, 0x06];
        const END_OF_CENTRAL_DIRECTORY_LEN: usize = 22;
        const END_OF_CENTRAL_DIRECTORY_COMMENT_LENGTH_OFFSET: usize = 20;

        let mut bytes = fs::read(path).expect("read zip for comment patch");
        assert!(comment.len() <= u16::MAX as usize);
        let eocd_index = bytes
            .len()
            .checked_sub(END_OF_CENTRAL_DIRECTORY_LEN)
            .expect("zip has EOCD");
        assert!(bytes[eocd_index..].starts_with(&END_OF_CENTRAL_DIRECTORY_SIGNATURE));
        bytes[eocd_index + END_OF_CENTRAL_DIRECTORY_COMMENT_LENGTH_OFFSET
            ..eocd_index + END_OF_CENTRAL_DIRECTORY_COMMENT_LENGTH_OFFSET + 2]
            .copy_from_slice(&(comment.len() as u16).to_le_bytes());
        bytes.extend_from_slice(comment);
        fs::write(path, bytes).expect("write zip comment patch");
    }

    fn patch_zip_member_raw_name(path: &Path, old_name: &[u8], new_name: &[u8]) {
        const LOCAL_FILE_SIGNATURE: [u8; 4] = [0x50, 0x4b, 0x03, 0x04];
        const LOCAL_FILE_HEADER_LEN: usize = 30;
        const LOCAL_FILE_FLAGS_OFFSET: usize = 6;
        const LOCAL_FILE_NAME_LENGTH_OFFSET: usize = 26;
        const LOCAL_FILE_EXTRA_FIELD_LENGTH_OFFSET: usize = 28;
        const CENTRAL_DIRECTORY_SIGNATURE: [u8; 4] = [0x50, 0x4b, 0x01, 0x02];
        const CENTRAL_DIRECTORY_HEADER_LEN: usize = 46;
        const CENTRAL_DIRECTORY_FLAGS_OFFSET: usize = 8;
        const CENTRAL_DIRECTORY_FILE_NAME_LENGTH_OFFSET: usize = 28;
        const CENTRAL_DIRECTORY_EXTRA_FIELD_LENGTH_OFFSET: usize = 30;
        const CENTRAL_DIRECTORY_FILE_COMMENT_LENGTH_OFFSET: usize = 32;
        const UTF8_FILE_NAME_FLAG: u16 = 1 << 11;

        assert_eq!(
            old_name.len(),
            new_name.len(),
            "raw ZIP name patches must preserve header sizes"
        );

        let mut bytes = fs::read(path).expect("read zip for name patch");
        let mut index = 0;
        while index + LOCAL_FILE_HEADER_LEN <= bytes.len() {
            if !bytes[index..].starts_with(&LOCAL_FILE_SIGNATURE) {
                index += 1;
                continue;
            }
            let name_len = u16::from_le_bytes([
                bytes[index + LOCAL_FILE_NAME_LENGTH_OFFSET],
                bytes[index + LOCAL_FILE_NAME_LENGTH_OFFSET + 1],
            ]) as usize;
            let extra_len = u16::from_le_bytes([
                bytes[index + LOCAL_FILE_EXTRA_FIELD_LENGTH_OFFSET],
                bytes[index + LOCAL_FILE_EXTRA_FIELD_LENGTH_OFFSET + 1],
            ]) as usize;
            let name_start = index + LOCAL_FILE_HEADER_LEN;
            let name_end = name_start + name_len;
            if name_end <= bytes.len() && &bytes[name_start..name_end] == old_name {
                bytes[name_start..name_end].copy_from_slice(new_name);
                let mut flags = u16::from_le_bytes([
                    bytes[index + LOCAL_FILE_FLAGS_OFFSET],
                    bytes[index + LOCAL_FILE_FLAGS_OFFSET + 1],
                ]);
                flags &= !UTF8_FILE_NAME_FLAG;
                bytes[index + LOCAL_FILE_FLAGS_OFFSET..index + LOCAL_FILE_FLAGS_OFFSET + 2]
                    .copy_from_slice(&flags.to_le_bytes());
            }
            index = name_end.saturating_add(extra_len);
        }

        let mut index = 0;
        while index + CENTRAL_DIRECTORY_HEADER_LEN <= bytes.len() {
            if !bytes[index..].starts_with(&CENTRAL_DIRECTORY_SIGNATURE) {
                index += 1;
                continue;
            }
            let name_len = u16::from_le_bytes([
                bytes[index + CENTRAL_DIRECTORY_FILE_NAME_LENGTH_OFFSET],
                bytes[index + CENTRAL_DIRECTORY_FILE_NAME_LENGTH_OFFSET + 1],
            ]) as usize;
            let extra_len = u16::from_le_bytes([
                bytes[index + CENTRAL_DIRECTORY_EXTRA_FIELD_LENGTH_OFFSET],
                bytes[index + CENTRAL_DIRECTORY_EXTRA_FIELD_LENGTH_OFFSET + 1],
            ]) as usize;
            let comment_len = u16::from_le_bytes([
                bytes[index + CENTRAL_DIRECTORY_FILE_COMMENT_LENGTH_OFFSET],
                bytes[index + CENTRAL_DIRECTORY_FILE_COMMENT_LENGTH_OFFSET + 1],
            ]) as usize;
            let name_start = index + CENTRAL_DIRECTORY_HEADER_LEN;
            let name_end = name_start + name_len;
            if name_end <= bytes.len() && &bytes[name_start..name_end] == old_name {
                bytes[name_start..name_end].copy_from_slice(new_name);
                let mut flags = u16::from_le_bytes([
                    bytes[index + CENTRAL_DIRECTORY_FLAGS_OFFSET],
                    bytes[index + CENTRAL_DIRECTORY_FLAGS_OFFSET + 1],
                ]);
                flags &= !UTF8_FILE_NAME_FLAG;
                bytes[index + CENTRAL_DIRECTORY_FLAGS_OFFSET
                    ..index + CENTRAL_DIRECTORY_FLAGS_OFFSET + 2]
                    .copy_from_slice(&flags.to_le_bytes());
            }
            index = name_end
                .saturating_add(extra_len)
                .saturating_add(comment_len);
        }
        fs::write(path, bytes).expect("write zip name patch");
    }

    fn patch_zip_to_use_zip64_central_directory_bounds(path: &Path) {
        const END_OF_CENTRAL_DIRECTORY_SIGNATURE: [u8; 4] = [0x50, 0x4b, 0x05, 0x06];
        const END_OF_CENTRAL_DIRECTORY_LEN: usize = 22;
        const END_OF_CENTRAL_DIRECTORY_ENTRY_COUNT_OFFSET: usize = 8;
        const CENTRAL_DIRECTORY_SIZE_OFFSET: usize = 12;
        const CENTRAL_DIRECTORY_OFFSET_OFFSET: usize = 16;
        const ZIP32_PLACEHOLDER_16: u16 = u16::MAX;
        const ZIP32_PLACEHOLDER_32: u32 = u32::MAX;

        let bytes = fs::read(path).expect("read zip for ZIP64 patch");
        let eocd_index = bytes
            .len()
            .checked_sub(END_OF_CENTRAL_DIRECTORY_LEN)
            .expect("zip has EOCD");
        assert!(bytes[eocd_index..].starts_with(&END_OF_CENTRAL_DIRECTORY_SIGNATURE));
        let directory_size = u32::from_le_bytes([
            bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET],
            bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET + 1],
            bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET + 2],
            bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET + 3],
        ]) as u64;
        let directory_start = u32::from_le_bytes([
            bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET],
            bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET + 1],
            bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET + 2],
            bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET + 3],
        ]) as u64;

        let mut zip64_eocd = Vec::new();
        zip64_eocd.extend_from_slice(&[0x50, 0x4b, 0x06, 0x06]);
        zip64_eocd.extend_from_slice(&44_u64.to_le_bytes());
        zip64_eocd.extend_from_slice(&45_u16.to_le_bytes());
        zip64_eocd.extend_from_slice(&45_u16.to_le_bytes());
        zip64_eocd.extend_from_slice(&0_u32.to_le_bytes());
        zip64_eocd.extend_from_slice(&0_u32.to_le_bytes());
        zip64_eocd.extend_from_slice(&2_u64.to_le_bytes());
        zip64_eocd.extend_from_slice(&2_u64.to_le_bytes());
        zip64_eocd.extend_from_slice(&directory_size.to_le_bytes());
        zip64_eocd.extend_from_slice(&directory_start.to_le_bytes());

        let mut zip64_locator = Vec::new();
        zip64_locator.extend_from_slice(&[0x50, 0x4b, 0x06, 0x07]);
        zip64_locator.extend_from_slice(&0_u32.to_le_bytes());
        zip64_locator.extend_from_slice(&(eocd_index as u64).to_le_bytes());
        zip64_locator.extend_from_slice(&1_u32.to_le_bytes());

        let mut patched = bytes[..eocd_index].to_vec();
        patched.extend_from_slice(&zip64_eocd);
        patched.extend_from_slice(&zip64_locator);
        patched.extend_from_slice(&bytes[eocd_index..]);
        let new_eocd_index = patched.len() - END_OF_CENTRAL_DIRECTORY_LEN;
        patched[new_eocd_index + END_OF_CENTRAL_DIRECTORY_ENTRY_COUNT_OFFSET
            ..new_eocd_index + END_OF_CENTRAL_DIRECTORY_ENTRY_COUNT_OFFSET + 2]
            .copy_from_slice(&ZIP32_PLACEHOLDER_16.to_le_bytes());
        patched[new_eocd_index + END_OF_CENTRAL_DIRECTORY_ENTRY_COUNT_OFFSET + 2
            ..new_eocd_index + END_OF_CENTRAL_DIRECTORY_ENTRY_COUNT_OFFSET + 4]
            .copy_from_slice(&ZIP32_PLACEHOLDER_16.to_le_bytes());
        patched[new_eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET
            ..new_eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET + 4]
            .copy_from_slice(&ZIP32_PLACEHOLDER_32.to_le_bytes());
        patched[new_eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET
            ..new_eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET + 4]
            .copy_from_slice(&ZIP32_PLACEHOLDER_32.to_le_bytes());

        fs::write(path, patched).expect("write ZIP64 bounds patch");
    }

    fn patch_zip_central_directory_external_attributes(path: &Path, system: u8, attributes: u32) {
        const CENTRAL_DIRECTORY_SIGNATURE: [u8; 4] = [0x50, 0x4b, 0x01, 0x02];
        const CENTRAL_DIRECTORY_HEADER_LEN: usize = 46;
        const VERSION_MADE_BY_SYSTEM_OFFSET: usize = 5;
        const FILE_NAME_LENGTH_OFFSET: usize = 28;
        const EXTRA_FIELD_LENGTH_OFFSET: usize = 30;
        const FILE_COMMENT_LENGTH_OFFSET: usize = 32;
        const EXTERNAL_FILE_ATTRIBUTES_OFFSET: usize = 38;
        const END_OF_CENTRAL_DIRECTORY_SIGNATURE: [u8; 4] = [0x50, 0x4b, 0x05, 0x06];
        const END_OF_CENTRAL_DIRECTORY_LEN: usize = 22;
        const CENTRAL_DIRECTORY_SIZE_OFFSET: usize = 12;
        const CENTRAL_DIRECTORY_OFFSET_OFFSET: usize = 16;

        let mut bytes = fs::read(path).expect("read zip for metadata patch");
        let eocd_index = bytes
            .len()
            .checked_sub(END_OF_CENTRAL_DIRECTORY_LEN)
            .and_then(|last_start| {
                let first_start = bytes
                    .len()
                    .saturating_sub(END_OF_CENTRAL_DIRECTORY_LEN + u16::MAX as usize);
                (first_start..=last_start)
                    .rev()
                    .find(|index| bytes[*index..].starts_with(&END_OF_CENTRAL_DIRECTORY_SIGNATURE))
            })
            .expect("find end of central directory");
        let directory_size = u32::from_le_bytes([
            bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET],
            bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET + 1],
            bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET + 2],
            bytes[eocd_index + CENTRAL_DIRECTORY_SIZE_OFFSET + 3],
        ]) as usize;
        let directory_start = u32::from_le_bytes([
            bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET],
            bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET + 1],
            bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET + 2],
            bytes[eocd_index + CENTRAL_DIRECTORY_OFFSET_OFFSET + 3],
        ]) as usize;
        let directory_end = directory_start + directory_size;

        let mut index = directory_start;
        while index + CENTRAL_DIRECTORY_HEADER_LEN <= directory_end {
            assert!(bytes[index..].starts_with(&CENTRAL_DIRECTORY_SIGNATURE));
            bytes[index + VERSION_MADE_BY_SYSTEM_OFFSET] = system;
            bytes[index + EXTERNAL_FILE_ATTRIBUTES_OFFSET
                ..index + EXTERNAL_FILE_ATTRIBUTES_OFFSET + 4]
                .copy_from_slice(&attributes.to_le_bytes());
            let name_len = u16::from_le_bytes([
                bytes[index + FILE_NAME_LENGTH_OFFSET],
                bytes[index + FILE_NAME_LENGTH_OFFSET + 1],
            ]) as usize;
            let extra_len = u16::from_le_bytes([
                bytes[index + EXTRA_FIELD_LENGTH_OFFSET],
                bytes[index + EXTRA_FIELD_LENGTH_OFFSET + 1],
            ]) as usize;
            let comment_len = u16::from_le_bytes([
                bytes[index + FILE_COMMENT_LENGTH_OFFSET],
                bytes[index + FILE_COMMENT_LENGTH_OFFSET + 1],
            ]) as usize;
            index += CENTRAL_DIRECTORY_HEADER_LEN + name_len + extra_len + comment_len;
        }
        fs::write(path, bytes).expect("write zip metadata patch");
    }

    fn mark_github_verified(record: &mut PackageRecord) {
        record.checksum_source = ChecksumSource::GitHubDigest;
        record.provider_digest_sha256 = Some(record.sha256.clone());
    }
}
