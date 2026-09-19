use std::{
    collections::BTreeSet,
    path::{Path, PathBuf},
    sync::Arc,
};

use anyhow::{ensure, Context, Result};
use clap::{Args, Parser, Subcommand, ValueEnum};
use serde_json::{json, Value};
use tokio_util::sync::CancellationToken;

use crate::{
    ci, config,
    discover::{self, Workspace},
    files,
    graph::Graph,
    plan::Plan,
    runner::{self, RunOptions},
};

#[derive(Parser)]
#[command(
    name = "tflow",
    version,
    about = "Graph-driven native command and development orchestration"
)]
pub struct Cli {
    #[arg(long, global = true)]
    pub root: Option<PathBuf>,
    #[arg(long, global = true)]
    pub json: bool,
    #[arg(long, global = true)]
    pub no_color: bool,
    /// Default OS for tasks that do not declare one.
    #[arg(long, global = true, value_enum)]
    pub os: Option<config::Os>,
    /// Default architecture for tasks that do not declare one.
    #[arg(long, global = true, value_enum)]
    pub arch: Option<config::Arch>,
    #[command(subcommand)]
    pub command: Action,
}
#[derive(Subcommand)]
pub enum Action {
    /// Validate configuration, graph references, cycles, and output ownership.
    Check,
    /// Print the version-1 configuration JSON Schema.
    Schema,
    /// Query project/task relationships and file ownership.
    Query {
        #[arg(value_enum)]
        query: Query,
        node: Option<String>,
        destination: Option<String>,
        #[arg(long)]
        projects: bool,
        #[arg(long)]
        transitive: bool,
    },
    /// Explain requested or affected work without executing task commands.
    Plan(Selection),
    /// Execute finite commands and their prerequisites.
    Run {
        #[command(flatten)]
        selection: Selection,
        #[command(flatten)]
        execution: Execution,
    },
    /// Run a development profile until its owners exit or it is interrupted.
    Start {
        #[arg(default_value = "default")]
        profile: String,
        #[command(flatten)]
        execution: Execution,
    },
    /// Report no change from inside a running task.
    Result {
        #[arg(value_enum)]
        result: Report,
    },
    /// Inspect, verify, or remove task cache entries.
    Cache {
        #[arg(value_enum)]
        action: CacheAction,
    },
    /// Export and execute distributed GitHub Actions plans.
    Ci {
        #[command(subcommand)]
        command: CiAction,
    },
}
#[derive(Clone, Copy, ValueEnum)]
pub enum Query {
    Projects,
    Tasks,
    Deps,
    Rdeps,
    Path,
    Owners,
    Inputs,
    Artifacts,
}
#[derive(Clone, Copy, ValueEnum)]
pub enum Report {
    Unchanged,
}
#[derive(Clone, Copy, ValueEnum)]
pub enum CacheAction {
    List,
    Verify,
    Clean,
}
#[derive(Args)]
pub struct Selection {
    pub tasks: Vec<String>,
    #[arg(long)]
    pub affected: bool,
    #[arg(long)]
    pub base: Option<String>,
    #[arg(long)]
    pub head: Option<String>,
    #[arg(long = "changed")]
    pub changed: Vec<PathBuf>,
}
#[derive(Args, Default)]
pub struct Execution {
    #[arg(long)]
    pub jobs: Option<usize>,
    #[arg(long)]
    pub force: bool,
    #[arg(long = "env", value_parser = parse_env)]
    pub env: Vec<(String, String)>,
    #[arg(long)]
    pub no_dotenv: bool,
    #[arg(long)]
    pub show_secrets: bool,
    #[arg(long)]
    pub quiet: bool,
    /// Zero-based shard index/count, e.g. 0/4.
    #[arg(long)]
    pub shard: Option<String>,
}
#[derive(Subcommand)]
pub enum CiAction {
    Export {
        tasks: Vec<String>,
        #[arg(long)]
        output: PathBuf,
    },
    Prepare {
        #[arg(long)]
        blueprint: PathBuf,
        #[arg(long)]
        base: Option<String>,
        #[arg(long)]
        output: PathBuf,
    },
    Execute {
        #[arg(long)]
        blueprint: PathBuf,
        #[arg(long)]
        plan: PathBuf,
        #[arg(long)]
        unit: String,
        #[arg(long)]
        input: PathBuf,
        #[arg(long)]
        output: PathBuf,
    },
    Aggregate {
        #[arg(long)]
        blueprint: PathBuf,
        #[arg(long)]
        plan: PathBuf,
        #[arg(long)]
        input: PathBuf,
    },
}
fn parse_env(value: &str) -> std::result::Result<(String, String), String> {
    let (key, value) = value
        .split_once('=')
        .ok_or("environment override must be NAME=VALUE")?;
    if key.is_empty() || key.contains('\0') || value.contains('\0') {
        return Err("invalid environment override".into());
    }
    Ok((key.into(), value.into()))
}
impl Execution {
    fn options(&self, os: Option<config::Os>, arch: Option<config::Arch>) -> Result<RunOptions> {
        ensure!(self.jobs != Some(0), "jobs must be positive");
        let shard = self
            .shard
            .as_ref()
            .map(|v| -> Result<(usize, usize)> {
                let (index, total) = v.split_once('/').context("shard must be index/count")?;
                let (index, total) = (index.parse()?, total.parse()?);
                ensure!(total > 0 && index < total, "invalid shard index/count");
                Ok((index, total))
            })
            .transpose()?;
        Ok(RunOptions {
            jobs: self.jobs.unwrap_or_else(|| RunOptions::default().jobs),
            force: self.force,
            env: self.env.iter().cloned().collect(),
            no_dotenv: self.no_dotenv,
            show_secrets: self.show_secrets,
            quiet: self.quiet,
            shard,
            os,
            arch,
            ..RunOptions::default()
        })
    }
}
impl Selection {
    async fn plan(&self, graph: &Graph) -> Result<Plan> {
        ensure!(
            self.head.is_none() || self.base.is_some() || self.affected,
            "--head requires --base or --affected"
        );
        ensure!(
            self.head.is_none() || self.changed.is_empty(),
            "--head cannot be combined with --changed"
        );
        let affected = self.affected || self.base.is_some() || !self.changed.is_empty();
        ensure!(
            affected || !self.tasks.is_empty(),
            "specify a task or affected selection"
        );
        let mut changes = self.changed.clone();
        if affected && changes.is_empty() {
            changes = crate::plan::git_changes(
                &graph.workspace.root,
                self.base.as_deref().unwrap_or("HEAD"),
                self.head.as_deref(),
            )
            .await?;
        }
        Plan::create(graph, &self.tasks, &changes, affected)
    }
}

pub async fn run(cli: Cli, cancel: CancellationToken) -> Result<i32> {
    if matches!(cli.command, Action::Schema) {
        print(
            &serde_json::to_value(schemars::schema_for!(config::Config))?,
            cli.json,
        )?;
        return Ok(0);
    }
    if matches!(cli.command, Action::Result { .. }) {
        runner::report_unchanged()?;
        return Ok(0);
    }
    let root = match &cli.root {
        Some(root) => root.canonicalize()?,
        None => discover::locate_root(&std::env::current_dir()?)?,
    };
    if let Action::Ci { command } = &cli.command {
        let value = match command {
            CiAction::Export { tasks, output } => {
                ensure!(!tasks.is_empty(), "CI export requires selected task roots");
                let graph = Graph::build(
                    Workspace::discover(&root)
                        .await?
                        .select_platform(cli.os, cli.arch),
                )?;
                ci::export(&graph, tasks.clone(), output)?;
                json!({"version":1,"workflow":output,"blueprint":output.with_extension("taskflow.json")})
            }
            CiAction::Prepare {
                blueprint,
                base,
                output,
            } => {
                let blueprint = read_json(&root.join(blueprint))?;
                let plan = ci::prepare(&root, &blueprint, base.as_deref()).await?;
                files::atomic_write(&root.join(output), &serde_json::to_vec(&plan)?)?;
                serde_json::to_value(plan)?
            }
            CiAction::Execute {
                blueprint,
                plan,
                unit,
                input,
                output,
            } => serde_json::to_value(
                ci::execute(
                    &root,
                    &read_json(&root.join(blueprint))?,
                    read_json(&root.join(plan))?,
                    unit,
                    &root.join(input),
                    &root.join(output),
                    cancel,
                )
                .await?,
            )?,
            CiAction::Aggregate {
                blueprint,
                plan,
                input,
            } => ci::aggregate(
                &read_json(&root.join(blueprint))?,
                &read_json(&root.join(plan))?,
                &root.join(input),
            )?,
        };
        let success = value
            .get("success")
            .and_then(Value::as_bool)
            .unwrap_or(true);
        print(&value, cli.json)?;
        return Ok(if success { 0 } else { 1 });
    }
    if let Action::Cache { action } = cli.command {
        let directory = root.join(".taskflow/cache/entries");
        if matches!(action, CacheAction::Clean) {
            let _locks = runner::acquire_locks(&root, "cache-maintenance", &[], &cancel).await?;
            crate::cache::remove_path(&root.join(".taskflow/cache"))?;
            print(
                &json!({"version":1,"removed":"task-cache","preserved":"native caches and task outputs"}),
                cli.json,
            )?;
        } else {
            let mut entries = vec![];
            if directory.is_dir() {
                for entry in std::fs::read_dir(directory)? {
                    let entry = entry?;
                    let key = entry
                        .path()
                        .file_stem()
                        .unwrap()
                        .to_string_lossy()
                        .into_owned();
                    let valid = if matches!(action, CacheAction::Verify) {
                        Some(crate::cache::load(&root, &key).is_ok())
                    } else {
                        None
                    };
                    entries.push(json!({"key":key,"valid":valid}));
                }
            }
            entries.sort_by_key(|v| v["key"].as_str().unwrap().to_owned());
            let invalid = entries.iter().any(|v| v["valid"] == false);
            print(&json!({"version":1,"entries":entries}), cli.json)?;
            if invalid {
                return Ok(1);
            }
        }
        return Ok(0);
    }
    if let Action::Start { profile, execution } = cli.command {
        let result = crate::session::start(
            &root,
            &profile,
            execution.options(cli.os, cli.arch)?,
            cancel,
        )
        .await?;
        print(&serde_json::to_value(&result)?, cli.json)?;
        return Ok(if result.success { 0 } else { 1 });
    }
    let mut graph = Arc::new(Graph::build(
        Workspace::discover(&root)
            .await?
            .select_platform(cli.os, cli.arch),
    )?);
    let is_query = matches!(cli.command, Action::Query { .. });
    let value = match cli.command {
        Action::Check => {
            ensure!(
                graph.unresolved.is_empty(),
                "unresolved native dependency selectors: {:?}",
                graph.unresolved
            );
            json!({"version":1,"valid":true,"projects":graph.workspace.projects.len(),"tasks":graph.tasks.len(),"coverage":graph.workspace.coverage,"explanations":graph.explanations})
        }
        Action::Query {
            query,
            node,
            destination,
            projects,
            transitive,
        } => {
            let node_id = || node.as_deref().context("query requires a node or file");
            match query {
                Query::Projects => json!(graph.workspace.projects),
                Query::Tasks => json!(graph.tasks),
                Query::Artifacts => json!(graph.artifacts),
                Query::Owners => {
                    json!(graph.owners(&files::within(&root, &root.join(node_id()?))?))
                }
                Query::Inputs => {
                    json!(graph.inputs(&files::within(&root, &root.join(node_id()?))?)?)
                }
                Query::Path => json!(graph.path(
                    node_id()?,
                    destination
                        .as_deref()
                        .context("path query requires destination")?,
                    projects
                )),
                Query::Deps | Query::Rdeps => {
                    let id = node_id()?;
                    ensure!(
                        if projects {
                            graph.workspace.projects.contains_key(id)
                        } else {
                            graph.tasks.contains_key(id)
                        },
                        "unknown graph node"
                    );
                    let reverse = matches!(query, Query::Rdeps);
                    if transitive {
                        let mut result =
                            graph.closure(&BTreeSet::from([id.into()]), reverse, projects);
                        result.remove(id);
                        json!(result)
                    } else if projects {
                        json!(graph
                            .workspace
                            .edges
                            .iter()
                            .filter(|e| if reverse { e.to == id } else { e.from == id })
                            .collect::<Vec<_>>())
                    } else {
                        json!(if reverse {
                            graph.dependents(id)
                        } else {
                            graph.prerequisites(id)
                        })
                    }
                }
            }
        }
        Action::Plan(selection) => serde_json::to_value(selection.plan(&graph).await?)?,
        Action::Run {
            selection,
            execution,
        } => {
            let mut options = execution.options(cli.os, cli.arch)?;
            let mut plan = selection.plan(&graph).await?;
            if plan.order.iter().any(|id| graph.unresolved.contains(id)) {
                let installs: Vec<_> = plan
                    .order
                    .iter()
                    .filter(|id| graph.tasks[*id].task.install)
                    .cloned()
                    .collect();
                ensure!(
                    !installs.is_empty(),
                    "unresolved native metadata requires an explicit install: true prerequisite"
                );
                let bootstrap = Plan::create(&graph, &installs, &[], false)?;
                let result = runner::run_plan(
                    graph.clone(),
                    bootstrap,
                    options.clone(),
                    cancel.child_token(),
                )
                .await?;
                ensure!(result.success, "installation prerequisite failed");
                options.provided.extend(result.results);
                graph = Arc::new(Graph::build(
                    Workspace::discover(&root)
                        .await?
                        .select_platform(cli.os, cli.arch),
                )?);
                plan = selection.plan(&graph).await?;
            }
            let result = runner::run_plan(graph, plan, options, cancel).await?;
            let success = result.success;
            print(&serde_json::to_value(result)?, cli.json)?;
            return Ok(if success { 0 } else { 1 });
        }
        _ => unreachable!(),
    };
    let value = if is_query {
        redact_query(value, &graph)?
    } else {
        value
    };
    print(&value, cli.json)?;
    Ok(0)
}
fn redact_query(mut value: Value, graph: &Graph) -> Result<Value> {
    let mut secrets = vec![];
    for node in graph.tasks.values() {
        let environment = crate::environment::Environment::build(
            &graph.workspace,
            &graph.workspace.projects[&node.project],
            &node.task,
            &Default::default(),
            false,
        )?;
        secrets.extend(environment.secrets);
    }
    fn visit(value: &mut Value, secrets: &[Vec<u8>]) {
        match value {
            Value::String(text) => {
                *text = String::from_utf8(crate::environment::Redactor::mask(
                    text.as_bytes(),
                    secrets.to_vec(),
                ))
                .expect("masking preserves UTF-8")
            }
            Value::Array(values) => {
                for value in values {
                    visit(value, secrets);
                }
            }
            Value::Object(values) => {
                for value in values.values_mut() {
                    visit(value, secrets);
                }
            }
            _ => {}
        }
    }
    visit(&mut value, &secrets);
    Ok(value)
}
fn read_json<T: serde::de::DeserializeOwned>(path: &Path) -> Result<T> {
    Ok(serde_json::from_slice(&std::fs::read(path)?)?)
}
fn print(value: &Value, compact: bool) -> Result<()> {
    use std::io::Write;
    let mut stdout = std::io::stdout().lock();
    if compact {
        serde_json::to_writer(&mut stdout, value)?;
    } else {
        serde_json::to_writer_pretty(&mut stdout, value)?;
    }
    writeln!(stdout)?;
    Ok(())
}
