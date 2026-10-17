# cmds/delidev-cli/internal/harness/claude working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/internal/harness/claude/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

Follow parent instructions and the harness/subscription contracts in `docs/`.

## Owning contracts

- [DeliDev account lifecycle](../../../../../docs/cmds-delidev-accounts-contract.md)
- [DeliDev native harness adapter contract](../../../../../docs/cmds-delidev-harness-contract.md)
- [DeliDev native subscriptions](../../../../../docs/cmds-delidev-subscription-contract.md)

## Setup-token and managed token execution

`setup_token.go` and `managed_token.go` own the server-owned token profile. Keep the setup-token URL profile separate from auth-login, never log or retain the token or approval code, never pass `--bare`, and keep the contract in the harness and subscription docs aligned.

## Usage profile

`usage_profile.go` owns the exclusive usage-tracking profile adapter. It may read and remove only the native credential storage derived from its own usage profile (the profile credential file and, on macOS, the exact derived Keychain item); never enumerate or adopt a personal, legacy execution or external profile, never log credentials, identities or paths, keep the Keychain derivation in one function, and fail closed on ambiguous, foreign or unsupported storage. The lifecycle contract lives in the subscription and harness docs.
