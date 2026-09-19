use std::{
    collections::{BTreeMap, BTreeSet},
    path::Path,
};

use anyhow::{ensure, Result};

use crate::{
    config::Task,
    discover::{Project, Workspace},
};

#[derive(Clone)]
pub struct Environment {
    pub values: BTreeMap<String, String>,
    pub secrets: Vec<Vec<u8>>,
    pub fingerprint: BTreeMap<String, String>,
}

impl Environment {
    pub fn build(
        ws: &Workspace,
        project: &Project,
        task: &Task,
        overrides: &BTreeMap<String, String>,
        no_dotenv: bool,
    ) -> Result<Self> {
        let mut values = BTreeMap::new();
        if !no_dotenv
            && task.dotenv.unwrap_or(
                project
                    .config
                    .as_ref()
                    .map_or(ws.config.dotenv, |c| c.dotenv),
            )
        {
            read_dotenv(&ws.root.join(".env"), &mut values)?;
            if project.directory != ws.root {
                read_dotenv(&project.directory.join(".env"), &mut values)?;
            }
        }
        values.extend(std::env::vars());
        values.extend(task.env.clone());
        values.extend(overrides.clone());
        let secrets = task
            .secrets
            .iter()
            .filter_map(|name| values.get(name))
            .filter(|v| !v.is_empty())
            .map(|v| v.as_bytes().to_vec())
            .collect();
        let mut keys: BTreeSet<_> = task
            .env_inputs
            .iter()
            .cloned()
            .chain(task.env.keys().cloned())
            .chain(overrides.keys().cloned())
            .collect();
        keys.extend(task.secrets.iter().cloned());
        let fingerprint = keys
            .iter()
            .map(|key| {
                (
                    key.clone(),
                    values
                        .get(key)
                        .map_or("<missing>".into(), |v| crate::files::digest(v.as_bytes())),
                )
            })
            .collect();
        if task.cache {
            // OS lookup/runtime context is necessary to launch native tools. Its
            // resolved tool identity is keyed separately; user command inputs must
            // be explicitly declared rather than assuming environment hermeticity.
            keys.extend(
                [
                    "PATH",
                    "HOME",
                    "USERPROFILE",
                    "SystemRoot",
                    "SYSTEMROOT",
                    "WINDIR",
                    "COMSPEC",
                    "PATHEXT",
                    "TEMP",
                    "TMP",
                    "TMPDIR",
                    "CARGO_HOME",
                    "RUSTUP_HOME",
                ]
                .into_iter()
                .map(str::to_owned),
            );
            values.retain(|key, _| keys.contains(key));
        }
        if let Some(remote) = &ws.config.remote {
            for key in [&remote.access_key_env, &remote.secret_key_env]
                .into_iter()
                .chain(remote.session_token_env.iter())
            {
                ensure!(
                    !task.env_inputs.contains(key)
                        && !task.env.contains_key(key)
                        && !task.secrets.contains(key)
                        && !overrides.contains_key(key),
                    "cache transport credentials cannot be task inputs"
                );
                values.remove(key);
            }
        }
        Ok(Self {
            values,
            secrets,
            fingerprint,
        })
    }
}
fn read_dotenv(path: &Path, values: &mut BTreeMap<String, String>) -> Result<()> {
    if path.is_file() {
        for pair in dotenvy::from_path_iter(path)? {
            let (key, value) = pair?;
            values.insert(key, value);
        }
    }
    Ok(())
}

/// Byte-oriented streaming masking preserves incomplete matches between reads.
pub struct Redactor {
    secrets: Vec<Vec<u8>>,
    pending: Vec<u8>,
    keep: usize,
}
impl Redactor {
    pub fn new(mut secrets: Vec<Vec<u8>>) -> Self {
        secrets.retain(|v| !v.is_empty());
        secrets.sort_by_key(|v| std::cmp::Reverse(v.len()));
        secrets.dedup();
        let keep = secrets.iter().map(Vec::len).max().unwrap_or(1) - 1;
        Self {
            secrets,
            pending: vec![],
            keep,
        }
    }

    pub fn push(&mut self, bytes: &[u8], eof: bool) -> Vec<u8> {
        self.pending.extend_from_slice(bytes);
        let limit = if eof {
            self.pending.len()
        } else {
            self.pending.len().saturating_sub(self.keep)
        };
        let mut index = 0;
        let mut output = vec![];
        while index < limit {
            if let Some(secret) = self
                .secrets
                .iter()
                .find(|s| self.pending[index..].starts_with(s))
            {
                output.extend_from_slice(b"[REDACTED]");
                index += secret.len();
            } else {
                output.push(self.pending[index]);
                index += 1;
            }
        }
        self.pending.drain(..index);
        output
    }

    pub fn mask(bytes: &[u8], secrets: Vec<Vec<u8>>) -> Vec<u8> {
        Self::new(secrets).push(bytes, true)
    }
}
