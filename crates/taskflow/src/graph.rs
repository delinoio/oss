use std::{
    collections::{BTreeMap, BTreeSet, VecDeque},
    path::Path,
};

use anyhow::{bail, ensure, Context, Result};
use serde::{Deserialize, Serialize};

use crate::{
    config::{Dependency, Task, WaitFor},
    discover::Workspace,
    files,
};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TaskNode {
    pub id: String,
    pub project: String,
    pub name: String,
    pub task: Task,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TaskEdge {
    pub task: String,
    pub prerequisite: String,
    pub wait_for: WaitFor,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ArtifactEdge {
    pub producer: String,
    pub consumer: String,
    pub output: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Graph {
    pub workspace: Workspace,
    pub tasks: BTreeMap<String, TaskNode>,
    pub edges: Vec<TaskEdge>,
    pub artifacts: Vec<ArtifactEdge>,
    pub explanations: Vec<String>,
    pub unresolved: BTreeSet<String>,
}
pub fn reference(project: &str, value: &str) -> String {
    if value.contains('#') {
        value.into()
    } else {
        format!("{project}#{value}")
    }
}

impl Graph {
    pub fn build(workspace: Workspace) -> Result<Self> {
        let mut graph = Self {
            workspace,
            tasks: BTreeMap::new(),
            edges: vec![],
            artifacts: vec![],
            explanations: vec![],
            unresolved: BTreeSet::new(),
        };
        for project in graph.workspace.projects.values() {
            if let Some(config) = &project.config {
                for (name, task) in &config.tasks {
                    let id = reference(&project.id, name);
                    if let Some(remote) = &graph.workspace.config.remote {
                        crate::environment::validate_remote_inputs(remote, task, &BTreeMap::new())
                            .map_err(|error| {
                                anyhow::anyhow!("invalid environment for {id}: {error}")
                            })?;
                    }
                    graph.tasks.insert(
                        id.clone(),
                        TaskNode {
                            id,
                            project: project.id.clone(),
                            name: name.clone(),
                            task: task.clone(),
                        },
                    );
                }
            }
        }
        for node in graph.tasks.values() {
            for dependency in &node.task.depends_on {
                let mut selected = vec![];
                let wait_for;
                match dependency {
                    Dependency::Reference(value) => {
                        selected.push(reference(&node.project, value));
                        wait_for = WaitFor::Success;
                    }
                    Dependency::Selector(selector) => {
                        wait_for = selector.wait_for;
                        if let Some(kinds) = &selector.from {
                            if !graph.workspace.project_complete(&node.project) {
                                graph.unresolved.insert(node.id.clone());
                            }
                            ensure!(
                                !selector.task.contains('#'),
                                "native selector task must be project-local"
                            );
                            for edge in &graph.workspace.edges {
                                if edge.from == node.project && kinds.contains(edge.kind) {
                                    if edge.active_platforms.as_ref().is_some_and(|platforms| {
                                        !platforms.contains(&node.task.platform.key())
                                    }) {
                                        graph.explanations.push(format!(
                                            "{} skips inactive native condition {:?} for {}",
                                            node.id, edge.condition, edge.to
                                        ));
                                        continue;
                                    }
                                    let candidate = reference(&edge.to, &selector.task);
                                    if graph.tasks.contains_key(&candidate) {
                                        selected.push(candidate);
                                    } else {
                                        graph.explanations.push(format!(
                                            "{} skips task-less native dependency {}",
                                            node.id, candidate
                                        ));
                                    }
                                }
                            }
                        } else {
                            selected.push(reference(&node.project, &selector.task));
                        }
                    }
                }
                for prerequisite in selected {
                    let required = graph.tasks.get(&prerequisite).with_context(|| {
                        format!("{} references missing task {prerequisite}", node.id)
                    })?;
                    ensure!(
                        required.task.service == (wait_for == WaitFor::Ready),
                        "{prerequisite}: service dependencies require waitFor: ready; finite \
                         dependencies require success"
                    );
                    ensure!(
                        wait_for != WaitFor::Ready || required.task.readiness.is_some(),
                        "{prerequisite}: readiness condition is required"
                    );
                    if !graph
                        .edges
                        .iter()
                        .any(|e| e.task == node.id && e.prerequisite == prerequisite)
                    {
                        graph.edges.push(TaskEdge {
                            task: node.id.clone(),
                            prerequisite,
                            wait_for,
                        });
                    }
                }
            }
            for companion in &node.task.with {
                ensure!(
                    graph
                        .tasks
                        .contains_key(&reference(&node.project, companion)),
                    "{} references missing companion {companion}",
                    node.id
                );
            }
        }
        for profile in graph.workspace.config.start.values() {
            for value in profile {
                ensure!(
                    graph
                        .tasks
                        .contains_key(&reference(&graph.workspace.config.project, value)),
                    "start references missing task {value}"
                );
            }
        }
        graph.topological(&graph.tasks.keys().cloned().collect())?;
        graph.validate_outputs()?;
        Ok(graph)
    }

    pub fn prerequisites(&self, id: &str) -> Vec<String> {
        self.edges
            .iter()
            .filter(|e| e.task == id)
            .map(|e| e.prerequisite.clone())
            .collect()
    }

    pub fn dependents(&self, id: &str) -> Vec<String> {
        self.edges
            .iter()
            .filter(|e| e.prerequisite == id)
            .map(|e| e.task.clone())
            .collect()
    }

    pub fn closure(
        &self,
        seeds: &BTreeSet<String>,
        reverse: bool,
        projects: bool,
    ) -> BTreeSet<String> {
        let mut result = seeds.clone();
        let mut queue: VecDeque<_> = seeds.iter().cloned().collect();
        while let Some(id) = queue.pop_front() {
            let neighbors = if projects {
                self.workspace
                    .edges
                    .iter()
                    .filter_map(|e| {
                        if reverse && e.to == id {
                            Some(e.from.clone())
                        } else if !reverse && e.from == id {
                            Some(e.to.clone())
                        } else {
                            None
                        }
                    })
                    .collect()
            } else if reverse {
                self.dependents(&id)
            } else {
                self.prerequisites(&id)
            };
            for neighbor in neighbors {
                if result.insert(neighbor.clone()) {
                    queue.push_back(neighbor);
                }
            }
        }
        result
    }

    pub fn path(&self, source: &str, destination: &str, projects: bool) -> Option<Vec<String>> {
        let mut queue = VecDeque::from([vec![source.to_owned()]]);
        let mut visited = BTreeSet::new();
        while let Some(path) = queue.pop_front() {
            let last = path.last().unwrap();
            if last == destination {
                return Some(path);
            }
            if !visited.insert(last.clone()) {
                continue;
            }
            let neighbors = if projects {
                self.workspace
                    .edges
                    .iter()
                    .filter(|e| &e.from == last)
                    .map(|e| e.to.clone())
                    .collect()
            } else {
                self.prerequisites(last)
            };
            for next in neighbors {
                let mut path = path.clone();
                path.push(next);
                queue.push_back(path);
            }
        }
        None
    }

    pub fn owners(&self, path: &Path) -> Vec<String> {
        let normalized = files::canonical_path(path).unwrap_or_else(|_| path.to_path_buf());
        let path = normalized.as_path();
        let max = self
            .workspace
            .projects
            .values()
            .filter(|p| path.starts_with(&p.directory))
            .map(|p| p.directory.components().count())
            .max();
        self.workspace
            .projects
            .values()
            .filter(|p| {
                path.starts_with(&p.directory) && Some(p.directory.components().count()) == max
            })
            .map(|p| p.id.clone())
            .collect()
    }

    pub fn inputs(&self, path: &Path) -> Result<Vec<String>> {
        let normalized = files::canonical_path(path)?;
        let path = normalized.as_path();
        self.tasks
            .values()
            .filter_map(|t| {
                match files::input_matches(&self.workspace.projects[&t.project], &t.task, path) {
                    Ok(true) => Some(Ok(t.id.clone())),
                    Ok(false) => None,
                    Err(e) => Some(Err(e)),
                }
            })
            .collect()
    }

    pub fn topological(&self, selected: &BTreeSet<String>) -> Result<Vec<String>> {
        let mut pending = selected.clone();
        let mut result = vec![];
        while !pending.is_empty() {
            let ready: Vec<_> = pending
                .iter()
                .filter(|id| self.prerequisites(id).iter().all(|p| !pending.contains(p)))
                .cloned()
                .collect();
            if ready.is_empty() {
                bail!(
                    "task dependency cycle: {}",
                    pending.into_iter().collect::<Vec<_>>().join(" -> ")
                );
            }
            for id in ready {
                pending.remove(&id);
                result.push(id);
            }
        }
        Ok(result)
    }

    fn validate_outputs(&mut self) -> Result<()> {
        let mut owners: Vec<(std::path::PathBuf, String)> = vec![];
        for node in self.tasks.values() {
            let project = &self.workspace.projects[&node.project];
            crate::cache::anchors(&node.task)?;
            for input in node.task.input.iter().flatten() {
                if let crate::config::Input::Pattern(pattern) = input {
                    let prefix = files::output_anchor(pattern.trim_start_matches('!'));
                    files::within(&self.workspace.root, &project.directory.join(prefix))?;
                }
            }
            for output in node.task.output.iter().flatten() {
                let anchor = files::output_anchor(output);
                ensure!(
                    !anchor.as_os_str().is_empty() && anchor != Path::new("."),
                    "{}: output must have a literal owned directory or file prefix",
                    node.id
                );
                let absolute = files::within(&project.directory, &project.directory.join(&anchor))?;
                for (path, owner) in &owners {
                    ensure!(
                        owner == &node.id
                            || !(absolute.starts_with(path) || path.starts_with(&absolute)),
                        "overlapping output ownership: {owner} and {}",
                        node.id
                    );
                }
                owners.push((absolute.clone(), node.id.clone()));
                for consumer in self.tasks.values().filter(|c| c.id != node.id) {
                    let consumer_project = &self.workspace.projects[&consumer.project];
                    // Glob-language intersection is deliberately conservative.
                    // Prefix overlap preserves extension-specific consumers even
                    // before their generated files exist; it does not add ordering.
                    let overlaps = consumer.task.input.as_ref().is_none_or(|inputs| {
                        inputs.iter().any(|input| match input {
                            crate::config::Input::Auto(auto) => {
                                auto.auto && absolute.starts_with(&consumer_project.directory)
                            }
                            crate::config::Input::Pattern(pattern) if !pattern.starts_with('!') => {
                                let prefix = files::normalize(
                                    &consumer_project
                                        .directory
                                        .join(files::output_anchor(pattern)),
                                );
                                prefix.starts_with(&absolute) || absolute.starts_with(prefix)
                            }
                            _ => false,
                        })
                    });
                    if overlaps {
                        self.artifacts.push(ArtifactEdge {
                            producer: node.id.clone(),
                            consumer: consumer.id.clone(),
                            output: output.clone(),
                        });
                    }
                }
            }
        }
        Ok(())
    }
}
