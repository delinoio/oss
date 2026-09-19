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
| 3–5 | `scenarios_03_04_05_18_unchanged_preserves_independent_causes` covers chained, direct, own-input, and multiple prerequisite causes. |
| 6–7 | `scenario_06_streaming_secret_masking_handles_all_boundaries`, `scenarios_06_07_dotenv_precedence_disable_and_persisted_masking`, and CLI query masking. |
| 8 | `scenarios_08_15_16_19_watch_and_timer_companions_preserve_server` uses a real TCP server. |
| 9 | Generic and Go/libtest/Vitest/Jest suites compared with unsharded commands, deterministic assignment/aggregate rejection, `cached_shards_retain_complete_accounting_evidence`, and `scenario_09_ci_shards_reject_partial_success_and_gate_secret_mapping`. |
| 10 | `scenario_10_local_docker_execution_uses_explicit_platform_and_cleans_container` also reports unchanged through the injected helper in an image without TaskFlow. |
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

Additional invariants include unknown/duplicate configuration rejection, schema freshness, stale/partial CI receipt rejection, required artifact accounting, cache path traversal rejection before mutation, masked stored logs, and service failure cancelling other running checks before returning.

`grouped_tasks_only_receive_their_declared_secrets` passes a CI-style credential union to a real CLI run: only the declaring task receives the credential, and persisted output remains masked.

`check_rejects_invalid_readiness_before_starting_processes` rejects empty readiness commands, malformed readiness timeouts, invalid TCP host/port addresses, and malformed or non-HTTP(S) readiness URLs at configuration validation, before service startup.

`unchanged_prerequisites_cannot_suppress_corrupt_outputs` checks a three-task chain with both cached and uncached middle tasks: existing but modified outputs must be restored or rebuilt before a directly requested consumer can read them. Suppression requires the current output digest to match a previous successful receipt.

`remote_lookup_input_change_preserves_current_outputs` changes an input at a loopback HTTP response barrier during object lookup. Restoration must recheck freshness before replacing any output, retain the post-restore race check, and reject the stale entry without publishing it locally.

`cache_clean_serializes_with_readers_and_writers` blocks a separate CLI cleaner behind a read lock, then races local stores, reads, and cleaning. The lock survives removal of the cache tree and covers the complete object/entry publication transaction.

`cache_rejects_links_through_external_ancestors_before_mutation` covers external directory links, parent components following links, archived link chains, cycles, and a valid internal chain. Validation follows the effective target before changing any existing output.

`cargo_target_selectors_follow_the_selected_platform` uses a real offline Cargo workspace to verify conditional, renamed prerequisites for all six platform selections and explicit compilation-target precedence.

`shard_preflight_preserves_explicit_metadata_bootstrap` validates the complete shard request before an explicitly declared installation creates missing Cargo metadata; installation itself runs unsharded before the selected test partition.

`reading_session_files_does_not_cancel_or_requeue_work` verifies that metadata/input reads cannot cancel or repeat a live task. Linux access notifications from discovery and hashing must not be treated as mutations.

`session_normalizes_watch_paths_for_existing_and_deleted_inputs` exercises a lexical root alias, deletion, and recreation. Notification paths use the same canonical root as discovery, including Windows verbatim prefixes; removed leaves are normalized through their existing ancestors.

The native JS sharding fixture executes files under a directory containing spaces, compares recorded file executions against the unsharded run, and prints masked task logs on failure. Validated file selectors are project-relative so Windows canonical path prefixes do not become JS filename filters.

Session fixtures await observable PID/record markers with a bounded 60-second startup allowance for concurrent native builds. Readiness failure permits 15 seconds for both children to start, with five additional seconds for cleanup. Ordinary shutdown tests retain their five-second bound; failures include structured engine logs and observed records instead of relying on fixed short startup sleeps.

## Storage
Fixtures use temporary directories and containers with UUID-v7 names. They do not replace repository credentials or identity material. Generated `dist` and `.taskflow` content is ignored and not committed.

## Security
Real remote transport tests use loopback MinIO and fixture-only credentials. The CI workflow consumes no repository secrets and never publishes. Native queries/installations are explicit in fixtures; untrusted PR execution disables remote access and secret injection.

## Logging
Test assertions include typed receipts and native fixture diagnostics on failure. Production logs remain subject to the engine's designation mask and stable context rules.

## Build and Test

```sh
cargo test -p taskflow
cargo clippy -p taskflow --all-targets -- -D warnings
cargo fmt --all --check
cargo test
```

Install pnpm 10.26.2, Node 24, Go 1.25+, and the repository Rust toolchain for native fixtures. Pull these immutable multi-architecture test images for Docker/S3 fixtures:

```sh
docker pull node@sha256:6dac556d980b7f0e5498d08f08cee0ca67798b4ad6c23964a9214920e67758d0
docker pull quay.io/minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e
cargo test -p taskflow --test conformance -- --include-ignored
```

To lint generated workflows inside the integration suite, build the repository-pinned actionlint with `go build -o <temporary-executable> github.com/rhysd/actionlint/cmd/actionlint`, then set `TFLOW_ACTIONLINT` to that executable for the conformance invocation. Also run `pnpm ci:workflows`, `pnpm ci:contracts`, and package-local `pnpm test` in `apps/public-docs`.

The `taskflow-conformance` CI job covers macOS/Linux/Windows x64/arm64 and excludes the Docker/S3 tests. `taskflow-docker` explicitly runs those tests on Linux x64/arm64. Both use the centralized `changes` plan before runner allocation on affected PRs and main pushes, run on manual dispatch, and feed `ci-result`.

## Validation evidence and limits
The implementation session exercised real macOS arm64 host execution, Linux arm64 containers through Docker, loopback MinIO, pnpm/Cargo/Go discovery, all four native shard adapters, source-build CLI/schema, generated-workflow actionlint, root Rust tests, and public-docs build/route checks. Committed CI definitions are not evidence that remote platform jobs have already executed. Windows and other host architectures require their conformance results before a release support claim. Registry/service availability failures must be reported as failed or unavailable validation, never counted as passes.

A local full Windows GNU cross-check was unavailable because the host lacks the MinGW C compiler required by the TLS dependency. The Windows process-owner source was type-checked separately for the Windows target; this is not a substitute for native execution evidence.

Known unresolved lifecycle gap: the Unix owner currently signals a process group. A macOS reproduction that launches a child with `start_new_session=True`, closes its inherited pipes, and lets the parent finish leaves that child alive after `tflow run` reports success. The reproduction explicitly killed the surviving child afterward. Existing lifecycle conformance proves cleanup only while descendants remain in the owned group. Do not treat it as evidence for daemonized or independently regrouped descendants. PR #906 review thread `PRRT_kwDORRAKg86j-f4J` remains open until ownership and cleanup are implemented and exercised for these descendants on both Unix platforms; periodic PID enumeration cannot prove that a fast double fork never escaped. Linux subreaper adoption alone also does not establish the macOS guarantee, whose kqueue fork-tracking flags are unsupported.

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

`log_write_failure_awaits_both_streams_and_cleanup` gives the real output drainer a read-only log descriptor and delays its sibling stream. Both owners settle before the awaited cleanup future runs; an unverified removal takes precedence over the log error. Finite tasks, shard commands, and failed readiness use this same completion path.

`finite_timeouts_fail_while_operator_cancellation_remains_distinct` compares deadline expiry with explicit cancellation using real parent/child processes. It asserts distinct outcomes and exit codes, timeout diagnostics, blocked dependents, absent success cache entries, and completed cleanup.

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
