// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useMemo, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { ScheduleDayMatch, ScheduleQuery, type PreviewScheduleCalendarResponse, type ScheduleCalendar } from "@delinoio/delidev-api-client";
import { copy, displayLocale, useLocale } from "./localization";
import { useDateFormat } from "./date-format";
import { DateFormatPreference, formatTimestampLabel, TimestampMode } from "./timestamp-format";
import { DisclosureButton, DisclosureContent, DisclosureDensity } from "./disclosure";

const full = (values: number[], first: number, last: number) => values.length === last - first + 1 && values.every((value, index) => value === first + index);
const sorted = (values: number[], first: number, last: number) => values.length > 0 && values.length <= last-first+1 && values.every((value, index) => Number.isInteger(value) && value >= first && value <= last && (index === 0 || value > values[index-1]!));
export function validCalendar(value: ScheduleCalendar | undefined): value is ScheduleCalendar {
  return Boolean(value && sorted(value.minutes,0,59) && sorted(value.hours,0,23) && sorted(value.daysOfMonth,1,31) && sorted(value.months,1,12) && sorted(value.weekdays,0,6) && [ScheduleDayMatch.AND,ScheduleDayMatch.OR].includes(value.dayMatch));
}
const join = (values: string[]) => new Intl.ListFormat(displayLocale(), { style: "long", type: "conjunction" }).format(values);
const weekday = (day: number) => new Intl.DateTimeFormat(displayLocale(), { weekday: "long", timeZone: "UTC" }).format(new Date(Date.UTC(2026,0,4+day)));
const month = (value: number) => new Intl.DateTimeFormat(displayLocale(), { month: "long", timeZone: "UTC" }).format(new Date(Date.UTC(2026,value-1,1)));

/** Render only server-normalized constraints. No cron parsing or calendar search. */
export function calendarSentence(calendar: ScheduleCalendar): string {
  const { minutes, hours, daysOfMonth: days, months, weekdays, dayMatch } = calendar;
  const allDays=full(days,1,31), allWeek=full(weekdays,0,6), allMonths=full(months,1,12);
  if (minutes.length===1 && hours.length===1 && allMonths && dayMatch===ScheduleDayMatch.AND && allDays) {
    const time = new Intl.DateTimeFormat(displayLocale(), { hour:"numeric", minute:"2-digit", timeZone:"UTC" }).format(new Date(Date.UTC(2026,0,1,hours[0],minutes[0])));
    if (allWeek) return copy("schedule-preview.daily", { time });
    if (weekdays.join(",")==="1,2,3,4,5") return copy("schedule-preview.weekdays", { time });
    return copy("schedule-preview.weekly", { days:join(weekdays.map(weekday)), time });
  }
  const time = copy("schedule-preview.times", { hours:full(hours,0,23)?copy("schedule-preview.everyHour"):hours.join(", "), minutes:full(minutes,0,59)?copy("schedule-preview.everyMinute"):minutes.join(", ") });
  const dayOfMonth=allDays?copy("schedule-preview.everyDayOfMonth"):copy("schedule-preview.daysOfMonth", { days:days.join(", ") });
  const dayOfWeek=allWeek?copy("schedule-preview.everyWeekday"):join(weekdays.map(weekday));
  return copy(dayMatch===ScheduleDayMatch.OR?"schedule-preview.constraintsOr":"schedule-preview.constraintsAnd", { time, months:allMonths?copy("schedule-preview.everyMonth"):join(months.map(month)), dayOfMonth, dayOfWeek });
}

function observation(response: PreviewScheduleCalendarResponse) {
  return { response, received:performance.now() };
}
export function SchedulePreview({ cron, timezone, enabled, connectionReady=true, invalid=false, fallback }: { cron:string; timezone:string; enabled:boolean; connectionReady?:boolean; invalid?:boolean; fallback:string }) {
  useLocale();
  const preference=useDateFormat(), transport=useTransport();
  const [online,setOnline]=useState(() => navigator.onLine);
  // A disconnected generation cannot publish when the same original transport
  // reconnects. Query identity changes, while calls still use that exact server.
  const readTransport=useMemo(() => ({ unary:transport.unary.bind(transport), stream:transport.stream.bind(transport) }),[transport,connectionReady,online]);
  const [debounced,setDebounced]=useState<{cron:string;timezone:string;transport:typeof transport}>();
  const [tick,setTick]=useState(0), [expanded,setExpanded]=useState(false);
  const disclosure=useId();
  const valid=!invalid && cron.trim().length>0 && cron.length<=512 && timezone.trim().length>0 && timezone.length<=256;
  useEffect(() => { const timer=setTimeout(() => setDebounced({cron,timezone,transport:readTransport}),300); return () => clearTimeout(timer); },[cron,timezone,readTransport]);
  useEffect(() => {
    const changed=() => setOnline(navigator.onLine);
    window.addEventListener("online",changed); window.addEventListener("offline",changed);
    return () => { window.removeEventListener("online",changed); window.removeEventListener("offline",changed); };
  },[]);
  const current=valid && connectionReady && online && debounced?.cron===cron && debounced.timezone===timezone && debounced.transport===readTransport;
  const query=useQuery(ScheduleQuery.previewScheduleCalendar, {cron,timezone}, {
    transport:readTransport, enabled:current, retry:false, staleTime:0, gcTime:0, refetchOnMount:"always", refetchOnWindowFocus:"always", refetchOnReconnect:"always", select:observation,
  });
  const data=current && !query.isFetching && !query.isError ? query.data : undefined;
  let boundary=NaN, next=NaN;
  if(data?.response.calculatedAt && data.response.nextRunAt) {
    boundary=timestampDate(data.response.calculatedAt).getTime(); next=timestampDate(data.response.nextRunAt).getTime();
  }
  const now=data?boundary+Math.max(0,performance.now()-data.received):NaN;
  const fresh=Boolean(data && validCalendar(data.response.calendar) && Number.isFinite(boundary) && Number.isFinite(next) && next>boundary && next>now);
  useEffect(() => {
    if (!current || query.isFetching) return;
    // Refresh both the wording and the server observation at expiry or one
    // minute. No second-by-second live-region announcements are produced.
    const remaining=data && next>boundary?next-(boundary+Math.max(0,performance.now()-data.received)):60000;
    const timer=setTimeout(() => { setTick(value=>value+1); void query.refetch(); },Math.max(1,Math.min(60000,Number.isFinite(remaining)?remaining:60000)));
    return () => clearTimeout(timer);
  },[current,query.isFetching,query.refetch,data,next,boundary,tick]);
  let status=copy("schedule-preview.calculating");
  if (!valid || ConnectError.from(query.error).code===Code.InvalidArgument) status=copy("schedule-preview.invalid");
  else if (!connectionReady || !online || query.isError || (current && !query.isPending && !query.isFetching && !fresh)) status=copy(ConnectError.from(query.error).code===Code.Unimplemented?"schedule-preview.unsupported":"schedule-preview.unavailable");
  let date="", relative="";
  if(fresh) {
    try {
      const instant=new Date(next);
      date=preference===DateFormatPreference.System
        ?new Intl.DateTimeFormat(displayLocale(),{timeZone:timezone,weekday:"long",year:"numeric",month:"long",day:"numeric",hour:"numeric",minute:"2-digit"}).format(instant)
        :`${new Intl.DateTimeFormat(displayLocale(),{timeZone:timezone,weekday:"long"}).format(instant)}, ${formatTimestampLabel(instant.toISOString(),{preference,mode:TimestampMode.Absolute,timeZone:timezone})}`;
      const delta=next-now, unit=delta>=86400000?"day":delta>=3600000?"hour":"minute";
      relative=new Intl.RelativeTimeFormat(displayLocale(),{numeric:"always"}).format(Math.max(1,Math.round(delta/(unit==="day"?86400000:unit==="hour"?3600000:60000))),unit);
    } catch { status=copy("schedule-preview.unavailable"); }
  }
  return <div className="schedule-creation-preview schedule-calendar-preview">
    <dl><dt>{copy("schedule-preview.repeats")}</dt><dd>{fresh?calendarSentence(data!.response.calendar!):fallback}<small>{timezone}</small></dd>
      <dt>{copy("schedule-preview.starts")}</dt><dd>{copy(enabled?"schedule-preview.afterSave":"schedule-preview.afterEnable")}</dd>
      <dt>{copy("schedule-preview.first")}</dt><dd role="status" aria-live="polite">{!enabled?<>{copy("schedule-preview.paused")}{!fresh?<small>{status}</small>:null}</>:date?<><span>{date}</span><small>{timezone} · {relative}</small></>:status}</dd></dl>
    <p>{copy("schedule-preview.provisional")}</p>
    {!invalid ? <><DisclosureButton type="button" density={DisclosureDensity.Settings} aria-expanded={expanded} aria-controls={disclosure} onClick={()=>setExpanded(!expanded)}>{copy("schedule-preview.details")}</DisclosureButton>
    <DisclosureContent id={disclosure} hidden={!expanded}><code>{cron}</code></DisclosureContent></> : null}
  </div>;
}
