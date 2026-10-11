#[cfg(any(target_os = "linux", target_os = "macos"))]
mod compiler;
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod optimizer;
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod output;
#[cfg(any(target_os = "linux", target_os = "macos"))]
fn main() {
    app::main();
}
#[cfg(not(any(target_os = "linux", target_os = "macos")))]
fn main() {
    eprintln!("typeopt supports macOS and Linux only");
    std::process::exit(1);
}
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod app {
    use std::{
        collections::BTreeMap,
        path::PathBuf,
        sync::atomic::{AtomicBool, Ordering},
        time::Instant,
    };

    use anyhow::{Context, Result, ensure};
    use clap::Parser;
    use serde::Serialize;

    use super::{compiler, optimizer, output};

    pub(crate) static CANCELLED: AtomicBool = AtomicBool::new(false);
    pub(crate) static VERBOSE: AtomicBool = AtomicBool::new(false);
    pub(crate) fn check_cancelled() -> Result<()> {
        ensure!(!CANCELLED.load(Ordering::Relaxed), "operation cancelled");
        Ok(())
    }

    #[derive(Parser)]
    #[command(
        version,
        about = "Conservative TypeScript property optimization (experimental v0)"
    )]
    struct Args {
        #[arg(long)]
        project: PathBuf,
        #[arg(long, required = true, num_args = 1..)]
        entry: Vec<PathBuf>,
        #[arg(long, required_unless_present = "check", conflicts_with = "check")]
        out_dir: Option<PathBuf>,
        #[arg(long)]
        check: bool,
        #[arg(long)]
        verbose: bool,
    }

    #[derive(Serialize)]
    pub(crate) struct Report {
        schema_version: u8,
        tool_version: &'static str,
        compiler_revision: &'static str,
        files: usize,
        object_regions: usize,
        analysis_scope: &'static str,
        no_change_reason: Option<&'static str>,
        properties: usize,
        renamed_properties: usize,
        changed_occurrences: usize,
        preservation_reasons: BTreeMap<String, usize>,
        measurements: Measurements,
    }
    impl Report {
        pub(crate) fn measure(&mut self, started: Instant) {
            self.measurements = Measurements {
                elapsed_ms: started.elapsed().as_millis(),
                peak_rss_bytes: output::peak_rss(),
            };
        }
    }

    #[derive(Serialize)]
    struct Measurements {
        elapsed_ms: u128,
        peak_rss_bytes: u64,
    }

    pub(crate) fn main() {
        let args = Args::parse();
        VERBOSE.store(args.verbose, Ordering::Relaxed);
        tracing_subscriber::fmt()
            .with_writer(std::io::stderr)
            .with_ansi(std::env::var_os("NO_COLOR").is_none())
            .with_max_level(if args.verbose {
                tracing::Level::DEBUG
            } else {
                tracing::Level::INFO
            })
            .init();
        std::panic::set_hook(Box::new(|_| {
            // Panic payloads can contain input text; never print them.
            tracing::error!("internal compiler failure; no output will be published");
        }));
        let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| run(args)))
            .unwrap_or_else(|_| {
                Err(anyhow::anyhow!(
                    "internal compiler failure; report a redacted reproduction with the tool \
                     version"
                ))
            });
        if let Err(error) = result {
            tracing::error!(error = %error, "typeopt failed");
            std::process::exit(if CANCELLED.load(Ordering::Relaxed) {
                130
            } else {
                1
            });
        }
    }

    fn run(args: Args) -> Result<()> {
        let started = Instant::now();
        ctrlc::set_handler(|| CANCELLED.store(true, Ordering::Relaxed))?;
        if let Some(path) = &args.out_dir {
            ensure!(
                std::fs::symlink_metadata(path)
                    .is_err_and(|e| e.kind() == std::io::ErrorKind::NotFound),
                "output destination already exists or cannot be inspected"
            );
        }
        let project = if args.project.is_dir() {
            args.project.join("tsconfig.json")
        } else {
            args.project
        };
        let project = project
            .canonicalize()
            .context("cannot find project configuration")?;
        let entries: Vec<String> = args
            .entry
            .iter()
            .map(|path| {
                let path = path.canonicalize().context("cannot find an entry file")?;
                ensure!(path.is_file(), "entry must be a file");
                ensure!(
                    !path.components().any(|c| c.as_os_str() == "node_modules"),
                    "entry cannot be external node_modules code"
                );
                Ok(path.to_string_lossy().into_owned())
            })
            .collect::<Result<_>>()?;
        tracing::info!(entries = entries.len(), "loading TypeScript program");
        let compiler = compiler::Project::load(&project.to_string_lossy(), entries)?;
        let (mut objects, sources) = compiler.analyze()?;
        check_cancelled()?;
        let (mappings, edits) = optimizer::allocate(&mut objects);
        let renamed = mappings.iter().filter(|m| m.original != m.renamed).count();
        tracing::info!(
            files = sources.len(),
            objects = objects.len(),
            renamed,
            "analysis complete"
        );
        let mut reasons = BTreeMap::new();
        for m in &mappings {
            if let Some(reason) = &m.preservation {
                *reasons
                    .entry(serde_json::to_value(reason)?.as_str().unwrap().to_owned())
                    .or_default() += 1;
            }
        }
        let mut report = Report {
            schema_version: 0,
            tool_version: env!("CARGO_PKG_VERSION"),
            compiler_revision: "d62b17c3d608e4c61c763e7ec18574f689cbb3d0",
            files: sources.len(),
            object_regions: objects.len(),
            analysis_scope: "closed_local_object_regions",
            no_change_reason: (renamed == 0).then_some(
                "No profitable property rename was proven safe; unsupported or observable shapes \
                 are preserved.",
            ),
            properties: mappings.len(),
            renamed_properties: renamed,
            changed_occurrences: edits.values().map(Vec::len).sum(),
            preservation_reasons: reasons,
            measurements: Measurements {
                elapsed_ms: 0,
                peak_rss_bytes: 0,
            },
        };
        if let Some(destination) = args.out_dir {
            let mut changed = sources.clone();
            for (name, file_edits) in &edits {
                changed.insert(name.clone(), output::apply(&sources[name], file_edits)?);
            }
            let emitted = compiler.emit(changed)?;
            check_cancelled()?;
            output::publish(
                &destination,
                emitted,
                &sources,
                &edits,
                &mappings,
                &mut report,
                started,
            )?;
            tracing::info!(renamed, "output published");
        } else {
            report.measure(started);
            println!("{}", serde_json::to_string_pretty(&report)?);
        }
        Ok(())
    }
}
