# FixKey Core Foundation Contract

## Scope

Owns the `crates/fixkey-core` crate (component `core`): layout reconstruction, language validation, correction and undo state, platform accessibility adapters, diagnostics events and update verification logic. The crate has no UI rendering dependency. The crate is an explicit workspace member; engine capabilities are added incrementally and the crate's rustdoc lists what is implemented.

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
- Automatic decisions and correction sessions receive directly observed physical keys together with the rendered word. They check that the keys reproduce that word under its current layout before validating candidates. Missing, unsupported or mismatched key provenance skips automatic correction; reverse-inferred keys from rendered text are for manual candidate exploration only. Keep the rendered original for unchanged-winner comparison and immediate undo.
- Process directly typed words only after IME composition has completed.
- Evaluate at spaces and punctuation boundaries. Enter and Tab never trigger evaluation. Separators that can occur inside URLs or email addresses (such as `.`, `/`, `:`, `@`, `?`, `#` and `_`) never trigger evaluation on their own: retain the pending token until the following character or token end allows URL, email and mixed-script classification, and skip the whole token when it is excluded. When a held separator is followed by Enter or Tab, and the target, caret, selection and text still match the captured context at that moment, classify the pending token at that point: if it is not an excluded token, evaluate it once as a trailing-punctuation word against the still-matching captured context (Enter and Tab themselves are never the trigger and are passed through unchanged); otherwise discard it. When the separator is instead terminated by a focus change, a caret or selection change, or a user edit other than the expected next typed character that appends to the held token, the captured context is stale: discard the pending token without evaluation or writes.
- Apostrophes and hyphens between letters are internal word characters. Delay their boundary decision until the next character shows whether they are internal or trailing punctuation.
- Layout reconstruction preserves internal ASCII apostrophes and hyphens at the same positions in English and Korean renderings. Punctuation-only input and leading, repeated, or trailing separators are not Korean-layout words.
- Standalone compound compatibility jamo are unsupported reconstruction input because their key sequences do not reproduce the same standalone characters. Reject a Korean token containing one, while retaining separate simple-jamo sequences and compound jamo inside precomposed syllables.
- Replace only the word and preserve the boundary character. Never change the OS input language.
- Exclude pasted, existing and programmatically inserted text, numeric, emoji, URL, email and mixed-script tokens. Code editors and terminals follow the same rules; eligibility never overrides accessibility safety checks.
- Never reprocess FixKey's own replacements automatically.

Replacement decision:

- Replace only when exactly one enabled language accepts its candidate and that candidate differs from the existing word. Preserve the original when zero or multiple languages accept, or when the winning candidate equals the original.
- If a required enabled-language validator is unavailable or fails, preserve the word. A failed validator is not an invalid result.
- Required examples: `rPthrgo` becomes `계속해`, and `zlqhemrk` becomes `키보드가`.

Validators:

- Korean uses the pinned `hephaex/mecab-ko` project. Acceptance requires complete dictionary-backed analysis with no unknown morphemes, plus an explicit part-of-speech connection rule table covering particle and ending attachment (for example `키보드` + `가`). A complete nominalization may end in ETN; a bare predicate stem or connective ending is incomplete. The rule table has positive and negative fixtures. Tokenizer output alone is never validity. When the selected API does not expose dictionary provenance, obtain it through the analysis and dictionary interfaces; never infer membership from a plausible part-of-speech tag.
- US English uses SCOWL/ESDB-based data with dictionary inflection rules. Compare case-insensitively. Accept proper nouns and abbreviations only when the selected dictionary lists them.
- Dictionaries are bundled so correction works offline from first launch. They update only with application releases. There are no separate dictionary downloads and no user dictionary editing.

### Bundled dictionary resources

- `crates/fixkey-core/assets/manifest.json` pins resource sources, versions, source revisions, build command, licenses and SHA-256 hashes. Rust dependency archive checksums remain in `Cargo.lock`. Preserve `assets/en-us/README_en_US.txt`, `assets/ko/COPYING`, `assets/ko/AUTHORS` and `assets/notices/` in application distributions alongside the dictionary data.
- English data is the official `en-wl/wordlist` US English size-60 Hunspell release `rel-2026.02.25`, loaded through `spellbook` `0.4.2`. Fold dictionary entry surfaces and candidates to lowercase without changing case-sensitive affix flags or morphology fields. Apply the supplied `.aff` rules and each entry's flags; the generic `Lexicon` helper is a fixture interface, not the bundled validator.
- Korean data is the complete `mecab-ko-dic` `2.1.1-20180720` source at `lindera/mecab-ko-dic` revision `12439fb32808b9244ded56d7dfed96e4b8d76869`, compiled with `mecab-ko-dict-builder` `0.7.2`. The selected hephaex MeCab-Ko engine and dictionary reader are pinned to `0.7.2`. Load `sys.dic`, `matrix.bin` and `entries.bin` from an explicit application resource directory; do not discover a user-installed dictionary or consult environment overrides.
- Use the public dictionary and lattice interfaces to retain node provenance and propagate lookup failures. Rank complete dictionary-backed paths with the dictionary's word/connection costs subject to the existing POS start, connection and end rules. A sentence tokenizer may prefer `XSV+EC` for `해`; a separate, dictionary-listed `XSV+EF` analysis is eligible when it completes the word. Do not relabel a connective as sentence-final. If no complete path exists, retain the analyzer's known/unknown evidence for rejection by the validator.
- Hydrate the three exact-path LFS resources before full-dictionary tests or packaging. Source regeneration requires the pinned dictionary checkout, including its LFS `matrix.def`, and the pinned builder. Copy only the three runtime files, preserve notices, and update hashes when deliberately changing resources. No runtime dictionary downloads occur.

Correction and undo state:

- Keep one active correction context per user session. Never share input state across users or applications.
- Before applying a result, verify that target, caret, selection and relevant text still match the captured context. Discard stale results after edits, caret, selection or focus changes.
- Immediately after an automatic correction, the first Backspace restores the original word and the complete boundary. A key observation before the host handles Backspace only marks undo pending; an unchanged snapshot must not cause a write. After the host deletes the boundary's final character, a fresh accessibility observation can complete the pending undo. A Backspace observation made after host deletion can complete it directly. The input observer never suppresses or replaces the host key. Later Backspace presses behave normally.
- When undo follows host deletion, replace the corrected word and any surviving boundary prefix together. Preserve text before and after that range. A matching observation of the engine's restored text and caret retains recorrection suppression; an external context change clears it. The restored word is not recorrected.
- Clear transient state when target, caret or selection changes, except for the caret and text transitions caused by the engine's own correction or restoration and the expected host deletion during pending undo. Clear it for other external edits, when the next word starts, correction is paused or permission is lost. Keep only the state needed for the current word and immediate restoration, in memory only.
- Recheck sensitivity, target, selection, text, caret, permission and safe-write support before undo. Do not retry failed corrections automatically. On a partial correction or undo write, make at most one recovery write only when the current target, text, caret and safety state match the expected intermediate state; otherwise stop without further writes. A non-partial write failure causes no recovery write.

Input-event adapter:

- Directly typed words, their key sequence and their provenance come from a per-platform input-event adapter behind a trait, separate from the accessibility adapter. It is a listen-only observer of key events for the current OS user session; it never injects, modifies or suppresses events.
- The adapter reports each event with its physical key, modifier state and whether it is a typed key, or a paste or programmatic insertion signal. When provenance cannot be established, treat the text as not directly typed and skip it (`pasted` or `programmatic`).
- It runs only while correction is active, permissions are granted, and the focused control is classified as editable and not protected. Key events from protected, unknown or excluded targets are discarded without buffering. Buffered keys exist only in memory for the current word, are cleared at the context and permission boundaries, and are never logged. Missing input-monitoring permission is reported through application status and keeps correction inactive.

Accessibility adapters:

- Use platform accessibility APIs to identify the focused editable control, inspect the relevant text range and replace it. Never read or correct password or protected fields.
- When permission, field sensitivity, target identity, text range or safe write support cannot be established, do not read or correct the input.
- Never use clipboard replacement or synthetic typing as a fallback.
- At an observed word boundary, the desktop worker may wait up to 100 ms for the host accessibility snapshot to commit the exact observed word and boundary. Only matching committed prefixes with a non-retreating caret may progress; unrelated text, target, selection or safety changes, read errors and timeouts abort before any write. This is a bounded read wait, never a correction retry.
- macOS canonicalizes only a lone `\n` value with caret zero and no selection as an empty paragraph. Some Chromium editors remove this placeholder on the first physical insertion. Never trim nonempty values, multiple newlines, a selected newline or a lone newline with a nonzero caret. A real newline retained after typing must fail the complete insertion proof against the canonical empty baseline. Native range conversion still validates the original value before normalization; replacement revalidation uses the complete committed field.
- For a classified macOS text control that supports selection and selected-text writes, prefer writable `AXValue` for a single native text mutation using the exact verified prefix, replacement and suffix; otherwise use `AXSelectedText`. Select the setter before writing, never retry with another setter after an uncertain result. Chromium hosts may advertise selected-text writes that do not mutate content. A value write may collapse the native selection to offset zero when editable text nodes are rebuilt; accept that exact whole-field result only for a value write, then restore and verify the intended insertion caret. Require exact whole-field text and caret read-back; there is no clipboard or keyboard injection. Native rich-format preservation is not established by plain-text snapshot verification.
- macOS AX selection, selected-text replacement and caret writes may publish their read-back state asynchronously. After each single write, wait at most 100 ms for an exact expected snapshot. While waiting, accept only the exact previous snapshot; stop on target, text, selection, sensitivity or write-capability changes. Never repeat the native write to obtain acknowledgment. Unsupported native setters remain unsupported; this does not authorize terminal input injection. Log only content-free capability flags and stable write-failure stages.
- Expose unsupported-control and failure reasons as stable `SkipReason` values for application status.

Manual candidates:

- A candidate list contains the original first and exactly once, even when the original's layout is disabled. Each other candidate comes from an enabled language and keeps its validation result. A layout-convertible but invalid candidate is selectable and labeled as not recognized by the dictionary.
- Each candidate carries a closed validation state:

```ts
enum CandidateValidation {
  Recognized = "recognized",
  NotRecognized = "not-recognized",
  ValidatorUnavailable = "validator-unavailable",
}
```

`NotRecognized` candidates are selectable and labeled as not recognized by the dictionary. `ValidatorUnavailable` candidates are shown with a distinct "could not be checked" label, never as not recognized, and are selectable explicitly. The label never relies on color alone.

- Revalidate the target before applying a manual choice. Switching applications or changing the target invalidates the list.
- The core manual session owns one explicit word range immediately before the source caret, its candidate selection and its captured source snapshot. Keyboard navigation wraps through every displayed candidate. Choosing the original or closing the session performs no write. Applying consumes the session before revalidation, so stale targets and failures cannot trigger a later retry. Partial writes use the same guarded recovery rule as automatic correction.

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
- `cargo run -p fixkey-core --example dictionary-profile` measures initialization of the bundled dictionaries and repeated fixture decision latency without native input access. Measure the compiled example's process memory separately with the platform's process profiler. These measurements cover the dictionary engine; they do not establish desktop startup time, real-user accuracy or native compatibility.
- Mocks, fixtures and builds do not establish actual accessibility-adapter behavior on any platform. Native acceptance of the adapters on each target platform is a separate activity required before claiming verified compatibility or shipping, as defined in [the desktop contract](apps-fixkey-desktop-contract.md). Record every native check or package inspection that was not performed in pull requests, issues and CI artifacts.

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

## Native adapter boundaries

The optional `native` feature exposes `platform::Adapter`. `observation.rs` proves completed-word insertions and bounds unfinished physical input to a single word; the desktop worker discards words longer than 256 observed keys. Matching committed prefixes may wait for IME completion, but never authorize correction. Completion requires the entire observed word and separator to match the committed field. macOS uses a listen-only Core Graphics event tap and AX text ranges. Windows uses a listen-only low-level keyboard hook and UI Automation Edit controls with writable ValuePattern plus verifiable TextPattern selections. Linux accepts local X11 sessions and local Unix accessibility buses only, correlates the active window process and WM_CLASS with the AT-SPI bus, resolves explicit GTK/KDE application hints against installed XDG desktop entries (with WM_CLASS-only fallback when unavailable or ambiguous), and requires focused non-password Text/Entry objects with EditableText. Linux hardware input requires a current slave keyboard with a device node; XTEST and untrusted events invalidate input provenance.

Content-free cursor markers expose caret and selection changes without fetching control values: AX ranges on macOS, UIA native range comparisons on Windows, and AT-SPI offsets on Linux. Classification and safe-write capability checks precede text reads. The caller explicitly restricts text reads to its approved application identity. Adapters repeat that identity check on the captured control before reading text. Target tokens use native equality instead of relying on a hash alone. macOS AX access and retained targets remain on the main thread; Windows COM ownership remains on its initializing thread. Linux writes use character-range deletion/insertion, with UTF-8 byte lengths for inserted text and a checked intermediate snapshot. Only a proven deletion permits the shared one-attempt recovery. Each adapter captures native target identity, verifies the complete snapshot again before writing, preserves the untouched prefix/suffix and boundary, restores the insertion caret and verifies the result. Uncertain native write failures permit no recovery write. Only a proven partial result can enter the shared guarded recovery path. UTF-16 conversions reject offsets inside surrogate pairs. No adapter changes the system layout, clipboard or injects input.

Native dependencies are target-scoped and pinned in Cargo manifests and the workspace lockfile. Windows UI Automation uses `uiautomation` with `uiautomation_derive` constrained to the workspace-compatible release; its upstream unconditional MouseButton import requires compiling its input module, but FixKey calls no input-generation APIs. Linux uses `zbus` for local accessibility IPC and `x11rb` for X11 metadata and event observation. Native application behavior requires separate platform acceptance; fixtures and cross-compilation cannot establish it.
