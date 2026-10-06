// SPDX-License-Identifier: Apache-2.0
import { useId } from "react";
import { Harness } from "./configuration-fields";

const harnesses = Object.values(Harness);
const names: Record<Harness, string> = { [Harness.Codex]: "Codex", [Harness.Claude]: "Claude Code", [Harness.OpenCode]: "OpenCode", [Harness.Grok]: "Grok Build" };
const origins: Record<Harness, string> = { [Harness.Codex]: "OpenAI", [Harness.Claude]: "Anthropic", [Harness.OpenCode]: "Open source", [Harness.Grok]: "xAI" };

// Both capability generations use the same keyboard and presentation contract.
export function WorkerHarnessPicker({ value, disabled, change }: { value: unknown; disabled: boolean; change: (harness: Harness) => void }) {
  const id = useId();
  return <>
    <p id={`${id}-help`}>Choose the tool that runs this Worker.</p>
    <div className="worker-harness-grid" role="radiogroup" aria-label="Harness" aria-describedby={`${id}-help`}>
      {harnesses.map((harness, index) => {
        const selected = value === harness;
        const entry = selected || !harnesses.includes(value as Harness) && index === 0;
        return <button key={harness} type="button" role="radio" className="worker-harness-card" aria-checked={selected} aria-label={names[harness]} aria-describedby={`${id}-${harness}-origin`} tabIndex={entry ? 0 : -1} data-wizard-field={entry ? "harness" : undefined} data-harness={harness} disabled={disabled} onClick={() => { if (!disabled) change(harness); }} onKeyDown={event => {
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
          <span className={`worker-harness-mark worker-harness-mark-${harness}`} aria-hidden="true" />
          <span className="worker-harness-indicator" aria-hidden="true">{selected ? <svg viewBox="0 0 16 16" width="16" height="16"><path d="m3.5 8 3 3 6-6" /></svg> : null}</span>
          <strong className="worker-harness-name">{names[harness]}</strong>
          <small id={`${id}-${harness}-origin`}>{origins[harness]}</small>
        </button>;
      })}
    </div>
    <p className="worker-harness-guidance">Select accounts and a model for each source.</p>
  </>;
}
