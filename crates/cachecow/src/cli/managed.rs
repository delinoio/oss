// SPDX-License-Identifier: Apache-2.0
//! Managed cache operations: discovery, planning, supervisor claims and
//! provenance (Go `internal/cli/managed.go`).

use std::{
    collections::{BTreeMap, BTreeSet},
    time::Duration,
};

use anyhow::{Result, anyhow};
use serde::{Deserialize, Serialize};
use serde_json::json;

use super::{App, Failure, bad, environ, notice};
use crate::{
    Context, clone, error,
    lease::{Record, Registry},
    lifecycle::{self, LifecycleResult, Pair},
    paths,
    preset::{self, Compatibility, Id, PresetResult},
    process,
    repository::{self, Info, Snapshot},
    session::{self, Action, Plan, RecordHooks, SourceError, error_text},
    shim, state,
};

/// Discovery evidence sent to the supervisor with each plan.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Claims {
    #[serde(rename = "Current")]
    pub current: PresetResult,
    #[serde(rename = "Source")]
    pub source: Option<PresetResult>,
    #[serde(rename = "Snapshot", default)]
    pub snapshot: Snapshot,
}

/// Successful-build provenance `cache-<key>.json`.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Provenance {
    #[serde(rename = "schemaVersion")]
    pub schema_version: i64,
    pub compatibility: Compatibility,
    pub paths: Vec<String>,
}

pub(crate) fn paths_of(r: &PresetResult) -> Vec<String> {
    r.roots.iter().map(|root| root.path.clone()).collect()
}

fn record_key(paths: &[String]) -> String {
    state::key(&serde_json::to_string(paths).unwrap_or_default())
}

pub(crate) fn metadata_path(root: &str, r: &PresetResult) -> String {
    paths::join2(root, &format!("cache-{}.json", record_key(&paths_of(r))))
}

pub(crate) fn transaction_path(root: &str, r: &PresetResult) -> String {
    paths::join2(
        root,
        &format!("transaction-{}.json", record_key(&paths_of(r))),
    )
}

/// Working directory in the source worktree that corresponds to the
/// canonical current directory (#4575).
pub(crate) fn source_working_directory(current: &Info, source_root: &str) -> Result<String> {
    let rel = paths::rel(&current.root, &current.cwd).ok_or_else(|| {
        anyhow!(
            "cannot relate the working directory {} to its worktree {}",
            current.cwd,
            current.root
        )
    })?;
    if rel == ".." || rel.starts_with(&format!("..{}", paths::SEPARATOR)) {
        return Err(anyhow!(
            "working directory {} is outside its worktree {}",
            current.cwd,
            current.root
        ));
    }
    Ok(paths::join2(source_root, &rel))
}

/// Validate is called in the supervisor, including after acquiring the
/// complete resource set. No tool or Git process runs inside this boundary.
pub(crate) fn validate(p: &Plan) -> Result<()> {
    if p.schema_version != 1 {
        return Err(anyhow!("unsupported operation schemaVersion"));
    }
    let c: Claims = serde_json::from_value(p.claims.clone())?;
    for selected in std::iter::once(&c.current).chain(c.source.as_ref()) {
        if selected.inputs.is_empty() {
            return Err(anyhow!(
                "preset cache-selection inputs are missing; repeat discovery"
            ));
        }
        preset::verify_cache_paths(selected)?;
        repository::verify_file_stamps(&selected.inputs)?;
    }
    if !c.current.compatibility.verified() {
        return Err(anyhow!("unverified preset compatibility"));
    }
    if p.record != transaction_path(&p.root, &c.current) {
        return Err(anyhow!("invalid transaction record path"));
    }
    c.snapshot.verify()?;
    if c.snapshot.repository.id != c.current.repository.id {
        return Err(anyhow!("snapshot belongs to another worktree"));
    }
    let expected = paths_of(&c.current);
    if expected != p.resources.destinations {
        return Err(anyhow!("cache claims differ from discovered destinations"));
    }
    let allowed: BTreeSet<&str> = c.snapshot.caches.iter().map(String::as_str).collect();
    let protected = vec![p.root.clone()];
    for v in &expected {
        if !allowed.contains(v.as_str()) {
            return Err(anyhow!("destination missing from discovery snapshot"));
        }
        repository::verify_cache_claim(&c.current.repository, v, &protected)?;
    }
    if let Some(source) = &c.source {
        if source.repository.common_dir != c.current.repository.common_dir {
            return Err(anyhow!("source belongs to another repository"));
        }
        if !c.current.compatibility.compatible(&source.compatibility) {
            return Err(anyhow!(
                "source tool version, platform, architecture, or build settings differ"
            ));
        }
        if paths_of(source) != p.resources.sources {
            return Err(anyhow!("source claims differ from discovered roots"));
        }
        for v in &p.resources.sources {
            if !allowed.contains(v.as_str()) {
                return Err(anyhow!("source missing from discovery snapshot"));
            }
            repository::verify_cache_claim(&source.repository, v, &protected)?;
        }
    } else if !p.resources.sources.is_empty() {
        return Err(anyhow!("source resources have no verified claims"));
    }
    for src in &p.resources.sources {
        for dst in &expected {
            if repository::overlaps(src, dst) {
                return Err(anyhow!("source and destination resource sets overlap"));
            }
        }
    }
    for pair in &p.pairs {
        if !expected.contains(&pair.destination) {
            return Err(anyhow!("unverified destination pair"));
        }
        if pair.source == pair.destination {
            continue;
        }
        if c.source.is_none() || !p.resources.sources.contains(&pair.source) {
            return Err(anyhow!("unverified source pair"));
        }
        if repository::overlaps(&pair.source, &pair.destination) {
            return Err(anyhow!("source and destination caches overlap"));
        }
    }
    Ok(())
}

/// An unusable source that permits the interactive recovery choices.
#[derive(Debug, thiserror::Error)]
#[error("{}", error_text(.0))]
struct SourceUnavailable(anyhow::Error);

fn source_unavailable(e: anyhow::Error) -> anyhow::Error {
    anyhow::Error::new(SourceUnavailable(e))
}

/// Preset configuration files recognized by detection.
const FRONTEND_EXTENSIONS: [&str; 6] = ["js", "ts", "mjs", "cjs", "mts", "cts"];

/// Detects the preset from the nearest ancestor of the canonical working
/// directory that contains a recognized configuration (#4575).
pub(crate) fn detect_preset(info: &Info) -> Result<String> {
    let mut dir = info.cwd.clone();
    while repository::contains(&info.root, &dir) {
        let mut found = Vec::new();
        if std::fs::metadata(paths::join2(&dir, "Cargo.toml")).is_ok() {
            found.push("cargo");
        }
        for name in ["rspack", "rsbuild"] {
            if FRONTEND_EXTENSIONS.iter().any(|ext| {
                std::fs::metadata(paths::join2(&dir, &format!("{name}.config.{ext}"))).is_ok()
            }) {
                found.push(name);
            }
        }
        match found.len() {
            1 => return Ok(found[0].to_string()),
            0 => {}
            _ => {
                return Err(bad(
                    "ambiguous preset; select --preset cargo, rspack, or rsbuild"
                ));
            }
        }
        if dir == info.root {
            break;
        }
        let parent = paths::dir(&dir);
        if parent == dir {
            break;
        }
        dir = parent;
    }
    Err(bad(
        "cannot detect a preset; select --preset cargo, rspack, or rsbuild"
    ))
}

impl App {
    fn detect(&self, cwd: &str) -> Result<String> {
        if !self.o.preset.is_empty() {
            return Ok(self.o.preset.clone());
        }
        let info = repository::discover(&self.ctx, cwd)?;
        detect_preset(&info)
    }

    fn discover(&self, cwd: &str, argv: &[String]) -> Result<PresetResult> {
        self.discover_with(cwd, argv, environ())
    }

    /// Discovers with an explicit child environment.
    fn discover_with(&self, cwd: &str, argv: &[String], env: Vec<String>) -> Result<PresetResult> {
        let mut id = self.o.preset.clone();
        if id.is_empty() {
            if let Some(tool) = argv.first().and_then(|a| shim::from_argv0(a)) {
                id = tool.as_str().to_string();
            }
            if id.is_empty() {
                id = self.detect(cwd)?;
            }
        }
        let tool = shim::Tool::parse(&id).ok_or_else(|| bad(format!("unknown preset {id}")))?;
        let mut discovery = argv.to_vec();
        if discovery.is_empty() {
            discovery = vec![self.resolve(tool)?.path];
        } else {
            // Discovery and execution must resolve the same executable. An
            // explicit path must never certify the unrelated tool
            // found on PATH.
            discovery[0] = self.command(&discovery, cwd)?.path;
        }
        preset::discover(
            &self.ctx,
            preset::Request {
                cwd: cwd.to_string(),
                argv: discovery,
                env: Some(env),
                preset: Id::parse(&id),
                protected: vec![self.store.root.clone()],
                runner: None,
            },
        )
    }

    pub(super) fn managed(&mut self, action: &str, argv: &[String]) -> Result<i32> {
        let cwd = paths::current_dir()?;
        if (action == "run" || action == "tool-clean") && argv.is_empty() {
            return Err(bad("run requires -- COMMAND [ARGS...]"));
        }
        let current = self.discover(&cwd, argv)?;
        if action == "status" || action == "doctor" {
            self.inspect(&current, action == "doctor")?;
            return Ok(0);
        }
        if self.o.replace
            && self.interactive()
            && !self.confirm("Replace the selected working cache after a new clone is prepared?")?
        {
            return Err(anyhow!("replacement cancelled"));
        }
        for name in ["processes", "leases"] {
            self.store.directory(name)?;
        }
        let mut planned = self.plan(action, &current, argv, false);
        loop {
            let failure = match planned {
                Ok((p, c)) => match self.execute(&p, c) {
                    Ok(code) => return Ok(code),
                    Err(err) => {
                        if error::find::<SourceError>(&err).is_none() {
                            return Err(err);
                        }
                        source_unavailable(err)
                    }
                },
                Err(e) => e,
            };
            if error::find::<SourceUnavailable>(&failure).is_none() {
                return Err(failure);
            }
            if !self.interactive() {
                return Err(anyhow::Error::new(Failure::new(
                    125,
                    error_text(&failure),
                    "Select a usable same-repository --source, or run interactively to choose a \
                     source/build. Build the source with ccow run -- and the exact requested \
                     command first. prepare never builds a source.",
                )));
            }
            notice(&format!("Source unavailable: {}", error_text(&failure)));
            let prompt = if action == "run" {
                "Choose: 1 build here without source; 2 select another source; 3 build source \
                 first; 4 cancel: "
            } else {
                "Choose: 2 select another source; 4 cancel: "
            };
            let choice = self.answer(prompt)?;
            planned = match choice.as_str() {
                "1" if action == "run" => self.plan(action, &current, argv, true),
                "2" => {
                    let selected = self.answer("Source worktree: ")?;
                    self.o.source = selected;
                    self.plan(action, &current, argv, false)
                }
                "3" if action == "run" => match self.build_source_first(&current, argv)? {
                    SourceBuild::Failed(code) => return Ok(code),
                    SourceBuild::Built => self.plan(action, &current, argv, false),
                    SourceBuild::Skipped => Err(failure),
                },
                "4" => return Err(anyhow!("operation cancelled")),
                _ => Err(failure),
            };
        }
    }

    /// Choice 3: runs the exact requested command in the source after
    /// confirmation.
    fn build_source_first(
        &mut self,
        current: &PresetResult,
        argv: &[String],
    ) -> Result<SourceBuild> {
        let settings = self.store.settings(&current.repository.id)?;
        let src = match repository::select_source(
            &self.ctx,
            &current.repository,
            &self.o.source,
            &settings.source,
        ) {
            Ok(src) => src,
            Err(e) => {
                notice(&error_text(&e));
                return Ok(SourceBuild::Skipped);
            }
        };
        if src.is_self {
            notice("Source is the current worktree; choose build here instead.");
            return Ok(SourceBuild::Skipped);
        }
        let source_cwd = source_working_directory(&current.repository, &src.repository.root)?;
        // Run the request as it would run in the source (#4578): translate
        // current-root paths so the recorded provenance matches the translated
        // source discovery of the following derived plan.
        let source_env = crate::preset::translate_worktree_root(
            &environ(),
            &current.repository.root,
            &src.repository.root,
        );
        let source_argv = crate::preset::translate_worktree_root(
            argv,
            &current.repository.root,
            &src.repository.root,
        );
        notice(&format!(
            "Source working directory: {source_cwd}\nExact requested argv: {}",
            quote_argv(&source_argv)
        ));
        if !self.confirm("Run this exact command in the source?")? {
            return Ok(SourceBuild::Skipped);
        }
        let source_result = match self.discover_with(&source_cwd, &source_argv, source_env.clone())
        {
            Ok(r) => r,
            Err(e) => {
                notice(&error_text(&e));
                return Ok(SourceBuild::Skipped);
            }
        };
        let (sp, mut sc) = self.plan("run", &source_result, &source_argv, true)?;
        sc.env = source_env;
        let code = self.execute(&sp, sc)?;
        if code != 0 {
            return Ok(SourceBuild::Failed(code));
        }
        Ok(SourceBuild::Built)
    }

    /// Builds the supervisor plan and the command to run.
    fn plan(
        &self,
        action: &str,
        current: &PresetResult,
        argv: &[String],
        without_source: bool,
    ) -> Result<(Plan, process::Command)> {
        let session_action = match action {
            "run" => Action::Run,
            "prepare" => Action::Prepare,
            "clean" => Action::Clean,
            "tool-clean" => Action::ToolClean,
            other => return Err(anyhow!("unknown session action {other}")),
        };
        let mut p = Plan {
            schema_version: 1,
            root: self.store.root.clone(),
            action: session_action,
            replace: self.o.replace && !without_source,
            allow_copy: self.o.allow_copy,
            record: transaction_path(&self.store.root, current),
            ..Plan::default()
        };
        let mut c = process::Command {
            noop: true,
            ..process::Command::default()
        };
        if action == "run" || action == "tool-clean" {
            c = self.command(argv, &current.repository.cwd)?;
        }
        p.resources.destinations = paths_of(current);
        let mut claims = Claims {
            current: current.clone(),
            ..Claims::default()
        };
        let mut need = self.o.replace;
        for root in &current.roots {
            // Validate every root, as Go did, before deciding.
            let exists = lifecycle::exists(&root.path)?;
            need = need || !exists;
        }
        if action == "clean" || action == "tool-clean" {
            need = false;
        }
        if need && !without_source {
            let saved = self.store.settings(&current.repository.id)?;
            let src = repository::select_source(
                &self.ctx,
                &current.repository,
                &self.o.source,
                &saved.source,
            )
            .map_err(source_unavailable)?;
            if src.is_self {
                if action == "prepare" {
                    return Err(source_unavailable(anyhow!(
                        "primary cache is missing; select another source or use ccow run to build \
                         it"
                    )));
                }
                if self.o.replace {
                    return Err(bad(
                        "cannot replace a cache from itself; choose another --source"
                    ));
                }
                // Only the primary checkout receives the automatic
                // build-in-place exception.
                if current.repository.root != current.repository.primary {
                    return Err(source_unavailable(anyhow!(
                        "selected source is the current derived worktree; choose another source"
                    )));
                }
            } else {
                let source_cwd =
                    source_working_directory(&current.repository, &src.repository.root)?;
                // Discover the source as if it ran there: current-root paths in
                // the environment and arguments become
                // source-root paths (#4578).
                let env = crate::preset::translate_worktree_root(
                    &environ(),
                    &current.repository.root,
                    &src.repository.root,
                );
                let source_argv = crate::preset::translate_worktree_root(
                    argv,
                    &current.repository.root,
                    &src.repository.root,
                );
                let source = self
                    .discover_with(&source_cwd, &source_argv, env)
                    .map_err(source_unavailable)?;
                if !current.compatibility.compatible(&source.compatibility) {
                    return Err(source_unavailable(anyhow!(
                        "source tool versions, platform, architecture, or build settings are \
                         incompatible"
                    )));
                }
                if source.roots.len() != current.roots.len() {
                    return Err(source_unavailable(anyhow!(
                        "source and destination cache layouts differ"
                    )));
                }
                for (sr, dst) in source.roots.iter().zip(&current.roots) {
                    if sr.kinds != dst.kinds {
                        return Err(source_unavailable(anyhow!(
                            "source and destination cache root roles differ"
                        )));
                    }
                    if repository::overlaps(&sr.path, &dst.path) {
                        return Err(source_unavailable(anyhow!(
                            "source and destination cache paths overlap"
                        )));
                    }
                    p.pairs.push(Pair {
                        source: sr.path.clone(),
                        destination: dst.path.clone(),
                    });
                }
                p.resources.sources = paths_of(&source);
                claims.source = Some(source);
            }
        }
        if p.pairs.is_empty() {
            p.pairs = paths_of(current)
                .into_iter()
                .map(|path| Pair {
                    source: path.clone(),
                    destination: path,
                })
                .collect();
        }
        let mut all = p.resources.destinations.clone();
        all.extend(p.resources.sources.iter().cloned());
        claims.snapshot = repository::capture_snapshot(
            &self.ctx,
            &current.repository,
            &all,
            std::slice::from_ref(&self.store.root),
        )?;
        p.claims = serde_json::to_value(&claims)?;
        Ok((p, c))
    }

    fn execute(&mut self, p: &Plan, c: process::Command) -> Result<i32> {
        let capability = std::env::var(session::ENV).unwrap_or_default();
        if !capability.is_empty() {
            return self.execute_nested(&capability, p, c);
        }
        let noop = c.noop;
        let (run, prepared, result) =
            session::execute(&self.ctx, p, c, process::Streams::default());
        result?;
        report_copy(&prepared);
        if noop && run.exit_code == 0 {
            self.output(&prepared)?;
            return Ok(0);
        }
        Ok(run.exit_code)
    }

    fn execute_nested(&mut self, capability: &str, p: &Plan, c: process::Command) -> Result<i32> {
        let response = session::nested(&self.ctx, capability, p)?;
        report_copy(&response.result);
        let mut code = 0;
        if !c.noop {
            let mut cmd = process::new_exec_command(&c)?;
            self.ctx.err()?;
            match cmd.spawn() {
                Ok(mut child) => {
                    // The outer native scope owns all descendants, including
                    // children that detach. Ask that owner
                    // to cancel; killing only this child
                    // would leak writers.
                    let capability = capability.to_string();
                    let ticket = response.ticket.clone();
                    let stop = self.ctx.after_func(move || {
                        let (ctx, _cancel) =
                            Context::background().with_timeout(Duration::from_secs(1));
                        if let Err(e) = session::cancel(&ctx, &capability, &ticket) {
                            notice(&format!(
                                "Cannot request session termination; outer cache protection \
                                 remains active: {}",
                                error_text(&e)
                            ));
                        }
                    });
                    let status = child.wait();
                    stop.stop();
                    code = match status {
                        Ok(status) => process::exit_code(status),
                        Err(e) => process::execution_code(&anyhow::Error::new(e)),
                    };
                }
                Err(e) => code = process::execution_code(&anyhow::Error::new(e)),
            }
        }
        session::complete(&self.ctx, capability, &response.ticket, code)?;
        if c.noop {
            self.output(&response.result)?;
            return Ok(0);
        }
        Ok(code)
    }

    fn inspect(&self, r: &PresetResult, doctor: bool) -> Result<()> {
        let mut caches = BTreeMap::new();
        for p in paths_of(r) {
            let exists = lifecycle::exists(&p)?;
            caches.insert(p, exists);
        }
        let registry = Registry::open(&self.ctx, &paths::join2(&self.store.root, "leases"))?;
        let mut records = registry.records(&self.ctx)?;
        let mut recovered = Vec::new();
        let mut retained = Vec::new();
        if doctor {
            // Reconcile only journals with positive completion evidence.
            // Incomplete journals retain their protection, even
            // when the owner PID is gone.
            for v in &records {
                let Ok(scope) = process::load_scope(&v.ownership) else {
                    continue;
                };
                if process::reconcile(&scope).is_ok() {
                    registry.reconcile(&self.ctx, v, &|_: &Context, _: &Record| {
                        process::reconcile(&scope)
                    })?;
                    recovered.push(json!({"id": v.id, "journal": v.ownership}));
                }
            }
            records = registry.records(&self.ctx)?;
            for v in &records {
                let reason = process::load_scope(&v.ownership)
                    .map_or(process::Retention::Unreadable, |scope| {
                        process::retention(&scope)
                    });
                retained
                    .push(json!({"id": v.id, "journal": v.ownership, "reason": reason.as_str()}));
            }
        }
        let saved = self.store.settings(&r.repository.id)?;
        let mut out = json!({
            "preset": r.preset,
            "cachePaths": caches,
            "compatibility": r.compatibility,
            "settings": saved,
            "leases": records,
            "cowCapability": "tested during preparation; --allow-copy permits ordinary fallback",
            "recovery": "Incomplete native journals retain leases. PID death alone cannot release protection.",
        });
        if doctor {
            out["retainedLeases"] = json!(retained);
            out["recoveredLeases"] = json!(recovered);
        }
        self.output(&out)
    }
}

/// Outcome of the interactive "build source first" choice.
enum SourceBuild {
    /// Not built (declined, unusable selection or failed discovery); prompt
    /// again with the previous failure.
    Skipped,
    /// The source build succeeded; plan the derived operation again.
    Built,
    /// The source build exited with this nonzero code; stop with it.
    Failed(i32),
}

fn report_copy(result: &LifecycleResult) {
    for tree in &result.trees {
        if tree.copied_files > 0 {
            notice(&format!(
                "Ordinary copying was used for {} files (--allow-copy).",
                tree.copied_files
            ));
        }
    }
}

/// Go `%q` formatting of an argument list.
fn quote_argv(argv: &[String]) -> String {
    let quoted: Vec<String> = argv.iter().map(|a| format!("{a:?}")).collect();
    format!("[{}]", quoted.join(" "))
}

fn decode(p: &Plan) -> Result<Claims> {
    Ok(serde_json::from_value(p.claims.clone())?)
}

fn validate_source(p: &Plan) -> Result<()> {
    let c = decode(p)?;
    let Some(source) = &c.source else {
        return Ok(());
    };
    for root in &source.roots {
        if !lifecycle::exists(&root.path).unwrap_or(false) {
            return Err(anyhow!(
                "source cache is missing or unusable; build the source first"
            ));
        }
        paths::walk_dir(&root.path, &mut |entry| {
            let internal_link = entry.file_type.is_symlink()
                && clone::source_link_is_internal(&root.path, &entry.path);
            if !entry.is_dir() && !entry.file_type.is_file() && !internal_link {
                return Err(anyhow!(
                    "source cache contains a symlink or special file; repair it or select another \
                     source"
                ));
            }
            Ok(paths::Walk::Continue)
        })?;
    }
    let saved: Provenance = state::read(&metadata_path(&p.root, source)).map_err(|e| {
        anyhow!(
            "source provenance is unavailable; run the exact requested command through ccow in \
             the source first: {}",
            error_text(&e)
        )
    })?;
    if saved.schema_version != 1
        || !saved.compatibility.compatible(&source.compatibility)
        || saved.paths != p.resources.sources
    {
        return Err(anyhow!(
            "source provenance does not match current settings; run the exact command through \
             ccow in the source first"
        ));
    }
    Ok(())
}

fn compatible(a: &Plan, b: &Plan) -> bool {
    match (decode(a), decode(b)) {
        (Ok(x), Ok(y)) => x.current.compatibility.compatible(&y.current.compatibility),
        _ => false,
    }
}

fn before(p: &Plan) -> Result<()> {
    let c = decode(p)?;
    if p.action == Action::Prepare && c.source.is_none() {
        return Ok(());
    }
    // Leases serialize every overlapping resource set, including sets with
    // different metadata keys. Invalidate all intersecting records before a
    // writer can change any certified cache.
    let mut entries = Vec::new();
    for entry in std::fs::read_dir(&p.root)? {
        let name = entry?.file_name();
        let Some(name) = name.to_str() else { continue };
        if name.starts_with("cache-") && name.ends_with(".json") {
            entries.push(paths::join2(&p.root, name));
        }
    }
    entries.sort();
    let current = paths_of(&c.current);
    let mut remove = Vec::new();
    for path in entries {
        let old: Provenance = match state::read(&path) {
            Ok(v) => v,
            // A disjoint session may remove its own record.
            Err(e) if error::is_not_found(&e) => continue,
            Err(e) => return Err(e),
        };
        if old.schema_version != 1 {
            return Err(anyhow!(
                "unsupported cache provenance schemaVersion; use a compatible ccow"
            ));
        }
        if old.paths.iter().any(|previous| {
            current
                .iter()
                .any(|now| repository::overlaps(previous, now))
        }) {
            remove.push(path);
        }
    }
    for path in remove {
        match std::fs::remove_file(&path) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(e.into()),
            _ => {}
        }
    }
    Ok(())
}

fn after(p: &Plan, code: i32) -> Result<()> {
    if code != 0 || p.action == Action::Clean || p.action == Action::ToolClean {
        return Ok(());
    }
    let c = decode(p)?;
    if p.action == Action::Prepare && !p.prepared_all {
        return Ok(());
    }
    state::write(
        &metadata_path(&p.root, &c.current),
        &Provenance {
            schema_version: 1,
            compatibility: c.current.compatibility.clone(),
            paths: paths_of(&c.current),
        },
    )
}

/// Provenance is updated under the supervisor's lease, never after the client
/// observes completion. Interrupted or failed builds retain caches but cannot
/// certify them as compatible sources for another worktree.
pub(crate) fn records() -> RecordHooks {
    RecordHooks {
        validate_source: Some(validate_source),
        compatible: Some(compatible),
        before: Some(before),
        after: Some(after),
    }
}
