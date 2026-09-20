# TaskFlow conformance and validation

## Scope
This document maps issue #898 scenarios to executable evidence in `crates/taskflow/tests/conformance.rs`. The approved v1 policy is in `docs/crates-taskflow-foundation.md`; scenario numbers identify acceptance coverage, not a claim of universal native-tool compatibility.

## Runtime and Language
Rust tests run the library and real CLI with isolated temporary workspaces. A standalone Rust fixture executable provides deterministic finite commands, output, TCP services, timed exclusive listeners, process trees, and generic test inventories. External suites use actual pnpm/Cargo/Go metadata, Vitest/Jest, Docker, and MinIO. Fixture credentials are generated names with non-production literal values.

## Users and Operators
Maintainers run the default suite without Docker, pnpm, or Go. Native and container suites are opt-in locally and explicitly enabled by their owning CI jobs. Operators are responsible for installed tools, accessible registries, and the local Docker daemon.

## Interfaces and Contracts
| Issue scenario | Executable evidence |
| --- | --- |
| 1 | `scenario_01_installation_prerequisite_precedes_build_and_deduplicates` |
| 2 | `scenarios_02_20_cache_hit_input_change_and_deleted_output_restoration` checks a deleted required output with unchanged metadata. |
| 3–5 | `scenarios_03_04_05_18_unchanged_preserves_independent_causes` covers chained, direct, own-input, and multiple prerequisite causes after establishing successful baselines. `outputless_suppression_requires_the_latest_attempt_to_succeed` covers missing, failed, and invalidated baselines. |
| 6–7 | `scenario_06_streaming_secret_masking_handles_all_boundaries`, `scenarios_06_07_dotenv_precedence_disable_and_persisted_masking`, `malformed_dotenv_diagnostics_never_expose_values`, and CLI query masking. |
| 8 | `scenarios_08_15_16_19_watch_and_timer_companions_preserve_server` uses a real TCP server. |
| 9 | Generic and Go/libtest/Vitest/Jest suites compared with unsharded commands, deterministic assignment/aggregate rejection, `cached_shards_retain_complete_accounting_evidence`, and `scenario_09_ci_shards_reject_partial_success_and_gate_secret_mapping`. |
| 10 | `scenario_10_local_docker_execution_uses_explicit_platform_and_cleans_container` also reports unchanged through the injected helper in an image without TaskFlow, propagating newly created outputs and suppressing an identical second result. |
| 11 | `scenarios_11_22_26_ci_units_transfer_artifacts_and_preserve_causes` executes separate fresh directories and validates generated YAML with actionlint. |
| 12–14 | `scenarios_12_13_14_21_23_24_native_workspaces_aliases_and_conditions` covers native membership/exclusion, Cargo virtual root, mixed workspace, aliases, renames, kinds, config-less projects, duplicate IDs, and task-less neighbors; explicit missing/cycles are checked by `scenario_26_queries_cycles_missing_references_and_output_ownership`. |
| 15 | Watch/server fixture and `scenarios_15_19_21_live_invalid_configuration_recovers_atomically`. |
| 16 | Real interval companion, explicit-time interval/weekday/day-union tests (including stepped star fields), and cron/DST tests in `scenarios_09_16_shard_accounting_and_cron_dst`. |
| 17 | `scenario_17_queue_skip_restart_own_real_exclusive_processes` and `scenario_17_cancellation_reaps_process_tree_and_never_caches`. |
| 18 | Independent cause tests and watched/scheduled companions with unchanged prerequisites. |
| 19 | Shared prerequisite/companion first-run deduplication, subscription shutdown, and `server_readiness_failure_reaps_concurrent_work_before_returning`. |
| 20 | Native workspace selection plus first-run/cache/input-change/deleted-output restoration. |
| 21 | Native configuration generation changes and live invalid-config recovery; removed manifest names conservatively select work. |
| 22 | `scenario_22_real_s3_cache_restores_on_clean_runner_and_handles_denied_credentials`, local archive corruption/traversal rejection, and fresh CI artifact transfer. |
| 23 | Direct Cargo execution compared with TaskFlow and absence of invented native compilation prerequisites. |
| 24 | Native reverse closure, explicit prerequisite deduplication, and concurrent exclusive-process fixtures. |
| 25 | `scenario_25_external_effect_survives_unchanged_prerequisite`. |
| 26 | Query, cycle, path, input/owner, and clean distributed CI fixtures. |

`check_rejects_windows_rooted_outputs_on_every_host` rejects drive, UNC, rooted, and drive-relative output paths before checking or CI export on every host.

`check_rejects_nul_in_complete_input_and_output_patterns` rejects NUL before and after wildcard components, including negative input globs. Configuration loading, `check`, and `run` fail before either the installation prerequisite or selected command writes its marker.

`reserved_output_aliases_are_rejected_on_every_host` rejects ASCII case aliases of `.git`, `.taskflow`, and `.taskflow-restore-*` before task execution and during artifact integrity validation, including nested components. Similar ordinary names remain valid. The portable rule prevents a case-sensitive cache producer from authorizing internal-state paths on a case-insensitive consumer.

`non_unicode_inherited_environment_is_rejected_without_panicking` launches the real CLI with invalid Unix environment names and values for check, query, plan, and run. Each returns an ordinary error without exposing entry bytes or executing the task. Native discovery, metadata capture, and task environment construction share fallible inherited-environment decoding.

`queued_service_exit_prevents_reusing_ready_receipts` starts a real service, verifies its ready receipt can support input/schedule work while alive, releases it to exit, and waits for its owner to queue completion. The next due wave must reject the old ready receipt and terminate the session even when the service exited successfully. Session scheduling drains exits before processing triggers and after finite output validation.

`partial_output_snapshots_require_matches_and_ignore_neighbors` rejects a JavaScript output glob when only neighboring JSON inputs exist, verifies those neighbors cannot alter output identity or override an unchanged report, and requires each declared pattern to match independently. Unowned links are not captured. Complete empty directory trees remain valid; partial roots retain live-filesystem link validation and cannot be restored as artifacts.

`cache_verify_rejects_misdirected_and_inconsistent_artifacts` also rejects validly encoded file records that declare descendants beneath another regular file, without restoring any output.

`cache_rejects_noncanonical_record_paths_before_restoration` verifies artifact integrity and restoration reject dot segments, repeated/trailing separators, backslashes, NUL, and drive-relative names, both alone and alongside a record for the same destination. Existing outputs remain untouched; canonical duplicate records are also rejected using normalized path identity.

`input_snapshots_only_walk_possible_project_and_pattern_roots` places an invalid Unix identity in an unrelated subtree to detect unintended traversal without timing thresholds. Empty, disabled, and negative-only inputs retain only metadata; automatic and explicit inputs scan their possible roots, including sibling references and missing prefixes, without traversing directory links. Explicitly selecting the invalid subtree still fails.

`uncached_output_digests_are_not_limited_by_artifact_size` executes a task with an output larger than 512 MiB, confirms successful local identity tracking, and retains the artifact capture bound. Local and captured identities share the `output-state-v2` digest domain; previous payload-based digests become safe cache misses.

`one_task_rejects_output_aliases_before_capture` rejects case/Unicode aliases within one task, including existing roots and nested aliases, while retaining lexical root deduplication.

`output_ownership_uses_destination_filesystem_aliases` probes actual case/Unicode equivalence and rejects overlapping clean output roots, including nested projects, before any output is created.

`docker_forwards_cli_overrides_to_tasks_tools_and_shards` observes Docker argv and resolved values through a local CLI fixture for finite tasks, tool probes, inventory, and shard execution, excluding ambient values and sibling credentials. Repeated declarations emit each effective name once; mixed-case task/input/CLI names collapse to the effective spelling on Windows while Unix retains the distinct names and values.

`directory_notifications_rescan_descendant_inputs` covers directory-only notifications and real rename, move-in, and removal of a tree whose files match `src/*.rs`. Exact filtered snapshots suppress irrelevant and self-output changes.

`invalid_local_artifacts_fall_back_to_valid_remote_entries` serves a valid object over loopback HTTP while the local entry has a valid envelope and corrupt file digest; the runner restores remotely and repairs its local entry without executing the task.

`readiness_cancellation_and_deadlines_await_probe_owners` owns real readiness parents and children through operator cancellation, readiness timeout, and service timeout. Unix probes ignore graceful termination to prove the session waits for forced cleanup and stream completion.

`pending_cancellation_uses_operator_exit_code`, `check_rejects_nul_in_every_shell_argument`, and `docker_host_environment_preserves_resolved_precedence` cover pre-start/pending cancellation, malformed shell prefixes, and isolated inherited/task/CLI environment precedence. Scenario 25 also exercises session input and schedule propagation after unchanged prerequisites.

The library regression `invalidation_at_each_publication_boundary_preserves_previous_entry` checks cancellation and failed input validation before object publication, before entry replacement, and after replacement under the exclusive cache lock, with and without a previous entry.

`cli_metadata_cancellation_returns_130_without_fallback` sends SIGINT to the real CLI while its Cargo metadata child is running, checks code 130 and child reaping, and proves no membership fallback starts. The fixture publishes its PID barrier after recording the metadata call so cancellation cannot race that evidence.

`non_utf8_paths_never_collapse_into_cache_identities` creates two Unix filenames with distinct invalid bytes, invalid output/root names, and a valid Japanese filename; identities fail explicitly instead of collapsing distinct paths.

`outputless_cached_prerequisites_preserve_semantic_result_identity` covers cached checks and shards, input changes, historical cache restoration, downstream cache invalidation, unchanged suppression, and rejection of legacy empty-snapshot identities. Separate shard selections retain distinct cache keys and the same tested-input result identity.

Additional invariants include unknown/duplicate configuration rejection, schema freshness, stale/partial CI receipt rejection, required artifact accounting, cache path traversal rejection before mutation, masked stored logs, and service failure cancelling other running checks before returning.

`grouped_tasks_only_receive_their_declared_secrets` passes a CI-style credential union to a real CLI run: only the declaring task receives the credential, and persisted output remains masked.

`check_rejects_invalid_readiness_before_starting_processes` rejects empty readiness commands, malformed readiness timeouts, invalid TCP host/port addresses, and malformed or non-HTTP(S) readiness URLs at configuration validation, before service startup.

`unchanged_prerequisites_cannot_suppress_corrupt_outputs` checks a three-task chain with both cached and uncached middle tasks: existing but modified outputs must be restored or rebuilt before a directly requested consumer can read them. Suppression requires the current output digest to match a previous successful receipt.

`remote_lookup_input_change_preserves_current_outputs` changes an input at a loopback HTTP response barrier during object lookup. Restoration must recheck freshness before replacing any output, retain the post-restore race check, and reject the stale entry without publishing it locally.

`cache_clean_serializes_with_readers_and_writers` blocks a separate CLI cleaner behind a read lock, then races local stores, reads, and cleaning. The lock survives removal of the cache tree and covers the complete object/entry publication transaction.

`cache_rejects_links_through_external_ancestors_before_mutation` covers external directory links, parent components following links, archived link chains, cycles, and a valid internal chain. Validation follows the effective target before changing any existing output.

`cargo_target_selectors_follow_the_selected_platform` uses a real offline Cargo workspace to verify conditional, renamed prerequisites for all six platform selections and explicit compilation-target precedence.

`shard_preflight_preserves_explicit_metadata_bootstrap` validates the complete shard request before an explicitly declared installation creates missing Cargo metadata; installation itself runs unsharded before the selected test partition.

`reading_session_files_does_not_cancel_or_requeue_work` verifies that metadata/input reads cannot cancel or repeat a live task. Its initial-disabled watcher starts with an absent input, then creates that input after an activation barrier establishes the baseline. This separates the read-only exercise from delayed macOS creation events originating before subscription. Linux access notifications from discovery and hashing must not be treated as mutations.

`session_normalizes_watch_paths_for_existing_and_deleted_inputs` exercises a lexical root alias, deletion, and recreation. Notification paths use the same canonical root as discovery, including Windows verbatim prefixes; removed leaves are normalized through their existing ancestors.

The native JS sharding fixture executes files under a directory containing spaces, compares recorded file executions against the unsharded run, and prints masked task logs on failure. Validated file selectors are project-relative so Windows canonical path prefixes do not become JS filename filters.

Session fixtures await observable PID/record markers with a bounded 60-second startup allowance for concurrent native builds. Readiness failure permits 15 seconds for both children to start, with five additional seconds for cleanup. Ordinary shutdown tests retain their five-second bound; failures include structured engine logs and observed records instead of relying on fixed short startup sleeps.

## Storage
Fixtures use temporary directories and containers with UUID-v7 names. They do not replace repository credentials or identity material. Generated `dist` and `.taskflow` content is ignored and not committed.

## Security
Real remote transport tests use loopback MinIO and fixture-only credentials. The CI workflow consumes no repository secrets and never publishes. Native queries/installations are explicit in fixtures; untrusted PR execution disables remote access and secret injection.

The MinIO fixture waits for `/minio/health/cluster` before configuring its alias or bucket. In the [pinned MinIO release's health handlers](https://github.com/minio/minio/blob/RELEASE.2025-09-07T16-13-09Z/cmd/healthcheck-handler.go), liveness and readiness can return HTTP 200 before initialization; the cluster handler requires storage, bucket metadata, IAM, and write quorum. The fixture uses a 60-second absolute deadline and bounded individual requests, with explicit status/attempt/timing diagnostics on failure. `minio_readiness_waits_for_initialized_storage_and_iam`, `minio_readiness_reports_unavailable_cluster_before_setup`, and `minio_readiness_deadline_cancels_stalled_health_requests` exercise delayed readiness, persistent unavailability, and stalled HTTP responses through real loopback listeners. Mock listeners are cancelled and awaited before assertions; the real container retains its cleanup owner on every exit.

## Logging
Test assertions include typed receipts and native fixture diagnostics on failure. Production logs remain subject to the engine's designation mask and stable context rules.

`combined_log_keeps_masking_across_pipe_eof` splits a multiline Unicode secret at every byte boundary across sequential pipe lifetimes. `combined_shard_logs_mask_secrets_before_storage_and_display` splits a secret across stdout and stderr of separate real shard processes and verifies masked stored/normal terminal output, including continued storage masking with `--show-secrets`. The combined mask is flushed only after every task stream and shard finishes.

`affected_gitlinks_select_descendant_inputs_without_a_checkout` uses real Git raw gitlink additions, updates, and deletions without a submodule checkout. Library and CLI plans select descendant inputs, retain input causes, exclude owned outputs, and keep ordinary deleted-file matching exact. Explicit existing-directory changes use the same conservative input-root intersection.

`docker_context_cannot_override_a_validated_local_host` rejects both a TCP context and a remote Windows named-pipe context before execution state exists. `only_local_socket_addresses_are_accepted` also checks local Unix/Windows forms and malformed authorities. Docker forwarding and deadline regressions assert that every helper directory is removed and that only outer task runs persist after tool probes, inventories, shard execution, and timeout.

`metadata_refresh_preserves_initial_disabled_watch_causes` changes an automatically tracked lockfile with `watch.initial: false`, then changes it while configuration is invalid and repairs the configuration. Each change produces a successful receipt carrying the metadata input cause, while inactive profiles stay inactive. Existing identical-rewrite and invalid-configuration regressions remain required.

`child_projects_reject_root_only_configuration` discovers a real Cargo member and rejects each nondefault workspace option, start profile, remote cache, and CI declaration in its child configuration before task side effects. Empty defaults and project-local dotenv remain accepted.

## Build and Test

```sh
cargo test -p taskflow
cargo clippy -p taskflow --all-targets -- -D warnings
cargo fmt --all --check
cargo test
```

The default conformance suite requires Git on PATH for real affected-selection fixtures; minimal Rust container images must install it explicitly. Install pnpm 10.26.2, Node 24, Go 1.25+, and the repository Rust toolchain for native fixtures. Pull these immutable multi-architecture test images for Docker/S3 fixtures:

```sh
docker pull rust@sha256:5b9332190bb3b9ece73b810cd1f1e9f06343b294ce184bcb067f0747d7d333ea
docker pull node@sha256:6dac556d980b7f0e5498d08f08cee0ca67798b4ad6c23964a9214920e67758d0
docker pull quay.io/minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e
cargo test -p taskflow --test conformance -- --include-ignored
```

To lint generated workflows inside the integration suite, build the repository-pinned actionlint with `go build -o <temporary-executable> github.com/rhysd/actionlint/cmd/actionlint`, then set `TFLOW_ACTIONLINT` to that executable for the conformance invocation. Also run `pnpm ci:workflows`, `pnpm ci:contracts`, and package-local `pnpm test` in `apps/public-docs`.

The `taskflow-conformance` CI job covers macOS/Linux/Windows x64/arm64 and excludes the Docker/S3 tests. `taskflow-docker` explicitly runs those tests on Linux x64/arm64. Both use the centralized `changes` plan before runner allocation on affected PRs and main pushes, run on manual dispatch, and feed `ci-result`.

## Validation evidence and limits
The implementation session exercised real macOS arm64 host execution, Linux arm64 containers through Docker, loopback MinIO, pnpm/Cargo/Go discovery, all four native shard adapters, source-build CLI/schema, generated-workflow actionlint, root Rust tests, and public-docs build/route checks. Committed CI definitions are not evidence that remote platform jobs have already executed. Windows and other host architectures require their conformance results before a release support claim. Registry/service availability failures must be reported as failed or unavailable validation, never counted as passes.

Before this repair, GitHub Actions for PR #906 revision `ba69e16f` reported success for all six native platform jobs and both Linux Docker/S3 jobs (40 successful checks, three skipped). That establishes prior-revision native evidence, including Windows; the new repairs still require their own matrix results. Local repair validation covers all 109 conformance cases, eight library regressions, and one CLI unit test on macOS arm64, including six external suites, and 103 conformance cases plus the same library and CLI tests in Linux arm64 containers. An earlier minimal-container run lacked Git and failed the Git fixture; current minimal containers install Git explicitly before the suite, which passed. Root `cargo test`, TaskFlow Clippy with warnings denied, workspace formatting, repository/generated-workflow actionlint, all 27 CI contract checks, and public-docs tests passed.

A local full Windows GNU cross-check was unavailable because the host lacks the MinGW C compiler required by the TLS dependency. The Windows process-owner source was type-checked separately for the Windows target; this is not a substitute for native execution evidence.

The subsequent `a0f3e8d3` Windows arm64 job failed `notification_paths_survive_concurrent_file_removal` with access denied during canonicalization; its 98 other conformance tests and 11 library tests passed. The error did not retain the failing native operation. Windows metadata queries now use `NtQueryInformationFile` on the original handle, preserving both `STATUS_DELETE_PENDING` and `STATUS_FILE_DELETED` before Win32 conversion can collapse them into access denied. Native open/query diagnostics retain their operation and NTSTATUS in returned errors as well as structured logs. `native_deletion_errors_remain_distinct_from_access_denial` checks both deletion statuses against genuine ACL denial; the pending-deletion fixture also queries a handle opened before deletion. These changes address the remaining lossy status boundary; native CI must establish whether they fully resolve the intermittent stress failure.

This Windows-only repair passed root `cargo test` (including 103 default TaskFlow conformance cases, eight library tests, and one CLI test), TaskFlow Clippy with warnings denied, workspace formatting, all 27 CI contract checks, and workflow actionlint on macOS arm64. The Windows path module and its tests passed isolated arm64 type checking and x64 Clippy, without executing or linking Windows binaries. No native Windows runtime or new external-suite run is claimed for this repair. The implementation follows Microsoft's [native query status contract](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/nf-ntifs-ntqueryinformationfile) and [distinct deletion status definitions](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-erref/596a1078-e883-4972-9bbc-49e60bebca55).

The subsequent reserved-output, inherited-environment, queued-service-exit, and partial-output repairs passed root `cargo test`, all 112 macOS arm64 conformance cases (106 default plus six external), nine library tests, and one CLI test. The final source also passed 106 default conformance cases, nine library tests, and one CLI test in a Linux arm64 container with Git installed. TaskFlow Clippy with warnings denied, workspace formatting, generated/repository workflow actionlint, and all 27 CI contract checks passed. Public documentation was unchanged in this repair; its preceding validation remains recorded above. Windows runtime confirmation of the deletion race and cleanup ownership for detached Unix descendants remain outstanding.

GitHub Actions for revision `12991c69` subsequently passed both Windows native jobs, both Linux native jobs, macOS x64, and both Docker/S3 jobs. The macOS arm64 job failed only scenario 17: FSEvents delivered an edit after its fixed-duration process had finished, so starting a fresh run correctly occurred outside the skip-overlap window. The fixture now holds the process until an independent subscriber observes each change, then releases it. This records native Windows confirmation of the deletion-status changes while preserving the separate detached-descendant limitation.

The following artifact-path, input-scan, CI-runner, Docker-port, and overlap-fixture repairs passed root `cargo test` on macOS arm64, with 110 default TaskFlow conformance cases, nine library tests, and one CLI test. All six external suites also passed, for 116 conformance cases in total. The same source passed 110 default conformance cases plus nine library tests and one CLI test in a Linux arm64 container. TaskFlow Clippy with warnings denied, workspace formatting, generated/repository workflow actionlint, and all 27 CI contract checks passed. Public documentation was unchanged; no new public-docs validation is claimed.

At revision `f3a9c187`, macOS arm64 CI again failed scenario 17 with an extra skip-policy execution. The old fixture passed 20 serial and 80 concurrent local attempts, so the exact CI event ordering was not reproduced locally. Its output-only acknowledgement allowed an older queued observer to read a newer input; the revised fixture excludes startup input events, publishes inputs atomically, and waits for distinct successful observer receipts. The revised scenario passed 20 concurrent repetitions, and the separate discovery-time mutation regression passed. Root `cargo test`, 110 default conformance cases plus nine library tests and one CLI test on both macOS arm64 and Linux arm64, TaskFlow Clippy with warnings denied, formatting, all 30 CI contract checks, and generated/repository workflow actionlint passed. External suites and public-docs tests were not rerun for this test/logging repair; their earlier results remain above. New native CI confirmation is still required, and the added structured watcher diagnostics preserve evidence if a failure recurs.

After merging main's Clibox additions, the next six review repairs added rustup-compatible CI version validation, conflicting affected-selector rejection, incremental encoded artifact bounds, diamond critical-path priority, persistent Docker libtest executables, and complete-or-single-partition cache evidence. Final local validation passed root `cargo test`, all 121 TaskFlow conformance cases on macOS arm64 (114 default plus seven external suites), 11 library tests, and one CLI test. The external suites exercised real Docker, loopback MinIO, and native Go/Rust/Vitest/Jest adapters. A Linux arm64 container with Git installed passed all 114 default conformance cases, 11 library tests, and one CLI test. TaskFlow Clippy with warnings denied, workspace formatting, generated/repository workflow actionlint, 35 CI contract checks, and 38 release-script checks passed. Public documentation was unchanged, so its earlier validation was not repeated. These are local results; the new revision's native CI matrix and the separate detached Unix descendant requirement remain unconfirmed.

GitHub Actions for revision `af97d7d2` passed all six native platform jobs and both Linux Docker/S3 jobs (44 successful checks, three skipped), including the previously intermittent macOS arm64 overlap scenario. This is evidence for that revision; the dotenv-diagnostic and suppression-baseline repairs require their own native CI results. The dangling Unix output-link review was reproduced against the existing containment check and rejected before type inference; its regression test preserves that behavior without changing the capture implementation.

The subsequent dotenv-diagnostic and successful-baseline repairs passed root `cargo test`, all 124 TaskFlow conformance cases on macOS arm64 (117 default plus seven external suites), 11 library tests, and one CLI test. The final source also passed 117 default conformance cases, 11 library tests, and one CLI test in a Linux arm64 container. TaskFlow Clippy with warnings denied, workspace formatting, all 35 CI contract checks, and generated/repository workflow actionlint passed. The external suites exercised Docker, loopback MinIO, and native pnpm/Cargo/Go and Go/Rust/Vitest/Jest adapters. The required ignored DevHud frontend/client outputs were generated before the fresh root build and removed afterward. Public documentation was unchanged and its tests were not rerun. The new revision's remote CI results and detached Unix descendant ownership remain outstanding.

After merging main's Linux package distribution and Clibox changes, the next five review repairs cover same-task output aliases, completed invocation exit status, pre-launch cancellation, workspace-designated query redaction, and bootstrap receipt revalidation. Final root `cargo test` passed on macOS arm64 with `RUST_TEST_THREADS=8`: all 121 default conformance cases, 13 library tests, and one CLI test passed. All seven external suites passed separately, bringing macOS coverage to 128 conformance cases, including real Docker, loopback MinIO, and native adapters. Linux arm64 passed the same 121 default cases, 13 library tests, and one CLI test. TaskFlow Clippy with warnings denied, workspace formatting, 38 CI contract checks, workflow actionlint, and public-docs `pnpm test` passed. All 204 release fixtures passed in a Linux Node container with the linked worktree's Git metadata available; macOS lacks the required `dpkg-deb` and GNU tar behavior. Required frontend/client generated outputs were built before compilation and removed after validation.

Two earlier default-concurrency root attempts were unsuccessful: the first timed out in four unchanged with-watch startup fixtures, and the second invalidated the large-output TaskFlow fixture after detecting an input change. The complete with-watch CLI suite passed its focused retry; neither failure reproduced in the final bounded-concurrency root run. Their causes were not established, and the passing retry does not claim to fix that intermittency. Before these changes, GitHub Actions for revision `96bce005` passed all six native jobs and both Docker/S3 jobs (44 successful checks, three skipped). The new revision still requires its own CI confirmation.

GitHub Actions subsequently confirmed revision `68873dd1` on all six native TaskFlow platforms and both Docker/S3 jobs. The following main-branch merge changes CI selection and documentation only: TaskFlow validation remains eligible on PRs, while Linux CLI packaging follows main's planned PR skip. Both aggregate-result test groups are retained; all 40 CI contract tests and workflow actionlint passed locally. Rust and frontend sources are unchanged, so their suites were not rerun for this merge. The detached Unix descendant requirement remains open.

Revision `67c0fd14` subsequently passed all six native TaskFlow platform jobs and both Docker/S3 jobs. The next main merge retains both TaskFlow's `cron` dependency and Clibox's new readiness dependencies. Local verification passed Clibox Rust unit/CLI/process/readiness tests, 15 npm launcher tests, standalone Cargo publish dry-run, and release-mode npm/pnpm consumer installation smoke tests. No package was published.

The following review repairs preserve aggregate timeout status, mask the combined task log across pipe and shard boundaries, and resolve artifact roots through actual destination filesystem equivalence. Final macOS arm64 root `cargo test --locked` passed with eight test threads and a dedicated temporary root, including 124 default TaskFlow conformance cases, 14 library tests, and one CLI test. All seven external suites passed separately, for 131 conformance cases in total; these exercised native Go/Rust/Vitest/Jest adapters, Docker, and loopback MinIO. A Linux arm64 container with Git installed passed 124 default cases, 14 library tests, and one CLI test using an isolated TaskFlow source copy and the workspace lockfile's cached dependencies. TaskFlow, Clibox, and Nodeup Clippy with warnings denied, workspace formatting, 40 CI contract checks, and workflow actionlint passed. Required frontend/client outputs were generated before compilation and removed after verification. Public documentation and release packaging behavior were unchanged by these repairs; their earlier validations were not repeated.

Before the final passing root run, one attempt lost TaskFlow fixture configuration/temporary files, and an isolated retry failed all Nodeup CLI cases because their temporary parent had disappeared. Investigation identified the existing Nodeup resolver unit test deleting two ancestors above its data directory, reaching the shared temporary root. It now retains an owning `TempDir` instead of deriving a cleanup ancestor. Focused validation and the final root suite preserved a sibling canary in that temporary root. These failures were not counted as passes; this repair does not establish the cause of every earlier intermittent timeout. The new revision's remote matrix results and detached Unix descendant ownership remain outstanding.

GitHub Actions for revision `e5e5c11` passed all six native TaskFlow jobs and Linux arm64 Docker/S3. Linux x64 failed while creating the MinIO alias, which also failed the aggregate CI result. Its captured error contained only the command exit code. Source inspection established that the old liveness gate could succeed before storage/IAM initialization; the fixture now waits for cluster readiness with a bounded deadline and explicit diagnostics. Three fresh local MinIO starts passed the targeted restoration/denied-credentials scenario. This fixes the demonstrated readiness defect without claiming that the unavailable CI stderr was reproduced.

The following four review repairs validate current identities before suppression, forward effective Docker environment names once, reject NUL throughout glob declarations, and retain queued directory mutation causes. Final macOS arm64 root `cargo test --locked` passed with eight test threads and a dedicated temporary root, including 129 default TaskFlow conformance cases, 15 library tests, and one CLI test. All seven external suites passed on the final source, for 136 conformance cases in total, including native adapters, real Docker, and loopback MinIO. An isolated Linux arm64 source build passed 129 default cases, 15 library tests, and one CLI test. TaskFlow Clippy with warnings denied, workspace formatting, 40 CI contract tests, and repository/generated workflow actionlint passed. Required frontend/client outputs were generated before root compilation and removed afterward; the temporary-root sibling canary remained intact. Public documentation and release packaging were unchanged and their earlier suites were not repeated. The repaired revision still requires fresh remote CI, including native Windows execution of environment-name forwarding; detached Unix descendant ownership remains open.

Historical lifecycle gap before the native-supervisor repair: the Unix owner signalled a process group. A macOS reproduction that launches a child with `start_new_session=True`, closes its inherited pipes, and lets the parent finish leaves that child alive after `tflow run` reports success. The reproduction explicitly killed the surviving child afterward. Existing lifecycle conformance proves cleanup only while descendants remain in the owned group. Do not treat it as evidence for daemonized or independently regrouped descendants. Those PR #906 review threads required ownership and cleanup to be implemented and exercised for these descendants on both Unix platforms; periodic PID enumeration cannot prove that a fast double fork never escaped. Linux subreaper adoption alone also does not establish the macOS guarantee, whose kqueue fork-tracking flags are unsupported.

Revision `bc488b05` subsequently passed all six native platform jobs and both Linux Docker/S3 jobs (44 checks passed, four skipped). The following repair merged main's Clibox and async-commit-hook additions without losing either domain's CI selection or aggregation. The five new repairs cover gitlink tree selection, local Docker named-pipe authority, container helper lifetimes, metadata watch causes, and root-only configuration scope.

Local validation initially encountered environment-specific repository fixture failures: five Binpm checks compared `/var` temporary paths with canonical `/private/var` output, and the first canonical temporary root made a Clibox Unix socket path exceed `SUN_LEN`. Neither run passed. The final root invocation uses a short, canonical, privately owned `/private/tmp` directory with a sibling canary. An isolated Linux TaskFlow workspace also required pruning the full repository lockfile, so its source-copy validation ran without `--locked`; it did not modify the repository lockfile.

Final local verification for these five repairs passed root `cargo test --locked` on macOS arm64 with eight test threads and the short canonical temporary root; the sibling canary remained intact. TaskFlow passed 132 default conformance cases, all seven external suites (139 total), 16 library tests, and one CLI test. The isolated Linux arm64 source build passed the same 132 default cases, 16 library tests, and one CLI test. Clippy with warnings denied, workspace formatting, 49 CI contract tests, repository/generated-workflow actionlint, and public-docs `pnpm test` passed. Required DevHud frontend/client outputs were generated before root compilation and removed afterward. These results do not establish the pushed revision's native Windows or other remote matrix results.

During the next repair, main supplied a concrete native-owner design in async-commit-hook: a dedicated Linux subreaper and a macOS launchd supervisor with inherited resource-coalition accounting. Its `TestScopeStartBarrierAndOutput`, `TestScopeReapsDaemonizedDescendants`, and macOS `TestScopeRecoveryAfterSupervisorDeath` passed locally. This is evidence for that separate tool, not TaskFlow: TaskFlow's own detached-child reproduction still returned success with a survivor, which the reproduction explicitly killed and verified absent. At that checkpoint, adapting the private supervisor protocol to TaskFlow's independently usable Rust library was still unimplemented. Neither polling nor installing a process-wide subreaper in the embedding application can substitute for that per-command ownership boundary.

`linux_concurrent_supervisor_launches_do_not_expose_writable_executables` overlaps 512 captured commands across eight runtime threads and sixteen independent sequences. It covers the Linux CI `ETXTBSY` failure mechanism: another fork can temporarily retain a writable descriptor for a filesystem executable even after its creating thread closes it. The sealed anonymous image has no filesystem write/execution exclusion window. Launch failures retain structured error kind and OS code so metadata fallbacks and task diagnostics cannot hide future distinct causes.

## Dependencies and Integrations
See `docs/repository-workflow-contract.md` and the TaskFlow engine contract. No existing application workflow is migrated to TaskFlow.

## Change Triggers
Update this matrix when scenario behavior, adapter scope, or fixture ownership changes. Keep `crates/taskflow/AGENTS.md`, CI contracts, schema, and public guidance aligned.

## References
- [Project index](project-taskflow.md)
- [Engine contract](crates-taskflow-foundation.md)
- [Issue #898](https://github.com/delinoio/oss/issues/898)

`libtest_ids_distinguish_workspace_packages` executes two Cargo packages with identically named integration targets and tests. Inventory IDs include stable package name/version identity, preserving complete shard accounting without checkout paths.

`cache_restores_directory_links_outside_output_roots` restores a directory symlink into a project-local tree outside the owned outputs. Cache link records retain the original directory/file type; older records lacking type information are rejected rather than guessed from the staging tree. It checks portable archive targets, native filesystem targets, successful traversal, and the restored digest. Windows link creation converts portable slashes to native separators because relative reparse targets otherwise remain untraversable.

`unrelated_native_metadata_does_not_block_resolved_selectors` discovers two independent Cargo workspaces, one lacking its lockfile. Coverage records name their owning projects: only selectors in the unresolved workspace require preparation, while global affected selection remains conservative.

`cargo_ci_blueprints_are_independent_of_checkout_paths` compares complete serialized CI blueprints from two identical Cargo workspaces at different absolute paths. Resolved local edges use stable project identities, while native manifests preserve version/configuration binding.

`service_timeout_shuts_down_and_reaps_the_session` verifies the task deadline both before readiness and after a service becomes ready, including cleanup of another running check.

`input_permission_changes_invalidate_cached_success` removes executable permission from a cached command input, both directly and through a file symlink. The next run must fail execution instead of reusing success. Task keys include Unix input modes; portable CI structure manifests remain content-based.

`unchanged_metadata_notifications_do_not_cancel_live_work` rewrites identical configuration bytes during an exclusive process. Delayed/coalesced metadata notifications must not cancel or repeat that process. Changed generations include native metadata bytes and cancel the complete old execution wave before replacement.

`go_sharding_preserves_separated_flag_values_and_package_failures` uses real Go coverage, shuffle, and vet arguments across a passing root and failing child package. Unsupported flags and selection/output overrides fail configuration validation rather than losing positional values.

`partial_output_globs_preserve_neighboring_inputs_and_watch_changes` verifies that a partial JavaScript output glob preserves neighboring JSON inputs for affected selection and watching, while matching outputs and exact output directory descendants cannot self-trigger their producer.

`generic_shard_inventory_and_execution_use_the_configured_shell` runs a portable explicit interpreter that the OS default shell cannot substitute. Both inventory collection and every generic shard execution must use the task's shell setting.

`cargo_member_commands_discover_the_implicit_workspace_root` launches the CLI from a Cargo member, verifies the root development profile and native prerequisite tasks, and checks that Cargo-excluded standalone packages remain independent. Root location uses Cargo's offline workspace lookup without dependency resolution.

`completed_tasks_keep_edits_while_an_independent_wave_task_runs` gates an independent slow task after a watched task has fully exited. Both skip and restart policies must retain the completed task's input change until its reserved wave is available; wave membership alone is not an executing task.

`go_metadata_queries_do_not_contact_module_proxies` runs real CLI queries with a cold isolated module cache and a recording loopback proxy, including an inherited private-module bypass. Missing dependencies yield incomplete coverage without requests or downloaded module metadata.

`docker_service_cleanup_failure_survives_session_cancellation` injects Docker removal and absence-check failures through an isolated CLI fixture. Ctrl+C must still reap its real child and return a cleanup failure instead of a successful session. No real daemon is interrupted.

`partial_outputs_cannot_be_exported_or_restored_as_artifacts` retains local uncached partial output support while rejecting CI export, artifact capture, and restoration before undeclared neighboring inputs can be transferred or replaced.

`readiness_commands_use_the_configured_shell` releases a readiness-dependent check only after the service's explicit interpreter runs its probe, then verifies the server process is reaped on cancellation.

`check_rejects_malformed_positive_and_negative_input_globs` exercises the public CLI's preflight diagnostics for malformed inclusion/exclusion patterns while retaining valid ordered globs and automatic inputs.

`environment_names_follow_host_precedence_and_security_rules` checks host-specific casing across dotenv/task/CLI precedence, secret scoping (including Unicode), environment fingerprints, lookup context, and remote credentials. The grouped-task subprocess fixture injects mixed-case inherited credentials on Windows and verifies both absence from siblings and masked stored logs.

`setup_cancellation_preserves_receipts_and_service_events` cancels finite tasks and services while waiting for a resource and while probing a real tool process. Receipts and service events retain cancellation, commands never start, and probe children are reaped. Unverified Docker cleanup remains a failure even when cancellation is active.

`cache_verify_rejects_misdirected_and_inconsistent_artifacts` gives the CLI valid object envelopes containing a mismatched entry key, invalid version, output/file digest corruption, malformed content encoding, duplicate paths, and traversal paths. Only the intact entry remains valid, verification fails, and task outputs remain untouched.

`notification_paths_survive_concurrent_file_removal` repeatedly removes and recreates a file while normalizing its notification path. Missing ancestors are retried at the next parent without hiding broken links or permission errors. On Windows, path resolution and deletion-state inspection share one handle so NTFS deletion-storage paths are treated as missing ancestors. `deleted_handle_does_not_resolve_to_ntfs_storage` verifies an open deleted handle even after a replacement reuses its name, including access-denied final-path lookups while retaining permission errors for live handles; native conformance CI also runs library tests. Session barrier assertions surface early session failures directly instead of waiting for a record timeout.

`docker_context_cannot_override_a_validated_local_host` injects a remote selected context alongside a local `DOCKER_HOST` and verifies rejection before container launch. Endpoint validation delegates precedence to Docker using the exact launch environment.

`check_rejects_remote_credentials_in_every_project_task` exercises CLI preflight across unselected child tasks, all three environment declaration forms, all transport credential references, and host-specific name casing, including remote mode `off`.

`ci_export_rejects_symlink_destinations_before_writing_either_file` rejects external parent, workflow-leaf, and blueprint-leaf links without creating or replacing either output; an ordinary missing nested destination remains supported.

`tool_identity_includes_stderr_without_contaminating_metadata` upgrades a tool reporting only on stderr, then moves identical bytes between streams. Both changes invalidate cached success while JSON metadata queries retain stdout-only parsing.

`ci_bundle_limits_apply_to_each_artifact_independently` uses scaled limits with the production JSON codec to accept multiple individually valid artifacts whose combined size exceeds one artifact limit, while rejecting an oversized artifact, metadata, or input file. The writer uses the same size validator.

`cache_restore_rejects_filesystem_aliases_before_replacing_outputs` checks case and Unicode equivalence for both files and directory prefixes on the actual restore filesystem. Aliases preserve the existing outputs; distinct names restore with the declared digest, and every temporary probe is removed.

`artifact_roots_follow_destination_filesystem_equivalence` compares declared and captured case/Unicode spellings, including nested roots. Equivalent names support local cache hits, deleted-output restoration, and artifact restoration into a clean directory with unchanged digests. Distinct names on case-sensitive destinations remain rejected before mutation, and temporary probes are removed.

`log_write_failure_awaits_both_streams_and_cleanup` gives the real output drainer a read-only log descriptor and delays its sibling stream. Both owners settle before the awaited cleanup future runs; an unverified removal takes precedence over the log error. Finite tasks, shard commands, and failed readiness use this same completion path.

`finite_timeouts_fail_while_operator_cancellation_remains_distinct` compares deadline expiry with explicit cancellation using real parent/child processes. It asserts distinct outcomes and exit codes, timeout diagnostics, blocked dependents, absent success cache entries, and completed cleanup.

`finite_cli_and_ci_preserve_deadline_exit_status` verifies code 124 through the actual finite-run and CI-execute CLI boundaries. Invocation exit status preserves cancellation first, then failed timeout receipts, before falling back to ordinary failure code 1; sharded Docker timeout fixtures also check the actual CLI status.

`explicit_wildcard_inputs_include_ignored_directories_in_cache_and_watch` exercises star, globstar, character-class, and brace patterns against `dist/manifest.json`. Each form participates in snapshots, cache invalidation, and live watch reruns, while internal restore trees remain excluded from both snapshots and event matching.

`libtest_inventory_honors_explicit_manifest_selection` runs a Cargo-excluded standalone package with split and equals-form manifest arguments, including a task directory without its own Cargo manifest. Inventory and complete shard results identify only the selected package.

`session_revalidates_provided_prerequisite_outputs` modifies and deletes a watched consumer's prerequisite output across live session waves, covering both uncached rebuild and cached restoration before consumption.

`docker_cleanup_reuses_the_validated_launch_environment` isolates the Docker executable and daemon/context/configuration from the parent environment and verifies both awaited removal/absence checks and destructor cleanup, including a relative configuration path.

`shard_timeouts_and_cancellation_preserve_receipt_reasons` exercises generic exchange and native command inventories, including empty shards. Deadlines and cancellation retain codes 124/130 and complete failed/cancelled accounting even when no generic result file is written.

`affected_selection_rejects_unknown_task_filters` validates qualified and unqualified filters independently of changed-file selection, including mixed valid/invalid requests and public plan/run preflight. Valid unaffected requests still produce an empty plan.

`docker_platform_validation_applies_cli_defaults_first` checks omitted, Linux, and non-Linux task OS values against all CLI OS defaults without starting Docker or task processes.

`check_rejects_absolute_input_patterns_on_every_host` rejects absolute inputs even inside the checkout, along with Windows drive/UNC/rooted forms and negative variants. Project-relative sibling inputs remain supported.

The distributed artifact fixture also corrupts terminal-unit file digests, paths, and matching receipt/artifact digest claims. Final aggregation must reject each unusable bundle without relying on downstream restoration.

`libtest_rejects_custom_harnesses_only_for_selected_targets` selects standard library and integration tests beside unselected custom harnesses in both the same and another package. A selected custom harness with a quoted TOML key is rejected before invocation. Harness scope follows [Cargo target declarations](https://doc.rust-lang.org/cargo/reference/cargo-targets.html#the-harness-field).

`readiness_endpoint_validation_does_not_require_a_running_service` accepts IPv4, bracketed IPv6, hostnames, and HTTP(S) paths without resolving or connecting during validation.

The finite deadline/cancellation fixture requires both real PIDs to disappear within five seconds after receipt delivery. Owned pipes have already reached EOF and the direct child has been waited; the bound accommodates asynchronous OS reaping of orphaned zombie grandchildren, rather than asserting PID disappearance in the same scheduler instant. Surviving processes still fail the test. This does not extend detached-descendant ownership coverage.

Windows path opening now preserves `NtOpenFile` status: `STATUS_DELETE_PENDING` becomes a missing path, while genuine access denial remains an error. [CreateFile maps a pending deletion to access denied](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew), so its Win32 result alone cannot distinguish the race. `delete_pending_open_is_not_a_permission_failure` holds a legacy delete-pending handle open, verifies missing-path classification before closing it, and checks the replacement file afterward. The concurrent removal fixture continues to exercise native notification normalization. Local cross-target type checks do not establish Windows runtime success.

Docker CLI fault-injection fixtures hard-link the compiled helper within the temporary filesystem instead of reopening executable bytes for copying. This avoids Linux `ETXTBSY` races with concurrent subprocess creation while preserving an isolated executable name and environment.

Windows native opens explicitly request `FILE_READ_ATTRIBUTES`, the metadata permission required by the path/deletion queries, instead of a zero access mask. They retain synchronous I/O with its required `SYNCHRONIZE` access right so metadata queries finish before the handle is inspected. `native_metadata_open_resolves_live_files_and_directories` verifies the native open, final-name query, and deletion-state query separately for an ordinary file and directory. Existing tests retain both deletion forms and permission-error classification. Debug diagnostics identify only the failed operation and OS/native status, never the queried path. [Microsoft's native file-access contract](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/wdm/nf-wdm-zwcreatefile) defines attribute access separately from file-data access.

`unix_backslash_paths_cannot_alias_directory_paths` verifies distinct Unix literal-backslash and nested-directory names are rejected before input or output identities can collide.

`captured_output_link_chains_never_leave_the_project` rejects captured and uncached output chains that cross an external prefix before returning to an internal file, while accepting an entirely internal link.

`shard_deadline_includes_inventory_and_all_units` checks slow inventory and cumulative unit duration against a single timeout, with failed complete accounting when inventory succeeded.

`remote_publication_cannot_retract_completed_receipts` cancels real HTTP object staging and manifest commits, including a lost response after the entry reaches the server. Only pre-completion cancellation produces a cancelled receipt; remote entry visibility follows durable local completion.

The real Go flag suite also compares `-failfast` and `-failfast=false` across two shard partitions: a later test is skipped only when requested, all results are accounted for, and the suite remains failed.

`input_digest_uses_bounded_reads_and_preserves_sha256_identity` checks the input reader buffer bound, reference digest parity, and regular/file-link identities.

`dangling_input_links_track_target_deletion_and_recreation` runs automatic and explicit inputs through present, deleted, and recreated file-link targets while rejecting missing external targets and cycles. Absolute internal links retain filesystem root aliases, including macOS `/var` and `/private/var`, before and after target deletion.

`ready_service_identity_invalidates_cached_consumers` restarts a real service with identical and changed inputs and verifies dependent cache reuse only for the identical service identity.

`shard_partitions_reuse_unsharded_prerequisite_cache_keys` runs separate suite partitions and proves their shared ordinary prerequisite executes only once, while suite cache identities remain partitioned.

`mixed_subscriptions_preserve_trigger_defaults_and_explicit_overrides` injects a file change followed by a timer tick during an active execution, checking preserved queued causes and explicit queue/skip/restart overrides.

`shard_deadlines_leave_docker_cleanup_available` times out inventory and unit processes through an isolated Docker CLI fixture and verifies awaited removal still executes after the task deadline, with code 124 and a reaped CLI child.

`changed_outputs_override_unchanged_reports` exercises cached and uncached producers invoking the real unchanged CLI. A newly created or modified output propagates to a consumer selected only by its prerequisite, while an identical successful output preserves suppression.

`check_validates_remote_syntax_without_credentials` checks invalid endpoint, bucket, namespace, region, and credential-reference syntax in every access mode. Valid remote settings pass without credentials or network access, and diagnostics omit embedded endpoint secrets.

`either_subscription_can_request_one_initial_execution` runs a real session across all nine absent/false/true watch and schedule combinations. Either initial flag requests exactly one run, while subscriptions that only disable initial execution remain idle.

`cache_rejects_nonportable_link_targets_on_every_host` rejects drive-absolute, drive-relative, rooted, UNC, and malformed serialized link targets before restoration changes outputs. Unix also rejects real internal links whose otherwise-relative names would become Windows drive prefixes during transfer.

`git_revision_operands_cannot_be_diff_options` uses real commits and a rename to verify valid comparisons and worktree untracked selection. Library and CLI reject option-shaped or empty base/head operands, including an output-file option, without creating that file.

`queued_discovery_mutations_run_watchers_without_initial_execution` gates native metadata behind a real subprocess, edits a file or moves a populated directory into an input root while discovery is blocked, then verifies that an initial-disabled watcher consumes the mutation without a second change.

`queued_directory_mutations_keep_causes_already_in_the_baseline` supplies directory-only create/rename/remove and ambiguous events after their mutation is already present in the first snapshot. Root overlap preserves the queued cause for an initial-disabled task whose file glob does not match the directory itself. Known excluded file events, unrelated roots, and the task's own output tree remain excluded.

`failed_service_logs_stop_live_processes_and_await_cleanup` injects read-only persisted log handles into live TCP-listening Rust processes. Either output stream failure must reap the process, release the listener, await cleanup, preserve cleanup-error priority, and leave successful EOF non-cancelling.

`libtest_cross_targets_require_generic_before_execution` validates both Cargo target argument forms and CLI preflight before any prerequisite runs, while keeping target commands valid through the generic protocol.

`docker_digest_references_validate_the_complete_suffix` rejects extra digest separators, duplicate markers, absent names, uppercase digests, and invalid lengths before Docker launch; registry ports and image tags remain accepted.

`overlap_replacements_finish_before_independent_waves_and_keep_latest_receipts` changes an input during a real exclusive process, exercises queue/restart before an unrelated gated task completes, and verifies that the old wave cannot overwrite the replacement receipt. `completed_tasks_keep_edits_while_an_independent_wave_task_runs` now requires the second execution before releasing the slow task for queue, skip, and restart.

`new_waves_retain_prerequisite_causes_behind_active_consumers` changes a producer while its previous consumer is gated, proving the producer can finish promptly and the consumer retains a second run for that prerequisite change.

`ci_export_rejects_blank_runner_mappings_before_writing` rejects empty, whitespace-only, and control-character runner labels during configuration checks and direct blueprint export, including the plan/aggregate fallback mapping. Neither workflow nor blueprint is written on failure.

`check_rejects_invalid_docker_ports_before_prerequisites` checks NUL, option-shaped, and blank Docker publish values through configuration loading, `check`, and `run`; the prerequisite marker is never written. Container-only, IPv4/IPv6 host binding, protocol suffix, and port-range forms remain accepted without contacting Docker.

`scenario_17_queue_skip_restart_own_real_exclusive_processes` starts with absent inputs and initial-disabled subscriptions, then creates the first input after an activation-only barrier. It holds a real TCP-exclusive process behind a release gate until each input version has a new successful independent subscriber receipt. Output content alone is not an acknowledgement: an older queued execution can read a newer version before its notification is handled. Each observer completes before the next version is published. Queue coalesces both edits, skip ignores both, and restart reaps each predecessor before the next process binds the port. Session debug logs record event age, baseline/snapshot decisions, and overlap decisions without input contents; conformance logging accepts `RUST_LOG` for diagnosis.

`scenario_10_local_docker_libtest_requires_persisted_executables` checks that Cargo config, environment, and argv target-directory selections outside the mount fail before listing; a target directory under `/workspace` inventories and runs both tests successfully across separate containers. The Docker CI fixture uses the pinned Rust image shown above.

`ci_export_requires_rustup_compatible_numeric_versions` rejects single/multiple v prefixes before writing either workflow file while accepting numeric and dated Rust toolchains. `explicit_changed_files_cannot_discard_a_git_base` exercises both plan/run and valid-looking/invalid base names without allowing prerequisites to execute.

The cache capture unit regressions use bounded fixtures to check empty file/directory records, exact Base64 boundaries, escaped paths, and link metadata; identity-only snapshots remain independent of transfer limits. `critical_path_priority_counts_shared_diamond_suffixes` seeds duration receipts and verifies that the longer diamond path wins over an independent task in a single-worker execution.

`cache_verify_rejects_impossible_partial_shard_suites` executes a four-partition generic suite, accepts a single nonzero-index partition and the complete suite, and rejects zero/two/three reports through both historical integrity validation and the CLI.

`dangling_unix_output_links_fail_before_capture` confirms the existing containment check rejects missing Unix link targets before link type inference in local output hashing, snapshots, and portable artifact capture. After each target is created as a file or directory, capture and restoration preserve its actual type and readable content. This is regression evidence for the existing rejection boundary, not a new capture policy.

`malformed_dotenv_diagnostics_never_expose_values` covers unterminated single/double quotes, multiline values, and malformed unquoted values after an earlier secret assignment. It checks full/debug error chains, CLI stdout/stderr, serialized failed receipts, and task state files for a synthetic secret canary.

`outputless_suppression_requires_the_latest_attempt_to_succeed` covers absent and empty output declarations, first-run failures, imported failed receipts, successful baseline establishment, suppression after success, and a later failure invalidating that baseline. The cancellation-during-restore fixture now verifies that cancellation leaves no suppressible receipt while the prior cache artifact remains valid and available for future restoration.

`suppression_requires_current_environment_tools_and_inputs` retains prerequisite-only causes while independently changing declared environment, observed tool identity, and own input contents. Absent, empty, and concrete outputs all require execution after identity changes; identical identities retain suppression. The executor performs this comparison under its task/resource locks, using the same prepared key as execution and cache lookup.

`late_remote_commit_cancellation_preserves_cli_success` sends SIGINT to the real Unix CLI during an acknowledged or lost remote manifest response, after local publication. Completed receipts, invocation success, and exit code remain successful; the portable publication unit test verifies the same aggregate boundary and cancellation before publication.

`cancelled_setup_never_consumes_a_baseline_or_launches` covers pre-cancelled uncontended resource locks and no-probe task setup, plus cancellation during synchronous input hashing. Pending cancellation preserves the previous receipt, and neither path reaches command launch.

`queries_mask_task_local_values_designated_by_other_tasks` checks real task/project CLI queries and environment scoping when only a sibling task designates a task-local variable as secret, including Windows environment-name aliases.

`bootstrap_refresh_revalidates_outputs_and_configuration` starts with unresolved real Cargo metadata and an installer that deletes/replaces an earlier output or rewrites its producer configuration. Finite CLI runs and development sessions must restore the refreshed producer before consumers run. `bootstrap_receipts_require_current_keys_outputs_and_prerequisites` independently verifies retaining valid receipts and rejecting stale keys/outputs through their dependent closure.


`unix_owners_reap_detached_descendants_before_returning` uses the library directly to run a double-fork fixture that changes session/process group, clears its environment, changes its directory, closes inherited stdio, and binds an exclusive socket. Normal exit, cancellation, timeout, and destructor cleanup must permit an immediate replacement bind and leave no live descendant. A separate unrelated process must survive. `unix_owner_survives_cli_death_and_reaps_detached_children` kills the real CLI and verifies the independent owner's EOF lease still triggers cleanup without publishing a receipt. These cases exercise the embedded supervisor on each Unix platform; process snapshots alone are not the completion proof. `unix_supervisors_preserve_the_callers_output_permissions` checks that private supervisor files do not change the caller's umask for task outputs. Focused ownership cases passed on macOS arm64 and in a Linux arm64 container. The Docker deadline fixture allows ten seconds for owner/context setup before its blocked command times out, with a separate 30-second bound for verified cleanup. Session reuse waits for completed receipts instead of treating a command's output marker as cleanup completion.


The next review repairs add `generic_shard_results_are_bounded_before_json_parsing` (reject sparse oversized result files before allocation/parsing), `reused_session_receipts_do_not_replay_historical_changes` (finite and live-service receipt reuse across an unchanged wave), and `resolved_graph_installs_refresh_before_newly_selected_work` (resolved graphs whose installers change commands, prerequisites, and later installation phases in both run and start). The Unix supervisor repair adds detached-descendant and caller-umask coverage described above. The macOS read-only watch CI regression now establishes its baseline before creating the watched input.

Final local validation for these repairs passed root `cargo test --locked` on macOS arm64, with eight test threads and a short canonical private temporary root whose sibling canary remained intact. TaskFlow passed 138 default conformance cases, all seven external suites (145 total), 16 library tests, and one CLI test. Real external suites exercised Go/Rust/Vitest/Jest, pnpm/Cargo/Go discovery, Docker, and loopback MinIO. A Linux arm64 source-copy build passed the same 138 default cases, 16 library tests, and one CLI test. Clippy with warnings denied, workspace formatting, 49 CI contract tests, repository/generated workflow actionlint, and public-docs `pnpm test` passed. Required frontend/client outputs were generated before the root build; repository-owned generated `dist` directories were removed afterward. New remote platform CI remains unverified locally.

Earlier development runs did not pass: pre-start cancellation lacked an owner completion acknowledgement, receipt-based session tests cancelled before native cleanup finished, and a two-second Docker fixture deadline could expire before container preparation. Those defects and fixture assumptions were corrected before the final root suite. An initial Linux target cache on the host bind mount also failed with a missing `digest` artifact; rebuilding in a fresh container-native target volume passed without changing the repository lockfile. The isolated workspace necessarily prunes its copied lockfile and does not use `--locked`; root verification does.


The next six review repairs retain unsharded bootstrap receipt validation, preserve bootstrap timeout/cancellation exit codes and JSON receipts, validate automatic metadata link containment, reconstruct CI platform defaults, support Linux noexec temporary storage, and isolate native-owner bootstrap environments. `session_sharded_install_revalidates_its_complete_bootstrap_receipt` exercises a real partitioned start whose installer runs the complete suite. `bootstrap_installs_preserve_termination_receipts_and_cli_status` covers failed, timed-out, and cancelled installers in run and start, with no consumer launch. `automatic_metadata_links_require_workspace_containment` rejects external existing/missing targets and cyclic/intermediate escapes while tracking internal target deletion/recreation with input scanning disabled. The Cargo conditional-selector test now round-trips CI blueprints for all six selected platforms and compares prerequisites, direct/affected causes, explicit overrides, and units.

`unix_loader_hooks_run_only_inside_the_owned_task` builds a real loader library whose constructor double-forks before main. Exactly the actual task loads it; completion, cancellation, and timeout reap its detached child while an unrelated process survives. `supervisor_image_is_an_immutable_anonymous_executable` verifies Linux image seals and absence of a disk executable. The concurrent-launch regression above runs 512 captures. Linux x64 CI for the previous revision recorded `ETXTBSY` during supervisor launch; other x64/arm64 failures lost their underlying spawn error. The anonymous image removes the identified writable-file race, and launch diagnostics now preserve safe OS codes. This does not claim reproduction of the unavailable error codes.

Final verification passed root `cargo test --locked` on macOS arm64 with eight test threads and a short canonical temporary root; its sibling canary remained intact. TaskFlow passed 142 default conformance cases and all seven real native/Docker/S3 suites (149 total), 16 library tests, and one CLI test. The Linux arm64 source-copy build passed 143 default conformance cases, 17 library tests, and one CLI test with the supervisor control directory on a verified `noexec` temporary mount. Linux-only tests account for the additional cases. Both hosts passed TaskFlow Clippy with warnings denied. Workspace formatting, 49 CI contract tests, repository/generated workflow actionlint, and public-docs `pnpm test` passed. Required DevHud frontend/client outputs were generated before root compilation and repository-owned `dist` directories were removed afterward. No new Windows or other remote matrix result is claimed before the final push.

The first Linux full-suite attempt failed five Cargo fixtures because its temporary root was placed inside the isolated Cargo workspace. A direct metadata reproduction confirmed Cargo's unexpected workspace-membership error; moving fixtures to a separate executable temporary root passed the full suite while keeping the supervisor's temporary mount noexec. The isolated source copy prunes its copied workspace lockfile and does not use `--locked`; the repository lockfile was unchanged and root verification remained locked. The container needed its pinned toolchain's Clippy component installed before the successful Linux lint run.
