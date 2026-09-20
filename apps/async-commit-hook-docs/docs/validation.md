# Final validation

A successful commit is not successful validation. Resolve the final commit and evaluate its gate:

```
git rev-parse HEAD
ach check --commit <exact-sha> --json
```

The latest accepted matching attempt wins, even when pending or unsuccessful. Matching requires exact commit, command/dependency/report/declared-input configuration, public environment values, OS, architecture and shell. This does not prove identical external tools, services or secret values.

Missing, malformed, expired, interrupted or incomplete evidence never passes a required check. A configured report containing failures defeats exit-zero success. Without configured reports, the command's exit status determines its check result. Optional failures remain visible without failing the required-check gate. No applicable checks is not validation.

```
ach failures --run <id> --json
ach logs --run <id> --check test
ach compare --run <id>
ach rerun --run <id> --failed
ach ack --run <id>
```

Failed reruns create a fresh workspace using the original commit/configuration, execute failed and blocked checks with their prerequisites, and identify inherited successful evidence. Original attempts remain intact. Comparison defaults to the previous compatible execution on an earlier commit of the same branch. Explicit `--previous ID` is also supported. Missing details remain unknown.

Acknowledgement is shared by CLI, MCP and web, is explicit and idempotent, accepts completed results only, and never changes validation. Reading does not acknowledge. `ach inbox --repo .` includes pending work and completed results needing review.

## Optional pre-push enforcement

```
ach hooks install --pre-push
```

Every actual branch tip in Git's pre-push input is checked, including multi-ref and non-HEAD pushes. Tags and deletions are excluded. The policy is block (reject incomplete immediately), wait (wait only for an existing unfinished attempt), or run-and-wait (submit a new attempt when none exists or the latest completed attempt does not pass, then wait). Run-and-wait reuses an unfinished attempt and renews failed, cancelled, interrupted, expired, or missing evidence. Block and wait never submit work. A missing result or expired wait never permits a push. New attempts appear under the pushed local branch; pushes from HEAD or an explicit commit use the destination branch. Reused attempts retain their original history, and branch labels do not change commit validation.
