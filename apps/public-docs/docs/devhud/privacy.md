# DevHud Privacy

DevHud is designed around local-first capture and explicit sharing. It does not use product analytics, remote feature flags, or a plugin SDK.

## What is stored

PATs, R2 access keys, Logto tokens, pairing secrets, permissions, drafts, caches, prompts, and API-origin details are device-local. PAT values never enter synchronized settings or requests to the DevHud API. A selected PAT is used for direct GitHub requests. For authenticated users, synchronized settings include appearance, complete Deck names and queries, repository and issue-tracker configuration, URL mappings, agent configuration, upload-provider selection, non-secret R2 metadata, and bounded non-secret profile descriptors and references. Credentials and other secrets remain device-local.

Unsubmitted RealQA drafts are authenticated-encrypted and retained for 30 days from their last successful save, subject to the default 10 GiB local quota. Successful issue creation, explicit deletion, logout, or expiry removes them. Diagnostics are redacted, opt-in for submission, and accepted crash reports are retained for 30 days; local diagnostic events are retained for at most 7 days.

## Public images

An image attached to a published issue may be publicly retrievable. Do not capture credentials, private data, or secrets. Official image removal is a supported administrative operation, but CDN and browser caches may take time to invalidate. Public image-removal requests are not currently accepted; this process remains a placeholder until a dedicated intake form is published. Do not send credentials.

## Account deletion and recovery

Deletion purges PATs, R2 credentials, and other account-local state while blocking access immediately; the current Logto recovery session remains available to the verified owner. Account restoration is available to the verified owner for 30 days. After that period, synchronized data and upload metadata are purged or irreversibly pseudonymized at the documented audit boundary. Official image objects are deleted and public CDN copies are invalidated; image bytes are not retained as pseudonymized data. A successful restore cannot follow final purge.

## Diagnostics

Preview shows the exact redacted export before consent. Exports never confirm an arbitrary filesystem path; crash submission is unavailable to guests and blocked or deletion-pending accounts. Do not include issue bodies, screenshots, DOM, prompts, credentials, or local paths in diagnostic text.

For security reports, use [Security](security). For account or image-removal help, use [Support](support).
