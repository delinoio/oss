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
}

#[derive(Debug)]
pub(crate) struct CleanupFailure;
impl std::fmt::Display for CleanupFailure {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("Docker container cleanup could not be confirmed")
    }
}
impl std::error::Error for CleanupFailure {}
pub fn host_environment(environment: &BTreeMap<String, String>) -> BTreeMap<String, String> {
    let mut values = environment.clone();
    for key in ["DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"] {
        if let Ok(value) = std::env::var(key) {
            crate::environment::insert(&mut values, key.into(), value);
        }
    }
    values
}
impl Container {
    pub async fn cleanup(&mut self) -> Result<()> {
        self.cleanup_inner().await.context(CleanupFailure)
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
    execution: &str,
    cancel: &CancellationToken,
) -> Result<(Command, Container)> {
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
        environment,
        cancel,
    )
    .await?;
    let endpoint = serde_json::from_slice::<String>(&bytes)?;
    ensure!(
        endpoint.starts_with("unix://") || endpoint.starts_with("npipe://"),
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
    let helper = root.join(format!(".taskflow/runs/{execution}/tflow-result"));
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
        format!("type=bind,source={},target=/workspace", root.display()),
        "--workdir".into(),
        format!("/workspace/{}", crate::files::slash(relative)),
    ];
    for key in task
        .env
        .keys()
        .chain(task.env_inputs.iter())
        .chain(task.secrets.iter())
    {
        args.extend(["--env".into(), key.clone()]);
    }
    args.extend([
        "--env".into(),
        format!("TFLOW_RESULT_FILE=/workspace/.taskflow/runs/{execution}/result.json"),
        "--env".into(),
        "TFLOW_EXECUTION_ID".into(),
        "--env".into(),
        format!("TFLOW_BIN=/workspace/.taskflow/runs/{execution}/tflow-result"),
    ]);
    for key in ["TFLOW_SHARD_INPUT", "TFLOW_SHARD_RESULT"] {
        if let Some(value) = crate::environment::get(environment, key) {
            let relative = Path::new(value)
                .strip_prefix(root)
                .context("shard exchange file outside workspace")?;
            args.extend([
                "--env".into(),
                format!("{key}=/workspace/{}", crate::files::slash(relative)),
            ]);
        }
    }
    for port in &task.platform.ports {
        ensure!(!port.starts_with('-'), "invalid Docker port mapping");
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
            environment: environment.clone(),
        },
    ))
}
