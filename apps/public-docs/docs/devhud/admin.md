# DevHud Administration

The administrator console is for individual accounts with the `devhud-admin` role. Sign-in requires an administrator account.

Administrators can search users with a prefix that is trimmed, normalized to Unicode NFC, fully case-folded, and limited to 512 UTF-8 bytes before submission; block or unblock accounts with a reason; and review typed audit outcomes. When official uploads are available, they can also inspect bounded usage and upload metadata, quarantine official uploads, or permanently delete them. The console must not expose settings bodies, PATs, R2 secrets, issue content, screenshots, browser data, Deck results, agent output, or local paths.

Blocking and account deletion are independent states. Deletion has a 30-day recovery window; permanent cleanup is irreversible after that boundary. Destructive upload operations identify the owner and submission, require a reason, and preserve audit records without exposing public or signed asset locators. Every block, unblock, quarantine, or deletion reason is trimmed at its Unicode edges and normalized to Unicode NFC before validation, submission, and auditing. It must be non-blank, well-formed UTF-8, NUL-free, and no more than 4 KiB when encoded as UTF-8. Reasons containing credentials, configured public asset locators, or local paths are rejected before the operation is submitted.

See [Security](security) for deployment safety and [Support](support) for recovery guidance.
