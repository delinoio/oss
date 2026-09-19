# Reproducible benchmarks

Measure on a quiet machine using the same Runlens version, native tool versions, OS, architecture, filesystem, and source revision. Publish those metadata, the fixture-generation commands, repetitions, measurement procedure, and individual results. Do not publish source contents or secrets from real projects.

1. Create a disposable repository containing 1,000, 10,000, and 100,000 small files, plus an ignored output directory. Record file counts and byte totals.
2. Measure a finite command directly, then through `runlens run -- <argv>`. Use identical input state and at least five measured repetitions after a separately documented warm-up.
3. Measure scans with an empty command, then commands that read many files, overwrite existing files, create output sets, and inspect missing paths.
4. Measure large access sets with default limits and a lowered memory threshold. Record elapsed time, peak resident memory, temporary storage, access count, snapshot count, and whether spill occurred.
5. Lower the collection byte/path limits deliberately and confirm an incomplete classification and nonzero verification outcome. Distinguish collection overhead from build/checkout resource use.
6. Measure `verify repeat` separately: its cost includes fresh checkout creation and explicit preparation on each run, so it is not a warm-cache build benchmark.

Use OS-native timing/resource tools and publish exact invocations. Keep saved benchmark metadata outside the measured worktree unless intentionally testing that case. No numerical performance or support SLA is claimed.
