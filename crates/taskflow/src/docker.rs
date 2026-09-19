use std::{collections::BTreeMap, path::Path};

use anyhow::{ensure, Context, Result};
use tokio_util::sync::CancellationToken;

use crate::{
    config::{Command, Task},
    process,
};

pub struct Container {
    name: String,
    cleaned: bool,
}
impl Container {
    pub async fn cleanup(&mut self) -> Result<()> {
        let result = process::capture(
            Path::new("."),
            &Command::Argv(vec![
                "docker".into(),
                "rm".into(),
                "-f".into(),
                self.name.clone(),
            ]),
            &[],
        )
        .await;
        self.cleaned = result.is_ok();
        // --rm may already have removed an exited container; a failed removal is
        // acceptable only after an independent inspect proves it no longer exists.
        if !self.cleaned {
            let exists = process::capture(
                Path::new("."),
                &Command::Argv(vec!["docker".into(), "inspect".into(), self.name.clone()]),
                &[],
            )
            .await
            .is_ok();
            ensure!(!exists, "owned Docker container cleanup failed");
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
                .env_clear()
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null());
            for key in [
                "PATH",
                "HOME",
                "USERPROFILE",
                "SystemRoot",
                "DOCKER_HOST",
                "DOCKER_CONTEXT",
                "DOCKER_CONFIG",
            ] {
                if let Some(value) = std::env::var_os(key) {
                    command.env(key, value);
                }
            }
            let _ = command.status();
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
    let endpoint = if let Ok(host) = std::env::var("DOCKER_HOST") {
        host
    } else {
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
        serde_json::from_slice::<String>(&bytes)?
    };
    ensure!(
        endpoint.starts_with("unix://") || endpoint.starts_with("npipe://"),
        "Docker execution requires a local daemon socket"
    );
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
    ]);
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
        },
    ))
}
