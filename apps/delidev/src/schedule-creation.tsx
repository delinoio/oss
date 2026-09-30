// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { EntityKind } from "@delinoio/delidev-api-client";
import "./schedule-creation.css";
import { ResourceChoice, TextField } from "./configuration-fields";
import { items, Mode, text, Workspace, type Document } from "./documents";

enum Frequency { Daily = "daily", Weekdays = "weekdays", Weekly = "weekly", Custom = "custom" }
enum Overlap { Overlap = "overlap", Skip = "skip", Wait = "wait" }
enum Weekday { Sunday = "0", Monday = "1", Tuesday = "2", Wednesday = "3", Thursday = "4", Friday = "5", Saturday = "6" }
const frequencyNames = { [Frequency.Daily]: "Daily", [Frequency.Weekdays]: "Weekdays", [Frequency.Weekly]: "Weekly", [Frequency.Custom]: "Custom cron" };
const weekdayNames = { [Weekday.Sunday]: "Sunday", [Weekday.Monday]: "Monday", [Weekday.Tuesday]: "Tuesday", [Weekday.Wednesday]: "Wednesday", [Weekday.Thursday]: "Thursday", [Weekday.Friday]: "Friday", [Weekday.Saturday]: "Saturday" };
const validTime = (value: string) => /^(?:[01]\d|2[0-3]):[0-5]\d$/.test(value);
function presetCron(frequency: Frequency, time: string, weekday: Weekday): string {
  const [hour, minute] = time.split(":").map(Number);
  return `${minute} ${hour} * * ${frequency === Frequency.Daily ? "*" : frequency === Frequency.Weekdays ? "1-5" : weekday}`;
}
function generatedTime(cron: string): string | undefined {
  // Import only canonical expressions emitted by these presets. Calendar,
  // timezone and arbitrary cron interpretation remain server responsibilities.
  const match = /^([0-9]|[1-5][0-9]) ([0-9]|1[0-9]|2[0-3]) \* \* (\*|1-5|[0-6])$/.exec(cron);
  return match ? `${match[2].padStart(2, "0")}:${match[1].padStart(2, "0")}` : undefined;
}
export interface ScheduleCreationProps {
  definition: Document;
  change: (next: Document) => boolean;
  active: boolean;
  blocked: boolean;
  localAvailable: boolean;
  selectLocal: () => void;
  submit: () => Promise<void>;
  cancel: () => void;
  references: ReactNode;
  errors: ReactNode;
  retry: ReactNode;
}

export function ScheduleCreation({ definition, change, active, blocked, localAvailable, selectLocal, submit, cancel, references, errors, retry }: ScheduleCreationProps) {
  const [frequency, setFrequency] = useState(Frequency.Weekdays);
  const [time, setTime] = useState("09:00");
  const [lastValidTime, setLastValidTime] = useState<string>();
  const [weekday, setWeekday] = useState(Weekday.Monday);
  const [expanded, setExpanded] = useState(false);
  const invalidReference = useRef<HTMLElement | null>(null);
  const name = useRef<HTMLInputElement>(null), focused = useRef(false);
  const disclosure = useId(), radios = useId();
  useEffect(() => {
    if (active && !focused.current) { focused.current = true; name.current?.focus(); }
  }, [active]);
  useEffect(() => { if (expanded && invalidReference.current) { invalidReference.current.focus(); invalidReference.current = null; } }, [expanded]);
  const local = definition.workspace === Workspace.Local;
  const preset = frequency !== Frequency.Custom;
  const timeInvalid = preset && !validTime(time);
  const field = (key: string) => (value: unknown) => change({ ...definition, [key]: value });
  const chooseFrequency = (next: Frequency) => {
    if (blocked) return;
    if (next === Frequency.Custom) { setFrequency(next); return; }
    const nextTime = frequency === Frequency.Custom ? generatedTime(text(definition.cron)) ?? lastValidTime ?? "09:00" : time;
    if (validTime(nextTime)) {
      if (!field("cron")(presetCron(next, nextTime, weekday))) return;
      setLastValidTime(nextTime);
    }
    setTime(nextTime); setFrequency(next);
  };
  const chooseTime = (next: string) => {
    if (validTime(next)) {
      if (!field("cron")(presetCron(frequency, next, weekday))) return;
      setLastValidTime(next);
    }
    setTime(next);
  };
  const chooseWeekday = (next: Weekday) => {
    if (validTime(time) && !field("cron")(presetCron(frequency, time, next))) return;
    setWeekday(next);
  };
  const summary = preset
    ? timeInvalid ? "Enter a valid time in HH:mm." : `${frequency === Frequency.Weekly ? `Weekly on ${weekdayNames[weekday]}` : frequencyNames[frequency]} at ${time} · ${text(definition.timezone)}`
    : `Custom cron · ${text(definition.timezone)}`;
  return <section className="schedule-creation"><form onSubmit={(event) => { event.preventDefault(); if (!blocked && !timeInvalid) void submit(); }}>
    <div className="schedule-creation-scroll">
      <header className="schedule-creation-header"><h2>New schedule</h2><p>Set up a recurring task for your project.</p></header>
      <fieldset className="schedule-creation-grid" disabled={blocked}>
        <section className="schedule-creation-card schedule-creation-task" aria-labelledby={`${radios}-task`}>
          <h3 id={`${radios}-task`}>Task</h3><p>Schedule name, Project and Scheduled prompt are required.</p>
          <label>Schedule name<input ref={name} required maxLength={256} placeholder="e.g. Weekday code review" value={text(definition.name)} onChange={(event) => field("name")(event.target.value)} /></label>
          <ResourceChoice label="Project" kind={EntityKind.PROJECT} value={text(definition.project_id)} active={active} required showStatus change={(project_id) => change({ ...definition, project_id, starting: [] })} />
          <label>Scheduled prompt<textarea required rows={3} maxLength={262144} placeholder="Describe what the agent should do on each run..." value={text(definition.prompt)} onChange={(event) => field("prompt")(event.target.value)} /></label>
        </section>
        <section className="schedule-creation-card schedule-creation-execution" aria-labelledby={`${radios}-execution`}>
          <h3 id={`${radios}-execution`}>Execution</h3><p>Agent Worker and Execution Worker are required.</p>
          <div className="schedule-creation-selectors">
            <ResourceChoice label="Agent Worker" kind={EntityKind.AGENT} value={text(definition.agent_id)} active={active} required showStatus change={field("agent_id")} />
            <ResourceChoice label="Execution Worker" kind={EntityKind.MACHINE} value={text(definition.machine_id)} active={active} disabled={local} required showStatus change={field("machine_id")} />
          </div>
          <fieldset className="schedule-creation-radio-group"><legend>Workspace</legend><div className="schedule-creation-radio-cards">
            <label><input type="radio" name={`${radios}-workspace`} checked={!local} onChange={() => field("workspace")(Workspace.Worktree)} /><span>Worktree</span></label>
            <label><input type="radio" name={`${radios}-workspace`} checked={local} disabled={!localAvailable} onChange={selectLocal} /><span>Local computer</span></label>
          </div></fieldset>
          <fieldset className="schedule-creation-radio-group"><legend>Execution mode</legend><div className="schedule-creation-radio-cards">
            <label><input type="radio" name={`${radios}-mode`} checked={definition.mode === Mode.Execute} onChange={() => field("mode")(Mode.Execute)} /><span>Execute</span></label>
            <label><input type="radio" name={`${radios}-mode`} checked={definition.mode === Mode.Plan} onChange={() => field("mode")(Mode.Plan)} /><span>Plan</span></label>
          </div></fieldset>
          {local ? <p>Local computer · originating Worker selected. Existing checkouts are shared as-is without fetch or starting-reference overrides.</p> : <div className="schedule-creation-disclosure">
            <button type="button" aria-expanded={expanded} aria-controls={disclosure} onClick={() => setExpanded(!expanded)}><span>Starting reference overrides</span><small>{items(definition.starting).length ? `${items(definition.starting).length} override${items(definition.starting).length === 1 ? "" : "s"}` : "Using saved project references"}</small></button>
            <div id={disclosure} hidden={!expanded} onInvalidCapture={(event) => { if (!expanded) { invalidReference.current ??= event.target as HTMLElement; setExpanded(true); } }}>{references}</div>
          </div>}
        </section>
        <section className="schedule-creation-card schedule-creation-repeat" aria-labelledby={`${radios}-repeat`}>
          <h3 id={`${radios}-repeat`}>Repeat schedule</h3>
          <label>Frequency<select value={frequency} onChange={(event) => chooseFrequency(event.target.value as Frequency)}>{Object.values(Frequency).map((value) => <option key={value} value={value}>{frequencyNames[value]}</option>)}</select></label>
          {preset ? <><label>Time<input type="time" required step={60} value={time} aria-invalid={timeInvalid} aria-describedby={timeInvalid ? `${radios}-time-error` : undefined} onChange={(event) => chooseTime(event.target.value)} /></label>{timeInvalid ? <p id={`${radios}-time-error`} role="alert">Enter a valid time in HH:mm before creating the schedule.</p> : null}{frequency === Frequency.Weekly ? <label>Weekday<select value={weekday} onChange={(event) => chooseWeekday(event.target.value as Weekday)}>{Object.values(Weekday).map((value) => <option key={value} value={value}>{weekdayNames[value]}</option>)}</select></label> : null}</> : <><TextField label="Cron expression" value={definition.cron} required max={512} change={field("cron")} /><p>Five fields: minute, hour, day of month, month, weekday. The server validates the calendar and computes the next UTC run.</p></>}
          <TextField label="IANA timezone" value={definition.timezone} required change={field("timezone")} />
          <div className="schedule-creation-preview" role="status"><p>{summary}</p>{!timeInvalid ? <code>{text(definition.cron)}</code> : null}<p>Next run is calculated by the server after saving.</p></div>
          <label>When a previous occurrence is still active<select value={text(definition.overlap)} onChange={(event) => field("overlap")(event.target.value)}><option value={Overlap.Overlap}>Overlap · independent sessions</option><option value={Overlap.Skip}>Skip the new occurrence</option><option value={Overlap.Wait}>Wait in FIFO order for confirmed cleanup</option></select></label>
          <label className="checkbox"><input type="checkbox" checked={definition.enabled === true} onChange={(event) => field("enabled")(event.target.checked)} />Enable future scheduled runs</label>
          <p>Schedules are paused on creation unless future runs are enabled.</p><p>The server continues scheduling when the desktop closes. Offline due times are recorded as skipped; no missed-run burst is created.</p>
        </section>
      </fieldset>
      <div className="schedule-creation-errors">{errors}</div>
    </div>
    <footer className="schedule-creation-footer"><div className="schedule-creation-footer-inner">
      <p>{definition.enabled === true ? "Enabled on creation" : "Paused on creation"}<small>{local ? "Local computer" : "Worktree"} · {definition.mode === Mode.Plan ? "Plan" : "Execute"}</small></p>
      <div className="actions">{retry}<button type="button" disabled={blocked} onClick={cancel}>Cancel</button><button className="primary" disabled={blocked || timeInvalid}>Create schedule</button></div>
    </div></footer>
  </form></section>;
}
