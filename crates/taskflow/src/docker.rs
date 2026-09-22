use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
};

use anyhow::{ensure, Context, Result};
use tokio_util::sync::CancellationToken;

use crate::{
    config::{Command, Task},
    process,
};

pub struct Container {
    name: String,
    cleaned: bool,
    directory: PathBuf,
    environment: BTreeMap<String, String>,
    helper: Option<tempfile::TempDir>,
}

#[derive(Debug)]
pub(crate) struct CleanupFailure;
impl std::fmt::Display for CleanupFailure {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("Docker container cleanup could not be confirmed")
    }
}
impl std::error::Error for CleanupFailure {}
pub fn host_environment(
    environment: &BTreeMap<String, String>,
) -> Result<BTreeMap<String, String>> {
    let inherited = crate::environment::inherited()?;
    let lookup = [
        "PATH",
        "HOME",
        "USERPROFILE",
        "SystemRoot",
        "WINDIR",
        "COMSPEC",
        "PATHEXT",
        "TEMP",
        "TMP",
        "TMPDIR",
    ];
    // Container PATH, loader hooks, proxies, and language startup variables must
    // never configure the host client. Lookup/runtime values come only from the
    // host; the three Docker transport selectors retain resolved precedence.
    let mut values: BTreeMap<_, _> = inherited
        .iter()
        .filter(|(key, _)| {
            lookup
                .iter()
                .any(|name| crate::environment::same_name(name, key))
        })
        .map(|(key, value)| (key.clone(), value.clone()))
        .collect();
    for key in ["DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"] {
        let source = if crate::environment::get(environment, key).is_some() {
            environment
        } else {
            &inherited
        };
        if let Some((key, value)) = source
            .iter()
            .find(|(name, _)| crate::environment::same_name(key, name))
        {
            crate::environment::insert(&mut values, key.clone(), value.clone());
        }
    }
    Ok(values)
}
impl Container {
    pub(crate) fn host_environment(&self) -> &BTreeMap<String, String> {
        &self.environment
    }

    pub async fn cleanup(&mut self) -> Result<()> {
        // Task deadlines stop new work, never the cleanup that proves container
        // absence. Each cleanup subprocess retains its own metadata deadline.
        process::DEADLINE
            .scope(None, self.cleanup_inner())
            .await
            .context(CleanupFailure)
    }

    async fn cleanup_inner(&mut self) -> Result<()> {
        // Cleanup must remain available after the task/session token is cancelled.
        let cleanup_token = CancellationToken::new();
        let result = process::capture_with_env(
            &self.directory,
            &Command::Argv(vec![
                "docker".into(),
                "rm".into(),
                "-f".into(),
                self.name.clone(),
            ]),
            &self.environment,
            &cleanup_token,
        )
        .await;
        self.cleaned = result.is_ok();
        // --rm may already have removed an exited container; a failed removal is
        // acceptable only after a successful daemon query proves it is absent.
        if !self.cleaned {
            let remaining = process::capture_with_env(
                &self.directory,
                &Command::Argv(vec![
                    "docker".into(),
                    "ps".into(),
                    "-a".into(),
                    "--filter".into(),
                    format!("name=^{}$", self.name),
                    "--format".into(),
                    "{{.ID}}".into(),
                ]),
                &self.environment,
                &cleanup_token,
            )
            .await
            .context("could not verify Docker container cleanup")?;
            ensure!(
                remaining.iter().all(u8::is_ascii_whitespace),
                "owned Docker container cleanup failed"
            );
            self.cleaned = true;
        }
        if let Some(helper) = self.helper.take() {
            helper
                .close()
                .context("could not remove Docker result helper")?;
        }
        Ok(())
    }
}
impl Drop for Container {
    fn drop(&mut self) {
        if !self.cleaned {
            // Last-resort cleanup if a future is dropped. Normal paths use the
            // awaited owner cleanup above; never leave a detached container.
            let mut command = std::process::Command::new("docker");
            command
                .args(["rm", "-f", &self.name])
                .current_dir(&self.directory)
                .env_clear()
                .envs(&self.environment)
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null());
            // Destructors cannot await the async owner, but they must not hang
            // indefinitely if the local daemon has stopped responding.
            if let Ok(mut child) = command.spawn() {
                let deadline = std::time::Instant::now() + std::time::Duration::from_secs(5);
                loop {
                    match child.try_wait() {
                        Ok(Some(_)) => break,
                        Ok(None) if std::time::Instant::now() < deadline => {
                            std::thread::sleep(std::time::Duration::from_millis(20))
                        }
                        _ => {
                            let _ = child.kill();
                            let _ = child.wait();
                            break;
                        }
                    }
                }
            }
        }
    }
}
pub async fn prepare(
    root: &Path,
    directory: &Path,
    task: &Task,
    environment: &BTreeMap<String, String>,
    overrides: &BTreeMap<String, String>,
    execution: &str,
    cancel: &CancellationToken,
) -> Result<(Command, Container)> {
    task.platform.validate_ports()?;
    let mount = workspace_mount(root)?;
    let host = host_environment(environment)?;
    // Ask the same CLI with the same environment that will launch the task.
    // Its selected context can override DOCKER_HOST, and the synthetic default
    // context incorporates DOCKER_HOST when no named context takes precedence.
    let bytes = process::capture_with_env(
        root,
        &Command::Argv(vec![
            "docker".into(),
            "context".into(),
            "inspect".into(),
            "--format".into(),
            "{{json .Endpoints.docker.Host}}".into(),
        ]),
        &host,
        cancel,
    )
    .await?;
    let endpoint = serde_json::from_slice::<String>(&bytes)?;
    ensure!(
        local_endpoint(&endpoint),
        "Docker execution requires a local daemon socket"
    );
    // Host binaries may target another OS/architecture. Provide the bounded
    // result-reporting interface using the container's POSIX shell instead.
    uuid::Uuid::parse_str(execution)?;
    let report = serde_json::to_string(&crate::runner::TaskReport {
        version: 1,
        execution: execution.into(),
        result: crate::runner::TaskReported::Unchanged,
    })?;
    // Every probe and shard unit owns a separate container, but must not leave
    // a permanent execution directory behind. Keep its helper in a scoped
    // directory; only the outer task owns retained result/log state.
    let helpers = root.join(".taskflow/docker-helpers");
    std::fs::create_dir_all(&helpers)?;
    let helper_owner = tempfile::Builder::new()
        .prefix("container-")
        .tempdir_in(&helpers)?;
    let helper = helper_owner.path().join("tflow-result");
    let container_helper = format!(
        "/workspace/{}",
        crate::files::slash(helper.strip_prefix(root)?)?
    );
    let result_path = format!("/workspace/.taskflow/runs/{execution}/result.json");
    let script = format!(
        "#!/bin/sh\nset -eu\n[ \"$#\" = 2 ] && [ \"${{1-}}\" = result ] && [ \"${{2-}}\" = \
         unchanged ] || exit 2\n[ \"${{TFLOW_EXECUTION_ID-}}\" = '{execution}' ] || exit 2\n[ \
         \"${{TFLOW_RESULT_FILE-}}\" = '{result_path}' ] || exit \
         2\nstaged=\"$TFLOW_RESULT_FILE.$$\"\ntrap 'rm -f \"$staged\"' EXIT HUP INT TERM\nprintf \
         '%s\\n' '{report}' > \"$staged\"\nmv -f \"$staged\" \"$TFLOW_RESULT_FILE\"\n"
    );
    crate::files::atomic_write(&helper, script.as_bytes())?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&helper, std::fs::Permissions::from_mode(0o755))?;
    }
    let name = format!("tflow-{execution}");
    let relative = directory.strip_prefix(root)?;
    let mut args = vec![
        "docker".into(),
        "run".into(),
        "--rm".into(),
        "--init".into(),
        "--name".into(),
        name.clone(),
        "--platform".into(),
        format!(
            "linux/{}",
            if task.platform.resolved().1 == crate::config::Arch::Arm64 {
                "arm64"
            } else {
                "amd64"
            }
        ),
        "--mount".into(),
        mount,
        "--workdir".into(),
        format!("/workspace/{}", crate::files::slash(relative)?),
    ];
    // Environment construction already chooses one effective host spelling.
    // Forward that spelling once: expanding declarations can turn Windows
    // aliases into distinct variables inside a Linux container.
    for key in environment.keys().filter(|key| {
        crate::environment::contains_name(
            task.env
                .keys()
                .chain(task.env_inputs.iter())
                .chain(task.secrets.iter())
                .chain(overrides.keys()),
            key,
        )
    }) {
        args.extend(["--env".into(), format!("{key}={}", environment[key])]);
    }
    args.extend([
        "--env".into(),
        format!("TFLOW_RESULT_FILE=/workspace/.taskflow/runs/{execution}/result.json"),
        "--env".into(),
        format!("TFLOW_EXECUTION_ID={execution}"),
        "--env".into(),
        format!("TFLOW_BIN={container_helper}"),
    ]);
    for key in ["TFLOW_SHARD_INPUT", "TFLOW_SHARD_RESULT"] {
        if let Some(value) = crate::environment::get(environment, key) {
            let relative = Path::new(value)
                .strip_prefix(root)
                .context("shard exchange file outside workspace")?;
            args.extend([
                "--env".into(),
                format!("{key}=/workspace/{}", crate::files::slash(relative)?),
            ]);
        }
    }
    for port in &task.platform.ports {
        args.extend(["--publish".into(), port.clone()]);
    }
    args.push(
        task.platform
            .image
            .clone()
            .context("Docker image missing")?,
    );
    match &task.command {
        Command::Argv(command) => args.extend(command.clone()),
        Command::Shell(command) => {
            args.extend(
                task.shell
                    .clone()
                    .unwrap_or_else(|| vec!["/bin/sh".into(), "-c".into()]),
            );
            args.push(command.clone());
        }
    }
    Ok((
        Command::Argv(args),
        Container {
            name,
            cleaned: false,
            directory: root.to_path_buf(),
            environment: host,
            helper: Some(helper_owner),
        },
    ))
}

fn workspace_mount(root: &Path) -> Result<String> {
    let source = root
        .to_str()
        .context("Docker workspace path must be Unicode")?;
    // Docker parses --mount with encoding/csv: quote the entire source field,
    // doubling literal quotes. Shell quoting does not escape CSV separators.
    // Its parser trims field values and normalizes CRLF, so reject those lossy
    // spellings before any client or helper is started.
    ensure!(
        source.trim() == source && !source.contains("\r\n"),
        "Docker workspace path cannot have boundary whitespace or CRLF"
    );
    Ok(format!(
        "type=bind,\"source={}\",target=/workspace",
        source.replace('"', "\"\"")
    ))
}

// Docker's npipe scheme also supports remote Windows servers. Only the literal
// dot server in the documented local UNC form proves a local transport.
fn local_endpoint(endpoint: &str) -> bool {
    if endpoint.chars().any(char::is_control) || endpoint.contains(['%', '?', '#']) {
        return false;
    }
    if let Some(path) = endpoint.strip_prefix("unix://") {
        return path.starts_with('/')
            && !path.starts_with("//")
            && path.len() > 1
            && !path.contains('\\');
    }
    if let Some(path) = endpoint.strip_prefix("npipe://") {
        let normalized = path.replace('\\', "/");
        let parts: Vec<_> = normalized.split('/').collect();
        return parts.len() == 5
            && parts[0].is_empty()
            && parts[1].is_empty()
            && parts[2] == "."
            && parts[3].eq_ignore_ascii_case("pipe")
            && !parts[4].is_empty()
            && !matches!(parts[4], "." | "..");
    }
    false
}

#[cfg(test)]
mod endpoint_tests {
    use super::{local_endpoint, workspace_mount};

    #[test]
    fn workspace_mount_encodes_csv_fields() {
        assert_eq!(
            workspace_mount(std::path::Path::new("/work/project,old\"copy")).unwrap(),
            "type=bind,\"source=/work/project,old\"\"copy\",target=/workspace"
        );
        for path in ["/work/trailing ", "/work/carriage\r\nreturn"] {
            assert!(workspace_mount(std::path::Path::new(path)).is_err());
        }
    }

    #[test]
    fn only_local_socket_addresses_are_accepted() {
        for endpoint in [
            "unix:///var/run/docker.sock",
            "npipe:////./pipe/docker_engine",
            r"npipe://\\.\pipe\dockerDesktopLinuxEngine",
        ] {
            assert!(local_endpoint(endpoint), "{endpoint}");
        }
        for endpoint in [
            "npipe:////remote-host/pipe/docker_engine",
            "npipe:////localhost/pipe/docker_engine",
            "npipe:////127.0.0.1/pipe/docker_engine",
            "npipe:////./pipe/",
            "npipe:////./pipe/../remote",
            "npipe:////%2e/pipe/docker_engine",
            "npipe://remote/pipe/docker_engine",
            "unix://remote/docker.sock",
            "unix:////remote/docker.sock",
            "unix:///",
            "tcp://127.0.0.1:2375",
        ] {
            assert!(!local_endpoint(endpoint), "{endpoint}");
        }
    }
}
