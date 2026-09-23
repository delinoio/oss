# Benchmarks

**pnport 0.1.0 is unreleased, and no benchmark results are published yet.** Performance numbers will accompany release acceptance after the complete six-target conformance work. This page specifies how to make future results reproducible; it sets no numerical performance promise.

## Reproduction protocol

1. Record the exact pnport version, operating system and architecture, Yarn 4 version, fixture lockfile, command, and host hardware.
2. Prepare one installed Yarn 4 PnP fixture with fixed dependency bytes. Keep project inputs and child command identical between measurements; prepare dependencies before timed execution.
3. Measure a cold run with a fresh, empty pnport cache directory. Measure a warm run using the completed cache from that same fixture. Repeat each condition at least five times and report the median and range rather than a single best result.
4. Record elapsed wall time from pnport start to exit, filesystem operation counts and bytes where the host can observe them, peak process-tree memory, and cache/disk bytes after each run. State the measurement tool and whether the sample includes package materialization or only a reused entry.
5. Publish the fixture and exact commands with results, and state which host/target actually ran them. Do not infer Windows or Linux performance from a macOS run.

Use `--cache-dir` to isolate benchmark runs and [cache commands](/pnport/cache) to inspect retained entries. Keep the child's own output separate from pnport diagnostics. Release results must include cold and warm behavior; no editor or arbitrary-tool certification follows from one benchmark.
