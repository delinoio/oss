#[cfg(windows)]
use std::os::windows::io::{BorrowedHandle, OwnedHandle};
use std::{
    borrow::Cow,
    cell::RefCell,
    ffi::{OsStr, OsString},
    fs::{DirEntry, Metadata},
    io,
    path::{Path, PathBuf},
    process::Stdio,
    sync::Arc,
};

#[cfg(unix)]
use fspy_shared_unix::exec::Exec;
use rustc_hash::FxHashMap;
use tokio::process::Command as TokioCommand;
use tokio_util::sync::CancellationToken;
use which::sys::{RealSys, Sys};

use crate::{SPY_IMPL, TrackedChild, error::SpawnError};

#[derive(derive_more::Debug)]
pub struct Command {
    program: OsString,
    args: Vec<OsString>,
    envs: FxHashMap<OsString, OsString>,
    cwd: Option<PathBuf>,
    pub(crate) resolution_accesses: Vec<PathBuf>,
    #[cfg(windows)]
    pub(crate) windows_job: Option<OwnedHandle>,
    #[cfg(unix)]
    arg0: Option<OsString>,

    stderr: Option<Stdio>,
    stdout: Option<Stdio>,
    stdin: Option<Stdio>,

    #[cfg(unix)]
    #[debug("({} pre_exec closures)", pre_exec_closures.len())]
    pre_exec_closures: Vec<Box<dyn FnMut() -> std::io::Result<()> + Send + Sync>>,
}

impl Command {
    /// Return the configured program path, including any prior resolution.
    #[must_use]
    pub fn program(&self) -> &OsStr {
        &self.program
    }

    /// Create a new command to spy on the given program.
    /// Initially, environment variables are not inherited from the parent.
    /// To inherit, explicitly use `.envs(std::env::vars_os())`.
    pub fn new<P: AsRef<OsStr>>(program: P) -> Self {
        Self {
            program: program.as_ref().to_os_string(),
            args: Vec::new(),
            envs: FxHashMap::default(),
            cwd: None,
            resolution_accesses: Vec::new(),
            #[cfg(windows)]
            windows_job: None,
            #[cfg(unix)]
            arg0: None,
            stderr: None,
            stdout: None,
            stdin: None,
            #[cfg(unix)]
            pre_exec_closures: Vec::new(),
        }
    }

    #[cfg(unix)]
    #[must_use]
    pub(crate) fn get_exec(&self) -> Exec {
        use std::{
            iter::once,
            os::unix::ffi::{OsStrExt, OsStringExt},
        };

        use bstr::{BString, ByteSlice as _};
        let arg0 = BString::from(
            self.arg0
                .clone()
                .unwrap_or_else(|| self.program.clone())
                .into_vec(),
        );
        Exec {
            program: self.program.as_bytes().into(),
            args: once(arg0)
                .chain(
                    self.args
                        .iter()
                        .map(|arg| arg.as_bytes().as_bstr().to_owned()),
                )
                .collect(),
            envs: self
                .envs
                .iter()
                .map(|(name, value)| (name.as_bytes().into(), Some(value.as_bytes().into())))
                .collect(),
        }
    }

    #[cfg(unix)]
    pub(crate) fn set_exec(&mut self, mut exec: Exec) {
        use std::os::unix::ffi::OsStringExt;

        self.program = OsString::from_vec(exec.program.into());
        self.arg0 = Some(OsString::from_vec(exec.args.remove(0).into()));
        self.args = exec
            .args
            .into_iter()
            .map(|arg| OsString::from_vec(arg.into()))
            .collect();
        self.envs = exec
            .envs
            .into_iter()
            .map(|(name, value)| {
                (
                    OsString::from_vec(name.into()),
                    OsString::from_vec(value.unwrap_or_default().into()),
                )
            })
            .collect();
    }

    pub fn env_remove<K: AsRef<OsStr>>(&mut self, key: K) -> &mut Self {
        self.envs.remove(key.as_ref());
        self
    }

    pub fn stderr<T: Into<Stdio>>(&mut self, cfg: T) -> &mut Self {
        self.stderr = Some(cfg.into());
        self
    }

    pub fn stdout<T: Into<Stdio>>(&mut self, cfg: T) -> &mut Self {
        self.stdout = Some(cfg.into());
        self
    }

    pub fn stdin<T: Into<Stdio>>(&mut self, cfg: T) -> &mut Self {
        self.stdin = Some(cfg.into());
        self
    }

    pub fn env<K, V>(&mut self, key: K, val: V) -> &mut Self
    where
        K: AsRef<OsStr>,
        V: AsRef<OsStr>,
    {
        self.envs
            .insert(key.as_ref().to_os_string(), val.as_ref().to_os_string());
        self
    }

    pub fn envs<I, K, V>(&mut self, vars: I) -> &mut Self
    where
        I: IntoIterator<Item = (K, V)>,
        K: AsRef<OsStr>,
        V: AsRef<OsStr>,
    {
        self.envs.extend(
            vars.into_iter()
                .map(|(key, val)| (key.as_ref().to_os_string(), val.as_ref().to_os_string())),
        );
        self
    }

    pub fn current_dir<P: AsRef<Path>>(&mut self, dir: P) -> &mut Self {
        self.cwd = Some(dir.as_ref().to_owned());
        self
    }

    #[cfg(windows)]
    pub fn windows_job_handle(&mut self, job: BorrowedHandle<'_>) -> io::Result<&mut Self> {
        self.windows_job = Some(job.try_clone_to_owned()?);
        Ok(self)
    }

    pub fn arg<S: AsRef<OsStr>>(&mut self, arg: S) -> &mut Self {
        self.args.push(arg.as_ref().to_os_string());
        self
    }

    pub fn args<I, S>(&mut self, args: I) -> &mut Self
    where
        I: IntoIterator<Item = S>,
        S: AsRef<OsStr>,
    {
        self.args
            .extend(args.into_iter().map(|arg| arg.as_ref().to_os_string()));
        self
    }

    #[cfg(unix)]
    pub fn arg0<S>(&mut self, arg: S) -> &mut Self
    where
        S: AsRef<OsStr>,
    {
        self.arg0 = Some(arg.as_ref().to_os_string());
        self
    }

    /// Spawn the command with file system access tracking.
    ///
    /// # Errors
    ///
    /// Returns [`SpawnError`] if program resolution fails or the process cannot
    /// be spawned.
    pub async fn spawn(
        mut self,
        cancellation_token: CancellationToken,
    ) -> Result<TrackedChild, SpawnError> {
        self.resolve_program()?;
        let spy = SPY_IMPL
            .as_ref()
            .map_err(|error| SpawnError::SpyInitialization(Arc::clone(error)))?;
        spy.spy.spawn(self, cancellation_token).await
    }

    /// Resolve program name to full path using `PATH` and cwd.
    ///
    /// # Errors
    ///
    /// Returns [`SpawnError::Which`] if the program cannot be found in `PATH`.
    ///
    /// # Panics
    ///
    /// Panics if no `cwd` is set and `std::env::current_dir()` fails.
    pub fn resolve_program(&mut self) -> Result<(), SpawnError> {
        let mut path_env: Option<&OsStr> = None;
        for (env_name, env_value) in &self.envs {
            let Some(env_name) = env_name.to_str() else {
                continue;
            };
            if env_name.eq_ignore_ascii_case("path") {
                path_env = Some(env_value.as_ref());
                break;
            }
        }

        let cwd = self
            .cwd
            .clone()
            .unwrap_or_else(|| std::env::current_dir().expect("failed to get current dir"));
        let lookup_cwd = std::env::current_dir().unwrap_or_else(|_| cwd.clone());
        let checked = RefCell::new(Vec::new());
        let recorder = RecordingSys {
            checked: &checked,
            lookup_cwd: &lookup_cwd,
        };
        let mut query = which::WhichConfig::new_with_sys(recorder)
            .binary_name(self.program.clone())
            .custom_cwd(cwd.clone());
        if let Some(path) = path_env {
            query = query.custom_path_list(path.to_owned());
        }
        let selected = query.first_result().map_err(|err| SpawnError::Which {
            program: self.program.clone(),
            path: path_env.map(OsStr::to_owned),
            cwd,
            cause: err,
        })?;
        self.resolution_accesses.extend(checked.into_inner());
        // PATH entries can be relative. Bind execution to the exact candidate
        // checked by which, using the lookup process's working directory.
        self.program = if selected.is_absolute() {
            selected
        } else {
            lookup_cwd.join(selected)
        }
        .into_os_string();
        Ok(())
    }

    /// Schedules a closure to be run just before the exec function is invoked.
    ///
    /// # Safety
    ///
    /// <https://doc.rust-lang.org/1.91.1/std/os/unix/process/trait.CommandExt.html#tymethod.pre_exec>
    #[cfg(unix)]
    pub unsafe fn pre_exec<F>(&mut self, f: F) -> &mut Self
    where
        F: FnMut() -> std::io::Result<()> + Send + Sync + 'static,
    {
        self.pre_exec_closures.push(Box::new(f));
        self
    }

    /// Convert to a `tokio::process::Command` without tracking.
    #[must_use]
    pub(crate) fn into_tokio_command(self) -> TokioCommand {
        let mut tokio_cmd = TokioCommand::new(self.program);
        if let Some(cwd) = &self.cwd {
            tokio_cmd.current_dir(cwd);
        }

        #[cfg(unix)]
        if let Some(arg0) = self.arg0 {
            tokio_cmd.arg0(arg0);
        }
        tokio_cmd.args(self.args);
        tokio_cmd.env_clear();
        tokio_cmd.envs(self.envs);

        if let Some(stdin) = self.stdin {
            tokio_cmd.stdin(stdin);
        }

        if let Some(stdout) = self.stdout {
            tokio_cmd.stdout(stdout);
        }

        if let Some(stderr) = self.stderr {
            tokio_cmd.stderr(stderr);
        }

        #[cfg(unix)]
        for pre_exec in self.pre_exec_closures {
            // Safety: The caller of `pre_exec` is responsible for ensuring safety.
            unsafe { tokio_cmd.pre_exec(pre_exec) };
        }

        tokio_cmd
    }
}

struct RecordingSys<'a> {
    checked: &'a RefCell<Vec<PathBuf>>,
    lookup_cwd: &'a Path,
}

impl RecordingSys<'_> {
    fn record(&self, path: &Path) {
        self.checked.borrow_mut().push(if path.is_absolute() {
            path.to_owned()
        } else {
            self.lookup_cwd.join(path)
        });
    }
}

impl Sys for RecordingSys<'_> {
    type Metadata = Metadata;
    type ReadDirEntry = DirEntry;

    fn is_windows(&self) -> bool {
        RealSys.is_windows()
    }

    fn current_dir(&self) -> io::Result<PathBuf> {
        RealSys.current_dir()
    }

    fn home_dir(&self) -> Option<PathBuf> {
        RealSys.home_dir()
    }

    fn env_split_paths(&self, paths: &OsStr) -> Vec<PathBuf> {
        RealSys.env_split_paths(paths)
    }

    fn env_path(&self) -> Option<OsString> {
        RealSys.env_path()
    }

    fn env_path_ext(&self) -> Option<OsString> {
        RealSys.env_path_ext()
    }

    fn env_windows_path_ext(&self) -> Cow<'static, [String]> {
        RealSys.env_windows_path_ext()
    }

    fn metadata(&self, path: &Path) -> io::Result<Self::Metadata> {
        self.record(path);
        RealSys.metadata(path)
    }

    fn symlink_metadata(&self, path: &Path) -> io::Result<Self::Metadata> {
        self.record(path);
        RealSys.symlink_metadata(path)
    }

    fn read_dir(
        &self,
        path: &Path,
    ) -> io::Result<Box<dyn Iterator<Item = io::Result<Self::ReadDirEntry>>>> {
        RealSys.read_dir(path)
    }

    fn is_valid_executable(&self, path: &Path) -> io::Result<bool> {
        RealSys.is_valid_executable(path)
    }
}

#[cfg(all(test, unix))]
mod tests {
    use std::{fs, os::unix::fs::PermissionsExt};

    use tokio_util::sync::CancellationToken;

    use super::Command;

    #[test]
    fn records_missing_path_candidates_before_the_selected_image() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let first = directory.path().join("first");
        let second = directory.path().join("second");
        fs::create_dir(&first).expect("create first PATH directory");
        fs::create_dir(&second).expect("create second PATH directory");
        let found = second.join("tool");
        fs::write(&found, "#!/bin/sh\n").expect("write executable");
        fs::set_permissions(&found, fs::Permissions::from_mode(0o755)).expect("make executable");

        let mut command = Command::new("tool");
        command.env(
            "PATH",
            std::env::join_paths([&first, &second]).expect("build PATH"),
        );
        command.resolve_program().expect("resolve tool");
        assert_eq!(command.program, found.as_os_str());
        assert!(command.resolution_accesses.contains(&first.join("tool")));
        assert!(command.resolution_accesses.contains(&found));
    }

    #[tokio::test]
    async fn trace_includes_missing_path_candidate() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let first = directory.path().join("first");
        let second = directory.path().join("second");
        fs::create_dir(&first).expect("create first PATH directory");
        fs::create_dir(&second).expect("create second PATH directory");
        let source = directory.path().join("tool.c");
        let executable = second.join("tool");
        fs::write(&source, "int main(void) { return 0; }\n").expect("write fixture");
        assert!(
            std::process::Command::new("cc")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile fixture")
                .success()
        );

        let mut command = Command::new("tool");
        command.env(
            "PATH",
            std::env::join_paths([&first, &second]).expect("build PATH"),
        );
        let child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn tracked fixture");
        let termination = child.wait_handle.await.expect("wait for fixture");
        assert!(termination.status.success());
        let accesses = termination.path_accesses.expect("complete trace");
        let missing = first.join("tool");
        assert!(accesses.iter().any(|access| {
            access.mode.contains(super::super::AccessMode::READ)
                && access.path.strip_path_prefix(&missing, |path| {
                    path.is_ok_and(|remaining| remaining.as_os_str().is_empty())
                })
        }));
    }
}
