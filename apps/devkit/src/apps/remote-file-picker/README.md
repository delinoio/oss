# Remote File Picker Mini App

This directory hosts the Devkit mini app with the stable id `remote-file-picker`.

## Route Contract
- `/apps/remote-file-picker`

## Core Responsibilities
- Parse and validate host upload requests.
- Allow users to select a source (cloud drive, local file, mobile camera).
- Upload selected files directly to AWS S3 or GCP Cloud Storage signed URLs.
- Support client-side metadata transforms (format conversion, size compression).
- Return users to the host flow after completion.

## References
- `docs/project-devkit-remote-file-picker.md`
- `docs/project-devkit.md`
