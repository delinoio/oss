// SPDX-License-Identifier: Apache-2.0
import { createContext, useCallback, useId, useState, type ReactNode, type SyntheticEvent } from "react";
import { items, object, type Document } from "./documents";
import "./agent-configuration.css";

// This private context observes existing selector reads; disclosure openness
// never controls their mounting, query keys, enablement or refresh lifetime.
export const AgentReadProblem = createContext<((label: string, problem: boolean) => void) | undefined>(undefined);

enum AgentSection { Reasoning = "Reasoning", Accounts = "Accounts & routing", Instructions = "Instructions", Native = "Native harness options" }
const nativeKeys = ["subagent_model", "subagent_effort", "max_concurrency", "approval_policy", "approval_review_model", "service_tier"];
const permissionKeys = ["permission", "claude_permission"];

function nativeSummary(data: Document) {
  const options = object(data.options);
  const unknown = Object.keys(options).some(key => !nativeKeys.includes(key) && !permissionKeys.includes(key));
  const customized = nativeKeys.some(key => options[key] !== undefined && options[key] !== "" && !(key === "max_concurrency" && options[key] === 0));
  return unknown ? "Customized · unknown options retained" : customized ? "Customized" : "Defaults";
}

function retainedSummary(value: unknown, inherited: string) {
  return value === undefined || value === "" ? inherited : typeof value === "string" ? value : "Unsupported value retained";
}

function SectionIcon({ section }: { section: AgentSection }) {
  const paths: Record<AgentSection, string> = {
    [AgentSection.Reasoning]: "M9 18h6M10 21h4M8 14a6 6 0 1 1 8 0l-1 2H9l-1-2Z",
    [AgentSection.Accounts]: "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M17 4a4 4 0 0 1 0 8M22 21v-2a4 4 0 0 0-3-4",
    [AgentSection.Instructions]: "M8 3h10v18H6V5h2v-2ZM9 8h6M9 12h6M9 16h4",
    [AgentSection.Native]: "m8 5-6 7 6 7M16 5l6 7-6 7M14 3l-4 18",
  };
  return <svg className="agent-section-icon" aria-hidden="true" focusable="false" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><path d={paths[section]} /></svg>;
}

function Disclosure({ section, summary, children, invalidValue = false, note }: { section: AgentSection; summary: string; children: ReactNode; invalidValue?: boolean; note?: string }) {
  const [reads, setReads] = useState<Record<string, boolean>>({});
  const [invalid, setInvalid] = useState(false);
  const reportRead = useCallback((label: string, problem: boolean) => setReads(current => current[label] === problem ? current : { ...current, [label]: problem }), []);
  const problem = invalid || invalidValue || Object.values(reads).some(Boolean);
  return <div className="agent-optional-section">
    <details className="agent-disclosure" onInvalidCapture={event => { event.currentTarget.open = true; setInvalid(true); }} onChangeCapture={event => setInvalid(Boolean(firstInvalidControl(event.currentTarget)))}>
      <summary><SectionIcon section={section} /><span className="agent-section-copy"><span className="agent-section-title">{section}{problem ? <span className="agent-section-problem" role="status">Needs attention</span> : null}</span><span className="agent-section-summary">{summary}</span></span><svg className="agent-chevron" aria-hidden="true" focusable="false" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6"><path d="m9 5 7 7-7 7" /></svg></summary>
      <div className="agent-section-fields"><AgentReadProblem.Provider value={reportRead}>{children}</AgentReadProblem.Provider></div>
    </details>
    {note ? <p className="agent-section-note">{note}</p> : null}
  </div>;
}

export function AgentConfiguration({ data, core, permissions, reasoning, accounts, instructions, native, routingProblem }: { data: Document; core: ReactNode; permissions: ReactNode; reasoning: ReactNode; accounts: ReactNode; instructions: ReactNode; native: ReactNode; routingProblem: boolean }) {
  const coreId = useId();
  const optionalId = useId();
  const links = items(data.accounts);
  const options = object(data.options);
  const concurrency = options.max_concurrency;
  const invalidConcurrency = concurrency !== undefined && (typeof concurrency !== "number" || !Number.isInteger(concurrency) || concurrency < 0 || concurrency > 64);
  const invalidWeight = links.some(link => { const weight = object(link).weight; return typeof weight !== "number" || !Number.isInteger(weight) || weight < 1 || weight > 1000; });
  return <>
    <section className="agent-core" aria-labelledby={coreId}><header><h4 id={coreId}>Core settings</h4><p>Required fields are marked *</p></header>{core}<div className="agent-permissions">{permissions}</div></section>
    <section className="agent-optional" aria-labelledby={optionalId}><header><h4 id={optionalId}>Optional settings</h4><p>Leave these unchanged to keep the current defaults.</p></header>
      <Disclosure section={AgentSection.Reasoning} summary={retainedSummary(data.effort, "Native default")} invalidValue={data.effort !== undefined && typeof data.effort !== "string"}>{reasoning}</Disclosure>
      <Disclosure section={AgentSection.Accounts} summary={`${links.length} accounts · ${retainedSummary(data.routing, "Server default")}`} invalidValue={invalidWeight || routingProblem} note={links.length === 0 ? "Can be saved without accounts; execution requires an eligible account." : undefined}>{accounts}</Disclosure>
      <Disclosure section={AgentSection.Instructions} summary={`${items(data.templates).length} templates`}>{instructions}</Disclosure>
      <Disclosure section={AgentSection.Native} summary={nativeSummary(data)} invalidValue={invalidConcurrency}>{native}</Disclosure>
    </section>
  </>;
}

function firstInvalidControl(container: HTMLElement) {
  return Array.from(container.querySelectorAll<HTMLInputElement | HTMLSelectElement>("input, select")).find(control => control.willValidate && !control.validity.valid);
}

export function revealAgentInvalidControl(event: SyntheticEvent<HTMLFormElement>) {
  event.preventDefault();
  const first = firstInvalidControl(event.currentTarget);
  const section = first?.closest<HTMLDetailsElement>(".agent-disclosure");
  if (section) section.open = true;
  // Native validation runs before submit and may find controls in a closed
  // details element. Reveal synchronously, then focus after its layout updates.
  window.requestAnimationFrame(() => { if (first?.isConnected && first.willValidate) first.focus(); });
}
