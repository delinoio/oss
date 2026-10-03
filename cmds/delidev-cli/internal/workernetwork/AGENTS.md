# DeliDev encrypted Worker network ownership

- Follow `docs/cmds-delidev-network-contract.md` and parent instructions. This package owns bounded X25519-only transfer and the protected derivative cache, not authoritative profile editing, pairing admission or native execution.
- Import requires the separately obtained ciphertext digest plus independently selected exact server, endpoint, machine, device, pairing and original recipient/key identity. Encryption alone cannot authenticate an export.
- Keep recipient private keys and derivative contents in the existing OS-key-wrapped credential Vault. Ordinary metadata contains public recipient/reference, scope, generation and ciphertext digest only. No plaintext fallback, key regeneration after uncertain publication, ambient route discovery, older-profile fallback or offline readiness.
- Preserve atomic complete cache publication and monotonic generation; same-generation conflicting authority is recovery-required. Native runtimes retain independent bounded Go copies while new control attempts adopt the newly reconciled cache.
- Tests inject isolated protected stores and temporary state. Never read user credentials or access real proxy infrastructure.
