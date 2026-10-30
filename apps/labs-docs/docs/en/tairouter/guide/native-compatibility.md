# Optional native TLS compatibility

## Availability

Optional Claude first-party Desktop/model-picker compatibility and experimental Codex Desktop compatibility are included product modes. They are separate from ordinary [gateway configuration](clients.md), and are not local executable provider backends.

The CLI commands below are implemented and covered by automated runtime/CLI tests. They do not establish real OS trust or installed-application acceptance. The installed build must pass the runtime's own checks; supplying a build number does not bypass attestation.

| Mode                     | Build gate      | Platform gate  | Application workflow                                                              |
| ------------------------ | --------------- | -------------- | --------------------------------------------------------------------------------- |
| `claude-firstparty`      | `1.18286.0`     | macOS, Windows | Configure `claude` after runtime readiness                                        |
| `claude-picker`          | `1.18286.0`     | macOS, Windows | Configure `claude-desktop` after runtime readiness                                |
| `codex-observe`          | `26.924.2738.0` | Windows        | Launch through the ready native runtime                                           |
| `codex-account-ui-trial` | `26.924.2738.0` | Windows        | Launch after verified personal Plus/Pro account checks and separate trial consent |

The account-UI trial is implemented with a maximum duration of three minutes and independent verification of an eligible personal Plus/Pro identity. It changes only bounded UI indicators. Observe mode leaves usage responses unchanged. Neither mode changes billing credit, provider isolation, or server authorization.

## Review before activation

The activation preview must identify the exact mode and installed build, intercepted destinations and protocol paths, loopback listener addresses, certificate fingerprint, trust-store changes, and application-setting changes. Unknown platforms or builds must refuse. Do not bypass a build check or change system-wide proxy settings to make a refused mode work.

Review and confirm certificate trust separately from application mutation. Ordinary connection or client-settings consent does not authorize either. A bounded Codex account-UI trial requires its own explicit confirmation and remains separate from observe behavior. A displayed quota does not establish billing credit, provider isolation, or server authorization.

The runtime must restrict interception to the pinned client routes and destinations, preserve upstream TLS verification, and avoid forwarding native account credentials to TaiRouter. It is not a general-purpose traffic interceptor. The server retains provider selection and usage authority.

## Plan and enable

First create the named connection profile using [the ordinary connection flow](clients.md). The examples use the existing `work` profile. Select one mode for that profile and review the complete plan before enabling it.

For Claude first-party mode:

```sh
tairouter native --action plan --profile work --mode claude-firstparty --build 1.18286.0 --native-port 17844
tairouter native --action enable --profile work --mode claude-firstparty --build 1.18286.0 --native-port 17844
```

Planning can prepare protected certificate material, but does not enroll trust or mutate the application. Enabling requests separate trust and application consent. Consent is bound to the complete displayed plan and certificate; changed profile, key, mode, build, port, or destinations require a fresh matching preview. Do not continue if trust remains pending or an error is reported. Start the listener in a terminal that remains running:

```sh
tairouter native --action serve --profile work
```

From another terminal, check readiness and then preview the application changes:

```sh
tairouter native --action status --profile work
tairouter configure --profile work --client claude --mode claude-firstparty
```

`configure --mode` requires the live runtime's matching readiness. For the Desktop picker workflow, use `claude-picker` in both plan and enable, then `tairouter configure --profile work --client claude-desktop --mode claude-picker`. Close Claude Desktop before confirming its settings changes. Omitting `--mode` selects ordinary gateway configuration; restore existing native settings before switching back.

## Codex observe and launch

On the gated Windows build, use observe mode:

```sh
tairouter native --action plan --profile work --mode codex-observe --build 26.924.2738.0 --native-port 17844
tairouter native --action enable --profile work --mode codex-observe --build 26.924.2738.0 --native-port 17844
tairouter native --action serve --profile work
```

Keep the listener running. In another terminal, check readiness and launch the attested application after reviewing the launch confirmation:

```sh
tairouter native --action status --profile work
tairouter native --action launch --profile work
```

Optional Codex modes use `native --action launch`, not `configure --mode` or a Codex settings mutation. Ordinary `configure --client codex` remains a separate gateway/catalog workflow. Observe mode does not authorize the bounded account-UI trial.

## Bounded Codex account-UI trial

Use a profile prepared for the Codex host constraints and the gated Windows MSIX build. Close the installed application before launch; the runtime refuses an already-running app. To choose the trial instead of observe mode:

```sh
tairouter native --action plan --profile work --mode codex-account-ui-trial --build 26.924.2738.0 --native-port 17844
tairouter native --action enable --profile work --mode codex-account-ui-trial --build 26.924.2738.0 --native-port 17844
```

Review the separate certificate-trust, application, and account-wide trial consent prompts. The runtime checks the installed package/publisher/executable and independently verifies the native personal Plus/Pro account. If verification fails, do not bypass it or assume a trial started. Native account credentials remain in memory and go directly to the pinned native account endpoint over verified TLS; they are not sent to TaiRouter.

After enable succeeds, keep `tairouter native --action serve --profile work` running. In another terminal, run `tairouter native --action status --profile work`, then `tairouter native --action launch --profile work`. The trial lasts at most three minutes. Account, credential, build, consent, and expiry checks can end eligibility earlier. A successful launch or changed UI indicator does not establish real-account acceptance or provider entitlement.

Codex launch uses a verified Windows package and an owned proxy auto-configuration (PAC) URL. It selects only HTTPS traffic for the pinned ChatGPT host; other destinations remain direct. The trial PAC expires, and traffic has a direct fallback after the listener stops. Do not use listener shutdown as a network isolation mechanism. Launch does not create a general system proxy or mutate ordinary Codex TOML settings.

## Trust and local access

The native listener defaults to port 17844, distinct from the ordinary relay default of 17843. Use a distinct explicit `--native-port` for each additional native listener. Listeners bind only to loopback and use fixed ports; conflicts must fail without choosing another port. Keep the runtime's CA private material in protected user storage. Never copy it into a ticket, commit it, log it, or transmit it. Trust changes must be limited to the exact owned certificate in the current user's trust scope. Missing trust is pending activation, not success.

Codex PAC traffic has a narrow local trust exception: CONNECT to its exact pinned host does not use proxy authentication because the application cannot supply proxy credentials through PAC. Other modes use generated proxy credentials. Control operations require separate authentication. Other local processes must therefore be within your selected computer trust boundary.

Status distinguishes enabled, trusted, and ready and redacts proxy authentication. Application settings must not be applied using a listener that has not passed readiness and consent checks. Ordinary gateway behavior must remain available without enrolling optional trust.

## Changing modes

The retained CA is constrained to the selected hosts. Disabling a mode removes owned trust but retains the CA for same-mode reactivation. A mode that needs different host constraints cannot silently replace that authority in the same profile. Use a separate connection profile with distinct relay/native ports for that change; do not delete protected recovery state or broaden certificate trust to force it. This applies when moving between Claude first-party, Claude picker, and Codex host sets.

## Stop, disable, and restore

Use these actions for the existing profile:

```sh
tairouter native --action status --profile work
tairouter native --action stop --profile work
tairouter native --action disable --profile work
```

`status` and `stop` remain available when a settings transaction is interrupted. `disable` requests confirmation, stops the owned listener, restores runtime-owned application settings, and removes owned certificate trust. If an unfinished settings transaction blocks mutation, use `tairouter recover` to review its recovery before retrying disable. Keep conflict backups until recovery is complete.

If the native certificate authority has expired, run `native --action disable` for an enabled profile and confirm that owned trust removal completes. Then run `native --action plan` again. The plan prepares a replacement certificate and shows its new fingerprint without enrolling trust or changing application settings. Review the new plan and provide fresh trust and application consent when enabling. A profile already disabled after successful trust removal can start with a new plan. If removal, ownership verification, or trust inspection fails, preserve the profile's recovery state and resolve the reported conflict before retrying; do not delete certificate files to force renewal. Disabling and reenabling before expiry keeps the existing authority.

Stopping a listener is not the same as removing certificate trust or restoring application settings. Disable/disconnect must identify owned resources, stop owned listeners, offer protected settings restoration, and remove only the owned trust certificate after the required confirmation. Preserve unrelated certificates, proxies, and user changes.

If restoration conflicts, retain recovery backups and reconcile the reported edits. If trust removal or listener shutdown fails, cleanup is incomplete; keep the recovery record and follow the release's explicit recovery instructions. Do not report a successful disconnect as proof that trust was removed when that step failed or was declined. Restore native settings before switching back to gateway configuration.

## Verification boundary

Temporary-home fixtures, mock trust adapters, and loopback tests do not establish actual OS trust, installed application, or live-account acceptance. No such acceptance is claimed here. The ChatGPT app-server shim is outside this workflow.
