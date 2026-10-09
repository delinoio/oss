# Image input storage ownership

- Follow the parent instructions and `docs/cmds-delidev-image-input-contract.md`.
- Store original image bytes only in private Worker-owned storage. Caller paths, filenames and native event paths grant no authority. Resolve native paths through the original closed reference, paired Runner and validated bounded content.
- Synchronize immutable journals, data writes and deletion tombstones before reporting success. Identical chunks may replay; conflicting bytes or ownership fail. Deletion receipts prevent delayed writes from recreating bytes.
- Retained deletion proof is read-only. Reappeared files, symlinks and changed receipts fail proof and remain untouched. Do not infer cleanup from a missing journal alone.
