import { DisclosureButton, DisclosureContent, DisclosureDensity } from "./disclosure";
import { copy, useLocale } from "./localization";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { EntityKind } from "@delinoio/delidev-api-client";
import "./schedule-creation.css";
import { ResourceChoice, TextField } from "./configuration-fields";
import { items, Mode, text, Workspace, type Document } from "./documents";

enum Frequency { Daily = "daily", Weekdays = "weekdays", Weekly = "weekly", Custom = "custom" }
enum Overlap { Overlap = "overlap", Skip = "skip", Wait = "wait" }
enum Weekday { Sunday = "0", Monday = "1", Tuesday = "2", Wednesday = "3", Thursday = "4", Friday = "5", Saturday = "6" }
const frequencyNames = { get [Frequency.Daily]() { return copy("schedule-creation.daily_b36c26"); }, get [Frequency.Weekdays]() { return copy("schedule-creation.weekdays_6f4b60"); }, get [Frequency.Weekly]() { return copy("schedule-creation.weekly_297513"); }, get [Frequency.Custom]() { return copy("schedule-creation.customCron_202132"); } };
const weekdayNames = { get [Weekday.Sunday]() { return copy("schedule-creation.sunday_873fef"); }, get [Weekday.Monday]() { return copy("schedule-creation.monday_6a00df"); }, get [Weekday.Tuesday]() { return copy("schedule-creation.tuesday_7d8af1"); }, get [Weekday.Wednesday]() { return copy("schedule-creation.wednesday_c0a6cc"); }, get [Weekday.Thursday]() { return copy("schedule-creation.thursday_fc2662"); }, get [Weekday.Friday]() { return copy("schedule-creation.friday_e21f3f"); }, get [Weekday.Saturday]() { return copy("schedule-creation.saturday_dbe35c"); } };
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
  submitBlocked?: boolean;
  localAvailable: boolean;
  selectLocal: () => void;
  submit: () => Promise<void>;
  cancel: () => void;
  references: ReactNode;
  errors: ReactNode;
  retry: ReactNode;
}

export function ScheduleCreation({ definition, change, active, blocked, submitBlocked = false, localAvailable, selectLocal, submit, cancel, references, errors, retry }: ScheduleCreationProps) {
  useLocale();
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
    ? timeInvalid ? copy("schedule-creation.extra.9cd18d06fde1") : copy("schedule-creation.sentence.6caab2b232d7", { v0: frequency === Frequency.Weekly ? copy("schedule-creation.weeklyOn", { day: weekdayNames[weekday] }) : frequencyNames[frequency], v1: time, v2: text(definition.timezone) })
    : copy("schedule-creation.sentence.2a3f85460313", { v0: text(definition.timezone) });
  return <section className="schedule-creation"><form onSubmit={(event) => { event.preventDefault(); if (!blocked && !submitBlocked && !timeInvalid) void submit(); }}>
    <div className="schedule-creation-scroll">
      <header className="schedule-creation-header"><h2>{copy("schedule-creation.newSchedule_3bfe90")}</h2><p>{copy("schedule-creation.setUpARecurringTaskFor_a7ff02")}</p></header>
      <fieldset className="schedule-creation-grid" disabled={blocked}>
        <section className="schedule-creation-card schedule-creation-task" aria-labelledby={`${radios}-task`}>
          <h3 id={`${radios}-task`}>{copy("schedule-creation.task_4bc74b")}</h3><p>{copy("schedule-creation.scheduleNameProjectAndScheduledPrompt_26bf44")}</p>
          <label>{copy("schedule-creation.scheduleName_60918e")}<input ref={name} required maxLength={256} placeholder={copy("schedule-creation.eGWeekdayCodeReview_9d7f64")} value={text(definition.name)} onChange={(event) => field("name")(event.target.value)} /></label>
          <ResourceChoice label={copy("schedule-creation.project_985959")} kind={EntityKind.PROJECT} value={text(definition.project_id)} active={active} required showStatus change={(project_id) => change({ ...definition, project_id, starting: [] })} />
          <label>{copy("schedule-creation.scheduledPrompt_209d2b")}<textarea required rows={3} maxLength={262144} placeholder={copy("schedule-creation.describeWhatTheAgentShouldDo_08ee20")} value={text(definition.prompt)} onChange={(event) => field("prompt")(event.target.value)} /></label>
        </section>
        <section className="schedule-creation-card schedule-creation-execution" aria-labelledby={`${radios}-execution`}>
          <h3 id={`${radios}-execution`}>{copy("schedule-creation.execution_a45cd4")}</h3><p>{copy("schedule-creation.agentWorkerAndRunnerDeviceAre_32fadd")}</p>
          <div className="schedule-creation-selectors">
            <ResourceChoice label={copy("schedule-creation.agentWorker_a4caa7")} kind={EntityKind.AGENT} value={text(definition.agent_id)} active={active} required showStatus change={field("agent_id")} />
            <ResourceChoice label={copy("schedule-creation.runnerDevice_37efe3")} kind={EntityKind.MACHINE} value={text(definition.machine_id)} active={active} disabled={local} required showStatus change={field("machine_id")} />
          </div>
          <fieldset className="schedule-creation-radio-group"><legend>{copy("schedule-creation.workspace_87bb59")}</legend><div className="schedule-creation-radio-cards">
            <label><input type="radio" name={`${radios}-workspace`} checked={!local} onChange={() => field("workspace")(Workspace.Worktree)} /><span>{copy("schedule-creation.worktree_c893ba")}</span></label>
            <label><input type="radio" name={`${radios}-workspace`} checked={local} disabled={!localAvailable} onChange={selectLocal} /><span>{copy("schedule-creation.localComputer_09d55f")}</span></label>
          </div></fieldset>
          <fieldset className="schedule-creation-radio-group"><legend>{copy("schedule-creation.executionMode_c21e7c")}</legend><div className="schedule-creation-radio-cards">
            <label><input type="radio" name={`${radios}-mode`} checked={definition.mode === Mode.Execute} onChange={() => field("mode")(Mode.Execute)} /><span>{copy("schedule-creation.execute_e3a67d")}</span></label>
            <label><input type="radio" name={`${radios}-mode`} checked={definition.mode === Mode.Plan} onChange={() => field("mode")(Mode.Plan)} /><span>{copy("schedule-creation.plan_fa8ed0")}</span></label>
          </div></fieldset>
          {local ? <p>{copy("schedule-creation.localComputerOriginatingWorkerSelectedExisting_b1be46")}</p> : <div className="schedule-creation-disclosure">
            <DisclosureButton density={DisclosureDensity.Settings} type="button" aria-expanded={expanded} aria-controls={disclosure} onClick={() => setExpanded(!expanded)}><span>{copy("schedule-creation.startingReferenceOverrides_58881e")}</span><small>{items(definition.starting).length ? copy("schedule-creation.override_ed569d", { v0: items(definition.starting).length, v1: items(definition.starting).length === 1 ? "" : copy("schedule-creation.s_043a71") }) : copy("schedule-creation.usingSavedProjectReferences_48868d")}</small></DisclosureButton>
            <DisclosureContent id={disclosure} hidden={!expanded} onInvalidCapture={(event) => { if (!expanded) { invalidReference.current ??= event.target as HTMLElement; setExpanded(true); } }}>{references}</DisclosureContent>
          </div>}
        </section>
        <section className="schedule-creation-card schedule-creation-repeat" aria-labelledby={`${radios}-repeat`}>
          <h3 id={`${radios}-repeat`}>{copy("schedule-creation.repeatSchedule_f77b95")}</h3>
          <label>{copy("schedule-creation.frequency_16b666")}<select value={frequency} onChange={(event) => chooseFrequency(event.target.value as Frequency)}>{Object.values(Frequency).map((value) => <option key={value} value={value}>{frequencyNames[value]}</option>)}</select></label>
          {preset ? <><label>{copy("schedule-creation.time_33b934")}<input type="time" required step={60} value={time} aria-invalid={timeInvalid} aria-describedby={timeInvalid ? `${radios}-time-error` : undefined} onChange={(event) => chooseTime(event.target.value)} /></label>{timeInvalid ? <p id={`${radios}-time-error`} role="alert">{copy("schedule-creation.enterAValidTimeInHh_2374e3")}</p> : null}{frequency === Frequency.Weekly ? <label>{copy("schedule-creation.weekday_6ff819")}<select value={weekday} onChange={(event) => chooseWeekday(event.target.value as Weekday)}>{Object.values(Weekday).map((value) => <option key={value} value={value}>{weekdayNames[value]}</option>)}</select></label> : null}</> : <><TextField label={copy("schedule-creation.cronExpression_9e6e7d")} value={definition.cron} required max={512} change={field("cron")} /><p>{copy("schedule-creation.fiveFieldsMinuteHourDayOf_8fec47")}</p></>}
          <TextField label={copy("schedule-creation.ianaTimezone_37cf56")} value={definition.timezone} required change={field("timezone")} />
          <div className="schedule-creation-preview" role="status"><p>{summary}</p>{!timeInvalid ? <code>{text(definition.cron)}</code> : null}<p>{copy("schedule-creation.nextRunIsCalculatedByThe_a9cb90")}</p></div>
          <label>{copy("schedule-creation.whenAPreviousOccurrenceIsStill_851448")}<select value={text(definition.overlap)} onChange={(event) => field("overlap")(event.target.value)}><option value={Overlap.Overlap}>{copy("schedule-creation.overlapIndependentSessions_df3095")}</option><option value={Overlap.Skip}>{copy("schedule-creation.skipTheNewOccurrence_e30b7e")}</option><option value={Overlap.Wait}>{copy("schedule-creation.waitInFifoOrderForConfirmed_f1ce8e")}</option></select></label>
          <label className="checkbox"><input type="checkbox" checked={definition.enabled === true} onChange={(event) => field("enabled")(event.target.checked)} />{copy("schedule-creation.enableFutureScheduledRuns_d1eab6")}</label>
          <p>{copy("schedule-creation.schedulesArePausedOnCreationUnless_4c9799")}</p><p>{copy("schedule-creation.theServerContinuesSchedulingWhenThe_116ec3")}</p>
        </section>
      </fieldset>
      <div className="schedule-creation-errors">{errors}</div>
    </div>
    <footer className="schedule-creation-footer"><div className="schedule-creation-footer-inner">
      <p>{definition.enabled === true ? copy("schedule-creation.enabledOnCreation_f6e986") : copy("schedule-creation.pausedOnCreation_484218")}<small>{local ? copy("schedule-creation.localComputer_09d55f") : copy("schedule-creation.worktree_c893ba")} · {definition.mode === Mode.Plan ? copy("schedule-creation.plan_fa8ed0") : copy("schedule-creation.execute_e3a67d")}</small></p>
      <div className="actions">{retry}<button type="button" disabled={blocked} onClick={cancel}>{copy("schedule-creation.cancel_19766e")}</button><button className="primary" disabled={blocked || submitBlocked || timeInvalid}>{copy("schedule-creation.createSchedule_5b08f3")}</button></div>
    </div></footer>
  </form></section>;
}
