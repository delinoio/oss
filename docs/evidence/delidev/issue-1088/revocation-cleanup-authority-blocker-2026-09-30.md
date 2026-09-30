# Revoked terminal Worker cleanup authority

Inspected terminal revision: `130be83f1235093890ffd6211de3ae105dcb73c2`, PR #1226. Review thread `PRRT_kwDORRAKg86nk487` remains unresolved.

The finding is actionable. `RevokeDevice` disables the machine, revokes its device credential and marks claimed terminals uncertain without confirming cleanup. `terminalMachine` requires current active machine/instance authority, and `ClaimTerminal` also binds the original machine and device. Fresh Worker pairing generates a new machine UUID in `worker/pair.go`; server pairing cannot replace an existing machine through the expected-zero creation path. Removing just the close device-ID comparison therefore cannot restore a usable cleanup path.

Existing contracts require immediate revocation, original process ownership, no input/shell replay, and uncertainty when native evidence is missing. The current protocol has no explicit cross-registration cleanup handoff. The two completed review repairs preserve shutdown loss and retire acknowledged ownership; neither constitutes revocation recovery.

A material authority decision is pending:

- Recommended: an explicit owner-authorized cleanup-only handoff binds the original terminal/machine/device, a newly paired Worker and the original retained private process journals. Keep the revoked credential disabled, preserve original process/operation identity, require fresh replacement claim/report identities, and authorize close reconciliation only. Do not transfer input/output, shell creation, agent, forwarding or workspace execution authority. Any new shared wire-number reservations must first be established on main under the structure contract.
- Alternative: a brief independently bounded cleanup-only admission for the original revoked credential, limited to its exact original closes and reports, never ordinary Worker/client operations. This changes immediate credential-revocation policy and needs an explicit decision on duration and renewal.

The owner was asked to choose between these authority boundaries. No credential was re-enabled, no original machine/device binding was weakened, no review was dismissed as incorrect, and no additional branch or PR was created. Maintenance should not repeat this blocked repair without the decision or new evidence establishing an authorized recovery path.
