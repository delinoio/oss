use std::{
    process::{Command, Output},
    thread,
    time::Duration,
};

use serde::Serialize;
use tracing::info;

use crate::{
    cli::PublishArgs,
    commands::{print_output, targeting},
    errors::{CargoMonoError, Result},
    git,
    types::{OutputFormat, PublishSkipReason},
    CargoMonoApp,
};

const MAX_PUBLISH_ATTEMPTS: usize = 3;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum PublishFailureKind {
    AlreadyPublished,
    IndexNotReady,
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
}

pub fn execute(args: &PublishArgs, output: OutputFormat, app: &CargoMonoApp) -> Result<i32> {
    git::ensure_clean_working_tree(args.allow_dirty)?;

    let resolved = targeting::resolve_targets(&args.target, &args.changed, &app.workspace)?;

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
        .collect::<std::collections::BTreeSet<_>>();

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
        };

        print_output(
            output,
            "No publishable packages were selected for publish.",
            &result,
        )?;

        return Ok(0);
    }

    let order = app.workspace.topological_order(&publishable_targets)?;
    let mut published = Vec::<PublishedPackage>::new();
    let mut failed = Vec::<FailedPackage>::new();

    for package_name in order {
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
                "Publishing package"
            );

            let publish_output =
                run_publish_command(&package_name, args.dry_run, args.registry.as_deref())?;
            if publish_output.status.success() {
                published.push(PublishedPackage {
                    name: package_name.clone(),
                    attempts,
                });
                published_or_skipped = true;
                break;
            }

            let failure_kind = classify_publish_failure(&publish_output);
            let stderr = String::from_utf8_lossy(&publish_output.stderr)
                .trim()
                .to_string();
            let stdout = String::from_utf8_lossy(&publish_output.stdout)
                .trim()
                .to_string();
            let details = if stderr.is_empty() { stdout } else { stderr };

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
                _ => {
                    failed.push(FailedPackage {
                        name: package_name.clone(),
                        attempts,
                        error: if details.is_empty() {
                            format!("publish failed with status {}", publish_output.status)
                        } else {
                            details
                        },
                    });
                    published_or_skipped = true;
                    break;
                }
            }
        }

        if !published_or_skipped {
            failed.push(FailedPackage {
                name: package_name,
                attempts,
                error: "publish did not complete within retry limit".to_string(),
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
    };

    info!(
        command_path = "cargo-mono.publish",
        workspace_root = %result.workspace_root,
        action = "publish-run",
        outcome = if result.failed.is_empty() { "success" } else { "partial-failure" },
        published_count = result.published.len(),
        skipped_count = result.skipped.len(),
        failed_count = result.failed.len(),
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

    for item in &result.skipped {
        human_lines.push(format!(
            "- skipped {} ({})",
            item.name,
            item.reason.as_str()
        ));
    }

    for item in &result.failed {
        human_lines.push(format!(
            "- failed {} (attempts={}): {}",
            item.name, item.attempts, item.error
        ));
    }

    print_output(output, &human_lines.join("\n"), &result)?;

    if result.failed.is_empty() {
        Ok(0)
    } else {
        Ok(1)
    }
}

fn run_publish_command(package: &str, dry_run: bool, registry: Option<&str>) -> Result<Output> {
    let mut command = Command::new("cargo");
    command.arg("publish").arg("-p").arg(package);

    if dry_run {
        command.arg("--dry-run");
    }

    if let Some(registry) = registry {
        command.arg("--registry").arg(registry);
    }

    command
        .output()
        .map_err(|error| CargoMonoError::cargo(format!("Failed to execute cargo publish: {error}")))
}

fn retry_delay(attempt: usize) -> Duration {
    match attempt {
        1 => Duration::from_secs(2),
        2 => Duration::from_secs(4),
        _ => Duration::from_secs(8),
    }
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

    PublishFailureKind::Other
}

#[cfg(test)]
mod tests {
    use std::process::Command;

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
}
