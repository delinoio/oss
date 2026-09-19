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
                for task in graph.inputs(&path)? {
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
    let mut args = vec!["git", "diff", "--name-only", "--no-renames", "-z", base];
    if let Some(head) = head {
        args.push(head);
    }
    args.push("--");
    let mut bytes = crate::discover::output_tool(root, &args, &[]).await?;
    if head.is_none() {
        bytes.extend(
            crate::discover::output_tool(
                root,
                &["git", "ls-files", "--others", "--exclude-standard", "-z"],
                &[],
            )
            .await?,
        );
    }
    let paths: Result<BTreeSet<_>> = bytes
        .split(|b| *b == 0)
        .filter(|p| !p.is_empty())
        .map(|p| Ok(PathBuf::from(std::str::from_utf8(p)?)))
        .collect();
    Ok(paths?.into_iter().collect())
}
