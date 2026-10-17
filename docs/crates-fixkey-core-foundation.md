# FixKey Core Foundation Contract

## Scope

Owns the planned `crates/fixkey-core` crate (component `core`): layout reconstruction, language validation, correction and undo state, platform accessibility adapters, diagnostics events and update verification logic. The crate has no UI rendering dependency. The path is `planned` until its skeleton and workspace membership are created together with the first runtime implementation.

## Runtime and Language

Rust. Platform accessibility adapters sit behind traits so the engine runs against mocks in tests. Target platforms: `macos-arm64`, `windows-x64`, `linux-x11-x64`. Minimum OS requirements are derived from the pinned dependencies and documented when dependencies are pinned. Actual platform and application compatibility is unverified.

## Users and Operators

- End users who type Korean and English on one keyboard.
- The `desktop` component, which hosts the engine and renders status and settings.
- Maintainers who update pinned dictionaries and morphology data with application releases.

## Interfaces and Contracts

Stable identifiers:

```ts
enum Language {
  KoDubeolsik = "ko-dubeolsik",
  EnUsQwerty = "en-us-qwerty",
}

enum CorrectionOutcome {
  Replaced = "replaced",
  PreservedNoMatch = "preserved-no-match",
  PreservedAmbiguous = "preserved-ambiguous",
  PreservedUnchanged = "preserved-unchanged",
  PreservedSkipped = "preserved-skipped",
  PreservedValidatorUnavailable = "preserved-validator-unavailable",
  PreservedStaleContext = "preserved-stale-context",
}

enum SkipReason {
  Composing = "composing",
  Pasted = "pasted",
  Programmatic = "programmatic",
  Numeric = "numeric",
  Emoji = "emoji",
  Url = "url",
  Email = "email",
  MixedScript = "mixed-script",
  ProtectedField = "protected-field",
  UnknownField = "unknown-field",
  PermissionMissing = "permission-missing",
  UnsupportedWrite = "unsupported-write",
  ExcludedApp = "excluded-app",
  Paused = "paused",
  SelfReplacement = "self-replacement",
  RestoredWord = "restored-word",
}
```

Layout reconstruction:

- Reconstruct each completed word from its key sequence under every enabled language, including the word's current representation. Support English-to-Korean and Korean-to-English. Preserve the casing implied by the key sequence, including shifted keys.
- Process directly typed words only after IME composition has completed.
- Evaluate at spaces and punctuation boundaries. Enter and Tab never trigger evaluation.
- Apostrophes and hyphens between letters are internal word characters. Delay their boundary decision until the next character shows whether they are internal or trailing punctuation.
- Replace only the word and preserve the boundary character. Never change the OS input language.
- Exclude pasted, existing and programmatically inserted text, numeric, emoji, URL, email and mixed-script tokens. Code editors and terminals follow the same rules; eligibility never overrides accessibility safety checks.
- Never reprocess FixKey's own replacements automatically.

Replacement decision:

- Replace only when exactly one enabled language accepts its candidate and that candidate differs from the existing word. Preserve the original when zero or multiple languages accept, or when the winning candidate equals the original.
- If a required enabled-language validator is unavailable or fails, preserve the word. A failed validator is not an invalid result.
- Required examples: `rPthrgo` becomes `계속해`, and `zlqhemrk` becomes `키보드가`.

Validators:

- Korean uses the pinned `hephaex/mecab-ko` project. Acceptance requires complete dictionary-backed analysis with no unknown morphemes, plus an explicit part-of-speech connection rule table covering particle and ending attachment (for example `키보드` + `가`). The rule table has positive and negative fixtures. Tokenizer output alone is never validity. When the selected API does not expose dictionary provenance, obtain it through the analysis and dictionary interfaces; never infer membership from a plausible part-of-speech tag.
- US English uses SCOWL/ESDB-based data with dictionary inflection rules. Compare case-insensitively. Accept proper nouns and abbreviations only when the selected dictionary lists them.
- Dictionaries are bundled so correction works offline from first launch. They update only with application releases. There are no separate dictionary downloads and no user dictionary editing.

Correction and undo state:

- Keep one active correction context per user session. Never share input state across users or applications.
- Before applying a result, verify that target, caret, selection and relevant text still match the captured context. Discard stale results after edits, caret, selection or focus changes.
- Immediately after an automatic correction, the first Backspace restores the original word and preserves the boundary character. Later Backspace presses behave normally. The restored word is not recorrected.
- Clear transient state when target, caret or selection changes, the next word starts, correction is paused or permission is lost. Keep only the state needed for the current word and immediate restoration, in memory only.
- Do not retry failed corrections automatically. On partial replacement failure, restore the original only when the current target and text still match the expected intermediate state; otherwise stop without further writes.

Accessibility adapters:

- Use platform accessibility APIs to identify the focused editable control, inspect the relevant text range and replace it. Never read or correct password or protected fields.
- When permission, field sensitivity, target identity, text range or safe write support cannot be established, do not read or correct the input.
- Never use clipboard replacement or synthetic typing as a fallback.
- Expose unsupported-control and failure reasons as stable `SkipReason` values for application status.

Manual candidates:

- A candidate list contains the original and each enabled-language candidate with its validation result. A layout-convertible but invalid candidate is selectable and labeled as not recognized by the dictionary.
- Revalidate the target before applying a manual choice. Switching applications or changing the target invalidates the list.

## Storage

The crate persists nothing. Settings storage belongs to the `desktop` component. Correction and undo text lives in memory only for the active context.

## Security

- Never persist or transmit input text, candidates, document content or window titles.
- Fail closed: unknown fields, missing permissions, unsupported writes and stale targets cause safe refusal.
- Update verification rejects failed downloads and invalid signatures without installation. Signing identities and release credentials are external release dependencies and are never committed.

## Logging

Use `tracing` structured events for errors, processing durations, correction outcomes and skip reasons, without input content. Include application version, platform and stable error or reason codes. CLI and operator-facing logs use ANSI color by default with a documented opt-out. Log retention is bounded by the `desktop` component contract.

## Build and Test

- Run `cargo test` from the repository root, plus `cargo fmt --all --check` and `cargo clippy --workspace --all-targets --all-features -- -D warnings`.
- Required automated tests: positive examples, reverse conversion, shifted keys, casing, dictionary inflections, proper nouns and abbreviations; unknown morphemes, invalid part-of-speech connections, incomplete analysis, missing dictionaries and validator failures; zero, one and multiple valid languages, disabled languages and unchanged winners; boundaries, apostrophes and hyphens, and excluded Enter and Tab; composition, pasted and programmatic input, numeric, emoji, URL, email and mixed-script tokens; Backspace restoration, separator preservation and recorrection suppression; mocked focus, caret, selection, permission, protected-field, unsupported-write and partial-write failures.
- Measure processing latency, memory use and startup duration without fixed numerical release thresholds.
- Actual OS and application execution and installation-package inspection are not part of validation. Record them as unperformed.

## Dependencies and Integrations

- `hephaex/mecab-ko` and SCOWL/ESDB data: pin versions and record source, hash, license and notices.
- Platform accessibility APIs for macOS, Windows and Linux X11.
- Consumed by `apps/fixkey`; the engine stays independent of UI rendering.

## Change Triggers

Update [project-fixkey.md](project-fixkey.md) and [the desktop contract](apps-fixkey-desktop-contract.md) when identifiers, decision rules, safety rules or dictionary sources change. Update `crates/AGENTS.md` and root `AGENTS.md` only for ownership or development-rule changes.

## References

- [Project index](project-fixkey.md)
- [Desktop application contract](apps-fixkey-desktop-contract.md)
- [Repository defaults](repository-defaults.md)
- [Repository license](repository-license-contract.md)
