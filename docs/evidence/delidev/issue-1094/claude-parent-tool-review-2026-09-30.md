# Issue #1094 Claude parent-tool ownership repair

Review thread `PRRT_kwDORRAKg86ng_NW` identified that two child task IDs could
claim one original Agent/Task tool, making forwarded content ownership depend
on map iteration. Shared ownership validation now rejects conflicting non-empty
parent-tool claims across the complete batch and retained tree before any
publication. This protects both Worker composition and atomic server publication.
The claim remains reserved after child completion; independent original tools
can still own siblings. Codex's existing prohibition on Claude-only parent-tool
fields remains intact.

The subagent contract, project cross-domain invariant and domain/server/Worker
instructions record this ownership requirement in the same change. No wire
reservation, capability, version, storage shape or child-control authority changes.

Domain regressions cover duplicate claims within a batch and against live or
terminal retained children, unchanged prior state on rejection, and distinct-tool
siblings. Worker regressions cover root, nested and completed child claims before
publication. These passed with all matching server/Worker subagent regressions:

```sh
GOMAXPROCS=4 go test -race -p 2 \
  ./cmds/delidev-cli/internal/domain \
  ./cmds/delidev-cli/internal/server \
  ./cmds/delidev-cli/internal/worker \
  -run 'Subagent' -count=1 -timeout=8m
```

Earlier broad failures remain recorded independently. The review thread is
resolved only after the single final repair push succeeds.
