# Workflow contracts

- DeliDev native packaging follows `docs/apps-delidev-packaging-contract.md` and the repository workflow contract. Keep its dry run manually dispatched, limited to `contents: read`, LFS-hydrated and free of production signing, notarization, publication credentials and stored checkout tokens.
- DeliDev uses the one six-target native matrix exported by its package tool. Verify packages before uploading revision-bound workflow artifacts; never count static package inspection as native runtime, production-signing or release acceptance. Update the workflow contract tests and evidence ledger with boundary changes.
