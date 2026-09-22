use std::{
    collections::{BTreeMap, BTreeSet},
    path::PathBuf,
};

use anyhow::{ensure, Result};
use serde::{Deserialize, Serialize};

use crate::{
    config::Effect,
    files,
    graph::{reference, Graph},
};

#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "kebab-case")]
pub enum Cause {
    Direct,
    Input { path: String },
    Prerequisite { task: String },
    NativeDependency { project: String },
    Schedule,
    Activation,
    ExternalEffect,
    IncompleteGraph,
}
impl Cause {
    pub fn independent(&self) -> bool {
        !matches!(self, Self::Prerequisite { .. })
    }
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Plan {
    pub version: u32,
    pub generation: String,
    pub causes: BTreeMap<String, BTreeSet<Cause>>,
    pub order: Vec<String>,
    pub explanations: Vec<String>,
}
impl Plan {
    pub fn create(
        graph: &Graph,
        requests: &[String],
        changes: &[PathBuf],
        affected: bool,
    ) -> Result<Self> {
        Self::create_with_directories(graph, requests, changes, affected, &BTreeSet::new())
    }

    pub async fn from_git(
        graph: &Graph,
        requests: &[String],
        base: &str,
        head: Option<&str>,
    ) -> Result<Self> {
        let (changes, directories) = git_changed_paths(&graph.workspace.root, base, head).await?;
        Self::create_with_directories(graph, requests, &changes, true, &directories)
    }

    fn create_with_directories(
        graph: &Graph,
        requests: &[String],
        changes: &[PathBuf],
        affected: bool,
        directories: &BTreeSet<PathBuf>,
    ) -> Result<Self> {
        let mut causes: BTreeMap<String, BTreeSet<Cause>> = BTreeMap::new();
        if !affected {
            for request in requests {
                let id = reference(&graph.workspace.config.project, request);
                ensure!(
                    graph.tasks.contains_key(&id),
                    "unknown requested task: {id}"
                );
                causes.entry(id).or_default().insert(Cause::Direct);
            }
        } else {
            for request in requests {
                ensure!(
                    graph.tasks.contains_key(request)
                        || (!request.contains('#')
                            && graph.tasks.values().any(|node| node.name == *request)),
                    "unknown requested task: {request}"
                );
            }
            let mut owners = BTreeSet::new();
            for path in changes {
                let path = files::within(&graph.workspace.root, &graph.workspace.root.join(path))?;
                owners.extend(graph.owners(&path));
                let mut matching: BTreeSet<_> = graph.inputs(&path)?.into_iter().collect();
                // Gitlinks represent a whole tree even when the submodule is
                // deleted or uninitialized. Ordinary deleted files retain exact
                // glob filtering; their absence is not directory evidence.
                if path.is_dir() || directories.contains(path.strip_prefix(&graph.workspace.root)?)
                {
                    for node in graph.tasks.values() {
                        let project = &graph.workspace.projects[&node.project];
                        if files::input_event_may_match(project, &node.task, &path)
                            && !files::output_matches(project, &node.task, &path)?
                        {
                            matching.insert(node.id.clone());
                        }
                    }
                }
                for task in matching {
                    causes.entry(task).or_default().insert(Cause::Input {
                        path: files::slash(path.strip_prefix(&graph.workspace.root)?)?,
                    });
                }
                if graph.workspace.metadata_files.contains(&path)
                    || path.file_name().and_then(|p| p.to_str()).is_some_and(|p| {
                        matches!(
                            p,
                            "taskflow.yml"
                                | "package.json"
                                | "pnpm-workspace.yaml"
                                | "pnpm-lock.yaml"
                                | "Cargo.toml"
                                | "Cargo.lock"
                                | "go.mod"
                                | "go.sum"
                                | "go.work"
                                | "go.work.sum"
                        )
                    })
                {
                    for task in graph.tasks.keys() {
                        causes
                            .entry(task.clone())
                            .or_default()
                            .insert(Cause::Input {
                                path: files::slash(path.strip_prefix(&graph.workspace.root)?)?,
                            });
                    }
                }
            }
            let changed_tasks = causes.keys().cloned().collect();
            let ordered_dependents = graph.closure(&changed_tasks, true, false);
            for owner in owners {
                for project in graph.closure(&BTreeSet::from([owner.clone()]), true, true) {
                    if project == owner {
                        continue;
                    }
                    for node in graph
                        .tasks
                        .values()
                        .filter(|n| n.project == project && !ordered_dependents.contains(&n.id))
                    {
                        causes.entry(node.id.clone()).or_default().insert(
                            Cause::NativeDependency {
                                project: owner.clone(),
                            },
                        );
                    }
                }
            }
            if !graph.workspace.complete() {
                for task in graph.tasks.keys() {
                    causes
                        .entry(task.clone())
                        .or_default()
                        .insert(Cause::IncompleteGraph);
                }
            }
            let all = graph.closure(&causes.keys().cloned().collect(), true, false);
            for id in all {
                causes.entry(id).or_default();
            }
            if !requests.is_empty() {
                let roots: BTreeSet<_> = causes
                    .keys()
                    .filter(|id| {
                        requests
                            .iter()
                            .any(|r| r == *id || r == &graph.tasks[*id].name)
                    })
                    .cloned()
                    .collect();
                let selected = graph.closure(&roots, false, false);
                causes.retain(|id, _| selected.contains(id));
            }
        }
        let selected = graph.closure(&causes.keys().cloned().collect(), false, false);
        for id in &selected {
            causes.entry(id.clone()).or_default();
            if graph.tasks[id].task.effect == Effect::External {
                causes.get_mut(id).unwrap().insert(Cause::ExternalEffect);
            }
        }
        // Only forward propagation is suppressible. Prerequisites of directly
        // requested tasks must still run/reuse even without their own file event.
        let initial: BTreeSet<_> = causes
            .iter()
            .filter(|(_, c)| c.iter().any(Cause::independent))
            .map(|(id, _)| id.clone())
            .collect();
        for id in &selected {
            for prerequisite in graph.prerequisites(id) {
                if selected.contains(&prerequisite) {
                    causes
                        .get_mut(id)
                        .unwrap()
                        .insert(Cause::Prerequisite { task: prerequisite });
                }
            }
        }
        for requested in initial {
            for prerequisite in graph.closure(&BTreeSet::from([requested]), false, false) {
                if causes[&prerequisite].is_empty() {
                    causes
                        .get_mut(&prerequisite)
                        .unwrap()
                        .insert(Cause::Activation);
                }
            }
        }
        Ok(Self {
            version: 1,
            generation: graph.workspace.generation.clone(),
            order: graph.topological(&selected)?,
            causes,
            explanations: graph.explanations.clone(),
        })
    }

    pub fn for_tasks(graph: &Graph, seeds: BTreeMap<String, BTreeSet<Cause>>) -> Result<Self> {
        let selected = graph.closure(&seeds.keys().cloned().collect(), false, false);
        let mut causes = seeds;
        for id in &selected {
            let reasons = causes
                .entry(id.clone())
                .or_insert_with(|| BTreeSet::from([Cause::Activation]));
            if graph.tasks[id].task.effect == Effect::External {
                reasons.insert(Cause::ExternalEffect);
            }
        }
        for id in &selected {
            for prerequisite in graph.prerequisites(id) {
                causes
                    .get_mut(id)
                    .unwrap()
                    .insert(Cause::Prerequisite { task: prerequisite });
            }
        }
        Ok(Self {
            version: 1,
            generation: graph.workspace.generation.clone(),
            order: graph.topological(&selected)?,
            causes,
            explanations: graph.explanations.clone(),
        })
    }
}

pub async fn git_changes(
    root: &std::path::Path,
    base: &str,
    head: Option<&str>,
) -> Result<Vec<PathBuf>> {
    Ok(git_changed_paths(root, base, head).await?.0)
}

async fn git_changed_paths(
    root: &std::path::Path,
    base: &str,
    head: Option<&str>,
) -> Result<(Vec<PathBuf>, BTreeSet<PathBuf>)> {
    for revision in std::iter::once(base).chain(head) {
        ensure!(
            !revision.is_empty() && !revision.starts_with('-'),
            "Git revision must be nonempty and must not start with '-'"
        );
    }
    let mut args = vec![
        "git",
        "diff",
        "--raw",
        "--no-abbrev",
        "--no-ext-diff",
        "--ignore-submodules=none",
        "--no-renames",
        "-z",
        base,
    ];
    if let Some(head) = head {
        args.push(head);
    }
    args.push("--");
    let bytes = crate::discover::output_tool(root, &args, &[]).await?;
    let mut paths = BTreeSet::new();
    let mut directories = BTreeSet::new();
    let mut records = bytes.split(|b| *b == 0).filter(|p| !p.is_empty());
    while let Some(header) = records.next() {
        let fields: Vec<_> = std::str::from_utf8(header)?.split_whitespace().collect();
        ensure!(
            fields.len() == 5 && fields[0].starts_with(':'),
            "invalid Git raw diff record"
        );
        let name = records
            .next()
            .ok_or_else(|| anyhow::anyhow!("missing Git raw diff path"))?;
        let path = PathBuf::from(std::str::from_utf8(name)?);
        if fields[0] == ":160000" || fields[1] == "160000" {
            directories.insert(path.clone());
        }
        paths.insert(path);
    }
    if head.is_none() {
        let untracked = crate::discover::output_tool(
            root,
            &["git", "ls-files", "--others", "--exclude-standard", "-z"],
            &[],
        )
        .await?;
        for name in untracked.split(|b| *b == 0).filter(|p| !p.is_empty()) {
            paths.insert(PathBuf::from(std::str::from_utf8(name)?));
        }
    }
    Ok((paths.into_iter().collect(), directories))
}
