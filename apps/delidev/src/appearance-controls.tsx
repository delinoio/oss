// SPDX-License-Identifier: Apache-2.0
import { consumeFormSubmit, guardEnterSubmission } from "./form-enter";
import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { newRequestId } from "@delinoio/delidev-api-client";
import { useEntranceActivation } from "./entrance-motion";
import { Modal } from "./ui";
import { copy, useLocale, type MessageKey } from "./localization";
import { AppearanceOperation, useAppearance } from "./appearance";
import {
  appearanceIdentity,
  colorTokens,
  defaultPreferences,
  palettes,
  Palette,
  Layout,
  StatusPresentation,
  ImageSize,
  DisclosureDefault,
  parseThemeFile,
  validColors,
  validTheme,
  type AppearancePreferences,
  type CustomTheme,
  type ThemeFile,
} from "./appearance-preferences";
import {
  SettingsActionButton,
  SettingsActionIcon,
  SettingsActionTone,
} from "./settings-action";
const label = (key: string) => copy(`appearance.v2.${key}` as MessageKey);
// The editor draft carries its UUIDv7 id, so it validates with the identity
// shape; the strict no-ID shape belongs to theme file import and export.
export function ColorPreview({ draft }: { draft: CustomTheme }) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (
      typeof CSSStyleSheet.prototype.replaceSync !== "function" ||
      !validTheme(draft, true)
    )
      return;
    const sheets = ["light", "dark"].map((mode) => {
      const sheet = new CSSStyleSheet();
      sheet.replaceSync(`.appearance-preview-${mode} {}`);
      const rule = sheet.cssRules[0] as CSSStyleRule;
      for (const [token, value] of Object.entries(
        draft[mode as "light" | "dark"],
      ))
        rule.style.setProperty(`--${token}`, value);
      return sheet;
    });
    document.adoptedStyleSheets = [...document.adoptedStyleSheets, ...sheets];
    return () => {
      document.adoptedStyleSheets = document.adoptedStyleSheets.filter(
        (v) => !sheets.includes(v),
      );
    };
  }, [draft]);
  return (
    <div ref={ref} className="appearance-preview" aria-label={label("preview")}>
      {["light", "dark"].map((mode) => (
        <div key={mode} className={`appearance-preview-${mode}`}>
          <h4>{label(mode)}</h4>
          <p>{label("synthetic")}</p>
          <p className="appearance-preview-selected">
            {label("exampleSelection")}
          </p>
          <button type="button" disabled>
            {label("exampleControl")}
          </button>
        </div>
      ))}
    </div>
  );
}
function ThemeEditor({
  initial,
  revision,
  close,
}: {
  initial: CustomTheme;
  revision: number;
  close: () => void;
}) {
  const { snapshot, operation, update } = useAppearance();
  const [draft, setDraft] = useState(initial),
    [error, setError] = useState(false),
    // The exact theme passed to `update`. It is never mutated, so completion
    // reconciles against what was requested, not against later draft state.
    [submitted, setSubmitted] = useState<CustomTheme>();
  // Name and color edits are frozen while the submitted save is in flight.
  const saving =
    submitted !== undefined && operation === AppearanceOperation.Saving;
  // One explicit editor opening owns one entrance; drafts, saves and refetches
  // never restart it, and it never delays focus or dismissal.
  const entrance = useEntranceActivation(initial.id);
  const changed = snapshot.revision !== revision;
  useEffect(() => {
    if (
      submitted &&
      snapshot.problem === null &&
      snapshot.revision > revision &&
      snapshot.preferences?.custom_themes.some(
        (t) => appearanceIdentity(t) === appearanceIdentity(submitted),
      )
    )
      close();
  }, [submitted, snapshot, revision, close]);
  return (
    <Modal title={label("editTheme")} close={close} entrance={entrance}>
      <form
        onKeyDown={guardEnterSubmission}
        onSubmit={(event) => {
          if (!consumeFormSubmit(event)) return;
          if (!validTheme(draft, true)) {
            setError(true);
            return;
          }
          if (changed || snapshot.problem || operation) return;
          const p = snapshot.preferences ?? defaultPreferences();
          const custom = p.custom_themes.filter((t) => t.id !== draft.id);
          if (custom.length >= 32) {
            setError(true);
            return;
          }
          setSubmitted(draft);
          update({ ...p, custom_themes: [...custom, draft] }, revision);
        }}
      >
        <label>
          {label("themeName")}
          <input
            aria-invalid={
              error && (!draft.name.trim() || [...draft.name].length > 80)
            }
            value={draft.name}
            disabled={saving}
            onChange={(event) => {
              setError(false);
              setDraft({ ...draft, name: event.target.value });
            }}
          />
        </label>
        <ColorPreview draft={draft} />
        <p>{label("tokenHelp")}</p>
        <div className="appearance-token-grid">
          {colorTokens.map((token) => (
            <fieldset key={token}>
              <legend>{token}</legend>
              {(["light", "dark"] as const).map((mode) => (
                <label key={mode}>
                  {label(mode)}
                  <input
                    aria-invalid={
                      error &&
                      (!/^#[0-9a-fA-F]{6}$/.test(draft[mode][token]) ||
                        !validColors(draft[mode]))
                    }
                    value={draft[mode][token]}
                    disabled={saving}
                    aria-label={`${label(mode)} ${token}`}
                    onChange={(event) => {
                      setError(false);
                      setDraft({
                        ...draft,
                        [mode]: { ...draft[mode], [token]: event.target.value },
                      });
                    }}
                  />
                </label>
              ))}
              {error &&
              (!validColors(draft.light) || !validColors(draft.dark)) ? (
                <p role="alert">{label("invalidTheme")}</p>
              ) : null}
            </fieldset>
          ))}
        </div>
        {error ? <p role="alert">{label("invalidTheme")}</p> : null}
        {changed ? <p role="alert">{label("draftConflict")}</p> : null}
        <div className="appearance-actions">
          <SettingsActionButton
            icon={SettingsActionIcon.Cancel}
            type="button"
            onClick={close}
          >
            {label("cancel")}
          </SettingsActionButton>
          <SettingsActionButton
            icon={SettingsActionIcon.Save}
            type="submit"
            disabled={changed || !!snapshot.problem || !!operation}
          >
            {label("saveTheme")}
          </SettingsActionButton>
        </div>
      </form>
    </Modal>
  );
}
export function AppearanceControls({
  modeControls,
}: {
  modeControls: ReactNode;
}) {
  useLocale();
  const { snapshot, operation, update } = useAppearance();
  const p = snapshot.preferences ?? defaultPreferences();
  const blocked = !!operation || !!snapshot.problem;
  const rowId = useId();
  const text = (key: string) => (
    <span className="appearance-row-text">
      <span id={`${rowId}-${key}-label`}>{label(key)}</span>
      <small id={`${rowId}-${key}-help`}>{label(`help.${key}`)}</small>
    </span>
  );
  const [editor, setEditor] = useState<{
      theme: CustomTheme;
      revision: number;
    }>(),
    [removal, setRemoval] = useState<{ id: string; revision: number }>(),
    [importing, setImporting] = useState(false),
    [importRevision, setImportRevision] = useState(0),
    [importText, setImportText] = useState(""),
    [importError, setImportError] = useState(false);
  const change = <K extends keyof AppearancePreferences>(
    key: K,
    value: AppearancePreferences[K],
  ) => update({ ...p, [key]: value }, snapshot.revision);
  const checkbox = (key: keyof AppearancePreferences) => (
    <label className="appearance-row">
      {text(key)}
      <input
        aria-labelledby={`${rowId}-${key}-label`}
        aria-describedby={`${rowId}-${key}-help`}
        type="checkbox"
        checked={p[key] === true}
        onChange={(e) => change(key, e.target.checked as never)}
      />
    </label>
  );
  const select = <K extends keyof AppearancePreferences>(
    key: K,
    values: readonly (string | number)[],
    grouped = false,
  ) => (
    <label
      className={grouped ? "appearance-disclosure-option" : "appearance-row"}
    >
      {text(key)}
      <select
        aria-labelledby={`${rowId}-${key}-label`}
        aria-describedby={`${rowId}-${key}-help`}
        value={String(p[key])}
        onChange={(e) =>
          change(
            key,
            (typeof p[key] === "number"
              ? Number(e.target.value)
              : e.target.value) as AppearancePreferences[K],
          )
        }
      >
        {values.map((v) => (
          <option key={v} value={v}>
            {typeof v === "number"
              ? v === 0
                ? label("default")
                : `${v}px`
              : label(
                  v === "original" && String(key).endsWith("_disclosure")
                    ? "default"
                    : v,
                )}
          </option>
        ))}
      </select>
    </label>
  );
  const section = (name: string, children: ReactNode) => (
    <fieldset
      data-settings-search-target={name.toLowerCase()}
      aria-describedby={`${rowId}-${name}-section-help`}
      disabled={blocked}
    >
      <legend>{label(name)}</legend>
      <p
        id={`${rowId}-${name}-section-help`}
        className="appearance-section-help"
      >
        {label(`help.${name}Section`)}
      </p>
      {children}
    </fieldset>
  );
  const duplicate = (theme: ThemeFile) =>
    setEditor({
      theme: {
        ...structuredClone(theme),
        name: theme.name,
        id: newRequestId(),
      },
      revision: snapshot.revision,
    });
  const duplicateReference = (reference: string) => {
    const custom = p.custom_themes.find((theme) => theme.id === reference);
    if (custom) duplicate(custom);
    else {
      const bundled = palettes[reference as Palette];
      if (bundled)
        duplicate({ version: 1, name: label(reference), ...bundled });
    }
  };
  const exportTheme = (theme: CustomTheme) => {
    const { id: _, ...file } = theme;
    const blob = new Blob([JSON.stringify(file, null, 2)], {
      type: "application/json",
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "delidev-theme.json";
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  };
  return (
    <>
      <fieldset
        disabled={blocked}
        data-settings-search-target="theme"
        aria-describedby={`appearance-scope ${rowId}-theme-section-help`}
      >
        <legend>{copy("appearance.theme_efb52e")}</legend>
        <p
          id={`${rowId}-theme-section-help`}
          className="appearance-section-help"
        >
          {label("help.themeSection")}
        </p>
        {modeControls}
        <div data-settings-search-target="palettes">
          {(["light_palette", "dark_palette"] as const).map((key) => (
            <div className="appearance-row appearance-palette-row" key={key}>
              <label htmlFor={`${rowId}-${key}`}>{text(key)}</label>
              <div className="appearance-palette-controls">
                <select
                  id={`${rowId}-${key}`}
                  aria-labelledby={`${rowId}-${key}-label`}
                  aria-describedby={`${rowId}-${key}-help`}
                  value={p[key]}
                  onChange={(e) => change(key, e.target.value)}
                >
                  {Object.values(Palette).map((id) => (
                    <option key={id} value={id}>
                      {label(id)}
                    </option>
                  ))}
                  {p.custom_themes.map((theme) => (
                    <option key={theme.id} value={theme.id}>
                      {theme.name}
                    </option>
                  ))}
                </select>
                <SettingsActionButton
                  icon={SettingsActionIcon.Copy}
                  type="button"
                  aria-label={label(
                    key === "light_palette"
                      ? "duplicateLightPalette"
                      : "duplicateDarkPalette",
                  )}
                  aria-describedby={`${rowId}-${key}-help`}
                  disabled={p.custom_themes.length >= 32}
                  onClick={() => duplicateReference(p[key])}
                >
                  {label("duplicatePalette")}
                </SettingsActionButton>
              </div>
            </div>
          ))}
          {checkbox("color_assistance")}
          <div className="appearance-actions">
            <SettingsActionButton
              icon={SettingsActionIcon.Upload}
              type="button"
              disabled={p.custom_themes.length >= 32}
              onClick={() => {
                setImportRevision(snapshot.revision);
                setImporting(true);
                setImportText("");
                setImportError(false);
              }}
            >
              {label("import")}
            </SettingsActionButton>
          </div>
          {p.custom_themes.map((theme) => (
            <div className="appearance-custom" key={theme.id}>
              <span>{theme.name}</span>
              <SettingsActionButton
                icon={SettingsActionIcon.Edit}
                targetName={theme.name}
                onClick={() =>
                  setEditor({
                    theme: structuredClone(theme),
                    revision: snapshot.revision,
                  })
                }
              >
                {label("editTheme")}
              </SettingsActionButton>
              <SettingsActionButton
                icon={SettingsActionIcon.Copy}
                targetName={theme.name}
                disabled={p.custom_themes.length >= 32}
                onClick={() => duplicate(theme)}
              >
                {label("duplicate")}
              </SettingsActionButton>
              <SettingsActionButton
                icon={SettingsActionIcon.Download}
                targetName={theme.name}
                onClick={() => exportTheme(theme)}
              >
                {label("export")}
              </SettingsActionButton>
              <SettingsActionButton
                tone={SettingsActionTone.Danger}
                icon={SettingsActionIcon.Delete}
                targetName={theme.name}
                onClick={() =>
                  setRemoval({ id: theme.id, revision: snapshot.revision })
                }
              >
                {label("delete")}
              </SettingsActionButton>
            </div>
          ))}
        </div>
      </fieldset>
      {section(
        "composer",
        <>
          {select("composer_layout", Object.values(Layout))}
          {select("composer_size", [0, 12, 14, 16, 18])}
        </>,
      )}
      {section(
        "status",
        <>
          {select("status", Object.values(StatusPresentation))}
          {checkbox("session_accent")}
          {checkbox("show_tokens")}
          {checkbox("show_time")}
          <p>{label("observationsHelp")}</p>
        </>,
      )}
      {section(
        "display",
        <>
          {select("density", Object.values(Layout))}
          {select("conversation_size", [0, 12, 14, 16, 18])}
          {checkbox("animation")}
          <div className="appearance-row appearance-disclosure-row">
            <span className="appearance-row-text">
              <span>{label("disclosureDefaults")}</span>
              <small>{label("help.disclosureDefaults")}</small>
            </span>
            <div className="appearance-disclosure-controls">
              {select(
                "tool_disclosure",
                Object.values(DisclosureDefault),
                true,
              )}
              {select(
                "reasoning_disclosure",
                Object.values(DisclosureDefault),
                true,
              )}
              {select(
                "compaction_disclosure",
                Object.values(DisclosureDefault),
                true,
              )}
            </div>
          </div>
          <fieldset disabled>
            {checkbox("markdown")}
            {checkbox("mermaid")}
            {checkbox("svg")}
            {checkbox("table_charts")}
            <p>{label("renderersUnavailable")}</p>
          </fieldset>
        </>,
      )}
      {section(
        "images",
        <>
          {checkbox("inline_images")}
          {select("image_size", Object.values(ImageSize))}
        </>,
      )}
      {editor ? (
        <ThemeEditor
          key={editor.theme.id}
          initial={editor.theme}
          revision={editor.revision}
          close={() => setEditor(undefined)}
        />
      ) : null}
      {importing ? (
        <Modal title={label("import")} close={() => setImporting(false)}>
          <form
            onKeyDown={guardEnterSubmission}
            onSubmit={(e) => {
              if (!consumeFormSubmit(e)) return;
              try {
                if (snapshot.revision !== importRevision) return;
                const file = parseThemeFile(importText);
                setEditor({
                  theme: { ...file, id: newRequestId() },
                  revision: importRevision,
                });
                setImporting(false);
              } catch {
                setImportError(true);
              }
            }}
          >
            <label>
              {label("themeJson")}
              <textarea
                value={importText}
                onChange={(e) => {
                  setImportText(e.target.value);
                  setImportError(false);
                }}
              />
            </label>
            {snapshot.revision !== importRevision ? (
              <p role="alert">{label("draftConflict")}</p>
            ) : null}
            {importError ? <p role="alert">{label("invalidTheme")}</p> : null}
            <SettingsActionButton
              icon={SettingsActionIcon.Inspect}
              type="submit"
              disabled={
                blocked ||
                p.custom_themes.length >= 32 ||
                snapshot.revision !== importRevision
              }
            >
              {label("preview")}
            </SettingsActionButton>
          </form>
        </Modal>
      ) : null}
      {removal ? (
        <Modal title={label("delete")} close={() => setRemoval(undefined)}>
          <p>{label("deleteHelp")}</p>
          <SettingsActionButton
            icon={SettingsActionIcon.Cancel}
            onClick={() => setRemoval(undefined)}
          >
            {label("cancel")}
          </SettingsActionButton>
          <SettingsActionButton
            tone={SettingsActionTone.Danger}
            icon={SettingsActionIcon.Confirm}
            disabled={blocked || removal.revision !== snapshot.revision}
            onClick={() => {
              update(
                {
                  ...p,
                  custom_themes: p.custom_themes.filter(
                    (t) => t.id !== removal.id,
                  ),
                  light_palette:
                    p.light_palette === removal.id
                      ? Palette.Default
                      : p.light_palette,
                  dark_palette:
                    p.dark_palette === removal.id
                      ? Palette.Default
                      : p.dark_palette,
                },
                removal.revision,
              );
              setRemoval(undefined);
            }}
          >
            {label("confirmDelete")}
          </SettingsActionButton>
          {removal.revision !== snapshot.revision ? (
            <p role="alert">{label("draftConflict")}</p>
          ) : null}
        </Modal>
      ) : null}
    </>
  );
}
