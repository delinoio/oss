# DevHud Administration

The administrator console is for individual accounts with the `devhud-admin` role. Sign-in uses the configured Logto administrator client and exact redirect; the API independently enforces the role.

Administrators can search users, inspect bounded usage, block or unblock accounts with a reason, inspect upload metadata, quarantine or permanently delete official uploads, and review typed audit outcomes. The console must not expose settings bodies, PATs, R2 secrets, issue content, screenshots, browser data, Deck results, agent output, or local paths.

Blocking and account deletion are independent states. Deletion has a 30-day recovery window; permanent cleanup is irreversible after that boundary. Destructive upload operations identify the owner and submission, require a reason, and preserve audit records without exposing public or signed asset locators.

See [Security](security) for deployment safety and [Support](support) for recovery guidance.
