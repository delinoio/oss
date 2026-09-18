# Runmoor command rules

- Follow `docs/project-runmoor.md` and `docs/cmds-runmoor-foundation.md`; issue #893 is the product contract.
- Keep CLI, TOML v1, SQLite v1, and versioned JSON contracts synchronized with the English README and public `/runmoor` documentation.
- Use the official pinned `actions/scaleset` client. Persist message effects before acknowledgement; derive demand from statistics, never event counts.
- Preserve installation ownership, per-runner resource reservations, original configuration generations, and cleanup progress across crashes. Never adopt resources based only on their names.
- GitHub management credentials stay on the host. Do not persist JIT credentials or copy raw workflow output, upstream response bodies, or subprocess stderr into diagnostics.
- Scale down through GitHub's busy-aware removal before terminating an idle execution. Preserve an assignment that wins the race.
- Docker jobs never receive the host socket, personal bind mounts, credential environment, or manager state. Each DinD daemon and all of its storage belong to exactly one execution.
- Detached VM/guest supervisors must use real null output descriptors so parent exit cannot cause SIGPIPE.
- Tart commands use argv/stdin, private `TART_HOME`, and `TART_NO_AUTO_PRUNE=1`. Base revisions are never job VMs. Setup and validation VMs share the global budget and two-VM ceiling.
- Status/doctor must expose capacity waits and image recovery. Force-stop immediately cancels preparation, and reload must not undo a concurrent stop or accept a concurrently removed image.
- Pool-scoped stop must leave other pools running. Storage relocation requires fully completed execution cleanup and closed image operations, preserving the installation identity.
- Keep unsupported platform implementations compilable so the repository's Windows Go CI remains intact.
- Run `go test ./cmds/runmoor/...`, supported-host race tests, and `go vet ./cmds/runmoor/...`. Docker integration is opt-in with `RUNMOOR_DOCKER_TEST=1`; Tart integration is opt-in with `RUNMOOR_TART_TEST=1`. Ordinary tests never contact GitHub or install user services.
- Publication must reject conflicting existing tags and existing releases before signing; uncertain remote status is a failure.
- Release dry runs never obtain OIDC credentials, sign, publish, or produce pretend Sigstore evidence. Public releases remain prereleases until the verification gap is explicitly removed from the contract.
