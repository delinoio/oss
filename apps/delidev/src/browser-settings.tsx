// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState } from "react";
import { copy, useLocale, type MessageKey } from "./localization";
import {
  BrowserChoiceState,
  BrowserPreferenceOperation,
  BrowserPreferenceProblem,
  BrowserSaveResult,
  useBrowserPreferences,
  type BrowserChoice,
  type BrowserChoices,
  type BrowserInventoryEntry,
} from "./browser-preference-controller";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import "./browser-settings.css";

export enum BrowserSlot {
  General = "general",
  Local = "local",
}
enum InventoryStatus {
  Loading = "loading",
  Ready = "ready",
  Failed = "failed",
}
const problemMessages: Record<BrowserPreferenceProblem, MessageKey> = {
  [BrowserPreferenceProblem.Unavailable]: "browser-settings.webUnavailable",
  [BrowserPreferenceProblem.ReadFailed]: "browser-settings.readFailed",
  [BrowserPreferenceProblem.InvalidDocument]:
    "browser-settings.invalidDocument",
  [BrowserPreferenceProblem.UnsupportedVersion]:
    "browser-settings.unsupportedVersion",
  [BrowserPreferenceProblem.WriteFailed]: "browser-settings.writeFailed",
  [BrowserPreferenceProblem.OutcomeUnknown]: "browser-settings.outcomeUnknown",
  [BrowserPreferenceProblem.Changed]: "browser-settings.changed",
};
const operationMessages: Record<BrowserPreferenceOperation, MessageKey> = {
  [BrowserPreferenceOperation.Reading]: "browser-settings.reading",
  [BrowserPreferenceOperation.Saving]: "browser-settings.saving",
  [BrowserPreferenceOperation.Resetting]: "browser-settings.resetting",
};
const slots: readonly {
  slot: BrowserSlot;
  target: string;
  label: MessageKey;
  help: MessageKey;
}[] = [
  {
    slot: BrowserSlot.General,
    target: "browser-general",
    label: "browser-settings.generalLabel",
    help: "browser-settings.generalHelp",
  },
  {
    slot: BrowserSlot.Local,
    target: "browser-local",
    label: "browser-settings.localLabel",
    help: "browser-settings.localHelp",
  },
];

// Option values are structural, never copy: the default sentinel cannot collide
// with a native identity because identities are always prefixed.
const DEFAULT_VALUE = "default";
const INSTALLED_PREFIX = "installed:";
const optionValue = (choice: BrowserChoice) =>
  choice.state === BrowserChoiceState.Installed
    ? `${INSTALLED_PREFIX}${choice.identity}`
    : DEFAULT_VALUE;
const choiceFromValue = (value: string): BrowserChoice =>
  value.startsWith(INSTALLED_PREFIX)
    ? {
        state: BrowserChoiceState.Installed,
        identity: value.slice(INSTALLED_PREFIX.length),
      }
    : { state: BrowserChoiceState.SystemDefault };

export function BrowserSettings({ active = true }: { active?: boolean }) {
  useLocale();
  const id = useId();
  const { native, snapshot, operation, save, reset, reload, inventory } =
    useBrowserPreferences();
  const [browsers, setBrowsers] = useState<BrowserInventoryEntry[]>([]);
  const [status, setStatus] = useState(InventoryStatus.Loading);
  const [outcome, setOutcome] = useState<BrowserSaveResult>();
  const generation = useRef(0);
  const refresh = () => {
    const owned = ++generation.current;
    setStatus(InventoryStatus.Loading);
    void inventory()
      .then((list) => {
        if (generation.current !== owned) return;
        setBrowsers(list);
        setStatus(InventoryStatus.Ready);
      })
      .catch(() => {
        if (generation.current === owned) setStatus(InventoryStatus.Failed);
      });
  };
  // Discovery refreshes whenever the page opens, not once per app session.
  useEffect(() => {
    if (!native || !active) return;
    refresh();
    return () => {
      generation.current++;
    };
  }, [native, active]);
  const busy = operation !== undefined;
  const problem = snapshot.problem;
  const editable = native && !busy && problem === null;
  const choose = async (slot: BrowserSlot, value: string) => {
    const next: BrowserChoices = {
      ...snapshot.choices,
      [slot]: choiceFromValue(value),
    };
    setOutcome(undefined);
    const result = await save(next, snapshot.revision);
    setOutcome(result);
    if (result === BrowserSaveResult.NotInstalled) refresh();
  };
  const known = new Set(browsers.map((browser) => browser.identity));
  const unavailable = slots.some(({ slot }) => {
    const choice = snapshot.choices[slot];
    return (
      choice.state === BrowserChoiceState.Installed &&
      status === InventoryStatus.Ready &&
      !known.has(choice.identity)
    );
  });
  const resettable =
    problem === BrowserPreferenceProblem.InvalidDocument ||
    problem === BrowserPreferenceProblem.UnsupportedVersion;
  const message = !native
    ? copy("browser-settings.webUnavailable")
    : operation
      ? copy(operationMessages[operation])
      : problem
        ? copy(problemMessages[problem])
        : outcome === BrowserSaveResult.Saved
          ? copy("browser-settings.saved")
          : outcome === BrowserSaveResult.NotInstalled
            ? copy("browser-settings.notInstalled")
            : outcome === BrowserSaveResult.DiscoveryFailed
              ? copy("browser-settings.selectionDiscoveryFailed")
              : outcome === BrowserSaveResult.InvalidSelection
                ? copy("browser-settings.invalidSelection")
                : undefined;
  return (
    <section
      className="browser-settings"
      aria-label={copy("browser-settings.title")}
      aria-busy={busy}
    >
      <div className="browser-settings-card">
        {slots.map(({ slot, target, label, help }) => {
          const choice = snapshot.choices[slot];
          const selectId = `${id}-${slot}`;
          const installed =
            choice.state === BrowserChoiceState.Installed
              ? choice.identity
              : undefined;
          // A saved identity that is not in the current inventory stays visible
          // and selected so the user can see and explicitly replace it.
          const missing =
            installed !== undefined && !known.has(installed)
              ? installed
              : undefined;
          return (
            <div
              key={slot}
              className="browser-settings-row"
              data-settings-search-target={target}
            >
              <div className="browser-settings-text">
                <label htmlFor={selectId}>{copy(label)}</label>
                <p id={`${selectId}-help`}>{copy(help)}</p>
              </div>
              <select
                id={selectId}
                aria-describedby={`${selectId}-help ${id}-status`}
                value={optionValue(choice)}
                disabled={!editable}
                onChange={(event) =>
                  void choose(slot, event.currentTarget.value)
                }
              >
                <option value={DEFAULT_VALUE}>
                  {copy("browser-settings.systemDefault")}
                </option>
                {browsers.map((browser) => (
                  <option
                    key={browser.identity}
                    value={`${INSTALLED_PREFIX}${browser.identity}`}
                  >
                    {browser.name}
                  </option>
                ))}
                {missing !== undefined ? (
                  <option value={`${INSTALLED_PREFIX}${missing}`}>
                    {status === InventoryStatus.Ready
                      ? copy("browser-settings.unavailableOption", {
                          identity: missing,
                        })
                      : missing}
                  </option>
                ) : null}
              </select>
            </div>
          );
        })}
      </div>
      <p className="browser-settings-note">
        {copy("browser-settings.authNote")}
      </p>
      <div
        id={`${id}-status`}
        className="browser-settings-status"
        role={
          problem ||
          outcome === BrowserSaveResult.NotInstalled ||
          outcome === BrowserSaveResult.DiscoveryFailed ||
          outcome === BrowserSaveResult.InvalidSelection
            ? "alert"
            : "status"
        }
      >
        {message ? <p>{message}</p> : null}
        {native && status === InventoryStatus.Loading ? (
          <p>{copy("browser-settings.discovering")}</p>
        ) : null}
        {native && status === InventoryStatus.Failed ? (
          <p>{copy("browser-settings.discoveryFailed")}</p>
        ) : null}
        {native && status === InventoryStatus.Ready && browsers.length === 0 ? (
          <p>{copy("browser-settings.noBrowsers")}</p>
        ) : null}
        {native && unavailable ? (
          <p>{copy("browser-settings.unavailableSaved")}</p>
        ) : null}
      </div>
      {native ? (
        <div className="browser-settings-actions">
          <SettingsActionButton
            icon={SettingsActionIcon.Refresh}
            type="button"
            disabled={status === InventoryStatus.Loading}
            onClick={refresh}
          >
            {copy("browser-settings.refresh")}
          </SettingsActionButton>
          {problem &&
          !resettable &&
          problem !== BrowserPreferenceProblem.Unavailable ? (
            <SettingsActionButton
              icon={SettingsActionIcon.Retry}
              type="button"
              disabled={busy}
              onClick={() => {
                setOutcome(undefined);
                reload();
              }}
            >
              {copy("browser-settings.reload")}
            </SettingsActionButton>
          ) : null}
          {resettable ? (
            <SettingsActionButton
              icon={SettingsActionIcon.Delete}
              type="button"
              disabled={busy}
              onClick={() => {
                setOutcome(undefined);
                void reset(snapshot.revision);
              }}
            >
              {copy("browser-settings.reset")}
            </SettingsActionButton>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
