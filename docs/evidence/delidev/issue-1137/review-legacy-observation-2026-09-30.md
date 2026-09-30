# Issue #1137 review: live legacy readiness observation

Codex thread `PRRT_kwDORRAKg86nhG2_` on PR #1228 identifies that desktop launch
can reuse an authenticated legacy server without lifecycle evidence, but its next
read-only observation returns stopped before probing that live listener.

Desktop observation now distinguishes absent lifecycle evidence from explicit
stopped intent. It probes compatible live legacy authority without writing
restart configuration, validates the configured listener and exact endpoint, and
returns unavailable after that legacy server exits. Automatic ensure still
requires original running intent; explicit stopped intent retains suppression.
CLI instructions and the desktop contract document the distinction.

The race-enabled legacy-listener and launch/reuse/Stop regressions pass in 4.007
seconds. They cover incompatible legacy listener rejection on observation, three
repeated authenticated observations preserving absent lifecycle and exact endpoint
bytes, and unavailable legacy observation without restart or intent publication.
The legacy fixture also confirms ensure remains stopped after the server exits.
