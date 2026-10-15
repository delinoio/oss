#[cfg(any(target_os = "linux", target_os = "macos"))]
mod bundle;
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod compiler;
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod minify;
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod optimizer;
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod output;
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod passes;
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod pipeline;
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod plan;
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod report;
#[cfg(any(target_os = "linux", target_os = "macos"))]
fn main() {
    // tsrs recursively checks complex types on a 512 MiB stack, as its native
    // CLI does.
    std::thread::Builder::new()
        .name("typeopt-compiler".into())
        .stack_size(512 << 20)
        .spawn(app::main)
        .expect("cannot start compiler thread")
        .join()
        .expect("compiler thread failed");
}
#[cfg(not(any(target_os = "linux", target_os = "macos")))]
fn main() {
    eprintln!("typeopt supports macOS and Linux only");
    std::process::exit(1);
}
#[cfg(any(target_os = "linux", target_os = "macos"))]
mod app {
    use std::{
        path::PathBuf,
        sync::atomic::{AtomicBool, Ordering},
        time::Instant,
    };

    use anyhow::{Context, Result, ensure};
    use clap::Parser;

    use super::{
        bundle, compiler, output,
        passes::{PassId, PassSet},
        pipeline, report,
    };

    /// Upstream compiler revision recorded in reports. It must match the
    /// `tsrs` pin in Cargo.toml; a unit test enforces this.
    pub(crate) const COMPILER_REVISION: &str = "abddaae32e5d4c17806095bd3874233fa29b26bf";

    pub(crate) static CANCELLED: AtomicBool = AtomicBool::new(false);
    pub(crate) static VERBOSE: AtomicBool = AtomicBool::new(false);
    /// Stage 2: the program is a closed set of bundler output chunks.
    static BUNDLE: AtomicBool = AtomicBool::new(false);
    pub(crate) fn bundle_mode() -> bool {
        BUNDLE.load(Ordering::Relaxed)
    }
    static KEEP_CHUNK_NAMES: AtomicBool = AtomicBool::new(false);
    pub(crate) fn keep_chunk_names() -> bool {
        KEEP_CHUNK_NAMES.load(Ordering::Relaxed)
    }
    static MINIFY: AtomicBool = AtomicBool::new(false);
    /// Bundle mode: minify edited chunks with SWC when that compresses smaller.
    pub(crate) fn minify() -> bool {
        MINIFY.load(Ordering::Relaxed)
    }
    /// Bundle mode: executable inline HTML code can observe callable source.
    static BUNDLE_HTML_REFLECTION: AtomicBool = AtomicBool::new(false);
    pub(crate) fn set_bundle_html_reflection(observed: bool) {
        BUNDLE_HTML_REFLECTION.store(observed, Ordering::Relaxed);
    }
    pub(crate) fn bundle_html_reflection() -> bool {
        BUNDLE_HTML_REFLECTION.load(Ordering::Relaxed)
    }
    /// Bundle chunks (path substrings) kept as unchecked boundaries.
    static BUNDLE_EXCLUDE: std::sync::OnceLock<Vec<String>> = std::sync::OnceLock::new();
    /// Bundle chunks known to run as ES modules (imported by a chunk or
    /// loaded by an HTML module script).
    static BUNDLE_MODULES: std::sync::OnceLock<std::collections::BTreeSet<String>> =
        std::sync::OnceLock::new();
    /// Bundle chunks that an HTML page loads as a classic script.
    static BUNDLE_CLASSIC: std::sync::OnceLock<std::collections::BTreeSet<String>> =
        std::sync::OnceLock::new();
    /// Bundle chunks with dynamic package imports that cannot be resolved in
    /// the closed bundle and therefore stay unchanged.
    static BUNDLE_DYNAMIC_PACKAGES: std::sync::OnceLock<std::collections::BTreeSet<String>> =
        std::sync::OnceLock::new();
    pub(crate) fn set_bundle_dynamic_packages(chunks: std::collections::BTreeSet<String>) {
        let _ = BUNDLE_DYNAMIC_PACKAGES.set(chunks);
    }
    pub(crate) fn bundle_dynamic_package(path: &str) -> bool {
        BUNDLE_DYNAMIC_PACKAGES
            .get()
            .is_some_and(|chunks| chunks.contains(path))
    }
    /// Source files reached only through dynamic package imports. Their
    /// declarations may be supplied by the package and are outside the
    /// bundle's checked application source.
    static BUNDLE_DYNAMIC_FILES: std::sync::OnceLock<std::collections::BTreeSet<String>> =
        std::sync::OnceLock::new();
    pub(crate) fn set_bundle_dynamic_files(files: std::collections::BTreeSet<String>) {
        let _ = BUNDLE_DYNAMIC_FILES.set(files);
    }
    pub(crate) fn bundle_dynamic_file(path: &str) -> bool {
        BUNDLE_DYNAMIC_FILES
            .get()
            .is_some_and(|files| files.contains(path))
    }
    /// Bundle chunks that an operator-excluded chunk can import, each with the
    /// excluded chunk that reaches it. They stay unchanged and keep their
    /// names: the excluded chunk keeps importing the original names.
    static BUNDLE_FROZEN: std::sync::OnceLock<std::collections::BTreeMap<String, String>> =
        std::sync::OnceLock::new();
    pub(crate) fn set_bundle_frozen(frozen: std::collections::BTreeMap<String, String>) {
        let _ = BUNDLE_FROZEN.set(frozen);
    }
    /// Chunks frozen because an excluded chunk can import them, with the
    /// causing excluded chunk, in path order.
    pub(crate) fn bundle_frozen() -> impl Iterator<Item = (&'static String, &'static String)> {
        BUNDLE_FROZEN.get().into_iter().flatten()
    }
    pub(crate) fn bundle_frozen_chunk(path: &str) -> bool {
        BUNDLE_FROZEN
            .get()
            .is_some_and(|frozen| frozen.contains_key(path))
    }
    pub(crate) fn set_bundle_modules(
        modules: std::collections::BTreeSet<String>,
        classic: std::collections::BTreeSet<String>,
    ) {
        let _ = BUNDLE_MODULES.set(modules);
        let _ = BUNDLE_CLASSIC.set(classic);
    }
    /// Whether `--bundle-exclude` matches a bundle chunk.
    pub(crate) fn bundle_excluded(path: &str) -> bool {
        BUNDLE_EXCLUDE
            .get()
            .is_some_and(|patterns| patterns.iter().any(|p| path.contains(p.as_str())))
    }
    /// Whether a bundle chunk stays byte-for-byte unchanged under its original
    /// name: excluded by the operator, reached by an excluded chunk, or
    /// containing a dynamic package import whose specifier must be preserved.
    pub(crate) fn bundle_pinned(path: &str) -> bool {
        bundle_excluded(path)
            || bundle_dynamic_package(path)
            || BUNDLE_FROZEN
                .get()
                .is_some_and(|frozen| frozen.contains_key(path))
    }
    /// Whether a bundle chunk is an unchecked boundary: excluded by the
    /// operator, or not known to be an ES module (a classic script shares
    /// its top-level bindings with other scripts). `.mjs` files, chunks with
    /// ESM statements and chunks that a chunk or an HTML module script loads
    /// are ES modules. A chunk that an excluded chunk can import is a boundary
    /// as well: the excluded chunk keeps importing its original name.
    pub(crate) fn bundle_boundary(path: &str, esm_statements: bool) -> bool {
        bundle_dynamic_package(path)
            || bundle_frozen_chunk(path)
            || bundle_excluded_or_classic(path, esm_statements)
    }
    /// The operator exclusion and classic-script decisions, without reach
    /// propagation. Logging checks these before a chunk's own syntax rule.
    pub(crate) fn bundle_excluded_or_classic(path: &str, esm_statements: bool) -> bool {
        bundle_excluded(path)
            // A chunk that any page loads as a classic script shares its
            // top-level bindings as globals there, even if it is also
            // imported as a module.
            || BUNDLE_CLASSIC.get().is_some_and(|c| c.contains(path))
            || !(esm_statements
                || path.ends_with(".mjs")
                || BUNDLE_MODULES.get().is_some_and(|m| m.contains(path)))
    }
    pub(crate) fn check_cancelled() -> Result<()> {
        ensure!(!CANCELLED.load(Ordering::Relaxed), "operation cancelled");
        Ok(())
    }

    #[derive(Parser)]
    #[command(
        version,
        about = "Conservative two-stage type-aware size optimization (experimental)"
    )]
    struct Args {
        #[arg(long, required_unless_present = "bundle", conflicts_with = "bundle")]
        project: Option<PathBuf>,
        #[arg(long, required_unless_present = "bundle", conflicts_with = "bundle", num_args = 1..)]
        entry: Vec<PathBuf>,
        /// Stage 2: optimize the JavaScript chunks of a bundler output
        /// directory as one closed program.
        #[arg(long, value_name = "DIR")]
        bundle: Option<PathBuf>,
        /// Bundle mode: keep chunks whose path contains this text unchanged
        /// as unchecked boundaries (for example very large vendor chunks);
        /// repeat to exclude several.
        #[arg(long = "bundle-exclude", value_name = "TEXT", requires = "bundle", action = clap::ArgAction::Append)]
        bundle_exclude: Vec<String>,
        /// Bundle mode: keep the names of edited content-hashed chunks.
        #[arg(long, requires = "bundle")]
        keep_chunk_names: bool,
        /// Bundle mode: minify every edited chunk with SWC and keep the result
        /// when its Brotli size shrinks and its gzip size does not grow.
        #[arg(long, requires = "bundle")]
        minify: bool,
        #[arg(long, required_unless_present = "check", conflicts_with = "check")]
        out_dir: Option<PathBuf>,
        #[arg(long)]
        check: bool,
        #[arg(long)]
        verbose: bool,
        /// Exclude one optimization pass; repeat to exclude several.
        #[arg(long = "disable-pass", value_enum, value_name = "ID", action = clap::ArgAction::Append)]
        disable_pass: Vec<PassId>,
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
        std::panic::set_hook(Box::new(|info| {
            // Panic payloads can contain input text; never print them.
            tracing::error!(location = %info.location().map(|l| l.to_string()).unwrap_or_default(), "internal compiler failure; no output will be published");
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
        tracing::debug!(
            compiler = "tsrs",
            single_threaded = true,
            "compiler memory regions configured"
        );
        if let Some(path) = &args.out_dir {
            ensure!(
                std::fs::symlink_metadata(path)
                    .is_err_and(|e| e.kind() == std::io::ErrorKind::NotFound),
                "output destination already exists or cannot be inspected"
            );
        }
        if let Some(dir) = args.bundle.clone() {
            return run_bundle(args, &dir, started);
        }
        let project = args.project.clone().context("--project is required")?;
        let project = if project.is_dir() {
            project.join("tsconfig.json")
        } else {
            project
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
        let passes = PassSet::without(&args.disable_pass);
        tracing::info!(
            entries = entries.len(),
            disabled = ?passes.disabled().iter().map(|p| p.as_str()).collect::<Vec<_>>(),
            "loading TypeScript program"
        );
        let compiler = compiler::Project::load(&project.to_string_lossy(), entries)?;
        let snapshot = compiler.snapshot()?;
        check_cancelled()?;
        // Check and output modes share every analysis, transformation and
        // size decision; only publication differs.
        let outcome = {
            let mut backend = compiler::Backend::new(&compiler, &snapshot);
            pipeline::Pipeline {
                backend: &mut backend,
                enabled: &passes,
            }
            .run(&snapshot.sources)?
        };
        let mut report = report::build(&snapshot, &outcome, &passes);
        tracing::info!(
            files = snapshot.sources.len(),
            bundles = outcome.bundles.len(),
            rounds = outcome.rounds,
            bytes_before = outcome.size_before,
            bytes_after = outcome.size_after,
            renamed = report.renamed_properties,
            "analysis complete"
        );
        if let Some(destination) = args.out_dir {
            let changed = outcome
                .texts
                .iter()
                .filter(|(name, text)| snapshot.sources.get(*name) != Some(*text))
                .map(|(name, text)| (name.clone(), text.clone()))
                .collect();
            let emitted = compiler.emit(&changed)?;
            check_cancelled()?;
            output::publish(
                &destination,
                emitted,
                &snapshot,
                &outcome,
                &mut report,
                started,
            )?;
            tracing::info!(bundles = outcome.bundles.len(), "output published");
        } else {
            report.measure(started);
            println!("{}", serde_json::to_string_pretty(&report)?);
        }
        Ok(())
    }

    fn run_bundle(args: Args, dir: &std::path::Path, started: Instant) -> Result<()> {
        BUNDLE.store(true, Ordering::Relaxed);
        KEEP_CHUNK_NAMES.store(args.keep_chunk_names, Ordering::Relaxed);
        MINIFY.store(args.minify, Ordering::Relaxed);
        let _ = BUNDLE_EXCLUDE.set(args.bundle_exclude.clone());
        let dir = dir.canonicalize().context("cannot find bundle directory")?;
        ensure!(dir.is_dir(), "--bundle must be a directory");
        if let Some(destination) = &args.out_dir {
            let parent = destination
                .parent()
                .filter(|p| !p.as_os_str().is_empty())
                .unwrap_or(std::path::Path::new("."))
                .canonicalize()
                .context("cannot find the output parent directory")?;
            ensure!(
                !parent.starts_with(&dir),
                "the output directory must be outside the bundle directory"
            );
        }
        let passes = PassSet::bundle_without(&args.disable_pass);
        tracing::info!(
            enabled = ?passes.enabled().iter().map(|p| p.as_str()).collect::<Vec<_>>(),
            "bundle mode passes"
        );
        bundle::preflight(&dir)?;
        // Publication verifies that it copies the directory as analyzed.
        let analyzed = bundle::fingerprint(&dir)?;
        let compiler = compiler::Project::load_bundle(&dir)?;
        let snapshot = compiler.snapshot()?;
        check_cancelled()?;
        let outcome = {
            let mut backend = compiler::Backend::new(&compiler, &snapshot);
            pipeline::Pipeline {
                backend: &mut backend,
                enabled: &passes,
            }
            .run(&snapshot.sources)?
        };
        let mut report = report::build(&snapshot, &outcome, &passes);
        tracing::info!(
            files = snapshot.sources.len(),
            bundles = outcome.bundles.len(),
            rounds = outcome.rounds,
            bytes_before = outcome.size_before,
            bytes_after = outcome.size_after,
            renamed = report.renamed_properties,
            "bundle analysis complete"
        );
        // Check mode stages into a temporary directory as well, so it reports
        // the same size and publication blockers as output mode.
        let boundaries = compiler.bundle_boundaries();
        // Only pinned chunks stay byte-for-byte unchanged in publication;
        // other boundary chunks take part in reference rewriting.
        let pinned: std::collections::BTreeSet<String> = boundaries
            .into_iter()
            .filter(|chunk| bundle_pinned(chunk))
            .collect();
        let scratch = tempfile::tempdir().context("cannot create a check staging directory")?;
        let destination = args
            .out_dir
            .clone()
            .unwrap_or_else(|| scratch.path().join("out"));
        let staged = bundle::stage(&dir, &destination, &snapshot, &outcome, &analyzed, &pinned)?;
        // Original chunks kept for encoded or boundary references are
        // published next to the optimized graph; the report discloses them.
        report.size.retained = staged.retained_bytes;
        // Minification happens at publication; the report describes the
        // published chunk text.
        report.size.minified = staged.minified_bytes;
        report.size.after -= staged.minified_bytes;
        report.size.saved += staged.minified_bytes as isize;
        let staged = args.out_dir.is_some().then_some(staged);
        report.measure(started);
        // The report is part of the result: publish only after it was
        // delivered, so a closed or full standard output leaves no output.
        {
            use std::io::Write as _;
            let mut stdout = std::io::stdout().lock();
            writeln!(stdout, "{}", serde_json::to_string_pretty(&report)?)
                .and_then(|()| stdout.flush())
                .context("cannot write the report to standard output")?;
        }
        check_cancelled()?;
        if let Some(staged) = staged {
            staged.commit()?;
            tracing::info!(bundles = outcome.bundles.len(), "bundle output published");
        }
        Ok(())
    }

    #[cfg(test)]
    mod tests {
        #[test]
        fn compiler_revision_matches_cargo_pin() {
            let manifest = include_str!("../Cargo.toml");
            let pin = format!("rev = \"{}\"", super::COMPILER_REVISION);
            let dependencies: Vec<_> = manifest
                .lines()
                .filter(|line| line.starts_with("tsrs_"))
                .collect();
            assert!(!dependencies.is_empty(), "tsrs dependencies must be pinned");
            for dependency in dependencies {
                assert!(
                    dependency.contains(&pin),
                    "report compiler_revision must match every tsrs dependency: {dependency}"
                );
            }
        }
    }
}
