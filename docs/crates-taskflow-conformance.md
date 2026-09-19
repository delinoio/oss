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

Additional invariants include unknown/duplicate configuration rejection, schema freshness, stale/partial CI receipt rejection, required artifact accounting, cache path traversal rejection before mutation, masked stored logs, and service failure cancelling other running checks before returning.

`grouped_tasks_only_receive_their_declared_secrets` passes a CI-style credential union to a real CLI run: only the declaring task receives the credential, and persisted output remains masked.

`check_rejects_invalid_readiness_before_starting_processes` rejects empty readiness commands and malformed readiness timeouts at configuration validation, before service startup.

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

## Dependencies and Integrations
See `docs/repository-workflow-contract.md` and the TaskFlow engine contract. No existing application workflow is migrated to TaskFlow.

## Change Triggers
Update this matrix when scenario behavior, adapter scope, or fixture ownership changes. Keep `crates/taskflow/AGENTS.md`, CI contracts, schema, and public guidance aligned.

## References
- [Project index](project-taskflow.md)
- [Engine contract](crates-taskflow-foundation.md)
- [Issue #898](https://github.com/delinoio/oss/issues/898)

`libtest_ids_distinguish_workspace_packages` executes two Cargo packages with identically named integration targets and tests. Inventory IDs include stable package name/version identity, preserving complete shard accounting without checkout paths.

`cache_restores_directory_links_outside_output_roots` restores a directory symlink into a project-local tree outside the owned outputs. Cache link records retain the original directory/file type; older records lacking type information are rejected rather than guessed from the staging tree.

`unrelated_native_metadata_does_not_block_resolved_selectors` discovers two independent Cargo workspaces, one lacking its lockfile. Coverage records name their owning projects: only selectors in the unresolved workspace require preparation, while global affected selection remains conservative.

`cargo_ci_blueprints_are_independent_of_checkout_paths` compares complete serialized CI blueprints from two identical Cargo workspaces at different absolute paths. Resolved local edges use stable project identities, while native manifests preserve version/configuration binding.

`service_timeout_shuts_down_and_reaps_the_session` verifies the task deadline both before readiness and after a service becomes ready, including cleanup of another running check.

`input_permission_changes_invalidate_cached_success` removes executable permission from a cached command input, both directly and through a file symlink. The next run must fail execution instead of reusing success. Task keys include Unix input modes; portable CI structure manifests remain content-based.

`unchanged_metadata_notifications_do_not_cancel_live_work` rewrites identical configuration bytes during an exclusive process. Delayed/coalesced metadata notifications must not cancel or repeat that process. Changed generations include native metadata bytes and cancel the complete old execution wave before replacement.

`go_sharding_preserves_separated_flag_values_and_package_failures` uses real Go coverage, shuffle, and vet arguments across a passing root and failing child package. Unsupported flags and selection/output overrides fail configuration validation rather than losing positional values.

`partial_output_globs_preserve_neighboring_inputs_and_watch_changes` verifies that a partial JavaScript output glob preserves neighboring JSON inputs for affected selection and watching, while matching outputs and exact output directory descendants cannot self-trigger their producer.

`generic_shard_inventory_and_execution_use_the_configured_shell` runs a portable explicit interpreter that the OS default shell cannot substitute. Both inventory collection and every generic shard execution must use the task's shell setting.

`cargo_member_commands_discover_the_implicit_workspace_root` launches the CLI from a Cargo member, verifies the root development profile and native prerequisite tasks, and checks that Cargo-excluded standalone packages remain independent. Root location uses Cargo's offline workspace lookup without dependency resolution.
