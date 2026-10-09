// SPDX-License-Identifier: Apache-2.0
import { useId } from "react";
import { HarnessMark } from "./harness-mark";
import { Harness } from "./configuration-fields";
import { copy, useLocale } from "./localization";

const harnesses = Object.values(Harness);
export const workerHarnessNames: Record<Harness, string> = { [Harness.Codex]: "Codex", [Harness.Claude]: "Claude Code", [Harness.OpenCode]: "OpenCode", [Harness.Grok]: "Grok Build" };
const origins: Record<Harness, string> = { [Harness.Codex]: "OpenAI", [Harness.Claude]: "Anthropic", [Harness.OpenCode]: "Open source", [Harness.Grok]: "xAI" };

// Both capability generations use the same keyboard and presentation contract.
export function WorkerHarnessPicker({ value, disabled, change, confirm }: { value: unknown; disabled: boolean; change: (harness: Harness) => void; confirm: (harness: Harness) => void }) {
  useLocale();
  const id = useId();
  return <>
    <p id={`${id}-help`}>{copy("agent-worker-wizard.chooseTool")}</p>
    <div className="worker-harness-grid" role="radiogroup" aria-label={copy("agent-worker-wizard.harness")} aria-describedby={`${id}-help ${id}-guidance`}>
      {harnesses.map((harness, index) => {
        const selected = value === harness;
        const entry = selected || !harnesses.includes(value as Harness) && index === 0;
        return <button key={harness} type="button" role="radio" className="worker-harness-card" aria-checked={selected} aria-label={workerHarnessNames[harness]} aria-describedby={`${id}-${harness}-origin`} tabIndex={entry ? 0 : -1} data-wizard-field={entry ? "harness" : undefined} data-harness={harness} disabled={disabled} onClick={() => { if (!disabled) confirm(harness); }} onKeyDown={event => {
          if (disabled) return;
          let next: number;
          switch (event.key) {
            case "ArrowRight": case "ArrowDown": next = (index + 1) % harnesses.length; break;
            case "ArrowLeft": case "ArrowUp": next = (index + harnesses.length - 1) % harnesses.length; break;
            case "Home": next = 0; break;
            case "End": next = harnesses.length - 1; break;
            default: return;
          }
          event.preventDefault(); change(harnesses[next]!);
          event.currentTarget.parentElement?.querySelector<HTMLButtonElement>(`[data-harness="${harnesses[next]}"]`)?.focus();
        }}>
          <HarnessMark harness={harness} />
          <span className="worker-harness-indicator" aria-hidden="true">{selected ? <svg viewBox="0 0 16 16" width="16" height="16"><path d="m3.5 8 3 3 6-6" /></svg> : null}</span>
          <strong className="worker-harness-name">{workerHarnessNames[harness]}</strong>
          <small id={`${id}-${harness}-origin`}>{harness === Harness.OpenCode ? copy("agent-worker-wizard.openSource") : origins[harness]}</small>
        </button>;
      })}
    </div>
    <p id={`${id}-guidance`} className="worker-harness-guidance">{copy("agent-worker-wizard.chooseHarnessToContinueToAccounts")}</p>
  </>;
}
