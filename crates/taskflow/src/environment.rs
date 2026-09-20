use std::{
    collections::{BTreeMap, BTreeSet},
    path::Path,
};

use anyhow::{ensure, Result};

use crate::{
    config::Task,
    discover::{Project, Workspace},
};

pub(crate) fn inherited() -> Result<BTreeMap<String, String>> {
    std::env::vars_os()
        .map(|(key, value)| {
            // Never include the entry bytes in this diagnostic: either half may
            // contain a credential, and lossy conversion would change identity.
            let key = key.into_string().map_err(|_| {
                anyhow::anyhow!("inherited environment contains a non-Unicode name")
            })?;
            let value = value.into_string().map_err(|_| {
                anyhow::anyhow!("inherited environment contains a non-Unicode value")
            })?;
            Ok((key, value))
        })
        .collect()
}

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
        for (key, value) in inherited()?
            .into_iter()
            .chain(task.env.clone())
            .chain(overrides.clone())
        {
            insert(&mut values, key, value);
        }
        // A CI unit can carry credentials for several tasks. Designation is
        // workspace-wide, but access remains scoped to each declaring task.
        for name in ws
            .projects
            .values()
            .filter_map(|project| project.config.as_ref())
            .chain(std::iter::once(&ws.config))
            .flat_map(|config| config.tasks.values())
            .flat_map(|task| &task.secrets)
        {
            if !contains_name(task.secrets.iter(), name) {
                values.retain(|key, _| !same_name(key, name));
            }
        }
        let secrets = task
            .secrets
            .iter()
            .filter_map(|name| get(&values, name))
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
                    get(&values, key)
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
            values.retain(|key, _| contains_name(keys.iter(), key));
        }
        if let Some(remote) = &ws.config.remote {
            validate_remote_inputs(remote, task, overrides)?;
            for key in [&remote.access_key_env, &remote.secret_key_env]
                .into_iter()
                .chain(remote.session_token_env.iter())
            {
                values.retain(|name, _| !same_name(name, key));
            }
        }
        Ok(Self {
            values,
            secrets,
            fingerprint,
        })
    }
}

pub(crate) fn validate_remote_inputs(
    remote: &crate::config::RemoteConfig,
    task: &Task,
    overrides: &BTreeMap<String, String>,
) -> Result<()> {
    for key in [&remote.access_key_env, &remote.secret_key_env]
        .into_iter()
        .chain(remote.session_token_env.iter())
    {
        ensure!(
            !contains_name(task.env_inputs.iter(), key)
                && !contains_name(task.env.keys(), key)
                && !contains_name(task.secrets.iter(), key)
                && !contains_name(overrides.keys(), key),
            "cache transport credentials cannot be task inputs"
        );
    }
    Ok(())
}

fn same_name(left: &str, right: &str) -> bool {
    #[cfg(windows)]
    {
        use windows_sys::Win32::Globalization::{CompareStringOrdinal, CSTR_EQUAL};
        // Match Windows and Rust's process environment comparison, including its
        // OS-specific Unicode casing table rather than locale-dependent folding.
        let left: Vec<_> = left.encode_utf16().collect();
        let right: Vec<_> = right.encode_utf16().collect();
        let result = unsafe {
            CompareStringOrdinal(
                left.as_ptr(),
                left.len().try_into().expect("environment name too long"),
                right.as_ptr(),
                right.len().try_into().expect("environment name too long"),
                1,
            )
        };
        assert_ne!(result, 0, "Windows environment name comparison failed");
        result == CSTR_EQUAL
    }
    #[cfg(not(windows))]
    {
        left == right
    }
}

fn contains_name<'a>(names: impl IntoIterator<Item = &'a String>, key: &str) -> bool {
    names.into_iter().any(|name| same_name(name, key))
}

pub(crate) fn insert(values: &mut BTreeMap<String, String>, key: String, value: String) {
    values.retain(|name, _| !same_name(name, &key));
    values.insert(key, value);
}

pub(crate) fn get<'a>(values: &'a BTreeMap<String, String>, key: &str) -> Option<&'a String> {
    values
        .iter()
        .find_map(|(name, value)| same_name(name, key).then_some(value))
}

fn read_dotenv(path: &Path, values: &mut BTreeMap<String, String>) -> Result<()> {
    if path.is_file() {
        for pair in dotenvy::from_path_iter(path)? {
            let (key, value) = pair?;
            insert(values, key, value);
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
