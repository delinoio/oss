use std::{
    collections::{BTreeSet, VecDeque},
    process::{Command, Output},
    sync::{Arc, Mutex},
    thread,
    time::Duration,
};

use reqwest::{
    header::{HeaderMap, RETRY_AFTER},
    StatusCode,
};
use semver::Version;
use serde::{Deserialize, Serialize};
use tracing::{info, warn};

use crate::{
    cli::PublishArgs,
    commands::{print_output, targeting, OutputSettings},
    errors::{message_with_details, CargoMonoError, ErrorKind, Result},
    git,
    types::PublishSkipReason,
    workspace::Workspace,
    CargoMonoApp,
};

const MAX_PUBLISH_ATTEMPTS: usize = 3;
const CRATES_IO_SPARSE_INDEX_BASE_URL: &str = "https://index.crates.io";
pub(super) const PUBLISH_PREFETCH_CONCURRENCY_ENV: &str = "CARGO_MONO_PUBLISH_PREFETCH_CONCURRENCY";
const DEFAULT_PREFETCH_CONCURRENCY: usize = 16;
const MAX_PREFETCH_CONCURRENCY: usize = 64;
const PREFETCH_HTTP_TIMEOUT: Duration = Duration::from_secs(15);
const PUBLISH_NO_VERIFY: bool = true;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum PublishFailureKind {
    AlreadyPublished,
    IndexNotReady,
    RateLimited,
    Other,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
enum PublishMode {
    Execute,
    DryRun,
}

impl PublishMode {
    fn as_str(self) -> &'static str {
        match self {
            Self::Execute => "execute",
            Self::DryRun => "dry-run",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum PublishTagSkipReason {
    DryRun,
    NotAllowlisted,
}

impl PublishTagSkipReason {
    fn as_str(self) -> &'static str {
        match self {
            Self::DryRun => "dry-run",
            Self::NotAllowlisted => "not-allowlisted",
        }
    }
}

#[derive(Debug, Serialize)]
struct PublishedPackage {
    name: String,
    attempts: usize,
}

#[derive(Debug, Serialize)]
struct SkippedPackage {
    name: String,
    reason: PublishSkipReason,
}

#[derive(Debug, Serialize)]
struct FailedPackage {
    name: String,
    attempts: usize,
    error: String,
}

#[derive(Debug, Serialize)]
struct PublishResult {
    workspace_root: String,
    selector: String,
    base_ref: Option<String>,
    merge_base: Option<String>,
    mode: PublishMode,
    registry: Option<String>,
    published: Vec<PublishedPackage>,
    skipped: Vec<SkippedPackage>,
    failed: Vec<FailedPackage>,
    tags: Vec<String>,
}

#[derive(Debug, Clone)]
struct PublishPrefetchCandidate {
    name: String,
    version: Version,
}

#[derive(Debug)]
struct PublishPrefetchResult {
    confirmed_already_published: BTreeSet<String>,
    lookup_errors: Vec<PrefetchLookupError>,
}

#[derive(Debug)]
struct PrefetchLookupError {
    package: String,
    http_status: Option<u16>,
    error: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum PrefetchLookupState {
    AlreadyPublished,
    NotPublished,
    Unknown,
}

#[derive(Debug)]
struct PrefetchPackageLookupResult {
    package: String,
    state: PrefetchLookupState,
    http_status: Option<u16>,
    error: Option<String>,
}

impl PrefetchPackageLookupResult {
    fn already_published(package: String) -> Self {
        Self {
            package,
            state: PrefetchLookupState::AlreadyPublished,
            http_status: None,
            error: None,
        }
    }

    fn not_published(package: String) -> Self {
        Self {
            package,
            state: PrefetchLookupState::NotPublished,
            http_status: None,
            error: None,
        }
    }

    fn unknown(package: String, http_status: Option<u16>, error: String) -> Self {
        Self {
            package,
            state: PrefetchLookupState::Unknown,
            http_status,
            error: Some(error),
        }
    }
}

#[derive(Debug, Deserialize)]
struct SparseIndexEntry {
    vers: String,
}

pub fn execute(args: &PublishArgs, output: OutputSettings, app: &CargoMonoApp) -> Result<i32> {
    let resolved = targeting::resolve_targets(&args.target, &args.changed, &app.workspace)?;
    let publish_tag_packages = app.workspace.publish_tag_packages();

    let mode = if args.dry_run {
        PublishMode::DryRun
    } else {
        PublishMode::Execute
    };

    let mut skipped = Vec::<SkippedPackage>::new();
    let publishable_targets = resolved
        .names
        .iter()
        .filter_map(|name| {
            let package = app.workspace.package(name)?;
            if package.publishable {
                Some(name.clone())
            } else {
                skipped.push(SkippedPackage {
                    name: name.clone(),
                    reason: PublishSkipReason::NonPublishable,
                });
                None
            }
        })
        .collect::<BTreeSet<_>>();

    if publishable_targets.is_empty() {
        let result = PublishResult {
            workspace_root: app.workspace.root.display().to_string(),
            selector: resolved.selector.as_str().to_string(),
            base_ref: resolved.base_ref,
            merge_base: resolved.merge_base,
            mode,
            registry: args.registry.clone(),
            published: Vec::new(),
            skipped,
            failed: Vec::new(),
            tags: Vec::new(),
        };

        print_output(
            output,
            "No publishable packages were selected for publish.",
            &result,
        )?;

        return Ok(0);
    }

    let order = app.workspace.topological_order(&publishable_targets)?;
    let prefetch_result =
        prefetch_published_versions(&app.workspace, &order, args.registry.as_deref());
    let mut published = Vec::<PublishedPackage>::new();
    let mut failed = Vec::<FailedPackage>::new();
    let mut tags = Vec::<String>::new();

    for package_name in order {
        if prefetch_result
            .confirmed_already_published
            .contains(&package_name)
        {
            skipped.push(SkippedPackage {
                name: package_name.clone(),
                reason: PublishSkipReason::AlreadyPublished,
            });
            info!(
                command_path = "cargo-mono.publish",
                workspace_root = %app.workspace.root.display(),
                package = %package_name,
                action = "publish-package",
                outcome = "already-published",
                source = "prefetch-sparse-index",
                retry_attempt = 0usize,
                "Skipping already-published crate version"
            );
            continue;
        }

        let mut attempts = 0usize;
        let mut published_or_skipped = false;

        while attempts < MAX_PUBLISH_ATTEMPTS {
            attempts += 1;
            info!(
                command_path = "cargo-mono.publish",
                workspace_root = %app.workspace.root.display(),
                package = %package_name,
                action = "publish-package",
                outcome = "attempt",
                retry_attempt = attempts,
                mode = mode.as_str(),
                no_verify = PUBLISH_NO_VERIFY,
                "Publishing package"
            );

            let publish_output =
                run_publish_command(&package_name, args.dry_run, args.registry.as_deref())?;
            if publish_output.status.success() {
                published.push(PublishedPackage {
                    name: package_name.clone(),
                    attempts,
                });
                maybe_create_publish_tag(
                    &app.workspace,
                    &package_name,
                    mode,
                    publish_tag_packages,
                    &mut tags,
                )?;
                published_or_skipped = true;
                break;
            }

            let failure_kind = classify_publish_failure(&publish_output);
            let details = collect_publish_failure_details(&publish_output);
            let retry_after_seconds =
                parse_publish_retry_after_seconds(&publish_output.stdout, &publish_output.stderr);

            match failure_kind {
                PublishFailureKind::AlreadyPublished => {
                    skipped.push(SkippedPackage {
                        name: package_name.clone(),
                        reason: PublishSkipReason::AlreadyPublished,
                    });
                    published_or_skipped = true;
                    info!(
                        command_path = "cargo-mono.publish",
                        workspace_root = %app.workspace.root.display(),
                        package = %package_name,
                        action = "publish-package",
                        outcome = "already-published",
                        retry_attempt = attempts,
                        source = "cargo-publish-output",
                        "Skipping already-published crate version"
                    );
                    break;
                }
                PublishFailureKind::IndexNotReady if attempts < MAX_PUBLISH_ATTEMPTS => {
                    let delay = retry_delay(attempts);
                    info!(
                        command_path = "cargo-mono.publish",
                        workspace_root = %app.workspace.root.display(),
                        package = %package_name,
                        action = "publish-package",
                        outcome = "retry-index-propagation",
                        retry_attempt = attempts,
                        delay_seconds = delay.as_secs(),
                        "Retrying publish due to index propagation lag"
                    );
                    thread::sleep(delay);
                }
                PublishFailureKind::RateLimited if attempts < MAX_PUBLISH_ATTEMPTS => {
                    let delay = resolve_retry_delay(attempts, retry_after_seconds);
                    info!(
                        command_path = "cargo-mono.publish",
                        workspace_root = %app.workspace.root.display(),
                        package = %package_name,
                        action = "publish-package",
                        outcome = "retry-rate-limited",
                        retry_attempt = attempts,
                        delay_seconds = delay.as_secs(),
                        retry_after_seconds = retry_after_seconds.unwrap_or_default(),
                        retry_after_present = retry_after_seconds.is_some(),
                        "Retrying publish due to rate limiting"
                    );
                    thread::sleep(delay);
                }
                _ => {
                    failed.push(FailedPackage {
                        name: package_name.clone(),
                        attempts,
                        error: format_publish_failure(
                            &package_name,
                            attempts,
                            &publish_output.status.to_string(),
                            &details,
                            args.dry_run,
                            args.registry.as_deref(),
                        ),
                    });
                    published_or_skipped = true;
                    break;
                }
            }
        }

        if !published_or_skipped {
            failed.push(FailedPackage {
                name: package_name.clone(),
                attempts,
                error: format_publish_retry_limit_failure(
                    &package_name,
                    attempts,
                    args.dry_run,
                    args.registry.as_deref(),
                ),
            });
        }
    }

    let result = PublishResult {
        workspace_root: app.workspace.root.display().to_string(),
        selector: resolved.selector.as_str().to_string(),
        base_ref: resolved.base_ref,
        merge_base: resolved.merge_base,
        mode,
        registry: args.registry.clone(),
        published,
        skipped,
        failed,
        tags,
    };

    info!(
        command_path = "cargo-mono.publish",
        workspace_root = %result.workspace_root,
        action = "publish-run",
        outcome = if result.failed.is_empty() { "success" } else { "partial-failure" },
        published_count = result.published.len(),
        skipped_count = result.skipped.len(),
        failed_count = result.failed.len(),
        tag_count = result.tags.len(),
        "Completed publish run"
    );

    let mut human_lines = vec![format!(
        "Publish summary: published={}, skipped={}, failed={}",
        result.published.len(),
        result.skipped.len(),
        result.failed.len()
    )];

    for item in &result.published {
        human_lines.push(format!(
            "- published {} (attempts={})",
            item.name, item.attempts
        ));
    }

    for tag in &result.tags {
        human_lines.push(format!("- tagged {tag}"));
    }

    for item in &result.skipped {
        human_lines.push(format!(
            "- skipped {} ({})",
            item.name,
            item.reason.as_str()
        ));
    }

    for item in &result.failed {
        human_lines.push(format!(
            "- failed {} (attempts={}):",
            item.name, item.attempts
        ));
        human_lines.push(indent_multiline(&item.error, "  "));
    }

    print_output(output, &human_lines.join("\n"), &result)?;

    if result.failed.is_empty() {
        Ok(0)
    } else {
        Ok(1)
    }
}

fn maybe_create_publish_tag(
    workspace: &Workspace,
    package_name: &str,
    mode: PublishMode,
    publish_tag_packages: &BTreeSet<String>,
    tags: &mut Vec<String>,
) -> Result<()> {
    let skip_reason = if mode == PublishMode::DryRun {
        Some(PublishTagSkipReason::DryRun)
    } else if !publish_tag_packages.contains(package_name) {
        Some(PublishTagSkipReason::NotAllowlisted)
    } else {
        None
    };

    if let Some(skip_reason) = skip_reason {
        info!(
            command_path = "cargo-mono.publish",
            workspace_root = %workspace.root.display(),
            package = %package_name,
            action = "create-publish-tag",
            outcome = "skipped",
            reason = skip_reason.as_str(),
            allowlist_count = publish_tag_packages.len(),
            "Skipping publish tag creation"
        );
        return Ok(());
    }

    let package = workspace.package(package_name).ok_or_else(|| {
        CargoMonoError::with_details(
            ErrorKind::Internal,
            "Publish succeeded but package metadata was missing for tag creation.",
            vec![("package", package_name.to_string())],
            "Reload workspace metadata and retry publish.",
        )
    })?;

    let tag = publish_tag_name(package_name, &package.version);
    info!(
        command_path = "cargo-mono.publish",
        workspace_root = %workspace.root.display(),
        package = %package_name,
        action = "create-publish-tag",
        outcome = "started",
        tag = %tag,
        "Creating publish tag"
    );

    git::create_tag(&tag).map_err(|error| {
        CargoMonoError::with_details(
            ErrorKind::Git,
            "Failed to create publish tag.",
            vec![
                ("package", package_name.to_string()),
                ("tag", tag.clone()),
                ("cause", error.message),
            ],
            "Ensure the tag does not already exist and retry publish.",
        )
    })?;

    info!(
        command_path = "cargo-mono.publish",
        workspace_root = %workspace.root.display(),
        package = %package_name,
        action = "create-publish-tag",
        outcome = "tagged",
        tag = %tag,
        "Created publish tag"
    );
    tags.push(tag);
    Ok(())
}

fn publish_tag_name(package: &str, version: &Version) -> String {
    format!("{package}@v{version}")
}

fn prefetch_published_versions(
    workspace: &Workspace,
    ordered_packages: &[String],
    registry: Option<&str>,
) -> PublishPrefetchResult {
    if !should_prefetch_published_versions(registry) {
        info!(
            command_path = "cargo-mono.publish",
            workspace_root = %workspace.root.display(),
            action = "prefetch-published-versions",
            outcome = "skipped",
            reason = "unsupported-registry",
            registry = %registry.unwrap_or(""),
            "Skipping published version prefetch for unsupported registry"
        );
        return PublishPrefetchResult {
            confirmed_already_published: BTreeSet::new(),
            lookup_errors: Vec::new(),
        };
    }

    let mut candidates = Vec::with_capacity(ordered_packages.len());
    for package_name in ordered_packages {
        let Some(package) = workspace.package(package_name) else {
            warn!(
                command_path = "cargo-mono.publish",
                workspace_root = %workspace.root.display(),
                action = "prefetch-published-versions",
                outcome = "partial-error",
                package = %package_name,
                reason = "missing-workspace-metadata",
                "Package is missing from workspace metadata during prefetch"
            );
            return PublishPrefetchResult {
                confirmed_already_published: BTreeSet::new(),
                lookup_errors: vec![PrefetchLookupError {
                    package: package_name.clone(),
                    http_status: None,
                    error: "package missing from workspace metadata".to_string(),
                }],
            };
        };

        candidates.push(PublishPrefetchCandidate {
            name: package_name.clone(),
            version: package.version.clone(),
        });
    }

    if candidates.is_empty() {
        info!(
            command_path = "cargo-mono.publish",
            workspace_root = %workspace.root.display(),
            action = "prefetch-published-versions",
            outcome = "skipped",
            reason = "no-candidates",
            "Skipping published version prefetch because there are no candidates"
        );
        return PublishPrefetchResult {
            confirmed_already_published: BTreeSet::new(),
            lookup_errors: Vec::new(),
        };
    }

    let concurrency = resolve_prefetch_concurrency();
    info!(
        command_path = "cargo-mono.publish",
        workspace_root = %workspace.root.display(),
        action = "prefetch-published-versions",
        outcome = "started",
        package_count = candidates.len(),
        concurrency,
        "Prefetching published crate versions from crates.io sparse index"
    );

    let lookup_results = run_parallel_sparse_index_lookup(&candidates, concurrency);
    let prefetch_result = merge_prefetch_lookup_results(lookup_results);

    for lookup_error in &prefetch_result.lookup_errors {
        warn!(
            command_path = "cargo-mono.publish",
            workspace_root = %workspace.root.display(),
            package = %lookup_error.package,
            action = "prefetch-published-versions",
            outcome = "lookup-error",
            http_status = lookup_error.http_status,
            error = %lookup_error.error,
            "Failed to prefetch published version from sparse index"
        );
    }

    info!(
        command_path = "cargo-mono.publish",
        workspace_root = %workspace.root.display(),
        action = "prefetch-published-versions",
        outcome = if prefetch_result.lookup_errors.is_empty() {
            "completed"
        } else {
            "partial-error"
        },
        package_count = candidates.len(),
        already_published_count = prefetch_result.confirmed_already_published.len(),
        lookup_error_count = prefetch_result.lookup_errors.len(),
        "Completed published version prefetch"
    );

    prefetch_result
}

fn should_prefetch_published_versions(registry: Option<&str>) -> bool {
    registry.is_none_or(|value| value.eq_ignore_ascii_case("crates-io"))
}

fn resolve_prefetch_concurrency() -> usize {
    let Ok(raw_value) = std::env::var(PUBLISH_PREFETCH_CONCURRENCY_ENV) else {
        return DEFAULT_PREFETCH_CONCURRENCY;
    };

    match parse_prefetch_concurrency_value(&raw_value) {
        Some(concurrency) => concurrency,
        None => {
            warn!(
                command_path = "cargo-mono.publish",
                action = "prefetch-published-versions",
                outcome = "invalid-prefetch-concurrency",
                env_var = PUBLISH_PREFETCH_CONCURRENCY_ENV,
                env_value = %raw_value,
                default_concurrency = DEFAULT_PREFETCH_CONCURRENCY,
                max_concurrency = MAX_PREFETCH_CONCURRENCY,
                "Invalid prefetch concurrency override; using default"
            );
            DEFAULT_PREFETCH_CONCURRENCY
        }
    }
}

fn parse_prefetch_concurrency_value(raw: &str) -> Option<usize> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return None;
    }

    let parsed = trimmed.parse::<usize>().ok()?;
    if parsed == 0 {
        return None;
    }

    Some(parsed.min(MAX_PREFETCH_CONCURRENCY))
}

fn run_parallel_sparse_index_lookup(
    candidates: &[PublishPrefetchCandidate],
    concurrency: usize,
) -> Vec<PrefetchPackageLookupResult> {
    if candidates.is_empty() {
        return Vec::new();
    }

    let http_client = match reqwest::blocking::Client::builder()
        .timeout(PREFETCH_HTTP_TIMEOUT)
        .build()
    {
        Ok(client) => client,
        Err(error) => {
            return candidates
                .iter()
                .map(|candidate| {
                    PrefetchPackageLookupResult::unknown(
                        candidate.name.clone(),
                        None,
                        format!("failed to initialize HTTP client: {error}"),
                    )
                })
                .collect();
        }
    };

    let worker_count = concurrency
        .clamp(1, MAX_PREFETCH_CONCURRENCY)
        .min(candidates.len());
    let queue = Arc::new(Mutex::new(VecDeque::from(candidates.to_vec())));

    let joined_worker_results = thread::scope(|scope| {
        let mut handles = Vec::with_capacity(worker_count);
        for _ in 0..worker_count {
            let worker_queue = Arc::clone(&queue);
            let worker_client = http_client.clone();
            handles.push(scope.spawn(move || prefetch_worker_loop(worker_queue, worker_client)));
        }

        handles
            .into_iter()
            .map(|handle| handle.join())
            .collect::<Vec<_>>()
    });

    let mut lookup_results = Vec::with_capacity(candidates.len());
    for joined_result in joined_worker_results {
        match joined_result {
            Ok(worker_results) => lookup_results.extend(worker_results),
            Err(_) => lookup_results.push(PrefetchPackageLookupResult::unknown(
                "<worker>".to_string(),
                None,
                "prefetch worker thread panicked".to_string(),
            )),
        }
    }

    let seen_packages = lookup_results
        .iter()
        .map(|result| result.package.clone())
        .collect::<BTreeSet<_>>();
    for candidate in candidates {
        if !seen_packages.contains(&candidate.name) {
            lookup_results.push(PrefetchPackageLookupResult::unknown(
                candidate.name.clone(),
                None,
                "prefetch lookup did not complete".to_string(),
            ));
        }
    }

    lookup_results
}

fn prefetch_worker_loop(
    queue: Arc<Mutex<VecDeque<PublishPrefetchCandidate>>>,
    client: reqwest::blocking::Client,
) -> Vec<PrefetchPackageLookupResult> {
    let mut results = Vec::new();

    loop {
        let next_candidate = match queue.lock() {
            Ok(mut guard) => guard.pop_front(),
            Err(_) => None,
        };
        let Some(candidate) = next_candidate else {
            break;
        };

        results.push(lookup_sparse_index_version(&client, &candidate));
    }

    results
}

fn lookup_sparse_index_version(
    client: &reqwest::blocking::Client,
    candidate: &PublishPrefetchCandidate,
) -> PrefetchPackageLookupResult {
    let path = sparse_index_path_for_crate(&candidate.name);
    let request_url = format!("{CRATES_IO_SPARSE_INDEX_BASE_URL}/{path}");

    for attempt in 1..=MAX_PUBLISH_ATTEMPTS {
        let response = match client.get(&request_url).send() {
            Ok(response) => response,
            Err(error) => {
                return PrefetchPackageLookupResult::unknown(
                    candidate.name.clone(),
                    None,
                    format!("sparse index request failed: {error}"),
                )
            }
        };

        let status = response.status();
        if status == StatusCode::TOO_MANY_REQUESTS {
            let retry_after_seconds = parse_retry_after_seconds_from_headers(response.headers());
            if attempt < MAX_PUBLISH_ATTEMPTS {
                let delay = resolve_retry_delay(attempt, retry_after_seconds);
                info!(
                    command_path = "cargo-mono.publish",
                    package = %candidate.name,
                    action = "prefetch-published-versions",
                    outcome = "retry-rate-limited",
                    retry_attempt = attempt,
                    delay_seconds = delay.as_secs(),
                    retry_after_seconds = retry_after_seconds.unwrap_or_default(),
                    retry_after_present = retry_after_seconds.is_some(),
                    "Retrying sparse index lookup due to rate limiting"
                );
                thread::sleep(delay);
                continue;
            }

            return PrefetchPackageLookupResult::unknown(
                candidate.name.clone(),
                Some(status.as_u16()),
                "sparse index rate limiting persisted after retry attempts".to_string(),
            );
        }

        if status == StatusCode::NOT_FOUND {
            return PrefetchPackageLookupResult::not_published(candidate.name.clone());
        }
        if !status.is_success() {
            return PrefetchPackageLookupResult::unknown(
                candidate.name.clone(),
                Some(status.as_u16()),
                format!("sparse index returned unexpected status {status}"),
            );
        }

        let body = match response.text() {
            Ok(body) => body,
            Err(error) => {
                return PrefetchPackageLookupResult::unknown(
                    candidate.name.clone(),
                    None,
                    format!("failed to read sparse index response body: {error}"),
                )
            }
        };

        return match sparse_index_has_version(&body, &candidate.version) {
            Ok(true) => PrefetchPackageLookupResult::already_published(candidate.name.clone()),
            Ok(false) => PrefetchPackageLookupResult::not_published(candidate.name.clone()),
            Err(error) => PrefetchPackageLookupResult::unknown(
                candidate.name.clone(),
                None,
                format!("failed to parse sparse index record: {error}"),
            ),
        };
    }

    PrefetchPackageLookupResult::unknown(
        candidate.name.clone(),
        None,
        "sparse index lookup exhausted retry attempts".to_string(),
    )
}

fn parse_publish_retry_after_seconds(stdout: &[u8], stderr: &[u8]) -> Option<u64> {
    let stdout_text = String::from_utf8_lossy(stdout);
    parse_retry_after_seconds_from_text(stdout_text.as_ref()).or_else(|| {
        let stderr_text = String::from_utf8_lossy(stderr);
        parse_retry_after_seconds_from_text(stderr_text.as_ref())
    })
}

fn parse_retry_after_seconds_from_headers(headers: &HeaderMap) -> Option<u64> {
    headers
        .get(RETRY_AFTER)
        .and_then(|value| value.to_str().ok())
        .and_then(parse_retry_after_seconds_value)
}

fn parse_retry_after_seconds_from_text(raw: &str) -> Option<u64> {
    raw.lines().find_map(|line| {
        let (header_name, header_value) = line.split_once(':')?;
        if header_name.trim().eq_ignore_ascii_case("retry-after") {
            parse_retry_after_seconds_value(header_value)
        } else {
            None
        }
    })
}

fn parse_retry_after_seconds_value(raw: &str) -> Option<u64> {
    let trimmed = raw.trim();
    if trimmed.is_empty() || !trimmed.chars().all(|character| character.is_ascii_digit()) {
        return None;
    }

    trimmed.parse::<u64>().ok()
}

fn sparse_index_has_version(
    index_body: &str,
    version: &Version,
) -> std::result::Result<bool, serde_json::Error> {
    let target_version = version.to_string();
    for line in index_body.lines() {
        let line = line.trim();
        if line.is_empty() {
            continue;
        }

        let entry: SparseIndexEntry = serde_json::from_str(line)?;
        if entry.vers == target_version {
            return Ok(true);
        }
    }

    Ok(false)
}

fn sparse_index_path_for_crate(crate_name: &str) -> String {
    let normalized = crate_name.to_ascii_lowercase();
    let char_count = normalized.chars().count();

    match char_count {
        1 => format!("1/{normalized}"),
        2 => format!("2/{normalized}"),
        3 => format!(
            "3/{}/{}",
            normalized.chars().next().unwrap_or('0'),
            normalized
        ),
        _ => {
            let prefix_a = normalized.chars().take(2).collect::<String>();
            let prefix_b = normalized.chars().skip(2).take(2).collect::<String>();
            format!("{prefix_a}/{prefix_b}/{normalized}")
        }
    }
}

fn merge_prefetch_lookup_results(
    results: Vec<PrefetchPackageLookupResult>,
) -> PublishPrefetchResult {
    let mut confirmed_already_published = BTreeSet::new();
    let mut lookup_errors = Vec::new();

    for result in results {
        match result.state {
            PrefetchLookupState::AlreadyPublished => {
                confirmed_already_published.insert(result.package);
            }
            PrefetchLookupState::NotPublished => {}
            PrefetchLookupState::Unknown => lookup_errors.push(PrefetchLookupError {
                package: result.package,
                http_status: result.http_status,
                error: result
                    .error
                    .unwrap_or_else(|| "unknown sparse index lookup error".to_string()),
            }),
        }
    }

    PublishPrefetchResult {
        confirmed_already_published,
        lookup_errors,
    }
}

fn run_publish_command(package: &str, dry_run: bool, registry: Option<&str>) -> Result<Output> {
    let mut command = Command::new("cargo");
    command
        .arg("publish")
        .arg("-p")
        .arg(package)
        .arg("--no-verify");

    if dry_run {
        command.arg("--dry-run");
    }

    if let Some(registry) = registry {
        command.arg("--registry").arg(registry);
    }

    command.output().map_err(|error| {
        CargoMonoError::with_details(
            ErrorKind::Cargo,
            "Failed to start `cargo publish` command.",
            vec![
                ("package", package.to_string()),
                ("dry_run", dry_run.to_string()),
                ("no_verify", PUBLISH_NO_VERIFY.to_string()),
                ("registry", registry.unwrap_or("default").to_string()),
                ("error", error.to_string()),
            ],
            "Ensure Cargo is installed, the package exists, and registry credentials are \
             configured before retrying.",
        )
    })
}

fn format_publish_failure(
    package: &str,
    attempts: usize,
    status: &str,
    raw_details: &str,
    dry_run: bool,
    registry: Option<&str>,
) -> String {
    let details = extract_relevant_error_excerpt(raw_details);
    let mut context = vec![
        ("package", package.to_string()),
        ("attempt", attempts.to_string()),
        ("status", status.to_string()),
        ("dry_run", dry_run.to_string()),
        ("no_verify", PUBLISH_NO_VERIFY.to_string()),
        ("registry", registry.unwrap_or("default").to_string()),
    ];
    if !details.is_empty() {
        context.push(("details_excerpt", details));
    }

    message_with_details(
        "`cargo publish` failed for package.",
        &context,
        "Verify package metadata, registry access, and network connectivity, then retry.",
    )
}

fn collect_publish_failure_details(output: &Output) -> String {
    let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
    let stdout = String::from_utf8_lossy(&output.stdout).trim().to_string();

    match (stderr.is_empty(), stdout.is_empty()) {
        (true, true) => String::new(),
        (false, true) => stderr,
        (true, false) => stdout,
        (false, false) => format!("{stderr}\n{stdout}"),
    }
}

fn format_publish_retry_limit_failure(
    package: &str,
    attempts: usize,
    dry_run: bool,
    registry: Option<&str>,
) -> String {
    message_with_details(
        "`cargo publish` did not complete within retry attempts.",
        &[
            ("package", package.to_string()),
            ("attempts", attempts.to_string()),
            ("max_attempts", MAX_PUBLISH_ATTEMPTS.to_string()),
            ("dry_run", dry_run.to_string()),
            ("no_verify", PUBLISH_NO_VERIFY.to_string()),
            ("registry", registry.unwrap_or("default").to_string()),
        ],
        "Wait for index propagation or rate limits to clear, then rerun publish.",
    )
}

fn compact_error_details(raw: &str) -> String {
    raw.split_whitespace().collect::<Vec<_>>().join(" ")
}

fn extract_relevant_error_excerpt(raw: &str) -> String {
    let lines = raw
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty())
        .collect::<Vec<_>>();
    let Some(selected_line) = preferred_failure_detail_line(&lines) else {
        return String::new();
    };

    compact_error_details(&selected_line)
}

fn preferred_failure_detail_line(lines: &[&str]) -> Option<String> {
    for (index, line) in lines.iter().enumerate() {
        if line.to_ascii_lowercase().starts_with("error:") {
            return Some(merge_continuation_line(lines, index));
        }
    }

    for line in lines {
        if line.to_ascii_lowercase().contains("failed to") {
            return Some((*line).to_string());
        }
    }

    lines.first().map(|line| (*line).to_string())
}

fn merge_continuation_line(lines: &[&str], index: usize) -> String {
    let primary = lines[index];
    let Some(next_line) = lines.get(index + 1) else {
        return primary.to_string();
    };

    if !primary.ends_with(':') {
        return primary.to_string();
    }

    let next_lower = next_line.to_ascii_lowercase();
    if next_lower.starts_with("warning:")
        || next_lower.starts_with("note:")
        || next_lower.starts_with("help:")
    {
        return primary.to_string();
    }

    format!("{primary} {next_line}")
}

fn indent_multiline(raw: &str, prefix: &str) -> String {
    raw.lines()
        .map(|line| format!("{prefix}{line}"))
        .collect::<Vec<_>>()
        .join("\n")
}

fn retry_delay(attempt: usize) -> Duration {
    match attempt {
        1 => Duration::from_secs(2),
        2 => Duration::from_secs(4),
        _ => Duration::from_secs(8),
    }
}

fn resolve_retry_delay(attempt: usize, retry_after_seconds: Option<u64>) -> Duration {
    retry_after_seconds
        .map(Duration::from_secs)
        .unwrap_or_else(|| retry_delay(attempt))
}

fn classify_publish_failure(output: &Output) -> PublishFailureKind {
    let combined = format!(
        "{}\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    )
    .to_lowercase();

    if combined.contains("already uploaded")
        || combined.contains("already exists")
        || combined.contains("already on crates.io")
    {
        return PublishFailureKind::AlreadyPublished;
    }

    if combined.contains("no matching package named")
        || combined.contains("failed to select a version for the requirement")
        || combined.contains("candidate versions found which didn't match")
    {
        return PublishFailureKind::IndexNotReady;
    }

    if combined.contains("too many requests")
        || combined.contains("status code: 429")
        || combined.contains("status code 429")
        || combined.contains("http 429")
        || combined.contains("429 too many requests")
    {
        return PublishFailureKind::RateLimited;
    }

    PublishFailureKind::Other
}

#[cfg(test)]
mod tests {
    use std::{collections::BTreeSet, process::Command};

    use super::*;

    fn output_with_stderr(stderr: &str) -> Output {
        let mut output = Command::new("cargo")
            .arg("--definitely-invalid-cargo-flag-for-tests")
            .output()
            .expect("cargo must be executable in tests");
        output.stdout = Vec::new();
        output.stderr = stderr.as_bytes().to_vec();
        output
    }

    #[test]
    fn classify_already_published_failure() {
        let output = output_with_stderr("crate `foo v0.1.0` is already uploaded");

        assert!(matches!(
            classify_publish_failure(&output),
            PublishFailureKind::AlreadyPublished
        ));
    }

    #[test]
    fn classify_index_not_ready_failure() {
        let output =
            output_with_stderr("failed to select a version for the requirement `foo = \"^0.1.0\"`");

        assert!(matches!(
            classify_publish_failure(&output),
            PublishFailureKind::IndexNotReady
        ));
    }

    #[test]
    fn classify_other_failure() {
        let output = output_with_stderr("network timeout");

        assert!(matches!(
            classify_publish_failure(&output),
            PublishFailureKind::Other
        ));
    }

    #[test]
    fn classify_rate_limited_failure() {
        let output = output_with_stderr("error: registry responded with 429 Too Many Requests");

        assert!(matches!(
            classify_publish_failure(&output),
            PublishFailureKind::RateLimited
        ));
    }

    #[test]
    fn format_publish_failure_uses_status_when_no_details_exist() {
        let message =
            format_publish_failure("alpha", 1, "exit status: 101", "", false, Some("crates-io"));
        assert!(message.contains("Summary: `cargo publish` failed for package."));
        assert!(message.contains("package=alpha"));
        assert!(message.contains("attempt=1"));
        assert!(message.contains("status=exit status: 101"));
        assert!(message.contains("no_verify=true"));
        assert!(message.contains("Hint: "));
    }

    #[test]
    fn format_publish_failure_compacts_multiline_details() {
        let message = format_publish_failure(
            "alpha",
            2,
            "exit status: 101",
            "error:\nnetwork timeout\n",
            true,
            None,
        );
        assert!(message.contains("details_excerpt=error: network timeout"));
        assert!(message.contains("dry_run=true"));
        assert!(message.contains("no_verify=true"));
        assert!(message.contains("registry=default"));
        assert!(message.contains("Hint: "));
    }

    #[test]
    fn extract_relevant_error_excerpt_prefers_error_line() {
        let excerpt = extract_relevant_error_excerpt(
            "warning: profiles for the non root package will be ignored\nerror: failed to publish",
        );
        assert_eq!(excerpt, "error: failed to publish");
    }

    #[test]
    fn extract_relevant_error_excerpt_uses_failed_to_fallback() {
        let excerpt = extract_relevant_error_excerpt(
            "warning: first line\nfailed to select a version for the requirement",
        );
        assert_eq!(excerpt, "failed to select a version for the requirement");
    }

    #[test]
    fn extract_relevant_error_excerpt_uses_first_non_empty_line_as_last_resort() {
        let excerpt = extract_relevant_error_excerpt("   \nwarning: only warning\n");
        assert_eq!(excerpt, "warning: only warning");
    }

    #[test]
    fn format_publish_retry_limit_failure_includes_hint() {
        let message = format_publish_retry_limit_failure("alpha", 3, false, Some("internal"));
        assert!(
            message.contains("Summary: `cargo publish` did not complete within retry attempts.")
        );
        assert!(message.contains("attempts=3"));
        assert!(message.contains("max_attempts=3"));
        assert!(message.contains("no_verify=true"));
        assert!(message.contains("registry=internal"));
        assert!(message.contains("Hint: "));
    }

    #[test]
    fn sparse_index_path_matches_registry_rules() {
        assert_eq!(sparse_index_path_for_crate("a"), "1/a");
        assert_eq!(sparse_index_path_for_crate("ab"), "2/ab");
        assert_eq!(sparse_index_path_for_crate("abc"), "3/a/abc");
        assert_eq!(sparse_index_path_for_crate("serde"), "se/rd/serde");
        assert_eq!(sparse_index_path_for_crate("Serde"), "se/rd/serde");
    }

    #[test]
    fn parse_prefetch_concurrency_value_accepts_and_clamps() {
        assert_eq!(parse_prefetch_concurrency_value("1"), Some(1));
        assert_eq!(parse_prefetch_concurrency_value("16"), Some(16));
        assert_eq!(
            parse_prefetch_concurrency_value("1024"),
            Some(MAX_PREFETCH_CONCURRENCY)
        );
    }

    #[test]
    fn parse_prefetch_concurrency_value_rejects_invalid_values() {
        assert_eq!(parse_prefetch_concurrency_value(""), None);
        assert_eq!(parse_prefetch_concurrency_value("   "), None);
        assert_eq!(parse_prefetch_concurrency_value("0"), None);
        assert_eq!(parse_prefetch_concurrency_value("-1"), None);
        assert_eq!(parse_prefetch_concurrency_value("invalid"), None);
    }

    #[test]
    fn parse_retry_after_seconds_from_text_accepts_delta_seconds() {
        let raw = "warning: temporary error\nRetry-After: 30\n";
        assert_eq!(parse_retry_after_seconds_from_text(raw), Some(30));
    }

    #[test]
    fn parse_retry_after_seconds_from_text_rejects_non_numeric_values() {
        let raw = "Retry-After: Wed, 21 Oct 2015 07:28:00 GMT";
        assert_eq!(parse_retry_after_seconds_from_text(raw), None);
    }

    #[test]
    fn parse_retry_after_seconds_from_headers_accepts_delta_seconds() {
        let mut headers = HeaderMap::new();
        headers.insert(RETRY_AFTER, "45".parse().expect("valid retry-after header"));
        assert_eq!(parse_retry_after_seconds_from_headers(&headers), Some(45));
    }

    #[test]
    fn parse_retry_after_seconds_from_headers_rejects_http_date_values() {
        let mut headers = HeaderMap::new();
        headers.insert(
            RETRY_AFTER,
            "Wed, 21 Oct 2015 07:28:00 GMT"
                .parse()
                .expect("valid retry-after header"),
        );
        assert_eq!(parse_retry_after_seconds_from_headers(&headers), None);
    }

    #[test]
    fn parse_publish_retry_after_seconds_prefers_stdout_then_stderr() {
        assert_eq!(
            parse_publish_retry_after_seconds(b"Retry-After: 7\n", b"Retry-After: 9\n"),
            Some(7)
        );
        assert_eq!(
            parse_publish_retry_after_seconds(b"no retry header", b"Retry-After: 11\n"),
            Some(11)
        );
    }

    #[test]
    fn resolve_retry_delay_prefers_retry_after_seconds() {
        assert_eq!(resolve_retry_delay(1, Some(30)), Duration::from_secs(30));
        assert_eq!(resolve_retry_delay(2, None), Duration::from_secs(4));
    }

    #[test]
    fn should_prefetch_only_for_default_or_crates_io_registry() {
        assert!(should_prefetch_published_versions(None));
        assert!(should_prefetch_published_versions(Some("crates-io")));
        assert!(should_prefetch_published_versions(Some("CRATES-IO")));
        assert!(!should_prefetch_published_versions(Some("internal")));
    }

    #[test]
    fn sparse_index_has_version_finds_requested_version() {
        let body = r#"
{"name":"alpha","vers":"0.1.0"}
{"name":"alpha","vers":"0.2.0"}
"#;
        let version = Version::new(0, 2, 0);
        assert!(sparse_index_has_version(body, &version).unwrap());

        let missing_version = Version::new(0, 3, 0);
        assert!(!sparse_index_has_version(body, &missing_version).unwrap());
    }

    #[test]
    fn sparse_index_has_version_reports_invalid_json_line() {
        let body = r#"
{"name":"alpha","vers":"0.1.0"}
{invalid}
"#;
        let version = Version::new(0, 2, 0);
        assert!(sparse_index_has_version(body, &version).is_err());
    }

    #[test]
    fn merge_prefetch_lookup_results_tracks_already_published_and_errors() {
        let result = merge_prefetch_lookup_results(vec![
            PrefetchPackageLookupResult::already_published("alpha".to_string()),
            PrefetchPackageLookupResult::not_published("beta".to_string()),
            PrefetchPackageLookupResult::unknown(
                "gamma".to_string(),
                Some(503),
                "service unavailable".to_string(),
            ),
        ]);

        assert_eq!(
            result.confirmed_already_published,
            BTreeSet::from(["alpha".to_string()])
        );
        assert_eq!(result.lookup_errors.len(), 1);
        assert_eq!(result.lookup_errors[0].package, "gamma");
        assert_eq!(result.lookup_errors[0].http_status, Some(503));
    }

    #[test]
    fn publish_tag_name_uses_at_v_format() {
        let version = Version::new(1, 2, 3);
        assert_eq!(publish_tag_name("nodeup", &version), "nodeup@v1.2.3");
    }
}
