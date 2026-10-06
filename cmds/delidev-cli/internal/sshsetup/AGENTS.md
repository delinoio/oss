# SSH Worker setup ownership

Follow `docs/cmds-delidev-ssh-setup-contract.md` and parent rules. Observe host keys without authentication; exact explicit confirmation is required before the closed command path. Never inherit SSH agents/config/known_hosts, emit raw SSH output or accept renderer/CLI shell strings. Protected credentials and encrypted stdin remain separate from operation metadata. Join all transport/session cancellation callbacks and preserve uncertainty after any remote effect send. Repeated setup must inspect the original registration and cannot reset private roots or workspaces.

Install each command's child-context cancellation before session creation. Close and join the owned transport on expiry, keep cancellation active through session cleanup, and join the callback before returning. Preserve typed cancellation errors and Stage/Setup recovery requirements after potentially sent effects; never replay effects automatically.
