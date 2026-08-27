# DevHud Support

## Troubleshooting

- If sign-in fails, check the HTTPS origin, configured Logto issuer, callback, system clock, and permission to store state and nonce data.
- If GitHub actions fail, confirm the intended PAT profile is selected, the repository is valid, and GitHub has not rate-limited the request.
- If capture is unavailable, grant screen-capture permission, use a supported desktop target, and retry. Accessibility or Input Monitoring permissions are needed only when registering a global shortcut. Mobile and native Wayland do not provide RealQA capture.
- If Chrome pairing fails, restart pairing in desktop Settings and enter the newly displayed one-time code in the extension. Approve only the displayed configured origin if prompted, or select the repository manually as a fallback.
- If a Deck or widget is stale, bring the app active and check connectivity and GitHub rate status. Suspended widgets refresh on an OS-controlled schedule.
- If logout or deletion reports incomplete cleanup, retry the action; the session remains available only to finish the contracted cleanup or recovery flow.

## Uninstall

Use the operating system's normal uninstall path. Unpair Chrome first when possible and disable widgets separately. On Debian, unpair every affected Chrome user or make every affected user's session active before retrying removal; otherwise removal aborts before deleting registration or binaries. Uninstalling the app does not remove GitHub issues or public images already shared. Public image-removal requests are not currently accepted because no public intake exists yet; do not send credentials.

## Help and reports

Include the app version, platform, and stable error code, but never include credentials, tokens, signed URLs, screenshots, issue bodies, browser DOM, prompts, or local paths. [Security](security) explains vulnerability reporting.
