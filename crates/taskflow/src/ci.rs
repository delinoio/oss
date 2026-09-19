use std::{
    collections::{BTreeMap, BTreeSet},
    io::{BufReader, Read, Write},
    path::Path,
    sync::Arc,
};

use anyhow::{ensure, Context, Result};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use tokio_util::sync::CancellationToken;

use crate::{
    cache::{self, Artifact},
    discover::{Coverage, ProjectEdge, Workspace},
    files,
    graph::Graph,
    plan::Plan,
    runner::{self, Receipt, RunOptions, RunResult},
    shard::{Inventory, ShardResults},
};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Blueprint {
    pub version: u32,
    pub manifests: BTreeMap<String, String>,
    pub targets: Vec<String>,
    pub edges: BTreeSet<ProjectEdge>,
    pub coverage: Vec<Coverage>,
    pub units: Vec<Unit>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Unit {
    pub id: String,
    pub tasks: Vec<String>,
    pub platform: String,
    pub needs: BTreeSet<String>,
    pub shard: Option<(usize, usize)>,
    pub outputs: BTreeSet<String>,
    pub suites: BTreeMap<String, usize>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CiPlan {
    pub version: u32,
    pub blueprint: String,
    pub plan: Plan,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Bundle {
    pub version: u32,
    pub blueprint: String,
    pub plan: String,
    pub unit: String,
    pub result: RunResult,
    pub artifacts: BTreeMap<String, Artifact>,
    pub shards: BTreeMap<String, (Inventory, Vec<ShardResults>)>,
}
pub fn manifest_state(graph: &Graph) -> Result<BTreeMap<String, String>> {
    graph
        .workspace
        .metadata_files
        .iter()
        .map(|p| {
            Ok((
                files::relative_to(&graph.workspace.root, p)?,
                files::file_state(p)?,
            ))
        })
        .collect()
}
impl Blueprint {
    pub fn new(graph: &Graph, targets: Vec<String>) -> Result<Self> {
        ensure!(
            graph.workspace.complete(),
            "CI export requires fully resolved native metadata"
        );
        let ci = graph
            .workspace
            .config
            .ci
            .as_ref()
            .context("CI export requires ci tool versions and runner mappings")?;
        ensure!(
            ci.revision.len() == 40
                && ci
                    .revision
                    .bytes()
                    .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase()),
            "ci.revision must be an immutable lowercase Git SHA"
        );
        let dated_rust = ci
            .rust
            .strip_prefix("nightly-")
            .or_else(|| ci.rust.strip_prefix("beta-"))
            .is_some_and(|s| chrono::NaiveDate::parse_from_str(s, "%Y-%m-%d").is_ok());
        ensure!(
            dated_rust || exact_version(&ci.rust),
            "CI requires an exact Rust version or dated toolchain"
        );
        for version in [&ci.node, &ci.pnpm, &ci.go].into_iter().flatten() {
            ensure!(
                exact_version(version),
                "CI native tool versions must pin all three numeric components"
            );
        }
        for project in graph.workspace.projects.values() {
            if project.native.contains("pnpm") {
                ensure!(
                    ci.node.is_some() && ci.pnpm.is_some(),
                    "pnpm CI requires exact Node and pnpm versions"
                );
            }
            if project.native.contains("go") {
                ensure!(ci.go.is_some(), "Go CI requires an exact Go version");
            }
        }
        let plan = Plan::create(graph, &targets, &[], false)?;
        for id in &plan.order {
            crate::cache::validate_artifact_outputs(&graph.tasks[id].task)
                .with_context(|| format!("{id}: CI output transfer requires complete ownership"))?;
        }
        let mut groups: BTreeMap<String, BTreeSet<String>> = plan
            .order
            .iter()
            .map(|id| (id.clone(), BTreeSet::from([id.clone()])))
            .collect();
        // A command without an explicit output contract may mutate shared state.
        // Keep its consumers in the same job instead of pretending a receipt
        // transfers that state to another fresh runner.
        for edge in &graph.edges {
            if !groups.values().any(|g| g.contains(&edge.task)) {
                continue;
            }
            if graph.tasks[&edge.prerequisite].task.output.is_none() {
                merge_groups(&mut groups, &edge.task, &edge.prerequisite);
            }
        }
        loop {
            let mut group_edges = BTreeSet::new();
            for edge in &graph.edges {
                if let (Some(a), Some(b)) = (
                    owner(&groups, &edge.task),
                    owner(&groups, &edge.prerequisite),
                ) {
                    if a != b {
                        group_edges.insert((a, b));
                    }
                }
            }
            let mut pending: BTreeSet<_> = groups.keys().cloned().collect();
            loop {
                let ready: Vec<_> = pending
                    .iter()
                    .filter(|id| {
                        !group_edges
                            .iter()
                            .any(|(a, b)| a == *id && pending.contains(b))
                    })
                    .cloned()
                    .collect();
                if ready.is_empty() {
                    break;
                }
                for id in ready {
                    pending.remove(&id);
                }
            }
            if pending.is_empty() {
                break;
            }
            let first = pending.pop_first().unwrap();
            for other in pending {
                let members = groups.remove(&other).unwrap();
                groups.get_mut(&first).unwrap().extend(members);
            }
        }
        let mut units = vec![];
        for members in groups.values() {
            let tasks: Vec<_> = plan
                .order
                .iter()
                .filter(|id| members.contains(*id))
                .cloned()
                .collect();
            let platform = graph.tasks[&tasks[0]].task.platform.key();
            for id in &tasks {
                let task = &graph.tasks[id].task;
                ensure!(
                    !task.service,
                    "CI export does not activate development services"
                );
                ensure!(
                    task.platform.os.is_some() && task.platform.arch.is_some(),
                    "{id}: CI requires explicit OS and architecture"
                );
                ensure!(
                    task.platform.key() == platform,
                    "tasks sharing undeclared state require the same platform"
                );
            }
            ensure!(
                ci.runners.contains_key(&platform),
                "missing CI runner mapping for {platform}"
            );
            let count = if tasks.len() == 1 {
                graph.tasks[&tasks[0]]
                    .task
                    .shard
                    .as_ref()
                    .map_or(1, |s| s.count)
            } else {
                1
            };
            for index in 0..count {
                let id = format!(
                    "task_{}_{}",
                    &files::digest(tasks.join("\n").as_bytes())[..12],
                    index
                );
                units.push(Unit {
                    id,
                    tasks: tasks.clone(),
                    platform: platform.clone(),
                    needs: BTreeSet::new(),
                    shard: (count > 1).then_some((index, count)),
                    outputs: tasks
                        .iter()
                        .filter(|id| {
                            graph.tasks[*id]
                                .task
                                .output
                                .as_ref()
                                .is_some_and(|v| !v.is_empty())
                        })
                        .cloned()
                        .collect(),
                    suites: tasks
                        .iter()
                        .filter_map(|id| {
                            graph.tasks[id]
                                .task
                                .shard
                                .as_ref()
                                .map(|s| (id.clone(), s.count))
                        })
                        .collect(),
                });
            }
        }
        let identity = units.clone();
        for unit in &mut units {
            for task in &unit.tasks {
                for dependency in graph.closure(&BTreeSet::from([task.clone()]), false, false) {
                    if unit.tasks.contains(&dependency) {
                        continue;
                    }
                    unit.needs.extend(
                        identity
                            .iter()
                            .filter(|u| u.tasks.contains(&dependency))
                            .map(|u| u.id.clone()),
                    );
                }
            }
        }
        Ok(Self {
            version: 1,
            manifests: manifest_state(graph)?,
            targets,
            edges: graph.workspace.edges.clone(),
            coverage: graph.workspace.coverage.clone(),
            units,
        })
    }

    pub fn digest(&self) -> Result<String> {
        Ok(files::digest(&serde_json::to_vec(self)?))
    }

    pub async fn graph(&self, root: &Path) -> Result<Graph> {
        ensure!(self.version == 1, "unknown CI blueprint version");
        let mut workspace = Workspace::discover(root).await?;
        let state: BTreeMap<_, _> = workspace
            .metadata_files
            .iter()
            .map(|p| {
                Ok((
                    files::relative_to(&workspace.root, p)?,
                    files::file_state(p)?,
                ))
            })
            .collect::<Result<_>>()?;
        ensure!(
            state == self.manifests,
            "CI native manifests or task configuration changed; regenerate tflow ci export"
        );
        // This is resolved project metadata bound to exact native input bytes, not
        // a replacement compiler plan. It allows a clean runner to plan explicit
        // installation before Cargo's offline metadata cache has been populated.
        workspace.edges = self.edges.clone();
        workspace.coverage = self.coverage.clone();
        workspace.generation = self.digest()?;
        Graph::build(workspace)
    }
}
fn owner(groups: &BTreeMap<String, BTreeSet<String>>, id: &str) -> Option<String> {
    groups
        .iter()
        .find(|(_, ids)| ids.contains(id))
        .map(|(id, _)| id.clone())
}
fn merge_groups(groups: &mut BTreeMap<String, BTreeSet<String>>, a: &str, b: &str) {
    if let (Some(a), Some(b)) = (owner(groups, a), owner(groups, b)) {
        if a != b {
            let other = groups.remove(&b).unwrap();
            groups.get_mut(&a).unwrap().extend(other);
        }
    }
}

pub fn export(graph: &Graph, targets: Vec<String>, output: &Path) -> Result<()> {
    let blueprint = Blueprint::new(graph, targets)?;
    let ci = graph.workspace.config.ci.as_ref().unwrap();
    // Resolve and validate both destinations before either artifact is written.
    // Existing parent links must not redirect exports outside the workspace.
    let workflow_path = files::within(&graph.workspace.root, &graph.workspace.root.join(output))?;
    let blueprint_path = files::within(
        &graph.workspace.root,
        &graph
            .workspace
            .root
            .join(output.with_extension("taskflow.json")),
    )?;
    let blueprint_relative = files::relative_to(&graph.workspace.root, &blueprint_path)?;
    let binary = ".taskflow/tools/source/target/release/tflow";
    let mut jobs = serde_json::Map::new();
    let runner = ci
        .runners
        .get("linux-x64")
        .or_else(|| ci.runners.values().next())
        .context("CI requires a runner")?;
    let setup = || {
        let mut steps = vec![
            json!({"uses":"actions/checkout@v6","with":{"fetch-depth":0,"persist-credentials":false}}),
            json!({"uses":"actions/checkout@v6","with":{"repository":"delinoio/oss","ref":ci.revision,"path":".taskflow/tools/source","persist-credentials":false}}),
            json!({"name":"Build pinned TaskFlow","shell":"bash","env":{"RUSTUP_TOOLCHAIN":ci.rust},"run":format!("rustup toolchain install '{}' --profile minimal\ncargo build --locked --release --manifest-path .taskflow/tools/source/Cargo.toml -p taskflow --bin tflow", ci.rust.replace('\'', "'\"'\"'"))}),
        ];
        if let Some(node) = &ci.node {
            steps.push(json!({"uses":"actions/setup-node@v6","with":{"node-version":node}}));
        }
        if let Some(pnpm) = &ci.pnpm {
            steps.push(
                json!({"uses":"pnpm/action-setup@v5","with":{"version":pnpm,"run_install":false}}),
            );
        }
        if let Some(go) = &ci.go {
            steps
                .push(json!({"uses":"actions/setup-go@v6","with":{"go-version":go,"cache":false}}));
        }
        steps
    };
    let mut prepare = setup();
    prepare.push(json!({"name":"Plan affected work","shell":"bash","env":{"TFLOW_BLUEPRINT":blueprint_relative,"TFLOW_BASE":"${{ github.event.pull_request.base.sha || github.event.before }}"},"run":format!("args=()\nif [[ -n \"$TFLOW_BASE\" && ! \"$TFLOW_BASE\" =~ ^0+$ ]]; then args+=(--base \"$TFLOW_BASE\"); fi\n{binary} ci prepare --blueprint \"$TFLOW_BLUEPRINT\" --output .taskflow/ci-plan.json \"${{args[@]}}\"")}));
    prepare.push(json!({"uses":"actions/upload-artifact@v4","with":{"name":"tflow-plan","path":".taskflow/ci-plan.json","include-hidden-files":true,"if-no-files-found":"error"}}));
    jobs.insert("plan".into(), json!({"runs-on":runner,"steps":prepare}));
    for unit in &blueprint.units {
        let mut steps = setup();
        steps.push(json!({"uses":"actions/download-artifact@v4","with":{"name":"tflow-plan","path":".taskflow"}}));
        if !unit.needs.is_empty() {
            steps.push(json!({"uses":"actions/download-artifact@v4","with":{"pattern":"tflow-result-*","path":".taskflow/ci-input"}}));
        }
        let mut env = serde_json::Map::from_iter([
            ("TFLOW_BLUEPRINT".into(), json!(blueprint_relative)),
            ("TFLOW_UNIT".into(), json!(unit.id)),
            (
                "TFLOW_UNTRUSTED_CI".into(),
                json!(
                    "${{ (github.event_name == 'pull_request' || github.event_name == \
                     'pull_request_target') && '1' || '0' }}"
                ),
            ),
        ]);
        let mut secrets: BTreeSet<String> = unit
            .tasks
            .iter()
            .flat_map(|id| graph.tasks[id].task.secrets.iter().cloned())
            .collect();
        if let Some(remote) = &graph.workspace.config.remote {
            secrets.extend([remote.access_key_env.clone(), remote.secret_key_env.clone()]);
            secrets.extend(remote.session_token_env.iter().cloned());
        }
        for name in secrets {
            ensure!(
                environment_name(&name)
                    && !matches!(
                        name.as_str(),
                        "TFLOW_BLUEPRINT" | "TFLOW_UNIT" | "TFLOW_UNTRUSTED_CI"
                    ),
                "CI secret requires a non-reserved environment identifier"
            );
            env.insert(
                name.clone(),
                json!(format!(
                    "${{{{ (github.event_name != 'pull_request' && github.event_name != \
                     'pull_request_target') && secrets.{name} || '' }}}}"
                )),
            );
        }
        steps.push(json!({"name":"Execute graph unit","shell":"bash","env":env,"run":format!("{binary} ci execute --blueprint \"$TFLOW_BLUEPRINT\" --plan .taskflow/ci-plan.json --unit \"$TFLOW_UNIT\" --input .taskflow/ci-input --output .taskflow/ci-result/bundle.json")}));
        steps.push(json!({"uses":"actions/upload-artifact@v4","if":"always()","with":{"name":format!("tflow-result-{}",unit.id),"path":".taskflow/ci-result/bundle.json","include-hidden-files":true,"if-no-files-found":"error"}}));
        let needs: Vec<_> = std::iter::once("plan".to_owned())
            .chain(unit.needs.iter().cloned())
            .collect();
        jobs.insert(unit.id.clone(), json!({"runs-on":ci.runners[&unit.platform],"needs":needs,"if":"${{ !cancelled() && needs.plan.result == 'success' }}","steps":steps}));
    }
    let mut finish = setup();
    finish.push(json!({"uses":"actions/download-artifact@v4","with":{"name":"tflow-plan","path":".taskflow"}}));
    finish.push(json!({"uses":"actions/download-artifact@v4","with":{"pattern":"tflow-result-*","path":".taskflow/ci-input"}}));
    finish.push(json!({"shell":"bash","env":{"TFLOW_BLUEPRINT":blueprint_relative},"run":format!("{binary} ci aggregate --blueprint \"$TFLOW_BLUEPRINT\" --plan .taskflow/ci-plan.json --input .taskflow/ci-input")}));
    let needs: Vec<_> = jobs.keys().cloned().collect();
    jobs.insert(
        "result".into(),
        json!({"runs-on":runner,"needs":needs,"if":"always()","steps":finish}),
    );
    let workflow = json!({"name":"TaskFlow","on":{"push":{},"pull_request":{},"workflow_dispatch":{}},"permissions":{"contents":"read"},"jobs":jobs});
    files::atomic_write(&blueprint_path, &serde_json::to_vec_pretty(&blueprint)?)?;
    files::atomic_write(&workflow_path, serde_yaml::to_string(&workflow)?.as_bytes())
}

pub async fn prepare(root: &Path, blueprint: &Blueprint, base: Option<&str>) -> Result<CiPlan> {
    let graph = blueprint.graph(root).await?;
    let changes = if let Some(base) = base {
        crate::plan::git_changes(root, base, None).await?
    } else {
        vec![]
    };
    let plan = Plan::create(&graph, &blueprint.targets, &changes, base.is_some())?;
    Ok(CiPlan {
        version: 1,
        blueprint: blueprint.digest()?,
        plan,
    })
}
#[derive(Clone, Copy)]
struct BundleLimits {
    artifacts: usize,
    artifact_bytes: u64,
    envelope_bytes: u64,
}
impl BundleLimits {
    fn for_unit(unit: &Unit) -> Self {
        Self {
            artifacts: unit.outputs.len(),
            artifact_bytes: cache::MAX_CACHE_BYTES as u64,
            envelope_bytes: cache::MAX_CACHE_BYTES as u64,
        }
    }

    fn maximum(self) -> Result<u64> {
        self.artifact_bytes
            .checked_mul(self.artifacts.try_into()?)
            .and_then(|bytes| bytes.checked_add(self.envelope_bytes))
            .context("CI bundle size limit overflow")
    }

    fn validate(self, bundle: &Bundle) -> Result<()> {
        ensure!(
            bundle.artifacts.len() <= self.artifacts,
            "too many CI artifacts"
        );
        let mut artifacts = 0;
        for artifact in bundle.artifacts.values() {
            artifacts += serialized_size(artifact, self.artifact_bytes)
                .context("CI artifact exceeds encoded size limit")?;
        }
        // Only receipts, shard accounting, and JSON framing consume the envelope
        // allowance; combining independent artifacts cannot shrink their limits.
        serialized_size(bundle, artifacts + self.envelope_bytes)
            .context("CI bundle metadata exceeds size limit")?;
        Ok(())
    }
}
fn serialized_size(value: &impl Serialize, limit: u64) -> Result<u64> {
    struct Counter {
        bytes: u64,
        limit: u64,
    }
    impl Write for Counter {
        fn write(&mut self, bytes: &[u8]) -> std::io::Result<usize> {
            if bytes.len() as u64 > self.limit - self.bytes {
                return Err(std::io::Error::other("encoded size limit exceeded"));
            }
            self.bytes += bytes.len() as u64;
            Ok(bytes.len())
        }

        fn flush(&mut self) -> std::io::Result<()> {
            Ok(())
        }
    }
    let mut counter = Counter { bytes: 0, limit };
    serde_json::to_writer(&mut counter, value)?;
    Ok(counter.bytes)
}
fn read_bundle(path: &Path, limits: BundleLimits) -> Result<Bundle> {
    let maximum = limits.maximum()?;
    let file = std::fs::File::open(path)?;
    ensure!(file.metadata()?.len() <= maximum, "CI bundle too large");
    // Bound reads as well as metadata to handle a file growing during parsing,
    // without retaining a second copy of the complete encoded bundle in memory.
    let bundle = serde_json::from_reader(BufReader::new(file).take(maximum + 1))?;
    limits.validate(&bundle)?;
    Ok(bundle)
}
fn read_bundles(path: &Path, blueprint: &Blueprint) -> Result<Vec<Bundle>> {
    if !path.exists() {
        return Ok(vec![]);
    }
    let limits = blueprint
        .units
        .iter()
        .map(BundleLimits::for_unit)
        .max_by_key(|limit| limit.artifacts)
        .unwrap_or(BundleLimits {
            artifacts: 0,
            artifact_bytes: cache::MAX_CACHE_BYTES as u64,
            envelope_bytes: cache::MAX_CACHE_BYTES as u64,
        });
    let mut bundles = vec![];
    for entry in walkdir::WalkDir::new(path) {
        let entry = entry?;
        if entry.file_name() == "bundle.json" && entry.file_type().is_file() {
            ensure!(bundles.len() < blueprint.units.len(), "too many CI bundles");
            let bundle = read_bundle(entry.path(), limits)?;
            let unit = blueprint
                .units
                .iter()
                .find(|unit| unit.id == bundle.unit)
                .context("unknown CI bundle unit")?;
            BundleLimits::for_unit(unit).validate(&bundle)?;
            bundles.push(bundle);
        }
    }
    Ok(bundles)
}
fn environment_name(name: &str) -> bool {
    !name.is_empty()
        && name
            .bytes()
            .enumerate()
            .all(|(i, b)| b == b'_' || b.is_ascii_alphabetic() || (i > 0 && b.is_ascii_digit()))
}
fn exact_version(version: &str) -> bool {
    let components: Vec<_> = version.trim_start_matches('v').split('.').collect();
    components.len() == 3
        && components
            .iter()
            .all(|s| !s.is_empty() && s.bytes().all(|b| b.is_ascii_digit()))
}
fn plan_digest(blueprint: &Blueprint, plan: &CiPlan) -> Result<String> {
    ensure!(
        plan.version == 1
            && plan.blueprint == blueprint.digest()?
            && plan.plan.generation == plan.blueprint,
        "CI plan mismatch"
    );
    Ok(files::digest(&serde_json::to_vec(plan)?))
}
fn validate_bundle(blueprint: &Blueprint, plan: &CiPlan, bundle: &Bundle) -> Result<()> {
    let unit = blueprint
        .units
        .iter()
        .find(|u| u.id == bundle.unit)
        .context("unknown CI unit receipt")?;
    ensure!(
        bundle.version == 1
            && bundle.result.version == 1
            && bundle.blueprint == blueprint.digest()?
            && bundle.plan == plan_digest(blueprint, plan)?,
        "CI receipt blueprint mismatch"
    );
    let expected: BTreeSet<_> = unit
        .tasks
        .iter()
        .filter(|id| plan.plan.causes.contains_key(*id))
        .collect();
    ensure!(
        expected == bundle.result.results.keys().collect(),
        "CI unit is missing a selected task result"
    );
    for (id, receipt) in &bundle.result.results {
        ensure!(
            unit.tasks.contains(id) && receipt.task == *id && receipt.version == 1,
            "CI receipt contains an unexpected task"
        );
    }
    for (id, receipt) in &bundle.result.results {
        if receipt.success() && unit.outputs.contains(id) {
            let artifact = bundle
                .artifacts
                .get(id)
                .context("CI unit is missing required output artifacts")?;
            ensure!(
                artifact.key == receipt.key
                    && artifact.task == *id
                    && artifact.output_digest == receipt.output,
                "CI artifact receipt mismatch"
            );
            artifact
                .validate_integrity(&receipt.key)
                .with_context(|| format!("invalid CI output artifact for {id}"))?;
        }
        if receipt.success() && receipt.outcome != runner::Outcome::Suppressed {
            if let Some(count) = unit.suites.get(id) {
                let (inventory, reports) = bundle
                    .shards
                    .get(id)
                    .context("CI unit is missing shard results")?;
                ensure!(
                    crate::shard::validate_reports(inventory, *count, reports)?,
                    "successful CI receipt contains failed shard results"
                );
                ensure!(
                    match unit.shard {
                        Some((index, count)) =>
                            reports.len() == 1
                                && reports[0].index == index
                                && reports[0].count == count,
                        None => reports.len() == *count,
                    },
                    "CI unit shard selection mismatch"
                );
            }
        }
    }
    for id in bundle.artifacts.keys().chain(bundle.shards.keys()) {
        ensure!(
            unit.tasks.contains(id),
            "CI artifact contains an unexpected task"
        );
    }
    Ok(())
}
pub async fn execute(
    root: &Path,
    blueprint: &Blueprint,
    plan: CiPlan,
    unit_id: &str,
    input: &Path,
    output: &Path,
    cancel: CancellationToken,
) -> Result<RunResult> {
    ensure!(
        plan.version == 1 && plan.blueprint == blueprint.digest()?,
        "CI plan mismatch"
    );
    let graph = Arc::new(blueprint.graph(root).await?);
    let unit = blueprint
        .units
        .iter()
        .find(|u| u.id == unit_id)
        .context("unknown CI unit")?;
    let mut provided = BTreeMap::new();
    let bundles = read_bundles(input, blueprint)?;
    let mut seen = BTreeSet::new();
    let mut shard_reports: BTreeMap<String, (Inventory, Vec<ShardResults>)> = BTreeMap::new();
    for bundle in bundles {
        validate_bundle(blueprint, &plan, &bundle)?;
        ensure!(seen.insert(bundle.unit.clone()), "duplicate CI unit result");
        if !unit.needs.contains(&bundle.unit) {
            continue;
        }
        for (id, artifact) in &bundle.artifacts {
            let node = &graph.tasks[id];
            let receipt = bundle
                .result
                .results
                .get(id)
                .context("artifact lacks receipt")?;
            ensure!(
                receipt.success() && artifact.output_digest == receipt.output,
                "artifact result mismatch"
            );
            artifact.restore(
                &receipt.key,
                id,
                &graph.workspace.projects[&node.project],
                &node.task,
            )?;
        }
        for (id, (inventory, reports)) in bundle.shards {
            let entry = shard_reports
                .entry(id)
                .or_insert((inventory.clone(), vec![]));
            ensure!(
                serde_json::to_vec(&entry.0)? == serde_json::to_vec(&inventory)?,
                "CI shard inventory mismatch"
            );
            entry.1.extend(reports);
        }
        for (id, receipt) in bundle.result.results {
            if let Some(existing) = provided.get_mut(&id) {
                let existing: &mut Receipt = existing;
                if !receipt.success() {
                    *existing = receipt;
                } else {
                    existing.changed |= receipt.changed;
                }
            } else {
                provided.insert(id, receipt);
            }
        }
    }
    for need in &unit.needs {
        ensure!(
            seen.contains(need),
            "missing prerequisite CI receipt: {need}"
        );
    }
    for (id, (inventory, reports)) in shard_reports {
        let count = graph.tasks[&id].task.shard.as_ref().unwrap().count;
        if !crate::shard::aggregate(&inventory, count, &reports)? {
            provided.get_mut(&id).unwrap().outcome = runner::Outcome::Failed;
        }
    }
    let selected: BTreeSet<_> = unit
        .tasks
        .iter()
        .filter(|id| plan.plan.causes.contains_key(*id))
        .cloned()
        .collect();
    let mut local = plan.plan.clone();
    let prerequisites = graph.closure(&selected, false, false);
    local.order = graph.topological(&prerequisites)?;
    local.causes.retain(|id, _| prerequisites.contains(id));
    local.generation = graph.workspace.generation.clone();
    let options = RunOptions {
        provided,
        shard: unit.shard,
        ..RunOptions::default()
    };
    let mut result = runner::run_plan(graph.clone(), local, options, cancel).await?;
    result.results.retain(|id, _| unit.tasks.contains(id));
    let mut bundle = Bundle {
        version: 1,
        blueprint: blueprint.digest()?,
        plan: plan_digest(blueprint, &plan)?,
        unit: unit_id.into(),
        result: result.clone(),
        artifacts: BTreeMap::new(),
        shards: BTreeMap::new(),
    };
    for (id, receipt) in &result.results {
        if !receipt.success() {
            continue;
        }
        let node = &graph.tasks[id];
        if node.task.output.as_ref().is_some_and(|v| !v.is_empty()) {
            bundle.artifacts.insert(
                id.clone(),
                Artifact::capture(
                    receipt.key.clone(),
                    id.clone(),
                    &graph.workspace.projects[&node.project],
                    &node.task,
                )?,
            );
        }
        if node.task.shard.is_some() && receipt.outcome != runner::Outcome::Suppressed {
            let directory = root.join(".taskflow/runs").join(&receipt.execution);
            let (inventory, reports) = crate::shard::read_reports(&directory)?;
            bundle.shards.insert(id.clone(), (inventory, reports));
        }
    }
    BundleLimits::for_unit(unit).validate(&bundle)?;
    files::atomic_write(output, &serde_json::to_vec(&bundle)?)?;
    Ok(result)
}
pub fn aggregate(blueprint: &Blueprint, plan: &CiPlan, input: &Path) -> Result<Value> {
    plan_digest(blueprint, plan)?;
    let bundles = read_bundles(input, blueprint)?;
    let mut seen = BTreeSet::new();
    let mut success = true;
    let mut shards: BTreeMap<String, (Inventory, Vec<ShardResults>)> = BTreeMap::new();
    for bundle in bundles {
        validate_bundle(blueprint, plan, &bundle)?;
        ensure!(seen.insert(bundle.unit), "duplicate CI unit result");
        success &= bundle.result.success && bundle.result.results.values().all(Receipt::success);
        for (id, (inventory, reports)) in bundle.shards {
            let entry = shards.entry(id).or_insert((inventory.clone(), vec![]));
            ensure!(
                serde_json::to_vec(&entry.0)? == serde_json::to_vec(&inventory)?,
                "shard inventory mismatch"
            );
            entry.1.extend(reports);
        }
    }
    ensure!(
        seen.len() == blueprint.units.len(),
        "missing CI job receipts"
    );
    for (inventory, reports) in shards.values() {
        let count = reports.first().context("empty shard report")?.count;
        success &= crate::shard::aggregate(inventory, count, reports)?;
    }
    Ok(json!({"version":1,"success":success,"units":seen.len()}))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ci_bundle_limits_apply_to_each_artifact_independently() {
        let artifact = |task: &str| Artifact {
            version: 1,
            key: "key".into(),
            task: task.into(),
            output_digest: "digest".into(),
            result_identity: None,
            files: vec![cache::FileRecord {
                path: "out".into(),
                content: cache::Content::File {
                    data: "a".repeat(2000),
                    digest: "digest".into(),
                    executable: false,
                },
            }],
            shards: None,
        };
        let mut bundle = Bundle {
            version: 1,
            blueprint: "blueprint".into(),
            plan: "plan".into(),
            unit: "unit".into(),
            result: RunResult {
                version: 1,
                success: true,
                results: BTreeMap::new(),
            },
            artifacts: BTreeMap::from([("a".into(), artifact("a")), ("b".into(), artifact("b"))]),
            shards: BTreeMap::new(),
        };
        // Scale only the byte limit, keeping the actual wire codec and checks.
        let limits = BundleLimits {
            artifacts: 2,
            artifact_bytes: 3000,
            envelope_bytes: 1000,
        };
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("bundle.json");
        let bytes = serde_json::to_vec(&bundle).unwrap();
        assert!(bytes.len() > limits.artifact_bytes as usize);
        std::fs::write(&path, bytes).unwrap();
        assert_eq!(read_bundle(&path, limits).unwrap().artifacts.len(), 2);
        bundle.artifacts.get_mut("a").unwrap().key = "a".repeat(1500);
        std::fs::write(&path, serde_json::to_vec(&bundle).unwrap()).unwrap();
        assert!(read_bundle(&path, limits)
            .unwrap_err()
            .to_string()
            .contains("CI artifact"));
        bundle.artifacts.clear();
        bundle.plan = "x".repeat(1500);
        std::fs::write(&path, serde_json::to_vec(&bundle).unwrap()).unwrap();
        assert!(read_bundle(&path, limits)
            .unwrap_err()
            .to_string()
            .contains("metadata"));
        std::fs::write(&path, vec![b' '; limits.maximum().unwrap() as usize + 1]).unwrap();
        assert!(read_bundle(&path, limits)
            .unwrap_err()
            .to_string()
            .contains("too large"));
    }
}
